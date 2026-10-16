package settings

// The dealer's legacy import source: an optional system_settings key that names the legacy system a
// dealer migrates from. Absent or blank means none. The surface config carries it so a surface draws
// the legacy import check only for a dealer that has one.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeLegacy struct {
	src string
	err error
}

func (f fakeLegacy) LegacyImportSource(context.Context) (string, error) { return f.src, f.err }

func TestConfig_ServesTheLegacyImportSource(t *testing.T) {
	svc := kitSvc(&fakeKitStore{profile: "manager"}, kitAdmin()).WithLegacyImport(fakeLegacy{src: "bistrack"})
	cfg, err := svc.Config(context.Background(), "money-desk", "")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if cfg.LegacyImportSource != "bistrack" {
		t.Fatalf("a dealer with a source must serve it, got %q", cfg.LegacyImportSource)
	}
	raw, _ := json.Marshal(cfg)
	if !strings.Contains(string(raw), `"legacy_import_source":"bistrack"`) {
		t.Fatalf("the wire field is legacy_import_source: %s", raw)
	}
}

func TestConfig_NoLegacyImportSourceLeavesTheFieldOut(t *testing.T) {
	for name, svc := range map[string]*Service{
		"no source wired": kitSvc(&fakeKitStore{profile: "manager"}, kitAdmin()),
		"blank source":    kitSvc(&fakeKitStore{profile: "manager"}, kitAdmin()).WithLegacyImport(fakeLegacy{}),
	} {
		cfg, err := svc.Config(context.Background(), "money-desk", "")
		if err != nil {
			t.Fatalf("%s: config: %v", name, err)
		}
		raw, _ := json.Marshal(cfg)
		if strings.Contains(string(raw), "legacy_import_source") {
			t.Fatalf("%s: a dealer with no source must not serve one: %s", name, raw)
		}
	}
}

func TestConfig_ALegacyImportReadThatFailsRefusesTheRead(t *testing.T) {
	svc := kitSvc(&fakeKitStore{profile: "manager"}, kitAdmin()).WithLegacyImport(fakeLegacy{err: errors.New("down")})
	if _, err := svc.Config(context.Background(), "money-desk", ""); err == nil {
		t.Fatal("a failed source read must refuse the config, never answer a guess")
	}
}

func TestParseLegacyImportSource(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		set  bool
		want string
		bad  bool
	}{
		{"absent", "", false, "", false},
		{"word", `"bistrack"`, true, "bistrack", false},
		{"padded", `"  bistrack "`, true, "bistrack", false},
		{"blank", `""`, true, "", false},
		{"upper case is malformed", `"BisTrack"`, true, "", true},
		{"not a string", `42`, true, "", true},
		{"spaces inside", `"bis track"`, true, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var raw json.RawMessage
			if c.set {
				raw = json.RawMessage(c.raw)
			}
			bad := false
			got := parseLegacyImportSource(raw, func() { bad = true })
			if got != c.want || bad != c.bad {
				t.Fatalf("got %q bad=%v, want %q bad=%v", got, bad, c.want, c.bad)
			}
		})
	}
}
