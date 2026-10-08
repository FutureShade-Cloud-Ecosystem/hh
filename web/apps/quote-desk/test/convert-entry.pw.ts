// Round 11 item 1 (live check on the real server): every Convert to Order entry point opens the guided surface at
// #/quotes/<id>/convert. The bare convert (one POST, no standing, no Will-call, no payment before release, no
// confirm) is never reachable from a button, and nothing in src calls it.
import { expect, test } from "@playwright/test";
import { readdirSync, readFileSync } from "node:fs";
import { open, seed, signIn, writes } from "./switch-helpers.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("the Convert to Order button on the quote page opens the guided surface and converts nothing", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick", status: "ACCEPTED" });
  await signIn(page);
  await open(page, "Q-1004");
  const seen = writes(page);
  await page.getByTestId("qb-convert").click();
  await expect(page.getByTestId("qc-title")).toBeVisible();
  expect(new URL(page.url()).hash).toMatch(/^#\/quotes\/q-1004\/convert$/);
  await page.waitForTimeout(400);
  expect(seen.filter((w) => w.includes("/convert") || w.includes("/order-payment"))).toEqual([]);
  await expect(page.getByTestId("qc-done")).toHaveCount(0);
});

test("the Convert to order control in the quote detail bar opens the guided surface and converts nothing", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick", status: "ACCEPTED" });
  await signIn(page);
  await open(page, "Q-1004");
  await page.getByText("Quote detail & admin").click();
  const seen = writes(page);
  await expect(page.getByTestId("qe-convert")).toBeVisible();
  await page.getByTestId("qe-convert").click();
  await expect(page.getByTestId("qc-title")).toBeVisible();
  expect(new URL(page.url()).hash).toMatch(/^#\/quotes\/q-1004\/convert$/);
  await page.waitForTimeout(400);
  expect(seen.filter((w) => w.includes("/convert") || w.includes("/order-payment"))).toEqual([]);
});

test("a staff accept then Convert goes through the guided surface", async ({ page }) => {
  const sent = await page.request.post("/_stub/seed-quote", { data: { status: "SENT", customer_id: "c-fenwick" } });
  const id = ((await sent.json()) as { id: string }).id;
  await signIn(page);
  await page.evaluate((q) => {
    window.location.hash = `#/quotes/${q}`;
  }, id);
  await page.getByTestId("qb-decision-accept-open").click();
  await page.getByTestId("qb-decision-channel").selectOption("phone");
  await page.getByTestId("qb-decision-statement").check();
  await page.getByTestId("qb-decision-accept").click();
  await expect(page.getByTestId("qb-convert")).toBeVisible();
  await page.getByTestId("qb-convert").click();
  await expect(page.getByTestId("qc-title")).toBeVisible();
  await expect(page.getByTestId("qc-done")).toHaveCount(0);
  expect(new URL(page.url()).hash).toBe(`#/quotes/${id}/convert`);
});

test("no source file calls the bare convert", () => {
  const dir = new URL("../src/", import.meta.url);
  const hits: string[] = [];
  for (const name of readdirSync(dir)) {
    if (!name.endsWith(".ts")) continue;
    readFileSync(new URL(name, dir), "utf8").split("\n").forEach((line, i) => {
      if (/\.convertQuote\(|async convertQuote\(/.test(line)) hits.push(`${name}:${i + 1}`);
    });
  }
  expect(hits).toEqual([]);
});
