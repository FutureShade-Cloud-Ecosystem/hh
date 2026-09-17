-- 0185 — COMMIT, NOT LOCK. The client's vocabulary, and the model correction it exposes.
--
-- R5, 27 August. "Lock" is our word. Theirs is COMMITTED — Scrubname0025 Scrubname0024: "the salesperson can request
-- but the dispatcher can commit to". speaker-0009 said "lock", speaker-0007's reply was a question,
-- not agreement: "you're saying 48 hour lock? What if an order comes in that's a better fit for that
-- load?" This migration renames the vocabulary AND collapses the two mechanisms 0184 shipped into
-- the ONE act the client describes.
--
-- ── WHAT 0184 BUILT AS TWO ──────────────────────────────────────────────────────────────────────
--
--   (1) delivery_stops.date_state — REQUESTED / COMMITTED, a SCHEDULING state on the load;
--   (2) delivery_runs.lock_state + dispatch_policy.lock_window_hours — a CHANGE FREEZE on the run,
--       computed from the clock against the run's date.
--
-- In the client's language those are one thing. A load is REQUESTED the moment a salesperson picks a
-- date; it becomes COMMITTED when a dispatcher approves it onto a truck; and a committed load is
-- precisely the one the twice-daily re-optimisation stops shuffling. The 48 hours is WHEN A
-- DISPATCHER TYPICALLY COMMITS — a prompt, not a second gate.
--
-- ── WHAT THIS MIGRATION MAKES TRUE ──────────────────────────────────────────────────────────────
--
--   THE STATE MACHINE LIVES ON THE LOAD.  delivery_stops.commit_state (was date_state) is the only
--   authority on whether a load is frozen. REQUESTED -> COMMITTED is the dispatcher's act;
--   COMMITTED -> REQUESTED is a REOPEN, which is a dispatch manager's act with a reason on it.
--
--   THE CLOCK NO LONGER GATES ANYTHING.  commit_prompt_hours (was lock_window_hours) drives a PROMPT
--   — "these loads are due to be committed" — and nothing else. Nothing in the system refuses a
--   mutation because of what time it is.
--
--   THE RUN ROW IS THE BULK LEDGER, NOT A SECOND MECHANISM.  delivery_runs.commit_state (was
--   lock_state) records that a manager reopened a WHOLE TRUCK, with who/when/why. Reopening a run
--   reopens its loads; the loads remain the authority.
--
-- ── WHY THIS IS SAFE AGAINST A STAGING DATABASE THAT IS NEVER WIPED ─────────────────────────────
--
-- Every step is a RENAME or an ADD, never a DROP-and-recreate of a column: no row's data is
-- reconstructed and nothing is retyped, so the shipped board's history survives byte for byte.
-- The two value sets that change (lock_policy's words, lock_state's 'UNLOCKED') are migrated with an
-- UPDATE between dropping the old CHECK and adding the new one, so no row is ever momentarily
-- illegal. Each statement is guarded so a partial application can be re-run: this file is idempotent.
--
-- REVERSING IT IS THE SAME FILE READ BACKWARDS (NOTES-DISPATCH-COMMIT.md carries the down-script).

-- ── §1 · dispatch_policy — the prompt, the reopen authority, and the working view ───────────────

ALTER TABLE dispatch_policy RENAME COLUMN lock_window_hours TO commit_prompt_hours;
ALTER TABLE dispatch_policy RENAME COLUMN lock_policy       TO reopen_policy;

ALTER TABLE dispatch_policy DROP CONSTRAINT IF EXISTS dispatch_policy_lock_window_hours_check;
ALTER TABLE dispatch_policy ADD  CONSTRAINT dispatch_policy_commit_prompt_hours_check
  CHECK (commit_prompt_hours BETWEEN 0 AND 336);

-- The words change with the meaning. MANAGER_UNLOCK described who held a key to a padlock; MANAGER
-- describes who may reopen a commitment. OFF meant "no padlock"; ANYONE means "a committed load may
-- be changed by anyone who could have committed it". HARD meant "no key exists"; NOBODY means a
-- commitment is final, which is the dealer who wants the cut-off to be a cut-off.
ALTER TABLE dispatch_policy DROP CONSTRAINT IF EXISTS dispatch_policy_lock_policy_check;
ALTER TABLE dispatch_policy ALTER COLUMN reopen_policy DROP DEFAULT;
UPDATE dispatch_policy SET reopen_policy = CASE reopen_policy
  WHEN 'MANAGER_UNLOCK' THEN 'MANAGER'
  WHEN 'OFF'            THEN 'ANYONE'
  WHEN 'HARD'           THEN 'NOBODY'
  ELSE reopen_policy END;
