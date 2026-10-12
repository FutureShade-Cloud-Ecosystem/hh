//go:build demo

// persona_inbox_test.go: every demo persona's House Messages inbox, read
// through the messaging module's own list, after the demo pipeline (migrate,
// demoseed, demo-populate) has built the box. Red first on the rep's leg:
// before the populate seeded the persona inboxes, the Sales persona's inbox
// answered the branch house channel (derived membership) but NOT the
// customer-message DM channel the short pick notice path lands in, which is
// the recorded red evidence; the other personas' derived rooms passed
// already, and stay pinned here so they cannot drift.

package demo

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messaging"
)

// The demo personas' fixed world: the populate's two role accounts (Trenton
// grants) and migration 0200's two tour personas (all-branch grants), beside
// the seeded channels' slugs the inboxes must answer.
const (
	personaSalesUserID  = "de110000-0000-4000-9000-000000000001"
	personaManagerID    = "de110000-0000-4000-9000-000000000002"
	personaYardUserID   = "d0200000-0000-4000-9000-000000000002"
	personaDriverUserID = "d0200000-0000-4000-9000-000000000003"
)

// personaInbox reads one persona's channel list through the module's own
// service, the same read the hub's first screen makes.
func personaInbox(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID string) map[string]bool {
	t.Helper()
	repo := messaging.NewRepository(pool)
	chans, err := messaging.NewService(repo).ListMyChannels(ctx, messaging.Caller{UserID: userID})
	if err != nil {
		t.Fatalf("persona inbox read: %v", err)
	}
	slugs := map[string]bool{}
	for _, c := range chans {
		if c.Slug != "" {
			slugs[c.Slug] = true
		}
	}
	return slugs
}

// TestPopulate_PersonaInboxesAnswerTheirRooms: each persona's inbox answers
// its branch's house channel (Trenton for the till pair, both yards for the
// tour personas) and the rep persona's inbox carries the customer-message DM
// the short pick notice path lands in, visible before any notice fires.
func TestPopulate_PersonaInboxesAnswerTheirRooms(t *testing.T) {
	ctx := context.Background()
	pool, dsn := freshMigratedDB(t, "persona_inbox")
	bin := buildDemoCore(t)
	seedAndPopulate(t, bin, dsn)

	slugsOf := func(id string) map[string]bool {
		t.Helper()
		return personaInbox(t, ctx, pool, id)
	}

	// The Sales persona: the branch house channel and the short pick notice
	// path, both visible in the one inbox.
	sales := slugsOf(personaSalesUserID)
	if !sales["house-point-loma"] {
		t.Errorf("the Sales persona's inbox missed its branch's house channel: %v", sales)
	}
	hasRepDM := false
	for slug := range sales {
		if len(slug) > 4 && slug[:4] == "rep-" {
			hasRepDM = true
		}
	}
	if !hasRepDM {
		t.Errorf("the Sales persona's inbox carries no rep DM channel, so the short pick notice path is invisible until a notice fires: %v", sales)
	}

	// The Sales manager: the branch house channel.
	manager := slugsOf(personaManagerID)
	if !manager["house-point-loma"] {
		t.Errorf("the Sales manager's inbox missed its branch's house channel: %v", manager)
	}

	// The Yard Team persona (all-branch grants): both yards' house channels.
	yard := slugsOf(personaYardUserID)
	if !yard["house-point-loma"] || !yard["house-julian"] {
		t.Errorf("the Yard persona's inbox missed a branch house channel: %v", yard)
	}

	// The Driver persona: both yards' house channels and both drivers' rooms.
	driver := slugsOf(personaDriverUserID)
	if !driver["house-point-loma"] || !driver["house-julian"] {
		t.Errorf("the Driver persona's inbox missed a branch house channel: %v", driver)
	}
	if !driver["drivers-point-loma"] || !driver["drivers-julian"] {
		t.Errorf("the Driver persona's inbox missed its role-scoped rooms: %v", driver)
	}
}
