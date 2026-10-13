// The Quotes surface: the dealer's quotes, reviewed, rejected, converted; the
// acceptance happens on the dealer's link and the core says so. Stub: the real
// surface is this file's own commit.
import type { Page, SurfaceAnswer } from "./views.js";
import { html, type TemplateResult } from "lit";

export const QUOTES_EMPTY = "No quotes yet. Submit an RFQ and the dealer's quotes land here.";

export async function quotesLoad(page: Page): Promise<SurfaceAnswer> {
  void page;
  return { kind: "empty" };
}

export function quotesHeading(data: unknown): string {
  void data;
  return "Quotes";
}

export function quotesView(data: unknown, page: Page): TemplateResult {
  void data;
  void page;
  return html``;
}

export function quotesCommand(action: string, control: Element, page: Page): boolean {
  void action;
  void control;
  void page;
  return false;
}
