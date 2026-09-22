# Carry ledger: normalization against the ERP pin

Carried Go source is ours: house punctuation applies. D32's
verbatim rule is migrations-only by recorded range. This ledger
pays the HH-3.7 diff debt: per carried file, the sha256 of the
pinned ERP blob (first 16 hex) and the punctuation classes
normalized (em, en, box-drawing, mojibake quotes) and the
personal-data scrub class (third-party identifying values removed;
recorded as counts by kind only, never values). Files marked
NEW have no pin counterpart. Sanctioned edit classes beyond
punctuation are named per package below. Regenerate counts:
  git -C hardscapeos_dibbits show 6eaea3ec:<erp rel> | sha256sum
  and diff the four classes between pin blob and carried file.

## Sanctioned edit classes in this batch (foundations)

- audit: the one registrar swapped to the Guards constructor,
  mapped by route (reviewed; the reverse-order trap disarmed).
- httpx: the offset Page family removed; cursor helpers added
  (cursor.go NEW); list endpoints convert in their own batches.
- config: exactly the two staff secrets and the Google SSO flags
  removed (the SSO leave-behind); three inputs kept as named
  dead-or-later findings (NATSURL, JWKSURL, StaffBaseURL).
- platform/otelhttp: NEW helper per the plan (reviewed, kept).
- jobs, notify, platform/db, logging, moneyutil, localization:
  punctuation-only normalization.

| carried file | pin sha256 (16) | em | en | box | mojibake |
|---|---|---|---|---|---|
| core/internal/audit/audit_test.go | 21defa29cd13c314 | 10 | 3 | 227 | 0 |
| core/internal/audit/handler.go | ceab64afd0ca4a92 | 3 | 0 | 0 | 0 |
| core/internal/audit/integration_test.go | 5cf259058aea879a | 17 | 0 | 8 | 0 |
| core/internal/audit/model.go | cd057e145fbd2a61 | 8 | 0 | 0 | 0 |
| core/internal/audit/repository.go | ad0d47f4843816d5 | 5 | 0 | 68 | 1 |
| core/internal/audit/service.go | bca2f72d3fb8a11d | 27 | 0 | 243 | 0 |
| core/internal/config/config.go | 04210d57768941bc | 15 | 0 | 0 | 0 |
| core/internal/config/config_test.go | 262bf02a8edc911b | 1 | 0 | 0 | 0 |
| core/internal/config/demo_mode_test.go | 9502b45409344b22 | 9 | 0 | 167 | 0 |
| core/internal/jobs/jobs.go | 05062f878da94329 | 7 | 0 | 194 | 0 |
| core/internal/jobs/repository.go | fbf3691804d0249b | 7 | 0 | 0 | 0 |
| core/internal/jobs/runner.go | f1c67e550ec8e698 | 10 | 0 | 0 | 0 |
| core/internal/jobs/runner_integration_test.go | 163b2a264650a033 | 6 | 0 | 0 | 0 |
| core/internal/jobs/runner_test.go | 32142ccefbaec407 | 1 | 0 | 0 | 0 |
| core/internal/localization/localization.go | f0e24a99a6390d3a | 8 | 0 | 0 | 0 |
| core/internal/localization/localization_test.go | b12ee4072c0476b7 | 0 | 0 | 0 | 0 |
| core/internal/notify/dispatcher.go | 59a78ad7d89d7b1d | 8 | 0 | 0 | 0 |
| core/internal/notify/dispatcher_test.go | 541e38ee5396a294 | 2 | 0 | 0 | 0 |
| core/internal/notify/integration_test.go | 9e7d98c97f74ec62 | 18 | 0 | 254 | 0 |
| core/internal/notify/notifier.go | f1cc988a4584cd5d | 6 | 0 | 0 | 0 |
| core/internal/notify/notifier_test.go | f94a2eed70c26569 | 0 | 0 | 0 | 0 |
| core/internal/notify/register_test.go | fafa0de48f346df7 | 1 | 0 | 0 | 0 |
| core/internal/notify/requeue.go | 8b46e07ef8672019 | 2 | 0 | 0 | 0 |
| core/internal/notify/store.go | 453ff8f78efa1ae6 | 2 | 0 | 0 | 0 |
| core/internal/platform/db/db.go | 26c67e6e30cc95f5 | 1 | 0 | 0 | 0 |
| core/internal/platform/db/dib396_lease_test.go | 09783750bdc5d8d1 | 8 | 0 | 0 | 0 |
| core/internal/platform/db/gl_seed_integrity_integration_test.go | 6c76165b7d4471d8 | 5 | 0 | 0 | 0 |
| core/internal/platform/db/invariants_integration_test.go | 1d72452cf67d8c4d | 21 | 0 | 71 | 0 |
| core/internal/platform/httpx/counter_vocabulary_test.go | 799e4af5f6c05155 | 16 | 0 | 87 | 0 |
| core/internal/platform/httpx/cursor.go | NEW |  |  |  |  |
| core/internal/platform/httpx/cursor_test.go | NEW |  |  |  |  |
| core/internal/platform/httpx/httpx.go | f2d5b6aa3bb0af48 | 5 | 1 | 0 | 0 |
| core/internal/platform/logging/logging.go | 355b6bc4b7a3af5a | 0 | 0 | 0 | 0 |
| core/internal/platform/moneyutil/allocate.go | 841df8aff4664ee5 | 1 | 0 | 0 | 0 |
| core/internal/platform/moneyutil/allocate_test.go | 3176c8ee0a639eb0 | 0 | 0 | 0 | 0 |
| core/internal/platform/otelhttp/otelhttp.go | NEW |  |  |  |  |
| core/internal/platform/otelhttp/otelhttp_test.go | NEW |  |  |  |  |

## Batch 2, the identity bridge (HH-3.2 B2): sanctioned edit classes

- identity/staffcontext is the bridge (NEW package, ruled mechanism):
  the ERP StaffContext/rbac vocabulary and the approvals rail carry under
  their own names, and resolution bridges onto the Phase B world
  (bridge.go NEW). Carried module code re-points its import from
  internal/identity to internal/identity/staffcontext in the module
  batches; that re-point is this batch's sanctioned class.
- The approvals rail home: the rail methods move off the ERP *Service
  (whose session-issuing half stays behind) onto *Approvals, and the rail
  SQL half of the ERP Repository becomes ApprovalRepository. Signatures,
  SQL bytes, seams and behaviour carried.
- The context helpers (ContextWithStaff family) re-home from the ERP
  handler.go excerpt into bridge.go over the bridge key; the carried
  personal-data scrub class applies here too (one email literal, two
  first names, one date literal in carried comments, replaced with role
  descriptions).
- httpx: the door-test floor raised 37 to 41 with this batch's four
  non-test files (same standing rule, same file as the B1 row).
- model.go, auth.go, handler.go (routes), password_reset.go,
  google_oauth.go and the session halves of service.go/repository.go are
  deliberately NOT carried (the superseded HS256 session world); the
  left-behind list is the batch report's.

Rows for batch 2 (sources with two pin blobs are assembled carries; the
counts are measured over the excerpts, by the line ranges the report
states):

| carried file | pin sha256 (16) | em | en | box | mojibake |
|---|---|---|---|---|---|
| core/internal/identity/staffcontext/approvals.go | 9efe71bb8973abd5 + afeb0cb2bfdd1cf3 | 21 | 0 | 0 | 0 |
| core/internal/identity/staffcontext/approvals_rail.go | 61dc1a155152007f | 8 | 0 | 0 | 0 |
| core/internal/identity/staffcontext/approvals_rail_test.go | 7b5c7ff7d74218b5 | 5 | 0 | 0 | 0 |
| core/internal/identity/staffcontext/approvals_test.go | 25201b1bf2e3643e | 1 | 0 | 0 | 0 |
| core/internal/identity/staffcontext/bridge.go | NEW (handler.go 67508ac9ee6ea2d8 excerpt) | 3 | 0 | 0 | 0 |
| core/internal/identity/staffcontext/bridge_test.go | NEW |  |  |  |  |
| core/internal/identity/staffcontext/freight_approval_test.go | 787320cc2fc9075e | 9 | 0 | 293 | 0 |
| core/internal/identity/staffcontext/rbac.go | 5fb74fbfdeb83b71 | 21 | 0 | 153 | 0 |
| core/internal/identity/staffcontext/rbac_test.go | 97b8c2e73d8ad78f + 25201b1bf2e3643e | 14 | 0 | 0 | 0 |
| core/internal/identity/staffcontext/tx_fake_test.go | 112b9ea5c8865851 | 1 | 0 | 0 | 0 |

Every later carry batch appends its rows before review.

## Batch 3, the money spine (HH-3.2 B4): sanctioned edit classes

- The batch carries gl, pricing, inventory, order, quote (the plan's B4
  set) plus product and settings, the minimal compile closure of that set
  on the actual committed base (the plan's B4 entry criteria assumed the
  leaf batch merged; it is not, and pricing and quote do not compile
  without those two leaf modules). Findings-only, no mapping table (G1).
- Module code re-points its import from internal/identity to
  internal/identity/staffcontext under the identity alias (the B2
  sanctioned class; declaration names byte-identical).
- The Guards-constructor swap: every registrar of the seven modules takes
  (mux, g *guards.Guards); guarded handlers gain the caller parameter;
  tier mapping pinned to the pin's main.go wiring (requireStaff and
  readGuard to RequireStaff, requireManager and writeGuard to
  RequireManager, requireAdmin to RequireAdmin). The h.staff stamp
  adapter per module (caller_stamp.go files) is the bridge doc's
  request-path adapter: it stamps the bridged StaffContext from the
  guard-resolved caller, and the wire-supplied WithUserLinker linker adds
  the ERP users.id (the B4 linkage ruling over users.platform_user_id;
  nil stamps authority only).
- Cursor conversions: gl's journal list (stable ordering fiscal_year DESC,
  entry_no DESC, unique pair) and quote's staff list (created_at DESC,
  quote_no unique); fetch limit+1, drop the extra row, mint next_cursor
  from the last kept row. The offset family stays out of the tree; the
  tenant ListQuotes seam keeps its offset shape for the portal batch.
