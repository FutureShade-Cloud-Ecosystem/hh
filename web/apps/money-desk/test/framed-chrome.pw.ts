// PR 503 round 2: framed by the Work shell the desk renders no band and no
// header, so the shell's account menu carries its Theme. The desk announces
// the controls it can act on to its parent, and acts on the shell's chrome
// action message from that parent alone. The leg frames the desk inside a
// same origin host page the leg serves itself.
import { expect, test } from "@playwright/test";

const HOST = "/__framed-host";
const HOST_PAGE = `<!doctype html><html><body>
<iframe id="f" src="/?hh-framed=work-shell" style="width:1200px;height:800px"></iframe>
<script>
  window.__controls = [];
  window.addEventListener("message", (e) => {
    if (e.data && e.data.type === "hh-work-shell:chrome-controls") window.__controls.push(e.data.actions);
  });
</script></body></html>`;

test("framed, the desk announces Theme to the shell and applies it when the shell asks", async ({ page }) => {
  await page.route(`**${HOST}`, (route) => route.fulfill({ contentType: "text/html", body: HOST_PAGE }));
  await page.goto(HOST);
  const frame = page.frameLocator("#f");
  // No band of its own and no app-local header: the shell's band is the one band.
  await expect(frame.locator("hh-shell-header")).toHaveCount(0);
  await expect(frame.locator("hos-app-header")).toHaveCount(0);
  await expect.poll(() => page.evaluate(() => (window as unknown as { __controls: string[][] }).__controls)).toEqual([["theme"]]);
  const root = () => page.frames()[1]!.evaluate(() => document.documentElement.getAttribute("data-theme"));
  const before = await root();
  const ask = (action: string) =>
    page.evaluate((a) => {
      const win = (document.getElementById("f") as HTMLIFrameElement).contentWindow!;
      win.postMessage({ type: "hh-work-shell:chrome-action", action: a }, window.location.origin);
    }, action);
  await ask("theme");
  await expect.poll(root).not.toBe(before);
  await ask("theme");
  await expect.poll(root).toBe(before);
  // A message that is not from the parent changes nothing: the frame's own window posting to itself.
  await page.frames()[1]!.evaluate(() =>
    window.postMessage({ type: "hh-work-shell:chrome-action", action: "theme" }, window.location.origin),
  );
  await page.waitForTimeout(300);
  expect(await root()).toBe(before);
});
