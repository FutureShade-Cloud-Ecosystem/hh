-- M7 Dibbits demo seed (W1). Loaded by cmd/seed (prod-guarded, idempotent).
-- Real Dibbits suppliers (the Dibbits supplier list) + a representative hardscape catalogue.
-- All SKUs prefixed 'DBS-' (greppable + the empty-DB guard keys on it).
-- Fixed UUIDs for determinism. Money = integer cents CAD. Every qty carries a UOM.
-- Branches (fixed, from 0003): TRT 5d1b3f6e-0000-4000-9000-000000000001 · KGN ...002

-- ───────────────────────── demo user (assignable in A-20) ─────────────────────────
-- password_hash is bcrypt("dibbits-demo-2026") for every ACTIVE demo staffer, so the
-- C-1 staff login works out of the box and the UI can "log in as" each role to show
-- enforcement live. Demo only, never a real credential. The INVITED new hire has no
-- password (cannot log in until provisioned), proving the status gate.
INSERT INTO users (id, email, display_name, status, password_hash) VALUES
  ('d7000000-0000-4000-9000-000000000001', 'demo@dibbits.local', 'Demo Operator', 'ACTIVE', '$2a$10$ja7jD99TS1QsOtU.7dVDIu.HwgOK2pBERc60C4IyjMEi49IvR/dGO'),
  ('d7000000-0000-4000-9000-000000000002', 'alex.morgan@dibbits.example',  'General manager (sample)',   'ACTIVE', '$2a$10$ja7jD99TS1QsOtU.7dVDIu.HwgOK2pBERc60C4IyjMEi49IvR/dGO'),
  ('d7000000-0000-4000-9000-000000000003', 'sam.rivera@dibbits.example',   'Sales manager (sample)',     'ACTIVE', '$2a$10$ja7jD99TS1QsOtU.7dVDIu.HwgOK2pBERc60C4IyjMEi49IvR/dGO'),
  ('d7000000-0000-4000-9000-000000000005', 'casey.doyle@dibbits.example',  'Yard manager (sample)',      'ACTIVE', '$2a$10$ja7jD99TS1QsOtU.7dVDIu.HwgOK2pBERc60C4IyjMEi49IvR/dGO'),
  ('d7000000-0000-4000-9000-000000000006', 'jordan.blake@dibbits.example', 'Financial controller (sample)', 'ACTIVE', '$2a$10$ja7jD99TS1QsOtU.7dVDIu.HwgOK2pBERc60C4IyjMEi49IvR/dGO'),
  ('d7000000-0000-4000-9000-000000000007', 'new.hire@dibbits.example',     'New Hire (sample)',     'INVITED', NULL),
  -- Platform owner (SUPER_ADMIN, migration 0043; password reset by migration 0044 -
  -- a throwaway bootstrap credential, not a real password; see 0044's comment).
  -- Real account, not a "(sample)", mirrored here so a fresh `make reset` DB carries it
  -- too; the staging DB gets it from the migrations.
  ('d7000000-0000-4000-9000-000000000010', 'colton@futurebuild.ai',        'Platform admin (sample)', 'ACTIVE',
   '$2a$10$n4gHBpnG/4nV9G2tu40QiOnfcPOryTWh3dzRYi5hdSzMgyMsXzLG6')
ON CONFLICT (email) DO NOTHING;

-- demo role assignments (so the Staff & Roles screen opens with real people; New Hire left unassigned)
INSERT INTO user_roles (user_id, role_id) VALUES
  ('d7000000-0000-4000-9000-000000000002','e7000000-0000-4000-9000-000000000001'), -- General manager login → General Manager
  ('d7000000-0000-4000-9000-000000000006','e7000000-0000-4000-9000-000000000002'), -- Financial controller login → Financial Controller
  ('d7000000-0000-4000-9000-000000000003','e7000000-0000-4000-9000-000000000006'), -- Sales manager login → Sales Manager
  ('d7000000-0000-4000-9000-000000000005','e7000000-0000-4000-9000-000000000007'), -- Yard manager login → Yard Manager
  ('d7000000-0000-4000-9000-000000000010','e7000000-0000-4000-9000-000000000014')  -- Platform admin login → Platform Administrator (SUPER_ADMIN)
ON CONFLICT (user_id, role_id) DO NOTHING;

INSERT INTO user_branch_grants (user_id, branch_id) VALUES
  ('d7000000-0000-4000-9000-000000000003','5d1b3f6e-0000-4000-9000-000000000001'), -- Sales manager login → Trenton
  ('d7000000-0000-4000-9000-000000000005','5d1b3f6e-0000-4000-9000-000000000002')  -- Yard manager login → Kingston
ON CONFLICT (user_id, branch_id) DO NOTHING;

-- DIB-378/C1-b: migration 0052 seeds the general manager ↔ sales manager and sales manager ↔ yard manager DM channels + their EXPLICIT
-- msg_members in ONE migrate-time statement that JOINs the demo staff by fixed id, but those users
-- are created by THIS file, not by any migration, so on a migrate-then-seed DB (dev/UAT, never
-- staging, which runs migrations only) that JOIN matches nothing and both DMs land with zero members
-- (TestIntegration_DMExplicitMembership fails: "the general manager should be in the general manager ↔ sales manager DM"). Backfill the
-- same membership here, now that the users above are guaranteed to exist. 0052 itself is untouched -
-- it is correct as written for staging. Idempotent (ON CONFLICT DO NOTHING); guarded on msg_channels
-- so this no-ops harmlessly if 0052 hasn't run.
INSERT INTO msg_members (channel_id, user_id, role, source)
SELECT c.id, u.id, 'MEMBER', 'EXPLICIT'
  FROM (VALUES
    ('a5000000-0000-4000-9000-0000000000d1'::uuid, 'd7000000-0000-4000-9000-000000000002'::uuid), -- GM ↔ sales manager DM: general manager
    ('a5000000-0000-4000-9000-0000000000d1'::uuid, 'd7000000-0000-4000-9000-000000000003'::uuid), -- GM ↔ sales manager DM: sales manager
    ('a5000000-0000-4000-9000-0000000000d2'::uuid, 'd7000000-0000-4000-9000-000000000003'::uuid), -- sales manager ↔ yard manager DM: sales manager
    ('a5000000-0000-4000-9000-0000000000d2'::uuid, 'd7000000-0000-4000-9000-000000000005'::uuid)   -- sales manager ↔ yard manager DM: yard manager
  ) AS c(id, uid)
  JOIN msg_channels ch ON ch.id = c.id
  JOIN users u ON u.id = c.uid
ON CONFLICT (channel_id, user_id) DO NOTHING;

-- ───────────────────────── categories ─────────────────────────
INSERT INTO product_categories (id, code, name) VALUES
  ('ca700000-0000-4000-9000-000000000001', 'PAVER',     'Pavers'),
  ('ca700000-0000-4000-9000-000000000002', 'WALL',      'Retaining Walls'),
  ('ca700000-0000-4000-9000-000000000003', 'SLABSTEP',  'Slabs & Steps'),
  ('ca700000-0000-4000-9000-000000000004', 'AGGREGATE', 'Aggregates'),
  ('ca700000-0000-4000-9000-000000000005', 'ACCESSORY', 'Accessories'),
  ('ca700000-0000-4000-9000-000000000006', 'BULK',      'Bulk')             -- DIB-260: first-class front-desk bulk category
ON CONFLICT (code) DO NOTHING;

-- ───────────────────────── products (~30, real manufacturers) ─────────────────────────
-- base_uom = stocking unit; price_uom = how it's sold. pieces_per_sf / pallet_count drive UoM rows below.
-- pieces_per_sf is pieces per SQUARE FOOT (92903.04 sq mm per SF) over the sold face of dimensions_lwh:
-- length x width for pavers and slabs, length x HEIGHT for a wall (application 'Wall', the width is the
-- depth into the wall). Never per square metre: a 600x600 slab is 0.2581, not 2.78 (HH-207).
INSERT INTO products (id, sku, name, category_id, base_uom, price_uom, manufacturer, collection, color, finish, application, dimensions_lwh, pallet_count, weight_per_unit_kg, pieces_per_sf) VALUES
 ('a7000000-0000-4000-9000-000000000001','DBS-TB-BLU60','Techo-Bloc Blu 60 Slab','ca700000-0000-4000-9000-000000000003','PC','SF','Techo Bloc','Blu','Greyed Nickel','Smooth','Patio','600x600x60',40,18.0,0.2581),
 ('a7000000-0000-4000-9000-000000000002','DBS-TB-ABERDEEN','Techo-Bloc Aberdeen Wall','ca700000-0000-4000-9000-000000000002','PC','SF','Techo Bloc','Aberdeen','Shale Grey','Tumbled','Wall','280x150x100',60,16.5,3.3180),
 ('a7000000-0000-4000-9000-000000000003','DBS-TB-BOREALIS','Techo-Bloc Borealis Step','ca700000-0000-4000-9000-000000000003','PC','EA','Techo Bloc','Borealis','Onyx Black','Textured','Step','1219x356x152',12,95.0,NULL),
 ('a7000000-0000-4000-9000-000000000004','DBS-TB-MINICRETA','Techo-Bloc Mini-Creta Wall','ca700000-0000-4000-9000-000000000002','PC','SF','Techo Bloc','Mini-Creta','Chocolate Brown','Tumbled','Wall','200x150x100',72,8.2,4.6452),
 ('a7000000-0000-4000-9000-000000000005','DBS-UL-BRUSSELS','Unilock Brussels Block','ca700000-0000-4000-9000-000000000001','PC','SF','Unilock','Brussels Block','Sierra','Tumbled','Paver','200x150x60',50,4.1,4.10),
 ('a7000000-0000-4000-9000-000000000006','DBS-UL-ILCAMPO','Unilock Il Campo Paver','ca700000-0000-4000-9000-000000000001','PC','SF','Unilock','Il Campo','Granite','Antiqued','Paver','300x300x60',45,9.8,1.0323),
 ('a7000000-0000-4000-9000-000000000007','DBS-UL-SERIES3000','Unilock Series 3000','ca700000-0000-4000-9000-000000000001','PC','SF','Unilock','Series 3000','Natural','Smooth','Paver','300x150x80',40,7.2,2.0645),
 ('a7000000-0000-4000-9000-000000000008','DBS-PM-MELVILLE','Permacon Melville Slab','ca700000-0000-4000-9000-000000000003','PC','SF','Permacon','Melville','Sand','Smooth','Patio','600x400x50',48,16.0,0.3871),
 ('a7000000-0000-4000-9000-000000000009','DBS-PM-LAFITT','Permacon Lafitt Paver','ca700000-0000-4000-9000-000000000001','PC','SF','Permacon','Lafitt','Range Grey','Textured','Paver','400x200x50',55,7.5,1.1613),
 ('a7000000-0000-4000-9000-000000000010','DBS-PM-MAGNUMSTONE','Permacon Magnumstone Wall','ca700000-0000-4000-9000-000000000002','PC','SF','Permacon','Magnumstone','Beige','Rock-face','Wall','450x300x200',16,38.0,1.0323),
 ('a7000000-0000-4000-9000-000000000011','DBS-OAKS-VINTAGE','Oaks Vintage Paver','ca700000-0000-4000-9000-000000000001','PC','SF','Oaks','Vintage','Burnt Sienna','Tumbled','Paver','230x153x60',48,4.4,2.6400),
 ('a7000000-0000-4000-9000-000000000012','DBS-OAKS-PROMA','Oaks Proma Wall','ca700000-0000-4000-9000-000000000002','PC','SF','Oaks','Proma','Charcoal','Smooth','Wall','290x200x150',54,12.0,2.1357),
 ('a7000000-0000-4000-9000-000000000013','DBS-BWS-ANTICO','Best Way Stone Antico Paver','ca700000-0000-4000-9000-000000000001','PC','SF','Best Way Stone','Antico','Beige Cream','Tumbled','Paver','210x140x60',50,3.9,3.1600),
 ('a7000000-0000-4000-9000-000000000014','DBS-BWS-TREVISTA','Best Way Stone Trevista Slab','ca700000-0000-4000-9000-000000000003','PC','SF','Best Way Stone','Trevista','Grey','Smooth','Patio','600x300x50',40,21.0,0.5161),
 ('a7000000-0000-4000-9000-000000000015','DBS-BANAS-COPING','Banas Sandstone Coping','ca700000-0000-4000-9000-000000000003','PC','LF','Banas Stone','Sandstone','Autumn Brown','Natural','Coping','900x300x50',30,18.0,NULL),
 ('a7000000-0000-4000-9000-000000000016','DBS-BANAS-STEP','Banas Natural Step','ca700000-0000-4000-9000-000000000003','PC','EA','Banas Stone','Natural','Raj Green','Natural','Step','1200x350x150',8,140.0,NULL),
 ('a7000000-0000-4000-9000-000000000017','DBS-PORCEA-2424','Porcea Porcelain Paver 24x24','ca700000-0000-4000-9000-000000000001','PC','SF','Porcea','Stonika','Cendre','Matte','Paver','600x600x20',40,16.0,0.2581),
 ('a7000000-0000-4000-9000-000000000018','DBS-PORCEA-PLANK','Porcea Porcelain Plank','ca700000-0000-4000-9000-000000000001','PC','SF','Porcea','Stonika','Noce','Wood-look','Paver','1200x300x20',32,11.0,0.2581),
-- UNION TRAIN 14: the two by-the-tonne aggregates below are filed application='Aggregate',
-- collection=<size>, NOT application=<use>. The counter hot list drills family -> size, so filing a
-- headline bulk line under its USE ('Bedding', 'Base') puts it on the board as its own one-item root
-- tile instead of under Aggregate. Migration 0186 re-files these two on any database that already
-- has them (staging), but a migration cannot reach rows the seed has not created yet, so on a fresh
-- `migrate -> seed` the filing has to be right here. 0061's DBS-AGG-SAND / DBS-AGG-34CLEAR need no
-- such change: they exist at migrate time, so 0186's guarded UPDATEs re-file them.
 ('a7000000-0000-4000-9000-000000000019','DBS-AGG-HPB','HPB Clear Stone (High Performance Bedding)','ca700000-0000-4000-9000-000000000004','T','T','Brown''s Concrete','HPB','Grey','Washed','Aggregate','-',NULL,1000.0,NULL),
 ('a7000000-0000-4000-9000-000000000020','DBS-AGG-SCREEN','Limestone Screenings','ca700000-0000-4000-9000-000000000004','T','T','Brown''s Concrete','Screenings','Grey','Crushed','Aggregate','-',NULL,1000.0,NULL),
 ('a7000000-0000-4000-9000-000000000021','DBS-AGG-SAND','Concrete Sand','ca700000-0000-4000-9000-000000000004','T','T','Brown''s Concrete','Aggregate','Tan','Washed','Bedding','-',NULL,1000.0,NULL),
 ('a7000000-0000-4000-9000-000000000022','DBS-AGG-34CLEAR','3/4" Clear Stone','ca700000-0000-4000-9000-000000000004','T','T','Brown''s Concrete','Aggregate','Grey','Crushed','Drainage','-',NULL,1000.0,NULL),
 ('a7000000-0000-4000-9000-000000000023','DBS-GATOR-SAND','Alliance Gator Polymeric Sand','ca700000-0000-4000-9000-000000000005','BAG','BAG','Alliance Gator','Gator','Beige','-','Jointing','-',56,22.7,NULL),
 ('a7000000-0000-4000-9000-000000000024','DBS-SIKA-SEALER','Sika Paver Sealer 4L','ca700000-0000-4000-9000-000000000005','EA','EA','Sika','Sealer','Clear','-','Sealing','-',NULL,4.5,NULL),
 ('a7000000-0000-4000-9000-000000000025','DBS-INLITE-LED','In-Lite LED Path Light','ca700000-0000-4000-9000-000000000005','EA','EA','In-Lite','Lighting','Black','Matte','Lighting','-',NULL,0.6,NULL),
 ('a7000000-0000-4000-9000-000000000026','DBS-RYMAR-TURF','Rymar Artificial Turf 40mm','ca700000-0000-4000-9000-000000000005','SF','SF','Rymar Grass','Turf','Green','-','Landscape','-',NULL,2.2,NULL),
 ('a7000000-0000-4000-9000-000000000027','DBS-GEO-FABRIC','Geotextile Fabric Roll','ca700000-0000-4000-9000-000000000005','EA','EA','Soleno','Geotextile','Black','Woven','Base','-',NULL,15.0,NULL),
 ('a7000000-0000-4000-9000-000000000028','DBS-TB-HYDR','Techo-Bloc Hydr Eco Paver','ca700000-0000-4000-9000-000000000001','PC','SF','Techo Bloc','Hydr','Champlain Grey','Permeable','Paver','225x112x80',44,4.0,3.6866),
 ('a7000000-0000-4000-9000-000000000029','DBS-UL-THORNBURY','Unilock Thornbury Wall','ca700000-0000-4000-9000-000000000002','PC','SF','Unilock','Thornbury','Coffee','Weathered','Wall','280x200x100',48,11.5,3.3180),
 ('a7000000-0000-4000-9000-000000000030','DBS-PM-BORANJA','Permacon Boranja Slab','ca700000-0000-4000-9000-000000000003','PC','SF','Permacon','Boranja','Grey','Smooth','Patio','600x600x60',36,20.0,0.2581)
ON CONFLICT (sku) DO NOTHING;

-- ───────────────────────── product_uoms (derived set-based) ─────────────────────────
-- base unit (qty_in_base = 1)
INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
  SELECT id, base_uom, 1, TRUE FROM products WHERE sku LIKE 'DBS-%'
ON CONFLICT (product_id, uom_code) DO NOTHING;
-- square-foot sell unit for piece-based products (qty_in_base = pieces per SF)
INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
  SELECT id, 'SF', pieces_per_sf, FALSE FROM products
  WHERE sku LIKE 'DBS-%' AND base_uom = 'PC' AND pieces_per_sf IS NOT NULL
ON CONFLICT (product_id, uom_code) DO NOTHING;
-- pallet convenience unit where a pallet count exists (qty_in_base = pieces per pallet)
INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
  SELECT id, 'PLT', pallet_count, FALSE FROM products
  WHERE sku LIKE 'DBS-%' AND base_uom = 'PC' AND pallet_count IS NOT NULL
ON CONFLICT (product_id, uom_code) DO NOTHING;
-- weight (KG) unit for the natural-stone step, so the A-07 partial-skid WEIGHT surcharge below can
-- bucket by load weight (1 piece = weight_per_unit_kg). Catalog-owned UOM data, additive, scoped to
-- the one heavy natural-stone product the 06-23 surcharge rule actually targets.
INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
  SELECT id, 'KG', 1.0 / weight_per_unit_kg, FALSE FROM products
  WHERE sku = 'DBS-BANAS-STEP' AND weight_per_unit_kg IS NOT NULL
ON CONFLICT (product_id, uom_code) DO NOTHING;

-- ───────────────────────── product_rounding_rules (derived) ─────────────────────────
INSERT INTO product_rounding_rules (product_id, sell_increment_uom, break_pallet_allowed, break_pallet_fee_cents, waste_factor_pct)
  SELECT id,
         CASE WHEN base_uom = 'PC' THEN price_uom ELSE base_uom END,
         (base_uom = 'PC'),
         CASE WHEN base_uom = 'PC' THEN 2500 ELSE 0 END,
         CASE WHEN category_id = 'ca700000-0000-4000-9000-000000000001' THEN 5.00
              WHEN category_id = 'ca700000-0000-4000-9000-000000000003' THEN 5.00
              ELSE 0 END
  FROM products WHERE sku LIKE 'DBS-%'
ON CONFLICT (product_id) DO NOTHING;

-- ───────────────────────── vendors (30 real Dibbits suppliers) ─────────────────────────
INSERT INTO vendors (vendor_no, name, payment_terms_days, is_edi) VALUES
 ('V-OAKS','Oaks / Brampton Brick',30,FALSE),('V-PERMACON','Permacon / Old Castle',30,FALSE),
 ('V-TECHO','Techo Bloc',30,FALSE),('V-BESTWAY','Best Way Stone',30,FALSE),
 ('V-UNILOCK','Unilock',30,FALSE),('V-BROWNS','Brown''s Concrete',30,FALSE),
 ('V-SOLENO','Soleno',30,FALSE),('V-TITAN','Titan',30,FALSE),
 ('V-GATOR','Alliance Gator',30,FALSE),('V-ALCLI','Alcli',30,FALSE),
 ('V-LINZEL','Linzel',30,FALSE),('V-INLITE','In-Lite',30,FALSE),
 ('V-BRILL','Brilliance',30,FALSE),('V-EUROPA','Europa',30,FALSE),
 ('V-SIKA','Sika',30,FALSE),('V-OXTOOLS','Ox Tools',30,FALSE),
 ('V-RYMAR','Rymar Grass',30,FALSE),('V-IKONIK','Ikonik',30,FALSE),
 ('V-GNH','Great North Hardscapes',30,FALSE),('V-RUBI','Rubi',30,FALSE),
 ('V-BANAS','Banas Stone',30,FALSE),('V-STONEARCH','Stone Arch',30,FALSE),
 ('V-PORCEA','Porcea',30,FALSE),('V-ECORASTER','Ecoraster',30,FALSE),
 ('V-ARMTEC','Armtec',30,FALSE),('V-ROMEX','Romex',30,FALSE),
 ('V-PROBST','Probst',30,FALSE),('V-MQUIP','MQuip',30,FALSE),
 ('V-AQUA','Aquascapes',30,FALSE),('V-DYNA','Dyna',30,FALSE)
ON CONFLICT (vendor_no) DO NOTHING;

-- W6 A-08 (scaffold-sanctioned edit): backfill the internal-only flag on the 30 committed supplier rows
-- (manufacturer names are INTERNAL-only, never surfaced on the portal, rule 1), and seed the 7 manufacturer
-- price-import profiles keyed to the EXISTING V-* vendors above (NO new vendor rows). Both are idempotent.
UPDATE vendors
   SET metadata = jsonb_set(COALESCE(metadata, '{}'::jsonb), '{internal_seed_only}', 'true'::jsonb, true)
 WHERE vendor_no IN (
   'V-OAKS','V-PERMACON','V-TECHO','V-BESTWAY','V-UNILOCK','V-BROWNS','V-SOLENO','V-TITAN','V-GATOR','V-ALCLI',
   'V-LINZEL','V-INLITE','V-BRILL','V-EUROPA','V-SIKA','V-OXTOOLS','V-RYMAR','V-IKONIK','V-GNH','V-RUBI',
   'V-BANAS','V-STONEARCH','V-PORCEA','V-ECORASTER','V-ARMTEC','V-ROMEX','V-PROBST','V-MQUIP','V-AQUA','V-DYNA');

INSERT INTO price_import_profiles (vendor_id, name, mapping)
SELECT v.id,
       v.name || ' price list',
       jsonb_build_object('sku_col','sku','cost_col','cost','retail_col','retail','effective_col','effective_date')
  FROM vendors v
 WHERE v.vendor_no IN ('V-TECHO','V-UNILOCK','V-PERMACON','V-OAKS','V-BESTWAY','V-BANAS','V-PORCEA')
ON CONFLICT (vendor_id, name) DO NOTHING;

-- ───────────────────────── customers (8 across the type enum) + contacts ─────────────────────────
INSERT INTO customers (id, account_no, name, customer_type, home_branch_id, payment_terms_days, credit_limit_cents, billing_address) VALUES
 ('c7000000-0000-4000-9000-000000000001','CUST-1001','Quinte Hardscapes Inc.','CONTRACTOR','5d1b3f6e-0000-4000-9000-000000000001',30,5000000,'{"city":"Trenton","province":"ON","country":"CA"}'),
 ('c7000000-0000-4000-9000-000000000002','CUST-1002','Bay of Quinte Landscaping','CONTRACTOR','5d1b3f6e-0000-4000-9000-000000000001',30,7500000,'{"city":"Belleville","province":"ON","country":"CA"}'),
 ('c7000000-0000-4000-9000-000000000003','CUST-1003','Limestone City Interlock','CONTRACTOR','5d1b3f6e-0000-4000-9000-000000000002',30,4000000,'{"city":"Kingston","province":"ON","country":"CA"}'),
 ('c7000000-0000-4000-9000-000000000004','CUST-1004','Cataraqui Custom Yards','CONTRACTOR','5d1b3f6e-0000-4000-9000-000000000002',30,3000000,'{"city":"Kingston","province":"ON","country":"CA"}'),
 ('c7000000-0000-4000-9000-000000000005','CUST-1005','Frontenac Home Builders','BUILDER','5d1b3f6e-0000-4000-9000-000000000002',45,12000000,'{"city":"Kingston","province":"ON","country":"CA"}'),
 ('c7000000-0000-4000-9000-000000000006','CUST-1006','Walk-In Retail (Trenton)','RETAIL','5d1b3f6e-0000-4000-9000-000000000001',0,0,'{"city":"Trenton","province":"ON","country":"CA"}'),
 ('c7000000-0000-4000-9000-000000000007','CUST-1007','Homeowner, J. Tremblay','RETAIL','5d1b3f6e-0000-4000-9000-000000000001',0,0,'{"city":"Brighton","province":"ON","country":"CA"}'),
 ('c7000000-0000-4000-9000-000000000008','CUST-1008','City of Quinte West','GOVERNMENT','5d1b3f6e-0000-4000-9000-000000000001',60,20000000,'{"city":"Trenton","province":"ON","country":"CA"}')
ON CONFLICT (account_no) DO NOTHING;

INSERT INTO customer_contacts (customer_id, name, email, phone, role, is_primary) VALUES
 ('c7000000-0000-4000-9000-000000000001','Mark Quinte','mark@quintehardscapes.example','613-555-0101','Owner',TRUE),
 ('c7000000-0000-4000-9000-000000000002','Sarah Bayfield','sarah@boqland.example','613-555-0102','Estimator',TRUE),
 ('c7000000-0000-4000-9000-000000000003','Dave Stone','dave@limestoneinterlock.example','613-555-0103','Owner',TRUE),
 ('c7000000-0000-4000-9000-000000000005','Priya Frontenac','priya@frontenacbuilders.example','613-555-0105','Purchasing',TRUE),
 ('c7000000-0000-4000-9000-000000000008','Public Works Desk','works@quintewest.example','613-555-0108','Procurement',TRUE)
ON CONFLICT DO NOTHING;

-- ───────────────────────── portal users (CP-07, bootstrap account admin) ─────────────────────────
-- One contractor account_admin so the React portal has a working login out of the box.
-- Password is "dibbits-demo-2026" (bcrypt). Demo only, never a real credential.
INSERT INTO portal_users (id, customer_id, email, name, password_hash, role, status) VALUES
 ('e7000000-0000-4000-9000-000000000001','c7000000-0000-4000-9000-000000000001','mark@quintehardscapes.example','Mark Quinte',
  '$2a$10$ja7jD99TS1QsOtU.7dVDIu.HwgOK2pBERc60C4IyjMEi49IvR/dGO','account_admin','active')
ON CONFLICT (email) DO NOTHING;

-- ───────────────────────── opening inventory (set-based: TRT ~80% / KGN ~20%) ─────────────────────────
-- Trenton receipt for every stocked product. Qty by stocking unit (pieces vs tonnes
-- vs each/bag), and a believable wholesale unit cost in cents (L-5: opening cost must
-- be non-zero, or valuation/COGS demos show $0). value_cents = qty × unit_cost.
INSERT INTO stock_moves (product_id, to_location_id, qty_base, qty_entered, uom_entered, move_type, unit_cost_cents, value_cents, branch_id, reason)
  SELECT p.id, '5d1b3f6e-0000-4000-9000-000000000001',
         q.qty, q.qty, p.base_uom, 'RECEIPT', q.ucost, q.qty * q.ucost, '5d1b3f6e-0000-4000-9000-000000000001', 'M7 opening balance'
  FROM products p
  CROSS JOIN LATERAL (SELECT
      CASE p.base_uom WHEN 'T' THEN 40 WHEN 'EA' THEN 120 WHEN 'BAG' THEN 200 WHEN 'SF' THEN 1200 ELSE 800 END AS qty,
      CASE p.base_uom WHEN 'T' THEN 6000 WHEN 'EA' THEN 8000 WHEN 'BAG' THEN 1200 WHEN 'SF' THEN 350 ELSE 2000 END AS ucost) q
  WHERE p.sku LIKE 'DBS-%';
-- Kingston receipt (~20%) for every product EXCEPT one intentionally left zero-stock (DBS-SIKA-SEALER) to demo transfers.
INSERT INTO stock_moves (product_id, to_location_id, qty_base, qty_entered, uom_entered, move_type, unit_cost_cents, value_cents, branch_id, reason)
  SELECT p.id, '5d1b3f6e-0000-4000-9000-000000000002',
         q.qty, q.qty, p.base_uom, 'RECEIPT', q.ucost, q.qty * q.ucost, '5d1b3f6e-0000-4000-9000-000000000002', 'M7 opening balance'
  FROM products p
  CROSS JOIN LATERAL (SELECT
      CASE p.base_uom WHEN 'T' THEN 10 WHEN 'EA' THEN 30 WHEN 'BAG' THEN 50 WHEN 'SF' THEN 300 ELSE 200 END AS qty,
      CASE p.base_uom WHEN 'T' THEN 6000 WHEN 'EA' THEN 8000 WHEN 'BAG' THEN 1200 WHEN 'SF' THEN 350 ELSE 2000 END AS ucost) q
  WHERE p.sku LIKE 'DBS-%' AND p.sku <> 'DBS-SIKA-SEALER';
-- Project balances from the opening moves (no DB trigger exists, app maintains balances).
INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base)
  SELECT product_id, to_location_id, branch_id, SUM(qty_base)
  FROM stock_moves WHERE reason = 'M7 opening balance'
  GROUP BY product_id, to_location_id, branch_id
