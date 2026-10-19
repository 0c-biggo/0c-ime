"""macOS bundle 版本號：CFBundleVersion 必須隨版本遞增。

每次更新都把舊 0c.app 整包移入備份，備份與新版是同一個 bundle ID。各版都寫 1 時，LaunchServices
分不出哪一份較新，可能開到備份裡的舊外殼（#77）。CFBundleVersion 只接受至多三段整數，所以把預發行
階段與編號併進第三段：patch×10⁷ ＋ 階段×10⁶ ＋ 編號×1000 ＋ 子編號；正式版的階段為 9，排在所有預發行之後。
dev.58 以前一律寫 1，第一段若是 0 會被當成比 1 舊，所以第一段用 100＋major。
"""
import re

# 與 semver 的字典序一致：alpha < beta < dev < rc < 正式版。
STAGES = {"alpha": 1, "beta": 2, "dev": 3, "rc": 4}


def short_version(version):
    match = re.match(r"^(\d+)\.(\d+)\.(\d+)", version)
    if not match:
        raise ValueError(f"版本請用 0.1.0-dev.1 形式：{version}")
    return ".".join(match.groups())


def bundle_version(version):
    match = re.fullmatch(r"(\d+)\.(\d+)\.(\d+)(?:-([a-z]+)\.(\d+)(?:\.(\d+))?)?", version)
    if not match:
        raise ValueError(f"macOS 版本號只接受 1.2.3、1.2.3-dev.4 或 1.2.3-dev.4.5 形式：{version}")
    major, minor, patch, stage, number, sub = match.groups()
    if stage is None:
        rank = 9 * 10**6
    elif stage not in STAGES:
        raise ValueError(f"不認得的預發行階段 {stage}（可用 {'、'.join(STAGES)}）")
    else:
        number, sub = int(number), int(sub or 0)
        if number >= 1000 or sub >= 1000:
            raise ValueError(f"預發行編號超過 999：{version}")
        rank = STAGES[stage] * 10**6 + number * 1000 + sub
    return f"{100 + int(major)}.{int(minor)}.{int(patch) * 10**7 + rank}"
