//go:build integration

package tasks

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestPickDoor_PullServesTheClaimersRoleLabel is UX-75 on the real pull: a claimed pick's row carries
// claimed_by_role (a label), an unclaimed one carries none, and nothing but the label, the claimer's id
// the row has always carried, and no name or email rides with it.
func TestPickDoor_PullServesTheClaimersRoleLabel(t *testing.T) {
	d := newPickDoor(t)
	ctx := context.Background()
	claimed, err := d.store.Create(ctx, d.f, uuid.NewString(), map[string]any{"order_id": uuid.NewString()})
	if err != nil {
		t.Fatalf("create claimed: %v", err)
	}
	free, err := d.store.Create(ctx, d.f, uuid.NewString(), map[string]any{"order_id": uuid.NewString()})
	if err != nil {
		t.Fatalf("create free: %v", err)
	}
	if _, err := d.pool.Exec(ctx, `UPDATE tasks SET claimed_by = $2::uuid, claimed_at = now() WHERE id = $1::uuid`, claimed.ID, d.user); err != nil {
		t.Fatalf("claim: %v", err)
	}
	d.store.WithClaimerRoles(&fakeClaimerRoles{labels: map[string]string{d.user: "Yard Manager"}})

	items, _, err := d.store.Pull(ctx, d.f, 0, 50)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	seen := 0
	for _, it := range items {
		switch it.ID {
		case claimed.ID:
			seen++
			if it.Data["claimed_by_role"] != "Yard Manager" {
				t.Fatalf("claimed row data = %v", it.Data)
			}
			raw, _ := json.Marshal(it.Data)
			for _, leak := range []string{"example.invalid", "pick door leg"} {
				if strings.Contains(string(raw), leak) {
					t.Fatalf("the row leaks %q: %s", leak, raw)
				}
			}
		case free.ID:
			seen++
			if _, has := it.Data["claimed_by_role"]; has {
				t.Fatalf("unclaimed row carries the field: %v", it.Data)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("saw %d of the 2 rows", seen)
	}
}
