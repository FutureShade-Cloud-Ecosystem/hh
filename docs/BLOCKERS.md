# Blockers

Lane-by-lane record of anything that blocked or redirected work in this
repository. Newest at the bottom; never edit another lane's entry.

## HH-2.1 (skeleton)

- **No `fb` CLI exists yet.** The lane acceptance calls for `fb check` to exit
  0 on the empty skeleton, but `fb` is a platform deliverable that has not been
  built and there is no template repository to take it from. The lane provides
  `scripts/check-shape.sh` as the in-repo stand-in (it validates the App Shape
  layout, the manifest header, the `ci` job contract and the no-dashes house
  rule) and the `ci` job runs it. When the real `fb check` lands, replace the
  script call in `.github/workflows/ci.yml` and delete this entry.
- **`hardscapeos_dibbits` carries no `CLAUDE.md` on `master` or `Demo`.** The
  lane brief says to read it at the repository root. The file exists only on
  the `fb/*` and `Feedback9_08_swarm` branch tips, byte-identical across all of
  them (blob `3d396ab1`), so the invariants were read from that blob. If a
  different revision was intended, diff `AGENTS.md` against it when it appears.

## HH-2.2: `fb check` cannot be executed yet

Lane HH-2.2's acceptance includes "`fb check` accepts the manifest". The `fb`
CLI does not exist yet: there is no template repository and no platform binary
in this session, and HH-2.1's skeleton (which carries the `ci` workflow and any
first-party check shim) lands on `qa/HH-2` only at integration time.

Mitigation in this lane: the manifest's structural conformance is covered by
`scripts/manifest-catalog.test.ts`, which asserts the App Shape identity fields
(`shape: 1`, `app: hh`, the runtimes list, `database.extensions` with `pg_trgm`
and `citext`), full catalog parity for every frontend, and byte-identical
regeneration. `scripts/generate-manifest.ts --check` is the drift gate the `ci`
job can run once HH-2.1's workflow exists. When a real `fb check` is available
it must be run against `manifest.yaml` and this note removed.

## HH-3 preflight

- **Where the D25 reassignment stopped: Colton's review outcome.**
  - Accepted: unassigned approvals, sign-offs and read-backs on the checkpoint lines now name Colton.
  - Accepted: dealer and delivery staff left alone; the sales manager, the finance controller and the droplet operator each give their own sign-off.
  - Accepted: the "switched and observed" outcome lines in HH-6 and HH-10 left alone.
  - Overruled: the HH-4 acceptance lines "Grant can open" and "Grant opens" now name Colton (addendum item 6).

## HH-3.1 (schema)

- **Pending D32: client-identifying fixture text in the carried baseline.**
  Item 2 (carry `hardscapeos_dibbits` `master` 6eaea3ec, `backend/migrations`
  0001 to 0212) is held. The five baseline migrations that write customer rows
  were classified locally before any carry; no value is recorded here.
  - `0029`: synthetic. One customer literal, a generic house walk-in
    placeholder. No person, branch or address.
  - `0046`: synthetic in its SQL values (a patterned UAT tester name, an email
    on a reserved `.example` domain, the customer by id only). Its header
    comment names one seeded contractor business whose name is not obviously
    invented: a real regional place name plus a trade.
  - `0049`: production-shaped. One job address with a house number on a real
    street in one of the client's branch towns. The project name is invented;
    the customer is referenced by id; the header comment repeats the contractor
    name from `0046`.
  - `0055`: production-shaped on one job address (a house number on a real
    street in a branch town). The customer and portal user are client-named
    demo labels, the email is on a reserved domain, and the billing address
    carries city and province only.
  - `0057`: production-shaped. Three customer literals labelled demo (two
    contractor names built on real regional place names, one homeowner shaped
    as an initial and a common surname); eight job addresses with house numbers
    on real streets in the branch towns and one neighbouring town; two emails
    on live manufacturer domains (public vendor warranty contacts, not customer
    data); branch town names in comments.
  - Distinct identifying literals across the five: customer 6 (5 identities,
    one contractor appearing bare in comments and with a demo suffix in
    `0057`); person 3 names (one also a customer row) and 4 emails (2 reserved,
    2 live vendor); branch 2 (town names in comments and addresses; SQL refers
    to branches by id); address 10 street addresses plus 1 city-only billing
    address. Not counted: the six manufacturer names and two vendor portal
    domains, which are the dealer's public supplier catalogue.
  - `Demo`'s `0222` selects its three customer renames on a name literal
    (`WHERE name =`), and likewise its 17 stop addresses and 4 run notes. None
    of those literals appears in the five files, in any `master` baseline
    migration or in `cmd/seed/dibbits_seed.sql`; all of them are in `master`'s
    `cmd/demoseed`, which is not carried. `0224` selects by id.
  - Why it stops here: a scrub overrides the brief's "unedited" rule, and
    migrations are never edited once committed, so what item 2 commits stays in
    `hh` history. Identifying text also sits in comments, so a scrub must cover
    comments as well as values and predicates.
  - Unblocked by: Colton recording D32 (scrub fixture values in the carried
    baseline, or carry verbatim).
  - Closed: D32 is recorded and the scrub ran under it. The carried baseline
    applies from zero, the ERP's own gate suite passes against the scrubbed
    seeded database, and the search for every mapped original returns zero.
    Sixty files carry the scrub, recorded with ERP blob hashes in
    core/db/PORTED.md. The replacement map was workstation-local, is not
    committed, and is discarded.
- **Open: additive migrations against a restored dump (Phase B precondition).**
  Phase A's fixture is the migrations applied from zero plus the ERP's own
  seed. The check that the additive set applies cleanly on top of a restored
  HardscapeOS dump has not been run. D31 records it as open and a
  precondition of Phase B, which does not start until it has been run
  against a restored dump and reported.
  - Closed: ran against a restored dump, the staging droplet's pre-migrate
    deploy backup at the pinned baseline (migration head 0212; the live
    staging database and `origin/master` have both advanced past the pin,
    recorded under HH-3.3 below). Restore into a fresh scratch database:
    261 public tables, 0 restore error classes, and the dump's shape exactly
    the carried baseline's (verified column by column before anything was
    applied, by the session checker at `tools/d31-check/run.sh`). Additive
    apply: 0 files with errors, 12 of 12 additive tables present after the
    apply. Precondition met.
- **Open: special-order migration against the dump's synthetic skus (Phase B
  precondition).** D16 requires the special-order migration to parse every
  synthetic sku in a restored HardscapeOS dump. With no restored dump
  available this check cannot run in Phase A. D31 records it as open and a
  precondition of Phase B, which does not start until it has been run
  against a restored dump and reported.
  - Closed: ran over the same restored dump. Special-order products: 0;
    flagged lines: 0; lines on special-order products without the flag: 0.
    Synthetic skus (the `SO-` prefix HH Pro documents at
    `isSpecialOrderSku`): 0 present across every sku-bearing column in the
    dump (products, price_change_lines, pricing_order_lines, task_pick_lines,
    task_shortages, commission_run_lines, purchasing_vendor_skus); 0 parsed,
    0 left unparsed. The staging snapshot carries no special-order traffic,
    so D16's parse holds with none left; the absence is recorded as what it
    is rather than counted as coverage of live special-order volume.

## HH-3.7 (tracking)

