#!/usr/bin/env python3
"""Install a verified 0c Linux bundle without touching personal learning or restarting Fcitx."""

import argparse
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import tempfile
import time


# 排程（systemd）與外殼選單執行的 0c-update；由更新器管理，不在收據裡（見 refresh_stable_updater）。
STABLE_UPDATER = ".local/share/zuin/bin/0c-update"

PAYLOAD = {
    "bin/0c-update": ".local/bin/0c-update",
    "install.py": ".local/share/zuin/tools/install.py",
    "bin/0c-server": ".local/bin/0c-server",
    "bin/0c-trace": ".local/bin/0c-trace",
    "lib/lib0c.so": ".local/lib/fcitx5/lib0c.so",
    "config/0c-addon.conf": ".local/share/fcitx5/addon/0c.conf",
    # The file name is the Fcitx input method ID saved in each user's profile; keep it.
    "config/0c-im.conf": ".local/share/fcitx5/inputmethod/zuin.conf",
    **{f"data/{name}": f".local/share/zuin/data/{name}" for name in
       ("lexicon.bin", "lm.bin", "manifest.json", "NOTICE.txt", "report.txt")},
}
# 選用的檔案：套件有才安裝。舊的安裝紀錄沒有它們，所以不算回復時必備的檔案（rollback 的 required）；
# 套件沒帶時，移除先前安裝的舊版（回復時放回），不留下沒人更新、收據也沒記錄的檔案。
STATUS_ICONS = {f"icons/{name}.svg": f".local/share/icons/hicolor/scalable/status/{name}.svg"
                for name in ("0c-zhuyin", "0c-english")}
# 舊套件曾附單色圖。新版不再附，避免 Kimpanel 優先選它而失去綠底；
# 保留舊檔的交易路徑，才能清除、降版及回復舊套件。
LEGACY_STATUS_ICONS = {f"icons/{name}-symbolic.svg": f".local/share/icons/hicolor/scalable/status/{name}-symbolic.svg"
                       for name in ("0c-zhuyin", "0c-english")}
ICON_THEME = ".local/share/icons/hicolor"
APPLICATIONS = ".local/share/applications"
CLASSIC_UI = ".config/fcitx5/conf/classicui.conf"
UI_CONFIGS = (CLASSIC_UI, ".config/fcitx5/conf/kimpanel.conf")
KIMPANEL_INDICATOR = ".local/share/gnome-shell/extensions/kimpanel@kde.org/indicator.js"
KIMPANEL_SPACING = "config/kimpanel-spacing.js"
KIMPANEL_RECORD = ".local/share/0c/gnome/kimpanel-spacing.json"
KIMPANEL_START = "        // BEGIN 0c compact brand icons\n"
KIMPANEL_END = "        // END 0c compact brand icons\n"
KIMPANEL_HOOK = "    _setIcon(property) {\n"


def kimpanel_block(content):
    if content.count(KIMPANEL_START) != 1 or content.count(KIMPANEL_END) != 1:
        return None
    start = content.index(KIMPANEL_START)
    end = content.index(KIMPANEL_END) + len(KIMPANEL_END)
    return content[start:end] if end > start else None


def kimpanel_record(snippet):
    payload = snippet.encode("utf-8") if isinstance(snippet, str) else snippet
    return (json.dumps({"schema": 1, "snippet_sha256": hashlib.sha256(payload).hexdigest()},
                       indent=2) + "\n").encode("utf-8")


def owned_kimpanel_snippet(home, installed=None):
    """Shared provenance works for both make upgrade and full-package installation."""
    source = destination(home, OPTIONAL[KIMPANEL_SPACING])
    if not source.is_file():
        return None
    record = destination(home, KIMPANEL_RECORD)
    if record.is_file():
        try:
            metadata = json.loads(record.read_bytes())
        except (ValueError, UnicodeError):
            return None
        expected = metadata.get("snippet_sha256") if isinstance(metadata, dict) and metadata.get("schema") == 1 else None
    else:
        # Compatibility with full packages installed before the shared record was added.
        expected = (installed or {}).get("files", {}).get(OPTIONAL[KIMPANEL_SPACING])
    return source.read_bytes().decode("utf-8") if expected == digest(source) else None


def restore_kimpanel_block(current, before, after):
    """Reverse addition/removal while retaining unrelated edits and unknown extension updates."""
    if current is None:
        return None
    content = current.decode("utf-8")
    before_block = kimpanel_block(before.decode("utf-8"))
    after_block = kimpanel_block(after.decode("utf-8"))
    current_block = kimpanel_block(content)
    if after_block and current_block == after_block:
        return content.replace(after_block, before_block or "", 1).encode("utf-8")
    if before_block and not after_block and current_block is None:
        if "0c compact brand icons" not in content and content.count(KIMPANEL_HOOK) == 1 and "this.mainIcon" in content and "property.icon" in content:
            return content.replace(KIMPANEL_HOOK, KIMPANEL_HOOK + before_block, 1).encode("utf-8")
    return current


def kimpanel_spacing_changes(home, snippet, enabled=True):
    """Only patch the recognized per-user Kimpanel hook; unknown versions are left intact."""
    if snippet is None:
        return []
    path = destination(home, KIMPANEL_INDICATOR)
    if not path.is_file():
        return []
    content = path.read_bytes().decode("utf-8")
    old = kimpanel_block(content)
    block = KIMPANEL_START + "".join("        " + line + "\n" for line in snippet.splitlines()) + KIMPANEL_END
    if not enabled:
        return [(path, content.replace(block, "", 1).encode("utf-8"))] if old == block else []
    if old == block:
        return []
    # Do not overwrite an edited patch or guess at a different extension's implementation.
    if old is not None or "0c compact brand icons" in content or content.count(KIMPANEL_HOOK) != 1:
        print("警告：Kimpanel 結構或既有留白修正已變更，保留原檔。", file=sys.stderr)
        return []
    if "this.mainIcon" not in content or "property.icon" not in content:
        return []
    return [(path, content.replace(KIMPANEL_HOOK, KIMPANEL_HOOK + block, 1).encode("utf-8"))]


def text_icon_preference(content):
    value = None
    for line in content.splitlines():
        if line.lstrip().startswith("["):
            break
        match = re.match(r"\s*PreferTextIcon\s*=(.*)", line)
        if match:
            value = match[1].strip()
    return value


def with_text_icon_preference(content, value):
    """只修改頂層圖示偏好；保留其他設定、區段、註解及換行。None 表示移除。"""
    lines = content.splitlines(keepends=True)
    output, found, root = [], False, True
    newline = "\r\n" if "\r\n" in content else "\n"
    for line in lines:
        if root and line.lstrip().startswith("["):
            if not found and value is not None:
                output.append(f"PreferTextIcon={value}{newline}")
                found = True
            root = False
        if root and re.match(r"\s*PreferTextIcon\s*=", line):
            found = True
            if value is not None:
                ending = "\r\n" if line.endswith("\r\n") else "\n" if line.endswith("\n") else ""
                output.append(f"PreferTextIcon={value}{ending}")
        else:
            output.append(line)
    if not found and value is not None:
        if output and not output[-1].endswith(("\n", "\r")):
            output[-1] += newline
        output.append(f"PreferTextIcon={value}{newline}")
    return "".join(output)


def status_icon_config_changes(home):
    changes = []
    for relative in UI_CONFIGS:
        path = destination(home, relative)
        before = path.read_bytes() if path.exists() else b""
        after = with_text_icon_preference(before.decode("utf-8"), "False").encode("utf-8")
        if before != after:
            changes.append((path, after))
    return changes


OPTIONAL = {"bin/0c-settings": ".local/bin/0c-settings",
            "bin/0c-ibus": ".local/bin/0c-ibus",
            # 只支援 stdio 的 AI 工具用的轉接（#126）；路徑不隨版本變動，設定程式把它填進範例。
            "bin/0c-mcp": ".local/bin/0c-mcp",
            KIMPANEL_SPACING: ".local/share/0c/gnome/kimpanel-spacing.js",
            **STATUS_ICONS,
            **LEGACY_STATUS_ICONS,
            **{f"data/english/{name}": f".local/share/zuin/data/english/{name}"
               for name in ("words.bin", "manifest.json", "NOTICE.txt")}}
# IBus 外殼的登錄（0c-ibus register 寫入）。不是套件檔案；外殼移除或回復到沒有它的版本時一併清掉，
# 免得 IBus 列出一個啟動不了的輸入法。
IBUS_COMPONENT = ".local/share/ibus/component/0c.xml"
IBUS_COMPONENT_NAME = "<name>org.biggo.ime.IBus</name>"
# 0c-ibus register 讓 GTK／Qt 改用 IBus 輸入模組的設定檔與變數（cmd/0c-ibus/toolkit.go）；檔案也可能只是停用記號。
IBUS_TOOLKIT_ENV = {".config/environment.d/92-0c-ibus-gtk.conf": "GTK_IM_MODULE",
                    ".config/environment.d/93-0c-ibus-qt.conf": "QT_IM_MODULES"}
