package httpapi

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/httpapi/api"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func TestSystemRoutesDistinguishProcessLivenessFromRuntimeReadiness(t *testing.T) {
	h := setup(t)
	if out := h.request("GET", "/health/live", ""); out.Code != 200 || out.Body.Len() != 0 {
		t.Fatalf("process is serving but not live: %d %s", out.Code, out.Body.String())
	}
	out := h.request("GET", "/health/ready", "")
	if out.Code != 503 {
		t.Fatalf("unwired runtime claimed ready: %d %s", out.Code, out.Body.String())
	}
}

func TestSystemStatusReportsLibraryAndDurableQueueWithoutInventedCapacity(t *testing.T) {
	h := setup(t)
	if err := h.s.Write(ctx, func(tx *sql.Tx) error {
		_, err := dbgen.New(tx).CreateJob(ctx, dbgen.CreateJobParams{ID: domain.NewUUID(), Type: domain.JobPoster, SubjectID: "1", DedupeKey: "pending-poster", PayloadJson: `{}`, NextAttemptAt: "2026-11-14T00:00:00.0000000Z", CreatedAt: "2026-11-14T00:00:00.0000000Z"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	out := h.request("GET", "/api/v1/system/status", "")
	if out.Code != 200 {
		t.Fatalf("system status: %d %s", out.Code, out.Body.String())
	}
	var status api.SystemStatus
	if err := json.Unmarshal(out.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	meta, err := dbgen.New(h.s.Reader()).GetLibraryMeta(ctx)
	if err != nil || status.LibraryId.String() != meta.LibraryID || status.Queue["queued"] != 1 || status.Queue["running"] != 0 || status.AppVersion == "" || status.SchemaVersion != store.SchemaVersion || status.FormatVersion != 1 {
		t.Fatalf("incorrect library/queue: %+v %v", status, err)
	}
	storage, ok := status.Disk["storage"].(map[string]any)
	if !ok || storage["availableBytes"] != nil || storage["status"] != "unknown" {
		t.Fatalf("unmeasured S3 capacity was invented: %+v", status.Disk)
	}
}
