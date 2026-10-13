// The sign in screen's plan: what the dealer's allowed methods put on the card. The board states
// (HH: Pro, shell-signin-phone-brand and build_signin_states.py): the demo set (email link and Google),
// every method, one method only, and a method the dealer refuses.
import { test } from "node:test";
import assert from "node:assert/strict";
import { signInPlan, METHOD_LABELS, DEMO_METHODS } from "../src/signin-methods.ts";

const labels = (plan) => plan.buttons.map((b) => b.label);

test("the demo dealer's set is the email link and Google, link first and primary", () => {
  assert.deepEqual(DEMO_METHODS, ["email_link", "google"]);
  const plan = signInPlan({ methods: DEMO_METHODS, dealer: "Gable Landscape Supply" });
  assert.equal(plan.state, "some");
  assert.equal(plan.email, true);
  assert.equal(plan.password, false);
  assert.deepEqual(labels(plan), ["Email me a sign in link", "Continue with Google"]);
  assert.deepEqual(plan.buttons.map((b) => b.primary), [true, false]);
  assert.equal(plan.divider, true);
  assert.equal(plan.hint, "Use the email your contractor account knows.");
  assert.match(plan.googleNote, /Google works where your Google email matches your contractor account email/);
  assert.equal(plan.refusal, null);
});

test("every method lists all four and says the dealer chooses", () => {
  const plan = signInPlan({ methods: ["google", "microsoft", "futurebuild", "email_link"], dealer: "Gable Landscape Supply" });
  assert.equal(plan.state, "every");
  assert.equal(plan.hint, "Gable Landscape Supply chooses the methods its contractors use.");
  assert.equal(plan.password, true);
  assert.deepEqual(labels(plan).sort(), ["Continue with Google", "Continue with Microsoft", "Email me a sign in link", "Sign in"].sort());
  assert.equal(plan.buttons.filter((b) => b.primary).length, 1);
});

test("one method only shows no alternatives and no divider", () => {
  const link = signInPlan({ methods: ["email_link"], dealer: "Gable Landscape Supply" });
  assert.equal(link.state, "one");
  assert.deepEqual(labels(link), ["Email me a sign in link"]);
  assert.equal(link.divider, false);
  assert.equal(link.hint, "This dealer allows one method.");
  const password = signInPlan({ methods: ["futurebuild"], dealer: "" });
  assert.deepEqual(labels(password), ["Sign in"]);
  assert.equal(password.password, true);
  assert.equal(password.email, true);
});

test("a refused method names the dealer and the methods it does allow", () => {
  const plan = signInPlan({ methods: DEMO_METHODS, refused: "microsoft", dealer: "Gable Landscape Supply" });
  assert.equal(plan.state, "refused");
  assert.equal(plan.refusal, "Gable Landscape Supply does not allow sign in with Microsoft. Use your email link or Google.");
  assert.deepEqual(labels(plan), ["Email me a sign in link", "Continue with Google"]);
});

test("a dealer with no name reads as your dealer, and unknown or repeated methods fall away", () => {
  const plan = signInPlan({ methods: ["google", "google", "nonsense", "email_link"], refused: "microsoft", dealer: "" });
  assert.equal(plan.refusal, "Your dealer does not allow sign in with Microsoft. Use your email link or Google.");
  assert.equal(plan.buttons.length, 2);
});

test("nothing allowed reads as an empty card in plain words, never a blank", () => {
  const plan = signInPlan({ methods: [], dealer: "Gable Landscape Supply" });
  assert.equal(plan.buttons.length, 0);
  assert.match(plan.hint, /allows no way to sign in/i);
});

test("no word on the card names the machinery", () => {
  const all = ["google", "microsoft", "futurebuild", "email_link"];
  const words = [...Object.values(METHOD_LABELS)];
  for (const refused of [null, ...all]) {
    const plan = signInPlan({ methods: all, refused, dealer: "Gable Landscape Supply" });
    words.push(plan.hint, plan.googleNote, plan.refusal ?? "");
  }
  for (const w of words) assert.doesNotMatch(w, /console|core|token|session|oauth|jwt/i, w);
});
