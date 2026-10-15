package tenantconfig

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

// The public config shape (the G2 handler half of ADR 0021): exactly the
// six dealer fields of TenantPublicConfig, no brand property, no
// tenant-scoped data, whatever headers the caller sends.
func TestServesPublicShape(t *testing.T) {
	h := New(Default())
	rsp := httptest.NewRecorder()
	h.ServeHTTP(rsp, httptest.NewRequest(http.MethodGet, "/api/v1/tenant/config", nil))
	if rsp.Code != http.StatusOK {
		t.Fatalf("status %d", rsp.Code)
	}
	if ct := rsp.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type %q", ct)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rsp.Body.Bytes(), &raw); err != nil {
		t.Fatalf("body is not a JSON object: %v", err)
	}
	want := map[string]string{
		"id":            "default",
		"dealer_name":   "",
		"logo_url":      "",
		"primary_color": "",
		"support_email": "",
		"support_phone": "",
	}
	if len(raw) != len(want) {
		t.Fatalf("served %d keys, want exactly the six dealer fields: %v", len(raw), raw)
	}
	for k, v := range want {
		got, ok := raw[k]
		if !ok {
			t.Fatalf("missing key %q in %v", k, raw)
		}
		var s string
		if err := json.Unmarshal(got, &s); err != nil || s != v {
			t.Fatalf("key %q: got %s, want %q", k, got, v)
		}
	}
	if _, ok := raw["brand"]; ok {
		t.Fatalf("public config serves a brand property: %v", raw)
	}
	// The generated contract type round-trips the same body.
	var typed apigen.TenantPublicConfig
	if err := json.Unmarshal(rsp.Body.Bytes(), &typed); err != nil {
		t.Fatalf("decode into TenantPublicConfig: %v", err)
	}
	if typed != Default() {
		t.Fatalf("round trip changed the config: %+v", typed)
	}
}

// The default is no dealer's: it carries no dealer name, brand, colour or
// support line, and names no dealer in any field (decision log row 1592).
// A dealer's name and brand reach the route through New(config) from the
// dealer's own configuration, the demo's from the demo seed.
func TestDefaultCarriesNoDealer(t *testing.T) {
	d := Default()
	if d.DealerName != "" || d.LogoUrl != "" || d.PrimaryColor != "" || d.SupportEmail != "" || d.SupportPhone != "" {
		t.Fatalf("the default carries dealer facts: %+v", d)
	}
	for _, v := range []string{d.Id, d.DealerName, d.LogoUrl, d.PrimaryColor, d.SupportEmail, d.SupportPhone} {
		if strings.Contains(strings.ToLower(v), "dibbits") {
			t.Fatalf("the default names a dealer: %+v", d)
		}
	}
}

// The default keeps the recorded capture's shape (core/api/fixtures-live/
// config.json, vendored under D22): the same six keys, so the contract's
// fixture and the route agree on the wire form. The capture's values are
// the ERP antecedent's own dealer and are history; the route no longer
// serves them.
func TestKeepsTheRecordedCaptureShape(t *testing.T) {
	capture, err := os.ReadFile("../../../api/fixtures-live/config.json")
	if err != nil {
		t.Fatalf("read recorded capture: %v", err)
	}
	rsp := httptest.NewRecorder()
	New(Default()).ServeHTTP(rsp, httptest.NewRequest(http.MethodGet, "/api/v1/tenant/config", nil))
	var served, recorded map[string]any
	if err := json.Unmarshal(rsp.Body.Bytes(), &served); err != nil {
		t.Fatalf("served body: %v", err)
	}
	if err := json.Unmarshal(capture, &recorded); err != nil {
		t.Fatalf("recorded capture: %v", err)
	}
	if len(served) != len(recorded) {
		t.Fatalf("served %d keys, capture has %d", len(served), len(recorded))
	}
	for k := range recorded {
		if _, ok := served[k]; !ok {
			t.Fatalf("key %q is in the capture and not served", k)
		}
	}
}

// A constructed config is served verbatim: the handler adds nothing,
// defaults nothing and drops nothing beyond the six schema fields.
func TestServesConstructedConfigVerbatim(t *testing.T) {
	cfg := apigen.TenantPublicConfig{
		Id:           "other",
		DealerName:   "Other Yard",
		LogoUrl:      "https://example.invalid/logo.png",
		PrimaryColor: "#101010",
		SupportEmail: "help@example.invalid",
		SupportPhone: "5550100",
	}
	rsp := httptest.NewRecorder()
	New(cfg).ServeHTTP(rsp, httptest.NewRequest(http.MethodGet, "/api/v1/tenant/config", nil))
	var served apigen.TenantPublicConfig
	if err := json.Unmarshal(rsp.Body.Bytes(), &served); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if served != cfg {
		t.Fatalf("served %+v, want %+v", served, cfg)
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(rsp.Body.Bytes(), &raw)
	if len(raw) != 6 {
		t.Fatalf("served %d keys, want exactly six: %v", len(raw), raw)
	}
}
