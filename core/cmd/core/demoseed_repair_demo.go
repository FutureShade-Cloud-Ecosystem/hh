//go:build demo

// demoseed_repair_demo.go, the demo build's side of the seed-repair seam: the one guarded
// home-branch repair this command carries, for databases the OLD plan already seeded.
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

// repairDemoWorld re-homes the one account the ruling moved, inside the apply transaction, and
// returns how many rows it repaired. On the second run against the same world the row already
// reads its new home, the statement matches nothing, and the answer is 0.
func repairDemoWorld(ctx context.Context, tx txExec, now time.Time, branches []branch) (int, error) {
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

// repairDemoWorldPlan prints the plan's one-line account of the repair pass: what -apply would
// repair on a database the old plan already seeded. The default build's side prints nothing,
// because that build carries no repair.
func repairDemoWorldPlan(out *os.File, branches []branch) {
	if len(branches) > 1 && branches[0].ID != branches[1].ID {
		fmt.Fprintf(out, "  home repair    the Northline account moves to %s with -apply if an earlier seed homed it at %s\n",
			branches[1].Code, branches[0].Code)
	}
}
