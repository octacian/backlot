import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdir, writeFile, readFile, lstat, symlink } from 'node:fs/promises';
import { join } from 'node:path';
import { spawn } from 'node:child_process';
import { captureProjectInputs, mask, setupProject, requireProjectRuntime } from './project.mjs';
import { command, createOwner, cleanup, backlotEnvironment } from './runtime.mjs';

function capture(raw, generated = '', register = () => {}) {
  process.env['INPUT_PROJECT-INPUTS'] = raw;
  process.env['INPUT_PROJECT-GENERATE-INPUTS'] = generated;
  return captureProjectInputs(register);
}

test('structured options preserve duplicate checks, masks and explicit enrollment', () => {
  assert.equal(capture('').requested, false);
  assert.equal(capture('{}').requested, true);
  assert.equal(capture('', '[]').requested, true);
  const masks = [];
  const options = capture('{"token":"line\\n%value","region":"west"}', '[]', value => masks.push(value));
  assert.deepEqual(masks, ['line\n%value', 'west']);
  assert.equal(options.inputs.token, 'line\n%value');
  assert.equal(process.env['INPUT_PROJECT-INPUTS'], undefined);
  for (const [raw, names] of [
    [' ', ''], ['null', ''], ['[]', ''], ['{"a":1}', ''], ['{"a":null}', ''],
    ['{"a":""}', ''], ['{"a":"\\u0000"}', ''], ['{"a":"x","\\u0061":"y"}', ''],
    ['{"a":"x",}', ''], ['{"a":"x"} trailing', ''], ['{"a":"x"}', '["a"]'],
    ['', '["a","a"]'], ['', '[null]'], ['', '["BAD"]'], ['', ' '],
    ['{"a":"' + 'x'.repeat(1 << 20) + '"}', '']
  ]) assert.throws(() => capture(raw, names), /^Error: Invalid project setup input/);
  const invalidMasks = [];
  assert.throws(() => capture('{"a":"\\u0000"}', '', value => invalidMasks.push(value)));
  assert.deepEqual(invalidMasks, []);
  const logs = []; const original = console.log;
  try { console.log = value => logs.push(value); mask('x%\r\ny'); }
  finally { console.log = original; }
  assert.deepEqual(logs, ['::add-mask::x%25%0D%0Ay']);
});

test('bounded sensitive child transport never exposes raw failure or payload env', async () => {
  process.env['INPUT_PROJECT-INPUTS'] = 'private-env-marker';
  try {
    const value = await command(process.execPath, ['-e', 'process.stdin.pipe(process.stdout)'], { stdin: 'unicode-☃', sensitive: true });
    assert.equal(value, 'unicode-☃');
    await command(process.execPath, ['-e', 'if(Object.keys(process.env).some(k=>k.startsWith("INPUT_PROJECT-")))process.exit(1)']);
    for (const script of ['process.stderr.write("private-child-marker");process.exit(1)', 'process.stdout.write("x".repeat(10000))', 'setInterval(()=>{},100)']) {
      await assert.rejects(command(process.execPath, ['-e', script], { sensitive: true, maxBytes: 1024, timeout: 100 }), error => {
        assert.equal(error.stdout, undefined);
        assert.doesNotMatch(error.message, /private-child-marker|xxxxx/);
        return /Sensitive project setup command failed/.test(error.message);
      });
    }
    await assert.rejects(command('/absent-fixture-binary', [], { sensitive: true }), /Sensitive project setup command failed/);
  } finally { delete process.env['INPUT_PROJECT-INPUTS']; }
});

test('version fence and child registry selection preserve HOME/XDG and unrelated tools', async () => {
  const owner = await createOwner();
  try {
    const env = await backlotEnvironment(owner);
    assert.equal(env.HOME, process.env.HOME);
    assert.equal(env.XDG_CONFIG_HOME, process.env.XDG_CONFIG_HOME);
    assert.equal(env.BACKLOT_PROJECT_CONFIG_DIR, join(owner.root, 'project-config'));
    await mkdir(join(owner.root, 'tools'));
    const binary = join(owner.root, 'tools/backlot');
    await writeFile(binary, `#!${process.execPath}\nif(process.argv[2]!=='version')throw Error('resolution attempted');console.log('{"api_version":"v1"}');`, { mode: 0o700 });
    await assert.rejects(requireProjectRuntime(owner, binary), /state-version-8/);
    await assert.rejects(lstat(join(owner.root, 'project-config')), { code: 'ENOENT' });
  } finally { await cleanup(owner); }
});

