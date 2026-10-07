//go:build integration

// qty_ack_integration_test.go: a customer may accept a quote only after confirming the quantities
// (the Dibbits team's rule: customers blame the dealer for quantities they chose). The public accept
// body carries quantities_confirmed beside typed_name and accepted; it is refused in words with 422
// when absent or false, stored on the acceptance record, echoed on the confirmation, and read back on
// the staff quote. A keyed replay keeps the answer it first gave.
package quote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const qtyAckKey = "5c1d2e3f-4a50-4b61-8c72-93a4b5c6d7e8"

func qtyAckPost(t *testing.T, svc *StaffService, token, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/public/v1/quotes/"+token+"/accept",
		strings.NewReader(body)).WithContext(context.Background())
	req.SetPathValue("token", token)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	NewStaffHandler(svc).publicAccept(w, req)
	return w
}

func qtyAckRows(t *testing.T, pool *pgxpool.Pool, quoteID string) (n int, confirmed []bool) {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT quantities_confirmed FROM quote_acceptances WHERE quote_id=$1 ORDER BY accepted_at`, quoteID)
	if err != nil {
		t.Fatalf("read acceptance rows: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c bool
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("scan: %v", err)
		}
		confirmed = append(confirmed, c)
	}
	return len(confirmed), confirmed
}

// Absent or false is refused in words with 422 and writes nothing.
func TestIntegration_QtyAck_RefusedWithoutConfirmation(t *testing.T) {
	pool := intPool(t)
	now := time.Now().UTC().Truncate(time.Second)
	cases := map[string]string{
		"absent": `{"typed_name":"Dana Reyes","accepted":true}`,
		"false":  `{"typed_name":"Dana Reyes","accepted":true,"quantities_confirmed":false}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			svc, quoteID, token := mintedFor(t, pool, now, nil)
			w := qtyAckPost(t, svc, token, "", body)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body %s", w.Code, w.Body.String())
			}
			if !strings.Contains(strings.ToLower(w.Body.String()), "quantities") {
				t.Errorf("the refusal must say it is the quantities; got %s", w.Body.String())
			}
			if got := qd3dStatus(t, pool, quoteID); got != "SENT" {
				t.Errorf("a refused acceptance must leave the quote SENT; got %s", got)
			}
			if n := acceptanceRows(t, pool, quoteID); n != 0 {
				t.Errorf("a refused acceptance must write no row; got %d", n)
			}
		})
	}
}

// A confirmed acceptance is stored, echoed and read back on the staff quote.
func TestIntegration_QtyAck_StoredEchoedAndReadBack(t *testing.T) {
	pool := intPool(t)
	now := time.Now().UTC().Truncate(time.Second)
	svc, quoteID, token := mintedFor(t, pool, now, nil)

	w := qtyAckPost(t, svc, token, "", `{"typed_name":"Dana Reyes","accepted":true,"quantities_confirmed":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body.String())
	}
	var echo map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &echo); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if echo["quantities_confirmed"] != true {
		t.Errorf("the confirmation must echo quantities_confirmed true; got %v", echo["quantities_confirmed"])
	}
	if n, conf := qtyAckRows(t, pool, quoteID); n != 1 || !conf[0] {
		t.Fatalf("want one stored row with quantities_confirmed true; got %d %v", n, conf)
	}

	view, err := svc.StaffQuoteRead(qCtxAs(intStaff(intTrenton)), quoteID)
	if err != nil {
		t.Fatalf("staff read: %v", err)
	}
	raw, _ := json.Marshal(view)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	hist, _ := m["acceptances"].([]any)
	if len(hist) != 1 {
		t.Fatalf("the staff quote must carry the acceptance history; got %v", m["acceptances"])
	}
	row, _ := hist[0].(map[string]any)
	if row["typed_name"] != "Dana Reyes" || row["quantities_confirmed"] != true || row["accepted_at"] == nil {
		t.Errorf("acceptance history row wrong: %v", row)
	}
}

// A keyed replay answers the first outcome (confirmation included), writes no second row.
func TestIntegration_QtyAck_ReplayKeepsItsAnswer(t *testing.T) {
	pool := intPool(t)
	now := time.Now().UTC().Truncate(time.Second)
	svc, quoteID, token := mintedFor(t, pool, now, nil)
	body := `{"typed_name":"Dana Reyes","accepted":true,"quantities_confirmed":true}`

	first := qtyAckPost(t, svc, token, qtyAckKey, body)
	again := qtyAckPost(t, svc, token, qtyAckKey, body)
	if first.Code != http.StatusOK || again.Code != http.StatusOK {
		t.Fatalf("both must be 200; got %d and %d (%s)", first.Code, again.Code, again.Body.String())
	}
	if first.Body.String() != again.Body.String() {
		t.Errorf("the replay must repeat the first answer\n first %s\n again %s", first.Body.String(), again.Body.String())
	}
	if !strings.Contains(again.Body.String(), `"quantities_confirmed":true`) {
		t.Errorf("the replay must carry quantities_confirmed true; got %s", again.Body.String())
	}
	if n, _ := qtyAckRows(t, pool, quoteID); n != 1 {
		t.Errorf("a replay must not add a row; got %d", n)
	}
}
