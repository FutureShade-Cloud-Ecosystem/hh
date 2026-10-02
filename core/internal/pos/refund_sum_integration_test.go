//go:build integration

package pos

// refund_sum_integration_test.go, the refund approval amount counting every
// return against the same sale together, over the real composition on the
// throwaway local Postgres the Trenton rig runs on (the world, the people,
// the two drawers and the exact 10.00 anchor are buildWorld's, so every
// return's money lands on whole cents): the reviewer's slice on one sale,
// units individually under the configured 250.00 and together over it, the
// refusal with the contract's body, the manager's credential committing the
// crossing return, a later small return refusing again, and one
// deterministic parallel pair of returns of two different lines of one
// sale, where the per-sale advisory lock lets exactly one commit.
//
// Run (own throwaway container, removed afterwards; the rig's run header in
// returns_trenton_integration_test.go shows the pattern, this is another
// unused port):
//
//	docker run --name hh-refundsum-pg -e POSTGRES_USER=hardscape \
//	  -e POSTGRES_PASSWORD=hardscape -e POSTGRES_DB=hardscapeos \
//	  -p 5613:5432 -d postgres:16
//	cd core
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5613/hardscapeos?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core migrate
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5613/hardscapeos?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core seed
//	CI=true DATABASE_URL='postgres://hardscape:hardscape@localhost:5613/hardscapeos?sslmode=disable' \
//	  go test -tags integration ./internal/pos/ -run TestIntegration_Trenton_RefundSums -p 1 -v

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// The reviewer's slice on one sale: a 40 SF sale at the exact 10.00 anchor
// (400.00 of goods, the flat 13 percent on top), whole-line returns of
// 10 SF paying 113.00 each. One at a time each sits under the configured
// 250.00; together they cross it. The first and the second commit, the third
// answers the contract's 409 with the configured amount and both approver
// roles and writes its audit row; the same third return with the Sales
// manager's credential in the approver header commits as the manager's
// decision; a later small return refuses again on the sale's running total.
func TestIntegration_Trenton_RefundSumsPerSale(t *testing.T) {
	s := newTrentonStack(t)
	sale := s.ringSale(key(), trentonBranchID, s.trentonTill, "trenton", []float64{40}, TenderCash)

	// The first and the second unit return commit: 113.00 each, the running
	// total 226.00 at or under the amount.
	for i := 0; i < 2; i++ {
		in := s.returnBody(key(), sale, 0, RefundCash, 10)
		rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", in)
		if rec.Code != http.StatusCreated {
			t.Fatalf("unit return %d must commit under the amount, got %d: %s", i+1, rec.Code, rec.Body.String())
		}
	}

	// The third crosses: 226.00 + 113.00 = 339.00 over the 250.00. Refused
	// with the body the handler writes, an audit row naming the clerk, the
	// sale and the asked-for 113.00 (the NEW return's own total, never the
	// cumulative).
	in3 := s.returnBody(key(), sale, 0, RefundCash, 10)
	rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", in3)
	if rec.Code != http.StatusConflict {
		t.Fatalf("the third 113.00 return must refuse on the 339.00 cumulative, got %d: %s", rec.Code, rec.Body.String())
	}
	body := s.decodeBody(rec)
	if body["error"] != "refund_approval_required" {
		t.Fatalf("the refusal's error string wrong: %v", body["error"])
	}
	approval, ok := body["approval"].(map[string]any)
	if !ok || approval["amount_cents"].(float64) != 25000 {
		t.Fatalf("the refusal must name the configured 250.00: %v", body)
	}
	approvers, ok := approval["approvers"].([]any)
	if !ok || len(approvers) != 2 ||
		approvers[0] != "sales_manager" || approvers[1] != "branch_manager" {
		t.Fatalf("the refusal must name both approver roles: %v", approval)
	}
	refusals := s.auditRows(auditActionRefundApprovalRefused, sale.OrderID)
	if len(refusals) == 0 {
		t.Fatal("the refusal must write its audit row")
	}
	if refusals[0]["actor_id"] != clerkUserID {
		t.Fatalf("the refusal row must name the clerk: %v", refusals[0])
	}
	if refusals[0]["amount_cents"] != float64(11300) {
		t.Fatalf("the refusal row must keep the asked-for 113.00, not the cumulative: %v", refusals[0])
	}

	// The same third return with the Sales manager's credential in the
	// approver header commits as the manager's decision: the prospect reads
	// the prior committed total, so the header is consulted even though the
	// new return alone sits under the amount.
	inMgr := s.withKey(in3)
	mgrToken := "Bearer " + s.harness.Minter.StaffToken(t, trentonMgrSub)
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), mgrToken, inMgr)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the manager's credential must commit the crossing return, got %d: %s", rec.Code, rec.Body.String())
	}
	committedReturn := s.decodeBody(rec)["return_id"].(string)
	var decisionRow map[string]any
	for _, row := range s.auditRows("RETURN_COMPLETED", committedReturn) {
		if row["approver_id"] == managerUserID {
			decisionRow = row
			break
		}
	}
	if decisionRow == nil {
		t.Fatalf("the commit must be the manager's decision, no completion row names the manager: %v", s.auditRows("RETURN_COMPLETED", committedReturn))
	}

	// A later small return refuses again: one unit pays 11.30, under the
	// amount alone, but the sale's cumulative is over it now.
	inSmall := s.returnBody(key(), sale, 0, RefundCash, 1)
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", inSmall)
	if rec.Code != http.StatusConflict {
		t.Fatalf("a later small return must refuse once the cumulative is over, got %d: %s", rec.Code, rec.Body.String())
	}
	if again := s.decodeBody(rec); again["approval"].(map[string]any)["amount_cents"] != float64(25000) {
		t.Fatalf("the later refusal must carry the configured amount: %v", again)
	}
}

