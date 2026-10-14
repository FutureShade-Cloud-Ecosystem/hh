//go:build demo

// demoseed_gableaddr_demo.go, the demo build's side of the Gable-address seam: the migration-planted
// demo email addresses are moved off their scrub placeholders and onto invented Gable Landscape
// Supply addresses. The default build's side is demoseed_gableaddr_default.go, a no-op stub, so a
// default binary carries no rename path at all (demoseed dispatches in every build, so the pair, not
// the entrypoint, is what holds the gate); demoseed_gableaddr_build_test.go reads both builds' binaries
// and proves it.
//
// WHY THIS IS IN THE SEED AND NOT IN A MIGRATION. The addresses are migration-planted on every
// migrated environment, production included. A migration that rewrote them would fix a problem only
// the demo box has and would change production data. The demoseed is the one writer whose audience is
// exactly the demo world.
//
// WHAT THE MIGRATIONS PLANT: system_settings org_email (0017), warranty_vendor_profiles.channel_ref
// for the EMAIL-channel Permacon and Best Way profiles (0057), and the six homework/UAT staff users
// (0061, 0066, 0077). Each statement below updates ONE column (and updated_at) on ONE row addressed by
// its FIXED lookup key or exact old email, so nothing outside the fixed list moves, a second run
// matches no row, and a hand-edited row is left alone.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// gableAddrRename is one migration-planted email address and the Gable address it carries in the
// demo world.
type gableAddrRename struct {
	table, column, key, oldEmail, newEmail string
	// byEmail says the row is addressed by the old email rather than by a fixed key, because the
	// migration mints the id from a sequence and the exact uuid is not stable in the source.
	byEmail bool
	// byVendor says the fixed key is a vendor_id, used for warranty_vendor_profiles where the
	// profile row's own id is generated, but the vendor it belongs to is fixed.
	byVendor bool
}

// gableAddrRenames is every migration-planted email a demo screen or integration could read.
// The invented domain is gablelandscape.example; vendor aliases match the GLS tenant's renamed
// suppliers. No persona names are used, only role labels.
var gableAddrRenames = []gableAddrRename{
	{"system_settings", "value", "org_email", "scrubbed-0001@example.invalid", "office@gablelandscape.example", false, false},
	{"warranty_vendor_profiles", "channel_ref", "f5700000-0000-4000-9000-000000000003", "scrubbed-0002@example.invalid", "orders@copperlinepavers.example", false, true},
	{"warranty_vendor_profiles", "channel_ref", "f5700000-0000-4000-9000-000000000005", "scrubbed-0003@example.invalid", "orders@heronfieldstone.example", false, true},
	{"users", "email", "40000061-0000-4000-9000-000000000001", "scrubbed-0004@example.invalid", "general.manager@gablelandscape.example", false, false},
	{"users", "email", "40000061-0000-4000-9000-000000000002", "scrubbed-0005@example.invalid", "inside.sales@gablelandscape.example", false, false},
	{"users", "email", "40000061-0000-4000-9000-000000000003", "scrubbed-0008@example.invalid", "counter.sales@gablelandscape.example", false, false},
	{"users", "email", "40000061-0000-4000-9000-000000000004", "scrubbed-0007@example.invalid", "branch.manager@gablelandscape.example", false, false},
	{"users", "email", "", "scrubbed-0009@example.invalid", "uat.inside.sales@gablelandscape.example", true, false},
}

// gableDisplayRename is one migration-planted user display_name and the invented Gable role label
// it carries in the demo world. The guard matches the exact value the migrations write (after 0363
// restores the ordinary words beside the personal-name tokens), so a second run matches no row.
type gableDisplayRename struct {
	id, oldDisplay, newDisplay string
}

