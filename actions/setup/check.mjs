// Action-only gaps; browser/lifecycle/fault assertions stay in the existing suite.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdir, writeFile, lstat, symlink, readFile, readdir, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { command, createOwner, verifyOwner, prepare, cleanup, listInstances } from './runtime.mjs';
import { safeCopy, settleForExport } from './evidence.mjs';

const script = name => fileURLToPath(new URL(name, import.meta.url));
test('ownership fencing and pre-daemon post cleanup are idempotent', async () => {
  const owner = await createOwner();
  try {
    await assert.rejects(cleanup({ ...owner, token: 'wrong' }), /ownership mismatch/);
    await verifyOwner(owner);
    await command(process.execPath, [script('post.mjs')], { env: { ...process.env, STATE_owner: JSON.stringify(owner) } });
    await assert.rejects(lstat(owner.root), { code: 'ENOENT' });
    await cleanup(owner);
  } finally { await cleanup(owner); }
});
test('shared exporter rejects links and plaintext keys; archive contracts are Go tests', async () => {
  const owner = await createOwner();
  try {
    const source = join(owner.root, 'input');
    await mkdir(source);
    await writeFile(join(source, 'root.key'), 'private');
    await assert.rejects(safeCopy(source, join(owner.root, 'export')), /private key filename/);
    await symlink('/etc/passwd', join(owner.root, 'link'));
    await assert.rejects(safeCopy(join(owner.root, 'link'), join(owner.root, 'out')), /evidence link/);
    await writeFile(join(owner.root, 'private.txt'), '-----BEGIN PRIVATE KEY-----');
    await assert.rejects(safeCopy(join(owner.root, 'private.txt'), join(owner.root, 'out')), /private key in evidence/);
  } finally { await cleanup(owner); }
});
test('real setup failure after daemon start is cleaned by native post hook', {
  skip: process.env.BACKLOT_BINARY ? false : 'requires action-installed binary on Ubuntu; no daemon allocated'
}, async () => {
  const owner = await createOwner();
  const emptyOutput = owner.root + '-empty-evidence';
  const preparedOutput = owner.root + '-prepared-evidence';
  try {
    await mkdir(join(owner.root, 'tools'));
    await symlink(process.env.BACKLOT_BINARY, join(owner.root, 'tools/backlot'));
    await writeFile(join(owner.root, 'daemon-attempted'), '');
    await assert.rejects(prepare({ root: owner.root, binary: process.env.BACKLOT_BINARY,
      caddy: join(owner.root, 'missing-caddy'), node: process.execPath }), /gateway.*failed/s);
    const cli = (...args) => command(process.env.BACKLOT_BINARY,
      [...args, '--state-dir', join(owner.root, 'd'), '--json']);
    // Bind the protocol fixture to actual CLI output, including an empty daemon.
    const empty = JSON.parse(await cli('list'));
    assert.equal(empty.api_version, 'v1');
    assert.deepEqual(empty.instances, []);
    const collect = output => command(process.execPath, [script('job.mjs'), 'collect', output], {
      env: { ...process.env, BACKLOT_SETUP_OWNER: JSON.stringify(owner) }
    });
    await collect(emptyOutput);
    assert.deepEqual(await readdir(emptyOutput), []);
    // Reuse the existing persistent metadata scene: no application is launched.
    const { instance } = JSON.parse(await cli('prepare', 'dev', '--project', script('../../examples/adoption')));
    await collect(preparedOutput);
    assert.equal(JSON.parse(await readFile(join(preparedOutput, instance.id, 'result.json'))).status, 'prepared');
    await command(process.execPath, [script('post.mjs')], { env: { ...process.env, STATE_owner: JSON.stringify(owner) }, timeout: 100_000 });
    await assert.rejects(lstat(owner.root), { code: 'ENOENT' });
    assert.deepEqual(await readdir(emptyOutput), []);
    assert.equal((await readdir(preparedOutput)).length, 1);
  } finally {
    await cleanup(owner);
    await rm(emptyOutput, { recursive: true, force: true });
    await rm(preparedOutput, { recursive: true, force: true });
    await assert.rejects(lstat(emptyOutput), { code: 'ENOENT' });
    await assert.rejects(lstat(preparedOutput), { code: 'ENOENT' });
  }
});

