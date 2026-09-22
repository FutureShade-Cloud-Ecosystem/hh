package warranty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// repository.go, the warranty pgx access layer over the 0053/0054 warranty_* tables. Money is BIGINT *_cents;
// nullable ids/dates pass through the null* helpers (a non-UUID actor label is recorded as NULL rather than a
// write error, mirroring internal/lien). Every method touches ONLY warranty-owned tables (zero-trust).

// - null helpers (mirror internal/lien) -

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullUUID(s string) any {
	if !isUUID(s) {
		return nil
	}
	return s
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func nullDate(s string) any {
	if s == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return nil
	}
	return s
}

func tsStr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

func dateStr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

// - FR-1 vendor registry -

func (r *Repository) UpsertVendorProfile(ctx context.Context, in VendorProfileInput, createdBy string) (VendorProfile, error) {
	if r.pool == nil {
		return VendorProfile{}, errNoDB
	}
	docs, _ := json.Marshal(in.Docs)
	if in.Docs == nil {
		docs = []byte("[]")
	}
	var id string
	var created, updated time.Time
	// ON CONFLICT (vendor_id) UPDATE, one profile per vendor; a re-PUT edits it in place.
	err := r.pool.QueryRow(ctx,
		`INSERT INTO warranty_vendor_profiles
		   (vendor_id, status, coverage, term_months, channel, channel_ref, docs, notes, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 ON CONFLICT (vendor_id) DO UPDATE SET
		   status = EXCLUDED.status, coverage = EXCLUDED.coverage, term_months = EXCLUDED.term_months,
		   channel = EXCLUDED.channel, channel_ref = EXCLUDED.channel_ref, docs = EXCLUDED.docs,
		   notes = EXCLUDED.notes, updated_at = now()
		 RETURNING id::text, created_at, updated_at`,
		in.VendorID, in.Status, in.Coverage, in.TermMonths, in.Channel, in.ChannelRef, docs, in.Notes, nullUUID(createdBy),
	).Scan(&id, &created, &updated)
	if err != nil {
		// A vendor_id that is not a real vendor (empty registry on staging, a typo'd id, or a non-UUID) trips
		// the vendor_id FK (23503). That is a CLIENT error (unknown vendor), not a 500, map it to a 422 so the
		// PUT reports "unknown vendor" instead of a raw internal error (the demo-gap PUT-500 root cause).
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "23503" || pgErr.Code == "22P02") {
			// 23503 foreign_key_violation (id is a well-formed UUID but no such vendor); 22P02
			// invalid_text_representation (the path segment isn't even a UUID).
			return VendorProfile{}, unprocessable("warranty: unknown vendor %q, create the vendor before configuring its warranty profile", in.VendorID)
		}
		return VendorProfile{}, fmt.Errorf("warranty: upsert vendor profile: %w", err)
	}
	return r.GetVendorProfileByVendor(ctx, in.VendorID)
}

const profileCols = `wp.id::text, wp.vendor_id::text, COALESCE(v.name,''), COALESCE(v.vendor_no::text,''),
	wp.status, wp.coverage, wp.term_months, wp.channel, wp.channel_ref, wp.docs, wp.notes,
	wp.created_at, wp.updated_at`

func scanProfile(row pgx.Row) (VendorProfile, error) {
	var p VendorProfile
	var term *int
	var docs []byte
	var created, updated time.Time
	if err := row.Scan(&p.ID, &p.VendorID, &p.VendorName, &p.VendorNo, &p.Status, &p.Coverage, &term,
		&p.Channel, &p.ChannelRef, &docs, &p.Notes, &created, &updated); err != nil {
		return VendorProfile{}, err
	}
	p.TermMonths = term
	if len(docs) > 0 {
		_ = json.Unmarshal(docs, &p.Docs)
	}
	if p.Docs == nil {
		p.Docs = []DocRef{}
	}
	p.CreatedAt = created.Format(time.RFC3339)
	p.UpdatedAt = updated.Format(time.RFC3339)
	return p, nil
}

