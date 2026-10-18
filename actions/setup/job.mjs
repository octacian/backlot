// Workflow-owned collection and final cleanup. Never invokes application tests.
import assert from 'node:assert/strict';
import { mkdir, writeFile, rename, rm } from 'node:fs/promises';
import { join, relative, isAbsolute, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { command, verifyOwner, cleanup } from './runtime.mjs';
import { safeCopy } from './evidence.mjs';

try {
  const owner = JSON.parse(process.env.BACKLOT_SETUP_OWNER);
  if (process.argv[2] === 'cleanup') {
    await cleanup(owner);
  } else if (process.argv[2] === 'collect') {
    await verifyOwner(owner);
    const output = resolve(process.argv[3]);
    const rel = relative(owner.root, output);
    assert.ok(rel.startsWith('..') && !isAbsolute(rel), 'export must be outside private job state');
    const cli = (...args) => command(join(owner.root, 'tools/backlot'), [...args, '--state-dir', join(owner.root, 'd'), '--json']);
    const staging = join(owner.root, 'export-staging');
    await mkdir(staging); // Existing/partial staging requires diagnosis, never reuse it.
    for (const item of JSON.parse(await cli('list')).instances) {
      const { instance } = JSON.parse(await cli('inspect', item.id));
      const e = instance.execution;
      const destination = join(staging, instance.id);
      await mkdir(destination);
      await writeFile(join(destination, 'result.json'), JSON.stringify({ id: instance.id, status: instance.status,
        origin: e?.origin, terminal_exit_code: e?.terminal_exit_code, cancelled: e?.cancelled,
        failure: e?.failure, collection_failure: e?.collection_failure, cleanup_failure: e?.cleanup_failure }, null, 2));
      await writeFile(join(destination, 'logs.ndjson'), await cli('logs', instance.id));
      for (const [index, artifact] of (e?.artifacts ?? []).entries()) {
        const rel = relative(join(owner.root, 'd/artifacts'), artifact.path);
        assert.ok(rel && !rel.startsWith('..') && !isAbsolute(rel), 'artifact outside daemon evidence');
        await safeCopy(artifact.path, join(destination, 'artifacts', String(index)));
      }
    }
    await command('python3', [fileURLToPath(new URL('audit.py', import.meta.url)), staging]);
    // Rename requires same filesystem; runners use /tmp and RUNNER_TEMP on /.
    // Output must not exist; workflows give each job an independent destination.
    const { lstat } = await import('node:fs/promises');
    try { await lstat(output); throw new Error('evidence destination already exists'); }
    catch (error) { if (error.code !== 'ENOENT') throw error; }
    await rename(staging, output);
    console.log(`Collected audited application evidence: ${output}`);
  } else throw new Error('usage: job.mjs collect ABSOLUTE_EXPORT_PATH | cleanup');
} catch (error) {
  console.error(error.stack);
  process.exitCode = 1;
}
