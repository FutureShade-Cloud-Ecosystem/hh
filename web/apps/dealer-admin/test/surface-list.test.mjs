// UX-104's contract in the sources: on a phone the section's own work comes
// first, so the left list folds behind the Settings control (a native
// button, 44px tall, aria-expanded and aria-controls announcing the folded
// list), choosing a section folds the list again, and the desk widths keep
// the two column layout and never see the control.
//
// Red first: against the pre UX-104 sources the fold, the control and the
// pinned columns are all absent.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const src = (rel) => readFileSync(join(here, "..", rel), "utf8");

test("the sheet folds the left list on a phone and holds the control to 44px", () => {
  const sheet = src("src/styles.ts");
  // The fold is the phone default; the open state is the exception.
  assert.match(sheet, /\.app-body:not\(\.list-open\) \.surface-list \{\s*display: none;/);
  // The control is a 44px touch target on the phone and absent at the desk.
  assert.match(sheet, /\.surface-list-toggle \{\s*display: none;/);
  const phone = sheet.slice(sheet.indexOf("@media (max-width: 900px)"));
  assert.match(phone, /\.surface-list-toggle \{[\s\S]*?min-height: 44px;/);
  // The desk columns are pinned, so the phone's DOM order changes nothing there.
  assert.match(sheet, /\.surface-list \{\s*grid-column: 1;\s*grid-row: 1;/);
  assert.match(sheet, /\.surface-work \{\s*grid-column: 2;\s*grid-row: 1;/);
});

test("the host renders the control before the list, and a chosen section folds it", () => {
  const root = src("src/app-root.ts");
  // The control announces and names the list it folds.
  assert.match(root, /data-action="toggle-surface-list"/);
  assert.match(root, /aria-expanded=\$\{this\.#surfaceListOpen \? "true" : "false"\}/);
  assert.match(root, /aria-controls="surface-list"/);
  assert.match(root, /<nav id="surface-list"/);
  // A section chosen from the open list folds it again.
  assert.match(root, /this\.#surfaceListOpen = false;/);
  // The DOM order under the app body is control, list, work: on a phone the
  // folded state puts the section's own work first, and the open list sits
  // directly under the control that opened it.
  const body = root.slice(root.indexOf("renderSignedIn"));
  const control = body.indexOf("renderSurfaceListControl()");
  const list = body.indexOf("renderSurfaceList()");
  const work = body.indexOf('class="surface-work"');
  assert.ok(control !== -1 && list !== -1 && work !== -1, "the app body renders the control, the list and the work");
  assert.ok(control < list && list < work, "the DOM order is control, list, work");
});
