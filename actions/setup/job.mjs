// Workflow adapter: portable evidence selection, audit and publication belong to Backlot.
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { command, verifyOwner, cleanup, listInstances } from './runtime.mjs';

export async function collect(owner, destination, ids, onResult = async () => {}) {
  await verifyOwner(owner);
  const output = resolve(destination);
  const binary = join(owner.root, 'tools/backlot');
  const state = join(owner.root, 'd');
  const deadline = Date.now() + 45_000;
  const instances = ids ? ids.map(id => ({ id })) : await listInstances((after, timeout) => command(binary,
    ['list', ...(after ? ['--after', after] : []), '--state-dir', state, '--json'], { timeout }), deadline);
  // A one-run action exports exactly one public bundle. Job-wide collection is
  // retained only for explicit helper callers; each child is independently atomic.
  if (!ids) await mkdir(output);
  for (const item of instances) {
    assert.match(item.id, /^[a-f0-9]{64}$/);
    const target = ids?.length === 1 ? output : join(output, item.id);
    let exported;
    try {
      exported = JSON.parse(await command(binary, ['export', item.id, '--output', target,
        '--wait=45s', '--state-dir', state, '--json'], { timeout: 50_000 }));
    } catch (error) {
      // Failed publication can still return an observed original result through
      // the public export response. No private runtime record is interpreted.
      let failed;
      try { failed = JSON.parse(error.stdout); } catch {}
      if (failed?.api_version === 'v1' && failed.result?.instance_id === item.id) await onResult(failed.result);
      if (/unknown command|unknown v1 endpoint/.test(error.message)) {
        throw Error('Selected Backlot runtime does not support public export; pin an export-capable revision for issue #41.');
      }
      throw error;
    }
    assert.equal(exported.api_version, 'v1');
    assert.equal(exported.published, true, 'export did not publish a bundle');
    assert.equal(exported.complete, true, 'action requires complete evidence');
    assert.equal(exported.result.instance_id, item.id);
    await onResult(exported.result);
  }
  console.log(`Collected public Backlot evidence: ${output}`);
  return instances.map(item => item.id);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  try {
    const owner = JSON.parse(process.env.BACKLOT_SETUP_OWNER);
    if (process.argv[2] === 'cleanup') await cleanup(owner);
    else if (process.argv[2] === 'collect') await collect(owner, process.argv[3]);
    else throw Error('usage: job.mjs collect ABSOLUTE_EXPORT_PATH | cleanup');
  } catch (error) { console.error(error.stack); process.exitCode = 1; }
}
