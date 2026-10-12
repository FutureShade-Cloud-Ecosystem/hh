//go:build integration

// register_search_like_integration_test.go, the invoice register's search term against real Postgres.
//
// registerWhere binds the typed term into five ILIKE arms, and a bound pattern is still a pattern:
// before the escape a typed '%' or '_' acted as a wildcard and the register listed documents that
// carry none of the text. The contract under test: the term matches as text, so the literal
// document is listed, its wildcard decoy is not, and the count agrees with the list.
//
//	Run: DATABASE_URL=... go test -tags integration ./internal/billing/ -run TestRegister_SearchTermIsLiteral -count=1
package billing

import (
	"context"
	"testing"
)

func TestRegister_SearchTermIsLiteral(t *testing.T) {
	f := newD510Fixture(t)
	cases := []struct {
		name    string
		literal string
		decoy   string
	}{
		{"a percent sign in the term is text, not any-run", "50%", "501"},
		{"an underscore in the term is text, not any-one", "A_1", "AB1"},
		{"the escape character in the term is text too", `Slab\2`, "Slab2"},
	}
	svc := f.service(d510Manager())
	ctx := context.Background()
	for _, tc := range cases {
		litID := f.invoice(t, tc.literal, "FINALIZED", 10, 100000, 100000, "INVOICE")
		f.invoice(t, tc.decoy, "FINALIZED", 10, 100000, 100000, "INVOICE")
		t.Run(tc.name, func(t *testing.T) {
			term := f.tag + "-" + tc.literal
			page, err := svc.Register(ctx, RegisterFilter{Search: term, Limit: 50})
			if err != nil {
				t.Fatalf("search %q: %v", term, err)
			}
			if len(page.Items) != 1 || page.Items[0].ID != litID {
				t.Fatalf("search %q listed %d documents, want only the literal one: %+v", term, len(page.Items), page.Items)
			}
			if page.Total != 1 {
				t.Fatalf("search %q total = %d, want 1", term, page.Total)
			}
		})
	}
}
