//go:build integration

// attempts_integration_test.go: the card charge attempt store against a real Postgres (migration
// 0361). The legs pin the claim (one winner per leg key, the till guard), every guarded transition
// over the whole status matrix, the bind riding the caller's transaction, the bind against the
// compensator, resolve, the CHECK constraints and the diagnostics sanitiser at the store boundary.
//
// The store under test runs on a pool of attemptsPoolSize connections, the pool default, and every
// fixture or probe uses a SEPARATE pool so a helper never eats one of the scenario connections.
// No port or database is pinned here: DATABASE_URL comes from the command, and the legs skip without it.
package payment

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const attemptsPoolSize = 4

func attemptsPools(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL is unset, the attempt store legs need a Postgres")
	}
	mk := func(size int) *pgxpool.Pool {
		cfg, err := pgxpool.ParseConfig(url)
		if err != nil {
			t.Fatalf("parse DATABASE_URL: %v", err)
		}
		cfg.MaxConns = int32(size)
		p, err := pgxpool.NewWithConfig(context.Background(), cfg)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		if err := p.Ping(context.Background()); err != nil {
			p.Close()
			t.Skipf("no integration DB: %v", err)
		}
		t.Cleanup(p.Close)
		return p
	}
	return NewStore(mk(attemptsPoolSize), nil), mk(attemptsPoolSize)
}

// attemptFx is one till session and the rows the leg plants on it, all removed at cleanup.
type attemptFx struct {
	t                     *testing.T
	store                 *Store
	helper                *pgxpool.Pool
	branch, user, session string
	payments              []string
}

func newAttemptFx(t *testing.T) *attemptFx {
	t.Helper()
	store, helper := attemptsPools(t)
	ctx := context.Background()
	fx := &attemptFx{t: t, store: store, helper: helper}
	if err := helper.QueryRow(ctx, `SELECT id::text FROM locations ORDER BY id LIMIT 1`).Scan(&fx.branch); err != nil {
		t.Skipf("no seeded location: %v", err)
	}
	if err := helper.QueryRow(ctx, `SELECT id::text FROM users ORDER BY id LIMIT 1`).Scan(&fx.user); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	fx.session = fx.newSession()
	t.Cleanup(func() {
		c := context.Background()
		_, _ = helper.Exec(c, `DELETE FROM card_charge_attempts WHERE till_session_id = ANY($1::uuid[])`, []string{fx.session})
		for _, p := range fx.payments {
			_, _ = helper.Exec(c, `DELETE FROM payments WHERE id = $1::uuid`, p)
		}
		_, _ = helper.Exec(c, `DELETE FROM till_sessions WHERE id = $1::uuid`, fx.session)
	})
	return fx
}

func (fx *attemptFx) newSession() string {
	fx.t.Helper()
	var id string
	err := fx.helper.QueryRow(context.Background(), `INSERT INTO till_sessions (branch_id, station, status, opened_by)
	    VALUES ($1::uuid, $2, 'OPEN', $3::uuid) RETURNING id::text`, fx.branch, "a1-"+uuid.NewString(), fx.user).Scan(&id)
	if err != nil {
		fx.t.Fatalf("till session fixture: %v", err)
	}
	return id
}

func (fx *attemptFx) newPayment() string {
	fx.t.Helper()
	var id string
	err := fx.helper.QueryRow(context.Background(), `INSERT INTO payments (payment_no, method, amount_cents, branch_id, till_session_id, gateway_ref)
	    VALUES ($1, 'CARD', 1000, $2::uuid, $3::uuid, 'gw-fixture') RETURNING id::text`,
		"A1-"+uuid.NewString(), fx.branch, fx.session).Scan(&id)
	if err != nil {
		fx.t.Fatalf("payment fixture: %v", err)
	}
	fx.payments = append(fx.payments, id)
	return id
}

func (fx *attemptFx) in(amount int64) ClaimInput {
	return ClaimInput{LegKey: uuid.NewString(), SaleKey: uuid.NewString(), LegSeq: 1, AmountCents: amount,
		BranchID: fx.branch, TillSessionID: fx.session, CardType: "CREDIT", Processor: "run_payments", CreatedBy: fx.user}
}

func (fx *attemptFx) claim(in ClaimInput) Attempt {
	fx.t.Helper()
	a, created, err := fx.store.Claim(context.Background(), in)
	if err != nil || !created {
		fx.t.Fatalf("claim %s: created=%v err=%v", in.LegKey, created, err)
	}
	return a
}