- **Closed in HH-3.7's first run: `erp-routes.txt` vendored from a `Demo`
  commit, not `master`.** D33 records that the commit from which
  `core/api/erp-routes.txt` was vendored under D22 is a `Demo` commit
  sixteen ahead of `master`, not `master`'s head (`6eaea3ec`), so the
  parity gate in `ci` had been proving the contract against a `Demo`
  snapshot. The lane's first run refreshed the file from `master` at
  05809a25 with `refresh-erp-routes.mjs`; the route difference the refresh
  reported is exactly one added route, `PUT
  /api/v1/purchasing/pos/{id}/delivery-method`, the measure of how far the
  snapshot was off. Closed by pull request #14
  (https://github.com/FutureShade-Cloud-Ecosystem/hh/pull/14).

## HH-3.3 (identity)

- **Open, routed to HH-3.7: ERP `master` advanced past the D33 pin and
  collides with the additive numbering.** While producing the D31 dump: the
  live staging database sits at ERP migration head 0219, and `origin/master`
  carries migrations 0213 through 0219 past the pinned baseline `6eaea3ec`
  (0001 through 0212). The ERP's own 0213 through 0215 are unrelated
  migrations holding the same numbers as this repository's frozen additive
  set (0213 org and tenant, 0214 entities and pricing, 0215 identity and
  audit), and the carried rule is that migrations are numbered, never edited
  after commit and never renumbered, so reconciling the collision is a port
  decision for HH-3.7 with Colton; D33 already notes the ERP delivery is in
  flight and lands migrations. The two D31 closures above are unaffected:
  they ran against the pin-era pre-migrate backup, the baseline the additive
  set was designed for. Nothing in HH-3.3 touches `core/db/migrations/`.

## HH-3.7 erp-routes refresh held for Phase C (released)

The first assigned port entry, refreshing core/api/erp-routes.txt from
master per the D33 finding, was HELD until Phase C integrated. Reason:
HH-3.4's parity test runs against the vendored list exactly as it stands
and must not re-vendor it; a refresh landing mid-lane moves the baseline
under that test, which either breaks it or passes it against a list
nobody approved (gate G2 of the Phase C dispatch ruling, verified
delivered artifact at context/phase-c-dispatch-ruling-r1.md in the
session workspace, sha256 5f88eb29015fa966077b4c0b3710a77a7d42a160aa13
cef96916b9db53c5dfdc). Release condition met: Phase C integrated and
merged, the eight decisions ruled, and the refresh ran as the lane's
first port entry in pull request #14
(https://github.com/FutureShade-Cloud-Ecosystem/hh/pull/14), which also
carries ERP migrations 0213 through 0220 through the same door per D39.
The tracking lane stays open for everything else.

## The migration door rule: ports versus greenfield schema (open)

HH-3.7's door (D39) is the PORT door: ERP migrations 0213 onward port
through it with a core/db/PORTED.md entry. It is not the door for new
core schema the ERP never had. Greenfield schema lands in HH-5, whose
job is migrations. Recorded so no lane re-routes a schema gap into the
port door: three Phase C findings (the tenant_brand table, the
customer_quote.share_token_hash index, the quote number column) were
initially routed to HH-3.7 and re-ruled to HH-5 by the Lane C checkpoint
ruling; they are ticketed in the Plane Records module as HH-5 items
(#157, #158, #159). D12 is not reopened. The gap-held annotations on the
tenant_brand contract operations stand until HH-5 lands the table.

## The public token elision table fails open for future routes (open)

The coarse audit middleware's token elision (the S-1 fix) is an
ALLOWLIST: publicTokenPrefixes names the three public token route
families that exist today (customer-quotes, the dealer quote pair, the
will-call pair), and a new public token route joins the table or its
credential persists in the coarse audit row. Whoever adds the fourth
public token route finds this table before Security does, or the route
ships with the leak the S-1 fix closed. The serve-shaped regression
covers today's routes only. A deny-by-default shape over /api/public/
with an explicit exempt list is the safe-side redesign, deliberately
not taken mid-Session (the integration review ruling r1, note N1).

## The bistrack module's push half and its rollback sentence are retired (open)

D43 retires the carried design that pushes to BisTrack during a shadow
phase, and the "rollback is free until November" sentence written into
core/internal/bistrack: nothing on the HH path writes to BisTrack at any
phase, and the parallel run is a nightly read-only reload of a recent
backup into a Dibbits staging org (HH-BISTRACK-CUTOVER-RULING-r1). The
module stays the fail-closed stub it is today; the FIRST lane that
touches it removes the push half and that sentence. Until then this
entry marks the obligation.

## L2 (till contract): the client generator needed the approver header in its allowlist (recorded, done in this lane)

The till's contract commit declares `X-Approver-Authorization` as a header
parameter on `POST /api/v1/pos/returns`, and `scripts/gen-client/gen-client.mjs`
refused every header parameter but the shared `X-Org`, so the regeneration the
contract change requires failed outright. The lane's declared Touches name the
contract sources and the generated outputs, not the generator, but the web
api-client's own idempotence gate runs this generator against the committed
tree, so without the change the committed gen tree could never be regenerated
again and the package's suite would stay red for every lane above this one in
the stack. The lane made the smallest change that unblocks the whole stack:
the allowlist gains the one named header (it rides the transport's caller-set
headers exactly like X-Org; the generated signature carries no credential, the
bearer never did either), and the generator emits a note on the one operation
that declares the header, so the regenerated client visibly carries it. The
lead may want this hunk re-homed when the pull request is read; the rest of
the commit is contract sources and generated outputs only.
## The Counter Till cycle, lane L1 (RC slice): statements the reconciled catalog lists contradict (open)

The RC slice brought the ten internal surfaces' role lists in
`docs/analysis/role-catalog.yaml` to the decided matrix (D48): each list
holds the thirteen staff roles exactly as the matrix admits them, and the
platform roles stand where the record names them, outside the matrix and
unjudged by it (`ROLE-ACCESS-r3`: the platform administrator, the dealer
administrator and the FutureBuild operator stay outside the matrix). Two
things are recorded here rather than edited, because the slice's Touches
stop at the lists:

- The role records' `micro_apps` lines no longer match the reconciled
  lists: `outside_sales`'s record names counter-till, which the list no
  longer holds, and eleven records omit read or work surfaces the matrix
  admits their role on (for example `general_manager`'s line names neither
  counter-till, quote-desk, dispatch-board nor buying-and-stock), so the
  role records and the surface lists no longer tell one story.
- The matrix judges the thirteen staff roles only: `super_admin` and
  `dealer_admin` keep their seats on manager-home, house-messages and
  dealer-admin as the record names them, at levels the matrix says nothing
  about, so any change to those seats is a ruling of its own and no part
  of this reconciliation.
## Counter Till cycle, lane L0 (W0): the `demoseed -plan` dated range does not describe the world it writes (open)

Item W0's exit test asserts that the demo world's first and last dates in the
database are those `demoseed -plan` reports for the run's clock. They are not,
in either direction, on a fresh database built by migrate plus `demoseed
-apply`:

- The world's oldest rows (the demo customers, products, stock moves and the
  pricing facts) carry a `created_at` older than the report's first figure.
  That figure is instead the oldest seeded invoice's issue date: `buildPlan`
  computes `FirstDate` from the same offset as the deepest invoice issue age
  (`demoseed_plan.go`, `demoseed_build.go`), while the catalogue, stock and
  pricing rows are stamped with a deeper `agoDays` figure.
- The world's newest written row is dated at the run's clock itself; no demo
  row is written later than the clock. The report's last figure is the
  furthest scheduled delivery (runs and stops), reached only by scheduled and
  expiry date columns, never by a written row's own stamp. The widest dated
  fact in the world overshoots it the other way: a seeded account counter
  sale's invoice due date lands further out still (the customer's terms added
  to a sale dated before the clock).

So the report line ("plan N rows across M tables, dated X .. Y") reads as a
history summary, oldest invoice to furthest scheduled delivery, rather than
the world's span, and it is not derivable from the rows it summarizes. W0
recorded this entry rather than narrowing its assertion to the two figures
that happen to match; the amendment at the end of this entry carries the
ruling that replaced the span equality with the world's own guarantees (the
world's earliest written row is recorded, and the newest written row over the
marker-stamped tables is asserted not after the run's clock). The world
itself applies clean and is dated to the run's clock: the newest seeded
counter sale falls on the run's day and every other W0 assertion passes. A
fix belongs to the seed or its report, never to the test: either derive
`FirstDate` and `LastDate` from the built rows, or word the report line as
what it is.

Recorded by lane L0, item W0 (`TestDemoWorld_CounterTill`,
`core/cmd/core/demoworld_countertill_test.go`). Per the plan's rule for a W0
gap, the lead closes it inside the goal or puts it to Colton.

- Ruled by the lead, lane L0 amended: the test asserts the world, not the
  report. The failing equality is replaced by the world's own guarantees
  (its written history ends at the run's clock, nothing written after it,
  and its newest business rows, the seeded counter sales, are the run's
  day), and dated facts that reach past the clock (a seeded account
  invoice's due date, the furthest scheduled delivery, the planned runs'
  and stops' own service-day row stamps) are recorded, not treated as the
  world's end. Both report figures moved to the recorded list, each named
  for what it actually is (the oldest seeded invoice issue date; the
  furthest scheduled delivery), so the goal report carries the finding
  exactly. Fixing the report's wording is outside this cycle's path and
  goes to the goal report with the recorded values.
## L5 (W6): the design system's gates demanded one edit inside web/packages (made, flagged)

The Counter Till app consumes `@hardscapehouse/design-system` (tokens, the
base element, the primitives), which makes it the first workspace package the
design system's gates treat as a dependent: those gates scan every workspace
package that depends on the design system, by manifest or by import, and one
of them is written to fail on a new dependent until its lock names the
package deliberately (`test/no-raw-markup.test.mjs`, the lock's own message:
"a package now depends on @hardscapehouse/design-system; its sources are
scanned below, and this lock must name it deliberately").

Naming it is a one-list edit inside `web/packages/design-system/test/`, which
is outside this lane's declared Touches (web/packages are off limits except
consuming them). Recorded and done rather than left red: the edit names the
app in the lock and nothing else, the gates' own audits over the app's
sources pass on the app's own merits (templates in allowed positions, no raw
markup), and the app's templates were rewritten to the kits' host delegation
rule so the positions gate accepts them without any gate change. If the lead
or a reviewer rules the lock edit out of bounds for this lane, it reverts as
one line and the decision it forces is whether the app may consume the design
system at all.
## L2 (till contract): the client generator needed the approver header in its allowlist (recorded, done in this lane)

The till's contract commit declares `X-Approver-Authorization` as a header
parameter on `POST /api/v1/pos/returns`, and `scripts/gen-client/gen-client.mjs`
refused every header parameter but the shared `X-Org`, so the regeneration the
contract change requires failed outright. The lane's declared Touches name the
contract sources and the generated outputs, not the generator, but the web
api-client's own idempotence gate runs this generator against the committed
tree, so without the change the committed gen tree could never be regenerated
again and the package's suite would stay red for every lane above this one in
the stack. The lane made the smallest change that unblocks the whole stack:
the allowlist gains the one named header (it rides the transport's caller-set
headers exactly like X-Org; the generated signature carries no credential, the
bearer never did either), and the generator emits a note on the one operation
that declares the header, so the regenerated client visibly carries it. The
lead may want this hunk re-homed when the pull request is read; the rest of
the commit is contract sources and generated outputs only.

### Review round (pull request 37): the accepted shape of the lock edit, and three findings recorded, not fixed

- **P3-1, the lock edit's shape.** The dependents lock edit carries one
  comment sentence beside the list entry ("The Counter Till app is the
  workspace's first dependent"); the functional change is the one list line,
  `[]` to `['@hardscapehouse/counter-till']`. The lead has accepted this
  shape; recorded so the acceptance and the diff match.
- **P3-4 (recorded, unreachable).** The entry page's Coming list is a fixed
  list beside the dynamic card list, with no reconciliation between them: a
  flows row whose label equals one of the eleven would show as a live card
  and as Coming at once. The demo's flow table is closed at two rows in this
  release, so no path reaches it.
- **P3-5 (recorded, unreachable).** The demo adapter keeps the mint in
  memory even when custody refuses the adoption, so the seam's getSession
  and createJWT would answer from a refused mint. Custody refuses only an
  unusable or expired mint, which the adapter's own validation screens
  first, and no path reaches those two operations before a successful
  adoption.
- **P3-7 (carried, not a defect of the app slice).** The exit test's own
  words run on the W5 image and against a default-build core, neither of
  which exists yet; the header provider sends X-Org on the two public demo
  calls as well as on staff calls, and whether the real routes and the
  phrase gate accept that waits for W2's mint; and the app holds a till past
  the token's expiry with nothing to re-check it against until W7's first
  core call. W5, W9, W2 and W7 hold the closures, per the plan.
## The Counter Till cycle, lane L3 (W1): the entrypoint seam sits in main.go (recorded, work continuing)

The lane's declared Touches name core/internal/demo/, demo_on.go,
demo_off.go and the NewStaticKeySource comment, but the item's guards
(G0's demo-check subcommand, G2's serve refusal, G3's check before the
listener, the demo graph's substituted verifier and membership source)
are unreachable from a build-tagged file alone: main() dispatches roles
through a switch with no seam, and registerRoutes constructs the platform
membership client unconditionally, so a demo box cannot boot the default
serve path at all. The lane therefore added the smallest seam main.go
admits: one guarded call to demoDispatch before the standard role switch,
with the no-op stub in demo_off.go. main.go is the only production file
outside the lane's Touches; beside it the lane added
core/cmd/core/demoenv_inventory.go (untagged, so its deny and allow lists
compile into the default binary as data and no /internal/demo symbol)
and three test files there (demo_build_guard_test.go,
demoenv_inventory_test.go, demo_on_test.go, the last carrying the demo
tag by the lead's ruling on review finding P3-1). The default build's behavior is
unchanged (the stub returns false before the switch), the G1 tests in
core/cmd/core hold the default dependency closure free of
core/internal/demo/, and the compiled-binary review command counts demo
symbols and expects none. The lead merges: any other lane that needs a
demo-only subcommand adds its case to demoDispatch in demo_on.go, not to
main.go.

## The Counter Till cycle, lane L3 (W1), review follow-ups: named gaps and their holders

- **No CI leg builds or tests the demo tag (review P2-4).** The ci job runs
  untagged only, so every demo-tagged guard and the compilation of
  demo_on.go sit outside the check, and another lane changing a wire
  function's signature would break the demo build with CI green. The lead
  assigns the leg to W5, whose plan row adds the CI job with a Postgres
  service; the interim holder is the in-team review, which runs both tag
  suites. Until W5 lands it, a merge that touches a wire function should
  be built with -tags demo by hand.
- **One demo-tagged test file sits outside the plan's two places (review
  P3-1).** core/cmd/core/demo_on_test.go carries //go:build demo because
  only a tagged test can call demoServe. The lead accepts it: test files
  enter no binary. The plan's sentence counts source files, and the
  amendment is the record's to make.
- **A boot dying between migrate and demo-populate cannot be retried
  (review P3-6).** demo-check refuses a migrated database that is not yet
  a demo world, which is G0 as the plan writes it, so an entrypoint that
  stops between the two steps leaves a box demo-check refuses from then
  on and the database has to be dropped. No change to G0; the W5
  entrypoint answers it (each step idempotent, the pair run to completion
  before serve).
## The Counter Till demo: one frozen channel name still carries a surname token (open)

W3's demo populate replaces every planted users row's display name on the
demo database (the two real-domain rows deactivated and renamed with it, the
placeholder names replaced with invented ones), which is the recorded scope.
One display surface the migrations plant is outside that scope and outside
the lane's Touches: migration 0060 names the DM channel it plants with a
text label that carries the same surname token the plan's data section
(PLAN-HH-ROLE-DEMO-r7, "Data") already raises for 0060's users row. The
schema is frozen, so no migration can fix it, and msg_channels is the
messaging module's table, so the populate writing it would be a cross-module
write nothing recorded asks for. A tester can meet the token at present only
through the messaging API (the Counter Till's screens never render channel
names). Decision wanted: either the demo populate takes on one guarded
rename of that channel's label on the demo database only, or the record
matter joins 0060's and 0200's under the same ruling. Recorded and
continued; nothing in W3's exit tests waits on it.

## The demo populate's review round: one fix outside the lane's Touches, and two assignments named (closed)

The independent review of W3 (pull request 38) found that a rerun of the
populate wrote one row it reported it had not: `identity.ProvisionOrgAnchor`'s
upsert (core/internal/identity/populate.go) rewrote the org anchor's name and
updated_at on every run. The fix is the review's own suggestion, minimal and
one place: the upsert's update leg is guarded on the value it would change
(`WHERE org.name IS DISTINCT FROM EXCLUDED.name`), so an unchanged anchor is
left untouched, row version and updated_at included, and a renamed one still
converges. Recorded here because the file is outside the lane's declared
Touches: this is the same exception class the identity population program
already carries (a one-shot provisioning tool that reads and writes across
module tables by design, the B3 review's recorded exception), the change is
guarded and tested at both levels (a unit leg beside the anchor's own tests
in the identity package, and the demo populate's rerun fingerprint over every
row it writes), and no caller's behaviour moves: inserted stays true only on
the insert path.

Two legs the review found unowned are assigned, not open. The `ci` check
compiles and runs no demo-tag job, so the demo build and its tests sit
outside the workflow's gate: the lead has assigned the demo CI job (with a
Postgres service) to W5's pull request, whose plan row adds a CI job, and the
in-team reviews cover the interim. And the serve leg of the payment
configuration check, `GET /api/v1/payments/config` behind the demo boot
guards, is covered by W5's image exit test, the lead having confirmed the
assignment; W3's exit test reads the payment store in process.

## W4 (quantity break, HH-184): the surcharge break vocabulary is inert configuration (open, for the dealers' record)

Under D1's replace rule a quantity break wins only when strictly cheaper, and
an uplift can never be, so the two surcharge value types (`SURCHARGE_PCT`,
`SURCHARGE_ABS`, the partial-skid tiers) no longer adjust any price anywhere;
ADR-0015 records that loss. The break editor still accepts surcharge tiers and
the first dealer's seed still plants one (the natural-stone partial-skid
schedule), and nothing on the editing surface tells the dealer who configures
one that the tier can never fire. The tier's own data, its validation and its
row survive untouched (the schema is frozen); what is owed is a dealer-facing
note on the editor, or a seed and editor cleanup once Colton rules which, so
nobody tunes a surcharge expecting the counter to charge it.
## The Counter Till cycle, lane L4 (PX): the exit test's CI leg stays with W5 (recorded)

The `ci` check runs the untagged suite with no gate variable and no
demo-tag step, so the card rail's wiring legs skip politely (they need
`HH_CORE_TEST_DATABASE_URL`) and the G7 legs, wiring level and serve level,
never run in CI. A green `ci` on the card rail's pull request therefore
says nothing about the exit test; the acceptance run is the gated local
run, executed by the lane and re-executed by the reviewer. W5 adds the CI
job that closes this gap; recorded here so the gap is visible until that
lands.

## L1 RG (role gate): the review's rulings and recorded gaps

- **The platform roles' treatment, by ruling.** The staff_role table
  grants three tier 1 roles the matrix holds no cells for (super_admin,
  dealer_admin, fb_operator, 0215). ROLE-ACCESS-r3 leaves them outside the
  matrix and the lead's catalog ruling 10 keeps their seats. Ruled in the
  PR 44 review (P1-2): a caller whose resolved roles include one of the
  three passes the role gate's level check on every staff route, the tier
  guard still applies its own x-role checks to them, and a route that
  names a configuration list is not covered by the pass. Implemented as
  the named list in core/internal/platform/authz/rolegate.go; kept on the
  record so the ruling travels with the code.
- **Three writes annotated at read by convention, named for the record**
  (the PR 44 review's P2-4): PUT /api/v1/purchasing/policy, PUT
  /api/v1/inventory/transit-days, PATCH /api/v1/quote-assist/scope-rules/
  {code}. On these surfaces no leadership role the requireAdmin tier
  admits holds a cell above read, so a higher level would leave the route
  to nobody; the tier gate alone holds each wall. Each block now carries
  its reason in place. A dealer re-shaping the matrix moves these levels
  with it.
- **The registered half of the completeness walk has no CI coverage**
  (the PR 44 review's P2-5). The plain census test
  (core/cmd/core/census_test.go) runs without the database and catches a
  document or census annotation fault; a route registered in Go and
  annotated nowhere is caught only by the registrar's boot panic and by
  the database-gated composition walk, neither of which the repository's
  CI executes. W5's acceptance run sets the gate variables and is the
  natural home; until then the attach-time panic is the only stop.
- **The whole-row walk composes across grants; the tier guard reads the
  winner** (the re-read review's note b, for the comprehensive review to
  judge on purpose). A person holding hr (tier 1, dealer-admin admin) and
  sales_manager (tier 2, manager-home work) passes a requireAdmin route
  annotated manager-home at read, which neither role passes alone: hr
  fails the tier gate, the sales manager fails the requireAdmin tier.
  That is one reading of D48's "a person holding two roles gets both",
  implemented in rolegate.go's whole-row walk over ResolveRoles beside
  the winner-only ResolveStaff the tier guard reads; the two gates
  deliberately read different facts about the same grants. ResolveRoles'
  own suite sits in internal/identity (authority_roles_test.go).
- **Recorded, not changed** (the re-read review's notes c and d):
  hr still reads the pricing surface's reads (rules, price files, the
  rebate ledger, dealer-admin at admin); the PR 44 ruling covered the
  writes, and cost and margin on the fields are the cost_visibility
  list's job, so these reads belong to the item that reads that list;
  the refusal wording in rolegate.go and merge.mjs says level none
  "refuses every role" where the comment beside it correctly says it
  would admit every role (a wording slip, three places); and apiV1Path
  recognises a registration only behind one of five method prefixes, so
  a methodless, HEAD or OPTIONS registration under /api/v1/ would pass
  the registrar ungated (none exists today).
- **The all-ten convention, for the comprehensive review's eye** (the PR
  44 review's P3-5): nineteen routes name every surface, so every role
  whose house-messages cell is at least read passes them, and every
  configuration that keeps house-messages at work (the default does, for
  all thirteen roles) leaves them open to all staff. Sound for a caller's
  own things; POST /api/v1/attachments and POST
  /api/v1/attachments/{id}/links are writes against any owner and deserve
  a look at the comprehensive review.
## The Counter Till cycle, lane L3 (W2): a refused commit message amend (recorded, work continuing)

W2's third commit's subject carried the words "half a minute" where the
house rules want no duration in committed prose. The amend to reword it
was refused by the repository hook (history rewrite refused in full, per
the lane's rules), and a refusal is final. The commit's content is
correct and the wording lives only in that one local commit message; the
lead may reword it when landing the pull request, or let it stand. Nothing
in the code, tests or this file carries the wording.

W2's third commit's subject carried the words "half a minute" where the
house rules want no duration in committed prose. The amend to reword it
was refused by the repository hook (history rewrite refused in full, per
the lane's rules), and a refusal is final. The commit's content is
correct and the wording lives only in that one local commit message; the
lead may reword it when landing the pull request, or let it stand. Nothing
in the code, tests or this file carries the wording.

## The Counter Till cycle, lane L4 (PX): the exit test's CI leg stays with W5 (recorded)

The `ci` check runs the untagged suite with no gate variable and no
demo-tag step, so the card rail's wiring legs skip politely (they need
`HH_CORE_TEST_DATABASE_URL`) and the G7 legs, wiring level and serve level,
never run in CI. A green `ci` on the card rail's pull request therefore
says nothing about the exit test; the acceptance run is the gated local
run, executed by the lane and re-executed by the reviewer. W5 adds the CI
job that closes this gap; recorded here so the gap is visible until that
lands.
## L5 (W6): the design system's gates demanded one edit inside web/packages (made, flagged)

The Counter Till app consumes `@hardscapehouse/design-system` (tokens, the
base element, the primitives), which makes it the first workspace package the
design system's gates treat as a dependent: those gates scan every workspace
package that depends on the design system, by manifest or by import, and one
of them is written to fail on a new dependent until its lock names the
package deliberately (`test/no-raw-markup.test.mjs`, the lock's own message:
"a package now depends on @hardscapehouse/design-system; its sources are
scanned below, and this lock must name it deliberately").

Naming it is a one-list edit inside `web/packages/design-system/test/`, which
is outside this lane's declared Touches (web/packages are off limits except
consuming them). Recorded and done rather than left red: the edit names the
app in the lock and nothing else, the gates' own audits over the app's
sources pass on the app's own merits (templates in allowed positions, no raw
markup), and the app's templates were rewritten to the kits' host delegation
rule so the positions gate accepts them without any gate change. If the lead
or a reviewer rules the lock edit out of bounds for this lane, it reverts as
one line and the decision it forces is whether the app may consume the design
system at all.
## L2 (till contract): the client generator needed the approver header in its allowlist (recorded, done in this lane)

The till's contract commit declares `X-Approver-Authorization` as a header
parameter on `POST /api/v1/pos/returns`, and `scripts/gen-client/gen-client.mjs`
refused every header parameter but the shared `X-Org`, so the regeneration the
contract change requires failed outright. The lane's declared Touches name the
contract sources and the generated outputs, not the generator, but the web
api-client's own idempotence gate runs this generator against the committed
tree, so without the change the committed gen tree could never be regenerated
again and the package's suite would stay red for every lane above this one in
the stack. The lane made the smallest change that unblocks the whole stack:
the allowlist gains the one named header (it rides the transport's caller-set
headers exactly like X-Org; the generated signature carries no credential, the
bearer never did either), and the generator emits a note on the one operation
that declares the header, so the regenerated client visibly carries it. The
lead may want this hunk re-homed when the pull request is read; the rest of
the commit is contract sources and generated outputs only.

### Review round (pull request 37): the accepted shape of the lock edit, and three findings recorded, not fixed

- **P3-1, the lock edit's shape.** The dependents lock edit carries one
  comment sentence beside the list entry ("The Counter Till app is the
  workspace's first dependent"); the functional change is the one list line,
  `[]` to `['@hardscapehouse/counter-till']`. The lead has accepted this
  shape; recorded so the acceptance and the diff match.
- **P3-4 (recorded, unreachable).** The entry page's Coming list is a fixed
  list beside the dynamic card list, with no reconciliation between them: a
  flows row whose label equals one of the eleven would show as a live card
  and as Coming at once. The demo's flow table is closed at two rows in this
  release, so no path reaches it.
- **P3-5 (recorded, unreachable).** The demo adapter keeps the mint in
  memory even when custody refuses the adoption, so the seam's getSession
  and createJWT would answer from a refused mint. Custody refuses only an
  unusable or expired mint, which the adapter's own validation screens
  first, and no path reaches those two operations before a successful
  adoption.
- **P3-7 (carried, not a defect of the app slice).** The exit test's own
  words run on the W5 image and against a default-build core, neither of
  which exists yet; the header provider sends X-Org on the two public demo
  calls as well as on staff calls, and whether the real routes and the
  phrase gate accept that waits for W2's mint; and the app holds a till past
  the token's expiry with nothing to re-check it against until W7's first
  core call. W5, W9, W2 and W7 hold the closures, per the plan.

## The Counter Till cycle, lane L3 (W5): the mint's limit still keys on the connection's peer alone, so the reviewer's two-bucket clause waits for W2's keying (recorded, work continuing)

W2's review, routed to W5: the mint's per-address limit reads the
forwarded chain only when the immediate peer is loopback, and trusts the
leftmost element, so the image's Caddy must overwrite `X-Forwarded-For`
with the address it saw itself, never append from the client. The image's
half is done and proven: every proxy leg in
`deploy/demo/Caddyfile.template` replaces the header with Caddy's own
remote host, and the exit test proves at the traffic level (a capture
behind the image's Caddy) that a client-written value never survives the
proxy, so it can buy no bucket under either keying.

The clause that cannot hold yet: two distinct client addresses behind the
image's own Caddy still share one limit bucket, because the core on this
branch never reads the forwarded chain at all. The limit's address is
`clientAddress` in `core/internal/demo/session.go`, the connection's host
only (the W2 commit message says the same: keyed by the connection's
host), so behind any proxy, the image's own Caddy included, every visitor
is the proxy's loopback address and the allowance is one shared bucket.
That is the conservative failure (a burst is still bounded), and the
reviewer's described keying (read the chain when the immediate peer is
loopback, trust the proxy's single element) is W2's surface, not the
image's: this lane touches no core Go. Until W2's keying lands, the
phrase gate and the boot guards carry the box, and the exit test asserts
the two clauses that hold under both keyings: the allowance is bounded,
and rotating forged `X-Forwarded-For` values buy no bucket. When the core
reads the chain, that same leg proves the two-bucket behavior the
reviewer asked for, with no script change.

## L2 (till contract): the client generator needed the approver header in its allowlist (recorded, done in this lane)

The till's contract commit declares `X-Approver-Authorization` as a header
parameter on `POST /api/v1/pos/returns`, and `scripts/gen-client/gen-client.mjs`
refused every header parameter but the shared `X-Org`, so the regeneration the
contract change requires failed outright. The lane's declared Touches name the
contract sources and the generated outputs, not the generator, but the web
api-client's own idempotence gate runs this generator against the committed
tree, so without the change the committed gen tree could never be regenerated
again and the package's suite would stay red for every lane above this one in
the stack. The lane made the smallest change that unblocks the whole stack:
the allowlist gains the one named header (it rides the transport's caller-set
headers exactly like X-Org; the generated signature carries no credential, the
bearer never did either), and the generator emits a note on the one operation
that declares the header, so the regenerated client visibly carries it. The
lead may want this hunk re-homed when the pull request is read; the rest of
the commit is contract sources and generated outputs only.
## L5 (the sale at the counter): two findings recorded, both handled in this lane

- **The two harnesses of the two app lanes shared one port pair.** W6's
  Playwright and Vite defaults (4820 and 4821) are inherited by every lane
  that copies them, and Playwright's `reuseExistingServer` then silently
  drives whichever app is already up: this lane's first exit-test run drove
  the other app lane's dev server and failed against its screens. This lane
  moved its own defaults to 4826 and 4827 (both files, and the app README),
  which leaves the old pair free; any later app lane should pick its own pair
  the same way, or the two suites will interleave again.
- **A form's submit event never crosses a shadow root.** The searches were
  first written as forms with the listener on the host, the same delegation
  the design system's README prescribes for click; the form submitted natively
  and the app signed itself out, because submit is not composed: the event
  stops at the shadow root and a host listener never fires, where click does.
  Measured in Chrome, then fixed by making every search a plain button with
  `data-action`. The design system's README's delegation section may want a
  sentence on this for the next dependent; that file is outside this lane's
  Touches, so it is recorded here instead.
## L5 (the review round): four findings recorded rather than closed, with their rulings

- **The ticket shows no total before the ring (review P2-7).** The counter
  price picture answers unit quotes alone, and the app rightly computes
  nothing, so the subtotal, tax and total first appear on the receipt. The
  lead's ruling: either a contract read (a ticket-total read off the same
  picture) or the testers' page says it. The contract read is outside this
  lane's Touches, so it is recorded for whoever holds the contract next; W9's
  testers' page should carry the sentence either way.
- **The screen's margin figure vs the plan's wording (review P3-6, second
  half).** C01's Holland pavers carry a margin of 1585 basis points, which is
  15.85 percent exactly; the column renders one decimal, half away from zero:
  "15.9%". The plan's screen 4 says "about 15.8". W9's testers' page should
  quote the screen's figure, or the core's real answer should be checked when
  the demo world is populated for real.
- **The counter's state machine lives in the Lit element (review P3-9).** The
  money helper and the boundary conversions are plain modules held by unit
  tests; the ticket's lines, quantities and pricing sequence are still fields
  on the app root. A later item should extract that state machine into a
  plain module with the same test, before a second screen shares it.
- **Nothing serves the organization's zone yet (review P2-3).** The ring's
  requested date is contract-required and no org or tenant read carries a
  time zone, so the app computes the day in the house zone constant the core
  itself compiles (America/Toronto), through Intl, and the stub refuses a
  ring whose date is not the house zone's day. When the core serves a
  configured zone, the constant should be replaced by the served value.
## L5 W8 (the returns desk's review round): the P3s not closed in code

- **P3-1 (residue: the dialog is not a modal surface).** The Manager approval
  dialog is a styled section carrying the dialog role; while it is open the
  desk's fields and the find row are disabled (this round), so nothing drafts
  or refinds under it and the dead-cards state the review reproduced is gone.
  What remains is focus and reading order: nothing behind it is inert, and a
  keyboard user can still reach the chrome. A truly modal surface (the inert
  attribute, or a native dialog element) is a pattern decision that belongs
  to the design system's kits, which this lane consumes read only.
- **P3-8 (carried, unchanged by this round).** The Z report assertion proves
  the app sends a cash return naming the open drawer and that the stub's
  counter moves; the real drawer's expected cash is W9's run on the image.
  Screen 10 (a Kingston sale refused before any money moves, by every refund
  method) has no test here beyond the unknown number's refusal in words.
- **The receipt's approver line is display wording, per the ruling on P2-8.**
  On the approver's own path the receipt names the caller from the session
  (display name and role) and the dialog path names the approver the dialog
  minted; the contract's PosReturn carries no approver, so neither line is a
  server-stated fact. The server's audit row holds the record. If a later
  ruling wants the receipt to quote the core, the field is the contract's to
  add, not this app's to invent.

## L5 W9 (the story end to end): what the run found on the image, recorded and worked around

The acceptance run drove the eleven screens as each role against the local
image built from the goal's integration branch head (till/goal d028c4d) with
this lane's app work. Four findings belong to other lanes' files; each one is
worked around inside this lane's Touches so the run proves every screen's
substance, and each needs its owner's close. A fifth entry records the one
edit outside this lane's Touches the lead granted.

- **The demo world homes C01 and C09 to Kingston, while the demo logins reach
  Trenton alone.** The seed plan places C01, C04, C06, C08 and C09 on branch
  index 0 by design, and on the migrated database the seed's branch resolution
  puts index 0 at Kingston (the plan's rows against the seed's branch
  resolution, not an ordering accident): C01 (the contractor whose 18 percent
  paver price the plan's screen 4 names) and C09 (the account on hold the plan's
  screen 5 names) come out homed to Kingston, and the customer picker is fenced
  to the caller's own branch server-side, so the Trenton counter cannot reach
  either row. The run proves screen 4 with C02 (a Trenton contractor, 12
  percent off pavers) and C03 (Trenton, 10 percent off bulk, whose Granular A
  sits below the 20 percent bulk floor and rings anyway), and screen 5 with
  C07 (a Trenton retail account whose zero limit the credit gate refuses in
  words, the same refusal the hold would show); the testers' page words the
  hold account's absence accordingly. Owner: W3's lane (the demo seed and
  populate), the lead to rule the rows or the grants.
- **No seeded sale at Trenton crosses the refund approval amount as a return.**
  DEMO-TRT-S09 is the seeded sale above 250.00 (251.71 with the seed's tax),
  but the return rail recomputes a return's amounts from the line, and the
  seeded line's own tax answer is zero: the whole line refunds at 222.75, under
  the threshold. The run crosses the amount with a sale it rings itself (sixty
  wall blocks, 847.50). The seed's tax on its counter sales and the return
  rail's tax answer disagree; owner: W3's lane for the seed's tax, with RT-B's
  lane the other side of the disagreement, so neither reads the record as its
  own defect alone.
- **The demo build's serve wiring did not attach the role gate: found, routed,
  and fixed while this round ran.** Confirmed at source on till/goal d028c4d:
  `authz.NewRoleGateRegistrar` wraps the registrar in main.go's
  `registerRoutes` (core/cmd/core/main.go:292) and appeared nowhere in
  demo_on.go, whose `demoRegisterRoutes` built the demo box's graph on the raw
  registrar, so on the demo box the role gate never ran and the tier gate
  stood alone. Probed on the image (the review round's probes, reproduced):
  the trucks, drivers, board, runs and loadboard reads (three routes) and the
  two policy reads answered 403 to BOTH logins, and the will-call and
  scan-trail reads passed the gate for both (their own validation answering
  next), so the plan's screen 11 manager cell failed on both halves: the clerk
  was admitted to some dispatch reads, and the manager was refused most. The
  lead routed the fix to RG's lane, which landed it on till/goal (merge
  a884020: the demo build loads the decided default and wraps every demo
  registration through the same registrar, with its own probe and bite). The
  exit run now builds from the head that carries the attach, the run asserts
  the matrix holding (a dispatch read refused to the clerk, admitted to the
  manager; the price rule write beside it), and the testers' page states the
  matrix as holding. The asymmetric beyond-the-till reads the review probed
  (will-call and the scan trail admitted to the manager only once the gate
  stands, the transaction trail admitted to either, none fenced to a branch
  yet) are the testers' page's own known-issues entry, asserted by the run.
- **The parked list serves a bare array against the contract's page shape.**
  `GET /api/v1/pos/parked` answers `PosParkedTicketPage` in the contract (an
  object with `items`), and the served core answers the bare array instead, so
  the app's field-by-field read refused the whole list. This lane's app now
  reads both shapes and refuses anything else; the same pass may want the
  counter search's optional `sold_by` looked at, which the served core leaves
  out of every hit (the app falls back to the required `base_uom`). Owner:
  RT-B's lane for the served route's shape, with W7's lane as the contract's
  owner for whichever side moves.
- **The demo database's dealer display words, replaced by the populate
  (the lead's granted exception to this lane's Touches, the review round's
  P2-1).** Migrations 0003 and 0014 plant the two demo branches' names with
  the first dealer's real trading name joined to the city by an em dash, and
  the till's drawer strip and parked list header render the branch name
  verbatim, so every till screen showed the real trading name while the
  testers' page said the box carries one invented dealer. The lead granted
  one edit in W3's file, core/internal/demo/populate.go: the populate's own
  rename discipline now converges the two demo branches' display names onto
  the invented trading name (Hardscape House (demo), Trenton and Hardscape
  House (demo), Kingston), each update guarded on the value it would write so
  a re-run writes nothing, counted in the run report; the org anchor's name
  was already the identity program's to converge. The acceptance run asserts
  the invented name on the drawer strip and the parked list header, and the
  screenshots show it.

## The Counter Till cycle, lane L3 (W5): the demoseed integration suite's migration glob resolves nowhere since the fold (recorded, not this lane's file)

The composition suite at `core/cmd/core/demoseed_integration_test.go`
globs its migrations at `filepath.Join("..", "..", "migrations", "*.sql")`
relative to the package directory, which resolves to `core/migrations`
and finds nothing: the migration set lives at `core/db/migrations`, and
the suite's own header still names the ERP's `./cmd/demoseed/...` layout
the fold replaced. Every test in the file fails at "no migrations found"
wherever it runs, so the suite's database legs cannot be gated into CI
(the demo job leaves the step out and says why, in the workflow). The fix
is one glob argument in a file outside this lane's declared Touches;
recorded so the owning lane lands it and the composition legs return to
the demo job's coverage.
W2's third commit's subject spells a window length out in words where
the house rules want no duration in committed prose. The amend to reword
it was refused by the repository hook (history rewrite refused in full,
per the lane's rules), and a refusal is final, so the subject stands as
history. The commit's content is correct; this entry deliberately quotes
no passage of the message, and the lead may reword the message when
landing the pull request, or let it stand.

## The Counter Till cycle, lane L3 (W2), the review round: two fixes placed outside the lane's Touches, by the lead's rulings

The independent review of W2 (pull request 49) returned no P1, three P2
and seven P3 findings, and the lead's rulings placed every fix on the
lane's branch. Two of the fixes edit files the lane's Touches never
named, each because the ruling says so and each recorded here in the W3
precedent's spirit: the audit middleware's carve-out table
(selfRecordedMutations in core/internal/audit/service.go, so the demo
session route takes no coarse HTTP row and a refused mint writes nothing
at all, the review's P2-1), and the contract's declaration of the
route's refusals (the demo session operation in core/api/skeleton.yaml
gains its 403, 429 and 500, the TooManyRequests and InternalServerError
response components join the house's table, and the stale description
now names the demo build, the four constructor collaborators, the limit
and the row order; the review's P2-3). Everything else sits inside the
Touches: the limit now keys on the forwarded chain when the peer is the
box's own loopback proxy and ignores the header from any other peer
(P2-2), the request decoder refuses unknown fields and trailing data
(P3-2), and the small findings (the blockers reword, the mint failure
after the trail row, the spent allowance stated, the batched eviction,
the exit test's rename and generic run note, the clock literals) are
fixed in place with their tests. All seven P3s were cheap and none is
left open.

## L5 W8 (the returns desk's review round): the P3s not closed in code

- **P3-1 (residue: the dialog is not a modal surface).** The Manager approval
  dialog is a styled section carrying the dialog role; while it is open the
  desk's fields and the find row are disabled (this round), so nothing drafts
  or refinds under it and the dead-cards state the review reproduced is gone.
  What remains is focus and reading order: nothing behind it is inert, and a
  keyboard user can still reach the chrome. A truly modal surface (the inert
  attribute, or a native dialog element) is a pattern decision that belongs
  to the design system's kits, which this lane consumes read only.
- **P3-8 (carried, unchanged by this round).** The Z report assertion proves
  the app sends a cash return naming the open drawer and that the stub's
  counter moves; the real drawer's expected cash is W9's run on the image.
  Screen 10 (a Kingston sale refused before any money moves, by every refund
  method) has no test here beyond the unknown number's refusal in words.
- **The receipt's approver line is display wording, per the ruling on P2-8.**
  On the approver's own path the receipt names the caller from the session
  (display name and role) and the dialog path names the approver the dialog
  minted; the contract's PosReturn carries no approver, so neither line is a
  server-stated fact. The server's audit row holds the record. If a later
  ruling wants the receipt to quote the core, the field is the contract's to
  add, not this app's to invent.
## L5 (the sale at the counter): two findings recorded, both handled in this lane

- **The two harnesses of the two app lanes shared one port pair.** W6's
  Playwright and Vite defaults (4820 and 4821) are inherited by every lane
  that copies them, and Playwright's `reuseExistingServer` then silently
  drives whichever app is already up: this lane's first exit-test run drove
  the other app lane's dev server and failed against its screens. This lane
  moved its own defaults to 4826 and 4827 (both files, and the app README),
  which leaves the old pair free; any later app lane should pick its own pair
  the same way, or the two suites will interleave again.
- **A form's submit event never crosses a shadow root.** The searches were
  first written as forms with the listener on the host, the same delegation
  the design system's README prescribes for click; the form submitted natively
  and the app signed itself out, because submit is not composed: the event
  stops at the shadow root and a host listener never fires, where click does.
  Measured in Chrome, then fixed by making every search a plain button with
  `data-action`. The design system's README's delegation section may want a
  sentence on this for the next dependent; that file is outside this lane's
  Touches, so it is recorded here instead.
## L5 (the review round): four findings recorded rather than closed, with their rulings

- **The ticket shows no total before the ring (review P2-7).** The counter
  price picture answers unit quotes alone, and the app rightly computes
  nothing, so the subtotal, tax and total first appear on the receipt. The
  lead's ruling: either a contract read (a ticket-total read off the same
  picture) or the testers' page says it. The contract read is outside this
  lane's Touches, so it is recorded for whoever holds the contract next; W9's
  testers' page should carry the sentence either way.
- **The screen's margin figure vs the plan's wording (review P3-6, second
  half).** C01's Holland pavers carry a margin of 1585 basis points, which is
  15.85 percent exactly; the column renders one decimal, half away from zero:
  "15.9%". The plan's screen 4 says "about 15.8". W9's testers' page should
  quote the screen's figure, or the core's real answer should be checked when
  the demo world is populated for real.
- **The counter's state machine lives in the Lit element (review P3-9).** The
  money helper and the boundary conversions are plain modules held by unit
  tests; the ticket's lines, quantities and pricing sequence are still fields
  on the app root. A later item should extract that state machine into a
  plain module with the same test, before a second screen shares it.
- **Nothing serves the organization's zone yet (review P2-3).** The ring's
  requested date is contract-required and no org or tenant read carries a
  time zone, so the app computes the day in the house zone constant the core
  itself compiles (America/Toronto), through Intl, and the stub refuses a
  ring whose date is not the house zone's day. When the core serves a
  configured zone, the constant should be replaced by the served value.

## L2 RT-B: the review fixes and the three facts the next reader needs (recorded, done in this lane)

- **The till's rules are wired in the composition root on this branch (the
  lead's accepted deviation into cmd/core, P1-1).** `core/cmd/core/wire_roles.go`
  loads the dealer's role configuration at boot (the load whose refusal stops
  the core) and `wireTillRules` wires the configuration, the approver
  credential source and the users id linker into the pos service at the serve
  composition and (the configuration alone; the compose builds no serving
  surface) the demo compose. A source-pinning test
  (`TestTillRules_WiringIsPinned`) refuses to let either site drift, and a
  composition-level test over the fixture database
  (`TestTillRules_WiredCompositionRefusesAndShows`) proves the built service
  refuses the 300.00 return and shows the clerk the glance. RG's fuller
  wiring of the same configuration into the gate supersedes the load here;
  the two must share one load then.
- **A 401 on `POST /api/v1/pos/returns` during an approval flow means the
  APPROVER credential failed, not the clerk's session (what W8's dialog must
  know, P3-5).** The clerk's bearer stays valid; the dialog should re-ask for
  the manager's credential, not re-login. A 409 with
  `error: refund_approval_required` after a credential was attached means the
  credential did not qualify (same person, off-list role, grant short of the
  sale's branch, issued outside the configured window), and the dialog stays
  up. A 503 means the machinery could not run; the route now declares it.
- **"The Branch Manager of the sale's branch" is enforced as "holds a listed
  role anywhere in the org and a branch grant for the sale's branch"**
  (the review's noted item, for the lead to rule on). The staff resolver
  returns the highest grant across the org and reads branches from
  `user_branch_grants`, so a Sales manager of Kingston holding a Trenton
  grant for another reason approves a Trenton refund. This is the resolver's
  standing rule for every caller; tightening it for refunds alone would be a
  new rule and stops at the lead.
## The Counter Till cycle, lane L3 (W1): the entrypoint seam sits in main.go (recorded, work continuing)

The lane's declared Touches name core/internal/demo/, demo_on.go,
demo_off.go and the NewStaticKeySource comment, but the item's guards
(G0's demo-check subcommand, G2's serve refusal, G3's check before the
listener, the demo graph's substituted verifier and membership source)
are unreachable from a build-tagged file alone: main() dispatches roles
through a switch with no seam, and registerRoutes constructs the platform
membership client unconditionally, so a demo box cannot boot the default
serve path at all. The lane therefore added the smallest seam main.go
admits: one guarded call to demoDispatch before the standard role switch,
with the no-op stub in demo_off.go. main.go is the only production file
outside the lane's Touches; beside it the lane added
core/cmd/core/demoenv_inventory.go (untagged, so its deny and allow lists
compile into the default binary as data and no /internal/demo symbol)
and three test files there (demo_build_guard_test.go,
demoenv_inventory_test.go, demo_on_test.go, the last carrying the demo
tag by the lead's ruling on review finding P3-1). The default build's behavior is
unchanged (the stub returns false before the switch), the G1 tests in
core/cmd/core hold the default dependency closure free of
core/internal/demo/, and the compiled-binary review command counts demo
symbols and expects none. The lead merges: any other lane that needs a
demo-only subcommand adds its case to demoDispatch in demo_on.go, not to
main.go.

## The Counter Till cycle, lane L3 (W1), review follow-ups: named gaps and their holders

- **No CI leg builds or tests the demo tag (review P2-4).** The ci job runs
  untagged only, so every demo-tagged guard and the compilation of
  demo_on.go sit outside the check, and another lane changing a wire
  function's signature would break the demo build with CI green. The lead
  assigns the leg to W5, whose plan row adds the CI job with a Postgres
  service; the interim holder is the in-team review, which runs both tag
  suites. Until W5 lands it, a merge that touches a wire function should
  be built with -tags demo by hand.
- **One demo-tagged test file sits outside the plan's two places (review
  P3-1).** core/cmd/core/demo_on_test.go carries //go:build demo because
  only a tagged test can call demoServe. The lead accepts it: test files
  enter no binary. The plan's sentence counts source files, and the
  amendment is the record's to make.
- **A boot dying between migrate and demo-populate cannot be retried
  (review P3-6).** demo-check refuses a migrated database that is not yet
  a demo world, which is G0 as the plan writes it, so an entrypoint that
  stops between the two steps leaves a box demo-check refuses from then
  on and the database has to be dropped. No change to G0; the W5
  entrypoint answers it (each step idempotent, the pair run to completion
  before serve).

## L1 RG (role gate), post-review fix routed from W9: the demo build's registration exception

W9's acceptance run found the demo build serving its staff routes by tier
alone: demo_on.go's demoRegisterRoutes mirrored the default build's
registerRoutes up to the role gate, so a demo box never held a demo
bearer to the dealer's configuration. The fix attaches the same wrapping
registrar and the same roleconfig.Default load in demoRegisterRoutes, in
the same place main.go's registerRoutes puts them (the mirror's own rule:
when the default wiring gains a step, the mirror gains the same step in
the same place). Two recorded notes. First, the Touches exception: W1's
branch owns demo_on.go, and this lane edited it for this fix by the
lead's granted exception, recorded here as asked. The fix requires W1's
demo build on this branch, so the lane merged till/l3-w1 into till/l1-rg
before the fix (the merge is the lead's own stitch shape; goal carried
the same union at b67c4f8 and f10f672, and the only conflict was this
file, resolved keeping both lanes' entries). Second, the test seam: the
graph carries demoMint (nil in the default build, set only by the demo
wiring) so the demo-tagged probe can hold a real session token; the
probe itself (rolegate_demo_test.go) hand-plants the populate's world in
miniature on a case database of its own, including the two flow logins'
users rows and grants the populate plants.

### Review round (pull request 37): the accepted shape of the lock edit, and three findings recorded, not fixed

- **P3-1, the lock edit's shape.** The dependents lock edit carries one
  comment sentence beside the list entry ("The Counter Till app is the
  workspace's first dependent"); the functional change is the one list line,
  `[]` to `['@hardscapehouse/counter-till']`. The lead has accepted this
  shape; recorded so the acceptance and the diff match.
- **P3-4 (recorded, unreachable).** The entry page's Coming list is a fixed
  list beside the dynamic card list, with no reconciliation between them: a
  flows row whose label equals one of the eleven would show as a live card
  and as Coming at once. The demo's flow table is closed at two rows in this
  release, so no path reaches it.
- **P3-5 (recorded, unreachable).** The demo adapter keeps the mint in
  memory even when custody refuses the adoption, so the seam's getSession
  and createJWT would answer from a refused mint. Custody refuses only an
  unusable or expired mint, which the adapter's own validation screens
  first, and no path reaches those two operations before a successful
  adoption.
- **P3-7 (carried, not a defect of the app slice).** The exit test's own
  words run on the W5 image and against a default-build core, neither of
  which exists yet; the header provider sends X-Org on the two public demo
  calls as well as on staff calls, and whether the real routes and the
  phrase gate accept that waits for W2's mint; and the app holds a till past
  the token's expiry with nothing to re-check it against until W7's first
  core call. W5, W9, W2 and W7 hold the closures, per the plan.

## HH hardening, defect HH-179: two reporting integration tests fail on any freshly migrated database (recorded, not this lane's defect)

The full reporting integration suite cannot go green on a database built
from this repository's migrations, on main and on the HH-179 branch
alike. Two tests assert the human title a template is seeded with:
TestIntegration_Print_FreezesArtefactAndEnqueuesREPORTJob wants the
printed artefact to contain "Sales by Day", and
TestIntegration_Export_CSVCarriesTheSameNumbersAsTheRun wants the CSV
title row to read "Sales by Day". The seeding migration for the
reporting templates inserts the scrubbed name for sales_by_day, so the
database matches its migrations and the assertions cannot hold on any
fresh database. The failure predates HH-179 (it reproduces on main
against the same local database) and CI does not see it because the
integration tag suite runs only where DATABASE_URL is set. The fix
belongs to whoever owns the template seed or those assertions: either
the assertions stop pinning the human title, or the seed carries it.
Recorded so the HH-179 checks can be read honestly: every other test in
the reporting integration suite passes on the HH-179 branch.

## HH hardening, defect HH-191: the integration tag suite's recorded-red packages, what ci cannot run yet (each with its verified failure against a freshly built database)

HH-191 puts the integration tag suite into ci. Four of the suite's
breaks are fixed on the HH-191 branch (the demoseed composition
fixture's migration glob, the printing receipt legs' dropped LineInput
field, the reporting surface legs' seeded-title pins, and the warranty
seed-coherence legs' migration path, the same fold break as the
composition fixture's). Running the rest of the suite against the
databases this repository's own commands build (cmd/core migrate, then
cmd/core seed for the module gate; migrate alone for the composition
gate) leaves the eight packages below red on main and on the HH-191
branch alike: none of it is HH-191's change, all of it predates the
suite entering ci, which is the defect's own point. The new integration
job runs the suite over everything else and leaves exactly these
packages out, each named in the job's test step. A fix owner takes the
package, lands the fix, and drops the package's line from the job.

- internal/billing, TestIntegration_Billing_ListInvoices_TenantFenceFilterAndPaging
  (repository_integration_test.go:211). The invoice list's cursor
  predicate is the row comparison (issue_date, invoice_no) <
  (cursor_date, cursor_no), whose tiebreak direction contradicts the
  ordering the same query documents (issue_date DESC, invoice_no ASC):
  on the cursor's own date the comparison keeps only invoice_no values
  BELOW the cursor's, so the walk silently drops every later invoice_no
  on that date and hands back the older date's row (the failing page
  returns F2 where F1 is wanted). Proven with two rows in psql: the
  shipped predicate returns the older row alone; the direction-correct
  keyset (issue_date < cursor, or issue_date = cursor and invoice_no >
  cursor) returns both, in order. A production fix in the billing
  repository, so it stays with the owning lane rather than riding a
  ci-defect branch.
- internal/compliance, TestIntegration_Compliance_HTTPSurface_ServesAndIsManagerGated
  (calendar_integration_test.go:267): every request answers 401
  "Missing or invalid credentials". The suite wires the kit's real
  Guards (testkit.NewStaffGuards plus SetAuthority) but never mints or
  sends the bearer the gate requires (sg.Bearer and the Authorization
  header); the guards-swap fold's shape, which these legs were never
  re-run against.
- internal/dispatch, the wire fence and freight-raise legs
  (integration_test.go, freightraise_integration_test.go): the same
  bearer-less 401 class (testMux wires the kit's Guards and a
  UserLinker but sends no Authorization header), so legs asserting 403,
  404 and 200 all read 401.
- internal/gl, TestIntegration_ListJournals_SourceRefID_HandlerGate
  (handler_sourcerefid_live_integration_test.go:90): the filtered page
  carries the one matching journal while the envelope reports total=0;
  the count and the page disagree about the source_ref_id filter.
- internal/messaging, TestIntegration_PaymentFacts_ResolveTheOrderThroughAllocations
  (payment_rules_integration_test.go:215): the assertion wants branch
  names that read as cities, while migration 0003 plants the dealer
  display names (the city inside a longer company name), so the pinned
  shape cannot hold on any database built from this repository's
  migrations. The same family as the reporting seeded-title pins (the
  HH-179 entry above): either the assertion pins what 0003 plants, or
  the seed carries the city names; the owning lane's call.
- internal/assist/adapters, TestIntegration_AScopedUserCannotReachAnotherBranch
  (integration_test.go:274): the same family and the same migration
  0003 names: the stock lookup's branch row is asserted against the
  city-shaped name.
- internal/sync, the gap and tie-out legs (gap_scopes_integration_test.go,
  bistrack_tieout_demo_seed_integration_test.go): the BisTrack tie-out
  wants its own demo world's ERP side (every account reads zero against
  the BisTrack figures on the shared gate), and the gap legs' shape
  assertions read rows beyond their own (23 keyed payment rows where
  the leg wants exactly its one). The suite wants a scratch world of
  its own rather than the shared migrated-and-seeded gate; the owning
  lane shapes it.
- internal/tour, the surface legs (integration_test.go:191, 340, 406,
  469): every call answers 503 with "authorization dependencies
  unavailable", and the off-state GET answers 503 where 200 is wanted;
  the tour surface's authorization dependencies and on-state are not
  wired by the harness the way the assertions expect.

Two working notes for the owners, both observed while proving the job.
First, a dozen module suites' fixtures read the catalogue, branches and
users the seed role plants, so any gate database for this suite must be
migrated and then seeded (plainly migrated databases fail or skip those
suites). Second, in the unscoped run the composition kit's fixture
branches (FXNA, FXNB, FXSA) were left on the shared module gate after
the red packages' legs ran, and internal/inventory's four-bucket test
(a branch with no stock must still appear) counted five branches where
it wants the two the migrations plant; with the recorded-red packages
out of the run the bleed is gone and inventory is green. Which red
package's cleanup discards the error is not pinned down here; it is
part of the bearer-class repairs above.

## HH hardening, HH-191 round 2: corrections to the recorded-red entry above, from its review

The round 1 review of the HH-191 pull request re-ran all eight packages
against a database built the way the integration job builds it. Three of
the entry's diagnoses above are wrong or incomplete, and an owner working
from them would mis-repair. This append corrects the record; the entry
above is never edited.

- The BisTrack tie-out leg named under internal/sync is internal/gl's
  failure: bistrack_tieout_demo_seed_integration_test.go lives in
  core/internal/gl. The gl package carries TWO red tests, the recorded
  filtered-page one (handler_sourcerefid_live_integration_test.go:90,
  total=0 while the page carries the matching journal) and the tie-out
  (bistrack_tieout_demo_seed_integration_test.go:72). Fixing only the
  recorded one and dropping the gl line turns the integration job red.
- The dispatch diagnosis above says every leg reads the bearer-less 401.
  That covers the fence legs, but the three freight-raise legs read 403
  with a bearer present (freightraise_integration_test.go:464, 483, 535;
  a general manager is refused where the leg expects admission), a second
  cause the bearer repair will not close, and two further legs die at
  setup with "testMux needs a staff-stamped context"
  (integration_test.go:715, 1000). The dispatch owner inherits three
  classes, not one.
- The sync package is red only in suite order. Standalone on a fresh
  job-shaped gate it passes (61 legs, on a used and on a fresh gate
  alike); inside the job's full run it fails the gap legs
  (gap_scopes_integration_test.go:753, 23 keyed payment rows where the
  leg wants exactly its one, and the walkthrough leg with it). An owner
  reproducing standalone will see green; reproduce inside a full run of
  the job's shape, or believe the entry and waste the work.

## HH hardening, HH-191 round 3: the round 2 correction above, corrected (four verified runs)

The round 2 review ran sync four ways and the clause above is wrong in
one part, which this append corrects (the entry and the round 2 append
stand untouched):

- The "on a used and on a fresh gate alike" claim is false for a gate
  used by the job's own scoped run. On such a gate standalone sync is
  RED with the same 23-row failure (59 pass); it is green only on a
  gate that sync alone has used, fresh or reused. The stray payment rows
  (24 rows counted after the scoped run) are left by packages INSIDE
  the green scoped set, not by the red packages and not by suite order
  as such. An owner reproducing on the job's own gate after a green run
  will see red for the same reason the job does.
- The full unscoped run's failure list is longer than the round 2
  append says: TestGapPaymentsLegs (gap_scopes_integration_test.go:753)
  and the walkthrough leg (:878), and TestGapInvoicesLegs (:519) with
  them.
- The dispatch freight-raise 403s do not go through a bearer: those
  legs send no HTTP request at all. The harness
  (freightraise_integration_test.go:177) calls identity.DecideApproval
  directly and maps the error to a status, so "read 403 with a bearer
  present" above misstates the mechanism. The conclusion stands: a
  second cause the bearer repair will not close.


## HH hardening, defect HH-186: the shared dev instance's migration ledger holds a numbering epoch that diverges from main (recorded, D39 territory)

The dev Postgres instance the lane was given (the default DATABASE_URL in
core/internal/config, the hardscapeos database on the local port 5435)
carries a schema_migrations ledger from a numbering epoch that differs
from current main's chain: its rows name a_saved_sort at 0213,
purchasing_delivery_method at 0214 and pos_counter_layout at 0220, where
main's chain numbers the same content 0216, 0217 and 0223, and the
epoch's ledger has no rows for main's 0214_new_entities_and_pricing or
0215_identity_audit. Because the ledger is keyed by whole filename, a
migrate run from current main against that instance applies main's
0214, 0215 and 0216 for real and then fails at main's 0217 with
"column delivery_method of relation purchasing_pos already exists" (the
epoch's own 0214 added it). One such run happened from the HH-186
worktree before the divergence was understood: the instance now also
records main's 0214 through 0216 in its ledger, 0215 having inserted its
staff_role and contractor role-map rows. The instance's schema content
matches its objects either way; no production system and no scratch
database of this lane was affected (HH-186's own integration legs run
against a scratch database built from main's chain, which migrates
cleanly end to end). D39 already assigns the two runners'
applied-history mechanisms to HH-3.7 to report before HH-5; this entry
records that the shared dev instance is itself the live example, and
that rebuilding it from main's chain (rather than patching its ledger)
is the clean remedy. Lane work should keep using scratch databases.

## HH hardening, HH-186 round 2: the incident's cause, named after the review (the entry above stands)

The round 1 review is right that the entry above records the damage and
the remedy but not the cause, and the cause is the recurring trap: the
run set CORE_DATABASE_URL and called the migrate role, which reads
DATABASE_URL (core/cmd/core/roles_oneshot.go, through the config default
at core/internal/config/config.go), so the variable the operator set was
never read and the shared default stood in silently. Nothing refused,
nothing logged a different target than the one hit: the migrate role does
not print its target, so the first visible failure is the migration
error on someone else's ledger. This is defect HH-199's exact class (the
two names for the database URL), and the trap will recur until that
defect's fix lands: one-shot roles reading CORE_DATABASE_URL with no
default and refusing when unset. Until then: always export DATABASE_URL,
never only CORE_DATABASE_URL, when running the migrate or seed roles
by hand, and give them a scratch database's URL in full.
## HH hardening, HH-180 (audit reopen fence): entity types without an owner resolver fail closed on the reopen route

The reopen route `GET /api/v1/audit/transaction/{entityType}/{entityId}` fences a scoped caller
through the entity's owning branch, resolved by narrow owner adapters wired at the composition
root (order and quote are wired today). An entity type with no adapter (payment, invoice,
pos_policy, ar_policy, and every other type the writers mint) answers an empty trail for every
scoped caller, fail closed; a global caller keeps the full trail. Wiring another owner is a
follow-up for its owning lane: one adapter over the owner's branch read plus one line in
wire_money.go. Related, same root cause, pre-existing on main: the policy trails are admin or
manager gated at their own surfaces while the reopen route is staff gated, so before this change
any staffer could read them through the route, and the pos_policy and ar_policy rows are
branch-less besides. The durable fix is the semantic writers stamping `branch_id` on their rows
(roughly 55 sites across modules, pos and till included), which is not this lane's to make: pos
and till are the Counter Till lane's.
## HH hardening, HH-181 (PO send replay): go vet with the integration tag fails on main outside this lane's Touches

`go vet -tags integration ./...` fails on a clean checkout of origin/main at
4f08060 and keeps failing with this lane's commits applied:
`core/internal/printing/receipt_integration_test.go` names `CostCents` on
`pos.LineInput`, a field that struct no longer carries. The untagged
`go vet ./...` passes because the integration build excludes that file.
`core/internal/pos/` and the printing integration test are outside this lane's
declared Touches, so the break is recorded here for the owning lane; the same
command is one of this lane's exit checks and cannot pass repository-wide
until the field mismatch is reconciled.

## HH hardening, HH-194 (AR register CSV export): the register list read still pages by offset and total

The export now reads every row of its filter in one statement (`RegisterFilter.All`), one snapshot, so a
write landing mid-export cannot shift a row. The list read itself still pages by offset and a counted
total and ignores `cursor`, against the cursor rule in `core/api/CONVENTIONS.md` (never offset, never
total). Moving it to the cursor helpers changes the register's contract and repository, so it is recorded
here for the billing lane rather than made inside this one-handler fix. Two further facts for that lane:
the export holds the whole file in memory and has no row ceiling (neither is timed), and the export
neither declares nor reads the `issued_from` and `issued_to` filters the list read takes, so a
date-filtered screen exports every date.
## HH-198 (config validation): `core seed` still reads an unset APP_ENV as dev

`core/cmd/core/roles_oneshot.go` (the seed role, the `cfg.Env != "dev"` check near line 107) runs
with `APP_ENV` unset or empty because `config.Load` resolves it to `dev`, so `core seed` on a box
with the variable forgotten still plants the Tier-1 global admins with the demo password. This
lane did not change it on purpose: the one-shot roles (migrate, seed, demoseed) read an unset
`APP_ENV` as dev so CI, reviewers and local developers can run them without setting it. Serve and
worker, which call `Validate`, now refuse an unset `APP_ENV` (`Config.EnvUnset`). Proposed
follow-up: CI sets `APP_ENV=dev` on the one seed step in `.github/workflows/ci.yml` (the
`go run ./cmd/core seed` line in the integration job, around line 359; there is no demoseed
step), then seed refuses `cfg.EnvUnset`. Demoseed runs in-process from tests and has no dev guard
in `demoseed_main.go`, so it is out of that follow-up's scope. Owner: the platform lane or the
principal.

HH-199 closed the two-names trap recorded above: the one-shot roles (migrate, seed, demoseed) now read CORE_DATABASE_URL with DATABASE_URL as the fallback, refuse a set-and-differing pair before any connection, and print the target host and database name before acting; the localhost dev default is reachable only behind ALLOW_DEV_DEFAULT_DB=true.

### UI parity pass, port work item (branch till/ui-1-port): the template fence against the ported surface's idioms

- **The positions gate over dependents fails on the ported tree, and no
  in-lane fix exists that is still a port.** The design system's positions
  gate (`web/packages/design-system/test/positions.test.mjs`, allow lists in
  `test/_lib/positions.mjs`) scans every dependent package's sources, and the
  Counter Till app is the dependents lock's one entry. The ported surface
  (`web/apps/counter-till/src/ported/`) carries the reference's Lit idioms
  whole: roughly eleven hundred template bindings in the `@event` and
  `.property` forms (the gate's property and event allow table is deliberately
  empty), roving `tabindex` bindings, and bound values on attributes outside
  the gate's allow list (`title`, `value`, `style`, `id`, `placeholder`,
  `name`, `max`, `label`, `alt`). The compliant idiom exists and the app's own
  views use it (host delegation: listeners on the element, controls read off
  `composedPath()`, static template branches); converting the ported surface
  to it is a rewrite of the surface's whole template layer, which the port
  brief forbids ("take the existing and improve: port the code, never
  rebuild"). What the port could do without crossing that line is done and
  committed: the raw-markup gate and the two-places rule pass (the receipt
  window write became a host seam, the panel layout store became host seams,
  `width`/`height` open from static per-size branches, `d`, `class` and `role`
  bindings are quoted, the `html` name appears only in its two places), and
  the tooltip bindings moved to the allowed `aria-label` namespace, which
  loses the hover tooltip until the polish item gives the surface a tooltip
  primitive. The decision this blocker asks for: whether the wiring and
  polish items convert the ported templates to the host-delegation idiom, or
  the design system's allow lists grow to admit the ported surface's forms
  deliberately. Until then the design system suite's one dependents test
  ("every html and svg template in any package that depends on this one sits
  in an allowed position") fails, and the failure is the record, not a
  surprise: every other suite in the loop is green on this branch.

  The lead has ruled on it (the fix commit that follows the ruling): the
  positions gate's allowance grows deliberately for the ported directory, per
  the dependents lock's own mechanism ("this lock must name it deliberately").
  The entry is the ported prefix named in the gate's lib
  (`apps/counter-till/src/ported/`) with the binding positions enumerated by
  name: the . and @ bindings the ported files use, their boolean bindings
  besides ?disabled, the attributes they bind values onto, and a whole-value
  src for the thumbnail route; style is the one deliberate admission past the
  gate's own security refusal, and the raw-markup refusals keep biting inside
  the prefix. Converting the ported templates to host delegation stays the
  rewrite row 563 forbids, so the ported code keeps the reference's idiom;
  every HH-written file outside the prefix is held exactly as before, a new
  binding position inside the prefix is still refused until it is added on
  purpose, and the gate's own narrowness guards pin those edges. The design
  system's suite is green on the strength of that entry.

## UI parity pass, wiring work item (branch till/ui-2-wire): the ported surface mounted, and what the acceptance item must know

The ported terminal is the app's one surface after the entry cards (the sale
suite's selectors moved onto the ported surface's own test ids; each move is
named in the item's report). Recorded here so the acceptance item and the
merge read it before running anything selector-shaped:

- **W9's acceptance suite still holds the OLD surface's selectors**
  (`test/acceptance/`: drawer-status, ticket-line, receipt, tender-refusal,
  the aria labels of the old ticket). The old views came out of the tree with
  the mount, so the acceptance item's selector moves are owed before
  `npm run acceptance` can read this branch; the sale suite's own moves in
  this branch are the worked example.
- **The pop-out posture differs from the reference, on purpose.** This app's
  committed sink gate refuses every browser storage and channel path, so the
  reference's session handshake and its local-store draft handoff have no
  home here. The pop-out opens the same app at a popout address naming the
  flow card; the second window signs itself in through the same public demo
  route and works the same branch drawer. The draft handoff is the server's
  own park: park the ticket, resume it in the second window.
- **The detached layout's pixel boxes persist in tab memory**, for the same
  sink gate; the layout CHOICE rides the server's workspace row and follows
  the person, which is the part HH's contract carries.
- **`listProducts` in the ported pos-api rides the authed door**: the
  reference fetched the catalogue list bare of credentials. The contract
  marks `GET /api/v1/products` `x-role: public` (the catalogue is public
  data), so no credential is needed; the ask rides `api()` anyway, which
  sends the bearer harmlessly and keeps the counter's reads on one door.
Closed by the refund-sum lane: the refund approval gate judged each return alone, so one sale could walk its whole value out of the drawer in approved-sized slices (the comprehensive review's finding, ruled per the principal); the gate now sums the sale's earlier committed returns with the new one against the configured amount and refuses the crossing return with the contract's 409, the returns of one sale serialized by a transaction-scoped advisory lock, on fix/refund-approval-sums-per-sale.

## Counter Till pre-ring and receipts pass, work item B (branch till/receipt-formats): the receipt's letterhead prints the branch address, so the demo address converges too (recorded, one file outside the lane's fence)

The quality receipt item (decision log row 666) makes the branch address a
printed fact: the 80 mm receipt's letterhead serves the branch row verbatim
through printing's ReadBranchHeader, and the brief binds the lane to "never a
real dealer's name" on it. On the demo database migration 0003 plants each
branch's address line1 as a note to the first dealer, with the first dealer's
real trading name inside it, so the demo box's receipt would have printed that
name to its test customers.

The brief's own instruction authorizes the fix, and the mechanism is the
review round P2-1 precedent recorded above (the branch display words): one
extension inside core/internal/demo/populate.go, outside this lane's declared
printing fence. The populate's renameDemoDealer now converges the two demo
branches' address line1 onto invented ones (Trenton: 12 Yard Gate Road,
Kingston: 9 Yard Gate Road), each update guarded on the value it would write
so a re-run writes nothing, counted in the run report. Real deployments keep
their own addresses; migrations stay untouched.

## Counter Till pre-ring and receipts pass, work item B (branch till/receipt-formats): the exit test's two margin glance legs are red on main itself (recorded, routed to the margin surface's owner)

The receipts item's own acceptance legs (the cash receipt document, the
full page format, the marked reprint) pass on the exit test's box. Two legs
of other lanes' surfaces fail beside them, and probes prove they fail
without this branch: a box built from pure origin/main (b8e4009) and a box
built from pure cd46fd6 each answer the CLERK's margin glance with the
byte-absent `{}` (10-world's "the clerk's margin glance answers: the
configuration names who sees cost and margin", and 20-sales' "a contractor's
pavers: the account price rings, and a below floor line rings too", whose
staff-side read meets the same answer). The previous train's exit run, on
the pre cd46fd6 main, held both green. The till-ui-train's P3-10 ruling made
the surface follow the policy word; the box's word is MANAGER_UP, and the
clerk's SERVER side glance now refuses too, while these two legs still hold
the old expectation ("the glance itself still answers the clerk
server-side"). The two legs' home is the margin surface this lane is
expressly out of scope of; the owner (or the principal) rules whether the
legs follow the ruling or the demo box's policy word changes. CLOSED inside
the receipts stack: the base branch till/margin-glance-flip flips the legs
to the ruling (the surface follows the policy word, and the clerk's glance
is the byte-absent answer the policy names), and the exit test on the stack
head runs green, so the row's question is answered and the routing here is
overtaken by the base's own round 2 closure.

## L2 (till parked dialog): the positions gate admits one new ported event name (recorded, done in this lane)

The parked dialog fix's round 2 wires a discard inside the open dialog to the
host the way every other child-to-host notification in the ported surface is
wired: a bubbling composed CustomEvent consumed by a template `@` binding
(`@pos-parked-discard` on the `hos-pos-parked` mount in pos-terminal.ts,
beside the existing `@pos-parked-close` and `@pos-parked-resume`). The
design system's dependents positions gate refuses every binding name the
ported allow table does not carry, so the tree's root gate (and the PR's
hosted ci with it) went red on the new name.

The lane's declared Touches consume web/packages rather than edit them, but
the gate's own record says a new position inside the ported prefix "is still
refused until it is added here on purpose", and this lane's precedent (the
dependent lock edit below) made exactly this kind of one list edit with the
reasoning recorded and the revert offered. The alternative shapes were
worse: withholding the notification splits the dialog's wiring across two
idioms (close and resume through template bindings, discard through an
imperative listener attached per open), and letting the button's count go
stale after a discard is the defect the round set out to close. The edit
adds `@pos-parked-discard` to `PORTED_PROPERTY_OR_EVENT` in
`web/packages/design-system/test/_lib/positions.mjs` and nothing else; the
table's own narrowness test (an unlisted name refused even inside the
prefix) keeps biting everywhere else. If the lead or a reviewer rules the
edit out of bounds for this lane, it reverts as one line and the decision it
forces is whether the discard notification may reach its host at all.

## gl is back in the integration job (decision log row 802, appended; the entries above are never edited)

The two red tests the HH-191 entry and its round 2 correction record for internal/gl are repaired and the package
rejoins the integration job's run (the `gl` word leaves `red=` in `.github/workflows/ci.yml`). Neither was a code
defect; both were stale tests, repaired test only, zero production lines:
- `TestIntegration_ListJournals_SourceRefID_HandlerGate`: the contract's `GlJournalPage` is a cursor page (items,
  next_cursor, limit) with no `total`, and the handler runs no count query; the test decoded a `total` the wire never
  carries. The page was always right. It now pins the contract's page (the matching journal, and `next_cursor` null on a
  complete last page).
- `TestIntegration_M2C1_DemoSeed_StagingTieOutTies`: migration 0221 deliberately posts 0056's four demonstration
  journals back out as POSTED reversing entries, so the June closing balance of every fixture account nets to zero and
  the static fixture cannot tie. The test is now `TestIntegration_M2C1_DemoSeed_RetiredSeedNetsZero` and pins the
  retirement; the tie-out machinery stays proven by `bistrack_tieout_integration_test.go`.
The packages still excluded after this change, and why, are in the stand-in's red list reading in the pull request body.

## internal/billing is back in the integration job (decision log row 804, appended; the entries above are never edited)

`TestIntegration_Billing_ListInvoices_TenantFenceFilterAndPaging` was red for a REAL production defect, not a stale test and not a
tenant leak (the tenant fence assertions all passed). The invoice list orders `issue_date DESC, invoice_no ASC` but its cursor
predicate was the row comparison `(issue_date, invoice_no) < (cursor date, cursor no)`, which is a descending comparison on both
keys: after a page ending at one invoice, every other invoice of the SAME issue date with a greater invoice number was skipped,
so a customer paging the portal's AR list lost those invoices. The predicate is now `issue_date < d OR (issue_date = d AND
invoice_no > no)`, matching the ordering. The test (unchanged) is the red proof on main and green with the fix; the package is
green alone and the `billing` word leaves `red=` in `.github/workflows/ci.yml`.

## internal/assist/adapters is back in the integration job (decision log row 804, appended; the entries above are never edited)

`TestIntegration_AScopedUserCannotReachAnotherBranch` was red only on the stock lookup's branch row name: the test asserted a
display name with a comma, while migration 0003 plants the dealer name, an em dash and the city. Every fence assertion in the
test passed on main (the order, raw id, customer and payment cases and the Kingston mirror), so this was a stale test and not a
leak. The assertion now requires the row to be Trenton's and never Kingston's; a production bite (the adapter naming the wrong
branch) turns it red. The package is green alone and the `assist/adapters` word leaves `red=` in `.github/workflows/ci.yml`.

## internal/compliance is back in the integration job (decision log row 804, appended; the entries above are never edited)

`TestIntegration_Compliance_HTTPSurface_ServesAndIsManagerGated` read 401 because the test drove the kit's real guards without a
bearer: after the guards swap the route is refused "Missing or invalid credentials" before the authority is consulted, which is
the guard doing its job and not a leak (an unauthenticated call reaches nothing). The test now sends the staff bearer and the
X-Org header the kit's double admits. The fence legs bite: with the calendar route moved behind the staff guard instead of the
manager guard, the frontline authority leg reads 200 and the test fails; restored byte identical. The package is green alone and
the `compliance` word leaves `red=` in `.github/workflows/ci.yml`.
