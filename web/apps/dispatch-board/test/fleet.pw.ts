// The trucks and drivers registry on the stub: the lists with what is out of use, a truck and a driver
// added, changed and taken out of use and brought back, the units in pounds and tons, the core's words
// on a refusal with the person's typing kept, the role gate's answer below the manage level, both widths.
import { test, expect, openBoard, stubPost } from "./helpers.js";
import type { Page } from "@playwright/test";

const truckCard = (page: Page, code: string) => page.locator(`[data-truck-code='${code}']`);
const driverCard = (page: Page, name: string) => page.locator(`[data-driver-name='${name}']`);
const sheetField = (page: Page, name: string) => page.locator(`[data-sheet] [data-field='${name}']`);

async function openFleet(page: Page, request: Parameters<typeof openBoard>[1]): Promise<void> {
  await openBoard(page, request);
  await page.locator("[data-tab-id='fleet']").first().click();
  await expect(page.locator("[data-trucks]")).toBeVisible();
}

test("the registry lists every truck and driver with its facts in the yard's units", async ({ page, request }) => {
  await openFleet(page, request);
  await expect(page.locator("[data-tab-id='fleet']").first()).toHaveAttribute("aria-current", "true");
  await expect(page.locator("[data-trucks] li[data-truck-id]")).toHaveCount(4);
  await expect(page.locator("[data-drivers] li[data-driver-id]")).toHaveCount(3);
  await expect(truckCard(page, "Truck 01")).toContainText("Flatbed");
  await expect(truckCard(page, "Truck 01")).toContainText("9.9 tons");
  await expect(truckCard(page, "Truck 04")).toContainText("2,425 lb");
  await expect(truckCard(page, "Truck 01")).toContainText("In use");
  await expect(driverCard(page, "Driver A (demo)")).toContainText("Licence A");
  await expect(driverCard(page, "Driver A (demo)")).toContainText("No staff login");
  await expect(page.locator("main")).not.toContainText(" kg");
  await expect(page.locator("[data-action='new-run']")).toHaveCount(0);
});

test("a truck is added in pounds, shows in tons, and a code that is taken is refused in the core's words with the typing kept", async ({ page, request }) => {
  await openFleet(page, request);
  await page.locator("[data-trucks]").getByRole("button", { name: "Add truck" }).click();
  await sheetField(page, "code").fill("Truck 01");
  await sheetField(page, "name").fill("A second flatbed");
  await sheetField(page, "capacity").fill("20000");
  await page.locator("[data-sheet]").getByRole("button", { name: "Add truck" }).click();
  await expect(page.locator("[data-sheet-error]")).toHaveText("a truck with that code already exists at this branch");
  await expect(sheetField(page, "name")).toHaveValue("A second flatbed");
  await expect(sheetField(page, "capacity")).toHaveValue("20000");
  await sheetField(page, "code").fill("Truck 05");
  await page.locator("[data-sheet]").getByRole("button", { name: "Add truck" }).click();
  await expect(page.locator("[data-notice]")).toHaveText("Truck 05 added to the trucks.");
  await expect(truckCard(page, "Truck 05")).toContainText("10 tons");
  await expect(truckCard(page, "Truck 05")).toContainText("A second flatbed");
  await expect(page.locator("[data-trucks] li[data-truck-id]")).toHaveCount(5);
});

test("the sheet checks a blank code and a capacity that is not a number before the core is asked", async ({ page, request }) => {
  await openFleet(page, request);
  await page.locator("[data-trucks]").getByRole("button", { name: "Add truck" }).click();
  await page.locator("[data-sheet]").getByRole("button", { name: "Add truck" }).click();
  await expect(page.locator("[data-sheet-error]")).toHaveText("A truck needs a code, like Truck 05.");
  await sheetField(page, "code").fill("Truck 06");
  await sheetField(page, "capacity").fill("heavy");
  await page.locator("[data-sheet]").getByRole("button", { name: "Add truck" }).click();
  await expect(page.locator("[data-sheet-error]")).toHaveText("Capacity is a number of pounds, like 20000.");
});

test("changing a truck opens on what it holds, and saving it unchanged leaves its stored capacity alone", async ({ page, request }) => {
  await openFleet(page, request);
  await truckCard(page, "Truck 01").getByRole("button", { name: "Change" }).click();
  await expect(sheetField(page, "code")).toHaveValue("Truck 01");
  await expect(sheetField(page, "type")).toHaveValue("Flatbed");
  await expect(sheetField(page, "capacity")).toHaveValue("19842");
  await sheetField(page, "name").fill("Truck 01, longer deck");
  await page.locator("[data-sheet]").getByRole("button", { name: "Save" }).click();
  await expect(page.locator("[data-notice]")).toHaveText("Truck 01 saved.");
  await expect(truckCard(page, "Truck 01")).toContainText("Truck 01, longer deck");
  await expect(truckCard(page, "Truck 01")).toContainText("9.9 tons");
  const trucks = await (await request.get("/api/v1/dispatch/trucks", { headers: { Authorization: "Bearer x", "X-Org": "hh-demo" } })).json();
  expect(trucks.items.find((t: { code: string }) => t.code === "Truck 01").capacity_kg).toBe(9000);
});

