// Package navigation owns bounded durable order snapshots, independent of live
// gallery metadata. Deletion hides IDs without changing the remaining order.
package navigation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/mikd1/tag-files/app/internal/catalog"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/organization"
	"github.com/mikd1/tag-files/app/internal/search"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

const MaxItems = 20000
const MaxOriginBytes = 65536
const Lifetime = 24 * time.Hour

type Input struct {
	Origin     string
	Options    catalog.ListOptions
	OrderedIDs *[]int64
}
type Origin struct {
	Route              string            `json:"route"`
	Query              *string           `json:"q,omitempty"`
	Kind               *domain.MediaKind `json:"kind,omitempty"`
	SavedSearchID      *int64            `json:"savedSearchId,omitempty"`
	SavedSearchVersion *int64            `json:"savedSearchVersion,omitempty"`
	QueryAST           *search.Expr      `json:"queryAst,omitempty"`
	CollectionID       *int64            `json:"collectionId,omitempty"`
	Sort               catalog.Sort      `json:"sort"`
	Seed               string            `json:"seed,omitempty"`
}
type Created struct {
	ID        string
	ExpiresAt time.Time
	Seed      string
}
type Neighbors struct {
	Expired            bool
	Origin             Origin
	Total              int
	PreviousID, NextID *int64
	Position           *int
}

func Create(ctx context.Context, tx *sql.Tx, in Input, now time.Time) (Created, error) {
	u, err := url.ParseRequestURI(in.Origin)
	if err != nil || len(in.Origin) > MaxOriginBytes || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") || strings.ContainsAny(u.Path, "\\\r\n") {
		return Created{}, domain.Invalid("origin", "Expected an internal app URL")
	}
	opts := in.Options
	if opts.Sort == "" {
		opts.Sort = catalog.UploadedDesc
	}
	f := opts.Filter
	origin := Origin{Route: in.Origin, Query: f.Query, Kind: f.Kind, SavedSearchID: f.SavedSearchID, CollectionID: f.CollectionID, Sort: opts.Sort, Seed: opts.Seed}
	q := dbgen.New(tx)
	var ids []int64
	if in.OrderedIDs != nil {
		if f.Query != nil || f.Kind != nil || f.SavedSearchID != nil || f.CollectionID != nil || opts.Seed != "" || opts.Sort != catalog.UploadedDesc {
			return Created{}, domain.Invalid("orderedIds", "Choose an explicit order or search source")
		}
		ids = append([]int64{}, (*in.OrderedIDs)...)
		if len(ids) > MaxItems {
			return Created{}, domain.Invalid("orderedIds", "Navigation is limited to 20000 items")
		}
		seen := map[int64]bool{}
		for _, id := range ids {
			if err := domain.ValidateID(id); err != nil {
				return Created{}, err
			}
			if seen[id] {
				return Created{}, domain.Invalid("orderedIds", "Item IDs must be unique")
			}
			seen[id] = true
		}
		live, err := liveIDs(ctx, q, ids)
		if err != nil {
			return Created{}, err
		}
		if len(live) != len(ids) {
			return Created{}, domain.ErrNotFound
		}
	} else {
		ids, origin.Seed, err = catalog.Snapshot(ctx, tx, opts, MaxItems)
		if err != nil {
			return Created{}, err
		}
		if f.SavedSearchID != nil {
			saved, err := q.GetSavedSearch(ctx, *f.SavedSearchID)
			if err != nil {
				return Created{}, err
			}
			origin.SavedSearchVersion = &saved.Version
			origin.QueryAST, err = organization.SavedAST(saved)
			if err != nil {
				return Created{}, err
			}
		}
	}
	created := Created{ID: domain.NewUUID(), ExpiresAt: now.Add(Lifetime), Seed: origin.Seed}
	originJSON, err := json.Marshal(origin)
	if err != nil {
		return Created{}, err
	}
	idsJSON, err := json.Marshal(ids)
	if err != nil {
		return Created{}, err
	}
	err = q.CreateNavigationContext(ctx, dbgen.CreateNavigationContextParams{ID: created.ID, OriginJson: string(originJSON), OrderedIdsJson: string(idsJSON), CreatedAt: store.Timestamp(now), ExpiresAt: store.Timestamp(created.ExpiresAt)})
	return created, err
}

func liveIDs(ctx context.Context, q *dbgen.Queries, ids []int64) (map[int64]bool, error) {
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	live, err := q.ListLiveItemIDs(ctx, string(raw))
	if err != nil {
		return nil, err
	}
	result := make(map[int64]bool, len(live))
	for _, id := range live {
		result[id] = true
	}
	return result, nil
}

func Get(ctx context.Context, db *sql.DB, id string, itemID int64, now time.Time) (Neighbors, error) {
	if err := domain.ValidateUUID(id); err != nil {
		return Neighbors{}, domain.Invalid("id", "Expected a UUID")
	}
	if err := domain.ValidateID(itemID); err != nil {
		return Neighbors{}, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Neighbors{}, err
	}
	defer tx.Rollback()
	q := dbgen.New(tx)
	row, err := q.GetNavigationContext(ctx, strings.ToLower(id))
	if errors.Is(err, sql.ErrNoRows) {
		return Neighbors{}, domain.ErrNotFound
	}
	if err != nil {
		return Neighbors{}, err
	}
	var result Neighbors
	if err := json.Unmarshal([]byte(row.OriginJson), &result.Origin); err != nil {
		return result, err
	}
	expires, err := time.Parse(time.RFC3339Nano, row.ExpiresAt)
	if err != nil {
		return result, err
	}
	if !now.Before(expires) {
		result.Expired = true
		return result, tx.Commit()
	}
	var ids []int64
	if err := json.Unmarshal([]byte(row.OrderedIdsJson), &ids); err != nil {
		return result, err
	}
	live, err := liveIDs(ctx, q, ids)
	if err != nil {
		return result, err
	}
	visible := []int64{}
	position := -1
	for _, id := range ids {
		if live[id] {
			if id == itemID {
				position = len(visible)
			}
			visible = append(visible, id)
		}
	}
	if position < 0 {
		return result, domain.ErrNotFound
	}
	result.Total = len(visible)
	humanPosition := position + 1
	result.Position = &humanPosition
	if position > 0 {
		result.PreviousID = &visible[position-1]
	}
	if position+1 < len(visible) {
		result.NextID = &visible[position+1]
	}
	return result, tx.Commit()
}
