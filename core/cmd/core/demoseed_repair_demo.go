//go:build demo

// demoseed_repair_demo.go, the demo build's side of the seed-repair seam: the guarded repairs
// (a home branch, and empty product applications) this command carries, for databases the OLD plan already seeded.
//
// WHY A REPAIR AT ALL. The applier is INSERT-only, by construction, so a change to a row the plan
// already wrote (C01's home branch, moved to Trenton by plan.go's ruling) lands only on databases
// that had never been seeded: on every box seeded before the ruling, the customers row conflicts
// and the apply writes nothing, and the visual bug the ruling fixes (the customer with quotes at
// Trenton unfindable in the branch-fenced search there) survives on exactly the box a demo is shown
// from. The scrub-name pass (demoseed_scrubname_demo.go) exists for the same class of row, with the
// same answer, and this file follows its guarantees one for one:
//
//   - ONE row addressed by its FIXED id, the plan's own demoID("customer:C01"), never a filter
//     over names or accounts;
//   - the WHERE names the exact value being repaired (the first branch by code order, the home the
//     old plan wrote), so the second run matches no row, and a row somebody re-homed by hand is
//     left alone, the same deference apply.go shows every row it did not create;
//   - ONE column moves (home_branch_id, and updated_at), nothing can delete, and the statement
//     list is fixed in this file, never built from input;
//   - a single-branch deployment (or a box whose first branch is already the target) repairs
//     nothing: from and to are the same branch and the pass is skipped whole.
//
// The default build's side is demoseed_repair_default.go, a no-op stub, so a default binary
// carries no home-branch write at all; demoseed_scrubname_build_test.go's repair leg proves the
// pair holds that gate by grepping both builds' binaries.
package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

// productApplicationRepairSQL fills one demo product's application where it is NULL or empty. The
// product is addressed by its FIXED id, the plan's own demoID("product:"+key), and the value comes
// from demoProducts, the same table the fresh seed writes from, so there is one source and no copy.
// A set value (even one a hand chose) never matches, and a repaired row never matches again.
const productApplicationRepairSQL = `UPDATE products SET application = $1, updated_at = $2 WHERE id = $3::uuid AND (application IS NULL OR application = '')`

// repairDemoWorld re-homes the one account the ruling moved, inside the apply transaction, and
// returns how many rows it repaired. On the second run against the same world the row already
// reads its new home, the statement matches nothing, and the answer is 0.
func repairDemoWorld(ctx context.Context, tx txExec, now time.Time, branches []branch) (int, error) {
	fixed, err := repairDemoProductApplications(ctx, tx, now)
	if err != nil {
		return 0, err
	}
	stamped, err := repairDriverDayOrg(ctx, tx, now)
	if err != nil {
		return 0, err
	}
	tasks, err := repairDemoTaskSync(ctx, tx)
	if err != nil {
		return 0, err
	}
	n, err := repairNorthlineHome(ctx, tx, now, branches)
	return fixed + stamped + tasks + n, err
}

// taskSyncRepairSQL gives the demo tasks of one type their org stamp and their place in the type's
// sync feed. The pick_tasks and weights pulls both read `org_id = caller's org AND seq IS NOT NULL
// AND seq > cursor` (core/internal/tasks/syncscope.go, syncweights.go), so a task the seed wrote
// with neither is invisible to every device: the Yard Pick board runs on that feed and showed the
// yard team an empty queue over a full table. Each unstamped task draws a number of the type's own
// counter, in creation order, in this one statement. The counter's rule is the live allocator's:
// next_value holds the last number issued. So the block is taken the way the allocator takes one,
// by ONE relative, locked write: the bump adds the block's size to next_value (a concurrent
// allocator's uncommitted draw makes the upsert wait, and the add lands on top of it), and the
// block is read back from what that write returned, the last number issued minus the block's size.
// There is no snapshot base to go stale. The guards hold it to its case: the id is a demo task's
// fixed id, the seq is still NULL (a second run matches nothing, and with nothing to do the bump
// is never written, so the counter does not move), and the org row exists (the first apply of a
// fresh box runs before the populate provisions it, the second apply stamps).
const taskSyncRepairSQL = `
WITH todo AS (
	SELECT id, row_number() OVER (ORDER BY created_at, id) AS n
	  FROM tasks
	 WHERE id = ANY($1::uuid[]) AND task_type = $2 AND seq IS NULL
	   AND EXISTS (SELECT 1 FROM org WHERE slug = $4::citext)
), bump AS (
	INSERT INTO scope_sequences (scope, next_value)
	SELECT $3, count(*) FROM todo HAVING count(*) > 0
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + EXCLUDED.next_value
	RETURNING next_value
)
UPDATE tasks t
   SET seq = (SELECT next_value FROM bump) - (SELECT count(*) FROM todo) + todo.n,
       org_id = COALESCE(t.org_id, (SELECT id FROM org WHERE slug = $4::citext))
  FROM todo
 WHERE t.id = todo.id`

