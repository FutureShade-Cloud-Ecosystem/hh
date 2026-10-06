package main

// The load doors' composed wiring proof (HH-LOAD-1 LD-1 round 2, ADR-0024):
// the wrong caller ledger's row 6 end to end through the real startup
// path. With the doors off, a load path answers 404: the route does not
// exist. With CORE_LOAD_DOORS on, the same path answers the machine
// gate's refusals for every wrong caller class, and the one right caller
// reaches the handler's own answers. LD-2's suite covers the gate's
// classes at the seam; this proves the composed wiring serves them on the
// real routes over the real registrar. All principals, slugs and keys are
// fixtures: the machine key material is generated inside the test and
// discarded with the process, and nothing here is a real deployment's
// record.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// fixtureMachineAudience, fixtureMachineKeyID and the fixture machine
// principal ids are invented; the org ids are the composition kit's own
// fixture orgs, planted by the kit itself.
const (
	fixtureMachineAudience = "hh-load-fixture"
	fixtureMachineKeyID    = "fixture-load-machine-rsa"
	fixtureMoverID         = "7c2e6b41-0000-4000-8000-00000000d001"
	fixtureMoverDisabledID = "7c2e6b41-0000-4000-8000-00000000d002"
)

// machinePrincipalPublicKeyPEM renders one RSA public key as PKIX PEM,
// the shape NewMachinePrincipalFromPEM reads.
func machinePrincipalPublicKeyPEM(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal machine public key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: der}))
}

// machineJWKS is the machine key set the fixture's JWKS URL serves.
func machineJWKS(t *testing.T, key *rsa.PrivateKey) map[string]any {
	t.Helper()
	return map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA",
			"kid": fixtureMachineKeyID,
			"use": "sig",
			"alg": "RS256",
			"n":   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
		}},
	}
}

// plantMachinePrincipals plants the fixture's two machine principals
// beside the kit's north org: one enabled mover holding both load doors,
// one disabled. Removed with the test.
func plantMachinePrincipals(t *testing.T, env *compEnv) {
	t.Helper()
	ctx := context.Background()
	_, err := env.fixturePool().Exec(ctx, `
		INSERT INTO machine_principal (id, org_id, name, enabled, doors)
		VALUES ($1::uuid, $2::uuid, 'fixture mover', TRUE, $4::text[]),
		       ($3::uuid, $2::uuid, 'fixture mover parked', FALSE, $4::text[])
		ON CONFLICT (id) DO UPDATE SET enabled = EXCLUDED.enabled, doors = EXCLUDED.doors`,
		fixtureMoverID, testkit.OrgNorthID, fixtureMoverDisabledID,
		[]string{"loadGetBatch", "loadResolveKeys"})
	if err != nil {
		t.Fatalf("plant machine principals: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.fixturePool().Exec(ctx,
			`DELETE FROM machine_principal WHERE id = ANY($1::uuid[])`,
			[]string{fixtureMoverID, fixtureMoverDisabledID})
	})
}

// machineBearer mints FB Move's token shape against the fixture's own
// machine key: the machine audience, the principal as subject, signed by
// the key the fixture's JWKS URL serves. The kit's staff class carries
// the signing path; the audience override names the machine audience,
// which is the class the verifier reads.
func machineBearer(t *testing.T, env *compEnv, key *rsa.PrivateKey, subject string) string {
	t.Helper()
	return env.minter.Mint(t, testkit.StaffClass, subject, testkit.TokenShape{
		KID:      fixtureMachineKeyID,
		Key:      key,
		Audience: fixtureMachineAudience,
	})
}

