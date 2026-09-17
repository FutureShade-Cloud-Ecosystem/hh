-- 0058: L8 landed-cost — (1) SCHEMA: snapshot column for the cent-exact Apply/Void round-trip, and (2) a
-- DEMONSTRABLE landed-cost fixture so Apply / Void / GL 1200 tie-out can be exercised on STAGING via the
-- EXISTING landed-costs endpoints.
--
-- ── PART 1 — SCHEMA (L8 MAJOR): landed_cost_allocations.pre_apply_avg_unit_cost ─────────────────────────────
-- Apply snapshots the EXACT pre-Apply weighted-average unit cost it capitalized over; Void restores that
-- snapshot verbatim instead of recomputing via round(). round(oldAvg × on-hand) quantizes oldAvg away when
-- on-hand is a sub-unit fraction (e.g. oh=0.25, oldAvg=1 → oldValue=0), so the legacy reverse-via-negate Void
-- could not reconstruct oldAvg and drifted the WAC by ±cents on an Apply/Void round-trip with no sale between.
-- Restoring the persisted snapshot is the cent-exact inverse for ALL on-hand → round-trip drift == 0. NULL for
-- rows written before this migration (and for no-on-hand / no-prior-cost shares that have nothing to restore):
-- Void falls back to the legacy negate path for those, so already-applied docs stay voidable. IDEMPOTENT via
-- IF NOT EXISTS.
ALTER TABLE landed_cost_allocations
  ADD COLUMN IF NOT EXISTS pre_apply_avg_unit_cost BIGINT;  -- exact pre-Apply avg_unit_cost_cents; NULL = no snapshot

-- ── PART 2 — DEMONSTRABLE FIXTURE (L8 DEMO-GAP: 4 ACs) ──────────────────────────────────────────────────────
-- Before this seed, staging's ONLY landed_cost is a DRAFT with an empty po_id → preview/apply 422; there is no
-- received PO to allocate across, so Apply/Void/tie-out cannot be shown. This seeds a complete, self-consistent
-- chain so POST /api/v1/landed-costs/{id}/apply posts a balanced DR 1200 / CR 2100 journal, GL 1200 rises by the
-- capitalized freight, and POST .../void reverses it exactly:
--   • a RECEIVED purchase_orders header + two received po_lines (received_base > 0, priced),
--   • branch on-hand (stock_balances) + a prior weighted-average (product_branch_costs) for both products so the
--     capitalization has inventory to absorb into (and the snapshot column above gets exercised), and
--   • a DRAFT landed_cost tied to that po_id (CAD, VALUE basis) → Applyable via the existing endpoints.
--
-- WHY A MIGRATION (not cmd/seed): the staging deploy runs ONLY migrations (bin/migrate); dibbits_seed.sql does
-- not exist on staging (same rationale as 0046/0049/0056/0057). Fixed UUIDs for every row we CREATE + guards +
-- ON CONFLICT DO NOTHING make re-runs a no-op. PREREQUISITES (vendor, products, branch, uom) are resolved by
-- STABLE natural keys (sku / code), never hardcoded UUIDs, because those differ across environments; if any
-- prerequisite is missing the migration skips safely rather than seeding a broken chain. Fresh-migrate-safe:
-- purchase_orders/po_lines (0011), stock_balances/product_branch_costs (0037), landed_costs (0041) all pre-exist.
-- MONEY: integer cents (BIGINT); quantities DECIMAL(19,4).
DO $$
DECLARE
  v_po    UUID := 'd0580000-0000-4000-9000-000000000001';  -- demo PO header
  v_l1    UUID := 'd0580000-0000-4000-9000-000000000011';  -- demo po_line 1
  v_l2    UUID := 'd0580000-0000-4000-9000-000000000012';  -- demo po_line 2
  v_lc    UUID := 'd0580000-0000-4000-9000-000000000099';  -- demo landed_cost (DRAFT, Applyable)
  v_p1    UUID := 'd0580000-0000-4000-9000-000000000021';  -- demo product 1 (self-contained)
  v_p2    UUID := 'd0580000-0000-4000-9000-000000000022';  -- demo product 2 (self-contained)
  v_branch UUID;
  v_vendor UUID;
  v_uom   TEXT;
