# core/db/PORTED.md

The ledger binding the carried baseline to its ERP source and recording every later movement between the ERP and this tree.

## Baseline and carry rule

Source: `hardscapeos_dibbits`, branch `master`, commit `6eaea3ec`, the head of `master` and of `origin/master` (D33): 196 migrations, `0001` through `0212`. Carry rule (D32): DDL, filenames and version numbers verbatim; fixture text scrubbed of third-party identifying values before the first commit. Tracking target: `master` only; a change existing only on `Demo` is not a port decision until it reaches `master` (D33).

## Scrub record: files changed against the ERP blob

One row per baseline file whose committed text differs from its ERP blob at `6eaea3ec`. The hash is the ERP original's, so the tracking lane diffs against the recorded original, not the scrubbed text (D32). Reasons are reason classes, never values. The pre-carry classification names `0029`, `0046`, `0049`, `0055` and `0057` as the minimum set; the attribution sweep across the whole carried set may add files. Rows are filled by the carry pass.

D45 finding (the PR 14 merge condition): the two rows above understate what
the carried text retains. The D32 pass replaced the personal identifier's
first token only; a surname remains beside the patterned placeholder in both
seed values (reason class name), beside a dash character and workstream text
the scrub classes never covered. Recorded by reason class only, never the
value. The baseline files stay byte-for-byte as carried: frozen migrations
are never edited and history is never rewritten, and the text remains in git
history as a known exposure in a private repository. The HH-3.7 port (core
0220) retires the value from live data by rewriting both rows to the clean
replacement wording, matching them with LIKE predicates that carry no name
and no dash. Decision: `docs/analysis/DECISIONS.md` D45.

