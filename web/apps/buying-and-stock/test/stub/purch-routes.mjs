// The purchasing areas' routes of the Buying and Stock stub, composed from one part per area. The
// main stub hands each request here after sign in; a part answers the routes it owns and returns
// true, or returns false and the main stub carries on. The world the parts share with the main stub
// (the orders, the vendors, the products) arrives through the kit; the parts that lean on each
// other (a landed cost attached to a receipt, a send to a vendor's contact) are wired here.
import { makeBills } from "./purch-bills.mjs";
import { makeLanded } from "./purch-landed.mjs";
import { makeReceiving } from "./purch-receiving.mjs";
import { makeVendors } from "./purch-vendors.mjs";

export function createPurchRoutes(kit) {
  const landed = makeLanded(kit);
  const vendors = makeVendors(kit);
  const receiving = makeReceiving(kit, landed, vendors);
  // The landed part reads a receipt's lines through the kit, the receiving part writes the receipts.
  kit.receiptLines = receiving.receiptLines;
  const bills = makeBills(kit);
  const parts = [landed, receiving, vendors, bills];
  return {
    reset() {
      for (const p of parts) p.reset();
    },
    receiptsFor: receiving.receiptsFor,
    async handle(ctx) {
      for (const p of parts) if (await p.handle(ctx)) return true;
      return false;
    },
  };
}
