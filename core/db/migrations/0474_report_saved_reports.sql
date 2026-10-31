-- 0474: saved reports for the report writer (wave 3, plan row RW-2).
-- Owner: internal/reporting. Additive, non-breaking, self-contained, forward only.
--
-- A saved report is a stored DEFINITION over one of the five curated data sets (RW-1): which set, which
-- of its catalog columns, which filters, which grouping and which sort. It stores no rows and no
-- results. Running it re-reads the set through the set's own module seam with the caller's own branch
-- reach and role gate at run time, so a saved report can never widen what its runner may see.
--
-- It is NOT a way to run a free query. Nothing in this table is ever text in SQL: the columns, filters
-- and grouping are JSON documents validated against the data set's catalog by the service on every save
-- and every run, and the sets are read through typed Go reads, not through this table.
--
-- The report is private to its owner. Every read of it is keyed by the owner, so another user's id
-- answers not found, and the unique index makes a name one report per owner.
CREATE TABLE IF NOT EXISTS report_saved_reports (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    dataset       TEXT NOT NULL,
    columns       JSONB NOT NULL,
    filters       JSONB NOT NULL DEFAULT '[]'::jsonb,
    group_by      JSONB NOT NULL DEFAULT '[]'::jsonb,
    sort          JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- A fixed house day for the sets that answer as of a day (ar, ap); empty means the day it runs.
    as_of         TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT report_saved_reports_dataset CHECK (dataset IN ('ar', 'ap', 'gl', 'sales', 'stock')),
    CONSTRAINT report_saved_reports_name CHECK (char_length(btrim(name)) BETWEEN 1 AND 120),
    CONSTRAINT report_saved_reports_as_of CHECK (as_of = '' OR as_of ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$'),
    CONSTRAINT report_saved_reports_shapes CHECK (
        jsonb_typeof(columns) = 'array' AND jsonb_typeof(filters) = 'array'
        AND jsonb_typeof(group_by) = 'array' AND jsonb_typeof(sort) = 'array')
);

-- One name per owner, compared without case: saving the same name twice is refused in words, never a
-- second row the owner cannot tell from the first.
CREATE UNIQUE INDEX IF NOT EXISTS uq_report_saved_reports_owner_name
    ON report_saved_reports (owner_user_id, lower(btrim(name)));
