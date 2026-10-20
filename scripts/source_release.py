#!/usr/bin/env python3
"""Build a source release from a Git tag and hash-pinned public model packages.

Only the release assembler downloads files. Consumers of the tarball already
have both models and their vocabularies; no LFS client is needed.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import tarfile
import tempfile
import urllib.request

from check_public_tree import check_repo, check_paths, read_rules, RULES
import release_dictionary

ROOT = Path(__file__).resolve().parents[1]
LOCK = "models/models.lock.json"
RECORD = "SOURCE-RELEASE.json"
NAME = "0c-ime"
# 0.2.1 以前的完整包以 ime-oss 為名；extract-data 仍要能重用它們的字典。
LEGACY_NAMES = ("ime-oss",)
MODEL_FILES = {"model.onnx", "vocab.json", "manifest.json", "LICENSE", "README.md"}
TAG = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?")
SHA256 = re.compile(r"[0-9a-f]{64}")


def digest(path):
    with open(path, "rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def json_bytes(value):
    return (json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True) + "\n").encode()


def read_lock(path):
    lock = json.loads(path.read_text(encoding="utf-8"))
    if lock.get("schema") != 1 or set(lock.get("models", {})) != {"select", "punct"}:
        raise ValueError("model lock must contain select and punct packages (schema 1)")
    for role, model in lock["models"].items():
        if not re.fullmatch(r"[A-Za-z0-9_-]+/[A-Za-z0-9_.-]+", model["repository"]):
            raise ValueError(f"invalid model repository: {role}")
        if not re.fullmatch(r"[0-9a-f]{40}", model["revision"]):
            raise ValueError(f"model must be pinned to a full commit: {role}")
        if model["license"] != "MIT" or set(model["files"]) != MODEL_FILES:
            raise ValueError(f"incomplete model package: {role}")
        for name, entry in model["files"].items():
            if not SHA256.fullmatch(entry["sha256"]) or type(entry["size"]) is not int or entry["size"] <= 0:
                raise ValueError(f"invalid model file record: {role}/{name}")
    return lock


def check_file(path, entry):
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"missing regular file: {path}")
    if path.stat().st_size != entry["size"] or digest(path) != entry["sha256"]:
        raise ValueError(f"size or SHA-256 mismatch: {path}")


def fetch_models(lock, cache, offline=False):
    cache.mkdir(parents=True, exist_ok=True)
    for role, model in sorted(lock["models"].items()):
        for name, entry in sorted(model["files"].items()):
            target = cache / entry["sha256"]
            if not target.exists():
                if offline:
                    raise ValueError(f"offline cache miss: {role}/{name} ({entry['sha256']})")
                url = f"https://huggingface.co/{model['repository']}/resolve/{model['revision']}/{name}"
                print(f"Fetching {role}/{name}", flush=True)
                with tempfile.NamedTemporaryFile(dir=cache, prefix=".download-", delete=False) as stream:
                    pending = Path(stream.name)
                    try:
                        with urllib.request.urlopen(url, timeout=120) as response:
                            # Bound a corrupt response before it can exhaust storage.
                            remaining = entry["size"] + 1
                            while remaining:
                                block = response.read(min(1 << 20, remaining))
                                if not block:
                                    break
                                stream.write(block)
                                remaining -= len(block)
                        stream.flush()
                        check_file(pending, entry)
                        os.replace(pending, target)
                    finally:
                        pending.unlink(missing_ok=True)
            check_file(target, entry)


def check_models(root, lock):
    for role, model in lock["models"].items():
        folder = root / "models" / role
        if {p.name for p in folder.iterdir()} != MODEL_FILES:
            raise ValueError(f"unexpected or missing model file: {role}")
        for name, entry in model["files"].items():
            check_file(folder / name, entry)
        manifest = json.loads((folder / "manifest.json").read_text(encoding="utf-8"))
        schema = "zuin-sequence-runtime-v1" if role == "select" else "zuin-punct-runtime-v1"
        expected = {name: model["files"][name]["sha256"] for name in ("model.onnx", "vocab.json")}
        if manifest.get("schema") != schema or manifest.get("files") != expected:
            raise ValueError(f"model manifest does not match lock: {role}")
        if role == "select" and not SHA256.fullmatch(manifest.get("dictionary_sha256", "")):
            raise ValueError("selection model is missing its system dictionary binding")
        if role == "select":
            vocab = json.loads((folder / "vocab.json").read_text(encoding="utf-8"))
            if vocab.get("dictionary_sha256") != manifest["dictionary_sha256"]:
                raise ValueError("selection vocabulary and manifest bind different system dictionaries")


def inventory(root):
    files = {}
    for path in sorted(root.rglob("*")):
        relative = path.relative_to(root).as_posix()
        if relative == RECORD:
            continue
        if path.is_symlink():
            # No source symlink may reach outside the tarball, even indirectly.
            if not path.resolve().is_relative_to(root.resolve()):
                raise ValueError(f"source symlink escapes archive: {relative}")
            files[relative] = {"link": os.readlink(path)}
        elif path.is_file():
            files[relative] = {"sha256": digest(path), "size": path.stat().st_size,
                               "mode": 0o755 if path.stat().st_mode & 0o111 else 0o644}
        elif not path.is_dir():
            raise ValueError(f"unsupported source file: {relative}")
    return files


def extract(archive, destination):
    # Python 3.12's data filter rejects traversal, devices and escaping links.
    seen = set()
    for member in archive.getmembers():
        path = PurePosixPath(member.name)
        if path.is_absolute() or ".." in path.parts or member.name in seen:
            raise ValueError(f"invalid or duplicate archive member: {member.name}")
        if not (member.isfile() or member.isdir() or member.issym()):
            raise ValueError(f"unsupported archive member: {member.name}")
        seen.add(member.name)
    archive.extractall(destination, filter="data")


def git(repo, *args):
    return subprocess.check_output(["git", "-C", str(repo), *args], text=True).strip()


def build(repo, tag, output, cache, offline=False, data_dir=None):
    if not TAG.fullmatch(tag):
        raise ValueError("expected a version tag such as v0.2.0 or v0.2.1-dev.1")
    ref = "refs/tags/" + tag
    commit = git(repo, "rev-parse", "--verify", ref + "^{commit}")
    check_repo(repo, commit)
    epoch = int(git(repo, "show", "-s", "--format=%ct", commit))
    # Resolve once: a tag moved during assembly must not change the source.
    name = f"{NAME}-{tag[1:]}"
    if output.exists():
        raise ValueError(f"output already exists: {output}")
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".source-release-", dir=output.parent) as temp:
        work = Path(temp)
        source_tar = work / "source.tar"
        with source_tar.open("wb") as stream:
            subprocess.run(["git", "-C", str(repo), "archive", "--format=tar", commit], stdout=stream, check=True)
        root = work / name
        root.mkdir()
        with tarfile.open(source_tar) as archive:
            extract(archive, root)
        if (root / RECORD).exists():
            raise ValueError(f"{RECORD} is generated and must not be committed")
        dictionary_lock = release_dictionary.read_lock(root)
        if data_dir is None:
            raise ValueError("--data-dir is required; supply the pinned compiled dictionary")
        release_dictionary.check(data_dir, dictionary_lock)
        folder = root / release_dictionary.DIRECTORY
        folder.mkdir(parents=True)
        for filename in dictionary_lock["files"]:
            target = folder / filename
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(data_dir / filename, target)
        lock = read_lock(root / LOCK)  # Always use the tag's lock, never the working tree's.
        fetch_models(lock, cache, offline)
        upstream = work / "upstream"
        (upstream / "models").mkdir(parents=True)
        for role, model in lock["models"].items():
            folder = upstream / "models" / role
            folder.mkdir()  # Fail if model artifacts were accidentally committed.
            for filename, entry in model["files"].items():
                shutil.copyfile(cache / entry["sha256"], folder / filename)
        from pair_models import checked_metadata
        _, derived = checked_metadata(root, upstream / "models")
        for role in lock["models"]:
            folder = root / "models" / role
            shutil.copytree(upstream / "models" / role, folder)
            for filename, content in derived[role].items():
                (folder / filename).write_bytes(content)
        release_dictionary.check_assets(root)
        record = {"schema": 2, "tag": tag, "commit": commit, "source_date_epoch": epoch,
                  "dictionary_lock_sha256": digest(root / release_dictionary.LOCK),
                  "models_lock_sha256": digest(root / LOCK), "files": inventory(root)}
        (root / RECORD).write_bytes(json_bytes(record))
        stage = work / "output"
        stage.mkdir()
        tarball = stage / (name + ".tar.xz")

        def normalized(info):
            info.uid = info.gid = 0
            info.uname = info.gname = ""
            info.mtime = epoch
            info.mode = 0o755 if info.isdir() or info.mode & 0o111 else 0o644
            info.pax_headers = {}
            return info

        with tarfile.open(tarball, "w:xz", format=tarfile.PAX_FORMAT, preset=6) as archive:
            archive.add(root, arcname=name, recursive=True, filter=normalized)
        (stage / "SHA256SUMS").write_text(f"{digest(tarball)}  {tarball.name}\n", encoding="ascii")
        os.rename(stage, output)
    return output / (name + ".tar.xz")


def verify(tarball):
    with tempfile.TemporaryDirectory(prefix=NAME + "-verify-") as temp:
        destination = Path(temp)
        with tarfile.open(tarball) as archive:
            extract(archive, destination)
        roots = list(destination.iterdir())
        if len(roots) != 1 or not roots[0].is_dir() or roots[0].is_symlink():
            raise ValueError("expected exactly one source directory")
        root = roots[0]
        record = json.loads((root / RECORD).read_text(encoding="utf-8"))
        if record.get("schema") != 2 or not TAG.fullmatch(record["tag"]):
            raise ValueError("invalid source release record")
        if root.name not in {f"{n}-{record['tag'][1:]}" for n in (NAME, *LEGACY_NAMES)}:
            raise ValueError("source directory does not match version")
        if not re.fullmatch(r"[0-9a-f]{40}|[0-9a-f]{64}", record["commit"]):
            raise ValueError("invalid source commit")
        if inventory(root) != record["files"]:
            raise ValueError("source release file inventory mismatch")
        check_paths(record["files"], read_rules((root / RULES).read_text(encoding="utf-8")))
        if digest(root / LOCK) != record["models_lock_sha256"]:
            raise ValueError("model lock checksum mismatch")
        if digest(root / release_dictionary.LOCK) != record["dictionary_lock_sha256"]:
            raise ValueError("dictionary lock checksum mismatch")
        release_dictionary.check_assets(root)
        return record


def extract_dictionary(tarball, output, root=ROOT):
    """Reuse only the pinned compiled dictionary from a previous full source release."""
    lock = release_dictionary.read_lock(root)
    if output.exists():
        raise ValueError(f"output already exists: {output}")
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".dictionary-", dir=output.parent) as temp:
        stage = Path(temp) / "inputs"
        stage.mkdir()
        seen = set()
        with tarfile.open(tarball, "r|*") as archive:
            for member in archive:
                parts = PurePosixPath(member.name).parts
                if len(parts) < 4 or "/".join(parts[1:3]) != release_dictionary.DIRECTORY:
                    continue
                name = "/".join(parts[3:])
                if name not in lock["files"]:
                    continue
                if name in seen or not member.isfile() or member.size != lock["files"][name]["size"]:
                    raise ValueError(f"invalid dictionary archive member: {name}")
                seen.add(name)
                (stage / name).parent.mkdir(parents=True, exist_ok=True)
                with archive.extractfile(member) as source, (stage / name).open("wb") as target:
                    shutil.copyfileobj(source, target)
        release_dictionary.check(stage, lock)
        os.rename(stage, output)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    fetch = commands.add_parser("fetch", help="prepare a verified model cache")
    fetch.add_argument("--lock", type=Path, default=ROOT / LOCK)
    fetch.add_argument("--cache", type=Path, default=ROOT / "build/cache/source-models")
    release = commands.add_parser("build", help="assemble code from a tag plus pinned models")
    release.add_argument("--tag", required=True)
    release.add_argument("--out", type=Path, required=True, help="new output directory")
    release.add_argument("--cache", type=Path, default=ROOT / "build/cache/source-models")
    release.add_argument("--offline", action="store_true", help="never download; require all cached files")
    release.add_argument("--data-dir", type=Path, required=True, help="directory containing the pinned compiled dictionary")
    reuse = commands.add_parser("extract-data", help="verify and reuse a compiled dictionary from a previous full tarball")
    reuse.add_argument("tarball", type=Path)
    reuse.add_argument("--out", type=Path, required=True)
    check = commands.add_parser("verify", help="verify tarball contents without network access")
    check.add_argument("tarball", type=Path)
    args = parser.parse_args()
    try:
        if args.command == "fetch":
            fetch_models(read_lock(args.lock), args.cache)
            print("Model cache verified.")
        elif args.command == "build":
            print(build(ROOT, args.tag, args.out.absolute(), args.cache.absolute(), args.offline, args.data_dir.absolute()))
        elif args.command == "extract-data":
            extract_dictionary(args.tarball, args.out.absolute())
            print("Compiled dictionary verified.")
        else:
            record = verify(args.tarball)
            print(f"Verified {record['tag']} ({record['commit']}), including the compiled dictionary and paired models.")
    except (ValueError, OSError, KeyError, subprocess.CalledProcessError, tarfile.TarError) as error:
        parser.exit(1, f"source release: {error}\n")


if __name__ == "__main__":
    main()
