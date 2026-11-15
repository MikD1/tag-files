package migrate

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/search"
	"github.com/mikd1/tag-files/app/internal/store"
)

type mapping struct {
	ID, SourceID, AssetID, Role, Key, SHA256, ContentType string
	ItemID                                                int64
	Size                                                  int64
	Metadata                                              map[string]string
	ArchivePath                                           string
}
type journal struct {
	db   *sql.DB
	lock *os.File
}

func (j *journal) Close() { j.db.Close(); j.lock.Close() }
func openJournal(ctx context.Context, c ApplyConfig, m *Manifest, db *store.Store, plans []mapping) (*journal, error) {
	if c.TargetID == "" || c.Journal == "" {
		return nil, fmt.Errorf("stable explicit target identity and journal required")
	}
	if e := os.MkdirAll(filepath.Dir(c.Journal), 0700); e != nil {
		return nil, e
	}
	if _, e := os.Lstat(c.Journal); e == nil {
		if _, e = regular(c.Journal); e != nil {
			return nil, e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	lock, e := os.OpenFile(c.Journal+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		lock.Close()
		return nil, fmt.Errorf("migration journal already owned: %w", e)
	}
	if info, statErr := os.Stat(c.Journal); statErr == nil && info.Size() > 0 {
		u := url.URL{Scheme: "file", Path: c.Journal, RawQuery: "mode=ro&_query_only=true"}
		probe, probeErr := sql.Open("sqlite3", u.String())
		if probeErr != nil {
			lock.Close()
			return nil, probeErr
		}
		var appID, version int
		probeErr = probe.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&appID)
		if probeErr == nil {
			probeErr = probe.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version)
		}
		probe.Close()
		if probeErr != nil || appID != 0x54464d4a || version != 1 {
			lock.Close()
			return nil, fmt.Errorf("unrecognized/incompatible migration journal; left untouched")
		}
	}
	file, e := os.OpenFile(c.Journal, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		lock.Close()
		return nil, e
	}
	file.Close()
	u := url.URL{Scheme: "file", Path: c.Journal, RawQuery: "_journal_mode=WAL&_synchronous=FULL&_foreign_keys=on&_txlock=immediate&_busy_timeout=5000"}
	sqlDB, e := sql.Open("sqlite3", u.String())
	if e != nil {
		lock.Close()
		return nil, e
	}
	j := &journal{sqlDB, lock}
	sqlDB.SetMaxOpenConns(1)
	fail := func(err error) (*journal, error) { j.Close(); return nil, err }
	_, e = sqlDB.ExecContext(ctx, `BEGIN IMMEDIATE; PRAGMA application_id=1413893450; PRAGMA user_version=1; CREATE TABLE IF NOT EXISTS run(singleton INTEGER PRIMARY KEY CHECK(singleton=1),version INTEGER NOT NULL,fingerprint TEXT NOT NULL,target_id TEXT NOT NULL,library_id TEXT NOT NULL,exception_json TEXT NOT NULL,created_at TEXT NOT NULL,target_schema INTEGER NOT NULL,library_format INTEGER NOT NULL,transfer_json TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS objects(id TEXT PRIMARY KEY,source_id TEXT NOT NULL,asset_id TEXT NOT NULL,item_id INTEGER NOT NULL,role TEXT NOT NULL,object_key TEXT NOT NULL UNIQUE,size INTEGER NOT NULL,sha256 TEXT NOT NULL,content_type TEXT NOT NULL,metadata_json TEXT NOT NULL,archive_path TEXT NOT NULL,state TEXT NOT NULL CHECK(state IN ('planned','copying','verified','imported','error')),attempts INTEGER NOT NULL DEFAULT 0,last_error TEXT,updated_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS multipart(object_id TEXT PRIMARY KEY REFERENCES objects(id),upload_id TEXT NOT NULL DEFAULT '',state TEXT NOT NULL,epoch TEXT NOT NULL,part_size INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS parts(object_id TEXT NOT NULL REFERENCES objects(id),number INTEGER NOT NULL,size INTEGER NOT NULL,sha256 TEXT NOT NULL,etag TEXT,PRIMARY KEY(object_id,number));
 CREATE TABLE IF NOT EXISTS part_acknowledgements(object_id TEXT NOT NULL,number INTEGER NOT NULL,upload_id TEXT NOT NULL,size INTEGER NOT NULL,sha256 TEXT NOT NULL,etag TEXT NOT NULL,PRIMARY KEY(object_id,number),FOREIGN KEY(object_id,number) REFERENCES parts(object_id,number));
 CREATE TRIGGER IF NOT EXISTS immutable_migration_ack BEFORE UPDATE ON part_acknowledgements BEGIN SELECT RAISE(ABORT,'immutable migration acknowledgement'); END;
 CREATE TRIGGER IF NOT EXISTS immutable_migration_part BEFORE UPDATE OF size,sha256 ON parts BEGIN SELECT RAISE(ABORT,'immutable migration part'); END;
 CREATE TABLE IF NOT EXISTS row_mapping(source_table TEXT NOT NULL,source_id TEXT NOT NULL,target_table TEXT,target_id TEXT,state TEXT NOT NULL,PRIMARY KEY(source_table,source_id));
 CREATE TRIGGER IF NOT EXISTS immutable_mapping BEFORE UPDATE OF source_id,asset_id,item_id,role,object_key,size,sha256,content_type,metadata_json,archive_path ON objects BEGIN SELECT RAISE(ABORT,'immutable migration mapping'); END; COMMIT;`)
	if e != nil {
		return fail(e)
	}
	var libraryID string
	if e = db.Reader().QueryRowContext(ctx, `SELECT library_id FROM library_meta WHERE singleton=1`).Scan(&libraryID); e != nil {
		return fail(e)
	}
	exceptionJSON, _ := json.Marshal(c.Exceptions)
	tx, e := sqlDB.BeginTx(ctx, nil)
	if e != nil {
		return fail(e)
	}
	defer tx.Rollback()
	var version, targetSchema, libraryFormat int
	var f, t, l, x, transfer string
	transferJSON := fmt.Sprintf("%d/%d", c.PartSize, c.SinglePutLimit)
	e = tx.QueryRowContext(ctx, `SELECT version,fingerprint,target_id,library_id,exception_json,target_schema,library_format,transfer_json FROM run WHERE singleton=1`).Scan(&version, &f, &t, &l, &x, &targetSchema, &libraryFormat, &transfer)
	if errors.Is(e, sql.ErrNoRows) {
		var count int
		if e = db.Reader().QueryRowContext(ctx, `SELECT (SELECT count(*) FROM items)+(SELECT count(*) FROM tags)+(SELECT count(*) FROM collections)+(SELECT count(*) FROM saved_searches)+(SELECT count(*) FROM assets)+(SELECT count(*) FROM upload_sessions)+(SELECT count(*) FROM jobs)+(SELECT count(*) FROM cleanup_receipts)+(SELECT count(*) FROM object_write_intents)+(SELECT count(*) FROM operation_results)+(SELECT count(*) FROM operation_targets)+(SELECT count(*) FROM navigation_contexts)+(SELECT count(*) FROM view_events)+(SELECT count(*) FROM deletion_assets)+(SELECT count(*) FROM upload_parts)`).Scan(&count); e != nil {
			return fail(e)
		}
		if count != 0 {
			return fail(fmt.Errorf("initial migration requires empty target library"))
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO run VALUES(1,1,?,?,?,?,?,?,?,?)`, m.Fingerprint, c.TargetID, libraryID, string(exceptionJSON), now(), store.SchemaVersion, store.FormatVersion, transferJSON); e != nil {
			return fail(e)
		}
		for _, p := range plans {
			meta, _ := json.Marshal(p.Metadata)
			if _, e = tx.ExecContext(ctx, `INSERT INTO objects(id,source_id,asset_id,item_id,role,object_key,size,sha256,content_type,metadata_json,archive_path,state,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,'planned',?)`, p.ID, p.SourceID, p.AssetID, p.ItemID, p.Role, p.Key, p.Size, p.SHA256, p.ContentType, string(meta), p.ArchivePath, now()); e != nil {
				return fail(e)
			}
		}
	} else if e != nil {
		return fail(e)
	} else if version != 1 || f != m.Fingerprint || t != c.TargetID || l != libraryID || x != string(exceptionJSON) || targetSchema != store.SchemaVersion || libraryFormat != store.FormatVersion || transfer != transferJSON {
		return fail(fmt.Errorf("journal source/target/exception identity differs; resume refused"))
	}
	if e = tx.Commit(); e != nil {
		return fail(e)
	}
	var count int
	if e = sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM objects`).Scan(&count); e != nil {
		return fail(e)
	}
	if count != len(plans) {
		return fail(fmt.Errorf("journal mapping count mismatch"))
	}
	for _, p := range plans {
		var got mapping
		var metadata string
		e = sqlDB.QueryRowContext(ctx, `SELECT id,source_id,asset_id,item_id,role,object_key,size,sha256,content_type,metadata_json,archive_path FROM objects WHERE id=?`, p.ID).Scan(&got.ID, &got.SourceID, &got.AssetID, &got.ItemID, &got.Role, &got.Key, &got.Size, &got.SHA256, &got.ContentType, &metadata, &got.ArchivePath)
		if e != nil {
			return fail(e)
		}
		if e = json.Unmarshal([]byte(metadata), &got.Metadata); e != nil || !reflect.DeepEqual(got, p) {
			return fail(fmt.Errorf("journal immutable mapping differs"))
		}
	}
	return j, nil
}
func stableUUID(s string) string {
	b, _ := hex.DecodeString(digest([]byte(s))[:32])
	b[6] = (b[6] & 15) | 80
	b[8] = (b[8] & 63) | 128
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
func resolve(m *Manifest, exceptions []Exception) (map[string]string, error) {
	out := map[string]string{}
	seen := map[string]bool{}
	known := map[string]Issue{}
	for _, i := range m.Issues {
		known[i.ID] = i
	}
	for _, x := range exceptions {
		i, ok := known[x.IssueID]
		if !ok || seen[x.IssueID] || strings.TrimSpace(x.Owner) == "" || strings.TrimSpace(x.Reason) == "" {
			return nil, fmt.Errorf("unknown/duplicate/incomplete named exception")
		}
		allowed := "archive_only"
		switch i.Code {
		case "missing_primary":
			allowed = "missing_media"
		case "dangling_collection":
			allowed = "clear_reference"
		case "dangling_join":
			allowed = "archive_only"
		}
		if x.Decision != allowed {
			return nil, fmt.Errorf("issue %s requires decision %s", x.IssueID, allowed)
		}
		seen[x.IssueID] = true
		out[i.Code+"\x00"+i.Entity] = x.Decision
	}
	for _, i := range m.Issues {
		if i.Blocking && !seen[i.ID] {
			return nil, fmt.Errorf("%w: %s %s (%s)", ErrUnresolved, i.Code, i.Entity, i.ID)
		}
	}
	return out, nil
}
func makePlans(m *Manifest, library string) []mapping {
	out := []mapping{}
	latest := map[string]SourceObject{}
	for _, o := range m.Objects {
		if o.Latest && !o.DeleteMarker {
			latest[o.Bucket+"\x00"+o.Key] = o
		}
	}
	used := map[string]bool{}
	appendPlan := func(o SourceObject, item int64, role string) {
		id := stableUUID(m.RunID + "\x00" + role + "\x00" + strconv.FormatInt(item, 10) + "\x00" + o.Identity())
		key := "quarantine/" + library + "/" + m.RunID + "/" + id + "/bytes"
		asset := ""
		if role != "quarantine" {
			asset = id
			prefix := "primary"
			name := "media"
			if role == "poster" {
				prefix = "previews"
				name = "poster.jpg"
			}
			key = prefix + "/" + library + "/" + id + "/" + name
		}
		ct := o.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		out = append(out, mapping{ID: id, SourceID: o.Identity(), AssetID: asset, ItemID: item, Role: role, Key: key, Size: o.Size, SHA256: o.SHA256, ContentType: ct, Metadata: o.Metadata, ArchivePath: o.ArchivePath})
		used[o.Identity()] = true
	}
	for _, i := range m.Model.Items {
		if !i.Valid {
			continue
		}
		if o, ok := latest["library\x00"+i.FileName]; ok {
			appendPlan(o, i.ID, "primary")
		}
		if i.ThumbnailStatus == "Generated" {
			if o, ok := latest["thumbnail\x00"+posterKey(i.FileName)]; ok {
				appendPlan(o, i.ID, "poster")
			}
		}
	}
	for _, o := range m.Objects {
		if !o.DeleteMarker && !used[o.Identity()] {
			appendPlan(o, 0, "quarantine")
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func budgetGate(ctx context.Context, c ApplyConfig, remaining int64, objects int) error {
	if c.Capacity == nil {
		return fmt.Errorf("measured destination capacity is required")
	}
	b := c.Budget
	if b.WritePeakBytes < 0 || b.MetadataBytes < 0 || b.ReserveBytes < 0 {
		return ErrBudget
	}
	reserve := b.ReserveBytes
	if reserve == 0 {
		reserve = 10000000000
		if remaining/10 > reserve {
			reserve = remaining / 10
		}
	}
	need := remaining
	for _, v := range []int64{b.WritePeakBytes, b.MetadataBytes, reserve} {
		if v > maxInt-need {
			return ErrBudget
		}
		need += v
	}
	available, inodes, e := c.Capacity(ctx)
	if e != nil {
		return e
	}
	if available < need || inodes < int64(objects+8) {
		return fmt.Errorf("%w: need %d bytes and %d inodes, available %d/%d", ErrBudget, need, objects+8, available, inodes)
	}
	return nil
}
func checkedArchive(ctx context.Context, c ApplyConfig, m *Manifest, s Source) (*Manifest, error) {
	loaded, e := LoadArchive(ctx, c.Archive)
	if e != nil {
		return nil, e
	}
	if loaded.Fingerprint != m.Fingerprint || loaded.RunID != m.RunID {
		return nil, ErrSourceChanged
	}
	if e = CheckFrozen(ctx, loaded, s); e != nil {
		return nil, e
	}
	if _, e = resolve(loaded, c.Exceptions); e != nil {
		return nil, e
	}
	for _, i := range loaded.Issues {
		if i.Code == "unknown_schema" {
			return nil, fmt.Errorf("unsupported schema must be reviewed by a matching tool, raw archive retained")
		}
	}
	return loaded, nil
}
func Apply(ctx context.Context, c ApplyConfig, m *Manifest, s Source, db *store.Store, d Destination) error {
	m, e := checkedArchive(ctx, c, m, s)
	if e != nil {
		return e
	}
	var library string
	if e = db.Reader().QueryRowContext(ctx, `SELECT library_id FROM library_meta WHERE singleton=1`).Scan(&library); e != nil {
		return e
	}
	plans := makePlans(m, library)
	var remaining int64
	for _, p := range plans {
		if p.Size > maxInt-remaining {
			return ErrBudget
		}
		remaining += p.Size
	}
	if c.Budget.ReserveBytes == 0 {
		c.Budget.ReserveBytes = 10000000000
		if remaining/10 > c.Budget.ReserveBytes {
			c.Budget.ReserveBytes = remaining / 10
		}
	}
	j, e := openJournal(ctx, c, m, db, plans)
	if e != nil {
		return e
	}
	defer j.Close()
	// A resume pays only for missing bytes. Complete keys require HEAD/full SHA.
	// Unfinished parts require source-bound checksum/validated-ACK evidence and
	// authoritative ListParts; journal state/ETag alone never earns credit.
	existing := map[string]bool{}
	partial := map[string]multipartCredit{}
	var missingProof string
	pending := len(plans)
	for _, p := range plans {
		info, headErr := d.Head(ctx, p.Key)
		if notFound(headErr) {
			credit, err := preflightMultipartCredit(ctx, c, j, p, d)
			if err != nil {
				return err
			}
			partial[p.ID] = credit
			remaining -= credit.bytes
			if missingProof == "" {
				missingProof = credit.missingProof
			}
			continue
		}
		if headErr != nil {
			return headErr
		}
		if info.Size != p.Size || info.ContentType != p.ContentType || !equalMetadata(info.Metadata, p.Metadata) {
			return fmt.Errorf("immutable target key differs: %s", p.Key)
		}
		verified, verifyErr := d.Verify(ctx, p.Key, p.Size, p.SHA256)
		if verifyErr != nil {
			return verifyErr
		}
		if verified.ContentType != p.ContentType || !equalMetadata(verified.Metadata, p.Metadata) {
			return fmt.Errorf("target Content-Type/metadata mismatch")
		}
		existing[p.ID] = true
		remaining -= p.Size
		pending--
	}
	gate := func(remaining int64, pending int) error {
		err := budgetGate(ctx, c, remaining, pending)
		if errors.Is(err, ErrBudget) && missingProof != "" {
			return fmt.Errorf("%w; %s", err, missingProof)
		}
		return err
	}
	if e = gate(remaining, pending); e != nil {
		return e
	}
	for _, p := range plans {
		if e = ctx.Err(); e != nil {
			return e
		}
		if e = gate(remaining, pending); e != nil {
			return e
		}
		if e = localBudget(filepath.Dir(c.Journal), c.Budget.MetadataBytes, 1); e != nil {
			return e
		}
		if existing[p.ID] {
			if _, e = j.db.ExecContext(ctx, `UPDATE multipart SET state='verified' WHERE object_id=?`, p.ID); e != nil {
				return e
			}
			if _, e = j.db.ExecContext(ctx, `UPDATE objects SET state='verified',last_error=NULL,updated_at=? WHERE id=?`, now(), p.ID); e != nil {
				return e
			}
			continue
		}
		if _, e = j.db.ExecContext(ctx, `UPDATE objects SET state='copying',attempts=attempts+1,last_error=NULL,updated_at=? WHERE id=?`, now(), p.ID); e != nil {
			return e
		}
		info, headErr := d.Head(ctx, p.Key)
		switch {
		case headErr == nil:
			if info.Size != p.Size || info.ContentType != p.ContentType || !equalMetadata(info.Metadata, p.Metadata) {
				e = fmt.Errorf("immutable target key differs: %s", p.Key)
			} else {
				_, e = d.Verify(ctx, p.Key, p.Size, p.SHA256)
			}
		case notFound(headErr):
			path, pathErr := safePath(c.Archive, p.ArchivePath)
			if pathErr != nil {
				return pathErr
			}
			f, openErr := os.Open(path)
			if openErr != nil {
				return openErr
			}
			limit := c.SinglePutLimit
			if limit == 0 {
				limit = 16 << 20
			}
			if p.Size > limit {
				e = copyMultipart(ctx, c, j, p, f, d, partial[p.ID], func() error {
					// All source parts now exist. Preserve the full working,
					// metadata and reserve budget while Complete is in flight.
					return gate(remaining-(p.Size-partial[p.ID].bytes), pending)
				})
			} else {
				e = d.Put(ctx, p.Key, f, p.Size, p.SHA256, p.ContentType, p.Metadata)
			}
			f.Close()
			if e == nil {
				var verified objectstore.Verified
				verified, e = d.Verify(ctx, p.Key, p.Size, p.SHA256)
				if e == nil && (verified.ContentType != p.ContentType || !equalMetadata(verified.Metadata, p.Metadata)) {
					e = fmt.Errorf("target Content-Type/metadata mismatch")
				}
			}
		default:
			e = headErr
		}
		if e != nil {
			_, recordErr := j.db.ExecContext(context.WithoutCancel(ctx), `UPDATE objects SET state='error',last_error=?,updated_at=? WHERE id=?`, e.Error(), now(), p.ID)
			return errors.Join(e, recordErr)
		}
		if _, e = j.db.ExecContext(ctx, `UPDATE multipart SET state='verified' WHERE object_id=?`, p.ID); e != nil {
			return e
		}
		if _, e = j.db.ExecContext(ctx, `UPDATE objects SET state='verified',last_error=NULL,updated_at=? WHERE id=?`, now(), p.ID); e != nil {
			return e
		}
		remaining -= p.Size - partial[p.ID].bytes
		pending--
	}
	if e = CheckFrozen(ctx, m, s); e != nil {
		return e
	}
	if e = importCatalog(ctx, c, m, plans, db); e != nil {
		return e
	}
	tx, e := j.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `UPDATE objects SET state='imported',updated_at=?`, now()); e != nil {
		return e
	}
	for _, v := range rowMappings(m) {
		if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO row_mapping VALUES(?,?,?,?,?)`, v[0], v[1], v[2], v[3], v[4]); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func notFound(e error) bool {
	if errors.Is(e, ErrNotFound) {
		return true
	}
	var s3err *objectstore.Error
	return errors.As(e, &s3err) && (s3err.Status == 404 || s3err.Code == "NoSuchKey")
}
func equalMetadata(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
func rowMappings(m *Manifest) [][5]string {
	out := [][5]string{}
	add := func(table string, id int64, target string, valid bool) {
		state := "imported"
		if !valid {
			state = "archived"
			target = ""
		}
		s := strconv.FormatInt(id, 10)
		out = append(out, [5]string{table, s, target, s, state})
	}
	for _, v := range m.Model.Tags {
		add("Tags", v.ID, "tags", v.Valid)
	}
	for _, v := range m.Model.Collections {
		add("Collections", v.ID, "collections", v.Valid)
	}
	for _, v := range m.Model.Items {
		add("LibraryItems", v.ID, "items", v.Valid)
	}
	for _, v := range m.Model.Categories {
		add("Categories", v.ID, "saved_searches", v.Valid)
	}
	for _, v := range m.Model.Processing {
		add("ProcessingFiles", v.ID, "", false)
	}
	for _, v := range m.Model.Joins {
		state := "imported"
		if !v.Valid {
			state = "archived"
		}
		id := fmt.Sprintf("%d:%d", v.ItemID, v.TagID)
		out = append(out, [5]string{"LibraryItemTag", id, "item_tags", id, state})
	}
	return out
}
func importCatalog(ctx context.Context, c ApplyConfig, m *Manifest, plans []mapping, db *store.Store) error {
	decisions, _ := resolve(m, c.Exceptions)
	var existing int
	if e := db.Reader().QueryRowContext(ctx, `SELECT (SELECT count(*) FROM items)+(SELECT count(*) FROM tags)+(SELECT count(*) FROM collections)+(SELECT count(*) FROM saved_searches)+(SELECT count(*) FROM assets)`).Scan(&existing); e != nil {
		return e
	}
	if existing > 0 {
		return verifyCatalog(ctx, m, plans, decisions, db, nil)
	}
	return db.Write(ctx, func(tx *sql.Tx) error {
		for _, v := range m.Model.Collections {
			if !v.Valid {
				continue
			}
			if _, e := tx.ExecContext(ctx, `INSERT INTO collections(id,name) VALUES(?,?)`, v.ID, v.Name); e != nil {
				return e
			}
		}
		for _, v := range m.Model.Tags {
			if !v.Valid {
				continue
			}
			if _, e := tx.ExecContext(ctx, `INSERT INTO tags(id,name) VALUES(?,?)`, v.ID, v.Name); e != nil {
				return e
			}
		}
		for _, v := range m.Model.Items {
			if !v.Valid {
				continue
			}
			collection := v.CollectionID
			if decisions["dangling_collection\x00"+fmt.Sprintf("item:%d", v.ID)] == "clear_reference" {
				collection = nil
			}
			if _, e := tx.ExecContext(ctx, `INSERT INTO items(id,description,media_kind,uploaded_at_utc,collection_id,is_favorite,view_count,last_view_at_utc,duration_ticks,state) VALUES(?,?,?,?,?,?,?,?,?,'importing')`, v.ID, v.Description, v.Kind, v.UploadedUTC, collection, v.Favorite, v.ViewCount, v.LastViewUTC, v.DurationTicks); e != nil {
				return e
			}
		}
		for _, p := range plans {
			if p.Role == "quarantine" {
				continue
			}
			if _, e := tx.ExecContext(ctx, `INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,state) VALUES(?,?,?,'tag-files',?,?,?,?,?,'published')`, p.AssetID, p.ItemID, p.Role, p.Key, p.Size, p.SHA256, p.ContentType, assetProvenance(m, p)); e != nil {
				return e
			}
		}
		for _, v := range m.Model.Items {
			if !v.Valid {
				continue
			}
			primary := ""
			for _, p := range plans {
				if p.ItemID == v.ID && p.Role == "primary" {
					primary = p.AssetID
					break
				}
			}
			if primary == "" {
				if _, e := tx.ExecContext(ctx, `UPDATE items SET state='missing' WHERE id=?`, v.ID); e != nil {
					return e
				}
			} else {
				if _, e := tx.ExecContext(ctx, `UPDATE items SET primary_asset_id=?,state='ready' WHERE id=?`, primary, v.ID); e != nil {
					return e
				}
			}
		}
		for _, j := range m.Model.Joins {
			if !j.Valid {
				continue
			}
			if _, e := tx.ExecContext(ctx, `INSERT INTO item_tags VALUES(?,?)`, j.ItemID, j.TagID); e != nil {
				return e
			}
		}
		for _, v := range m.Model.Categories {
			if !v.Valid {
				continue
			}
			q := ""
			if v.Query != nil {
				q = *v.Query
			}
			ast, _ := json.Marshal(v.AST)
			if _, e := tx.ExecContext(ctx, `INSERT INTO saved_searches(id,name,query_text,legacy_source_text,query_ast_json,parser_version,media_kind,validation_state) VALUES(?,?,?,?,?,?,?,?)`, v.ID, v.Name, q, v.Query, string(ast), search.ParserVersion, v.Kind, search.ValidationState(v.AST)); e != nil {
				return e
			}
		}
		return nil
	})
}

func assetProvenance(m *Manifest, p mapping) string {
	if p.Role == "poster" {
		return "legacy_derived"
	}
	for _, i := range m.Model.Items {
		if i.ID == p.ItemID {
			return i.Provenance
		}
	}
	return "legacy_unknown"
}
