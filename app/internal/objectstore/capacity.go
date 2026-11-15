package objectstore

import (
	"context"

	"github.com/mikd1/tag-files/app/internal/storageprotocol"
)

func (c *Client) Capacity(ctx context.Context) (storageprotocol.Capacity, error) {
	if c.storageID == "" || c.libraryID == "" || c.volumeID == "" {
		return storageprotocol.Capacity{}, storageprotocol.ErrBindingMismatch
	}
	capacity, err := c.control.Capacity(ctx, c.storageID, c.libraryID, c.volumeID)
	if err != nil {
		return capacity, err
	}
	if c.epoch != "" && capacity.Epoch != c.epoch {
		return storageprotocol.Capacity{}, &Error{Status: 409, Code: storageprotocol.EpochMismatchCode, Cause: ErrNoStorageEpoch}
	}
	return capacity, nil
}