test('real CLI adapter isolates projects, masks generation and leaves default registry alone', {
 skip: !process.env.BACKLOT_BINARY && 'requires explicitly built current-head Backlot binary'
}, async () => {
 const owner = await createOwner();
 try {
  await mkdir(join(owner.root, 'tools'));
  const binary = join(owner.root, 'tools/backlot');
  await symlink(process.env.BACKLOT_BINARY, binary);
  await requireProjectRuntime(owner, binary);
  const machine = join(owner.root, 'machine.json');
  await writeFile(machine, '{"version":"backlot/v1"}', { mode: 0o600 });
  const projects = [];
  for (let i = 0; i < 2; i++) {
   const project = owner.root + `-project-${i}`; projects.push(project);
   await mkdir(project);
   // Register cleanup before later setup failures, outside the selected registry root.
   const { rm } = await import('node:fs/promises');
   try {
    await writeFile(join(project, 'backlot.json'), JSON.stringify({ version:'backlot/v1', project:`private-${i}`, tools:{node:process.execPath}, inputs:{token:{secret:true, generate:'random-hex-32'}}, components:{check:{kind:'job',runtime:'native',policy:'each-start',command:{tool:'node'}}}, scenes:{test:{lifetime:'disposable',components:['check'],terminal_job:'check'}}}));
    const generated = [];
    await setupProject(owner, binary, machine, { requested:true, project, inputs:i ? {token:'second-private-marker'} : {}, generate:i ? [] : ['token'] }, value => generated.push(value));
    if (!i) assert.match(generated[0], /^[0-9a-f]{64}$/);
    const env = await backlotEnvironment(owner);
    const inspect = async () => JSON.parse(await command(binary, ['project','setup','--project',project,'--config',machine,'--dangerously-reveal-secrets','--json'], { env, sensitive:true }));
    const before = await inspect();
    assert.equal(before.inputs[0].revealed_value, i ? 'second-private-marker' : generated[0]);
    await assert.rejects(setupProject(owner, binary, machine, {requested:true,project,inputs:{unknown:'must-not-escape'},generate:[]}), /Sensitive project setup command failed/);
    assert.equal((await inspect()).private_path, before.private_path);
   } finally { await rm(project, { recursive:true, force:true }); }
  }
 } finally { await cleanup(owner); }
 await assert.rejects(lstat(owner.root), {code:'ENOENT'});
});

test('setup/reveal transport failures and mask failure stay sanitized before daemon attempt', async () => {
 const owner = await createOwner();
 try {
  await mkdir(join(owner.root, 'tools'));
  const binary = join(owner.root, 'tools/backlot');
  const project = owner.root + '-unused-project';
  const options = {requested:true, project, inputs:{credential:'private-stdin-marker'}, generate:['token']};
  // Skipping enrollment has no manifest or CLI requirement.
  await setupProject(owner, '/does-not-exist', '/does-not-exist', {...options, requested:false});
  for (const mode of ['setup-error','reveal-error','reveal-invalid','mask-error']) {
   await writeFile(binary, `#!${process.execPath}
const args=process.argv.slice(2);
if(args.some(arg=>arg.includes('private-stdin-marker')))throw Error('value in argv');
if(args.includes('--from')) {
 let text=''; for await(const part of process.stdin)text+=part;
 if(JSON.parse(text).inputs.credential!=='private-stdin-marker')process.exit(2);
 if(${JSON.stringify(mode)}==='setup-error'){console.error('private-child-marker');process.exit(1);}
 console.log('{}');
} else {
 if(${JSON.stringify(mode)}==='reveal-error'){console.error('private-child-marker');process.exit(1);}
 if(${JSON.stringify(mode)}==='reveal-invalid')console.log('private-child-marker');
 else console.log(JSON.stringify({inputs:[{name:'token',secret:true,revealed_value:'a'.repeat(64)}]}));
}
`, {mode:0o700});
   await assert.rejects(setupProject(owner,binary,'/unused-machine',options,()=>{throw Error('private-child-marker');}), error=>{
    assert.doesNotMatch(error.message,/private-child-marker|private-stdin-marker|aaaa/);
    return /Sensitive project setup command failed|masking failed/.test(error.message);
   });
   await assert.rejects(lstat(join(owner.root,'daemon-attempted')),{code:'ENOENT'});
  }
 } finally { await cleanup(owner); }
 await assert.rejects(lstat(owner.root),{code:'ENOENT'});
});


test('sensitive command forwards cancellation to its retained child and joins', async () => {
 const source = new URL('./runtime.mjs', import.meta.url).href;
 const script = `import {command} from ${JSON.stringify(source)};
 const promise=command(process.execPath,['-e','process.stdout.write("ready");setInterval(()=>{},100)'],{sensitive:true,timeout:1000});
 setTimeout(()=>console.log('ready'),100);
 try {await promise;process.exitCode=2;}catch(error){console.error(error.message);process.exitCode=1;}`;
 const child=spawn(process.execPath,['--input-type=module','-e',script],{stdio:['ignore','pipe','pipe']});
 let stderr='';child.stderr.on('data',data=>{stderr+=data;});
 child.stdout.once('data',()=>child.kill('SIGTERM'));
 const exit=await new Promise((resolve,reject)=>{child.once('error',reject);child.once('close',(code,signal)=>resolve({code,signal}));});
 assert.deepEqual(exit,{code:1,signal:null});
 assert.match(stderr,/Sensitive project setup command failed/);
});