// One deterministic parallel pair: two returns of two DIFFERENT lines of one
// sale, fired together, each under the configured 250.00 alone, their sum
// over it. The line locks serialize only returns of the SAME line, so the
// per-sale advisory lock taken at the top of every Return transaction is
// what makes exactly one commit and exactly one refuse (on the unfixed core
// both read the prior sum before the other commits and both slip under).
func TestIntegration_Trenton_RefundSums_ConcurrentLines(t *testing.T) {
	s := newTrentonStack(t)
	// Two lines of 15 SF (150.00 of goods a line): a whole-line return pays
	// 169.50, under the amount alone, over it with its sibling.
	sale := s.ringSale(key(), trentonBranchID, s.trentonTill, "trenton", []float64{15, 15}, TenderCash)

	bearer := s.bearer(trentonClerkSub)
	bodies := []*bytes.Reader{}
	for i := 0; i < 2; i++ {
		raw, err := json.Marshal(s.returnBody(key(), sale, i, RefundCash, 15))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		bodies = append(bodies, bytes.NewReader(raw))
	}

	results := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/pos/returns", bodies[i])
			req.Header.Set("Authorization", bearer)
			req.Header.Set("X-Org", demoOrgSlug)
			rec := httptest.NewRecorder()
			s.mux.ServeHTTP(rec, req)
			results[i] = rec
		}(i)
	}
	wg.Wait()

	created, refused := 0, 0
	for _, rec := range results {
		switch rec.Code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			refused++
			if b := s.decodeBody(rec); b["error"] != "refund_approval_required" {
				t.Fatalf("the refused one must carry the approval refusal: %v", b)
			}
		default:
			t.Fatalf("each parallel return must commit or refuse with the approval body, got %d: %s", rec.Code, rec.Body.String())
		}
	}
	if created != 1 || refused != 1 {
		t.Fatalf("exactly one of the two parallel returns may commit, got created=%d refused=%d", created, refused)
	}
}

