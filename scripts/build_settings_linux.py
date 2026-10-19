#!/usr/bin/env python3
"""在 Ubuntu 22.04 容器裡建置「靈犀注音設定」（Tauri），輸出 0c-settings 與第三方授權聲明。

打包機不需要 Rust 或 WebKitGTK 開發套件：scripts/package_release.py --settings 只收這裡的輸出。
建置不改任何 shell 設定檔，也不寫進家目錄；快取（Cargo 套件與編譯結果）放在輸出目錄旁。

  python3 scripts/build_settings_linux.py --version 0.1.0-dev.43
  python3 scripts/build_settings_linux.py --version 0.1.0-dev.43 --test   # 另外跑 Rust 單元測試
"""
import argparse
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
APP = ROOT / "platforms/settings"
IMAGE = "zc-settings-build"
TARGETS = {"x86_64": "x86_64-unknown-linux-gnu", "aarch64": "aarch64-unknown-linux-gnu"}


def docker(*args, **kwargs):
    return subprocess.run(["docker", *args], check=True, **kwargs)


def ensure_image(rust):
    tag = f"{IMAGE}:{rust}"
    have = subprocess.run(["docker", "image", "inspect", tag], capture_output=True).returncode == 0
    if not have:
        docker("build", "--build-arg", f"RUST_VERSION={rust}", "-t", tag, "-f", str(ROOT / "scripts/settings-build.Dockerfile"), str(ROOT / "scripts"))
    return tag


def build(args):
    machine = platform.machine()
    if machine not in TARGETS:
        raise ValueError("目前只支援 x86_64 與 aarch64 的 Linux 建置")
    if not shutil.which("docker"):
        raise ValueError("需要 docker")
    out = args.out.resolve()
    cache = out / "cache"
    for sub in ("cargo", "target", "work"):
        (cache / sub).mkdir(parents=True, exist_ok=True)
    image = ensure_image(args.rust)
    work = cache / "work/settings"
    if work.exists():
        shutil.rmtree(work)
    shutil.copytree(APP, work, ignore=shutil.ignore_patterns("target", "gen"))
    uid, gid = os.getuid(), os.getgid()
    script = [
        "set -e",
        "cd /work/settings",
        f"export ZC_BUILD_VERSION={args.version}",
        "export CARGO_HOME=/cache/cargo CARGO_TARGET_DIR=/cache/target HOME=/tmp/home && mkdir -p $HOME",
        # 沒有 Cargo.lock 時（第一次）才允許解析；之後一律 --locked，建置結果由 lock 檔決定。
        'LOCKED=--locked; [ -f Cargo.lock ] || LOCKED=""',
        "cargo build --release $LOCKED --features tauri/custom-protocol",
    ]
    if args.test:
        script.append("cargo test --release $LOCKED")
    script += [
        "mkdir -p /out",
        "install -m 0755 /cache/target/release/0c-settings /out/0c-settings",
        f"python3 /src/scripts/collect_notices.py rust --workspace /work/settings --target {TARGETS[machine]} > /out/NOTICES.txt",
        "cp Cargo.lock /out/Cargo.lock",
    ]
    target_dir = out / f"linux-{ 'amd64' if machine == 'x86_64' else 'arm64' }"
    target_dir.mkdir(parents=True, exist_ok=True)
    docker("run", "--rm", "--user", f"{uid}:{gid}",
           "-v", f"{ROOT}:/src:ro", "-v", f"{cache}:/cache", "-v", f"{target_dir}:/out",
           "-v", f"{cache / 'work'}:/work", image, "bash", "-c", "\n".join(script))
    lock = target_dir / "Cargo.lock"
    if not (APP / "Cargo.lock").exists():
        shutil.copy2(lock, APP / "Cargo.lock")
        print(f"已產生 {APP / 'Cargo.lock'}，請一併提交。")
    elif lock.read_bytes() != (APP / "Cargo.lock").read_bytes():
        raise ValueError("建置改動了 Cargo.lock；請先更新並提交鎖定檔")
    lock.unlink()
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