IBUS_ENV = (".config/environment.d/91-0c-ibus.conf", *IBUS_TOOLKIT_ENV)
ETC = Path("/etc")  # 系統 environment 設定；測試換成暫存目錄
IBUS_CACHE = ".cache/ibus/bus/registry"
IBUS_SOURCE = "('ibus', '0c')"
# 有靈犀注音的輸入來源清單：GNOME 與 Cinnamon（#237）。
IBUS_SOURCE_SCHEMAS = ("org.gnome.desktop.input-sources", "org.cinnamon.desktop.input-sources")
# Debian 系 X11 桌面（Cinnamon、MATE、Xfce）讀不到 environment.d，0c-ibus register 把元件路徑寫在
# ~/.xsessionrc 的這一段（cmd/0c-ibus/desktop.go）；移除時只拿掉這一段。
IBUS_SESSION_RC = ".xsessionrc"
IBUS_SESSION_BEGIN = "# >>> 0c-ibus："
IBUS_SESSION_END = "# <<< 0c-ibus"
# 0c-update 在執行時建立的應用程式選單項目（Exec 要絕對路徑）；解除安裝時一併移除，只動帶標記的檔案。
DESKTOP_ENTRY = ".local/share/applications/0c-settings.desktop"
DESKTOP_ICON = ".local/share/icons/hicolor/128x128/apps/0c-settings.png"
DESKTOP_MARKER = "X-0c-Managed=true"
# Program files of the zuin-* releases (through 0.1.0-dev.15). Install removes them into the backup
# so rollback can restore that release; uninstall leaves them alone.
# engine.json（相容核心更新所指的引擎版本）同樣在完整安裝時移入備份，回復時放回。
LEGACY = (".local/share/zuin/engine.json", ".local/bin/zuin-server", ".local/bin/zuin-trace", ".local/lib/fcitx5/libzuin.so", ".local/share/fcitx5/addon/zuin.conf",
          ".config/environment.d/90-zuin.conf")
RECEIPT = ".local/share/zuin/install-receipt.json"
POINTER = LEGACY[0]
# The personal dictionary is not part of the transaction; a copy is kept for an explicit restore.
USER_STORE = ".local/share/zuin/user.json"
SAVED_USER = "personal/user.json"
# 自訂詞彙（#122、#127）跟著 user.json 備份與還原；舊版不認得這個檔，照常輸入、不碰它。
VOCAB_STORE = ".local/share/zuin/vocab.json"
SAVED_VOCAB = "personal/vocab.json"
SERVERS = ("0c-server", "zuin-server")
# Bundled neural models and portable Python (over a thousand files) are installed and restored as one
# directory tree next to the paired dictionary; 0c-server finds them there.
NEURAL = ".local/share/zuin/neural"
# Fcitx5 外殼的 addon 設定：安裝時把 Library 改寫成家目錄裡 lib0c 的絕對路徑（見 addon_conf）。
ADDON_CONF = "config/0c-addon.conf"
ADDON_LIBRARY = ".local/lib/fcitx5/lib0c"  # Fcitx5 自己補上 .so
# 0.1.0-dev.59 以前寫入的 addon 搜尋路徑。新版不需要；安裝時只移除舊版寫的設定（見 addon_env_cleanup）。
ADDON_ENV = ".config/environment.d/90-0c.conf"
OLD_ADDON_ENV_COMMENTS = ("# 讓 Fcitx 5 在使用者目錄找", "# 靈犀注音 Fcitx5 外殼路徑")
# 各發行版的 Fcitx5 系統 addon 目錄：/usr/lib/<多架構>/fcitx5、/usr/lib64/fcitx5、/usr/lib/fcitx5 等。
SYSTEM_ADDON_DIR = re.compile(r"/(usr/)?(local/)?lib[^/:]*(/[^/:]+)?/fcitx5")


def addon_conf(content, home):
    """addon 設定的 Library 改成家目錄裡外殼的絕對路徑，Fcitx5 不必設定 FCITX_ADDON_DIRS 就找得到。

    相對的 Library 只在 FCITX_ADDON_DIRS（未設定時是編譯時的系統目錄）裡找；絕對路徑則直接載入：
    5.1.7～5.1.12 的 StandardPath::locateAll 先判斷 isAbsolutePath，5.1.13 起 StandardPaths 的
    scanDirectories 遇到絕對路徑只看它本身。原本的 FCITX_ADDON_DIRS 寫的是建置機的系統目錄，在 Fedora
    等發行版會讓 Fcitx5 連自己的 addon 都找不到（#92）；D-Bus 啟動的 Fcitx5 也拿不到登入環境的變數（#93）。
    """
    library = str(home / ADDON_LIBRARY)
    # Fcitx5 的 Library 以分號分隔多個函式庫，設定值的反斜線、引號與前後空白另有意義。
    if any(c in library for c in ';"\\\n\r') or library != library.strip():
        raise ValueError(f"家目錄路徑含有 Fcitx5 設定無法表示的字元：{home}")
    lines = content.decode("utf-8").splitlines(keepends=True)
    found = [i for i, line in enumerate(lines) if re.match(r"\s*Library\s*=", line)]
    if len(found) != 1:
        raise ValueError("外殼的 addon 設定應有一行 Library")
    line = lines[found[0]]
    lines[found[0]] = f"Library={library}" + line[len(line.rstrip("\r\n")):]
    return "".join(lines).encode("utf-8")


def user_addon_dirs(home):
    """FCITX_ADDON_DIRS 裡指向家目錄外殼目錄的寫法。家目錄可能是符號連結（例如 Fedora Silverblue 的 /home → /var/home），兩種都認。"""
    return {"${HOME}/.local/lib/fcitx5", "$HOME/.local/lib/fcitx5", str(home / ".local/lib/fcitx5"),
            str(home.resolve() / ".local/lib/fcitx5")}


def addon_dirs_parts(value):
    return [part.rstrip("/") or part for part in value.strip().strip("\"'").split(":") if part]


def obsolete_addon_dirs(value, home):
    """FCITX_ADDON_DIRS 是舊版安裝器寫的（外殼的使用者目錄加上系統 addon 目錄），不是使用者自己加的目錄。"""
    user = user_addon_dirs(home)
    parts = addon_dirs_parts(value)
    return any(part in user for part in parts) and all(part in user or SYSTEM_ADDON_DIR.fullmatch(part) for part in parts)


def addon_env_cleanup(content, home):
    """移除舊版寫入的 FCITX_ADDON_DIRS 與它的註解；使用者自己的其他設定（含自訂 addon 目錄）保留。"""
    lines = content.splitlines(keepends=True)

    def ours(line):
        key, sep, value = line.strip().removeprefix("export ").partition("=")
        return bool(sep) and key.strip() == "FCITX_ADDON_DIRS" and obsolete_addon_dirs(value, home)

    if not any(ours(line) for line in lines):
        return content
    return "".join(line for line in lines if not ours(line) and not line.lstrip().startswith(OLD_ADDON_ENV_COMMENTS))


def addon_env_change(home):
    """舊版的 90-0c.conf 要怎麼處理：(路徑, 新內容)，只剩空行與註解時新內容為 None（移除）；不必改時回傳 None。"""
    path = destination(home, ADDON_ENV)
    if not path.is_file():
        return None
    content = path.read_bytes().decode("utf-8", errors="surrogateescape")
    cleaned = addon_env_cleanup(content, home)
    if cleaned == content:
        return None
    if all(not line.strip() or line.lstrip().startswith("#") for line in cleaned.splitlines()):
        return path, None
    return path, cleaned.encode("utf-8", errors="surrogateescape")


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def regular(path):
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"需要一般檔案：{path}")
    return path


def verify(bundle):
    release = json.loads(regular(bundle / "release.json").read_text())
    if release.get("schema") != 1 or release.get("product") != "0c":
        raise ValueError("不支援的套件格式")
    files = release.get("files", {})
    if not (set(PAYLOAD) | {"install.py"}).issubset(files):
        raise ValueError("套件缺少必要檔案")
    english = {name for name in OPTIONAL if name.startswith("data/english/")}
    if english.intersection(files) and not english.issubset(files):
        raise ValueError("英文資料包缺少必要檔案")
    if (STATUS_ICONS.keys() | LEGACY_STATUS_ICONS.keys()) & files.keys() and not STATUS_ICONS.keys() <= files.keys():
        raise ValueError("狀態圖示缺少注音或英文檔案")
    if LEGACY_STATUS_ICONS.keys() & files.keys() and not LEGACY_STATUS_ICONS.keys() <= files.keys():
        raise ValueError("舊版單色狀態圖示缺少注音或英文檔案")
    if KIMPANEL_SPACING in files and not STATUS_ICONS.keys() <= files.keys():
        raise ValueError("Kimpanel 留白修正需要注音與英文狀態圖示")
    for name, expected in files.items():
        relative = Path(name)
        if relative.is_absolute() or ".." in relative.parts:
            raise ValueError("套件含不安全的檔案路徑")
        path = bundle / relative
        if any(parent.is_symlink() for parent in path.parents if parent != bundle.parent):
            raise ValueError("套件不接受符號連結目錄")
        if digest(regular(path)) != expected:
            raise ValueError(f"套件雜湊不符：{name}")
    return release


