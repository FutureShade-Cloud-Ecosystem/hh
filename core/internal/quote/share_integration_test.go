//go:build integration

// share_integration_test.go, DIB-57 share links + acceptance against a LIVE Postgres.
//
// The token lifecycle is the whole feature, so it is tested as a lifecycle: mint → view → accept, plus
// each way a link can be dead and what each of those must answer.
package quote

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/jackc/pgx/v5/pgxpool"
)

// shareSvc composes the real repository with the share store wired.
func shareSvc(t *testing.T, pool *pgxpool.Pool, now time.Time) *StaffService {
	t.Helper()
	svc, repo, _ := intSvc(t, pool, now)
	return svc.WithShares(repo)
}

func cleanupShares(t *testing.T, pool *pgxpool.Pool, quoteID string) {
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM quote_acceptances WHERE quote_id = $1`, quoteID)
		_, _ = pool.Exec(c, `DELETE FROM quote_share_link_events WHERE share_link_id IN
			(SELECT id FROM quote_share_links WHERE quote_id = $1)`, quoteID)
		_, _ = pool.Exec(c, `DELETE FROM quote_share_links WHERE quote_id = $1`, quoteID)
	})
}

// - the happy lifecycle -

func TestIntegration_ShareLink_MintViewAccept(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
	cleanupShares(t, pool, quoteID)
	svc := shareSvc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))

	minted, err := mintAndSend(svc, ctx, quoteID, 0)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if minted.Token == "" {
		t.Fatal("mint must return the raw token exactly once")
	}
	// D4: minting a link on a DRAFT quote SENDS it, that edge had no implementation before.
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM quotes WHERE id=$1`, quoteID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "SENT" {
		t.Errorf("minting the first link must move DRAFT -> SENT; status is %q", status)
	}
	// …and that transition must be on the change trail like any other.
	hist, err := svc.QuoteEditHistory(ctx, quoteID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	var sawSend bool
	for _, c := range hist.Changes {
		if c.Field == "status" && c.Before == "DRAFT" && c.After == "SENT" {
			sawSend = true
		}
	}
	if !sawSend {
		t.Error("the DRAFT -> SENT transition must be recorded on the change trail")
	}

	// The public view resolves from the RAW token.
	view, err := svc.PublicQuoteByToken(context.Background(), minted.Token)
	if err != nil {
		t.Fatalf("public view: %v", err)
	}
	if view.QuoteNo == "" || len(view.Lines) != 1 {
		t.Errorf("public view is not populated: %+v", view)
	}
	if !view.Acceptable {
		t.Error("a live SENT quote inside its window must be acceptable")
	}
	// C2: the page states the numbers' vintage.
	if view.AsOf.IsZero() {
		t.Error("the public view must carry as-of (updated_at) so a stale link is visibly stale")
	}

	// Accept.
	res, err := svc.AcceptByToken(context.Background(), minted.Token, "  Dana Reyes  ", true, true)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if res.TypedName != "Dana Reyes" {
		t.Errorf("typed name = %q, want it trimmed to Dana Reyes", res.TypedName)
	}
	// C2: the confirmation echoes the totals, so the customer's record does not depend on having
	// read the page carefully.
	if res.TotalCents != view.TotalCents || res.SubtotalCents != view.SubtotalCents {
		t.Errorf("the confirmation must echo the agreed totals: got %+v, page showed %d/%d",
			res, view.SubtotalCents, view.TotalCents)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM quotes WHERE id=$1`, quoteID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "ACCEPTED" {
		t.Errorf("accepting must flip the quote to ACCEPTED; got %q", status)
	}

	// C1: the acceptance record snapshots the money + anchors into the trail, and does NOT read back
	// through the quote at display time.
	var snapNo, snapName string
	var snapSub, snapTax, snapTotal int64
	var auditSeq *int64
	var linkID string
	if err := pool.QueryRow(context.Background(), `
		SELECT quote_no::text, typed_name, accepted_subtotal_cents, accepted_tax_cents,
		       accepted_total_cents, audit_seq, share_link_id::text
		  FROM quote_acceptances WHERE quote_id = $1`, quoteID).
		Scan(&snapNo, &snapName, &snapSub, &snapTax, &snapTotal, &auditSeq, &linkID); err != nil {
		t.Fatalf("read acceptance record: %v", err)
	}
	if snapNo != view.QuoteNo || snapName != "Dana Reyes" {
		t.Errorf("acceptance snapshot wrong: %q / %q", snapNo, snapName)
	}
	if snapSub != view.SubtotalCents || snapTax != view.TaxCents || snapTotal != view.TotalCents {
		t.Errorf("acceptance must SNAPSHOT the agreed money; got %d/%d/%d want %d/%d/%d",
			snapSub, snapTax, snapTotal, view.SubtotalCents, view.TaxCents, view.TotalCents)
	}
	if linkID != minted.ID {
		t.Errorf("acceptance must record WHICH token was used: got %q want %q", linkID, minted.ID)
	}
	if auditSeq == nil || *auditSeq == 0 {
		t.Error("C1: the acceptance must anchor to the audit_log seq of its QUOTE_ACCEPTED row")
	}

	// R4: the view and the accept are both logged against the token.
	var views, accepts int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FILTER (WHERE event='VIEW'), COUNT(*) FILTER (WHERE event='ACCEPT')
		  FROM quote_share_link_events WHERE share_link_id = $1`, minted.ID).
		Scan(&views, &accepts); err != nil {
		t.Fatalf("read events: %v", err)
	}
	if views < 1 || accepts < 1 {
		t.Errorf("every view and accept must be logged; got %d views, %d accepts", views, accepts)
	}
}

// TestIntegration_ShareLink_AcceptanceSurvivesTheQuote is the C1 requirement stated directly: the
// record must stay meaningful when the quote row is gone.
func TestIntegration_ShareLink_AcceptanceSurvivesTheQuote(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
	cleanupShares(t, pool, quoteID)
	svc := shareSvc(t, pool, now)

	minted, err := mintAndSend(svc, qCtxAs(intStaff(intTrenton)), quoteID, 0)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, err := svc.AcceptByToken(context.Background(), minted.Token, "Dana Reyes", true, true); err != nil {
		t.Fatalf("accept: %v", err)
	}
	// Delete the quote out from under the acceptance. quote_lines + share_links cascade; the
	// acceptance must NOT, because it is evidence, not a detail of the document.
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DELETE FROM audit_log WHERE entity_id = $1`, quoteID); err != nil {
		t.Fatalf("clear audit: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM quote_lines WHERE quote_id = $1`, quoteID); err != nil {
		t.Fatalf("clear lines: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM quotes WHERE id = $1`, quoteID); err != nil {
		t.Fatalf("delete quote: %v", err)
	}
	var no, name string
	var total int64
	if err := pool.QueryRow(ctx, `
		SELECT quote_no::text, typed_name, accepted_total_cents
		  FROM quote_acceptances WHERE quote_id = $1`, quoteID).Scan(&no, &name, &total); err != nil {
		t.Fatalf("C1: the acceptance record must outlive the quote, got: %v", err)
	}
	if no == "" || name != "Dana Reyes" || total == 0 {
		t.Errorf("C1: the surviving record must still say what was agreed: %q / %q / %d", no, name, total)
	}
}

// - the ways a link dies -

func TestIntegration_ShareLink_Lifecycle(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	t.Run("expired link is GONE not not-found", func(t *testing.T) {
		_, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
		cleanupShares(t, pool, quoteID)
		svc := shareSvc(t, pool, now)
		minted, err := mintAndSend(svc, qCtxAs(intStaff(intTrenton)), quoteID, 0)
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		// Age the link past its expiry rather than sleeping.
		if _, err := pool.Exec(ctx,
			`UPDATE quote_share_links SET expires_at = $2 WHERE id = $1`,
			minted.ID, now.Add(-time.Hour)); err != nil {
			t.Fatalf("age link: %v", err)
		}
		_, err = svc.PublicQuoteByToken(ctx, minted.Token)
		if !errors.Is(err, ErrLinkGone) {
			t.Fatalf("an expired link must answer GONE (410), got %v", err)
		}
		if _, err := svc.AcceptByToken(ctx, minted.Token, "Dana", true, true); !errors.Is(err, ErrLinkGone) {
			t.Fatalf("an expired link must not accept, got %v", err)
		}
	})

	t.Run("revoked link is GONE", func(t *testing.T) {
		_, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
		cleanupShares(t, pool, quoteID)
		svc := shareSvc(t, pool, now)
		staffCtx := qCtxAs(intStaff(intTrenton))
		minted, err := mintAndSend(svc, staffCtx, quoteID, 0)
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		if err := svc.RevokeShareLink(staffCtx, quoteID, minted.ID); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if _, err := svc.PublicQuoteByToken(ctx, minted.Token); !errors.Is(err, ErrLinkGone) {
			t.Fatalf("a revoked link must answer GONE (410), got %v", err)
		}
	})

	t.Run("unknown token is NOT FOUND", func(t *testing.T) {
		svc := shareSvc(t, pool, now)
		// A well-formed token that was never issued.
		raw, _, err := newShareToken()
		if err != nil {
			t.Fatalf("mint raw: %v", err)
		}
		if _, err := svc.PublicQuoteByToken(ctx, raw); !errors.Is(err, ErrLinkNotFound) {
			t.Fatalf("a token that never existed must answer NOT FOUND (404), got %v", err)
		}
	})

	t.Run("a token grants exactly ONE quote", func(t *testing.T) {
		_, quoteA, _, _ := seedQuote(t, pool, "SENT", nil)
		_, quoteB, _, _ := seedQuote(t, pool, "SENT", nil)
		cleanupShares(t, pool, quoteA)
		cleanupShares(t, pool, quoteB)
		svc := shareSvc(t, pool, now)
		staffCtx := qCtxAs(intStaff(intTrenton))
		mintedA, err := mintAndSend(svc, staffCtx, quoteA, 0)
		if err != nil {
			t.Fatalf("mint A: %v", err)
		}
		viewA, err := svc.PublicQuoteByToken(ctx, mintedA.Token)
		if err != nil {
			t.Fatalf("view A: %v", err)
		}
		var noB string
		if err := pool.QueryRow(ctx, `SELECT quote_no::text FROM quotes WHERE id=$1`, quoteB).
			Scan(&noB); err != nil {
			t.Fatalf("read B: %v", err)
		}
		if viewA.QuoteNo == noB {
			t.Fatal("a token resolved to the wrong quote")
		}
		// Accepting with A's token must accept A, never B.
		if _, err := svc.AcceptByToken(ctx, mintedA.Token, "Dana", true, true); err != nil {
			t.Fatalf("accept A: %v", err)
		}
		var statusB string
		if err := pool.QueryRow(ctx, `SELECT status FROM quotes WHERE id=$1`, quoteB).
			Scan(&statusB); err != nil {
			t.Fatalf("read B status: %v", err)
		}
		if statusB != "SENT" {
			t.Errorf("accepting quote A moved quote B to %q, a token must reach exactly one quote", statusB)
		}
	})

	t.Run("an expired QUOTE cannot be accepted even on a live link", func(t *testing.T) {
		// The link clock and the quote clock are independent (R4 vs D2). A live link on a lapsed quote
		// must still refuse, and name the date.
		_, quoteID, _, _ := seedQuote(t, pool, "SENT", "2026-06-01")
		cleanupShares(t, pool, quoteID)
		svc := shareSvc(t, pool, now)
		minted, err := mintAndSend(svc, qCtxAs(intStaff(intTrenton)), quoteID, 0)
		if err == nil {
			// Minting on a lapsed quote is itself refused; if that ever changes, the accept must still
			// refuse, so assert that too.
			_, aerr := svc.AcceptByToken(ctx, minted.Token, "Dana", true, true)
			var qe *QuoteExpiredError
			if !errors.As(aerr, &qe) {
				t.Fatalf("a lapsed quote must refuse acceptance, got %v", aerr)
			}
			return
		}
		var qe *QuoteExpiredError
		if !errors.As(err, &qe) {
			t.Fatalf("minting a link for a lapsed quote must refuse naming the date, got %v", err)
		}
		if !strings.Contains(qe.Error(), "2026-06-01") {
			t.Errorf("refusal must name the expiry date, got %q", qe.Error())
		}
	})
}

// - acceptance is a deliberate act -

func TestIntegration_Accept_RequiresNameAndAffirmative(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
	cleanupShares(t, pool, quoteID)
	svc := shareSvc(t, pool, now)
	ctx := context.Background()
	minted, err := mintAndSend(svc, qCtxAs(intStaff(intTrenton)), quoteID, 0)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	t.Run("no typed name is refused", func(t *testing.T) {
		if _, err := svc.AcceptByToken(ctx, minted.Token, "   ", true, true); err == nil {
			t.Fatal("an acceptance with no name must be refused")
		}
	})
	t.Run("no affirmative is refused", func(t *testing.T) {
		// The affirmative is a REQUIRED separate act. An attestation someone could produce by accident
		// is not worth recording.
		if _, err := svc.AcceptByToken(ctx, minted.Token, "Dana Reyes", false, true); err == nil {
			t.Fatal("an acceptance without the explicit affirmative must be refused")
		}
	})
	t.Run("neither refusal wrote anything", func(t *testing.T) {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM quote_acceptances WHERE quote_id=$1`, quoteID).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 0 {
			t.Errorf("a refused acceptance must persist nothing; got %d rows", n)
		}
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM quotes WHERE id=$1`, quoteID).
			Scan(&status); err != nil {
			t.Fatalf("status: %v", err)
		}
		if status != "SENT" {
			t.Errorf("a refused acceptance must not move the quote; got %q", status)
		}
	})
}

