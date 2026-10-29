// The acceptance-real teardown: the box box.sh started is removed however
// the run ended. Idempotent, so a teardown after a failed `up` is quiet.
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

export default function teardown() {
  const script = fileURLToPath(new URL("./box.sh", import.meta.url));
  try {
    execFileSync("bash", [script, "down"], { stdio: "inherit" });
  } catch {
    // The box is already down: nothing to remove.
  }
}
