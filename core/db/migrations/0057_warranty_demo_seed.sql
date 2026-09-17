-- 0057: WTY-01 warranty DEMO seed — makes GET /warranty/vendors (FR-1 registry) and the FR-7 dashboard
-- DEMONSTRABLE LIVE on staging. Owner: internal/warranty. DIB-153 / L8 remediation (fix/l8-wty01).
--
-- WHY a migration (not cmd/seed): the staging deploy runs ONLY bin/migrate — it does NOT run cmd/seed. The
-- warranty demo data (6 manufacturer profiles + the back-dated claim backlog) lives ONLY in cmd/seed/
-- dibbits_seed.sql today, so on staging the registry is EMPTY and the dashboard is blank. This migration ports
-- that demo data behind GUARDS + IDEMPOTENCY so it applies exactly once on staging and safely no-ops on a
-- re-run OR on a DB that already has the cmd/seed data (mirrors 0046_uat_portal_test_fixtures.sql +
-- 0049_seed_demo_project.sql).
--
-- SELF-CONTAINED: migrations run BEFORE any seed, so this cannot assume the vendor/customer/product masters
-- exist. It plants its OWN demo vendors + a demo customer (fixed UUIDs, ON CONFLICT DO NOTHING so a
-- pre-existing master is never clobbered), resolves the profile vendor by vendor_no (works whether the vendor
-- is ours or pre-seeded), and uses free-text claim lines (no product-master dependency). Branches (Trenton
-- 5d1b…0001 / Kingston 5d1b…0002) are seeded by 0003 so they are always present. The claim owner resolves to
-- any existing user (or NULL — owner_id is ON DELETE SET NULL) so no user-master row is required.
--
-- Also (L8 demo-gap): the PUT /warranty/vendors/{id} 500 root cause was a bare FK violation surfacing as a
-- 500 when the target vendor is not in the vendors master (the empty-registry case). The Go fix maps that FK
-- error to a 422; THIS migration removes the trigger for the demo by ensuring the 6 vendors exist so the
-- registry is non-empty and a PUT against a seeded vendor succeeds.

-- ─────────────────────────────────────────────────────────────────────────────
-- 1. Demo vendors (the 6 manufacturers). Idempotent on vendor_no: if staging already has them (a historical
--    cmd/seed), DO NOTHING keeps the existing rows; otherwise they are created with fixed UUIDs. Oaks is the
--    CLIENT-CONFIRMED UNDOCUMENTED case (2026-06-30).
-- ─────────────────────────────────────────────────────────────────────────────
INSERT INTO vendors (id, vendor_no, name, payment_terms_days, is_edi) VALUES
  ('f5700000-0000-4000-9000-000000000001','V-UNILOCK', 'Unilock',                       30, FALSE),
  ('f5700000-0000-4000-9000-000000000002','V-TECHO',   'Techo Bloc',                    30, FALSE),
  ('f5700000-0000-4000-9000-000000000003','V-PERMACON','Permacon / Old Castle',         30, FALSE),
  ('f5700000-0000-4000-9000-000000000004','V-OAKS',    'Oaks / Brampton Brick',         30, FALSE),
  ('f5700000-0000-4000-9000-000000000005','V-BESTWAY', 'Best Way Stone',                30, FALSE),
  ('f5700000-0000-4000-9000-000000000006','V-BANAS',   'Banas Stone',                   30, FALSE)
ON CONFLICT (vendor_no) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────────
-- 2. Demo customers for the claims. Idempotent on account_no. customer_type must satisfy the 0002 CHECK
--    (CONTRACTOR/RETAIL/BUILDER/GOVERNMENT/INTERNAL). One contractor + one retail homeowner.
-- ─────────────────────────────────────────────────────────────────────────────
INSERT INTO customers (id, account_no, name, customer_type, home_branch_id) VALUES
  ('c5700000-0000-4000-9000-000000000001','WTY-QUINTE',   'Scrubbed Party 0007',        'CONTRACTOR','5d1b3f6e-0000-4000-9000-000000000001'),
  ('c5700000-0000-4000-9000-000000000003','WTY-LIMESTONE','Scrubbed Party 0006', 'CONTRACTOR','5d1b3f6e-0000-4000-9000-000000000002'),
  ('c5700000-0000-4000-9000-000000000007','WTY-HOMEOWNER','Scrubbed Party 0005',    'RETAIL',    '5d1b3f6e-0000-4000-9000-000000000001')
