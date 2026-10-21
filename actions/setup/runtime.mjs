// Shared job preparation, paginated discovery and authenticated cleanup.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtemp, mkdir, writeFile, readFile, lstat, rm } from 'node:fs/promises';
import { createServer } from 'node:net';
import { join, resolve } from 'node:path';
import { randomBytes } from 'node:crypto';

// Raw structured input payloads never reach any child, even non-Backlot tools.
export function cleanEnvironment(env = process.env) {
  const result = { ...env };
  delete result['INPUT_PROJECT-INPUTS'];
  delete result['INPUT_PROJECT-GENERATE-INPUTS'];
  return result;
}
export async function backlotEnvironment(owner, env = process.env) {
  await verifyOwner(owner);
  return { ...cleanEnvironment(env), BACKLOT_PROJECT_CONFIG_DIR: join(owner.root, 'project-config') };
}
export async function command(binary, args, options = {}) {
  const { rejectStderr = false, stdin, sensitive = false, maxBytes = 8 << 20, timeout = 60_000, ...spawnOptions } = options;
  const fail = () => Error('Sensitive project setup command failed; runtime was not started');
  if (stdin !== undefined && (typeof stdin !== 'string' || Buffer.byteLength(stdin) > (1 << 20))) throw fail();
  const child = spawn(binary, args, { ...spawnOptions, env: cleanEnvironment(spawnOptions.env), stdio: [stdin === undefined ? 'ignore' : 'pipe', 'pipe', 'pipe'] });
  let stdout = '', stderr = '', bytes = 0, failure = '';
  const stop = reason => { failure ||= reason; child.kill('SIGTERM'); };
  const capture = stream => chunk => {
    bytes += Buffer.byteLength(chunk);
    if (bytes > maxBytes) { stop('output limit exceeded'); return; }
    if (stream === 'stdout') stdout += chunk; else stderr += chunk;
  };
  child.stdout.setEncoding('utf8'); child.stderr.setEncoding('utf8');
  child.stdout.on('data', capture('stdout'));
  child.stderr.on('data', capture('stderr'));
  if (child.stdin) { child.stdin.on('error', () => stop('stdin failed')); child.stdin.end(stdin); }
  const onInt = () => { failure ||= 'interrupted'; child.kill('SIGINT'); };
  const onTerm = () => { failure ||= 'interrupted'; child.kill('SIGTERM'); };
  process.on('SIGINT', onInt); process.on('SIGTERM', onTerm);
  const timer = setTimeout(() => stop('timeout'), timeout);
  // Retained child handle only; always join, including timeout and capture errors.
  const hardTimer = setTimeout(() => child.kill('SIGKILL'), timeout + 5_000);
  try {
    const code = await new Promise((resolve, reject) => {
      child.on('error', error => reject(sensitive ? fail() : error));
      child.on('close', (code, signal) => resolve(code ?? signal));
    });
    if (failure || code !== 0 || (rejectStderr && stderr.trim())) {
      if (sensitive) throw fail();
      const error = Error(`${binary} ${args[0]} failed (${failure || code}): ${stderr || stdout}`);
      error.stdout = stdout;
      throw error;
    }
    return stdout;
  } finally {
    clearTimeout(timer); clearTimeout(hardTimer);
    process.off('SIGINT', onInt); process.off('SIGTERM', onTerm);
  }
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
export async function prepare({ root, binary, caddy, node, owner, beforeStart = async () => {} }) {
  await mkdir(join(root, 'empty-roots'), { mode: 0o700 });
  const httpsPort = await port();
  let adminPort = await port();
  while (adminPort === httpsPort) adminPort = await port();
  const state = join(root, 'd');
  const config = join(root, 'machine.json');
  await writeFile(config, JSON.stringify({ version: 'backlot/v1', tools: { node: resolve(node) },
    caddy: { mode: 'managed', executable: resolve(caddy), endpoint: `http://127.0.0.1:${adminPort}`, https_port: httpsPort }
  }), { mode: 0o600 });
  const env = await backlotEnvironment(owner);
  await mkdir(join(root, 'project-config'), { mode: 0o700 });
  await beforeStart(config);
  await writeFile(join(root, 'daemon-attempted'), '', { mode: 0o600 });
  await command(binary, ['daemon', 'start', '--state-dir', state, '--lease-duration', '3s', '--json'], {
    env: { ...env, SSL_CERT_FILE: join(state, 'gateway/data/pki/authorities/local/root.crt'),
      SSL_CERT_DIR: join(root, 'empty-roots') }
  });
  await command(binary, ['gateway', 'start', '--config', config, '--state-dir', state, '--json'], { env });
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
    assert.ok(page?.api_version === 'v1' && Array.isArray(page.instances) &&
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
  const env = await backlotEnvironment(owner);
  // No daemon was attempted: private downloads alone can safely be removed.
  try { await lstat(join(owner.root, 'daemon-attempted')); }
  catch (error) { if (error.code !== 'ENOENT') throw error; await rm(owner.root, { recursive: true }); return; }
  const binary = join(owner.root, 'tools/backlot');
  const deadline = Date.now() + 90_000;
  const cli = (...args) => {
    const remaining = deadline - Date.now();
    assert.ok(remaining > 0, 'cleanup exceeded 90-second budget; preserve state');
    return command(binary, [...args, '--state-dir', state, '--json'], { env, timeout: Math.min(20_000, remaining) });
  };
  const instances = await listInstances((after, timeout) => command(binary,
    ['list', ...(after ? ['--after', after] : []), '--state-dir', state, '--json'], { env, timeout }), deadline);
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
