package storageprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/mikd1/tag-files/app/internal/diskspace"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
)

func pairingGate(p *Installation, credentials aws.Credentials, epoch string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == storageprotocol.ControlPath || r.URL.Path == storageprotocol.CapacityPath {
			serveControl(w, r, p, credentials, epoch)
			return
		}
		state, err := p.Snapshot()
		if err != nil {
			http.Error(w, "storage unavailable", 503)
			return
		}
		storage, library := r.Header.Values(storageprotocol.ExpectedStorageHeader), r.Header.Values(storageprotocol.ExpectedLibraryHeader)
		volume := r.Header.Values(storageprotocol.ExpectedVolumeHeader)
		if len(storage) > 0 || len(library) > 0 || len(volume) > 0 {
			if len(storage) != 1 || len(library) != 1 || len(volume) != 1 || strings.ToLower(storage[0]) != state.ID || strings.ToLower(library[0]) != state.LibraryID || state.LibraryID == "" || volume[0] != state.VolumeID {
				w.Header().Set(storageprotocol.EpochHeader, epoch)
				w.Header().Set(storageprotocol.StorageHeader, state.ID)
				w.Header().Set(storageprotocol.VolumeHeader, state.VolumeID)
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(409)
				_, _ = w.Write([]byte(`<Error><Code>` + storageprotocol.IdentityMismatchCode + `</Code><Message>The storage/library pair changed.</Message></Error>`))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func serveControl(w http.ResponseWriter, r *http.Request, p *Installation, credentials aws.Credentials, epoch string) {
	defer r.Body.Close()
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, storageprotocol.MaxControlBytes))
	if err != nil {
		http.Error(w, "control request too large", 413)
		return
	}
	if !storageprotocol.Authenticate(r, data, credentials.AccessKeyID, credentials.SecretAccessKey) {
		http.Error(w, "unauthorized", 401)
		return
	}
	reply := func(status int, body []byte) { storageprotocol.Reply(w, r, credentials.SecretAccessKey, status, body) }
	if r.URL.RawQuery != "" || r.URL.RawPath != "" {
		reply(400, []byte(`{"error":"invalid_request"}`))
		return
	}
	state, err := p.Snapshot()
	if err != nil || state.Phase != "ready" {
		reply(503, []byte(`{"error":"storage_unavailable"}`))
		return
	}
	if r.URL.Path == storageprotocol.CapacityPath {
		var expected storageprotocol.Binding
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if r.Method != "POST" || !json.Valid(data) || decoder.Decode(&expected) != nil || expected.Version != 1 {
			reply(400, []byte(`{"error":"invalid_request"}`))
			return
		}
		if expected.StorageID != state.ID || expected.LibraryID == "" || expected.LibraryID != state.LibraryID || expected.VolumeID != state.VolumeID {
			reply(409, []byte(`{"error":"storage_identity_mismatch"}`))
			return
		}
		capacity, err := diskspace.Measure(p.root)
		if err != nil || capacity.RootIdentity != state.VolumeID {
			reply(503, []byte(`{"error":"storage_unavailable"}`))
			return
		}
		body, _ := json.Marshal(storageprotocol.Capacity{Identity: storageprotocol.Identity{Version: 1, StorageID: state.ID, LibraryID: state.LibraryID, Epoch: epoch, VolumeID: state.VolumeID}, Snapshot: capacity})
		reply(200, body)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if len(data) != 0 {
			reply(400, []byte(`{"error":"invalid_request"}`))
			return
		}
	case http.MethodPost:
		var binding storageprotocol.Binding
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if !json.Valid(data) || decoder.Decode(&binding) != nil || binding.Version != 1 {
			reply(400, []byte(`{"error":"invalid_request"}`))
			return
		}
		if binding.VolumeID != state.VolumeID {
			reply(409, []byte(`{"error":"storage_identity_mismatch"}`))
			return
		}
		state, err = p.BindLibrary(binding.StorageID, binding.LibraryID)
		if errors.Is(err, ErrStorageBindingMismatch) {
			reply(409, []byte(`{"error":"storage_identity_mismatch"}`))
			return
		}
		if err != nil {
			reply(503, []byte(`{"error":"storage_unavailable"}`))
			return
		}
	default:
		reply(405, []byte(`{"error":"method_not_allowed"}`))
		return
	}
	body, _ := json.Marshal(storageprotocol.Identity{Version: 1, StorageID: state.ID, LibraryID: state.LibraryID, Epoch: epoch, VolumeID: state.VolumeID})
	reply(200, body)
}
