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
