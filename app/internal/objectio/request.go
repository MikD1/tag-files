// Package objectio journals external writes independently of their domain
// parents. It never owns upload/media state transitions or terminal history.
package objectio

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"strings"
	"unicode/utf8"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

type Operation string

const (
	CreateMPU   Operation = "create_mpu"
	UploadPart  Operation = "upload_part"
	CompleteMPU Operation = "complete_mpu"
	PutObject   Operation = "put_object"
)

type Arguments struct {
	Size           int64  `json:"size"`
	MultipartID    string `json:"multipartId,omitempty"`
	PartNumber     int    `json:"partNumber,omitempty"`
	SHA256         string `json:"sha256,omitempty"`
	ManifestSHA256 string `json:"manifestSha256,omitempty"`
	ContentType    string `json:"contentType,omitempty"`
}
type Request struct {
	TargetKind, TargetID string
	Generation           int64
	Operation            Operation
	Key                  string
	Arguments            Arguments
}
type Intent struct {
	Request
	ID, OwnerID, StorageEpoch, State, CreatedAtUTC string
}

var ErrIncompatibleIntent = errors.New("unsupported or invalid external write intent")

func hexSHA(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value
}
func base64SHA(value string) bool {
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)
	return err == nil && len(decoded) == 32 && base64.StdEncoding.EncodeToString(decoded) == value
}
func opaque(value string) bool {
	return utf8.ValidString(value) && len(value) > 0 && len(value) <= 4096 && !strings.ContainsAny(value, "\x00\r\n")
}
func contentType(value string) bool {
	_, _, err := mime.ParseMediaType(value)
	return err == nil && len(value) <= 256 && !strings.ContainsAny(value, "\x00\r\n")
}
func keyValid(value string) bool {
	if len(value) < 1 || len(value) > 1024 || strings.Contains(value, "\\") {
		return false
	}
	for _, r := range value {
		if r < 33 || r > 126 {
			return false
		}
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}
func validate(request Request) error {
	if domain.ValidateUUID(request.TargetID) != nil || request.Generation < 1 || request.Generation > domain.MaxSafeInteger || !keyValid(request.Key) || request.Arguments.Size < 1 || request.Arguments.Size > domain.MaxSafeInteger {
		return domain.Invalid("writeIntent", "Invalid external write identity or size")
	}
	a := request.Arguments
	valid := false
	switch request.Operation {
	case CreateMPU:
		valid = request.TargetKind == "upload" && a.MultipartID == "" && a.PartNumber == 0 && a.SHA256 == "" && a.ManifestSHA256 == "" && contentType(a.ContentType)
	case UploadPart:
		valid = request.TargetKind == "upload" && opaque(a.MultipartID) && a.PartNumber >= 1 && a.PartNumber <= 10000 && a.Size <= 5*1024*1024*1024 && base64SHA(a.SHA256) && a.ManifestSHA256 == "" && a.ContentType == ""
	case CompleteMPU:
		valid = request.TargetKind == "upload" && opaque(a.MultipartID) && a.PartNumber == 0 && a.SHA256 == "" && hexSHA(a.ManifestSHA256) && a.ContentType == ""
	case PutObject:
		valid = request.TargetKind == "asset" && a.MultipartID == "" && a.PartNumber == 0 && hexSHA(a.SHA256) && a.ManifestSHA256 == "" && contentType(a.ContentType)
	}
	if !valid {
		return domain.Invalid("writeIntent", "Invalid external write operation arguments")
	}
	return nil
}
func decode(row dbgen.ObjectWriteIntent) (Intent, error) {
	bad := func() (Intent, error) { return Intent{}, fmt.Errorf("%w: %s", ErrIncompatibleIntent, row.ID) }
	if row.PayloadVersion != 1 || len(row.PayloadJson) > 65536 || !json.Valid([]byte(row.PayloadJson)) || domain.ValidateUUID(row.ID) != nil || domain.ValidateUUID(row.OwnerID) != nil || domain.ValidateUUID(row.StorageEpoch) != nil {
		return bad()
	}
	var args Arguments
	decoder := json.NewDecoder(bytes.NewBufferString(row.PayloadJson))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&args) != nil {
		return bad()
	}
	request := Request{TargetKind: row.TargetKind, TargetID: row.TargetID, Generation: row.Generation, Operation: Operation(row.Operation), Key: row.ObjectKey, Arguments: args}
	if validate(request) != nil {
		return bad()
	}
	return Intent{Request: request, ID: row.ID, OwnerID: row.OwnerID, StorageEpoch: row.StorageEpoch, State: row.State, CreatedAtUTC: row.CreatedAtUtc}, nil
}