// repairDemoTaskSync stamps the demo tasks into their sync feeds: the PICK tasks into pick_tasks and
// the NEED_WEIGHT tasks into weights (the counters the core's own allocators draw from).
func repairDemoTaskSync(ctx context.Context, tx txExec) (int, error) {
	ids := map[string][]string{}
	for _, t := range demoTasks {
		ids[t.Type] = append(ids[t.Type], demoID("task:"+t.Key))
	}
	total := 0
	for _, f := range []struct{ taskType, scope string }{{"PICK", "pick_tasks"}, {"NEED_WEIGHT", "weights"}} {
		tag, err := tx.Exec(ctx, taskSyncRepairSQL, ids[f.taskType], f.taskType, f.scope, demoOrgSlug)
		if err != nil {
			return 0, fmt.Errorf("demoseed: stamping the demo %s tasks into the %s sync feed: %w", f.taskType, f.scope, err)
		}
		total += int(tag.RowsAffected())
	}
	return total, nil
}

// repairDemoProductApplications fills the application of every demo product an older seed left
// empty, so the till can place the product under its family's category tile. An older seed's rows
// were left alone by the INSERT-only applier (ON CONFLICT DO NOTHING), so a fresh plan's value
// never reached that box. On a database that is not the demo world no row carries a demo id and
// nothing matches.
func repairDemoProductApplications(ctx context.Context, tx txExec, now time.Time) (int, error) {
	total := 0
	for _, p := range demoProducts {
		tag, err := tx.Exec(ctx, productApplicationRepairSQL, p.Application, now, demoID("product:"+p.Key))
		if err != nil {
			return 0, fmt.Errorf("demoseed: filling the empty application of %s: %w", p.SKU, err)
		}
		total += int(tag.RowsAffected())
	}
	return total, nil
}

