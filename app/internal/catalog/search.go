package catalog

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/organization"
	"github.com/mikd1/tag-files/app/internal/search"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

type Sort string

const (
	UploadedDesc      Sort = "UploadedDesc"
	UploadedAsc       Sort = "UploadedAsc"
	VideoDurationDesc Sort = "VideoDurationDesc"
	VideoDurationAsc  Sort = "VideoDurationAsc"
	ViewCountDesc     Sort = "ViewCountDesc"
	ViewCountAsc      Sort = "ViewCountAsc"
	Random            Sort = "Random"
)

var sortSQL = map[Sort]string{
	UploadedDesc: "i.uploaded_at_utc DESC,i.id ASC", UploadedAsc: "i.uploaded_at_utc ASC,i.id ASC",
	VideoDurationDesc: "i.duration_ticks DESC,i.id ASC", VideoDurationAsc: "i.duration_ticks ASC,i.id ASC",
	ViewCountDesc: "i.view_count DESC,i.id ASC", ViewCountAsc: "i.view_count ASC,i.id ASC", Random: "i.id ASC",
}

type Filter struct {
	Query         *string
	Kind          *domain.MediaKind
	SavedSearchID *int64
	CollectionID  *int64
}
type ListOptions struct {
	Filter         Filter
	Sort           Sort
	Page, PageSize int
	Seed           string
}
type Page struct {
	Items                                  []Item
	TotalItems, Page, PageSize, TotalPages int
	Seed                                   string
}

func normalize(o ListOptions) (ListOptions, error) {
	if o.Sort == "" {
		o.Sort = UploadedDesc
	}
	if _, ok := sortSQL[o.Sort]; !ok {
		return o, domain.Invalid("sort", "Invalid sort order")
	}
	if o.Page == 0 {
		o.Page = 1
	}
	if o.PageSize == 0 {
		o.PageSize = 24
	}
	if o.Page < 1 || o.PageSize < 1 || o.PageSize > 200 || int64(o.Page) > domain.MaxSafeInteger/int64(o.PageSize) {
		return o, domain.Invalid("page", "Invalid page or page size")
	}
	if len(o.Seed) > 128 {
		return o, domain.Invalid("seed", "Seed is too long")
	}
	if o.Sort == Random && o.Seed == "" {
		o.Seed = rand.Text()
	}
	return o, nil
}

func predicate(ctx context.Context, q *dbgen.Queries, f Filter) (string, []any, error) {
	var ast *search.Expr
	var err error
	kind := f.Kind
	if f.SavedSearchID != nil {
		if f.Query != nil || f.Kind != nil {
			return "", nil, domain.Invalid("savedSearchId", "Choose a saved search or query and kind")
		}
		if err := domain.ValidateID(*f.SavedSearchID); err != nil {
			return "", nil, err
		}
		saved, err := q.GetSavedSearch(ctx, *f.SavedSearchID)
		if err != nil {
			return "", nil, notFound(err)
		}
		ast, err = organization.SavedAST(saved)
		if err != nil {
			return "", nil, err
		}
		if saved.MediaKind.Valid {
			k := domain.MediaKind(saved.MediaKind.String)
			kind = &k
		}
	} else {
		text := ""
		if f.Query != nil {
			text = *f.Query
		}
		ast, err = search.Parse(text)
		if err != nil {
			return "", nil, err
		}
		tags, err := q.ListTags(ctx)
		if err != nil {
			return "", nil, err
		}
		names := make([]search.NamedTag, len(tags))
		for i, tag := range tags {
			names[i] = search.NamedTag{ID: tag.ID, Name: tag.Name}
		}
		ast, err = search.Bind(ast, names)
		if err != nil {
			return "", nil, err
		}
	}
	filter, args, err := search.Compile(ast)
	if err != nil {
		return "", nil, err
	}
	filter = "i.state='ready' AND (" + filter + ")"
	if kind != nil {
		if err := kind.Validate(); err != nil {
			return "", nil, err
		}
		filter += " AND i.media_kind=?"
		args = append(args, string(*kind))
	}
	if f.CollectionID != nil {
		if err := domain.ValidateID(*f.CollectionID); err != nil {
			return "", nil, err
		}
		filter += " AND i.collection_id=?"
		args = append(args, *f.CollectionID)
	}
	return filter, args, nil
}

