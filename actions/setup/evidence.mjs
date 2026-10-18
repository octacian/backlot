import assert from 'node:assert/strict';
import { lstat, mkdir, readdir, readFile, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';

// Copy only regular evidence files, never links, keys, private state or CA storage.
export async function safeCopy(source, destination, checkpoint = () => {}) {
  checkpoint();
  const stat = await lstat(source);
  assert.ok(!stat.isSymbolicLink(), 'evidence link rejected');
  if (stat.isDirectory()) {
    await mkdir(destination, { recursive: true });
    for (const name of await readdir(source)) await safeCopy(join(source, name), join(destination, name), checkpoint);
  } else {
    assert.ok(stat.isFile(), 'special evidence file rejected');
    const data = await readFile(source);
    assert.ok(!/-----BEGIN (?:[A-Z0-9 ]* )?PRIVATE KEY-----/.test(data.toString()), 'private key in evidence');
    assert.ok(!/\.(key|p12|pfx)$/.test(source), 'private key filename in evidence');
    await mkdir(resolve(destination, '..'), { recursive: true });
    await writeFile(destination, data, { mode: 0o600 });
  }
}

// Actions may advance to always() steps before a killed client lease settles.
// Observe only: never start/cancel work or wait for a persistent service to exit.
export async function settleForExport(instance, inspect, deadline) {
  while (instance.plan?.lifetime === 'disposable' && !instance.execution?.completed_at &&
      (instance.execution || ['preparing', 'prepared', 'starting', 'stopping'].includes(instance.status))) {
    const remaining = deadline - Date.now();
    assert.ok(remaining > 0, 'disposable result did not settle before export deadline; evidence not published');
    await new Promise(resolve => setTimeout(resolve, Math.min(100, remaining)));
    instance = await inspect(instance.id, Math.max(1, deadline - Date.now()));
  }
  return instance;
}
