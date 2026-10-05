package payment

// run_outcome_test.go: the outcome classifier (ONE table in ONE function, classifyRun), the live
// client's Charge, Void and Refund against it over httptest, and the stub's Void and per key refs.
// The classifier's rows are the design's section 3.5: A approved, C a decline, a 401 refreshed and
// re-posted inside the same dispatch, and everything else (a transport error, the deadline, a 5xx, a
// non-401 4xx, an unreadable body, result B, an unrecognised result) an unknown outcome that wraps
// ErrProcessorOutcomeUnknown. No database, no network beyond httptest.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var _ Processor = RunPaymentsStub{}
var _ Processor = (*RunPaymentsClient)(nil)

func TestClassifyRun_Table(t *testing.T) {
	rows := []struct {
		name         string
		status       int
		transport    error
		raw          string
		afterRefresh bool
		want         runOutcome
	}{
		{"A approved", 200, nil, `{"result":"A","trans_id":"t1"}`, false, outcomeApproved},
		{"A approved after a refresh", 200, nil, `{"result":"A","trans_id":"t1"}`, true, outcomeApproved},
		{"C declined", 200, nil, `{"result":"C","resp_code":"05","trans_id":"t1"}`, false, outcomeDeclined},
		{"B retry is unknown", 200, nil, `{"result":"B","resp_text":"please retry"}`, false, outcomeUnknown},
		{"unrecognised result is unknown", 200, nil, `{"result":"Z"}`, false, outcomeUnknown},
		{"empty result is unknown", 200, nil, `{}`, false, outcomeUnknown},
		{"lowercase a is unrecognised", 200, nil, `{"result":"a","trans_id":"t1"}`, false, outcomeUnknown},
		{"unreadable body is unknown", 200, nil, `<html>gateway</html>`, false, outcomeUnknown},
		{"empty body is unknown", 200, nil, ``, false, outcomeUnknown},
		{"transport error is unknown", 0, errors.New("connection reset"), ``, false, outcomeUnknown},
		{"deadline is unknown", 0, context.DeadlineExceeded, ``, false, outcomeUnknown},
		{"cancelled is unknown", 0, context.Canceled, ``, false, outcomeUnknown},
		{"500 is unknown", 500, nil, ``, false, outcomeUnknown},
		{"502 is unknown", 502, nil, `bad gateway`, false, outcomeUnknown},
		{"503 is unknown", 503, nil, ``, false, outcomeUnknown},
		{"a 5xx body that says A is never trusted", 500, nil, `{"result":"A","trans_id":"t1"}`, false, outcomeUnknown},
		{"400 is unknown", 400, nil, ``, false, outcomeUnknown},
		{"403 is unknown", 403, nil, ``, false, outcomeUnknown},
		{"404 is unknown", 404, nil, ``, false, outcomeUnknown},
		{"422 is unknown", 422, nil, ``, false, outcomeUnknown},
		{"a 4xx body that says C is not a decline", 400, nil, `{"result":"C"}`, false, outcomeUnknown},
		{"a 3xx is unknown", 302, nil, ``, false, outcomeUnknown},
		{"401 asks for the refresh and re-post", 401, nil, `{"error":"expired"}`, false, outcomeReauth},
		{"401 that survives the refresh is unknown", 401, nil, `{"error":"expired"}`, true, outcomeUnknown},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			got, _, err := classifyRun(r.status, r.transport, []byte(r.raw), r.afterRefresh)
			if got != r.want {
				t.Fatalf("outcome = %v, want %v (err %v)", got, r.want, err)
			}
			switch r.want {
			case outcomeUnknown:
				if !errors.Is(err, ErrProcessorOutcomeUnknown) {
					t.Fatalf("an unknown outcome must wrap ErrProcessorOutcomeUnknown, got %v", err)
				}
				if errors.Is(err, ErrProcessorDeclined) {
					t.Fatalf("an unknown outcome must not read as a decline: %v", err)
				}
			case outcomeDeclined:
				if !errors.Is(err, ErrProcessorDeclined) || errors.Is(err, ErrProcessorOutcomeUnknown) {
					t.Fatalf("a decline must wrap ErrProcessorDeclined only, got %v", err)
				}
			case outcomeApproved, outcomeReauth:
				if err != nil {
					t.Fatalf("outcome %v carries no error, got %v", r.want, err)
				}
			}
		})
	}
	// An approval hands back the answer's trans_id.
	_, ans, _ := classifyRun(200, nil, []byte(`{"result":"A","trans_id":"txn-9","resp_code":"00"}`), false)
	if ans.TransID != "txn-9" || ans.RespCode != "00" {
		t.Fatalf("answer = %+v, want trans_id txn-9 and resp_code 00", ans)
	}
}

