package storageprofile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/mikd1/tag-files/app/internal/domain"
)

const StateFilename = ".tag-files-storage.json"
const lockFilename = ".tag-files-storage.lock"
const stateTempPrefix = ".tag-files-storage-tmp-"
const StorageEngine = "seaweedfs/4.53/leveldb2"
const StorageFormatVersion = 2

var (
	ErrStorageInUse        = errors.New("storage volume is already owned by another process")
	ErrUnownedStorage      = errors.New("refusing to initialize nonempty unowned storage")
	ErrIncompatibleStorage = errors.New("incompatible or invalid storage ownership state")
	ErrCredentialMismatch  = errors.New("supplied credentials differ from the owned storage profile")
)

type StorageState struct {
	FormatVersion       int    `json:"formatVersion"`
	Engine              string `json:"engine"`
	ID                  string `json:"id"`
	Phase               string `json:"phase"`
	AppCredentialSHA256 string `json:"appCredentialSha256"`
	ConfigSHA256        string `json:"configSha256"`
	LibraryID           string `json:"libraryId,omitempty"`
	VolumeID            string `json:"volumeId,omitempty"`
}

type Installation struct {
	State    StorageState
	root     string
	lock     *os.File
	mu       sync.Mutex
	closed   bool
	closeErr error
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func credentialDigest(c aws.Credentials) string {
	data, _ := json.Marshal([]string{c.AccessKeyID, c.SecretAccessKey})
	return digest(data)
}
func validDigest(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == sha256.Size
}

func validateState(state StorageState) error {
	if (state.FormatVersion != 1 && state.FormatVersion != StorageFormatVersion) || (state.FormatVersion == 1 && (state.LibraryID != "" || state.VolumeID != "")) || (state.FormatVersion == StorageFormatVersion && !validDigest(state.VolumeID)) || (state.LibraryID != "" && (domain.ValidateUUID(state.LibraryID) != nil || state.Phase != "ready")) || state.Engine != StorageEngine || domain.ValidateUUID(state.ID) != nil || (state.Phase != "initializing" && state.Phase != "ready") || !validDigest(state.AppCredentialSHA256) || (state.ConfigSHA256 != "" && !validDigest(state.ConfigSHA256)) || (state.Phase == "ready" && state.ConfigSHA256 == "") {
		return ErrIncompatibleStorage
	}
	return nil
}

func LoadState(root string) (StorageState, error) {
	path := filepath.Join(root, StateFilename)
	info, err := os.Lstat(path)
	if err != nil {
		return StorageState{}, err
	}
	if !info.Mode().IsRegular() {
		return StorageState{}, ErrIncompatibleStorage
	}
	data, err := readConfig(path)
	if err != nil {
		return StorageState{}, err
	}
	var state StorageState
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if !json.Valid(data) || decoder.Decode(&state) != nil {
		return state, ErrIncompatibleStorage
	}
	if err := validateState(state); err != nil {
		return state, err
	}
	if state.FormatVersion == StorageFormatVersion {
		volume, err := VolumeIdentity(root)
		if err != nil {
			return state, err
		}
		if state.VolumeID != volume {
			return state, ErrStorageVolumeChanged
		}
	}
	return state, nil
}

func emptyUnownedRoot(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Dir(path) == root && (d.Name() == lockFilename || strings.HasPrefix(d.Name(), stateTempPrefix)) && d.Type().IsRegular() {
			return nil
		}
		return ErrUnownedStorage
	})
}

func initialState(root string) (StorageState, bool, error) {
	state, err := LoadState(root)
	if err == nil {
		return state, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return state, false, err
	}
	if err := emptyUnownedRoot(root); err != nil {
		return state, false, err
	}
	return StorageState{}, true, nil
}

