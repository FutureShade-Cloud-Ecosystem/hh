//go:build integration

package pos

// sale_preview_integration_test.go, the read-only sale preview (the row 985
// item) at Trenton over the real composition on a throwaway local Postgres:
// the real handler behind the real guards, the pricing and tax rails wired
// over the real engine the way cmd/core wires them, so every number a leg
// reads is the ring's own. The legs:
//
//	A. the preview equals the ring's totals for the same body: with tax
//	   (the live-demo example: a 266.00 subtotal, 13% HST, a 300.58
//	   total), with an exempt reason (an effective-dated zero-rate
//	   MANUAL row, the one ungated reason a person's decision may zero:
//	   no tax line, the total is the subtotal), and with a quantity
//	   break (the preview applies the break before the ring does).
//	B. the preview writes no row: no order, no order line, no pos_sale,
//	   no payment, no order number consumed.
//	C. the preview refuses a role the sale refuses: the staff wall
//	   reads, a foreign org's bearer takes the shared 403, no bearer
//	   takes the shared 401, an empty body is the clear 400.
//	D. the split refusal's problem body: the stable word, the two cents
//	   fields, and a message in dollars, never cents.
//
// Run (own throwaway container, removed afterwards, an unused port):
//
//	docker run --name hh-r15-sale-preview-pg -e POSTGRES_USER=hardscape \
//	  -e POSTGRES_PASSWORD=sppw -e POSTGRES_DB=r15SPMain -p 5768:5432 -d postgres:16
//	cd core
//	DATABASE_URL='postgres://hardscape:sppw@localhost:5768/r15SPMain?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core migrate
//	DATABASE_URL='postgres://hardscape:sppw@localhost:5768/r15SPMain?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core seed
//	CI=true DATABASE_URL='postgres://hardscape:sppw@localhost:5768/r15SPMain?sslmode=disable' \
//	  go test -tags integration ./internal/pos/ -run TestIntegration_Trenton_SalePreview -p 1 -v

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tax"
)

// previewWiredStack builds the Trenton stack with the tax engine wired the
// way cmd/core wires it, so the preview and the ring both assess through the
// real engine over the seeded rate table.
func previewWiredStack(t *testing.T) *trentonStack {
	t.Helper()
	s := newTrentonStack(t)
	s.svc.WithTaxAssessor(servedTaxAssessor{svc: tax.NewService(tax.NewRepository(s.pool))})
	return s
}

// previewTicket is the sale's body without any tender: the lines, the
// customer (empty resolves the house walk-in), the branch, the sale type and
// fulfilment, and the relief decision when one rides the ticket. 26.6 SF of
// the world's exact 10.00 paver is the live-demo example: a 266.00 subtotal,
// 3458 cents of 13% HST, a 300.58 total.
func previewTicket(branchID string) map[string]any {
	return map[string]any{
		"branch_id":   branchID,
		"customer_id": "",
		"sale_type":   "COLLECT_NOW",
		"fulfilment":  "PICKUP",
		"lines": []map[string]any{
			{"product_id": l2cProductID, "qty_entered": 26.6, "uom_entered": "SF"},
		},
	}
}

// ringTicket is the same body made a ring: the replay key, the drawer and
// the tender added, the relief decision carried when the caller has one.
func ringTicket(branchID, tillSessionID string, relief map[string]any) map[string]any {
	body := previewTicket(branchID)
	body["idempotency_key"] = key()
	body["till_session_id"] = tillSessionID
	body["tender"] = "CASH"
	if relief != nil {
		body["relief"] = relief
	}
	return body
}

// previewCents reads one integer cents field out of a decoded body.
func previewCents(t *testing.T, body map[string]any, field string) int64 {
	t.Helper()
	got, ok := body[field].(float64)
	if !ok {
		t.Fatalf("%s must be an integer cents field, got %v (%v)", field, body[field], body)
	}
	return int64(got)
}

