# 0452 on a box that closed years before the marker existed

Migration 0452 creates `gl_year_end_closes`, the year end close's own marker, and backfills it
from the evidence the box already carries. One row per closed fiscal year, and `fiscal_year` is
the primary key, so a year is closed once and only once whatever the shape of its books.

The marker is the once only signal for the year end close. Before it, the signal was the shape of
the books: a year that holds a closing journal was answered by that journal, and every other year
was answered by the seal, every period of the year already CLOSED. The seal is not a record of the
act, so a year whose twelve months were sealed one by one, with no profit and loss to sweep,
recorded nothing at all on its year end press and could not be told from a year that was never
closed. The marker is the record of the act: it names who closed the year, when, and why.

## What 0452 backfills, and from what

A year gets a row when BOTH hold:

1. every one of the year's fiscal periods is CLOSED, and the year has at least one period. A year
   with no periods was never closed, and a year with one OPEN period was never closed either. The
   count comes before the CLOSED test, so "every period is CLOSED" is never vacuously true on zero
   periods.
2. the year carries EITHER a CLOSING journal OR a GL_YEAR_END_CLOSE audit row. This is the
   evidence that a year end close actually happened. The audit row speaks first for the instant,
   the actor and the reason. The closing journal is the accounting artefact, so it supplies its own
   instant and its own creator. The journal carries no reason, so no reason is taken from it.

The row is built from that evidence and from nothing else:

| column | where it comes from |
| --- | --- |
| `closed_at` | the GL_YEAR_END_CLOSE audit row's own instant, else the CLOSING journal's own instant |
| `closed_by` | the audit row's own actor, else the closing journal's own creator |
| `reason` | the GL_YEAR_END_CLOSE audit row's own reason, else NULL |

## No actor is invented

`closed_by` is NULL only where the evidence carries none, and only for a backfilled row. A close
that runs after this migration always writes the caller. An audit row's actor id carries no
foreign key by design, so an actor who has since been deleted is dropped to NULL rather than
failing the whole migration on a foreign key. The column is the same shape as
`fiscal_periods.closed_by`: a uuid that names a user, and a deleted user drops it to NULL.

The same is true of the reason. The closing journal carries no reason, so a backfilled row that
rests on a journal alone carries reason NULL. A NULL reason is a fact about the evidence, not a
gap in the record.

## The case an operator will meet: a no profit and loss year closed before 655

A year with no profit and loss to sweep closes nothing: it posts no closing journal, and before
the marker existed it recorded no audit row either. Its twelve months were sealed one by one, and
the seal was all it left behind.

So a year closed that way BEFORE 655 carries no evidence, and 0452 gives it no marker. That is by
rule, and the rule is the honest one: twelve months sealed one by one is not a year end close,
and the marker is what tells the two apart from here on. The backfill cannot see what it cannot
prove.

What happens next on such a box is this. The next press on that year finds no marker and no
closing journal, so it is not answered as already closed. It runs the close as a NEW close: it
seals what is still OPEN (nothing, on a year sealed month by month), records its
GL_YEAR_END_CLOSE audit row, and writes its marker with TODAY'S Controller and TODAY'S reason.
The box then holds a year end close for a year that was closed long ago, and the marker names the
press that took it.

That is honest and it is what the box actually records. The act on the box is the press; the
marker names it. No actor is invented and no instant is backdated.

## What to check on the box

To see the years that will be closed again by the next press, run this before the box takes the
new code:

```sql
SELECT p.year,
       count(*)                                              AS periods,
       count(*) FILTER (WHERE p.status = 'CLOSED')          AS closed
  FROM fiscal_periods p
 GROUP BY p.year
HAVING count(*) = count(*) FILTER (WHERE p.status = 'CLOSED')
   AND NOT EXISTS (SELECT 1 FROM gl_year_end_closes m WHERE m.fiscal_year = p.year)
   AND NOT EXISTS (SELECT 1 FROM gl_journals j WHERE j.source = 'CLOSING' AND j.fiscal_year = p.year)
   AND NOT EXISTS (SELECT 1 FROM audit_log a
                    WHERE a.action = 'GL_YEAR_END_CLOSE'
                      AND (a.changes->>'fiscal_year') ~ '^[0-9]+$'
                      AND (a.changes->>'fiscal_year')::int = p.year)
 ORDER BY p.year;
```

Every year it names is a year whose twelve months are sealed and whose year end close left no
evidence, so every year it names is a year the next press will close again and record.

To see the markers the box carries, with the years that hold no marker beside them:

```sql
SELECT p.year,
       (m.fiscal_year IS NOT NULL)                          AS has_marker,
       COALESCE(m.closed_by::text, '')                       AS closed_by,
       m.reason
  FROM (SELECT DISTINCT year FROM fiscal_periods) p
  LEFT JOIN gl_year_end_closes m ON m.fiscal_year = p.year
 ORDER BY p.year;
```

A box that must show the historical close of one of those years, with the actor and the instant it
really happened, needs the principal's ruling on that box from its own facts. The migration does
not backdate a marker, and no hand step is written here in advance.