ON CONFLICT (account_no) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────────
-- 3. Vendor warranty profiles (FR-1) — the registry. Resolves the vendor by vendor_no so it binds to whichever
--    row exists (ours or pre-seeded). Idempotent on vendor_id. Coverage/term values are DOCUMENTED PLACEHOLDERS
--    pending the policy-document collection (Q-065). Oaks = UNDOCUMENTED (the registry LEARNS per claim).
-- ─────────────────────────────────────────────────────────────────────────────
INSERT INTO warranty_vendor_profiles (vendor_id, status, coverage, term_months, channel, channel_ref, notes)
SELECT v.id, s.status, s.coverage, s.term_months, s.channel, s.channel_ref, s.notes
FROM (VALUES
  ('V-UNILOCK',  'DOCUMENTED',   'Transferable lifetime structural integrity warranty on pavers/walls; efflorescence + colour variation excluded.', NULL, 'PORTAL', 'unilock.com/warranty',    'PLACEHOLDER terms pending policy doc (Q-065).'),
  ('V-TECHO',    'DOCUMENTED',   'Lifetime limited warranty on manufacturing defects (smooth/textured); efflorescence excluded.',                  NULL, 'PORTAL', 'techo-bloc.com/warranty', 'PLACEHOLDER; Techo answers ~2 weeks (residual SLA, PRD Q3).'),
  ('V-PERMACON', 'DOCUMENTED',   'Limited lifetime residential warranty on manufacturing defects; commercial 25 yr.',                             300,  'EMAIL',  'scrubbed-0002@example.invalid',    'PLACEHOLDER terms pending policy doc.'),
  ('V-OAKS',     'UNDOCUMENTED', '',                                                                                                              NULL, 'REP',    '',                        'UNDOCUMENTED (the Oaks case, 2026-06-30) — capture terms negotiated per claim; the registry learns.'),
  ('V-BESTWAY',  'DOCUMENTED',   'Limited warranty on manufacturing defects; colour/efflorescence excluded.',                                     120,  'EMAIL',  'scrubbed-0003@example.invalid',   'PLACEHOLDER terms pending policy doc.'),
  ('V-BANAS',    'DOCUMENTED',   'Natural-stone material warranty (transit breakage / manufacturing flaw); natural variation excluded.',          NULL, 'REP',    'Banas territory rep',     'PLACEHOLDER; natural stone — variation is expected, not a defect.')
) AS s(vendor_no, status, coverage, term_months, channel, channel_ref, notes)
JOIN vendors v ON v.vendor_no = s.vendor_no
ON CONFLICT (vendor_id) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────────
-- 4. Wire the existing staging portal user (uat.tester@quintehardscapes.example, migration 0046) to the demo
--    project (0049), so its customer owns the demo project — verified + wired. Guarded on BOTH the portal user
--    and the project existing (safe no-op on a fresh DB where neither is present). Re-point the 0049 project to
--    the uat.tester's customer so the portal login and the project rollup share one customer.
-- ─────────────────────────────────────────────────────────────────────────────
DO $$
DECLARE
  uat_cust UUID := (SELECT customer_id FROM portal_users WHERE email = 'uat.tester@quintehardscapes.example');
BEGIN
  IF uat_cust IS NOT NULL AND EXISTS (SELECT 1 FROM projects WHERE id = 'b8000000-0000-4000-9000-0000000000a1') THEN
    UPDATE projects
       SET customer_id = uat_cust, updated_at = now()
     WHERE id = 'b8000000-0000-4000-9000-0000000000a1'
       AND customer_id <> uat_cust;
  END IF;
END $$;

