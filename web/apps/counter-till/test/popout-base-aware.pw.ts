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

/**
 * Land on the till as Sales the way the box does (row 1040): the front door's key is in the tab
 * and the till adopts it. Under /till/ a visit with no session goes to the front door, so the
 * till's own cards are no longer the way in here.
 */
async function signIn(page: Page): Promise<void> {
  const issuedAtSeconds = Math.floor(Date.now() / 1000);
  const segment = (value: unknown) => Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
  const token = `${segment({ alg: "ES256", typ: "JWT" })}.${segment({
    iss: "hh-demo-stub",
    sub: "demo-account-sales",
    aud: "staff",
    iat: issuedAtSeconds,
    exp: issuedAtSeconds + 60 * 60,
  })}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
  const record = {
    token,
    tokenExpiresAtMs: (issuedAtSeconds + 60 * 60) * 1000,
    flow: "sales",
    roleLabel: "Sales",
    user: { userId: "demo-account-sales", email: "sales@demo.invalid", name: "Sales (demo)" },
    roleCode: "inside_sales",
    roleName: "Inside Sales",
    tier: 3,
    scope: "BRANCH",
  };
  await page.addInitScript((value) => {
    if (window.name === "seeded") return;
    window.name = "seeded";
    window.sessionStorage.setItem("hh-demo.front-door.session.v1", value);
  }, JSON.stringify(record));
  await page.goto("/till/");
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
