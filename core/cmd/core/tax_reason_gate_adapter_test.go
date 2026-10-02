package main

// tax_reason_gate_adapter_test.go pins the PRODUCTION reason-gate translation
// (lead coded, disclosed in the pull request body): posTaxAdapter is where the
// engine's reason-universe refusal becomes pos.ErrUnknownTaxReason, the
// sentinel writeErr maps to the cashier's 400. The rig's servedTaxAssessor
// mirror carries the same translation, but a mirror can drift (the sibling
// arm's unpinned translation was a round 1 P2 on the silent zero pull request,
// found exactly that way), so this leg drives the adapter itself:
//
//   - an out-of-universe reason is translated into the sentinel, the engine's
//     own words (the offending reason) riding in the message; the universe
//     check fires before any table read, so this leg needs no damage;
//   - an in-universe but ungated below-standard row (planted fixture scoped)
//     refuses with the typed MisconfiguredRateError and that refusal crosses
//     the seam RAW: never the 400 sentinel (it is the table's fault, not the
//     cashier's), never swallowed.
//
// Drop the adapter's errors.As arm and the first leg fails; broaden it to
// translate every refusal and the second fails.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tax"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ungatedPlantNotes is the marker this leg's planted row carries: the fixture
// only ever touches rows it marked itself, so a database lawfully carrying a
// FNT_PRODUCT row keeps it.
const ungatedPlantNotes = "ungated gate leg marker"

// plantUngatedZeroRow plants the misconfigured shape the admin edge now
// refuses to plant itself: FNT_PRODUCT (in the lawful universe, never gated)
// at 0 bps, marked as this leg's own. Fixture scoped by the marker: any stale
// row this leg left behind goes first, and the cleanup removes only marked
// rows.
func plantUngatedZeroRow(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DELETE FROM tax_rates WHERE jurisdiction = 'ON' AND reason_code = 'FNT_PRODUCT' AND notes = $1`, ungatedPlantNotes); err != nil {
		t.Fatalf("pre-clean the leg's marked rows: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO tax_rates (jurisdiction, reason_code, rate_bps, notes) VALUES ('ON', 'FNT_PRODUCT', 0, $1)`, ungatedPlantNotes); err != nil {
		t.Fatalf("plant the ungated zero row: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM tax_rates WHERE jurisdiction = 'ON' AND reason_code = 'FNT_PRODUCT' AND notes = $1`, ungatedPlantNotes); err != nil {
			t.Errorf("remove the planted ungated row: %v", err)
		}
	})
}

// TestPosTaxAdapter_TranslatesTheReasonUniverseRefusal drives the production
// adapter over the real engine, first on an out-of-universe reason, then on
// the planted misconfigured row.
func TestPosTaxAdapter_TranslatesTheReasonUniverseRefusal(t *testing.T) {
	pool := wiringPool(t)
	ctx := context.Background()
	a := posTaxAdapter{svc: tax.NewService(tax.NewRepository(pool))}

	// The out-of-universe reason: the sentinel, with the engine's words riding.
	_, err := a.AssessSale(ctx, pos.TaxQuoteInput{SubtotalCents: 100_000, ReasonCode: "FROB_NOMINAL_RELIEF"})
	if err == nil {
		t.Fatal("an out-of-universe reason must refuse, not bind")
	}
	if !errors.Is(err, pos.ErrUnknownTaxReason) {
		t.Fatalf("the adapter must translate the universe refusal into pos.ErrUnknownTaxReason, got %v", err)
	}
	if !strings.Contains(err.Error(), "FROB_NOMINAL_RELIEF") {
		t.Fatalf("the engine's words (the offending reason) must ride behind the sentinel, got %q", err.Error())
	}

	// The misconfigured row (in universe, ungated, below standard): the typed
	// refusal crosses RAW. The value itself, reason and rate, unchanged: this
	// is the table's fault and the ring surfaces it as the server fault it is.
	plantUngatedZeroRow(t, pool)
	_, err = a.AssessSale(ctx, pos.TaxQuoteInput{SubtotalCents: 100_000, ReasonCode: "FNT_PRODUCT"})
	if err == nil {
		t.Fatal("the ungated below-standard row must refuse, not bind untaxed")
	}
	if errors.Is(err, pos.ErrUnknownTaxReason) {
		t.Fatalf("the misconfigured-table refusal must not borrow the cashier's 400 sentinel: %v", err)
	}
	var mre tax.MisconfiguredRateError
	if !errors.As(err, &mre) {
		t.Fatalf("the typed misconfigured-rate refusal must cross the seam unchanged, got %T: %v", err, err)
	}
	if mre.ReasonCode != "FNT_PRODUCT" || mre.RateBps != 0 {
		t.Fatalf("the typed refusal must name the planted facts, got %+v", mre)
	}
}
