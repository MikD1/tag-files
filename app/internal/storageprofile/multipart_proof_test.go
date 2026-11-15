package storageprofile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/mikd1/tag-files/app/internal/objectstore"
)

func proofSHA(data []byte) string {
	sum := sha256.Sum256(data)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func TestMultipartProofAuthenticatesActualPinnedSDKNonseekableRequest(t *testing.T) {
	data := []byte("SDK body and SHA independently authored")
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			io.WriteString(w, `<ListPartsResult><IsTruncated>false</IsTruncated></ListPartsResult>`)
			return
		}
		got, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(got, data) {
			t.Errorf("wrong actual bytes: %q %v", got, err)
		}
		w.Header().Set("ETag", "sdk-opaque")
		w.Header().Set("X-Amz-Checksum-Sha256", proofSHA(data))
	}))
	defer backend.Close()
	target, _ := url.Parse(backend.URL)
	p, err := newMultipartProof(t.TempDir(), "storage-id", "epoch", appCredentials(), target, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}
	owned := httptest.NewServer(p.wrap(http.NotFoundHandler()))
	defer owned.Close()
	c, err := objectstore.New(objectstore.Config{Endpoint: owned.URL, Region: "us-east-1", Bucket: "tag-files", AccessKey: appCredentials().AccessKeyID, SecretKey: appCredentials().SecretAccessKey})
	if err != nil {
		t.Fatal(err)
	}
	part, err := c.UploadPart(context.Background(), "incoming/SDK 猫", "native-id", 1, int64(len(data)), proofSHA(data), io.LimitReader(bytes.NewReader(data), int64(len(data))))
	if err != nil || part.SHA256 != proofSHA(data) {
		t.Fatalf("actual SDK authentication failed: %+v %v", part, err)
	}
}

func proofRequest(t *testing.T, method, query string, body []byte, sum string, credentials aws.Credentials) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, "http://owned.test/tag-files/incoming/test%20%E7%8C%AB?"+query, bytes.NewReader(body))
	if sum != "" {
		r.Header.Set("X-Amz-Checksum-Sha256", sum)
	}
	r.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
	if err := v4.NewSigner(func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true }).SignHTTP(context.Background(), credentials, r, "UNSIGNED-PAYLOAD", "s3", "us-east-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMultipartProofActualBytesDurableListingAndImmutableRetry(t *testing.T) {
	data := []byte("actual body independent of a client binding")
	sum := proofSHA(data)
	var stored bool
	puts := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Header().Set("Content-Type", "application/xml")
			if stored {
				fmt.Fprintf(w, `<ListPartsResult><Bucket>tag-files</Bucket><Key>incoming/test 猫</Key><UploadId>mpu</UploadId><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>opaque</ETag><Size>%d</Size></Part></ListPartsResult>`, len(data))
			} else {
				io.WriteString(w, `<ListPartsResult><IsTruncated>false</IsTruncated></ListPartsResult>`)
			}
			return
		}
		puts++
		got, _ := io.ReadAll(r.Body)
		if proofSHA(got) != r.Header.Get("X-Amz-Checksum-Sha256") {
			w.WriteHeader(400)
			io.WriteString(w, `<Error><Code>BadDigest</Code></Error>`)
			return
		}
		stored = true
		w.Header().Set("ETag", "opaque")
		w.Header().Set("X-Amz-Checksum-Sha256", sum)
	}))
	defer backend.Close()
	target, _ := url.Parse(backend.URL)
	root := t.TempDir()
	newProof := func(epoch string) *multipartProof {
		p, err := newMultipartProof(root, "storage-id", epoch, appCredentials(), target, http.DefaultTransport)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := newProof("first")
	call := func(p *multipartProof, r *http.Request) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		p.wrap(http.NotFoundHandler()).ServeHTTP(w, r)
		return w
	}
	if w := call(p, proofRequest(t, "PUT", "uploadId=mpu&partNumber=1", data, sum, appCredentials())); w.Code != 200 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	// A new gateway lifetime must recover actual proof; the backend omits SHA.
	p = newProof("second")
	if w := call(p, proofRequest(t, "GET", "uploadId=mpu", nil, "", appCredentials())); w.Code != 200 || !strings.Contains(w.Body.String(), "<ChecksumSHA256>"+sum+"</ChecksumSHA256>") {
		t.Fatalf("durable list: %d %s", w.Code, w.Body.String())
	}
	before := puts
	if w := call(p, proofRequest(t, "PUT", "uploadId=mpu&partNumber=1", []byte("different"), proofSHA([]byte("different")), appCredentials())); w.Code != 409 || puts != before {
		t.Fatalf("changed binding forwarded: %d calls=%d", w.Code, puts-before)
	}
	// A certified native BadDigest rejects the overwrite before storage, so
	// a failed same-binding retry does not erase the earlier validated proof.
	wrong := bytes.Repeat([]byte("!"), len(data))
	if w := call(p, proofRequest(t, "PUT", "uploadId=mpu&partNumber=1", wrong, sum, appCredentials())); w.Code < 400 {
		t.Fatalf("wrong body accepted: %d", w.Code)
	}
	if w := call(p, proofRequest(t, "GET", "uploadId=mpu", nil, "", appCredentials())); w.Code != 200 || !strings.Contains(w.Body.String(), sum) {
		t.Fatalf("retry erased proof: %d %s", w.Code, w.Body.String())
	}
}

func TestMultipartProofRejectsUnauthenticatedBindingBeforeMutation(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unauthenticated request reached backend") }))
	defer backend.Close()
	target, _ := url.Parse(backend.URL)
	root := t.TempDir()
	p, err := newMultipartProof(root, "storage-id", "epoch", appCredentials(), target, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadDir(filepath.Join(root, proofDirectory))
	bad := appCredentials()
	bad.SecretAccessKey += "wrong"
	r := proofRequest(t, "PUT", "uploadId=mpu&partNumber=1", []byte("body"), proofSHA([]byte("body")), bad)
	w := httptest.NewRecorder()
	p.wrap(http.NotFoundHandler()).ServeHTTP(w, r)
	after, _ := os.ReadDir(filepath.Join(root, proofDirectory))
	if w.Code != 403 || len(before) != len(after) {
		t.Fatalf("auth poisoned proof journal: status=%d before=%d after=%d", w.Code, len(before), len(after))
	}
}