// force plants a row in a status, with the columns that status's CHECKs demand, bypassing the store.
func (fx *attemptFx) force(id, status string) {
	fx.t.Helper()
	ref, kind, rref, resolution, note := "", "", "", "", ""
	var payment, resolver any
	switch status {
	case AttemptCaptured, AttemptVoiding, AttemptVoidUnknown, AttemptVoidDeclined:
		ref = "ref-" + id
	case AttemptBound:
		ref, payment = "ref-"+id, fx.newPayment()
	case AttemptVoided:
		ref, kind, rref = "ref-"+id, ReversalVoid, "void-"+id
	case AttemptResolved:
		resolution, note, resolver = ResolutionNotCharged, "forced by the leg", fx.user
	}
	_, err := fx.helper.Exec(context.Background(), `UPDATE card_charge_attempts
	    SET status = $2, processor_ref = $3, reversal_kind = $4, reversal_ref = $5, payment_id = $6::uuid,
	        resolution = $7, resolution_note = $8, resolved_by = $9::uuid WHERE id = $1::uuid`,
		id, status, ref, kind, rref, payment, resolution, note, resolver)
	if err != nil {
		fx.t.Fatalf("force %s: %v", status, err)
	}
}

func (fx *attemptFx) get(legKey string) Attempt {
	fx.t.Helper()
	a, ok, err := fx.store.GetByLegKey(context.Background(), legKey)
	if err != nil || !ok {
		fx.t.Fatalf("GetByLegKey %s: ok=%v err=%v", legKey, ok, err)
	}
	return a
}

var allAttemptStatuses = []string{AttemptCharging, AttemptCaptured, AttemptDeclined, AttemptUnknown, AttemptBound,
	AttemptVoiding, AttemptVoided, AttemptVoidUnknown, AttemptVoidDeclined, AttemptResolved}

func TestAttempts_ClaimInsertsOnceAndReturnsTheExistingRow(t *testing.T) {
	fx := newAttemptFx(t)
	ctx := context.Background()
	in := fx.in(4200)
	in.LegSeq = 2
	first, created, err := fx.store.Claim(ctx, in)
	if err != nil || !created {
		t.Fatalf("first claim: created=%v err=%v", created, err)
	}
	if first.Status != AttemptCharging || first.LegKey != in.LegKey || first.SaleKey != in.SaleKey ||
		first.AmountCents != 4200 || first.LegSeq != 2 || first.BranchID != fx.branch || first.TillSessionID != fx.session ||
		first.CardType != "CREDIT" || first.Processor != "run_payments" || first.CreatedBy != fx.user || first.ID == "" {
		t.Fatalf("claimed row = %+v", first)
	}
	again, created, err := fx.store.Claim(ctx, in)
	if err != nil || created {
		t.Fatalf("second claim of the same leg key: created=%v err=%v", created, err)
	}
	if again.ID != first.ID {
		t.Fatalf("second claim returned %s, want the existing %s", again.ID, first.ID)
	}
	var n int
	if err := fx.helper.QueryRow(ctx, `SELECT count(*) FROM card_charge_attempts WHERE leg_key = $1`, in.LegKey).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows for the leg key = %d (err=%v), want 1", n, err)
	}
	if _, ok, err := fx.store.GetByLegKey(ctx, uuid.NewString()); err != nil || ok {
		t.Fatalf("GetByLegKey of an unknown key: ok=%v err=%v", ok, err)
	}
}

func TestAttempts_ClaimOfAnExistingLegIsNotRefusedByTheGuard(t *testing.T) {
	fx := newAttemptFx(t)
	a := fx.claim(fx.in(900))
	// The retry of a leg whose own row is unresolved gets its row back, never the guard's refusal.
	in := ClaimInput{LegKey: a.LegKey, SaleKey: a.SaleKey, LegSeq: 1, AmountCents: 900, BranchID: fx.branch, TillSessionID: fx.session}
	got, created, err := fx.store.Claim(context.Background(), in)
	if err != nil || created || got.ID != a.ID {
		t.Fatalf("retry claim: id=%s created=%v err=%v, want %s existing", got.ID, created, err, a.ID)
	}
}

// Contenders on one leg key at pool size: exactly one winner, everybody answers within the deadline.
// A claim that held a connection while asking for a second would stall here (decision log row 786).
func TestAttempts_ConcurrentClaimsOfOneLegKeyHaveOneWinner(t *testing.T) {
	fx := newAttemptFx(t)
	in := fx.in(1500)
	const contenders = attemptsPoolSize * 2
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	start := make(chan struct{})
	type res struct {
		id      string
		created bool
		err     error
	}
	out := make([]res, contenders)
	for i := range out {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			a, created, err := fx.store.Claim(ctx, in)
			out[i] = res{a.ID, created, err}
		}(i)
	}
	close(start)
	wg.Wait()
	winners, ids := 0, map[string]bool{}
	for i, r := range out {
		if r.err != nil {
			t.Fatalf("contender %d: %v (a stall shows as the deadline)", i, r.err)
		}
		if r.created {
			winners++
		}
		ids[r.id] = true
	}
	if winners != 1 || len(ids) != 1 {
		t.Fatalf("winners=%d distinct ids=%d, want 1 and 1", winners, len(ids))
	}
}