func (r *Repository) GetVendorProfileByVendor(ctx context.Context, vendorID string) (VendorProfile, error) {
	if r.pool == nil {
		return VendorProfile{}, errNoDB
	}
	if !isUUID(vendorID) {
		return VendorProfile{}, errProfileNotFound
	}
	p, err := scanProfile(r.pool.QueryRow(ctx,
		`SELECT `+profileCols+` FROM warranty_vendor_profiles wp
		 JOIN vendors v ON v.id = wp.vendor_id WHERE wp.vendor_id = $1`, vendorID))
	if err != nil {
		if err == pgx.ErrNoRows {
			return VendorProfile{}, errProfileNotFound
		}
		return VendorProfile{}, fmt.Errorf("warranty: get vendor profile: %w", err)
	}
	return p, nil
}

func (r *Repository) ListVendorProfiles(ctx context.Context) ([]VendorProfile, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+profileCols+` FROM warranty_vendor_profiles wp
		 JOIN vendors v ON v.id = wp.vendor_id ORDER BY v.name`)
	if err != nil {
		return nil, fmt.Errorf("warranty: list vendor profiles: %w", err)
	}
	defer rows.Close()
	var out []VendorProfile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, fmt.Errorf("warranty: scan profile: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repository) AddProductOverride(ctx context.Context, in ProductOverrideInput) (ProductOverride, error) {
	if r.pool == nil {
		return ProductOverride{}, errNoDB
	}
	var id string
	var created time.Time
	err := r.pool.QueryRow(ctx,
		`INSERT INTO warranty_product_overrides (profile_id, product_id, coverage, term_months, notes)
		 VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (profile_id, product_id) DO UPDATE SET
		   coverage = EXCLUDED.coverage, term_months = EXCLUDED.term_months, notes = EXCLUDED.notes
		 RETURNING id::text, created_at`,
		in.ProfileID, in.ProductID, in.Coverage, in.TermMonths, in.Notes,
	).Scan(&id, &created)
	if err != nil {
		return ProductOverride{}, fmt.Errorf("warranty: add product override: %w", err)
	}
	return ProductOverride{
		ID: id, ProfileID: in.ProfileID, ProductID: in.ProductID, Coverage: in.Coverage,
		TermMonths: in.TermMonths, Notes: in.Notes, CreatedAt: created.Format(time.RFC3339),
	}, nil
}

func (r *Repository) OverridesForProfile(ctx context.Context, profileID string) ([]ProductOverride, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT o.id::text, o.profile_id::text, o.product_id::text, COALESCE(p.name,''), o.coverage,
		        o.term_months, o.notes, o.created_at
		 FROM warranty_product_overrides o
		 LEFT JOIN products p ON p.id = o.product_id
		 WHERE o.profile_id = $1 ORDER BY p.name`, profileID)
	if err != nil {
		return nil, fmt.Errorf("warranty: overrides for profile: %w", err)
	}
	defer rows.Close()
	var out []ProductOverride
	for rows.Next() {
		var o ProductOverride
		var term *int
		var created time.Time
		if err := rows.Scan(&o.ID, &o.ProfileID, &o.ProductID, &o.ProductName, &o.Coverage, &term, &o.Notes, &created); err != nil {
			return nil, fmt.Errorf("warranty: scan override: %w", err)
		}
		o.TermMonths = term
		o.CreatedAt = created.Format(time.RFC3339)
		out = append(out, o)
	}
	return out, rows.Err()
}

// - FR-2 claims -

// warrantyClaimSeqLock is a fixed advisory-lock key so concurrent claim captures SERIALIZE the "read MAX(#WTY-n)
// → INSERT next" step. Without it, two simultaneous captures both read the same MAX, both compute the same next
// number, and the second hits the claim_no UNIQUE constraint and 500s (a lost capture). The lock is held for the
// txn only; the CITEXT UNIQUE remains the correctness backstop.
const warrantyClaimSeqLock = int64(0x57545930315345) // "WTY01SE"

// InsertClaim assigns the next #WTY-n and inserts the claim (+ lines) in ONE transaction under an advisory lock,
// so claim numbers are gap-free and never collide under concurrent capture (MAJOR-1). When claimNo is non-empty
// it is honoured (the seed/migration path supplies its own); otherwise the number is assigned inside the tx.
func (r *Repository) InsertClaim(ctx context.Context, in ClaimInput, claimNo string, openedAt time.Time, createdBy string) (Claim, error) {
	if r.pool == nil {
		return Claim{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Claim{}, fmt.Errorf("warranty: begin insert claim: %w", err)
	}
	defer tx.Rollback(ctx)

	// Serialize claim-number assignment across concurrent captures (held to COMMIT).
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
	var id string
	var created time.Time
	err = tx.QueryRow(ctx,
		`INSERT INTO warranty_claims
		   (claim_no, customer_id, job_address, vendor_id, owner_id, branch_id, phase, source, description,
		    invoice_id, order_id, po_id, supplier_purchase_date, expected_cents, opened_at, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,'INTAKE',$7,$8,$9,$10,$11,$12,$13,$14,$15)
		 RETURNING id::text, created_at`,
		claimNo, nullUUID(in.CustomerID), addr, nullUUID(in.VendorID), nullUUID(in.OwnerID), in.BranchID,
		in.Source, in.Description, nullUUID(in.InvoiceID), nullUUID(in.OrderID), nullUUID(in.POID),
		nullDate(in.SupplierPurchaseDate), in.ExpectedCents, openedAt, nullUUID(createdBy),
	).Scan(&id, &created)
	if err != nil {
		// A well-formed but non-existent customer_id/vendor_id/owner_id/invoice_id/order_id/po_id/branch_id trips
		// its FK (23503), an unknown master, i.e. a CLIENT error, not a 500. (branch_id is NOT NULL, so a bad one
		// can also surface as 23502; a non-UUID branch_id is 22P02.) Map them to 422, mirroring the sibling
		// UpsertVendorProfile path, so capture reports "unknown reference" rather than a raw internal error.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "23503" || pgErr.Code == "23502" || pgErr.Code == "22P02") {
			return Claim{}, unprocessable("warranty: capture references an unknown customer/vendor/owner/invoice/order/po/branch, create it first")
		}
		return Claim{}, fmt.Errorf("warranty: insert claim: %w", err)
	}
	// Lines.
	for _, l := range in.Lines {
		if _, lerr := tx.Exec(ctx,
			`INSERT INTO warranty_claim_lines
			   (claim_id, product_id, product_text, qty, uom_code, purchase_date_supplier, purchase_date_contractor, notes)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			id, nullUUID(l.ProductID), l.ProductText, nullDecimal(l.Qty), nullStr(l.UOMCode),
			nullDate(l.PurchaseDateSupplier), nullDate(l.PurchaseDateContractor), l.Notes,
		); lerr != nil {
			return Claim{}, fmt.Errorf("warranty: insert claim line: %w", lerr)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Claim{}, fmt.Errorf("warranty: commit insert claim: %w", err)
	}
	return r.GetClaim(ctx, id)
}

