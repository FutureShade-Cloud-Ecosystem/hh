// Item 8e: a framed desk app's sign out runs when the person leaves the Work
// shell, and what the host can revoke on the server is revoked.
//
// What the legs prove, and what they cannot. The only server side session the
// Work shell can end is the FB Console session (the host's auth_sign_out; the
// stub Console in test/support/server.mjs records it). The core's token is a
// stateless signed token with no revoke route (core/internal/identity/
// sessionsigner.go: claims iss, sub, aud, iat, exp), and every desk app's
// account surface is the in memory demo account whose deleteSession clears
// memory only (web/packages/pos-kit/src/ported/demo-account.ts), so a desk
// app's own sign out is a custody, lock and offline copy release, not a call
// to a server. The legs therefore prove, per desk app: the app's OWN sign out
// ran before the host dropped the frame (its custody mirror was removed by the
// frame's own script and the host saw the acknowledgement), and the stub
// Console session is gone after Switch role. The composed legs prove the
// host's side: the bounded wait, the silent frame, a forged acknowledgement,
// and the frame that ends its own walk. The per app legs need the REAL desk
// bundles (WS_DIST names an assembled dist).
import { expect, test, useConsole } from "./fixtures";
import type { Frame, Page } from "@playwright/test";

const MIRROR_KEY = "hh-auth.session.v2";

/** The seven desk apps of the Work profile: id, serve path, name. A sales manager's switcher holds all of them. */
const APPS = [
  ["counter-till", "/till/", "Counter Till"],
  ["quote-desk", "/quote/", "Quote Desk"],
  ["dispatch-board", "/dispatch/", "Dispatch"],
  ["buying-and-stock", "/buying-and-stock/", "Buying and Stock"],
  ["money-desk", "/money-desk/", "Money Desk"],
  ["manager-home", "/home/", "Manager Home"],
  ["dealer-admin", "/dealer-admin/", "Dealer Admin"],
] as const;

type ConsoleState = () => Promise<{ live: boolean; signOuts: number; opened: number; calls: string[] }>;

async function openConsoleShell(page: Page, shellUrl: string): Promise<ConsoleState> {
  const state = await useConsole(page, shellUrl);
  await page.goto(`${shellUrl}/`);
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-phase", "desk");
  expect((await state()).live).toBe(true);
  return state;
}

async function openApp(page: Page, appId: string): Promise<void> {
  await page.locator("hh-shell-header button.selector").click();
  await page.locator(`hh-shell-header .panel .mi[data-app="${appId}"]`).click();
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-current-app", appId);
}

/** Press Switch role in the shell's account menu; returns the time it took to reach the door, in milliseconds. */
async function pressSwitchRole(page: Page): Promise<number> {
  await page.locator("hh-shell-header button.account").click();
  const button = page.getByTestId("account-menu").getByRole("button", { name: "Switch role" });
  const started = Date.now();
  await button.click();
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-phase", "door", { timeout: 10_000 });
  return Date.now() - started;
}

/** A same origin sibling frame the host page did not make: the strongest hostile window there is. Returns its Playwright frame. */
async function hostileSibling(page: Page): Promise<Frame> {
  await page.evaluate(() => {
    const sibling = document.createElement("iframe");
    sibling.setAttribute("data-testid", "hostile");
    sibling.srcdoc = "<!doctype html><title>hostile</title>";
    document.body.appendChild(sibling);
  });
  const handle = await page.locator('[data-testid="hostile"]').elementHandle();
  const frame = await handle?.contentFrame();
  if (frame === null || frame === undefined) throw new Error("the hostile sibling frame did not mount");
  return frame;
}

/** The framed app's own root element has mounted and is registered (judged in the FRAME's registry, not the host's). */
async function waitForAppRoot(page: Page): Promise<void> {
  await expect
    .poll(async () =>
      page.locator('[data-testid="app-frame"]').evaluate((f) => {
        const frameWindow = (f as HTMLIFrameElement).contentWindow;
        const root = frameWindow?.document.body?.querySelector(":scope > *:not(script)");
        const tag = root?.tagName.toLowerCase() ?? "";
        return tag.includes("-") && frameWindow?.customElements.get(tag) !== undefined ? tag : "";
      }),
    )
    .not.toBe("");
}

/** The wait the host allows a frame (config.ts SIGN_OUT_WAIT_MS), with room for the page to react. */
const WAIT_MS = 2000;