// TestIntegration_Accept_IsAtomicWithItsTrail proves the acceptance shares its transaction with the
// audit row (R3), the same property the edit path has.
func TestIntegration_Accept_IsAtomicWithItsTrail(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
	cleanupShares(t, pool, quoteID)

	repo := NewRepository(pool).withNow(func() time.Time { return now })
	aud := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewStaffService(repo).
		WithAudit(failAfterRealRecorder{real: aud}).
		WithShares(repo).
		WithPricing(qPricer{}).WithCostBasis(qInv{}).WithCustomerType(qCustType{}).
		withCaller(qCallerFrom).withNow(func() time.Time { return now })

	// Mint with a recorder that works, by using a clean service for the mint only.
	minter := shareSvc(t, pool, now)
	minted, err := mintAndSend(minter, qCtxAs(intStaff(intTrenton)), quoteID, 0)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	ctx := context.Background()
	if _, err := svc.AcceptByToken(ctx, minted.Token, "Dana Reyes", true, true); err == nil {
		t.Fatal("want the acceptance to fail when the recorder fails")
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM quotes WHERE id=$1`, quoteID).Scan(&status); err != nil {
		t.Fatalf("status: %v", err)
	}
	if status != "SENT" {
		t.Errorf("a failed trail must roll the acceptance back; status is %q", status)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM quote_acceptances WHERE quote_id=$1`, quoteID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("a rolled-back acceptance must leave no record; got %d", n)
	}
	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE entity_id=$1 AND action=$2`,
		quoteID, audit.ActQuoteAccepted).Scan(&rows); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if rows != 0 {
		t.Errorf("a rolled-back acceptance must leave no QUOTE_ACCEPTED row; got %d", rows)
	}
}

// - the acceptance guards (DIB-393) -
//
// These exist because both guards inside AcceptQuoteGuarded were CORRECT but unproven: no-opping either
// one left the whole package green. A guard on a public, unauthenticated, money-adjacent write path
// that no test defends is one refactor away from being a live defect, and the refactor will look safe.
//
// They drive the REPOSITORY path (AcceptByToken -> AcceptQuoteGuarded), deliberately not the
// display-side Acceptable flag on PublicQuoteByToken. Those are different code and only one of them is
// the enforcement, a test against the flag would pass with the real guard deleted.

// acceptanceRows counts persisted acceptances for a quote. Every refusal below asserts this is zero:
// the refusal must be atomic, not merely a returned error with a row left behind.
func acceptanceRows(t *testing.T, pool *pgxpool.Pool, quoteID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM quote_acceptances WHERE quote_id = $1`, quoteID).Scan(&n); err != nil {
		t.Fatalf("count acceptances: %v", err)
	}
	return n
}