// The guard is race-free: contenders on different sale keys for one till and amount, one gets in.
func TestAttempts_ConcurrentClaimsOnOneTillAndAmountHaveOneWinner(t *testing.T) {
	fx := newAttemptFx(t)
	const contenders = attemptsPoolSize * 2
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, contenders)
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		in := fx.in(7700)
		go func(i int) {
			defer wg.Done()
			<-start
			_, _, errs[i] = fx.store.Claim(ctx, in)
		}(i)
	}
	close(start)
	wg.Wait()
	won, refused := 0, 0
	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrTillUnresolved):
			refused++
		default:
			t.Fatalf("contender %d: %v", i, err)
		}
	}
	if won != 1 || refused != contenders-1 {
		t.Fatalf("won=%d refused=%d, want 1 and %d", won, refused, contenders-1)
	}
	var n int
	if err := fx.helper.QueryRow(context.Background(), `SELECT count(*) FROM card_charge_attempts
	    WHERE till_session_id = $1::uuid AND amount_cents = 7700`, fx.session).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows = %d (err=%v), want 1", n, err)
	}
}

func TestAttempts_TillGuard(t *testing.T) {
	ctx := context.Background()
	unresolved := map[string]bool{AttemptCharging: true, AttemptCaptured: true, AttemptUnknown: true,
		AttemptVoiding: true, AttemptVoidUnknown: true, AttemptVoidDeclined: true}

	for _, status := range allAttemptStatuses {
		t.Run("holder_"+status, func(t *testing.T) {
			fx := newAttemptFx(t)
			holder := fx.claim(fx.in(5000))
			fx.force(holder.ID, status)
			_, created, err := fx.store.Claim(ctx, fx.in(5000))
			if unresolved[status] {
				if !errors.Is(err, ErrTillUnresolved) || created {
					t.Fatalf("holder %s: created=%v err=%v, want ErrTillUnresolved", status, created, err)
				}
				return
			}
			if err != nil || !created {
				t.Fatalf("holder %s: created=%v err=%v, want a new claim", status, created, err)
			}
		})
	}

	t.Run("same_sale_key_other_leg_is_ignored", func(t *testing.T) {
		fx := newAttemptFx(t)
		leg1 := fx.claim(fx.in(5000))
		leg2 := fx.in(5000)
		leg2.SaleKey, leg2.LegSeq = leg1.SaleKey, 2
		if _, created, err := fx.store.Claim(ctx, leg2); err != nil || !created {
			t.Fatalf("leg 2 of the same sale: created=%v err=%v", created, err)
		}
	})
	t.Run("same_sale_key_past_an_answer_blocks_a_new_shape", func(t *testing.T) {
		// The round 1 review's re-shaped ring: a single tender's leg key IS the sale key, it loses
		// its answer (or sits mid reversal), the cashier re-shapes the ring (a split, or reordered
		// legs), the client mints a new leg key under the SAME sale key, and that new key must not
		// reach the processor over the open outcome.
		for _, status := range []string{AttemptUnknown, AttemptVoiding, AttemptVoidUnknown, AttemptVoidDeclined} {
			t.Run(status, func(t *testing.T) {
				fx := newAttemptFx(t)
				ctx := context.Background()
				first := fx.claim(fx.in(5000))
				driveViaStore(t, fx, first, status)
				reshaped := fx.in(5000)
				reshaped.SaleKey = first.SaleKey
				_, created, err := fx.store.Claim(ctx, reshaped)
				if !errors.Is(err, ErrTillUnresolved) || created {
					t.Fatalf("re-shaped ring over a same-sale %s row: created=%v err=%v, want ErrTillUnresolved", status, created, err)
				}
			})
		}
	})
	t.Run("same_sale_key_in_flight_sibling_still_claims", func(t *testing.T) {
		// A live sibling leg is only ever CHARGING or CAPTURED (a ring stops at the first leg that
		// ends anything else), and a split's next leg must claim over it.
		for _, status := range []string{AttemptCharging, AttemptCaptured} {
			t.Run(status, func(t *testing.T) {
				fx := newAttemptFx(t)
				ctx := context.Background()
				first := fx.claim(fx.in(5000))
				driveViaStore(t, fx, first, status)
				sibling := fx.in(5000)
				sibling.SaleKey, sibling.LegSeq = first.SaleKey, 2
				if _, created, err := fx.store.Claim(ctx, sibling); err != nil || !created {
					t.Fatalf("sibling leg over a same-sale %s row: created=%v err=%v", status, created, err)
				}
			})
		}
	})
	t.Run("different_amount_is_allowed", func(t *testing.T) {
		fx := newAttemptFx(t)
		fx.claim(fx.in(5000))
		if _, created, err := fx.store.Claim(ctx, fx.in(5001)); err != nil || !created {
			t.Fatalf("different amount: created=%v err=%v", created, err)
		}
	})
	t.Run("different_till_session_is_allowed", func(t *testing.T) {
		fx := newAttemptFx(t)
		fx.claim(fx.in(5000))
		other := fx.in(5000)
		other.TillSessionID = fx.newSession()
		t.Cleanup(func() {
			_, _ = fx.helper.Exec(ctx, `DELETE FROM card_charge_attempts WHERE till_session_id = $1::uuid`, other.TillSessionID)
			_, _ = fx.helper.Exec(ctx, `DELETE FROM till_sessions WHERE id = $1::uuid`, other.TillSessionID)
		})
		if _, created, err := fx.store.Claim(ctx, other); err != nil || !created {
			t.Fatalf("different session: created=%v err=%v", created, err)
		}
	})
}