ON CONFLICT (product_id, location_id) DO NOTHING;

-- DIB-378/C1-a: migration 0058's landed-cost demo fixture sets stock_balances directly (LC-DEMO-PROD-1:
-- 40, LC-DEMO-PROD-2: 60 @ Trenton) so the capitalization has on-hand to absorb into, but writes NO
-- stock_moves to back it, on staging that balance later accretes real moves via receipts/landed-cost
-- applies, but a fresh migrate-then-seed DB has none, so v_stock_balance_drift sees the balance with
-- no ledger (TestGate_StockReconciles_NoNegative / TestIntegration_StockBalanceDrift_IsZero fail).
-- Backfill the matching opening RECEIPT here, qty/cost copied verbatim from 0058's own po_lines
-- (40 @ $30.00, 60 @ $70.00) so the ledger and the projection agree. NEVER edit 0058 itself (staging
-- already applied it). Idempotent (guarded on NOT EXISTS by product+reason); guarded on the products/
-- location existing so this no-ops harmlessly if 0058 skipped (non-Dibbits DB, see its own guard).
INSERT INTO stock_moves (product_id, to_location_id, qty_base, qty_entered, uom_entered, move_type,
                          unit_cost_cents, value_cents, branch_id, reason)
SELECT p.id, l.id, v.qty, v.qty, 'EA', 'RECEIPT', v.ucost, v.qty * v.ucost, l.id,
       'L8 landed-cost demo opening balance'
  FROM (VALUES
    ('d0580000-0000-4000-9000-000000000021'::uuid, 40, 3000), -- LC-DEMO-PROD-1: 40 @ $30.00 (0058 po_line 1)
    ('d0580000-0000-4000-9000-000000000022'::uuid, 60, 7000)  -- LC-DEMO-PROD-2: 60 @ $70.00 (0058 po_line 2)
  ) AS v(id, qty, ucost)
  JOIN products p ON p.id = v.id
  JOIN locations l ON l.code = 'TRT'
 WHERE NOT EXISTS (
   SELECT 1 FROM stock_moves sm
    WHERE sm.product_id = v.id AND sm.reason = 'L8 landed-cost demo opening balance'
 );

-- ───────────────────────── a few in-flight transactions (demo) ─────────────────────────
-- One open quote
INSERT INTO quotes (id, quote_no, customer_id, branch_id, status, valid_until, subtotal_cents, tax_cents, total_cents, created_by) VALUES
 ('17000000-0000-4000-9000-000000000001','Q-2026-0001','c7000000-0000-4000-9000-000000000001','5d1b3f6e-0000-4000-9000-000000000001','SENT', CURRENT_DATE + 30, 480000, 62400, 542400, 'd7000000-0000-4000-9000-000000000001')
ON CONFLICT (quote_no) DO NOTHING;
INSERT INTO quote_lines (quote_id, line_no, product_id, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents) VALUES
 ('17000000-0000-4000-9000-000000000001',1,'a7000000-0000-4000-9000-000000000001','Blu 60 Slab, patio',960,'SF',247.776,500,'SF',480000)
ON CONFLICT DO NOTHING;
-- One confirmed order + its lines (M-3: must not be header-only, a CONFIRMED order
-- with no lines reads as data corruption in a fulfillment screen). Lines sum to the
-- 312500 subtotal.
INSERT INTO orders (id, order_no, customer_id, branch_id, status, fulfillment, requested_date, subtotal_cents, tax_cents, total_cents, created_by) VALUES
 ('07000000-0000-4000-9000-000000000001','SO-2026-0001','c7000000-0000-4000-9000-000000000002','5d1b3f6e-0000-4000-9000-000000000001','CONFIRMED','DELIVERY', CURRENT_DATE + 5, 312500, 40625, 353125, 'd7000000-0000-4000-9000-000000000001')
ON CONFLICT (order_no) DO NOTHING;
INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents) VALUES
 ('07000000-0000-4000-9000-000000000001',1,'a7000000-0000-4000-9000-000000000005','Brussels Block paver',1000,'SF',4100,300,'SF',300000),
 ('07000000-0000-4000-9000-000000000001',2,'a7000000-0000-4000-9000-000000000023','Gator polymeric sand',5,'BAG',5,2500,'BAG',12500)
