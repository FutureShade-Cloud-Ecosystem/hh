-- 0199 — every place a workstream code is stored as DATA and rendered to a person.
--
-- THE RULE, which this product already holds itself to everywhere else: "Screen copy never carries an
-- internal workstream code (A-05, W6, WTY-01) or a ticket number." Session B closed that class in the
-- built bundle (192 codes → 0) and in the Go constants that compose the lien document's banner. This
-- file closes the half that lives in the DATABASE, which no source scan of either kind could see —
-- the client gates read app/src, the Go gates read Go, and these sentences are rows.
--
-- Found by driving the real application in a browser and reading the words back out of the live DOM
-- (scripts/walkthrough-sessionB-item3.cjs). A tax administrator opening Compliance → Tax was being
-- shown five rate rows each ending "(D-021)".
--
-- ── WHY A MIGRATION NUMBER OUTSIDE THE SESSION'S RESERVATIONS ────────────────────────────────────
--
-- The session brief reserves 0192-0198 across its five items and gives the copy-hygiene item none, on
-- the reasonable expectation that copy hygiene is a source change. That expectation was wrong for
-- eight stored sentences. 0199 is taken rather than one of the reserved numbers so the whole
-- reservation block stays intact for the items that were promised it — a lapsed reservation is cheap,
-- a collision between two sessions is not.
--
-- ── WHAT IS AND IS NOT CHANGED ──────────────────────────────────────────────────────────────────
--
-- The MEANING of every sentence is unchanged. Each still says the figure is provisional and what it
-- is waiting on. What comes out is the number of the internal decision tracking that wait, which
-- tells a dealer's tax administrator, or a lien document's reader, precisely nothing about their own
-- day. That is the whole edit.
--
-- Both halves are needed for each column and neither is sufficient alone:
--   the UPDATE   fixes the rows that exist and are being read today;
--   SET DEFAULT  fixes every row written from here on.
-- Changing only the default leaves the live rows dirty. Changing only the rows leaves a loaded gun:
-- the next INSERT that omits the column picks the old sentence straight back up.
--
-- The UPDATEs are pattern-scoped, not blanket. A row somebody has since written a real note into does
-- not match and is not touched — the same discipline 0191:116 used for its scoped DELETE, for the
-- same reason: a migration must not overwrite a human's own words.
--
-- ── review_status.banner_text, AND WHY IT IS HERE ───────────────────────────────────────────────
--
-- The other three are a tax administrator's screen. This one is a LIEN DOCUMENT — a statutory notice
-- under the Ontario Construction Act, served on an owner, a contractor or their lawyer — and our
-- ticket number was stamped at its head.
--
-- The Go constants that compose it were fixed earlier this session (internal/lien/template.go,
-- internal/compliance/model.go), which was sufficient in practice because `review_status` holds ZERO
-- rows on every database checked: that constant IS the banner everywhere. But 0038:103 still declared
-- the old sentence as the column DEFAULT, deliberately out of step, and
-- compliance/banner_vocabulary_test.go was left pinning that the divergence was SAFE rather than
-- absent — it asserts the one INSERT names banner_text explicitly, so no row can pick the stale
-- default up. This closes it properly: the default now matches the constant, and that test goes from
-- guarding a known gap to guarding a closed one.
--
-- ── SAFETY ──────────────────────────────────────────────────────────────────────────────────────
--
-- Nine scoped UPDATEs and four ALTER COLUMN SET DEFAULTs. Nothing dropped, nothing renamed, no row
-- deleted, no column added. Safe against a staging database that is never wiped; the UPDATEs are
-- idempotent (a second run matches nothing) and re-running the file is impossible anyway because
-- cmd/migrate records the version. Reverting means putting the codes back, which nobody wants.

-- ── tax_rates.notes ─────────────────────────────────────────────────────────────────────────────
UPDATE tax_rates
   SET notes = 'Placeholder rate — pending confirmation of the tax table.'
 WHERE notes = 'PLACEHOLDER — pending tax-table confirmation (D-021)';

ALTER TABLE tax_rates
  ALTER COLUMN notes SET DEFAULT 'Placeholder rate — pending confirmation of the tax table.';

COMMENT ON COLUMN tax_rates.notes IS
  'Operator-facing note on a rate. The shipped default says the rate is provisional and what it waits on; it carries no internal decision number, because a tax administrator reading this screen is not reading our backlog (0199).';