- The identity shim removal (Security S-3): the five order lifecycle
  operationIds and quotePublicViewQuote came out of the served shim
  registrar in the same change that registers the real order and quote
  routes on the serve mux (wire_money.go); they survive only as the
  DB-free RegisterSupersededProofRoutes the acceptance harness registers.
- door-test floor raised 41 to 188 with this batch's non-test files; the
  ERP's order bypass entries did NOT re-enter: the two hand-rolled
  envelopes (change_handler.go, duplicate.go) re-route through
  httpx.ErrorWith, so the baseline still holds only authz.
- personal-data class, this batch: 17 person references in carried prose
  (a dealer-principal first name plus initial, its possessive, and an
  example display name) replaced with role descriptions, plus 1 email
  literal in a test comment replaced with a role description; the
  product-vocabulary term in settings.go and the fixture display names in
  order's test files are data, kept, counts only. The A3 classes
  provenance-date and data-date also apply where carried comments cite
  scope-reconciliation dates (kept as provenance) and test fixtures carry
  RFC3339 literals (kept as data).
- wire-log-punctuation, this batch: 140 string-literal em dashes in
  non-test files became comma punctuation (keywords intact), measured by
  the census command in the batch report; test assertion strings carry
  the same normalization without reaching any wire.

Rows for batch 3 (counts measured at the pin before the sanctioned edits;
the edits above are recorded per class, not per count):

