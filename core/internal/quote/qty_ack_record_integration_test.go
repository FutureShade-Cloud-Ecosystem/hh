//go:build integration

// qty_ack_record_integration_test.go: the acceptance record is the dealer's evidence in a dispute about
// quantities, so it keeps what the customer was shown and agreed to: the lines as accepted and the
// notice wording that stood on the page. Later edits to the quote or the wording leave it untouched.
package quote

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestIntegration_QtyAck_RecordKeepsTheLinesAndTheNotice(t *testing.T) {
	pool := intPool(t)
	now := time.Now().UTC().Truncate(time.Second)
	qtyNoticeEnsureDealerRow(t, pool)
	qtyNoticeSet(t, pool, "", "Words in force at the accept.")
	svc, quoteID, token := mintedFor(t, pool, now, nil)

	if w := qtyAckPost(t, svc, token, "", `{"typed_name":"Dana Reyes","accepted":true,"quantities_confirmed":true}`); w.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", w.Code, w.Body.String())
	}
	// Wording and lines move on after the accept; the record must not.
	if _, err := pool.Exec(context.Background(), `UPDATE quote_policy SET quantity_notice = 'Changed later.' WHERE branch_id IS NULL`); err != nil {
		t.Fatalf("change wording: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE quote_lines SET qty_entered = qty_entered + 5 WHERE quote_id = $1`, quoteID); err != nil {
		t.Fatalf("change line: %v", err)
	}

	var notice string
	var linesRaw []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT quantity_notice, accepted_lines FROM quote_acceptances WHERE quote_id=$1`, quoteID).Scan(&notice, &linesRaw); err != nil {
		t.Fatalf("read record: %v", err)
	}
	if notice != "Words in force at the accept." {
		t.Errorf("stored notice = %q, want the wording that stood at the accept", notice)
	}
	var lines []map[string]any
	if err := json.Unmarshal(linesRaw, &lines); err != nil || len(lines) != 1 {
		t.Fatalf("stored lines = %s (%v), want the one line accepted", linesRaw, err)
	}
	l := lines[0]
	for _, k := range []string{"line_no", "description", "qty_entered", "uom_entered", "unit_price_cents", "line_total_cents"} {
		if _, ok := l[k]; !ok {
			t.Errorf("stored line lacks %s: %v", k, l)
		}
	}
	var qty string
	_ = pool.QueryRow(context.Background(), `SELECT qty_entered::text FROM quote_lines WHERE quote_id=$1`, quoteID).Scan(&qty)
	if l["qty_entered"] == qty {
		t.Errorf("the stored quantity %v moved with the edit to %s; it must be the accepted one", l["qty_entered"], qty)
	}

	// The staff read returns both, so the dispute answer is one read away.
	hist := qtyAckHistory(t, svc, quoteID)
	if len(hist) != 1 || hist[0]["quantity_notice"] != "Words in force at the accept." {
		t.Fatalf("history must carry the stored notice: %v", hist)
	}
	if hl, _ := hist[0]["lines"].([]any); len(hl) != 1 {
		t.Errorf("history must carry the accepted lines: %v", hist[0]["lines"])
	}
}
