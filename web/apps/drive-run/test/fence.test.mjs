// The money fence: the driver's shipped bytes and sources carry no money field
// and no money figure. The bundle is built the way the demo image builds it
// (demo mode, under the /drive/ prefix) into a scratch folder, and every file
// of it is scanned. The scanner is proven alive first: it must find a planted
// money word, and must not trip on CSS spacing, because a gate that passes
// vacuously is not a gate.
import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { MONEY_WORDS, describeHits, exemptBundleHit, scanPayload, scanText, textFiles } from "./_lib/fence.mjs";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");

test("the scanner finds every money word, a money figure, and passes CSS spacing", () => {
  for (const word of MONEY_WORDS) {
    assert.ok(scanText("probe.js", `const x = row.${word};`).length > 0, `the scanner missed ${word}`);
  }
  assert.ok(scanText("probe.js", 'label = "Total $12.50"').some((h) => h.kind === "figure"));
  assert.ok(scanText("probe.js", "const UNIT_PRICE = 2;").length > 0, "the scan is case blind");
  assert.deepEqual(scanText("probe.css", ".a{margin:0 auto;padding-top:4px;margin-bottom:8px}"), []);
  assert.ok(scanText("probe.js", "const margin = 0.2;").length > 0, "a field named margin still counts");
});

test("no source of the app names a money field or carries a money figure", () => {
  const hits = [];
  for (const [name, text] of textFiles(join(appDir, "src"), [".ts"])) hits.push(...scanText(`src/${name}`, text));
  assert.deepEqual(describeHits(hits), []);
});

test("the built bundle carries no money field and no money figure", () => {
  const out = mkdtempSync(join(tmpdir(), "drive-fence-"));
  try {
    execFileSync("npx", ["--no-install", "vite", "build", "--mode", "demo", "--base=/drive/", "--outDir", out, "--emptyOutDir"], {
      cwd: appDir,
      stdio: "pipe",
    });
    const files = textFiles(out, [".js", ".html", ".css"]);
    // The scan must have read the app itself, or it says nothing.
    const all = files.map(([, text]) => text).join("\n");
    assert.ok(files.some(([name]) => name.endsWith(".js")), "the build emitted no script");
    assert.ok(all.includes("drive-run-app"), "the built bundle does not hold the app: the scan would pass vacuously");
    assert.ok(all.includes("End of run"), "the built bundle does not hold the run screens");
    const hits = [];
    for (const [name, text] of files) hits.push(...scanText(name, text));
    assert.deepEqual(describeHits(hits.filter((h) => !exemptBundleHit(h))), []);
  } finally {
    rmSync(out, { recursive: true, force: true });
  }
});

test("the payload scan reads keys and money figures, and leaves a word inside a name alone", () => {
  assert.ok(scanPayload("p", JSON.stringify({ items: [{ stop: { value_cents: 5 } }] })).length > 0);
  assert.ok(scanPayload("p", JSON.stringify({ gauge: { freight_cents: 5 } })).length > 0);
  assert.ok(scanPayload("p", JSON.stringify({ note: "Collect $12.50 on delivery" })).some((h) => h.kind === "figure"));
  assert.deepEqual(scanPayload("p", JSON.stringify({ customer_name: "Crescents Pricewise (demo)", stop_no: 2 })), []);
  assert.deepEqual(scanPayload("p", JSON.stringify({ stop_lines: { a: [{ description: "Retaining wall block", qty: "84.000" }] } })), []);
});

test("the bundle exemptions are narrow: a money word outside them still fails", () => {
  // The engine chunk exemption names the engine's file, not every file.
  assert.equal(exemptBundleHit({ file: "assets/index-abc.js", kind: "word", word: "cost", count: 1, window: "estimatedCost=x" }), false);
  // The registry exemption names the registry's row, not the word alone.
  assert.equal(exemptBundleHit({ file: "assets/index-abc.js", kind: "word", word: "price", count: 1, window: "the price is on the ticket" }), false);
  assert.equal(exemptBundleHit({ file: "assets/index-abc.js", kind: "word", word: "price", count: 1, window: "price_matrix:`server_wins`" }), true);
});

test("the payload exemption is narrow: the caller's own cost_hidden flag is not money", () => {
  // The settings substrate's own layout flag: the user asked for the cost
  // column to be hidden. It hides money rather than carrying any, and the
  // contract requires it on every surface config answer.
  assert.deepEqual(scanPayload("config", JSON.stringify({ workspace: { cost_hidden: true } })), []);
  assert.deepEqual(scanPayload("config", JSON.stringify({ workspace: { cost_hidden: false } })), []);
  // The guard holds the shape narrow: the flag reads as money outside the
  // workspace view, and a flag that carries a figure is money.
  assert.ok(scanPayload("config", JSON.stringify({ cost_hidden: true })).length > 0, "a cost_hidden outside the workspace view still counts");
  assert.ok(scanPayload("config", JSON.stringify({ stop: { cost_hidden: true } })).length > 0, "a cost_hidden on a stop still counts");
  assert.ok(scanPayload("config", JSON.stringify({ workspace: { cost_hidden: "$12.50" } })).some((h) => h.kind === "figure"), "a cost_hidden carrying a figure still counts");
  assert.ok(scanPayload("config", JSON.stringify({ workspace: { cost: 5 } })).length > 0, "a cost field still counts");
  // The workspace view is the surface config answer's own top-level member (SettingsWorkspaceView), so an
  // object that is merely named workspace somewhere deeper is not it, and its flag still counts.
  assert.ok(scanPayload("config", JSON.stringify({ stop: { workspace: { cost_hidden: true } } })).length > 0, "a workspace nested under a stop still counts");
  assert.ok(scanPayload("config", JSON.stringify({ a: { b: { workspace: { cost_hidden: true } } } })).length > 0, "a workspace two levels deep still counts");
  assert.ok(scanPayload("config", JSON.stringify({ rows: [{ workspace: { cost_hidden: true } }] })).length > 0, "a workspace inside an array still counts");
});
