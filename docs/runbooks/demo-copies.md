# Demo copies: the three FB Deploy applications of the public demo

Operator runbook for the three demo applications of the Gable Landscape Supply
publish (master plan section B.9, Phase 8). Each copy is a separate FB Deploy
application running the same demo image with a different set of variables and
its own database. The staging copy signs people in with the gate phrase; the
testers' copy and the public copy sign people in through FB Console
(`CORE_SIGNIN_MODE=console`).

This runbook covers three copies, how each is built and deployed, the console
sign in variables by name, the persona partners list, how to reset a copy,
the outbound messaging sink and rate limits as the plan describes them, the
exit test, and a short incident section. Every fact cites the file it came
from; anything the plan leaves open is marked OPEN, never invented.

Sources, in the order read:

- `principal/state/HH-MASTER-PLAN-r1.md` (master plan, sections A, B.9, D1 to D5)
- `hh-s4/CP1-STATE.md` (sign in and deploy state at `51f1dd79`)
- `hh-s4/PRE-REAL-DATA-PLAN.md` (Phase 8 items `HH-697` and `HH-698`)
- `deploy/demo/README.md` (the demo image, the boot order, the operator variables)
- `deploy/demo/entrypoint.sh` (the boot order, the gate hash, the resume refusals)
- `deploy/demo/exit-test.sh` (the throwaway local end to end proof)
- `deploy/demo/Dockerfile`, `deploy/demo/Caddyfile.template`,
  `deploy/demo/write-web-config.sh` (the image, the proxy, the web runtime config)
- `core/internal/identity/sessionsigner.go` (the signing key file mode check)
- `core/cmd/core/demoenv_inventory.go` (the allow list of `CORE_` names)

## The three copies, and who uses each

The master plan section D3 names three FB Deploy applications and three
databases. Promotion order is staging first (`principal/state/HH-MASTER-PLAN-r1.md`
row 483, cited in plan section D3). The three copies are:

1. **Staging** at `hh-demo-staging.futurebuild.ai`. Colton and the principal.
   Runs with `CORE_SIGNIN_MODE=demo` (the gate phrase stays). Used for the
   sandbox probe (`PRE-REAL-DATA-PLAN.md` section 3, `HH-691`) and for the
   internal walk and device testing (`hh-s4/CP1-STATE.md` section 2).
2. **Testers'** at `hh-demo.futurebuild.ai`. The product team. Real sign in
   through FB Console, no phrase. The Phase 1 exit test (CP-1) lands here,
   and the testers run their rounds on this copy (`HH-MASTER-PLAN-r1.md`
   section D5).
3. **Public** (host to come). Anyone. The open persona picker, the entry
   page, the downloads page, the scheduled reset, the messaging sink, the
   rate limits, the sandbox processor only, no export, the feedback widget
   off or routed to a public inbox (`HH-MASTER-PLAN-r1.md` section A6).

What "published" means is the exit test in `HH-MASTER-PLAN-r1.md` section A,
run by section A's checks and recorded under `docs/sessions/` in infra. The
public copy is reviewed by Sonnet and Opus security reads with no P1 or P2
open before it is opened to anyone (`HH-MASTER-PLAN-r1.md` section A6).

I could not confirm: the public host name. The plan names the entry page and
the downloads page and says DNS for the public host and the downloads is
Colton's (`HH-MASTER-PLAN-r1.md` section B.9, D4); the host string is not yet
on record.

## How a copy is built and deployed on FB Deploy

### The image

Each copy is one FB Deploy application running the demo image built from
`deploy/demo/Dockerfile`. The image holds the core built with `-tags demo`,
the built files of every web app (front door at `/`, till at `/till/`,
buying-and-stock at `/buying-and-stock/`, public desk at `/q/`, HH Drive at
`/drive/`, Manager Home at `/home/`, Yard Pick at `/yard/`, Money Desk at
`/money-desk/`, Dealer Admin at `/dealer-admin/`, Quote Desk at `/quote/`
when present, and the three Pro apps under `/pro-procurement/`, `/pro-pay/`
and `/pro-field/`), Caddy in front, and the frozen migration set
(`deploy/demo/README.md` opening section). The build command is
`docker build -f deploy/demo/Dockerfile -t <tag> .` from the repository root.

### What the operator sets

Nothing is baked into the image and nothing is committed; every value reaches
the box at runtime (`deploy/demo/README.md` sections "What the operator sets"
and "The console sign in mode"). The values below are the names only, never
the values.

The baseline runtime values (every copy):

