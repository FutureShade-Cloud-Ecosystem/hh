//go:build integration

// evidence_test.go, the walkthrough harness.
//
// THE PROBLEM IT SOLVES. The route this package serves is mounted in cmd/server/main.go, and this
// lane does not touch main.go (a union conflict site, the wiring block is parked in
// NOTES-FIX-441X.md). So a browser walkthrough of the button cannot reach the endpoint through the
// real server yet.
//
// Rather than fake the walkthrough, this runs the PARKED WIRING BLOCK for real, out of tree: a small
// front door that serves GET /api/v1/exports/catalogue.xlsx from this package (over the same
// *product.Service and *inventory.Service main.go builds) and reverse-proxies everything else -
// login, /api/v1/products, /api/v1/inventory/browse, to the real backend. The UI is pointed at it.
// What the screenshots then show is the actual component talking to the actual handler; the only
// thing simulated is the two lines of main.go that are waiting on the supervisor.
//
//	HOS_EXPORT_EVIDENCE_ADDR=127.0.0.1:8098 HOS_EXPORT_EVIDENCE_UPSTREAM=http://127.0.0.1:8097 \
//	DATABASE_URL='postgres://…' go test -tags=integration ./internal/export/ -run Evidence -timeout 30m -v
//
// It is skipped unless HOS_EXPORT_EVIDENCE_ADDR is set, so it never runs in a normal suite.
package export

import (
	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

func TestEvidence_ServeTheParkedWiringForAWalkthrough(t *testing.T) {
	addr := os.Getenv("HOS_EXPORT_EVIDENCE_ADDR")
	if addr == "" {
		t.Skip("HOS_EXPORT_EVIDENCE_ADDR not set, this is the walkthrough harness, not a test")
	}
	upstream := os.Getenv("HOS_EXPORT_EVIDENCE_UPSTREAM")
	if upstream == "" {
		upstream = "http://127.0.0.1:8080"
	}
	target, err := url.Parse(upstream)
	if err != nil {
		t.Fatal(err)
	}
	pool := intPool(t)
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	mux := http.NewServeMux()
	// The parked block, verbatim in effect: build the catalogue export over the same two services
	// cmd/server already has in scope, and register it behind the staff guard. The guard here is the
	// real thing's stand-in, a pass-through, because identity's middleware needs the server's own
	// handler, which lives on the other side of the proxy.
	inv := inventoryService(pool)
	// Guards-swap carry note (HH-3.2 fold): the pass-through stand-in became
	// the kit's real Guards with a resolved staff authority.
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(coreid.StaffAuthority{RoleID: "inside_sales", Tier: 3, Scope: "global"})
	NewHandler(NewCatalogueService(productService(pool, inv), inv, log), log).
		RegisterRoutes(mux, sg.Guards)

	proxy := httputil.NewSingleHostReverseProxy(target)
	mux.Handle("/", proxy)

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("export evidence harness listening", "addr", addr, "upstream", upstream)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("harness stopped", "err", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	_ = srv.Close()
}
