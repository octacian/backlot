// One-scene adapter: the CLI owns execution, this adapter owns only its result.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { appendFile, mkdtemp, readdir, readFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { randomUUID } from 'node:crypto';
import { command, verifyOwner } from './runtime.mjs';
import { collect } from './job.mjs';

export function argumentsInput(value = '[]') {
  const args = JSON.parse(value);
  assert.ok(Array.isArray(args) && args.every(arg => typeof arg === 'string' && !arg.includes('\0')), 'args must be a JSON array of strings');
  return args;
}

export async function output(name, value) {
  if (value === undefined || value === null) return;
  // Outputs here are scalar IDs, statuses, paths and error strings. Use a unique
  // delimiter so application failure text cannot inject additional outputs.
  const delimiter = randomUUID();
  await appendFile(process.env.GITHUB_OUTPUT, `${name}<<${delimiter}\n${value}\n${delimiter}\n`);
}

async function resultOutputs(result) {
  for (const [key, value] of Object.entries({ status: result.status, 'terminal-exit-code': result.terminal_exit_code,
    cancelled: result.cancelled, 'runtime-failure': result.failure, 'runtime-collection-failure': result.collection_failure,
    'runtime-cleanup-failure': result.cleanup_failure })) await output(key, value);
}

export async function invoke() {
  const failures = [];
  let interrupted = '', child;
  const forward = signal => { if (!interrupted) interrupted = signal; child?.kill(signal); };
  const onInt = () => forward('SIGINT'), onTerm = () => forward('SIGTERM');
  process.on('SIGINT', onInt); process.on('SIGTERM', onTerm);
  let cliCode, cliSignal, instance, owner, evidence;
  await output('evidence-available', false);
  const fail = (stage, error) => { failures.push(stage); console.error(`${stage}: ${error.message}`); return output(`${stage}-error`, error.message); };
  try {
    owner = JSON.parse(process.env.BACKLOT_SETUP_OWNER);
    await verifyOwner(owner);
    const project = resolve(process.env.INPUT_PROJECT || '.');
    const scene = process.env.INPUT_SCENE;
    assert.ok(scene && !scene.startsWith('-'), 'scene is required');
    const args = argumentsInput(process.env.INPUT_ARGS);
    const binary = join(owner.root, 'tools/backlot');
    const base = ['--project', project, '--config', join(owner.root, 'machine.json'), '--json'];
    const plan = JSON.parse(await command(binary, ['plan', scene, ...base, '--', ...args]));
    assert.equal(plan.lifetime, 'disposable', 'actions/run requires a disposable scene');
    if (interrupted) throw Error('cancelled before scene launch');
    assert.ok(['true', 'false', undefined].includes(process.env['INPUT_UPLOAD-ARTIFACTS']), 'upload-artifacts must be true or false');
    let stdout = '';
    child = spawn(binary, ['run', scene, ...base, '--state-dir', join(owner.root, 'd'), '--', ...args], { cwd: project, stdio: ['ignore', 'pipe', 'inherit'] });
    child.stdout.on('data', chunk => { stdout += chunk; });
    // Forward to the retained actual CLI and JOIN; never leave a lease-renewing
    // child behind. Runner hard termination remains bounded by the daemon lease.
    if (interrupted) child.kill(interrupted);
    ({ code: cliCode, signal: cliSignal } = await new Promise((resolve, reject) => {
      child.once('error', reject); child.once('close', (code, signal) => resolve({ code, signal }));
    }));
    try { instance = JSON.parse(stdout).instance; } catch { /* CLI may die before its final response. */ }
    await output('cli-exit-code', cliCode);
    await output('cli-signal', cliSignal);
    await output('cancelled', interrupted ? true : instance?.execution?.cancelled);
    assert.match(instance?.id ?? '', /^[a-f0-9]{64}$/, 'CLI returned no owned instance identity; preserve state for setup post');
    await output('instance-id', instance.id);
    // Publish known original results before collection/audit can fail.
    await resultOutputs({ status: instance.status, ...instance.execution,
      cancelled: instance.execution ? instance.execution.cancelled ?? false : undefined });
    assert.equal(instance.plan?.lifetime, 'disposable');
    const parent = await mkdtemp(join(process.env.RUNNER_TEMP, 'backlot-evidence-'));
    evidence = join(parent, 'evidence');
    try {
      await collect(owner, evidence, [instance.id]);
      const result = JSON.parse(await readFile(join(evidence, instance.id, 'result.json'), 'utf8'));
      await resultOutputs(result);
      await output('evidence-path', evidence);
      await output('evidence-available', true);
    } catch (error) { evidence = undefined; await fail('collection', error); }
    // Only this invocation's authenticated record; no gateway/daemon shutdown.
    try { await command(binary, ['destroy', instance.id, '--state-dir', join(owner.root, 'd'), '--json']); }
    catch (error) { await fail('cleanup', error); }
    if (evidence && process.env['INPUT_UPLOAD-ARTIFACTS'] !== 'false') {
      try {
        const { DefaultArtifactClient } = await import('./sdk.mjs');
        const files = await readdir(evidence, { recursive: true, withFileTypes: true });
        const paths = files.filter(item => item.isFile()).map(item => join(item.parentPath, item.name));
        const name = `${process.env['INPUT_ARTIFACT-NAME'] || 'backlot'}-${instance.id}-${randomUUID()}`;
        const uploaded = await new DefaultArtifactClient().uploadArtifact(name, paths, evidence);
        await output('artifact-id', uploaded.id);
      } catch (error) { await fail('upload', error); }
    }
  } catch (error) { await fail('infrastructure', error); }
  finally {
    process.off('SIGINT', onInt); process.off('SIGTERM', onTerm);
    await output('wrapper-signal', interrupted || undefined);
  }
  return interrupted === 'SIGTERM' ? 143 : interrupted ? 130 : cliCode !== undefined && cliCode !== null && cliCode !== 0 ? cliCode : failures.length || cliSignal ? 1 : 0;
}
