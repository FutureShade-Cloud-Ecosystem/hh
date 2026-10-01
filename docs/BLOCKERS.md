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

## The Counter Till cycle, lane L3 (W2): a refused commit message amend (recorded, work continuing)

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
