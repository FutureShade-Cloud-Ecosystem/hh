package tax

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// repository.go is tax's pgx access layer over the 0042 tables (tax_rates, tax_status_card_profiles,
// tax_evidence, tax_config) + the inherited 0029 tax_decision_snapshot. It reads/writes ONLY tax-owned tables
// (no cross-module table access). *Repository satisfies the taxStore seam the service depends on.

// compile-time assertion: *Repository satisfies the store seam.
var _ taxStore = (*Repository)(nil)

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// MaxRateInRange returns the highest rate_bps among the rows for (jurisdiction, reasonCode) whose
// effectiveness overlaps [from, to] (to empty means open-ended, matching an open-ended new row).
// found=false when no row overlaps (MAX answers NULL, the scan takes it as no row). The admin edge's guard uses it against the standard reason: a
// backdated row governs beside every later standard era its interval reaches into, and must clear the
// highest of them.
func (r *Repository) MaxRateInRange(ctx context.Context, jurisdiction, reasonCode, from, to string) (int, bool, error) {
	if r.pool == nil {
		return 0, false, errNoDB
	}
	// MAX over an empty set answers ONE row carrying NULL, never no row, so the value scans into a
	// nullable and found answers the NULL (the round 3 review's P2: the dead ErrNoRows arm faulted
	// the admin edge on every no-overlap shape, before the seed's start and in foreign jurisdictions).
	var bps *int
	err := r.pool.QueryRow(ctx, `
		SELECT MAX(rate_bps) FROM tax_rates
		WHERE jurisdiction = $1 AND reason_code = $2
		  AND effective_from <= coalesce(NULLIF($4, '')::date, 'infinity'::date)
		  AND (effective_to IS NULL OR effective_to >= $3::date)`,
		jurisdiction, reasonCode, from, to).Scan(&bps)
	if err != nil {
		return 0, false, err
	}
	if bps == nil {
		return 0, false, nil
	}
	return *bps, true, nil
}

// -- rates ---------------------------------------------------------------------

// ResolveRate returns the rate_bps of the row in effect for (jurisdiction, reasonCode) on asOf: the latest
// effective_from ≤ asOf whose effective_to is NULL or ≥ asOf. found=false when no row governs that date.
func (r *Repository) ResolveRate(ctx context.Context, jurisdiction, reasonCode, asOf string) (int, bool, error) {
	if r.pool == nil {
		return 0, false, errNoDB
	}
	var bps int
	err := r.pool.QueryRow(ctx, `
		SELECT rate_bps FROM tax_rates
		WHERE jurisdiction = $1 AND reason_code = $2
		  AND effective_from <= $3::date
		  AND (effective_to IS NULL OR effective_to >= $3::date)
		ORDER BY effective_from DESC
		LIMIT 1`, jurisdiction, reasonCode, asOf).Scan(&bps)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("tax: resolve rate: %w", err)
	}
	return bps, true, nil
}

func (r *Repository) ListRates(ctx context.Context) ([]Rate, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, jurisdiction, reason_code, rate_bps,
		       to_char(effective_from,'YYYY-MM-DD'),
		       to_char(effective_to,'YYYY-MM-DD'), notes
		FROM tax_rates
		ORDER BY jurisdiction, reason_code, effective_from DESC`)
	if err != nil {
		return nil, fmt.Errorf("tax: list rates: %w", err)
	}
	defer rows.Close()
	var out []Rate
	for rows.Next() {
		var rt Rate
		var to *string
		if err := rows.Scan(&rt.ID, &rt.Jurisdiction, &rt.ReasonCode, &rt.RateBps, &rt.EffectiveFrom, &to, &rt.Notes); err != nil {
			return nil, fmt.Errorf("tax: scan rate: %w", err)
		}
		rt.EffectiveTo = str(to)
		out = append(out, rt)
	}
	return out, rows.Err()
}

func (r *Repository) InsertRate(ctx context.Context, in Rate) (Rate, error) {
	if r.pool == nil {
		return Rate{}, errNoDB
	}
	notes := in.Notes
	if notes == "" {
		notes = "operator-entered"
	}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO tax_rates (jurisdiction, reason_code, rate_bps, effective_from, effective_to, notes)
		VALUES ($1,$2,$3,$4::date,$5::date,$6)
		RETURNING id::text, to_char(effective_from,'YYYY-MM-DD')`,
		in.Jurisdiction, in.ReasonCode, in.RateBps, in.EffectiveFrom, nullStr(in.EffectiveTo), notes,
	).Scan(&in.ID, &in.EffectiveFrom)
	if err != nil {
		return Rate{}, fmt.Errorf("tax: insert rate: %w", err)
	}
	in.Notes = notes
	return in, nil
}

