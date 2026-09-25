// syncscope_invoices.go is the invoices scope's Store (server_wins,
// a both-tenancies scope): the sync core's seam over invoices, the
// table this module owns beside the AR subledger. The pattern is the
// pilot's and the drive-run adapters'; the fence rules hold on the
// sync surface exactly as they hold on the REST surface.
//
// TENANCY, both classes, one fence each: a staff caller (the counter
// reading its invoice list) pulls the org's rows, org from X-Org per
// G4; a tenant caller (the payer's app) pulls its own documents,
// customer_id from the token's tenant, never from any header, the
// same predicate the aging scope reads (the tenant of a tenant bearer
// IS the contractor account). Alpha and beta share an org and must
// never see each other's invoices, which is why the tenant fence is
// the customer, not the org.
//
// THE WRITE SURFACE IS THE WRITER'S, AND IT IS A CREATE-ONLY DOOR.
// An invoice is a money document raised by this module's own writer
// from facts resolved server-side (invoice_writer.go's guarantees:
// one writer, one shape, tax and totals never trusted from a client),
// and every field a device could name on a raised document is either
// the writer's (the numbers, the status at birth) or the posting
// path's (the balance, the status money moves derive). So the pushed
// CREATE names an ORDER and nothing else: the document is raised by
// InvoiceOrderNamed, the writer's own path, under the writer's own
// idempotency key (order-invoice:<order>), which means the desk's
// double-clicked button and the device's replayed outbox converge on
// ONE document, the idempotency the writer already owns as a database
// fact. Updates and deletes refuse: a document whose balance and
// status are derived by money events has no client-declared field,
// and a delete that hides a settled document from the payer while the
// ledger keeps it is a collections lie. Corrections are the payments
// module's reversals and the desk's acts.
//
// THE IDEMPOTENCY COLUMN IS OCCUPIED, AND STAYS THE WRITER'S. 0146
// gave invoices.idempotency_key the writer's TEXT retry keys under a
// partial UNIQUE index, so 0213's additive UUID pass was a no-op
// there and the sync key CANNOT be stamped on the row without
// breaking the desk's convergence guard (a stamped-over key means the
// next double-click re-inserts a second document for the same order).
// FindApplied therefore consults the column (a writer key that
// happens to match answers its row) but the engine's replay
// correctness does not lean on it: a replayed create re-runs the
// writer and the writer's own key returns the same document, one
// state, the same answer the desk's double-click gets.
package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// invoiceSyncQuery is the contract's TenantInvoice projection (the
// list-row shape, no lines: the device's cached row and the payer
// app's list read one shape) plus the two sync legs.
const invoiceSyncQuery = `SELECT i.id::text, i.invoice_no::text, i.customer_id::text,
	COALESCE(i.order_id::text,''), i.status, i.issue_date, i.due_date,
	i.subtotal_cents, i.tax_cents, i.holdback_cents, i.total_cents, i.balance_cents,
	i.seq, (i.deleted_at IS NOT NULL) AS deleted
	FROM invoices i`

// invoiceSyncRow is one scanned row plus its sync legs.
type invoiceSyncRow struct {
	row     invoiceProjection
	seq     int64
	deleted bool
}

// invoiceProjection is the wire shape, the contract's TenantInvoice
// legs (dates as the date-time format the contract declares).
type invoiceProjection struct {
	ID            string    `json:"id"`
	InvoiceNo     string    `json:"invoice_no"`
	CustomerID    string    `json:"customer_id"`
	OrderID       string    `json:"order_id"`
	Status        string    `json:"status"`
	IssueDate     time.Time `json:"issue_date"`
	DueDate       time.Time `json:"due_date"`
	SubtotalCents int64     `json:"subtotal_cents"`
	TaxCents      int64     `json:"tax_cents"`
	HoldbackCents int64     `json:"holdback_cents"`
	TotalCents    int64     `json:"total_cents"`
	BalanceCents  int64     `json:"balance_cents"`
}

// scanInvoiceSync reads the 14-target row.
func scanInvoiceSync(row interface{ Scan(dest ...any) error }) (invoiceSyncRow, error) {
	var r invoiceSyncRow
	if err := row.Scan(&r.row.ID, &r.row.InvoiceNo, &r.row.CustomerID, &r.row.OrderID,
		&r.row.Status, &r.row.IssueDate, &r.row.DueDate,
		&r.row.SubtotalCents, &r.row.TaxCents, &r.row.HoldbackCents,
		&r.row.TotalCents, &r.row.BalanceCents, &r.seq, &r.deleted); err != nil {
		return invoiceSyncRow{}, err
	}
	return r, nil
}

// invoiceData projects one row through its json tags.
func invoiceData(p invoiceProjection) (map[string]any, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("billing: invoices sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("billing: invoices sync projection decode: %w", err)
	}
	return data, nil
}

