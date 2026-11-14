package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/organization"
	"github.com/mikd1/tag-files/app/internal/store"
)

var ctx = context.Background()

func library(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func write(t *testing.T, s *store.Store, fn func(*sql.Tx) error) {
	t.Helper()
	if err := s.Write(ctx, fn); err != nil {
		t.Fatal(err)
	}
}
func ptr[T any](v T) *T { return &v }
func seedItem(tx *sql.Tx, id int64, kind string, ticks any, views int, at string, collection any, tags ...int64) error {
	asset := fmt.Sprintf("primary-%d", id)
	if _, err := tx.Exec(`INSERT INTO items(id,media_kind,uploaded_at_utc,state,duration_ticks,view_count,collection_id) VALUES(?, ?,?,'importing',?,?,?)`, id, kind, at, ticks, views, collection); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,state) VALUES(?,?,'primary','fixture',?,1,?,'application/octet-stream','fixture','verified')`, asset, id, asset, strings.Repeat("a", 64)); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE items SET primary_asset_id=?,state='ready' WHERE id=?`, asset, id); err != nil {
		return err
	}
	for _, tag := range tags {
		if _, err := tx.Exec(`INSERT INTO item_tags VALUES(?,?)`, id, tag); err != nil {
			return err
		}
	}
	return nil
}
func itemIDs(items []Item) []int64 {
	got := []int64{}
	for _, item := range items {
		got = append(got, item.ID)
	}
	return got
}

func TestAllSortsNullTicksAndDeterministicTies(t *testing.T) {
	s := library(t)
	write(t, s, func(tx *sql.Tx) error {
		for i, tc := range []struct {
			kind  string
			ticks any
			views int
			at    string
		}{
			{"Image", nil, 2, "2026-01-01"}, {"Video", int64(0), 1, "2026-01-02"},
			{"Video", int64(123456789), 3, "2026-01-02"}, {"Audio", int64(864001234567), 1, "2026-01-04"},
			{"Text", nil, 0, "2026-01-03"}, {"Unknown", int64(9007199254740993), 4, "2026-01-05"},
		} {
			if err := seedItem(tx, int64(i+1), tc.kind, tc.ticks, tc.views, tc.at+"T00:00:00.0000000Z", nil); err != nil {
				return err
			}
		}
		return nil
	})
	for _, tc := range []struct {
		sort Sort
		ids  []int64
	}{
		{UploadedDesc, []int64{6, 4, 5, 2, 3, 1}}, {UploadedAsc, []int64{1, 2, 3, 5, 4, 6}},
		{VideoDurationAsc, []int64{1, 5, 2, 3, 4, 6}}, {VideoDurationDesc, []int64{6, 4, 3, 2, 1, 5}},
		{ViewCountAsc, []int64{5, 2, 4, 1, 3, 6}}, {ViewCountDesc, []int64{6, 3, 1, 2, 4, 5}},
	} {
		t.Run(string(tc.sort), func(t *testing.T) {
			page, err := List(ctx, s.Reader(), ListOptions{Sort: tc.sort})
			if err != nil {
				t.Fatal(err)
			}
			if got := itemIDs(page.Items); !reflect.DeepEqual(got, tc.ids) {
				t.Fatalf("got %v want %v", got, tc.ids)
			}
			if page.TotalItems != 6 || page.TotalPages != 1 || page.PageSize != 24 {
				t.Fatalf("page %+v", page)
			}
		})
	}
	for _, kind := range []domain.MediaKind{domain.Unknown, domain.Text, domain.Image, domain.Video, domain.Audio} {
		page, err := List(ctx, s.Reader(), ListOptions{Filter: Filter{Kind: &kind}})
		if err != nil {
			t.Fatal(err)
		}
		want := 1
		if kind == domain.Video {
			want = 2
		}
		if page.TotalItems != want {
			t.Fatalf("kind %s: %d want %d", kind, page.TotalItems, want)
		}
	}
}

