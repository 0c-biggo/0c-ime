#!/usr/bin/env python3
"""Upgrade the existing per-user Linux desktop; never replace its dictionaries."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import select
import signal
import socket
import struct
import subprocess
import sys
import tempfile
import time

from install_package import (STATUS_ICONS, LEGACY_STATUS_ICONS, status_icon_config_changes,
                             kimpanel_spacing_changes, kimpanel_block, kimpanel_record,
                             KIMPANEL_SPACING, KIMPANEL_INDICATOR, KIMPANEL_RECORD, OPTIONAL, destination,
                             KIMPANEL_START, KIMPANEL_END, icon_files, refresh_icon_theme,
                             addon_conf, addon_env_change, addon_env_cleanup, obsolete_addon_dirs, clear_session_addon_dirs)

ROOT = Path(__file__).resolve().parents[1]
PLATFORM = ROOT / "platforms/linux-fcitx5"
INSTALL_HOME = Path.home()
BASE = INSTALL_HOME / ".local/share/zuin"
FILES = {
    "build/0c-server": INSTALL_HOME / ".local/bin/0c-server",
    "build/0c-trace": INSTALL_HOME / ".local/bin/0c-trace",
    "build/lib0c.so": INSTALL_HOME / ".local/lib/fcitx5/lib0c.so",
    "0c-addon.conf": INSTALL_HOME / ".local/share/fcitx5/addon/0c.conf",
    # The file name is the Fcitx input method ID saved in the user's profile; keep it.
    "0c-im.conf": INSTALL_HOME / ".local/share/fcitx5/inputmethod/zuin.conf",
    **{f"icons/{name}.svg": INSTALL_HOME / f".local/share/icons/hicolor/scalable/status/{name}.svg"
       for name in ("0c-zhuyin", "0c-english")},
}
# 舊 zuin-* 程式名稱及會蓋過彩色圖的單色 SVG；更新時移入備份。
LEGACY_SERVER = INSTALL_HOME / ".local/bin/zuin-server"
LEGACY_ADDON = INSTALL_HOME / ".local/lib/fcitx5/libzuin.so"
LEGACY_ENVIRONMENT = INSTALL_HOME / ".config/environment.d/90-zuin.conf"
LEGACY = (LEGACY_SERVER, INSTALL_HOME / ".local/bin/zuin-trace", LEGACY_ADDON,
          INSTALL_HOME / ".local/share/fcitx5/addon/zuin.conf", LEGACY_ENVIRONMENT,
          *(INSTALL_HOME / name for name in LEGACY_STATUS_ICONS.values()))
ENVIRONMENT = INSTALL_HOME / ".config/environment.d/90-0c.conf"


def install_content(src):
    """要安裝的內容：addon 設定的 Library 改成外殼的絕對路徑（同完整套件），其餘照建置結果。"""
    data = (PLATFORM / src).read_bytes()
    return addon_conf(data, INSTALL_HOME.resolve()) if src == "0c-addon.conf" else data


def run(*args, **kwargs):
    return subprocess.run(args, cwd=ROOT, check=True, **kwargs)


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def preflight():
    if sys.platform != "linux":
        raise RuntimeError("make upgrade 目前支援 Linux Fcitx5；macOS 請參考 platforms/macos/README.md")
    if os.getuid() == 0:
        raise RuntimeError("請以桌面使用者執行，不要使用 sudo。")
    if not hasattr(os, "pidfd_open") or not hasattr(signal, "pidfd_send_signal"):
        raise RuntimeError("需要 Python 3.9 以上及支援 pidfd 的 Linux 核心。")
    fd = os.pidfd_open(os.getpid())
    os.close(fd)
    for tool in ("git", "go", "make", "g++", "pkg-config", "fcitx5"):
        if not shutil.which(tool):
            raise RuntimeError(f"缺少建置工具：{tool}")
    for path, legacy in ((FILES["build/0c-server"], LEGACY_SERVER), (FILES["build/lib0c.so"], LEGACY_ADDON),
                         (BASE / "data/manifest.json", None)):
        if not path.is_file() and not (legacy and legacy.is_file()):
            raise RuntimeError(f"找不到既有安裝：{path}；首次安裝請依 README 操作。")
    run("pkg-config", "--exists", "Fcitx5Core", "Fcitx5Utils", "Fcitx5Config")


def pull():
    status = run("git", "status", "--porcelain", stdout=subprocess.PIPE, text=True).stdout
    if status.strip():
        raise RuntimeError("工作目錄有未提交變更，請先處理；若要安裝本機版本，使用 make upgrade PULL=0。")
    run("git", "pull", "--ff-only")


def frame(conn, payload):
    data = json.dumps(payload).encode()
    conn.sendall(struct.pack("<I", len(data)) + data)

    def read(size):
        result = b""
        while len(result) < size:
            part = conn.recv(size - len(result))
            if not part:
                raise RuntimeError("測試引擎中斷連線")
            result += part
        return result

    size = struct.unpack("<I", read(4))[0]
    if not 0 < size <= 1 << 20:
        raise RuntimeError("測試引擎回傳無效訊框")
    reply = json.loads(read(size))
    if not reply.get("ok"):
        raise RuntimeError(f"測試引擎拒絕請求：{reply.get('error')}")
    return reply


def check_data():
    # Use a private socket and empty user store: no desktop session or learning touched.
    with tempfile.TemporaryDirectory(prefix="0c-upgrade-") as directory:
        tmp = Path(directory)
        with (tmp / "server.log").open("w+b") as log:
            proc = subprocess.Popen([
                str(PLATFORM / "build/0c-server"), "-data", str(BASE / "data"),
                "-user", str(tmp / "user.json"), "-socket", str(tmp / "engine.sock"),
            ], stdout=log, stderr=log)
            try:
                deadline = time.monotonic() + 30
                while not (tmp / "engine.sock").exists():
                    if proc.poll() is not None or time.monotonic() >= deadline:
                        log.seek(0)
                        raise RuntimeError("新版無法載入現有字典：\n" + log.read().decode(errors="replace"))
                    time.sleep(0.1)
                with socket.socket(socket.AF_UNIX) as conn:
                    conn.settimeout(5)
                    conn.connect(str(tmp / "engine.sock"))
                    reply = frame(conn, {"op": "open", "version": 0})
                    frame(conn, {"op": "close", "session": reply["session"]})
            finally:
                proc.terminate()
                try:
                    proc.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    # Only our isolated probe, never the desktop engine.
                    proc.kill()
                    proc.wait()


PROC = Path("/proc")


def settle():
    """確認多個同名行程前的等待；測試替換成直接改假 /proc。"""
    time.sleep(0.15)


def scan_processes(name):
    """目前使用者名為 name、非殭屍的行程：pid → (路徑, 父行程, 啟動時間)。"""
    found = {}
    for proc in PROC.iterdir():
        if not proc.name.isdigit():
            continue
        try:
            if proc.stat().st_uid != os.getuid() or (proc / "comm").read_text().strip() != name:
                continue
            stat = (proc / "stat").read_text()
            fields = stat[stat.rindex(")") + 2:].split()  # comm 可含空白與括號，從最後一個 ) 起算
            state, parent = fields[0], int(fields[1])
        except (FileNotFoundError, ProcessLookupError, ValueError, IndexError):
            continue
        if state != "Z":
            found[int(proc.name)] = (proc, parent, fields[19] if len(fields) > 19 else "")
    return found


def processes(name):
    """目前使用者名為 name 的行程，排除外殼的暫時子行程。

    外殼（lib0c.so，跑在 Fcitx5 裡）用 posix_spawn 啟動 0c-update、用兩次 fork 啟動 0c-server，子行程在 exec 之前
    沿用父行程的名稱 fcitx5，那幾毫秒內會被數成第二個 Fcitx5，造成「偵測到多個 Fcitx5 行程」的誤報。posix_spawn 的
    子行程父行程仍是 Fcitx5，直接排除；兩次 fork 的孫行程在中間層退出後由 init 接手，只能靠時間分辨：找到不只一個時
    隔一小段時間重掃兩次，只留每次都在、啟動時間相同的行程。另一個桌面的 Fcitx5 會一直在，仍各算一個。
    """
    def without_children(found):
        return [proc for pid, (proc, parent, _) in sorted(found.items()) if parent not in found]

    found = scan_processes(name)
    if len(without_children(found)) <= 1:
        return without_children(found)
    for _ in range(2):
        settle()
        again = scan_processes(name)
        found = {pid: v for pid, v in found.items() if pid in again and again[pid][2] == v[2]}
    return without_children(found)


def running_same(proc, path):
    try:
        return digest(proc / "exe") == digest(path)
    except FileNotFoundError:
        return False


def addon_loaded(proc):
    path = FILES["build/lib0c.so"]
    try:
        stat = path.stat()
        mappings = [line.split() for line in (proc / "maps").read_text().splitlines()
                    if str(path) in line]
        return bool(mappings) and all(int(line[4]) == stat.st_ino and line[-1] != "(deleted)"
                                      for line in mappings)
    except FileNotFoundError:
        return False


def atomic_copy(src, dst):
    dst.parent.mkdir(parents=True, exist_ok=True)
    fd, name = tempfile.mkstemp(prefix=f".{dst.name}.", dir=dst.parent)
    os.close(fd)
    try:
        shutil.copy2(src, name)
        os.replace(name, dst)
    finally:
        Path(name).unlink(missing_ok=True)


def install(changes, removals, config_changes=()):
    """config_changes：(路徑, 新內容)，例如去掉舊 addon 搜尋路徑後的 90-0c.conf；與其他檔案一起備份。"""
    icon_configs = status_icon_config_changes(INSTALL_HOME) if all((PLATFORM / name).is_file() for name in STATUS_ICONS) else []
    icon_configs += list(config_changes)
    spacing_src = PLATFORM / "kimpanel-spacing.js"
    if spacing_src.is_file():
        snippet = spacing_src.read_text()
        spacing = kimpanel_spacing_changes(INSTALL_HOME, snippet)
        icon_configs += spacing
        indicator = destination(INSTALL_HOME, KIMPANEL_INDICATOR)
        content = spacing[0][1].decode("utf-8") if spacing else indicator.read_text() if indicator.is_file() else ""
        expected = KIMPANEL_START + "".join("        " + line + "\n" for line in snippet.splitlines()) + KIMPANEL_END
        if kimpanel_block(content) == expected:
            for relative, payload in ((OPTIONAL[KIMPANEL_SPACING], snippet.encode("utf-8")),
                                      (KIMPANEL_RECORD, kimpanel_record(snippet))):
                path = destination(INSTALL_HOME, relative)
                if not path.exists() or path.read_bytes() != payload:
                    icon_configs.append((path, payload))
    if not changes and not removals and not icon_configs:
        return None
    changes = dict(changes)
    backups = BASE / "backups"
    backups.mkdir(parents=True, exist_ok=True)
    backup = Path(tempfile.mkdtemp(prefix=time.strftime("upgrade-%Y%m%d-%H%M%S-"), dir=backups))
    sources = {src: PLATFORM / src for src in changes}
    if "0c-addon.conf" in sources:
        sources["0c-addon.conf"] = backup / "new-0c.conf"
        sources["0c-addon.conf"].write_bytes(install_content("0c-addon.conf"))
    for path, content in icon_configs:
        source = backup / f"new-{path.name}"
        source.write_bytes(content)
        if path.exists():
            shutil.copymode(path, source)
        key = str(path.relative_to(INSTALL_HOME))
        changes[key] = path
        sources[key] = source
    existed = {}
    for src, dst in changes.items():
        existed[src] = dst.exists()
        if existed[src]:
            saved = backup / dst.relative_to(INSTALL_HOME)
            saved.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(dst, saved)
    # Keep a copy of the personal dictionary for a manual restore; installing never changes it.
    if (BASE / "user.json").is_file():
        (backup / "personal").mkdir(mode=0o700)
        shutil.copy2(BASE / "user.json", backup / "personal/user.json")
    for path in removals:
        saved = backup / path.relative_to(INSTALL_HOME)
        saved.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(path, saved)
    print(f"舊程式備份：{backup}", flush=True)
    applied = []
    try:
        for src, dst in changes.items():
            atomic_copy(sources[src], dst)
            applied.append(src)
    except Exception:
        for src in reversed(applied):
            dst = changes[src]
            if existed[src]:
                atomic_copy(backup / dst.relative_to(INSTALL_HOME), dst)
            else:
                dst.unlink()
        raise
    # 舊名稱的環境檔若有使用者自己的設定，改名保留；外殼的 addon 搜尋路徑已不需要，不帶過去。
    if LEGACY_ENVIRONMENT in removals and not ENVIRONMENT.exists():
        content = LEGACY_ENVIRONMENT.read_text(errors="surrogateescape")
        kept = addon_env_cleanup(content, INSTALL_HOME.resolve())
        if any(line.strip() and not line.lstrip().startswith("#") for line in kept.splitlines()):
            carried = backup / "new-90-0c.conf"
            carried.write_text(kept, errors="surrogateescape")
            atomic_copy(carried, ENVIRONMENT)
    # Remove old names only after every new file is in place.
    for path in removals:
        path.unlink()
    return backup


def stop_old_servers():
    installed = FILES["build/0c-server"]
    # An old zuin-server keeps serving the legacy socket that the new addon falls back to; always stop it.
    for name, path, legacy in (("0c-server", installed, False), ("zuin-server", LEGACY_SERVER, True)):
        stop_servers(name, path, legacy)


def stop_servers(name, installed, legacy):
    for proc in processes(name):
        try:
            # Leave test engines and other installations alone; pidfd prevents PID reuse.
            fd = os.pidfd_open(int(proc.name))
            try:
                if os.readlink(proc / "exe").removesuffix(" (deleted)") != str(installed):
                    continue
                if not legacy and running_same(proc, installed):
                    continue
                print("讓舊引擎正常退出並保存學習資料…", flush=True)
                signal.pidfd_send_signal(fd, signal.SIGTERM)
                poll = select.poll()
                poll.register(fd, select.POLLIN)
                if not poll.poll(10000):
                    raise RuntimeError("舊引擎未在 10 秒內退出；未強制終止，請檢查後重跑 make upgrade PULL=0。")
            finally:
                os.close(fd)
        except (ProcessLookupError, FileNotFoundError):
            pass


def apply():
    print("測試與建置新版…", flush=True)
    run("go", "test", "./...")
    run("go", "vet", "./...")
    # Force rebuild: timestamps alone miss deleted sources and Git build metadata.
    run("make", "-C", str(PLATFORM), "-B", "all", "test", "build/0c-trace")
    check_data()
    fcitx = processes("fcitx5")
    if len(fcitx) > 1:
        raise RuntimeError("偵測到多個 Fcitx5 行程，無法判斷要更新哪個桌面；尚未安裝。")
    env = dict(os.environ)
    if fcitx:
        # Preserve Wayland/D-Bus and ZUIN_* settings even when invoked over SSH.
        for entry in (fcitx[0] / "environ").read_bytes().split(b"\0"):
            if b"=" in entry:
                key, value = entry.split(b"=", 1)
                env[os.fsdecode(key)] = os.fsdecode(value)
        # addon 設定寫的是外殼的絕對路徑；舊版留下的搜尋路徑可能只有建置機的系統目錄，重啟時不再沿用。
        if obsolete_addon_dirs(env.get("FCITX_ADDON_DIRS", ""), INSTALL_HOME.resolve()):
            del env["FCITX_ADDON_DIRS"]
    expected = {src: hashlib.sha256(install_content(src)).hexdigest() for src in FILES}
    changes = {src: dst for src, dst in FILES.items()
               if not dst.exists() or expected[src] != digest(dst)}
    removals = [path for path in LEGACY if path.is_file()]
    env_change = addon_env_change(INSTALL_HOME)
    if env_change and env_change[1] is None:
        removals.append(env_change[0])
    icon_config_changed = all((PLATFORM / name).is_file() for name in STATUS_ICONS) and bool(status_icon_config_changes(INSTALL_HOME))
    restart = bool(fcitx) and (not addon_loaded(fcitx[0]) or bool(removals) or any(
        src in changes for src in ("build/lib0c.so", "0c-addon.conf", "0c-im.conf")) or icon_config_changed)
    if changes or removals or restart or icon_config_changed or env_change:
        print("準備更新；請完成目前組字，5 秒後安裝並在需要時重啟輸入法。", flush=True)
        time.sleep(5)
    icons = icon_files(INSTALL_HOME)
    backup = install(changes, removals, [env_change] if env_change and env_change[1] is not None else [])
    refresh_icon_theme(INSTALL_HOME, icons)
    clear_session_addon_dirs(INSTALL_HOME, bool(env_change))
    stop_old_servers()
    if restart:
        print("重新載入 Fcitx5 外殼…", flush=True)
        # Use the original desktop's environment; do not print its potentially private values.
        with tempfile.TemporaryFile() as log:
            try:
                direct = without_wayland_socket(env)  # 那是 KWin 傳給舊行程的 fd，新行程裡已失效
                if kwin_launched(fcitx[0], env):
                    try:
                        restart_kwin_input_method(env, log)
                    except KWinFallback as error:
                        print(f"無法交由 KWin 重新啟動 Fcitx5（{error}），改為直接啟動。", flush=True)
                        run("fcitx5", "-r", "-d", env=direct, stdout=log, stderr=log, timeout=20)
                else:
                    run("fcitx5", "-r", "-d", env=direct, stdout=log, stderr=log, timeout=20)
                deadline = time.monotonic() + 10
                while time.monotonic() < deadline:
                    current = processes("fcitx5")
                    if len(current) == 1 and addon_loaded(current[0]):
                        break
                    time.sleep(0.1)
                else:
                    raise RuntimeError("Fcitx5 尚未載入新版外殼，請重新登入。")
            except Exception:
                if backup:
                    log.seek(0)
                    with (backup / "fcitx-restart.log").open("wb") as output:
                        shutil.copyfileobj(log, output)
                raise
    for src, dst in FILES.items():
        if expected[src] != digest(dst):
            raise RuntimeError(f"安裝驗證失敗：{dst}")
    revision = run("git", "rev-parse", "--short", "HEAD", stdout=subprocess.PIPE, text=True).stdout.strip()
    dirty = run("git", "status", "--porcelain", stdout=subprocess.PIPE, text=True).stdout.strip()
    print(f"更新完成：{revision}{'（含本機變更）' if dirty else ''}；現有字典與個人詞庫保留。", flush=True)
    if not fcitx:
        print("目前未執行 Fcitx5；下次登入桌面時載入新版。")


KWINRC_INPUT_METHOD = ("--file", "kwinrc", "--group", "Wayland", "--key", "InputMethod")
# kwinrc 變更後 KConfigWatcher 收的通知：org.kde.kconfig.notify.ConfigChanged，路徑 /kwinrc，參數 a{saay}
# {"Wayland": ["InputMethod"]}。Plasma 5（KF 5.115）的 kwriteconfig5 沒有 --notify，所以自己送（Plasma 6 也一樣）。
KCONFIG_NOTIFY = ("--user", "emit", "/kwinrc", "org.kde.kconfig.notify", "ConfigChanged", "a{saay}",
                  "1", "Wayland", "1", str(len("InputMethod")), *(str(b) for b in b"InputMethod"))
VIRTUAL_KEYBOARD = ("--user", "get-property", "org.kde.KWin", "/VirtualKeyboard", "org.kde.kwin.VirtualKeyboard", "available")


class KWinFallback(Exception):
    """交由 KWin 重新啟動沒有成功；kwinrc 已寫回原值，改為直接啟動 Fcitx5。"""


def without_wayland_socket(env):
    return {key: value for key, value in env.items() if key != "WAYLAND_SOCKET"}


def kconfig_tool(*names):
    for name in names:
        if path := shutil.which(name):
            return path
    raise KWinFallback(f"找不到 {'／'.join(names)}")


def kconfig(*args, env):
    """執行 kreadconfig／kwriteconfig／busctl，回傳標準輸出；失敗時錯誤帶出它的 stderr。"""
    try:
        return run(*args, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=10).stdout or ""
    except subprocess.CalledProcessError as error:
        raise KWinFallback(f"{Path(args[0]).name}：{(error.stderr or '').strip()}") from error
    except subprocess.TimeoutExpired as error:
        raise KWinFallback(f"{Path(args[0]).name} 逾時") from error


def parent_comm(proc):
    try:
        stat = (proc / "stat").read_text()
        parent = stat[stat.rindex(") ") + 2:].split()[1]
        return (proc.parent / parent / "comm").read_text().strip()
    except (OSError, ValueError, IndexError):
        return ""


def user_processes(root):
    uid = os.getuid()
    for other in root.iterdir():
        try:
            if other.name.isdigit() and other.stat().st_uid == uid:
                yield other, (other / "comm").read_text().strip()
        except OSError:
            continue


def in_restart_unit(proc):
    """更新器以 systemd-run 啟動的 Fcitx5（0c-fcitx5-*.service；0.2.4 以前在 Plasma Wayland 也這樣啟動）。"""
    try:
        lines = (proc / "cgroup").read_text().splitlines()
    except OSError:
        return False
    return any(Path(line.rpartition(":")[2]).name.startswith("0c-fcitx5-")
               and line.endswith(".service") for line in lines)


def kwin_input_method(env):
    return kconfig(kconfig_tool("kreadconfig6", "kreadconfig5"), *KWINRC_INPUT_METHOD, env=env).strip()


def kwin_launched(proc, env):
    """只有 KDE Plasma Wayland 交還 KWin。「虛擬鍵盤」選 Fcitx 5 時 KWin 直接啟動 fcitx5（帶 WAYLAND_SOCKET、
    父行程是 kwin_wayland）；選 Fcitx 5 Wayland Launcher 時 KWin 的子行程是 fcitx5-wayland-launcher（comm 截成
    fcitx5-wayland-），fcitx5 由 D-Bus 叫起。更新器自己啟動的（0c-fcitx5-*.service）在 KWin 執行中、虛擬鍵盤是
    Fcitx 時也交還。其他合成器（例如 Weston）和 GNOME、X11 照原本的 fcitx5 -r。"""
    if "WAYLAND_SOCKET" in env and parent_comm(proc) == "kwin_wayland":
        return True
    running = list(user_processes(proc.parent))
    if any(comm.startswith("fcitx5-wayland") and parent_comm(other) == "kwin_wayland" for other, comm in running):
        return True
    if in_restart_unit(proc) and any(comm == "kwin_wayland" for _, comm in running):
        try:
            return "fcitx" in Path(kwin_input_method(without_wayland_socket(env))).name.lower()
        except KWinFallback:
            return False
    return False


def user_kwinrc(env):
    config = env.get("XDG_CONFIG_HOME", "")
    if not os.path.isabs(config):
        config = os.path.join(env.get("HOME") or os.path.expanduser("~"), ".config")
    return Path(config) / "kwinrc"


def input_method_lines(path):
    """使用者 kwinrc 的各行，與 [Wayland] 裡 InputMethod（可帶 [$e]、[$d] 之類的標記）所在的行。"""
    lines = path.read_text().split("\n")
    group, found = "", []
    for index, line in enumerate(lines):
        line = line.strip()
        if line.startswith("["):
            group = line
        elif group == "[Wayland]" and line.split("=", 1)[0].split("[", 1)[0].strip() == "InputMethod":
            found.append(index)
    return lines, found


def user_sets_input_method(path):
    """使用者的 kwinrc 的 [Wayland] 有 InputMethod；沒有時值來自系統預設（例如 /etc/xdg/kwinrc）。"""
    try:
        return bool(input_method_lines(path)[1])
    except (OSError, UnicodeDecodeError):
        return False


def revert_user_input_method(path):
    """刪掉使用者 kwinrc 裡的 InputMethod，回到系統預設。不用 kwriteconfig --delete：有系統預設時它寫的是遮蔽
    預設的 InputMethod[$d]，值會變成空的。kwinrc 是符號連結時改它指向的檔案。"""
    real = path.resolve()
    try:
        lines, found = input_method_lines(real)
    except FileNotFoundError:
        return
    except (OSError, UnicodeDecodeError) as error:
        raise KWinFallback(f"還原 kwinrc 的系統預設：{error}") from error
    if not found:
        return
    tmp = real.with_name(real.name + ".0c-new")
    try:
        tmp.write_text("\n".join(line for index, line in enumerate(lines) if index not in found))
        tmp.chmod(real.stat().st_mode & 0o7777)
        tmp.replace(real)
    except OSError as error:
        tmp.unlink(missing_ok=True)
        raise KWinFallback(f"還原 kwinrc 的系統預設：{error}") from error


def wait(seconds, ready):
    deadline = time.monotonic() + seconds
    while not ready():
        if time.monotonic() >= deadline:
            return False
        time.sleep(0.1)
    return True


def restart_kwin_input_method(env, log):
    """KDE Plasma Wayland：Fcitx5 由 KWin 帶著 WAYLAND_SOCKET 啟動，自己執行 fcitx5 -r 拿不到 Wayland 輸入法協定。
    依 KDE 的方式重新啟動：等同在「系統設定 → 鍵盤 → 虛擬鍵盤」先選「無」再選回原本的項目。KWin 沒有停用
    （3 秒內 available 沒變 false）或寫回後沒有啟動 fcitx5 時，kwinrc 寫回原值後以 KWinFallback 改為直接啟動。"""
    env = without_wayland_socket(env)
    method = kwin_input_method(env)
    if not method:
        raise KWinFallback("kwinrc 沒有設定 InputMethod")
    inherited = not user_sets_input_method(user_kwinrc(env))
    write = kconfig_tool("kwriteconfig6", "kwriteconfig5")
    busctl = shutil.which("busctl")  # 沒有時退回 kwriteconfig --notify（Plasma 6 才有）

    def set_input_method(*value):
        if value:
            kconfig(write, *KWINRC_INPUT_METHOD, *(() if busctl else ("--notify",)), *value, env=env)
        else:
            # 原值來自系統預設：刪掉清空時寫入的鍵，不把預設固定寫進使用者的 kwinrc。
            revert_user_input_method(user_kwinrc(env))
            if not busctl:  # 沒有 busctl 時借 kwriteconfig --notify 送通知（寫回目前的系統預設值不會留下鍵）
                kconfig(write, *KWINRC_INPUT_METHOD, "--notify", "--type", "path", method, env=env)
                revert_user_input_method(user_kwinrc(env))
        if busctl:
            kconfig(busctl, *KCONFIG_NOTIFY, env=env)

    def restore():
        value = () if inherited else ("--type", "path", method)
        for attempt in range(3):
            try:
                set_input_method(*value)
                return
            except KWinFallback as error:
                if attempt == 2:
                    raise RuntimeError(f"無法寫回 kwinrc 的 InputMethod（{error}）；請在系統設定的虛擬鍵盤選回 Fcitx 5。") from error
                time.sleep(0.3)

    def disabled():
        try:
            return kconfig(busctl, *VIRTUAL_KEYBOARD, env=env).split() == ["b", "false"]
        except KWinFallback:
            return False

    try:
        set_input_method("")
    except KWinFallback:
        restore()
        raise
    # KWin 收到通知後停用並結束舊的 Fcitx5；等它結束再寫回，否則 KWin 重讀設定時看到相同的值而不重新啟動，
    # 或新的 fcitx5 看到舊實例還在而退出。
    cleared = wait(3, disabled) if busctl else True
    cleared = wait(5, lambda: not processes("fcitx5")) and cleared
    restore()
    if not cleared:
        raise KWinFallback("KWin 在 3 秒內沒有停用輸入法")
    if not wait(8, lambda: processes("fcitx5")):
        raise KWinFallback("寫回 kwinrc 後 KWin 沒有啟動 Fcitx5")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--no-pull", action="store_true")
    parser.add_argument("--apply", action="store_true", help=argparse.SUPPRESS)
    args = parser.parse_args()
    preflight()
    if args.apply:
        apply()
        return
    import fcntl
    # One lock across checkouts, including the child running the freshly pulled script.
    with (BASE / "upgrade.lock").open("a") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise RuntimeError("另一個 make upgrade 正在執行。") from None
        if not args.no_pull:
            pull()
        run(sys.executable, str(Path(__file__).resolve()), "--apply")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, OSError, subprocess.SubprocessError) as error:
        print(f"更新未完成：{error}", file=sys.stderr)
        sys.exit(1)