def runtime_check(bundle, release):
    if sys.platform != "linux" or platform.machine() != release["machine"]:
        raise ValueError(f"此套件需要 Linux {release['machine']}")
    fcitx = shutil.which("fcitx5")
    ibus = (bundle / "bin/0c-ibus").is_file() and shutil.which("ibus-daemon")
    if not fcitx and not ibus:
        raise ValueError("請先安裝 Fcitx5（或使用 IBus 的桌面，例如 Ubuntu／GNOME）" if (bundle / "bin/0c-ibus").is_file()
                         else "請先安裝 Fcitx5 與 fcitx5-configtool")
    for name in ("bin/0c-server", "bin/0c-trace"):
        subprocess.run([str(bundle / name), "-h"], check=True, capture_output=True)
    if (bundle / "bin/0c-ibus").is_file():
        subprocess.run([str(bundle / "bin/0c-ibus"), "-version"], check=True, capture_output=True)
    if (bundle / "neural").is_dir():
        # The portable Python needs a compatible glibc; fail before touching the installation.
        # onnxruntime 一 import 就啟動遙測並連到外部伺服器；環境同 internal/neural 的 NoTelemetryEnv。
        subprocess.run([str(bundle / "neural/python/bin/python3"), "-I", "-B", "-c", "import numpy, onnxruntime"],
                       check=True, capture_output=True,
                       env={**os.environ, "ORT_DISABLE_TELEMETRY": "1", "ORT_RUNNING_UNIT_TESTS": "1"})
    subprocess.run([str(bundle / "bin/0c-server"), "-check-data", "-data", str(bundle / "data")],
                   check=True, capture_output=True)
    if not fcitx:
        return  # 只用 IBus：Fcitx5 外殼照樣裝，但這台電腦沒有它的函式庫也不影響輸入。
    # -r 另外解析符號：Fcitx5 函式庫找得到但版本太舊時，ldd 不加 -r 看不出來，Fcitx5 會靜默載入失敗。
    result = subprocess.run(["ldd", "-r", str(bundle / "lib/lib0c.so")],
                            check=True, capture_output=True, text=True)
    if "not found" in result.stdout:
        raise ValueError("外殼缺少執行期函式庫：\n" + result.stdout)
    if "undefined symbol" in result.stdout:
        missing = "\n".join(line for line in result.stdout.splitlines() if "undefined symbol" in line)
        raise ValueError("外殼與這台電腦的 Fcitx5 版本不相容，Fcitx5 會無法載入：\n" + missing)


# WebKitGTK 是設定程式（Tauri）的執行期相依，輸入法本身不需要它；缺少時只提示，不讓安裝失敗。
WEBKIT_INSTALL = (
    (("debian", "ubuntu"), ["sudo", "apt", "install", "libwebkit2gtk-4.1-0"]),
    (("fedora", "rhel", "centos"), ["sudo", "dnf", "install", "webkit2gtk4.1"]),
    (("arch", "manjaro"), ["sudo", "pacman", "-S", "webkit2gtk-4.1"]),
    (("suse", "opensuse", "opensuse-leap", "opensuse-tumbleweed"), ["sudo", "zypper", "install", "libwebkit2gtk-4_1-0"]),
)


def webkit_install_command(os_release):
    """依 /etc/os-release 的 ID 與 ID_LIKE 回傳安裝 WebKitGTK 的指令；認不得的發行版回傳 None。"""
    fields = {}
    for line in os_release.splitlines():
        key, sep, value = line.partition("=")
        if sep:
            fields[key] = value.strip().strip("\"'")
    family = set(f"{fields.get('ID', '')} {fields.get('ID_LIKE', '')}".split())
    for names, command in WEBKIT_INSTALL:
        if family & set(names):
            return command
    return None


def missing_libraries(program):
    """設定程式需要、但這台電腦找不到的共用函式庫；沒有 ldd 或查不出來時回傳空清單。"""
    try:
        result = subprocess.run(["ldd", str(program)], capture_output=True, text=True, timeout=10)
    except (OSError, subprocess.SubprocessError):
        return []
    return [line.split("=>")[0].strip() for line in result.stdout.splitlines() if "=>" in line and "not found" in line]


def settings_dependencies(home, interactive=None, ask=input, run=subprocess.run, show=print, os_release=None):
    """設定程式缺函式庫時說明並（在互動終端、使用者同意時）以 sudo 安裝；任何情況都不讓安裝失敗。"""
    program = home / OPTIONAL["bin/0c-settings"]
    if not program.is_file():
        return False
    missing = missing_libraries(program)
    if not missing:
        return False
    if os_release is None:
        try:
            os_release = Path("/etc/os-release").read_text()
        except OSError:
            os_release = ""
    command = webkit_install_command(os_release)
    show("輸入法已安裝；但「靈犀注音設定」需要這台電腦還沒有的函式庫：" + "、".join(missing))
    show("沒有它也能正常輸入，之後可用 0c-update settings 與 0c-update set 查看、修改設定。")
    if command is None:
        show("請安裝 WebKitGTK 4.1（libwebkit2gtk-4.1）。")
        return True
    show("安裝指令：" + " ".join(command))
    if interactive is None:
        interactive = sys.stdin.isatty() and sys.stdout.isatty()
    if interactive:
        try:
            if ask("要現在安裝嗎？（會用 sudo，需要輸入密碼）[y/N] ").strip().lower() in ("y", "yes"):
                if run(command).returncode == 0:
                    show("已安裝。")
                else:
                    show("安裝沒有完成；稍後可自行執行上面的指令。")
        except (EOFError, OSError, subprocess.SubprocessError):
            show("略過安裝；稍後可自行執行上面的指令。")
    return True


def first_install(home):
    """No earlier 0c or zuin install: nothing in use can be replaced, so the desktop may keep running."""
    return not any((home / p).exists() for p in (".local/share/zuin/install-receipt.json", ".local/bin/0c-server",
                                                  ".local/bin/zuin-server", ".local/lib/fcitx5/lib0c.so",
                                                  ".local/lib/fcitx5/libzuin.so"))


def user_processes(names):
    """目前使用者名為 names 之一、尚未結束的行程：[(名稱, /proc/PID)]。"""
    found = []
    for proc in Path("/proc").iterdir():
        if not proc.name.isdigit():
            continue
        try:
            if proc.stat().st_uid != os.getuid():
                continue
            # 已退出但尚未收屍的行程不再讀取套件；不把 zombie 當成正在使用。
            if (proc / "stat").read_text().rpartition(") ")[2].startswith("Z "):
                continue
            name = (proc / "comm").read_text().strip()
            if name in names:
                found.append((name, proc))
        except (FileNotFoundError, ProcessLookupError, PermissionError):
            pass
    return found


RELOGIN = "請登出再登入；如果靈犀注音還是沒有出現，請重新開機"


def fcitx_on_bus(run=subprocess.run):
    """Fcitx5 是否持有自己的 D-Bus 名稱；查不出來時為 None。沒有名稱表示系統 addon 載不到（#92），任何程式都打不了字。"""
    try:
        if shutil.which("busctl"):
            out = run(["busctl", "--user", "call", "org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus",
                       "NameHasOwner", "s", "org.fcitx.Fcitx5"], capture_output=True, text=True, timeout=10)
            return {"b true": True, "b false": False}.get(out.stdout.strip()) if out.returncode == 0 else None
        if shutil.which("dbus-send"):
            out = run(["dbus-send", "--session", "--print-reply", "--dest=org.freedesktop.DBus", "/org/freedesktop/DBus",
                       "org.freedesktop.DBus.NameHasOwner", "string:org.fcitx.Fcitx5"], capture_output=True, text=True, timeout=10)
            if out.returncode == 0:
                return "boolean true" in out.stdout
    except (OSError, subprocess.SubprocessError):
        pass
    return None


def shell_unused(procs, run=subprocess.run):
    """執行中的 Fcitx5 沒有透過外殼輸入：沒映射任何一版外殼（#93），或連自己的 D-Bus 介面都沒有（#92）。

    這時沒有組字要保護，換檔也不影響執行中的 Fcitx5，可以直接安裝、登出再登入後生效。查不出來時當成使用中。
    0c-update 已確認過時以 ZC_SHELL_UNUSED=1 告知。
    """
    if os.environ.get("ZC_SHELL_UNUSED") == "1":
        return True
    if not any(maps_shell(proc) for proc in procs):
        return True
    return fcitx_on_bus(run) is False


def require_desktop_stopped(home=None, run=subprocess.run):
    # Avoid replacing a paired dictionary while the old desktop engine is still using it.
    names = ("fcitx5", "0c-ibus", *SERVERS)
    found = user_processes(names)
    running = sorted({name for name, _ in found}, key=names.index)
    if home is not None and "fcitx5" in running and shell_unused([proc for name, proc in found if name == "fcitx5"], run):
        running.remove("fcitx5")
    if running:
        # 先停輸入法框架，引擎才不會被下一個按鍵重新啟動；引擎收到 SIGTERM 會先存好學習資料。
        stop = "; ".join(f"pkill -x {name}" for name in running)
        after = "完成後登出再登入。" if "fcitx5" in running else "完成後切回「靈犀注音」即可。"
        raise ValueError(f"靈犀注音正在使用中，不能直接覆蓋。請先完成組字，再執行 {stop} 結束輸入法後重跑這個指令；{after}")


