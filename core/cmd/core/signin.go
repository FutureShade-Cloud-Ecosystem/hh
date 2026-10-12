package main

// The sign in mode and the session composition: which key source, issuer
// and membership source a copy runs (state note section 6). The mode has no
// default, so every copy's configuration names it before the build that
// requires it ships. The keys arrive as files the platform mounts (the
// private key is never an environment value); the environment carries only
// their paths and the exchange token window.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

const (
	envSigninMode             = "CORE_SIGNIN_MODE"
	envSessionKeyFile         = "CORE_SESSION_SIGNING_KEY_FILE"
	envSessionKeyNextFile     = "CORE_SESSION_KEY_NEXT_FILE"
	envSessionKeyPreviousFile = "CORE_SESSION_KEY_PREVIOUS_FILE"
	envExchangeTokenTTL       = "CORE_EXCHANGE_TOKEN_TTL"
)

// signinMode names the composition a copy runs.
type signinMode string

const (
	// signinConsole: sessions are signed by the deployment's own persistent
	// key and members come from FB Console. The only mode of the default
	// build.
	signinConsole signinMode = "console"
	// signinDemo: the demo build's in memory issuer and flow table
	// membership. It exists only where internal/demo is compiled.
	signinDemo signinMode = "demo"
)

// readSigninMode reads CORE_SIGNIN_MODE. Unset, empty or any other value is
// a refusal; allowDemo is true only in the demo build.
func readSigninMode(getenv func(string) string, allowDemo bool) (signinMode, error) {
	raw := strings.TrimSpace(getenv(envSigninMode))
	switch signinMode(raw) {
	case signinConsole:
		return signinConsole, nil
	case signinDemo:
		if allowDemo {
			return signinDemo, nil
		}
		return "", fmt.Errorf("%s=demo exists only in the demo build; this build accepts %s", envSigninMode, signinConsole)
	case "":
		return "", fmt.Errorf("missing required configuration: sign in mode (%s), there is no default", envSigninMode)
	default:
		return "", fmt.Errorf("%s must be one of the modes this build accepts, got %q", envSigninMode, raw)
	}
}

// sessionKeyConfig is the key file paths and the exchange window, read from
// the environment without touching any file.
type sessionKeyConfig struct {
	files       identity.SessionKeyFiles
	exchangeTTL time.Duration
}

// loadSessionKeyConfig reads the paths and the exchange token window. The
// window is a Go duration, defaults to the knob minimum, and is refused
// above the staff session window and above MinDriverSessionHours hours, so
// a role change to driver can never turn a live token into a 401.
func loadSessionKeyConfig(getenv func(string) string) (sessionKeyConfig, error) {
	cfg := sessionKeyConfig{
		files: identity.SessionKeyFiles{
			Current:  strings.TrimSpace(getenv(envSessionKeyFile)),
			Next:     strings.TrimSpace(getenv(envSessionKeyNextFile)),
			Previous: strings.TrimSpace(getenv(envSessionKeyPreviousFile)),
		},
		exchangeTTL: identity.MinDriverSessionHours * time.Hour,
	}
	if cfg.files.Current == "" {
		return sessionKeyConfig{}, fmt.Errorf("missing required configuration: session signing key file (%s)", envSessionKeyFile)
	}
	if raw := strings.TrimSpace(getenv(envExchangeTokenTTL)); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return sessionKeyConfig{}, fmt.Errorf("%s must be a positive duration, got %q", envExchangeTokenTTL, raw)
		}
		if d > identity.StaffSessionTTL {
			return sessionKeyConfig{}, fmt.Errorf("%s is longer than the staff session window", envExchangeTokenTTL)
		}
		if d > identity.MinDriverSessionHours*time.Hour {
			return sessionKeyConfig{}, fmt.Errorf("%s is longer than the driver knob minimum of %d hours", envExchangeTokenTTL, identity.MinDriverSessionHours)
		}
		cfg.exchangeTTL = d
	}
	return cfg, nil
}

// sessionComposition is what the persistent key source composition hands
// the routes that mount on it (the exchange route is item 6b's): the
// minter, the key set the verifier and the JWK Set route read, and the
// exchange window.
type sessionComposition struct {
	minter      *identity.SessionMinter
	keys        *identity.SessionKeySet
	exchangeTTL time.Duration
}

// loadSessionComposition reads the key files and builds the composition.
// A missing or unparseable current key refuses the boot.
func loadSessionComposition(getenv func(string) string, issuer, staffAudience string) (*sessionComposition, error) {
	cfg, err := loadSessionKeyConfig(getenv)
	if err != nil {
		return nil, err
	}
	if issuer == "" {
		return nil, errors.New("missing required configuration: token issuer (CORE_TOKEN_ISSUER)")
	}
	loaded, err := identity.LoadSessionKeys(cfg.files, issuer, staffAudience)
	if err != nil {
		return nil, err
	}
	return &sessionComposition{minter: loaded.Minter, keys: loaded.Keys, exchangeTTL: cfg.exchangeTTL}, nil
}

// consoleMembership builds the FB Console membership source behind its
// cache, the same pair for the default build and the demo build's console
// mode.
func consoleMembership(endpoint, project, apiKey string) (authz.MembershipSource, error) {
	appwrite, err := authz.NewAppwriteMembershipSource(endpoint, project, apiKey, nil)
	if err != nil {
		return nil, err
	}
	return authz.NewCachedMembership(appwrite, 30*time.Second), nil
}
