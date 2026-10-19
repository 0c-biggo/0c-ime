#!/usr/bin/env python3
"""Build a Windows x64 bundle on Windows; never install, register, or upload.

The bundle also carries an i686 TSF DLL at x86/0c_tsf.dll for 32-bit applications (#112); the engine,
candidate window and updater stay x64."""
import argparse
import json
import os
from pathlib import Path
import platform
import shutil
import struct
import subprocess
import sys
import tempfile
import zipfile

from install_package import digest, regular
from collect_notices import collect
from package_release import ROOT, DATA_FILES, check_licenses, copy_licenses, check_release_docs, copy_release_docs, check_data, copy_neural, copy_english, seal_data

# 內嵌 Python 的 ._pth 或 PYTHONPATH 指向另一份原始碼時，會打包出那份程式（2026-10-01 曾因沿用舊建置目錄的
# Python，把 dev.21 的程式標成新版本）。
if ROOT != Path(__file__).resolve().parents[1]:
    raise SystemExit(f"打包模組來自另一份原始碼：{ROOT}；請檢查 Python 的 ._pth 或 PYTHONPATH")

TARGET = "x86_64-pc-windows-msvc"
# 32 位元應用程式（例如 32 位元 Office、HCL Notes 12）只載入 32 位元的 TSF DLL（#112）。只建 DLL：引擎與候選窗
# 由 x64 那份經 named pipe 服務。C 執行階段靜態連結：32 位元的 Visual C++ 可轉散發套件不一定有裝。
TARGET32 = "i686-pc-windows-msvc"
DLL32 = "x86/0c_tsf.dll"  # 與 internal/update 的 WindowsDLL32 相同
PLATFORM = ROOT / "platforms/windows"
PE_MACHINE = {"x64": b"\x64\x86", "x86": b"\x4c\x01"}


def check_pe(path, machine="x64"):
    with regular(path).open("rb") as stream:
        if stream.read(2) != b"MZ":
            raise ValueError(f"不是 Windows PE 執行檔：{path}")
        stream.seek(0x3c)
        offset = stream.read(4)
        if len(offset) != 4:
            raise ValueError(f"PE 標頭不完整：{path}")
        stream.seek(struct.unpack("<I", offset)[0])
        if stream.read(6) != b"PE\0\0" + PE_MACHINE[machine]:
            raise ValueError(f"需要 Windows {machine} PE：{path}")


# 檔案內容（說明）出現在檔案內容對話框、工作管理員與 SmartScreen／Defender 的報告；發行者與 Artifact Signing 的
# 法定名稱一致。
PUBLISHER = "FUNMULA PTE. LTD."
PRODUCT = "靈犀注音（0c）"
DESCRIPTIONS = {
    "0c_tsf.dll": "靈犀注音輸入法（TSF 文字服務）",
    "0c-ui.exe": "靈犀注音候選窗",
    "0c-server.exe": "靈犀注音引擎",
    "0c-update.exe": "靈犀注音安裝與更新工具",
    "0c-settings.exe": "靈犀注音設定程式",
    "0c-mcp.exe": "靈犀注音 AI 工具轉接（MCP）",
}


def file_version(version):
    """數字版本只取 x.y.z.0（與安裝精靈相同）；預覽序號留在字串欄位，避免正式版的數字反而比預覽版小。"""
    return tuple(int(part) for part in version.split("-")[0].split(".")) + (0,)


