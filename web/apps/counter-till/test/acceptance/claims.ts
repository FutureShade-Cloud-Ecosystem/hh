// The page's claims and the run's proof of each: the one ledger between what
// the testers' page states about the till's behavior and what this acceptance
// run asserts. The claims spec holds the page to this ledger in both
// directions: a claim on the page with no entry here fails, and an entry with
// no claim on the served page fails, so the page can never quietly claim what
// the run does not prove, and a retired proof cannot outlive its sentence.
//
// Entries name the spec files of this run that carry the assertions; the run
// is green only when every one of them passed, so a covered claim is a proven
// claim. World facts the counter cannot enumerate through its own walls (the
// nine accounts behind the branch fence, nine in the seed's own integration
// suite on the goal branch) are covered as far as the product's API can see:
// the naming rule (every name carries (demo)) is asserted over everything the
// counter can reach.
export const CLAIM_COVERAGE: Readonly<Record<string, string>> = {
  "sign-in-roles": "10-world (the flows table) and 20-sales and 30-manager (the screens)",
  "coming-eleven": "20-sales and 30-manager (the way in, and beyond the till)",
  "world-invented": "10-world (the catalogue and the picker's naming)",
  "world-dated": "10-world (the day-keyed sale against the run's clock)",
  "engine-prices": "20-sales and 30-manager (a sale, no price field, the read compared)",
  "margin-both": "10-world (the glance as the clerk) and both screen specs (the column)",
  "no-floor": "20-sales and 30-manager (the contractor's line below the floor, rung)",
  "cash-change": "20-sales and 30-manager (cash with change due)",
  simulated: "20-sales and 30-manager (the notice on the tender and the receipt)",
  "account-balance": "20-sales (the account sale's invoice and fresh balance)",
  "return-within": "20-sales and 30-manager (the drawer's expected cash drops by it)",
  "refund-refused": "20-sales (the refusal with the amount and the approvers named)",
  "manager-card": "20-sales (the dialog, the receipt naming both, the header still the clerk's)",
  "manager-own": "30-manager (the same size of refund with no dialog)",
  "other-branch": "10-world and both screen specs (the lookup refused, nothing laid out)",
  "credit-refused": "20-sales and 30-manager (the refusal in words, cash and card intact)",
  "parked-branch": "20-sales and 30-manager (the list fenced, and a pickup across a sign-in)",
  "drawer-shared": "20-sales and 30-manager (the same shared drawer, found by the second login)",
  "known-seeded-card": "10-world (the seeded card sale answers not card refundable) and 20-sales (the method greyed with its reason)",
  restart: "90-restart (the pre restart bearer refused, a fresh sign in working)",
  "signout-reload": "20-sales (sign out returns the cards; a fresh page has no session)",
};
