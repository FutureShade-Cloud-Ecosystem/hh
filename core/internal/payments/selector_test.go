package payments

// selector_test.go, DIB-416: the fail-closed boot guard, tested at the CONSTRUCTOR.
//
// "At the constructor level" is the requirement and it is the right one. A boot guard proved by
// starting a server is proved by a test that is slow, needs a database, and can only ever exercise
// whichever configuration the harness happens to set. SelectGateway takes a plain struct, so every
// branch, including the ones a running process could never reach without deliberately breaking a
// production box, is one table row here.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestSelector_DefaultsToStub_AndNeverToLive pins the single most important property in this file:
// with nothing configured, the rail cannot move money.
func TestSelector_DefaultsToStub_AndNeverToLive(t *testing.T) {
	gw, err := SelectGateway(GatewayConfig{})
	if err != nil {
		t.Fatalf("an unconfigured rail must select the stub, got error: %v", err)
	}
	if gw.Live() {
		t.Fatal("the DEFAULT gateway reports Live(), an unconfigured box must not be able to charge a real card")
	}
	if gw.Name() != "stub" {
		t.Fatalf("default gateway = %q, want \"stub\"", gw.Name())
	}
}

// TestSelector_LiveModeWithoutCredentials_RefusesToBoot is the named fail-closed boot-guard test.
//
// Each case is a credential removed from an otherwise-complete live configuration, because the
// failure that matters operationally is not "nothing is set" (obvious, caught immediately) but "three
// of the four are set", a half-finished configuration change that would otherwise produce a rail
// claiming to be live while missing the one thing it needs.
func TestSelector_LiveModeWithoutCredentials_RefusesToBoot(t *testing.T) {
	full := GatewayConfig{
		Mode: ModeRun, APIKey: "test-only-not-a-secret", MerchantID: "MID-TEST",
		BaseURL: "http://127.0.0.1:1/never-dialled", WebhookSecret: "test-only-not-a-secret",
	}

	cases := []struct {
		name     string
		mutate   func(*GatewayConfig)
		wantName string
	}{
		{"no api key", func(c *GatewayConfig) { c.APIKey = "" }, EnvGatewayAPIKey},
		{"no merchant id", func(c *GatewayConfig) { c.MerchantID = "" }, EnvGatewayMID},
		{"no base url", func(c *GatewayConfig) { c.BaseURL = "" }, EnvGatewayBaseURL},
		{"no webhook secret", func(c *GatewayConfig) { c.WebhookSecret = "" }, EnvGatewaySecret},
		{"nothing at all", func(c *GatewayConfig) { *c = GatewayConfig{Mode: ModeRun} }, EnvGatewayAPIKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := full
			tc.mutate(&cfg)
			gw, err := SelectGateway(cfg)
			if err == nil {
				t.Fatalf("a live rail missing %s BOOTED (gateway %q), it must fail closed", tc.wantName, gw.Name())
			}
			if gw != nil {
				t.Fatal("a refused selection must return a nil gateway, never a usable fallback: falling back " +
					"to the stub is how payments silently stop being real")
			}
			if !strings.Contains(err.Error(), tc.wantName) {
				t.Fatalf("the error must NAME the missing variable so an operator can fix it: %v", err)
			}
		})
	}
}

// TestSelector_LiveModeWithCredentials_SelectsTheLiveRail is the other half. A guard that refuses
// everything is trivially safe and useless.
func TestSelector_LiveModeWithCredentials_SelectsTheLiveRail(t *testing.T) {
	gw, err := SelectGateway(GatewayConfig{
		Mode: ModeRun, APIKey: "test-only-not-a-secret", MerchantID: "MID-TEST",
		BaseURL: "http://127.0.0.1:1/never-dialled", WebhookSecret: "test-only-not-a-secret",
	})
	if err != nil {
		t.Fatalf("a fully configured live rail must select: %v", err)
	}
	if !gw.Live() || gw.Name() != "run" {
		t.Fatalf("selected gateway = %q live=%v, want the live \"run\" rail", gw.Name(), gw.Live())
	}
	// Constructing it dials nothing. The base URL above would fail loudly if anything tried.
	if _, ok := gw.(*RunGateway); !ok {
		t.Fatalf("live mode selected %T, want *RunGateway", gw)
	}
}

