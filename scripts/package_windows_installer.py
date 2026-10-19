#!/usr/bin/env python3
"""Wrap a verified Windows ZIP in a double-click installer using NSIS 3 (any host)."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import shutil
import stat
import subprocess
import sys
import tempfile
import zipfile

from install_package import digest
from package_release import ROOT

# 內嵌 Python 的 ._pth 或 PYTHONPATH 指向另一份原始碼時，會打包出那份程式（2026-10-01 曾因沿用舊建置目錄的
# Python，把 dev.21 的程式標成新版本）。
if ROOT != Path(__file__).resolve().parents[1]:
    raise SystemExit(f"打包模組來自另一份原始碼：{ROOT}；請檢查 Python 的 ._pth 或 PYTHONPATH")
from package_windows import PUBLISHER, TIMESTAMP_URL, authenticode_sign, check_pe, file_version, signer
from release_feed import metadata


def read_bundle(archive):
    release = metadata(archive)
    if (release['os'], release['arch']) != ('windows', 'amd64'):
        raise ValueError('安裝精靈需要 Windows amd64 ZIP')
    required = {'0c-update.exe', '0c-server.exe', '0c-ui.exe', '0c_tsf.dll', 'x86/0c_tsf.dll',
                'data/manifest.json', 'data/lexicon.bin', 'data/lm.bin', 'data/NOTICE.txt', 'data/report.txt'}
    required |= {'TERMS.md', 'LICENSE', 'legal/README.md', 'THIRD_PARTY_NOTICES.txt'}
    files = release.get('files', {})
    if not required.issubset(files):
        raise ValueError('套件缺少必要檔案')
    with zipfile.ZipFile(archive) as package:
        entries = package.infolist()
        if len(entries) > 10000 or sum(p.file_size for p in entries) > 2 << 30:
            raise ValueError('套件超出大小限制')
        names = [p.filename for p in entries if p.filename.endswith('/release.json')]
        root = names[0].rsplit('/', 1)[0] + '/'
        seen = set()
        for p in entries:
            name = p.filename
            parts = PurePosixPath(name).parts
            if ('\\' in name or ':' in name or name.startswith('/') or '..' in parts
                    or not name.startswith(root) or name.casefold() in seen):
                raise ValueError('套件路徑無效或重複')
            seen.add(name.casefold())
            mode = p.external_attr >> 16
            if stat.S_ISLNK(mode):
                raise ValueError('套件不接受符號連結')
            if p.is_dir():
                continue
            rel = name[len(root):]
            if rel in ('release.json', 'SHA256SUMS'):
                continue
            if rel not in files:
                raise ValueError(f'套件含未列出的檔案：{rel}')
            with package.open(p) as stream:
                sha = hashlib.file_digest(stream, 'sha256').hexdigest() if hasattr(hashlib, 'file_digest') else hash_stream(stream)
            if sha != files[rel]:
                raise ValueError(f'套件雜湊不符：{rel}')
        for name in files:
            if (root + name).casefold() not in seen:
                raise ValueError(f'套件缺少檔案：{name}')
        updater = package.read(root + '0c-update.exe')
        terms = package.read(root + 'TERMS.md').decode('utf-8')
    return release, updater, terms


def hash_stream(stream):
    sha = hashlib.sha256()
    for block in iter(lambda: stream.read(1024 * 1024), b''):
        sha.update(block)
    return sha.hexdigest()


def build(args):
    archive = args.archive.resolve()
    if archive.stat().st_size > 1 << 30:
        raise ValueError('壓縮包大小超過 1 GiB')
    release, updater, terms = read_bundle(archive)
    version = release['version']
    if any(int(n) > 65535 for n in version.split('-')[0].split('.')):
        raise ValueError('Windows 檔案版本各段須在 65535 以內')
    sha = digest(archive)
    preview = release['channel'] == 'internal-preview'
    if not preview and not args.sign:
        raise ValueError('公開版安裝精靈必須簽章：請指定 --signtool、--sign-dlib、--sign-metadata')
    name = f'0c-{version}-windows-amd64' + ('-preview' if preview else '') + '-setup.exe'
    args.out.mkdir(parents=True, exist_ok=True)
    output = args.out / name
    checksum = output.with_suffix('.exe.sha256')
    if output.exists() or checksum.exists():
        raise ValueError('輸出已存在，請使用新版本或新目錄')
    with tempfile.TemporaryDirectory(prefix='0c-setup-') as temp:
        stage = Path(temp)
        (stage / '0c-update.exe').write_bytes(updater)
        check_pe(stage / '0c-update.exe')
        if args.sign and signer(stage / '0c-update.exe', args) != PUBLISHER:
            raise ValueError(f'套件內的 0c-update.exe 沒有 {PUBLISHER} 的簽章；請用已簽章的 ZIP')
        shutil.copyfile(archive, stage / 'payload.zip')
        shutil.copyfile(ROOT / 'platforms/windows/installer/setup.nsi', stage / 'setup.nsi')
        shutil.copyfile(ROOT / 'platforms/windows/installer/README.txt', stage / 'README.txt')
        # 安裝前顯示的使用條款與套件內 TERMS.md 同一份；NSIS 需要含 BOM 的 UTF-8 文字檔。
        (stage / 'terms.txt').write_text(terms.replace('\n', '\r\n'), encoding='utf-8-sig')
        # Definitions are derived from validated version/channel/hash, never arbitrary script text.
        (stage / 'bundle.nsh').write_text(
            f'!define VERSION "{version}"\n!define FILE_VERSION "{".".join(map(str, file_version(version)))}"\n'
            f'!define PUBLISHER "{PUBLISHER}"\n!define CHANNEL "{release["channel"]}"\n!define ARCHIVE_SHA256 "{sha}"\n'
            f'!define SETUP_ID "{version}-{sha[:12]}"\n'
            f'!define CHANNEL_LABEL "{"內部預覽版" if preview else "公開測試版"}"\n'
            + ('!define SIGN_UNINSTALLER\n' if args.sign else ''), encoding='utf-8-sig')
        if args.sign:
            # NSIS 在產生 uninstall.exe 時呼叫（!uninstfinalize），傳入的 %1 已含引號。
            (stage / 'sign.cmd').write_text(
                f'@"{args.signtool}" sign /fd SHA256 /tr {TIMESTAMP_URL} /td SHA256 '
                f'/dlib "{args.sign_dlib}" /dmdf "{args.sign_metadata}" %1\r\n', encoding='utf-8')
        # setup.nsi 是 UTF-8；Windows 版 NSIS 預設以系統碼頁讀取，中文會被拒絕。
        subprocess.run([args.makensis, '-V2', '-INPUTCHARSET', 'UTF8', 'setup.nsi'], cwd=stage, check=True)
        if args.sign:
            # 先簽章，之後才計算 .sha256。
            authenticode_sign([stage / '0c-installer.exe'], args)
        fd, temporary = tempfile.mkstemp(prefix='.0c-setup-', dir=args.out)
        os.close(fd)
        try:
            shutil.copyfile(stage / '0c-installer.exe', temporary)
            os.replace(temporary, output)
        finally:
            Path(temporary).unlink(missing_ok=True)
        checksum.write_text(f'{digest(output)}  {name}\n', encoding='utf-8')
    return output


def main():
    parser = argparse.ArgumentParser(description='將已建置的 Windows ZIP 製成雙擊安裝精靈；不安裝、不上傳')
    parser.add_argument('--archive', required=True, type=Path)
    parser.add_argument('--out', type=Path, default=ROOT / 'build/release')
    parser.add_argument('--makensis', default='makensis', help='NSIS 3 編譯器路徑')
    parser.add_argument('--signtool', type=Path, help='Windows SDK x64 signtool.exe（簽章只能在 Windows 執行）')
    parser.add_argument('--sign-dlib', type=Path, help='Artifact Signing 用戶端的 Azure.CodeSigning.Dlib.dll')
    parser.add_argument('--sign-metadata', type=Path, help='Artifact Signing metadata.json')
    try:
        args = parser.parse_args()
        sign = (args.signtool, args.sign_dlib, args.sign_metadata)
        if any(sign) and not all(sign):
            parser.error('簽章需要同時指定 --signtool、--sign-dlib、--sign-metadata')
        args.sign = all(sign)
        print(build(args))
    except (OSError, ValueError, KeyError, zipfile.BadZipFile, subprocess.SubprocessError) as error:
        print(f'安裝程式建置失敗：{error}', file=sys.stderr)
        sys.exit(1)


if __name__ == '__main__':
    main()
