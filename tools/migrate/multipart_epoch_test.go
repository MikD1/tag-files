package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikd1/tag-files/app/internal/store"
)

// A Create request with an uncertain outcome and no visible MPU must remain
// fenced until another storage epoch. Inspect a separate connection during the
// actual dispatch to prove the attempt epoch is already durably journaled.
type lostCreateDestination struct {
	*fakeMultipartDestination
	epoch   string
	calls   int
	journal string
	t       *testing.T
}

func (d *lostCreateDestination) BoundEpoch() string { return d.epoch }
func (d *lostCreateDestination) CreateMultipart(_ context.Context, key, _ string, _ map[string]string) (string, error) {
	d.t.Helper()
	d.calls++
	db, err := sql.Open("sqlite3", d.journal+"?mode=ro&_query_only=true")
	if err != nil {
		d.t.Fatal(err)
	}
	defer db.Close()
	var epoch, state, uploadID string
	err = db.QueryRow(`SELECT m.epoch,m.state,m.upload_id FROM multipart AS m JOIN objects AS o ON o.id=m.object_id WHERE o.object_key=?`, key).Scan(&epoch, &state, &uploadID)
	if err != nil {
		d.t.Fatal(err)
	}
	if epoch != d.epoch || state != "creating" || uploadID != "" {
		d.t.Errorf("Create dispatched before its journal fence: current=%q persisted=%q state=%q uploadID=%q", d.epoch, epoch, state, uploadID)
	}
	return "", errors.New("lost CreateMultipart response")
}

func TestEachUncertainMultipartCreatePersistsEpochBeforeDispatch(t *testing.T) {
	p := legacyFixture(t)
	source := fixtureSource()
	large := bytes.Repeat([]byte("abcdef"), 2<<20)
	for index, object := range source.objects {
		if object.Key == "video.mp4" {
			source.objects[index].Size = int64(len(large))
			source.data[object.Identity()] = large
		}
	}
	archive := filepath.Join(t.TempDir(), "archive")
	m, err := Scan(ctx, ScanConfig{Database: p, Archive: archive, SnapshotID: "lost-create-epochs", RollbackReference: "cold fixture", RollbackVerified: true}, source)
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.Open(ctx, filepath.Join(t.TempDir(), "target"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	c := ApplyConfig{Archive: archive, Journal: filepath.Join(t.TempDir(), "journal.sqlite"), TargetID: "create-epochs", PartSize: 5 << 20, SinglePutLimit: 5 << 20, Budget: Budget{ReserveBytes: 1}, Capacity: func(context.Context) (int64, int64, error) { return 1 << 40, 1 << 20, nil }}
	destination := &lostCreateDestination{fakeMultipartDestination: &fakeMultipartDestination{fakeDestination: newDestination()}, epoch: "epoch-1", journal: c.Journal, t: t}
	for _, step := range []struct {
		epoch   string
		calls   int
		message string
	}{
		{"epoch-1", 1, "lost CreateMultipart response"},
		{"epoch-1", 1, "cannot be repeated before a storage epoch change"},
		{"", 1, "cannot be repeated before a storage epoch change"},
		{"epoch-2", 2, "lost CreateMultipart response"},
		{"epoch-2", 2, "cannot be repeated before a storage epoch change"},
	} {
		destination.epoch = step.epoch
		// Apply closes/reopens its real WAL/FULL journal on every invocation.
		err = Apply(ctx, c, m, source, target, destination)
		if err == nil || !strings.Contains(err.Error(), step.message) {
			t.Errorf("%s calls=%d: expected %q, got %v", step.epoch, step.calls, step.message, err)
		}
		if destination.calls != step.calls {
			t.Errorf("%s dispatched %d Create requests; expected %d", step.epoch, destination.calls, step.calls)
		}
	}
}
