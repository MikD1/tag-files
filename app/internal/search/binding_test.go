package search

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"context"
	"github.com/mikd1/tag-files/app/internal/store"
)

func parse(t *testing.T, q string) *Expr {
	t.Helper()
	e, err := Parse(q)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestBindingPreservesDuplicatesCaseAndUnresolvedIdentity(t *testing.T) {
	tags := []NamedTag{{1, "cat"}, {2, "cat"}, {3, "Cat"}, {4, "кошки-猫"}}
	parsed := parse(t, "cat && !missing")
	bound, err := Bind(parsed, tags)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bound.Left.Term.IDs, []int64{1, 2}) {
		t.Fatal(bound.Left.Term.IDs)
	}
	if len(parsed.Left.Term.IDs) != 0 {
		t.Fatal("binding mutated source AST")
	}
	if bound.Right.Term.SourceName != "missing" || len(bound.Right.Term.IDs) != 0 || !bound.Right.Term.Negated {
		t.Fatal("unresolved NOT lost")
	}
	if ValidationState(bound) != "unresolved" {
		t.Fatal("unresolved warning missing")
	}
	caseQuery, err := Bind(parse(t, "Cat"), tags)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(caseQuery.Term.IDs, []int64{3}) {
		t.Fatal("case folded")
	}
	raw, err := json.Marshal(bound)
	if err != nil {
		t.Fatal(err)
	}
	var restored Expr
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bound, &restored) {
		t.Fatal("AST JSON roundtrip changed identity")
	}
}

func TestCreateExpandsMatchingGroupsButRenameDoesNotRebind(t *testing.T) {
	tags := []NamedTag{{1, "a"}}
	e, err := Bind(parse(t, "a && !x"), tags)
	if err != nil {
		t.Fatal(err)
	}
	tags[0].Name = "x" // Rename changes labels; the unresolved x remains unrelated.
	text, err := Render(e, tags)
	if err != nil {
		t.Fatal(err)
	}
	if text != "(x && !x)" {
		t.Fatal(text)
	}
	if len(e.Right.Term.IDs) != 0 {
		t.Fatal("rename rebound unrelated unresolved name")
	}
	terms, err := DisplayTerms(e, tags)
	if err != nil {
		t.Fatal(err)
	}
	if len(terms) != 2 || terms[0].TagID == nil || *terms[0].TagID != 1 || terms[0].CurrentName != "x" || terms[0].SourceName != "a" || terms[1].TagID != nil {
		t.Fatalf("terms: %+v", terms)
	}
	tags = append(tags, NamedTag{2, "x"})
	expanded, changed, err := ExpandOnCreate(e, "x", tags)
	if err != nil || !changed {
		t.Fatalf("expand %t: %v", changed, err)
	}
	for _, term := range []*Term{expanded.Left.Term, expanded.Right.Term} {
		if !reflect.DeepEqual(term.IDs, []int64{1, 2}) {
			t.Fatal(term.IDs)
		}
	}
	if len(e.Right.Term.IDs) != 0 {
		t.Fatal("create expansion mutated prior AST")
	}
	if ValidationState(expanded) != "valid" {
		t.Fatal("create did not resolve warning")
	}
}

func TestDuplicateGroupRenderPreservesNotAndCreateSemantics(t *testing.T) {
	tags := []NamedTag{{1, "cat"}, {2, "cat"}}
	e, err := Bind(parse(t, "!cat"), tags)
	if err != nil {
		t.Fatal(err)
	}
	tags[0].Name = "dog"
	text, err := Render(e, tags)
	if err != nil {
		t.Fatal(err)
	}
	if text != "(!dog && !cat)" {
		t.Fatal(text)
	}
	tags = append(tags, NamedTag{3, "dog"})
	expanded, changed, err := ExpandOnCreate(e, "dog", tags)
	if err != nil || !changed {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expanded.Term.IDs, []int64{1, 2, 3}) {
		t.Fatal(expanded.Term.IDs)
	}
	if _, changed, err := ExpandOnCreate(expanded, "unrelated", append(tags, NamedTag{4, "unrelated"})); err != nil || changed {
		t.Fatalf("unrelated expanded: %t %v", changed, err)
	}
}