func TestCompletePaginationAndRandomSeedSurvivePages(t *testing.T) {
	s := library(t)
	write(t, s, func(tx *sql.Tx) error {
		for i := int64(1); i <= 305; i++ {
			if err := seedItem(tx, i, "Image", nil, 0, "2026-01-01T00:00:00.0000000Z", nil); err != nil {
				return err
			}
		}
		return nil
	})
	seen := map[int64]bool{}
	var seed string
	for page := 1; page <= 13; page++ {
		result, err := List(ctx, s.Reader(), ListOptions{Sort: Random, Page: page, Seed: seed})
		if err != nil {
			t.Fatal(err)
		}
		if page == 1 {
			seed = result.Seed
		}
		if seed == "" || result.Seed != seed {
			t.Fatal("random seed lost")
		}
		if result.TotalItems != 305 || result.TotalPages != 13 {
			t.Fatalf("hidden cap: %+v", result)
		}
		for _, item := range result.Items {
			if seen[item.ID] {
				t.Fatalf("duplicate %d", item.ID)
			}
			seen[item.ID] = true
		}
	}
	if len(seen) != 305 {
		t.Fatalf("lost results: %d", len(seen))
	}
	a, err := List(ctx, s.Reader(), ListOptions{Sort: Random, Seed: seed})
	if err != nil {
		t.Fatal(err)
	}
	b, err := List(ctx, s.Reader(), ListOptions{Sort: Random, Seed: seed})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(itemIDs(a.Items), itemIDs(b.Items)) {
		t.Fatal("seed order changed")
	}
	c, err := List(ctx, s.Reader(), ListOptions{Sort: Random})
	if err != nil {
		t.Fatal(err)
	}
	if c.Seed == seed || reflect.DeepEqual(itemIDs(a.Items), itemIDs(c.Items)) {
		t.Fatal("new search reused random order")
	}
	last, err := List(ctx, s.Reader(), ListOptions{Page: 14})
	if err != nil || len(last.Items) != 0 || last.TotalItems != 305 {
		t.Fatalf("past end %+v %v", last, err)
	}
}

