package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

// Link belongs to the same transaction as Execute's receipt and its domain
// intent. The deferred foreign key allows the callback to run first.
func Link(ctx context.Context, tx *sql.Tx, operationID, kind, targetID string) error {
	if err := domain.ValidateUUID(operationID); err != nil {
		return err
	}
	if err := domain.ValidateUUID(targetID); err != nil {
		return err
	}
	if kind != "upload_complete" && kind != "job" {
		return domain.Invalid("target", "Invalid operation target")
	}
	return dbgen.New(tx).AddOperationTarget(ctx, dbgen.AddOperationTargetParams{OperationID: strings.ToLower(operationID), TargetKind: kind, TargetID: strings.ToLower(targetID)})
}

func resolveUploads(ctx context.Context, tx *sql.Tx, uploadID, state string, result func(string) (Result, error)) error {
	q := dbgen.New(tx)
	after := ""
	for {
		rows, err := q.PendingTargetOperations(ctx, dbgen.PendingTargetOperationsParams{TargetKind: "upload_complete", TargetID: uploadID, AfterID: after})
		if err != nil {
			return err
		}
		for _, row := range rows {
			outcome, err := result(row.OperationID)
			if err != nil {
				return err
			}
			raw, err := json.Marshal(outcome)
			if err != nil {
				return err
			}
			if _, err = q.ResolveTargetOperation(ctx, dbgen.ResolveTargetOperationParams{State: state, HttpStatus: int64(outcome.Status), ResultJson: string(raw), OperationID: row.OperationID}); err != nil {
				return err
			}
			after = row.OperationID
		}
		if len(rows) < 100 {
			return nil
		}
	}
}

// ResolveUpload materializes the final result before publication commits. It
// survives later removal of the item and never recreates a session on replay.
func ResolveUpload(ctx context.Context, tx *sql.Tx, uploadID string, itemID int64) error {
	if err := domain.ValidateID(itemID); err != nil {
		return err
	}
	return resolveUploads(ctx, tx, uploadID, "completed", func(id string) (Result, error) {
		body, err := json.Marshal(struct {
			OperationID string `json:"operationId"`
			UploadID    string `json:"uploadId"`
			State       string `json:"state"`
			ItemID      int64  `json:"itemId"`
		}{id, uploadID, "ready", itemID})
		return Result{Status: 200, Body: body}, err
	})
}

// FailUpload ends pending receipts only. A later manual retry gets a new
// operation; previous failed commands keep their original outcome.
func FailUpload(ctx context.Context, tx *sql.Tx, uploadID string, status int, code string) error {
	return resolveUploads(ctx, tx, uploadID, "failed", func(id string) (Result, error) {
		body, err := json.Marshal(struct {
			Type    string `json:"type"`
			Title   string `json:"title"`
			Status  int    `json:"status"`
			Code    string `json:"code"`
			TraceID string `json:"traceId"`
		}{"about:blank", "Upload could not be completed", status, code, id})
		return Result{Status: status, Body: body, Problem: true}, err
	})
}
