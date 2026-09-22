//go:build integration

// integration_w5b_test.go, the Wave 5 Swarm B surfaces against REAL Postgres: the 0129 posting rows and their
// CHECKs, the 0130 recovery VIEW (where outstanding = accrued − received actually lives), the 0131 public write
// with real BYTEA photos and a real atomic rate limiter, and the 0132 phase clocks derived from the audit spine.
//
// These exist because the unit fakes assert the CONTRACT and the SQL is where the contract is really enforced.
// The view in particular has no Go equivalent to be wrong in step with.
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:5432/swarm5_b?sslmode=disable' \
//	  go test -tags=integration ./internal/warranty/... -count=1
package warranty

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// w5bSvc is realSvc plus the two W5-B money-path seams and a controllable clock.
func w5bSvc(pool *pgxpool.Pool, caller identity.StaffContext, now *time.Time) *Service {
	glSvc := gl.NewService(gl.NewRepository(pool))
	return NewService(NewRepository(pool)).
		WithGLPoster(realGLPoster{svc: glSvc}).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return caller, caller.UserID != "" }).
		withClock(func() time.Time { return *now })
}

// w5bClaim walks a fresh claim to DECISION and REGISTERS ITS CLEANUP.
//
// FIXTURES RESTORE WHAT THEY TOUCH. Without this every run left a settled, accruing claim behind, and the
// WTY-RECOVERY board, which aggregates every accrual in the branch, grew by one row per test per run. That is
// not merely untidy: the board is a MONEY total, so test residue silently inflates the number a manager reads,
// and the first person to notice would be the one asking why the business is owed $160k. The ON DELETE CASCADE
// on postings/actions/events means deleting the claim is enough.
func w5bClaim(t *testing.T, svc *Service, pool *pgxpool.Pool, ctx context.Context, in ClaimInput) string {
	t.Helper()
	id := advanceToDecision(t, svc, ctx, in)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM warranty_claims WHERE id = $1`, id)
	})
	return id
}

// - 0129: the posting row is real, and its CHECKs hold -

func TestIntegrationW5B_VendorCreditPostsRealJournalAndRecordsTheAccrual(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	svc := w5bSvc(pool, staffCaller(demoOwner), &now)

	id := w5bClaim(t, svc, pool, ctx, ClaimInput{BranchID: trentonBranch, Description: "W5B vendor credit"})
	res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeVendorCredit, RealizedCents: 3_400_00})
	if err != nil {
		t.Fatal(err)
	}
	if res.MoneyPath != PathVendorRecovery || res.PathStatus != PathPosted || res.GLJournalID == "" {
		t.Fatalf("vendor credit must post the recovery journal; got %+v", res)
	}

	// The posting row exists, with the journal FK satisfied (the 0129 posted-has-ref CHECK).
	var status, path string
	var amount int64
	var accrues bool
	var journal string
	if err := pool.QueryRow(ctx,
		`SELECT status, money_path, amount_cents, accrues_recovery, COALESCE(gl_journal_id::text,'')
		   FROM warranty_settlement_postings WHERE claim_id = $1`, id).
		Scan(&status, &path, &amount, &accrues, &journal); err != nil {
		t.Fatalf("posting row: %v", err)
	}
	if status != PathPosted || path != PathVendorRecovery || amount != 3_400_00 || !accrues || journal == "" {
		t.Fatalf("posting row wrong: %s/%s/%d/%v/%s", status, path, amount, accrues, journal)
	}

	// The real journal carries the CONFIGURED account pair.
	rows, err := pool.Query(ctx,
		`SELECT a.code::text, COALESCE(l.debit_cents,0), COALESCE(l.credit_cents,0)
		   FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id
		  WHERE l.journal_id = $1 ORDER BY l.line_no`, journal)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var legs []string
	for rows.Next() {
		var code string
		var dr, cr int64
		if err := rows.Scan(&code, &dr, &cr); err != nil {
			t.Fatal(err)
		}
		if dr > 0 {
			legs = append(legs, "DR"+code)
		} else {
			legs = append(legs, "CR"+code)
		}
	}
	if strings.Join(legs, " ") != "DR1100 CR4900" {
		t.Errorf("the recovery journal must be DR1100 / CR4900 from the config; got %v", legs)
	}
}

func TestIntegrationW5B_ReplacementRecordsSeamUnwiredWithAReplayableRequest(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	svc := w5bSvc(pool, staffCaller(demoOwner), &now) // inventory seam deliberately unbound

	id := w5bClaim(t, svc, pool, ctx, ClaimInput{
		BranchID: trentonBranch, Description: "W5B replacement",
		Lines: []ClaimLineInput{{ProductText: "Blu 60", Qty: "20"}},
	})
	res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeReplacementStock, RealizedCents: 1_100_00})
	if err != nil {
		t.Fatal(err)
	}
	if res.PathStatus != PathSeamUnwired {
		t.Fatalf("an unbound inventory seam must record SEAM_UNWIRED; got %q", res.PathStatus)
	}
	var req string
	if err := pool.QueryRow(ctx,
		`SELECT request::text FROM warranty_settlement_postings WHERE claim_id = $1`, id).Scan(&req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req, "inventory.MoveReplacementCost") || !strings.Contains(req, "warranty-replace:") {
		t.Errorf("the preserved request must be replayable; got %s", req)
	}
	// And NO journal, warranty never posts cash for a replacement.
	var journals int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM warranty_settlement_postings WHERE claim_id = $1 AND gl_journal_id IS NOT NULL`, id).
		Scan(&journals); err != nil {
		t.Fatal(err)
	}
	if journals != 0 {
		t.Errorf("a replacement must post no journal; found %d", journals)
	}
}

