# ADR-0026: a reversed payment is never banked, and its money waits in 2310

Status: Proposed

Drafted in the sync freshness cycle, item SF-2b (money class), after the Opus read of SF-2 found the defect on main
and the principal's rulings on it (FROM-PRINCIPAL 16, 19 and 20).

## Context

A reversal (`payments/reversal.go`) posts a reversing entry and stamps `reversed_at` on the original payment. It
leaves the original's `status` as it was, `RECEIVED`. Two writers moved a payment from `RECEIVED` to `DEPOSITED`
looking only at the status: the bank match claim (`bankrec` PersistMatch) and `till.Repository.MarkDeposited`. Both
reported success on a reversed payment, so a payment whose money had gone back read DEPOSITED and was booked into a
bank match.

## Decision

1. **The refusal lives in the UPDATE that decides the move.** Every writer that moves a payment toward banked carries
   `reversed_at IS NULL` in the same `UPDATE ... WHERE id = ANY(...) AND status = 'RECEIVED' AND reversed_at IS NULL`.
   Never a separate pre-check read: the UPDATE's row lock, and its WHERE re-checked after a concurrent commit, is what
   sees a reversal that commits beside it; a read before it would reopen the race it closes. Both batch writers take
   every named payment row first, in ascending id order (`payments.LockPaymentRowsTx`, LOCK-ORDER.md), then the UPDATE.
2. **Reversing a DEPOSITED payment is legal.** `reversal.go` refuses only a payment already reversed, a reversing
   entry, and a VOID payment. A payment banked first and reversed after ends DEPOSITED with `reversed_at` set: the money
   was banked, then given back by a reversal that posts its own entries. The reversal-first order is the one the
   predicate refuses.
3. **A replay on a payment that was deposited and then reversed is refused, not a silent success.** A replay of
   `MarkDeposited` on an ordinary already DEPOSITED payment is the idempotent no op it always was. On one that was
   reversed in between, a success would tell the caller the money sits in a deposit when it went back
   (FROM-PRINCIPAL 19). The unmoved row reads `reversed_at IS NOT NULL`, so `MarkDeposited` returns
   `till.ErrPaymentReversed` (409) and rolls the whole call back. A test pins it.
4. **MarkDeposited compares the rows moved with the rows asked.** For each row not moved it reads why (till owns that
   read): a reversed row refuses; an already DEPOSITED or absent row stays the no op.
