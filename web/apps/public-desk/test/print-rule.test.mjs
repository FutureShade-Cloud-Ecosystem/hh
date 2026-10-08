// The printed quote's own pin (round 2, FROM-PRINCIPAL 9): the dealer header's rule stays
// in the document's family ON PAPER. The rule's colour rides the --doc-ink alias from the
// screen cascade, and a print path that re-resolves custom properties can drop a border
// back to currentColor, the near-black text ink: that is the black rule the principal's
// live check saw. The print half therefore STATES the family itself, in an ordinary
// longhand inside a print media block: quote blue for a quote, the gold ink for the
// converted order, never a grey or black stand-in. This gate reads the shipped source and
// fails when the print block is missing, reworded to a literal, or pointed anywhere but
// the family tokens; the browser leg in test/page.pw.ts pins the computed colour under
// print emulation against the same tokens.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const SRC = readFileSync(join(here, "..", "src", "quote-public.ts"), "utf8");

/** The body of one @media print block, or null when the file ships none. */
function printBlock() {
  const m = SRC.match(/@media\s+print\s*\{([\s\S]*)\}\s*`\s*;?\s*(?:\n|$)/);
  return m ? m[1] : null;
}

test("the component ships a print media block", () => {
  assert.ok(printBlock() !== null, "quote-public.ts carries no @media print block");
});

test("the print half states the dealer header rule in the document's own family", () => {
  const block = printBlock();
  assert.ok(block, "no print block to read");
  // The quote's rule: the family token itself, in a longhand, not the alias chain the
  // print path can fail to resolve.
  assert.match(
    block,
    /header\.dealer\s*\{[^}]*border-bottom-color:\s*var\(--hh-quote\)/,
    "the print block must set the header rule's colour from var(--hh-quote)",
  );
  // The converted order keeps its own family in print.
  assert.match(
    block,
    /:host\(\[data-doc="order"\]\)\s+header\.dealer\s*\{[^}]*border-bottom-color:\s*var\(--hh-accent-text\)/,
    "the print block must keep the order variant's rule on the gold ink",
  );
  // Never a stand-in: no literal colour, no currentColor, no greyscale line in print.
  const offending = block.match(/border[^:]*:\s*[^;]*(#[0-9a-fA-F]{3,8}|currentColor|rgba?\()/g) ?? [];
  assert.deepEqual(offending, [], `the print rule must name only family tokens: ${offending.join(", ")}`);
});
