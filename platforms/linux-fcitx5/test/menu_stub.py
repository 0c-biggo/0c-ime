"""選單測試用：以記錄命令的替身取代 ~/.local/bin/0c-update。

引擎會定時執行 `0c-update status --json`，所以 status 照常轉給真正的更新器；其他命令只記錄（或失敗），
測試才能確認「點選單項目會執行哪個命令」，不必真的開設定程式或連網。
"""
import os
from pathlib import Path
import time


class Updater:
    def __init__(self):
        self.path = Path.home() / '.local/bin/0c-update'
        self.real = self.path.read_bytes()
        self.real_path = self.path.with_name('0c-update.real')
        self.calls = Path('/tmp/0c-updater-calls.txt')

    def replace(self, path, data, mode):
        # 引擎每 5 秒執行一次 status；直接寫入正在執行的檔案會得到 ETXTBSY，所以先寫暫存檔再換名。
        temp = path.with_name(path.name + '.tmp')
        temp.write_bytes(data)
        temp.chmod(mode)
        os.replace(temp, path)

    def stub(self, fail=False, slow=False, old_status=False):
        self.replace(self.real_path, self.real, 0o755)
        status = f'exec {self.real_path} "$@"'
        if old_status:  # 模擬 0.2.0 以前的更新器：status 沒有 features（外殼要把「我的詞彙…」藏起來）
            status = (f'case "$*" in *json*) {self.real_path} "$@" | python3 -I -c \'import json,sys; d=json.load(sys.stdin); '
                      f'd.pop("features",None); print(json.dumps(d,ensure_ascii=False))\'; exit;; esac; exec {self.real_path} "$@"')
        body = f'#!/bin/sh\nif [ "$1" = status ]; then {status}; fi\necho "$@" >> {self.calls}\n'
        if slow:  # 更新檢查跑很久：記錄之後睡 8 秒，用來確認設定列不被擋住
            body += '[ "$1" = auto ] && sleep 8\n'
        if fail:
            body += 'echo "測試用更新工具失敗"\nexit 1\n'
        self.replace(self.path, body.encode(), 0o755)
        self.calls.write_text('')

    def restore(self):
        self.replace(self.path, self.real, 0o755)
        self.real_path.unlink(missing_ok=True)

    def wait_call(self, text, pump, seconds=20):
        until = time.monotonic() + seconds
        while time.monotonic() < until:
            if text in self.calls.read_text():
                return
            pump(.2)
        raise RuntimeError(f'updater was not called with {text!r}: {self.calls.read_text()!r}')
