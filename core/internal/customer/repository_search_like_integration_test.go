//go:build integration

// repository_search_like_integration_test.go, the customer account search against real Postgres.
//
// SearchAccountsScoped binds the typed text into two ILIKE patterns (name and account number), and a
// bound pattern is still a pattern: before the escape a typed '%' or '_' acted as a wildcard and the
// search listed accounts that carry none of the text. The contract under test: the search finds the
// account whose name or account number literally carries the text, and no account that only fits it
// as a wildcard.
//
//	Run: DATABASE_URL=... go test -tags integration ./internal/customer/ -run TestIntegration_SearchAccounts_TextIsLiteral -count=1
package customer

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestIntegration_SearchAccounts_TextIsLiteral(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	scope := SearchScope{Global: true}

	cases := []struct {
		name    string
		term    string
		literal string
		decoy   string
	}{
		{"a percent sign in the term is text, not any-run",
			"CSLK-%s 50%%", "CSLK-%s 50%% Co", "CSLK-%s 501 Co"},
		{"an underscore in the term is text, not any-one",
			"CSLK-%s A_1", "CSLK-%s A_1 Co", "CSLK-%s AB1 Co"},
		{"the escape character in the term is text too",
			`CSLK-%s Slab\2`, `CSLK-%s Slab\2 Co`, "CSLK-%s Slab2 Co"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tag := lookupLikeNonce()
			term := fmt.Sprintf(tc.term, tag)
			litName := fmt.Sprintf(tc.literal, tag)
			litID := plantLookupAccount(t, pool, lookupLikeNonce()+"-L", litName)
			plantLookupAccount(t, pool, lookupLikeNonce()+"-D", fmt.Sprintf(tc.decoy, tag))
			got, err := repo.SearchAccountsScoped(ctx, term, 50, scope)
			if err != nil {
				t.Fatalf("search %q: %v", term, err)
			}
			if len(got) != 1 || got[0].ID != litID {
				t.Fatalf("search %q returned %d rows, want only the literal account %q: %+v", term, len(got), litName, got)
			}
			for _, r := range got {
				if !strings.Contains(strings.ToLower(r.Name), strings.ToLower(term)) {
					t.Fatalf("search %q returned a row containing none of it: name=%q", term, r.Name)
				}
			}
		})
	}

	t.Run("the account number arm is literal too", func(t *testing.T) {
		tag := lookupLikeNonce()
		litID := plantLookupAccount(t, pool, "CSAN-"+tag+"-5_1", "CSLK-"+tag+" Number Co")
		plantLookupAccount(t, pool, "CSAN-"+tag+"-5X1", "CSLK-"+tag+" Number Decoy Co")
		got, err := repo.SearchAccountsScoped(ctx, "CSAN-"+tag+"-5_1", 50, scope)
		if err != nil {
			t.Fatalf("search by account number: %v", err)
		}
		if len(got) != 1 || got[0].ID != litID {
			t.Fatalf("account number search returned %d rows, want only the literal one: %+v", len(got), got)
		}
	})
}