def version_info(name, version, dll):
    """VS_VERSIONINFO 資源（RT_VERSION）：固定欄位＋一個 zh-TW／UTF-16 字串表。"""
    def pad(data):
        return data + b"\0" * (-len(data) % 4)

    def node(key, value=b"", text=False, children=b""):
        # 每個節點：wLength、wValueLength（文字以字元計、二進位以位元組計）、wType，鍵名與值各自對齊 4 位元組。
        key_part = pad(b"\0" * 6 + (key + "\0").encode("utf-16-le"))[6:]
        rest = key_part + pad(value) + children
        value_length = len(value) // 2 if text else len(value)
        return pad(struct.pack("<HHH", 6 + len(rest), value_length, 1 if text else 0) + rest)

    def text(key, value):
        return node(key, (value + "\0").encode("utf-16-le"), text=True)

    major, minor, patch, build = file_version(version)
    fixed = struct.pack("<13I", 0xFEEF04BD, 0x00010000, major << 16 | minor, patch << 16 | build,
                        major << 16 | minor, patch << 16 | build, 0x3F, 0, 0x00040004, 2 if dll else 1, 0, 0, 0)
    strings = b"".join(text(k, v) for k, v in (
        ("CompanyName", PUBLISHER), ("FileDescription", DESCRIPTIONS[name]), ("FileVersion", version),
        ("InternalName", name.rsplit(".", 1)[0]), ("LegalCopyright", f"© {PUBLISHER}"),
        ("OriginalFilename", name), ("ProductName", PRODUCT), ("ProductVersion", version)))
    table = node("040404B0", children=strings)
    translation = node("Translation", struct.pack("<HH", 0x0404, 1200))
    children = node("StringFileInfo", children=table) + node("VarFileInfo", children=translation)
    return node("VS_VERSION_INFO", fixed, children=children)


def existing_version_languages(path):
    """檔案內已有的版本資源（RT_VERSION、ID 1）的語言；沒有時為空。Tauri 編出的 exe 已經帶一份（0.1.0、
    識別碼當公司名），不先移除，檔案會同時有兩份不同語言的版本資訊。"""
    import ctypes
    from ctypes import wintypes
    kernel32 = ctypes.WinDLL("kernel32", use_last_error=True)
    kernel32.LoadLibraryExW.restype = wintypes.HMODULE
    kernel32.LoadLibraryExW.argtypes = [wintypes.LPCWSTR, wintypes.HANDLE, wintypes.DWORD]
    kernel32.EnumResourceLanguagesW.argtypes = [wintypes.HMODULE, ctypes.c_void_p, ctypes.c_void_p, ctypes.c_void_p, ctypes.c_ssize_t]
    kernel32.FreeLibrary.argtypes = [wintypes.HMODULE]
    module = kernel32.LoadLibraryExW(str(path), None, 0x2)  # LOAD_LIBRARY_AS_DATAFILE
    if not module:
        raise OSError(ctypes.get_last_error(), f"無法讀取檔案資源：{path}")
    languages = []
    callback_type = ctypes.WINFUNCTYPE(wintypes.BOOL, wintypes.HMODULE, ctypes.c_void_p, ctypes.c_void_p, wintypes.WORD, ctypes.c_ssize_t)

    def collect_language(_module, _kind, _name, language, _param):
        languages.append(language)
        return True

    callback = callback_type(collect_language)
    try:
        kernel32.EnumResourceLanguagesW(module, 16, 1, ctypes.cast(callback, ctypes.c_void_p), 0)  # 沒有這個資源時回傳失敗，languages 維持空
    finally:
        kernel32.FreeLibrary(module)
    return languages


def stamp_version(path, version):
    """以 Win32 UpdateResource 寫入版本資訊；必須在簽章與計算雜湊之前。"""
    import ctypes
    from ctypes import wintypes
    languages = existing_version_languages(path)
    kernel32 = ctypes.WinDLL("kernel32", use_last_error=True)
    kernel32.BeginUpdateResourceW.restype = wintypes.HANDLE
    kernel32.BeginUpdateResourceW.argtypes = [wintypes.LPCWSTR, wintypes.BOOL]
    kernel32.UpdateResourceW.argtypes = [wintypes.HANDLE, ctypes.c_void_p, ctypes.c_void_p, wintypes.WORD,
                                         ctypes.c_void_p, wintypes.DWORD]
    kernel32.EndUpdateResourceW.argtypes = [wintypes.HANDLE, wintypes.BOOL]
    data = version_info(path.name, version, path.suffix.lower() == ".dll")
    handle = kernel32.BeginUpdateResourceW(str(path), False)
    if not handle:
        raise OSError(ctypes.get_last_error(), f"無法寫入版本資訊：{path}")
    ok = all(kernel32.UpdateResourceW(handle, 16, 1, language, None, 0) for language in languages if language != 0x0404)
    buffer = ctypes.create_string_buffer(data, len(data))
    ok = ok and kernel32.UpdateResourceW(handle, 16, 1, 0x0404, buffer, len(data))  # RT_VERSION、ID 1
    if not kernel32.EndUpdateResourceW(handle, not ok) or not ok:
        raise OSError(ctypes.get_last_error(), f"無法寫入版本資訊：{path}")


