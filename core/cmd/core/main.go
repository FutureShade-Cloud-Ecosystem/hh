// Command core is the hh modular monolith binary. The App Shape gives it three
// roles: serve, worker and migrate. Serve wires the B3 identity surface: the
// JWKS verifier, the Appwrite membership source behind the D28 cache, the pgx
// org anchor resolver, the guard layer and the identity-owned route shims.
// Worker and migrate stay no-ops. All configuration arrives from environment
// at spawn and fails closed when incomplete; no secret has a default and none
// is read from any vault here.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/shims"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: core <serve|worker|migrate>")
		os.Exit(2)
	}
	switch role := os.Args[1]; role {
	case "serve":
		if err := serve(os.Getenv); err != nil {
			fmt.Fprintln(os.Stderr, "core: serve:", err)
			os.Exit(1)
		}
	case "worker", "migrate":
		fmt.Printf("core: role %q is a skeleton no-op\n", role)
	default:
		fmt.Fprintf(os.Stderr, "core: unknown role %q (want serve, worker or migrate)\n", role)
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
		listenAddr:     getenv("CORE_LISTEN_ADDR"),
	}
	if cfg.listenAddr == "" {
		cfg.listenAddr = ":8080"
	}
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

// serve constructs the identity surface and serves it until the listener
// fails. The driver session knob source is deliberately unwired here: the
// knob lives in dispatch_policy, a dispatch-owned table whose concrete
// reader lands with the Phase C dispatch module, and the guard refuses the
// driver ceiling leg until then (fail closed, identity.SessionCeiling).
func serve(getenv func(string) string) error {
	cfg, err := loadServeConfig(getenv)
	if err != nil {
		return err
	}
	ctx := context.Background()

	verifier, err := identity.NewVerifier(identity.Config{
		JWKSURL:        cfg.jwksURL,
		Issuer:         cfg.issuer,
		StaffAudience:  cfg.staffAudience,
		TenantAudience: cfg.tenantAudience,
		KeyCacheTTL:    cfg.jwksCacheTTL,
		RefreshFloor:   cfg.refreshFloor,
		ClockSkew:      cfg.clockSkew,
	})
	if err != nil {
		return err
	}

	// The pool is shared by the org resolver and the authority resolvers;
	// the production shape carries no tracer (the zero-statement proof
	// builds the same pool with one attached).
	pool, err := authz.NewPgxPoolWithTracer(ctx, cfg.databaseURL, nil)
	if err != nil {
		return err
	}
	defer pool.Close()

	appwrite, err := authz.NewAppwriteMembershipSource(cfg.appwriteURL, cfg.appwriteProj, cfg.appwriteKey, nil)
	if err != nil {
		return err
	}
	members := authz.NewCachedMembership(appwrite, 30*time.Second)

	orgs, err := authz.NewPgxOrgResolver(pool)
	if err != nil {
		return err
	}
	gate, err := authz.NewGate(verifier, members, orgs)
	if err != nil {
		return err
	}

	staff, err := identity.NewStaffAuthorityResolver(pool)
	if err != nil {
		return err
	}
	portal, err := identity.NewPortalRoleResolver(pool)
	if err != nil {
		return err
	}
	g, err := guards.NewGuards(gate, staff, portal, nil)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	rec := shims.NewRecorder(1024)
	shims.RegisterRoutes(mux, g, rec)

	// The money spine (HH-3.2 B4): gl, pricing, inventory, order, quote
	// (plus product and settings, their compile closure) register their
	// real routes through the Guards, and the identity shims for those
	// operationIds came out of shims.RegisterRoutes in the same change
	// (the ServeMux duplicate-registration panic is the backstop).
	wireMoney(mux, pool, g, slog.Default(), "")

	srv := &http.Server{Addr: cfg.listenAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	fmt.Printf("core: serve: listening on %s (identity shims plus the money spine)\n", cfg.listenAddr)
	err = srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
