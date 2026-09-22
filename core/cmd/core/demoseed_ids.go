// ids.go, how a demo row is NAMED, and how it is RECOGNISED afterwards.
//
// THE WHOLE COMMAND RESTS ON THIS FILE. demoseed runs against a database that live testers are
// using for real homework. It may therefore never wipe, reset, truncate or reseed, never delete,
// and never update a row a human touched. The only way to write into a shared database and still
// promise that is to make every write CREATE-IF-ABSENT on a key the command can derive from
// nothing but itself, so a second run recognises its own first run's rows and declines to write
// them again, and so a row it never created is a row it never sees.
//
// The key is a UUIDv5 (RFC 4122 name-based, SHA-1) over a fixed namespace and a stable string.
// demoID("order:KGN:0007") is the same UUID on this machine, on staging, and next year. That gives
// three properties at once:
//
//  1. IDEMPOTENCY. Every INSERT carries an explicit id and ends in ON CONFLICT DO NOTHING. Run
//     twice, and the second run's inserts all conflict and write nothing. Row counts are identical.
//  2. NON-DESTRUCTIVENESS. There is no UPDATE and no DELETE anywhere in this command. A human's
//     row cannot be touched, because the only statement kind the applier can emit is an INSERT
//     that yields on conflict.
//  3. IDENTIFIABILITY. Every row is either derivable (its id falls out of the plan) or greppable
//     (its human-readable key is DEMO-prefixed, its metadata carries the marker). `demoseed -plan`
//     prints the full id list, which doubles as the removal manifest if the world is ever unwanted.
//
// demoseed_ids carries the pin's cmd/demoseed/ids.go (the fold:
// one-shot subcommand of cmd/core, plan and apply modes preserved).
package main

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/google/uuid"
)

// demoNamespace is the UUIDv5 namespace for everything this command writes. It is a constant of the
// command, not a config knob: changing it orphans every previously-seeded row (they stop being
// recognised, so a re-run would create a SECOND world beside the first). If a future wave needs a
// second, separable demo world, it gets a second namespace AND a second marker, never a mutation
// of this one.
var demoNamespace = uuid.MustParse("d3305eed-0000-4000-9000-000000000000")

// demoMarker is written into every metadata JSONB the seed touches, so the world is findable in SQL
// without knowing the plan:
//
//	SELECT * FROM orders WHERE metadata->>'demo_seed' = 'hos-demoseed-v1';
//
// The version suffix is deliberate. A later wave that seeds a different world writes v2 and the two
// remain distinguishable, which is what "removed by its own marker" requires.
const demoMarker = "hos-demoseed-v1"

// demoMetadata is the marker as a JSON literal, ready to hand to a jsonb column.
const demoMetadata = `{"demo_seed":"` + demoMarker + `"}`

// demoPrefix is the prefix on every human-readable key the seed mints, SKUs, account numbers,
// order numbers, invoice numbers, run numbers, truck codes. A tester who sees it on a screen knows
// what they are looking at without asking anybody.
const demoPrefix = "DEMO-"

// demoID derives the stable id for one seeded row. key is a colon-delimited path naming the row's
// role in the world ("customer:C01", "stop:run3:2"), and it is part of the contract: changing an
// existing key changes that row's identity, which on an already-seeded database means a duplicate
// rather than an edit. Keys are append-only.
func demoID(key string) string {
	return uuid.NewSHA1(demoNamespace, []byte(key)).String()
}

// -- THE ONE KIND OF ROW THAT IS DELIBERATELY RE-MINTED -------------------------------------------
//
// EVERYTHING ABOVE FREEZES A ROW FOREVER, AND FOR THE "TODAY" TILES THAT IS THE BUG. Every date in
// this plan is computed relative to the injected clock, and every id is derived from a key that
// carries no date, so the FIRST apply decides what "today" means and the second, third and
// hundredth apply recognise those rows and write nothing. The commander auto-applies on every
// deploy, which is idempotent (good) and means that on the day after the first apply "Sales today"
// is $0 and "Deliveries today" is 0 on a box people look at weekly. The demo goes dark, and the two
// widgets it goes dark on are the two Home is built around.
//
// A DAY-KEYED ROW PUTS THE DATE INTO THE KEY. demoID(dayKey("20260903", "sale-order:T1")) is a
// different uuid from the same row on the 4th, so each deploy INSERTS that day's rows and every
// previous day's rows stay exactly where they are, as history. Nothing about the applier changes:
// still one INSERT … ON CONFLICT DO NOTHING per row, still idempotent WITHIN a day (two deploys on
// the 3rd propose the identical id and the second creates nothing), still deterministic for a given
// clock. What changes is only which question the key answers, "the demo world's sale S01" for a
// stable key, "the demo world's sale on the 3rd" for this one.
//
// THE COST IS REAL AND IT IS BOUNDED BY THE COUNT, WHICH IS WHY THE COUNT IS SMALL. These rows grow
// without limit: one apply per day forever. The honest trade is a handful of rows a deploy, a
// counter sale and an unassigned delivery per branch, eighteen rows, not a fixture a day. At that
// size a year of daily deploys is about 6,500 rows in tables that already hold hundreds, and a
// tester can still tell the demo world from their own work by its DEMO- numbers.
//
// AND IT IS APPLIED TO THE "TODAY" SURFACES AND NOTHING ELSE. The seven-run fleet, the four trucks,
// the four drivers, the ten-product catalogue, the twenty-eight-order book, the eighteen invoices
// and the pricing configuration are all STABLE-keyed and must stay that way: re-creating those every
// deploy would not refresh the demo world, it would build a new one beside it every week, a
// different set of trucks, a different order book, a customer's aging split across fifty accounts.
// A tile that reads a POPULATION wants the population to hold still. Only a tile that reads a DAY
// needs a row dated today, and there are exactly four such reads: sales.today and deliveries.today
// in the metric catalog, and dispatch.unassigned / dispatch.due_to_commit in the Needs-you queue,
// both of which count unassigned stops inside a working window that starts at today.
//
// `demoseed -plan` prints every day-keyed key it minted, so which rows these are is a fact somebody
// reads off the command rather than out of this comment.
func dayKey(day, key string) string { return "today:" + day + ":" + key }

// demoRef derives a short opaque reference from the same key space, used where the schema wants a
// text handle rather than a UUID (a POD signature ref, a will-call token). Same determinism, same
// reason.
func demoRef(prefix, key string) string {
	return prefix + demoID(key)[:8] + demoID(key)[9:13]
}

// demoTokenHash is the sha256 hex a will-call pickup stores instead of its raw token. The raw token
// is derived, not random, because a demo pickup nobody can look up is a demo pickup nobody can
// test: `demoseed -plan` prints the raw tokens next to their pickups.
func demoTokenHash(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}