| carried file | pin sha256 (16) | em | en | box | mojibake |
|---|---|---|---|---|---|
| core/internal/gl/approvals_rail_test.go | de18bb8d79a8416a | 4 | 0 | 0 | 0 |
| core/internal/gl/approvers.go | 51b17c1c13a5dd33 | 7 | 0 | 255 | 0 |
| core/internal/gl/approvers_integration_test.go | b06a8ee994599104 | 7 | 0 | 259 | 0 |
| core/internal/gl/bistrack_tieout.go | 49387d2ba658acbf | 11 | 0 | 4 | 0 |
| core/internal/gl/bistrack_tieout_demo_seed_integration_test.go | 4a252d1e7fa0a7d7 | 6 | 0 | 0 | 0 |
| core/internal/gl/bistrack_tieout_integration_test.go | 79d5e224b2a652f1 | 16 | 0 | 0 | 0 |
| core/internal/gl/bistrack_tieout_test.go | 24324633953fa06e | 8 | 0 | 0 | 0 |
| core/internal/gl/businessday.go | 74d79ab1f973ab2e | 7 | 0 | 175 | 0 |
| core/internal/gl/businessday_test.go | 3a5576bb2a17dfec | 10 | 0 | 0 | 0 |
| core/internal/gl/close.go | e1fd79e4729b169e | 10 | 0 | 345 | 0 |
| core/internal/gl/consumer_test.go | dd6ffd6c5812ab25 | 7 | 0 | 0 | 0 |
| core/internal/gl/dib418_entry_no_integration_test.go | e5dbfbf94ffe9db5 | 12 | 1 | 0 | 0 |
| core/internal/gl/handler.go | adff49b5c86d0c72 | 6 | 0 | 382 | 2 |
| core/internal/gl/handler_branchid_test.go | 0adefc382a38526d | 3 | 0 | 0 | 1 |
| core/internal/gl/handler_pathid_test.go | f6253192e501db57 | 3 | 0 | 0 | 0 |
| core/internal/gl/handler_sourcerefid_live_integration_test.go | c72cd1f48e966a0a | 1 | 0 | 0 | 0 |
| core/internal/gl/handler_sourcerefid_test.go | 28cdd223663a2db2 | 1 | 0 | 0 | 0 |
| core/internal/gl/integration_test.go | dfbe42cea5ee5a26 | 17 | 0 | 0 | 0 |
| core/internal/gl/journal.go | f8cc4a7647884687 | 9 | 0 | 281 | 0 |
| core/internal/gl/list_journals_source_ref_integration_test.go | e902c9393f0be697 | 2 | 0 | 0 | 0 |
| core/internal/gl/model.go | 48634b188d119884 | 4 | 0 | 210 | 0 |
| core/internal/gl/module.go | d678637e99ea303c | 11 | 0 | 276 | 0 |
| core/internal/gl/post_external_guard_test.go | 9bec401450880822 | 2 | 0 | 0 | 0 |
| core/internal/gl/posting.go | dbf366401e5824f9 | 14 | 0 | 75 | 0 |
| core/internal/gl/posting_cogs_costbasis_test.go | 4263492e72da55e2 | 2 | 0 | 0 | 0 |
| core/internal/gl/posting_return_fee_test.go | 043dfbaa703b1f06 | 1 | 0 | 0 | 0 |
| core/internal/gl/read_guard_integration_test.go | 7bb4fe34159c3af9 | 1 | 0 | 0 | 0 |
| core/internal/gl/read_guard_test.go | a5829109b630b1d7 | 5 | 0 | 0 | 0 |
| core/internal/gl/repository.go | 61d4bc10f1180745 | 14 | 0 | 545 | 0 |
| core/internal/gl/rules_seam.go | f1333fc0ab54ddac | 3 | 0 | 0 | 0 |
| core/internal/gl/service_test.go | 7d013ddaa18096bf | 6 | 0 | 0 | 0 |
| core/internal/gl/store.go | ff07520589b64511 | 3 | 0 | 36 | 0 |
| core/internal/gl/store_fake_test.go | 4f518465594220fe | 1 | 0 | 118 | 0 |
| core/internal/pricing/admin_test.go | a78b6d87d905a2f3 | 1 | 0 | 188 | 0 |
| core/internal/pricing/auditref.go | 77c2fb95adf21f8c | 10 | 0 | 0 | 0 |
| core/internal/pricing/auditref_integration_test.go | 3f2d8bc34c76bb1c | 7 | 0 | 0 | 0 |
| core/internal/pricing/auditref_test.go | 67bd2229a3fd9543 | 10 | 0 | 65 | 0 |
| core/internal/pricing/batch_handler.go | 8797ec1b06293ee0 | 3 | 0 | 0 | 0 |
| core/internal/pricing/batch_integration_test.go | 35e20a9e3ad15a33 | 4 | 0 | 0 | 0 |
| core/internal/pricing/batch_model.go | 11e01da189d20e34 | 11 | 1 | 20 | 0 |
| core/internal/pricing/batch_repository.go | 8d7f78915577c31b | 0 | 0 | 0 | 0 |
| core/internal/pricing/batch_service.go | da389908f0d0f625 | 4 | 0 | 176 | 0 |
| core/internal/pricing/batch_service_test.go | 40b36e1fbb0be874 | 1 | 0 | 152 | 0 |
| core/internal/pricing/clientstatus_test.go | 05fefc3dcacf9e74 | 0 | 0 | 0 | 0 |
| core/internal/pricing/counter.go | 0a443ca0ef668006 | 16 | 0 | 47 | 0 |
| core/internal/pricing/counter_test.go | 171dd7b12bcd6b29 | 3 | 0 | 343 | 0 |
| core/internal/pricing/dib420_integration_test.go | ce13d3c1869d3880 | 5 | 0 | 0 | 0 |
| core/internal/pricing/dib420_override_authority_test.go | a83c2e825ee35064 | 11 | 0 | 180 | 0 |
| core/internal/pricing/findings_test.go | 28534081eba1992a | 3 | 0 | 198 | 0 |
| core/internal/pricing/gate.go | 486289d44285a461 | 1 | 0 | 0 | 0 |
| core/internal/pricing/handler.go | 3c4cd552b2bc9a27 | 20 | 1 | 201 | 0 |
| core/internal/pricing/handler_test.go | f0baa57c5689a040 | 1 | 0 | 125 | 0 |
| core/internal/pricing/integration_test.go | b1047d1ee8e81d8d | 40 | 0 | 29 | 0 |
| core/internal/pricing/listprice_bulk.go | 0fb200a40a4f3bca | 7 | 0 | 49 | 0 |
| core/internal/pricing/lowmargin_scan_test.go | 2485ab1bd8df93b9 | 3 | 0 | 46 | 0 |
| core/internal/pricing/model.go | dcc8b450a107eefe | 28 | 0 | 152 | 0 |
| core/internal/pricing/override_authority.go | 0a763fe2e1bcf712 | 14 | 0 | 320 | 0 |
| core/internal/pricing/percent_format_test.go | 900330229f446548 | 2 | 0 | 0 | 0 |
| core/internal/pricing/pricefile_handler.go | 76a6e750e12630dc | 6 | 0 | 118 | 0 |
| core/internal/pricing/pricefile_integration_test.go | f37e54735ea779f3 | 15 | 0 | 190 | 0 |
| core/internal/pricing/pricefile_model.go | b2f79e8ec510dcfa | 28 | 0 | 348 | 0 |
| core/internal/pricing/pricefile_money_test.go | 041c551c0aabd7a9 | 9 | 0 | 236 | 0 |
| core/internal/pricing/pricefile_repository.go | 77385911fa6adc36 | 6 | 0 | 0 | 0 |
| core/internal/pricing/pricefile_service.go | 0b08784f894ca625 | 25 | 0 | 602 | 0 |
| core/internal/pricing/pricefile_service_test.go | 0206d0aba826ed7f | 11 | 0 | 597 | 0 |
| core/internal/pricing/promo.go | f02b58c473576e5d | 20 | 0 | 79 | 0 |
| core/internal/pricing/promo_test.go | e7ea9986b4265ef0 | 6 | 0 | 375 | 0 |
| core/internal/pricing/rebate.go | 8920f942f3c9f677 | 18 | 0 | 79 | 0 |
| core/internal/pricing/rebate_test.go | c2a7544f54357706 | 7 | 0 | 512 | 0 |
| core/internal/pricing/repository.go | aded3b7a3c47177e | 12 | 0 | 173 | 0 |
| core/internal/pricing/service.go | 545983da64907a49 | 84 | 3 | 343 | 0 |
| core/internal/pricing/service_test.go | 1b413a4b5a60a4b0 | 14 | 5 | 535 | 0 |
| core/internal/pricing/uomplan.go | b8dd4ff6fe6ec869 | 15 | 0 | 71 | 0 |
| core/internal/pricing/uomplan_test.go | e133b58549ea1e55 | 6 | 0 | 491 | 0 |
| core/internal/pricing/w4_handler.go | 53d762d1912d9aff | 3 | 0 | 339 | 0 |
| core/internal/pricing/w4_integration_test.go | 41128a0b0e0cc7c9 | 6 | 0 | 219 | 0 |
| core/internal/pricing/w4_repository.go | ee17407c720b6f2c | 11 | 0 | 246 | 0 |
| core/internal/pricing/waterfall_test.go | 983651bc35d1502d | 5 | 0 | 392 | 0 |
| core/internal/pricing/xlsx_reader.go | dccd55144721fe8b | 12 | 0 | 213 | 0 |
| core/internal/pricing/xlsx_reader_test.go | db4228461a688e23 | 10 | 0 | 39 | 0 |
| core/internal/inventory/availability.go | b815a569a4c5c3f9 | 9 | 0 | 0 | 0 |
| core/internal/inventory/availability_test.go | d661c2f9135774f8 | 1 | 0 | 0 | 0 |
| core/internal/inventory/browse.go | c62b25c8b1a3987c | 16 | 0 | 73 | 0 |
| core/internal/inventory/count_seam.go | 277dfa5075d8a2c8 | 1 | 0 | 0 | 0 |
| core/internal/inventory/dib396_lease_test.go | 32de8ff39b4ac294 | 8 | 0 | 0 | 0 |
| core/internal/inventory/dib443_lookup_integration_test.go | 2cc1dcffdd02b083 | 6 | 0 | 0 | 0 |
| core/internal/inventory/dib443_lookup_test.go | 25e81c4c0db2756f | 11 | 0 | 159 | 0 |
| core/internal/inventory/dib451_pending_branch_integration_test.go | a28bc97c904b7d37 | 6 | 0 | 0 | 0 |
| core/internal/inventory/dib451_pending_branch_test.go | 1de5d68423aa4b1f | 11 | 0 | 223 | 0 |
| core/internal/inventory/dib452_browse_paging_integration_test.go | d320631ddfd4119f | 12 | 0 | 0 | 0 |
| core/internal/inventory/dib452_browse_paging_test.go | d9964f6a695b3f8a | 14 | 0 | 144 | 0 |
| core/internal/inventory/dib470_onorder_test.go | 7a1caa103dc9e26e | 27 | 0 | 156 | 0 |
| core/internal/inventory/dib472_transfer_eta_integration_test.go | ef62286de9064849 | 21 | 0 | 0 | 0 |
| core/internal/inventory/dib472_transfer_eta_test.go | 913aab4a34acda38 | 15 | 0 | 200 | 0 |
| core/internal/inventory/fake_store_test.go | d52f5b4697a8afd2 | 2 | 0 | 83 | 0 |
| core/internal/inventory/handler.go | 115c64273851892f | 18 | 0 | 0 | 0 |
| core/internal/inventory/incoming.go | 22e49e89635c1814 | 9 | 0 | 88 | 0 |
| core/internal/inventory/incoming_integration_test.go | d7195887b16485a3 | 9 | 0 | 0 | 0 |
| core/internal/inventory/incoming_repository.go | 96d2110630c8d4e4 | 5 | 0 | 0 | 0 |
| core/internal/inventory/incoming_test.go | fb1640d235b84540 | 31 | 0 | 333 | 0 |
| core/internal/inventory/integration_test.go | 5e50fde51beccb09 | 15 | 0 | 249 | 0 |
| core/internal/inventory/landed_capitalize_test.go | 895ecb8b27b2b7fd | 24 | 0 | 0 | 0 |
| core/internal/inventory/landed_seam.go | 2a559afd8883e31c | 9 | 0 | 0 | 0 |
| core/internal/inventory/lead_time.go | c38d2f6a6655ea1f | 3 | 0 | 0 | 0 |
| core/internal/inventory/model.go | 973d50eb8ac8f05b | 3 | 0 | 117 | 0 |
| core/internal/inventory/module.go | afdbce403198fe35 | 13 | 0 | 331 | 0 |
| core/internal/inventory/on_order_seam.go | 965c7199cfb61680 | 12 | 0 | 216 | 0 |
| core/internal/inventory/pricing_seam.go | 6c7d16769ec313cf | 1 | 0 | 0 | 0 |
| core/internal/inventory/reorder_actions.go | 926fdac0fef9ca23 | 33 | 0 | 262 | 0 |
| core/internal/inventory/replenish.go | d0bb86e312a7939a | 19 | 0 | 112 | 0 |
| core/internal/inventory/replenish_repository.go | 06cfaf053003049c | 7 | 0 | 0 | 0 |
| core/internal/inventory/repository.go | ae4ba59712a59cbc | 11 | 0 | 166 | 0 |
| core/internal/inventory/reservation.go | 152210e7047c3cef | 11 | 0 | 194 | 1 |
| core/internal/inventory/reservation_repository.go | f4d87be5d8041f63 | 4 | 0 | 0 | 0 |
| core/internal/inventory/service.go | 2d3c56757412ff61 | 32 | 0 | 276 | 0 |
| core/internal/inventory/service_test.go | b6e1d8925b6732ee | 9 | 0 | 68 | 0 |
| core/internal/inventory/stock_lookup.go | 8e3c962e60f4ae9f | 12 | 0 | 90 | 0 |
| core/internal/inventory/stock_lookup_test.go | 3f61c40fcde50ffc | 7 | 0 | 65 | 0 |
| core/internal/inventory/stock_truth_handler.go | 1237d22c4d79c711 | 26 | 0 | 351 | 0 |
| core/internal/inventory/stock_truth_integration_test.go | a58d46e5f6935234 | 39 | 0 | 418 | 0 |
| core/internal/inventory/stock_truth_test.go | 56a54a9ef1bbeb99 | 20 | 0 | 294 | 0 |
| core/internal/inventory/transfer.go | 57fb850d4775f6dd | 23 | 0 | 318 | 0 |
| core/internal/inventory/transfer_eta.go | 83b3906022072453 | 17 | 0 | 231 | 0 |
| core/internal/inventory/transfer_eta_repository.go | 3f6de9a2486c5dd1 | 8 | 0 | 0 | 0 |
| core/internal/inventory/transfer_repository.go | 964f0690ac21666c | 4 | 0 | 0 | 0 |
| core/internal/inventory/util.go | 4040bf9a7207486b | 1 | 0 | 0 | 0 |
| core/internal/inventory/visibility.go | 8f1da51b7bc34075 | 10 | 0 | 0 | 0 |
| core/internal/inventory/wac_adversarial_integration_test.go | a294c96eb2cc38ea | 9 | 0 | 200 | 0 |
| core/internal/inventory/wac_concurrency_integration_test.go | 3a7e1724a04739de | 6 | 0 | 0 | 0 |
| core/internal/inventory/wac_lock_test.go | 56466930bbe0e976 | 5 | 0 | 0 | 0 |
| core/internal/order/activity.go | cc7dbe8d20720d80 | 35 | 0 | 298 | 0 |
| core/internal/order/activity_integration_test.go | b976ce07447215a7 | 9 | 0 | 0 | 0 |
| core/internal/order/activity_repository.go | 4bd722f5493eb965 | 10 | 0 | 0 | 0 |
| core/internal/order/activity_test.go | d9d9a458612ddea5 | 9 | 0 | 325 | 0 |
| core/internal/order/activity_trail.go | 9dd40d5fce8417bb | 14 | 0 | 0 | 0 |
| core/internal/order/activity_trail_test.go | 33631b2c567db82f | 7 | 0 | 0 | 0 |
| core/internal/order/cancel_seam.go | 5fa86aaf4515fed1 | 8 | 0 | 0 | 0 |
| core/internal/order/change.go | 0e274c4d568ac3b1 | 33 | 0 | 902 | 0 |
| core/internal/order/change_handler.go | b129acc6efe9ecfd | 6 | 0 | 331 | 0 |
| core/internal/order/change_repository.go | 45427b3a7d3541c5 | 10 | 0 | 56 | 0 |
| core/internal/order/consumer.go | 2f0cda123e08c562 | 11 | 0 | 0 | 0 |
| core/internal/order/consumer_test.go | 32947dfd44cbd602 | 6 | 0 | 63 | 0 |
| core/internal/order/customer_reader_integration_test.go | 946d3953bc79b4e7 | 4 | 0 | 0 | 0 |
| core/internal/order/dib396_lease_test.go | 0ed646572bf49f2d | 4 | 0 | 0 | 0 |
| core/internal/order/dib513_orderref_integration_test.go | e633ddf46020ae0d | 6 | 0 | 0 | 0 |
| core/internal/order/dib513_shipments_test.go | 8a90d9a0b8664c64 | 3 | 0 | 0 | 0 |
| core/internal/order/duplicate.go | 498a08ea5f5b62fb | 42 | 0 | 519 | 0 |
| core/internal/order/duplicate_integration_test.go | 7a8fa84f2ab7cd56 | 8 | 0 | 0 | 0 |
| core/internal/order/duplicate_test.go | 09d0d6a73c2137fc | 9 | 0 | 330 | 0 |
| core/internal/order/edit.go | c35e931ce7d80975 | 46 | 0 | 836 | 0 |
| core/internal/order/edit_handler.go | 23caff025f33da93 | 9 | 0 | 0 | 0 |
| core/internal/order/edit_history.go | cbba250135bef52f | 10 | 0 | 0 | 0 |
| core/internal/order/edit_integration_test.go | e48b905bfc7af3ab | 15 | 0 | 546 | 0 |
| core/internal/order/edit_repository.go | d88d5b95f5676fd1 | 16 | 0 | 0 | 0 |
| core/internal/order/edit_test.go | d32c9a5033043b91 | 22 | 0 | 611 | 0 |
| core/internal/order/handler.go | aaf557c88535bec3 | 10 | 0 | 0 | 0 |
| core/internal/order/handler_test.go | cbec58448ff0cbe8 | 10 | 0 | 166 | 0 |
| core/internal/order/history.go | 66fa99ee719c90d1 | 15 | 0 | 12 | 0 |
| core/internal/order/history_integration_test.go | dcfd8d9d86a69793 | 7 | 0 | 0 | 0 |
| core/internal/order/history_test.go | 4bb26651ae3e7596 | 13 | 0 | 31 | 0 |
| core/internal/order/invoice_source.go | 4efe046abd4f193d | 11 | 0 | 272 | 0 |
| core/internal/order/lien_seam.go | 3cbc2a7c05f45266 | 2 | 0 | 0 | 0 |
| core/internal/order/model.go | 056377a6442fecf7 | 0 | 0 | 0 | 0 |
| core/internal/order/order_no_integration_test.go | 957d0d084f74d8f1 | 5 | 0 | 0 | 0 |
| core/internal/order/reorder.go | 0e065f81f52fabdb | 16 | 0 | 254 | 0 |
| core/internal/order/reorder_integration_test.go | 8b9a80a4b809d49e | 1 | 0 | 0 | 0 |
| core/internal/order/reorder_test.go | 3fdd256578a725b2 | 7 | 0 | 0 | 0 |
| core/internal/order/repository.go | 2b956fb56b16f8bc | 25 | 0 | 0 | 0 |
| core/internal/order/reservation_repository.go | d40c4bbc69e402bd | 7 | 0 | 0 | 0 |
| core/internal/order/reservations.go | 53de1f10fc5cdad7 | 8 | 0 | 97 | 0 |
| core/internal/order/service.go | e98e65d50ef61bec | 44 | 0 | 82 | 0 |
| core/internal/order/service_test.go | dde7315426b6adf9 | 5 | 0 | 0 | 0 |
| core/internal/order/shipgroup.go | c93ca455be8374d3 | 30 | 1 | 431 | 0 |
| core/internal/order/shipgroup_repository.go | b7b21f84a46b0fb5 | 6 | 0 | 0 | 0 |
| core/internal/order/soft_hold_expiry.go | 3d93838b42b1d7e9 | 13 | 0 | 221 | 0 |
| core/internal/order/stock_seam.go | b76347c44e4498e5 | 11 | 0 | 120 | 0 |
| core/internal/order/summary.go | 6b2d947be46e02b2 | 9 | 0 | 0 | 0 |
| core/internal/order/summary_test.go | 85fb439671d42678 | 5 | 0 | 0 | 0 |
| core/internal/order/transition_test.go | a50bbe25c13a8b49 | 14 | 0 | 68 | 0 |
| core/internal/order/w5a_change_integration_test.go | 576ab0202b340f16 | 24 | 0 | 169 | 0 |
| core/internal/order/w5a_reservation_integration_test.go | 8adeaa6e21805dac | 20 | 0 | 185 | 0 |
| core/internal/order/w5a_rules_test.go | 183c3723e22080e6 | 14 | 0 | 348 | 0 |
| core/internal/order/w5a_shipgroup_integration_test.go | d5ed9c48f2e865da | 19 | 0 | 0 | 0 |
| core/internal/order/w6c_invoice_rules_test.go | 04ec9888ab0e2148 | 7 | 0 | 0 | 0 |
| core/internal/order/w6c_soft_hold_expiry_integration_test.go | 6bbfd7fb755856f5 | 6 | 0 | 0 | 0 |
| core/internal/quote/acceptcontrols.go | 62631f249b8a5671 | 18 | 0 | 0 | 0 |
| core/internal/quote/acceptcontrols_repository.go | 5948bf6dbffb7fbd | 9 | 0 | 0 | 0 |
| core/internal/quote/builder.go | 944cf0b3119b516d | 20 | 0 | 303 | 0 |
| core/internal/quote/builder_handler.go | b72090cceca694ac | 3 | 0 | 0 | 0 |
| core/internal/quote/builder_repository.go | 4087130a0b9fc2a7 | 9 | 0 | 0 | 0 |
| core/internal/quote/compare.go | ed8bd2bd52783144 | 6 | 0 | 0 | 0 |
| core/internal/quote/compare_integration_test.go | 858977c4697b0e47 | 10 | 0 | 0 | 0 |
| core/internal/quote/convert.go | 43dfe5432465609a | 16 | 0 | 207 | 0 |
| core/internal/quote/convert_seam_test.go | 24877ddc42309bdb | 28 | 0 | 247 | 0 |
| core/internal/quote/convert_staff.go | ebe0790824762022 | 10 | 0 | 0 | 0 |
| core/internal/quote/credit.go | 5144a9f56f155f93 | 16 | 0 | 0 | 0 |
| core/internal/quote/dib396_lease_test.go | 524d4f86b3267aa1 | 4 | 0 | 0 | 0 |
| core/internal/quote/dib428_softhold_expiry_integration_test.go | d620019573e03d94 | 17 | 0 | 42 | 0 |
| core/internal/quote/dib428_softhold_seam_test.go | b3cb062f31ff6652 | 17 | 0 | 167 | 0 |
| core/internal/quote/dib482_cadence_integration_test.go | cacc20480b014216 | 11 | 0 | 0 | 0 |
| core/internal/quote/dib491_acceptcontrols_integration_test.go | 93b7e32f12a9e6e6 | 25 | 0 | 291 | 0 |
| core/internal/quote/dib491_acceptcontrols_test.go | 09870bf64ffab61d | 13 | 0 | 311 | 0 |
| core/internal/quote/dib506_test.go | d44243ec69fdfc9c | 6 | 0 | 267 | 0 |
| core/internal/quote/edit.go | 9071a0ce85ba7c57 | 43 | 0 | 928 | 0 |
| core/internal/quote/edit_handler.go | 411f7e0171eb0cc6 | 18 | 0 | 129 | 0 |
| core/internal/quote/edit_history.go | c58448374c321aed | 8 | 0 | 0 | 0 |
| core/internal/quote/edit_integration_test.go | b8aa54e8828c1fc8 | 13 | 0 | 547 | 0 |
| core/internal/quote/edit_repository.go | 018b6a2774ef0712 | 18 | 0 | 339 | 0 |
| core/internal/quote/edit_test.go | 52998b7281745ad4 | 26 | 0 | 991 | 0 |
| core/internal/quote/edit_write.go | 5d6a8c229649d176 | 10 | 0 | 86 | 0 |
| core/internal/quote/followup.go | 62ef36de57dcff1f | 6 | 0 | 468 | 0 |
| core/internal/quote/followup_handler.go | 1a95f1da415129fa | 8 | 0 | 0 | 0 |
| core/internal/quote/followup_integration_test.go | 3273b0cd5aabc229 | 15 | 0 | 0 | 0 |
| core/internal/quote/followup_on_share.go | e4a14d5daad92bbe | 9 | 0 | 0 | 0 |
| core/internal/quote/followup_repository.go | 102e4d953049614f | 7 | 0 | 0 | 0 |
| core/internal/quote/followup_service.go | b90488dc6efa5b34 | 14 | 0 | 193 | 0 |
| core/internal/quote/followup_test.go | e700115191d0f7e4 | 9 | 0 | 0 | 0 |
| core/internal/quote/lineage.go | 5ae6b966d818854d | 7 | 0 | 0 | 0 |
| core/internal/quote/lineage_repository.go | bef5808be028932f | 8 | 0 | 0 | 0 |
| core/internal/quote/model.go | 93d9293303cf2e4a | 3 | 0 | 0 | 0 |
| core/internal/quote/repository.go | dc7dfcc8e78929a2 | 13 | 0 | 0 | 0 |
| core/internal/quote/reserve.go | ee44b6be479a66a4 | 10 | 0 | 148 | 0 |
| core/internal/quote/reserve_handler.go | fb08ef9fac23671c | 3 | 0 | 0 | 0 |
| core/internal/quote/reserve_repository.go | c2d4e355125c7b27 | 4 | 0 | 0 | 0 |
| core/internal/quote/service.go | c92b27f8a3e04731 | 6 | 0 | 0 | 0 |
| core/internal/quote/service_test.go | 706e442eee1ae015 | 1 | 0 | 0 | 0 |
| core/internal/quote/share.go | 640d4379bb53930d | 35 | 0 | 328 | 0 |
| core/internal/quote/share_handler.go | 1c1d6428975b8e0f | 7 | 0 | 0 | 0 |
| core/internal/quote/share_integration_test.go | 75aff5d58fdabdd7 | 10 | 0 | 457 | 0 |
| core/internal/quote/share_repository.go | d9d6ab0db1820c0d | 12 | 0 | 0 | 0 |
| core/internal/quote/share_test.go | c01adc368c94805f | 10 | 0 | 151 | 0 |
| core/internal/quote/softhold_expiry_seam.go | c2d81f94fe538983 | 23 | 0 | 228 | 0 |
| core/internal/quote/staff.go | 01b63a44c769ff7e | 14 | 0 | 178 | 0 |
| core/internal/quote/stock_seam.go | 153db8ad06b9cca7 | 9 | 0 | 209 | 0 |
| core/internal/quote/w5a_reserve_integration_test.go | d44083459ab09fe1 | 23 | 0 | 200 | 0 |
| core/internal/quote/w5a_rules_test.go | 6deba94a98c28551 | 3 | 0 | 0 | 0 |
| core/internal/product/counter_search.go | 8d0c3e19c80a9a1e | 17 | 0 | 125 | 0 |
| core/internal/product/counter_search_integration_test.go | f3dc852423d983be | 17 | 0 | 259 | 0 |
| core/internal/product/coverage.go | 89b2be8626090c97 | 17 | 0 | 88 | 0 |
| core/internal/product/coverage_integration_test.go | 58ef5ec7dd07d85a | 8 | 0 | 89 | 0 |
| core/internal/product/coverage_test.go | 97e31749db495890 | 13 | 0 | 99 | 0 |
| core/internal/product/detail.go | a2839feee6134968 | 6 | 0 | 0 | 0 |
| core/internal/product/dib482_search_integration_test.go | a434497312075b00 | 13 | 0 | 0 | 0 |
| core/internal/product/handler.go | a75038d5f25d4324 | 14 | 0 | 0 | 0 |
| core/internal/product/images.go | b60e428c30e8497a | 4 | 0 | 162 | 0 |
| core/internal/product/integration_test.go | c235680f35b89401 | 6 | 0 | 61 | 0 |
| core/internal/product/model.go | f683d1aef98fda35 | 5 | 0 | 44 | 0 |
| core/internal/product/paging_integration_test.go | b558ac0215de743e | 5 | 0 | 0 | 0 |
| core/internal/product/paging_test.go | 1b96c86455e1e29d | 9 | 0 | 62 | 0 |
| core/internal/product/repository.go | 2dd60c552b6376dc | 14 | 0 | 46 | 0 |
| core/internal/product/service.go | e58be4675988974c | 16 | 0 | 20 | 0 |
| core/internal/product/service_test.go | c7116ccfa2f88032 | 12 | 0 | 200 | 0 |
| core/internal/settings/handler.go | 9fa5486267fce0a6 | 4 | 0 | 0 | 0 |
| core/internal/settings/openref_test.go | 2432d1962072ae45 | 5 | 0 | 0 | 0 |
| core/internal/settings/platform_hotkeys.go | 7cde8cc27537877a | 28 | 0 | 161 | 0 |
| core/internal/settings/platform_hotkeys_effective.go | 625369856e7f5ede | 24 | 0 | 327 | 0 |
| core/internal/settings/platform_hotkeys_effective_test.go | ce7778d47a676d51 | 7 | 0 | 162 | 0 |
| core/internal/settings/platform_hotkeys_integration_test.go | 67214fd9ca4719b6 | 16 | 0 | 31 | 0 |
| core/internal/settings/platform_hotkeys_store.go | 7a6a76e263afe1fc | 6 | 0 | 0 | 0 |
| core/internal/settings/platform_hotkeys_test.go | 52b16d02b2383f69 | 15 | 0 | 137 | 0 |
| core/internal/settings/service.go | 369b5930991713e6 | 13 | 0 | 0 | 0 |
| core/internal/settings/settings.go | c6277eae120064e8 | 13 | 0 | 169 | 0 |
| core/internal/settings/settings_test.go | a1c3dd135933bec3 | 11 | 0 | 416 | 0 |
| core/internal/settings/store.go | b7a915cca4c719bf | 3 | 0 | 0 | 0 |
| core/internal/gl/caller_stamp.go | NEW |  |  |  |  |
| core/internal/pricing/caller_stamp.go | NEW |  |  |  |  |
| core/internal/inventory/caller_stamp.go | NEW |  |  |  |  |
| core/internal/order/caller_stamp.go | NEW |  |  |  |  |
| core/internal/quote/caller_stamp.go | NEW |  |  |  |  |
| core/internal/product/caller_stamp.go | NEW |  |  |  |  |
| core/internal/settings/caller_stamp.go | NEW |  |  |  |  |
| core/internal/identity/staffcontext/stamp.go | NEW |  |  |  |  |
| core/internal/order/lifecycle_seed_integration_test.go | NEW |  |  |  |  |
| core/internal/quote/credit_helpers_integration_test.go | NEW |  |  |  |  |
| core/cmd/core/wire_money.go | NEW |  |  |  |  |
| core/internal/testkit/staffguards.go | NEW |  |  |  |  |

