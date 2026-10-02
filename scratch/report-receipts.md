# Report: the quality receipt formats (work item B, branch till/receipt-formats)

The brief: /home/colton/Desktop/FBHQ/counter-till/scratch/brief-receipts.md (decision log
row 666). Everything below was done in /home/colton/Desktop/FBHQ/counter-till/wt-receipt,
one commit per sub-item, tests first with bites run, on top of cd46fd6.

## The commit list

The item was written against cd46fd6 (the main the brief named). While the work ran, main
moved (the hardening lane's open PRs 96 and 97 merged there, and they touch the same
receipt files), so the branch was rebased onto the moved main (b8e4009) before the exit
test. The rebase's one seam: main's PR 97 had already landed the fulfilment words, keyed the
same way on pos's own constants and spelling the identical four phrases, so this branch
keeps main's saleWords funcMap as the one mapping and the page template speaks through it
too; the change due, the deposit and the page format remain this branch's deltas. The
commit list, as rebased:

- `1d215d3` printing: the quality 80 mm counter receipt (row 666 item 1): the fulfilment in
  words, the change due row, the deposit and balance rows
- `a3f92ea` printing: the full-page receipt, format=page (row 666 item 2): HH's own second
  paper, the same Receipt
- `e9c1fa4` printing: the reprint from the order, pinned end to end (row 666 item 3)
- `1cea0fa` printing: the integration legs for the receipt's committed reads and reprints
- `512ee04` printing: the page template speaks the sale type words the roll template speaks
  (the rebase's one seam)
- `8a5e69d` client: regenerate the api client for the receipt route's format parameter (the
  contract change rides the generated client; the idempotence gate demanded it)
