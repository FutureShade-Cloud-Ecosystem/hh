-- 0364: the leftover surnames beside a scrubbed first name go (the dispatch tour persona's first).
--
-- THE DEFECT. 0200 planted the dispatch persona's login with the display name "Scrubname0008 <surname>
-- (demo, Dispatch)": the scrub pass took out the first name and left the surname, so every build, from zero,
-- shows a person's surname on the tour's dispatch login. 0200 is frozen, so the value is rewritten here.
--
-- THE FIX. The display name becomes the plain role label the other demo personas carry ("Sales (demo)",
-- "Sales manager (demo)"): "Dispatch (demo)". One UPDATE, one row addressed by its fixed id, matching the
-- EXACT value 0200 wrote (the dash in it is the one em dash character, written here as chr(8212) so this file
-- carries none). A login somebody renamed falls through the guard and is left alone, and a second run
-- matches nothing and moves no updated_at.
UPDATE users
   SET display_name = 'Dispatch (demo)', updated_at = now()
 WHERE id = 'd0200000-0000-4000-9000-000000000001'::uuid
   AND display_name = 'Scrubname0008 Whitlock (demo ' || chr(8212) || ' Dispatch)';

-- THE SAME FLAW, TWO MORE PLACES (found by scanning a migrated database for a first-name placeholder followed
-- by a surname). 0060 planted the demo teammate's login as "Scrubname0008 <surname> (Demo Teammate)" and the
-- demo DM's title as "Scrubname0005, Scrubname0008 <surname>". Each becomes the label the demo already uses
-- for it (internal/demo/populate.go and the demo seed's own channel rename): the login "Demo teammate (demo)",
-- the DM title the two accounts' labels. Same guard: the fixed id and the EXACT planted value, so a login or
-- a conversation somebody renamed is left alone and a second run matches nothing.
UPDATE users
   SET display_name = 'Demo teammate (demo)', updated_at = now()
 WHERE id = 'd7000000-0000-4000-9000-0000000000da'::uuid
   AND display_name = 'Scrubname0008 Reyes (Demo Teammate)';

UPDATE msg_channels
   SET name = 'Platform admin (demo), Demo teammate (demo)', updated_at = now()
 WHERE id = 'a6000000-0000-4000-9000-0000000000d1'::uuid
   AND name = 'Scrubname0005, Scrubname0008 Reyes';
