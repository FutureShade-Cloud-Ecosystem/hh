-- 0171_dealer_hotkeys_pay.sql — UNION-TRAIN11: the concept's "F8 Pay" joins the dealer hotkey map.
--
-- DIB-496 (0164) shipped the dealer-level map; DIB-488 rebuilt the till around one guarded
-- Complete-sale CTA (.complete-cta). The client's POS concept draws a keycap chip — "F8 Pay" —
-- on that button. The two halves shipped on separate trains and meet on this one, so the binding
-- lands now, by exactly the roster-growth path the 0164 header promised: a follow-up migration
-- plus a registry line (app/src/hotkey-logic.ts gains 'sale.pay'; internal/dashboard/hotkeys.go's
-- DefaultHotkeys grows the same row so code and migration keep answering alike).
--
-- What the key DOES is the frontend's business (the 0164 ruling): 'sale.pay' publishes the
-- pos.pay intent, and the terminal completes the sale through the SAME guarded path a CTA click
-- takes — an un-armed till is walked to the payment step, a DONE ring stays disarmed. A dealer
-- who wants a different key (or none) edits this row like any other; ON CONFLICT DO NOTHING means
-- a dealer who already claimed F8 for something else keeps their own binding.
--
-- F8 avoids every key the browser refuses to yield (F5/F6/F11/F12 — the 0164 survey) and sits
-- beside F9 on the row the concept's chip names.

INSERT INTO dealer_hotkeys (binding, action_id) VALUES
    ('F8', 'sale.pay')
ON CONFLICT (binding) DO NOTHING;
