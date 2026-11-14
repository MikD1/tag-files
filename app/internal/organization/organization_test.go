package organization

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/search"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
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

func ids(t *testing.T, s *store.Store, savedID int64) []int64 {
	t.Helper()
	saved, err := dbgen.New(s.Reader()).GetSavedSearch(ctx, savedID)
	if err != nil {
		t.Fatal(err)
	}
	ast, err := SavedAST(saved)
	if err != nil {
		t.Fatal(err)
	}
	filter, args, err := search.Compile(ast)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.Reader().Query(`SELECT i.id FROM items i WHERE `+filter+` ORDER BY i.id`, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestRenamePreservesSavedResultsWithoutResolvingUnrelatedNames(t *testing.T) {
	s := library(t)
	var tag dbgen.Tag
	var saved dbgen.SavedSearch
	write(t, s, func(tx *sql.Tx) error {
		var err error
		tag, err = CreateTag(ctx, tx, "a")
		if err != nil {
			return err
		}
		saved, err = CreateSavedSearch(ctx, tx, NewSavedSearch{Name: "Not x", QueryText: "a && !x"})
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO items(id,media_kind,uploaded_at_utc,state) VALUES(1,'Image','2026-11-14T00:00:00.0000000Z','missing'),(2,'Image','2026-11-14T00:00:00.0000000Z','missing');INSERT INTO item_tags VALUES(1,1)`)
		return err
	})
	if got := ids(t, s, saved.ID); !reflect.DeepEqual(got, []int64{1}) {
		t.Fatal(got)
	}
	write(t, s, func(tx *sql.Tx) error { _, err := RenameTag(ctx, tx, tag.ID, tag.Version, "x"); return err })
	if got := ids(t, s, saved.ID); !reflect.DeepEqual(got, []int64{1}) {
		t.Fatalf("rename changed saved result: %v", got)
	}
	after, err := dbgen.New(s.Reader()).GetSavedSearch(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.QueryText != "x && !x" || after.Version != 2 || after.QueryAstJson != saved.QueryAstJson {
		t.Fatalf("rename corrupted binding: %+v", after)
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error { _, err := RenameTag(ctx, tx, tag.ID, tag.Version, "stale"); return err }); !errors.Is(err, domain.ErrStaleVersion) {
		t.Fatalf("stale rename: %v", err)
	}
	// Explicit text editing deliberately rebinds both occurrences to x.
	write(t, s, func(tx *sql.Tx) error {
		text := after.QueryText
		_, err := PatchSavedSearch(ctx, tx, saved.ID, after.Version, SavedSearchPatch{QueryText: &text})
		return err
	})
	if got := ids(t, s, saved.ID); len(got) != 0 {
		t.Fatalf("explicit edit did not rebind: %v", got)
	}
}

func TestTagCreateExpandsDuplicateAndUnresolvedSearchesAtomically(t *testing.T) {
	s := library(t)
	var a, b dbgen.SavedSearch
	write(t, s, func(tx *sql.Tx) error {
		if _, err := CreateTag(ctx, tx, "cat"); err != nil {
			return err
		}
		var err error
		a, err = CreateSavedSearch(ctx, tx, NewSavedSearch{Name: "Cats", QueryText: "cat"})
		if err != nil {
			return err
		}
		b, err = CreateSavedSearch(ctx, tx, NewSavedSearch{Name: "Dogs", QueryText: "dog"})
		return err
	})
	write(t, s, func(tx *sql.Tx) error {
		if _, err := CreateTag(ctx, tx, "cat"); err != nil {
			return err
		}
		if _, err := CreateTag(ctx, tx, "dog"); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO items(id,media_kind,uploaded_at_utc,state) VALUES(1,'Image','2026-11-14T00:00:00.0000000Z','missing'),(2,'Video','2026-11-14T00:00:00.0000000Z','missing');INSERT INTO item_tags VALUES(1,2),(2,3)`)
		return err
	})
	if got := ids(t, s, a.ID); !reflect.DeepEqual(got, []int64{1}) {
		t.Fatal(got)
	}
	if got := ids(t, s, b.ID); !reflect.DeepEqual(got, []int64{2}) {
		t.Fatal(got)
	}
	q := dbgen.New(s.Reader())
	astRow, err := q.GetSavedSearch(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ast search.Expr
	if err := json.Unmarshal([]byte(astRow.QueryAstJson), &ast); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ast.Term.IDs, []int64{1, 2}) {
		t.Fatal(ast.Term.IDs)
	}
	if astRow.Version != 2 {
		t.Fatal("unrelated dog create changed cat search")
	}
	// Corrupt executable AST must roll back the new tag and every category update.
	write(t, s, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE saved_searches SET query_ast_json='{"op":"tag"}' WHERE id=?`, b.ID)
		return err
	})
	err = s.Write(ctx, func(tx *sql.Tx) error { _, err := CreateTag(ctx, tx, "cat"); return err })
	if err == nil {
		t.Fatal("invalid saved AST accepted")
	}
	tags, err := q.ListTags(ctx)
	if err != nil || len(tags) != 3 {
		t.Fatalf("partial tag create: %v %v", tags, err)
	}
	still, err := q.GetSavedSearch(ctx, a.ID)
	if err != nil || still.Version != astRow.Version {
		t.Fatalf("partial category update: %+v %v", still, err)
	}
}

func TestNameOnlySavedPatchPreservesASTAndInvalidInputDoesNotSave(t *testing.T) {
	s := library(t)
	var row dbgen.SavedSearch
	write(t, s, func(tx *sql.Tx) error {
		var err error
		row, err = CreateSavedSearch(ctx, tx, NewSavedSearch{Name: "Unknown", QueryText: "!missing"})
		return err
	})
	write(t, s, func(tx *sql.Tx) error {
		name := "Renamed"
		after, err := PatchSavedSearch(ctx, tx, row.ID, row.Version, SavedSearchPatch{Name: &name})
		if err != nil {
			return err
		}
		if after.QueryAstJson != row.QueryAstJson || after.QueryText != row.QueryText || after.ParserVersion != row.ParserVersion {
			t.Fatal("name patch rebound query")
		}
		return nil
	})
	for _, in := range []NewSavedSearch{{Name: "", QueryText: "cat"}, {Name: "Bad query", QueryText: "cat$"}, {Name: "Invalid type", MediaKind: kindPtr(domain.MediaKind("Banana"))}} {
		if err := s.Write(ctx, func(tx *sql.Tx) error { _, err := CreateSavedSearch(ctx, tx, in); return err }); err == nil {
			t.Fatalf("invalid saved search: %+v", in)
		}
	}
	var n int
	if err := s.Reader().QueryRow(`SELECT count(*) FROM saved_searches`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("invalid persisted: %d %v", n, err)
	}
}

func kindPtr(k domain.MediaKind) *domain.MediaKind { return &k }

func TestCollectionDeletionPreservesItemsAndBumpsTheirVersions(t *testing.T) {
	s := library(t)
	var col dbgen.Collection
	write(t, s, func(tx *sql.Tx) error {
		var err error
		col, err = CreateCollection(ctx, tx, "Travel")
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO items(id,media_kind,uploaded_at_utc,state,collection_id) VALUES(1,'Image','2026-11-14T00:00:00.0000000Z','missing',?)`, col.ID)
		return err
	})
	if err := s.Write(ctx, func(tx *sql.Tx) error { return DeleteCollection(ctx, tx, col.ID, 99) }); !errors.Is(err, domain.ErrStaleVersion) {
		t.Fatal(err)
	}
	write(t, s, func(tx *sql.Tx) error { return DeleteCollection(ctx, tx, col.ID, col.Version) })
	var collection sql.NullInt64
	var version int64
	if err := s.Reader().QueryRow(`SELECT collection_id,version FROM items WHERE id=1`).Scan(&collection, &version); err != nil || collection.Valid || version != 2 {
		t.Fatalf("lost or stale item: %v v%d %v", collection, version, err)
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error { return DeleteCollection(ctx, tx, col.ID, col.Version) }); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestTagNamesAndGlobalStatisticsPreserveLegacyGrouping(t *testing.T) {
	s := library(t)
	write(t, s, func(tx *sql.Tx) error {
		for _, name := range []string{"cat", "cat", "Cat", "unused", "кошки-猫"} {
			if _, err := CreateTag(ctx, tx, name); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`INSERT INTO items(id,media_kind,uploaded_at_utc,state) VALUES(1,'Image','2026-11-14T00:00:00.0000000Z','missing');INSERT INTO item_tags VALUES(1,1),(1,2),(1,3)`)
		return err
	})
	stats, err := dbgen.New(s.Reader()).GlobalTagStatistics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 4 || stats[0].Name != "cat" || stats[0].UsageCount != 2 || stats[1].Name != "Cat" || stats[1].UsageCount != 1 || stats[2].Name != "unused" || stats[2].UsageCount != 0 {
		t.Fatalf("global stats: %+v", stats)
	}
	for _, name := range []string{"", strings.Repeat("a", 51), "a_b", "e\u0301", "𐐀"} {
		if err := s.Write(ctx, func(tx *sql.Tx) error { _, err := CreateTag(ctx, tx, name); return err }); err == nil {
			t.Fatalf("invalid tag %q", name)
		}
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error { _, err := CreateCollection(ctx, tx, strings.Repeat("🙂", 101)); return err }); err == nil {
		t.Fatal("UTF-16 length not enforced")
	}
	write(t, s, func(tx *sql.Tx) error { _, err := CreateCollection(ctx, tx, strings.Repeat("🙂", 100)); return err })
}

