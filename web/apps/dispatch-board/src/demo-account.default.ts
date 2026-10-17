// The default build's demo account module: the seam between the demo-compiled
// mode and the default build that has no demo handoff. The rule: every build
// except the demo mode resolves this empty stub in place of src/demo-account.ts
// (vite.config.ts does the swap), so the key name and the reader never enter
// a default build's module graph whatever the tree shaker keeps.
// The stub keeps the real module's two callable shapes: absent is the honest
// read, and the clear drops a key that cannot exist there.
import type { FrontDoorHandoffRead } from "./demo-account.js";

/** The default build's reader, compiled out: absent is the honest answer here. */
export function readFrontDoorHandoff(): FrontDoorHandoffRead {
  return { kind: "absent" };
}

/** The default build's drop, compiled out: nothing was ever read here. */
export function clearFrontDoorHandoff(): void {}
