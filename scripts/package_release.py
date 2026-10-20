#!/usr/bin/env python3
"""Build a local Linux bundle with an explicit file list and source checks; never upload."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile

from collect_notices import collect
from install_package import digest, regular, verify

ROOT = Path(__file__).resolve().parents[1]
PLATFORM = ROOT / "platforms/linux-fcitx5"
LICENSE_FILES = {
    "LICENSE": "LICENSE",
    "legal/README.md": "legal/README.md",
    "legal/CC-BY-SA-4.0.txt": "legal/CC-BY-SA-4.0.txt",
    "legal/UNICODE-NOTICE.txt": "legal/UNICODE-NOTICE.txt",
    "legal/TERMS.md": "TERMS.md",
}
DATA_FILES = ("lexicon.bin", "lm.bin", "manifest.json", "NOTICE.txt", "report.txt")
ENGLISH_FILES = ("words.bin", "manifest.json", "NOTICE.txt")


def check_licenses():
    for name in LICENSE_FILES:
        if not regular(ROOT / name).read_text(encoding="utf-8").strip():
            raise ValueError(f"授權文件不可為空：{name}")


def copy_licenses(stage):
    check_licenses()
    for source, destination in LICENSE_FILES.items():
        target = stage / destination
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(ROOT / source, target)


RELEASE_DOC_FILES = ("INSTALL.md", "PRIVACY.md", "UPDATING.md")


def check_release_docs(directory):
    if directory is None:
        raise ValueError("完整安裝包需要 --release-docs 指定安裝說明、隱私與更新文件")
    directory = Path(directory)
    for name in RELEASE_DOC_FILES:
        if not regular(directory / name).read_text(encoding="utf-8").strip():
            raise ValueError(f"安裝包文件不可為空：{name}")
    return directory


def copy_release_docs(directory, stage):
    directory = check_release_docs(directory)
    for name in RELEASE_DOC_FILES:
        shutil.copy2(directory / name, stage / name)


def check_english(data):
    directory = data / "english"
    if not directory.exists() and not directory.is_symlink():
        return None  # Older Chinese packs remain usable.
    if directory.is_symlink() or not directory.is_dir():
        raise ValueError("英文資料包必須是一般目錄")
    manifest = json.loads(regular(directory / "manifest.json").read_text(encoding="utf-8"))
    count = manifest.get("words")
    if manifest.get("format") != 1 or type(count) is not int or not 0 < count <= 200000 or not manifest.get("ranking"):
        raise ValueError("英文資料包格式或詞數不合法")
    files = manifest.get("files", {})
    if set(files) != {"words.bin", "NOTICE.txt"}:
        raise ValueError("英文資料包檔案清單不合法")
    for name in ("words.bin", "NOTICE.txt"):
        path = regular(directory / name)
        info = files[name]
        if digest(path) != info.get("sha256") or path.stat().st_size != info.get("size"):
            raise ValueError(f"英文資料包雜湊或大小不符：{name}")
    if not manifest.get("sources"):
        raise ValueError("英文資料包缺少來源紀錄")
    for source in manifest["sources"]:
        for field in ("name", "version", "url", "sha256", "license", "attribution"):
            if not str(source.get(field, "")).strip():
                raise ValueError(f"英文資料包缺少來源 {field}")
    if not (directory / "NOTICE.txt").read_text(encoding="utf-8").strip():
        raise ValueError("英文資料包缺少授權聲明")
    return manifest


def copy_english(source, destination, go):
    """Stage an independent English pack before sealing/signing; no model re-pairing."""
    if check_english(source) is not None:
        if source.resolve() != destination.resolve():
            target = destination / "english"
            target.mkdir()
            for name in ENGLISH_FILES:
                shutil.copy2(regular(source / "english" / name), target / name)
    else:
        subprocess.run([go, "run", "./cmd/0c-english", "-source", str(ROOT / "data/english"),
                        "-out", str(destination / "english")], cwd=ROOT, check=True)
    check_english(destination)


def valid_version(version):
    if not re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[a-zA-Z0-9]+(?:\.[a-zA-Z0-9]+)*)?", version):
        return False
    return all(not (part.isdigit() and len(part) > 1 and part[0] == "0")
               for part in version.partition("-")[2].split("."))


def check_data(data, public):
    manifest = json.loads(regular(data / "manifest.json").read_text(encoding="utf-8"))
    if manifest.get("format") not in (1, 2):
        raise ValueError("資料包格式須為 1 或 2")
    for name in DATA_FILES:
        regular(data / name)
    for name in ("lexicon.bin", "lm.bin"):
        info = manifest.get("files", {}).get(name, {})
        if digest(data / name) != info.get("sha256") or (data / name).stat().st_size != info.get("size"):
            raise ValueError(f"資料包雜湊或大小不符：{name}")
    if public:
        check_public_sources(manifest, (data / "NOTICE.txt").read_text(encoding="utf-8"))
    check_english(data)
    return manifest


# Legacy identifiers are rejected for public packages, never silently removed.
PRIVATE_RESEARCH_SOURCE = "zuin-news-stratified-20260921"
PRIVATE_LICENSE = "私有"
NEURAL_PACKAGES = ("select", "punct")


def private_source(source):
    return isinstance(source, dict) and (source.get("ID") == PRIVATE_RESEARCH_SOURCE
                                         or str(source.get("License", "")).strip() == PRIVATE_LICENSE)


def check_public_sources(manifest, notice):
    """Validate public provenance without rewriting the manifest or its notices."""
    options = manifest.get("options")
    if not isinstance(options, dict) or options.get("Public") is not True:
        raise ValueError("公開包需要以 0c-compile -public 重新編譯的資料包")
    # Old post-compilation mixes may have had their source removed by older packagers.
    if "research_news_mix" in manifest:
        raise ValueError("舊混合資料的來源無法完整確認，請以 -public 重新編譯")
    sources = manifest.get("sources")
    if not isinstance(sources, list) or not sources:
        raise ValueError("資料包缺少來源紀錄或來源格式不合法")
    seen = set()
    for source in sources:
        if not isinstance(source, dict):
            raise ValueError("來源紀錄格式不合法")
        source_id = source.get("ID")
        if source.get("Distributable") is not True or private_source(source):
            raise ValueError(f"來源 {source_id!r} 不可散布或未明確標示可散布；請重建資料包")
        for field in ("ID", "Name", "URL", "Version", "License", "Attribution"):
            value = source.get(field)
            if not isinstance(value, str) or value.strip().lower() in ("", "-", "未定", "unknown") or any(c in value for c in "\r\n"):
                raise ValueError(f"來源 {source_id!r} 缺少或具有無效的 {field}")
        if source_id in (".", "..") or any(c in source_id for c in "/\\"):
            raise ValueError(f"來源 ID 無效：{source_id!r}")
        if source_id in seen:
            raise ValueError(f"重複來源 ID：{source_id}")
        seen.add(source_id)
    notice = notice.replace("\r\n", "\n")
    if any(line.lstrip().startswith("研究來源：") for line in notice.splitlines()):
        raise ValueError("NOTICE 含舊混合來源紀錄，請以 -public 重新編譯，不得移除標記後出貨")
    headings = list(re.finditer(r"(?m)^== (.+)（([^（）\n]+)）==$", notice))
    sections = {}
    for index, heading in enumerate(headings):
        name, source_id = heading.groups()
        if source_id in sections:
            raise ValueError(f"NOTICE 有重複來源：{source_id}")
        end = headings[index + 1].start() if index + 1 < len(headings) else len(notice)
        sections[source_id] = (name, notice[heading.end():end].strip())
    if set(sections) != seen:
        raise ValueError("NOTICE 的逐來源授權聲明與 manifest 不一致")
    for source in sources:
        name, section = sections[source["ID"]]
        metadata, separator, statement = section.partition("\n\n")
        expected = [f"{label}：{source[field]}" for label, field in (
            ("授權", "License"), ("版本", "Version"), ("網址", "URL"), ("姓名標示", "Attribution"))]
        if name != source["Name"] or any(line not in metadata.splitlines() for line in expected):
            raise ValueError(f"NOTICE 來源 {source['ID']} 的授權資訊與 manifest 不一致")
        if not separator or not statement.strip():
            raise ValueError(f"NOTICE 來源 {source['ID']} 缺少授權全文或聲明")


def json_bytes(value):
    # 與 0c-compile／模型匯出寫出的格式相同（兩格縮排、不轉義中文、結尾換行）。
    return (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode("utf-8")


def prepare_data_manifest(data, neural=None):
    """Verify model binding and return the unchanged dictionary manifest hash.

    Packaging must not delete source metadata or rebind models to conceal provenance.
    """
    path = regular(data / "manifest.json")
    original = path.read_bytes()
    old = hashlib.sha256(original).hexdigest()
    if neural is not None:
        neural = Path(neural)
        for kind in NEURAL_PACKAGES:
            if kind != "select" and not (neural / kind / "manifest.json").exists():
                continue
            package_manifest = json.loads(regular(neural / kind / "manifest.json").read_text(encoding="utf-8"))
            vocab = json.loads(regular(neural / kind / "vocab.json").read_text(encoding="utf-8"))
            if package_manifest.get("files", {}).get("vocab.json") != digest(neural / kind / "vocab.json"):
                raise ValueError(f"神經模型 {kind} 的 vocab.json 與模型 manifest 不符")
            recorded = [m["dictionary_sha256"] for m in (package_manifest, vocab) if "dictionary_sha256" in m]
            if kind == "select" and len(recorded) != 2 or any(value != old for value in recorded):
                raise ValueError("神經選字模型與本套件字典不配對" if kind == "select" else f"神經模型 {kind} 與本套件字典不配對")
    return old


def prepare_shipped_data(data, neural=None, *, public=True):
    """Validate staged data and model pairing without changing provenance or bindings."""
    check_data(data, public)
    return prepare_data_manifest(data, neural)


def seal_data(stage, go, *, public=True):
    """Validate before obfuscation; source metadata and model bindings stay unchanged."""
    neural = stage / "neural"
    prepare_shipped_data(stage / "data", neural if neural.is_dir() else None, public=public)
    subprocess.run([go, "run", "./cmd/0c-compile", "-seal", str(stage / "data")], cwd=ROOT, check=True)


def copy_neural(source, stage):
    """Add the bundled neural models; the selection model must be paired with this bundle's dictionary."""
    source = source.resolve()
    if any(p.is_symlink() for p in source.rglob("*")):
        raise ValueError("神經套件不接受符號連結")
    select = json.loads(regular(source / "select/manifest.json").read_text(encoding="utf-8"))
    if select.get("dictionary_sha256") != digest(stage / "data/manifest.json"):
        raise ValueError("神經選字模型與本套件字典不配對")
    for name in ("sequence_runtime.py", "punct_runtime.py", "punct/manifest.json"):
        regular(source / name)
    shutil.copytree(source, stage / "neural")


