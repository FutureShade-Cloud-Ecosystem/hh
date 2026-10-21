//go:build integration

// matchreach_integration_test.go: the bank match is fenced to the caller's branch reach (SF-2g).
//
// POST /api/v1/bankrec/match is a manager route: any manager, any branch. The manual match took the payment target
// from the request and claimed it (RECEIVED to DEPOSITED) without looking at its branch, so a manager scoped to
// branch A holding branch B's payment id could bank it, and the refusal of a payment that was not RECEIVED named
// its number. An out of reach payment must answer EXACTLY as a missing one, and write nothing.
//
// The legs drive the real handler with a real caller authority (identity.StaffAuthority), over the real service,
// repository and payments rows.
package bankrec

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	reachPayB1 = "22222201-2222-4222-8222-222222222201" // branch B, RECEIVED
	reachPayB2 = "22222202-2222-4222-8222-222222222202" // branch B, already DEPOSITED
	reachPayA1 = "22222203-2222-4222-8222-222222222203" // branch A, RECEIVED
	reachGhost = "222222ff-2222-4222-8222-2222222222ff" // no such payment
)

type reachWorld struct {
	pool     *pgxpool.Pool
	h        *Handler
	branchA  string
	branchB  string
	account  string
	pay      *fakePayments
	poster   *dbPoster
	callerA  guards.StaffCaller
	callerAB guards.StaffCaller
}

