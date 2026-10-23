import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdir, writeFile, readFile, lstat, symlink, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { spawn } from 'node:child_process';
import * as vm from 'node:vm';
import { fileURLToPath } from 'node:url';
import { captureProjectInputs, mask, setupProject, requireProjectRuntime } from './project.mjs';
import { command, createOwner, cleanup, backlotEnvironment, isInterruption } from './runtime.mjs';

function capture(raw, generated = '', register = () => {}) {
  process.env['INPUT_PROJECT-INPUTS'] = raw;
  process.env['INPUT_PROJECT-GENERATE-INPUTS'] = generated;
  return captureProjectInputs(register);
}

// The branch fixture evaluates the actual setup source and uses the real command
// helper only for a retained native Node child. Platform/dependency/installer
// adapters are bounded stubs; no apt, downloads, filesystem owner or runtime runs.
async function setupCancellationFixture(mode) {
 const sourcePath = fileURLToPath(new URL('./setup.mjs', import.meta.url));
 const source = await readFile(sourcePath, 'utf8');
 const calls = [];
 let joined = false, lsofCalls = 0, owners = 0, runtime = 0;
 const context = vm.createContext({
  process: {platform:'linux',arch:'x64',env:{'INPUT_BACKLOT-REF':'a'.repeat(40),'INPUT_NODE-BINARY':'/fixture/node'},argv:[]},
  performance, console: {log(){},warn(){},error(){}}
 });
 const synthetic = values => new vm.SyntheticModule(Object.keys(values),function(){
  for(const [key,value] of Object.entries(values))this.setExport(key,value);
 },{context});
 const commandStub = async (binary,args) => {
  calls.push([binary,...args].join(' '));
  if(binary==='/fixture/node')return '';
  if(binary==='go')return 'go version go1.27.1 linux/amd64\n';
  if(binary==='lsof') {
   if(mode==='missing') { if(lsofCalls++===0)throw Error('missing fixture dependency'); return ''; }
   const timer=setTimeout(()=>process.kill(process.pid,mode),100);
   try { return await command(process.execPath,['-e','process.stderr.write("private-output-marker");setInterval(()=>{},100)'],{timeout:1000}); }
   finally {clearTimeout(timer);joined=true;}
  }
  if(binary==='sudo')return ''; // installer adapter only; never execute sudo
  throw Error('unexpected fixture executable');
 };
 const module = new vm.SourceTextModule(source,{context,identifier:'file://'+sourcePath,initializeImportMeta(meta){meta.url='file://'+sourcePath;}});
 await module.link(spec=>{
  if(spec==='node:assert/strict')return synthetic({default:assert});
  if(spec==='node:fs/promises')return synthetic({appendFile(){},mkdir(){},readFile:async()=> 'VERSION_ID="24.04"\n',writeFile(){}});
  if(spec==='node:path')return synthetic({join:(...parts)=>parts.join('/'),isAbsolute:value=>value.startsWith('/'),delimiter:':'});
  if(spec==='node:url')return synthetic({fileURLToPath:value=>String(value).replace('file://','')});
  if(spec==='./runtime.mjs')return synthetic({command:commandStub,isInterruption,createOwner(){owners++;throw Error('positive control stops before owner allocation');},prepare(){runtime++;throw Error('runtime must not run');},cleanup(){throw Error('cleanup requires an allocated owner');}});
  if(spec==='./project.mjs')return synthetic({captureProjectInputs(){return {requested:false};},setupProject(){},requireProjectRuntime(){}});
  throw Error('unexpected fixture import');
 });
 await module.evaluate();
 let error;
 try {await module.namespace.setup();}catch(cause){error=cause;}
 assert.equal(runtime,0);
 if(mode==='missing') {
  assert.deepEqual(calls.slice(-3),['sudo apt-get update','sudo apt-get install -y lsof','lsof -v']);
  assert.equal(owners,1); // control reaches the owner adapter but allocates nothing
  assert.match(error?.message??'',/positive control stops/);
 } else {
  assert.ok(joined,'interrupted child was not joined');
  assert.equal(error?.code,'BACKLOT_COMMAND_INTERRUPTED');
  assert.equal(error?.signal,mode);
  assert.equal(error?.stdout,undefined);
  assert.doesNotMatch(error?.message??'',/private-output-marker/);
  assert.ok(!calls.some(call=>call.startsWith('sudo ')),'interruption entered installer fallback');
  assert.equal(owners,0,'interruption reached owner allocation');
 }
 console.log(`PASS actual setup dependency branch: ${mode}; no system installer or owner/runtime allocation`);
}

if(process.argv[2]==='--setup-cancellation-fixture') {
 await setupCancellationFixture(process.argv[3]);
} else {
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
    await assert.rejects(requireProjectRuntime(owner, binary), /state-version-10/);
    await assert.rejects(lstat(join(owner.root, 'project-config')), { code: 'ENOENT' });
  } finally { await cleanup(owner); }
});

test('real CLI adapter isolates projects, masks generation and leaves default registry alone', {
 skip: !process.env.BACKLOT_BINARY && 'requires explicitly built current-head Backlot binary'
}, async () => {
 const owner = await createOwner();
 const projects = [];
 try {
  await mkdir(join(owner.root, 'tools'));
  const binary = join(owner.root, 'tools/backlot');
  await symlink(process.env.BACKLOT_BINARY, binary);
  await requireProjectRuntime(owner, binary);
  const machine = join(owner.root, 'machine.json');
  await writeFile(machine, '{"version":"backlot/v1"}', { mode: 0o600 });
  for (let i = 0; i < 2; i++) {
   const project = owner.root + `-project-${i}`; projects.push(project);
   await mkdir(project);
   // Keep both identities alive through isolation assertions; cleanup is registered
   // before mkdir and remains outside the selected registry root.
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
  }
 } finally {
  // Attempt every owned removal even when one fails, then release the registry.
  const failures = [];
  for (const project of projects) {
   try { await rm(project, { recursive:true, force:true }); }
   catch (error) { failures.push(error); }
  }
  try { await cleanup(owner); } catch (error) { failures.push(error); }
  if (failures.length) throw new AggregateError(failures, 'isolation fixture cleanup failed');
 }
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
 try {await promise;process.exitCode=2;}catch(error){console.error(error.code);process.exitCode=1;}`;
 const child=spawn(process.execPath,['--input-type=module','-e',script],{stdio:['ignore','pipe','pipe']});
 let stderr='';child.stderr.on('data',data=>{stderr+=data;});
 child.stdout.once('data',()=>child.kill('SIGTERM'));
 const exit=await new Promise((resolve,reject)=>{child.once('error',reject);child.once('close',(code,signal)=>resolve({code,signal}));});
 assert.deepEqual(exit,{code:1,signal:null});
 assert.match(stderr,/BACKLOT_COMMAND_INTERRUPTED/);
});


for(const mode of ['SIGINT','SIGTERM','missing'])test(`actual setup dependency branch: ${mode}`,async()=>{
 const result=await command(process.execPath,['--experimental-vm-modules',fileURLToPath(import.meta.url),'--setup-cancellation-fixture',mode],{timeout:5000});
 assert.ok(result.includes(`PASS actual setup dependency branch: ${mode}`));
});
}
