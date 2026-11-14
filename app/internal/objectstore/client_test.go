package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/testutil"
)

var ctx = context.Background()

func digests(data []byte) (string, string) {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), base64.StdEncoding.EncodeToString(sum[:])
}

func fixture(t *testing.T) (*Client, *testutil.Storage) {
	f := testutil.StartStorage(t)
	return connected(t, f), f
}

func connected(t *testing.T, f *testutil.Storage) *Client {
	t.Helper()
	c, err := New(Config{Endpoint: f.Endpoint, Region: "us-east-1", Bucket: "tag-files", AccessKey: f.AccessKey, SecretKey: f.SecretKey})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSeaweedCRUDRangeConditionsPaginationAndRestart(t *testing.T) {
	c, f := fixture(t)
	data := []byte("verified unicode 猫 bytes 0123456789")
	hash, _ := digests(data)
	key := "primary/isolated/猫/media.bin"
	if err := c.Put(ctx, key, bytes.NewReader(data), int64(len(data)), hash, "application/octet-stream", map[string]string{"provenance": "conformance"}); err != nil {
		t.Fatal(err)
	}
	info, err := c.Head(ctx, key)
	if err != nil || info.Size != int64(len(data)) || info.Metadata["provenance"] != "conformance" || info.ETag == "" {
		t.Fatalf("HEAD %+v %v", info, err)
	}
	if _, err := c.Verify(ctx, key, int64(len(data)), hash); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Verify(ctx, key, int64(len(data)), strings.Repeat("0", 64)); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("wrong hash accepted: %v", err)
	}
	if err := c.Put(ctx, "primary/isolated/bad", bytes.NewReader(data), int64(len(data)), strings.Repeat("0", 64), "application/octet-stream", nil); err == nil {
		t.Fatal("gateway accepted an incorrect object SHA256")
	}
	wrongCredentials, err := New(Config{Endpoint: f.Endpoint, Region: "us-east-1", Bucket: "tag-files", AccessKey: f.AccessKey, SecretKey: "incorrect-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrongCredentials.Head(ctx, key); err == nil {
		t.Fatal("incorrect signing secret was accepted")
	}
	ranged, err := c.Get(ctx, key, GetOptions{Range: "bytes=2-7", IfMatch: info.ETag})
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(ranged.Body)
	ranged.Body.Close()
	if err != nil || !bytes.Equal(got, data[2:8]) || ranged.ContentRange != "bytes 2-7/"+strconv.Itoa(len(data)) {
		t.Fatalf("Range %+v %q %v", ranged, got, err)
	}
	future, past := info.LastModified.Add(time.Hour), info.LastModified.Add(-time.Hour)
	for _, test := range []struct {
		options GetOptions
		status  int
	}{{GetOptions{Range: "bytes=9999-"}, 416}, {GetOptions{IfNoneMatch: info.ETag}, 304}, {GetOptions{IfMatch: `"not-the-object"`}, 412}, {GetOptions{IfModifiedSince: &future}, 304}, {GetOptions{IfUnmodifiedSince: &past}, 412}} {
		_, err := c.Get(ctx, key, test.options)
		var upstream *Error
		if !errors.As(err, &upstream) || upstream.Status != test.status {
			t.Fatalf("condition status %d: %v", test.status, err)
		}
	}
	for _, suffix := range []string{"a", "b", "c"} {
		if err := c.Put(ctx, "listing/"+suffix, bytes.NewReader(data), int64(len(data)), hash, "application/octet-stream", nil); err != nil {
			t.Fatal(err)
		}
	}
	var keys []string
	token := ""
	for {
		page, err := c.List(ctx, "listing/", token, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range page.Objects {
			keys = append(keys, o.Key)
		}
		if page.NextToken == "" {
			break
		}
		if token == page.NextToken {
			t.Fatal("pagination did not advance")
		}
		token = page.NextToken
	}
	if strings.Join(keys, ",") != "listing/a,listing/b,listing/c" {
		t.Fatal(keys)
	}
	for _, method := range []string{"GET", "PUT"} {
		req, _ := http.NewRequest(method, f.Endpoint+"/tag-files/"+key, strings.NewReader("bad"))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 403 {
			t.Fatalf("anonymous %s returned %d", method, res.StatusCode)
		}
	}
	f.Restart()
	c = connected(t, f) // Docker may reassign an ephemeral published test port.
	if _, err := c.Verify(ctx, key, int64(len(data)), hash); err != nil {
		t.Fatal(err)
	}
	f.AssertGlobalFsync()
	for i := 0; i < 2; i++ {
		if err := c.Delete(ctx, key); err != nil {
			t.Fatal(err)
		}
	}
	_, err = c.Head(ctx, key)
	var missing *Error
	if !errors.As(err, &missing) || missing.Status != 404 {
		t.Fatalf("deleted object: %v", err)
	}
}

func TestSeaweedLostUploadAndCompleteResponsesAreReconcilable(t *testing.T) {
	c, f := fixture(t)
	key := "incoming/isolated/small"
	id, err := c.CreateMultipart(ctx, key, "application/octet-stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("small single part 猫")
	fullHash, sum := digests(data)
	var uploads, completes atomic.Int32
	wire := clientFunc(func(r *http.Request) (*http.Response, error) {
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			return nil, err
		}
		if response.StatusCode == 200 && ((r.Method == "PUT" && r.URL.Query().Get("partNumber") != "" && uploads.Add(1) == 1) || (r.Method == "POST" && r.URL.Query().Get("uploadId") != "" && completes.Add(1) == 1)) {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			return nil, errors.New("injected lost response after storage acknowledgement")
		}
		return response, nil
	})
	lossy, err := New(Config{Endpoint: f.Endpoint, Region: "us-east-1", Bucket: "tag-files", AccessKey: f.AccessKey, SecretKey: f.SecretKey, HTTPClient: wire})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lossy.UploadPart(ctx, key, id, 1, int64(len(data)), sum, io.LimitReader(bytes.NewReader(data), int64(len(data)))); err == nil {
		t.Fatal("response loss not injected")
	}
	listed, err := c.ListParts(ctx, key, id)
	if err != nil || len(listed) != 1 || listed[0].Size != int64(len(data)) {
		t.Fatalf("lost part response not reconcilable: %+v %v", listed, err)
	}
	// The checksum comes from the immutable request binding, never ListParts.
	manifest := []Part{{Number: 1, Size: int64(len(data)), ETag: listed[0].ETag, SHA256: sum}}
	if _, err := lossy.Complete(ctx, key, id, manifest); err == nil {
		t.Fatal("Complete response loss not injected")
	}
	verified, err := c.VerifyParts(ctx, key, manifest)
	if err != nil || verified.SHA256 != fullHash || uploads.Load() != 1 || completes.Load() != 1 {
		t.Fatalf("ambiguous complete %+v %v uploads=%d completes=%d", verified, err, uploads.Load(), completes.Load())
	}
	active, err := c.ListMultipart(ctx, "incoming/")
	if err != nil || len(active) != 0 {
		t.Fatalf("completed MPU retained: %+v %v", active, err)
	}
}

func TestSeaweedMultipartPaginationBeyond1000(t *testing.T) {
	c, _ := fixture(t)
	const key = "incoming/pages/parts"
	id, err := c.CreateMultipart(ctx, key, "application/octet-stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, sum := digests([]byte("x"))
	for i := 1; i <= 1001; i++ {
		if _, err := c.UploadPart(ctx, key, id, i, 1, sum, strings.NewReader("x")); err != nil {
			t.Fatalf("part %d: %v", i, err)
		}
	}
	parts, err := c.ListParts(ctx, key, id)
	if err != nil || len(parts) != 1001 {
		t.Fatalf("part pages: %d %v", len(parts), err)
	}
	for i, p := range parts {
		if p.Number != i+1 || p.Size != 1 {
			t.Fatalf("part page gap: %+v", p)
		}
	}
	wanted := map[string]bool{}
	for i := 0; i < 1001; i++ {
		uploadID, err := c.CreateMultipart(ctx, "incoming/pages/shared", "application/octet-stream", map[string]string{"case": fmt.Sprint(i)})
		if err != nil {
			t.Fatal(err)
		}
		wanted[uploadID] = true
	}
	uploads, err := c.ListMultipart(ctx, "incoming/pages/shared")
	if err != nil || len(uploads) != len(wanted) {
		t.Fatalf("MPU pages: %d %v", len(uploads), err)
	}
	for _, upload := range uploads {
		if !wanted[upload.UploadID] || upload.Key != "incoming/pages/shared" {
			t.Fatalf("duplicate/unknown MPU: %+v", upload)
		}
		delete(wanted, upload.UploadID)
	}
	if len(wanted) != 0 {
		t.Fatal("MPU page gap")
	}
}

func TestSeaweedMultipartNonseekableChecksumsAndCompleteVerification(t *testing.T) {
	c, f := fixture(t)
	key := "incoming/isolated/source"
	id, err := c.CreateMultipart(ctx, key, "application/octet-stream", map[string]string{"purpose": "multipart"})
	if err != nil {
		t.Fatal(err)
	}
	first := bytes.Repeat([]byte("abcdefg"), 800000) // >5 MiB; the reader has no Seek.
	second := []byte("last part 猫")
	_, oneHash := digests(first)
	_, twoHash := digests(second)
	bad := strings.Repeat("A", 43) + "="
	if _, err := c.UploadPart(ctx, key, id, 1, int64(len(first)), bad, io.LimitReader(bytes.NewReader(first), int64(len(first)))); err == nil {
		t.Fatal("gateway accepted bytes with an incorrect explicit SHA256")
	}
	parts, err := c.ListParts(ctx, key, id)
	if err != nil || len(parts) != 0 {
		t.Fatalf("wrong checksum persisted a confirmed part: %+v %v", parts, err)
	}
	one, err := c.UploadPart(ctx, key, id, 1, int64(len(first)), oneHash, io.LimitReader(bytes.NewReader(first), int64(len(first))))
	if err != nil {
		t.Fatal(err)
	}
	// Identical-byte resend after a lost response must remain reconcilable.
	again, err := c.UploadPart(ctx, key, id, 1, int64(len(first)), oneHash, io.LimitReader(bytes.NewReader(first), int64(len(first))))
	if err != nil || again.ETag != one.ETag {
		t.Fatalf("same bytes retry %+v %v", again, err)
	}
	two, err := c.UploadPart(ctx, key, id, 2, int64(len(second)), twoHash, io.LimitReader(bytes.NewReader(second), int64(len(second))))
	if err != nil {
		t.Fatal(err)
	}
	f.Restart()
	c = connected(t, f)
	parts, err = c.ListParts(ctx, key, id)
	if err != nil || len(parts) != 2 || parts[0].Number != 1 || parts[1].Number != 2 || parts[0].ETag != one.ETag || parts[1].ETag != two.ETag || parts[0].Size != one.Size || parts[1].Size != two.Size || parts[0].SHA256 != "" || parts[1].SHA256 != "" {
		t.Fatalf("resume parts %+v %v", parts, err)
	}
	manifest := []Part{one, two}
	wrong := append([]Part{}, manifest...)
	wrong[0].ETag = `"wrong"`
	if _, err := c.Complete(ctx, key, id, wrong); err == nil {
		t.Fatal("InvalidPart manifest accepted")
	}
	verified, err := c.Complete(ctx, key, id, manifest)
	if err != nil {
		t.Fatal(err)
	}
	all := append(append([]byte{}, first...), second...)
	fullHash, _ := digests(all)
	if verified.SHA256 != fullHash || verified.Size != int64(len(all)) {
		t.Fatalf("complete without full verification: %+v", verified)
	}
	if _, err := c.VerifyParts(ctx, key, manifest); err != nil {
		t.Fatal(err)
	}
	wrong[0] = one
	wrong[0].SHA256 = bad
	if _, err := c.VerifyParts(ctx, key, wrong); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("binding mismatch accepted: %v", err)
	}
	abortID, err := c.CreateMultipart(ctx, "incoming/isolated/abort", "application/octet-stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	active, err := c.ListMultipart(ctx, "incoming/")
	if err != nil || len(active) != 1 || active[0].UploadID != abortID {
		t.Fatalf("active MPU %+v %v", active, err)
	}
	if err := c.Abort(ctx, "incoming/isolated/abort", abortID); err != nil {
		t.Fatal(err)
	}
	active, err = c.ListMultipart(ctx, "incoming/")
	if err != nil || len(active) != 0 {
		t.Fatalf("abort left MPU %+v %v", active, err)
	}
}
