//go:build integration

// qty_notice_integration_test.go: every quote carries a dealer notice that the quantities are the
// customer's to confirm. The wording is a dealer text setting on quote_policy (branch over dealer
// default), with a default, carried on the public view, the staff preview of it and the staff quote.
package quote

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const qtyNoticeDefault = "All quantities are to be confirmed by the customer. Please review them before you accept it."

// qtyNoticeEnsureDealerRow makes sure the dealer default row exists: another suite's cleanup deletes it.
func qtyNoticeEnsureDealerRow(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO quote_policy (branch_id, send_sms_enabled)
		SELECT NULL, TRUE WHERE NOT EXISTS (SELECT 1 FROM quote_policy WHERE branch_id IS NULL)`); err != nil {
		t.Fatalf("ensure dealer row: %v", err)
	}
}

func qtyNoticeOf(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	s, _ := m["quantity_notice"].(string)
	return s
}

// qtyNoticeSet writes the notice straight to the dealer default row and restores the column default
// afterwards, so the tests do not leak into each other.
func qtyNoticeSet(t *testing.T, pool *pgxpool.Pool, branchID, text string) {
	t.Helper()
	ctx := context.Background()
	qtyNoticeEnsureDealerRow(t, pool)
	if branchID == "" {
		if _, err := pool.Exec(ctx, `UPDATE quote_policy SET quantity_notice = $1 WHERE branch_id IS NULL`, text); err != nil {
			t.Fatalf("set dealer notice: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `UPDATE quote_policy SET quantity_notice = DEFAULT WHERE branch_id IS NULL`)
		})
		return
	}
	if _, err := pool.Exec(ctx, `INSERT INTO quote_policy (branch_id, quantity_notice) VALUES ($1, $2)
		ON CONFLICT (branch_id) DO UPDATE SET quantity_notice = EXCLUDED.quantity_notice`, branchID, text); err != nil {
		t.Fatalf("set branch notice: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM quote_policy WHERE branch_id = $1`, branchID) })
}

func TestIntegration_QtyNotice_DefaultOnEveryRead(t *testing.T) {
	pool := intPool(t)
	now := time.Now().UTC().Truncate(time.Second)
	qtyNoticeEnsureDealerRow(t, pool)
	svc, quoteID, token := mintedFor(t, pool, now, nil)
	staff := qCtxAs(intStaff(intTrenton))

	pub, err := svc.PublicQuoteByToken(context.Background(), token)
	if err != nil {
		t.Fatalf("public: %v", err)
	}
	if got := qtyNoticeOf(t, pub); got != qtyNoticeDefault {
		t.Errorf("public view notice = %q, want the default", got)
	}
	prev, err := svc.CustomerViewPreview(staff, quoteID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if got := qtyNoticeOf(t, prev); got != qtyNoticeDefault {
		t.Errorf("preview notice = %q, want the default", got)
	}
	sv, err := svc.StaffQuoteRead(staff, quoteID)
	if err != nil {
		t.Fatalf("staff read: %v", err)
	}
	if got := qtyNoticeOf(t, sv); got != qtyNoticeDefault {
		t.Errorf("staff quote notice = %q, want the default", got)
	}
}

func TestIntegration_QtyNotice_DealerWordingAndBranchOverride(t *testing.T) {
	pool := intPool(t)
	now := time.Now().UTC().Truncate(time.Second)
	svc, quoteID, token := mintedFor(t, pool, now, nil)
	staff := qCtxAs(intStaff(intTrenton))

	qtyNoticeSet(t, pool, "", "Check every quantity before you accept.")
	pub, _ := svc.PublicQuoteByToken(context.Background(), token)
	if got := qtyNoticeOf(t, pub); got != "Check every quantity before you accept." {
		t.Errorf("dealer wording not served: %q", got)
	}

	var branch string
	if err := pool.QueryRow(context.Background(), `SELECT branch_id::text FROM quotes WHERE id=$1`, quoteID).Scan(&branch); err != nil {
		t.Fatalf("quote branch: %v", err)
	}
	qtyNoticeSet(t, pool, branch, "Branch wording on quantities.")
	pub, _ = svc.PublicQuoteByToken(context.Background(), token)
	if got := qtyNoticeOf(t, pub); got != "Branch wording on quantities." {
		t.Errorf("the branch row must win: %q", got)
	}
	sv, _ := svc.StaffQuoteRead(staff, quoteID)
	if got := qtyNoticeOf(t, sv); got != "Branch wording on quantities." {
		t.Errorf("staff quote must follow the branch row: %q", got)
	}
}

// The policy save validates in words, and an empty value goes back to the default wording.
func TestIntegration_QtyNotice_PolicyWrite(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	qtyNoticeEnsureDealerRow(t, pool)
	gm := qd1User(t, pool, "qtynotice")
	svc := qd1Svc(t, pool, now)
	ctx := qd1WithRole(qCtxAs(intStaff(intTrenton)), "GENERAL_MANAGER", intTrenton, gm)
	// The save writes every knob of the dealer row, so put the row back as the seed makes it.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM quote_policy WHERE branch_id IS NULL`)
		_, _ = pool.Exec(context.Background(), `INSERT INTO quote_policy (branch_id, send_sms_enabled) VALUES (NULL, TRUE)`)
	})

	put := func(notice string) (QuotePolicy, error) {
		var p QuotePolicy
		raw, _ := json.Marshal(map[string]any{"cost_visibility": "MANAGER_UP", "quantity_notice": notice})
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatalf("build policy: %v", err)
		}
		return svc.UpdateQuotePolicy(ctx, p)
	}
	p, err := put("  Please check each quantity.  ")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := qtyNoticeOf(t, p); got != "Please check each quantity." {
		t.Errorf("saved notice = %q, want it trimmed", got)
	}
	p, err = put("   ")
	if err != nil {
		t.Fatalf("save blank: %v", err)
	}
	if got := qtyNoticeOf(t, p); got != qtyNoticeDefault {
		t.Errorf("a blank notice must go back to the default; got %q", got)
	}
	if _, err := put(strings.Repeat("x", 501)); err == nil {
		t.Error("a notice over the bound must be refused in words")
	}
}