// runStand is a tiny Run stand-in for the client legs: it answers each path from a handler and counts
// the hits and the bodies.
type runStand struct {
	mu     sync.Mutex
	hits   map[string]int
	bodies map[string][]string
	hdrs   []http.Header
	srv    *httptest.Server
}

func newRunStand(t *testing.T, h func(path string, n int, w http.ResponseWriter, r *http.Request)) *runStand {
	t.Helper()
	rs := &runStand{hits: map[string]int{}, bodies: map[string][]string{}}
	rs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rs.mu.Lock()
		rs.hits[r.URL.Path]++
		n := rs.hits[r.URL.Path]
		rs.bodies[r.URL.Path] = append(rs.bodies[r.URL.Path], string(raw))
		rs.hdrs = append(rs.hdrs, r.Header.Clone())
		rs.mu.Unlock()
		h(r.URL.Path, n, w, r)
	}))
	t.Cleanup(rs.srv.Close)
	return rs
}

func (rs *runStand) count(path string) int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.hits[path]
}

func (rs *runStand) body(path string, i int) map[string]any {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	var m map[string]any
	_ = json.Unmarshal([]byte(rs.bodies[path][i]), &m)
	return m
}

func answer(body string) func(string, int, http.ResponseWriter, *http.Request) {
	return func(_ string, _ int, w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }
}

func status(code int, body string) func(string, int, http.ResponseWriter, *http.Request) {
	return func(_ string, _ int, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	}
}

func liveCreds(rs *runStand) map[string]string {
	return map[string]string{"api_key": "k-live", "base_url": rs.srv.URL}
}

func TestLiveClient_Charge_OutcomeRows(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		h       func(string, int, http.ResponseWriter, *http.Request)
		wantErr error // nil: approved
		wantNot error
	}{
		{"A approves", answer(`{"result":"A","trans_id":"txn-1"}`), nil, nil},
		{"C declines", answer(`{"result":"C","resp_code":"05","resp_text":"declined","trans_id":"txn-d"}`), ErrProcessorDeclined, ErrProcessorOutcomeUnknown},
		{"B is unknown", answer(`{"result":"B","resp_text":"please retry"}`), ErrProcessorOutcomeUnknown, ErrProcessorDeclined},
		{"unrecognised result is unknown", answer(`{"result":"Q"}`), ErrProcessorOutcomeUnknown, ErrProcessorDeclined},
		{"500 is unknown", status(500, `boom`), ErrProcessorOutcomeUnknown, ErrProcessorDeclined},
		{"503 is unknown", status(503, ``), ErrProcessorOutcomeUnknown, ErrProcessorDeclined},
		{"404 is unknown", status(404, `{}`), ErrProcessorOutcomeUnknown, ErrProcessorDeclined},
		{"400 is unknown", status(400, `{"result":"C"}`), ErrProcessorOutcomeUnknown, ErrProcessorDeclined},
		{"unreadable body is unknown", answer(`not json at all`), ErrProcessorOutcomeUnknown, ErrProcessorDeclined},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rs := newRunStand(t, c.h)
			res, err := NewRunPaymentsClient(rs.srv.URL).Charge(ctx, liveCreds(rs), "MID-1", 1000, "tok")
			if c.wantErr == nil {
				if err != nil || res.Status != "captured" || res.ProcessorRef != "txn-1" {
					t.Fatalf("got %+v / %v, want captured txn-1", res, err)
				}
				return
			}
			if !errors.Is(err, c.wantErr) || errors.Is(err, c.wantNot) {
				t.Fatalf("err = %v, want it to wrap %v and not %v", err, c.wantErr, c.wantNot)
			}
			if res.Status == "captured" {
				t.Fatalf("a non-approved answer must never read captured: %+v", res)
			}
			if rs.count("/api/v1/charge") != 1 {
				t.Fatalf("charge posted %d times, want exactly 1 (no blind re-post)", rs.count("/api/v1/charge"))
			}
		})
	}
}

