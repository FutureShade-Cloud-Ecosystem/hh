# The demo image (the Counter Till cycle, item W5)

One image in the Railway shape (plan r7 section 5): the core built with
`-tags demo`, the built files of every web app (front door at `/`, till at
`/till/`, buying-and-stock at `/buying-and-stock/`, public desk at `/q/`, HH
Drive at `/drive/`, Manager Home at `/home/`, Yard Pick at `/yard/`, Money
Desk at `/money-desk/`, Dealer Admin at `/dealer-admin/`, Quote Desk at `/quote/` when present), Caddy in
front, and the frozen migration set. Build from the repository root:

    docker build -f deploy/demo/Dockerfile -t hh-demo-till .

and run it with the values the platform sets (all runtime values, nothing
baked):

    docker run \
      -e DATABASE_URL='postgres://...' \
      -e PAYMENT_VAULT_KEY='<64 hex chars>' \
      -e HH_ACCEPTANCE_SIGN_KEY_ID=demo-accept-1 \
      -e HH_ACCEPTANCE_SIGN_KEY='<64 hex chars>' \
      -e DEMO_GATE_BCRYPT_HASH='$2a$...' \
      -p 8080:8080 \
      hh-demo-till

## The boot order, and the mid-boot answer

The entrypoint runs, in order: a bounded wait for the database; the
database shape's classification; `core demo-check` (G0) before anything
writes; `core migrate`; `core demoseed -apply`; `core demo-populate`;
`core demoseed -apply` again, because the quote world's owners are the two
demo role logins the populate plants and the seed is idempotent, so the
second pass writes the quotes and nothing else; then
`core serve` on a loopback port with Caddy on the public port. Both
database variables are derived from the one the operator sets: the serve
role reads `CORE_DATABASE_URL` and the one-shots read `DATABASE_URL`, and
the entrypoint sets the second from the first so the two cannot drift.

A boot that stops between migrate and demo-populate leaves a database
demo-check refuses (the guard reads a migrated database without the
populate's marker as a world never built here). The answer is the image's
own build marker: on a fresh database (no migration record and no user
table at all) the entrypoint writes `demo_build_marker`, one row recording
the build started, before migrate runs. The image therefore only ever
resumes a build it started: a schema without the marker is refused before
anything writes, whatever it holds, because every database of the source
product below the org migration has that shape and none of them is the
demo's. A marked build whose populate row is absent is the interrupted
build, and before it resumes, the entrypoint runs the populate's own
refusals as guarded probes (no org other than the demo's, no user outside
the migrations' planted set, no payment configuration the populate did not
write): a database holding a real user or a real configuration is refused
before migrate or the seed write a row. What passes resumes: migrate
completes what it had not applied, the seed writes only its missing rows,
the populate completes, and demo-check runs before serve with the world
whole. Every write stays forward only and idempotent, so a resume adds
nothing twice. The refusal everywhere names the recovery: recreate the
database resource; the demo world builds from nothing on the fresh
database.

## The phrase gate (G6)

The shared phrase lives in the vault and nowhere else. Only its hash is
set on the copy, as the runtime value `DEMO_GATE_BCRYPT_HASH`, made by a
command that takes the phrase from the vault and prints only the hash
(Caddy wants bcrypt; `caddy hash-password` makes one). Without that value
the entrypoint refuses to start, before anything runs, and a value that
does not look like a bcrypt hash is refused with a message that names the
mistake (the phrase pasted where the hash belongs) without repeating the
value.

The hash flows to Caddy alone, and what "alone" means exactly: the
entrypoint substitutes it into the gate's configuration and pipes the
configuration to Caddy on standard input, and the entrypoint's own copy of
the variable is unset before the core starts, so no core process inherits
it and no log line prints it. What remains on the box is the hash by
design: Caddy's autosave of the adapted configuration sits in the
container's shared-memory tmpfs (root only, gone with the container), and
the kernel keeps the starting environment of the container's first process
for its life, which is where the operator's runtime value arrived. The box
is permitted to hold the hash; it must never hold the phrase, and no file
in the image's layers, a volume, or the writable layer ever holds either.
The gate asks for the phrase on pages and on `/api/v1/demo/*` only: every
other `/api/v1/*` route is the core's own law (staff routes answer 401
without a bearer, the public legs answer as on any box), which is why a
staff call with a valid demo bearer and no phrase still answers.