// driveViaStore moves a row to a status through the store's own guarded transitions, so the guard
// legs see exactly what production writes and nothing a forced UPDATE could fake.
func driveViaStore(t *testing.T, fx *attemptFx, a Attempt, status string) {
	t.Helper()
	ctx := context.Background()
	switch status {
	case AttemptCharging:
		return
	case AttemptUnknown:
		if _, ok, err := fx.store.StampUnknown(ctx, a.ID, "no usable answer"); err != nil || !ok {
			t.Fatalf("StampUnknown: ok=%v err=%v", ok, err)
		}
	case AttemptCaptured, AttemptVoiding, AttemptVoidUnknown, AttemptVoidDeclined:
		if _, ok, err := fx.store.StampCaptured(ctx, a.ID, "trans-"+a.ID, "A"); err != nil || !ok {
			t.Fatalf("StampCaptured: ok=%v err=%v", ok, err)
		}
		if status == AttemptCaptured {
			return
		}
		if _, ok, err := fx.store.BeginVoiding(ctx, a.ID); err != nil || !ok {
			t.Fatalf("BeginVoiding: ok=%v err=%v", ok, err)
		}
		switch status {
		case AttemptVoidUnknown:
			if _, ok, err := fx.store.StampVoidUnknown(ctx, a.ID, "answer lost"); err != nil || !ok {
				t.Fatalf("StampVoidUnknown: ok=%v err=%v", ok, err)
			}
		case AttemptVoidDeclined:
			if _, ok, err := fx.store.StampVoidDeclined(ctx, a.ID, "reversal refused"); err != nil || !ok {
				t.Fatalf("StampVoidDeclined: ok=%v err=%v", ok, err)
			}
		}
	default:
		t.Fatalf("driveViaStore cannot reach %s", status)
	}
}

type transitionCase struct {
	name string
	from []string
	want string
	call func(s *Store, fx *attemptFx, id string) (Attempt, bool, error)
}

func transitionCases() []transitionCase {
	ctx := context.Background()
	return []transitionCase{
		{"StampCaptured", []string{AttemptCharging}, AttemptCaptured, func(s *Store, _ *attemptFx, id string) (Attempt, bool, error) {
			return s.StampCaptured(ctx, id, "trans-1", "A")
		}},
		{"StampDeclined", []string{AttemptCharging}, AttemptDeclined, func(s *Store, _ *attemptFx, id string) (Attempt, bool, error) {
			return s.StampDeclined(ctx, id, "C", "declined")
		}},
		{"StampUnknown", []string{AttemptCharging}, AttemptUnknown, func(s *Store, _ *attemptFx, id string) (Attempt, bool, error) {
			return s.StampUnknown(ctx, id, "timeout")
		}},
		{"BeginVoiding", []string{AttemptCaptured}, AttemptVoiding, func(s *Store, _ *attemptFx, id string) (Attempt, bool, error) {
			return s.BeginVoiding(ctx, id)
		}},
		{"StampVoided", []string{AttemptVoiding}, AttemptVoided, func(s *Store, _ *attemptFx, id string) (Attempt, bool, error) {
			return s.StampVoided(ctx, id, ReversalVoid, "void-1")
		}},
		{"StampVoidUnknown", []string{AttemptVoiding}, AttemptVoidUnknown, func(s *Store, _ *attemptFx, id string) (Attempt, bool, error) {
			return s.StampVoidUnknown(ctx, id, "timeout")
		}},
		{"StampVoidDeclined", []string{AttemptVoiding}, AttemptVoidDeclined, func(s *Store, _ *attemptFx, id string) (Attempt, bool, error) {
			return s.StampVoidDeclined(ctx, id, "declined")
		}},
		{"BindTx", []string{AttemptCaptured}, AttemptBound, func(s *Store, fx *attemptFx, id string) (Attempt, bool, error) {
			pay := fx.newPayment()
			tx, err := s.pool.Begin(ctx)
			if err != nil {
				return Attempt{}, false, err
			}
			a, ok, err := s.BindTx(ctx, tx, id, pay)
			if err != nil {
				_ = tx.Rollback(ctx)
				return a, ok, err
			}
			return a, ok, tx.Commit(ctx)
		}},
		{"Resolve", []string{AttemptUnknown, AttemptVoidUnknown, AttemptVoidDeclined}, AttemptResolved, func(s *Store, fx *attemptFx, id string) (Attempt, bool, error) {
			return s.Resolve(ctx, id, ResolutionNotCharged, "checked the settlement report", fx.user)
		}},
	}
}

