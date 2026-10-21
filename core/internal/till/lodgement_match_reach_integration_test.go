//go:build integration

package till

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/bankrec"
)

// The match and the unmatch of a LODGEMENT, on the REAL till seam (SF-2j, S3's P3 3), not the fake the bankrec legs
// use: one real LODGEMENT movement on branch B, and manual matches and an unmatch from a reach that covers only
// branch A and from one that covers branch B.
func TestIntegration_LodgementMatchAndUnmatchReachOnTheRealTillSeam(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	var branches []string
	rows, err := pool.Query(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' ORDER BY id LIMIT 2`)
	if err != nil {
		t.Fatalf("branches: %v", err)
	}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		branches = append(branches, id)
	}
	rows.Close()
	if len(branches) < 2 {
		t.Skipf("need two seeded branches, found %d", len(branches))
	}
	branchA, branchB := branches[0], branches[1]
	var user, session, lodgement, account, line string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE status = 'ACTIVE' LIMIT 1`).Scan(&user); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM bank_accounts WHERE gl_account_code = '1000'`).Scan(&account); err != nil {
		t.Skipf("no seeded bank account: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO till_sessions (branch_id, station, opened_by) VALUES ($1::uuid, 'SF2JLG', $2::uuid) RETURNING id::text`, branchB, user).Scan(&session); err != nil {
		t.Fatalf("session: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO till_movements (till_session_id, movement_type, method, amount_cents, reason, reference, created_by)
		VALUES ($1::uuid, 'LODGEMENT', 'CASH', 7000, 'test', 'SF2J-LG', $2::uuid) RETURNING id::text`, session, user).Scan(&lodgement); err != nil {
		t.Fatalf("lodgement: %v", err)
	}
	hash := fmt.Sprintf("sf2j-lodgement-%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `INSERT INTO bank_statement_lines (bank_account_id, txn_date, description, reference, amount_cents, dedupe_hash, status)
		VALUES ($1::uuid, now(), 'SF2J LODGEMENT', 'SF2J-LG', 7000, $2, 'UNMATCHED') RETURNING id::text`, account, hash).Scan(&line); err != nil {
		t.Fatalf("line: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM bank_matches WHERE statement_line_id = $1::uuid`, line)
		_, _ = pool.Exec(c, `DELETE FROM bank_statement_lines WHERE id = $1::uuid`, line)
		_, _ = pool.Exec(c, `DELETE FROM till_movements WHERE till_session_id = $1::uuid`, session)
		_, _ = pool.Exec(c, `DELETE FROM till_sessions WHERE id = $1::uuid`, session)
	})

	svc := bankrec.NewService(bankrec.NewRepository(pool)).WithLodgements(NewRepository(pool))
	only := func(b string) func(string) bool { return func(x string) bool { return x == b } }
	state := func() (status string, live int) {
		_ = pool.QueryRow(ctx, `SELECT status FROM bank_statement_lines WHERE id = $1::uuid`, line).Scan(&status)
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM bank_matches WHERE statement_line_id = $1::uuid AND unmatched_at IS NULL`, line).Scan(&live)
		return status, live
	}
	match := func(target string, reach func(string) bool) (bankrec.Match, error) {
		return svc.ManualMatch(ctx, bankrec.MatchInput{Mode: "manual", StatementLineID: line, TargetType: bankrec.TargetLodgement, TargetRefID: target, CanAccessBranch: reach}, "")
	}

	for name, target := range map[string]string{"an unknown uuid": "33333333-3333-4333-8333-3333333333ee", "text": "not-an-id", "branch B's lodgement": lodgement} {
		if _, err := match(target, only(branchA)); err == nil || err.Error() != "bankrec: not found" {
			t.Errorf("%s from reach A: %v, want bankrec: not found", name, err)
		}
		if s, n := state(); s != "UNMATCHED" || n != 0 {
			t.Fatalf("%s from reach A wrote: line %s, %d live matches", name, s, n)
		}
	}
	m, err := match(lodgement, only(branchB))
	if err != nil {
		t.Fatalf("the match from reach B: %v", err)
	}
	if s, n := state(); s != "MATCHED" || n != 1 {
		t.Fatalf("after the match from reach B: line %s, %d live matches", s, n)
	}
	// The unmatch from reach A is refused as a match that is not live, and writes nothing.
	if err := svc.UnmatchReach(ctx, m.ID, "", only(branchA)); err == nil {
		t.Fatalf("the unmatch of branch B's lodgement from reach A succeeded")
	} else {
		t.Logf("the unmatch from reach A: %v", err)
	}
	if s, n := state(); s != "MATCHED" || n != 1 {
		t.Errorf("after the refused unmatch: line %s, %d live matches, want MATCHED and 1", s, n)
	}
	if err := svc.UnmatchReach(ctx, m.ID, "", only(branchB)); err != nil {
		t.Fatalf("the unmatch from reach B: %v", err)
	}
	if s, n := state(); s != "UNMATCHED" || n != 0 {
		t.Errorf("after the unmatch from reach B: line %s, %d live matches, want UNMATCHED and 0", s, n)
	}
}
