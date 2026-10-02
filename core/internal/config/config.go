// Package config centralizes all environment configuration for HardscapeOS.
// Dev has sane defaults for local work; staging, production and demo must say what they mean
// (see Validate), and APP_ENV itself must be set for any role that serves traffic.
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
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode"
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
	Env     string // dev | staging | production | demo

	// EnvUnset records that APP_ENV was not set at all, so Env holds the "dev" default and
	// nobody said so. The one-shot tools (migrate, seed, demoseed) never call Validate and keep
	// reading the default as dev; the roles that serve traffic or send messages refuse it in
	// Validate, because a production box with the variable forgotten would otherwise skip every
	// check below.
	EnvUnset bool

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

// DevDatabaseURLDefault is the single literal behind Load's DATABASE_URL
// default. The one-shot roles' guarded default (cmd/core's
// resolveOneshotDatabaseURL) reads the same constant, so the two
// spellings cannot drift: a second, slightly different localhost default
// is exactly the two-names defect class HH-199 closed.
const DevDatabaseURLDefault = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"

func Load() Config {
	env, envSet := os.LookupEnv("APP_ENV")
	envUnset := !envSet || env == ""
	if envUnset {
		env = "dev"
	}
	// The link bases default to the Vite dev origin ONLY where a localhost link is the right
	// answer (dev, and the demo box, which has never set them). Anywhere else they stay empty
	// until the operator sets them, and Validate refuses the empty value.
	portalDef, erpDef := "", ""
	if env == "dev" || env == EnvDemo {
		portalDef, erpDef = "http://localhost:5173/portal.html", "http://localhost:5173"
	}
	return Config{
		EnvUnset:        envUnset,
		Port:            intEnv("PORT", 8080),
		DatabaseURL:     strEnv("DATABASE_URL", DevDatabaseURLDefault),
		NATSURL:         strEnv("NATS_URL", ""),
		JWKSURL:         strEnv("JWKS_URL", ""),
		Env:             env,
		PaymentVaultKey: strEnv("PAYMENT_VAULT_KEY", ""),
		PortalBaseURL:   strEnv("PORTAL_BASE_URL", portalDef),
		PlaneAPIURL:     strEnv("PLANE_API_URL", ""),
		PlaneAPIKey:     strEnv("PLANE_API_KEY", ""),
		PlaneProject:    strEnv("PLANE_PROJECT_ID", ""),
		PlaneWorkspace:  strEnv("PLANE_WORKSPACE_SLUG", ""),
		StaffBaseURL:    strEnv("ERP_BASE_URL", erpDef),
		DemoMode:        boolEnv("DEMO_MODE", false),

		BisTrackEndpoint: strEnv("BISTRACK_ENDPOINT", ""),
		BisTrackAPIKey:   strEnv("BISTRACK_API_KEY", ""),
	}
}

// Validate fails closed on insecure configuration. Call it at startup and treat
// any error as fatal. The dev defaults are accepted ONLY when APP_ENV=dev is set
// explicitly; an unset APP_ENV is refused, a value that is not exactly dev, staging,
// production or demo (a typo) is refused outright, and staging, production and demo
// must supply the values the remaining checks demand. (The
// two signing-secret clauses this function used to carry left with the HS256
// world they served: token verification is no longer this package's to
// configure.)
func (c Config) Validate() error {
	// An unset APP_ENV is refused before the dev short-circuit: the default is "dev", and a
	// production box that forgot the variable must not pass as one. An explicit APP_ENV=dev
	// is the deliberate spelling and stays permissive. Only serve and worker (and the demo
	// serve) call Validate; the one-shot tools never do.
	if c.EnvUnset {
		return fmt.Errorf("APP_ENV is not set: set it explicitly to one of dev, staging, production or demo (an unset value is refused so a missing variable cannot skip the checks)")
	}
	// APP_ENV must be exactly one of the four named values, case-sensitive and untrimmed. A
	// typo such as prod, Production or "dev " would otherwise get the strict checks below but
	// slip past the ones elsewhere that compare against the literal word production (the card
	// rail refusals in payment and payments). The bad value is deliberately not echoed.
	switch c.Env {
	case "dev", "staging", "production", EnvDemo:
	default:
		return fmt.Errorf("APP_ENV is not one of the accepted values: set it exactly (case-sensitive, no spaces) to dev, staging, production or demo")
	}
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
	// The link bases that invite, reset and reminder messages point at. A demo box is exempt:
	// it sends nothing live and has always run on the localhost defaults.
	if c.Env != EnvDemo {
		if err := checkBaseURL("PORTAL_BASE_URL", c.PortalBaseURL); err != nil {
			return err
		}
		if err := checkBaseURL("ERP_BASE_URL", c.StaffBaseURL); err != nil {
			return err
		}
	}
	return nil
}

