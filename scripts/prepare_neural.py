#!/usr/bin/env python3
"""Assemble the bundled neural/ directory: portable Python, pinned wheels, workers and model packages.

The result sits next to the paired system dictionary in a release bundle; 0c-server finds it there.
Downloads are pinned by SHA-256 and cached; nothing is installed on this machine.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parents[1]
WORKERS = ROOT / "platforms/neural/runtime"
PYTHON_VERSION = "3.14"
# CPython 3.14.7 for both platforms; python.org stopped shipping Windows binaries for 3.12.
RUNTIMES = {
    "linux-amd64": {
        "python": ("https://github.com/astral-sh/python-build-standalone/releases/download/20260924/"
                   "cpython-3.14.7%2B20260924-x86_64-unknown-linux-gnu-install_only_stripped.tar.gz",
                   "bd0d0568ccded07bbf1c87727230dc5dd0187e706a87da23c4de78388a229b78"),
        "wheels": [
            ("https://files.pythonhosted.org/packages/45/8f/9beacf79ca7c650688ad0baa80931adb988fe6e6e5d5903c23cc3dbd70eb/"
             "numpy-2.5.3-cp314-cp314-manylinux_2_27_x86_64.manylinux_2_28_x86_64.whl",
             "b0521d0f4aebb6e06189451025fa17a913287b13c03d5fe05c017333b654ea5b"),
            ("https://files.pythonhosted.org/packages/f1/38/8138eed225c5bc6ddfc05879ecac7dacc63c34b9b6f99be72839c1f6dc49/"
             "onnxruntime-1.30.0-cp314-cp314-manylinux_2_28_x86_64.whl",
             "8b611d24db2954545ce6bd9acd4670183cb368e4642450de7a9ab6474eb374ec"),
        ],
    },
    "macos-arm64": {
        # onnxruntime 1.30.0 ships macOS arm64 wheels for 14.0 and later only; older macOS falls back
        # to the statistical engine because the worker cannot start.
        "python": ("https://github.com/astral-sh/python-build-standalone/releases/download/20260924/"
                   "cpython-3.14.7%2B20260924-aarch64-apple-darwin-install_only_stripped.tar.gz",
                   "f0fb6b233971672dd7add4efa90f8c209a63253e457ffabd0148b9f734638319"),
        "wheels": [
            ("https://files.pythonhosted.org/packages/94/75/4640d2d6e4b64a049e48425a82728a41ef4adb61332d2cba68055774878b/"
             "numpy-2.5.3-cp314-cp314-macosx_14_0_arm64.whl",
             "adc1ada2662f8a5f960b8a10d9986897e7499ef07e06d4cfe7197f8cce923c07"),
            ("https://files.pythonhosted.org/packages/6a/03/05c9a9234688757d2876ddecf80bba908561ee11debf125bc1a427ae6f48/"
             "onnxruntime-1.30.0-cp314-cp314-macosx_14_0_arm64.whl",
             "8b6169c16a48429890d2f4a0c774ebf54dfe9066a998514aad0518a16d398547"),
        ],
    },
    "windows-amd64": {
        "python": ("https://www.python.org/ftp/python/3.14.7/python-3.14.7-embed-amd64.zip",
                   "d297e5ff019966817ad8502465176139f2d3d840fa4ed84b13bed399a6ab1f15"),
        "wheels": [
            ("https://files.pythonhosted.org/packages/a4/73/d2c08231e4fde7e415501fd02c715d96e98599b2d8384445933944152984/"
             "numpy-2.5.3-cp314-cp314-win_amd64.whl",
             "2c25dfa72943e4336ddb6b0ee4277b47a0c85bede0807530ec68103bf58e2c10"),
            ("https://files.pythonhosted.org/packages/4d/0a/748b86000fbf9518b5dfc5bdc1b924eeaca976203740fd9d30649e568ee6/"
             "onnxruntime-1.30.0-cp314-cp314-win_amd64.whl",
             "3bdd1752a8502ac1a7ccc6e878d16db6943df574d54ed7f01058a6806ae05be4"),
        ],
        # onnxruntime 需要 MSVCP140.dll／MSVCP140_1.dll；embeddable Python 只帶 vcruntime140*.dll，numpy 的 msvcp140
        # 改過名只給自己用。沒裝 Visual C++ 可轉散發套件的電腦（例如精簡版 Windows）會載入失敗、退回統計選字（#80），
        # 系統裝的版本太舊時 onnxruntime 也可能當掉。這個 wheel 只取這兩個 Microsoft 簽章的 DLL，放在 python.exe 旁，
        # 擴充模組載入時優先於 System32 找到。14.44 的匯出涵蓋 onnxruntime 1.30.0 用到的全部符號，所需的 vcruntime140
        # 匯出也都在內附的 14.51 版裡。
        "crt": ("https://files.pythonhosted.org/packages/d2/48/7fb574a4bfa697574a7dc7105bc15ef6882125e7c77c794c5e73429de832/"
                "msvc_runtime-14.44.35112-cp314-cp314-win_amd64.whl",
                "af179a6c552070e660493765efd5856283af3fed971ddc3577203a7d3f1ee8e7",
                ("msvcp140.dll", "msvcp140_1.dll")),
    },
}
# Not needed by the workers; removing them keeps the bundle smaller and reduces attack surface.
TRIM = ("lib/python3.14/site-packages/pip", "lib/python3.14/ensurepip", "lib/python3.14/idlelib",
        "lib/python3.14/tkinter", "lib/python3.14/turtledemo", "lib/tcl9", "lib/tcl9.0", "lib/tk9.0",
        "lib/libtcl9.0.so", "lib/libtcl9tk9.0.so", "lib/libtcl9.0.dylib", "lib/libtcl9tk9.0.dylib",
        "lib/itcl4.3.8", "lib/thread3.0.6", "lib/pkgconfig", "include", "share")


def digest(path):
    h = hashlib.sha256()
    with open(path, "rb") as stream:
        for block in iter(lambda: stream.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def fetch(url, sha256, cache, local=None):
    """Return a verified file: from --wheels/--python-archive overrides, the cache, or the network."""
    name = url.rsplit("/", 1)[-1].replace("%2B", "+")
    target = cache / name
    if local:
        target = Path(local)
    elif not target.exists():
        cache.mkdir(parents=True, exist_ok=True)
        with tempfile.NamedTemporaryFile(dir=cache, delete=False) as out, urllib.request.urlopen(url) as reply:
            shutil.copyfileobj(reply, out)
        os.replace(out.name, target)
    if digest(target) != sha256:
        raise ValueError(f"下載檔雜湊不符：{name}")
    return target


def install_python(platform, archive, dest):
    if platform in ("linux-amd64", "macos-arm64"):
        # The archive's top-level directory is python/, which is the destination name.
        with tarfile.open(archive) as tar:
            tar.extractall(dest.parent, filter="data")
        for relative in TRIM:
            path = dest / relative
            shutil.rmtree(path) if path.is_dir() else path.unlink(missing_ok=True)
        for info in (dest / "lib/python3.14/site-packages").glob("pip-*.dist-info"):
            shutil.rmtree(info)
        # Bundles and installers reject links; keep only the interpreter, named as 0c-server expects.
        for path in sorted(dest.rglob("*"), reverse=True):
            if path.is_symlink():
                path.unlink()
        for path in (dest / "bin").iterdir():
            if path.name != "python3.14":
                path.unlink()
        (dest / "bin/python3.14").rename(dest / "bin/python3")
        return dest / "lib/python3.14/site-packages"
    with zipfile.ZipFile(archive) as z:
        z.extractall(dest)
    site = dest / "Lib/site-packages"
    site.mkdir(parents=True)
    # The embeddable distribution fixes sys.path with its ._pth file; add the bundled packages.
    (dest / "python314._pth").write_text("python314.zip\n.\nLib\\site-packages\n", encoding="ascii")
    return site


def install_crt(wheel, names, dest):
    """Copy only the named C++ runtime DLLs from the wheel's data directory next to python.exe."""
    with zipfile.ZipFile(wheel) as z:
        for name in names:
            members = [m for m in z.namelist() if m.endswith(".data/data/" + name)]
            if len(members) != 1:
                raise ValueError(f"C++ 執行階段 wheel 缺少 {name}")
            (dest / name).write_bytes(z.read(members[0]))