def server_version(args, binary):
    """E2E fault package F: the engine reports a different version, so the post-switch health check fails."""
    if args.e2e_fault == "version-mismatch" and binary == "0c-server":
        return args.version + "-fault"
    return args.version


ELF_MACHINE = {"x86_64": 62, "aarch64": 183}


def add_settings(stage, directory, machine=None):
    """附上預先建好的設定程式（scripts/build_settings_linux.py 的輸出：0c-settings 與 NOTICES.txt）。

    不在打包機上建置：Tauri 需要 Rust 與 WebKitGTK 開發套件，這些只放在建置容器裡。回傳要接在第三方聲明
    後面的授權文字。檔案必須是這個套件的 CPU 架構的 ELF，避免把錯的二進位打進去。
    """
    machine = machine or platform.machine()
    program = regular(Path(directory) / "0c-settings")
    notices = regular(Path(directory) / "NOTICES.txt").read_text(encoding="utf-8")
    if not notices.strip():
        raise ValueError("設定程式缺少第三方授權聲明")
    header = program.read_bytes()[:20]
    if len(header) < 20 or header[:4] != b"\x7fELF" or int.from_bytes(header[18:20], "little") != ELF_MACHINE.get(machine):
        raise ValueError(f"設定程式不是 {machine} 的 Linux 執行檔：{program}")
    target = stage / "bin/0c-settings"
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(program, target)
    target.chmod(0o755)
    return notices


