#!/usr/bin/env python3
"""設定程式的冒煙測試：在建置容器的 Xvfb 裡執行真的 0c-settings、0c-update 與 0c-server，逐頁截圖並實際操作。

  python3 scripts/test_settings_ui.py --settings build/settings/linux-amd64/0c-settings --out build/settings/smoke

截圖放在 --out；任何一項 FAIL 結束碼為 1。需要 docker 與已建好的容器映像（build_settings_linux.py 會建立）。
"""
import argparse
import os
from pathlib import Path
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
from build_settings_linux import ROOT, ensure_image  # noqa: E402

ENGINE_VERSION = "0.2.1"


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--settings", required=True, type=Path, help="已建好的 0c-settings")
    parser.add_argument("--out", type=Path, default=ROOT / "build/settings/smoke")
    parser.add_argument("--rust", default="1.99.0")
    parser.add_argument("--go", default="go")
    parser.add_argument("--only", choices=["vocab", "models", "styles"], help="只跑一部分（vocab：我的詞彙；models：模型）")
    args = parser.parse_args()
    out = args.out.resolve()
    bin_dir = out / "bin"
    bin_dir.mkdir(parents=True, exist_ok=True)
    for name in ("0c-update", "0c-server"):
        # 引擎回報固定版本：總覽比對執行中的引擎與收據／核心指標的場景要用（smoke.sh 的 ENGINE_VERSION）。
        ldflags = [f"-ldflags=-X main.buildVersion={ENGINE_VERSION}"] if name == "0c-server" else []
        subprocess.run([args.go, "build", "-trimpath", *ldflags, "-o", str(bin_dir / name), f"./cmd/{name}"], cwd=ROOT, check=True,
                       env=dict(os.environ, CGO_ENABLED="0", GOOS="linux"))
    image = ensure_image(args.rust)
    shots = out / "shots"
    shots.mkdir(exist_ok=True)
    env = {"SETTINGS_BIN": "/in/0c-settings", "UPDATE_BIN": "/bin-dir/0c-update", "SERVER_BIN": "/bin-dir/0c-server",
           "DATA": "/src/testdata/toy", "OUT": "/shots", "HOME": "/tmp/h", "SMOKE_ONLY": args.only or "",
           "ENGINE_VERSION": ENGINE_VERSION}
    # /etc/passwd：以任意 uid 執行時，dbus-launch 需要查得到使用者。
    command = ["docker", "run", "--rm", "--user", f"{os.getuid()}:{os.getgid()}",
               "-v", "/etc/passwd:/etc/passwd:ro", "-v", f"{ROOT}:/src:ro", "-v", f"{args.settings.resolve()}:/in/0c-settings:ro", "-v", f"{bin_dir}:/bin-dir:ro", "-v", f"{shots}:/shots",
               *[x for k, v in env.items() for x in ("-e", f"{k}={v}")], image, "bash", "/src/platforms/settings/test/smoke.sh"]
    result = subprocess.run(command, text=True, capture_output=True)
    print(result.stdout)
    if result.stderr.strip():
        print(result.stderr, file=sys.stderr)
    sys.exit(0 if result.returncode == 0 and "SMOKE_RESULT=PASS" in result.stdout else 1)


if __name__ == "__main__":
    main()
