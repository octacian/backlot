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
test('shared exporter rejects links, plaintext keys and keys hidden inside trace ZIPs', async () => {
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
    // Exercise safeCopy followed by the publication auditor, with no real key.
    for (const [label, name, nested] of [['lowercase', 'trace.zip', false],
      ['uppercase', 'trace.ZIP', false], ['nested-uppercase', 'trace.zip', true]]) {
      const source = join(owner.root, label); await mkdir(source);
      await command('python3', ['-c', `import io,sys,zipfile
buf=io.BytesIO()
with zipfile.ZipFile(buf,"w",compression=zipfile.ZIP_DEFLATED) as z:
    z.writestr("nested/root.pem", "-----BEGIN PRIVATE KEY-----")
with zipfile.ZipFile(sys.argv[1],"w",compression=zipfile.ZIP_DEFLATED) as z:
    z.writestr("inner.ZIP",buf.getvalue()) if sys.argv[2] == "true" else z.writestr("nested/root.pem", "-----BEGIN PRIVATE KEY-----")
`, join(source, name), String(nested)]);
      const copied = join(owner.root, label + '-copy');
      await safeCopy(source, copied);
      await assert.rejects(command('python3', [script('audit.py'), copied]), /private key rejected/);
    }
    const invalid = join(owner.root, 'invalid-archive'); await mkdir(invalid);
    await writeFile(join(invalid, 'trace.ZIP'), 'uninspected data');
    await assert.rejects(command('python3', [script('audit.py'), invalid]), /not a zip file/);
  } finally { await cleanup(owner); }
});
test('real setup failure after daemon start is cleaned by native post hook', {
  skip: process.env.BACKLOT_BINARY ? false : 'requires action-installed binary on Ubuntu; no daemon allocated'
}, async () => {
  const owner = await createOwner();
  try {
    await mkdir(join(owner.root, 'tools'));
    await symlink(process.env.BACKLOT_BINARY, join(owner.root, 'tools/backlot'));
    await writeFile(join(owner.root, 'daemon-attempted'), '');
    await assert.rejects(prepare({ root: owner.root, binary: process.env.BACKLOT_BINARY,
      caddy: join(owner.root, 'missing-caddy'), node: process.execPath }), /gateway.*failed/s);
    await command(process.execPath, [script('post.mjs')], { env: { ...process.env, STATE_owner: JSON.stringify(owner) }, timeout: 100_000 });
    await assert.rejects(lstat(owner.root), { code: 'ENOENT' });
  } finally { await cleanup(owner); }
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
  console.log(JSON.stringify({api_version: 'backlot/v1', instances: page.map(id => ({id})),
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
    await rm(join(owner.root, 'export-staging'), { recursive: true });
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
  const page = { api_version: 'backlot/v1', instances: ids, next_after_id: ids.at(-1).id };
  for (const bad of [{}, { ...page, instances: null }, { ...page, next_after_id: null },
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
    return JSON.stringify({ api_version: 'backlot/v1', instances: [] });
  }, Date.now() + 5), /deadline exceeded/);
});