// -- profiles --------------------------------------------------------------------

const profileCols = `id::text, code, name, rate_bps, requires_photo, requires_number, requires_attestation, status, notes`

func scanProfile(row pgx.Row) (StatusCardProfile, error) {
	var p StatusCardProfile
	err := row.Scan(&p.ID, &p.Code, &p.Name, &p.RateBps, &p.RequiresPhoto, &p.RequiresNumber, &p.RequiresAttestation, &p.Status, &p.Notes)
	return p, err
}

func (r *Repository) ListProfiles(ctx context.Context) ([]StatusCardProfile, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `SELECT `+profileCols+` FROM tax_status_card_profiles ORDER BY rate_bps, code`)
	if err != nil {
		return nil, fmt.Errorf("tax: list profiles: %w", err)
	}
	defer rows.Close()
	var out []StatusCardProfile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, fmt.Errorf("tax: scan profile: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repository) GetProfileByCode(ctx context.Context, code string) (StatusCardProfile, bool, error) {
	if r.pool == nil {
		return StatusCardProfile{}, false, errNoDB
	}
	p, err := scanProfile(r.pool.QueryRow(ctx, `SELECT `+profileCols+` FROM tax_status_card_profiles WHERE code = $1`, code))
	if errors.Is(err, pgx.ErrNoRows) {
		return StatusCardProfile{}, false, nil
	}
	if err != nil {
		return StatusCardProfile{}, false, fmt.Errorf("tax: get profile: %w", err)
	}
	return p, true, nil
}

func (r *Repository) UpsertProfile(ctx context.Context, p StatusCardProfile) (StatusCardProfile, error) {
	if r.pool == nil {
		return StatusCardProfile{}, errNoDB
	}
	row := r.pool.QueryRow(ctx, `
		INSERT INTO tax_status_card_profiles (code, name, rate_bps, requires_photo, requires_number, requires_attestation, status, notes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (code) DO UPDATE SET
		  name = EXCLUDED.name, rate_bps = EXCLUDED.rate_bps,
		  requires_photo = EXCLUDED.requires_photo, requires_number = EXCLUDED.requires_number,
		  requires_attestation = EXCLUDED.requires_attestation, status = EXCLUDED.status,
		  notes = EXCLUDED.notes, updated_at = now()
		RETURNING `+profileCols,
		p.Code, p.Name, p.RateBps, p.RequiresPhoto, p.RequiresNumber, p.RequiresAttestation, p.Status, p.Notes)
	out, err := scanProfile(row)
	if err != nil {
		return StatusCardProfile{}, fmt.Errorf("tax: upsert profile: %w", err)
	}
	return out, nil
}

// -- evidence (masked-only: number_mask is the ONLY identifier column read/written) ------------------------

const evidenceCols = `id::text, COALESCE(profile_id::text,''), COALESCE(card_profile_id::text,''),
	COALESCE(order_id::text,''), photo_ref, number_mask, attestation,
	COALESCE(to_char(tcrd_expiry,'YYYY-MM-DD'),''), COALESCE(captured_by::text,''),
	to_char(created_at,'YYYY-MM-DD"T"HH24:MI:SSOF')`

func scanEvidence(row pgx.Row) (Evidence, error) {
	var e Evidence
	err := row.Scan(&e.ID, &e.ProfileID, &e.CardProfileID, &e.OrderID, &e.PhotoRef, &e.NumberMask,
		&e.Attestation, &e.TCRDExpiry, &e.CapturedBy, &e.CreatedAt)
	return e, err
}

func (r *Repository) InsertEvidence(ctx context.Context, e Evidence) (Evidence, error) {
	if r.pool == nil {
		return Evidence{}, errNoDB
	}
	row := r.pool.QueryRow(ctx, `
		INSERT INTO tax_evidence (profile_id, card_profile_id, order_id, photo_ref, number_mask, attestation, tcrd_expiry, captured_by)
		VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7::date,$8::uuid)
		RETURNING `+evidenceCols,
		nullStr(e.ProfileID), nullStr(e.CardProfileID), nullStr(e.OrderID), e.PhotoRef, e.NumberMask,
		e.Attestation, nullStr(e.TCRDExpiry), nullStr(e.CapturedBy))
	out, err := scanEvidence(row)
	if err != nil {
		return Evidence{}, fmt.Errorf("tax: insert evidence: %w", err)
	}
	return out, nil
}

