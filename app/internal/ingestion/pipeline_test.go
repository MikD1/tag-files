package ingestion

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/media"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/scratch"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func TestNativeIngestPipelineStreamsVerifiesAndPublishesActualPhoto(t *testing.T) {
	if os.Getenv("TAG_FILES_MEDIA_INTEGRATION") != "1" {
		t.Skip("run native ingestion/media integration")
	}
	for _, mode := range []string{"ready", "wrong-source", "no-space"} {
		t.Run(mode, func(t *testing.T) {
			db := database(t)
			var source bytes.Buffer
			if err := png.Encode(&source, image.NewNRGBA(image.Rect(0, 0, 13, 9))); err != nil {
				t.Fatal(err)
			}
			data := source.Bytes()
			sum := sha256.Sum256(data)
			var session Session
			transaction(t, db, func(tx *sql.Tx) error {
				batch, err := Create(testContext, tx, []File{{ClientFileID: domain.NewUUID(), Filename: "misleading.txt", Size: int64(len(data))}}, nil, DefaultLimits(), testTime)
				if err != nil {
					return err
				}
				session = batch.Sessions[0]
				if err := AttachMultipart(testContext, tx, session.ID, 1, "kept"); err != nil {
					return err
				}
				q := dbgen.New(tx)
				if err := q.QueueVerifiedUpload(testContext, dbgen.QueueVerifiedUploadParams{ID: session.ID, SourceSha256: sql.NullString{String: hex.EncodeToString(sum[:]), Valid: true}}); err != nil {
					return err
				}
				_, err = q.CreateJob(testContext, dbgen.CreateJobParams{ID: domain.NewUUID(), Type: domain.JobIngest, SubjectID: session.ID, DedupeKey: "ingest/" + session.ID, PayloadJson: `{"version":1,"uploadId":"` + session.ID + `","generation":1}`, NextAttemptAt: store.Timestamp(testTime), CreatedAt: store.Timestamp(testTime)})
				return err
			})
			job, err := db.ClaimJob(testContext, testTime, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			var stored []byte
			gets, puts := 0, 0
			c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				key := strings.TrimPrefix(r.URL.Path, "/tag-files/")
				if key == session.IncomingKey {
					if r.Method != "GET" {
						t.Errorf("source removed before cleanup: %s", r.Method)
						w.WriteHeader(500)
						return
					}
					gets++
					if mode == "wrong-source" {
						bad := append([]byte(nil), data...)
						bad[len(bad)-1] ^= 1
						w.Write(bad)
					} else {
						w.Write(data)
					}
					return
				}
				switch r.Method {
				case "HEAD":
					if stored == nil {
						w.WriteHeader(404)
						return
					}
					w.Header().Set("Content-Length", strconv.Itoa(len(stored)))
				case "PUT":
					puts++
					stored, _ = io.ReadAll(r.Body)
					var n int
					if err := db.Reader().QueryRow("SELECT count(*) FROM assets WHERE object_key=? AND state='candidate'", key).Scan(&n); err != nil || n != 1 {
						t.Error("PUT before durable candidate")
					}
				case "GET":
					w.Write(stored)
				default:
					t.Errorf("unexpected object operation %s", r.Method)
					w.WriteHeader(500)
				}
			}))
			config := media.DefaultConfig()
			config.MaxOutputBytes = 1024 * 1024
			p, err := media.New(config)
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			quota := int64(4 * 1024 * 1024)
			if mode == "no-space" {
				quota = 1024
			}
			sandbox, err := scratch.Open(scratch.Config{Root: root, MaxBytes: quota})
			if err != nil {
				t.Fatal(err)
			}
			err = c.ProcessIngest(testContext, job, p, sandbox, testTime)
			if closeErr := sandbox.Close(); closeErr != nil {
				t.Fatal("pipeline returned with live scratch", closeErr)
			}
			if mode == "wrong-source" {
				if !errors.Is(err, objectstore.ErrIntegrity) || puts != 0 || tableCount(t, db, "assets") != 0 {
					t.Fatalf("unverified source published: %v puts=%d", err, puts)
				}
			} else if mode == "no-space" {
				if !errors.Is(err, scratch.ErrInsufficientStorage) || gets != 0 || puts != 0 {
					t.Fatalf("capacity admission did IO: %v gets=%d puts=%d", err, gets, puts)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				outcome, err := Lookup(testContext, db.Reader(), session.ID)
				if err != nil || outcome.State != "ready" || gets != 1 || puts != 1 || !bytes.Equal(stored, data) || tableCount(t, db, "upload_sessions") != 0 {
					t.Fatalf("pipeline failed ready: %+v %v get=%d put=%d", outcome, err, gets, puts)
				}
				var jobs int
				if err := db.Reader().QueryRow("SELECT count(*) FROM jobs WHERE type IN ('cleanup','poster')").Scan(&jobs); err != nil || jobs != 2 {
					t.Fatalf("independent follow-up work missing: %d %v", jobs, err)
				}
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 1 || entries[0].Name() != ".scratch.lock" {
				t.Fatalf("attempt leaked after producers returned: %v %v", entries, err)
			}
		})
	}
}

func TestNativeIngestCancellationAndCapacityMonitorJoinSourceReader(t *testing.T) {
	if os.Getenv("TAG_FILES_MEDIA_INTEGRATION") != "1" {
		t.Skip("requires media profile")
	}
	for _, mode := range []string{"cancel", "budget"} {
		t.Run(mode, func(t *testing.T) {
			db := database(t)
			session, job := processingJob(t, db)
			started := make(chan struct{})
			stopped := make(chan struct{})
			c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/source") {
					t.Errorf("unexpected source operation %s", r.Method)
					w.WriteHeader(500)
					return
				}
				w.Header().Set("Content-Length", "7")
				w.Write([]byte("c"))
				w.(http.Flusher).Flush()
				close(started)
				<-r.Context().Done()
				close(stopped)
			}))
			config := media.DefaultConfig()
			config.MaxOutputBytes = 2 * 1024 * 1024
			p, err := media.New(config)
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			sandbox, err := scratch.Open(scratch.Config{Root: root, MaxBytes: 3 * 1024 * 1024})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(testContext, 4*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- c.ProcessIngest(ctx, job, p, sandbox, testTime) }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("source download did not begin")
			}
			if mode == "cancel" {
				cancelled(t, db, session.ID)
				cancel()
			} else {
				if err := os.WriteFile(filepath.Join(root, "retained-cache"), make([]byte, 2*1024*1024), 0600); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				want := error(context.Canceled)
				if mode == "budget" {
					want = scratch.ErrInsufficientStorage
				}
				if !errors.Is(err, want) {
					t.Fatalf("source not interrupted by %s: %v", mode, err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("pipeline retained live source reader")
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("source body not closed before scratch removal")
			}
			if err := sandbox.Close(); err != nil {
				t.Fatal("pipeline retained scratch ownership", err)
			}
			if tableCount(t, db, "assets") != 0 || tableCount(t, db, "items") != 0 {
				t.Fatal("interrupted source was published")
			}
			if mode == "budget" {
				if _, err := os.Stat(filepath.Join(root, "retained-cache")); err != nil {
					t.Fatal("monitor deleted unrelated retained bytes")
				}
			}
		})
	}
}
