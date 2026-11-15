package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/store"
)

var ctx = context.Background()

// This fixture is intentionally authored from EF migration columns, without
// using the importer or new-library schema to construct legacy records.
func legacyFixture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite3", p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
 CREATE TABLE __EFMigrationsHistory(MigrationId TEXT PRIMARY KEY, ProductVersion TEXT NOT NULL);
 INSERT INTO __EFMigrationsHistory VALUES
 ('20250622200914_Initial','9.0.5'),('20250716221835_AddCollections','9.0.5'),
 ('20250815135444_AddViewCount','9.0.5'),('20250929211941_ChangeVideoDurationType','9.0.5'),
 ('20250929214538_FixVideoDurationDataConversion','9.0.5'),('20260322165707_AddCollectionIdToProcessingFile','9.0.5');
 CREATE TABLE LibraryItems(Id INTEGER PRIMARY KEY AUTOINCREMENT, FileName TEXT NOT NULL, UploadedOn TEXT NOT NULL, FileType TEXT NOT NULL, Description TEXT, CollectionId INTEGER, VideoDuration INTEGER, ThumbnailStatus TEXT NOT NULL, IsFavorite INTEGER NOT NULL, LastView TEXT, ViewCount INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE Tags(Id INTEGER PRIMARY KEY AUTOINCREMENT,Name TEXT NOT NULL);
 CREATE TABLE Collections(Id INTEGER PRIMARY KEY AUTOINCREMENT,Name TEXT NOT NULL);
 CREATE TABLE Categories(Id INTEGER PRIMARY KEY AUTOINCREMENT,Name TEXT NOT NULL,TagQuery TEXT,ItemsType TEXT);
 CREATE TABLE LibraryItemTag(LibraryItemId INTEGER NOT NULL,TagsId INTEGER NOT NULL,PRIMARY KEY(LibraryItemId,TagsId),FOREIGN KEY(LibraryItemId) REFERENCES LibraryItems(Id),FOREIGN KEY(TagsId) REFERENCES Tags(Id));
 CREATE TABLE ProcessingFiles(Id INTEGER PRIMARY KEY AUTOINCREMENT,OriginalFileName TEXT NOT NULL,LibraryFileName TEXT NOT NULL,FileType INTEGER NOT NULL,Status INTEGER NOT NULL,CollectionId INTEGER);
 INSERT INTO Collections VALUES(7,'同じ'),(9,'同じ');
 INSERT INTO Tags VALUES(1,'猫'),(2,'猫'),(3,'Cat'),(4,'cat'),(5,'unused');
 INSERT INTO LibraryItems VALUES
 (11,'unknown.bin','2025-06-22 20:09:14.1234567','Unknown',NULL,7,NULL,'NotGenerated',0,NULL,0),
 (12,'text.txt','2025-06-23T20:09:14.1234567Z','Text','',NULL,0,'Failed',1,'2025-06-24 00:00:00',9),
 (13,'image.jpg','2025-06-24 20:09:14','Image','猫',9,9223372036854775807,'Generated',0,NULL,3),
 (14,'video.mp4','2025-06-25 20:09:14','Video',NULL,7,-9223372036854775808,'Generated',1,NULL,4),
 (15,'audio.aac','2025-06-26 20:09:14','Audio','audio',NULL,9007199254740993,'NotGenerated',0,NULL,7);
 INSERT INTO LibraryItemTag VALUES(11,1),(11,2),(12,3),(13,4),(14,2),(15,1);
 INSERT INTO Categories VALUES(3,'duplicates','Cat || cat',NULL),(4,'all',NULL,NULL),(5,'case','Cat || cat','Text'),(6,'negative','!unused',NULL);
 INSERT INTO ProcessingFiles VALUES(21,'source.mov','video.mp4',3,6,7),(22,'still waiting.mov','not-ready.mp4',3,0,NULL),(23,'failed.wav','failed.aac',4,7,NULL);
 `)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type fakeSource struct {
	objects []SourceObject
	data    map[string][]byte
	reads   int
}

func fixtureSource() *fakeSource {
	s := &fakeSource{data: map[string][]byte{}}
	for _, key := range []string{"unknown.bin", "text.txt", "image.jpg", "video.mp4", "audio.aac"} {
		o := SourceObject{Bucket: "library", Key: key, ETag: "etag-" + key, Size: int64(len(key)), ContentType: "application/octet-stream", Metadata: map[string]string{"source": "legacy"}, Tags: map[string]string{"item-id": "legacy"}, Latest: true}
		s.objects = append(s.objects, o)
		s.data[o.Identity()] = []byte(key)
	}
	for _, o := range []SourceObject{{Bucket: "thumbnail", Key: "image.jpg", ContentType: "image/jpeg", Latest: true}, {Bucket: "thumbnail", Key: "video.jpg", ContentType: "image/jpeg", Latest: true}, {Bucket: "temporary", Key: "still waiting.mov", Latest: true}, {Bucket: "library", Key: "orphan.bin", VersionID: "old", Latest: false}, {Bucket: "library", Key: "orphan.bin", VersionID: "new", Latest: true}} {
		o.Size = 3
		o.ETag = "etag-" + o.Identity()
		s.objects = append(s.objects, o)
		s.data[o.Identity()] = []byte("abc")
	}
	return s
}
func (s *fakeSource) Inventory(context.Context) (Inventory, error) {
	return Inventory{Objects: append([]SourceObject(nil), s.objects...)}, nil
}
func (s *fakeSource) Open(_ context.Context, o SourceObject) (io.ReadCloser, error) {
	s.reads++
	b, ok := s.data[o.Identity()]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

type fakeDestination struct {
	data      map[string][]byte
	info      map[string]objectstore.Object
	puts      int
	failAfter int
}

func newDestination() *fakeDestination {
	return &fakeDestination{data: map[string][]byte{}, info: map[string]objectstore.Object{}}
}
func (d *fakeDestination) Head(_ context.Context, k string) (objectstore.Object, error) {
	o, ok := d.info[k]
	if !ok {
		return o, ErrNotFound
	}
	return o, nil
}
func (d *fakeDestination) Put(_ context.Context, k string, r io.Reader, n int64, sha, ct string, m map[string]string) error {
	if d.failAfter > 0 && d.puts >= d.failAfter {
		return errors.New("injected interruption")
	}
	b, e := io.ReadAll(r)
	if e != nil {
		return e
	}
	d.puts++
	d.data[k] = b
	d.info[k] = objectstore.Object{Key: k, Size: int64(len(b)), ContentType: ct, Metadata: m}
	return nil
}
func (d *fakeDestination) Verify(_ context.Context, k string, n int64, h string) (objectstore.Verified, error) {
	o, e := d.Head(ctx, k)
	if e != nil {
		return objectstore.Verified{}, e
	}
	if int64(len(d.data[k])) != n || digest(d.data[k]) != h {
		return objectstore.Verified{}, objectstore.ErrIntegrity
	}
	return objectstore.Verified{Object: o, SHA256: h}, nil
}

func scanned(t *testing.T) (*Manifest, *fakeSource, string) {
	t.Helper()
	p := legacyFixture(t)
	s := fixtureSource()
	dir := filepath.Join(t.TempDir(), "archive")
	m, e := Scan(ctx, ScanConfig{Database: p, Archive: dir, SnapshotID: "fixture-frozen-1", RollbackReference: "fixture cold source", RollbackVerified: true}, s)
	if e != nil {
		t.Fatal(e)
	}
	return m, s, dir
}
func TestArchivePreservesAllRawRowsObjectsAndSourceBytes(t *testing.T) {
	p := legacyFixture(t)
	before, _ := os.ReadFile(p)
	s := fixtureSource()
	dir := filepath.Join(t.TempDir(), "archive")
	m, e := Scan(ctx, ScanConfig{Database: p, Archive: dir, SnapshotID: "frozen", RollbackReference: "cold fixture", RollbackVerified: true}, s)
	if e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Fatal("source DB changed")
	}
	if len(m.Objects) != 10 || len(m.Model.Items) != 5 || len(m.Model.Processing) != 3 {
		t.Fatalf("lost inventory: objects=%d model=%+v", len(m.Objects), m.Model)
	}
	if m.Model.Items[2].DurationTicks == nil || *m.Model.Items[2].DurationTicks != 9223372036854775807 || *m.Model.Items[3].DurationTicks != -9223372036854775808 || *m.Model.Items[4].DurationTicks != 9007199254740993 {
		t.Fatal("duration lost precision")
	}
	if m.Model.Items[0].DurationTicks != nil || *m.Model.Items[1].DurationTicks != 0 {
		t.Fatal("null/zero collapsed")
	}
	for _, o := range m.Objects {
		b, e := os.ReadFile(filepath.Join(dir, o.ArchivePath))
		if e != nil || digest(b) != o.SHA256 {
			t.Fatal("archive byte verification", e)
		}
	}
	loaded, e := LoadArchive(ctx, dir)
	if e != nil || loaded.Fingerprint != m.Fingerprint {
		t.Fatal("load frozen archive", e)
	}
	if e = CheckFrozen(ctx, m, s); e != nil {
		t.Fatal(e)
	}
	s.objects[0].Metadata = map[string]string{"changed": "yes"}
	if e = CheckFrozen(ctx, m, s); !errors.Is(e, ErrSourceChanged) {
		t.Fatalf("metadata mutation accepted: %v", e)
	}
}
func TestCopyResumeDoesNotRewriteVerifiedBytesAndComparesCatalog(t *testing.T) {
	m, s, dir := scanned(t)
	db, e := store.Open(ctx, filepath.Join(t.TempDir(), "target"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	d := newDestination()
	cfg := ApplyConfig{Archive: dir, Journal: filepath.Join(t.TempDir(), "journal.sqlite"), TargetID: "fixture-target", Budget: Budget{WritePeakBytes: 1, MetadataBytes: 1, ReserveBytes: 1}, Capacity: func(context.Context) (int64, int64, error) { return 1 << 40, 1 << 20, nil }}
	d.failAfter = 2
	if e = Apply(ctx, cfg, m, s, db, d); e == nil {
		t.Fatal("expected interrupted copy")
	}
	count := d.puts
	d.failAfter = 0
	if e = Apply(ctx, cfg, m, s, db, d); e != nil {
		t.Fatal(e)
	}
	if d.puts-count != len(d.data)-count {
		t.Fatal("resume rewrote verified keys")
	}
	report, e := Verify(ctx, cfg, m, s, db, d)
	if e != nil || !report.Complete {
		t.Fatalf("verify %+v %v", report, e)
	}
	var got int64
	db.Reader().QueryRow(`SELECT duration_ticks FROM items WHERE id=15`).Scan(&got)
	if got != 9007199254740993 {
		t.Fatal("target ticks changed")
	}
	var names int
	db.Reader().QueryRow(`SELECT count(*) FROM tags WHERE name='猫' COLLATE BINARY`).Scan(&names)
	if names != 2 {
		t.Fatal("duplicate Unicode names merged")
	}
	if len(report.Categories) != 4 || !reflect.DeepEqual(report.Categories[0].SourceIDs, []int64{12, 13}) {
		t.Fatalf("category semantics %+v", report.Categories)
	}
	if e = Apply(ctx, cfg, m, s, db, d); e != nil {
		t.Fatal(e)
	}
	if d.puts != len(d.data) {
		t.Fatal("idempotent replay overwrote media")
	}
	for k := range d.data {
		d.data[k] = []byte("bad")
		break
	}
	if _, e = Verify(ctx, cfg, m, s, db, d); e == nil {
		t.Fatal("destination corruption accepted")
	}
}
func TestMalformedOrphansAndMissingAreExplicitAndNeverDiscardRaw(t *testing.T) {
	p := legacyFixture(t)
	db, _ := sql.Open("sqlite3", p)
	_, e := db.Exec(`UPDATE LibraryItems SET VideoDuration='not ticks',CollectionId=999 WHERE Id=12;INSERT INTO LibraryItemTag VALUES(999,999);INSERT INTO Categories VALUES(99,'bad','猫 @',NULL);INSERT INTO ProcessingFiles VALUES(99,'missing','missing',77,99,999)`)
	db.Close()
	if e != nil {
		t.Fatal(e)
	}
	s := fixtureSource()
	s.objects = s.objects[1:]
	dir := filepath.Join(t.TempDir(), "archive")
	m, e := Scan(ctx, ScanConfig{Database: p, Archive: dir, SnapshotID: "bad-frozen", RollbackReference: "cold fixture", RollbackVerified: true}, s)
	if e != nil {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	for _, x := range m.Issues {
		seen[x.Code] = true
	}
	for _, code := range []string{"invalid_duration", "dangling_collection", "dangling_join", "missing_primary", "invalid_category", "invalid_processing"} {
		if !seen[code] {
			t.Errorf("missing issue %s: %+v", code, m.Issues)
		}
	}
	if _, e = LoadArchive(ctx, dir); e != nil {
		t.Fatal("malformed raw archive not preserved", e)
	}
	target, _ := store.Open(ctx, filepath.Join(t.TempDir(), "target"))
	defer target.Close()
	d := newDestination()
	cfg := ApplyConfig{Archive: dir, Journal: filepath.Join(t.TempDir(), "journal.sqlite"), TargetID: "t", Budget: Budget{ReserveBytes: 1}, Capacity: func(context.Context) (int64, int64, error) { return 1 << 40, 1 << 20, nil }}
	if e = Apply(ctx, cfg, m, s, target, d); !errors.Is(e, ErrUnresolved) {
		t.Fatalf("unaccepted losses applied: %v", e)
	}
	if d.puts != 0 {
		t.Fatal("wrote bytes before unresolved gate")
	}
}
func TestArchiveTamperAndInsufficientSpaceStopBeforeCopy(t *testing.T) {
	m, s, dir := scanned(t)
	db, _ := store.Open(ctx, filepath.Join(t.TempDir(), "target"))
	defer db.Close()
	d := newDestination()
	cfg := ApplyConfig{Archive: dir, Journal: filepath.Join(t.TempDir(), "journal.sqlite"), TargetID: "t", Budget: Budget{ReserveBytes: 1}, Capacity: func(context.Context) (int64, int64, error) { return 1, 0, nil }}
	if e := Apply(ctx, cfg, m, s, db, d); !errors.Is(e, ErrBudget) {
		t.Fatal(e)
	}
	if d.puts != 0 {
		t.Fatal("budget did not gate writes")
	}
	os.WriteFile(filepath.Join(dir, m.Objects[0].ArchivePath), []byte("tamper"), 0600)
	if _, e := LoadArchive(ctx, dir); e == nil {
		t.Fatal("tampered archive accepted")
	}
}
func TestStrictTimeSpanParserUsesIntegerArithmetic(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want int64
	}{{"00:00:00", 0}, {"1.02:03:04.1234567", 937841234567}, {"-00:00:00.0000001", -1}, {"10675199.02:48:05.4775807", 9223372036854775807}, {"-10675199.02:48:05.4775808", -9223372036854775808}} {
		got, e := parseDurationText(tt.in)
		if e != nil || got != tt.want {
			t.Errorf("%s: %d %v", tt.in, got, e)
		}
	}
	for _, in := range []string{"1.99:00:00", "25:00:00", "00:60:00", "1e9", "00:00:00.00000001", "10675199.02:48:05.4775808"} {
		if _, e := parseDurationText(in); e == nil {
			t.Errorf("accepted %s", in)
		}
	}
}

type fakeMultipartDestination struct {
	*fakeDestination
	uploadID  string
	parts     map[int]objectstore.Part
	bodies    map[int][]byte
	partCalls int
	interrupt bool
}

func (d *fakeMultipartDestination) CreateMultipart(context.Context, string, string, map[string]string) (string, error) {
	d.uploadID = "owned-mpu"
	return d.uploadID, nil
}
func (d *fakeMultipartDestination) ListMultipart(context.Context, string) ([]objectstore.Multipart, error) {
	if d.uploadID == "" {
		return nil, nil
	}
	return []objectstore.Multipart{{UploadID: d.uploadID}}, nil
}
func (d *fakeMultipartDestination) ListParts(context.Context, string, string) ([]objectstore.Part, error) {
	out := []objectstore.Part{}
	for _, p := range d.parts {
		p.SHA256 = ""
		out = append(out, p)
	}
	return out, nil
}
func (d *fakeMultipartDestination) UploadPart(_ context.Context, key, id string, number int, size int64, sha string, r io.Reader) (objectstore.Part, error) {
	b, e := io.ReadAll(r)
	if e != nil {
		return objectstore.Part{}, e
	}
	d.partCalls++
	p := objectstore.Part{Number: number, Size: size, ETag: fmt.Sprint("etag-", number), SHA256: sha}
	d.parts[number] = p
	d.bodies[number] = b
	if d.interrupt && number == 2 {
		return objectstore.Part{}, errors.New("lost part response")
	}
	return p, nil
}
func (d *fakeMultipartDestination) Complete(_ context.Context, key, id string, parts []objectstore.Part) (objectstore.Verified, error) {
	var b []byte
	for _, p := range parts {
		b = append(b, d.bodies[p.Number]...)
	}
	d.data[key] = b
	d.info[key] = objectstore.Object{Key: key, Size: int64(len(b)), ContentType: "application/octet-stream", Metadata: map[string]string{"source": "legacy"}}
	d.uploadID = ""
	return d.Verify(ctx, key, int64(len(b)), digest(b))
}
func TestMultipartResumeReconcilesBoundPartAfterLostResponse(t *testing.T) {
	p := legacyFixture(t)
	s := fixtureSource()
	large := bytes.Repeat([]byte("abcdef"), 2<<20)
	for idx, o := range s.objects {
		if o.Key == "video.mp4" {
			s.objects[idx].Size = int64(len(large))
			s.data[o.Identity()] = large
		}
	}
	dir := filepath.Join(t.TempDir(), "archive")
	m, e := Scan(ctx, ScanConfig{Database: p, Archive: dir, SnapshotID: "large", RollbackReference: "cold", RollbackVerified: true}, s)
	if e != nil {
		t.Fatal(e)
	}
	db, e := store.Open(ctx, filepath.Join(t.TempDir(), "target"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	d := &fakeMultipartDestination{fakeDestination: newDestination(), parts: map[int]objectstore.Part{}, bodies: map[int][]byte{}, interrupt: true}
	c := ApplyConfig{Archive: dir, Journal: filepath.Join(t.TempDir(), "journal.sqlite"), TargetID: "multipart", PartSize: 5 << 20, SinglePutLimit: 5 << 20, Budget: Budget{ReserveBytes: 1}, Capacity: func(context.Context) (int64, int64, error) { return 1 << 40, 1 << 20, nil }}
	if e = Apply(ctx, c, m, s, db, d); e == nil {
		t.Fatal("expected lost part response")
	}
	d.interrupt = false
	if e = Apply(ctx, c, m, s, db, d); e != nil {
		t.Fatal(e)
	}
	if d.partCalls != 3 {
		t.Fatalf("bound acknowledged parts were resent: %d", d.partCalls)
	}
	if _, e = Verify(ctx, c, m, s, db, d); e != nil {
		t.Fatal(e)
	}
}

func TestFrozenWALSnapshotNeverTouchesSourceFiles(t *testing.T) {
	p := legacyFixture(t)
	db, e := sql.Open("sqlite3", p)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, e = db.Exec(`PRAGMA journal_mode=WAL;PRAGMA wal_autocheckpoint=0;UPDATE LibraryItems SET Description='committed WAL only' WHERE Id=11`); e != nil {
		t.Fatal(e)
	}
	before, e := nativeFiles(p)
	if e != nil {
		t.Fatal(e)
	}
	if len(before) < 2 {
		t.Fatal("fixture did not retain WAL")
	}
	dir := filepath.Join(t.TempDir(), "archive")
	m, e := Scan(ctx, ScanConfig{Database: p, Archive: dir, SnapshotID: "frozen-wal", RollbackReference: "cold", RollbackVerified: true}, fixtureSource())
	if e != nil {
		t.Fatal(e)
	}
	after, _ := nativeFiles(p)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("read-only source files changed")
	}
	if m.Model.Items[0].Description == nil || *m.Model.Items[0].Description != "committed WAL only" {
		t.Fatal("committed WAL not recovered into independent snapshot")
	}
}
func TestUnsafeObjectKeysNeverBecomeLocalPaths(t *testing.T) {
	p := legacyFixture(t)
	s := fixtureSource()
	for _, key := range []string{"../../escape", "/absolute/path", "猫/../Cat\n?.dat"} {
		o := SourceObject{Bucket: "temporary", Key: key, Size: 3, Latest: true}
		s.objects = append(s.objects, o)
		s.data[o.Identity()] = []byte("abc")
	}
	dir := filepath.Join(t.TempDir(), "archive")
	m, e := Scan(ctx, ScanConfig{Database: p, Archive: dir, SnapshotID: "keys", RollbackReference: "cold", RollbackVerified: true}, s)
	if e != nil {
		t.Fatal(e)
	}
	for _, o := range m.Objects {
		if !strings.HasPrefix(o.ArchivePath, "objects/") || len(filepath.Base(o.ArchivePath)) != 64 {
			t.Fatal("legacy key escaped hash layout")
		}
	}
	p = filepath.Join(dir, m.Objects[0].ArchivePath)
	os.Remove(p)
	os.Symlink("/etc/passwd", p)
	if _, e = LoadArchive(ctx, dir); e == nil {
		t.Fatal("archive symlink accepted")
	}
}
func TestAcceptedExceptionsRetainMissingMetadataAndArchivedRows(t *testing.T) {
	p := legacyFixture(t)
	db, _ := sql.Open("sqlite3", p)
	db.Exec(`UPDATE LibraryItems SET CollectionId=999 WHERE Id=12;INSERT INTO LibraryItemTag VALUES(999,999);INSERT INTO Categories VALUES(88,'unicode legacy bug','猫',NULL)`)
	db.Close()
	s := fixtureSource()
	s.objects = s.objects[1:]
	dir := filepath.Join(t.TempDir(), "archive")
	m, e := Scan(ctx, ScanConfig{Database: p, Archive: dir, SnapshotID: "accepted", RollbackReference: "cold", RollbackVerified: true}, s)
	if e != nil {
		t.Fatal(e)
	}
	exceptions := []Exception{}
	for _, i := range m.Issues {
		if !i.Blocking {
			continue
		}
		decision := "archive_only"
		switch i.Code {
		case "missing_primary":
			decision = "missing_media"
		case "dangling_collection":
			decision = "clear_reference"
		}
		exceptions = append(exceptions, Exception{i.ID, decision, "fixture owner reviewed raw source", "fixture-owner"})
	}
	target, _ := store.Open(ctx, filepath.Join(t.TempDir(), "target"))
	defer target.Close()
	c := ApplyConfig{Archive: dir, Journal: filepath.Join(t.TempDir(), "journal.sqlite"), TargetID: "accepted", Exceptions: exceptions, Budget: Budget{ReserveBytes: 1}, Capacity: func(context.Context) (int64, int64, error) { return 1 << 40, 1 << 20, nil }}
	d := newDestination()
	if e = Apply(ctx, c, m, s, target, d); e != nil {
		t.Fatal(e)
	}
	report, e := Verify(ctx, c, m, s, target, d)
	if e != nil || !report.Complete || len(report.Exceptions) != len(exceptions) {
		t.Fatal(report, e)
	}
	var state string
	target.Reader().QueryRow(`SELECT state FROM items WHERE id=11`).Scan(&state)
	if state != "missing" {
		t.Fatal("missing item metadata dropped")
	}
	for _, category := range report.Categories {
		if category.ID == 4 {
			for _, id := range category.TargetIDs {
				if id == 11 {
					t.Fatal("category parity report included a missing item that catalog cannot display")
				}
			}
			found := false
			for _, id := range category.SourceIDs {
				found = found || id == 11
			}
			if !found {
				t.Fatal("source category loss was hidden")
			}
		}
	}

	var collection any
	target.Reader().QueryRow(`SELECT collection_id FROM items WHERE id=12`).Scan(&collection)
	if collection != nil {
		t.Fatal("dangling collection was not explicitly cleared")
	}
}

func TestSharedLegacyKeyProducesIndependentOwnedPrimaryCopies(t *testing.T) {
	p := legacyFixture(t)
	db, _ := sql.Open("sqlite3", p)
	db.Exec(`UPDATE LibraryItems SET FileName='image.jpg' WHERE Id=15;INSERT INTO Tags VALUES(6,'Cat');INSERT INTO LibraryItemTag VALUES(14,6)`)
	db.Close()
	s := fixtureSource()
	dir := filepath.Join(t.TempDir(), "archive")
	m, e := Scan(ctx, ScanConfig{Database: p, Archive: dir, SnapshotID: "shared", RollbackReference: "cold", RollbackVerified: true}, s)
	if e != nil {
		t.Fatal(e)
	}
	target, _ := store.Open(ctx, filepath.Join(t.TempDir(), "target"))
	defer target.Close()
	c := ApplyConfig{Archive: dir, Journal: filepath.Join(t.TempDir(), "journal.sqlite"), TargetID: "shared", Budget: Budget{ReserveBytes: 1}, Capacity: func(context.Context) (int64, int64, error) { return 1 << 40, 1 << 20, nil }}
	d := newDestination()
	if e = Apply(ctx, c, m, s, target, d); e != nil {
		t.Fatal(e)
	}
	report, e := Verify(ctx, c, m, s, target, d)
	if e != nil {
		t.Fatal(e)
	}
	var a, b string
	target.Reader().QueryRow(`SELECT primary_asset_id FROM items WHERE id=13`).Scan(&a)
	target.Reader().QueryRow(`SELECT primary_asset_id FROM items WHERE id=15`).Scan(&b)
	if a == b || a == "" || b == "" {
		t.Fatal("shared legacy primary was merged")
	}
	if !reflect.DeepEqual(report.Categories[0].SourceIDs, []int64{12, 13, 14}) {
		t.Fatal("duplicate tag category changed", report.Categories)
	}
}
func TestUnexpectedSchemaIsArchivedAndBlocksImport(t *testing.T) {
	p := legacyFixture(t)
	db, _ := sql.Open("sqlite3", p)
	db.Exec(`ALTER TABLE LibraryItems ADD COLUMN ForgottenSourceData TEXT;UPDATE LibraryItems SET ForgottenSourceData='must retain'`)
	db.Close()
	dir := filepath.Join(t.TempDir(), "archive")
	m, e := Scan(ctx, ScanConfig{Database: p, Archive: dir, SnapshotID: "unexpected", RollbackReference: "cold", RollbackVerified: true}, fixtureSource())
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, i := range m.Issues {
		if i.Code == "unknown_schema" {
			found = true
		}
	}
	if !found {
		t.Fatal("unknown schema ignored")
	}
	if _, e = LoadArchive(ctx, dir); e != nil {
		t.Fatal(e)
	}
}
func TestFrozenFingerprintDoesNotTrustETagAsContentHash(t *testing.T) {
	m, s, _ := scanned(t)
	o := m.Objects[0]
	s.data[o.Identity()] = bytes.Repeat([]byte("x"), int(o.Size))
	if e := CheckFrozen(ctx, m, s); !errors.Is(e, ErrSourceChanged) {
		t.Fatalf("changed bytes with unchanged ETag were accepted: %v", e)
	}
}

func TestInterruptionAfterVerifiedObjectsBeforeCatalogCommitResumesWithoutOverwrite(t *testing.T) {
	m, s, dir := scanned(t)
	db, _ := store.Open(ctx, filepath.Join(t.TempDir(), "target"))
	defer db.Close()
	if e := db.Write(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec(`CREATE TRIGGER fixture_fail_import BEFORE INSERT ON items BEGIN SELECT RAISE(ABORT,'injected metadata interruption'); END`)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	c := ApplyConfig{Archive: dir, Journal: filepath.Join(t.TempDir(), "journal.sqlite"), TargetID: "sql-interruption", Budget: Budget{ReserveBytes: 1}, Capacity: func(context.Context) (int64, int64, error) { return 1 << 40, 1 << 20, nil }}
	d := newDestination()
	if e := Apply(ctx, c, m, s, db, d); e == nil {
		t.Fatal("injected SQL interruption not reached")
	}
	puts := d.puts
	var rows int
	db.Reader().QueryRow(`SELECT count(*) FROM tags`).Scan(&rows)
	if rows != 0 {
		t.Fatal("catalog import was not atomic")
	}
	if e := db.Write(ctx, func(tx *sql.Tx) error { _, e := tx.Exec(`DROP TRIGGER fixture_fail_import`); return e }); e != nil {
		t.Fatal(e)
	}
	if e := Apply(ctx, c, m, s, db, d); e != nil {
		t.Fatal(e)
	}
	if d.puts != puts {
		t.Fatal("recovery rewrote already verified bytes")
	}
	if _, e := Verify(ctx, c, m, s, db, d); e != nil {
		t.Fatal(e)
	}
}
func TestUnknownJournalDatabaseIsRejectedWithoutModification(t *testing.T) {
	m, s, dir := scanned(t)
	p := filepath.Join(t.TempDir(), "unrelated.sqlite")
	other, _ := sql.Open("sqlite3", p)
	other.Exec(`CREATE TABLE important(bytes BLOB);INSERT INTO important VALUES(X'001122')`)
	other.Close()
	before, _ := os.ReadFile(p)
	db, _ := store.Open(ctx, filepath.Join(t.TempDir(), "target"))
	defer db.Close()
	d := newDestination()
	c := ApplyConfig{Archive: dir, Journal: p, TargetID: "wrong-journal", Budget: Budget{ReserveBytes: 1}, Capacity: func(context.Context) (int64, int64, error) { return 1 << 40, 1 << 20, nil }}
	if e := Apply(ctx, c, m, s, db, d); e == nil {
		t.Fatal("unrecognized SQLite file used as journal")
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Fatal("unrecognized journal database was modified")
	}
	if d.puts != 0 {
		t.Fatal("copied before journal ownership was established")
	}
}
func TestTimestampCanonicalizationMatchesProductionTickSortFormat(t *testing.T) {
	for _, in := range []string{"2025-06-24 20:09:14", "2025-06-24T20:09:14.0000000Z"} {
		got, e := timestamp(in)
		if e != nil || got != "2025-06-24T20:09:14.0000000Z" {
			t.Fatalf("timestamp sort format: %s %v", got, e)
		}
	}
	if _, e := timestamp("2025-06-24T20:09:14.000000001Z"); e == nil {
		t.Fatal("silently rounded sub-tick timestamp")
	}
}

func TestArchiveBudgetUsesProductionFilesystemObservation(t *testing.T) {
	root := t.TempDir()
	if e := localBudget(root, 1, 1); e != nil {
		t.Fatal(e)
	}
	if e := localBudget(root, maxInt-1, 0); !errors.Is(e, ErrBudget) {
		t.Fatal("impossible archive budget accepted", e)
	}
}

func TestResumeAndCompletedReplayBudgetCreditsOnlyFullyVerifiedCopies(t *testing.T) {
	m, s, dir := scanned(t)
	db, e := store.Open(ctx, filepath.Join(t.TempDir(), "target"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	d := newDestination()
	var library string
	db.Reader().QueryRow(`SELECT library_id FROM library_meta WHERE singleton=1`).Scan(&library)
	var planned int64
	for _, p := range makePlans(m, library) {
		planned += p.Size
	}
	const overhead int64 = 30
	c := ApplyConfig{Archive: dir, Journal: filepath.Join(t.TempDir(), "journal.sqlite"), TargetID: "decreasing-capacity", Budget: Budget{WritePeakBytes: 10, MetadataBytes: 10, ReserveBytes: 10}, Capacity: func(context.Context) (int64, int64, error) {
		var used int64
		for _, b := range d.data {
			used += int64(len(b))
		}
		return planned + overhead - used, 100000, nil
	}}
	d.failAfter = 2
	if e = Apply(ctx, c, m, s, db, d); e == nil {
		t.Fatal("expected interruption")
	}
	if d.puts != 2 {
		t.Fatal("fixture did not persist two copies")
	}
	d.failAfter = 0
	if e = Apply(ctx, c, m, s, db, d); e != nil {
		t.Fatalf("already copied bytes were charged again: %v", e)
	}
	puts := d.puts
	if e = Apply(ctx, c, m, s, db, d); e != nil {
		t.Fatalf("completed replay requires payload space again: %v", e)
	}
	if d.puts != puts {
		t.Fatal("completed replay rewrote bytes")
	}
	for key := range d.data {
		d.data[key][0] ^= 0xff
		break
	}
	if e = Apply(ctx, c, m, s, db, d); !errors.Is(e, objectstore.ErrIntegrity) {
		t.Fatalf("corrupt key received optimistic budget credit: %v", e)
	}
	if d.puts != puts {
		t.Fatal("corrupt immutable bytes were overwritten")
	}
}

func TestVerifyRejectsRuntimeStateOutsideImportedCatalog(t *testing.T) {
	m, s, dir := scanned(t)
	db, _ := store.Open(ctx, filepath.Join(t.TempDir(), "target"))
	defer db.Close()
	d := newDestination()
	c := ApplyConfig{Archive: dir, Journal: filepath.Join(t.TempDir(), "journal.sqlite"), TargetID: "target-integrity", Budget: Budget{ReserveBytes: 1}, Capacity: func(context.Context) (int64, int64, error) { return 1 << 40, 100000, nil }}
	if e := Apply(ctx, c, m, s, db, d); e != nil {
		t.Fatal(e)
	}
	if e := db.Write(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec(`INSERT INTO operation_results VALUES('fixture-op','hash','completed',200,'{}','now','later')`)
		return e
	}); e != nil {
		t.Fatal(e)
	}

	if _, e := Verify(ctx, c, m, s, db, d); e == nil {
		t.Fatal("unexpected runtime receipt outside migrated state was accepted")
	}
}
