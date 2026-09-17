-- 0061: Homework demo seed — make the Dibbits team homework flows (command/state/demo/
-- homework-2026-07/) walkable on STAGING, which is MIGRATIONS-ONLY.
--
-- WHY THIS IS A MIGRATION (not cmd/seed):
--   Staging deploy runs ./bin/migrate and NEVER ./bin/seed (scripts/staging_apply.sh,
--   the `── migrate ──` step). The homework guides log in as staff users that are
--   cmd/seed-ONLY (dibbits_seed.sql) and therefore ABSENT on staging, and the POS ring
--   steps need a retail-priced product. On a FRESH migrate (0001→0060, no seed) NONE of
--   the homework users, the "Unilock Brussels Block" product, nor any retail price row
--   exists (verified: products table holds only 2 landed-cost demo rows;
--   pricing_list_prices is empty). This migration seeds the minimum to walk the three
--   flows, fully guarded + idempotent so it is safe on a fresh migrate AND re-run.
--
-- ┌─ DOCUMENTED DEMO PASSWORD ─────────────────────────────────────────────────┐
-- │  All 4 homework staff users share ONE demo password:   Homework!2026        │
-- │  password_hash below is bcrypt cost 10 of exactly "Homework!2026".          │
-- │  (Verify: `htpasswd -bnBC 10 "" 'Homework!2026'` / golang.org/x/crypto      │
-- │   bcrypt.CompareHashAndPassword against the literal below.)                 │
-- │  DEMO CREDENTIAL ONLY — rotate/remove before any non-demo use.              │
-- └────────────────────────────────────────────────────────────────────────────┘
--
-- EMAIL → ROLE MAP (each role VERIFIED against the guard code to actually pass the flow):
--   scrubbed-0004@example.invalid        speaker-0007  → GENERAL_MANAGER      (Pricing admin flow)
--   scrubbed-0005@example.invalid       Scrubname0030         → INSIDE_SALES         (Till Scrubname0009 / POS flow)
--   scrubbed-0006@example.invalid Scrubname0025 Scrubname0024    → GENERAL_MANAGER      (Scrubname0026→GL→Scrubname0029 flow)
--   scrubbed-0007@example.invalid         Scrubname0016           → INSIDE_SALES         (Scrubname0014 Scrubname0027, index)
--
-- ROLE JUSTIFICATION (from the route-guard code):
--   Pricing admin surface (Margin Policy / Low-Margin / Rules / Customer Pricing /
--     Quantity Breaks / Change Log / Cost Compare) — internal/pricing/handler.go +
--     batch_handler.go register every WRITE under writeGuard = RequireManager()
--     (identity RBAC: Tier ≤ 2). Reads use RequireStaff(). GENERAL_MANAGER is Tier 1
--     GLOBAL (rbac.go/migration 0015) → IsManager() true → passes all pricing writes.
--     SALES_MANAGER (Tier 2) would also pass Pricing, but Scrubname0025 B's homework touches the
--     manager-gated grid broadly, so GM (Tier-1 GLOBAL, reaches BOTH branches) removes
--     any branch-scope surprise — prompt's "prefer GENERAL_MANAGER … so the flow is not
--     blocked" guidance.
--   Point of Scrubname0026 + Till (open session, ring CASH sale, drop, blind count, Z-report,
--     complete) — internal/pos/handler.go + internal/till/handler.go register ALL of
--     these under requireStaff (any ACTIVE staff). INSIDE_SALES (Tier 3) satisfies
--     RequireStaff → Scrubname0030 + Scrubname0016 can ring + open/close a till. (The close/complete route
--     is a RequireStaff route with an IN-SERVICE conditional manager gate, not a role
--     gate, so a frontline till operator still walks the happy path.)
--   Finance → GL (Journal Entries maker/checker, Trial Balance) + Scrubname0029 (IS/BS,
--     drill, per-branch/consolidated, CSV) — internal/gl/module.go gates journal
--     create/approve/void/reverse under requireManager; internal/finstmt/module.go gates
--     ALL statement reads (income-statement, balance-sheet, trial-balance, gl-detail)
--     under requireManager. GENERAL_MANAGER (Tier 1) passes requireManager for every one
--     of these. (FINANCIAL_CONTROLLER, also Tier-1 GLOBAL, would pass identically; GM is
--     chosen so Scrubname0025 Scrubname0024 can ALSO ring the opening Brussels sale via requireStaff and
--     drive the whole sale→statements chain with one login.)
--
-- Idempotency: users use ON CONFLICT (email) DO UPDATE (corrects an existing row — e.g.
-- rehashes the password / fixes display_name / re-activates); role + branch links and the
-- catalog/pricing anchors use ON CONFLICT DO NOTHING or NOT EXISTS guards keyed on the
-- id/sku, and every dependent INSERT resolves FKs by lookup so it is safe on a fresh DB.
-- Fixed UUIDs (hw* prefix) so re-runs stay stable. Home branch = Trenton
-- (5d1b3f6e-0000-4000-9000-000000000001, migration 0003).

