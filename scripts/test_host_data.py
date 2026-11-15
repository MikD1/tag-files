"""Disposable Docker check for the downloadable installer and host data lifecycle."""
import base64
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import tarfile
import tempfile
import time
import unittest
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent


@unittest.skipUnless(os.environ.get('TAG_FILES_DEPLOYMENT_INTEGRATION') == '1',
                     'set TAG_FILES_DEPLOYMENT_INTEGRATION=1 for disposable Docker host-data checks')
class HostDataIntegration(unittest.TestCase):
    def test_installer_recreate_backup_update_and_restore_preserve_host_library(self):
        app = os.environ.get('TAG_FILES_APP_IMAGE', 'tag-files-app:local')
        storage = os.environ.get('TAG_FILES_STORAGE_IMAGE', 'tag-files-storage:local-check')
        tool = os.environ.get('TAG_FILES_BACKUP_IMAGE', 'tag-files-cold-backup:local-drill')
        project = 'tag-files-host-data-' + uuid.uuid4().hex[:10]
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        environment = {k: v for k, v in os.environ.items() if not k.startswith(('COMPOSE_', 'TAG_FILES_'))}
        environment['PYTHONDONTWRITEBYTECODE'] = '1'
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        origin = f'http://127.0.0.1:{port}'

        def request(route, data=None, method=None, headers=None, raw=False):
            headers = dict(headers or {})
            if isinstance(data, dict):
                data = json.dumps(data).encode()
                headers.update({'Content-Type':'application/json', 'Idempotency-Key':str(uuid.uuid4())})
            with opener.open(urllib.request.Request(origin + route, data=data, method=method, headers=headers), timeout=20) as response:
                content = response.read()
                return content if raw else json.loads(content)

        def run(command, cwd=ROOT):
            result = subprocess.run(list(map(str, command)), cwd=cwd, env=environment,
                                    text=True, capture_output=True, timeout=240)
            self.assertEqual(result.returncode, 0, result.stderr)
            return result.stdout

        def ready():
            deadline = time.monotonic() + 90
            while True:
                try:
                    request('/health/ready', raw=True)
                    return
                except (OSError, urllib.error.URLError):
                    self.assertLess(time.monotonic(), deadline, 'application never became ready')
                    time.sleep(0.25)

        # TMPDIR can select a host-backed disk for archive capacity checks.
        with tempfile.TemporaryDirectory(prefix='tag-files-host-data-') as temporary:
            work = Path(temporary)
            deployment = work / 'library with spaces'
            restored = work / 'restored library'
            current = deployment
            try:
                run(['sh', ROOT / 'scripts/setup.sh', '--version', 'v0.1.2', '--skip-pull',
                     '--app-image', app, '--storage-image', storage, '--project', project,
                     '--port', port, '--directory', deployment])
                for name in ('data', 'data/metadata', 'data/media', 'scratch'):
                    info = (deployment / name).stat()
                    self.assertEqual(info.st_mode & 0o777, 0o700)
                    self.assertEqual(info.st_uid, os.getuid())
                self.assertTrue((deployment / 'data/metadata/library.sqlite').is_file())
                for role, source in (('app', 'data/metadata'), ('storage', 'data/media')):
                    cid = run(['docker', 'compose', 'ps', '-q', role], deployment).strip()
                    info = json.loads(run(['docker', 'inspect', cid]))[0]
                    native = [m for m in info['Mounts'] if m['Destination'] == '/data']
                    self.assertEqual([(m['Type'], m['Source']) for m in native], [('bind', str(deployment / source))])
                    self.assertEqual(info['Config']['User'], f'{os.getuid()}:{os.getgid()}')
                request('/api/v1/tags', {'name':'persistent雪'})
                request('/api/v1/collections', {'name':'Host data'})
                fixture = (ROOT / 'app/internal/media/testdata/quadrants.png').read_bytes()
                created = request('/api/v1/upload-sessions', {'files':[{
                    'clientFileId':str(uuid.uuid4()), 'filename':'host-data.png', 'size':len(fixture)}]})
                session = created['sessions'][0]
                upload = '/api/v1/upload-sessions/' + session['id']
                part = request(upload + '/parts/1', fixture, 'PUT', {
                    'Content-Type':'application/octet-stream', 'X-Upload-Generation':str(session['generation']),
                    'X-Part-SHA256':base64.b64encode(hashlib.sha256(fixture).digest()).decode()})
                request(upload + '/complete', {'generation':session['generation'],
                        'parts':[{'partNumber':1, 'etag':part['etag']}]})
                deadline = time.monotonic() + 90
                while True:
                    items = request('/api/v1/items')
                    if items['totalItems'] == 1 and items['items'][0].get('posterUrl'):
                        break
                    self.assertLess(time.monotonic(), deadline, 'media and poster did not publish')
                    time.sleep(0.25)
                before = {route:request(route) for route in ('/api/v1/items','/api/v1/tags','/api/v1/collections')}
                content_url = items['items'][0]['contentUrl']
                self.assertEqual(request(content_url, raw=True), fixture)

                # Removing all containers/networks must leave the host library intact.
                run(['docker', 'compose', 'down', '--volumes'], deployment)
                self.assertTrue((deployment / 'data/metadata/library.sqlite').is_file())
                self.assertTrue(any((deployment / 'data/media').iterdir()))
                run(['docker', 'compose', 'up', '-d'], deployment)
                ready()
                for route, expected in before.items():
                    self.assertEqual(request(route), expected)
                self.assertEqual(request(content_url, raw=True), fixture)

                # Unrelated host files must not be traversed as configuration.
                (deployment / 'unrelated').mkdir()
                (deployment / 'unrelated/excluded').symlink_to('/not/a/backup/input')
                run([ROOT / 'scripts/update', '--directory', deployment, '--version', 'v0.1.3',
                     '--app-image', app, '--storage-image', storage, '--no-pull',
                     '--backup-tool-image', tool, '--backup-destination', work / 'backups',
                     '--backup-generation', 'before-update'])
                ready()
                archive = work / 'backups/before-update'
                self.assertTrue((archive / 'complete').is_file())
                with tarfile.open(archive / 'deployment.tar') as config_archive:
                    names = config_archive.getnames()
                    self.assertFalse(any(n.startswith(('data/', 'scratch/', 'unrelated/')) for n in names))
                for route, expected in before.items():
                    self.assertEqual(request(route), expected)
                self.assertEqual(request(content_url, raw=True), fixture)
                run(['docker', 'compose', 'stop'], deployment)
                restored.mkdir(mode=0o700)
                (restored / 'data').mkdir(mode=0o700)
                for name in ('metadata', 'media'):
                    (restored / 'data' / name).mkdir(mode=0o700)
                run([ROOT / 'tools/backup/cold-backup', 'restore', '--image', tool,
                     '--app-container', project + '-app-1', '--storage-container', project + '-storage-1',
                     '--metadata-directory', restored / 'data/metadata', '--storage-directory', restored / 'data/media',
                     '--archive', archive, '--deployment-output', restored])
                (restored / 'scratch').mkdir(mode=0o700)
                for directory in (deployment / 'data', restored / 'data'):
                    for path in directory.rglob('*'):
                        self.assertEqual(path.stat().st_uid, os.getuid(), str(path))
                current = restored
                run(['docker', 'compose', 'up', '-d', '--wait', '--wait-timeout', '180'], restored)
                for route, expected in before.items():
                    self.assertEqual(request(route), expected)
                self.assertEqual(request(content_url, raw=True), fixture)
                print('Host data: installer, container recreation, full media bytes, update/cold backup, and nested restore passed')
            finally:
                if (current / 'compose.yml').is_file():
                    subprocess.run(['docker', 'compose', 'down', '--volumes', '--timeout', '120'],
                                   cwd=current, env=environment, capture_output=True, timeout=240, check=True)


if __name__ == '__main__':
    unittest.main()
