package main

// sync_adapters_test.go: the HH-193 actor resolution of the sync audit
// seam. A staff caller resolves through the linker into the uuid actor
// column; a caller the linker does not know (a tenant, contractor) is
// recorded by platform subject under the actor_subject key and the uuid
// column stays empty, never holding a non-uuid.

import (
	"context"
	"math/rand"
	"regexp"
	"strings"
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
		{"tenant keeps its subject, separator-encoded", "fxportal-alpha", "", "fxport:al-alp:ha"},
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

// digitRun7 is the audit chokepoint's redaction trigger: seven or more
// consecutive decimal digits.
var digitRun7 = regexp.MustCompile(`[0-9]{7,}`)

// A subject the cleartext detector would redact raw (a hex id holding a
// digit run) must leave the adapter encoded so that no run survives, and
// stay lossless: stripping the colons returns the subject exactly.
func TestSyncAuditSeamSubjectEncodingNeverTripsTheDetector(t *testing.T) {
	rng := rand.New(rand.NewSource(193))
	const hexdigits = "0123456789abcdef"
	const idChars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ._-"
	gen := func(alphabet string, n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteByte(alphabet[rng.Intn(len(alphabet))])
		}
		return b.String()
	}
	subjects := []string{"6512345678a1b2c3d4e5", strings.Repeat("9", 36), "12345678901234567890"}
	for i := 0; i < 300; i++ {
		subjects = append(subjects, gen(hexdigits, 20), gen(idChars, 36), gen(hexdigits, 36))
	}
	for _, sub := range subjects {
		rec := &syncRecorderDouble{}
		seam := syncAuditSeam{svc: rec, link: func(context.Context, string) (string, bool) { return "", false }}
		if err := seam.RecordSync(context.Background(), sync.AuditEntry{Action: "SYNC_PUSH", Scope: "orders", Op: "delete", ClientID: "c", Outcome: "applied", Actor: sub}); err != nil {
			t.Fatal(err)
		}
		got, _ := rec.got[0].Extra["actor_subject"].(string)
		if digitRun7.MatchString(got) {
			t.Fatalf("subject %q encodes to %q, which still holds a 7-digit run", sub, got)
		}
		if strings.ReplaceAll(got, ":", "") != sub {
			t.Fatalf("subject %q encodes to %q, not reversible by stripping colons", sub, got)
		}
	}
}
