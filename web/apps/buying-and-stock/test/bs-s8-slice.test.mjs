// The source slicer the S8 source reading tests rely on: it takes a function by name, and a missing name fails
// loudly instead of widening or emptying the text under test. The fixture's templates are untagged on purpose:
// the design system's position gate reads every dependent's test text and refuses a tagged template it cannot parse.
import { test } from "node:test";
import assert from "node:assert/strict";
import { functionBody } from "./_slice.mjs";

const source = [
  "// ---- section ----",
  "function first(a: A): R {",
  "  return `<p>${a.map((x) => { return x; })}</p>`;",
  "}",
  "export function second(b: B): R {",
  "  return `two`;",
  "}",
].join("\n");

test("functionBody takes exactly the named function, nested braces and all", () => {
  assert.match(functionBody(source, "first"), /^\{\s+return `<p>\$\{a\.map/);
  assert.ok(!functionBody(source, "first").includes("two"), "the first body must stop at its own close");
  assert.ok(functionBody(source, "second").includes("two"));
});

test("functionBody throws by name when the function is missing or empty", () => {
  assert.throws(() => functionBody(source, "third"), /function third\( is not declared/);
  assert.throws(() => functionBody("function e(): void {}", "e"), /empty body/);
});
