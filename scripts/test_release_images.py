import contextlib
import gzip
import hashlib
import importlib.machinery
import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest import mock

path = Path(__file__).with_name('release-images')
loader = importlib.machinery.SourceFileLoader('release_images', str(path))
spec = importlib.util.spec_from_loader(loader.name, loader)
release = importlib.util.module_from_spec(spec)
loader.exec_module(release)


class ReleaseImagesTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def archive(self, name='app', arch='amd64', version='v1.2.3', corrupt=False, compressed=False, runtime_config=None, destination=None, ref=None):
        layer = b'actual layer contents\0' + name.encode()
        config_value = {'architecture': arch, 'os': 'linux', 'config': {'Labels': {
            'org.opencontainers.image.version': version,
            'org.opencontainers.image.revision': 'a' * 40}},
            'rootfs': {'type': 'layers', 'diff_ids': ['sha256:' + hashlib.sha256(layer).hexdigest()]}}
        config_value['config'].update(runtime_config or {})
        config = json.dumps(config_value).encode()
        ref = ref or release.local_ref(name, arch)
        manifest = [{'Config': 'config.json', 'Layers': ['layer.tar'], 'RepoTags': [ref]}]
        with tarfile.open(destination or self.root / f'{name}.tar', 'w') as archive:
            for member, data in [('config.json', config), ('layer.tar', gzip.compress(layer) if compressed else layer + (b'x' if corrupt else b'')),
                                 ('manifest.json', json.dumps(manifest).encode())]:
                info = tarfile.TarInfo(member)
                info.size = len(data)
                archive.addfile(info, io.BytesIO(data))
        return hashlib.sha256(config).hexdigest()

    def bundle(self, arch='amd64'):
        for name in ('app', 'storage'):
            self.archive(name, arch=arch)
        bundle = release.prepare_bundle(self.root, 'v1.2.3', arch, 'a' * 40, '42', '1')
        for entry in bundle['images'].values():
            entry['testedImageID'] = 'sha256:' + 'b' * 64
        bundle['sealed'] = True
        release.write_bundle(self.root, bundle)
        return bundle

    def verify(self, **kwargs):
        return release.verify_bundle(self.root, kwargs.get('version', 'v1.2.3'),
                                     kwargs.get('arch', 'amd64'), kwargs.get('commit', 'a' * 40),
                                     '42', '1')

    def test_real_archive_config_and_ordered_rootfs_hashes(self):
        config = self.archive()
        info = release.archive_identity(self.root / 'app.tar', release.local_ref('app', 'amd64'), 'amd64', 'v1.2.3')
        self.assertEqual(info['configDigest'], 'sha256:' + config)
        self.assertEqual(len(info['diffIDs']), 1)
        self.assertEqual(info['platform'], 'linux/amd64')

    def test_containerd_gzip_and_classic_plain_layers_have_same_identity(self):
        self.archive()
        plain = release.archive_identity(self.root / 'app.tar', release.local_ref('app', 'amd64'), 'amd64', 'v1.2.3')
        self.archive(compressed=True)
        compressed = release.archive_identity(self.root / 'app.tar', release.local_ref('app', 'amd64'), 'amd64', 'v1.2.3')
        self.assertEqual(plain, compressed)

    def test_both_archives_are_validated_before_any_publishing_side_effect(self):
        publish = self.root / 'publish'
        for arch in release.ARCHES:
            self.root = publish / arch
            self.root.mkdir(parents=True)
            self.bundle()  # arm64 folder deliberately contains amd64 receipt.
        with mock.patch.object(release, 'execute') as execute:
            with self.assertRaisesRegex(ValueError, 'architecture'):
                release.promote(publish, 'v1.2.3', False, 'Owner/Repo', 'a' * 40, '42', '1')
            execute.assert_not_called()

    def test_promote_loads_and_pushes_tested_images_without_build(self):
        self.promote_fixture()

    def test_registry_config_mismatch_retains_evidence_and_never_changes_channels(self):
        self.promote_fixture(corrupt_registry=True)

    def promote_fixture(self, corrupt_registry=False):
        publish = self.root / 'publish'
        bundles = {}
        for arch in release.ARCHES:
            self.root = publish / arch
            self.root.mkdir(parents=True)
            bundles[arch] = self.bundle(arch)
        source_configs = {}

        def inspect(ref):
            arch = ref.rsplit(':', 1)[1]
            name = 'storage' if 'storage' in ref else 'app'
            return {'Id': 'sha256:' + 'b' * 64, 'Architecture': arch, 'Os': 'linux',
                    'RootFS': {'Layers': bundles[arch]['images'][name]['identity']['diffIDs']},
                    'Config': {'Labels': {'org.opencontainers.image.version': 'v1.2.3'}}}

        def capture(command):
            if command[:3] == ['docker', 'image', 'push']:
                stage = command[3]
                arch = stage.rsplit('-', 1)[1]
                name = 'storage' if '-storage:' in stage else 'app'
                digest = 'sha256:' + hashlib.sha256(stage.encode()).hexdigest()
                source = stage.rsplit(':', 1)[0] + '@' + digest
                source_configs[source] = bundles[arch]['images'][name]['identity']
                return f'{stage}: digest: {digest} size: 123'
            identity = source_configs[command[-1]]
            return json.dumps({'config': {'digest': 'sha256:' + '0' * 64 if corrupt_registry else identity['configDigest']}, 'layers': identity['diffIDs']})

        with contextlib.redirect_stdout(io.StringIO()), mock.patch.object(release, 'execute') as execute, mock.patch.object(release, 'capture', side_effect=capture), mock.patch.object(release, 'inspect_image', side_effect=inspect):
            if corrupt_registry:
                with self.assertRaisesRegex(ValueError, 'registry config'):
                    release.promote(publish, 'v1.2.3', False, 'Owner/Repo', 'a' * 40, '42', '1')
                self.assertFalse(any(call.args[0][:4] == ['docker', 'buildx', 'imagetools', 'create'] for call in execute.call_args_list))
                receipt = json.loads((publish / 'publication.json').read_text())
                self.assertEqual(receipt['state'], 'failed')
                self.assertIn('@sha256:', receipt['images']['app']['architectures']['amd64']['source'])
                return
            release.promote(publish, 'v1.2.3', False, 'Owner/Repo', 'a' * 40, '42', '1')
            commands = [call.args[0] for call in execute.call_args_list]
        self.assertEqual(sum(c[:3] == ['docker', 'image', 'load'] for c in commands), 4)
        self.assertFalse(any(c[:2] == ['docker', 'build'] or c[:3] == ['docker', 'buildx', 'build'] for c in commands))
        manifests = [c for c in commands if c[:4] == ['docker', 'buildx', 'imagetools', 'create']]
        self.assertEqual(len(manifests), 2)
        self.assertTrue(all(sum(x == '--tag' for x in c) == 3 for c in manifests))
        self.assertTrue(all(len([x for x in c if '@sha256:' in x]) == 2 for c in manifests))
        receipt = json.loads((publish / 'publication.json').read_text())
        self.assertEqual(receipt['version'], 'v1.2.3')

    def test_layer_mutation_is_rejected_even_with_repacked_archive(self):
        self.archive(corrupt=True)
        with self.assertRaisesRegex(ValueError, 'layer'):
            release.archive_identity(self.root / 'app.tar', release.local_ref('app', 'amd64'), 'amd64', 'v1.2.3')

    def test_archive_payload_mutation_refused_before_load(self):
        self.bundle()
        with (self.root / 'app.tar').open('ab') as stream:
            stream.write(b'unverified trailing bytes')
        with self.assertRaisesRegex(ValueError, 'archive'):
            self.verify()

    def test_receipt_tampering_is_rejected(self):
        bundle = self.bundle()
        bundle['images']['app']['identity']['configDigest'] = 'sha256:' + 'c' * 64
        release.write_bundle(self.root, bundle)
        with self.assertRaisesRegex(ValueError, 'identity'):
            self.verify()

    def test_wrong_version_arch_commit_run_and_unsealed_refused(self):
        bundle = self.bundle()
        for value in ({'version': 'v1.2.4'}, {'arch': 'arm64'}, {'commit': 'd' * 40}):
            with self.subTest(value=value), self.assertRaises(ValueError):
                self.verify(**value)
        with self.assertRaises(ValueError):
            release.verify_bundle(self.root, 'v1.2.3', 'amd64', 'a' * 40, '99', '1')
        with self.assertRaises(ValueError):
            release.verify_bundle(self.root, 'v1.2.3', 'amd64', 'a' * 40, '42', '2')
        for invalid in (False, 1, 'true'):
            bundle['sealed'] = invalid
            release.write_bundle(self.root, bundle)
            with self.subTest(sealed=invalid), self.assertRaisesRegex(ValueError, 'sealed'):
                self.verify()

    def test_archive_config_wrong_arch_version_or_tag_refused(self):
        self.archive()
        for arch, version, ref in [('arm64', 'v1.2.3', release.local_ref('app', 'amd64')),
                                   ('amd64', 'v1.2.4', release.local_ref('app', 'amd64')),
                                   ('amd64', 'v1.2.3', 'unrelated:tag')]:
            with self.subTest(arch=arch, version=version, ref=ref), self.assertRaises(ValueError):
                release.archive_identity(self.root / 'app.tar', ref, arch, version)

    def test_image_retag_or_mutation_after_test_refused_by_seal(self):
        bundle = self.bundle()
        bundle['sealed'] = False
        release.write_bundle(self.root, bundle)
        changed = {'Id': 'sha256:' + 'e' * 64, 'Architecture': 'amd64', 'Os': 'linux',
                   'RootFS': {'Layers': bundle['images']['app']['identity']['diffIDs']},
                   'Config': {'Labels': {'org.opencontainers.image.version': 'v1.2.3'}}}
        with mock.patch.object(release, 'inspect_image', return_value=changed):
            with self.assertRaisesRegex(ValueError, 'tested image'):
                release.seal_bundle(self.root, 'v1.2.3', 'amd64', 'a' * 40, '42', '1')

    def seal_config_fixture(self, runtime_config=None, alias_changes_during_export=False):
        bundle = self.bundle()
        bundle['sealed'] = False
        release.write_bundle(self.root, bundle)
        exported = []
        commands = []

        def inspect(ref):
            name = 'storage' if 'storage' in ref else 'app'
            image_id = 'c' if alias_changes_during_export and exported and ref == release.check_alias(name) else 'b'
            return {'Id': 'sha256:' + image_id * 64, 'Architecture': 'amd64', 'Os': 'linux',
                    'RootFS': {'Layers': bundle['images'][name]['identity']['diffIDs']},
                    'Config': {'Labels': {'org.opencontainers.image.version': 'v1.2.3'}, **(runtime_config or {})}}

        def execute(command):
            commands.append(command)
            if command[:3] == ['docker', 'image', 'save']:
                name = release.IMAGES[len(exported)]
                exported.append(name)
                output = Path(command[command.index('--output') + 1])
                self.archive(name, destination=output, ref=command[-1], runtime_config=runtime_config)

        with mock.patch.object(release, 'inspect_image', side_effect=inspect), mock.patch.object(release, 'execute', side_effect=execute):
            if runtime_config or alias_changes_during_export:
                with self.assertRaisesRegex(ValueError, 'config|identity'):
                    release.seal_bundle(self.root, 'v1.2.3', 'amd64', 'a' * 40, '42', '1')
                self.assertFalse(json.loads((self.root / 'bundle.json').read_text())['sealed'])
            else:
                release.seal_bundle(self.root, 'v1.2.3', 'amd64', 'a' * 40, '42', '1')
                self.assertTrue(json.loads((self.root / 'bundle.json').read_text())['sealed'])
                self.assertEqual(exported, ['app', 'storage'])
                # Containerd image ID is intentionally distinct from the config hash.
                self.assertNotEqual(bundle['images']['app']['testedImageID'], bundle['images']['app']['identity']['configDigest'])
        self.assertEqual(sum(c[:3] == ['docker', 'image', 'rm'] for c in commands), len(exported))
        self.assertFalse(list(self.root.glob('.seal-*')))

    def test_seal_rejects_changed_runtime_config_with_same_rootfs_version_and_recorded_id(self):
        for changed in ({'Cmd': ['unexpected']}, {'Entrypoint': ['/unexpected']},
                        {'User': '0'}, {'Env': ['MODE=unexpected']}):
            with self.subTest(changed=changed):
                self.seal_config_fixture(changed)

    def test_seal_accepts_exact_exported_config_without_equating_engine_id_to_config_digest(self):
        self.seal_config_fixture()

    def test_seal_rechecks_test_alias_after_immutable_export(self):
        self.seal_config_fixture(alias_changes_during_export=True)

    def test_native_build_refuses_wrong_host_before_creating_archives(self):
        with mock.patch.object(release, 'capture', return_value='arm64'), mock.patch.object(release, 'execute') as execute:
            with self.assertRaisesRegex(ValueError, 'native'):
                release.build(self.root / 'output', 'v1.2.3', 'amd64', 'a' * 40, '42', '1')
            execute.assert_not_called()
        self.assertFalse((self.root / 'output').exists())

    def test_seal_checks_the_alias_used_by_runtime_tests(self):
        bundle = self.bundle()
        bundle['sealed'] = False
        release.write_bundle(self.root, bundle)

        def inspect(ref):
            name = 'storage' if 'storage' in ref else 'app'
            image_id = 'c' if ref == release.check_alias(name) else 'b'
            return {'Id': 'sha256:' + image_id * 64, 'Architecture': 'amd64', 'Os': 'linux',
                    'RootFS': {'Layers': bundle['images'][name]['identity']['diffIDs']},
                    'Config': {'Labels': {'org.opencontainers.image.version': 'v1.2.3'}}}

        with mock.patch.object(release, 'inspect_image', side_effect=inspect):
            with self.assertRaisesRegex(ValueError, 'tested image'):
                release.seal_bundle(self.root, 'v1.2.3', 'amd64', 'a' * 40, '42', '1')
        self.assertFalse(json.loads((self.root / 'bundle.json').read_text())['sealed'])

    def test_release_tags_and_prerelease_channel_exclusion(self):
        self.assertEqual(release.release_tags('v1.2.3', False), ['1.2.3', '1.2', 'latest'])
        self.assertEqual(release.release_tags('v1.2.3-rc.1', True), ['1.2.3-rc.1'])
        for version, prerelease in [('v1.2.3-rc.1', False), ('v1.2.3', True),
                                    ('development', False), ('v01.2.3', False),
                                    ('v1.2.3-01', True), ('v1.2.3+build', False),
                                    ('v1.2.3\nother', False)]:
            with self.subTest(version=version), self.assertRaises(ValueError):
                release.release_tags(version, prerelease)


if __name__ == '__main__':
    unittest.main()
