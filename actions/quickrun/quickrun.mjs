import { setup } from '../setup/setup.mjs';
import { invoke, output } from '../setup/invocation.mjs';
import { cleanup } from '../setup/runtime.mjs';

let owner;
await output('evidence-available', false);
try {
  owner = await setup();
  process.exitCode = await invoke();
} catch (error) {
  await output('infrastructure-error', error.message);
  console.error(error.stack);
  process.exitCode = 1;
} finally {
  if (owner) {
    try { await cleanup(owner); }
    catch (error) {
      await output('job-cleanup-error', error.message);
      console.error(`Final private job cleanup: ${error.stack}`);
      if (!process.exitCode) process.exitCode = 1;
    }
  }
}
