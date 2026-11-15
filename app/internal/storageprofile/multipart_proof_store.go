package storageprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const proofDirectory = ".tag-files-mpu-proofs"
const proofRecordLimit = 16 << 10

type proofTuple struct{ Bucket, Key, UploadID string }
type proofUpload struct {
	Version              int        `json:"version"`
	StorageID            string     `json:"storageId"`
	Tuple                proofTuple `json:"tuple"`
	Epoch                string     `json:"epoch"`
	Closed               bool       `json:"closed"`
	TerminalAttemptEpoch string     `json:"terminalAttemptEpoch,omitempty"`
	TerminalUncertain    bool       `json:"terminalUncertain"`
}
type proofPart struct {
	Version        int    `json:"version"`
	Number         int    `json:"number"`
	Size           int64  `json:"size"`
	SHA256         string `json:"sha256"`
	ETag           string `json:"etag,omitempty"`
	Validated      bool   `json:"validated"`
	AttemptEpoch   string `json:"attemptEpoch,omitempty"`
	Uncertain      bool   `json:"uncertain"`
	UploadIdentity string `json:"uploadIdentity"`
}
type proofStore struct {
	root, storageID string
	fail            func(string) error
	directorySync   func(string) error
}

func syncProofDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func (s *proofStore) proofDir(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err = os.Mkdir(path, 0700); err != nil {
			return err
		}
		return s.durableDir(filepath.Dir(path))
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("multipart proof path must be an owned directory")
	}
	// A previous mkdir may have become visible even when synchronizing its
	// parent failed. Existence alone does not establish durable reachability.
	return s.durableDir(filepath.Dir(path))
}
func openProofStore(root, storageID string) (*proofStore, error) {
	s := &proofStore{root: filepath.Join(root, proofDirectory), storageID: storageID}
	if err := s.proofDir(s.root); err != nil {
		return nil, err
	}
	if err := cleanProofTemps(s.root); err != nil {
		return nil, err
	}
	marker := struct {
		Version   int    `json:"version"`
		StorageID string `json:"storageId"`
	}{1, storageID}
	path := filepath.Join(s.root, "format.json")
	var got struct {
		Version   int    `json:"version"`
		StorageID string `json:"storageId"`
	}
	err := s.read(path, &got)
	if errors.Is(err, os.ErrNotExist) {
		entries, readErr := boundedProofEntries(s.root, 0)
		if readErr != nil || len(entries) != 0 {
			return nil, fmt.Errorf("multipart proof format missing from nonempty journal")
		}
		if err = s.write(path, marker); err != nil {
			return nil, err
		}
	} else if err != nil || got.Version != marker.Version || got.StorageID != storageID {
		return nil, fmt.Errorf("invalid/incompatible multipart proof format: %v", err)
	}
	if err = s.durableDir(s.root); err != nil {
		return nil, err
	}
	return s, nil
}

func boundedProofEntries(path string, limit int) ([]os.DirEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(limit + 1)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(entries) > limit {
		return nil, fmt.Errorf("multipart proof directory exceeds record bound")
	}
	return entries, nil
}

