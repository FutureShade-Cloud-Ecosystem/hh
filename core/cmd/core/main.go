// Command core is the hh modular monolith binary. The App Shape gives it
// three long-running roles (serve, worker, migrate) and two one-shot
// subcommands (seed, demoseed). Serve owns the HTTP listener and nothing
// that drains or schedules; worker owns every background loop that writes
// (both outbox dispatchers, the jobs runner, the retention sweep, the
// approvals reconciler, D37); migrate applies the frozen migration set
// read-only. All configuration arrives from environment at spawn and fails
// closed when incomplete; no secret has a default and none is read from
// any vault here. OpenTelemetry activates on serve only: the middleware
// wraps the served mux from outside the authz gate, and the traced pool
// stays the test-only variant (the zero-statement proofs depend on both
// halves).
package main

import (
	// time/tzdata embeds the IANA zone database INTO the binary so
	// LoadLocation resolves without the OS carrying /usr/share/zoneinfo
	// (the pin's D-112 insurance, carried by the fold: the ledger's
	// house-zone day boundary, the dashboard windows, the messaging rollup
	// and the SMS quiet-hours all degrade SILENTLY to UTC when the zone
	// does not resolve, which is a reporting defect nobody can see).
	_ "time/tzdata"

	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customerquote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dispatch"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/shims"
	staffcontext "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/tenantconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/otelhttp"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: core <serve|worker|migrate|seed|demoseed> [flags]")
		os.Exit(2)
	}
	// Demo-only roles (the demo build's serve, the demo one-shots) answer
	// through the build-tagged pair demo_on.go and demo_off.go. In the
	// default build the stub answers false and everything below runs
	// exactly as it always has.
	if demoDispatch(os.Args[1], os.Args[2:]) {
		return
	}
	switch role := os.Args[1]; role {
	case "serve":
		if err := serve(os.Getenv); err != nil {
			fmt.Fprintln(os.Stderr, "core: serve:", err)
			os.Exit(1)
		}
	case "worker":
		if err := worker(os.Getenv); err != nil {
			fmt.Fprintln(os.Stderr, "core: worker:", err)
			os.Exit(1)
		}
	case "migrate":
		if err := migrateRole(os.Getenv); err != nil {
			fmt.Fprintln(os.Stderr, "core: migrate:", err)
			os.Exit(1)
		}
	case "seed":
		if err := seedRole(os.Getenv); err != nil {
			fmt.Fprintln(os.Stderr, "core: seed:", err)
			os.Exit(1)
		}
	case "demoseed":
		if err := demoseedRole(os.Args[2:], os.Stdout, os.Getenv); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "core: unknown role %q (want serve, worker, migrate, seed or demoseed)\n", role)
		os.Exit(2)
	}
}

// serveConfig names every environment input of the serve role plainly.
// The Appwrite API key and the database URL are credentials and secrets by
// content: they arrive as environment values, are held in memory only, and
// are never logged, echoed or cached. The three optional durations (JWK Set
// cache TTL, refresh floor, clock skew) select the verifier's timing knobs;
// unset selects the identity package defaults, and a set but unparseable
// value is refused rather than ignored.
type serveConfig struct {
	issuer         string
	staffAudience  string
	tenantAudience string
	jwksURL        string
	databaseURL    string
	appwriteURL    string
	appwriteProj   string
	appwriteKey    string
	listenAddr     string
	jwksCacheTTL   time.Duration
	refreshFloor   time.Duration
	clockSkew      time.Duration
}

