package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testImage = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func fixture(t *testing.T) (options, []container) {
	t.Helper()
	root := t.TempDir()
	deployment := filepath.Join(root, "old")
	os.Mkdir(deployment, 0700)
	os.WriteFile(filepath.Join(deployment, "compose.yml"), []byte("services: {}\n"), 0600)
	state := filepath.Join(root, "new.migration")
	os.Mkdir(state, 0700)
	o := options{State: state, Deployment: deployment, SourceDirectory: "/srv/old", TargetDirectory: "/srv/new", Version: "v1.2.3", AppImage: testImage, StorageImage: testImage, MigrationImage: testImage, Project: "migrate-fixture", Port: 8080}
	labels := map[string]string{"com.docker.compose.project": "old", "com.docker.compose.project.working_dir": "/srv/old"}
	app := container{ID: "app-id", Image: testImage, Name: "/tag-files-service"}
	app.Config.Env = []string{"ConnectionStrings__SQLite=Data Source=/app/data/tag-files.db", "S3__Url=minio:9000", "S3__Username=admin", "S3__Password=private-password"}
	app.Config.Labels = labels
	app.Mounts = []mount{{Type: "bind", Source: "/srv/old/data", Destination: "/app/data", RW: true}}
	storage := container{ID: "minio-id", Image: testImage, Name: "/minio"}
	storage.Config.Env = []string{"MINIO_ROOT_USER=admin", "MINIO_ROOT_PASSWORD=private-password"}
	storage.Config.Cmd = []string{"server", "--console-address", ":9001", "/data"}
	storage.Config.Labels = labels
	storage.Mounts = []mount{{Type: "bind", Source: "/srv/old/data/minio_data", Destination: "/data", RW: true}}
	rows := []container{app, storage}
	o.AvailableBytes = func(string) (int64, error) { return 1 << 50, nil }
	return o, rows
}
func inspectFixture(t *testing.T, o *options, rows []container) {
	t.Helper()
	o.Inspect = filepath.Join(o.State, "docker-inspect.json")
	b, _ := json.Marshal(rows)
	if e := os.WriteFile(o.Inspect, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestDiscoveryPreservesNestedSourceAndSecretsWithoutExecutableProtocol(t *testing.T) {
	o, rows := fixture(t)
	inspectFixture(t, &o, rows)
	if e := discover(o); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(o.State, "host.env"))
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), "DB_MOUNT_SOURCE=/srv/old/data\n") || !strings.Contains(string(b), "SOURCE_DB_RELATIVE=tag-files.db\n") {
		t.Fatalf("bad host protocol %s", b)
	}
	if strings.Contains(string(b), "private-password") {
		t.Fatal("secret in host protocol")
	}
	for _, name := range []string{"source-secrets/source_access", "source-secrets/source_secret", "source-minio.env"} {
		i, e := os.Stat(filepath.Join(o.State, name))
		if e != nil || i.Mode().Perm() != 0600 {
			t.Fatalf("nonprivate secret %s %v", name, e)
		}
	}
	if e := discover(o); e != nil {
		t.Fatalf("unchanged retry: %v", e)
	}
	rows[0].Config.Env[3] = "S3__Password=changed"
	inspectFixture(t, &o, rows)
	if e := discover(o); e == nil {
		t.Fatal("changed source was accepted on retry")
	}
}
func TestDiscoveryRejectsWritersAmbiguityAndOverlap(t *testing.T) {
	for _, name := range []string{"writer", "paused", "duplicate", "target-overlap", "storage-db-overlap", "unsafe-mount", "unknown-mount"} {
		t.Run(name, func(t *testing.T) {
			o, rows := fixture(t)
			switch name {
			case "writer", "paused":
				r := container{ID: "other"}
				r.State.Running = name == "writer"
				r.State.Paused = name == "paused"
				r.Mounts = []mount{{Type: "bind", Source: "/srv/old/data", Destination: "/different"}}
				rows = append(rows, r)
			case "duplicate":
				rows = append(rows, rows[0])
			case "target-overlap":
				o.TargetDirectory = "/srv/old/data/new"
			case "storage-db-overlap":
				rows[1].Mounts[0].Source = "/srv/old/data"
			case "unsafe-mount":
				rows[0].Mounts[0].Source = "/srv/old/data,invalid"
			case "unknown-mount":
				rows[0].Mounts = append(rows[0].Mounts, mount{Type: "bind", Source: "/other", Destination: "/other", RW: true})
			}
			inspectFixture(t, &o, rows)
			if e := discover(o); e == nil {
				t.Fatal("unsafe source accepted")
			}
		})
	}
}
func TestColdRollbackVerifiedCloneAndRetry(t *testing.T) {
	o, rows := fixture(t)
	inspectFixture(t, &o, rows)
	if e := discover(o); e != nil {
		t.Fatal(e)
	}
	o.Source = filepath.Join(t.TempDir(), "db")
	o.SourceStorage = filepath.Join(t.TempDir(), "storage")
	os.Mkdir(o.Source, 0700)
	os.Mkdir(o.SourceStorage, 0700)
	for name, data := range map[string]string{"tag-files.db": "sqlite bytes", "tag-files.db-wal": "wal bytes", "extra": "retained extra file"} {
		os.WriteFile(filepath.Join(o.Source, name), []byte(data), 0600)
	}
	os.Mkdir(filepath.Join(o.SourceStorage, "bucket"), 0700)
	os.WriteFile(filepath.Join(o.SourceStorage, "bucket", "object"), []byte("object bytes"), 0600)
	if e := prepare(o); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(o.State, "rollback", "complete")); e != nil {
		t.Fatal(e)
	}
	clone := filepath.Join(o.State, "source-minio", "bucket", "object")
	os.WriteFile(clone, []byte("runtime changed clone"), 0600)
	if e := prepare(o); e != nil {
		t.Fatalf("verified retry: %v", e)
	}
	b, _ := os.ReadFile(clone)
	if string(b) != "runtime changed clone" {
		t.Fatal("retry overwrote mutable serving clone")
	}
	original, _ := os.ReadFile(filepath.Join(o.SourceStorage, "bucket", "object"))
	if string(original) != "object bytes" {
		t.Fatal("source changed")
	}
	os.WriteFile(filepath.Join(o.State, "rollback", "storage", "bucket", "object"), []byte("tampered"), 0600)
	if e := prepare(o); e == nil {
		t.Fatal("tampered rollback accepted")
	}
}
func TestColdRollbackRejectsChangedSourceAndSymlinks(t *testing.T) {
	for _, name := range []string{"changed", "symlink", "space"} {
		t.Run(name, func(t *testing.T) {
			o, rows := fixture(t)
			inspectFixture(t, &o, rows)
			if e := discover(o); e != nil {
				t.Fatal(e)
			}
			o.Source = t.TempDir()
			o.SourceStorage = t.TempDir()
			os.WriteFile(filepath.Join(o.Source, "tag-files.db"), []byte("sqlite"), 0600)
			os.WriteFile(filepath.Join(o.SourceStorage, "object"), []byte("object"), 0600)
			if name == "space" {
				o.AvailableBytes = func(string) (int64, error) { return 1, nil }
			} else if name == "symlink" {
				os.Symlink("/outside", filepath.Join(o.SourceStorage, "bad"))
			} else {
				if e := prepare(o); e != nil {
					t.Fatal(e)
				}
				os.WriteFile(filepath.Join(o.Source, "tag-files.db"), []byte("changed"), 0600)
			}
			if e := prepare(o); e == nil {
				t.Fatal("unsafe rollback accepted")
			}
		})
	}
}

