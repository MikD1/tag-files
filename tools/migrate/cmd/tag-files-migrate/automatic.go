package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"syscall"

	"github.com/mikd1/tag-files/app/internal/store"
	migrate "github.com/mikd1/tag-files/app/tools/migrate"
)

type automaticRecord struct {
	Version        int                     `json:"version"`
	Identity       string                  `json:"settingsIdentity"`
	BaseArchive    string                  `json:"baseArchive"`
	CurrentArchive string                  `json:"archive"`
	Attempts       []string                `json:"archiveAttempts"`
	Buckets        []string                `json:"buckets"`
	Stage          string                  `json:"stage"`
	CopyStarted    bool                    `json:"copyStarted"`
	Exceptions     []migrate.Exception     `json:"exceptions"`
	Budget         *migrate.BudgetEvidence `json:"budgetEvidence,omitempty"`
}

type automaticState struct {
	automaticRecord
	path string
	lock *os.File
}

func privateJSON(path string, value any) error {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4<<20 {
		return fmt.Errorf("automatic migration state must be a private regular file")
	}
	d := json.NewDecoder(io.LimitReader(f, 4<<20))
	d.DisallowUnknownFields()
	if err = d.Decode(value); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("one automatic migration state object required")
	}
	return nil
}

func writePrivateJSON(path string, value any) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("automatic migration output must be a private regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".migration-json-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = inheritOwner(f, filepath.Dir(path)); err == nil {
		_, err = f.Write(append(b, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func inheritOwner(file *os.File, parent string) error {
	info, err := os.Stat(parent)
	if err != nil {
		return err
	}
	owner := info.Sys().(*syscall.Stat_t)
	return file.Chown(int(owner.Uid), int(owner.Gid))
}

func privateDirectories(path string) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("automatic migration directories must be real directories")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(path)
	if err := privateDirectories(parent); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0700); err != nil {
		return err
	}
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err = inheritOwner(dir, parent); err != nil {
		return err
	}
	return dir.Sync()
}

func archiveOwnership(root string) error {
	info, err := os.Stat(filepath.Dir(root))
	if err != nil {
		return err
	}
	owner := info.Sys().(*syscall.Stat_t)
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) && path == root {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("automatic migration archive symlink refused")
		}
		return os.Chown(path, int(owner.Uid), int(owner.Gid))
	})
}

func settingsIdentity(s settings, baseArchive string) (string, error) {
	s.Archive = baseArchive
	s.Source.Buckets = nil // Recorded separately after automatic discovery.
	s.Budget = migrate.Budget{}
	s.Exceptions = nil // Explicit loss decisions may change until copying starts.
	credentials := make([]string, 0, 4)
	for _, p := range []string{s.Source.AccessKeyFile, s.Source.SecretKeyFile, s.Target.AccessKeyFile, s.Target.SecretKeyFile} {
		value, err := readSecret(p)
		if err != nil {
			return "", err
		}
		hash := sha256.Sum256([]byte(value))
		credentials = append(credentials, hex.EncodeToString(hash[:]))
	}
	b, err := json.Marshal(struct {
		Settings    settings
		Credentials []string
	}{s, credentials})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:]), nil
}

