#!/usr/bin/env python3
"""Generate the one-line install scripts (install.sh, install.ps1, install.cmd; issue #19).

Each script pins the SHA-256 of the bootstrap 0c-update for its platform, downloads it over HTTPS and runs
`0c-update install -online`, which verifies the signed feed and the full package itself. Bootstraps are the
exact 0c-update binaries inside the published packages.
"""
import argparse
import hashlib
from pathlib import Path
import re

TEMPLATES = Path(__file__).resolve().parent / "installer"
PLATFORMS = ("linux_amd64", "darwin_arm64", "windows_amd64")


def sha256(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def render(name, values):
    text = (TEMPLATES / (name + ".in")).read_text(encoding="utf-8")
    for key, value in values.items():
        if re.search(r"[\s'\"`$;&|<>]", value) and not key.endswith("_EXTRA"):
            raise ValueError(f"{key} 含不允許的字元：{value!r}")
        text = text.replace(f"@{key}@", value)
    missing = re.findall(r"@[A-Z0-9_]+@", text)
    if missing:
        raise ValueError(f"{name} 還有未填的欄位：{missing}")
    return text


def generate(out, base_url, feed, channel, bootstraps, cacert=None):
    """bootstraps: {platform: (local_path, url)}. Returns {filename: path}."""
    values = {"FEED": feed, "CHANNEL": channel, "SELF_URL": f"{base_url}/install.sh", "PS1_URL": f"{base_url}/install.ps1",
              "CURL_EXTRA": f"--cacert {cacert} " if cacert else "", "WGET_EXTRA": f"--ca-certificate={cacert} " if cacert else ""}
    for platform in PLATFORMS:
        path, url = bootstraps.get(platform, (None, ""))
        values[f"URL_{platform.upper()}"] = url
        values[f"SHA_{platform.upper()}"] = sha256(path) if path else ""
    out.mkdir(parents=True, exist_ok=True)
    written = {}
    for name, self_name in (("install.sh", "install.sh"), ("install.ps1", "install.ps1"), ("install.cmd", "install.cmd")):
        v = dict(values, SELF_URL=f"{base_url}/{self_name}")
        text = render(name, v)
        target = out / name
        target.write_text(text if name != "install.cmd" else text.replace("\n", "\r\n"), encoding="utf-8", newline="")
        written[name] = target
    return written


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--out", type=Path, required=True)
    p.add_argument("--base-url", required=True, help="腳本公開位置（不含檔名）")
    p.add_argument("--feed", required=True)
    p.add_argument("--channel", default="public-beta")
    p.add_argument("--bootstrap", action="append", default=[], metavar="PLATFORM=PATH=URL",
                   help=f"平台（{', '.join(PLATFORMS)}）的 0c-update 本機檔與公開網址；可重複")
    p.add_argument("--cacert", help="僅供 E2E：curl／wget 另外信任的 CA 檔")
    args = p.parse_args()
    boots = {}
    for spec in args.bootstrap:
        platform, path, url = spec.split("=", 2)
        if platform not in PLATFORMS:
            p.error(f"未知平台 {platform}")
        boots[platform] = (Path(path), url)
    for name, path in generate(args.out, args.base_url.rstrip("/"), args.feed, args.channel, boots, args.cacert).items():
        print(path)


if __name__ == "__main__":
    main()
