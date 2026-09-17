-- 0179 — DIB-510: the receivables register's DEMO FIXTURE. A book of invoices that actually has an
--        aging profile, so the screen can be seen doing its job.
--
-- WHY THIS EXISTS. The seeded book (0046/0056/0065 and the POS ring) is a healthy one: 15 finalized
-- invoices, none of them past due, no deliveries completed, no disputes, no promises, nobody on hold.
-- That is a fine state for a fresh install and a useless one for a receivables screen — every chip
-- reads zero, the overdue colour never fires, the proof-of-delivery strip has nothing to show and the
-- bulk bar has nothing worth selecting. A reviewer cannot tell a working register from a broken one.
--
-- SO THIS SEEDS A BOOK WITH A SHAPE: current, due-soon, three degrees of late, one partially paid,
-- one disputed, one paid, one credit memo, one account on hold with a promise against it, one
-- delivery with a signature and photographs behind it.
--
-- IT IS A FIXTURE AND IT SAYS SO. Nothing in production code depends on these rows; nothing here
-- teaches the application a fact it would not otherwise learn. Every id is a fixed `510…` UUID, every
-- statement is ON CONFLICT DO NOTHING, and every insert is guarded `WHERE EXISTS (…)` against the
-- demo customers, so:
--   · on a FRESH database (migrations run BEFORE `make seed`) this migration applies to nothing and
--     no-ops, exactly as 0046/0049/0059 do; re-running migrate after the seed lands it;
--   · a re-run is a no-op rather than a second book.
--
-- DATES ARE RELATIVE TO CURRENT_DATE, not literals. A fixture pinned to August 2026 is a fixture that
-- silently stops demonstrating "47 days overdue" the moment somebody looks at it in October.
--
-- MONEY IS INTERNALLY CONSISTENT: subtotal + tax = total for every row, balance ≤ total, and the one
-- partially-paid document carries a real payment with a real allocation whose amount is exactly the
-- difference. The register computes nothing client-side, so a fixture that did not add up would show
-- up as a screen that contradicts itself.

-- ── the documents ──────────────────────────────────────────────────────────────────────────────
-- 13% HST on each subtotal, rounded half-up once, matching FlatHSTAssessor.
INSERT INTO invoices (id, invoice_no, customer_id, order_id, branch_id, status, issue_date, due_date,
                      subtotal_cents, tax_cents, holdback_cents, total_cents, balance_cents,
                      finalized_at, source_kind, doc_type)
SELECT v.id::uuid, v.invoice_no, v.customer_id::uuid, NULL, v.branch_id::uuid, v.status,
       CURRENT_DATE - v.issued_ago, CURRENT_DATE + v.due_in,
       v.subtotal, v.tax, 0, v.subtotal + v.tax, v.balance,
       now() - (v.issued_ago || ' days')::interval, 'ORDER', v.doc_type
FROM (VALUES
  -- id                                     | no          | customer                             | branch                               | status          | issued_ago | due_in | subtotal | tax    | balance  | doc
  ('51000000-0000-4000-9000-000000000001','INV-2117','c7000000-0000-4000-9000-000000000001','5d1b3f6e-0000-4000-9000-000000000001','FINALIZED',      42,   -12, 486000::bigint, 63180::bigint, 549180::bigint,'INVOICE'),
  ('51000000-0000-4000-9000-000000000002','INV-2094','c7000000-0000-4000-9000-000000000003','5d1b3f6e-0000-4000-9000-000000000002','PARTIALLY_PAID', 77,   -47, 312500::bigint, 40625::bigint, 178125::bigint,'INVOICE'),
  ('51000000-0000-4000-9000-000000000003','INV-2136','c7000000-0000-4000-9000-000000000002','5d1b3f6e-0000-4000-9000-000000000001','FINALIZED',      26,     4, 189400::bigint, 24622::bigint, 214022::bigint,'INVOICE'),
  ('51000000-0000-4000-9000-000000000004','INV-2151','c7000000-0000-4000-9000-000000000004','5d1b3f6e-0000-4000-9000-000000000002','FINALIZED',       8,    22,  74250::bigint,  9653::bigint,  83903::bigint,'INVOICE'),
  ('51000000-0000-4000-9000-000000000005','INV-2061','c7000000-0000-4000-9000-000000000005','5d1b3f6e-0000-4000-9000-000000000001','FINALIZED',     113,   -68, 928000::bigint,120640::bigint,1048640::bigint,'INVOICE'),
  ('51000000-0000-4000-9000-000000000006','INV-2142','c7000000-0000-4000-9000-000000000002','5d1b3f6e-0000-4000-9000-000000000001','PAID',           19,    11, 145000::bigint, 18850::bigint,      0::bigint,'INVOICE'),
  ('51000000-0000-4000-9000-000000000007','INV-2019','c7000000-0000-4000-9000-000000000001','5d1b3f6e-0000-4000-9000-000000000002','FINALIZED',     136,   -91, 402000::bigint, 52260::bigint, 454260::bigint,'INVOICE'),
  ('51000000-0000-4000-9000-000000000009','INV-2158','c7000000-0000-4000-9000-000000000005','5d1b3f6e-0000-4000-9000-000000000001','FINALIZED',       3,    42, 268900::bigint, 34957::bigint, 303857::bigint,'INVOICE')
) AS v (id, invoice_no, customer_id, branch_id, status, issued_ago, due_in, subtotal, tax, balance, doc_type)
WHERE EXISTS (SELECT 1 FROM customers c WHERE c.id = v.customer_id::uuid)
ON CONFLICT (id) DO NOTHING;

