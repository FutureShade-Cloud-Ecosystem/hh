//go:build demo

package main

// demoworld_seqstream_test.go: the demo seq repair numbers will_call_pickups from the same stream every
// live writer of that table draws (UX-72). Two streams over one table let a device feed skip a row: the
// repair's rows and a live writer's rows would interleave out of order. The live writers' scope is
// dispatch.WillCallSeqScope (the constant allocateSyncSeqTx draws for the staff and contractor paths),
// imported here, not repeated as a literal; the legacy contractor counter is "pickups", which nothing
// advances any more.

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dispatch"
)

func TestDemoSeqRepair_WillCallPickupsDrawsTheLiveWritersStream(t *testing.T) {
	found := 0
	for _, s := range demoSeqTables {
		if s.table != "will_call_pickups" {
			continue
		}
		found++
		if got := s.scopeName(); got != dispatch.WillCallSeqScope {
			t.Errorf("the repair draws will_call_pickups from scope %q, the live writers draw %q", got, dispatch.WillCallSeqScope)
		}
		if s.scopeName() == "pickups" {
			t.Errorf("the repair draws will_call_pickups from the legacy pickups counter")
		}
	}
	if found != 1 {
		t.Fatalf("will_call_pickups appears %d times in demoSeqTables, want 1", found)
	}
}