test.describe("composed dist: the host's side of the ask", () => {
  test.skip(() => (process.env["WS_DIST"] ?? "") !== "", "the composed fixture pages are not in an assembled dist");

  test("Switch role asks the frame to sign out first: it runs, the host sees the acknowledgement, the Console session is gone", async ({ page, shellUrl }) => {
    const state = await openConsoleShell(page, shellUrl);
    await expect(page.frameLocator('[data-testid="app-frame"]').getByTestId("fixture")).toContainText("fixture:/home/");
    const took = await pressSwitchRole(page);
    expect(await page.evaluate(() => (window as unknown as { __fixtureSignedOut?: number }).__fixtureSignedOut)).toBe(1);
    await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-leave", "acknowledged");
    expect(took, "an answering frame does not cost the whole wait").toBeLessThan(WAIT_MS - 300);
    const after = await state();
    expect(after.live, "the Console session is gone on the server").toBe(false);
    expect(after.signOuts).toBe(1);
    const kept = await page.evaluate((key) => sessionStorage.getItem(key), MIRROR_KEY);
    expect(kept).toBeNull();
  });

  test("a frame that never answers: Switch role waits the bounded time, then the host revokes the Console session itself", async ({ page, shellUrl }) => {
    const state = await openConsoleShell(page, shellUrl);
    await expect(page.frameLocator('[data-testid="app-frame"]').getByTestId("fixture")).toContainText("fixture:/home/");
    await page.evaluate(() => {
      (window as unknown as { __fixtureSilent: boolean }).__fixtureSilent = true;
    });
    const took = await pressSwitchRole(page);
    await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-leave", "timed-out");
    expect(took, "the host waited for the frame").toBeGreaterThanOrEqual(WAIT_MS - 200);
    expect(took, "and not much longer").toBeLessThan(WAIT_MS + 2500);
    expect((await state()).live, "the host revoked the Console session whether or not the frame answered").toBe(false);
    expect(await page.evaluate(() => (window as unknown as { __fixtureSignedOut?: number }).__fixtureSignedOut)).toBeUndefined();
    expect(await page.evaluate((key) => sessionStorage.getItem(key), MIRROR_KEY)).toBeNull();
  });

  test("a forged acknowledgement from another window, with the live nonce, does not end the wait", async ({ page, shellUrl }) => {
    const state = await openConsoleShell(page, shellUrl);
    await expect(page.frameLocator('[data-testid="app-frame"]').getByTestId("fixture")).toContainText("fixture:/home/");
    await page.evaluate(() => {
      (window as unknown as { __fixtureSilent: boolean }).__fixtureSilent = true;
    });
    const sibling = await hostileSibling(page);
    // From the sibling's OWN realm (so the message's source is the sibling): it reads the nonce the host chose, which
    // the legs share through the page's global (the strongest forger there is), and posts the acknowledgement.
    await sibling.evaluate(() => {
      const top = window.parent as unknown as { __fixtureNonce?: string };
      const timer = setInterval(() => {
        if (top.__fixtureNonce !== undefined) window.parent.postMessage({ type: "hh-work-shell:signed-out", nonce: top.__fixtureNonce }, window.parent.location.origin);
      }, 50);
      setTimeout(() => clearInterval(timer), 4500);
    });
    const took = await pressSwitchRole(page);
    await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-leave", "timed-out");
    expect(took, "the forgery did not shorten the wait").toBeGreaterThanOrEqual(WAIT_MS - 200);
    expect((await state()).live).toBe(false);
  });

  test("an app that ends its own walk: the host is told, is not stalled by asking again, and still revokes the Console session", async ({ page, shellUrl }) => {
    const state = await openConsoleShell(page, shellUrl);
    const started = Date.now();
    await page.frameLocator('[data-testid="app-frame"]').getByTestId("fixture-switch-role").click();
    await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-phase", "door", { timeout: 10_000 });
    expect(Date.now() - started, "no second wait for a frame that already left").toBeLessThan(WAIT_MS - 300);
    await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-leave", "ended-by-app");
    expect((await state()).live).toBe(false);
  });

  test("offline, Switch role is never held past the bounded wait and the keys are cleared", async ({ page, context, shellUrl }) => {
    await openConsoleShell(page, shellUrl);
    await expect(page.frameLocator('[data-testid="app-frame"]').getByTestId("fixture")).toContainText("fixture:/home/");
    await context.setOffline(true);
    const took = await pressSwitchRole(page);
    expect(took).toBeLessThan(WAIT_MS + 2500);
    expect(await page.evaluate((key) => sessionStorage.getItem(key), MIRROR_KEY)).toBeNull();
    await context.setOffline(false);
  });
});

