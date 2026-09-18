package testkit

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// Reach is what one handler invocation saw: the scope facts the middleware
// stamped, or nothing when the handler is a reached-recorder no gate let
// through. Class is "staff", "tenant" or "none", so a sweep asserts the
// bearer class that reached the handler, the subject, and the org and
// tenant ids resolution produced. IssuedAt and ExpiresAt carry the token
// window the stamped scope held, so the ceiling legs assert the exact
// window the handler saw, never a re-parse of the token (B3 review
// required fix 8).
type Reach struct {
	Class     string
	Subject   string
	OrgSlug   string
	OrgID     string
	TenantID  string
	IssuedAt  time.Time
	ExpiresAt time.Time
	StaffOK   bool
	TenantOK  bool
}

// Recorder is the reached-recorder: the handler shape the lane's route
// shims use (QA finding F1). It records the scope facts of every
// invocation, so the sweeps assert that scope reached the handler, which
// kind, and with which ids, and that no refused request reached it at all.
// It mirrors the B2 recorder in core/internal/platform/authz/mint_test.go
// and exposes the facts as data instead of locked fields.
type Recorder struct {
	mu      sync.Mutex
	reaches []Reach
}

// NewRecorder returns a recorder with no reaches.
func NewRecorder() *Recorder { return &Recorder{} }

// Handler returns the reached-recorder handler: it answers 200 with a
// constant body and records the stamped scope.
func (rec *Recorder) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reach := Reach{Class: "none"}
		if scope, ok := authz.StaffScopeFrom(r.Context()); ok {
			reach = Reach{
				Class:     "staff",
				Subject:   scope.PlatformUserID,
				OrgSlug:   scope.OrgSlug,
				OrgID:     scope.OrgID,
				IssuedAt:  scope.IssuedAt,
				ExpiresAt: scope.ExpiresAt,
				StaffOK:   true,
			}
		} else if scope, ok := authz.TenantScopeFrom(r.Context()); ok {
			reach = Reach{
				Class:     "tenant",
				Subject:   scope.PlatformUserID,
				OrgSlug:   scope.OrgSlug,
				OrgID:     scope.OrgID,
				TenantID:  scope.TenantID,
				IssuedAt:  scope.IssuedAt,
				ExpiresAt: scope.ExpiresAt,
				TenantOK:  true,
			}
		}
		rec.mu.Lock()
		rec.reaches = append(rec.reaches, reach)
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
}

// Count reports how many requests reached the handler.
func (rec *Recorder) Count() int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return len(rec.reaches)
}

// Last returns the most recent reach. It fails the test if nothing
// reached the handler.
func (rec *Recorder) Last(t *testing.T) Reach {
	t.Helper()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.reaches) == 0 {
		t.Fatalf("testkit: the handler was never reached")
	}
	return rec.reaches[len(rec.reaches)-1]
}

// Reset forgets every reach, the per-request pattern of the sweeps.
func (rec *Recorder) Reset() {
	rec.mu.Lock()
	rec.reaches = nil
	rec.mu.Unlock()
}

// AssertNotReached fails the test when any request reached the handler:
// the zero-reach assertion of every refusal leg.
func (rec *Recorder) AssertNotReached(t *testing.T, leg string) {
	t.Helper()
	if n := rec.Count(); n != 0 {
		t.Fatalf("testkit: leg %s: the handler was reached %d times, want 0", leg, n)
	}
}

// AssertStaffReach fails the test unless the last reach was a staff scope
// with the wanted subject and org id.
func (rec *Recorder) AssertStaffReach(t *testing.T, leg, wantSubject, wantOrgID string) {
	t.Helper()
	reach := rec.Last(t)
	if reach.Class != "staff" || !reach.StaffOK {
		t.Fatalf("testkit: leg %s: handler reached as class %s, want staff", leg, reach.Class)
	}
	if reach.Subject != wantSubject {
		t.Fatalf("testkit: leg %s: handler saw subject %s, want %s", leg, reach.Subject, wantSubject)
	}
	if reach.OrgID != wantOrgID {
		t.Fatalf("testkit: leg %s: handler saw org id %s, want %s", leg, reach.OrgID, wantOrgID)
	}
}