def system_addon_dir(run=subprocess.run):
    """這台電腦 Fcitx5 的系統 addon 目錄：fcitx5 實際連結的 libFcitx5Core 旁的 fcitx5/。

    Debian 系是 /usr/lib/<多架構>/fcitx5，Fedora、openSUSE 是 /usr/lib64/fcitx5，Arch 是 /usr/lib/fcitx5；
    找不到或目錄裡沒有 addon 時回傳 None。
    """
    fcitx = shutil.which("fcitx5")
    if not fcitx:
        return None
    try:
        out = run(["ldd", fcitx], capture_output=True, text=True, timeout=10).stdout
    except (OSError, subprocess.SubprocessError):
        return None
    for line in out.splitlines():
        name, sep, rest = line.partition("=>")
        if sep and name.strip().startswith("libFcitx5Core.so"):
            library = rest.split("(")[0].strip()
            candidate = Path(os.path.realpath(library)).parent / "fcitx5"
            if library.startswith("/") and any(candidate.glob("*.so")):
                return candidate
    return None


def environ(proc):
    try:
        pairs = (proc / "environ").read_bytes().split(b"\0")
    except (FileNotFoundError, ProcessLookupError, PermissionError):
        return {}
    return dict(os.fsdecode(pair).split("=", 1) for pair in pairs if b"=" in pair)


def systemd_addon_dirs(run=subprocess.run):
    """systemd 使用者管理員環境裡的 FCITX_ADDON_DIRS；沒有設定或查不到時為 None。"""
    try:
        shown = run(["systemctl", "--user", "show-environment"], capture_output=True, text=True, timeout=10)
    except (OSError, subprocess.SubprocessError):
        return None
    for line in shown.stdout.splitlines():
        key, _, value = line.partition("=")
        if key == "FCITX_ADDON_DIRS":
            return value
    return None


def clear_session_addon_dirs(home, stale=False, run=subprocess.run):
    """登入中的 systemd 使用者管理員與 D-Bus 若還帶著舊版寫的 FCITX_ADDON_DIRS，一併清掉。

    environment.d 只在使用者管理員啟動時讀取。管理員與 D-Bus 可能跨登出存活（#93），刪掉檔案後，下次由
    D-Bus 啟動的 Fcitx5 仍會拿到舊值；舊值的系統目錄是建置機的，在 Fedora 等發行版會讓 Fcitx5 找不到自己的
    addon（#92）。dbus-daemon 的啟動環境刪不掉變數，改設成這台電腦的系統 addon 目錄，與沒有設定時相同；
    dbus-broker 把它轉給 systemd，所以之後再從 systemd 拿掉。stale：這次安裝移除了舊版的 90-0c.conf，
    工作階段很可能載入過它（D-Bus 的啟動環境讀不到，只能這樣推斷）。
    只處理安裝給目前使用者的情況；沒有桌面工作階段時什麼都不做。
    """
    if home.resolve() != Path.home().resolve():
        return False
    managed = obsolete_addon_dirs(systemd_addon_dirs(run) or "", home)
    stale = stale or managed or any(obsolete_addon_dirs(environ(proc).get("FCITX_ADDON_DIRS", ""), home)
                                    for _, proc in user_processes(("fcitx5",)))
    if not stale:
        return False
    system = system_addon_dir(run)
    try:
        if system and shutil.which("dbus-update-activation-environment"):
            run(["dbus-update-activation-environment", f"FCITX_ADDON_DIRS={system}"], capture_output=True, timeout=10)
        current = systemd_addon_dirs(run)
        if current is not None and (obsolete_addon_dirs(current, home) or current == str(system)):
            run(["systemctl", "--user", "unset-environment", "FCITX_ADDON_DIRS"], capture_output=True, timeout=10)
    except (OSError, subprocess.SubprocessError):
        pass
    return True


# #93 卡住時換上新外殼後，由 0c-update 在重新登入、外殼載入後把 zuin 補回輸入法清單一次的待辦
# （cmd/0c-update 的 restoreStuckInputMethod 讀取並清除）。
ZUIN_RESTORE = ".local/share/zuin/update/fcitx-restore-zuin.json"


def stuck_without_shell(home, procs):
    """執行中的 Fcitx5 找不到舊版外殼（#93）：它把 zuin 從輸入法清單拿掉，登出時存回 profile。

    要在移除 90-0c.conf 的舊設定之前判斷，以下全部成立才算：
    - 已安裝的 addon 設定是舊版的相對 Library（0.1.0-dev.59 以前），Fcitx5 只能靠 FCITX_ADDON_DIRS 找到外殼；
    - 90-0c.conf 還有舊版安裝器寫的 FCITX_ADDON_DIRS（這份安裝確實靠它找外殼）；
    - 每個執行中的 Fcitx5 都沒映射任何一版外殼，而且它自己的環境裡 FCITX_ADDON_DIRS 沒有外殼的目錄。
    外殼的 addon 是 OnDemand=False：Fcitx5 找得到就會在啟動時載入，與 zuin 在不在輸入法清單無關，所以使用者自己
    把靈犀注音拿掉時外殼照樣載入，不會走到這裡。最後一項把「找不到外殼」和「找得到卻沒載入」（例如使用者在
    Fcitx5 停用了這個 addon）分開：後者是使用者的選擇，不補回。讀不到 maps 時當成已映射（不算卡住）。
    """
    if not procs or addon_env_change(home) is None:
        return False
    try:
        conf = destination(home, PAYLOAD[ADDON_CONF]).read_text(encoding="utf-8", errors="replace")
    except OSError:
        return False
    library = re.search(r"(?m)^\s*Library\s*=\s*(\S+)", conf)
    if not library or library[1].startswith("/"):
        return False
    user = user_addon_dirs(home)
    for proc in procs:
        if maps_shell(proc) or any(part in user for part in addon_dirs_parts(environ(proc).get("FCITX_ADDON_DIRS", ""))):
            return False
    return True


def zuin_restore_needed(home):
    """這次安裝是不是在 #93 卡住狀態下換外殼（見 stuck_without_shell）。一行安裝（ZC_INPUT_SETUP）由 0c-update 自己設定輸入法清單。"""
    if os.environ.get("ZC_INPUT_SETUP") == "1" or home.resolve() != Path.home().resolve():
        return False
    return stuck_without_shell(home.resolve(), [proc for _, proc in user_processes(("fcitx5",))])


def mark_zuin_restore(home, version):
    """留下補回輸入法清單的待辦；寫不進去只提示，不影響已完成的安裝。"""
    try:
        path = destination(home, ZUIN_RESTORE)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps({"schema": 1, "created": time.strftime("%Y-%m-%dT%H:%M:%S%z"), "version": version}) + "\n")
    except (OSError, ValueError) as error:
        print(f"無法記下補回輸入法清單的待辦（{error}）；重新登入後若切換不到「靈犀注音」，請在 Fcitx5 設定把它加回輸入法清單。",
              file=sys.stderr)


def forget_zuin_restore(home):
    """回復或移除後，換回的版本不是卡住時換上的那一版；留著的待辦日後可能把使用者自己拿掉的靈犀注音加回去。"""
    try:
        destination(home, ZUIN_RESTORE).unlink(missing_ok=True)
    except (OSError, ValueError):
        pass


# 回復時要看的 addon 設定與 environment.d（0.1.0-dev.15 以前是 zuin.conf、90-zuin.conf），依 environment.d 的讀取順序。
ADDON_CONFS = (PAYLOAD[ADDON_CONF], ".local/share/fcitx5/addon/zuin.conf")
ADDON_ENVS = (ADDON_ENV, ".config/environment.d/90-zuin.conf")


def restored_addon_dirs(home):
    """放回的舊版要靠 FCITX_ADDON_DIRS 才找得到外殼時，它的 environment.d 設的值（展開 $HOME）；不需要時為 None。"""
    libraries = []
    for relative in ADDON_CONFS:
        try:
            libraries += re.findall(r"(?m)^\s*Library\s*=\s*(\S+)", destination(home, relative).read_text(errors="replace"))
        except OSError:
            pass
    if not libraries or all(library.startswith("/") for library in libraries):
        return None
    value = None
    for relative in ADDON_ENVS:
        try:
            lines = destination(home, relative).read_text(errors="surrogateescape").splitlines()
        except OSError:
            continue
        for line in lines:
            key, sep, assigned = line.strip().removeprefix("export ").partition("=")
            if sep and key.strip() == "FCITX_ADDON_DIRS":
                value = assigned.strip().strip("\"'")
    if not value:
        return None
    value = value.replace("${HOME}", str(home)).replace("$HOME", str(home))
    return None if "$" in value else value  # 其他變數展開不了，不設定錯的值


def restore_session_addon_dirs(home, run=subprocess.run):
    """回復到靠 FCITX_ADDON_DIRS 找外殼的舊版（Library 是相對路徑）時，依放回的 environment.d 重設登入中的工作階段。

    安裝新版時 clear_session_addon_dirs 把 D-Bus 啟動環境改成只有系統 addon 目錄，也從 systemd 拿掉。使用者管理員與
    D-Bus 跨登出存活時不會重讀 environment.d，回復後 D-Bus 啟動的 Fcitx5 就找不到外殼（#93 重現）。放回的版本已用
    絕對路徑時不必設定。只處理安裝給目前使用者的情況。回傳設定的值。
    """
    home = home.resolve()
    if home != Path.home().resolve():
        return None
    value = restored_addon_dirs(home)
    if value is None:
        return None
    try:
        # --systemd 同時設定 systemd 使用者環境；dbus-broker 本來就轉給 systemd。
        if shutil.which("dbus-update-activation-environment"):
            run(["dbus-update-activation-environment", "--systemd", f"FCITX_ADDON_DIRS={value}"], capture_output=True, timeout=10)
        elif shutil.which("systemctl"):
            run(["systemctl", "--user", "set-environment", f"FCITX_ADDON_DIRS={value}"], capture_output=True, timeout=10)
    except (OSError, subprocess.SubprocessError):
        pass
    return value


