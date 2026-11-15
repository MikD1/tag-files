package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/mikd1/tag-files/app/internal/search"
	"github.com/mikd1/tag-files/app/internal/store"
)

func rowEqual(ctx context.Context, db *sql.DB, query string, expected []any, args ...any) error {
	row := db.QueryRowContext(ctx, query, args...)
	got := make([]any, len(expected))
	ptr := make([]any, len(got))
	for i := range got {
		ptr[i] = &got[i]
	}
	if e := row.Scan(ptr...); e != nil {
		return e
	}
	normalize := func(v []any) string { b, _ := json.Marshal(v); return string(b) }
	if normalize(got) != normalize(expected) {
		return fmt.Errorf("target scalar mismatch: %s arguments %v", query, args)
	}
	return nil
}
func value[T any](v *T) any {
	if v == nil {
		return nil
	}
	return *v
}
func verifyCatalog(ctx context.Context, m *Manifest, plans []mapping, decisions map[string]string, db *store.Store, report *Report) error {
	q := db.Reader()
	counts := map[string]int{"items": 0, "tags": 0, "collections": 0, "item_tags": 0, "saved_searches": 0, "assets": 0, "upload_sessions": 0, "jobs": 0, "cleanup_receipts": 0, "object_write_intents": 0, "operation_results": 0, "operation_targets": 0, "navigation_contexts": 0, "view_events": 0, "deletion_assets": 0, "upload_parts": 0}
	for _, v := range m.Model.Tags {
		if !v.Valid {
			continue
		}
		counts["tags"]++
		if e := rowEqual(ctx, q, `SELECT id,name,version FROM tags WHERE id=?`, []any{v.ID, v.Name, int64(1)}, v.ID); e != nil {
			return e
		}
	}
	for _, v := range m.Model.Collections {
		if !v.Valid {
			continue
		}
		counts["collections"]++
		if e := rowEqual(ctx, q, `SELECT id,name,version FROM collections WHERE id=?`, []any{v.ID, v.Name, int64(1)}, v.ID); e != nil {
			return e
		}
	}
	visibleIDs := map[int64]bool{}
	for _, v := range m.Model.Items {
		if !v.Valid {
			continue
		}
		counts["items"]++
		collection := value(v.CollectionID)
		if decisions["dangling_collection\x00"+fmt.Sprintf("item:%d", v.ID)] == "clear_reference" {
			collection = nil
		}
		var primary any
		state := "missing"
		for _, p := range plans {
			if p.ItemID == v.ID && p.Role == "primary" {
				primary = p.AssetID
				state = "ready"
			}
		}
		visibleIDs[v.ID] = state == "ready"
		if e := rowEqual(ctx, q, `SELECT id,upload_id,description,media_kind,uploaded_at_utc,collection_id,is_favorite,view_count,last_view_at_utc,duration_ticks,primary_asset_id,state,version FROM items WHERE id=?`, []any{v.ID, nil, value(v.Description), v.Kind, v.UploadedUTC, collection, v.Favorite, v.ViewCount, value(v.LastViewUTC), value(v.DurationTicks), primary, state, int64(1)}, v.ID); e != nil {
			return e
		}
	}
	for _, v := range m.Model.Joins {
		if !v.Valid {
			continue
		}
		counts["item_tags"]++
		if e := rowEqual(ctx, q, `SELECT item_id,tag_id FROM item_tags WHERE item_id=? AND tag_id=?`, []any{v.ItemID, v.TagID}, v.ItemID, v.TagID); e != nil {
			return e
		}
	}
	for _, p := range plans {
		if p.Role == "quarantine" {
			continue
		}
		counts["assets"]++
		if e := rowEqual(ctx, q, `SELECT id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,state,upload_id,job_id,width,height,rotation,media_kind,duration_ticks FROM assets WHERE id=?`, []any{p.AssetID, p.ItemID, p.Role, "tag-files", p.Key, p.Size, p.SHA256, p.ContentType, assetProvenance(m, p), "published", nil, nil, nil, nil, nil, nil, nil}, p.AssetID); e != nil {
			return e
		}
	}
	for _, v := range m.Model.Categories {
		if !v.Valid {
			continue
		}
		counts["saved_searches"]++
		text := ""
		if v.Query != nil {
			text = *v.Query
		}
		ast, _ := json.Marshal(v.AST)
		if e := rowEqual(ctx, q, `SELECT id,name,query_text,legacy_source_text,query_ast_json,parser_version,media_kind,validation_state,version FROM saved_searches WHERE id=?`, []any{v.ID, v.Name, text, value(v.Query), string(ast), int64(search.ParserVersion), value(v.Kind), search.ValidationState(v.AST), int64(1)}, v.ID); e != nil {
			return e
		}
		predicate, args, e := search.Compile(v.AST)
		if e != nil {
			return e
		}
		query := `SELECT i.id FROM items AS i WHERE i.state='ready' AND (` + predicate + `)`
		if v.Kind != nil {
			query += ` AND i.media_kind=?`
			args = append(args, *v.Kind)
		}
		query += ` ORDER BY i.id`
		rows, e := q.QueryContext(ctx, query, args...)
		if e != nil {
			return e
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		expected := []int64{}
		for _, id := range v.SourceIDs {
			if visibleIDs[id] {
				expected = append(expected, id)
			}
		}
		if !reflect.DeepEqual(ids, expected) {
			return fmt.Errorf("category %d item ID set differs: source %v target %v", v.ID, v.SourceIDs, ids)
		}
		if report != nil {
			report.Categories = append(report.Categories, CategoryResult{v.ID, v.SourceIDs, ids})
		}
	}
	keys := []string{}
	for table := range counts {
		keys = append(keys, table)
	}
	sort.Strings(keys)
	for _, table := range keys {
		var n int
		if e := q.QueryRowContext(ctx, `SELECT count(*) FROM `+quoteID(table)).Scan(&n); e != nil {
			return e
		}
		if n != counts[table] {
			return fmt.Errorf("target %s count differs: got %d want %d", table, n, counts[table])
		}
	}
	var integrity string
	if e := q.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); e != nil {
		return e
	}
	if integrity != "ok" {
		return fmt.Errorf("target SQLite integrity_check failed: %s", integrity)
	}
	rows, e := q.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if e != nil {
		return e
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("target foreign_key_check failed")
	}
	return rows.Err()
}
func Verify(ctx context.Context, c ApplyConfig, m *Manifest, s Source, db *store.Store, d Destination) (Report, error) {
	report := Report{Version: 1, RunID: m.RunID, Issues: m.Issues, Exceptions: c.Exceptions, VerifiedAt: now()}
	m, e := checkedArchive(ctx, c, m, s)
	if e != nil {
		return report, e
	}
	if _, e = regular(c.Journal); e != nil {
		return report, fmt.Errorf("verify requires durable existing journal: %w", e)
	}
	var library string
	if e = db.Reader().QueryRowContext(ctx, `SELECT library_id FROM library_meta WHERE singleton=1`).Scan(&library); e != nil {
		return report, e
	}
	plans := makePlans(m, library)
	j, e := openJournal(ctx, c, m, db, plans)
	if e != nil {
		return report, e
	}
	defer j.Close()
	var unfinished int
	if e = j.db.QueryRowContext(ctx, `SELECT count(*) FROM objects WHERE state<>'imported'`).Scan(&unfinished); e != nil {
		return report, e
	}
	if unfinished != 0 {
		return report, fmt.Errorf("%d mapping checkpoints are not imported", unfinished)
	}
	for _, p := range plans {
		v, e := d.Verify(ctx, p.Key, p.Size, p.SHA256)
		if e != nil {
			return report, e
		}
		if v.ContentType != p.ContentType || !equalMetadata(v.Metadata, p.Metadata) {
			return report, fmt.Errorf("target object metadata mismatch")
		}
		report.ObjectsVerified++
		report.BytesVerified += p.Size
	}
	decisions, _ := resolve(m, c.Exceptions)
	if e = verifyCatalog(ctx, m, plans, decisions, db, &report); e != nil {
		return report, e
	}
	var rowCount int
	if e = j.db.QueryRowContext(ctx, `SELECT count(*) FROM row_mapping`).Scan(&rowCount); e != nil {
		return report, e
	}
	if rowCount != len(rowMappings(m)) {
		return report, fmt.Errorf("row mapping checkpoint count mismatch")
	}
	if e = CheckFrozen(ctx, m, s); e != nil {
		return report, e
	}
	for _, o := range m.Objects {
		if !o.DeleteMarker {
			report.SourceObjectCount++
			report.SourceBytes += o.Size
		}
	}
	for _, item := range m.Model.Items {
		r := ItemResult{ID: item.ID, Provenance: item.Provenance, OriginalAvailability: item.OriginalAvailability, Evidence: item.ProvenanceEvidence}
		for _, p := range plans {
			if p.ItemID == item.ID && p.Role == "primary" {
				r.PrimaryAssetID = p.AssetID
				for _, o := range m.Objects {
					if o.Identity() == p.SourceID {
						r.Bucket = o.Bucket
						r.Key = o.Key
						r.VersionID = o.VersionID
						r.SHA256 = o.SHA256
						break
					}
				}
				break
			}
		}
		report.Items = append(report.Items, r)
	}
	report.Complete = true
	// Each verification is an immutable dated report; no existing report is overwritten.
	path := filepath.Join(filepath.Dir(c.Journal), "verify-"+m.RunID+"-"+digest([]byte(report.VerifiedAt))[:12]+".json")
	if e = saveJSON(path, report); e != nil {
		return report, e
	}
	return report, nil
}