// AssertTenantReach fails the test unless the last reach was a tenant
// scope with the wanted subject, org id and tenant id.
func (rec *Recorder) AssertTenantReach(t *testing.T, leg, wantSubject, wantOrgID, wantTenantID string) {
	t.Helper()
	reach := rec.Last(t)
	if reach.Class != "tenant" || !reach.TenantOK {
		t.Fatalf("testkit: leg %s: handler reached as class %s, want tenant", leg, reach.Class)
	}
	if reach.Subject != wantSubject {
		t.Fatalf("testkit: leg %s: handler saw subject %s, want %s", leg, reach.Subject, wantSubject)
	}
	if reach.OrgID != wantOrgID {
		t.Fatalf("testkit: leg %s: handler saw org id %s, want %s", leg, reach.OrgID, wantOrgID)
	}
	if reach.TenantID != wantTenantID {
		t.Fatalf("testkit: leg %s: handler saw tenant id %s, want %s", leg, reach.TenantID, wantTenantID)
	}
}

// Do drives one request at the handler with the given bearer and X-Org
// value. xOrg is variadic so the three shapes are all natural: no header,
// one header, and (with two values) the repeated-header shape leg.
func Do(t *testing.T, handler http.Handler, method, target, bearer string, xOrg ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for _, v := range xOrg {
		req.Header.Add("X-Org", v)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// AssertStatus fails unless the response carries the wanted status.
func AssertStatus(t *testing.T, leg string, got *httptest.ResponseRecorder, want int) {
	t.Helper()
	if got.Code != want {
		t.Fatalf("testkit: leg %s: status %d, want %d, body %s", leg, got.Code, want, got.Body.String())
	}
}

// AssertRefusalBody fails unless the body is exactly the wanted refusal
// body. At worktree HEAD 58f075b the pinned constants live unexported in
// package authz (middleware.go); the sweeps that run inside that package
// pass them directly, and this helper keeps the comparison byte-for-byte
// everywhere else. Backend's in-flight b3-guards exports them unchanged
// (authz.BodyUnauthorized and siblings) without moving a byte, so the
// helper stays correct either way. wantBody is compared as written, so
// the sweep's constant decides the bytes, never this helper.
func AssertRefusalBody(t *testing.T, leg string, got *httptest.ResponseRecorder, wantStatus int, wantBody string) {
	t.Helper()
	AssertStatus(t, leg, got, wantStatus)
	if body := got.Body.String(); body != wantBody {
		t.Fatalf("testkit: leg %s: refusal body %q, want %q", leg, body, wantBody)
	}
}

// AssertOneBodyAcross fails unless every given response carries the same
// status and the byte-identical body: the oracle rule. No refusal may
// reveal which check failed, so the 401 family must be one body across
// the no-token, garbage, wrong-key, unknown-kid, expired and
// alg-confusion variants, and the 403 family one body across its legs.
func AssertOneBodyAcross(t *testing.T, leg string, responses []*httptest.ResponseRecorder) {
	t.Helper()
	if len(responses) == 0 {
		t.Fatalf("testkit: leg %s: no responses to compare", leg)
	}
	status := responses[0].Code
	body := responses[0].Body.String()
	for i, r := range responses[1:] {
		if r.Code != status || r.Body.String() != body {
			t.Fatalf("testkit: leg %s: response %d differs (status %d body %q) from the first (status %d body %q)",
				leg, i+1, r.Code, r.Body.String(), status, body)
		}
	}
	if status < 400 {
		t.Fatalf("testkit: leg %s: compared status %d is not a refusal", leg, status)
	}
}

// FormatLeg builds a leg label for failure messages. Sweeps name their
// legs; this exists so failure output always carries the sweep's own
// vocabulary.
func FormatLeg(sweep, leg string) string { return fmt.Sprintf("%s/%s", sweep, leg) }