// mintedFor seeds a DRAFT quote, mints a live link (which flips it to SENT), and hands back the raw
// token. Tests then move the quote into the state under test, so the link stays live and the guard
// being exercised is the QUOTE guard rather than link liveness.
func mintedFor(t *testing.T, pool *pgxpool.Pool, now time.Time, validUntil any) (svc *StaffService, quoteID, token string) {
	t.Helper()
	_, quoteID, _, _ = seedQuote(t, pool, "DRAFT", validUntil)
	cleanupShares(t, pool, quoteID)
	svc = shareSvc(t, pool, now)
	minted, err := mintAndSend(svc, qCtxAs(intStaff(intTrenton)), quoteID, 0)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	return svc, quoteID, minted.Token
}

func TestIntegration_Accept_RefusedWhenNotSent(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	// Every status the schema's CHECK allows EXCEPT SENT. The brief named three; covering all five is
	// the same amount of code and leaves no non-SENT state unproven.
	for _, status := range []string{"DRAFT", "ACCEPTED", "CONVERTED", "EXPIRED", "LOST"} {
		t.Run(status, func(t *testing.T) {
			svc, quoteID, token := mintedFor(t, pool, now, nil)
			if _, err := pool.Exec(context.Background(),
				`UPDATE quotes SET status=$2 WHERE id=$1`, quoteID, status); err != nil {
				t.Fatalf("force status %s: %v", status, err)
			}

			_, err := svc.AcceptByToken(context.Background(), token, "Dana Reyes", true, true)
			if !errors.Is(err, ErrNotAcceptable) {
				t.Fatalf("accepting a %s quote must return ErrNotAcceptable; got %v", status, err)
			}
			// The refusal must NAME the state, a customer support call starts from this string.
			if !strings.Contains(err.Error(), status) {
				t.Errorf("the refusal must name the status; %q does not contain %q", err.Error(), status)
			}
			if n := acceptanceRows(t, pool, quoteID); n != 0 {
				t.Errorf("a refused acceptance must write no quote_acceptances row; got %d", n)
			}
		})
	}
}