// - 0130: the VIEW is where the subtraction lives -

func TestIntegrationW5B_RecoveryViewOutstandingIsAccruedMinusReceived(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	svc := w5bSvc(pool, staffCaller(demoOwner), &now)

	id := w5bClaim(t, svc, pool, ctx, ClaimInput{BranchID: trentonBranch, Description: "W5B recovery"})
	if _, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeVendorCredit, RealizedCents: 2_000_00}); err != nil {
		t.Fatal(err)
	}

	read := func() (accrued, received, outstanding, promised int64) {
		if err := pool.QueryRow(ctx,
			`SELECT accrued_cents, received_cents, outstanding_cents, promised_cents
			   FROM warranty_recovery_balances WHERE claim_id = $1`, id).
			Scan(&accrued, &received, &outstanding, &promised); err != nil {
			t.Fatalf("view read: %v", err)
		}
		return
	}
	if a, r, o, _ := read(); a != 2_000_00 || r != 0 || o != 2_000_00 {
		t.Fatalf("a fresh accrual is fully outstanding; got %d/%d/%d", a, r, o)
	}

	if _, err := svc.LogRecoveryAction(ctx, id, RecoveryActionInput{Action: ActionPromised, AmountCents: 2_000_00, PromisedDate: "2026-08-15"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.LogRecoveryAction(ctx, id, RecoveryActionInput{Action: ActionReceived, AmountCents: 750_00, Reference: "CN-771"}); err != nil {
		t.Fatal(err)
	}
	a, r, o, p := read()
	if a != 2_000_00 || r != 750_00 || o != 1_250_00 || p != 2_000_00 {
		t.Fatalf("outstanding must be accrued − received in SQL; got accrued=%d received=%d outstanding=%d promised=%d", a, r, o, p)
	}

	// The board (Go) and the view (SQL) must agree, that is the whole point of there being one definition.
	board, err := svc.RecoveryBoardView(ctx, trentonBranch, "", true)
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, v := range board.Vendors {
		for _, c := range v.Claims {
			if c.ClaimID == id {
				seen = true
				if c.OutstandingCents != 1_250_00 || c.ReceivedCents != 750_00 {
					t.Errorf("board row disagrees with the view: %+v", c)
				}
			}
		}
	}
	if !seen {
		t.Error("the accrued claim must appear on the board")
	}

	// Over-receipt is refused against the REAL balance.
	if _, err := svc.LogRecoveryAction(ctx, id, RecoveryActionInput{Action: ActionReceived, AmountCents: 1_250_01}); err == nil {
		t.Fatal("an over-receipt must be refused against the live balance")
	}
	// Exact remainder reconciles to zero.
	if _, err := svc.LogRecoveryAction(ctx, id, RecoveryActionInput{Action: ActionReceived, AmountCents: 1_250_00}); err != nil {
		t.Fatal(err)
	}
	if _, _, o, _ := read(); o != 0 {
		t.Errorf("a fully-received claim reconciles to zero; got %d", o)
	}
}

func TestIntegrationW5B_RecoveryActionAmountShapeCheckIsEnforcedBySQL(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	// The service refuses this first; the CHECK is the backstop, and a backstop nobody proves is a comment.
	var claimID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM warranty_claims LIMIT 1`).Scan(&claimID); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO warranty_recovery_actions (claim_id, action, amount_cents) VALUES ($1,'CONTACT_MADE',5000)`, claimID)
	if err == nil {
		// Clean up if the constraint ever regresses, so the failure does not poison other tests.
		_, _ = pool.Exec(ctx, `DELETE FROM warranty_recovery_actions WHERE claim_id = $1 AND action = 'CONTACT_MADE' AND amount_cents = 5000`, claimID)
		t.Fatal("a non-money action carrying an amount must be refused by the 0130 CHECK")
	}
}

