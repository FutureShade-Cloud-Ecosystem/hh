//go:build integration

// capture_integration_test.go: Service.Capture, Compensate and BindTx against a real Postgres and an
// httptest Run stand-in (design sections 3.3 to 3.5, packet A2).
//
// The Service under test is built on the attempt store's pool of attemptsPoolSize connections (the pool
// default), and every fixture or probe uses the separate helper pool, so a leg that fails to release a
// connection around the processor call stalls visibly (decision log row 786). No port or database is
// pinned: DATABASE_URL comes from the command and the legs skip without it. The payment_config row is
// the singleton, held under the DIB-396 lease for the whole leg.
package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// captureRun is the Run stand-in: it counts charges, voids, refunds and refreshes, records the bodies,
// and answers each call from an optional hook (a nil hook approves).
type captureRun struct {
	mu                               sync.Mutex
	charges, voids, refunds, refresh int
	chargeBodies, voidBodies         []map[string]any
	refundBodies                     []map[string]any
	chargeHdrs                       []http.Header
	charge, void, refund             func(n int, w http.ResponseWriter, r *http.Request)
	srv                              *httptest.Server
}

func newCaptureRun(t *testing.T) *captureRun {
	t.Helper()
	cr := &captureRun{}
	cr.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		cr.mu.Lock()
		var n int
		var hook func(int, http.ResponseWriter, *http.Request)
		prefix := ""
		switch {
		case r.URL.Path == "/api/v1/api_keys/refresh":
			cr.refresh++
			cr.mu.Unlock()
			_, _ = io.WriteString(w, `{"api_key":"key-2","refresh_token":"r2"}`)
			return
		case r.URL.Path == "/api/v1/charge":
			cr.charges++
			n, hook, prefix = cr.charges, cr.charge, "run-ch-"
			cr.chargeBodies = append(cr.chargeBodies, body)
			cr.chargeHdrs = append(cr.chargeHdrs, r.Header.Clone())
		case body["action"] == "void":
			cr.voids++
			n, hook, prefix = cr.voids, cr.void, "run-vd-"
			cr.voidBodies = append(cr.voidBodies, body)
		default:
			cr.refunds++
			n, hook, prefix = cr.refunds, cr.refund, "run-rf-"
			cr.refundBodies = append(cr.refundBodies, body)
		}
		cr.mu.Unlock()
		if hook != nil {
			hook(n, w, r)
			return
		}
		_, _ = fmt.Fprintf(w, `{"result":"A","trans_id":"%s%d"}`, prefix, n)
	}))
	t.Cleanup(cr.srv.Close)
	return cr
}

func (cr *captureRun) counts() (charges, voids, refunds int) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.charges, cr.voids, cr.refunds
}

// await waits a bounded time for a stand-in signal, so a missing call fails the leg instead of hanging it.
func await(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// holdUntil parks a stand-in handler until the leg releases it, bounded so a failed leg cannot leave the
// server's close waiting on it.
func holdUntil(release <-chan struct{}) {
	select {
	case <-release:
	case <-time.After(15 * time.Second):
	}
}

func awaitErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for the capture to answer")
		return nil
	}
}

func replyC(_ int, w http.ResponseWriter, _ *http.Request) {
	_, _ = io.WriteString(w, `{"result":"C","resp_code":"05","resp_text":"declined","trans_id":"run-declined"}`)
}

