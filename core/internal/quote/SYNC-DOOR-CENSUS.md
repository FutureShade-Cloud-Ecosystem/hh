# Sync door census

The sync doors are the second way into the quote and approval records: the device
feed at `/api/v1/sync/quotes` and `/api/v1/sync/approvals`. Each store behind a door
(`quote.SyncStore` in `syncscope.go`, `staffcontext.ApprovalsSyncStore` in
`syncscope_approvals.go`) must answer every caller exactly as the REST route that
does the same thing answers it. This document is the ruling for every method of
both stores, for every one of the thirteen roles. The test that holds it is
`TestQD1G_SyncDoorCensus` (`core/cmd/core/qd1g_sync_census_test.go`).

How the test holds it:

- It enumerates the exported methods of both store types by reflection, and the
  operations of the sync core's `Store` seam. A method or operation with no row in
  the test's table, or no row in section 1 below, fails the test and names it. A
  fourteenth role in the configuration fails it the same way.
- For each method and each role it plants the same rows twice (one for the REST
  call, one for the sync call), calls the REST route and the sync operation through
  the real registered mux and gates as the same caller, and compares allow with
  refuse. Where a sync method has no REST twin (a batch door, the withdrawal, the
  note, the replay), the row states the rule it must match and the test asserts that
  rule explicitly for every role.
- It requires that every door both admits and refuses somebody, so a census whose
  every answer is a refusal cannot pass.
- It reads this document: every row of section 1 must name a method the stores
  have, and every method must have a row, and every cell of section 3 must equal the
  answer observed.
- It follows both lists to the end (the REST cursor, the sync next_cursor), so a long
  lived gate cannot push a planted row off a first page.

## 1. The rule per door

