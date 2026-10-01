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
with the no-op stub in demo_off.go. The default build's behavior is
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
substance, and each needs its owner's close.

- **The demo world homes C01 and C09 to Kingston, while the demo logins reach
  Trenton alone.** The seed writes its customers onto `b.in.Branches[i]`, and
  on a migrated database that order puts branch 0 at Kingston: C01 (the
  contractor whose 18 percent paver price the plan's screen 4 names) and C09
  (the account on hold the plan's screen 5 names) come out homed to Kingston,
  and the customer picker is fenced to the caller's own branch server-side, so
  the Trenton counter cannot reach either row. The run proves screen 4 with
  C02 (a Trenton contractor, 12 percent off pavers) and C03 (Trenton, 10
  percent off bulk, whose Granular A sits below the 20 percent bulk floor and
  rings anyway), and screen 5 with C07 (a Trenton retail account whose zero
  limit the credit gate refuses in words, the same refusal the hold would
  show). The lead closes this inside the goal by either moving the two
  accounts' home branch in the seed or granting the demo logins nothing more
  and renaming the plan's rows; it is W3 and the seed's file, not this lane's.
- **No seeded sale at Trenton crosses the refund approval amount as a return.**
  DEMO-TRT-S09 is the seeded sale above 250.00 (251.71 with the seed's tax),
  but the return rail recomputes a return's amounts from the line, and the
  seeded line's own tax answer is zero: the whole line refunds at 222.75, under
  the threshold. The run crosses the amount with a sale it rings itself (sixty
  wall blocks, 847.50). The seed's tax on counter sales and the return rail's
  tax answer disagree; which one moves is the seed lane's call, recorded here
  so the disagreement does not read as the return rail's defect.
- **The demo build's serve wiring does not attach the role gate.** main.go's
  `registerRoutes` wraps its registrar in `authz.NewRoleGateRegistrar`;
  demo_on.go's `demoRegisterRoutes` (the wiring the demo image serves) builds
  the same graph on the raw registrar, so on the demo box the role gate does
  not run and any staff route the tier gate admits is served to the clerk
  (a dispatch read answers 200 to inside_sales, which the role gate's own
  tests hold shut). The plan's screen 11 manager cell (dispatch reads and
  price rule writes admitted to the manager and refused to Sales) is provable
  on this box only on the price rule write, whose refusal is the tier gate's; the
  run asserts exactly that and no more. The fix is demo_on.go's to build the
  same gate main.go builds; it is one wiring block, core, not this lane's.
- **The parked list serves a bare array against the contract's page shape.**
  `GET /api/v1/pos/parked` answers `PosParkedTicketPage` in the contract (an
  object with `items`), and the served core answers the bare array instead, so
  the app's field-by-field read refused the whole list. This lane's app now
  reads both shapes and refuses anything else; the server (or the contract) is
  the side to reconcile, and the same pass may want to look at the counter
  search's optional `sold_by`, which the served core leaves out of every hit
  (the app falls back to the required `base_uom`).
