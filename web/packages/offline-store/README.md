# @hardscapehouse/offline-store

One offline-store package, two backends: SQLite in the Tauri shells, wa-sqlite
on OPFS in the PWA (IndexedDB fallback for read-cache-only frontends). Local
schema, outbox, sync_state and attachments_queue follow context section 4.5;
the sync protocol is pull `GET /sync/{scope}?since={seq}` plus an ordered outbox
replay, with per-entity conflict policy from the manifest. The `sync`
statechart in `statecharts/sync/` is its acceptance gate.

## Layout

- `src/sql.ts`: the `SqlExecutor` seam (`run`, `all`, `atomic`). Every write is one
  statement or one atomic list; no transaction is held open across calls, so a pooled
  Tauri SQL plugin connection cannot break it.
- `src/schema.ts`: the one schema (`ent_<scope>` per declared scope, `outbox`,
  `sync_state`, `cards`, `attachments_queue`).
- `src/backend-sqlite.ts`: takes an injected executor (the shell plugs the Tauri SQL
  plugin in). `src/backend-opfs.ts` plus `src/opfs-worker.ts` and
  `src/wasqlite-executor.ts`: wa-sqlite on AccessHandlePoolVFS in a worker, one owner
  tab (a Web Lock); a second tab is `read_cache_only` and the backend reports it.
- `src/scopes.ts`: the profile's scopes, the money fence, copies of the Go registry and
  the manifest offline blocks (pinned by `test/scopes.test.mjs`).
- `src/outbox.ts`: the durable outbox. `src/cards.ts`: conflict cards.
  `src/store.ts`: pull, reads with staleness, the whole store.
- `src/runner.ts`: drives the pure machine in `statecharts/sync/machine.ts` over the store.
- `src/transport-api.ts`: the api-client adapter. `src/testing.ts`: node:sqlite executor,
  the fake sync server (the Go engine's order of decisions), Web Locks and worker doubles.

## Invariants

1. Replay is in enqueue order, across scopes (runs of one scope batch together).
2. An operation's key is generated once, stored, and sent unchanged on every attempt.
3. Money never queues: a money scope is refused at enqueue and never stored, and replay
   checks the fence again on every stored row.
4. A pushed operation's status change and its card land in one atomic unit.
5. No outbox row is deleted; no card is deleted (dismissed only).
6. A fault is whole: nothing is marked, and the retry sends the same rows again.
7. A parked (auth expired) outbox sends nothing and loses nothing.
8. A standing refusal of a request (400, 403, 404, 413, 501) is a verdict, never a fault to
   retry: the batch is split to single operations, and one still refused is closed as
   rejected with a card. A push is also capped by bytes, under the core's 1 MiB.
9. A row that cannot be trusted (hash mismatch, unreadable payload, a fenced scope) is closed
   as rejected with a card, never sent and never closed silently.
10. A failed request is `unreachable`, apart from the platform's `offline`: a sync request or
    a reconnect leaves it. A push fault still lets the pull run.
11. A pull carries on past a failing scope and reports each failure; only the network or the
    session going stops it.
12. Every OPFS worker call settles: a dead or silent worker rejects the call, releases the
    lock and turns ownership to `read_cache_only`.

## Secure context

`crypto.subtle` (the body hash) and `crypto.randomUUID` (idempotency keys) exist only in a
secure context: https, `localhost`, or a Tauri webview. A page served over plain http from
another host has neither, so every enqueue there is refused as `invalid` and nothing is
queued. Serve the PWA over https; the shells are already fine.

## Known limits

- A tab that did not get the OPFS lock stays `read_cache_only` until it reloads; nothing
  waits on the lock to take over when the owner closes.
- The pull cursor never moves backwards. A server restore that resets its sequence would
  leave a device stuck; recovering needs a server epoch, which is an engine change.
- A lost response after a per field merge costs the merge report (the engine answers the
  retry with a plain applied).

## Tests

Node 22.13 or later (type stripping and `node:sqlite`). `npm test` runs the package suites,
the statechart table test and the generated path model tests (`HH_MODEL_PATHS` and
`HH_MODEL_STEPS` deepen the model run). The suites that need `node:sqlite` are named
`*.node-test.mjs` so the repository's `bun test` drift gate (Bun has no `node:sqlite`) does
not discover them; this package's `npm test` runs them, and the web CI loop runs that.
`probe/` holds the browser legs (`*.pw.js`, Playwright, Chromium, not in CI):
`PROBE_PORT=<free port in 56200 to 56299> npx playwright test` from `probe/`.