Batch totals: 3053 em, 12 en, 34212 box, 4 mojibake normalized (corrected at the B5 carry to the row sums: the recorded figure carried the deleted phantom row's 3 em and the pre-correction runs total for box).

## Batch 4, the operations cluster (HH-3.2 B5): sanctioned edit classes

- The batch carries the plan's B5 set (dispatch, tasks, messaging, lien,
  landedcost, invcount, warranty, printing, bankrec, till, purchasing)
  plus payments, which the A1 review's F-1 assigned to this batch (the
  2,659-line module, the same guards-swap, stamp and linker discipline
  as the money spine). Findings-only, no mapping table (G1).
- Module code re-points its import from internal/identity to
  internal/identity/staffcontext under the identity alias (the B2
  sanctioned class; declaration names byte-identical).
- The Guards-constructor swap: every registrar of the twelve modules
  takes (mux, g *guards.Guards); guarded handlers gain the caller
  parameter; tier mapping pinned to the pin's main.go wiring lines
  (requireStaff/requireManager/requireAdmin to RequireStaff/
  RequireManager/RequireAdmin, name for name; dispatch's and warranty's
  public registrars stay bare, the quote share-link precedent). The
  h.staff stamp adapter per module (caller_stamp.go files) plus the
  payments ReversalHandler's is the bridge doc's request-path adapter;
  the wire-supplied WithUserLinker linker adds the ERP users.id (the B4
  linkage ruling).
