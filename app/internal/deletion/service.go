package deletion

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

var ErrAwaitingRestart = fmt.Errorf("%w: deletion awaits restart verification", objectio.ErrUnsettled)

type Service struct {
	db *store.Store
	io *objectio.Manager
}

func New(db *store.Store, manager *objectio.Manager) (*Service, error) {
	if db == nil || manager == nil {
		return nil, domain.Invalid("deletion", "Deletion dependencies are required")
	}
	return &Service{db: db, io: manager}, nil
}
func leased(ctx context.Context, q *dbgen.Queries, job dbgen.Job, now time.Time) error {
	current, err := q.GetLeasedJob(ctx, dbgen.GetLeasedJobParams{ID: job.ID, Generation: job.LeaseGeneration, Now: sql.NullString{String: store.Timestamp(now), Valid: true}})
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrLeaseLost
	}
	if err != nil {
		return err
	}
	if current.SubjectID != job.SubjectID || current.PayloadJson != job.PayloadJson {
		return ErrIncompatible
	}
	_, err = DecodeJob(current)
	return err
}
func (s *Service) load(ctx context.Context, id string, full bool) (plan, error) {
	tx, err := s.db.Reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return plan{}, err
	}
	defer tx.Rollback()
	q := dbgen.New(tx)
	r, err := q.GetCleanupReceipt(ctx, id)
	if err != nil {
		return plan{}, err
	}
	var p plan
	if full {
		p, err = Validate(ctx, q, r)
	} else {
		p, err = readPlan(ctx, q, r)
	}
	if err != nil {
		return p, err
	}
	return p, tx.Commit()
}
func (s *Service) each(ctx context.Context, id string, fn func(dbgen.DeletionAsset) error) error {
	cursor := ""
	for {
		rows, err := dbgen.New(s.db.Reader()).ListDeletionAssets(ctx, dbgen.ListDeletionAssetsParams{ReceiptID: id, AfterID: cursor, LimitCount: 100})
		if err != nil {
			return err
		}
		for _, a := range rows {
			if err := fn(a); err != nil {
				return err
			}
			cursor = a.ObjectKey
		}
		if len(rows) < 100 {
			return nil
		}
	}
}
func matchesIntent(a dbgen.DeletionAsset, i objectio.Intent) error {
	if a.Role == "incoming" {
		if i.TargetKind != "upload" || i.TargetID != a.AssetID || i.Key != a.ObjectKey || i.Generation < 1 || (i.Operation != objectio.CreateMPU && i.Operation != objectio.UploadPart && i.Operation != objectio.CompleteMPU) {
			return ErrIncompatible
		}
		return nil
	}
	if i.TargetKind != "asset" || i.TargetID != a.AssetID || i.Key != a.ObjectKey || i.Generation != a.LeaseGeneration || i.Operation != objectio.PutObject || i.Arguments.Size != a.SizeBytes.Int64 || i.Arguments.SHA256 != a.Sha256.String || i.Arguments.ContentType != a.ContentType.String {
		return ErrIncompatible
	}
	return nil
}
func (s *Service) preflight(ctx context.Context, a dbgen.DeletionAsset) error {
	cursor := ""
	for {
		rows, err := s.io.ListKey(ctx, a.ObjectKey, cursor, 100)
		if err != nil {
			return err
		}
		for _, i := range rows {
			if err := matchesIntent(a, i); err != nil {
				return err
			}
			cursor = i.ID
		}
		if len(rows) < 100 {
			return nil
		}
	}
}
func missing(err error) bool {
	var wire *objectstore.Error
	return errors.As(err, &wire) && wire.Status == 404
}

