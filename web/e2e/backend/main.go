// This disposable browser fixture runs the production HTTP/SQLite stack. Its
// generated PNG endpoint substitutes only media bytes; real S3/FFmpeg, upload and
// restore acceptance belong to the separate owned-image integration suites.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mikd1/tag-files/app/internal/httpapi"
	"github.com/mikd1/tag-files/app/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dir, err := os.MkdirTemp("", "tag-files-browser-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	s, err := store.Open(ctx, dir)
	if err != nil {
		return err
	}
	defer s.Close()
	canvas := image.NewRGBA(image.Rect(0, 0, 640, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 640; x++ {
			canvas.SetRGBA(x, y, color.RGBA{uint8(x*120/640 + 30), uint8(y*100/400 + 80), 110, 255})
		}
	}
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, canvas); err != nil {
		return err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(imageBytes.Bytes()))
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		for i := 1; i <= 350; i++ {
			if _, err := tx.Exec("INSERT INTO collections(id,name) VALUES(?,?)", i, fmt.Sprintf("Collection %03d", i)); err != nil {
				return err
			}
		}
		names := []string{"cat", "cat", "Cat", "犬", "猫", "nature", "travel"}
		for i, name := range names {
			if _, err := tx.Exec("INSERT INTO tags(id,name) VALUES(?,?)", i+1, name); err != nil {
				return err
			}
		}
		for id := 1; id <= 72; id++ {
			asset := fmt.Sprintf("00000000-0000-4000-8000-%012d", id)
			if _, err := tx.Exec("INSERT INTO items(id,media_kind,uploaded_at_utc,collection_id,description,state) VALUES(?,'Image',?,1,'Legacy text stays private','importing')", id, time.Date(2026, 1, 1, 0, 0, id, 0, time.UTC).Format(time.RFC3339Nano)); err != nil {
				return err
			}
			if _, err := tx.Exec("INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,state) VALUES(?,?,'primary','fixture',?,?,?,'image/png','fixture','verified')", asset, id, asset, imageBytes.Len(), digest); err != nil {
				return err
			}
			if _, err := tx.Exec("UPDATE items SET state='ready',primary_asset_id=? WHERE id=?", asset, id); err != nil {
				return err
			}
			if _, err := tx.Exec("INSERT INTO item_tags(item_id,tag_id) VALUES(?,?)", id, 1+(id%7)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	api, err := httpapi.New(s, httpapi.Options{Version: "browser-fixture"})
	if err != nil {
		return err
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/media/") {
			w.Header().Set("Content-Type", "image/png")
			http.ServeContent(w, r, "fixture.png", time.Time{}, bytes.NewReader(imageBytes.Bytes()))
			return
		}
		api.ServeHTTP(w, r)
	})
	server := &http.Server{Addr: "127.0.0.1:18080", Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Print("browser fixture: http://127.0.0.1:18080")
	err = server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