// nullDecimal treats a blank/invalid qty as 0 (operator-entered; never a write error).
//
// IT NOW ACTUALLY DOES WHAT THAT SENTENCE SAYS. Previously it only handled the BLANK case and passed anything
// else straight to a DECIMAL(19,4) column, so a quantity a person typed as "180 sq ft", which is exactly how
// the DIB-222 public form invites it ("e.g. 180 sq ft"), reached Postgres as text and came back 22P02, i.e. a
// 500 on an unauthenticated form for a perfectly reasonable answer. Found by the W5-B Chromium walkthrough.
//
// The rule: take the leading numeric prefix ("180 sq ft" → 180, "1,250" → 1250, "about 12" → 0) and fall back
// to 0 for anything with no number in front. The ORIGINAL text is never destroyed, the public path preserves
// it on the line's notes (see publicLineNotes), because the words are the information a triager needs and the
// number is only what the column can hold.
func nullDecimal(s string) any {
	n := leadingDecimal(s)
	if n == "" {
		return "0"
	}
	return n
}

// leadingDecimal extracts the leading decimal number from an operator/public-typed quantity, tolerating
// thousands separators and surrounding whitespace. Returns "" when there is no leading number.
func leadingDecimal(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	seenDigit, seenDot := false, false
	for i, r := range s {
		switch {
		case r == '-' && i == 0:
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			seenDigit = true
			b.WriteRune(r)
		case r == ',' && seenDigit && !seenDot:
			// A thousands separator inside the number ("1,250"); skipped, never written.
		case r == '.' && !seenDot:
			seenDot = true
			b.WriteRune(r)
		default:
			// First non-numeric character ends the number, "180 sq ft" stops at the space.
			if !seenDigit {
				return ""
			}
			return trimTrailingDot(b.String())
		}
	}
	if !seenDigit {
		return ""
	}
	return trimTrailingDot(b.String())
}

// trimTrailingDot keeps "12." from reaching the column as a malformed numeric.
func trimTrailingDot(s string) string {
	return strings.TrimSuffix(s, ".")
}

