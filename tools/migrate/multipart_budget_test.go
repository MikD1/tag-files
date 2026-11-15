package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/store"
)

type budgetMultipartDestination struct {
	*fakeMultipartDestination
	listChecksums bool
	stopComplete  bool
	completeCalls int
}

func (d *budgetMultipartDestination) ListParts(context.Context, string, string) ([]objectstore.Part, error) {
	parts := make([]objectstore.Part, 0, len(d.parts))
	for _, part := range d.parts {
		if !d.listChecksums {
			part.SHA256 = ""
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func (d *budgetMultipartDestination) Complete(ctx context.Context, key, id string, parts []objectstore.Part) (objectstore.Verified, error) {
	d.completeCalls++
	if d.stopComplete {
		return objectstore.Verified{}, errors.New("interrupted before Complete dispatch")
	}
	verified, err := d.fakeMultipartDestination.Complete(ctx, key, id, parts)
	if err == nil {
		d.parts = map[int]objectstore.Part{}
		d.bodies = map[int][]byte{}
	}
	return verified, err
}

func partialBudgetFixture(t *testing.T, lostACK, checksums bool) (*Manifest, *fakeSource, *store.Store, *budgetMultipartDestination, ApplyConfig) {
	t.Helper()
	p := legacyFixture(t)
	source := fixtureSource()
	large := bytes.Repeat([]byte("abcdef"), 1<<20) // 6 MiB, exactly two parts.
	for index, object := range source.objects {
		if object.Key == "video.mp4" {
			source.objects[index].Size = int64(len(large))
			source.data[object.Identity()] = large
		}
	}
	archive := filepath.Join(t.TempDir(), "archive")
	m, err := Scan(ctx, ScanConfig{Database: p, Archive: archive, SnapshotID: "partial-capacity", RollbackReference: "cold fixture", RollbackVerified: true}, source)
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.Open(ctx, filepath.Join(t.TempDir(), "target"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { target.Close() })
	d := &budgetMultipartDestination{fakeMultipartDestination: &fakeMultipartDestination{fakeDestination: newDestination(), parts: map[int]objectstore.Part{}, bodies: map[int][]byte{}, interrupt: lostACK}, listChecksums: checksums, stopComplete: !lostACK}
	var library string
	if err = target.Reader().QueryRow(`SELECT library_id FROM library_meta WHERE singleton=1`).Scan(&library); err != nil {
		t.Fatal(err)
	}
	var planned int64
	for _, plan := range makePlans(m, library) {
		planned += plan.Size
	}
	c := ApplyConfig{Archive: archive, Journal: filepath.Join(t.TempDir(), "journal.sqlite"), TargetID: "partial-capacity", PartSize: 5 << 20, SinglePutLimit: 5 << 20, Budget: Budget{WritePeakBytes: 10, MetadataBytes: 10, ReserveBytes: 10}, Capacity: func(context.Context) (int64, int64, error) {
		var used int64
		for _, body := range d.data {
			used += int64(len(body))
		}
		for _, body := range d.bodies {
			used += int64(len(body))
		}
		return planned + 30 - used, 100000, nil
	}}
	return m, source, target, d, c
}

func interruptPartial(t *testing.T, m *Manifest, source *fakeSource, target *store.Store, d *budgetMultipartDestination, c ApplyConfig) {
	t.Helper()
	err := Apply(ctx, c, m, source, target, d)
	if err == nil || !(strings.Contains(err.Error(), "lost part response") || strings.Contains(err.Error(), "interrupted before Complete dispatch")) {
		t.Fatalf("fixture did not interrupt after storing two parts: %v", err)
	}
	if len(d.parts) != 2 || d.partCalls != 2 || len(d.bodies[1])+len(d.bodies[2]) != 6<<20 {
		t.Fatalf("incomplete partial fixture: parts=%v calls=%d", d.parts, d.partCalls)
	}
	d.interrupt = false
	d.stopComplete = false
}

func TestPartialMultipartResumeBudgetCreditsOnlyProvenStoredParts(t *testing.T) {
	for _, test := range []struct {
		name      string
		lostACK   bool
		checksums bool
	}{
		{"lost_ACK_with_authoritative_checksums", true, true},
		{"durable_validated_ACK_when_ListParts_omits_checksums", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, source, target, d, c := partialBudgetFixture(t, test.lostACK, test.checksums)
			interruptPartial(t, m, source, target, d, c)
			if err := Apply(ctx, c, m, source, target, d); err != nil {
				t.Fatalf("stored partial bytes were charged again: %v", err)
			}
			if d.partCalls != 2 {
				t.Fatalf("immutable stored parts were resent: %d", d.partCalls)
			}
			if report, err := Verify(ctx, c, m, source, target, d); err != nil || !report.Complete {
				t.Fatalf("partial resume verification: %+v %v", report, err)
			}
			if err := Apply(ctx, c, m, source, target, d); err != nil {
				t.Fatalf("completed partial replay: %v", err)
			}
		})
	}
}

func TestPartialMultipartBudgetRejectsMissingConflictingOrUnboundProof(t *testing.T) {
	for _, mode := range []string{"lost_ACK_missing_checksum", "old_reconciled_ETag_without_validated_ACK", "wrong_checksum", "noncanonical_checksum", "wrong_geometry", "unbound_part", "wrong_ETag_against_ACK"} {
		t.Run(mode, func(t *testing.T) {
			lostACK := mode != "wrong_ETag_against_ACK"
			m, source, target, d, c := partialBudgetFixture(t, lostACK, true)
			interruptPartial(t, m, source, target, d, c)
			part := d.parts[2]
			switch mode {
			case "lost_ACK_missing_checksum":
				d.listChecksums = false
			case "old_reconciled_ETag_without_validated_ACK":
				d.listChecksums = false
				db, err := sql.Open("sqlite3", c.Journal)
				if err != nil {
					t.Fatal(err)
				}
				_, err = db.Exec(`UPDATE parts SET etag=? WHERE number=2`, part.ETag)
				db.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "wrong_checksum":
				part.SHA256 = strings.Repeat("A", 43) + "="
			case "noncanonical_checksum":
				part.SHA256 += "\n"
			case "wrong_geometry":
				part.Size--
			case "unbound_part":
				db, err := sql.Open("sqlite3", c.Journal)
				if err != nil {
					t.Fatal(err)
				}
				_, err = db.Exec(`DELETE FROM parts WHERE number=2`)
				db.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "wrong_ETag_against_ACK":
				d.listChecksums = false
				part.ETag = "unproven-replacement"
			}
			d.parts[2] = part
			calls := d.completeCalls
			err := Apply(ctx, c, m, source, target, d)
			if mode == "lost_ACK_missing_checksum" || mode == "old_reconciled_ETag_without_validated_ACK" {
				if !errors.Is(err, ErrBudget) || !strings.Contains(err.Error(), "no checksum or validated acknowledgement") {
					t.Fatalf("missing proof did not explain zero credit: %v", err)
				}
			} else if !errors.Is(err, objectstore.ErrIntegrity) {
				t.Fatalf("conflicting or unbound part earned credit: %v", err)
			}
			if d.partCalls != 2 || d.completeCalls != calls {
				t.Fatal("wrote or completed after invalid capacity proof")
			}
		})
	}
}

func TestMultipartPreservesEntireWorkingAndReserveBudgetBeforeComplete(t *testing.T) {
	m, source, target, d, c := partialBudgetFixture(t, false, true)
	d.stopComplete = false
	measure := c.Capacity
	c.Capacity = func(ctx context.Context) (int64, int64, error) {
		available, inodes, err := measure(ctx)
		if len(d.parts) == 2 {
			available-- // The final byte of W+D+R must never be spent on Complete.
		}
		return available, inodes, err
	}
	if err := Apply(ctx, c, m, source, target, d); !errors.Is(err, ErrBudget) {
		t.Fatalf("Complete ignored exhausted working/reserve space: %v", err)
	}
	if d.completeCalls != 0 || len(d.parts) != 2 {
		t.Fatalf("Complete dispatched without full reserve: calls=%d parts=%d", d.completeCalls, len(d.parts))
	}
}