def shell_mapped(proc, library):
    """行程已載入 library（目前這個檔案，不是已被取代的舊檔）。用於核對新外殼生效；是否仍在使用看 maps_shell。"""
    try:
        maps = (proc / "maps").read_text(errors="replace").splitlines()
    except (FileNotFoundError, ProcessLookupError, PermissionError):
        return False
    return any(len(fields) == 6 and fields[5] == str(library) for fields in (line.split(None, 5) for line in maps))


SHELL_LIBRARIES = ("lib0c.so", "libzuin.so")  # libzuin.so：0.1.0-dev.15 以前的外殼


def maps_shell(proc):
    """行程映射著任何一版外殼：目前的檔案、已被取代的舊檔（(deleted) 或別的 inode）、舊版的 libzuin.so 都算。

    上次更新換了 lib0c.so 但 Fcitx5 沒有重新啟動時，使用者仍以舊外殼打字。讀不到 maps 時當成已映射。
    """
    try:
        maps = (proc / "maps").read_text(errors="replace").splitlines()
    except OSError:
        return True
    for fields in (line.split(None, 5) for line in maps):
        if len(fields) == 6 and os.path.basename(fields[5].removesuffix(" (deleted)")) in SHELL_LIBRARIES:
            return True
    return False


def refresh_fcitx(run=subprocess.run):
    """請執行中的 Fcitx5 載入新安裝的 addon（Controller1.Refresh），不重新啟動。

    不能經 D-Bus 另外啟動一個 Fcitx5：busctl 直接關掉自動啟動；沒有 busctl 時用 dbus-send，先確認名稱有人持有。
    """
    try:
        if shutil.which("busctl"):
            return run(["busctl", "--user", "--auto-start=no", "call", "org.fcitx.Fcitx5", "/controller",
                        "org.fcitx.Fcitx.Controller1", "Refresh"], capture_output=True, timeout=10).returncode == 0
        if shutil.which("dbus-send"):
            owner = run(["dbus-send", "--session", "--print-reply", "--dest=org.freedesktop.DBus", "/org/freedesktop/DBus",
                         "org.freedesktop.DBus.NameHasOwner", "string:org.fcitx.Fcitx5"], capture_output=True, text=True, timeout=10)
            return "boolean true" in owner.stdout and run(
                ["dbus-send", "--session", "--print-reply", "--dest=org.fcitx.Fcitx5", "/controller",
                 "org.fcitx.Fcitx.Controller1.Refresh"], capture_output=True, timeout=10).returncode == 0
    except (OSError, subprocess.SubprocessError):
        pass
    return False


def report_fcitx_addon(home, run=subprocess.run, wait=3.0, show=print):
    """安裝後核對執行中的 Fcitx5 是否載入了外殼；還沒載入就請它載入，載不到時說明怎麼重新啟動。

    回傳 None 表示沒有執行中的 Fcitx5（下次啟動時載入），True／False 為是否已載入。
    """
    procs = [proc for _, proc in user_processes(("fcitx5",))]
    if not procs:
        return None
    library = home.resolve() / (ADDON_LIBRARY + ".so")
    if any(shell_mapped(proc, library) for proc in procs):
        show("Fcitx5 已載入靈犀注音外殼。")
        return True
    # 首次安裝：已執行的 Fcitx5 可直接載入新 addon。之前載入失敗過的 addon 要重新啟動 Fcitx5 才會再試。
    if refresh_fcitx(run):
        deadline = time.monotonic() + wait
        while time.monotonic() < deadline:
            if any(shell_mapped(proc, library) for proc in procs):
                show("已讓執行中的 Fcitx5 載入靈犀注音，不必重新登入。")
                return True
            time.sleep(0.1)
    show(f"靈犀注音已安裝，但目前的 Fcitx5 還沒有載入它。{RELOGIN}。")
    system = system_addon_dir(run)
    for proc in procs:
        dirs = environ(proc).get("FCITX_ADDON_DIRS")
        if dirs is not None and system and str(system) not in [os.path.realpath(d) for d in dirs.split(":") if d]:
            show(f"（技術細節：目前的 Fcitx5 以 FCITX_ADDON_DIRS={dirs} 啟動，缺少系統 addon 目錄 {system}，"
                 "其他輸入法也無法使用；重新登入後恢復。）")
            break
    return False


def destination(home, relative):
    path = home / relative
    for part in (path, *path.parents):
        if part == home:
            break
        if part.is_symlink():
            raise ValueError(f"安裝目標不接受符號連結：{part}")
    if path.exists() and not path.is_file():
        raise ValueError(f"安裝目標不是一般檔案：{path}")
    return path


def tree_destination(home, relative):
    path = home / relative
    for part in (path, *path.parents):
        if part == home:
            break
        if part.is_symlink():
            raise ValueError(f"安裝目標不接受符號連結：{part}")
    if path.exists() and not path.is_dir():
        raise ValueError(f"安裝目標不是目錄：{path}")
    return path


def tree_digest(root):
    h = hashlib.sha256()
    for path in sorted(root.rglob("*")):
        relative = path.relative_to(root).as_posix()
        if path.is_symlink():
            raise ValueError(f"目錄不接受符號連結：{path}")
        if path.is_dir():
            h.update(f"D {relative}\n".encode())
        else:
            h.update(f"F {relative} {path.stat().st_mode & 0o111:o} {digest(path)}\n".encode())
    return h.hexdigest()


def without_ibus_source(sources):
    """GNOME 輸入來源清單（gsettings 的文字形式）去掉靈犀注音；其他來源與順序不動。"""
    for old in (", " + IBUS_SOURCE, IBUS_SOURCE + ", ", IBUS_SOURCE):
        sources = sources.replace(old, "")
    return sources


def assigns(path, name):
    """environment 檔案有沒有設定 name。"""
    try:
        lines = path.read_text(encoding="utf-8", errors="replace").splitlines()
    except OSError:
        return False
    return any(line.strip().removeprefix("export ").lstrip().startswith(name + "=") for line in lines)


def user_assigns(home, name):
    """使用者或系統自己在 environment.d／/etc/environment 指定了 name（不算我們的檔案）。"""
    ours = {destination(home, relative) for relative in IBUS_TOOLKIT_ENV}
    files = [ETC / "environment", *sorted((ETC / "environment.d").glob("*.conf")),
             *sorted((home / ".config/environment.d").glob("*.conf"))]
    return any(path not in ours and assigns(path, name) for path in files)


def without_session_block(text):
    """去掉 ~/.xsessionrc 裡 0c-ibus register 寫的那一段；找不到完整的開頭、結尾標記時不動。"""
    start = text.find(IBUS_SESSION_BEGIN)
    end = text.find(IBUS_SESSION_END, start) if start >= 0 else -1
    if end < 0:
        return text
    end += len(IBUS_SESSION_END)
    if text[end:end + 1] == "\n":
        end += 1
    return text[:start] + text[end:]


def remove_session_block(home):
    path = home / IBUS_SESSION_RC
    try:
        real = path.resolve()  # 使用者把設定檔連到別處時改那個檔案，連結保留
        text = real.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError):
        return
    rest = without_session_block(text)
    if rest == text:
        return
    if not rest.strip():
        real.unlink()
        return
    tmp = real.with_name(real.name + ".0c-new")
    tmp.write_text(rest, encoding="utf-8")
    os.chmod(tmp, real.stat().st_mode & 0o777)
    os.replace(tmp, real)


def ibus_unregister(home, run=subprocess.run):
    """撤銷 0c-ibus register：刪元件檔、環境檔（含 ~/.xsessionrc 裡我們那一段）與 IBus 登錄快取，
    並從 GNOME／Cinnamon 輸入來源移除。

    與 cmd/0c-ibus 的 unregister 做同樣的事；這裡不依賴外殼程式，回復到沒有它的版本後也能清。
    只刪內容確認是靈犀注音的元件檔；GTK_IM_MODULE／QT_IM_MODULES 只在我們的檔案有這項設定、
    其他 environment 檔案也沒指定時才清。
    gsettings／systemd 只在安裝的就是目前使用者時才動。
    """
    component = destination(home, IBUS_COMPONENT)
    if not component.is_file() or IBUS_COMPONENT_NAME not in component.read_text(encoding="utf-8", errors="replace"):
        return False
    ours = [name for relative, name in IBUS_TOOLKIT_ENV.items()
            if assigns(destination(home, relative), name) and not user_assigns(home, name)]
    component.unlink()
    for relative in (*IBUS_ENV, IBUS_CACHE):
        destination(home, relative).unlink(missing_ok=True)
    remove_session_block(home)
    if home.resolve() == Path.home().resolve():
        try:
            for schema in IBUS_SOURCE_SCHEMAS:
                current = run(["gsettings", "get", schema, "sources"], capture_output=True, text=True, timeout=10)
                if current.returncode == 0 and IBUS_SOURCE in current.stdout:
                    run(["gsettings", "set", schema, "sources",
                         without_ibus_source(current.stdout.strip())], capture_output=True, timeout=10)
            run(["systemctl", "--user", "unset-environment", "IBUS_COMPONENT_PATH"], capture_output=True, timeout=10)
            for name in ours:
                run(["systemctl", "--user", "unset-environment", name], capture_output=True, timeout=10)
            # 執行中的 ibus-daemon 記著啟動時讀到的元件；重新啟動才不再列出靈犀注音（沒在執行就不啟動）。
            for unit in ("org.freedesktop.IBus.session.GNOME.service", "org.freedesktop.IBus.session.generic.service"):
                run(["systemctl", "--user", "try-restart", unit], capture_output=True, timeout=30)
        except (OSError, subprocess.SubprocessError):
            pass  # 沒有桌面工作階段：檔案已刪，下次登入就不會再出現
    return True


