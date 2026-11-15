// This offline fixture writer is test preparation, never an application feature.
// All objects are real private S3 PUTs, followed by complete size/SHA verification.
// Catalog rows are seeded after those calls finish and before the app starts.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/store"
)

const items = 10000

type scenario struct {
	Name        string `json:"name"`
	Query       string `json:"query"`
	ExpectedIDs []int  `json:"expectedIDs"`
	Total       int    `json:"total"`
}

type payload struct {
	Data        []byte `json:"-"`
	Bytes       int    `json:"bytes"`
	SHA256      string `json:"sha256"`
	ContentType string `json:"contentType"`
}

func assetID(item int, poster bool) string {
	role := 1
	if poster {
		role = 2
	}
	return fmt.Sprintf("00000000-0000-4000-800%d-%012d", role, item)
}

func tagsFor(id int) []int {
	tags := make([]int, 10)
	for index := range tags {
		tags[index] = 1 + (id*7+index*137)%2000
	}
	return tags
}

func objectKey(library string, item int, poster bool) string {
	asset := assetID(item, poster)
	if poster {
		return "previews/" + library + "/" + asset + "/fixture-v1/poster.jpg"
	}
	return "primary/" + library + "/" + asset + "/media.png"
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	root := os.Getenv("TAG_FILES_BROWSER_PERFORMANCE_WORK")
	if root == "" {
		return fmt.Errorf("explicit private fixture work directory required")
	}
	secret := func(name string) (string, error) {
		value, err := os.ReadFile(filepath.Join(root, "secrets", name))
		return strings.TrimSpace(string(value)), err
	}
	access, err := secret("s3_access_key")
	if err != nil {
		return err
	}
	key, err := secret("s3_secret_key")
	if err != nil {
		return err
	}
	client, err := objectstore.New(objectstore.Config{Endpoint: os.Getenv("TAG_FILES_BROWSER_PERFORMANCE_S3"), Region: "us-east-1", Bucket: "tag-files", AccessKey: access, SecretKey: key, HTTPClient: &http.Client{Timeout: 60 * time.Second}})
	if err != nil {
		return err
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 640, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 640; x++ {
			canvas.SetRGBA(x, y, color.RGBA{uint8(x*120/640 + 30), uint8(y*100/400 + 80), 110, 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		return err
	}
	var jpegBytes bytes.Buffer
	if err := jpeg.Encode(&jpegBytes, canvas, &jpeg.Options{Quality: 80}); err != nil {
		return err
	}
	payloads := map[bool]payload{}
	for poster, data := range map[bool][]byte{false: encoded.Bytes(), true: jpegBytes.Bytes()} {
		contentType := "image/png"
		if poster {
			contentType = "image/jpeg"
		}
		payloads[poster] = payload{Data: data, Bytes: len(data), SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), ContentType: contentType}
	}
	db, err := store.Open(ctx, filepath.Join(root, "metadata"))
	if err != nil {
		return err
	}
	defer db.Close()
	var library string
	if err := db.Reader().QueryRowContext(ctx, "SELECT library_id FROM library_meta WHERE singleton=1").Scan(&library); err != nil {
		return err
	}
	for _, poster := range []bool{false, true} {
		role := "primary"
		if poster {
			role = "poster"
		}
		if !domain.OwnedObjectKey(library, assetID(1, poster), role, objectKey(library, 1, poster)) {
			return fmt.Errorf("fixture key does not satisfy domain ownership")
		}
	}
	started := time.Now()
	// Eight preparation clients keep the fixture practical; no benchmark runs
	// concurrently with this seeding or its full GET/hash verification.
	jobs := make(chan int)
	errors := make(chan error, 8)
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			for id := range jobs {
				for _, poster := range []bool{false, true} {
					key := objectKey(library, id, poster)
					value := payloads[poster]
					if err := client.Put(ctx, key, bytes.NewReader(value.Data), int64(value.Bytes), value.SHA256, value.ContentType, map[string]string{"provenance": "browser-performance-fixture"}); err != nil {
						errors <- err
						cancel()
						return
					}
					if _, err := client.Verify(ctx, key, int64(value.Bytes), value.SHA256); err != nil {
						errors <- err
						cancel()
						return
					}
				}
			}
		})
	}
	for id := 1; id <= items; id++ {
		select {
		case jobs <- id:
		case <-ctx.Done():
			break
		}
	}
	close(jobs)
	group.Wait()
	close(errors)
	for err := range errors {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Inventory proves unique native keys, including listings after page 1000.
	count, objectBytes := 0, int64(0)
	for _, prefix := range []string{"primary/" + library + "/", "previews/" + library + "/"} {
		token := ""
		for {
			page, err := client.List(ctx, prefix, token, 1000)
			if err != nil {
				return err
			}
			for _, object := range page.Objects {
				count++
				objectBytes += object.Size
			}
			if page.NextToken == "" {
				break
			}
			token = page.NextToken
		}
	}
	if count != items*2 || objectBytes != int64(items*(payloads[false].Bytes+payloads[true].Bytes)) {
		return fmt.Errorf("S3 inventory mismatch: %d objects/%d bytes", count, objectBytes)
	}
	err = db.Write(ctx, func(tx *sql.Tx) error {
		for id := 1; id <= 2000; id++ {
			if _, err := tx.ExecContext(ctx, "INSERT INTO tags(id,name) VALUES(?,?)", id, fmt.Sprintf("tag%04d", id)); err != nil {
				return err
			}
		}
		for id := 1; id <= 350; id++ {
			if _, err := tx.ExecContext(ctx, "INSERT INTO collections(id,name) VALUES(?,?)", id, fmt.Sprintf("Collection%03d", id)); err != nil {
				return err
			}
		}
		for id := 1; id <= items; id++ {
			if _, err := tx.ExecContext(ctx, "INSERT INTO items(id,media_kind,uploaded_at_utc,state,collection_id) VALUES(?,'Image',?,'importing',?)", id, time.Date(2026, 1, 1, 0, 0, id, 0, time.UTC).Format(time.RFC3339Nano), 1+id%350); err != nil {
				return err
			}
			for _, poster := range []bool{false, true} {
				role := "primary"
				if poster {
					role = "poster"
				}
				asset := assetID(id, poster)
				value := payloads[poster]
				if _, err := tx.ExecContext(ctx, `INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,width,height,provenance,state) VALUES(?,?,?,'tag-files',?,?,?,?,640,400,'browser-performance-fixture','verified')`, asset, id, role, objectKey(library, id, poster), value.Bytes, value.SHA256, value.ContentType); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, "UPDATE items SET state='ready',primary_asset_id=? WHERE id=?", assetID(id, false), id); err != nil {
				return err
			}
			for _, tag := range tagsFor(id) {
				if _, err := tx.ExecContext(ctx, "INSERT INTO item_tags VALUES(?,?)", id, tag); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	var nItems, nTags, nLinks, nCollections, nAssets int
	if err := db.Reader().QueryRowContext(ctx, `SELECT (SELECT count(*) FROM items),(SELECT count(*) FROM tags),(SELECT count(*) FROM item_tags),(SELECT count(*) FROM collections),(SELECT count(*) FROM assets)`).Scan(&nItems, &nTags, &nLinks, &nCollections, &nAssets); err != nil {
		return err
	}
	if nItems != items || nTags != 2000 || nLinks != 100000 || nCollections != 350 || nAssets != 20000 {
		return fmt.Errorf("SQL fixture counts mismatch")
	}
	cases := []scenario{{Name: "library"}, {Name: "simple-search", Query: "tag0001 || tag0002"}, {Name: "compound-search", Query: "(tag0001 || tag0002) && !tag0138"}}
	// Independent deterministic fixture oracle, not a second HTTP search parser.
	for index := range cases {
		for id := items; id >= 1; id-- {
			has := map[int]bool{}
			for _, tag := range tagsFor(id) {
				has[tag] = true
			}
			match := index == 0 || (has[1] || has[2]) && (index != 2 || !has[138])
			if match {
				cases[index].Total++
				if len(cases[index].ExpectedIDs) < 24 {
					cases[index].ExpectedIDs = append(cases[index].ExpectedIDs, id)
				}
			}
		}
		if len(cases[index].ExpectedIDs) != 24 {
			return fmt.Errorf("scenario lacks useful first page: %s", cases[index].Name)
		}
	}
	manifest := map[string]any{"items": nItems, "tags": nTags, "links": nLinks, "collections": nCollections, "assets": nAssets, "objects": count, "objectBytes": objectBytes, "primary": payloads[false], "poster": payloads[true], "width": 640, "height": 400, "preparationSeconds": time.Since(started).Seconds(), "scenarios": cases}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "fixture.json"), append(raw, '\n'), 0600); err != nil {
		return err
	}
	log.Printf("Fixture verified: %d items, %d tags, %d links, %d collections; %d actual objects/%d bytes; preparation=%s", nItems, nTags, nLinks, nCollections, count, objectBytes, time.Since(started))
	return db.Close()
}
