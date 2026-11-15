"""Exercise the distribution collector with real, isolated Go modules."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("collect-go-notices")


class CollectGoNoticesTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        (self.root / "go.mod").write_text("module example.test/main\n\ngo 1.24\n")
        for name in ("tag-files", "storage-run", "storage-check"):
            path = self.root / "cmd" / name
            path.mkdir(parents=True)
            (path / "main.go").write_text("package main\nfunc main() {}\n")

    def collect(self, *arguments):
        return subprocess.run([str(SCRIPT), str(self.root / "out"), *arguments],
                              cwd=self.root, env={**os.environ, "GOWORK": "off"},
                              capture_output=True, text=True)

    def test_default_target_remains_the_app(self):
        result = self.collect()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.root / "out/go-modules.txt").read_text(), "")

    def test_collects_actual_selected_targets_and_nested_notices_verbatim(self):
        dep = self.root / "dep"
        (dep / "embedded").mkdir(parents=True)
        (dep / "licenses").mkdir()
        (dep / "go.mod").write_text("module example.test/dep\n\ngo 1.24\n")
        (dep / "dep.go").write_text("package dep\n")
        (dep / "LICENSE").write_bytes(b"license original\r\n")
        (dep / "embedded/COPYRIGHT").write_bytes(b"embedded original\n")
        (dep / "licenses/widget.txt").write_bytes(b"embedded widget license\n")
        (self.root / "go.mod").write_text("module example.test/main\n\ngo 1.24\nrequire example.test/dep v0.0.0\nreplace example.test/dep => ./dep\n")
        (self.root / "cmd/storage-check/main.go").write_text('package main\nimport _ "example.test/dep"\nfunc main() {}\n')
        result = self.collect("./cmd/storage-run", "./cmd/storage-check")
        self.assertEqual(result.returncode, 0, result.stderr)
        target = self.root / "out/modules/example.test/dep@v0.0.0"
        self.assertTrue((target / "LICENSE").is_file())
        self.assertEqual((target / "LICENSE").read_bytes(), b"license original\r\n")
        self.assertEqual((target / "embedded/COPYRIGHT").read_bytes(), b"embedded original\n")
        self.assertTrue((target / "licenses/widget.txt").is_file())
        self.assertEqual((target / "licenses/widget.txt").read_bytes(), b"embedded widget license\n")

    def test_accepts_an_explicit_module_inventory_without_assuming_app_targets(self):
        module = self.root / "module"
        module.mkdir()
        (module / "NOTICE").write_bytes(b"upstream notice\n")
        inventory = self.root / "modules.txt"
        inventory.write_text(f"example.test/upstream v1.2.3 {module}\n")
        result = self.collect("--modules", str(inventory))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.root / "out/modules/example.test/upstream@v1.2.3/NOTICE").is_file())
        self.assertEqual((self.root / "out/modules/example.test/upstream@v1.2.3/NOTICE").read_bytes(), b"upstream notice\n")

    def test_rejects_a_selected_dependency_without_distribution_texts(self):
        module = self.root / "module"
        module.mkdir()
        inventory = self.root / "modules.txt"
        inventory.write_text(f"example.test/unlicensed v1.0.0 {module}\n")
        result = self.collect("--modules", str(inventory))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("example.test/unlicensed v1.0.0", (self.root / "out/missing-go-notices.txt").read_text())
        self.assertIn("example.test/unlicensed v1.0.0", result.stderr)

    def test_collects_version_scoped_supplemental_originals_for_nested_modules(self):
        module = self.root / "module"
        module.mkdir()
        inventory = self.root / "modules.txt"
        inventory.write_text(f"example.test/nested/go v1.2.3 {module}\n")
        supplement = self.root / "source-notices/example.test/nested/go@v1.2.3"
        supplement.mkdir(parents=True)
        (supplement / "LICENSE.txt").write_bytes(b"original repository-root license\r\n")
        result = subprocess.run([str(SCRIPT), str(self.root / "out"), "--modules", str(inventory)],
                                env={**os.environ, "COLLECT_GO_NOTICES_EXTRA_DIR": str(self.root / "source-notices")},
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.root / "out/modules/example.test/nested/go@v1.2.3/LICENSE.txt").read_bytes(),
                         b"original repository-root license\r\n")
        inventory.write_text(f"example.test/nested/go v1.2.4 {module}\n")
        result = subprocess.run([str(SCRIPT), str(self.root / "out-other"), "--modules", str(inventory)],
                                env={**os.environ, "COLLECT_GO_NOTICES_EXTRA_DIR": str(self.root / "source-notices")},
                                capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0, "a different version must not inherit a source notice")


if __name__ == "__main__":
    unittest.main()
