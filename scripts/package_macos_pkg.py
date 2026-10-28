#!/usr/bin/env python3
"""把 package_macos.py 產生的 macOS 套件（tar.gz）包成雙擊安裝的 .pkg。

pkg 不直接放檔案（nopayload）：完整套件放在安裝腳本目錄，由 postinstall 執行套件內的
`0c-update install -bundle`，沿用逐檔雜湊核對、備份、自動更新排程與輸入來源啟用。只裝到目前使用者的
家目錄（~/Library/Input Methods），不需要系統管理員密碼。在 macOS 執行；不安裝、不上傳。
"""

import argparse
import html
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

from install_package import digest
from macos_version import bundle_version

IDENTIFIER = "com.biggo.inputmethod.0c.pkg"

POSTINSTALL = """#!/bin/sh
# 雙擊安裝時由 macOS 安裝程式以目前使用者身分執行；輸出記在 /var/log/install.log。
# sudo installer -target /、MDM 會以 root 執行，0c-update 自己改以登入的使用者身分安裝（#198）。
set -eu
BUNDLE="$(cd "$(dirname "$0")" && pwd)/{name}"
exec "$BUNDLE/0c-update" install -bundle "$BUNDLE" -channel {channel} </dev/null
"""

DISTRIBUTION = """<?xml version="1.0" encoding="utf-8"?>
<installer-gui-script minSpecVersion="2">
    <title>靈犀注音 {version}</title>
    <options customize="never" require-scripts="false" hostArchitectures="arm64"/>
    <domains enable_anywhere="false" enable_currentUserHome="true" enable_localSystem="false"/>
    <volume-check>
        <allowed-os-versions><os-version min="13.0"/></allowed-os-versions>
    </volume-check>
    <installation-check script="checkInstalledVersion()"/>
    <script><![CDATA[
// 已安裝較新的版本時，在開始安裝前說明原因。postinstall 的 0c-update 照樣會拒絕降級，但安裝程式對
// postinstall 失敗只顯示通用的「安裝失敗」，原因只寫在 install.log。CFBundleVersion 由 macos_version.py
// 從版本推出、隨版本嚴格遞增（正式版排在同版號的預發行之後），可以直接比大小。讀不到時不擋，交給 0c-update。
function checkInstalledVersion() {{
    var home = system.env.HOME;
    if (!home) {{ return true; }}
    var app = system.files.bundleAtPath(home + "/Library/Input Methods/0c.app");
    if (!app || !app.CFBundleVersion) {{ return true; }}
    if (system.compareVersions(app.CFBundleVersion, "{bundle_version}") <= 0) {{ return true; }}
    var installed = app.CFBundleShortVersionString || app.CFBundleVersion;
    my.result.type = "Fatal";
    my.result.title = "已安裝較新的靈犀注音";
    my.result.message = "這台電腦已經裝了較新的靈犀注音（" + installed + "），這個安裝檔是 {version}，不能直接覆蓋成舊版。"
        + "要回到上一個版本，請在終端機執行 \\"$HOME/Library/Application Support/0c/bin/0c-update\\" rollback；"
        + "要改裝這個版本，請先執行同一個程式的 uninstall（個人學習的詞與設定會保留），再重新打開這個安裝檔。";
    return false;
}}
    ]]></script>
    <welcome file="welcome.html" mime-type="text/html"/>
    <conclusion file="conclusion.html" mime-type="text/html"/>
    <choices-outline><line choice="0c"/></choices-outline>
    <choice id="0c" title="靈犀注音"><pkg-ref id="{identifier}"/></choice>
    <pkg-ref id="{identifier}" version="{version}" onConclusion="none">0c.pkg</pkg-ref>
</installer-gui-script>
"""

PAGE = """<!doctype html><html><head><meta charset="utf-8"><style>
body {{ font: 13px -apple-system, "PingFang TC", sans-serif; line-height: 1.6; }}
</style></head><body>{body}</body></html>
"""

WELCOME = """<h2>靈犀注音 {version}</h2>
<p>這會把靈犀注音輸入法安裝到你的帳號（<code>~/Library/Input Methods</code>），不需要系統管理員密碼。</p>
<p>已安裝舊版時會直接升級，不必登出；個人學習的詞與設定都會保留，舊版另存備份。</p>
<p>安裝後會自動在背景檢查更新。</p>"""

CONCLUSION = """<h2>安裝完成</h2>
<p>如果系統詢問是否允許「靈犀注音」，請按「允許」；第一次安裝會連續問兩次，兩次都按「允許」。</p>
<p>從舊版升級時不必登出：下一次切換到「靈犀注音」或開始打字，就是新版。</p>
<p>第一次安裝時，選單列找不到的話，到「系統設定 → 鍵盤 → 文字輸入 → 編輯 → ＋ → 繁體中文」加入「靈犀注音」。</p>
<p><b>如果輸入來源清單一直沒有出現「靈犀注音」，請先儲存工作，登出目前帳號再重新登入，然後回到上述設定加入。</b></p>
<p>切換：第一次安裝預設用系統的輸入來源切換（例如 Control＋空白鍵或地球鍵）；從舊版升級的保留原本的單按右 Shift。要開啟或關閉單按 Shift 切換注音／英文，可以在設定程式「中英切換」頁的「中英切換鍵」調整。</p>
<p>移除：在終端機執行 <code>~/Library/Application\\ Support/0c/bin/0c-update uninstall</code>。</p>"""


