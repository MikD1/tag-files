package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDaemonMountIdentityDetectsSymlinkAliasesAndTargetAliases(t *testing.T) {
	plan := inspectionPlan{InputSHA256: "input", TargetLeaf: "new", Mounts: []inspectionMount{{Index: 0, Type: "bind", Source: "/srv/data"}, {Index: 1, Type: "bind", Source: "/different/symlink"}, {Index: 2, Type: "bind", Source: "/alias/child"}}}
	info := strings.NewReader("24 1 8:1 /real/data /inspection/mount-0 ro - ext4 /dev/sda ro\n25 1 8:1 /real/data /inspection/mount-1 ro - ext4 /dev/sda ro\n26 1 8:1 /real/data/child /inspection/mount-2 ro - ext4 /dev/sda ro\n27 1 8:1 /real /inspection/target-parent ro - ext4 /dev/sda ro\n")
	result, e := canonicalInspection(plan, info)
	if e != nil {
		t.Fatal(e)
	}
	if result.Mounts[0].Identity != result.Mounts[1].Identity || !overlap(result.Mounts[0].Identity, result.Mounts[2].Identity) {
		t.Fatal("symlink alias not mapped to same physical tree")
	}
	if result.TargetIdentity != "/@device/8:1/real/new" {
		t.Fatalf("bad target identity %s", result.TargetIdentity)
	}
}
func TestDaemonMountIdentityDecodesEscapedPathsAndSeparatesFilesystems(t *testing.T) {
	plan := inspectionPlan{InputSHA256: "input", TargetLeaf: "new", Mounts: []inspectionMount{{Index: 0, Type: "bind", Source: "/spaces"}, {Index: 1, Type: "bind", Source: "/other"}}}
	result, e := canonicalInspection(plan, strings.NewReader("1 0 8:1 /some\\040space /inspection/mount-0 ro - ext4 /dev/sda ro\n2 0 8:2 /some\\040space /inspection/mount-1 ro - ext4 /dev/sdb ro\n3 0 8:1 /target /inspection/target-parent ro - ext4 /dev/sda ro\n"))
	if e != nil {
		t.Fatal(e)
	}
	if overlap(result.Mounts[0].Identity, result.Mounts[1].Identity) {
		t.Fatal("different filesystems conflated")
	}
	if !strings.Contains(result.Mounts[0].Identity, "some space") {
		t.Fatal("mountinfo path escape not decoded")
	}
}
func TestDiscoveryRejectsUnrelatedSourceS3AndProject(t *testing.T) {
	for _, name := range []string{"external", "different-project", "tls", "wrong-port"} {
		t.Run(name, func(t *testing.T) {
			o, rows := fixture(t)
			switch name {
			case "external":
				rows[0].Config.Env[1] = "S3__Url=other-minio:9000"
			case "different-project":
				labels := map[string]string{}
				for k, v := range rows[1].Config.Labels {
					labels[k] = v
				}
				labels["com.docker.compose.project"] = "another"
				rows[1].Config.Labels = labels
			case "tls":
				rows[0].Config.Env[1] = "S3__Url=https://minio:9000"
			case "wrong-port":
				rows[0].Config.Env[1] = "S3__Url=minio:9010"
			}
			inspectFixture(t, &o, rows)
			if e := discover(o); e == nil {
				t.Fatal("unrelated source endpoint accepted")
			}
		})
	}
}
func TestRawRollbackRetainsFullSourceContainerConfiguration(t *testing.T) {
	o, rows := fixture(t)
	inspectFixture(t, &o, rows)
	b, _ := os.ReadFile(o.Inspect)
	b = []byte(strings.Replace(string(b), `"Id":"app-id"`, `"HostConfig":{"RestartPolicy":{"Name":"always"}},"Id":"app-id"`, 1))
	os.WriteFile(o.Inspect, b, 0600)
	if e := discover(o); e != nil {
		t.Fatal(e)
	}
	o.Source = t.TempDir()
	o.SourceStorage = t.TempDir()
	os.WriteFile(filepath.Join(o.Source, "tag-files.db"), []byte("sqlite"), 0600)
	if e := prepare(o); e != nil {
		t.Fatal(e)
	}
	raw, _ := os.ReadFile(filepath.Join(o.State, "rollback", "containers.json"))
	if !strings.Contains(string(raw), "RestartPolicy") {
		t.Fatal("effective source Docker metadata lost")
	}
}

func TestSourceCorrelationUsesSharedNetworkAliases(t *testing.T) {
	o, rows := fixture(t)
	rows[0].Config.Env[1] = "S3__Url=source-objects:9000"
	rows[0].NetworkSettings.Networks = map[string]networkAttachment{"legacy": {Aliases: []string{"app"}}}
	rows[1].NetworkSettings.Networks = map[string]networkAttachment{"legacy": {Aliases: []string{"source-objects"}}}
	inspectFixture(t, &o, rows)
	if e := discover(o); e != nil {
		t.Fatal(e)
	}
	duplicate := container{ID: "other"}
	duplicate.NetworkSettings.Networks = map[string]networkAttachment{"legacy": {Aliases: []string{"source-objects"}}}
	rows = append(rows, duplicate)
	inspectFixture(t, &o, rows)
	if e := discover(o); e == nil {
		t.Fatal("ambiguous source network alias accepted")
	}
}
