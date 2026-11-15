// Package webui serves the built, local-only SPA alongside the application API.
package webui

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func New(directory string, api http.Handler) (http.Handler, error) {
	index, err := os.ReadFile(filepath.Join(directory, "index.html"))
	if err != nil || !strings.Contains(string(index), `id="root"`) {
		return nil, fmt.Errorf("built web application is unavailable: %s", directory)
	}
	files := http.FileServer(http.Dir(directory))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/api" || strings.HasPrefix(p, "/api/") || p == "/media" || strings.HasPrefix(p, "/media/") || p == "/health" || strings.HasPrefix(p, "/health/") {
			api.ServeHTTP(w, r)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != "GET" && r.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", 405)
			return
		}
		if path.Clean(p) != p && p != "/" || strings.Contains(p, "\\") || strings.Contains(p, "/.") {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(p, "/assets/") {
			info, err := os.Stat(filepath.Join(directory, filepath.FromSlash(strings.TrimPrefix(p, "/"))))
			if err != nil || !info.Mode().IsRegular() {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			files.ServeHTTP(w, r)
			return
		}
		if path.Ext(p) != "" && p != "/index.html" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Length", fmt.Sprint(len(index)))
		if r.Method == "GET" {
			_, _ = w.Write(index)
		}
	}), nil
}
