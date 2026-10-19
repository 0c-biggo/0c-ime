"""Build the website manifest feeds/<feed>-site.json (schema 1, 0c/ime#28).

The website reads this file at runtime (same-origin /release.json proxy), so uploading it is the moment
the download buttons, version and changelog change. The archive assets (Linux: the installer) must be
exactly the assets of the signed feed; installers (Windows setup.exe, macOS .pkg) are --extra uploads.
The changelog comes only from the "### 公開摘要" subsections of CHANGELOG.md.

Older changelog entries may carry their own "platforms" (0c/ime#81) so the download page can offer past
versions. They are carried over from the manifest that is online before publishing (the previous current
version comes from its top-level platforms), optionally backfilled from S3 sidecars, re-validated, and
dropped for withdrawn versions. changelog[0] never carries platforms; the top level is authoritative.
"""
import re

PLATFORMS = {("windows", "amd64"): "windows-amd64", ("linux", "amd64"): "linux-amd64", ("darwin", "arm64"): "macos-arm64"}  # 官網顯示順序
RELEASED_STAGES = ("public-beta", "stable")
SUMMARY = "### 公開摘要"
HEADING = re.compile(r"^## (\S+)（(\d{4}-\d{2}-\d{2})，([a-z-]+)）$")
FILE = re.compile(r"^0c-(?P<version>.+)-(?P<platform>linux-amd64|windows-amd64|macos-arm64)(?P<setup>-setup)?\.(?P<ext>tar\.gz|zip|exe|pkg)$")
LINK = re.compile(r"\[([^\]]+)\]\(([^)\s]+)\)")
SHA256 = re.compile(r"^[0-9a-f]{64}$")
ROLES = ("installer", "archive")


def check_item(version, item):
    """官網原樣顯示項目：只允許純文字與使用 https 完整網址的行內連結。"""
    for _, url in LINK.findall(item):
        if not url.startswith("https://"):
            raise ValueError(f"{version} 公開摘要的連結須為 https 完整網址：{url}")
    plain = LINK.sub(r"\1", item)
    if re.search(r"[`*_<>]|\]\(|^#|!\[", plain):
        raise ValueError(f"{version} 公開摘要只能有純文字與行內連結：{item}")


def parse_changelog(text):
    """CHANGELOG.md 中已上架版本的公開摘要，依檔案順序（新到舊）。

    版本標題固定「## <version>（YYYY-MM-DD，<stage>）」；「## 未發布」略過。已上架（public-beta、stable）
    的版本都必須有至少一項「### 公開摘要」，否則丟出 ValueError，發布中止。"""
    entries, current, section = [], None, None
    for number, line in enumerate(text.splitlines(), 1):
        if line.startswith("## "):
            current, section = None, None
            if line.strip() == "## 未發布":
                continue
            m = HEADING.match(line.strip())
            if not m:
                raise ValueError(f"CHANGELOG.md 第 {number} 行的版本標題格式不符：{line}")
            version, date, stage = m.groups()
            if stage in RELEASED_STAGES:
                current = {"version": version, "date": date, "stage": stage, "items": []}
                entries.append(current)
        elif line.startswith("### "):
            section = line.strip()
        elif current is not None and section == SUMMARY and line.strip():
            if line.startswith("- "):
                current["items"].append(line[2:].strip())
            elif line.startswith("  ") and current["items"]:
                current["items"][-1] += line.strip()  # 中文換行接續不加空白
            else:
                raise ValueError(f"CHANGELOG.md 第 {number} 行：公開摘要只能是「- 」開頭的項目")
    seen = set()
    for e in entries:
        if e["version"] in seen:
            raise ValueError(f"CHANGELOG.md 重複的版本：{e['version']}")
        seen.add(e["version"])
        if not e["items"]:
            raise ValueError(f"CHANGELOG.md 的 {e['version']} 缺少「{SUMMARY}」或沒有項目")
        for item in e["items"]:
            check_item(e["version"], item)
    return entries


def classify(name, version):
    """回傳 (平台, 'installer'|'archive')；不是官網下載檔時為 None。"""
    m = FILE.match(name)
    if not m or m["version"] != version:
        return None
    platform, ext = m["platform"], m["ext"]
    if platform == "linux-amd64" and ext == "tar.gz" and not m["setup"]:
        return platform, "installer"
    if platform == "windows-amd64" and ext == "exe" and m["setup"]:
        return platform, "installer"
    if platform == "windows-amd64" and ext == "zip" and not m["setup"]:
        return platform, "archive"
    if platform == "macos-arm64" and ext == "pkg" and not m["setup"]:
        return platform, "installer"
    if platform == "macos-arm64" and ext == "tar.gz" and not m["setup"]:
        return platform, "archive"
    return None


