// Package config centralizes all environment configuration for HardscapeOS.
// Every value has a sane dev default; production overrides via env vars.
//
// The carried configuration is trimmed by the carry itself: the HS256 world
// this package used to configure (PORTAL_JWT_SECRET and STAFF_JWT_SECRET and
// their dev defaults and validation blocks, and the Google staff sign-in
// flags with theirs) is superseded. Staff and portal bearers verify through
// JWKS (the core's serve role reads its own verifier inputs from the
// environment), and the Google staff sign-in surface is deliberately left
// behind, folded into the open sign-in methods item traveling to the next
// spine stage. The two ERP consumers of the staff secret (the messaging
// stream-ticket HMAC and the Google issuer handoff) never read this
// package in the target: the ticket key arrives as its own dedicated
// environment input when messaging carries, and the sign-in handoff does
// not carry.
package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
)

// EnvDemo is the APP_ENV a DEMO BOX declares, and it is a value this program had no need of until
// something genuinely dangerous had to be confined to one machine. It sits beside dev / staging /
// production as a fourth environment rather than as a mode of one of them, because the demo droplet
// site is exactly that: its own Caddy site, its own database, its own env file, sharing nothing with
// staging but a kernel. A demo reset must never be able to reach a tester's homework.
//
// IT IS NOT THE FLAG. DEMO_MODE is (see Config.DemoMode). This constant is the SECOND of the two
// independent facts a box must state before the credential bypass exists on it, and the two are
// deliberately different KINDS of fact: DEMO_MODE is an explicit opt-in that says "yes, I mean it",
// and APP_ENV=demo is the box saying what it IS. Requiring both means neither a stray DEMO_MODE=1
// in a shared env file nor a mis-set APP_ENV can, on its own, open the door.
const EnvDemo = "demo"

type Config struct {
	Port        int
	DatabaseURL string

	// NATSURL has no consumer in this tree: the event bus the ERP reserved
	// it for is not carried (the transactional outbox is the write seam),
	// and nothing reads NATS_URL. Kept because the trim is exactly the two
	// secrets; its disposition is a recorded finding for the wiring batch.
	NATSURL string
	// JWKSURL has no consumer in this tree either: the serve role reads its
	// own verifier inputs from the environment (CORE_JWKS_URL), so this
	// value would be a second, unused spelling of that input. Kept because
	// the trim is exactly the two secrets; disposition likewise recorded.
	JWKSURL string
	Env     string // dev | staging | production

	// PaymentVaultKey is the AES-256-GCM key (64 hex chars / 32 bytes) that seals
	// the RUN-PAY processor credentials at rest (W4). Empty → the vault generates an
	// ephemeral key (dev/test ONLY); a non-dev env MUST set a stable 32-byte key, or
	// credentials would be sealed under a key lost on restart.
	PaymentVaultKey string

	// PortalBaseURL is the contractor portal entry that invite/reset emails link to (F0.2).
	// The portal shell reads ?token= → accept-invite and ?reset_token= → reset-confirm, so
	// any path is fine; production overrides to the public origin (e.g. https://pro.dibbits.ca).
	PortalBaseURL string

	// Plane feedback bridge (#16 / IMP-038): staff feedback submitted in the ERP is
	// filed as an issue on the Dibbits Plane board. All three must be set for the
	// /api/v1/feedback route to be wired; otherwise the module stays dark (dev default).
	// The key is server-side ONLY, it must never reach the frontend bundle.
	PlaneAPIURL    string // e.g. https://pm.futurebuild.ai/api/v1
	PlaneAPIKey    string
	PlaneProject   string // Plane project id (workspace slug is part of the URL)
	PlaneWorkspace string // Plane workspace slug, e.g. futurebuild

	// StaffBaseURL is the ERP SPA origin used to build admin-generated password-reset
	// links (DIB-186): "<StaffBaseURL>/?reset_token=<raw>". No secret; the raw token is
	// appended by the identity service. Dev default is the Vite dev origin.
	StaffBaseURL string

	// DemoMode wires the CREDENTIAL BYPASS in internal/demo: one route that hands a caller a real,
	// fully-authorized staff session for a named demo FLOW, with no password presented at all. Said
	// plainly because it deserves to be said plainly, a route that mints sessions for free is
	// precisely what an attacker wants, and the only thing that makes it acceptable is that it cannot
	// exist anywhere but a demo box.
	//
	// OFF BY DEFAULT, AND EXPLICIT. The default is false and the env var is DEMO_MODE, it is NOT
	// derived from "APP_ENV != production", and that distinction is the whole point of having a flag
	// at all. Staging runs a non-prod env, staging is where real people do real homework against real
	// shaped data, and staging must never carry this route. An "is it prod?" test would have handed
	// it to them.
	//
	// TWO FACTS, NOT ONE. DemoEnabled(), the thing main.go actually gates registration on, is true
	// only when this flag is set AND the box has declared itself a demo box (APP_ENV=demo, or dev for
	// the local preview stack). Validate() refuses to BOOT a non-demo, non-dev box that has this set,
	// so the misconfiguration is a loud crash at start rather than a quiet bypass nobody notices.
	//
	// Only values strconv.ParseBool recognises turn it on (1, t, T, TRUE, true, True). Anything
	// else, including "yes" and "on", reads as FALSE: this is the one flag where an unparseable value
	// must fail towards the closed door, and the composition root logs which way it went so an
	// operator who typed "yes" finds out at boot instead of at a 404.
	DemoMode bool

	// BisTrack live-sync credentials (M9 / DIB-53). BOTH must be set for the sync module to be
	// considered configured; otherwise it stays idle and healthz reports "unconfigured". Phase 0 makes
	// NO live connection regardless, these gate the future worker, not this scaffold. The endpoint is
	// the BisTrack transport target (base URL or DSN, the wire format is decided at the first live
	// connection) and the key is its secret, server-side ONLY (never in the frontend bundle).
	BisTrackEndpoint string
	BisTrackAPIKey   string
}

