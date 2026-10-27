//go:build integration

// syncshortage_fence_integration_test.go, UX-105: the shortages sync scope's reads (Pull, FindApplied and
// Load) hold the caller's branch reach, the fence the pick scope's pull has held since UX-81 (567) and the
// weights scope since UX-81c (594). A shortage's scope IS its task's: the branch is read through the parent
// task exactly as the write paths' branch fences read it. A device at one branch pulls none of another
// branch's shortages; a GLOBAL caller of the org keeps the org predicate alone; an out of reach shortage's
// Load and FindApplied answer as absent; and through the real engine and key ledger, a branch Kingston
// replay of a key bound on a branch Trenton shortage answers with nothing of the row, the owning branch's
// replay still answered.
//
// Run with DATABASE_URL set to a throwaway migrated database; the legs skip politely without one.
package tasks

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// shortageReachDoor wires a shortages store whose staff authority answers as given, the caller the reads
// are fenced by (has=false is the absent authority).
func shortageReachDoor(d *shortDoor, has bool, sc identity.StaffContext) *ShortageSyncStore {
	return NewShortageSyncStore(d.pool, nil).WithStaffAuthority(
		func(context.Context, string, string) (identity.StaffContext, bool, error) { return sc, has, nil })
}

// shortageReachDoorWithOrg wires the same door with the branch to org seam the pick and weights doors carry
// (UX-81 round 5): a GLOBAL caller's reach is every branch of its OWN org, never every branch in the
// database.
func shortageReachDoorWithOrg(d *shortDoor, sc identity.StaffContext, org BranchOrgReader) *ShortageSyncStore {
	return shortageReachDoor(d, true, sc).WithBranchOrgReader(org)
}

// shortageReachTask births one PICK task in the org on the given branch, through the pick sync create
// unwired (the branch fence is the writes' own; setup needs no authority).
func shortageReachTask(t *testing.T, d *shortDoor, branch string) string {
	t.Helper()
	s := NewSyncStore(d.pool, pickDoorOrders{branch: branch}, nil)
	row, err := s.Create(context.Background(), d.f, uuid.NewString(), map[string]any{"order_id": uuid.NewString()})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return row.ID
}

// shortageOnTask reports one shortage on the task through the sync door under the key given, and answers
// the shortage's id.
func shortageOnTask(t *testing.T, d *shortDoor, taskID, key string) string {
	t.Helper()
	sid := uuid.NewString()
	res := d.push(apigen.SyncPushOperation{ClientId: key, Op: "create", Payload: appPayload(sid, taskID)})
	if res[0].Status != "applied" {
		t.Fatalf("create shortage: %+v", res[0])
	}
	return sid
}

// shortageWithKey creates one shortage on the task under the key given, through the store itself (no
// engine, no ledger): task_shortages carries no unique index on idempotency_key (0213), so one key can
// name rows on two branches, the state the find-applied fence has to survive.
func shortageWithKey(t *testing.T, d *shortDoor, taskID, key string) string {
	t.Helper()
	sid := uuid.NewString()
	if _, err := d.store.Create(context.Background(), d.f, key, appPayload(sid, taskID)); err != nil {
		t.Fatalf("create shortage under key %s: %v", key, err)
	}
	return sid
}

// shortageReachIDs folds one pull page's live rows and tombstones into the ids the caller saw.
func shortageReachIDs(items []hhsync.Entity, tombs []hhsync.Tombstone) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.ID)
	}
	for _, tb := range tombs {
		out = append(out, tb.ID)
	}
	return out
}