// recordEpoch precedes DELETE. The lease and CAS prevent a delayed worker from
// replacing a newer checkpoint with an older backend lifetime.
func (s *Service) recordEpoch(ctx context.Context, job dbgen.Job, p plan, a dbgen.DeletionAsset, epoch string, clock func() time.Time) error {
	if domain.ValidateUUID(epoch) != nil {
		return ErrIncompatible
	}
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		if err := leased(ctx, q, job, clock()); err != nil {
			return err
		}
		r, err := q.GetCleanupReceipt(ctx, a.ReceiptID)
		if err != nil {
			return err
		}
		current, err := readPlan(ctx, q, r)
		if err != nil {
			return err
		}
		if current != p {
			return ErrIncompatible
		}
		n, err := q.RecordDeletionEpoch(ctx, dbgen.RecordDeletionEpochParams{ReceiptID: a.ReceiptID, ObjectKey: a.ObjectKey, Epoch: sql.NullString{String: epoch, Valid: true}, ExpectedEpoch: a.AbsenceEpoch})
		if err != nil {
			return err
		}
		if n == 1 {
			return nil
		}
		fresh, err := q.GetDeletionAsset(ctx, dbgen.GetDeletionAssetParams{ReceiptID: a.ReceiptID, ObjectKey: a.ObjectKey})
		if err != nil {
			return err
		}
		if fresh.AbsenceEpoch.Valid && fresh.AbsenceEpoch.String == epoch {
			return nil
		}
		return objectio.ErrUnsettled
	})
}
func (s *Service) remove(ctx context.Context, job dbgen.Job, p plan, a dbgen.DeletionAsset, c objectio.ReconciliationClient, clock func() time.Time) error {
	current, err := s.load(ctx, a.ReceiptID, false)
	if err != nil {
		return err
	}
	if current != p {
		return ErrIncompatible
	}
	// Read the checkpoint before HEAD, so its CAS cannot downgrade a newer
	// epoch recorded by another execution while this request was delayed.
	fresh, err := dbgen.New(s.db.Reader()).GetDeletionAsset(ctx, dbgen.GetDeletionAssetParams{ReceiptID: a.ReceiptID, ObjectKey: a.ObjectKey})
	if err != nil {
		return err
	}
	if fresh.ObjectKey != a.ObjectKey || fresh.Sha256 != a.Sha256 || fresh.SizeBytes != a.SizeBytes {
		return ErrIncompatible
	}
	if a.Role == "incoming" {
		mpus, err := c.ListMultipart(ctx, a.ObjectKey)
		if err != nil {
			return err
		}
		for _, mpu := range mpus {
			if mpu.Key == a.ObjectKey {
				if err := s.recordEpoch(ctx, job, p, fresh, c.BoundEpoch(), clock); err != nil {
					return err
				}
				if err := c.Abort(ctx, a.ObjectKey, mpu.UploadID); err != nil {
					return err
				}
			}
		}
		mpus, err = c.ListMultipart(ctx, a.ObjectKey)
		if err != nil {
			return err
		}
		for _, mpu := range mpus {
			if mpu.Key == a.ObjectKey {
				return objectio.ErrUnsettled
			}
		}
	}
	_, err = c.Head(ctx, a.ObjectKey)
	if missing(err) {
		if !fresh.AbsenceEpoch.Valid {
			return s.recordEpoch(ctx, job, p, fresh, c.BoundEpoch(), clock)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.recordEpoch(ctx, job, p, fresh, c.BoundEpoch(), clock); err != nil {
		return err
	}
	if err := c.Delete(ctx, a.ObjectKey); err != nil {
		return err
	}
	_, err = c.Head(ctx, a.ObjectKey)
	if missing(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return objectio.ErrUnsettled
}
func (s *Service) settle(ctx context.Context, job dbgen.Job, p plan, a dbgen.DeletionAsset, clock func() time.Time) error {
	if err := s.io.Drain(ctx, a.ObjectKey, ""); err != nil {
		return err
	}
	if err := s.io.RemoveOwned(ctx, func(c objectio.ReconciliationClient) error { return s.remove(ctx, job, p, a, c, clock) }); err != nil {
		return err
	}
	cursor := ""
	unsettled := false
	for {
		rows, err := s.io.ListKey(ctx, a.ObjectKey, cursor, 100)
		if err != nil {
			return err
		}
		for _, i := range rows {
			err := s.io.ReconcileAfterRestart(ctx, i, func(c objectio.ReconciliationClient, current objectio.Intent) error {
				if err := matchesIntent(a, current); err != nil {
					return err
				}
				return s.remove(ctx, job, p, a, c, clock)
			})
			if errors.Is(err, objectio.ErrUnsettled) {
				unsettled = true
			} else if err != nil {
				return err
			}
			cursor = i.ID
		}
		if len(rows) < 100 {
			break
		}
	}
	if unsettled {
		return objectio.ErrUnsettled
	}
	return nil
}

func (s *Service) Process(ctx context.Context, job dbgen.Job, now time.Time) error {
	started := time.Now()
	clock := func() time.Time { return now.Add(time.Since(started)) }
	id, err := DecodeJob(job)
	if err != nil {
		return err
	}
	p, err := s.load(ctx, id, true)
	if err != nil {
		return err
	}
	if job.SubjectID != fmt.Sprint(p.ItemID) {
		return ErrIncompatible
	}
	dependencies, err := dbgen.New(s.db.Reader()).CountDeletionDependencies(ctx, dbgen.CountDeletionDependenciesParams{ReceiptID: id, ItemID: nullableID(p.ItemID), UploadID: sql.NullString{String: p.UploadID, Valid: p.UploadID != ""}})
	if err != nil {
		return err
	}
	if dependencies != 0 {
		return objectio.ErrUnsettled
	}
	// Preflight every resource/journal page before the first removal.
	if err := s.each(ctx, id, func(a dbgen.DeletionAsset) error { return s.preflight(ctx, a) }); err != nil {
		return err
	}
	unsettled := false
	if err := s.each(ctx, id, func(a dbgen.DeletionAsset) error {
		err := s.settle(ctx, job, p, a, clock)
		if errors.Is(err, objectio.ErrUnsettled) {
			unsettled = true
			return nil
		}
		return err
	}); err != nil {
		return err
	}
	if unsettled {
		return objectio.ErrUnsettled
	}
	if err := s.db.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		if err := leased(ctx, q, job, clock()); err != nil {
			return err
		}
		r, err := q.GetCleanupReceipt(ctx, id)
		if err != nil {
			return err
		}
		current, err := Validate(ctx, q, r)
		if err != nil {
			return err
		}
		if current != p {
			return ErrIncompatible
		}
		n, err := q.CountDeletionDependencies(ctx, dbgen.CountDeletionDependenciesParams{ReceiptID: id, ItemID: nullableID(p.ItemID), UploadID: r.UploadID})
		if err != nil {
			return err
		}
		if n != 0 {
			return objectio.ErrUnsettled
		}
		n, err = q.CountUninventoriedAssets(ctx, dbgen.CountUninventoriedAssetsParams{ItemID: nullableID(p.ItemID), ReceiptID: id})
		if err != nil {
			return err
		}
		if n != 0 {
			return ErrIncompatible
		}
		rows, err := q.ListDeletionAssets(ctx, dbgen.ListDeletionAssetsParams{ReceiptID: id, AfterID: "", LimitCount: 100})
		if err != nil {
			return err
		}
		cursor := ""
		for {
			for _, a := range rows {
				n, err := q.CountWritesForKey(ctx, a.ObjectKey)
				if err != nil {
					return err
				}
				if n != 0 {
					return objectio.ErrUnsettled
				}
				cursor = a.ObjectKey
			}
			if len(rows) < 100 {
				break
			}
			rows, err = q.ListDeletionAssets(ctx, dbgen.ListDeletionAssetsParams{ReceiptID: id, AfterID: cursor, LimitCount: 100})
			if err != nil {
				return err
			}
		}
		_, err = q.DeleteTombstonedItem(ctx, p.ItemID)
		return err
	}); err != nil {
		return err
	}
	verifiedEpoch := ""
	err = s.io.RemoveOwned(ctx, func(c objectio.ReconciliationClient) error {
		verifiedEpoch = c.BoundEpoch()
		if domain.ValidateUUID(verifiedEpoch) != nil {
			return ErrIncompatible
		}
		return s.each(ctx, id, func(a dbgen.DeletionAsset) error {
			if !a.AbsenceEpoch.Valid || a.AbsenceEpoch.String == verifiedEpoch {
				return ErrAwaitingRestart
			}
			if a.Role == "incoming" {
				mpus, err := c.ListMultipart(ctx, a.ObjectKey)
				if err != nil {
					return err
				}
				for _, mpu := range mpus {
					if mpu.Key == a.ObjectKey {
						return objectio.ErrUnsettled
					}
				}
			}
			_, err := c.Head(ctx, a.ObjectKey)
			if missing(err) {
				return nil
			}
			if err != nil {
				return err
			}
			return objectio.ErrUnsettled
		})
	})
	if err != nil {
		return err
	}
	return s.db.CommitJob(ctx, job.ID, job.LeaseGeneration, clock(), func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		r, err := q.GetCleanupReceipt(ctx, id)
		if err != nil {
			return err
		}
		current, err := Validate(ctx, q, r)
		if err != nil {
			return err
		}
		if current != p {
			return ErrIncompatible
		}
		if _, err := q.GetDeletionItem(ctx, p.ItemID); !errors.Is(err, sql.ErrNoRows) {
			if err != nil {
				return err
			}
			return ErrIncompatible
		}
		n, err := q.CountUnverifiedDeletionEpochs(ctx, dbgen.CountUnverifiedDeletionEpochsParams{ReceiptID: id, Epoch: sql.NullString{String: verifiedEpoch, Valid: true}})
		if err != nil {
			return err
		}
		if n != 0 {
			return ErrAwaitingRestart
		}
		return q.DeleteCleanupReceipt(ctx, id)
	})
}
