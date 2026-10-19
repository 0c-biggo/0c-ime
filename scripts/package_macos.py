#!/usr/bin/env python3
"""Build a local macOS (Apple Silicon) bundle: 0c.app with engine, paired dictionary and neural models.

Runs on macOS with Xcode command line tools and Go. Never installs or uploads. Without an Apple Developer ID
the app is ad-hoc signed; 0c-update verifies every file before installing it.
"""

import argparse
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tarfile
import tempfile

from install_package import digest, regular
from collect_notices import collect
from package_release import ROOT, DATA_FILES, check_licenses, copy_licenses, check_release_docs, copy_release_docs, check_data, copy_english, prepare_shipped_data, valid_version
from macos_version import bundle_version, short_version
import package_macos_pkg

# 內嵌 Python 的 ._pth 或 PYTHONPATH 指向另一份原始碼時，會打包出那份程式（2026-10-01 曾因沿用舊建置目錄的
# Python，把 dev.21 的程式標成新版本）。
if ROOT != Path(__file__).resolve().parents[1]:
    raise SystemExit(f"打包模組來自另一份原始碼：{ROOT}；請檢查 Python 的 ._pth 或 PYTHONPATH")

PLATFORM = ROOT / "platforms/macos"
SETTINGS_BUNDLE = "0c-settings.app"
SETTINGS_EXECUTABLE = "0c-settings"
SETTINGS_LAUNCHER = "Contents/Helpers/0c-settings-launcher.app"
# 64 位元 Mach-O（魔術數字 0xfeedfacf，小端序）與 arm64 的 CPU 類型（0x0100000c）。
MACHO_ARM64 = bytes.fromhex("cffaedfe") + bytes.fromhex("0c000001")


def check_settings(directory):
    """檢查 scripts/build_settings_macos.py 的輸出（0c-settings.app 與 NOTICES.txt），回傳 (App 路徑, 授權文字)。

    不在打包流程裡建置：Tauri 需要 Rust 工具鏈，改由事先建好再交給打包。App 內只接受一般檔案，執行檔必須是
    arm64 的 Mach-O，避免把錯的二進位打進套件。
    """
    directory = Path(directory)
    app = directory / SETTINGS_BUNDLE
    if not app.is_dir() or app.is_symlink():
        raise ValueError(f"找不到設定程式：{app}")
    for p in app.rglob("*"):
        if p.is_symlink():
            raise ValueError(f"設定程式不接受符號連結：{p.relative_to(app)}")
    if not regular(app / "Contents/Info.plist").stat().st_size:
        raise ValueError("設定程式缺少 Info.plist")
    program = regular(app / "Contents/MacOS" / SETTINGS_EXECUTABLE)
    if program.read_bytes()[:8] != MACHO_ARM64:
        raise ValueError(f"設定程式不是 arm64 的 macOS 執行檔：{program}")
    notices = regular(directory / "NOTICES.txt").read_text(encoding="utf-8")
    if not notices.strip():
        raise ValueError("設定程式缺少第三方授權聲明")
    return app, notices


