package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/mikd1/tag-files/app/internal/search"
	"github.com/mikd1/tag-files/app/internal/store"
)

// This oracle follows the actual C# TagQueryConverter.cs Regex.Matches and
// projects its generated Any(Name==...) expression to SQLite EXISTS. It does
// not call the migration tokenizer/AST evaluator or the new search parser.
func sourceCategorySQL(t *testing.T, db *sql.DB, query string) []int64 {
	t.Helper()
	pattern := regexp.MustCompile(`(!)?([a-zA-Z0-9\-]+)|([()])|(&&)|(\|\|)`)
	var predicate strings.Builder
	var args []any
	for _, match := range pattern.FindAllStringSubmatch(query, -1) {
		if match[2] != "" {
			if match[1] != "" {
				predicate.WriteString(" NOT ")
			}
			predicate.WriteString(` EXISTS(SELECT 1 FROM LibraryItemTag AS j JOIN Tags AS t ON t.Id=j.TagsId WHERE j.LibraryItemId=i.Id AND t.Name=? COLLATE BINARY) `)
			args = append(args, match[2])
		} else {
			switch match[0] {
			case "&&":
				predicate.WriteString(" AND ")
			case "||":
				predicate.WriteString(" OR ")
			default:
				predicate.WriteString(match[0])
			}
		}
	}
	where := predicate.String()
	if strings.TrimSpace(query) == "" {
		where = "1"
	}
	rows, err := db.Query(`SELECT i.Id FROM LibraryItems AS i WHERE `+where+` ORDER BY i.Id`, args...)
	if err != nil {
		t.Fatalf("source query %q: %v", query, err)
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestLegacyCategorySkippedTokensBlockInsteadOfInventingParity(t *testing.T) {
	queries := []struct {
		query string
		ids   []int64
		block bool
	}{
		{"! Cat", []int64{12}, true},
		{"!\tCat", []int64{12}, true},
		{"!(Cat)", []int64{12}, true},
		{"!!Cat", []int64{11, 13, 14, 15}, true},
		{"Cat!", []int64{12}, true},
		{"Cat &", []int64{12}, true},
		{"Cat |", []int64{12}, true},
		{"Cat ||| cat", []int64{12, 13}, true},
		{"Cat &&& cat", []int64{}, true},
		{"猫Cat", []int64{12}, true},
		{"Cat?", []int64{12}, true},
		{"Cat_", []int64{12}, true},
		{"Cat😀", []int64{12}, true},
		{"Cat", []int64{12}, false},
		{"!Cat", []int64{11, 13, 14, 15}, false},
		{"Cat || cat && !unused", []int64{12, 13}, false},
		{"(Cat || cat) && !unused", []int64{12, 13}, false},
		{"Cat || cat && unused", []int64{12}, false},
		{"(Cat || cat) && unused", []int64{}, false},
		{"!(Cat || cat) && !unused", []int64{12, 13}, true},
		{"!Cat && !cat || unused", []int64{11, 14, 15}, false},
		{" \tCat\r\n", []int64{12}, false},
		{"--- || 9", []int64{}, false},
		{"", []int64{11, 12, 13, 14, 15}, false},
	}
	p := legacyFixture(t)
	sourceDB, err := sql.Open("sqlite3", p)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceDB.Close()
	if _, err = sourceDB.Exec(`DELETE FROM Categories`); err != nil {
		t.Fatal(err)
	}
	for index, q := range queries {
		if ids := sourceCategorySQL(t, sourceDB, q.query); !reflect.DeepEqual(ids, q.ids) {
			t.Fatalf("incorrect independent legacy fixture %q: %v != %v", q.query, ids, q.ids)
		}
		if _, err = sourceDB.Exec(`INSERT INTO Categories VALUES(?,?,?,NULL)`, index+100, fmt.Sprint(index), q.query); err != nil {
			t.Fatal(err)
		}
	}
	source := fixtureSource()
	archive := filepath.Join(t.TempDir(), "archive")
	m, err := Scan(ctx, ScanConfig{Database: p, Archive: archive, SnapshotID: "legacy-token-boundaries", RollbackReference: "fixture cold source", RollbackVerified: true}, source)
	if err != nil {
		t.Fatal(err)
	}
	var exceptions []Exception
	for index, q := range queries {
		category := m.Model.Categories[index]
		if category.Query == nil || *category.Query != q.query {
			t.Fatalf("raw query lost: %+v", category)
		}
		if category.Valid == q.block {
			t.Errorf("query %q valid=%v; source IDs %v must never become different target IDs", q.query, category.Valid, q.ids)
		}
		if !q.block && !reflect.DeepEqual(category.SourceIDs, q.ids) {
			t.Errorf("query %q source parity: %v != %v", q.query, category.SourceIDs, q.ids)
		}
		found := false
		for _, issue := range m.Issues {
			if issue.Code == "invalid_category" && issue.Entity == fmt.Sprintf("category:%d", index+100) && issue.Blocking {
				found = true
				exceptions = append(exceptions, Exception{IssueID: issue.ID, Decision: "archive_only", Owner: "fixture owner", Reason: "keep ambiguous raw query in frozen archive"})
			}
		}
		if found != q.block {
			t.Errorf("query %q blocking issue=%v expected=%v", q.query, found, q.block)
		}
	}
	target, err := store.Open(ctx, filepath.Join(t.TempDir(), "target"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	destination := newDestination()
	c := ApplyConfig{Archive: archive, Journal: filepath.Join(t.TempDir(), "journal.sqlite"), TargetID: "legacy-queries", Budget: Budget{ReserveBytes: 1}, Capacity: func(_ context.Context) (int64, int64, error) { return 1 << 40, 1 << 20, nil }}
	if err = Apply(ctx, c, m, source, target, destination); !errors.Is(err, ErrUnresolved) {
		t.Fatalf("unsafe categories passed copy gate: %v", err)
	}
	if destination.puts != 0 {
		t.Fatal("target object written before owner resolution")
	}
	if report, err := Verify(ctx, c, m, source, target, destination); err == nil || report.Complete {
		t.Fatalf("unsafe query falsely reported complete: %+v %v", report, err)
	}
	c.Exceptions = exceptions
	if err = Apply(ctx, c, m, source, target, destination); err != nil {
		t.Fatal(err)
	}
	report, err := Verify(ctx, c, m, source, target, destination)
	if err != nil || !report.Complete {
		t.Fatalf("explicitly archived query verification: %+v %v", report, err)
	}
	for _, category := range report.Categories {
		q := queries[category.ID-100]
		if q.block || !reflect.DeepEqual(category.SourceIDs, q.ids) || !reflect.DeepEqual(category.TargetIDs, q.ids) {
			t.Errorf("incorrect category parity %+v, legacy query=%q IDs=%v", category, q.query, q.ids)
		}
	}
}

func TestLegacyCategoryInvalidBoundariesAndTargetLimitsRemainBlocking(t *testing.T) {
	for _, query := range []string{
		"Cat Dog", "Cat!Dog", "Cat (cat)", "(Cat)cat", "(Cat)(cat)",
		"Cat &&", "|| Cat", "()", "(Cat", "Cat)", "!", "&&", "!!",
		strings.Repeat("(", search.MaxNesting+1) + "Cat" + strings.Repeat(")", search.MaxNesting+1),
		strings.Repeat("Cat || ", search.MaxTerms) + "Cat",
		strings.Repeat(" ", search.MaxQueryLength+1),
	} {
		if _, err := legacyCategory(query, nil); err == nil {
			t.Errorf("unsafe or oversized legacy query accepted: %q", query)
		}
	}
}