func newReachWorld(t *testing.T) *reachWorld {
	t.Helper()
	pool := intPool(t)
	cleanTables(t, pool)
	w := &reachWorld{pool: pool, account: rbcAccountID(t, pool)}
	rows, err := pool.Query(context.Background(), `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' ORDER BY id LIMIT 2`)
	if err != nil {
		t.Fatalf("branches: %v", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) < 2 {
		t.Skipf("need two seeded BRANCH locations, found %d", len(ids))
	}
	w.branchA, w.branchB = ids[0], ids[1]
	insertPayment(t, pool, reachPayB1, w.branchB, "CARD", "GTW-REACH-1", 5000)
	insertPayment(t, pool, reachPayA1, w.branchA, "CARD", "GTW-REACH-3", 5000)
	insertPayment(t, pool, reachPayB2, w.branchB, "CARD", "GTW-REACH-2", 5000)
	if _, err := pool.Exec(context.Background(), `UPDATE payments SET status = 'DEPOSITED' WHERE id = $1::uuid`, reachPayB2); err != nil {
		t.Fatalf("deposit: %v", err)
	}
	w.poster = newDBPoster(pool)
	w.pay = &fakePayments{}
	svc := NewService(NewRepository(pool)).WithPayments(w.pay).WithJournalPoster(w.poster)
	w.h = NewHandler(svc, slog.Default())
	w.callerA = guards.StaffCaller{StaffAuthority: identity.StaffAuthority{Tier: identity.TierManager, Scope: identity.ScopeBranch, Branches: []string{w.branchA}}}
	w.callerAB = guards.StaffCaller{StaffAuthority: identity.StaffAuthority{Tier: identity.TierManager, Scope: identity.ScopeGlobal}}
	return w
}

// post sends one body to the match route as the caller and returns the status and the body text.
func (w *reachWorld) post(caller guards.StaffCaller, body map[string]any) (int, string) {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/bankrec/match", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	w.h.match(rec, req, caller)
	return rec.Code, strings.TrimSpace(rec.Body.String())
}

func (w *reachWorld) line(t *testing.T, desc string, cents int64) string {
	t.Helper()
	return insertLine(t, w.pool, w.account, time.Now().UTC().Truncate(24*time.Hour), desc, desc, cents)
}

func (w *reachWorld) manual(line, pay string) map[string]any {
	return map[string]any{"mode": "manual", "statement_line_id": line, "target_type": "PAYMENT", "target_ref_id": pay, "amount_cents": 5000}
}

func (w *reachWorld) written(t *testing.T, line, pay string) (lineStatus, payStatus string, matches int) {
	t.Helper()
	ctx := context.Background()
	if err := w.pool.QueryRow(ctx, `SELECT status FROM bank_statement_lines WHERE id = $1::uuid`, line).Scan(&lineStatus); err != nil {
		t.Fatalf("line: %v", err)
	}
	if err := w.pool.QueryRow(ctx, `SELECT status FROM payments WHERE id = $1::uuid`, pay).Scan(&payStatus); err != nil {
		payStatus = "(none)"
	}
	if err := w.pool.QueryRow(ctx, `SELECT count(*) FROM bank_matches WHERE statement_line_id = $1::uuid`, line).Scan(&matches); err != nil {
		t.Fatalf("matches: %v", err)
	}
	return lineStatus, payStatus, matches
}

// A manager of branch A cannot bank branch B's payment: it answers as a missing payment does, and nothing is written.
func TestIntegration_MatchReach_AManagerOfOneBranchCannotBankAnotherBranchsPayment(t *testing.T) {
	w := newReachWorld(t)
	missingLine := w.line(t, "REACH MISSING", 5000)
	missCode, missBody := w.post(w.callerA, w.manual(missingLine, reachGhost))
	t.Logf("a payment that does not exist answers %d %s", missCode, missBody)

	line := w.line(t, "REACH OTHER BRANCH", 5000)
	code, body := w.post(w.callerA, w.manual(line, reachPayB1))
	ls, ps, n := w.written(t, line, reachPayB1)
	t.Logf("branch B's payment, asked by a branch A manager: %d %s | line %s payment %s matches %d", code, body, ls, ps, n)
	if code != missCode || body != missBody {
		t.Errorf("an out of reach payment answers %d %q, a missing one %d %q: they must be the same", code, body, missCode, missBody)
	}
	if ls != "UNMATCHED" || ps != "RECEIVED" || n != 0 {
		t.Errorf("after the refusal: line %s, payment %s, match rows %d; want UNMATCHED, RECEIVED, 0", ls, ps, n)
	}
}

// The refusal never reveals the number of an out of reach payment: a payment of another branch that is not RECEIVED
// answers as a missing one, not with its payment number.
func TestIntegration_MatchReach_TheRefusalNeverNamesAnOutOfReachPayment(t *testing.T) {
	w := newReachWorld(t)
	missingLine := w.line(t, "REACH MISSING 2", 5000)
	missCode, missBody := w.post(w.callerA, w.manual(missingLine, reachGhost))
	line := w.line(t, "REACH OTHER BRANCH DEPOSITED", 5000)
	code, body := w.post(w.callerA, w.manual(line, reachPayB2))
	t.Logf("branch B's deposited payment, asked by a branch A manager: %d %s", code, body)
	if strings.Contains(body, "PAY-") {
		t.Errorf("the refusal names the number of another branch's payment: %s", body)
	}
	if code != missCode || body != missBody {
		t.Errorf("an out of reach deposited payment answers %d %q, a missing one %d %q", code, body, missCode, missBody)
	}
}

// Controls: the same manager matches its own branch's payment, and a manager with reach over both branches matches
// branch B's.
func TestIntegration_MatchReach_ManagersWithReachStillMatch(t *testing.T) {
	w := newReachWorld(t)
	own := w.line(t, "REACH OWN", 5000)
	if code, body := w.post(w.callerA, w.manual(own, reachPayA1)); code != http.StatusCreated {
		t.Fatalf("a branch A manager matching branch A's payment answered %d %s", code, body)
	}
	if _, ps, _ := w.written(t, own, reachPayA1); ps != "DEPOSITED" {
		t.Errorf("own branch payment reads %s, want DEPOSITED", ps)
	}
	both := w.line(t, "REACH BOTH", 5000)
	if code, body := w.post(w.callerAB, w.manual(both, reachPayB1)); code != http.StatusCreated {
		t.Fatalf("a manager with reach over both branches matching branch B's payment answered %d %s", code, body)
	}
	if _, ps, _ := w.written(t, both, reachPayB1); ps != "DEPOSITED" {
		t.Errorf("branch B's payment reads %s, want DEPOSITED", ps)
	}
}

// The auto match builds its proposals from the till's unreconciled card payments, which the client cannot name; a
// branch A manager's run must not claim branch B's payment either.
func TestIntegration_MatchReach_AnAutoMatchByABranchManagerSkipsAnotherBranchsPayments(t *testing.T) {
	w := newReachWorld(t)
	d := time.Now().UTC().Truncate(24 * time.Hour)
	line := insertLine(t, w.pool, w.account, d, "RUN-PAY CARD SETTLEMENT", "GTW-REACH-1", 5000)
	w.pay.cands = []CandidatePayment{{PaymentID: reachPayB1, BranchID: w.branchB, GatewayRef: "GTW-REACH-1", AmountCents: 5000, ReceivedAt: d}}
	from, to := d.Add(-24*time.Hour), d.Add(48*time.Hour)
	code, body := w.post(w.callerA, map[string]any{"mode": "auto", "bank_account_id": w.account, "from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339)})
	ls, ps, n := w.written(t, line, reachPayB1)
	t.Logf("auto match by a branch A manager over branch B's candidate: %d %s | line %s payment %s matches %d", code, body, ls, ps, n)
	if ps != "RECEIVED" || n != 0 {
		t.Errorf("the auto match claimed branch B's payment: payment %s, match rows %d", ps, n)
	}
	// Control: a manager with reach over both branches, same candidates, same window: the match is made.
	code, body = w.post(w.callerAB, map[string]any{"mode": "auto", "bank_account_id": w.account, "from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339)})
	ls, ps, n = w.written(t, line, reachPayB1)
	if code != http.StatusOK || ls != "MATCHED" || ps != "DEPOSITED" || n != 1 {
		t.Errorf("control: a manager with reach over both: %d %s | line %s payment %s matches %d; want the match made", code, body, ls, ps, n)
	}
}
