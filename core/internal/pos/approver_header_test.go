package pos

// approver_header_test.go, the never-logged proof for the approver's
// credential (row 482, section 3.2; rebuilt per the lead's P2-1 ruling).
// Three properties, each pointed at a hole the review's plants fell
// through:
//
// The capture sees STRUCTURED ATTRIBUTES, not only message text: the usual
// way this codebase logs is a field, so a plant logging the header as an
// attribute must fail these tests too (the review's Plant A stayed green
// under the old message-only capture).
//
// Every test sweeps for the EXACT credential value that test sent: the
// approved path sweeps the real manager token it carried, the refused path
// the token it carried, the garbage path the canary. A test that sweeps a
// string its request never carried cannot fail, and proved nothing.
//
// The capture is attached to EVERY logger on the request path: the
// handler's injected logger AND the audit writer's (the recorder is the
// real audit service over a pool-less repository, whose logger is the
// capture). The guards and the authz gate log nothing at all, so there is
// no third logger to attach; a logger added to either package will be
// covered by re-pointing its constructor here.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// capturingHandler keeps every record it is given, message and structured
// attributes together, rendered into one searchable line.
type capturingHandler struct {
	mu      sync.Mutex
	records []string
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value.Resolve())
		return true
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, b.String())
	return nil
}

func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler      { return h }

func (h *capturingHandler) all() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.records...)
}

// subjectAuthorities resolves a fixed authority per platform subject, so
// one harness can carry a clerk and a manager as two different people.
type subjectAuthorities struct {
	bySubject map[string]identity.StaffAuthority
}

func (s subjectAuthorities) ResolveStaff(_ context.Context, subject, _ string) (identity.StaffAuthority, bool, error) {
	a, ok := s.bySubject[subject]
	return a, ok, nil
}

// portalNever is a PortalRoleSource the staff-only surface never consults.
type portalNever struct{}

func (portalNever) PortalRole(context.Context, string, string) (string, bool, error) {
	return "", false, nil
}

// the garbage path's credential: a canary, never a real token.
const credentialCanary = "Bearer canary-credential-8f3a1c4b"

// headerSweepFixture wires the real handler, real guards and the service
// behind them, with every request-path logger pointed at one capture.
type headerSweepFixture struct {
	name    string
	mux     *http.ServeMux
	capture *capturingHandler
	logger  *slog.Logger
	svc     *Service
	harness *testkit.Harness
}

func newHeaderSweep(t *testing.T) *headerSweepFixture {
	t.Helper()
	h := newHarness()
	seedSaleWorth(h, 30000, 0)
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("roleconfig.Default: %v", err)
	}
	harness := testkit.NewHarness(t)
	// The demo's two people: the clerk inside Sales, the approver the
	// Sales manager, both granted the sale's branch.
	auths := subjectAuthorities{bySubject: map[string]identity.StaffAuthority{
		"fx-clerk-sub": {
			RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
			Branches: []string{"branch-trt"},
		},
		"fx-approver-sub": {
			RoleID: "sales_manager", Tier: identity.TierManager, Scope: identity.ScopeBranch,
			Branches: []string{"branch-trt"},
		},
	}}
	g, err := guards.NewGuards(harness.Gate, auths, portalNever{}, nil)
	if err != nil {
		t.Fatalf("NewGuards: %v", err)
	}
	harness.Membership.SetMember("fx-clerk-sub", testkit.OrgNorthSlug, true)
	harness.Membership.SetMember("fx-approver-sub", testkit.OrgNorthSlug, true)
	harness.Resolver.Set(testkit.OrgNorthSlug, testkit.OrgNorthID)

	// Every logger on the request path writes into the one capture: the
	// handler's logger and the audit writer's (the recorder is the real
	// audit service over a pool-less repository; its writer logs there).
	capture := &capturingHandler{}
	logger := slog.New(capture)
	h.svc = h.svc.WithRoleConfig(cfg).
		WithApproverSource(g).
		WithClock(harness.Clock.Now).
		WithAudit(audit.NewService(audit.NewRepository(nil), logger))

	handler := NewHandler(h.svc, logger)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, g)
	return &headerSweepFixture{name: "sweep", mux: mux, capture: capture, logger: logger, svc: h.svc, harness: harness}
}

func (f *headerSweepFixture) postReturn(t *testing.T, bearer, approverHeader string) *httptest.ResponseRecorder {
	t.Helper()
	in := wholeReturnInput()
	in.IdempotencyKey = "sweep-key-" + f.name
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pos/returns", bytes.NewReader(body))
	req.Header.Set("Authorization", bearer)
	req.Header.Set("X-Org", testkit.OrgNorthSlug)
	req.Header.Set(approverAuthorizationHeader, approverHeader)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

// assertNeverLogged sweeps every captured record, message and attributes
// together, for the EXACT credential value this test's request carried,
// and the response body beside them.
func (f *headerSweepFixture) assertNeverLogged(t *testing.T, rec *httptest.ResponseRecorder, credential string) {
	t.Helper()
	if strings.Contains(rec.Body.String(), credential) {
		t.Fatal("the credential must never be echoed into a response body")
	}
	for _, msg := range f.capture.all() {
		if strings.Contains(msg, credential) {
			t.Fatalf("the credential reached a log record: %q", msg)
		}
	}
}

func TestApproverHeader_NeverLogged_OnTheApprovedPath(t *testing.T) {
	f := newHeaderSweep(t)
	bearer := "Bearer " + f.harness.Minter.StaffToken(t, "fx-clerk-sub")
	approver := "Bearer " + f.harness.Minter.StaffToken(t, "fx-approver-sub")

	rec := f.postReturn(t, bearer, approver)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the manager's own bearer must commit the return, got %d: %s", rec.Code, rec.Body.String())
	}
	// The proof is live: the capture must take records WITH structured
	// attributes, not only bare messages.
	f.logger.Info("control record", "marker", "capture-is-live")
	found := false
	for _, msg := range f.capture.all() {
		if strings.Contains(msg, "marker=capture-is-live") {
			found = true
		}
	}
	if !found {
		t.Fatal("the capture must be proven to take structured attributes, not vacuously green")
	}
	f.assertNeverLogged(t, rec, approver)
}

func TestApproverHeader_NeverLogged_OnTheRefusedPath(t *testing.T) {
	f := newHeaderSweep(t)
	bearer := "Bearer " + f.harness.Minter.StaffToken(t, "fx-clerk-sub")
	// A second clerk credential: verifies, resolves, but names the same
	// person as the caller, so the standing refusal stays. The sweep runs
	// against the value this request actually carried.
	samePerson := "Bearer " + f.harness.Minter.StaffToken(t, "fx-clerk-sub")

	rec := f.postReturn(t, bearer, samePerson)
	if rec.Code != http.StatusConflict {
		t.Fatalf("the same person's credential cannot approve, got %d: %s", rec.Code, rec.Body.String())
	}
	f.assertNeverLogged(t, rec, samePerson)
}

func TestApproverHeader_NeverLogged_WhenTheCredentialIsGarbage(t *testing.T) {
	f := newHeaderSweep(t)
	bearer := "Bearer " + f.harness.Minter.StaffToken(t, "fx-clerk-sub")

	rec := f.postReturn(t, bearer, credentialCanary)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("garbage is refused on the credential family, got %d", rec.Code)
	}
	f.assertNeverLogged(t, rec, credentialCanary)
}
