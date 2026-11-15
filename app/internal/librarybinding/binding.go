// Package librarybinding establishes one durable SQLite/storage pair before
// worker/admission/reconciliation. It performs no media/object mutations.
package librarybinding

import (
	"context"
	"database/sql"

	"github.com/mikd1/tag-files/app/internal/storageprotocol"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

type Storage interface {
	Identity(context.Context) (storageprotocol.Identity, error)
	Bind(context.Context, string, string, string) (storageprotocol.Identity, error)
}
type Pair struct{ StorageID, LibraryID, VolumeID string }

func Ensure(ctx context.Context, db *store.Store, remote Storage) (Pair, error) {
	meta, err := dbgen.New(db.Reader()).GetLibraryMeta(ctx)
	if err != nil {
		return Pair{}, err
	}
	identity, err := remote.Identity(ctx)
	if err != nil {
		return Pair{}, err
	}
	if meta.StorageID.Valid {
		if meta.StorageID.String != identity.StorageID || meta.LibraryID != identity.LibraryID || !meta.StorageVolumeID.Valid || meta.StorageVolumeID.String != identity.VolumeID {
			return Pair{}, storageprotocol.ErrBindingMismatch
		}
		return Pair{StorageID: meta.StorageID.String, LibraryID: meta.LibraryID, VolumeID: meta.StorageVolumeID.String}, nil
	}
	if identity.LibraryID != "" && identity.LibraryID != meta.LibraryID {
		return Pair{}, storageprotocol.ErrBindingMismatch
	}
	// Always repeat a storage-side bind when SQLite is unbound, including a
	// lost reply/SQLite-commit retry. The storage side confirms durable sync.
	bound, err := remote.Bind(ctx, identity.StorageID, meta.LibraryID, identity.VolumeID)
	if err != nil {
		return Pair{}, err
	}
	if bound.StorageID != identity.StorageID || bound.LibraryID != meta.LibraryID || bound.VolumeID != identity.VolumeID {
		return Pair{}, storageprotocol.ErrBindingMismatch
	}
	err = db.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		if _, err := q.BindLibraryStorage(ctx, dbgen.BindLibraryStorageParams{StorageID: sql.NullString{String: bound.StorageID, Valid: true}, StorageVolumeID: sql.NullString{String: bound.VolumeID, Valid: true}, LibraryID: meta.LibraryID}); err != nil {
			return err
		}
		current, err := q.GetLibraryMeta(ctx)
		if err != nil {
			return err
		}
		if current.LibraryID != meta.LibraryID || !current.StorageID.Valid || current.StorageID.String != bound.StorageID || !current.StorageVolumeID.Valid || current.StorageVolumeID.String != bound.VolumeID {
			return storageprotocol.ErrBindingMismatch
		}
		return nil
	})
	if err != nil {
		return Pair{}, err
	}
	return Pair{StorageID: bound.StorageID, LibraryID: meta.LibraryID, VolumeID: bound.VolumeID}, nil
}
