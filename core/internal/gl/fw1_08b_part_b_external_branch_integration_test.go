//go:build integration

// fw1_08b_part_b_external_branch_integration_test.go is the RUNTIME half of the part B rule
// (FROM-PRINCIPAL 318): the static census reads SQL column lists and cannot see a runtime path,
// so a caller-built external journal that names NO branch is posted through the production
// service here, and every line it lands is read back for the head office branch.
//
// The leg is the one K3's P1 was about: before the fix, externalDraft copied the caller's empty
// branch into the draft, the shared insert turned it into NULL, and migration 0446's CHECK
// refused the row. After the fix the draft resolves the head office through the same resolver
// the internal path uses, so every line carries it.
package gl

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/branches"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// TestIntegration_FW1_08b_PartB_AnExternalJournalWithNoBranchStampsTheHeadOfficeOnEveryLine
// posts an external journal that names no branch, through the production service, and holds
// every line it lands to the head office branch. It is the runtime check FROM-PRINCIPAL 318
// asks for: the static census missed this path, so a leg exercises it.
func TestIntegration_FW1_08b_PartB_AnExternalJournalWithNoBranchStampsTheHeadOfficeOnEveryLine(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2098)
	at := time.Date(2098, 6, 15, 12, 0, 0, 0, time.UTC)
	// The production wiring's seam: the head office reads TRT by code through the branches
	// module, the way wire_money binds it.
	svc := svcAt(pool, identity.StaffContext{
		UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeGlobal,
	}, at).WithBranchFacts(fw1R4BranchFacts{repo: branches.NewRepository(pool)})

	headOffice, err := svc.HeadOfficeBranchID(ctx)
	if err != nil {
		t.Fatalf("the head office branch: %v", err)
	}
	if headOffice == "" {
		t.Fatal("the head office branch answered empty; the leg cannot check what it is stamping")
	}

	// The provider surface exactly as a bankrec or a landedcost poster calls it: the caller
	// names no branch, so the service owns the answer. The key rides the leg's one pinned clock
	// (TestSuiteHasNoDatedTimeBombs): the cleanup below removes the journal, so a fixed key
	// never meets a row from an earlier run.
	key := fmt.Sprintf("fw1-08b-part-b-external-no-branch-%d", at.UnixNano())
	id, err := svc.PostExternal(ctx, ExternalJournal{
		Source:         SourceAdjustment,
		IdempotencyKey: key,
		Memo:           "FW1-08b part B: a company level external journal with no branch",
		Lines: []ExternalLine{
			{AccountCode: "1000", DebitCents: 25000, Memo: "cash"},
			{AccountCode: "4000", CreditCents: 25000, Memo: "revenue"},
		},
	})
	if err != nil {
		t.Fatalf("posting an external journal with no branch: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journal_lines WHERE journal_id = $1::uuid`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journals WHERE id = $1::uuid`, id)
	})

	var stamped, total, unstamped int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE branch_id::text = $2),
		        count(*),
		        count(*) FILTER (WHERE branch_id IS NULL)
		   FROM gl_journal_lines
		  WHERE journal_id = $1::uuid`, id, headOffice).Scan(&stamped, &total, &unstamped); err != nil {
		t.Fatalf("read the external journal's lines: %v", err)
	}
	if total != 2 {
		t.Fatalf("the external journal landed %d line(s), want 2", total)
	}
	if unstamped != 0 {
		t.Errorf("%d of the %d line(s) carry no branch; every line of a journal carries the branch it posted at", unstamped, total)
	}
	if stamped != total {
		t.Errorf("%d of the %d line(s) carry the head office branch %q, want every line", stamped, total, headOffice)
	}

	// The header carries the same branch: the draft's branch is the header's and every line's.
	var header string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(branch_id::text, '') FROM gl_journals WHERE id = $1::uuid`, id).Scan(&header); err != nil {
		t.Fatalf("read the external journal's header: %v", err)
	}
	if header != headOffice {
		t.Errorf("the external journal's header carries branch %q, want the head office branch %q", header, headOffice)
	}
}
