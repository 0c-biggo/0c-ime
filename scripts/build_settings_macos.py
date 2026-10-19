#!/usr/bin/env python3
"""在 Apple Silicon 的 macOS 上建置「靈犀注音設定」（Tauri），輸出 0c-settings.app 與第三方授權聲明。

scripts/package_macos.py --settings 只收這裡的輸出，所以打包機不必自己裝 Rust。Rust 工具鏈裝在輸出目錄旁的
快取（--out/cache），不改任何 shell 設定檔（rustup 一律 --no-modify-path），不寫進家目錄。

  python3 scripts/build_settings_macos.py --version 0.1.0-dev.43
  python3 scripts/build_settings_macos.py --version 0.1.0-dev.43 --test   # 另外跑 Rust 單元測試

App 在這裡只做 ad-hoc 簽章（讓本機可以跑）；正式簽章與公證由 package_macos.py 在整個 0c.app 完成後做，
巢狀的 0c-settings.app 會先被簽成 bundle。需與 Python 3.9（macOS 內建）相容。
"""
import argparse
import hashlib
import os
from pathlib import Path
import platform
import plistlib
import re
import shutil
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
from macos_version import bundle_version  # noqa: E402

ROOT = Path(__file__).resolve().parents[1]
APP = ROOT / "platforms/settings"
ICON = ROOT / "platforms/macos/Icon/AppIcon.icns"
TARGET = "aarch64-apple-darwin"
RUSTUP_URL = "https://static.rust-lang.org/rustup/dist/" + TARGET + "/rustup-init"
BUNDLE_ID = "com.biggo.0c.settings"
NAME = "靈犀注音設定"
EXECUTABLE = "0c-settings"
APP_NAME = EXECUTABLE + ".app"
MINIMUM_SYSTEM = "13.0"


def run(cmd, **kwargs):
    return subprocess.run([str(c) for c in cmd], check=True, **kwargs)


def info_plist(version):
    """Info.plist 的內容。CFBundleShortVersionString 只放數字部分（預覽版後綴不合 macOS 的版本格式），
    完整版本由程式內的 ZC_BUILD_VERSION 顯示。LSMultipleInstancesProhibited：重複啟動時由系統把既有的視窗帶到前面。"""
    match = re.match(r"^(\d+\.\d+\.\d+)", version)
    if not match:
        raise ValueError("版本請用 0.1.0-beta.1 形式")
    return {
        "CFBundleDevelopmentRegion": "zh_TW",
        "CFBundleDisplayName": NAME,
        "CFBundleName": NAME,
        "CFBundleExecutable": EXECUTABLE,
        "CFBundleIconFile": "AppIcon",
        "CFBundleIdentifier": BUNDLE_ID,
        "CFBundlePackageType": "APPL",
        "CFBundleShortVersionString": match.group(1),
        "CFBundleVersion": bundle_version(version),  # 隨版本遞增，見 macos_version.py（#77）
        "LSMinimumSystemVersion": MINIMUM_SYSTEM,
        "LSApplicationCategoryType": "public.app-category.utilities",
        "LSMultipleInstancesProhibited": True,
        "NSHighResolutionCapable": True,
        "NSPrincipalClass": "NSApplication",
    }


def assemble(app, binary, version):
    """把編好的執行檔包成 App：Info.plist、圖示與執行檔，全部是一般檔案。"""
    if app.exists():
        shutil.rmtree(app)
    (app / "Contents/MacOS").mkdir(parents=True)
    (app / "Contents/Resources").mkdir()
    shutil.copy2(binary, app / "Contents/MacOS" / EXECUTABLE)
    (app / "Contents/MacOS" / EXECUTABLE).chmod(0o755)
    shutil.copy2(ICON, app / "Contents/Resources/AppIcon.icns")
    with open(app / "Contents/Info.plist", "wb") as handle:
        plistlib.dump(info_plist(version), handle)
    # 權限不跟著來源或 umask 走：0c-update 會拒絕群組或其他人可寫的檔案。
    for path in app.rglob("*"):
        path.chmod(0o755 if path.is_dir() or path.name == EXECUTABLE else 0o644)
    run(["plutil", "-lint", app / "Contents/Info.plist"], capture_output=True)
    run(["codesign", "--force", "--sign", "-", app], capture_output=True)
    run(["codesign", "--verify", "--strict", app], capture_output=True)