ON CONFLICT DO NOTHING;
-- Two invoices (one paid, one open) + lines
-- org_id is the branch location's (SF-2p): NULL on a box whose locations carry no org yet (this seed runs before the
-- populate that provisions the org), which 0432 allows there, and the branch's org on a box that has one.
-- THE OPENING BALANCE MARKER (FROM-PRINCIPAL 180, ruled again in round 4): EVERY document this seed
-- plants WITHOUT a journal of its own carries the marker, PAID OR OPEN. They are all history from
-- before HH: the seed runs after the migrations, so 0222's repair (which queues an ar.invoice.finalized
-- event for every invoice with no journal behind it) never saw these rows and no journal of their own
-- will ever post behind them. An opening balance's HST is not a period's collection, so the tax
-- reconcile keeps every one of them out of collected and names them on the opening balances line.
-- INV-2026-0001 is PAID and dated CURRENT_DATE - 30, which lands in the walk month on the 31st of a
-- 31-day month: marked, it is still an opening balance and the reconcile still ties that month.
-- The demo pair INV-DEMO-CP03-Q and INV-DEMO-CP03-X (0055) are NOT planted by this seed and carry
-- real finalize journals of their own, so they stay unmarked.
INSERT INTO invoices (id, invoice_no, customer_id, branch_id, status, issue_date, due_date, subtotal_cents, tax_cents, total_cents, balance_cents, created_by, org_id, metadata) VALUES
 ('19000000-0000-4000-9000-000000000001','INV-2026-0001','c7000000-0000-4000-9000-000000000002','5d1b3f6e-0000-4000-9000-000000000001','PAID', CURRENT_DATE - 30, CURRENT_DATE, 312500, 40625, 353125, 0,'d7000000-0000-4000-9000-000000000001', (SELECT org_id FROM locations WHERE id = '5d1b3f6e-0000-4000-9000-000000000001'), '{"opening_balance": true}'::jsonb),
 ('19000000-0000-4000-9000-000000000002','INV-2026-0002','c7000000-0000-4000-9000-000000000005','5d1b3f6e-0000-4000-9000-000000000002','FINALIZED', CURRENT_DATE - 10, CURRENT_DATE + 35, 900000, 117000, 1017000, 1017000,'d7000000-0000-4000-9000-000000000001', (SELECT org_id FROM locations WHERE id = '5d1b3f6e-0000-4000-9000-000000000002'), '{"opening_balance": true}'::jsonb)
ON CONFLICT (invoice_no) DO NOTHING;
INSERT INTO invoice_lines (invoice_id, line_no, product_id, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents) VALUES
 ('19000000-0000-4000-9000-000000000001',1,'a7000000-0000-4000-9000-000000000005','Brussels Block paver',1000,'SF',4100,300,'SF',300000),
 ('19000000-0000-4000-9000-000000000001',2,'a7000000-0000-4000-9000-000000000023','Gator polymeric sand',5,'BAG',5,2500,'BAG',12500),
 ('19000000-0000-4000-9000-000000000002',1,'a7000000-0000-4000-9000-000000000010','Magnumstone wall',1500,'SF',1548.45,600,'SF',900000)
ON CONFLICT DO NOTHING;
-- The receipt that settled INV-2026-0001, dated after its issue: a PAID invoice with no dated act
-- behind it reads as still owed at an as-of date (FW1-06b rebuilds a document's as-of balance from
-- the dated acts that moved it).
INSERT INTO payments (id, payment_no, customer_id, method, amount_cents, status, branch_id, received_at)
VALUES ('1a000000-0000-4000-9000-000000000001','PAY-2026-0001','c7000000-0000-4000-9000-000000000002','CHEQUE',353125,'RECEIVED','5d1b3f6e-0000-4000-9000-000000000001', CURRENT_DATE - 20)
ON CONFLICT (payment_no) DO NOTHING;
INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, created_at)
VALUES ('1a000000-0000-4000-9000-000000000001','19000000-0000-4000-9000-000000000001',353125, CURRENT_DATE - 20)
ON CONFLICT DO NOTHING;
-- One open purchase order to a real vendor
INSERT INTO purchase_orders (id, po_no, vendor_id, branch_id, status, expected_at, freight_cents, created_by)
  SELECT '20000000-0000-4000-9000-000000000001','PO-2026-0001', v.id, '5d1b3f6e-0000-4000-9000-000000000001','SUBMITTED', CURRENT_DATE + 14, 25000,'d7000000-0000-4000-9000-000000000001'
  FROM vendors v WHERE v.vendor_no = 'V-TECHO'
ON CONFLICT (po_no) DO NOTHING;

-- ───────────────────────── pricing reference data (A-07) ─────────────────────────
-- Category/product-DEPENDENT pricing reference data lives HERE, NOT in migration 0024: migrations run BEFORE
-- `make seed`, so the catalog product_categories + products these rows FK do not exist at migrate time, and
-- 0024 bootstrapping its own categories created an ORPHAN parallel set (PAVERS/AGGREGATES/…) that matched no
-- product. Everything below keys to the REAL catalog category codes (PAVER/WALL/SLABSTEP/AGGREGATE/ACCESSORY)
-- and real DBS- products inserted above, so the policy + tier-category rules actually match a product.
-- Migration 0024 keeps ONLY the category-independent TIER_DEFAULT rule. ZERO-TRUST: pricing OWNS the pricing_*
-- tables; these INSERTs only REFERENCE catalog-owned product_categories/products by code/sku, they never
-- INSERT into those catalog tables. ALL NUMBERS PROVISIONAL (D-031 gate; tunable via admin CRUD / row UPDATE,
-- no seed or migration edit). Idempotent (ON CONFLICT DO NOTHING) + reset-safe.

-- Pricing-OWNED margin policy, one row per REAL catalog category. PROVISIONAL 30/20 family: margin_target_bps
-- = 30% target; margin_floor_bps = ~20% HARD enforced floor (the per-category adjuster moves BOTH, encoded
-- directly in each row). AGGREGATE = bulk overhead → enforcement disabled (still audited via a BYPASS row at
-- resolve time). NOTE: the catalog registry has NO separate Sand & Base category, bulk sand/base products
-- DIB-260/252 (W8 UAT burn-down): Bulk is now its OWN enforced category for front-desk by-the-ton
-- sales; AGGREGATE enforcement is turned ON (was audit-only). See migration 0067 for the same change
-- applied to already-migrated environments.
INSERT INTO pricing_category_policy (category_id, margin_target_bps, margin_floor_bps, margin_enforcement_disabled)
SELECT pc.id, v.target, v.floor, v.disabled
  FROM (VALUES
    ('PAVER',     3000, 2000, FALSE),   -- PROVISIONAL (target 30% / floor 20%)
    ('WALL',      3000, 2000, FALSE),   -- PROVISIONAL
    ('SLABSTEP',  3000, 2000, FALSE),   -- PROVISIONAL (slabs & steps)
    ('AGGREGATE', 3000, 2000, FALSE),   -- DIB-252: enforcement ENABLED (was audit-only)
    ('ACCESSORY', 3000, 2000, FALSE),   -- PROVISIONAL
    ('BULK',      4000, 2000, FALSE)    -- DIB-260/252: GM 20 floor / 40 target, enforced (bulk sand/base/aggregate by the ton)
  ) AS v(code, target, floor, disabled)
  JOIN product_categories pc ON pc.code = v.code
ON CONFLICT (category_id) DO NOTHING;

-- DIB-260: reassign the by-the-ton bulk products to the BULK category (guarded, only moves rows
-- still on AGGREGATE, never clobbers a hand assignment). Mirrors migration 0067 step 4.
UPDATE products
   SET category_id = (SELECT id FROM product_categories WHERE code = 'BULK'), updated_at = now()
 WHERE sku IN ('DBS-AGG-SAND', 'DBS-AGG-34CLEAR', 'DBS-AGG-SCREEN', 'DBS-AGG-HPB')
   AND category_id = (SELECT id FROM product_categories WHERE code = 'AGGREGATE');

-- DIB-260: default BULK pricing rule (contractor tier) so bulk lines resolve against a real tier.
INSERT INTO pricing_rules (scope, customer_type, category_id, calc_type, discount_bps, price_uom, priority, detail_text)
SELECT 'TIER_CATEGORY','CONTRACTOR', pc.id,'PCT_OFF_LIST',0,'T',100,'PROVISIONAL, Bulk default (DIB-260)'
  FROM product_categories pc WHERE pc.code='BULK'
ON CONFLICT DO NOTHING;

-- TIER_CATEGORY CONTRACTOR rules keyed to REAL categories (Pavers deeper, Accessories shallower), mirroring
-- the prior 0024 intent on the real codes. PROVISIONAL discount depths. RETAIL = list (no rule);
-- BUILDER/GOVERNMENT analogous, unseeded at gate.
INSERT INTO pricing_rules (scope, customer_type, category_id, calc_type, discount_bps, price_uom, priority, detail_text)
SELECT 'TIER_CATEGORY','CONTRACTOR', pc.id,'PCT_OFF_LIST',1800,'SF',40,'Contractor, Pavers: 18% off'
  FROM product_categories pc WHERE pc.code='PAVER'                                                 -- PROVISIONAL
ON CONFLICT DO NOTHING;
INSERT INTO pricing_rules (scope, customer_type, category_id, calc_type, discount_bps, price_uom, priority, detail_text)
SELECT 'TIER_CATEGORY','CONTRACTOR', pc.id,'PCT_OFF_LIST',800,'EA',40,'Contractor, Accessories: 8% off'
  FROM product_categories pc WHERE pc.code='ACCESSORY'                                             -- PROVISIONAL
ON CONFLICT DO NOTHING;

-- Retail-anchor (pricing_list_prices) rows. The resolver needs a list-price base; these FK products(id) by
-- sku. Keyed to the real M7 demo aggregate SKUs (price per tonne; cost×1.85 family). SINGLE SOURCE OF TRUTH -
-- seeded ONLY here (migration 0024 no longer seeds anchors). ZERO-TRUST: references catalog-owned products by
-- sku, never INSERTs into products. ON CONFLICT DO NOTHING keeps it idempotent.
INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom)
SELECT p.id, v.cents, v.uom
  FROM (VALUES
    ('DBS-AGG-HPB',     6500, 'T'),   -- PROVISIONAL retail anchor
    ('DBS-AGG-SCREEN',  4800, 'T'),   -- PROVISIONAL
    ('DBS-AGG-SAND',    5200, 'T'),   -- PROVISIONAL
    ('DBS-AGG-34CLEAR', 6000, 'T')    -- PROVISIONAL
  ) AS v(sku, cents, uom)
  JOIN products p ON p.sku = v.sku
ON CONFLICT (product_id) DO NOTHING;

-- DIB-378: DBS-UL-BRUSSELS carries a PRE-EXISTING anchor from migration 0062 (975¢/SF, a "homework
-- ring" PROVISIONAL value) that sits below a routine contractor-tier ring cost, 975 × 0.82 (the
-- seeded PAVER −18% tier) undercuts even a modest cost basis, so an ORDINARY sale (not a deliberate
-- below-floor scenario) trips MARGIN_BELOW_MINIMUM. Bump it to a comfortably clearing PROVISIONAL
-- value in the SAME unit (SF, matching its sold_by, untouched, still correct); guarded so it never
-- clobbers a real admin-CRUD override that's already higher.
UPDATE pricing_list_prices lp
   SET list_price_cents = 1800
  FROM products p
 WHERE lp.product_id = p.id AND p.sku = 'DBS-UL-BRUSSELS' AND lp.list_price_cents < 1800;

-- DIB-378: the four rows above anchor only the aggregate SKUs, every other demo product had NO
-- pricing_list_prices row, so a walk-in RETAIL ring fell through to the resolver's derive-from-cost
-- path (or, when cost was also unavailable at ring time, straight to the 422 "no retail anchor"
-- error, the exact repro was Banas coping/step + Antico paver on a cash tender). Backfill a
-- PROVISIONAL category-bucketed anchor for every remaining catalog product so a fresh DB can always
-- ring a demo SKU. price_uom = COALESCE(sold_by, base_uom, 'EA'), the SAME fallback pos-terminal.ts
-- uses to pick the sell UoM at ring time (line ~685), so the anchor's unit always matches what the
-- terminal actually sends and no UOM-conversion factor is ever required. Real prices are admin-CRUD
-- overrides; this is the interim mitigation from command/tools/uat.sh, productionized here so every
-- fresh DB (make reset / CI / a new UAT box) gets it, not just the UAT-in-a-box script.
--
-- Bucket values are set with margin headroom, not just "non-zero": the seeded CONTRACTOR PAVER tier
-- discounts −18% off list (the steepest seeded discount), and the PAVER/SLABSTEP category floor
-- enforces a 20% minimum margin, so a PAVER/SLABSTEP anchor must clear list × 0.82 × 0.80 against a
-- plausible cost, or a routine contractor ring blocks on MARGIN_BELOW_MINIMUM. 2200/2800 clear that
-- with room for a real stand-in cost (e.g. a $10 base-unit cost still nets ~40%+ margin).
INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom)
SELECT p.id,
  CASE
    WHEN pc.code IN ('PAVER', 'SLABSTEP')  THEN 2200  -- PROVISIONAL ~$22.00
    WHEN pc.code = 'WALL'                  THEN 2800  -- PROVISIONAL ~$28.00
    WHEN pc.code IN ('AGGREGATE', 'BULK')  THEN 5500  -- PROVISIONAL ~$55/T
    ELSE 2400                                          -- PROVISIONAL accessories/other
  END,
  COALESCE(NULLIF(p.sold_by, ''), p.base_uom, 'EA')
  FROM products p
  LEFT JOIN product_categories pc ON pc.id = p.category_id
 WHERE p.sku LIKE 'DBS-%'
   AND NOT EXISTS (SELECT 1 FROM pricing_list_prices lp WHERE lp.product_id = p.id)
ON CONFLICT (product_id) DO NOTHING;

-- ───────────────────────── quantity breaks (A-07, PROVISIONAL examples for Ryan B.) ─────────────
-- A quantity break is an ADJUSTMENT that STACKS on the tier-resolved base price, it does NOT compete
-- as a waterfall level (so a bulk break can never wipe or exceed the contractor discount). Two
-- PROVISIONAL examples so Ryan B. can SEE real shapes and react. The stacking model itself AND every
-- trigger value below are PROVISIONAL pending his confirmation (tunable via the admin CRUD / a row
-- UPDATE, no seed or migration edit):
--   1. NATURAL-STONE partial-skid SURCHARGE, a partial load (under a full skid by WEIGHT) costs MORE
--      (06-23: heavy natural stone under ~4500 lb ⇒ +10%), keyed to the Banas natural step by KG.
--   2. PAVER full-skid DISCOUNT, at/above a full skid (1 pallet) the contractor base gets a further
--      −5%, keyed to the PAVER category by pallet (PLT).
-- ZERO-TRUST: pricing-OWNED quantity_break_* tables; references catalog products/categories by sku/code,
-- never INSERTs into them. Idempotent: header ON CONFLICT (code) DO NOTHING; items guard on NOT EXISTS.

INSERT INTO quantity_break_headers (code, name, product_id, threshold_uom)
  SELECT 'NATSTONE-PARTIAL-SKID', 'Natural stone, partial-skid surcharge (PROVISIONAL)', p.id, 'KG'
    FROM products p WHERE p.sku = 'DBS-BANAS-STEP'
ON CONFLICT (code) DO NOTHING;
INSERT INTO quantity_break_items (header_id, from_qty, to_qty, value_type, surcharge_bps, price_uom)
  SELECT h.id, 0, 2000, 'SURCHARGE_PCT', 1000, 'EA'   -- PROVISIONAL: under ~2000 KG (a partial skid) ⇒ +10%
    FROM quantity_break_headers h
   WHERE h.code = 'NATSTONE-PARTIAL-SKID'
     AND NOT EXISTS (SELECT 1 FROM quantity_break_items i WHERE i.header_id = h.id);

INSERT INTO quantity_break_headers (code, name, category_id, threshold_uom)
  SELECT 'PAVER-FULL-SKID', 'Pavers, full-skid discount (PROVISIONAL)', pc.id, 'PLT'
    FROM product_categories pc WHERE pc.code = 'PAVER'
ON CONFLICT (code) DO NOTHING;
INSERT INTO quantity_break_items (header_id, from_qty, to_qty, value_type, discount_bps, price_uom)
  SELECT h.id, 1, NULL, 'PCT_OFF', 500, 'SF'   -- PROVISIONAL: at/above 1 full skid (pallet) ⇒ −5% on the tier base
    FROM quantity_break_headers h
   WHERE h.code = 'PAVER-FULL-SKID'
     AND NOT EXISTS (SELECT 1 FROM quantity_break_items i WHERE i.header_id = h.id);

-- ═══════════════════════════════════════════════════════════════════════════════
-- WTY-01 Warranty-Claim Management (P1) demo data, DIB-153. Runs AFTER the vendor /
-- customer / product masters above, so vendor-keyed profiles and demo claims resolve.
-- The default checklist template ships in migration 0054 (vendor-independent schema data).
-- ═══════════════════════════════════════════════════════════════════════════════

