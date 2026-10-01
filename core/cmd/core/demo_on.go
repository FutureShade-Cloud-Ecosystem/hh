//go:build demo

// The demo build's side of the entrypoint seam (the default build's side
// is demo_off.go, a stub that imports nothing under core/internal/demo/
// and answers false, so the default build's dispatch is unchanged). This
// file registers the demo pieces into the binary: the serve role runs the
// demo wiring, where the verifier is constructed over the in-memory demo
// issuer's static key source, the gate is constructed over the demo
// membership source, and the boot guards run before the listener opens.
//
// The graph below mirrors main.go's registerRoutes step for step over the
// same wire functions, with exactly two substitutions (the verifier's key
// source and the membership source): everything after the token is the
// real path, so a demo bearer is held to the same verification, org
// resolution, role grants, tier gates and module rules as any
// platform-issued bearer. When the default wiring gains a step, this
// mirror gains the same step in the same place.
package main

import (
	// time/tzdata is embedded by main.go; the serve path here relies on
	// it the same way.
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customerquote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/boot"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/issuer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/membership"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dispatch"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/shims"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/tenantconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/otelhttp"
)

// demoDispatch runs a demo-only role and reports whether it answered.
// main.go consults it before the standard role switch; a refusal exits
// non zero, which is how every boot guard stops the entrypoint.
func demoDispatch(role string, args []string) bool {
	switch role {
	case "serve":
		if err := demoServe(os.Getenv, os.LookupEnv, os.Environ); err != nil {
			fmt.Fprintln(os.Stderr, "core: serve:", err)
			os.Exit(1)
		}
		return true
	case "demo-check":
		if err := boot.DemoCheck(context.Background(), os.LookupEnv); err != nil {
			fmt.Fprintln(os.Stderr, "core: demo-check:", err)
			os.Exit(1)
		}
		return true
	}
	return false
}

// demoServeConfig names the environment inputs of the demo serve role.
// The demo box needs no token issuer (the demo issuer names itself), no
// JWK Set URL (the key source is in memory) and none of the platform
// client's values (the demo membership source replaces it).
type demoServeConfig struct {
	databaseURL    string
	staffAudience  string
	tenantAudience string
	tokenTTL       time.Duration
}

func loadDemoServeConfig(getenv func(string) string) (demoServeConfig, error) {
	databaseURL := strings.TrimSpace(getenv("CORE_DATABASE_URL"))
	staff := strings.TrimSpace(getenv("CORE_STAFF_AUDIENCE"))
	tenant := strings.TrimSpace(getenv("CORE_TENANT_AUDIENCE"))
	var missing []string
	if databaseURL == "" {
		missing = append(missing, "database URL (CORE_DATABASE_URL)")
	}
	if staff == "" {
		missing = append(missing, "staff audience (CORE_STAFF_AUDIENCE)")
	}
	if tenant == "" {
		missing = append(missing, "tenant audience (CORE_TENANT_AUDIENCE)")
	}
	if len(missing) > 0 {
		return demoServeConfig{}, fmt.Errorf("missing required configuration: %s", strings.Join(missing, ", "))
	}
	if staff == tenant {
		return demoServeConfig{}, errors.New("staff and tenant audience values must differ")
	}
	cfg := demoServeConfig{
		databaseURL:    databaseURL,
		staffAudience:  staff,
		tenantAudience: tenant,
	}
	if raw := strings.TrimSpace(getenv(issuer.TokenTTLVar)); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return demoServeConfig{}, fmt.Errorf("%s must be a positive duration, got %q", issuer.TokenTTLVar, raw)
		}
		if d > issuer.DefaultTTL {
			return demoServeConfig{}, fmt.Errorf("%s is longer than the demo ceiling (%s): the expiry is short by design and the configuration may lengthen it, not remove its shortness", issuer.TokenTTLVar, issuer.DefaultTTL)
		}
		cfg.tokenTTL = d
	}
	return cfg, nil
}

