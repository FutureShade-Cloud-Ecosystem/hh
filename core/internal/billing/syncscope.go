// syncscope.go is the billing module's pull-only aging Store (HH-3.5
// #169): what is past due, by the one definition of late the whole
// product shares (the due date on the invoice the posting path owns).
// The POST answers the contract's refusal at the route (Colton's START
// ruling); the write primitives below fail closed.
//
// The row is the tenant's invoice (id and seq straight off the
// invoices feed) with the aging legs computed in the projection: the
// balance the posting path maintains, the days past due against the
// dealer's day (the house time zone rule: daily means the dealer's
// local day, never UTC), and the bucket that derives from the days. A
// device may not author a balance, so nothing here is writable.
package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// arAgingZone is the Ontario default zone the aging SQL falls back to when no dealer zone is wired
// (the SQL twin of platform/houseday's default, spelled here because the aging store keeps its own
// copy of the name). The zone the SQL actually reads is the dealer's, bound as a parameter, never a
// literal in the text; the backend-wide zone census (dashboard's
// TestHouseZone_EveryCopyInTheBackendNamesTheSameZone) still enumerates this constant as a default.
const arAgingZone = "America/Toronto"

// houseDaySQL is the Ontario day expression, kept for tests that seed relative to the Ontario day. The
// v_ar_aging view no longer carries it: migration 0394 reads the dealer's zone through settings'
// settings_business_zone(), so finstmt's and reporting's AR aging name the same day as everything billing
// reads itself, which takes the dealer's zone as a bind parameter (houseDaySQLAt).
const houseDaySQL = `(now() AT TIME ZONE '` + arAgingZone + `')::date`

// houseDaySQLAt is the ONE expression every SQL-side aging computation derives the dealer's day
// from, the choice the 0376 migration names in its comment: the session clock converted to the
// dealer's zone, which arrives as the bind parameter zoneParam (for example "$4"). CURRENT_DATE and
// now()::date name the session zone's day instead, which is the UTC day here, and age every open item
// one day early through the evening window.
func houseDaySQLAt(zoneParam string) string {
	return `(now() AT TIME ZONE ` + zoneParam + `::text)::date`
}

// agingColumnsAt is the projection: the invoice legs the payer reads plus the computed aging legs and
// the sync legs. zoneParam is the bind parameter carrying the dealer's zone name.
func agingColumnsAt(zoneParam string) string {
	return `i.id::text, i.invoice_no::text, i.status, i.issue_date, i.due_date,
	i.total_cents, i.balance_cents,
	(` + houseDaySQLAt(zoneParam) + ` - i.due_date)::bigint,
	i.seq, (i.deleted_at IS NOT NULL) AS deleted`
}

// AgingStore is the aging pull-only Store.
type AgingStore struct {
	pool *pgxpool.Pool
	// tz is the dealer's time zone, the house day the past-due count
	// means. nil answers UTC, the recorded fallback while org config
	// carries no zone column in the frozen schema.
	tz *time.Location
	// zone is the dealer's business day zone source; nil = the Ontario dealer's. The past-due count is
	// the dealer's own calendar days.
	zone houseday.Source
}

// WithZone binds the dealer's business day zone source. Nil keeps the Ontario dealer's.
func (s *AgingStore) WithZone(src houseday.Source) *AgingStore { s.zone = src; return s }

// NewAgingStore wires the adapter.
func NewAgingStore(pool *pgxpool.Pool, tz *time.Location) *AgingStore {
	return &AgingStore{pool: pool, tz: tz}
}

// agingRow is the wire shape of one cached aging row.
type agingRow struct {
	ID           string `json:"id"`
	InvoiceNo    string `json:"invoice_no"`
	Status       string `json:"status"`
	IssueDate    string `json:"issue_date"`
	DueDate      string `json:"due_date"`
	TotalCents   int64  `json:"total_cents"`
	BalanceCents int64  `json:"balance_cents"`
	DaysPastDue  int64  `json:"days_past_due"`
	Bucket       string `json:"bucket"`
	PastDue      bool   `json:"past_due"`
}

// bucketOf derives the aging bucket from the days past due, the
// receivables buckets the ar console already reads.
func bucketOf(days int64) string {
	switch {
	case days <= 0:
		return "current"
	case days <= 30:
		return "days_01_30"
	case days <= 60:
		return "days_31_60"
	case days <= 90:
		return "days_61_90"
	default:
		return "days_90_plus"
	}
}

