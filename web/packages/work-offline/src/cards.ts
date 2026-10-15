// A conflict card in words. The store records what the server answered to a
// queued write (a merge, a newer server row, a case for a person, a refusal);
// the desk shows each as a card a person reads and dismisses. These are the
// words, kept in one place so the desk apps say the same thing.
import type { Card } from "@hardscapehouse/offline-store";

export interface CardWords {
  title: string;
  body: string;
}

const NOUN: Readonly<Record<string, string>> = {
  quotes: "quote",
  purchase_orders: "purchase order",
  cycle_counts: "count",
};

export function cardWords(card: Card): CardWords {
  const noun = NOUN[card.scope] ?? "record";
  switch (card.kind) {
    case "merge_report":
      return {
        title: `A ${noun} change was merged`,
        body: `Your change to a ${noun} was merged field by field with a newer change from the desk. Look the ${noun} over to see what it holds now.`,
      };
    case "superseded":
      return {
        title: `The ${noun} had moved on`,
        body: `The server already held a newer ${noun}, so your change was not applied. The server's version stands.`,
      };
    case "review":
      return {
        title: `A ${noun} needs a person`,
        body: `Your change and the server's version could not be joined by rule. Nothing was overwritten: open the ${noun} and decide.`,
      };
    case "rejected":
      return {
        title: `The server did not take the ${noun} change`,
        body: card.error !== null && card.error !== "" ? card.error : `The server refused the ${noun} change.`,
      };
  }
}
