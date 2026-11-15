import importlib.machinery
import importlib.util
import pathlib
import unittest

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

if __name__=="__main__":unittest.main()
