# ADR-0018: citext on the extension allowlist, and the PostgreSQL major-version move

Status: Proposed

Drafted in HH-2 lane HH-2.4. The decision was taken in the HH-1 PR review and recorded in `analysis/gap-analysis.md` section 1; this draft is the ADR that review item asked for (item 5 in `analysis/REVIEW-RESPONSE.md`). It records the decision; it does not reopen it.

## Decision

`citext` is added to the App Shape's extension allowlist. Context section 4.1 lists the allowed extensions as pgvector, pg_trgm and PostGIS; the allowlist becomes pgvector, pg_trgm, PostGIS and citext. `citext` is a standard contrib extension that 51 baseline migrations depend on.

The org database moves to the shape's pinned PostgreSQL major version (context section 4.1 pins PostgreSQL 18; the ERP at present runs PostgreSQL 16, `docker-compose.yml` per `analysis/gap-analysis.md` section 1) rather than rewriting the baseline.

`manifest.yaml` therefore declares `database.extensions: [pg_trgm, citext]` (HH-2.2), and HH-3.1 carries `citext` as is.

## Evidence

- `backend/migrations/0001_extensions.sql:5-6` enables `pgcrypto` and `citext`; `CITEXT` is used across 51 migration files (`analysis/gap-analysis.md` section 1).
- Natural keys are `CITEXT` by binding convention: sku, account number, GL code, email (`analysis/domain-map.md` section 3.0, citing `docs/design/DATA_MODEL.md` section 0), plus the human-facing document numbers `quote_no`, `order_no`, `invoice_no`, `payment_no`, `po_no`, `manifest_no`.

## Options rejected, and what each loses

### Rewrite the baseline off CITEXT

Rejected. The rewrite would replace 51 migrations' `CITEXT` columns with `TEXT` plus functional unique indexes on `lower(...)`. HH-3.1's scope is to carry `0001` through the last file **unedited** as the baseline of `core/db/migrations`, with the acceptance that the migrations apply on top of a restored HardscapeOS dump and that "HardscapeOS must keep running against this schema unchanged" (`analysis/sessions/HH-3-unified-core.md`, HH-3.1). A rewrite invalidates both the restored-dump acceptance and the running ERP's schema for no behavioural gain, and it would have to be re-done for every migration the in-flight delivery still lands (the tracking lane ports new migrations unedited).

### Keep the allowlist unchanged and treat hh as an exception

Rejected. The allowlist exists so `fb check` can verify conformance (context section 4: "Conformance is checked by `fb check` in CI"). An app whose baseline needs an unlisted extension would make the manifest and the schema disagree from the first commit, and there is no exception mechanism to hang that on. `citext` is a stock contrib extension, so widening the allowlist once is cheaper than inventing per-app exceptions, and Gable inherits the same baseline shape (the G-2 session reuses this ADR rather than re-deciding it).

### Pin the platform's data runtime down to PostgreSQL 16 for this app

Rejected. Context section 4.1 pins the current stable line in the template, and one app's baseline is not a reason to pin every org database down a major version. The baseline is forward-only numbered SQL; the major-version move is a provisioning and verification task (migrations apply from zero and on a restored dump on the pinned version, HH-3.1's acceptance), not a schema change.

## Note on pgcrypto

`0001` also enables `pgcrypto`. On the pinned PostgreSQL major version `gen_random_uuid()` is built in, so the baseline's known use of the extension is vestigial. HH-3.1 confirms no other `pgcrypto` function is used before the allowlist is treated as complete, and reports the finding rather than silently extending the allowlist.

## Sessions gated

- HH-2.2: the manifest's `database.extensions` declares `citext` alongside `pg_trgm` per this ADR.
- HH-3.1: the baseline carries `citext` as is, and its acceptance runs on the pinned major version.
- HH-5: the platform org database (Managed Postgres) must offer `citext` before convergence.
- G-2: this ADR "is reused, not re-decided" (`analysis/transformation-path.md` section 5).