// invoiceStateOf folds one scanned row into the engine's RowState; a
// tombstone carries its seq and no projection.
func invoiceStateOf(r invoiceSyncRow) (sync.RowState, error) {
	rs := sync.RowState{ID: r.row.ID, Seq: r.seq, Deleted: r.deleted}
	if r.deleted {
		return rs, nil
	}
	data, err := invoiceData(r.row)
	if err != nil {
		return sync.RowState{}, err
	}
	rs.Data = data
	return rs, nil
}

// invoiceScopePredicate is the WHERE predicate every invoices adapter
// query runs under: the org for a staff caller, the customer (the
// token's tenant) for a tenant caller. An empty anchor fails closed.
func invoiceScopePredicate(f sync.ScopeFilter) (string, []any, bool) {
	if f.Tenant {
		if f.TenantID == "" {
			return "", nil, false
		}
		return "i.customer_id = $1::uuid", []any{f.TenantID}, true
	}
	if f.OrgID == "" {
		return "", nil, false
	}
	return "i.org_id = $1::uuid", []any{f.OrgID}, true
}

// BillingAuthorizer is the billing door's narrow authority seam: may
// this platform subject bill inside this org? The REST door gates the
// same act behind RequireManager plus the service's own manager
// re-check (the order surface's own rule: invoicing is the supplier's
// move, a manager-tier act); the sync door asks the same question
// through the identity module's own read because the scaffold grants
// belong there. nil refuses every create (fail closed).
type BillingAuthorizer interface {
	ManagerInOrg(ctx context.Context, platformUserID, orgID string) (bool, error)
}

// InvoicesSyncStore serves the invoices scope over the invoices
// table, raising documents through the module's own InvoiceWriter.
type InvoicesSyncStore struct {
	pool   *pgxpool.Pool
	writer *InvoiceWriter
	auth   BillingAuthorizer
}

// NewInvoicesSyncStore wires the adapter; the writer must carry the
// order source and the tax assessor the composition root binds (the
// same seams the desk's writer carries, G1/G2 of invoice_writer.go),
// and the authorizer is the identity module's ManagerInOrg.
func NewInvoicesSyncStore(pool *pgxpool.Pool, writer *InvoiceWriter, auth BillingAuthorizer) *InvoicesSyncStore {
	return &InvoicesSyncStore{pool: pool, writer: writer, auth: auth}
}

// Pull returns the page of invoice changes past since, live rows and
// tombstones in seq order, at most limit rows total, under the
// caller-class fence.
func (s *InvoicesSyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	where, args, ok := invoiceScopePredicate(f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	rows, err := s.pool.Query(ctx, invoiceSyncQuery+`
		WHERE `+where+` AND i.seq IS NOT NULL AND i.seq > $2
		ORDER BY i.seq ASC
		LIMIT $3`, append(args, since, limit)...)
	if err != nil {
		return nil, nil, fmt.Errorf("billing: invoices sync pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for rows.Next() {
		r, err := scanInvoiceSync(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("billing: invoices sync pull scan: %w", err)
		}
		if r.deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: r.row.ID, Seq: r.seq})
			continue
		}
		data, err := invoiceData(r.row)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: r.row.ID, Seq: r.seq, Data: data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("billing: invoices sync pull: %w", err)
	}
	return items, tombstones, nil
}

