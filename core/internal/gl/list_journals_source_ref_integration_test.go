//go:build integration

package gl

import (
	"context"
	"fmt"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// TestIntegration_ListJournals_FilterBySourceRefID is the L8 regression for the SILENTLY-IGNORED
// source_ref_id query param on GET /api/v1/gl/journals. Before the fix, JournalFilter carried no SourceRefID
// and the store WHERE clause never referenced it, so a caller filtering by source ref got EVERY journal back
// (the param was dropped). This test posts two journals under distinct source refs and asserts the filter
// returns only the matching one. Without the wiring the assertion `total == 1` fails (both rows come back).
func TestIntegration_ListJournals_FilterBySourceRefID(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2097)
	at := time.Date(2097, 6, 15, 12, 0, 0, 0, time.UTC)
	// DIB-392: listing journals across every branch is a GLOBAL-scope read (a branch-scoped manager must
	// name their branch), so this source_ref_id regression runs as the Financial Controller.
	svc := svcAt(pool, identity.StaffContext{
		UserID: user, RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
	}, at)

	// Two source refs (must be valid UUIDs, gl_journals.source_ref_id is UUID).
	refA := "11111111-1111-1111-1111-111111111111"
	refB := "22222222-2222-2222-2222-222222222222"
	uniq := time.Now().UnixNano()

	post := func(ref string, tag string) string {
		id, err := svc.PostExternal(ctx, ExternalJournal{
			Source:         SourceAdjustment,
			SourceRefID:    ref,
			IdempotencyKey: fmt.Sprintf("srcref-itest-%d-%s", uniq, tag),
			Memo:           "source_ref filter test " + tag,
			Lines: []ExternalLine{
				{AccountCode: "1000", DebitCents: 1000, Memo: "bank"},
				{AccountCode: "1020", CreditCents: 1000, Memo: "undeposited"},
			},
		})
		if err != nil {
			t.Fatalf("post external (%s): %v", tag, err)
		}
		return id
	}
	idA := post(refA, "A")
	post(refB, "B")

	// Filter by refA, must return exactly the refA journal.
	js, err := svc.ListJournals(ctx, JournalFilter{SourceRefID: refA})
	if err != nil {
		t.Fatalf("list journals by source_ref_id: %v", err)
	}
	if len(js) != 1 {
		t.Fatalf("filter by source_ref_id=%s must return exactly 1 journal (the param was ignored?), got %d", refA, len(js))
	}
	if len(js) != 1 || js[0].ID != idA {
		t.Fatalf("filtered result must be journal %s, got %+v", idA, js)
	}
	if js[0].SourceRefID != refA {
		t.Fatalf("returned journal has source_ref_id=%s, want %s", js[0].SourceRefID, refA)
	}

	// An empty source_ref_id must NOT filter (must include both refs among results).
	all, err := svc.ListJournals(ctx, JournalFilter{})
	if err != nil {
		t.Fatalf("list journals unfiltered: %v", err)
	}
	if len(all) < 2 {
		t.Fatalf("unfiltered list must include both journals, got %d", len(all))
	}
}
