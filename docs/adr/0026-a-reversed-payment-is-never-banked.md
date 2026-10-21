# ADR-0026: a reversed payment is never banked

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

## Consequences

- A refused write changes no row, so the sync feed re-serves nothing for it (the trigger of ADR 0025 draws only when
  a row changes).
- A core message that a web package reads is a contract: the Money Desk's stub contract test finds each sentence in the
  core's source, so a message change runs every web contract test and stub that reads it.
- What the ledger shows after a payment is banked and then reversed (the reversing journal of a banked payment) is a
  separate question, outside this record (SF-2c).