const claimCols = `c.id::text, c.claim_no::text, COALESCE(c.customer_id::text,''), COALESCE(cu.name,''),
	c.job_address, COALESCE(c.vendor_id::text,''), COALESCE(v.name,''), COALESCE(c.owner_id::text,''),
	COALESCE(u.display_name,''), c.branch_id::text, c.phase, c.source, c.description,
	COALESCE(c.invoice_id::text,''), COALESCE(c.order_id::text,''), COALESCE(c.po_id::text,''),
	c.supplier_purchase_date, c.submitted_at, c.sla_due, COALESCE(c.outcome,''),
	c.expected_cents, c.realized_cents, COALESCE(c.gl_journal_id::text,''), c.opened_at, c.closed_at, c.created_at`

const claimJoins = `FROM warranty_claims c
	LEFT JOIN customers cu ON cu.id = c.customer_id
	LEFT JOIN vendors v ON v.id = c.vendor_id
	LEFT JOIN users u ON u.id = c.owner_id`

func scanClaim(row pgx.Row) (Claim, error) {
	var c Claim
	var addr []byte
	var supPurch, submitted, sla, opened, closed *time.Time
	var created time.Time
	if err := row.Scan(&c.ID, &c.ClaimNo, &c.CustomerID, &c.CustomerName, &addr, &c.VendorID, &c.VendorName,
		&c.OwnerID, &c.OwnerName, &c.BranchID, &c.Phase, &c.Source, &c.Description, &c.InvoiceID, &c.OrderID,
		&c.POID, &supPurch, &submitted, &sla, &c.Outcome, &c.ExpectedCents, &c.RealizedCents, &c.GLJournalID,
		&opened, &closed, &created); err != nil {
		return Claim{}, err
	}
	if len(addr) > 0 {
		_ = json.Unmarshal(addr, &c.JobAddress)
	}
	c.SupplierPurchaseDate = dateStr(supPurch)
	c.SubmittedAt = tsStr(submitted)
	c.SLADue = tsStr(sla)
	c.OpenedAt = tsStr(opened)
	c.ClosedAt = tsStr(closed)
	c.CreatedAt = created.Format(time.RFC3339)
	return c, nil
}

func (r *Repository) GetClaim(ctx context.Context, id string) (Claim, error) {
	if r.pool == nil {
		return Claim{}, errNoDB
	}
	// A malformed (non-UUID) id can never match a uuid PK, treat it as not-found before the query
	// (which would otherwise raise 22P02 → HTTP 500), consistent with the no-existence-leak contract.
	if !isUUID(id) {
		return Claim{}, errClaimNotFound
	}
	c, err := scanClaim(r.pool.QueryRow(ctx, `SELECT `+claimCols+` `+claimJoins+` WHERE c.id = $1`, id))
	if err != nil {
		if err == pgx.ErrNoRows {
			return Claim{}, errClaimNotFound
		}
		return Claim{}, fmt.Errorf("warranty: get claim %q: %w", id, err)
	}
	return c, nil
}

func (r *Repository) ListClaims(ctx context.Context, f ClaimFilter) ([]Claim, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+claimCols+` `+claimJoins+`
		 WHERE ($1 = '' OR c.branch_id = $1::uuid)
		   AND ($2 = '' OR c.phase = $2)
		   AND ($3 = '' OR c.vendor_id = $3::uuid)
		 ORDER BY c.opened_at DESC`,
		f.BranchID, f.Phase, f.VendorID)
	if err != nil {
		return nil, fmt.Errorf("warranty: list claims: %w", err)
	}
	defer rows.Close()
	var out []Claim
	for rows.Next() {
		c, err := scanClaim(rows)
		if err != nil {
			return nil, fmt.Errorf("warranty: scan claim: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repository) ClaimLines(ctx context.Context, claimID string) ([]ClaimLine, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT l.id::text, l.claim_id::text, COALESCE(l.product_id::text,''), l.product_text,
		        l.qty::text, COALESCE(l.uom_code::text,''), l.purchase_date_supplier, l.purchase_date_contractor, l.notes
		 FROM warranty_claim_lines l WHERE l.claim_id = $1 ORDER BY l.created_at`, claimID)
	if err != nil {
		return nil, fmt.Errorf("warranty: claim lines: %w", err)
	}
	defer rows.Close()
	var out []ClaimLine
	for rows.Next() {
		var l ClaimLine
		var sup, con *time.Time
		if err := rows.Scan(&l.ID, &l.ClaimID, &l.ProductID, &l.ProductText, &l.Qty, &l.UOMCode, &sup, &con, &l.Notes); err != nil {
			return nil, fmt.Errorf("warranty: scan claim line: %w", err)
		}
		l.PurchaseDateSupplier = dateStr(sup)
		l.PurchaseDateContractor = dateStr(con)
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *Repository) UpdateClaimVendorOwner(ctx context.Context, claimID, vendorID, ownerID string) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE warranty_claims SET vendor_id = $2, owner_id = $3, updated_at = now() WHERE id = $1`,
		claimID, nullUUID(vendorID), nullUUID(ownerID))
	if err != nil {
		// A well-formed but non-existent vendor_id/owner_id trips its FK (23503), an unknown master, a CLIENT
		// error, not a 500. Map it to 422, mirroring the sibling UpsertVendorProfile path, so an assign against a
		// stale/typo'd vendor or owner reports "unknown reference" instead of a raw internal error.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "23503" || pgErr.Code == "22P02") {
			return unprocessable("warranty: assign references an unknown vendor or owner, create it first")
		}
		return fmt.Errorf("warranty: update claim vendor/owner: %w", err)
	}
	return nil
}

