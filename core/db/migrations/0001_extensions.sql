-- 0001: platform extensions only.
-- The real baseline schema (products, UoM, locations, customers, orders, GL)
-- is DESIGNED in G2 (Step 0.3) and authored as 0002+ after spec approval.
-- Greenfield discipline: no schema before its data-model design is approved.
CREATE EXTENSION IF NOT EXISTS pgcrypto;   -- gen_random_uuid() for UUID v4 PKs
CREATE EXTENSION IF NOT EXISTS citext;     -- case-insensitive emails/codes
