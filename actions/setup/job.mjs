// Workflow-owned collection and final cleanup. Never invokes application tests.
import assert from 'node:assert/strict';
import { mkdir, writeFile, rename, mkdtemp } from 'node:fs/promises';
import { join, relative, isAbsolute, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { command, verifyOwner, cleanup, listInstances } from './runtime.mjs';
import { safeCopy, settleForExport } from './evidence.mjs';

export async function collect(owner, destination, ids, onResult = async () => {}) {
 const output = resolve(destination);
    await verifyOwner(owner);
    const rel = relative(owner.root, output);
    assert.ok(rel.startsWith('..') && !isAbsolute(rel), 'export must be outside private job state');
    const staging = await mkdtemp(join(owner.root, 'export-staging-'));
    const deadline = Date.now() + 45_000;
    const instances = ids ? ids.map(id => ({ id })) : await listInstances((after, timeout) => command(join(owner.root, 'tools/backlot'),
      ['list', ...(after ? ['--after', after] : []), '--state-dir', join(owner.root, 'd'), '--json'], { timeout }), deadline);
    for (const item of instances) {
      const inspect = async (id, timeout) => JSON.parse(await command(join(owner.root, 'tools/backlot'),
        ['inspect', id, '--state-dir', join(owner.root, 'd'), '--json'], { timeout })).instance;
      const instance = await settleForExport(await inspect(item.id, 20_000), inspect, deadline);
      const e = instance.execution;
      const result = { id: instance.id, status: instance.status,
        origin: e?.origin, completed_at: e?.completed_at, terminal_exit_code: e?.terminal_exit_code, cancelled: e ? e.cancelled ?? false : undefined,
        failure: e?.failure, collection_failure: e?.collection_failure, cleanup_failure: e?.cleanup_failure };
      // Preserve final runtime outcomes even when their evidence cannot be published.
      await onResult(result);
      assert.ok(!e?.collection_failure, `runtime evidence collection failed: ${e?.collection_failure}`);
      assert.ok(!e?.evidence_expired, 'runtime evidence expired by retention policy');
      const destination = join(staging, instance.id);
      await mkdir(destination);
      await writeFile(join(destination, 'result.json'), JSON.stringify(result, null, 2));
      // The native CLI emits LogRecord NDJSON, but reports gaps on stderr and
      // can still exit zero. Reject diagnostics rather than losing that signal.
      await writeFile(join(destination, 'logs.ndjson'), await command(join(owner.root, 'tools/backlot'),
        ['logs', instance.id, '--state-dir', join(owner.root, 'd'), '--json'], { rejectStderr: true }));
      for (const [index, artifact] of (e?.artifacts ?? []).entries()) {
        const rel = relative(join(owner.root, 'd/artifacts'), artifact.path);
        assert.ok(rel && !rel.startsWith('..') && !isAbsolute(rel), 'artifact outside daemon evidence');
        await safeCopy(artifact.path, join(destination, 'artifacts', String(index)));
      }
    }
    await command(process.env.BACKLOT_AUDIT_BINARY ?? fileURLToPath(new URL('audit-bin', import.meta.url)), [staging]);
    // Rename requires same filesystem; runners use /tmp and RUNNER_TEMP on /.
    // Output must not exist; workflows give each job an independent destination.
    const { lstat } = await import('node:fs/promises');
    try { await lstat(output); throw new Error('evidence destination already exists'); }
    catch (error) { if (error.code !== 'ENOENT') throw error; }
    await rename(staging, output);
    console.log(`Collected audited application evidence: ${output}`);
    return instances.map(item => item.id);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
 try {
  const owner = JSON.parse(process.env.BACKLOT_SETUP_OWNER);
  if (process.argv[2] === "cleanup") await cleanup(owner);
  else if (process.argv[2] === "collect") await collect(owner, process.argv[3]);
  else throw Error("usage: job.mjs collect ABSOLUTE_EXPORT_PATH | cleanup");
 } catch (error) { console.error(error.stack); process.exitCode = 1; }
}
