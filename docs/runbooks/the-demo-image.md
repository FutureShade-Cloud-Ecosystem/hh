# The demo image: how it is built and how it is checked

The demo image is the one image the platform runs for the box. It carries
the core built with `-tags demo`, the built files of every app the box
serves, Caddy in front, and the frozen migration set. The entrypoint
(`deploy/demo/entrypoint.sh`) is the one place the boot order lives.
This runbook is grounded in the files in `deploy/demo/` and in
`web/apps/_role-template/ROLE-GOES-LIVE.md` on the role template branch
(Lane 0 sub item 3): every command it quotes is one the exit test runs
or one the operator runs, and the runbook never reads or prints a
secret.

## The image at a glance

The image is multi stage:

- `core-build`, the Go stage: the core compiled with `-tags demo`, the
  flag that pulls in the demo issuer, the membership source, and every
  boot guard. The stage is pinned by digest in the FROM line of
  `deploy/demo/Dockerfile` (PR 287 lands the pin; the digest is not
  reproduced in this runbook). The stage copies the whole `core/`
  tree, downloads modules, then builds with
  `CGO_ENABLED=0 go build -trimpath -tags demo -o /out/core ./cmd/core`.
- `web-build`, the Node stage: the whole `web/` workspace copied
  before `npm ci` so the install resolves workspaces, then one
  `npx vite build` per app, each with the app's own `--base=/<prefix>/`
  flag and the staff apps carrying `--mode demo` so the front door
  handoff reader is compiled in. The result is `/out/<prefix>/` for
  every app the box serves.
- `caddy`, the static host stage: pulls the Caddy binary from
  `caddy:2.10.2`.
- The final stage: `alpine:3.20` with `ca-certificates` and
  `postgresql16-client` (the entrypoint's one database probe reads the
  database's shape, and nothing else in the image talks to the
  database), the built `core` binary, the Caddy binary, the
  `/out/<prefix>/` trees, the frozen migration set, the Caddy
  template, and the entrypoint. The working directory is
  `/srv/core` so the migrate role finds its migrations on the
  working directory path the role reads.

Each app is built once with its own `--base` flag, so each app's
`index.html` and its assets carry the app's prefix and no app source
changes for serving. A new role app joins by one build line, one
`COPY` line, and one handle block in the Caddyfile template: the
checklist at `web/apps/_role-template/ROLE-GOES-LIVE.md` (Lane 0
sub item 3) lists the steps in order and names the file a copy
removes from itself.

## The route plan, one origin behind the one phrase gate

`deploy/demo/Caddyfile.template` is the template the entrypoint
substitutes and pipes to Caddy on standard input; Caddy's autosave
home is pointed at the container's shared memory tmpfs so nothing
generated and no secret reaches the image's layers, a volume, or
anything that outlives the container. The handle blocks are ordered
most specific first, and every app sits behind the one gate:

- `/api/v1/demo/*`, the mint: basic auth on, reverse proxy to the
  core on a loopback port.
- `/api/*`, the rest of the API: reverse proxy only, the core's own
  law; a staff call with a valid demo bearer and no phrase still
  answers.
- `/healthz`, the platform's health leg: reverse proxy only, the
  gate does not ask for the phrase.
- `/till/*`, the counter till: basic auth on, `try_files {path}
  /till/index.html`, the history mode rewrite returns the till's
  own index and no other's.
- `/drive/*`, HH Drive: basic auth on, `try_files {path}
  /drive/index.html`.
- `/q/*`, the public desk, and `/quote.html`, where the server's
  emailed share link lands: basic auth on, the desk's own
  `try_files`; the rewrite for `/quote.html` keeps the share token
  in the one request the browser made.
- `/quote/*`, the Quote Desk: basic auth on; while
  `web/apps/quote-desk` is absent from the build context the
  directory is empty and the prefix answers 404, so the image
  needs no edit when the desk lands.
- The catch-all handle, the front door at `/`: basic auth on,
  `try_files {path} /index.html`.

A header above every response, gate open or shut (decision log row
981):

