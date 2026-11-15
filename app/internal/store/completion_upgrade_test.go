package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
	"github.com/pressly/goose/v3"
)

func TestSchemaFiveUpgradeKeepsRunningJobsAndAdmitsDurableComplete(t *testing.T) {
	root := t.TempDir()
	old, err := sql.Open("sqlite3", dsn(filepath.Join(root, "library.sqlite"), false))
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	for _, name := range []string{"00001_library.sql", "00002_object_write_intents.sql", "00003_candidate_descriptor.sql", "00004_storage_pair.sql", "00005_deletion_inventory.sql"} {
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
	jobID := domain.NewUUID()
	if _, err := old.Exec("INSERT INTO jobs(id,type,subject_id,dedupe_key,payload_json,state,attempt,next_attempt_at,lease_until,lease_generation,created_at) VALUES(?,'cleanup','kept','kept','{}','running',2,'2026-01-01','2030-01-01',43,'2026-01-01')", jobID); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	job, err := dbgen.New(db.Reader()).GetJob(ctx, jobID)
	if err != nil || job.State != domain.JobRunning || job.Attempt != 2 || job.LeaseGeneration != 43 || job.PayloadJson != "{}" {
		t.Fatal("migration altered live work", job, err)
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := dbgen.New(tx).CreateJob(ctx, dbgen.CreateJobParams{ID: domain.NewUUID(), Type: domain.JobKind("complete"), SubjectID: "upload", DedupeKey: "complete/upload", PayloadJson: `{"version":1}`, CreatedAt: "2026-01-01", NextAttemptAt: "2026-01-01"})
		return err
	}); err != nil {
		t.Fatal("durable input verification was not admitted", err)
	}
}
