// Unit leg of the Projects surface against the shared tenant stub: every
// procurement project of the account in one flat list, the QUOTING ones
// included (the Board keeps only the planning projects, so this list is the
// one place a quoting project stands, and the only way back to Recall). Each
// card carries the kit's status chip, the line count, and the indicative
// subtotal only past PLANNING (the scope page's rule), and opens the scope
// page as having come from Projects.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { projectsLoad, projectsView, PROJECTS_EMPTY } from "../src/projects.ts";
import { InMemoryTenantSession, tenantClient, stubAdapter, money } from "@hardscapehouse/pro-kit";
import { PERSONAS, STUB_PASSWORD } from "@hardscapehouse/pro-kit/test-stub/tenant-data.mjs";
import { spawnStub } from "@hardscapehouse/pro-kit/test-stub/spawn-stub.mjs";

let stub;
let base;

before(async () => {
  stub = await spawnStub();
  base = stub.base;
});

after(async () => {
  await stub.stop();
});

async function pageFor(who) {
  const session = new InMemoryTenantSession({ adapter: stubAdapter({ baseUrl: base }), baseUrl: base });
  const signed = await session.signIn(PERSONAS[who].email, STUB_PASSWORD);
  assert.equal(signed.ok, true, who);
  return {
    session,
    client: tenantClient(session, { baseUrl: base }),
    route: { surface: "projects" },
    busy: false,
    field: () => null,
    goto: () => {},
    refresh: () => {},
    say: () => {},
    hold: () => {},
    touch: () => {},
    assign: () => {},
    openModal: () => {},
    closeModal: () => {},
  };
}

function words(template) {
  if (template === null || template === undefined || template === false) return "";
  if (Array.isArray(template)) return template.map(words).join(" ");
  if (typeof template === "object" && "strings" in template) {
    return template.strings.map((s, i) => s + (i < template.values.length ? words(template.values[i]) : "")).join("");
  }
  return String(template);
}

test("a role without submit_rfq is refused before any call is made", async () => {
  const page = await pageFor("field");
  const answer = await projectsLoad(page);
  assert.equal(answer.kind, "refusal");
});

test("the list reads every project of the account, the QUOTING ones included", async () => {
  const page = await pageFor("owner");
  const answer = await projectsLoad(page);
  assert.equal(answer.kind, "ready", JSON.stringify(answer));
  if (answer.kind !== "ready") return;
  assert.equal(answer.data.length, 4, JSON.stringify(answer.data.map((p) => p.name)));
  const names = answer.data.map((p) => p.name);
  assert.ok(names.includes("Santa Fe courtyard stone"), "the QUOTING project is missing from the list");
  assert.ok(names.includes("La Jolla terrace pavers"));
  assert.ok(names.includes("Point Loma retaining wall"));
  assert.ok(names.includes("Backyard planting beds"));
});

test("each card carries its status word and line count, and the indicative subtotal only past PLANNING", async () => {
  const page = await pageFor("buyer");
  const answer = await projectsLoad(page);
  assert.equal(answer.kind, "ready");
  if (answer.kind !== "ready") return;
  const raw = words(projectsView(answer.data, page));
  const text = raw.replace(/<[^>]*>/g, " ");
  // Every project is named, each with the kit's status word, never the stored string.
  for (const name of ["La Jolla terrace pavers", "Santa Fe courtyard stone", "Point Loma retaining wall", "Backyard planting beds"]) {
    assert.ok(text.includes(name), text);
  }
  assert.ok(text.includes("Planning"), text);
  assert.ok(text.includes("Quoting"), text);
  assert.ok(text.includes("Ordered"), text);
  assert.ok(/\b3 lines\b/.test(text), `the line count is missing: ${text}`);
  // The scope page's rule: a PLANNING project awaits its price; past PLANNING the indicative subtotal shows.
  assert.ok(text.includes("Awaiting price"), text);
  assert.ok(text.includes("Indicative"), text);
  const quoting = answer.data.find((p) => p.status === "QUOTING");
  assert.ok(quoting.subtotal_cents > 0);
  assert.ok(text.includes(money(quoting.subtotal_cents)), "the quoting project's indicative subtotal is missing");
  // A card opens the scope page as having come from Projects.
  assert.match(raw, /data-action="open-scope" data-project="[^"]+" data-from="projects"/);
  assert.doesNotMatch(text, /\b[A-Z]{2,}_[A-Z]{2,}\b/, "a stored status string reached the screen");
  assert.doesNotMatch(text, /undefined|null|NaN|!/);
});

test("an empty account shows the surface's own empty words", async () => {
  const page = await pageFor("owner");
  await fetch(`${base}/_stub/control`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ empty: "projects" }) });
  const answer = await projectsLoad(page);
  assert.equal(answer.kind, "empty");
  assert.match(PROJECTS_EMPTY, /^No projects yet/);
  await fetch(`${base}/_stub/reset`, { method: "POST" });
});