test('export waits for disposable final evidence, bounds timeout and leaves persistent services live', async () => {
  const active = { id: 'run', status: 'starting', plan: { lifetime: 'disposable' }, execution: {} };
  let calls = 0;
  const final = { ...active, status: 'failed', execution: { completed_at: 'done', cancelled: true, terminal_exit_code: -1, artifacts: ['reports'] } };
  const result = await settleForExport(active, async () => ++calls === 1 ? active : final, Date.now() + 1000);
  assert.deepEqual(result, final);
  assert.equal(calls, 2);
  await assert.rejects(settleForExport(active, async () => active, Date.now()), /did not settle/);
  const persistent = { ...active, plan: { lifetime: 'persistent' } };
  assert.equal(await settleForExport(persistent, () => { throw Error('must not poll persistent service'); }, Date.now()), persistent);
});

// Protocol-only fixture: actual collect/cleanup helpers, no daemon or gateway.
test('paginated discovery collects and cleans every owned record before removing state', async () => {
  const owner = await createOwner();
  const output = owner.root + '-evidence';
  // Register cleanup before fixture setup, retaining authenticated state on failure.
  try {
    await mkdir(join(owner.root, 'tools'));
    await writeFile(join(owner.root, 'tools/backlot'), `#!${process.execPath}
import assert from 'node:assert/strict';
import { readFileSync, appendFileSync, existsSync } from 'node:fs';
const root = ${JSON.stringify(owner.root)}, output = ${JSON.stringify(output)};
const args = process.argv.slice(2);
const ids = Array.from({length: 101}, (_, i) => (i + 1).toString(16).padStart(64, '0'));
const after = args.includes('--after') ? args[args.indexOf('--after') + 1] : '';
if (args[0] === 'list') {
  const page = ids.filter(id => id > after).slice(0, 100);
  const bad = existsSync(root + '/malformed');
  console.log(JSON.stringify({api_version: 'v1', instances: page.map(id => ({id})),
    next_after_id: bad && after ? 'stuck' : page.at(-1) !== ids.at(-1) ? page.at(-1) : undefined}));
} else if (args[0] === 'inspect') {
  console.log(JSON.stringify({instance: {id: args[1], status: args[1] === ids.at(-1) ? 'ready' : 'destroyed',
    plan: {lifetime: args[1] === ids.at(-1) ? 'persistent' : 'disposable'},
    execution: args[1] === ids.at(-1) ? {} : {completed_at: 'done', terminal_exit_code: 0}}}));
} else if (args[0] === 'logs') {
  console.log(JSON.stringify({instance_id: args[1], output: 'retained evidence'}));
} else if (args[0] === 'destroy') {
  assert.ok(existsSync(output + '/' + args[1] + '/result.json'), 'collection must precede destruction');
  appendFileSync(root + '/destroyed', args[1] + '\\n');
} else if (args[0] === 'gateway') {
  assert.deepEqual(readFileSync(root + '/destroyed', 'utf8').trim().split('\\n'), ids);
} else assert.equal(args[0], 'daemon');
`, { mode: 0o700 });
    await writeFile(join(owner.root, 'daemon-attempted'), '');
    await writeFile(join(owner.root, 'malformed'), '');
    await assert.rejects(cleanup(owner), /discovery cursor/);
    await verifyOwner(owner);
    await assert.rejects(lstat(join(owner.root, 'destroyed')), { code: 'ENOENT' });
    await assert.rejects(command(process.execPath, [script('job.mjs'), 'collect', output], {
      env: { ...process.env, BACKLOT_SETUP_OWNER: JSON.stringify(owner) }
    }), /discovery cursor/);
    await assert.rejects(lstat(output), { code: 'ENOENT' });
    await rm(join(owner.root, 'malformed'));
    await command(process.execPath, [script('job.mjs'), 'collect', output], {
      env: { ...process.env, BACKLOT_SETUP_OWNER: JSON.stringify(owner) }
    });
    assert.equal((await readdir(output)).length, 101);
    const last = '65'.padStart(64, '0');
    assert.equal(JSON.parse(await readFile(join(output, last, 'result.json'))).status, 'ready');
    const first = '1'.padStart(64, '0');
    assert.equal(JSON.parse(await readFile(join(output, first, 'result.json'))).terminal_exit_code, 0);
    assert.match(await readFile(join(output, last, 'logs.ndjson'), 'utf8'), /retained evidence/);
    await cleanup(owner);
    await assert.rejects(lstat(owner.root), { code: 'ENOENT' });
    assert.equal((await readdir(output)).length, 101, 'cleanup retains exported evidence');
  } finally {
    await cleanup(owner);
    await rm(output, { recursive: true, force: true });
    await assert.rejects(lstat(output), { code: 'ENOENT' });
  }
});

