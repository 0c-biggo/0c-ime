#!/usr/bin/env python3
"""設定程式的冒煙測試：在建置容器的 Xvfb 裡執行真的 0c-settings、0c-update 與 0c-server，逐頁截圖並實際操作。

  python3 scripts/test_settings_ui.py --settings build/settings/linux-amd64/0c-settings --out build/settings/smoke

截圖放在 --out；任何一項 FAIL 結束碼為 1，失敗當下的現場（視窗樹、行程、app log、整個畫面）在 --out/shots/failures。
需要 docker 與已建好的容器映像（build_settings_linux.py 會建立）。

每次執行有自己的容器（自己的 Xvfb、D-Bus 與 /tmp），可以同時跑好幾份；同一個 --out 一次只能一份
（截圖與 bin 會互相蓋掉，#366），第二份直接拒絕並說明。
"""
import argparse
import fcntl
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parent))
from build_settings_linux import ROOT, ensure_image  # noqa: E402

ENGINE_VERSION = "0.2.1"


def lock_out(out):
    """鎖住輸出目錄到這個行程結束；已經有別的一份在用就結束並說明。"""
    handle = open(out / ".lock", "a+")
    try:
        fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        handle.seek(0)
        owner = handle.read().strip() or "不明"
        sys.exit(f"{out} 正由另一份 test_settings_ui.py 使用（{owner}）；同一個輸出目錄的截圖與 bin 會互相蓋掉。\n"
                 "要同時跑請給不同的 --out，或等那一份結束。")
    handle.seek(0)
    handle.truncate()
    handle.write(f"pid {os.getpid()}，{time.strftime('%Y-%m-%d %H:%M:%S')} 開始\n")
    handle.flush()
    return handle


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--settings", required=True, type=Path, help="已建好的 0c-settings")
    parser.add_argument("--out", type=Path, default=ROOT / "build/settings/smoke")
    parser.add_argument("--rust", default="1.99.0")
    parser.add_argument("--go", default="go")
    parser.add_argument("--cpus", help="限制容器可用的 CPU（例如 0.5），重現慢機器上的時序問題（#366）；平常不用")
    parser.add_argument("--only", choices=["vocab", "models", "styles", "scroll", "personal", "update", "help", "search"], help="只跑一部分（vocab：我的詞彙；models：模型；scroll：捲動位置；personal：清除學習紀錄；update：更新方式下拉選單；help：選項提示與說明連結；search：設定搜尋）")
    args = parser.parse_args()
    out = args.out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    lock = lock_out(out)  # noqa: F841 —— 持有到行程結束
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
    # 上一次的現場與重查紀錄不留，免得跟這次的混在一起
    shutil.rmtree(shots / "failures", ignore_errors=True)
    (shots / "xdotool-errors.log").unlink(missing_ok=True)
    env = {"SETTINGS_BIN": "/in/0c-settings", "UPDATE_BIN": "/bin-dir/0c-update", "SERVER_BIN": "/bin-dir/0c-server",
           "DATA": "/src/testdata/toy", "OUT": "/shots", "HOME": "/tmp/h", "SMOKE_ONLY": args.only or "",
           "ENGINE_VERSION": ENGINE_VERSION}
    # 容器名稱帶行程編號：中斷（Ctrl+C、上層逾時）時用它收掉容器，docker run --rm 自己不會因為用戶端結束而停。
    container = f"zc-settings-ui-{os.getpid()}"
    # /etc/passwd：以任意 uid 執行時，dbus-launch 需要查得到使用者。
    command = ["docker", "run", "--rm", "--name", container, *(["--cpus", args.cpus] if args.cpus else []), "--user", f"{os.getuid()}:{os.getgid()}",
               "-v", "/etc/passwd:/etc/passwd:ro", "-v", f"{ROOT}:/src:ro", "-v", f"{args.settings.resolve()}:/in/0c-settings:ro", "-v", f"{bin_dir}:/bin-dir:ro", "-v", f"{shots}:/shots",
               *[x for k, v in env.items() for x in ("-e", f"{k}={v}")], image, "bash", "/src/platforms/settings/test/smoke.sh"]
    try:
        result = subprocess.run(command, text=True, capture_output=True)
    except BaseException:
        subprocess.run(["docker", "kill", container], capture_output=True)
        raise
    print(result.stdout)
    if result.stderr.strip():
        print(result.stderr, file=sys.stderr)
    passed = result.returncode == 0 and "SMOKE_RESULT=PASS" in result.stdout
    if (shots / "failures").is_dir():
        print(f"失敗當下的現場：{shots / 'failures'}", file=sys.stderr)
    sys.exit(0 if passed else 1)


if __name__ == "__main__":
    main()
