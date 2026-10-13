//go:build integration

// keybody_race_integration_test.go: the fourth guarantee under concurrency.
// Two pushes of one key with different bodies must not both apply.
package sync_test

import (
	"context"
	"fmt"
	"strings"
	gosync "sync"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// Two pushes of one fresh key with different bodies, started together: the
// guarantee is that exactly one applies. Twenty rounds, each a fresh key.
func TestKeyBodyD30ConcurrentDifferentBodiesApplyOnce(t *testing.T) {
	raceDifferentBodies(t, func(round, i int) string { return newKey(1000 + round) })
}

// The same race with the key spelled in two letter cases: a uuid is one
// key whatever the case, so the second spelling must wait on the first and
// be refused, exactly as the one spelling is.
func TestKeyBodyD30ConcurrentMixedCaseKeyApplyOnce(t *testing.T) {
	raceDifferentBodies(t, func(round, i int) string {
		key := fmt.Sprintf("abcdef%02x-0000-4000-8000-%012d", round, 5000+round)
		if i == 1 {
			return strings.ToUpper(key)
		}
		return key
	})
}

func raceDifferentBodies(t *testing.T, keyFor func(round, i int) string) {
	t.Helper()
	f := new169Fixture(t)
	cleanLedger(t, f)
	f.engine.WithKeyLedger(sync.NewPgKeyLedger(f.pool))
	k := f.quotesKit()
	if r := d30Push(t, f, apigen.SyncPushOperation{ClientId: newKey(960), Op: "create", Payload: k.Create("")}); r.Status != "applied" {
		t.Fatalf("create: %+v", r)
	}
	id := pullOf(t, f.engine, "customer_quotes", k.Filter, 0, 500)[0]

	for round := 0; round < 20; round++ {
		start := make(chan struct{})
		var wg gosync.WaitGroup
		results := make([]apigen.SyncPushResult, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				resp, err := f.engine.Push(context.Background(), k.Scope, k.Filter, []apigen.SyncPushOperation{{
					ClientId: keyFor(round, i), Op: "update", Payload: map[string]any{"id": id, "markup_bps": 2000 + 2*round + i},
				}})
				if err != nil || len(resp.Results) != 1 {
					results[i] = apigen.SyncPushResult{Status: "fault"}
					return
				}
				results[i] = resp.Results[0]
			}(i)
		}
		close(start)
		wg.Wait()
		applied, refused := 0, 0
		for _, r := range results {
			switch {
			case r.Status == "applied":
				applied++
			case r.Status == "rejected" && r.Error != nil && len(*r.Error) >= 10 && (*r.Error)[:10] == "key_reused":
				refused++
			}
		}
		if applied != 1 || refused != 1 {
			t.Fatalf("round %d: %d applied, %d key_reused of %+v, want exactly one of each", round, applied, refused, results)
		}
		if m := d30Markup(t, f, id); m != float64(2000+2*round) && m != float64(2000+2*round+1) {
			t.Fatalf("round %d: markup %v is neither body", round, m)
		}
	}
}

// More simultaneous pushes than the pool has connections must neither
// deadlock nor fail: the ledger's lock holds at most half the pool, so the
// stores always find a connection to apply with.
func TestKeyBodyD30ManyConcurrentKeysDoNotStarveThePool(t *testing.T) {
	f := new169Fixture(t)
	cleanLedger(t, f)
	f.engine.WithKeyLedger(sync.NewPgKeyLedger(f.pool))
	k := f.quotesKit()
	if r := d30Push(t, f, apigen.SyncPushOperation{ClientId: newKey(970), Op: "create", Payload: k.Create("")}); r.Status != "applied" {
		t.Fatalf("create: %+v", r)
	}
	id := pullOf(t, f.engine, "customer_quotes", k.Filter, 0, 500)[0]
	n := 3 * int(f.pool.Config().MaxConns)
	var wg gosync.WaitGroup
	statuses := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := f.engine.Push(context.Background(), k.Scope, k.Filter, []apigen.SyncPushOperation{{
				ClientId: newKey(2000 + i), Op: "update", Payload: map[string]any{"id": id, "markup_bps": 3000 + i},
			}})
			if err != nil || len(resp.Results) != 1 {
				statuses[i] = "fault"
				return
			}
			statuses[i] = resp.Results[0].Status
		}(i)
	}
	wg.Wait()
	for i, s := range statuses {
		if s != "applied" {
			t.Fatalf("push %d of %d answered %q, want applied", i, n, s)
		}
	}
}
