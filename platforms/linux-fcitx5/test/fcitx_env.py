"""測試自行啟動 Fcitx5 時要不要補 FCITX_ADDON_DIRS。

新版安裝器把 addon 設定的 Library 寫成外殼的絕對路徑，不需要這個變數（#92、#93），測試也不設，才驗得到使用者
的實際情況。dev.59 以前的套件寫相對的 Library=lib0c，更新測試從舊版開始時才補上使用者目錄與這台電腦實際的
系統 addon 目錄（不寫死 Debian 的多架構路徑）。
"""

import os
from pathlib import Path
import re
import shutil
import subprocess


def system_addon_dir():
    fcitx = shutil.which('fcitx5')
    out = subprocess.run(['ldd', fcitx], capture_output=True, text=True).stdout if fcitx else ''
    for line in out.splitlines():
        name, sep, rest = line.partition('=>')
        if sep and name.strip().startswith('libFcitx5Core.so'):
            return Path(os.path.realpath(rest.split('(')[0].strip())).parent / 'fcitx5'
    raise RuntimeError('找不到 Fcitx5 的系統 addon 目錄')


def addon_env(home):
    """回傳要加進 Fcitx5 環境的變數：新版外殼為空。"""
    try:
        conf = (Path(home) / '.local/share/fcitx5/addon/0c.conf').read_text()
    except FileNotFoundError:
        return {}
    library = re.search(r'(?m)^\s*Library\s*=\s*(\S+)', conf)
    if not library or library[1].startswith('/'):
        return {}
    return {'FCITX_ADDON_DIRS': f'{home}/.local/lib/fcitx5:{system_addon_dir()}'}
