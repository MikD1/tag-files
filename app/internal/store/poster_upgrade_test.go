package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
	"github.com/pressly/goose/v3"
)

func TestSchemaSixUpgradeRebuildsOnlyPublishedDerivedVideoPosters(t *testing.T) {
	root := t.TempDir()
	old, err := sql.Open("sqlite3", dsn(filepath.Join(root, "library.sqlite"), false))
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	for _, name := range []string{"00001_library.sql", "00002_object_write_intents.sql", "00003_candidate_descriptor.sql", "00004_storage_pair.sql", "00005_deletion_inventory.sql", "00006_complete_commands.sql"} {
		data, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: data}
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, old, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var library string
	if err := old.QueryRow("SELECT library_id FROM library_meta").Scan(&library); err != nil {
		t.Fatal(err)
	}
	type fixture struct {
		kind, provenance, profile, state, posterState, jobState string
		repair                                                  bool
	}
	cases := []fixture{
		{"Video", "derived", "poster-v1", "ready", "published", "", true},
		{"Video", "derived", "poster-v1", "ready", "published", "queued", true},
		{"Video", "derived", "poster-v1", "ready", "published", "failed", true},
		{"Image", "derived", "poster-v1", "ready", "published", "", false},
		{"Video", "legacy", "poster-v1", "ready", "published", "", false},
		{"Video", "derived", "future", "ready", "published", "", false},
		{"Video", "derived", "poster-v1", "missing", "published", "", false},
		{"Video", "derived", "poster-v1", "deleting", "published", "", false},
		{"Video", "derived", "poster-v1", "ready", "candidate", "", false},
		{"Video", "derived", "poster-v1", "ready", "published", "future", true},
	}
	primaries := make([]string, len(cases))
	posters := make([]string, len(cases))
	items := make([]dbgen.Item, len(cases))
	assets := make([]dbgen.Asset, len(cases))
	jobs := map[int]dbgen.Job{}
	for i, c := range cases {
		id := int64(i + 1)
		primaries[i], posters[i] = domain.NewUUID(), domain.NewUUID()
		if _, err := old.Exec("INSERT INTO items(id,media_kind,uploaded_at_utc,description,duration_ticks,state,version) VALUES(?,?,'2001-01-01T00:00:00.0000000Z','preserved',9223372036854775807,'importing',17)", id, c.kind); err != nil {
			t.Fatal(err)
		}
		if _, err := old.Exec("INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,state) VALUES(?,?,'primary','tag-files',?,123,?,'video/mp4','processed','published')", primaries[i], id, "primary/"+library+"/"+primaries[i]+"/media.mp4", strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
		if _, err := old.Exec("UPDATE items SET primary_asset_id=?,state=? WHERE id=?", primaries[i], c.state, id); err != nil {
			t.Fatal(err)
		}
		if _, err := old.Exec("INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,profile_version,state) VALUES(?,?,'poster','tag-files',?,456,?,'image/jpeg',?,?,?)", posters[i], id, "previews/"+library+"/"+posters[i]+"/"+c.profile+"/poster.jpg", strings.Repeat("b", 64), c.provenance, c.profile, c.posterState); err != nil {
			t.Fatal(err)
		}
		q := dbgen.New(old)
		if err := old.QueryRow("SELECT id,upload_id,description,media_kind,uploaded_at_utc,collection_id,is_favorite,view_count,last_view_at_utc,duration_ticks,primary_asset_id,state,version FROM items WHERE id=?", id).Scan(&items[i].ID, &items[i].UploadID, &items[i].Description, &items[i].MediaKind, &items[i].UploadedAtUtc, &items[i].CollectionID, &items[i].IsFavorite, &items[i].ViewCount, &items[i].LastViewAtUtc, &items[i].DurationTicks, &items[i].PrimaryAssetID, &items[i].State, &items[i].Version); err != nil {
			t.Fatal(err)
		}
		assets[i], err = q.GetCleanupAsset(ctx, posters[i])
		if err != nil {
			t.Fatal(err)
		}
		if c.jobState != "" {
			payload := fmt.Sprintf(`{"version":1,"itemId":%d,"primaryAssetId":%q,"profile":"poster-v1"}`, id, primaries[i])
			state := c.jobState
			if state == "future" {
				state = "failed"
				payload = strings.Replace(payload, `"version":1`, `"version":99`, 1)
			}
			jobID := domain.NewUUID()
			if _, err := old.Exec("INSERT INTO jobs(id,type,subject_id,dedupe_key,payload_json,state,attempt,next_attempt_at,lease_generation,error_code,created_at) VALUES(?,'poster',?,?,?,?,4,'2001-01-01T00:00:00.0000000Z',29,'fixture','2001-01-01T00:00:00.0000000Z')", jobID, fmt.Sprint(id), "poster/"+primaries[i]+"/poster-v1", payload, state); err != nil {
				t.Fatal(err)
			}
			jobs[i], err = q.GetJob(ctx, jobID)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := old.Exec("INSERT INTO tags(id,name) VALUES(11,'猫'),(12,'猫');INSERT INTO item_tags(item_id,tag_id) VALUES(1,11),(1,12);INSERT INTO cleanup_receipts(id,reason,payload_json,generation,state,created_at_utc) VALUES('kept','poster_candidate','{}',29,'pending','2001-01-01T00:00:00.0000000Z')"); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	for run := range 2 {
		db, err := Open(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		q := dbgen.New(db.Reader())
		for i, c := range cases {
			wantAsset := assets[i]
			wantItem := items[i]
			if c.repair {
				wantAsset.State = domain.AssetMissing
				wantItem.Version++
			}
			got, err := q.GetCleanupAsset(ctx, posters[i])
			if err != nil || !reflect.DeepEqual(got, wantAsset) {
				t.Fatalf("reopen %d case %d poster: got %+v want %+v err %v", run, i, got, wantAsset, err)
			}
			var actual dbgen.Item
			if err := db.Reader().QueryRow("SELECT id,upload_id,description,media_kind,uploaded_at_utc,collection_id,is_favorite,view_count,last_view_at_utc,duration_ticks,primary_asset_id,state,version FROM items WHERE id=?", wantItem.ID).Scan(&actual.ID, &actual.UploadID, &actual.Description, &actual.MediaKind, &actual.UploadedAtUtc, &actual.CollectionID, &actual.IsFavorite, &actual.ViewCount, &actual.LastViewAtUtc, &actual.DurationTicks, &actual.PrimaryAssetID, &actual.State, &actual.Version); err != nil || !reflect.DeepEqual(actual, wantItem) {
				t.Fatalf("reopen %d item %d changed: got %+v want %+v err %v", run, i, actual, wantItem, err)
			}
			if previous, ok := jobs[i]; ok {
				got, err := q.GetJob(ctx, previous.ID)
				if c.jobState == "failed" {
					previous.State, previous.ErrorCode = domain.JobQueued, sql.NullString{}
					previous.NextAttemptAt = got.NextAttemptAt
				}
				if err != nil || !reflect.DeepEqual(got, previous) {
					t.Fatalf("existing work altered: got %+v want %+v err %v", got, previous, err)
				}
			} else if c.repair {
				job, err := q.FindPosterJob(ctx, "poster/"+primaries[i]+"/poster-v1")
				var payload struct {
					Version int    `json:"version"`
					ItemID  int64  `json:"itemId"`
					Primary string `json:"primaryAssetId"`
					Profile string `json:"profile"`
				}
				if err != nil || json.Unmarshal([]byte(job.PayloadJson), &payload) != nil || payload.Version != 1 || payload.ItemID != wantItem.ID || payload.Primary != primaries[i] || payload.Profile != "poster-v1" || job.State != domain.JobQueued || job.Attempt != 0 || job.LeaseGeneration != 0 || domain.ValidateUUID(job.ID) != nil {
					t.Fatalf("missing compatible poster work: %+v %+v %v", job, payload, err)
				}
			}
		}
		for query, want := range map[string]int{"SELECT count(*) FROM jobs": 4, "SELECT count(*) FROM assets": 20, "SELECT count(*) FROM items": 10, "SELECT count(*) FROM tags WHERE name='猫'": 2, "SELECT count(*) FROM item_tags WHERE item_id=1": 2, "SELECT count(*) FROM cleanup_receipts WHERE id='kept' AND payload_json='{}' AND generation=29": 1} {
			var n int
			if err := db.Reader().QueryRow(query).Scan(&n); err != nil || n != want {
				t.Fatalf("%s: %d want %d, %v", query, n, want, err)
			}
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
