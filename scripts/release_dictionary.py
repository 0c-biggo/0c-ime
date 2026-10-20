"""Check the fixed, ready-to-use public dictionary shipped with source releases."""

import json
from pathlib import Path

from package_release import check_data

LOCK = "data/dictionary.lock.json"
DIRECTORY = "assets/data"
FILES = {"manifest.json", "lexicon.bin", "lm.bin", "NOTICE.txt", "report.txt",
         "english/manifest.json", "english/words.bin", "english/NOTICE.txt"}


def read_lock(root):
    from source_release import SHA256
    lock = json.loads((root / LOCK).read_text(encoding="utf-8"))
    if lock.get("schema") != 1 or set(lock.get("files", {})) != FILES:
        raise ValueError("dictionary lock must contain the complete Chinese and English packages")
    for name, entry in lock["files"].items():
        if (not SHA256.fullmatch(entry.get("sha256", ""))
                or type(entry.get("size")) is not int or entry["size"] <= 0):
            raise ValueError(f"invalid dictionary file record: {name}")
    return lock


def check(directory, lock):
    from source_release import check_file
    paths = list(directory.rglob("*"))
    if any(p.is_symlink() for p in paths) or directory.is_symlink():
        raise ValueError("dictionary package must not contain symlinks")
    if {p.relative_to(directory).as_posix() for p in paths if p.is_file()} != FILES:
        raise ValueError("unexpected or missing dictionary file")
    # Source attribution is checked even if a caller changes a lock file.
    check_data(directory, public=True)
    for name, entry in lock["files"].items():
        check_file(directory / name, entry)


def check_assets(root, release=True):
    """release=False checks a source tree, where maintainers may keep local bigram inputs."""
    from source_release import LOCK as MODEL_LOCK, check_models, digest, read_lock as models_lock
    from pair_models import PAIR_LOCK
    if release and (root / "data/lm/bigram").exists():
        raise ValueError("source release must not include data/lm/bigram inputs")
    check(root / DIRECTORY, read_lock(root))
    lock = models_lock(root / MODEL_LOCK)
    pair = json.loads((root / PAIR_LOCK).read_text(encoding="utf-8"))
    if (pair.get("schema") != 1 or pair.get("models_lock_sha256") != digest(root / MODEL_LOCK)
            or set(pair.get("packages", {})) != {"select", "punct"}
            or pair.get("dictionary_manifest_sha256") != digest(root / DIRECTORY / "manifest.json")):
        raise ValueError("dictionary and model pairing lock mismatch")
    for role, model in lock["models"].items():
        if set(pair["packages"][role]) != {"manifest.json", "vocab.json"}:
            raise ValueError(f"incomplete pairing metadata: {role}")
        model["files"].update(pair["packages"][role])
        vocab = json.loads((root / "models" / role / "vocab.json").read_text(encoding="utf-8"))
        if vocab.get("dictionary_sha256") != pair["dictionary_manifest_sha256"]:
            raise ValueError(f"vocabulary and dictionary binding mismatch: {role}")
    check_models(root, lock)