TIMESTAMP_URL = "http://timestamp.acs.microsoft.com"
PE_SUFFIXES = (".exe", ".dll", ".pyd")
NEURAL_CRT = ("msvcp140.dll", "msvcp140_1.dll")  # scripts/prepare_neural.py 放在 python.exe 旁


def signer(path, args):
    """回傳 signtool verify /pa 認可的簽署者（葉憑證的 Issued to）；未簽章或驗證失敗時為 None。"""
    run = subprocess.run([str(args.signtool), "verify", "/pa", "/v", str(path)], capture_output=True, text=True,
                         encoding="utf-8", errors="replace")
    if run.returncode != 0:
        return None
    return leaf_signer(run.stdout)


def leaf_signer(output):
    """signtool verify /v 先列簽章憑證鏈（根到葉），之後才是時間戳的憑證鏈；取時間戳段落之前最後一個 Issued to。"""
    names = []
    for line in output.splitlines():
        line = line.strip()
        if line.startswith("The signature is timestamped") or line.startswith("Timestamp Verified by"):
            break
        if line.startswith("Issued to:"):
            names.append(line.split(":", 1)[1].strip())
    return names[-1] if names else None


def authenticode_sign(paths, args):
    """以 Artifact Signing（signtool＋dlib）簽章並加時間戳，簽完逐檔核對發行者。"""
    if not paths:
        return
    subprocess.run([str(args.signtool), "sign", "/fd", "SHA256", "/tr", TIMESTAMP_URL, "/td", "SHA256",
                    "/dlib", str(args.sign_dlib), "/dmdf", str(args.sign_metadata), *map(str, paths)], check=True)
    for path in paths:
        if signer(path, args) != PUBLISHER:
            raise ValueError(f"簽章後核對失敗（發行者不是 {PUBLISHER}）：{path}")


def unsigned_pe(root, args):
    """內附元件中沒有有效原廠簽章的 PE（例如 numpy 的 .pyd）；已有其他發行者簽章的保留原狀。"""
    return [p for p in sorted(root.rglob("*")) if p.is_file() and p.suffix.lower() in PE_SUFFIXES and signer(p, args) is None]


def check_settings(directory):
    """檢查 scripts/build_settings_windows.py 的輸出（0c-settings.exe 與 NOTICES.txt），回傳 (執行檔, 授權文字)。

    不在打包流程裡建置：Tauri 需要另一個 Rust 工作區與較長的編譯時間，改由事先建好再交給打包；
    這裡只確認它是 Windows x64 的 PE 而不是別的東西。
    """
    program = regular(Path(directory) / "0c-settings.exe")
    check_pe(program)
    notices = regular(Path(directory) / "NOTICES.txt").read_text(encoding="utf-8")
    if not notices.strip():
        raise ValueError("設定程式缺少第三方授權聲明")
    return program, notices


def server_version(args, binary):
    """E2E fault package F: the engine reports a different version, so the post-switch health check fails."""
    if args.e2e_fault == "version-mismatch" and binary == "0c-server":
        return args.version + "-fault"
    return args.version