BEGIN
  -- Idempotency: if the demo landed cost already exists, this migration already ran — no-op.
  IF EXISTS (SELECT 1 FROM landed_costs WHERE id = v_lc) THEN
    RETURN;
  END IF;

  -- Resolve prerequisites by STABLE natural keys (all created by earlier MIGRATIONS, so this is fresh-migrate /
  -- staging-safe and does NOT depend on cmd/seed): TRT branch (0003), a vendor (0011-seeded fixed UUIDs), and the
  -- EA unit of measure (0002). Skip safely if any is absent (a non-Dibbits DB). The demo PRODUCTS are created by
  -- THIS migration (fixed UUIDs) rather than reused from dibbits_seed.sql, because that seed file is not applied on
  -- staging — reusing catalog SKUs would make the fixture skip there and the demo would remain un-demonstrable.
  SELECT id INTO v_branch FROM locations WHERE code = 'TRT';
  SELECT id INTO v_vendor FROM vendors ORDER BY name LIMIT 1;
  SELECT code INTO v_uom FROM uoms WHERE code = 'EA';
  IF v_branch IS NULL OR v_vendor IS NULL OR v_uom IS NULL THEN
    RAISE NOTICE '0058: demo prerequisites missing (branch/vendor/uom) — skipping fixture seed';
    RETURN;
  END IF;

  -- 0. Self-contained demo products (EA base uom; weight set so WEIGHT basis is also meaningful).
  INSERT INTO products (id, sku, name, base_uom, price_uom, weight_per_unit_kg, is_active)
  VALUES
    (v_p1, 'LC-DEMO-PROD-1', 'Landed-Cost Demo Product 1', v_uom, v_uom, 20.0, TRUE),
    (v_p2, 'LC-DEMO-PROD-2', 'Landed-Cost Demo Product 2', v_uom, v_uom, 35.0, TRUE)
  ON CONFLICT (id) DO NOTHING;

  -- 1. RECEIVED purchase order the landed cost allocates across.
  INSERT INTO purchase_orders (id, po_no, vendor_id, branch_id, status, currency, fx_rate)
  VALUES (v_po, 'LC-DEMO-PO-1', v_vendor, v_branch, 'RECEIVED', 'CAD', 1.0)
  ON CONFLICT (id) DO NOTHING;

  -- 2. Received PO lines (received_base > 0 so ReceivedLinesForPO returns them; priced so VALUE basis has weight).
  --    line 1: 40 base units @ $30.00 = extended $1,200.00 ; line 2: 60 base @ $70.00 = extended $4,200.00.
  INSERT INTO po_lines (id, po_id, line_no, product_id, qty_entered, uom_entered, qty_base, received_base,
                        unit_cost_cents, cost_uom)
  VALUES
    (v_l1, v_po, 1, v_p1, 40, v_uom, 40, 40, 3000, v_uom),
    (v_l2, v_po, 2, v_p2, 60, v_uom, 60, 60, 7000, v_uom)
  ON CONFLICT (id) DO NOTHING;

  -- 3. Branch on-hand for both products so the capitalization has inventory to absorb into (location_id == branch;
  --    branches ARE locations in this chart). Additive upsert mirrors the inventory writer.
  INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, updated_at)
  VALUES (v_p1, v_branch, v_branch, 40, now()),
         (v_p2, v_branch, v_branch, 60, now())
  ON CONFLICT (product_id, location_id) DO NOTHING;

  -- 4. Prior weighted-average cost per product/branch so a snapshot exists to capitalize over (and to restore on
  --    Void). Matches the received unit costs above.
  INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents, updated_at)
  VALUES (v_p1, v_branch, 3000, now()),
         (v_p2, v_branch, 7000, now())
  ON CONFLICT (product_id, branch_id) DO NOTHING;

  -- 5. DRAFT landed cost tied to the PO — Applyable via POST /api/v1/landed-costs/{id}/apply. CAD, VALUE basis.
  --    $135.00 freight allocates value-proportionally: $1,200 : $4,200 → 2916 / 10584 CAD-cents.
  INSERT INTO landed_costs (id, po_id, po_ref, branch_id, currency, fx_rate,
                            freight_cents, duty_cents, handling_cents,
                            freight_cad_cents, duty_cad_cents, handling_cad_cents,
                            allocation_basis, exclude_special_buys, status)
  VALUES (v_lc, v_po, 'LC-DEMO-PO-1', v_branch, 'CAD', 1.0,
          13500, 0, 0, 13500, 0, 0,
          'VALUE', TRUE, 'DRAFT')
  ON CONFLICT (id) DO NOTHING;

  RAISE NOTICE '0058: seeded demo landed cost % (PO %) — Apply/Void/tie-out now demonstrable', v_lc, v_po;
END $$;
