//go:build demo

// demoseed_scrubname_demo.go, the demo build's side of the scrub-name seam: the demo
// world's one non-INSERT write, migration-planted demo customers losing their scrub
// placeholder names, here and only here. The default build's side is
// demoseed_scrubname_default.go, a no-op stub, so a default binary carries no rename
// path at all (demoseed dispatches in every build, so the pair, not the entrypoint, is
// what holds the gate); demoseed_scrubname_build_test.go reads both builds' binaries
// and proves it.
//
// WHY THIS IS IN THE SEED AND NOT IN A MIGRATION (the principal's ruling, decision log row
// 925). The scrub pass renamed live customer data before it reached this repository, and the
// migrations that plant demo islands inherited the placeholder style, so every migrated
// environment, production included, carries customers named "Scrubbed Party NNNN". A migration
// that renamed them would rewrite production's rows to fix a problem only the demo box has:
// production is meant to keep its scrubbed names. The demoseed is the one writer whose
// audience is exactly the demo world (the demo image's entrypoint is the only thing that runs
// it), so the fix lands here.
//
// WHAT THE MIGRATIONS ACTUALLY PLANT, every scrub-named customer a tester can meet: 0029 the
// house WALK-IN (a19…0001), 0055 the portal demo island (df…0001, the record the principal's
// look traced onto the Quote Desk's list), 0057 the three warranty-demo accounts (c57…0001,
// c57…0003, c57…0007). The probe test beside this file reads every customer's name after the
// apply and holds the whole family to the rule, so a later migration that plants another one
// cannot slip through unnamed.
//
// WHY AN UPDATE IS SAFE HERE WHEN apply.go REFUSES TO HAVE ONE. apply.go stays INSERT-only by
// construction, and this file is deliberately separate so that guarantee is untouched: to
// destroy a row somebody would still have to add a statement kind to apply.go. Each statement
// below updates ONE display column (and updated_at) on ONE row addressed by its FIXED id, from
// the exact placeholder literal the migration writes to a demo name in the same "(demo)" style
// plan.go's own customers carry. Nothing here can delete, nothing can touch a row outside the
// list, and nothing fires twice: the WHERE names the placeholder, so the second run matches no
// row, and a row somebody already renamed by hand is left alone, the same deference apply.go
// shows every row it did not create.
package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

// scrubRename is one migration-planted demo customer and the name it carries in the demo world.
type scrubRename struct {
	id, account, fromName, toName string
}

// scrubRenames, every migration-planted customer whose placeholder name a tester would meet on
// a demo screen (the Quote Desk's list and the counter's customer search render customers.name).
// Each to-name fits the row's own demo story and follows the seeded customers' style: the
// island backs the "Riverside Walkway (Demo)" project, the warranty accounts back the claim
// backlog at Trenton, Kingston and Brighton, and the walk-in is the counter's anonymous COD
// customer.
var scrubRenames = []scrubRename{
	{"a1900000-0000-4000-9000-000000000001", "WALK-IN", "Scrubbed Party 0009", "Counter Walk-in (demo)"},
	{"df000000-0000-4000-9000-000000000001", "CUST-DEMO-CP03", "Scrubbed Party 0004", "Riverside Walkway Contracting (demo)"},
	{"c5700000-0000-4000-9000-000000000001", "WTY-QUINTE", "Scrubbed Party 0007", "Quinte Exteriors (demo)"},
	{"c5700000-0000-4000-9000-000000000003", "WTY-LIMESTONE", "Scrubbed Party 0006", "Limestone City Landscaping (demo)"},
	{"c5700000-0000-4000-9000-000000000007", "WTY-HOMEOWNER", "Scrubbed Party 0005", "S. Marchand (demo homeowner)"},
}

// scrubTextRename is one migration-planted persona-facing text value (a role title, the title of
// a seeded conversation, the body of a seeded message) carrying a Scrubname token, addressed by
// the table, the fixed id of the row, and the exact literal the migration writes.
type scrubTextRename struct {
	table, column, id, fromText, toText string
	// stamped says the table has an updated_at to move; messages do not, and their edited_at
	// would paint an edited mark on a message nobody edited.
	stamped bool
}

