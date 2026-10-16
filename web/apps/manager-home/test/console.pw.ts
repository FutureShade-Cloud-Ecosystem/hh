// Manager Home in console mode (item 7d): see packages/pos-kit/test/console-legs.ts for the legs.
import { expect } from "@playwright/test";
import { defineConsoleLegs } from "../../../packages/pos-kit/test/console-legs.js";

defineConsoleLegs({
  app: "Manager Home",
  role: { role_code: "sales_manager", role_name: "Sales manager", user_id: "de110000-0000-4000-9000-000000000002" },
  persona: "sales-manager",
  personaCard: "Sales manager",
  signedIn: async (page, roleName) => {
    await expect(page.locator("hos-app-header")).toHaveAttribute("signed-in", "", { timeout: 20_000 });
    await expect(page.getByTestId("header-word")).toHaveText(roleName);
    await expect(page.locator("[data-widget]").first()).toBeVisible();
  },
  unavailable: async (page) => {
    await expect(page.getByText("The demo roles could not be loaded", { exact: false })).toBeVisible({ timeout: 20_000 });
    await expect(page.locator("hos-app-header")).not.toHaveAttribute("signed-in", "");
  },
});
