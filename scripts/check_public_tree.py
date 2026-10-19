"""Reject local instructions and private records in public Git source trees."""

import argparse
from fnmatch import fnmatchcase
from pathlib import Path
import subprocess


ROOT = Path(__file__).resolve().parent.parent
RULES = ".public-source-excludes"


def read_rules(text):
    patterns = [line.strip() for line in text.splitlines()
                if line.strip() and not line.lstrip().startswith("#")]
    if not patterns:
        raise ValueError("public source exclusion rules are empty")
    return patterns


def check_paths(paths, patterns):
    blocked = sorted({path for path in paths if path and any(
        fnmatchcase(path, pattern.rstrip("/")) or
        path.startswith(pattern.rstrip("/") + "/")
        for pattern in patterns)})
    if blocked:
        raise ValueError("private paths in public source:\n" + "\n".join(blocked))


def check_repo(repo, ref=None):
    def git(*args):
        return subprocess.check_output(["git", "-C", str(repo), *args], text=True)

    if ref:
        # Inspect the committed tree before export-ignore can conceal mistakes.
        patterns = read_rules(git("show", ref + ":" + RULES))
        paths = git("ls-tree", "-r", "--name-only", "-z", ref).split("\0")
    else:
        patterns = read_rules((Path(repo) / RULES).read_text(encoding="utf-8"))
        paths = git("ls-files", "--cached", "--others", "--exclude-standard", "-z").split("\0")
    check_paths(paths, patterns)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ref", help="check a committed tree, including export-ignored paths")
    args = parser.parse_args()
    try:
        check_repo(ROOT, args.ref)
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"public source: {error}\n")
    print("Public source paths verified.")


if __name__ == "__main__":
    main()