def build(args):
    if sys.platform != "darwin" or platform.machine() != "arm64":
        raise ValueError("macOS 套件需在 Apple Silicon 的 macOS 原生建置")
    if not valid_version(args.version):
        raise ValueError("版本請用 0.1.0-beta.1 形式")
    bundle_version(args.version)  # macOS 的 CFBundleVersion 要能由版本推出，建置前先檢查
    check_licenses()
    data = args.data.resolve()
    manifest = check_data(data, not args.preview)
    release_docs = check_release_docs(getattr(args, "release_docs", None))
    settings_app, settings_notices = check_settings(args.settings) if args.settings else (None, "")
    if args.neural:
        select = json.loads(regular(args.neural / "select/manifest.json").read_text(encoding="utf-8"))
        if select.get("dictionary_sha256") != digest(data / "manifest.json"):
            raise ValueError("神經選字模型與本套件字典不配對")
        if any(p.is_symlink() for p in args.neural.rglob("*")):
            raise ValueError("神經套件不接受符號連結")
    revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    dirty = bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT, text=True).strip())
    if dirty and not args.preview:
        raise ValueError("公開包需要乾淨且已提交的程式碼；未提交版本請用 --preview")
    args.out.mkdir(parents=True, exist_ok=True)
    name = f"0c-{args.version}-macos-arm64" + ("-preview" if args.preview else "")
    archive = args.out / (name + ".tar.gz")
    if archive.exists():
        raise ValueError(f"輸出已存在，請使用新版本或新目錄：{archive}")
    with tempfile.TemporaryDirectory(prefix="0c-macos-") as temp:
        stage = Path(temp) / name
        stage.mkdir()
        tags = ["-tags", "e2etest"] if args.e2etest else []
        subprocess.run([args.go, "build", "-trimpath", *tags, "-ldflags", f"-X main.buildVersion={args.version}",
                        "-o", str(stage / "0c-update"), "./cmd/0c-update"], cwd=ROOT, check=True)
        # 只支援 stdio 的 AI 工具用的轉接（#126），放進 App 的 Contents/MacOS/0c-mcp：App 原地更新，路徑不隨版本變動，
        # 設定程式把它填進範例。簽章（developer_id_sign 逐一簽 App 內的 Mach-O）與公證跟著 App。
        mcp = Path(temp) / "0c-mcp"
        subprocess.run([args.go, "build", "-trimpath", "-o", str(mcp), "./cmd/0c-mcp"], cwd=ROOT, check=True)
        # 字典在簽章前混淆：app 的 ad-hoc 簽章涵蓋 Resources，之後不能再改內容。
        sealed = Path(temp) / "sealed-data"
        shutil.copytree(data, sealed)
        # 使用模型副本核對原有綁定；打包不修改來源紀錄或重新配對模型。
        neural = Path(temp) / "neural" if args.neural else None
        if neural:
            shutil.copytree(args.neural, neural)
        shipped_manifest = prepare_shipped_data(sealed, neural, public=not args.preview)
        copy_english(sealed, sealed, args.go)
        subprocess.run([args.go, "run", "./cmd/0c-compile", "-seal", str(sealed)], cwd=ROOT, check=True)
        # App 內附一份 0c-update 與排程 plist，以 SMAppService 註冊（系統設定顯示「靈犀注音」，#25）；
        # 套件根目錄的 0c-update 照舊供安裝與一行安裝使用。
        make = ["make", "-C", str(PLATFORM), "-B", "app", f"DATA_DIR={sealed}", f"UPDATER={stage / '0c-update'}",
                f"AGENT_INTERVAL={60 if args.e2etest and not args.production_timing else 900}",
                f"BUNDLE_VERSION={bundle_version(args.version)}", f"SHORT_VERSION={short_version(args.version)}",
                f"BUILD_VERSION={args.version}", f"MCP={mcp}"]
        if args.neural:
            make.append(f"NEURAL_DIR={neural}")
        if settings_app:
            make.append(f"SETTINGS_APP={settings_app.resolve()}")
        env = dict(os.environ, GOFLAGS=f"-ldflags=-X=main.buildVersion={args.version}")
        if os.sep in args.go:
            # The Makefile builds 0c-server with the go found on PATH.
            env["PATH"] = str(Path(args.go).resolve().parent) + os.pathsep + env.get("PATH", "")
        subprocess.run(make, check=True, env=env)
        subprocess.run([str(PLATFORM / "build/0c-server"), "-check-data", "-data", str(sealed)], check=True)
        # ditto keeps the signature intact; the bundle contains no links.
        subprocess.run(["ditto", str(PLATFORM / "build/0c.app"), str(stage / "0c.app")], check=True)
        if args.sign_identity:
            developer_id_sign(stage / "0c.app", args.sign_identity, args.keychain)
            developer_id_sign(stage / "0c-update", args.sign_identity, args.keychain)
        subprocess.run(["codesign", "--verify", "--deep", "--strict", str(stage / "0c.app")], check=True)
        if args.notary_profile:
            # 雜湊與 release.json 之前完成：staple 會在 App 內加入公證票據。
            notarize(stage, Path(temp), args.notary_profile, args.keychain)
        copy_release_docs(release_docs, stage)
        shutil.copy2(PLATFORM / "README.md", stage / "README-macOS.md")
        copy_licenses(stage)
        (stage / "THIRD_PARTY_NOTICES.txt").write_text(
            collect("macos-arm64", neural, go=args.go)
            + ("\n" + settings_notices if settings_notices else ""), encoding="utf-8")
        if args.preview:
            signing = ("App 以 Developer ID 簽章並經 Apple 公證。" if args.notary_profile else
                       "App 以 Developer ID 簽章、未經 Apple 公證。" if args.sign_identity else "App 為 ad-hoc 簽章、未經 Apple 公證。")
            (stage / "PREVIEW.txt").write_text("內部驗證包，非公開發行版。" + signing + "\n", encoding="utf-8")
        files = {}
        for p in sorted(stage.rglob("*")):
            if p.is_symlink():
                raise ValueError(f"套件不接受符號連結：{p.relative_to(stage)}")
            if p.is_file():
                files[p.relative_to(stage).as_posix()] = digest(p)
        release = {"schema": 1, "product": "0c", "name": "靈犀注音", "version": args.version,
                   "channel": "internal-preview" if args.preview else "public-beta", "os": "darwin", "arch": "arm64",
                   "revision": revision, "dirty": dirty, "build_platform": platform.platform(),
                   "data_format": manifest["format"], "data_manifest_sha256": shipped_manifest,
                   "files": files}
        (stage / "release.json").write_text(json.dumps(release, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        subprocess.run([str(stage / "0c-update"), "verify", "-bundle", str(stage)], check=True)
        (stage / "SHA256SUMS").write_text("".join(f"{digest(p)}  {p.relative_to(stage).as_posix()}\n"
                                                  for p in sorted(stage.rglob("*")) if p.is_file() and p.name != "SHA256SUMS"),
                                          encoding="utf-8")
        temporary = Path(temp) / archive.name
        with tarfile.open(temporary, "w:gz") as tar:
            tar.add(stage, arcname=name)
        shutil.copyfile(temporary, archive)
    archive.with_suffix(archive.suffix + ".sha256").write_text(f"{digest(archive)}  {archive.name}\n")
    if args.installer_identity:
        # 一般使用者雙擊安裝用；tar.gz 仍是更新索引與一行安裝使用的套件。
        print(package_macos_pkg.build(archive, args.out, args.installer_identity, args.keychain, args.notary_profile))
    return archive


def signing_order(target, is_macho):
    """簽章順序：先簽每個 Mach-O，再由深到淺簽內附的 .app（巢狀 App 要以 bundle 簽，Info.plist 與資源才會被封），
    最後簽 target 本身。外層的簽章會封住內層的簽章，所以內層一定要先完成。"""
    if not target.is_dir():
        return [target]

    def deepest_first(paths):
        return sorted(paths, key=lambda p: len(p.parts), reverse=True)

    machos = [p for p in target.rglob("*") if p.is_file() and not p.is_symlink() and is_macho(p)]
    bundles = [p for p in target.rglob("*.app") if p.is_dir() and not p.is_symlink()]
    return deepest_first(machos) + deepest_first(bundles) + [target]


def developer_id_sign(target, identity, keychain=None):
    """以 Developer ID＋hardened runtime 由內而外重簽（順序見 signing_order）；都加時間戳。
    內附 Python 在 hardened runtime 下不需要 entitlement（2026-10-01 於 macOS 26 實測）。"""
    base = ["codesign", "--force", "--options", "runtime", "--timestamp", "--sign", identity]
    if keychain:
        base += ["--keychain", str(keychain)]

    def is_macho(p):
        return subprocess.run(["file", "-b", str(p)], capture_output=True, text=True).stdout.startswith("Mach-O")

    for p in signing_order(target, is_macho):
        subprocess.run(base + [str(p)], check=True, capture_output=True)


def notarize(stage, temp, profile, keychain=None):
    """把 0c.app 與 0c-update 一起送 Apple 公證，通過後把票據 staple 到 App 並以 Gatekeeper 核對。
    0c-update 是單一執行檔，無法 staple；Gatekeeper 會向 Apple 線上查詢它的公證狀態。"""
    auth = ["--keychain-profile", profile] + (["--keychain", str(keychain)] if keychain else [])
    bundle = temp / "notarize" / "0c"
    bundle.mkdir(parents=True)
    for name in ("0c.app", "0c-update"):
        subprocess.run(["ditto", str(stage / name), str(bundle / name)], check=True)
    archive = temp / "notarize" / "0c.zip"
    subprocess.run(["ditto", "-c", "-k", "--keepParent", str(bundle), str(archive)], check=True)
    out = subprocess.run(["xcrun", "notarytool", "submit", str(archive), *auth, "--wait", "--timeout", "60m",
                          "--output-format", "json"], capture_output=True, text=True)
    try:
        result = json.loads(out.stdout)
    except ValueError:
        raise ValueError(f"公證送出失敗：{out.stderr.strip() or out.stdout.strip()}")
    if not result.get("id"):
        # 送出階段就失敗（鑰匙圈未解鎖、設定名稱錯誤、網路等），沒有可查的記錄
        raise ValueError(f"公證送出失敗：{result.get('message') or result}\n{out.stderr.strip()}")
    if result.get("status") != "Accepted":
        log = subprocess.run(["xcrun", "notarytool", "log", result["id"], *auth], capture_output=True, text=True)
        raise ValueError(f"公證未通過（{result.get('status')}，{result['id']}）：\n{log.stdout or log.stderr}")
    print(f"公證通過：{result['id']}", file=sys.stderr)
    # 啟動器會複製到 Applications，必須有自己的票據，不能只靠外層 App 的公證狀態。
    apps = [stage / "0c.app"]
    launcher = apps[0] / SETTINGS_LAUNCHER
    if launcher.is_dir():
        apps.insert(0, launcher)
    for app in apps:
        for action in ("staple", "validate"):
            run = subprocess.run(["xcrun", "stapler", action, str(app)], capture_output=True, text=True)
            if run.returncode != 0:
                raise ValueError(f"stapler {action} 失敗（{result['id']}，{app}）：{(run.stderr or run.stdout).strip()}")
        assess = subprocess.run(["spctl", "--assess", "--type", "execute", "-vv", str(app)],
                                capture_output=True, text=True)
        if assess.returncode != 0 or "Notarized Developer ID" not in assess.stderr:
            raise ValueError(f"Gatekeeper 未接受公證後的 App {app}：{assess.stderr.strip()}")
    subprocess.run(["codesign", "--verify", "--deep", "--strict", str(stage / "0c.app")], check=True)


def main():
    parser = argparse.ArgumentParser(description="建立靈犀注音 macOS（Apple Silicon）套件；不安裝、不上傳")
    parser.add_argument("--release-docs", required=True, type=Path, help="外部文件目錄，須含 INSTALL.md、PRIVACY.md、UPDATING.md")
    parser.add_argument("--data", required=True, type=Path, help="配對的系統字典目錄（" + "、".join(DATA_FILES) + "）")
    parser.add_argument("--neural", type=Path, help="prepare_neural.py --platform macos-arm64 產生的 neural/ 目錄")
    parser.add_argument("--settings", type=Path, help="scripts/build_settings_macos.py 產生的目錄（0c-settings.app 與 NOTICES.txt）；省略則不內附設定程式")
    parser.add_argument("--version", required=True)
    parser.add_argument("--preview", action="store_true", help="產生明確標示為內部驗證的套件")
    parser.add_argument("--e2etest", action="store_true", help="E2E 測試版：0c-update 以 -tags e2etest 建置（信任測試金鑰與測試 CA）；須搭配 --preview")
    parser.add_argument("--production-timing", action="store_true", help="E2E 使用正式 900 秒 OS 排程；協調器時間仍由既有設定決定")
    parser.add_argument("--out", type=Path, default=ROOT / "build/release")
    parser.add_argument("--go", default="go", help="Go 工具鏈執行檔")
    parser.add_argument("--sign-identity", help="Developer ID Application 憑證（SHA-1 或名稱）；未指定時維持 ad-hoc 簽章")
    parser.add_argument("--installer-identity", help="Developer ID Installer 憑證（SHA-1 或名稱）；指定時另外產生簽章的 .pkg 安裝檔")
    parser.add_argument("--keychain", type=Path, help="存放簽章憑證與公證帳密的鑰匙圈（需事先解鎖）")
    parser.add_argument("--notary-profile", help="notarytool store-credentials 存入的設定名稱；指定時送 Apple 公證並 staple")
    args = parser.parse_args()
    if args.e2etest and not args.preview:
        parser.error("--e2etest 只能用於 --preview 套件")
    if args.notary_profile and not args.sign_identity:
        parser.error("--notary-profile 需要搭配 --sign-identity（公證只接受 Developer ID 簽章）")
    if args.installer_identity and not args.notary_profile:
        parser.error("--installer-identity 需要搭配 --notary-profile（pkg 要公證才能雙擊安裝）")
    if not args.preview and not args.notary_profile:
        parser.error("公開包必須以 Developer ID 簽章並經 Apple 公證（--sign-identity 與 --notary-profile）")
    try:
        print(build(args))
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        print(f"打包失敗：{error}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
