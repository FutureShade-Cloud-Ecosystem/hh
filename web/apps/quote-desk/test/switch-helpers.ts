// Shared by the quote switch race tests: sign in, seed quotes, and hold an answer back behind a gate the
// test opens, so the order of events is exact and never depends on how fast the machine is.
import { expect, type Page, type Request } from "@playwright/test";

export const SALES = { name: "Sales", exact: true };

export async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", SALES).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

export async function seed(page: Page, body: Record<string, unknown>): Promise<string> {
  const response = await page.request.post("/_stub/seed-quote", { data: body });
  return ((await response.json()) as { id: string }).id;
}

export function row(page: Page, quoteNo: string) {
  return page.getByTestId("qb-list-row").filter({ hasText: quoteNo }).first();
}

export async function open(page: Page, quoteNo: string): Promise<void> {
  await row(page, quoteNo).click();
  await expect(page.getByTestId("qb-context")).toContainText(quoteNo);
}

/** A held answer: the route waits on the gate, and the test opens it when the order of events is set. */
export function gate(): { opened: Promise<void>; open: () => void } {
  let release: () => void = () => undefined;
  const opened = new Promise<void>((resolve) => {
    release = resolve;
  });
  return { opened, open: release };
}

/**
 * Let the app's own continuations run after an answer has been delivered: the page's promise callbacks and the
 * render they cause, plus a short quiet stretch so a wrong late write has had its chance to show before a test
 * asserts that nothing changed.
 */
export async function settle(page: Page): Promise<void> {
  await page.evaluate(
    () => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => setTimeout(resolve, 150)))),
  );
}

/** Every write the page makes to a quote, in order: "PUT /api/v1/quotes/q-1004/terms {...}". */
export function writes(page: Page): string[] {
  const seen: string[] = [];
  page.on("request", (request: Request) => {
    const url = new URL(request.url());
    if (request.method() !== "GET" && url.pathname.startsWith("/api/v1/quotes/")) seen.push(`${request.method()} ${url.pathname} ${request.postData() ?? ""}`);
  });
  return seen;
}
