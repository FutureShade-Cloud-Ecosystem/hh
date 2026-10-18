package dashboard

// needs_changes_test.go, the Needs You row for the change orders waiting on the manager (0411).
//
// The catalog entry is manager only (its destination, Manager Home's Approvals, is manager gated), the
// shipped policy puts it last on the manager shapes with the approvals source's edges, and no other shape
// carries it. The adapter's counting rule lives in cmd/core beside the adapters (needs_changes_test.go
// there); the migration rows are held by needs_changes_integration_test.go.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestChangesWaitingIsACatalogSourceForManagersOnly(t *testing.T) {
	meta, ok := NeedsSourceByKey[NeedsChangesWaiting]
	if !ok {
		t.Fatalf("%s is not in the needs source catalog", NeedsChangesWaiting)
	}
	if meta.Key != "orders.changes_waiting" {
		t.Errorf("key = %q, the stable key is orders.changes_waiting", meta.Key)
	}
	if meta.What != "Change orders to sign" || meta.Action != "Sign" {
		t.Errorf("words = %q / %q, want %q / %q", meta.What, meta.Action, "Change orders to sign", "Sign")
	}
	if meta.Module != "order" || meta.Unit != "admin" || meta.Screen != "approvals" || meta.View == "" {
		t.Errorf("destination = module %q unit %q screen %q view %q, want order / admin / approvals (Manager Home Approvals) with a view", meta.Module, meta.Unit, meta.Screen, meta.View)
	}
	if !meta.ManagerOnly {
		t.Error("the source must be ManagerOnly: its destination is a manager gated surface")
	}
}

// TestChangesWaitingIsByteAbsentBelowTheManagerTier: curation cannot put it on a counter queue, and the
// bytes of the row, its count and its words never reach a caller below the tier.
func TestChangesWaitingIsByteAbsentBelowTheManagerTier(t *testing.T) {
	policy := map[string][]NeedsPolicy{ProfileCounter: {
		{SourceKey: NeedsChangesWaiting, Position: 0, Visible: true, WarnAt: 1},
		{SourceKey: NeedsParkedTickets, Position: 1, Visible: true, WarnAt: 1},
	}}
	build := func() *Service {
		s := needsService(t, policy)
		if err := s.RegisterNeedsSource(NeedsChangesWaiting, countingSource(4417, time.Time{})); err != nil {
			t.Fatalf("register: %v", err)
		}
		s.RegisterNeedsSource(NeedsParkedTickets, countingSource(6, time.Time{}))
		return s
	}
	allowed, _ := json.Marshal(build().NeedsFor(context.Background(), Scope{Profile: ProfileCounter, CostAllowed: true}))
	for _, want := range []string{NeedsChangesWaiting, "Change orders to sign", "4417"} {
		if !strings.Contains(string(allowed), want) {
			t.Fatalf("positive control failed: %q missing for a manager: %s", want, allowed)
		}
	}
	refused, _ := json.Marshal(build().NeedsFor(context.Background(), Scope{Profile: ProfileCounter, CostAllowed: false}))
	for _, forbidden := range []string{NeedsChangesWaiting, "Change orders to sign", "4417", "Sign"} {
		if strings.Contains(string(refused), forbidden) {
			t.Fatalf("byte %q is present below the manager tier: %s", forbidden, refused)
		}
	}
}

func hasNeed(rows []NeedsPolicy, key string) (NeedsPolicy, bool) {
	for _, r := range rows {
		if r.SourceKey == key {
			return r, true
		}
	}
	return NeedsPolicy{}, false
}

// TestTheShippedPolicyPutsChangesWaitingLastOnTheSigningManagers: the approvals source's edges (warn at 1,
// stop at 5, stale after 240 minutes), appended after the rows the shape already has.
func TestTheShippedPolicyPutsChangesWaitingLastOnTheSigningManagers(t *testing.T) {
	for _, profile := range []string{
		ProfileAdmin, ProfileManager,
		ProfileManagerBranch, ProfileManagerSales, ProfileManagerController, ProfileManagerPayables, ProfileManagerGeneral,
	} {
		rows := defaultNeedsFor(profile)
		got, ok := hasNeed(rows, NeedsChangesWaiting)
		if !ok {
			t.Errorf("%s: the fallback policy lacks %s", profile, NeedsChangesWaiting)
			continue
		}
		if got.Position != len(rows)-1 || rows[len(rows)-1].SourceKey != NeedsChangesWaiting {
			t.Errorf("%s: %s is at position %d of %d, want it appended last", profile, NeedsChangesWaiting, got.Position, len(rows))
		}
		if !got.Visible || got.WarnAt != 1 || got.StopAt != 5 || got.StaleMins != 240 {
			t.Errorf("%s: edges %+v, want visible, warn 1, stop 5, stale 240 (the approvals source's)", profile, got)
		}
	}
	for _, profile := range []string{
		ProfileCounter, ProfileYard, ProfileManagerYard, ProfileManagerLogistics, ProfileManagerProcurement,
	} {
		if _, ok := hasNeed(defaultNeedsFor(profile), NeedsChangesWaiting); ok {
			t.Errorf("%s must not carry %s: it neither signs nor applies change orders", profile, NeedsChangesWaiting)
		}
	}
}
