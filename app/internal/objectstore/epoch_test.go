package objectstore

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStorageEpochDiscoveryAndRequestFenceUseTheActualS3Wire(t *testing.T) {
	const epoch = "11111111-1111-4111-8111-111111111111"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tag-Files-Storage-Epoch", epoch)
		if r.Method == "HEAD" {
			return
		}
		if got := r.Header.Get("X-Tag-Files-Expected-Storage-Epoch"); got != epoch {
			t.Errorf("write lost its epoch fence: %q", got)
			w.WriteHeader(409)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<InitiateMultipartUploadResult><Bucket>tag-files</Bucket><Key>source</Key><UploadId>owned-mpu</UploadId></InitiateMultipartUploadResult>`))
	}))
	defer server.Close()
	c, err := New(Config{Endpoint: server.URL, Region: "us-east-1", Bucket: "tag-files", AccessKey: "explicit-access", SecretKey: "explicit-secret"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.StorageEpoch(context.Background())
	if err != nil || got != epoch {
		t.Fatalf("epoch discovery: %q %v", got, err)
	}
	fenced, err := c.AtEpoch(got)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := fenced.CreateMultipart(context.Background(), "source", "application/octet-stream", nil); err != nil || id != "owned-mpu" {
		t.Fatalf("fenced create: %q %v", id, err)
	}
}

func TestStorageWithoutAnOwnedEpochCannotProvideSettlementProof(t *testing.T) {
	for _, header := range []string{"", "invalid"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("X-Tag-Files-Storage-Epoch", header) }))
		c, err := New(Config{Endpoint: server.URL, Region: "us-east-1", Bucket: "tag-files", AccessKey: "explicit-access", SecretKey: "explicit-secret"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.StorageEpoch(context.Background())
		server.Close()
		if !errors.Is(err, ErrNoStorageEpoch) {
			t.Fatalf("unowned epoch accepted: %v", err)
		}
	}
}

func TestAmbiguousEpochHeadersAreNotSettlementProof(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("X-Tag-Files-Storage-Epoch", "11111111-1111-4111-8111-111111111111")
		w.Header().Add("X-Tag-Files-Storage-Epoch", "22222222-2222-4222-8222-222222222222")
	}))
	defer server.Close()
	c, err := New(Config{Endpoint: server.URL, Region: "us-east-1", Bucket: "tag-files", AccessKey: "explicit-access", SecretKey: "explicit-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.StorageEpoch(context.Background()); !errors.Is(err, ErrNoStorageEpoch) {
		t.Fatalf("ambiguous storage lifetime accepted: %v", err)
	}
}
