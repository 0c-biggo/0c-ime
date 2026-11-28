#!/usr/bin/env python3
"""在野的舊版更新器遇到新格式的索引不能壞（#295、#291）。

已安裝的使用者電腦上跑的是當初那一版的 0c-update，它們只看得懂當時的索引格式。索引加欄位（例如 #295 的 rules）時，
舊版必須照舊接受整份索引、照舊選到舊格式 assets 裡的資產；任何一版拒絕整份索引，那一版的使用者就永遠停在原地。

做法：從 S3 bucket 下載各版 Linux 套件裡的 0c-update（串流讀 tar.gz，讀到 bin/0c-update 就停，不必下載整個 400 MB
套件；快取在 ~/.cache/0c/old-updaters），把內建的正式發行公鑰換成 E2E 測試公鑰（同長度的 base64 字串，只換這一處），
以本機 HTTPS（testdata/e2e 的測試 CA 與伺服器憑證，經 SSL_CERT_FILE 讓舊版信任）提供以測試金鑰簽章的新索引，
在隔離的 HOME 執行舊版的 `check -json`，核對它接受索引並選到舊格式 assets 的資產。另以目前的程式碼（同樣換公鑰）
核對它依規則選資產，並以一份壞索引（同平台兩個資產）確認這個測試抓得到舊版拒絕索引的情況。

另一組（#365）：新版在 settings.json 加欄位時，舊版要照舊讀得懂、存檔時不能把新欄位弄丟。「自動加空格」選
「中英文與數字之間」存成 cjk_latin_space＋cjk_digit_space 兩個 true，舊版（0.2.10–0.2.15，只認得前者）要讀成開啟，
改別的設定存檔後兩個欄位都還在；舊版把它關掉留下的 false＋true，目前的版本要讀成關閉。

環境變數：ZC_OLD_UPDATERS=0.2.0,0.2.14 指定版本；ZC_OLD_UPDATER_CACHE 改快取位置；只能在 linux/amd64 執行。
"""

import base64
import hashlib
import http.server
import io
import json
import os
from pathlib import Path
import platform
import shutil
import ssl
import subprocess
import sys
import tarfile
import tempfile
import threading
import unittest
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
E2E = ROOT / "testdata" / "e2e"
BUCKET = "https://s3.biggo.com/0c-releases/releases"
# 0.2.0 是第一個不帶後綴的正式版；之前的 0.1.0-dev.* 已全部升上來（自動更新從 dev.18 起），不再測。
DEFAULT_VERSIONS = [f"0.2.{n}" for n in range(0, 15)]
# 有 cjk-latin-space（#273）、還沒有 cjk_digit_space（#365）的版本。
SPACING_VERSIONS = [f"0.2.{n}" for n in range(10, 16)]
PRODUCTION_KEY = b"p8+04oez+UEucOQTdHmA7sjI1Pixh5Q4VtepvVBqlb0="  # internal/update/trust_keys.go
TEST_KEY = b"qnPEX/wcxogJ72KQcc2BT6UunnJyBKxtLDcOk9l1HAY="  # internal/update/trust_e2etest.go


def versions():
    raw = os.environ.get("ZC_OLD_UPDATERS", "")
    return [v for v in raw.split(",") if v] or DEFAULT_VERSIONS


def cache_dir():
    return Path(os.environ.get("ZC_OLD_UPDATER_CACHE", Path.home() / ".cache" / "0c" / "old-updaters"))


def fetch_updater(version):
    """各版 Linux 套件裡的 bin/0c-update（換過公鑰）；串流讀到這個檔案就停。"""
    target = cache_dir() / version / "0c-update"
    if target.is_file():
        return target
    url = f"{BUCKET}/{version}/0c-{version}-linux-amd64.tar.gz"
    with urllib.request.urlopen(url, timeout=120) as response, tarfile.open(fileobj=response, mode="r|gz") as tar:
        for member in tar:
            if member.isfile() and member.name.endswith("/bin/0c-update"):
                data = tar.extractfile(member).read()
                break
        else:
            raise RuntimeError(f"{url} 裡沒有 bin/0c-update")
    if data.count(PRODUCTION_KEY) != 1:
        raise RuntimeError(f"{version} 的 0c-update 裡正式公鑰出現 {data.count(PRODUCTION_KEY)} 次，無法換成測試公鑰")
    target.parent.mkdir(parents=True, exist_ok=True)
    tmp = target.with_suffix(".tmp")
    tmp.write_bytes(data.replace(PRODUCTION_KEY, TEST_KEY))
    tmp.chmod(0o755)
    tmp.rename(target)
    return target