- The rail's HTTP decide surface lands in the bridge package
  (identity/staffcontext/approvals_handler.go, NEW): the A3 ownership
  ruling applied, the batch that first wires rail entry points to the
  request path. The decide-path re-point rides with it: ListApprovals
  and DecideApproval take the bridged StaffContext the guard resolved
  (the signature variant the A3 review named), the store's
  scaffold-table ResolveStaff read is re-pointed over staff_role_grant
  keyed through users.platform_user_id (tombstones honored, the same
  linkage column the linker reads) and is a projection read only (lien's
  co-signer), and ListApproverCandidates (fan-out only) gains the 0213
  tombstone predicates on every leg.
- Cursor conversions (the plan's enumeration for this batch): the tasks
  staff list and yard pick queue (stable ordering open-first flag,
  priority rank, created_at, id tie-breaker; the id completes the key the
  pin's three-column order left ambiguous) and bankrec's statement lines
  (txn_date DESC, created_at DESC, id DESC). The yard pending-tickets
  feed converts its envelope with a position-carrying cursor: the feed is
  an in-memory merge the pin's own design rules cannot be keyset-paged
  at the database, recorded as this endpoint's convention difference.
  The offset family stays out of the tree.
- P-4 closes: the driver session ceiling's knob source is the
  dispatch-owned dispatch_policy reader (dispatchDriverSessionKnob over
  the dealer-default row through the module's own ValidatePolicy),
  refusing on unreadable policy per the B2-07 no-fallback ruling where
  the pin's login-time read failed to the twelve-hour default (recorded
  in the batch report).
- The stream-ticket HMAC moves to a dedicated environment input
  (CORE_STREAM_TICKET_KEY; the A1 ruling, provisioned per the wiring
  notes, value never in any file). Unset leaves the tickets seam dark
  (501) and clients use the Bearer stream, the designed degradation.
- No shim removals this batch: the remaining served shims (tenant set,
  sync pair, capability) belong to Phase D and the tops batch, and the
  batch's 214 registered patterns were census-diffed against the shim
  set with zero overlap; the permanent registration test
  (cmd/core/wire_test.go) pins the served set free of duplicates.
- door-test floor raised 188 to 342 with this batch's non-test files;
  the bypassBaseline did not grow.
- personal-data class, this batch: 19 person references in non-test
  prose replaced with role descriptions (dealer-principal names and
  possessives quoted in dispatch's load-board comments, an example
  display name pair, and a demo persona name in lien's package
  comment). Kept as data, counts only: seeded demo-persona fixture
  names and channel names in test files, and example-domain email
  literals in test fixtures.
- wire-log-punctuation, this batch: 167 string-literal em dashes in
  non-test files became comma punctuation (keywords intact), measured by
  the census command in the batch report; 339 more in test assertion
  strings reach no wire.

Record correction at the B5 carry (inherited batch-3 gap): the money-spine
batch carried three testdata files its rows omitted, and one of them (the
gl BisTrack snapshot) carried em dashes in its JSON banner strings, which
the house shape gate flags on the committed tree. Rows appended here, and
the snapshot's 3 banner em dashes normalized (comma substitution; JSON
validity re-proven; no data value touched).

| carried file | pin sha256 (16) | em | en | box | mojibake |
|---|---|---|---|---|---|
| core/internal/gl/testdata/bistrack_gl_snapshot_2026-06.json | bd35c139381bad4d | 3 | 0 | 0 | 0 |
| core/internal/pricing/testdata/unilock_sample.csv | 43f87d5666aee915 | 0 | 0 | 0 | 0 |
| core/internal/pricing/testdata/techo_sample.csv | d31bcf563221bc8e | 0 | 0 | 0 | 0 |

Rows for batch 4 (counts measured at the pin before the sanctioned edits,
as CHARACTERS for every class, the corrected metric; the edits above are
recorded per class, not per count):
| carried file | pin sha256 (16) | em | en | box | mojibake |
|---|---|---|---|---|---|
| core/internal/bankrec/handler.go | 8f572ea74aa77d81 | 1 | 0 | 64 | 0 |
| core/internal/bankrec/integration_test.go | bf528b0c297eb38e | 3 | 0 | 339 | 0 |
| core/internal/bankrec/matcher.go | 172054969a1bb965 | 4 | 0 | 0 | 0 |
| core/internal/bankrec/matcher_test.go | 2f1d40bf80d9e2aa | 4 | 0 | 0 | 0 |
| core/internal/bankrec/model.go | 9864dec303443283 | 5 | 0 | 209 | 0 |
| core/internal/bankrec/module.go | 4cf2d54b13c7fa57 | 2 | 0 | 294 | 0 |
| core/internal/bankrec/repository.go | 8e738510285f802f | 7 | 0 | 376 | 0 |
| core/internal/bankrec/service.go | 0f1ba57f290a3aa2 | 7 | 0 | 336 | 0 |
| core/internal/bankrec/statement_source.go | 755575bf5047e9f8 | 1 | 0 | 0 | 0 |
| core/internal/bankrec/statement_source_test.go | 85ab475eed859b41 | 4 | 0 | 0 | 0 |
| core/internal/dispatch/d110_pickupcard_integration_test.go | a9312a15e509ecb9 | 17 | 0 | 0 | 0 |
| core/internal/dispatch/dib483_orderref_test.go | 0d4b1f8f31c7595c | 23 | 0 | 298 | 0 |
| core/internal/dispatch/dispatch_test.go | 7cc7f2e710ae6c69 | 14 | 0 | 417 | 0 |
| core/internal/dispatch/driver_completes_run_integration_test.go | 046af2ececcb9ae0 | 20 | 0 | 36 | 0 |
| core/internal/dispatch/driver_completes_run_test.go | a7175d82166655ff | 16 | 0 | 51 | 0 |
| core/internal/dispatch/driver_handback_integration_test.go | 4e8a5756cb134fe0 | 23 | 0 | 24 | 0 |
| core/internal/dispatch/driver_moneyscan_integration_test.go | dbc4eb381b189600 | 14 | 2 | 16 | 0 |
| core/internal/dispatch/driver_payload_test.go | 3a884885cee32202 | 15 | 0 | 0 | 0 |
| core/internal/dispatch/driver_session_integration_test.go | 1cc4ec8f6463ed65 | 12 | 0 | 0 | 0 |
| core/internal/dispatch/driver_session_policy_test.go | 401c7cb4354a86a8 | 10 | 0 | 0 | 0 |
| core/internal/dispatch/freightraise.go | 7241e4c4d24e296b | 16 | 0 | 65 | 0 |
| core/internal/dispatch/freightraise_integration_test.go | e10fab82e07de2b6 | 25 | 0 | 414 | 0 |
| core/internal/dispatch/freightraise_test.go | 275d2ba00005aa40 | 11 | 0 | 286 | 0 |
| core/internal/dispatch/freightraise_wiring_test.go | 5b5106aa1ea33bce | 14 | 0 | 0 | 0 |
| core/internal/dispatch/geo.go | 0bc3fef16b0b5a20 | 4 | 1 | 0 | 0 |
| core/internal/dispatch/handler.go | fa02353e0efc8218 | 4 | 0 | 441 | 0 |
| core/internal/dispatch/handler_loadboard.go | 99e59f373a872279 | 9 | 0 | 0 | 0 |
| core/internal/dispatch/handler_w3.go | 8e98511de4462bde | 3 | 0 | 333 | 0 |
| core/internal/dispatch/integration_test.go | 90feaf225effa382 | 35 | 0 | 857 | 0 |
| core/internal/dispatch/loadboard.go | f523f2676b6ed2fc | 35 | 1 | 472 | 0 |
| core/internal/dispatch/loadboard_test.go | 7688894b23c152d1 | 58 | 1 | 826 | 0 |
| core/internal/dispatch/manifest.go | b8e1e3abb5b7f46b | 11 | 0 | 4 | 0 |
| core/internal/dispatch/model.go | 3cc8ea68494ba5c8 | 39 | 1 | 453 | 0 |
| core/internal/dispatch/model_w3.go | 6c84f06de0d47c93 | 12 | 0 | 301 | 0 |
| core/internal/dispatch/module.go | 8e7f8a306969a60e | 30 | 0 | 4 | 0 |
| core/internal/dispatch/orderref.go | 64d1be069aed8702 | 9 | 0 | 0 | 0 |
| core/internal/dispatch/picksource.go | bdd8fbca8cd83d60 | 29 | 1 | 0 | 0 |
| core/internal/dispatch/policy_history_who_test.go | 3c47a4bcf7dacc12 | 13 | 0 | 0 | 0 |
| core/internal/dispatch/pullinstruction_integration_test.go | 19dd7fbdb79d296f | 19 | 0 | 277 | 0 |
| core/internal/dispatch/pullinstruction_test.go | 902af94b0d131b96 | 23 | 0 | 228 | 1 |
| core/internal/dispatch/repository.go | 5089d728bd247c01 | 16 | 1 | 621 | 0 |
| core/internal/dispatch/repository_freightraise.go | d2e752ce3499a05b | 11 | 0 | 54 | 0 |
| core/internal/dispatch/repository_loadboard.go | bcddc97faceb5e87 | 9 | 0 | 726 | 0 |
| core/internal/dispatch/repository_w3.go | 1edfaf9de3f84f68 | 17 | 1 | 436 | 0 |
| core/internal/dispatch/service.go | 32897f1bb3559114 | 73 | 0 | 993 | 0 |
| core/internal/dispatch/service_freightraise.go | 61d6f7f58910154f | 19 | 0 | 161 | 0 |
| core/internal/dispatch/service_loadboard.go | b8547dd376a5a15e | 94 | 1 | 737 | 0 |
| core/internal/dispatch/service_w3.go | 80508a774b81f622 | 22 | 0 | 420 | 0 |
| core/internal/dispatch/transitions.go | ed03a627b9e4a4ed | 4 | 0 | 0 | 0 |
| core/internal/dispatch/union15_loadvalue_wiring_integration_test.go | 8294dbd29e3e5f06 | 11 | 0 | 0 | 0 |
| core/internal/dispatch/w3_integration_test.go | cfdf426ddaf6ba53 | 12 | 0 | 270 | 0 |
| core/internal/dispatch/w3_test.go | 05ac5f3417aa8c0c | 14 | 0 | 388 | 0 |
| core/internal/dispatch/willcall.go | 638f4f6acdea90e1 | 17 | 0 | 0 | 0 |
| core/internal/invcount/branch_fence_integration_test.go | d41b4cbe467ff616 | 4 | 0 | 0 | 0 |
| core/internal/invcount/dib396_lease_test.go | eb6e4e2a2f43976b | 8 | 0 | 0 | 0 |
| core/internal/invcount/handler.go | 2a9564e4d8d990b0 | 0 | 0 | 0 | 0 |
| core/internal/invcount/integration_test.go | 89bfeaee6c76db76 | 7 | 0 | 68 | 0 |
| core/internal/invcount/model.go | c040c763cff5311c | 6 | 0 | 300 | 0 |
| core/internal/invcount/module.go | 0a73bd266d14655e | 1 | 0 | 147 | 0 |
| core/internal/invcount/repository.go | 2083c8513faee3f9 | 2 | 0 | 246 | 0 |
| core/internal/invcount/service.go | 16a37a583d6413b6 | 6 | 0 | 171 | 0 |
| core/internal/invcount/service_test.go | ddca991f82bae307 | 2 | 0 | 184 | 0 |
| core/internal/landedcost/alloc.go | 60375df72990271a | 3 | 0 | 0 | 0 |
| core/internal/landedcost/handler.go | 59b3268541613fcd | 1 | 0 | 0 | 0 |
| core/internal/landedcost/journal_balance_test.go | 3e5f28302b7c3320 | 9 | 0 | 0 | 0 |
| core/internal/landedcost/landedcost_integration_test.go | e7318e33681675f0 | 22 | 0 | 36 | 0 |
| core/internal/landedcost/landedcost_test.go | f7738defd44e9403 | 11 | 0 | 0 | 0 |
| core/internal/landedcost/model.go | 5a13a28ba3fad641 | 3 | 0 | 22 | 0 |
| core/internal/landedcost/module.go | 6764843494b52516 | 3 | 0 | 147 | 0 |
| core/internal/landedcost/repository.go | 4a936dc008cff549 | 3 | 0 | 23 | 0 |
| core/internal/landedcost/repository_uuid_test.go | 130c9e2707badb03 | 2 | 0 | 0 | 0 |
| core/internal/landedcost/service.go | 1ce90a14ee95fc52 | 25 | 0 | 63 | 0 |
| core/internal/lien/aging.go | 04e337b3591946cb | 6 | 0 | 0 | 0 |
| core/internal/lien/banner_vocabulary_test.go | 403b5c8c99d175a8 | 8 | 0 | 0 | 0 |
| core/internal/lien/errors.go | 0edd2384e2d80556 | 6 | 0 | 51 | 0 |
| core/internal/lien/handler.go | 076a00614ffe3d03 | 1 | 0 | 136 | 0 |
| core/internal/lien/model.go | 045717947e16613b | 2 | 0 | 0 | 0 |
| core/internal/lien/module.go | 8c65a62b7975a102 | 4 | 0 | 157 | 0 |
| core/internal/lien/repository.go | c960996abeea124b | 4 | 0 | 0 | 0 |
| core/internal/lien/service.go | 60cee608d40c7179 | 11 | 0 | 280 | 0 |
| core/internal/lien/service_test.go | e99f6b2e46022b22 | 6 | 0 | 209 | 0 |
| core/internal/lien/store.go | 8c7b478fa12d23b1 | 2 | 0 | 0 | 0 |
| core/internal/lien/template.go | a45fab1a07bb6298 | 4 | 0 | 0 | 0 |
| core/internal/messaging/ack_test.go | 6353d6b8e99692b7 | 5 | 0 | 45 | 0 |
| core/internal/messaging/approvals_rail_test.go | a4855512f513c9a4 | 3 | 0 | 0 | 0 |
| core/internal/messaging/consumer.go | 77287b9d475fc30e | 46 | 0 | 168 | 0 |
| core/internal/messaging/consumer_test.go | 8fcf42fea3c1461b | 5 | 0 | 91 | 0 |
| core/internal/messaging/event_class.go | 9f12999c0e9168bc | 10 | 0 | 78 | 0 |
| core/internal/messaging/events_priority.go | 1c1b9907d6b5b645 | 16 | 0 | 125 | 0 |
| core/internal/messaging/events_priority_repo.go | 4695d12b6862f323 | 8 | 0 | 12 | 0 |
| core/internal/messaging/events_priority_service.go | ca6d5b757187b381 | 9 | 0 | 68 | 0 |
| core/internal/messaging/events_priority_test.go | 17a0c49d6cb7655e | 16 | 0 | 183 | 0 |
| core/internal/messaging/export_test.go | f958638580615233 | 0 | 0 | 127 | 0 |
| core/internal/messaging/handler.go | d888aa33fbbc7372 | 18 | 0 | 108 | 0 |
| core/internal/messaging/handler_test.go | ebaa48e3f20eb960 | 7 | 0 | 41 | 0 |
| core/internal/messaging/integration_test.go | 7bcb6f5d9aa48517 | 18 | 0 | 73 | 0 |
| core/internal/messaging/model.go | 7461a9bb76977928 | 16 | 0 | 176 | 0 |
| core/internal/messaging/notify_prefs_test.go | 09a6219b1f2c3e55 | 1 | 0 | 109 | 0 |
| core/internal/messaging/payment_rules.go | d846a70acd2050f4 | 32 | 0 | 397 | 0 |
| core/internal/messaging/payment_rules_fake_test.go | cc86830122a40885 | 5 | 0 | 181 | 0 |
| core/internal/messaging/payment_rules_handler_test.go | 9640b607842d6ffe | 2 | 0 | 50 | 0 |
| core/internal/messaging/payment_rules_integration_test.go | f7518744282066bc | 7 | 0 | 149 | 0 |
| core/internal/messaging/payment_rules_repo.go | 10c9906dd3d4c822 | 13 | 0 | 71 | 0 |
| core/internal/messaging/payment_rules_test.go | ae8e2ef9554be4d3 | 15 | 0 | 563 | 0 |
| core/internal/messaging/payments_today.go | 6910547eb419394a | 5 | 0 | 86 | 0 |
| core/internal/messaging/payments_today_integration_test.go | 50690ebfc7d63668 | 6 | 0 | 47 | 0 |
| core/internal/messaging/payments_today_repo.go | c0ac39c1f8590297 | 2 | 0 | 103 | 0 |
| core/internal/messaging/payments_today_test.go | cd15df0d0ae65651 | 7 | 0 | 237 | 0 |
| core/internal/messaging/portal_bridge.go | 06d7757faa44d5cb | 12 | 0 | 19 | 0 |
| core/internal/messaging/portal_bridge_test.go | f1fac4677a4eb623 | 3 | 0 | 0 | 0 |
| core/internal/messaging/reconcile.go | 38c96abaa5d21e19 | 5 | 0 | 44 | 0 |
| core/internal/messaging/repassign.go | 5a8fa55bc56a09a1 | 18 | 0 | 60 | 0 |
| core/internal/messaging/repassign_handler.go | 68bd522dd871967e | 5 | 0 | 30 | 0 |
| core/internal/messaging/repassign_repo.go | 505cbd327b9702cf | 12 | 0 | 43 | 0 |
| core/internal/messaging/repassign_service.go | 66909382d3ff782e | 10 | 0 | 18 | 0 |
| core/internal/messaging/repassign_test.go | dabd4adb309674ed | 6 | 0 | 195 | 0 |
| core/internal/messaging/repository.go | dc4be1f1f064040c | 25 | 0 | 446 | 1 |
| core/internal/messaging/repository_test.go | 6bc78515bf9b18c2 | 2 | 0 | 0 | 0 |
| core/internal/messaging/retention.go | 04355e612535ae14 | 3 | 0 | 44 | 0 |
| core/internal/messaging/retention_test.go | 04ec6140c9e65164 | 1 | 0 | 81 | 0 |
| core/internal/messaging/rollup.go | 0b9e2c29e3ff3195 | 19 | 0 | 257 | 0 |
| core/internal/messaging/rollup_fake_test.go | 5c08e2d010ebc679 | 9 | 0 | 259 | 0 |
| core/internal/messaging/rollup_integration_test.go | 2938f8df3a142c46 | 13 | 0 | 76 | 0 |
| core/internal/messaging/rollup_repo.go | 2e889f194997cc82 | 6 | 0 | 75 | 0 |
| core/internal/messaging/rollup_test.go | a9840d467516ea3a | 20 | 0 | 300 | 0 |
| core/internal/messaging/service.go | ec52bba603f0c1eb | 21 | 0 | 218 | 0 |
| core/internal/messaging/service_test.go | 79ce011959e896f4 | 1 | 0 | 255 | 0 |
| core/internal/messaging/sse.go | 88cafea97edae8fd | 6 | 0 | 0 | 0 |
| core/internal/messaging/sse_chain_test.go | 220d20094bbbae3c | 5 | 0 | 0 | 0 |
| core/internal/messaging/sse_test.go | e5987a61f85b81e3 | 0 | 0 | 0 | 0 |
| core/internal/messaging/ticket.go | 6ffc518404099dd6 | 3 | 0 | 34 | 0 |
| core/internal/messaging/ticket_test.go | bf79489346858698 | 0 | 0 | 0 | 0 |
| core/internal/messaging/w5_fake_test.go | 0a8037b8bc744910 | 2 | 0 | 189 | 0 |
| core/internal/messaging/w5_integration_test.go | 54208909bba371ab | 14 | 0 | 147 | 0 |
| core/internal/payments/counter_receipt.go | 18be5bce9c6042c8 | 11 | 0 | 196 | 0 |
| core/internal/payments/dib510_receipt_integration_test.go | bd5ee650bb43cb85 | 4 | 0 | 4 | 0 |
| core/internal/payments/dib510_receipt_test.go | 1c9aa82a8b39e104 | 2 | 0 | 0 | 0 |
| core/internal/payments/gateway.go | 8d7098640af7abb9 | 4 | 0 | 0 | 0 |
| core/internal/payments/intent.go | 20ca98ee52fdaca6 | 6 | 0 | 0 | 0 |
| core/internal/payments/intent_integration_test.go | 8aacbb6553820492 | 17 | 0 | 78 | 0 |
| core/internal/payments/model.go | bc0a38ad9fe1309f | 4 | 0 | 0 | 0 |
| core/internal/payments/payment_lookup.go | 02368331bd89adb3 | 9 | 0 | 0 | 0 |
| core/internal/payments/payment_lookup_test.go | 3ebda9cd925c63f3 | 6 | 0 | 65 | 0 |
| core/internal/payments/repository.go | b8d754e91b15f343 | 6 | 0 | 22 | 0 |
| core/internal/payments/repository_integration_test.go | 1854fa4a7858a03c | 3 | 0 | 0 | 0 |
| core/internal/payments/reversal.go | 0d2fac4bbf9b200f | 14 | 0 | 330 | 0 |
| core/internal/payments/reversal_handler.go | 7fe532bb82c6a895 | 2 | 0 | 0 | 0 |
| core/internal/payments/reversal_integration_test.go | db9f3af746776f10 | 10 | 0 | 342 | 0 |
| core/internal/payments/run_gateway.go | 5abf2f151398c339 | 10 | 0 | 24 | 0 |
| core/internal/payments/selector.go | 1da4031add89ff4b | 10 | 0 | 86 | 0 |
| core/internal/payments/selector_integration_test.go | 17530f956896fc29 | 6 | 0 | 79 | 0 |
| core/internal/payments/selector_test.go | 487a201a574e5e32 | 13 | 0 | 0 | 0 |
| core/internal/payments/service.go | 2543d7e4718b4a7d | 7 | 0 | 56 | 0 |
| core/internal/payments/service_test.go | 9305bbabb4eec325 | 0 | 0 | 35 | 0 |
| core/internal/payments/stub_acceptance_integration_test.go | 58f53865ec576ca2 | 17 | 0 | 425 | 0 |
| core/internal/payments/stub_gateway.go | a8e87b57ee68234a | 2 | 0 | 0 | 0 |
| core/internal/printing/handler.go | fb1bd06ec015c3c9 | 3 | 0 | 0 | 0 |
| core/internal/printing/integration_test.go | edb0962088a202e2 | 3 | 0 | 40 | 0 |
| core/internal/printing/model.go | 8379e29354bf870b | 6 | 0 | 68 | 0 |
| core/internal/printing/queue.go | 7d1b53519a8be527 | 4 | 0 | 0 | 0 |
| core/internal/printing/queue_test.go | 60ed7abe8deeee56 | 3 | 0 | 0 | 0 |
| core/internal/printing/receipt.go | 8e1e7e4e4fd79b45 | 9 | 0 | 44 | 0 |
| core/internal/printing/receipt_repository.go | a063523323c3d38e | 6 | 0 | 0 | 0 |
| core/internal/printing/receipt_test.go | 64f8794f94cf5c30 | 10 | 0 | 0 | 0 |
| core/internal/printing/render.go | ee75d64253b1bba3 | 6 | 0 | 0 | 0 |
| core/internal/printing/render_test.go | 9988880be68b3854 | 5 | 0 | 0 | 0 |
| core/internal/printing/repository.go | 4c3f33e71072e081 | 1 | 0 | 0 | 0 |
| core/internal/purchasing/bills_integration_test.go | 512e90096f77b6dd | 16 | 0 | 0 | 0 |
| core/internal/purchasing/cost_gate.go | 050ea6cc21d21db4 | 15 | 0 | 377 | 0 |
| core/internal/purchasing/cost_gate_test.go | e34aad539a88c1bb | 16 | 0 | 169 | 0 |
| core/internal/purchasing/dib421_currency_integration_test.go | 33db051979809371 | 10 | 0 | 0 | 0 |
| core/internal/purchasing/dib421_currency_test.go | 49204f3a4462d7fd | 9 | 0 | 0 | 0 |
| core/internal/purchasing/dib470_onorder_test.go | ed522dacd448d4e3 | 10 | 0 | 0 | 0 |
| core/internal/purchasing/document.go | 4902f09ffa3b26e3 | 22 | 0 | 501 | 0 |
| core/internal/purchasing/document_test.go | 0ae83da4d69f50d2 | 12 | 0 | 233 | 0 |
| core/internal/purchasing/handler.go | 349a653a9702f339 | 4 | 0 | 0 | 0 |
| core/internal/purchasing/incoming.go | d9fb073b28621797 | 19 | 0 | 0 | 0 |
| core/internal/purchasing/incoming_integration_test.go | 033742200240299f | 19 | 0 | 0 | 0 |
| core/internal/purchasing/incoming_repository.go | ae846717a5733d0c | 7 | 0 | 40 | 0 |
| core/internal/purchasing/incoming_test.go | 83359759e304ca02 | 22 | 0 | 356 | 0 |
| core/internal/purchasing/integration_test.go | d92ddeaead30a017 | 5 | 0 | 163 | 0 |
| core/internal/purchasing/lifecycle.go | 9641a3268bb891d1 | 8 | 0 | 0 | 0 |
| core/internal/purchasing/lifecycle_test.go | 58c6da606d972039 | 5 | 0 | 0 | 0 |
| core/internal/purchasing/match.go | 76d5a526bb51e5a7 | 9 | 0 | 52 | 0 |
| core/internal/purchasing/match_test.go | cfb379f61ca5050b | 18 | 0 | 0 | 0 |
| core/internal/purchasing/model.go | 021c07c6e3e5fd05 | 18 | 0 | 224 | 0 |
| core/internal/purchasing/module.go | bbe84401715be93a | 11 | 1 | 462 | 0 |
| core/internal/purchasing/on_order.go | c482d0e756a39b9f | 5 | 0 | 82 | 0 |
| core/internal/purchasing/receiving_integration_test.go | 7ae4c87635e4b67d | 11 | 0 | 8 | 0 |
| core/internal/purchasing/repository.go | 734c258b66ddcef9 | 10 | 0 | 282 | 0 |
| core/internal/purchasing/service.go | 4e3dbdc0b041d96e | 26 | 0 | 446 | 0 |
| core/internal/purchasing/vendor_order.go | aeb17517a66bf18e | 33 | 0 | 495 | 0 |
| core/internal/purchasing/vendor_order_handler.go | e0bb9f9636859f33 | 10 | 0 | 345 | 0 |
| core/internal/purchasing/vendor_order_integration_test.go | f094b58b8ce8aea8 | 12 | 0 | 222 | 0 |
| core/internal/purchasing/vendor_order_repository.go | b33d6ba2416f1463 | 5 | 0 | 312 | 0 |
| core/internal/purchasing/vendor_order_test.go | a7abff05e29ca201 | 20 | 0 | 266 | 0 |
| core/internal/tasks/advance.go | 6852969a5de5a9db | 5 | 0 | 0 | 0 |
| core/internal/tasks/handler.go | 235d2b7dbeb036cb | 4 | 0 | 0 | 0 |
| core/internal/tasks/model.go | 51f2f98a8fcf8af8 | 8 | 0 | 64 | 0 |
| core/internal/tasks/pickrecord_test.go | c24e9010e99c437b | 6 | 0 | 0 | 0 |
| core/internal/tasks/pullinstruction_integration_test.go | a8e5b6392a1b50cc | 8 | 0 | 0 | 0 |
| core/internal/tasks/repository.go | b19c4c2b74114ef1 | 5 | 0 | 0 | 0 |
| core/internal/tasks/service.go | 4010dee2fd2d6c5d | 16 | 0 | 16 | 0 |
| core/internal/tasks/service_test.go | 042b7722555ce31f | 3 | 0 | 220 | 0 |
| core/internal/tasks/w3_pickfeed_integration_test.go | f35b28d8f4d0e5b3 | 27 | 0 | 249 | 0 |
| core/internal/tasks/weight.go | f8ebdee490091fac | 13 | 0 | 0 | 0 |
| core/internal/tasks/weight_fake_test.go | 31e721b10b6a42b5 | 3 | 0 | 0 | 0 |
| core/internal/tasks/weight_handler.go | e4e67d203438f88b | 2 | 0 | 0 | 0 |
| core/internal/tasks/weight_integration_test.go | 9ee0a6cce7f09855 | 6 | 0 | 0 | 0 |
| core/internal/tasks/weight_repository.go | f9c0200a80e4dbce | 4 | 0 | 0 | 0 |
| core/internal/tasks/weight_test.go | 64b1442d97e2bb13 | 16 | 0 | 377 | 0 |
| core/internal/tasks/yard.go | ed9982ae2530e2c8 | 30 | 0 | 0 | 0 |
| core/internal/tasks/yard_fake_test.go | 3295ad596a99d5a6 | 3 | 0 | 63 | 0 |
| core/internal/tasks/yard_handler.go | ea770c08ee3f9dab | 4 | 0 | 0 | 0 |
| core/internal/tasks/yard_integration_test.go | 004464abc5def567 | 9 | 0 | 0 | 0 |
| core/internal/tasks/yard_repository.go | f2bd764e1b426106 | 9 | 0 | 54 | 0 |
| core/internal/tasks/yard_service.go | 01afaa7b5620c424 | 18 | 0 | 0 | 0 |
| core/internal/tasks/yard_service_test.go | 1da3052b96a0dbd3 | 7 | 0 | 0 | 0 |
| core/internal/tasks/yard_source_test.go | e04da5d76e6dbf8a | 3 | 0 | 0 | 0 |
| core/internal/till/dib488_till_test.go | bcba633b28950484 | 4 | 0 | 0 | 0 |
| core/internal/till/handler.go | 0f435d75ea57ec55 | 5 | 0 | 4 | 0 |
| core/internal/till/history.go | 0da409c0023c5727 | 11 | 0 | 12 | 0 |
| core/internal/till/history_integration_test.go | bbe7e4fdbae74465 | 5 | 0 | 0 | 0 |
| core/internal/till/history_key_test.go | 77a0bf8fce409538 | 1 | 0 | 0 | 0 |
| core/internal/till/holds.go | fa6f1ad537496622 | 5 | 0 | 0 | 0 |
| core/internal/till/model.go | 03430abd637ab572 | 19 | 0 | 233 | 0 |
| core/internal/till/nsf_integration_test.go | cb04000dda3a466e | 7 | 0 | 58 | 0 |
| core/internal/till/nsf_test.go | e9fd66ef7e8201de | 10 | 0 | 0 | 0 |
| core/internal/till/recon_seam.go | 0727eb58731241b4 | 2 | 0 | 0 | 0 |
| core/internal/till/repository.go | b468fe7a67a90b7a | 12 | 0 | 110 | 0 |
| core/internal/till/service.go | fa8dcc726e4ce1a3 | 18 | 0 | 112 | 0 |
| core/internal/till/service_test.go | f4768cad741f9ca2 | 16 | 3 | 412 | 0 |
| core/internal/warranty/errors.go | 81809aaabf792bbe | 7 | 0 | 0 | 0 |
| core/internal/warranty/fakestore_w5b_test.go | d1814e0c6c96e73b | 1 | 0 | 290 | 0 |
| core/internal/warranty/handler.go | 4f426068f493d769 | 2 | 0 | 296 | 0 |
| core/internal/warranty/handler_w5b.go | 2a372c5a466ce40b | 9 | 0 | 240 | 0 |
| core/internal/warranty/intake.go | 248bf0ad57728b81 | 17 | 0 | 176 | 0 |
| core/internal/warranty/intake_test.go | 520c866999b8263d | 7 | 0 | 230 | 0 |
| core/internal/warranty/integration_test.go | f6a1bfb9ad2bf43d | 32 | 0 | 52 | 0 |
| core/internal/warranty/integration_w5b_test.go | b34e030be5cf52ae | 11 | 0 | 130 | 0 |
| core/internal/warranty/model.go | e0857e084f9eaeba | 9 | 0 | 314 | 0 |
| core/internal/warranty/module.go | ab3ffac13265da36 | 2 | 0 | 175 | 0 |
| core/internal/warranty/moneypath.go | 25f052e3be944d87 | 26 | 0 | 120 | 0 |
| core/internal/warranty/moneypath_test.go | 996fd107f5783c69 | 8 | 0 | 159 | 0 |
| core/internal/warranty/phases.go | 8d52ca18e65fd1de | 10 | 0 | 47 | 0 |
| core/internal/warranty/phases_test.go | 2150ed0a21be6a5d | 7 | 0 | 183 | 0 |
| core/internal/warranty/recovery.go | 5e760d8d365a91fb | 12 | 0 | 161 | 0 |
| core/internal/warranty/recovery_test.go | 263d3fb84f8e3529 | 4 | 0 | 115 | 0 |
| core/internal/warranty/reporting.go | 124492f12e93d33a | 5 | 0 | 59 | 0 |
| core/internal/warranty/repository.go | 16d32982604bed68 | 27 | 0 | 337 | 0 |
| core/internal/warranty/repository_w5b.go | 8760af2bfbfecbe7 | 9 | 0 | 219 | 0 |
| core/internal/warranty/scope_test.go | 6f661beb7fe83382 | 8 | 0 | 91 | 0 |
| core/internal/warranty/service.go | 69def9534e746885 | 37 | 0 | 350 | 0 |
| core/internal/warranty/service_test.go | eabcde38d59a31c6 | 17 | 0 | 518 | 0 |
| core/internal/warranty/sla.go | 73ba69aa48d6662d | 9 | 0 | 158 | 0 |
| core/internal/warranty/sla_test.go | 0eacfc4cb5c8e7c8 | 8 | 0 | 85 | 0 |
| core/internal/warranty/status_helper_test.go | ee45d6aea058c750 | 1 | 0 | 0 | 0 |
| core/internal/warranty/store.go | c1281fa9d4fbeee4 | 6 | 0 | 36 | 0 |
| core/internal/bankrec/caller_stamp.go | NEW |  |  |  |  |
| core/internal/bankrec/testdata/rbc_sample.csv | f2764c474942bbec | 0 | 0 | 0 | 0 |
| core/internal/bankrec/testdata/visa_sample.csv | 2e75719f464e03b6 | 0 | 0 | 0 | 0 |
| core/internal/dispatch/caller_stamp.go | NEW |  |  |  |  |
| core/internal/invcount/caller_stamp.go | NEW |  |  |  |  |
| core/internal/landedcost/caller_stamp.go | NEW |  |  |  |  |
| core/internal/lien/caller_stamp.go | NEW |  |  |  |  |
| core/internal/messaging/caller_stamp.go | NEW |  |  |  |  |
| core/internal/payments/caller_stamp.go | NEW |  |  |  |  |
| core/internal/printing/caller_stamp.go | NEW |  |  |  |  |
| core/internal/purchasing/caller_stamp.go | NEW |  |  |  |  |
| core/internal/tasks/caller_stamp.go | NEW |  |  |  |  |
| core/internal/till/caller_stamp.go | NEW |  |  |  |  |
| core/internal/warranty/caller_stamp.go | NEW |  |  |  |  |
| core/internal/identity/staffcontext/approvals_handler.go | NEW |  |  |  |  |
| core/internal/identity/staffcontext/approvals_handler_test.go | NEW |  |  |  |  |
| core/internal/till/integration_helpers_test.go | NEW |  |  |  |  |
| core/cmd/core/wire_operations.go | NEW |  |  |  |  |
| core/cmd/core/wire_test.go | NEW |  |  |  |  |

Batch totals: 2806 em, 14 en, 33999 box, 2 mojibake normalized.
