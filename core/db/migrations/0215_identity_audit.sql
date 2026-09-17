-- 0215_identity_audit.sql: HH-3.1 Phase A additive migration, identity and audit (D3, D28).
--
-- D28: no durable Team-membership cache table exists anywhere in this schema, and this
-- file adds none. Org membership resolves against the platform per request, held at most
-- in a short-lived in-process cache (HH-3.3). The credential (the platform JWT verified
-- against the JWK Set) is never cached and never stored here.
--
-- Additive only: new columns on carried tables are nullable, new tables start empty, no
-- destructive statement, the running ERP (carried baseline 0001 to 0212, D33) is
-- unaffected. Platform user id is the platform JWT subject, held as TEXT. Carried table
-- names are taken from the analysis inventory and domain map (the ERP tree itself is out
-- of lane): org(id) (domain-map 5.1), locations (0003), users (0004), portal_users (0019,
-- widened 0160), dispatch_trucks (0090s), audit_log (0013, seq 0078).

-- Staff role catalogue (D3, C30): the thirteen staff roles of role-catalog.yaml, the
-- platform role above them (super_admin, "13 roles in 3 tiers plus a platform role"),
-- and the two internal roles the catalog sources outside the ERP (dealer_admin,
-- fb_operator), so every role the contract gates on exists in one table.
CREATE TABLE staff_role (
    id               TEXT PRIMARY KEY,
    label            TEXT NOT NULL,
    tier             SMALLINT NOT NULL CHECK (tier IN (1, 2, 3)),
    scope            TEXT NOT NULL CHECK (scope IN ('global', 'branch')),
    origin           TEXT NOT NULL CHECK (origin IN ('hardscapeos', 'hhpro', 'new')),
    origin_code      TEXT NULL,
    is_platform_role BOOLEAN NOT NULL DEFAULT FALSE,
    seq              BIGINT NULL,        -- server-assigned, per scope (C34)
    deleted_at       TIMESTAMPTZ NULL    -- tombstone (C35)
);
INSERT INTO staff_role (id, label, tier, scope, origin, origin_code, is_platform_role) VALUES
    ('general_manager',      'General Manager',          1, 'global', 'hardscapeos', 'GENERAL_MANAGER',      FALSE),
    ('financial_controller', 'Financial Controller',     1, 'global', 'hardscapeos', 'FINANCIAL_CONTROLLER', FALSE),
    ('hr',                   'HR',                       1, 'global', 'hardscapeos', 'HR',                   FALSE),
    ('branch_manager',       'Branch Manager',           2, 'branch', 'hardscapeos', 'BRANCH_MANAGER',       FALSE),
    ('sales_manager',        'Sales Manager',            2, 'branch', 'hardscapeos', 'SALES_MANAGER',        FALSE),
    ('yard_manager',         'Yard Manager',             2, 'branch', 'hardscapeos', 'YARD_MANAGER',         FALSE),
    ('logistics_manager',    'Logistics Manager',        2, 'branch', 'hardscapeos', 'LOGISTICS_MANAGER',    FALSE),
    ('procurement_manager',  'Procurement Manager',      2, 'branch', 'hardscapeos', 'PROCUREMENT_MANAGER',  FALSE),
    ('payables_receivables', 'Payables and Receivables', 2, 'global', 'hardscapeos', 'PAYABLES_RECEIVABLES', FALSE),
    ('inside_sales',         'Inside Sales',             3, 'branch', 'hardscapeos', 'INSIDE_SALES',         FALSE),
    ('outside_sales',        'Outside Sales',            3, 'branch', 'hardscapeos', 'OUTSIDE_SALES',        FALSE),
    ('yard_team',            'Yard Team',                3, 'branch', 'hardscapeos', 'YARD_TEAM',            FALSE),
    ('driver',               'Driver',                   3, 'branch', 'hardscapeos', 'DRIVERS',              FALSE),
    ('super_admin',          'Platform Administrator',   1, 'global', 'hardscapeos', 'SUPER_ADMIN',          TRUE),
    ('dealer_admin',         'Dealer Administrator',     1, 'global', 'hhpro',       'HHPRO_ADMIN_TOKEN',    FALSE),
    ('fb_operator',          'FutureBuild Operator',     1, 'global', 'new',         'operator',             FALSE);
-- The catalog gives driver two origin codes (DRIVERS, dispatch_drivers); the first is carried.