test("a truck out of use leaves the pick lists and stays in the registry, and comes back", async ({ page, request }) => {
  await openFleet(page, request);
  await truckCard(page, "Truck 03").getByRole("button", { name: "Take out of use" }).click();
  await expect(page.locator("[data-notice]")).toHaveText("Truck 03 is out of use. It leaves the pick lists and stays on the books.");
  await expect(truckCard(page, "Truck 03")).toContainText("Out of use");
  await expect(truckCard(page, "Truck 03")).toHaveAttribute("data-active", "false");
  await page.locator("[data-tab-id='load']").first().click();
  await page.locator("[data-run-no='RUN-PLM-2']").locator("select[data-field='crew-truck']").waitFor();
  // The truck on a planned run stays shown, marked, so changing the driver cannot silently un-crew the truck.
  await expect(page.locator("[data-run-no='RUN-PLM-2']").locator("select[data-field='crew-truck'] option", { hasText: "Truck 03, out of use" })).toHaveCount(1);
  await expect(page.locator("[data-run-no='RUN-PLM-2']").locator("select[data-field='crew-truck']")).toHaveValue("Truck 03, out of use");
  await page.locator("[data-run-no='RUN-PLM-2']").locator("select[data-field='crew-driver']").selectOption({ index: 1 });
  await expect(page.locator("[data-notice]")).toHaveText("Crew set.");
  const kept = await (await request.get("/_stub/world")).json();
  expect(kept.runs.find((r: { run_no: string }) => r.run_no === "RUN-PLM-2").truck_code).toBe("Truck 03");
  await page.locator("[data-tab-id='fleet']").first().click();
  await truckCard(page, "Truck 03").getByRole("button", { name: "Put back in use" }).click();
  await expect(page.locator("[data-notice]")).toHaveText("Truck 03 is back in use.");
  await expect(truckCard(page, "Truck 03")).toContainText("In use");
});

test("a driver is added, linked to a staff login id, and the core's words answer a login that is not one", async ({ page, request }) => {
  await openFleet(page, request);
  await page.locator("[data-drivers]").getByRole("button", { name: "Add driver" }).click();
  await sheetField(page, "name").fill("Driver D (demo)");
  await sheetField(page, "login").fill("not-an-id");
  await page.locator("[data-sheet]").getByRole("button", { name: "Add driver" }).click();
  await expect(page.locator("[data-sheet-error]")).toContainText("A staff login id is a long id with dashes");
  await sheetField(page, "login").fill("d0200000-0000-4000-9000-000000000001");
  await sheetField(page, "phone").fill("619-555-0144");
  await page.locator("[data-sheet]").getByRole("button", { name: "Add driver" }).click();
  await expect(page.locator("[data-notice]")).toHaveText("Driver D (demo) added to the drivers.");
  await expect(driverCard(page, "Driver D (demo)")).toContainText("Has a staff login");
  await expect(driverCard(page, "Driver D (demo)")).toContainText("619-555-0144");
  await driverCard(page, "Driver D (demo)").getByRole("button", { name: "Take out of use" }).click();
  await expect(driverCard(page, "Driver D (demo)")).toContainText("Out of use");
  await expect(driverCard(page, "Driver D (demo)")).toContainText("Has a staff login");
});

test("below the manage level the core's gate answers, and the screen says who may", async ({ page, request }) => {
  await openFleet(page, request);
  await stubPost(request, "caller", { can_manage: false });
  await truckCard(page, "Truck 02").getByRole("button", { name: "Take out of use" }).click();
  await expect(page.locator("[data-error]")).toHaveText("That is not yours to do. The Logistics Manager or a Branch Manager can.");
  await expect(truckCard(page, "Truck 02")).toContainText("In use");
  await expect(page.locator("[data-fleet-note]")).toContainText("for the Logistics Manager and a Branch Manager");
});

for (const size of [{ width: 390, height: 844 }, { width: 1440, height: 900 }]) {
  test(`at ${size.width} px the registry never scrolls sideways and every control is at least 44 tall`, async ({ page, request }) => {
    await page.setViewportSize(size);
    await openFleet(page, request);
    await page.locator("[data-trucks]").getByRole("button", { name: "Add truck" }).click();
    const wide = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
    expect(wide).toBe(false);
    const small = await page.evaluate(() => {
      const root = document.querySelector("dispatch-board-app")?.shadowRoot;
      return [...(root?.querySelectorAll("[data-trucks] button, [data-trucks] input, [data-trucks] select, [data-drivers] button") ?? [])]
        .filter((el) => el.getBoundingClientRect().height < 44)
        .map((el) => el.textContent?.trim() || el.getAttribute("data-field") || "?");
    });
    expect(small).toEqual([]);
  });
}
