# @hardscapehouse/work-offline

The Work shell's offline layer for the desk apps (plan B.3 and B.4). It is the one place the desk apps get three things:

1. **A read cache.** `openWorkStore` opens the offline store for one app with the scopes and the write fence of its manifest block. `WorkReader` answers the catalogue (with the price band joined), customers, open documents (quotes), parked tickets and invoices from the store, each with the scope's staleness in words (`stalenessWords`). An app whose manifest mode is `none` (Money Desk, Dealer Admin) cannot open a store: `WorkModeError`. An app whose mode is `read_cache_only` (the Counter Till, Manager Home) gets a scope book that admits no write at all, so it has no outbox to queue on.
2. **A quote draft outbox.** `QuoteDrafts` keeps a quote started with no signal and sends it when the signal returns (last write wins by field, the quotes scope's policy). It queues only what the core's quotes store admits through sync: a create of `id`, `customer_id`, `branch_id`, `valid_until`; an update of `valid_until`; a delete of a draft. `test/contract.test.mjs` reads those sets from `core/internal/quote/syncscope.go`, and `src/testing.ts` gives a strict transport that refuses what the core refuses, so a payload cannot drift from the server again. Lines, terms, status and every money leg are the server's and are reached online.
3. **The no signal guard.** `noSignalFetch` wraps the fetch an app's client is built on: with no signal a write verb is refused before anything dials, with one shared state (`NO_SIGNAL_ERROR`, status 0, the contract's error envelope). A read is untouched. `onlineOnly` is the same rule for a single action. Nothing in the guard stores anything: money never queues. The sync transport is built on a separate, unguarded client.

`WorkSync` drives the sync statechart over the store (connectivity in, a cycle that pushes then pulls, the banner mode for the shell chrome). `cardWords` puts a conflict card in plain words.

## Tests

Node 22.13 or later. `npm test` runs the guard, the Go set contract, the read cache, the quote drafts and the sync driver. The suites that need `node:sqlite` are named `*.node-test.mjs` so the repository's `bun test` gate does not discover them.