// Interrupted atomic writes have never authorized a subsequent network call.
// Discard only our bounded regular temporary files under the locked private
// journal, leaving canonical binding/proof records authoritative.
func cleanProofTemps(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	changed := false
	for {
		entries, e := f.ReadDir(128)
		if e != nil && e != io.EOF {
			return e
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), stateTempPrefix) {
				continue
			}
			info, err := os.Lstat(filepath.Join(path, entry.Name()))
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Size() > proofRecordLimit {
				return fmt.Errorf("invalid multipart proof temporary file")
			}
			if err = os.Remove(filepath.Join(path, entry.Name())); err != nil {
				return err
			}
			changed = true
		}
		if e == io.EOF {
			break
		}
	}
	if changed {
		return syncProofDir(path)
	}
	return nil
}
func (s *proofStore) uploadPath(t proofTuple) string {
	b, _ := json.Marshal([]string{s.storageID, t.Bucket, t.Key, t.UploadID})
	return filepath.Join(s.root, digest(b))
}
func (s *proofStore) upload(t proofTuple) (proofUpload, error) {
	path := s.uploadPath(t)
	info, err := os.Lstat(path)
	if err != nil {
		return proofUpload{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return proofUpload{}, fmt.Errorf("invalid multipart proof directory")
	}
	var u proofUpload
	if err := s.read(filepath.Join(path, "upload.json"), &u); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			entries, e := boundedProofEntries(path, 0)
			if e != nil || len(entries) != 0 {
				return u, fmt.Errorf("multipart upload identity missing from nonempty journal")
			}
		}
		return u, err
	}
	if u.Version != 1 || u.StorageID != s.storageID || u.Tuple != t || u.Epoch == "" {
		return u, fmt.Errorf("multipart upload proof identity differs")
	}
	return u, nil
}
func (s *proofStore) saveUpload(u proofUpload) error {
	path := s.uploadPath(u.Tuple)
	if err := s.durableUploadDirectory(u.Tuple); err != nil {
		return err
	}
	return s.write(filepath.Join(path, "upload.json"), u)
}
func (s *proofStore) part(t proofTuple, n int) (proofPart, error) {
	if _, err := s.upload(t); err != nil {
		return proofPart{}, err
	}
	var p proofPart
	if err := s.read(filepath.Join(s.uploadPath(t), strconv.Itoa(n)+".json"), &p); err != nil {
		return p, err
	}
	if p.Version != 1 || p.UploadIdentity != filepath.Base(s.uploadPath(t)) || p.Number != n || p.Size < 1 || p.Size > 5<<30 || !canonicalPartSHA(p.SHA256) || (p.Validated && p.ETag == "") || strings.ContainsAny(p.ETag, "\x00\r\n") {
		return p, fmt.Errorf("invalid multipart part proof")
	}
	return p, nil
}
func (s *proofStore) savePart(t proofTuple, p proofPart) error {
	identity := filepath.Base(s.uploadPath(t))
	if p.UploadIdentity != "" && p.UploadIdentity != identity {
		return fmt.Errorf("multipart part belongs to another upload")
	}
	p.UploadIdentity = identity
	if err := s.durableUploadDirectory(t); err != nil {
		return err
	}
	return s.write(filepath.Join(s.uploadPath(t), strconv.Itoa(p.Number)+".json"), p)
}

func (s *proofStore) durableUploadDirectory(t proofTuple) error {
	// Re-establish the complete journal entry chain before network dispatch,
	// positive acknowledgement, or publishing a ListParts checksum.
	if err := s.proofDir(s.root); err != nil {
		return err
	}
	path := s.uploadPath(t)
	if err := s.proofDir(path); err != nil {
		return err
	}
	return s.durableDir(path)
}

func (s *proofStore) resyncPart(t proofTuple, n int) error {
	if s.fail != nil {
		if err := s.fail("before-proof-file-sync"); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(filepath.Join(s.uploadPath(t), strconv.Itoa(n)+".json"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > proofRecordLimit {
		return fmt.Errorf("invalid multipart proof file")
	}
	return f.Sync()
}
func (s *proofStore) read(path string, value any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > proofRecordLimit {
		return fmt.Errorf("invalid multipart proof file")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, proofRecordLimit+1))
	if err != nil || len(b) > proofRecordLimit {
		return fmt.Errorf("invalid multipart proof bytes: %v", err)
	}
	var envelope struct {
		Record json.RawMessage `json:"record"`
		SHA256 string          `json:"sha256"`
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if !json.Valid(b) || d.Decode(&envelope) != nil || digest(envelope.Record) != envelope.SHA256 {
		return fmt.Errorf("corrupt multipart proof checksum")
	}
	d = json.NewDecoder(bytes.NewReader(envelope.Record))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil {
		return fmt.Errorf("invalid multipart proof schema")
	}
	return nil
}
func (s *proofStore) write(path string, value any) error {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("refusing nonregular multipart proof")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if s.fail != nil {
		if err := s.fail("before-write"); err != nil {
			return err
		}
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	envelope, err := json.Marshal(struct {
		Record json.RawMessage `json:"record"`
		SHA256 string          `json:"sha256"`
	}{b, digest(b)})
	if err != nil || len(envelope) > proofRecordLimit {
		return fmt.Errorf("multipart proof exceeds metadata bound")
	}
	f, err := os.CreateTemp(filepath.Dir(path), stateTempPrefix)
	if err != nil {
		return err
	}
	defer func() { f.Close(); os.Remove(f.Name()) }()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(envelope); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if s.fail != nil {
		if err = s.fail("after-file-sync"); err != nil {
			return err
		}
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	if s.fail != nil {
		if err = s.fail("after-rename"); err != nil {
			return err
		}
	}
	if err = s.durableDir(filepath.Dir(path)); err != nil {
		return err
	}
	if s.fail != nil {
		return s.fail("after-durable-write")
	}
	return nil
}

func (s *proofStore) durableDir(path string) error {
	if s.fail != nil {
		if err := s.fail("before-directory-sync"); err != nil {
			return err
		}
	}
	if s.directorySync != nil {
		return s.directorySync(path)
	}
	return syncProofDir(path)
}
