// Screenshots are an optional local aid: they are written only when QD_SHOT_DIR
// names a directory, and the call is a no-op when it is unset, so a CI run
// never writes outside the checkout and a local run never overwrites anything
// it was not pointed at.
import { mkdirSync } from "node:fs";
import type { Locator, Page } from "@playwright/test";

export async function shot(target: Page | Locator, name: string): Promise<void> {
  const dir = process.env.QD_SHOT_DIR;
  if (!dir) return;
  mkdirSync(dir, { recursive: true });
  await target.screenshot({ path: `${dir}/${name}.png` });
}