-- The credit memo: a negative document beside INV-2094, the reversing-entry discipline 0146 enforces
-- in the schema. It is what the register's "Show credits" toggle reveals.
INSERT INTO invoices (id, invoice_no, customer_id, branch_id, status, issue_date, due_date,
                      subtotal_cents, tax_cents, holdback_cents, total_cents, balance_cents,
                      finalized_at, source_kind, doc_type, reverses_invoice_id)
SELECT '51000000-0000-4000-9000-000000000008'::uuid, 'CM-2094-1',
       'c7000000-0000-4000-9000-000000000003'::uuid, '5d1b3f6e-0000-4000-9000-000000000002'::uuid,
       'FINALIZED', CURRENT_DATE - 31, CURRENT_DATE - 1,
       -42000, -5460, 0, -47460, -47460, now() - interval '31 days', 'ORDER', 'CREDIT_MEMO',
       '51000000-0000-4000-9000-000000000002'::uuid
WHERE EXISTS (SELECT 1 FROM invoices WHERE id = '51000000-0000-4000-9000-000000000002'::uuid)
ON CONFLICT (id) DO NOTHING;

-- ── the lines ──────────────────────────────────────────────────────────────────────────────────
-- Two or three real catalogue lines per document, priced so each set sums to its header subtotal.
-- Product ids are resolved by SKU rather than hard-coded: the demo catalogue's ids are generated.
INSERT INTO invoice_lines (invoice_id, line_no, product_id, description, qty_entered, uom_entered,
                           qty_base, unit_price_cents, price_uom, line_total_cents)
SELECT l.invoice_id::uuid, l.line_no, p.id, l.description, l.qty, l.uom, l.qty, l.unit, l.uom, l.total
FROM (VALUES
  ('51000000-0000-4000-9000-000000000001',1,'DBS-AGG-34CLEAR','3/4" Clear Stone — driveway base',  18.0000,'T', 1800::bigint, 324000::bigint),
  ('51000000-0000-4000-9000-000000000001',2,'DBS-AGG-HPB',    'HPB bedding stone',                  9.0000,'T', 1800::bigint, 162000::bigint),
  ('51000000-0000-4000-9000-000000000002',1,'DBS-AGG-34CLEAR','3/4" Clear Stone — parking pad',    12.5000,'T', 1800::bigint, 225000::bigint),
  ('51000000-0000-4000-9000-000000000002',2,'DBS-AGG-HPB',    'HPB bedding stone',                  4.8611,'T', 1800::bigint,  87500::bigint),
  ('51000000-0000-4000-9000-000000000003',1,'DBS-AGG-HPB',    'HPB bedding stone — rear patio',    10.5222,'T', 1800::bigint, 189400::bigint),
  ('51000000-0000-4000-9000-000000000004',1,'DBS-AGG-34CLEAR','3/4" Clear Stone — trench backfill', 4.1250,'T', 1800::bigint,  74250::bigint),
  ('51000000-0000-4000-9000-000000000005',1,'DBS-AGG-34CLEAR','3/4" Clear Stone — subdivision lot',30.0000,'T', 1800::bigint, 540000::bigint),
  ('51000000-0000-4000-9000-000000000005',2,'DBS-AGG-HPB',    'HPB bedding stone',                 21.5556,'T', 1800::bigint, 388000::bigint),
  ('51000000-0000-4000-9000-000000000006',1,'DBS-AGG-HPB',    'HPB bedding stone — walkway',        8.0556,'T', 1800::bigint, 145000::bigint),
  ('51000000-0000-4000-9000-000000000007',1,'DBS-AGG-34CLEAR','3/4" Clear Stone — laneway rebuild',22.3333,'T', 1800::bigint, 402000::bigint),
  ('51000000-0000-4000-9000-000000000009',1,'DBS-AGG-HPB',    'HPB bedding stone — pool surround', 14.9389,'T', 1800::bigint, 268900::bigint)
) AS l (invoice_id, line_no, sku, description, qty, uom, unit, total)
JOIN products p ON p.sku = l.sku
WHERE EXISTS (SELECT 1 FROM invoices i WHERE i.id = l.invoice_id::uuid)
  AND EXISTS (SELECT 1 FROM uoms u WHERE u.code = l.uom)
