package objectio

import (
	"context"
	"database/sql"

	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

// PreflightJournal decodes all independent write records before startup changes
// any jobs. It never treats a process restart as external-write settlement.
func PreflightJournal(ctx context.Context, tx *sql.Tx) error {
	q := dbgen.New(tx)
	after := ""
	for {
		rows, err := q.ListObjectWriteIntents(ctx, dbgen.ListObjectWriteIntentsParams{AfterID: after, LimitCount: 100})
		if err != nil {
			return err
		}
		for _, row := range rows {
			if _, err := decode(row); err != nil {
				return err
			}
			after = row.ID
		}
		if len(rows) < 100 {
			return nil
		}
	}
}