// TestShortageSyncPullAndLoadHoldTheCallersBranchReach: a shortage's branch is its task's, so a device at
// Kingston pulls and loads none of Trenton's shortages, a deleted one's tombstone included, and one at
// Trenton none of Kingston's; a GLOBAL caller of the org still pulls both branches; an absent or inactive
// authority pulls nothing; and the owning branch's reads behave as before. The unwired posture is the pick
// scope's own: an unwired seam fences nothing, the reads run under the org predicate alone, exactly the
// posture the composition root refuses to boot a door in (StaffAuthorityWired is the fail fast's leg).
func TestShortageSyncPullAndLoadHoldTheCallersBranchReach(t *testing.T) {
	d := newShortDoor(t)
	trenton, kingston := uuid.NewString(), uuid.NewString()
	taskTrenton, taskKingston := shortageReachTask(t, d, trenton), shortageReachTask(t, d, kingston)
	shortTrenton := shortageOnTask(t, d, taskTrenton, uuid.NewString())
	shortKingston := shortageOnTask(t, d, taskKingston, uuid.NewString())
	goneTrenton := shortageOnTask(t, d, taskTrenton, uuid.NewString())
	if _, err := d.store.Delete(context.Background(), d.f, goneTrenton, uuid.NewString()); err != nil {
		t.Fatalf("delete: %v", err)
	}
	global := scopedTo(uuid.NewString())
	global.Scope, global.Tier = identity.ScopeGlobal, identity.TierLeadership
	atTrenton := shortageReachDoor(d, true, scopedTo(uuid.NewString(), trenton))
	atKingston := shortageReachDoor(d, true, scopedTo(uuid.NewString(), kingston))
	pull := func(s *ShortageSyncStore) []string {
		items, tombs, err := s.Pull(context.Background(), d.f, 0, 100)
		if err != nil {
			t.Fatalf("pull: %v", err)
		}
		return shortageReachIDs(items, tombs)
	}

	got := pull(atKingston)
	if contains(got, shortTrenton) || contains(got, goneTrenton) {
		t.Errorf("a Kingston device pulled a Trenton shortage or its tombstone: %v", got)
	}
	if !contains(got, shortKingston) {
		t.Errorf("a Kingston device pulls its own branch's shortage: %v", got)
	}
	got = pull(atTrenton)
	if contains(got, shortKingston) {
		t.Errorf("a Trenton device pulled Kingston's shortage: %v", got)
	}
	if !contains(got, shortTrenton) || !contains(got, goneTrenton) {
		t.Errorf("a Trenton device pulls its own branch's shortage and its deleted one's tombstone: %v", got)
	}
	got = pull(shortageReachDoor(d, true, global))
	if !contains(got, shortTrenton) || !contains(got, shortKingston) || !contains(got, goneTrenton) {
		t.Errorf("a GLOBAL caller of the org still pulls every branch: %v", got)
	}
	if got := pull(shortageReachDoor(d, false, identity.StaffContext{})); len(got) != 0 {
		t.Errorf("an absent authority pulls nothing: %v", got)
	}
	inactive := scopedTo(uuid.NewString(), trenton)
	inactive.Status = "SUSPENDED"
	if got := pull(shortageReachDoor(d, true, inactive)); len(got) != 0 {
		t.Errorf("an inactive authority pulls nothing: %v", got)
	}
	if got := pull(d.store); !contains(got, shortTrenton) || !contains(got, shortKingston) || !contains(got, goneTrenton) {
		t.Errorf("an unwired seam fences nothing, the org predicate alone (the pick scope's own posture): %v", got)
	}

	if _, found, err := atKingston.Load(context.Background(), d.f, shortTrenton); err != nil || found {
		t.Errorf("loading Trenton's shortage from Kingston answers a missing row: found %v err %v", found, err)
	}
	if _, found, err := atKingston.Load(context.Background(), d.f, goneTrenton); err != nil || found {
		t.Errorf("a deleted Trenton shortage is Kingston's absent too: found %v err %v", found, err)
	}
	if _, found, err := atKingston.Load(context.Background(), d.f, shortKingston); err != nil || !found {
		t.Errorf("loading its own branch's shortage works: found %v err %v", found, err)
	}
	row, found, err := atTrenton.Load(context.Background(), d.f, goneTrenton)
	if err != nil || !found || !row.Deleted {
		t.Errorf("the owning branch still reads its own deleted shortage: found %v deleted %v err %v", found, row.Deleted, err)
	}
	if _, found, err := shortageReachDoor(d, true, global).Load(context.Background(), d.f, shortTrenton); err != nil || !found {
		t.Errorf("a GLOBAL caller still loads either branch's shortage: found %v err %v", found, err)
	}
}