def build(args):
    if sys.platform != "win32" or platform.machine().lower() not in ("amd64", "x86_64"):
        raise ValueError("請在 Windows x64 的建置環境執行")
    data = args.data.resolve()
    manifest = check_data(data, not args.preview)
    check_licenses()
    release_docs = check_release_docs(getattr(args, "release_docs", None))
    revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    dirty = bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT, text=True).strip())
    if dirty and not args.preview:
        raise ValueError("公開包需要乾淨且已提交的程式碼；未提交版本請用 --preview")
    # Keep version syntax identical to the updater before using it in a path.
    from package_release import valid_version
    if not valid_version(args.version):
        raise ValueError("版本請用 0.1.0-beta.1 形式")
    settings, settings_notices = check_settings(args.settings) if args.settings else (None, "")
    args.out.mkdir(parents=True, exist_ok=True)
    name = f"0c-{args.version}-windows-amd64" + ("-preview" if args.preview else "")
    archive = args.out / (name + ".zip")
    checksum = archive.with_suffix(".zip.sha256")
    if archive.exists() or checksum.exists():
        raise ValueError(f"輸出已存在，請使用新版本或新目錄：{archive}")
    with tempfile.TemporaryDirectory(prefix="0c-windows-") as temp:
        stage = Path(temp) / name
        stage.mkdir()
        def copy(src, dst):
            target = stage / dst
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(regular(src), target)
        # Force the documented target even if the shell has another default toolchain.
        subprocess.run([args.cargo, "build", "--locked", "--release", "--target", TARGET], cwd=PLATFORM, check=True)
        for binary in ("0c_tsf.dll", "0c-ui.exe"):
            copy(PLATFORM / "target" / TARGET / "release" / binary, binary)
        env32 = dict(os.environ, CARGO_TARGET_I686_PC_WINDOWS_MSVC_RUSTFLAGS="-C target-feature=+crt-static")
        subprocess.run([args.cargo, "build", "--locked", "--release", "--target", TARGET32, "-p", "zuin-tsf"],
                       cwd=PLATFORM, env=env32, check=True)
        copy(PLATFORM / "target" / TARGET32 / "release" / "0c_tsf.dll", DLL32)
        env = dict(os.environ, GOOS="windows", GOARCH="amd64", CGO_ENABLED="0")
        # 0c-mcp：只支援 stdio 的 AI 工具用的轉接（#126）；0c-update 安裝時把它複製到不隨版本變動的
        # %LOCALAPPDATA%\Zuin\bin\0c-mcp.exe（穩定副本），設定程式把那個路徑填進範例。
        for binary in ("0c-server", "0c-update", "0c-mcp"):
            tags = ["-tags", "e2etest"] if args.e2etest and binary in ("0c-update", "0c-server") else []
            subprocess.run([args.go, "build", "-trimpath", *tags, "-ldflags", f"-X main.buildVersion={server_version(args, binary)}",
                            "-o", str(stage / (binary + ".exe")), f"./cmd/{binary}"],
                           cwd=ROOT, env=env, check=True)
        if settings:
            copy(settings, "0c-settings.exe")
        ours = [stage / b for b in ("0c_tsf.dll", "0c-ui.exe", "0c-server.exe", "0c-update.exe", "0c-mcp.exe") + (("0c-settings.exe",) if settings else ())]
        for binary in ours:
            check_pe(binary)
        check_pe(stage / DLL32, "x86")
        ours.append(stage / DLL32)
        for binary in ours:
            stamp_version(binary, args.version)
        # 先寫版本資訊再簽章；之後才計算 release.json 與 SHA256SUMS 的雜湊。
        if args.sign:
            authenticode_sign(ours, args)
        for filename in DATA_FILES:
            copy(data / filename, "data/" + filename)
        copy_english(data, stage / "data", args.go)
        if args.neural:
            copy_neural(args.neural, stage)
            check_pe(stage / "neural/python/python.exe")
            # 沒有這兩個檔，沒裝 Visual C++ 可轉散發套件的電腦載入不了 onnxruntime（#80）；沿用舊版 neural/ 時在這裡擋下。
            # 迴圈變數不可用 name：它是套件名稱，後面要當 ZIP 頂層目錄。
            for crt in NEURAL_CRT:
                check_pe(stage / "neural/python" / crt)
            if args.sign:
                for crt in NEURAL_CRT:
                    if not (signer(stage / "neural/python" / crt, args) or "").startswith("Microsoft"):
                        raise ValueError(f"內附的 {crt} 不是 Microsoft 簽章")
                authenticode_sign(unsigned_pe(stage / "neural", args), args)
        seal_data(stage, args.go, public=not args.preview)
        copy(PLATFORM / "install.ps1", "install.ps1")
        copy_release_docs(release_docs, stage)
        copy_licenses(stage)
        (stage / "THIRD_PARTY_NOTICES.txt").write_text(
            collect("windows-amd64", stage / "neural" if args.neural else None, go=args.go, cargo=args.cargo)
            + ("\n" + settings_notices if settings_notices else ""), encoding="utf-8")
        if args.preview:
            (stage / "PREVIEW.txt").write_text("內部驗證包，非公開發行版。\n", encoding="utf-8")
        subprocess.run([str(stage / "0c-server.exe"), "-check-data", "-data", str(stage / "data")], check=True)
        release = {"schema": 1, "product": "0c", "name": "靈犀注音", "version": args.version,
                   "channel": "internal-preview" if args.preview else "public-beta", "os": "windows", "arch": "amd64",
                   "revision": revision, "dirty": dirty, "build_platform": platform.platform(),
                   "data_format": manifest["format"], "data_manifest_sha256": digest(stage / "data/manifest.json"),
                   "files": {p.relative_to(stage).as_posix(): digest(p) for p in sorted(stage.rglob("*")) if p.is_file()}}
        (stage / "release.json").write_text(json.dumps(release, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        subprocess.run([str(stage / "0c-update.exe"), "verify", "-bundle", str(stage)], check=True)
        (stage / "SHA256SUMS").write_text("".join(f"{digest(p)}  {p.relative_to(stage).as_posix()}\n"
                                                 for p in sorted(stage.rglob("*")) if p.is_file() and p.name != "SHA256SUMS"), encoding="utf-8")
        fd, temporary = tempfile.mkstemp(prefix=".0c-", dir=args.out)
        os.close(fd)
        try:
            with zipfile.ZipFile(temporary, "w", zipfile.ZIP_DEFLATED) as zip_file:
                for p in sorted(stage.rglob("*")):
                    if p.is_file():
                        zip_file.write(p, f"{name}/{p.relative_to(stage).as_posix()}")
            os.replace(temporary, archive)
        finally:
            Path(temporary).unlink(missing_ok=True)
        checksum.write_text(f"{digest(archive)}  {archive.name}\n", encoding="utf-8")
    return archive


def main():
    parser = argparse.ArgumentParser(description="建立靈犀注音 Windows x64 完整套件；不安裝、不註冊、不上傳")
    parser.add_argument("--release-docs", required=True, type=Path, help="外部文件目錄，須含 INSTALL.md、PRIVACY.md、UPDATING.md")
    parser.add_argument("--data", required=True, type=Path)
    parser.add_argument("--version", required=True)
    parser.add_argument("--out", type=Path, default=ROOT / "build/release")
    parser.add_argument("--preview", action="store_true")
    parser.add_argument("--e2e-fault", choices=["version-mismatch"], help="E2E 故障套件 F：引擎回報不符的版本（須搭配 --e2etest）")
    parser.add_argument("--e2etest", action="store_true", help="E2E 測試版：更新器信任測試金鑰／CA，更新器與引擎包含可控同步點；須搭配 --preview")
    parser.add_argument("--go", default="go")
    parser.add_argument("--neural", type=Path, help="scripts/prepare_neural.py --platform windows-amd64 產生的 neural/ 目錄")
    parser.add_argument("--cargo", default="cargo")
    parser.add_argument("--settings", type=Path, help="scripts/build_settings_windows.py 產生的目錄（0c-settings.exe 與 NOTICES.txt）；省略則不內附設定程式")
    parser.add_argument("--signtool", type=Path, help="Windows SDK x64 signtool.exe")
    parser.add_argument("--sign-dlib", type=Path, help="Artifact Signing 用戶端的 Azure.CodeSigning.Dlib.dll")
    parser.add_argument("--sign-metadata", type=Path, help="Artifact Signing metadata.json（端點、帳戶、憑證設定檔）")
    try:
        args = parser.parse_args()
        sign = (args.signtool, args.sign_dlib, args.sign_metadata)
        if any(sign) and not all(sign):
            parser.error("簽章需要同時指定 --signtool、--sign-dlib、--sign-metadata")
        args.sign = all(sign)
        if not args.preview and not args.sign:
            parser.error("公開包必須簽章：請指定 --signtool、--sign-dlib、--sign-metadata")
        if args.e2etest and not args.preview:
            parser.error("--e2etest 只能用於 --preview 套件")
        if args.e2e_fault and not args.e2etest:
            parser.error("--e2e-fault 只能用於 --e2etest 套件")
        print(build(args))
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        print(f"打包失敗：{error}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