5. **The bank claim's refusal names the payments that did not move, and three causes.** The claim's UPDATE returns the
   moved ids (`RETURNING id`); the refusal lists the others by the id bankrec holds for them (the match input's target,
   the proposal's payment ids: the id the Money Desk also asks the dealer to paste) with the sentence "payment already
   claimed, reversed or not RECEIVED". It names three causes because naming one would need a read of the payments
   table, a cross module read the tablegate census forbids. The desk matches the stable first words and puts the
   same three causes in the dealer's words.

6. **EXCEPTION, a reversed CARD payment is matched (SF-2d round 4, ruling 52.2).** A reversal is a ledger act: it
   does not touch the gateway, so the processor settles the captured card anyway and the bank statement carries a
   settlement line that has to be explained. The bank claim's predicate is `status = 'RECEIVED' AND (reversed_at IS
   NULL OR method = 'CARD')`. The match posts the usual clearing (Dr 1000 / Cr 1010), which leaves the card clearing
   account at zero because the receipt debited it. Every other reversed tender (cash, cheque, EFT) is still never
   matched: no deposit of it can be real. `MarkDeposited` (no live caller) still refuses every reversed payment.
7. **Every reversal credits 2310, whatever the tender or status (FROM-PRINCIPAL 52).** The GL books a reversal as
   Dr AR 1100 / Cr 2310 "Customer Refunds and Unapplied Cash" (`payment.reversed` / `refund_payable`, migration
   0426). A reversal moves no money at the bank, and 1020 is never the right credit for it: the original payment stays
   RECEIVED and the till's expected tenders count every RECEIVED payment of the session, so the money is either still
   in the drawer, where it is lodged and the bank match clears 1020, or gone unrecorded, where the count shows the
   shortage and the close posts Dr 6350 / Cr 1020. Crediting 1020 at the reversal would credit it twice in either
   case, driving it below zero while the bank holds the money. The account therefore depends on no status, no
   deposit, no lodgement, and no payload fact: there is no fallback by status. `payment.bounced` is its own rule
   (that money never arrived) and is untouched. A payment is reversed or bounced, never both: a reversal takes only a
   RECEIVED or DEPOSITED payment and a bounce refuses a reversed one, each decided under the payment row lock.
8. **2310 is cleared by the act that moves the money.** Re-application to an invoice (Dr 2310 / Cr AR; also the right
   way to fix a payment rung on the wrong account, since a new tender would count the cash twice in the drawer),
   a pay out (Dr 2310 / Cr where the money leaves: 1020 with a PAID_OUT movement from the drawer, 1000 from the bank),
   or a write off for the short or duplicate case (Dr 2310 / Cr 6350, after the count has posted the shortage).
   Those three are SF-2l, SF-2m and SF-2n; until they land the liability simply stands, which is the truth: the
   customer is owed it.

## Consequences

- A refused write changes no row, so the sync feed re-serves nothing for it (the trigger of ADR 0025 draws only when
  a row changes).
- A core message that a web package reads is a contract: the Money Desk's stub contract test finds each sentence in the
  core's source, so a message change runs every web contract test and stub that reads it.
- The ledger side is SF-2d (decisions 6 to 8). An unmatch of a reversed CARD's deposit is allowed (it only moves Dr 1000 / Cr 1010 back, the card is claimable
  again and 2310 is untouched), while a reversed cheque, cash, EFT or other payment's is refused (it can never be
  claimed again), and the `payment.reversed` payload keeps the original's status facts and branch for the
  audit only.
- LATENT: `till.Repository.MarkDeposited` sets a payment DEPOSITED without posting a clearing journal (only a bank
  match posts Dr bank / Cr clearing). It has no live caller today (it follows the claim's predicate, a reversed CARD is deposited and every other reversed tender refused). A caller that used it would leave the GL on the
  clearing account while the status says DEPOSITED, and a deposit-status payment would show as banked in the matcher while the
  GL never moved the money there. It must post its own clearing journal before it gains a caller.

## Re-application (SF-2l, migration 0431)

Re-application is the first act that clears 2310: it applies the money a reversal holds to invoices of the same
customer, Dr 2310 / Cr AR.

1. **The held figure** of a reversed payment is its amount less the sum of its rows in `payment_reapplications`. There
   is no column on `payments` (a sync feed row; a stamp would draw its counter for a fact no device reads) and no second
   ledger. The service refuses a request above the figure under the payment row `FOR UPDATE`, and the table's guard
   trigger refuses the same insert, so a second call site cannot forget. The pay out and the write off (SF-2m, SF-2n)
   subtract from the same figure in rows of their own.
2. **It reuses the settle.** The invoice balance move (with its invoices feed seq and the PAID or PARTIALLY_PAID flip)
   and the AR subledger PAYMENT row are `applySliceTx`, the body `AllocateToInvoiceTx` already ran, now shared. A
   re-application writes no payments row, no allocation, no till movement and no tender: the cash is already in the
   drawer's count through the reversed original, and a new payment would count it again.
3. **Who and what.** A manager, with a reason and an idempotency key. The invoices are the payment's own customer's,
   open, in the caller's branch reach and in the payment's own branch (the 2310 liability was booked to that branch and
   the clearing journal carries one branch), each taking at most its payable. Another customer's invoice is not allowed:
   the source does not say the money may follow a customer's group.
4. **GL.** `payment.reapplied` (rule rows in 0431) posts one journal per request, keyed `payment.reapplied:<batch>`,
   Dr 2310 `refund_payable` / Cr AR `ar` for the total.
5. **Reversed or bounced again.** The original is already reversed, so `ReversePayment` refuses it
   (`ErrAlreadyReversed`) and `MarkNSF` refuses a reversed payment; the applied slices are not payments, so there is
   nothing for either to take twice. An applied slice is final here: moving money back from an invoice into 2310 is a
   separate act, not built.
6. **Locks.** Payment row, then the invoice rows ascending (`FOR NO KEY UPDATE`), then the invoices counter the balance
   moves draw. No payments counter is drawn. Two concurrent applies of one held amount serialise on the payment row:
   the first lands, the second is refused with the held figure it finds.
7. **Dunning, ageing and credit hold do not yet net the held money** (FROM-PRINCIPAL 55.3). Every reader sums invoice
   balances (`agingSelect`, `v_ar_aging`, the reminder worklist, the credit limit and COD gates, the dispatch gate, the
   financial statements' ageing), and the reversal reopens the invoices, so a customer whose payment is reversed and
   held reads as owing it until it is re-applied or paid out. Netting is a policy choice (which invoice or bucket the
   held money offsets) across those readers, with a payments seam for the customer's held total; it is sized and left
   for a ruling.
8. **The register shows re-applied slices (SF-2r).** An invoice's Applied figure adds its slices and its timeline carries
   one applied entry per slice ("re applied from a reversed payment", with the payment it came from), read through the
   payments module's `ReappliedToInvoice` seam: the register never reads `payment_reapplications`. The Applied figure
   also counts a reversing row's contra allocation as negative, so a payment and its reversal net to zero (summed
   plainly it read twice the payment). The statement and the customer activity read the subledger row and already show
   a slice.
