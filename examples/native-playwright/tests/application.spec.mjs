import { test, expect } from '@playwright/test';

test('counter interaction', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Counter' })).toBeVisible();
  await page.getByRole('button', { name: '0', exact: true }).click();
  await expect(page.getByRole('button', { name: '1', exact: true })).toBeVisible();
});