// agingScan is one scanned row plus its sync legs.
type agingScan struct {
	row     agingRow
	seq     int64
	deleted bool
}

// scanAging reads the 10-target row.
func (s *AgingStore) scanAging(row interface{ Scan(dest ...any) error }) (agingScan, error) {
	var out agingScan
	var issue, due time.Time
	if err := row.Scan(&out.row.ID, &out.row.InvoiceNo, &out.row.Status, &issue, &due,
		&out.row.TotalCents, &out.row.BalanceCents, &out.row.DaysPastDue,
		&out.seq, &out.deleted); err != nil {
		return agingScan{}, err
	}
	loc := s.tz
	if loc == nil {
		loc = time.UTC
	}
	out.row.IssueDate = issue.In(loc).Format("2006-01-02")
	out.row.DueDate = due.In(loc).Format("2006-01-02")
	out.row.Bucket = bucketOf(out.row.DaysPastDue)
	out.row.PastDue = out.row.DaysPastDue > 0 && out.row.BalanceCents > 0
	return out, nil
}

// projectAging marshals one row through its json tags.
func projectAging(r agingRow) (map[string]any, error) {
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("billing: aging projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("billing: aging projection decode: %w", err)
	}
	return data, nil
}

// Pull returns the tenant's invoice page past since with the aging
// projection.
//
// A PARKED CREDIT MEMO IS NOT IN THE PAGE. A credit memo that waits for its approval is a DRAFT
// document, and a draft credit is not yet a credit: no money has moved, so the customer's page must not
// show it. It arrives on the next pull, once the approval has made it FINALIZED.
func (s *AgingStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	if !f.Tenant || f.TenantID == "" {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(
		`SELECT %s FROM invoices i
		 WHERE i.customer_id = $1::uuid AND i.seq IS NOT NULL AND i.seq > $2
		   AND NOT (i.doc_type = 'CREDIT_MEMO' AND i.status = 'DRAFT')
		 ORDER BY i.seq ASC LIMIT $3`, agingColumnsAt("$4")), f.TenantID, since, limit, zone.Name())
	if err != nil {
		return nil, nil, fmt.Errorf("billing: aging pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for rows.Next() {
		r, err := s.scanAging(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("billing: aging pull scan: %w", err)
		}
		if r.deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: r.row.ID, Seq: r.seq})
			continue
		}
		data, err := projectAging(r.row)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: r.row.ID, Seq: r.seq, Data: data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("billing: aging pull: %w", err)
	}
	return items, tombstones, nil
}

// FindApplied fails closed: the scope has no push.
func (s *AgingStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	return sync.RowState{}, false, sync.ErrOpRefused
}

// Load reads one invoice's aging row under the tenant predicate. The
// pull's PARKED CREDIT MEMO clause rides the query (UX-112): a parked
// memo is in no page, so the key read answers the absent row's answer
// and the engine never reads a row the pull hides as visible.
func (s *AgingStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	if !f.Tenant || f.TenantID == "" {
		return sync.RowState{}, false, nil
	}
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return sync.RowState{}, false, err
	}
	r, err := s.scanAging(s.pool.QueryRow(ctx, fmt.Sprintf(
		`SELECT %s FROM invoices i WHERE i.customer_id = $1::uuid AND i.id = $2::uuid
		   AND NOT (i.doc_type = 'CREDIT_MEMO' AND i.status = 'DRAFT')`,
		agingColumnsAt("$3")), f.TenantID, id, zone.Name()))
	if err == pgx.ErrNoRows {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("billing: aging load: %w", err)
	}
	rs := sync.RowState{ID: r.row.ID, Seq: r.seq, Deleted: r.deleted}
	if !r.deleted {
		data, err := projectAging(r.row)
		if err != nil {
			return sync.RowState{}, false, err
		}
		rs.Data = data
	}
	return rs, true, nil
}

// Create fails closed: the scope has no push.
func (s *AgingStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	return sync.RowState{}, sync.ErrOpRefused
}

// Write fails closed: the scope has no push.
func (s *AgingStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	return sync.RowState{}, sync.ErrOpRefused
}

// Delete fails closed: the scope has no push.
func (s *AgingStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	return sync.RowState{}, sync.ErrOpRefused
}
