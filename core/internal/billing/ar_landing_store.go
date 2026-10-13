package billing

// ar_landing_store.go, the pgx half of the landing and its reminder machinery (0183).
//
// It is a SEPARATE type from Repository rather than more methods on it, for the reason the package
// already applies to StatementRepository: Repository is the invoice/ledger reader half a dozen
// surfaces share, and hanging the reminder writes off it would make every consumer of an invoice
// read a consumer of the send machinery too.
//
// EVERY READ HERE IS A PROJECTION, and none of them is a second way to read an invoice. The landing
// never edits money and must never become a back door to it.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ARLandingRepository implements ARLandingStore and ARReminderStore over pgx.
type ARLandingRepository struct {
	pool *pgxpool.Pool   // nil in scaffold/tests
	zone houseday.Source // the dealer's business day zone; nil = the Ontario dealer's
}

// WithZone binds the dealer's business day zone source. Nil keeps the Ontario dealer's.
func (r *ARLandingRepository) WithZone(src houseday.Source) *ARLandingRepository {
	r.zone = src
	return r
}

// NewARLandingRepository wires the pool.
func NewARLandingRepository(pool *pgxpool.Pool) *ARLandingRepository {
	return &ARLandingRepository{pool: pool}
}

// arLandingPolicyColumns is the canonical policy SELECT list, kept in one place so the read and the
// upsert cannot drift on column order.
const arLandingPolicyColumns = `aging_warn_days, aging_escalate_days, reminder_cadence_days,
	reminder_max_per_cycle, black_hole_window_days, black_hole_min_sends,
	reminder_email_enabled, reminder_sms_enabled, untagged_fallback, ar_tier`

// ReadARLandingPolicy resolves branch-over-dealer: the branch row if one exists, else the NULL row.
// ORDER BY branch_id NULLS LAST puts the branch row first, so LIMIT 1 IS the resolution, the 0043
// idiom, and the reason there is no second query and no merge to get wrong.
func (r *ARLandingRepository) ReadARLandingPolicy(ctx context.Context, branchID string) (ARLandingPolicy, error) {
	if r.pool == nil {
		return ARLandingPolicy{}, errNoDB
	}
	var p ARLandingPolicy
	err := r.pool.QueryRow(ctx, `
		SELECT `+arLandingPolicyColumns+`
		  FROM ar_landing_policy
		 WHERE branch_id IS NULL OR ($1 <> '' AND branch_id = $1::uuid)
		 ORDER BY branch_id NULLS LAST
		 LIMIT 1`, branchID).Scan(
		&p.AgingWarnDays, &p.AgingEscalateDays, &p.ReminderCadenceDays,
		&p.ReminderMaxPerCycle, &p.BlackHoleWindowDays, &p.BlackHoleMinSends,
		&p.ReminderEmailEnabled, &p.ReminderSMSEnabled, &p.UntaggedFallback, &p.Tier)
	if errors.Is(err, pgx.ErrNoRows) {
		return ARLandingPolicy{}, fmt.Errorf("billing: ar_landing_policy default row missing (0183 seed)")
	}
	if err != nil {
		return ARLandingPolicy{}, fmt.Errorf("billing: read ar landing policy: %w", err)
	}
	p.BranchID = branchID
	return p, nil
}

