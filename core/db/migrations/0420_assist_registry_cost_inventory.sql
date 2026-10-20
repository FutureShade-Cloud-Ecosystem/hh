-- 0420: slice 6a of the assistant's database boundary (TanyAI plan r3 slice 6, the money gate).
--
-- 1. The signed claim grows a tier. Its cost branch ids and margin flag were already fields of the composite
--    (0418); the Go side now fills them from the dealer's cost rule. ai.claims() reads every field by name
--    through jsonb_populate_record, so a new attribute needs no change to the function: a claim signed with
--    a tier reads it back, and a claim signed without one (an old payload) verifies and reads NULL, which
--    every tier floor below treats as no tier. The type is owned by the claims owner, so the change is made
--    under SET LOCAL ROLE and reset before the file ends.
-- 2. The tier floor in the policies of 0419 (and of every table this file adds).
-- 3. The inventory views, their row security and the cost mask.
--
-- Role names carry the database suffix exactly as in 0418.

DO $s6a$
DECLARE
  db          text := current_database();
  norm        text;
  suffix      text;
  v_view      text;
  v_claims    text;
  v_reader    text;
BEGIN
  norm := translate(db, 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz');
  norm := regexp_replace(norm, '[^a-z0-9]', '_', 'g');
  IF norm = db AND length(norm) <= 30 THEN
    suffix := norm;
  ELSE
    suffix := left(norm, 21) || '_' || left(md5(db), 8);
  END IF;
  v_view   := 'assist_view_owner_'   || suffix;
  v_claims := 'assist_claims_owner_' || suffix;
  v_reader := 'assist_reader_'       || suffix;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = v_claims) THEN
    RAISE EXCEPTION 'migration 0418 has not created the assist roles for this database';
  END IF;

  -- 1. The claim's tier.
  EXECUTE format('SET LOCAL ROLE %I', v_claims);
  ALTER TYPE ai.claim_set ADD ATTRIBUTE tier integer;
  RESET ROLE;
END
$s6a$;

RESET ROLE;