// Every guarded transition succeeds from its expected status and moves no row from any other.
func TestAttempts_TransitionMatrix(t *testing.T) {
	for _, tc := range transitionCases() {
		for _, from := range allAttemptStatuses {
			t.Run(tc.name+"_from_"+from, func(t *testing.T) {
				fx := newAttemptFx(t)
				a := fx.claim(fx.in(300))
				fx.force(a.ID, from)
				before := fx.get(a.LegKey)
				got, ok, err := tc.call(fx.store, fx, a.ID)
				if err != nil {
					t.Fatalf("%s from %s: %v", tc.name, from, err)
				}
				expected := false
				for _, f := range tc.from {
					expected = expected || f == from
				}
				after := fx.get(a.LegKey)
				if ok != expected {
					t.Fatalf("%s from %s: ok=%v, want %v", tc.name, from, ok, expected)
				}
				if expected {
					if got.Status != tc.want || after.Status != tc.want {
						t.Fatalf("%s from %s: returned %s, row %s, want %s", tc.name, from, got.Status, after.Status, tc.want)
					}
					return
				}
				if after.Status != from || after.UpdatedAt != before.UpdatedAt {
					t.Fatalf("%s from %s moved the row: %+v", tc.name, from, after)
				}
			})
		}
	}
}

func TestAttempts_TransitionsRecordWhatTheyCarry(t *testing.T) {
	fx := newAttemptFx(t)
	ctx := context.Background()
	a := fx.claim(fx.in(2100))
	if _, ok, err := fx.store.StampCaptured(ctx, a.ID, "trans-77", "A"); err != nil || !ok {
		t.Fatalf("StampCaptured: ok=%v err=%v", ok, err)
	}
	if got := fx.get(a.LegKey); got.ProcessorRef != "trans-77" || got.RespCode != "A" {
		t.Fatalf("captured row = %+v", got)
	}
	if _, ok, err := fx.store.BeginVoiding(ctx, a.ID); err != nil || !ok {
		t.Fatalf("BeginVoiding: ok=%v err=%v", ok, err)
	}
	if _, ok, err := fx.store.StampVoided(ctx, a.ID, ReversalRefund, "refund-9"); err != nil || !ok {
		t.Fatalf("StampVoided: ok=%v err=%v", ok, err)
	}
	if got := fx.get(a.LegKey); got.ReversalKind != ReversalRefund || got.ReversalRef != "refund-9" || got.Status != AttemptVoided {
		t.Fatalf("voided row = %+v", got)
	}

	b := fx.claim(fx.in(2200))
	if _, ok, err := fx.store.StampDeclined(ctx, b.ID, "C", "declined by the bank"); err != nil || !ok {
		t.Fatalf("StampDeclined: ok=%v err=%v", ok, err)
	}
	if got := fx.get(b.LegKey); got.RespCode != "C" || got.LastError != "declined by the bank" {
		t.Fatalf("declined row = %+v", got)
	}

	if _, _, err := fx.store.StampVoided(ctx, a.ID, "SHRUG", "x"); err == nil {
		t.Fatal("an unknown reversal kind must be refused")
	}
}

// Diagnostics never hold a token or a PAN: the store sanitises on every write of last_error.
func TestAttempts_LastErrorIsSanitisedOnWrite(t *testing.T) {
	fx := newAttemptFx(t)
	ctx := context.Background()
	a := fx.claim(fx.in(3300))
	if _, ok, err := fx.store.StampUnknown(ctx, a.ID, `post failed token=tok_live_abcdef card 4111 1111 1111 1111`); err != nil || !ok {
		t.Fatalf("StampUnknown: ok=%v err=%v", ok, err)
	}
	got := fx.get(a.LegKey).LastError
	for _, bad := range []string{"tok_live_abcdef", "4111"} {
		if strings.Contains(got, bad) {
			t.Fatalf("last_error = %q, still holds %q", got, bad)
		}
	}
}