// - FR-3 lifecycle -

// AdvanceClaimPhaseGuarded is the SERIALIZATION POINT for a phase advance, mirroring ClaimSettlementWinner: it
// SELECT … FOR UPDATE-locks the claim row at the START of the tx, RE-CHECKS the mutual-exclusion guard UNDER the
// lock, and only then writes the phase + the PHASE_CHANGE event in the SAME tx.
//
// TOCTOU FIX (money-critical): AdvancePhase used to read outcome via a plain SELECT (in callerScopedClaim) and
// then write the phase via an UNCONDITIONAL `UPDATE … WHERE id=$1`. A concurrent advance(ABANDONED) and
// settle(VENDOR_CREDIT) on a DECISION claim both read outcome="" / phase=DECISION, both passed their Go-side
// guards, and both wrote, stranding the claim phase=ABANDONED(LOST) WITH outcome=VENDOR_CREDIT + realized $ + a
// POSTED GL journal (a recovery reported as a loss). Because BOTH this method AND ClaimSettlementWinner now take
// the SAME row-level write lock and re-check the guard under it, they SERIALIZE: whichever commits first, the
// other sees the committed state and refuses.
//
// guardExit=true marks an advance to a terminal-LOST exit (ABANDONED/REJECTED). Under the lock, if the claim is
// already settled (outcome set) the advance is REFUSED with errAlreadySettledExit (the service maps it to 422) -
// it can no longer strand a settled recovery as a loss. Forward transitions pass guardExit=false: the guard is a
// no-op but the row lock still serializes this write against a concurrent settle (harmless, and keeps the phase
// write + event atomic).
func (r *Repository) AdvanceClaimPhaseGuarded(ctx context.Context, claimID, toPhase string, guardExit bool, submittedAt, slaDue, closedAt *time.Time, event ClaimEvent) error {
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("warranty: begin advance phase: %w", err)
	}
	defer tx.Rollback(ctx)

	// Lock the claim row for the life of the tx. A concurrent settle (ClaimSettlementWinner) blocks here until
	// this commits/rolls back, and vice-versa, so the guard re-check below sees the OTHER's committed state.
	var curPhase, curOutcome string
	err = tx.QueryRow(ctx,
		`SELECT phase, COALESCE(outcome,'') FROM warranty_claims WHERE id = $1 FOR UPDATE`, claimID).
		Scan(&curPhase, &curOutcome)
	if err == pgx.ErrNoRows {
		return errClaimNotFound
	}
	if err != nil {
		return fmt.Errorf("warranty: lock claim for advance: %w", err)
	}

	// Re-check UNDER the lock: a terminal state may have been reached by a concurrent advance between the
	// caller's read and this lock, never double-close (the CHECK on closed_at would also reject, but a clean
	// 422 is friendlier than a raw constraint 500).
	if IsTerminal(curPhase) {
		return errAlreadyTerminal
	}
	// Re-check UNDER the lock: advancing to a terminal-LOST exit is REFUSED once the claim is settled. This is
	// the atomic half of the mutual exclusion with RecordSettlement, the settle either already committed (we see
	// its outcome here and refuse) or has not yet acquired the lock (it will see OUR committed terminal phase and
	// refuse in ClaimSettlementWinner).
	if guardExit && curOutcome != "" {
		return errAlreadySettledExit
	}

	// COALESCE keeps a previously-stamped submitted_at/sla_due if this transition doesn't set them; closed_at is
	// set only on entering a terminal phase (and the 0053 CHECK enforces the terminal⇔closed_at invariant).
	if _, err := tx.Exec(ctx,
		`UPDATE warranty_claims SET
		   phase = $2,
		   submitted_at = COALESCE($3, submitted_at),
		   sla_due = COALESCE($4, sla_due),
		   closed_at = $5,
		   updated_at = now()
		 WHERE id = $1`,
		claimID, toPhase, submittedAt, slaDue, closedAt); err != nil {
		return fmt.Errorf("warranty: update claim phase: %w", err)
	}

	// The PHASE_CHANGE audit-spine event rides the SAME tx as the phase write: an advanced claim always carries
	// its event, and a refused/rolled-back one leaves neither.
	if err := insertEventTx(ctx, tx, event); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("warranty: commit advance phase: %w", err)
	}
	return nil
}

