package messaging

// personaseed.go seeds the demo personas' House Messages inboxes (House
// Messages core, roles roadmap Lane E): every persona's inbox must answer its
// branch's house channel, and the short pick notice path (the order's rep's
// customer-message DM, where NotifyOrderRep lands a yard short pick) must be
// visible in the rep's inbox before any notice ever fires, or the first thing
// a demo shows is an empty room the path would have filled.
//
// The seeding is BY ROLE, never by user id: the till pair's users are the
// populate's own rows, while the Yard Team and the Driver personas are the
// rows migration 0200 plants and the flows table rows that name them arrive
// on their own branch. Keying the roles means a persona gets its rooms the
// moment its rows land and the populate runs again, and a persona whose rows
// never arrive simply seeds nothing.
//
// The two halves:
//
//   - membership materialized for every ACTIVE holder of a persona role
//     (INSIDE_SALES, SALES_MANAGER, YARD_TEAM, DRIVERS): the derived rows
//     that put the branch's house channels in the inbox. ListMyChannels
//     would derive them on first read anyway; materializing at seed time
//     makes the box's world settled, and SyncMembership is idempotent and
//     self-healing either way.
//   - the rep DM channel ensured for every ACTIVE holder of a rep role
//     (INSIDE_SALES, OUTSIDE_SALES, the sales floor): the exact channel
//     EnsureRepDMChannel provisions on demand, seeded up front so the short
//     pick notice path is visible from the first sign-in.

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// personaRoles are the demo personas' role codes (the roles catalog's own
// spellings, matched case-insensitively by the CITEXT code column).
var personaRoles = []string{"INSIDE_SALES", "SALES_MANAGER", "YARD_TEAM", "DRIVERS"}

// repRoles are the sales floor's role codes: the order's rep is who a yard
// short pick's notice reaches, through the DM channel this seeding ensures.
var repRoles = []string{"INSIDE_SALES", "OUTSIDE_SALES"}

// PersonaSeedReport counts what one seeding pass did, the populate report's
// own shape (zero everywhere on a re-run).
type PersonaSeedReport struct {
	MembershipsSynced int
	RepChannelsSeeded int
}

// SeedPersonaInboxes seeds the persona roles' inboxes over the pool, one user
// at a time, idempotently (both halves converge on their own rows; a re-run
// writes nothing). It runs OUTSIDE the populate's one transaction by design:
// every statement it touches is ON CONFLICT DO NOTHING or a membership sync
// that self-heals, so a partial pass is completed by the next one, and the
// transactional world above it keeps its all-or-nothing promise untouched.
func SeedPersonaInboxes(ctx context.Context, pool *pgxpool.Pool) (PersonaSeedReport, error) {
	var report PersonaSeedReport
	repo := NewRepository(pool)
	users, err := repo.personaUsersByRole(ctx)
	if err != nil {
		return report, err
	}
	for _, role := range personaRoles {
		for _, uid := range users[role] {
			if err := repo.SyncMembership(ctx, uid); err != nil {
				return report, err
			}
			report.MembershipsSynced++
		}
	}
	for _, role := range repRoles {
		for _, uid := range users[role] {
			// Guarded: the ensure's upsert DO UPDATE arm would touch the
			// row on every run, and a re-run of the demo populate must
			// write nothing.
			exists, err := repo.RepDMChannelExists(ctx, uid)
			if err != nil {
				return report, err
			}
			if exists {
				continue
			}
			if _, err := repo.EnsureRepDMChannel(ctx, uid); err != nil {
				return report, err
			}
			report.RepChannelsSeeded++
		}
	}
	return report, nil
}
