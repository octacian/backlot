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