The same proxy legs carry the forwarded address discipline (W2's review,
routed here): Caddy replaces `X-Forwarded-For` with the address it saw
itself on every proxied call, so a client-written value never survives the
proxy and cannot buy the mint's per-address limit a separate bucket. Behind
another host edge the discipline collapses every visitor onto the edge's
address by design: one shared bucket is the conservative failure, and a
forged chain is never the answer.

## What the operator sets

| Variable | What it is |
|---|---|
| `DATABASE_URL` | the one database; the entrypoint derives `CORE_DATABASE_URL` from it |
| `DEMO_GATE_BCRYPT_HASH` | the shared phrase's bcrypt hash, from the vault, runtime only |
| `PAYMENT_VAULT_KEY` | the vault key the invented payment credentials are sealed under; an operator value, so restarts unseal |
| `HH_ACCEPTANCE_SIGN_KEY_ID`, `HH_ACCEPTANCE_SIGN_KEY` | the customer quote acceptance signing pair |
| `PORT` | the public port (the default is 8080) |
| `CORE_INTERNAL_PORT` | the core's loopback port (the default is 9090) |
| `CORE_STAFF_AUDIENCE`, `CORE_TENANT_AUDIENCE` | the token audiences (defaults `hh-staff` and `hh-tenant`) |
| `CORE_DEMO_TOKEN_TTL` | the demo session length the mint issues, optional |
| `HH_DEMO_MINT_VISITOR_ALLOWANCE` | the per-visitor mint allowance on the testers' copy, optional; unset or non-positive keeps the default of 5, and the value is capped at 20. Staging and public deployments leave this unset. |
| `CORE_SIGNIN_MODE` | `demo` or `console`, required, no default (the image refuses to start without it) |
| `CORE_SESSION_SIGNING_KEY_FILE` | console mode: path of the PEM EC P-256 private key. The secret mount must be mode 0400 or 0600 and owned by the user the core runs as (a file readable by group or other is refused at boot, and so is one the core's user cannot read); mount it read only |
| `CORE_SESSION_KEY_NEXT_FILE`, `CORE_SESSION_KEY_PREVIOUS_FILE` | console mode, optional: PEM public halves only, any mode, one PEM block each |
| `CORE_TOKEN_ISSUER`, `CORE_APPWRITE_ENDPOINT`, `CORE_APPWRITE_PROJECT`, `CORE_APPWRITE_API_KEY` | console mode: the deployment's issuer (never `hh-demo`) and the FB Console values |
| `CORE_EXCHANGE_TOKEN_TTL` | console mode, optional: the exchange token window, one second to one hour |

## The console sign in mode (the testers' copy and the public copy)

A copy that signs real people in through FB Console runs this image with
`CORE_SIGNIN_MODE=console`. Staging keeps `CORE_SIGNIN_MODE=demo` until it is
retired; the mode has no default, so every copy names it, and a console copy
does not serve the old demo mint routes (they answer 404). The console mode
is a declared shape of the demo environment guard: it carries a live
connection to FB Console (the one outbound a console copy makes) and the
guard allows exactly the names listed here under `CORE_`, refusing any other
`CORE_` name at boot, naming the variable and never its value.

Every value below is set in FB Deploy for the copy. Nothing is baked into the
image and nothing is committed.

| Name | Kind | What it is |
|---|---|---|
| `CORE_SIGNIN_MODE` | public value | `console` |
| `CORE_TOKEN_ISSUER` | public value | one value per deployment, never `hh-demo`; a token minted on one copy never verifies on another |
| `CORE_SESSION_SIGNING_KEY_FILE` | **secret file** (a path) | the PEM private key (ES256, P-256); mount it read only with mode 0400 or 0600, readable by the core's user, never an environment value; boot refuses a group or other readable file |
| `CORE_SESSION_KEY_NEXT_FILE` | public half (a path) | optional; the PEM public key staged for a rotation |
| `CORE_SESSION_KEY_PREVIOUS_FILE` | public half (a path) | optional; the retired key's PEM public key while its tokens live |
| `CORE_APPWRITE_ENDPOINT` | public value | the FB Console API endpoint, ending in `/v1` |
| `CORE_APPWRITE_PROJECT` | public value | the HH project id in FB Console |
| `CORE_APPWRITE_API_KEY` | **secret** | the core's server key: users read, teams read, files read and files write only (it cannot mint a JWT for anyone) |
| `CORE_EXCHANGE_TOKEN_TTL` | public value | optional Go duration, at most 1 hour (the default is 1 hour) |
| `CORE_EXCHANGE_JWT_BOUND` | public value | optional Go duration, at least 900 seconds, default 20 minutes; the furthest a FB Console JWT's `exp` may lie ahead |
| `CORE_SIGNIN_EMAIL_DOMAINS` | public value | comma list; with `CORE_SIGNIN_EMAIL_ADDRESSES` the allow list of sign in emails; both empty or unset refuses every sign in |
| `CORE_SIGNIN_EMAIL_ADDRESSES` | public value | comma list of whole addresses |
| `CORE_TRUSTED_PROXIES` | public value | comma list of peer addresses allowed to name the client: set it to the address Caddy connects from (`127.0.0.1` in this image), or every visitor shares the proxy's one rate limit bucket |
| `CORE_JWKS_URL` | unset | the console copy verifies its own tokens; if it is still set the boot logs one warning and ignores it |

The rest of the table above (database, vault key, gate hash, acceptance
signing pair) is unchanged. The JWK Set at `/.well-known/jwks.json` is served
through Caddy with no phrase, ahead of the front door's catch all; proven by
`deploy/demo/caddy-jwks-test.sh`. The environment guard's list is
`demoEnvAllowCoreNames` in `core/cmd/core/demoenv_inventory.go`: a new
`CORE_` name is added there, by name, in the same change that reads it.

A console copy needs the core to be the only replica per deployment (a
replica knows only its own key configuration), the signing key rotated by the
staged order in the sign in state note, and FB Deploy's secret file mode
confirmed before the copy ships.

## One replica

The copy runs as one replica and no more. The demo issuer's signing key is
born in one process and never leaves it, so a second replica would mint
tokens the first refuses and sign testers out on every balancing decision,
and both replicas would run the populate against one database. One
replica is also the plan's G5: a restart signs every tester out, and there
is no second box sharing the work.

## The exit test

`deploy/demo/exit-test.sh` drives the built image end to end against a
throwaway local Postgres (container `hh-till-l3d-pg` on `127.0.0.1:5599`,
started by the script when absent): the image builds; on a clean database
it answers `/healthz` (no phrase asked), the flows listing with the eight
flows, the app's first page (refused without the phrase, served with it),
and `/api/v1/payments/config` with `simulated: true`; the gate refuses the
mint without the phrase and still admits a staff call with a valid demo
bearer and no phrase; without the hash value the image exits before serve
and before anything writes; an interrupted build resumes and reaches
serve; a database that is not the demo's is refused before migrate, the
recovery named and the migration record untouched; the forwarded address
discipline is proven at the traffic level (one capture behind the image's
Caddy: a client-written value never survives the proxy) and at the mint
(rotating forged values buy the limit no bucket); and the phrase's hash
sits on no file in the writable layer and in no served process's
environment. The phrase and its hash are generated inside the script,
held in shell variables, and never written to a file or printed.

One clause of the mint's limit proof waits on the core, not the image: two
distinct client addresses behind the proxy hold two separate buckets only
once the core's limit reads the forwarded chain (it keys on the
connection's peer alone on this branch). That is recorded in
`docs/BLOCKERS.md`; the script asserts the two clauses that hold under
either keying, so it does not flip when the core's keying lands.

