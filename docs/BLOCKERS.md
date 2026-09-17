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
- **Open: additive migrations against a restored dump (Phase B precondition).**
  Phase A's fixture is the migrations applied from zero plus the ERP's own
  seed. The check that the additive set applies cleanly on top of a restored
  HardscapeOS dump has not been run. D31 records it as open and a
  precondition of Phase B, which does not start until it has been run
  against a restored dump and reported.
- **Open: special-order migration against the dump's synthetic skus (Phase B
  precondition).** D16 requires the special-order migration to parse every
  synthetic sku in a restored HardscapeOS dump. With no restored dump
  available this check cannot run in Phase A. D31 records it as open and a
  precondition of Phase B, which does not start until it has been run
  against a restored dump and reported.

## HH-3.7 (tracking)

- **Open: `erp-routes.txt` vendored from a `Demo` commit, not `master`.**
  D33 records that the commit from which `core/api/erp-routes.txt` was
  vendored under D22 is a `Demo` commit sixteen ahead of `master`, not
  `master`'s head (`6eaea3ec`), so the parity gate in `ci` has been proving
  the contract against a `Demo` snapshot. Refreshing the file from `master`
  with `refresh-erp-routes.mjs` is HH-3.7's first entry; the route
  difference the refresh reports is the measure of how far the snapshot was
  off. Not fixed in HH-3.1: `core/api/` is outside its scope and the
  tracking lane owns the tool by D22.
