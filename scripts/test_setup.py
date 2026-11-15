"""Exercise the downloadable installer with a disposable Docker CLI boundary."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


APP = "sha256:" + "1" * 64
STORAGE = "sha256:" + "2" * 64
SOURCE = Path(__file__).with_name("setup.sh")
DOCKER = r'''
import json, os, pathlib, sys
args = sys.argv[1:]
config = json.loads(os.environ.get("STUB_CONFIG", "{}"))
with open(os.environ["STUB_LOG"], "a") as log:
    log.write(json.dumps(args) + "\n")
history = [json.loads(line) for line in pathlib.Path(os.environ["STUB_LOG"]).read_text().splitlines()]
conflict = any(call[0] == config.get("race_after") for call in history)
if conflict:
    config.update(config["race_resources"])
if args[:len(config.get("fail", ["impossible"]))] == config.get("fail", ["impossible"]):
    print("injected Docker failure", file=sys.stderr)
    sys.exit(17)
if args == ["compose", "version"]:
    print("Docker Compose version v2.40.0")
elif args[0] == "info":
    print(config.get("host", "linux aarch64"))
elif args[0] == "ps":
    print(config.get("containers", ""))
elif args[:2] == ["volume", "ls"]:
    print(config.get("label_volumes" if "--filter" in args else "volumes", ""))
elif args[0] == "pull":
    print("Pulled " + args[-1])
elif args[:2] == ["image", "inspect"]:
    ref, template = args[-1], args[args.index("--format") + 1]
    app = "app" in ref or ref == "sha256:" + "1" * 64
    if template == "{{.Id}}":
        print(config.get("app_id" if app else "storage_id", "sha256:" + ("1" if app else "2") * 64))
    elif template == "{{.Os}} {{.Architecture}}":
        print(config.get("app_platform" if app else "storage_platform", "linux arm64"))
    else:
        sys.exit(78)
elif args[0] == "run":
    mount = args[args.index("--mount") + 1]
    root = pathlib.Path(mount.removeprefix("type=bind,src=").removesuffix(",dst=/install"))
    if any(root.iterdir()):
        print("setup directory must be empty", file=sys.stderr)
        sys.exit(19)
    options = args[args.index("setup") + 1:]
    values = dict(zip(options[::2], options[1::2]))
    (root / ".env").write_text("COMPOSE_PROJECT_NAME=" + values["--project"] + "\nTAG_FILES_APP_IMAGE=" + values["--app-image"] + "\nTAG_FILES_STORAGE_IMAGE=" + values["--storage-image"] + "\n")
    (root / "compose.yml").write_text("services: {}\n")
elif args[0] == "compose" and "up" in args:
    if conflict:
        pathlib.Path(config["race_sentinel"]).write_text("existing project was changed")
    if any(name in os.environ for name in config.get("forbid_env", [])):
        print("inherited deployment settings reached Compose", file=sys.stderr)
        sys.exit(20)
else:
    print("unexpected Docker call: " + repr(args), file=sys.stderr)
    sys.exit(79)
'''


class SetupTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        docker = self.bin / "docker"
        docker.write_text("#!" + sys.executable + "\n" + DOCKER)
        docker.chmod(0o755)
        (self.bin / "mkdir").symlink_to(shutil.which("mkdir"))
        (self.bin / "tr").symlink_to(shutil.which("tr"))
        self.log = self.root / "docker.jsonl"
        self.home = self.root / "home"
        self.home.mkdir()
        self.directory = self.root / "library's configuration"

    def run_setup(self, *args, config=None, stamped=True, env=None):
        script = self.root / "setup.sh"
        source = SOURCE.read_text()
        if stamped:
            source = source.replace("@TAG_FILES_VERSION@", "v1.2.3").replace("@TAG_FILES_REPOSITORY@", "Example/Tag-Files")
        script.write_text(source)
        environment = dict(os.environ, PATH=str(self.bin), HOME=str(self.home), STUB_LOG=str(self.log), STUB_CONFIG=json.dumps(config or {}))
        environment.update(env or {})
        return subprocess.run(["/bin/sh", str(script), *args], text=True, capture_output=True, env=environment, cwd=self.root)

    def calls(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []

    def assert_safe(self):
        for args in self.calls():
            self.assertFalse(any(word in args for word in ("prune", "down", "rm", "rmi")), args)
            self.assertFalse(args[:2] in (["volume", "create"], ["volume", "rm"]), args)

    def test_release_defaults_pull_pinned_images_and_start_without_host_build_tools(self):
        result = self.run_setup()
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.calls()
        self.assertEqual([row for row in calls if row[0] == "pull"], [["pull", "ghcr.io/example/tag-files-app:1.2.3"], ["pull", "ghcr.io/example/tag-files-storage:1.2.3"]])
        root = self.home / "tag-files"
        bootstrap = next(row for row in calls if row[0] == "run")
        self.assertEqual(bootstrap, ["run", "--rm", "--network", "none", "--user", "0:0", "--entrypoint", "storage-run", "--mount", "type=bind,src=" + str(root) + ",dst=/install", STORAGE, "setup", "--directory", "/install", "--version", "v1.2.3", "--app-image", APP, "--storage-image", STORAGE, "--project", "tag-files", "--port", "8080"])
        self.assertEqual(calls[-1], ["compose", "--project-directory", str(root), "--env-file", str(root / ".env"), "-f", str(root / "compose.yml"), "up", "-d", "--wait", "--wait-timeout", "180"])
        self.assertIn(["image", "inspect", "--format", "{{.Os}} {{.Architecture}}", APP], calls)
        self.assertIn(["image", "inspect", "--format", "{{.Os}} {{.Architecture}}", STORAGE], calls)
        self.assertLess(max(i for i, row in enumerate(calls) if row[:2] == ["image", "inspect"]), calls.index(bootstrap))
        self.assertIn(APP, (root / ".env").read_text())
        self.assertIn("http://localhost:8080", result.stdout)
        self.assertIn("docker compose up -d", result.stdout)
        self.assert_safe()

    def test_relative_directory_starting_with_hyphen_is_treated_as_a_path(self):
        result = self.run_setup("--directory", "-library")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.root / "-library/.env").is_file())
        self.assertIn("type=bind,src=" + str(self.root / "-library") + ",dst=/install", next(row for row in self.calls() if row[0] == "run"))

    def test_inherited_compose_settings_cannot_override_generated_deployment(self):
        inherited = {"COMPOSE_FILE": "/unrelated/compose.yml", "COMPOSE_PROJECT_NAME": "existing-library", "COMPOSE_PROFILES": "custom", "COMPOSE_ENV_FILES": "/unrelated/.env", "TAG_FILES_APP_IMAGE": "app:foreign", "TAG_FILES_PORT": "7777"}
        result = self.run_setup(config={"forbid_env": list(inherited)}, env=inherited)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_source_flags_support_native_preloaded_images_and_quoted_physical_directory(self):
        actual = self.root / "parent with spaces"
        actual.mkdir()
        alias = self.root / "alias"
        alias.symlink_to(actual, target_is_directory=True)
        directory = alias / "library's files"
        result = self.run_setup("--version", "2.3.4", "--directory", str(directory), "--app-image", "app:local", "--storage-image", "storage:local", "--skip-pull", "--project", "test_library", "--port", "9182", stamped=False, config={"host": "linux x86_64", "app_platform": "linux amd64", "storage_platform": "linux amd64"})
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.calls()
        self.assertFalse(any(row[0] == "pull" for row in calls))
        bootstrap = next(row for row in calls if row[0] == "run")
        self.assertIn("type=bind,src=" + str(actual / "library's files") + ",dst=/install", bootstrap)
        self.assertEqual(bootstrap[-6:], ["--storage-image", STORAGE, "--project", "test_library", "--port", "9182"])
        self.assertIn("v2.3.4", bootstrap)
        self.assertIn("http://localhost:9182", result.stdout)
        self.assert_safe()

    def test_existing_project_or_named_volume_is_refused_before_pull_or_directory_creation(self):
        for state in ({"containers": "stopped-container"}, {"label_volumes": "custom-storage-name"}, {"volumes": "unrelated\ntag-files_app_data\nother"}, {"volumes": "tag-files_storage_data"}, {"volumes": "tag-files_scratch"}):
            with self.subTest(state=state):
                self.log.unlink(missing_ok=True)
                result = self.run_setup("--directory", str(self.directory), config=state)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(self.directory.exists())
                self.assertFalse(any(row[0] in ("pull", "run") or "up" in row for row in self.calls()))
                self.assert_safe()

    def test_project_claimed_during_pull_or_setup_is_preserved_without_starting(self):
        existing = self.root / "other-library.sqlite"
        for stage in ("pull", "run"):
            for resource in ({"containers": "newly-created-container"}, {"label_volumes": "custom-storage-volume"}, {"volumes": "tag-files_storage_data"}):
                with self.subTest(stage=stage, resource=resource):
                    existing.write_bytes(b"existing library remains unchanged\x00\xff")
                    self.log.unlink(missing_ok=True)
                    directory = self.root / (stage + "-" + next(iter(resource)))
                    result = self.run_setup("--directory", str(directory), config={"race_after": stage, "race_resources": resource, "race_sentinel": str(existing)})
                    self.assertNotEqual(result.returncode, 0)
                    self.assertEqual(existing.read_bytes(), b"existing library remains unchanged\x00\xff")
                    self.assertFalse(any("up" in row for row in self.calls()))
                    if stage == "pull":
                        self.assertFalse(directory.exists())
                        self.assertFalse(any(row[0] == "run" for row in self.calls()))
                    else:
                        self.assertTrue((directory / ".env").is_file())
                    self.assert_safe()

    def test_foreign_or_unknown_platform_is_refused_before_directory_creation(self):
        for state in ({"host": "windows amd64"}, {"host": "linux unknown"}, {"app_platform": "linux amd64"}, {"storage_platform": "linux amd64"}, {"storage_platform": "windows arm64"}):
            with self.subTest(state=state):
                self.log.unlink(missing_ok=True)
                result = self.run_setup("--directory", str(self.directory), config=state)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("platform", result.stderr.lower())
                self.assertFalse(self.directory.exists())
                self.assertFalse(any(row[0] == "run" for row in self.calls()))
                self.assert_safe()

    def test_existing_directory_contents_remain_untouched(self):
        self.directory.mkdir()
        sentinel = self.directory / ".private-sentinel"
        sentinel.write_bytes(b"keep exactly\x00\xff")
        result = self.run_setup("--directory", str(self.directory))
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(list(self.directory.iterdir()), [sentinel])
        self.assertEqual(sentinel.read_bytes(), b"keep exactly\x00\xff")
        self.assertFalse(any("up" in row for row in self.calls()))
        self.assert_safe()

    def test_docker_or_compose_unavailable_has_clear_failure_without_files(self):
        (self.bin / "docker").unlink()
        result = self.run_setup("--directory", str(self.directory))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Docker", result.stderr)
        self.assertFalse(self.directory.exists())
        docker = self.bin / "docker"
        docker.write_text("#!" + sys.executable + "\n" + DOCKER)
        docker.chmod(0o755)
        result = self.run_setup("--directory", str(self.directory), config={"fail": ["compose", "version"]})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Compose", result.stderr)
        self.assertFalse(self.directory.exists())

    def test_unstamped_source_explains_release_artifact_and_help_needs_no_docker(self):
        result = self.run_setup(stamped=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("release", result.stderr.lower())
        self.assertEqual(self.calls(), [])
        (self.bin / "docker").unlink()
        result = self.run_setup("--help", stamped=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("--directory", result.stdout)

    def test_failures_never_start_after_pull_inspect_or_bootstrap_errors(self):
        for prefix in (["pull"], ["image", "inspect"], ["run"]):
            with self.subTest(prefix=prefix):
                self.log.unlink(missing_ok=True)
                directory = self.root / ("failure-" + prefix[0])
                result = self.run_setup("--directory", str(directory), config={"fail": prefix})
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any("up" in row for row in self.calls()))
                if prefix != ["run"]:
                    self.assertFalse(directory.exists())
                self.assert_safe()

    def test_compose_start_failure_retains_generated_configuration_and_returns_failure(self):
        result = self.run_setup("--directory", str(self.directory), config={"fail": ["compose", "--project-directory"]})
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue((self.directory / ".env").exists())
        self.assertTrue((self.directory / "compose.yml").exists())
        self.assertNotIn("http://localhost", result.stdout)
        self.assert_safe()

    def test_invalid_cli_or_image_id_never_creates_deployment(self):
        for args in (("--version", "latest"), ("--version", "1.2"), ("--version", "01.2.3"), ("--project", "Bad"), ("--port", "0"), ("--port", "65536"), ("--directory",), ("--unknown",)):
            with self.subTest(args=args):
                result = self.run_setup(*args)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse((self.home / "tag-files").exists())
        result = self.run_setup("--directory", str(self.directory), config={"app_id": "app:mutable"})
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.directory.exists())


if __name__ == "__main__":
    unittest.main()