| Variable | Kind |
|---|---|
| `DATABASE_URL` | the one database; the entrypoint derives `CORE_DATABASE_URL` from it (`deploy/demo/entrypoint.sh` line 92) |
| `PAYMENT_VAULT_KEY` | the vault key the invented payment credentials are sealed under; an operator value, so restarts unseal (`deploy/demo/README.md`) |
| `HH_ACCEPTANCE_SIGN_KEY_ID`, `HH_ACCEPTANCE_SIGN_KEY` | the customer quote acceptance signing pair (`deploy/demo/README.md`) |
| `DEMO_GATE_BCRYPT_HASH` | the gate's bcrypt hash, from the vault, runtime only; staging copies set it, console copies do not (`deploy/demo/README.md`) |
| `PORT` | the public port, default `8880` (`deploy/demo/entrypoint.sh` line 112) |
| `CORE_INTERNAL_PORT` | the core's loopback port, default `9090` (`deploy/demo/entrypoint.sh` line 113) |
| `CORE_STAFF_AUDIENCE`, `CORE_TENANT_AUDIENCE` | the token audiences (defaults `hh-staff` and `hh-tenant`) |
| `CORE_SIGNIN_MODE` | required, no default (`deploy/demo/entrypoint.sh` line 86); `demo` for staging, `console` for the testers' and public copies |
| `HH_DEMO_MINT_VISITOR_ALLOWANCE` | optional, the per-vitor mint allowance on the testers' copy; staging and public deployments leave this unset (`deploy/demo/README.md`) |
| `CORE_DEMO_TOKEN_TTL` | optional, the demo session length the mint issues |

The console sign in variables (`CORE_` and `HH_WEB_` names) are listed in the
sign in section below.

### The signing key file, and its mode

The console copies need a persistent ES256 (P-256) session signing key pair.
`CORE_SESSION_SIGNING_KEY_FILE` names the path to the PEM private key inside
the container. The platform mounts the secret file read only with mode `0400`
or `0600` and owned by the user the core runs as. The boot refuses a file
readable by group or other, and refuses a file the core's user cannot read
(`deploy/demo/README.md` and `core/internal/identity/sessionsigner.go`
lines 92 to 99). `CORE_SESSION_KEY_NEXT_FILE` and
`CORE_SESSION_KEY_PREVIOUS_FILE` are the optional staged and retired PEM
public halves, any mode, one PEM block each (`deploy/demo/README.md`).

