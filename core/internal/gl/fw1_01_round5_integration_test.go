//go:build integration

// fw1_01_round5_integration_test.go, PR 614 round 5's red first leg: the
// `case y > maxYear:` arm of insertJournalIn says a date beyond the next year
// refuses but has no return, so the write falls through with no period, the
// header insert gets period_id = "" and dies on the uuid cast (an opaque
// 500, and next_entry_no mints a number the rollback must take back). After
// the fix a SYSTEM journal dated more than one year past the books' latest
// year is refused 422 in words, and nothing is written.
package gl

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// TestIntegration_FW1_01R5_FarFutureSystemJournalIsRefusedInWordsAndWritesNoRow
// is round 5's red: a SYSTEM journal dated two years past the latest year the
// books hold must answer the 422 refusal in words and write no row (no
// journal, no lines). Today the missing return lets the write fall through and
// the header insert dies on the uuid cast of the empty period id.
func TestIntegration_FW1_01R5_FarFutureSystemJournalIsRefusedInWordsAndWritesNoRow(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	var maxYear int
	if err := pool.QueryRow(ctx, `SELECT COALESCE(MAX(year), 0) FROM fiscal_periods`).Scan(&maxYear); err != nil {
		t.Fatalf("read the books' latest year: %v", err)
	}
	if maxYear == 0 {
		t.Fatalf("the books hold no fiscal periods; this leg needs the migrated and seeded database")
	}
	far := maxYear + 2
	want := fmt.Sprintf("gl: no fiscal period for %04d-%02d: a date more than one year past the books' latest year is refused", far, 1)
	key := fmt.Sprintf("fw1-01-r5-far-future-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.idempotency_key=$1`, key)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key=$1`, key)
	})
	clockAt := time.Date(far, 1, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, clockAt)

	_, err := svc.PostExternal(ctx, ExternalJournal{
		Source: SourceInvoice, IdempotencyKey: key,
		Memo:         "FW1-01 round 5: a system journal dated far past the books",
		DocumentDate: clockAt,
		Lines: []ExternalLine{
			{AccountCode: "1000", DebitCents: 1200, Memo: "bank"},
			{AccountCode: "4000", CreditCents: 1200, Memo: "revenue"},
		},
	})
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 422 || ce.msg != want {
		t.Fatalf("a SYSTEM journal dated %04d-01-15, two years past the books' latest year %d, answered %v, want the 422 refusal %q", far, maxYear, err, want)
	}

	var journals, lines int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE idempotency_key=$1`, key).Scan(&journals); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.idempotency_key=$1`, key).Scan(&lines); err != nil {
		t.Fatal(err)
	}
	if journals != 0 || lines != 0 {
		t.Fatalf("the refused write left %d journals and %d lines behind; a refusal writes nothing", journals, lines)
	}
}