// TestShortageSyncFindAppliedHoldsTheCallersBranchReach: a replay never answers for a shortage the caller
// cannot pull (the pick scope's fence, 567 round 2, PR 640's path). A key bound on a Trenton shortage
// answers not applied for a Kingston device, exactly the answer an absent key gets; the owning branch and a
// GLOBAL caller of the org still resolve their keys.
func TestShortageSyncFindAppliedHoldsTheCallersBranchReach(t *testing.T) {
	d := newShortDoor(t)
	trenton, kingston := uuid.NewString(), uuid.NewString()
	taskTrenton := shortageReachTask(t, d, trenton)
	key := uuid.NewString()
	shortageOnTask(t, d, taskTrenton, key)
	global := scopedTo(uuid.NewString())
	global.Scope, global.Tier = identity.ScopeGlobal, identity.TierLeadership
	atTrenton := shortageReachDoor(d, true, scopedTo(uuid.NewString(), trenton))
	atKingston := shortageReachDoor(d, true, scopedTo(uuid.NewString(), kingston))

	row, found, err := atKingston.FindApplied(context.Background(), d.f, key)
	if err != nil || found || row.ID != "" || row.Seq != 0 {
		t.Errorf("a Kingston device replaying Trenton's key gets not applied: found %v id %q seq %d err %v", found, row.ID, row.Seq, err)
	}
	absentRow, absentFound, aerr := atKingston.FindApplied(context.Background(), d.f, uuid.NewString())
	if aerr != nil || absentFound {
		t.Fatalf("the absent key answers not applied: found %v err %v", absentFound, aerr)
	}
	row, found, err = atKingston.FindApplied(context.Background(), d.f, key)
	if found != absentFound || err != aerr || row.ID != absentRow.ID || row.Seq != absentRow.Seq {
		t.Errorf("an out of reach key answers exactly what an absent key answers: key found %v id %q seq %d, absent found %v id %q seq %d",
			found, row.ID, row.Seq, absentFound, absentRow.ID, absentRow.Seq)
	}
	if _, found, err := atTrenton.FindApplied(context.Background(), d.f, key); err != nil || !found {
		t.Errorf("the owning branch's device still resolves its key: found %v err %v", found, err)
	}
	if _, found, err := shortageReachDoor(d, true, global).FindApplied(context.Background(), d.f, key); err != nil || !found {
		t.Errorf("a GLOBAL caller of the org still resolves the key: found %v err %v", found, err)
	}
}

// TestShortageSyncHoldsTheCallersOrgBoundForAGlobalCaller: a GLOBAL caller's reach is every branch of its
// OWN org, never every branch in the database (UX-81 round 5), the bound the pick and weights doors carry
// through the branchOrg seam. A shortage whose parent task sits in the caller's own org but on a branch of
// another org is out of the GLOBAL caller's reach: the key answers not applied and the row answers absent,
// the same answers an out of reach row gets. A door without the seam runs the org predicate alone, the
// unwired posture the composition root refuses to boot a door in (BranchOrgWired is the fail fast's leg).
func TestShortageSyncHoldsTheCallersOrgBoundForAGlobalCaller(t *testing.T) {
	d := newShortDoor(t)
	own, foreign := uuid.NewString(), uuid.NewString()
	taskOwn := shortageReachTask(t, d, own)
	taskForeign := shortageReachTask(t, d, foreign)
	keyOwn, keyForeign := uuid.NewString(), uuid.NewString()
	shortOwn := shortageWithKey(t, d, taskOwn, keyOwn)
	shortForeign := shortageWithKey(t, d, taskForeign, keyForeign)
	global := scopedTo(uuid.NewString())
	global.Scope, global.Tier = identity.ScopeGlobal, identity.TierLeadership
	// The seam: the caller's own branch is a branch of its own org, the other one belongs to another org.
	wired := shortageReachDoorWithOrg(d, global, branchesOfOrg{own: {d.org: true}, foreign: {d.other: true}})

	if _, found, err := wired.FindApplied(context.Background(), d.f, keyForeign); err != nil || found {
		t.Errorf("a GLOBAL caller does not resolve a key on a branch outside its own org: found %v err %v", found, err)
	}
	if _, found, err := wired.Load(context.Background(), d.f, shortForeign); err != nil || found {
		t.Errorf("a GLOBAL caller does not load a shortage on a branch outside its own org: found %v err %v", found, err)
	}
	if _, found, err := wired.FindApplied(context.Background(), d.f, keyOwn); err != nil || !found {
		t.Errorf("a GLOBAL caller still resolves the key on its own org's branch: found %v err %v", found, err)
	}
	if _, found, err := wired.Load(context.Background(), d.f, shortOwn); err != nil || !found {
		t.Errorf("a GLOBAL caller still loads the shortage on its own org's branch: found %v err %v", found, err)
	}
	// The unwired posture: without the org bound seam the org predicate alone runs, exactly main's behavior.
	unwired := shortageReachDoor(d, true, global)
	if _, found, err := unwired.FindApplied(context.Background(), d.f, keyForeign); err != nil || !found {
		t.Errorf("a door without the org bound seam keeps the org predicate alone, main's posture: found %v err %v", found, err)
	}
}