| File | ERP blob hash | Reason class |
| :--- | :--- | :--- |
| `0007_gl_block.sql` | `327fdea484defa52fb6493e19d0b8bfe402e4e40` | first_name |
| `0015_rbac_roles.sql` | `f67fcbf82c2f3ec0409d9d5dcae33285750b549c` | first_name |
| `0017_dibbits_branch_addresses.sql` | `99c8adafb772c9c2b8965c2befd088fc74c48869` | email |
| `0024_pricing_seed.sql` | `5ac725cc06867350c7b4fe0c0610525c213b7f90` | first_name |
| `0026_quantity_break_adjustments.sql` | `49f14f96e09b69c71ce8a1f0c3d68f6f187fd4de` | first_name |
| `0029_pos_counter_sales.sql` | `ca5d6d0ee5f96b2a7029bab28c43ea29bac7cc34` | first_name, name |
| `0032_gl_module.sql` | `19bbecb86e2255b7b085703568dbc7a9bca248ab` | first_name, name |
| `0033_bank_recon.sql` | `8f71a6408f662574f465de25d5848db7692ed64a` | first_name |
| `0035_nsf_credit_hold.sql` | `cbcde97c7fe2c0ab23609a63cacc37fe04598eef` | first_name, name |
| `0038_lien_holdback_compliance.sql` | `55d2429488754857b346b039b667bf0f9e5f9469` | first_name |
| `0039_price_change_log.sql` | `f4e234cac49a76dfe7c734a258c17606367365b0` | first_name |
| `0043_super_admin_platform_role.sql` | `e5baee15ec813683aa3a68dadc9abab75a4cfc1b` | first_name |
| `0046_uat_portal_test_fixtures.sql` | `38814af635f6bfe8358d95718e81b837c7da5a9f` | attribution, first_name, name |
| `0049_seed_demo_project.sql` | `b0af23ba646e19415161aff0a972a0b7847c6e85` | attribution, first_name, street_address |
| `0052_messaging_demo_seed.sql` | `d3421102daaf527d3fefa3e37f9f455f51c945c5` | attribution, first_name |
| `0054_warranty_registry_seed.sql` | `e40128c99547caccefccdd879934b7fe7f943352` | first_name |
| `0055_portal_demo_fixtures.sql` | `034097338117224a27d4d46d0630556df0f1279b` | attribution, first_name, name, street_address |
| `0057_warranty_demo_seed.sql` | `5d8bb179c4101bb4765c91a36cb95841f5fc1c58` | attribution, email, first_name, name, street_address |
| `0059_order_history_demo.sql` | `f0654c5bc25494e8645a44ec907736a27f04b456` | first_name, name |
| `0060_messaging_demo_topic_dm.sql` | `1cfc9b43245df3ba722f278fa9024532519c8c8b` | first_name |
| `0061_homework_demo_users.sql` | `e75f266c4b39c148ad6f2d5aad4b01d9de1fe1f6` | attribution, email, first_name |
| `0062_homework_roles_pricing_fix.sql` | `0ac56a335d2e82d45bdfa2d882ae5d14c9b3cbf6` | email, first_name |
| `0066_ryan_rood_email.sql` | `fa076932bdee5f33b2bd633c60a061ea82209c9d` | email, first_name |
| `0074_staging_uat_admin_access.sql` | `84960241dc8bad95bfce0d400369f9eda74ed7a8` | email, first_name |
| `0077_uat_tier3_inside_sales.sql` | `c0719db4d86a4583b1dc7f116cbc3d0d7e3f369a` | email, first_name |
| `0085_quote_staff_surface.sql` | `9fd9730b02b06968b54664c65caa6cf963fd51f5` | first_name |
| `0095_yard_pick_stage.sql` | `11d774c5a6d1c0eb44900db864072c899357e871` | first_name |
| `0108_scan_trail_weight_advance.sql` | `0ba55562dbdca98b7b312c3415a5ddebbd4178e3` | first_name |
| `0113_pricing_uom_conversions.sql` | `f54928faa20168c75579828b6bd65506ebe04646` | first_name |
| `0130_warranty_recovery_board.sql` | `d96e9da199ddef3d18ef930f1b931d878806352a` | first_name |
| `0139_msg_conversations_demo.sql` | `6ceede5abceef27dffabc760c959e85b50fcbf4d` | attribution, first_name |
| `0143_reporting_templates.sql` | `9135267c03effe5f660b61515850f0ed88d501f7` | first_name |
| `0146_billing_invoice_documents.sql` | `0b4abd1454382d7ad0d5439c1ebe66b4aceaff55` | first_name |
| `0148_statement_run_notice_counts.sql` | `e7ae07df699063f9fda5a037aa3cdb5f40c53907` | first_name |
| `0151_quick_action_visibility.sql` | `695ea8ceecc6f3e6e216435ae0cc317c645e2332` | first_name |
| `0152_msg_event_classes_rollup.sql` | `b0b70417ca6122bff2c5977a92da3b2bf4d73598` | first_name |
| `0153_assist_commands_config.sql` | `017774f9fd7b8d695fc1b0acac75df2d3548bca0` | first_name |
| `0154_assist_token_ledger.sql` | `2e289eaefcedc3d0689a74a31c914e2ca8c9cceb` | first_name |
| `0158_transfer_eta_transit_days.sql` | `e029746d0394e3b276f2753cda87a72e43474196` | first_name |
| `0159_quote_scope_rules.sql` | `b6c1c0a8252b305d9005a0c192042cb2151c6fe8` | first_name |
| `0164_dealer_hotkeys.sql` | `e6bfc429d60a5536f3693678f306229adf9a3d73` | first_name |
| `0176_ar_promises_to_pay.sql` | `5459d29bc8c50dd068957eb49f50c694782a4fc6` | first_name |
| `0179_ar_invoice_register_demo.sql` | `b7fe6f4e7c3c1b5a8782dda7f0d4409673423249` | attribution, first_name |
| `0181_pos_balance_visibility.sql` | `2f678989a9028098ea77138fed6f38a6eac7ef91` | attribution, first_name |
| `0184_dispatch_loadboard.sql` | `7f9e618df33798b3541f2540d679632862d8ff71` | first_name |
| `0185_dispatch_commit.sql` | `229a2ab26516b160c918993839f20ad7668dbd32` | attribution, first_name |
| `0186_pos_hotlist_bulk_board.sql` | `80496ab0b6371fe3f14aac8b88c789d83bec9822` | attribution, first_name |
| `0187_task_pick_lines.sql` | `4cede90778b0b6e0c8a6f45681430ba5c7059598` | first_name |
| `0192_tour_substrate.sql` | `05e0b50580afaf150a3dfb35af9fe8586fc1d4ea` | first_name |
| `0193_tour_progress.sql` | `258086e04db59a92825c83bc6b1aaeafc5aeb13a` | first_name |
| `0195_dispatch_freight_raise.sql` | `509f2c545f9f89df935485cf805f4bf8777a1bcd` | attribution, first_name |
| `0197_purchasing_vendor_order.sql` | `2e499a45a7ec9d87b16387cdb2f17ec2193ae422` | first_name |
| `0200_tour_demo_personas.sql` | `1a61e38869dfbf6a307678c22e291d98d2d60dfe` | attribution, email, first_name |
| `0201_dashboard_widgets.sql` | `22f5151589d8ed11cfd7e9dbb4ceced1422571b4` | first_name |
| `0205_widget_windows_tell_the_truth.sql` | `57b9f6232fbb83a87154dc6d0383112757deb61e` | first_name |
| `0206_a_demo_row_names_a_demo_person.sql` | `23c1f686b608f6c0bb8359a440d21d39b6342d20` | attribution, first_name |
| `0208_widget_gallery.sql` | `0295e7039ddd18357d575f951578b30c54b1d799` | first_name |
| `0210_four_flows_that_finish.sql` | `22bd54ee6d8e20d34c71bcb3b9dd20b305f9051d` | first_name |
| `0211_the_receivables_flow_gets_a_chapter_of_its_own.sql` | `2832dee5590ebf66c8a24b89e69e6e91188aa350` | first_name |
| `0212_reports_is_a_surface_now_so_the_strips_can_point_at_it.sql` | `dbc6926ec309cba7a924de9f29cdbc6f47582305` | first_name |

