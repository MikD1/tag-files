"""Disposable historical SQLite/MinIO -> Docker-only migration rehearsal.

The legacy schema is authored from the old EF migration columns. The image
fixture pins upstream MinIO source; media comes from the repository's documented
quadrants.png fixture. No installed library or credentials are used.
"""
import datetime
import hashlib
import hmac
import json
import os
from pathlib import Path
import shutil
import socket
import sqlite3
import subprocess
import tempfile
import time
import unittest
import urllib.parse
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent
MINIO_IMAGE = 'tag-files-legacy-minio:20250422'
MIGRATIONS = (
    '20250622200914_Initial', '20250716221835_AddCollections',
    '20250815135444_AddViewCount', '20250929211941_ChangeVideoDurationType',
    '20250929214538_FixVideoDurationDataConversion',
    '20260322165707_AddCollectionIdToProcessingFile',
)


def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def s3_request(origin, access, secret, method, path, data=b'', content_type='application/octet-stream'):
    """Sign fixture requests with SigV4 without an extra host dependency."""
    now = datetime.datetime.now(datetime.timezone.utc)
    stamp, day = now.strftime('%Y%m%dT%H%M%SZ'), now.strftime('%Y%m%d')
    body_hash = hashlib.sha256(data).hexdigest()
    host = urllib.parse.urlsplit(origin).netloc
    canonical_headers = f'host:{host}\nx-amz-content-sha256:{body_hash}\nx-amz-date:{stamp}\n'
    signed = 'host;x-amz-content-sha256;x-amz-date'
    canonical = '\n'.join((method, path, '', canonical_headers, signed, body_hash))
    scope = f'{day}/us-east-1/s3/aws4_request'
    message = f'AWS4-HMAC-SHA256\n{stamp}\n{scope}\n{hashlib.sha256(canonical.encode()).hexdigest()}'
    key = ('AWS4' + secret).encode()
    for part in (day, 'us-east-1', 's3', 'aws4_request'):
        key = hmac.new(key, part.encode(), hashlib.sha256).digest()
    signature = hmac.new(key, message.encode(), hashlib.sha256).hexdigest()
    request = urllib.request.Request(origin + path, data=data, method=method, headers={
        'x-amz-date': stamp, 'x-amz-content-sha256': body_hash,
        'Content-Type': content_type,
        'Authorization': f'AWS4-HMAC-SHA256 Credential={access}/{scope}, SignedHeaders={signed}, Signature={signature}',
    })
    with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(request, timeout=30) as response:
        return response.read()


def legacy_database(path, missing=False):
    with sqlite3.connect(path) as db:
        db.executescript('''
CREATE TABLE __EFMigrationsHistory(MigrationId TEXT PRIMARY KEY, ProductVersion TEXT NOT NULL);
CREATE TABLE LibraryItems(Id INTEGER PRIMARY KEY AUTOINCREMENT, FileName TEXT NOT NULL, UploadedOn TEXT NOT NULL, FileType TEXT NOT NULL, Description TEXT, CollectionId INTEGER, VideoDuration INTEGER, ThumbnailStatus TEXT NOT NULL, IsFavorite INTEGER NOT NULL, LastView TEXT, ViewCount INTEGER NOT NULL DEFAULT 0);
CREATE TABLE Tags(Id INTEGER PRIMARY KEY AUTOINCREMENT, Name TEXT NOT NULL);
CREATE TABLE Collections(Id INTEGER PRIMARY KEY AUTOINCREMENT, Name TEXT NOT NULL);
CREATE TABLE Categories(Id INTEGER PRIMARY KEY AUTOINCREMENT, Name TEXT NOT NULL, TagQuery TEXT, ItemsType TEXT);
CREATE TABLE LibraryItemTag(LibraryItemId INTEGER NOT NULL, TagsId INTEGER NOT NULL, PRIMARY KEY(LibraryItemId,TagsId));
CREATE TABLE ProcessingFiles(Id INTEGER PRIMARY KEY AUTOINCREMENT, OriginalFileName TEXT NOT NULL, LibraryFileName TEXT NOT NULL, FileType INTEGER NOT NULL, Status INTEGER NOT NULL, CollectionId INTEGER);
INSERT INTO Collections VALUES(7,'旧 collection');
INSERT INTO Tags VALUES(1,'猫'),(2,'猫'),(3,'Cat'),(4,'cat');
INSERT INTO LibraryItems VALUES(11,'quadrants.png','2025-06-22 20:09:14.1234567','Image','original',7,9007199254740993,'NotGenerated',1,NULL,9);
INSERT INTO LibraryItemTag VALUES(11,1),(11,2),(11,3);
INSERT INTO Categories VALUES(3,'case','Cat || cat',NULL);
''')
        db.executemany('INSERT INTO __EFMigrationsHistory VALUES (?,?)', [(value, '9.0.5') for value in MIGRATIONS])
        if missing:
            db.execute("INSERT INTO LibraryItems VALUES(12,'missing.png','2025-06-22 20:09:14','Image',NULL,NULL,NULL,'NotGenerated',0,NULL,0)")