func replyStatus(code int) func(int, http.ResponseWriter, *http.Request) {
	return func(_ int, w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

func replyBody(s string) func(int, http.ResponseWriter, *http.Request) {
	return func(_ int, w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, s) }
}

// replyDrop closes the connection with no answer: a transport error on the client.
func replyDrop(_ int, w http.ResponseWriter, _ *http.Request) {
	if hj, ok := w.(http.Hijacker); ok {
		if c, _, err := hj.Hijack(); err == nil {
			_ = c.Close()
		}
	}
}

// recordingProc wraps the live client and records the per dispatch key each call carried.
type recordingProc struct {
	inner                            Processor
	mu                               sync.Mutex
	chargeKeys, voidKeys, refundKeys []string
}

func (p *recordingProc) Name() string { return p.inner.Name() }
func (p *recordingProc) Charge(ctx context.Context, creds map[string]string, mid string, amt int64, tok string) (ChargeResult, error) {
	p.mu.Lock()
	p.chargeKeys = append(p.chargeKeys, creds["__idem"])
	p.mu.Unlock()
	return p.inner.Charge(ctx, creds, mid, amt, tok)
}
func (p *recordingProc) Refund(ctx context.Context, creds map[string]string, mid, ref string, amt int64) (RefundResult, error) {
	p.mu.Lock()
	p.refundKeys = append(p.refundKeys, creds["__idem"])
	p.mu.Unlock()
	return p.inner.Refund(ctx, creds, mid, ref, amt)
}
func (p *recordingProc) Void(ctx context.Context, creds map[string]string, mid, ref string, amt int64) (RefundResult, error) {
	p.mu.Lock()
	p.voidKeys = append(p.voidKeys, creds["__idem"])
	p.mu.Unlock()
	return p.inner.Void(ctx, creds, mid, ref, amt)
}

type captureRig struct {
	t     *testing.T
	fx    *attemptFx
	run   *captureRun
	st    *Store
	svc   *Service
	rec   *recordingProc
	count int64
}

const rigToken = "tok_SECRETMARK_4242"

func newCaptureRig(t *testing.T) *captureRig {
	t.Helper()
	fx := newAttemptFx(t)
	run := newCaptureRun(t)
	cleanConfig(t, fx.helper) // the singleton lease for the rest of the leg
	vault, err := NewVault(testVaultKey)
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	st := NewStore(fx.store.pool, vault)
	if err := st.Configure(context.Background(), "run_payments", "MID-CAP", "CAD", run.srv.URL, 0, false,
		map[string]string{"api_key": "key-1"}, ""); err != nil {
		t.Fatalf("configure: %v", err)
	}
	t.Cleanup(func() { _, _ = fx.helper.Exec(context.Background(), `DELETE FROM payment_config`) })
	rec := &recordingProc{inner: NewRunPaymentsClient(run.srv.URL)}
	return &captureRig{t: t, fx: fx, run: run, st: st, svc: NewService(st, rec), rec: rec}
}

// input is a fresh leg: its own leg key and sale key, and an amount no other leg of the rig shares (the
// till guard blocks one till and amount while an earlier attempt is unresolved).
func (r *captureRig) input() CaptureInput {
	r.count++
	return CaptureInput{LegKey: uuid.NewString(), SaleKey: uuid.NewString(), LegSeq: 1, AmountCents: 1000 + r.count,
		BranchID: r.fx.branch, TillSessionID: r.fx.session, CardType: "CREDIT", CreatedBy: r.fx.user, AccountToken: rigToken}
}

func claimOf(in CaptureInput) ClaimInput {
	return ClaimInput{LegKey: in.LegKey, SaleKey: in.SaleKey, LegSeq: in.LegSeq, AmountCents: in.AmountCents,
		BranchID: in.BranchID, TillSessionID: in.TillSessionID, CustomerID: in.CustomerID, CardType: in.CardType,
		Processor: "run_payments", CreatedBy: in.CreatedBy}
}

// planted claims the leg and forces its row into a status, with no processor call.
func (r *captureRig) planted(in CaptureInput, status string) Attempt {
	r.t.Helper()
	a := r.fx.claim(claimOf(in))
	if status != AttemptCharging {
		r.fx.force(a.ID, status)
	}
	return r.fx.get(in.LegKey)
}

func (r *captureRig) capture(in CaptureInput) (CaptureResult, error) {
	return r.svc.Capture(context.Background(), in)
}

func (r *captureRig) mustCapture(in CaptureInput) Attempt {
	r.t.Helper()
	res, err := r.capture(in)
	if err != nil || res.Kind != CaptureKindCaptured || res.Attempt.Status != AttemptCaptured {
		r.t.Fatalf("capture: %+v / %v, want a CAPTURED attempt", res, err)
	}
	return res.Attempt
}

func (r *captureRig) row(legKey string) Attempt { return r.fx.get(legKey) }

func (r *captureRig) rowText(legKey string) string {
	r.t.Helper()
	var s string
	if err := r.fx.helper.QueryRow(context.Background(),
		`SELECT row_to_json(a)::text FROM card_charge_attempts a WHERE leg_key = $1`, legKey).Scan(&s); err != nil {
		r.t.Fatalf("row text: %v", err)
	}
	return s
}

func (r *captureRig) wantCounts(charges, voids, refunds int) {
	r.t.Helper()
	c, v, f := r.run.counts()
	if c != charges || v != voids || f != refunds {
		r.t.Fatalf("processor calls: charges %d voids %d refunds %d, want %d %d %d", c, v, f, charges, voids, refunds)
	}
}

// logSink captures the default slog output for a leg.
func logSink(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&lockedWriter{w: &buf}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func TestCapture_FirstCaptureClaimsChargesOnceAndStampsCaptured(t *testing.T) {
	logs := logSink(t)
	r := newCaptureRig(t)
	in := r.input()
	in.CustomerID = ""
	res, err := r.capture(in)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if res.Kind != CaptureKindCaptured || res.Attempt.Status != AttemptCaptured || res.Attempt.ProcessorRef != "run-ch-1" {
		t.Fatalf("result = %+v, want a CAPTURED attempt with the stand-in's trans_id", res)
	}
	r.wantCounts(1, 0, 0)
	row := r.row(in.LegKey)
	if row.Status != AttemptCaptured || row.ProcessorRef != "run-ch-1" || row.ID != res.Attempt.ID {
		t.Fatalf("row = %+v, want CAPTURED with ref run-ch-1", row)
	}
	if row.AmountCents != in.AmountCents || row.SaleKey != in.SaleKey || row.TillSessionID != in.TillSessionID || row.Processor != "run_payments" {
		t.Fatalf("the claim must record the leg's facts, got %+v", row)
	}
	// The attempt id is the per dispatch key, so __idem is no longer empty.
	r.rec.mu.Lock()
	keys := append([]string(nil), r.rec.chargeKeys...)
	r.rec.mu.Unlock()
	if len(keys) != 1 || keys[0] != row.ID {
		t.Fatalf("the dispatch key = %v, want the attempt id %s", keys, row.ID)
	}
	// Nothing new on the wire (T9): no idempotency header, no key in the body, no reference field.
	r.run.mu.Lock()
	hdr, body := r.run.chargeHdrs[0], r.run.chargeBodies[0]
	r.run.mu.Unlock()
	for _, h := range []string{"X-Idempotency-Key", "Idempotency-Key"} {
		if hdr.Get(h) != "" {
			t.Fatalf("the charge must send no %s header", h)
		}
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), row.ID) || strings.Contains(string(raw), in.LegKey) || strings.Contains(string(raw), "reference") {
		t.Fatalf("neither the attempt id, the leg key nor a reference may reach the wire: %s", raw)
	}
	if body["account_token"] != rigToken || body["amount"].(float64) != float64(in.AmountCents) {
		t.Fatalf("wire body = %v, want the account token and the amount", body)
	}
	// The token is never persisted and never logged.
	if strings.Contains(r.rowText(in.LegKey), "SECRETMARK") || strings.Contains(logs.String(), "SECRETMARK") {
		t.Fatal("the account token reached the attempt row or a log line")
	}
}

func TestCapture_SameLegKeyAnswersFromTheRowWithZeroCalls(t *testing.T) {
	cases := []struct {
		status   string
		wantErr  error  // nil with wantKind set: a result
		wantKind string // result kind
	}{
		{AttemptCharging, ErrCaptureInFlight, ""},
		{AttemptCaptured, ErrCaptureInFlight, ""},
		{AttemptBound, nil, CaptureKindBound},
		{AttemptDeclined, ErrCaptureDeclined, ""},
		{AttemptVoided, ErrCaptureReversed, ""},
		{AttemptUnknown, ErrCaptureNeedsManager, ""},
		{AttemptVoiding, ErrCaptureNeedsManager, ""},
		{AttemptVoidUnknown, ErrCaptureNeedsManager, ""},
		{AttemptVoidDeclined, ErrCaptureNeedsManager, ""},
		{AttemptResolved, ErrCaptureResolved, ""},
	}
	for _, c := range cases {
		t.Run(c.status, func(t *testing.T) {
			r := newCaptureRig(t)
			in := r.input()
			planted := r.planted(in, c.status)
			res, err := r.capture(in)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, want %v", err, c.wantErr)
				}
			} else if err != nil || res.Kind != c.wantKind {
				t.Fatalf("got %+v / %v, want result kind %s", res, err, c.wantKind)
			}
			if res.Attempt.ID != planted.ID || res.Attempt.Status != c.status {
				t.Fatalf("the answer must carry the existing row (%s, %s), got %+v", planted.ID, c.status, res.Attempt)
			}
			if c.status == AttemptBound && res.Attempt.PaymentID == "" {
				t.Fatalf("a BOUND answer carries the payment id: %+v", res.Attempt)
			}
			r.wantCounts(0, 0, 0)
			if got := r.row(in.LegKey); got.Status != c.status || got.UpdatedAt != planted.UpdatedAt {
				t.Fatalf("the row must not move: %+v", got)
			}
		})
	}
}

