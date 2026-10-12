import { test, expect } from '@playwright/test';

const vfs = process.env.PROBE_VFS ?? 'ahp';
const url = `/?vfs=${vfs}`;

test('outbox row survives a reload', async ({ page }) => {
  await page.goto(url);
  await expect(page.locator('#out')).toHaveAttribute('data-state', 'ready');
  await expect(page.locator('#out')).toHaveAttribute('data-rows', '0');
  await expect(page.locator('body')).toHaveAttribute('data-vfs', vfs === 'memory' ? 'memory' : 'ahp');
  await page.click('#write');
  await expect(page.locator('#out')).toHaveAttribute('data-rows', '1');
  expect(await page.evaluate(() => self.crossOriginIsolated)).toBe(false);

  await page.reload();
  await expect(page.locator('#out')).toHaveAttribute('data-state', 'ready');
  await expect(page.locator('#out')).toHaveAttribute('data-rows', '1');
  const rows = JSON.parse(await page.locator('#out').textContent());
  expect(Object.keys(rows[0])).toEqual(['seq', 'idempotency_key', 'scope', 'body', 'created_at']);
  expect(rows[0].scope).toBe('yard-pick');
});
