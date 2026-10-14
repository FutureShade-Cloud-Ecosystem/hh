// Free loopback ports for a Playwright config, taken from the system at config load. A fixed port inside the system's
// ephemeral range (32768 to 60999, which holds the Pro block 56800 to 56899) is also handed out as the source port of
// some other suite's client connection, so a fixed pair fails now and then with EADDRINUSE when two suites overlap.
// A config sets its variables with `process.env.NAME ??= port`: the main process decides once, the workers inherit
// it, and an inline value still wins.
import { createServer } from "node:net";

export async function freePorts(count) {
  const held = [];
  try {
    for (let i = 0; i < count; i += 1) {
      const probe = createServer();
      await new Promise((resolve, reject) => {
        probe.once("error", reject);
        probe.listen(0, "127.0.0.1", resolve);
      });
      held.push(probe);
    }
    return held.map((probe) => String(probe.address().port));
  } finally {
    await Promise.all(held.map((probe) => new Promise((resolve) => probe.close(resolve))));
  }
}