func TestCapture_TillGuardRefusalCallsNothing(t *testing.T) {
	r := newCaptureRig(t)
	first := r.input()
	r.planted(first, AttemptUnknown)
	second := r.input()
	second.AmountCents = first.AmountCents // same till, same amount, a different sale key
	_, err := r.capture(second)
	if !errors.Is(err, ErrTillUnresolved) {
		t.Fatalf("err = %v, want ErrTillUnresolved", err)
	}
	r.wantCounts(0, 0, 0)
	if _, ok, _ := r.st.GetByLegKey(context.Background(), second.LegKey); ok {
		t.Fatal("a refused claim must leave no row")
	}
}

func TestCapture_DeclineStampsDeclinedAndARetryCallsNothing(t *testing.T) {
	r := newCaptureRig(t)
	r.run.charge = replyC
	in := r.input()
	res, err := r.capture(in)
	if !errors.Is(err, ErrCaptureDeclined) {
		t.Fatalf("err = %v, want ErrCaptureDeclined", err)
	}
	row := r.row(in.LegKey)
	if row.Status != AttemptDeclined || res.Attempt.Status != AttemptDeclined || row.RespCode != "05" || row.ProcessorRef != "" {
		t.Fatalf("row = %+v, want DECLINED with resp_code 05 and no ref", row)
	}
	r.wantCounts(1, 0, 0)
	if _, err := r.capture(in); !errors.Is(err, ErrCaptureDeclined) {
		t.Fatalf("retry err = %v, want ErrCaptureDeclined", err)
	}
	r.wantCounts(1, 0, 0)
}

