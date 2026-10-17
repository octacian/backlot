// Real outer-runner regression. Requires the job's already bootstrapped daemon.
import assert from 'node:assert/strict';
import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { readFile, writeFile, lstat } from 'node:fs/promises';
import { createServer, connect } from 'node:net';
import https from 'node:https';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { verifyReports } from './reports.mjs';

const exec = promisify(execFile);
const project = fileURLToPath(new URL('.', import.meta.url));
const root = (await readFile(process.env.BACKLOT_CI_POINTER, 'utf8')).trim();
assert.match(root, /^\/tmp\/bl-ci-[a-zA-Z0-9]+$/);
const state = join(root, 'd');
const ca = await readFile(join(state, 'gateway/data/pki/authorities/local/root.crt'));
async function cli(...args) {
  const result = await exec(resolve(process.env.BACKLOT_BINARY), [...args, '--state-dir', state, '--json'], { timeout: 30_000 });
  return JSON.parse(result.stdout);
}
async function absent(path) {
  try { await lstat(path); return false; }
  catch (error) { if (error.code === 'ENOENT') return true; throw error; }
}
const sentinel = createServer(socket => socket.end());
const clients = new Set();
const pending = new Set();
const observations = [];
try {
  await new Promise((resolve, reject) => { sentinel.on('error', reject); sentinel.listen(0, '127.0.0.1', resolve); });
  for (const [signal, expected] of [['SIGINT', 130], ['SIGTERM', 143]]) {
    const before = new Set((await cli('list')).instances.map(i => i.id));
    const events = [];
    let checkpoint;
    let stdout = '', stderr = '', buffer = '';
    const child = spawn(process.execPath, [join(project, 'ci.mjs'), 'acceptance'], { cwd: project, env: process.env });
    clients.add(child);
    const done = new Promise((resolve, reject) => {
      child.once('error', reject);
      child.once('close', (code, signal) => resolve({ code, signal }));
    });
    pending.add(done);
    done.then(() => { clients.delete(child); pending.delete(done); }, () => { clients.delete(child); pending.delete(done); });
    // A hung regression fails; signal only the retained outer handle. Workflow
    // authenticated cleanup then destroys every job-owned scene by recorded ID.
    const timer = setTimeout(() => child.kill('SIGKILL'), 60_000);
    child.stderr.on('data', data => { stderr += data; });
    child.stdout.on('data', data => {
      stdout += data;
      buffer += data;
      for (;;) {
        const end = buffer.indexOf('\n');
        if (end < 0) break;
        const line = buffer.slice(0, end); buffer = buffer.slice(end + 1);
        let event;
        try { event = JSON.parse(line); } catch { continue; }
        events.push(event);
        if (event.checkpoint === 'same-checkout clients waiting') {
          checkpoint = event;
          // This marker precedes an abortable no-command wait, after polling
          // has completed while both held and queued CLI clients are alive.
          child.kill(signal);
        }
      }
    });
    let result;
    try { result = await done; } finally { clearTimeout(timer); }
    assert.ok(checkpoint, stderr || stdout);
    assert.equal(result.signal, null, stderr);
    assert.equal(result.code, expected, stderr || stdout);
    const joined = events.filter(e => e.checkpoint === 'interrupted run client');
    assert.deepEqual(joined.map(e => e.id).sort(), [...checkpoint.ids].sort(), 'every retained CLI must join');
    for (const client of joined) {
      assert.equal(client.cli_exit, expected, JSON.stringify(client));
      assert.equal(client.client_signal, null, 'CLI handled cancellation rather than being killed');
    }
    const created = (await cli('list')).instances.filter(i => !before.has(i.id));
    assert.deepEqual(created.map(i => i.id).sort(), [...checkpoint.ids].sort(), 'runner allocated subsequent scenes');
    assert.ok(await absent(join(root, 'peer-worktree')), 'runner progressed to independent worktree allocation');
    const results = [];
    for (const item of created) {
      const instance = (await cli('inspect', item.id)).instance;
      assert.equal(instance.status, 'cancelled');
      assert.equal(instance.execution.cancelled, true);
      for (const key of ['failure', 'collection_failure', 'cleanup_failure']) assert.equal(instance.execution[key] ?? '', '');
      if (item.id === checkpoint.ids[1]) {
        assert.equal(instance.execution.terminal_exit_code, undefined, 'queued terminal never launched');
        assert.deepEqual(instance.execution.components, [], 'queued components never launched');
        assert.deepEqual(instance.execution.artifacts ?? [], [], 'queued run claimed peer reports');
        assert.ok(await absent(join(state, 'artifacts', item.id)), 'queued run created foreign invocation storage');
      }
      for (const fixture of instance.execution.fixtures ?? []) {
        if (fixture.name === 'data') assert.ok(await absent(fixture.value), 'cancelled data retained');
      }
      if (instance.execution.origin) {
        await new Promise((resolve, reject) => {
          const req = https.get(new URL('/api/stack', instance.execution.origin), { ca }, res => {
            let body = ''; res.on('data', data => { body += data; });
            res.on('end', () => {
              try { assert.equal(res.headers['backlot-instance'], undefined); assert.equal(body, ''); resolve(); }
              catch (error) { reject(error); }
            });
            res.on('error', reject);
          });
          req.setTimeout(5000, () => req.destroy(new Error('route check timeout')));
          req.on('error', reject);
        });
      }
      results.push({ id: instance.id, cancelled: true, terminal_exit: instance.execution.terminal_exit_code,
        artifacts: instance.execution.artifacts?.map(a => a.name) ?? [] });
    }
    // The first client actually ran Chromium; cancellation must retain its
    // invocation/holding evidence. The queued client has no terminal job yet.
    const first = (await cli('inspect', checkpoint.ids[0])).instance;
    const reports = first.execution.artifacts.find(a => a.name === 'reports');
    assert.ok(reports, 'held browser evidence missing');
    const invocation = await verifyReports(first, reports);
    assert.deepEqual(invocation.args, ['--config=failure.config.mjs', 'hold.spec.mjs']);
    assert.equal(JSON.parse(await readFile(join(reports.path, 'reports/holding.json'))).origin, first.execution.origin);
    await new Promise((resolve, reject) => {
      const socket = connect(sentinel.address().port, '127.0.0.1');
      socket.once('error', reject); socket.once('connect', () => { socket.destroy(); resolve(); });
    });
    observations.push({ signal, outer_exit: result.code, checkpoint: 'no-command wait with overlapping clients',
      clients: joined, results, no_subsequent_scenes: true, unrelated_listener_preserved: true });
    await writeFile(join(root, 'outer-cancellation.json'), JSON.stringify(observations, null, 2));
  }
  console.log(JSON.stringify(observations));
} finally {
  for (const child of clients) child.kill('SIGTERM');
  await Promise.allSettled([...pending]);
  await new Promise(resolve => sentinel.close(resolve));
}
