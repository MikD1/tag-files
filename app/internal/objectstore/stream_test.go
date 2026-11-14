package objectstore

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

type clientFunc func(*http.Request) (*http.Response, error)

func (f clientFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func zeroChecksum(n int64) string {
	h := sha256.New()
	_, _ = io.CopyN(h, zeros{}, n)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func TestNonseekableFailureNeverAutomaticallyResendsConsumedBody(t *testing.T) {
	var attempts atomic.Int32
	data := strings.Repeat("streamed", 1024)
	_, sum := digests([]byte(data))
	var read int64
	transport := clientFunc(func(r *http.Request) (*http.Response, error) {
		attempts.Add(1)
		if r.Header.Get("X-Amz-Checksum-Sha256") != sum || r.ContentLength != int64(len(data)) {
			t.Fatalf("wire profile: checksum=%q length=%d", r.Header.Get("X-Amz-Checksum-Sha256"), r.ContentLength)
		}
		var err error
		read, err = io.Copy(io.Discard, r.Body)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 503, Header: http.Header{"Content-Type": []string{"application/xml"}}, Body: io.NopCloser(strings.NewReader(`<Error><Code>SlowDown</Code><Message>retryable storage failure</Message></Error>`)), Request: r}, nil
	})
	c, err := New(Config{Endpoint: "http://storage.test", Region: "us-east-1", Bucket: "tag-files", AccessKey: "test", SecretKey: "test", HTTPClient: transport})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.UploadPart(context.Background(), "incoming/test/source", "test-mpu", 1, int64(len(data)), sum, io.LimitReader(strings.NewReader(data), int64(len(data))))
	if err == nil || attempts.Load() != 1 || read != int64(len(data)) {
		t.Fatalf("attempts=%d read=%d err=%v", attempts.Load(), read, err)
	}
}

func TestSuccessfulCompleteStillRequiresFullGETAndPartHashes(t *testing.T) {
	var complete, head, get atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "POST":
			complete.Add(1)
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><Location>http://storage.test/tag-files/incoming/test</Location><Bucket>tag-files</Bucket><Key>incoming/test</Key><ETag>"opaque"</ETag></CompleteMultipartUploadResult>`)
		case "HEAD":
			head.Add(1)
			w.Header().Set("Content-Length", "3")
			w.Header().Set("ETag", `"opaque"`)
		case "GET":
			get.Add(1)
			w.Header().Set("Content-Length", "3")
			w.Header().Set("ETag", `"opaque"`)
			_, _ = io.WriteString(w, "BAD")
		default:
			t.Errorf("unexpected %s", r.Method)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	c, err := New(Config{Endpoint: server.URL, Region: "us-east-1", Bucket: "tag-files", AccessKey: "test", SecretKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	_, sum := digests([]byte("yes"))
	_, err = c.Complete(context.Background(), "incoming/test", "mpu", []Part{{Number: 1, Size: 3, ETag: `"opaque"`, SHA256: sum}})
	if !errors.Is(err, ErrIntegrity) || complete.Load() != 1 || head.Load() != 1 || get.Load() != 1 {
		t.Fatalf("complete=%d HEAD=%d GET=%d err=%v", complete.Load(), head.Load(), get.Load(), err)
	}
}

func TestListPartsNormalizesOnlyIdenticalSeaweedAliases(t *testing.T) {
	for _, conflicting := range []bool{false, true} {
		t.Run(strconv.FormatBool(conflicting), func(t *testing.T) {
			etag := "same"
			if conflicting {
				etag = "different"
			}
			transport := clientFunc(func(r *http.Request) (*http.Response, error) {
				xml := `<ListPartsResult><Bucket>tag-files</Bucket><Key>incoming/test</Key><UploadId>mpu</UploadId><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>same</ETag><Size>3</Size></Part><Part><PartNumber>1</PartNumber><ETag>` + etag + `</ETag><Size>3</Size></Part></ListPartsResult>`
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/xml"}}, Body: io.NopCloser(strings.NewReader(xml)), Request: r}, nil
			})
			c, err := New(Config{Endpoint: "http://storage.test", Region: "us-east-1", Bucket: "tag-files", AccessKey: "test", SecretKey: "test", HTTPClient: transport})
			if err != nil {
				t.Fatal(err)
			}
			parts, err := c.ListParts(context.Background(), "incoming/test", "mpu")
			if conflicting {
				if !errors.Is(err, ErrIntegrity) {
					t.Fatalf("conflicting aliases accepted: %+v %v", parts, err)
				}
			} else if err != nil || len(parts) != 1 || parts[0].SHA256 != "" {
				t.Fatalf("aliases not normalized, or a SHA was invented: %+v %v", parts, err)
			}
		})
	}
}

func BenchmarkNonseekablePart(b *testing.B) {
	for _, size := range []int64{8 << 20, 64 << 20} {
		b.Run(strconv.FormatInt(size>>20, 10)+"MiB", func(b *testing.B) {
			sum := zeroChecksum(size)
			transport := clientFunc(func(r *http.Request) (*http.Response, error) {
				n, err := io.Copy(io.Discard, r.Body)
				if err != nil || n != size {
					b.Fatalf("read %d: %v", n, err)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Etag": []string{`"opaque"`}, "X-Amz-Checksum-Sha256": []string{sum}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			})
			c, err := New(Config{Endpoint: "http://storage.test", Region: "us-east-1", Bucket: "tag-files", AccessKey: "test", SecretKey: "test", HTTPClient: transport})
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := c.UploadPart(context.Background(), "incoming/test/source", "mpu", 1, size, sum, io.LimitReader(zeros{}, size)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
