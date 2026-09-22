package warranty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// repository_w5b.go, the pgx layer for Wave 5 Swarm B: per-outcome money paths (0129), the WTY-RECOVERY board
// (0130), public intake (0131) and SLA timers (0132). Same rules as repository.go: warranty-owned tables only
// (plus the read-only vendor/user joins the module already relies on for display names), money as BIGINT cents,
// and no arithmetic in Go that SQL already owns.

// - 0129: per-outcome money paths -

// OutcomePath reads the configured money path for an outcome. A missing (or deactivated) row is an ERROR, not a
// default, the module refuses to invent a money path for money.
func (r *Repository) OutcomePath(ctx context.Context, outcome string) (MoneyPathConfig, error) {
	if r.pool == nil {
		return MoneyPathConfig{}, errNoDB
	}
	var c MoneyPathConfig
	err := r.pool.QueryRow(ctx,
		`SELECT outcome, money_path, seam, COALESCE(debit_account_code::text,''),
		        COALESCE(credit_account_code::text,''), accrues_recovery, notes
		   FROM warranty_outcome_paths WHERE outcome = $1 AND is_active`, outcome).
		Scan(&c.Outcome, &c.MoneyPath, &c.Seam, &c.DebitAccount, &c.CreditAccount, &c.AccruesRecovery, &c.Notes)
	if errors.Is(err, pgx.ErrNoRows) {
		return MoneyPathConfig{}, unprocessable("warranty: no active money path is configured for outcome %s", outcome)
	}
	if err != nil {
		return MoneyPathConfig{}, fmt.Errorf("warranty: read outcome path %q: %w", outcome, err)
	}
	return c, nil
}

// InsertSettlementPosting records one executed money path, UPSERTing on (claim_id, money_path): a retried path
// updates its row rather than accruing a second time. The `request` payload is stored so a SEAM_UNWIRED row can
// be replayed once the union binds the adapter.
func (r *Repository) InsertSettlementPosting(ctx context.Context, p SettlementPosting) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	req := []byte("{}")
	if len(p.Request) > 0 {
		if b, err := json.Marshal(p.Request); err == nil {
			req = b
		}
	}
	var id string
	err := r.pool.QueryRow(ctx,
		`INSERT INTO warranty_settlement_postings
		   (claim_id, outcome, money_path, seam, status, amount_cents, accrues_recovery,
		    gl_journal_id, external_ref, request, error_text, note)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		 ON CONFLICT (claim_id, money_path) DO UPDATE SET
		   status = EXCLUDED.status, amount_cents = EXCLUDED.amount_cents,
		   accrues_recovery = EXCLUDED.accrues_recovery, gl_journal_id = EXCLUDED.gl_journal_id,
		   external_ref = EXCLUDED.external_ref, request = EXCLUDED.request,
		   error_text = EXCLUDED.error_text, note = EXCLUDED.note
		 RETURNING id::text`,
		p.ClaimID, p.Outcome, p.MoneyPath, p.Seam, p.Status, p.AmountCents, p.AccruesRecovery,
		nullUUID(p.GLJournalID), p.ExternalRef, req, p.ErrorText, p.Note).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("warranty: insert settlement posting: %w", err)
	}
	return id, nil
}

