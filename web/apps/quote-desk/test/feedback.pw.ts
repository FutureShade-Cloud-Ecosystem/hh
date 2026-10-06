// The feedback widget on the Quote Desk (Feature Polish, decision log row 958):
// the shared widget's third mount. The desk shows no floating pill: the trigger
// is a Feedback button in the shared header's right cluster, which opens the
// same panel. The desk's own stub answers the feedback route in the contract's
// shape (the work item number, an error, or an empty answer), so this spec
// never reaches a real board.
//
// Three legs: the header trigger and the absent pill, the open, type, attach,
// Send leg with its failure leg, and a bounds leg that proves no working
// control of the work area is covered by any box of the widget.
import { expect, test, type Page } from "@playwright/test";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

/** Open a quote with one line, at the top of its page. */
async function openQuote(page: Page): Promise<void> {
  await signIn(page);
  // A new quote is its own page under the split-list navigation.
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  await expect(page.getByTestId("qb-identity")).toBeVisible();
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill("Maple");
  await picker.getByRole("option", { name: /Maple Ridge Contracting/ }).click();
  const scan = page.getByTestId("pos-scan-input");
  await scan.fill("PSN-100");
  await scan.press("Enter");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  await page.evaluate(() => window.scrollTo(0, 0));
}

// One tiny PNG, enough for the widget's type and size checks.
const PNG = Buffer.from(
  "89504e470d0a1a0a0000000d4948445200000001000000010802000000907753de" +
    "0000000c4944415408d763f8cfc000000301010018dd8db00000000049454e44ae426082",
  "hex",
);

async function setMode(page: Page, mode: "ok" | "fail" | "no-number"): Promise<void> {
  await page.request.put("/_stub/feedback-mode", { data: { mode } });
}

async function openPanel(page: Page): Promise<void> {
  await page.getByTestId("feedback-control").click();
  await expect(page.getByTestId("feedback-panel")).toBeVisible();
}

async function fill(page: Page): Promise<void> {
  await page.getByTestId("feedback-kind-feedback").click();
  await page.getByTestId("feedback-note").fill("The send channel reads small");
  // Attach by paste: the widget hears a paste on the document while the panel is open.
  await page.evaluate((hex) => {
    const bytes = new Uint8Array(hex.match(/../g)!.map((pair) => parseInt(pair, 16)));
    const data = new DataTransfer();
    data.items.add(new File([bytes], "shot.png", { type: "image/png" }));
    document.dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }));
  }, PNG.toString("hex"));
  await expect(page.getByTestId("feedback-thumbs").locator("li")).toHaveCount(1);
}

test("the header carries a Feedback button, no floating pill, and the button opens the shared panel", async ({ page }) => {
  await signIn(page);
  // The pill is not in the layout; the trigger sits in the shared header's cluster.
  await expect(page.getByTestId("feedback-pill")).toBeHidden();
  const bar = (await page.getByTestId("qd-bar").boundingBox())!;
  const control = page.getByTestId("feedback-control");
  await expect(control).toHaveText("Feedback");
  const box = (await control.boundingBox())!;
  expect(box.y).toBeGreaterThanOrEqual(bar.y);
  expect(box.y + box.height).toBeLessThanOrEqual(bar.y + bar.height);

  await control.click();
  await expect(page.getByTestId("feedback-panel")).toBeVisible();
  await expect(page.getByTestId("feedback-kind-bug")).toHaveText("Something broke");
  await expect(page.getByTestId("feedback-kind-feedback")).toHaveText("Polish idea");
  await expect(page.getByTestId("feedback-kind-flag")).toHaveText("Flag for later");
  // The button only opens; the panel's own Close shuts it, and a second press opens it again.
  await page.getByRole("button", { name: "Close feedback" }).click();
  await expect(page.getByTestId("feedback-panel")).toBeHidden();
  await control.click();
  await expect(page.getByTestId("feedback-panel")).toBeVisible();
});

test("open, type, attach, Send: the stub answers the work item number and the panel clears", async ({ page, request }) => {
  await signIn(page);
  await openPanel(page);
  await fill(page);
  await page.getByTestId("feedback-send").click();
  await expect(page.getByTestId("feedback-sent")).toHaveText("Sent: HH-1");
  await expect(page.getByTestId("feedback-note")).toHaveValue("");
  await expect(page.getByTestId("feedback-thumbs")).toHaveCount(0);
  const seen = (await (await request.get("/_stub/feedback")).json()) as { filed: Array<Record<string, unknown>> };
  expect(seen.filed).toEqual([{ type: "feedback", message: "The send channel reads small", screen: "Quote Desk", images: 1 }]);
});

test("a pick through the picker adds the thumbnail and Send files it", async ({ page, request }) => {
  await signIn(page);
  await openPanel(page);
  await page.getByTestId("feedback-note").fill("Picked from the file picker");
  await page.getByTestId("feedback-file").setInputFiles({ name: "picked.png", mimeType: "image/png", buffer: PNG });
  await expect(page.getByTestId("feedback-thumbs").locator("li")).toHaveCount(1);
  await page.getByTestId("feedback-send").click();
  await expect(page.getByTestId("feedback-sent")).toHaveText("Sent: HH-1");
  const seen = (await (await request.get("/_stub/feedback")).json()) as { filed: Array<Record<string, unknown>> };
  expect(seen.filed).toEqual([{ type: "bug", message: "Picked from the file picker", screen: "Quote Desk", images: 1 }]);
});