-- ── vendor warranty profiles (FR-1), the registry, keyed off the vendor master by vendor_no. ──
-- Oaks is the CLIENT-CONFIRMED UNDOCUMENTED case (2026-06-30 @ 00:44:28): a claim against it prompts "capture
-- the terms you negotiate", the registry LEARNS. Coverage/term values are DOCUMENTED PLACEHOLDERS pending the
-- "no rush" policy-document collection (2026-07-07 sync, Q-065). Idempotent on vendor_id.
INSERT INTO warranty_vendor_profiles (vendor_id, status, coverage, term_months, channel, channel_ref, notes)
SELECT v.id, s.status, s.coverage, s.term_months, s.channel, s.channel_ref, s.notes
FROM (VALUES
  ('V-UNILOCK',  'DOCUMENTED',   'Transferable lifetime structural integrity warranty on pavers/walls; efflorescence + colour variation excluded.', NULL, 'PORTAL', 'unilock.com/warranty',    'PLACEHOLDER terms pending policy doc (Q-065).'),
  ('V-TECHO',    'DOCUMENTED',   'Lifetime limited warranty on manufacturing defects (smooth/textured); efflorescence excluded.',                  NULL, 'PORTAL', 'techo-bloc.com/warranty', 'PLACEHOLDER; Techo answers ~2 weeks (residual SLA, PRD Q3).'),
  ('V-PERMACON', 'DOCUMENTED',   'Limited lifetime residential warranty on manufacturing defects; commercial 25 yr.',                             300,  'EMAIL',  'warranty@permacon.ca',    'PLACEHOLDER terms pending policy doc.'),
  ('V-OAKS',     'UNDOCUMENTED', '',                                                                                                              NULL, 'REP',    '',                        'UNDOCUMENTED (the Oaks case, 2026-06-30), capture terms negotiated per claim; the registry learns.'),
  ('V-BESTWAY',  'DOCUMENTED',   'Limited warranty on manufacturing defects; colour/efflorescence excluded.',                                     120,  'EMAIL',  'info@bestwaystone.com',   'PLACEHOLDER terms pending policy doc.'),
  ('V-BANAS',    'DOCUMENTED',   'Natural-stone material warranty (transit breakage / manufacturing flaw); natural variation excluded.',          NULL, 'REP',    'Banas territory rep',     'PLACEHOLDER; natural stone, variation is expected, not a defect.')
) AS s(vendor_no, status, coverage, term_months, channel, channel_ref, notes)
JOIN vendors v ON v.vendor_no = s.vendor_no
ON CONFLICT (vendor_id) DO NOTHING;

-- ── demo claims ACROSS the confirmed phases (FR-3) with BACK-DATED history (FR-9). ──
-- opened_at + each event's occurred_at are set to TRUE past dates (not now()), demonstrating the migration path
-- for the live 6-8 claims. Guarded on WTY-1 so a re-seed is a no-op.
DO $$
DECLARE
  v_unilock  UUID := (SELECT id FROM vendors WHERE vendor_no = 'V-UNILOCK');
  v_techo    UUID := (SELECT id FROM vendors WHERE vendor_no = 'V-TECHO');
  v_permacon UUID := (SELECT id FROM vendors WHERE vendor_no = 'V-PERMACON');
  v_oaks     UUID := (SELECT id FROM vendors WHERE vendor_no = 'V-OAKS');
  trt        UUID := '5d1b3f6e-0000-4000-9000-000000000001'; -- Trenton
  kgn        UUID := '5d1b3f6e-0000-4000-9000-000000000002'; -- Kingston
  owner_a    UUID := 'd7000000-0000-4000-9000-000000000001'; -- Demo Operator
  owner_b    UUID := 'd7000000-0000-4000-9000-000000000003'; -- Sales manager login
  cust1      UUID := 'c7000000-0000-4000-9000-000000000001'; -- Quinte Hardscapes
  cust3      UUID := 'c7000000-0000-4000-9000-000000000003'; -- Limestone City Interlock
  cust7      UUID := 'c7000000-0000-4000-9000-000000000007'; -- Homeowner J. Tremblay (retail)
  prod_ul    UUID := 'a7000000-0000-4000-9000-000000000005'; -- Unilock Brussels Block
  prod_tb    UUID := 'a7000000-0000-4000-9000-000000000001'; -- Techo-Bloc Blu 60 Slab
  prod_pm    UUID := 'a7000000-0000-4000-9000-000000000008'; -- Permacon Melville Slab
  prod_oaks  UUID := 'a7000000-0000-4000-9000-000000000011'; -- Oaks Vintage Paver
  c1 UUID := 'a1c70000-0000-4000-9000-000000000001';
  c2 UUID := 'a1c70000-0000-4000-9000-000000000002';
  c3 UUID := 'a1c70000-0000-4000-9000-000000000003';
  c4 UUID := 'a1c70000-0000-4000-9000-000000000004';
  c5 UUID := 'a1c70000-0000-4000-9000-000000000005';
  c6 UUID := 'a1c70000-0000-4000-9000-000000000006';
  c7 UUID := 'a1c70000-0000-4000-9000-000000000007';
BEGIN
  IF EXISTS (SELECT 1 FROM warranty_claims WHERE claim_no = 'WTY-1') THEN RETURN; END IF;

  -- Claim 1, TRIAGE, Unilock, Trenton (in-flight). Back-dated 20 days.
  INSERT INTO warranty_claims (id, claim_no, customer_id, job_address, vendor_id, owner_id, branch_id, phase, source,
      description, expected_cents, realized_cents, opened_at, created_by)
    VALUES (c1,'WTY-1',cust1,'{"line1":"14 Dundas St W","city":"Trenton","province":"ON"}',v_unilock,owner_a,trt,'TRIAGE','INTAKE',
      'Efflorescence blooming across Brussels Block patio after first winter.', 145000, 0, now()-INTERVAL '20 days', owner_a);
  INSERT INTO warranty_claim_lines (claim_id, product_id, product_text, qty, uom_code, purchase_date_supplier, purchase_date_contractor)
    VALUES (c1, prod_ul,'Unilock Brussels Block', 320, 'SF', (now()-INTERVAL '400 days')::date, (now()-INTERVAL '380 days')::date);
  INSERT INTO warranty_claim_events (claim_id, event_type, from_phase, to_phase, actor_id, note, occurred_at) VALUES
    (c1,'CREATED',      NULL,   'INTAKE', owner_a,'Logged from office intake.',  now()-INTERVAL '20 days'),
    (c1,'PHASE_CHANGE','INTAKE','TRIAGE', owner_a,'The sales manager picked it up for triage.',now()-INTERVAL '18 days');

  -- Claim 2, WAITING_ON_DECISION, Techo-Bloc, Trenton. SLA stamped on Submit. Back-dated 45 days.
  INSERT INTO warranty_claims (id, claim_no, customer_id, job_address, vendor_id, owner_id, branch_id, phase, source,
      description, expected_cents, realized_cents, submitted_at, sla_due, opened_at, created_by)
    VALUES (c2,'WTY-2',cust1,'{"line1":"9 Marmora St","city":"Trenton","province":"ON"}',v_techo,owner_b,trt,'WAITING_ON_DECISION','COUNTER',
      'Two Blu 60 slabs cracked through, suspected manufacturing defect.', 89000, 0,
      now()-INTERVAL '30 days', now()-INTERVAL '16 days', now()-INTERVAL '45 days', owner_b);
  INSERT INTO warranty_claim_lines (claim_id, product_id, product_text, qty, uom_code, purchase_date_supplier, purchase_date_contractor)
    VALUES (c2, prod_tb,'Techo-Bloc Blu 60 Slab', 2, 'PC', (now()-INTERVAL '200 days')::date, (now()-INTERVAL '180 days')::date);
  INSERT INTO warranty_claim_events (claim_id, event_type, from_phase, to_phase, actor_id, note, occurred_at) VALUES
    (c2,'CREATED',      NULL,               'INTAKE',              owner_b,'Contractor walked in with photos; started from invoice.', now()-INTERVAL '45 days'),
    (c2,'PHASE_CHANGE','INTAKE',            'TRIAGE',              owner_b,'Triaged, clear manufacturing crack.',                    now()-INTERVAL '40 days'),
    (c2,'PHASE_CHANGE','TRIAGE',            'SUBMITTED',           owner_b,'Submitted to Techo-Bloc portal.',                         now()-INTERVAL '30 days'),
    (c2,'PHASE_CHANGE','SUBMITTED',         'WAITING_ON_DECISION', owner_b,'Techo acknowledged; ~2 week decision window.',            now()-INTERVAL '29 days');

  -- Claim 3, CLOSED / VENDOR_CREDIT (a WIN), Permacon, Kingston. Full history + realized $.
  INSERT INTO warranty_claims (id, claim_no, customer_id, job_address, vendor_id, owner_id, branch_id, phase, source,
      description, outcome, expected_cents, realized_cents, submitted_at, sla_due, opened_at, closed_at, created_by)
    VALUES (c3,'WTY-3',cust3,'{"line1":"120 Princess St","city":"Kingston","province":"ON"}',v_permacon,owner_a,kgn,'CLOSED','INTAKE',
      'Melville slab colour fade on a full patio; Permacon approved credit.', 'VENDOR_CREDIT', 210000, 198000,
      now()-INTERVAL '75 days', now()-INTERVAL '61 days', now()-INTERVAL '92 days', now()-INTERVAL '40 days', owner_a);
  INSERT INTO warranty_claim_lines (claim_id, product_id, product_text, qty, uom_code, purchase_date_supplier, purchase_date_contractor)
    VALUES (c3, prod_pm,'Permacon Melville Slab', 460, 'SF', (now()-INTERVAL '520 days')::date, (now()-INTERVAL '500 days')::date);
  INSERT INTO warranty_claim_events (claim_id, event_type, from_phase, to_phase, actor_id, note, detail, occurred_at) VALUES
    (c3,'CREATED',      NULL,                 'INTAKE',              owner_a,'Logged from office intake.', '{}', now()-INTERVAL '92 days'),
    (c3,'PHASE_CHANGE','INTAKE',              'TRIAGE',              owner_a,'Triaged.',                    '{}', now()-INTERVAL '88 days'),
    (c3,'PHASE_CHANGE','TRIAGE',              'SUBMITTED',           owner_a,'Submitted to Permacon.',      '{}', now()-INTERVAL '75 days'),
    (c3,'PHASE_CHANGE','SUBMITTED',           'WAITING_ON_DECISION', owner_a,'Ack received.',               '{}', now()-INTERVAL '74 days'),
    (c3,'PHASE_CHANGE','WAITING_ON_DECISION', 'DECISION',            owner_a,'Permacon approved.',          '{}', now()-INTERVAL '55 days'),
    (c3,'PHASE_CHANGE','DECISION',            'SETTLEMENT',          owner_a,'Booking the credit.',         '{}', now()-INTERVAL '50 days'),
    (c3,'SETTLEMENT',  'SETTLEMENT',          'SETTLEMENT',          owner_a,'Vendor credit received.',     '{"outcome":"VENDOR_CREDIT","expected_cents":210000,"realized_cents":198000}', now()-INTERVAL '42 days'),
    (c3,'PHASE_CHANGE','SETTLEMENT',          'CLOSED',              owner_a,'Closed, credit applied.',    '{}', now()-INTERVAL '40 days');

  -- Claim 4, REJECTED (a LOSS), Oaks (UNDOCUMENTED vendor), Kingston. Demonstrates the Oaks case + win/loss.
  INSERT INTO warranty_claims (id, claim_no, customer_id, job_address, vendor_id, owner_id, branch_id, phase, source,
      description, expected_cents, realized_cents, submitted_at, sla_due, opened_at, closed_at, created_by)
    VALUES (c4,'WTY-4',cust3,'{"line1":"55 Barrie St","city":"Kingston","province":"ON"}',v_oaks,owner_b,kgn,'REJECTED','INTAKE',
      'Oaks Vintage paver spalling; Oaks declined, no documented warranty on record.', 62000, 0,
      now()-INTERVAL '50 days', now()-INTERVAL '36 days', now()-INTERVAL '66 days', now()-INTERVAL '30 days', owner_b);
  INSERT INTO warranty_claim_lines (claim_id, product_id, product_text, qty, uom_code, purchase_date_supplier, purchase_date_contractor)
    VALUES (c4, prod_oaks,'Oaks Vintage Paver', 180, 'SF', (now()-INTERVAL '620 days')::date, (now()-INTERVAL '600 days')::date);
  INSERT INTO warranty_claim_events (claim_id, event_type, from_phase, to_phase, actor_id, note, occurred_at) VALUES
    (c4,'CREATED',      NULL,                 'INTAKE',              owner_b,'Logged; Oaks warranty terms UNDOCUMENTED.', now()-INTERVAL '66 days'),
    (c4,'PHASE_CHANGE','INTAKE',              'TRIAGE',              owner_b,'Triaged.',                                  now()-INTERVAL '62 days'),
    (c4,'PHASE_CHANGE','TRIAGE',              'SUBMITTED',           owner_b,'Submitted via rep.',                        now()-INTERVAL '50 days'),
    (c4,'PHASE_CHANGE','SUBMITTED',           'WAITING_ON_DECISION', owner_b,'Awaiting rep response.',                    now()-INTERVAL '49 days'),
    (c4,'PHASE_CHANGE','WAITING_ON_DECISION', 'DECISION',            owner_b,'Oaks declined.',                            now()-INTERVAL '32 days'),
    (c4,'PHASE_CHANGE','DECISION',            'REJECTED',            owner_b,'Rejected, no coverage on record.',         now()-INTERVAL '30 days');

  -- Claim 5, INTAKE, retail/homeowner, no vendor yet (UNKNOWN pending triage). Fresh.
  INSERT INTO warranty_claims (id, claim_no, customer_id, job_address, vendor_id, owner_id, branch_id, phase, source,
      description, expected_cents, realized_cents, opened_at, created_by)
    VALUES (c5,'WTY-5',cust7,'{"line1":"3 Elizabeth St","city":"Brighton","province":"ON"}',NULL,owner_a,trt,'INTAKE','INTAKE',
      'Homeowner reports pitting on patio pavers; product/manufacturer not yet identified.', 0, 0, now()-INTERVAL '3 days', owner_a);
  INSERT INTO warranty_claim_lines (claim_id, product_id, product_text, qty, uom_code)
    VALUES (c5, NULL,'Unknown grey paver (photos supplied)', 90, 'SF');
  INSERT INTO warranty_claim_events (claim_id, event_type, from_phase, to_phase, actor_id, note, occurred_at) VALUES
    (c5,'CREATED', NULL,'INTAKE', owner_a,'Retail intake; vendor UNKNOWN pending triage.', now()-INTERVAL '3 days');

  -- Claim 6, WAITING_ON_DECISION, Unilock, Trenton (open exposure). Back-dated 33 days.
  INSERT INTO warranty_claims (id, claim_no, customer_id, job_address, vendor_id, owner_id, branch_id, phase, source,
      description, expected_cents, realized_cents, submitted_at, sla_due, opened_at, created_by)
    VALUES (c6,'WTY-6',cust1,'{"line1":"88 Sidney St","city":"Trenton","province":"ON"}',v_unilock,owner_a,trt,'WAITING_ON_DECISION','COUNTER',
      'Il Campo pavers heaving on a driveway after one freeze/thaw, suspected base or product defect.', 176000, 0,
      now()-INTERVAL '18 days', now()-INTERVAL '4 days', now()-INTERVAL '33 days', owner_a);
  INSERT INTO warranty_claim_lines (claim_id, product_id, product_text, qty, uom_code, purchase_date_supplier, purchase_date_contractor)
    VALUES (c6, prod_ul,'Unilock Il Campo', 540, 'SF', (now()-INTERVAL '300 days')::date, (now()-INTERVAL '280 days')::date);
  INSERT INTO warranty_claim_events (claim_id, event_type, from_phase, to_phase, actor_id, note, occurred_at) VALUES
    (c6,'CREATED',      NULL,       'INTAKE',              owner_a,'Started from invoice at the counter.',      now()-INTERVAL '33 days'),
    (c6,'PHASE_CHANGE','INTAKE',    'TRIAGE',              owner_a,'Triaged, photos + base spec gathered.',    now()-INTERVAL '30 days'),
    (c6,'PHASE_CHANGE','TRIAGE',    'SUBMITTED',           owner_a,'Submitted to Unilock portal.',              now()-INTERVAL '18 days'),
    (c6,'PHASE_CHANGE','SUBMITTED', 'WAITING_ON_DECISION', owner_a,'Unilock acknowledged; awaiting ruling.',    now()-INTERVAL '17 days');

  -- Claim 7, DECISION, Techo-Bloc, Kingston (open exposure, further along). Back-dated 58 days.
  INSERT INTO warranty_claims (id, claim_no, customer_id, job_address, vendor_id, owner_id, branch_id, phase, source,
      description, expected_cents, realized_cents, submitted_at, sla_due, opened_at, created_by)
    VALUES (c7,'WTY-7',cust3,'{"line1":"200 Division St","city":"Kingston","province":"ON"}',v_techo,owner_b,kgn,'DECISION','INTAKE',
      'Techo-Bloc Aberdeen wall units delaminating; Techo reviewing, decision imminent.', 224000, 0,
      now()-INTERVAL '40 days', now()-INTERVAL '26 days', now()-INTERVAL '58 days', owner_b);
  INSERT INTO warranty_claim_lines (claim_id, product_id, product_text, qty, uom_code, purchase_date_supplier, purchase_date_contractor)
    VALUES (c7, prod_tb,'Techo-Bloc Aberdeen Wall', 130, 'PC', (now()-INTERVAL '250 days')::date, (now()-INTERVAL '230 days')::date);
  INSERT INTO warranty_claim_events (claim_id, event_type, from_phase, to_phase, actor_id, note, occurred_at) VALUES
    (c7,'CREATED',      NULL,                 'INTAKE',              owner_b,'Logged from office intake.',           now()-INTERVAL '58 days'),
    (c7,'PHASE_CHANGE','INTAKE',              'TRIAGE',              owner_b,'Triaged, clear delamination.',        now()-INTERVAL '54 days'),
    (c7,'PHASE_CHANGE','TRIAGE',              'SUBMITTED',           owner_b,'Submitted to Techo-Bloc.',             now()-INTERVAL '40 days'),
    (c7,'PHASE_CHANGE','SUBMITTED',           'WAITING_ON_DECISION', owner_b,'Techo acknowledged.',                  now()-INTERVAL '39 days'),
    (c7,'PHASE_CHANGE','WAITING_ON_DECISION', 'DECISION',            owner_b,'Techo engineer reviewing on-site.',    now()-INTERVAL '12 days');
