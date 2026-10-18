// Runner-owned bootstrap and evidence export; lifecycle remains in Backlot.
import assert from 'node:assert/strict';
import { spawn, execFileSync } from 'node:child_process';
import { mkdir, writeFile, readFile, rm, rename } from 'node:fs/promises';
import { join, resolve, relative, isAbsolute } from 'node:path';
import { fileURLToPath } from 'node:url';
import { verifyReports } from './reports.mjs';
import { safeCopy as copyEvidence } from '../../actions/setup/evidence.mjs';
import { cleanup } from '../../actions/setup/runtime.mjs';

const project = fileURLToPath(new URL('.', import.meta.url));
const binary = resolve(process.env.BACKLOT_BINARY);
const pointer = resolve(process.env.BACKLOT_CI_POINTER);
const output = resolve(process.env.BACKLOT_CI_EXPORT);
const mode = process.argv[2];
let root;
const children = new Set();
const pending = new Set();
const cancellation = new AbortController();
function checkpoint() { cancellation.signal.throwIfAborted(); }
const safeCopy = (source, destination) => copyEvidence(source, destination, checkpoint);
let interrupted = 0;
for (const [signal, code] of [['SIGINT', 130], ['SIGTERM', 143]]) {
  process.on(signal, () => {
    if (interrupted) return; // Preserve the first requested signal result.
    interrupted = code;
    cancellation.abort(new Error(`runner interrupted by ${signal}`));
    for (const child of children) child.kill(signal);
  });
}
function command(args, options = {}) {
  checkpoint();
  const child = spawn(binary, args, { cwd: options.cwd ?? project, env: options.env ?? process.env });
  children.add(child);
  let stdout = '', stderr = '';
  child.stdout.on('data', chunk => { stdout += chunk; });
  child.stderr.on('data', chunk => { stderr += chunk; });
  const timer = setTimeout(() => child.kill('SIGTERM'), options.timeout ?? 180_000);
  const done = (async () => {
    try {
      const result = await new Promise((resolve, reject) => {
        child.on('error', reject);
        child.on('close', (code, signal) => resolve({ code, signal, stdout, stderr }));
      });
      if (interrupted && args[0] === 'run') {
        // Keep the client result distinct from the native terminal job result.
        let instance;
        try { instance = JSON.parse(stdout).instance; } catch { /* No final JSON if the CLI itself was killed. */ }
        console.log(JSON.stringify({ checkpoint: 'interrupted run client', id: instance?.id,
          cli_exit: result.code, client_signal: result.signal,
          terminal_exit: instance?.execution?.terminal_exit_code }));
      }
      if (options.ok !== false && result.code !== 0) throw new Error(`backlot ${args[0]} failed (${result.code}): ${stderr || stdout}`);
      return result;
    } finally { clearTimeout(timer); children.delete(child); }
  })();
  pending.add(done);
  done.then(() => pending.delete(done), () => pending.delete(done));
  options.onSpawn?.(child);
  return done;
}
const state = () => join(root, 'd');
const config = () => join(root, 'machine.json');
async function cli(...args) { return command([...args, '--state-dir', state(), '--json']); }
async function instances() { return JSON.parse((await cli('list')).stdout).instances; }
async function inspect(id) { return JSON.parse((await cli('inspect', id)).stdout).instance; }
async function run(args, cwd = project, options = {}) {
  return command(['run', 'browser', '--project', cwd, '--config', config(), '--state-dir', state(), '--json', '--', ...args], { ...options, ok: false, cwd });
}
function clean(instance) {
  assert.equal(instance.execution?.cleanup_failure ?? '', '');
  assert.equal(instance.execution?.collection_failure ?? '', '');
  assert.equal(instance.execution?.failure ?? '', '');
}
async function requireRun(args, expected) {
  const result = await run(args);
  checkpoint();
  const instance = JSON.parse(result.stdout).instance;
  assert.ok(instance, result.stdout);
  clean(instance);
  assert.equal(result.code, expected);
  assert.equal(instance.execution.terminal_exit_code, expected);
  const artifact = instance.execution.artifacts.find(a => a.name === 'reports');
  const invocation = await verifyReports(instance, artifact);
  assert.deepEqual(invocation.args, args);
  console.log(JSON.stringify({ checkpoint: expected ? 'expected test failure' : 'trusted browser success',
    id: instance.id, origin: instance.execution.origin, terminal_exit_code: instance.execution.terminal_exit_code }));
  return instance;
}

