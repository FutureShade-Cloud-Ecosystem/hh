package load

// handler_test.go covers the two refusals that need no database: a list
// over the bound refuses with 400 before any store call, and a call that
// names no org refuses with 400. The store is built with a nil pool on
// purpose: if either refusal reached the pool the test would answer a
// 500 and fail, which is the assertion's teeth.

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

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

func postResolve(t *testing.T, h *Handler, org string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/load/keys/resolve", bytes.NewReader(body))
	if org != "" {
		req.Header.Set("X-Org", org)
	}
	rec := httptest.NewRecorder()
	h.resolveKeys(rec, req)
	return rec
}

// TestResolveWireCarriesNullHHIDForUnrecordedKeys pins the contract's
// shape: hh_id is required and nullable on LoadKeyResolution, so an
// unrecorded name answers hh_id present as null in the payload, never an
// omitted key.
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
	rec := postResolve(t, h, "org-x", boundBody(MaxBatchItems+1))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a resolve body over the bound answered %d, want 400", rec.Code)
	}
}

func TestResolveKeysRefusesAnEmptyList(t *testing.T) {
	h := NewHandler(NewStore(nil), testLogger(t))
	rec := postResolve(t, h, "org-x", []byte(`{"source_system":"testsuite","items":[]}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an empty resolve list answered %d, want 400", rec.Code)
	}
}

func TestResolveKeysRefusesAMissingOrg(t *testing.T) {
	h := NewHandler(NewStore(nil), testLogger(t))
	rec := postResolve(t, h, "", boundBody(1))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a resolve with no X-Org answered %d, want 400", rec.Code)
	}
}

func TestGetBatchRefusesAMissingOrg(t *testing.T) {
	h := NewHandler(NewStore(nil), testLogger(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/load/batches/b-1", nil)
	rec := httptest.NewRecorder()
	h.getBatch(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a batch read with no X-Org answered %d, want 400", rec.Code)
	}
}
