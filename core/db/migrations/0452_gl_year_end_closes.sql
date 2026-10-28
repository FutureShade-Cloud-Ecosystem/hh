-- 0452: FW1-09g: the year end close's OWN marker, gl_year_end_closes.
--
-- One row per closed fiscal year. fiscal_year is the PRIMARY KEY, so a year is closed once
-- and only once, whatever the shape of its books: a year with a P&L to sweep, a year with no
-- P&L at all, a year whose P&L nets to zero on every account, and a year whose twelve
-- months were sealed one by one.
--
-- WHY THE MARKER. Until this table the once only signal for the year end close was the
-- SHAPE of the books: a year that holds a closing journal is answered by that journal, and
-- every other year is answered by the SEAL (every period of the year already CLOSED). The
-- seal is not a record of the act. A year whose months were sealed one by one, with no P&L
-- to sweep, therefore records NOTHING on its year end press, and cannot be told from a
-- year that was never closed at the year end. The marker is the record of the act: it names
-- who closed the year, when, and why.
--
-- THE BACKFILL RULE, in full. A year gets a row when BOTH hold:
--
--   1. every one of the year's fiscal periods is CLOSED (and the year has at least one
--      period: a year with no periods was never closed), AND
--   2. the year carries EITHER a CLOSING journal OR a GL_YEAR_END_CLOSE audit row.
--
-- THE ROW IS BUILT FROM THAT EVIDENCE AND FROM NOTHING ELSE:
--
--   closed_at  the evidence's own instant: the GL_YEAR_END_CLOSE audit row's created_at,
--              else the CLOSING journal's created_at,
--   closed_by  the evidence's own actor: the audit row's actor_id, else the closing
--              journal's created_by,
--   reason     the GL_YEAR_END_CLOSE audit row's own reason (its changes->>'reason'), else
--              NULL.
--
-- NO ACTOR IS INVENTED. closed_by is NULL only where the evidence carries none, and only
-- for a backfilled row: a close that runs after this migration always writes the caller. An
-- audit row's actor_id carries no foreign key by design (audit_log, 0013), so an actor who
-- has since been deleted is dropped to NULL here rather than failing the whole migration
-- on a foreign key. The column is the same shape as fiscal_periods.closed_by: a uuid that
-- names a user, and a deleted user drops it to NULL.
--
-- A year whose periods are all CLOSED but which carries NO evidence is NOT backfilled:
-- twelve months sealed one by one is not a year end close, and the marker is what tells
-- the two apart from here on. A year with one OPEN period is not backfilled either: it was
-- never closed.

CREATE TABLE IF NOT EXISTS gl_year_end_closes (
    fiscal_year INTEGER PRIMARY KEY,
    closed_at   TIMESTAMPTZ NOT NULL,
    closed_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    reason      TEXT
);

INSERT INTO gl_year_end_closes (fiscal_year, closed_at, closed_by, reason)
SELECT e.year,
       e.closed_at,
       e.closed_by,
       e.reason
  FROM (
      SELECT y.year,
             COALESCE(a.created_at, j.created_at) AS closed_at,
             CASE WHEN COALESCE(a.actor_id, j.created_by) IN (SELECT u.id FROM users u)
                  THEN COALESCE(a.actor_id, j.created_by)
                  ELSE NULL END AS closed_by,
             a.reason AS reason
        FROM (
            -- The years whose every period is CLOSED. The count comes before the CLOSED
            -- test, so "every period is CLOSED" is never vacuously true, and a year with no
            -- periods at all is not here: GROUP BY only ever names a year that has periods.
            SELECT p.year
              FROM fiscal_periods p
             GROUP BY p.year
            HAVING count(*) = count(*) FILTER (WHERE p.status = 'CLOSED')
        ) y
        LEFT JOIN LATERAL (
            -- The GL_YEAR_END_CLOSE row is the record of the act, so it speaks first: its
            -- own instant, its own actor, and its own reason.
            SELECT al.created_at,
                   al.actor_id,
                   al.changes->>'reason' AS reason
              FROM audit_log al
             WHERE al.action = 'GL_YEAR_END_CLOSE'
               AND al.changes ? 'fiscal_year'
               AND (al.changes->>'fiscal_year') ~ '^[0-9]+$'
               AND (al.changes->>'fiscal_year')::int = y.year
             ORDER BY al.created_at
             LIMIT 1
        ) a ON true
        LEFT JOIN LATERAL (
            -- The closing journal is the accounting artefact: its own instant and its own
            -- creator. It carries no reason, so none is taken from it.
            SELECT gj.created_at,
                   gj.created_by
              FROM gl_journals gj
             WHERE gj.source = 'CLOSING'
               AND gj.fiscal_year = y.year
             ORDER BY gj.created_at
             LIMIT 1
        ) j ON true
       WHERE a.created_at IS NOT NULL OR j.created_at IS NOT NULL
  ) e
ON CONFLICT (fiscal_year) DO NOTHING;
