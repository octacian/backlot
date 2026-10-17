import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { readFile, writeFile, lstat } from 'node:fs/promises';
import { createServer, connect } from 'node:net';
import https from 'node:https';
import { join } from 'node:path';
import { setTimeout } from 'node:timers/promises';

const exec = promisify(execFile);
async function absent(path) {
  try { await lstat(path); return false; }
  catch (error) { if (error.code === 'ENOENT') return true; throw error; }
}

// The acceptance driver uses public CLI results and retained client handles.
// It never edits journals, adopts resources, overrides DNS or signals by PID.
export async function acceptance({ root, project, state, cli, inspect, instances, run, signal }) {
  const delay = ms => setTimeout(ms, undefined, { signal });
  async function until(check, description) {
    const deadline = Date.now() + 30_000;
    do {
      signal.throwIfAborted();
      const value = await check();
      signal.throwIfAborted();
      if (value) return value;
      await delay(100);
    } while (Date.now() < deadline);
    throw new Error('timeout: ' + description);
  }
  const clients = new Set();
  const pending = new Set();
  const observations = [];
  const sentinel = createServer(socket => socket.end());
  const ca = await readFile(join(state, 'gateway/data/pki/authorities/local/root.crt'));
  function get(origin, path = '/api/stack') {
    return new Promise((resolve, reject) => {
      const req = https.get(new URL(path, origin), { ca, signal }, res => {
        let data = '';
        res.on('data', chunk => { data += chunk; });
        res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body: data }));
        res.on('error', reject);
      });
      req.setTimeout(5000, () => req.destroy(new Error('canonical request timeout')));
      req.on('error', reject);
    });
  }
  function begin(args, cwd = project) {
    let child;
    const done = run(args, cwd, { onSpawn: handle => {
      child = handle; clients.add(handle); handle.once('close', () => clients.delete(handle));
    } });
    pending.add(done);
    done.then(() => pending.delete(done), () => pending.delete(done));
    return { child, done };
  }
  async function holding(cwd = project) {
    const before = new Set((await instances()).map(i => i.id));
    const client = begin(['--config=failure.config.mjs', 'hold.spec.mjs'], cwd);
    const instance = await until(async () => {
      const candidate = (await instances()).find(i => !before.has(i.id));
      if (!candidate) return;
      const value = await inspect(candidate.id);
      try {
        const report = JSON.parse(await readFile(join(cwd, 'reports/holding.json')));
        if (report.origin === value.execution?.origin) return value;
      } catch (error) { if (error.code !== 'ENOENT') throw error; }
    }, 'real browser ready');
    const data = instance.execution.fixtures.find(f => f.name === 'data').value;
    const stack = JSON.parse((await get(instance.execution.origin)).body).stack;
    return { ...client, instance, data, stack };
  }
  function clean(instance) {
    assert.equal(instance.execution.cleanup_failure ?? '', '');
    assert.equal(instance.execution.collection_failure ?? '', '');
    assert.equal(instance.execution.failure ?? '', '');
  }
  async function invocation(instance, args) {
    const artifact = instance.execution.artifacts.find(a => a.name === 'reports');
    assert.ok(artifact, 'reports not collected');
    assert.deepEqual(JSON.parse(await readFile(join(artifact.path, 'reports/invocation.json'))).args, args);
  }
  async function routeGone(origin) {
    // Stock Caddy's empty scope can return an empty HTTP 200. Verify the
    // instance marker and API payload are gone rather than assuming a 404.
    const response = await get(origin);
    assert.equal(response.headers['backlot-instance'], undefined, 'disposed route marker remains');
    assert.equal(response.body, '', 'disposed application still reachable');
  }
  async function disposed(held) {
    const final = await until(async () => {
      const value = await inspect(held.instance.id);
      return value.status === 'cancelled' && value.execution.artifacts?.length ? value : undefined;
    }, 'lease/cancellation collection and cleanup');
    clean(final);
    assert.equal(final.execution.cancelled, true);
    await invocation(final, ['--config=failure.config.mjs', 'hold.spec.mjs']);
    assert.ok(await absent(held.data), 'disposed data still exists');
    await routeGone(final.execution.origin);
    return final;
  }
  try {
    signal.throwIfAborted();
    await new Promise((resolve, reject) => { sentinel.on('error', reject); sentinel.listen(0, '127.0.0.1', resolve); });
    const sentinelPort = sentinel.address().port;
    async function sentinelAlive() {
      await new Promise((resolve, reject) => {
        const socket = connect(sentinelPort, '127.0.0.1');
        socket.once('error', reject);
        socket.once('connect', () => { socket.destroy(); resolve(); });
      });
    }
    // Same checkout: the second run must not build or clear reports while the
    // first owns the application output group, including evidence collection.
    const first = await holding();
    const before = new Set((await instances()).map(i => i.id));
    const queued = begin([]); // Both specs run with two workers in this one scene.
    const queuedID = await until(async () => (await instances()).find(i => !before.has(i.id))?.id, 'queued scene identity');
    console.log(JSON.stringify({ checkpoint: 'same-checkout clients waiting', ids: [first.instance.id, queuedID] }));
    await delay(300);
    const logs = (await cli('logs', queuedID)).stdout.trim().split('\n').filter(Boolean).map(line => JSON.parse(line));
    assert.ok(!logs.some(r => r.message?.includes('application built')), 'build ran while checkout output was owned');
    assert.equal(JSON.parse(await readFile(join(project, 'reports/holding.json'))).origin, first.instance.execution.origin);
    assert.equal(JSON.parse((await get(first.instance.execution.origin)).body).stack, first.stack);
    first.child.kill('SIGINT');
    const cancelled = await first.done;
    assert.equal(cancelled.code, 130);
    const final = JSON.parse(cancelled.stdout).instance;
    clean(final);
    assert.equal(final.status, 'cancelled');
    assert.equal(final.execution.cancelled, true);
    await invocation(final, ['--config=failure.config.mjs', 'hold.spec.mjs']);
    assert.ok(await absent(first.data));
    await routeGone(first.instance.execution.origin);
    const next = await queued.done;
    assert.equal(next.code, 0, next.stderr || next.stdout);
    const nextInstance = JSON.parse(next.stdout).instance;
    clean(nextInstance);
    assert.equal(nextInstance.execution.terminal_exit_code, 0);
    const report = nextInstance.execution.artifacts.find(a => a.name === 'reports');
    const stats = JSON.parse(await readFile(join(report.path, 'reports/playwright.json'))).stats;
    assert.equal(stats.expected, 2);
    assert.equal(stats.unexpected, 0);
    assert.equal(stats.skipped, 0);
    assert.notEqual(nextInstance.execution.origin, first.instance.execution.origin);
    await sentinelAlive();
    observations.push({ check: 'same-checkout output serialization and SIGINT', cli_exit: cancelled.code,
      terminal_exit: final.execution.terminal_exit_code, cancelled: true });

    // Concurrent real worktrees: independent build/report paths and a peer
    // must survive loss of the other run's CLI and the daemon-enforced lease.
    signal.throwIfAborted();
    const peer = join(root, 'peer');
    await writeFile(join(root, 'peer-worktree'), peer, { flag: 'wx', mode: 0o600 });
    await exec('git', ['worktree', 'add', '--quiet', '--detach', peer, 'HEAD'], { cwd: project, timeout: 30_000, signal });
    const peerProject = join(peer, 'examples/ci-scenes');
    await exec('npm', ['ci', '--ignore-scripts', '--no-audit', '--no-fund'], { cwd: peerProject, timeout: 120_000, signal });
    const lost = await holding();
    const survivor = await holding(peerProject);
    assert.notEqual(lost.instance.checkout.id, survivor.instance.checkout.id);
    assert.notEqual(lost.instance.execution.origin, survivor.instance.execution.origin);
    assert.notEqual(lost.data, survivor.data);
    assert.notEqual(lost.stack, survivor.stack);
    assert.notEqual(lost.instance.execution.ports.port, survivor.instance.execution.ports.port);
    for (const held of [lost, survivor]) {
      assert.equal((await get(held.instance.execution.origin, '/api/notes/isolation')).status, 404);
    }
    // Store a mutation via the real canonical API and verify the peer remains fresh.
    await new Promise((resolve, reject) => {
      const req = https.request(new URL('/api/notes/isolation', lost.instance.execution.origin), { ca, signal, method: 'PUT' }, res => {
        res.resume(); res.on('end', () => res.statusCode === 200 ? resolve() : reject(new Error('mutation failed')));
      });
      req.on('error', reject); req.end('private-to-first-worktree');
    });
    assert.equal((await get(survivor.instance.execution.origin, '/api/notes/isolation')).status, 404);
    lost.child.kill('SIGKILL'); // Retained exact client handle; daemon survives.
    const deadClient = await lost.done;
    assert.equal(deadClient.signal, 'SIGKILL');
    const lostFinal = await disposed(lost);
    assert.equal(JSON.parse((await get(survivor.instance.execution.origin)).body).stack, survivor.stack);
    assert.ok(!(await absent(survivor.data)), 'peer data removed');
    await sentinelAlive();
    survivor.child.kill('SIGINT');
    const survivorResult = await survivor.done;
    assert.equal(survivorResult.code, 130);
    const survivorFinal = JSON.parse(survivorResult.stdout).instance;
    clean(survivorFinal);
    assert.equal(survivorFinal.execution.cancelled, true);
    await invocation(survivorFinal, ['--config=failure.config.mjs', 'hold.spec.mjs']);
    assert.ok(await absent(survivor.data));
    await routeGone(survivor.instance.execution.origin);
    await sentinelAlive();
    observations.push({ check: 'concurrent worktrees, distinct data/origins/ports, client-loss lease and scoped cleanup',
      client_signal: deadClient.signal, terminal_exit: lostFinal.execution.terminal_exit_code,
      cancelled: lostFinal.execution.cancelled, peer_preserved: true, unrelated_listener_preserved: true });
    await writeFile(join(root, 'acceptance.json'), JSON.stringify(observations, null, 2));
    console.log(JSON.stringify(observations));
  } finally {
    // Registered before faults. Bounded CLI cancellation/lease remains the
    // daemon's responsibility; workflow cleanup then verifies every owned ID.
    // The outer owner already delivered the requested signal when aborted.
    if (!signal.aborted) for (const child of clients) child.kill('SIGINT');
    await Promise.allSettled([...pending]);
    await new Promise(resolve => sentinel.close(resolve));
  }
}
