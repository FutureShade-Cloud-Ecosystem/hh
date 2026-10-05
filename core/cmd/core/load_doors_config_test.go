package main

// The load doors switch (HH-LOAD-1 LD-2, ADR-0024): the doors ship
// switched off, the switch and the machine credential's configuration
// arrive from the environment at spawn, and every incomplete or
// contradictory combination is refused by name, the same fail-closed
// contract loadServeConfig carries for the rest of the serve role.

import (
	"strings"
	"testing"
)

// TestLoadServeConfigDoorsDefaultOff: with no switch set the doors are
// off and nothing about the machine credential is configured; the
// deployment that wants them must say so.
func TestLoadServeConfigDoorsDefaultOff(t *testing.T) {
	env := fullEnv()
	cfg, err := loadServeConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("no switch set: %v", err)
	}
	if cfg.loadDoors {
		t.Fatal("the load doors defaulted on")
	}
	if cfg.machineAudience != "" || cfg.machineJWKSURL != "" {
		t.Fatalf("machine credential read with the doors off: %+v", cfg)
	}
}

// TestLoadServeConfigDoorsOnRequiresTheMachineCredential: switching the
// doors on requires FB Move's machine audience and public key set
// together; each missing half is named, and so is a junk switch value.
func TestLoadServeConfigDoorsOnRequiresTheMachineCredential(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"no machine audience", map[string]string{
			"CORE_LOAD_DOORS": "on", "CORE_MACHINE_JWKS_URL": "https://mover.example.test/jwks",
		}, "CORE_MACHINE_AUDIENCE"},
		{"no machine key set", map[string]string{
			"CORE_LOAD_DOORS": "on", "CORE_MACHINE_AUDIENCE": "hh-machine",
		}, "CORE_MACHINE_JWKS_URL"},
		{"junk switch value", map[string]string{
			"CORE_LOAD_DOORS": "maybe",
		}, "CORE_LOAD_DOORS"},
	} {
		env := fullEnv()
		for k, v := range tc.env {
			env[k] = v
		}
		if _, err := loadServeConfig(func(k string) string { return env[k] }); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: refused wrongly: %v (want %s named)", tc.name, err, tc.want)
		}
	}
}

// TestLoadServeConfigDoorsOnCarriesTheMachineCredential: the switched on
// configuration carries the machine audience and its key set URL, and
// the machine audience must differ from both platform audiences.
func TestLoadServeConfigDoorsOnCarriesTheMachineCredential(t *testing.T) {
	env := fullEnv()
	env["CORE_LOAD_DOORS"] = "on"
	env["CORE_MACHINE_AUDIENCE"] = "hh-machine"
	env["CORE_MACHINE_JWKS_URL"] = "https://mover.example.test/jwks"
	cfg, err := loadServeConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("switched on: %v", err)
	}
	if !cfg.loadDoors || cfg.machineAudience != "hh-machine" || cfg.machineJWKSURL != "https://mover.example.test/jwks" {
		t.Fatalf("switched on configuration read as %+v", cfg)
	}

	for _, clash := range []string{env["CORE_STAFF_AUDIENCE"], env["CORE_TENANT_AUDIENCE"]} {
		env := fullEnv()
		env["CORE_LOAD_DOORS"] = "on"
		env["CORE_MACHINE_AUDIENCE"] = clash
		env["CORE_MACHINE_JWKS_URL"] = "https://mover.example.test/jwks"
		if _, err := loadServeConfig(func(k string) string { return env[k] }); err == nil || !strings.Contains(err.Error(), "machine") {
			t.Fatalf("machine audience equal to a platform audience accepted: %v", err)
		}
	}
}

// TestLoadServeConfigMachineCredentialIgnoredWhenDoorsOff is the ruled
// relaxation: with the doors off, machine credential names in the
// environment are ignored, never a spawn refusal, because the plan's end
// state after the cut over load is doors off and a partial unset must
// never stop a production boot. The configuration carries a warning that
// names all three variables, the caller logs it once, and nothing of the
// machine leg is wired.
func TestLoadServeConfigMachineCredentialIgnoredWhenDoorsOff(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{"both names set", map[string]string{
			"CORE_MACHINE_AUDIENCE": "hh-machine", "CORE_MACHINE_JWKS_URL": "https://mover.example.test/jwks",
		}},
		{"audience alone", map[string]string{
			"CORE_MACHINE_AUDIENCE": "hh-machine",
		}},
		{"key set url alone", map[string]string{
			"CORE_MACHINE_JWKS_URL": "https://mover.example.test/jwks",
		}},
	} {
		env := fullEnv()
		for k, v := range tc.env {
			env[k] = v
		}
		cfg, err := loadServeConfig(func(k string) string { return env[k] })
		if err != nil {
			t.Fatalf("%s: the doors are off, so set names must not stop the boot: %v", tc.name, err)
		}
		if cfg.loadDoors {
			t.Fatalf("%s: the doors read on with no switch", tc.name)
		}
		if cfg.machineAudience != "" || cfg.machineJWKSURL != "" {
			t.Fatalf("%s: the ignored credential leaked into the configuration: %+v", tc.name, cfg)
		}
		warning := cfg.machineCredentialIgnoredWarning()
		if warning == "" {
			t.Fatalf("%s: the ignored credential must carry a warning", tc.name)
		}
		for _, name := range []string{"CORE_LOAD_DOORS", "CORE_MACHINE_AUDIENCE", "CORE_MACHINE_JWKS_URL"} {
			if !strings.Contains(warning, name) {
				t.Fatalf("%s: the warning must name %s: %s", tc.name, name, warning)
			}
		}
	}

	// Nothing set and the doors off: no warning at all.
	env := fullEnv()
	cfg, err := loadServeConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("nothing set: %v", err)
	}
	if cfg.machineCredentialIgnoredWarning() != "" {
		t.Fatalf("a warning with nothing set: %s", cfg.machineCredentialIgnoredWarning())
	}
}
