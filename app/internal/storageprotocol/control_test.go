package storageprotocol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mikd1/tag-files/app/internal/domain"
)

type clientFunc func(*http.Request) (*http.Response, error)

func (f clientFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestControlRejectsTamperedRequestAndResponse(t *testing.T) {
	const access, secret = "private-test-access", "private-test-secret"
	identity := Identity{Version: 1, StorageID: domain.NewUUID(), LibraryID: domain.NewUUID(), Epoch: domain.NewUUID(), VolumeID: strings.Repeat("a", 64)}
	body, _ := json.Marshal(identity)
	var mutations atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if !Authenticate(r, data, access, secret) {
			w.WriteHeader(401)
			return
		}
		if r.Method == "POST" {
			mutations.Add(1)
		}
		Reply(w, r, secret, 200, body)
	}))
	defer server.Close()
	requestClient := clientFunc(func(r *http.Request) (*http.Response, error) {
		r.Body = io.NopCloser(bytes.NewReader([]byte(`{"version":1,"storageId":"changed"}`)))
		r.ContentLength = int64(len(`{"version":1,"storageId":"changed"}`))
		return server.Client().Do(r)
	})
	c, err := NewClient(server.URL, access, secret, requestClient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Bind(context.Background(), identity.StorageID, identity.LibraryID, identity.VolumeID); !errors.Is(err, ErrAuthentication) || mutations.Load() != 0 {
		t.Fatalf("modified request authenticated: %v mutations=%d", err, mutations.Load())
	}
	responseClient := clientFunc(func(r *http.Request) (*http.Response, error) {
		response, err := server.Client().Do(r)
		if err != nil {
			return nil, err
		}
		response.Body.Close()
		response.Body = io.NopCloser(bytes.NewReader(bytes.ReplaceAll(body, []byte(identity.StorageID), []byte(domain.NewUUID()))))
		return response, nil
	})
	c, err = NewClient(server.URL, access, secret, responseClient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Identity(context.Background()); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("modified identity authenticated: %v", err)
	}
}

func TestControlRejectsReplayUnknownVersionAndOversizedResponse(t *testing.T) {
	const access, secret = "private-test-access", "private-test-secret"
	for _, mode := range []string{"replay", "future", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			identity := Identity{Version: 1, StorageID: domain.NewUUID(), Epoch: domain.NewUUID(), VolumeID: strings.Repeat("a", 64)}
			if mode == "future" {
				identity.Version = 99
			}
			body, _ := json.Marshal(identity)
			if mode == "oversize" {
				body = bytes.Repeat([]byte("x"), MaxControlBytes+1)
			}
			var prior http.Header
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !Authenticate(r, nil, access, secret) {
					w.WriteHeader(401)
					return
				}
				if prior != nil {
					for key, values := range prior {
						w.Header()[key] = values
					}
					w.Write(body)
					return
				}
				Reply(w, r, secret, 200, body)
				if mode == "replay" {
					prior = w.Header().Clone()
				}
			}))
			defer server.Close()
			c, err := NewClient(server.URL, access, secret, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Identity(context.Background())
			if mode == "replay" {
				if err != nil {
					t.Fatal(err)
				}
				_, err = c.Identity(context.Background())
				if !errors.Is(err, ErrAuthentication) {
					t.Fatalf("old signed identity replayed: %v", err)
				}
			} else if !errors.Is(err, ErrProtocol) {
				t.Fatalf("unbounded/future control data accepted: %v", err)
			}
		})
	}
}
