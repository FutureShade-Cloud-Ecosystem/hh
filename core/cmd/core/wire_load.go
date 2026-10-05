// wire_load.go is the load doors' wiring (HH-LOAD-1 item LD-1): one
// function, one deployment configuration switch, no other surface.
//
// THE SWITCH. The load doors register only where the deployment's
// configuration turns them on: HH_LOAD_DOORS_ENABLED, value on, off
// under every other value and by default. The plan's gate section is
// explicit that this is a per deployment decision, on in the dealer's
// staging copy for the parallel run and in production only for the
// cut-over load, and the demo box refuses the variable outright (the
// environment inventory denies it). The switch is documented here and
// nowhere else; no dates.
//
// THE GATE, PENDING. The routes are machine surface: x-role machine in
// the census, and the machine bearer and the machine gate are the load
// cycle's machine identity item (LD-2), built in parallel on another
// branch. Until that gate lands, a boot with the switch on registers the
// routes behind a refusal wrapper, not behind nothing: every request
// answers the 503 family rather than serving ungated, because a
// non-staff census word passes the role gate registrar through
// unchanged, which is main's behaviour for non-staff words. LD-2
// replaces the wrapper below with the machine gate and re-points the
// handler's org resolution; the two registration lines do not move.
package main

import (
	"log/slog"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/jackc/pgx/v5/pgxpool"
)

// loadDoorsEnabledVar is the deployment configuration switch: value on
// registers the load doors, every other value (and its absence) leaves
// them unregistered. The demo box denies the variable outright.
const loadDoorsEnabledVar = "HH_LOAD_DOORS_ENABLED"

// wireLoad registers the load doors when and only when the switch is on.
// Nothing here starts anything: the store rides the shared pool.
func wireLoad(r httpx.Registrar, pool *pgxpool.Pool, log *slog.Logger, getenv func(string) string) {
	if getenv(loadDoorsEnabledVar) != "on" {
		log.Info("load: doors switched off; /api/v1/load not wired")
		return
	}
	h := load.NewHandler(load.NewStore(pool), log)
	h.RegisterRoutes(loadGatePending{r: r, log: log})
	log.Info("load: doors switched on, served behind the pending machine gate refusal (the machine gate is LD-2's)")
}

// loadGatePending is the fail-closed wrapper: every request refuses
// until the machine gate wraps these routes, because an ungated machine
// door would serve whoever reached the port.
type loadGatePending struct {
	r   httpx.Registrar
	log *slog.Logger
}

func (w loadGatePending) Handle(pattern string, h http.Handler) {
	w.r.Handle(pattern, http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		w.log.Warn("load: request refused, the machine gate is not wired in this build", "pattern", pattern)
		httpx.Error(rw, http.StatusServiceUnavailable, "the load doors register only behind the machine gate, which this build does not carry")
	}))
}