func loadServeConfig(getenv func(string) string) (serveConfig, error) {
	required := map[string]string{
		"CORE_TOKEN_ISSUER":      "token issuer",
		"CORE_STAFF_AUDIENCE":    "staff audience",
		"CORE_TENANT_AUDIENCE":   "tenant audience",
		"CORE_JWKS_URL":          "JWK Set URL",
		"CORE_DATABASE_URL":      "database URL",
		"CORE_APPWRITE_ENDPOINT": "Appwrite endpoint",
		"CORE_APPWRITE_PROJECT":  "Appwrite project",
		"CORE_APPWRITE_API_KEY":  "Appwrite API key",
	}
	var missing []string
	for env, name := range required {
		if strings.TrimSpace(getenv(env)) == "" {
			missing = append(missing, name+" ("+env+")")
		}
	}
	if len(missing) > 0 {
		return serveConfig{}, fmt.Errorf("missing required configuration: %s", strings.Join(missing, ", "))
	}
	if getenv("CORE_STAFF_AUDIENCE") == getenv("CORE_TENANT_AUDIENCE") {
		return serveConfig{}, errors.New("staff and tenant audience values must differ")
	}
	cfg := serveConfig{
		issuer:         getenv("CORE_TOKEN_ISSUER"),
		staffAudience:  getenv("CORE_STAFF_AUDIENCE"),
		tenantAudience: getenv("CORE_TENANT_AUDIENCE"),
		jwksURL:        getenv("CORE_JWKS_URL"),
		databaseURL:    getenv("CORE_DATABASE_URL"),
		appwriteURL:    getenv("CORE_APPWRITE_ENDPOINT"),
		appwriteProj:   getenv("CORE_APPWRITE_PROJECT"),
		appwriteKey:    getenv("CORE_APPWRITE_API_KEY"),
	}
	addr, err := resolveListenAddr(getenv)
	if err != nil {
		return serveConfig{}, err
	}
	cfg.listenAddr = addr
	for env, dst := range map[string]*time.Duration{
		"CORE_JWKS_CACHE_TTL":     &cfg.jwksCacheTTL,
		"CORE_JWKS_REFRESH_FLOOR": &cfg.refreshFloor,
		"CORE_CLOCK_SKEW":         &cfg.clockSkew,
	} {
		raw := strings.TrimSpace(getenv(env))
		if raw == "" {
			continue
		}
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return serveConfig{}, fmt.Errorf("%s must be a positive duration, got %q", env, raw)
		}
		*dst = d
	}
	return cfg, nil
}

// appGraph is the constructed application: everything the wire functions
// built over one Registrar, plus the identity legs both roles share. Serve
// hands the wire functions the served mux; the worker hands them a discard
// registrar (construction only, no route surface), so both roles run the
// SAME wiring decisions and the loops' subjects are the instances the
// request path would use.
type appGraph struct {
	cfg     config.Config
	log     *slog.Logger
	pool    *pgxpool.Pool
	verify  *identity.Verifier
	link    staffcontext.UserIDLinker
	g       *guards.Guards
	money   *moneySpine
	ops     *operationsSpine
	tops    *topsSpine
	smsBelt *smsBelt
	// listenAddr is the address serve binds, resolved ONCE at config load (HH-200): serve and the demo
	// serve read it from the graph, never from the environment a second time.
	listenAddr string
	// demoMint is the demo build's session mint over the in-memory demo
	// issuer, set only by demo_on.go's wiring; the default build's graph
	// carries nil and mints nothing (test support and the demo session
	// lane read it, never the default request path).
	demoMint func(string) (string, error)
}

// registerRoutes is the one place every production registration lands: the
// identity shims, then the money spine, the operations cluster and the tops
// in the pin's construction order (the ServeMux duplicate-registration
// panic is the backstop that keeps the shim-removal rule self-enforcing).
// Nothing outside this function registers on a production route surface,
// and nothing inside it starts a loop: the dispatchers, the jobs runner,
// the retention sweep and the reconciler start only in the worker role.
//
// The role configuration (D48, D49, plan item RG): the composition root is
// the startup contract's owner. The decided default loads here and a
// refusal stops the core: a refused configuration never reaches the gate
// as a zero value and never falls back behind the refusal's back.
func registerRoutes(r httpx.Registrar, getenv func(string) string) (*appGraph, error) {
	roleCfg, err := roleconfig.Default()
	if err != nil {
		return nil, fmt.Errorf("role configuration: %w", err)
	}
	return registerRoutesWithRoleConfig(r, getenv, roleCfg)
}

