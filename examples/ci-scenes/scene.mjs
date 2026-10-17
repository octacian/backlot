import { spawn, execFileSync } from 'node:child_process';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

await rm('reports', { recursive: true, force: true });
await mkdir('reports', { recursive: true });
const args = process.argv.slice(2);
await writeFile('reports/invocation.json', JSON.stringify({ args, origin: process.env.APP_ORIGIN }));
const home = join(process.env.APP_DATA, 'browser-home');
const db = join(home, '.pki/nssdb');
await mkdir(db, { recursive: true, mode: 0o700 });
execFileSync('/usr/bin/certutil', ['-N', '--empty-password', '-d', 'sql:' + db]);
execFileSync('/usr/bin/certutil', ['-A', '-d', 'sql:' + db, '-n', 'backlot-job-public-root',
  '-t', 'C,,', '-i', process.env.NODE_EXTRA_CA_CERTS]);
// Playwright alone interprets selectors and --shard; no application scheduler.
const child = spawn(process.execPath, ['node_modules/@playwright/test/cli.js', 'test', ...args], {
  stdio: 'inherit', env: { ...process.env, BROWSER_HOME: home }
});
const code = await new Promise((resolve, reject) => {
  child.on('error', reject);
  child.on('exit', (code, signal) => resolve(code ?? (signal === 'SIGINT' ? 130 : 143)));
});
process.exitCode = code;
