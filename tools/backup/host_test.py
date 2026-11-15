import importlib.machinery
import importlib.util
import contextlib
import io
import json
import pathlib
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

loader = importlib.machinery.SourceFileLoader("cold_backup", str(pathlib.Path(__file__).with_name("cold-backup")))
spec = importlib.util.spec_from_loader(loader.name, loader)
module = importlib.util.module_from_spec(spec)
loader.exec_module(module)

class SafetyTests(unittest.TestCase):
    def fixture(self):
        return [dict(Name="/app", Id="app-id", State=dict(Running=False, Paused=False, Restarting=False), Mounts=[dict(Type="volume", Name="metadata", Destination="/data", RW=True),dict(Type="volume", Name="scratch", Destination="/scratch", RW=True)]),dict(Name="/storage",Id="storage-id",State=dict(Running=False,Paused=False,Restarting=False),Mounts=[dict(Type="volume",Name="storage-data",Destination="/data",RW=True)])]
    def test_rejects_running_unrelated_writer_using_the_same_volume(self):
        rows=self.fixture()
        rows.append(dict(Name="/other",Id="other-id",State=dict(Running=True,Paused=False,Restarting=False),Mounts=[dict(Type="volume",Name="metadata",Destination="/anything",RW=True)]))
        with self.assertRaises(ValueError):module.check_containers(rows,"app","storage","metadata","storage-data")
    def test_rejects_unarchived_external_storage_paths(self):
        rows=self.fixture();rows[1]["Mounts"].append(dict(Type="volume",Name="filer-elsewhere",Destination="/filer-meta",RW=True))
        with self.assertRaises(ValueError):module.check_containers(rows,"app","storage","metadata","storage-data")
    def test_rejects_wrong_volume_pair_and_same_targets(self):
        rows=self.fixture()
        for meta,storage in [("other","storage-data"),("metadata","metadata")]:
            with self.assertRaises(ValueError):module.check_containers(rows,"app","storage",meta,storage)
    def test_rejects_bind_alias_of_a_protected_native_volume(self):
        rows=self.fixture()
        rows[0]["Mounts"][0]["Source"]="/var/lib/docker/volumes/metadata/_data"
        rows.append(dict(Name="/alias-writer",Id="alias-id",State=dict(Running=True),Mounts=[dict(Type="bind",Source="/var/lib/docker/volumes/metadata/_data",Destination="/alias",RW=True)]))
        with self.assertRaises(ValueError):module.check_containers(rows,"app","storage","metadata","storage-data")

    def test_accepts_stopped_owned_layout(self):
        rows=module.check_containers(self.fixture(),"app","storage","metadata","storage-data")
        self.assertEqual([x["Id"] for x in rows],["app-id","storage-id"])

    def bind_fixture(self, root):
        metadata, storage, scratch = [root / name for name in ("metadata", "media", "scratch")]
        for directory in (metadata, storage, scratch):
            directory.mkdir()
        rows = self.fixture()
        for row, directory in zip(rows, (metadata, storage)):
            row["Mounts"][0] = dict(Type="bind", Source=str(directory), Destination="/data", RW=True)
        rows[0]["Mounts"][1] = dict(Type="bind", Source=str(scratch), Destination="/scratch", RW=True)
        return rows, metadata, storage

    def test_accepts_exact_stopped_bind_pair_and_scratch(self):
        with tempfile.TemporaryDirectory() as td:
            rows, metadata, storage = self.bind_fixture(pathlib.Path(td))
            selected = module.check_containers(rows, "app", "storage", metadata, storage)
            self.assertEqual([row["Id"] for row in selected], ["app-id", "storage-id"])

    def test_restore_accepts_fresh_bind_targets_but_protects_both_pairs(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            rows, metadata, storage = self.bind_fixture(root)
            fresh_metadata, fresh_storage = root / "new-metadata", root / "new-media"
            fresh_metadata.mkdir()
            fresh_storage.mkdir()
            selected = module.check_containers(rows, "app", "storage", fresh_metadata, fresh_storage, False)
            self.assertEqual(len(selected), 2)
            with self.assertRaises(ValueError):
                module.check_containers(rows, "app", "storage", fresh_metadata, fresh_storage)
            for source in (metadata, storage, fresh_metadata, fresh_storage, root):
                with self.subTest(source=source):
                    writer = dict(Name="/other", Id="other", State=dict(Running=True),
                                  Mounts=[dict(Type="bind", Source=str(source), Destination="/writer", RW=True)])
                    with self.assertRaises(ValueError):
                        module.check_containers(rows + [writer], "app", "storage", fresh_metadata, fresh_storage, False)

    def test_bind_targets_reject_missing_symlink_and_overlapping_directories(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            rows, metadata, storage = self.bind_fixture(root)
            alias = root / "alias"
            alias.symlink_to(metadata, target_is_directory=True)
            child = metadata / "child"
            child.mkdir()
            for first, second in ((alias, storage), (root / "missing", storage), (metadata, child), (root, storage)):
                with self.subTest(first=first, second=second):
                    with self.assertRaises((ValueError, OSError)):
                        module.check_containers(rows, "app", "storage", first, second, False)

    def test_rejects_named_volume_writer_aliasing_bind_data(self):
        with tempfile.TemporaryDirectory() as td:
            rows, metadata, storage = self.bind_fixture(pathlib.Path(td))
            rows.append(dict(Name="/other", Id="other", State=dict(Running=True), Mounts=[
                dict(Type="volume", Name="alias", Source=str(metadata), Destination="/writer", RW=True)]))
            with self.assertRaises(ValueError):
                module.check_containers(rows, "app", "storage", metadata, storage)

    def test_destination_rejects_native_bind_paths_and_their_ancestors(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            metadata = root / "data"
            metadata.mkdir()
            with patch.object(module, "docker", return_value="/var/lib/docker"):
                for destination in (metadata, metadata / "backup", root):
                    with self.subTest(destination=destination):
                        with self.assertRaises(ValueError):
                            module.safe_destination(destination, [metadata])
                self.assertEqual(module.safe_destination(root / "archives", [metadata]), root / "archives")

    def test_configuration_snapshot_excludes_data_scratch_and_arbitrary_trees(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            source, target = root / "deployment", root / "snapshot"
            source.mkdir()
            (source / "compose.yml").write_text("services: {}\n")
            (source / ".env").write_text("OWNER=1000\n")
            (source / "installation.json").write_text('{"state":"running"}\n')
            for name in ("data", "scratch", "unrelated"):
                (source / name).mkdir()
                (source / name / "unreadable-symlink").symlink_to(root / "missing")
            update = source / "updates" / "fixture"
            (update / "before").mkdir(parents=True)
            (update / "operation.json").write_text('{"state":"complete"}\n')
            (update / "before" / ".env").write_text("OWNER=999\n")
            (update / "native-media").mkdir()
            (update / "native-media" / "alias").symlink_to(root / "missing")
            module.copy_deployment(source, target)
            self.assertEqual(sorted(str(p.relative_to(target)) for p in target.rglob("*") if p.is_file()),
                             [".env", "compose.yml", "installation.json", "updates/fixture/before/.env", "updates/fixture/operation.json"])

    def test_configuration_snapshot_rejects_symlink_config_and_oversized_files(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            source = root / "deployment"
            source.mkdir()
            config = source / "compose.yml"
            config.symlink_to(root / "missing")
            with self.assertRaises(ValueError):
                module.copy_deployment(source, root / "snapshot-link")
            config.unlink()
            with config.open("wb") as stream:
                stream.truncate(4 * 1024 * 1024 + 1)
            with self.assertRaises(ValueError):
                module.copy_deployment(source, root / "snapshot-large")

    def arguments(self, root, command, metadata, storage):
        return SimpleNamespace(command=command, image="backup-tool", app_container="app", storage_container="storage",
                               metadata_directory=str(metadata), storage_directory=str(storage),
                               metadata_volume=None, storage_volume=None, generation="fixture",
                               deployment=str(root / "deployment"), destination=str(root / "archives"),
                               archive=str(root / "archives" / "fixture"), deployment_output=str(root / "restored-config"))

    def fake_docker(self, runs):
        def run(*args, **kwargs):
            if args[:2] == ("image", "inspect"):
                return json.dumps([dict(Id="sha256:" + "1" * 64, Os="linux", Architecture="amd64", RepoDigests=[])])
            if args[0] == "info":
                return "/var/lib/docker"
            if args[:2] == ("volume", "inspect"):
                return json.dumps([dict(Name=args[2], Driver="local", Options=None,
                                        Mountpoint="/var/lib/docker/volumes/" + args[2] + "/_data")])
            if args[0] == "cp":
                pathlib.Path(args[2]).write_text("effective-container-config\n")
                return ""
            if args[0] == "run":
                runs.append(args)
                mounts = [args[index + 1] for index, value in enumerate(args) if value == "--mount"]
                for mount in mounts:
                    fields = dict(field.split("=", 1) for field in mount.split(",") if "=" in field)
                    if fields["dst"] == "/deployment" and "readonly" in mount:
                        snapshot = pathlib.Path(fields["src"])
                        self.assertEqual((snapshot / "secrets" / "s3_access_key").read_text(), "effective-container-config\n")
                        self.assertFalse((snapshot / "data").exists())
                        self.assertFalse((snapshot / "scratch").exists())
                        self.assertEqual((snapshot / "compose.yml").read_text(), "services: {}\n")
                    if fields["dst"] == "/deployment" and "readonly" not in mount and "restore" in args:
                        (pathlib.Path(fields["src"]) / "compose.yml").write_text("restored configuration\n")
                return "{}"
            raise AssertionError("unexpected Docker operation: " + str(args))
        return run

    def test_backup_mounts_bind_data_and_captures_only_configuration(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            deployment = root / "deployment"
            deployment.mkdir()
            rows, metadata, storage = self.bind_fixture(deployment)
            (deployment / "compose.yml").write_text("services: {}\n")
            (deployment / "data").mkdir()
            (deployment / "data" / "live-object").symlink_to(root / "missing")
            for row in rows:
                row["Image"] = "installed-image"
            runs = []
            with patch.object(module, "docker", side_effect=self.fake_docker(runs)), patch.object(module, "all_containers", return_value=rows), contextlib.redirect_stdout(io.StringIO()):
                module.execute(self.arguments(root, "backup", metadata, storage))
            command = runs[-1]
            self.assertIn("type=bind,src=" + str(metadata) + ",dst=/metadata,readonly", command)
            self.assertIn("type=bind,src=" + str(storage) + ",dst=/storage,readonly", command)

    def test_restore_verifies_archive_before_mounting_fresh_bind_targets_writable(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            rows, metadata, storage = self.bind_fixture(root)
            fresh_metadata, fresh_storage = root / "new-metadata", root / "new-storage"
            fresh_metadata.mkdir()
            fresh_storage.mkdir()
            args = self.arguments(root, "restore", fresh_metadata, fresh_storage)
            archive = pathlib.Path(args.archive)
            archive.mkdir(parents=True)
            (archive / "backup.json").write_text(json.dumps({"images": {"app": "old-image"}}))
            runs = []
            with patch.object(module, "docker", side_effect=self.fake_docker(runs)), patch.object(module, "all_containers", return_value=rows), contextlib.redirect_stdout(io.StringIO()):
                module.execute(args)
            self.assertIn("verify", runs[0])
            self.assertFalse(any("dst=/metadata" in argument or "dst=/storage," in argument for argument in runs[0]))
            self.assertIn("restore", runs[1])
            self.assertIn("type=bind,src=" + str(fresh_metadata) + ",dst=/metadata", runs[1])
            self.assertIn("type=bind,src=" + str(fresh_storage) + ",dst=/storage", runs[1])

    def test_restore_output_cannot_overlap_current_or_target_data(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            rows, metadata, storage = self.bind_fixture(root)
            fresh_metadata, fresh_storage = root / "new-metadata", root / "new-storage"
            fresh_metadata.mkdir()
            fresh_storage.mkdir()
            for directory in (metadata, storage, fresh_metadata, fresh_storage):
                args = self.arguments(root, "restore", fresh_metadata, fresh_storage)
                args.deployment_output = str(directory / "restored-config")
                pathlib.Path(args.archive).mkdir(parents=True, exist_ok=True)
                runs = []
                with self.subTest(directory=directory), patch.object(module, "docker", side_effect=self.fake_docker(runs)), patch.object(module, "all_containers", return_value=rows):
                    with self.assertRaises(ValueError):
                        module.execute(args)
                    self.assertFalse(pathlib.Path(args.deployment_output).exists())
                    self.assertEqual(runs, [])

    def test_cli_accepts_directories_and_rejects_ambiguous_targets(self):
        common = ["cold-backup", "backup", "--image", "tool", "--app-container", "app", "--storage-container", "storage",
                  "--deployment", "/deployment", "--destination", "/archives"]
        with patch.object(module.sys, "argv", common + ["--metadata-directory", "/metadata", "--storage-directory", "/storage"]), patch.object(module, "execute") as execute:
            self.assertEqual(module.main(), 0)
            self.assertEqual(execute.call_args.args[0].metadata_directory, "/metadata")
        with patch.object(module.sys, "argv", common + ["--metadata-directory", "/metadata", "--metadata-volume", "metadata", "--storage-volume", "storage"]), contextlib.redirect_stderr(io.StringIO()):
            with self.assertRaises(SystemExit):
                module.main()

    def test_legacy_named_volume_backup_keeps_volume_mounts(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            deployment = root / "deployment"
            deployment.mkdir()
            (deployment / "compose.yml").write_text("services: {}\n")
            rows = self.fixture()
            for row in rows:
                row["Image"] = "installed-image"
            args = self.arguments(root, "backup", root / "unused-meta", root / "unused-media")
            args.metadata_directory = args.storage_directory = None
            args.metadata_volume, args.storage_volume = "metadata", "storage-data"
            runs = []
            with patch.object(module, "docker", side_effect=self.fake_docker(runs)), patch.object(module, "all_containers", return_value=rows), contextlib.redirect_stdout(io.StringIO()):
                module.execute(args)
            self.assertIn("type=volume,src=metadata,dst=/metadata,readonly", runs[-1])
            self.assertIn("type=volume,src=storage-data,dst=/storage,readonly", runs[-1])

    def test_named_targets_still_reject_nonlocal_or_custom_local_volumes(self):
        for driver, options in (("nfs", None), ("local", {"type": "none", "device": "/library", "o": "bind"})):
            with self.subTest(driver=driver, options=options), patch.object(module, "docker", return_value=json.dumps([
                    dict(Driver=driver, Options=options, Mountpoint="/var/lib/docker/volumes/metadata/_data")])):
                with self.assertRaises(ValueError):
                    module.named_volume("metadata")

    def test_failed_archive_verification_never_mounts_restore_targets(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            rows, metadata, storage = self.bind_fixture(root)
            args = self.arguments(root, "restore", metadata, storage)
            pathlib.Path(args.archive).mkdir(parents=True)
            runs = []
            delegate = self.fake_docker(runs)
            def fail_verify(*arguments, **kwargs):
                result = delegate(*arguments, **kwargs)
                if arguments[0] == "run" and "verify" in arguments:
                    raise ValueError("invalid immutable archive")
                return result
            with patch.object(module, "docker", side_effect=fail_verify), patch.object(module, "all_containers", return_value=rows):
                with self.assertRaises(ValueError):
                    module.execute(args)
            self.assertEqual(len(runs), 1)
            self.assertFalse(any("dst=/metadata" in argument or "dst=/storage," in argument for argument in runs[0]))

    def test_restore_scratch_cannot_overlap_current_storage(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            rows, metadata, storage = self.bind_fixture(root)
            fresh_metadata, fresh_storage = root / "fresh-metadata", root / "fresh-storage"
            fresh_metadata.mkdir()
            fresh_storage.mkdir()
            rows[0]["Mounts"][1]["Source"] = str(storage)
            with self.assertRaises(ValueError):
                module.check_containers(rows, "app", "storage", fresh_metadata, fresh_storage, False)

    def test_restore_rejects_a_shared_current_named_volume(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            rows = self.fixture()
            rows[1]["Mounts"][0]["Name"] = "metadata"
            with self.assertRaises(ValueError):
                module.check_containers(rows, "app", "storage", "fresh-metadata", "fresh-storage", False)

    def test_restore_allows_empty_bind_targets_inside_deployment_output(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            rows, _, _ = self.bind_fixture(root)
            output = root / "restored"
            metadata, storage = output / "data" / "metadata", output / "data" / "media"
            metadata.mkdir(parents=True)
            storage.mkdir()
            metadata_inode, storage_inode = metadata.stat().st_ino, storage.stat().st_ino
            args = self.arguments(root, "restore", metadata, storage)
            args.deployment_output = str(output)
            archive = pathlib.Path(args.archive)
            archive.mkdir(parents=True)
            (archive / "backup.json").write_text(json.dumps({"images": {}}))
            runs = []
            with patch.object(module, "docker", side_effect=self.fake_docker(runs)), patch.object(module, "all_containers", return_value=rows), contextlib.redirect_stdout(io.StringIO()):
                module.execute(args)
            self.assertEqual((output / "compose.yml").read_text(), "restored configuration\n")
            self.assertEqual((metadata.stat().st_ino, storage.stat().st_ino), (metadata_inode, storage_inode))
            self.assertEqual(list(root.glob(".restored.restore-*")), [])

    def test_restore_refuses_extra_files_beside_nested_targets(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            rows, _, _ = self.bind_fixture(root)
            output = root / "restored"
            metadata, storage = output / "data" / "metadata", output / "data" / "media"
            metadata.mkdir(parents=True)
            storage.mkdir()
            (output / "data" / "unexpected").write_text("keep")
            args = self.arguments(root, "restore", metadata, storage)
            args.deployment_output = str(output)
            pathlib.Path(args.archive).mkdir(parents=True)
            runs = []
            with patch.object(module, "docker", side_effect=self.fake_docker(runs)), patch.object(module, "all_containers", return_value=rows):
                with self.assertRaises(ValueError):
                    module.execute(args)
            self.assertEqual((output / "data" / "unexpected").read_text(), "keep")
            self.assertEqual(runs, [])

    def test_failed_restore_retains_configuration_staging_evidence(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            rows, metadata, storage = self.bind_fixture(root)
            args = self.arguments(root, "restore", metadata, storage)
            archive = pathlib.Path(args.archive)
            archive.mkdir(parents=True)
            (archive / "backup.json").write_text(json.dumps({"images": {}}))
            runs = []
            delegate = self.fake_docker(runs)
            def fail_restore(*arguments, **kwargs):
                result = delegate(*arguments, **kwargs)
                if arguments[0] == "run" and "restore" in arguments:
                    raise ValueError("partial restore")
                return result
            with patch.object(module, "docker", side_effect=fail_restore), patch.object(module, "all_containers", return_value=rows):
                with self.assertRaises(ValueError):
                    module.execute(args)
            evidence = list(root.glob(".restored-config.restore-*"))
            self.assertEqual(len(evidence), 1)
            self.assertEqual((evidence[0] / "compose.yml").read_text(), "restored configuration\n")

    def test_restore_never_creates_output_inside_immutable_archive(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            rows, metadata, storage = self.bind_fixture(root)
            args = self.arguments(root, "restore", metadata, storage)
            archive = pathlib.Path(args.archive)
            archive.mkdir(parents=True)
            args.deployment_output = str(archive / "new-config")
            with patch.object(module, "docker", side_effect=self.fake_docker([])), patch.object(module, "all_containers", return_value=rows):
                with self.assertRaises(ValueError):
                    module.execute(args)
            self.assertEqual(list(archive.iterdir()), [])

if __name__=="__main__":unittest.main()
