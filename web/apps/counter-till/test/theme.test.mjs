// The theme choice's pure decisions: the cycle, the attribute each choice
// writes on the document root, and the control's accessible name. The dark
// sheet is the tokens' own default, so it is the absence of the attribute.
import { test } from "node:test";
import assert from "node:assert/strict";
import { themeAfter, themeAttribute, themeControlLabel, themeStatusText } from "../src/theme.js";

test("the cycle visits Auto, Light and Dark and returns to Auto", () => {
  assert.equal(themeAfter("auto"), "light");
  assert.equal(themeAfter("light"), "dark");
  assert.equal(themeAfter("dark"), "auto");
});

test("Auto follows the system's light setting; Light and Dark pin it", () => {
  assert.equal(themeAttribute("auto", true), "light");
  assert.equal(themeAttribute("auto", false), null);
  assert.equal(themeAttribute("light", false), "light");
  assert.equal(themeAttribute("dark", true), null);
});

test("the control's name says what is on and what the press brings", () => {
  assert.equal(themeControlLabel("auto"), "Theme: Auto. Press for Light.");
  assert.equal(themeControlLabel("dark"), "Theme: Dark. Press for Auto.");
});

test("a press announces the state that took effect", () => {
  assert.equal(themeStatusText("light"), "Theme is now Light.");
  assert.equal(themeStatusText("dark"), "Theme is now Dark.");
  assert.equal(themeStatusText("auto"), "Theme is now Auto, following the system.");
});