// A diagnostic carrying bytes that are not UTF-8 must stamp, not strand: Postgres refuses the write
// otherwise (SQLSTATE 22021) and the row would sit in its old status until the sweep moves it, so a
// plain decline pages a manager. The sanitiser replaces the bad bytes and every stamp lands.
func TestAttempts_NonUTF8DiagnosticStillStamps(t *testing.T) {
	fx := newAttemptFx(t)
	ctx := context.Background()
	bad := "declined \xff\xfe body"

	d := fx.claim(fx.in(3400))
	if _, ok, err := fx.store.StampDeclined(ctx, d.ID, "C", bad); err != nil || !ok {
		t.Fatalf("StampDeclined: ok=%v err=%v", ok, err)
	}
	if got := fx.get(d.LegKey); got.Status != AttemptDeclined || !utf8.ValidString(got.LastError) {
		t.Fatalf("declined row = %+v", got)
	}

	u := fx.claim(fx.in(3500))
	if _, ok, err := fx.store.StampUnknown(ctx, u.ID, bad); err != nil || !ok {
		t.Fatalf("StampUnknown: ok=%v err=%v", ok, err)
	}
	if got := fx.get(u.LegKey); got.Status != AttemptUnknown || !utf8.ValidString(got.LastError) {
		t.Fatalf("unknown row = %+v", got)
	}

	vu := capturedAttempt(t, fx, 3600)
	if _, ok, err := fx.store.BeginVoiding(ctx, vu.ID); err != nil || !ok {
		t.Fatalf("BeginVoiding: ok=%v err=%v", ok, err)
	}
	if _, ok, err := fx.store.StampVoidUnknown(ctx, vu.ID, bad); err != nil || !ok {
		t.Fatalf("StampVoidUnknown: ok=%v err=%v", ok, err)
	}
	if got := fx.get(vu.LegKey); got.Status != AttemptVoidUnknown || !utf8.ValidString(got.LastError) {
		t.Fatalf("void unknown row = %+v", got)
	}

	vd := capturedAttempt(t, fx, 3700)
	if _, ok, err := fx.store.BeginVoiding(ctx, vd.ID); err != nil || !ok {
		t.Fatalf("BeginVoiding: ok=%v err=%v", ok, err)
	}
	if _, ok, err := fx.store.StampVoidDeclined(ctx, vd.ID, bad); err != nil || !ok {
		t.Fatalf("StampVoidDeclined: ok=%v err=%v", ok, err)
	}
	if got := fx.get(vd.LegKey); got.Status != AttemptVoidDeclined || !utf8.ValidString(got.LastError) {
		t.Fatalf("void declined row = %+v", got)
	}
}