func TestIntegration_Accept_RefusedWhenExpired(t *testing.T) {
	pool := intPool(t)
	// valid_until is inclusive through end of day (quoteExpired), so relative to this `now`,
	// 2026-07-29 is lapsed and 2026-08-15 is live.
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	t.Run("past is refused", func(t *testing.T) {
		// Seeded NULL and moved to the past AFTER minting: minting itself asserts non-expiry, so a
		// quote seeded already-lapsed could never reach the guard under test.
		svc, quoteID, token := mintedFor(t, pool, now, nil)
		if _, err := pool.Exec(context.Background(),
			`UPDATE quotes SET valid_until='2026-07-29' WHERE id=$1`, quoteID); err != nil {
			t.Fatalf("lapse quote: %v", err)
		}
		var expired *QuoteExpiredError
		_, err := svc.AcceptByToken(context.Background(), token, "Dana Reyes", true, true)
		if !errors.As(err, &expired) {
			t.Fatalf("accepting a lapsed quote must return QuoteExpiredError; got %v", err)
		}
		if n := acceptanceRows(t, pool, quoteID); n != 0 {
			t.Errorf("a refused acceptance must write no quote_acceptances row; got %d", n)
		}
	})

	// The two success legs prove the guard DISCRIMINATES rather than simply refusing everything, a
	// guard that always refused would pass the case above on its own.
	t.Run("future succeeds", func(t *testing.T) {
		svc, quoteID, token := mintedFor(t, pool, now, "2026-08-15")
		if _, err := svc.AcceptByToken(context.Background(), token, "Dana Reyes", true, true); err != nil {
			t.Fatalf("a quote inside its window must be acceptable; got %v", err)
		}
		if n := acceptanceRows(t, pool, quoteID); n != 1 {
			t.Errorf("a successful acceptance must write exactly one row; got %d", n)
		}
	})

	t.Run("null never expires", func(t *testing.T) {
		// Pins D2a: NULL valid_until means the quote never lapses. This is our own ruling, so it gets a
		// test rather than living only in a comment.
		svc, quoteID, token := mintedFor(t, pool, now, nil)
		if _, err := svc.AcceptByToken(context.Background(), token, "Dana Reyes", true, true); err != nil {
			t.Fatalf("a quote with NULL valid_until must never expire; got %v", err)
		}
		if n := acceptanceRows(t, pool, quoteID); n != 1 {
			t.Errorf("a successful acceptance must write exactly one row; got %d", n)
		}
	})
}