// serve routes one request at the composed mux and returns the answer.
func serveLoad(t *testing.T, mux *http.ServeMux, method, target, bearer, org string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if org != "" {
		req.Header.Set(authz.HeaderXOrg, org)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestLoadDoorsWrongCallerRow6ThroughTheRealWiring(t *testing.T) {
	ctx := context.Background()

	// Doors off, the default: the routes do not exist, and neither a
	// bearer nor its absence changes that.
	offEnv := newCompEnv(t)
	offRec, _ := registeredSurface(t, offEnv)
	for _, probe := range []struct{ method, target string }{
		{http.MethodGet, "/api/v1/load/batches/b-1"},
		{http.MethodPost, "/api/v1/load/keys/resolve"},
	} {
		rec := serveLoad(t, offRec.inner, probe.method, probe.target, "", "", `{}`)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s with the doors off answered %d, want 404: the route does not exist", probe.method, probe.target, rec.Code)
		}
	}

	// Doors on, with the machine credential's configuration and the
	// fixture principals planted beside the kit's north org.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate machine key: %v", err)
	}
	jwks := machineJWKS(t, key)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(srv.Close)

	onEnv := newCompEnv(t)
	plantMachinePrincipals(t, onEnv)
	onEnv.set("CORE_LOAD_DOORS", "on")
	onEnv.set("CORE_MACHINE_AUDIENCE", fixtureMachineAudience)
	onEnv.set("CORE_MACHINE_JWKS_URL", srv.URL)
	onRec, _ := registeredSurface(t, onEnv)
	mux := onRec.inner

	right := machineBearer(t, onEnv, key, fixtureMoverID)
	disabled := machineBearer(t, onEnv, key, fixtureMoverDisabledID)

	// Row 1: no bearer at all is the gate's 401.
	if rec := serveLoad(t, mux, http.MethodGet, "/api/v1/load/batches/b-1", "", testkit.OrgNorthSlug, ``); rec.Code != http.StatusUnauthorized || rec.Body.String() != string(authz.BodyUnauthorized) {
		t.Fatalf("no bearer: status %d body %q, want the machine gate's 401", rec.Code, rec.Body.String())
	}
	// A malformed bearer is the same 401 family.
	if rec := serveLoad(t, mux, http.MethodGet, "/api/v1/load/batches/b-1", "not-a-token", testkit.OrgNorthSlug, ``); rec.Code != http.StatusUnauthorized {
		t.Fatalf("malformed bearer answered %d, want 401", rec.Code)
	}
	// Row 2: a staff bearer of any tier is 403, the one body.
	staff := onEnv.minter.StaffToken(t, testkit.SubGeneralMgr)
	if rec := serveLoad(t, mux, http.MethodGet, "/api/v1/load/batches/b-1", staff, testkit.OrgNorthSlug, ``); rec.Code != http.StatusForbidden || rec.Body.String() != string(authz.BodyForbidden) {
		t.Fatalf("staff bearer: status %d body %q, want the one 403 body", rec.Code, rec.Body.String())
	}
	// Row 3: a tenant bearer is 403, the same body.
	tenant := onEnv.minter.TenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID)
	if rec := serveLoad(t, mux, http.MethodPost, "/api/v1/load/keys/resolve", tenant, "", `{"source_system":"fixture","items":[{"kind":"probe","legacy_id":"k"}]}`); rec.Code != http.StatusForbidden || rec.Body.String() != string(authz.BodyForbidden) {
		t.Fatalf("tenant bearer: status %d body %q, want the one 403 body", rec.Code, rec.Body.String())
	}
	// Row 4: the right credential naming another org, and a malformed
	// X-Org, are both the one 403 body, never a 500 and never a
	// directory answer.
	for _, org := range []string{testkit.OrgSouthSlug, "not a slug at all", ""} {
		rec := serveLoad(t, mux, http.MethodGet, "/api/v1/load/batches/b-1", right, org, ``)
		if rec.Code != http.StatusForbidden || rec.Body.String() != string(authz.BodyForbidden) {
			t.Fatalf("X-Org %q: status %d body %q, want the one 403 body", org, rec.Code, rec.Body.String())
		}
	}
	// Row 5: a disabled principal is 403, the same body.
	if rec := serveLoad(t, mux, http.MethodGet, "/api/v1/load/batches/b-1", disabled, testkit.OrgNorthSlug, ``); rec.Code != http.StatusForbidden || rec.Body.String() != string(authz.BodyForbidden) {
		t.Fatalf("disabled principal: status %d body %q, want the one 403 body", rec.Code, rec.Body.String())
	}
	// The right caller reaches the handler's own answers: an absent batch
	// is the handler's 404, wearing no gate body.
	rec := serveLoad(t, mux, http.MethodGet, "/api/v1/load/batches/b-absent", right, testkit.OrgNorthSlug, ``)
	if rec.Code != http.StatusNotFound || rec.Body.String() == string(authz.BodyForbidden) {
		t.Fatalf("right caller on an absent batch: status %d body %q, want the handler's 404", rec.Code, rec.Body.String())
	}
	resolve := serveLoad(t, mux, http.MethodPost, "/api/v1/load/keys/resolve", right, testkit.OrgNorthSlug,
		`{"source_system":"fixture","items":[{"kind":"probe","legacy_id":"k-absent"}]}`)
	if resolve.Code != http.StatusOK {
		t.Fatalf("right caller resolve: status %d body %q, want the handler's 200", resolve.Code, resolve.Body.String())
	}
	_ = ctx
}