def distribution(version):
    return DISTRIBUTION.format(version=html.escape(version), identifier=IDENTIFIER, bundle_version=bundle_version(version))


def build(archive, out, identity=None, keychain=None, notary=None):
    if sys.platform != "darwin":
        raise ValueError(".pkg 需在 macOS 建置")
    name = archive.name.removesuffix(".tar.gz")
    if not name.startswith("0c-") or "-macos-" not in name or name == archive.name:
        raise ValueError(f"不是 macOS 套件：{archive.name}")
    pkg = out / (name + ".pkg")
    if pkg.exists():
        raise ValueError(f"輸出已存在：{pkg}")
    with tempfile.TemporaryDirectory(prefix="0c-pkg-") as temp:
        temp = Path(temp)
        scripts = temp / "scripts"
        scripts.mkdir()
        # bsdtar 保留執行權限；套件不含連結（package_macos.py 已拒絕）
        subprocess.run(["tar", "-xzf", str(archive), "-C", str(scripts)], check=True)
        bundle = scripts / name
        subprocess.run([str(bundle / "0c-update"), "verify", "-bundle", str(bundle)], check=True)
        release = json.loads((bundle / "release.json").read_text(encoding="utf-8"))
        version, channel = release["version"], release["channel"]
        (scripts / "postinstall").write_text(POSTINSTALL.format(name=name, channel=channel), encoding="utf-8")
        (scripts / "postinstall").chmod(0o755)
        subprocess.run(["pkgbuild", "--nopayload", "--scripts", str(scripts), "--identifier", IDENTIFIER,
                        "--version", version, str(temp / "0c.pkg")], check=True)
        resources = temp / "resources"
        resources.mkdir()
        v = html.escape(version)
        for file, body in (("welcome.html", WELCOME.format(version=v)), ("conclusion.html", CONCLUSION)):
            (resources / file).write_text(PAGE.format(body=body), encoding="utf-8")
        (temp / "distribution.xml").write_text(distribution(version), encoding="utf-8")
        cmd = ["productbuild", "--distribution", str(temp / "distribution.xml"), "--resources", str(resources),
               "--package-path", str(temp)]
        if identity:
            cmd += ["--sign", identity, "--timestamp"] + (["--keychain", str(keychain)] if keychain else [])
        built = temp / pkg.name
        subprocess.run(cmd + [str(built)], check=True)
        if notary:
            notarize(built, notary, keychain)
        out.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(built, pkg)
    pkg.with_suffix(".pkg.sha256").write_text(f"{digest(pkg)}  {pkg.name}\n")
    return pkg


def notarize(pkg, profile, keychain=None):
    """送 Apple 公證、staple 票據，再以 Gatekeeper 的安裝檔規則核對。"""
    auth = ["--keychain-profile", profile] + (["--keychain", str(keychain)] if keychain else [])
    out = subprocess.run(["xcrun", "notarytool", "submit", str(pkg), *auth, "--wait", "--timeout", "60m",
                          "--output-format", "json"], capture_output=True, text=True)
    try:
        result = json.loads(out.stdout)
    except ValueError:
        raise ValueError(f"公證送出失敗：{out.stderr.strip() or out.stdout.strip()}")
    if result.get("status") != "Accepted":
        log = ""
        if result.get("id"):
            log = subprocess.run(["xcrun", "notarytool", "log", result["id"], *auth], capture_output=True, text=True).stdout
        raise ValueError(f"pkg 公證未通過（{result.get('status')}，{result.get('id')}）：{result.get('message', '')}\n{log}")
    print(f"pkg 公證通過：{result['id']}", file=sys.stderr)
    for action in ("staple", "validate"):
        run = subprocess.run(["xcrun", "stapler", action, str(pkg)], capture_output=True, text=True)
        if run.returncode != 0:
            raise ValueError(f"stapler {action} 失敗（{result['id']}）：{(run.stderr or run.stdout).strip()}")
    assess = subprocess.run(["spctl", "--assess", "--type", "install", "-vv", str(pkg)], capture_output=True, text=True)
    if assess.returncode != 0 or "Notarized Developer ID" not in assess.stderr:
        raise ValueError(f"Gatekeeper 未接受公證後的 pkg：{assess.stderr.strip()}")


def main():
    parser = argparse.ArgumentParser(description="把 macOS 套件 tar.gz 包成 .pkg；不安裝、不上傳")
    parser.add_argument("archive", type=Path, help="package_macos.py 的輸出 0c-<版本>-macos-arm64.tar.gz")
    parser.add_argument("--out", type=Path, help="輸出目錄（預設與壓縮包相同）")
    parser.add_argument("--installer-identity", help="Developer ID Installer 憑證（SHA-1 或名稱）；未指定時不簽章")
    parser.add_argument("--keychain", type=Path, help="存放簽章憑證與公證帳密的鑰匙圈（需事先解鎖）")
    parser.add_argument("--notary-profile", help="notarytool store-credentials 的設定名稱；指定時送 Apple 公證並 staple")
    args = parser.parse_args()
    if args.notary_profile and not args.installer_identity:
        parser.error("--notary-profile 需要搭配 --installer-identity（公證只接受已簽章的 pkg）")
    if "-preview" not in args.archive.name and not args.notary_profile:
        parser.error("公開包的 pkg 必須以 Developer ID Installer 簽章並經 Apple 公證")
    try:
        print(build(args.archive.resolve(), (args.out or args.archive.parent).resolve(), args.installer_identity,
                    args.keychain, args.notary_profile))
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        print(f"pkg 打包失敗：{error}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
