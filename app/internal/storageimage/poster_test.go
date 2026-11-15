package storageimage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/ingestion"
	"github.com/mikd1/tag-files/app/internal/librarybinding"
	"github.com/mikd1/tag-files/app/internal/media"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/poster"
	"github.com/mikd1/tag-files/app/internal/scratch"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
	"github.com/mikd1/tag-files/app/internal/testutil"
	"github.com/mikd1/tag-files/app/internal/worker"
)

func TestPosterOwnedImageLostPutPublicationAndOrphanCleanupAcrossRestart(t *testing.T) {
	if os.Getenv("TAG_FILES_MEDIA_INTEGRATION") != "1" {
		t.Skip("requires actual media tools")
	}
	f := create(t)
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root := t.TempDir()
	db, err := store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	endpoint, _ := url.Parse(f.endpoint())
	var target atomic.Pointer[url.URL]
	target.Store(endpoint)
	proxy := httputil.NewSingleHostReverseProxy(endpoint)
	proxy.Director = func(r *http.Request) { httputil.NewSingleHostReverseProxy(target.Load()).Director(r) }
	var lose atomic.Bool
	proxy.ModifyResponse = func(res *http.Response) error {
		if res.Request.Method == "PUT" && strings.Contains(res.Request.URL.Path, "/previews/") && res.StatusCode == 200 && lose.Swap(false) {
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			return errors.New("lost durable poster PUT reply")
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) { w.WriteHeader(502) }
	peer := httptest.NewServer(proxy)
	defer peer.Close()
	httpClient := &http.Client{Timeout: 60 * time.Second}
	control, err := storageprotocol.NewClient(peer.URL, f.access, f.secret, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := librarybinding.Ensure(ctx, db, control)
	if err != nil {
		t.Fatal(err)
	}
	base, err := objectstore.New(objectstore.Config{Endpoint: peer.URL, Region: "us-east-1", Bucket: "tag-files", AccessKey: f.access, SecretKey: f.secret, HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	client, err := base.AtLibrary(pair.StorageID, pair.LibraryID, pair.VolumeID)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := objectio.New(db, client, 2)
	if err != nil {
		t.Fatal(err)
	}
	s, err := poster.New(db, manager)
	if err != nil {
		t.Fatal(err)
	}
	var source bytes.Buffer
	if err := png.Encode(&source, image.NewNRGBA(image.Rect(0, 0, 13, 9))); err != nil {
		t.Fatal(err)
	}
	sourceSum := sha256.Sum256(source.Bytes())
	sourceHash := hex.EncodeToString(sourceSum[:])
	input := filepath.Join(t.TempDir(), "primary")
	if err := os.WriteFile(input, source.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := media.New(media.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := p.PreparePoster(ctx, input, filepath.Join(t.TempDir(), "poster"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(prepared.Path)
	if err != nil {
		t.Fatal(err)
	}
	var published, orphan, interrupted dbgen.Asset
	var primaries []string
	for _, mode := range []string{"published", "orphan", "interrupted"} {
		primaryID := domain.NewUUID()
		primaryKey := "primary/" + pair.LibraryID + "/" + primaryID + "/media.png"
		primaries = append(primaries, primaryKey)
		if err := client.Put(ctx, primaryKey, bytes.NewReader(source.Bytes()), int64(source.Len()), sourceHash, "image/png", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Verify(ctx, primaryKey, int64(source.Len()), sourceHash); err != nil {
			t.Fatal(err)
		}
		err := db.Write(ctx, func(tx *sql.Tx) error {
			var itemID int64
			if err := tx.QueryRow("INSERT INTO items(media_kind,uploaded_at_utc,state) VALUES('Image',?,'importing') RETURNING id", store.Timestamp(time.Now())).Scan(&itemID); err != nil {
				return err
			}
			if _, err := tx.Exec("INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,width,height,rotation,provenance,state,media_kind) VALUES(?,?,'primary','tag-files',?,?,?,'image/png',13,9,0,'legacy','published','Image')", primaryID, itemID, primaryKey, source.Len(), sourceHash); err != nil {
				return err
			}
			q := dbgen.New(tx)
			if err := q.MarkItemReady(ctx, dbgen.MarkItemReadyParams{ID: itemID, PrimaryAssetID: sql.NullString{String: primaryID, Valid: true}}); err != nil {
				return err
			}
			body, _ := json.Marshal(poster.Payload{Version: 1, ItemID: itemID, PrimaryAssetID: primaryID, Profile: poster.Profile})
			now := store.Timestamp(time.Now())
			_, err := q.CreateJob(ctx, dbgen.CreateJobParams{ID: domain.NewUUID(), Type: domain.JobPoster, SubjectID: strconv.FormatInt(itemID, 10), DedupeKey: "poster/" + primaryID + "/" + poster.Profile, PayloadJson: string(body), NextAttemptAt: now, CreatedAt: now})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		job, err := db.ClaimJobKinds(ctx, []domain.JobKind{domain.JobPoster}, time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		a, err := s.NewCandidate(ctx, job, prepared.Output, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if mode == "interrupted" {
			interrupted = a
			continue
		}
		lose.Store(true)
		if _, err := s.Upload(ctx, job, a.ID, bytes.NewReader(data), time.Now()); err == nil {
			t.Fatal("actual PUT reply was not lost")
		}
		if _, err := s.Upload(ctx, job, a.ID, bytes.NewReader(data), time.Now()); err != nil {
			t.Fatal("actual full GET did not recover candidate", err)
		}
		if mode == "published" {
			published, err = s.Publish(ctx, job, a.ID, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if err := s.ReconcileWrites(ctx, a.ID); !errors.Is(err, objectio.ErrUnsettled) {
				t.Fatalf("same epoch settled published writer: %v", err)
			}
		} else {
			orphan = a
			if err := db.FailJob(ctx, job.ID, job.LeaseGeneration, time.Now(), "fixture_failure", nil); err != nil {
				t.Fatal(err)
			}
			cleanup, err := db.ClaimJobKinds(ctx, []domain.JobKind{domain.JobCleanup}, time.Now(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.ProcessCleanup(ctx, cleanup, time.Now()); !errors.Is(err, objectio.ErrUnsettled) {
				t.Fatalf("same epoch settled orphan writer: %v", err)
			}
		}
	}
	if err := os.Remove(input); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(prepared.Path); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	testutil.Docker(t, "", "kill", "--signal", "KILL", f.name)
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	endpoint, _ = url.Parse(f.endpoint())
	target.Store(endpoint)
	db, err = store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := librarybinding.Ensure(ctx, db, control); err != nil {
		t.Fatal(err)
	}
	if _, err := ingestion.RecoverInterrupted(ctx, db, time.Now()); err != nil {
		t.Fatal(err)
	}
	manager, err = objectio.New(db, client, 2)
	if err != nil {
		t.Fatal(err)
	}
	s, err = poster.New(db, manager)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileWrites(ctx, published.ID); err != nil {
		t.Fatal("published poster unknown writer not retired after actual restart", err)
	}
	sandbox, err := scratch.Open(scratch.Config{Root: t.TempDir(), MaxBytes: 32 * 1024 * 1024 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer sandbox.Close()
	if _, err := sandbox.Reclaim(); err != nil {
		t.Fatal(err)
	}
	c, err := ingestion.NewCoordinator(db, manager)
	if err != nil {
		t.Fatal(err)
	}
	r, err := worker.New(db, map[domain.JobKind]worker.Handler{domain.JobPoster: worker.Poster(s, db, p, sandbox), domain.JobCleanup: worker.Cleanup(c, db, s)}, worker.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		ran, err := r.RunOne(ctx)
		if err != nil {
			t.Fatal("poster recovery worker", err)
		}
		if !ran {
			break
		}
		if i == 7 {
			t.Fatal("poster recovery did not drain")
		}
	}
	var n int
	if err := db.Reader().QueryRow("SELECT count(*) FROM object_write_intents").Scan(&n); err != nil || n != 0 {
		t.Fatalf("unknown writers remain: %d %v", n, err)
	}
	if err := db.Reader().QueryRow("SELECT count(*) FROM cleanup_receipts").Scan(&n); err != nil || n != 0 {
		t.Fatal("orphan cleanup receipt remains", err)
	}
	if err := db.Reader().QueryRow("SELECT count(*) FROM assets WHERE role='poster' AND state='published'").Scan(&n); err != nil || n != 2 {
		t.Fatalf("interrupted poster did not resume from primary: %d %v", n, err)
	}
	if _, err := dbgen.New(db.Reader()).GetCleanupAsset(ctx, interrupted.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("interrupted candidate not independently cleaned: %v", err)
	}
	for _, key := range primaries {
		if _, err := client.Verify(ctx, key, int64(source.Len()), sourceHash); err != nil {
			t.Fatal("primary lost", err)
		}
	}
	if _, err := client.Verify(ctx, published.ObjectKey, published.SizeBytes, published.Sha256); err != nil {
		t.Fatal("published poster lost", err)
	}
	var wire *objectstore.Error
	if _, err := client.Head(ctx, orphan.ObjectKey); !errors.As(err, &wire) || wire.Status != 404 {
		t.Fatalf("orphan object remains: %v", err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		t.Fatal("native output is not decodable JPEG", err)
	}
}