// - the empty-quote mint guard (DIB-395) -

func TestIntegration_Mint_RefusedWhenQuoteHasNoLines(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	ctx := qCtxAs(intStaff(intTrenton))

	t.Run("empty quote is refused and stays DRAFT", func(t *testing.T) {
		_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
		cleanupShares(t, pool, quoteID)
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM quote_lines WHERE quote_id = $1`, quoteID); err != nil {
			t.Fatalf("empty the quote: %v", err)
		}
		svc := shareSvc(t, pool, now)

		_, err := mintAndSend(svc, ctx, quoteID, 0)
		if !errors.Is(err, ErrEmptyQuote) {
			t.Fatalf("minting an empty quote must return ErrEmptyQuote; got %v", err)
		}

		// The refusal has to leave nothing behind: the status flip and the link creation are the two
		// side effects that would otherwise strand an empty quote in SENT with a live customer link.
		var status string
		if err := pool.QueryRow(context.Background(),
			`SELECT status FROM quotes WHERE id=$1`, quoteID).Scan(&status); err != nil {
			t.Fatalf("read status: %v", err)
		}
		if status != "DRAFT" {
			t.Errorf("a refused mint must leave the quote DRAFT; got %q", status)
		}
		var links int
		if err := pool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM quote_share_links WHERE quote_id=$1`, quoteID).Scan(&links); err != nil {
			t.Fatalf("count links: %v", err)
		}
		if links != 0 {
			t.Errorf("a refused mint must create no share link; got %d", links)
		}
	})

	t.Run("a quote with lines still mints", func(t *testing.T) {
		_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
		cleanupShares(t, pool, quoteID)
		svc := shareSvc(t, pool, now)
		if _, err := mintAndSend(svc, ctx, quoteID, 0); err != nil {
			t.Fatalf("a one-line quote must still mint; got %v", err)
		}
	})

	t.Run("zero total with lines is legitimate", func(t *testing.T) {
		// The ruling: guard on CONTENT, never on money. A fully-discounted or warranty-replacement
		// quote totals $0 and must still be sendable. Guarding on total would refuse a real business
		// case, so this test would fail if anyone "improved" the guard into a money check.
		_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
		cleanupShares(t, pool, quoteID)
		if _, err := pool.Exec(context.Background(),
			`UPDATE quote_lines SET unit_price_cents=0, line_total_cents=0 WHERE quote_id=$1`,
			quoteID); err != nil {
			t.Fatalf("zero the lines: %v", err)
		}
		if _, err := pool.Exec(context.Background(),
			`UPDATE quotes SET subtotal_cents=0, total_cents=0 WHERE id=$1`, quoteID); err != nil {
			t.Fatalf("zero the quote: %v", err)
		}
		svc := shareSvc(t, pool, now)
		if _, err := mintAndSend(svc, ctx, quoteID, 0); err != nil {
			t.Fatalf("a $0 quote WITH lines must still mint; got %v", err)
		}
	})
}

