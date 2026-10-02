package main

// tax_adapter_translation_test.go pins the PRODUCTION translation (round 1's
// P2): posTaxAdapter is the only place the tax engine's typed no-standard-rate
// refusal becomes pos.ErrTaxRailUnavailable, the sentinel writeErr maps to the
// counter-readable 503. The pos-side legs prove the rig's hand copy of the
// translation; this leg drives the adapter itself, over a real *tax.Service on
// a real table with the standard row gone, and demands three things: the
// sentinel comes back on the damaged table; the tax engine's own typed error
// does NOT leak through (pos must not need the tax package to understand its
// refusal); and a repository error passes through RAW, never borrowing the
// sentinel. Drop the three-line translation in posTaxAdapter and this leg
// fails while every no-database suite stays green: that was exactly the hole
// round 1's review found.
//
// The service here is the plain tax.NewService over tax.NewRepository: the
// composition root decorates it WithGLBalances and WithAudit for the snapshot
// and GL arms, none of which sits on AssessSale's refusal path, so the plain
// service is the honest minimum for this pin.
//
// The damage is fixture-scoped exactly as the pos leg's: DELETE RETURNING
// captures every column including the timestamps, and the cleanup puts the
// rows back byte identical.

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tax"
	"github.com/jackc/pgx/v5/pgxpool"
)

// stdRateRow is one captured tax_rates row, every column the restore needs.
type stdRateRow struct {
	id      string
	bps     int
	fromD   string
	toD     string
	notes   string
	created string
	updated string
}

// dropStdRateRows removes every ON STD_ON_HST_13 row in ONE statement
// (DELETE RETURNING: no capture/delete window) and restores them byte
// identical, timestamps included, in the leg's cleanup.
func dropStdRateRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx, `
		DELETE FROM tax_rates WHERE jurisdiction = 'ON' AND reason_code = 'STD_ON_HST_13'
		RETURNING id::text, rate_bps, effective_from::text, COALESCE(effective_to::text, ''), notes,
		          created_at::text, updated_at::text`)
	if err != nil {
		t.Fatalf("remove the standard rows: %v", err)
	}
	var captured []stdRateRow
	for rows.Next() {
		var r stdRateRow
		if err := rows.Scan(&r.id, &r.bps, &r.fromD, &r.toD, &r.notes, &r.created, &r.updated); err != nil {
			rows.Close()
			t.Fatalf("scan the removed standard rows: %v", err)
		}
		captured = append(captured, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("read the removed standard rows: %v", err)
	}
	if len(captured) == 0 {
		t.Fatal("no seeded STD_ON_HST_13 row to remove: the scratch world is not migrated and seeded")
	}
	t.Cleanup(func() {
		for _, r := range captured {
			if _, err := pool.Exec(ctx, `
				INSERT INTO tax_rates (id, jurisdiction, reason_code, rate_bps, effective_from, effective_to, notes, created_at, updated_at)
				VALUES ($1::uuid, 'ON', 'STD_ON_HST_13', $2, $3::date, NULLIF($4, '')::date, $5, $6::timestamptz, $7::timestamptz)
				ON CONFLICT (id) DO NOTHING`, r.id, r.bps, r.fromD, r.toD, r.notes, r.created, r.updated); err != nil {
				t.Errorf("restore the standard rows: %v", err)
			}
		}
	})
}

// TestPosTaxAdapter_TranslatesTheEnginesRefusal drives the production adapter
// over the real engine on a damaged table, then over a closed pool.
func TestPosTaxAdapter_TranslatesTheEnginesRefusal(t *testing.T) {
	pool := wiringPool(t)
	dropStdRateRows(t, pool)
	ctx := context.Background()

	a := posTaxAdapter{svc: tax.NewService(tax.NewRepository(pool))}
	in := pos.TaxQuoteInput{SubtotalCents: 100_000}

	// The damaged table: the adapter must answer the pos sentinel, the value
	// itself (not merely an errors.Is match): writeErr's arm keys on it.
	_, err := a.AssessSale(ctx, in)
	if err == nil {
		t.Fatal("no standard rate row in effect: the adapter must refuse, not let a silent zero quote cross the seam")
	}
	if !errors.Is(err, pos.ErrTaxRailUnavailable) {
		t.Fatalf("the adapter must translate the engine's refusal into pos.ErrTaxRailUnavailable, got %v", err)
	}
	if err != pos.ErrTaxRailUnavailable {
		t.Fatalf("the translation must return the sentinel itself (writeErr keys on the value), got %v", err)
	}
	// The engine's own typed error must NOT leak: pos never imports the tax
	// package, and a caller above the seam cannot be asked to know tax's
	// sentinel. If this fails, the adapter passes the refusal through instead
	// of translating it.
	if errors.Is(err, tax.ErrNoStandardRate) {
		t.Fatalf("the tax engine's typed error leaked across the seam untranslated: %v", err)
	}

	// A repository failure is a different failure: it must pass through RAW,
	// never borrowing the counter-readable sentinel. The closed pool is a
	// SECOND pool over the same DSN: closing the shared wiringPool here would
	// also kill the fixture's restore cleanup (its INSERT would run on the
	// closed pool and the damaged world would outlive the leg, exactly the
	// crash-shape round 1's P3-2 warns about).
	closedDSN := os.Getenv(coreDBEnvVar)
	closedPool, err := pgxpool.New(ctx, closedDSN)
	if err != nil {
		t.Fatalf("open the throwaway pool: %v", err)
	}
	closedPool.Close()
	_, err = posTaxAdapter{svc: tax.NewService(tax.NewRepository(closedPool))}.AssessSale(ctx, in)
	if err == nil {
		t.Fatal("a repository failure must surface, not vanish")
	}
	if errors.Is(err, pos.ErrTaxRailUnavailable) {
		t.Fatalf("a repository error must pass through raw, not borrow the tax rail sentinel: %v", err)
	}
}
