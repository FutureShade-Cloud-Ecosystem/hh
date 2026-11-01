// The resolve leg: a shortage the yard team flagged, resolved end to end
// through the core's own resolve route, with the queue's count dropping
// after it. The seed's two open shortages live on one Point Loma pick: the
// Class 2 base rock shortfall ("Only 8 yards left in the bin") and the HPB dust
// one ("Pile is down to fines").
import { test, expect } from "@playwright/test";
import { mint, staffGet } from "./helpers.js";

test("a shortage resolves end to end and the count drops", async ({ page }) => {
  await page.goto("/home/");
  await page.locator("[data-flow='yard-manager']").click();
  const needsCount = page.locator("[data-widget='needs.you'] [data-needs='tasks.open_shortages'] .needs-count");
  await expect(needsCount).toHaveText("2");

  await page.locator("[data-needs='tasks.open_shortages'] button", { hasText: "Resolve" }).click();
  await expect(page.getByRole("heading", { name: "Shortages · open" })).toBeVisible();

  // The view lists what the yard flagged, with the resolver words beside it. Both seeded
  // shortages are here, on the one Point Loma pick, in the queue's own order. Each card is
  // named by its own words: a locator over every shortage card would be a strict mode
  // violation the moment the world holds two of them, which is what it holds.
  await expect(page.locator(".shortage[data-shortage]", { hasText: "Only 8 yards left in the bin" })).toHaveCount(1);
  await expect(page.locator(".shortage[data-shortage]", { hasText: "Pile is down to fines" })).toHaveCount(1);
  await expect(page.locator("[data-test='resolver-words']")).toHaveText(
    "Shortages are resolved by the yard team and the yard manager.",
  );

  // The resolve: the first open shortage, one of the three stated resolutions, with a note.
  const first = page.locator(".shortage[data-shortage]:has([data-action='open-resolve'])").first();
  const taskId = (await first.getByRole("button", { name: "Resolve" }).getAttribute("data-task-id")) ?? "";
  expect(taskId).not.toBe("");
  const seeded = ["Only 8 yards left in the bin", "Pile is down to fines"];
  const clearedId = (await first.getAttribute("data-shortage")) ?? "";
  expect(clearedId).not.toBe("");
  const firstText = await first.innerText();
  const clearedWords = seeded.find((words) => firstText.includes(words)) ?? "";
  expect(clearedWords, "the first open card is one of the two seeded shortages").not.toBe("");
  const otherWords = seeded.find((words) => words !== clearedWords) ?? "";
  await first.getByRole("button", { name: "Resolve" }).click();
  await page.getByRole("button", { name: "Backorder the shortfall" }).click();
  await page.locator("[data-field='resolution-note']").fill("Customer ok with Thursday");
  await page.locator("[data-action='confirm-resolve']").click();

  await expect(page.locator("[data-test='shortages-notice']")).toContainText("1 still open");
  // The sheet keeps what the manager just resolved: the cleared shortage stays on its card as a done
  // row with its resolution words, and the other seeded shortage is still open with its Resolve button.
  // The core's own pick read, below, proves the core recorded the resolve.
  const resolvedWords = "Backordered the shortfall; the order keeps the short line open. Customer ok with Thursday";
  // The done row does not repeat the note, so it is found by its own id, not by the note's words.
  const clearedRow = page.locator(`.shortage[data-shortage="${clearedId}"]`);
  await expect(clearedRow).toHaveCount(1);
  await expect(clearedRow.locator(".resolved-word")).toHaveText(resolvedWords);
  await expect(clearedRow.getByRole("button", { name: "Resolve" })).toHaveCount(0);
  const otherRow = page.locator(".shortage[data-shortage]", { hasText: otherWords });
  await expect(otherRow).toHaveCount(1);
  await expect(otherRow.getByRole("button", { name: "Resolve" })).toBeVisible();
  const bearer = await mint(page, "yard-manager");
  const pick = await staffGet(page, bearer, `/api/v1/yard/picks/${taskId}`);
  expect(pick.status).toBe(200);
  const history = ((pick.body as { shortages?: Array<{ note?: string; resolved_at?: string | null; resolution?: string }> }).shortages ?? []);
  // The cleared shortage by its own seeded note (the words above are each note's opening words), never
  // the first resolved row the pick happens to hold.
  const cleared = history.find((s) => typeof s.note === "string" && s.note.startsWith(clearedWords));
  expect(cleared, "the core's pick read holds the cleared shortage").toBeDefined();
  expect(cleared?.resolved_at, "the core records the shortage as resolved").toBeTruthy();
  expect(cleared?.resolution).toBe(resolvedWords);
  // Back on the landing, the queue's own count dropped: 2 became 1.
  await page.getByRole("button", { name: "Back to Home" }).click();
  await expect(needsCount).toHaveText("1");
});
