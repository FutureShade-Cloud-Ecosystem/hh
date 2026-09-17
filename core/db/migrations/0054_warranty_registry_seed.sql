-- 0054: WTY-01 default checklist template (P1). Owner: internal/warranty. DIB-153.
-- Seeds the DEFAULT per-phase checklist template that gates advancement through the CLIENT-CONFIRMED
-- 2026-07-07 Kanban lifecycle (FR-3): a phase can't advance while any REQUIRED item for that phase is unticked.
-- This is schema-level, vendor-INDEPENDENT default data (vendor_id NULL), so it lives in a migration. The
-- vendor warranty PROFILES (FR-1) and the demo CLAIMS depend on the vendor/customer/product MASTERS that the
-- Go seed (cmd/seed/dibbits_seed.sql) plants AFTER migrations run, so they live in the seed, not here.
-- All checklist copy is a DOCUMENTED PLACEHOLDER pending Scrubname0025 Scrubname0024's flooring-workflow writeup (PRD §9 Q4).

-- ── default checklist template (FR-3) — the phase gates ────────────────────────
-- Fixed UUIDs so a tick's history ref (warranty_claim_events.checklist_item_id) is stable across re-seeds and
-- the item_key is denormalized onto the tick for extra stability. vendor_id NULL = applies to every vendor.
-- required=TRUE items GATE advancement OUT of their phase (a phase can't advance with a required item unticked).
-- Example (Submit gate, PRD FR-3): photos attached, proof-of-purchase attached, vendor form sent.
INSERT INTO warranty_checklist_items (id, phase, vendor_id, item_key, label, required, sort_order) VALUES
  -- INTAKE: get the basics on the card before triage owns it.
  ('c1a70000-0000-4000-9000-000000000001','INTAKE',              NULL,'contact_captured',     'Customer / contact captured',            TRUE, 10),
  ('c1a70000-0000-4000-9000-000000000002','INTAKE',              NULL,'product_identified',   'Failed product + quantity identified',   TRUE, 20),
  ('c1a70000-0000-4000-9000-000000000003','INTAKE',              NULL,'description_captured', 'Failure description recorded',           TRUE, 30),
  ('c1a70000-0000-4000-9000-000000000004','INTAKE',              NULL,'photos_attached',      'Photos attached',                        FALSE,40),
  -- TRIAGE: someone owns it; confirm the vendor + coverage before submitting.
  ('c1a70000-0000-4000-9000-000000000010','TRIAGE',              NULL,'owner_assigned',       'Owner assigned',                         TRUE, 10),
  ('c1a70000-0000-4000-9000-000000000011','TRIAGE',              NULL,'vendor_identified',    'Manufacturer / vendor identified',       TRUE, 20),
  ('c1a70000-0000-4000-9000-000000000012','TRIAGE',              NULL,'coverage_reviewed',    'Warranty coverage reviewed',             TRUE, 30),
  ('c1a70000-0000-4000-9000-000000000013','TRIAGE',              NULL,'proof_of_purchase',    'Proof of purchase located',              TRUE, 40),
  -- SUBMITTED: everything is with the vendor (the FR-3 Submit gate).
  ('c1a70000-0000-4000-9000-000000000020','SUBMITTED',           NULL,'photos_attached',      'Photos attached',                        TRUE, 10),
  ('c1a70000-0000-4000-9000-000000000021','SUBMITTED',           NULL,'proof_attached',       'Proof of purchase attached',             TRUE, 20),
  ('c1a70000-0000-4000-9000-000000000022','SUBMITTED',           NULL,'vendor_form_sent',     'Vendor warranty form sent',              TRUE, 30),
  -- WAITING_ON_DECISION: acknowledge + track the vendor's clock.
  ('c1a70000-0000-4000-9000-000000000030','WAITING_ON_DECISION', NULL,'submission_ack',       'Vendor acknowledged the submission',     TRUE, 10),
  ('c1a70000-0000-4000-9000-000000000031','WAITING_ON_DECISION', NULL,'followup_logged',      'Follow-up scheduled / logged',           FALSE,20),
  -- DECISION: record the vendor's verdict + terms.
  ('c1a70000-0000-4000-9000-000000000040','DECISION',            NULL,'decision_recorded',    'Vendor decision recorded',               TRUE, 10),
  ('c1a70000-0000-4000-9000-000000000041','DECISION',            NULL,'terms_captured',       'Settlement terms captured',              TRUE, 20),
  -- SETTLEMENT: book the money path before close.
  ('c1a70000-0000-4000-9000-000000000050','SETTLEMENT',          NULL,'outcome_selected',     'Settlement outcome selected',            TRUE, 10),
  ('c1a70000-0000-4000-9000-000000000051','SETTLEMENT',          NULL,'amounts_recorded',     'Expected + realized amounts recorded',   TRUE, 20),
  ('c1a70000-0000-4000-9000-000000000052','SETTLEMENT',          NULL,'customer_made_whole',  'Customer made whole (if applicable)',    FALSE,30)
ON CONFLICT (phase, vendor_id, item_key) DO NOTHING;
