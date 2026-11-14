package storageprofile_test

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/mikd1/tag-files/app/internal/storageprofile"
	"github.com/mikd1/tag-files/app/internal/testutil"
)

func TestStoragePreflightInsidePinnedContainer(t *testing.T) {
	f := testutil.StartStorage(t)
	arch := testutil.Docker(t, "", "image", "inspect", "--format", "{{.Architecture}}", testutil.SeaweedImage)
	file := filepath.Join(t.TempDir(), "storage-check")
	build := exec.Command("go", "build", "-o", file, "./cmd/storage-check")
	build.Dir = filepath.Join("..", "..")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build preflight: %v\n%s", err, output)
	}
	testutil.Docker(t, "", "cp", file, f.Container+":/usr/local/bin/storage-check")
	check := func(wantHealthy bool) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "docker", "exec", f.Container, "/usr/local/bin/storage-check", "-timeout=8s", "-s3-config=/etc/seaweedfs/tag-files-s3.json")
		out, err := cmd.CombinedOutput()
		if (err == nil) != wantHealthy {
			t.Fatalf("preflight healthy=%t want %t: %v\n%s", err == nil, wantHealthy, err, out)
		}
	}
	check(true)
	f.Restart()
	check(true)
	f.Shell("fs.configure -locationPrefix=/ -fsync=false -apply")
	check(false)
	f.Shell("fs.configure -locationPrefix=/ -fsync=true -apply")
	check(true)
	f.Shell("fs.configure -locationPrefix=/buckets/tag-files/.uploads/ -fsync=false -apply")
	check(false)
	f.Shell("fs.configure -locationPrefix=/buckets/tag-files/.uploads/ -delete -apply")
	check(true)
	f.Shell("fs.configure -locationPrefix=/buckets/tag-files/ -ttl=1d -apply")
	check(false)
	f.Shell("fs.configure -locationPrefix=/buckets/tag-files/ -delete -apply")
	check(true)
	api := f.S3()
	f.PublicAccessBlock(false)
	check(false)
	f.PublicAccessBlock(true)
	check(true)
	if _, err := api.PutBucketLifecycleConfiguration(context.Background(), &s3.PutBucketLifecycleConfigurationInput{Bucket: aws.String("tag-files"), LifecycleConfiguration: &types.BucketLifecycleConfiguration{Rules: []types.LifecycleRule{{ID: aws.String("unexpected-ttl"), Status: types.ExpirationStatusEnabled, Filter: &types.LifecycleRuleFilter{Prefix: aws.String("")}, Expiration: &types.LifecycleExpiration{Days: aws.Int32(1)}}}}}); err != nil {
		t.Fatal(err)
	}
	check(false)
	if _, err := api.DeleteBucketLifecycle(context.Background(), &s3.DeleteBucketLifecycleInput{Bucket: aws.String("tag-files")}); err != nil {
		t.Fatal(err)
	}
	check(true)
	f.PublicAccessBlock(false)
	policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::tag-files/*"}]}`
	if _, err := api.PutBucketPolicy(context.Background(), &s3.PutBucketPolicyInput{Bucket: aws.String("tag-files"), Policy: &policy}); err != nil {
		t.Fatal(err)
	}
	f.PublicAccessBlock(true)
	check(false)
	if _, err := api.DeleteBucketPolicy(context.Background(), &s3.DeleteBucketPolicyInput{Bucket: aws.String("tag-files")}); err != nil {
		t.Fatal(err)
	}
	check(true)
	f.PublicAccessBlock(false)
	if _, err := api.PutBucketAcl(context.Background(), &s3.PutBucketAclInput{Bucket: aws.String("tag-files"), ACL: types.BucketCannedACLPublicRead}); err != nil {
		t.Fatal(err)
	}
	f.PublicAccessBlock(true)
	check(false)
	if _, err := api.PutBucketAcl(context.Background(), &s3.PutBucketAclInput{Bucket: aws.String("tag-files"), ACL: types.BucketCannedACLPrivate}); err != nil {
		t.Fatal(err)
	}
	check(true)
	f.PublicAccessBlock(false)
	if _, err := api.PutObject(context.Background(), &s3.PutObjectInput{Bucket: aws.String("tag-files"), Key: aws.String("legacy-public"), Body: strings.NewReader("private"), ACL: types.ObjectCannedACLPublicRead}); err != nil {
		t.Fatal(err)
	}
	publicGET := func(expected int) {
		t.Helper()
		res, err := http.Get(f.Endpoint + "/tag-files/legacy-public")
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != expected {
			t.Fatalf("legacy public ACL GET=%d want %d", res.StatusCode, expected)
		}
	}
	publicGET(200)
	f.PublicAccessBlock(true)
	publicGET(403)
	check(true)
	// All mutations below are confined to this disposable fixture. The checker
	// itself never changes files/rules or heals an unsafe profile.
	config := filepath.Join(t.TempDir(), "filer.toml")
	if err := os.WriteFile(config, []byte("[leveldb2]\nenabled = true\ndir = \"/outside\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	testutil.Docker(t, "", "cp", config, f.Container+":/etc/seaweedfs/filer.toml")
	check(false)
	if err := os.WriteFile(config, []byte(storageprofile.Backend), 0600); err != nil {
		t.Fatal(err)
	}
	testutil.Docker(t, "", "cp", config, f.Container+":/etc/seaweedfs/filer.toml")
	check(true)
	credentials := filepath.Join(t.TempDir(), "s3.json")
	if err := os.WriteFile(credentials, []byte(`{"identities":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	testutil.Docker(t, "", "cp", credentials, f.Container+":/etc/seaweedfs/tag-files-s3.json")
	check(false)
}