-- ─────────────────────────────────────────────────────────────────────────────
-- 5. Demo claim backlog (6-8 claims) ACROSS the confirmed phases (FR-3) with BACK-DATED history (FR-9). Fixed
--    UUIDs + guarded on the demo WTY-* claim numbers so a re-run (or a DB already carrying the cmd/seed set) is
--    a no-op. Owner resolves to any existing user, else NULL (owner_id is ON DELETE SET NULL). Claim lines are
--    free-text (product_text) so no product master is required. The set is engineered so the FR-7 dashboard is
--    non-trivial live: an in-flight funnel across INTAKE→DECISION, a WON close (VENDOR_CREDIT with realized $),
--    a LOST reject (the Oaks case), plus open exposure by vendor.
-- ─────────────────────────────────────────────────────────────────────────────
DO $$
DECLARE
  v_unilock  UUID := (SELECT id FROM vendors WHERE vendor_no = 'V-UNILOCK');
  v_techo    UUID := (SELECT id FROM vendors WHERE vendor_no = 'V-TECHO');
  v_permacon UUID := (SELECT id FROM vendors WHERE vendor_no = 'V-PERMACON');
  v_oaks     UUID := (SELECT id FROM vendors WHERE vendor_no = 'V-OAKS');
  v_bestway  UUID := (SELECT id FROM vendors WHERE vendor_no = 'V-BESTWAY');
  v_banas    UUID := (SELECT id FROM vendors WHERE vendor_no = 'V-BANAS');
  trt        UUID := '5d1b3f6e-0000-4000-9000-000000000001'; -- Trenton
  kgn        UUID := '5d1b3f6e-0000-4000-9000-000000000002'; -- Kingston
  -- Owner: any existing user (the staging admin, or NULL if the DB has none). owner_id is ON DELETE SET NULL.
  owner_a    UUID := (SELECT id FROM users ORDER BY created_at LIMIT 1);
  -- Customers: prefer the demo customers seeded above; fall back to whatever exists (guards resolve NULLs).
  cust1      UUID := (SELECT id FROM customers WHERE account_no = 'WTY-QUINTE');
  cust3      UUID := (SELECT id FROM customers WHERE account_no = 'WTY-LIMESTONE');
  cust7      UUID := (SELECT id FROM customers WHERE account_no = 'WTY-HOMEOWNER');
  c1 UUID := 'a1570000-0000-4000-9000-000000000001';
  c2 UUID := 'a1570000-0000-4000-9000-000000000002';
  c3 UUID := 'a1570000-0000-4000-9000-000000000003';
  c4 UUID := 'a1570000-0000-4000-9000-000000000004';
  c5 UUID := 'a1570000-0000-4000-9000-000000000005';
  c6 UUID := 'a1570000-0000-4000-9000-000000000006';
  c7 UUID := 'a1570000-0000-4000-9000-000000000007';
  c8 UUID := 'a1570000-0000-4000-9000-000000000008';
