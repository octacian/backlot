// Application content deliberately contains only public evidence.
import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
assert.ok(process.env.CREDENTIAL?.includes('\n'));
assert.match(process.env.TOKEN ?? '', /^[0-9a-f]{64}$/);
assert.ok(['west', 'east'].includes(process.env.REGION));
assert.equal(process.env.HOME, process.env.EXPECTED_HOME);
assert.equal(process.env.XDG_CONFIG_HOME ?? '', process.env.EXPECTED_XDG);
for (const name of ['INPUT_PROJECT-INPUTS', 'INPUT_PROJECT-GENERATE-INPUTS', 'BACKLOT_PROJECT_CONFIG_DIR']) assert.equal(process.env[name], undefined);
await mkdir('reports', { recursive: true });
await writeFile('reports/result.json', JSON.stringify({ checked: true, region: process.env.REGION }));
console.log('Private input and environment checks passed');
if (process.argv[2] === 'fail') process.exitCode = 17;