END $$;

-- ───────────────────────── product images (DIB-488 rev 7 · demo thumbnails) ─────────────────────────
-- GENERATED by scripts/gen-demo-product-swatches.py, generic programmatic texture swatches
-- (never scraped or fabricated real-product photos; no text baked in). Stored exactly the way
-- the 0102 pipeline expects: one primary webp per product, checksum = md5 hex (the ETag).
INSERT INTO product_images (product_id, content_type, byte_size, image_bytes, alt_text, sort_order, checksum)
SELECT p.id, 'image/webp', octet_length(v.b), v.b, p.name, 0, md5(v.b)
FROM (VALUES
  ('DBS-AGG-34CLEAR', decode('5249464634050000574542505650382028050000501d009d012a600060003ed156a44ca824a3a233946f69001a09694c1c1cc99e2bfbbb3c401bb8f09659c3202ebbb8ee1dbeedbed16794e58362acf53beb1b24e21267ee101018305eaa26fcbb444bbd936b6b54cc29ab37ce23bf4541884fff16fe6285dd454181eb6fb5b0ad9683bf9233e74b1ac356fc5e112c6f2ba2afc5ad9dd614d6298b148c963428835d2d645df23e3d99e60d348db76de6445f5d25133613d770e1335f567ab4de4d15f01d56593f19aaf36044c9607523e743affa3bfe78dda7aeda38010def1b78f040bb4cc6d895cb4250bbac14a291452fa5fc049adb3f40c10cdb7ebe08aba55783b198780000fc6dd282d03a8032ab984923ecbeb15d972e189d70abe9bda6d783444f3580d39ae1af12c3e6c97d05c3288a36a7d689d77aa84fcb280effac4bec0e0c7b66e016291533bae57575970b701c8acea538f044fd1db8d4b7781fa2a479bb8d0c62ef09ca690328f8cd287b41a895c9b75bb0528ca8536615e44bc68d84fecb521776d78d76eb8d916b08dbffe241f908f81080a2181869d3a620a5db21c30ee32404a6a69a43f36ed22d9d277dcc1f9e8e04428ac65155ce9612e1dbfc2170b50eb5955bec3a986a729ab96a5feb974a8246cc0322867477eed458181dca56990786f010f81e4a6ccea00ef4a9791e6eb041c557c1df8b1f24a26884f0d5fc0e0c15a657c990c7c14fb0b7030b0ed5dc037c8ab66be9397b6424f6291641969c2b22758ca73222d53890cbad90e35a4bb7a8945431fa5ede44f5a87141307c42f7d155257cc42b22f9c234b7851da97768ba695f08de061ace51b1b63dcc736d4e9ee272cb57c67a2eab5ee9460eceec64ee8f0179ab8fde73390a6077aa5dca6ecac755af9e19227da0a32badfcda4b93fad9369a8fa4dc96729e0ce27f8b241121948fc4b871eef6e77206420f1781b3a0d455794140105695c3100656c8ac3eb42155ea84cecec5b8e92d5b58d5755af7b77257e75fc5644aba6c9356ce6608ac89b74311d5d7af4ba711af44d3c65de5b38dc50a6980887bd411a637969dc76f5ca6b71af893dc2bcb0446ae382929b54ca97d4cc6b80ee1d305c0b9445b88a4e75338ee94545c7907bf36beed55721ec65dfa1b84b7a3548ad8ea0b4d16a2f6310f0671883ec21c2e8bcf04863a99d6839a9349f0ad1c17ef0ebf9b3c2ef189012f4fda9eb8d4c3cb93d120ba1beb8a047d30784950e9a48dfd5f13e01b943fe8ed308186dff445ce86ffcc52411fa4b21baea9a6c36219e8b314f9d66454e5d28f051a768434bfa4c39a940c77becdcfb9e6910cc3f48b304a8089d1e0ae4d6ceca5e41f0b2033ae37e02c494ccb9b9db93e00dfd8a3590dd79b60ea31dd26d907425eafa9ad3a80f169c2c8213f1a36c3305fd202acd1b7826cab5594770d45587771ece32238d867561866d8ecde12831fdde4d0ab6fdb9c36a44f73ba2363a03d893ccf4ccd4954c13782fdb294285be66ea9aeef0576db0421a3e87224852c9f4781e5be0b4bbbfca71a8ab4fe2883e88f9c9c4e357fea98cbca3594e121f47cb87703ccbce201b62b17597dff7b6a4ae5cfb0a8ac9120a60367a23ff626c567e768c39bd5f8948ffa7d8ce09833161bdd2b15c38f8ecd7efa1748b8afbba48bef3715920436e3205c010a80ae20a5243fed73cde0bea8b6056598f32da3c01fb27521ceb61c330c44e0d1d4f0aa112193274fcb9824ec8f95a215c177b4397cdb6ae5f98a5f3d2ff6a3c4f4f380c7de8916c2d911e4bb13d6d5f0e02c2d65ce877ab112fec71270ee597891aaa7d1a9cf9b1f0fc6a2ce49963f82bc5b9b7c3735190bfdb016e3e949e5f3cd35e00000','hex')),
  ('DBS-AGG-HPB', decode('5249464612050000574542505650382006050000f01a009d012a600060003edd5ca74da8a4a3a22c724f41101b896900d50c7e497dd40d9245dfdf3e07f889789f0f74db3477f71df6e2797abcbc9efa95512a18025e2b9016b806a2f0a55d8b0f632793ef3e78b23bd630aeb035c6be19798f48f6b1630b08622ac37b49cbb9d68b8abea894dfafc57dbd192b8eb1ffc5cb99f3461f51d920b62ebdcd1792bcd1b4aa8b9598a5b9e15c6dc11f1940cecc726817ebe88f7951372cf9e91785168b4722eada3cb8b50ccea0e65d5800d6a3ed03a8307530a160031e29ea4d8eb533e721a5b8c4ab776c8360cb59d6e49d4ebd160000fbe5875fc7529ab7ebf68e81948aa2bcaff9e145b958ef45b039db5b4ef80b2b3b2f591cc66a0b9f89d1af58d8715704b9f46b747d5a34c47d98b90e3cc1e3ae4a47cb565ff1edee237291ab93debd4572339197dc53fd09e985a2d03f67b9f0c472c9cdb45107e50ffbff5e31e547cf8e54794f88e2f3e838eaa1bdd66d594859e8c4b6763c6336fe875051f3542982694b495545671921986f000cc0bfa3c8a520d3a75f6932b6f20cd60aeb866315a97b49d8bf384075242f1f14f1559aba91958b746499f70c0bbea6e6c8d762fca6b47a5bc337ba360803d45846f3eae09ca6a2bf5e856aba76492cf553479463af9ece15dfb747b45f7139d6092bf84f1c9f62d8a368ad94a4f34dde70c58749738d2695b5d52c030033a6f6e930567950a52270f71f68376c48dad7d59157a6743d5c03c34dc910a0b96d698ae9fe35a08818ff2381287d5b5c1c2fa89c42ca4ffcf3fc9f1d3c9e760dd1e6fecdf03f446df9d838bc76a3c1606007d7cd91b979b06c815b65ef97b4f7fcbda96dfeed24c72c7d6ffae39cca4ab717625b47b02dc251a0e5583288c67bd860fe707b2316e36c7b963370e7df7c319d847b54db1cb4b1f6cd33fc89b17d66765485734f3d01889d689345ef3e91889478c8dad7939b58ce40e6944164f3d22c6f9539ec50752774ea22b6843a62b4689bf7d33e5f140a7f7fc119fa41d9dfc4962498263362683ec3cf08ea7404cec0527515c6a40e317a5b3b3c681d6af009033c95e6e96c3ead3c9fb079f1029af095cc0d18702cba57e7b6a31fba798e23ef9339c3b98a8506b2c7c08914e6e69804c2f2491d65c1b56bc4b7c7daac23b98cb3f7623e0a1dce06dd478834536bf138e05d430241a7991a1676039793d5a12c9b730d7c1904d1aefc89c3147da4e212fb6a29d782df4c01e13af743134e8dfb269f205106078d3f88d71b3afb5ee8fe905a2a06bce70e8f16bf7c3570eefa6b6e9222015219aedd8673e0393a752490708209093cdb74cf3f26bd606c8f60ec6aaf167baf53e0fea4f3350f7f22539aa840df2abd8a7f6ca9516a3382c994eb8b513cd020455ed56c5fb0634dcbb44fbbda2d84012885c5d26b2f056879f4307084fb085a2af354908812b0e5ee3985d3a385441db2a9437ddca612e1eef8fe3d115c6318f7fc0374c8fb487b80423d761f07e58c8046ea24115a6b5b5445c7ed9c9d4cbc739abe1033c40cecc96fbffa60e05a38cfdb16133e4a59ba1c5741aed580ce5a23c7eb9764442b529f1cbdb530dd56a0b3d820899706434dad8745b52f61f946471ebf5e2daf817ca52accdca590337124f664ef4ae8071fc666f58757d81b4ee5feb40d92e8b21b35860c2d287cdd89adb38123ed28c32ee52b1ccdaeb98b507be19d9668b0b97fb17906b791ce24c686360d689fdcd7dc46e3bf42f238b8bc001def5ddf8c7bb8bc677e32788f1acae6daa2976bd16085f2eb895806de3f1e000000','hex')),
  ('DBS-AGG-SAND', decode('52494646b40500005745425056503820a80500003015009d012a600060003ed95ea34ca825a3223018edc1001b09675abd4259a9838d0a156b906879caa1ae47e8314ccf20d6d1fab993d7069cf2d1dba4fe3b7d50a3fc615c1a8285fd87ac9132e66649ed840620e784506f7186c61dfe0d823b2d2e4834672d513d2b18a7f6284a6109ffa20a4bf14784b45b6071a4632a5052d99db1ef7a0ce7317d5d2fac513d2f4ec138476d3a111ab65111014a5e30a8a7eb3ea4505f0dedd7bacda5c7291c3668600000feeb13dcdbd8eed01956e2c01620361c3c38d52f6158494b4b976a7da8ab6f23ff83648a637f6d96893fdf3e6d3eae1dd6c9a965f6aa392cb1534eee9da2572f8b01ba151307cd405cf91aea6c99d84e86c26a87403dfd4e5e02d382cafd2ebc024dfcd138d5a32480a07b74258fa3bcd6be442b21b94d1bc39712bed5ba8c23086caaf386fc2ee0cefa99cedc79f22c3d7904f7925f375cfb3d1a3fc3b055aebd883f3a8d1f0a2f0dbf0e6bed429219244422b297a01d3cfeefe54c1984e485ca1c14fd5b752e5066b8a9d9b8142b9f93ca58bf876d367e6d7726989d8585d52a928d16b6eefc8ea8826b0c6b409df185f948b9cf2d8b84b4231dc35b480de4f2bae4c4b868231a48532cfdde477e160104bb64b84945b95e54aa813392457615c5c47b48c2c57a75e0760da31d5a8ca889f1ce51bd387e029e01aa2e9b699a7ddd11a685f59ec5787b7f32582c4a65f88a78277708aad78be4d136e5933e7c15311234566044cbcf5ec491e7554e617ee3c941025ebf3fe06b407e7f220b07e116c60f83af4804bb756976b770c2de0e65de94b1c2f04282d2485e5e9f955609746b33e7531f5d6ba6b4c6a0e2b00c152a17ad3455aa08ee9eb9c58852ebd44038bc34f59185df3798f11ace8ed5775e1e0b9a869a3b9adffd41b0fc0a05b37ab3c5c4be97dcf7997426f9574b35e9d5e8ef2104679d7938e7d6f1195ac9dc125a6c90304a57517ddbe074395cbfec8ad1b6c620e87029fb66c73169d3bc6aebda4e1febc7403daff0ee40954498f1346b096dc07a196e37f2411221d2479e8888cd6146eff0fc246feb4a0e67d5312f2809910a75571ed009bbceb494d3957df7de148f07b4e815fe2e18668704c1b83ac723da1207e204e091171115ad35d622872aa8f7892a844e35bee0945361a5a669ad6ea31e05ea7c7121f84470f1547c8a0aacd002c58100f33b20dc668aaaff0826f75eaee9975a32fb9ba3f7fe5abb4afb5231213822fafe73f582b2e7bdf891fcb3cae6e0372a40bafe6fbeae4cc24bdcce676e3f8f97e58799ed520b6c70a3b2aa142059be28802578cfce79a7a1ba78751b2586af810599070b016c17dbc387bf007da02134e53a1dbc4625da4cd7761d919d2d61b93ab397ce85d777822aeec8c4b29f3b903cb04562f8d75f3d44bb544444592c493d4ed609a17a020ebba26a32bc22879e86f139bdf24d6f03a60d7d438b4254b4d85364ab2a028a9660fd129a42ac64db271c2a720be0950e15b56c13f397d86ef85b62154074d2fa274d232b35f89707e49ef87d7121aa40d7b3ae1c0725def73620e33248594fb793b9bd5005b3419fcac315dcf0854c59019441978ae53611a82579ecdc743f7ef4ecf346b8e6fdd5fe5abd5686cb14b1701fe407b6cca2821675d2dd07548b5690859b556bb8c0a57ac76e4a6f2a1000cee1f9e4514b2f292e24462b8611637b32900120af69bb0fe35d7770c89f3b6cbe538e7dbffe8af617690ed237ba61fdafeaed9aa0ae025d6856685156c8f3483daf4fabe83f47b6ef52feca2ec92b29884a2ba0ae60f56c1ea94b62c594f5babd5ddb3c530635fed01dc1f966942e3338bc33dca80d0f65de19e76fc9d923dd2bfd0677b62f3b94286372e4317577095a5d2b4d1d3185188794cc5c652b686d0c397283f12558e78ac476519a0cee3fb2eab942e3ccd35312fed17abacdcf198c2477a1af35778a057f2136b42052f755ec9e64fc4864449aede4e9a40cf665fd04abc8d4b1e6d0edb8ef1148ff70003c6000','hex')),
  ('DBS-AGG-SCREEN', decode('52494646d00400005745425056503820c4040000901b009d012a600060003edd5ca54da8a523222f14bd09101b8969001676322be4163b9bf9d0665a10047642c2dbfca99c2f2cf8166e8bded157d4aef45b7bc4713da2ade08fe7b772b9c5f01db253d280562483482da2013d6dd70e347a6df0ffadebf3e6982a95d7f1c03231e8c1104c9ee0ecb9cfa74c35a8358ebe4856929215d39b0f72ee33416c1fe27f6a76287923da6fd6bfafb6eeea609934d3979a3685d3f3184cb05b300d625e02b47946cab75cf1aa88d903de631f6c03b4b96e2dd3e38e2c75b35a3bfbfe1c7c40fb434f9f8ac2128004fd6372979e19f0922e622d70b44000fee03830d66229817fb5f0714e7164cdd1f736f70a7dc8bc6fd5fbd488a8ab03e3d7d188c48f8871e7412615a9098c1169f0907f94403c9b1f70e5fc776f6759ccce5c08f714f147ad4f947fa9ac681dbe9b304e1d6779791c1c399065935596fb2c9e49da6b2897e844cc7e3705b7896308a5bfe49cde580ac304b183a9b92e76b9cfeedfb5eec4faff075f3cba0b810673ae976971bad2a40716b4ba115a8256034fbd6a7fa86d91e582fc135cd96b05c67e3be158cd545ba1fa7c663aa271c73009ba32685b96e0bc287698c7a4426fa67d296bb95c52cc0c26777d15be7d3529e8b5d608a1c24d3d6d086e55d71fce067ee98b8e85ed5825829f74d3da0ec9621232da07875dedd8b966ac6ad464d13b87b9c1b0f7913cd950552977d2087e60938568e3ab668d664b26163d81ec8cf87e8cba07cf48e781e2bfefd9d846e9b498cde2effcc43bd5f03bdceab1de06a655b1e2413a2c37efbd65f01c6353468d98e0f49a39bd595b0870fbdf20f9d93edcd365ec8cf7fc85a48c63b108f1bbef3e5faa9234ecc483cb6064ce02538d620fd380f989e631a95b1f9363b8bdc18676d1efddac4152ab680429a0bac1156e61731a979e5007d1b3e5eb476ed80b5b3d7b068b04750e913b65249109da04f68c89225daeeec6466e61cbb13ae025e7c48b3f24df6b6d80da428ed148268460189230296eb0f525f89b4138d1bb65f05394a0d8e9ec79426cd6dea1e25ebdaaae8f45b48b869ee0ed5ed7a98373a823ae9102003315510984866f2eaf75339b411124c818188b67be3616a8cd88406d84eb5001f42e6b693b2a10bd512215525ea530a78bb58816222a31dab01863a72d0d6b639ebd7a866f0f8c744c4ef18636ff76420753a2fea29ef2be6cf1f57abdb6f9c6ca2a3c837e9128d7db5be74b6617e362f9906fd62b4a3b3ffe6d40e92eb6cd904dbe6dabd14d2f0a893b5fd29713b87f74b83b4b79fc46b87eb575cc5ade57dcc38005474a05f7d8085439606813016526ee96b1b5ae743cc2d0b3b18532242c0b95bf17b32a2f6dff8444c34d98813642e206db735bc3e940adef4fa0a3a6b1e0291dcdfde50dafbd34fc1a2c7c50eec19ad7af2e132c2c5007613819da8c905c9168f6cb8d7735aa6dccfc82113bed8b5d0f1b9696be0da25c5ee00d8943bd18cc501bd209f711d8bda53343450563648702a4d81e94547e6bf47f6182452d4ddc483faaad76b35b7879edba0877d90d3cb522c4fdfb68da4c0b70eca59cda56e88a1cdc57214645b54521f4e85025651d3ba58ae655b20686d0f0b5bb399ca9361907b05d0fc13a4aea4e09effb0065aed15e6eceb9fd9e476e7ae4f36fb1177bdacc9550ad3b32e9f48bec4ac1b834f82321f5000000','hex')),
  ('DBS-BANAS-COPING', decode('524946464801000057454250565038203c0100005008009d012a600060003edd60ab51a8a4a4a2a7340b69101b89696bae83820280094405af0b7a42bd6a46df1aa3249312bc142f9cace23eec087282c57e10cf0ac2579d6c1eec05cfdae000fedcdb82c35c8e84f8f88c35bacec7beceebb2ebbc2b8c82bbc87fe0a6ad18ad3e83881b7682b9f75cf4f80929a978995969b6433189924581f9e80f21820fc755151ae5e0e0d4f125352d75a09692fad60e77b1b8c1d0abb42744e672d6e5c66e8afa4f9b095034e6510bd1a450ff51aa89259b2ba0a1f26edf4a66d0cc7a6da99649ab175a92c64539dbad220dc4a6dad63341887d88159a0d4781562409df5eb13dc17735b075967a17187d535f34ba04813822f729b5413b25393df1e8b186ca905bf3d4ee2bdd69ace0add4d7d5182779993bbc6d56fa4294e289f91e43e7cebede48e7378fae666187e8040000','hex')),
  ('DBS-BANAS-STEP', decode('52494646c20100005745425056503820b60100003009009d012a600060003ed560a44ca82623a22e39fdf9001a89696a54c6921c773e24b9100ec3cff54b38fe5217bec70738779ae31b0da0b451a45864f200be1a488fe95550b42d17ce773ea9cda56aac8000fed4385f18b0f0dafec3098263278f6e66278cd441a9c0d78d46fa96a345593348959b0410da4c2df82cd5f55134c23905bb09b2404c8d9f2dc02c3856a7b8766038f8a746e360f75bf4de42c02fff0d9d9946f94d6962411f66845a124e90e54289b2c220c8f91e94b3b2029e9dfc8d06b2837e5496e7bbdb6a1e4ea089d876d46830ea632989704a162526dcb9151f9887a948e425177f3a2f183b1c40ef2a1faa87ebfdcbf6ad9efd7f86ffb90502e3d6ca34d7e5d1fecb84acddf6853b9c50a37bb426cdc36332b6f81d865c0583ba6632a25c72778b5f1dd8f1818ca49e985240374b568bc14a43373ed777a5ff42ef053bfe78e60c4df17d981270457770b80e3ca1d1e82e3784a1b87bbe76a032945b81cf07aeb49f77519028675684f31d463bfc5e72a211179f872206db80f1d271f0756f69b5315398ad09ea1f06bfd115c30bc2ff3819b38eaabedf78903950bfd33984681578ed2683a1550824000000','hex')),
  ('DBS-BWS-ANTICO', decode('52494646b60200005745425056503820aa020000100f009d012a600060003ecd56a44ca7a523a23474ee20f019896968d3834b80ce1db009ce96bb1a10a28d339e0d43469bad6e77b3badfb0cbfc91fed173f6b2f84874cb8012a80275246609fa3f3c3298e009a9f562e750dbe24b9efd9422e34ad7781c2016d29fb485e058853f87f92ec9a0501c6adcae6d8b1e57592c4b220000f7dcf1be15431711a7f92aec0e691d66dded12388c35b750768adcade4565d336f51559be2d8858f23979b45bc973b1b6f51de0d9d21d0ddc6c7cbc72b533fb58f13b03bb2d9a1e2a9cce1ea0bea04451c79011ce68540bbbe0bb3c6383fd63bc0cb7ff5e118e479bca10094ec6bfe61b11a4e10e929e5da7965bf3da0ebaefebb77f393c2fb240aa3250188f6f1eee50cf53a501f1737513a4ca57f1ab8275ef78af50f561a5ca2b0309bb88b25ac274d944881b9fbc7dcae59b8bc13405ab505331379779b8d8c828c60fe1c14720d0168e2a5b4c7e7a04c79674c2ee0e2cc2651a3b0e228cb59a0af9aaeb3611f5eae90a3ea61bd41104c5814d7e4dc0d5bc45f008a11a38bd005141395949f8feb5e8b4a9d5500727d58af420b04c8ca52ade8a3620fd1ed37d1ba3caf786cbea9cb17cc3076eef1c0b81f8524eb7851807501abcebae36b27a055aee229cdf0fea036a8e88a600b6ee3fa28d9f1c4994a9745e871b97cee8b2098105346d908cb7df0d965deddfb923b062b903b6b81fc84f17f7b43bb19890d64b1199bd72abdaea2c2f963d29cf6146530edcc298c5e6be57e0b81e9154c5f88c65c3146dd3abe5f0e188bd433585da302a525f64153ebc5cd6fd21c1d68c1098821183eab343370b86e020bff7a29cdd2ead37b351f1966b356b66d20ba744a062d45b9bba0c09fc6433c086920bc1d1b97683850b59b60961d37659d7890318f1a3f0754f29ea37c27f16475f31561cb57bf9e83857e30e4e2ba200000','hex')),
  ('DBS-BWS-TREVISTA', decode('52494646500100005745425056503820440100003009009d012a600060003edd62a84f28a5a4222ab40cf9101b89696b1e526001f1c0f6cf6f72e08da0ac2cde85435caf8e9098a2d35e8db3328e520abc181f9301203c4d37d50fcc2ea3843aba4abc7f400000fe496b196bccd85314d3e9df126f94970e53dae5dd8075a1fcb805379cbaf5b042bd9653df193d0b70fc87614f18f0bce5df82951956c817584f6139273204431165e163b70d41b4b6568b6039c4d27ded7ed7c2293ed554479ca7c199ca8dad742e5121d2b400530da1be608f82807dcdba6eb362f3b94a46023241c9d25ec240cb5d4c4f0264aadf5fd1b6499fb9add6ab73f6021077752b06684784e8836f75fe092ff6f21f04a17871aa9556cddaf468b4ca9e9a8ab503c560bcf6a88342a989cf8f41527035344f923cb8b952339e666b486ae1465ec231c8576696be44f580cf58065b1be8b1d8cee3fd3eda3000','hex')),
  ('DBS-GATOR-SAND', decode('524946461a01000057454250565038200e010000500b009d012a600060003edd64ae4ea8a5a4a22c749a31101b896900d50ca92e4081d1add26f2c451721c32bab726fd0160be296e8034ccffc6cea4ddde59f8f51ddfb427ba70bd91966e286ca2b3ca564f7a849ebd1869aceaf1341daf36b4f94000000fede91c9d6df6a3a62a9c3bd4f43469f7d3d808f18e3348cf89189e48da4cdbf446ec39d26d089d3d012ccdd80d5e2aee972d3f39f3c2191aa2185f8e63d19fbad30ad5c890b87651f3b63b2056c76b7b6d4c9d5e938388c8d14cf144c159b7a51af11910913a33e31af3b9dc6e2ac37441554d2247f18560e36f02981cf0644bc0851cca796e4f57b24fe4882fe0c3689514bd98111d1cb57dada4e2fbdf77beccd1aa69336c0500000','hex')),
  ('DBS-GEO-FABRIC', decode('524946460c0200005745425056503820000200009009009d012a600060003edd68a94f28a623a228f99b61101b89696ac2f3d4afabc3d3f1fc68a67930fc2281f2eeff4f4ef2ef7ba5ffa079fdc150008a649767a2bd66854a4ba9ef158f8e2793ef09cce88aa3a45000feef4cec376203eba316ef4e67a911803cb91054555e41d8446eb0652ce3f2415b45e4a5264cc26768fe2c164cc9e3447977363c859fcf880c56a6c2be6c1c769710b80767b66f5accb52abdfe93a03e834bc10cb62ee090053f34a9116e8b9ed9e32851062bcf4e3c1d93867a0b5eea3c7b714dd1c2090969d7be82a00938331dedbf9f8bcf77440e4fab567148aa3932ef5d8007e86b986948083e11c3515873df01112ce564ca92b1822e5b52852ded103101f834aa07a7973a940073c43f00a7d3b27c544b894c8a994fc27976589ebf1d9320854f240000f7f37ffdc886546822602b079aeb1b2269844efeb092027908ac37939afa57962bc302491782554d4d0ead1a66a4934c50a779140b2b108a5905039b32f24c30969d06a1ac4a81cd9d88240afff741b941ba9dcff41ddf20c5609e1b21d163f0c9531b1853e3e60fd45d66d0662103d3e0be273de109b63bf0402998b5536fa2cc17bdb727a9a89887752a9cfd5cc3a0423f59de9702f3c3b25431a01e8f8e320dbc7dea8255ed98fd5234687fb78497bc85f5a15426b397703e860f120f422ebfbc24acce2ae5e94e30ab9763300000','hex')),
  ('DBS-INLITE-LED', decode('5249464650010000574542505650382044010000700e009d012a600060003edd60a34cb12632a22f1d2b5a501b896300cd50a7e263ad951fb96f7b31573d02ca3787668a374153e9ff528dbce58e0ab662a5c78717ebc216b7e7574fd85b85cac80925c7981525aab996fc4599947b3f5bd8d3b223140e1c2a64693b8a978dc34d280405b844fae92fcd233653ce3e0000fef052b759d941dcdd90ba5fb65f934ec6feadb465df8b3da5ca7d8fe202be49d4d5ff9b05e5e7d53bab2023c75c2a2033687b8a6d605e98ce8a917ae57aea44ff5e8e053f694d79206799e986fd2a38b5b372df8ef4f1642a1afa9f526780a851df4bf5427fac3e87ca3782b2eb51b70bfed858b5f88a6ce84ada62f1b7d4ec78f99783a27921692fe92637aa56f50f6d432528a5fef411f95f73866d6552fdf54af5be414504f05121d3b2ce028acdc24e06c3c5de13be370d06e2de70ad61e77db505000000','hex')),
  ('DBS-OAKS-PROMA', decode('5249464632020000574542505650382026020000d00d009d012a600060003edd62a74f28a5a32229d74c29101b896966c68080026f4b937234e4b12e53230f7abc08770b0b82d13f4755b918d22b81dc86682c728bb99f18acf2e8b309b1b0bbdc24d6a75370aa6246ce82523ba72c3e7b209880e92652ff8a31f60243af8d822a243672f1667733e4200000feeddd1061770a8167bf698b45246911d5230907c9766027277529ab83caa93165930e34ee064eae9c62a88eb78b2d058f00d2ea797ed7b26aaf9dd2f71eca0914ad7fc2f74aefc0a700fc755c1456dadb57887c54438c82d598e0a1f5f1f63f590d01266e55226f35cadd7012c0374146cecea4f2ace9d08623560d82b3a0a5260e8f141eb37c2980d07a0cdaab679a97516d90f778a89848dac8441c881cb6e85e4a44ba127c718522231382ed274b4711928d28ac788d01602ab9294f2e107216f37fcaf7345e601cbce735d742f87ca734bddb818bb547eafd8269fc2bd511c71ed1f163b0785148b23723306d01d1e59b51c67deef2b6bcf9c88c0d4f6594673a2ea2a52911113cdde97d2daf82acccc62dd5cccc61de1b303073f7b881482b7d4dbebb0c57666a230312375f164ff3188e221dd28be39fc537a4d62b36baa35afd3feefbc534f2dd7d44279a56c01be8bd8e0c3cffe1dd2aa3099660291929cd5cf07f3b04a99d2b5df8d0030c2a7a7208f25b782c1129d3198e0e96f816368ea2a072ddde11646ca0e15e96642651c40102c560b950f5909411e60e587844a4be6b9fac816ae1aa400000','hex')),
  ('DBS-OAKS-VINTAGE', decode('524946468602000057454250565038207a020000700d009d012a600060003edd5ca74ca8a524222e38fc61101b89676a5488379bf56aa969917cc15b441ac3422f895a9a23bde39b5ce7c22e885ff33c98a2b5c9e01eebf8e9664cf89fb122e330fc53875de6f69e6c94b1d82d8953ddd5b4163a1ade4f6fcbc8b13160756c15f52249443ccf79c000fea8e6402db7f8510234e309a4b6ef81256acd8b98906eeda217d717e589bfb678d8464635e85b06af4a33452a28508ef10b4ea928b5fd49c6604c9b8ded4a460f0acce5b202e846799000a1ca77ed070331372017b8c2d84d8628e2cb815535dae9628c8cfa9d6372395bb6b8b514e007dd7d40c3714d8e6de26ec6eaeb8a85ea510d97c4fc640587f8b828a4077bad59fd47abca1b1c1dc8c0836b4d302c63a22ae833c98b68784732c487bd079c4c9d11706bda5e36dc4e10b2ceb8c86924eed1697eb403f8e9be6d202e7c5b3f5b16898cbc9b93aede80b6919c29282935e33bd8ed5aa0575247a3582f1dbfc2e9792976a8feb24b4ec191be0cf542032f2a4ff5b1f7de1cf78badbbaab1ad98d4d792c63c9f83410f29237e881d7c2eee14a8d0fdb4e93f4d30e3e19aea16f98e0e5b3a01ef96e334752602a32796c7a5350036934a822eb2acbc04a54fed2769b50d4e3caab82363cc4e6e78f8627b5ae787bf61b7a01680561a35cccfc04ad25129725056ff4b0d6006fbde6461df47b92c77cd0b459d6d8713dbe0bf15e4416c39bb75ad64fd2385f3c16f4b23d1850be6d94881e55e659f14e834ae9e9460e626ea294d3447097778e2e5bd8e43028b85c93488244b9a9fd6ebfe109aaeed8ccd3bef700be4e46abd201dd0c9a9c6d623a9aba6441c1156be4318ee68f8b712463e8c4e309bba399294252242e2f70d31c40000','hex')),
  ('DBS-PM-BORANJA', decode('524946465a01000057454250565038204e010000b008009d012a600060003edd60a54da8aa23a22f14bc31401b89696a98183819860d25008be2c05ab6297dc91a280803bc731c45b59bd300d3d8697fd8ab5caf51f70e981b938593b1ab53b9420000fe9fe8f055b3d1b06ce4a11cdfd51dfd02c8f38fbf4ca912695998a6399ebfcf6cef2563a89196fe72f036c0078545f986149c1f6c4dcd8d4bdd1606e54d7b569be9dec484ea2578d6655954045fa969a12d2921144cad2a5dc6f8ea8f0e73534c9ee31f01154b3c958e1eba686f9592ca537dd8ec148ad34bb54b3fbc99e1d5d998e1c1297dc182cbabf92b6e0641f1278bf565436abf9b2977631dc30eea6f3982f3ac5350a368113a22948bae334cfd1b52130570fb04cb2a6395f4c1c42738a824640d02e528876958d1f9c97b56e5578c6ebd1566f9472a78a29b66b34da717005ce466379fd5040ec111da581f5cde235f721c96c7623c4eb7638000','hex')),
  ('DBS-PM-LAFITT', decode('524946464a02000057454250565038203e020000f00c009d012a600060003ed556a14ca824a3223556ad51001a89696982ad0a6330b344e6ea43d48e8837944ec07a1f4f3922fc63eb405d28a3dfa6af1cc93dd53d7df8413a0f670cd6eed7c1f82ee299d89edf0dca558648101aead87733c8c5ce1930965c6483ccd23e957b9c52200000fe8756f2fc5e4d426ce479255ae6e54fab831baf2520ec200aa458fb94614178307cfbe1e4ff73e6599cf6a5ffa96867d8de9c7fe9c2d4fa0e29fea25c6b17854adfbf8210505676b8996c9cfed7960c3d2217eb31cbd37f5775637f2fdcf51fc251fad88144130c95899170b447867b4bf245569b301768180f88b0e6ffbd878743ca730fb44ec1899b5ff8b4615e4ffcb761d7ceffe10c2272d81ee30e732f2aea862283e62b822a8e817b4c4f5d792ac5d034c753f2bb4cdcbc8475ba2e183c00f0088182cd27794aada527444bc0e14d7db29b33ad766eb5ab35b0d95b9ed215a40d57b78be4abaa452d6b68294519686b6f00ff519f49461519e25d9773cd010c120a344d8833b9368b621323ca6710577a2011df956e656d77ce9d179069326fb7e8d39ff20d89f71a448306996b51bde6d4492cd05cf9ac46b2955fba32f8bb865f28403969d21a1c52d2fffa039260800fd7690ab3be8cb25d45d2bb038fe4f1fa87815589485bc399898fc911ec267c6cabd6012f87e92edd81f540e39fc98fe59dff6476f88f385531f1db59a6c2a2a9e1b886d6c1a401cdc0f29b5b08bc6b4bc78085d4eab5ffb2705519226b8de27ced1536f18661df93dbcfc2ef41beec81b05c586026000000','hex')),
  ('DBS-PM-MAGNUMSTONE', decode('524946461a05000057454250565038200e0500009018009d012a600060003ed556a24ca824a32236352c59001a89673c15e9342052f544b7f24f981f1cd0da660dc04748b156ecdf78b2b65a7f31ae9f1aba8654d432e18b879f7072c925eada8d5b047752402b85d1a43bac9e7cda1ace60f32fc3298a2138d7044eb838e03a66e4732073f624d34d49ca810a426434ca40b1052908266385509a5a389dad1b8361d30fb9c7474181bf1fb42cb181d736c57f4f66443ba356b4badefe140861df985100c166efc2f0b8f56690835dc6b816742693d999fe1190bafbc9bbc6bc3400fdb76ccf2f0cbeaf4dfdfb1c3da87d2f7609f0042934779f42c5383d9f0c835d1a78370f2fef7a5ae3fc886a32401366c90baca2c376b223ed4803b20c16e1dd572331c2924401ab63c42f94475ed78d5cccef6b360e5e03ee588d5c554d079256b52f91f9e6ccb2e6c468d79596f8d24b04199954dd577b02c1c9433835356f756aaaf89b48309fb01932ed50a3cd29af377601e10831803fb73a4717f8d2db828571abed9d4ff484a53b1d4513dbe6d51204f86276457760a4fd1c37b58b5db6578f4b0d042b84f40bb5a7c287c299e691105fb0314c8dcac9521600487b2ece43fc6246431f622ba362db4c2296f8f7eb47d7dc8214f2f1d0e039922aebfa0754c47db5a5dc2042712cd93754799567de26a6f9f1234574faf541971b6d53479023935808bb0bb9da214869c5e31fcc2d714c94998f4e6e61a342efe0cccaea79714c080e1e34ad0e39b92ba8a77d73c2789ee592cb4d2e6d50845d5f757efaecc957e83c5aed4a868545613cbe595c47f66fd2a2d929a7c00470f88751150bae6dcc365687385ec75eaff47c83a12d087a45ebccb4e183529251c313ed413bdc40a2c45f4f734c7d680e5903b64bba69cba633698a120ea15086cfeecc76dfdc92cd9d6ed9a66640516334e7c563438755c3ef1321ea63967da28cf9bf979f50ee8e1073fefaa65c5c0e98e5f86dd7e4a7bff19f298c88080093281a91eda4a2f0c2ebbe52efbc3bef6701e1164edf4a541e33f95b1c1ef2a4986e1475238467f13f0d94ebc514dade2254f5e0e881628b7766b11e838a7a06be68b099ec366807ab101f849537d080ad2c388ff9a1bfc5d85f06ff3d76f05b454b5391d1f6e1fd802c99f31f0cfcfdf1d0ba0e64ee9dd55a946816edac89c7c81847d495d9bb5da4ffac4381bce47f49fb2888e66f80155743a0baebe122afb149eb5abdc47cae3a5bf64608b3d16746952afa40202f93f4806be211e8972445ef211830fbeff90da5d9b11e94ec098750f49ab0d4fa392b4bbfdaac1e65f78be3113c0b22772034e14623c68fe8bef0de4f2c4792f83d895e02fffa2ee4f29b41606460fe81d850d536c97b1fcc62af1f54f7ae07bb18b73c9d6507a9ba6a3937472ec97ce50f48542e64766dc996ce45898572847a83d3f45c5a7e6c4ef6b710c3c793940f9f6eef630700eb0e0be6317bf0f354be6aadb4d1a8fb5bd14b7bc132c8d1120b4d1d4c5bf0ffeef24bea8e4a0c88b0b62c32998479056567134187b0f9929810c00bd73e198cf95cb7210560dd9668e39495032ba6fee7f0022ec26a4774e977b1b6f2bb2cba56f42a2facc134f46498f15b32c05fb1f9084daecd5ea91aef3cab3ca3b54eac15832760e21b3bfdb256a5e823a452e0c8fc60e07ae9f19de42cb39b90308b3d820729c6a31cf044fb5a31bfbe2e6c95400740d3a6c8ae0a9e68fba154670b32f985bfdf31b188ca96ef38a33288c5bb083da9ad8b5903c0ed76786778c74dc76acb0f35710610058b8df408ea42db04c7d6ebf8577e6400','hex')),
  ('DBS-PM-MELVILLE', decode('52494646f80100005745425056503820ec010000500a009d012a600060003ec9569c4ca7a522a2371c4a68f01909676a9a9491e3188caf7127c110458295472807dd7cb6c42d5db9c067eb3df920252d1c608ecded405ac40f2e9995837235b701a17d59aa5abfca662cdc9eac080000f668e390240e288abedcbe23db767dee5a290d6354d45877c574584627cd5ce7a4c1f4fe2f9c905d241453830d7a6e564203f4b6d88c9c02e6dea5415e96021d0e550a3f983c11792876f54443adeefec96046289a506ac1f4ffba5c2e7f556e18c928d01cb67e978b2470d6d4dc85839b920db6bca60e47c1278797c9ddd4fd647be8fcd365037cff4ae984b70d1c77a50dc96b7ce38c1801b751ee4cf05755a4e24e4fde5a8240597109d2e51b234310c7c028d52516b4c5230147315039932d6c4d68aa6cb291a78302455f1e44f926d30e9ed2b1b9292051bb09e3ec72ad6c368e9263657098e79a1c4c2e3dbe0cfe732d90fc23a01d294a00e4263076371886c7ee6fa92630f74fc1763fdf0bd59fca81f6a103964e5e5b7daf59c9eeec7bf5a49f9d3aa2b093f0dfce9ea1177cbc0c44243c5c57997b25601a8cd7423a42f4c06982cbfd5753d5488cedca8307261fe3e5c0632f4464b9cabb8c5c7f1cb1d257d812c70ae2ef8ff4cd7b0d1a745b03ca90bc6f8658dda01477420629611114ad2f55f867371838792800000000','hex')),
  ('DBS-PORCEA-2424', decode('52494646f80000005745425056503820ec000000300a009d012a600060003ed15aa44ca825a3a2b2b9ecc9001a09690000b42266ca6b5ee0d1021e93790928c0c7a51d0f299628cf96493236874d317ae8663f08909f68f16e194066842ef8b718882f37181cc4af845368af6a8000fa9eb2ac8acb998f8b57a3d1c0bc3b3379e1191196e602f0991253916bdcda5b447b3b4a8aeb0566b9adce431044a62f375f5d34322f55065ebd2b151a2461e697d7aa4f10ca89e19ff0ffe8685b7dc4c54cde8a7966ffd85280ceedfc55573f3ac70568796ef513f4e3ee6dc460ff4e9460f6770da109ee81846173338058b3ff17ab00d59e812ca026ab760e72060000','hex')),
  ('DBS-PORCEA-PLANK', decode('52494646e40000005745425056503820d80000001009009d012a600060003edd66ac50a8a524a724799bd9101b896912e002e40813656d68f9ffc9932bbbdd65d8d327630a0c2c468bb2c2eb6712f899e8cd6c9a243255968822aaad5e817f6b4a923afd8000fed0667d57a33cd60b7b93f4bddfc88464c66d019ea11c41b3e54ad39b9a41523e94a2e60b7f15d3d6a13f08bd48569e3c982cc715be730e49747d13931355f5e9cf170b0c9b00fc775219178731c33522351c34fb02cb1e3b51a6486a6a35f63ade03ca8e5279940cf81f5fb5a871ba1462ae5a5201a25907af388e2e9f52e7e76f2a180000','hex')),
  ('DBS-RYMAR-TURF', decode('524946460a0500005745425056503820fe0400009014009d012a600060003edd5ea54da8a5232230155b89101b89695bf8daf93dcc1429f33209c10e688cb1d4a2c4078e93385db1fed8e97ed27fabe3697b7397e488cbd3eabd135e037f79178d9f1bb88b17de36d568aaca12a0930b62f4161fec1b3d83284626c82b63a90bfc4bea799aa81d6a40ee6f56d5354e548ada71c74d3e6aa14d5479851d89beebfd047eaef8391fca85692a16865562854531fb7002bb669584083f9166c980480000fe31cbcd98d4b7098b233f0988f6431f68db8762259a8976d7800d699ffbde96b86bff4974b90d0657ce1ec795eeb074fd579ce80323a49566efef575dbcde3a144207623261c90fffc73891cb63333eecb687bf3f111d27fccd496c287cdbf18daf7e0f8aa3d5c7c0033c15d9a80d5e416ad57fd42af16a6c6b479e21f02c6aff45380e70eee825cc82678ee4ca62f0e6593ddcb73a3f140f6ddfbaa4677334fc835fb2d87c107e82d9c1a82a26fe6f95fe52982ec5c4c245417be2c13320e188d4410c24c392cd2719e1b76c6ec0a849e27ea9fdbb275cee1f45a510e0d2f841e5aecb3dddfb8591b17418cb8c4df9cd33a5d9bd420a273e357f7409f5f31d096de762180dbe87a68319b18b3ea39d10dc8ad2e80792bbaac575d7feb43575ce7269544ab006d0d6cd1cf320f5202452d8867f4596faa3ff653ed206f3dc0440839d68722e69aec69369afd9c628b3080b6cc13dd1d68fcc6d592d96d1302eca609693bcc46512f2ad21635b082d094103415b2fa962efe3893deddd197cc5c50c1bd26f35d07416ab0bc6bf50412b9d06216ca9e222c016a597f88bb8049ef4f365d0283dd978a9e951c1c91a26da290f52de1244aa4005aed115c4dd7e7898634d1a057eb4cbada042d64596f29a85fef3b70c524c0d685efa23135a1228ef450d30af386c8887d8df1e0c380e97ba88a5154cc0671767722af66381b71f180d68df01764069523b189eb9c10a8646fae8101e3440e81d7541ebb80cecd40d2771ccfccdfbe06f4de2a1c2a007e9d7051349a48bed23324bf64e2bacdc3518d769161206ff030953905e2ea802101c90f87c28721492c4a493d943a3d958e4fd97af57d3bb72704c6a5f33bcac924278a4e45633445bbab63bde4e83ca0c833226a8939dd54e43e65065a1b8266cf88be5c85eaa7e801addbf4c3c48773170d8d359c94fdfc05181ebc85990d189a6c01f11107002c74abef89cbb69f08fe1ae2668ed3cc1640022a7018bbaecfe068fc16522923747c0ef09195836dc565c5433126513f62673b79bcedb0193073c2f20f3f3c4a027e8f259ca0449efecd15be048008c3ae617a20287ccad4748119a12b30b54e673cd9af754af8caaacec4d3b51a2afac594c475a94855284e8013e31e2c07761c0d0c0a804ec51c8038aa50f427f279f4228cc6433a15c71a5b4d724889ebd9b4b8da6a0aba0ff4492819a1a82ef64863f306826a6ec3d77e41862319eb896301ece01b501e6c53bb57a7e30d72a48f4c530103abdb4859b72566227f5151b84fd4449e26687992e78f043e99fb49ac231d5517768cafb721e2f7c3d4d09e2a06efbd29fe711b6bde2db5b4cd4b3e5b999c964048d93c52559d5afc6778326620da4697dd8cf454ee743112336db126ef925315646ab49a0f1eaf04a37d2d570f6cc59dd4715e9bb02e4ed555e056293fcca6f2602d6151766c0e52dcc6276b80766c6550ebc97093c1a561c49a6e024403249e6796d30dc9ff6e4e805e897d38c31c8ab4db79b0c048c03ecb3474e404ed4467ba900000000','hex')),
  ('DBS-SIKA-SEALER', decode('524946469800000057454250565038208c000000f007009d012a600060003edd6eae51a8a6a422a4766a91101b896900d3d8103c3f6f1df4676a8bf4623c7e6a6cc159df5dd9c1d79b6b2c0f13c2c004dd83a250a1647b070296649000feeb362d204beda69817f01122595682bd77911fced1f1f22df35fcd09cf2a47bfc02a6fe8c020810bfdbd3ede646e601c8c901ac18dd4277179e3a9438e42ee100000','hex')),
  ('DBS-TB-ABERDEEN', decode('52494646c00200005745425056503820b4020000f010009d012a600060003edd60a84ea8a523a229d46dc1101b8969656883a8de612ac5d96014f473cc46d58d5e9efb29a5b13894b3427cb24891878f72a8f7aacf92ac12d7a82922cc15b8da0c8b4dbec4a884ed8f5109e7668fb59d00a1cf055fa2a74b02cfbbec87d03d6c849351389e8fb38e2e20518ba19200adac6f4c37b186ace9ed04cab539b74b9b6c2b8c0000feeef27428366a3007d51f8d2c6afec886c69aefa17d5ff9e65ffdfd1c932af698af34c5763c652bc80ecdf09b4411322795ae76cf9683fa77a4536a04996d2218f778b2d1e34a7deefcb595688cb82a4e98117d8094847602c1309f450b08be13be728979909da87ac1f545673db317b6079630428abdb38c792db9a2af32c298eec0507a11c4ea8e34623cc7f80f47ea6ee32721c5a8e2152aa9a06b8875f461260417cde3f3725640a942cd8e503a11799694fcf630af18801d0bd5ff8fd1f0bc6308bfaa7b537c7bfa4ffdb9eec5cffc384baf638d65f3bdbca97b6f13b02b73afba94448de159675ebbcd9ae83c5873fbf9ca360f56238d1b8daa036dd8de5a7cac991a3046cbacfdb4e700802cb6603fda9212234640c03236af3b3e3f8d12c6936c5b86c2ec494f9c9611f13f28dd91b594b750f7c9b3afaa6c185f0474fc499ebc28e21ada7f04ebb08f9e8080a61e8bf47088f55a7c15573c5e4fb4d7ff9877798a4b3d122264c4335b5fc46cc17d63a1a283818339fef3be75548792208ed410852b8da81cec9cfbd363b739812a697e1f0ffde56dda2d0a640471913e46e00b2c60059484c6a41f0cc0a454f9756d5c85628ac65ca684d8f763bfd7bf4796b282ce3461ae504a8fb220775e164f4b1e06dd0ce5f52e95d221bbdadc6f83b9de3972166283f8bb32403073db707abc5d765eef931504c75f10b122cfaa56144cb8006a2e3538ddd8af47806cb64fd508b826cb44c6f2cf34ea6d59e00000','hex')),
  ('DBS-TB-BLU60', decode('52494646aa01000057454250565038209e010000f009009d012a600060003ed95ea54ca825a3a231d4dc79001b09696bb0137b6f8577122c056e92e0a33e2695d5a186bd455a7c8a28c6c1e3f0b5d320684fdee460cae4a046a0c94e99bc8dfaff23a3d8fbc5bf16a013a28000fe8795b9ddf9beea9d173a6559c849fdebf0c707ad70fc43090e751e5cbed1fdb9ae7cf4e665bb1fc06d413d3ceedd60ecad4edb8e62d57b1f0e4894d6153622f7d6102e211c1384fe15448ecdd65b64bf6bb0e4712ce27cb3966efd49b18a006839b1f7d19140b4190210ad3be5ba49b6f3404ddb422f67dbad156d6c9e05827259aa094dc5cc1293a0dbaa53d1a29853c9547a4591cbeaa88d8d1bb3aaead18fe322e994f847fbe75ea921681959b8464c1d26a3483de170880fe5c883932e651ad8dd80c2a48094967999daf4ce465df9ed5ad3ffe57aa232bc52576bedb5fce5b50d6dfb345e5c0f789281628872fed6d0d7382d21547273dc89b9b8d5cd646feb6b570c2fcd33d88d159afedc049ba2ecbba565b3a5760a246274bbea6bbf8f29bce0764d16a4909eb11fa7dcb9cc334cab42a3023dd1e6f47d9581d5f62140420000','hex')),
  ('DBS-TB-BOREALIS', decode('52494646020100005745425056503820f6000000b007009d012a600060003edd60a951a8a52422a811ccb9101b89696c7b68c1c9f5f5808b9f7834044f247624e8c7c36fab1a57f074fd965eda9fcb1b2c66e9bb77334eaa690000feef01e0677bca5d36055ed074685e48ac554715fea323d6d7bd5ae0971eaacfad15d8bed548f3be0e0c1933e6d67a0cc1e98eae2f78985720d9f9bbbf0fedf6fbd900d7da05ad953bd6aac402f1ab6b88ebd43aa76278ed7b3861b01348bde3f4c308584789b98114fb5bc8824b1760828b32431609b7f1bdb3c9dfce38fec0aa7d4b706ec4eb4c8b288c7a306c3250440ad478fb05a017d37adebb7fe00bf746fbcb094283d405b624ffa3000000','hex')),
  ('DBS-TB-HYDR', decode('5249464650020000574542505650382044020000b00c009d012a600060003ec154a24ca7a523a239b95b68f0180969693a3a57c3a9825de848bfe54587a35852d2b0d4b745fc0f5d6b33e973cb090975400d8c60d4f474650a3b53037e79825c2f70106146a9f0ecb926871a29e648e731efde2116c8409c096bab402a039fb00a0000fe86b1f1fbc225e8de6a7c5f4037c4ffc7de41e1b440373d955263ac7ae82319e5b083f9d863057e07a235bdc66997fd736e3bcf69017844d71bccd997275b9eded5dd16bde812831ea6ad409f51c9f535d1ec3a2256ec57722bacf2f2a80d1655b600113310bab94a61d9b4ca8eabbac8f4411c3114e486742677a1f4cb358600e990908a29fef153927c5b415bc391de3bb63334802a868c64c09a0b5e1289587254d9be40e4e518085c3039f70fe6c05e2f6f6bfee8620785cf027a8a6326502b67ebb3d16e234b443f7234abe1f8aa8110a3b68c302d5c2263a07eddba2f99d9a1ddc6077ef43b2e722d5d85c820fea6ca1aa43ed7ecfda8b2e3b7a207705279ab17df418ed15f710692a949ea676f0a1ba304c92891d55ee44dd3bb817b4af067fb36c538722f44251d845497448816800f25f6614280cc5ff39b4a3b705328ea20b0a6bbf2d3aa0c479df937edb6865edfe337f271b1df2f7386734ef28e11c463380a701d48e566e223a5aac188d7efb0ec662abbf60b8084ab29ef2549e877a888a03371375b273bd3a52aec3795acf66770b3c78526ba3f176d6ad00bbe78d5523d756d845770f166c06ebee85095865c153754b77b9e8264ce59d1fc546919bc2b2cc707cfddded5a72b82148f6bf000','hex')),
  ('DBS-TB-MINICRETA', decode('524946465e0300005745425056503820520300009013009d012a600060003edd58a54ca8a523a232b55cc9101b896766a95db81cefc2669a56ddc03c627bacaa600d68daa40808aa79b41ab009ad46dd4bc488c9d8c005f62bb4311077494e5ad0e44c147229e20181db9945c9507bc3216b0ab4752205d5c5e33b7da746b5a12c0bbd8609bfa8948eb0b553230de980a81a531359f5dbd0834743106b6b0c4e44168ce545cc69da19130191b1f91a327b5390f3959604e6f800fecb3e272d588468639146e8b481e5af8b864311daf14dd0702580fa425b193be8884fcc66a969f26da41e97b94343fadaba758819ddc1add7b745e24351e5541c48580ed8459faa7f390007069c7b00f12cdc5c4df036efc8aef742ddc9559c1efeee7461240df2c6442809a4b2ef19d02548cbe7daf7c80b48467a598ef0ec0dcc9b9093e50adf0152fd088e069197f2b008395bff9f25dc270830e195d2d482c8dcd5e19ed97df3862a764a0d6d6114054baec314f96ada6ed3f04b4a544813d52171d666c5e5b26a8d7775ea6589fe9533b1d3b7f8f48dfffd232df740e2178705cc4dd012b873939d79eaea03379fe9bf5699d90e0668810b7bbb85fc8142ada640f76f167475e01e1fdd1e1a667b33178086d8e4fc28e495d85b9b747cdec171bd6315bc8fe76311c02bde7c8f0d288befbe8e4971c5b1daa27478d18cf1715df9fa0c307dc9fc0090178f8f3015c07456ac053714650f7e2d1308462c2cad6c308e12b9022bab4efb7e97939a015b1074b5b5eb4a509156364c70210c2534ddee4e7f4ef80027e49ca92f47c94b6f6f2feb99843b6485ea5aa67149baa76013eb65d5888c40e06b2fc75bc6cd14dde6c8a04f9badee4f9e53b1fcb5906652f5af916ad3863a2aaa16d0a6721299ebb606075505983856073160716cbeb5b56acec15a0dda92c267ccc95723a570e875ac642165d31fb0c6cedd5e627794b40648dd8f2644cb5835456c4c3470aed6b300bfe2e5b4e5261486cc53e47b41bed8b293244dc4b92ac22b5993c09b0fa7aa604bfee5823de6962c9f348bfa6bc0eae7f0c927e4b4e8e8cf41e230a5ab9605ab18f17f8a432e234205ee20272c22f8292c21c9313df7f73b9da099ffce6b5ecf862d1328c5428146f510f975fe1052a662def40ccb99756ab7daf9038f723b8302f98cdcd6f40cfcf2a4c7b2d9228cd9b8ffd65f30000000','hex')),
  ('DBS-UL-BRUSSELS', decode('52494646aa02000057454250565038209e020000300f009d012a600060003edd5aa44ca8a523a230180d51101b89676a0e524a05a7de40e732f4c09e3a5520001c933851f30153722b464c3d4ee68eeecc914bd891ca3a9e5f5a078cb4fb6dbd529e7f789c9df2d94d593fc7b78f82da008abafe9f75ecc24391089dfe12da72e0fbba4184413a426ce5c93499214fdc7163d2e0880000fe734599164c38c1e3109d8177e76c417accb93f25244514fb9801993405826b617d3899d115ecc92edd657013ffebfe23be8643cab9be2bb241e6f4b6edb25a7a0fe704b9fe860aea7e94a607700141a140609f688c47d5a90c0b8a7ea829fcd3d14b7d5ee1eb4d3b67a2ed6fa25ee4ca9f9e76ba0c04246fa8d3645457028c1af0e32b772602c7f2628afbca1ca467e6c88e4cbeff66d95ab9623b4eab89693d7575fea71c2d6730374ffc2c8445ad42c97bdcf067f3b1da8387bb550c6b97691694207643c783bcbce519f1defcaec979bad1d71ae4eaa5fe37eb23b00dbcdaed4075f9564064314c571eb392a5cd30307b788a9cf53bda858dc12cbb3ee708e22408fc3b5d41eae46b3f4b0b4881afedb6fc281d2982e075d40acda4efedf0c2970392ae42c61ef4f28556c81324178b6de653f921f8208072c4be1d468caabb223bcf968e1d84435a8db312f2d8f41f7cdda298409837c5e32c13a0196aa2781b0f5a061b409d2f0b8f4e32c925e0aae435bb567c05f9a8fbf27a8869328f123f205dfa734f3920946d96829bf94b54258420f67af7a505ff48c1c34a424a220fc686ecf530dc6f249adf60bab6f50164fa67fe8416aa63396a5003de0177ddf6c0b2326ee52ce51e70776b1932e7715f21e66d836cf7c4e835d806a06a42b794cdbc6339f1f984898db2836411cc81d4b72b1dcf3925941b8dfbcfc94c0f4a2e8ae45a92f299374045873a6eb9e49b90f22ba9b366058000','hex')),
  ('DBS-UL-ILCAMPO', decode('52494646fe0100005745425056503820f2010000d00b009d012a600060003edd66a94fa8aaa3a2281c6b41501b89696a318cd284880005be8b67dbb7aabc6906116f276ad86736906dcd542c89fdcf85a4e021a0933ffdf7761551ddffdaa3ff01c5178c7edb7be5d054b31c342bac1428576f0bd842f2c81be80000fedce4aad028a7499f8b87abc2f1bbf8597783c9e8886b1d1d3c79f6f855cb97415e1337c7ce1abb94548bd4afc1d02025aa20792272df8b57a9bdb92e296ee311830747bd6804fb504e9124fc5479a40b0c1d7f659e23d13974bbccbe3e1230b49aed2bd945300e1894bb692eb51985da416114a76de63481ff46f69c835bcbc43ddd4d8f872fc1e707991ebdfef4a7c2e08103e02e823a99a7633886029c95775285a673e10ade2dc43494ad01835c807d9f4a1ea3f8bbc7f957a2f6db106994786d5e4e55a36d0f580f0bd5d8316411a0150a7f5fa296aeaeb50fea02d6bafd4db8db2eecad037cc0e14ad88fd93d96b2a4e72f7e8eb4ea6eb546ec7764962a7063963c861e55253cd585c1b4c75984335d519b0f34dfb7267028ce7ccd9dc83f2aadb42dc3b9ec4fead11b13127b5090aa8fdeecf4c751edb87144d7ada861198764ca26531f8403f97460c82e64f648c57b0031908d3b1a931434dc182aa8327f5fbe58b379dbadae91a0d2fe94e7264f3b25b3175ef7ea5d72e450868ecb61533b95dc80800000','hex')),
  ('DBS-UL-SERIES3000', decode('524946461e020000574542505650382012020000d00b009d012a600060003ed95aa54ca82523a231d4dd79001b0969674a3ad860d3012e47917373cb0aabf1d7ccb4893d0fa3892d7a985e618fd2c43b656ea877c944666843e9dcb86c4acee4decef48e2283b13aab2df2778b35a50c4412327c6a1126175c4c0000feba83d940c5cacbba9dffb0d9c899acb004a88af1011cdcd07aaaecabf20d792679cfd0785986e9576074ed295cc5d9c2fba9a3e3d7d28aa4d8551f0925222a4aaca2144351c68cc0d70c1e29dd632a489b86c0018cee5d9a32aff7325bed901ecc34bc650197b5f3afbded6f66a56deae7c242bf3fac4767190932f03c07aeae7d2e382bb54d60db02244d6705a78f326b32b5f4f8d6714d67284c0959f78facfd06e437e234bc61207ac8d615dc228d8e72ce4e4a82cb297f06735ab810caf3d377600267e4dd6c50074d67493965f613581f89a5596cac0b839933adc1fbc9dcf3281bd6f2658b55c184b58b09b7aaadcd04de8cfe44704a966c7d74b366431807b06dd1c753965d727fe0ea49ebc3cd011ed813446adc7d91a4f3ef2f4fcef1e1b85268ed3cadef1054910478640ad9f24fb54ca4e4c18294ddd7e37d7cc7989085f0ee2f6b6ff5908c98c983b2f807f35683a2063c625a3725a7bd5357e1575f7be43e10e4d44903f9e27959ceb2a61cfd5f4cd593097114a09048fa3a2407b8aca28dfb2781c1568d22d1882cb11e496e0f991eef8edd400eb581fca943f117a10ef7f2b40000','hex')),
  ('DBS-UL-THORNBURY', decode('524946467c0300005745425056503820700300009014009d012a600060003edd58a44ca8a523a230180dc1101b896965b45db81dbe24a985bfd43a9c1a62a36329d6f7217a4219bfbb9eee8a71b8213a4f3eab9705333faeec5e4b1e59666affb969b17a315581826af9cfaf13da17bea801718dc5f024a4ac7f795c6bc67b3beeee5c5aee20e9584e566e457cc624ec247be020caf5f885914d4f78c475c7c533841e6b298160acd4dcfb194cc246aff42b9d2c88e5b81db80fc01356039b1ea000fecfb4dadbbe5269a63d9072085ec1371dca0357ec26ecb3d97d63da12c463028f7fc352fd2bab7f56da518229dfd5cdb5e5e864643a3f8a70a5cc9e3a0f5005320851a2cf3046748b5024ba0cd5f652eaf9bb0d56b7c1d1bb3789680b9b1d4e917501ce11fa5f445fcf03c7a379495c854998ac27b0fea76736ae3af809ce349007fae7a2f54393e36f3681c3fa60c175782155c4797a6fb0578b6e296d6a35af688ac7ecb8897fca049555715e40bb1f838500f9a7ef633431706009509327bb92b81edf279dcd11571439feb1e1468df4da60a86c3759a9ca0e2365161ca72d71ae0377bd0245e7087c60170749c923c06acbcd39504319709f06a02ad0c80760786c5859ed66cd39cab31d2d57315291b28bc4b580e3c43b579e7d51a157d6c435c64491bf991a267de4c1df8e9a1a248e16af8fed9d0eef4e4be8ee581e736b4a22d1fdd4f3d9c10fae4f0d8a2d2e5821ba5150f6986c78b01a590dbe3ce8bdfa00013348937e92f635b50f0dec811b41b9e3c08fe46286fcfadbd7b0069c54546f47babdb3276fa0bb260f39920c051e4198f958200bdbcb0a829b000d6460bf97fd8b9b45e40e82aae63cbaf912d1dce2b1734525ba78a60133c6bbe815c1228ffd7b8a88b8e63f9c69b4c3642e7a43887dfc1fda84c093622ab1b3d82a15539da251081fbca66bc9e28f6103325bca69e8ffcc1c09e3bc7b83eb10370080ff1633d32c64127ac7bdd960419ec347adb401e366401d73a85cd36400bacdd87d6e9e7f7b4ffc20addb5ab9581909b9729edf49671f7d3e3f0f4fffa82edeaaac3d234a97449bd344a563399ad8cb87a5158b3ac0b339b7c7b2d4befad47cdd11d2cc8677921371f26a1563033fd5fea4cc229acb7a3a2894eceb458705ffc3502171daf47f988138172bca304c1fe47a0a121919d22dcc77148de35f60f272c35e579e7f3d49e9ec736934e24b0510dfa65f0d3db74d683fa13c3021fff800','hex'))
) AS v(sku, b)
JOIN products p ON p.sku = v.sku
ON CONFLICT (product_id) WHERE sort_order = 0 DO NOTHING;

