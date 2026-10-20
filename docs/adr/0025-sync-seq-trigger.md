# ADR-0025: the sync seq trigger

Status: Proposed

Drafted in the sync freshness cycle, item SF-1, against the plan SYNC-FRESHNESS-PLAN-r3 (section 3) and the
principal's rulings on its Opus review. Migration 0422.

## Context

Every device feed pulls `seq IS NOT NULL AND seq > cursor`, under an org or tenant equality fence. Migration 0213
added `seq` to the served tables nullable, with no default and no trigger. So a row is only visible to a device
when the writer that created or changed it remembered to draw a seq from `scope_sequences` by hand, and about
sixty write sites did not all remember: a bounced payment, a deposited payment, a credit memo, a removed card, an
edited quote and more never reached a device that had synced past them. The sweep that found them read the
sites one by one; the next new writer, loader or job would repeat the class.

## Decision

The database draws the seq when the writer did not.

1. `sync_seq_draw()` is a `BEFORE INSERT OR UPDATE ... FOR EACH ROW` trigger function, one per served table, with
   the table's stream as its trigger argument.
   - On INSERT, when `NEW.seq IS NULL`, it draws.
   - On UPDATE, when `NEW.seq IS NOT DISTINCT FROM OLD.seq AND NEW IS DISTINCT FROM OLD`, it draws. A tombstone
     (`deleted_at` set) is such an update, so devices learn deletes.
   - The draw is the statement every adapter already runs: `INSERT INTO scope_sequences (scope) VALUES (stream) ON
     CONFLICT (scope) DO UPDATE SET next_value = next_value + 1 RETURNING next_value`.
2. **It defers to every explicit draw.** A writer that sets `seq` itself (every adapter and module write path that
   draws today) changes `seq`, so the trigger does nothing and no existing path changes value. A true no op
   update (`NEW = OLD`) draws nothing.
3. **One stream per table**, the stream the table's live writers already draw on. `internal/sync`'s catalogue test
   holds the table to stream map, and checks the live trigger against it.
4. **The counter's row lock is held to commit**, so seq order stays commit order per stream, the property a
   device's cursor needs. This is also the cost: see the lock order rule.
5. **Floors.** Each stream's counter is raised to the greatest seq any of its tables holds
   (`next_value = greatest(next_value, max(seq))`; `next_value` is the last value handed out, so there is no
   `+ 1`), so no new draw lands at or below a seq a device already consumed.
6. **One time re-draw.** Rows changed earlier without a draw are invisible to devices that synced past them. The
   migration re-draws every row of every trigger table, live and tombstoned, as one set based draw per stream:
   advance the counter once by the row count, then `seq = base + row_number()` in the present seq order. It is
   written as an explicit draw, never a touch: `UPDATE t SET seq = seq` is a no op the trigger's own condition
   skips. The new seqs sit above every old seq, so the trigger sees an explicit draw and does not draw again.

## Consequences

- **An upsert draws twice.** `INSERT ... ON CONFLICT DO UPDATE` fires the BEFORE INSERT trigger for the proposed
  row and then the BEFORE UPDATE trigger on the conflict arm. The row ends on the second draw; the first is a gap.
  Gaps are allowed by 0213 and a device only ever needs `seq > cursor`.
- **`INSERT ... ON CONFLICT DO NOTHING` burns a draw per proposed row.** The BEFORE INSERT trigger runs before
  the conflict is known, so a re-run of an idempotent seed (the demo world's applier) advances each counter
  by the rows it proposes. The rows keep their seq, so no device re-pulls them; only the counter gaps.
  The demo world's driver run leg asserts the rows' own seq, not the counter.
- **Updates to columns a feed does not serve now re-serve the row.** Harmless; one extra row on the next pull.
- **A BEFORE UPDATE row trigger changes the row lock strength.** Postgres locks the row `FOR UPDATE`, not `FOR NO
  KEY UPDATE`, whenever the UPDATE's SET list names a key column (a column of a unique index), even when the
  value does not change. `FOR UPDATE` conflicts with the `FOR KEY SHARE` a referencing insert takes. So an UPDATE
  of a trigger table must not list a key column unless the key really changes; the products and customers
  loaders split the key into its own statement for this reason. The catalogue test refuses a new statement that
  does it.
- **A json, xml or geometric column on a trigger table makes every UPDATE of it fail** (no equality operator for
  the row comparison). The catalogue test refuses such a column.
- **Lock order.** The draw takes the stream's counter row until commit. A transaction that takes two counters in
  one order and another that takes them in the other can deadlock. The rule, written in
  `core/internal/payments/LOCK-ORDER.md`: a table written inside a money transaction gets its counter's place in
  that order before it gets a trigger; a writer that updates a row ahead of a counter it needs takes that counter
  first, without advancing it (`LockPaymentsSequenceTx` for payments, `lockStopsSeqTx` for dispatch's stops).
- **Hidden behaviour.** A comment at `syncSeqCTE` (`dispatch/syncscope.go`) and this record say so.

## Left out, and why

- `stock_balances`: a trigger there deadlocks a sale (stock rows, then the payments counter) against a restocking
  return (the counter, then the restock). SF-7 adds the table together with its place in the lock order.
- Every `msg_*` table: their `seq` is an IDENTITY column (0385), a different counter on the same column. Its own
  gap (identity draws are not commit ordered) is a later item.
- `will_call_pickups`: its stream, `will_call`, waits for the UX-72 change to reach main; a one line follow up
  adds it.
- `tasks`: two scopes serve it on two streams chosen by task type (`pick_tasks`, `weights`), which one trigger
  argument cannot express. To be ruled.
- `dashboard_user_widgets`: the plan names the stream `widget_measures`, the demo repair map counts it under the
  table's own name. To be ruled; SF-13 owns the table.

## What the trigger does not fix

A missing tenancy stamp (`org_id`, `tenant_id`), a hard delete, a change to a joined or derived column, and a
change to who the fence admits are not a missing seq. Each module item (SF-3 onward) fixes its own, and the
catalogue test lists the hard deletes still pending.
