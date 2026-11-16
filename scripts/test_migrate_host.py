"""POSIX downloadable migration orchestration against a disposable Docker boundary."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('migrate.sh')
APP = 'sha256:' + '1' * 64
STORAGE = 'sha256:' + '2' * 64
MIGRATE = 'sha256:' + '3' * 64
MINIO = 'sha256:' + '4' * 64
DOCKER = r'''
import json, os, pathlib, sys
args=sys.argv[1:]
with open(os.environ['STUB_LOG'],'a') as log:log.write(json.dumps(args)+'\n')
config=json.loads(os.environ.get('STUB_CONFIG','{}'))
if args==['compose','version']:print('v2')
elif args[0]=='info':print('linux amd64')
elif args[:2]==['image','inspect']:
 ref=args[-1]
 if '--format' in args:
  template=args[args.index('--format')+1]
  print('linux amd64' if template=='{{.Os}} {{.Architecture}}' else (ref if ref.startswith('sha256:') else 'sha256:'+('1' if 'app' in ref else '2' if 'storage' in ref else '3')*64))
 else:print('[{}]')
elif args[0]=='pull':pass
elif args[0]=='ps':print('source-app\nsource-minio' if '--filter' not in args else '')
elif args[0]=='inspect':print('[]')
elif args[:2]==['volume','ls']:pass
elif args[:2]==['network','inspect']:sys.exit(1)
elif args[:2]==['network','create']:print('network-id')
elif args[:2]==['network','connect']:pass
elif args[:2]==['network','rm']:pass
elif args[0] in ('stop','rm'):pass
elif args[0]=='run':
 mounts=[args[i+1] for i,x in enumerate(args) if x=='--mount']
 def mounted(dest):
  for m in mounts:
   fields=dict(piece.split('=',1) for piece in m.split(',') if '=' in piece)
   if fields.get('dst')==dest:return pathlib.Path(fields['src'])
  return None
 work=mounted('/work');target=mounted('/install')
 if 'mount-sources' in args:
  (work/'inspection-plan.json').write_text('{}');(work/'inspection-mounts.env').write_text('')
 elif 'canonical-mounts' in args:print('{}')
 elif 'discover' in args:
  if config.get('discover_fail'):sys.exit(9)
  values={args[i]:args[i+1] for i in range(len(args)-1) if args[i].startswith('--')}
  project=values.get('--project') or 'tag-files-auto'
  (work/'host.env').write_text('DB_MOUNT_KIND=bind\nDB_MOUNT_SOURCE='+os.environ['STUB_SOURCE']+'/data\nSTORAGE_MOUNT_KIND=bind\nSTORAGE_MOUNT_SOURCE='+os.environ['STUB_SOURCE']+'/data/minio_data\nSOURCE_DB_RELATIVE=tag-files.db\nSOURCE_STORAGE_IMAGE=sha256:'+'4'*64+'\nSOURCE_APP_ID=source-app\nSOURCE_STORAGE_ID=source-minio\nPROJECT='+project+'\nOPERATION_ID='+'5'*32+'\n')
  (work/'state.json').write_text('{}')
 elif 'prepare' in args:
  (work/'rollback').mkdir(exist_ok=True);(work/'rollback/complete').write_text('verified');(work/'source-minio').mkdir(exist_ok=True);(work/'source-minio.env').write_text('private\n');(work/'source-secrets').mkdir(exist_ok=True)
 elif 'setup-stage' in args:
  name='setup-attempt-'+os.urandom(16).hex();(work/name).mkdir();(work/'setup-stage.env').write_text('SETUP_STAGE='+name+'\n')
 elif 'publish-target' in args:
  stage=args[args.index('--stage')+1];destination=pathlib.Path(os.environ['STUB_TARGET'])
  if destination.exists():destination.rmdir()
  (work/stage).rename(destination)
 elif 'setup' in args:
  for d in ('data/metadata','data/media','scratch','secrets'):(target/d).mkdir(parents=True,exist_ok=True)
  (target/'.env').write_text('COMPOSE_PROJECT_NAME=fixture\n');(target/'compose.yml').write_text('services: {}\n');(target/'installation.json').write_text('{}')
 elif 'configure-target' in args:
  (work/'target-secrets').mkdir(exist_ok=True);(work/'config.json').write_text('{}')
 elif 'auto' in args:
  if config.get('auto_conflict'):sys.exit(9)
  if config.get('auto_fail'):print('injected verification blocker',file=sys.stderr);sys.exit(7)
  (work/'journal').mkdir(exist_ok=True);(work/'journal/automatic-result.json').write_text('{"complete":true}')
 elif 'finalize' in args:(work/'verified.json').write_text('{"complete":true}')
 elif 'completed' in args:
  if not (work/'verified.json').is_file():sys.exit(1)
 elif 'verify-source' in args or 'wait-source' in args:pass
 elif '--detach' in args or '-d' in args:print('temporary-minio-id')
 else:print('unexpected run '+repr(args),file=sys.stderr);sys.exit(8)
elif args[0]=='compose':
 if 'ps' in args:pass
 elif 'up' in args or 'stop' in args:pass
 else:sys.exit(10)
else:print('unexpected '+repr(args),file=sys.stderr);sys.exit(11)
'''

class MigrationHostTests(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory();self.addCleanup(self.tmp.cleanup);self.root=Path(self.tmp.name)
  self.bin=self.root/'bin';self.bin.mkdir();docker=self.bin/'docker';docker.write_text('#!'+sys.executable+'\n'+DOCKER);docker.chmod(0o755)
  self.source=self.root/"old library's data";self.source.mkdir();(self.source/'compose.yml').write_text('services: {}\n');self.target=self.root/'new library'
  self.env=self.root/'migration.env';self.env.write_text('SOURCE_DIR='+str(self.source)+'\nTARGET_DIR='+str(self.target)+'\n')
  self.log=self.root/'docker.jsonl'
 def run_script(self,*args,config=None):
  env=dict(os.environ,PATH=str(self.bin)+os.pathsep+os.environ['PATH'],STUB_LOG=str(self.log),STUB_SOURCE=str(self.source),STUB_TARGET=str(self.target),STUB_CONFIG=json.dumps(config or {}))
  return subprocess.run(['sh',str(SCRIPT),'--version','1.2.3','--app-image','app:local','--storage-image','storage:local','--migration-image','migrate:local','--skip-pull','--project','fixture','--env-file',str(self.env),*args],capture_output=True,text=True,env=env)
 def calls(self):return [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []
 def test_one_command_prepares_private_source_clone_and_starts_app_after_verify(self):
  r=self.run_script();self.assertEqual(r.returncode,0,r.stderr);calls=self.calls()
  auto=next(i for i,c in enumerate(calls) if 'auto' in c);finalize=next(i for i,c in enumerate(calls) if 'finalize' in c)
  app=next(i for i,c in enumerate(calls) if c[0]=='compose' and 'up' in c and c[-1]=='app')
  self.assertLess(auto,finalize);self.assertLess(finalize,app)
  minio=next(c for c in calls if '--env-file' in c and c[0]=='run')
  self.assertIn('fixture-source-minio',minio);self.assertNotIn('-p',minio);self.assertIn('--read-only',minio);self.assertIn('--tmpfs',minio)
  self.assertTrue(any('source-minio,dst=/data' in x for x in minio));self.assertIn(MINIO,minio)
  auto_call=calls[auto];self.assertIn('fixture-migrate-run',auto_call);self.assertIn('--sig-proxy=true',auto_call)
  self.assertTrue(any('dst=/source,readonly' in x for x in auto_call));self.assertIn(str(self.target)+'.migration',r.stdout)
  self.assertFalse(any('down' in c or 'prune' in c for c in calls))
 def test_failed_auto_keeps_app_stopped_and_retry_uses_same_state(self):
  r=self.run_script(config={'auto_fail':True});self.assertNotEqual(r.returncode,0)
  self.assertFalse(any(c[0]=='compose' and 'up' in c and c[-1]=='app' for c in self.calls()))
  self.assertTrue((Path(str(self.target)+'.migration')/'rollback/complete').is_file())
  self.log.unlink();r=self.run_script();self.assertEqual(r.returncode,0,r.stderr)
  self.assertFalse(any('setup' in c for c in self.calls()))
 def test_env_is_parsed_as_literal_data_and_unknown_keys_are_rejected(self):
  sentinel=self.root/'executed';self.env.write_text('SOURCE_DIR=$(touch '+str(sentinel)+')\nTARGET_DIR='+str(self.target)+'\n')
  r=self.run_script();self.assertNotEqual(r.returncode,0);self.assertFalse(sentinel.exists());self.assertFalse(self.target.exists())
  self.env.write_text('SOURCE_DIR='+str(self.source)+'\nTARGET_DIR='+str(self.target)+'\nOTHER=bad\n');r=self.run_script();self.assertNotEqual(r.returncode,0);self.assertFalse(self.target.exists())
 def test_unrelated_target_is_untouched_before_docker(self):
  self.target.mkdir();sentinel=self.target/'keep';sentinel.write_text('old library');r=self.run_script();self.assertNotEqual(r.returncode,0);self.assertEqual(sentinel.read_text(),'old library');self.assertEqual(self.calls(),[])
 def test_existing_completed_run_skips_copy_and_preserves_running_app(self):
  r=self.run_script();self.assertEqual(r.returncode,0,r.stderr);self.log.unlink();r=self.run_script();self.assertEqual(r.returncode,0,r.stderr)
  self.assertFalse(any('auto' in c or 'prepare' in c for c in self.calls()))


 def test_failed_discovery_is_retriable_after_source_is_stopped(self):
  r=self.run_script(config={'discover_fail':True});self.assertNotEqual(r.returncode,0)
  self.log.unlink();r=self.run_script();self.assertEqual(r.returncode,0,r.stderr)
 def test_unrelated_container_name_conflict_is_never_stopped(self):
  r=self.run_script(config={'auto_conflict':True});self.assertNotEqual(r.returncode,0)
  self.assertFalse(any(c[0]=='stop' and c[-1]=='fixture-migrate-run' for c in self.calls()))

if __name__=='__main__':unittest.main()
