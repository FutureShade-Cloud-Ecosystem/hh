//go:build integration

// qty_ack_staff_integration_test.go: a staff accept on the customer's behalf writes no quote_acceptances
// row (that table is the customer's own record), so the staff quote's acceptance history projects the
// STAFF_QUOTE_DECISION audit rows beside the customer's acceptances.
package quote

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func qtyAckHistory(t *testing.T, svc *StaffService, quoteID string) []map[string]any {
	t.Helper()
	view, err := svc.StaffQuoteRead(qCtxAs(intStaff(intTrenton)), quoteID)
	if err != nil {
		t.Fatalf("staff read: %v", err)
	}
	raw, _ := json.Marshal(view)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	var out []map[string]any
	for _, r := range m["acceptances"].([]any) {
		out = append(out, r.(map[string]any))
	}
	return out
}

func TestIntegration_QtyAck_StaffAcceptReadsInTheHistory(t *testing.T) {
	pool := intPool(t)
	now := time.Now().UTC().Truncate(time.Second)
	_, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
	svc := shareSvc(t, pool, now)

	if h := qtyAckHistory(t, svc, quoteID); len(h) != 0 {
		t.Fatalf("a quote nobody accepted has an empty history; got %v", h)
	}
	if _, err := svc.AcceptOnBehalf(decisionCaller("INSIDE_SALES", intTrenton), quoteID, StaffAcceptIn{
		Channel: "phone", Note: "Called back, wants it Friday", QuantitiesConfirmed: true,
		ExpectedTotalCents: i64p(2000), IdempotencyKey: decKeyA,
	}); err != nil {
		t.Fatalf("staff accept: %v", err)
	}
	h := qtyAckHistory(t, svc, quoteID)
	if len(h) != 1 {
		t.Fatalf("a staff accepted quote must read one history row, got %v", h)
	}
	r := h[0]
	if r["source"] != "STAFF_ON_BEHALF" || r["channel"] != "phone" || r["quantities_confirmed"] != true ||
		r["amount_cents"] != float64(2000) || r["note"] != "Called back, wants it Friday" || r["accepted_at"] == nil {
		t.Errorf("staff history row wrong: %v", r)
	}
	if _, has := r["typed_name"]; has {
		t.Errorf("a staff row carries no typed name, the customer typed none: %v", r)
	}
}

// A staff row and a customer's own acceptance (a repeat, multi-accept on) read side by side in time order,
// each as what it is.
func TestIntegration_QtyAck_CustomerAndStaffRowsSideBySide(t *testing.T) {
	pool := intPool(t)
	now := time.Now().UTC().Truncate(time.Second)
	setMultiAccept(t, pool, true)
	svc, quoteID, token := mintedFor(t, pool, now, nil)
	if _, err := svc.AcceptOnBehalf(decisionCaller("INSIDE_SALES", intTrenton), quoteID, StaffAcceptIn{
		Channel: "email", QuantitiesConfirmed: true, ExpectedTotalCents: i64p(2000), IdempotencyKey: decKeyB,
	}); err != nil {
		t.Fatalf("staff accept: %v", err)
	}
	later := shareSvc(t, pool, now.Add(time.Minute))
	if w := qtyAckPost(t, later, token, "", `{"typed_name":"Dana Reyes","accepted":true,"quantities_confirmed":true}`); w.Code != http.StatusOK {
		t.Fatalf("customer accept: %d %s", w.Code, w.Body.String())
	}
	h := qtyAckHistory(t, svc, quoteID)
	if len(h) != 2 || h[0]["source"] != "STAFF_ON_BEHALF" || h[1]["source"] != "CUSTOMER" || h[1]["typed_name"] != "Dana Reyes" {
		t.Fatalf("want the staff row then the customer row, got %v", h)
	}
	if h[1]["amount_cents"] != float64(2000) {
		t.Errorf("the customer row carries the accepted total too: %v", h[1])
	}
}