// - 0131: the public write, for real -

func TestIntegrationW5B_PublicIntakeWritesExactlyOneClaimWithPhotoBytes(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	// No caller at all, the public path must not need one.
	svc := w5bSvc(pool, identity.StaffContext{}, &now)

	png := base64.StdEncoding.EncodeToString(pngBytes())
	res, err := svc.SubmitPublicIntake(ctx, PublicIntakeInput{
		Contact:     IntakeContact{Name: "Integration Homeowner", Email: "hi@example.com"},
		JobAddress:  Address{Line1: "1 Test Way", City: "Trenton", Province: "ON"},
		Description: "W5B integration public intake",
		Lines:       []PublicIntakeLine{{ProductText: "grey pavers", Qty: "60"}},
		Photos:      []PublicIntakePhoto{{ContentType: "image/png", DataBase64: png, Caption: "driveway"}},
	}, "203.0.113.201:5555")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM warranty_claims WHERE public_ref = $1`, res.Reference)
		_, _ = pool.Exec(context.Background(), `DELETE FROM warranty_intake_rate_limit WHERE bucket_key = $1`,
			rateBucketKey("203.0.113.201:5555"))
	})

	var claimID, phase, source, contact string
	if err := pool.QueryRow(ctx,
		`SELECT id::text, phase, source, intake_contact::text FROM warranty_claims WHERE public_ref = $1`,
		res.Reference).Scan(&claimID, &phase, &source, &contact); err != nil {
		t.Fatalf("the public claim must exist under its reference: %v", err)
	}
	if phase != PhaseIntake || source != SourceWeb {
		t.Errorf("a web claim lands INTAKE/WEB; got %s/%s", phase, source)
	}
	if !strings.Contains(contact, "Integration Homeowner") {
		t.Errorf("self-reported contact must ride the claim as JSONB; got %s", contact)
	}
	// It must NOT have created a customer: the public cannot write to the customer master.
	var custCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM customers WHERE name = 'Integration Homeowner'`).Scan(&custCount); err != nil {
		t.Fatal(err)
	}
	if custCount != 0 {
		t.Error("the public doorway must never create a customer record")
	}

	// The photo bytes round-trip, and the 0131 size-matches CHECK held.
	var size int
	var ct string
	var stored []byte
	if err := pool.QueryRow(ctx,
		`SELECT byte_size, content_type, image_bytes FROM warranty_intake_photos WHERE claim_id = $1`, claimID).
		Scan(&size, &ct, &stored); err != nil {
		t.Fatalf("photo row: %v", err)
	}
	if ct != "image/png" || size != len(pngBytes()) || len(stored) != size {
		t.Errorf("photo storage wrong: %s %d/%d", ct, size, len(stored))
	}
	// The CREATED event exists with a NULL actor, no fictional "system" user on a real audit trail.
	var actorNull bool
	if err := pool.QueryRow(ctx,
		`SELECT actor_id IS NULL FROM warranty_claim_events WHERE claim_id = $1 AND event_type = 'CREATED'`, claimID).
		Scan(&actorNull); err != nil {
		t.Fatal(err)
	}
	if !actorNull {
		t.Error("a public submission has no actor; inventing one would put a fictional name on an audit trail")
	}
}

