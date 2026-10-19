#!/usr/bin/env python3
"""在 Windows x64 建置機上建置「靈犀注音設定」（Tauri），輸出 0c-settings.exe 與第三方授權聲明。

scripts/package_windows.py --settings 只收這裡的輸出：版本資訊與 Authenticode 簽章由打包腳本在整個套件
完成時一起做，所以這裡只產生未簽章的執行檔。建置用機器上已安裝的 Rust（MSVC 工具鏈，需要 1.90 以上，
Tauri 2.12 的要求）；不安裝或修改任何工具鏈、PATH 或 shell 設定檔，編譯結果與快取放在 --out 旁。

  python scripts\\build_settings_windows.py --version 0.1.0-dev.43
  python scripts\\build_settings_windows.py --version 0.1.0-dev.43 --test   # 另外跑 Rust 單元測試
"""
import argparse
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
APP = ROOT / "platforms/settings"
TARGET = "x86_64-pc-windows-msvc"
MINIMUM_RUST = (1, 90)


def rust_version(cargo):
    """cargo --version 的 (主, 次) 版本；讀不出來時是 None。"""
    text = subprocess.run([cargo, "--version"], capture_output=True, text=True).stdout
    match = re.match(r"cargo (\d+)\.(\d+)", text)
    return (int(match.group(1)), int(match.group(2))) if match else None


def build(args):
    if sys.platform != "win32" or platform.machine().lower() not in ("amd64", "x86_64"):
        raise ValueError("Windows 設定程式需在 Windows x64 的建置環境建置")
    if not re.match(r"^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$", args.version):
        raise ValueError("版本請用 0.1.0-beta.1 形式")
    version = rust_version(args.cargo)
    if version is None or version < MINIMUM_RUST:
        raise ValueError(f"需要 Rust {MINIMUM_RUST[0]}.{MINIMUM_RUST[1]} 以上（目前：{version}）")
    out = args.out.resolve()
    cache = out / "cache"
    work = cache / "work/settings"
    if work.exists():
        shutil.rmtree(work)
    work.parent.mkdir(parents=True, exist_ok=True)
    shutil.copytree(APP, work, ignore=shutil.ignore_patterns("target", "gen"))
    # PYTHONUTF8：授權文字含 Big5 編碼不了的字元，子行程的輸出管道不能用主控台的預設編碼。
    env = dict(os.environ, ZC_BUILD_VERSION=args.version, CARGO_TARGET_DIR=str(cache / "target"), PYTHONUTF8="1")
    # --locked：建置結果由 Cargo.lock 決定，鎖定檔需更新時直接失敗。
    subprocess.run([args.cargo, "build", "--release", "--locked", "--target", TARGET, "--features", "tauri/custom-protocol"],
                   cwd=work, env=env, check=True)
    if args.test:
        subprocess.run([args.cargo, "test", "--release", "--locked"], cwd=work, env=env, check=True)
    target_dir = out / "windows-amd64"
    target_dir.mkdir(parents=True, exist_ok=True)
    shutil.copy2(cache / "target" / TARGET / "release/0c-settings.exe", target_dir / "0c-settings.exe")
    notices = subprocess.run([sys.executable, str(ROOT / "scripts/collect_notices.py"), "rust", "--workspace", str(work),
                              "--target", TARGET, "--cargo", args.cargo],
                             check=True, env=env, capture_output=True, text=True, encoding="utf-8").stdout
    (target_dir / "NOTICES.txt").write_text(notices, encoding="utf-8")
    if (work / "Cargo.lock").read_bytes() != (APP / "Cargo.lock").read_bytes():
        raise ValueError("建置改動了 Cargo.lock；請先更新並提交鎖定檔")
    return target_dir


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--version", required=True, help="顯示在「關於」的設定程式版本，通常與套件版本相同")
    parser.add_argument("--out", type=Path, default=ROOT / "build/settings")
    parser.add_argument("--cargo", default="cargo")
    parser.add_argument("--test", action="store_true", help="另外執行 Rust 單元測試")
    args = parser.parse_args()
    try:
        print(build(args))
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        print(f"建置失敗：{error}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