-- ───────────────────────── DIB-488 rev 8 · demo weight/cost/favorites + the DIB-502 pairing map ──
-- All idempotent (guarded UPDATEs / ON CONFLICT DO NOTHING) so a re-seed is a no-op.

-- (item 9) The three DBS products whose weight_per_unit_kg the original seed left NULL. Realistic
-- magnitudes per the ruling: bulk aggregate ≈ a metric tonne per T base unit (~2,200 lbs, the
-- same figure the other T-based aggregates already carry); the Brussels paver ≈ the other pavers'
-- per-piece weight. Only fills the gap, never overwrites an authored weight.
UPDATE products SET weight_per_unit_kg = 1000.0
 WHERE sku IN ('DBS-AGG-SAND', 'DBS-AGG-34CLEAR') AND weight_per_unit_kg IS NULL;
UPDATE products SET weight_per_unit_kg = 3.6
 WHERE sku = 'DBS-UL-BRUSSELS' AND weight_per_unit_kg IS NULL;

-- (item 10) Demo per-branch WAC rows, THE pricing engine's cost source (product_branch_costs is
-- what the low-margin scan and inventory costing read). Derived deterministically from each
-- product's own retail anchor at a realistic dealer cost ratio (~64% of list → ~36% margin),
-- jittered per-SKU by the sku hash so the demo margins aren't one flat number. Guarded: only
-- where the anchor is in the product's BASE uom (the WAC's unit), and never overwriting a real
-- cost row (ON CONFLICT DO NOTHING, receiving/landed-cost writes always win).
INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
SELECT p.id, b.branch_id, GREATEST(1, ROUND(plp.list_price_cents * (0.58 + (hashtext(p.sku::text) & 15) / 100.0)))::bigint
  FROM products p
  JOIN pricing_list_prices plp ON plp.product_id = p.id AND plp.price_uom = p.base_uom
  CROSS JOIN (VALUES ('5d1b3f6e-0000-4000-9000-000000000001'::uuid),
                     ('5d1b3f6e-0000-4000-9000-000000000002'::uuid)) AS b(branch_id)
 WHERE p.sku LIKE 'DBS-%'
