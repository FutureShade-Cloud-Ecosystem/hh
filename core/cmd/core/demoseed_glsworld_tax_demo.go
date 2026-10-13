//go:build demo

// demoseed_glsworld_tax_demo.go, the demo world seam's money and tax words: the demo dealer sells
// in San Diego County, so it charges the combined California sales tax, 7.75 percent, in US
// dollars. This is configuration and seed data only; no shared money path moves. The default
// build's side is nothing at all (the build gate's proof is demoseed_glsworld_build_test.go).
//
// TWO PARTS.
//
//  1. init() sets the rate the seed prices its invoices, orders and quotes at, before any plan is
//     built, so the seeded documents carry the tax the engine writes at the configured rate and
//     the books tie out. The shared arithmetic (taxOn) is untouched.
//  2. glsConvergeSalesTax moves the rows the MIGRATIONS plant onto the same rate and the same
//     words: the settings the quote header and the receipt read, the engine's standard and manual
//     review rows, the status card profiles (retired) and the ledger account names. It runs from
//     glsRenamePlanted, so it is inside the apply's transaction and only while the world is being
//     built, never on a built box. Each update names the value it replaces, by key, code or
//     reason, so a row a person changed is left alone and a second run matches nothing. Nothing is
//     deleted and no code, id or posting rule moves.
//
// The relief rows (the reduced and zero rates behind evidence) stay as the migrations plant them.
// They bind only when evidence is attached, and the counter's adapters pin evidence to false, so a
// relief reason rung at the counter binds the standard 775. They are inert here, not removed.
package main

import (
	"context"
	"fmt"
	"time"
)

// glsSalesTaxBps is the San Diego County combined rate, in basis points.
const glsSalesTaxBps = 775

// glsSalesTaxNote is the note the standard and manual review rows carry.
const glsSalesTaxNote = "San Diego County combined sales tax rate."

func init() { hstBps = glsSalesTaxBps }

// glsSetting is a settings key whose planted value moves: the update fires only on the planted one.
type glsSetting struct{ key, from, to string }

var glsSettings = []glsSetting{
	{"currency", `"CAD"`, `"USD"`},
	{"tax_region", `"ON"`, `"CA"`},
	{"hst_rate_bps", `1300`, `775`},
}

// glsAccount is a ledger account whose planted name moves; code and exact old name guard it.
type glsAccount struct{ code, from, to string }

var glsAccounts = []glsAccount{
	{"2200", "HST Payable", "Sales tax payable"},
	{"2201", "HST Payable — Federal 5% Portion", "Sales tax payable, spare one"},
	{"2202", "HST Payable — Ontario 8% Portion", "Sales tax payable, spare two"},
	{"1165", "FN Point-of-Sale Relief Recoverable (ON 8%)", "Sales tax relief recoverable"},
}

// glsPlantedOrder is a demo order the migrations plant with 13 percent tax typed into the header.
// No payment, journal or invoice references them, so the header alone moves; the order number,
// the subtotal and the planted tax guard the update.
type glsPlantedOrder struct {
	orderNo       string
	subtotalCents int64
	plantedTax    int64
}

var glsPlantedOrders = []glsPlantedOrder{
	{"SO-DEMO-HIST-0001", 48000, 6240},
	{"SO-DPA3-CRIT", 34000, 4420},
	{"SO-DPA3-WARN", 95000, 12350},
}

// glsProfileNote is the note a retired status card profile carries.
const glsProfileNote = "Retired. This dealer holds no status card exemption."

// glsConvergeSalesTax moves the migration-planted money and tax rows onto the demo dealer's
// currency, rate and words, and returns how many rows it changed.
func glsConvergeSalesTax(ctx context.Context, tx glsTx, now time.Time) (int, error) {
	changed := 0
	for _, s := range glsSettings {
		tag, err := tx.Exec(ctx,
			`UPDATE system_settings SET value = $3::jsonb, updated_at = $4 WHERE key = $1 AND value = $2::jsonb`,
			s.key, s.from, s.to, now)
		if err != nil {
			return changed, fmt.Errorf("demoseed: setting %s: %w", s.key, err)
		}
		changed += int(tag.RowsAffected())
	}
	for _, reason := range []string{"STD_ON_HST_13", "MANUAL_TAX_REVIEW"} {
		tag, err := tx.Exec(ctx,
			`UPDATE tax_rates SET rate_bps = $2, notes = $3, updated_at = $4
			  WHERE jurisdiction = 'ON' AND reason_code = $1 AND rate_bps = 1300 AND effective_to IS NULL`,
			reason, glsSalesTaxBps, glsSalesTaxNote, now)
		if err != nil {
			return changed, fmt.Errorf("demoseed: tax rate %s: %w", reason, err)
		}
		changed += int(tag.RowsAffected())
	}
	for _, code := range []string{"ON_RESERVE_DELIVERY", "FEDERAL_PICKUP", "THIRD_PROFILE"} {
		tag, err := tx.Exec(ctx,
			`UPDATE tax_status_card_profiles
			    SET name = 'Retired exemption profile', notes = $2, status = 'RETIRED', updated_at = $3
			  WHERE code = $1 AND status <> 'RETIRED'`,
			code, glsProfileNote, now)
		if err != nil {
			return changed, fmt.Errorf("demoseed: status card profile %s: %w", code, err)
		}
		changed += int(tag.RowsAffected())
	}
	for _, a := range glsAccounts {
		tag, err := tx.Exec(ctx,
			`UPDATE gl_accounts SET name = $3, updated_at = $4 WHERE code = $1 AND name = $2`,
			a.code, a.from, a.to, now)
		if err != nil {
			return changed, fmt.Errorf("demoseed: ledger account %s: %w", a.code, err)
		}
		changed += int(tag.RowsAffected())
	}
	for _, o := range glsPlantedOrders {
		newTax := taxOn(o.subtotalCents)
		tag, err := tx.Exec(ctx,
			`UPDATE orders SET tax_cents = $4, total_cents = subtotal_cents + $4, updated_at = $5
			  WHERE order_no IN ($1, '#' || $1) AND subtotal_cents = $2 AND tax_cents = $3`,
			o.orderNo, o.subtotalCents, o.plantedTax, newTax, now)
		if err != nil {
			return changed, fmt.Errorf("demoseed: planted order %s: %w", o.orderNo, err)
		}
		changed += int(tag.RowsAffected())
	}
	return changed, nil
}