def tree_hashes(root):
    return {str(path.relative_to(root)): ('directory' if path.is_dir() else hashlib.sha256(path.read_bytes()).hexdigest(),
                                         path.stat().st_mode & 0o777)
            for path in sorted(root.rglob('*'))}


@unittest.skipUnless(os.environ.get('TAG_FILES_MIGRATION_INTEGRATION') == '1',
                     'set TAG_FILES_MIGRATION_INTEGRATION=1 for disposable MinIO migration')
class MigrationIntegration(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.app = os.environ.get('TAG_FILES_MIGRATION_APP_IMAGE', 'tag-files-app:local')
        cls.storage = os.environ.get('TAG_FILES_MIGRATION_STORAGE_IMAGE', 'tag-files-storage:local-check')
        cls.migration = os.environ.get('TAG_FILES_MIGRATION_IMAGE', 'tag-files-migrate:local')
        cls.minio = os.environ.get('TAG_FILES_MIGRATION_MINIO_IMAGE', MINIO_IMAGE)
        if subprocess.run(['docker', 'image', 'inspect', cls.minio], capture_output=True).returncode:
            subprocess.run(['docker', 'build', '-f', ROOT / 'tools/migrate/testdata/minio/Dockerfile',
                            '-t', cls.minio, ROOT], check=True, timeout=900)

    def setUp(self):
        # Reserve checks need a real host filesystem, not a small /tmp tmpfs.
        parent = os.environ.get('TAG_FILES_MIGRATION_TEST_ROOT', str(ROOT.parent))
        self.temporary = tempfile.TemporaryDirectory(prefix='tag-files-migration-test-', dir=parent)
        self.work = Path(self.temporary.name)
        self.source = self.work / 'old library'
        self.target = self.work / 'new library'
        self.state = Path(str(self.target) + '.migration')
        self.source_project = 'legacy-migration-' + uuid.uuid4().hex[:10]
        self.project = 'migration-test-' + uuid.uuid4().hex[:10]
        self.port = free_port()
        self.environment = {key: value for key, value in os.environ.items()
                            if not key.startswith(('TAG_FILES_', 'COMPOSE_'))}
        self.extra_containers = []
        self.addCleanup(self.cleanup)

    def command(self, command, cwd=ROOT, success=True, timeout=300):
        result = subprocess.run(list(map(str, command)), cwd=cwd, env=self.environment,
                                capture_output=True, text=True, timeout=timeout)
        if success:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return result

    def compose(self, arguments, target=False):
        return self.command(['docker', 'compose', *arguments], self.target if target else self.source)

    def create_source(self, missing=False, large=False):
        self.source.mkdir(mode=0o700)
        (self.source / 'data/minio_data').mkdir(parents=True, mode=0o700)
        legacy_database(self.source / 'data/tag-files.db', missing)
        self.access, self.secret = uuid.uuid4().hex, uuid.uuid4().hex
        source_port = free_port()
        definition = {'name': self.source_project, 'services': {
            'minio': {'image': self.minio, 'command': ['server', '/data'],
                      'user': f'{os.getuid()}:{os.getgid()}',
                      'environment': {'MINIO_ROOT_USER': self.access, 'MINIO_ROOT_PASSWORD': self.secret},
                      'ports': [f'127.0.0.1:{source_port}:9000'], 'volumes': ['./data/minio_data:/data']},
            'tag-files-service': {'image': self.app, 'entrypoint': ['/bin/sh'], 'command': ['-c', 'exit 0'],
                                 'environment': {'ConnectionStrings__SQLite': 'Data Source=/app/data/tag-files.db',
                                                 'S3__Url': 'minio:9000', 'S3__Username': self.access,
                                                 'S3__Password': self.secret}, 'volumes': ['./data:/app/data']},
        }}
        (self.source / 'compose.yml').write_text(json.dumps(definition))
        self.compose(['up', '-d'])
        origin = f'http://127.0.0.1:{source_port}'
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        deadline = time.monotonic() + 60
        while True:
            try:
                with opener.open(origin + '/minio/health/ready', timeout=2):
                    break
            except OSError:
                self.assertLess(time.monotonic(), deadline, 'fixture MinIO did not start')
                time.sleep(0.2)
        for bucket in ('library', 'temporary', 'thumbnail', 'extra-bucket'):
            s3_request(origin, self.access, self.secret, 'PUT', '/' + bucket)
        self.media = (ROOT / 'app/internal/media/testdata/quadrants.png').read_bytes()
        s3_request(origin, self.access, self.secret, 'PUT', '/library/quadrants.png', self.media, 'image/png')
        s3_request(origin, self.access, self.secret, 'PUT', '/extra-bucket/retained.bin', b'archive orphan bytes')
        if large:
            # A real large object leaves a useful observation window in both
            # streaming archive and multipart copy, without production hooks.
            s3_request(origin, self.access, self.secret, 'PUT', '/extra-bucket/large.bin', b'x' * (64 << 20))
        self.compose(['stop'])
        self.before = tree_hashes(self.source)
        self.env_file = self.work / 'migration.env'
        self.env_file.write_text(f'SOURCE_DIR={self.source}\nTARGET_DIR={self.target}\n')

    def migrate_command(self):
        return ['sh', ROOT / 'scripts/migrate.sh', '--env-file', self.env_file,
                '--version', 'v0.1.2', '--skip-pull', '--app-image', self.app,
                '--storage-image', self.storage, '--migration-image', self.migration,
                '--project', self.project, '--port', str(self.port)]

    def request(self, route, raw=False):
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        with opener.open(f'http://127.0.0.1:{self.port}' + route, timeout=20) as response:
            data = response.read()
        return data if raw else json.loads(data)

    def test_download_workflow_preserves_catalog_bytes_and_verified_rollback(self):
        self.create_source()
        result = self.command(self.migrate_command(), timeout=600)
        self.assertIn(str(self.state), result.stdout)
        self.assertEqual(tree_hashes(self.source), self.before)
        listing = self.request('/api/v1/items')
        self.assertEqual(listing['totalItems'], 1)
        item = listing['items'][0]
        self.assertEqual(item['id'], 11)
        self.assertEqual(self.request(item['contentUrl'], raw=True), self.media)
        tags = self.request('/api/v1/tags')
        self.assertEqual(sorted((row['id'], row['name']) for row in tags), [(1, '猫'), (2, '猫'), (3, 'Cat'), (4, 'cat')])
        with sqlite3.connect(f'file:{self.target}/data/metadata/library.sqlite?mode=ro', uri=True) as db:
            self.assertEqual(db.execute('SELECT duration_ticks FROM items WHERE id=11').fetchone(), (9007199254740993,))
        complete = json.loads((self.state / 'journal/automatic-result.json').read_text())
        self.assertTrue(complete.get('complete'))
        config = json.loads((self.state / 'config.json').read_text())
        self.assertEqual(config['Source']['Buckets'], ['extra-bucket', 'library', 'temporary', 'thumbnail'])
        self.assertTrue((self.state / 'rollback/complete').is_file())
        for path in (self.state / 'config.json', self.state / 'journal/automatic-result.json',
                     self.target / 'data/metadata/library.sqlite'):
            self.assertEqual(path.stat().st_uid, os.getuid(), str(path))
        self.assertNotIn(self.access, result.stdout + result.stderr)
        self.assertNotIn(self.secret, result.stdout + result.stderr)
        rollback_before = tree_hashes(self.state / 'rollback')
        # A completed invocation must be idempotent even with the new app running.
        self.command(self.migrate_command(), timeout=600)
        self.assertEqual(tree_hashes(self.state / 'rollback'), rollback_before)
        self.assertEqual(tree_hashes(self.source), self.before)
        self.compose(['down'], target=True)
        self.compose(['up', '-d', '--wait', '--wait-timeout', '180'], target=True)
        self.assertEqual(self.request(item['contentUrl'], raw=True), self.media)
        self.verify_rollback_restore()

    def verify_rollback_restore(self):
        """Check the rollback really opens as legacy SQLite and native MinIO."""
        rollback = self.state / 'rollback'
        # Find native files without relying on the backup implementation's tree names.
        databases = list(rollback.rglob('tag-files.db'))
        self.assertEqual(len(databases), 1)
        with sqlite3.connect(f'file:{databases[0]}?mode=ro', uri=True) as db:
            self.assertEqual(db.execute('PRAGMA integrity_check').fetchone(), ('ok',))
            self.assertEqual(db.execute('SELECT Id,VideoDuration FROM LibraryItems').fetchall(), [(11, 9007199254740993)])
        roots = [path.parent for path in rollback.rglob('.minio.sys') if path.is_dir()]
        self.assertEqual(len(roots), 1)
        for bucket in ('library', 'temporary', 'thumbnail', 'extra-bucket'):
            self.assertTrue((roots[0] / bucket).is_dir(), bucket)
        restored = self.work / 'rollback-drill'
        shutil.copytree(roots[0], restored)
        name, port = self.project + '-rollback-drill', free_port()
        self.drill_container = name
        env = self.work / 'drill.env'
        env.write_text(f'MINIO_ROOT_USER={self.access}\nMINIO_ROOT_PASSWORD={self.secret}\n')
        env.chmod(0o600)
        self.command(['docker', 'run', '-d', '--name', name, '--user', f'{os.getuid()}:{os.getgid()}',
                      '--env-file', env, '--mount', f'type=bind,src={restored},dst=/data',
                      '-p', f'127.0.0.1:{port}:9000', self.minio, 'server', '/data'])
        deadline = time.monotonic() + 60
        while True:
            try:
                data = s3_request(f'http://127.0.0.1:{port}', self.access, self.secret, 'GET', '/library/quadrants.png')
                break
            except OSError:
                self.assertLess(time.monotonic(), deadline, 'restored legacy MinIO not ready')
                time.sleep(0.2)
        self.assertEqual(data, self.media)


    def test_blocking_missing_media_needs_explicit_decision_then_retries(self):
        self.create_source(missing=True)
        failed = self.command(self.migrate_command(), success=False, timeout=600)
        self.assertNotEqual(failed.returncode, 0)
        self.assertEqual(tree_hashes(self.source), self.before)
        self.assertFalse(self.compose(['ps', '-q', 'app'], target=True).stdout.strip())
        config = json.loads((self.state / 'config.json').read_text())
        archive = self.state / Path(config['Archive']).relative_to('/work')
        manifest = json.loads((archive / 'manifest.json').read_text())
        issues = [issue for issue in manifest['issues'] if issue['code'] == 'missing_primary']
        self.assertEqual(len(issues), 1)
        config['Exceptions'] = [{'issueId': issues[0]['id'], 'decision': 'missing_media',
                                 'reason': 'Fixture has a known absent original', 'owner': 'integration test'}]
        (self.state / 'config.json').write_text(json.dumps(config))
        self.command(self.migrate_command(), timeout=600)
        # The catalog lists ready items; accepted missing media stays visible
        # through direct item lookup and retained metadata instead.
        self.assertEqual(self.request('/api/v1/items')['totalItems'], 1)
        with sqlite3.connect(f'file:{self.target}/data/metadata/library.sqlite?mode=ro', uri=True) as db:
            self.assertEqual(db.execute('SELECT id,state FROM items ORDER BY id').fetchall(), [(11, 'ready'), (12, 'missing')])
        self.assertEqual(tree_hashes(self.source), self.before)

    def test_refuses_running_mount_writer_unrelated_target_and_insufficient_space(self):
        self.create_source()
        writer = self.project + '-unrelated-writer'
        self.extra_containers.append(writer)
        source_alias = self.work / 'source mount alias'
        source_alias.symlink_to(self.source / 'data', target_is_directory=True)
        self.command(['docker', 'run', '-d', '--name', writer, '--network', 'none',
                      '--entrypoint', '/bin/sh', '--mount', f'type=bind,src={source_alias},dst=/alias',
                      self.app, '-c', 'sleep 600'])
        refused = self.command(self.migrate_command(), success=False)
        self.assertNotEqual(refused.returncode, 0)
        self.assertFalse((self.state / 'rollback/complete').exists())
        self.command(['docker', 'rm', '-f', writer])
        self.extra_containers.remove(writer)
        self.target.mkdir(exist_ok=True)
        target_alias = self.work / 'target mount alias'
        target_alias.symlink_to(self.target, target_is_directory=True)
        self.extra_containers.append(writer)
        self.command(['docker', 'run', '-d', '--name', writer, '--network', 'none',
                      '--entrypoint', '/bin/sh', '--mount', f'type=bind,src={target_alias},dst=/alias',
                      self.app, '-c', 'sleep 600'])
        refused = self.command(self.migrate_command(), success=False)
        self.assertNotEqual(refused.returncode, 0)
        self.assertFalse((self.target / 'installation.json').exists())
        self.command(['docker', 'rm', '-f', writer])
        self.extra_containers.remove(writer)
        marker = self.target / 'unrelated-library'
        marker.write_text('must remain unchanged')
        refused = self.command(self.migrate_command(), success=False)
        self.assertNotEqual(refused.returncode, 0)
        self.assertEqual(marker.read_text(), 'must remain unchanged')
        self.assertEqual(tree_hashes(self.source), self.before)
        marker.unlink()
        conflict = self.project + '-migrate-run'
        self.extra_containers.append(conflict)
        self.command(['docker', 'run', '-d', '--name', conflict, '--network', 'none',
                      '--entrypoint', '/bin/sh', self.app, '-c', 'sleep 600'])
        refused = self.command(self.migrate_command(), success=False)
        self.assertNotEqual(refused.returncode, 0)
        info = json.loads(self.command(['docker', 'inspect', conflict]).stdout)[0]
        self.assertTrue(info['State']['Running'], 'unrelated name collision must stay running')
        self.command(['docker', 'rm', '-f', conflict])
        self.extra_containers.remove(conflict)
        # An actual small filesystem tests capacity before a rollback is published.
        small = Path('/dev/shm')
        if small.is_dir() and os.statvfs(small).f_bavail * os.statvfs(small).f_frsize < 10_000_000_000:
            with tempfile.TemporaryDirectory(prefix='tag-files-migration-space-', dir=small) as temporary:
                destination = Path(temporary) / 'new'
                space_env = self.work / 'space.env'
                space_env.write_text(f'SOURCE_DIR={self.source}\nTARGET_DIR={destination}\n')
                command = self.migrate_command()
                command[command.index('--env-file') + 1] = space_env
                try:
                    refused = self.command(command, success=False)
                    self.assertNotEqual(refused.returncode, 0)
                    self.assertRegex(refused.stdout + refused.stderr, '(?i)space|budget|capacity')
                    self.assertFalse(Path(str(destination) + '.migration/rollback/complete').exists())
                finally:
                    if (destination / 'compose.yml').exists():
                        self.command(['docker', 'compose', 'down', '--volumes', '--timeout', '60'], destination)
                    self.command(['docker', 'run', '--rm', '--network', 'none', '--user', '0:0',
                                  '--entrypoint', '/bin/sh', '--mount', f'type=bind,src={temporary},dst=/fixture',
                                  self.app, '-c', 'find /fixture -mindepth 1 -delete'])

    def interrupt_copying(self):
        container = self.project + '-migrate-run'
        self.command(['docker', 'pause', container])
        interrupted = False
        try:
            # The root migration owns its private 0600 journal. Docker copies
            # it for the host user; pausing keeps the database and WAL coherent.
            with tempfile.TemporaryDirectory(prefix='journal-snapshot-', dir=self.work) as snapshot:
                self.command(['docker', 'cp', f'{container}:/work/journal/.', snapshot])
                journal = Path(snapshot) / 'migration.sqlite'
                with sqlite3.connect(f'file:{journal}?mode=ro', uri=True, timeout=0.1) as db:
                    copying = db.execute("SELECT count(*) FROM objects WHERE state='copying'").fetchone()[0] > 0
            if copying:
                self.command(['docker', 'kill', container])
                interrupted = True
            return interrupted
        finally:
            if not interrupted:
                self.command(['docker', 'unpause', container])

    def interrupt_when(self, stage):
        log = self.work / f'interrupt-{stage}.log'
        with log.open('w') as output:
            process = subprocess.Popen(list(map(str, self.migrate_command())), cwd=ROOT,
                                       env=self.environment, stdout=output, stderr=subprocess.STDOUT)
            deadline = time.monotonic() + 240
            try:
                while True:
                    self.assertIsNone(process.poll(), log.read_text())
                    self.assertLess(time.monotonic(), deadline, f'did not observe {stage}: {log.read_text()}')
                    try:
                        record = json.loads((self.state / 'journal/automatic.json').read_text())
                        if stage == 'archiving':
                            archive = self.state / Path(record['archive']).relative_to('/work')
                            observed = record['stage'] == stage and archive.is_dir() and not (archive / 'complete').exists()
                            if observed:
                                self.command(['docker', 'kill', self.project + '-migrate-run'])
                        else:
                            observed = False
                            journal = self.state / 'journal/migration.sqlite'
                            if record['copyStarted'] and journal.exists():
                                observed = self.interrupt_copying()
                        if observed:
                            break
                    except (OSError, ValueError, KeyError, sqlite3.Error):
                        pass
                    time.sleep(0.01)
                self.assertNotEqual(process.wait(timeout=60), 0, log.read_text())
            finally:
                if process.poll() is None:
                    process.terminate()
                    process.wait(timeout=60)

    def test_interrupted_archive_and_copy_resume_same_verified_operation(self):
        self.create_source(large=True)
        self.interrupt_when('archiving')
        initial = json.loads((self.state / 'journal/automatic.json').read_text())
        first_archive = self.state / Path(initial['archive']).relative_to('/work')
        self.assertTrue(first_archive.is_dir())
        self.assertFalse((first_archive / 'complete').exists())
        self.interrupt_when('copying')
        resumed = json.loads((self.state / 'journal/automatic.json').read_text())
        self.assertNotEqual(initial['archive'], resumed['archive'])
        self.assertTrue(first_archive.exists(), 'partial archive must remain available')
        self.assertTrue(resumed['copyStarted'])
        self.command(self.migrate_command(), timeout=600)
        complete = json.loads((self.state / 'journal/automatic-result.json').read_text())
        self.assertTrue(complete['complete'])
        self.assertEqual(self.request('/api/v1/items')['totalItems'], 1)
        self.assertEqual(tree_hashes(self.source), self.before)

    def cleanup(self):
        for directory in (self.target, self.source):
            if (directory / 'compose.yml').exists():
                subprocess.run(['docker', 'compose', 'down', '--volumes', '--timeout', '60'], cwd=directory,
                               env=self.environment, capture_output=True, timeout=150, check=True)
        if hasattr(self, 'drill_container'):
            subprocess.run(['docker', 'rm', '-f', self.drill_container], capture_output=True, check=True)
        for name in self.extra_containers:
            subprocess.run(['docker', 'rm', '-f', name], capture_output=True, check=True)
        result = self._outcome.result
        failed = any(test is self for test, _ in result.failures + result.errors)
        if failed and os.environ.get('TAG_FILES_MIGRATION_KEEP_FAILURES') == '1':
            self.temporary._finalizer.detach()
            print(f'Retained disposable failure fixture: {self.work}', flush=True)
            return
        # Only this test's private directory is writable in the cleanup container.
        if self.work.exists():
            subprocess.run(['docker', 'run', '--rm', '--network', 'none', '--user', '0:0',
                            '--entrypoint', '/bin/sh', '--mount', f'type=bind,src={self.work},dst=/fixture',
                            self.app, '-c', 'find /fixture -mindepth 1 -delete'], capture_output=True, check=True)
        self.temporary.cleanup()


if __name__ == '__main__':
    unittest.main()