-- Role grants keyed by platform user id (D3). branch_id scopes a branch-scoped grant to
-- one branch; for a role whose staff_role.scope is 'branch', the identity layer refuses a
-- grant with a NULL branch_id, fail closed (C31); for a 'global' role branch_id is NULL.
CREATE TABLE staff_role_grant (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    platform_user_id TEXT NOT NULL,
    role_id          TEXT NOT NULL REFERENCES staff_role(id) ON DELETE RESTRICT,
    org_id           UUID NOT NULL REFERENCES org(id) ON DELETE RESTRICT,
    branch_id        UUID NULL REFERENCES locations(id) ON DELETE RESTRICT,
    granted_by       TEXT NULL,          -- platform user id of the granter
    seq              BIGINT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at       TIMESTAMPTZ NULL
);
CREATE UNIQUE INDEX staff_role_grant_unique
    ON staff_role_grant (platform_user_id, role_id, org_id,
                         COALESCE(branch_id, '00000000-0000-0000-0000-000000000000'::uuid))
    WHERE deleted_at IS NULL;

-- Platform identity links on the carried identity tables (domain-map 5.1: "platform
-- identity, text claim, new"). Nullable, unique where present, never read by the ERP.
-- The unified model calls portal_users tenant_user; renaming is destructive to the
-- running ERP, so the carried name stays and the additive set extends it. The canonical
-- contractor role set (account_admin, buyer, buyer_no_pay, ap_finance, field_crew) is
-- already enforced by the carried CHECK (0160); the role column is untouched.
ALTER TABLE users ADD COLUMN IF NOT EXISTS platform_user_id TEXT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS users_platform_user_id_key
    ON users (platform_user_id) WHERE platform_user_id IS NOT NULL;
ALTER TABLE portal_users ADD COLUMN IF NOT EXISTS platform_user_id TEXT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS portal_users_platform_user_id_key
    ON portal_users (platform_user_id) WHERE platform_user_id IS NOT NULL;

-- One-time mapping from HH Pro's contractor role set onto the canonical ERP set (D3).
-- A NULL canonical_role marks a value with no canonical equivalent: it reconciles to a
-- capability, not a role (D3: any extras become capabilities).
CREATE TABLE hhpro_contractor_role_map (
    hhpro_role     TEXT PRIMARY KEY,
    canonical_role TEXT NULL CHECK (canonical_role IN
        ('account_admin', 'buyer', 'buyer_no_pay', 'ap_finance', 'field_crew')),
    note           TEXT NULL
);
INSERT INTO hhpro_contractor_role_map (hhpro_role, canonical_role, note) VALUES
    ('owner',     'account_admin', NULL),
    ('pm',        'buyer_no_pay',  NULL),
    ('ap',        'ap_finance',    NULL),
    ('field',     'field_crew',    NULL),
    ('estimator', NULL,            'HH Pro User.role value; that enum is dropped (C29), reconciles to a capability if it appears');

-- Driver profile: driver is a role (staff_role 'driver') plus this row, not a separate
-- identity (D3), so one person can be yard staff and a driver. Vehicle and licence live
-- here. The dispatch registry (dispatch_trucks, dispatch_drivers, carried 0090 to 0093)
-- stays the logistics manager's registry; reconciling the two is a tracking-lane
-- question, not schema.
CREATE TABLE driver_profile (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    platform_user_id TEXT NOT NULL,
    org_id           UUID NOT NULL REFERENCES org(id) ON DELETE RESTRICT,
    truck_id         UUID NULL REFERENCES dispatch_trucks(id) ON DELETE RESTRICT,
    licence_number   TEXT NULL,
    licence_class    TEXT NULL,
    licence_expiry   DATE NULL,
    is_active        BOOLEAN NOT NULL DEFAULT TRUE,
    seq              BIGINT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at       TIMESTAMPTZ NULL
);
CREATE UNIQUE INDEX driver_profile_user_org_key
    ON driver_profile (platform_user_id, org_id) WHERE deleted_at IS NULL;

-- Audit names the acting person, not the device session. The carried audit_log attributes
-- through the authenticated session identity; on a shared device the acting person is the
-- unlocked user (context 4.5), which that shape cannot express, so the column is added,
-- additive and nullable. The carried actor column is untouched and the ERP keeps writing
-- it. The identity middleware populates this from the JWT subject on privileged actions,
-- and the device-unlock path writes the unlocked user.
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS acting_platform_user_id TEXT NULL;
