//go:build integration

// landedcost_gl_tx_wire_integration_test.go, the production adapter (landedGLPoster) over the real gl
// service, driven through a caller's transaction. The landedcost legs drive a test copy of the adapter; this
// leg pins the one that ships: its PostJournalTx must ride the caller's transaction, so a rolled back caller
// leaves no journal. An adapter that fell back to the pool variant would commit the journal on its own.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 \
//	     -run LandedGLTxAdapter ./cmd/core/
package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/landedcost"
)

func TestIntegration_LandedGLTxAdapter_RidesTheCallersTransaction(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	poster := landedGLPoster{svc: gl.NewService(gl.NewRepository(pool))}

	key := fmt.Sprintf("wire-landed-gltx-%d", time.Now().UnixNano())
	count := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, key).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	// A landed cost belongs to a branch (internal/landedcost/service.go refuses one with no branch), so
	// the fixture names the branch the landed cost is at. KGN is a real branch that is NOT the head
	// office: a fixture that left the branch off would ride the resolver's head office fallback and
	// would not prove that the adapter carries the document's own branch (S4's census, FW1-08b part B
	// round 3 item 1).
	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE code = 'KGN' LIMIT 1`).Scan(&branchID); err != nil {
		t.Fatalf("resolve KGN branch: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id, err := poster.PostJournalTx(ctx, tx, landedcost.GLJournal{
		Source: "ADJUSTMENT", IdempotencyKey: key, Memo: "wire leg",
		BranchID: branchID,
		Lines: []landedcost.GLLine{
			{AccountCode: "2100", DebitCents: 700},
			{AccountCode: "1200", CreditCents: 700},
		},
	})
	if err != nil || id == "" {
		t.Fatalf("post through tx: %q %v", id, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Fatalf("a rolled back caller left %d journal(s): the adapter did not ride the caller's transaction", n)
	}
}
