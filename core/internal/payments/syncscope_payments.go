// syncscope_payments.go is the payments scope's Store (server_wins,
// a tenant scope): the sync core's seam over the payments table this
// module owns beside its allocations. The pattern is the pilot's and
// the drive-run adapters'; the fence rules hold on the sync surface
// exactly as they hold on the REST surface.
//
// TENANT FROM THE TOKEN, NEVER FROM A HEADER (G4): the scope serves
// the payer (the catalog's own declaration: the contractor pays, the
// money desk reads), the fence is the customer_id the middleware
// stamped off the token's tenant (the tenant of a tenant bearer IS
// the contractor account, the aging and team precedents), and a
// staff caller answers the route's RBAC-403 before any store call.
// Alpha and beta share an org and must never see each other's
// payments, which is why the fence is the customer, not the org.
//
// THE WRITE SURFACE IS THE PAY FLOW, AND IT IS A CREATE-ONLY DOOR.
// A pushed create is the payer's pay request replayed: it names the
// invoices and the stored method, and NOTHING else, because the
// amount is derived server-side (payable balances, holdback
// excluded), the charge runs under the module's server-bound
// idempotency key (the DIB-415 intent, claimed BEFORE the gateway is
// touched), and the settle is the one atomic transaction the module
// already owns (payment row, allocations, subledger, invoice balance
// recompute). The offline stamps ride that same settle: the
// client-chosen row id and the client idempotency key, the key
// landing in the table's own 0028 replay-guard column, so the
// engine's dedup and the partial-unique index are one guard, not
// two. A declined charge is the operation's rejected result (a
// decline is data, the catalog's own words), never a failed request.
//
// UPDATES AND DELETES REFUSE: a payment is a settled fact. Its
// status moves are teller acts (a deposit batch), its corrections
// are the reversal flow (a staff act with a reason, 0106), and a
// tombstone over a gateway-settled charge is a ledger divergence no
// feed should carry. The conflict policy's stale arm (the engine's
// server_wins envelope) remains the replay outcome for any
// update-shaped operation, and the invoice rows a settle re-opens or
// pays carry their own scope's feed.
package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// paymentScopePredicate is the WHERE predicate every payments adapter
// query runs under: the customer the token's tenant named. A staff
// filter or an empty tenant fails closed (the route's 403 is the
// middleware half, this is the store's).
func paymentScopePredicate(f sync.ScopeFilter) (string, []any, bool) {
	if !f.Tenant || f.TenantID == "" {
		return "", nil, false
	}
	return "p.customer_id = $1::uuid", []any{f.TenantID}, true
}

// paymentSyncQuery is the module's own Payment projection (the shape
// ListPayments serves, the model's json tags) plus the two sync legs.
// The allocations ride a second batched read, exactly as ListPayments
// attaches them.
const paymentSyncQuery = `SELECT p.id::text, p.payment_no::text, p.customer_id::text, p.method,
	p.amount_cents, p.status, p.gateway_ref, p.received_at,
	p.seq, (p.deleted_at IS NOT NULL) AS deleted
	FROM payments p`

// paymentSyncRow is one scanned row plus its sync legs.
type paymentSyncRow struct {
	pay     Payment
	seq     int64
	deleted bool
}

// scanPaymentSync reads the 10-target row.
func scanPaymentSync(row interface{ Scan(dest ...any) error }) (paymentSyncRow, error) {
	var r paymentSyncRow
	var customerID, gatewayRef *string
	if err := row.Scan(&r.pay.ID, &r.pay.PaymentNo, &customerID, &r.pay.Method,
		&r.pay.AmountCents, &r.pay.Status, &gatewayRef, &r.pay.ReceivedAt,
		&r.seq, &r.deleted); err != nil {
		return paymentSyncRow{}, err
	}
	if customerID != nil {
		r.pay.CustomerID = *customerID
	}
	if gatewayRef != nil {
		r.pay.GatewayRef = *gatewayRef
	}
	return r, nil
}

// paymentStateOf folds one scanned row into the engine's RowState,
// allocations attached by the caller; a tombstone carries its seq and
// no projection.
func paymentStateOf(r paymentSyncRow, allocs []Allocation) (sync.RowState, error) {
	rs := sync.RowState{ID: r.pay.ID, Seq: r.seq, Deleted: r.deleted}
	if r.deleted {
		return rs, nil
	}
	r.pay.Allocations = allocs
	raw, err := paymentProjection(r.pay)
	if err != nil {
		return sync.RowState{}, err
	}
	rs.Data = raw
	return rs, nil
}

