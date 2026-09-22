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
| core/testkit/staffguards.go | NEW |  |  |  |  |

Batch totals: 3056 em, 12 en, 1326 box, 4 mojibake normalized.
