//go:build integration

// account_lookup_like_integration_test.go, the account resolver's name arms against real Postgres.
//
// The resolver binds the typed reference into two ILIKE patterns, and a bound pattern is still a
// pattern: before the escape a typed '%' or '_' acted as a wildcard inside them, and because the
// name arms are LIMIT 1 the wildcard resolved to whichever fitting account sorted first, not to the
// account the person meant. At the counter a resolved account is where a sale or a payment lands,
// so the contract under test is the resolver's own: a wildcard reference resolves to NOT FOUND
// unless an account literally carrying that text exists, and a literal match still resolves, to the
// account that carries it and never to a neighbour a wildcard would have reached.
//
//	Run: DATABASE_URL=... go test -tags integration ./internal/customer/ -run TestIntegration_LookupAccount_RefIsLiteral -count=1
package customer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// lookupLikeNonce is 8 hex characters of entropy with no clock in it, enough to keep this file's
// terms off every other fixture's rows on the shared gate database.
func lookupLikeNonce() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail in practice; a fixed nonce fails loudly on the account_no
		// UNIQUE index rather than silently sharing a term with another run.
		return "fixed"
	}
	return hex.EncodeToString(b[:])
}

// plantLookupAccount plants one account with no home branch, company-wide by construction and so
// visible to every reach, and returns its id. Removed at teardown; the account carries no
// transactions, so nothing else can be perturbed by it existing briefly.
func plantLookupAccount(t *testing.T, pool *pgxpool.Pool, acct, name string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ($1, $2, 'CONTRACTOR', 30, 100000) RETURNING id::text`, acct, name).Scan(&id); err != nil {
		t.Fatalf("plant account %q: %v", name, err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1::uuid`, id) })
	return id
}

// TestIntegration_LookupAccount_RefIsLiteral, the three metacharacter classes a bound pattern can
// misread, each in both shapes: with no literal match in the table (the reference must resolve to
// not found, the wildcard must not adopt a decoy that fits it) and with a literal match planted
// beside the decoy (the reference must resolve to the literal account, never to the decoy).
//
// Every term carries a fresh nonce and so does every planted name, so on the unescaped query the
// only rows a term's wildcard can reach are this test's own. The decoy's name starts "AAA" and the
// literal's "ZZZ" so that when both fit the pattern and ORDER BY name LIMIT 1 decides, the decoy
// deterministically wins: that is the wrong-account resolution this test exists to close.
func TestIntegration_LookupAccount_RefIsLiteral(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	reach := AccountReach{Global: true}

	notFound := []struct {
		name  string
		term  string
		decoy string
	}{
		{"a percent sign with no literal match resolves to not found",
			"LKLK-%s 50%%", "AAA LKLK-%s 501 Co"},
		{"an underscore with no literal match resolves to not found",
			"LKLK-%s A_1", "AAA LKLK-%s AB1 Co"},
		{"the escape character with no literal match resolves to not found",
			`LKLK-%s Slab\2`, "AAA LKLK-%s Slab2 Co"},
	}
	for _, tc := range notFound {
		t.Run(tc.name, func(t *testing.T) {
			tag := lookupLikeNonce()
			term := fmt.Sprintf(tc.term, tag)
			decoy := fmt.Sprintf(tc.decoy, tag)
			plantLookupAccount(t, pool, lookupLikeNonce()+"-D", decoy)
			if _, found, err := repo.LookupAccountScoped(ctx, term, reach); err != nil {
				t.Fatalf("lookup %q: %v", term, err)
			} else if found {
				t.Fatalf("lookup %q resolved to an account, want not found: no account carries the "+
					"reference as text, only the decoy %q fits it as a wildcard", term, decoy)
			}
		})
	}

	wrongAccount := []struct {
		name    string
		term    string
		literal string
		decoy   string
	}{
		{"a percent sign with a literal match resolves to that account, not the decoy",
			"LKLK-%s 50%%", "ZZZ LKLK-%s 50%% Co", "AAA LKLK-%s 501 Co"},
		{"an underscore with a literal match resolves to that account, not the decoy",
			"LKLK-%s A_1", "ZZZ LKLK-%s A_1 Co", "AAA LKLK-%s AB1 Co"},
	}
	for _, tc := range wrongAccount {
		t.Run(tc.name, func(t *testing.T) {
			tag := lookupLikeNonce()
			term := fmt.Sprintf(tc.term, tag)
			literal := fmt.Sprintf(tc.literal, tag)
			litID := plantLookupAccount(t, pool, lookupLikeNonce()+"-L", literal)
			plantLookupAccount(t, pool, lookupLikeNonce()+"-W", fmt.Sprintf(tc.decoy, tag))
			got, found, err := repo.LookupAccountScoped(ctx, term, reach)
			if err != nil {
				t.Fatalf("lookup %q: %v", term, err)
			}
			if !found {
				t.Fatalf("lookup %q did not resolve, want the account whose name carries it as text", term)
			}
			if got.ID != litID {
				t.Fatalf("lookup %q resolved to %q (%q), want the literal account %q: a wildcard "+
					"reference must never put money on a different account", term, got.ID, got.Name, literal)
			}
		})
	}

	t.Run("a reference with no metacharacter still resolves", func(t *testing.T) {
		tag := lookupLikeNonce()
		litID := plantLookupAccount(t, pool, lookupLikeNonce()+"-P", "ZZZ LKLK-"+tag+" Northgate Co")
		if got, found, err := repo.LookupAccountScoped(ctx, "LKLK-"+tag+" Northgate", reach); err != nil || !found {
			t.Fatalf("found=%v err=%v, a plain prefix reference must keep resolving", found, err)
		} else if got.ID != litID {
			t.Fatalf("resolved to %q, want %q", got.ID, litID)
		}
	})
}