// PaymentsSyncStore serves the payments scope over the payments
// table, paying through the module's own guarded flow.
type PaymentsSyncStore struct {
	pool *pgxpool.Pool
	svc  *Service
	repo *Repository
}

// NewPaymentsSyncStore wires the adapter; the service must be the
// production composition (the Repository store, so the DIB-415 intent
// guard and the stamped settle both hold: PaySynced refuses over a
// store that cannot carry the stamps).
func NewPaymentsSyncStore(pool *pgxpool.Pool, svc *Service) *PaymentsSyncStore {
	return &PaymentsSyncStore{pool: pool, svc: svc, repo: NewRepository(pool)}
}

// Pull returns the page of payment changes past since, live rows and
// tombstones in seq order, at most limit rows total, under the tenant
// fence. The allocations ride one batched read, ListPayments' own
// shape, so the projection is the module's public Payment wire shape.
func (s *PaymentsSyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	where, args, ok := paymentScopePredicate(f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	rows, err := s.pool.Query(ctx, paymentSyncQuery+`
		WHERE `+where+` AND p.seq IS NOT NULL AND p.seq > $2
		ORDER BY p.seq ASC
		LIMIT $3`, append(args, since, limit)...)
	if err != nil {
		return nil, nil, fmt.Errorf("payments: sync pull: %w", err)
	}
	defer rows.Close()
	scanned := []paymentSyncRow{}
	tombstones := []sync.Tombstone{}
	ids := []string{}
	for rows.Next() {
		r, err := scanPaymentSync(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("payments: sync pull scan: %w", err)
		}
		if r.deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: r.pay.ID, Seq: r.seq})
			continue
		}
		scanned = append(scanned, r)
		ids = append(ids, r.pay.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("payments: sync pull: %w", err)
	}
	byPayment, err := s.repo.allocationsByPayment(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	items := make([]sync.Entity, 0, len(scanned))
	for _, r := range scanned {
		rs, err := paymentStateOf(r, byPayment[r.pay.ID])
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: rs.ID, Seq: rs.Seq, Data: rs.Data})
	}
	return items, tombstones, nil
}

// paymentProjection marshals one payment through the model's json
// tags, the module's public wire shape.
func paymentProjection(p Payment) (map[string]any, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("payments: sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("payments: sync projection decode: %w", err)
	}
	return data, nil
}

