#!/usr/bin/env python3
import hashlib, json, os, pathlib, shutil, subprocess, tarfile, time

os.umask(0o077)
root = pathlib.Path('/tmp/backlot-99-delivery/predecessor-v11')
repo = pathlib.Path('/Users/end/orca/workspaces/backlot/feat-start-semantics')
sha = '6efda8051396286a95903dd2189a4ce20073e1de'
for name in ['source', 'project', 'state', 'home', 'registry', 'tmp', 'cache', 'modules', 'gopath', 'exports', 'raw', 'fixture']:
    (root/name).mkdir(mode=0o700, exist_ok=True)
with (root/'source.tar').open('wb') as out:
    subprocess.run(['git', 'archive', sha], cwd=repo, stdout=out, check=True)
with tarfile.open(root/'source.tar') as archive:
    archive.extractall(root/'source', filter='data')
(root/'producer-sha.txt').write_text(sha+'\n')
build_env = {'HOME': str(root/'home'), 'PATH': '/usr/local/go/bin:/usr/bin:/bin:/usr/sbin:/sbin', 'TMPDIR': str(root/'tmp'), 'GOCACHE': str(root/'cache'), 'GOMODCACHE': str(root/'modules'), 'GOPATH': str(root/'gopath'), 'CGO_ENABLED': '1', 'GOARM64': 'v8.0', 'GOTOOLCHAIN': 'local'}
build_cmd = ['/usr/local/go/bin/go', 'build', '-trimpath', '-buildvcs=false', '-o', str(root/'backlot'), './cmd/backlot']
(root/'build-command.json').write_text(json.dumps({'argv': build_cmd, 'cwd': str(root/'source'), 'env': build_env}, indent=2)+'\n')
with (root/'build.log').open('wb') as out:
    subprocess.run(build_cmd, cwd=root/'source', env=build_env, stdout=out, stderr=subprocess.STDOUT, check=True)
with (root/'build-provenance.txt').open('wb') as out:
    for args in [['/usr/local/go/bin/go','version'], ['/usr/local/go/bin/go','version','-m',str(root/'backlot')], ['/usr/local/go/bin/go','env','GOOS','GOARCH','CGO_ENABLED','GOARM64']]:
        subprocess.run(args, cwd=root/'source', env=build_env, stdout=out, stderr=subprocess.STDOUT, check=True)
manifest = '''version: backlot/v1
project: predecessor-v11-fixture
tools:
  shell: /bin/sh
components:
  success:
    kind: job
    runtime: native
    policy: each-start
    command:
      tool: shell
      args: ['-c', 'mkdir -p reports; printf "success-fixture\\n" > reports/success.txt; printf "success-stdout\\n"; printf "success-stderr\\n" >&2; exit 0']
    environment:
      assign:
        FIXTURE_VALUE: {literal: public-v11-fixture-only}
    artifacts:
      report: {path: reports/success.txt}
  failure:
    kind: job
    runtime: native
    policy: each-start
    command:
      tool: shell
      args: ['-c', 'mkdir -p reports; printf "failure-fixture\\n" > reports/failure.txt; printf "failure-stdout\\n"; printf "failure-stderr\\n" >&2; exit 17']
    environment:
      assign:
        FIXTURE_VALUE: {literal: public-v11-fixture-only}
    artifacts:
      report: {path: reports/failure.txt}
scenes:
  prepared:
    lifetime: persistent
    components: [success]
  completed:
    lifetime: disposable
    components: [success]
    terminal_job: success
  failed:
    lifetime: disposable
    components: [failure]
    terminal_job: failure
'''
(root/'project/backlot.yaml').write_text(manifest)
(root/'machine.yaml').write_text('version: backlot/v1\n')
shutil.copy2(root/'project/backlot.yaml', root/'fixture/backlot.yaml')
shutil.copy2(root/'machine.yaml', root/'fixture/machine.yaml')
runtime_env = {'HOME': str(root/'home'), 'XDG_CONFIG_HOME': str(root/'home/config'), 'XDG_STATE_HOME': str(root/'home/state'), 'BACKLOT_PROJECT_CONFIG_DIR': str(root/'registry'), 'PATH': '/usr/bin:/bin:/usr/sbin:/sbin', 'TMPDIR': str(root/'tmp')}
(root/'runtime-env.json').write_text(json.dumps(runtime_env, indent=2)+'\n')
commands = []
def invoke(label, args, expected=0):
    argv = [str(root/'backlot'), *args]
    p = subprocess.run(argv, cwd=root/'project', env=runtime_env, capture_output=True, timeout=90)
    (root/f'raw/{label}.stdout').write_bytes(p.stdout)
    (root/f'raw/{label}.stderr').write_bytes(p.stderr)
    commands.append({'label':label, 'argv': argv, 'cwd':str(root/'project'), 'exit':p.returncode})
    (root/'commands.json').write_text(json.dumps(commands, indent=2)+'\n')
    assert p.returncode == expected, f'{label} exit {p.returncode}, expected {expected}; private raw output retained'
    return json.loads(p.stdout) if p.stdout else None

