-- 0397: messaging group rooms (walk change 7 slice S2; the change 7 rulings 3 and 4, row 2139):
-- one additive kind widening and one shape guard, no new tables.
-- Owner: internal/messaging. Boards: Group DM, first message of a new group.
--
-- Plain-language intent: a group room is a named conversation any staffer may start with any set of
-- staffers, any member may add a person to (any staffer, any branch), and any member may rename; a
-- system line records who renamed it and who was added. Membership is EXPLICIT like a DM's (never
-- rule filled), the room spans branches, and a direct message stays a pair. The GROUP kind rides the
-- existing msg_channels/msg_members tables: explicit member rows, the membership-fenced reads, the
-- notify prefs and the pull-only channels sync feed all treat it like any other room the caller is
-- in, because every one of them joins on membership and filters no kind.
--
-- Rollback: the kind widening is additive (every value legal before stays legal). The shape guard
-- states what the service already refuses in code (a group carries no branch, no slug and no
-- auto-membership rule, so the branch and derivation walls are inert on it by construction).

-- 1. msg_channels.kind → admit 'GROUP' (the 0075 pattern: drop and re-add the widened CHECK).
ALTER TABLE msg_channels DROP CONSTRAINT msg_channels_kind_check;
ALTER TABLE msg_channels ADD CONSTRAINT msg_channels_kind_check
  CHECK (kind IN ('BRANCH','TEAM','TOPIC','DM','APPROVAL','GROUP'));

-- 2. The group shape: a group is founded by hand, spans branches and converges on nothing, so it
-- carries no branch (the branch wall has nothing to wall), no slug (the slug namespaces stay the
-- system's and the caller-founded rooms'), and no auto rule (membership is explicit only, like a
-- DM's, never derived).
ALTER TABLE msg_channels ADD CONSTRAINT msg_channels_group_shape
  CHECK (kind <> 'GROUP' OR (branch_id IS NULL AND slug IS NULL AND auto_scope IS NULL AND auto_role IS NULL));
