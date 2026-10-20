-- 0425: slice 6b of the assistant's database boundary (TanyAI plan r3 slice 6, quotes and purchasing).
--
-- 1. The signed claim grows the branches where the asker may see purchase cost (po_cost_branch_ids), decided in
--    Go by the per branch purchasing policy, the second cost vocabulary. ai.claims() reads fields by name, so the
--    new attribute needs no change to the function: an old claim verifies and reads NULL, which every purchase
--    cost mask treats as no purchase cost. Made under SET LOCAL ROLE the claims owner and reset.
-- 2. The quote views, the purchasing views, their row security and their masks.
--
-- Role names carry the database suffix exactly as in 0418.

DO $s6b$
DECLARE
  db          text := current_database();
  norm        text;
  suffix      text;
  v_view      text;
  v_claims    text;
  v_reader    text;
  t           text;
  owner_name  text;
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

  -- 1. The claim's purchase cost branches.
  EXECUTE format('SET LOCAL ROLE %I', v_claims);
  ALTER TYPE ai.claim_set ADD ATTRIBUTE po_cost_branch_ids uuid[];
  RESET ROLE;
END
$s6b$;

RESET ROLE;