def install_wheel(wheel, site):
    with zipfile.ZipFile(wheel) as z:
        for member in z.namelist():
            parts = Path(member).parts
            if member.startswith("/") or ".." in parts:
                raise ValueError(f"wheel 路徑不安全：{member}")
            # Package test suites are not needed at runtime.
            if "tests" in parts[:-1] and parts[0] in ("numpy", "onnxruntime"):
                continue
            z.extract(member, site)


def precompile(tree, compiler):
    """Bake hash-based bytecode that Python uses without checking source times.

    Workers run with -B, so installed files never change after installation (receipts stay valid).
    Bytecode is platform independent for one CPython version; any 3.14.7 interpreter can build it.
    """
    for cache in tree.rglob("__pycache__"):
        shutil.rmtree(cache)
    subprocess.run([str(compiler), "-I", "-m", "compileall", "-q", "-f", "-j", "0",
                    "--invalidation-mode", "unchecked-hash", str(tree)], check=True)


def check_package(path, kind):
    manifest = json.loads((path / "manifest.json").read_text(encoding="utf-8"))
    files = manifest.get("files", {})
    if set(files) != {"model.onnx", "vocab.json"}:
        raise ValueError(f"{kind} 套件缺少模型檔")
    for name, expected in files.items():
        if digest(path / name) != expected:
            raise ValueError(f"{kind} 模型雜湊不符：{name}")
    return manifest