// registerRoutesWithRoleConfig is registerRoutes over a caller-supplied
// role configuration: the production entry loads the decided default and
// calls this; the tests build a modified document and call this, which is
// how a per dealer configuration changes admissions with no code change.
func registerRoutesWithRoleConfig(r httpx.Registrar, getenv func(string) string, roleCfg *roleconfig.Config) (*appGraph, error) {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	log := slog.Default()

	auth, err := loadServeConfig(getenv)
	if err != nil {
		return nil, err
	}

	verifier, err := identity.NewVerifier(identity.Config{
		JWKSURL:        auth.jwksURL,
		Issuer:         auth.issuer,
		StaffAudience:  auth.staffAudience,
		TenantAudience: auth.tenantAudience,
		KeyCacheTTL:    auth.jwksCacheTTL,
		RefreshFloor:   auth.refreshFloor,
		ClockSkew:      auth.clockSkew,
	})
	if err != nil {
		return nil, err
	}

	// The pool is shared by every module and the org resolver; the
	// production shape carries no tracer (the zero-statement proof builds
	// the same pool with one attached; the traced pool stays test-only).
	pool, err := authz.NewPgxPoolWithTracer(context.Background(), auth.databaseURL, nil)
	if err != nil {
		return nil, err
	}

	appwrite, err := authz.NewAppwriteMembershipSource(auth.appwriteURL, auth.appwriteProj, auth.appwriteKey, nil)
	if err != nil {
		pool.Close()
		return nil, err
	}
	members := authz.NewCachedMembership(appwrite, 30*time.Second)
	orgs, err := authz.NewPgxOrgResolver(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	gate, err := authz.NewGate(verifier, members, orgs)
	if err != nil {
		pool.Close()
		return nil, err
	}
	staff, err := identity.NewStaffAuthorityResolver(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	portal, err := identity.NewPortalRoleResolver(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	// P-4 closed (HH-3.2 B5): the driver session ceiling's knob source is
	// the dispatch-owned dispatch_policy reader; the knob read refuses on
	// an unreadable or out-of-contract policy (the B2-07 no-fallback
	// ruling: never a guess), so the 503 family answers an outage rather
	// than silently shortening sessions.
	g, err := guards.NewGuards(gate, staff, portal, dispatchDriverSessionKnob{repo: dispatch.NewRepository(pool)})
	if err != nil {
		pool.Close()
		return nil, err
	}
	roleGate, err := authz.NewRoleGate(gate, staff, roleCfg)
	if err != nil {
		pool.Close()
		return nil, err
	}
	// Every registration below lands through the wrapper, so each staff
	// route is served only through its role gate, reading the annotation
	// the census carries for it.
	wired, err := authz.NewRoleGateRegistrar(r, roleGate)
	if err != nil {
		pool.Close()
		return nil, err
	}
	r = wired

	// The proof shims the real modules have not replaced: the tenant set,
	// the sync pair and the capability route, all Phase D-owned (their
	// modules are the tenant surface; the justification is recorded in the
	// fold's report).
	rec := shims.NewRecorder(1024)
	shims.RegisterRoutes(r, g, rec)

	// The G2 handler half (ADR 0021): the public tenant config on the
	// public leg, no verification, no org resolution (ADR-0020 leg 2).
	// The served values come from the stub sourcing ADR 0021 records:
	// the ERP antecedent's static dealer identity, equal to the vendored
	// capture.
	r.Handle("GET /api/v1/tenant/config", g.Public(tenantconfig.New(tenantconfig.Default())))

	// The customer-quote surface (HH-3.6 C5, the integration audit
	// re-point): the service is constructed AFTER the spines below, because
	// the concrete audit seam rides the carried audit service wireMoney
	// builds (one audit service instance serves every consumer; a second
	// construction would strand the seam on an instance nobody reads). The
	// construction sits beside the spine wiring with its own marked block.

	// The SMS belt precedes the operations wiring: the yard repository's
	// stage transaction enqueues through its outbox. Its contact source
	// lands in the tops wiring with the customer, billing and order
	// services.
	belt := buildSmsBelt(pool, log)

	money := wireMoney(r, pool, g, log, cfg.StaffBaseURL)
	ops := wireOperations(r, pool, g, log, money, os.Getenv("CORE_STREAM_TICKET_KEY"), belt.outbox)
	tops := wireTops(r, pool, g, log, money, ops, cfg, belt)

	// The customer-quote surface (HH-3.6 C5, THE AUDIT RE-POINT SITE): the
	// store over the shared pool, the signing keys over the environment
	// (the HH_ACCEPTANCE_SIGN_* placeholder names, ADR 0022's key schedule:
	// no value exists in any file, and the secret store path is Colton's
	// to name at deploy), and the concrete audit seam over the carried
	// audit service (cq_adapters.go), so the acceptance audit write is
	// real: NewService refuses a nil seam by construction, a missing or
	// malformed signing key fails the spawn loudly, and no nil-service
	// absence remains to print. registerRoutes gains nothing else: the
	// registration is the module's own three documented operations.
	cqKeys, err := customerquote.LoadSigningKeys(getenv)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("customer-quote signing keys: %w", err)
	}
	cqStore, err := customerquote.NewStore(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	cqService, err := customerquote.NewService(cqStore, cqAuditSeam{svc: money.auditSvc}, cqKeys, customerquote.Config{})
	if err != nil {
		pool.Close()
		return nil, err
	}
	customerquote.RegisterRoutes(r, g, cqService)

	// The customer_quotes sync scope (HH-3.5 #169, manual policy)
	// registers beside the service it reads through: the module's own
	// TenantQuote projection over its own table, the lifecycle writes
	// stamping their seqs (send, revise, accept), and the sync writes
	// bounded to the compose legs so no replay can resurrect a rotated
	// token's document. The engine rides the operations spine the
	// wiring above returned.
	if err := ops.syncEngine.RegisterScope("customer_quotes", customerquote.NewSyncStore(cqStore, cqService)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}

	return &appGraph{
		cfg: cfg, log: log, pool: pool, verify: verifier, link: linkedUserID(pool), g: g,
		money: money, ops: ops, tops: tops, smsBelt: belt, listenAddr: auth.listenAddr,
	}, nil
}

// healthzHandler serves GET /healthz: liveness plus the database's live
// ping. The pin's google_sso flag is carried as the constant false (the
// staff Google sign-in surface is the recorded SSO leave-behind, folded
// into the D38 sign-in-methods item): the body shape is additive-only, so
// a reader of the flag keeps reading an answer.
func healthzHandler(pingDB func(context.Context) error, bistrackStatus string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		dbUp := false
		if pingDB != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			dbUp = pingDB(ctx) == nil
		}
		if !dbUp {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		dbState := "down"
		if dbUp {
			dbState = "up"
		}
		fmt.Fprintf(w, `{"status":"ok","db":"%s","google_sso":false,"bistrack":"%s"}`,
			dbState, bistrackStatus)
	}
}

// serve constructs the served surface and serves it until the process is
// signalled, then shuts down clean. Serve starts no scheduler and drains
// no outbox (the acceptance test beside this file proves the first, the
// worker's drains test the second).
func serve(getenv func(string) string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	graph, err := registerRoutes(mux, getenv)
	if err != nil {
		return err
	}
	defer graph.pool.Close()

	// healthz: a LIVE database ping, not a startup nil-check, so a deploy
	// gate fails honestly when the database is down.
	mux.Handle("GET /healthz", healthzHandler(graph.pool.Ping, graph.tops.bistrackSvc.HealthStatus()))

	// A-21 records every mutating /api request, outside the authz wrapping (the pin's Handler
	// order: the audit middleware wraps the whole mux, the otel middleware wraps from outside it).
	srv, err := newServer(graph, otelhttp.Wrap(nil)(auditWrap(mux, graph)))
	if err != nil {
		return err
	}
	errCh := make(chan error, 1)
	go func() {
		fmt.Printf("core: serve: listening on %s\n", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	fmt.Println("core: serve: shutdown complete")
	return nil
}

// newServer builds the HTTP server both serve builds run, binding the address resolved at config
// load and carried on the graph. An empty address is refused: http.Server would bind ":80"
// silently, so a graph constructor that forgot the field must fail loudly, not listen on port 80.
func newServer(graph *appGraph, handler http.Handler) (*http.Server, error) {
	if graph.listenAddr == "" {
		return nil, errors.New("serve: no listen address was resolved at config load")
	}
	return &http.Server{
		Addr:              graph.listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}, nil
}

// resolveListenAddr is the listen address rule, run once at config load by the default
// build (loadServeConfig) and by the demo build (loadDemoServeConfig), so a bad PORT is
// refused before the database is touched in both: CORE_LISTEN_ADDR wins when
// set; otherwise PORT (the platform's injected port) gives ":<port>"; with
// neither set the address is ":8080". A PORT that is set but not a whole
// number from 1 to 65535 is refused by name, never ignored: the shared
// config's intEnv falls back to a default on junk, and a service listening
// somewhere the platform did not point is unreachable with no error. Empty
// reads as unset, as for CORE_LISTEN_ADDR.
func resolveListenAddr(getenv func(string) string) (string, error) {
	if v := strings.TrimSpace(getenv("CORE_LISTEN_ADDR")); v != "" {
		return v, nil
	}
	raw := strings.TrimSpace(getenv("PORT"))
	if raw == "" {
		return ":8080", nil
	}
	// ASCII digits only, at most five of them: no sign, no separators, no other scripts' digits,
	// no long run of leading zeros. A platform probe that substitutes the raw value into a URL must
	// read the same number this resolver does.
	n := -1
	if len(raw) <= 5 && strings.Trim(raw, "0123456789") == "" {
		n, _ = strconv.Atoi(raw)
	}
	if n < 1 || n > 65535 {
		shown := raw
		if rs := []rune(shown); len(rs) > 32 {
			shown = string(rs[:32]) + "..."
		}
		return "", fmt.Errorf("PORT must be a whole number from 1 to 65535 (digits only), got %q", shown)
	}
	return ":" + strconv.Itoa(n), nil
}

// auditWrap applies the A-21 recording middleware (every mutating /api
// request) with the bridged authenticator.
func auditWrap(mux *http.ServeMux, graph *appGraph) http.Handler {
	return graph.money.auditSvc.Middleware(bridgedAuthn{verify: graph.verify, link: graph.link})(mux)
}

// bridgedAuthn satisfies audit.Authenticator over the target bearer world
// (the pin's identityHandler.Actor re-read the HS256 token's snapshot;
// under JWKS no role snapshot exists in the token and D3 forbids trusting
// one, so the audit row's actor id is the linked ERP users.id the stamp
// uses and the role word reads empty: the tier and role facts resolve per
// request in the guard, and attribution keeps the load-bearing half, the
// id). Unauthenticated mutations record the empty actor, as at the pin.
type bridgedAuthn struct {
	verify *identity.Verifier
	link   staffcontext.UserIDLinker
}

func (a bridgedAuthn) Actor(r *http.Request) (string, string) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return "", ""
	}
	token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	if token == "" || a.verify == nil {
		return "", ""
	}
	tok, err := a.verify.Verify(r.Context(), token)
	if err != nil || tok.Class != identity.ClassStaff {
		return "", ""
	}
	if a.link == nil {
		return "", ""
	}
	uid, ok := a.link(r.Context(), tok.Subject)
	if !ok {
		return "", ""
	}
	return uid, ""
}

// discardRegistrar is the worker's construction stand-in: it satisfies the
// Registrar seam by dropping every registration, because the worker role
// builds the same graph the request path uses without owning a route
// surface. No production route ever lands on it.
type discardRegistrar struct{}

func (discardRegistrar) Handle(string, http.Handler) {}