test("when no number comes back the panel reads Sent.", async ({ page }) => {
  await setMode(page, "no-number");
  await signIn(page);
  await openPanel(page);
  await page.getByTestId("feedback-note").fill("No number expected");
  await page.getByTestId("feedback-send").click();
  await expect(page.getByTestId("feedback-sent")).toHaveText("Sent.");
});

test("a failed send keeps the note and the images and says so", async ({ page, request }) => {
  await setMode(page, "fail");
  await signIn(page);
  await openPanel(page);
  await fill(page);
  await page.getByTestId("feedback-send").click();
  await expect(page.getByTestId("feedback-error")).toBeVisible();
  await expect(page.getByTestId("feedback-sent")).toHaveCount(0);
  await expect(page.getByTestId("feedback-note")).toHaveValue("The send channel reads small");
  await expect(page.getByTestId("feedback-thumbs").locator("li")).toHaveCount(1);
  const seen = (await (await request.get("/_stub/feedback")).json()) as { filed: unknown[] };
  expect(seen.filed).toHaveLength(0);

  // The retry, once the board answers, files the same note.
  await setMode(page, "ok");
  await page.getByTestId("feedback-send").click();
  await expect(page.getByTestId("feedback-sent")).toHaveText("Sent: HH-1");
});

// Bounds: no box of the widget meets a working control of the work area.
const SIZES = [
  { width: 1920, height: 1080 },
  { width: 1366, height: 768 },
  { width: 1280, height: 720 },
  { width: 1024, height: 768 },
  { width: 820, height: 1180 },
] as const;

for (const scheme of ["light", "dark"] as const) {
  for (const size of SIZES) {
    test(`bounds at ${size.width}x${size.height}, ${scheme}: no working control is covered by the widget`, async ({ page }) => {
      await page.emulateMedia({ colorScheme: scheme });
      await page.setViewportSize(size);
      await openQuote(page);

      const verdict = await page.evaluate(() => {
        function deepAll(root: ParentNode, selector: string, out: Element[] = []): Element[] {
          for (const el of Array.from(root.querySelectorAll(selector))) out.push(el);
          for (const el of Array.from(root.querySelectorAll("*"))) {
            if (el.shadowRoot !== null) deepAll(el.shadowRoot, selector, out);
          }
          return out;
        }
        function deepAt(x: number, y: number): Element | null {
          let el: Element | null = document.elementFromPoint(x, y);
          while (el !== null && el.shadowRoot !== null) {
            const inner: Element | null = el.shadowRoot.elementFromPoint(x, y);
            if (inner === null || inner === el) break;
            el = inner;
          }
          return el;
        }
        function inWidget(el: Element | null): boolean {
          for (let node: Node | null = el; node !== null; node = node.parentNode instanceof ShadowRoot ? node.parentNode.host : node.parentNode) {
            if (node instanceof Element && node.localName === "hh-feedback") return true;
          }
          return false;
        }
        const ids = ["qb-send", "qb-send-channel", "qb-fulfil-pickup", "qb-fulfil-delivery"];
        const controls: Element[] = [];
        for (const id of ids) controls.push(...deepAll(document, `[data-testid="${id}"]`));
        controls.push(...deepAll(document, 'nav[aria-label="Quote sections"] button, nav[aria-label="Quote sections"] a'));
        const problems: string[] = [];
        if (controls.length < 7) problems.push(`only ${controls.length} controls found`);
        // Every fixed or sticky box the widget owns, shown or not.
        const host = document.querySelector("qd-app-root")?.shadowRoot?.querySelector("hh-feedback") ?? null;
        const boxes = host?.shadowRoot === null || host === null
          ? []
          : Array.from(host.shadowRoot.querySelectorAll("*")).filter((el) => {
              const style = getComputedStyle(el);
              return (style.position === "fixed" || style.position === "sticky") && style.display !== "none";
            });
        for (const control of controls) {
          const rect = control.getBoundingClientRect();
          const name = control.getAttribute("data-testid") ?? control.textContent?.trim() ?? control.localName;
          if (rect.width === 0 || rect.height === 0) continue;
          const onScreen = rect.bottom > 0 && rect.top < innerHeight && rect.right > 0 && rect.left < innerWidth;
          if (!onScreen) continue;
          const cx = Math.min(Math.max(rect.left + rect.width / 2, 0), innerWidth - 1);
          const cy = Math.min(Math.max(rect.top + rect.height / 2, 0), innerHeight - 1);
          if (inWidget(deepAt(cx, cy))) problems.push(`${name}: the widget is at the control's centre`);
          for (const box of boxes) {
            const b = box.getBoundingClientRect();
            if (b.left < rect.right && b.right > rect.left && b.top < rect.bottom && b.bottom > rect.top) {
              problems.push(`${name}: a ${getComputedStyle(box).position} box of the widget meets it`);
            }
          }
        }
        return { problems, found: controls.length };
      });
      expect(verdict.found).toBeGreaterThanOrEqual(7);
      expect(verdict.problems).toEqual([]);

      // The open panel sits wholly inside the viewport.
      await openPanel(page);
      const panel = (await page.getByTestId("feedback-panel").boundingBox())!;
      expect(panel.x).toBeGreaterThanOrEqual(0);
      expect(panel.y).toBeGreaterThanOrEqual(0);
      expect(panel.x + panel.width).toBeLessThanOrEqual(size.width);
      expect(panel.y + panel.height).toBeLessThanOrEqual(size.height);
    });
  }
}