// TestShortageSyncFindAppliedResolvesAKeyNamingTwoBranchesToTheCallersOwnRow: one key can name shortages
// on two branches (task_shortages carries no unique index on idempotency_key, 0213), so the find-applied
// query's unordered LIMIT 1 can land on the other branch's row. The reach clause is in the query (the
// pick scope's rule, PR 640's shape), so the answer is always the caller's own row: the Trenton device
// resolves the key to its own shortage and the Kingston device to one of its own, never to the other's.
func TestShortageSyncFindAppliedResolvesAKeyNamingTwoBranchesToTheCallersOwnRow(t *testing.T) {
	d := newShortDoor(t)
	trenton, kingston := uuid.NewString(), uuid.NewString()
	taskTrenton := shortageReachTask(t, d, trenton)
	taskKingston := shortageReachTask(t, d, kingston)
	key := uuid.NewString()
	// The out of reach rows first: on the tree without the clause the query's unordered LIMIT 1 takes the
	// row that comes first in the table, which is a row the Trenton device cannot reach. That is what makes
	// this leg red there.
	away := shortageWithKey(t, d, taskKingston, key)
	awayToo := shortageWithKey(t, d, taskKingston, key)
	shortTrenton := shortageWithKey(t, d, taskTrenton, key)
	atTrenton := shortageReachDoor(d, true, scopedTo(uuid.NewString(), trenton))
	atKingston := shortageReachDoor(d, true, scopedTo(uuid.NewString(), kingston))

	row, found, err := atTrenton.FindApplied(context.Background(), d.f, key)
	if err != nil || !found || row.ID != shortTrenton {
		t.Errorf("the Trenton device resolves the key to its own shortage: found %v id %q seq %d err %v (own %q, the other branch's %q)",
			found, row.ID, row.Seq, err, shortTrenton, away)
	}
	row, found, err = atKingston.FindApplied(context.Background(), d.f, key)
	if err != nil || !found || row.ID == shortTrenton || (row.ID != away && row.ID != awayToo) {
		t.Errorf("the Kingston device resolves the key to one of its own shortages: found %v id %q seq %d err %v (own %q, the other branch's %q)",
			found, row.ID, row.Seq, err, away, shortTrenton)
	}
}

// TestShortageSyncCreateCollisionAnswersRowExistsForAnotherBranchsShortage: the client-chosen id is taken
// by a shortage on a branch the caller cannot reach. The collision is inside the caller's own org, so it
// is the caller's own fact and answers ErrRowExists, the pick door's rule (its create's collision probe
// reads the row under the org predicate alone, syncscope.go's collision arm). A fence that held the probe
// to the branch reach answered ErrRowNotFound, which is a write-path answer the fence did not owe.
func TestShortageSyncCreateCollisionAnswersRowExistsForAnotherBranchsShortage(t *testing.T) {
	d := newShortDoor(t)
	trenton, kingston := uuid.NewString(), uuid.NewString()
	taskTrenton := shortageReachTask(t, d, trenton)
	taskKingston := shortageReachTask(t, d, kingston)
	taken := shortageWithKey(t, d, taskTrenton, uuid.NewString())
	atKingston := shortageReachDoor(d, true, scopedTo(uuid.NewString(), kingston))
	_, err := atKingston.Create(context.Background(), d.f, uuid.NewString(), appPayload(taken, taskKingston))
	if !errors.Is(err, hhsync.ErrRowExists) {
		t.Errorf("a taken id answers ErrRowExists, the collision is inside the caller's own org: %v", err)
	}
}

