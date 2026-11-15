// Package resourceerr classifies real exhaustion at runtime adapter boundaries.
package resourceerr

import (
	"errors"
	"syscall"

	"github.com/mattn/go-sqlite3"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/scratch"
)

func Insufficient(err error) bool {
	if errors.Is(err, scratch.ErrInsufficientStorage) || errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) {
		return true
	}
	var db sqlite3.Error
	if errors.As(err, &db) && db.Code == sqlite3.ErrFull {
		return true
	}
	var s3 *objectstore.Error
	return errors.As(err, &s3) && (s3.Status == 507 || s3.Code == "InsufficientStorage" || s3.Code == "XMinioStorageFull")
}
