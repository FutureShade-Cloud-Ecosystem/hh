-- 0187 — WHAT WAS ACTUALLY PULLED, line by line. Owner: internal/tasks. Session A, item 1.
--
-- THE SENTENCE THIS ANSWERS. Scrubname0005: "your picking and load team… understand what needs to get picked
-- if it hasn't already been picked". A pick queue can say what SHOULD be pulled because the order
-- says so. Until this migration nothing in the system could say what WAS pulled — only, through
-- task_shortages (0095), what could not be found.
--
-- THOSE ARE NOT THE SAME FACT, AND THE DIFFERENCE IS WHY THIS TABLE EXISTS.
--
--   task_shortages answers "what is missing, and who has been told" — an EXCEPTION, append-only,
--   with a resolution somebody at the counter owns. It is a message.
--
--   task_pick_lines answers "what came off the shelf" — the NORMAL case, one row per line of the
--   ticket, rewritten as the picker corrects themselves mid-pick. It is a measurement.
--
-- A short pick recorded only as a shortage tells the counter a number is missing but never tells
-- anybody what the yard DID load onto the truck, which is the number the driver hands over and the
-- number the customer counts. That gap is what this closes: after a short pick, the ticket says
-- "7 of 10 pulled" rather than "3 short" and leaving the rest to arithmetic nobody performed.
--
-- ── WHICH ONE IS AUTHORITATIVE (the derivation rule) ────────────────────────────────────────────
--
-- With both models present, "this line is short" becomes derivable two ways, and two derivations of
-- one fact is the failure dispatch-logic.ts:431-434 names about pickup state: "a third place for the
-- same truth to live, and the one that nobody remembers to write is the one that goes stale".
--
-- THE RULE, RECORDED HERE BECAUSE THE SCHEMA CANNOT ENFORCE IT: qty_picked IS THE MEASUREMENT AND
-- task_shortages IS THE MESSAGE. Shortness is DERIVED from qty_picked < qty_ordered. A
-- task_shortages row is not the definition of short; it is the record that somebody deliberately
-- told the counter about it, with a note and a resolution. A line may be short with no shortage row
-- (the picker recorded 7 of 10 and said nothing) and there is nothing inconsistent about that — it
-- means the counter has not been told, which is itself worth seeing on a screen.
--
-- ── CONVENTIONS (0095's, deliberately) ─────────────────────────────────────────────────────────
-- UUID v4 PK; TIMESTAMPTZ; quantities NUMERIC(19,4) and never float; every quantity carries its UOM;
-- actor columns are plain UUIDs that reference users(id) SEMANTICALLY but are not FK-joined (the
-- tasks module never JOINs identity). The FK to tasks(id) IS real and cascades, because a pick line
-- is owned by, and meaningless without, its task — the call 0073 made for task_events and 0095 made
-- for task_shortages.
--
-- ADDITIVE AND SAFE AGAINST A STAGING DATABASE THAT IS NEVER WIPED: one CREATE TABLE and two
-- indexes, no ALTER of an existing table, no backfill. Every pick recorded before this migration
-- simply has no rows here, which reads correctly as "nobody wrote down what was pulled" rather than
-- as "nothing was pulled".

CREATE TABLE IF NOT EXISTS task_pick_lines (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id      uuid NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,

    -- The ticket's own line number. It is the join back to the order line for a human, not a FK: the
    -- tasks module does not own order_lines and does not JOIN to it (the module rule), and a pick
    -- against an order line that was later deleted is still a true record of what left the yard.
    order_line_no integer NOT NULL CHECK (order_line_no > 0),

    -- Copied from the ticket at pick time, on purpose. The picker pulled THIS description of THIS
    -- sku; re-reading them from the order later would show what the order says today, which is a
    -- different question and the wrong one to answer about a pallet that already left.
    product_id   uuid,
    sku          text NOT NULL DEFAULT '',
    description  text NOT NULL DEFAULT '',

    -- qty_ordered is the number the ticket asked for, frozen at pick time for the same reason.
    -- qty_picked is what came off the shelf. ZERO IS A LEGITIMATE VALUE and is the whole point: "we
    -- went to pick it and there was none" is a recorded pick of nothing, not an absent row.
    qty_ordered  numeric(19,4) NOT NULL CHECK (qty_ordered >= 0),
    qty_picked   numeric(19,4) NOT NULL CHECK (qty_picked  >= 0),
    uom          text NOT NULL,

    picked_by    uuid,
    picked_at    timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),

    -- ONE ROW PER LINE PER TASK. A picker who corrects themselves rewrites the row rather than
    -- appending a second one: this is a measurement, and two live measurements of one line is the
    -- ambiguity the table exists to remove. The correction history lives in task_events (0073),
    -- which is where this module already keeps "what happened and who did it".
    UNIQUE (task_id, order_line_no)
);

-- The read the yard console makes on every open ticket: this task's lines, in ticket order.
CREATE INDEX IF NOT EXISTS idx_task_pick_lines_task
    ON task_pick_lines (task_id, order_line_no);

-- The read behind "which picks came up short" — the derivation the rule above makes authoritative.
-- Partial, because a full pick is the common case and is not what anybody searches for.
CREATE INDEX IF NOT EXISTS idx_task_pick_lines_short
    ON task_pick_lines (task_id)
    WHERE qty_picked < qty_ordered;

COMMENT ON TABLE task_pick_lines IS
  'Session A item 1: what the yard ACTUALLY pulled, per line of a pick ticket. qty_picked is the authoritative measurement; shortness is derived from qty_picked < qty_ordered, and a task_shortages row is the separate record that the counter was told.';