ON CONFLICT (product_id, branch_id) DO NOTHING;

-- (item 4) A short favorites set for the ★ Favorites chip, the counter's everyday movers.
UPDATE products SET is_featured = TRUE
 WHERE sku IN ('DBS-BWS-ANTICO', 'DBS-GATOR-SAND', 'DBS-AGG-34CLEAR',
               'DBS-TB-BLU60', 'DBS-UL-BRUSSELS', 'DBS-SIKA-SEALER')
   AND NOT is_featured;

-- (DIB-502 / rev-8 item 8) The seeded category-level co-purchase pairing map (0169). DOMAIN pairs
-- expressed in this catalogue's own application vocabulary, pavers/patio → jointing sand,
-- bedding, sealer; walls/steps → coping (caps) and base; aggregate (drainage/bedding) → base
-- fabric; turf → bedding. Data-driven by design: real co-purchase analysis replaces these rows
-- without any code change.
INSERT INTO pos_addon_pairings (source_application, suggested_application, rank) VALUES
  ('Paver',    'Jointing', 10),
  ('Paver',    'Bedding',  20),
  ('Paver',    'Sealing',  30),
  ('Patio',    'Jointing', 10),
  ('Patio',    'Bedding',  20),
  ('Patio',    'Sealing',  30),
  ('Wall',     'Coping',   10),
  ('Wall',     'Base',     20),
  ('Step',     'Coping',   10),
  ('Step',     'Base',     20),
  ('Drainage', 'Base',     10),
  ('Bedding',  'Base',     10),
  ('Landscape','Bedding',  10),
  ('Coping',   'Sealing',  10)
ON CONFLICT (source_application, suggested_application) DO NOTHING;