func (r *Repository) InsertEvent(ctx context.Context, e ClaimEvent) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	return insertEventQ(ctx, r.pool, e)
}

// eventQuerier is the pgx surface insertEventQ needs, satisfied by both *pgxpool.Pool and pgx.Tx, so an event
// can be written on its own or enlisted in a surrounding transaction (the settlement path).
type eventQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// insertEventTx writes a claim event inside an existing transaction (used so the SETTLEMENT event commits
// atomically with the claim mutation).
func insertEventTx(ctx context.Context, tx pgx.Tx, e ClaimEvent) error {
	_, err := insertEventQ(ctx, tx, e)
	return err
}

func insertEventQ(ctx context.Context, q eventQuerier, e ClaimEvent) (string, error) {
	detail, _ := json.Marshal(e.Detail)
	if e.Detail == nil {
		detail = []byte("{}")
	}
	occurred := time.Now()
	if e.OccurredAt != "" {
		if t, err := time.Parse(time.RFC3339, e.OccurredAt); err == nil {
			occurred = t
		}
	}
	var id string
	err := q.QueryRow(ctx,
		`INSERT INTO warranty_claim_events
		   (claim_id, event_type, from_phase, to_phase, checklist_item_id, checklist_key, checked, note,
		    evidence_ref, actor_id, detail, occurred_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		 RETURNING id::text`,
		e.ClaimID, e.EventType, nullStr(e.FromPhase), nullStr(e.ToPhase), nullUUID(e.ChecklistItemID),
		e.ChecklistKey, e.Checked, e.Note, e.EvidenceRef, nullUUID(e.ActorID), detail, occurred,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("warranty: insert event: %w", err)
	}
	return id, nil
}

func (r *Repository) EventsForClaim(ctx context.Context, claimID string) ([]ClaimEvent, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT e.id::text, e.claim_id::text, e.event_type, COALESCE(e.from_phase,''), COALESCE(e.to_phase,''),
		        COALESCE(e.checklist_item_id::text,''), e.checklist_key, e.checked, e.note, e.evidence_ref,
		        COALESCE(e.actor_id::text,''), e.detail, e.occurred_at
		 FROM warranty_claim_events e WHERE e.claim_id = $1 ORDER BY e.occurred_at, e.created_at`, claimID)
	if err != nil {
		return nil, fmt.Errorf("warranty: events for claim: %w", err)
	}
	defer rows.Close()
	var out []ClaimEvent
	for rows.Next() {
		var e ClaimEvent
		var checked *bool
		var detail []byte
		var occurred time.Time
		if err := rows.Scan(&e.ID, &e.ClaimID, &e.EventType, &e.FromPhase, &e.ToPhase, &e.ChecklistItemID,
			&e.ChecklistKey, &checked, &e.Note, &e.EvidenceRef, &e.ActorID, &detail, &occurred); err != nil {
			return nil, fmt.Errorf("warranty: scan event: %w", err)
		}
		e.Checked = checked
		if len(detail) > 0 {
			_ = json.Unmarshal(detail, &e.Detail)
		}
		e.OccurredAt = occurred.Format(time.RFC3339)
		out = append(out, e)
	}
	return out, rows.Err()
}

// TickedKeys reconstructs the CURRENT ticked state per item_key: the LATEST CHECKLIST_TICK for each key wins.
func (r *Repository) TickedKeys(ctx context.Context, claimID string) (map[string]bool, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT DISTINCT ON (checklist_key) checklist_key, checked
		 FROM warranty_claim_events
		 WHERE claim_id = $1 AND event_type = 'CHECKLIST_TICK' AND checklist_key <> ''
		 ORDER BY checklist_key, occurred_at DESC, created_at DESC`, claimID)
	if err != nil {
		return nil, fmt.Errorf("warranty: ticked keys: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var key string
		var checked *bool
		if err := rows.Scan(&key, &checked); err != nil {
			return nil, err
		}
		out[key] = checked != nil && *checked
	}
	return out, rows.Err()
}