// A bind on a transaction that rolls back leaves the attempt CAPTURED, so the compensator still owns it.
func TestAttempts_BindTxRollbackLeavesTheRowCaptured(t *testing.T) {
	fx := newAttemptFx(t)
	ctx := context.Background()
	a := fx.claim(fx.in(1100))
	if _, ok, err := fx.store.StampCaptured(ctx, a.ID, "trans-r", "A"); err != nil || !ok {
		t.Fatalf("StampCaptured: ok=%v err=%v", ok, err)
	}
	tx, err := fx.store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := fx.store.BindTx(ctx, tx, a.ID, fx.newPayment()); err != nil || !ok {
		t.Fatalf("BindTx: ok=%v err=%v", ok, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	got := fx.get(a.LegKey)
	if got.Status != AttemptCaptured || got.PaymentID != "" {
		t.Fatalf("after the rollback the row is %s payment=%q, want CAPTURED with no payment", got.Status, got.PaymentID)
	}
}

func TestAttempts_BindTxNeedsATransactionAndAPayment(t *testing.T) {
	fx := newAttemptFx(t)
	ctx := context.Background()
	a := fx.claim(fx.in(1200))
	if _, _, err := fx.store.BindTx(ctx, nil, a.ID, fx.newPayment()); err == nil {
		t.Fatal("BindTx with no transaction must be refused: a bind outside the sale's transaction defeats the point")
	}
	tx, err := fx.store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, _, err := fx.store.BindTx(ctx, tx, a.ID, ""); err == nil {
		t.Fatal("BindTx with no payment id must be refused")
	}
}

func waitForLockWaiters(t *testing.T, helper *pgxpool.Pool, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := helper.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity
		    WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never saw %d sessions waiting on a lock", want)
}

func capturedAttempt(t *testing.T, fx *attemptFx, amount int64) Attempt {
	t.Helper()
	a := fx.claim(fx.in(amount))
	if _, ok, err := fx.store.StampCaptured(context.Background(), a.ID, "trans-"+a.ID, "A"); err != nil || !ok {
		t.Fatalf("StampCaptured: ok=%v err=%v", ok, err)
	}
	return a
}

// The sale binds first (uncommitted); the compensator's guarded move waits on the row, then finds
// it BOUND and moves nothing.
func TestAttempts_BindFirstThenVoidingLoses(t *testing.T) {
	fx := newAttemptFx(t)
	ctx := context.Background()
	a := capturedAttempt(t, fx, 410)
	tx, err := fx.store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, ok, err := fx.store.BindTx(ctx, tx, a.ID, fx.newPayment()); err != nil || !ok {
		t.Fatalf("BindTx: ok=%v err=%v", ok, err)
	}
	type res struct {
		ok  bool
		err error
	}
	done := make(chan res, 1)
	go func() {
		_, ok, err := fx.store.BeginVoiding(ctx, a.ID)
		done <- res{ok, err}
	}()
	waitForLockWaiters(t, fx.helper, 1)
	select {
	case r := <-done:
		t.Fatalf("BeginVoiding returned (%+v) while the bind was uncommitted", r)
	default:
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil || r.ok {
		t.Fatalf("BeginVoiding after the bind: ok=%v err=%v, want no row moved", r.ok, r.err)
	}
	if got := fx.get(a.LegKey); got.Status != AttemptBound {
		t.Fatalf("status = %s, want BOUND", got.Status)
	}
}

// The compensator moves first; the sale's bind then moves nothing, so the sale can roll back.
func TestAttempts_VoidingFirstThenBindLoses(t *testing.T) {
	fx := newAttemptFx(t)
	ctx := context.Background()
	a := capturedAttempt(t, fx, 420)
	if _, ok, err := fx.store.BeginVoiding(ctx, a.ID); err != nil || !ok {
		t.Fatalf("BeginVoiding: ok=%v err=%v", ok, err)
	}
	tx, err := fx.store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, ok, err := fx.store.BindTx(ctx, tx, a.ID, fx.newPayment()); err != nil || ok {
		t.Fatalf("BindTx after voiding: ok=%v err=%v, want no row moved", ok, err)
	}
	if got := fx.get(a.LegKey); got.Status != AttemptVoiding {
		t.Fatalf("status = %s, want VOIDING", got.Status)
	}
}

// Both contenders blocked on a held FOR UPDATE and released together: whichever order Postgres
// grants, exactly one wins and the row agrees with the winner.
func TestAttempts_BindAndVoidingRaceHasOneWinner(t *testing.T) {
	for i := 0; i < 15; i++ {
		fx := newAttemptFx(t)
		ctx := context.Background()
		a := capturedAttempt(t, fx, 430)
		hold, err := fx.helper.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var locked string
		if err := hold.QueryRow(ctx, `SELECT id::text FROM card_charge_attempts WHERE id = $1::uuid FOR UPDATE`, a.ID).Scan(&locked); err != nil {
			t.Fatal(err)
		}
		pay := fx.newPayment()
		type res struct {
			ok  bool
			err error
		}
		bind, void := make(chan res, 1), make(chan res, 1)
		go func() {
			tx, err := fx.store.pool.Begin(ctx)
			if err != nil {
				bind <- res{false, err}
				return
			}
			_, ok, err := fx.store.BindTx(ctx, tx, a.ID, pay)
			if err != nil || !ok {
				_ = tx.Rollback(ctx)
				bind <- res{ok, err}
				return
			}
			bind <- res{ok, tx.Commit(ctx)}
		}()
		go func() {
			_, ok, err := fx.store.BeginVoiding(ctx, a.ID)
			void <- res{ok, err}
		}()
		waitForLockWaiters(t, fx.helper, 2)
		if err := hold.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		b, v := <-bind, <-void
		if b.err != nil || v.err != nil {
			t.Fatalf("round %d: bind err=%v void err=%v", i, b.err, v.err)
		}
		if b.ok == v.ok {
			t.Fatalf("round %d: bind ok=%v void ok=%v, want exactly one winner", i, b.ok, v.ok)
		}
		want := AttemptVoiding
		if b.ok {
			want = AttemptBound
		}
		if got := fx.get(a.LegKey); got.Status != want {
			t.Fatalf("round %d: status = %s, want %s", i, got.Status, want)
		}
	}
}

func TestAttempts_ResolveNeedsNoteResolverAndAKnownResolution(t *testing.T) {
	fx := newAttemptFx(t)
	ctx := context.Background()
	a := fx.claim(fx.in(640))
	fx.force(a.ID, AttemptUnknown)
	for name, call := range map[string]func() error{
		"no note": func() error { _, _, err := fx.store.Resolve(ctx, a.ID, ResolutionNotCharged, "", fx.user); return err },
		"blank note": func() error {
			_, _, err := fx.store.Resolve(ctx, a.ID, ResolutionNotCharged, "  \t", fx.user)
			return err
		},
		"no resolver": func() error {
			_, _, err := fx.store.Resolve(ctx, a.ID, ResolutionNotCharged, "checked", "")
			return err
		},
		"unknown outcome":  func() error { _, _, err := fx.store.Resolve(ctx, a.ID, "SHRUG", "checked", fx.user); return err },
		"empty resolution": func() error { _, _, err := fx.store.Resolve(ctx, a.ID, "", "checked", fx.user); return err },
	} {
		if err := call(); !errors.Is(err, ErrResolveInvalid) {
			t.Errorf("%s: err = %v, want ErrResolveInvalid", name, err)
		}
	}
	if got := fx.get(a.LegKey); got.Status != AttemptUnknown {
		t.Fatalf("a refused resolve moved the row to %s", got.Status)
	}
	got, ok, err := fx.store.Resolve(ctx, a.ID, ResolutionReversedAtProcessor, "reversed in the Run portal", fx.user)
	if err != nil || !ok {
		t.Fatalf("Resolve: ok=%v err=%v", ok, err)
	}
	if got.Status != AttemptResolved || got.Resolution != ResolutionReversedAtProcessor ||
		got.ResolutionNote != "reversed in the Run portal" || got.ResolvedBy != fx.user {
		t.Fatalf("resolved row = %+v", got)
	}
}

func TestAttempts_ListOpen(t *testing.T) {
	fx := newAttemptFx(t)
	ctx := context.Background()
	// Distinct amounts keep the till guard out of the way; every claim is made before any status is
	// forced.
	rows := map[string]Attempt{}
	for i, s := range []string{AttemptUnknown, AttemptVoidDeclined, AttemptBound, AttemptVoided, AttemptCharging, AttemptVoidUnknown} {
		a := fx.claim(fx.in(int64(10_000 + i)))
		rows[s] = a
	}
	for s, a := range rows {
		fx.force(a.ID, s)
	}

	ids := func(list []Attempt) map[string]bool {
		m := map[string]bool{}
		for _, a := range list {
			if a.TillSessionID == fx.session {
				m[a.Status] = true
			}
		}
		return m
	}

	open, err := fx.store.ListOpen(ctx, ListQuery{OpenOnly: true})
	if err != nil {
		t.Fatalf("ListOpen open: %v", err)
	}
	if got := ids(open); len(got) != 3 || !got[AttemptUnknown] || !got[AttemptVoidDeclined] || !got[AttemptVoidUnknown] {
		t.Fatalf("open list statuses = %v, want UNKNOWN, VOID_DECLINED, VOID_UNKNOWN", got)
	}

	from, to := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	ranged, err := fx.store.ListOpen(ctx, ListQuery{OpenOnly: true, From: from, To: to})
	if err != nil {
		t.Fatalf("ListOpen range: %v", err)
	}
	// Open ones, plus every attempt in range with a processor ref that is not BOUND (VOIDED here).
	// BOUND has a ref and a sale behind it, CHARGING has no ref.
	if got := ids(ranged); len(got) != 4 || !got[AttemptVoided] || got[AttemptBound] || got[AttemptCharging] {
		t.Fatalf("range list statuses = %v, want the open three plus VOIDED", got)
	}

	past, err := fx.store.ListOpen(ctx, ListQuery{OpenOnly: true, From: from.Add(-48 * time.Hour), To: from.Add(-24 * time.Hour)})
	if err != nil {
		t.Fatalf("ListOpen past range: %v", err)
	}
	if got := ids(past); len(got) != 3 || got[AttemptVoided] {
		t.Fatalf("past range statuses = %v, want only the open three", got)
	}

	if _, err := fx.store.ListOpen(ctx, ListQuery{}); err == nil {
		t.Fatal("a list with neither the open filter nor a range must be refused")
	}
}

func checkViolation(t *testing.T, err error, constraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != constraint {
		t.Fatalf("err = %v, want check violation %s", err, constraint)
	}
}

// The impossible rows cannot exist, whatever the application does.
func TestAttempts_CheckConstraintsRejectImpossibleRows(t *testing.T) {
	fx := newAttemptFx(t)
	ctx := context.Background()
	set := func(id, sql string, args ...any) error {
		_, err := fx.helper.Exec(ctx, `UPDATE card_charge_attempts SET `+sql+` WHERE id = $1::uuid`, append([]any{id}, args...)...)
		return err
	}
	a := fx.claim(fx.in(880))
	checkViolation(t, set(a.ID, `status = 'BOUND', processor_ref = 'r'`), "cca_bound_has_payment")
	checkViolation(t, set(a.ID, `status = 'CAPTURED'`), "cca_money_has_ref")
	checkViolation(t, set(a.ID, `status = 'VOIDING'`), "cca_money_has_ref")
	checkViolation(t, set(a.ID, `status = 'VOIDED', processor_ref = 'r'`), "cca_voided_says_how")
	checkViolation(t, set(a.ID, `status = 'VOIDED', processor_ref = 'r', reversal_kind = 'VOID'`), "cca_voided_says_how")
	checkViolation(t, set(a.ID, `status = 'VOIDED', processor_ref = 'r', reversal_ref = 'v'`), "cca_voided_says_how")
	checkViolation(t, set(a.ID, `status = 'RESOLVED'`), "cca_resolved_says_how")
	checkViolation(t, set(a.ID, `status = 'RESOLVED', resolution = 'NOT_CHARGED', resolution_note = 'n'`), "cca_resolved_says_how")
	checkViolation(t, set(a.ID, `status = 'RESOLVED', resolution = 'NOT_CHARGED', resolved_by = $2::uuid`, fx.user), "cca_resolved_says_how")
	checkViolation(t, set(a.ID, `status = 'RESOLVED', resolution_note = 'n', resolved_by = $2::uuid`, fx.user), "cca_resolved_says_how")
	if err := set(a.ID, `status = 'NOPE'`); err == nil {
		t.Fatal("an unknown status must be rejected")
	}
	if err := set(a.ID, `amount_cents = 0`); err == nil {
		t.Fatal("a zero amount must be rejected")
	}
	if got := fx.get(a.LegKey); got.Status != AttemptCharging {
		t.Fatalf("rejected writes changed the row: %+v", got)
	}
	// The legal shape of each is accepted.
	fx.force(a.ID, AttemptVoided)
	fx.force(a.ID, AttemptResolved)
	fx.force(a.ID, AttemptBound)
}
