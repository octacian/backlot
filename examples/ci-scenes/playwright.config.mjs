import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests', fullyParallel: true, workers: 2, retries: 0,
  timeout: 30_000,
  outputDir: 'reports/test-results',
  reporter: [['list'], ['json', { outputFile: 'reports/playwright.json' }],
    ['html', { outputFolder: 'reports/html', open: 'never' }]],
  use: { baseURL: process.env.APP_ORIGIN, trace: 'on', screenshot: 'on',
    launchOptions: { env: { ...process.env, HOME: process.env.BROWSER_HOME,
      XDG_CONFIG_HOME: process.env.BROWSER_HOME + '/.config', XDG_DATA_HOME: process.env.BROWSER_HOME + '/.local/share' } } }
});