// ChecklistForPhase returns the effective checklist for a (phase, vendor): a vendor-specific item overrides the
// default item of the same key; otherwise the default (vendor_id IS NULL) applies. Only active items.
func (r *Repository) ChecklistForPhase(ctx context.Context, phase, vendorID string) ([]ChecklistItem, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	// DISTINCT ON (item_key) preferring the vendor-specific row (vendor_id matched first via the ORDER BY).
	rows, err := r.pool.Query(ctx,
		`SELECT DISTINCT ON (item_key)
		        id::text, phase, COALESCE(vendor_id::text,''), item_key, label, required, sort_order
		 FROM warranty_checklist_items
		 WHERE phase = $1 AND is_active AND (vendor_id IS NULL OR vendor_id = $2::uuid)
		 ORDER BY item_key, (vendor_id IS NOT NULL) DESC`, phase, nullUUID(vendorID))
	if err != nil {
		return nil, fmt.Errorf("warranty: checklist for phase: %w", err)
	}
	defer rows.Close()
	items, err := collectChecklist(rows)
	if err != nil {
		return nil, err
	}
	// Stable render order: by sort_order then key.
	sortChecklist(items)
	return items, nil
}

func (r *Repository) ChecklistItemByKey(ctx context.Context, phase, vendorID, itemKey string) (ChecklistItem, error) {
	items, err := r.ChecklistForPhase(ctx, phase, vendorID)
	if err != nil {
		return ChecklistItem{}, err
	}
	for _, it := range items {
		if it.ItemKey == itemKey {
			return it, nil
		}
	}
	return ChecklistItem{}, unprocessable("warranty: no checklist item %q on phase %s", itemKey, phase)
}

