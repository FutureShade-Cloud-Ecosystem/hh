# 0446 on a box that still carries branchless journal lines

Migration 0446 makes `gl_journal_lines.branch_id` NOT NULL. It backfills nothing.

On a box that still holds a journal line with no branch, the migration's pre check refuses in
words, names every journal that carries one, and the migrate step fails. FB Deploy keeps the old
container: a deploy whose new container fails its health check leaves the previous deploy serving,
and that has been seen on a box whose boot failed. The database is unchanged, and the box stays on
the version it was already running. No line is stamped by the migration and no line is guessed at.

A box already past 0446 (on a later migration) still takes it: the migration runner checks each
version on its own, by whether that version has been applied, not by whether it is newer than the
newest one applied, so 0446 applies late on such a box.

This runbook is what the operator does next.

## The check to run before any box takes 0446

Run this against the box's database first:

```sql
SELECT count(*) FROM gl_journal_lines WHERE branch_id IS NULL;
```

`0` means the box takes 0446 with no further work. Any other number means the box stops here and
the operator works through the sections below.

To see the journals that carry the lines, run:

```sql
SELECT j.id::text              AS journal_id,
       j.source                AS source,
       j.source_ref_id::text   AS source_ref_id,
       j.posted_at AT TIME ZONE 'UTC' AS posted_at,
       count(*)                AS unstamped_lines
  FROM gl_journal_lines l
  JOIN gl_journals j ON j.id = l.journal_id
 WHERE l.branch_id IS NULL
 GROUP BY j.id, j.source, j.source_ref_id, j.posted_at
 ORDER BY j.posted_at, j.id::text;
```

That query names the same journals and counts the migration's refusal names, so its answer and the
refusal always agree.

## What the refusal looks like

The migration's words, on a box whose year end close carried two unstamped lines:

```
ERROR:  gl: 2 journal line(s) across 1 journal(s) carry no branch:
  journal 11111111-1111-4111-9111-111111111111 (source CLOSING, dated <YYYY-MM-DD>) carries 2 unstamped line(s)
stamp each of those lines at its own branch and run this migration again. A pre 629 year end close is not backfilled to the head office and there is no reverse and rerun tool. See docs/runbooks/gl-branch-stamp-0446.md.
```

The count leads. Each journal follows with its id, its source and its date, and the number of
unstamped lines it carries. The date is the journal's own `posted_at` read in UTC, so the words are
the same on every box whatever that box's timezone setting is.

## Which branch an unstamped line takes

Two answers, in this order.

**1. The journal's own header.** `gl_journals.branch_id` is the branch the writer stamped on the
journal. Every writer in the tree stamps it, and the same branch is written on each of the
journal's lines. So a journal whose header carries a branch hands that branch to its lines, and
that is the answer with no further reading.

**2. The source document the journal names.** When the header carries no branch, the branch comes
from the document the journal was posted for, read from that document's own branch column. The
table the document lives in depends on which module posted the journal, and `source_ref_id` is
the document's id:

| journal source | the source document | its branch column |
| --- | --- | --- |
| `INVOICE` | the invoice | `invoices.branch_id` |
| `PAYMENT` | the payment | `payments.branch_id` (NOT NULL) |
| `FULFILLMENT` | the order | `orders.branch_id` |
| `PO_RECEIPT` | the vendor bill | `purchasing_bills.branch_id` (NOT NULL) |
| `ADJUSTMENT` posted by the bank reconciliation module | the bank statement line | the claimed payment's branch, or the lodgement's |
| `ADJUSTMENT` posted by the landed cost module | the landed cost | `landed_costs.branch_id` (NOT NULL) |
| `ADJUSTMENT` posted by the inventory count module | the count sheet | `count_sheets.branch_id` (NOT NULL) |
| `MANUAL`, `ADJUSTMENT` with no module document | no document | the head office branch, below |
| `CLOSING` | no document | see the year end close section below |

The head office branch is the org default policy row's column:

```sql
SELECT head_office_branch_id::text
  FROM gl_policy
 WHERE branch_id IS NULL;
```

At run time the head office is that column; when it is empty, the live branch whose code is `TRT`;
and when neither answers, the posting is refused in words. That is `HeadOfficeBranchID` in the GL
module. "The earliest branch" was only migration 0445's one time backfill of the column, never a run
time answer.

## The two cases an operator will meet

### A journal whose source document names its branch

This is the ordinary case: a vendor bill, a landed cost, a customer invoice, a count sheet, a
bank match. The document names one branch and every unstamped line on that journal takes it.

The owner of the journal stamps the lines:

```sql
UPDATE gl_journal_lines l
   SET branch_id = $1
  FROM gl_journals j
 WHERE l.journal_id = j.id
   AND j.id = $2
   AND l.branch_id IS NULL;
```

where `$1` is the document's branch and `$2` is the journal's id. The `branch_id IS NULL` guard
keeps the statement from touching a line that is already stamped, so it is safe to run twice.

### A pre 629 year end close

Before 629 the year end close consolidated the year's result across the branches into one
journal, so its lines carry no branch of their own: there is no single branch to take, and the
migration refuses to backfill them to the head office.

There is no hand path that brings such a box to a count of `0`. A CLOSING journal cannot be
reversed (the ledger's reverse route refuses it: "posted by the year end close, not by a person in
the ledger"), a reversal would leave the original lines branchless anyway, and nothing voids a
line. So no hand step is written here in advance.

A box holding such a close stops here. Run the journal query above, and send its answer, quoted,
to the principal, who rules on that box from its own facts. The box takes 0446 only after that
ruling.

## After the lines are stamped

Run the migration again on the same box. The pre check counts the unstamped lines first, and with
none left it adds the constraint and validates it against the rows that are already there. The
migrate step then succeeds, the container comes up on the new version, and the box is on 0446.

If the count is still above zero, the refusal names what is left. It names the journal, its
source and its date, so the operator goes back to the two cases above rather than guessing.

## The frozen boxes

Frozen boxes (hh-demo-testers and staging) take 0446 only after Colton's CP-1 walk, under this
runbook, with the count query run first by whoever holds that box's access. A frozen box whose
unstamped lines all sit on ADJUSTMENT journals is worked through the ADJUSTMENT rows of the table
above; a frozen box that holds a CLOSING journal stops as the year end close section says.

## The finance box

The finance box has no exec, so the check is the boot itself. When a head carrying 0446 deploys,
the pre check refuses in words naming any branchless journal, and FB Deploy keeps the old
container. Nothing is stamped by the boot and nothing is written by the refusal.

So on the finance box the count query is run first, by whoever holds that box's access, and the
box is only given 0446 once it answers `0`. If it answers anything else, the refusal at the next
boot names the journals, and the work is the same work this runbook describes.