func TestFilterIntersectionSavedSourceAndExactSimilar(t *testing.T) {
	s := library(t)
	var savedID int64
	write(t, s, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO tags(id,name) VALUES(1,'cat'),(2,'dog'),(3,'extra');INSERT INTO collections(id,name) VALUES(1,'One'),(2,'Two')`); err != nil {
			return err
		}
		for _, f := range []struct {
			id   int64
			kind string
			col  any
			tags []int64
		}{{1, "Image", 1, []int64{1, 2}}, {2, "Video", 2, []int64{1, 2}}, {3, "Video", 1, []int64{1, 2, 3}}, {4, "Image", nil, []int64{1}}, {5, "Text", nil, nil}} {
			if err := seedItem(tx, f.id, f.kind, nil, 0, fmt.Sprintf("2026-01-%02dT00:00:00.0000000Z", f.id), f.col, f.tags...); err != nil {
				return err
			}
		}
		saved, err := organization.CreateSavedSearch(ctx, tx, organization.NewSavedSearch{Name: "Cats video", QueryText: "cat", MediaKind: ptr(domain.Video)})
		savedID = saved.ID
		return err
	})
	result, err := List(ctx, s.Reader(), ListOptions{Filter: Filter{Query: ptr("cat"), Kind: ptr(domain.Video), CollectionID: ptr(int64(1))}})
	if err != nil {
		t.Fatal(err)
	}
	if got := itemIDs(result.Items); !reflect.DeepEqual(got, []int64{3}) {
		t.Fatal(got)
	}
	result, err = List(ctx, s.Reader(), ListOptions{Filter: Filter{SavedSearchID: &savedID}})
	if err != nil {
		t.Fatal(err)
	}
	if got := itemIDs(result.Items); !reflect.DeepEqual(got, []int64{3, 2}) {
		t.Fatal(got)
	}
	if _, err := List(ctx, s.Reader(), ListOptions{Filter: Filter{SavedSearchID: &savedID, Query: ptr("")}}); err == nil {
		t.Fatal("mixed filter sources accepted")
	}
	similar, err := Similar(ctx, s.Reader(), 1, 1, 24)
	if err != nil {
		t.Fatal(err)
	}
	if got := itemIDs(similar.Items); !reflect.DeepEqual(got, []int64{2}) {
		t.Fatal(got)
	}
	empty, err := Similar(ctx, s.Reader(), 5, 1, 24)
	if err != nil || len(empty.Items) != 0 {
		t.Fatalf("empty tags similar: %+v %v", empty, err)
	}
	if _, err := Get(ctx, s.Reader(), 999); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestMetadataReplaceIsAtomicAndOptimistic(t *testing.T) {
	s := library(t)
	write(t, s, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO tags(id,name) VALUES(1,'a'),(2,'b');INSERT INTO collections(id,name) VALUES(1,'One')`); err != nil {
			return err
		}
		for i := int64(1); i <= 2; i++ {
			if err := seedItem(tx, i, "Image", nil, 0, "2026-01-01T00:00:00.0000000Z", nil, 1); err != nil {
				return err
			}
		}
		return nil
	})
	err := s.Write(ctx, func(tx *sql.Tx) error {
		return ReplaceMetadata(ctx, tx, []VersionedItem{{1, 1}, {2, 999}}, []int64{2}, ptr(int64(1)))
	})
	if !errors.Is(err, domain.ErrStaleVersion) {
		t.Fatalf("stale bulk: %v", err)
	}
	first, err := Get(ctx, s.Reader(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || first.CollectionID.Valid || first.Tags[0].ID != 1 {
		t.Fatal("partially saved bulk")
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		return ReplaceMetadata(ctx, tx, []VersionedItem{{1, 1}, {2, 1}}, []int64{99}, nil)
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("bad target: %v", err)
	}
	write(t, s, func(tx *sql.Tx) error {
		return ReplaceMetadata(ctx, tx, []VersionedItem{{1, 1}, {2, 1}}, []int64{2}, ptr(int64(1)))
	})
	for _, id := range []int64{1, 2} {
		item, err := Get(ctx, s.Reader(), id)
		if err != nil {
			t.Fatal(err)
		}
		if item.Version != 2 || item.CollectionID.Int64 != 1 || len(item.Tags) != 1 || item.Tags[0].ID != 2 {
			t.Fatalf("bad replace %+v", item)
		}
	}
	write(t, s, func(tx *sql.Tx) error { return ReplaceTags(ctx, tx, []int64{1, 2}, []int64{}) })
	write(t, s, func(tx *sql.Tx) error { return ReplaceCollection(ctx, tx, []int64{1, 2}, nil) })
	for _, id := range []int64{1, 2} {
		item, err := Get(ctx, s.Reader(), id)
		if err != nil {
			t.Fatal(err)
		}
		if item.CollectionID.Valid || len(item.Tags) != 0 || item.Version != 4 {
			t.Fatalf("clear failed %+v", item)
		}
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error { return ReplaceTags(ctx, tx, make([]int64, 501), nil) }); err == nil {
		t.Fatal("bulk limit missing")
	}
}

func TestFavoriteAndViewsAreIdempotentAndReadsHaveNoSideEffects(t *testing.T) {
	s := library(t)
	write(t, s, func(tx *sql.Tx) error { return seedItem(tx, 1, "Image", nil, 0, "2026-01-01T00:00:00.0000000Z", nil) })
	for i := 0; i < 2; i++ {
		write(t, s, func(tx *sql.Tx) error { return SetFavorite(ctx, tx, 1, true) })
	}
	item, err := Get(ctx, s.Reader(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if item.IsFavorite != 1 || item.Version != 2 {
		t.Fatalf("favorite replay mutated twice: %+v", item)
	}
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		write(t, s, func(tx *sql.Tx) error {
			return RecordView(ctx, tx, 1, "f0cd2bb6-5b37-4f27-98be-9410d51d3470", now.Add(time.Duration(i)*time.Hour))
		})
	}
	item, err = Get(ctx, s.Reader(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if item.ViewCount != 1 || item.LastViewAtUtc.String != store.Timestamp(now) || item.Version != 3 {
		t.Fatalf("view replay mutated: %+v", item)
	}
	for i := 0; i < 3; i++ {
		if _, err := List(ctx, s.Reader(), ListOptions{}); err != nil {
			t.Fatal(err)
		}
		if _, err := Get(ctx, s.Reader(), 1); err != nil {
			t.Fatal(err)
		}
	}
	after, err := Get(ctx, s.Reader(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != item.Version || after.ViewCount != item.ViewCount {
		t.Fatal("GET changed data")
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error { return RecordView(ctx, tx, 1, "not-uuid", now) }); err == nil {
		t.Fatal("bad event ID accepted")
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error { return RecordView(ctx, tx, 1, "f0cd--b6-5b37-4f27-98be-9410d51d3470", now) }); err == nil {
		t.Fatal("extra UUID separators accepted")
	}
}
