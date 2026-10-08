# The default role configuration's source

`default.json` in this package is the decided default role configuration
(D48), carried into hh as the seed of record: under D49, what sits inside
the hard walls is the dealer's configuration, and this is the configuration
every dealer's is seeded with until per dealer overrides land. The file
names its source itself, in its `source` block, and so do the constants in
`default.go`:

- data: `role-access-r3.json`, sha256
  `8f9dc29430750629fe250dbc0b5d9ce6edf14fc8e422b40239392aec18528092`
- narrative: `ROLE-ACCESS-r3.md`, sha256
  `7801e4cd398c725d14bad33e9371b938fa4cc0608dc1a6da3002d674338b6733`

both in the principal's Session 2 planning record, as D48 names them.

## The derivation, field by field

- `cells`: the record's matrix, all 130 cells (13 roles on 10 surfaces),
  each cell's level exactly as decided, as the decision log amended it: row
  1156 reverses row 370's yard manager cells, so the yard manager's Counter
  Till and Quote Desk are none (its sale and quote access removed; yard
  picking, short picks and skid changes stay). No cell's ruling rows, scope
  prose or server-today marks travel: those live in the planning record,
  named by the hashes above.
- `lists.exception_approvers`: rule X4's four kinds, each approved by the
  Financial Controller and the General Manager, never HR: a quote price
  below the margin floor (the quote flow only, row 481), a journal over the
  threshold, freight above the quote, and a credit or COD exception
  ("leadership without HR": the Financial Controller and the General
  Manager; HR holds no money power, and Payables and Receivables is
  refused the credit and COD decisions in its own section).
- `lists.floor_clearers_own_branch`: rule X5, the Sales Manager and the
  Branch Manager, gated by the `manager_own_branch_floor_clearing` setting.
- `lists.delivery_date_committers`: rule X11 and the per role sections:
  the Yard Manager (a date on a load being built), the Logistics Manager
  (commits and re-times dates), the Branch Manager (commits a date and
  stands behind it). The Yard Team commits no delivery date; the General
  Manager commits none while `general_manager_selling` is off.
- `settings.cost_visibility`: rule X3: Inside Sales, Outside Sales and
  every manager and leadership role except HR (ten roles); hidden from the
  Yard Team, Drivers and HR.
- `settings.credit_at_till`: on (row 353: shown to the counter clerk).
- `settings.refund_amount_without_approval_cents`: 25000, the record's
  250.00 held as integer cents (X7, row 352).
- `settings.refund_approvers`: the Sales Manager and the Branch Manager of
  the sale's branch (X7, row 457).
- `settings.manager_own_branch_floor_clearing`: on (X5, rows 361, 457,
  481: the quote flow only).
- `settings.pricing_editors`: the Sales Manager and the General Manager
  (X6, row 360).
- `settings.shortage_resolvers`: the Yard Team within its own tool, the
  Yard Manager for the rest (X11, rows 368, 369, 457).
- `settings.dispatch_policy_setters`: the Logistics Manager and the Branch
  Manager (X13, row 457).
- `settings.general_manager_selling`: off (row 457: Read on the till,
  quotes and dispatch; sells nothing and commits no date).
- `settings.refund_approval_window_seconds`: 300, the tenth setting, added
  on the lead's ruling after the reader landed: the plan's section 3.2 and
  the contract's approver header both name the configuration as the source
  of the window an approver's credential may be old when the return
  carrying it commits (X7, row 482), and the planning record's nine
  settings name no window. The value 300 is the product default the
  return rules carried as a constant before this setting existed; the
  loader bounds the setting to 1 through 3600.

The tests in this package hold the carried file to the record: the full
130-cell matrix, the lists and the ten settings are asserted against the
decided values, and the load checks refuse anything that would break a
wall.
