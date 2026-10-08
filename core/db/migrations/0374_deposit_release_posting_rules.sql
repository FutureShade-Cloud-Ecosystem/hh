-- 0374_deposit_release_posting_rules.sql
--
-- The deposit release (the item after 9.5): when the special order a supplier
-- deposit secures is fulfilled, the liability the deposit document booked in
-- 2300 Customer Deposits releases into revenue, so the sale's revenue equals
-- its pre-tax total and 2300 returns to zero.
--
-- The posting pair for the release topic, deposit.released, emitted once per
-- live deposit document when the order's fulfilment road says the material
-- has left (order.Service.AdvanceFromEvent to FULFILLED, the choke point the
-- dispatch handoff, the will-call collection and the pos order.fulfilled
-- observer all call):
--
--   deposit.released / customer_deposit -> 2300 (DR, the liability cleared)
--   deposit.released / revenue          -> 4000 (CR, the deposit recognised)
--
-- No AR and no tax legs: the deposit document billed AR in full when it
-- finalised (DR 1100 / CR 2300, the 0373 rule), and the sale's own invoice
-- booked the sale's whole tax on the rest, so the release is exactly the
-- deposit amount moving from the liability to revenue.
--
-- Owner module: internal/order (the emission), internal/gl (the posting).

INSERT INTO posting_rules (event_type, line_role, account_code)
VALUES
    ('deposit.released', 'customer_deposit', '2300'),
    ('deposit.released', 'revenue', '4000')
ON CONFLICT (event_type, line_role) WHERE branch_id IS NULL DO NOTHING;
