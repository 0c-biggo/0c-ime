#!/usr/bin/env python3
"""Collect third-party license texts for what a bundle actually ships.

Sources are the shipped components themselves: the Go toolchain and modules linked into the
binaries, the bundled CPython and its packages, Rust crates in the Windows build, and headers
compiled into the Fcitx addon. The dictionary's source attributions stay in data/NOTICE.txt.
"""

import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
LICENSE_NAMES = ("LICENSE", "LICENCE", "COPYING", "NOTICE")
NLOHMANN_MIT = """MIT License

Copyright (c) 2013-2023 Niels Lohmann

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
"""


def license_files(directory):
    found = []
    for path in sorted(Path(directory).iterdir()):
        if path.is_file() and path.name.upper().startswith(LICENSE_NAMES):
            found.append(path)
    return found


def section(title, texts):
    body = "\n\n".join(t.strip() for t in texts if t.strip())
    if not body:
        raise ValueError(f"找不到授權文字：{title}")
    return f"{'=' * 78}\n{title}\n{'=' * 78}\n\n{body}\n"


def read(path):
    return Path(path).read_text(encoding="utf-8", errors="replace")


def go_sections(go, goos, packages):
    env = dict(os.environ, GOOS=goos, GOARCH="arm64" if goos == "darwin" else "amd64", CGO_ENABLED="0")
    goroot = subprocess.check_output([go, "env", "GOROOT"], text=True, encoding="utf-8", env=env).strip()
    version = subprocess.check_output([go, "env", "GOVERSION"], text=True, encoding="utf-8", env=env).strip()
    # Homebrew 的 GOROOT 是 Cellar/go/<版本>/libexec，LICENSE 在上一層。
    license = next(p for p in (Path(goroot) / "LICENSE", Path(goroot).parent / "LICENSE") if p.is_file())
    out = [section(f"Go 工具鏈與標準函式庫（{version}）", [read(license)])]
    listing = subprocess.check_output(
        [go, "list", "-deps", "-f", "{{with .Module}}{{if not .Main}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}{{end}}", *packages],
        text=True, encoding="utf-8", env=env, cwd=ROOT)
    for line in sorted(set(filter(None, listing.splitlines()))):
        path, version, directory = line.split("\t")
        out.append(section(f"Go 模組 {path} {version}", [read(p) for p in license_files(directory)]))
        if path == "github.com/clipperhouse/uax29/v2":
            out.append(section("Unicode 17 grapheme properties（uax29 產生的字元分段資料）", [read(ROOT / "legal/UNICODE-NOTICE.txt")]))
    return out


def python_sections(neural, platform):
    python = Path(neural) / "python"
    if platform == "windows-amd64":
        cpython, site = python / "LICENSE.txt", python / "Lib/site-packages"
    else:
        cpython, site = python / "lib/python3.14/LICENSE.txt", python / "lib/python3.14/site-packages"
    out = [section("CPython 3.14", [read(cpython)])]
    crt = [p.name for p in sorted(python.glob("msvcp140*.dll"))]
    if crt:
        # prepare_neural.py 隨 onnxruntime 附上的 C++ 執行階段（#80）。
        out.append(section("Microsoft Visual C++ 執行階段（" + "、".join(crt) + "）", [
            "Microsoft Visual Studio 2022 可轉散發程式碼，未經修改隨程式散布；"
            "見 https://learn.microsoft.com/visualstudio/releases/2022/redistribution#visual-c-runtime-files"]))
    for info in sorted(site.glob("*.dist-info")):
        name = info.name.removesuffix(".dist-info").replace("-", " ", 1)
        texts = [read(p) for p in sorted((info / "licenses").rglob("*")) if p.is_file()] if (info / "licenses").is_dir() else []
        package = site / info.name.split("-")[0]
        texts += [read(p) for p in sorted(package.glob("*")) if p.is_file() and
                  (p.name.upper().startswith(LICENSE_NAMES) or p.name == "ThirdPartyNotices.txt")]
        out.append(section(f"Python 套件 {name}", texts))
    return out


def rust_sections(cargo, workspace, target="x86_64-pc-windows-msvc"):
    meta = json.loads(subprocess.check_output([cargo, "metadata", "--format-version", "1", "--locked",
                                               "--filter-platform", target], text=True, encoding="utf-8", cwd=workspace))
    used = {node["id"] for node in meta["resolve"]["nodes"]}
    out = []
    for package in sorted(meta["packages"], key=lambda p: (p["name"], p["version"])):
        if package["id"] not in used or package.get("source") is None:
            continue  # workspace crates are ours
        directory = Path(package["manifest_path"]).parent
        texts = [f"授權：{package.get('license') or '見下列檔案'}"] + [read(p) for p in license_files(directory)]
        out.append(section(f"Rust crate {package['name']} {package['version']}", texts))
    return out


def collect(platform, neural=None, go="go", cargo=None):
    """Return the notices text for a bundle; raises when an expected license text is missing."""
    header = ("靈犀注音（0c）第三方元件聲明\n\n"
              "自有程式依套件內 LICENSE 授權，授權範圍見 legal/README.md 與 TERMS.md。以下是隨套件出貨的第三方元件及其授權條款；"
              "字典資料的來源與授權另見 data/NOTICE.txt。\n\n")
    if platform == "windows-amd64":
        parts = go_sections(go, "windows", ["./cmd/0c-server", "./cmd/0c-update"])
        parts += rust_sections(cargo, ROOT / "platforms/windows")
    elif platform == "macos-arm64":
        parts = go_sections(go, "darwin", ["./cmd/0c-server", "./cmd/0c-update"])
    else:
        parts = go_sections(go, "linux", ["./cmd/0c-server", "./cmd/0c-update", "./cmd/0c-trace"])
        parts.append(section("nlohmann/json 3.11.3（Fcitx5 外殼）", [NLOHMANN_MIT]))
    if neural:
        parts += python_sections(neural, platform)
    return header + "\n".join(parts)


if __name__ == "__main__":
    # 只輸出某個 Rust 工作區的授權文字（設定程式的建置容器用）：collect_notices.py rust --workspace DIR --target TRIPLE
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument("what", choices=["rust"])
    parser.add_argument("--workspace", required=True, type=Path)
    parser.add_argument("--target", required=True)
    parser.add_argument("--cargo", default="cargo")
    options = parser.parse_args()
    print("\n".join(rust_sections(options.cargo, options.workspace, options.target)))
