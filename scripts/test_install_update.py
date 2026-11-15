import argparse
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import threading
import select
import subprocess
import sys
import unittest
from unittest.mock import patch

spec=importlib.util.spec_from_file_location("tag_files_deployment",Path(__file__).with_name("deployment.py"))
deployment=importlib.util.module_from_spec(spec)
spec.loader.exec_module(deployment)

APP="sha256:"+"1"*64
STORE="sha256:"+"2"*64
NEXT="sha256:"+"3"*64
TOOL="sha256:"+"4"*64
NEXT2="sha256:"+"5"*64

class Docker:
    def __init__(self):self.running=False;self.fail_pull=False;self.fail_app=False;self.fail_config=False;self.pulled=[];self.rows=[];self.stopped=[];self.host_arch="aarch64";self.image_architectures={}
    def __call__(self,*args):
        if args[0]=="pull":
            if self.fail_pull:raise ValueError("injected unavailable image")
            self.pulled.append(args[1]);return ""
        if args[:2]==("image","inspect"):
            image={"app:v1.0.0":APP,"storage:v1.0.0":STORE,"app:v1.1.0":NEXT,"storage:v1.1.0":STORE,"app:v1.2.0":NEXT2,"storage:v1.2.0":STORE,"tool:v1":TOOL}.get(args[-1],args[-1])
            return json.dumps([{"Id":image,"RepoDigests":[],"Os":"linux","Architecture":self.image_architectures.get(args[-1],"arm64")}])
        if args[0]=="info":
            if args[-1]=="{{json .}}":return json.dumps({"OSType":"linux","Architecture":self.host_arch})
            return "/var/lib/docker"
        if args[0]=="ps":return "existing" if self.running else ""
        if args[:2]==("volume","ls"):return ""
        if args[0]=="inspect":return json.dumps(self.rows)
        if args[0]=="compose":
            if "config" in args:
                if self.fail_config:raise ValueError("injected invalid candidate Compose")
                return ""
            if "ps" in args:return "app-id" if args[-1]=="app" else "storage-id"
            if "stop" in args:self.running=False;self.stopped.append(args[-1]);return ""
            if "up" in args:
                if args[-1]=="app" and self.fail_app:raise ValueError("injected incompatible application image")
                self.running=True;return ""
            if "exec" in args:return ""
        raise AssertionError(args)