// PostingsForClaim returns the money-path rows for a claim, newest first.
func (r *Repository) PostingsForClaim(ctx context.Context, claimID string) ([]SettlementPosting, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if !isUUID(claimID) {
		return []SettlementPosting{}, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, claim_id::text, outcome, money_path, seam, status, amount_cents, accrues_recovery,
		        COALESCE(gl_journal_id::text,''), external_ref, request, error_text, note, posted_at
		   FROM warranty_settlement_postings WHERE claim_id = $1 ORDER BY posted_at DESC`, claimID)
	if err != nil {
		return nil, fmt.Errorf("warranty: read settlement postings: %w", err)
	}
	defer rows.Close()
	out := []SettlementPosting{}
	for rows.Next() {
		var p SettlementPosting
		var req []byte
		var posted time.Time
		if err := rows.Scan(&p.ID, &p.ClaimID, &p.Outcome, &p.MoneyPath, &p.Seam, &p.Status, &p.AmountCents,
			&p.AccruesRecovery, &p.GLJournalID, &p.ExternalRef, &req, &p.ErrorText, &p.Note, &posted); err != nil {
			return nil, fmt.Errorf("warranty: scan settlement posting: %w", err)
		}
		if len(req) > 0 {
			_ = json.Unmarshal(req, &p.Request)
		}
		p.PostedAt = posted.Format(time.RFC3339)
		out = append(out, p)
	}
	return out, rows.Err()
}

// - 0130: the recovery board -

// RecoveryRows reads the per-claim recovery projection straight off warranty_recovery_balances. The subtraction
// lives in the view: there is exactly one definition of outstanding and no second implementation here to drift
// away from it.
func (r *Repository) RecoveryRows(ctx context.Context, branchID, vendorID string, openOnly bool) ([]RecoveryRow, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT b.claim_id::text, b.claim_no::text, b.branch_id::text, COALESCE(b.vendor_id::text,''),
		        COALESCE(v.name,''), b.accrued_cents, b.received_cents, b.outstanding_cents, b.promised_cents,
		        b.accrued_at, b.last_action_at, COALESCE(b.action_count,0)
		   FROM warranty_recovery_balances b
		   LEFT JOIN vendors v ON v.id = b.vendor_id
		  WHERE ($1 = '' OR b.branch_id = $1::uuid)
		    AND ($2 = '' OR b.vendor_id = $2::uuid)
		    AND (NOT $3 OR b.outstanding_cents > 0)
		  ORDER BY b.accrued_at`, branchID, vendorID, openOnly)
	if err != nil {
		return nil, fmt.Errorf("warranty: read recovery balances: %w", err)
	}
	defer rows.Close()
	out := []RecoveryRow{}
	for rows.Next() {
		var rr RecoveryRow
		var accrued time.Time
		var last *time.Time
		if err := rows.Scan(&rr.ClaimID, &rr.ClaimNo, &rr.BranchID, &rr.VendorID, &rr.VendorName,
			&rr.AccruedCents, &rr.ReceivedCents, &rr.OutstandingCents, &rr.PromisedCents,
			&accrued, &last, &rr.ActionCount); err != nil {
			return nil, fmt.Errorf("warranty: scan recovery balance: %w", err)
		}
		rr.AccruedAt = accrued.Format(time.RFC3339)
		rr.LastActionAt = tsStr(last)
		out = append(out, rr)
	}
	return out, rows.Err()
}

// RecoveryBalanceForClaim reads ONE claim's position. A claim with no accrual returns a zero-value row (ClaimID
// ""), which the service reads as "this claim was never settled to the vendor-recovery path", a 422, not a 404.
func (r *Repository) RecoveryBalanceForClaim(ctx context.Context, claimID string) (RecoveryRow, error) {
	if r.pool == nil {
		return RecoveryRow{}, errNoDB
	}
	if !isUUID(claimID) {
		return RecoveryRow{}, nil
	}
	var rr RecoveryRow
	var accrued time.Time
	var last *time.Time
	err := r.pool.QueryRow(ctx,
		`SELECT b.claim_id::text, b.claim_no::text, b.branch_id::text, COALESCE(b.vendor_id::text,''),
		        b.accrued_cents, b.received_cents, b.outstanding_cents, b.promised_cents,
		        b.accrued_at, b.last_action_at, COALESCE(b.action_count,0)
		   FROM warranty_recovery_balances b WHERE b.claim_id = $1`, claimID).
		Scan(&rr.ClaimID, &rr.ClaimNo, &rr.BranchID, &rr.VendorID, &rr.AccruedCents, &rr.ReceivedCents,
			&rr.OutstandingCents, &rr.PromisedCents, &accrued, &last, &rr.ActionCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return RecoveryRow{}, nil
	}
	if err != nil {
		return RecoveryRow{}, fmt.Errorf("warranty: read recovery balance: %w", err)
	}
	rr.AccruedAt = accrued.Format(time.RFC3339)
	rr.LastActionAt = tsStr(last)
	return rr, nil
}