- `cd40fe9` BLOCKERS: the exit test's two margin glance legs are red on main itself
  (recorded, routed to the margin surface's owner)

The branch is pushed: `git push origin till/receipt-formats` (a first push of a branch that
was never on the remote; no force anywhere).

## What landed

Sub-item 1 (the 80 mm roll): the sale type prints as the counter's plain words, keyed on
pos's own constants, unknown verbatim, empty silent; the change due row on a cash
over-tender, read from the order's own cash-tendered fact (orders.metadata, the same column
pos's replay read serves); the deposit row and the balance due row on an unpaid-at-ring sale
taken with a deposit. The demo stub's bare document is replaced by a mirror of the polished
document (same words, same layout, the stub's own invented values), and the exit test's
union context overlays this branch's core tree beside the app directory (named below).

Sub-item 2 (format=page): the pass's only contract change, one optional query parameter
`format` (roll, the default; page), declared in `core/api/openapi.yaml` (the receipt path,
about line 3594) and `core/api/fragments/catalog-support.yaml` (the same path, about line
1105) in step with each other and with the neighbouring query parameters; the core's parity
test over the fragments passes. The page document renders the SAME Receipt struct with its
own print CSS (letter and A4 margins, never the roll's narrow column); the template's
provenance comment names it HH's own, no reference file. The till's Summary step gains the
roll's sibling control, Print full page, disabled exactly when the roll print is; the More
menu's any-past-sale receipt stays the reprint path. The stub mirrors the page document and
the format refusal.

Sub-item 3 (the reprint from the order): the core's ordinal behaviour kept and pinned: the
fetch by the order id and the fetch by the visible number read the same order-keyed count.
The pin bit: the receipt tests' queue fake answered the count from a static field, so the
by-order-number pin read 0 after a real prior print; the fake is faithful now (a successful
mark flips the row, the count is the store's own PRINTED rows for the document and ref).

## The bite proofs (command and failing output)

1. Item 1, the struct bite (before any implementation):

       cd core && go test ./internal/printing/
       internal/printing/receipt_test.go:221:9: rec.ChangeDueCents undefined (type Receipt has no
       field or method ChangeDueCents)
       internal/printing/receipt_test.go:244:13: store.sale.DepositCents undefined (type receiptSale
       has no field or method DepositCents)  [and more of the same]

2. Item 1, the paper bite (structs, service and reads in, template still unwired):

       --- FAIL: TestRenderReceiptHTML_FulfilmentInWords
           render_test.go:205: sale type COLLECT_NOW must print as "Collected at the counter", the
           words are not on the page
           render_test.go:208: the raw constant "COLLECT_NOW" must never reach the paper  [x4]
       --- FAIL: TestRenderReceiptHTML_ChangeDueRow
           render_test.go:251: an over-tendered cash sale must carry the Change due row
       --- FAIL: TestRenderReceiptHTML_DepositAndBalanceRows
           render_test.go:292: a sale taken with a deposit must carry the Deposit row

3. Item 2, the surface bite:

       internal/printing/receipt_test.go:291:15: undefined: parseReceiptFormat
       internal/printing/render_test.go:356:15: undefined: RenderReceiptPageHTML

   and the till side, run against the pre-item wiring (the four app files stashed):

       Error: locator.click: Test timeout of 30000ms exceeded.
         - waiting for getByTestId('sold-panel').getByTestId('sold-print-page')
       1 failed

4. Item 3, the fake-fidelity bite (the pin against the old fake):

       --- FAIL: TestReceipt_ReprintByTheOrderCarriesTheOrdinal
           receipt_test.go:330: the reprint must be marked #1, got count 0

5. Item 3, the stub mutation bite (the stub's print count disabled, the leg run, restored):

       Error: expect(received).toContain(expected) // indexOf
       Expected substring: "REPRINT #1"
       > 276 |   expect(pageText).toContain("REPRINT #1");
       1 failed

6. The deposit predicate's mutation bite (integration, against real Postgres:
   `AND p.received_at = i.created_at` dropped from the deposit subselect, leg run, restored;
   a settlement payment of 2000 allocated later):

       --- FAIL: TestIntegration_Receipt_DepositAndChangeDueFromTheCommittedRows
           receipt_integration_test.go:588: a later settlement must never ride the deposit figure,
           got 6000

   With the predicate in place the same leg passes: the deposit holds at 4000 while the
   tender rows grow to two and the balance moves to total less 6000.

## The gates' verdict lines

- `go test ./internal/printing/` (the module root is `core/`, so the command is
  `cd core && go test ./internal/printing/`): ok
- `gofmt -l` on every file this lane touched: clean (the syncscope files gofmt lists on
  main are not this lane's)
- `go vet -tags demo ./internal/demo/` and `go test -tags demo ./internal/demo/`: ok (the
  package builds only under the demo tag)
- `go test ./cmd/core/ -run Parity` (the fragments against the openapi): ok
- Integration against a throwaway Postgres (postgres:16-alpine, migrate + seed through the
  core binary, removed afterwards): the whole receipt integration set ok, including the new
  legs; `-tags integration` with no DATABASE_URL still skips cleanly
- `cd web/apps/counter-till && npm run typecheck`: clean
- `npm test` (the unit runner 388 pass, the Playwright stub suites 90 pass): green; the
  first playwright start hit vite's known EMFILE and the rerun used CHOKIDAR_USEPOLLING=1,
  as the brief allows; the unit runner needs node 22 (the version the stub header names),
  taken from scratch/node22
- The exit test on the head: see the verdict line in the log block below.

## The exit test

Three runs, reported run by run; the third is the verdict on the head as it stands.

First run (pre rebase, the branch still on cd46fd6; scratch/receipts-exit-run1.log): 9
failure lines. Two were real acceptance failures (the margin glance legs), the screenshot
ledger failures were their consequence, the lanes' suites and money scan lines died in the
stub harness's vite web server hitting EMFILE, and the bun test line failed. The bun
failure turned out to be REAL and this item's: the api client's committed generation had to
carry the contract change (fixed by the regeneration commit). The rebase was forced by the
margin failures' first reading (a mixed union); the probes below correct that reading.

Second run, on the rebased head (scratch/receipts-exit-run2.log): the same failure set
minus nothing, which disproved the mixed-union reading and forced the probes.

The probes (both boxes removed again after): a box built from PURE origin/main (b8e4009)
and a box built from PURE cd46fd6, no overlays, each answering the CLERK's margin glance
with the byte-absent `{}`. The two failing legs (10-world's clerk glance and 20-sales'
below floor line, whose staff-side read meets the same answer) are red on main itself,
without this branch anywhere in the build; the previous train's green run was on the pre
cd46fd6 main. Recorded in docs/BLOCKERS.md and routed to the margin surface's owner (the
surface this lane is expressly out of scope of).

The EMFILE's own cause, for the record: the machine carries leaked vite dev servers from
earlier sessions (visible as hh-203/wt-before and wt-after processes), and
fs.inotify.max_user_instances is 128, so a run that starts several vite web servers meets
EMFILE intermittently no matter whose branch it builds. The same gates pass standalone
(proven above and below).

Third run, on the final head (scratch/receipts-exit-run3.log):

    PASS: the union context assembles (origin/main with this branch's app and core directories)
    PASS: the image builds from main's tree and this branch's app
    PASS: the throwaway Postgres answers
    PASS: the generated gate value is a bcrypt hash
    PASS: the box answers /healthz on a clean database
    PASS: the testers' page sits behind the phrase gate
    PASS: the app serves the testers' page
    PASS: the harness holds the Kingston sale's line id for the three-rail leg
    PASS: the flows table answers its two rows
    FAIL: the acceptance run failed
    FAIL: the screenshots drifted (count 28, missing: sales-04-below-floor-populated sales-04-below-floor-rung, extra:)
    PASS: no two of the story's screenshots are byte-identical
    PASS: the box came back after the restart
    PASS: the restart leg is green (the pre restart bearer is dead, a fresh sign in works)
    FAIL: the restart leg left 29 screenshots, want 31
    PASS: no two of the run's 31 screenshots are byte-identical
    FAIL: the lanes' suites failed
    PASS: the app typechecks
    PASS: the shape check passes
    PASS: the scope check passes
    PASS: bun test passes
    PASS: the manifest drift check passes
    FAIL: the money scan printed something
    PASS: the card scan prints nothing
    PASS: the sink scan is green
    == done: 7 failure(s)

The verdict, honestly: every line this item owns is green, and every FAIL is accounted for
outside this item's surface.

- The acceptance run's own failures are exactly the two margin glance legs red on pure main
  (the probes above), plus their two screenshot-ledger consequences, plus ONE further leg,
  20-sales' parked sales, failing this round on openParked's five second settle race (the
  dialog's list a beat behind the button's stated count, a -1 under box load; the same leg
  passed in runs 1 and 2 and on the previous train's run, and no product assertion is
  involved: a timing flake, reported as seen).
- Every receipt leg this item adds passed on the box in all three runs: the cash receipt
  document (change due row, plain fulfilment words, the invented letterhead, never the real
  dealer's name), the full page leg (the same figures, REPRINT #1 by its own print count),
  and the More menu reprint (REPRINT #2 by the order id).
- bun test passes (the regeneration), as predicted.
- The lanes' suites and money scan lines are the inotify EMFILE lottery again (the leaked
  dev servers above); both pass standalone, the unit runner's fail count was 0 inside the
  run's own log, and the money scan passes standalone with the polling env.

The named script change: `web/apps/counter-till/test/acceptance/exit-test.sh` now overlays
the branch's `core/` tree into the union context the same way the app directory is
overlaid (a tar copy beside the git-archive base, the same exclusion discipline, MAIN_REF's
behaviour unchanged), so the image's core and the acceptance box run this branch's receipt
routes. The change is named here and in the commit messages, per the brief.

Run: `bash web/apps/counter-till/test/acceptance/exit-test.sh` (scratch/receipts-exit-run1.log
in the cycle workspace holds the full output; the script cleaned its containers, database,
network, image and scratch context after itself).

## The contract diff's shape

One optional query parameter on one path, in two files, nothing else:

    core/api/openapi.yaml               /api/v1/pos/sales/{id}/receipt.get.parameters  +12
      - name: format
        in: query
        required: false
        description: The paper the document is laid out for, the 80 mm counter roll (the
        default) or the full-page letter and A4 document. An unknown value is a 400 naming
        the value.
        schema: { type: string, enum: [roll, page], default: roll }
      plus the 400 response ref the route did not carry before
    core/api/fragments/catalog-support.yaml   the same parameter, the fragment's own
      dense style (enum: [roll, page]), +12

## The judgements the reviewer should judge

1. The change-due source. The order's own `cash_tendered_cents` in `orders.metadata`, the
   exact column pos's replay read serves, with the ring's own derivation (tendered minus
   total, pos repository.go's arithmetic). Rendered only when it is greater than zero, so an
   exact tender prints no row and a short tender cannot exist (the ring refuses it). The
   alternative the brief refuses (recomputing from raw tenders) is nowhere.
2. The deposit source. The core states no deposit column, and `pos_sale_tenders` carries
   only split legs, so the figure is read as the payments allocated to the sale's invoice
   whose `received_at` equals the invoice's `created_at`: both are now() of the ring's one
   transaction (the pos repository's own comment names that transaction-fixed property), so
   the predicate names exactly the ring's own capture. A later AR settlement is a later
   transaction and never rides the figure (the integration leg pins this with a real later
   settlement, and the mutation bite proves the predicate is what holds it). Rendered only
   while the invoice carries a balance, so a settled sale shows its payments as tenders and
   an unsettled account sale shows its balance alone, no invented deposit. Consequence worth
   judging: a deposit sale's paper carries the deposit money twice in two vocabularies (the
   tender row that moved it, the Deposit row that names it inside the total), and the
   arithmetic on the paper reads total, deposit, balance.
3. The full-page layout. HH's own (no reference file; the template's provenance comment says
   so, row 666 item 2): the roll's blocks and words on a sheet, 18mm @page margins and a
   180mm measure, never a forced page size (the same document prints on letter and A4), the
   same monospace family as the roll so the two documents read as siblings, the same
   escaping boundary, the same honesty rows, the mark in the foot.
4. The stub mirror's fidelity. The stub's documents are built from the same shapes the core
   renders: the letterhead from the stub's own invented branch rows, the ON ACCOUNT
   synthetic tender, the fulfilment words map, the money and stamp formats, the simulated
   card word, the change due, deposit and balance rows, the reprint ordinal from a per-sale
   print count, and the format refusal's words (400 naming the value). The stub's ring grew
   the fields the mirror needs (payment numbers on tender legs, the deposit's own capture
   leg, the invoice balance, the operator, the created-at stamp); the contract's response
   bodies are unchanged (the new fields are stub-internal).
5. The demo letterhead's address. Row 666 item 1 makes the branch address a printed fact and
   the brief binds the lane to never a real dealer's name; migration 0003 planted the demo
   branches' address line1 with the first dealer's real trading name in it. The populate's
   existing convergence (the P2-1 precedent) now covers the two address line1 values
   (Trenton: 12 Yard Gate Road, Kingston: 9 Yard Gate Road), guarded, demo only, migrations
   untouched. That is one file outside this lane's declared printing fence,
   `core/internal/demo/populate.go`; the extension is recorded in docs/BLOCKERS.md with the
   reasoning, and the acceptance leg pins the paper: the letterhead carries the invented
   words and never the real name (held encoded, the name sweep's own discipline).
6. The judgement to rule on that is not mine: the two margin glance legs and the P3-10
   ruling they now meet (the BLOCKERS row). The brief put the margin surface out of my
   scope; the probes say the redness travels with main, not with this branch, so the
   principal can sequence it without touching this train.
7. Anything the brief missed, found on the way:
   - The receipt tests' queue fake was not faithful (the count came from a static field);
     the item 3 pin exposed it and the fake is fixed, so future count pins prove the real
     keying.
   - The exit test's screenshot ledger was left at exactly its 30 + 1 names: the new pins
     ride the existing cash receipt leg's story (roll, page, reprint) rather than adding
     screens, so no ledger change was needed and the run's cost did not grow.
   - The acceptance suite's change-due and deposit figures are read from the ring's own
     answer (PosSale carries change_due_cents and deposit_cents; the GET sale view carries
     neither), which is still every figure from the server.
   - The api client is generated from the openapi and committed: the contract change owed a
     regeneration (the staff-printing function's optional format in its query opts), and the
     idempotence gate is the test that caught it. The Go side's generated types
     (core/internal/platform/apigen) carry no receipt route shape, so nothing to regenerate
     there.
   - The repo's gate command from the root: the Go module root is `core/`, so the brief's
     `go test ./core/internal/printing/...` runs as `cd core && go test ./internal/printing/...`
     (the root form answers "directory prefix does not contain main module").