def build(args):
    runtime = RUNTIMES[args.platform]
    select = check_package(args.select, "選字")
    check_package(args.punct, "補標點")
    dictionary = digest(args.dictionary / "manifest.json")
    if select.get("dictionary_sha256") != dictionary:
        raise ValueError(f"選字模型配對的字典 {select.get('dictionary_sha256')} 與打包字典 {dictionary} 不同")
    out = args.out
    if out.exists():
        raise ValueError(f"輸出已存在：{out}")
    stage = Path(tempfile.mkdtemp(prefix=".neural-", dir=out.parent))
    try:
        archive = fetch(*runtime["python"], args.cache, args.python_archive)
        site = install_python(args.platform, archive, stage / "python")
        for url, sha256 in runtime["wheels"]:
            local = next((w for w in args.wheel if Path(w).name == url.rsplit("/", 1)[-1]), None)
            install_wheel(fetch(url, sha256, args.cache, local), site)
        if "crt" in runtime:
            url, sha256, names = runtime["crt"]
            local = next((w for w in args.wheel if Path(w).name == url.rsplit("/", 1)[-1]), None)
            install_crt(fetch(url, sha256, args.cache, local), names, stage / "python")
        if args.platform == "linux-amd64":
            precompile(stage / "python", stage / "python/bin/python3")
        elif args.platform == "macos-arm64":
            # Bytecode is platform independent; compile the macOS tree with the pinned Linux interpreter
            # when building on Linux, or with the bundled interpreter itself on macOS.
            if sys.platform == "darwin":
                precompile(stage / "python", stage / "python/bin/python3")
            else:
                with tempfile.TemporaryDirectory(prefix=".compiler-", dir=out.parent) as temp:
                    compiler_archive = fetch(*RUNTIMES["linux-amd64"]["python"], args.cache, args.compiler_archive)
                    install_python("linux-amd64", compiler_archive, Path(temp) / "python")
                    precompile(stage / "python", Path(temp) / "python/bin/python3")
        else:
            # The Windows stdlib ships as bytecode inside python314.zip; compile the packages with the
            # pinned Linux interpreter of the same CPython release.
            with tempfile.TemporaryDirectory(prefix=".compiler-", dir=out.parent) as temp:
                compiler_archive = fetch(*RUNTIMES["linux-amd64"]["python"], args.cache, args.compiler_archive)
                install_python("linux-amd64", compiler_archive, Path(temp) / "python")
                precompile(site, Path(temp) / "python/bin/python3")
        for name in ("sequence_runtime.py", "punct_runtime.py"):
            shutil.copy2(WORKERS / name, stage / name)
        shutil.copytree(args.select, stage / "select")
        shutil.copytree(args.punct, stage / "punct")
        (stage / "README.txt").write_text(
            "靈犀注音內附的神經選字與提交時補標點：可攜 Python " + PYTHON_VERSION +
            "（numpy、onnxruntime）、worker 與模型。只在本機執行，不連網。\n"
            "切換：0c-update set selection statistical|neural、0c-update set punctuation on|off。\n",
            encoding="utf-8")
        os.replace(stage, out)
    except BaseException:
        shutil.rmtree(stage, ignore_errors=True)
        raise
    return out


def main():
    parser = argparse.ArgumentParser(description="組出套件內附的 neural/ 目錄（可攜 Python、worker、模型）")
    parser.add_argument("--platform", required=True, choices=sorted(RUNTIMES))
    parser.add_argument("--select", required=True, type=Path, help="選字模型套件目錄")
    parser.add_argument("--punct", required=True, type=Path, help="補標點模型套件目錄")
    parser.add_argument("--dictionary", required=True, type=Path, help="將一起出貨的系統字典目錄（核對配對）")
    parser.add_argument("--out", required=True, type=Path, help="輸出目錄（不可已存在）")
    parser.add_argument("--cache", type=Path, default=ROOT / "build/cache/neural", help="下載快取")
    parser.add_argument("--python-archive", help="已下載的 Python 壓縮檔（仍核對雜湊）")
    parser.add_argument("--wheel", action="append", default=[], help="已下載的 wheel（仍核對雜湊）")
    parser.add_argument("--compiler-archive", help="Windows 建置時編譯 bytecode 用的 Linux Python 壓縮檔（仍核對雜湊）")
    args = parser.parse_args()
    try:
        print(build(args))
    except (OSError, ValueError, KeyError) as error:
        print(f"準備神經套件失敗：{error}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