// InsertRecoveryAction appends one chase-log row.
func (r *Repository) InsertRecoveryAction(ctx context.Context, a RecoveryAction) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	occurred := time.Now()
	if a.OccurredAt != "" {
		if t, err := parseFlexTime(a.OccurredAt); err == nil {
			occurred = t
		}
	}
	var id string
	err := r.pool.QueryRow(ctx,
		`INSERT INTO warranty_recovery_actions
		   (claim_id, vendor_id, action, amount_cents, promised_date, reference, note, actor_id, occurred_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id::text`,
		a.ClaimID, nullUUID(a.VendorID), a.Action, a.AmountCents, nullDate(a.PromisedDate), a.Reference,
		a.Note, nullUUID(a.ActorID), occurred).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "23503" || pgErr.Code == "23514" || pgErr.Code == "22P02") {
			return "", unprocessable("warranty: the recovery action references an unknown claim/vendor, or violates the amount rule")
		}
		return "", fmt.Errorf("warranty: insert recovery action: %w", err)
	}
	return id, nil
}

// RecoveryActionsForClaim returns the chase log oldest-first (the order a person reads a history in).
func (r *Repository) RecoveryActionsForClaim(ctx context.Context, claimID string) ([]RecoveryAction, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if !isUUID(claimID) {
		return []RecoveryAction{}, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT a.id::text, a.claim_id::text, COALESCE(a.vendor_id::text,''), a.action, a.amount_cents,
		        a.promised_date, a.reference, a.note, COALESCE(a.actor_id::text,''), COALESCE(u.display_name,''),
		        a.occurred_at
		   FROM warranty_recovery_actions a
		   LEFT JOIN users u ON u.id = a.actor_id
		  WHERE a.claim_id = $1 ORDER BY a.occurred_at, a.created_at`, claimID)
	if err != nil {
		return nil, fmt.Errorf("warranty: read recovery actions: %w", err)
	}
	defer rows.Close()
	out := []RecoveryAction{}
	for rows.Next() {
		var a RecoveryAction
		var promised *time.Time
		var occurred time.Time
		if err := rows.Scan(&a.ID, &a.ClaimID, &a.VendorID, &a.Action, &a.AmountCents, &promised,
			&a.Reference, &a.Note, &a.ActorID, &a.ActorName, &occurred); err != nil {
			return nil, fmt.Errorf("warranty: scan recovery action: %w", err)
		}
		a.PromisedDate = dateStr(promised)
		a.OccurredAt = occurred.Format(time.RFC3339)
		out = append(out, a)
	}
	return out, rows.Err()
}

// - 0131: public intake -

// IntakeConfig reads the singleton. A missing row is a configuration error, not a set of invented defaults: the
// public door's limits must be the ones an operator can see in the table.
func (r *Repository) IntakeConfig(ctx context.Context) (IntakeConfig, error) {
	if r.pool == nil {
		return IntakeConfig{}, errNoDB
	}
	var c IntakeConfig
	err := r.pool.QueryRow(ctx,
		`SELECT is_enabled, max_body_bytes, max_photo_bytes, max_photos, window_seconds, max_per_window,
		        COALESCE(default_branch_id::text,'')
		   FROM warranty_intake_config WHERE id`).
		Scan(&c.IsEnabled, &c.MaxBodyBytes, &c.MaxPhotoBytes, &c.MaxPhotos, &c.WindowSeconds,
			&c.MaxPerWindow, &c.DefaultBranchID)
	if errors.Is(err, pgx.ErrNoRows) {
		return IntakeConfig{}, clientErr(503, "warranty: online intake is not configured")
	}
	if err != nil {
		return IntakeConfig{}, fmt.Errorf("warranty: read intake config: %w", err)
	}
	return c, nil
}

// IntakeRateLimitHit atomically increments the (bucket, window) counter and returns the POST-increment count.
// One statement: a read-then-write limiter is not a limiter under concurrency.
func (r *Repository) IntakeRateLimitHit(ctx context.Context, bucketKey string, windowStart time.Time) (int, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var hits int
	err := r.pool.QueryRow(ctx,
		`INSERT INTO warranty_intake_rate_limit (bucket_key, window_start, hits)
		 VALUES ($1,$2,1)
		 ON CONFLICT (bucket_key, window_start)
		 DO UPDATE SET hits = warranty_intake_rate_limit.hits + 1, updated_at = now()
		 RETURNING hits`, bucketKey, windowStart).Scan(&hits)
	if err != nil {
		return 0, fmt.Errorf("warranty: intake rate-limit hit: %w", err)
	}
	return hits, nil
}

// InsertPublicClaim is the ONE write the public doorway performs: claim + lines + photos, one transaction.
//
// It is a separate method from InsertClaim on purpose. InsertClaim takes a ClaimInput carrying customer_id,
// vendor_id, owner_id, invoice_id, order_id, po_id and expected_cents, every one of which is a foreign key an
// unauthenticated body must never be able to choose. This method's signature simply has nowhere to put them:
// the public path cannot set a field it cannot name.
func (r *Repository) InsertPublicClaim(ctx context.Context, in PublicIntakeInput, claimNo, publicRef, branchID string, photos []IntakePhoto, openedAt time.Time) (Claim, error) {
	if r.pool == nil {
		return Claim{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Claim{}, fmt.Errorf("warranty: begin public intake: %w", err)
	}
	defer tx.Rollback(ctx)

	// Same advisory lock the staff capture path takes, so a web submission and a counter capture landing in the
	// same millisecond get sequential #WTY-n numbers instead of one of them 500ing on the UNIQUE constraint.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, warrantyClaimSeqLock); err != nil {
		return Claim{}, fmt.Errorf("warranty: claim-seq advisory lock: %w", err)
	}
	if claimNo == "" {
		var next int
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(NULLIF(regexp_replace(claim_no::text, '\D', '', 'g'), '')::int), 0) + 1
			 FROM warranty_claims`).Scan(&next); err != nil {
			return Claim{}, fmt.Errorf("warranty: next claim seq: %w", err)
		}
		claimNo = fmt.Sprintf("WTY-%d", next)
	}

	addr, _ := json.Marshal(in.JobAddress)
	contact, _ := json.Marshal(in.Contact)
	var id string
	err = tx.QueryRow(ctx,
		`INSERT INTO warranty_claims
		   (claim_no, public_ref, job_address, intake_contact, branch_id, phase, source, description,
		    supplier_purchase_date, opened_at)
		 VALUES ($1,$2,$3,$4,$5,'INTAKE','WEB',$6,$7,$8)
		 RETURNING id::text`,
		claimNo, publicRef, addr, contact, branchID, in.Description,
		nullDate(in.PurchasedOn), openedAt).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "23503" || pgErr.Code == "23502" || pgErr.Code == "22P02") {
			return Claim{}, clientErr(503, "warranty: online intake is not configured correctly, please contact the branch")
		}
		return Claim{}, fmt.Errorf("warranty: insert public claim: %w", err)
	}

	for _, l := range in.Lines {
		// product_id is deliberately absent: a public body never chooses a catalogue row. Triage links it.
		//
		// QTY IS FREE TEXT AND THE COLUMN IS NOT. The form asks "how much?" and suggests "180 sq ft", because
		// that is how a homeowner knows the answer; qty is DECIMAL(19,4). nullDecimal takes the leading number
		// and publicLineNotes KEEPS THE WORDS, so "180 sq ft" becomes 180 with "quantity as submitted: 180 sq
		// ft" on the line. Throwing the units away would leave a triager guessing between square feet and
		// pallets, and passing the raw string through was a 500 on an unauthenticated form.
		if _, lerr := tx.Exec(ctx,
			`INSERT INTO warranty_claim_lines (claim_id, product_text, qty, notes) VALUES ($1,$2,$3,$4)`,
			id, l.ProductText, nullDecimal(l.Qty), publicLineNotes(l)); lerr != nil {
			return Claim{}, fmt.Errorf("warranty: insert public claim line: %w", lerr)
		}
	}
	for _, p := range photos {
		if _, perr := tx.Exec(ctx,
			`INSERT INTO warranty_intake_photos (claim_id, content_type, byte_size, image_bytes, caption, checksum)
			 VALUES ($1,$2,$3,$4,$5,$6)`,
			id, p.ContentType, p.ByteSize, p.Bytes, p.Caption, p.Checksum); perr != nil {
			return Claim{}, fmt.Errorf("warranty: insert intake photo: %w", perr)
		}
	}
	// The CREATED audit-spine event rides the SAME tx: a web claim always carries its origin event, and a
	// rolled-back submission leaves neither. actor_id is NULL, there was no actor, and inventing one (a
	// "system" user) would put a fictional name on a real audit trail.
	if _, eerr := tx.Exec(ctx,
		`INSERT INTO warranty_claim_events (claim_id, event_type, to_phase, note, detail, occurred_at)
		 VALUES ($1,'CREATED','INTAKE',$2,$3,$4)`,
		id, "Claim submitted through the public warranty form.",
		[]byte(`{"source":"WEB","channel":"public_intake"}`), openedAt); eerr != nil {
		return Claim{}, fmt.Errorf("warranty: insert public intake event: %w", eerr)
	}

	if err := tx.Commit(ctx); err != nil {
		return Claim{}, fmt.Errorf("warranty: commit public intake: %w", err)
	}
	return r.GetClaim(ctx, id)
}