func TestIntegrationW5B_IntakeRateLimiterCountsAtomically(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)
	bucket := rateBucketKey("198.51.100.250:1")
	window := time.Date(2026, 7, 14, 11, 0, 0, 0, time.UTC)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM warranty_intake_rate_limit WHERE bucket_key = $1`, bucket)
	})

	// Concurrent hits must produce a strictly increasing count with no duplicates, a read-then-write limiter
	// would hand out the same number twice and let the limit be exceeded.
	const n = 12
	seen := make(chan int, n)
	for i := 0; i < n; i++ {
		go func() {
			h, err := repo.IntakeRateLimitHit(ctx, bucket, window)
			if err != nil {
				seen <- -1
				return
			}
			seen <- h
		}()
	}
	counts := map[int]bool{}
	for i := 0; i < n; i++ {
		h := <-seen
		if h < 1 {
			t.Fatalf("rate-limit hit failed")
		}
		if counts[h] {
			t.Fatalf("the limiter handed out %d twice, it is not atomic", h)
		}
		counts[h] = true
	}
	if !counts[n] {
		t.Errorf("the final count must reach %d; got %v", n, counts)
	}
}

func TestIntegrationW5B_IntakePhotoCheckRefusesOversizeAtTheDatabase(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	var claimID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM warranty_claims LIMIT 1`).Scan(&claimID); err != nil {
		t.Fatal(err)
	}
	// The service caps at the door; this proves the backstop. A table that holds one row per claim forever must
	// not be able to hold a video.
	_, err := pool.Exec(ctx,
		`INSERT INTO warranty_intake_photos (claim_id, content_type, byte_size, image_bytes)
		 VALUES ($1,'image/png', 4194305, repeat('x', 4194305)::bytea)`, claimID)
	if err == nil {
		_, _ = pool.Exec(ctx, `DELETE FROM warranty_intake_photos WHERE claim_id = $1 AND byte_size = 4194305`, claimID)
		t.Fatal("the 0131 size CHECK must refuse an oversize photo")
	}
	// And a declared size that lies about the bytes is refused too.
	_, err = pool.Exec(ctx,
		`INSERT INTO warranty_intake_photos (claim_id, content_type, byte_size, image_bytes)
		 VALUES ($1,'image/png', 99, repeat('x', 10)::bytea)`, claimID)
	if err == nil {
		_, _ = pool.Exec(ctx, `DELETE FROM warranty_intake_photos WHERE claim_id = $1 AND byte_size = 99`, claimID)
		t.Fatal("the 0131 size-matches CHECK must refuse a declared size that disagrees with the bytes")
	}
}

// - 0132: phase clocks come off the audit spine -

