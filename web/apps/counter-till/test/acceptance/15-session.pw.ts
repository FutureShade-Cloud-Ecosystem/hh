// The drawer's own open. The acceptance world's drawer is opened through the
// API (the world leg's helper posts the session), so the drawer screens read
// back a session the harness made, and no test pressed what a tester presses:
// the surface's Open Session button. This spec closes the world's drawer
// through the core's own close flow, signs in on the closed drawer (what a
// clean box holds), and presses the button: the request it sends carries the
// reference's own float, and the till then shows the session open.
import { POINT_LOMA_ID, expect, expectedCash, mint, openCounterTill, staffGet, staffPost, tapRoleCard, test } from "./helpers.js";

const OPEN_PATH = `/api/v1/till/sessions/open?branch_id=${POINT_LOMA_ID}&station=MAIN`;

test("the closed drawer: the till's own Open Session button opens MAIN with the reference's float", async ({
  page,
}) => {
  const bearer = await mint(page, "sales");
  // The close flow the core serves: count the open session at its expected
  // cash (a zero variance count, so no manager sign-off is asked), then
  // complete it. If the box holds no open drawer the closed state stands
  // already; either way the lookup must answer closed before the press.
  const lookup = await staffGet(page, bearer, OPEN_PATH);
  expect(lookup.status).toBe(200);
  if (lookup.body.open === true) {
    const openId = lookup.body.id as string;
    const counted = await staffPost(page, bearer, `/api/v1/till/sessions/${openId}/count`, {
      tenders: [{ method: "CASH", actual_cents: await expectedCash(page, bearer, openId) }],
    });
    expect(counted.status).toBe(200);
    const completed = await staffPost(page, bearer, `/api/v1/till/sessions/${openId}/complete`, {});
    expect(completed.status).toBe(200);
  }
  const closed = await staffGet(page, bearer, OPEN_PATH);
  expect(closed.status).toBe(200);
  expect(closed.body.open).toBe(false);

  // A fresh sign in on the closed drawer: the front door's card tap lands on
  // the role home, the Counter Till tile opens the till, and the surface
  // shows its Open Session button and no session dot (the hydrate read
  // answers no open session).
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  await tapRoleCard(page, "Sales");
  await openCounterTill(page);
  await expect(page.getByRole("button", { name: "Open Session" })).toBeVisible();
  await expect(page.getByTestId("session-dot")).toHaveCount(0);

  // The press is the surface's own open, and the float it sends is the
  // reference's: hardscapeos_dibbits app/src/pos-terminal.ts line 2650
  // (master at 87d7941b) sends float_cents 30000 on its Open Session press,
  // and the ported surface keeps it (src/ported/pos-terminal.ts, openSession).
  // The world leg's API opened drawer carries the same float (see openDrawer),
  // so the suite asserts one truth.
  const posted = page.waitForResponse(
    (response) =>
      response.url().endsWith("/api/v1/till/sessions") && response.request().method() === "POST",
  );
  await page.getByRole("button", { name: "Open Session" }).click();
  const answer = await posted;
  expect(answer.ok()).toBe(true);
  const body = answer.request().postDataJSON() as {
    branch_id: string;
    station: string;
    float_cents: number;
  };
  expect(body.branch_id).toBe(POINT_LOMA_ID);
  expect(body.station).toBe("MAIN");
  expect(body.float_cents).toBe(30000);
  // The till shows the session open: the bar's dot, as a signed in till does.
  await expect(page.getByTestId("session-dot")).toContainText("Open", { timeout: 15_000 });
});