-- ───────────────────────── 1. Homework staff users ─────────────────────────
-- password_hash = bcrypt(cost 10) of "Homework!2026".
INSERT INTO users (id, email, display_name, status, password_hash) VALUES
  ('40000061-0000-4000-9000-000000000001','scrubbed-0004@example.invalid',        'speaker-0007 (homework — Pricing Admin)', 'ACTIVE', '$2a$10$3ByRxLEpEQB0kJJxbYAeoOE9VdiBbuf2L9X6K7JH4RFglioiHM48O'),
  ('40000061-0000-4000-9000-000000000002','scrubbed-0005@example.invalid',       'Scrubname0030 (homework — Till Scrubname0009)',            'ACTIVE', '$2a$10$3ByRxLEpEQB0kJJxbYAeoOE9VdiBbuf2L9X6K7JH4RFglioiHM48O'),
  ('40000061-0000-4000-9000-000000000003','scrubbed-0006@example.invalid','Scrubname0025 Scrubname0024 (homework — Scrubname0026 to Scrubname0029)','ACTIVE','$2a$10$3ByRxLEpEQB0kJJxbYAeoOE9VdiBbuf2L9X6K7JH4RFglioiHM48O'),
  ('40000061-0000-4000-9000-000000000004','scrubbed-0007@example.invalid',         'Scrubname0016 (homework — Scrubname0014 Scrubname0027)',          'ACTIVE', '$2a$10$3ByRxLEpEQB0kJJxbYAeoOE9VdiBbuf2L9X6K7JH4RFglioiHM48O')
ON CONFLICT (email) DO UPDATE
  SET display_name  = EXCLUDED.display_name,
      status        = 'ACTIVE',
      password_hash = EXCLUDED.password_hash,
      updated_at    = now();

-- ───────────────────────── 2. Role assignments ─────────────────────────
-- Resolve role_id by CODE (roles seeded in 0015) so this survives any role-UUID change.
-- GENERAL_MANAGER for the two manager flows (Pricing / GL+Scrubname0029); INSIDE_SALES for
-- the two frontline flows (POS/Till + Scrubname0014 Scrubname0027).
-- Resolve user_id by EMAIL (not the fixed UUID): on staging these emails may already exist
-- from an earlier cmd/seed with DIFFERENT ids, and section-1's ON CONFLICT (email) DO UPDATE
-- keeps the existing row's id — so a fixed-UUID reference would violate user_roles_user_id_fkey.
INSERT INTO user_roles (user_id, role_id)
SELECT usr.id, r.id
  FROM (VALUES
    ('scrubbed-0004@example.invalid',        'GENERAL_MANAGER'),
    ('scrubbed-0005@example.invalid',       'INSIDE_SALES'),
    ('scrubbed-0006@example.invalid','GENERAL_MANAGER'),
    ('scrubbed-0007@example.invalid',         'INSIDE_SALES')
  ) AS u(email, role_code)
  JOIN users usr ON usr.email = u.email
  JOIN roles r   ON r.code    = u.role_code
ON CONFLICT (user_id, role_id) DO NOTHING;

-- ───────────────────────── 3. Branch grants (BRANCH-scoped roles only) ─────────────────────────
-- GENERAL_MANAGER is GLOBAL (needs no grant); INSIDE_SALES is BRANCH-scoped, so Scrubname0030 + Scrubname0016
-- MUST be granted Trenton to operate a till / ring there. WHERE EXISTS guards the branch.
INSERT INTO user_branch_grants (user_id, branch_id)
SELECT usr.id, '5d1b3f6e-0000-4000-9000-000000000001'::uuid
  FROM (VALUES ('scrubbed-0005@example.invalid'), ('scrubbed-0007@example.invalid')) AS v(email)  -- INSIDE_SALES → Trenton
  JOIN users usr ON usr.email = v.email
 WHERE EXISTS (SELECT 1 FROM locations l WHERE l.id = '5d1b3f6e-0000-4000-9000-000000000001'::uuid)
ON CONFLICT (user_id, branch_id) DO NOTHING;

