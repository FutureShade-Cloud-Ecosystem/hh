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

// How long the cleanup waits for the stub to close after its standard input
// ends. The wait has a bound of its own: a stub that ignores its standard
// input end would hold this leg open and keep its port for the rest of the
// machine's day, so the wait ends, the leg ends its own child and says so
// instead of hanging.
const STOP_TIMEOUT_MS = 5000;

let child;
let closed;
let bound;
let stderr = "";

// Whether a host is the loopback. The IPv4 loopback and the IPv6 loopback
// are the same machine, so both answer.
function isLoopbackHost(host) {
  return host === "127.0.0.1" || host === "::1";
}

// What the outside probe can prove on a host with the given outside
// addresses, and null when it can run. A host with no outside address has
// nothing for the probe to try, so the probe proves nothing there and has to
// say so rather than pass as if it had proved the port is unreachable off
// the loopback.
function outsideProbeNote(addresses) {
  if (addresses.length > 0) return null;
  return "this host has no outside IPv4 address, so the outside probe could not run: the bound host above is the only proof that the stub reached no other interface";
}

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

// Stops a stub through its own stop, and when that does not stop it within
// the bound, ends the child this leg spawned. The wait has a bound of its
// own: a stub that ignores its standard input end would hold this hook
// waiting with its listener up. The leg then ends its OWN child at runtime
// (child.kill() on the process it spawned) and waits for its exit, so no
// listener of the leg survives a failing run, and it reports the stub that
// would not stop.
async function stopStub(proc, exited, timeoutMs = STOP_TIMEOUT_MS) {
  proc.stdin.end();
  let timer;
  let stopped = false;
  try {
    stopped = await Promise.race([
      exited.then(() => true),
      new Promise((resolve) => {
        timer = setTimeout(() => resolve(false), timeoutMs);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
  if (stopped) return;
  proc.kill();
  await exited;
  proc.stdout?.destroy();
  proc.stderr?.destroy();
  throw new Error(
    `the stub did not close within ${timeoutMs} ms of its standard input ending, so the leg ended the child it spawned`,
  );
}

after(async () => {
  if (child !== undefined) await stopStub(child, closed);
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

test("the stub binds the loopback and no other interface", async (t) => {
  assert.ok(isLoopbackHost(bound.host), `the stub bound [${bound.host}]:${bound.port}`);
  assert.ok(bound.port > 0, `the stub bound no port: [${bound.host}]:${bound.port}`);
  // The loopback answer has to work, or "it does not answer off the
  // loopback" would hold for a dead server and prove nothing.
  assert.equal(
    await answers(bound.host, bound.port),
    true,
    `the stub does not answer on ${bound.host} at ${bound.port}`,
  );
  // The outside addresses are probed together: one answer at a time would
  // spend the probe timeout on each of them in turn.
  const outside = outsideAddresses();
  const note = outsideProbeNote(outside);
  if (note !== null) {
    t.diagnostic(note);
    return;
  }
  const reached = await Promise.all(outside.map((address) => answers(address, bound.port)));
  const answered = outside.filter((address, index) => reached[index]);
  assert.deepEqual(
    answered,
    [],
    `the stub answered off the loopback on: ${answered.join(", ")}`,
  );
});

test("the host check accepts the IPv6 loopback", () => {
  assert.equal(isLoopbackHost("127.0.0.1"), true, "the IPv4 loopback is the loopback");
  assert.equal(isLoopbackHost("::1"), true, "the IPv6 loopback is the loopback");
  assert.equal(isLoopbackHost("0.0.0.0"), false, "the wildcard address is not the loopback");
  assert.equal(isLoopbackHost("::"), false, "the wildcard IPv6 address is not the loopback");
});

test("the outside probe says so when the host has no outside address", () => {
  assert.match(
    String(outsideProbeNote([]) ?? ""),
    /no outside IPv4 address/,
    "the probe must say it could not run when the host holds no outside address",
  );
  assert.equal(
    outsideProbeNote(["192.168.1.10"]),
    null,
    "a host that holds outside addresses needs no note",
  );
});

// A scratch stub that binds the loopback, says so, ignores its standard input
// and ends itself after a while (so a failing run cannot leave it for good).
const SCRATCH_STUB = [
  'const s = require("node:net").createServer(() => {});',
  's.listen(0, "127.0.0.1", () => console.log("scratch listening on " + s.address().port));',
  "process.stdin.resume();",
  "setTimeout(() => process.exit(0), 12000);",
].join("\n");

// The port a scratch process names on its standard output.
function scratchPort(scratch) {
  return new Promise((resolve, reject) => {
    scratch.once("error", reject);
    scratch.stdout.on("data", (chunk) => {
      const found = /scratch listening on (\d+)/.exec(String(chunk));
      if (found !== null) resolve(Number(found[1]));
    });
  });
}

test("a stub that ignores its standard input is still ended by the leg's own cleanup", async () => {
  const scratch = spawn(process.execPath, ["-e", SCRATCH_STUB], { stdio: ["pipe", "pipe", "pipe"] });
  const exited = new Promise((resolve) => scratch.once("close", resolve));
  const port = await scratchPort(scratch);
  await assert.rejects(stopStub(scratch, exited, 500), /did not close within 500 ms of its standard input ending/);
  assert.notEqual(scratch.exitCode === null && scratch.signalCode === null, true, "the leg must end the stub it spawned");
  assert.equal(
    await answers("127.0.0.1", port),
    false,
    `the scratch stub's port ${port} must be gone, so no listener of the leg survives a failing run`,
  );
});

// Settles with the value of the promise, or with "hung" when it has not
// settled within the bound, so a leg that would wait for ever fails instead.
async function within(promise, ms) {
  let timer;
  try {
    return await Promise.race([
      promise.then(
        (value) => ({ value }),
        (error) => ({ error }),
      ),
      new Promise((resolve) => {
        timer = setTimeout(() => resolve("hung"), ms);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}

test("a scratch that cannot run is a failure and not a wait", async () => {
  const broken = spawn(process.execPath, ["-e", "const s = ;;"], { stdio: ["pipe", "pipe", "pipe"] });
  const settled = await within(scratchPort(broken), 3000);
  assert.notEqual(settled, "hung", "test 4's scratch must reject when the process exits without naming a port");
  assert.ok(settled.error instanceof Error, "the wait must reject");
});

// A scratch stub that traps SIGTERM and ignores its standard input. It names
// no port and listens nowhere, and ends itself after a while.
const TRAPPING_STUB = [
  'process.on("SIGTERM", () => {});',
  "process.stdin.resume();",
  'console.log("scratch listening on 1");',
  "setTimeout(() => process.exit(0), 8000);",
].join("\n");

test("a stub that traps SIGTERM fails the cleanup naming it, and the wait is bounded", async () => {
  const scratch = spawn(process.execPath, ["-e", TRAPPING_STUB], { stdio: ["pipe", "pipe", "pipe"] });
  const exited = new Promise((resolve) => scratch.once("close", resolve));
  await scratchPort(scratch);
  const started = Date.now();
  const settled = await within(stopStub(scratch, exited, 300, 300), 4000);
  assert.notEqual(settled, "hung", "the cleanup must not wait on a child that ignores the kill");
  assert.match(String(settled.error?.message ?? ""), /still running/);
  assert.match(String(settled.error?.message ?? ""), new RegExp(String(scratch.pid)));
  assert.ok(Date.now() - started < 4000, "the cleanup ended inside its bound");
  await exited;
});