// TestSelector_ProductionRefusesANonLiveGateway is the port doc's promise, finally executable: "the
// boot path must fail closed in prod, refusing to register payment routes unless a LIVE gateway +
// webhook secret resolve".
func TestSelector_ProductionRefusesANonLiveGateway(t *testing.T) {
	for _, mode := range []string{"", ModeStub} {
		gw, err := SelectGateway(GatewayConfig{Mode: mode, Env: "production"})
		if err == nil {
			t.Fatalf("APP_ENV=production booted on gateway %q, a stub in production does not lose money, "+
				"it fails to MOVE it, and nobody finds out until a bank reconciliation", gw.Name())
		}
		if gw != nil {
			t.Fatal("a refused production selection must return nil, not a usable stub")
		}
	}

	// dev and staging are explicitly allowed to run the stub, that is the whole point of having a
	// stub. Asserting it here stops a future tightening from taking every non-prod box down.
	for _, env := range []string{"", "dev", "staging"} {
		if _, err := SelectGateway(GatewayConfig{Env: env}); err != nil {
			t.Fatalf("APP_ENV=%q must be allowed to run the stub: %v", env, err)
		}
	}
}

// TestSelector_UnknownModeRefuses, a typo must not silently downgrade to the stub. It would be safe
// money-wise and wrong operationally: correct-looking behaviour hiding a broken config.
func TestSelector_UnknownModeRefuses(t *testing.T) {
	gw, err := SelectGateway(GatewayConfig{Mode: "runn"})
	if err == nil {
		t.Fatalf("a typo'd mode selected %q instead of refusing", gw.Name())
	}
	if !strings.Contains(err.Error(), "runn") {
		t.Fatalf("the error must echo the bad value so the typo is visible: %v", err)
	}
}

// TestRunGateway_UnverifiedOperationsFailByName pins the deliberate not-implemented surface. A live
// rail that silently pretended to vault a card would be discovered the first time someone charged it.
func TestRunGateway_UnverifiedOperationsFailByName(t *testing.T) {
	g := NewRunGateway("http://127.0.0.1:1/never-dialled", "test-only", "MID-TEST")
	if _, err := g.Vault(t.Context(), VaultRequest{CustomerID: "c", Nonce: "n"}); !errors.Is(err, ErrLiveContractUnverified) {
		t.Fatalf("live Vault error = %v, want ErrLiveContractUnverified", err)
	}
	if err := g.DetachMethod(t.Context(), "tok"); !errors.Is(err, ErrLiveContractUnverified) {
		t.Fatalf("live DetachMethod error = %v, want ErrLiveContractUnverified", err)
	}
}

// TestService_RefusesToChargeWithoutTheIntentGuard pins the optional-seam decision. The seam is
// optional so another swarm's test fake keeps compiling (see intentStore's doc); it must NOT be
// optional in the sense of "works without it".
func TestService_RefusesToChargeWithoutTheIntentGuard(t *testing.T) {
	svc := NewService(guardlessStore{}, NewStubGateway())
	if svc.GuardedByIntent() {
		t.Fatal("a store with no intent support must not report the guard as active")
	}
	_, err := svc.Pay(t.Context(), "cust", PayRequest{InvoiceIDs: []string{"inv"}, Nonce: "n", ExpectedCents: 1})
	if err == nil {
		t.Fatal("a service with no intent guard CHARGED, a payment path with no double-charge guard is " +
			"not a degraded mode, it is the bug DIB-415 exists to close")
	}
	if !strings.Contains(err.Error(), "DIB-415") {
		t.Fatalf("the refusal must name what is missing: %v", err)
	}
}

// guardlessStore implements paymentStore and nothing else, the shape another swarm's test fake has.
type guardlessStore struct{}

func (guardlessStore) ListMethods(context.Context, string) ([]PaymentMethod, error) { return nil, nil }
func (guardlessStore) GetMethod(context.Context, string, string) (PaymentMethod, error) {
	return PaymentMethod{}, nil
}
func (guardlessStore) AddMethod(context.Context, PaymentMethod, bool) (PaymentMethod, error) {
	return PaymentMethod{}, nil
}
func (guardlessStore) RemoveMethod(context.Context, string, string) error      { return nil }
func (guardlessStore) SetDefaultMethod(context.Context, string, string) error  { return nil }
func (guardlessStore) ListPayments(context.Context, string) ([]Payment, error) { return nil, nil }
func (guardlessStore) FindByGatewayRef(context.Context, string, string) (Payment, bool, error) {
	return Payment{}, false, nil
}
func (guardlessStore) PayableInvoices(context.Context, string, []string) ([]Allocation, error) {
	return nil, nil
}
func (guardlessStore) Pay(context.Context, string, string, string, []Allocation) (Payment, error) {
	return Payment{}, nil
}

// TestRepository_CarriesTheIntentGuard is the other half: the PRODUCTION store must always implement
// it. The compile-time assertion in service.go says so; this says so where a reader will look.
func TestRepository_CarriesTheIntentGuard(t *testing.T) {
	if !NewService(NewRepository(nil), NewStubGateway()).GuardedByIntent() {
		t.Fatal("the production Repository no longer satisfies the DIB-415 intent seam, the guard has " +
			"been silently removed from every real payment path")
	}
}