def ibus_orphaned(home):
    """IBus 登錄還在，但外殼程式已不在（回復到沒有 IBus 外殼的版本）。"""
    return (home / IBUS_COMPONENT).is_file() and not (home / OPTIONAL["bin/0c-ibus"]).is_file()


def atomic_copy(src, dst):
    dst.parent.mkdir(parents=True, exist_ok=True)
    fd, name = tempfile.mkstemp(prefix=f".{dst.name}.", dir=dst.parent)
    os.close(fd)
    try:
        shutil.copy2(src, name)
        os.replace(name, dst)
    finally:
        Path(name).unlink(missing_ok=True)


def install(bundle, home, release):
    home = home.resolve()
    targets = {source: destination(home, target) for source, target in PAYLOAD.items()}
    legacy = [destination(home, target) for target in LEGACY]
    for source, target in OPTIONAL.items():
        if (bundle / source).is_file():
            targets[source] = destination(home, target)
        else:
            legacy.append(destination(home, target))
    receipt = destination(home, RECEIPT)
    # 只在套件帶齊自有圖示時套用；舊套件與降版維持既有介面設定。
    icon_configs = status_icon_config_changes(home) if all((bundle / name).is_file() for name in STATUS_ICONS) else []
    spacing_src = bundle / KIMPANEL_SPACING
    spacing = kimpanel_spacing_changes(home, spacing_src.read_text() if spacing_src.is_file() else None)
    if not spacing_src.is_file():
        old_snippet = owned_kimpanel_snippet(home, json.loads(receipt.read_text()) if receipt.is_file() else None)
        spacing = kimpanel_spacing_changes(home, old_snippet, enabled=False)
        legacy.append(destination(home, KIMPANEL_RECORD))
    config_changes = icon_configs + spacing
    if spacing_src.is_file():
        config_changes.append((destination(home, KIMPANEL_RECORD), kimpanel_record(spacing_src.read_bytes())))
    # 舊套件的 addon 設定同樣改寫，降版也不必再靠 FCITX_ADDON_DIRS；套件裡的 environment.conf 不再安裝。
    generated = {ADDON_CONF: addon_conf((bundle / ADDON_CONF).read_bytes(), home)}
    env_change = addon_env_change(home)
    if env_change and env_change[1] is None:
        legacy.append(env_change[0])
    elif env_change:
        config_changes.append(env_change)
    neural_src = bundle / "neural"
    neural_dst = tree_destination(home, NEURAL) if neural_src.is_dir() else None
    moved_neural, placed_neural, staged = None, False, None
    backups = home / ".local/share/zuin/backups"
    destination(home, ".local/share/zuin/backups/placeholder")
    backups.mkdir(parents=True, exist_ok=True)
    backup = Path(tempfile.mkdtemp(prefix="0c-install-", dir=backups))
    user = destination(home, USER_STORE)
    if user.exists():
        (backup / SAVED_USER).parent.mkdir(mode=0o700, exist_ok=True)
        shutil.copy2(user, backup / SAVED_USER)
    vocab = destination(home, VOCAB_STORE)
    if vocab.exists():
        (backup / SAVED_VOCAB).parent.mkdir(mode=0o700, exist_ok=True)
        shutil.copy2(vocab, backup / SAVED_VOCAB)
    previous = {}
    for dst in [*targets.values(), *legacy, receipt, *(path for path, _ in config_changes)]:
        if dst.exists():
            saved = backup / dst.relative_to(home)
            saved.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(dst, saved)
            previous[dst] = saved
    applied = []
    try:
        for source, dst in targets.items():
            if source in generated:
                staged_file = backup / f"new-{dst.name}"
                staged_file.write_bytes(generated[source])
                shutil.copymode(bundle / source, staged_file)
                atomic_copy(staged_file, dst)
            else:
                atomic_copy(bundle / source, dst)
            applied.append(dst)
        for path, content in config_changes:
            replacement = backup / f"new-{path.name}"
            replacement.write_bytes(content)
            if path in previous:
                shutil.copymode(previous[path], replacement)
            atomic_copy(replacement, path)
            applied.append(path)
        for dst in legacy:
            applied.append(dst)
            dst.unlink(missing_ok=True)
        if neural_dst:
            staged = Path(tempfile.mkdtemp(prefix=".neural-", dir=neural_dst.parent))
            shutil.copytree(neural_src, staged / "neural")
            neural_after = tree_digest(staged / "neural")
            if neural_after != tree_digest(neural_src):
                raise ValueError("神經模型目錄複製後內容不符")
            if neural_dst.exists():
                moved_neural = backup / NEURAL
                moved_neural.parent.mkdir(parents=True, exist_ok=True)
                neural_before = tree_digest(neural_dst)
                os.rename(neural_dst, moved_neural)
            os.rename(staged / "neural", neural_dst)
            placed_neural = True
        installed = {str(dst.relative_to(home)): digest(dst) for dst in targets.values()}
        if placed_neural:
            installed[NEURAL] = neural_after
        new_receipt = backup / "new-receipt.json"
        new_receipt.write_text(json.dumps({"version": release["version"], "files": installed,
                                          "backup": str(backup), "active": True}, indent=2) + "\n")
        atomic_copy(new_receipt, receipt)
        applied.append(receipt)
        transaction = {str(dst.relative_to(home)): {
            "before": digest(previous[dst]) if dst in previous else None, "after": digest(dst) if dst.exists() else None
        } for dst in applied}
        for path, _ in icon_configs:
            transaction[str(path.relative_to(home))]["preference"] = "PreferTextIcon"
        for path, _ in spacing:
            transaction[str(path.relative_to(home))]["kimpanel_spacing"] = True
        if placed_neural:
            transaction[NEURAL] = {"before": neural_before if moved_neural else None, "after": neural_after, "tree": True}
        (backup / "transaction.json").write_text(json.dumps(transaction, indent=2) + "\n")
    except Exception:
        for dst in reversed(applied):
            if dst in previous:
                atomic_copy(previous[dst], dst)
            else:
                dst.unlink(missing_ok=True)
        if placed_neural:
            shutil.rmtree(neural_dst)
        if moved_neural and moved_neural.exists():
            os.rename(moved_neural, neural_dst)
        raise
    finally:
        if staged:
            shutil.rmtree(staged, ignore_errors=True)
    refresh_stable_updater(bundle, home)
    return backup


# install() 建立的備份目錄（tempfile.mkdtemp 的 8 個亂數字元）；其他名稱（手動備份、make upgrade 的 upgrade-*、
# restore-user 另存的 0c-user-*）都不是安裝工具清理的對象。
INSTALL_BACKUP = re.compile(r"0c-install-[a-z0-9_]{8}")


def prune_backups(home, keep):
    """安裝成功後只留這次的備份 keep（收據 backup 指到的那一份），刪掉更早的安裝備份（#179）。

    每份備份都含整個舊的神經模型目錄（約 500 MB），以前從不刪，越積越多。回復（0c-update rollback、
    更新器核對失敗時的自動回復）只用收據記錄的這一份；macOS、Windows 也只保留上一版。
    備份目錄是符號連結時照舊拒絕，什麼都不刪；只刪自己命名格式、真正的目錄；刪除失敗只警告，不影響安裝結果。
    舊的備份裡沒有執行中的程式：安裝是複製檔案出去，換下的神經模型目錄在 keep 裡。
    """
    try:
        backups = tree_destination(home, ".local/share/zuin/backups")
        entries = list(backups.iterdir()) if backups.is_dir() else []
    except (OSError, ValueError) as error:
        print(f"警告：沒有清理舊備份（{error}）", file=sys.stderr)
        return []
    removed = []
    for entry in sorted(entries):
        if entry.name == Path(keep).name or not INSTALL_BACKUP.fullmatch(entry.name):
            continue
        if entry.is_symlink() or not entry.is_dir():
            continue
        try:
            shutil.rmtree(entry)
            removed.append(entry)
        except OSError as error:
            print(f"警告：舊備份暫不刪除 {entry}：{error}", file=sys.stderr)
    return removed


def icon_files(home):
    root = home / ICON_THEME
    if not root.is_dir() or root.is_symlink():
        return {}
    return {path.relative_to(root).as_posix(): digest(path)
            for path in root.rglob("0c-*") if path.is_file() and not path.is_symlink()}


