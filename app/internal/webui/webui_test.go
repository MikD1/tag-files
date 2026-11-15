package webui_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikd1/tag-files/app/internal/webui"
)

func TestBuiltSPADeepLinksPreserveAPIAndMediaProtocols(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"index.html": "<!doctype html><div id=\"root\">fixture</div>", "assets/index-abc123.js": "console.log('fixture')"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(418)
		_, _ = w.Write([]byte(`{"code":"upstream"}`))
	})
	h, err := webui.New(root, upstream)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/library?page=2", "/content/42?origin=%2Flibrary", "/uploads", "/categories"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "fixture") || w.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("deep link %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/unknown", "/media/missing", "/health/ready"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 418 || w.Header().Get("Content-Type") != "application/problem+json" {
			t.Fatalf("protocol intercepted: %s %d", path, w.Code)
		}
	}
	for method, want := range map[string]int{"GET": 200, "HEAD": 200, "POST": 405} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, "/assets/index-abc123.js", nil))
		if w.Code != want {
			t.Fatalf("asset %s: %d", method, w.Code)
		}
		if method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal("HEAD body")
		}
		if method == "GET" && !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
			t.Fatal("hashed assets are not immutable")
		}
	}
	for _, path := range []string{"/assets/stale.js", "/.env", "/src/main.tsx", "/assets/../index.html"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatalf("unexpected static exposure %s: %d", path, w.Code)
		}
	}
}

func TestMissingOrInvalidBuildRefusedBeforeServing(t *testing.T) {
	for _, body := range []string{"", "not an SPA"} {
		root := t.TempDir()
		if body != "" {
			_ = os.WriteFile(filepath.Join(root, "index.html"), []byte(body), 0600)
		}
		if _, err := webui.New(root, http.NotFoundHandler()); err == nil {
			t.Fatal("invalid build accepted")
		}
	}
}
