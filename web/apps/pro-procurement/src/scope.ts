// The Order scope surface: one procurement project's lines at the account
// price, the catalog picker, and the RFQ. Stub: the real surface is this
// file's own commit.
import type { Page, SurfaceAnswer } from "./views.js";
import { html, type TemplateResult } from "lit";

export async function scopeLoad(page: Page): Promise<SurfaceAnswer> {
  void page;
  return { kind: "empty" };
}

export function scopeHeading(data: unknown): string {
  void data;
  return "Order scope";
}

export function scopeView(data: unknown, page: Page): TemplateResult {
  void data;
  void page;
  return html``;
}

export function scopeCommand(action: string, control: Element, page: Page): boolean {
  void action;
  void control;
  void page;
  return false;
}
