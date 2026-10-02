// The restart leg: the box was restarted by the exit script between this spec
// and the rest of the run. The demo issuer holds its signing key in memory
// alone, so the restart's new key refuses every token minted before it: the
// pre restart bearer (CT_PRIOR_TOKEN, minted by the exit script before the
// restart) is dead, the till is nobody's, and a fresh sign in works. This is
// the proof behind the testers' page's "a restart signs everyone out".
import { TRENTON_ID, apiPicture, expect, shotParts, signIn, staffGet, test } from "./helpers.js";

test("the pre restart bearer is refused, and a fresh sign in works", async ({ page }, testInfo) => {
  const prior = process.env.CT_PRIOR_TOKEN ?? "";
  expect(prior, "the exit script mints the pre restart token").not.toBe("");
  const dead = await staffGet(
    page,
    prior,
    `/api/v1/pos/parked?branch_id=${TRENTON_ID}`,
  );
  expect(dead.status).toBe(401);

  // The till itself is fresh: nobody is signed in, and the cards answer.
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  await expect(page.getByTestId("signed-in-role")).toHaveCount(0);
  await signIn(page, "Sales");
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales");
  // The picture holds both halves of the claim: the core's refusal of the
  // pre restart bearer, as the browser shows it, and the till signed back in,
  // loaded, as a fresh sign in leaves it.
  const refusal = await apiPicture(page, prior, `/api/v1/pos/parked?branch_id=${TRENTON_ID}`);
  const till = await page.screenshot({ fullPage: true });
  await shotParts(page, "90-restart-signed-back-in", testInfo, [refusal, till]);
});
