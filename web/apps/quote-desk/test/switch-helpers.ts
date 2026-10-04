// Shared by the quote switch race tests: sign in, seed quotes, and hold an answer back behind a gate the
// test opens, so the order of events is exact and never depends on how fast the machine is.
import { expect, type Page, type Request } from "@playwright/test";

export const SALES = { name: "Sales", exact: true };

export async function signIn(page: Page, role: string = SALES.name): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
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

export interface Held {
  /** Resolves when the first matching request has reached the route and is being held. */
  asked: Promise<void>;
  /** Let the held request(s) go; the answer is the server's own, or the refusal given. */
  open: () => void;
  /** The held requests that have been opened, as "METHOD url". */
  seen: string[];
}

/**
 * Hold back the requests that match, until the test opens the gate. With `refuse` the answer, once opened, is that
 * refusal rather than the stub's own; with `times` only that many matching requests are held and the rest pass.
 */
export async function hold(
  page: Page,
  url: RegExp,
  options: {
    method?: string;
    times?: number;
    refuse?: { status: number; error: string };
    match?: (request: Request) => boolean;
    /** Ask the server at once and hold only the answer, so a late release delivers what the server said then. */
    snapshot?: boolean;
  } = {},
): Promise<Held> {
  const door = gate();
  const seen: string[] = [];
  let held = 0;
  let askedNow: () => void = () => undefined;
  const asked = new Promise<void>((resolve) => {
    askedNow = resolve;
  });
  await page.route(url, async (route) => {
    const request = route.request();
    const wanted = (options.method === undefined || request.method() === options.method) && (options.match === undefined || options.match(request));
    if (!wanted || (options.times !== undefined && held >= options.times)) {
      await route.continue();
      return;
    }
    held += 1;
    seen.push(`${request.method()} ${request.url()}`);
    askedNow();
    const taken = options.snapshot === true ? await route.fetch() : undefined;
    await door.opened;
    if (taken !== undefined) {
      await route.fulfill({ response: taken });
    } else if (options.refuse !== undefined) {
      await route.fulfill({ status: options.refuse.status, contentType: "application/json", body: JSON.stringify({ error: options.refuse.error }) });
    } else {
      await route.continue();
    }
  });
  return { asked, open: door.open, seen };
}
