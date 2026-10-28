// The harness interface leg (W2-4 (a)): the three names the later parts
// write against, proven on the world under test.
//
// THE INTERFACE (W2-4-HARNESS-INTERFACE-r1). A part's spec imports only
// asStaff, coreURL and the StaffRole type from "./session.js". This leg is
// what holds that promise to the truth: asStaff puts the world's own session
// on the context and never one signed here, coreURL is the core's own base
// URL, and a role the world does not name is refused in words rather than
// faked with an invented identity.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test, expect } from "@playwright/test";
import { asStaff, coreURL, type StaffRole } from "./session.js";

const real = (): boolean => process.env.MDREAL_CONFIG === "1";

test("asStaff lands the world's own Financial Controller, and coreURL is where the core answers", async ({ page }) => {
  await asStaff(page.context(), "financial_controller");
  await page.goto(real() ? "./" : "/");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Approvals", { timeout: 30_000 });
  await expect(page.getByText("Financial Controller").first()).toBeVisible();
  // The core's own base URL: it answers there.
  const health = await page.request.get(`${coreURL()}/healthz`);
  expect(health.status()).toBe(200);
});

test("a role the world does not name is refused in words", async ({ page }) => {
  // The General Manager is named by neither world today.
  await expect(asStaff(page.context(), "general_manager")).rejects.toThrow(/general_manager/);
  if (real()) {
    // The box provisions the Financial Controller alone today, so the
    // desk's reader is refused here in words as well.
    await expect(asStaff(page.context(), "bookkeeper")).rejects.toThrow(/bookkeeper/);
  } else {
    // The stub names the desk's reader, the role the interface calls the
    // bookkeeper, and mints it.
    const role: StaffRole = "bookkeeper";
    await asStaff(page.context(), role);
  }
});

test("the session on the context is the world's own, not an invented identity", async ({ page }) => {
  test.skip(!real(), "the stub's identities are the demo ones: there is no provisioned subject to compare");
  const state = JSON.parse(
    readFileSync(join(process.env.MDREAL_STATE ?? "", "state.json"), "utf8"),
  ) as { subject: string; email: string };
  await asStaff(page.context(), "financial_controller");
  await page.goto("./");
  // The console build has no front door reader, so the session asStaff put on
  // the context is still there to be read.
  const planted = await page.evaluate(() => window.sessionStorage.getItem("hh-demo.front-door.session.v1"));
  expect(planted).not.toBeNull();
  const record = JSON.parse(planted ?? "{}") as { token?: string; user?: { email?: string } };
  const claims = JSON.parse(Buffer.from((record.token ?? "").split(".")[1] ?? "", "base64url").toString("utf8")) as { sub?: string };
  // The core minted this for the subject the box provisioned.
  expect(claims.sub).toBe(state.subject);
  expect(record.user?.email).toBe(state.email);
  // Not one of the stand-ins' invented identities.
  expect(record.user?.email).not.toBe("financial.controller@hh-demo.invalid");
});
