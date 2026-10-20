# Demo copies: the three FB Deploy applications of the public demo

Operator runbook for the three demo applications of the Gable Landscape Supply
publish (master plan section B.9, Phase 8). Each copy is a separate FB Deploy
application running the same demo image with a different set of variables and
its own database. Today every copy runs behind the gate phrase; the plan
target is to drop the phrase on the testers' and public copies (section A6,
D3) once an image change lands.

This runbook covers the three copies, how each is built and deployed, the
console sign in variables by name, the persona partners list, what a reset
looks like today, the outbound messaging sink and rate limits as the plan
describes them, the exit test, and a short incident section. Every fact
cites the file it came from on `origin/main` at `b73d385c`; anything the plan
leaves open is marked OPEN, never invented.

Sources, in the order read at `origin/main` (`b73d385c`, the head after PR
502 merged):

- `principal/state/HH-MASTER-PLAN-r1.md` (master plan, sections A, B.9, B.10, D1 to D5, decisions H3 and H10)
- `principal/state/HH-DIRECTION-ON-RECORD.md` (the demo host strings, LOG 483, LOG 485, line 297)
- `hh-s4/CP1-STATE.md` (sign in and deploy state at `51f1dd79`)
- `hh-s4/PRE-REAL-DATA-PLAN.md` (Phase 8 items `HH-697` and `HH-698`, `HH-691` to `HH-770`)
- `deploy/demo/README.md` (the demo image, the boot order, the operator variables)
- `deploy/demo/entrypoint.sh` on `origin/main` (the boot order, the gate hash, the signing key copy, the resume refusals)
- `deploy/demo/exit-test.sh` (the throwaway local end to end proof)
- `deploy/demo/Dockerfile` (the image's `HEALTHCHECK` on `/healthz`)
- `deploy/demo/Caddyfile.template` (the gate's `basic_auth demo __GATE_HASH__` lines)
- `deploy/demo/write-web-config.sh` (the web runtime config writer)
- `core/internal/identity/sessionsigner.go` (the signing key file mode check)
- `core/internal/demo/persona/handler.go` (the audit row constants on the persona entry)
- `core/cmd/core/demoenv_inventory.go` (the allow list of `CORE_` names)

## The three copies, and who uses each

The master plan section D3 names three FB Deploy applications and three
databases. Promotion order is staging first (master plan section D3, citing
row 483 of the decision log; the host strings below are on
`principal/state/HH-DIRECTION-ON-RECORD.md` line 297, LOG 483). The three
copies are:

1. **Staging** at `hh-demo-staging.futurebuild.ai`. Colton and the principal.
   Runs with `CORE_SIGNIN_MODE=demo` today (the gate phrase is required on
   every copy; the "no phrase" target for console copies is plan section A6
   and a plan target, not yet on `origin/main`). Used for the sandbox probe
   (`hh-s4/PRE-REAL-DATA-PLAN.md` section 3, `HH-691`) and for the internal
   walk and device testing (`hh-s4/CP1-STATE.md` section 2).
2. **Testers'** at `hh-demo.futurebuild.ai`. The product team. Real sign in
   through FB Console, with the gate phrase still required on `origin/main`
   today. The Phase 1 exit test (CP-1) lands here, and the testers run their
   rounds on this copy (`HH-MASTER-PLAN-r1.md` section D5).
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
Colton's (`HH-MASTER-PLAN-r1.md` section B.9, D4); the host string is not
yet on record.

## How a copy is built and deployed on FB Deploy

### The image

Each copy is one FB Deploy application running the demo image built from
`deploy/demo/Dockerfile`. The image holds the core built with `-tags demo`,
the built files of every web app (front door at `/`, till at `/till/`,
buying-and-stock at `/buying-and-stock/`, public desk at `/q/`, HH Drive at
`/drive/`, Manager Home at `/home/`, Yard Pick at `/yard/`, Money Desk at
`/money-desk/`, the dispatch board at `/dispatch/`, House Messages at
`/messages/`, Dealer Admin at `/dealer-admin/`, Quote Desk at `/quote/`
when present, and the three Pro apps under `/pro-procurement/`, `/pro-pay/`
and `/pro-field/`), Caddy in front, and the frozen migration set
(`deploy/demo/README.md` opening section and the `mkdir -p /out/...` line in
`deploy/demo/Dockerfile`). The build command is `docker build -f
deploy/demo/Dockerfile -t <tag> .` from the repository root.

### What the operator sets

Nothing is baked into the image and nothing is committed; every value reaches
the box at runtime (`deploy/demo/README.md` sections "What the operator sets"
and "The console sign in mode"). The values below are the names only, never
the values.

The baseline runtime values (every copy):

| Variable | Kind |
|---|---|
| `DATABASE_URL` | the one database; the entrypoint derives `CORE_DATABASE_URL` from it (`deploy/demo/entrypoint.sh`) |
| `PAYMENT_VAULT_KEY` | the vault key the invented payment credentials are sealed under; an operator value, so restarts unseal (`deploy/demo/README.md`) |
| `HH_ACCEPTANCE_SIGN_KEY_ID`, `HH_ACCEPTANCE_SIGN_KEY` | the customer quote acceptance signing pair (`deploy/demo/README.md`) |
| `DEMO_GATE_BCRYPT_HASH` | the gate's bcrypt hash, from the vault, runtime only; required on every copy today (see the gate section below) |
| `PORT` | the public port, default `8080` (`deploy/demo/entrypoint.sh`; `deploy/demo/Dockerfile` `EXPOSE 8080`) |
| `CORE_INTERNAL_PORT` | the core's loopback port, default `9090` (`deploy/demo/entrypoint.sh`; the core is reached on `http://127.0.0.1:${CORE_INTERNAL_PORT}` only) |
| `CORE_STAFF_AUDIENCE`, `CORE_TENANT_AUDIENCE` | the token audiences (defaults `hh-staff` and `hh-tenant`) |
| `CORE_SIGNIN_MODE` | required, no default; `demo` for staging, `console` for the testers' and public copies |
| `HH_DEMO_MINT_VISITOR_ALLOWANCE` | optional, the per-visitor mint allowance on the testers' copy; staging and public deployments leave this unset (`deploy/demo/README.md`) |
| `HH_DEMO_MINT_ADDRESS_CEILING` | optional, the mint allowance of one network address, shared by every visitor behind it; staging and public deployments leave this unset (`deploy/demo/README.md`) |
| `CORE_DEMO_TOKEN_TTL` | optional, the demo session length the mint issues |

The console sign in variables (`CORE_` and `HH_WEB_` names) are listed in the
sign in section below.

### The signing key file, and its copy

The console copies need a persistent ES256 (P-256) session signing key pair.
`CORE_SESSION_SIGNING_KEY_FILE` names the path to the PEM private key inside
the container. FB Deploy's file storage mounts files as `0755` `root:root`
and its API refuses `chmod` and `chown` (HTTP 422); the core refuses a
session signing key readable by group or other (`principal/DECISION-LOG-SESSION-2.md`
row 2354, the principal's probe of FB Deploy's file storage, and
`core/internal/identity/sessionsigner.go` on `origin/main`).

The entrypoint on `origin/main` reconciles the two through
`ensure_signing_key`, console mode only. The check on the mounted key's
group or other bits is `key_mode & 0177 == 0` (no group or other bits): a
mounted key whose mode satisfies it is used as-is; any mounted key that
fails it (readable by group or other) is copied once with `umask 077` and
then `chmod 0400` into `/run/hh-secrets/session.pem`, the directory is
created at `0700`, and `CORE_SESSION_SIGNING_KEY_FILE` is repointed at the
copy before any core process starts. The mounted file is untouched. A copy
that fails refuses the boot
(`deploy/demo/entrypoint.sh` on `origin/main`, `ensure_signing_key`).

`CORE_SESSION_KEY_NEXT_FILE` and `CORE_SESSION_KEY_PREVIOUS_FILE` are the
optional staged and retired PEM public halves, any mode, one PEM block each
(`deploy/demo/README.md`).

### The health check

Two probes hit `/healthz` from two places. The image's `HEALTHCHECK` runs
inside the image, targets the public port (Caddy), and uses the public
port's default: `CMD wget -q -O /dev/null
"http://127.0.0.1:${PORT:-8080}/healthz" || exit 1`, with `--interval=10s
--timeout=3s --start-period=90s --retries=12` and `EXPOSE 8080`
(`deploy/demo/Dockerfile`). The entrypoint's own wait loop targets the
core's loopback port (`CORE_INTERNAL_PORT`, default `9090`), bypassing
Caddy: `until wget -q -O /dev/null
"http://127.0.0.1:${CORE_INTERNAL_PORT}/healthz"` (`deploy/demo/entrypoint.sh`
on `origin/main`).

Caddy serves `/healthz` in its own handle with no `basic_auth` line, so on
the public port `/healthz` answers without the phrase
(`deploy/demo/Caddyfile.template`, the `handle /healthz` block).

I could not confirm: how FB Deploy wires the image's `HEALTHCHECK` to the
copy's platform health check. The plan section A9 names "a health check"
per copy but no source file in this tree names the platform wiring.

### The phrase gate on every copy

`DEMO_GATE_BCRYPT_HASH` is required on every copy today. The entrypoint
reads it, unsets the variable, refuses to start if it is unset, and refuses
a value that does not look like a bcrypt hash; the check has no branch on
`CORE_SIGNIN_MODE` (`deploy/demo/entrypoint.sh` on `origin/main`, the
`HASH_VALUE` block). Caddy in front of the core gates pages and the demo
mint routes with `basic_auth demo __GATE_HASH__`
(`deploy/demo/Caddyfile.template`).

The plan target is "no phrase" on the testers' and public copies
(`HH-MASTER-PLAN-r1.md` section A6, D3). That target needs an image change
(the gate path and the Caddy template both have to skip the basic auth on
console copies); until it lands, every copy needs the hash and the phrase.
This is OPEN: the image change is not on `origin/main` and the gate's
`Caddyfile.template` carries `basic_auth demo __GATE_HASH__` unconditionally.

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
outbound a console copy makes) and the guard allows at least the `CORE_`
names on its allow list, refusing any other `CORE_` name at boot, naming the
variable and never its value (`deploy/demo/README.md` and
`core/cmd/core/demoenv_inventory.go`, the `demoEnvAllowCoreNames` table).
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
| `CORE_SESSION_SIGNING_KEY_FILE` | secret file (a path) | the PEM private key (ES256, P-256); the entrypoint copies a too-open mount into `/run/hh-secrets/session.pem` at `0400` before the core starts |
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
`CORE_PERSONA_PARTNERS`, writes an audit row whose constants are
`auditAction = "AUTH_PERSONA_ENTRY"`, `auditEntityType = "auth.persona"`,
`auditPurpose = "PERSONA_ENTRY"` (the row's narrative is CP1-STATE's
paraphrase, `"partner P entered as persona R"`), and mints a core token for
`persona:<id>`. On the public copy it is open and limited per address and
per address and persona (`deploy/demo/README.md` "The console sign in mode",
`hh-s4/CP1-STATE.md` section 1, `core/internal/demo/persona/handler.go` on
`origin/main`).

The persona partners list is the value of `CORE_PERSONA_PARTNERS`: the FB
Console user ids of the named partners who may enter as a persona on the
testers' copy, each one stamped onto one of the demo populate's unlinked
`ACTIVE` staff rows in `users` (a step the principal runs on the testers'
copy's database; see `hh-s4/CP1-STATE.md` Step 3). The entry is refused at
boot if the copy is `open`.

### The `HH_WEB_` names (the web side)

Set in FB Deploy and read by `deploy/demo/write-web-config.sh`, which writes
`/srv/app/config.json` at container boot from the names below. The
entrypoint refuses to start, naming the variable and never its value, if one
is wrong (`deploy/demo/README.md` "What the operator sets"):

| Name | Kind | What it is |
|---|---|---|
| `HH_WEB_MODE` | required for console and when any `HH_WEB_*` is set | the web runtime config the box serves at `/config.json`: `demo` or `console`; unset or `demo` keeps the file the image baked (`{"mode":"demo"}`); `console` writes it at boot |
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

## The assistant on a demo copy: a real model behind caps

A demo copy runs TanyAI on Claude Sonnet 5.5 (the `tanyai-premium` alias, the native
Anthropic adapter) when, and only when, every step below is done. With any of them
missing the copy still boots where it can and answers on the deterministic stub, and the
answer says so (the `model_unavailable` notice). The boot guard is
`core/cmd/core/demoenv_inventory.go`; the guards are in `core/cmd/core/demo_on.go`.

What the guard admits: exactly two `HOS_ASSIST_` names, `HOS_ASSIST_ANTHROPIC_API_KEY` and
`HOS_ASSIST_DEALER_CAP_MICROCENTS`. Every other `HOS_ASSIST_` name (the gateway URL and
key, the dealer tag) still refuses the boot: a demo reaches the vendor directly through
the one alias and no gateway. The route is fixed by the build to `tanyai-premium`; no
setting and no request moves it.

Do these in this order, once per demo copy:

1. **Set the provider spend limit** on the Anthropic workspace that owns the key. It is
   the outer bound; Colton sets it before the key is placed.
2. **Place the key and the cap.** The key is the vault prod `/hh-demo` value
   `HOS_ASSIST_ANTHROPIC_API_KEY`, put into FB Deploy's environment for the copy from the
   vault; no agent reads or prints it. `HOS_ASSIST_DEALER_CAP_MICROCENTS` is the box's hard
   spend bound in whole micro cents (a positive integer; one cent is 1,000,000 micro cents).
   A key with no cap, a zero, a negative or a non numeric cap stops the boot with a message
   naming the cap variable.
3. **Insert the price row** for the model. The table is append only and deliberately not
   seeded; the boot refuses a keyed copy whose model has no row, with a message naming
   `assist_model_prices`, the alias and the model. Insert the vendor's published prices for
   `claude-sonnet-5-5`, in integer micro cents per million tokens, from the vendor's current
   published pricing (a later price change is a new row with a higher
   `effective_seq`; never an update):

   ```sql
   INSERT INTO assist_model_prices
     (alias, model, price_in_micro_per_m, price_out_micro_per_m, price_cached_micro_per_m, effective_seq)
   VALUES ('tanyai-premium', 'claude-sonnet-5-5', <input>, <output>, <cached input>, 1);
   ```

   Check a row exists (and which one is in force, the highest `effective_seq`):

   ```sql
   SELECT alias, model, price_in_micro_per_m, price_out_micro_per_m, price_cached_micro_per_m, effective_seq
     FROM assist_model_prices
    WHERE alias = 'tanyai-premium' AND model = 'claude-sonnet-5-5'
    ORDER BY effective_seq DESC LIMIT 1;
   ```

   No rows means the copy will refuse to boot with the key set.
4. **Restart the copy** and read the boot log: it either serves, or names the one step
   still missing.

The demo's per visitor, per address and box wide request limits are always on for a demo
copy, and the boot refuses a keyed copy whose wired assistant lacks them. When the dealer
cap is reached the copy answers on the stub with the `model_unavailable` notice until the
month turns or an operator raises the cap.

## Resetting a copy

The plan's reset story is in two pieces, and only one of them is built today.

**What is built today.** The entrypoint on `origin/main` runs the build order
on every boot: bounded wait for the database, classify its shape
(`empty`, `built`, `building`, `foreign`), run `core demo-check` (the G0
guard), write the build marker on a fresh database, then `core migrate`,
`core demoseed -apply`, `core demo-populate`, and `core demoseed -apply`
again (`deploy/demo/entrypoint.sh` on `origin/main`). On a built database the
seed and the populate write nothing. A `foreign` database (a schema this
image did not start) is refused before anything writes, with the recovery
named as "recreate the database resource". A `building` database (the
marker present, the populate's row absent) is the resume path; the
`resume_refusals` function runs the populate's own refusals as guarded
probes (no org other than the demo's, no user outside the migrations'
planted set, no payment configuration the populate did not write), and a
real user or a real configuration refuses before migrate or the seed write a
row (`deploy/demo/entrypoint.sh` on `origin/main`, the `resume_refusals`
function). The plan's section A6 names "the reset seen to run once" as one
of the public copy's acceptance checks; the plan's section A9 names "a
reset runbook" as one of the operations checks per copy.

**What is not built today.** The script `deploy/demo/reset-demo-db.sh` does
not exist on `origin/main` (`git ls-tree origin/main deploy/demo` lists no
such file; `git grep reset-demo-db origin/main` finds nothing; no PR in the
checked refs carries it). The pre-real-data plan row 58 names it as something
to wire, and no holder is recorded. So the only proven reset today is
recreating the database resource (the entrypoint rebuilds the world on the
fresh database on its next boot), and what the reset wipes and what it keeps
are not on record because the script is not.

OPEN: the reset script's exact flags, the schedule cadence (the plan says
"on a schedule and on demand" but does not name a clock time, and the master
plan forbids clock times in any record), the staging copy's reset cadence
(not named in the sources), and whether the reset will be called from a
scheduled job or a platform command when it lands. Colton rules the cadence.

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

The sandbox credentials' vault path is named in the master plan section B.10
item 1: Production, `/hh-demo/run`. What the plan leaves open is whether
testers' and public copies share one sandbox account or have two
(`HH-MASTER-PLAN-r1.md` decision H10). The pre-real-data plan section 3
names four paths and the five Plane items that build them (`HH-691`,
`HH-692`, `HH-768`, `HH-769`, `HH-770`) and the credential placement: an
FB Vault path under the demo, never carried in chat. The names of the
values, not their values, are what the deploy side records.

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
scan (`deploy/demo/denylist-scan.sh`), the public protections ("the reset
seen to run once" per section A6, a probe from outside the tailnet for each
protection), the sandbox payments walk, the testers' source of truth, the
operations checks (each copy with "a health check, a database backup, a reset
runbook" per section A9), and the records close (`HH-MASTER-PLAN-r1.md`
section A and section B.9). The pre-real-data plan item `HH-698` is the same
test as a Plane item, sized at about 4 dh.

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
   on `origin/main`, step 2). Tell: the principal, in `principal/state/`,
   with the copy name, the build head, and the last green leg. Colton is
   told if the public copy is the one down, since the public copy is open
   to anyone (`HH-MASTER-PLAN-r1.md` section A6).
2. **Sign in fails on the testers' copy.** Stop: do not bypass the exchange
   or paste a partner credential into a partner's place; the gate refuses
   it and the audit row records who entered (`hh-s4/CP1-STATE.md` Step 4
   clause 3, and the persona handler's `auditAction`/`auditEntityType`
   constants on `origin/main`). Tell: the principal, with the partner
   email and the `X-Org: hh-demo` request id; if the partner is removed
   from the `org-hh-demo` Team the refusal is the expected answer and the
   partner needs restoring, not the copy.
3. **A tester sees another dealer's data.** Stop: take the affected copy
   offline through the FB Deploy control plane (do not edit the database);
   the entrypoint's `resume_refusals` function refuses a database holding a
   real user or a real payment configuration before migrate or the seed
   write a row, so a manual fix is not on (`deploy/demo/entrypoint.sh` on
   `origin/main`, `resume_refusals`). Tell: the principal and Colton
   immediately, with the tester name, the partner, and what the tester saw.
   The persona partners list and the `CORE_PERSONA_CALLER` value are the
   two settings to read first; the testers' copy should run with
   `CORE_PERSONA_CALLER=partner` and a non-empty `CORE_PERSONA_PARTNERS`,
   and a public visitor must never reach the testers' copy's database.

For every incident the postmortem's first read is the principal's record and
the decision log, not the local copy's logs.

## OPEN items the plan leaves open

These are the items the runbook could not pin to a value or a name in the
sources. They are listed so a runner can resolve them before the public copy
opens:

- the public host name (the plan names DNS for the public host as Colton's,
  section B.9, D4; the string is not yet on record).
- the reset cadence for the public copy (the plan says "on a schedule and on
  demand"; the master plan forbids clock times in any record). The reset
  script itself is not on `origin/main` today.
- the staging copy's reset cadence (not named in the sources).
- the "no phrase on console copies" image change (the plan wants it per
  section A6, D3; the entrypoint and `Caddyfile.template` on `origin/main`
  still require the phrase on every copy).
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
- whether testers' and public copies share one Run Payments sandbox account
  or have two (decision H10; the vault path is named as Production
  `/hh-demo/run` in section B.10 item 1).
- how the image's `HEALTHCHECK` is wired to the copy's platform health
  check (no source file in this tree names the platform wiring).