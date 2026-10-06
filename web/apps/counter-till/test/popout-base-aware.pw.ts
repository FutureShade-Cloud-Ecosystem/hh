// The pop-out's base-awareness leg (HH-r15): on the demo box the till is
// served under /till/ (vite built with --base=/till/). On main (the bug)
// the pop-out opens "/?popout=<flow>" -- the root, the front door's picker.
// After the fix it opens "${BASE_URL}?popout=<flow>" which with
// --base=/till/ becomes "/till/?popout=<flow>" -- the till's own page.
//
// The leg also asserts the window was asked for as a popup with the opener's
// size: the fix passes popup=yes,width=<opener outer width>,height=<opener
// outer height> and calls .focus() on the result. Headless Chrome does not
// honour the requested size reliably, so the leg asserts the features string
// handed to window.open through a page level spy, never the measured size.
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

test("the pop-out opens under the till's base, not the root, as a popup of the opener's size", async ({
  page,
  context,
}) => {
  // Spy on window.open before any page script runs, and still call through so
  // the real window opens. The spy keeps the arguments the till passed.
  await page.addInitScript(() => {
    const real = window.open.bind(window);
    const calls: Array<{ url: string; target: string; features: string }> = [];
    (window as unknown as { __openCalls: typeof calls }).__openCalls = calls;
    window.open = (url?: string | URL, target?: string, features?: string) => {
      calls.push({ url: String(url ?? ""), target: target ?? "", features: features ?? "" });
      return real(url, target, features);
    };
  });
  await signIn(page);

  // Wait for the new window before clicking Pop out.
  const poppedPromise = context.waitForEvent("page");
  await page.getByRole("button", { name: "Pop out" }).click();
  const popped = await poppedPromise;
  await popped.waitForLoadState();

  // What the till asked the browser for: one call, a popup, the opener's size.
  const calls = await page.evaluate(
    () =>
      (window as unknown as { __openCalls: Array<{ url: string; features: string }> }).__openCalls,
  );
  expect(calls).toHaveLength(1);
  const opener = await page.evaluate(() => ({
    width: window.outerWidth,
    height: window.outerHeight,
  }));
  const features = new Map(
    calls[0].features.split(",").map((pair) => pair.split("=") as [string, string]),
  );
  expect(features.get("popup")).toBe("yes");
  expect(Number(features.get("width"))).toBe(opener.width);
  expect(Number(features.get("height"))).toBe(opener.height);

  // The new window's URL is under the till's base (/till/) and carries
  // ?popout=. On main the bug opens "/?popout=" (the root, front door picker).
  const url = new URL(popped.url());
  expect(url.pathname.startsWith("/till/")).toBe(true);
  expect(url.searchParams.get("popout")).toBe("sales");

  // It lands on the sale surface, not the front door and not a picker.
  await expect(popped.getByTestId("pos-page-title")).toHaveText("New Counter Sale");

  await popped.close();
});