func List(ctx context.Context, db *sql.DB, options ListOptions) (Page, error) {
	o, err := normalize(options)
	if err != nil {
		return Page{}, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Page{}, err
	}
	defer tx.Rollback()
	filter, args, err := predicate(ctx, dbgen.New(tx), o.Filter)
	if err != nil {
		return Page{}, err
	}
	page, err := selectPage(ctx, tx, filter, args, o)
	if err != nil {
		return Page{}, err
	}
	return page, tx.Commit()
}

func selectPage(ctx context.Context, tx *sql.Tx, filter string, args []any, o ListOptions) (Page, error) {
	p := Page{Items: []Item{}, Page: o.Page, PageSize: o.PageSize, Seed: o.Seed}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM items i WHERE `+filter, args...).Scan(&p.TotalItems); err != nil {
		return p, err
	}
	p.TotalPages = (p.TotalItems + o.PageSize - 1) / o.PageSize
	query := `SELECT i.id FROM items i WHERE ` + filter + ` ORDER BY ` + sortSQL[o.Sort]
	queryArgs := append([]any(nil), args...)
	offset := (o.Page - 1) * o.PageSize
	if o.Sort != Random {
		query += " LIMIT ? OFFSET ?"
		queryArgs = append(queryArgs, o.PageSize, offset)
	}
	rows, err := tx.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return p, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return p, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return p, err
	}
	if err := rows.Close(); err != nil {
		return p, err
	}
	if o.Sort == Random {
		type ranked struct {
			id   int64
			hash [32]byte
		}
		rank := make([]ranked, len(ids))
		for i, id := range ids {
			input := make([]byte, len(o.Seed)+8)
			copy(input, o.Seed)
			binary.BigEndian.PutUint64(input[len(o.Seed):], uint64(id))
			rank[i] = ranked{id: id, hash: sha256.Sum256(input)}
		}
		sort.Slice(rank, func(i, j int) bool {
			c := bytes.Compare(rank[i].hash[:], rank[j].hash[:])
			if c == 0 {
				return rank[i].id < rank[j].id
			}
			return c < 0
		})
		ids = ids[:0]
		for i := offset; i < len(rank) && len(ids) < o.PageSize; i++ {
			ids = append(ids, rank[i].id)
		}
	}
	q := dbgen.New(tx)
	for _, id := range ids {
		item, err := get(ctx, q, id)
		if err != nil {
			return p, err
		}
		p.Items = append(p.Items, item)
	}
	return p, nil
}

func Similar(ctx context.Context, db *sql.DB, id int64, page, pageSize int) (Page, error) {
	if err := domain.ValidateID(id); err != nil {
		return Page{}, err
	}
	o, err := normalize(ListOptions{Page: page, PageSize: pageSize, Sort: UploadedDesc})
	if err != nil {
		return Page{}, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Page{}, err
	}
	defer tx.Rollback()
	if _, err := dbgen.New(tx).GetItem(ctx, id); err != nil {
		return Page{}, notFound(err)
	}
	filter := strings.Join([]string{
		`i.state='ready' AND i.id<>?`,
		`EXISTS (SELECT 1 FROM item_tags source WHERE source.item_id=?)`,
		`NOT EXISTS (SELECT tag_id FROM item_tags WHERE item_id=i.id EXCEPT SELECT tag_id FROM item_tags WHERE item_id=?)`,
		`NOT EXISTS (SELECT tag_id FROM item_tags WHERE item_id=? EXCEPT SELECT tag_id FROM item_tags WHERE item_id=i.id)`,
	}, " AND ")
	result, err := selectPage(ctx, tx, filter, []any{id, id, id, id}, o)
	if err != nil {
		return Page{}, fmt.Errorf("similar items: %w", err)
	}
	return result, tx.Commit()
}
