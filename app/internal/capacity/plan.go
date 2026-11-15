package capacity

import (
	"context"
	"database/sql"
	"errors"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/poster"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func (m *Manager) load(ctx context.Context, extra Extra) (plan, error) {
	p := plan{Incoming: extra.Incoming, Working: extra.Working, Scratch: extra.Scratch}
	tx, err := m.db.Reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	q := dbgen.New(tx)
	maxPart := extra.PartBytes
	after := ""
	for {
		rows, err := q.ListCapacitySessions(ctx, dbgen.ListCapacitySessionsParams{AfterID: after, LimitCount: 100})
		if err != nil {
			return p, err
		}
		for _, s := range rows {
			after = s.ID
			if s.State == domain.SessionFailed {
				continue
			}
			if s.ConfirmedBytes < 0 || s.ConfirmedBytes > s.ExpectedSize {
				return p, ErrUnavailable
			}
			work, err := m.Retry(s.ExpectedSize)
			if err != nil {
				return p, err
			}
			p.Working, p.Scratch = max(p.Working, work.Working), max(p.Scratch, work.Scratch)
			if !s.SourceSha256.Valid {
				incoming := s.ExpectedSize
				for range m.config.Parallel {
					incoming, err = add(incoming, s.ExpectedSize)
					if err != nil {
						return p, err
					}
				}
				p.Incoming, err = add(p.Incoming, incoming-s.ConfirmedBytes)
				if err != nil {
					return p, err
				}
				maxPart = max(maxPart, min(s.ExpectedSize, s.PartSize))
			}
		}
		if len(rows) < 100 {
			break
		}
	}
	// A queued derivative is an intent, not a concurrently running producer.
	// The runner checks this budget after claiming and before source IO. This
	// keeps one oversized legacy poster from reserving every queued file's peak.
	after = ""
	for {
		rows, err := q.ListCapacityPosters(ctx, dbgen.ListCapacityPostersParams{AfterID: after, LimitCount: 100})
		if err != nil {
			return p, err
		}
		for _, j := range rows {
			after = j.ID
			payload, err := poster.DecodeJob(j)
			if err != nil {
				return p, err
			}
			primary, err := q.GetCleanupAsset(ctx, payload.PrimaryAssetID)
			if err != nil {
				return p, err
			}
			peak, err := add(primary.SizeBytes, m.config.PosterLimit)
			if err != nil {
				return p, err
			}
			p.Working, p.Scratch = max(p.Working, m.config.PosterLimit), max(p.Scratch, peak)
		}
		if len(rows) < 100 {
			break
		}
	}
	// Part retries can be identical yet still allocate another physical extent.
	// Reserve the complete configured number of largest persisted/new parts,
	// including the gap between admission and committing their write intents.
	for range m.config.Parallel {
		p.Incoming, err = add(p.Incoming, maxPart)
		if err != nil {
			return p, err
		}
	}
	after = ""
	for {
		rows, err := q.ListObjectWriteIntents(ctx, dbgen.ListObjectWriteIntentsParams{AfterID: after, LimitCount: 100})
		if err != nil {
			return p, err
		}
		for _, row := range rows {
			after = row.ID
			i, err := objectio.DecodeIntent(row)
			if err != nil {
				return p, err
			}
			// Bounded control metadata also needs space when an old request can
			// still finish; a CreateMPU has no binary body equal to its file size.
			bytes := int64(64 * 1024)
			covered := false
			if i.State != "uncertain" {
				switch i.Operation {
				case objectio.CreateMPU:
					covered = true
				case objectio.CompleteMPU:
					s, err := q.GetUploadSession(ctx, i.TargetID)
					if err != nil && !errors.Is(err, sql.ErrNoRows) {
						return p, err
					}
					covered = err == nil && !s.SourceSha256.Valid && s.Generation == i.Generation
				case objectio.PutObject:
					covered, err = m.coveredCandidate(ctx, q, i)
					if err != nil {
						return p, err
					}
				}
			}
			if !covered && i.Operation != objectio.CreateMPU {
				bytes, err = add(bytes, i.Arguments.Size)
				if err != nil {
					return p, err
				}
			}
			p.Incoming, err = add(p.Incoming, bytes)
			if err != nil {
				return p, err
			}
		}
		if len(rows) < 100 {
			break
		}
	}
	return p, tx.Commit()
}

func (m *Manager) coveredCandidate(ctx context.Context, q *dbgen.Queries, i objectio.Intent) (bool, error) {
	a, err := q.GetCleanupAsset(ctx, i.TargetID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || !a.JobID.Valid || a.ItemID.Valid || a.LeaseGeneration != i.Generation {
		return false, err
	}
	if a.ObjectKey != i.Key || a.SizeBytes != i.Arguments.Size || a.Sha256 != i.Arguments.SHA256 || a.ContentType != i.Arguments.ContentType || a.State != domain.AssetCandidate && a.State != domain.AssetVerified {
		return false, nil
	}
	if a.Role == "primary" && a.SizeBytes > m.config.OutputLimit || a.Role == "poster" && a.SizeBytes > m.config.PosterLimit {
		return false, nil
	}
	j, err := q.GetJob(ctx, a.JobID.String)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return j.State == domain.JobRunning && j.LeaseGeneration == i.Generation && (a.Role == "primary" && j.Type == domain.JobIngest || a.Role == "poster" && j.Type == domain.JobPoster), nil
}
