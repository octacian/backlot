import { test, expect } from '@playwright/test';
import { readFile, writeFile, rename } from 'node:fs/promises';

test('held browser', async ({ page, browser }) => {
  test.setTimeout(120_000);
  await page.goto('/');
  await expect(page.getByRole('heading')).toBeVisible();
  const session = await browser.newBrowserCDPSession();
  const { processInfo } = await session.send('SystemInfo.getProcessInfo');
  await session.detach();
  const processes = processInfo.filter(p => p.type === 'browser' || p.type === 'renderer');
  expect(processes.some(p => p.type === 'browser')).toBeTruthy();
  const server = JSON.parse(await readFile('reports/server.json', 'utf8'));
  await writeFile('reports/holding.tmp', JSON.stringify({ instance: process.env.RUN_ID, pid: process.pid,
    server_pid: server.pid, browser_pids: processes.map(p => p.id) }));
  await rename('reports/holding.tmp', 'reports/holding.json');
  await new Promise(() => {});
});
