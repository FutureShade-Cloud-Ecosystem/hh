// Gap 9 of the per role homes: the landing's sentence and its link strip differ
// by role, and the core's profile name decides which role it is. A payables
// clerk never gets Shortages; a yard manager never gets Approvals they cannot
// decide. Words and order only: the core still gates every read.
import { test } from "node:test";
import assert from "node:assert/strict";
import { roleWords, ROLE_PROFILES } from "../src/role-words.ts";

const PROFILES = [
  "manager.yard", "manager.logistics", "manager.branch", "manager.sales",
  "manager.procurement", "manager.payables", "manager.controller", "manager.general",
];

test("all eight manager profiles have their own sentence, and no two share one", () => {
  assert.deepEqual([...ROLE_PROFILES].sort(), [...PROFILES].sort());
  const sentences = PROFILES.map((p) => roleWords(p).sub);
  assert.equal(new Set(sentences).size, 8);
  for (const s of sentences) assert.notEqual(s, "Your day at a glance.");
});

test("the link strip is the role's own: payables has no Shortages, the yard has no Approvals", () => {
  assert.ok(!roleWords("manager.payables").views.includes("shortages"));
  assert.ok(roleWords("manager.payables").views.includes("receiving"));
  assert.ok(!roleWords("manager.yard").views.includes("approvals"));
  assert.ok(roleWords("manager.yard").views.includes("shortages"));
  assert.ok(roleWords("manager.procurement").views.includes("shortages"));
  assert.ok(roleWords("manager.procurement").views.includes("receiving"));
  assert.ok(roleWords("manager.controller").views.includes("approvals"));
  assert.ok(roleWords("manager.sales").views.includes("approvals"));
  assert.ok(!roleWords("manager.branch").views.includes("approvals"));
  assert.ok(roleWords("manager.logistics").views.includes("drivers"));
});

test("a profile the table does not know keeps the plain sentence and the whole strip", () => {
  for (const profile of ["manager", "admin", "", "manager.nobody"]) {
    const words = roleWords(profile);
    assert.equal(words.sub, "Your day at a glance.");
    assert.deepEqual(words.views, ["shortages", "receiving", "approvals", "drivers", "warranty", "quick-actions"]);
  }
});

test("every strip lists only views the app owns, in the strip's own order, each once", () => {
  const order = ["shortages", "receiving", "approvals", "drivers", "warranty", "quick-actions"];
  for (const p of PROFILES) {
    const views = roleWords(p).views;
    assert.equal(new Set(views).size, views.length);
    const positions = views.map((v) => order.indexOf(v));
    assert.ok(positions.every((i) => i >= 0), p);
    assert.deepEqual([...positions].sort((a, b) => a - b), positions, `${p} keeps the strip's order`);
  }
});
