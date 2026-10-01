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