```
header X-Robots-Tag "noindex, nofollow"
```

The forward address discipline, on every reverse proxy leg (W2's
review, routed here): the leg REPLACES `X-Forwarded-For` with the
remote host Caddy itself saw, so a client written value never
survives the proxy and the chain the core reads is exactly one
element, the proxy's own observation. Behind another host edge
this collapses every visitor onto the edge's address by design:
one bucket shared at the edge is the conservative failure, and a
forged chain is never the answer.

## The boot order, and the mid boot question

`deploy/demo/entrypoint.sh` is the only place the boot order lives.
The order is the plan's G0 made executable, with the phrase gate's
value checked before anything else:

1. refuse to start without `DEMO_GATE_BCRYPT_HASH` and refuse a
   value that does not look like a bcrypt hash (the gate wants the
   hash, not the phrase; the refusal names the mistake without
   repeating the value);
2. wait, bounded, for the database to accept connections;
3. classify the database's shape: empty, foreign, building, or
   built;
4. on the ordinary paths, `core demo-check` (G0) before anything
   writes;
5. on a fresh database, write the image's own build marker, then
   `core migrate`, `core demoseed -apply`, `core demo-populate`,
   and `core demoseed -apply` again (the quote world is conditional
   on the two demo role logins the populate plants, and the seed
   is idempotent);
6. `core serve` on a loopback port, Caddy in front on the public
   port; the monitor loop tears the pair down together, so
   whichever side stops, the container exits and the platform
   retries the whole boot.

A boot that stops between migrate and demo populate leaves a
database demo check refuses. The answer is the image's own build
marker: on a fresh database the entrypoint creates
`demo_build_marker` (one row, outside the migration chain) before
migrate runs, so the image only ever resumes a build it started.
A schema without the marker is refused with the recovery named
before anything writes. A marked build whose populate row is
absent is the interrupted build, and before it resumes the
entrypoint runs the populate's own refusals as guarded probes:
no org other than the demo's, no user outside the migrations'
planted set, no payment configuration the populate did not write.
A database holding a real user or a real configuration is refused
before migrate or the seed write a row. The refusal everywhere
names the recovery: recreate the database resource; the demo
world builds from nothing on the fresh database.

## How a role app gets its prefix

`web/apps/_role-template/ROLE-GOES-LIVE.md` is the role goes live
checklist, the file a copy of the template removes from itself
once the copy is renamed. The checklist names the file paths and
the line numbers as they sit on main, in six steps:

1. start from the template, copy the folder, rename the workspace,
   element tag, class name, title, and ports; commit the
   `web/package-lock.json` so CI's `npm ci` finds the workspace;
2. add the app to the design system's dependent lock and the
   manifest, and let CI's web loop run typecheck and test on the
   copy;
3. add the role's account to the closed table in
   `core/internal/demo/flows/flows.go` (the `accounts` map, the
   `All()` list, the closed table comment, and `flows_test.go`);
4. add the account's constants and the three walk lists plus the
   two branch grant lists in
   `core/internal/demo/populate.go`; the ids, emails, and names
   must equal the flows row by hand;
5. move the card from coming to live in
   `web/apps/front-door/src/roles.ts`, widen the two unions,
   swap the role from `COMING_ROLES` to `LIVE_ROLES`, and add a
   tile whose `href` is the app's prefix;
6. add the app's prefix to the image: one build line after the
   public desk's in `deploy/demo/Dockerfile` with the app's own
   `--base=/<prefix>/` (and `--mode demo` for a staff app), one
   `mkdir` entry, one `COPY` line, and one `handle /<prefix>/*`
   block beside the till's in `deploy/demo/Caddyfile.template`
   with its own `try_files` fallback.

Step 6 is the runbook's concern: the prefix the till's handle
block sits behind, the copy the build stage writes, the COPY the
final stage carries, and the front door tile's `href` are the
same string. The exit test's page leg at the new prefix is the
proof, and step 6 also adds that leg beside the others in
`deploy/demo/exit-test.sh`.

