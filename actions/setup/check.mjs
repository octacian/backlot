// Action-only gaps; browser/lifecycle/fault assertions stay in the existing suite.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdir, writeFile, lstat, symlink, readFile, readdir, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { command, createOwner, verifyOwner, prepare, cleanup, listInstances, backlotEnvironment } from './runtime.mjs';


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
    await assert.rejects(prepare({ owner, root: owner.root, binary: process.env.BACKLOT_BINARY,
      caddy: join(owner.root, 'missing-caddy'), node: process.execPath }), /gateway.*failed/s);
    const cli = async (...args) => command(process.env.BACKLOT_BINARY,
      [...args, '--state-dir', join(owner.root, 'd'), '--json'], { env: await backlotEnvironment(owner) });
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
    await writeFile(join(owner.root, 'metadata-machine.json'), '{"version":"backlot/v1"}', { mode: 0o600 });
    const { instance } = JSON.parse(await cli('prepare', 'dev', '--project', script('../../examples/adoption'), '--config', join(owner.root, 'metadata-machine.json')));
    await assert.rejects(collect(preparedOutput), /no recorded execution|no_execution/);
    await command(process.execPath, [script('post.mjs')], { env: { ...process.env, STATE_owner: JSON.stringify(owner) }, timeout: 100_000 });
    await assert.rejects(lstat(owner.root), { code: 'ENOENT' });
    assert.deepEqual(await readdir(emptyOutput), []);
    assert.deepEqual(await readdir(preparedOutput), []);
  } finally {
    await cleanup(owner);
    await rm(emptyOutput, { recursive: true, force: true });
    await rm(preparedOutput, { recursive: true, force: true });
    await assert.rejects(lstat(emptyOutput), { code: 'ENOENT' });
    await assert.rejects(lstat(preparedOutput), { code: 'ENOENT' });
  }
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
} else if (args[0] === 'export') {
  const target=args[args.indexOf('--output')+1];
  const fs=await import('node:fs/promises');await fs.mkdir(target);
  await fs.writeFile(target+'/report.json',JSON.stringify({schema_version:'backlot.export/v1',result:{instance_id:args[1],status:'succeeded',terminal_exit_code:0}}));
  await fs.writeFile(target+'/logs.ndjson','retained evidence');
  console.log(JSON.stringify({api_version:'v1',published:true,complete:true,result:{instance_id:args[1],status:'succeeded',terminal_exit_code:0}}));
} else if (args[0] === 'destroy') {
  assert.ok(existsSync(output + '/' + args[1] + '/report.json'), 'collection must precede destruction');
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
    assert.equal(JSON.parse(await readFile(join(output, last, 'report.json'))).result.status, 'succeeded');
    const first = '1'.padStart(64, '0');
    assert.equal(JSON.parse(await readFile(join(output, first, 'report.json'))).result.terminal_exit_code, 0);
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

for (const scenario of [
  { name: 'clean cancellation', signal: 'SIGINT' },
  { name: 'audit rejection', signal: 'SIGINT', auditFails: true },
  { name: 'declared artifact collection failure', signal: 'SIGINT', collectionFailure: 'declared artifact missing' },
  { name: 'settled native collection failure', signal: 'SIGTERM', collectionFailure: 'application output collection failed', lateFailure: true },
  { name: 'native logs gap', signal: 'SIGINT', logGap: 'retained output interrupted' },
  { name: 'expired evidence', signal: 'SIGTERM', expired: true },
  { name: 'ordinary assertion failure' }
]) test(`native wrapper preserves original results and complete-evidence availability: ${scenario.name}`, async () => {
  const { signal, auditFails, collectionFailure, lateFailure, logGap, expired } = scenario;
  const unavailable = Boolean(auditFails || collectionFailure || logGap || expired);
  const nativeCode = signal ? -1 : 23;
  const cliCode = collectionFailure ? 1 : !signal ? nativeCode : signal === 'SIGTERM' ? 143 : 130;
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
const instance = {id, status:${JSON.stringify(collectionFailure || !signal ? 'failed' : 'cancelled')}, plan:{lifetime:'disposable'},
  execution:{completed_at:'2026-10-18T00:00:00Z', terminal_exit_code:${nativeCode}, cancelled:${Boolean(signal)},
    collection_failure:${JSON.stringify(collectionFailure)}, evidence_expired:${Boolean(expired)}}};
if (args[0] === 'plan') console.log(JSON.stringify({api_version:'v1', lifetime:'disposable'}));
else if (args[0] === 'run') {
  if (${!signal}) { console.log(JSON.stringify({api_version:'v1',instance})); process.exit(${cliCode}); }
  const done = signal => {
    writeFileSync(root+'/cli-signal', signal);
    // A final result after signal proves the wrapper joined rather than
    // exiting on delivery and leaving its owned child renewing a lease.
    setTimeout(() => {
      if (${Boolean(lateFailure)}) { instance.status = 'stopping'; delete instance.execution.completed_at; delete instance.execution.collection_failure; }
      console.log(JSON.stringify({api_version:'v1',instance})); process.exit(${cliCode});
    }, 100);
  };
  process.once('SIGINT', () => done('SIGINT'));
  process.once('SIGTERM', () => done('SIGTERM'));
  writeFileSync(root+'/running','ready');
  setInterval(() => {}, 1000);
  setTimeout(() => process.exit(9), 10000); // Bound a broken signal relay without orphaning a fixture.
} else if (args[0] === 'inspect') console.log(JSON.stringify({api_version:'v1',instance}));
else if (args[0] === 'export') {
  const result={instance_id:id,status:instance.status,...instance.execution};
  if (${Boolean(auditFails || collectionFailure || logGap || expired)}) {
    console.log(JSON.stringify({api_version:'v1',result,error:{code:'export_incomplete',message:'known incomplete evidence'}}));
    console.error(${JSON.stringify(auditFails ? 'evidence safety audit failed' : collectionFailure ? 'runtime evidence collection failed: '+collectionFailure : expired ? 'evidence expired' : 'known log gap')});process.exit(1);
  }
  const fs=await import('node:fs/promises');await fs.mkdir(args[args.indexOf('--output')+1]);
  console.log(JSON.stringify({api_version:'v1',published:true,complete:true,result}));
}
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
      // Rejected exports must never reach the SDK even with upload enabled.
      'INPUT_UPLOAD-ARTIFACTS': unavailable ? 'true' : 'false', RUNNER_TEMP: owner.root + '-exports' }, stdio: ['ignore', 'pipe', 'pipe'] });
    let logs = '';
    wrapper.stdout.on('data', chunk => { logs += chunk; }); wrapper.stderr.on('data', chunk => { logs += chunk; });
    done = new Promise((resolve, reject) => { wrapper.once('error', reject); wrapper.once('close', (code, signal) => resolve({ code, signal })); });
    for (let attempt = 0; signal; attempt++) {
      try { await lstat(join(owner.root, 'running')); break; }
      catch (error) { assert.equal(error.code, 'ENOENT'); assert.ok(attempt < 100, logs); await new Promise(resolve => setTimeout(resolve, 20)); }
    }
    if (signal) wrapper.kill(signal);
    const result = await done;
    assert.equal(result.code, signal === 'SIGTERM' ? 143 : signal ? 130 : cliCode, logs); assert.equal(result.signal, null);
    if (signal) assert.equal(await readFile(join(owner.root, 'cli-signal'), 'utf8'), signal);
    assert.equal(await readFile(join(owner.root, 'destroyed'), 'utf8'), id);
    const values = await readFile(output, 'utf8');
    assert.match(values, new RegExp(`cli-exit-code<<[^\\n]+\\n${cliCode}\\n`));
    assert.match(values, new RegExp(`terminal-exit-code<<[^\\n]+\\n${nativeCode}\\n`));
    assert.match(values, new RegExp(`cancelled<<[^\\n]+\\n${Boolean(signal)}\\n`));
    assert.match(values, new RegExp(`status<<[^\\n]+\\n${collectionFailure || !signal ? 'failed' : 'cancelled'}\\n`));
    if (collectionFailure) assert.match(values, new RegExp(`runtime-collection-failure<<[^\\n]+\\n${collectionFailure}\\n`));
    if (unavailable) {
      assert.doesNotMatch(values, /evidence-available<<[^\n]+\ntrue\n/);
      assert.match(values, /collection-error<</);
      assert.doesNotMatch(values, /evidence-path<</);
      assert.doesNotMatch(values, /(?:upload-error|artifact-id)<</);
      const parents = await readdir(owner.root + '-exports');
      for (const parent of parents) await assert.rejects(lstat(join(owner.root + '-exports', parent, 'evidence')), { code: 'ENOENT' });
      assert.match(logs, auditFails ? /safety audit/ : collectionFailure ? /runtime evidence collection failed/ : expired ? /expired/ : /known log gap/);
    } else assert.match(values, /evidence-available<<[^\n]+\ntrue\n/);
    if (signal) assert.match(values, new RegExp(`wrapper-signal<<[^\\n]+\\n${signal}\\n`));
    await verifyOwner(owner); // Shared setup state remains available.
  } finally {
    if (wrapper && wrapper.exitCode === null) { wrapper.kill('SIGINT'); await done; }
    await cleanup(owner);
    for (const path of exports) await rm(path, { recursive: true, force: true });
  }
});