// UpsertARLandingPolicy writes the dealer row (branch ") or a branch override.
func (r *ARLandingRepository) UpsertARLandingPolicy(ctx context.Context, p ARLandingPolicy) error {
	if r.pool == nil {
		return errNoDB
	}
	args := []any{p.AgingWarnDays, p.AgingEscalateDays, p.ReminderCadenceDays, p.ReminderMaxPerCycle,
		p.BlackHoleWindowDays, p.BlackHoleMinSends, p.ReminderEmailEnabled, p.ReminderSMSEnabled,
		p.UntaggedFallback, p.Tier}
	if p.BranchID == "" {
		_, err := r.pool.Exec(ctx, `
			UPDATE ar_landing_policy
			   SET aging_warn_days=$1, aging_escalate_days=$2, reminder_cadence_days=$3,
			       reminder_max_per_cycle=$4, black_hole_window_days=$5, black_hole_min_sends=$6,
			       reminder_email_enabled=$7, reminder_sms_enabled=$8, untagged_fallback=$9,
			       ar_tier=$10, updated_at=now()
			 WHERE branch_id IS NULL`, args...)
		if err != nil {
			return fmt.Errorf("billing: write ar landing policy: %w", err)
		}
		return nil
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO ar_landing_policy (branch_id, aging_warn_days, aging_escalate_days,
		    reminder_cadence_days, reminder_max_per_cycle, black_hole_window_days,
		    black_hole_min_sends, reminder_email_enabled, reminder_sms_enabled,
		    untagged_fallback, ar_tier)
		VALUES ($11::uuid, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (branch_id) DO UPDATE SET
		    aging_warn_days=EXCLUDED.aging_warn_days,
		    aging_escalate_days=EXCLUDED.aging_escalate_days,
		    reminder_cadence_days=EXCLUDED.reminder_cadence_days,
		    reminder_max_per_cycle=EXCLUDED.reminder_max_per_cycle,
		    black_hole_window_days=EXCLUDED.black_hole_window_days,
		    black_hole_min_sends=EXCLUDED.black_hole_min_sends,
		    reminder_email_enabled=EXCLUDED.reminder_email_enabled,
		    reminder_sms_enabled=EXCLUDED.reminder_sms_enabled,
		    untagged_fallback=EXCLUDED.untagged_fallback,
		    ar_tier=EXCLUDED.ar_tier,
		    updated_at=now()`, append(args, p.BranchID)...)
	if err != nil {
		return fmt.Errorf("billing: write ar landing branch policy: %w", err)
	}
	return nil
}

// OpenInvoices projects the unpaid book. Same status set as the aging query next door
// (FINALIZED / PARTIALLY_PAID) so the landing and the register can never disagree about which
// invoices are open, two definitions of "open" is two totals and one very long meeting.
func (r *ARLandingRepository) OpenInvoices(ctx context.Context, asOf time.Time, branchID string, reach BranchReach) ([]OpenInvoice, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT i.id::text, i.invoice_no::text, c.id::text, c.name, c.account_no::text,
		       COALESCE(i.branch_id::text, ''), i.balance_cents, i.due_date
		  FROM invoices i
		  JOIN customers c ON c.id = i.customer_id
		 WHERE i.status IN ('FINALIZED','PARTIALLY_PAID')
		   AND i.balance_cents <> 0
		   AND ($1 = '' OR i.branch_id = $1::uuid)
		   AND ($2::uuid[] IS NULL OR i.branch_id = ANY($2::uuid[]))`, branchID, reach.SQLArg())
	if err != nil {
		return nil, fmt.Errorf("billing: open invoices: %w", err)
	}
	defer rows.Close()
	var out []OpenInvoice
	for rows.Next() {
		var iv OpenInvoice
		if err := rows.Scan(&iv.InvoiceID, &iv.InvoiceNo, &iv.CustomerID, &iv.CustomerName,
			&iv.AccountNo, &iv.BranchID, &iv.BalanceCents, &iv.DueDate); err != nil {
			return nil, fmt.Errorf("billing: scan open invoice: %w", err)
		}
		out = append(out, iv)
	}
	return out, rows.Err()
}

// ActiveHoldCustomers is the set of accounts currently stopped at the counter.
func (r *ARLandingRepository) ActiveHoldCustomers(ctx context.Context) (map[string]bool, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `SELECT DISTINCT customer_id::text FROM credit_holds WHERE status = 'ACTIVE'`)
	if err != nil {
		return nil, fmt.Errorf("billing: active holds: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// EngagementFor answers, per account, whether anything we sent has ever been opened.
//
// delivered_never_opened counts sends INSIDE the window; last_open_at is deliberately NOT windowed,
// because "this customer opened one in February" is a fact that should stop a paper-path flag no
// matter which window is configured today.
//
// last_reminder_at counts DELIVERED sends only (HH-185 round 2): the field drives the work list's
// cadence, and a FAILED send that advanced it would move the account from "Send reminder" to "Call"
// for a whole cadence period on a message that never left the server, the same phantom the
// DELIVERED filter already refuses to count.
func (r *ARLandingRepository) EngagementFor(ctx context.Context, customerIDs []string, windowDays int, now time.Time) (map[string]EngagementInput, error) {
	out := map[string]EngagementInput{}
	if r.pool == nil {
		return out, errNoDB
	}
	if len(customerIDs) == 0 {
		return out, nil
	}
	cutoff := now.Add(-time.Duration(windowDays) * 24 * time.Hour)
	rows, err := r.pool.Query(ctx, `
		SELECT c.id::text,
		       EXISTS (SELECT 1 FROM ar_ap_contacts a WHERE a.customer_id = c.id),
		       (SELECT max(s.sent_at)      FROM ar_reminder_sends s
		         WHERE s.customer_id = c.id AND s.status = 'DELIVERED'),
		       (SELECT max(s.first_open_at) FROM ar_reminder_sends s WHERE s.customer_id = c.id),
		       (SELECT count(*) FROM ar_reminder_sends s
		         WHERE s.customer_id = c.id AND s.status = 'DELIVERED'
		           AND s.first_open_at IS NULL AND s.sent_at >= $2),
		       EXISTS (SELECT 1 FROM ar_paper_path_flags f
		                WHERE f.customer_id = c.id AND f.status = 'OPEN')
		  FROM customers c
		 WHERE c.id = ANY($1::uuid[])`, customerIDs, cutoff)
	if err != nil {
		return out, fmt.Errorf("billing: engagement: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e EngagementInput
		var lastSend, lastOpen *time.Time
		if err := rows.Scan(&e.CustomerID, &e.APContactTagged, &lastSend, &lastOpen,
			&e.DeliveredNeverOpened, &e.PaperPathFlagged); err != nil {
			return out, fmt.Errorf("billing: scan engagement: %w", err)
		}
		e.LastReminderAt, e.LastOpenAt = lastSend, lastOpen
		out[e.CustomerID] = e
	}
	return out, rows.Err()
}

// OpenPaperPathFlags lists the flags waiting for a decision, oldest first, a queue nobody works
// from the bottom of. A flag is on an account, so it lists for a reach when the account has an open
// invoice in it, the cut the landing's own queue makes.
func (r *ARLandingRepository) OpenPaperPathFlags(ctx context.Context, reach BranchReach) ([]PaperPathEntry, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT f.id::text, c.id::text, c.name, c.account_no::text,
		       f.delivered_sends, f.window_days, f.raised_at
		  FROM ar_paper_path_flags f
		  JOIN customers c ON c.id = f.customer_id
		 WHERE f.status = 'OPEN'
		   AND ($1::uuid[] IS NULL OR EXISTS (
		         SELECT 1 FROM invoices i
		          WHERE i.customer_id = f.customer_id
		            AND i.status IN ('FINALIZED','PARTIALLY_PAID')
		            AND i.balance_cents <> 0
		            AND i.branch_id = ANY($1::uuid[])))
		 ORDER BY f.raised_at`, reach.SQLArg())
	if err != nil {
		return nil, fmt.Errorf("billing: paper path flags: %w", err)
	}
	defer rows.Close()
	var out []PaperPathEntry
	for rows.Next() {
		var e PaperPathEntry
		if err := rows.Scan(&e.FlagID, &e.CustomerID, &e.CustomerName, &e.AccountNo,
			&e.DeliveredSends, &e.WindowDays, &e.RaisedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// -- the reminder half --------------------------------------------------------------------------

// accountInReachSQL is the predicate "this account is one the branch reach touches": its home branch
// is in the reach, or it has an invoice in it. cust is the SQL expression for the account's id and p
// the placeholder of the reach array. A NULL reach (every branch) is true.
func accountInReachSQL(cust, p string) string {
	return "(" + p + "::uuid[] IS NULL OR EXISTS (SELECT 1 FROM customers ac WHERE ac.id = " + cust +
		" AND ac.home_branch_id = ANY(" + p + "::uuid[])) OR EXISTS (SELECT 1 FROM invoices ai WHERE ai.customer_id = " + cust +
		" AND ai.branch_id = ANY(" + p + "::uuid[])))"
}

// AccountInReach reports whether an account is one the branch reach touches (home branch or an
// invoice in it). The door every account level read or act goes through.
func (r *ARLandingRepository) AccountInReach(ctx context.Context, customerID string, reach BranchReach) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	var in bool
	if err := r.pool.QueryRow(ctx, `SELECT `+accountInReachSQL("$1::uuid", "$2")+` AND EXISTS (SELECT 1 FROM customers WHERE id = $1::uuid)`,
		customerID, reach.SQLArg()).Scan(&in); err != nil {
		return false, fmt.Errorf("billing: account in reach: %w", err)
	}
	return in, nil
}

// ReminderContextFor reads everything one send needs in one round trip.
//
// The candidate list is the account's OTHER addresses on this channel, and it exists only so
// ResolveTarget can REFUSE an ambiguous fallback. Nothing downstream iterates it to send.
func (r *ARLandingRepository) ReminderContextFor(ctx context.Context, customerID, invoiceID, channel string, reach BranchReach) (ReminderContext, error) {
	if r.pool == nil {
		return ReminderContext{}, errNoDB
	}
	zone, err := houseday.Resolve(ctx, r.zone)
	if err != nil {
		return ReminderContext{}, err
	}
	var rc ReminderContext
	// WITH NO INVOICE NAMED, THE OLDEST OPEN ONE IS THE SUBJECT. A reminder fired from the
	// escalation queue is about the ACCOUNT, and an account-level reminder that fell back to
	// CURRENT_DATE would record itself in the CURRENT bucket and print no watermark on a resend -
	// a notice about a 128-day-old invoice, filed as if nothing were late. The worst open invoice is
	// the one a collections conversation is actually about, so it is the one the message names.
	err = r.pool.QueryRow(ctx, `
		SELECT c.id::text, c.name, c.account_no::text,
		       COALESCE(i.invoice_no::text, ''), COALESCE(i.due_date, $3::date),
		       COALESCE(i.balance_cents, 0)
		  FROM customers c
		  LEFT JOIN LATERAL (
		       SELECT iv.invoice_no, iv.due_date, iv.balance_cents
		         FROM invoices iv
		        WHERE iv.customer_id = c.id
		          AND iv.status IN ('FINALIZED','PARTIALLY_PAID')
		          AND iv.balance_cents > 0
		          AND iv.doc_type = 'INVOICE'
		          AND ($2::uuid IS NULL OR iv.id = $2::uuid)
		          AND ($4::uuid[] IS NULL OR iv.branch_id = ANY($4::uuid[]))
		        ORDER BY iv.due_date
		        LIMIT 1
		  ) i ON TRUE
		 WHERE c.id = $1::uuid`, customerID, nullUUID(invoiceID), zone.Today(nil), reach.SQLArg()).
		Scan(&rc.CustomerID, &rc.CustomerName, &rc.AccountNo, &rc.InvoiceNo, &rc.DueDate, &rc.BalanceCents)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReminderContext{}, ErrNotFound
	}
	if err != nil {
		return ReminderContext{}, fmt.Errorf("billing: reminder context: %w", err)
	}
	if c, ok, err := r.ReadAPContact(ctx, customerID); err == nil && ok {
		rc.APContact = &c
	}
	col := "COALESCE(email::text, '')"
	if channel == ChannelSMS {
		col = "phone"
	}
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT `+col+` FROM customer_contacts
		 WHERE customer_id = $1::uuid AND is_active AND `+col+` <> ''`, customerID)
	if err != nil {
		return rc, fmt.Errorf("billing: reminder candidates: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return rc, err
		}
		rc.Candidates = append(rc.Candidates, a)
	}
	return rc, rows.Err()
}

const arSendColumns = `id::text, customer_id::text, COALESCE(invoice_id::text,''), channel,
	recipient, recipient_kind, fallback_reason, bucket, resend, watermark, status, provider,
	sent_at, delivered_at, first_open_at, first_click_at, open_count, click_count`

func scanReminderSend(row pgx.Row) (ReminderSend, error) {
	var s ReminderSend
	if err := row.Scan(&s.ID, &s.CustomerID, &s.InvoiceID, &s.Channel, &s.Recipient, &s.RecipientKind,
		&s.FallbackReason, &s.Bucket, &s.Resend, &s.Watermark, &s.Status, &s.Provider,
		&s.SentAt, &s.DeliveredAt, &s.FirstOpenAt, &s.FirstClickAt, &s.OpenCount, &s.ClickCount); err != nil {
		return ReminderSend{}, err
	}
	return s, nil
}

// FindSendByKey is the idempotency read: an identical retry finds its own row.
func (r *ARLandingRepository) FindSendByKey(ctx context.Context, key string) (ReminderSend, bool, error) {
	if r.pool == nil {
		return ReminderSend{}, false, errNoDB
	}
	s, err := scanReminderSend(r.pool.QueryRow(ctx,
		`SELECT `+arSendColumns+` FROM ar_reminder_sends WHERE idempotency_key = $1`, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return ReminderSend{}, false, nil
	}
	if err != nil {
		return ReminderSend{}, false, fmt.Errorf("billing: find send by key: %w", err)
	}
	return s, true, nil
}

// InsertSend writes the row BEFORE the transport is called, see ARReminderService.Send.
func (r *ARLandingRepository) InsertSend(ctx context.Context, s ReminderSend, trackToken string) (ReminderSend, error) {
	if r.pool == nil {
		return ReminderSend{}, errNoDB
	}
	var id string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO ar_reminder_sends (customer_id, invoice_id, channel, recipient, recipient_kind,
		    fallback_reason, bucket, resend, watermark, status, provider, track_token,
		    idempotency_key, sent_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id::text`,
		s.CustomerID, nullUUID(s.InvoiceID), s.Channel, s.Recipient, s.RecipientKind,
		s.FallbackReason, s.Bucket, s.Resend, s.Watermark, s.Status, s.Provider, trackToken,
		s.IdempotencyKey, s.SentAt).Scan(&id)
	if err != nil {
		return ReminderSend{}, fmt.Errorf("billing: insert send: %w", err)
	}
	s.ID = id
	return s, nil
}

// MarkSendResult stamps the transport's answer and appends the event.
func (r *ARLandingRepository) MarkSendResult(ctx context.Context, sendID, status, provider, providerRef, detail string, at time.Time) error {
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("billing: mark send: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE ar_reminder_sends
		   SET status = $2, provider = COALESCE(NULLIF($3,''), provider), provider_ref = $4,
		       delivered_at = CASE WHEN $2 = 'DELIVERED' THEN $5 ELSE delivered_at END
		 WHERE id = $1::uuid`, sendID, status, provider, providerRef, at); err != nil {
		return fmt.Errorf("billing: mark send: %w", err)
	}
	event := EventDelivered
	if status == SendFailed {
		event = EventFailed
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO ar_reminder_events (send_id, event, occurred_at, detail) VALUES ($1::uuid, $2, $3, $4)`,
		sendID, event, at, truncate(detail, 256)); err != nil {
		return fmt.Errorf("billing: send event: %w", err)
	}
	return tx.Commit(ctx)
}

// RecordTrackEvent appends an open/click and advances the counters in ONE transaction.
//
// The counter and the event row are written together because they are the same fact told twice, and
// a build where they can disagree is a build where the black-hole flag is unfalsifiable.
func (r *ARLandingRepository) RecordTrackEvent(ctx context.Context, token, event, userAgent string, at time.Time) (string, bool, error) {
	if r.pool == nil {
		return "", false, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var sendID string
	col, firstCol := "open_count", "first_open_at"
	if event == EventClick {
		col, firstCol = "click_count", "first_click_at"
	}
	err = tx.QueryRow(ctx, `
		UPDATE ar_reminder_sends
		   SET `+col+` = `+col+` + 1,
		       `+firstCol+` = COALESCE(`+firstCol+`, $2)
		 WHERE track_token = $1
		 RETURNING id::text`, token, at).Scan(&sendID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("billing: track event: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO ar_reminder_events (send_id, event, occurred_at, user_agent)
		 VALUES ($1::uuid, $2, $3, $4)`, sendID, event, at, truncate(userAgent, 256)); err != nil {
		return "", false, fmt.Errorf("billing: track event row: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", false, err
	}
	return sendID, true, nil
}

// ListSends is one account's reminder history, newest first.
func (r *ARLandingRepository) ListSends(ctx context.Context, customerID string, limit int, reach BranchReach) ([]ReminderSend, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+arSendColumns+` FROM ar_reminder_sends
		  WHERE ($1 = '' OR customer_id = $1::uuid)
		    AND `+accountInReachSQL("ar_reminder_sends.customer_id", "$3")+`
		  ORDER BY sent_at DESC LIMIT $2`, customerID, limit, reach.SQLArg())
	if err != nil {
		return nil, fmt.Errorf("billing: list sends: %w", err)
	}
	defer rows.Close()
	var out []ReminderSend
	for rows.Next() {
		s, err := scanReminderSend(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// EngagementWindows is the detector's input: delivered and opened counts per account inside the
// window, with the span of the sends that produced them.
func (r *ARLandingRepository) EngagementWindows(ctx context.Context, windowDays int, now time.Time) ([]EngagementWindow, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	cutoff := now.Add(-time.Duration(windowDays) * 24 * time.Hour)
	rows, err := r.pool.Query(ctx, `
		SELECT c.id::text, c.name, c.account_no::text,
		       count(*) FILTER (WHERE s.status = 'DELIVERED'),
		       count(*) FILTER (WHERE s.first_open_at IS NOT NULL),
		       min(s.sent_at), max(s.sent_at)
		  FROM ar_reminder_sends s
		  JOIN customers c ON c.id = s.customer_id
		 WHERE s.sent_at >= $1
		 GROUP BY c.id, c.name, c.account_no`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("billing: engagement windows: %w", err)
	}
	defer rows.Close()
	var out []EngagementWindow
	for rows.Next() {
		var e EngagementWindow
		if err := rows.Scan(&e.CustomerID, &e.CustomerName, &e.AccountNo,
			&e.DeliveredSends, &e.OpenedSends, &e.FirstSendAt, &e.LastSendAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RaisePaperPathFlag inserts one OPEN flag per account. The partial unique index makes a second
// nightly run a no-op rather than a pile, and the ON CONFLICT reports which happened.
func (r *ARLandingRepository) RaisePaperPathFlag(ctx context.Context, e EngagementWindow, windowDays int, now time.Time) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO ar_paper_path_flags (customer_id, delivered_sends, window_days,
		    first_send_at, last_send_at, raised_at)
		SELECT $1::uuid, $2, $3, $4, $5, $6
		 WHERE NOT EXISTS (SELECT 1 FROM ar_paper_path_flags
		                    WHERE customer_id = $1::uuid AND status = 'OPEN')`,
		e.CustomerID, e.DeliveredSends, windowDays, e.FirstSendAt, e.LastSendAt, now)
	if err != nil {
		return false, fmt.Errorf("billing: raise paper-path flag: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ResolvePaperPathFlag closes one flag with a person and a reason attached.
func (r *ARLandingRepository) ResolvePaperPathFlag(ctx context.Context, flagID, status, note, actorID string, at time.Time, reach BranchReach) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE ar_paper_path_flags
		   SET status = $2, resolution_note = $3, resolved_by = $4::uuid, resolved_at = $5
		 WHERE id = $1::uuid AND status = 'OPEN'
		   AND `+accountInReachSQL("ar_paper_path_flags.customer_id", "$6"), flagID, status, note, nullUUID(actorID), at, reach.SQLArg())
	if err != nil {
		return fmt.Errorf("billing: resolve paper-path flag: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ReadAPContact reads the tagged contact. Absent is not an error, see the handler.
func (r *ARLandingRepository) ReadAPContact(ctx context.Context, customerID string) (APContact, bool, error) {
	if r.pool == nil {
		return APContact{}, false, errNoDB
	}
	var c APContact
	var updated time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT customer_id::text, contact_name, email, phone, updated_at
		  FROM ar_ap_contacts WHERE customer_id = $1::uuid`, customerID).
		Scan(&c.CustomerID, &c.Name, &c.Email, &c.Phone, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return APContact{}, false, nil
	}
	if err != nil {
		return APContact{}, false, fmt.Errorf("billing: read ap contact: %w", err)
	}
	c.UpdatedAt = updated.Format(time.RFC3339)
	return c, true, nil
}

// UpsertAPContact tags (or re-tags) the account's AP contact.
func (r *ARLandingRepository) UpsertAPContact(ctx context.Context, c APContact, actorID string) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO ar_ap_contacts (customer_id, contact_name, email, phone, tagged_by)
		VALUES ($1::uuid, $2, $3, $4, $5::uuid)
		ON CONFLICT (customer_id) DO UPDATE SET
		    contact_name = EXCLUDED.contact_name, email = EXCLUDED.email,
		    phone = EXCLUDED.phone, tagged_by = EXCLUDED.tagged_by, updated_at = now()`,
		c.CustomerID, c.Name, c.Email, c.Phone, nullUUID(actorID))
	if err != nil {
		return fmt.Errorf("billing: tag ap contact: %w", err)
	}
	return nil
}

// DeleteAPContact untags. Sends already made keep their AP_CONTACT kind, see 0183.
func (r *ARLandingRepository) DeleteAPContact(ctx context.Context, customerID string) error {
	if r.pool == nil {
		return errNoDB
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM ar_ap_contacts WHERE customer_id = $1::uuid`, customerID); err != nil {
		return fmt.Errorf("billing: untag ap contact: %w", err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