## The exit test, leg by leg

`deploy/demo/exit-test.sh` drives the built image end to end
against a throwaway local Postgres and a docker network it owns.
Every name, port, and tag has an environment override and a
default, so a second party runs the script unmodified on its own
names. The script force removes only the containers it names with
its own prefix, never a substring match, so another lane's or a
reviewer's containers are never touched.

The legs, each one a clause the image asserts:

- **The image builds.** One `docker build -f deploy/demo/Dockerfile
  -t <tag> .` against the worktree root, quiet. The script reports
  `the image builds` on success.
- **On a clean database the box answers `/healthz` and the health
  leg asks for no phrase.** A fresh database, the full boot, then
  `curl /healthz` returns 200 without the phrase. The platform's
  health probe is the one leg the gate does not ask for.
- **The phrase is asked on the pages and on the mint.** `curl /`
  without the phrase returns 401; `curl -u "demo:$PHRASE" /` returns
  the app's first page (its marker in the body); `curl -u
  "demo:$PHRASE" /api/v1/demo/flows` returns the closed table's
  two flows; `POST /api/v1/demo/session` without the phrase returns
  401, with the phrase returns a token; `GET /api/v1/quotes` with
  a valid demo bearer and no phrase returns 200 (the gate is the
  pages and the mint only, every other api route is the core's
  own law).
- **Each app serves under its own prefix.** `page_has` walks `/`,
  `/till/`, `/drive/`, `/q/`, and `/quote.html?t=x`, each with the
  app's marker in the body, behind the phrase. `page_gated` walks
  `/till/`, `/drive/`, `/q/`, and `/quote/`, each returning 401
  without the phrase. The till's script asset resolves under
  `/till/assets/...`; HH Drive's resolves under
  `/drive/assets/...`; a till asset fetched under the till's
  prefix returns 200, the proof that the Vite base flag reached
  the build.
- **The never indexed header rides every response.** For `/`,
  `/healthz`, and `/till/`, with and without the phrase, the
  response carries `X-Robots-Tag: noindex, nofollow`. The header
  rides the refusal, a served page, an asset less prefix, and the
  unauthenticated health leg alike.
- **`/api/v1/payments/config` answers `simulated: true` (G7).**
  The payments leg the demo world ships.
- **The boot wrote the image's build marker.** One row in
  `demo_build_marker`. The marker is the proof this image started
  the build, and is the same marker the resume path reads.
- **The phrase's hash sits on no file in the writable layer, the
  scan bites on a planted value, and no served process carries
  the variable.** The scan searches the hash's value as a fixed
  string, plus the bcrypt shape over `/srv` with the migration
  set's own fixture rows pruned. A planted file in `/tmp` is
  found and removed, and the layer is clean again. Caddy's
  autosave home (`/root/.config`) is absent, so the gate's
  configuration went to the shared memory tmpfs and not the
  writable layer. The hash variable is unset before the core
  starts, so no core or Caddy process inherits it.
- **Without the hash value the image exits before serve and
  before anything writes.** The refusal names the missing hash
  value, the boot does not reach `listening`, and the database
  holds no user tables.
- **An interrupted build this image started resumes.** A database
  the script marked (the image's own migrate plus the build
  marker) boots a new container, the resume path runs the
  populate's own refusals as guarded probes, the boot's output
  names itself ("resuming the build this image started"), and the
  box reaches serve.
- **A schema without this image's marker is refused, both
  shapes.** Shape one: a migrated database holding a non demo
  org. Shape two: another application's database (its own
  migration record and its own tables, no marker, no org table
  at all). The refusal names the recovery in both shapes, and
  the migration record and the table count are unchanged.
- **A marked build holding a real user or a real payment
  configuration is refused by the resume refusals before migrate
  or the seed write.** A real user (one active row at a real
  address), and a real payment configuration (one row at a real
  merchant id). The refusal names the recovery, and the
  migration record and the user count are unchanged.
- **The forward address discipline, three legs.** The template
  the image carries holds the three overwrite lines (the source
  pin). The generated configuration holds them too (the run
  pin). One capture behind a Caddy built from the image's own
  template, with a client that sends a forged `X-Forwarded-For`:
  the capture shows the proxy replaced the header with the
  address it saw itself, and the forged value did not survive.
  One mint leg, one client address, one session: spend the
  allowance with honest mints, then rotate forged forwarded
  values from the same address; the limit is bounded and the
  forged values buy no bucket.

The script's own containers, by exact name (`<prefix>-happy`,
`<prefix>-resume`, `<prefix>-cap`, `<prefix>-capcli`), are removed
at the end. The throwaway databases it created are dropped on the
script's Postgres. The Postgres and the network stay for the next
run.

## Build the image and run the exit test on a workstation

The commands a workstation runs, with its own tag and ports. Pick
a tag no other lane uses (the script's default is `hh-demo-till:
w5-exit`, here overridden to `hh-docs-demo:local`) and a pair of
free host ports. Every name the script owns is prefixed with the
workstation's chosen short tag so another lane's or a reviewer's
containers are never touched.

The Postgres the exit test needs, one container of your own,
image `postgres:16-alpine`, published on `127.0.0.1` only, on
the port the script's `W5_EXIT_PG_PORT` names (the default is
`5599`):

```
docker run -d --name hh-docs-demo-pg \
  --publish 127.0.0.1:5599:5432 \
  -e POSTGRES_PASSWORD=hardscape \
  postgres:16-alpine
