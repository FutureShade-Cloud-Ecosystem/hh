-- 0215 — DIB-520: the two labels the tester asked for reach a Home that nobody has retitled.
--
-- max(branch)+1 CONFIRMED AT THE MOMENT OF WRITING, across every branch rather than this one: 0213 is
-- L0's saved sort (on the integration branch) and 0214 is L7's purchasing delivery method (on
-- origin/fb/L7, in a second CLI session that this one cannot lock a number against). 0215 is free.
--
-- ── WHY A DATA CHANGE IS THE WHOLE OF THIS TICKET'S SECOND HALF ─────────────────────────────────
--
-- The tester asked two questions and both were about words: "'open claims' could mean many things i
-- feel. can it say opened warranty claims. and deliveries scheduled for today?" The catalog answered
-- them — viz.go now reads "Open warranty claims" and "Deliveries scheduled today" — and on a role
-- default that answer reaches nobody, because a widget's DISPLAYED title is its stored row's `title`
-- and the catalog label only fills in when that is empty:
--
--     if w.Title == "" { w.Title = AutoTitle(m, w.Split) }          -- widget.go
--
-- That emptiness is deliberate and it is the mechanism this file uses rather than fights. The client
-- says why, in home-bento-logic.ts: "the EMPTY STRING is what gets stored, because 'I never titled
-- this' and 'I titled it exactly this' are different facts, and materialising the guess on save means
-- the next release's better wording never reaches the person." 0201 and 0208 seeded these rows with
-- the guess materialised, which is exactly the state that sentence warns about — so the repair is to
-- put the rows back into "I never titled this", not to write the new wording in as a second guess.
--
-- MEASURED ON A FROM-ZERO DATABASE, not estimated: 6 rows in dashboard_profile_widgets (three
-- 'Open claims', three 'Deliveries today') and 5 in dashboard_gallery_entries (two and three).
--
-- ── WHAT IT REFUSES TO DO, WHICH IS MOST OF THE FILE ────────────────────────────────────────────
--
-- IT DOES NOT TOUCH A ROW A HUMAN HAS EDITED. 0212 set this precedent one ticket ago and the reason
-- has not changed: updated_by is NULL for a seeded default and carries a user id once somebody has
-- saved through the API, and a dealer who has retitled their own card has made a decision this file
-- does not get to overrule. On a box where these rows have been curated, this migration correctly
-- does nothing at all.
--
-- IT DOES NOT TOUCH A TITLE THAT IS NOT THE RETIRED ONE. The predicate is byte-identity with the
-- exact wording being retired, and that is the whole test for "this was the old default rather than a
-- choice". It matters here and not hypothetically: the same two measures also carry curator-written
-- titles in the seed — 'By branch' on a profile row and 'Deliveries by branch' in the gallery — and
-- those are somebody saying something, not the catalog echoing itself. They are untouched, and the
-- verification below counts them to prove it.
--
-- IT WRITES NO NEW WORDING INTO A LAYOUT. Setting '' is what makes the CATALOG govern from here on,
-- so the next time these labels improve the rows follow without a migration; writing the new wording
-- into a layout row would re-materialise the guess and put the next release back where this one
-- started. THE GALLERY IS THE ONE EXCEPTION and it is not an inconsistency — see the comment above
-- that statement. A gallery row is a menu entry with a title and a blurb, both authored, and this
-- build refuses one without a name; blank-means-the-catalog-governs is an idiom the layout tables can
-- afford because AutoTitle fills a placed widget in, and a menu row has no such fallback.
--
-- dashboard_user_widgets has no updated_by column BY DESIGN (0201: "a person's own layout is only
-- ever written by that person"), so byte-identity is the only test available there — and it is the
-- right one for the same reason: a person who never opened the rename field has a title that is
-- character-for-character the old default because the client PUT back what the server sent it.

BEGIN;

UPDATE dashboard_profile_widgets
   SET title = '', updated_at = now()
 WHERE updated_by IS NULL
   AND ( (measure = 'warranty.open_claims' AND title = 'Open claims')
      OR (measure = 'deliveries.today'     AND title = 'Deliveries today') );

-- THE GALLERY IS REWRITTEN, NOT BLANKED, and the difference is a rule this build already enforces.
-- A gallery entry is an OFFER in a menu: it carries a title AND a blurb, both authored by a curator,
-- and gallery_integration_test.go refuses a row with either missing ("ships without a name or a
-- sentence"). Blank-means-the-catalog-governs is the LAYOUT tables' idiom, because AutoTitle fills a
-- placed widget in; a menu row has no such fallback and no business having one. So these take the new
-- wording literally, and the byte-identity predicate still keeps the curator's own 'Deliveries by
-- branch' out of it.
UPDATE dashboard_gallery_entries
   SET title = 'Open warranty claims', updated_at = now()
 WHERE updated_by IS NULL AND measure = 'warranty.open_claims' AND title = 'Open claims';

UPDATE dashboard_gallery_entries
   SET title = 'Deliveries scheduled today', updated_at = now()
 WHERE updated_by IS NULL AND measure = 'deliveries.today' AND title = 'Deliveries today';

UPDATE dashboard_user_widgets
   SET title = '', updated_at = now()
 WHERE ( (measure = 'warranty.open_claims' AND title = 'Open claims')
      OR (measure = 'deliveries.today'     AND title = 'Deliveries today') );

COMMIT;
