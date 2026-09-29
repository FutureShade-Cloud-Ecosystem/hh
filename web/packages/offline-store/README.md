# @hardscapehouse/offline-store

One offline-store package, two backends: SQLite in the Tauri shells, wa-sqlite
on OPFS in the PWA (IndexedDB fallback for read-cache-only frontends). Local
schema, outbox, sync_state and attachments_queue follow context section 4.5;
the sync protocol is pull `GET /sync/{scope}?since={seq}` plus an ordered outbox
replay, with per-entity conflict policy from the manifest. The `sync`
statechart in `statecharts/` is its acceptance gate.