// - the blank-description fallback (DIB-397) -

func TestIntegration_PublicQuote_BlankDescriptionFallsBackToProductName(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	ctx := qCtxAs(intStaff(intTrenton))

	t.Run("blank line descriptions fall back to the product name", func(t *testing.T) {
		// DIB-397: the customer was shown unnamed rows and asked to type their name to accept them.
		// The fallback lives in the READ, so this passes for rows that are ALREADY blank, no backfill.
		_, quoteID, prod, _ := seedQuote(t, pool, "DRAFT", nil)
		cleanupShares(t, pool, quoteID)
		if _, err := pool.Exec(context.Background(),
			`UPDATE quote_lines SET description='' WHERE quote_id=$1`, quoteID); err != nil {
			t.Fatalf("blank the description: %v", err)
		}
		var wantName string
		if err := pool.QueryRow(context.Background(),
			`SELECT name FROM products WHERE id=$1`, prod).Scan(&wantName); err != nil {
			t.Fatalf("read product name: %v", err)
		}
		svc := shareSvc(t, pool, now)
		minted, err := mintAndSend(svc, ctx, quoteID, 0)
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		view, err := svc.PublicQuoteByToken(context.Background(), minted.Token)
		if err != nil {
			t.Fatalf("public view: %v", err)
		}
		if len(view.Lines) != 1 {
			t.Fatalf("expected one line; got %d", len(view.Lines))
		}
		if view.Lines[0].Description != wantName {
			t.Errorf("a blank description must fall back to the product name %q; got %q",
				wantName, view.Lines[0].Description)
		}
	})
}

// - stored tax reaches the customer page (DIB-412) -

