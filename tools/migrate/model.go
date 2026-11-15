package migrate

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mikd1/tag-files/app/internal/search"
	"github.com/mikd1/tag-files/app/internal/store"
)

type RawValue struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}
type RawRow struct {
	Table  string              `json:"table"`
	Values map[string]RawValue `json:"values"`
}

func quoteID(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func rawTable(ctx context.Context, db *sql.DB, table string) ([]RawRow, error) {
	cols, e := db.QueryContext(ctx, `PRAGMA table_info(`+quoteID(table)+`)`)
	if e != nil {
		return nil, e
	}
	names := []string{}
	for cols.Next() {
		var cid, nn, pk int
		var name, typ string
		var def any
		if e = cols.Scan(&cid, &name, &typ, &nn, &def, &pk); e != nil {
			cols.Close()
			return nil, e
		}
		names = append(names, name)
	}
	e = cols.Err()
	cols.Close()
	if e != nil {
		return nil, e
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("missing legacy table %s", table)
	}
	expr := []string{}
	for _, n := range names {
		q := quoteID(n)
		expr = append(expr, `typeof(`+q+`)`, `CASE WHEN typeof(`+q+`) IN ('text','blob') THEN hex(`+q+`) ELSE quote(`+q+`) END`)
	}
	rows, e := db.QueryContext(ctx, `SELECT `+strings.Join(expr, ",")+` FROM `+quoteID(table)+` ORDER BY `+strings.Join(func() []string {
		r := []string{}
		for _, n := range names {
			r = append(r, quoteID(n))
		}
		return r
	}(), ","))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []RawRow{}
	for rows.Next() {
		v := make([]string, len(names)*2)
		args := make([]any, len(v))
		for i := range v {
			args[i] = &v[i]
		}
		if e = rows.Scan(args...); e != nil {
			return nil, e
		}
		r := RawRow{Table: table, Values: map[string]RawValue{}}
		for i, n := range names {
			r.Values[n] = RawValue{v[2*i], v[2*i+1]}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func tableNames(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, e := db.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type='table' ORDER BY name`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{"sqlite_schema"}
	for rows.Next() {
		var n string
		if e = rows.Scan(&n); e != nil {
			return nil, e
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func dumpRaw(ctx context.Context, db *sql.DB, path string) error {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	names, e := tableNames(ctx, db)
	if e != nil {
		return e
	}
	for _, n := range names {
		rows, e := rawTable(ctx, db, n)
		if e != nil {
			return e
		}
		for _, r := range rows {
			if e = enc.Encode(r); e != nil {
				return e
			}
		}
	}
	return f.Sync()
}
func (r RawRow) text(k string) (string, error) {
	v := r.Values[k]
	if v.Type != "text" {
		return "", fmt.Errorf("%s must be TEXT (got %s)", k, v.Type)
	}
	b, e := hex.DecodeString(v.Value)
	if e != nil || !utf8.Valid(b) {
		return "", fmt.Errorf("invalid UTF-8 %s", k)
	}
	return string(b), nil
}
func (r RawRow) integer(k string) (int64, error) {
	v := r.Values[k]
	if v.Type != "integer" {
		return 0, fmt.Errorf("%s must be INTEGER (got %s)", k, v.Type)
	}
	return strconv.ParseInt(v.Value, 10, 64)
}
func (r RawRow) nullableText(k string) (*string, error) {
	if r.Values[k].Type == "null" || r.Values[k].Type == "" {
		return nil, nil
	}
	s, e := r.text(k)
	return &s, e
}
func (r RawRow) nullableInt(k string) (*int64, error) {
	if r.Values[k].Type == "null" || r.Values[k].Type == "" {
		return nil, nil
	}
	i, e := r.integer(k)
	return &i, e
}

type Named struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Valid bool   `json:"valid"`
}
type Item struct {
	Provenance           string
	OriginalAvailability string
	ProvenanceEvidence   []string

	ID                                       int64 `json:"id"`
	FileName, UploadedRaw, UploadedUTC, Kind string
	Description                              *string
	CollectionID                             *int64
	DurationTicks                            *int64
	ThumbnailStatus                          string
	Favorite                                 int64
	LastViewRaw, LastViewUTC                 *string
	ViewCount                                int64
	Valid                                    bool
}
type Category struct {
	ID        int64
	Name      string
	Query     *string
	Kind      *string
	AST       *search.Expr
	Valid     bool
	SourceIDs []int64
}
type Join struct {
	ItemID, TagID int64
	Valid         bool
}
type Processing struct {
	ID                                int64
	OriginalFileName, LibraryFileName string
	Kind, Status                      int64
	CollectionID                      *int64
	Valid                             bool
}
type Model struct {
	Migrations        []string
	Tags, Collections []Named
	Items             []Item
	Categories        []Category
	Joins             []Join
	Processing        []Processing
}

var migrations = []string{"20250622200914_Initial", "20250716221835_AddCollections", "20250815135444_AddViewCount", "20250929211941_ChangeVideoDurationType", "20250929214538_FixVideoDurationDataConversion", "20260322165707_AddCollectionIdToProcessingFile"}

func issue(code, entity, reason string, blocking bool) Issue {
	return Issue{ID: digest([]byte(code + "\x00" + entity + "\x00" + reason))[:24], Code: code, Entity: entity, Reason: reason, Blocking: blocking}
}
func knownKind(s string) bool {
	switch s {
	case "Unknown", "Text", "Image", "Video", "Audio":
		return true
	}
	return false
}
func idValid(id int64) bool { return id > 0 && id <= 4294967295 }
func timestamp(s string) (string, error) {
	for _, format := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999"} {
		t, e := time.Parse(format, s)
		if e == nil {
			if t.Nanosecond()%100 != 0 {
				return "", fmt.Errorf("sub-tick timestamp cannot be represented exactly")
			}
			return store.Timestamp(t), nil
		}
	}
	return "", fmt.Errorf("unrecognized .NET UTC timestamp %q", s)
}

var durationPattern = regexp.MustCompile(`^(-)?(?:(0|[1-9][0-9]*)\.)?([0-9]{2}):([0-9]{2}):([0-9]{2})(?:\.([0-9]{1,7}))?$`)

func parseDurationText(s string) (int64, error) {
	m := durationPattern.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("unrecognized TimeSpan text")
	}
	days := new(big.Int)
	if m[2] != "" {
		if _, ok := days.SetString(m[2], 10); !ok {
			return 0, fmt.Errorf("invalid days")
		}
	}
	h, _ := strconv.Atoi(m[3])
	min, _ := strconv.Atoi(m[4])
	sec, _ := strconv.Atoi(m[5])
	if h > 23 || min > 59 || sec > 59 {
		return 0, fmt.Errorf("TimeSpan component out of range")
	}
	ticks := new(big.Int).Mul(days, big.NewInt(864000000000))
	ticks.Add(ticks, big.NewInt(int64(h)*36000000000+int64(min)*600000000+int64(sec)*10000000))
	if m[6] != "" {
		fraction, _ := strconv.ParseInt(m[6]+strings.Repeat("0", 7-len(m[6])), 10, 64)
		ticks.Add(ticks, big.NewInt(fraction))
	}
	if m[1] != "" {
		ticks.Neg(ticks)
	}
	if !ticks.IsInt64() {
		return 0, fmt.Errorf("TimeSpan overflow")
	}
	return ticks.Int64(), nil
}
func duration(r RawRow) (*int64, error) {
	v := r.Values["VideoDuration"]
	if v.Type == "null" {
		return nil, nil
	}
	if v.Type == "integer" {
		return r.nullableInt("VideoDuration")
	}
	if v.Type == "text" {
		s, e := r.text("VideoDuration")
		if e != nil {
			return nil, e
		}
		n, e := parseDurationText(s)
		return &n, e
	}
	return nil, fmt.Errorf("duration SQLite typeof=%s", v.Type)
}

func readModel(ctx context.Context, db *sql.DB) (Model, []Issue, error) {
	model := Model{}
	issues := []Issue{}
	names, e := tableNames(ctx, db)
	if e != nil {
		return model, nil, e
	}
	known := map[string]bool{"sqlite_schema": true, "sqlite_sequence": true, "__EFMigrationsHistory": true, "LibraryItems": true, "Tags": true, "Collections": true, "Categories": true, "LibraryItemTag": true, "ProcessingFiles": true}
	for _, n := range names {
		if !known[n] {
			issues = append(issues, issue("unknown_schema", "schema", n, true))
		}
	}
	history, e := rawTable(ctx, db, "__EFMigrationsHistory")
	if e != nil {
		issues = append(issues, issue("unknown_schema", "history", e.Error(), true))
	} else {
		for i, r := range history {
			v, e := r.text("MigrationId")
			if e != nil || i >= len(migrations) || v != migrations[i] {
				issues = append(issues, issue("unknown_schema", "history", fmt.Sprintf("unexpected migration %q", v), true))
			}
			model.Migrations = append(model.Migrations, v)
		}
	}
	if len(model.Migrations) == 0 {
		issues = append(issues, issue("unknown_schema", "history", "empty EF migration history", true))
	}
	expectedColumns := map[string]string{
		"__EFMigrationsHistory": "MigrationId ProductVersion", "Tags": "Id Name", "Collections": "Id Name", "Categories": "Id Name TagQuery ItemsType", "LibraryItemTag": "LibraryItemId TagsId", "ProcessingFiles": "Id OriginalFileName LibraryFileName FileType Status CollectionId", "LibraryItems": "Id FileName UploadedOn FileType Description CollectionId VideoDuration ThumbnailStatus IsFavorite LastView ViewCount",
	}
	for table, columns := range expectedColumns {
		allowed := map[string]bool{}
		for _, name := range strings.Fields(columns) {
			allowed[name] = true
		}
		info, e := db.QueryContext(ctx, `PRAGMA table_info(`+quoteID(table)+`)`)
		if e != nil {
			return model, issues, e
		}
		for info.Next() {
			var cid, notNull, pk int
			var name, typ string
			var def any
			if e = info.Scan(&cid, &name, &typ, &notNull, &def, &pk); e != nil {
				info.Close()
				return model, issues, e
			}
			if !allowed[name] {
				issues = append(issues, issue("unknown_schema", table, "unmapped column "+name, true))
			}
		}
		e = info.Err()
		info.Close()
		if e != nil {
			return model, issues, e
		}
	}
	for _, table := range []string{"Tags", "Collections"} {
		rows, e := rawTable(ctx, db, table)
		if e != nil {
			if table == "Collections" && len(model.Migrations) < 2 {
				continue
			}
			return model, issues, e
		}
		for _, r := range rows {
			id, e1 := r.integer("Id")
			name, e2 := r.text("Name")
			valid := e1 == nil && e2 == nil && idValid(id)
			if !valid {
				issues = append(issues, issue("invalid_row", fmt.Sprintf("%s:%d", table, id), fmt.Sprintf("invalid ID/name: %v %v", e1, e2), true))
			}
			n := Named{id, name, valid}
			if table == "Tags" {
				model.Tags = append(model.Tags, n)
			} else {
				model.Collections = append(model.Collections, n)
			}
		}
	}
	rows, e := rawTable(ctx, db, "LibraryItems")
	if e != nil {
		return model, issues, e
	}
	for _, r := range rows {
		i := Item{Valid: true}
		var errs []error
		i.ID, e = r.integer("Id")
		errs = append(errs, e)
		i.FileName, e = r.text("FileName")
		errs = append(errs, e)
		i.Kind, e = r.text("FileType")
		errs = append(errs, e)
		i.UploadedRaw, e = r.text("UploadedOn")
		errs = append(errs, e)
		i.UploadedUTC, e = timestamp(i.UploadedRaw)
		errs = append(errs, e)
		i.Description, e = r.nullableText("Description")
		errs = append(errs, e)
		i.CollectionID, e = r.nullableInt("CollectionId")
		errs = append(errs, e)
		i.ThumbnailStatus, e = r.text("ThumbnailStatus")
		errs = append(errs, e)
		i.Favorite, e = r.integer("IsFavorite")
		errs = append(errs, e)
		if r.Values["ViewCount"].Type != "" {
			i.ViewCount, e = r.integer("ViewCount")
			errs = append(errs, e)
		}
		i.LastViewRaw, e = r.nullableText("LastView")
		errs = append(errs, e)
		if i.LastViewRaw != nil {
			s, e := timestamp(*i.LastViewRaw)
			errs = append(errs, e)
			i.LastViewUTC = &s
		}
		i.DurationTicks, e = duration(r)
		if e != nil {
			i.Valid = false
			issues = append(issues, issue("invalid_duration", fmt.Sprintf("item:%d", i.ID), e.Error(), true))
		}
		for _, err := range errs {
			if err != nil {
				i.Valid = false
				issues = append(issues, issue("invalid_row", fmt.Sprintf("item:%d", i.ID), err.Error(), true))
			}
		}
		if !idValid(i.ID) || !knownKind(i.Kind) || i.ViewCount < 0 || i.Favorite < 0 || i.Favorite > 1 || i.FileName == "" {
			i.Valid = false
			issues = append(issues, issue("invalid_row", fmt.Sprintf("item:%d", i.ID), "out-of-range ID/scalar or unknown media kind", true))
		}
		if i.ThumbnailStatus != "NotGenerated" && i.ThumbnailStatus != "Generated" && i.ThumbnailStatus != "Failed" {
			issues = append(issues, issue("unknown_thumbnail_status", fmt.Sprintf("item:%d", i.ID), i.ThumbnailStatus, true))
		}
		model.Items = append(model.Items, i)
	}
	rows, e = rawTable(ctx, db, "LibraryItemTag")
	if e != nil {
		return model, issues, e
	}
	for _, r := range rows {
		a, e1 := r.integer("LibraryItemId")
		b, e2 := r.integer("TagsId")
		model.Joins = append(model.Joins, Join{a, b, e1 == nil && e2 == nil})
	}
	tags := []search.NamedTag{}
	for _, t := range model.Tags {
		if t.Valid {
			tags = append(tags, search.NamedTag{ID: t.ID, Name: t.Name})
		}
	}
	rows, e = rawTable(ctx, db, "Categories")
	if e != nil {
		return model, issues, e
	}
	for _, r := range rows {
		c := Category{Valid: true}
		c.ID, e = r.integer("Id")
		if e != nil || !idValid(c.ID) {
			c.Valid = false
		}
		c.Name, e = r.text("Name")
		if e != nil {
			c.Valid = false
		}
		c.Query, e = r.nullableText("TagQuery")
		if e != nil {
			c.Valid = false
		}
		c.Kind, e = r.nullableText("ItemsType")
		if e != nil || (c.Kind != nil && !knownKind(*c.Kind)) {
			c.Valid = false
		}
		q := ""
		if c.Query != nil {
			q = *c.Query
		}
		c.AST, e = legacyCategory(q, tags)
		if e != nil {
			c.Valid = false
		}
		if !c.Valid {
			issues = append(issues, issue("invalid_category", fmt.Sprintf("category:%d", c.ID), fmt.Sprintf("unsafe legacy category: %v", e), true))
		} else {
			c.SourceIDs = sourceCategoryIDs(model, c)
		}
		model.Categories = append(model.Categories, c)
	}
	rows, e = rawTable(ctx, db, "ProcessingFiles")
	if e != nil {
		return model, issues, e
	}
	for _, r := range rows {
		p := Processing{Valid: true}
		p.ID, e = r.integer("Id")
		if e != nil || !idValid(p.ID) {
			p.Valid = false
		}
		p.OriginalFileName, e = r.text("OriginalFileName")
		if e != nil {
			p.Valid = false
		}
		p.LibraryFileName, e = r.text("LibraryFileName")
		if e != nil {
			p.Valid = false
		}
		p.Kind, e = r.integer("FileType")
		if e != nil || p.Kind < 0 || p.Kind > 4 {
			p.Valid = false
		}
		p.Status, e = r.integer("Status")
		if e != nil || p.Status < 0 || p.Status > 7 {
			p.Valid = false
		}
		p.CollectionID, e = r.nullableInt("CollectionId")
		if e != nil {
			p.Valid = false
		}
		if !p.Valid {
			issues = append(issues, issue("invalid_processing", fmt.Sprintf("processing:%d", p.ID), "invalid ID, ordinal, status or raw type", true))
		}
		model.Processing = append(model.Processing, p)
	}
	return model, issues, nil
}
func sourceCategoryIDs(m Model, c Category) []int64 {
	out := []int64{}
	tags := map[int64]map[string]bool{}
	names := map[int64]string{}
	for _, t := range m.Tags {
		names[t.ID] = t.Name
	}
	for _, j := range m.Joins {
		if tags[j.ItemID] == nil {
			tags[j.ItemID] = map[string]bool{}
		}
		tags[j.ItemID][names[j.TagID]] = true
	}
	var eval func(*search.Expr, map[string]bool) bool
	eval = func(e *search.Expr, t map[string]bool) bool {
		switch e.Op {
		case search.All:
			return true
		case search.Tag:
			match := t[e.Term.SourceName]
			if e.Term.Negated {
				return !match
			}
			return match
		case search.And:
			return eval(e.Left, t) && eval(e.Right, t)
		case search.Or:
			return eval(e.Left, t) || eval(e.Right, t)
		}
		return false
	}
	for _, i := range m.Items {
		if (c.Kind == nil || *c.Kind == i.Kind) && eval(c.AST, tags[i.ID]) {
			out = append(out, i.ID)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
func classify(m *Manifest) {
	collections, tags, items := map[int64]bool{}, map[int64]bool{}, map[int64]bool{}
	for _, c := range m.Model.Collections {
		collections[c.ID] = c.Valid
	}
	for _, t := range m.Model.Tags {
		tags[t.ID] = t.Valid
	}
	used := map[string]bool{}
	latest := map[string]SourceObject{}
	for _, o := range m.Objects {
		if o.Latest && !o.DeleteMarker {
			latest[o.Bucket+"\x00"+o.Key] = o
		}
	}
	for idx := range m.Model.Items {
		i := &m.Model.Items[idx]
		i.Provenance = "legacy_unknown"
		i.OriginalAvailability = "unknown"
		primary, hasPrimary := latest["library\x00"+i.FileName]
		if !hasPrimary {
			i.OriginalAvailability = "missing_primary"
		} else {
			i.ProvenanceEvidence = []string{"primary matched exact LibraryItems.FileName", "primary SHA256=" + primary.SHA256}
			matches := []Processing{}
			owners := 0
			for _, other := range m.Model.Items {
				if other.FileName == i.FileName {
					owners++
				}
			}
			for _, job := range m.Model.Processing {
				if job.Valid && job.LibraryFileName == i.FileName {
					matches = append(matches, job)
				}
			}
			kinds := []string{"Unknown", "Text", "Image", "Video", "Audio"}
			if owners == 1 && len(matches) == 1 && matches[0].Status >= 5 && matches[0].Status <= 6 && kinds[matches[0].Kind] == i.Kind && primary.Tags["item-id"] == strconv.FormatInt(i.ID, 10) {
				job := matches[0]
				i.ProvenanceEvidence = append(i.ProvenanceEvidence, fmt.Sprintf("unique completed ProcessingFiles.Id=%d with matching enum and S3 item-id", job.ID))
				if i.Kind == "Video" && strings.HasSuffix(strings.ToLower(i.FileName), ".mp4") {
					i.Provenance = "legacy_transcoded"
					i.OriginalAvailability = "unavailable"
				} else if i.Kind != "Video" {
					i.Provenance = "legacy_copied"
					i.OriginalAvailability = "retained_as_primary"
				}
				if _, ok := latest["temporary\x00"+job.OriginalFileName]; ok {
					i.ProvenanceEvidence = append(i.ProvenanceEvidence, "same-name temporary object is archived; name alone does not prove original identity")
					if i.Kind == "Video" {
						i.OriginalAvailability = "unknown"
					}
				}
			} else if len(matches) > 1 || owners > 1 {
				i.ProvenanceEvidence = append(i.ProvenanceEvidence, "ambiguous processing or shared legacy key; no original identity inferred")
			}
		}
		items[i.ID] = i.Valid
		if i.CollectionID != nil && !collections[*i.CollectionID] {
			m.Issues = append(m.Issues, issue("dangling_collection", fmt.Sprintf("item:%d", i.ID), fmt.Sprintf("collection %d is absent", *i.CollectionID), true))
		}
		key := "library\x00" + i.FileName
		if _, ok := latest[key]; !ok {
			m.Issues = append(m.Issues, issue("missing_primary", fmt.Sprintf("item:%d", i.ID), i.FileName, true))
		} else {
			used[key] = true
		}
		if i.ThumbnailStatus == "Generated" {
			poster := "thumbnail\x00" + posterKey(i.FileName)
			if _, ok := latest[poster]; !ok {
				m.Issues = append(m.Issues, issue("missing_poster", fmt.Sprintf("item:%d", i.ID), posterKey(i.FileName), false))
			} else {
				used[poster] = true
			}
		}
	}
	for idx, j := range m.Model.Joins {
		if !items[j.ItemID] || !tags[j.TagID] {
			m.Model.Joins[idx].Valid = false
			m.Issues = append(m.Issues, issue("dangling_join", fmt.Sprintf("join:%d:%d", j.ItemID, j.TagID), "missing/invalid item or tag", true))
		}
	}
	for _, p := range m.Model.Processing {
		if p.CollectionID != nil && !collections[*p.CollectionID] {
			m.Issues = append(m.Issues, issue("dangling_processing_collection", fmt.Sprintf("processing:%d", p.ID), fmt.Sprintf("collection %d is absent", *p.CollectionID), false))
		}
		reason := "archived historical processing row; never scheduled as a new upload"
		if _, ok := latest["library\x00"+p.LibraryFileName]; ok {
			reason += "; final media exists"
		} else if _, ok := latest["temporary\x00"+p.OriginalFileName]; ok {
			reason += "; only temporary source remains"
		} else {
			reason += "; no available media"
		}
		m.Issues = append(m.Issues, issue("processing_archive", fmt.Sprintf("processing:%d", p.ID), reason, false))
	}
	for _, o := range m.Objects {
		if !o.DeleteMarker && (!o.Latest || !used[o.Bucket+"\x00"+o.Key]) {
			m.Issues = append(m.Issues, issue("quarantined_object", o.Identity(), "preserved in archive and independent target quarantine", false))
		}
	}
	if len(m.Inventory.Multipart) > 0 {
		m.Issues = append(m.Issues, issue("source_multipart", "source", "uncommitted source multipart bytes are not retrievable as objects; retain native rollback snapshot", true))
	}
}
func posterKey(key string) string {
	idx := strings.LastIndex(key, ".")
	slash := strings.LastIndexAny(key, "/\\")
	if idx > slash {
		return key[:idx] + ".jpg"
	}
	return key + ".jpg"
}