// BisTrackConfigured reports whether BisTrack sync credentials are present (both endpoint + key). It
// gates the future sync worker; it does NOT enable any live connection in phase 0 (the module is
// structurally unable to sync regardless). False → healthz reports "unconfigured".
func (c Config) BisTrackConfigured() bool {
	return c.BisTrackEndpoint != "" && c.BisTrackAPIKey != ""
}

// DemoEnabled reports whether the internal/demo credential bypass may be registered. This is the
// predicate the composition root gates on, and it is deliberately STRICTER than the flag alone: the
// route exists only where DEMO_MODE is set AND the environment is one that a demo may live in.
//
// IT IS A SECOND LOCK ON THE SAME DOOR, and it is not redundant with Validate(). Validate refuses to
// boot a staging or production box that carries DEMO_MODE, which is the loud failure an operator
// wants. But a predicate that answers "no" independently means that even if Validate were skipped,
// reordered, or its error swallowed by some future edit to main(), the route still does not get
// registered on a box that is not a demo box. Belt and braces on the one route in this program that
// gives away sessions.
func (c Config) DemoEnabled() bool {
	return c.DemoMode && (c.Env == "dev" || c.Env == EnvDemo)
}

func Load() Config {
	return Config{
		Port:            intEnv("PORT", 8080),
		DatabaseURL:     strEnv("DATABASE_URL", "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"),
		NATSURL:         strEnv("NATS_URL", ""),
		JWKSURL:         strEnv("JWKS_URL", ""),
		Env:             strEnv("APP_ENV", "dev"),
		PaymentVaultKey: strEnv("PAYMENT_VAULT_KEY", ""),
		PortalBaseURL:   strEnv("PORTAL_BASE_URL", "http://localhost:5173/portal.html"),
		PlaneAPIURL:     strEnv("PLANE_API_URL", ""),
		PlaneAPIKey:     strEnv("PLANE_API_KEY", ""),
		PlaneProject:    strEnv("PLANE_PROJECT_ID", ""),
		PlaneWorkspace:  strEnv("PLANE_WORKSPACE_SLUG", ""),
		StaffBaseURL:    strEnv("ERP_BASE_URL", "http://localhost:5173"),
		DemoMode:        boolEnv("DEMO_MODE", false),

		BisTrackEndpoint: strEnv("BISTRACK_ENDPOINT", ""),
		BisTrackAPIKey:   strEnv("BISTRACK_API_KEY", ""),
	}
}

// Validate fails closed on insecure configuration. Call it at startup and treat
// any error as fatal. The dev defaults are accepted ONLY when APP_ENV=dev (or
// unset → "dev"); every other environment (staging, production, typos, unset-to-
// something-else) must supply the values the remaining checks demand. (The
// two signing-secret clauses this function used to carry left with the HS256
// world they served: token verification is no longer this package's to
// configure.)
func (c Config) Validate() error {
	if c.Env == "dev" {
		return nil
	}
	// THE CREDENTIAL BYPASS IS CHECKED FIRST, because it is the only misconfiguration in this
	// function that hands out authority rather than merely weakening a key. DEMO_MODE is admitted on
	// exactly two kinds of box: a developer's laptop (APP_ENV=dev, which returned above) and a box
	// that has declared itself a demo (APP_ENV=demo). Every other value, staging, production, prod,
	// a typo, unset, is a refusal to boot.
	//
	// A CRASH IS THE POINT. The alternative, logging a warning and carrying on, is how a staging box
	// ends up quietly serving password-free sessions for a week because one line scrolled past in a
	// deploy log. Staging is where real people do real homework against real shaped data; there is no
	// reading of "DEMO_MODE on staging" that is anything but a mistake, so it stops the process.
	if c.DemoMode && c.Env != EnvDemo {
		return fmt.Errorf("DEMO_MODE is set but APP_ENV=%q: the demo sign-in is a credential bypass and may run only on a demo box (APP_ENV=%q) or in dev", c.Env, EnvDemo)
	}
	// RUN-PAY vault key: fail closed outside dev. A wrong-length key must not boot a
	// non-dev box silently encrypting credentials with an ephemeral key it loses on restart.
	if b, err := hex.DecodeString(c.PaymentVaultKey); err != nil || len(b) != 32 {
		return fmt.Errorf("PAYMENT_VAULT_KEY must be 64 hex chars (32 bytes) when APP_ENV=%q", c.Env)
	}
	return nil
}

func strEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// boolEnv reads a boolean env var, FAILING CLOSED to def on anything strconv.ParseBool does not
// recognise. Unset, empty and unparseable are all the default, so DEMO_MODE=yes leaves the demo
// route unregistered rather than half-opening it on a spelling. The composition root logs which way
// every gated feature went at boot, which is where an operator who typed "yes" learns that it read
// as no.
func boolEnv(k string, def bool) bool {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func intEnv(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