BEGIN
  -- Idempotent guard: the demo backlog is keyed on WTY-D1 (the demo prefix, distinct from the live WTY-n
  -- sequence the service assigns, so a demo claim never collides with a captured one).
  IF EXISTS (SELECT 1 FROM warranty_claims WHERE claim_no = 'WTY-D1') THEN RETURN; END IF;

  -- Claim 1 — TRIAGE, Unilock, Trenton (in-flight). Back-dated 20 days.
  INSERT INTO warranty_claims (id, claim_no, customer_id, job_address, vendor_id, owner_id, branch_id, phase, source,
      description, expected_cents, realized_cents, opened_at, created_by)
    VALUES (c1,'WTY-D1',cust1,'{"line1":"0002 Placeholder Way","city":"Trenton","province":"ON"}',v_unilock,owner_a,trt,'TRIAGE','INTAKE',
      'Efflorescence blooming across Brussels Block patio after first winter.', 145000, 0, now()-INTERVAL '20 days', owner_a);
  INSERT INTO warranty_claim_lines (claim_id, product_id, product_text, qty, uom_code)
    VALUES (c1, NULL,'Unilock Brussels Block', 320, NULL);
  INSERT INTO warranty_claim_events (claim_id, event_type, from_phase, to_phase, actor_id, note, occurred_at) VALUES
    (c1,'CREATED',      NULL,   'INTAKE', owner_a,'Logged from office intake.',  now()-INTERVAL '20 days'),
    (c1,'PHASE_CHANGE','INTAKE','TRIAGE', owner_a,'Picked up for triage.',       now()-INTERVAL '18 days');

  -- Claim 2 — WAITING_ON_DECISION, Techo-Bloc, Trenton. SLA stamped on Submit. Back-dated 45 days.
  INSERT INTO warranty_claims (id, claim_no, customer_id, job_address, vendor_id, owner_id, branch_id, phase, source,
      description, expected_cents, realized_cents, submitted_at, sla_due, opened_at, created_by)
    VALUES (c2,'WTY-D2',cust1,'{"line1":"0010 Placeholder Way","city":"Trenton","province":"ON"}',v_techo,owner_a,trt,'WAITING_ON_DECISION','COUNTER',
      'Two Blu 60 slabs cracked through — suspected manufacturing defect.', 89000, 0,
      now()-INTERVAL '30 days', now()-INTERVAL '16 days', now()-INTERVAL '45 days', owner_a);
  INSERT INTO warranty_claim_lines (claim_id, product_id, product_text, qty, uom_code)
    VALUES (c2, NULL,'Techo-Bloc Blu 60 Slab', 2, NULL);
  INSERT INTO warranty_claim_events (claim_id, event_type, from_phase, to_phase, actor_id, note, occurred_at) VALUES
    (c2,'CREATED',      NULL,               'INTAKE',              owner_a,'Contractor walked in with photos; started from invoice.', now()-INTERVAL '45 days'),
    (c2,'PHASE_CHANGE','INTAKE',            'TRIAGE',              owner_a,'Triaged — clear manufacturing crack.',                    now()-INTERVAL '40 days'),
    (c2,'PHASE_CHANGE','TRIAGE',            'SUBMITTED',           owner_a,'Submitted to Techo-Bloc portal.',                         now()-INTERVAL '30 days'),
    (c2,'PHASE_CHANGE','SUBMITTED',         'WAITING_ON_DECISION', owner_a,'Techo acknowledged; ~2 week decision window.',            now()-INTERVAL '29 days');

  -- Claim 3 — CLOSED / VENDOR_CREDIT (a WIN), Permacon, Kingston. Full history + realized $.
  INSERT INTO warranty_claims (id, claim_no, customer_id, job_address, vendor_id, owner_id, branch_id, phase, source,
      description, outcome, expected_cents, realized_cents, submitted_at, sla_due, opened_at, closed_at, created_by)
    VALUES (c3,'WTY-D3',cust3,'{"line1":"0001 Placeholder Way","city":"Kingston","province":"ON"}',v_permacon,owner_a,kgn,'CLOSED','INTAKE',
      'Melville slab colour fade on a full patio; Permacon approved credit.', 'VENDOR_CREDIT', 210000, 198000,
      now()-INTERVAL '75 days', now()-INTERVAL '61 days', now()-INTERVAL '92 days', now()-INTERVAL '40 days', owner_a);
  INSERT INTO warranty_claim_lines (claim_id, product_id, product_text, qty, uom_code)
    VALUES (c3, NULL,'Permacon Melville Slab', 460, NULL);
  INSERT INTO warranty_claim_events (claim_id, event_type, from_phase, to_phase, actor_id, note, detail, occurred_at) VALUES
    (c3,'CREATED',      NULL,                 'INTAKE',              owner_a,'Logged from office intake.', '{}', now()-INTERVAL '92 days'),
    (c3,'PHASE_CHANGE','INTAKE',              'TRIAGE',              owner_a,'Triaged.',                    '{}', now()-INTERVAL '88 days'),
    (c3,'PHASE_CHANGE','TRIAGE',              'SUBMITTED',           owner_a,'Submitted to Permacon.',      '{}', now()-INTERVAL '75 days'),
    (c3,'PHASE_CHANGE','SUBMITTED',           'WAITING_ON_DECISION', owner_a,'Ack received.',               '{}', now()-INTERVAL '74 days'),
    (c3,'PHASE_CHANGE','WAITING_ON_DECISION', 'DECISION',            owner_a,'Permacon approved.',          '{}', now()-INTERVAL '55 days'),
    (c3,'PHASE_CHANGE','DECISION',            'SETTLEMENT',          owner_a,'Booking the credit.',         '{}', now()-INTERVAL '50 days'),
    (c3,'SETTLEMENT',  'SETTLEMENT',          'SETTLEMENT',          owner_a,'Vendor credit received.',     '{"outcome":"VENDOR_CREDIT","expected_cents":210000,"realized_cents":198000}', now()-INTERVAL '42 days'),
    (c3,'PHASE_CHANGE','SETTLEMENT',          'CLOSED',              owner_a,'Closed — credit applied.',    '{}', now()-INTERVAL '40 days');

  -- Claim 4 — REJECTED (a LOSS), Oaks (UNDOCUMENTED vendor), Kingston. Demonstrates the Oaks case + win/loss.
  INSERT INTO warranty_claims (id, claim_no, customer_id, job_address, vendor_id, owner_id, branch_id, phase, source,
      description, expected_cents, realized_cents, submitted_at, sla_due, opened_at, closed_at, created_by)
    VALUES (c4,'WTY-D4',cust3,'{"line1":"0008 Placeholder Way","city":"Kingston","province":"ON"}',v_oaks,owner_a,kgn,'REJECTED','INTAKE',
      'Oaks Vintage paver spalling; Oaks declined — no documented warranty on record.', 62000, 0,
      now()-INTERVAL '50 days', now()-INTERVAL '36 days', now()-INTERVAL '66 days', now()-INTERVAL '30 days', owner_a);
  INSERT INTO warranty_claim_lines (claim_id, product_id, product_text, qty, uom_code)
    VALUES (c4, NULL,'Oaks Vintage Paver', 180, NULL);
  INSERT INTO warranty_claim_events (claim_id, event_type, from_phase, to_phase, actor_id, note, occurred_at) VALUES
    (c4,'CREATED',      NULL,                 'INTAKE',              owner_a,'Logged; Oaks warranty terms UNDOCUMENTED.', now()-INTERVAL '66 days'),
    (c4,'PHASE_CHANGE','INTAKE',              'TRIAGE',              owner_a,'Triaged.',                                  now()-INTERVAL '62 days'),
    (c4,'PHASE_CHANGE','TRIAGE',              'SUBMITTED',           owner_a,'Submitted via rep.',                        now()-INTERVAL '50 days'),
    (c4,'PHASE_CHANGE','SUBMITTED',           'WAITING_ON_DECISION', owner_a,'Awaiting rep response.',                    now()-INTERVAL '49 days'),
    (c4,'PHASE_CHANGE','WAITING_ON_DECISION', 'DECISION',            owner_a,'Oaks declined.',                            now()-INTERVAL '32 days'),
    (c4,'PHASE_CHANGE','DECISION',            'REJECTED',            owner_a,'Rejected — no coverage on record.',         now()-INTERVAL '30 days');

  -- Claim 5 — INTAKE, retail/homeowner, no vendor yet (UNKNOWN pending triage). Fresh.
  INSERT INTO warranty_claims (id, claim_no, customer_id, job_address, vendor_id, owner_id, branch_id, phase, source,
      description, expected_cents, realized_cents, opened_at, created_by)
    VALUES (c5,'WTY-D5',cust7,'{"line1":"0005 Placeholder Way","city":"Brighton","province":"ON"}',NULL,owner_a,trt,'INTAKE','INTAKE',
      'Homeowner reports pitting on patio pavers; product/manufacturer not yet identified.', 0, 0, now()-INTERVAL '3 days', owner_a);
  INSERT INTO warranty_claim_lines (claim_id, product_id, product_text, qty, uom_code)
    VALUES (c5, NULL,'Unknown grey paver (photos supplied)', 90, NULL);
  INSERT INTO warranty_claim_events (claim_id, event_type, from_phase, to_phase, actor_id, note, occurred_at) VALUES
    (c5,'CREATED', NULL,'INTAKE', owner_a,'Retail intake; vendor UNKNOWN pending triage.', now()-INTERVAL '3 days');

  -- Claim 6 — WAITING_ON_DECISION, Unilock, Trenton (open exposure). Back-dated 33 days.
  INSERT INTO warranty_claims (id, claim_no, customer_id, job_address, vendor_id, owner_id, branch_id, phase, source,
      description, expected_cents, realized_cents, submitted_at, sla_due, opened_at, created_by)
    VALUES (c6,'WTY-D6',cust1,'{"line1":"0009 Placeholder Way","city":"Trenton","province":"ON"}',v_unilock,owner_a,trt,'WAITING_ON_DECISION','COUNTER',
      'Il Campo pavers heaving on a driveway after one freeze/thaw — suspected base or product defect.', 176000, 0,
      now()-INTERVAL '18 days', now()-INTERVAL '4 days', now()-INTERVAL '33 days', owner_a);
  INSERT INTO warranty_claim_lines (claim_id, product_id, product_text, qty, uom_code)
    VALUES (c6, NULL,'Unilock Il Campo', 540, NULL);
  INSERT INTO warranty_claim_events (claim_id, event_type, from_phase, to_phase, actor_id, note, occurred_at) VALUES
    (c6,'CREATED',      NULL,       'INTAKE',              owner_a,'Started from invoice at the counter.',      now()-INTERVAL '33 days'),
    (c6,'PHASE_CHANGE','INTAKE',    'TRIAGE',              owner_a,'Triaged — photos + base spec gathered.',    now()-INTERVAL '30 days'),
    (c6,'PHASE_CHANGE','TRIAGE',    'SUBMITTED',           owner_a,'Submitted to Unilock portal.',              now()-INTERVAL '18 days'),
    (c6,'PHASE_CHANGE','SUBMITTED', 'WAITING_ON_DECISION', owner_a,'Unilock acknowledged; awaiting ruling.',    now()-INTERVAL '17 days');

  -- Claim 7 — DECISION, Techo-Bloc, Kingston (open exposure, further along). Back-dated 58 days.
  INSERT INTO warranty_claims (id, claim_no, customer_id, job_address, vendor_id, owner_id, branch_id, phase, source,
      description, expected_cents, realized_cents, submitted_at, sla_due, opened_at, created_by)
    VALUES (c7,'WTY-D7',cust3,'{"line1":"0004 Placeholder Way","city":"Kingston","province":"ON"}',v_techo,owner_a,kgn,'DECISION','INTAKE',
      'Techo-Bloc Aberdeen wall units delaminating; Techo reviewing — decision imminent.', 224000, 0,
      now()-INTERVAL '40 days', now()-INTERVAL '26 days', now()-INTERVAL '58 days', owner_a);
  INSERT INTO warranty_claim_lines (claim_id, product_id, product_text, qty, uom_code)
    VALUES (c7, NULL,'Techo-Bloc Aberdeen Wall', 130, NULL);
  INSERT INTO warranty_claim_events (claim_id, event_type, from_phase, to_phase, actor_id, note, occurred_at) VALUES
    (c7,'CREATED',      NULL,                 'INTAKE',              owner_a,'Logged from office intake.',           now()-INTERVAL '58 days'),
    (c7,'PHASE_CHANGE','INTAKE',              'TRIAGE',              owner_a,'Triaged — clear delamination.',        now()-INTERVAL '54 days'),
    (c7,'PHASE_CHANGE','TRIAGE',              'SUBMITTED',           owner_a,'Submitted to Techo-Bloc.',             now()-INTERVAL '40 days'),
    (c7,'PHASE_CHANGE','SUBMITTED',           'WAITING_ON_DECISION', owner_a,'Techo acknowledged.',                  now()-INTERVAL '39 days'),
    (c7,'PHASE_CHANGE','WAITING_ON_DECISION', 'DECISION',            owner_a,'Techo engineer reviewing on-site.',    now()-INTERVAL '12 days');

  -- Claim 8 — CLOSED / VENDOR_CREDIT (a WIN), Best Way Stone, Trenton. Adds a second manufacturer's realized
  --   recovery so the by-manufacturer win/loss + realized-$ chart spans more than Permacon.
  INSERT INTO warranty_claims (id, claim_no, customer_id, job_address, vendor_id, owner_id, branch_id, phase, source,
      description, outcome, expected_cents, realized_cents, submitted_at, sla_due, opened_at, closed_at, created_by)
    VALUES (c8,'WTY-D8',cust1,'{"line1":"0006 Placeholder Way","city":"Trenton","province":"ON"}',v_bestway,owner_a,trt,'CLOSED','COUNTER',
      'Best Way Antico pavers with a pin-hole casting defect across a lot; credit approved.', 'VENDOR_CREDIT', 54000, 54000,
      now()-INTERVAL '80 days', now()-INTERVAL '66 days', now()-INTERVAL '96 days', now()-INTERVAL '52 days', owner_a);
  INSERT INTO warranty_claim_lines (claim_id, product_id, product_text, qty, uom_code)
    VALUES (c8, NULL,'Best Way Stone Antico Paver', 260, NULL);
  INSERT INTO warranty_claim_events (claim_id, event_type, from_phase, to_phase, actor_id, note, detail, occurred_at) VALUES
    (c8,'CREATED',      NULL,                 'INTAKE',              owner_a,'Started from counter invoice.', '{}', now()-INTERVAL '96 days'),
    (c8,'PHASE_CHANGE','INTAKE',              'TRIAGE',              owner_a,'Triaged.',                      '{}', now()-INTERVAL '92 days'),
    (c8,'PHASE_CHANGE','TRIAGE',              'SUBMITTED',           owner_a,'Submitted to Best Way.',        '{}', now()-INTERVAL '80 days'),
    (c8,'PHASE_CHANGE','SUBMITTED',           'WAITING_ON_DECISION', owner_a,'Ack received.',                 '{}', now()-INTERVAL '79 days'),
    (c8,'PHASE_CHANGE','WAITING_ON_DECISION', 'DECISION',            owner_a,'Best Way approved.',            '{}', now()-INTERVAL '60 days'),
    (c8,'PHASE_CHANGE','DECISION',            'SETTLEMENT',          owner_a,'Booking the credit.',           '{}', now()-INTERVAL '55 days'),
    (c8,'SETTLEMENT',  'SETTLEMENT',          'SETTLEMENT',          owner_a,'Vendor credit received.',       '{"outcome":"VENDOR_CREDIT","expected_cents":54000,"realized_cents":54000}', now()-INTERVAL '53 days'),
    (c8,'PHASE_CHANGE','SETTLEMENT',          'CLOSED',              owner_a,'Closed — credit applied.',      '{}', now()-INTERVAL '52 days');
