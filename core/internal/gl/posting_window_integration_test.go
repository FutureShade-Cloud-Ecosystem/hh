//go:build integration

package gl

// posting_window_integration_test.go, the posting window's answer against the posting path it
// mirrors. The window is a read of the same rule the posting path applies, so the two must
// agree: a date the window refuses is refused by the real posting with the same words, and a
// date the window allows is taken.
//
// Without this leg the window is a mirror nobody checks, and a mirror that drifts refuses a
// date the books would take, which would stop a payment the books would have posted.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// TestIntegration_GL_PostingDateRefusal_MatchesThePostingPath ties the window to the posting
// path: the far date is refused by both, with the same words and no row written, and the next
// year, the year the books would open at the latest, is taken by both.
func TestIntegration_GL_PostingDateRefusal_MatchesThePostingPath(t *testing.T) {
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
	svc := svcAt(pool, identity.StaffContext{
		UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeGlobal,
	}, time.Now())

	// THE REFUSED HALF: two years past the books' latest year.
	far := maxYear + 2
	farAt := time.Date(far, 1, 15, 12, 0, 0, 0, time.UTC)
	reason, err := svc.PostingDateRefusal(ctx, farAt)
	if err != nil {
		t.Fatalf("read the posting window: %v", err)
	}
	want := fmt.Sprintf("no fiscal period for %04d-%02d: a date more than one year past the books' latest year is refused", far, 1)
	if reason != want {
		t.Fatalf("the posting window answered %q for %04d-01-15, want %q", reason, far, want)
	}
	key := fmt.Sprintf("gl-window-far-%d", time.Now().UnixNano())
	_, err = svc.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: key,
		Memo:         "the posting window's far future date",
		DocumentDate: farAt,
		Lines: []ExternalLine{
			{AccountCode: "1000", DebitCents: 1200, Memo: "cash"},
			{AccountCode: "4000", CreditCents: 1200, Memo: "revenue"},
		},
	})
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 422 {
		t.Fatalf("the posting path answered %v for %04d-01-15, want the 422 refusal the window named", err, far)
	}
	if !strings.Contains(ce.msg, want) {
		t.Fatalf("the posting path answered %q, want it to carry the window's words %q", ce.msg, want)
	}
	var journals int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE idempotency_key=$1`, key).Scan(&journals); err != nil {
		t.Fatal(err)
	}
	if journals != 0 {
		t.Fatalf("the refused write left %d journals behind; a refusal writes nothing", journals)
	}

	// THE ALLOWED HALF: the year the books would open at the latest is a date they would post
	// on, and the posting path takes it. The year is planted for this leg alone and wiped on
	// cleanup, journal and all.
	next := maxYear + 1
	ensurePeriods(t, pool, next)
	nextAt := time.Date(next, 3, 15, 12, 0, 0, 0, time.UTC)
	reason, err = svc.PostingDateRefusal(ctx, nextAt)
	if err != nil {
		t.Fatalf("read the posting window: %v", err)
	}
	if reason != "" {
		t.Fatalf("the posting window refused %04d-03-15 with %q, want no refusal", next, reason)
	}
	okKey := fmt.Sprintf("gl-window-next-%d", time.Now().UnixNano())
	if _, err := svc.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: okKey,
		Memo:         "the posting window's allowed date",
		DocumentDate: nextAt,
		Lines: []ExternalLine{
			{AccountCode: "1000", DebitCents: 1200, Memo: "cash"},
			{AccountCode: "4000", CreditCents: 1200, Memo: "revenue"},
		},
	}); err != nil {
		t.Fatalf("the posting path refused %04d-03-15, which the window allowed: %v", next, err)
	}
}
