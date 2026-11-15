package storageprotocol

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mikd1/tag-files/app/internal/diskspace"
	"github.com/mikd1/tag-files/app/internal/domain"
)

const CapacityPath = "/_tag-files/v1/capacity"

type Capacity struct {
	Identity
	diskspace.Snapshot
}

func (c *Client) Capacity(ctx context.Context, storageID, libraryID, volumeID string) (Capacity, error) {
	if domain.ValidateUUID(storageID) != nil || domain.ValidateUUID(libraryID) != nil || !ValidVolumeID(volumeID) {
		return Capacity{}, ErrProtocol
	}
	request := Binding{Version: 1, StorageID: strings.ToLower(storageID), LibraryID: strings.ToLower(libraryID), VolumeID: volumeID}
	body, err := json.Marshal(request)
	if err != nil {
		return Capacity{}, err
	}
	data, err := c.exchange(ctx, http.MethodPost, CapacityPath, body)
	if err != nil {
		return Capacity{}, err
	}
	var result Capacity
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if !json.Valid(data) || decoder.Decode(&result) != nil || result.Version != 1 || domain.ValidateUUID(result.Epoch) != nil || !diskspace.Valid(result.Snapshot) {
		return Capacity{}, ErrProtocol
	}
	if result.StorageID != request.StorageID || result.LibraryID != request.LibraryID || result.VolumeID != request.VolumeID {
		return Capacity{}, ErrBindingMismatch
	}
	return result, nil
}
