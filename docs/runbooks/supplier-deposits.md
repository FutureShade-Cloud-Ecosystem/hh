# Supplier deposits: the release, the nightly sweep and returns

No returns or AR runbook existed in the repository, so this is the first page of one. It covers the
supplier deposit on a special order (the SUPPLIER_DEPOSIT invoice beside the rest invoice).

## When the deposit is recognised

The deposit sits in 2300 Customer Deposits until the goods leave. It is released once (DR 2300, CR 4000,
journal key `deposit.released:<deposit invoice id>`) when a delivery road records the goods leaving
(dispatch handoff, will call confirmation, the ring), or when any return, partial or whole, is
committed on the order. Whichever comes first posts the journal; the other dedupes on the key.

A return gives the customer back the returned goods' value through the ordinary return and store credit
path. It never hands back a share of the deposit.

## A cash or card refund leaves the deposit invoice open

The cash and card refund rails do not touch the customer's invoices. An on account customer who is
refunded in cash or to a card keeps any open deposit invoice (and any open rest invoice), exactly as
with any unpaid invoice. Revenue and 2300 are right either way; the open invoice is for the desk to
void or credit if the customer is not to be billed for it.

## The nightly sweep

The job `ar.credit_memo_repost` also heals a deposit release whose emission was lost after a delivery.
It reads the live deposit documents a page at a time, asks the ledger which release journals already
exist, asks the order module which orders recorded a delivery, and emits the release for the rest. The
run detail in `job_runs` carries:

- `deposit_candidates`: deposit documents with no release journal that the sweep looked at
- `deposit_released`: documents the sweep emitted a release for
- `deposit_failed`: emissions that refused (found again the next night)
- `deposit_first_error`: the first refusal's message, present only when something failed

A failed release at delivery time is only a warning in the log until the sweep runs, a delay of up to
one night. Look at these keys first.

Backfill: orders delivered before the delivery stamp existed carry no `delivered_at` and are not
healed by the sweep. There is no prior emission to heal for them.