// IntakePhotosForClaim lists photo METADATA (never bytes) for the claim card.
func (r *Repository) IntakePhotosForClaim(ctx context.Context, claimID string) ([]IntakePhoto, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if !isUUID(claimID) {
		return []IntakePhoto{}, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, claim_id::text, content_type, byte_size, caption, checksum, uploaded_at
		   FROM warranty_intake_photos WHERE claim_id = $1 ORDER BY uploaded_at`, claimID)
	if err != nil {
		return nil, fmt.Errorf("warranty: read intake photos: %w", err)
	}
	defer rows.Close()
	out := []IntakePhoto{}
	for rows.Next() {
		var p IntakePhoto
		var up time.Time
		if err := rows.Scan(&p.ID, &p.ClaimID, &p.ContentType, &p.ByteSize, &p.Caption, &p.Checksum, &up); err != nil {
			return nil, fmt.Errorf("warranty: scan intake photo: %w", err)
		}
		p.UploadedAt = up.Format(time.RFC3339)
		out = append(out, p)
	}
	return out, rows.Err()
}

// IntakePhoto reads one photo's bytes. The claim id is part of the WHERE, not just the authorization check, so a
// photo id from another claim cannot be fetched under a claim the caller happens to be allowed to see.
func (r *Repository) IntakePhoto(ctx context.Context, claimID, photoID string) (IntakePhoto, error) {
	if r.pool == nil {
		return IntakePhoto{}, errNoDB
	}
	if !isUUID(claimID) || !isUUID(photoID) {
		return IntakePhoto{}, notFound("warranty: photo not found")
	}
	var p IntakePhoto
	var up time.Time
	err := r.pool.QueryRow(ctx,
		`SELECT id::text, claim_id::text, content_type, byte_size, caption, checksum, uploaded_at, image_bytes
		   FROM warranty_intake_photos WHERE id = $1 AND claim_id = $2`, photoID, claimID).
		Scan(&p.ID, &p.ClaimID, &p.ContentType, &p.ByteSize, &p.Caption, &p.Checksum, &up, &p.Bytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return IntakePhoto{}, notFound("warranty: photo not found")
	}
	if err != nil {
		return IntakePhoto{}, fmt.Errorf("warranty: read intake photo: %w", err)
	}
	p.UploadedAt = up.Format(time.RFC3339)
	return p, nil
}

// - 0132: SLA -

// SLATargets reads every configured target (defaults + vendor overrides).
func (r *Repository) SLATargets(ctx context.Context) ([]SLATarget, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT s.id::text, s.phase, COALESCE(s.vendor_id::text,''), COALESCE(v.name,''),
		        s.target_hours, s.is_active, s.notes
		   FROM warranty_sla_config s LEFT JOIN vendors v ON v.id = s.vendor_id
		  ORDER BY s.phase, COALESCE(v.name,'')`)
	if err != nil {
		return nil, fmt.Errorf("warranty: read sla config: %w", err)
	}
	defer rows.Close()
	out := []SLATarget{}
	for rows.Next() {
		var t SLATarget
		if err := rows.Scan(&t.ID, &t.Phase, &t.VendorID, &t.VendorName, &t.TargetHours, &t.IsActive, &t.Notes); err != nil {
			return nil, fmt.Errorf("warranty: scan sla config: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// OpenClaimPhaseClocks returns every OPEN claim with the moment it entered its CURRENT phase.
//
// The entry time is DERIVED from the audit spine, the latest PHASE_CHANGE whose to_phase equals the current
// phase, and falls back to opened_at for a claim that has never moved (still in INTAKE). A denormalized
// phase_since column would be a second source of truth that a BACK-DATED transition (FR-9) could put out of
// step with the very events that justify it.
func (r *Repository) OpenClaimPhaseClocks(ctx context.Context, branchID string) ([]PhaseClock, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT c.id::text, c.claim_no::text, c.branch_id::text, COALESCE(c.vendor_id::text,''),
		        COALESCE(v.name,''), COALESCE(c.owner_id::text,''), c.phase, c.expected_cents,
		        COALESCE(
		          (SELECT MAX(e.occurred_at) FROM warranty_claim_events e
		            WHERE e.claim_id = c.id AND e.event_type = 'PHASE_CHANGE' AND e.to_phase = c.phase),
		          c.opened_at)
		   FROM warranty_claims c LEFT JOIN vendors v ON v.id = c.vendor_id
		  WHERE c.phase NOT IN ('CLOSED','REJECTED','ABANDONED')
		    AND ($1 = '' OR c.branch_id = $1::uuid)
		  ORDER BY c.claim_no`, branchID)
	if err != nil {
		return nil, fmt.Errorf("warranty: read phase clocks: %w", err)
	}
	defer rows.Close()
	out := []PhaseClock{}
	for rows.Next() {
		var c PhaseClock
		if err := rows.Scan(&c.ClaimID, &c.ClaimNo, &c.BranchID, &c.VendorID, &c.VendorName, &c.OwnerID,
			&c.Phase, &c.ExpectedCents, &c.PhaseSince); err != nil {
			return nil, fmt.Errorf("warranty: scan phase clock: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpsertSLAAlert raises (or refreshes) the alert for a (claim, phase).
//
// An UPDATE deliberately does NOT clear an acknowledgement: a claim that was acknowledged as overdue and is
// still overdue an hour later has not become a new problem, and re-raising it would train people to dismiss the
// list. It DOES advance overdue_hours, so an acknowledged alert still shows how far it has slipped.
func (r *Repository) UpsertSLAAlert(ctx context.Context, a SLAAlert) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	due, err := parseFlexTime(a.DueAt)
	if err != nil {
		return "", unprocessable("warranty: alert due_at must be an ISO-8601 timestamp")
	}
	breached := time.Now()
	if a.BreachedAt != "" {
		if t, perr := parseFlexTime(a.BreachedAt); perr == nil {
			breached = t
		}
	}
	var id string
	err = r.pool.QueryRow(ctx,
		`INSERT INTO warranty_sla_alerts (claim_id, phase, due_at, breached_at, overdue_hours, notify_status)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (claim_id, phase) DO UPDATE SET
		   due_at = EXCLUDED.due_at, overdue_hours = EXCLUDED.overdue_hours, updated_at = now()
		 RETURNING id::text`,
		a.ClaimID, a.Phase, due, breached, a.OverdueHours, NotifySuppressed).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("warranty: upsert sla alert: %w", err)
	}
	return id, nil
}

// SLAAlerts lists alerts for a branch scope, worst-first.
func (r *Repository) SLAAlerts(ctx context.Context, branchID string, includeAcknowledged bool) ([]SLAAlert, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT a.id::text, a.claim_id::text, c.claim_no::text, c.branch_id::text, COALESCE(v.name,''),
		        COALESCE(c.owner_id::text,''), a.phase, a.due_at, a.breached_at, a.overdue_hours,
		        a.acknowledged_at, COALESCE(a.acknowledged_by::text,''), a.notify_status, a.notify_ref,
		        a.notify_error, c.expected_cents
		   FROM warranty_sla_alerts a
		   JOIN warranty_claims c ON c.id = a.claim_id
		   LEFT JOIN vendors v ON v.id = c.vendor_id
		  WHERE ($1 = '' OR c.branch_id = $1::uuid)
		    AND ($2 OR a.acknowledged_at IS NULL)
		  ORDER BY a.overdue_hours DESC, c.claim_no`, branchID, includeAcknowledged)
	if err != nil {
		return nil, fmt.Errorf("warranty: read sla alerts: %w", err)
	}
	defer rows.Close()
	out := []SLAAlert{}
	for rows.Next() {
		var a SLAAlert
		var due, breached time.Time
		var acked *time.Time
		if err := rows.Scan(&a.ID, &a.ClaimID, &a.ClaimNo, &a.BranchID, &a.VendorName, &a.OwnerID, &a.Phase,
			&due, &breached, &a.OverdueHours, &acked, &a.AcknowledgedBy, &a.NotifyStatus, &a.NotifyRef,
			&a.NotifyError, &a.ExposureCents); err != nil {
			return nil, fmt.Errorf("warranty: scan sla alert: %w", err)
		}
		a.DueAt = due.Format(time.RFC3339)
		a.BreachedAt = breached.Format(time.RFC3339)
		a.AcknowledgedAt = tsStr(acked)
		out = append(out, a)
	}
	return out, rows.Err()
}

// AcknowledgeSLAAlert stamps who saw it. Idempotent: re-acknowledging keeps the FIRST acknowledgement, because
// "who saw this first" is the fact worth keeping.
func (r *Repository) AcknowledgeSLAAlert(ctx context.Context, alertID, userID string) error {
	if r.pool == nil {
		return errNoDB
	}
	if !isUUID(alertID) {
		return notFound("warranty: alert not found")
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE warranty_sla_alerts
		    SET acknowledged_at = now(), acknowledged_by = NULLIF($2,'')::uuid, updated_at = now()
		  WHERE id = $1 AND acknowledged_at IS NULL`, alertID, userID)
	if err != nil {
		return fmt.Errorf("warranty: acknowledge sla alert: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either already acknowledged (fine, idempotent) or absent. Distinguish so a bad id is a 404.
		var exists bool
		if qerr := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM warranty_sla_alerts WHERE id = $1)`, alertID).Scan(&exists); qerr == nil && !exists {
			return notFound("warranty: alert not found")
		}
	}
	return nil
}

// MarkSLANotified records the outcome of the notification attempt.
func (r *Repository) MarkSLANotified(ctx context.Context, alertID, status, ref, errText string) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE warranty_sla_alerts SET notify_status = $2, notify_ref = $3, notify_error = $4, updated_at = now()
		  WHERE id = $1`, alertID, status, ref, errText)
	if err != nil {
		return fmt.Errorf("warranty: mark sla notified: %w", err)
	}
	return nil
}