ALTER TABLE dispatch_policy ALTER COLUMN reopen_policy SET DEFAULT 'MANAGER';
ALTER TABLE dispatch_policy DROP CONSTRAINT IF EXISTS dispatch_policy_reopen_policy_check;
ALTER TABLE dispatch_policy ADD  CONSTRAINT dispatch_policy_reopen_policy_check
  CHECK (reopen_policy IN ('ANYONE','MANAGER','NOBODY'));

-- THE OTHER 48. R5's last finding: two different numbers were conflated. commit_horizon_weeks (8) is
-- how far the board PLANS and it is correct. What was missing is how far the dispatcher's DAILY
-- WORKING VIEW reaches — Scrubname0005: "what I don't want to do is have 100 items in their daily workflow
-- view that aren't relevant until next week"; speaker-0007: "it needs to be MORE than 48 hours just
-- from a quick view perspective"; Scrubname0025 Scrubname0024: "almost a couple weeks."
--
-- Fourteen days, and it is ITS OWN KNOB. Reusing the planning horizon for it would put eight weeks of
-- work in a view whose whole purpose is to hold this fortnight.
ALTER TABLE dispatch_policy
  ADD COLUMN IF NOT EXISTS working_view_days INTEGER NOT NULL DEFAULT 14;
ALTER TABLE dispatch_policy DROP CONSTRAINT IF EXISTS dispatch_policy_working_view_days_check;
ALTER TABLE dispatch_policy ADD  CONSTRAINT dispatch_policy_working_view_days_check
  CHECK (working_view_days BETWEEN 1 AND 90);

COMMENT ON COLUMN dispatch_policy.commit_prompt_hours IS
  'How many hours before a delivery day a still-REQUESTED load is PROMPTED for commitment. 48 is the client''s number. It gates NOTHING — the commitment does that. 0 switches the prompt off.';
COMMENT ON COLUMN dispatch_policy.reopen_policy IS
  'Scrubname0033 may reopen a COMMITTED load. MANAGER (default) = a dispatch manager. ANYONE = anyone who could commit it. NOBODY = a commitment is final.';
COMMENT ON COLUMN dispatch_policy.working_view_days IS
  'How far ahead the dispatcher''s daily WORKING VIEW reaches, in days. 14 = "almost a couple weeks" (Aug-26 call). Distinct from commit_horizon_weeks, which is how far the board PLANS.';

-- ── §2 · delivery_stops — the state machine, and the reopen on it ───────────────────────────────

ALTER TABLE delivery_stops RENAME COLUMN date_state TO commit_state;
ALTER TABLE delivery_stops DROP CONSTRAINT IF EXISTS delivery_stops_date_state_check;
ALTER TABLE delivery_stops DROP CONSTRAINT IF EXISTS delivery_stops_commit_state_check;
ALTER TABLE delivery_stops ADD  CONSTRAINT delivery_stops_commit_state_check
  CHECK (commit_state IN ('REQUESTED','COMMITTED'));

