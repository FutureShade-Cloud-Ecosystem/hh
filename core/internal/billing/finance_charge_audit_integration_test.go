//go:build integration

// finance_charge_audit_integration_test.go, the FC-1b fix round (PR 772's Opus read): the press's
// audit row keeps the month and the counts on the real audit service, every finance charge audit
// write is inside its act's transaction, and the night job names each failing branch.
//
// Run: go test -tags integration ./internal/billing/ -run W3FC1BFix
package billing

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// fcTxAudit is the real audit service behind a seam that can be told to fail its transactional
// write, so a leg can prove a failed record rolls the act back.
type fcTxAudit struct {
	real   *audit.Service
	failTx bool
}

func (a *fcTxAudit) Record(ctx context.Context, s audit.SensitiveAccess) error {
	return a.real.Record(ctx, s)
}

func (a *fcTxAudit) RecordTx(ctx context.Context, q audit.Execer, s audit.SensitiveAccess) error {
	if a.failTx {
		return errors.New("forced audit failure")
	}
	return a.real.RecordTx(ctx, q, s)
}

func (r *fcRig) realAudit() *fcTxAudit {
	r.t.Helper()
	a := &fcTxAudit{real: audit.NewService(audit.NewRepository(r.pool), slog.Default())}
	ids := []string{r.branch, r.other}
	r.t.Cleanup(func() {
		fcDelete(r.t, r.pool, "audit_log",
			`DELETE FROM audit_log WHERE action IN ('FINANCE_CHARGE_RUN','FINANCE_CHARGE_WAIVE','FINANCE_CHARGE_WAIVE_CLEARED')
			   AND (entity_id::text = ANY($1::text[]) OR actor_id = $2::uuid)`, ids, fc1Controller)
	})
	return a
}

// auditRows reads the changes of every audit row of an action for an entity, oldest first.
func (r *fcRig) auditRows(action, entityID string) []map[string]any {
	r.t.Helper()
	rows, err := r.pool.Query(r.ctx,
		`SELECT changes FROM audit_log WHERE action = $1 AND entity_id::text = $2 ORDER BY seq`, action, entityID)
	if err != nil {
		r.t.Fatalf("read audit_log: %v", err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			r.t.Fatalf("scan audit_log: %v", err)
		}
		m := map[string]any{}
		if err := json.Unmarshal(raw, &m); err != nil {
			r.t.Fatalf("decode audit_log changes: %v", err)
		}
		out = append(out, m)
	}
	return out
}

func fcAfterMonth() func() time.Time {
	return func() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, fc1Loc()) }
}

// P3-1: on the real audit service a press that charged and a replay that charged nothing read
// differently, and both name the month.
func TestIntegration_W3FC1BFix_PressAuditKeepsTheMonthAndTheCounts(t *testing.T) {
	r := fcRigNew(t)
	rec := r.realAudit()
	cust := r.customer(r.branch, "AUDITKEEP")
	r.tradeInvoice(cust, r.branch, fc1Due31, 100_000, "AUDITKEEP")
	w := r.writer(fcAfterMonth()).WithAudit(rec)
	req := FinanceChargeRequest{BranchID: r.branch, Month: fc1Month, ActorID: fc1Controller}
	if _, err := w.RunFinanceCharges(r.ctx, req); err != nil {
		t.Fatalf("W3-FC1b-fix red: the press: %v", err)
	}
	if _, err := w.RunFinanceCharges(r.ctx, req); err != nil {
		t.Fatalf("W3-FC1b-fix red: the replay: %v", err)
	}
	rows := r.auditRows(ActFinanceChargeRun, r.branch)
	if len(rows) != 2 {
		t.Fatalf("W3-FC1b-fix red: %d press audit rows, want 2", len(rows))
	}
	press, replay := rows[0], rows[1]
	for i, row := range rows {
		if row["month"] != fc1Month {
			t.Fatalf("W3-FC1b-fix red: audit row %d month = %v, want %s (%v)", i, row["month"], fc1Month, row)
		}
	}
	if press["charged"] != float64(1) || press["charged_cents"] != float64(1_500) || press["already_charged"] != float64(0) {
		t.Fatalf("W3-FC1b-fix red: the press row = %v, want charged 1, 1500 cents, already 0", press)
	}
	if replay["charged"] != float64(0) || replay["already_charged"] != float64(1) {
		t.Fatalf("W3-FC1b-fix red: the replay row = %v, want charged 0, already 1", replay)
	}
}
