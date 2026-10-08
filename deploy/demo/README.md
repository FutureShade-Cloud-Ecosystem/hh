# The demo image (the Counter Till cycle, item W5)

One image in the Railway shape (plan r7 section 5): the core built with
`-tags demo`, the built files of every web app (front door at `/`, till at
`/till/`, public desk at `/q/`, Quote Desk at `/quote/` when present), Caddy in front, and the
frozen migration set. Build from the repository root:

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
it answers `/healthz` (no phrase asked), the flows listing with the two
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