// checkBaseURL requires an absolute https URL with a usable link base. It enforces: https; a
// host that is ASCII and is not empty, localhost (or *.localhost, with or without a trailing
// dot), a loopback or unspecified address, an IPv6 zone, a numeric shorthand spelling of an
// address (127.1, 2130706433, 0x7f.0.0.1, 0177.0.0.1, 127.0.0x.1) that a resolver or browser
// would read as loopback, an empty or hyphen-edged label, or a port outside 1 to 65535; and no
// userinfo, query, fragment, whitespace or control character, because every link built from
// this value would carry them. Non-ASCII hosts are refused outright (their full-width and
// ideographic-dot spellings normalise to localhost and 127.0.0.1); punycode xn-- hosts are ASCII
// and pass.
//
// IT CHECKS SPELLING ONLY. POLICY: it does not require a public address and it does not
// resolve anything. Private ranges, tailnet addresses, .local, .internal and single-label hosts
// pass, since a staging box may sit on a tailnet; so do names that resolve to loopback through a
// hosts file or public DNS (127.0.0.1.nip.io, localhost.localdomain, ip6-localhost,
// ip6-loopback, localhost6). The error names the variable and the reason, never the value.
func checkBaseURL(name, raw string) error {
	if raw == "" {
		return fmt.Errorf("%s must be set outside dev: it is the origin that links in messages point at", name)
	}
	for _, r := range raw {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("%s must not contain whitespace or control characters", name)
		}
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Scheme != "https" {
		return fmt.Errorf("%s must be an absolute https URL outside dev", name)
	}
	if u.User != nil {
		return fmt.Errorf("%s must not carry credentials (userinfo): they would be mailed in every link", name)
	}
	if strings.ContainsAny(raw, "?#") {
		return fmt.Errorf("%s must not carry a query or fragment: it is a base that links are built from", name)
	}
	if strings.HasSuffix(u.Host, ":") {
		return fmt.Errorf("%s must not carry an empty port", name)
	}
	if ps := u.Port(); ps != "" {
		if n, err := strconv.Atoi(ps); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("%s must carry a port from 1 to 65535 or none", name)
		}
	}
	for i := 0; i < len(u.Hostname()); i++ {
		if u.Hostname()[i] >= 0x80 {
			return fmt.Errorf("%s must name an ASCII host (use punycode for an international name)", name)
		}
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	const notLocal = "%s must name a real host outside dev, not an empty, localhost, loopback, unspecified or numeric-shorthand host"
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.Contains(host, "%") {
		return fmt.Errorf(notLocal, name)
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsUnspecified() {
			return fmt.Errorf(notLocal, name)
		}
		return nil
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("%s must name a well formed host (no empty or hyphen-edged label)", name)
		}
	}
	if numericShorthand(host) {
		return fmt.Errorf(notLocal, name)
	}
	return nil
}

// numericShorthand reports whether every dot-separated label is all digits or a 0x-prefixed hex
// group (a bare 0x counts, browsers read it as zero): the integer and short forms of an IPv4
// address that net.ParseIP does not accept but a resolver or browser does.
func numericShorthand(host string) bool {
	for _, label := range strings.Split(host, ".") {
		if label == "" {
			return false
		}
		digits := label
		if strings.HasPrefix(label, "0x") {
			for _, r := range label[2:] {
				if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
					return false
				}
			}
			continue
		}
		for _, r := range digits {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
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