def check_platforms(version, platforms):
    """舊版的下載資料（#81）：平台與 installer／archive 的意義同最上層；網址須是該版本 releases/<version>/ 下
    對應角色的檔名，size 為正整數，sha256 為 64 位小寫十六進位。舊版可以只有 archive，每個平台至少一個檔案。
    回傳依官網順序整理過的副本；不合規則時丟出 ValueError。"""
    if not isinstance(platforms, dict) or not platforms:
        raise ValueError(f"{version} 的 platforms 須為非空物件")
    unknown = set(platforms) - set(PLATFORMS.values())
    if unknown:
        raise ValueError(f"{version} 的 platforms 有不認得的平台：{'、'.join(sorted(unknown))}")
    out = {}
    for platform in PLATFORMS.values():
        if platform not in platforms:
            continue
        roles = platforms[platform]
        if not isinstance(roles, dict) or not roles or set(roles) - set(ROLES):
            raise ValueError(f"{version} 的 {platform} 須有 installer 或 archive，且沒有其他欄位")
        entry = {}
        for role in ROLES:
            if role not in roles:
                continue
            f = roles[role]
            url, size, digest = (f.get("url"), f.get("size"), f.get("sha256")) if isinstance(f, dict) else (None, None, None)
            name = url.rsplit("/", 1)[-1] if isinstance(url, str) else ""
            if not (isinstance(url, str) and url.startswith("https://") and f"/releases/{version}/{name}" in url
                    and classify(name, version) == (platform, role)):
                raise ValueError(f"{version} 的 {platform} {role} 網址須在 releases/{version}/ 之下且檔名相符：{url}")
            if type(size) is not int or size <= 0:
                raise ValueError(f"{version} 的 {platform} {role} 大小須為正整數：{size}")
            if not isinstance(digest, str) or not SHA256.match(digest):
                raise ValueError(f"{version} 的 {platform} {role} sha256 須為 64 位小寫十六進位")
            entry[role] = {"url": url, "size": size, "sha256": digest}
        out[platform] = entry
    return out


def known_platforms(history, backfill=None):
    """{版本: platforms}：先取線上清單各版已有的 platforms，上一版取線上清單最上層，最後才用 backfill 補缺。"""
    known = {}
    if history:
        for entry in history.get("changelog") or []:
            if isinstance(entry, dict) and entry.get("platforms"):
                known.setdefault(entry.get("version"), entry["platforms"])
        if history.get("version") and history.get("platforms") and not history.get("withdrawn"):
            known.setdefault(history["version"], history["platforms"])
    for version, platforms in (backfill or {}).items():
        known.setdefault(version, platforms)
    return known


def build(feed, uploads, changelog, published, withdrawn_reason=None, history=None, backfill=None):
    """feed：已簽章索引（dict）；uploads：[(檔名, url, sha256, size)]，含索引資產與 --extra；
    changelog：parse_changelog 的結果；published：UTC 時間字串；history：上傳前線上的官網清單（沒有為 None）；
    backfill：{版本: platforms}，由呼叫端提供線上清單尚未列出的舊版本。"""
    version = feed["version"]
    if not changelog or changelog[0]["version"] != version:
        raise ValueError(f"CHANGELOG.md 最新的已上架版本須為 {version}（目前為 {changelog[0]['version'] if changelog else '無'}）")
    if feed["channel"] != changelog[0]["stage"]:
        raise ValueError(f"索引通道 {feed['channel']} 與 CHANGELOG 的 {changelog[0]['stage']} 不符")
    platforms = {}
    for name, url, digest, size in uploads:
        kind = classify(name, version)
        if kind is None:
            continue
        platform, role = kind
        if not url.startswith("https://") or f"/releases/{version}/{name}" not in url:
            raise ValueError(f"下載網址須在 releases/{version}/ 之下：{url}")
        if role in platforms.setdefault(platform, {}):
            raise ValueError(f"{platform} 的 {role} 重複")
        platforms[platform][role] = {"url": url, "size": int(size), "sha256": digest}
    signed = {}
    for asset in feed["assets"]:
        platform = PLATFORMS.get((asset["os"], asset["arch"]))
        if platform is None:
            raise ValueError(f"官網清單不支援的平台：{asset['os']}/{asset['arch']}")
        signed[platform] = {"url": asset["url"], "size": int(asset["size"]), "sha256": asset["sha256"]}
    for platform, entry in signed.items():
        role = "installer" if platform == "linux-amd64" else "archive"
        if platforms.get(platform, {}).get(role) != entry:
            raise ValueError(f"{platform} 的 {role} 與已簽章索引的資產不一致")
    for platform, roles in platforms.items():
        if platform not in signed:
            raise ValueError(f"{platform} 不在已簽章索引中，不能只上架安裝檔")
        if "installer" not in roles:
            raise ValueError(f"{platform} 缺少官網主要下載（Windows setup.exe／macOS .pkg 請以 --extra 提供）")
    withdrawn = None
    if version in (feed.get("withdrawn") or []):
        if not withdrawn_reason:
            raise ValueError(f"{version} 已撤回：請以 --withdrawn-reason 寫給使用者看的一句話")
        withdrawn = {"reason": withdrawn_reason}
    elif withdrawn_reason:
        raise ValueError(f"已簽章索引沒有撤回 {version}，不能填 --withdrawn-reason")
    # 舊版附下載資料（#81）：撤回的版本（已簽章索引的 withdrawn）一律不附，即使撤回的不是目前版本。
    pulled = set(feed.get("withdrawn") or [])
    known = known_platforms(history, backfill)
    entries = []
    for i, entry in enumerate(changelog):
        entry = {k: v for k, v in entry.items() if k != "platforms"}
        if i > 0 and entry["version"] not in pulled and entry["version"] in known:
            entry["platforms"] = check_platforms(entry["version"], known[entry["version"]])
        entries.append(entry)
    return {
        "schema": 1, "product": "0c", "channel": feed["channel"], "version": version, "published": published,
        "withdrawn": withdrawn, "platforms": {k: platforms[k] for k in PLATFORMS.values() if k in platforms},
        "changelog": entries,
    }