// Two copies of the SAME return, same idempotency key, fired together, on a
// line whose doubled total crosses the amount (the reviewer's probe): both
// pass the pre-transaction replay check before either row exists, and the
// loser waits on the per-sale advisory lock while the winner commits. The
// loser must answer the committed winner as the replay (a 200-class body
// carrying the winner's return id), never read the winner's own row into
// its prior sum and refuse a return that actually committed, and the sale
// must carry no REFUND_APPROVAL_REFUSED audit row at all.
func TestIntegration_Trenton_RefundSums_SameKeyDuplicate(t *testing.T) {
	s := newTrentonStack(t)
	// One line of 30 SF, 15 SF returned per copy: a return pays 169.50, the
	// doubled total 339.00 over the configured 250.00, and each copy alone
	// leaves half the line returnable so the loser clears the over-return
	// guard and reaches the gate, the reviewer's probe.
	sale := s.ringSale(key(), trentonBranchID, s.trentonTill, "trenton", []float64{30}, TenderCash)

	idem := key()
	raw, err := json.Marshal(s.returnBody(idem, sale, 0, RefundCash, 15))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	bearer := s.bearer(trentonClerkSub)

	results := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/pos/returns", bytes.NewReader(raw))
			req.Header.Set("Authorization", bearer)
			req.Header.Set("X-Org", demoOrgSlug)
			rec := httptest.NewRecorder()
			s.mux.ServeHTTP(rec, req)
			results[i] = rec
		}(i)
	}
	wg.Wait()

	created, replayed := 0, 0
	var winnerID, replayID string
	for _, rec := range results {
		switch rec.Code {
		case http.StatusCreated:
			created++
			winnerID = s.decodeBody(rec)["return_id"].(string)
		case http.StatusOK:
			replayed++
			replayID = s.decodeBody(rec)["return_id"].(string)
		default:
			t.Fatalf("the same-key pair must answer one commit and one replay, got %d: %s", rec.Code, rec.Body.String())
		}
	}
	if created != 1 || replayed != 1 {
		t.Fatalf("exactly one commit and one replay for the same-key pair, got created=%d replayed=%d", created, replayed)
	}
	if replayID != winnerID {
		t.Fatalf("the replay must carry the committed winner's return id, got %s vs %s", replayID, winnerID)
	}
	if refusals := s.auditRows(auditActionRefundApprovalRefused, sale.OrderID); len(refusals) != 0 {
		t.Fatalf("a return that committed must leave no refusal row behind: %v", refusals)
	}
}

// The prospect's prior blind spot, over the real composition: two lines each
// individually under the amount, the first line's return commits with no
// approval, and the second line's return carrying the Sales manager's
// credential commits as the manager's decision, the header consulted only
// because the prior committed total pushes the cumulative over (the
// requested line's own original total sits under the amount).
func TestIntegration_Trenton_RefundSums_TwoLinesManagerHeader(t *testing.T) {
	s := newTrentonStack(t)
	// Two lines of 15 SF (150.00 of goods a line): a whole-line return pays
	// 169.50, under the amount alone, over it with its sibling.
	sale := s.ringSale(key(), trentonBranchID, s.trentonTill, "trenton", []float64{15, 15}, TenderCash)

	in1 := s.returnBody(key(), sale, 0, RefundCash, 15)
	rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", in1)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the first line's return commits under the amount, got %d: %s", rec.Code, rec.Body.String())
	}

	in2 := s.returnBody(key(), sale, 1, RefundCash, 15)
	mgrToken := "Bearer " + s.harness.Minter.StaffToken(t, trentonMgrSub)
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), mgrToken, in2)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the second line's return must commit on the manager's credential, got %d: %s", rec.Code, rec.Body.String())
	}
	committedReturn := s.decodeBody(rec)["return_id"].(string)
	var decisionRow map[string]any
	for _, row := range s.auditRows("RETURN_COMPLETED", committedReturn) {
		if row["approver_id"] == managerUserID {
			decisionRow = row
			break
		}
	}
	if decisionRow == nil {
		t.Fatalf("the commit must be the manager's decision, no completion row names the manager: %v", s.auditRows("RETURN_COMPLETED", committedReturn))
	}
}