// demoServe is the demo build's serve role. The guards run in order,
// cheapest first, and every one of them refuses before the listener
// opens: the box must say demo twice (G2), the environment must carry no
// outbound provider (G4), the database must be a demo database (G3),
// and only then does the demo graph build and the listener open.
func demoServe(getenv func(string) string, lookupEnv func(string) (string, bool), environ func() []string) error {
	if err := boot.CheckDemoBox(lookupEnv); err != nil {
		return err
	}
	// G4: the demo box carries no outbound provider. The deny set is the
	// one copy in demoenv_inventory.go that the default-build inventory
	// test holds against the source tree. The environment picture arrives
	// as an argument, the way getenv and lookupEnv do, so this call is
	// pinned by a test through demoServe and not only inside the boot
	// package. The allow list's CORE_ prefix is the plan's own: every
	// future CORE_* name classes as allowed on the demo box without
	// anyone looking at it, which is G4's stated blind spot and not an
	// oversight of this wiring.
	if err := boot.RefuseProviderVars(environ(), boot.ProviderDeny{
		Prefixes:    demoEnvDenyPrefixes,
		Names:       demoEnvDenyNames,
		CardVar:     demoEnvCardProcessorVar,
		CardAllowed: demoEnvCardProcessorValue,
	}); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	graph, err := demoRegisterRoutes(mux, getenv)
	if err != nil {
		return err
	}
	defer graph.pool.Close()

	// G7 (the card rail's lane) slots its refusal here, after the graph
	// is built and before the listener opens: the wired payment service
	// must report itself simulated or the demo build does not serve.

	// healthz: the same live database ping the default build serves.
	mux.Handle("GET /healthz", healthzHandler(graph.pool.Ping, graph.tops.bistrackSvc.HealthStatus()))

	srv := &http.Server{
		Addr:              listenAddr(getenv),
		Handler:           otelhttp.Wrap(nil)(auditWrap(mux, graph)),
		ReadHeaderTimeout: 10 * time.Second,
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

// demoRegisterRoutes builds the demo graph: the same construction
// main.go's registerRoutes makes, with the demo issuer under the verifier
// and the demo membership source under the gate.
func demoRegisterRoutes(r httpx.Registrar, getenv func(string) string) (*appGraph, error) {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	log := slog.Default()

	demoCfg, err := loadDemoServeConfig(getenv)
	if err != nil {
		return nil, err
	}

	// Piece 2: the demo issuer. Its key is born here and never leaves the
	// process; the verifier is the unchanged production verifier over the
	// static key source, so no JWK Set URL is configured and no key set
	// route exists.
	iss, err := issuer.New(demoCfg.staffAudience, demoCfg.tokenTTL)
	if err != nil {
		return nil, err
	}
	verifier, err := identity.NewVerifier(identity.Config{
		KeySource:      iss.KeySource(),
		Issuer:         issuer.ClaimIssuer,
		StaffAudience:  demoCfg.staffAudience,
		TenantAudience: demoCfg.tenantAudience,
	})
	if err != nil {
		return nil, err
	}

	// The pool is shared by every module and the org resolver, as in the
	// default build.
	pool, err := authz.NewPgxPoolWithTracer(context.Background(), demoCfg.databaseURL, nil)
	if err != nil {
		return nil, err
	}

	// G3: the database must be the demo's invented world, and the check
	// runs here, before anything registers or serves.
	if err := boot.CheckDemoDatabase(context.Background(), pool); err != nil {
		pool.Close()
		return nil, err
	}

	// Piece 3: the demo membership source. The platform client is never
	// constructed on a demo box and its environment values are never
	// read; membership answers only the demo org and the flow accounts.
	members := membership.New()
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
	g, err := guards.NewGuards(gate, staff, portal, dispatchDriverSessionKnob{repo: dispatch.NewRepository(pool)})
	if err != nil {
		pool.Close()
		return nil, err
	}

	// The proof shims, the public tenant config and the three spines: the
	// default build's construction, unchanged, over the demo gate.
	rec := shims.NewRecorder(1024)
	shims.RegisterRoutes(r, g, rec)
	r.Handle("GET /api/v1/tenant/config", g.Public(tenantconfig.New(tenantconfig.Default())))

	belt := buildSmsBelt(pool, log)
	money := wireMoney(r, pool, g, log, cfg.StaffBaseURL)
	ops := wireOperations(r, pool, g, log, money, getenv("CORE_STREAM_TICKET_KEY"), belt.outbox)
	tops := wireTops(r, pool, g, log, money, ops, cfg, belt)

	// The customer-quote surface over the shared audit seam: the same
	// construction main.go makes, signing keys from the environment.
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
	if err := ops.syncEngine.RegisterScope("customer_quotes", customerquote.NewSyncStore(cqStore, cqService)); err != nil {
		pool.Close()
		panic("sync: scope wiring refused to boot: " + err.Error())
	}

	// The demo module's mint routes (POST /api/v1/demo/session and
	// GET /api/v1/demo/flows, the parity ledger's demo pair) register
	// here when their lane lands: the mint builds on this file's issuer,
	// the gate above and the closed flow table, and registers on the
	// public leg the tenant config uses.

	return &appGraph{
		cfg: cfg, log: log, pool: pool, verify: verifier, link: linkedUserID(pool), g: g,
		money: money, ops: ops, tops: tops, smsBelt: belt,
	}, nil
}
