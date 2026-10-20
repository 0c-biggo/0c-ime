"""Reject local instructions and private records in public Git source trees."""

import argparse
from fnmatch import fnmatchcase
from pathlib import Path
import subprocess


ROOT = Path(__file__).resolve().parent.parent
RULES = ".public-source-excludes"
# The private development host; public code imports github.com/0c-biggo/0c-ime.
PRIVATE_HOSTS = ("git.biggo.com",)


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


def check_hosts(git, ref=None):
    found = []
    for host in PRIVATE_HOSTS:
        args = ["grep", "-I", "-l", "-F", host, ref] if ref else \
            ["grep", "--untracked", "-I", "-l", "-F", host]
        args += ["--", ".", ":!scripts/check_public_tree.py"]
        try:
            found += [path.removeprefix(ref + ":") if ref else path
                      for path in git(*args).splitlines()]
        except subprocess.CalledProcessError as error:
            if error.returncode != 1:  # 1 means no match
                raise
    if found:
        raise ValueError("private host in public source:\n" + "\n".join(sorted(set(found))))


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
    check_hosts(git, ref)


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
