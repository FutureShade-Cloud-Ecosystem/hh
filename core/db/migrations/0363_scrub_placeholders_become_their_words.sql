-- 0363: the scrub placeholders become the words they replaced.
--
-- THE DEFECT. The scrub pass that carried the ERP baseline (core/db/scrub) swapped some ORDINARY WORDS for
-- "ScrubnameNNNN" tokens along with the personal names it was after: "Scrubname0027 Revenue" for the GL
-- account "Sales Revenue", "Scrubname0014 Scrubname0027" for the role "Inside Sales". The carried
-- migrations are frozen, so every build, from zero, shows the tokens to its users: nine role titles, two
-- GL account names, the report names, the dashboard widget titles, one tour line, and the persona line a
-- feedback note prints.
--
-- THE MAP, each word proven twice (the role code, report code or sentence it sits in, and the ERP original of
-- the same line, found through core/db/PORTED.md):
--   0027 Sales      0014 Inside     0019 Manager    0012 General     0010 Financial   0007 Controller
--   0009 Day        0026 Sale       0029 Statements 0032 Where
-- Tokens that stand for a PERSON's name are not words and are NOT touched here: the platform admin's, the
-- demo accounts' and the dispatch persona's display names, and the seeded DM (what a person reads in their
-- place is a decision for the principal and Colton; the demo build's rename pass covers its own).
--
-- MONEY. Two rows are general ledger accounts (4000 Sales Revenue, 1165 Point-of-Sale Relief Recoverable).
-- Only the NAME text changes, and the row is found by its account code AND its exact current name: no
-- code, type, normal side, balance, policy or journal line is read or written, so the books do not move.
--
-- THE GUARD, the same discipline as 0360. Every statement is UPDATE only, addressed by the row's own key,
-- and matches the EXACT value the frozen migration wrote (an underscore in a LIKE stands for the one dash
-- character the file does not carry). A role a dealer renamed, an account a controller retitled, a widget a
-- person retitled is somebody's words and is never touched; a database that already reads words falls
-- through every guard, so a second run matches nothing and moves no updated_at.
--
-- The demo build's name pass (the demo seed's role-title renames) writes the same words for the same nine
-- roles; whichever runs first, the other finds the row already holding the words and does nothing.

-- 1. role titles (9 rows).
UPDATE roles r
   SET name = v.new_name, updated_at = now()
  FROM (VALUES
    ('e7000000-0000-4000-9000-000000000001'::uuid, 'Scrubname0012 Scrubname0019',   'General Manager'),
    ('e7000000-0000-4000-9000-000000000002'::uuid, 'Scrubname0010 Scrubname0007',   'Financial Controller'),
    ('e7000000-0000-4000-9000-000000000004'::uuid, 'Branch Scrubname0019',          'Branch Manager'),
    ('e7000000-0000-4000-9000-000000000005'::uuid, 'Procurement Scrubname0019',     'Procurement Manager'),
    ('e7000000-0000-4000-9000-000000000006'::uuid, 'Scrubname0027 Scrubname0019',   'Sales Manager'),
    ('e7000000-0000-4000-9000-000000000007'::uuid, 'Yard Scrubname0019',            'Yard Manager'),
    ('e7000000-0000-4000-9000-000000000008'::uuid, 'Logistics Scrubname0019',       'Logistics Manager'),
    ('e7000000-0000-4000-9000-000000000010'::uuid, 'Scrubname0014 Scrubname0027',   'Inside Sales'),
    ('e7000000-0000-4000-9000-000000000011'::uuid, 'Outside Scrubname0027',         'Outside Sales')
  ) AS v(id, old_name, new_name)
 WHERE r.id = v.id AND r.name = v.old_name;

-- 2. GL account names (2 rows), by code and exact current name. Name text only.
UPDATE gl_accounts a
   SET name = v.new_name, updated_at = now()
  FROM (VALUES
    ('4000', 'Scrubname0027 Revenue',                                  'Sales Revenue'),
    ('1165', 'FN Point-of-Scrubname0026 Relief Recoverable (ON 8%)',   'FN Point-of-Sale Relief Recoverable (ON 8%)')
  ) AS v(code, old_name, new_name)
 WHERE a.code = v.code AND a.name = v.old_name;

