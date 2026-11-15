package storageimage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/testutil"
)

type multipartProofTransport func(*http.Request) (*http.Response, error)

func (f multipartProofTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func nativeProofSHA(data []byte) string {
	sum := sha256.Sum256(data)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func TestOwnedGatewayMultipartProofLostACKRestartPagingAndCleanup(t *testing.T) {
	f := create(t)
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	ctx := context.Background()
	c := f.client()
	key := "incoming/proof-native/source 猫"
	id, err := c.CreateMultipart(ctx, key, "application/octet-stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	first := bytes.Repeat([]byte("actual bytes; not an application binding;"), 140000)
	last := []byte("second part with a genuinely lost client ACK")
	one, err := c.UploadPart(ctx, key, id, 1, int64(len(first)), nativeProofSHA(first), io.LimitReader(bytes.NewReader(first), int64(len(first))))
	if err != nil {
		t.Fatal(err)
	}
	var lost atomic.Bool
	lostClient, err := objectstore.New(objectstore.Config{Endpoint: f.endpoint(), Region: "us-east-1", Bucket: "tag-files", AccessKey: f.access, SecretKey: f.secret, HTTPClient: &http.Client{Transport: multipartProofTransport(func(r *http.Request) (*http.Response, error) {
		res, e := http.DefaultTransport.RoundTrip(r)
		if e == nil && r.Method == "PUT" && r.URL.Query().Get("partNumber") == "2" && !lost.Swap(true) {
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			return nil, errors.New("fixture lost ACK after gateway durable proof")
		}
		return res, e
	})}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lostClient.UploadPart(ctx, key, id, 2, int64(len(last)), nativeProofSHA(last), io.LimitReader(bytes.NewReader(last), int64(len(last)))); err == nil || !lost.Load() {
		t.Fatal("fixture failed to lose actual ACK", err)
	}
	assertParts := func(client *objectstore.Client) []objectstore.Part {
		t.Helper()
		parts, e := client.ListParts(ctx, key, id)
		if e != nil || len(parts) != 2 {
			t.Fatalf("durable native listing: %+v %v", parts, e)
		}
		for index, data := range [][]byte{first, last} {
			if parts[index].Number != index+1 || parts[index].Size != int64(len(data)) || parts[index].SHA256 != nativeProofSHA(data) || parts[index].ETag == "" {
				t.Fatalf("native proof differs from independent bytes: %+v", parts[index])
			}
		}
		return parts
	}
	assertParts(c)
	wrong := bytes.Repeat([]byte("!"), len(first))
	if _, err = c.UploadPart(ctx, key, id, 1, int64(len(wrong)), nativeProofSHA(first), io.LimitReader(bytes.NewReader(wrong), int64(len(wrong)))); err == nil {
		t.Fatal("native storage accepted body/header checksum mismatch")
	}
	if _, err = c.UploadPart(ctx, key, id, 1, int64(len(wrong)), nativeProofSHA(wrong), io.LimitReader(bytes.NewReader(wrong), int64(len(wrong)))); err == nil {
		t.Fatal("gateway overwrote immutable binding")
	} else {
		var wire *objectstore.Error
		if !errors.As(err, &wire) || wire.Status != 409 {
			t.Fatalf("changed binding was not rejected before forwarding: %v", err)
		}
	}
	if parts := assertParts(c); parts[0].ETag != one.ETag {
		t.Fatal("bad retry changed stored primary part")
	}
	testutil.Docker(t, "", "restart", "--time", "15", f.name)
	f.ready()
	c = f.client()
	parts := assertParts(c)
	verified, err := c.Complete(ctx, key, id, parts)
	if err != nil {
		t.Fatal(err)
	}
	full := sha256.New()
	full.Write(first)
	full.Write(last)
	if verified.SHA256 != hex.EncodeToString(full.Sum(nil)) {
		t.Fatal("completed object differs from source bytes")
	}
	if dirs := testutil.Docker(t, "", "exec", f.name, "find", "/data/.tag-files-mpu-proofs", "-mindepth", "1", "-maxdepth", "1", "-type", "d"); dirs != "" {
		t.Fatalf("settled terminal MPU retained proof history: %s", dirs)
	}
	// Small nonfinal parts are legal UploadPart/ListParts fixtures; this MPU is
	// deliberately aborted, so it never claims a valid complete small-part file.
	pageKey := "incoming/proof-native/paging"
	pageID, err := c.CreateMultipart(ctx, pageKey, "application/octet-stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 1001; n++ {
		data := []byte(fmt.Sprintf("native page part %04d", n))
		if _, err = c.UploadPart(ctx, pageKey, pageID, n, int64(len(data)), nativeProofSHA(data), io.LimitReader(bytes.NewReader(data), int64(len(data)))); err != nil {
			t.Fatalf("native paging part %d: %v", n, err)
		}
	}
	paged, err := c.ListParts(ctx, pageKey, pageID)
	if err != nil || len(paged) != 1001 {
		t.Fatalf("native paging: count=%d error=%v", len(paged), err)
	}
	for n, part := range paged {
		expected := []byte(fmt.Sprintf("native page part %04d", n+1))
		if part.Number != n+1 || part.Size != int64(len(expected)) || part.SHA256 != nativeProofSHA(expected) {
			t.Fatalf("page checksum gap: %+v", part)
		}
	}
	api := s3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: f.access, SecretAccessKey: f.secret}, nil
	})}, func(o *s3.Options) { o.BaseEndpoint = aws.String(f.endpoint()); o.UsePathStyle = true })
	pager := s3.NewListPartsPaginator(api, &s3.ListPartsInput{Bucket: aws.String("tag-files"), Key: aws.String(pageKey), UploadId: aws.String(pageID), MaxParts: aws.Int32(1000)})
	pages, total := 0, 0
	for pager.HasMorePages() {
		page, e := pager.NextPage(ctx)
		if e != nil {
			t.Fatal(e)
		}
		pages++
		for _, part := range page.Parts {
			total++
			expected := []byte(fmt.Sprintf("native page part %04d", total))
			if int(aws.ToInt32(part.PartNumber)) != total || aws.ToString(part.ChecksumSHA256) != nativeProofSHA(expected) {
				t.Fatalf("explicit page boundary lost proof: %+v", part)
			}
		}
	}
	if pages < 2 || total != 1001 {
		t.Fatalf("native explicit paging did not cross boundary: pages=%d parts=%d", pages, total)
	}
	if err = c.Abort(ctx, pageKey, pageID); err != nil {
		t.Fatal(err)
	}
	if dirs := testutil.Docker(t, "", "exec", f.name, "find", "/data/.tag-files-mpu-proofs", "-mindepth", "1", "-maxdepth", "1", "-type", "d"); strings.TrimSpace(dirs) != "" {
		t.Fatalf("aborted native MPU leaked proof history: %s", dirs)
	}
	t.Logf("native durable multipart proof: lost ACK, restart, independent SHA, rejected body/binding changes, 1001 paged parts, verified Complete and zero terminal proof directories")
}
