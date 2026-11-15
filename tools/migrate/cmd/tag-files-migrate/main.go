// tag-files-migrate is an external offline executable, never an application subcommand.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mikd1/tag-files/app/internal/librarybinding"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
	"github.com/mikd1/tag-files/app/internal/store"
	migrate "github.com/mikd1/tag-files/app/tools/migrate"
)

type endpoint struct {
	Endpoint, Region, AccessKeyFile, SecretKeyFile string
	Buckets                                        []string
}
type settings struct {
	FFprobe                                          string
	Source                                           endpoint
	Database, Archive, SnapshotID, RollbackReference string
	RollbackVerified                                 bool
	ArchiveReserveBytes                              int64
	Target                                           endpoint
	TargetMetadata, Journal                          string
	Budget                                           migrate.Budget
	Exceptions                                       []migrate.Exception
	PartSize, SinglePutLimit                         int64
}

func readSecret(path string) (string, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return "", e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return "", fmt.Errorf("credential file must be a private regular file")
	}
	b, e := io.ReadAll(io.LimitReader(f, 4097))
	if e != nil {
		return "", e
	}
	s := strings.TrimSuffix(string(b), "\n")
	if len(b) > 4096 || s == "" || strings.ContainsAny(s, "\x00\r\n") {
		return "", fmt.Errorf("invalid credential file")
	}
	return s, nil
}
func endpointConfig(e endpoint) (objectstore.Config, error) {
	a, err := readSecret(e.AccessKeyFile)
	if err != nil {
		return objectstore.Config{}, err
	}
	s, err := readSecret(e.SecretKeyFile)
	if err != nil {
		return objectstore.Config{}, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return objectstore.Config{Endpoint: e.Endpoint, Region: e.Region, AccessKey: a, SecretKey: s, Bucket: "tag-files", HTTPClient: &http.Client{Timeout: 10 * time.Minute, Transport: transport}}, nil
}
func readSettings(path string) (settings, error) {
	var s settings
	f, e := os.Open(path)
	if e != nil {
		return s, e
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	d.DisallowUnknownFields()
	if e = d.Decode(&s); e != nil {
		return s, e
	}
	var more any
	if d.Decode(&more) != io.EOF {
		return s, fmt.Errorf("one config JSON object required")
	}
	return s, nil
}
func canonicalRoot(path string) (string, error) {
	abs, e := filepath.Abs(path)
	if e != nil {
		return "", e
	}
	tail := []string{}
	p := abs
	for {
		resolved, e := filepath.EvalSymlinks(p)
		if e == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved, nil
		}
		if !errors.Is(e, os.ErrNotExist) {
			return "", e
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", e
		}
		tail = append(tail, filepath.Base(p))
		p = parent
	}
}
func rootsDisjoint(s settings) error {
	paths := []string{filepath.Dir(s.Database), s.Archive, s.TargetMetadata, filepath.Dir(s.Journal)}
	for i, p := range paths {
		if p == "" {
			return fmt.Errorf("explicit source/archive/target/journal paths required")
		}
		abs, e := canonicalRoot(p)
		if e != nil {
			return e
		}
		paths[i] = filepath.Clean(abs)
	}
	for i, a := range paths {
		for j, b := range paths {
			if i >= j {
				continue
			}
			r, e := filepath.Rel(a, b)
			if e != nil {
				return e
			}
			if r == "." || (!strings.HasPrefix(r, ".."+string(os.PathSeparator)) && r != "..") {
				return fmt.Errorf("source, archive, target and journal directories must be disjoint")
			}
			r, e = filepath.Rel(b, a)
			if e != nil {
				return e
			}
			if r == "." || (!strings.HasPrefix(r, ".."+string(os.PathSeparator)) && r != "..") {
				return fmt.Errorf("source, archive, target and journal directories must be disjoint")
			}
		}
	}
	return nil
}
func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("tag-files-migrate", flag.ContinueOnError)
	config := fs.String("config", "", "private configuration JSON")
	if len(args) < 1 {
		return fmt.Errorf("usage: tag-files-migrate {scan|copy|verify|probe} -config settings.json")
	}
	command := args[0]
	if command != "scan" && command != "copy" && command != "verify" && command != "probe" {
		return fmt.Errorf("unknown command %q", command)
	}
	if e := fs.Parse(args[1:]); e != nil {
		return e
	}
	if fs.NArg() != 0 || *config == "" {
		return fmt.Errorf("-config is required")
	}
	s, e := readSettings(*config)
	if e != nil {
		return e
	}
	if e = rootsDisjoint(s); e != nil {
		return e
	}
	if command == "probe" {
		ffprobe := s.FFprobe
		if ffprobe == "" {
			ffprobe = "ffprobe"
		}
		report, e := migrate.Probe(ctx, s.Archive, ffprobe)
		if outputErr := json.NewEncoder(os.Stdout).Encode(report); outputErr != nil {
			return outputErr
		}
		return e
	}
	sourceConfig, e := endpointConfig(s.Source)
	if e != nil {
		return e
	}
	source, e := migrate.NewS3Source(sourceConfig, s.Source.Buckets)
	if e != nil {
		return e
	}
	if command == "scan" {
		reserve := s.ArchiveReserveBytes
		if reserve == 0 {
			reserve = 10000000000
		}
		m, e := migrate.Scan(ctx, migrate.ScanConfig{Database: s.Database, Archive: s.Archive, SnapshotID: s.SnapshotID, RollbackReference: s.RollbackReference, RollbackVerified: s.RollbackVerified, ArchiveReserveBytes: reserve}, source)
		if e != nil {
			return e
		}
		blocking := 0
		for _, i := range m.Issues {
			if i.Blocking {
				blocking++
			}
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"runId": m.RunID, "sourceFingerprint": m.Fingerprint, "objectsArchived": len(m.Objects), "issues": len(m.Issues), "blockingIssues": blocking})
	}
	if strings.TrimRight(strings.ToLower(s.Source.Endpoint), "/") == strings.TrimRight(strings.ToLower(s.Target.Endpoint), "/") {
		return fmt.Errorf("source and destination S3 must be separate origins")
	}
	if s.Budget.WritePeakBytes <= 0 || s.Budget.MetadataBytes <= 0 {
		return fmt.Errorf("measured pilot writePeakBytes and metadataBytes must be positive")
	}
	m, e := migrate.LoadArchive(ctx, s.Archive)
	if e != nil {
		return e
	}
	if e = migrate.CheckFrozen(ctx, m, source); e != nil {
		return e
	}
	targetConfig, e := endpointConfig(s.Target)
	if e != nil {
		return e
	}
	control, e := storageprotocol.NewClient(targetConfig.Endpoint, targetConfig.AccessKey, targetConfig.SecretKey, nil)
	if e != nil {
		return e
	}
	identity, e := control.Identity(ctx)
	if e != nil {
		return e
	}
	if command == "copy" && identity.LibraryID == "" {
		c, e := objectstore.New(targetConfig)
		if e != nil {
			return e
		}
		page, e := c.List(ctx, "", "", 1)
		if e != nil {
			return e
		}
		mpus, e := c.ListMultipart(ctx, "")
		if e != nil {
			return e
		}
		if len(page.Objects) > 0 || len(mpus) > 0 {
			return fmt.Errorf("unbound target storage must be empty")
		}
	}
	if command == "verify" {
		if _, e = os.Stat(filepath.Join(s.TargetMetadata, "library.sqlite")); e != nil {
			return e
		}
	}
	db, e := store.Open(ctx, s.TargetMetadata)
	if e != nil {
		return e
	}
	defer db.Close()
	pair, e := librarybinding.Ensure(ctx, db, control)
	if e != nil {
		return e
	}
	client, e := objectstore.New(targetConfig)
	if e != nil {
		return e
	}
	client, e = client.AtLibrary(pair.StorageID, pair.LibraryID, pair.VolumeID)
	if e != nil {
		return e
	}
	epoch, e := client.StorageEpoch(ctx)
	if e != nil {
		return e
	}
	client, e = client.AtEpoch(epoch)
	if e != nil {
		return e
	}
	c := migrate.ApplyConfig{Archive: s.Archive, Journal: s.Journal, TargetID: pair.StorageID + "/" + pair.VolumeID, Exceptions: s.Exceptions, Budget: s.Budget, PartSize: s.PartSize, SinglePutLimit: s.SinglePutLimit, Capacity: func(ctx context.Context) (int64, int64, error) {
		capacity, e := client.Capacity(ctx)
		if e != nil {
			return 0, 0, e
		}
		if capacity.AvailableInodes == nil {
			return 0, 0, fmt.Errorf("destination inode capacity unavailable")
		}
		return capacity.AvailableBytes, *capacity.AvailableInodes, nil
	}}
	if command == "copy" {
		if e = migrate.Apply(ctx, c, m, source, db, client); e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"runId": m.RunID, "copied": true, "next": "verify"})
	}
	report, e := migrate.Verify(ctx, c, m, source, db, client)
	if e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if e := run(ctx, os.Args[1:]); e != nil {
		if errors.Is(e, context.Canceled) {
			fmt.Fprintln(os.Stderr, "migration interrupted at durable checkpoint")
		} else {
			fmt.Fprintln(os.Stderr, e)
		}
		os.Exit(1)
	}
}