END $$;

-- ─────────────────────────────────────────────────────────────────────────────
-- 6. FR-7 COHERENCE GUARD (L8 round 5, MONEY-CRITICAL). A claim in a terminal-LOST exit phase (ABANDONED /
--    REJECTED) must NEVER carry a settlement outcome or realized recovery: those phases mean "nothing came
--    back", and the runtime settled-exit guard enforces exactly this at write time. A seed (this one, a
--    historical cmd/seed, or a pre-existing staging row) that inserts such a state directly BYPASSES that guard
--    and makes the by-manufacturer report incoherent — realized $ attributed to a vendor with zero wins. The
--    Go fix (buildByManufacturer counts realized only on classify()==WON) neutralizes the READ; this clause
--    neutralizes the DATA so a fresh migrate is coherent at the source. IDEMPOTENT + set-based: it only touches
--    rows that are actually in the corrupt state, so it is a safe no-op on a clean DB and on re-run. We DROP the
--    settlement fields (outcome + realized) rather than move the phase, so the claim stays a genuine LOSS
--    (which is what an abandoned/rejected claim is) and win/loss + realized $ can never disagree.
UPDATE warranty_claims
   SET outcome = NULL, realized_cents = 0
 WHERE phase IN ('ABANDONED', 'REJECTED')
   AND (outcome IS NOT NULL OR realized_cents <> 0);