test('discovery rejects malformed, incomplete, stuck pages and expired deadlines', async () => {
  const ids = Array.from({ length: 100 }, (_, i) => ({ id: (i + 1).toString(16).padStart(64, '0') }));
  const page = { api_version: 'v1', instances: ids, next_after_id: ids.at(-1).id };
  assert.deepEqual(await listInstances(async () => JSON.stringify({ api_version: 'v1', instances: [] }), Date.now() + 1000), []);
  for (const bad of [{}, { api_version: 'backlot/v1', instances: [] },
    { ...page, api_version: 'backlot/v1' }, { ...page, instances: null }, { ...page, next_after_id: null },
    { ...page, next_after_id: 'invalid' }, { ...page, instances: ids.slice(0, 1) }]) {
    await assert.rejects(listInstances(async () => JSON.stringify(bad), Date.now() + 1000), /malformed/);
  }
  let calls = 0;
  await assert.rejects(listInstances(async () => { calls++; return JSON.stringify(page); }, Date.now() + 1000), /stuck/);
  assert.equal(calls, 2);
  calls = 0;
  await assert.rejects(listInstances(async () => {
    if (++calls === 2) throw Error('page transport failed');
    return JSON.stringify(page);
  }, Date.now() + 1000), /page transport failed/);
  await assert.rejects(listInstances(() => { throw Error('must not call'); }, Date.now()), /deadline exceeded/);
  await assert.rejects(listInstances(async () => {
    await new Promise(resolve => setTimeout(resolve, 15));
    return JSON.stringify({ api_version: 'v1', instances: [] });
  }, Date.now() + 5), /deadline exceeded/);
});

test('native wrapper preserves exact argv and fences scalar output records', async () => {
  const { argumentsInput, output } = await import('./invocation.mjs');
  assert.deepEqual(argumentsInput('["--grep","a b", "$(echo no)", "", "--shard=1/2"]'),
    ['--grep', 'a b', '$(echo no)', '', '--shard=1/2']);
  for (const value of ['{}', '[1]', '[null]', '"text"', '["\\u0000"]']) {
    assert.throws(() => argumentsInput(value));
  }
  const owner = await createOwner();
  const previous = process.env.GITHUB_OUTPUT;
  try {
    process.env.GITHUB_OUTPUT = join(owner.root, 'outputs');
    await output('runtime-failure', 'first\ninjected=value\nlast');
    await output('terminal-exit-code', undefined);
    const lines = (await readFile(process.env.GITHUB_OUTPUT, 'utf8')).trimEnd().split('\n');
    assert.match(lines[0], /^runtime-failure<</);
    assert.deepEqual(lines.slice(1, -1), ['first', 'injected=value', 'last']);
    assert.equal(lines.at(-1), lines[0].slice('runtime-failure<<'.length));
  } finally {
    if (previous === undefined) delete process.env.GITHUB_OUTPUT; else process.env.GITHUB_OUTPUT = previous;
    await cleanup(owner);
  }
});

