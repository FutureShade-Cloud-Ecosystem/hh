package load

// handler_test.go covers the refusals that need no database: a list over
// the bound refuses with 400 before any store call, the machine scope's
// absence refuses with the one 403 body (the gate owns X-Org and stamps
// the scope; a request in here without it is answered as the gate would
// answer it, never a 500), and the resolve payload carries hh_id null
// for an unrecorded name. The store is built with a nil pool on purpose:
// if any of these reached the pool the test would answer a 500 and fail,
// which is the assertion's teeth.

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// testScopeOrg is the fixture machine scope's org uuid, invented.
const testScopeOrg = "5e1f0000-0000-4000-8000-1f00000000e1"

func boundBody(n int) []byte {
	var b bytes.Buffer
	b.WriteString(`{"source_system":"testsuite","items":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"kind":"probe","legacy_id":"key-` + strconv.Itoa(i) + `"}`)
	}
	b.WriteString(`]}`)
	return b.Bytes()
}

func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.DiscardHandler)
}

// withScope stamps the machine scope a request carries past the gate.
func withScope(r *http.Request) *http.Request {
	return r.WithContext(authz.WithMachineScope(r.Context(), authz.MachineScope{
		PrincipalID: "5e1f0000-0000-4000-8000-1f00000000e2",
		OrgID:       testScopeOrg,
		OrgSlug:     "fixture-load",
	}))
}

func postResolve(t *testing.T, h *Handler, scoped bool, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/load/keys/resolve", bytes.NewReader(body))
	if scoped {
		req = withScope(req)
	}
	rec := httptest.NewRecorder()
	h.resolveKeys(rec, req)
	return rec
}

func TestResolveWireCarriesNullHHIDForUnrecordedKeys(t *testing.T) {
	out := ResolveResult{
		Items: []Resolution{
			{Kind: "probe", LegacyID: "k-absent"},
			{Kind: "probe", LegacyID: "k-present", HHID: "5e1f0000-0000-4000-8000-1f00000000f1"},
		},
		Counts: map[string]int{"found": 1, "missing": 1},
	}
	raw, err := json.Marshal(resolveResultWire(out))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"hh_id":null`) {
		t.Fatalf("the unrecorded key's hh_id is not in the payload as null: %s", raw)
	}
	if !strings.Contains(string(raw), `"hh_id":"5e1f0000-0000-4000-8000-1f00000000f1"`) {
		t.Fatalf("the recorded key's hh_id is not in the payload: %s", raw)
	}
}

func TestResolveKeysRefusesABodyOverTheBound(t *testing.T) {
	h := NewHandler(NewStore(nil), testLogger(t))
	rec := postResolve(t, h, true, boundBody(MaxBatchItems+1))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a resolve body over the bound answered %d, want 400", rec.Code)
	}
}

func TestResolveKeysRefusesAnEmptyList(t *testing.T) {
	h := NewHandler(NewStore(nil), testLogger(t))
	rec := postResolve(t, h, true, []byte(`{"source_system":"testsuite","items":[]}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an empty resolve list answered %d, want 400", rec.Code)
	}
}

func TestResolveKeysRefusesAMissingScope(t *testing.T) {
	h := NewHandler(NewStore(nil), testLogger(t))
	rec := postResolve(t, h, false, boundBody(1))
	if rec.Code != http.StatusForbidden || rec.Body.String() != string(authz.BodyForbidden) {
		t.Fatalf("a resolve with no machine scope answered %d %q, want the one 403 body", rec.Code, rec.Body.String())
	}
}

func TestGetBatchRefusesAMissingScope(t *testing.T) {
	h := NewHandler(NewStore(nil), testLogger(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/load/batches/b-1", nil)
	rec := httptest.NewRecorder()
	h.getBatch(rec, req)
	if rec.Code != http.StatusForbidden || rec.Body.String() != string(authz.BodyForbidden) {
		t.Fatalf("a batch read with no machine scope answered %d %q, want the one 403 body", rec.Code, rec.Body.String())
	}
}

// TestResolveKeysRefusesAnOverlongKind pins the resolve body's own bound:
// a kind over the contract's 64 refuses with 400 at the boundary.
func TestResolveKeysRefusesAnOverlongKind(t *testing.T) {
	h := NewHandler(NewStore(nil), testLogger(t))
	long := strings.Repeat("k", 65)
	rec := postResolve(t, h, true, []byte(`{"source_system":"testsuite","items":[{"kind":"`+long+`","legacy_id":"k"}]}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an overlong kind answered %d, want 400", rec.Code)
	}
}