def build(args):
    if sys.platform != "linux":
        raise ValueError("目前僅提供 Linux 原生建置")
    if not valid_version(args.version):
        raise ValueError("版本請用 0.1.0-beta.1 形式")
    public = not args.preview
    data = args.data.resolve()
    manifest = check_data(data, public)
    check_licenses()
    release_docs = check_release_docs(getattr(args, "release_docs", None))
    if public:
        dirty = subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT, text=True)
        if dirty.strip():
            raise ValueError("公開包需要乾淨且已提交的程式碼；未提交版本請用 --preview")
    revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    dirty = bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT, text=True).strip())
    args.out.mkdir(parents=True, exist_ok=True)
    arch = {"x86_64": "amd64", "aarch64": "arm64"}.get(platform.machine())
    if not arch:
        raise ValueError("目前只支援 Linux amd64 與 arm64 原生建置")
    name = f"0c-{args.version}-linux-{arch}" + ("-preview" if args.preview else "")
    archive = args.out / (name + ".tar.gz")
    if archive.exists() or archive.with_suffix(archive.suffix + ".sha256").exists():
        raise ValueError(f"輸出已存在，請使用新版本或新目錄：{archive}")
    with tempfile.TemporaryDirectory(prefix="0c-package-") as temp:
        stage = Path(temp) / name
        stage.mkdir()
        def copy(src, dst):
            target = stage / dst
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(regular(src), target)
        # Always rebuild the addon, including changes to flags or the toolchain.
        subprocess.run(["make", "-B", f"SHELL_VERSION={args.version}", "build/lib0c.so"], cwd=PLATFORM, check=True)
        for binary in ("0c-server", "0c-trace", "0c-update", "0c-ibus", "0c-mcp"):
            target = stage / "bin" / binary
            target.parent.mkdir(exist_ok=True)
            tags = ["-tags", "e2etest"] if args.e2etest and binary == "0c-update" else []
            # 0c-update 也是一行安裝下載的 bootstrap，靜態連結才能在各發行版直接執行；
            # IBus 外殼由 ibus-daemon 啟動，同樣不依賴系統的 C 函式庫；0c-mcp 由使用者的 AI 工具啟動（#126）。
            env = dict(os.environ, CGO_ENABLED="0") if binary in ("0c-update", "0c-ibus", "0c-mcp") else None
            subprocess.run([args.go, "build", "-trimpath", *tags, "-ldflags", f"-X main.buildVersion={server_version(args, binary)}", "-o", str(target),
                            f"./cmd/{binary}"], cwd=ROOT, env=env, check=True)
        copy(PLATFORM / "build/lib0c.so", "lib/lib0c.so")
        settings_notices = add_settings(stage, args.settings) if args.settings else ""
        for filename in ("0c-addon.conf", "0c-im.conf"):
            copy(PLATFORM / filename, "config/" + filename)
        copy(PLATFORM / "kimpanel-spacing.js", "config/kimpanel-spacing.js")
        for filename in ("0c-zhuyin.svg", "0c-english.svg"):
            copy(PLATFORM / "icons" / filename, "icons/" + filename)
        for filename in DATA_FILES:
            copy(data / filename, "data/" + filename)
        copy_english(data, stage / "data", args.go)
        if args.neural:
            copy_neural(args.neural, stage)
        seal_data(stage, args.go, public=public)
        copy(ROOT / "scripts/install_package.py", "install.py")
        copy_release_docs(release_docs, stage)
        copy_licenses(stage)
        (stage / "THIRD_PARTY_NOTICES.txt").write_text(
            collect("linux-amd64", stage / "neural" if args.neural else None, go=args.go) + ("\n" + settings_notices if settings_notices else ""),
            encoding="utf-8")
        if not public:
            (stage / "PREVIEW.txt").write_text("內部驗證包，非公開發行版。\n")
        subprocess.run([args.go, "run", "./cmd/0c-cli", "-data", str(stage / "data")],
                       cwd=ROOT, input="", text=True, stdout=subprocess.DEVNULL, check=True)
        release = {"schema": 1, "product": "0c", "name": "靈犀注音", "version": args.version,
                   "channel": "internal-preview" if args.preview else "public-beta",
                   "revision": revision, "dirty": dirty, "machine": platform.machine(),
                   "os": "linux", "arch": arch,
                   "build_platform": platform.platform(), "libc": list(platform.libc_ver()),
                   "data_format": manifest["format"], "data_manifest_sha256": digest(stage / "data/manifest.json"),
                   "files": {str(p.relative_to(stage)): digest(p) for p in sorted(stage.rglob("*")) if p.is_file()}}
        (stage / "release.json").write_text(json.dumps(release, ensure_ascii=False, indent=2) + "\n")
        verify(stage)
        (stage / "SHA256SUMS").write_text("".join(f"{digest(p)}  {p.relative_to(stage)}\n"
                                                  for p in sorted(stage.rglob("*")) if p.is_file() and p.name != "SHA256SUMS"))
        temporary = Path(temp) / archive.name
        with tarfile.open(temporary, "w:gz") as tar:
            tar.add(stage, arcname=name)
        # Copy to a temporary file on the output filesystem, then publish atomically.
        fd, output_temp = tempfile.mkstemp(prefix=".0c-", dir=args.out)
        os.close(fd)
        try:
            shutil.copyfile(temporary, output_temp)
            os.replace(output_temp, archive)
        finally:
            Path(output_temp).unlink(missing_ok=True)
        archive.with_suffix(archive.suffix + ".sha256").write_text(f"{digest(archive)}  {archive.name}\n")
    return archive


