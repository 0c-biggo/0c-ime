#!/usr/bin/env python3
"""Create an update index from existing release archives; never publish it."""
import argparse
import json
from pathlib import Path, PurePosixPath
import sys
import tarfile
from urllib.parse import urlsplit
import zipfile

from install_package import digest
from package_release import valid_version


def metadata(archive):
    if archive.suffix == ".zip":
        with zipfile.ZipFile(archive) as package:
            entries = [entry for entry in package.infolist() if entry.filename.endswith("/release.json")]
            if len(entries) != 1 or entries[0].file_size > 1024 * 1024:
                raise ValueError("壓縮包需要一份 release.json，大小上限 1 MiB")
            name = entries[0].filename
            payload = package.read(entries[0])
    else:
        with tarfile.open(archive, "r:gz") as package:
            entries = [entry for entry in package if entry.name.endswith("/release.json")]
            if len(entries) != 1 or not entries[0].isfile() or entries[0].size > 1024 * 1024:
                raise ValueError("壓縮包需要一份 release.json，大小上限 1 MiB")
            name = entries[0].name
            with package.extractfile(entries[0]) as stream:
                payload = stream.read()
    if len(PurePosixPath(name).parts) != 2 or name.startswith("/") or ".." in PurePosixPath(name).parts:
        raise ValueError("release.json 必須位於套件根目錄")
    release = json.loads(payload)
    if release.get("schema") != 1 or release.get("product") != "0c" or not valid_version(release.get("version", "")):
        raise ValueError("不支援的套件版本資訊")
    if release.get("channel") not in ("public-beta", "internal-preview"):
        raise ValueError("未知通道")
    if (release.get("os"), release.get("arch")) not in (("linux", "amd64"), ("linux", "arm64"), ("windows", "amd64")):
        raise ValueError("不支援的平台或缺少 os/arch 欄位；請以新版工具重新建置")
    return release


def make_feed(assets):
    feed = None
    seen = set()
    for file, address in assets:
        url = urlsplit(address)
        if url.scheme != "https" or not url.hostname or url.username is not None or url.password is not None or url.fragment:
            raise ValueError("下載網址須為 HTTPS，且不含帳密或片段")
        archive = Path(file)
        release = metadata(archive)
        if feed is None:
            feed = {key: release[key] for key in ("schema", "product", "version", "channel")}
            feed["assets"] = []
        if release["version"] != feed["version"] or release["channel"] != feed["channel"]:
            raise ValueError("同一索引的版本與通道必須一致")
        key = (release["os"], release["arch"])
        if key in seen:
            raise ValueError("平台資產重複")
        seen.add(key)
        size = archive.stat().st_size
        if not 0 < size <= 1 << 30:
            raise ValueError("套件大小須在 1 GiB 以內")
        feed["assets"].append({"os": key[0], "arch": key[1], "url": address,
                               "sha256": digest(archive), "size": size})
    if feed is None:
        raise ValueError("至少需要一個套件")
    return feed


def main():
    parser = argparse.ArgumentParser(description="從既有套件建立 0c HTTPS 更新索引；不會上傳")
    parser.add_argument("--asset", action="append", nargs=2, required=True, metavar=("ARCHIVE", "HTTPS_URL"))
    parser.add_argument("--out", required=True, type=Path)
    args = parser.parse_args()
    try:
        feed = make_feed(args.asset)
        with args.out.open("x", encoding="utf-8") as stream:
            stream.write(json.dumps(feed, ensure_ascii=False, indent=2) + "\n")
        print(args.out)
    except (OSError, ValueError, KeyError, tarfile.TarError, zipfile.BadZipFile) as error:
        print(f"索引建立失敗：{error}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
