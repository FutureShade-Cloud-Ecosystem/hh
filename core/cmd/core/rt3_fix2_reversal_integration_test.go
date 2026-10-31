//go:build integration

package main

// rt3_fix2_reversal_integration_test.go, the RT-3 fix round 2, item 4 (P3-3 and P3-4): a replay of a key whose
// release was reversed says so, and the reversal's audit row is written inside its transaction.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
)

// P3-3: the first key, replayed after the release was reversed, answers that it was reversed, never "replayed"
// as if the release stood.
func TestRT3Fix2_AReplayOfAReversedReleaseSaysItWasReversed(t *testing.T) {
	l := rt2Setup(t, "f2k")
	l.newJob(true)
	doc := l.invoice(100_000, 0)
	l.rt3PerfOn(70)

	code, body := l.rt3Over("f2k-key")
	if code != http.StatusCreated {
		t.Fatalf("release: want 201, got %d %s", code, body)
	}
	l.drain()
	var releaseID string
	if err := l.pool.QueryRow(context.Background(), `SELECT id::text FROM holdback_ledger WHERE invoice_id = $1::uuid AND entry_type = 'RELEASE'`, doc.InvoiceID).Scan(&releaseID); err != nil {
		t.Fatalf("read the release: %v", err)
	}
	if code, body := l.call("POST", "/api/v1/ar/holdback/releases/"+releaseID+"/reversal", `{"reason":"released in error"}`); code != http.StatusOK {
		t.Fatalf("reverse: want 200, got %d %s", code, body)
	}
	l.drain()

	code, body = l.rt3Over("f2k-key")
	if code != http.StatusConflict || !strings.Contains(body, "reversed") || strings.Contains(body, `"replayed":true`) {
		t.Errorf("the replay of a reversed release: want 409 saying it was reversed, got %d %s", code, body)
	}
	eq(t, "nothing released by the replay", l.holdbackOf(doc.InvoiceID), int64(10_000))
}

// failingAudit is a recorder that refuses every write, in and out of a transaction.
type failingAudit struct{}

func (failingAudit) Record(context.Context, audit.SensitiveAccess) error {
	return errors.New("audit down")
}
func (failingAudit) RecordTx(context.Context, audit.Execer, audit.SensitiveAccess) error {
	return errors.New("audit down")
}

// P3-4: the reversal and its audit row (with the reason) are one transaction; a failing audit write rolls the
// reversal back.
func TestRT3Fix2_AReversalWhoseAuditFailsRollsBack(t *testing.T) {
	l := rt2Setup(t, "f2m")
	l.newJob(true)
	doc := l.invoice(100_000, 0)
	l.rt3PerfOn(70)
	res, err := l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input("f2m-key"))
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	l.drain()
	before1150, before1100, rows := l.glBalance("1150"), l.glBalance("1100"), l.ledgerRows()

	svc := billing.NewHoldbackReleaseService(holdBillingRepo(l.pool)).WithAudit(failingAudit{})
	_, err = svc.Reverse(context.Background(), billing.HoldbackReversalInput{
		ReleaseID: res.Items[0].ReleaseID, Reason: "released in error", ActorID: "d7000000-0000-4000-9000-000000000006", IsController: true,
	})
	if err == nil {
		t.Fatalf("a reversal whose audit row cannot be written must fail")
	}
	l.drain()
	eq(t, "the column is still released", l.holdbackOf(doc.InvoiceID), int64(0))
	eq(t, "1150 unchanged", l.glBalance("1150"), before1150)
	eq(t, "1100 unchanged", l.glBalance("1100"), before1100)
	eq(t, "ledger rows unchanged", l.ledgerRows(), rows)
	var reversed int
	if err := l.pool.QueryRow(context.Background(), `SELECT count(*) FROM holdback_ledger WHERE id = $1::uuid AND reversed_at IS NOT NULL`, res.Items[0].ReleaseID).Scan(&reversed); err != nil {
		t.Fatalf("read the release: %v", err)
	}
	eq(t, "the release is not marked reversed", reversed, 0)
}