// FindApplied resolves one idempotency key to the row it applied,
// under the tenant fence: the 0028 column the settle stamped, the
// table's own replay guard.
func (s *PaymentsSyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	where, args, ok := paymentScopePredicate(f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	r, err := scanPaymentSync(s.pool.QueryRow(ctx, paymentSyncQuery+`
		WHERE `+where+` AND p.idempotency_key = $2
		LIMIT 1`, append(args, key)...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("payments: sync find applied: %w", err)
	}
	allocs, err := s.repo.allocationsByPayment(ctx, []string{r.pay.ID})
	if err != nil {
		return sync.RowState{}, false, err
	}
	rs, err := paymentStateOf(r, allocs[r.pay.ID])
	return rs, true, err
}

// Load reads one payment by id under the tenant fence; a row outside
// the fence is the same as an absent one (existence never leaks, the
// module's own IDOR discipline).
func (s *PaymentsSyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	where, args, ok := paymentScopePredicate(f)
	if !ok || !paymentLooksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	r, err := scanPaymentSync(s.pool.QueryRow(ctx, paymentSyncQuery+`
		WHERE p.id = $2::uuid AND `+where, append(args, id)...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("payments: sync load: %w", err)
	}
	allocs, err := s.repo.allocationsByPayment(ctx, []string{r.pay.ID})
	if err != nil {
		return sync.RowState{}, false, err
	}
	rs, err := paymentStateOf(r, allocs[r.pay.ID])
	return rs, true, err
}

// paymentCreateFields is the field set a pushed create may declare:
// the invoices the payment settles and the stored method that funds
// it, plus the row id when the client generated one. Nothing else:
// the amount is derived server-side, and a tokenization nonce is a
// one-time ONLINE artifact an offline outbox cannot carry (a replayed
// nonce is not a funding source).
func paymentCreateFields(fields map[string]any) error {
	for k := range fields {
		switch k {
		case "id", "invoice_ids", "payment_method_id":
		default:
			return fmt.Errorf("payments: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// Create runs the payer's pay request through the module's own
// guarded flow: the invoices and the stored method are the payload,
// everything else (the amount, the charge, the intent, the settle) is
// the flow's own. A decline answers the operation's rejected result
// with the module's own words; a replay under the same key resolves
// through the stamped column, and a replay under a NEW key converges
// through the server-bound intent (the module's own double-charge
// guard: one charge, one settlement set).
func (s *PaymentsSyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := paymentScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := paymentCreateFields(fields); err != nil {
		return sync.RowState{}, err
	}
	rawIDs, _ := fields["invoice_ids"].([]any)
	if len(rawIDs) == 0 {
		return sync.RowState{}, fmt.Errorf("payments: at least one invoice is required: %w", sync.ErrOpRefused)
	}
	invoiceIDs := make([]string, 0, len(rawIDs))
	seen := map[string]bool{}
	for _, raw := range rawIDs {
		id, _ := raw.(string)
		if !paymentLooksLikeUUID(id) {
			return sync.RowState{}, fmt.Errorf("payments: every invoice id must be a uuid: %w", sync.ErrOpRefused)
		}
		if seen[id] {
			return sync.RowState{}, fmt.Errorf("payments: an invoice may be selected only once: %w", sync.ErrOpRefused)
		}
		seen[id] = true
		invoiceIDs = append(invoiceIDs, id)
	}
	methodID, _ := fields["payment_method_id"].(string)
	if !paymentLooksLikeUUID(methodID) {
		return sync.RowState{}, fmt.Errorf("payments: a stored payment method is required (a replayed nonce is not a funding source): %w", sync.ErrOpRefused)
	}
	rowID, hasID := fields["id"].(string)
	if hasID && !paymentLooksLikeUUID(rowID) {
		return sync.RowState{}, fmt.Errorf("payments: payload id must be a uuid: %w", sync.ErrOpRefused)
	}

	p, err := s.svc.PaySynced(ctx, f.TenantID, PayRequest{
		InvoiceIDs:      invoiceIDs,
		PaymentMethodID: methodID,
	}, rowID, key)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			return sync.RowState{}, sync.ErrRowNotFound
		case errors.Is(err, ErrDeclined), errors.Is(err, ErrChargePending), errors.Is(err, ErrPaymentInFlight):
			return sync.RowState{}, fmt.Errorf("payments: %w: %w", sync.ErrOpRefused, err)
		}
		return sync.RowState{}, fmt.Errorf("payments: %w: %w", sync.ErrOpRefused, err)
	}
	r, err := scanPaymentSync(s.pool.QueryRow(ctx, paymentSyncQuery+` WHERE p.id = $1::uuid`, p.ID))
	if err != nil {
		return sync.RowState{}, fmt.Errorf("payments: sync create read back: %w", err)
	}
	allocs, err := s.repo.allocationsByPayment(ctx, []string{r.pay.ID})
	if err != nil {
		return sync.RowState{}, err
	}
	return paymentStateOf(r, allocs[r.pay.ID])
}

// Write refuses: a settled payment's status moves are teller acts and
// its corrections are the reversal flow (a staff act with a reason);
// neither is a payer's device write, and the stale replay's outcome
// is the engine's own server_wins envelope.
func (s *PaymentsSyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	return sync.RowState{}, fmt.Errorf("payments: a settled payment has no client-declared field; corrections are the desk's reversals: %w", sync.ErrOpRefused)
}

// Delete refuses: a tombstone over a gateway-settled charge diverges
// the feed from the ledger; the reversal flow is the honest
// correction and it is the desk's.
func (s *PaymentsSyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	return sync.RowState{}, fmt.Errorf("payments: a payment is not deletable through sync; the ledger behind it stays: %w", sync.ErrOpRefused)
}

// paymentLooksLikeUUID reports whether s is a canonical 36-char uuid,
// the boundary guard every pushed id passes before it reaches a
// Postgres uuid column.
func paymentLooksLikeUUID(s string) bool {
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
