// UX-127: the words "Demo data only." are written only by the shared note, which a console box drops.
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { demoNote } from "../src/band-identity.ts";

test("the demo note is there for no served config and gone for a console config", () => {
  assert.equal(demoNote(null), " Demo data only.");
  assert.equal(demoNote({ dealer: { name: "Acme Stone" } }), "");
});

test("the lien and compliance pages write the words only through the note", () => {
  for (const file of ["../src/lien/lien-views.ts", "../src/compliance/compliance-views.ts"]) {
    const source = readFileSync(new URL(file, import.meta.url), "utf8");
    assert.equal(source.includes("Demo data only"), false, file);
    assert.ok(source.includes("demoNote("), file);
  }
});
