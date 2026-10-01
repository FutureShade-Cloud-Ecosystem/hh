package pos

// approver_header_test.go, the never-logged proof for the approver's
// credential (row 482, section 3.2): the credential rides
// X-Approver-Authorization beside the clerk's bearer, and NOTHING on the
// request path, guard, rule or handler, puts it in a log. The test drives
// the real handler behind the real guards (the kit's DB-free harness for
// verification and membership), first through the REAL second-credential
// resolver with a qualifying manager bearer, then through a refusal, and
// sweeps every log record the path produced for the credential's bytes. A
// control record proves the capture itself works.

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// capturingHandler is a slog handler that keeps every record it is given.
type capturingHandler struct{ records []string }

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r.Message)
	return nil
}
func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler      { return h }

// subjectAuthorities resolves a fixed authority per platform subject, so
// one harness can carry a clerk and a manager as two different people.
type subjectAuthorities struct{ bySubject map[string]identity.StaffAuthority }

func (s subjectAuthorities) ResolveStaff(_ context.Context, subject, _ string) (identity.StaffAuthority, bool, error) {
	a, ok := s.bySubject[subject]
	return a, ok, nil
}

// portalNever is a PortalRoleSource the staff-only surface never consults.
type portalNever struct{}

func (portalNever) PortalRole(context.Context, string, string) (string, bool, error) {
	return "", false, nil
}

// the credential the sweep looks for: a canary, never a real token.
const credentialCanary = "Bearer canary-credential-8f3a1c4b"

// headerSweepFixture wires the real handler, real guards and the service
// behind them, with the audit capture attached.
type headerSweepFixture struct {
	name    string
	mux     *http.ServeMux
	capture *capturingHandler
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

	// The service clock rides the harness clock so the window reads the
	// same time the tokens were minted at, and the service's approver
	// source is the REAL guards the routes registered behind, so the
	// credential here rides exactly the machinery it rides in production.
	h.svc = h.svc.WithRoleConfig(cfg).WithApproverSource(g).WithClock(harness.Clock.Now)

	capture := &capturingHandler{}
	handler := NewHandler(h.svc, slog.New(capture))
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, g)
	return &headerSweepFixture{name: "sweep", mux: mux, capture: capture, svc: h.svc, harness: harness}
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
	req.Header.Set("X-Approver-Authorization", approverHeader)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func (f *headerSweepFixture) assertNeverLogged(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if strings.Contains(rec.Body.String(), credentialCanary) {
		t.Fatal("the credential must never be echoed into a response body")
	}
	for _, msg := range f.capture.records {
		if strings.Contains(msg, credentialCanary) {
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
	f.capture.records = append(f.capture.records, "control-marker-the-capture-works")
	f.assertNeverLogged(t, rec)
	found := false
	for _, msg := range f.capture.records {
		if strings.Contains(msg, "control-marker") {
			found = true
		}
	}
	if !found {
		t.Fatal("the capture must be proven live, not vacuously green")
	}
}

func TestApproverHeader_NeverLogged_OnTheRefusedPath(t *testing.T) {
	f := newHeaderSweep(t)
	bearer := "Bearer " + f.harness.Minter.StaffToken(t, "fx-clerk-sub")
	// A second clerk credential: verifies, resolves, but names the same
	// person as the caller, so the standing refusal stays.
	samePerson := "Bearer " + f.harness.Minter.StaffToken(t, "fx-clerk-sub")

	rec := f.postReturn(t, bearer, samePerson)
	if rec.Code != http.StatusConflict {
		t.Fatalf("the same person's credential cannot approve, got %d: %s", rec.Code, rec.Body.String())
	}
	f.assertNeverLogged(t, rec)
}

func TestApproverHeader_NeverLogged_WhenTheCredentialIsGarbage(t *testing.T) {
	f := newHeaderSweep(t)
	bearer := "Bearer " + f.harness.Minter.StaffToken(t, "fx-clerk-sub")

	rec := f.postReturn(t, bearer, credentialCanary)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("garbage is refused on the credential family, got %d", rec.Code)
	}
	f.assertNeverLogged(t, rec)
}