func TestCapture_EveryUnknownOutcomeStampsUnknownAndARetryCallsNothing(t *testing.T) {
	logs := logSink(t)
	cases := []struct {
		name string
		h    func(int, http.ResponseWriter, *http.Request)
	}{
		{"transport error", replyDrop},
		{"500", replyStatus(500)},
		{"503", replyStatus(503)},
		{"404", replyStatus(404)},
		{"result B", replyBody(`{"result":"B","resp_text":"retry","trans_id":"run-b"}`)},
		{"unrecognised result", replyBody(`{"result":"X"}`)},
		{"unreadable body", replyBody(`<html>`)},
		{"approved without a trans_id", replyBody(`{"result":"A"}`)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newCaptureRig(t)
			r.run.charge = c.h
			in := r.input()
			res, err := r.capture(in)
			if !errors.Is(err, ErrCaptureNeedsManager) {
				t.Fatalf("err = %v, want ErrCaptureNeedsManager", err)
			}
			row := r.row(in.LegKey)
			if row.Status != AttemptUnknown || res.Attempt.Status != AttemptUnknown || row.LastError == "" {
				t.Fatalf("row = %+v, want UNKNOWN with a diagnostic", row)
			}
			r.wantCounts(1, 0, 0)
			if _, err := r.capture(in); !errors.Is(err, ErrCaptureNeedsManager) {
				t.Fatalf("retry err = %v, want ErrCaptureNeedsManager", err)
			}
			r.wantCounts(1, 0, 0)
			if strings.Contains(r.rowText(in.LegKey), "SECRETMARK") || strings.Contains(logs.String(), "SECRETMARK") {
				t.Fatal("the account token reached the row or a log line")
			}
		})
	}
}

func TestCapture_NothingIsDispatchedWhenTheProcessorCannotBeReached(t *testing.T) {
	t.Run("no active config", func(t *testing.T) {
		r := newCaptureRig(t)
		if _, err := r.fx.helper.Exec(context.Background(), `DELETE FROM payment_config`); err != nil {
			t.Fatal(err)
		}
		in := r.input()
		res, err := r.capture(in)
		if !errors.Is(err, ErrCaptureNotDispatched) || !errors.Is(err, ErrNoConfig) {
			t.Fatalf("err = %v, want ErrCaptureNotDispatched wrapping ErrNoConfig", err)
		}
		if res.Attempt.Status != AttemptDeclined || r.row(in.LegKey).Status != AttemptDeclined {
			t.Fatalf("a refused dispatch must close the row (DECLINED, no money moved): %+v", res.Attempt)
		}
		r.wantCounts(0, 0, 0)
	})
	t.Run("a missing token or amount fails before any claim", func(t *testing.T) {
		r := newCaptureRig(t)
		in := r.input()
		in.AccountToken = ""
		if _, err := r.capture(in); err == nil {
			t.Fatal("a capture with no token must fail")
		}
		in = r.input()
		in.AmountCents = 0
		if _, err := r.capture(in); err == nil {
			t.Fatal("a capture with no amount must fail")
		}
		var n int
		if err := r.fx.helper.QueryRow(context.Background(), `SELECT count(*) FROM card_charge_attempts WHERE till_session_id = $1::uuid`, r.fx.session).Scan(&n); err != nil || n != 0 {
			t.Fatalf("rows = %d / %v, want none", n, err)
		}
		r.wantCounts(0, 0, 0)
	})
}

// A request that hangs up after the processor call started must not lose the answer: the dispatch and
// its stamp run on a context detached from the request.
func TestCapture_TheAnswerIsStampedAfterTheRequestContextIsCancelled(t *testing.T) {
	r := newCaptureRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.run.charge = func(n int, w http.ResponseWriter, _ *http.Request) {
		cancel() // the client hangs up while the processor call is in flight
		time.Sleep(50 * time.Millisecond)
		_, _ = fmt.Fprintf(w, `{"result":"A","trans_id":"run-ch-%d"}`, n)
	}
	in := r.input()
	res, err := r.svc.Capture(ctx, in)
	if err != nil || res.Kind != CaptureKindCaptured {
		t.Fatalf("Capture: %+v / %v, want CAPTURED despite the cancelled request", res, err)
	}
	if row := r.row(in.LegKey); row.Status != AttemptCaptured || row.ProcessorRef != "run-ch-1" {
		t.Fatalf("row = %+v, want CAPTURED with the answer's ref", row)
	}
}

