//go:build integration

// seqtrigger_drawbeforelock_integration_test.go: the draw before lock leg for customer_quote (PR 556, round 3c).
// The draft edit used to draw the customer_quote counter in a CTE and then lock the row; a writer that relies
// on the sync seq trigger takes the row first. The draft edit now relies on the trigger too. The trigger
// relying writer (a plain touch) is parked between its row lock and its draw with a BEFORE INSERT trigger on
// scope_sequences (testkit.DrawPark); the real draft edit runs on the same quote. Red at 099f2e27: the edit
// draws the counter, waits for the row, the park releases, the touch wants the counter, 40P01. After the fix
// the edit still answers the row it wrote with the seq the trigger drew (RETURNING carries no seq column
// here, so the leg reads the stored seq before and after).
package customerquote

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

const (
	dblOrgID    = "a0e20000-0000-4000-8000-0000000000d1"
	dblTenantID = "a0e20000-0000-4000-8000-0000000000d2"
)

func TestIntegration_DrawBeforeLock_DraftEditBesideAQuoteWrite(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("customerquote draw before lock leg: DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	for _, q := range []string{
		`INSERT INTO org (id, slug, name) VALUES ('` + dblOrgID + `', 'customerquote-draw-before-lock', 'draw before lock')`,
		`INSERT INTO customers (id, account_no, name, org_id, seq) VALUES ('` + dblTenantID + `', 'CQDBL-1', 'draw before lock tenant', '` + dblOrgID + `', 1)`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM customer_quote WHERE org_id = $1`, dblOrgID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, dblTenantID)
		_, _ = pool.Exec(c, `DELETE FROM org WHERE id = $1`, dblOrgID)
	})

	store, err := NewStore(pool)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	in := QuoteRow{OrgID: dblOrgID, TenantID: dblTenantID, LinesJSON: []byte(`[]`), LabourJSON: []byte(`[]`),
		OverheadJSON: []byte(`[]`), CustomerContactJSON: []byte(`{}`)}
	row, err := store.InsertQuote(ctx, pool, in, 1)
	if err != nil {
		t.Fatalf("insert quote: %v", err)
	}
	var before int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(seq, 0) FROM customer_quote WHERE id = $1::uuid`, row.ID).Scan(&before); err != nil {
		t.Fatalf("read seq: %v", err)
	}

	park := testkit.NewDrawPark(t, pool, ScopeCustomerQuote, 55010008)
	touch := park.TouchRow("customer_quote", "updated_at = now()", row.ID)
	park.WaitParked("the quote touch stands between its row lock and its draw")

	in.MarkupBps = 250
	edit := make(chan error, 1)
	go func() {
		_, err := store.UpdateDraftQuote(context.Background(), pool, dblTenantID, dblOrgID, row.ID, in)
		edit <- err
	}()
	park.WaitBlocked("the draft edit waits")

	park.Answers(map[string]chan error{"touch": touch, "edit": edit})

	var after, markup int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(seq, 0), markup_bps FROM customer_quote WHERE id = $1::uuid`, row.ID).Scan(&after, &markup); err != nil {
		t.Fatalf("read seq: %v", err)
	}
	if markup != 250 || after <= before {
		t.Fatalf("after the edit the quote reads markup %d seq %d (was %d): the trigger must have drawn", markup, after, before)
	}
}