// TestIntegration_PublicQuote_CarriesStoredTax pins the DATA half of DIB-412.
//
// The customer page labels its totals from tax_cents: a quote carrying tax renders Subtotal / HST /
// Total, and only an untaxed quote may say "before HST" (app/src/_quote-public.ts, unit-tested in
// _quote-public.test.mjs). That branch is only as good as the field it reads, and the public
// projection is a hand-built ALLOWLIST, SELECT list and struct both, so tax_cents survives there
// by choice, not by inheritance. Drop it from either and the page silently reverts to calling every
// quote pre-tax, which is exactly the $624 mislabel testers hit on Q-2026-0001.
//
// The acceptance echo is asserted on the same fixture because it is a second copy of the same money,
// written by a different query, and it is what the customer keeps.
func TestIntegration_PublicQuote_CarriesStoredTax(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	ctx := qCtxAs(intStaff(intTrenton))

	// The seeded shape from the field report: 480000 + 62400 = 542400.
	const wantSubtotal, wantTax, wantTotal = int64(480000), int64(62400), int64(542400)

	t.Run("the public payload carries tax_cents through", func(t *testing.T) {
		_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
		cleanupShares(t, pool, quoteID)
		if _, err := pool.Exec(context.Background(),
			`UPDATE quotes SET subtotal_cents=$2, tax_cents=$3, total_cents=$4 WHERE id=$1`,
			quoteID, wantSubtotal, wantTax, wantTotal); err != nil {
			t.Fatalf("apply stored tax: %v", err)
		}
		svc := shareSvc(t, pool, now)
		minted, err := mintAndSend(svc, ctx, quoteID, 0)
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		view, err := svc.PublicQuoteByToken(context.Background(), minted.Token)
		if err != nil {
			t.Fatalf("public view: %v", err)
		}
		if view.TaxCents != wantTax {
			t.Errorf("the public payload must carry the stored tax: tax_cents = %d, want %d",
				view.TaxCents, wantTax)
		}
		if view.SubtotalCents != wantSubtotal || view.TotalCents != wantTotal {
			t.Errorf("public money = %d/%d/%d, want %d/%d/%d",
				view.SubtotalCents, view.TaxCents, view.TotalCents, wantSubtotal, wantTax, wantTotal)
		}
		// The page draws Subtotal + HST = Total. If these three ever stop reconciling, the rows add
		// up wrong in front of the customer, whatever the labels say.
		if view.SubtotalCents+view.TaxCents != view.TotalCents {
			t.Errorf("the three public figures must reconcile: %d + %d != %d",
				view.SubtotalCents, view.TaxCents, view.TotalCents)
		}
	})

	t.Run("the acceptance confirmation echoes the same tax", func(t *testing.T) {
		_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
		cleanupShares(t, pool, quoteID)
		if _, err := pool.Exec(context.Background(),
			`UPDATE quotes SET subtotal_cents=$2, tax_cents=$3, total_cents=$4 WHERE id=$1`,
			quoteID, wantSubtotal, wantTax, wantTotal); err != nil {
			t.Fatalf("apply stored tax: %v", err)
		}
		svc := shareSvc(t, pool, now)
		minted, err := mintAndSend(svc, ctx, quoteID, 0)
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		res, err := svc.AcceptByToken(context.Background(), minted.Token, "Dana Reyes", true, true)
		if err != nil {
			t.Fatalf("accept: %v", err)
		}
		if res.TaxCents != wantTax || res.SubtotalCents != wantSubtotal || res.TotalCents != wantTotal {
			t.Errorf("the confirmation echo = %d/%d/%d, want %d/%d/%d",
				res.SubtotalCents, res.TaxCents, res.TotalCents, wantSubtotal, wantTax, wantTotal)
		}
	})

	t.Run("an untaxed quote still reports zero tax", func(t *testing.T) {
		// The other half of the branch: the before-HST framing must keep a fixture that earns it.
		_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
		cleanupShares(t, pool, quoteID)
		svc := shareSvc(t, pool, now)
		minted, err := mintAndSend(svc, ctx, quoteID, 0)
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		view, err := svc.PublicQuoteByToken(context.Background(), minted.Token)
		if err != nil {
			t.Fatalf("public view: %v", err)
		}
		if view.TaxCents != 0 {
			t.Errorf("an untaxed quote must report tax_cents 0; got %d", view.TaxCents)
		}
	})
}

