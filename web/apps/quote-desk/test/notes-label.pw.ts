// UX-64a: the quote's customer_notes field is labeled "Note to the customer" with the quiet hint
// "The customer sees this on their quote." directly under the field, linked via aria-describedby.
import { expect, test, type Page } from "@playwright/test";
import { openAccountMenu } from "./account-menu";
import { open, signIn } from "./switch-helpers.js";
import { shot } from "./shot.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

const SIZES = [
  { label: "1440x900", width: 1440, height: 900 },
  { label: "390x844", width: 390, height: 844 },
] as const;

const MODES = ["light", "charcoal"] as const;

async function setTheme(page: Page, mode: (typeof MODES)[number]): Promise<void> {
  const want = mode === "charcoal" ? "Brand" : "Light";
  const control = page.getByTestId("theme-control");
  await openAccountMenu(page);
  for (let i = 0; i < 3; i += 1) {
    if ((await control.textContent())?.includes(want)) {
      await page.keyboard.press("Escape");
      return;
    }
    await control.click();
  }
  await expect(control).toContainText(want);
  await page.keyboard.press("Escape");
}

for (const size of SIZES) {
  for (const mode of MODES) {
    test(`the quote note field is labeled "Note to the customer" with the customer visibility hint (${size.label}, ${mode})`, async ({
      page,
    }) => {
      await page.setViewportSize({ width: size.width, height: size.height });
      await signIn(page);
      await setTheme(page, mode);
      await open(page, "Q-1001");
      const field = page.getByTestId("qb-terms-notes");
      await expect(field).toBeVisible();
      const termsCard = field.locator("xpath=ancestor::div[contains(@class,'card')][1]");
      await termsCard.scrollIntoViewIfNeeded();
      await shot(page, `${size.label}-${mode}`);
      await shot(termsCard, `field-${size.label}-${mode}`);
      await expect(field).toHaveAccessibleName("Note to the customer");
      await expect(field).toHaveAccessibleDescription("The customer sees this on their quote.");
      const hint = page.getByTestId("qb-terms-notes-hint");
      await expect(hint).toBeVisible();
      await expect(hint).toHaveText("The customer sees this on their quote.");
      const fieldBox = (await field.boundingBox())!;
      const hintBox = (await hint.boundingBox())!;
      expect(hintBox.y).toBeGreaterThanOrEqual(fieldBox.y + fieldBox.height - 1);
    });
  }
}
