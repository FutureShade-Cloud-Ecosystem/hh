-- 0220: the counter's LAYOUT — the four-step single column, or all four as panels you arrange.
--
-- WHAT WAS ASKED, BY SEVERAL PEOPLE, IN DIFFERENT WORDS. "can we just have everything opened and on
-- the same page? being able to see customer and products and payment and everything all at once is
-- actually extremely useful." "more options should just be opened there. And not a drop down."
-- "Still not loving the layout of this screen... Maybe if users still want this we give the ability
-- to turn it off?" And, on the same screen from the other side: "I prefer Dynamic, but Tanya would
-- prefer static."
--
-- THE RULING (commander, 20 Sep). The four-step single column REMAINS the standard counter and the
-- shipped default at every level. What this adds is the ability to show all four as panels and to
-- size and place them. It is opt-in, per person, and it changes nothing for anybody who does not
-- ask for it.
--
-- WHY THIS IS A COLUMN AND NOT A FIFTH ACCORDION SETTING. The single column REPLACED a two-panel
-- counter after the client used it for a day and said four half-visible panels made them hunt for
-- the one thing they were doing (the account is in _pos-accordion.ts). That finding is not
-- overturned here and this column is why: the person who wants one column keeps it without opting
-- into anything, and the person who wants four panels says so once. A dealer-wide switch would have
-- to pick one of them and be wrong for the other every day — the same argument 0172's per-user
-- panels made, and the same answer.
--
-- It follows the rev-2 workspace persistence ruling EXACTLY (0166 / 0167; workspace.go):
--
--   DEALER LEVEL (pos_policy)          the default a till opens on, written only through the
--                                      admin-gated PUT /api/v1/pos/policy. No new admin surface.
--   PER USER (pos_user_workspace)      one person's own choice, riding the existing whole-or-absent
--                                      workspace row — if a row exists it IS the workspace, this
--                                      column included, and reset is the same DELETE.
--
--   Existing rows backfill to 'STEPPER', which is what they are already looking at, so nobody's
--   saved workspace changes shape underneath them.
--
-- WHAT IS NOT HERE: where the panels ended up. A panel's x/y/width/height is measured in the pixels
-- of the screen it was arranged on, and a counter person moves between a 27-inch till and a laptop
-- in the back office. Carrying coordinates between those two puts panels off the bottom of one of
-- them, so the geometry is stored per DEVICE (the floating-assistant precedent) while the CHOICE of
-- layout — which is about the person, not the glass — is stored here and follows them across tills.

ALTER TABLE pos_policy
  ADD COLUMN counter_layout TEXT NOT NULL DEFAULT 'STEPPER'
    CHECK (counter_layout IN ('STEPPER', 'PANELS'));

ALTER TABLE pos_user_workspace
  ADD COLUMN counter_layout TEXT NOT NULL DEFAULT 'STEPPER'
    CHECK (counter_layout IN ('STEPPER', 'PANELS'));

COMMENT ON COLUMN pos_policy.counter_layout IS
  'The dealer-default counter layout: STEPPER (the four-step single column) or PANELS (all four open, arrangeable).';
COMMENT ON COLUMN pos_user_workspace.counter_layout IS
  'The person''s own counter layout, riding the whole-or-absent workspace override (reset = DELETE). Panel geometry is per-device and is not stored here.';
