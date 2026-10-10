// The page's claims and the run's proof of each: the one ledger between what
// the testers' page states about the till's behavior and what this acceptance
// run asserts. Each claim names its proofs as a spec file and the EXACT test
// title inside it, and the claims spec holds three things together: the
// page's id set, this ledger's key set, and the suite's own titles, so a
// claim with no proof fails, a proof naming a test that does not exist fails,
// and a retired proof cannot outlive its sentence.
//
// World facts the counter cannot enumerate through its own walls (the nine
// accounts behind the branch fence) are covered as far as the product's API
// can see: the naming rule is asserted over everything the counter reaches,
// and the on-hold account's absence from this counter is itself asserted (it
// sits behind the branch fence, which is the wall the returns desk shows).
export interface ClaimProof {
  file: string;
  test: string;
}

export const CLAIM_COVERAGE: Readonly<Record<string, readonly ClaimProof[]>> = {
  "sign-in-roles": [
    { file: "10-world.pw.ts", test: "the way in's data: seven flows with role codes and labels" },
    { file: "20-sales.pw.ts", test: "the way in: two cards, the eleven coming roles untapped, and the banner" },
    { file: "30-manager.pw.ts", test: "the way in: the manager's card opens the same till under its own name" },
  ],
  "coming-eleven": [
    { file: "20-sales.pw.ts", test: "the way in: two cards, the eleven coming roles untapped, and the banner" },
    { file: "30-manager.pw.ts", test: "beyond the till: the manager's reach past the counter's walls, and the cards come back" },
  ],
  "world-invented": [
    { file: "10-world.pw.ts", test: "the catalogue's ten demo products answer, every name carrying (demo)" },
    { file: "10-world.pw.ts", test: "the account picker answers the branch's demo accounts, every name carrying (demo)" },
  ],
  "world-dated": [
    { file: "10-world.pw.ts", test: "the world's newest rows are dated to the moment the box was rebuilt" },
  ],
  "engine-prices": [
    { file: "20-sales.pw.ts", test: "a sale: lines priced by the engine, no price field, and the margin column refuses the clerk" },
    { file: "30-manager.pw.ts", test: "a sale: the same engine prices, the manager's margin column, no price field" },
  ],
  // P3-10 (the comprehensive train review), with row 651's flip: the margin
  // surface follows the policy word /pos/policy answers (this box: MANAGER_UP)
  // and the glance honours the same word server-side since the hardening
  // train, so the clerk's glance answers the empty document. The world leg
  // holds both halves of the word, the manager leg holds the allowed surface,
  // and the clerk leg holds the refusal.
  "margin-policy": [
    { file: "10-world.pw.ts", test: "the clerk's margin glance is refused, the manager's answers: the policy word names who sees cost and margin" },
    { file: "30-manager.pw.ts", test: "a sale: the same engine prices, the manager's margin column, no price field" },
    { file: "20-sales.pw.ts", test: "a sale: lines priced by the engine, no price field, and the margin column refuses the clerk" },
  ],
  "no-floor": [
    { file: "20-sales.pw.ts", test: "a contractor's pavers: the account price rings, and a below floor line rings too" },
    { file: "30-manager.pw.ts", test: "a contractor's pavers: the account price the clerk sees, and the margin column the policy allows the manager" },
  ],
  "credit-refused": [
    { file: "20-sales.pw.ts", test: "an account that cannot carry the sale on account: refused in words, nothing rung" },
    { file: "30-manager.pw.ts", test: "an account that cannot carry the sale on account: the same words to the manager" },
  ],
  "cash-change": [
    { file: "20-sales.pw.ts", test: "tender and receipt: cash with change due, held against the core's own read" },
  ],
  simulated: [
    { file: "20-sales.pw.ts", test: "tender and receipt: a card sale says no card was charged, on the tender and the receipt" },
    { file: "30-manager.pw.ts", test: "tender and receipt: a card sale under the manager carries the same simulated notice" },
  ],
  "account-balance": [
    { file: "20-sales.pw.ts", test: "tender and receipt: an account sale bills the account, invoice and fresh balance named" },
  ],
  "parked-branch": [
    { file: "20-sales.pw.ts", test: "parked sales: the branch's list, a park and a pickup, never the other branch's" },
    { file: "30-manager.pw.ts", test: "parked sales: a ticket parks and either role picks it up, at the same counter" },
  ],
  "return-customer-first": [
    { file: "20-sales.pw.ts", test: "a two order return: lines from both orders, one credit note in cash, and the drawer drops by its total" },
  ],
  "return-walk-in": [
    { file: "20-sales.pw.ts", test: "a walk in return: found by its receipt number, cash or card only, and the drawer drops by its total" },
  ],
  "return-swap": [
    { file: "20-sales.pw.ts", test: "a swap: the credit pays a new order, the leftover goes back by cash, both documents name each other" },
  ],
  "other-branch": [
    { file: "10-world.pw.ts", test: "a Kingston sale is refused at this counter: the lookup, its ticket, and all three refund rails" },
  ],
  "role-matrix": [
    { file: "10-world.pw.ts", test: "the walls the box holds: the role matrix, and the price rule write beside it" },
    { file: "30-manager.pw.ts", test: "beyond the till: the manager's reach past the counter's walls, and the cards come back" },
  ],
  "beyond-reads": [
    { file: "10-world.pw.ts", test: "the reads a technical tester reaches: the manager's reach the clerk's lacks, none fenced to a branch" },
  ],
  "drawer-shared": [
    { file: "20-sales.pw.ts", test: "the drawer: the branch's MAIN counter drawer, found open with the till's own float" },
    { file: "30-manager.pw.ts", test: "the drawer: the same shared counter drawer the clerk found" },
  ],
  "known-seeded-card": [
    { file: "10-world.pw.ts", test: "the seeded card sale carries no card reference, so the desk cannot refund it by card" },
  ],
  restart: [
    { file: "90-restart.pw.ts", test: "the pre restart bearer is refused, and a fresh sign in works" },
  ],
  "signout-reload": [
    { file: "20-sales.pw.ts", test: "beyond the till: sign out, a reload, and a fresh page each leave the till nobody's" },
  ],
};