func (r *Repository) ListEvidence(ctx context.Context, f EvidenceFilter) ([]Evidence, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+evidenceCols+` FROM tax_evidence
		WHERE ($1 = '' OR order_id = $1::uuid)
		  AND ($2 = '' OR profile_id = $2::uuid)
		ORDER BY created_at DESC
		LIMIT $3`, f.OrderID, f.ProfileID, limit)
	if err != nil {
		return nil, fmt.Errorf("tax: list evidence: %w", err)
	}
	defer rows.Close()
	var out []Evidence
	for rows.Next() {
		e, err := scanEvidence(rows)
		if err != nil {
			return nil, fmt.Errorf("tax: scan evidence: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *Repository) GetEvidence(ctx context.Context, id string) (Evidence, bool, error) {
	if r.pool == nil {
		return Evidence{}, false, errNoDB
	}
	e, err := scanEvidence(r.pool.QueryRow(ctx, `SELECT `+evidenceCols+` FROM tax_evidence WHERE id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Evidence{}, false, nil
	}
	// A malformed (non-UUID) id raises SQLSTATE 22P02 on the ::uuid cast, treat it as not-found
	// (found=false), so viewEvidence/downloadEvidence return a clean 404 (IDOR-safe) and
	// validateEvidence surfaces its "evidence not found" 400 rather than an opaque 500.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "22P02" {
		return Evidence{}, false, nil
	}
	if err != nil {
		return Evidence{}, false, fmt.Errorf("tax: get evidence: %w", err)
	}
	return e, true, nil
}

// -- config --------------------------------------------------------------------

const configCols = `COALESCE(branch_id::text,''), hst_registration_no, round_mode, avalara_enabled, postal_routing_enabled, notes`

func (r *Repository) GetConfig(ctx context.Context, branchID string) (Config, error) {
	if r.pool == nil {
		return Config{}, errNoDB
	}
	var c Config
	err := r.pool.QueryRow(ctx, `
		SELECT `+configCols+` FROM tax_config
		WHERE (branch_id IS NOT DISTINCT FROM $1::uuid)
		LIMIT 1`, nullStr(branchID)).Scan(
		&c.BranchID, &c.HSTRegistrationNo, &c.RoundMode, &c.AvalaraEnabled, &c.PostalRoutingEnabled, &c.Notes)
	if errors.Is(err, pgx.ErrNoRows) {
		// Fall back to the org-default row when a branch has no override.
		if branchID != "" {
			return r.GetConfig(ctx, "")
		}
		return Config{RoundMode: "INVOICE_TOTAL"}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("tax: get config: %w", err)
	}
	return c, nil
}

func (r *Repository) UpsertConfig(ctx context.Context, c Config) (Config, error) {
	if r.pool == nil {
		return Config{}, errNoDB
	}
	// The org-default row (branch_id NULL) has no UNIQUE partial index, so update-else-insert it explicitly;
	// per-branch rows key on the branch_id UNIQUE constraint.
	if c.BranchID == "" {
		tag, err := r.pool.Exec(ctx, `
			UPDATE tax_config SET hst_registration_no = $1, round_mode = $2, avalara_enabled = $3,
			  postal_routing_enabled = $4, notes = COALESCE(NULLIF($5,''), notes), updated_at = now()
			WHERE branch_id IS NULL`,
			c.HSTRegistrationNo, c.RoundMode, c.AvalaraEnabled, c.PostalRoutingEnabled, c.Notes)
		if err != nil {
			return Config{}, fmt.Errorf("tax: update config: %w", err)
		}
		if tag.RowsAffected() == 0 {
			if _, err := r.pool.Exec(ctx, `
				INSERT INTO tax_config (branch_id, hst_registration_no, round_mode, avalara_enabled, postal_routing_enabled)
				VALUES (NULL,$1,$2,$3,$4)`,
				c.HSTRegistrationNo, c.RoundMode, c.AvalaraEnabled, c.PostalRoutingEnabled); err != nil {
				return Config{}, fmt.Errorf("tax: insert config: %w", err)
			}
		}
		return r.GetConfig(ctx, "")
	}
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO tax_config (branch_id, hst_registration_no, round_mode, avalara_enabled, postal_routing_enabled)
		VALUES ($1::uuid,$2,$3,$4,$5)
		ON CONFLICT (branch_id) DO UPDATE SET
		  hst_registration_no = EXCLUDED.hst_registration_no, round_mode = EXCLUDED.round_mode,
		  avalara_enabled = EXCLUDED.avalara_enabled, postal_routing_enabled = EXCLUDED.postal_routing_enabled,
		  updated_at = now()`,
		c.BranchID, c.HSTRegistrationNo, c.RoundMode, c.AvalaraEnabled, c.PostalRoutingEnabled); err != nil {
		return Config{}, fmt.Errorf("tax: upsert branch config: %w", err)
	}
	return r.GetConfig(ctx, c.BranchID)
}

// -- snapshots (recompute + recon) -------------------------------------------------

func (r *Repository) ListW4Snapshots(ctx context.Context, fromDate, toDate string) ([]Snapshot, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, order_id::text, COALESCE(order_line_id::text,''), tax_reason_code,
		       rule_version, rate_bps, full_hst_cents, on_credit_cents, net_collected_cents, automated,
		       standard_rate_bps
		FROM tax_decision_snapshot
		WHERE rule_version = $1
		  AND ($2 = '' OR created_at >= $2::date)
		  AND ($3 = '' OR created_at < ($3::date + INTERVAL '1 day'))
		ORDER BY created_at`, ruleVersionW4, fromDate, toDate)
	if err != nil {
		return nil, fmt.Errorf("tax: list w4 snapshots: %w", err)
	}
	defer rows.Close()
	var out []Snapshot
	for rows.Next() {
		var s Snapshot
		if err := rows.Scan(&s.ID, &s.OrderID, &s.OrderLineID, &s.ReasonCode, &s.RuleVersion,
			&s.RateBps, &s.FullHSTCents, &s.OnCreditCents, &s.NetCollectedCents, &s.Automated,
			&s.StandardRateBps); err != nil {
			return nil, fmt.Errorf("tax: scan snapshot: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repository) SnapshotExists(ctx context.Context, orderID, orderLineID, ruleVersion string) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM tax_decision_snapshot
			WHERE order_id = $1::uuid AND rule_version = $3
			  AND (order_line_id IS NOT DISTINCT FROM $2::uuid))`,
		orderID, nullStr(orderLineID), ruleVersion).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("tax: snapshot exists: %w", err)
	}
	return exists, nil
}

func (r *Repository) InsertSnapshot(ctx context.Context, s Snapshot) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var id string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO tax_decision_snapshot
		  (order_id, order_line_id, tax_reason_code, rule_version, rate_bps, full_hst_cents, on_credit_cents, net_collected_cents, automated, standard_rate_bps)
		VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id::text`,
		s.OrderID, nullStr(s.OrderLineID), s.ReasonCode, s.RuleVersion, s.RateBps,
		s.FullHSTCents, s.OnCreditCents, s.NetCollectedCents, s.Automated, standardRateOrOntario(s.StandardRateBps)).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("tax: insert snapshot: %w", err)
	}
	return id, nil
}

// CollectedHSTCents sums net_collected_cents from the tax-decision snapshots created in a period (the
// tax-decision side of the HST → GL tie-out). Period-scoped by created_at; org-wide (snapshots carry no
// branch column in the frozen 0029 schema, branch-scoped recon is deferred, see the worker report).
func (r *Repository) CollectedHSTCents(ctx context.Context, year, month int) (int64, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var total int64
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(net_collected_cents),0)
		FROM tax_decision_snapshot
		WHERE EXTRACT(YEAR FROM created_at) = $1
		  AND ($2 = 0 OR EXTRACT(MONTH FROM created_at) = $2)`, year, month).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("tax: collected hst: %w", err)
	}
	return total, nil
}

// standardRateOrOntario answers the standard rate a snapshot row is written with: a zero (a caller that
// carries none) is the Ontario 13 percent, the column's default.
func standardRateOrOntario(bps int) int {
	if bps <= 0 {
		return stdRateBps
	}
	return bps
}
