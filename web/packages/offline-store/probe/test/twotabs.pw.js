import { test, expect } from '@playwright/test';

// Two tabs of one origin open the same AccessHandlePoolVFS directory.
test('second tab while the first holds the database', async ({ context }) => {
  const a = await context.newPage();
  await a.goto('/');
  await expect(a.locator('#out')).toHaveAttribute('data-state', 'ready');
  await a.click('#write');
  await expect(a.locator('#out')).toHaveAttribute('data-rows', '1');

  const b = await context.newPage();
  await b.goto('/');
  await expect(b.locator('#out')).toHaveAttribute('data-state', /ready|error/);
  const state = await b.locator('#out').getAttribute('data-state');
  console.log('TWO_TABS second tab state:', state, '|', (await b.locator('#out').textContent()).slice(0, 160));
  expect(state).toBe('error');

  // Once the first tab is closed the second can open it and read the row.
  await a.close();
  await b.reload();
  await expect(b.locator('#out')).toHaveAttribute('data-state', 'ready');
  await expect(b.locator('#out')).toHaveAttribute('data-rows', '1');
});
