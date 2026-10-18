import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';

// Storage containment alone cannot prove who produced a shared checkout report.
// Bind the browser invocation to the destination run's retained plan and origin.
export async function verifyReports(instance, artifact) {
  assert.equal(artifact.name, 'reports');
  assert.equal(artifact.component, instance.plan.terminal_job);
  assert.equal(artifact.attempt, instance.execution.attempt);
  assert.ok(instance.execution.components.some(c => c.name === artifact.component && c.status !== 'skipped'),
    'unlaunched terminal cannot own reports');
  const terminal = instance.plan.components.find(c => c.name === instance.plan.terminal_job);
  assert.equal(terminal.command.args[0], 'scene.mjs');
  const invocation = JSON.parse(await readFile(join(artifact.path, 'reports/invocation.json')));
  assert.equal(invocation.origin, instance.execution.origin, 'reports belong to a different run');
  assert.deepEqual(invocation.args, terminal.command.args.slice(1), 'reports contain a different argument vector');
  try {
    const holding = JSON.parse(await readFile(join(artifact.path, 'reports/holding.json')));
    assert.equal(holding.origin, instance.execution.origin, 'holding report belongs to a different run');
  } catch (error) { if (error.code !== 'ENOENT') throw error; }
  const report = JSON.parse(await readFile(join(artifact.path, 'reports/playwright.json')));
  assert.ok(Array.isArray(report.suites) && report.stats && !Array.isArray(report.stats));
  return invocation;
}
