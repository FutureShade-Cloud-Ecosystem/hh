//go:build demo

package main

// card_rail_g7_test.go is G7's wiring-level evidence (plan 4A, row PX): the
// demo build's refusal reads the card rail the wiring actually built. A
// demo wiring given a live processor (HOS_CARD_PROCESSOR=run at a local
// httptest base URL) fails demoRefuseLiveCardRail, the check demoServe
// calls at its marked spot after the graph is built and before the
// listener opens, so the serve role returns its refusal and never opens a
// listener on a live rail; the simulator wiring passes the check and
// serves. Both legs construct the rail through buildCardRail itself, the
// composition root's one construction, over a nil pool: registration
// touches no table, so the choice of processor is provable without a
// database (the same shape wire_test's registration proof uses). The
// end-to-end serve replay waits on the demo database the populate lane
// writes, the same way W5's image test reads the status route's answer.

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// g7WiredRail builds the card rail the way wireTops builds it, over the
// kit's DB-free guards and a nil pool, picking up whatever rail variables
// the caller set.
func g7WiredRail(t *testing.T) *payment.Service {
	t.Helper()
	sg := testkit.NewStaffGuards(t)
	paySvc, _ := buildCardRail(http.NewServeMux(), sg.Guards, nil, config.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return paySvc
}

// TestDemoG7_LiveCardRailRefusesToServe: a demo wiring on a live processor
// is a refusal, with the sentence that names the check, and nothing
// reaches the processor host (the refusal fires on what was wired, before
// any call exists to make).
func TestDemoG7_LiveCardRailRefusesToServe(t *testing.T) {
	var hitsMu sync.Mutex
	var hits int
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsMu.Lock()
		hits++
		hitsMu.Unlock()
	}))
	t.Cleanup(host.Close)
	t.Setenv("APP_ENV", "dev")
	t.Setenv("HOS_CARD_PROCESSOR", "run")
	t.Setenv("HOS_CARD_BASE_URL", host.URL)

	paySvc := g7WiredRail(t)
	if paySvc.Simulated() {
		t.Fatal("the wiring built the simulator under HOS_CARD_PROCESSOR=run, there is nothing for G7 to refuse")
	}
	err := demoRefuseLiveCardRail(paySvc)
	if err == nil {
		t.Fatal("a demo wiring on a live processor passed the G7 refusal: the demo build would open a listener on a live rail")
	}
	if !strings.Contains(err.Error(), "G7") || !strings.Contains(err.Error(), "not the simulator") {
		t.Fatalf("the refusal must name its own check, got %q", err)
	}
	hitsMu.Lock()
	defer hitsMu.Unlock()
	if hits != 0 {
		t.Fatalf("the live rail made %d request(s) during wiring, want none", hits)
	}
}

// TestDemoG7_SimulatorCardRailServes: with the setting unset the stub is
// what the wiring builds, and that is the rail the demo build serves.
func TestDemoG7_SimulatorCardRailServes(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("HOS_CARD_PROCESSOR", "")
	t.Setenv("HOS_CARD_BASE_URL", "")

	paySvc := g7WiredRail(t)
	if !paySvc.Simulated() {
		t.Fatal("the wiring did not build the simulator with the setting unset: the demo default must be the stub")
	}
	if err := demoRefuseLiveCardRail(paySvc); err != nil {
		t.Fatalf("the simulator wiring must pass the G7 refusal: %v", err)
	}
}

// TestDemoG7_ANilCardRailRefusesToo: the guard fails closed. A rail that
// cannot say what it is never serves a demo box.
func TestDemoG7_ANilCardRailRefusesToo(t *testing.T) {
	if err := demoRefuseLiveCardRail(nil); err == nil {
		t.Fatal("a nil card rail passed the G7 refusal: the guard must fail closed")
	}
}
