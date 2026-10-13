-- 0388_org_signin_method.sql
-- Owner module: internal/identity (HH-4c item 6c, the dealer's allowed sign
-- in methods, state note section 5b).
-- Additive only: one new table, nothing else touched, starts empty.
-- One row per (org, method code) the dealer allows. An org with no row
-- gets the default set in the reader (email_password and google), so this
-- migration changes no dealer's sign in. The code is NOT constrained here:
-- the vocabulary is closed in code, and the reader refuses an unknown code
-- as a data defect instead of skipping it, so a bad row is loud.
CREATE TABLE org_signin_method (
    org_id UUID NOT NULL REFERENCES org (id),
    code   TEXT NOT NULL,
    PRIMARY KEY (org_id, code)
);
