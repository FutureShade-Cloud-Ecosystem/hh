package main

// DIB-381: /healthz must return 503 when the DB is down (a live ping), with the JSON body shape
// unchanged so the SPA can still read google_sso. These pin all three branches: DB up, DB ping
// error, and no pool (scaffold/offline). No DB required, pingDB is injected.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type healthBody struct {
	Status    string `json:"status"`
	DB        string `json:"db"`
	GoogleSSO bool   `json:"google_sso"`
	BisTrack  string `json:"bistrack"` // DIB-53: the sync module's health token
}

// Carry note (HH-3.2 fold): the pin's google flag parameter is gone; the
// staff Google sign-in surface is the recorded SSO leave-behind, so
// google_sso reads the constant false and the body shape stays additive.
func callHealthz(t *testing.T, pingDB func(context.Context) error) (*httptest.ResponseRecorder, healthBody) {
	t.Helper()
	rr := httptest.NewRecorder()
	// DIB-53: "unconfigured" is what phase 0 always reports (no BisTrack credentials in the test env).
	healthzHandler(pingDB, "unconfigured").ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var b healthBody
	if err := json.Unmarshal(rr.Body.Bytes(), &b); err != nil {
		t.Fatalf("body is not the unchanged JSON shape: %v (raw=%s)", err, rr.Body.String())
	}
	return rr, b
}

func TestHealthz_DBUp_200(t *testing.T) {
	rr, b := callHealthz(t, func(context.Context) error { return nil })
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 when DB is up", rr.Code)
	}
	if b.Status != "ok" || b.DB != "up" || b.GoogleSSO {
		t.Errorf("body = %+v, want {ok up google_sso:false (the leave-behind)}", b)
	}
	// DIB-53: the bistrack key is carried (additive body shape; the SPA can read it alongside the rest).
	if b.BisTrack != "unconfigured" {
		t.Errorf("bistrack = %q, want unconfigured", b.BisTrack)
	}
}

func TestHealthz_DBDown_503_BodyShapeUnchanged(t *testing.T) {
	rr, b := callHealthz(t, func(context.Context) error { return errors.New("connection refused") })
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when the DB ping fails", rr.Code)
	}
	// Body unchanged: still the same JSON keys; db flips to "down"; google_sso still carried.
	if b.Status != "ok" || b.DB != "down" {
		t.Errorf("body = %+v, want status ok / db down (shape unchanged, only the code is 503)", b)
	}
	if b.GoogleSSO {
		t.Errorf("google_sso = true, want false (the leave-behind constant)")
	}
}

func TestHealthz_NilPool_503(t *testing.T) {
	// scaffold/offline mode: no pool wired -> pingDB nil -> DB down -> 503.
	rr, b := callHealthz(t, nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when there is no DB pool", rr.Code)
	}
	if b.DB != "down" {
		t.Errorf("db = %q, want down", b.DB)
	}
	// google_sso is orthogonal to DB health and must still surface (the
	// constant false under the SSO leave-behind, carried in the body).
	if !strings.Contains(rr.Body.String(), `"google_sso":false`) {
		t.Errorf("google_sso not carried in body: %s", rr.Body.String())
	}
}
