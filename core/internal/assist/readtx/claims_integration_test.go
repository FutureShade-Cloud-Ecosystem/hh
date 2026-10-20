//go:build integration

package readtx

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/redact"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// testKey writes a signing key into ai.claims_key as the superuser and removes
// it when the test ends, under the id the signer derives from the key. A key
// already provisioned on the gate database is never disturbed.
func testKey(t *testing.T, conn *pgx.Conn) (id int, secret string) {
	t.Helper()
	secret = randomHex(t, 24)
	id = int(KeyID(redact.NewSecret(secret)))
	if _, err := conn.Exec(ctxT(t), `INSERT INTO ai.claims_key (key_id, key) VALUES ($1, $2)`, id, secret); err != nil {
		t.Fatalf("insert test key: %v", err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(ctxT(t), conn.Config().ConnString())
		if err != nil {
			return
		}
		defer c.Close(ctxT(t))
		_, _ = c.Exec(ctxT(t), `DELETE FROM ai.claims_key WHERE key_id = $1`, id)
	})
	return id, secret
}

// rawSigned builds a payload by hand, independent of the package's own signer,
// so the verifier is held to the wire format and not to a shared bug.
func rawSigned(keyID int, secret string, payload map[string]any) string {
	body, _ := json.Marshal(payload)
	b64 := base64.StdEncoding.EncodeToString(body)
	signed := fmt.Sprintf("v1.%d.%s", keyID, b64)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(signed))
	return signed + "." + hex.EncodeToString(m.Sum(nil))
}