```

The build, from the worktree root, with your own tag:

```
docker build -f deploy/demo/Dockerfile -t hh-docs-demo:local .
```

The exit test, with your own tag, ports, and container prefix.
The script generates the phrase and its hash internally and never
writes either to a file or prints it:

```
W5_EXIT_IMAGE=hh-docs-demo:local \
W5_EXIT_PG=hh-docs-demo-pg \
W5_EXIT_PG_PORT=5599 \
W5_EXIT_APP_PORT=5812 \
W5_EXIT_PREFIX=hhdocs \
  ./deploy/demo/exit-test.sh
```

The script reports `done: 0 failure(s)` on a clean pass. Read
the lines, they name the leg and the verdict.

## Remove what the run put on the box

The script removed the four containers it named (`<prefix>-happy`,
`<prefix>-resume`, `<prefix>-cap`, `<prefix>-capcli`) and dropped
the seven throwaway databases it created. The Postgres and the
network are still on the box, and the image is in the local image
store. Remove them by name, exactly the names this run created.
Never prune.

The image, by tag:

```
docker rmi hh-docs-demo:local
```

The Postgres container, by id. Record its id and remove it with
`-v` so its volume goes too:

```
docker rm -f -v <pg-container-id>
```

The docker network, by name, only if the script created it (the
script reuses an existing network with the same name when one
exists):

```
docker network rm hh-docs-demo-net
```

That is the whole cycle: one Postgres, one image, one network,
one exit test, then removal by name. A workstation running the
exit test repeatedly is the workstation that owns the names; a
second party runs the same script on its own names and the two
runs do not collide.

## Sources

- `deploy/demo/Dockerfile` on `origin/main`: the multi stage
  build, the Go stage pinned by digest (PR 287), each app's own
  `--base=/<prefix>/` build line, the final stage layout.
- `deploy/demo/Caddyfile.template` on `origin/main`: the
  `X-Robots-Tag` header, the per app handle blocks, the
  forward address overwrite on every reverse proxy leg.
- `deploy/demo/entrypoint.sh` on `origin/main`: the boot order,
  the phrase gate refusal, the database shape classification, the
  resume path's own refusals, the build marker written on a fresh
  database.
- `deploy/demo/exit-test.sh` on `origin/main`: the legs above,
  each one a clause the image asserts.
- `web/apps/_role-template/ROLE-GOES-LIVE.md` on the role
  template branch (Lane 0 sub item 3): the six steps a role takes
  from a coming card to a working app on the demo box, and the
  file paths and line numbers the role's copy of the template
  changes.
