// The Logistics Manager's world leg and the four legs every role carries (the roadmap's section 2):
// the card, the wall, the branch and the cost, read from the demo image's own core as the rules name
// them. The world these legs assume is the seeded one: the Logistics Manager's mint row and its one
// branch grant (flows.go and populate.go). A world without them fails loudly.
import { expect, myBranch, staff, test, shot, arriveFromFrontDoor } from "./helpers.js";

test("L-card: the front door lists the Logistics Manager, with its role code and label", async ({ page }) => {
  const flows = await page.request.get("/api/v1/demo/flows");
  expect(flows.ok()).toBe(true);
  const body = (await flows.json()) as { flows: Array<{ flow: string; role_code: string; label: string }> };
  const row = body.flows.find((f) => f.flow === "logistics-manager");
  expect(row, "the flows table carries the Logistics Manager's row").toBeTruthy();
  expect(row?.role_code).toBe("logistics_manager");
  expect(row?.label).toBe("Logistics Manager");
});

test("L-card: the card lands on the load board at the dealer's main yard", async ({ page }, testInfo) => {
  await arriveFromFrontDoor(page);
  await expect(page.locator("[data-card='card.deliveries']")).toBeVisible();
  await expect(page.locator("[data-branch-name]")).toHaveText("Point Loma");
  await expect(page.getByTestId("header-word")).toHaveText("Logistics Manager");
  await shot(page, "10-load-board", testInfo);
});

test("L-wall: a surface at None for the Logistics Manager is refused by the server", async ({ page }) => {
  const branch = await myBranch(page);
  const counter = await staff(page, "POST", "/api/v1/pos/counter-prices", { branch_id: branch, customer_id: "", items: [] });
  expect(counter.status).toBe(403);
  const rules = await staff(page, "POST", "/api/v1/pricing/rules", {});
  expect(rules.status).toBe(403);
});

test("L-branch: the Logistics Manager reads its own branch only", async ({ page }) => {
  const branch = await myBranch(page);
  const board = await staff(page, "GET", `/api/v1/dispatch/loadboard?branch_id=${branch}`);
  expect(board.status).toBe(200);
  expect(board.body.branch_id).toBe(branch);
  const all = await staff(page, "GET", "/api/v1/dispatch/loadboard");
  // With no branch named the core answers the caller's own branch, never another yard's.
  if (all.status === 200) expect(all.body.branch_id).toBe(branch);
  const other = await staff(page, "GET", "/api/v1/dispatch/loadboard?branch_id=5d1b3f6e-0000-4000-9000-000000000002");
  expect([403, 404]).toContain(other.status);
});

test("L-cost: no figure rides a load the dealer's policy hides, and the screen draws none it was not sent", async ({ page }) => {
  const branch = await myBranch(page);
  const board = await staff(page, "GET", `/api/v1/dispatch/loadboard?branch_id=${branch}`);
  expect(board.status).toBe(200);
  if (board.body.value_visible === false) {
    expect(board.text).not.toContain("value_cents");
    expect(board.text).not.toContain("freight_cents");
  }
  await arriveFromFrontDoor(page);
  await expect(page.locator("[data-card='card.deliveries']")).toBeVisible();
  const text = await page.locator("main").innerText();
  if (board.body.value_visible === false) expect(text).not.toMatch(/\$\d/);
});