# Retain the sole child handle and register finally cleanup before starting effects.
daemon = None
joined = False
summary = {}
try:
    argv = [str(root/'backlot'), 'daemon', 'serve', '--state-dir',str(root/'state')]
    daemon_log = (root/'raw/daemon.log').open('wb')
    daemon = subprocess.Popen(argv, cwd=root/'project', env=runtime_env, stdout=daemon_log, stderr=subprocess.STDOUT)
    commands.append({'label':'daemon-serve','argv':argv,'cwd':str(root/'project')})
    ready = False
    for _ in range(100):
        probe = subprocess.run([str(root/'backlot'),'daemon','status','--state-dir',str(root/'state'),'--json'], cwd=root/'project', env=runtime_env, capture_output=True, timeout=5)
        if probe.returncode == 0:
            status = json.loads(probe.stdout)
            (root/'raw/daemon-ready.json').write_bytes(probe.stdout)
            assert status['state_version'] == 11
            ready = True
            break
        if daemon.poll() is not None:
            raise RuntimeError('owned daemon exited before readiness; private log retained')
        time.sleep(0.05)
    assert ready, 'daemon not ready within bounded observation'
    common = ['--project',str(root/'project'),'--config',str(root/'machine.yaml'),'--state-dir',str(root/'state'),'--json']
    prepared = invoke('prepared', ['prepare',*common,'prepared'])
    assert prepared['instance']['status'] == 'prepared'
    summary['prepared'] = {'status':'prepared','instance_id':prepared['instance']['id']}
    for scene, expected in [('completed',0),('failed',17)]:
        result = invoke(scene, ['run',*common,scene], expected)
        i = result['instance']; e = i['execution']
        assert i['status'] == ('succeeded' if expected == 0 else 'failed')
        assert e['terminal_exit_code'] == expected and e['completed_at']
        assert not e.get('collection_failure') and not e.get('cleanup_failure') and not e.get('kept')
        assert e['components'][0]['exit_code'] == expected
        assert e['native_cleanup'] and all(c['confidence'] == 'observed_empty' for c in e['native_cleanup'])
        assert len(e['artifacts']) == 1
        summary[scene] = {'status':i['status'],'instance_id':i['id'],'attempt':e['attempt'],'terminal_exit_code':expected,'native_cleanup':[c['confidence'] for c in e['native_cleanup']],'artifact_count':1}
        logs = invoke(scene+'-logs',['logs','--page','--attempt',e['attempt'],'--state-dir',str(root/'state'),'--json',i['id']])
        assert len(logs['records']) >= 2 and not logs.get('gaps')
        exported = invoke(scene+'-export',['export','--state-dir',str(root/'state'),'--json','--output',str(root/'exports'/scene),i['id']])
        assert exported['published'] and exported['complete'] and exported['result']['attempt'] == e['attempt']
        assert exported['result']['terminal_exit_code'] == expected
        summary[scene]['export_complete'] = True
        inspect = invoke(scene+'-inspect',['inspect','--state-dir',str(root/'state'),'--json',i['id']])
        assert inspect['instance']['execution']['attempt'] == e['attempt']
finally:
    cleanup = {'stop_requested':False,'joined':False,'socket_absent':False}
    if daemon is not None:
        try:
            invoke('daemon-stop',['daemon','stop','--state-dir',str(root/'state'),'--json'])
            cleanup['stop_requested'] = True
        finally:
            try:
                code = daemon.wait(timeout=60)
                joined = True
                cleanup.update(joined=True, daemon_exit=code, socket_absent=not (root/'state/daemon.sock').exists())
                assert code == 0 and cleanup['socket_absent'], 'daemon shutdown not clean'
            except subprocess.TimeoutExpired:
                # The retained, unreaped direct-child handle alone authorizes kill.
                daemon.kill()
                daemon.wait(timeout=10)
                cleanup['forced_kill'] = True
                raise RuntimeError('normal daemon join failed; preserve private diagnostics')
            finally:
                daemon_log.close()
                (root/'cleanup.json').write_text(json.dumps(cleanup, indent=2)+'\n')
assert joined
shutil.copy2(root/'state/state.db', root/'fixture/state.db')
os.chmod(root/'fixture/state.db',0o600)
summary['cleanup'] = cleanup
(root/'summary.json').write_text(json.dumps(summary, indent=2)+'\n')
files = ['source.tar','backlot','fixture/state.db','fixture/backlot.yaml','fixture/machine.yaml','capture.py']
hashes = {name: {'sha256':hashlib.sha256((root/name).read_bytes()).hexdigest(), 'bytes':(root/name).stat().st_size} for name in files}
assert hashes['fixture/state.db']['bytes'] > 0
(root/'hashes.json').write_text(json.dumps(hashes, indent=2)+'\n')
print('Captured closed native v11 predecessor with prepared, successful and failed execution records; daemon joined normally; private details retained under capture root.')