def toolchain(cache, rust):
    """安裝（或沿用）快取裡指定版本的 Rust，回傳 (環境變數, cargo 路徑)。rustup-init 直接下載並核對官方公布的雜湊。"""
    cargo_home, rustup_home = cache / "cargo", cache / "rustup"
    env = dict(os.environ, CARGO_HOME=str(cargo_home), RUSTUP_HOME=str(rustup_home))
    cargo = cargo_home / "bin/cargo"
    env["PATH"] = str(cargo_home / "bin") + os.pathsep + env.get("PATH", "")
    if cargo.exists() and subprocess.run([str(cargo), "--version"], env=env, capture_output=True, text=True).stdout.split()[1:2] == [rust]:
        return env, cargo
    download = cache / "download"
    download.mkdir(parents=True, exist_ok=True)
    init = download / "rustup-init"
    for url, name in ((RUSTUP_URL, "rustup-init"), (RUSTUP_URL + ".sha256", "rustup-init.sha256")):
        run(["curl", "--proto", "=https", "--tlsv1.2", "-fsSL", "-o", download / name, url])
    expected = (download / "rustup-init.sha256").read_text().split()[0]
    if hashlib.sha256(init.read_bytes()).hexdigest() != expected:
        raise ValueError("rustup-init 的雜湊與官方公布的不符")
    init.chmod(0o755)
    run([init, "-y", "--no-modify-path", "--profile", "minimal", "--default-toolchain", rust], env=env, capture_output=True)
    return env, cargo


def build(args):
    if sys.platform != "darwin" or platform.machine() != "arm64":
        raise ValueError("macOS 設定程式需在 Apple Silicon 的 macOS 原生建置")
    info_plist(args.version)  # 版本格式
    out = args.out.resolve()
    cache = out / "cache"
    cache.mkdir(parents=True, exist_ok=True)
    env, cargo = toolchain(cache, args.rust)
    work = cache / "work/settings"
    if work.exists():
        shutil.rmtree(work)
    shutil.copytree(APP, work, ignore=shutil.ignore_patterns("target", "gen"))
    env.update(ZC_BUILD_VERSION=args.version, CARGO_TARGET_DIR=str(cache / "target"), MACOSX_DEPLOYMENT_TARGET=MINIMUM_SYSTEM)
    # --locked：建置結果由 Cargo.lock 決定，鎖定檔需更新時直接失敗。
    run([cargo, "build", "--release", "--locked", "--features", "tauri/custom-protocol"], cwd=work, env=env)
    if args.test:
        run([cargo, "test", "--release", "--locked"], cwd=work, env=env)
    target_dir = out / "macos-arm64"
    target_dir.mkdir(parents=True, exist_ok=True)
    assemble(target_dir / APP_NAME, cache / "target/release" / EXECUTABLE, args.version)
    notices = subprocess.run([sys.executable, str(ROOT / "scripts/collect_notices.py"), "rust", "--workspace", str(work),
                              "--target", TARGET, "--cargo", str(cargo)],
                             check=True, env=env, capture_output=True, text=True, encoding="utf-8").stdout
    (target_dir / "NOTICES.txt").write_text(notices, encoding="utf-8")
    if (work / "Cargo.lock").read_bytes() != (APP / "Cargo.lock").read_bytes():
        raise ValueError("建置改動了 Cargo.lock；請先更新並提交鎖定檔")
    return target_dir


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--version", required=True, help="顯示在「關於」的設定程式版本，通常與套件版本相同")
    parser.add_argument("--out", type=Path, default=ROOT / "build/settings")
    parser.add_argument("--rust", default="1.99.0", help="Rust 工具鏈版本（Tauri 2.12 需要 1.90 以上）")
    parser.add_argument("--test", action="store_true", help="另外執行 Rust 單元測試")
    args = parser.parse_args()
    try:
        print(build(args))
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        print(f"建置失敗：{error}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
