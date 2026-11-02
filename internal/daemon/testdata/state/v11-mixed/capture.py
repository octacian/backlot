import pathlib,tempfile,os,subprocess,time,json,shutil,hashlib
os.umask(0o077)
root=pathlib.Path(tempfile.mkdtemp(prefix='legacy-mixed.',dir='/tmp/backlot-99-delivery'))
for name in ['home','registry','tmp','project','state','raw','fixture']:(root/name).mkdir()
binary='/tmp/backlot-99-delivery/predecessor-v11/backlot'
env={'HOME':str(root/'home'),'XDG_CONFIG_HOME':str(root/'home/config'),'XDG_STATE_HOME':str(root/'home/state'),'BACKLOT_PROJECT_CONFIG_DIR':str(root/'registry'),'TMPDIR':str(root/'tmp'),'PATH':'/usr/bin:/bin:/usr/sbin:/sbin'}
(root/'machine.yaml').write_text('version: backlot/v1\n')
(root/'project/backlot.yaml').write_text('version: backlot/v1\nproject: mixed-v11-fixture\ntools: {shell: /bin/sh}\ncomponents:\n  job:\n    kind: job\n    runtime: native\n    policy: each-start\n    command: {tool: shell, args: [-c, "cat marker; cp marker report"]}\n    artifacts: {report: {path: report}}\nscenes:\n  dev: {lifetime: persistent, components: [job]}\n  destroyed: {lifetime: persistent, components: [job]}\n')
commands=[]
def call(label,args):
 p=subprocess.run([binary,*args,'--state-dir',str(root/'state'),'--json'],cwd=root/'project',env=env,capture_output=True,timeout=60)
 (root/f'raw/{label}.stdout').write_bytes(p.stdout);(root/f'raw/{label}.stderr').write_bytes(p.stderr)
 commands.append({'label':label,'argv':[binary,*args,'--state-dir',str(root/'state'),'--json'],'exit':p.returncode});(root/'commands.json').write_text(json.dumps(commands,indent=2));assert p.returncode==0,(label,p.returncode)
 return json.loads(p.stdout)
process=None
try:
 log=(root/'raw/daemon.log').open('wb');process=subprocess.Popen([binary,'daemon','serve','--state-dir',str(root/'state')],cwd=root/'project',env=env,stdout=log,stderr=subprocess.STDOUT)
 for _ in range(100):
  p=subprocess.run([binary,'daemon','status','--state-dir',str(root/'state'),'--json'],env=env,capture_output=True,timeout=5)
  if p.returncode==0:assert json.loads(p.stdout)['state_version']==11;break
  assert process.poll() is None;time.sleep(.05)
 else:raise AssertionError('readiness unavailable')
 (root/'project/marker').write_text('unrepresented-old-attempt\n')
 first=call('first',['run','dev','--project',str(root/'project'),'--config',str(root/'machine.yaml')]);id=first['instance']['id'];old=first['instance']['execution']['attempt']
 call('first-stop',['stop',id])
 (root/'project/marker').write_text('represented-current-attempt\n')
 second=call('second',['restart',id,'--config',str(root/'machine.yaml')]);current=second['instance']['execution']['attempt'];assert old!=current
 completed=call('second-stop',['stop',id]);assert completed['instance']['execution']['completed_at']
 (root/'project/marker').write_text('destroyed-known-attempt\n')
 erased=call('legacy-destroyed-start',['run','destroyed','--project',str(root/'project'),'--config',str(root/'machine.yaml')]);erased_id=erased['instance']['id']
 call('legacy-destroyed-stop',['stop',erased_id]);legacy=call('legacy-destroy',['destroy',erased_id]);assert legacy['instance']['execution'] and legacy['instance']['status']=='destroyed'
 call('shutdown',['daemon','stop'])
 for _ in range(1000):
  if process.poll() is not None:break
  time.sleep(.05)
 assert process.poll()==0;process.wait(timeout=1);assert not (root/'state/daemon.sock').exists()
 fixture=root/'fixture';shutil.copy2(root/'state/state.db',fixture/'state.db');shutil.copy2(root/'project/backlot.yaml',fixture/'backlot.yaml');shutil.copy2(root/'machine.yaml',fixture/'machine.yaml');shutil.copytree(root/'state/logs',fixture/'logs');shutil.copytree(root/'state/artifacts',fixture/'artifacts')
 records=[json.loads(line) for line in (fixture/'logs'/f'{id}-output.ndjson').read_text().splitlines()];assert {r['attempt'] for r in records}=={old,current}
 (root/'summary.json').write_text(json.dumps({'producer':'6efda8051396286a95903dd2189a4ce20073e1de','binary_sha256':hashlib.sha256(pathlib.Path(binary).read_bytes()).hexdigest(),'state_sha256':hashlib.sha256((fixture/'state.db').read_bytes()).hexdigest(),'instance_id':id,'unrepresented_attempt':old,'current_attempt':current,'logs_attempts':2,'cleanup':{'daemon_exit':process.returncode,'socket_absent':True}},indent=2))
 print(root)
finally:
 if process is not None and process.poll() is None:
  subprocess.run([binary,'daemon','stop','--state-dir',str(root/'state'),'--json'],env=env,capture_output=True,timeout=30)
  for _ in range(1000):
   if process.poll() is not None:break
   time.sleep(.05)
  assert process.poll() is not None;process.wait(timeout=1)
