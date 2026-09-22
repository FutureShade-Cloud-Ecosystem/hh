package main

// cq_adapters.go, the customer-quote audit seam re-point (the Phase C
// integration closing HH-3.6 C5's marked construction site per ADR 0022):
// the module's AuditSeam interface satisfied over the carried audit
// service's transactional recorder. The proof double the module's lane
// shipped (core/internal/customerquote/seamdouble_test.go) is the
// specification: this adapter writes exactly the row that double writes,
// through the audit module's own chokepoint instead of a private copy of
// it, and loads under the same pinned linkage key with the same
// zero-or-multiple failure. The module's fence holds: customerquote never
// touches audit_log, and this adapter is composition-root wiring, the one
// place the two modules meet.

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customerquote"
)

// cqAuditSeam satisfies customerquote.AuditSeam over the carried audit
// service. It holds no state beyond the service: both halves of the seam
// are one call each, on the caller's transaction and the shared pool.
type cqAuditSeam struct{ svc *audit.Service }

// RecordAcceptanceTx writes the acceptance row ON the caller's acceptance
// transaction through audit.Service.RecordTx: the audit module's
// deny-by-default allowlist and the cleartext chokepoint are the single
// privacy boundary the row passes (the separator encoding the module
// applies upstream keeps every hex value under the chokepoint's
// seven-digit threshold by construction, and the key id's shape rule
// carries the same bound), and a write failure propagates so the whole
// acceptance rolls back (ADR 0022's mandatory write: an acceptance without
// its audit row would be an unsigned acceptance). Actorship stays empty:
// an acceptance has no principal, and attributing it to the link minter
// would misattribute the customer's decision.
func (a cqAuditSeam) RecordAcceptanceTx(ctx context.Context, tx customerquote.TxExec, row customerquote.AcceptanceAuditRow) error {
	return a.svc.RecordTx(ctx, tx, audit.SensitiveAccess{
		Action:     customerquote.AuditAction,
		EntityType: customerquote.AuditEntityType,
		EntityID:   row.AcceptanceID,
		Purpose:    customerquote.AuditPurpose,
		Extra: map[string]any{
			customerquote.ExtraArtifactHash: row.ArtifactHash,
			customerquote.ExtraArtifactSig:  row.ArtifactSig,
			customerquote.ExtraAlg:          row.Alg,
			customerquote.ExtraKeyID:        row.KeyID,
			customerquote.ExtraRefType:      customerquote.RefTypeShareToken,
			customerquote.ExtraTokenHash:    row.TokenHash,
			customerquote.ExtraAmountCents:  row.AcceptedTotalCents,
		},
	})
}

// LoadAcceptance reads the acceptance row back under the pinned linkage
// key (the action constant plus the entity type plus the entity id equal
// to the quote_acceptance row id): exactly one row loads, zero or multiple
// rows report ambiguity (ok false with no error) and fail verification
// closed. The changes ride as JSON through the audit row, so the integer
// amount returns as the JSON number it stored.
func (a cqAuditSeam) LoadAcceptance(ctx context.Context, acceptanceID string) (customerquote.AcceptanceAuditRow, bool, error) {
	entries, err := a.svc.ReopenTransaction(ctx, customerquote.AuditEntityType, acceptanceID)
	if err != nil {
		return customerquote.AcceptanceAuditRow{}, false, err
	}
	var found *audit.Entry
	matches := 0
	for i := range entries {
		if entries[i].Action == customerquote.AuditAction {
			found = &entries[i]
			matches++
		}
	}
	if matches != 1 {
		return customerquote.AcceptanceAuditRow{}, false, nil
	}
	get := func(k string) string {
		if v, ok := found.Changes[k].(string); ok {
			return v
		}
		return ""
	}
	amount := int64(0)
	if v, ok := found.Changes[customerquote.ExtraAmountCents].(float64); ok {
		amount = int64(v)
	}
	return customerquote.AcceptanceAuditRow{
		AcceptanceID:       acceptanceID,
		ArtifactHash:       get(customerquote.ExtraArtifactHash),
		ArtifactSig:        get(customerquote.ExtraArtifactSig),
		Alg:                get(customerquote.ExtraAlg),
		KeyID:              get(customerquote.ExtraKeyID),
		TokenHash:          get(customerquote.ExtraTokenHash),
		AcceptedTotalCents: amount,
	}, true, nil
}
