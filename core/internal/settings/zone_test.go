package settings

// The surface config read carries the dealer's business day zone, so the web apps name the dealer's
// day instead of compiling one in (the Pacific dealer's 21:30 would otherwise be tomorrow in Toronto
// terms). An absent source answers nothing, and a source answers its zone byte for byte.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeZone struct {
	zone string
	err  error
}

func (f fakeZone) BusinessZone(context.Context) (string, error) { return f.zone, f.err }

func TestConfig_ServesTheDealersBusinessZone(t *testing.T) {
	svc := kitSvc(&fakeKitStore{profile: "manager"}, kitAdmin()).WithZone(fakeZone{zone: "America/Los_Angeles"})
	cfg, err := svc.Config(context.Background(), "drive-run", "")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if cfg.BusinessZone != "America/Los_Angeles" {
		t.Fatalf("the Pacific dealer's config must carry its zone, got %q", cfg.BusinessZone)
	}
	raw, _ := json.Marshal(cfg)
	if !strings.Contains(string(raw), `"business_zone":"America/Los_Angeles"`) {
		t.Fatalf("the wire field is business_zone: %s", raw)
	}
}

func TestConfig_NoZoneSourceLeavesTheFieldOut(t *testing.T) {
	cfg, err := kitSvc(&fakeKitStore{profile: "manager"}, kitAdmin()).Config(context.Background(), "drive-run", "")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	raw, _ := json.Marshal(cfg)
	if strings.Contains(string(raw), "business_zone") {
		t.Fatalf("a kit with no zone source must not invent one: %s", raw)
	}
}

func TestConfig_AZoneReadThatFailsRefusesTheRead(t *testing.T) {
	svc := kitSvc(&fakeKitStore{profile: "manager"}, kitAdmin()).WithZone(fakeZone{err: errors.New("down")})
	if _, err := svc.Config(context.Background(), "drive-run", ""); err == nil {
		t.Fatal("a failed zone read must refuse the config, never answer a guessed calendar")
	}
}
