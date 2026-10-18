import { cleanup } from './runtime.mjs';
// Always bounded, also when setup failed before it could publish outputs.
if (process.env.STATE_owner) {
  try {
    await cleanup(JSON.parse(process.env.STATE_owner));
  } catch (error) {
    console.error(`Post-job cleanup failed; private state preserved: ${error.stack}`);
    process.exitCode = 1;
  }
}
