// Shared job preparation, paginated discovery and authenticated cleanup.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtemp, mkdir, writeFile, readFile, lstat, rm } from 'node:fs/promises';
import { createServer } from 'node:net';
import { join, resolve } from 'node:path';
import { randomBytes } from 'node:crypto';

export async function command(binary, args, options = {}) {
  const child = spawn(binary, args, { stdio: ['ignore', 'pipe', 'pipe'], ...options });
  let stdout = '', stderr = '';
  child.stdout.on('data', chunk => { stdout += chunk; });
  child.stderr.on('data', chunk => { stderr += chunk; });
  const timer = setTimeout(() => child.kill('SIGTERM'), options.timeout ?? 60_000);
  // Retained child handle only; never act on discovered PIDs or process names.
  const hardTimer = setTimeout(() => child.kill('SIGKILL'), (options.timeout ?? 60_000) + 5_000);
  try {
    const code = await new Promise((resolve, reject) => {
      child.on('error', reject);
      child.on('close', (code, signal) => resolve(code ?? signal));
    });
    assert.equal(code, 0, `${binary} ${args[0]} failed (${code}): ${stderr || stdout}`);
    return stdout;
  } finally { clearTimeout(timer); clearTimeout(hardTimer); }
}
export async function port() {
  const server = createServer();
  await new Promise((resolve, reject) => { server.on('error', reject); server.listen(0, '127.0.0.1', resolve); });
  const value = server.address().port;
  await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
  return value; // No reservation: conflicts fail without touching the winner.
}
export async function createOwner() {
  const root = await mkdtemp('/tmp/bl-ci-');
  await import('node:fs/promises').then(fs => fs.chmod(root, 0o700));
  const token = randomBytes(32).toString('hex');
  await writeFile(join(root, 'owner'), token, { flag: 'wx', mode: 0o600 });
  return { root, token };
}
export async function verifyOwner(owner) {
  assert.match(owner.root, /^\/tmp\/bl-ci-[a-zA-Z0-9]+$/);
  const stat = await lstat(owner.root);
  assert.ok(stat.isDirectory() && !stat.isSymbolicLink() && (stat.mode & 0o777) === 0o700);
  assert.equal(await readFile(join(owner.root, 'owner'), 'utf8'), owner.token, 'job ownership mismatch; preserve state');
}
export async function prepare({ root, binary, caddy, node }) {
  await mkdir(join(root, 'empty-roots'), { mode: 0o700 });
  const httpsPort = await port();
  let adminPort = await port();
  while (adminPort === httpsPort) adminPort = await port();
  const state = join(root, 'd');
  const config = join(root, 'machine.json');
  await writeFile(config, JSON.stringify({ version: 'backlot/v1', tools: { node: resolve(node) },
    caddy: { mode: 'managed', executable: resolve(caddy), endpoint: `http://127.0.0.1:${adminPort}`, https_port: httpsPort }
  }), { mode: 0o600 });
  await command(binary, ['daemon', 'start', '--state-dir', state, '--lease-duration', '3s', '--json'], {
    env: { ...process.env, SSL_CERT_FILE: join(state, 'gateway/data/pki/authorities/local/root.crt'),
      SSL_CERT_DIR: join(root, 'empty-roots') }
  });
  await command(binary, ['gateway', 'start', '--config', config, '--state-dir', state, '--json']);
  return { state, config };
}
// Discover every page before callers export or destroy anything. The CLI returns
// sorted recorded IDs, with the last ID as cursor only when another page exists.
export async function listInstances(listPage, deadline) {
  const instances = [];
  let after = '';
  for (;;) {
    const remaining = deadline - Date.now();
    assert.ok(remaining > 0, 'instance discovery deadline exceeded; preserve state');
    const page = JSON.parse(await listPage(after, Math.min(20_000, remaining)));
    assert.ok(Date.now() < deadline, 'instance discovery deadline exceeded; preserve state');
    assert.ok(page?.api_version === 'backlot/v1' && Array.isArray(page.instances) &&
      page.instances.length <= 100, 'malformed instance discovery page; preserve state');
    let previous = after;
    for (const item of page.instances) {
      assert.ok(typeof item?.id === 'string' && /^[0-9a-f]{64}$/.test(item.id) && item.id > previous,
        'malformed or stuck instance discovery IDs; preserve state');
      previous = item.id;
    }
    const next = page.next_after_id === undefined ? '' : page.next_after_id;
    assert.ok(typeof next === 'string' && (next === '' ||
      (page.instances.length === 100 && next === previous && next > after)),
    'malformed or stuck instance discovery cursor; preserve state');
    instances.push(...page.instances);
    if (!next) return instances;
    after = next;
  }
}
export async function cleanup(owner, beforeRemove = async () => {}) {
  try { await lstat(owner.root); } catch (error) { if (error.code === 'ENOENT') return; throw error; }
  await verifyOwner(owner);
  const state = join(owner.root, 'd');
  // No daemon was attempted: private downloads alone can safely be removed.
  try { await lstat(join(owner.root, 'daemon-attempted')); }
  catch (error) { if (error.code !== 'ENOENT') throw error; await rm(owner.root, { recursive: true }); return; }
  const binary = join(owner.root, 'tools/backlot');
  const deadline = Date.now() + 90_000;
  const cli = (...args) => {
    const remaining = deadline - Date.now();
    assert.ok(remaining > 0, 'cleanup exceeded 90-second budget; preserve state');
    return command(binary, [...args, '--state-dir', state, '--json'], { timeout: Math.min(20_000, remaining) });
  };
  const instances = await listInstances((after, timeout) => command(binary,
    ['list', ...(after ? ['--after', after] : []), '--state-dir', state, '--json'], { timeout }), deadline);
  for (const item of instances) await cli('destroy', item.id);
  await cli('gateway', 'stop');
  await cli('daemon', 'stop');
  for (let attempt = 0; ; attempt++) {
    try { await lstat(join(state, 'daemon.sock')); }
    catch (error) { if (error.code === 'ENOENT') break; throw error; }
    assert.ok(attempt < 100, 'daemon shutdown not confirmed; preserve state');
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  // A deleted socket alone is not permission to stop anything else; the CLI
  // already authenticated the daemon/guardian shutdown above.
  await beforeRemove();
  await rm(owner.root, { recursive: true });
  console.log('Verified scoped cleanup of job daemon, gateway and private state');
}
