// The Money Desk stub's bound address, read from the stub itself.
//
// The static gate (web/scripts/stubs-bind-loopback.node-test.mjs) reads the
// source; this leg reads the truth. The stub is started the way its own
// Playwright webServer starts it (the same script, the same ROLE_STUB_PORT),
// the address it bound is asked for, and the answer must be the loopback.
//
// The harm of a stub that binds every interface is reach: it answers on the
// LAN as well as on the loopback, so this leg also proves the port does not
// answer on this host's own outside address.
//
// The stop is the stub's own: its standard input ends (ROLE_STUB_STDIN_EXIT),
// never a signal.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { connect } from "node:net";
import { networkInterfaces } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");

// How long the stub gets to name the address it bound. A stub that starts
// and says nothing must fail this leg, not hang it: a leg that hangs holds
// the CI job open instead of reporting what went wrong.
const START_TIMEOUT_MS = 30000;

// How long a single probe waits for an answer.
const PROBE_TIMEOUT_MS = 1500;

let child;
let closed;
let bound;
let stderr = "";

before(async () => {
  // The same command the Playwright webServer runs, in the same directory,
  // with the port the kernel hands out (a port 0 bind never collides, and
  // the stub reports the port it got).
  child = spawn(process.execPath, [join("test", "stub", "money-desk-stub.mjs")], {
    cwd: appDir,
    env: { ...process.env, ROLE_STUB_PORT: "0", ROLE_STUB_STDIN_EXIT: "1" },
    stdio: ["pipe", "pipe", "pipe"],
  });
  closed = new Promise((resolve) => child.once("close", resolve));
  child.stderr.on("data", (chunk) => {
    stderr += chunk;
  });
  let said = "";
  bound = await new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      reject(
        new Error(
          `the stub named no bound address in ${START_TIMEOUT_MS} ms; it said: ${said || "(nothing)"}; it wrote to stderr: ${stderr || "(nothing)"}`,
        ),
      );
    }, START_TIMEOUT_MS);
    child.once("error", (err) => {
      clearTimeout(timer);
      reject(err);
    });
    child.once("close", (code) => {
      clearTimeout(timer);
      reject(
        new Error(
          `the stub exited with ${code} before it named its address; it wrote to stderr: ${stderr || "(nothing)"}`,
        ),
      );
    });
    child.stdout.on("data", (chunk) => {
      said += chunk;
      const found = /money-desk-stub listening on \[([^\]]+)\]:(\d+)/.exec(said);
      if (found !== null) {
        clearTimeout(timer);
        resolve({ host: found[1], port: Number(found[2]) });
      }
    });
  });
});

after(async () => {
  child?.stdin.end();
  await closed;
});

// The host's own addresses that are not the loopback: the ones the LAN
// reaches.
function outsideAddresses() {
  const out = [];
  for (const list of Object.values(networkInterfaces())) {
    for (const entry of list ?? []) {
      if (entry.family !== "IPv4" || entry.internal) continue;
      out.push(entry.address);
    }
  }
  return out;
}

// Whether the port answers on an address. A refusal and a silence both mean
// the port is not reachable there.
function answers(host, port) {
  return new Promise((resolve) => {
    const socket = connect({ host, port });
    socket.setTimeout(PROBE_TIMEOUT_MS, () => {
      socket.destroy();
      resolve(false);
    });
    socket.once("connect", () => {
      socket.destroy();
      resolve(true);
    });
    socket.once("error", () => {
      socket.destroy();
      resolve(false);
    });
  });
}

test("the stub binds the loopback and no other interface", async () => {
  assert.equal(bound.host, "127.0.0.1", `the stub bound [${bound.host}]:${bound.port}`);
  assert.ok(bound.port > 0, `the stub bound no port: [${bound.host}]:${bound.port}`);
  // The loopback answer has to work, or "it does not answer off the
  // loopback" would hold for a dead server and prove nothing.
  assert.equal(
    await answers("127.0.0.1", bound.port),
    true,
    `the stub does not answer on 127.0.0.1 at ${bound.port}`,
  );
  // The outside addresses are probed together: one answer at a time would
  // spend the probe timeout on each of them in turn.
  const outside = outsideAddresses();
  const reached = await Promise.all(outside.map((address) => answers(address, bound.port)));
  const answered = outside.filter((address, index) => reached[index]);
  assert.deepEqual(
    answered,
    [],
    `the stub answered off the loopback on: ${answered.join(", ")}`,
  );
});
