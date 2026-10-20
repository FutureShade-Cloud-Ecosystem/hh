// The Counter Till in console mode (item 7d): see packages/pos-kit/test/console-legs.ts for the legs.
import { expect } from "@playwright/test";
import { closeAccountMenu, openAccountMenu } from "./account-menu";
import { defineConsoleLegs } from "../../../packages/pos-kit/test/console-legs.js";

defineConsoleLegs({
  app: "Counter Till",
  role: { role_code: "inside_sales", role_name: "Sales", user_id: "demo-account-sales" },
  branchPath: "/api/v1/branches",
  persona: "sales",
  personaCard: "Sales",
  signedIn: async (page, roleName) => {
    await openAccountMenu(page);
    await expect(page.getByTestId("signed-in-role")).toHaveText(roleName, { timeout: 20_000 });
    await closeAccountMenu(page);
    await expect(page.getByRole("button", { name: "Sales", exact: true })).toHaveCount(0);
  },
  unavailable: async (page) => {
    await expect(page.getByText("The demo roles could not be loaded", { exact: false })).toBeVisible({ timeout: 20_000 });
    await openAccountMenu(page);
    await expect(page.getByTestId("signed-in-role")).toHaveCount(0);
    await closeAccountMenu(page);
  },
});
