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

1. The sign in allow list is derived from the box's finance user list
   (`HH_FINANCE_USERS`), so the list is the one source of truth for the
   people on the box and the allow list is not a second one. A list and an
   allow list that disagree refuse the boot, naming the two variables and
   never an address.
2. The variable checks: every name below is required, and
   `HH_BOX_KIND`, `APP_ENV` and `CORE_SIGNIN_MODE` must carry the exact
   words `finance`, `staging` and `console`. One line per missing name,
   never a value.
3. `CORE_DATABASE_URL` is set from `DATABASE_URL`: one database in both
   spellings.
4. The session signing key: a mounted key readable by group or other is
   copied once to a private path under `/run/`; the core is pointed at the
   copy. The key's bytes are never printed.
5. `/srv/app/config.json` is written from the `HH_WEB_*` names (console
   mode), validated with the same rules as the browser's parser.
6. The database is classified, by the core itself (`core finance-box-state`,
   the one definition of the shape that the boot guard reads too, so the two
   can never answer "empty" differently). An EMPTY database (no migration
   bookkeeping, no table in any user schema, no large object at all and no
   user schema of its own): `core migrate`, then `core finance-seed`
   (applies the Dibbits seed, records `box_kind=finance` in `system_settings`
   through the settings module, and leaves the seed's users with no usable
   password). An UNSEEDED database (the box's own bookkeeping is there, the
   seed is not and nothing is marked, which is what a crash between migrate
   and seed leaves behind): `core migrate`, then `core finance-seed` again,
   whose own guards decide whether the seed may run, so an interrupted first
   boot heals at the next boot. The finance box's own world: `core
   finance-box-check` (refuses a database the box did not seed, before anything
   writes), then `core migrate`, forward only. Anything else is refused by the
   box kind check: a database that holds a table the box never migrated, or a
   large object somebody else left in it, is somebody else's world and the box
   never claims it. Every redeploy keeps the data; nothing on the boot path
   drops, truncates or reseeds.
   The wait for the database comes before this step, and the database URL
   never sits on a command line: `psql` reads the connection from a libpq
   service file on the shared memory tmpfs, which the probe alone reads and
   which is removed as soon as the probe is done.
7. The box's finance users are applied from the list:
   `core finance-user --apply-from-env` provisions every entry and revokes
   every finance person the list left out, in one transaction. The apply is
   convergent, so a boot with a changed list brings the box to the list and
   a boot with the same list changes nothing. The command prints ids and
   counts only, never an address.

## The database role's rights

`DATABASE_URL` names the box's own Postgres and the role the box connects as.
That role needs one right the boot does not assume: **CREATEROLE**.

- **Why.** Migration 0418 creates the assistant's assist roles: the reader
  roles, the two logins and the function users role. `CREATE ROLE` needs
  CREATEROLE, so a role without it can never finish a migrate. The migrate
  runner applies each file in its own transaction, so every migration before
  0418 commits and 0418 rolls back whole, and a fresh database is left half
  migrated: a bootstrap mark, a bookkeeping table, hundreds of migration rows
  and no seed.
- **What the box does about it.** On a fresh (empty) database the box checks
  the role before it writes anything, and refuses in words, naming the right
  and the migration that needs it: `the box's database role needs CREATEROLE:
  migration 0418 creates the assist roles`. Nothing is written on that path:
  no mark, no migration row, and the database still reads empty afterwards.
- **The check is in the empty branch only.** Only migrations that create
  roles need the right: 0418 and 0427 today. A database the box has already
  migrated holds those roles, so an existing box keeps booting with a role that
  has no CREATEROLE, and a finance marked database passes the box kind check
  with such a role. A later migration that creates a role would stop such a box
  at its next boot (the migrate rolls that file back whole, so no data is
  harmed, but the box stays down until the right is granted).
- **How to grant it.** `ALTER ROLE <the box's database role> CREATEROLE;`, run
  as a role that may alter roles. A superuser already has the right. Grant it
  before the first boot of a fresh database, and keep it, or grant it again
  before deploying an image that adds a migration that creates a role.

## The box's finance user list

`HH_FINANCE_USERS` carries the people on the box. The entries are separated
by `;`, each one `email|platform_user_id|display name|role`:

```
fin-controller-a@example.invalid|fb-console-user-a|Financial Controller|financial_controller;fin-payables-b@example.invalid|fb-console-user-b|Payables and Receivables|payables_receivables
```

The rules:

- the role is one of the finance roles (`financial_controller`,
  `payables_receivables`);
- a field cannot hold a `;` or a `|`, so a display name is a display name
  with spaces and no pipes;
- an entry that is wrong refuses the boot with one line naming the entry by
  its email alone, never the platform id, the display name or the role;
- the sign in allow list (`CORE_SIGNIN_EMAIL_ADDRESSES`) is derived from the
  list at boot. Setting it by hand is allowed only when it agrees with the
  list; two sources that disagree refuse the boot.

With no list in the environment there is nobody to apply at boot, and the
box's people are the ones an operator linked by hand:

```
core finance-user --email <address> --platform-user-id <id> --display-name <name> --role <role>
core finance-user --revoke --email <address> --platform-user-id <id>
```

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
- `CORE_SIGNIN_EMAIL_ADDRESSES` - the sign in allow list. It is derived from
  `HH_FINANCE_USERS` at boot; set it by hand only when it agrees with that
  list, and two sources that disagree refuse the boot.
- `HH_FINANCE_USERS` - the box's finance users (the list of people). See
  "The box's finance user list" below. Optional: with no list in the
  environment nobody is applied at boot, and the people on the box are the
  ones an operator linked by hand.
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
- `entrypoint-finance-users-test.sh` - the finance user list: the sign in
  allow list is derived from it, a list and an allow list that disagree
  refuse naming the variables and never a value, and the entrypoint does not
  ask for the allow list when the list derives it.
- `entrypoint-supervisor-test.sh` - the supervisor: stand-in children on
  the PATH record their invocations and exit; the entrypoint exits non
  zero as soon as any one of them exits, on both the empty database path
  (migrate then finance-seed) and the finance box's own world (box kind
  check then migrate).
- `entrypoint-boot-test.sh` - the boot's own legs: the database URL is
  parsed into a libpq service file and never sits on a command line, the
  ports written into the Caddy template are digits or the boot refuses, and
  the database's shape (empty, unseeded, the finance box's own world, and
  anything else) decides what the entrypoint runs.

Run each from a checkout:

```
sh deploy/finance/entrypoint-vars-test.sh
sh deploy/finance/entrypoint-finance-users-test.sh
sh deploy/finance/entrypoint-supervisor-test.sh
sh deploy/finance/entrypoint-boot-test.sh
```