-- ── tax_config.notes ────────────────────────────────────────────────────────────────────────────
UPDATE tax_config
   SET notes = 'Placeholder — pending the HST registration number and confirmation of the tax table.'
 WHERE notes = 'PLACEHOLDER — pending HST registration + tax confirmation (D-021)';

ALTER TABLE tax_config
  ALTER COLUMN notes SET DEFAULT 'Placeholder — pending the HST registration number and confirmation of the tax table.';

-- ── pricing_anchor_policy.notes ─────────────────────────────────────────────────────────────────
UPDATE pricing_anchor_policy
   SET notes = 'Placeholder anchors — pending confirmation of the pricing anchors.'
 WHERE notes = 'PLACEHOLDER — pending pricing-anchor confirmation (D-021)';

ALTER TABLE pricing_anchor_policy
  ALTER COLUMN notes SET DEFAULT 'Placeholder anchors — pending confirmation of the pricing anchors.';

-- ── review_status.banner_text ───────────────────────────────────────────────────────────────────
-- The sentence here is byte-identical to internal/lien.draftBanner minus its *** emphasis, and to
-- internal/compliance.defaultBanner exactly. Those two are pinned by their own package gates; this
-- one is pinned by TestIntegration_NoStoredOperatorCopyCarriesAWorkstreamCode.
UPDATE review_status
   SET banner_text = 'DRAFT — statute-derived, pending client validation'
 WHERE banner_text = 'DRAFT — statute-derived, pending client validation (DIB-42)';

ALTER TABLE review_status
  ALTER COLUMN banner_text SET DEFAULT 'DRAFT — statute-derived, pending client validation';

-- ── §2 · the four the SCHEMA WALK found, which the browser pass had not ─────────────────────────
--
-- The gate this migration ships with (internal/gate/stored_copy_vocabulary_integration_test.go) walks
-- information_schema rather than a list of tables somebody typed out, and on its first run it found
-- four more stored sentences carrying a code — every one of them on a screen the browser walkthrough
-- had not visited. That is the difference between a gate and a checklist, and it is the reason the
-- gate is written the way it is: a table added next year is covered without anybody remembering.
--
-- All four are seeded rows rather than column defaults, and all four are rendered. `pricing_rules
-- .detail_text` reaches a PRINT DOCUMENT (pricing-doc.ts:208), so that one can leave the building.
--
-- Same discipline as §1: the meaning is preserved exactly, the internal reference comes out, and each
-- UPDATE is scoped to the precise string it replaces so a row somebody has since edited is untouched.

-- 0104:70 — the AR credit policy's own note, editable by a manager on the AR console (ar-console.ts).
-- "DIB-65 flag-off" told a controller which of OUR tickets turned the switch off. What they need is
-- that it IS off and what that means, which the rest of the sentence already said.
UPDATE ar_credit_policy
   SET notes = 'Org default. Automatic holds ship OFF: the nightly job evaluates and reports but places no holds until an operator turns them on.'
 WHERE notes = 'Org default. auto_hold_enabled ships FALSE (DIB-65 flag-off): the nightly job evaluates and reports but places no holds until an operator flips it.';

-- 0067:29 — a seeded pricing rule's detail text, shown in the rule editor AND on the printed pricing
-- document. A contractor holding that sheet has no use for a ticket number.
UPDATE pricing_rules
   SET detail_text = 'Provisional — bulk default; real breaks land with the general manager''s worksheet'
 WHERE detail_text = 'PROVISIONAL — Bulk default (DIB-260); real breaks land with the GM worksheet';

-- 0112:111,119 — the two demo rebate programs. "see REPORT-W4-A.md" points at a file in OUR repository
-- from a screen in the dealer's product.
UPDATE rebate_programs
   SET notes = 'Demo rate (2.50% of sale). Not a confirmed vendor sheet term.'
 WHERE notes = 'DEMO RATE (2.50% of sale). Not a confirmed vendor sheet term — see REPORT-W4-A.md.';

UPDATE rebate_programs
   SET notes = 'Demo rate ($0.15 per base unit). Not a confirmed vendor sheet term.'
 WHERE notes = 'DEMO RATE ($0.15 per base unit). Not a confirmed vendor sheet term — see REPORT-W4-A.md.';

-- 0042:49 — the third tax status-card profile. D-036 is the ratification decision; the profile being
-- PENDING is the fact, and the screen already shows that in its own status column.
UPDATE tax_status_card_profiles
   SET notes = 'Placeholder — pending ratification.'
 WHERE notes = 'PLACEHOLDER — pending D-036 ratification';
