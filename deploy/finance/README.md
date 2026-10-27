# The HH finance box image

The deploy artifact for the finance test box (the deploy spec
`FINANCE-BOX-SPEC-r1`). One container, one database, one replica. The demo
image (`deploy/demo/`) is the pattern; the differences are the point: the
default build of the core (no demo tag, no demo commands, no credential
bypass), the console sign in (no phrase gate), and the finance seed in
place of the demo world build.

## Build

From the repository root:

```
docker build -f deploy/finance/Dockerfile -t hh-finance-box .
```

## What runs

One container runs three processes under `entrypoint.sh`:

1. `core serve` on the loopback port `CORE_INTERNAL_PORT` (default 9090);
2. `core worker` (the outbox dispatchers, the GL posting consumer, the
   reconciler and the jobs runner). The jobs runner is a single instance
   with no leader election: run ONE replica of this container, never more;
3. Caddy on `PORT` (the platform's routed port), serving the front door,
   the Counter Till (`/till/`), the Money Desk (`/money-desk/`) and
   Manager Home (`/home/`), and proxying `/api/*`,
   `/.well-known/jwks.json` and `/healthz` to the core. No basic auth gate:
   the box's own FB Console sign in guards every page and every API call.
   TLS is the platform's; Caddy runs `auto_https off`.

If any one of the three exits, the entrypoint exits non zero and the
platform restarts the container. The health check is `GET /healthz`
through Caddy (it pings the database, 503 when the database is down).

## The boot order

1. The variable checks: every name below is required, and
   `HH_BOX_KIND`, `APP_ENV` and `CORE_SIGNIN_MODE` must carry the exact
   words `finance`, `staging` and `console`. One line per missing name,
   never a value.
2. `CORE_DATABASE_URL` is set from `DATABASE_URL`: one database in both
   spellings.
3. The session signing key: a mounted key readable by group or other is
   copied once to a private path under `/run/`; the core is pointed at the
   copy. The key's bytes are never printed.
4. `/srv/app/config.json` is written from the `HH_WEB_*` names (console
   mode), validated with the same rules as the browser's parser.
5. The database is classified. An EMPTY database: `core migrate`, then
   `core finance-seed` (applies the Dibbits seed, records
   `box_kind=finance` in `system_settings` through the settings module,
   and leaves the seed's users with no usable password). A NON EMPTY
   database: `core finance-box-check` (refuses a database the box did not
   seed, before anything writes), then `core migrate`, forward only. Every
   redeploy keeps the data; nothing on the boot path drops, truncates or
   reseeds.

## Environment variables (names only)

Every value arrives from FB Vault through FB Deploy; nothing is written
here or in the repository.

Required at boot (the entrypoint refuses without each; the refusal names
the variable):

- `DATABASE_URL` - the box's own Postgres (the one database name; the
  entrypoint sets `CORE_DATABASE_URL` from it)
- `APP_ENV` - must be `staging`
- `HH_BOX_KIND` - must be `finance`
- `CORE_SIGNIN_MODE` - must be `console`
- `CORE_TOKEN_ISSUER` - the box's own token issuer
- `CORE_STAFF_AUDIENCE`, `CORE_TENANT_AUDIENCE` - the staff and tenant
  audience names (must differ; the core refuses equal values)
- `CORE_SESSION_SIGNING_KEY_FILE` - path of the mounted session signing
  key file
- `CORE_APPWRITE_ENDPOINT`, `CORE_APPWRITE_PROJECT`,
  `CORE_APPWRITE_API_KEY` - FB Console, for the sign in exchange and team
  membership
- `CORE_SIGNIN_EMAIL_ADDRESSES` - the sign in allow list
- `PAYMENT_VAULT_KEY` - 64 hex characters (32 bytes)
- `HH_ACCEPTANCE_SIGN_KEY_ID`, `HH_ACCEPTANCE_SIGN_KEY` - the customer
  quote acceptance signing pair (key id and hex key; the spawn refuses
  without both)
- `PORTAL_BASE_URL`, `ERP_BASE_URL` - https origins, required outside dev
- `PORT` - the public port Caddy listens on
- `HH_WEB_MODE` - `console`
- `HH_WEB_ORG` - the org slug the browser sends
- `HH_WEB_CONSOLE_ENDPOINT`, `HH_WEB_CONSOLE_PROJECT_ID` - FB Console for
  the browser
- `HH_WEB_DEALER_NAME` - the dealer name on screen: `Dibbits` (the box
  runs today's Ontario seed identity; every customer, document and person
  in its world stays synthetic)

Optional:

- `CORE_INTERNAL_PORT` - the core's loopback port (default 9090)
- `CORE_TRUSTED_PROXIES` - the platform's proxy addresses, so the core
  reads the client address right
- `CORE_EXCHANGE_TOKEN_TTL`, `CORE_EXCHANGE_JWT_BOUND` - the exchange
  token window and the console JWT bound (sane defaults when unset)
- `FINANCE_ROOT` - the image layout root (default `/srv`; exists so the
  entrypoint can run from a checkout)

Never set on this box: `DEMO_MODE`, `DEMO_GATE_BCRYPT_HASH`,
`ALLOW_SEED`, `CORE_LOAD_DOORS`, `CORE_JWKS_URL`, `HOS_CARD_PROCESSOR`,
`HOS_PAYMENT_GATEWAY` (both payment rails stay on the stub; the boot
guard refuses a live rail on a finance box), `CORE_SIGNIN_EMAIL_DOMAINS`.

## The tests beside this file

- `entrypoint-vars-test.sh` - the variable checks refuse, one line naming
  the missing or wrong name, before any child process or database call.
- `entrypoint-supervisor-test.sh` - the supervisor: stand-in children on
  the PATH record their invocations and exit; the entrypoint exits non
  zero as soon as any one of them exits, on both the empty database path
  (migrate then finance-seed) and the non empty one (box kind check then
  migrate).

Run each from a checkout:

```
sh deploy/finance/entrypoint-vars-test.sh
sh deploy/finance/entrypoint-supervisor-test.sh
```
