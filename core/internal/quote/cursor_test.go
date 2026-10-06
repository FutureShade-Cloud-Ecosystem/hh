//go:build integration

// cursor_test.go, the quote list cursor legs, the billing invoice cursor's siblings (row 804).
//
// Both quote list reads order created_at DESC, quote_no ASC, but their cursor predicates compared
// the row (created_at, quote_no) < (cursor created, cursor no), a descending comparison on both
// keys: a page ending past the first quote of a timestamp skipped every same-timestamp quote with
// a greater number. Each leg plants three quotes at ONE created_at with numbers ...100, ...101,
// ...102, pages from the first quote's cursor pair, and requires the next page to carry the two
// greater numbers.
//
// Run: DATABASE_URL=... go test -tags=integration ./internal/quote/... -run 'QuoteListCursor|StaffQuoteListCursor' -count=1
package quote

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedCursorQuotes plants one customer with three quotes at ONE created_at whose quote numbers
// carry the per-run namespace plus the digits 100, 101, 102, and registers cleanup. quote_no is
// UNIQUE across the table, so the per-run namespace keeps the legs collision free on a shared
// gate; the digits ride the shared prefix, so the text comparison of the numbers is the numeric
// comparison: ...100 < ...101 < ...102.
func seedCursorQuotes(t *testing.T, pool *pgxpool.Pool) (cust, ns string) {
	t.Helper()
	ctx := context.Background()
	ns = fmt.Sprintf("qtecur-%d", time.Now().UnixNano()%1_000_000_000)
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type) VALUES ($1, 'Quote cursor leg tenant', 'CONTRACTOR') RETURNING id::text`,
		ns).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM quotes WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})

	// One fixed instant for all three rows: rows written by one transaction share created_at, so
	// this is the shape a real page boundary lands on.
	stamp := time.Date(1990, 3, 15, 12, 0, 0, 0, time.UTC)
	for _, no := range []string{"100", "101", "102"} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO quotes (quote_no, customer_id, branch_id, status, subtotal_cents, tax_cents, total_cents, created_at, updated_at)
			 VALUES ($1, $2::uuid, $3::uuid, 'SENT', 2000, 0, 2000, $4::timestamptz, $4::timestamptz)`,
			ns+"-"+no, cust, intTrenton, stamp); err != nil {
			t.Fatalf("seed quote %s: %v", no, err)
		}
	}
	return cust, ns
}

// wantCursorNumbers checks that rows (a namespace-filtered page, in list order) are exactly the
// namespace's 100, 101, 102, and returns the rows' numbers for the failure message.
func checkOwnOrder(t *testing.T, got []string, ns string) {
	t.Helper()
	want := []string{ns + "-100", ns + "-101", ns + "-102"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("page = %v, want [%s %s %s] (created_at DESC, quote_no ASC)", got, want[0], want[1], want[2])
	}
}

func TestIntegration_QuoteListCursor_SameTimestampGreaterNumbers(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	cust, ns := seedCursorQuotes(t, pool)

	// Page one: the tenant's three in tiebreak order, and the cursor is the FIRST row's pair.
	page1, err := repo.ListQuotes(ctx, cust, Filter{Limit: 50})
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	nums1 := make([]string, 0, len(page1))
	for _, q := range page1 {
		nums1 = append(nums1, q.QuoteNo)
	}
	checkOwnOrder(t, nums1, ns)

	// Page two from the first quote's pair: the same-timestamp quotes with a GREATER number. The
	// buggy predicate wants both keys strictly less, so it drops exactly these rows (and would
	// re-serve lower ones); the fixed two-part predicate keeps created_at = cursor AND quote_no >
	// cursor number.
	cursorCreated := page1[0].CreatedAt.Format(time.RFC3339Nano)
	cursorQuoteNo := page1[0].QuoteNo
	page2, err := repo.ListQuotes(ctx, cust, Filter{
		Limit:         50,
		AfterCursor:   true,
		CursorCreated: cursorCreated,
		CursorQuoteNo: cursorQuoteNo,
	})
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	got := make([]string, 0, len(page2))
	for _, q := range page2 {
		got = append(got, q.QuoteNo)
	}
	want := []string{ns + "-101", ns + "-102"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("cursor at (created_at %s, quote_no %s): next page = %v, want [%s %s]; the page lost the same-timestamp quotes with the greater quote numbers %s and %s",
			cursorCreated, cursorQuoteNo, got, want[0], want[1], ns+"-101", ns+"-102")
	}
}

func TestIntegration_StaffQuoteListCursor_SameTimestampGreaterNumbers(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	_, ns := seedCursorQuotes(t, pool)

	// Page one: the branch list (the staff read's scope) may also carry seeded quotes, so the
	// ordering assertion runs on this run's own rows, in list order.
	page1, err := repo.ListStaffQuotes(ctx, false, []string{intTrenton}, "", Filter{Limit: 50})
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	nums1 := make([]string, 0, len(page1))
	for _, q := range page1 {
		if strings.HasPrefix(q.QuoteNo, ns+"-") {
			nums1 = append(nums1, q.QuoteNo)
		}
	}
	checkOwnOrder(t, nums1, ns)
	if len(page1) < 3 {
		t.Fatalf("page 1 = %d rows, want at least the 3 planted in this run", len(page1))
	}

	// The cursor is the FIRST of this run's rows, taken from the summary's own minted key (the
	// exact shape production rides the wire).
	var cursorKey string
	for _, q := range page1 {
		if q.QuoteNo == ns+"-100" {
			cursorKey = q.cursorKey
			break
		}
	}
	if cursorKey == "" {
		t.Fatalf("page 1 lost this run's first quote %q", ns+"-100")
	}
	i := strings.LastIndex(cursorKey, "/")
	cursorCreated, cursorQuoteNo := cursorKey[:i], cursorKey[i+1:]

	// Page two from that pair: the same-timestamp quotes with a GREATER number. The buggy
	// predicate wants both keys strictly less, so it drops exactly these rows; the fixed two-part
	// predicate keeps q.created_at = cursor AND q.quote_no > cursor number.
	page2, err := repo.ListStaffQuotes(ctx, false, []string{intTrenton}, "", Filter{
		Limit:         50,
		AfterCursor:   true,
		CursorCreated: cursorCreated,
		CursorQuoteNo: cursorQuoteNo,
	})
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	got := make([]string, 0, len(page2))
	for _, q := range page2 {
		if strings.HasPrefix(q.QuoteNo, ns+"-") {
			got = append(got, q.QuoteNo)
		}
	}
	want := []string{ns + "-101", ns + "-102"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("cursor at (created_at %s, quote_no %s): next page (this run's rows) = %v, want [%s %s]; the page lost the same-timestamp quotes with the greater quote numbers %s and %s",
			cursorCreated, cursorQuoteNo, got, want[0], want[1], ns+"-101", ns+"-102")
	}
}