class InstallUpdateTests(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory();self.addCleanup(self.tmp.cleanup);self.root=Path(self.tmp.name);self.docker=Docker();self.patch=patch.object(deployment,"docker",self.docker,create=True);self.patch.start();self.addCleanup(self.patch.stop)
    def args(self,**kw):
        base=dict(directory=str(self.root/"deployment"),version="v1.0.0",app_image="app:v1.0.0",storage_image="storage:v1.0.0",no_pull=True,project="fixture",port=4200,start=False,backup_destination=str(self.root/"backups"),backup_tool_image="tool:v1",backup_generation="before-update")
        base.update(kw);return argparse.Namespace(**base)
    def prepare(self):
        args=self.args();deployment.install(args);return Path(args.directory)
    def backup(self,*args,**kw):
        target=self.root/"backups"/"before-update";target.mkdir(parents=True);(target/"complete").write_text("fixture\n");return {"generation":"before-update","libraryId":"library","storageId":"storage"}
    def test_install_prepares_private_distinct_secrets_and_exact_images_without_start(self):
        root=self.prepare();env=(root/".env").read_text();self.assertIn("TAG_FILES_APP_IMAGE="+APP,env);self.assertIn("TAG_FILES_STORAGE_IMAGE="+STORE,env);self.assertFalse(self.docker.running);self.assertEqual(root.stat().st_mode&0o777,0o700);a=(root/"secrets/s3_access_key").read_bytes();s=(root/"secrets/s3_secret_key").read_bytes();self.assertNotEqual(a,s);self.assertGreaterEqual(len(a.strip()),20);self.assertGreaterEqual(len(s.strip()),20);self.assertEqual((root/"secrets/s3_access_key").stat().st_mode&0o777,0o444);self.assertNotIn(a.decode().strip(),(root/"installation.json").read_text())
    def test_install_refuses_existing_data_and_mutable_remote_latest(self):
        args=self.args();root=Path(args.directory);root.mkdir();(root/"sentinel").write_text("keep");with_error=False
        with self.assertRaises(ValueError):deployment.install(args)
        self.assertEqual((root/"sentinel").read_text(),"keep")
        with self.assertRaises(ValueError):deployment.install(self.args(directory=str(self.root/"latest"),no_pull=False,app_image="app:latest"))
    def test_unavailable_update_image_never_stops_current_services(self):
        root=self.prepare();original=(root/".env").read_bytes();self.docker.running=True;self.docker.fail_pull=True
        with self.assertRaises(ValueError):deployment.update(self.args(version="v1.1.0",app_image="app:v1.1.0",storage_image="storage:v1.1.0",no_pull=False))
        self.assertTrue(self.docker.running);self.assertEqual(self.docker.stopped,[]);self.assertEqual((root/".env").read_bytes(),original)

    def test_update_rejects_foreign_platform_for_every_image_before_stopping(self):
        root = self.prepare()
        original = {name: (root / name).read_bytes() for name in (".env", "compose.yml", "installation.json")}
        cases = ({"app:v1.1.0": "amd64", "storage:v1.1.0": "amd64"},
                 {"storage:v1.1.0": "amd64"}, {"tool:v1": "amd64"})
        for architectures in cases:
            with self.subTest(architectures=architectures):
                self.docker.image_architectures = architectures
                self.docker.running = True
                with patch.object(deployment, "cold_backup") as backup:
                    with self.assertRaisesRegex(ValueError, "native Docker platform"):
                        deployment.update(self.args(version="v1.1.0", app_image="app:v1.1.0", storage_image="storage:v1.1.0"))
                    backup.assert_not_called()
                self.assertTrue(self.docker.running)
                self.assertEqual(self.docker.stopped, [])
                self.assertFalse((root / "updates").exists())
                self.assertEqual({name: (root / name).read_bytes() for name in original}, original)

    def test_install_requires_native_platform_even_for_preloaded_images(self):
        for host_arch, image_arch, accepted in (("aarch64", "amd64", False),
                                                ("x86_64", "amd64", True),
                                                ("arm64", "arm64", True),
                                                ("unknown", "arm64", False)):
            with self.subTest(host_arch=host_arch, image_arch=image_arch):
                self.docker.host_arch = host_arch
                self.docker.image_architectures = {"app:v1.0.0": image_arch, "storage:v1.0.0": image_arch}
                args = self.args(directory=str(self.root / (host_arch + "-" + image_arch)))
                if accepted:
                    deployment.install(args)
                    self.assertEqual(json.loads((Path(args.directory) / "installation.json").read_text())["images"]["app"]["Architecture"], image_arch)
                else:
                    with self.assertRaisesRegex(ValueError, "native Docker platform"):
                        deployment.install(args)
                    self.assertFalse(Path(args.directory).exists())
        self.assertEqual(self.docker.stopped, [])
    def test_invalid_candidate_configuration_retains_failure_receipt_without_stopping(self):
        root=self.prepare();self.docker.running=True;self.docker.fail_config=True
        with self.assertRaises(ValueError):deployment.update(self.args(version="v1.1.0",app_image="app:v1.1.0",storage_image="storage:v1.1.0"))
        self.assertTrue(self.docker.running);self.assertEqual(self.docker.stopped,[])
        self.assertEqual(json.loads(next(root.glob("updates/*/operation.json")).read_text())["state"],"failed")

    def test_update_retains_current_config_when_cold_backup_fails(self):
        root=self.prepare();original=(root/".env").read_bytes();self.docker.running=True
        with patch.object(deployment,"cold_backup",side_effect=ValueError("injected archive failure"),create=True):
            with self.assertRaises(ValueError):deployment.update(self.args(version="v1.1.0",app_image="app:v1.1.0",storage_image="storage:v1.1.0"))
        self.assertEqual((root/".env").read_bytes(),original);self.assertFalse(self.docker.running);self.assertEqual(self.docker.stopped[:2],["app","storage"]);self.assertEqual(json.loads(next(root.glob("updates/*/operation.json")).read_text())["state"],"failed")
    def test_failed_new_runtime_keeps_evidence_and_never_restarts_old_images(self):
        root=self.prepare();self.docker.running=True;self.docker.fail_app=True
        with patch.object(deployment,"cold_backup",self.backup,create=True):
            with self.assertRaises(ValueError):deployment.update(self.args(version="v1.1.0",app_image="app:v1.1.0",storage_image="storage:v1.1.0"))
        self.assertIn(NEXT,(root/".env").read_text());self.assertFalse(self.docker.running);receipt=next(root.glob("updates/*/operation.json"));self.assertEqual(json.loads(receipt.read_text())["state"],"failed");self.assertIn(APP,(receipt.parent/"before/.env").read_text());self.assertTrue((self.root/"backups/before-update/complete").exists());self.assertEqual(json.loads((root/"installation.json").read_text())["version"],"v1.0.0")
    def test_successful_update_keeps_secrets_and_refuses_version_downgrade(self):
        root=self.prepare();secret=(root/"secrets/s3_secret_key").read_bytes()
        with patch.object(deployment,"cold_backup",self.backup,create=True):deployment.update(self.args(version="v1.1.0",app_image="app:v1.1.0",storage_image="storage:v1.1.0"))
        self.assertTrue(self.docker.running);self.assertEqual((root/"secrets/s3_secret_key").read_bytes(),secret);self.assertEqual(json.loads((root/"installation.json").read_text())["version"],"v1.1.0")
        with self.assertRaises(ValueError):deployment.update(self.args(version="v1.0.0"))


    def test_replaced_lock_parent_cannot_redirect_update_to_an_unlocked_deployment(self):
        import shutil
        parent = self.root / "lock-parent"
        root = parent / "deployment"
        deployment.install(self.args(directory=str(root)))
        outside = self.root / "outside-parent"
        outside.mkdir(mode=0o700)
        shutil.copytree(root, outside / "deployment")
        original_env = (outside / "deployment/.env").read_bytes()
        acquire = deployment.fcntl.flock

        def replace_parent(fd, operation):
            acquire(fd, operation)
            parent.rename(self.root / "held-parent")
            parent.symlink_to(outside, target_is_directory=True)

        with patch.object(deployment.fcntl, "flock", replace_parent), patch.object(deployment, "cold_backup", self.backup):
            with self.assertRaises((ValueError, OSError)):
                deployment.update(self.args(directory=str(root), version="v1.1.0", app_image="app:v1.1.0", storage_image="storage:v1.1.0"))
        self.assertEqual((outside / "deployment/.env").read_bytes(), original_env)
        self.assertFalse((outside / "deployment/updates").exists())
        self.assertEqual(self.docker.stopped, [])

    def test_busy_update_does_not_read_installed_version_or_state(self):
        root = self.prepare()
        original = (root / "installation.json").read_bytes()
        with deployment.deployment_lock(root):
            (root / "installation.json").write_bytes(b"unfinished metadata")
            try:
                with self.assertRaisesRegex(ValueError, "another install/update"):
                    deployment.update(self.args(version="v1.1.0", app_image="app:v1.1.0", storage_image="storage:v1.1.0"))
            finally:
                (root / "installation.json").write_bytes(original)
        self.assertEqual(self.docker.stopped, [])
        self.assertFalse((root / "updates").exists())

    def test_unusual_deployment_lock_entries_are_refused_without_side_effects(self):
        for kind in ("hardlink", "fifo", "directory", "public"):
            with self.subTest(kind=kind):
                root = self.root / kind
                deployment.install(self.args(directory=str(root)))
                lock = root.parent / ("." + root.name + ".install-update.lock")
                lock.unlink()
                outside = self.root / (kind + "-unrelated")
                if kind == "hardlink":
                    outside.write_bytes(b"keep private host data")
                    outside.chmod(0o600)
                    os.link(outside, lock)
                elif kind == "fifo":
                    os.mkfifo(lock, 0o600)
                elif kind == "directory":
                    lock.mkdir(mode=0o700)
                else:
                    lock.touch(mode=0o600)
                    lock.chmod(0o666)
                with patch.object(deployment, "cold_backup", self.backup):
                    with self.assertRaises((ValueError, OSError)):
                        deployment.update(self.args(directory=str(root), version="v1.1.0", app_image="app:v1.1.0", storage_image="storage:v1.1.0"))
                if kind == "hardlink":
                    self.assertEqual(outside.read_bytes(), b"keep private host data")
                self.assertEqual(json.loads((root / "installation.json").read_text())["version"], "v1.0.0")
                self.assertFalse((root / "updates").exists())
        self.assertEqual(self.docker.stopped, [])

    def test_killed_lock_owner_releases_kernel_lock_but_retains_incomplete_update_fence(self):
        root = self.prepare()
        child_code = """
import importlib.util
from pathlib import Path
import signal
import sys
spec = importlib.util.spec_from_file_location('deployment_child', sys.argv[1])
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
root = Path(sys.argv[2])
with module.deployment_lock(root):
    operation = root / 'updates' / 'interrupted-operation'
    operation.mkdir(parents=True, mode=0o700)
    module.save_json(operation / 'operation.json', {'state': 'prepared', 'phase': 'starting-new-images'})
    print('locked', flush=True)
    signal.pause()
"""
        child = subprocess.Popen([sys.executable, "-c", child_code, str(deployment.REPO / "scripts/deployment.py"), str(root)],
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            self.assertTrue(select.select([child.stdout], [], [], 5)[0], "child did not acquire ownership")
            self.assertEqual(child.stdout.readline(), "locked\n")
            with self.assertRaisesRegex(ValueError, "another install/update"):
                deployment.update(self.args(version="v1.1.0", app_image="app:v1.1.0", storage_image="storage:v1.1.0"))
        finally:
            if child.poll() is None:
                child.kill()
            child.communicate(timeout=5)
        with self.assertRaisesRegex(ValueError, "incomplete earlier update"):
            deployment.update(self.args(version="v1.1.0", app_image="app:v1.1.0", storage_image="storage:v1.1.0"))
        self.assertEqual(json.loads((root / "installation.json").read_text())["version"], "v1.0.0")
        self.assertEqual(self.docker.stopped, [])

    def test_interrupted_first_start_is_not_treated_as_an_unstarted_installation(self):
        args = self.args(start=True)
        with patch.object(deployment, "start_services", side_effect=KeyboardInterrupt("interrupted while Docker starts")):
            with self.assertRaises(KeyboardInterrupt):
                deployment.install(args)
        root = Path(args.directory)
        self.assertEqual(json.loads((root / "installation.json").read_text())["state"], "starting")
        with self.assertRaisesRegex(ValueError, "unsupported or failed installation"):
            deployment.update(self.args(version="v1.1.0", app_image="app:v1.1.0", storage_image="storage:v1.1.0"))
        self.assertEqual(self.docker.stopped, [])

    def test_symlink_deployment_lock_is_refused_without_touching_its_target(self):
        root = self.prepare()
        lock = root.parent / ("." + root.name + ".install-update.lock")
        if lock.exists():
            lock.unlink()
        outside = self.root / "not-a-lock"
        outside.write_bytes(b"retain unrelated host file")
        lock.symlink_to(outside)
        with patch.object(deployment, "cold_backup", self.backup):
            with self.assertRaises((ValueError, OSError)):
                deployment.update(self.args(version="v1.1.0", app_image="app:v1.1.0", storage_image="storage:v1.1.0"))
        self.assertEqual(outside.read_bytes(), b"retain unrelated host file")
        self.assertEqual(json.loads((root / "installation.json").read_text())["version"], "v1.0.0")
        self.assertFalse((root / "updates").exists())
        self.assertEqual(self.docker.stopped, [])

    def test_overlapping_install_cannot_replace_the_active_installers_directory(self):
        first_at_images = threading.Event()
        resume_first = threading.Event()
        first_errors = []
        resolve_image = deployment.image

        def paused_image(ref, no_pull):
            if threading.current_thread().name == "first-installer" and ref == "app:v1.0.0":
                first_at_images.set()
                if not resume_first.wait(5):
                    raise AssertionError("overlap test did not release first installer")
            return resolve_image(ref, no_pull)

        def first_install():
            try:
                deployment.install(self.args())
            except BaseException as error:
                first_errors.append(error)

        with patch.object(deployment, "image", paused_image):
            thread = threading.Thread(target=first_install, name="first-installer")
            thread.start()
            try:
                self.assertTrue(first_at_images.wait(5))
                with self.assertRaises(ValueError):
                    deployment.install(self.args(version="v1.1.0", app_image="app:v1.1.0", storage_image="storage:v1.1.0"))
            finally:
                resume_first.set()
                thread.join(5)
        self.assertFalse(thread.is_alive())
        self.assertEqual(first_errors, [])
        root = Path(self.args().directory)
        self.assertEqual(json.loads((root / "installation.json").read_text())["version"], "v1.0.0")

    def test_overlapping_updates_cannot_restart_older_images_after_newer_completion(self):
        root = self.prepare()
        older_at_images = threading.Event()
        resume_older = threading.Event()
        older_errors = []
        resolve_image = deployment.image

        def paused_image(ref, no_pull):
            if threading.current_thread().name == "older-updater" and ref == "app:v1.1.0":
                older_at_images.set()
                if not resume_older.wait(5):
                    raise AssertionError("overlap test did not release the older updater")
            return resolve_image(ref, no_pull)

        def backup_by_version(root, args, old):
            target = self.root / "backups" / args.version
            target.mkdir(parents=True)
            (target / "complete").write_text("fixture\n")
            return {"generation": args.version, "libraryId": "library", "storageId": "storage"}

        older = self.args(version="v1.1.0", app_image="app:v1.1.0", storage_image="storage:v1.1.0")
        newer = self.args(version="v1.2.0", app_image="app:v1.2.0", storage_image="storage:v1.2.0")

        def run_older():
            try:
                deployment.update(older)
            except BaseException as error:
                older_errors.append(error)

        with patch.object(deployment, "image", paused_image), patch.object(deployment, "cold_backup", backup_by_version):
            thread = threading.Thread(target=run_older, name="older-updater")
            thread.start()
            try:
                self.assertTrue(older_at_images.wait(5), "older update never reached image resolution")
                try:
                    deployment.update(newer)
                    newer_was_blocked = False
                except ValueError:
                    newer_was_blocked = True
            finally:
                resume_older.set()
                thread.join(5)
            self.assertFalse(thread.is_alive(), "older update did not finish")
            self.assertEqual(older_errors, [])
            if newer_was_blocked:
                deployment.update(newer)
            self.assertEqual(json.loads((root / "installation.json").read_text())["version"], "v1.2.0")
            self.assertIn(NEXT2, (root / ".env").read_text())
            with self.assertRaises(ValueError):
                deployment.update(older)

        receipts = [json.loads(p.read_text()) for p in root.glob("updates/*/operation.json")]
        self.assertEqual({r["toVersion"]: r["fromVersion"] for r in receipts},
                         {"v1.1.0": "v1.0.0", "v1.2.0": "v1.1.0"})
        self.assertTrue(all(r["state"] == "complete" for r in receipts))


@unittest.skipUnless(os.environ.get("TAG_FILES_DEPLOYMENT_INTEGRATION")=="1","set TAG_FILES_DEPLOYMENT_INTEGRATION=1 for disposable real Docker install/update")
class RealDockerInstallUpdate(unittest.TestCase):
    def test_installs_and_updates_disposable_pair_preserving_native_backup_and_tags(self):
        import socket
        import subprocess
        import time
        import urllib.request
        import urllib.error
        import uuid
        root=Path(tempfile.mkdtemp(prefix="tag-files-install-drill-"))
        project="tag-files-install-"+uuid.uuid4().hex[:12]
        app=os.environ.get("TAG_FILES_APP_IMAGE","tag-files-app:local")
        storage=os.environ.get("TAG_FILES_STORAGE_IMAGE","tag-files-storage:local-check")
        tool=os.environ.get("TAG_FILES_BACKUP_IMAGE","tag-files-cold-backup:local-drill")
        sock=socket.socket();sock.bind(("127.0.0.1",0));port=sock.getsockname()[1];sock.close()
        path=root/"deployment"
        def invoke(script,args):
            r=subprocess.run([str(deployment.REPO/"scripts"/script),*args],capture_output=True,text=True)
            self.assertEqual(r.returncode,0,r.stderr)
            return json.loads(r.stdout)
        def request(method,route,data=None):
            body=None if data is None else json.dumps(data).encode()
            req=urllib.request.Request("http://127.0.0.1:"+str(port)+route,data=body,method=method,headers={"Content-Type":"application/json","Idempotency-Key":str(uuid.uuid4())})
            try:
                with urllib.request.urlopen(req,timeout=15) as response:return json.load(response)
            except urllib.error.HTTPError as error:
                with error:self.fail("API HTTP %d: %s"%(error.code,error.read().decode()))
        try:
            begin=time.monotonic()
            installed=invoke("install",["--directory",str(path),"--version","v0.1.0","--app-image",app,"--storage-image",storage,"--project",project,"--port",str(port),"--no-pull","--start"])
            self.assertEqual(installed["state"],"running")
            created=request("POST","/api/v1/tags",{"name":"persistent雪"})
            tag_id=created["resource"]["id"]
            secret=(path/"secrets/s3_secret_key").read_bytes()
            updated=invoke("update",["--directory",str(path),"--version","v0.2.0","--app-image",app,"--storage-image",storage,"--no-pull","--backup-tool-image",tool,"--backup-destination",str(root/"backups"),"--backup-generation","before-update"])
            self.assertEqual(updated["state"],"running")
            tags=request("GET","/api/v1/tags")
            self.assertTrue(any(t["id"]==tag_id and t["name"]=="persistent雪" for t in tags))
            self.assertEqual((path/"secrets/s3_secret_key").read_bytes(),secret)
            self.assertTrue((root/"backups/before-update/complete").is_file())
            r=subprocess.run([str(deployment.REPO/"tools/backup/cold-backup"),"verify","--image",tool,"--archive",str(root/"backups/before-update")],capture_output=True,text=True)
            self.assertEqual(r.returncode,0,r.stderr)
            print("Disposable real Docker install + cold-backup/update preserved tag ID/Unicode name/credentials; elapsed %.3fs"%(time.monotonic()-begin))
        finally:
            # Only this random project's containers/volumes are selected.
            if (path/".env").is_file():
                subprocess.run(["docker","compose","--project-directory",str(path),"--env-file",str(path/".env"),"-f",str(path/"compose.yml"),"-p",project,"down","--volumes","--remove-orphans"],capture_output=True,text=True)
            import shutil
            shutil.rmtree(root)

if __name__=="__main__":unittest.main()
