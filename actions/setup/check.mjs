// Action-only gaps; browser/lifecycle/fault assertions stay in the existing suite.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdir, writeFile, lstat, symlink } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { command, createOwner, verifyOwner, prepare, cleanup } from './runtime.mjs';
import { safeCopy } from './evidence.mjs';

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
    const zip = join(owner.root, 'zip'); await mkdir(zip);
    await command('python3', ['-c', 'import sys,zipfile; z=zipfile.ZipFile(sys.argv[1],"w"); z.writestr("nested/root.pem", "-----BEGIN PRIVATE KEY-----"); z.close()', join(zip, 'trace.zip')]);
    await assert.rejects(command('python3', [script('audit.py'), zip]), /private key rejected/);
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
