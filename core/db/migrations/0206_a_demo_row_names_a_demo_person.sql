-- 0206 — the demo world had rows and it had people, and nothing joined them.
--
-- APPROVED by the commander, 2 Sep 2026 (max(branch) 0205 + 1, confirmed with
-- `ls backend/migrations | tail -1` at the moment of writing — the standing rule, and the brief's
-- stated block is not the authority for it).
--
-- ── THE DEFECT, IN TWO PLACES A PERSON CAN SEE ──────────────────────────────────────────────────
--
-- The demo world was built by TWO MECHANISMS THAT WERE NEVER JOINED, and each is right about its
-- own half:
--
--   0200_tour_demo_personas.sql   a MIGRATION, so it reaches staging. Plants three logins —
--                                 dispatch.demo@, yard.demo@, driver.demo@dibbits.example — with
--                                 roles and branch grants. AND NO WORK.
--   backend/cmd/demoseed          a COMMAND, run by hand. Plants 4 drivers, 7 runs, 17 stops, 17
--                                 pick tasks, 3 shortages, orders, invoices, a scan trail. AND NO
--                                 PEOPLE.
--
-- Neither writes the EDGE. Concretely, and both of these are on a screen rather than in a table:
--
--   1. dispatch_drivers went in with NO user_id. So DriverForUser — `SELECT … FROM dispatch_drivers
--      WHERE user_id = $1 AND is_active` (dispatch/repository.go:177) — returns pgx.ErrNoRows for
--      driver.demo@, MyRuns turns that into ErrNotFound, and the driver surface renders, correctly
--      and uselessly, "You are not set up as a driver at this branch." The one persona 0200 planted
--      for "driver at phone width" cannot open a run sheet.
--
--   2. tasks went in with a claimed_at and a NULL claimed_by. 0073 gave the table BOTH columns
--      (0073_tasks_and_task_events.sql:32-33) and the seed wrote only the stamp, so nine of
--      seventeen demo tasks say "somebody accepted this at 08:00" with nobody's id on them. Every
--      read that carries claimed_by through — yard_repository.go:69 → tasks/model.go:108 → the yard
--      console — has an empty string where a name belongs, and the yard shape's Needs-you widget,
--      which is the whole reason 0204 stopped taking that persona over, has nothing to point at.
--
-- ── WHY THIS IS A MIGRATION *AND* A CHANGE TO THE COMMAND, AND NOT EITHER ALONE ─────────────────
--
-- Both halves are needed and neither substitutes for the other. The argument is short in each
-- direction:
--
--   THE COMMAND CANNOT REPAIR A DATABASE IT HAS ALREADY SEEDED. apply.go can emit exactly one kind
--   of statement — `INSERT … ON CONFLICT … DO NOTHING` — and its line 3 calls that "a structural
--   guarantee rather than a discipline", with TestApply_EmitsOnlyConflictYieldingInserts holding it
--   there. So on every database demoseed has already run against, the driver row and the task rows
--   conflict, DO NOTHING, and the edge is never written. That is the correct behaviour for a command
--   pointed at a database live testers are using, and it is why the repair belongs here.
--
--   THIS FILE CANNOT MAKE A FRESH WORLD CORRECT AT BIRTH. A migration runs BEFORE the command that
--   plants the rows it would fix, so on a from-zero install this file matches nothing at all and the
--   world demoseed writes an hour later would be wrong again. The command is therefore changed to
--   write user_id and claimed_by at INSERT time (plan.go's demoDrivers ruling; build.go's picking()).
--
-- And the reason the repair is a MIGRATION rather than a scripts/ fixture is 0200's, unchanged:
-- scripts/staging_apply.sh runs ./bin/migrate and NEVER ./bin/seed, so anything outside the
-- migration chain is absent from the one box a demo is actually shown from. 0200's header makes this
-- argument for its own personas; this file is the other end of the same wire.
--
-- ── EVERY PERSON IS RESOLVED BY EMAIL. THE ONE ROW THAT IS NOT IS NOT A PERSON ──────────────────
--
-- 0200 plants its users with fixed ids (d0200…0001/2/3) and then guards the insert with
-- `ON CONFLICT (email) DO NOTHING`. Its own section 2 spells out the consequence: where the email
-- was ALREADY present, the pre-existing row KEEPS ITS OWN id and 0200's literal is never written —
-- "so a fixed-uuid reference would violate user_roles_user_id_fkey". Every statement below therefore
-- joins `users` on the email and takes whatever id that database carries. A hard-coded d0200…0003
-- would work on a from-zero box and break on precisely the deployments where somebody had already
-- made that account by hand.
--
-- THE DRIVER ROW IS ADDRESSED BY A LITERAL UUID, AND THAT IS NOT THE SAME HAZARD. dispatch_drivers
-- has no metadata column, so the demoseed marker cannot scope it (ids.go:45,
-- demo_seed = 'hos-demoseed-v1', is written into metadata JSONB and dispatch_drivers has none). What
-- it does have is an id the command DERIVES rather than mints: demoID("driver:D1") is
-- uuid_v5(d3305eed-0000-4000-9000-000000000000, 'driver:D1') = cf168361-3c3d-5967-b275-d7ffdfc730dd,
-- the same value on every machine and in every year, and ids.go:57 makes those keys append-only for
-- exactly this reason. So the literal below is not "a guess at which row" — it is the row's name,
-- written out. A demoseed test reads this file and re-derives both uuids, so the two cannot drift.
--
-- ── THE THIRD STATEMENT: THERE WERE TWO PRIYAS ─────────────────────────────────────────────────
--
-- demoseed shipped "speaker-0003 (demo)" as its Trenton driver while 0200 shipped "speaker-0002
-- (demo — Driver)" as the driver LOGIN. speaker-0008 in one demo world, only one of whom anybody can
-- sign in as, is a demo defect on its own — and it stops being cosmetic the moment the login is
-- attached to a driver record, because dispatch-board.ts:495 and dispatch-loadboard.ts:1141 both
-- print `run.driver_name` on the crew line. Without the rename, a dispatcher on the load board
-- watches "Scrubname0020 Scrubname0004 (demo)" drive the very run the person signed in as speaker-0002 has open on a
-- phone: one human, two names, two screens, in the same walkthrough.
--
-- The reconciliation is recorded in full under demoDrivers in cmd/demoseed/plan.go and is the same
-- on both sides of the wire: the persona takes D1 (which owns a completed run, one TODAY and one
-- tomorrow — the walkable set), D1 takes the persona's name, and the other Priya keeps only her
-- surname. The record reads "speaker-0002 (demo)" and the login reads "speaker-0002 (demo — Driver)":
-- the same human under each surface's own convention, since 0200's suffix marks WHICH PERSONA A
-- LOGIN IS and on a crew line would read as a label wearing its own caption.
--
-- ── SAFETY ──────────────────────────────────────────────────────────────────────────────────────
--
-- Three UPDATEs, no INSERT, no DELETE, and no schema change of any kind.
--
--   ABSENT DEMO ROWS → NO-OP. Every statement joins `users` on a demo email and every one is scoped
--   to a demoseed-planted row, so a deployment with no personas, or with personas and no demo world,
--   matches zero rows in all three. That is the ordinary case on a from-zero CI run, and it is why
--   this file cannot be verified by a from-zero run alone — see the VERIFY note in the session
--   report.
--
--   A SECOND RUN IS A NO-OP BY CONSTRUCTION. Each WHERE excludes its own result: the link requires
--   user_id IS NULL, the rename requires the OLD name, the claim requires claimed_by IS NULL. This
--   is 0205's discipline ("the WHERE clause excludes its own result") applied three times.
--
--   A HUMAN'S EDIT SURVIVES. The same three predicates mean a driver somebody has already linked to
--   a real login, a driver somebody has renamed, and a task somebody has actually claimed are all
--   left exactly as they are. Nothing here overwrites a value a person chose.
--
-- REVERT COST: three UPDATEs the other way — NULL the user_id on that one driver row, put the two
-- name literals back, and NULL claimed_by on the demo tasks — plus reverting the two column
-- additions in cmd/demoseed (plan.go's Persona field, build.go's two appends). No row is created or
-- destroyed either way, so a revert loses nothing but the edge itself.

