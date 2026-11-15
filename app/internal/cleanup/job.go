// Package cleanup owns the identity of independent cleanup work. Resource
// modules own their versioned receipt payloads and permanent deletion fences.
package cleanup

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

var ErrIncompatibleJob = errors.New("unsupported or invalid cleanup payload")

type Payload struct {
	Version   int    `json:"version"`
	ReceiptID string `json:"receiptId"`
}

func DecodeJob(job dbgen.Job) (string, error) {
	var payload Payload
	decoder := json.NewDecoder(strings.NewReader(job.PayloadJson))
	decoder.DisallowUnknownFields()
	if job.Type != domain.JobCleanup || decoder.Decode(&payload) != nil || !json.Valid([]byte(job.PayloadJson)) || payload.Version != 1 || domain.ValidateUUID(payload.ReceiptID) != nil || job.SubjectID != payload.ReceiptID || job.DedupeKey != "cleanup/"+payload.ReceiptID {
		return "", ErrIncompatibleJob
	}
	return payload.ReceiptID, nil
}
