//go:build integration

package billing

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// ROUND 10, M3-P3-7: the key table's twin serialisation, pinned by a committed leg.
//
// LOCK-ORDER.md's row for this door claims the reservation insert is the twin serialisation the
// round 7 advisory slot used to provide, so two requests under one org's key cannot both find the
// key unspent. Until this leg the claim rested on the reader's belief about ON CONFLICT DO
// NOTHING. Two CONCURRENT applies under ONE key against two DIFFERENT invoices take different
// invoice row locks, so nothing else stands between them: only the key's reservation can
// serialise them. Exactly one applies; the other answers the spent key 409 once the winner's row
// commits, whichever twin wins the race.
//
// The negative control, run by hand with the reservation lifted locally and NEVER committed (the
// key step returning a fresh unspent row without its insert), bites: one key moved money two
// times and conflicted zero times, the two anchors queueing one after the other with the group
// still holding open credit for each. Its red line is quoted in the round 10 report.

func TestIntegration_FW1_12b_TwoConcurrentAppliesUnderOneKeyApplyOnce(t *testing.T) {
	pool := intPool(t)
	applySvc := NewCreditApplyService(NewRepository(pool))
	ctx := context.Background()
	n := ns(t)
	applyOrg := plantApplyOrg(t, pool, n)

	custID, invA, invB, creditID := spentKeyFixture(t, n)

	key := n + "-key-1"
	type answer struct {
		inv string
		res CreditApplication
		err error
	}
	answers := make(chan answer, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		res, err := applySvc.Apply(ctx, ApplyCreditInput{
			CustomerID: custID, CreditKind: CreditKindAccount, CreditID: creditID, InvoiceID: invA,
			OrgID:       applyOrg,
			AmountCents: 5000, Reason: "the twin on invoice a", IdempotencyKey: key,
		}, true)
		answers <- answer{inv: invA, res: res, err: err}
	}()
	go func() {
		defer wg.Done()
		res, err := applySvc.Apply(ctx, ApplyCreditInput{
			CustomerID: custID, CreditKind: CreditKindAccount, CreditID: creditID, InvoiceID: invB,
			OrgID:       applyOrg,
			AmountCents: 5000, Reason: "the twin on invoice b", IdempotencyKey: key,
		}, true)
		answers <- answer{inv: invB, res: res, err: err}
	}()
	wg.Wait()
	close(answers)

	var applied, conflicted int
	var appliedInv string
	for a := range answers {
		if a.err == nil {
			applied++
			appliedInv = a.inv
			if a.res.Replayed {
				t.Fatalf("the winner on %s answered Replayed=true, want a fresh application: %+v", a.inv, a.res)
			}
			continue
		}
		var conflict CreditApplicationConflictError
		if !errors.As(a.err, &conflict) {
			t.Fatalf("the twin on %s answered %v, want the spent key conflict or a clean win", a.inv, a.err)
		}
		conflicted++
	}
	if applied != 1 || conflicted != 1 {
		t.Fatalf("one key moved money %d times and conflicted %d times, want 1 and 1", applied, conflicted)
	}

	// The winner's invoice carries the one movement and the loser's stands at its seeded balance:
	// 15000 between them, whichever twin won.
	var balA, balB int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invA).Scan(&balA); err != nil {
		t.Fatalf("read invoice a: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invB).Scan(&balB); err != nil {
		t.Fatalf("read invoice b: %v", err)
	}
	if balA+balB != 15000 {
		t.Fatalf("the two invoices carry %d between them, want 15000: one application of 5000 moved", balA+balB)
	}
	if appliedInv == invA && balA != 5000 {
		t.Fatalf("the winner %s stands at %d, want 5000", appliedInv, balA)
	}
	if appliedInv == invB && balB != 5000 {
		t.Fatalf("the winner %s stands at %d, want 5000", appliedInv, balB)
	}

	// The table carries exactly one row under the key, the winner's.
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM credit_application_keys WHERE idempotency_key = $1`, key).Scan(&rows); err != nil {
		t.Fatalf("count the key's rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("the key carries %d rows, want 1", rows)
	}
}
