package customerquote

// The persistence layer over the two tables the module owns, customer_quote
// and quote_acceptance, and nothing else (the module fence). Scoped reads
// always carry their scope predicate: the tenant leg stamps tenant_id and
// org_id from the verified scope, and the public leg is scoped by the token
// match itself, which is the whole authorization decision on that leg. UUIDs
// cross the boundary as canonical lowercase text (the ::text casts), JSONB
// columns as raw bytes decoded by the service layer.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX is the shared query surface of a pool and a transaction. Only the
// Exec and QueryRow signatures coincide across both pgx types (Query differs
// in its error return), and the store needs nothing else.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store is the SQL layer.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore validates the pool and returns the store.
func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("customerquote: pool is required")
	}
	return &Store{pool: pool}, nil
}

// Pool exposes the pool for transaction begin.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// QuoteRow is one customer_quote row as the service layer sees it.
type QuoteRow struct {
	ID                  string
	OrgID               string
	TenantID            string
	Status              Status
	MarkupBps           int64
	LinesJSON           []byte
	LabourJSON          []byte
	OverheadJSON        []byte
	HideLinePrices      bool
	CustomerContactJSON []byte
	ShareTokenHash      *string
	ShareExpiresAt      *time.Time
	ValidUntil          *time.Time
	SupplierHorizon     *time.Time
	ViewCount           int64
	FirstViewedAt       *time.Time
	ChangeRequestsJSON  []byte
	CreatedAt           time.Time
	UpdatedAt           time.Time
	DeletedAt           *time.Time
}

const quoteCols = `id::text, org_id::text, tenant_id::text, status, markup_bps,
	lines, labour_lines, overhead_lines, hide_line_prices, customer_contact,
	share_token_hash, share_expires_at, valid_until, supplier_price_horizon,
	view_count, first_viewed_at, change_requests, created_at, updated_at, deleted_at`

func scanQuote(row pgx.Row) (QuoteRow, bool, error) {
	var q QuoteRow
	err := row.Scan(&q.ID, &q.OrgID, &q.TenantID, &q.Status, &q.MarkupBps,
		&q.LinesJSON, &q.LabourJSON, &q.OverheadJSON, &q.HideLinePrices, &q.CustomerContactJSON,
		&q.ShareTokenHash, &q.ShareExpiresAt, &q.ValidUntil, &q.SupplierHorizon,
		&q.ViewCount, &q.FirstViewedAt, &q.ChangeRequestsJSON, &q.CreatedAt, &q.UpdatedAt, &q.DeletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return QuoteRow{}, false, nil
	}
	if err != nil {
		return QuoteRow{}, false, fmt.Errorf("customerquote: quote read: %w", err)
	}
	return q, true, nil
}

// ErrQuoteNotFound names the tenant-scoped absence.
var ErrQuoteNotFound = errors.New("customerquote: quote not found")

// GetQuoteForTenant reads one quote under the tenant leg's scope predicate.
// A tombstoned row is absent on this leg.
func (s *Store) GetQuoteForTenant(ctx context.Context, q DBTX, tenantID, orgID, quoteID string) (QuoteRow, error) {
	row, ok, err := scanQuote(q.QueryRow(ctx,
		`SELECT `+quoteCols+` FROM customer_quote
		 WHERE id = $1 AND tenant_id = $2 AND org_id = $3 AND deleted_at IS NULL`,
		quoteID, tenantID, orgID))
	if err != nil {
		return QuoteRow{}, err
	}
	if !ok {
		return QuoteRow{}, ErrQuoteNotFound
	}
	return row, nil
}

// GetQuoteForOrg reads one quote under the dealer desk's org fence: the
// staff send's lookup, where the org is the whole scope the caller holds
// (ADR 0021, Decision 2: the send is a dealer action and the issuing
// contractor is on the row, not in the caller's scope). A tombstoned row is
// absent on this leg, exactly as on the tenant leg.
func (s *Store) GetQuoteForOrg(ctx context.Context, q DBTX, orgID, quoteID string) (QuoteRow, error) {
	row, ok, err := scanQuote(q.QueryRow(ctx,
		`SELECT `+quoteCols+` FROM customer_quote
		 WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL`,
		quoteID, orgID))
	if err != nil {
		return QuoteRow{}, err
	}
	if !ok {
		return QuoteRow{}, ErrQuoteNotFound
	}
	return row, nil
}

