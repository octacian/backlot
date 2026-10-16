import { test, expect, chromium } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';

export function flow(viewport) {
  test('reject missing trust; interact over canonical HTTPS; recover an error', async ({ page, request }, info) => {
    await page.setViewportSize(viewport);
    const untrusted = await mkdtemp(join(tmpdir(), 'bl-untrusted-'));
    let browser;
    try {
      browser = await chromium.launch({ env: { ...process.env, HOME: untrusted,
        XDG_CONFIG_HOME: join(untrusted, '.config'), XDG_DATA_HOME: join(untrusted, '.local/share') } });
      const missing = await browser.newPage();
      await expect(missing.goto(process.env.APP_ORIGIN)).rejects.toThrow(/ERR_CERT_AUTHORITY_INVALID/);
    } finally { await browser?.close(); await rm(untrusted, { recursive: true, force: true }); }
    const env = { ...process.env };
    delete env.NODE_EXTRA_CA_CERTS;
    delete env.NODE_USE_SYSTEM_CA;
    const rejection = execFileSync(process.execPath, ['--input-type=module', '-e',
      `import https from 'node:https'; https.get(process.argv[1], () => process.exitCode=2).on('error', e => console.log(e.code));`,
      process.env.APP_ORIGIN], { env, encoding: 'utf8' });
    expect(rejection).toMatch(/UNABLE_TO_GET_ISSUER_CERT_LOCALLY|UNABLE_TO_VERIFY_LEAF_SIGNATURE/);
    // Playwright's API runner uses Node roots independently of browser NSS.
    const api = await request.get('/api/stack');
    expect(api.ok()).toBeTruthy();
    const { stack } = await api.json();
    const worker = `w-${info.workerIndex}-${info.testId.replace(/[^a-zA-Z0-9-]/g, '').slice(-60)}`;
    expect((await request.get('/api/notes/' + worker)).status()).toBe(404);
    await page.goto('/?worker=' + worker);
    await expect(page.getByRole('heading')).toHaveText('Job-local HTTPS notes');
    await expect(page.locator('#ssr')).toHaveText('Native SSR stack: ' + stack);
    await page.getByRole('textbox', { name: 'Note' }).fill('reject-save');
    // Delay a real request to observe loading, then exercise the application's error UI.
    let release;
    let entered;
    const waiting = new Promise(resolve => { entered = resolve; });
    const gate = new Promise(resolve => { release = resolve; });
    await page.route('**/api/notes/*', async route => { entered(); await gate; await route.continue(); });
    await page.getByRole('button', { name: 'Save note' }).click();
    await waiting;
    await expect(page.getByRole('status')).toHaveText('Saving…');
    release();
    await expect(page.getByRole('status')).toHaveText('Save failed. Try again.');
    await page.unroute('**/api/notes/*');
    await page.getByRole('textbox', { name: 'Note' }).fill('hello-' + worker);
    await page.getByRole('button', { name: 'Save note' }).click();
    await expect(page.getByRole('status')).toHaveText('Saved: hello-' + worker);
    expect(await (await request.get('/api/notes/' + worker)).text()).toBe('hello-' + worker);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
    await info.attach('stack', { body: JSON.stringify({ stack, origin: process.env.APP_ORIGIN, worker,
      browser: page.context().browser().version(), viewport }), contentType: 'application/json' });
  });
}