func backendPID(t *testing.T, conn *pgx.Conn) int {
	t.Helper()
	var pid int
	if err := conn.QueryRow(ctxT(t), `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	return pid
}

func goodPayload(pid int) map[string]any {
	return map[string]any{
		"audience":   "staff",
		"asker_id":   "11111111-1111-1111-1111-111111111111",
		"global":     false,
		"branch_ids": []string{"22222222-2222-2222-2222-222222222222"},
		"pid":        pid,
		"exp":        time.Now().Add(time.Minute).Unix(),
		"nonce":      "n1",
	}
}

// verified sets the claims setting on the session and asks the verifier; the
// bool is true when it returned claims.
func verified(t *testing.T, conn *pgx.Conn, value string) (ok bool, asker string) {
	t.Helper()
	if _, err := conn.Exec(ctxT(t), `SELECT set_config('ai.claims', $1, false)`, value); err != nil {
		t.Fatal(err)
	}
	var got *string
	if err := conn.QueryRow(ctxT(t), `SELECT (ai.claims()).asker_id`).Scan(&got); err != nil {
		t.Fatalf("ai.claims() must never raise, got %v", err)
	}
	if got == nil {
		return false, ""
	}
	return true, *got
}

func TestVerifierPositiveControlAndEveryForgery(t *testing.T) {
	conn := connect(t, gateURL(t))
	kid, secret := testKey(t, conn)
	pid := backendPID(t, conn)

	if ok, _ := verified(t, conn, ""); ok {
		t.Fatal("no setting must verify to NULL")
	}
	good := rawSigned(kid, secret, goodPayload(pid))
	ok, asker := verified(t, conn, good)
	if !ok || asker != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("a correctly signed claim for this pid must return the claims (ok=%v asker=%q)", ok, asker)
	}
	var branches []string
	var global bool
	if err := conn.QueryRow(ctxT(t), `SELECT (ai.claims()).branch_ids::text[], (ai.claims()).global`).Scan(&branches, &global); err != nil {
		t.Fatal(err)
	}
	if len(branches) != 1 || branches[0] != "22222222-2222-2222-2222-222222222222" || global {
		t.Errorf("claims fields wrong: branches=%v global=%v", branches, global)
	}

	parts := strings.Split(good, ".")
	forgeries := map[string]string{
		"forged mac":      strings.Join([]string{parts[0], parts[1], parts[2], strings.Repeat("0", 64)}, "."),
		"empty mac":       strings.Join([]string{parts[0], parts[1], parts[2], ""}, "."),
		"wrong key id":    rawSigned(kid+1000, secret, goodPayload(pid)),
		"wrong key":       rawSigned(kid, secret+"x", goodPayload(pid)),
		"expired":         rawSigned(kid, secret, func() map[string]any { p := goodPayload(pid); p["exp"] = time.Now().Add(-time.Minute).Unix(); return p }()),
		"other pid":       rawSigned(kid, secret, goodPayload(pid+1)),
		"no pid":          rawSigned(kid, secret, func() map[string]any { p := goodPayload(pid); delete(p, "pid"); return p }()),
		"no expiry":       rawSigned(kid, secret, func() map[string]any { p := goodPayload(pid); delete(p, "exp"); return p }()),
		"bad audience":    rawSigned(kid, secret, func() map[string]any { p := goodPayload(pid); p["audience"] = "admin"; return p }()),
		"no asker":        rawSigned(kid, secret, func() map[string]any { p := goodPayload(pid); delete(p, "asker_id"); return p }()),
		"not base64":      fmt.Sprintf("v1.%d.!!!.%s", kid, strings.Repeat("a", 64)),
		"not a version":   strings.Replace(good, "v1.", "v2.", 1),
		"three parts":     strings.Join(parts[:3], "."),
		"five parts":      good + ".x",
		"key id not int":  strings.Replace(good, fmt.Sprintf("v1.%d.", kid), "v1.zz.", 1),
		"garbage":         "not a claim at all",
		"unquoted branch": rawSigned(kid, secret, func() map[string]any { p := goodPayload(pid); p["branch_ids"] = []string{"not-a-uuid"}; return p }()),
	}
	// An edited payload that keeps the old MAC.
	edited := goodPayload(pid)
	edited["global"] = true
	eb, _ := json.Marshal(edited)
	forgeries["edited payload, old mac"] = strings.Join([]string{parts[0], parts[1], base64.StdEncoding.EncodeToString(eb), parts[3]}, ".")
	for name, value := range forgeries {
		if ok, _ := verified(t, conn, value); ok {
			t.Errorf("%s verified, it must return NULL", name)
		}
	}
	// The positive control still holds after all of that.
	if ok, _ := verified(t, conn, good); !ok {
		t.Fatal("the good claim stopped verifying")
	}
}

func TestVerifierAcceptsOnlyTheCurrentAndPreviousKey(t *testing.T) {
	conn := connect(t, gateURL(t))
	k1, s1 := testKey(t, conn)
	k2, s2 := testKey(t, conn)
	k3, s3 := testKey(t, conn)
	pid := backendPID(t, conn)
	for _, c := range []struct {
		name   string
		id     int
		secret string
		want   bool
	}{{"oldest", k1, s1, false}, {"previous", k2, s2, true}, {"current", k3, s3, true}} {
		if ok, _ := verified(t, conn, rawSigned(c.id, c.secret, goodPayload(pid))); ok != c.want {
			t.Errorf("%s key verified = %v, want %v", c.name, ok, c.want)
		}
	}
}

func TestVerifierAndKeyAreOwnedByTheClaimsOwnerAndBuiltHard(t *testing.T) {
	conn := connect(t, gateURL(t))
	roles := RolesFor(currentDatabase(t, conn))
	var owner, volatility, parallel string
	var secdef bool
	var config []string
	if err := conn.QueryRow(ctxT(t), `SELECT r.rolname, p.prosecdef, p.provolatile::text, p.proparallel::text, coalesce(p.proconfig, '{}')
		FROM pg_proc p JOIN pg_roles r ON r.oid = p.proowner WHERE p.oid = 'ai.claims()'::regprocedure`).
		Scan(&owner, &secdef, &volatility, &parallel, &config); err != nil {
		t.Fatalf("ai.claims(): %v", err)
	}
	if owner != roles.ClaimsOwner || !secdef || volatility != "s" || parallel != "s" {
		t.Errorf("ai.claims() owner=%s secdef=%v volatility=%s parallel=%s", owner, secdef, volatility, parallel)
	}
	if len(config) != 1 || config[0] != "search_path=pg_catalog, pg_temp" {
		t.Errorf("search_path setting = %v, want pg_catalog, pg_temp", config)
	}
	for _, rel := range []string{"ai.claims_key", "ai.claim_set"} {
		var o string
		if err := conn.QueryRow(ctxT(t), `SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid = c.relowner WHERE c.oid = $1::regclass`, rel).Scan(&o); err != nil || o != roles.ClaimsOwner {
			t.Errorf("%s owner = %q (err %v), want %s", rel, o, err, roles.ClaimsOwner)
		}
	}
	// EXECUTE on ai.claims(): the two readers and the app role, nobody else.
	for _, r := range []string{roles.Reader, roles.ProReader} {
		var can bool
		if err := conn.QueryRow(ctxT(t), `SELECT has_function_privilege($1, 'ai.claims()', 'EXECUTE')`, r).Scan(&can); err != nil || !can {
			t.Errorf("%s must execute ai.claims() (can=%v err=%v)", r, can, err)
		}
	}
	for _, r := range []string{roles.ViewOwner, roles.ClaimsOwner} {
		var can bool
		if err := conn.QueryRow(ctxT(t), `SELECT has_function_privilege($1, 'ai.claims()', 'EXECUTE')`, r).Scan(&can); err != nil {
			t.Fatal(err)
		}
		if r == roles.ViewOwner && can {
			t.Errorf("%s must not execute ai.claims() directly", r)
		}
	}
	var public bool
	if err := conn.QueryRow(ctxT(t), `SELECT has_function_privilege('public', 'ai.claims()', 'EXECUTE')`).Scan(&public); err != nil || public {
		t.Errorf("PUBLIC can execute ai.claims() (public=%v err=%v)", public, err)
	}
}

// P2-1 of the PR 536 read: expiry is checked against the clock, not the
// transaction's start, so one long transaction cannot keep an expired claim alive.
func TestClaimExpiresInsideOneTransaction(t *testing.T) {
	conn := connect(t, gateURL(t))
	kid, secret := testKey(t, conn)
	pid := backendPID(t, conn)
	tx, err := conn.Begin(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctxT(t))
	p := goodPayload(pid)
	p["exp"] = time.Now().Add(1 * time.Second).Unix()
	if _, err := tx.Exec(ctxT(t), `SELECT set_config('ai.claims', $1, true)`, rawSigned(kid, secret, p)); err != nil {
		t.Fatal(err)
	}
	read := func() *string {
		var a *string
		if err := tx.QueryRow(ctxT(t), `SELECT (ai.claims()).asker_id`).Scan(&a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	if read() == nil {
		t.Fatal("a fresh short lived claim must verify")
	}
	time.Sleep(2500 * time.Millisecond)
	if a := read(); a != nil {
		t.Fatalf("the claim verified %q in the same transaction after its expiry", *a)
	}
}

// Slice 6a: the claim carries the asker's tier. A claim signed with one reads it back; a claim signed
// without one (an old payload) still verifies and reads as no tier, so every tier floor refuses it.
func TestClaimTierRoundTripsAndAnOldClaimReadsAsNoTier(t *testing.T) {
	conn := connect(t, gateURL(t))
	kid, secret := testKey(t, conn)
	pid := backendPID(t, conn)
	read := func(payload map[string]any) (asker *string, tier *int) {
		if _, err := conn.Exec(ctxT(t), `SELECT set_config('ai.claims', $1, false)`, rawSigned(kid, secret, payload)); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRow(ctxT(t), `SELECT (ai.claims()).asker_id, (ai.claims()).tier`).Scan(&asker, &tier); err != nil {
			t.Fatalf("the verifier or the tier attribute: %v", err)
		}
		return
	}
	with := goodPayload(pid)
	with["tier"] = 2
	if a, tier := read(with); a == nil || tier == nil || *tier != 2 {
		t.Errorf("a claim with a tier: asker=%v tier=%v", a, tier)
	}
	if a, tier := read(goodPayload(pid)); a == nil || tier != nil {
		t.Errorf("an old claim without a tier: asker=%v tier=%v, want it to verify with no tier", a, tier)
	}
}
