-- 0088: print_queue — the one substrate every printed document in this system will go through
-- (DIB-411, bundle W1A-B).
--
-- WHY A QUEUE AND NOT A BOOLEAN. Nothing in HardscapeOS prints today. The first real need is the
-- counter receipt (DIB-410), which the browser renders and the operator prints from the print
-- dialog — no device, no spooler. But the two printing gaps already found in the field are LABELS
-- (yard/bin labels on a Zebra, i.e. raw ZPL pushed at a networked printer) and DELIVERY DOCUMENTS
-- (the driver's paperwork). Those are agent/device consumers: something outside the request cycle
-- picks the job up, renders it, and reports back. A boolean "printed_at" on pos_sale could never
-- carry that, and retrofitting a queue after two doc types shipped means migrating live rows.
--
-- So the enum is designed for the destination NOW, and the receipt is simply its first, simplest
-- consumer: the BROWSER target completes inside the request that created it (enqueue → render →
-- mark printed), while a ZPL target will sit QUEUED until a device agent claims it. Same table,
-- same lifecycle, no second mechanism.
--
-- REF_ID IS A RECORDED IDENTIFIER, NOT AN FK. doc_type decides what ref_id points at (RECEIPT → the
-- order, LABEL_ZPL → a product/bin, DELIVERY_DOC → a manifest). A polymorphic column cannot carry a
-- foreign key, and it should not: the print history is a record of what was PRINTED, and it must
-- stay readable after the thing it described is gone. That is the posture audit_log.entity_id and
-- quote_acceptances.quote_id already take here, for the same reason.
--
-- branch_id IS a real FK: every job belongs to exactly one branch's paper, the branch outlives the
-- job, and the branch is what an agent/device will filter its work queue on.
CREATE TABLE print_queue (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  doc_type      TEXT NOT NULL CHECK (doc_type IN ('RECEIPT','LABEL_ZPL','DELIVERY_DOC')),
  ref_id        UUID NOT NULL,  -- recorded identifier; deliberately NOT an FK (see above)
  branch_id     UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  render_target TEXT NOT NULL CHECK (render_target IN ('BROWSER','ZPL')),
  status        TEXT NOT NULL DEFAULT 'QUEUED' CHECK (status IN ('QUEUED','PRINTED','FAILED')),
  -- who asked for the paper. SET NULL on user delete: the job stays true even when the staff row goes.
  requested_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  -- FAILED jobs carry the reason so a re-queue decision is made on evidence, not a guess.
  failure_reason TEXT NOT NULL DEFAULT '',
  requested_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  printed_at    TIMESTAMPTZ,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- printed_at is set EXACTLY when the job is PRINTED. This is what makes the reprint count on a
  -- receipt trustworthy: "printed twice" cannot be produced by a row that only ever queued.
  CONSTRAINT print_queue_printed_at_matches_status CHECK ((status = 'PRINTED') = (printed_at IS NOT NULL))
);

-- The reprint read: "how many times has THIS document been printed", newest first.
CREATE INDEX idx_print_queue_ref ON print_queue (doc_type, ref_id, requested_at DESC);
-- The future device/agent claim read: "what is still QUEUED for my branch, oldest first".
CREATE INDEX idx_print_queue_pending ON print_queue (status, branch_id, requested_at) WHERE status = 'QUEUED';
