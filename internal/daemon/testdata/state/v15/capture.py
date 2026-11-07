import pathlib,tempfile,os,subprocess,time,json,shutil,hashlib,sys
os.umask(0o077)
evidence=pathlib.Path('/tmp/backlot-125-128/evidence/v15')
shared='--shared-registration' in sys.argv
binary='/tmp/backlot-125-128/backlot-v15-predecessor'
if shared:evidence=pathlib.Path(tempfile.mkdtemp(prefix='shared-registration.',dir=evidence))
root=pathlib.Path(tempfile.mkdtemp(prefix='bl15.',dir='/tmp'))
for name in ['registry','project','state','raw']:(root/name).mkdir()
env=os.environ.copy();env['BACKLOT_PROJECT_CONFIG_DIR']=str(root/'registry')
(root/'machine.yaml').write_text('version: backlot/v1\n')
(root/'project/backlot.yaml').write_text('version: backlot/v1\nproject: retained-fixture\ntools: {shell: /bin/sh}\ncomponents:\n  job:\n    kind: job\n    runtime: native\n    policy: each-start\n    command: {tool: shell, args: [-c, "exit 0"]}\nscenes:\n  prepared: {lifetime: persistent, components: [job]}\n  completed: {lifetime: persistent, components: [job]}\n')
if shared:
 text=(root/'project/backlot.yaml').read_text()
 text=text.replace('tools: {shell: /bin/sh}', 'shared_services:\n  sql: {manager: mariadb, image: mariadb:11.4, storage: volume, admin: generated, port: 3306, readiness_timeout: 60s}\nresources:\n  db: {kind: allocation, shared_service: sql}\ntools: {shell: /bin/sh}')
 text=text.replace('components: [job]}', 'components: [job], resources: [db]}')
 text=text.replace('    policy: each-start', '    resources: [db]\n    policy: each-start')
 (root/'project/backlot.yaml').write_text(text)
 (root/'machine.yaml').write_text('version: backlot/v1\ndocker: {endpoint: unix:///owned-v12-fixture.sock}\n')
commands=[]
def call(label,args):
 argv=[binary,*args,'--state-dir',str(root/'state'),'--json']
 p=subprocess.run(argv,cwd=root/'project',env=env,capture_output=True,timeout=60)
 (root/f'raw/{label}.stdout').write_bytes(p.stdout);(root/f'raw/{label}.stderr').write_bytes(p.stderr)
 commands.append({'label':label,'argv':argv,'exit':p.returncode});(evidence/'commands.json').write_text(json.dumps(commands,indent=2));assert p.returncode==0,(label,p.returncode)
 return json.loads(p.stdout)
process=None
try:
 log=(root/'raw/daemon.log').open('wb');process=subprocess.Popen([binary,'daemon','serve','--state-dir',str(root/'state')],cwd=root/'project',env=env,stdout=log,stderr=subprocess.STDOUT)
 for _ in range(100):
  p=subprocess.run([binary,'daemon','status','--state-dir',str(root/'state'),'--json'],env=env,capture_output=True,timeout=5)
  if p.returncode==0:assert json.loads(p.stdout)['state_version']==15;break
  assert process.poll() is None;time.sleep(.05)
 else:raise AssertionError('readiness unavailable')
 call('prepare',['prepare','prepared','--project',str(root/'project'),'--config',str(root/'machine.yaml')])
 if shared:call('prepare-completed',['prepare','completed','--project',str(root/'project'),'--config',str(root/'machine.yaml')])
 else:
  completed=call('run',['run','completed','--project',str(root/'project'),'--config',str(root/'machine.yaml')])
  call('stop-consumer',['stop',completed['instance']['id']])
 call('shutdown',['daemon','stop']);process.wait(timeout=30)
 assert process.returncode==0 and not (root/'state/daemon.sock').exists()
 for name,path in [('state.db',root/'state/state.db'),('backlot.yaml',root/'project/backlot.yaml'),('machine.yaml',root/'machine.yaml')]:shutil.copy2(path,evidence/name)
 summary={'producer':'6178482084f076fc71efadf24e41091db1a42cb6+dirty-source:97f982b9132f9134e98878cb1748abc6fe7e5c5de97413c74f76041998c152c1','state_version':15,'producer_binary_sha256':hashlib.sha256(pathlib.Path(binary).read_bytes()).hexdigest(),'capture_script_sha256':hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),'owned_runtime_root':str(root),'cleanup':{'daemon_joined':True,'exit':0,'socket_absent':True},'hashes':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in evidence.iterdir() if p.is_file() and p.name!='summary.json'}}
 (evidence/'summary.json').write_text(json.dumps(summary,indent=2));print(json.dumps({'capture':str(evidence),'cleanup':summary['cleanup'],'state_sha256':summary['hashes']['state.db']}))
finally:
 if process is not None and process.poll() is None:
  subprocess.run([binary,'daemon','stop','--state-dir',str(root/'state'),'--json'],env=env,capture_output=True,timeout=30)
  process.wait(timeout=30)

 if process is not None:
  assert process.poll()==0 and not (root/'state/daemon.sock').exists()
  (evidence/'cleanup.json').write_text(json.dumps({'daemon_joined':True,'exit':process.returncode,'socket_absent':True}))
  shutil.rmtree(root)
  assert not root.exists()
