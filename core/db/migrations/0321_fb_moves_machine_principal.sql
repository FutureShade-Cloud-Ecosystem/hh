-- 0321_fb_moves_machine_principal.sql
-- Owner module: internal/identity (HH-LOAD-1 LD-2, ADR-0024).
-- Additive only: one new table, nothing else touched, starts empty.
-- The machine principal is FB Move's own credential subject for the load
-- doors: one row per dealer org, naming the org it belongs to, its name,
-- whether it is enabled, and the doors (the contract operation ids) it may
-- call. It holds no staff role and appears in no roleconfig cell, so no
-- staff role, no roleconfig cell and no dealer setting can widen it: the
-- door list is read here and nowhere else. The token subject names the id;
-- the private key stays in FB Move's vault and nothing of it lands here.
CREATE TABLE machine_principal (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     UUID NOT NULL REFERENCES org(id) ON DELETE RESTRICT,
    name       TEXT NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    doors      TEXT[] NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX machine_principal_org_name_key ON machine_principal (org_id, name);