// scrubTextRenames, every such value a demo screen shows. The role titles are what the feedback
// note's sender line prints beside the persona's display name ("Sales (demo) (Inside Sales)"),
// so they are job titles, not people. The conversation is the seeded DM between the two demo
// accounts the populate names "Platform admin (demo)" and "Demo teammate (demo)", and its one
// message addresses the second. Table and column come from this fixed list only, never from
// input; the ids are the rows' own and the WHERE names the literal, so a second run matches
// nothing and a hand-edited row is left alone.
var scrubTextRenames = []scrubTextRename{
	{"roles", "name", "e7000000-0000-4000-9000-000000000001", "Scrubname0012 Scrubname0019", "General Manager", true},
	{"roles", "name", "e7000000-0000-4000-9000-000000000002", "Scrubname0010 Scrubname0007", "Financial Controller", true},
	{"roles", "name", "e7000000-0000-4000-9000-000000000004", "Branch Scrubname0019", "Branch Manager", true},
	{"roles", "name", "e7000000-0000-4000-9000-000000000005", "Procurement Scrubname0019", "Procurement Manager", true},
	{"roles", "name", "e7000000-0000-4000-9000-000000000006", "Scrubname0027 Scrubname0019", "Sales Manager", true},
	{"roles", "name", "e7000000-0000-4000-9000-000000000007", "Yard Scrubname0019", "Yard Manager", true},
	{"roles", "name", "e7000000-0000-4000-9000-000000000008", "Logistics Scrubname0019", "Logistics Manager", true},
	{"roles", "name", "e7000000-0000-4000-9000-000000000010", "Scrubname0014 Scrubname0027", "Inside Sales", true},
	{"roles", "name", "e7000000-0000-4000-9000-000000000011", "Outside Scrubname0027", "Outside Sales", true},
	// 0055's portal login (df…0a1) carries the first dealer's name in its email and a scrub placeholder
	// for a name; a staff portal-users screen lists both. Migration 0055 is never edited (forward
	// only), so the demo world renames the row here, by its fixed id and the exact literals.
	{"portal_users", "name", "df000000-0000-4000-9000-0000000000a1", "Scrubbed Party 0003", "Portal admin (demo)", true},
	{"portal_users", "email", "df000000-0000-4000-9000-0000000000a1", "portal.demo@dibbits.example", "portal.demo@demo.example", true},
	{"msg_channels", "name", "a6000000-0000-4000-9000-0000000000d1", "Scrubname0005, Scrubname0008 Reyes", "Platform admin (demo), Demo teammate (demo)", true},
	{"msg_messages", "body", "b6000000-0000-4000-9000-0000000000d1",
		"Hey Scrubname0008 \u2014 sending you the Techo-Bloc restock ETA once the yard confirms.",
		"Hey Demo teammate, sending you the Techo-Bloc restock ETA once the yard confirms.", false},
}

// scrubNamesPlan prints the plan's one-line account of the rename pass: how many
// migration-planted demo customers -apply renames off their scrub placeholders. The
// default build's side of the pair prints nothing, because that build carries no rename.
func scrubNamesPlan(out *os.File) {
	fmt.Fprintf(out, "  scrub names    %d migration-planted demo customers and %d role titles, conversation titles, messages and portal logins renamed off their placeholders with -apply\n", len(scrubRenames), len(scrubTextRenames))
}

// renameScrubbedDemoCustomers renames the migration-planted demo customers inside the apply
// transaction, and returns how many rows it renamed. On the second run against the same world
// every name already reads its demo one, so every statement matches no row and the answer is 0.
func renameScrubbedDemoCustomers(ctx context.Context, tx txExec, now time.Time) (int, error) {
	renamed := 0
	for _, r := range scrubRenames {
		tag, err := tx.Exec(ctx,
			`UPDATE customers SET name = $1, updated_at = $2 WHERE id = $3::uuid AND name = $4`,
			r.toName, now, r.id, r.fromName)
		if err != nil {
			return renamed, fmt.Errorf("demoseed: renaming %s (%s) off its scrub placeholder: %w", r.account, r.id, err)
		}
		renamed += int(tag.RowsAffected())
	}
	for _, r := range scrubTextRenames {
		set, args := r.column+` = $1`, []any{r.toText, r.id, r.fromText}
		if r.stamped {
			set, args = set+`, updated_at = $4`, append(args, now)
		}
		tag, err := tx.Exec(ctx,
			`UPDATE `+r.table+` SET `+set+` WHERE id = $2::uuid AND `+r.column+` = $3`, args...)
		if err != nil {
			return renamed, fmt.Errorf("demoseed: renaming %s.%s (%s) off its scrub placeholder: %w", r.table, r.column, r.id, err)
		}
		renamed += int(tag.RowsAffected())
	}
	return renamed, nil
}