func openAutomatic(s settings) (*automaticState, error) {
	dir := filepath.Dir(s.Journal)
	if err := privateDirectories(dir); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "automatic.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("automatic migration already running")
	}
	if err = inheritOwner(lock, dir); err != nil {
		lock.Close()
		return nil, err
	}
	state := &automaticState{path: filepath.Join(dir, "automatic.json"), lock: lock}
	fail := func(err error) (*automaticState, error) { state.close(); return nil, err }
	err = privateJSON(state.path, &state.automaticRecord)
	if errors.Is(err, os.ErrNotExist) {
		for _, path := range []string{s.Archive, s.Journal} {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				return fail(fmt.Errorf("automatic migration requires an unclaimed archive and journal; existing data retained"))
			}
		}
		identity, err := settingsIdentity(s, s.Archive)
		if err != nil {
			return fail(err)
		}
		state.automaticRecord = automaticRecord{Version: 1, Identity: identity, BaseArchive: s.Archive, CurrentArchive: s.Archive, Attempts: []string{s.Archive}, Stage: "prepared", Buckets: append([]string(nil), s.Source.Buckets...), Exceptions: s.Exceptions}
		sort.Strings(state.Buckets)
		if err = state.save(); err != nil {
			return fail(err)
		}
		return state, nil
	}
	if err != nil {
		return fail(err)
	}
	if state.Version != 1 || state.BaseArchive == "" || state.CurrentArchive == "" || len(state.Attempts) == 0 || !slices.Contains(state.Attempts, s.Archive) || state.Attempts[len(state.Attempts)-1] != state.CurrentArchive {
		return fail(fmt.Errorf("automatic migration archive identity differs; resume refused"))
	}
	identity, err := settingsIdentity(s, state.BaseArchive)
	if err != nil {
		return fail(err)
	}
	buckets := append([]string(nil), s.Source.Buckets...)
	sort.Strings(buckets)
	if identity != state.Identity || (len(buckets) > 0 && !reflect.DeepEqual(buckets, state.Buckets)) {
		return fail(fmt.Errorf("automatic migration settings or credentials differ; resume refused"))
	}
	if state.CopyStarted && !reflect.DeepEqual(s.Exceptions, state.Exceptions) {
		return fail(fmt.Errorf("automatic migration exceptions differ after copying began; resume refused"))
	}
	state.Exceptions = s.Exceptions
	return state, nil
}

func (s *automaticState) close()      { s.lock.Close() }
func (s *automaticState) save() error { return writePrivateJSON(s.path, s.automaticRecord) }

func (s *automaticState) archive(ctx context.Context) (string, error) {
	path := s.CurrentArchive
	if info, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if s.CopyStarted || (s.Stage != "prepared" && s.Stage != "archiving") {
			return "", fmt.Errorf("automatic archive disappeared after completion; resume refused")
		}
		return path, nil
	} else if err != nil {
		return "", err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("automatic archive must be a real directory")
	}
	if err := archiveOwnership(path); err != nil {
		return "", err
	}
	if _, err := os.Lstat(filepath.Join(path, "complete")); err == nil {
		if _, err := migrate.LoadArchive(ctx, path); err != nil {
			return "", fmt.Errorf("completed automatic archive failed verification; retained at %s: %w", path, err)
		}
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if s.CopyStarted || (s.Stage != "prepared" && s.Stage != "archiving") {
		return "", fmt.Errorf("automatic archive lost its completion marker; resume refused")
	}
	for number := len(s.Attempts) + 1; ; number++ {
		path = fmt.Sprintf("%s.attempt-%03d", s.BaseArchive, number)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			break
		} else if err != nil {
			return "", err
		}
	}
	s.CurrentArchive = path
	s.Attempts = append(s.Attempts, path)
	if err := s.save(); err != nil {
		return "", err
	}
	return path, nil
}

