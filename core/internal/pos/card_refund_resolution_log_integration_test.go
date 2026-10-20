//go:build integration

package pos

// card_refund_resolution_log_integration_test.go, PR #501 round 1 P2-2 on
// real Postgres: the resolution's audit row rides the settle's own
// transaction (a failed audit write rolls the settle back), and the
// attempt's resolution history is appended to, never erased, by a same-key
// restage; the restage also clears the unknown-outcome stamp so the
// live-request fence guards the new request too.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/jackc/pgx/v5"
)

// failResolveAudit wraps the real recorder and fails the transactional write
// of the resolution row, the way a broken audit insert would.
type failResolveAudit struct{ real *audit.Service }

func (f failResolveAudit) Record(ctx context.Context, a audit.SensitiveAccess) error {
	return f.real.Record(ctx, a)
}

func (f failResolveAudit) RecordTx(ctx context.Context, q audit.Execer, a audit.SensitiveAccess) error {
	if a.Action == audit.ActCardRefundAttemptResolved {
		return fmt.Errorf("audit: the resolution row failed")
	}
	return f.real.RecordTx(ctx, q, a)
}

// seqRail answers the unknown outcome on every call and holds its second
// call open until released (the restaged request).
type seqRail struct {
	mu      sync.Mutex
	calls   int
	entered chan struct{}
	release chan struct{}
}

func (r *seqRail) RefundCard(_ context.Context, _ pgx.Tx, _ string, _ int64, _, _ string) (string, error) {
	r.mu.Lock()
	r.calls++
	n := r.calls
	r.mu.Unlock()
	if n == 2 {
		close(r.entered)
		<-r.release
	}
	return "", fmt.Errorf("run_payments: refund: %w: status 504", payment.ErrProcessorOutcomeUnknown)
}

func resolutionLog(t *testing.T, s *trentonStack, refundKey string) []map[string]any {
	t.Helper()
	var log []map[string]any
	if err := s.pool.QueryRow(context.Background(),
		`SELECT resolution_log FROM pos_card_refund_attempts WHERE refund_key = $1`, refundKey).Scan(&log); err != nil {
		t.Fatalf("the resolution_log read: %v", err)
	}
	return log
}

// A failed audit write aborts the settle: the row stays open, nothing is
// half written; with a working recorder the same settle then lands with its
// one audit row.
func TestIntegration_TwoTxResolution_AuditRowRidesTheSettleTx(t *testing.T) {
	pool := intPool(t)
	rail := &outcomeRail{err: fmt.Errorf("run_payments: refund: %w: status 504", payment.ErrProcessorOutcomeUnknown)}
	s := newTrentonStackOnPoolWithRail(t, pool, rail)
	cust := s.plantCustomer("HH-501 audit in tx", 30, 5000000)
	sale := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 4, TenderCard)
	returnKey := key()
	if rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(returnKey, sale, 0, RefundCard, 4)); rec.Code != http.StatusBadGateway {
		t.Fatalf("the unknown outcome answers 502, got %d: %s", rec.Code, rec.Body.String())
	}
	id, _, _, _, _ := attemptRow(t, pool, "refund:"+returnKey)
	realAudit := audit.NewService(audit.NewRepository(pool), nil)
	body := map[string]any{"outcome": "NOT_REFUNDED", "note": "processor shows no reversal"}

	s.svc.WithAudit(failResolveAudit{real: realAudit})
	rec := s.do(http.MethodPost, "/api/v1/pos/card-refund-attempts/"+id+"/resolve", s.bearer(trentonMgrSub), "", body)
	if rec.Code < http.StatusInternalServerError {
		t.Fatalf("a failed audit write fails the settle, got %d: %s", rec.Code, rec.Body.String())
	}
	_, status, _, _, resolvedBy := attemptRow(t, pool, "refund:"+returnKey)
	if status != "IN_FLIGHT" || resolvedBy != "" {
		t.Fatalf("the rolled back settle leaves the row open: %s %q", status, resolvedBy)
	}
	if n := len(resolutionLog(t, s, "refund:"+returnKey)); n != 0 {
		t.Fatalf("the rolled back settle leaves no history, got %d events", n)
	}
	if rows := s.auditRows(audit.ActCardRefundAttemptResolved, id); len(rows) != 0 {
		t.Fatalf("no resolution audit row after the rollback, got %d", len(rows))
	}

	s.svc.WithAudit(realAudit)
	rec = s.do(http.MethodPost, "/api/v1/pos/card-refund-attempts/"+id+"/resolve", s.bearer(trentonMgrSub), "", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("the settle lands with a working recorder, got %d: %s", rec.Code, rec.Body.String())
	}
	if rows := s.auditRows(audit.ActCardRefundAttemptResolved, id); len(rows) != 1 {
		t.Fatalf("the settle wrote its one audit row, got %d", len(rows))
	}
}

