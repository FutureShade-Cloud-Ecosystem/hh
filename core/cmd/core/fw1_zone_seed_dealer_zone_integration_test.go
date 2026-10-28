//go:build integration

// fw1_zone_seed_dealer_zone_integration_test.go, FW1 zone seed: the seed and the testkit date their
// invoices in the DEALER'S zone, not in Ontario's.
//
// THE RULE. A dealer's business day is the day in the dealer's own zone: the business_zone key in
// system_settings (migration 0014), read by settings_business_zone() (migration 0394). Every document
// the house dates itself takes that day. The seed and the testkit were the two places that still
// spelled Ontario out as a literal, (now() AT TIME ZONE 'America/Toronto')::date, so a dealer east or
// west of Ontario got invoices dated the wrong calendar day whenever the two zones sat on different
// days, which is most of the day.
//
// THE LEGS, each on its own fresh migrated database:
//
//	1. THE SEED: the dealer's zone is set to a far zone, the seed is applied, and the invoices it
//	   plants are read back. Each must carry the DEALER's day and the seed's own offsets from it.
//	2. THE TESTKIT: the same far zone, the kit's synthetic fixture built on top of the seeded world,
//	   and the fixture's invoices must carry the dealer's day too.
//
// THE FAR ZONE IS PICKED AT RUN TIME, never hard coded: the leg asks the database for a zone whose
// calendar day differs from Toronto's at this instant and whose day will not roll over inside the
// leg. Pacific/Pago_Pago and Pacific/Kiritimati roll over an hour apart, so one of them always
// qualifies, and the leg says which one it took. The seed and the read then run in ONE transaction,
// where now() is the transaction's own start: the day the seed read and the day the leg compares
// against are the same day by construction. The red is therefore deterministic, and cannot age into
// a flaky leg the way a leg that reads the clock twice would.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 \
//	     -run 'FW1SeedDealerZone' ./cmd/core/

package main

