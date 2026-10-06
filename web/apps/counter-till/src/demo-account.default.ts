// The default build's demo account module (HH-DEMO-1 D2 fix, decision log
// rows 893 and 899). The seam rule: the front door's handoff is read only
// where the demo is compiled in, so every build mode except the demo mode
// resolves this stub in place of src/demo-account.ts (vite.config.ts does
// the swap). The real reader, its validation and the key name it reads
// never enter a default build's module graph at all, whatever the tree
// shaker keeps; app-root.ts's demo mode gate holds the call itself out of
// the default build the same way, and test/no-default-adopt.test.mjs reads
// the built files to prove both halves. The stub keeps the real module's
// two callable shapes: absent is the honest read (the till's ordinary
// direct visit), and the clear drops a key that cannot exist there.
import type { FrontDoorHandoffRead } from "./demo-account.js";

/** The demo build's reader, compiled out: absent is the honest answer here. */
export function readFrontDoorHandoff(): FrontDoorHandoffRead {
  return { kind: "absent" };
}

/** The demo build's drop, compiled out: nothing was ever read here. */
export function clearFrontDoorHandoff(): void {}