// NOT_REFUNDED, then the same-key restage, then REFUNDED: the restage blanks
// the live resolution columns but the resolution_log holds every event in
// order; the restaged request is itself fenced (its unknown-outcome stamp is
// cleared) until it lands; both settles wrote their audit row.
func TestIntegration_TwoTxResolution_RestageKeepsResolutionHistory(t *testing.T) {
	pool := intPool(t)
	rail := &seqRail{entered: make(chan struct{}), release: make(chan struct{})}
	s := newTrentonStackOnPoolWithRail(t, pool, rail)
	cust := s.plantCustomer("HH-501 resolution log", 30, 5000000)
	sale := s.ringForCustomer(t, key(), trentonBranchID, s.trentonTill, "trenton", cust, "", 4, TenderCard)
	returnKey := key()
	refundKey := "refund:" + returnKey
	settledRef := "rp_log_" + returnKey

	if rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(returnKey, sale, 0, RefundCard, 4)); rec.Code != http.StatusBadGateway {
		t.Fatalf("the unknown outcome answers 502, got %d: %s", rec.Code, rec.Body.String())
	}
	id, _, _, _, _ := attemptRow(t, pool, refundKey)
	rec := s.do(http.MethodPost, "/api/v1/pos/card-refund-attempts/"+id+"/resolve", s.bearer(trentonMgrSub), "",
		map[string]any{"outcome": "NOT_REFUNDED", "note": "first look: no reversal"})
	if rec.Code != http.StatusOK {
		t.Fatalf("the first settle lands, got %d: %s", rec.Code, rec.Body.String())
	}

	// The cashier retries on the same key: the restage, its processor call held.
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", s.returnBody(returnKey, sale, 0, RefundCard, 4))
	}()
	<-rail.entered

	// The live columns are blank; the history is not.
	_, status, _, _, resolvedBy := attemptRow(t, pool, refundKey)
	if status != "IN_FLIGHT" || resolvedBy != "" {
		t.Fatalf("the restage blanks the live resolution columns: %s %q", status, resolvedBy)
	}
	var note string
	var resolvedAt, processorAt *string
	if err := pool.QueryRow(context.Background(),
		`SELECT resolution_note, resolved_at::text, processor_at::text FROM pos_card_refund_attempts WHERE refund_key = $1`,
		refundKey).Scan(&note, &resolvedAt, &processorAt); err != nil {
		t.Fatalf("the restaged row read: %v", err)
	}
	if note != "" || resolvedAt != nil {
		t.Fatalf("the restage blanks the note and the resolved time: %q %v", note, resolvedAt)
	}
	if processorAt != nil {
		t.Fatal("the restage clears the unknown-outcome stamp, or the live fence is blind to the new request")
	}
	// The restaged request is fenced like any live one.
	rec = s.do(http.MethodPost, "/api/v1/pos/card-refund-attempts/"+id+"/resolve", s.bearer(trentonMgrSub), "",
		map[string]any{"outcome": "REFUNDED", "refund_ref": settledRef, "note": "too early"})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "may still be running at the processor") {
		t.Fatalf("the restaged live request refuses a settle, got %d: %s", rec.Code, rec.Body.String())
	}

	close(rail.release)
	if rec := <-done; rec.Code != http.StatusBadGateway {
		t.Fatalf("the held restage lands as the unknown outcome, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = s.do(http.MethodPost, "/api/v1/pos/card-refund-attempts/"+id+"/resolve", s.bearer(trentonMgrSub), "",
		map[string]any{"outcome": "REFUNDED", "refund_ref": settledRef, "note": "second look: reversal found"})
	if rec.Code != http.StatusOK {
		t.Fatalf("the second settle lands, got %d: %s", rec.Code, rec.Body.String())
	}

	log := resolutionLog(t, s, refundKey)
	var events []string
	for _, e := range log {
		events = append(events, fmt.Sprint(e["event"]))
	}
	if got := strings.Join(events, ","); got != "NOT_REFUNDED,RESTAGED,REFUNDED" {
		t.Fatalf("the resolution log holds every event in order, got %q", got)
	}
	if log[0]["by"] != managerUserID || log[0]["note"] != "first look: no reversal" || log[2]["refund_ref"] != settledRef {
		t.Fatalf("the log keeps who, why and the ref of each settle: %v", log)
	}
	if rows := s.auditRows(audit.ActCardRefundAttemptResolved, id); len(rows) != 2 {
		t.Fatalf("one audit row per settle, got %d", len(rows))
	}
}