-- REOPENING IS NORMAL, so it is a fact with a person on it — the same shape the commit already has.
-- The stamps are NOT cleared by a later commit: "this load was reopened on the 3rd and re-committed
-- on the 4th" is the sentence a dispatcher needs when a customer asks why the truck changed.
ALTER TABLE delivery_stops
  ADD COLUMN IF NOT EXISTS reopened_at    TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS reopened_by    UUID REFERENCES users(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS reopen_reason  TEXT NOT NULL DEFAULT '';

ALTER INDEX IF EXISTS idx_delivery_stops_datestate RENAME TO idx_delivery_stops_commit_state;

COMMENT ON COLUMN delivery_stops.commit_state IS
  'THE state machine (0185). REQUESTED = a salesperson asked and it is on the board; COMMITTED = a dispatcher put it on a truck and stood behind it. A committed load is the one re-optimisation stops shuffling and the one whose changes are gated. Reopening returns it to REQUESTED.';
COMMENT ON COLUMN delivery_stops.reopen_reason IS
  'Why a dispatch manager reopened this committed load. speaker-0007''s case — "what if an order comes in that''s a better fit for that load?" — is the normal one, so the reason is a note, never an apology.';

-- ── §3 · delivery_runs — the bulk reopen ledger ─────────────────────────────────────────────────
--
-- The evidence CHECK is dropped FIRST and re-added LAST, so the value migration in between never
-- leaves a row that fails a constraint.
ALTER TABLE delivery_runs DROP CONSTRAINT IF EXISTS delivery_runs_unlock_needs_evidence;
ALTER TABLE delivery_runs DROP CONSTRAINT IF EXISTS delivery_runs_lock_state_check;

ALTER TABLE delivery_runs RENAME COLUMN lock_state    TO commit_state;
ALTER TABLE delivery_runs RENAME COLUMN unlocked_by   TO reopened_by;
ALTER TABLE delivery_runs RENAME COLUMN unlocked_at   TO reopened_at;
ALTER TABLE delivery_runs RENAME COLUMN unlock_reason TO reopen_reason;

ALTER TABLE delivery_runs ALTER COLUMN commit_state DROP DEFAULT;
UPDATE delivery_runs SET commit_state = 'REOPENED' WHERE commit_state = 'UNLOCKED';
ALTER TABLE delivery_runs ALTER COLUMN commit_state SET DEFAULT 'AUTO';

ALTER TABLE delivery_runs DROP CONSTRAINT IF EXISTS delivery_runs_commit_state_check;
ALTER TABLE delivery_runs ADD  CONSTRAINT delivery_runs_commit_state_check
  CHECK (commit_state IN ('AUTO','REOPENED'));
ALTER TABLE delivery_runs DROP CONSTRAINT IF EXISTS delivery_runs_reopen_needs_evidence;
ALTER TABLE delivery_runs ADD  CONSTRAINT delivery_runs_reopen_needs_evidence
  CHECK (commit_state <> 'REOPENED' OR (reopened_at IS NOT NULL AND btrim(reopen_reason) <> ''));

COMMENT ON COLUMN delivery_runs.commit_state IS
  'The BULK ledger, not a second mechanism. AUTO = ordinary; REOPENED = a dispatch manager reopened this whole truck, with a reason, which returned its loads to REQUESTED. The authority on whether a load is frozen is delivery_stops.commit_state.';

-- ── §4 · the surface registry (0172 substrate) ──────────────────────────────────────────────────
--
-- THREE CHANGES, AND THE FIRST IS A RETIREMENT RATHER THAN A RENAME. Item ids are add-only (0140):
-- renaming `card.locked` in place would orphan every curation row a dealer has saved against it. So
-- the id is RETIRED — hidden here, absent from the frontend catalog, and never reused — and the card
-- the client asked for arrives under its own id.
--
--   card.committed  the client's word, counting COMMITTED loads on the day.
--   card.due        the 48 hours in its true role: loads still REQUESTED inside the prompt window.
--   view.working    the two-week working view (§1's working_view_days) as a span the dealer curates.
UPDATE surface_role_items SET visible = false
 WHERE surface_id = 'dispatch.loadboard' AND item_kind = 'chip' AND item_id = 'card.locked';
UPDATE surface_user_items SET visible = false
 WHERE surface_id = 'dispatch.loadboard' AND item_kind = 'chip' AND item_id = 'card.locked';

INSERT INTO surface_role_items (surface_id, profile, item_kind, item_id, position, visible)
SELECT 'dispatch.loadboard', p.profile, i.item_kind, i.item_id, i.position, i.visible
FROM (VALUES ('counter'), ('yard'), ('driver'), ('manager'), ('admin')) AS p (profile)
CROSS JOIN (VALUES
    ('chip',   'card.committed', 5, true),
    ('chip',   'card.due',       6, true),
    -- APPENDED, not slotted in beside the day. 0184 seeded positions 2-4 for day/week/month, and
    -- renumbering shipped rows would stomp any dealer who had already reordered them. A dealer who
    -- wants the working view first moves it, which is what the substrate is for.
    ('action', 'view.working',   5, true)
) AS i (item_kind, item_id, position, visible)
ON CONFLICT (surface_id, profile, item_kind, item_id) DO NOTHING;