// repairNorthlineHome is the home-branch repair, split out so the pass above can run beside it.
func repairNorthlineHome(ctx context.Context, tx txExec, now time.Time, branches []branch) (int, error) {
	if len(branches) == 0 {
		return 0, nil
	}
	from := branches[0].ID // by code order, the home the plan wrote before the ruling
	to := from
	if len(branches) > 1 {
		to = branches[1].ID
	}
	if from == to {
		return 0, nil // a single-branch box homes every account at the branch it has
	}
	tag, err := tx.Exec(ctx,
		`UPDATE customers SET home_branch_id = $1::uuid, updated_at = $2 WHERE id = $3::uuid AND home_branch_id = $4::uuid`,
		to, now, demoID("customer:C01"), from)
	if err != nil {
		return 0, fmt.Errorf("demoseed: re-homing the Northline account to the branch the demo logins work: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// driverOrgRepairSQL stamps the demo org on one day-keyed Driver row the first apply wrote without
// it. The value is the org row the populate provisions (the same read driverOrgStamp makes at insert
// time), and the guards hold the repair to its case: the row must still be unstamped (a second run
// matches nothing, and a row somebody stamped by hand is left alone) and the org row must exist (on
// the first apply of a fresh box it does not yet, and a NULL stamped over a NULL would touch the row
// for no change and report a repair that did not happen).
const driverOrgRepairSQL = `UPDATE delivery_runs
	   SET org_id = (SELECT id FROM org WHERE slug = $1::citext), updated_at = $2
	 WHERE id = $3::uuid AND org_id IS NULL
	   AND EXISTS (SELECT 1 FROM org WHERE slug = $1::citext)`

const driverStopOrgRepairSQL = `UPDATE delivery_stops
	   SET org_id = (SELECT id FROM org WHERE slug = $1::citext), updated_at = $2
	 WHERE id = $3::uuid AND org_id IS NULL
	   AND EXISTS (SELECT 1 FROM org WHERE slug = $1::citext)`

// repairDriverDayOrg stamps the demo org on THIS day's Driver run and its stops, for the one real
// hole the insert-side stamp cannot cover: a fresh demo box's FIRST apply runs before the populate
// provisions the org row (the entrypoint's order: demoseed, demo-populate, demoseed), so the boot
// day's rows land with org NULL and the second apply's INSERTs recognise them and write nothing.
// The rows are addressed by the same day-keyed ids the plan just used (the house-zone day of the
// same clock), never by a filter, and the stop keys come from demoDriverDayStops, the same table
// the fresh seed writes from, so there is one source and no copy. Earlier days' rows are stamped by
// the boot that wrote them (their own apply runs with the org already in place through the insert
// side); a box upgraded from a seed that stamped nothing keeps its old days unstamped, and those
// rows carry no seq either (nothing walks a past day), so no scope ever answered them.
func repairDriverDayOrg(ctx context.Context, tx txExec, now time.Time) (int, error) {
	stamp := now.In(seedLocation()).Format("20060102")
	total := 0
	tag, err := tx.Exec(ctx, driverOrgRepairSQL, demoOrgSlug, now, demoID(dayKey(stamp, "driver-run")))
	if err != nil {
		return 0, fmt.Errorf("demoseed: stamping the demo org on the Driver's run: %w", err)
	}
	total += int(tag.RowsAffected())
	for _, id := range driverDayStopIDs(stamp) {
		tag, err := tx.Exec(ctx, driverStopOrgRepairSQL, demoOrgSlug, now, id)
		if err != nil {
			return 0, fmt.Errorf("demoseed: stamping the demo org on the Driver's stop: %w", err)
		}
		total += int(tag.RowsAffected())
	}
	return total, nil
}

// driverDayStopIDs is the day-keyed ids of the Driver's stops, from the plan's own table.
func driverDayStopIDs(stamp string) []string {
	ids := make([]string, 0, len(demoDriverDayStops))
	for _, s := range demoDriverDayStops {
		ids = append(ids, demoID(dayKey(stamp, "driver-stop:"+s.Key)))
	}
	return ids
}

// repairDemoWorldPlan prints the plan's one-line account of the repair pass, and only when the
// apply would actually repair something: the row the pass moves must still sit at the home it is
// moved from. A second apply, or a box the plan already homes correctly, prints nothing, because a
// plan line naming a repair that will not happen is a report that cries wolf. The default build's
// side prints nothing, because that build carries no repair.
func repairDemoWorldPlan(ctx context.Context, out *os.File, rows rowQuery, branches []branch, now time.Time) {
	var empty int
	if err := rows.QueryRow(ctx,
		`SELECT count(*) FROM products WHERE id = ANY($1::uuid[]) AND (application IS NULL OR application = '')`,
		demoProductIDs()).Scan(&empty); err == nil && empty > 0 {
		fmt.Fprintf(out, "  product repair %d demo product(s) have no application and get their family with -apply\n", empty)
	}
	// The org repair says its line only when it will stamp: the org row exists and this day's Driver
	// rows are still unstamped (the boot day's first apply ran before the populate did).
	stamp := now.In(seedLocation()).Format("20060102")
	var unstamped int
	if err := rows.QueryRow(ctx, `
		SELECT count(*) FROM delivery_runs
		 WHERE id = $1::uuid AND org_id IS NULL
		   AND EXISTS (SELECT 1 FROM org WHERE slug = $2::citext)`,
		demoID(dayKey(stamp, "driver-run")), demoOrgSlug).Scan(&unstamped); err == nil && unstamped > 0 {
		fmt.Fprintf(out, "  org repair     the Driver's day-keyed run and stops take the demo org with -apply\n")
	}
	if len(branches) > 1 && branches[0].ID != branches[1].ID {
		var waiting int
		if err := rows.QueryRow(ctx,
			`SELECT count(*) FROM customers WHERE id = $1::uuid AND home_branch_id = $2::uuid`,
			demoID("customer:C01"), branches[0].ID).Scan(&waiting); err != nil {
			// A read that cannot run says nothing rather than guessing: the plan's silence is the
			// honest answer, the apply's own report is the authoritative one.
			return
		}
		if waiting > 0 {
			fmt.Fprintf(out, "  home repair    the Northline account moves to %s with -apply, it is still homed at %s\n",
				branches[1].Code, branches[0].Code)
		}
	}
}

// demoProductIDs is every demo product's fixed id, from the plan's own table.
func demoProductIDs() []string {
	ids := make([]string, 0, len(demoProducts))
	for _, p := range demoProducts {
		ids = append(ids, demoID("product:"+p.Key))
	}
	return ids
}