ON CONFLICT (invoice_id, line_no) DO NOTHING;

-- The credit memo's single negative line (0146's deferred line-sign trigger requires the sign to
-- match the document).
INSERT INTO invoice_lines (invoice_id, line_no, product_id, description, qty_entered, uom_entered,
                           qty_base, unit_price_cents, price_uom, line_total_cents)
SELECT '51000000-0000-4000-9000-000000000008'::uuid, 1, p.id,
       'Credit — short delivery on INV-2094', -2.3333, 'T', -2.3333, 1800, 'T', -42000
FROM products p WHERE p.sku = 'DBS-AGG-34CLEAR'
  AND EXISTS (SELECT 1 FROM invoices i WHERE i.id = '51000000-0000-4000-9000-000000000008'::uuid)
ON CONFLICT (invoice_id, line_no) DO NOTHING;

-- ── the partial payment behind INV-2094 ────────────────────────────────────────────────────────
-- 312500 + 40625 = 353125 total, 178125 outstanding ⇒ 175000 received. A real payments row with a
-- real allocation, so the detail's "paid to date" figure is a stored fact and not a fixture fiction.
INSERT INTO payments (id, payment_no, customer_id, method, amount_cents, status, received_at,
                      branch_id, received_by, idempotency_key)
SELECT '51000000-0000-4000-9000-0000000000a1'::uuid, 'PAY-DEMO-510-1',
       'c7000000-0000-4000-9000-000000000003'::uuid, 'CHEQUE', 175000, 'RECEIVED',
       now() - interval '30 days', '5d1b3f6e-0000-4000-9000-000000000002'::uuid,
       (SELECT id FROM users WHERE email = 'jordan.blake@dibbits.example'),
       'demo-510-partial-payment'
WHERE EXISTS (SELECT 1 FROM invoices WHERE id = '51000000-0000-4000-9000-000000000002'::uuid)
ON CONFLICT (id) DO NOTHING;

INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, created_at, allocated_by)
SELECT '51000000-0000-4000-9000-0000000000a1'::uuid, '51000000-0000-4000-9000-000000000002'::uuid,
       175000, now() - interval '30 days',
       (SELECT id FROM users WHERE email = 'jordan.blake@dibbits.example')
WHERE EXISTS (SELECT 1 FROM payments WHERE id = '51000000-0000-4000-9000-0000000000a1'::uuid)
ON CONFLICT (payment_id, invoice_id) DO NOTHING;

-- ── the order behind INV-2117, and its delivery ────────────────────────────────────────────────
-- The register's Order/PO column and the detail's proof strip both need a real order with a real
-- customer PO and a named salesperson, and a delivery stop that was actually signed for.
INSERT INTO orders (id, order_no, customer_id, branch_id, status, fulfillment, requested_date,
                    subtotal_cents, tax_cents, total_cents, po_job_ref, salesperson_id)
