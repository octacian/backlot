import { test, expect } from '@playwright/test';
import { writeFile } from 'node:fs/promises';
test('hold a real browser for external cancellation and lease expiry', async ({ page }) => {
  test.setTimeout(120_000);
  await page.goto('/');
  await expect(page.getByRole('heading')).toHaveText('Job-local HTTPS notes');
  await writeFile('reports/holding.json', JSON.stringify({ origin: process.env.APP_ORIGIN }));
  console.log('real browser holding');
  await new Promise(() => {});
});
