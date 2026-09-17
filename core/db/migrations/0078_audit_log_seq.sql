-- 0078: a monotonic insertion-order key on audit_log (DIB-175 B2-1).
--
-- Why. The order-edit change history (DIB-175) must read back CAUSALLY: the several ORDER_CHANGED rows
-- a single edit emits, and successive edits to the same order, have to sort in the order they actually
-- happened. created_at is the TRANSACTION timestamp (now()), so every row of one edit shares it to the
-- microsecond, and two concurrent transactions can carry created_at values that invert relative to the
-- order they committed in. A DB-assigned identity column, handed out at INSERT time, gives a strictly
-- increasing key; because same-order edits are serialized by the orders-row FOR UPDATE lock the edit
-- path holds, that insert order equals the causal order for a given order's trail.
--
-- BACKFILL HONESTY (R1). ADD COLUMN ... GENERATED ALWAYS AS IDENTITY assigns seq to the EXISTING rows in
-- physical heap-scan order. For an append-only table that APPROXIMATES historical insert order but does
-- NOT guarantee it (a heap scan is not insertion order). That is acceptable here precisely because the
-- only reader that sorts by seq is the order-edit history, and every ORDER_CHANGED edit row is created
-- AFTER this migration — no consumer relies on the seq of any pre-0078 row. Do not over-trust historical
-- seq for other purposes.
--
-- Safety. Additive, append-only-friendly (the trail already forbids UPDATE/DELETE). GENERATED ALWAYS
-- means callers never supply seq — the audit INSERT column list is deliberately left untouched (Postgres
-- rejects an explicit value for a GENERATED ALWAYS column). NOT NULL is implied by IDENTITY.

ALTER TABLE audit_log ADD COLUMN seq BIGINT GENERATED ALWAYS AS IDENTITY;

-- Read path for the per-entity causal trail (order-edit history filters + sorts by seq in Go; this
-- index keeps the entity scan cheap and seq-ordered at the source).
CREATE INDEX idx_audit_log_entity_seq ON audit_log (entity_type, entity_id, seq);
