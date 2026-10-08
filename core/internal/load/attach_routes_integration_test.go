//go:build integration

// attach_routes_integration_test.go proves the door's create arm answers
// attached on either route a writer can use to say its create arm bound
// an existing row: LD-3's AttachReporter seam (ApplyAttached reports the
// fact through its return value) and LD-6's Write.Attached callback.
//
// Run (the database URL rides the command line, never a default):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5841/integration_gate?sslmode=disable' \
//	  go test -tags integration ./internal/load/... -count=1 -run TestCreateArmAnswersAttached
package load

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// seamWriter binds through the AttachReporter seam: the fact rides the
// return value and the callback is never called.
type seamWriter struct{}

func (w seamWriter) Apply(ctx context.Context, tx pgx.Tx, in Write) (string, error) {
	id, _, err := w.ApplyAttached(ctx, tx, in)
	return id, err
}

func (seamWriter) ApplyAttached(ctx context.Context, tx pgx.Tx, in Write) (string, bool, error) {
	id, err := probeWriter{}.Apply(ctx, tx, in)
	return id, true, err
}

// callbackWriter binds through Write.Attached and carries no seam.
type callbackWriter struct{}

func (callbackWriter) Apply(ctx context.Context, tx pgx.Tx, in Write) (string, error) {
	id, err := probeWriter{}.Apply(ctx, tx, in)
	if err != nil {
		return "", err
	}
	if in.Attached != nil {
		in.Attached()
	}
	return id, nil
}

func TestCreateArmAnswersAttachedOnBothRoutes(t *testing.T) {
	for _, c := range []struct {
		name   string
		writer Writer
	}{
		{"seam", seamWriter{}},
		{"callback", callbackWriter{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t)
			door := NewDoor(e.store, "probe", c.writer, e.probeAudit)
			r, err := door.UpsertBatch(context.Background(), Batch{OrgID: testOrgID, SourceSystem: testSource, BatchID: "b-route-1", Caller: testActorID,
				Items: []Item{probeItem("L-1", "c-1", "first", "a1000000-0000-4000-8000-000000000001")}})
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if o := outcomeOf(t, r, "L-1"); o.Status != OutcomeAttached {
				t.Fatalf("the %s route answered %q, want %s", c.name, o.Status, OutcomeAttached)
			}
			if r.Counts[OutcomeAttached] != 1 || r.Counts[OutcomeCreated] != 0 {
				t.Fatalf("counts %+v, want one attached and none created", r.Counts)
			}
		})
	}
}