for (const auditFails of [false, true]) test(`native wrapper joins cancelled CLI and preserves original results (audit failure: ${auditFails})`, async () => {
  const { spawn } = await import('node:child_process');
  const owner = await createOwner();
  const id = 'a'.repeat(64);
  let wrapper, done;
  const exports = [];
  try {
    await mkdir(join(owner.root, 'tools'));
    await writeFile(join(owner.root, 'tools/backlot'), `#!${process.execPath}
import { writeFileSync, readFileSync, appendFileSync } from 'node:fs';
const root = ${JSON.stringify(owner.root)}, id = ${JSON.stringify(id)};
const args = process.argv.slice(2);
const instance = {id, status:'cancelled', plan:{lifetime:'disposable'}, execution:{completed_at:'done', terminal_exit_code:-1, cancelled:true}};
if (args[0] === 'plan') console.log(JSON.stringify({api_version:'v1', lifetime:'disposable'}));
else if (args[0] === 'run') {
  const done = signal => {
    writeFileSync(root+'/cli-signal', signal);
    // A final result after signal proves the wrapper joined rather than
    // exiting on delivery and leaving its owned child renewing a lease.
    setTimeout(() => { console.log(JSON.stringify({api_version:'v1',instance})); process.exit(130); }, 100);
  };
  process.once('SIGINT', () => done('SIGINT'));
  process.once('SIGTERM', () => done('SIGTERM'));
  writeFileSync(root+'/running','ready');
  setInterval(() => {}, 1000);
  setTimeout(() => process.exit(9), 10000); // Bound a broken signal relay without orphaning a fixture.
} else if (args[0] === 'inspect') console.log(JSON.stringify({api_version:'v1',instance}));
else if (args[0] === 'logs') console.log(${auditFails} ? 'invalid NDJSON' : JSON.stringify({api_version:'v1',entries:[]}));
else if (args[0] === 'destroy') {
  if (args[1] !== id) throw Error('foreign record');
  appendFileSync(root+'/destroyed',args[1]);
} else throw Error('wrapper must not list, stop gateway or stop daemon');
`, { mode: 0o700 });
    const output = join(owner.root, 'outputs');
    await mkdir(owner.root + '-exports'); exports.push(owner.root + '-exports');
    wrapper = spawn(process.execPath, [script('../run/run.mjs')], { env: { ...process.env,
      BACKLOT_SETUP_OWNER: JSON.stringify(owner), GITHUB_OUTPUT: output,
      INPUT_SCENE: 'browser', INPUT_PROJECT: owner.root, INPUT_ARGS: '[]',
      'INPUT_UPLOAD-ARTIFACTS': 'false', RUNNER_TEMP: owner.root + '-exports' }, stdio: ['ignore', 'pipe', 'pipe'] });
    let logs = '';
    wrapper.stdout.on('data', chunk => { logs += chunk; }); wrapper.stderr.on('data', chunk => { logs += chunk; });
    done = new Promise((resolve, reject) => { wrapper.once('error', reject); wrapper.once('close', (code, signal) => resolve({ code, signal })); });
    for (let attempt = 0; ; attempt++) {
      try { await lstat(join(owner.root, 'running')); break; }
      catch (error) { assert.equal(error.code, 'ENOENT'); assert.ok(attempt < 100, logs); await new Promise(resolve => setTimeout(resolve, 20)); }
    }
    wrapper.kill('SIGINT');
    const result = await done;
    assert.equal(result.code, 130, logs); assert.equal(result.signal, null);
    assert.equal(await readFile(join(owner.root, 'cli-signal'), 'utf8'), 'SIGINT');
    assert.equal(await readFile(join(owner.root, 'destroyed'), 'utf8'), id);
    const values = await readFile(output, 'utf8');
    assert.match(values, /cli-exit-code<<[^\n]+\n130\n/);
    assert.match(values, /terminal-exit-code<<[^\n]+\n-1\n/);
    if (auditFails) {
      assert.doesNotMatch(values, /evidence-available<<[^\n]+\ntrue\n/);
      assert.match(values, /collection-error<</);
      assert.doesNotMatch(values, /evidence-path<</);
    } else assert.match(values, /evidence-available<<[^\n]+\ntrue\n/);
    assert.match(values, /wrapper-signal<<[^\n]+\nSIGINT\n/);
    await verifyOwner(owner); // Shared setup state remains available.
  } finally {
    if (wrapper && wrapper.exitCode === null) { wrapper.kill('SIGINT'); await done; }
    await cleanup(owner);
    for (const path of exports) await rm(path, { recursive: true, force: true });
  }
});