def package(path, version, goos="linux", arch="amd64"):
    """只有 release.json 的套件：0c-release 只讀它，舊版查詢時也不下載。"""
    release = json.dumps({"schema": 1, "product": "0c", "version": version, "channel": "public-beta", "os": goos, "arch": arch,
                          "files": {}}).encode()
    with tarfile.open(path, "w:gz") as tar:
        info = tarfile.TarInfo(f"0c-{version}-{goos}-{arch}/release.json")
        info.size = len(release)
        tar.addfile(info, io.BytesIO(release))
    return path


def sign(body, key_file):
    """以 E2E 測試私鑰簽索引位元組（手改過的索引用；正常索引由 0c-release 簽）。"""
    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
    raw = base64.b64decode(key_file.read_text().strip())
    return base64.b64encode(Ed25519PrivateKey.from_private_bytes(raw[:32]).sign(body)) + b"\n"


class Quiet(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass


@unittest.skipUnless(sys.platform == "linux" and platform.machine() in ("x86_64", "AMD64"), "舊版 Linux 更新器只能在 linux/amd64 執行")
class OldUpdatersAcceptNewFeeds(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = Path(tempfile.mkdtemp(prefix="0c-old-updaters-"))
        cls.www = cls.temp / "www"
        (cls.www / "feeds").mkdir(parents=True)
        go = os.environ.get("GO", "go")
        cls.release = cls.temp / "0c-release"
        subprocess.run([go, "build", "-o", str(cls.release), "./cmd/0c-release"], cwd=ROOT, check=True)
        current = cls.temp / "0c-update-current"
        subprocess.run([go, "build", "-ldflags", "-X main.buildVersion=0.2.15", "-o", str(current), "./cmd/0c-update"], cwd=ROOT, check=True)
        data = current.read_bytes()
        assert data.count(PRODUCTION_KEY) == 1
        current.write_bytes(data.replace(PRODUCTION_KEY, TEST_KEY))
        cls.current = current
        cls.old = {v: fetch_updater(v) for v in versions()}

        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(E2E / "feed-server.pem", E2E / "feed-server.key")
        handler = lambda *a, **k: Quiet(*a, directory=str(cls.www), **k)
        cls.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
        cls.server.socket = context.wrap_socket(cls.server.socket, server_side=True)
        threading.Thread(target=cls.server.serve_forever, daemon=True).start()
        cls.base = f"https://127.0.0.1:{cls.server.server_address[1]}"

        legacy = package(cls.temp / "shell.tar.gz", "0.3.0")
        core = package(cls.temp / "core.tar.gz", "0.3.0")
        cls.shell_sha = hashlib.sha256(legacy.read_bytes()).hexdigest()
        cls.core_sha = hashlib.sha256(core.read_bytes()).hexdigest()
        rules = cls.temp / "rules.json"
        rules.write_text(json.dumps([
            {"os": "linux", "arch": "amd64", "installed_min": "0.2.15", "assets": [
                {"kind": "core", "path": str(core), "url": cls.base + "/releases/core.tar.gz"},
                {"kind": "updater", "path": str(legacy), "url": cls.base + "/releases/shell.tar.gz"}]},
            {"os": "linux", "arch": "amd64", "installed_below": "0.2.0", "assets": []},
        ]))
        feed = cls.www / "feeds" / "rules.json"
        subprocess.run([str(cls.release), "feed", "-key", str(E2E / "release-test.key"), "-serial", "5", "-kind", "shell",
                        "-out", str(feed), "-rules", str(rules), "-asset", f"{legacy}={cls.base}/releases/shell.tar.gz"], check=True)
        body = json.loads(feed.read_text())
        assert body["rules"] and len(body["assets"]) == 1

        # 未來的索引：規則裡有這一版還不認得的種類與條件、頂層有新欄位。舊版與目前的版本都要照常運作。
        body["mirrors"] = ["https://mirror.example.org/0c/"]
        body["rules"].insert(0, {"os": "linux", "arch": "amd64", "installed_min": "0.2.15", "requires": {"glibc": "2.40"},
                                 "assets": [{"kind": "delta", "from": "0.2.15", "url": cls.base + "/releases/d",
                                             "sha256": "d" * 64, "size": 1}]})
        cls.write_feed("future", body)
        # 壞索引：同平台兩個舊格式資產。舊版一定拒絕，用來確認測試抓得到「舊版拒絕整份索引」。
        broken = json.loads(feed.read_text())
        broken["assets"].append(dict(broken["assets"][0]))
        cls.write_feed("broken", broken)

    @classmethod
    def write_feed(cls, name, body):
        raw = json.dumps(body, indent=2).encode() + b"\n"
        path = cls.www / "feeds" / f"{name}.json"
        path.write_bytes(raw)
        Path(str(path) + ".sig").write_bytes(sign(raw, E2E / "release-test.key"))

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        shutil.rmtree(cls.temp, ignore_errors=True)

    def check(self, updater, installed, feed):
        """在隔離的 HOME 以 updater 查詢 feed，回傳 check -json 的結果。"""
        home = Path(tempfile.mkdtemp(prefix="home-", dir=self.temp))
        base = home / ".local/share/zuin"
        (base / "update").mkdir(parents=True)
        (base / "install-receipt.json").write_text(json.dumps({"schema": 1, "product": "0c", "version": installed, "active": True}))
        (base / "update/state.json").write_text(json.dumps({"schema": 1, "install_id": "00" * 16, "mode": "ask", "stage": "idle",
                                                            "feed": f"{self.base}/feeds/{feed}.json"}))
        (home / "run").mkdir()
        env = {"HOME": str(home), "PATH": "/usr/bin:/bin", "SSL_CERT_FILE": str(E2E / "test-ca.pem"), "XDG_RUNTIME_DIR": str(home / "run"),
               "XDG_CONFIG_HOME": str(home / ".config"), "XDG_DATA_HOME": str(home / ".local/share"),
               "DBUS_SESSION_BUS_ADDRESS": "unix:path=" + str(home / "no-bus")}
        run = subprocess.run([str(updater), "check", "-json"], env=env, capture_output=True, text=True, timeout=120)
        try:
            return json.loads(run.stdout)
        except ValueError:
            self.fail(f"{updater} check -json 沒有輸出 JSON（{run.returncode}）：{run.stdout}\n{run.stderr}")

    def test_old_updaters_accept_rules_and_pick_the_legacy_asset(self):
        for version, updater in self.old.items():
            for feed in ("rules", "future"):
                with self.subTest(version=version, feed=feed):
                    result = self.check(updater, version, feed)
                    view = result.get("update", {})
                    self.assertTrue(result.get("ok"), result)
                    self.assertFalse(view.get("error"), view)
                    self.assertEqual(view.get("state"), "update_available", view)
                    self.assertEqual(view["target"]["version"], "0.3.0")
                    self.assertEqual(view["target"]["kind"], "shell")
                    self.assertEqual(view["target"]["sha256"], self.shell_sha)

    def test_harness_detects_a_feed_old_updaters_reject(self):
        version = sorted(self.old, key=lambda v: [int(x) for x in v.split(".")])[0]
        view = self.check(self.old[version], version, "broken").get("update", {})
        self.assertIn("invalid or duplicate asset", view.get("error", ""), view)

    def test_current_updater_follows_rules(self):
        for feed in ("rules", "future"):
            with self.subTest(feed=feed):
                view = self.check(self.current, "0.2.15", feed).get("update", {})
                self.assertFalse(view.get("error"), view)
                self.assertEqual(view["target"]["kind"], "core", view)
                self.assertEqual(view["target"]["sha256"], self.core_sha)
        # 規則不符（外殼比 0.2.15 舊）時照舊用 assets。
        view = self.check(self.current, "0.2.14", "rules").get("update", {})
        self.assertEqual(view["target"]["kind"], "shell", view)


def number(version):
    return [int(x) for x in version.split(".")]


@unittest.skipUnless(sys.platform == "linux" and platform.machine() in ("x86_64", "AMD64"), "舊版 Linux 更新器只能在 linux/amd64 執行")
class OldUpdatersKeepNewSettings(unittest.TestCase):
    """#365：舊版讀到「自動加空格」選 all 的設定檔時照 on，並保留新欄位。"""

    @classmethod
    def setUpClass(cls):
        cls.temp = Path(tempfile.mkdtemp(prefix="0c-old-settings-"))
        cls.current = cls.temp / "0c-update-current"
        subprocess.run([os.environ.get("GO", "go"), "build", "-o", str(cls.current), "./cmd/0c-update"], cwd=ROOT, check=True)
        chosen = [v for v in versions() if v in SPACING_VERSIONS] if os.environ.get("ZC_OLD_UPDATERS") else SPACING_VERSIONS
        cls.old = {v: fetch_updater(v) for v in chosen}

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.temp, ignore_errors=True)

    def home(self):
        home = Path(tempfile.mkdtemp(prefix="home-", dir=self.temp))
        (home / "run").mkdir()
        env = {"HOME": str(home), "PATH": "/usr/bin:/bin", "XDG_RUNTIME_DIR": str(home / "run"), "XDG_CONFIG_HOME": str(home / ".config"),
               "XDG_DATA_HOME": str(home / ".local/share"), "DBUS_SESSION_BUS_ADDRESS": "unix:path=" + str(home / "no-bus")}
        return home / ".local/share/0c/settings.json", env

    def run_updater(self, updater, env, *args):
        run = subprocess.run([str(updater), *args], env=env, capture_output=True, text=True, timeout=120)
        self.assertEqual(run.returncode, 0, f"{updater} {args}：{run.stdout}\n{run.stderr}")
        return run.stdout

    def spacing(self, updater, env):
        doc = json.loads(self.run_updater(updater, env, "settings", "-json"))
        return next(item["value"] for item in doc["items"] if item["key"] == "cjk-latin-space")

    def test_old_updaters_read_all_as_on_and_keep_the_new_field(self):
        self.assertTrue(self.old, "沒有可測的舊版")
        for version, updater in sorted(self.old.items(), key=lambda kv: number(kv[0])):
            with self.subTest(version=version):
                path, env = self.home()
                self.run_updater(self.current, env, "set", "cjk-latin-space", "all")
                saved = json.loads(path.read_text())
                self.assertEqual((saved["cjk_latin_space"], saved["cjk_digit_space"]), (True, True))
                # 舊版：照 on，改別的設定存檔後新欄位還在。
                self.assertEqual(self.spacing(updater, env), "on")
                self.run_updater(updater, env, "set", "output-simplified", "on")
                saved = json.loads(path.read_text())
                self.assertEqual((saved["cjk_latin_space"], saved["cjk_digit_space"], saved["output_simplified"]), (True, True, True), saved)
                self.assertEqual(self.spacing(self.current, env), "all")
                # 舊版關掉中英文：留下 false＋true。目前的版本讀成關閉，再選 on 就是 on。
                self.run_updater(updater, env, "set", "cjk-latin-space", "off")
                saved = json.loads(path.read_text())
                self.assertEqual((saved["cjk_latin_space"], saved["cjk_digit_space"]), (False, True), saved)
                self.assertEqual(self.spacing(self.current, env), "off")
                self.run_updater(self.current, env, "set", "cjk-latin-space", "on")
                saved = json.loads(path.read_text())
                self.assertEqual((saved["cjk_latin_space"], saved["cjk_digit_space"]), (True, False), saved)
                self.assertEqual(self.spacing(updater, env), "on")


if __name__ == "__main__":
    unittest.main()
