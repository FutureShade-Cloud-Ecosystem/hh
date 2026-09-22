package customerquote

// The audit seam's proof double. The audit module itself is not in this
// lane's base (the review's F-7): it arrives with Lane A's carry, and the
// concrete wiring is an integration-time re-point against the AuditSeam
// interface. This double exists so the lane's tests prove the seam's two
// load-bearing properties against the real frozen audit_log table: the
// transactional write with the carried chokepoint semantics (the deny by
// default allowlist and the seven-digit-run whole-value redaction, copied
// from ERP backend/internal/audit/service.go), and the linkage-key load with
// zero-or-multiple failing closed. It writes exactly the row the integration
// adapter will write; it is test scaffolding in a test file, never code
// under test, and the production seam is not built here (the seam is built
// once, in audit.go).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seamAllowlist is the deny-by-default Extra allowlist for the acceptance
// row: the pinned key set of ADR-0022, and nothing else.
var seamAllowlist = map[string]bool{
	ExtraArtifactHash: true,
	ExtraArtifactSig:  true,
	ExtraAlg:          true,
	ExtraKeyID:        true,
	ExtraRefType:      true,
	ExtraTokenHash:    true,
	ExtraAmountCents:  true,
}

// seamRedact copies the carried chokepoint's string rule verbatim: a value
// containing a run of seven or more consecutive digits is replaced whole
// with "[redacted]". A UUID-shaped value passes (the carried exemption).
func seamRedact(s string) string {
	if seamLooksLikeUUID(s) {
		return s
	}
	if HasSevenDigitRun(s) {
		return "[redacted]"
	}
	return s
}

func seamLooksLikeUUID(s string) bool {
	return uuidShape.MatchString(strings.ToLower(s))
}

// pgxAuditSeam is the proof double over the frozen audit_log table.
type pgxAuditSeam struct {
	pool     *pgxpool.Pool
	failNext atomic.Bool
}

func newPgxAuditSeam(pool *pgxpool.Pool) *pgxAuditSeam { return &pgxAuditSeam{pool: pool} }

// FailNext makes the next RecordAcceptanceTx fail once, proving the
// mandatory-write rollback.
func (d *pgxAuditSeam) FailNext() { d.failNext.Store(true) }

// RecordAcceptanceTx implements AuditSeam: the pinned allowlist, the
// chokepoint on every string value, and the INSERT on the caller's
// transaction. Actorship is the link: acting_platform_user_id stays NULL.
func (d *pgxAuditSeam) RecordAcceptanceTx(ctx context.Context, tx TxExec, row AcceptanceAuditRow) error {
	if d.failNext.CompareAndSwap(true, false) {
		return errors.New("seam double: induced audit failure")
	}
	changes := map[string]any{
		"purpose": AuditPurpose,
	}
	strs := map[string]string{
		ExtraArtifactHash: row.ArtifactHash,
		ExtraArtifactSig:  row.ArtifactSig,
		ExtraAlg:          row.Alg,
		ExtraKeyID:        row.KeyID,
		ExtraRefType:      RefTypeShareToken,
		ExtraTokenHash:    row.TokenHash,
	}
	for k, v := range strs {
		if !seamAllowlist[k] {
			continue // deny by default; unreachable for this fixed set
		}
		changes[k] = seamRedact(v)
	}
	changes[ExtraAmountCents] = row.AcceptedTotalCents
	payload, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_log (action, entity_type, entity_id, changes)
		 VALUES ($1, $2, $3::uuid, $4)`,
		AuditAction, AuditEntityType, row.AcceptanceID, payload); err != nil {
		return fmt.Errorf("seam double: insert: %w", err)
	}
	return nil
}

// LoadAcceptance implements AuditSeam under the pinned linkage key: exactly
// one row loads; zero or multiple reports ambiguity.
func (d *pgxAuditSeam) LoadAcceptance(ctx context.Context, acceptanceID string) (AcceptanceAuditRow, bool, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT changes FROM audit_log
		 WHERE entity_type = $1 AND entity_id = $2::uuid AND action = $3`,
		AuditEntityType, acceptanceID, AuditAction)
	if err != nil {
		return AcceptanceAuditRow{}, false, err
	}
	defer rows.Close()
	var found []map[string]any
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return AcceptanceAuditRow{}, false, err
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return AcceptanceAuditRow{}, false, err
		}
		found = append(found, m)
	}
	if err := rows.Err(); err != nil {
		return AcceptanceAuditRow{}, false, err
	}
	if len(found) != 1 {
		return AcceptanceAuditRow{}, false, nil
	}
	m := found[0]
	get := func(k string) string {
		if v, ok := m[k].(string); ok {
			return v
		}
		return ""
	}
	amount := int64(0)
	if v, ok := m[ExtraAmountCents].(float64); ok {
		amount = int64(v)
	}
	return AcceptanceAuditRow{
		AcceptanceID:       acceptanceID,
		ArtifactHash:       get(ExtraArtifactHash),
		ArtifactSig:        get(ExtraArtifactSig),
		Alg:                get(ExtraAlg),
		KeyID:              get(ExtraKeyID),
		TokenHash:          get(ExtraTokenHash),
		AcceptedTotalCents: amount,
	}, true, nil
}