func TestDiscoveryRejectsUnrelatedProjectAndRunningUnverifiedTarget(t *testing.T) {
	for _, name := range []string{"unrelated", "running-app"} {
		t.Run(name, func(t *testing.T) {
			o, rows := fixture(t)
			target := container{ID: "target"}
			target.Config.Labels = map[string]string{"com.docker.compose.project": o.Project, "com.docker.compose.service": "app"}
			target.Mounts = []mount{{Type: "bind", Source: "/unrelated/data", Destination: "/data"}}
			if name == "running-app" {
				if e := discoverFixture(t, &o, rows); e != nil {
					t.Fatal(e)
				}
				target.Mounts[0].Source = o.TargetDirectory + "/data/metadata"
				target.State.Running = true
			}
			rows = append(rows, target)
			inspectFixture(t, &o, rows)
			if e := discover(o); e == nil {
				t.Fatal("unrelated or running target accepted")
			}
		})
	}
}
func discoverFixture(t *testing.T, o *options, rows []container) error {
	t.Helper()
	inspectFixture(t, o, rows)
	return discover(*o)
}
func TestTargetConfigurationRequiresPreparedInstallationAndRetainsGeneratedConfig(t *testing.T) {
	o, rows := fixture(t)
	if e := discoverFixture(t, &o, rows); e != nil {
		t.Fatal(e)
	}
	o.Target = t.TempDir()
	if e := configureTarget(o); e == nil {
		t.Fatal("unprepared target accepted")
	}
	prepareTargetFixture(t, o)
	if e := configureTarget(o); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(o.State, "config.json"))
	if e != nil {
		t.Fatal(e)
	}
	var cfg map[string]any
	json.Unmarshal(b, &cfg)
	if cfg["Archive"] != "/work/archive/run-001" {
		t.Fatalf("bad archive %v", cfg["Archive"])
	}
	changed := []byte("{\"generated\":true}\n")
	os.WriteFile(filepath.Join(o.State, "config.json"), changed, 0600)
	if e = configureTarget(o); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(filepath.Join(o.State, "config.json"))
	if string(after) != string(changed) {
		t.Fatal("retry overwrote generated configuration")
	}
	if e = finalize(o); e == nil {
		t.Fatal("finalized without complete verification")
	}
	os.Mkdir(filepath.Join(o.State, "journal"), 0700)
	os.WriteFile(filepath.Join(o.State, "journal", "automatic-result.json"), []byte(`{"complete":true}`), 0600)
	if e = finalize(o); e != nil {
		t.Fatal(e)
	}
	if e = completed(o); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(o.Target, ".env"), []byte("changed"), 0600)
	if e = configureTarget(o); e == nil {
		t.Fatal("changed target accepted")
	}
	if e = completed(o); e == nil {
		t.Fatal("changed completed target accepted")
	}
}
func prepareTargetFixture(t *testing.T, o options) {
	t.Helper()
	for _, name := range []string{"secrets", "data/metadata", "data/media", "scratch"} {
		if e := os.MkdirAll(filepath.Join(o.Target, name), 0700); e != nil {
			t.Fatal(e)
		}
	}
	receipt := map[string]any{"format": 1, "state": "prepared", "version": o.Version, "project": o.Project, "port": o.Port, "images": map[string]any{"app": map[string]string{"Id": o.AppImage}, "storage": map[string]string{"Id": o.StorageImage}}}
	b, _ := json.Marshal(receipt)
	for name, data := range map[string][]byte{"installation.json": b, "compose.yml": []byte("services: {}"), ".env": []byte("COMPOSE_PROJECT_NAME=" + o.Project + "\n"), "secrets/s3_access_key": []byte("target-access\n"), "secrets/s3_secret_key": []byte("target-secret\n")} {
		if e := os.WriteFile(filepath.Join(o.Target, name), data, 0600); e != nil {
			t.Fatal(e)
		}
	}
}

