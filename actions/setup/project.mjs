// Structured action inputs are private transport, never a new registry writer.
import { resolve } from 'node:path';
import { command, backlotEnvironment } from './runtime.mjs';

const limit = 1 << 20;
const identifier = /^[a-z][a-z0-9_-]{0,62}$/;
const bad = () => Error('Invalid project setup input; use a JSON string-value object and an explicit JSON name array');
export function mask(value) {
  console.log(`::add-mask::${value.replaceAll('%', '%25').replaceAll('\r', '%0D').replaceAll('\n', '%0A')}`);
}

// Parse the two flat shapes before conversion, retaining duplicate decoded keys.
// Syntax errors deliberately have no payload excerpts or native parser details.
function parse(text, object, register) {
  if (Buffer.byteLength(text) > limit) throw bad();
  let position = 0;
  const space = () => { while (/[\t\r\n ]/.test(text[position] ?? '') && position < text.length) position++; };
  const take = char => { space(); if (text[position++] !== char) throw bad(); };
  const string = () => {
    space();
    const match = /^"(?:[^"\\\x00-\x1f]|\\(?:["\\/bfnrt]|u[0-9a-fA-F]{4}))*"/.exec(text.slice(position));
    if (!match) throw bad();
    position += match[0].length;
    return JSON.parse(match[0]);
  };
  const values = object ? Object.create(null) : [];
  const seen = new Set();
  take(object ? '{' : '['); space();
  if (text[position] !== (object ? '}' : ']')) for (;;) {
    const key = string();
    let value = key;
    if (object) {
      take(':'); value = string();
      // Invalid transport scalars receive a fixed error without attempting an
      // empty/NUL workflow command. Valid supplied values are masked before
      // name, duplication, membership or subprocess validation can report errors.
      if (value === '' || value.includes('\0')) throw bad();
      register(value);
    }
    if (!identifier.test(key) || seen.has(key) || value.includes('\0') || (object && value === '')) throw bad();
    seen.add(key);
    if (object) values[key] = value; else values.push(key);
    space(); if (text[position] !== ',') break;
    position++;
  }
  take(object ? '}' : ']'); space();
  if (position !== text.length) throw bad();
  return values;
}

export function captureProjectInputs(register = mask) {
  const raw = process.env['INPUT_PROJECT-INPUTS'] ?? '';
  const generated = process.env['INPUT_PROJECT-GENERATE-INPUTS'] ?? '';
  // Drop payloads from this process too: SDKs and arbitrary child adapters inherit it.
  delete process.env['INPUT_PROJECT-INPUTS'];
  delete process.env['INPUT_PROJECT-GENERATE-INPUTS'];
  const inputs = raw === '' ? Object.create(null) : parse(raw, true, register);
  const generate = generated === '' ? [] : parse(generated, false, register);
  if (generate.some(name => Object.hasOwn(inputs, name))) throw bad();
  const project = resolve(process.env.INPUT_PROJECT || '.');
  return { requested: raw !== '' || generated !== '', project, inputs, generate };
}

// Version discovery does not resolve configuration or touch any registry.
export async function requireProjectRuntime(owner, binary) {
  const raw = await command(binary, ['version', '--json'], { env: await backlotEnvironment(owner), sensitive: true });
  let supported = false;
  try { const version = JSON.parse(raw); supported = version.api_version === 'v1' && version.state_version === 10; } catch {}
  if (!supported) throw Error('Private job configuration requires a compatible state-version-10 Backlot revision');
}

export async function setupProject(owner, binary, config, options, register = mask) {
  if (!options.requested) return;
  const env = await backlotEnvironment(owner);
  const base = ['project', 'setup', '--project', options.project, '--config', config, '--json'];
  const stdin = JSON.stringify({ version: 'backlot/v1', inputs: options.inputs });
  await command(binary, [...base, '--from', '-', ...options.generate.flatMap(name => ['--generate', name])], { env, stdin, sensitive: true });
  if (options.generate.length) {
    const raw = await command(binary, [...base, '--dangerously-reveal-secrets'], { env, sensitive: true });
    try {
      const result = JSON.parse(raw);
      for (const name of options.generate) {
        const item = result.inputs?.find(item => item.name === name);
        if (!item?.secret || typeof item.revealed_value !== 'string' || !/^[0-9a-f]{64}$/.test(item.revealed_value)) throw Error();
        register(item.revealed_value);
      }
    } catch { throw Error('Generated project input masking failed; runtime was not started'); }
  }
}