// Prepare acquires the complete storage volume and persists a resumable intent
// before any backend process or S3 request. It never adopts arbitrary existing
// bytes or replaces credentials/configuration belonging to a ready installation.
func Prepare(root string, app aws.Credentials, uid, gid int) (_ *Installation, err error) {
	if len(app.AccessKeyID) < 20 || len(app.AccessKeyID) > 256 || len(app.SecretAccessKey) < 20 || len(app.SecretAccessKey) > 256 || app.SessionToken != "" || strings.ContainsAny(app.AccessKeyID+app.SecretAccessKey, "\x00\r\n") {
		return nil, fmt.Errorf("explicit private static credentials of 20..256 bytes are required")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	// Reject unknown state/data before even creating an ownership lock file.
	if _, _, err := initialState(root); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(root, lockFilename), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	info, err := lock.Stat()
	if err != nil || !info.Mode().IsRegular() {
		lock.Close()
		return nil, fmt.Errorf("storage ownership lock must be a regular file")
	}
	p := &Installation{root: root, lock: lock}
	defer func() {
		if err != nil {
			p.Close()
		}
	}()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrStorageInUse
		}
		return nil, err
	}
	state, fresh, err := initialState(root)
	if err != nil {
		return nil, err
	}
	appHash := credentialDigest(app)
	if fresh {
		volume, err := VolumeIdentity(root)
		if err != nil {
			return nil, err
		}
		state = StorageState{FormatVersion: StorageFormatVersion, Engine: StorageEngine, ID: domain.NewUUID(), Phase: "initializing", AppCredentialSHA256: appHash, VolumeID: volume}
		if err := writeState(root, state); err != nil {
			return nil, err
		}
	} else if state.AppCredentialSHA256 != appHash {
		return nil, ErrCredentialMismatch
	}
	for _, sub := range []string{"", "master", "filerldb2", "config"} {
		path := filepath.Join(root, sub)
		if err := os.MkdirAll(path, 0700); err != nil {
			return nil, err
		}
		if err := contained(root, path); err != nil {
			return nil, err
		}
		if err := os.Chmod(path, 0700); err != nil {
			return nil, err
		}
		if err := os.Chown(path, uid, gid); err != nil {
			return nil, err
		}
	}
	path := filepath.Join(root, "config", "s3.json")
	config, readErr := readConfig(path)
	if errors.Is(readErr, os.ErrNotExist) && state.Phase == "initializing" && state.ConfigSHA256 == "" {
		credentials := identityConfig{Identities: []identity{
			{Name: "storage-bootstrap", Credentials: []identityCredential{{AccessKey: domain.NewUUID(), SecretKey: domain.NewUUID() + domain.NewUUID()}}, Actions: []string{"Admin", "Read", "Write", "List", "Tagging"}},
			{Name: "app", Credentials: []identityCredential{{AccessKey: app.AccessKeyID, SecretKey: app.SecretAccessKey}}, Actions: []string{"Read:tag-files", "Write:tag-files", "List:tag-files"}},
		}}
		config, err = json.MarshalIndent(credentials, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := atomicFile(path, config, uid, gid); err != nil {
			return nil, err
		}
	} else if readErr != nil {
		return nil, readErr
	}
	if err := contained(root, path); err != nil {
		return nil, err
	}
	if _, err := PrivateCredentials(config); err != nil {
		return nil, err
	}
	var identities identityConfig
	if err := json.Unmarshal(config, &identities); err != nil {
		return nil, err
	}
	if len(identities.Identities) != 2 || identities.Identities[0].Name != "storage-bootstrap" || identities.Identities[1].Name != "app" || len(identities.Identities[1].Credentials) != 1 {
		return nil, ErrIncompatibleStorage
	}
	stored := identities.Identities[1].Credentials[0]
	if stored.AccessKey != app.AccessKeyID || stored.SecretKey != app.SecretAccessKey {
		return nil, ErrCredentialMismatch
	}
	hash := digest(config)
	if state.ConfigSHA256 != "" && state.ConfigSHA256 != hash {
		return nil, ErrIncompatibleStorage
	}
	if state.ConfigSHA256 == "" {
		state.ConfigSHA256 = hash
		if err := writeState(root, state); err != nil {
			return nil, err
		}
	}
	if state.FormatVersion == 1 {
		state.FormatVersion = StorageFormatVersion
		state.VolumeID, err = VolumeIdentity(root)
		if err != nil {
			return nil, err
		}
		if err := writeState(root, state); err != nil {
			return nil, err
		}
	}
	p.State = state
	return p, nil
}

func contained(root, path string) error {
	actual, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, actual)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("owned storage path escapes the complete data volume")
	}
	return nil
}

func atomicFile(path string, data []byte, uid, gid int) (err error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, stateTempPrefix)
	if err != nil {
		return err
	}
	defer func() { f.Close(); os.Remove(f.Name()) }()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if uid >= 0 {
		if err := f.Chown(uid, gid); err != nil {
			return err
		}
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	parent, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}

func writeState(root string, state StorageState) error {
	if err := validateState(state); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return atomicFile(filepath.Join(root, StateFilename), data, -1, -1)
}

// RecordReady is called only after the running backend has passed Check. The
// image's health command additionally requires this durable publication marker.
func (p *Installation) RecordReady() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return os.ErrClosed
	}
	config, err := readConfig(filepath.Join(p.root, "config", "s3.json"))
	if err != nil {
		return err
	}
	if digest(config) != p.State.ConfigSHA256 {
		return ErrIncompatibleStorage
	}
	next := p.State
	next.Phase = "ready"
	if err := writeState(p.root, next); err != nil {
		return err
	}
	p.State = next
	return nil
}

func (p *Installation) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		p.closeErr = errors.Join(syscall.Flock(int(p.lock.Fd()), syscall.LOCK_UN), p.lock.Close())
	}
	return p.closeErr
}