func collectChecklist(rows pgx.Rows) ([]ChecklistItem, error) {
	var out []ChecklistItem
	for rows.Next() {
		var it ChecklistItem
		if err := rows.Scan(&it.ID, &it.Phase, &it.VendorID, &it.ItemKey, &it.Label, &it.Required, &it.SortOrder); err != nil {
			return nil, fmt.Errorf("warranty: scan checklist item: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// - FR-5 settlement -

// ClaimSettlementWinner is the SERIALIZATION POINT for settlement under concurrency: a single conditional
// UPDATE ... WHERE id=$1 AND outcome IS NULL RETURNING that lets EXACTLY ONE concurrent/repeat settle claim the
// claim. Postgres takes a row-level write lock, so N simultaneous settles serialize on this row: the first to
// commit writes its outcome/amounts and gets won=true; every other one then sees outcome
// already set (0 rows affected) and gets won=false, the caller returns 409 and posts NOTHING to GL. Because the
// winner is decided BEFORE any GL post, the goroutine that owns the claim row is unambiguous, so the journal it
// later posts (and stamps via StampSettlementJournal) can never disagree with the claim/FR-7 money. The
// SETTLEMENT audit-spine event is written in the SAME transaction, so a settled claim always has its event.
func (r *Repository) ClaimSettlementWinner(ctx context.Context, claimID, outcome string, expectedCents, realizedCents int64, event ClaimEvent) (won bool, err error) {
	if r.pool == nil {
		return false, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("warranty: begin settlement: %w", err)
	}
	defer tx.Rollback(ctx)

	// Lock the claim row for the life of the tx (TOCTOU FIX, the settle half of the mutual exclusion with
	// AdvanceClaimPhaseGuarded). A concurrent advance-to-a-terminal-LOST-exit blocks here until it commits/rolls
	// back; then we see its committed terminal phase below and REFUSE, so a claim can never end up settled WITH
	// a recovery AND phase=ABANDONED/REJECTED. Symmetrically, if WE lock first, the concurrent advance sees our
	// committed outcome under its own lock and refuses there.
	var curPhase, curOutcome string
	err = tx.QueryRow(ctx,
		`SELECT phase, COALESCE(outcome,'') FROM warranty_claims WHERE id = $1 FOR UPDATE`, claimID).
		Scan(&curPhase, &curOutcome)
	if err == pgx.ErrNoRows {
		return false, errClaimNotFound
	}
	if err != nil {
		return false, fmt.Errorf("warranty: lock claim for settlement: %w", err)
	}
	// Re-check UNDER the lock: a claim that a concurrent advance drove to a terminal exit (ABANDONED/REJECTED) -
	// or that was closed, must NOT be settled. This is the atomic re-check of the Go-side IsTerminal pre-check
	// in RecordSettlement; here it is authoritative because it holds the row lock. errAlreadyTerminal → 409.
	if IsTerminal(curPhase) {
		return false, errAlreadyTerminal
	}
	// Already settled (repeat/concurrent settle that lost the race elsewhere): loser, post nothing → 409.
	if curOutcome != "" {
		return false, nil
	}

	// Conditional write: only the first settle (outcome still NULL) mutates the row. Redundant WHERE clause
	// belt-and-suspenders behind the FOR UPDATE re-check above, this is the atomic winner-election.
	var settledID string
	err = tx.QueryRow(ctx,
		`UPDATE warranty_claims SET
		   outcome = $2, expected_cents = $3, realized_cents = $4, updated_at = now()
		 WHERE id = $1 AND outcome IS NULL
		 RETURNING id::text`,
		claimID, outcome, expectedCents, realizedCents).Scan(&settledID)
	if err == pgx.ErrNoRows {
		return false, nil // already settled, this goroutine is a loser; caller 409s and posts nothing
	}
	if err != nil {
		return false, fmt.Errorf("warranty: claim settlement winner: %w", err)
	}

	// The SETTLEMENT audit-spine event rides the SAME tx as the claim mutation: a committed settlement always
	// carries its event, and a rolled-back one leaves neither (no settled-claim-without-event skew).
	if err := insertEventTx(ctx, tx, event); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("warranty: commit settlement: %w", err)
	}
	return true, nil
}

// StampSettlementJournal records the GL journal id on the claim AFTER a successful post. Only the settlement
// winner (which already owns the row) calls this, so it cannot race another settle. Best-effort by design,
// consistent with "GL post is non-fatal": a failure here leaves the settlement recorded (amounts + outcome are
// already committed) merely without the journal id stamped, it never lets the outcome and the ledger disagree.
func (r *Repository) StampSettlementJournal(ctx context.Context, claimID, glJournalID string) error {
	if r.pool == nil {
		return errNoDB
	}
	if glJournalID == "" {
		return nil
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE warranty_claims SET gl_journal_id = $2, updated_at = now() WHERE id = $1`,
		claimID, nullUUID(glJournalID))
	if err != nil {
		return fmt.Errorf("warranty: stamp settlement journal: %w", err)
	}
	return nil
}

// - FR-7 reporting projection -

func (r *Repository) AllClaimsForReport(ctx context.Context, branchID string) ([]ReportClaim, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT c.id::text, c.claim_no::text, c.branch_id::text, COALESCE(c.vendor_id::text,''), COALESCE(v.name,''),
		        c.phase, COALESCE(c.outcome,''), c.expected_cents, c.realized_cents, c.opened_at, c.closed_at
		 FROM warranty_claims c LEFT JOIN vendors v ON v.id = c.vendor_id
		 WHERE ($1 = '' OR c.branch_id = $1::uuid)`, branchID)
	if err != nil {
		return nil, fmt.Errorf("warranty: report claims: %w", err)
	}
	defer rows.Close()
	var out []ReportClaim
	for rows.Next() {
		var rc ReportClaim
		var closed *time.Time
		if err := rows.Scan(&rc.ID, &rc.ClaimNo, &rc.BranchID, &rc.VendorID, &rc.VendorName, &rc.Phase, &rc.Outcome,
			&rc.ExpectedCents, &rc.RealizedCents, &rc.OpenedAt, &closed); err != nil {
			return nil, fmt.Errorf("warranty: scan report claim: %w", err)
		}
		rc.ClosedAt = closed
		out = append(out, rc)
	}
	return out, rows.Err()
}
