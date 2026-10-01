# The demo image (the Counter Till cycle, item W5)

One image in the Railway shape (plan r7 section 5): the core built with
`-tags demo`, the Counter Till app's built files, Caddy in front, and the
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
writes; `core migrate`; `core demoseed -apply`; `core demo-populate`; then
`core serve` on a loopback port with Caddy on the public port. Both
database variables are derived from the one the operator sets: the serve
role reads `CORE_DATABASE_URL` and the one-shots read `DATABASE_URL`, and
the entrypoint sets the second from the first so the two cannot drift.

A boot that stops between migrate and demo-populate leaves a database
demo-check refuses (the guard reads a migrated database without the
populate's marker as a world never built here). The answer is the
entrypoint's classifier: a database holding the migration set, no marker
row, and nothing but the demo's own org anchor (or none) is an interrupted
build of the demo's own world, so the next boot resumes it: migrate
completes what it had not applied, the seed writes only its missing rows,
the populate completes, and demo-check runs before serve with the world
whole. Every write stays forward only and idempotent, so a resume adds
nothing twice. Any other shape (a schema that is not the demo's) is
refused before anything runs, and the refusal names the recovery:
recreate the database resource; the demo world builds from nothing on the
fresh database.

## The phrase gate (G6)

The shared phrase lives in the vault and nowhere else. Only its hash is
set on the copy, as the runtime value `DEMO_GATE_BCRYPT_HASH`, made by a
command that takes the phrase from the vault and prints only the hash
(Caddy wants bcrypt; `caddy hash-password` makes one). Without that value
the entrypoint refuses to start, before anything runs, and a value that
does not look like a bcrypt hash is refused with a message that names the
mistake (the phrase pasted where the hash belongs) without repeating the
value.

The hash flows to Caddy alone: the entrypoint substitutes it into the
gate's configuration and pipes the configuration to Caddy on standard
input, so no generated configuration and no hash ever sits on a filesystem
inside the container; the variable is unset before the core starts, so no
core process carries it; and no log line prints it. The gate asks for the
phrase on pages and on `/api/v1/demo/*` only: every other `/api/v1/*`
route is the core's own law (staff routes answer 401 without a bearer, the
public legs answer as on any box), which is why a staff call with a valid
demo bearer and no phrase still answers.

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
