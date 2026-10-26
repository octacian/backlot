import { defineConfig } from '@playwright/test';
import { writeFileSync } from 'node:fs';

const baseURL = `http://127.0.0.1:${process.env.APP_PORT}`;
// Workers also load this file; only the actual CLI owns invocation evidence.
if (process.argv[2] === 'test') {
  writeFileSync('reports/invocation.json', JSON.stringify({ args: process.argv.slice(3), baseURL }));
}

export default defineConfig({
  testDir: './tests', workers: 1, retries: 0, timeout: 15_000,
  outputDir: 'reports/test-results',
  reporter: [['list'], ['json', { outputFile: 'reports/playwright.json' }],
    ['html', { outputFolder: 'reports/html', open: 'never' }]],
  use: { baseURL, browserName: 'chromium', trace: 'on', screenshot: 'on' }
});