test.describe("real bundles: one leg per desk app", () => {
  test.skip(() => (process.env["WS_DIST"] ?? "") === "", "WS_DIST names an assembled dist for these legs");

  for (const [id, path, name] of APPS) {
    test(`${name}: Switch role runs the app's own sign out, then the Console session is gone on the server`, async ({ page, shellUrl }) => {
      const state = await openConsoleShell(page, shellUrl);
      if (id !== "manager-home") await openApp(page, id);
      await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-current-app", id);
      const frameEl = page.locator('[data-testid="app-frame"]');
      await expect.poll(async () => (await frameEl.evaluate((f) => (f as HTMLIFrameElement).contentWindow?.location.pathname ?? "")) as string).toBe(path);
      await waitForAppRoot(page);
      await page.waitForTimeout(1200);
      // a spy in the FRAME's own realm: every storage removal the app's script makes is logged on the host page
      await frameEl.evaluate((f) => {
        const frameWindow = (f as HTMLIFrameElement).contentWindow as unknown as { Storage: typeof Storage; parent: { __removals?: string[] } };
        frameWindow.parent.__removals = [];
        const original = frameWindow.Storage.prototype.removeItem;
        frameWindow.Storage.prototype.removeItem = function (key: string): void {
          frameWindow.parent.__removals?.push(key);
          return original.call(this, key);
        };
      });
      const took = await pressSwitchRole(page);
      await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-leave", "acknowledged");
      expect(took, "the app answered inside the bound").toBeLessThan(WAIT_MS - 100);
      const removals = await page.evaluate(() => (window as unknown as { __removals?: string[] }).__removals ?? []);
      expect(removals, "the app's own script cleared its custody").toContain(MIRROR_KEY);
      const after = await state();
      expect(after.live, "the Console session is gone on the server").toBe(false);
      expect(after.signOuts, `one sign out command, sent by the host: ${after.calls.join(", ")}`).toBe(1);
    });
  }

  test("a hostile window asking the Counter Till to sign out is ignored; only its own parent can ask", async ({ page, shellUrl }) => {
    const state = await openConsoleShell(page, shellUrl);
    await openApp(page, "counter-till");
    const frameEl = page.locator('[data-testid="app-frame"]');
    await waitForAppRoot(page);
    await page.waitForTimeout(1200);
    await frameEl.evaluate((f) => {
      const frameWindow = (f as HTMLIFrameElement).contentWindow as unknown as { Storage: typeof Storage; parent: { __removals?: string[]; __acks?: number } };
      frameWindow.parent.__removals = [];
      const original = frameWindow.Storage.prototype.removeItem;
      frameWindow.Storage.prototype.removeItem = function (key: string): void {
        frameWindow.parent.__removals?.push(key);
        return original.call(this, key);
      };
    });
    await page.evaluate(() => {
      const w = window as unknown as { __acks: number };
      w.__acks = 0;
      window.addEventListener("message", (event) => {
        const data = event.data as { type?: unknown } | null;
        if (data !== null && typeof data === "object" && data.type === "hh-work-shell:signed-out") w.__acks += 1;
      });
    });
    // a sibling frame posts the exact request into the till's window, from its own realm
    const sibling = await hostileSibling(page);
    await sibling.evaluate(() => {
      const till = (window.parent.document.querySelector("ws-host")?.shadowRoot?.querySelector('iframe[data-testid="app-frame"]') as HTMLIFrameElement | null)?.contentWindow;
      till?.postMessage({ type: "hh-work-shell:sign-out", nonce: "hostile01" }, window.parent.location.origin);
    });
    await page.waitForTimeout(1500);
    expect(await page.evaluate(() => (window as unknown as { __removals?: string[] }).__removals ?? [])).toEqual([]);
    expect(await page.evaluate(() => (window as unknown as { __acks: number }).__acks)).toBe(0);
    await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-phase", "desk");
    expect((await state()).live, "nothing was signed out").toBe(true);
  });
});