-- ───────────────────────── 1 · the driver persona reaches a driver record ─────────────────────────
-- The user by EMAIL (0200 §2's lesson); the driver by its derived id (see the header). user_id IS
-- NULL keeps this off a driver somebody has already linked and makes the second run match nothing.
UPDATE dispatch_drivers d
   SET user_id    = u.id,
       updated_at = now()
  FROM users u
 WHERE u.email     = 'driver.demo@dibbits.example'
   AND d.id        = 'cf168361-3c3d-5967-b275-d7ffdfc730dd'::uuid  -- demoID("driver:D1")
   AND d.user_id IS NULL;

-- ───────────────────────── 2 · one Priya, and she is the one with the password ─────────────────────
-- Data-driven and keyed on the value it moves FROM, which is 0205's shape and gives idempotence for
-- free. A driver a human has since renamed does not match either literal and is left alone.
WITH renamed(driver_id, was, becomes) AS (
    VALUES ('cf168361-3c3d-5967-b275-d7ffdfc730dd', 'Scrubname0020 Scrubname0004 (demo)',  'speaker-0002 (demo)'),
           ('6543d360-6375-51d8-bbdc-7cceacce4292', 'speaker-0003 (demo)', 'Ravi Scrubname0022 (demo)')
)
UPDATE dispatch_drivers d
   SET name       = r.becomes,
       updated_at = now()
  FROM renamed r
 WHERE d.id   = r.driver_id::uuid
   AND d.name = r.was;

-- ───────────────────────── 3 · every claim stamp gets a name ──────────────────────────────────────
-- THE PREDICATE IS THE ORPHANED STAMP, NOT A LIST OF STATUSES. It reaches ACCEPTED, IN_PROGRESS,
-- STAGED and DONE today because those are the four lanes demoseed stamps a claimed_at on, and it
-- will reach a fifth without anybody having to remember this file. build.go's picking() applies the
-- identical rule at insert time, so the command and this migration cannot disagree about what a
-- claimed task is.
--
-- SCOPED BY THE DEMOSEED MARKER, through the order the task hangs off: tasks has no metadata column
-- of its own, orders does, and every demo order carries {"demo_seed":"hos-demoseed-v1"} (ids.go:45).
-- So this reaches demo picks and nothing else — a real task with a real claim stamp is doubly
-- protected, by the marker and by claimed_by IS NULL.
UPDATE tasks t
   SET claimed_by = u.id,
       updated_at = now()
  FROM users u
 WHERE u.email        = 'yard.demo@dibbits.example'
   AND t.claimed_at  IS NOT NULL
   AND t.claimed_by  IS NULL
   AND EXISTS (SELECT 1
                 FROM orders o
                WHERE o.id                    = t.order_id
                  AND o.metadata->>'demo_seed' = 'hos-demoseed-v1');

COMMENT ON COLUMN dispatch_drivers.user_id IS
  'The staff login this driver record belongs to, or NULL. NULL is an ordinary state and not a gap — yards run casual and contract drivers who will never hold one (0090). It is also the ONE thing the driver surface reads to decide whose day to show: DriverForUser matches on it, and a driver with no login gets "You are not set up as a driver at this branch" rather than an empty run sheet. The demo world''s driver record was linked to 0200''s driver.demo@dibbits.example persona by migration 0206; cmd/demoseed writes it at insert time for worlds seeded after that.';

COMMENT ON COLUMN tasks.claimed_by IS
  'Scrubname0033 accepted this task, stamped alongside claimed_at when the status moves to ACCEPTED. The two travel together: a claimed_at with a NULL claimed_by is a row that contradicts itself — a claim with nobody on it — which is what the demo world carried on every ACCEPTED, IN_PROGRESS, STAGED and DONE pick until migration 0206 named 0200''s yard.demo@dibbits.example persona on them. Distinct from assigned_to, which is the PUSH seam and is still unused (0073).';
