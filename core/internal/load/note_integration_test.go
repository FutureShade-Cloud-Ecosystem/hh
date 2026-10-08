//go:build integration

// note_integration_test.go proves the landed item's note (HH-LOAD-1
// LD-6): a writer that lands its row but leaves one field out says so
// through Write.Note, and the frame carries the note's code, field and
// detail on the item's created or updated outcome. The note names a
// field and a reason, never a value.
//
// Run (the database URL rides the command line, never a default):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5660/ld6a?sslmode=disable' \
//	  go test -tags integration ./internal/load/... -count=1 -run TestLandedItemNote
package load

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// noteWriter lands the probe row, then notes that one field was left out.
type noteWriter struct{}

func (noteWriter) Apply(ctx context.Context, tx pgx.Tx, in Write) (string, error) {
	id, err := probeWriter{}.Apply(ctx, tx, in)
	if err != nil {
		return "", err
	}
	if in.Note != nil {
		in.Note(Refusal{Code: "field_left_out", Field: "payload", Detail: "the field failed validation and was left out"})
	}
	return id, nil
}

func TestLandedItemNoteRidesTheCreatedAndUpdatedOutcomes(t *testing.T) {
	e := setup(t)
	door := NewDoor(e.store, "probe", noteWriter{}, e.probeAudit)
	ctx := context.Background()
	r, err := door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource, BatchID: "b-note-1", Caller: testActorID,
		Items: []Item{probeItem("L-1", "c-1", "first", "a1000000-0000-4000-8000-000000000001")}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	o := outcomeOf(t, r, "L-1")
	if o.Status != OutcomeCreated || o.Reason != "field_left_out" || o.Field != "payload" || o.Detail == "" {
		t.Fatalf("created outcome %+v, want created carrying the note's reason, field and detail", o)
	}
	r, err = door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource, BatchID: "b-note-2", Caller: testActorID,
		Items: []Item{probeItem("L-1", "c-1", "second", "a1000000-0000-4000-8000-000000000001")}})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	o = outcomeOf(t, r, "L-1")
	if o.Status != OutcomeUpdated || o.Reason != "field_left_out" || o.Field != "payload" {
		t.Fatalf("updated outcome %+v, want updated carrying the note", o)
	}
}
