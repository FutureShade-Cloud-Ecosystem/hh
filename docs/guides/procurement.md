# Procurement Manager: the testers' brief

The Buying and Stock board. The full brief lives in the part 1 page, the testers' walk, on the demo box at the Buying and Stock app's "Testers' brief" link. The page's title is "Buying and Stock: the testers' brief" and it opens from the Buying and Stock app's own "Testers' brief" link (the same convention the Counter Till carries: a `public/testers.html` served at the app's prefix, in the till's case `/till/testers.html`).

## One line, in the desk's own words

The role reads the open purchase orders, the incoming list, and the reorder worklist, and drafts, places and tracks a purchase order. Receiving against an order is later (the yard's Yard Pick and Receive lands the goods; the procurement manager's desk is the screen that drafts and tracks the order, not the screen that closes the gap on the yard's count). Vendor bills are later (Payables and Receivables posts them). The procurement gap, by name, is K08, on P07 Granular A at Trenton.

## Where the part 1 page holds

- The sign-in: the card on the front door (per the lane's core branch, where the role is live; the demo branch's `roles.ts` is still behind the lane's front door push), the tile the card opens, the desk's tabs.
- The demo world: the three demo suppliers (VND-01 Granular supplier, VND-02 Paver supplier, VND-03 Edging and tools supplier), the two seeded POs (DEMO-TRT-P01 at Trenton on V2, DEMO-KGN-P02 at Kingston on V1), the ten demo products (P01 to P10 with the DEMO- prefix), the three shortfall rows (K08 on P07 Granular A at Trenton, P05 Step Slab at Trenton, P09 Black Cedar Mulch at Kingston).
- The home tiles: open purchase orders, drafts waiting, lines below reorder point, incoming.
- The reorder worklist: every line below its point, worst first, K08 hot on top.
- The K08 gap: the gap, by name, the picked-on-landing row, the row the buyer's draft covers first; the yard's K08 note is the app's own words.
- The draft flow: the form, the empty PO wall, the expected date's three checks (the picker, the format, the calendar), the place, the cancel.
- The register and incoming: the seeded POs on top, the relative day words, the branch fence held by the read.
- The walls: the order wall names who may place, edit, cancel; the Yard Manager's reach; the bill wall names who may post.
- Known issues: the world is dated, the restart signs out, the phone fits, beyond reads are walled.

Read the part 1 page in full on the demo box; every claim on it is backed by the demo seed, the app's own logic, or the demoseed procurement plan test on the lane.

## What is "later"

The part 1 page names the "later" tabs in the desk's own words: Receiving (later), Vendors (later), Vendor bills (later), the role's own card on the demo branch's `roles.ts` (the lane C core's front door push is pending).

## Where feedback goes

One channel: send everything to Colton, and he files every note with the principal in one place. What helps most: what you tapped, what you expected, and what you saw instead. The older demo's feedback path stays open too, but what you say about this box is what decides what gets built next.
