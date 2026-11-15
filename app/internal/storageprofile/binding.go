package storageprofile

import (
	"errors"
	"os"
	"strings"

	"github.com/mikd1/tag-files/app/internal/domain"
)

var ErrStorageBindingMismatch = errors.New("storage volume belongs to a different library or identity")
var ErrStorageVolumeChanged = errors.New("storage marker belongs to a different physical volume root; use the external cold restore workflow")

func (p *Installation) Snapshot() (StorageState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return StorageState{}, os.ErrClosed
	}
	return p.State, nil
}

// BindLibrary runs under the complete-volume ownership lock. The storage side
// is committed first; an app whose SQLite commit/reply was lost can repeat only
// the same pair. No endpoint can clear or replace this binding.
func (p *Installation) BindLibrary(storageID, libraryID string) (StorageState, error) {
	if domain.ValidateUUID(storageID) != nil || domain.ValidateUUID(libraryID) != nil {
		return StorageState{}, ErrStorageBindingMismatch
	}
	storageID, libraryID = strings.ToLower(storageID), strings.ToLower(libraryID)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return StorageState{}, os.ErrClosed
	}
	// Reload even after a prior atomic rename returned a directory-sync error;
	// a persisted first winner must never be replaced using stale memory.
	state, err := LoadState(p.root)
	if err != nil {
		return StorageState{}, err
	}
	if state.FormatVersion != StorageFormatVersion || state.ID != p.State.ID || state.Engine != p.State.Engine || state.AppCredentialSHA256 != p.State.AppCredentialSHA256 || state.ConfigSHA256 != p.State.ConfigSHA256 || state.Phase != "ready" || (p.State.LibraryID != "" && state.LibraryID != p.State.LibraryID) {
		return StorageState{}, ErrIncompatibleStorage
	}
	if state.ID != storageID || (state.LibraryID != "" && state.LibraryID != libraryID) {
		return StorageState{}, ErrStorageBindingMismatch
	}
	if state.LibraryID == "" {
		state.LibraryID = libraryID
		if err := writeState(p.root, state); err != nil {
			return StorageState{}, err
		}
	} else {
		directory, err := os.Open(p.root)
		if err != nil {
			return StorageState{}, err
		}
		err = directory.Sync()
		closeErr := directory.Close()
		if err != nil || closeErr != nil {
			return StorageState{}, errors.Join(err, closeErr)
		}
	}
	p.State = state
	return state, nil
}
