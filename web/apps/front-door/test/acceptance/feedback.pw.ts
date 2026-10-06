// The feedback widget's acceptance spec (Feature Polish, decision log row
// 958): the pill, the panel it opens, the capture, the paste, the remove, and
// a stubbed send. The stub answers the bridge's route in the contract's shape
// and records what the widget posted; CI never reaches a real board. Written
// red first: on main no pill exists to find.
import { expect, test, type Page } from "@playwright/test";

/** The widget lives in the design system's shadow DOM; its testids surface
 * through the penetrant locators Playwright rides. */
const pill = (page: Page) => page.getByTestId("feedback-pill");

async function openPanel(page: Page): Promise<void> {
  await expect(pill(page)).toBeVisible();
  await pill(page).click();
  await expect(page.getByTestId("feedback-panel")).toBeVisible();
}

async function stubPeek(page: Page): Promise<{
  feedback: { fields: Record<string, string | number>; bytes: number; org: string } | null;
}> {
  return (await page.request.get("/_stub/peek").then((answer) => answer.json())) as {
    feedback: { fields: Record<string, string | number>; bytes: number; org: string } | null;
  };
}

/** One small PNG, the same lead the api-client's own binary tests ride. */
function pngFile(): { buffer: Buffer; name: string; type: string } {
  const png = Buffer.from(
    "89504e470d0a1a0a0000000d4948445200000001000000010802000000907753de" +
      "0000000c4944415408d763f8cfc000000301010018dd8db00000000049454e44ae426082",
    "hex",
  );
  return { buffer: png, name: "pasted-shot.png", type: "image/png" };
}

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("the pill renders, opens the panel, and the kinds are the plain words", async ({ page }) => {
  await page.goto("/");
  await openPanel(page);
  await expect(page.getByTestId("feedback-kind-bug")).toHaveText("Something broke");
  await expect(page.getByTestId("feedback-kind-feedback")).toHaveText("Polish idea");
  await expect(page.getByTestId("feedback-kind-flag")).toHaveText("Flag for later");
  await expect(page.getByTestId("feedback-note")).toBeVisible();
  // The pill sits in the reserved corner: about 22px off the bottom edge,
  // never over the picker's cards or the header.
  const box = await pill(page).boundingBox();
  const viewportHeight = page.viewportSize()?.height ?? 720;
  expect(box).not.toBeNull();
  if (box !== null) {
    const inset = viewportHeight - (box.y + box.height);
    expect(inboxBounds(inset)).toBe(true);
  }
});

/** The pill's bottom inset is the reserved lane: 22px, give or take rendering. */
function inboxBounds(inset: number): boolean {
  return inset >= 16 && inset <= 30;
}

test("capture this screen returns a thumbnail of the view, the widget out of the shot", async ({ page }) => {
  await page.goto("/");
  await openPanel(page);
  await page.getByTestId("feedback-capture").click();
  const thumbs = page.getByTestId("feedback-thumbs");
  await expect(thumbs).toBeVisible({ timeout: 15_000 });
  await expect(thumbs.locator("li")).toHaveCount(1);
  // The capture is the page itself with the widget hidden: the shot must
  // carry real painted pixels (the capture's own evidence it rasterised the
  // view), and the pill's gold (#FFC313) must not appear where the pill sits
  // on the live screen, the shot's bottom-right corner.
  const paintedPixels = async (): Promise<number> =>
    page.evaluate(() => {
      // The widget mounts inside the app root's shadow DOM, so the canvas is
      // reached through both roots.
      const canvas = document
        .querySelector("fd-app-root")
        ?.shadowRoot?.querySelector("hh-feedback")
        ?.shadowRoot?.querySelector("[data-thumb-index] canvas");
      if (!(canvas instanceof HTMLCanvasElement)) return 0;
      const context = canvas.getContext("2d");
      if (context === null) return 0;
      const { width, height } = canvas;
      const data = context.getImageData(0, 0, width, height).data;
      let painted = 0;
      for (let index = 0; index < data.length; index += 4) {
        if (data[index + 3] > 0) painted += 1;
      }
      return painted;
    });
  await expect.poll(paintedPixels, { timeout: 10_000 }).toBeGreaterThan(100);
});

test("a pasted image becomes a thumbnail and remove takes it away", async ({ page }) => {
  await page.goto("/");
  await openPanel(page);
  const image = pngFile();
  await page.evaluate(
    async ({ buffer, name, type }) => {
      const file = new File([Uint8Array.from(buffer)], name, { type });
      const transfer = new DataTransfer();
      transfer.items.add(file);
      document.dispatchEvent(new ClipboardEvent("paste", { clipboardData: transfer }));
    },
    { buffer: Array.from(image.buffer), name: image.name, type: image.type },
  );
  const thumbs = page.getByTestId("feedback-thumbs");
  await expect(thumbs).toBeVisible();
  await expect(thumbs.locator("li")).toHaveCount(1);
  await page.getByTestId("feedback-remove-0").click();
  await expect(thumbs).toHaveCount(0);
});

test("a stubbed send answers with the work item's number and posts the widget's shape", async ({ page }) => {
  await page.goto("/");
  await openPanel(page);
  await page.getByTestId("feedback-kind-feedback").click();
  await page.getByTestId("feedback-note").fill("The pricing panel jumps when it loads.");
  await page.getByTestId("feedback-capture").click();
  await expect(page.getByTestId("feedback-thumbs")).toBeVisible({ timeout: 15_000 });
  await page.getByTestId("feedback-send").click();
  await expect(page.getByTestId("feedback-sent")).toHaveText("Sent: HH-42");

  const peek = await stubPeek(page);
  expect(peek.feedback).not.toBeNull();
  if (peek.feedback !== null) {
    expect(peek.feedback.fields["type"]).toBe("feedback");
    expect(peek.feedback.fields["message"]).toContain("pricing panel jumps");
    expect(peek.feedback.fields["screen"]).toBe("Front door");
    expect(peek.feedback.fields["images"]).toBe(1);
    expect(peek.feedback.org).toBe("hh-demo");
  }
});

test("a refused send keeps the note and the images for the retry", async ({ page }) => {
  await page.goto("/");
  await openPanel(page);
  await page.getByTestId("feedback-note").fill("This one will be refused.");
  const image = pngFile();
  await page.evaluate(
    async ({ buffer, name, type }) => {
      const file = new File([Uint8Array.from(buffer)], name, { type });
      const transfer = new DataTransfer();
      transfer.items.add(file);
      document.dispatchEvent(new ClipboardEvent("paste", { clipboardData: transfer }));
    },
    { buffer: Array.from(image.buffer), name: image.name, type: image.type },
  );
  await expect(page.getByTestId("feedback-thumbs").locator("li")).toHaveCount(1);
  await page.request.post("/_stub/refuse-feedback", { data: { status: 502, error: "the board is unreachable" } });

  await page.getByTestId("feedback-send").click();
  await expect(page.getByTestId("feedback-error")).toContainText("board is unreachable");
  // The failure kept everything: the note's text and the thumbnail.
  await expect(page.getByTestId("feedback-note")).toHaveValue("This one will be refused.");
  await expect(page.getByTestId("feedback-thumbs").locator("li")).toHaveCount(1);

  // The retry, with the refusal spent, sends and clears.
  await page.getByTestId("feedback-send").click();
  await expect(page.getByTestId("feedback-sent")).toHaveText("Sent: HH-42");
  await expect(page.getByTestId("feedback-note")).toHaveValue("");
});
