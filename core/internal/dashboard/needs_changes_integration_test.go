//go:build integration

// needs_changes_integration_test.go, migration 0411: the change orders source on the manager profiles.
//
// The rows Postgres holds must equal the Go fallbacks for every profile that carries the source (the same
// drift gate the other sources keep), the source sits last with the approvals source's edges, and no other
// profile carries it. The migration only ADDS rows (idempotently), so the rows each profile already had are
// read back unchanged.
package dashboard

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// needsChangesMigrationStatement is the migration's own text, so the rerun leg runs what ships.
var needsChangesMigrationStatement = func() string {
	b, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "0411_needs_you_change_orders.sql"))
	if err != nil {
		return "SELECT 'the 0411 migration file is missing'"
	}
	return string(b)
}()

func TestIntegration_0411_TheManagerProfilesCarryTheChangeOrdersSource(t *testing.T) {
	repo := widgetRepo(t)
	ctx := context.Background()
	for _, profile := range []string{
		ProfileAdmin, ProfileManager,
		ProfileManagerBranch, ProfileManagerSales, ProfileManagerController, ProfileManagerPayables, ProfileManagerGeneral,
	} {
		got, err := repo.NeedsSources(ctx, profile)
		if err != nil {
			t.Fatalf("NeedsSources(%s): %v", profile, err)
		}
		want := defaultNeedsFor(profile)
		if len(got) != len(want) {
			t.Fatalf("%s: the table holds %d sources %v, the fallback ships %d %v", profile, len(got), needsKeys(got), len(want), needsKeys(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s row %d: database %+v, fallback %+v", profile, i, got[i], want[i])
			}
		}
		last := got[len(got)-1]
		if last.SourceKey != NeedsChangesWaiting || !last.Visible || last.WarnAt != 1 || last.StopAt != 5 || last.StaleMins != 240 {
			t.Fatalf("%s: the last row is %+v, want %s visible with warn 1, stop 5, stale 240", profile, last, NeedsChangesWaiting)
		}
	}
}

func TestIntegration_0411_NoOtherProfileCarriesIt(t *testing.T) {
	repo := widgetRepo(t)
	for _, profile := range []string{
		ProfileCounter, ProfileYard, ProfileManagerYard, ProfileManagerLogistics, ProfileManagerProcurement,
	} {
		got, err := repo.NeedsSources(context.Background(), profile)
		if err != nil {
			t.Fatalf("NeedsSources(%s): %v", profile, err)
		}
		for _, p := range got {
			if p.SourceKey == NeedsChangesWaiting {
				t.Errorf("%s carries %s: 0411 only files it on the signing managers", profile, NeedsChangesWaiting)
			}
		}
	}
}

// The migration is idempotent: running its statement a second time adds nothing (the way 0381 files its
// copies), and a profile a dealer emptied on purpose stays empty.
func TestIntegration_0411_RerunningTheStatementAddsNothingAndAnEmptiedProfileStaysEmpty(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	var before, after int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_needs_sources`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, needsChangesMigrationStatement); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_needs_sources`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("the statement added %d rows on a second run, want 0", after-before)
	}
}