func TestIntegrationW5B_PhaseClockDerivesFromTheAuditSpineAndSweepRaisesAnAlert(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	svc := w5bSvc(pool, staffCaller(demoOwner), &now)

	c, err := svc.CreateClaim(ctx, ClaimInput{BranchID: trentonBranch, Description: "W5B sla"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM warranty_claims WHERE id = $1`, c.ID) })
	for _, k := range []string{"contact_captured", "product_identified", "description_captured"} {
		if _, err := svc.TickChecklist(ctx, c.ID, ChecklistTickInput{ItemKey: k, Checked: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.AdvancePhase(ctx, c.ID, PhaseTransitionInput{ToPhase: PhaseTriage}); err != nil {
		t.Fatal(err)
	}

	clocks, err := NewRepository(pool).OpenClaimPhaseClocks(ctx, trentonBranch)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, cl := range clocks {
		if cl.ClaimID == c.ID {
			found = true
			if cl.Phase != PhaseTriage {
				t.Errorf("phase wrong: %s", cl.Phase)
			}
			// The TRIAGE clock starts at the TRANSITION, derived from the PHASE_CHANGE event.
			if !cl.PhaseSince.Equal(now) {
				t.Errorf("phase_since must be the PHASE_CHANGE time (%v); got %v", now, cl.PhaseSince)
			}
		}
	}
	if !found {
		t.Fatal("the open claim must appear in the phase clocks")
	}

	// 49 hours later, the fake clock moves, nothing sleeps, TRIAGE (48h) is breached.
	now = now.Add(49 * time.Hour)
	raised, err := svc.SweepSLA(ctx, trentonBranch)
	if err != nil {
		t.Fatal(err)
	}
	var mine *SLAAlert
	for i := range raised {
		if raised[i].ClaimID == c.ID {
			mine = &raised[i]
		}
	}
	if mine == nil {
		t.Fatalf("the breached claim must raise an alert; raised %d", len(raised))
	}
	if mine.OverdueHours != 1 || mine.NotifyStatus != NotifySuppressed {
		t.Errorf("alert wrong: %+v", *mine)
	}

	// The (claim, phase) UNIQUE makes the sweep idempotent in SQL, not just in Go.
	if _, err := svc.SweepSLA(ctx, trentonBranch); err != nil {
		t.Fatal(err)
	}
	var alerts int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM warranty_sla_alerts WHERE claim_id = $1`, c.ID).Scan(&alerts); err != nil {
		t.Fatal(err)
	}
	if alerts != 1 {
		t.Errorf("a repeat sweep must not duplicate the alert; found %d", alerts)
	}
}

func TestIntegrationW5B_SLADefaultsAreSeededAndPreserveP1(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	targets, err := NewRepository(pool).SLATargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		PhaseIntake: 24, PhaseTriage: 48, PhaseSubmitted: 72,
		PhaseWaitingOnDecision: defaultSLADays * 24, PhaseDecision: 48, PhaseSettlement: 120,
	}
	for phase, hours := range want {
		got, ok := ResolveSLATarget(targets, phase, "")
		if !ok || got != hours {
			t.Errorf("%s: want %dh got %dh (ok=%v)", phase, hours, got, ok)
		}
	}
}

func TestIntegrationW5B_OutcomePathsAreConfiguredForAllFourOutcomes(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)
	want := map[string]struct{ path, seam string }{
		OutcomeVendorCredit:          {PathVendorRecovery, SeamGL},
		OutcomeCustomerAccommodation: {PathCustomerCredit, SeamBilling},
		OutcomeReplacementStock:      {PathInventoryReplacement, SeamInventory},
		OutcomeWriteOff:              {PathExpenseWriteOff, SeamGL},
	}
	for outcome, w := range want {
		got, err := repo.OutcomePath(ctx, outcome)
		if err != nil {
			t.Fatalf("%s: %v", outcome, err)
		}
		if got.MoneyPath != w.path || got.Seam != w.seam {
			t.Errorf("%s: want %s/%s got %s/%s", outcome, w.path, w.seam, got.MoneyPath, got.Seam)
		}
	}
	// An outcome with no row is refused, never defaulted.
	if _, err := repo.OutcomePath(ctx, "NOT_AN_OUTCOME"); err == nil {
		t.Fatal("an unconfigured outcome must be refused, not defaulted")
	}
}