| Door | REST twin | The rule it must match | Enforced in |
|---|---|---|---|
| `quotes.Create` | `POST /api/v1/quotes` | Work or better on the Quote Desk, and the quote's branch in the caller's reach. The caller becomes the quote's salesperson. | `SyncStore.Create`, `authorizeSyncCreate` (`syncscope_authority.go`), before any read or write |
| `quotes.Write` | `PATCH /api/v1/quotes/{id}` | The ownership ladder (plan 3.3): Work edits its own quotes, Manage edits any quote of its branch, nothing below Work edits, no role edits another branch's quote. The edit matrix and the expiry guard follow. | `SyncStore.Write`, `authorizeSyncWrite` and `ownershipRung`, on the locked row |
| `quotes.Delete` | the `PATCH` ladder; no REST delete exists | Withdrawing is an edit, so the same ladder as the `PATCH`; and only a DRAFT is withdrawn through sync (a SENT, ACCEPTED or CONVERTED quote is a record, the desk's own path withdraws it). | `SyncStore.Delete`, `authorizeSyncWrite`, then the DRAFT check |
| `quotes.Pull` | `GET /api/v1/quotes` (the list) | The list rule (plan 3.3): nothing without a Quote Desk level of Read or better; at Work the caller's own quotes; at Manage or Read every quote of the branches the caller reaches, a dealer wide caller every branch. | `SyncStore.Pull`, `syncReadScope` as a predicate of the query, so a hidden row never shortens a page |
| `quotes.Load` | `GET /api/v1/quotes` (the list), at the conflict envelope | The list rule (plan 3.3), as a predicate of the query: a quote the list would not show the caller is not found at all, the absent row's answer, so the engine never reads an unreadable row as visible. A quote the list would show travels whole. See section 4: this is the one deliberate difference from `GET /api/v1/quotes/{id}`. | `SyncStore.Load`, `syncReadScope` as a predicate of the query |
| `quotes.FindApplied` | none; a replay answers status and seq only | A replay of an applied key answers applied with the stamped seq and applies nothing. The read rule is the list rule above: a key on a quote the caller may not read is not found, so the replay carries nothing of that row, the seq included (UX-108). | `SyncStore.FindApplied`, `syncReadScope` as a predicate of the query; the engine never reads data on this path |
| `approvals.Create` | `POST /api/v1/staff/approvals` | Work or better on the counter, the Quote Desk or Manager Home. The caller files as itself. Credit and cash on delivery filings only: a margin, journal or freight filing is refused exactly as the rail refuses it. The branch must sit in the caller's org, and a caller not global scope must be granted it. | `ApprovalsSyncStore.Create`, `callerHoldsLevel`, `approvalCallerPolicy`, `approvalBranchInOrg` |
| `approvals.Write` | `POST /api/v1/staff/approvals/{id}/decide` for a decision; the requester's note has no REST twin | A decision needs Manage or better on Manager Home or the Quote Desk, then the rail's own `Decide` (branch reach, who may decide that policy and filer, separation of duties, PENDING only). A note is the requester's own edit of its own PENDING plain request, for a role the sync write fence lists as an approvals writer (a role that cannot file is refused at the fence); a floor request's detail is never an editable note. | `ApprovalsSyncStore.Write`, `writeDecision` (`holdsLevel`, `Decide`), `writeNote` |
| `approvals.Delete` | none; the withdrawal has no REST route | Only the requester withdraws a request, in any state except one whose approval has been consumed. Nobody else, in any state. Ownership decides, among the roles the sync write fence lists as approvals writers. | `ApprovalsSyncStore.Delete`, the requester and consumed checks on the locked row |
| `approvals.Pull` | `GET /api/v1/staff/approvals` (the list) | The list rule: Work or better on the counter, the Quote Desk or Manager Home; a dealer wide caller reads every branch, a scoped caller only its granted branches, none when it holds none; a floor request only for who could decide it or filed it. One addition to the list: a caller reads back its own filings in any branch. | `ApprovalsSyncStore.Pull`, `readerFor`; a tombstone names an id alone and stays (section 4) |
| `approvals.Load` | `GET /api/v1/staff/approvals` (the list), at the conflict envelope | The projection of a request travels only to a caller the list would show it to; the engine keeps the id and seq. | `ApprovalsSyncStore.Load`, `readerFor` |
| `approvals.FindApplied` | none; a replay answers status and seq only | As quotes.FindApplied: applied with the stamped seq, no projection, nothing applied. | `ApprovalsSyncStore.FindApplied`; the engine never reads data on this path |

`SyncStore.WithNow` and `ApprovalsSyncStore.WithJEDecider` are the two exported
methods that are not doors. `WithNow` pins the store's clock for tests, and
`WithJEDecider` attaches the bridge that voids a withdrawn credit memo's park on the
withdrawal's own transaction, so the tombstone and the void commit together. The rule
that bridge serves is `approvals.Delete`'s, ruled above: the requester withdraws its
own request, and nobody else, in any state except one whose approval has been
consumed. Both take no caller and touch no row, so both are listed in the test as not
a door.

## 2. The thirteen roles

The configuration's decided default (`core/internal/roleconfig/default.json`), the
levels the doors read. "Global" roles reach every branch; the rest reach the branches
they are granted.

| Role | Reach | Quote Desk | Counter | Manager Home |
|---|---|---|---|---|
| inside_sales | branch | work | work | none |
| sales_manager | branch | manage | manage | work |
| outside_sales | branch | work | none | none |
| yard_team | branch | none | none | none |
| yard_manager | branch | none | none | work |
| logistics_manager | branch | none | none | work |
| driver | branch | none | none | none |
| branch_manager | branch | work | manage | work |
| procurement_manager | branch | none | none | work |
| payables_receivables | global | none | none | work |
| financial_controller | global | none | none | manage |
| general_manager | global | read | read | manage |
| hr | global | none | none | none |

The yard manager's two none cells are decision log row 1156 (reversing row 370):
its sale and quote access is removed, so every quotes door refuses it here,
while the approvals doors keep admitting it through Manager Home.

## 3. The answers

One matrix per door: a row per role, a column per question the census asks of it.
A is allow, R is refuse. The sync answer and the REST answer (or the stated rule, where
there is no REST twin) are equal in every cell, which is what the test asserts. The
branch cases use branch A (the roles' granted branch) and branch B (not granted).

### `quotes.Create`

| Role | 1 | 2 |
|---|---|---|
| inside_sales | A | R |
| sales_manager | A | R |
| outside_sales | A | R |
| yard_team | R | R |
| yard_manager | R | R |
| logistics_manager | R | R |
| driver | R | R |
| branch_manager | A | R |
| procurement_manager | R | R |
| payables_receivables | R | R |
| financial_controller | R | R |
| general_manager | R | R |
| hr | R | R |

1. create in branch A
2. create in branch B

### `quotes.Write`

| Role | 1 | 2 | 3 | 4 |
|---|---|---|---|---|
| inside_sales | A | R | R | R |
| sales_manager | A | A | R | R |
| outside_sales | A | R | R | R |
| yard_team | R | R | R | R |
| yard_manager | R | R | R | R |
| logistics_manager | R | R | R | R |
| driver | R | R | R | R |
| branch_manager | A | R | R | R |
| procurement_manager | R | R | R | R |
| payables_receivables | R | R | R | R |
| financial_controller | R | R | R | R |
| general_manager | R | R | R | R |
| hr | R | R | R | R |

1. edit own quote in branch A
2. edit another seller's quote in branch A
3. edit own quote in branch B
4. edit another seller's quote in branch B

### `quotes.Delete`

| Role | 1 | 2 | 3 | 4 | 5 |
|---|---|---|---|---|---|
| inside_sales | A | R | R | R | R |
| sales_manager | A | A | R | R | R |
| outside_sales | A | R | R | R | R |
| yard_team | R | R | R | R | R |
| yard_manager | R | R | R | R | R |
| logistics_manager | R | R | R | R | R |
| driver | R | R | R | R | R |
| branch_manager | A | R | R | R | R |
| procurement_manager | R | R | R | R | R |
| payables_receivables | R | R | R | R | R |
| financial_controller | R | R | R | R | R |
| general_manager | R | R | R | R | R |
| hr | R | R | R | R | R |

1. withdraw own quote in branch A
2. withdraw another seller's quote in branch A
3. withdraw own quote in branch B
4. withdraw another seller's quote in branch B
5. withdraw its own SENT quote (stated rule, no REST twin)

### `quotes.Pull`

| Role | 1 | 2 | 3 | 4 |
|---|---|---|---|---|
| inside_sales | A | R | R | R |
| sales_manager | A | A | R | R |
| outside_sales | A | R | R | R |
| yard_team | R | R | R | R |
| yard_manager | R | R | R | R |
| logistics_manager | R | R | R | R |
| driver | R | R | R | R |
| branch_manager | A | R | R | R |
| procurement_manager | R | R | R | R |
| payables_receivables | R | R | R | R |
| financial_controller | R | R | R | R |
| general_manager | A | A | A | A |
| hr | R | R | R | R |

1. read own quote in branch A
2. read another seller's quote in branch A
3. read own quote in branch B
4. read another seller's quote in branch B

### `quotes.Load`

| Role | 1 | 2 | 3 | 4 |
|---|---|---|---|---|
| inside_sales | A | R | R | R |
| sales_manager | A | A | R | R |
| outside_sales | A | R | R | R |
| yard_team | R | R | R | R |
| yard_manager | R | R | R | R |
| logistics_manager | R | R | R | R |
| driver | R | R | R | R |
| branch_manager | A | R | R | R |
| procurement_manager | R | R | R | R |
| payables_receivables | R | R | R | R |
| financial_controller | R | R | R | R |
| general_manager | A | A | A | A |
| hr | R | R | R | R |

1. projection of own quote in branch A
2. projection of another seller's quote in branch A
3. projection of own quote in branch B
4. projection of another seller's quote in branch B

### `quotes.FindApplied`

The census asserts for every role and for every planted row that the replay answer carries no projection and changes nothing. There is no allow or refuse cell: the rule is the same for all thirteen roles. A caller that may not read the row is refused the row's seq as well: the key is not found for that caller (UX-108), and the answer still carries nothing of the row.

### `approvals.Create`

| Role | 1 | 2 | 3 |
|---|---|---|---|
| inside_sales | A | R | R |
| sales_manager | A | R | R |
| outside_sales | A | R | R |
| yard_team | R | R | R |
| yard_manager | A | R | R |
| logistics_manager | A | R | R |
| driver | R | R | R |
| branch_manager | A | R | R |
| procurement_manager | A | R | R |
| payables_receivables | A | A | R |
| financial_controller | A | A | R |
| general_manager | A | A | R |
| hr | R | R | R |

1. a credit filing in branch A
2. a credit filing in branch B
3. a margin filing (the quote desk's own)

### `approvals.Write`

| Role | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 |
|---|---|---|---|---|---|---|---|---|---|---|
| inside_sales | R | R | R | R | R | R | A | R | R | R |
| sales_manager | R | R | R | A | R | R | A | R | R | R |
| outside_sales | R | R | R | R | R | R | A | R | R | R |
| yard_team | R | R | R | R | R | R | R | R | R | R |
| yard_manager | R | R | R | R | R | R | A | R | R | R |
| logistics_manager | R | R | R | R | R | R | A | R | R | R |
| driver | R | R | R | R | R | R | R | R | R | R |
| branch_manager | R | R | R | R | R | R | A | R | R | R |
| procurement_manager | R | R | R | R | R | R | A | R | R | R |
| payables_receivables | R | R | R | R | R | R | A | R | R | R |
| financial_controller | A | A | R | A | A | A | A | R | R | R |
| general_manager | A | A | R | A | A | A | A | R | R | R |
| hr | R | R | R | R | R | R | R | R | R | R |

1. decide a credit request of branch A
2. decide a credit request of branch B
3. decide its own credit request
4. decide a floor request Inside Sales filed in branch A
5. decide a floor request Inside Sales filed in branch B
6. decide a floor request a Branch manager filed in branch A
7. edit its own pending plain request's note (stated rule, no REST twin)
8. edit another requester's pending note (stated rule, no REST twin)
9. edit its own decided request's note (stated rule, no REST twin)
10. edit the detail of a floor request it filed (stated rule, no REST twin)

### `approvals.Delete`

| Role | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 |
|---|---|---|---|---|---|---|---|---|
| inside_sales | A | R | A | R | A | R | R | R |
| sales_manager | A | R | A | R | A | R | R | R |
| outside_sales | A | R | A | R | A | R | R | R |
| yard_team | R | R | R | R | R | R | R | R |
| yard_manager | A | R | A | R | A | R | R | R |
| logistics_manager | A | R | A | R | A | R | R | R |
| driver | R | R | R | R | R | R | R | R |
| branch_manager | A | R | A | R | A | R | R | R |
| procurement_manager | A | R | A | R | A | R | R | R |
| payables_receivables | A | R | A | R | A | R | R | R |
| financial_controller | A | R | A | R | A | R | R | R |
| general_manager | A | R | A | R | A | R | R | R |
| hr | R | R | R | R | R | R | R | R |

1. withdraw its own pending request (stated rule, no REST twin)
2. withdraw another requester's pending request (stated rule, no REST twin)
3. withdraw its own approved-unused request (stated rule, no REST twin)
4. withdraw another requester's approved-unused request (stated rule, no REST twin)
5. withdraw its own rejected request (stated rule, no REST twin)
6. withdraw another requester's rejected request (stated rule, no REST twin)
7. withdraw its own consumed request (stated rule, no REST twin)
8. withdraw another requester's consumed request (stated rule, no REST twin)

### `approvals.Pull`

| Role | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 |
|---|---|---|---|---|---|---|---|---|
| inside_sales | A | R | A | A | A | R | A | A |
| sales_manager | A | R | A | A | R | R | A | A |
| outside_sales | A | R | A | R | R | R | A | A |
| yard_team | R | R | R | R | R | R | R | R |
| yard_manager | A | R | A | R | R | R | A | A |
| logistics_manager | A | R | A | R | R | R | A | A |
| driver | R | R | R | R | R | R | R | R |
| branch_manager | A | R | A | R | R | A | A | A |
| procurement_manager | A | R | A | R | R | R | A | A |
| payables_receivables | A | A | A | R | R | R | A | A |
| financial_controller | A | A | A | A | A | A | A | A |
| general_manager | A | A | A | A | A | A | A | A |
| hr | R | R | R | R | R | R | R | R |

1. read a plain request of branch A
2. read a plain request of branch B
3. read its own plain request
4. read a floor request Inside Sales filed in branch A
5. read a floor request Inside Sales filed in branch B
6. read a floor request a Branch manager filed in branch A
7. read a floor request it filed itself
8. read its own plain request filed in branch B

### `approvals.Load`

| Role | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 |
|---|---|---|---|---|---|---|---|---|
| inside_sales | A | R | A | A | A | R | A | A |
| sales_manager | A | R | A | A | R | R | A | A |
| outside_sales | A | R | A | R | R | R | A | A |
| yard_team | R | R | R | R | R | R | R | R |
| yard_manager | A | R | A | R | R | R | A | A |
| logistics_manager | A | R | A | R | R | R | A | A |
| driver | R | R | R | R | R | R | R | R |
| branch_manager | A | R | A | R | R | A | A | A |
| procurement_manager | A | R | A | R | R | R | A | A |
| payables_receivables | A | A | A | R | R | R | A | A |
| financial_controller | A | A | A | A | A | A | A | A |
| general_manager | A | A | A | A | A | A | A | A |
| hr | R | R | R | R | R | R | R | R |

1. projection of a plain request of branch A
2. projection of a plain request of branch B
3. projection of its own plain request
4. projection of a floor request Inside Sales filed in branch A
5. projection of a floor request Inside Sales filed in branch B
6. projection of a floor request a Branch manager filed in branch A
7. projection of a floor request it filed itself
8. projection of its own plain request filed in branch B

### `approvals.FindApplied`

The census asserts for every role and for every planted row that the replay answer carries no projection and changes nothing. There is no allow or refuse cell: the rule is the same for all thirteen roles.

## 4. Differences from REST that are ruled, not accidents

1. **The quotes door is a list door.** `quotes.Pull`, `quotes.Load` and the replay read
   what `GET /api/v1/quotes` lists. A Work caller therefore reads its own quotes only
   through sync, while `GET /api/v1/quotes/{id}` lets a Work caller read any quote in its
   branch by id. This is the one deliberate difference from REST GET by id: a device feed
   has no id to ask for, it syncs a list, and plan 3.3 gives the list at Work as own
   quotes. Manage, Read and the dealer wide roles read the same set both ways.
2. **A requester reads back its own filings.** `approvals.Pull` and `approvals.Load` show a
   caller its own filing in a branch it does not reach, when the list route admits the
   caller at all (leg 8 of the two approvals read matrices; the census asserts this
   explicitly). The rail does not fence the filing branch by reach, so without this a device
   could push a request and never see it come back. Nothing is shown that the caller did not
   file.
3. **Tombstones carry an id alone.** `approvals.Pull` returns the tombstone of a withdrawn
   request to every caller that passes the org fence, with no projection. REST has no
   tombstones. The id and seq reveal no field of the request.
4. **A replay is answered from the key alone.** `FindApplied` answers an
   already applied key as applied with the stamped seq, to whoever replays the key, and
   applies nothing. A caller that may not read the row learns nothing of it: the key is
   not found for that caller, so the replay carries neither the row's seq nor any part of
   the row (UX-108). The status word stays the engine's own bound answer: the quotes
   store cannot answer the gate's existence read, so the gate keeps the bound answer for
   a key it cannot attribute, and what the scope owes is the row.
5. **Withdrawal and note edits are ownership rules.** `approvals.Delete` and the note arm
   of `approvals.Write` have no REST twin and are ownership rules behind the sync write fence: the requester
   edits or withdraws its own request, and nobody else does, provided it still holds a
   filing role (the fence lists the ten roles with work on counter-till, quote-desk or
   manager-home). A request a role could not file through REST can exist only if the role
   was changed after filing; that demoted requester is refused through sync (the desk's
   REST path is unaffected).
6. **Only a DRAFT quote is withdrawn through sync,** where the REST edit of a SENT
   quote's validity is allowed. The withdrawal is a tombstone that takes effect; the
   desk's own path withdraws a record.

## 5. Known and accepted

Recorded as known, so the next reader does not reopen them:

1. The census principals double as planted filers for some cells: the inside_sales
   caller is the same user as the Inside Sales filer, and the branch_manager caller is
   the planted Branch manager filer. So some legs of the approvals matrices are a
   caller's own filings. The peer case is covered by the QD1C and QD1E tests, not here.
2. A create replay after a sync decision files a twin pending card. The decision stamps
   the decider's key over the filer's, so the filer's replay finds no match. It fails
   safe (a duplicate pending card) and is the same pattern the quotes write has.
3. The approvals pull for the walled roles (yard_team, driver, hr) refuses every live
   row, so it pages to the end of the table on each pull. Accepted cost.
4. The margin detail of a floor request is gated by who may read the request, not by the
   cost list. The default cost list covers every role that can file or decide, so
   nothing leaks today. A dealer that drops a Quote Desk role from the list would show
   that role the figures of requests it may still read.
5. Quote create on a taken id tells a Work caller that the id exists, for any quote id
   in its org. This is intended.

## 6. The registered sync scopes

The census reads every `RegisterScope` in `core/cmd/core` and requires each scope to be
ruled. A scope on neither list fails the census by name. These may touch the quote or
approval tables, and why:

- `quotes` and `approvals`: the two stores this census drives door by door.
- `customer_quotes`: a separate table, not the staff quotes table.
- `orders`: the tenant plan table; it reads no quote or approval column.
- `widget_measures`: reads only the layout tables.
- `attachments`: carries links with owner_kind quote. They are fenced by org only and
  carry no quote field today; nothing reads those links through sync.

Every other registered scope is listed in the test as reviewed clear of both tables.

## 7. Keeping it true

A door added to either store, a new operation on the sync core's `Store`, or a new role in
the configuration fails the census until it is ruled: add its row to the table in
`qd1g_sync_census_test.go`, its rule to section 1 and its matrix to section 3 here. The
matrices are the test's own answers (`QD1G_DUMP=<file>` writes them as data), and the
test reads them: after the run it parses section 3 and requires every door, role and leg
cell to equal the answer the census observed, in both directions, so a rule changed on
both sides at once leaves the census red until this document follows.
