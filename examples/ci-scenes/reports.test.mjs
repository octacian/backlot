import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { execFileSync } from 'node:child_process';
import { verifyReports } from './reports.mjs';

test('application media, compressed trace/network records and bound cancellation', async t => {
  const root = await mkdtemp(join(tmpdir(), 'backlot-report-check-'));
  t.after(() => rm(root, {recursive: true, force: true}));
  const origin = 'https://fixture.backlot.localhost';
  const args = ['--config=failure.config.mjs', 'hold.spec.mjs'];
  const instance = {
    plan: {terminal_job: 'browser', components: [{name: 'browser', command: {args: ['scene.mjs', ...args]}}]},
    execution: {attempt: 'attempt', origin, cancelled: false, components: [{name: 'browser', status: 'completed'}]}
  };
  const artifact = {name: 'reports', component: 'browser', attempt: 'attempt', path: root};
  const reports = join(root, 'reports');
  await mkdir(reports);
  await writeFile(join(reports, 'invocation.json'), JSON.stringify({origin, args}));
  const final = () => writeFile(join(reports, 'playwright.json'), JSON.stringify({suites: [], stats: {}}));
  // ZIP fixtures are produced by the standard writer, not hand-built local
  // headers that could make a broken reader and fixture agree by construction.
  function zip(mode) {
    execFileSync('python3', ['-c', `
import sys,zipfile
root,mode=sys.argv[1:]
with zipfile.ZipFile(root+'/trace.zip','w',compression=zipfile.ZIP_DEFLATED) as z:
 z.writestr('0.trace', 'broken' if mode=='trace' else '{"type":"context-options"}\\n')
 z.writestr('0.network', '{' if mode=='network' else 'NaN' if mode=='non-json' else '{"type":"resource-snapshot"}\\n')
 z.writestr('resources/frame.png', b'bad' if mode=='zip-png' else b'\\x89PNG\\r\\n\\x1a\\n')
`, reports, mode]);
  }
  await final(); zip('valid');
  await writeFile(join(reports, 'screen.png'), Buffer.from('89504e470d0a1a0a', 'hex'));
  await verifyReports(instance, artifact);
  for (const mode of ['trace', 'network', 'non-json', 'zip-png']) {
    zip(mode);
    await assert.rejects(verifyReports(instance, artifact), /invalid (NDJSON|PNG)/);
  }
  zip('valid');
  await writeFile(join(reports, 'screen.png'), 'not PNG');
  await assert.rejects(verifyReports(instance, artifact), /invalid PNG/);
  await rm(join(reports, 'screen.png'));
  await writeFile(join(reports, 'trace.zip'), 'not ZIP');
  await assert.rejects(verifyReports(instance, artifact), /BadZipFile/);
  zip('valid');
  await rm(join(reports, 'playwright.json'));
  await assert.rejects(verifyReports(instance, artifact), /ENOENT/);
  instance.execution.cancelled = true;
  await assert.rejects(verifyReports(instance, artifact), /ENOENT/);
  await writeFile(join(reports, 'holding.json'), JSON.stringify({origin: 'wrong'}));
  await assert.rejects(verifyReports(instance, artifact), /holding report/);
  await writeFile(join(reports, 'holding.json'), JSON.stringify({origin}));
  await verifyReports(instance, artifact); // Only this bound cancellation permits absence.
  zip('network');
  await assert.rejects(verifyReports(instance, artifact), /invalid NDJSON/);
  zip('valid');
  await writeFile(join(reports, 'playwright.json'), '{');
  await assert.rejects(verifyReports(instance, artifact), SyntaxError);
  await writeFile(join(reports, 'playwright.json'), JSON.stringify({suites: [], stats: 'wrong'}));
  await assert.rejects(verifyReports(instance, artifact), assert.AssertionError);
  await final();
  await writeFile(join(reports, 'invocation.json'), JSON.stringify({origin, args: ['other']}));
  await assert.rejects(verifyReports(instance, artifact), /argument vector/);
});