func runAutomatic(ctx context.Context, config string, s settings) (any, error) {
	if s.SnapshotID == "" || s.RollbackReference == "" || !s.RollbackVerified {
		return nil, fmt.Errorf("automatic migration requires a frozen snapshot and verified rollback reference")
	}
	state, err := openAutomatic(s)
	if err != nil {
		return nil, err
	}
	defer state.close()
	sourceConfig, err := endpointConfig(s.Source)
	if err != nil {
		return nil, err
	}
	if len(state.Buckets) == 0 {
		state.Buckets, err = migrate.DiscoverSourceBuckets(ctx, sourceConfig)
		if err != nil {
			return nil, err
		}
		if err = state.save(); err != nil {
			return nil, err
		}
	}
	s.Source.Buckets = append([]string(nil), state.Buckets...)
	s.Archive, err = state.archive(ctx)
	if err != nil {
		return nil, err
	}
	if err = rootsDisjoint(s); err != nil {
		return nil, err
	}
	if err = writePrivateJSON(config, s); err != nil {
		return nil, err
	}
	source, err := migrate.NewS3Source(sourceConfig, s.Source.Buckets)
	if err != nil {
		return nil, err
	}
	if _, err = os.Lstat(s.Archive); errors.Is(err, os.ErrNotExist) {
		if err = privateDirectories(filepath.Dir(s.Archive)); err != nil {
			return nil, err
		}
		state.Stage = "archiving"
		if err = state.save(); err != nil {
			return nil, err
		}
		reserve := s.ArchiveReserveBytes
		if reserve == 0 {
			reserve = 10000000000
		}
		_, scanErr := migrate.Scan(ctx, migrate.ScanConfig{Database: s.Database, Archive: s.Archive, SnapshotID: s.SnapshotID, RollbackReference: s.RollbackReference, RollbackVerified: true, ArchiveReserveBytes: reserve}, source)
		if err = errors.Join(scanErr, archiveOwnership(s.Archive)); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	m, err := migrate.LoadArchive(ctx, s.Archive)
	if err != nil {
		return nil, err
	}
	if err = migrate.CheckFrozen(ctx, m, source); err != nil {
		return nil, err
	}
	state.Stage = "archived"
	if err = state.save(); err != nil {
		return nil, err
	}
	if err = writePrivateJSON(filepath.Join(filepath.Dir(s.Journal), "source-issues.json"), m.Issues); err != nil {
		return nil, err
	}
	if err = migrate.ValidateExceptions(m, s.Exceptions); err != nil {
		return nil, fmt.Errorf("review source-issues.json and add explicit named Exceptions to the retained config: %w", err)
	}
	ffprobe := s.FFprobe
	if ffprobe == "" {
		ffprobe = "ffprobe"
	}
	probe, probeErr := migrate.Probe(ctx, s.Archive, ffprobe)
	if err = writePrivateJSON(filepath.Join(filepath.Dir(s.Journal), "probe.json"), probe); err != nil {
		return nil, err
	}
	if probeErr != nil && len(probe.Results) == 0 {
		return nil, probeErr
	}
	if err = migrate.ValidateProbe(m, probe, s.Exceptions); err != nil {
		return nil, err
	}
	if state.Budget == nil {
		db, err := store.Open(ctx, s.TargetMetadata)
		if err != nil {
			return nil, err
		}
		if err = db.Close(); err != nil {
			return nil, err
		}
		measured, err := migrate.MeasureMetadata(s.TargetMetadata)
		if err != nil {
			return nil, err
		}
		snapshot, err := os.Stat(filepath.Join(s.Archive, "snapshot.sqlite"))
		if err != nil {
			return nil, err
		}
		manifest, err := os.Stat(filepath.Join(s.Archive, "manifest.json"))
		if err != nil {
			return nil, err
		}
		evidence, err := migrate.AutomaticBudget(m, measured, snapshot.Size(), manifest.Size(), s.PartSize)
		if err != nil {
			return nil, err
		}
		state.Budget = &evidence
	}
	s.Budget = state.Budget.Budget
	if err = writePrivateJSON(config, s); err != nil {
		return nil, err
	}
	if err = writePrivateJSON(filepath.Join(filepath.Dir(s.Journal), "budget.json"), state.Budget); err != nil {
		return nil, err
	}
	state.CopyStarted, state.Stage = true, "copying"
	if err = state.save(); err != nil {
		return nil, err
	}
	if _, err = targetStage(ctx, "copy", s, source, m); err != nil {
		return nil, err
	}
	state.Stage = "verifying"
	if err = state.save(); err != nil {
		return nil, err
	}
	report, err := targetStage(ctx, "verify", s, source, m)
	if err != nil {
		return nil, err
	}
	if err = writePrivateJSON(filepath.Join(filepath.Dir(s.Journal), "automatic-result.json"), report); err != nil {
		return nil, err
	}
	state.Stage = "verified"
	if err = state.save(); err != nil {
		return nil, err
	}
	return report, nil
}
