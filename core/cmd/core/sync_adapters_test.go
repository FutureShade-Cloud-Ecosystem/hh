package main

// sync_adapters_test.go: the HH-193 actor resolution of the sync audit
// seam. A staff caller resolves through the linker into the uuid actor
// column; a caller the linker does not know (a tenant, contractor) is
// recorded by platform subject under the actor_subject key and the uuid
// column stays empty, never holding a non-uuid.

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

type syncRecorderDouble struct{ got []audit.SensitiveAccess }

func (r *syncRecorderDouble) Record(_ context.Context, a audit.SensitiveAccess) error {
	r.got = append(r.got, a)
	return nil
}

func TestSyncAuditSeamActorResolution(t *testing.T) {
	const staffUser = "11111111-2222-4333-8444-555555555555"
	link := func(_ context.Context, sub string) (string, bool) {
		if sub == "fxstaff-any" {
			return staffUser, true
		}
		return "", false
	}
	for _, c := range []struct {
		name, actor, wantID, wantSubject string
	}{
		{"staff links to users.id", "fxstaff-any", staffUser, ""},
		{"tenant keeps its subject", "fxportal-alpha", "", "fxportal-alpha"},
		{"no actor records neither", "", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := &syncRecorderDouble{}
			seam := syncAuditSeam{svc: rec, link: link}
			if err := seam.RecordSync(context.Background(), sync.AuditEntry{
				Action: "SYNC_PUSH", Scope: "pick_tasks", Op: "delete", ClientID: "c", Outcome: "applied", Actor: c.actor,
			}); err != nil {
				t.Fatal(err)
			}
			if len(rec.got) != 1 {
				t.Fatalf("%d rows", len(rec.got))
			}
			a := rec.got[0]
			subject, _ := a.Extra["actor_subject"].(string)
			if a.ActorID != c.wantID || subject != c.wantSubject {
				t.Fatalf("actor_id %q subject %q, want %q and %q", a.ActorID, subject, c.wantID, c.wantSubject)
			}
		})
	}
}
