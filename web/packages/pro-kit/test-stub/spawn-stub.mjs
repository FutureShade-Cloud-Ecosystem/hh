// Starts the shared tenant stub as a child process on an ephemeral port the stub itself reports, so no two suites ever
// contend for a port. The stop door is the stub's own POST /_stub/exit (nothing is ever signalled); stop() awaits the
// child's close, so no process outlives the test that started it.
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";

const stubFile = fileURLToPath(new URL("./tenant-stub.mjs", import.meta.url));

export async function spawnStub() {
  const child = spawn(process.execPath, [stubFile], {
    env: { ...process.env, ROLE_STUB_PORT: "0", ROLE_STUB_IDLE_EXIT_MS: "120000" },
    stdio: ["ignore", "pipe", "ignore"],
  });
  const closed = new Promise((resolve) => child.once("close", resolve));
  let said = "";
  const port = await new Promise((resolve, reject) => {
    const fail = (why) => reject(new Error(`the stub did not start: ${why}`));
    child.once("error", (e) => fail(e.message));
    child.once("close", (code) => fail(`it exited with ${code}`));
    child.stdout.on("data", (chunk) => {
      said += chunk;
      const m = /tenant-stub listening on (\d+)/.exec(said);
      if (m) resolve(Number(m[1]));
    });
  });
  const base = `http://127.0.0.1:${port}`;
  for (let i = 0; i < 100; i += 1) {
    try {
      if ((await fetch(`${base}/healthz`)).ok) break;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 50));
  }
  return {
    base,
    port,
    async stop() {
      await fetch(`${base}/_stub/exit`, { method: "POST" }).catch(() => {});
      await closed;
    },
  };
}
