#!/usr/bin/env python3
"""Fetch the compiled dictionary and paired models for this source tree.

A git clone or GitHub's source archive carries only the lock files. This
downloads the matching full source tarball from GitHub Releases, verifies it,
checks that its dictionary, model and pairing locks equal this tree's, and
installs assets/data/ and models/{select,punct}/ in the same layout as the
tarball.
"""

import argparse
import hashlib
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile
import urllib.error
import urllib.request

import release_dictionary
import source_release as release
from pair_models import PAIR_LOCK

REPOSITORY = "0c-biggo/0c-ime"
TARGETS = (release_dictionary.DIRECTORY, "models/select", "models/punct")
LOCKS = (release_dictionary.LOCK, release.LOCK, PAIR_LOCK)


def default_tag(root):
    """The release this tree comes from: the nearest tag, else a GitHub archive's directory name."""
    if (root / ".git").exists():
        try:
            return release.git(root, "describe", "--tags", "--abbrev=0", "--match", "v[0-9]*")
        except subprocess.CalledProcessError:
            pass
    for name in (release.NAME, *release.LEGACY_NAMES):
        m = re.fullmatch(re.escape(name) + r"-(.+)", root.name)
        if m and release.TAG.fullmatch("v" + m.group(1)):
            return "v" + m.group(1)
    raise ValueError("cannot tell which release this tree belongs to; pass --release vX.Y.Z")


def installed(root):
    try:
        release_dictionary.check_assets(root, release=False)
        return True
    except (ValueError, OSError, KeyError):
        return False


def install(tarball, root):
    """Verify a full source tarball and install its assets when its locks equal root's."""
    record = release.verify(tarball)
    with tempfile.TemporaryDirectory(prefix=".assets-", dir=root) as temp:
        stage = Path(temp)
        with tarfile.open(tarball) as archive:
            release.extract(archive, stage)
        (source,) = stage.iterdir()
        for lock in LOCKS:
            if release.digest(source / lock) != release.digest(root / lock):
                raise ValueError(f"{lock} differs from {record['tag']}: this tree's dictionary or models "
                                 "are not in that release; use a matching release or rebuild the dictionary")
        for target in TARGETS:
            old = root / target
            if old.is_symlink() or old.is_file():
                raise ValueError(f"refusing to replace non-directory: {target}")
            old.parent.mkdir(parents=True, exist_ok=True)
            if old.exists():
                shutil.rmtree(old)
            os.rename(source / target, old)
    if not installed(root):
        raise ValueError("installed assets do not match this tree's locks")
    return record


def download(tag, cache, repository=REPOSITORY):
    """Download a release's full tarball once, checked against the release's SHA256SUMS."""
    base = f"https://github.com/{repository}/releases/download/{tag}/"
    try:
        with urllib.request.urlopen(base + "SHA256SUMS", timeout=60) as response:
            sums = response.read(1 << 16).decode("ascii")
    except urllib.error.HTTPError as error:
        raise ValueError(f"{tag} has no published source release on {repository} ({error.code})") from None
    names = [f"{name}-{tag[1:]}.tar.xz" for name in (release.NAME, *release.LEGACY_NAMES)]
    entries = dict(reversed(line.split(maxsplit=1)) for line in sums.splitlines() if line.strip())
    name = next((n for n in names if n in entries), None)
    expected = entries.get(name, "")
    if name is None or not release.SHA256.fullmatch(expected):
        raise ValueError(f"no source tarball listed in {tag}'s SHA256SUMS")
    cache.mkdir(parents=True, exist_ok=True)
    target = cache / name
    if target.exists() and release.digest(target) == expected:
        return target
    print(f"Downloading {base}{name}", flush=True)
    with tempfile.NamedTemporaryFile(dir=cache, prefix=".download-", delete=False) as stream:
        pending = Path(stream.name)
        try:
            h = hashlib.sha256()
            with urllib.request.urlopen(base + name, timeout=120) as response:
                while block := response.read(1 << 20):
                    stream.write(block)
                    h.update(block)
            stream.flush()
            if h.hexdigest() != expected:
                raise ValueError(f"SHA-256 mismatch: {name}")
            os.replace(pending, target)
        finally:
            pending.unlink(missing_ok=True)
    return target


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--release", help="release tag to fetch (default: this tree's version)")
    parser.add_argument("--tarball", type=Path, help="use an already downloaded full source tarball")
    parser.add_argument("--cache", type=Path, default=release.ROOT / "build/cache/releases")
    args = parser.parse_args()
    root = release.ROOT
    try:
        if args.tarball is None and installed(root):
            print("Dictionary and models already match this tree's locks.")
            return
        if args.tarball:
            tarball = args.tarball
        else:
            tag = args.release or default_tag(root)
            if not release.TAG.fullmatch(tag):
                raise ValueError(f"not a release tag: {tag}")
            tarball = download(tag, args.cache.absolute())
        record = install(tarball.absolute(), root)
        print(f"Installed the dictionary and paired models from {record['tag']} into "
              f"{', '.join(t + '/' for t in TARGETS)}.")
    except (ValueError, OSError, KeyError, urllib.error.URLError, tarfile.TarError) as error:
        parser.exit(1, f"fetch assets: {error}\n")


if __name__ == "__main__":
    main()