## Recorded changes to the carried schema

**D30, idempotency keys.** Every synced table, carried and new, gains a client-generated idempotency key that the server accepts. On carried ERP tables the column is nullable at the database level and its presence is enforced at the API boundary, never by a not-null constraint, so neither the ERP's own suite nor any writer in the in-flight delivery can be broken by it. Applied by additive migration `0213`; the carried files stay at their ERP DDL.

## Port ledger (HH-3.7)

One entry per ERP migration landing on `master` above the baseline, each a human decision of ported or deferred with its reason recorded at the time. A ported entry lands in the pull request carrying it, and that pull request is the only one permitted to move `SCHEMA-FROZEN`.

| ERP migration | ERP commit | Decision | Reason | Ported as |
| :------------ | :--------- | :------- | :----- | :-------- |
| 0213 `0213_a_saved_sort_is_curation_too.sql` | `05809a25` | Ported | Additive CHECK widening by one value on two carried tables; no row written, read or moved; existing rows re-validate clean. | 0216 `0216_a_saved_sort_is_curation_too.sql` |
| 0214 `0214_purchasing_delivery_method.sql` | `05809a25` | Ported | Adds `purchasing_pos.delivery_method`; ported paired with the 0216 port so core never carries the uncorrected default. | 0217 `0217_purchasing_delivery_method.sql` |
| 0215 `0215_a_widget_title_that_was_never_chosen_stops_speaking_for_the_catalog.sql` | `05809a25` | Ported | Guarded byte-identity rewrites of seeded widget titles with `updated_by IS NULL` guards; idempotent. Count-verified on core's from-zero apply: matched 6 rows in `dashboard_profile_widgets`, 5 in `dashboard_gallery_entries`, 0 in `dashboard_user_widgets`; a second run matches 0. | 0218 `0218_a_widget_title_that_was_never_chosen_stops_speaking_for_the_catalog.sql` |
| 0216 `0216_purchasing_delivery_method_can_be_unrecorded.sql` | `05809a25` | Ported | Second half of the 0214 pair: third CHECK value, corrected default, guarded backfill. Ruling condition 1 met: the backfill cut's bookkeeping lookup pattern was adapted from the ERP's 0214 file number to core's 0217 file number (the 0214 port), the one adaptation in an otherwise byte-identical body; core's runner records the same filename-stem versions in the same `schema_migrations` shape, so the guarded lookup resolves identically on core's from-zero path. | 0219 `0219_purchasing_delivery_method_can_be_unrecorded.sql` |
| 0217 `0217_a_finance_note_a_client_reads_names_nobody.sql` | `05809a25` | Ported | Adapted port. Rewrites `gl_policy.notes` and `nsf_policy.notes` to the ERP's clean replacement sentences. Ruling condition 2 met, and the PR 14 merge condition (D45) with it: the WHERE predicates are LIKE patterns anchored on the patterned placeholder token, the workstream code and the surrounding program words, with wildcards where the surname and the dash sit, so the same single seeded row per table matches (proven by a direct LIKE check and on the from-zero apply) and neither the surname nor the dash enters this file; the ERP's original sentence never entered core either, in code or in commit text. Same safety properties as the ERP's 0217: no `updated_at` bump, no `schema_migrations` reference, idempotent (from-zero apply rewrote 1 row per table; a second run matches 0). | 0220 `0220_a_finance_note_a_client_reads_names_nobody.sql` |
| 0218 `0218_the_setup_entries_behind_no_customer_document_come_back_out.sql` | `05809a25` | Ported | Verbatim. Balanced reversing journals with fixed UUIDs and idempotency keys for the four demonstration journals 0056 posted; triple-guarded no-op where 0056 is absent, and core carried 0056 verbatim. | 0221 `0221_the_setup_entries_behind_no_customer_document_come_back_out.sql` |
| 0219 `0219_an_invoice_with_no_journal_behind_it_asks_for_one.sql` | `05809a25` | Ported | Verbatim. Queues outbox posting events for finalized invoices and received payments with no GL journal, and requeues DEAD events whose documents still lack one. Ruling condition 3 met: core's `events_outbox` matches the ERP's on the `status`, `attempts` and `last_error` columns, verified by comparing the carried baseline DDL at `core/db/migrations/0013_audit_outbox.sql:22` against the ERP's `backend/migrations/0013_audit_outbox.sql:22` at `05809a25` (identical, no later ALTER in either tree). On core's from-zero apply it queued 2 PENDING `ar.invoice.finalized` rows, the two demo invoices 0055 carries. | 0222 `0222_an_invoice_with_no_journal_behind_it_asks_for_one.sql` |
| 0220 `0220_pos_counter_layout.sql` | `05809a25` | Ported | Pure DDL: `counter_layout` on `pos_policy` and `pos_user_workspace`, both NOT NULL with a default that preserves what existing users already see; both tables are carried. | 0223 `0223_pos_counter_layout.sql` |