def refresh_kde_icons(home, run=subprocess.run):
    """通知目前工作階段的 KIconLoader 重新掃描圖示目錄（#227）。

    KDE 在載入主題時記住當時存在的目錄，首次安裝才建立的 scalable/status 不會因 touch
    hicolor 被加入。使用與 KIconLoader::emitChange 相同的廣播；不啟動桌面服務、不改主題。
    安裝到別的 --home 或沒有工作階段匯流排時，不影響目前使用者的桌面。
    """
    if home.resolve() != Path.home().resolve() or not os.environ.get("DBUS_SESSION_BUS_ADDRESS"):
        return False
    try:
        if shutil.which("busctl"):
            return run(["busctl", "--user", "emit", "/KIconLoader", "org.kde.KIconLoader", "iconChanged", "i", "0"],
                       capture_output=True, timeout=3).returncode == 0
        if shutil.which("dbus-send"):
            return run(["dbus-send", "--session", "--type=signal", "/KIconLoader", "org.kde.KIconLoader.iconChanged", "int32:0"],
                       capture_output=True, timeout=3).returncode == 0
    except (OSError, subprocess.SubprocessError):
        pass  # 圖示通知失敗不應回復已成功的安裝
    return False


def refresh_icon_theme(home, before):
    """自有圖示有增刪改時，讓登入中的 GNOME Shell／KDE 重讀圖示主題。

    GNOME Shell 只在應用程式清單變動時、依 hicolor 目錄本身的時間判斷要不要重讀；只換 scalable/status
    裡的檔案，工作階段會一直沿用登入時的結果。dev.51 刪掉單色圖後，在那之前登入的桌面還照舊選它，
    黑色狀態列上就只剩看不清的暗灰「注」，要重新登入才恢復綠底。兩個目錄都更新時間，就不必重新登入。
    """
    if icon_files(home) == before:
        return False
    for relative in (ICON_THEME, APPLICATIONS):
        path = home / relative
        if path.is_dir() and not path.is_symlink():
            try:
                os.utime(path)
            except OSError:
                pass  # 只影響登入中的桌面何時換圖，不影響安裝結果
    refresh_kde_icons(home)
    return True


def refresh_stable_updater(bundle, home):
    """讓排程與選單用的 0c-update（穩定副本）跟上這次安裝的版本。

    自動更新由舊版更新器代為執行這支安裝工具，更新器建立排程時只會把「自己」複製成穩定副本，
    副本就永遠停在舊版：新版才有的子命令（例如選單的 open-settings）舊副本不認得，點了沒有任何反應。
    這裡跑的是新套件的程式碼，所以升級時也能換掉舊副本。沒有排程（沒有穩定副本）就不建立；失敗不影響安裝，但會警告。
    穩定副本由更新器管理，核心更新也會換它，所以不放進收據與交易（回復時核對會因此失敗）；它只往新版走，
    新版更新器要能操作舊版外殼與資料，回復完整安裝後留著新版是預期的狀態。
    """
    try:
        stable = destination(home.resolve(), STABLE_UPDATER)
        new = bundle / "bin/0c-update"
        if not stable.is_file() or digest(stable) == digest(new):
            return False
        atomic_copy(new, stable)
        stable.chmod(0o700)
        return True
    except (OSError, ValueError) as error:
        print(f"警告：排程用的 0c-update 沒有換成新版（{error}）。可執行 ~/.local/bin/0c-update schedule install 補上。", file=sys.stderr)
        return False


def rollback(home, backup, restore_user=False):
    home = home.resolve()
    # Only restore installer-owned paths, after validating both current and saved bytes.
    backup = backup.resolve()
    if backup.parent != home / ".local/share/zuin/backups":
        raise ValueError("回復目錄必須位於此使用者的套件備份目錄")
    if restore_user:
        regular(backup / SAVED_USER)
    transaction = json.loads(regular(backup / "transaction.json").read_text())
    known = set(PAYLOAD.values()) | set(OPTIONAL.values()) | set(LEGACY) | {RECEIPT, NEURAL, KIMPANEL_INDICATOR, KIMPANEL_RECORD, ADDON_ENV} | set(UI_CONFIGS)
    required = set(PAYLOAD.values()) | {RECEIPT}
    if not required <= set(transaction) <= known:
        raise ValueError("備份檔案清單不完整或含未知路徑")
    tree = transaction.pop(NEURAL, None)
    if tree:
        neural_dst = tree_destination(home, NEURAL)
        if not neural_dst.is_dir() or tree_digest(neural_dst) != tree["after"]:
            raise ValueError(f"安裝後檔案已變更，拒絕覆蓋：{NEURAL}")
        neural_saved = backup / NEURAL
        if tree["before"] is not None and (not neural_saved.is_dir() or tree_digest(neural_saved) != tree["before"]):
            raise ValueError(f"備份雜湊不符：{NEURAL}")
    targets, config_restore = {}, {}
    for relative, entry in transaction.items():
        dst = destination(home, relative)
        if relative == KIMPANEL_INDICATOR:
            if entry.get("kimpanel_spacing") is not True or entry["before"] is None:
                raise ValueError("備份含不支援的 Kimpanel 修改")
            saved = destination(backup, relative)
            if digest(regular(saved)) != entry["before"]:
                raise ValueError(f"備份雜湊不符：{relative}")
            replacement = regular(backup / "new-indicator.js")
            if digest(replacement) != entry["after"]:
                raise ValueError(f"備份雜湊不符：{relative} 修正內容")
            before = saved.read_bytes()
            current = regular(dst).read_bytes() if dst.exists() else None
            if current is not None and digest(dst) == entry["after"]:
                restored = before
            else:
                restored = restore_kimpanel_block(current, before, replacement.read_bytes())
            config_restore[relative] = restored
            targets[relative] = (dst, saved)
            continue
        if relative in UI_CONFIGS:
            if entry.get("preference") != "PreferTextIcon":
                raise ValueError("備份含不支援的介面設定")
            saved = destination(backup, relative)
            before = regular(saved).read_bytes() if entry["before"] is not None else None
            if before is not None and digest(saved) != entry["before"]:
                raise ValueError(f"備份雜湊不符：{relative}")
            content = regular(dst).read_bytes() if dst.exists() else None
            if content is not None and digest(dst) == entry["after"]:
                restored = before
            elif content is not None and (text_icon_preference(content.decode("utf-8")) or "").lower() == "false":
                # Fcitx 可重存整份設定；只撤回本次偏好，保留使用者後來調整的其他項目。
                restored = with_text_icon_preference(content.decode("utf-8"),
                    text_icon_preference(before.decode("utf-8")) if before is not None else None).encode("utf-8")
            else:
                restored = content  # 使用者已改偏好或移除設定，保留其選擇。
            config_restore[relative] = restored
            targets[relative] = (dst, saved)
            continue
        if entry["after"] is None:
            # 完整安裝移除的引擎指標，會在之後的核心更新（0c-update auto）重新寫入；回復完整安裝時
            # 一併改回安裝前的狀態（刪除或放回舊指標），讓引擎回到該次安裝的程式與字典。
            if relative == POINTER and dst.exists():
                regular(dst)
            elif dst.exists():
                raise ValueError(f"安裝後已移除的舊檔又出現，拒絕覆蓋：{relative}")
        elif digest(regular(dst)) != entry["after"]:
            raise ValueError(f"安裝後檔案已變更，拒絕覆蓋：{relative}")
        saved = destination(backup, relative)
        if entry["before"] is not None and digest(regular(saved)) != entry["before"]:
            raise ValueError(f"備份雜湊不符：{relative}")
        targets[relative] = (dst, saved)
    # A failed rollback restores the state from immediately before this operation.
    with tempfile.TemporaryDirectory(prefix="0c-rollback-", dir=backup.parent) as temp:
        current = Path(temp)
        for relative, (dst, _) in targets.items():
            if dst.exists():
                atomic_copy(dst, current / relative)
        applied = []
        moved_current = False
        try:
            for relative, (dst, saved) in targets.items():
                if relative in config_restore:
                    if config_restore[relative] is None:
                        dst.unlink(missing_ok=True)
                    else:
                        replacement = current / f"restored-{dst.name}"
                        replacement.write_bytes(config_restore[relative])
                        if dst.exists():
                            shutil.copymode(dst, replacement)
                        atomic_copy(replacement, dst)
                elif transaction[relative]["before"] is None:
                    dst.unlink(missing_ok=True)
                else:
                    atomic_copy(saved, dst)
                applied.append(relative)
            if tree:
                # Directory renames stay on this filesystem; the current tree is kept until success.
                os.rename(neural_dst, current / "neural")
                moved_current = True
                if tree["before"] is not None:
                    os.rename(neural_saved, neural_dst)
        except Exception:
            if moved_current:
                if neural_dst.exists():
                    os.rename(neural_dst, neural_saved)
                os.rename(current / "neural", neural_dst)
            for relative in reversed(applied):
                if (current / relative).exists():
                    atomic_copy(current / relative, targets[relative][0])
                else:
                    targets[relative][0].unlink(missing_ok=True)
            raise
    if ibus_orphaned(home):
        ibus_unregister(home)
    if restore_user:
        return restore_user_store(home, backup)
    return None


