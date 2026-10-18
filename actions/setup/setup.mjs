import assert from 'node:assert/strict';
import { appendFile, mkdir, readFile, writeFile } from 'node:fs/promises';
import { join, isAbsolute, delimiter } from 'node:path';
import { fileURLToPath } from 'node:url';
import { command, createOwner, prepare, cleanup } from './runtime.mjs';

export async function setup() {
 const started = performance.now();
 let owner;
 try {
  assert.equal(process.platform, 'linux', 'supported runner: Ubuntu 24.04 amd64');
  assert.equal(process.arch, 'x64');
  assert.match(await readFile('/etc/os-release', 'utf8'), /^VERSION_ID="24\.04"$/m);
  const sha = process.env['INPUT_BACKLOT-REF'];
  let node = process.env['INPUT_NODE-BINARY'];
  if (!node) {
    const { access } = await import('node:fs/promises');
    for (const directory of (process.env.PATH ?? '').split(delimiter)) {
      if (!isAbsolute(directory)) continue;
      const candidate = join(directory, 'node');
      try { await access(candidate, 1); node = candidate; break; } catch {}
    }
  }
  assert.match(sha ?? '', /^[a-f0-9]{40}$/, 'backlot-ref must be a full lowercase commit SHA');
  assert.ok(node && isAbsolute(node), 'node-binary must be an absolute application Node executable');
  await command(node, ['--version']);
  const env = { ...process.env, GOTOOLCHAIN: 'local', GOPROXY: 'https://proxy.golang.org',
    GOSUMDB: 'sum.golang.org', GOINSECURE: '', GOWORK: 'off', GOENV: 'off', GOPRIVATE: '', GONOSUMDB: '', GONOPROXY: '', GOFLAGS: '-mod=readonly' };
  assert.match(await command('go', ['version'], { env }), /^go version go1\.27\.1 linux\/amd64\s*$/,
    'install exact Go 1.27.1 using pinned actions/setup-go before Backlot setup');
  try { await command('lsof', ['-v']); } catch {
    await command('sudo', ['apt-get', 'update'], { timeout: 180_000 });
    await command('sudo', ['apt-get', 'install', '-y', 'lsof'], { timeout: 180_000 });
    await command('lsof', ['-v']);
  }
  owner = await createOwner();
  console.log(`::add-mask::${owner.token}`);
  // Register post-job ownership before downloads or any daemon/gateway allocation.
  await appendFile(process.env.GITHUB_STATE, `owner=${JSON.stringify(owner)}\n`);
  const { restoreCache, saveCache } = await import('./sdk.mjs');
  const paths = [(await command('go', ['env', 'GOMODCACHE'], { env })).trim(), (await command('go', ['env', 'GOCACHE'], { env })).trim()];
  const cacheKey = `backlot-source-linux-amd64-go1.27.1-${sha}-caddy2.11.6`;
  let cacheHit;
  try { cacheHit = await restoreCache(paths, cacheKey); } catch (error) { console.warn(`Go cache restore unavailable: ${error.message}`); }
  const source = join(owner.root, 'source');
  const tools = join(owner.root, 'tools');
  await mkdir(source); await mkdir(tools);
  const git = (...args) => command('git', args, { cwd: source });
  await git('init', '--quiet');
  await git('remote', 'add', 'origin', 'https://github.com/octacian/backlot.git');
  await git('-c', 'protocol.file.allow=never', 'fetch', '--quiet', '--depth=1', 'origin', sha);
  await git('checkout', '--quiet', '--detach', 'FETCH_HEAD');
  assert.equal((await git('rev-parse', 'HEAD')).trim(), sha, 'source commit identity mismatch');
  assert.equal((await command('go', ['list', '-m'], { cwd: source, env })).trim(), 'github.com/octacian/backlot');
  await command('go', ['mod', 'download'], { cwd: source, env, timeout: 300_000 });
  await command('go', ['mod', 'verify'], { cwd: source, env });
  await command('go', ['build', '-trimpath', '-buildvcs=false', '-ldflags=-buildid=', '-o', join(tools, 'backlot'), './cmd/backlot'],
    { cwd: source, env: { ...env, CGO_ENABLED: '0' }, timeout: 300_000 });
  await command('go', ['install', 'github.com/caddyserver/caddy/v2/cmd/caddy@v2.11.6'],
    { env: { ...env, GOBIN: tools }, timeout: 300_000 });
  const identity = await command('go', ['version', '-m', join(tools, 'caddy')], { env });
  assert.match(identity, /\tpath\tgithub\.com\/caddyserver\/caddy\/v2\/cmd\/caddy\n/);
  assert.match(identity, /\tmod\tgithub\.com\/caddyserver\/caddy\/v2\tv2\.11\.6\th1:/);
  assert.ok(!identity.includes('\t=>'), 'replaced Caddy modules are not supported');
  if (!cacheHit) {
    try { await saveCache(paths, cacheKey); } catch (error) { console.warn(`Go cache save unavailable: ${error.message}`); }
  }
  const binary = join(tools, 'backlot');
  await writeFile(join(owner.root, 'daemon-attempted'), '', { mode: 0o600 });
  const { state, config } = await prepare({ root: owner.root, binary, caddy: join(tools, 'caddy'), node });
  const helper = fileURLToPath(new URL('job.mjs', import.meta.url));
  const values = { BACKLOT_BINARY: binary, BACKLOT_STATE_DIR: state, BACKLOT_CONFIG: config,
    BACKLOT_SETUP_OWNER: JSON.stringify(owner), BACKLOT_SETUP_HELPER: helper };
  for (const [name, value] of Object.entries(values)) {
    process.env[name] = value;
    await appendFile(process.env.GITHUB_ENV, `${name}=${value}\n`);
  }
  await appendFile(process.env.GITHUB_PATH, `${tools}\n`);
  const seconds = ((performance.now() - started) / 1000).toFixed(3);
  for (const [name, value] of Object.entries({ binary, config, 'state-dir': state, helper, 'setup-seconds': seconds })) {
    await appendFile(process.env.GITHUB_OUTPUT, `${name}=${value}\n`);
  }
  console.log(`Backlot ${sha}; stock Caddy v2.11.6; setup_seconds=${seconds}`);
  return owner;
} catch (error) {
  console.error(error.stack);
  if (owner) {
    try { await cleanup(owner); } catch (cleanupError) { console.error(`Setup cleanup uncertain; retained ${owner.root}: ${cleanupError.message}`); }
  }
  throw error;
 }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
 try { await setup(); } catch { process.exitCode = 1; }
}
