package payment

// selector_test.go, DIB-416: the POS card rail's fail-closed boot guard, tested at the CONSTRUCTOR.
// The portal rail's twin is internal/payments/selector_test.go; the two rails are deliberately
// independent, so each owns its own guard and its own evidence.

import (
	"strings"
	"testing"
)

// TestCardSelector_DefaultsToTheSimulatedRail, the unconfigured counter cannot charge a real card.
func TestCardSelector_DefaultsToTheSimulatedRail(t *testing.T) {
	p, err := SelectCardProcessor(CardRailConfig{})
	if err != nil {
		t.Fatalf("an unconfigured card rail must select the stub: %v", err)
	}
	if _, ok := p.(RunPaymentsStub); !ok {
		t.Fatalf("default card processor = %T, want RunPaymentsStub", p)
	}
	// The flag the POS reads to label the reader honestly follows from the same value.
	if !NewService(nil, p).Simulated() {
		t.Fatal("the default rail must report Simulated(), the POS labels the card reader off this flag")
	}
}

// TestCardSelector_LiveModeWithoutHost_RefusesToBoot is the named fail-closed boot-guard test for the
// card rail. Its credentials live sealed in payment_config (0027), so the thing that CAN be missing at
// boot is the host, and a live rail with nowhere to send a charge must not start.
func TestCardSelector_LiveModeWithoutHost_RefusesToBoot(t *testing.T) {
	p, err := SelectCardProcessor(CardRailConfig{Mode: CardModeRun})
	if err == nil {
		t.Fatalf("a live card rail with no %s BOOTED (%T), it must fail closed", EnvCardBaseURL, p)
	}
	if p != nil {
		t.Fatal("a refused selection must return nil, never a silent fallback to the stub: a counter that " +
			"thinks it is live while simulating is a counter that prints receipts for money it never took")
	}
	if !strings.Contains(err.Error(), EnvCardBaseURL) {
		t.Fatalf("the error must NAME the missing variable: %v", err)
	}
}

// TestCardSelector_LiveModeWithHost_SelectsTheLiveClient, the guard must still let a correct
// configuration through.
func TestCardSelector_LiveModeWithHost_SelectsTheLiveClient(t *testing.T) {
	p, err := SelectCardProcessor(CardRailConfig{Mode: CardModeRun, BaseURL: "http://127.0.0.1:1/never-dialled"})
	if err != nil {
		t.Fatalf("a configured live card rail must select: %v", err)
	}
	if _, ok := p.(*RunPaymentsClient); !ok {
		t.Fatalf("live card processor = %T, want *RunPaymentsClient", p)
	}
	if NewService(nil, p).Simulated() {
		t.Fatal("after the flip the rail must NOT report Simulated(), the POS would keep labelling live charges as test mode")
	}
}

// TestCardSelector_ProductionRefusesTheSimulatedRail, the counter equivalent of the portal rail's
// production guard.
func TestCardSelector_ProductionRefusesTheSimulatedRail(t *testing.T) {
	for _, mode := range []string{"", CardModeStub} {
		p, err := SelectCardProcessor(CardRailConfig{Mode: mode, Env: "production"})
		if err == nil {
			t.Fatalf("APP_ENV=production booted the simulated card rail (%T)", p)
		}
		if p != nil {
			t.Fatal("a refused production selection must return nil")
		}
	}
	for _, env := range []string{"", "dev", "staging"} {
		if _, err := SelectCardProcessor(CardRailConfig{Env: env}); err != nil {
			t.Fatalf("APP_ENV=%q must be allowed to run the simulated rail: %v", env, err)
		}
	}
}

// TestCardSelector_UnknownModeRefuses, a typo must be visible, not absorbed.
func TestCardSelector_UnknownModeRefuses(t *testing.T) {
	if p, err := SelectCardProcessor(CardRailConfig{Mode: "live"}); err == nil {
		t.Fatalf("a typo'd mode selected %T instead of refusing", p)
	} else if !strings.Contains(err.Error(), "live") {
		t.Fatalf("the error must echo the bad value: %v", err)
	}
}