I could not confirm: a `0400` copy by the entrypoint. The sources that name
`0400` are the README ("mode 0400 or 0600") and the signer ("refused: the
platform mounts the secret file 0400 or 0600"). The entrypoint script
(`deploy/demo/entrypoint.sh`) does not perform a `0400` copy of the signing
key. The mount is the platform's job.

### The health check

The health leg is `GET /healthz`, served with no phrase. The image's boot
probes it on a loopback before Caddy takes the public port
(`deploy/demo/entrypoint.sh` lines 293 to 303) and the throwaway exit test
asserts `/healthz` answers 200 with no phrase asked (`deploy/demo/exit-test.sh`
"the health leg asks for no phrase"). FB Deploy wires the same leg to the
copy's health check.

### One replica per copy

Each copy runs as one replica and no more. The demo issuer's signing key is
born in one process and never leaves it, so a second replica would mint
tokens the first refuses and sign testers out on every balancing decision,
and both replicas would run the populate against one database
(`deploy/demo/README.md` "One replica", master plan G5).

## Console sign in: the `CORE_` and `HH_WEB_` variables

A copy that signs real people in through FB Console runs the image with
`CORE_SIGNIN_MODE=console`. The console mode is a declared shape of the demo
environment guard: it carries a live connection to FB Console (the one
outbound a console copy makes) and the guard allows exactly the names listed
here, refusing any other `CORE_` name at boot, naming the variable and never
its value (`deploy/demo/README.md` and `core/cmd/core/demoenv_inventory.go`).
The console mode does not serve the old demo mint routes; both
`/api/v1/demo/flows` and `/api/v1/demo/session` answer `404`
(`hh-s4/CP1-STATE.md` section 1).

### The `CORE_` names (the core side)

Set in FB Deploy for each console copy. From `deploy/demo/README.md` "The
console sign in mode":

| Name | Kind | What it is |
|---|---|---|
| `CORE_SIGNIN_MODE` | public value | `console` |
| `CORE_TOKEN_ISSUER` | public value | one value per deployment, never `hh-demo`; a token minted on one copy never verifies on another |
| `CORE_SESSION_SIGNING_KEY_FILE` | secret file (a path) | the PEM private key (ES256, P-256); mount read only with mode `0400` or `0600` |
| `CORE_SESSION_KEY_NEXT_FILE` | public half (a path) | optional; the PEM public key staged for a rotation |
| `CORE_SESSION_KEY_PREVIOUS_FILE` | public half (a path) | optional; the retired key's PEM public key while its tokens live |
| `CORE_APPWRITE_ENDPOINT` | public value | the FB Console API endpoint, ending in `/v1` |
| `CORE_APPWRITE_PROJECT` | public value | the HH project id in FB Console |
| `CORE_APPWRITE_API_KEY` | secret | the core's server key: `users.read`, `teams.read`, `files.read`, `files.write` only (it cannot mint a JWT for anyone) |
| `CORE_EXCHANGE_TOKEN_TTL` | public value | optional Go duration, at most 1 hour (the default is 1 hour) |
| `CORE_EXCHANGE_JWT_BOUND` | public value | optional Go duration, at least 900 seconds, default 20 minutes; the furthest a FB Console JWT's `exp` may lie ahead |
| `CORE_SIGNIN_EMAIL_DOMAINS` | public value | comma list; with `CORE_SIGNIN_EMAIL_ADDRESSES` the allow list of sign in emails; both empty or unset refuses every sign in |
| `CORE_SIGNIN_EMAIL_ADDRESSES` | public value | comma list of whole addresses |
| `CORE_TRUSTED_PROXIES` | public value | comma list of peer addresses allowed to name the client; set it to the address Caddy connects from (`127.0.0.1` in this image), or every visitor shares the proxy's one rate limit bucket |
| `CORE_PERSONA_CALLER` | public value | `partner` on the testers' copy, `open` on the public copy; no default, a console copy that does not say does not boot |
| `CORE_PERSONA_PARTNERS` | public value | comma list of FB Console user ids that may enter as a persona on the testers' copy (each also needs a linked `ACTIVE` staff row); empty refuses every caller; refused at boot on an `open` copy |
| `CORE_JWKS_URL` | unset | the console copy verifies its own tokens; if it is still set the boot logs one warning and ignores it |

The persona route `POST /api/v1/auth/persona` exists in console mode only. On
the testers' copy (`partner` mode) it verifies the partner's core staff
bearer token, refuses tenant tokens and persona tokens, checks
`CORE_PERSONA_PARTNERS`, records an audit row `"partner P entered as persona
R"`, and mints a core token for `persona:<id>`. On the public copy it is open
and limited per address and per address and persona
(`deploy/demo/README.md` "The console sign in mode", `hh-s4/CP1-STATE.md`
section 1, `core/internal/demo/persona/handler.go`).

The persona partners list is the value of `CORE_PERSONA_PARTNERS`: the FB
Console user ids of the named partners who may enter as a persona on the
testers' copy, each one stamped onto one of the demo populate's unlinked
`ACTIVE` staff rows in `users` (a step the principal runs on the testers'
copy's database; see `hh-s4/CP1-STATE.md` Step 3). The entry is refused at
boot if the copy is `open`.

### The `HH_WEB_` names (the web side)

Set in FB Deploy and read by `deploy/demo/write-web-config.sh`, which writes
`socket-server/app/config.json` at container boot from the names below. The
entrypoint refuses to start, naming the variable and never its value, if one
is wrong (`deploy/demo/README.md` "What the operator sets"):

| Name | Kind | What it is |
|---|---|---|
| `HH_WEB_MODE` | required | the web runtime config the box serves at `/config.json`: `demo` or `console`; unset or `demo` keeps the file the image baked (`{"mode":"demo"}`); `console` writes it at boot |
| `HH_WEB_ORG` | required on console | the org slug (`hh-demo` on the console copies) |
| `HH_WEB_CONSOLE_ENDPOINT` | required on console | FB Console's endpoint (https, or http on localhost) |
| `HH_WEB_CONSOLE_PROJECT_ID` | required on console | the HH project id |
| `HH_WEB_DEALER_NAME` | required on console | the dealer's name |
| `HH_WEB_CORE_BASE_URL` | optional | the core's base URL (empty means the page's own origin) |
| `HH_WEB_DEALER_MARK` | optional | a built in mark code |
| `HH_WEB_PERSONAS` | optional | `true` or `false` |
| `HH_WEB_PERSONAS_OPEN` | optional | `true` or `false`; the public copy sets both |
| `HH_WEB_PERSONA_CALLER` | optional | `public` or `partner`, must agree with `HH_WEB_PERSONAS_OPEN` |

The console mode writes `/srv/app/config.json` at boot, so the container must
run as root with a writable `/srv/app` (no `--read-only`, no `--user`), and a
copy that sets any `HH_WEB_*` must also set `HH_WEB_MODE`
(`deploy/demo/README.md`).

For the testers' copy the principal's deploy notes (`hh-s4/CP1-STATE.md`
Step 2) name `HH_WEB_MODE=console`, `HH_WEB_ORG=hh-demo`,
`HH_WEB_CONSOLE_ENDPOINT=https://api.futurebuild.ai/v1`,
`HH_WEB_CONSOLE_PROJECT_ID=<hh_console_project_id>`,
`HH_WEB_PERSONAS=true`, `HH_WEB_PERSONAS_OPEN=false`,
`HH_WEB_PERSONA_CALLER=partner`, and
`HH_WEB_DEALER_NAME="Gable Landscape Supply"`.

I could not confirm: the public copy's exact `HH_WEB_*` values. The README
names the fields and says `HH_WEB_PERSONAS=true` and `HH_WEB_PERSONAS_OPEN=true`
on the public copy; the rest of the console names are the same shape.

## Resetting a copy

The reset script is `deploy/demo/reset-demo-db.sh`, named in
`hh-s4/PRE-REAL-DATA-PLAN.md` row 58 ("wiring `deploy/demo/reset-demo-db.sh`
daily timer") and in `HH-MASTER-PLAN-r1.md` section D3 ("the public copy
rebuilds its world on a schedule and on demand; the testers' copy only on
Colton's word before a round"). The public copy resets on a schedule and on
demand; the testers' copy resets on Colton's word before a round
(`HH-MASTER-PLAN-r1.md` row 485, cited in section D3); staging resets when
the principal needs a clean world for the sandbox probe.

The reset keeps the database resource and wipes the world inside it. What
the reset wipes is the demo world's data: the migrate runs, the seed writes
its missing rows, the populate completes, and demo-check runs before serve
(`deploy/demo/entrypoint.sh` step 4). What the reset keeps is the database
itself, the operator variables (`DATABASE_URL`, the `CORE_` and `HH_WEB_`
names, the gate hash, the vault key, the signing key file), the FB Deploy
application, and the host name. The shared phrase and its hash never reach a
file in the image's layers, a volume, or the writable layer
(`deploy/demo/README.md` "The phrase gate"); the reset does not change that.

OPEN: the script's exact flags, the schedule cadence (the plan says "on a
schedule and on demand" but does not name a clock time), and whether the
script is also called from a probe's command (probe 7 names "the reset runbook
run once"). The pre-real-data plan row 58 names a "daily timer" but the
master plan forbids clock times in any record, so the cadence is OPEN until
Colton rules.

## The outbound messaging sink and rate limits

The plan section A6 names the protections the public copy must hold before it
opens. The named ones are:

- **Outbound email and text routed to a sink** so no real address is ever
  messaged (`HH-MASTER-PLAN-r1.md` section A6).
- **Rate limits on sign in and on every write** (`HH-MASTER-PLAN-r1.md`
  section A6). The core side already enforces per-address and per-user limits
  on the exchange (`hh-s4/CP1-STATE.md` section 1, Gate 2: per-address
  `60/min` and per-user `10/min`, behind `CORE_TRUSTED_PROXIES`).
- **A scheduled reset** on the public copy (section A6, D3).
- **The sandbox processor only**; no export; the feedback widget off or
  routed to a public inbox (section A6).
- **Sonnet and Opus security reads** with no P1 or P2 open before the public
  copy opens (section A6).

OPEN: the exact sink address (the plan names a sink but not its identity), the
limit values for writes other than the exchange, the schedule cadence, the
public inbox's destination, and the feedback widget's on or off default. The
record carries none of these by name. A runner is responsible for naming them
when Phase 8 lands them.

I could not confirm: the sandbox processor credentials' path in the vault.
The pre-real-data plan section 3 names Run Payments' sandbox and the four
paths (`HH-691`, `HH-692`, `HH-768`, `HH-769`, `HH-770`); the credential
placement is "FB Vault at a demo path (his, never in chat); one sandbox
account or two (testers versus public)" (`HH-MASTER-PLAN-r1.md` decision H10
and B.10 item 1). The names of the values, not their values, are what the
deploy side records.

## Running the exit test

The image's own end to end test is `deploy/demo/exit-test.sh`, run from a
checkout of the branch: `./deploy/demo/exit-test.sh`. The script drives the
image against a local Postgres it starts when absent and asserts every clause
the image can prove alone: the image builds; on a clean database it answers
`/healthz` (no phrase asked); each app serves under its own prefix behind the
phrase gate; the gate refuses the mint without the phrase and still admits a
staff call with a valid demo bearer and no phrase; without the hash value
the image exits before serve; an interrupted build resumes and reaches
serve; a database that is not the demo's is refused before migrate; the
forwarded address discipline holds (a client-written `X-Forwarded-For` never
survives the image's Caddy, and rotating forged values buy the mint's limit
no bucket); and the phrase's hash sits on no file in the writable layer and
in no served process's environment. The phrase and its hash are generated
inside the script, held in shell variables, and never written to a file or
printed (`deploy/demo/README.md` "The exit test" and `deploy/demo/exit-test.sh`
header).

The exit test for the public copy is the master plan section A, run against
the live public copy and filed under `docs/sessions/` in infra. It includes
the parity coverage line, every role on the testers' copy, the three shells
on devices, real sign in through FB Console, the synthesized data denylist
scan (`deploy/demo/denylist-scan.sh`), the public protections (the scheduled
reset seen to run once, a probe from outside the tailnet for each
protection), the sandbox payments walk, the testers' source of truth, the
operations checks, and the records close (`HH-MASTER-PLAN-r1.md` section A
and section B.9). The pre-real-data plan item `HH-698` is the same test as a
Plane item, sized at about 4 dh.

The exit test's one known blocker is recorded in `docs/BLOCKERS.md`: two
distinct client addresses behind the proxy hold two separate limit buckets
only once the core's limit reads the forwarded chain. The script asserts the
two clauses that hold under either keying, so it does not flip when the
core's keying lands (`deploy/demo/exit-test.sh` header and
`deploy/demo/README.md` "The exit test" final paragraph).

