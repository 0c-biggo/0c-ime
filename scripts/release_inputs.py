"""Validate redistributable, hash-pinned bigram sources before copying them."""

import csv
import hashlib
import json
from pathlib import Path
import re

LOCK = "data/sources/bigram.lock.json"
DIRECTORY = "data/lm/bigram"
NAMES = {"chewing-v4.tsv", "zhwiki.tsv", "0c_collect-ns.tsv", "0c_collect-ss.tsv"}


def digest(path):
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def read_lock(root):
    lock = json.loads((root / LOCK).read_text(encoding="utf-8"))
    if lock.get("schema") != 1 or set(lock.get("files", {})) != NAMES:
        raise ValueError("bigram lock must contain all four source files")
    with (root / "data/sources/ledger.tsv").open(encoding="utf-8") as stream:
        rows = [row for row in csv.reader(stream, delimiter="\t") if row and not row[0].startswith("#")]
    ledger = {row[0]: row for row in rows}
    if len(ledger) != len(rows):
        raise ValueError("duplicate source ID in ledger")
    for name, entry in lock["files"].items():
        source = name.removesuffix(".tsv")
        if entry.get("source") != source or not re.fullmatch(r"[0-9a-f]{64}", entry.get("sha256", "")):
            raise ValueError(f"invalid bigram record: {name}")
        if type(entry.get("size")) is not int or entry["size"] <= 0:
            raise ValueError(f"invalid bigram size: {name}")
        row = ledger.get(source, [])
        if (len(row) != 7 or row[5] != "yes"
                or any(value.strip().lower() in ("", "-", "unknown", "未定") for value in row)
                or row[4] != entry.get("license") or row[4] not in ("CC-BY-4.0", "CC-BY-SA-4.0")):
            raise ValueError(f"bigram source is not approved for redistribution: {source}")
        notice = root / "data/sources/licenses" / (source + ".txt")
        if not notice.is_file() or not notice.read_text(encoding="utf-8").strip():
            raise ValueError(f"missing source license notice: {source}")
    return lock


def check(directory, lock):
    for name, entry in lock["files"].items():
        path = directory / name
        if path.is_symlink() or not path.is_file():
            raise ValueError(f"missing regular bigram source: {path}")
        if path.stat().st_size != entry["size"] or digest(path) != entry["sha256"]:
            raise ValueError(f"bigram size or SHA-256 mismatch: {path}")
        with path.open("rb") as stream:
            if stream.readline() != f"# origin: {entry['source']}\n".encode():
                raise ValueError(f"bigram source header mismatch: {path}")
