-- 0074: Scrubname0013 the four Dibbits UAT testers full platform-admin (SUPER_ADMIN) so they can
-- see, touch, and test every module on staging during client UAT (commander directive 2026-07-22).
--
-- These are the staging-seeded UAT users (migration 0061). On any environment that does NOT
-- have them (e.g. a future production DB), this is a safe no-op — the loop only touches rows
-- that already exist. Idempotent: re-running re-asserts SUPER_ADMIN. Mirrors the identity
-- module's SetRole semantics (clear existing user_roles for the user, then assign the target).
--
-- NOTE (governance): this is a STAGING UAT convenience. Before go-live, real production RBAC
-- must be re-derived from the signed role matrix — these blanket SUPER_ADMIN grants must NOT
-- carry into prod. Tracked as a pre-go-live gate.
DO $$
DECLARE
  admin_role_id UUID;
  u             RECORD;
BEGIN
  SELECT id INTO admin_role_id FROM roles WHERE code = 'SUPER_ADMIN';
  IF admin_role_id IS NULL THEN
    RAISE NOTICE '0074: SUPER_ADMIN role not present — skipping UAT admin grant';
    RETURN;
  END IF;

  FOR u IN
    SELECT id, email FROM users
    WHERE email IN ('scrubbed-0004@example.invalid', 'scrubbed-0007@example.invalid', 'scrubbed-0005@example.invalid', 'scrubbed-0008@example.invalid')
  LOOP
    DELETE FROM user_roles WHERE user_id = u.id;
    INSERT INTO user_roles (user_id, role_id) VALUES (u.id, admin_role_id)
      ON CONFLICT DO NOTHING;
    RAISE NOTICE '0074: granted SUPER_ADMIN to %', u.email;
  END LOOP;
END $$;