func TestLiveClient_Charge_TransportErrorIsUnknown(t *testing.T) {
	rs := newRunStand(t, answer(`{"result":"A","trans_id":"x"}`))
	creds := liveCreds(rs)
	rs.srv.Close() // nothing listens any more
	_, err := NewRunPaymentsClient(rs.srv.URL).Charge(context.Background(), creds, "MID-1", 1000, "tok")
	if !errors.Is(err, ErrProcessorOutcomeUnknown) {
		t.Fatalf("a transport error must wrap ErrProcessorOutcomeUnknown, got %v", err)
	}
}

func TestLiveClient_Charge_DeadlineIsUnknown(t *testing.T) {
	release := make(chan struct{})
	rs := newRunStand(t, func(_ string, _ int, w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, err := NewRunPaymentsClient(rs.srv.URL).Charge(ctx, liveCreds(rs), "MID-1", 1000, "tok")
	if !errors.Is(err, ErrProcessorOutcomeUnknown) {
		t.Fatalf("a deadline must wrap ErrProcessorOutcomeUnknown, got %v", err)
	}
}

func TestLiveClient_Charge_401RefreshRepostRows(t *testing.T) {
	ctx := context.Background()
	t.Run("refresh then approved", func(t *testing.T) {
		rs := newRunStand(t, func(path string, _ int, w http.ResponseWriter, r *http.Request) {
			switch path {
			case "/api/v1/api_keys/refresh":
				_, _ = io.WriteString(w, `{"api_key":"k-new","refresh_token":"r2"}`)
			default:
				if r.Header.Get("Authorization") != "Bearer k-new" {
					w.WriteHeader(401)
					return
				}
				_, _ = io.WriteString(w, `{"result":"A","trans_id":"txn-r"}`)
			}
		})
		creds := liveCreds(rs)
		res, err := NewRunPaymentsClient(rs.srv.URL).Charge(ctx, creds, "MID-1", 1000, "tok")
		if err != nil || res.ProcessorRef != "txn-r" {
			t.Fatalf("got %+v / %v, want approved after the refresh", res, err)
		}
		if creds["api_key"] != "k-new" {
			t.Fatalf("the rotated key must land in the creds map, got %q", creds["api_key"])
		}
		if rs.count("/api/v1/charge") != 2 || rs.count("/api/v1/api_keys/refresh") != 1 {
			t.Fatalf("charge posts %d, refresh posts %d, want 2 and 1", rs.count("/api/v1/charge"), rs.count("/api/v1/api_keys/refresh"))
		}
	})
	t.Run("a refused refresh was never dispatched", func(t *testing.T) {
		rs := newRunStand(t, func(path string, _ int, w http.ResponseWriter, _ *http.Request) {
			if path == "/api/v1/api_keys/refresh" {
				w.WriteHeader(500)
				return
			}
			w.WriteHeader(401)
		})
		_, err := NewRunPaymentsClient(rs.srv.URL).Charge(ctx, liveCreds(rs), "MID-1", 1000, "tok")
		if !errors.Is(err, ErrProcessorNotDispatched) || errors.Is(err, ErrProcessorOutcomeUnknown) {
			t.Fatalf("a 401 whose refresh failed is a not dispatched error, got %v", err)
		}
		if rs.count("/api/v1/charge") != 1 {
			t.Fatalf("charge posted %d times, want 1", rs.count("/api/v1/charge"))
		}
	})
	t.Run("a 401 that survives the refresh is unknown", func(t *testing.T) {
		rs := newRunStand(t, func(path string, _ int, w http.ResponseWriter, _ *http.Request) {
			if path == "/api/v1/api_keys/refresh" {
				_, _ = io.WriteString(w, `{"api_key":"k-new"}`)
				return
			}
			w.WriteHeader(401)
		})
		_, err := NewRunPaymentsClient(rs.srv.URL).Charge(ctx, liveCreds(rs), "MID-1", 1000, "tok")
		if !errors.Is(err, ErrProcessorOutcomeUnknown) {
			t.Fatalf("a 401 on the re-post is unknown, got %v", err)
		}
		if rs.count("/api/v1/charge") != 2 {
			t.Fatalf("charge posted %d times, want 2 (one re-post, never a third)", rs.count("/api/v1/charge"))
		}
	})
}

func TestLiveClient_Charge_PreDispatchRefusalsAreNotDispatched(t *testing.T) {
	rs := newRunStand(t, func(string, int, http.ResponseWriter, *http.Request) { t.Error("no call may be made") })
	c := NewRunPaymentsClient(rs.srv.URL)
	for name, call := range map[string]func() error{
		"no api_key": func() error {
			_, err := c.Charge(context.Background(), map[string]string{"base_url": rs.srv.URL}, "MID-1", 100, "tok")
			return err
		},
		"no mid": func() error {
			_, err := c.Charge(context.Background(), liveCreds(rs), "", 100, "tok")
			return err
		},
		"no token": func() error {
			_, err := c.Charge(context.Background(), liveCreds(rs), "MID-1", 100, "")
			return err
		},
		"no host": func() error {
			_, err := NewRunPaymentsClient("").Charge(context.Background(), map[string]string{"api_key": "k"}, "MID-1", 100, "tok")
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrProcessorNotDispatched) {
			t.Errorf("%s: err = %v, want ErrProcessorNotDispatched", name, err)
		}
	}
}

func TestLiveClient_Void_SendsActionVoidAndNeverRefund(t *testing.T) {
	rs := newRunStand(t, answer(`{"result":"A","trans_id":"void-1"}`))
	res, err := NewRunPaymentsClient(rs.srv.URL).Void(context.Background(),
		map[string]string{"api_key": "k-live", "base_url": rs.srv.URL, "__idem": "void:attempt-1"}, "MID-1", "txn-orig", 4200)
	if err != nil {
		t.Fatalf("Void: %v", err)
	}
	if res.Status != "voided" || res.ProcessorRef != "void-1" {
		t.Fatalf("got %+v, want voided / void-1", res)
	}
	if rs.count("/api/v1/void-or-refund") != 1 || rs.count("/api/v1/charge") != 0 {
		t.Fatalf("hits: void-or-refund %d, charge %d", rs.count("/api/v1/void-or-refund"), rs.count("/api/v1/charge"))
	}
	body := rs.body("/api/v1/void-or-refund", 0)
	if body["action"] != "void" {
		t.Fatalf("action = %v, want void", body["action"])
	}
	if body["trans_id"] != "txn-orig" || body["mid"] != "MID-1" || body["amount"].(float64) != 4200 {
		t.Fatalf("body = %v, want trans_id txn-orig, mid MID-1, amount 4200", body)
	}
	if strings.Contains(rs.bodies["/api/v1/void-or-refund"][0], "refund") {
		t.Fatalf("a void body must not mention refund: %s", rs.bodies["/api/v1/void-or-refund"][0])
	}
	if strings.Contains(rs.bodies["/api/v1/void-or-refund"][0], "__idem") || strings.Contains(rs.bodies["/api/v1/void-or-refund"][0], "void:attempt-1") {
		t.Fatalf("the per dispatch key must not reach the wire: %s", rs.bodies["/api/v1/void-or-refund"][0])
	}
	for _, h := range []string{"X-Idempotency-Key", "Idempotency-Key"} {
		if rs.hdrs[0].Get(h) != "" {
			t.Fatalf("void must send no %s header", h)
		}
	}
	if rs.hdrs[0].Get("Authorization") != "Bearer k-live" || rs.hdrs[0].Get("mid") != "MID-1" {
		t.Fatalf("void must carry the Bearer key and mid header: %v", rs.hdrs[0])
	}
}

func TestLiveClient_Refund_SendsActionRefundAndNeverVoid(t *testing.T) {
	rs := newRunStand(t, answer(`{"result":"A","trans_id":"ref-1"}`))
	if _, err := NewRunPaymentsClient(rs.srv.URL).Refund(context.Background(), liveCreds(rs), "MID-1", "txn-orig", 4200); err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if got := rs.body("/api/v1/void-or-refund", 0)["action"]; got != "refund" {
		t.Fatalf("action = %v, want refund", got)
	}
}

func TestLiveClient_Reversals_OutcomeRows(t *testing.T) {
	ctx := context.Background()
	for _, op := range []string{"void", "refund"} {
		call := func(c *RunPaymentsClient, creds map[string]string) (RefundResult, error) {
			if op == "void" {
				return c.Void(ctx, creds, "MID-1", "txn-orig", 4200)
			}
			return c.Refund(ctx, creds, "MID-1", "txn-orig", 4200)
		}
		cases := []struct {
			name    string
			h       func(string, int, http.ResponseWriter, *http.Request)
			wantErr error
			wantNot error
		}{
			{"A approves", answer(`{"result":"A","trans_id":"rev-1"}`), nil, nil},
			{"C declines", answer(`{"result":"C","resp_code":"51","resp_text":"cannot void"}`), ErrProcessorDeclined, ErrProcessorOutcomeUnknown},
			{"B is unknown", answer(`{"result":"B"}`), ErrProcessorOutcomeUnknown, ErrProcessorDeclined},
			{"unrecognised is unknown", answer(`{"result":"?"}`), ErrProcessorOutcomeUnknown, ErrProcessorDeclined},
			{"500 is unknown", status(500, ``), ErrProcessorOutcomeUnknown, ErrProcessorDeclined},
			{"404 is unknown", status(404, ``), ErrProcessorOutcomeUnknown, ErrProcessorDeclined},
			{"401 is unknown (no refresh on a reversal)", status(401, ``), ErrProcessorOutcomeUnknown, ErrProcessorDeclined},
			{"unreadable is unknown", answer(`nope`), ErrProcessorOutcomeUnknown, ErrProcessorDeclined},
		}
		for _, c := range cases {
			t.Run(op+" "+c.name, func(t *testing.T) {
				rs := newRunStand(t, c.h)
				res, err := call(NewRunPaymentsClient(rs.srv.URL), liveCreds(rs))
				if c.wantErr == nil {
					if err != nil || res.ProcessorRef != "rev-1" {
						t.Fatalf("got %+v / %v, want approved rev-1", res, err)
					}
					return
				}
				if !errors.Is(err, c.wantErr) || errors.Is(err, c.wantNot) {
					t.Fatalf("err = %v, want it to wrap %v and not %v", err, c.wantErr, c.wantNot)
				}
				if rs.count("/api/v1/void-or-refund") != 1 {
					t.Fatalf("posted %d times, want exactly 1", rs.count("/api/v1/void-or-refund"))
				}
			})
		}
		t.Run(op+" transport error is unknown", func(t *testing.T) {
			rs := newRunStand(t, answer(`{"result":"A","trans_id":"x"}`))
			creds := liveCreds(rs)
			rs.srv.Close()
			if _, err := call(NewRunPaymentsClient(rs.srv.URL), creds); !errors.Is(err, ErrProcessorOutcomeUnknown) {
				t.Fatalf("err = %v, want unknown", err)
			}
		})
		t.Run(op+" with no host stays ErrNotImplemented", func(t *testing.T) {
			if _, err := call(NewRunPaymentsClient(""), map[string]string{"api_key": "k"}); !errors.Is(err, ErrNotImplemented) {
				t.Fatalf("err = %v, want ErrNotImplemented", err)
			}
		})
		t.Run(op+" refuses bad input before any call", func(t *testing.T) {
			rs := newRunStand(t, func(string, int, http.ResponseWriter, *http.Request) { t.Error("no call may be made") })
			c := NewRunPaymentsClient(rs.srv.URL)
			do := func(creds map[string]string, mid, ref string, amt int64) error {
				if op == "void" {
					_, err := c.Void(ctx, creds, mid, ref, amt)
					return err
				}
				_, err := c.Refund(ctx, creds, mid, ref, amt)
				return err
			}
			if do(map[string]string{}, "MID-1", "r", 1) == nil || do(liveCreds(rs), "", "r", 1) == nil ||
				do(liveCreds(rs), "MID-1", "", 1) == nil || do(liveCreds(rs), "MID-1", "r", 0) == nil {
				t.Fatal("a reversal with a missing key, mid, ref or amount must fail closed")
			}
		})
	}
}

func TestStub_Void(t *testing.T) {
	ctx := context.Background()
	creds := func(key string) map[string]string {
		m := map[string]string{"api_key": "k"}
		if key != "" {
			m["__idem"] = key
		}
		return m
	}
	a, err := RunPaymentsStub{}.Void(ctx, creds("void:1"), "MID-1", "rp_sim_orig", 500)
	if err != nil || a.Status != "voided" || !strings.HasPrefix(a.ProcessorRef, "rp_void_") {
		t.Fatalf("got %+v / %v, want an approved void with an rp_void_ ref", a, err)
	}
	if again, _ := (RunPaymentsStub{}).Void(ctx, creds("void:1"), "MID-1", "rp_sim_orig", 500); again.ProcessorRef != a.ProcessorRef {
		t.Fatalf("the same key must give the same void ref: %q vs %q", again.ProcessorRef, a.ProcessorRef)
	}
	if other, _ := (RunPaymentsStub{}).Void(ctx, creds("void:2"), "MID-1", "rp_sim_orig", 500); other.ProcessorRef == a.ProcessorRef {
		t.Fatalf("different keys on one ref must give different void refs, both %q", a.ProcessorRef)
	}
	// The same key on a refund gives a different ref: a void and a refund of one capture never collide.
	ref, _ := RunPaymentsStub{}.Refund(ctx, creds("void:1"), "MID-1", "rp_sim_orig", 500)
	if ref.ProcessorRef == a.ProcessorRef {
		t.Fatalf("a void and a refund under one key must not share a ref: %q", ref.ProcessorRef)
	}
	// Empty key: the old deterministic fallback.
	f1, _ := RunPaymentsStub{}.Void(ctx, creds(""), "MID-1", "rp_sim_orig", 500)
	f2, _ := RunPaymentsStub{}.Void(ctx, creds(""), "MID-1", "rp_sim_orig", 500)
	if f1.ProcessorRef == "" || f1.ProcessorRef != f2.ProcessorRef {
		t.Fatalf("an empty key must fall back to a stable ref, got %q and %q", f1.ProcessorRef, f2.ProcessorRef)
	}
	for name, bad := range map[string]func() error{
		"no api_key": func() error { _, e := RunPaymentsStub{}.Void(ctx, map[string]string{}, "MID-1", "r", 1); return e },
		"no mid":     func() error { _, e := RunPaymentsStub{}.Void(ctx, creds(""), "", "r", 1); return e },
		"no ref":     func() error { _, e := RunPaymentsStub{}.Void(ctx, creds(""), "MID-1", "", 1); return e },
		"no amount":  func() error { _, e := RunPaymentsStub{}.Void(ctx, creds(""), "MID-1", "r", 0); return e },
	} {
		if bad() == nil {
			t.Errorf("stub Void with %s must fail closed", name)
		}
	}
}

func TestStub_ChargeRefsAreKeyedPerDispatch(t *testing.T) {
	ctx := context.Background()
	charge := func(key string) string {
		creds := map[string]string{"api_key": "k"}
		if key != "" {
			creds["__idem"] = key
		}
		r, err := RunPaymentsStub{}.Charge(ctx, creds, "MID-1", 2500, "tok_same")
		if err != nil {
			t.Fatalf("stub charge: %v", err)
		}
		return r.ProcessorRef
	}
	if charge("attempt-a") == charge("attempt-b") {
		t.Fatal("two keys with the same token and amount must give different refs (the cross sale collision)")
	}
	if charge("attempt-a") != charge("attempt-a") {
		t.Fatal("one key must give one ref")
	}
	if e1, e2 := charge(""), charge(""); e1 != e2 || e1 == "" {
		t.Fatalf("an empty key keeps the token, amount and mid fallback: %q vs %q", e1, e2)
	}
	if charge("") == charge("attempt-a") {
		t.Fatal("a keyed ref must differ from the empty key fallback")
	}
}
