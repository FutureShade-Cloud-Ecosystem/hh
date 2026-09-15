# AGENTS.md

Canonical guidance for every harness session working in this repository.
`CLAUDE.md` imports this file and adds Claude-only notes; keep the two in
sync by editing this file, never the import.

`context/FUTURESHADE-CONTEXT.md` is authoritative on architecture. Where it
and this file disagree on a repo-local convention, this file wins; on
architecture, the context file wins. Read `docs/adr/` before changing
architecture; any new architectural decision gets an ADR numbered after the
last one in context section 12.

## What this is

The `hh` monorepo: Hardscape House on the App Shape (context section 4),
merging HH Yard and HH Pro into one repository with a unified Go core and
role-based micro-apps in audience shells (ADR-014, DECISIONS.md in
`docs/analysis/`). One repository per app: `core/` (Go modular monolith with
`serve`, `worker`, `migrate` roles), `web/` (shared packages plus one Vite
entry per micro-app), `tauri/` (one shell per audience: `hh-work`, `hh-field`,
`hh-pro`), `statecharts/`, `agents/`, `manifest.yaml`.

## House rules for every session

- No em dashes or en dashes anywhere: code, comments, docs, commit messages,
  pull request text. Use commas, colons, or parentheses.
- No dates, durations, or calendar references in committed prose.
- Commits are signed off (DCO, `git commit -s`). All changes land through
  pull requests; the `ci` check must pass.
- Work only inside your lane's declared Touches. Other lanes run beside you.
- Secrets arrive as environment variables at spawn. Never read a vault, never
  print a secret, never commit one.
- Blockers go in `docs/BLOCKERS.md`; record and continue rather than stop.
- `docs/analysis/` is frozen history copied from hh_pro_dibbits and its bodies
  are never edited except where a recorded decision names the file live or
  names the edit an exception: `DECISIONS.md` is live from D20 and
  `role-catalog.yaml` from D23, and D23 and D25 are the recorded body
  exceptions (D27). `docs/sessions/` holds the live brief a Session works
  against, and where a live brief and its frozen origin disagree the live one
  wins.
- Statecharts and the OpenAPI document are the spec; tests generated from
  them are the acceptance criteria.

## Invariants carried from the source products

These rules were earned in the two products this repository replaces
(HardscapeOS ERP and HH Pro). Each is stated with its reason. They bind every
lane from the first commit.

### The domain core is framework-free

Everything that touches the domain, pricing, an ERP, or an AI tool lives in
plain, framework-free code; the UI layer is thin and only renders core state
and calls core actions. Reason: the distribution target is embeddable web
components (Lit), so when a UI framework is swapped, only the UI layer is
rewritten. Enforce with a lint rule and a test, never the lint rule alone: a
lint rule can be disabled inline and a test cannot.

### Money is integer cents, computed by the core

Money is always integer cents, never floats, and is never computed
client-side. Convert through the decimal string at the boundary, not by
multiplying by 100, because `1.005 * 100 === 100.49999999999999` rounds a
half-cent the wrong way. Quantities are fixed-precision decimal and every
quantity carries a unit of measure; inventory moves are double-entry. Reason:
this is real money, and a wrong price at the counter is discovered by a
customer, not by a test.

### Daily means the dealer's day (the house time zone)

Any "daily" limit, cap, or rollup means the dealer's local day, not UTC. UTC
rolls over mid-afternoon or evening everywhere west of Greenwich: a daily cap
once lifted at 8pm while the yard was still open, giving a capped contractor
a fresh allowance over dinner. A dealer whose server sits in another zone
needs a configured time zone; UTC is the one guess that is wrong for almost
everybody.

### Never render a confident wrong number

An empty or unknown value renders as an explicit placeholder, never as a
plausible-looking zero. An empty draft shows a blank marker, not `$0.00`;
a subtotal sums only priced lines, so an order awaiting the quote desk never
reads as an order worth nothing. Reason: a confident wrong number is
dishonest in a way that costs money, and it is worse than admitting the
number is not known yet.

### Disabled must look disabled

A filled brand button at reduced opacity still reads as clickable in
daylight. Disabled controls drop their fill and go grey: fade is not a state,
colour is. The rule applies everywhere, not just in the shared button
component; the counterexample was a raw "Accept and sign" button on the one
screen where someone decides to spend money. Related: with no key configured,
the assistant renders disabled, never faked, because a mock assistant would
undermine the one part of the product that has to be real.

### Module boundary rules (the core)

The core is one binary of many flat modules, kept modular by fence rules:

- Zero cross-module table access: a module's repository may touch only the
  tables it owns.
- Cross-module reads go through the owning module's interface; cross-module
  write side-effects go through the event bus only.
- Consumers declare the narrow interface they need; required collaborators
  arrive by constructor, with setter injection reserved for genuinely
  optional integrations.
- Transactions span services through a context-carried transaction, never
  through shared repositories.
- Scoped tables (`org_id`, `tenant_id`, branch) are never queried without a
  scope predicate; scoping is enforced in server middleware, not left to
  individual queries.

Reason: the fence is what keeps a modular monolith from collapsing into a
big ball of mud, and it is what lets the core gate every endpoint by role so
the shell is never the security boundary.

### Migrations are forward-only

Migrations are numbered, never edited after commit, and never renumbered.
Reason: the platform snapshots the database, migrates, then starts the new
binary and rolls back on failure; edited history breaks that contract, and a
renumber silently reorders what already ran.

## Decisions pending

Five ADR drafts in `docs/adr/` carry decisions already taken in HH-1 (`docs/analysis/DECISIONS.md`, moved into this repository by lane HH-2.5) and hold status `Proposed` until Colton signs them off at the HH-2 end checkpoint:

- ADR-0015 pricing-model resolution (D1)
- ADR-0016 tenancy and identity (D3)
- ADR-0017 shell split and first micro-app (D2)
- ADR-0018 citext on the extension allowlist, and the PostgreSQL major-version move
- ADR-0019 contract entities (D7 to D12)

Until sign-off, lanes treat the decisions as taken (the session briefs under `docs/analysis/sessions/` already do) and the drafts as the record to argue against.
