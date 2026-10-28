//go:build integration

// approvals_collision_probe_integration_test.go: UX-108 round 2, the
// approvals create path's collision probe. A create with a client-chosen id
// that is taken answers the collision as the caller's own fact INSIDE the
// caller's org (sync.ErrRowExists, the pick door's rule, PR 648's collision
// arm) and as not-found outside it, so existence never leaks across the
// fence.
//
// The probe read the row back through Load, and Load is reader fenced (round
// 1): a card the caller may not read read as absent, so a colliding id
// answered "sync: row not found" where it answered "sync: row already
// exists". The quotes scope, changed in the same PR, kept its collision
// answer through an org-held read (quote/syncscope.go's load, the collision
// probe's read over a querier), and the probe's own comment still described
// that rule.
//
// The leg: an org caller who may not read the card, creating with its id,
// gets the collision, and no row is written.
package sync_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// TestUX108R2ApprovalsCollisionProbeAnswersRowExistsInsideTheOrg: the
// collision probe reads the row org-held, as its own comment describes, so a
// card the caller may not read still answers the collision inside the
// caller's own org.
func TestUX108R2ApprovalsCollisionProbeAnswersRowExistsInsideTheOrg(t *testing.T) {
	f := newGapFixture(t)
	ctx := context.Background()
	store := ux108ApprovalsStore(t, f)
	gm := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubGeneralMgr}
	clerk := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales}

	// The dealer wide manager files a card at branch B. The clerk reaches
	// branch A alone and filed nothing, so the pull shows him no card.
	rs, err := store.Create(ctx, gm, uuid.NewString(), map[string]any{
		"branch_id":   testkit.BranchNorthBID,
		"policy_type": "CREDIT_LIMIT_EXCEEDED",
		"detail":      "ux108r2: a card the clerk may not read"})
	if err != nil {
		t.Fatalf("the manager files the credit exception: %v", err)
	}
	if _, found, err := store.Load(ctx, clerk, rs.ID); err != nil || found {
		t.Fatalf("the clerk may not read the card: found %v err %v", found, err)
	}

	// The clerk creates with the card's id, at his own branch: the id is
	// taken inside his own org, so the collision is his own fact.
	_, err = store.Create(ctx, clerk, uuid.NewString(), map[string]any{
		"id":          rs.ID,
		"branch_id":   testkit.BranchNorthAID,
		"policy_type": "CREDIT_LIMIT_EXCEEDED",
		"detail":      "ux108r2: a colliding filing"})
	if !errors.Is(err, sync.ErrRowExists) {
		t.Errorf("a taken id inside the caller's own org answers ErrRowExists: %v", err)
	}

	// The same answer through the real engine, the way a device sees it.
	engine, info := ux108r2Engine(t, f, store)
	key := uuid.NewString()
	t.Cleanup(func() {
		_, _ = f.pool.Exec(ctx, `DELETE FROM sync_op_hashes WHERE client_id = $1::uuid`, key)
	})
	answer := ux108r2Push(t, engine, info, clerk, apigen.SyncPushOperation{
		ClientId: key, Op: "create", Payload: map[string]any{
			"id":          rs.ID,
			"branch_id":   testkit.BranchNorthAID,
			"policy_type": "CREDIT_LIMIT_EXCEEDED",
			"detail":      "ux108r2: a colliding filing"}})
	if answer.Status != "rejected" || answer.Error == nil || *answer.Error != sync.ErrRowExists.Error() {
		t.Errorf("the colliding filing answers the caller's own fact: %+v", answer)
	}

	// Nothing was written: the card the manager filed is the only row under
	// that id, and no second card was filed at the clerk's branch.
	var n int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM permission_approval_requests WHERE id = $1::uuid`, rs.ID).Scan(&n); err != nil || n != 1 {
		t.Errorf("the colliding create wrote no row: %d cards under the id, %v", n, err)
	}
	var filed int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM permission_approval_requests WHERE requested_by = $1::uuid`,
		ux108r2UserID(t, f, testkit.SubInsideSales)).Scan(&filed); err != nil {
		t.Fatalf("count the clerk's filings: %v", err)
	}
	if filed != 0 {
		t.Errorf("the colliding create filed no card of the clerk's own: %d cards", filed)
	}
}

// ux108r2UserID is the ERP user row behind a platform subject.
func ux108r2UserID(t *testing.T, f *theGapFixture, sub string) string {
	t.Helper()
	id, ok := linkedUser(f.pool)(context.Background(), sub)
	if !ok {
		t.Fatalf("no linked user for %s", sub)
	}
	return id
}