try {
  {
    root = (await readFile(pointer, 'utf8')).trim();
    assert.match(root, /^\/tmp\/bl-ci-[a-zA-Z0-9]+$/);
    assert.equal(root, JSON.parse(process.env.BACKLOT_SETUP_OWNER).root);
    if (mode === 'run') {
      const args = process.argv.slice(3);
      const first = await requireRun(args, 0);
      await requireRun(['--config=failure.config.mjs', 'failure.spec.mjs'], 1);
      const recovered = await requireRun(args, 0);
      assert.notEqual(first.id, recovered.id);
      assert.notEqual(first.execution.origin, recovered.execution.origin);
    } else if (mode === 'acceptance') {
      const { acceptance } = await import('./acceptance.mjs');
      await acceptance({ root, project, state: state(), cli, inspect, instances, run, signal: cancellation.signal });
    } else if (mode === 'collect') {
      const staging = join(root, 'export-staging');
      await mkdir(staging);
      for (const item of await instances()) {
        const instance = await inspect(item.id);
        const destination = join(staging, instance.id);
        await mkdir(destination, { recursive: true });
        const e = instance.execution;
        // Deliberate allowlist excludes API capabilities and internal snapshots.
        await writeFile(join(destination, 'result.json'), JSON.stringify({ id: instance.id, status: instance.status,
          checkout: instance.checkout.id, origin: e?.origin, ports: e?.ports, terminal_exit_code: e?.terminal_exit_code,
          cancelled: e?.cancelled ?? false, failure: e?.failure, collection_failure: e?.collection_failure,
          cleanup_failure: e?.cleanup_failure }, null, 2));
        const logs = await cli('logs', instance.id);
        await writeFile(join(destination, 'logs.ndjson'), logs.stdout);
        for (const artifact of e?.artifacts ?? []) {
          assert.equal(artifact.name, 'reports');
          const rel = relative(join(state(), 'artifacts'), artifact.path);
          assert.ok(rel && !rel.startsWith('..') && !isAbsolute(rel));
          await verifyReports(instance, artifact);
          await safeCopy(artifact.path, join(destination, 'artifacts'));
        }
      }
      try { await safeCopy(join(root, 'acceptance.json'), join(staging, 'acceptance.json')); }
      catch (error) { if (error.code !== 'ENOENT') throw error; }
      try { await safeCopy(join(root, 'outer-cancellation.json'), join(staging, 'outer-cancellation.json')); }
      catch (error) { if (error.code !== 'ENOENT') throw error; }
      // Check exported text/logs too. Never copy daemon/gateway state or logs wholesale.
      await safeCopy(staging, join(root, 'export-audit'));
      checkpoint();
      execFileSync('python3', [fileURLToPath(new URL('../../actions/setup/audit.py', import.meta.url)), join(root, 'export-audit')], { stdio: 'inherit' });
      await rename(join(root, 'export-audit'), output);
      await rm(staging, { recursive: true });
      console.log('Collected allowlisted results, application logs and Playwright reports before runner teardown');
    } else if (mode === 'cleanup') {
      await cleanup(JSON.parse(process.env.BACKLOT_SETUP_OWNER), async () => {
        // Only this runner-created, recorded worktree may be removed.
        try {
          const peer = await readFile(join(root, 'peer-worktree'), 'utf8');
          assert.equal(peer, join(root, 'peer'));
          execFileSync('git', ['worktree', 'remove', '--force', peer], { cwd: project, timeout: 20_000 });
        } catch (error) { if (error.code !== 'ENOENT') throw error; }
      });
      await rm(pointer);
      console.log('Verified scoped destruction and stopped job-local gateway/daemon');
    } else throw new Error('usage: ci.mjs run [Playwright arguments]|acceptance|collect|cleanup');
  }
} catch (error) {
  console.error(error.stack);
  console.error('Owned private state preserved for authenticated cleanup: ' + (root ?? pointer));
  process.exitCode = interrupted || 1;
} finally {
  // Registered before bootstrap/acceptance work: every retained CLI is cancelled
  // and joined before this runner exits, including clients hidden by polling.
  for (const child of children) child.kill(interrupted === 143 ? 'SIGTERM' : 'SIGINT');
  await Promise.allSettled([...pending]);
  if (interrupted) process.exitCode = interrupted;
}
