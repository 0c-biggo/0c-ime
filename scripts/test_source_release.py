"""Exercise tagged source assembly, offline use and corrupted release rejection."""

import hashlib
import io
import json
from pathlib import Path
import subprocess
import shutil
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import source_release as release


def fixture_dictionary(directory):
    directory.mkdir()
    source = {"ID": "fixture", "Name": "Fixture", "URL": "https://example.invalid",
              "Version": "1", "License": "MIT", "Attribution": "Test authors", "Distributable": True}
    manifest = {"format": 2, "files": {}, "options": {"Public": True}, "sources": [source]}
    for name in ("lexicon.bin", "lm.bin"):
        (directory / name).write_bytes(name.encode())
        manifest["files"][name] = {"size": len(name), "sha256": release.digest(directory / name)}
    (directory / "report.txt").write_text("Test fixture")
    (directory / "NOTICE.txt").write_text("== Fixture（fixture）==\n授權：MIT\n版本：1\n網址：https://example.invalid\n姓名標示：Test authors\n\nPermission to use test fixture.\n")
    (directory / "manifest.json").write_bytes(release.json_bytes(manifest))
    english = directory / "english"
    english.mkdir()
    for name in ("words.bin", "NOTICE.txt"):
        (english / name).write_text("Test English fixture")
    english_manifest = {"format": 1, "words": 1, "ranking": "fixture", "sources": [
        {key: "fixture" for key in ("name", "version", "url", "sha256", "license", "attribution")}],
        "files": {name: {"size": (english / name).stat().st_size, "sha256": release.digest(english / name)}
                  for name in ("words.bin", "NOTICE.txt")}}
    (english / "manifest.json").write_bytes(release.json_bytes(english_manifest))
    return {"schema": 1, "files": {p.relative_to(directory).as_posix():
            {"size": p.stat().st_size, "sha256": release.digest(p)}
            for p in directory.rglob("*") if p.is_file()}}


class SourceReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.work = Path(self.temp.name)
        self.repo = self.work / "repo"
        self.repo.mkdir()
        self.cache = self.work / "cache"
        self.cache.mkdir()
        self.git("init", "-q")
        self.git("config", "user.name", "Source Release Test")
        self.git("config", "user.email", "test@example.invalid")
        self.models = {}
        for role in ("select", "punct"):
            content = {"model.onnx": (role + " model fixture").encode(),
                       "vocab.json": release.json_bytes({"characters": ["test"], "bases": [],
                                                         "dictionary_sha256": "a" * 64}),
                       "LICENSE": b"MIT test fixture", "README.md": b"Test model card"}
            manifest = {"schema": "zuin-sequence-runtime-v1" if role == "select" else "zuin-punct-runtime-v1",
                        "files": {name: hashlib.sha256(content[name]).hexdigest()
                                  for name in ("model.onnx", "vocab.json")}}
            if role == "select":
                manifest["dictionary_sha256"] = "a" * 64
            content["manifest.json"] = release.json_bytes(manifest)
            files = {}
            for name, value in content.items():
                sha = hashlib.sha256(value).hexdigest()
                (self.cache / sha).write_bytes(value)
                files[name] = {"sha256": sha, "size": len(value)}
            self.models[role] = {"repository": "example/" + role, "revision": "b" * 40,
                                 "license": "MIT", "version": "fixture", "files": files}
        (self.repo / "models").mkdir()
        (self.repo / release.LOCK).write_bytes(release.json_bytes({"schema": 1, "models": self.models}))
        self.data = self.work / "dictionary"
        dictionary_lock = fixture_dictionary(self.data)
        (self.repo / "data").mkdir()
        (self.repo / release.release_dictionary.LOCK).write_bytes(release.json_bytes(dictionary_lock))
        dictionary = release.digest(self.data / "manifest.json")
        from pair_models import metadata, PAIR_LOCK
        pair = {"schema": 1, "models_lock_sha256": release.digest(self.repo / release.LOCK),
                "dictionary_manifest_sha256": dictionary, "packages": {}}
        for role, model in self.models.items():
            original = {name: json.loads((self.cache / model["files"][name]["sha256"]).read_bytes())
                        for name in ("manifest.json", "vocab.json")}
            pair["packages"][role] = {name: {"sha256": hashlib.sha256(content).hexdigest(), "size": len(content)}
                                      for name, content in metadata(original["manifest.json"], original["vocab.json"], dictionary).items()}
        (self.repo / PAIR_LOCK).write_bytes(release.json_bytes(pair))
        (self.repo / "README.md").write_text("Tagged source\n")
        (self.repo / release.RULES).write_text("AGENTS.md\ndocs/\ndata/research/\ndata/lm/bigram/\n")
        (self.repo / "tool.sh").write_text("#!/bin/sh\nexit 0\n")
        (self.repo / "tool.sh").chmod(0o755)
        (self.repo / "alias").symlink_to("README.md")
        (self.repo / ".gitattributes").write_text("private/ export-ignore\n")
        (self.repo / "private").mkdir()
        (self.repo / "private/note.txt").write_text("not for release")
        self.commit_tag("v0.2.0")

    def git(self, *args):
        return release.git(self.repo, *args)

    def commit_tag(self, tag):
        self.git("add", ".")
        self.git("commit", "-qm", "test snapshot")
        self.git("tag", tag)

    def build(self, name="output", tag="v0.2.0"):
        with patch.object(release.urllib.request, "urlopen", side_effect=AssertionError("network used")):
            return release.build(self.repo, tag, self.work / name, self.cache, offline=True, data_dir=self.data)

    def test_tag_source_models_offline_and_reproducible(self):
        (self.repo / "README.md").write_text("Dirty working tree must not leak\n")
        (self.repo / release.LOCK).write_text("invalid working tree lock")
        (self.repo / "untracked.txt").write_text("local note")
        first = self.build()
        record = release.verify(first)
        self.assertEqual(record["commit"], self.git("rev-parse", "v0.2.0"))
        self.assertEqual(record["files"]["tool.sh"]["mode"], 0o755)
        self.assertEqual(record["files"]["alias"], {"link": "README.md"})
        self.assertNotIn("private/note.txt", record["files"])
        self.assertNotIn("untracked.txt", record["files"])
        self.assertIn("models/select/model.onnx", record["files"])
        self.assertIn("models/punct/LICENSE", record["files"])
        self.assertIn("assets/data/lm.bin", record["files"])
        self.assertFalse(any("data/lm/bigram/" in p for p in record["files"]))
        with tarfile.open(first) as archive:
            self.assertEqual(archive.extractfile("ime-oss-0.2.0/README.md").read(), b"Tagged source\n")
        other = self.build("repeat")
        self.assertEqual(release.digest(first), release.digest(other))
        self.assertEqual((first.parent / "SHA256SUMS").read_text(),
                         f"{release.digest(first)}  {first.name}\n")

    def test_dictionary_reuse_and_corruption(self):
        tarball = self.build()
        output = self.work / "reused"
        release.extract_dictionary(tarball, output, self.repo)
        for name in release.release_dictionary.FILES:
            self.assertEqual((output / name).read_bytes(), (self.data / name).read_bytes())
        (self.data / "lm.bin").write_text("changed data")
        with self.assertRaisesRegex(ValueError, "雜湊或大小不符"):
            self.build("corrupt")
        self.assertFalse((self.work / "corrupt").exists())

    def test_pairing_metadata_cannot_be_changed_without_validation(self):
        path = self.repo / "models/pairing.lock.json"
        pair = json.loads(path.read_bytes())
        pair["dictionary_manifest_sha256"] = "e" * 64
        path.write_bytes(release.json_bytes(pair))
        self.commit_tag("v0.2.1")
        with self.assertRaisesRegex(ValueError, "paired metadata does not match"):
            self.build(tag="v0.2.1")

    def test_pair_export_preserves_weights_tokens_and_rejects_other_data(self):
        import pair_models
        data = self.work / "data"
        data.mkdir()
        source = {"ID": "fixture", "Name": "Fixture", "URL": "https://example.invalid",
                  "Version": "1", "License": "MIT", "Attribution": "Test authors", "Distributable": True}
        manifest = {"format": 2, "files": {}, "options": {"Public": True}, "sources": [source]}
        for name in ("lexicon.bin", "lm.bin"):
            (data / name).write_bytes(name.encode())
            manifest["files"][name] = {"size": len(name), "sha256": release.digest(data / name)}
        (data / "report.txt").write_text("Test fixture")
        (data / "NOTICE.txt").write_text("== Fixture（fixture）==\n授權：MIT\n版本：1\n網址：https://example.invalid\n姓名標示：Test authors\n\nPermission to use test fixture.\n")
        (data / "manifest.json").write_bytes(release.json_bytes(manifest))
        dictionary = release.digest(data / "manifest.json")
        pair = json.loads((self.repo / pair_models.PAIR_LOCK).read_bytes())
        pair["dictionary_manifest_sha256"] = dictionary
        for role, model in self.models.items():
            folder = self.repo / "models" / role
            folder.mkdir()
            for name, entry in model["files"].items():
                shutil.copyfile(self.cache / entry["sha256"], folder / name)
            metadata = pair_models.metadata(json.loads((folder / "manifest.json").read_bytes()),
                                            json.loads((folder / "vocab.json").read_bytes()), dictionary)
            pair["packages"][role] = {name: {"size": len(content), "sha256": hashlib.sha256(content).hexdigest()}
                                      for name, content in metadata.items()}
        (self.repo / pair_models.PAIR_LOCK).write_bytes(release.json_bytes(pair))
        output = self.work / "paired"
        pair_models.export(self.repo, data, self.repo / "models", output)
        for role in self.models:
            original = self.repo / "models" / role
            self.assertEqual((output / role / "model.onnx").read_bytes(), (original / "model.onnx").read_bytes())
            vocab = json.loads((output / role / "vocab.json").read_bytes())
            self.assertEqual(vocab.pop("dictionary_sha256"), dictionary)
            old = json.loads((original / "vocab.json").read_bytes())
            old.pop("dictionary_sha256")
            self.assertEqual(vocab, old)
        manifest["runtime"] = {"version": 2}
        (data / "manifest.json").write_bytes(release.json_bytes(manifest))
        with self.assertRaisesRegex(ValueError, "not the validated public model pair"):
            pair_models.export(self.repo, data, self.repo / "models", self.work / "wrong")
        self.assertFalse((self.work / "wrong").exists())
        manifest["options"]["Public"] = False
        (data / "manifest.json").write_bytes(release.json_bytes(manifest))
        with self.assertRaisesRegex(ValueError, "-public"):
            pair_models.export(self.repo, data, self.repo / "models", self.work / "private")

    def test_private_dictionary_is_rejected_even_with_updated_lock(self):
        path = self.data / "manifest.json"
        manifest = json.loads(path.read_bytes())
        manifest["options"]["Public"] = False
        path.write_bytes(release.json_bytes(manifest))
        lock_path = self.repo / release.release_dictionary.LOCK
        lock = json.loads(lock_path.read_bytes())
        lock["files"]["manifest.json"] = {"size": path.stat().st_size, "sha256": release.digest(path)}
        lock_path.write_bytes(release.json_bytes(lock))
        self.commit_tag("v0.2.1")
        with self.assertRaisesRegex(ValueError, "-public"):
            self.build(tag="v0.2.1")

    def test_bigram_source_must_not_be_shipped(self):
        folder = self.repo / "data/lm/bigram"
        folder.mkdir(parents=True)
        (folder / "test.tsv").write_text("unwanted large input")
        self.commit_tag("v0.2.1")
        with self.assertRaisesRegex(ValueError, "data/lm/bigram"):
            self.build(tag="v0.2.1")

    def test_corrupt_and_missing_cache_leave_no_release(self):
        entry = self.models["select"]["files"]["model.onnx"]
        target = self.cache / entry["sha256"]
        original = target.read_bytes()
        target.write_bytes(b"x" * len(original))
        with self.assertRaisesRegex(ValueError, "mismatch"):
            self.build()
        self.assertFalse((self.work / "output").exists())
        target.unlink()
        with self.assertRaisesRegex(ValueError, "offline cache miss"):
            self.build()

    def test_private_tracked_files_rejected_even_if_export_ignored(self):
        folder = self.repo / "docs/acceptance"
        folder.mkdir(parents=True)
        (folder / "report.txt").write_text("private test evidence")
        (self.repo / ".gitattributes").write_text("docs/acceptance export-ignore\n")
        self.commit_tag("v0.2.1")
        # Neither a working-tree rule change nor export-ignore may hide the tag's violation.
        (self.repo / release.RULES).write_text("AGENTS.md\n")
        with self.assertRaisesRegex(ValueError, "private paths.*"):
            self.build(tag="v0.2.1")
        self.assertFalse((self.work / "output").exists())

    def test_worktree_checks_tracked_and_untracked_but_respects_ignores(self):
        from check_public_tree import check_repo
        (self.repo / "AGENTS.md").write_text("local instructions")
        with self.assertRaisesRegex(ValueError, "AGENTS.md"):
            check_repo(self.repo)
        (self.repo / ".gitignore").write_text("/AGENTS.md\n")
        check_repo(self.repo)
        self.git("add", "-f", "AGENTS.md")
        with self.assertRaisesRegex(ValueError, "AGENTS.md"):
            check_repo(self.repo)

    def test_downloads_pinned_files_and_rejects_corruption(self):
        entry = self.models["punct"]["files"]["model.onnx"]
        target = self.cache / entry["sha256"]
        content = target.read_bytes()
        target.unlink()
        lock = release.read_lock(self.repo / release.LOCK)
        with patch.object(release.urllib.request, "urlopen", return_value=io.BytesIO(content)) as request:
            release.fetch_models(lock, self.cache)
        self.assertIn("/resolve/" + "b" * 40 + "/model.onnx", request.call_args.args[0])
        target.unlink()
        with patch.object(release.urllib.request, "urlopen", return_value=io.BytesIO(b"wrong")):
            with self.assertRaisesRegex(ValueError, "mismatch"):
                release.fetch_models(lock, self.cache)
        self.assertFalse(target.exists())
        self.assertEqual(list(self.cache.glob(".download-*")), [])

    def test_rejects_existing_output_missing_tag_and_nonversion(self):
        self.build()
        with self.assertRaisesRegex(ValueError, "already exists"):
            self.build()
        with self.assertRaises(subprocess.CalledProcessError):
            self.build("missing", "v9.9.9")
        with self.assertRaisesRegex(ValueError, "version tag"):
            self.build("branch", "main")

    def test_mismatched_model_manifest_is_rejected(self):
        entry = self.models["select"]["files"]["manifest.json"]
        manifest = json.loads((self.cache / entry["sha256"]).read_bytes())
        manifest["files"]["vocab.json"] = "c" * 64
        content = release.json_bytes(manifest)
        entry.update(sha256=hashlib.sha256(content).hexdigest(), size=len(content))
        (self.cache / entry["sha256"]).write_bytes(content)
        (self.repo / release.LOCK).write_bytes(release.json_bytes({"schema": 1, "models": self.models}))
        self.commit_tag("v0.2.1")
        with self.assertRaisesRegex(ValueError, "manifest does not match"):
            self.build(tag="v0.2.1")

    def test_rejects_lfs_pointer_and_mutable_model_revision(self):
        lock = json.loads((self.repo / release.LOCK).read_bytes())
        lock["models"]["select"]["revision"] = "main"
        (self.repo / release.LOCK).write_bytes(release.json_bytes(lock))
        with self.assertRaisesRegex(ValueError, "full commit"):
            release.read_lock(self.repo / release.LOCK)
        entry = self.models["punct"]["files"]["model.onnx"]
        (self.cache / entry["sha256"]).write_text("version https://git-lfs.github.com/spec/v1\n")
        with self.assertRaisesRegex(ValueError, "mismatch"):
            self.build()

    def test_verify_rejects_modified_and_extra_contents(self):
        good = self.build()
        for extra in (False, True):
            bad = self.work / f"bad-{extra}.tar.xz"
            with tarfile.open(good) as source, tarfile.open(bad, "w:xz") as target:
                for member in source:
                    stream = source.extractfile(member) if member.isfile() else None
                    if not extra and member.name.endswith("/README.md"):
                        stream = io.BytesIO(b"tampered")
                        member.size = len(stream.getvalue())
                    target.addfile(member, stream)
                if extra:
                    member = tarfile.TarInfo("ime-oss-0.2.0/extra.txt")
                    target.addfile(member, io.BytesIO())
            with self.assertRaisesRegex(ValueError, "inventory mismatch"):
                release.verify(bad)

    def test_rejects_escaping_symlinks_and_traversal(self):
        (self.repo / "escape").symlink_to("../outside")
        self.commit_tag("v0.2.1")
        with self.assertRaises((ValueError, tarfile.FilterError)):
            self.build(tag="v0.2.1")
        bad = self.work / "traversal.tar"
        with tarfile.open(bad, "w") as archive:
            archive.addfile(tarfile.TarInfo("../outside"), io.BytesIO())
        with self.assertRaisesRegex(ValueError, "invalid"):
            release.verify(bad)


if __name__ == "__main__":
    unittest.main()
