//go:build integration

// dealer_country_integration_test.go, address validation follows the dealer's country through the
// composition root's own adapter and the real customer repository: an Ontario dealer is unchanged
// and refuses a ZIP, a California dealer (country US) takes a San Diego address and refuses a
// Canadian postal code, and the change is live at the next write.
package main

import (
	"context"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

func TestIntegration_CustomerAddressFollowsTheDealersCountry(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "dealercountry")
	reader := settings.NewDealerConfigReader(pool)
	repo := customer.NewRepository(pool)
	svc := customer.NewService(repo).WithWriter(repo).WithCountry(dealerCountryAdapter{r: reader})

	// The seeded dealer is Ontario: a Canadian address lands, a ZIP is refused in Canadian words.
	if _, err := svc.CreateCustomer(ctx, customer.CreateInput{AccountNo: "DC-ON-1", Name: "Quinte Pavers",
		BillingAddress: map[string]any{"province": "on", "postal": "k8v5p7"}}); err != nil {
		t.Fatalf("an Ontario dealer's Canadian address: %v", err)
	}
	if _, err := svc.CreateCustomer(ctx, customer.CreateInput{AccountNo: "DC-ON-2", Name: "Zip Co",
		BillingAddress: map[string]any{"province": "CA", "postal": "92101"}}); err == nil {
		t.Fatal("an Ontario dealer accepted a US address")
	}

	// The dealer becomes the California one; the very next write follows.
	cfg, err := reader.DealerConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Country, cfg.TaxRegion, cfg.Currency, cfg.BusinessZone = "US", "CA", "USD", "America/Los_Angeles"
	if err := reader.WriteDealerConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	acct, err := svc.CreateCustomer(ctx, customer.CreateInput{AccountNo: "DC-CA-1", Name: "Harbor Pavers",
		BillingAddress: map[string]any{"line1": "1 Harbor Dr", "city": "San Diego", "province": "ca", "postal": "92101"}})
	if err != nil {
		t.Fatalf("a California dealer's San Diego address: %v", err)
	}
	var state, zip, country string
	if err := pool.QueryRow(ctx, `SELECT billing_address->>'province', billing_address->>'postal', billing_address->>'country' FROM customers WHERE id = $1`,
		acct.CustomerID).Scan(&state, &zip, &country); err != nil {
		t.Fatal(err)
	}
	if state != "CA" || zip != "92101" || country != "US" {
		t.Fatalf("stored %q %q %q", state, zip, country)
	}
	_, err = svc.CreateCustomer(ctx, customer.CreateInput{AccountNo: "DC-CA-2", Name: "Maple Co",
		BillingAddress: map[string]any{"province": "CA", "postal": "K8V 5P7"}})
	if err == nil || !strings.Contains(err.Error(), "ZIP code") {
		t.Fatalf("a California dealer must refuse a Canadian postal code in ZIP words, got %v", err)
	}
}