The freeze marker names 441bfbe, the commit holding 0220's final D45
content, not the port commit 6b92d21: the merge-condition fix rewrote
0220's predicates after the port commit landed, and a marker naming the
port commit would leave every later pull request reporting 0220 as an
uncovered change forever. The diff from the named commit to main under
core/db/migrations/ is empty, so any later migration change needs its
own ledger coverage again. Moved in the housekeeping pull request that
also restates 0220's ground in check-shape.

First entry, per D33: refreshing `core/api/erp-routes.txt` from `master` (the snapshot vendored under D22 was taken from a `Demo` commit ahead of `master`); the route difference the refresh reports measures how far the snapshot was off. Done in this run: the refresh renamed the provenance header to `05809a25` and added exactly one route line (`PUT /api/v1/purchasing/pos/{id}/delivery-method`).

## Drift measurement (the erp-routes hold's precondition)

Measured at origin/master 05809a25 against the pinned 6eaea3ec:
git diff --name-status 6eaea3ec origin/master -- backend/migrations/
returns EIGHT added files and nothing else (no modification, no
deletion; the baseline at 0212 and below is untouched, D32 holds):

- 0213_a_saved_sort_is_curation_too.sql
- 0214_purchasing_delivery_method.sql
- 0215_a_widget_title_that_was_never_chosen_stops_speaking_for_the_catalog.sql
- 0216_purchasing_delivery_method_can_be_unrecorded.sql
- 0217_a_finance_note_a_client_reads_names_nobody.sql
- 0218_the_setup_entries_behind_no_customer_document_come_back_out.sql
- 0219_an_invoice_with_no_journal_behind_it_asks_for_one.sql
- 0220_pos_counter_layout.sql

These eight were HH-3.7's port-or-defer decisions under D39, and all eight
are now ported: each landed at its core number in the ledger above (the
ERP's 0213 to 0215 collide with core's frozen additive set and are never
carried by those numbers), through the tracking lane's door, in the pull
request that moves `SCHEMA-FROZEN`. The hold on the erp-routes refresh is
released (its release condition is met: Phase C merged, the eight
decisions ruled, and the refresh has run), and the refreshed vendored
list lands in the same pull request.