def restore_user_store(home, backup):
    # Keep the dictionary being replaced, so the restore itself can be undone.
    home = home.resolve()
    backup = backup.resolve()
    if backup.parent != home / ".local/share/zuin/backups":
        raise ValueError("還原來源必須位於此使用者的套件備份目錄")
    regular(backup / SAVED_USER)
    restore_vocab = (backup / SAVED_VOCAB).exists()
    if restore_vocab:
        regular(backup / SAVED_VOCAB)
    user = destination(home, USER_STORE)
    vocab = destination(home, VOCAB_STORE)
    kept = Path(tempfile.mkdtemp(prefix="0c-user-", dir=backup.parent))
    if user.exists():
        (kept / SAVED_USER).parent.mkdir(mode=0o700, exist_ok=True)
        shutil.copy2(user, kept / SAVED_USER)
    if vocab.exists():
        (kept / SAVED_VOCAB).parent.mkdir(mode=0o700, exist_ok=True)
        shutil.copy2(vocab, kept / SAVED_VOCAB)
    atomic_copy(backup / SAVED_USER, user)
    # 備份裡沒有自訂詞彙（升級前還沒加詞）時保留目前的檔案：刪掉等於丟掉使用者親手加的詞，
    # 而舊版本來就不讀它。
    if restore_vocab:
        atomic_copy(backup / SAVED_VOCAB, vocab)
    return kept


@contextmanager
def mutation_lock(home):
    import fcntl
    path = destination(home, ".local/share/zuin/upgrade.lock")
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise ValueError("另一個升級工具正在執行") from None
        yield


def uninstall(home):
    # Only remove unchanged program files owned by this installer. Keep dictionaries and learning.
    receipt = destination(home, RECEIPT)
    installed = json.loads(receipt.read_text())
    spacing_src = destination(home, OPTIONAL[KIMPANEL_SPACING])
    snippet = owned_kimpanel_snippet(home, installed)
    if snippet is not None:
        indicator = destination(home, KIMPANEL_INDICATOR)
        block = KIMPANEL_START + "".join("        " + line + "\n" for line in snippet.splitlines()) + KIMPANEL_END
        content = indicator.read_bytes() if indicator.is_file() else b""
        if kimpanel_block(content.decode()) == block:
            with tempfile.TemporaryDirectory(prefix="0c-panel-", dir=indicator.parent) as temp:
                source = Path(temp) / "indicator.js"
                source.write_bytes(content.replace(block.encode(), b"", 1))
                shutil.copymode(indicator, source)
                atomic_copy(source, indicator)
        # make upgrade owns these through the shared record, without rewriting the package receipt.
        spacing_src.unlink()
        destination(home, KIMPANEL_RECORD).unlink(missing_ok=True)
    ibus_unregister(home)
    # ADDON_ENV：0.1.0-dev.59 以前的收據記有它；新版安裝不再寫入。
    allowed = {target for source, target in {**PAYLOAD, **OPTIONAL}.items() if not source.startswith("data/")} | {ADDON_ENV}
    removable, retained = [], []
    for relative in sorted(allowed):
        path = destination(home, relative)
        if path.exists():
            if installed.get("files", {}).get(relative) == digest(path):
                removable.append(path)
            else:
                retained.append(relative)
    for path in removable:
        path.unlink()
    entry = destination(home, DESKTOP_ENTRY)
    if entry.exists() and DESKTOP_MARKER in entry.read_text(encoding="utf-8", errors="replace"):
        entry.unlink()
        destination(home, DESKTOP_ICON).unlink(missing_ok=True)  # 圖示是同一個動作建立的
    neural = tree_destination(home, NEURAL)
    if NEURAL in installed.get("files", {}) and neural.is_dir():
        if tree_digest(neural) == installed["files"][NEURAL]:
            shutil.rmtree(neural)
        else:
            retained.append(NEURAL)
    installed["active"] = False
    with tempfile.TemporaryDirectory(prefix="0c-receipt-", dir=receipt.parent) as temp:
        replacement = Path(temp) / "receipt.json"
        replacement.write_text(json.dumps(installed, indent=2) + "\n")
        atomic_copy(replacement, receipt)
    return retained


def main():
    parser = argparse.ArgumentParser(description="靈犀注音（0c）Linux 安裝工具；首次安裝可在桌面中執行，升級、回復與移除請先登出圖形桌面並由文字主控台操作")
    parser.add_argument("action", choices=("check", "install", "rollback", "restore-user", "uninstall"))
    parser.add_argument("--backup", type=Path, help="安裝時顯示的套件備份目錄")
    parser.add_argument("--restore-user", action="store_true",
                        help="rollback 時一併還原該次安裝前保存的個人詞庫；目前的詞庫另存一份")
    parser.add_argument("--home", type=Path, default=Path.home(), help="安裝家目錄，預設為目前使用者")
    parser.add_argument("--bundle", type=Path, default=Path(__file__).resolve().parent,
                        help="要驗證及安裝的套件目錄；可用新版安裝工具處理舊套件")
    args = parser.parse_args()
    bundle = args.bundle.resolve()
    try:
        if os.getuid() == 0:
            raise ValueError("請以一般使用者執行，不要使用 sudo")
        if args.action in ("uninstall", "rollback", "restore-user"):
            require_desktop_stopped()
            with mutation_lock(args.home.resolve()):
                icons = icon_files(args.home.resolve())
                if args.action == "restore-user":
                    if args.backup is None:
                        raise ValueError("restore-user 需要 --backup")
                    kept = restore_user_store(args.home, args.backup)
                    print(f"已還原個人詞庫；還原前的詞庫另存於：{kept}")
                    print(f"撤回：python3 {Path(__file__).resolve()} restore-user --backup {kept}")
                    return
                if args.action == "rollback":
                    if args.backup is None:
                        raise ValueError("rollback 需要 --backup")
                    kept = rollback(args.home, args.backup, args.restore_user)
                    forget_zuin_restore(args.home.resolve())
                    refresh_icon_theme(args.home.resolve(), icons)
                    restore_session_addon_dirs(args.home)
                    if kept:
                        print(f"已回復程式與配對字典，並還原安裝前的個人詞庫；回復前的詞庫另存於：{kept}")
                        # The installed tools/install.py is now the older release's copy and may lack restore-user.
                        print(f"撤回：以本版套件的 install.py 執行 python3 install.py restore-user --backup {kept}")
                    else:
                        print("已回復程式與配對字典，個人詞庫保留；若舊版無法讀取新版格式，會照常輸入但不存檔，可加 --restore-user。")
                    print("重新登入後生效。")
                    return
                retained = uninstall(args.home.resolve())
                forget_zuin_restore(args.home.resolve())
                refresh_icon_theme(args.home.resolve(), icons)
            print("已移除套件程式；字典、個人詞庫、診斷紀錄與備份保留。")
            if retained:
                print("本機曾修改，保留：" + ", ".join(retained))
            return
        release = verify(bundle)
        runtime_check(bundle, release)
        print(f"已驗證靈犀注音 {release['version']}（{release['channel']}）")
        if args.action == "install":
            if not first_install(args.home):
                require_desktop_stopped(args.home)
            with mutation_lock(args.home.resolve()):
                icons = icon_files(args.home.resolve())
                old_env = addon_env_change(args.home.resolve()) is not None
                restore_zuin = zuin_restore_needed(args.home)  # 要在移除 90-0c.conf 的舊設定之前判斷
                backup = install(bundle, args.home, release)
                prune_backups(args.home.resolve(), backup)
                if restore_zuin:
                    mark_zuin_restore(args.home.resolve(), release["version"])
                refresh_icon_theme(args.home.resolve(), icons)
            print(f"安裝完成，舊程式與配對字典備份：{backup}")
            clear_session_addon_dirs(args.home, old_env)
            loaded = None
            if os.environ.get("ZC_MANAGED_RELOAD") != "1":  # 更新器代為執行時由它重新載入並核對
                settings_dependencies(args.home.resolve())
                if shutil.which("fcitx5"):
                    loaded = report_fcitx_addon(args.home)
            ibus = (args.home / OPTIONAL["bin/0c-ibus"]).is_file()
            if os.environ.get("ZC_INPUT_SETUP") == "1":
                print("個人詞庫已保留。")  # 0c-update 接著設定輸入法框架並說明下一步
            elif os.environ.get("ZC_SHELL_UNUSED") == "1":
                print("個人詞庫已保留。登出再登入後生效。")
            elif os.environ.get("ZC_MANAGED_RELOAD") == "1":
                print("個人詞庫已保留，更新器將重新載入輸入法並核對生效。")
            elif ibus and (args.home / IBUS_COMPONENT).is_file():
                print("個人詞庫已保留。下次登入（或重新選用 IBus 的「靈犀注音」）後載入新版。")
            elif loaded is not None:
                print("個人詞庫已保留。")
            else:
                print("個人詞庫已保留。重新啟動 Fcitx5 或下次登入後載入新版。")
                if ibus and not shutil.which("fcitx5"):
                    print(f"使用 IBus（例如 Ubuntu／GNOME）：執行 {args.home / OPTIONAL['bin/0c-ibus']} register")
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        print(f"安裝失敗：{error}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