// Leg A: the preview equals the ring's own totals for the same body, three
// worlds over one stack: the taxed example, an exempt reason, a quantity
// break.
func TestIntegration_Trenton_SalePreview_MatchesTheRing(t *testing.T) {
	s := previewWiredStack(t)
	clerk := s.bearer(trentonClerkSub)

	// a) With tax, the live-demo example: 266.00 subtotal, one 13% tax
	// line of 3458 cents, a 300.58 total, and the ring of the same body
	// commits those same three numbers.
	rec := s.do(http.MethodPost, "/api/v1/pos/sale-preview", clerk, "", previewTicket(trentonBranchID))
	if rec.Code != http.StatusOK {
		t.Fatalf("the preview must answer the ticket's totals: got %d: %s", rec.Code, rec.Body.String())
	}
	preview := s.decodeBody(rec)
	if got := previewCents(t, preview, "subtotal_cents"); got != 26600 {
		t.Fatalf("the example subtotal is 266.00: got %d (%v)", got, preview["subtotal_cents"])
	}
	if got := previewCents(t, preview, "total_cents"); got != 30058 {
		t.Fatalf("the example total is 300.58 (13%% on 266.00): got %d (%v)", got, preview["total_cents"])
	}
	lines, ok := preview["tax_lines"].([]any)
	if !ok || len(lines) != 1 {
		t.Fatalf("the example carries one tax line, the 13%% HST: %v", preview["tax_lines"])
	}
	taxLine, _ := lines[0].(map[string]any)
	if taxLine["reason_code"] != TaxStdON13 {
		t.Fatalf("the tax line names the standard reason: %v", taxLine)
	}
	if got, ok := taxLine["rate_bps"].(float64); !ok || got != 1300 {
		t.Fatalf("the tax line carries the seeded 1300 bps row: %v", taxLine)
	}
	if got := previewCents(t, taxLine, "tax_cents"); got != 3458 {
		t.Fatalf("13%% on 266.00 is 3458 cents: got %d (%v)", got, taxLine["tax_cents"])
	}

	rec = s.do(http.MethodPost, "/api/v1/pos/sales", clerk, "", ringTicket(trentonBranchID, s.trentonTill, nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the ring of the same body must commit: got %d: %s", rec.Code, rec.Body.String())
	}
	sale := s.decodeBody(rec)
	if got, want := previewCents(t, sale, "subtotal_cents"), previewCents(t, preview, "subtotal_cents"); got != want {
		t.Fatalf("the preview's subtotal must be the ring's own: preview %d, sale %d", want, got)
	}
	if got, want := previewCents(t, sale, "total_cents"), previewCents(t, preview, "total_cents"); got != want {
		t.Fatalf("the preview's total must be the ring's own: preview %d, sale %d", want, got)
	}
	if got, want := previewCents(t, sale, "tax_cents"), previewCents(t, taxLine, "tax_cents"); got != want {
		t.Fatalf("the preview's tax line must be the ring's own tax: preview %d, sale %d", want, got)
	}

	// b) An exempt ticket. The counter cannot bind an evidence-gated 0%
	// reason (the pos seam pins evidence false, the relief falls back to
	// the standard rate), so the exempt world here is the ungated one a
	// person's decision may zero: an effective-dated zero-rate MANUAL row,
	// planted for this leg and removed on the way out. The totals show no
	// tax line and the total is the subtotal, in the preview and in the
	// committed ring alike.
	ctx := context.Background()
	var rateRowID string
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO tax_rates (jurisdiction, reason_code, rate_bps, effective_from)
		VALUES ('ON','MANUAL_TAX_REVIEW',0,CURRENT_DATE)
		RETURNING id::text`).Scan(&rateRowID); err != nil {
		t.Fatalf("plant the zero-rate MANUAL row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM tax_rates WHERE id = $1::uuid`, rateRowID)
	})
	exemptRelief := map[string]any{"reason_code": "MANUAL_TAX_REVIEW"}

	exemptTicket := previewTicket(trentonBranchID)
	exemptTicket["relief"] = exemptRelief
	rec = s.do(http.MethodPost, "/api/v1/pos/sale-preview", clerk, "", exemptTicket)
	if rec.Code != http.StatusOK {
		t.Fatalf("the exempt preview must answer: got %d: %s", rec.Code, rec.Body.String())
	}
	exempt := s.decodeBody(rec)
	if got := previewCents(t, exempt, "subtotal_cents"); got != 26600 {
		t.Fatalf("the exempt subtotal is still 266.00: got %d (%v)", got, exempt)
	}
	if lines, ok := exempt["tax_lines"].([]any); !ok || len(lines) != 0 {
		t.Fatalf("an exempt ticket shows no tax line: %v", exempt["tax_lines"])
	}
	if got := previewCents(t, exempt, "tax_cents"); got != 0 {
		t.Fatalf("an exempt ticket collects no tax: got %d (%v)", got, exempt)
	}
	if got, want := previewCents(t, exempt, "total_cents"), previewCents(t, exempt, "subtotal_cents"); got != want {
		t.Fatalf("an exempt total is its subtotal: total %d, subtotal %d", got, want)
	}

	rec = s.do(http.MethodPost, "/api/v1/pos/sales", clerk, "", ringTicket(trentonBranchID, s.trentonTill, exemptRelief))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the exempt ring of the same body must commit: got %d: %s", rec.Code, rec.Body.String())
	}
	exemptSale := s.decodeBody(rec)
	if got := previewCents(t, exemptSale, "tax_cents"); got != 0 {
		t.Fatalf("the exempt ring collects no tax: got %d (%v)", got, exemptSale)
	}
	if got, want := previewCents(t, exemptSale, "total_cents"), previewCents(t, exempt, "total_cents"); got != want {
		t.Fatalf("the exempt preview's total must be the ring's own: preview %d, sale %d", want, got)
	}

	// c) A quantity break. A 10%-off break from 10 SF, planted for this
	// leg (one header, one tier, removed on the way out): the preview must
	// show the break's subtotal before the ring computes it, and the ring
	// of the same body commits the same three numbers.
	var breakHeaderID string
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO quantity_break_headers (code, name, product_id, threshold_uom, is_active)
		VALUES ('r15-preview-break', 'R15 preview break', $1::uuid, 'SF', TRUE)
		RETURNING id::text`, l2cProductID).Scan(&breakHeaderID); err != nil {
		t.Fatalf("plant the break header: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM quantity_break_headers WHERE id = $1::uuid`, breakHeaderID)
	})
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO quantity_break_items (header_id, from_qty, to_qty, value_type, discount_bps, price_uom)
		VALUES ($1::uuid, 10, NULL, 'PCT_OFF', 1000, 'SF')`, breakHeaderID); err != nil {
		t.Fatalf("plant the break tier: %v", err)
	}

	rec = s.do(http.MethodPost, "/api/v1/pos/sale-preview", clerk, "", previewTicket(trentonBranchID))
	if rec.Code != http.StatusOK {
		t.Fatalf("the broken-quantity preview must answer: got %d: %s", rec.Code, rec.Body.String())
	}
	broken := s.decodeBody(rec)
	if got := previewCents(t, broken, "subtotal_cents"); got != 23940 {
		t.Fatalf("the break must land in the preview before the ring: want 23940 (10%% off 266.00), got %d (%v)", got, broken["subtotal_cents"])
	}
	if got := previewCents(t, broken, "total_cents"); got != 27052 {
		t.Fatalf("the broken total is 270.52 (13%% on 239.40): got %d (%v)", got, broken["total_cents"])
	}

	rec = s.do(http.MethodPost, "/api/v1/pos/sales", clerk, "", ringTicket(trentonBranchID, s.trentonTill, nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the broken-quantity ring of the same body must commit: got %d: %s", rec.Code, rec.Body.String())
	}
	brokenSale := s.decodeBody(rec)
	if got, want := previewCents(t, brokenSale, "subtotal_cents"), previewCents(t, broken, "subtotal_cents"); got != want {
		t.Fatalf("the broken preview's subtotal must be the ring's own: preview %d, sale %d", want, got)
	}
	if got, want := previewCents(t, brokenSale, "total_cents"), previewCents(t, broken, "total_cents"); got != want {
		t.Fatalf("the broken preview's total must be the ring's own: preview %d, sale %d", want, got)
	}
}

// previewNoBearerCall fires one request with the org header and no bearer,
// the shape the shared 401 legs use.
func previewNoBearerCall(s *trentonStack, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("X-Org", demoOrgSlug)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

// Leg B: the preview writes no row. A body that the ring would commit as an
// order goes through the preview, and every table the ring writes is
// unchanged afterwards: no order, no order line, no pos_sale, no payment,
// and no order number consumed (the branch's counter holds its last value).
func TestIntegration_Trenton_SalePreview_WritesNoRow(t *testing.T) {
	s := previewWiredStack(t)
	ctx := context.Background()

	count := func(table string) int64 {
		t.Helper()
		var n int64
		if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	lastOrderNo := func() int64 {
		t.Helper()
		var n int64
		if err := s.pool.QueryRow(ctx,
			`SELECT last_value FROM order_no_sequences WHERE branch_id = $1::uuid`, trentonBranchID).Scan(&n); err != nil {
			t.Fatalf("read the branch order counter: %v", err)
		}
		return n
	}
	before := map[string]int64{
		"orders":          count("orders"),
		"order_lines":     count("order_lines"),
		"pos_sale":        count("pos_sale"),
		"payments":        count("payments"),
		"order_no_seq":    lastOrderNo(),
		"idempotency_key": countIdempotencyKeys(t, s, ctx),
	}

	rec := s.do(http.MethodPost, "/api/v1/pos/sale-preview", s.bearer(trentonClerkSub), "", previewTicket(trentonBranchID))
	if rec.Code != http.StatusOK {
		t.Fatalf("the preview must answer the ticket's totals: got %d: %s", rec.Code, rec.Body.String())
	}
	if got, want := previewCents(t, s.decodeBody(rec), "total_cents"), int64(30058); got != want {
		t.Fatalf("the preview priced the ticket (the example total): got %d, want %d", got, want)
	}

	after := map[string]int64{
		"orders":          count("orders"),
		"order_lines":     count("order_lines"),
		"pos_sale":        count("pos_sale"),
		"payments":        count("payments"),
		"order_no_seq":    lastOrderNo(),
		"idempotency_key": countIdempotencyKeys(t, s, ctx),
	}
	for _, table := range []string{"orders", "order_lines", "pos_sale", "payments", "order_no_seq", "idempotency_key"} {
		if before[table] != after[table] {
			t.Fatalf("the preview must write nothing: %s moved from %d to %d", table, before[table], after[table])
		}
	}
}

// countIdempotencyKeys counts the replay keys the ring would leave behind:
// the pos_sale key and every per-leg payment key.
func countIdempotencyKeys(t *testing.T, s *trentonStack, ctx context.Context) int64 {
	t.Helper()
	var n int64
	if err := s.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM pos_sale WHERE idempotency_key IS NOT NULL)
		     + (SELECT count(*) FROM payments WHERE idempotency_key IS NOT NULL)`).Scan(&n); err != nil {
		t.Fatalf("count idempotency keys: %v", err)
	}
	return n
}