SELECT '51000000-0000-4000-9000-0000000000b1'::uuid, 'SO-4471',
       'c7000000-0000-4000-9000-000000000001'::uuid, '5d1b3f6e-0000-4000-9000-000000000001'::uuid,
       'INVOICED', 'DELIVERY', CURRENT_DATE - 44, 486000, 63180, 549180, 'PO 4471 — Wallbridge',
       (SELECT id FROM users WHERE email = 'sam.rivera@dibbits.example')
WHERE EXISTS (SELECT 1 FROM customers WHERE id = 'c7000000-0000-4000-9000-000000000001'::uuid)
ON CONFLICT (id) DO NOTHING;

UPDATE invoices SET order_id = '51000000-0000-4000-9000-0000000000b1'::uuid
 WHERE id = '51000000-0000-4000-9000-000000000001'::uuid
   AND order_id IS NULL
   AND EXISTS (SELECT 1 FROM orders WHERE id = '51000000-0000-4000-9000-0000000000b1'::uuid);

INSERT INTO delivery_stops (id, branch_id, order_id, scheduled_date, status, stop_no,
                            contact_name, contact_phone, address_line, city, postal_code,
                            pod_signature_ref, pod_received_by, pod_at, pod_by,
                            delivered_at, handoff_at)
SELECT '51000000-0000-4000-9000-0000000000c1'::uuid,
       '5d1b3f6e-0000-4000-9000-000000000001'::uuid,
       '51000000-0000-4000-9000-0000000000b1'::uuid,
       CURRENT_DATE - 43, 'DELIVERED', 1,
       'Mark Scrubname0021', '613-555-0101', '1187 Wallbridge–Loyalist Rd', 'Belleville', 'K8N 4Z5',
       'demo-510-pod-ref', 'Mark Scrubname0021', now() - (43 || ' days')::interval,
       (SELECT id FROM users WHERE email = 'casey.doyle@dibbits.example'),
       now() - (43 || ' days')::interval, now() - (43 || ' days')::interval
WHERE EXISTS (SELECT 1 FROM orders WHERE id = '51000000-0000-4000-9000-0000000000b1'::uuid)
ON CONFLICT (id) DO NOTHING;

-- Two delivery photographs. Each is a real, tiny PNG (a 1×1 pixel) so the byte route and the
-- size CHECK are genuinely exercised rather than mocked — the point of the strip is that the
-- photo comes from the same endpoint the driver uploaded to.
INSERT INTO delivery_pod_photos (id, stop_id, branch_id, content_type, byte_size, image_bytes, caption, captured_at)
SELECT v.id::uuid, '51000000-0000-4000-9000-0000000000c1'::uuid,
       '5d1b3f6e-0000-4000-9000-000000000001'::uuid, 'image/png',
       octet_length(decode('89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c4890000000d4944415478da63f8cfc0f01f0005fb02fe8e9b4a8b0000000049454e44ae426082','hex')),
       decode('89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c4890000000d4944415478da63f8cfc0f01f0005fb02fe8e9b4a8b0000000049454e44ae426082','hex'),
       v.caption, now() - (43 || ' days')::interval
FROM (VALUES
  ('51000000-0000-4000-9000-0000000000d1','Load dropped at the north side of the pad'),
  ('51000000-0000-4000-9000-0000000000d2','Signed ticket on the tailgate')
) AS v (id, caption)
WHERE EXISTS (SELECT 1 FROM delivery_stops WHERE id = '51000000-0000-4000-9000-0000000000c1'::uuid)
ON CONFLICT (id) DO NOTHING;

-- ── the collections state ──────────────────────────────────────────────────────────────────────
-- speaker-0004 is 91 days out on INV-2019 and on hold for it, with a promise recorded against
-- the document. That combination is the whole reason the screen exists: the account is stopped, the
-- customer has committed to a date, and the next person to pick up the phone can see both.
INSERT INTO credit_holds (id, customer_id, status, reason, hold_kind, auto_placed, placed_by, created_at)
SELECT '51000000-0000-4000-9000-0000000000e1'::uuid, 'c7000000-0000-4000-9000-000000000001'::uuid,
       'ACTIVE', 'INV-2019 is 91 days past due', 'AR_AGING', false,
       (SELECT id FROM users WHERE email = 'jordan.blake@dibbits.example'), now() - interval '9 days'