// gableDisplayRenames moves the migration-planted homework/UAT staff display names off their
// personal-name tokens and onto role labels. The persona addresses on hh-demo.invalid are not touched.
var gableDisplayRenames = []gableDisplayRename{
	{"40000061-0000-4000-9000-000000000001", "speaker-0007 (homework — Pricing Admin)", "General Manager (demo)"},
	{"40000061-0000-4000-9000-000000000002", "Scrubname0030 (homework — Till Day)", "Inside Sales (demo)"},
	{"40000061-0000-4000-9000-000000000003", "Scrubname0025 Scrubname0024 (homework — Sale to Statements)", "Counter Sales (demo)"},
	{"40000061-0000-4000-9000-000000000004", "Scrubname0016 (homework — Inside Sales)", "Branch Manager (demo)"},
	{"", "UAT Tier-3 (Inside Sales, Trenton)", "UAT Inside Sales (demo)"},
}

// gableAddressesPlan prints the plan's one-line account of the address rename pass: how many
// migration-planted email addresses and display names -apply renames onto the demo dealer's
// addresses and role labels. The default build's side of the pair prints nothing, because that build
// carries no rename.
func gableAddressesPlan(out *os.File) {
	fmt.Fprintf(out, "  gable addresses %d migration-planted email addresses and %d display names renamed onto Gable Landscape Supply addresses with -apply\n", len(gableAddrRenames), len(gableDisplayRenames))
}

// renameGableAddresses renames the migration-planted demo email addresses inside the apply
// transaction, and returns how many rows it renamed. On the second run against the same world every
// address already reads its Gable one, so every statement matches no row and the answer is 0.
func renameGableAddresses(ctx context.Context, tx txExec, now time.Time) (int, error) {
	renamed := 0
	for _, r := range gableAddrRenames {
		var tag pgconn.CommandTag
		var err error
		switch {
		case r.table == "system_settings":
			tag, err = tx.Exec(ctx,
				`UPDATE system_settings SET value = to_jsonb($1::text), updated_at = $2 WHERE key = $3 AND value::text = $4`,
				r.newEmail, now, r.key, `"`+r.oldEmail+`"`)
		case r.table == "warranty_vendor_profiles":
			tag, err = tx.Exec(ctx,
				`UPDATE warranty_vendor_profiles SET channel_ref = $1, updated_at = $2 WHERE vendor_id = $3::uuid AND channel = 'EMAIL' AND channel_ref = $4`,
				r.newEmail, now, r.key, r.oldEmail)
		case r.table == "users" && r.byEmail:
			tag, err = tx.Exec(ctx,
				`UPDATE users SET email = $1, updated_at = $2 WHERE email = $3`,
				r.newEmail, now, r.oldEmail)
		case r.table == "users":
			tag, err = tx.Exec(ctx,
				`UPDATE users SET email = $1, updated_at = $2 WHERE id = $3::uuid AND email = $4`,
				r.newEmail, now, r.key, r.oldEmail)
		default:
			return renamed, fmt.Errorf("demoseed: unknown gable address table %s", r.table)
		}
		if err != nil {
			return renamed, fmt.Errorf("demoseed: renaming %s.%s off %s: %w", r.table, r.column, r.oldEmail, err)
		}
		renamed += int(tag.RowsAffected())
	}
	return renamed, nil
}

// renameGableDisplayNames renames the migration-planted demo users' display_name values inside
// the apply transaction, and returns how many rows it renamed. On the second run against the same
// world every display name already reads its Gable role label, so every statement matches no row
// and the answer is 0.
func renameGableDisplayNames(ctx context.Context, tx txExec, now time.Time) (int, error) {
	renamed := 0
	for _, r := range gableDisplayRenames {
		var tag pgconn.CommandTag
		var err error
		if r.id != "" {
			tag, err = tx.Exec(ctx,
				`UPDATE users SET display_name = $1, updated_at = $2 WHERE id = $3::uuid AND display_name = $4`,
				r.newDisplay, now, r.id, r.oldDisplay)
		} else {
			tag, err = tx.Exec(ctx,
				`UPDATE users SET display_name = $1, updated_at = $2 WHERE email = 'scrubbed-0009@example.invalid' AND display_name = $3`,
				r.newDisplay, now, r.oldDisplay)
		}
		if err != nil {
			return renamed, fmt.Errorf("demoseed: renaming display_name off %s: %w", r.oldDisplay, err)
		}
		renamed += int(tag.RowsAffected())
	}
	return renamed, nil
}
