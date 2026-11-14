package storageprofile

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestGlobalFsyncMustPersistAndCoverEveryOverrideWithoutTTL(t *testing.T) {
	for _, raw := range []string{
		`{"version":2,"locations":[{"locationPrefix":"/","fsync":true}]}`,
		`{"version":2,"locations":[{"locationPrefix":"/","fsync":true},{"locationPrefix":"/buckets/tag-files","fsync":true},{"locationPrefix":"/buckets/tag-files/.uploads/"}]}`,
	} {
		if err := ValidateRules([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{
		`{}`, `{"version":3,"locations":[{"locationPrefix":"/","fsync":true}]}`,
		`{"version":2,"locations":[]}`,
		`{"version":2,"locations":[{"locationPrefix":"/buckets/tag-files","fsync":true}]}`,
		`{"version":2,"locations":[{"locationPrefix":"/"}]}`,
		`{"version":2,"locations":[{"locationPrefix":"/","fsync":false}]}`,
		`{"version":2,"locations":[{"locationPrefix":"/","fsync":true},{"locationPrefix":"/buckets/tag-files/.uploads/","fsync":false}]}`,
		`{"version":2,"locations":[{"locationPrefix":"/","fsync":true,"ttl":"1d"}]}`,
		`{"version":2,"locations":[{"locationPrefix":"/","fsync":true,"readOnly":true}]}`,
		`{"version":2,"locations":[{"locationPrefix":"/","fsync":true},{"locationPrefix":"/","fsync":true}]}`,
		`{"version":2,"locations":[{"locationPrefix":"/","fsync":true}]} garbage`,
	} {
		if err := ValidateRules([]byte(raw)); err == nil {
			t.Fatalf("unsafe config accepted: %s", raw)
		}
	}
}

func TestCompleteVolumeLayoutCannotEscapeThroughMetadataSymlink(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"master", "filerldb2"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := checkLayout(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "filerldb2")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "filerldb2")); err != nil {
		t.Fatal(err)
	}
	if err := checkLayout(root); err == nil {
		t.Fatal("metadata outside complete data volume was accepted")
	}
}

func TestReadConfigIsBounded(t *testing.T) {
	file := filepath.Join(t.TempDir(), "oversized")
	if err := os.WriteFile(file, []byte(strings.Repeat("x", MaxConfigBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfig(file); err == nil {
		t.Fatal("unbounded configuration accepted")
	}
}

func TestControlOutputBoundAppliesToIOCopyFastPaths(t *testing.T) {
	out := &boundedOutput{max: 16}
	_, err := io.Copy(out, io.LimitReader(strings.NewReader(strings.Repeat("x", 2*1024*1024)), 2*1024*1024))
	if err == nil || len(out.Bytes()) > 16 {
		t.Fatalf("control output bypassed bound: retained=%d err=%v", len(out.Bytes()), err)
	}
}

func TestPrivateCredentialsMustAllowReadWriteListOnOwnedBucket(t *testing.T) {
	for _, actions := range []string{`"Read","List"`, `"Read:tag-files","List:tag-files","Write:other-bucket"`, `"Write:tag-files","List:tag-files"`} {
		raw := `{"identities":[{"name":"app","credentials":[{"accessKey":"key","secretKey":"secret"}],"actions":[` + actions + `]}]}`
		if _, err := PrivateCredentials([]byte(raw)); err == nil {
			t.Fatalf("insufficient actions accepted: %s", actions)
		}
	}
}

func TestUnmodeledIdentityPoliciesCannotOverrideRequiredActions(t *testing.T) {
	raw := `{"identities":[{"name":"app","credentials":[{"accessKey":"key","secretKey":"secret"}],"actions":["Read","Write","List"],"policies":["deny-writes"]}]}`
	if _, err := PrivateCredentials([]byte(raw)); err == nil {
		t.Fatal("unmodeled policy field accepted")
	}
}

func TestBucketACLAcceptsOnlyOwnerFullControl(t *testing.T) {
	private := `<AccessControlPolicy><Owner><ID>owner</ID></Owner><AccessControlList><Grant><Grantee xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="CanonicalUser"><ID>owner</ID></Grantee><Permission>FULL_CONTROL</Permission></Grant></AccessControlList></AccessControlPolicy>`
	if err := ValidateACL([]byte(private)); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`<AccessControlPolicy/>`, strings.Replace(private, `<ID>owner</ID>`, `<ID>other</ID>`, 1), strings.Replace(private, `<ID>owner</ID></Grantee>`, `<URI>http://acs.amazonaws.com/groups/global/AllUsers</URI></Grantee>`, 1), private + `<extra/>`, strings.Replace(private, `FULL_CONTROL`, `READ`, 1)} {
		if err := ValidateACL([]byte(data)); err == nil {
			t.Fatal("non-owner or unverified ACL accepted")
		}
	}
}

func TestPublicAccessBlockMustDisableEveryPublicPath(t *testing.T) {
	private := `<PublicAccessBlockConfiguration><BlockPublicAcls>true</BlockPublicAcls><IgnorePublicAcls>true</IgnorePublicAcls><BlockPublicPolicy>true</BlockPublicPolicy><RestrictPublicBuckets>true</RestrictPublicBuckets></PublicAccessBlockConfiguration>`
	if err := ValidatePublicAccessBlock([]byte(private)); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"BlockPublicAcls", "IgnorePublicAcls", "BlockPublicPolicy", "RestrictPublicBuckets"} {
		missing := strings.Replace(private, "<"+field+">true</"+field+">", "", 1)
		if err := ValidatePublicAccessBlock([]byte(missing)); err == nil {
			t.Fatalf("missing %s accepted", field)
		}
	}
}

func TestReadConfigRejectsFIFOWithoutWaitingForAWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config-fifo")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := readConfig(path); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO configuration accepted")
		}
	case <-time.After(100 * time.Millisecond):
		// Release the old blocking implementation before failing the regression.
		writer, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			t.Fatal(err)
		}
		writer.Close()
		<-done
		t.Fatal("configuration reader waited for a FIFO writer")
	}
}

func TestOnlyThePinnedEmbeddedBackendAndPrivateCredentialsAreAccepted(t *testing.T) {
	if err := ValidateBackend([]byte("[leveldb2]\nenabled = true\ndir = \"/data/filerldb2\"\n")); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "[memory]\nenabled=true", "[leveldb2]\nenabled = false\ndir = \"/data/filerldb2\"", "[leveldb2]\nenabled = true\ndir = \"/outside\""} {
		if err := ValidateBackend([]byte(raw)); err == nil {
			t.Fatal("unsafe backend accepted")
		}
	}
	valid := `{"identities":[{"name":"app","credentials":[{"accessKey":"test-access","secretKey":"test-secret"}],"actions":["Read:tag-files","Write:tag-files","List:tag-files"]}]}`
	credentials, err := PrivateCredentials([]byte(valid))
	if err != nil || credentials.AccessKeyID != "test-access" || credentials.SecretAccessKey != "test-secret" {
		t.Fatalf("private identity: %v", err)
	}
	for _, raw := range []string{`{}`, `{"identities":[]}`, strings.Replace(valid, `"app"`, `"anonymous"`, 1), strings.Replace(valid, `"test-secret"`, `""`, 1), strings.Replace(valid, `"credentials":[{"accessKey":"test-access","secretKey":"test-secret"}],`, "", 1)} {
		if _, err := PrivateCredentials([]byte(raw)); err == nil {
			t.Fatal("anonymous/empty profile accepted")
		}
	}
}