// FindApplied resolves one idempotency key to the row it applied,
// under the caller-class fence. The column holds the WRITER's keys;
// the sync key is never stamped there (the file header's occupied
// column note), so a sync replay resolves through the writer's own
// convergence instead, and this read answers only a key that is
// genuinely the row's.
func (s *InvoicesSyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	where, args, ok := invoiceScopePredicate(f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	r, err := scanInvoiceSync(s.pool.QueryRow(ctx, invoiceSyncQuery+`
		WHERE `+where+` AND i.idempotency_key = $2
		LIMIT 1`, append(args, key)...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("billing: invoices sync find applied: %w", err)
	}
	rs, err := invoiceStateOf(r)
	return rs, true, err
}

// Load reads one invoice by id under the caller-class fence; a row
// outside the fence is the same as an absent one (existence never
// leaks, the IDOR discipline the payments module's reads share).
func (s *InvoicesSyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	where, args, ok := invoiceScopePredicate(f)
	if !ok || !invoiceLooksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	r, err := scanInvoiceSync(s.pool.QueryRow(ctx, invoiceSyncQuery+`
		WHERE i.id = $2::uuid AND `+where, append(args, id)...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("billing: invoices sync load: %w", err)
	}
	rs, err := invoiceStateOf(r)
	return rs, true, err
}

// invoiceCreateFields is the field set a pushed create may declare:
// the order the document bills, and the row id when the client
// generated one. Nothing else: the numbers, the tax, the due date and
// the status at birth are the writer's, resolved from the order's own
// facts (invoice_writer.go's G1).
func invoiceCreateFields(fields map[string]any) error {
	for k := range fields {
		switch k {
		case "id", "order_id":
		default:
			return fmt.Errorf("billing: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// Create raises one invoice for a named order through the module's
// own writer: the desk's act replayed, staff callers only (the
// catalog's own declaration: an invoice is written by the counter and
// the money desk; a tenant naming an order to bill its dealer is
// upside-down and refuses), MANAGER-TIER in the caller's org (the
// same authority the REST door's RequireManager and the order
// surface's own re-check carry). The order must sit in the caller's
// org; the document lands under the writer's own idempotency key, so
// a replay (same or different outbox key) and the desk's double-click
// converge on ONE document.
func (s *InvoicesSyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	if f.Tenant {
		return sync.RowState{}, fmt.Errorf("billing: an invoice is raised by the desk, not by a payer: %w", sync.ErrOpRefused)
	}
	if _, _, ok := invoiceScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if s.auth == nil {
		return sync.RowState{}, fmt.Errorf("billing: the billing door has no authority seam wired, refusing to raise a document: %w", sync.ErrOpRefused)
	}
	if f.Actor != "" {
		ok, err := s.auth.ManagerInOrg(ctx, f.Actor, f.OrgID)
		if err != nil {
			return sync.RowState{}, fmt.Errorf("billing: resolve the biller's authority: %w: %w", sync.ErrOpRefused, err)
		}
		if !ok {
			return sync.RowState{}, fmt.Errorf("billing: raising an invoice is a manager-tier act in this organisation: %w", sync.ErrOpRefused)
		}
	}
	if err := invoiceCreateFields(fields); err != nil {
		return sync.RowState{}, err
	}
	orderID, _ := fields["order_id"].(string)
	if !invoiceLooksLikeUUID(orderID) {
		return sync.RowState{}, fmt.Errorf("billing: order_id must be a uuid: %w", sync.ErrOpRefused)
	}
	docID, hasID := fields["id"].(string)
	if hasID && !invoiceLooksLikeUUID(docID) {
		return sync.RowState{}, fmt.Errorf("billing: payload id must be a uuid: %w", sync.ErrOpRefused)
	}
	// The org fence on the ORDER the document bills: the writer
	// derives the document's branch from the order's facts, so the
	// fence reads the order row's own org stamp (0213) before the
	// writer runs, fail closed on a NULL or foreign org. The orders
	// read is reference data this module's seams already sit beside
	// (the writer's facts come from the order module's own source);
	// the pricing module's products read records the same posture.
	var orderOrg *string
	if err := s.pool.QueryRow(ctx,
		`SELECT org_id::text FROM orders WHERE id = $1::uuid`, orderID).Scan(&orderOrg); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("billing: invoices sync order fence: %w", err)
	}
	if orderOrg == nil || *orderOrg != f.OrgID {
		return sync.RowState{}, sync.ErrRowNotFound
	}

	doc, err := s.writer.InvoiceOrderNamed(ctx, orderID, docID)
	if err != nil {
		// The writer's reuse arm already converged a same-order
		// replay; the remaining unique violation is the client-chosen
		// id taken by another document, and which refusal answers
		// depends on WHERE it is taken (the caller's own org is the
		// caller's fact, outside it existence never leaks).
		if isUniqueViolation(err) {
			if _, own, _ := s.Load(ctx, f, docID); own {
				return sync.RowState{}, sync.ErrRowExists
			}
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("billing: %w: %w", sync.ErrOpRefused, err)
	}
	r, err := scanInvoiceSync(s.pool.QueryRow(ctx, invoiceSyncQuery+` WHERE i.id = $1::uuid`, doc.InvoiceID))
	if err != nil {
		return sync.RowState{}, fmt.Errorf("billing: invoices sync create read back: %w", err)
	}
	return invoiceStateOf(r)
}

// Write refuses: every field on a raised document is the writer's or
// the posting path's, and the conflict policy's stale arm (the
// engine's own server_wins envelope) is the only replay outcome an
// update-shaped operation can produce here.
func (s *InvoicesSyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	return sync.RowState{}, fmt.Errorf("billing: an invoice's fields are the writer's and the posting path's; a money document has no client-declared field: %w", sync.ErrOpRefused)
}

// Delete refuses: a tombstone that hides a settled document from the
// payer while the ledger keeps it is a collections lie; voiding is
// the desk's own act with its own reasons (the reversal module's own
// recorded rule).
func (s *InvoicesSyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	return sync.RowState{}, fmt.Errorf("billing: an invoice is not deletable through sync; the ledger behind it stays: %w", sync.ErrOpRefused)
}

// invoiceLooksLikeUUID reports whether s is a canonical 36-char uuid,
// the boundary guard every pushed id passes before it reaches a
// Postgres uuid column.
func invoiceLooksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}
