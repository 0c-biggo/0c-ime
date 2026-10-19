#!/usr/bin/env python3
"""Export the explicitly pinned public dictionary/model pair without changing weights.

Only dictionary binding metadata changes. Token order, inference settings and
ONNX bytes remain those of models.lock.json. A different dictionary requires a
reviewed pairing.lock.json update; packaging cannot silently bind arbitrary data.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import tempfile

from package_release import check_data, json_bytes
from source_release import check_file, check_models, digest, read_lock, LOCK

ROOT = Path(__file__).resolve().parents[1]
PAIR_LOCK = "models/pairing.lock.json"


def metadata(manifest, vocab, dictionary):
    manifest, vocab = dict(manifest), dict(vocab)
    manifest["files"] = dict(manifest["files"])
    vocab["dictionary_sha256"] = dictionary
    if "dictionary_sha256" in manifest:
        manifest["dictionary_sha256"] = dictionary
    vocab_bytes = json_bytes(vocab)
    manifest["files"]["vocab.json"] = hashlib.sha256(vocab_bytes).hexdigest()
    return {"vocab.json": vocab_bytes, "manifest.json": json_bytes(manifest)}


def checked_metadata(root, models):
    """Check original packages and all declared derived bytes before exporting."""
    lock = read_lock(root / LOCK)
    # check_models uses the conventional models/ directory under a source root.
    if models.name != "models":
        raise ValueError("upstream package directory must be named models")
    check_models(models.parent, lock)
    pair = json.loads((root / PAIR_LOCK).read_text(encoding="utf-8"))
    if pair.get("schema") != 1 or pair.get("models_lock_sha256") != digest(root / LOCK):
        raise ValueError("pairing lock does not match upstream model lock")
    dictionary = pair.get("dictionary_manifest_sha256", "")
    if len(dictionary) != 64 or any(c not in "0123456789abcdef" for c in dictionary):
        raise ValueError("invalid paired dictionary hash")
    if set(pair.get("packages", {})) != {"select", "punct"}:
        raise ValueError("pairing lock must bind both models")
    derived = {}
    for role in lock["models"]:
        package = models / role
        manifest = json.loads((package / "manifest.json").read_text(encoding="utf-8"))
        vocab = json.loads((package / "vocab.json").read_text(encoding="utf-8"))
        derived[role] = metadata(manifest, vocab, dictionary)
        expected = pair["packages"][role]
        if set(expected) != set(derived[role]):
            raise ValueError(f"incomplete pairing metadata: {role}")
        for name, content in derived[role].items():
            if expected[name] != {"sha256": hashlib.sha256(content).hexdigest(), "size": len(content)}:
                raise ValueError(f"paired metadata does not match lock: {role}/{name}")
    return pair, derived


def export(root, data, models, output):
    if output.exists():
        raise ValueError(f"output already exists: {output}")
    # Validate public provenance before considering dictionary hashes.
    check_data(data, public=True)
    pair, derived = checked_metadata(root, models)
    if digest(data / "manifest.json") != pair["dictionary_manifest_sha256"]:
        raise ValueError("dictionary is not the validated public model pair; rebuild or review a new pair")
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".paired-models-", dir=output.parent) as temp:
        stage = Path(temp) / "models"
        stage.mkdir()
        for role in derived:
            shutil.copytree(models / role, stage / role)
            for name, content in derived[role].items():
                (stage / role / name).write_bytes(content)
                check_file(stage / role / name, pair["packages"][role][name])
        shutil.copyfile(root / PAIR_LOCK, stage / "pairing.lock.json")
        os.rename(stage, output)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data", type=Path, required=True)
    parser.add_argument("--models", type=Path, required=True, help="原始上游模型目錄；tarball 內的模型已完成配對")
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    try:
        export(ROOT, args.data, args.models, args.out)
    except (ValueError, OSError, KeyError) as error:
        parser.exit(1, f"model pairing: {error}\n")
    print(f"Paired models: {args.out}")


if __name__ == "__main__":
    main()