## Incidents: stop, and who to tell

These three incidents are the ones the runbook names in advance. Every one
of them is a stop signal: the operator stops the affected action and tells
the named people, not the runner of the next step.

1. **A copy is down.** Stop: do not retry the boot by hand; the boot is the
   image's own, the classifier handles a resume (`deploy/demo/entrypoint.sh`
   step 2). Tell: the principal, in `principal/state/`, with the copy name,
   the build head, and the last green leg. Colton is told if the public copy
   is the one down, since the public copy is open to anyone
   (`HH-MASTER-PLAN-r1.md` section A6).
2. **Sign in fails on the testers' copy.** Stop: do not bypass the exchange
   or paste a partner credential into a partner's place; the gate refuses
   it and the audit row records who entered (`hh-s4/CP1-STATE.md` Step 4
   clause 3). Tell: the principal, with the partner email and the
   `X-Org: hh-demo` request id; if the partner is removed from the
   `org-hh-demo` Team the refusal is the expected answer and the partner
   needs restoring, not the copy.
3. **A tester sees another dealer's data.** Stop: take the affected copy
   offline through the FB Deploy control plane (do not edit the database);
   the resume path refuses a database holding a real user or a real payment
   configuration before migrate or the seed write a row, so a manual fix is
   not on (`deploy/demo/entrypoint.sh` lines 193 to 225). Tell: the principal
   and Colton immediately, with the tester name, the partner, and what the
   tester saw. The persona partners list and the `CORE_PERSONA_CALLER` value
   are the two settings to read first; the testers' copy should run with
   `CORE_PERSONA_CALLER=partner` and a non-empty `CORE_PERSONA_PARTNERS`, and
   a public visitor must never reach the testers' copy's database.

For every incident the postmortem's first read is the principal's record and
the decision log, not the local copy's logs.

## OPEN items the plan leaves open

These are the items the runbook could not pin to a value or a name in the
sources. They are listed so a runner can resolve them before the public copy
opens:

- the public host name (the plan names DNS for the public host as Colton's,
  section B.9, D4; the string is not yet on record).
- the schedule cadence for the public copy's `deploy/demo/reset-demo-db.sh`
  (the plan says "on a schedule" and the pre-real-data plan row 58 says
  "daily timer"; the master plan forbids clock times in any record).
- the outbound messaging sink's identity and the public inbox's destination
  (the plan names a sink and a public inbox but not their addresses; section
  A6).
- the write rate limit values other than the exchange's `60/min` per address
  and `10/min` per user (the plan says "rate limits on every write"; section
  A6).
- the `CORE_` and `HH_WEB_` values for the public copy beyond what the
  README and the testers' copy's deploy notes name (the plan names the
  public copy's protections but not its exact variable set; section A6,
  decision H3).
- the sandbox credentials' vault path (the plan names the vault and "one
  sandbox account or two" as Colton's; decision H10).