// Leg C: the preview takes the sale's own walls. The staff wall reads the
// ticket; a bearer whose membership is another org's takes the shared 403
// the sale takes for the same credential; no bearer takes the shared 401;
// an empty body is the clear 400 (the no-lines refusal in words).
func TestIntegration_Trenton_SalePreview_RoleWalls(t *testing.T) {
	s := previewWiredStack(t)
	ticket := previewTicket(trentonBranchID)

	// The staff wall: the counter's own bearer reads the ticket's totals.
	rec := s.do(http.MethodPost, "/api/v1/pos/sale-preview", s.bearer(trentonClerkSub), "", ticket)
	if rec.Code != http.StatusOK {
		t.Fatalf("the staff wall must read the preview: got %d: %s", rec.Code, rec.Body.String())
	}

	// The wall the sale refuses is the wall the preview refuses: a
	// foreign org's bearer takes the shared 403 body on both routes for
	// the same credential, so the preview opens no door the sale keeps
	// shut.
	foreign := s.bearer(elsewhereSub)
	rec = s.do(http.MethodPost, "/api/v1/pos/sale-preview", foreign, "", ticket)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a foreign org's bearer must take the 403 at the preview: got %d: %s", rec.Code, rec.Body.String())
	}
	if body := s.decodeBody(rec); body["error"] != "Forbidden" {
		t.Fatalf("the preview's 403 must be the shared pinned body: %v", body)
	}
	rec = s.do(http.MethodPost, "/api/v1/pos/sales", foreign, "", ringTicket(trentonBranchID, s.trentonTill, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("the same credential must take the 403 at the sale too: got %d: %s", rec.Code, rec.Body.String())
	}
	if body := s.decodeBody(rec); body["error"] != "Forbidden" {
		t.Fatalf("the sale's 403 must be the same shared body: %v", body)
	}

	// No bearer: the shared 401 body.
	rec = previewNoBearerCall(s, http.MethodPost, "/api/v1/pos/sale-preview")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer must take the shared 401: got %d: %s", rec.Code, rec.Body.String())
	}
	if body := s.decodeBody(rec); body["error"] != "Missing or invalid credentials" {
		t.Fatalf("the preview's 401 must be the shared pinned body: %v", body)
	}

	// An empty body: the no-lines refusal in words, the same clear code
	// the sale answers.
	rec = s.do(http.MethodPost, "/api/v1/pos/sale-preview", s.bearer(trentonClerkSub), "", map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an empty body must be the clear 400: got %d: %s", rec.Code, rec.Body.String())
	}
	if body := s.decodeBody(rec); body["error"] != "a sale needs at least one line" {
		t.Fatalf("an empty body must be refused in words: %v", body)
	}
}