// GetQuoteLockTx reads one quote under the quote row lock, inside the
// acceptance transaction, with the tombstone visible (the public leg answers
// 410 on tombstoned quotes rather than not-found).
func (s *Store) GetQuoteLockTx(ctx context.Context, tx pgx.Tx, quoteID string) (QuoteRow, bool, error) {
	row, ok, err := scanQuote(tx.QueryRow(ctx,
		`SELECT `+quoteCols+` FROM customer_quote WHERE id = $1 FOR UPDATE`, quoteID))
	return row, ok, err
}

// ResolveByTokenHash fetches every candidate row whose stored token value
// contains the presented hash. The hash is 64 lowercase hex characters and
// carries no LIKE wildcard, and in a well-formed value every maximal hex run
// is exactly one 64-character atom, so containment is exact membership; the
// service layer parses every candidate under the grammar before believing
// any match, so an ill-formed stored value cannot win by substring luck.
// Under the frozen schema this probe is an unindexed scan (finding F2,
// routed to HH-3.7).
func (s *Store) ResolveByTokenHash(ctx context.Context, hash string) ([]QuoteRow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+quoteCols+` FROM customer_quote WHERE share_token_hash LIKE '%' || $1 || '%'`, hash)
	if err != nil {
		return nil, fmt.Errorf("customerquote: token resolve: %w", err)
	}
	defer rows.Close()
	var out []QuoteRow
	for rows.Next() {
		q := QuoteRow{}
		if err := rows.Scan(&q.ID, &q.OrgID, &q.TenantID, &q.Status, &q.MarkupBps,
			&q.LinesJSON, &q.LabourJSON, &q.OverheadJSON, &q.HideLinePrices, &q.CustomerContactJSON,
			&q.ShareTokenHash, &q.ShareExpiresAt, &q.ValidUntil, &q.SupplierHorizon,
			&q.ViewCount, &q.FirstViewedAt, &q.ChangeRequestsJSON, &q.CreatedAt, &q.UpdatedAt, &q.DeletedAt); err != nil {
			return nil, fmt.Errorf("customerquote: token resolve scan: %w", err)
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// InsertQuote creates a draft quote, tenant-stamped from the verified scope.
// The row's seq is drawn by the caller on the same transaction (Create), per
// the per-scope seq convention.
func (s *Store) InsertQuote(ctx context.Context, q DBTX, in QuoteRow, seq int64) (QuoteRow, error) {
	row, ok, err := scanQuote(q.QueryRow(ctx,
		`INSERT INTO customer_quote
		   (org_id, tenant_id, status, markup_bps, lines, labour_lines, overhead_lines,
		    hide_line_prices, customer_contact, valid_until, supplier_price_horizon, seq)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 RETURNING `+quoteCols,
		in.OrgID, in.TenantID, StatusDraft, in.MarkupBps, in.LinesJSON, in.LabourJSON, in.OverheadJSON,
		in.HideLinePrices, in.CustomerContactJSON, in.ValidUntil, in.SupplierHorizon, seq))
	if err != nil {
		return QuoteRow{}, err
	}
	if !ok {
		return QuoteRow{}, errors.New("customerquote: insert returned no row")
	}
	return row, nil
}

// UpdateDraftQuote writes the draft-state edits (R5: only a draft may be
// edited) with the scope predicate and the status guard in the statement
// itself, so a raced state change cannot be overwritten.
func (s *Store) UpdateDraftQuote(ctx context.Context, q DBTX, tenantID, orgID, quoteID string, in QuoteRow) (QuoteRow, error) {
	row, ok, err := scanQuote(q.QueryRow(ctx,
		`UPDATE customer_quote SET
		   markup_bps = $4, lines = $5, labour_lines = $6, overhead_lines = $7,
		   hide_line_prices = $8, customer_contact = $9, valid_until = $10,
		   supplier_price_horizon = $11, updated_at = now()
		 WHERE id = $1 AND tenant_id = $2 AND org_id = $3 AND status = 'draft' AND deleted_at IS NULL
		 RETURNING `+quoteCols,
		quoteID, tenantID, orgID, in.MarkupBps, in.LinesJSON, in.LabourJSON, in.OverheadJSON,
		in.HideLinePrices, in.CustomerContactJSON, in.ValidUntil, in.SupplierHorizon))
	if err != nil {
		return QuoteRow{}, err
	}
	if !ok {
		return QuoteRow{}, ErrQuoteNotFound
	}
	return row, nil
}

// RotateOnSend performs the send write under the row lock: retire whatever
// is live into the dead list, install the minted hash as live, re-freeze the
// presentation lines, reset the view counters, set the link bound to the
// offer bound, and move the status to sent (R4 and the send rotation).
func (s *Store) RotateOnSend(ctx context.Context, tx pgx.Tx, quoteID string, newState TokenState, linesJSON []byte, shareExpiresAt, validUntil time.Time) error {
	_, err := tx.Exec(ctx,
		`UPDATE customer_quote SET
		   share_token_hash = $2, share_expires_at = $3, valid_until = $4,
		   lines = $5, status = 'sent', view_count = 0, first_viewed_at = NULL,
		   updated_at = now()
		 WHERE id = $1`,
		quoteID, newState.Encode(), shareExpiresAt, validUntil, linesJSON)
	if err != nil {
		return fmt.Errorf("customerquote: send rotation: %w", err)
	}
	return nil
}

// RetireOnRevise performs the revise rotation under the row lock: the live
// hash moves to the dead list, the live slot empties, and the status returns
// to draft (the HH Pro rule: the token rotates the moment the intent to
// change forms, not at the eventual re-send).
func (s *Store) RetireOnRevise(ctx context.Context, tx pgx.Tx, quoteID string, newState TokenState) error {
	_, err := tx.Exec(ctx,
		`UPDATE customer_quote SET share_token_hash = $2, status = 'draft', updated_at = now()
		 WHERE id = $1`,
		quoteID, newState.Encode())
	if err != nil {
		return fmt.Errorf("customerquote: revise rotation: %w", err)
	}
	return nil
}

// TrackView advances the view counters, sent to viewed only (a responded
// quote keeps its status), and never advances updated_at: viewing is not a
// document change, and the public payload's as-of stamp is the document's.
// The write is best-effort and never denies the read (the carried rule for
// view logging).
func (s *Store) TrackView(ctx context.Context, quoteID string) {
	_, _ = s.pool.Exec(ctx,
		`UPDATE customer_quote SET
		   view_count = view_count + 1,
		   first_viewed_at = COALESCE(first_viewed_at, now()),
		   status = CASE WHEN status = 'sent' THEN 'viewed'::text ELSE status END
		 WHERE id = $1`, quoteID)
}

// AcceptanceRow is one quote_acceptance row as the service layer sees it.
type AcceptanceRow struct {
	ID                    string
	OrgID                 string
	TenantID              string
	CustomerQuoteID       string
	SignedName            string
	Affirmative           bool
	ConsentText           string
	AcceptedTotalCents    int64
	SignatureAttachmentID *string
	AcceptedAt            time.Time
}

const acceptanceCols = `id::text, org_id::text, tenant_id::text, customer_quote_id::text,
	signed_name, affirmative, consent_text, accepted_total_cents,
	signature_attachment_id::text, accepted_at`

func scanAcceptance(row pgx.Row) (AcceptanceRow, bool, error) {
	var a AcceptanceRow
	err := row.Scan(&a.ID, &a.OrgID, &a.TenantID, &a.CustomerQuoteID,
		&a.SignedName, &a.Affirmative, &a.ConsentText, &a.AcceptedTotalCents,
		&a.SignatureAttachmentID, &a.AcceptedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AcceptanceRow{}, false, nil
	}
	if err != nil {
		return AcceptanceRow{}, false, fmt.Errorf("customerquote: acceptance read: %w", err)
	}
	return a, true, nil
}

// InsertAcceptanceTx writes the acceptance row on the acceptance's own
// transaction. The id is generated here and returned so the artifact can
// name it; the row's seq was drawn by the caller on this same transaction.
func (s *Store) InsertAcceptanceTx(ctx context.Context, tx pgx.Tx, quote QuoteRow, signedName string, totalCents int64, acceptedAt time.Time, seq int64) (AcceptanceRow, error) {
	row, ok, err := scanAcceptance(tx.QueryRow(ctx,
		`INSERT INTO quote_acceptance
		   (org_id, tenant_id, customer_quote_id, signed_name, affirmative,
		    consent_text, accepted_total_cents, accepted_at, seq)
		 VALUES ($1, $2, $3, $4, TRUE, $5, $6, $7, $8)
		 RETURNING `+acceptanceCols,
		quote.OrgID, quote.TenantID, quote.ID, signedName, ConsentText, totalCents, acceptedAt, seq))
	if err != nil {
		return AcceptanceRow{}, err
	}
	if !ok {
		return AcceptanceRow{}, errors.New("customerquote: acceptance insert returned no row")
	}
	return row, nil
}

// FlipAcceptedTx flips the quote status to accepted on the acceptance's own
// transaction and re-tails the row's seq with a freshly drawn value, so the
// updated quote reappears in the customer_quote scope's pull cursor; the
// previous value is abandoned as a gap, which the convention allows.
func (s *Store) FlipAcceptedTx(ctx context.Context, tx pgx.Tx, quoteID string, seq int64) error {
	_, err := tx.Exec(ctx,
		`UPDATE customer_quote SET status = 'accepted', seq = $2, updated_at = now() WHERE id = $1`, quoteID, seq)
	if err != nil {
		return fmt.Errorf("customerquote: accept flip: %w", err)
	}
	return nil
}

// The sync scopes this module's two tables allocate seq under: one
// scope_sequences counter row per scope, named by the table, the same naming
// as the manifest scopes of the 0213 convention.
const (
	ScopeCustomerQuote   = "customer_quote"
	ScopeQuoteAcceptance = "quote_acceptance"
)

// AllocateSeqTx draws the next seq for one sync scope on the caller's
// business transaction, exactly the recipe the 0213 comment records: the
// scope_sequences upsert returning next_value, gaps allowed, never reused
// (the C34 per-scope monotonic convention, the D30 sync obligation for the
// plan-flow tables). scope_sequences is the platform's shared counter
// utility, not another module's business table: every core-owned write draws
// its row's seq from it inside the write's own transaction, which is why the
// upsert lives here, beside the writes it serves.
func (s *Store) AllocateSeqTx(ctx context.Context, tx pgx.Tx, scope string) (int64, error) {
	var next int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO scope_sequences (scope) VALUES ($1)
		 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
		 RETURNING next_value`, scope).Scan(&next); err != nil {
		return 0, fmt.Errorf("customerquote: seq allocate for scope %s: %w", scope, err)
	}
	return next, nil
}

// LoadAcceptance reads one acceptance row by id.
func (s *Store) LoadAcceptance(ctx context.Context, acceptanceID string) (AcceptanceRow, bool, error) {
	return scanAcceptance(s.pool.QueryRow(ctx,
		`SELECT `+acceptanceCols+` FROM quote_acceptance WHERE id = $1`, acceptanceID))
}

// AppendChangeRequestTx records one change request (R8) and moves the status
// to changes-requested under the row lock; the offer remains respondable.
func (s *Store) AppendChangeRequestTx(ctx context.Context, tx pgx.Tx, quoteID string, appended []byte) error {
	_, err := tx.Exec(ctx,
		`UPDATE customer_quote SET change_requests = $2, status = 'changes-requested', updated_at = now()
		 WHERE id = $1`, quoteID, appended)
	if err != nil {
		return fmt.Errorf("customerquote: change request: %w", err)
	}
	return nil
}