func TestCapture_TwoConcurrentCapturesOfOneLegKeyChargeOnce(t *testing.T) {
	r := newCaptureRig(t)
	arrived, release := make(chan struct{}), make(chan struct{})
	r.run.charge = func(n int, w http.ResponseWriter, _ *http.Request) {
		close(arrived)
		holdUntil(release)
		_, _ = fmt.Fprintf(w, `{"result":"A","trans_id":"run-ch-%d"}`, n)
	}
	in := r.input()
	type out struct {
		res CaptureResult
		err error
	}
	first := make(chan out, 1)
	go func() {
		res, err := r.capture(in)
		first <- out{res, err}
	}()
	await(t, arrived, "the first capture to reach the processor")
	// The second request, on the same leg key, while the first charge is in flight.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res2, err2 := r.svc.Capture(ctx, in)
	if !errors.Is(err2, ErrCaptureInFlight) {
		t.Fatalf("second capture = %+v / %v, want ErrCaptureInFlight (a stall prints a deadline error)", res2, err2)
	}
	close(release)
	select {
	case o := <-first:
		if o.err != nil || o.res.Kind != CaptureKindCaptured {
			t.Fatalf("first capture = %+v / %v, want CAPTURED", o.res, o.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("STALL: the first capture never answered")
	}
	r.wantCounts(1, 0, 0)
}

// Rule 786: a capture holds no pool connection while the processor works. Pool size plus two captures
// are in the processor at once; the stand-in answers none until all have arrived, so a capture that
// kept a connection through its dispatch would starve a later one's claim and the leg would stall.
func TestCapture_NoPoolConnectionIsHeldAcrossTheProcessorCall(t *testing.T) {
	r := newCaptureRig(t)
	const n = attemptsPoolSize + 2
	var mu sync.Mutex
	inflight := 0
	allIn := make(chan struct{})
	r.run.charge = func(k int, w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		inflight++
		if inflight == n {
			close(allIn)
		}
		mu.Unlock()
		select {
		case <-allIn:
		case <-time.After(10 * time.Second):
		}
		_, _ = fmt.Fprintf(w, `{"result":"A","trans_id":"run-ch-%d"}`, k)
	}
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		in := r.input()
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if res, err := r.svc.Capture(ctx, in); err != nil || res.Kind != CaptureKindCaptured {
				errs <- fmt.Errorf("capture: %+v / %w", res, err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("%v", err)
	}
	select {
	case <-allIn:
	default:
		t.Fatalf("STALL: only %d of %d captures were in the processor at once, a capture held a pool connection through its dispatch", inflight, n)
	}
	r.wantCounts(n, 0, 0)
}

func TestCapture_ALateAnswerAfterTheSweepIsVoidedNotDropped(t *testing.T) {
	for _, moved := range []string{AttemptUnknown, AttemptResolved} {
		t.Run(moved, func(t *testing.T) {
			logs := logSink(t)
			r := newCaptureRig(t)
			arrived, release := make(chan struct{}), make(chan struct{})
			r.run.charge = func(n int, w http.ResponseWriter, _ *http.Request) {
				close(arrived)
				holdUntil(release)
				_, _ = fmt.Fprintf(w, `{"result":"A","trans_id":"run-ch-%d"}`, n)
			}
			in := r.input()
			done := make(chan error, 1)
			var res CaptureResult
			go func() {
				var err error
				res, err = r.capture(in)
				done <- err
			}()
			await(t, arrived, "the charge to reach the processor")
			// While the call is out, the row leaves CHARGING (the sweep moved it, or a manager closed it).
			set := `status = 'UNKNOWN', last_error = 'swept past grace'`
			if moved == AttemptResolved {
				set = fmt.Sprintf(`status = 'RESOLVED', resolution = 'NOT_CHARGED', resolution_note = 'closed while in flight', resolved_by = '%s'::uuid`, r.fx.user)
			}
			if _, err := r.fx.helper.Exec(context.Background(), `UPDATE card_charge_attempts SET `+set+` WHERE leg_key = $1 AND status = 'CHARGING'`, in.LegKey); err != nil {
				t.Fatal(err)
			}
			close(release)
			if err := awaitErr(t, done); !errors.Is(err, ErrCaptureReversed) {
				t.Fatalf("err = %v, want ErrCaptureReversed (the late capture is voided)", err)
			}
			row := r.row(in.LegKey)
			if row.Status != AttemptVoided || row.ProcessorRef != "run-ch-1" || row.ReversalKind != ReversalVoid || row.ReversalRef != "run-vd-1" {
				t.Fatalf("row = %+v, want VOIDED by void with the late capture's ref kept", row)
			}
			if res.Attempt.Status != AttemptVoided {
				t.Fatalf("the result carries the row: %+v", res.Attempt)
			}
			// A RESOLVED source keeps the manager's story: the resolution and note survive in the
			// diagnostic (the table holds the resolution columns on a RESOLVED row only), and the
			// late answer's ref is the row's ref.
			if moved == AttemptResolved {
				if !strings.Contains(row.LastError, "NOT_CHARGED") || !strings.Contains(row.LastError, "closed while in flight") {
					t.Fatalf("the manager's resolution and note must survive the move: last_error = %q", row.LastError)
				}
				if row.Resolution != "" || row.ResolutionNote != "" || row.ResolvedBy != "" {
					t.Fatalf("the resolution columns must go with the move: %+v", row)
				}
			}
			r.wantCounts(1, 1, 0)
			r.run.mu.Lock()
			vb := r.run.voidBodies[0]
			r.run.mu.Unlock()
			if vb["trans_id"] != "run-ch-1" || vb["action"] != "void" {
				t.Fatalf("void body = %v, want the late capture's trans_id and action void", vb)
			}
			if !strings.Contains(logs.String(), "run-ch-1") {
				t.Fatalf("a late answer must be logged with its ref: %s", logs.String())
			}
		})
	}
}

// A late answer on a row no status path can take (not UNKNOWN, not RESOLVED) is surfaced loudly with
// the ref in the error and in the log, never dropped.
func TestCapture_ALateAnswerThatCannotBeRecordedIsSurfacedWithItsRef(t *testing.T) {
	logs := logSink(t)
	r := newCaptureRig(t)
	arrived, release := make(chan struct{}), make(chan struct{})
	r.run.charge = func(n int, w http.ResponseWriter, _ *http.Request) {
		close(arrived)
		holdUntil(release)
		_, _ = fmt.Fprintf(w, `{"result":"A","trans_id":"run-ch-%d"}`, n)
	}
	in := r.input()
	done := make(chan error, 1)
	go func() {
		_, err := r.capture(in)
		done <- err
	}()
	await(t, arrived, "the charge to reach the processor")
	if _, err := r.fx.helper.Exec(context.Background(), `UPDATE card_charge_attempts SET status = 'DECLINED' WHERE leg_key = $1`, in.LegKey); err != nil {
		t.Fatal(err)
	}
	close(release)
	err := awaitErr(t, done)
	if !errors.Is(err, ErrCaptureAnswerUnrecorded) || !strings.Contains(err.Error(), "run-ch-1") {
		t.Fatalf("err = %v, want ErrCaptureAnswerUnrecorded naming the ref", err)
	}
	if !strings.Contains(logs.String(), "run-ch-1") || !strings.Contains(logs.String(), "level=ERROR") {
		t.Fatalf("the unrecorded ref must be logged at error level: %s", logs.String())
	}
	r.wantCounts(1, 0, 0)
}

func TestCapture_RotationKeepsTheLedgerAndFlusherCarrier(t *testing.T) {
	r := newCaptureRig(t)
	r.run.charge = func(n int, w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer key-2" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = fmt.Fprintf(w, `{"result":"A","trans_id":"run-ch-%d"}`, n)
	}
	in := r.input()
	if _, err := r.capture(in); err != nil {
		t.Fatalf("Capture through a refresh: %v", err)
	}
	r.svc.flushRotation(context.Background())
	_, creds, err := r.st.activeConfig(context.Background())
	if err != nil || creds["api_key"] != "key-2" {
		t.Fatalf("the rotated key must be written back to the config row, got %q / %v", creds["api_key"], err)
	}
}

func TestCapture_StubRefsDifferAcrossSalesWithTheSameTokenAndAmount(t *testing.T) {
	r := newCaptureRig(t)
	svc := NewService(r.st, RunPaymentsStub{})
	in1 := r.input()
	a1, err := svc.Capture(context.Background(), in1)
	if err != nil || !strings.HasPrefix(a1.Attempt.ProcessorRef, StubCaptureRefPrefix) {
		t.Fatalf("stub capture: %+v / %v", a1, err)
	}
	c1, err := svc.Compensate(context.Background(), a1.Attempt.ID)
	if err != nil || c1.Kind != CompensateKindVoided || c1.Attempt.ReversalKind != ReversalVoid || !strings.HasPrefix(c1.Attempt.ReversalRef, "rp_void_") {
		t.Fatalf("stub compensate: %+v / %v", c1, err)
	}
	in2 := r.input()
	in2.AmountCents = in1.AmountCents // the same token and amount, another sale
	a2, err := svc.Capture(context.Background(), in2)
	if err != nil {
		t.Fatalf("second stub capture: %v", err)
	}
	if a1.Attempt.ProcessorRef == a2.Attempt.ProcessorRef {
		t.Fatalf("two sales with one token and amount must not share a ref, both %q", a1.Attempt.ProcessorRef)
	}
}

// -- Compensate --

func (r *captureRig) compensate(id string) (CompensateResult, error) {
	return r.svc.Compensate(context.Background(), id)
}

func TestCompensate_VoidApproved(t *testing.T) {
	r := newCaptureRig(t)
	in := r.input()
	a := r.mustCapture(in)
	res, err := r.compensate(a.ID)
	if err != nil || res.Kind != CompensateKindVoided {
		t.Fatalf("Compensate: %+v / %v, want VOIDED", res, err)
	}
	row := r.row(in.LegKey)
	if row.Status != AttemptVoided || row.ReversalKind != ReversalVoid || row.ReversalRef != "run-vd-1" || row.ProcessorRef != "run-ch-1" {
		t.Fatalf("row = %+v, want VOIDED by VOID, ref run-vd-1", row)
	}
	r.wantCounts(1, 1, 0)
	r.run.mu.Lock()
	vb := r.run.voidBodies[0]
	r.run.mu.Unlock()
	if vb["action"] != "void" || vb["trans_id"] != "run-ch-1" || vb["amount"].(float64) != float64(in.AmountCents) || vb["mid"] != "MID-CAP" {
		t.Fatalf("void body = %v, want action void on the charge's trans_id for the full amount", vb)
	}
	r.rec.mu.Lock()
	vk := append([]string(nil), r.rec.voidKeys...)
	r.rec.mu.Unlock()
	if len(vk) != 1 || vk[0] != "void:"+a.ID {
		t.Fatalf("void dispatch key = %v, want void:<attempt id>", vk)
	}
	// A second compensation of a VOIDED row is answered from the row, calling nothing.
	again, err := r.compensate(a.ID)
	if err != nil || again.Kind != CompensateKindVoided {
		t.Fatalf("second Compensate: %+v / %v, want VOIDED", again, err)
	}
	r.wantCounts(1, 1, 0)
}

func TestCompensate_VoidDeclinedThenRefundApproved(t *testing.T) {
	r := newCaptureRig(t)
	r.run.void = replyC
	in := r.input()
	a := r.mustCapture(in)
	res, err := r.compensate(a.ID)
	if err != nil || res.Kind != CompensateKindVoided {
		t.Fatalf("Compensate: %+v / %v, want VOIDED", res, err)
	}
	row := r.row(in.LegKey)
	if row.Status != AttemptVoided || row.ReversalKind != ReversalRefund || row.ReversalRef != "run-rf-1" {
		t.Fatalf("row = %+v, want VOIDED by REFUND, ref run-rf-1", row)
	}
	r.wantCounts(1, 1, 1)
	r.run.mu.Lock()
	rb := r.run.refundBodies[0]
	r.run.mu.Unlock()
	if rb["action"] != "refund" || rb["trans_id"] != "run-ch-1" || rb["amount"].(float64) != float64(in.AmountCents) {
		t.Fatalf("refund body = %v", rb)
	}
	r.rec.mu.Lock()
	rk := append([]string(nil), r.rec.refundKeys...)
	r.rec.mu.Unlock()
	if len(rk) != 1 || rk[0] != "refund:"+a.ID {
		t.Fatalf("refund dispatch key = %v, want refund:<attempt id>", rk)
	}
}

func TestCompensate_BothDeclinedIsVoidDeclined(t *testing.T) {
	r := newCaptureRig(t)
	r.run.void, r.run.refund = replyC, replyC
	in := r.input()
	a := r.mustCapture(in)
	res, err := r.compensate(a.ID)
	if err != nil || res.Kind != CompensateKindNeedsManager {
		t.Fatalf("Compensate: %+v / %v, want NEEDS_MANAGER", res, err)
	}
	if row := r.row(in.LegKey); row.Status != AttemptVoidDeclined || row.LastError == "" {
		t.Fatalf("row = %+v, want VOID_DECLINED with a diagnostic", row)
	}
	r.wantCounts(1, 1, 1)
	// Never retried blindly.
	if again, err := r.compensate(a.ID); err != nil || again.Kind != CompensateKindNeedsManager {
		t.Fatalf("second Compensate: %+v / %v", again, err)
	}
	r.wantCounts(1, 1, 1)
}

func TestCompensate_ALostVoidAnswerIsVoidUnknownAndNeverRetried(t *testing.T) {
	for name, h := range map[string]func(int, http.ResponseWriter, *http.Request){
		"transport": replyDrop, "500": replyStatus(500), "result B": replyBody(`{"result":"B"}`), "unreadable": replyBody(`x`),
	} {
		t.Run(name, func(t *testing.T) {
			r := newCaptureRig(t)
			r.run.void = h
			in := r.input()
			a := r.mustCapture(in)
			res, err := r.compensate(a.ID)
			if err != nil || res.Kind != CompensateKindNeedsManager {
				t.Fatalf("Compensate: %+v / %v, want NEEDS_MANAGER", res, err)
			}
			if row := r.row(in.LegKey); row.Status != AttemptVoidUnknown || row.LastError == "" {
				t.Fatalf("row = %+v, want VOID_UNKNOWN", row)
			}
			r.wantCounts(1, 1, 0) // no refund: a lost answer is never followed by another reversal
			if _, err := r.compensate(a.ID); err != nil {
				t.Fatalf("second Compensate: %v", err)
			}
			r.wantCounts(1, 1, 0)
		})
	}
}

func TestCompensate_ALostRefundAnswerIsVoidUnknown(t *testing.T) {
	r := newCaptureRig(t)
	r.run.void, r.run.refund = replyC, replyStatus(502)
	in := r.input()
	a := r.mustCapture(in)
	res, err := r.compensate(a.ID)
	if err != nil || res.Kind != CompensateKindNeedsManager {
		t.Fatalf("Compensate: %+v / %v", res, err)
	}
	if row := r.row(in.LegKey); row.Status != AttemptVoidUnknown {
		t.Fatalf("row = %+v, want VOID_UNKNOWN", row)
	}
	r.wantCounts(1, 1, 1)
}

func TestCompensate_ABoundRowIsACommittedSaleAndVoidsNothing(t *testing.T) {
	r := newCaptureRig(t)
	in := r.input()
	a := r.mustCapture(in)
	ctx := context.Background()
	tx, err := r.fx.helper.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.svc.BindTx(ctx, tx, a.ID, r.fx.newPayment()); err != nil {
		t.Fatalf("BindTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := r.compensate(a.ID)
	if err != nil || res.Kind != CompensateKindCommitted || res.Attempt.Status != AttemptBound || res.Attempt.PaymentID == "" {
		t.Fatalf("Compensate: %+v / %v, want COMMITTED on the BOUND row", res, err)
	}
	r.wantCounts(1, 0, 0)
	if row := r.row(in.LegKey); row.Status != AttemptBound {
		t.Fatalf("row = %+v, want BOUND untouched", row)
	}
}

func TestCompensate_OnlyACapturedRowCanBeReversed(t *testing.T) {
	for _, st := range []string{AttemptCharging, AttemptDeclined, AttemptUnknown, AttemptResolved} {
		t.Run(st, func(t *testing.T) {
			r := newCaptureRig(t)
			in := r.input()
			a := r.planted(in, st)
			if _, err := r.compensate(a.ID); !errors.Is(err, ErrCompensateNotCaptured) {
				t.Fatalf("err = %v, want ErrCompensateNotCaptured", err)
			}
			r.wantCounts(0, 0, 0)
			if row := r.row(in.LegKey); row.Status != st {
				t.Fatalf("the row must not move, got %+v", row)
			}
		})
	}
	t.Run("no such attempt", func(t *testing.T) {
		r := newCaptureRig(t)
		if _, err := r.compensate(uuid.NewString()); err == nil {
			t.Fatal("an unknown attempt id must fail")
		}
		r.wantCounts(0, 0, 0)
	})
	t.Run("a reversal already in flight is not started twice", func(t *testing.T) {
		r := newCaptureRig(t)
		in := r.input()
		a := r.planted(in, AttemptVoiding)
		res, err := r.compensate(a.ID)
		if err != nil || res.Kind != CompensateKindNeedsManager {
			t.Fatalf("Compensate: %+v / %v, want NEEDS_MANAGER", res, err)
		}
		r.wantCounts(0, 0, 0)
	})
}

func TestCompensate_RunsOnADetachedContext(t *testing.T) {
	t.Run("cancelled while the void is out", func(t *testing.T) {
		r := newCaptureRig(t)
		in := r.input()
		a := r.mustCapture(in)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.run.void = func(n int, w http.ResponseWriter, _ *http.Request) {
			cancel()
			time.Sleep(50 * time.Millisecond)
			_, _ = fmt.Fprintf(w, `{"result":"A","trans_id":"run-vd-%d"}`, n)
		}
		res, err := r.svc.Compensate(ctx, a.ID)
		if err != nil || res.Kind != CompensateKindVoided {
			t.Fatalf("Compensate: %+v / %v, want VOIDED despite the cancelled request", res, err)
		}
		if row := r.row(in.LegKey); row.Status != AttemptVoided {
			t.Fatalf("row = %+v, want VOIDED", row)
		}
	})
	t.Run("already cancelled on entry", func(t *testing.T) {
		r := newCaptureRig(t)
		in := r.input()
		a := r.mustCapture(in)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		res, err := r.svc.Compensate(ctx, a.ID)
		if err != nil || res.Kind != CompensateKindVoided {
			t.Fatalf("Compensate: %+v / %v, want VOIDED (compensation is not abandoned with the request)", res, err)
		}
	})
}

func TestCompensate_ANoConfigAnswerStopsAtVoidUnknownWithNoCall(t *testing.T) {
	r := newCaptureRig(t)
	in := r.input()
	a := r.mustCapture(in)
	if _, err := r.fx.helper.Exec(context.Background(), `DELETE FROM payment_config`); err != nil {
		t.Fatal(err)
	}
	res, err := r.compensate(a.ID)
	if err != nil || res.Kind != CompensateKindNeedsManager {
		t.Fatalf("Compensate: %+v / %v, want NEEDS_MANAGER", res, err)
	}
	if row := r.row(in.LegKey); row.Status != AttemptVoidUnknown {
		t.Fatalf("row = %+v, want VOID_UNKNOWN (money is held, a person must look)", row)
	}
	r.wantCounts(1, 0, 0)
}

// -- BindTx --

func TestServiceBindTx_BindsOnTheSalesTransactionOnce(t *testing.T) {
	r := newCaptureRig(t)
	in := r.input()
	a := r.mustCapture(in)
	ctx := context.Background()

	// A rolled back sale leaves the row CAPTURED.
	tx, _ := r.fx.helper.Begin(ctx)
	if err := r.svc.BindTx(ctx, tx, a.ID, r.fx.newPayment()); err != nil {
		t.Fatalf("BindTx: %v", err)
	}
	_ = tx.Rollback(ctx)
	if row := r.row(in.LegKey); row.Status != AttemptCaptured {
		t.Fatalf("after a rollback the row = %+v, want CAPTURED", row)
	}
	// A committed bind moves it to BOUND with the payment id; a second bind finds no captured row.
	pay := r.fx.newPayment()
	tx, _ = r.fx.helper.Begin(ctx)
	if err := r.svc.BindTx(ctx, tx, a.ID, pay); err != nil {
		t.Fatalf("BindTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if row := r.row(in.LegKey); row.Status != AttemptBound || row.PaymentID != pay {
		t.Fatalf("row = %+v, want BOUND to %s", row, pay)
	}
	tx, _ = r.fx.helper.Begin(ctx)
	defer func() { _ = tx.Rollback(ctx) }()
	if err := r.svc.BindTx(ctx, tx, a.ID, r.fx.newPayment()); !errors.Is(err, ErrAttemptNotCaptured) {
		t.Fatalf("second bind err = %v, want ErrAttemptNotCaptured", err)
	}
	if err := r.svc.BindTx(ctx, nil, a.ID, pay); err == nil {
		t.Fatal("a bind with no transaction must fail")
	}
}
