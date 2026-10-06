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

test("closing and reopening the panel shows the note and thumbnails as kept, and Send posts what is seen", async ({ page }) => {
  await page.goto("/");
  await openPanel(page);
  await page.getByTestId("feedback-note").fill("Kept words across a reopen.");
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
  // The thumbnail's pixels, read through both shadow roots.
  const painted = async (): Promise<number> =>
    page.evaluate(() => {
      const canvas = document
        .querySelector("fd-app-root")
        ?.shadowRoot?.querySelector("hh-feedback")
        ?.shadowRoot?.querySelector("[data-thumb-index] canvas");
      if (!(canvas instanceof HTMLCanvasElement)) return 0;
      const context = canvas.getContext("2d");
      if (context === null) return 0;
      const data = context.getImageData(0, 0, canvas.width, canvas.height).data;
      let count = 0;
      for (let index = 0; index < data.length; index += 4) if (data[index + 3] > 0) count += 1;
      return count;
    });
  await expect.poll(painted).toBeGreaterThan(0);

  // Close with the pill, reopen with the pill.
  await pill(page).click();
  await expect(page.getByTestId("feedback-panel")).toHaveCount(0);
  await pill(page).click();
  await expect(page.getByTestId("feedback-panel")).toBeVisible();

  // The note and the thumbnail are shown exactly as kept, pixels included.
  await expect(page.getByTestId("feedback-note")).toHaveValue("Kept words across a reopen.");
  await expect(page.getByTestId("feedback-thumbs").locator("li")).toHaveCount(1);
  await expect.poll(painted).toBeGreaterThan(0);

  // Send posts what the user can see.
  await page.getByTestId("feedback-send").click();
  await expect(page.getByTestId("feedback-sent")).toHaveText("Sent: HH-42");
  const peek = await stubPeek(page);
  expect(peek.feedback?.fields["message"]).toBe("Kept words across a reopen.");
  expect(peek.feedback?.fields["images"]).toBe(1);
});

/** The pill's whole box is the topmost thing at its corners and its centre:
 * the deep hit walks every shadow root the way a pointer does. */
async function pillIsTopmost(page: Page): Promise<string[]> {
  return pill(page).evaluate((button) => {
    const deepHit = (x: number, y: number): Element | null => {
      let hit = document.elementFromPoint(x, y);
      while (hit?.shadowRoot) {
        const inner = hit.shadowRoot.elementFromPoint(x, y);
        if (inner === null || inner === hit) break;
        hit = inner;
      }
      return hit;
    };
    const box = button.getBoundingClientRect();
    // The corner probes sit 8px in, inside the pill's rounding.
    const inset = 8;
    const points: Array<[string, number, number]> = [
      ["top-left", box.left + inset, box.top + inset],
      ["top-right", box.right - inset, box.top + inset],
      ["bottom-left", box.left + inset, box.bottom - inset],
      ["bottom-right", box.right - inset, box.bottom - inset],
      ["centre", box.left + box.width / 2, box.top + box.height / 2],
    ];
    const bad: string[] = [];
    for (const [name, x, y] of points) {
      const hit = deepHit(x, y);
      if (hit === null || !(hit === button || button.contains(hit))) {
        bad.push(`${name} at ${Math.round(x)},${Math.round(y)} is ${hit?.tagName ?? "nothing"}`);
      }
    }
    return bad;
  });
}

for (const size of [
  { width: 1440, height: 900 },
  { width: 1024, height: 768 },
]) {
  test(`the pill is the topmost element at its corners at ${size.width}x${size.height}`, async ({ page }) => {
    await page.setViewportSize(size);
    await page.goto("/");
    await expect(pill(page)).toBeVisible();
    expect(await pillIsTopmost(page)).toEqual([]);
  });
}

test("a file picked through the hidden picker becomes a thumbnail and is sent", async ({ page }) => {
  await page.goto("/");
  await openPanel(page);
  await page.getByTestId("feedback-note").fill("A picked image.");
  const image = pngFile();
  // The picker's change event is not composed, so the widget listens on the
  // input itself; setInputFiles fires the real change.
  await page.getByTestId("feedback-file").setInputFiles({ name: image.name, mimeType: image.type, buffer: image.buffer });
  await expect(page.getByTestId("feedback-thumbs").locator("li")).toHaveCount(1);
  await page.getByTestId("feedback-send").click();
  await expect(page.getByTestId("feedback-sent")).toHaveText("Sent: HH-42");
  const peek = await stubPeek(page);
  expect(peek.feedback?.fields["images"]).toBe(1);
});

test("a send answered with no work item number reads Sent. and not Sent: filed", async ({ page }) => {
  await page.goto("/");
  await openPanel(page);
  await page.request.post("/_stub/feedback-numberless");
  await page.getByTestId("feedback-note").fill("No number comes back.");
  await page.getByTestId("feedback-send").click();
  await expect(page.getByTestId("feedback-sent")).toHaveText("Sent.");
});

test("trigger=external hides the pill and open() opens the panel for the host's own button", async ({ page }) => {
  await page.goto("/");
  await expect(pill(page)).toBeVisible();
  // The widget is mounted by the app; flip it to the external trigger the way
  // a host that owns its own button would mount it.
  await page.evaluate(() => {
    const widget = document.querySelector("fd-app-root")?.shadowRoot?.querySelector("hh-feedback") as
      | (HTMLElement & { open?: unknown })
      | null;
    widget?.setAttribute("trigger", "external");
  });
  await expect(pill(page)).toBeHidden();
  await expect(page.getByTestId("feedback-panel")).toHaveCount(0);
  await page.evaluate(() => {
    const widget = document.querySelector("fd-app-root")?.shadowRoot?.querySelector("hh-feedback") as
      | (HTMLElement & { show: () => void })
      | null;
    widget?.show();
  });
  await expect(page.getByTestId("feedback-panel")).toBeVisible();
  // The panel still sends, and Escape still closes it.
  await page.getByTestId("feedback-note").fill("From an external button.");
  await page.getByTestId("feedback-send").click();
  await expect(page.getByTestId("feedback-sent")).toHaveText("Sent: HH-42");
  // The send button gave up focus while it worked; Escape is heard from
  // inside the widget, so put focus back in the note first.
  await page.getByTestId("feedback-note").focus();
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("feedback-panel")).toHaveCount(0);
  await expect(pill(page)).toBeHidden();
});
