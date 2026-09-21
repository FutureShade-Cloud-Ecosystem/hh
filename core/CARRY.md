# Carry ledger: normalization against the ERP pin

Carried Go source is ours: house punctuation applies. D32's
verbatim rule is migrations-only by recorded range. This ledger
pays the HH-3.7 diff debt: per carried file, the sha256 of the
pinned ERP blob (first 16 hex) and the punctuation classes
normalized (em, en, box-drawing, mojibake quotes). Files marked
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

Every later carry batch appends its rows before review.
