// UX-127 and UX-128: the words "Demo data only." are written only by the shared note, which a console box drops.
// The liens and compliance pages are rendered with and without a console config and the words are asserted.
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { demoNote } from "../src/band-identity.ts";
import { loadAppRuntime } from "@hardscapehouse/pos-kit/src/ported/boot-custody.js";
import { renderLiens } from "../src/lien/lien-views.ts";
import { renderCompliance } from "../src/compliance/compliance-views.ts";

const CONSOLE = {
  mode: "console",
  org: "gable",
  consoleEndpoint: "https://console.example.test/v1",
  consoleProjectId: "fake-project",
  coreBaseUrl: "https://core.example.test",
  personas: false,
  personasOpen: false,
  dealer: { name: "Acme Stone" },
};
const served = (body) => async () => new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
const missing = async () => new Response("", { status: 404 });

/** A template as the words it prints, tags dropped. */
function words(value) {
  if (value === null || value === undefined || typeof value === "boolean") return "";
  if (Array.isArray(value)) return value.map(words).join("");
  if (typeof value === "object" && "strings" in value) {
    return value.strings.map((s, i) => s + (i < value.values.length ? words(value.values[i]) : "")).join("");
  }
  return typeof value === "symbol" ? "" : String(value);
}
const text = (value) => words(value).replace(/<[^>]*>/g, "").replace(/\s+/g, " ");

const lienModel = { phase: "loading", tab: "triage" };
const complianceModel = { phase: "loading", tab: "calendar" };

test("the demo note is there for no served config and gone for a console config", () => {
  assert.equal(demoNote(null), " Demo data only.");
  assert.equal(demoNote({ dealer: { name: "Acme Stone" } }), "");
});

test("the liens and compliance pages end at their own full stop on a console box", async () => {
  await loadAppRuntime(served(CONSOLE));
  const liens = text(renderLiens(lienModel));
  const compliance = text(renderCompliance(complianceModel));
  assert.match(liens, /Holdback, release and lien documents for the jobs we are on\. ?(Loading the liens\.)?\s*$/);
  assert.doesNotMatch(liens, /Demo data only/);
  assert.doesNotMatch(compliance, /Demo data only/);
  assert.match(compliance, /the review status of the forms\./);
  assert.doesNotMatch(compliance, /forms\.\s+Demo/);
});

test("the liens and compliance pages say Demo data only on a demo page", async () => {
  await loadAppRuntime(missing);
  assert.match(text(renderLiens(lienModel)), /Holdback, release and lien documents for the jobs we are on\. Demo data only\./);
  assert.match(text(renderCompliance(complianceModel)), /the review status of the forms\. Demo data only\./);
});

test("band-identity is imported once in the app root", () => {
  const root = readFileSync(new URL("../src/app-root.ts", import.meta.url), "utf8");
  assert.equal(root.match(/from "\.\/band-identity\.js"/g)?.length, 1);
});
