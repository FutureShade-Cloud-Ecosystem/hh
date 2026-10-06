// The pop-out's base-awareness leg (HH-r15): on the demo box the till is
// served under /till/ (vite built with --base=/till/). On main (the bug)
// the pop-out opens "/?popout=<flow>" -- the root, the front door's picker.
// After the fix it opens "${BASE_URL}?popout=<flow>" which with
// --base=/till/ becomes "/till/?popout=<flow>" -- the till's own page.
//
// The leg also asserts the new window is same-size as the opener: the fix
// passes popup=yes,width=<opener outer width>,height=<opener outer height>
// and calls .focus() on the result.
//
// Red: fails on main because the pop-out lands on /?popout= (root, front door).
// Green: passes after the fix because the pop-out lands on /till/?popout= (till).
import { expect, test, type Page } from "@playwright/test";

/** Mint a demo session as Sales and navigate to the till. */
async function signIn(page: Page): Promise<void> {
  const minted = await page.request.post("/api/v1/demo/session", { data: { flow: "sales" } });
  expect(minted.ok()).toBe(true);
  await page.goto("/till/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
}

test("the pop-out opens under the till's base, not the root, and is same-size as the opener", async ({
  page,
  context,
}) => {
  await signIn(page);

  // Record the opener's outer dimensions so we can assert the new window
  // carries them through to its inner dimensions.
  const openerWidth = page.viewportSize()?.width ?? 1280;
  const openerHeight = page.viewportSize()?.height ?? 720;

  // Wait for the new window before clicking Pop out.
  const poppedPromise = context.waitForEvent("page");
  await page.getByRole("button", { name: "Pop out" }).click();
  const popped = await poppedPromise;
  await popped.waitForLoadState();

  // The URL must begin with the till's base (/till/) and carry ?popout=.
  // On main the bug opens "/?popout=" (the root, front door picker).
  const poppedUrl = popped.url();
  const urlPattern = /^\/till\/.*\?popout=/;
  expect(urlPattern.test(poppedUrl)).toBe(true);

  // After the fix the new window is opened with popup=yes and the opener's
  // outer dimensions passed as width=/height=, so the new window's inner
  // dimensions must match the opener's outer dimensions within a small
  // tolerance (5px each axis covers rounding and browser chrome).
  const poppedInner = popped.viewportSize() ?? { width: 0, height: 0 };
  expect(Math.abs(poppedInner.width - openerWidth)).toBeLessThanOrEqual(5);
  expect(Math.abs(poppedInner.height - openerHeight)).toBeLessThanOrEqual(5);

  // The new window must show the sale surface (the popped window has focus
  // after .focus(), so the page title is visible without a timeout).
  await expect(popped.getByTestId("pos-page-title")).toBeVisible();

  await popped.close();
});