WHERE EXISTS (SELECT 1 FROM invoices WHERE id = '51000000-0000-4000-9000-000000000007'::uuid)
  AND NOT EXISTS (SELECT 1 FROM credit_holds h
                   WHERE h.customer_id = 'c7000000-0000-4000-9000-000000000001'::uuid AND h.status = 'ACTIVE')
ON CONFLICT (id) DO NOTHING;

INSERT INTO ar_promises_to_pay (id, invoice_id, customer_id, promised_cents, promised_date,
                                status, promised_by, note, taken_by, taken_at, idempotency_key)
SELECT '51000000-0000-4000-9000-0000000000f1'::uuid, '51000000-0000-4000-9000-000000000007'::uuid,
       'c7000000-0000-4000-9000-000000000001'::uuid, 250000, CURRENT_DATE + 6,
       'OPEN', 'Mark Scrubname0021', 'Part payment first, balance once the Wallbridge draw clears.',
       (SELECT id FROM users WHERE email = 'jordan.blake@dibbits.example'), now() - interval '2 days',
       'demo-510-promise'
WHERE EXISTS (SELECT 1 FROM invoices WHERE id = '51000000-0000-4000-9000-000000000007'::uuid)
ON CONFLICT (id) DO NOTHING;

-- Frontenac is arguing about INV-2061 rather than ignoring it. The chip counts this, and the
-- collections tone should know about it.
INSERT INTO ar_invoice_disputes (id, invoice_id, customer_id, status, reason, note, amount_cents,
                                 raised_by, raised_at, idempotency_key)
SELECT '51000000-0000-4000-9000-0000000000f2'::uuid, '51000000-0000-4000-9000-000000000005'::uuid,
       'c7000000-0000-4000-9000-000000000005'::uuid, 'OPEN', 'SHORT_DELIVERY',
       'Two loads short on the subdivision lot; site super has the ticket.', 180000,
       (SELECT id FROM users WHERE email = 'jordan.blake@dibbits.example'), now() - interval '5 days',
       'demo-510-dispute'
WHERE EXISTS (SELECT 1 FROM invoices WHERE id = '51000000-0000-4000-9000-000000000005'::uuid)
ON CONFLICT (id) DO NOTHING;

-- ── what we have sent, and what was opened ─────────────────────────────────────────────────────
-- INV-2117 was emailed and read. INV-2061 was emailed and never opened — which is why the register
-- warns about it rather than assuming the customer is stalling. INV-2019 has had one FIRM chase.
INSERT INTO ar_invoice_deliveries (id, invoice_id, customer_id, invoice_no, kind, tone, channel,
                                   to_address, balance_cents, idempotency_key, sent_by, sent_at,
                                   opened_at, open_count)
SELECT v.id::uuid, v.invoice_id::uuid, v.customer_id::uuid, v.invoice_no, v.kind, v.tone, 'EMAIL',
       v.to_address, v.balance, v.key,
       (SELECT id FROM users WHERE email = 'jordan.blake@dibbits.example'),
       now() - (v.sent_ago || ' days')::interval,
       CASE WHEN v.opened_ago IS NULL THEN NULL ELSE now() - (v.opened_ago || ' days')::interval END,
       CASE WHEN v.opened_ago IS NULL THEN 0 ELSE 2 END
FROM (VALUES
  ('51000000-0000-4000-9000-00000000aa01','51000000-0000-4000-9000-000000000001','c7000000-0000-4000-9000-000000000001','INV-2117','INVOICE', '', 'mark@quintehardscapes.example',   549180::bigint,'demo-510-send-1',42, 41),
  ('51000000-0000-4000-9000-00000000aa02','51000000-0000-4000-9000-000000000005','c7000000-0000-4000-9000-000000000005','INV-2061','INVOICE', '', 'priya@frontenacbuilders.example',1048640::bigint,'demo-510-send-2',113, NULL),
  ('51000000-0000-4000-9000-00000000aa03','51000000-0000-4000-9000-000000000007','c7000000-0000-4000-9000-000000000001','INV-2019','REMINDER','FIRM','mark@quintehardscapes.example', 454260::bigint,'demo-510-send-3',11, 10)
) AS v (id, invoice_id, customer_id, invoice_no, kind, tone, to_address, balance, key, sent_ago, opened_ago)
WHERE EXISTS (SELECT 1 FROM invoices i WHERE i.id = v.invoice_id::uuid)
ON CONFLICT (id) DO NOTHING;
