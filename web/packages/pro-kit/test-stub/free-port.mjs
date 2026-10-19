// Free loopback ports for a Playwright config, taken from the system at config load. A bind on port=0 lands
// inside the kernel's ephemeral range (32768..60999 on stock Linux, which also holds the Pro block 56800..56899)
// and so is a candidate source port for any outgoing connection: a connect() between the probe close and the
// Playwright webServer bind can grab the same number and the bind fails with EADDRINUSE. The hand-out band
// 29000..31999 sits below that range and above the console legs' fixed bases (28170..28189), so a probe there
// is a real bind on a number no outgoing socket will pick and no console leg will want.
// Each call walks the band from a hash-derived offset so two freePorts(N) calls in the same process do not
// collide on the starting point, and every port handed out is recorded in a process-level Set so a later call
// in the same process never revisits it (the kernel has released the port to the loopback pool by the time
// Playwright binds it; only the Set knows it's taken). The band is large enough (3000 numbers) that a single
// test process exhausting it is rare, so port=0 is a fallback, never the first choice.
import { createServer } from "node:net";

const BAND_LOW = 29000;
const BAND_HIGH = 31999;
const BAND_SIZE = BAND_HIGH - BAND_LOW + 1;
const handedOut = new Set();

function bindProbe(port) {
  return new Promise((resolve, reject) => {
    const probe = createServer();
    probe.once("error", reject);
    probe.listen(port, "127.0.0.1", () => {
      probe.close((err) => (err ? reject(err) : resolve()));
    });
  });
}

async function freePort() {
  // Hash the process id with the call index to spread the start of the
  // walk across the band; the handed-out Set still guarantees uniqueness
  // even if two calls hash to the same offset, because a revisit is
  // caught by the Set membership check below.
  const seed = ((process.pid * 0x9e3779b1) ^ (handedOut.size * 0x85ebca6b)) >>> 0;
  const start = seed % BAND_SIZE;
  for (let i = 0; i < BAND_SIZE; i += 1) {
    const candidate = BAND_LOW + ((start + i) % BAND_SIZE);
    if (handedOut.has(candidate)) continue;
    try {
      await bindProbe(candidate);
      handedOut.add(candidate);
      return String(candidate);
    } catch {
      // The probe refused; another suite or a stray socket holds that port. Try the next.
    }
  }
  // Band exhausted (a runner parking hundreds of ports, rare on a workstation): fall back to port=0,
  // the legacy behaviour. The Set still records this hand-out so a later call in the same process
  // does not re-allocate the same ephemeral slot before the first one's Playwright has bound it.
  return new Promise((resolve, reject) => {
    const probe = createServer();
    probe.once("error", reject);
    probe.listen(0, "127.0.0.1", () => {
      const { port } = probe.address();
      handedOut.add(port);
      probe.close(() => resolve(String(port)));
    });
  });
}

export async function freePorts(count) {
  const ports = [];
  for (let i = 0; i < count; i += 1) {
    ports.push(await freePort());
  }
  return ports;
}