func TestRemainingOrganizationCRUDAndFullCollectionDirectory(t *testing.T) {
	s := library(t)
	write(t, s, func(tx *sql.Tx) error {
		col, err := CreateCollection(ctx, tx, "Original")
		if err != nil {
			return err
		}
		renamed, err := RenameCollection(ctx, tx, col.ID, col.Version, "Renamed")
		if err != nil {
			return err
		}
		if renamed.Name != "Renamed" || renamed.Version != 2 {
			t.Fatal(renamed)
		}
		for i := 0; i < 350; i++ {
			if _, err := CreateCollection(ctx, tx, fmt.Sprintf("Collection %03d", i)); err != nil {
				return err
			}
		}
		saved, err := CreateSavedSearch(ctx, tx, NewSavedSearch{Name: "Videos", MediaKind: kindPtr(domain.Video)})
		if err != nil {
			return err
		}
		cleared, err := PatchSavedSearch(ctx, tx, saved.ID, saved.Version, SavedSearchPatch{SetMediaKind: true})
		if err != nil {
			return err
		}
		if cleared.MediaKind.Valid {
			t.Fatal("explicit NULL kind ignored")
		}
		if err := DeleteSavedSearch(ctx, tx, saved.ID, cleared.Version); err != nil {
			return err
		}
		return nil
	})
	q := dbgen.New(s.Reader())
	collections, err := q.ListCollections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 351 || collections[0].Name != "Collection 000" || collections[350].Name != "Renamed" {
		t.Fatalf("directory truncated or unsorted: %d", len(collections))
	}
	if _, err := q.GetSavedSearch(ctx, 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("saved search deletion: %v", err)
	}
}

func TestCollectionCoverUsesOldestItemEvenIfItsPosterIsMissing(t *testing.T) {
	s := library(t)
	write(t, s, func(tx *sql.Tx) error {
		if _, err := CreateCollection(ctx, tx, "One"); err != nil {
			return err
		}
		for id := 1; id <= 2; id++ {
			asset := fmt.Sprintf("primary-%d", id)
			if _, err := tx.Exec(`INSERT INTO items(id,media_kind,uploaded_at_utc,state,collection_id) VALUES(?,'Image',?,'importing',1)`, id, fmt.Sprintf("2026-01-%02dT00:00:00.0000000Z", id)); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,state) VALUES(?,?,'primary','fixture',?,1,?,'image/jpeg','fixture','verified')`, asset, id, asset, strings.Repeat("a", 64)); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE items SET state='ready',primary_asset_id=? WHERE id=?`, asset, id); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,state) VALUES('poster-2',2,'poster','fixture','poster-2',1,?,'image/jpeg','fixture','verified')`, strings.Repeat("b", 64))
		return err
	})
	rows, err := dbgen.New(s.Reader()).ListCollections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ItemCount != 2 || rows[0].CoverAssetID.Valid {
		t.Fatalf("oldest missing poster was skipped: %+v", rows)
	}
}
