// The purchasing areas' routes of the Buying and Stock stub, composed from one part per area. The
// main stub hands each request here after sign in; a part answers the routes it owns and returns
// true, or returns false and the main stub carries on. The world the parts share with the main stub
// (the orders, the vendors, the products) arrives through the kit.
import { makeLanded } from "./purch-landed.mjs";

export function createPurchRoutes(kit) {
  const parts = [makeLanded(kit)];
  return {
    reset() {
      for (const p of parts) p.reset();
    },
    async handle(ctx) {
      for (const p of parts) if (await p.handle(ctx)) return true;
      return false;
    },
  };
}
