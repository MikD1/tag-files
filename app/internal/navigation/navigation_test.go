package navigation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/catalog"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/organization"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

var ctx = context.Background()

func TestNavigationPreservesURLForMaximumUnicodeQuery(t *testing.T) {
	s, _ := fixture(t)
	query := strings.Repeat("猫", 4096)
	origin := "/library?q=" + url.QueryEscape(query) + "&sort=UploadedDesc&page=1"
	now := time.Now()
	created := create(t, s, Input{Origin: origin, Options: catalog.ListOptions{Filter: catalog.Filter{Query: &query}}}, now)
	var raw string
	if err := s.Reader().QueryRowContext(ctx, "SELECT origin_json FROM navigation_contexts WHERE id=?", created.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var saved Origin
	if err := json.Unmarshal([]byte(raw), &saved); err != nil || saved.Route != origin {
		t.Fatalf("origin lost: %v", err)
	}
}

func fixture(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		if _, err := organization.CreateTag(ctx, tx, "cat"); err != nil {
			return err
		}
		for id := 1; id <= 5; id++ {
			asset := fmt.Sprintf("asset-%d", id)
			if _, err := tx.Exec(`INSERT INTO items(id,media_kind,uploaded_at_utc,state) VALUES(?,'Image',?,'importing')`, id, fmt.Sprintf("2026-01-%02dT00:00:00.0000000Z", id)); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,state) VALUES(?,?,'primary','fixture',?,1,?,'image/jpeg','fixture','verified')`, asset, id, asset, strings.Repeat("a", 64)); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE items SET primary_asset_id=?,state='ready' WHERE id=?`, asset, id); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO item_tags VALUES(?,1)`, id); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return s, dir
}
func create(t *testing.T, s *store.Store, in Input, now time.Time) Created {
	t.Helper()
	var created Created
	if err := s.Write(ctx, func(tx *sql.Tx) error { var err error; created, err = Create(ctx, tx, in, now); return err }); err != nil {
		t.Fatal(err)
	}
	return created
}

func TestDurableSnapshotSkipsDeletedAndPreservesSavedSearchVersion(t *testing.T) {
	s, dir := fixture(t)
	now := time.Now().UTC()
	var saved dbgen.SavedSearch
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		var err error
		saved, err = organization.CreateSavedSearch(ctx, tx, organization.NewSavedSearch{Name: "Cats", QueryText: "cat"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	created := create(t, s, Input{Origin: "/library?savedSearchId=1&page=2&sort=UploadedDesc", Options: catalog.ListOptions{Filter: catalog.Filter{SavedSearchID: &saved.ID}}}, now)
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='deleting' WHERE id=4`); err != nil {
			return err
		}
		if _, err := organization.RenameTag(ctx, tx, 1, 1, "dog"); err != nil {
			return err
		}
		return catalog.SetFavorite(ctx, tx, 3, true)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	neighbors, err := Get(ctx, s.Reader(), created.ID, 3, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if neighbors.PreviousID == nil || *neighbors.PreviousID != 5 || neighbors.NextID == nil || *neighbors.NextID != 2 || neighbors.Position == nil || *neighbors.Position != 2 || neighbors.Total != 4 {
		t.Fatalf("neighbors %+v", neighbors)
	}
	if neighbors.Origin.SavedSearchVersion == nil || *neighbors.Origin.SavedSearchVersion != 1 || neighbors.Origin.QueryAST == nil || !reflect.DeepEqual(neighbors.Origin.QueryAST.Term.IDs, []int64{1}) {
		t.Fatalf("source changed %+v", neighbors.Origin)
	}
	if neighbors.Origin.Route != "/library?savedSearchId=1&page=2&sort=UploadedDesc" {
		t.Fatal(neighbors.Origin.Route)
	}
	expired, err := Get(ctx, s.Reader(), created.ID, 3, now.Add(24*time.Hour))
	if err != nil || !expired.Expired || expired.Position != nil || expired.NextID != nil {
		t.Fatalf("expired %+v %v", expired, err)
	}
}

func TestRandomContextRetainsGalleryOrderAndExplicitOrderWorks(t *testing.T) {
	s, _ := fixture(t)
	now := time.Now()
	opts := catalog.ListOptions{Sort: catalog.Random, Seed: "stable-context"}
	page, err := catalog.List(ctx, s.Reader(), opts)
	if err != nil {
		t.Fatal(err)
	}
	created := create(t, s, Input{Origin: "/library?sort=Random&seed=stable-context", Options: opts}, now)
	neighbors, err := Get(ctx, s.Reader(), created.ID, page.Items[2].ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if *neighbors.PreviousID != page.Items[1].ID || *neighbors.NextID != page.Items[3].ID || neighbors.Origin.Seed != "stable-context" {
		t.Fatalf("random reordered %+v", neighbors)
	}
	ids := []int64{3, 1, 5}
	explicit := create(t, s, Input{Origin: "/library", OrderedIDs: &ids}, now)
	neighbors, err = Get(ctx, s.Reader(), explicit.ID, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if *neighbors.PreviousID != 3 || *neighbors.NextID != 5 || neighbors.Total != 3 {
		t.Fatal(neighbors)
	}
}

func TestNavigationRejectsUnsafeOrUnboundedInput(t *testing.T) {
	s, _ := fixture(t)
	now := time.Now()
	huge := make([]int64, 20001)
	duplicate := []int64{1, 1}
	missing := []int64{999}
	explicit := []int64{1}
	query := "cat"
	for _, in := range []Input{
		{Origin: "https://example.com/library"}, {Origin: "//example.com/library"}, {Origin: "/\\evil"}, {Origin: "/%2fexample.com"}, {Origin: "javascript:evil"},
		{Origin: "/library", OrderedIDs: &huge}, {Origin: "/library", OrderedIDs: &duplicate}, {Origin: "/library", OrderedIDs: &missing},
		{Origin: "/library", OrderedIDs: &explicit, Options: catalog.ListOptions{Filter: catalog.Filter{Query: &query}}},
	} {
		if err := s.Write(ctx, func(tx *sql.Tx) error { _, err := Create(ctx, tx, in, now); return err }); err == nil {
			t.Fatalf("invalid navigation accepted %+v", in)
		}
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error { _, _, err := catalog.Snapshot(ctx, tx, catalog.ListOptions{}, 3); return err }); err == nil {
		t.Fatal("snapshot silently truncated")
	}
	if _, err := Get(ctx, s.Reader(), "f36e6d7b-f772-49ef-97be-4cc9b49c58ad", 1, now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
}
