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
- `src/outbox.ts`: the durable outbox, with `settle`, a person's way out of a stall. `src/cards.ts`:
  conflict cards.
  `src/store.ts`: pull, reads with staleness, the whole store.
- `src/runner.ts`: drives the pure machine in `statecharts/sync/machine.ts` over the store.
- `src/transport-api.ts`: the api-client adapter. `src/testing.ts`: node:sqlite executor,
  the fake sync server (the Go engine's order of decisions), Web Locks and worker doubles.

## Invariants

1. Replay is in enqueue order, across scopes (runs of one scope batch together). Within a scope
   the order always holds. Across scopes it holds until a refusal parks a scope (invariant 8):
   the other scopes carry on, and the parked scope's rows go out later, in their own order,
   after rows enqueued behind them in other scopes.
2. An operation's key is generated once, stored, and sent unchanged on every attempt.
3. Money never queues: a money scope is refused at enqueue and never stored, and replay
   checks the fence again on every stored row.
4. A pushed operation's status change and its card land in one atomic unit.
5. No outbox row is deleted; no card is deleted (dismissed only).
6. A fault is whole: no row is closed or lost, and the retry sends the same rows again, where the
   server dedups on the keys. The rows stay pending, marked `sent` with the attempt counted
   (invariant 8), because a request that did not come back cannot be told from one the server applied.
7. A parked (auth expired) outbox sends nothing and loses nothing.
8. A refusal closes a row only when the server cannot hold it. The refusals are the core's own:
   400, 403, 501 (404 and 413 come from a hop in front of the core and are faults, rows stay
   pending, the replay stops and retries whole). Of the core's 400s only one is about a body:
   `invalid_json`, matched exactly on the message. A 400 on a batch with that message is split to
   single operations; any other 400 (the core's `unknown sync scope` and `scope ... is pull-only`,
   which refuse every row of the scope, or a hop's own 400 with any body, JSON or not) parks the
   scope as `scope_refused`, exactly like a 403 or 501, with no split and no card.
   A single `invalid_json` 400 closes its row as rejected, with a card, only if no earlier request
   for it could have reached the server. Every row carries `sent`, set durably, in the same atomic step as the
   attempt count, before its first request leaves; a refusal or a 401 (the server applied
   nothing) puts the mark back. A row that was sent before, including after an answer that was
   lost, is uncertain: a refusal never closes it. It stays pending and the scope shows a
   `check_applied` stall. A 403, a 501 or any other 400 parks the scope: its rows stay pending (nothing is lost, no
   card), a `scope_refused` stall shows, the other scopes and the pull carry on, and every replay
   tries the scope again. A push is also capped by bytes, under the core's 1 MiB.
9. A row that cannot be trusted (hash mismatch, unreadable payload, a fenced scope) is closed
   as rejected with a card, never sent and never closed silently.
10. A failed request is `unreachable`, apart from the platform's `offline`: a sync request or
    a reconnect leaves it. A push fault still lets the pull run.
11. A pull carries on past a failing scope and reports each failure; only the network or the
    session going stops it.
12. Every OPFS worker call settles: a dead or silent worker rejects the call, releases the
    lock and turns ownership to `read_cache_only`. Calls reach the worker one at a time, and a
    call's timer starts when it is posted to an idle worker, so it never counts queue time.
13. A stuck outbox is visible. `store.outbox.health()` returns `waiting` (pending writes),
    `stuck` (the most attempts any pending write has had and that write's last error, or null)
    and `stalls` (kind, scope, status, the operation to check, last error, writes waiting in the
    scope, and words for the person). It is read from the rows, so it survives a restart. The
    runner still rests in `idle` after a standing fault; the shell reads `health()` when the
    runner settles and shows the signal. The stall is a record on the row, not a card: a
    `check_applied` stall tells the person the operation may already have been applied and must
    be checked before it is entered again. `health().stuck` has no threshold of its own: one dropped
    response or one offline attempt already makes it non null, with `attempts: 1`. The shell must
    not alarm on attempts of one; it picks its own level, and shows a stall at once.
14. A stall has a way out, and it is a person's. `store.settle(key, choice)` (`outbox.settle` on the
    outbox) takes the key of a stalled row (`check_applied` or `scope_refused`) and a choice:
    `applied` (the person checked and the server holds it: the row closes as `applied`, with a review
    card of policy `settled_by_person`, and is never sent again), `withdrawn` (the row closes as
    `rejected`, with a card, and is never sent), or `resend` (the sent mark and the stall are cleared,
    so the next replay sends the row as a new attempt with the same key, and a refusal then judges it as
    a row never sent; choose it only after checking the server does not hold it). Each choice is
    recorded on the row (`settled`, `settled_at`) in the same atomic unit as its card. A row that is not
    stalled is refused (`not_stalled`, `unknown_key`, `invalid_choice`) and nothing changes. It sends
    nothing, waits for a replay in flight, and releases the rows behind the stall: the next replay
    sends them in order. The stall UI belongs to item 7; the shell calls `settle` from it, then asks
    the runner for a cycle.

## Whose outbox it is

An outbox belongs to the person who queued the work. This slice does not know who that is: a
device that signs a second person in over a first person's queue would replay the first person's
operations with the second person's grant. A scope the new grant lacks is refused 403, which now
parks (keeps) the first person's work rather than closing it, but the real rule, an outbox keyed
to the person and never replayed under another sign in, belongs to item 7 (identity). Until it
lands, the shell must not sign a second person in over a non-empty outbox.

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
- A request with no status (offline, a dropped response) cannot be told from one that never
  left, so it counts as sent. A device that retries offline many times therefore holds
  uncertain rows, and a later 400 stalls them as `check_applied` instead of closing them. That
  is the safe side.
- A standing 500 or 503 on every push rests the runner in `idle`; the stuck signal is in
  `outbox.health()`, and the shell must show it. The runner has no retry timer.
- A parked scope's work can go out after later work in other scopes (invariant 1).
- `resend` gives up the protection of the sent mark for that row: if the server holds it and a later
  `invalid_json` 400 arrives, the row closes as rejected although the server applied it. The person
  chooses `applied` for a row the server holds; `resend` is for one the person checked it does not.
- `replay`'s `sent` total counts operations in requests the server answered; refused requests
  are not counted.

## Tests

Node 22.13 or later (type stripping and `node:sqlite`). `npm test` runs the package suites,
the statechart table test and the generated path model tests (`HH_MODEL_PATHS` and
`HH_MODEL_STEPS` deepen the model run). The suites that need `node:sqlite` are named
`*.node-test.mjs` so the repository's `bun test` drift gate (Bun has no `node:sqlite`) does
not discover them; this package's `npm test` runs them, and the web CI loop runs that.
`probe/` holds the browser legs (`*.pw.js`, Playwright, Chromium, not in CI):
`PROBE_PORT=<free port in 56200 to 56299> npx playwright test` from `probe/`.