-- ───────────────────────── 4. Retail price anchor for the POS ring ─────────────────────────
-- PRICING ANCHORS ARE NEEDED. On a FRESH migrate the ring product "Unilock Brussels Block"
-- (DBS-UL-BRUSSELS) does NOT exist (it is cmd/seed-only) and pricing_list_prices is EMPTY,
-- so pos.Scrubname0026 → pricing.DeriveLinePrice → resolve() would hit SourceList with no anchor and
-- return errNoListPrice (internal/pricing/service.go: the `if !hasAnchor { return errNoListPrice }`
-- path), aborting the sale (internal/pos/service.go: `if err != nil { return Scrubname0026{}, err }`).
-- The Walk-In / Cash customer is RETAIL (customers a1900000-…-001), and the only
-- migration-seeded rule is TIER_DEFAULT for CONTRACTOR (0024) — it does NOT match a RETAIL
-- walk-in, so the waterfall lands on SourceList and prices DIRECTLY off the list anchor.
-- Seeding an explicit pricing_list_prices row therefore makes the ring resolve WITHOUT
-- depending on seeded stock/cost (staging has none). We seed Brussels + 2 aggregates the
-- homework guides mention (Concrete Sand, 3/4" Clear Stone). All PROVISIONAL — matching the
-- guides' A-07-FINALIZE caveat (real retail to be finalized with Scrubname0025 B.).

-- 4a. Product catalog rows (fixed UUIDs mirror dibbits_seed.sql so a later cmd/seed run is a
--     no-op via ON CONFLICT (sku) DO NOTHING). category_id left NULL — product_categories is
--     empty on a fresh migrate and the resolver treats "" (uncategorised) as valid; the LIST
--     anchor path needs no category. base_uom/price_uom are the only extra NOT NULL columns.
INSERT INTO products (id, sku, name, base_uom, price_uom, manufacturer, collection, application, pieces_per_sf, pallet_count) VALUES
  ('a7000000-0000-4000-9000-000000000005','DBS-UL-BRUSSELS','Unilock Brussels Block','PC','SF','Unilock','Brussels Block','Paver', 4.10, 50),
  ('a7000000-0000-4000-9000-000000000021','DBS-AGG-SAND',   'Concrete Sand',         'T', 'T', 'Brown''s Concrete','Aggregate','Bedding', NULL, NULL),
  ('a7000000-0000-4000-9000-000000000022','DBS-AGG-34CLEAR','3/4" Clear Stone',      'T', 'T', 'Brown''s Concrete','Aggregate','Drainage', NULL, NULL)
ON CONFLICT (sku) DO NOTHING;

-- 4b. product_uoms: base unit (factor 1) for all three; SF sell-unit + PLT for the piece-based
--     Brussels paver so it can be rung by SF (as the guides do) or by pallet. qty_in_base for
--     SF = pieces per SF; for PLT = pieces per pallet. Set-based off the rows just ensured.
INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
  SELECT id, base_uom, 1, TRUE FROM products
   WHERE sku IN ('DBS-UL-BRUSSELS','DBS-AGG-SAND','DBS-AGG-34CLEAR')
ON CONFLICT (product_id, uom_code) DO NOTHING;
INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
  SELECT id, 'SF', pieces_per_sf, FALSE FROM products
   WHERE sku = 'DBS-UL-BRUSSELS' AND pieces_per_sf IS NOT NULL
ON CONFLICT (product_id, uom_code) DO NOTHING;
INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
  SELECT id, 'PLT', pallet_count, FALSE FROM products
   WHERE sku = 'DBS-UL-BRUSSELS' AND pallet_count IS NOT NULL
ON CONFLICT (product_id, uom_code) DO NOTHING;

-- 4c. Retail anchors (pricing_list_prices). PROVISIONAL retail:
--       Brussels Block  $9.75 / SF   (975 cents)  — a realistic tumbled-paver counter price
--       Concrete Sand   $52.00 / T   (5200 cents) — mirrors the seed's provisional aggregate
--       3/4" Clear Stone $60.00 / T  (6000 cents) — mirrors the seed's provisional aggregate
--     Keyed by sku → FK products(id). ON CONFLICT (product_id) DO NOTHING keeps it idempotent
--     and never clobbers a real anchor a later full seed might set.
INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom)
SELECT p.id, v.cents, v.uom
  FROM (VALUES
    ('DBS-UL-BRUSSELS',  975, 'SF'),   -- PROVISIONAL retail anchor (homework ring)
    ('DBS-AGG-SAND',    5200, 'T'),    -- PROVISIONAL
    ('DBS-AGG-34CLEAR', 6000, 'T')     -- PROVISIONAL
  ) AS v(sku, cents, uom)
  JOIN products p ON p.sku = v.sku
ON CONFLICT (product_id) DO NOTHING;