func TestCompiledGoldenQueriesExecuteInProductionSQLite(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tags := []NamedTag{{1, "cat"}, {2, "cat"}, {3, "dog"}, {4, "Cat"}, {5, "кошки-猫"}}
	err = s.Write(ctx, func(tx *sql.Tx) error {
		for _, tag := range tags {
			if _, err := tx.Exec(`INSERT INTO tags(id,name) VALUES(?,?)`, tag.ID, tag.Name); err != nil {
				return err
			}
		}
		for id, tagIDs := range [][]int{{}, {1}, {2, 3}, {1, 3}, {4}, {5}} {
			item := id + 1
			asset := fmt.Sprintf("asset-%d", item)
			if _, err := tx.Exec(`INSERT INTO items(id,media_kind,uploaded_at_utc,state) VALUES(?,'Image','2026-11-14T00:00:00.0000000Z','importing')`, item); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,state) VALUES(?,?,'primary','fixture',?,1,?,'image/jpeg','fixture','verified')`, asset, item, asset, strings.Repeat("a", 64)); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE items SET state='ready',primary_asset_id=? WHERE id=?`, asset, item); err != nil {
				return err
			}
			for _, tagID := range tagIDs {
				if _, err := tx.Exec(`INSERT INTO item_tags VALUES(?,?)`, item, tagID); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		q   string
		ids []int
	}{
		{"", []int{1, 2, 3, 4, 5, 6}}, {"cat", []int{2, 3, 4}}, {"Cat", []int{5}},
		{"cat && dog", []int{3, 4}}, {"cat && !dog", []int{2}},
		{"cat || кошки-猫", []int{2, 3, 4, 6}}, {"unknown", []int{}},
		{"!unknown", []int{1, 2, 3, 4, 5, 6}}, {"(cat || dog) && !Cat", []int{2, 3, 4}},
	} {
		t.Run(tc.q, func(t *testing.T) {
			e, err := Bind(parse(t, tc.q), tags)
			if err != nil {
				t.Fatal(err)
			}
			filter, args, err := Compile(e)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := s.Reader().Query(`SELECT i.id FROM items i WHERE i.state='ready' AND (`+filter+`) ORDER BY i.id`, args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			got := []int{}
			for rows.Next() {
				var id int
				if err := rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				got = append(got, id)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.ids) {
				t.Fatalf("got %v want %v", got, tc.ids)
			}
		})
	}
}

func TestCompilerRejectsMalformedASTAndUsesBoundArguments(t *testing.T) {
	for _, e := range []*Expr{nil, {Op: "sql"}, {Op: And, Left: &Expr{Op: All}}, {Op: Tag}, {Op: Tag, Term: &Term{SourceName: "cat", IDs: []int64{-1}}}, {Op: Tag, Term: &Term{SourceName: "cat", IDs: []int64{9007199254740992}}}} {
		if _, _, err := Compile(e); err == nil {
			t.Fatalf("invalid AST accepted: %+v", e)
		}
	}
	e := parse(t, "cat")
	for i := 0; i < MaxTerms; i++ {
		e = &Expr{Op: And, Left: e, Right: parse(t, "cat")}
	}
	if _, _, err := Compile(e); err == nil {
		t.Fatal("unbounded stored AST accepted")
	}
	e, err := Bind(parse(t, "cat"), []NamedTag{{42, "cat"}})
	if err != nil {
		t.Fatal(err)
	}
	filter, args, err := Compile(e)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(filter, "42") || strings.Contains(filter, "cat") || len(args) != 1 {
		t.Fatalf("not parameterized: %s %v", filter, args)
	}
}