-- 3. report template names (5) and legacy names (4), by report code.
UPDATE report_templates t
   SET name = v.new_name, updated_at = now()
  FROM (VALUES
    ('sales_by_day',      'Scrubname0027 by Scrubname0009', 'Sales by Day'),
    ('sales_by_branch',   'Scrubname0027 by Branch',        'Sales by Branch'),
    ('sales_by_rep',      'Scrubname0027 by Rep',           'Sales by Rep'),
    ('sales_by_category', 'Scrubname0027 by Category',      'Sales by Category'),
    ('payments_by_day',   'Payments by Scrubname0009',      'Payments by Day')
  ) AS v(code, old_name, new_name)
 WHERE t.code = v.code AND t.name = v.old_name;

UPDATE report_templates t
   SET legacy_name = replace(t.legacy_name, 'Scrubname0027', 'Sales'), updated_at = now()
  FROM (VALUES
    ('sales_by_day',      'Scrubname0027 Analysis _ Daily'),
    ('sales_by_branch',   'Scrubname0027 Analysis _ Branch'),
    ('sales_by_rep',      'Scrubname0027 Analysis _ Salesperson'),
    ('sales_by_category', 'Scrubname0027 Analysis _ Product Group')
  ) AS v(code, old_pattern)
 WHERE t.code = v.code AND t.legacy_name LIKE v.old_pattern;

-- 4. dashboard widget titles. The same three titles live in the per-profile layout, in the gallery, and
--    in any copy a person placed from the gallery; each is matched on its exact value, so a retitled one
--    is left alone.
UPDATE dashboard_profile_widgets
   SET title = CASE title WHEN 'Scrubname0027 today' THEN 'Sales today' ELSE 'Sales trend' END, updated_at = now()
 WHERE title IN ('Scrubname0027 today', 'Scrubname0027 trend');

UPDATE dashboard_gallery_entries
   SET title = CASE title WHEN 'Scrubname0027 today'     THEN 'Sales today'
                          WHEN 'Scrubname0027 trend'     THEN 'Sales trend'
                          ELSE 'Sales by branch' END,
       updated_at = now()
 WHERE title IN ('Scrubname0027 today', 'Scrubname0027 trend', 'Scrubname0027 by branch');

UPDATE dashboard_user_widgets
   SET title = CASE title WHEN 'Scrubname0027 today'     THEN 'Sales today'
                          WHEN 'Scrubname0027 trend'     THEN 'Sales trend'
                          ELSE 'Sales by branch' END,
       updated_at = now()
 WHERE title IN ('Scrubname0027 today', 'Scrubname0027 trend', 'Scrubname0027 by branch');

-- 5. gallery blurbs: only the word at the front changes; the one dash character in the middle is carried
--    through untouched, and the rest of the sentence must match exactly.
UPDATE dashboard_gallery_entries
   SET blurb = replace(blurb, 'Scrubname0032', 'Where'), updated_at = now()
 WHERE blurb LIKE 'Scrubname0032 the open book is sitting _ one bar per lifecycle state.'
    OR blurb LIKE 'Scrubname0032 the open book is sitting _ one bar per state.';

-- 6. the tour line.
UPDATE platform_tour_steps
   SET line = 'Where are you putting it? The counter reads this back.', updated_at = now()
 WHERE chapter_key = 'pick-to-proof' AND step_key = 'where'
   AND line = 'Scrubname0032 are you putting it? The counter reads this back.';

-- 7. user display names that carry words. The UAT tier 3 account is all words. The three homework
--    accounts keep the personal-name token the scrub removed on purpose; only the words beside it return.
UPDATE users
   SET display_name = 'UAT Tier-3 (Inside Sales, Trenton)', updated_at = now()
 WHERE email = 'scrubbed-0009@example.invalid'
   AND display_name = 'UAT Tier-3 (Scrubname0014 Scrubname0027, Trenton)';

UPDATE users
   SET display_name = replace(display_name, 'Scrubname0009', 'Day'), updated_at = now()
 WHERE id = '40000061-0000-4000-9000-000000000002'::uuid
   AND display_name LIKE 'Scrubname0030 (homework _ Till Scrubname0009)';

UPDATE users
   SET display_name = replace(replace(display_name, 'Scrubname0026', 'Sale'), 'Scrubname0029', 'Statements'), updated_at = now()
 WHERE id = '40000061-0000-4000-9000-000000000003'::uuid
   AND display_name LIKE 'Scrubname0025 Scrubname0024 (homework _ Scrubname0026 to Scrubname0029)';

UPDATE users
   SET display_name = replace(display_name, 'Scrubname0014 Scrubname0027', 'Inside Sales'), updated_at = now()
 WHERE id = '40000061-0000-4000-9000-000000000004'::uuid
   AND display_name LIKE 'Scrubname0016 (homework _ Scrubname0014 Scrubname0027)';