// TestIntegration_PublicQuote_OrderNoOnlyAfterConversion is the fence of the quote accent's one new
// field: a SENT or ACCEPTED quote's view carries no order number, and the same quote read after it
// converts carries the number of the order it became (the earliest, as the repeat-accept refusal
// names it), on the token read and on the staff preview alike, because both are one code path.
func TestIntegration_PublicQuote_OrderNoOnlyAfterConversion(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	svc, quoteID, token := mintedFor(t, pool, now, nil)
	cust := ""
	if err := pool.QueryRow(context.Background(), `SELECT customer_id::text FROM quotes WHERE id=$1`, quoteID).Scan(&cust); err != nil {
		t.Fatalf("read customer: %v", err)
	}
	cleanupConvertedOrders(t, pool, quoteID)
	setCreditLimit(t, pool, cust, 5_000_00)

	for _, status := range []string{"SENT", "ACCEPTED"} {
		if _, err := pool.Exec(context.Background(), `UPDATE quotes SET status=$2 WHERE id=$1`, quoteID, status); err != nil {
			t.Fatalf("force %s: %v", status, err)
		}
		view, err := svc.PublicQuoteByToken(context.Background(), token)
		if err != nil {
			t.Fatalf("view a %s quote: %v", status, err)
		}
		if view.OrderNo != "" {
			t.Errorf("a %s quote must carry no order number; got %q", status, view.OrderNo)
		}
	}

	res, err := gatedRepo(pool).Convert(context.Background(), cust, quoteID, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	view, err := svc.PublicQuoteByToken(context.Background(), token)
	if err != nil {
		t.Fatalf("view the converted quote: %v", err)
	}
	if view.Status != "CONVERTED" || view.OrderNo == "" || view.OrderNo != res.OrderNo {
		t.Errorf("a converted quote's view = status %q order_no %q, want CONVERTED and %q", view.Status, view.OrderNo, res.OrderNo)
	}
	preview, err := svc.CustomerViewPreview(qCtxAs(intStaff(intTrenton)), quoteID)
	if err != nil {
		t.Fatalf("preview the converted quote: %v", err)
	}
	if preview.OrderNo != res.OrderNo {
		t.Errorf("the staff preview carries order_no %q, want %q: the two reads are one code path", preview.OrderNo, res.OrderNo)
	}
	// Forcing the status away again hides the number: it rides the status, not the order row alone.
	if _, err := pool.Exec(context.Background(), `UPDATE quotes SET status='LOST' WHERE id=$1`, quoteID); err != nil {
		t.Fatalf("force LOST: %v", err)
	}
	lost, err := svc.PublicQuoteByToken(context.Background(), token)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if lost.OrderNo != "" {
		t.Errorf("only a CONVERTED quote carries an order number; got %q on %s", lost.OrderNo, lost.Status)
	}
}

// TestIntegration_PublicQuote_ConvertedWithoutProvenanceServesNoOrderNo pins the swallowed no-row
// branch of the order number read (review P3-3): a CONVERTED quote whose provenance row is gone
// still reads 200 by token, with no order number, rather than failing the whole public view.
func TestIntegration_PublicQuote_ConvertedWithoutProvenanceServesNoOrderNo(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	svc, quoteID, token := mintedFor(t, pool, now, nil)
	cust := ""
	if err := pool.QueryRow(context.Background(), `SELECT customer_id::text FROM quotes WHERE id=$1`, quoteID).Scan(&cust); err != nil {
		t.Fatalf("read customer: %v", err)
	}
	cleanupConvertedOrders(t, pool, quoteID)
	setCreditLimit(t, pool, cust, 5_000_00)
	if _, err := pool.Exec(context.Background(), `UPDATE quotes SET status='ACCEPTED' WHERE id=$1`, quoteID); err != nil {
		t.Fatalf("force ACCEPTED: %v", err)
	}

	if _, err := gatedRepo(pool).Convert(context.Background(), cust, quoteID, ConvertOptions{}); err != nil {
		t.Fatalf("convert: %v", err)
	}
	before, err := svc.PublicQuoteByToken(context.Background(), token)
	if err != nil {
		t.Fatalf("view the converted quote: %v", err)
	}
	if before.Status != "CONVERTED" || before.OrderNo == "" {
		t.Fatalf("the converted quote must first read with its order number; got status %q order_no %q", before.Status, before.OrderNo)
	}

	// The provenance row gone is not the customer's problem: the view still serves, minus the number.
	if _, err := pool.Exec(context.Background(), `DELETE FROM quote_accept_provenance WHERE quote_id=$1`, quoteID); err != nil {
		t.Fatalf("delete provenance: %v", err)
	}
	after, err := svc.PublicQuoteByToken(context.Background(), token)
	if err != nil {
		t.Fatalf("a converted quote with no provenance row must still read 200; got %v", err)
	}
	if after.Status != "CONVERTED" || after.OrderNo != "" {
		t.Errorf("the view with no provenance row = status %q order_no %q, want CONVERTED and empty", after.Status, after.OrderNo)
	}
}