func TestSetupStagesRetainIncompleteAttemptsAndPublishOnlyIntoEmptyTarget(t *testing.T) {
	o, rows := fixture(t)
	if e := discoverFixture(t, &o, rows); e != nil {
		t.Fatal(e)
	}
	o.Parent = filepath.Dir(o.State)
	if e := setupStage(o); e != nil {
		t.Fatal(e)
	}
	first, _ := os.ReadFile(filepath.Join(o.State, "setup-stage.env"))
	o.Stage = strings.TrimSpace(strings.TrimPrefix(string(first), "SETUP_STAGE="))
	firstPath := filepath.Join(o.State, o.Stage)
	os.WriteFile(filepath.Join(firstPath, ".setup-incomplete"), []byte("incomplete"), 0600)
	if e := setupStage(o); e != nil {
		t.Fatal(e)
	}
	second, _ := os.ReadFile(filepath.Join(o.State, "setup-stage.env"))
	if string(first) == string(second) {
		t.Fatal("incomplete attempt reused")
	}
	if _, e := os.Stat(firstPath); e != nil {
		t.Fatal("incomplete stage removed")
	}
	o.Stage = strings.TrimSpace(strings.TrimPrefix(string(second), "SETUP_STAGE="))
	stageOptions := o
	stageOptions.Target = filepath.Join(o.State, o.Stage)
	prepareTargetFixture(t, stageOptions)
	target := filepath.Join(o.Parent, "new")
	os.Mkdir(target, 0700)
	os.WriteFile(filepath.Join(target, "sentinel"), []byte("keep"), 0600)
	if e := publishTarget(o); e == nil {
		t.Fatal("unrelated target replaced")
	}
	b, _ := os.ReadFile(filepath.Join(target, "sentinel"))
	if string(b) != "keep" {
		t.Fatal("unrelated target changed")
	}
	os.Remove(filepath.Join(target, "sentinel"))
	if e := publishTarget(o); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(target, "installation.json")); e != nil {
		t.Fatal(e)
	}
}
func TestNamedVolumeDiscoveryRejectsActiveBindAlias(t *testing.T) {
	o, rows := fixture(t)
	rows[0].Mounts[0] = mount{Type: "volume", Name: "old_database", Source: "/var/lib/docker/volumes/old_database/_data", Destination: "/app/data", RW: true}
	rows[1].Mounts[0] = mount{Type: "volume", Name: "old_minio", Source: "/var/lib/docker/volumes/old_minio/_data", Destination: "/data", RW: true}
	if e := discoverFixture(t, &o, rows); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(o.State, "host.env"))
	if !strings.Contains(string(b), "DB_MOUNT_SOURCE=old_database\n") {
		t.Fatal("native volume name not selected")
	}
	writer := container{ID: "writer"}
	writer.State.Running = true
	writer.Mounts = []mount{{Type: "bind", Source: "/var/lib/docker/volumes/old_minio/_data/bucket", Destination: "/alias"}}
	rows = append(rows, writer)
	inspectFixture(t, &o, rows)
	if e := discover(o); e == nil {
		t.Fatal("active bind alias accepted")
	}
}
func TestRollbackEffectiveMetadataAndSourceConfigAreVerified(t *testing.T) {
	for _, name := range []string{"containers.json", "operation.json", "changed-config"} {
		t.Run(name, func(t *testing.T) {
			o, rows := fixture(t)
			if e := discoverFixture(t, &o, rows); e != nil {
				t.Fatal(e)
			}
			o.Source = t.TempDir()
			o.SourceStorage = t.TempDir()
			os.WriteFile(filepath.Join(o.Source, "tag-files.db"), []byte("sqlite"), 0600)
			os.WriteFile(filepath.Join(o.SourceStorage, "object"), []byte("object"), 0600)
			if name == "changed-config" {
				os.WriteFile(filepath.Join(o.Deployment, "compose.yml"), []byte("changed"), 0600)
				if e := prepare(o); e == nil {
					t.Fatal("changed source configuration accepted")
				}
				return
			}
			if e := prepare(o); e != nil {
				t.Fatal(e)
			}
			os.WriteFile(filepath.Join(o.State, "rollback", name), []byte("changed credentials or settings"), 0600)
			if e := prepare(o); e == nil {
				t.Fatal("tampered effective metadata accepted")
			}
		})
	}
}

