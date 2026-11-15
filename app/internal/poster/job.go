// Package poster owns versioned preview work independently of upload sessions.
package poster

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

const Profile = "poster-v1"

var ErrIncompatibleJob = errors.New("unsupported or invalid poster job payload")

type Payload struct {
	Version        int    `json:"version"`
	ItemID         int64  `json:"itemId"`
	PrimaryAssetID string `json:"primaryAssetId"`
	Profile        string `json:"profile"`
}

func DecodeJob(job dbgen.Job) (Payload, error) {
	var payload Payload
	decoder := json.NewDecoder(strings.NewReader(job.PayloadJson))
	decoder.DisallowUnknownFields()
	if job.Type != domain.JobPoster || !json.Valid([]byte(job.PayloadJson)) || decoder.Decode(&payload) != nil || payload.Version != 1 || payload.Profile != Profile || domain.ValidateID(payload.ItemID) != nil || domain.ValidateUUID(payload.PrimaryAssetID) != nil || job.SubjectID != strconv.FormatInt(payload.ItemID, 10) || job.DedupeKey != "poster/"+payload.PrimaryAssetID+"/"+Profile {
		return payload, ErrIncompatibleJob
	}
	return payload, nil
}
