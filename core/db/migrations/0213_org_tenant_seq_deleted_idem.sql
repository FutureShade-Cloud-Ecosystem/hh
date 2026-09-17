-- 0213_org_tenant_seq_deleted_idem.sql
-- Owner module: internal/platform (additive pass, HH-3.1 Phase A).
-- Additive only: every column nullable; no constraint, index, rename or drop,
-- so the running ERP on baseline 0001..0212 is unaffected.
-- Tenancy (context 4.4, domain-map section 5, C33): org_id on org-owned rows,
-- tenant_id on rows belonging to a dealer's customer. No foreign keys yet: the
-- org anchor table lands in a later migration of this Session, and carried
-- customer_id cannot be renamed non-destructively, so tenant_id is additive
-- beside customer_id and resolved at the API layer.
-- seq (C34, context 4.5): per-scope monotonic sequence for the 43 manifest
-- sync scopes. Server-side allocator design: scope_sequences follows the
-- order_no_sequences precedent (0087), one counter row per scope; the server
-- assigns seq inside the business transaction with INSERT INTO
-- scope_sequences(scope) VALUES (s) ON CONFLICT (scope) DO UPDATE SET
-- next_value = scope_sequences.next_value + 1 RETURNING next_value, gaps
-- allowed, never reused. Carried rows keep seq NULL: a backfill order would
-- not be authoritative (the 0078 warning); ERP-written rows stay NULL until
-- the unified core owns the write path.
-- deleted_at (C35): tombstone on every synced carried table (protocol deletes
-- are tombstones). is_active stays a business state, deleted_at a sync fact.
-- idempotency_key (D30): client-generated, on every synced carried table,
-- nullable at the database level; enforcement is at the API boundary, never by
-- a not-null or unique constraint, so no ERP writer or suite can break on it.
-- org, the dealer organisation (domain-map 5.1): the anchor every org_id
-- references. Neither source repository has one; the ERP is single-org today.
CREATE TABLE org (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug       CITEXT UNIQUE NOT NULL,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE scope_sequences (
    scope      TEXT PRIMARY KEY,
    next_value BIGINT NOT NULL DEFAULT 1
);
DO $$ DECLARE t TEXT;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'audit_log','cost_layers','customer_transactions','customers','delivery_runs',
    'delivery_stops','events_outbox','invoice_lines','invoices','locations',
    'manifest_lines','manifest_versions','manifests','order_lines','orders',
    'payment_allocations','payments','po_lines','po_receipts','price_batches',
    'price_change_lines','price_change_log','price_import_profiles',
    'price_import_runs','pricing_anchor_policy','pricing_audit_log',
    'pricing_category_policy','pricing_clearance_items','pricing_list_prices',
    'pricing_product_policy','pricing_promotions','pricing_rules',
    'pricing_uom_conversions','product_branch_costs','product_categories',
    'product_rounding_rules','product_uoms','products','purchase_orders',
    'quantity_break_headers','quote_lines','quotes','rebate_accruals',
    'rebate_programs','roles','stock_balances','stock_moves','task_events',
    'task_pick_lines','tasks','uoms','user_branch_grants','user_roles','users',
    'vendors'
  ] LOOP
    EXECUTE format('ALTER TABLE %I ADD COLUMN IF NOT EXISTS org_id UUID', t);
  END LOOP;
  FOREACH t IN ARRAY ARRAY[
    'audit_log','customer_category_overrides','order_lines','orders',
    'portal_payment_methods','portal_users','procurement_project_lines',
    'procurement_projects','pricing_rules','projects','return_request_lines',
    'return_requests'
  ] LOOP
    EXECUTE format('ALTER TABLE %I ADD COLUMN IF NOT EXISTS tenant_id UUID', t);
  END LOOP;
  FOREACH t IN ARRAY ARRAY[
    'count_lines','count_sheets','customer_category_overrides',
    'customer_transactions','customers','dashboard_profile_widgets',
    'dashboard_user_widgets','delivery_pod_photos','delivery_runs',
    'delivery_stops','dispatch_drivers','dispatch_trucks','invoice_lines',
    'invoices','load_scan_checkpoints','msg_acks','msg_channels','msg_messages',
    'msg_notification_prefs','order_lines','orders','payment_allocations',
    'payments','permission_approval_requests','po_lines','po_receipts',
    'portal_payment_methods','portal_users','pos_parked_tickets',
    'pricing_clearance_items','pricing_list_prices','pricing_promotions',
    'pricing_rules','procurement_project_lines','procurement_projects',
    'product_categories','products','projects','purchase_orders',
    'purchasing_po_lines','purchasing_pos','purchasing_receipt_lines',
    'purchasing_receipts','quote_lines','quotes','stock_balances','stock_moves',
    'task_pick_lines','task_shortages','tasks','vendors','warranty_claims',
    'will_call_pickup_events','will_call_pickups'
  ] LOOP
    EXECUTE format('ALTER TABLE %I ADD COLUMN IF NOT EXISTS seq BIGINT', t);
  END LOOP;
  -- Synced carried tables (the 43-scope tables plus the read-cache reference
  -- tables of domain-map section 6): tombstone and client idempotency key.
  FOREACH t IN ARRAY ARRAY[
    'count_lines','count_sheets','customer_category_overrides',
    'customer_transactions','customers','dashboard_profile_widgets',
    'dashboard_user_widgets','delivery_pod_photos','delivery_runs',
    'delivery_stops','dispatch_drivers','dispatch_trucks','invoice_lines',
    'invoices','load_scan_checkpoints','locations','manifest_lines',
    'manifest_versions','manifests','msg_acks','msg_channels','msg_messages',
    'msg_notification_prefs','order_lines','orders','payment_allocations',
    'payments','permission_approval_requests','po_lines','po_receipts',
    'portal_payment_methods','portal_users','pos_parked_tickets',
    'pricing_clearance_items','pricing_list_prices','pricing_promotions',
    'pricing_rules','pricing_uom_conversions','procurement_project_lines',
    'procurement_projects','product_categories','product_rounding_rules',
    'product_uoms','products','projects','purchase_orders',
    'purchasing_po_lines','purchasing_pos','purchasing_receipt_lines',
    'purchasing_receipts','quote_lines','quotes','roles','stock_balances',
    'stock_moves','task_pick_lines','task_shortages','tasks','uoms',
    'user_branch_grants','user_roles','users','vendors','warranty_claims',
    'will_call_pickup_events','will_call_pickups'
  ] LOOP
    EXECUTE format('ALTER TABLE %I ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ', t);
    EXECUTE format('ALTER TABLE %I ADD COLUMN IF NOT EXISTS idempotency_key UUID', t);
  END LOOP;
END $$;
