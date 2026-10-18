import { test, expect } from '@playwright/test';
test('requested assertion failure retains a real browser trace', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading')).toHaveText('deliberately incorrect heading', { timeout: 500 });
});