// TestShortageSyncEngineReplayOfAnotherBranchsKeyCarriesNothingOfTheRow: the real engine over the real key
// ledger. A Trenton device applies a report under key K; a Kingston device replaying the very same keyed
// operation gets nothing of the row: not applied at the dedup (the fence above), so the ledger-bound replay
// answers applied without the row's seq, the answer a row the caller cannot read gets. Trenton's own replay
// still answers applied with the row's seq, and nothing ran twice.
func TestShortageSyncEngineReplayOfAnotherBranchsKeyCarriesNothingOfTheRow(t *testing.T) {
	d := newShortDoor(t)
	trenton, kingston := uuid.NewString(), uuid.NewString()
	taskTrenton := shortageReachTask(t, d, trenton)
	atTrenton := scopedTo(uuid.NewString(), trenton)
	atKingston := scopedTo(uuid.NewString(), kingston)
	caller := atTrenton
	store := NewShortageSyncStore(d.pool, nil).WithStaffAuthority(
		func(context.Context, string, string) (identity.StaffContext, bool, error) { return caller, true, nil })
	engine := hhsync.NewEngine().WithKeyLedger(hhsync.NewPgKeyLedger(d.pool))
	if err := engine.RegisterScope("shortages", store); err != nil {
		t.Fatalf("register: %v", err)
	}
	info, ok := hhsync.Lookup("shortages")
	if !ok {
		t.Fatal("shortages missing from the registry")
	}
	key := uuid.NewString()
	op := apigen.SyncPushOperation{ClientId: key, Op: "create", Payload: appPayload(uuid.NewString(), taskTrenton)}

	res, err := engine.Push(context.Background(), info, d.f, []apigen.SyncPushOperation{op})
	if err != nil || res.Results[0].Status != "applied" || res.Results[0].Seq == nil {
		t.Fatalf("Trenton applies its report: %+v %v", res.Results[0], err)
	}
	first := res.Results[0]

	caller = atKingston
	res, err = engine.Push(context.Background(), info, d.f, []apigen.SyncPushOperation{op})
	if err != nil {
		t.Fatalf("Kingston's replay failed the push: %v", err)
	}
	replay := res.Results[0]
	// The answer today's tree gives: applied with nothing of the row, the fence's half of the brief's leg.
	// PR 640's engine gate (a key whose row the caller cannot read answers as an absent key) lands first
	// and turns this answer into the absent key's rejected, so this assertion is switched then, by the
	// lead or a later coder (UX-105 round 2, the review's second finding). PR 640's branch is NOT merged
	// here: this tree carries the fence, and the fence is what makes 640's gate bite here.
	if replay.Status != "applied" || replay.Seq != nil {
		t.Errorf("a Kingston replay of Trenton's key answers with nothing of the row: status %q seq %v error %v",
			replay.Status, replay.Seq, replay.Error)
	}

	caller = atTrenton
	res, err = engine.Push(context.Background(), info, d.f, []apigen.SyncPushOperation{op})
	if err != nil || res.Results[0].Status != "applied" || res.Results[0].Seq == nil || *res.Results[0].Seq != *first.Seq {
		t.Errorf("the owning branch's replay still answers applied with the row's seq: %+v %v", res.Results[0], err)
	}
	var n int
	if err := d.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM task_shortages s JOIN tasks t ON t.id = s.task_id WHERE t.org_id = $1::uuid`, d.org).Scan(&n); err != nil || n != 1 {
		t.Errorf("the replay ran nothing twice: %d shortage rows, %v", n, err)
	}
}
