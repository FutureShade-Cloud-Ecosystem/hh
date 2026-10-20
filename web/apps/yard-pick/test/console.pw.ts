// Yard Pick in console mode (item 7d): see packages/pos-kit/test/console-legs.ts for the legs.
import { expect } from "@playwright/test";
import { defineConsoleLegs } from "../../../packages/pos-kit/test/console-legs.js";

defineConsoleLegs({
  app: "Yard Pick",
  role: { role_code: "yard_team", role_name: "Yard Team", user_id: "d0200000-0000-4000-9000-000000000002" },
  persona: "yard-team",
  personaCard: "Yard Team",
  signedIn: async (page, roleName) => {
    await expect(page.locator("hh-field-frame")).toHaveAttribute("signed-in", "", { timeout: 20_000 });
    await expect(page.getByTestId("header-word")).toHaveText(roleName);
    await expect(page.locator("[data-branch]")).toBeVisible();
  },
  unavailable: async (page) => {
    await expect(page.getByText("The demo way in is not available on this box.")).toBeVisible({ timeout: 20_000 });
    await expect(page.locator("hh-field-frame")).not.toHaveAttribute("signed-in", "");
  },
});
