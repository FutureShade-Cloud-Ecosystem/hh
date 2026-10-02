// syncscope.go is the dashboard module's pull-only widget_measures
// Store (HH-3.5 #169): the manager's Home widget configuration with its
// measures, the read cache manager-home declares. The POST answers the
// contract's refusal at the route (Colton's START ruling); the
// readcache skeleton's write primitives fail closed.
//
// THE FEED IS THE CALLER'S OWN ROWS: dashboard_user_widgets is keyed by
// user, so the honest device cache is the caller's own widget set, not
// every user's layout in the org. The caller resolves through the staff
// identity linker (the linked ERP user of the platform subject); an
// unlinked caller answers nothing, fail closed. The row id is derived
// (uuid v5 over the user and widget key, the composite natural key),
// deterministic across pulls; the pair rides in the projection.
package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/readcache"
)

// widgetNamespace is the uuid v5 namespace of the derived widget ids.
var widgetNamespace = uuid.MustParse("f1a7ba1a-0000-4000-8000-590000000002")

// widgetsColumns is the sync projection: the widget's configuration
// with its measure, plus the sync legs.
const widgetsColumns = `user_id::text, widget_key, shape, measure, split, COALESCE(branch_id::text,''),
	time_range, size, title, position, visible, updated_at,
	seq, (deleted_at IS NOT NULL) AS deleted`

// widgetRow is the wire shape of one cached widget row.
type widgetRow struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	WidgetKey string    `json:"widget_key"`
	Shape     string    `json:"shape"`
	Measure   string    `json:"measure"`
	Split     string    `json:"split"`
	BranchID  string    `json:"branch_id"`
	TimeRange string    `json:"time_range"`
	Size      string    `json:"size"`
	Title     string    `json:"title"`
	Position  int       `json:"position"`
	Visible   bool      `json:"visible"`
	UpdatedAt time.Time `json:"updated_at"`
}

// widgetID derives the stable uuid of one (user, widget key) pair.
func widgetID(userID, key string) string {
	return uuid.NewSHA1(widgetNamespace, []byte(userID+"|"+key)).String()
}

// NewWidgetMeasuresStore builds the widget_measures Store over the
// caller's own widget rows.
func NewWidgetMeasuresStore(pool *pgxpool.Pool, link identity.UserIDLinker) *WidgetMeasuresStore {
	return &WidgetMeasuresStore{pool: pool, link: link}
}

// WidgetMeasuresStore is the widget_measures Store: the per-user feed
// resolves the caller's linked user before the predicate can bind, so
// it carries its own Pull rather than the readcache skeleton's.
type WidgetMeasuresStore struct {
	pool *pgxpool.Pool
	link identity.UserIDLinker
}

// Pull resolves the caller's linked user and pulls their widget page;
// an unlinked caller answers nothing (fail closed, never another
// user's layout).
func (s *WidgetMeasuresStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	if f.Tenant || f.OrgID == "" || s.link == nil || f.Actor == "" {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	userID, ok := s.link(ctx, f.Actor)
	if !ok || userID == "" {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(
		`SELECT %s FROM dashboard_user_widgets
		 WHERE user_id = $1::uuid AND seq IS NOT NULL AND seq > $2
		 ORDER BY seq ASC LIMIT $3`, widgetsColumns), userID, since, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("dashboard: widgets pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for rows.Next() {
		id, seq, deleted, data, err := scanWidgetRow(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("dashboard: widgets pull scan: %w", err)
		}
		if deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: id, Seq: seq})
			continue
		}
		items = append(items, sync.Entity{ID: id, Seq: seq, Data: data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("dashboard: widgets pull: %w", err)
	}
	return items, tombstones, nil
}

// FindApplied fails closed: the scope has no push.
func (s *WidgetMeasuresStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	return sync.RowState{}, false, sync.ErrOpRefused
}

// Load answers the composite-key miss: the feed is the scope's whole
// read surface and no write path addresses one row by id.
func (s *WidgetMeasuresStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	return sync.RowState{}, false, nil
}

// Create fails closed: the scope has no push.
func (s *WidgetMeasuresStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	return sync.RowState{}, sync.ErrOpRefused
}

// Write fails closed: the scope has no push.
func (s *WidgetMeasuresStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	return sync.RowState{}, sync.ErrOpRefused
}

// Delete fails closed: the scope has no push.
func (s *WidgetMeasuresStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	return sync.RowState{}, sync.ErrOpRefused
}

// scanWidgetRow reads the 14-target row and projects it.
func scanWidgetRow(row readcache.Scanner) (string, int64, bool, map[string]any, error) {
	var r widgetRow
	var seq int64
	var deleted bool
	if err := row.Scan(&r.UserID, &r.WidgetKey, &r.Shape, &r.Measure, &r.Split, &r.BranchID,
		&r.TimeRange, &r.Size, &r.Title, &r.Position, &r.Visible, &r.UpdatedAt, &seq, &deleted); err != nil {
		return "", 0, false, nil, err
	}
	r.ID = widgetID(r.UserID, r.WidgetKey)
	raw, err := json.Marshal(r)
	if err != nil {
		return "", 0, false, nil, fmt.Errorf("dashboard: widgets projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", 0, false, nil, fmt.Errorf("dashboard: widgets projection decode: %w", err)
	}
	return r.ID, seq, deleted, data, nil
}