import (
	"context"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// fw1ZoneDB is the surface the leg needs from a pool or a transaction: both satisfy it, so the
// helpers work the same on either.
type fw1ZoneDB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// fw1ZoneTxDB runs the testkit's fixture statements on the leg's own transaction, so the fixture's
// day and the leg's day are the same day.
type fw1ZoneTxDB struct{ tx pgx.Tx }

func (d fw1ZoneTxDB) Exec(ctx context.Context, query string, args ...any) error {
	_, err := d.tx.Exec(ctx, query, args...)
	return err
}

func (d fw1ZoneTxDB) Count(ctx context.Context, query string, args ...any) (int, error) {
	var n int
	err := d.tx.QueryRow(ctx, query, args...).Scan(&n)
	return n, err
}

// fw1ZoneFarZone picks, at run time, a zone whose calendar day differs from Toronto's right now and
// whose day does not roll over inside the leg. The second condition is what keeps the leg honest
// across the two candidate zones' rollovers, an hour apart, so one of them always qualifies.
func fw1ZoneFarZone(t *testing.T, db fw1ZoneDB) string {
	t.Helper()
	var z string
	err := db.QueryRow(context.Background(), `
		SELECT z FROM unnest(ARRAY['Pacific/Pago_Pago','Pacific/Kiritimati']) AS z
		 WHERE (now() AT TIME ZONE z)::date <> (now() AT TIME ZONE 'America/Toronto')::date
		   AND (now() AT TIME ZONE z)::date = ((now() + interval '2 minutes') AT TIME ZONE z)::date
		 LIMIT 1`).Scan(&z)
	if err != nil {
		t.Fatalf("picking a zone on another day than Ontario's: %v", err)
	}
	return z
}

// fw1ZoneSet stores the dealer's zone in system_settings, the key settings_business_zone() reads.
func fw1ZoneSet(t *testing.T, db fw1ZoneDB, zone string) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `
		INSERT INTO system_settings (key, value) VALUES ('business_zone', $1::jsonb)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, strconv.Quote(zone)); err != nil {
		t.Fatalf("setting the dealer's zone: %v", err)
	}
}

// TestIntegration_FW1SeedDealerZone_TheSeedDatesItsInvoicesInTheDealersZone proves the seed dates the
// invoices it plants in the DEALER's zone.
func TestIntegration_FW1SeedDealerZone_TheSeedDatesItsInvoicesInTheDealersZone(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "fw1zone")

	// ONE transaction for the seed and the read: now() is the transaction's start, so the day the
	// seed read is the day the leg compares against, whatever the clock does in between.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("beginning the leg's transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	zone := fw1ZoneFarZone(t, tx)
	fw1ZoneSet(t, tx, zone)

	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	if _, err := tx.Exec(ctx, string(body)); err != nil {
		t.Fatalf("applying the seed: %v", err)
	}

	// The day the dealer's zone is on and the day Ontario is on. The leg's whole point is that they
	// differ, so the leg proves something rather than nothing.
	var dealerDay, ontarioDay string
	if err := tx.QueryRow(ctx, `
		SELECT to_char((now() AT TIME ZONE settings_business_zone())::date, 'YYYY-MM-DD'),
		       to_char((now() AT TIME ZONE 'America/Toronto')::date, 'YYYY-MM-DD')`).Scan(&dealerDay, &ontarioDay); err != nil {
		t.Fatalf("reading the dealer's day: %v", err)
	}
	if dealerDay == ontarioDay {
		t.Fatalf("FW1 zone seed: the dealer's zone %s is on Ontario's day %s, so the leg would prove nothing (the leg needs a zone on another day)",
			zone, dealerDay)
	}

	// The seed's own two invoices, each with the day it wants: the settled one thirty days before the
	// dealer's day and due on it, the open one ten days before it and due thirty-five days after.
	type seeded struct {
		no, issue, due, wantIssue, wantDue string
	}
	rows, err := tx.Query(ctx, `
		SELECT i.invoice_no,
		       to_char(i.issue_date, 'YYYY-MM-DD'),
		       to_char(i.due_date, 'YYYY-MM-DD'),
		       to_char((now() AT TIME ZONE settings_business_zone())::date
		              - CASE i.invoice_no WHEN 'INV-2026-0001' THEN 30 ELSE 10 END, 'YYYY-MM-DD'),
		       to_char((now() AT TIME ZONE settings_business_zone())::date
		              + CASE i.invoice_no WHEN 'INV-2026-0001' THEN 0 ELSE 35 END, 'YYYY-MM-DD')
		  FROM invoices i
		 WHERE i.invoice_no IN ('INV-2026-0001','INV-2026-0002')
		 ORDER BY i.invoice_no`)
	if err != nil {
		t.Fatalf("reading the seeded invoices: %v", err)
	}
	defer rows.Close()
	var seen []seeded
	for rows.Next() {
		var s seeded
		if err := rows.Scan(&s.no, &s.issue, &s.due, &s.wantIssue, &s.wantDue); err != nil {
			t.Fatalf("scan the seeded invoice: %v", err)
		}
		seen = append(seen, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the seeded invoices: %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("FW1 zone seed: the seed planted %d of its two invoices, want 2, so the leg proves nothing", len(seen))
	}
	for _, s := range seen {
		if s.issue != s.wantIssue || s.due != s.wantDue {
			t.Errorf("FW1 zone seed: %s is dated %s to %s, want %s to %s (the dealer's day in %s is %s, Ontario's day is %s: the seed dates its documents in the DEALER's zone)",
				s.no, s.issue, s.due, s.wantIssue, s.wantDue, zone, dealerDay, ontarioDay)
		}
	}
}

// TestIntegration_FW1SeedDealerZone_TheTestkitFixtureDatesItsInvoicesInTheDealersZone proves the
// testkit's synthetic fixture dates its invoices in the DEALER's zone.
func TestIntegration_FW1SeedDealerZone_TheTestkitFixtureDatesItsInvoicesInTheDealersZone(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "fw1zonekit")

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("beginning the leg's transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	zone := fw1ZoneFarZone(t, tx)
	fw1ZoneSet(t, tx, zone)

	// The kit's fixture lives beside the seed's world, exactly as the integration gate's database
	// carries it, so the seed goes down first.
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	if _, err := tx.Exec(ctx, string(body)); err != nil {
		t.Fatalf("applying the seed: %v", err)
	}
	if err := testkit.Build(ctx, fw1ZoneTxDB{tx: tx}); err != nil {
		t.Fatalf("building the testkit fixture: %v", err)
	}

	// The fixture's invoices: the dealer's day, due thirty days after it.
	rows, err := tx.Query(ctx, `
		SELECT i.invoice_no,
		       to_char(i.issue_date, 'YYYY-MM-DD'),
		       to_char(i.due_date, 'YYYY-MM-DD'),
		       to_char((now() AT TIME ZONE settings_business_zone())::date, 'YYYY-MM-DD'),
		       to_char((now() AT TIME ZONE settings_business_zone())::date + 30, 'YYYY-MM-DD'),
		       to_char((now() AT TIME ZONE 'America/Toronto')::date, 'YYYY-MM-DD')
		  FROM invoices i
		 WHERE i.invoice_no IN ('FXINV-A-1','FXINV-B-1')
		 ORDER BY i.invoice_no`)
	if err != nil {
		t.Fatalf("reading the fixture's invoices: %v", err)
	}
	defer rows.Close()
	var seen int
	for rows.Next() {
		var no, issue, due, wantIssue, wantDue, ontarioDay string
		if err := rows.Scan(&no, &issue, &due, &wantIssue, &wantDue, &ontarioDay); err != nil {
			t.Fatalf("scan the fixture's invoice: %v", err)
		}
		seen++
		if issue != wantIssue || due != wantDue {
			t.Errorf("FW1 zone seed: the testkit fixture's %s is dated %s to %s, want %s to %s (the dealer's day in %s is %s, Ontario's day is %s: the fixture dates its documents in the DEALER's zone)",
				no, issue, due, wantIssue, wantDue, zone, wantIssue, ontarioDay)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the fixture's invoices: %v", err)
	}
	if seen != 2 {
		t.Fatalf("FW1 zone seed: the testkit fixture planted %d of its two invoices, want 2, so the leg proves nothing", seen)
	}
}
