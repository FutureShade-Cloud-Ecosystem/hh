//go:build demo

package main

// The persona route's composition (item 7c, state note section 5): the
// copy's declared caller rule, the pinned partner list and the handler,
// built only in the demo build's console mode. The default build and the
// demo mode never compile or call any of it into a route.

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/persona"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/ratelimit"
)

const (
	envPersonaCaller   = "CORE_PERSONA_CALLER"
	envPersonaPartners = "CORE_PERSONA_PARTNERS"
)

// personaConfig is the persona route's environment: the caller rule, the
// pinned partners (Console user ids; empty refuses every caller) and the
// trusted proxies the address limits count behind.
type personaConfig struct {
	mode     persona.CallerMode
	partners []string
	trusted  ratelimit.TrustedProxies
}

// loadPersonaConfig reads the caller rule, which has no default: a copy
// that does not say who may enter as a persona does not boot. No refusal
// echoes a configured value.
func loadPersonaConfig(getenv func(string) string) (personaConfig, error) {
	var cfg personaConfig
	switch persona.CallerMode(strings.TrimSpace(getenv(envPersonaCaller))) {
	case persona.CallerPartner:
		cfg.mode = persona.CallerPartner
	case persona.CallerOpen:
		cfg.mode = persona.CallerOpen
	case "":
		return personaConfig{}, fmt.Errorf("missing required configuration: persona caller rule (%s), there is no default", envPersonaCaller)
	default:
		return personaConfig{}, fmt.Errorf("%s must be partner or open", envPersonaCaller)
	}
	cfg.partners = persona.ParsePartners(getenv(envPersonaPartners))
	if cfg.mode == persona.CallerOpen && len(cfg.partners) > 0 {
		return personaConfig{}, fmt.Errorf("%s is set on an open copy, which reads no bearer: unset it", envPersonaPartners)
	}
	for _, id := range cfg.partners {
		if !authz.ValidConsoleID(id) {
			return personaConfig{}, fmt.Errorf("%s holds an entry that is not an FB Console user id", envPersonaPartners)
		}
	}
	trusted, err := ratelimit.ParseTrustedProxies(getenv(envTrustedProxies))
	if err != nil {
		return personaConfig{}, fmt.Errorf("%s must be a comma list of addresses", envTrustedProxies)
	}
	cfg.trusted = trusted
	return cfg, nil
}

// personaDeps is what the demo composition hands the persona handler.
type personaDeps struct {
	cfg     personaConfig
	log     *slog.Logger
	verify  *identity.Verifier
	session *sessionComposition
	orgs    authz.OrgResolver
	staff   *identity.StaffAuthorityResolver
	knob    identity.DriverSessionHours
	audit   *audit.Service
}

// buildPersona constructs the handler over the deployment's own minter, the
// verifier the gate verifies with, and the real staff reads.
func buildPersona(d personaDeps) (*persona.Handler, error) {
	if d.session == nil {
		return nil, errors.New("persona: the persistent session composition is required")
	}
	cfg := persona.Config{
		Mode: d.cfg.mode, Partners: d.cfg.partners, Staff: d.staff, Orgs: d.orgs, Knob: d.knob,
		Audit: d.audit, Minter: d.session.minter, Trusted: d.cfg.trusted, Log: d.log,
	}
	if d.cfg.mode == persona.CallerPartner {
		cfg.Verifier = d.verify
	}
	return persona.New(cfg)
}
