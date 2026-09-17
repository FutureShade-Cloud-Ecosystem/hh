-- 0072: MSG-01 scopeBrief #6 — Contractor-Portal → Chat inbound bridge (portal-inbox house queue).
-- Owner: internal/messaging. Feature: scopeBrief #6 (buildableSpikeNow). Follows 0052/0069 seed style.
--
-- Plain-language intent: when a contractor activates in the portal, the portal emits
-- crm.NewPortalContact to the transactional outbox (today drained with NO real consumer — D-026). The
-- messaging bridge (internal/messaging/portal_bridge.go) OBSERVES that topic on the shared dispatcher
-- and posts a system EVENT message into this seeded #portal-inbox channel — the inbound "house queue"
-- the counter watches. Messaging never imports the portal's repository: it only reacts to the topic on
-- the bus (RegisterObserver — a non-owning side-effect; the portal's drain handler stays the owner).
--
-- Kind is TOPIC (a subject channel), but auto_scope GLOBAL + auto_role '*' so every ACTIVE staffer
-- auto-joins the queue (the FR-1 derived-membership rule keys on auto_scope, not kind) — mirroring
-- #office / #finance (0052) and #alerts-all (0069). Without the auto rule this would be a dead,
-- member-less channel (exactly what 0069's comment warns against).
--
-- Fixed UUID + ON CONFLICT (id) DO NOTHING, matching the 0052/0069 seed style; safe on any DB.
INSERT INTO msg_channels (id, kind, branch_id, slug, name, topic, auto_scope, auto_role) VALUES
  ('a5000000-0000-4000-9000-000000000006','TOPIC',NULL,
     'portal-inbox','#portal-inbox','Contractor portal inbound — new portal contacts land here','GLOBAL','*')
ON CONFLICT (id) DO NOTHING;

-- GATED (do NOT build — v1 is strictly inbound → this one house queue, one topic):
--  * rep-routing to a rep's DM — needs rep_assignments, which does NOT exist (customers carry no
--    assigned rep; they carry only created_by). Until an assigned-rep source is settled, everything
--    lands in #portal-inbox rather than a rep's DM.
--  * the other two portal topics — portal.RFQSubmitted and portal.PaymentReceived — are NOT bridged.
--  * the default contractor-visible auto-reply text (G-CLIENT) — must be client-approved before it
--    ships. v1 posts an internal EVENT only; it sends nothing back to the contractor.
