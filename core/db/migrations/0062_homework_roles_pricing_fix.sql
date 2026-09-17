-- 0062: Homework polish fix — applied AFTER 0061 already ran on staging (migrations run once),
-- so the corrections below live in their own migration. Two fixes surfaced by walking the flows
-- live as the seeded users:
--   (1) ROLE DETERMINISM: on staging the homework emails pre-existed (07-14 cmd/seed) and already
--       carried OTHER roles, so 0061's ON CONFLICT (user_id,role_id) DO NOTHING left Scrubname0030/Scrubname0016 with
--       a stray GENERAL_MANAGER — Scrubname0030 then self-overrode the till manager-gate instead of being
--       blocked, undercutting that homework step. Clear the 4 homework accounts' role links and
--       reassign the intended one: Scrubname0030/Scrubname0016 = INSIDE_SALES (true frontline — hit the real manager
--       block), Scrubname0025/RyanR = GENERAL_MANAGER (Pricing / GL+Scrubname0029 manager-gated flows).
--   (2) PRICING: 0061's realistic $9.75/SF Brussels anchor was SKIPPED by ON CONFLICT DO NOTHING
--       because a stale $3.00/SF row pre-existed, so the ring showed ~$2.85/SF (unrealistically low,
--       mismatching the guide). Overwrite the provisional anchors to the sensible numbers.
-- Idempotent (delete-then-insert on a fixed set; pricing DO UPDATE). Scoped to the 4 demo accounts.

-- (1) deterministic homework roles
DELETE FROM user_roles ur
 USING users u
 WHERE ur.user_id = u.id
   AND u.email IN ('scrubbed-0004@example.invalid','scrubbed-0005@example.invalid','scrubbed-0006@example.invalid','scrubbed-0007@example.invalid');

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

-- (2) overwrite the stale provisional retail anchors so the ring reads sensibly
INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom)
SELECT p.id, v.cents, v.uom
  FROM (VALUES
    ('DBS-UL-BRUSSELS',  975, 'SF'),   -- PROVISIONAL $9.75/SF
    ('DBS-AGG-SAND',    5200, 'T'),    -- PROVISIONAL
    ('DBS-AGG-34CLEAR', 6000, 'T')     -- PROVISIONAL
  ) AS v(sku, cents, uom)
  JOIN products p ON p.sku = v.sku
ON CONFLICT (product_id) DO UPDATE
  SET list_price_cents = EXCLUDED.list_price_cents,
      price_uom        = EXCLUDED.price_uom;