func TestDiscoveryRejectsActiveTargetAndStateAliasesOutsideComposeProject(t *testing.T) {
	for _, suffix := range []string{"/data/metadata", ".migration", "/data/media"} {
		t.Run(suffix, func(t *testing.T) {
			o, rows := fixture(t)
			r := container{ID: "unrelated"}
			r.State.Running = true
			r.Mounts = []mount{{Type: "bind", Source: o.TargetDirectory + suffix, Destination: "/alias", RW: true}}
			rows = append(rows, r)
			inspectFixture(t, &o, rows)
			if e := discover(o); e == nil {
				t.Fatal("active unrelated target or state writer accepted")
			}
		})
	}
}

func TestColdRollbackPreservesEmptyBucketDirectories(t *testing.T) {
	o, rows := fixture(t)
	if e := discoverFixture(t, &o, rows); e != nil {
		t.Fatal(e)
	}
	o.Source = t.TempDir()
	o.SourceStorage = t.TempDir()
	os.WriteFile(filepath.Join(o.Source, "tag-files.db"), []byte("sqlite"), 0600)
	os.Mkdir(filepath.Join(o.SourceStorage, "empty-bucket"), 0750)
	if e := prepare(o); e != nil {
		t.Fatal(e)
	}
	for _, root := range []string{"rollback/storage", "source-minio"} {
		i, e := os.Stat(filepath.Join(o.State, root, "empty-bucket"))
		if e != nil || !i.IsDir() || i.Mode().Perm() != 0750 {
			t.Fatalf("empty native bucket missing or changed in %s: %v", root, e)
		}
	}
}
