-- 0388: the money rooms keep their seats for the finance roles.
-- Owner: internal/messaging.
--
-- WHAT WAS WRONG. #finance (0052), #payments and #payments-<branch> (0152)
-- were seeded auto_role '*', so derived membership seated every active staff
-- member in them: the sales floor and the yard managers read each day's
-- payment lines. auto_role names a single role code, so the model could not
-- say "these three roles" at all.
--
-- THE FIX. msg_channels.member_roles, a nullable list of role codes. NULL
-- (every room today but these) changes nothing. A room carrying a list seats
-- only users who hold one of the codes: the derived rule in SyncMembership
-- adds it to its predicate (so the next sync also prunes the rows already
-- seated wrongly, announced on the channels feed like any other prune), and
-- the explicit add refuses a target outside it. The list is the roles the
-- decided matrix gives the money desk at work level or above:
-- PAYABLES_RECEIVABLES, FINANCIAL_CONTROLLER and GENERAL_MANAGER (pinned by
-- the messaging integration test against core/internal/roleconfig).
--
-- The rooms are matched by their system slugs and the auto_scope marker only
-- a system row carries; a caller founded room is never touched (and the
-- payments- family is reserved against founding besides).
--
-- Forward only, additive only.

ALTER TABLE msg_channels ADD COLUMN member_roles TEXT[];

UPDATE msg_channels
   SET member_roles = ARRAY['PAYABLES_RECEIVABLES','FINANCIAL_CONTROLLER','GENERAL_MANAGER']
 WHERE auto_scope IS NOT NULL
   AND (slug = 'finance' OR slug = 'payments' OR slug LIKE 'payments-%');
