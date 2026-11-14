// Package testutil provides isolated integration fixtures, never runtime code.
package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/storageprofile"
)

const SeaweedImage = "chrislusf/seaweedfs:4.53@sha256:ccad23e18639c8b98cee17e7100b95830a06077b56d8e5b006d6dcf8ec5e94a0"

type Storage struct {
	Container, Volume, Endpoint, AccessKey, SecretKey string
	t                                                 testing.TB
}

func Docker(t testing.TB, input string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", args[0], err, out)
	}
	return strings.TrimSpace(string(out))
}

func StartStorage(t testing.TB) *Storage {
	t.Helper()
	if os.Getenv("TAG_FILES_INTEGRATION") != "1" {
		t.Skip("set TAG_FILES_INTEGRATION=1 to run isolated Docker storage integration")
	}
	name := "tag-files-test-" + domain.NewUUID()
	f := &Storage{Volume: name, AccessKey: domain.NewUUID(), SecretKey: domain.NewUUID() + domain.NewUUID(), t: t}
	Docker(t, "", "volume", "create", f.Volume)
	t.Cleanup(func() { Docker(t, "", "volume", "rm", f.Volume) })
	f.Container = Docker(t, "", "create", "--name", name, "--mount", "type=volume,src="+f.Volume+",dst=/data", "-p", "127.0.0.1::8333", "--entrypoint", "/bin/sh", SeaweedImage,
		"-ec", `mkdir -p /data/master; chown seaweed:seaweed /data/master /etc/seaweedfs/tag-files-s3.json; exec /entrypoint.sh "$@"`, "tag-files-test",
		"mini", "-dir=/data", "-master.dir=/data/master", "-ip=127.0.0.1", "-ip.bind=0.0.0.0", "-s3.config=/etc/seaweedfs/tag-files-s3.json", "-bucket=tag-files", "-s3.autoCreateBucket=false", "-s3.iam.readOnly=true", "-webdav=false", "-admin.ui=false", "-master.telemetry=false", "-master.defaultReplication=000", "-filer.defaultReplicaPlacement=000", "-volume.max=8")
	t.Cleanup(func() { Docker(t, "", "rm", "-f", f.Container) })
	dir := t.TempDir()
	config := map[string]any{"identities": []any{map[string]any{"name": "isolated-test", "credentials": []any{map[string]string{"accessKey": f.AccessKey, "secretKey": f.SecretKey}}, "actions": []string{"Admin", "Read", "Write", "List", "Tagging"}}}}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(config); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s3.json")
	if err := os.WriteFile(path, buf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	Docker(t, "", "cp", path, f.Container+":/etc/seaweedfs/tag-files-s3.json")
	Docker(t, "", "start", f.Container)
	f.waitReady()
	f.Shell("fs.configure -locationPrefix=/ -fsync=true -apply")
	f.AssertGlobalFsync()
	f.PublicAccessBlock(true)
	return f
}

func (f *Storage) S3() *s3.Client {
	return s3.NewFromConfig(aws.Config{Region: "us-east-1", HTTPClient: &http.Client{Timeout: 5 * time.Second}, Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: f.AccessKey, SecretAccessKey: f.SecretKey}, nil
	})}, func(o *s3.Options) { o.BaseEndpoint = aws.String(f.Endpoint); o.UsePathStyle = true })
}

func (f *Storage) PublicAccessBlock(enabled bool) {
	f.t.Helper()
	_, err := f.S3().PutPublicAccessBlock(context.Background(), &s3.PutPublicAccessBlockInput{Bucket: aws.String("tag-files"), PublicAccessBlockConfiguration: &types.PublicAccessBlockConfiguration{BlockPublicAcls: aws.Bool(enabled), IgnorePublicAcls: aws.Bool(enabled), BlockPublicPolicy: aws.Bool(enabled), RestrictPublicBuckets: aws.Bool(enabled)}})
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *Storage) AssertGlobalFsync() {
	f.t.Helper()
	raw := f.Shell("fs.configure")
	if err := storageprofile.ValidateRules([]byte(raw)); err != nil {
		f.t.Fatalf("unsafe persisted storage configuration: %v", err)
	}
}

func (f *Storage) Shell(command string) string {
	f.t.Helper()
	return Docker(f.t, command+"\n", "exec", "-i", f.Container, "weed", "shell", "-master=127.0.0.1:9333", "-filer=127.0.0.1:8888", "-connectTimeout=5s")
}

func (f *Storage) Restart() {
	Docker(f.t, "", "restart", "--time", "5", f.Container)
	f.waitReady()
}

func (f *Storage) waitReady() {
	f.t.Helper()
	address := Docker(f.t, "", "port", f.Container, "8333/tcp")
	f.Endpoint = "http://" + address
	client := &http.Client{Timeout: time.Second}
	signer := v4.NewSigner()
	deadline := time.Now().Add(35 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("HEAD", f.Endpoint+"/tag-files", nil)
		if err := signer.SignHTTP(context.Background(), aws.Credentials{AccessKeyID: f.AccessKey, SecretAccessKey: f.SecretKey}, req, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "s3", "us-east-1", time.Now()); err != nil {
			f.t.Fatal(err)
		}
		res, err := client.Do(req)
		if err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	logs := Docker(f.t, "", "logs", "--tail", "60", f.Container)
	f.t.Fatal(fmt.Sprintf("private S3 did not become ready: %s\n%s", f.Endpoint, logs))
}
