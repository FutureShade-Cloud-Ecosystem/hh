import { test, expect } from '@playwright/test';

// The shipped OPFS backend and worker under the real store, in a real browser.
test('store: pull and outbox survive a reload, a second tab is read cache only, and takes over when the first closes', async ({ context }) => {
  const a = await context.newPage();
  await a.goto('/store.html');
  await expect(a.locator('#out')).toHaveAttribute('data-state', 'ready');
  expect(await a.evaluate(() => window.hh.ownership())).toBe('owner');
  expect(await a.evaluate(() => window.hh.crossOriginIsolated())).toBe(false);

  const pulled = await a.evaluate(() => window.hh.pull());
  expect(pulled.kind).toBe('done');
  const queued = await a.evaluate(() => window.hh.enqueue());
  expect(queued.ok).toBe(true);
  expect(await a.evaluate(() => window.hh.pending())).toBe(1);

  // Durable across a reload: the row, the cursor, and the queued operation.
  await a.reload();
  await expect(a.locator('#out')).toHaveAttribute('data-state', 'ready');
  expect(await a.evaluate(() => window.hh.pending())).toBe(1);
  const listed = await a.evaluate(() => window.hh.rows());
  expect(listed.rows.length).toBe(1);
  expect(listed.staleness.lastSeq).toBe(5);
  expect(listed.readOnly).toBe(false);

  // A second tab degrades to read cache only, and refuses writes with a reason.
  const b = await context.newPage();
  await b.goto('/store.html');
  await expect(b.locator('#out')).toHaveAttribute('data-state', 'ready');
  expect(await b.evaluate(() => window.hh.ownership())).toBe('read_cache_only');
  await expect(b.locator('body')).toHaveAttribute('data-ownership', 'read_cache_only');
  const refused = await b.evaluate(() => window.hh.enqueue());
  expect(refused.ok).toBe(false);
  expect(refused.reason).toBe('read_only');
  expect(await a.evaluate(() => window.hh.pending())).toBe(1);

  // The owner replays: the outbox drains.
  const replay = await a.evaluate(() => window.hh.replay());
  expect(replay.kind).toBe('done');
  expect(await a.evaluate(() => window.hh.pending())).toBe(0);

  // The first tab closes; a reload of the second takes the store over and sees it all.
  await a.close();
  await b.reload();
  await expect(b.locator('#out')).toHaveAttribute('data-state', 'ready');
  expect(await b.evaluate(() => window.hh.ownership())).toBe('owner');
  expect(await b.evaluate(() => window.hh.pending())).toBe(0);
  expect((await b.evaluate(() => window.hh.rows())).rows.length).toBe(1);
});