def main():
    parser = argparse.ArgumentParser(description="建立靈犀注音 Linux 完整套件；不安裝、不上傳")
    parser.add_argument("--release-docs", required=True, type=Path, help="外部文件目錄，須含 INSTALL.md、PRIVACY.md、UPDATING.md")
    parser.add_argument("--data", required=True, type=Path)
    parser.add_argument("--version", required=True)
    parser.add_argument("--out", type=Path, default=ROOT / "build/release")
    parser.add_argument("--preview", action="store_true", help="產生明確標示為內部驗證的套件")
    parser.add_argument("--e2e-fault", choices=["version-mismatch"], help="E2E 故障套件 F：引擎回報不符的版本（須搭配 --e2etest）")
    parser.add_argument("--e2etest", action="store_true", help="E2E 測試版：0c-update 以 -tags e2etest 建置（信任測試金鑰與測試 CA）；須搭配 --preview")
    parser.add_argument("--go", default="go", help="Go 工具鏈執行檔")
    parser.add_argument("--neural", type=Path, help="scripts/prepare_neural.py 產生的 neural/ 目錄；省略則不內附神經模型")
    parser.add_argument("--settings", type=Path, help="scripts/build_settings_linux.py 產生的目錄（0c-settings 與 NOTICES.txt）；省略則不內附設定程式")
    args = parser.parse_args()
    if args.e2etest and not args.preview:
        parser.error("--e2etest 只能用於 --preview 套件")
    if args.e2e_fault and not args.e2etest:
        parser.error("--e2e-fault 只能用於 --e2etest 套件")
    try:
        print(build(args))
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        print(f"打包失敗：{error}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
