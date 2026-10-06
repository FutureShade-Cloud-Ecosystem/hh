//go:build integration

package main

// claim_pool_lifecycle_test.go pins the two properties the claim pools must keep besides not
// starving the shared pool (site 23): they carry the shared pool's session settings, and closing the
// claim stores gives every connection back. Both run against the leg's own throwaway database and
// skip without DATABASE_URL.

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// otherSessions counts the database's sessions apart from the asking one.
func otherSessions(t *testing.T, helper *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := helper.QueryRow(context.Background(),
		`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid()`).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

func TestClaimPool_CarriesTheSharedPoolsSessionSettings(t *testing.T) {
	raw := claimLegURL(t)
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	// Session settings that differ from the server defaults, set on the shared pool's URL the way the
	// deploy sets them; the claim pools must come up with the same ones.
	q := u.Query()
	q.Set("TimeZone", "America/Chicago")
	q.Set("search_path", "public,pg_catalog")
	u.RawQuery = q.Encode()

	ctx := context.Background()
	shared := claimLegPool(t, u.String(), claimLegPoolSize)
	cs, err := buildClaimStores(ctx, shared, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("buildClaimStores: %v", err)
	}
	t.Cleanup(cs.Close)

	read := func(p *pgxpool.Pool) (tz, path string) {
		if err := p.QueryRow(ctx, `SELECT current_setting('TimeZone'), current_setting('search_path')`).Scan(&tz, &path); err != nil {
			t.Fatalf("read settings: %v", err)
		}
		return
	}
	wantTZ, wantPath := read(shared)
	if wantTZ != "America/Chicago" || wantPath != "public,pg_catalog" {
		t.Fatalf("the rig did not set the shared pool's session: TimeZone %q search_path %q", wantTZ, wantPath)
	}
	for name, p := range map[string]*pgxpool.Pool{"events claim pool": cs.eventsPool, "sms claim pool": cs.smsPool} {
		tz, path := read(p)
		if tz != wantTZ || path != wantPath {
			t.Errorf("%s: TimeZone %q search_path %q, shared pool has %q and %q", name, tz, path, wantTZ, wantPath)
		}
		if got := p.Config().MaxConns; got != claimPoolConns {
			t.Errorf("%s: MaxConns %d, want %d", name, got, claimPoolConns)
		}
	}
}

func TestClaimPool_CloseGivesEveryConnectionBack(t *testing.T) {
	raw := claimLegURL(t)
	ctx := context.Background()
	helper := claimLegPool(t, raw, 1)
	shared := claimLegPool(t, raw, claimLegPoolSize)

	baseline := otherSessions(t, helper) // the shared pool's pinged connection is part of the baseline
	cs, err := buildClaimStores(ctx, shared, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("buildClaimStores: %v", err)
	}
	if got := otherSessions(t, helper); got != baseline+2*claimPoolConns {
		t.Errorf("with the claim pools open the database has %d other sessions, want baseline %d plus %d", got, baseline, 2*claimPoolConns)
	}
	cs.Close()
	var got int
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if got = otherSessions(t, helper); got == baseline {
			return
		}
	}
	t.Errorf("after Close the database has %d other sessions, want the baseline %d: a claim connection leaked", got, baseline)
}
