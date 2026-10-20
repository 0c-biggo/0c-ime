#!/usr/bin/env python3
"""重產 testdata/simplified/tw2s.tsv：上游 OpenCC 的 tw2s 轉換結果，給 internal/simplified 的測試對照（#118）。

需要與內附字典同版本的 Python 套件（目前 opencc==1.4.2），只在換字典版本時用，不是建置的一部分：

    python3 -m venv /tmp/occ && /tmp/occ/bin/pip install opencc==1.4.2
    /tmp/occ/bin/python -I scripts/simplified_fixture.py

輸入：手選的難例、兩本詞組字典（TSPhrases、TWVariantsRevPhrases）的每個鍵、已追蹤 Markdown 文件的前 400 句。
不含豆腐字字典（include_tofu_risk_dictionaries=False），同 OpenCC 命令列與 core/zhconv。
"""
import re
import subprocess
from pathlib import Path

import opencc

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "testdata/simplified/tw2s.tsv"
DICTS = ROOT / "internal/simplified/opencc"

HAND = """顯著的計畫
他看著我，著名作家
乾隆皇帝喝乾杯中的酒
我瞭解了，一目瞭然
頭髮發財
公司幹部在樹幹旁
皇后在後面
臺灣的麵包，台灣的颱風
什麼甚麼，老么
一隻手只有五根手指
鐘錶與鍾先生
復興與複雜與覆蓋，反覆答覆
在這裡在這裏
週末周到
於是
藉口與憑藉，慰藉
夥伴
乾淨
瞭望台
吃喝喫
梁先生的橋樑
稜角
衛生紙
今天天氣很好，我們去公園散步吧！
請問 iPhone 17 的價格是多少？大約 NT$32,900 元。
😀 表情符號與 (｡◕‿◕｡) 顏文字
「引號」『雙引號』……——
ㄅㄆㄇ 注音符號
沈船、下沈、消沈、沈穩
土著、原著、編著、卓著
一方面聯繫客戶，一方面準備資料
這個軟體的介面設計得很直覺
記憶體不足，請關閉其他程式
網路連線失敗
他說：「我會儘快回覆。」
醫院的護理師很專業
後來我們發現了問題的癥結
這隻貓叫做咪咪
著作權法第十條
乾坤大挪移
"""


def keys(name):
    for line in (DICTS / name).read_text(encoding="utf-8").splitlines():
        if line and not line.startswith("#"):
            yield line.split("\t", 1)[0]


def sentences(paths, limit):
    seen = set()
    han = re.compile(r"[一-鿿]")
    for p in paths:
        for line in p.read_text(encoding="utf-8", errors="replace").splitlines():
            for s in re.split(r"(?<=[。！？；])", line):
                s = s.strip().replace("\t", " ")
                if 6 <= len(s) <= 120 and len(han.findall(s)) >= 4 and s not in seen:
                    seen.add(s)
                    yield s
                    if len(seen) >= limit:
                        return


def main():
    conv = opencc.OpenCC("tw2s", include_tofu_risk_dictionaries=False)
    lines = [l for l in HAND.splitlines() if l]
    lines += sorted(set(keys("TSPhrases.txt")) | set(keys("TWVariantsRevPhrases.txt")))
    # 只讀 git 追蹤的文件，避免把本機未公開的筆記寫進 fixture。
    docs = subprocess.check_output(["git", "-C", str(ROOT), "ls-files", "-z", "*.md"], text=True).split("\0")
    lines += list(sentences([ROOT / d for d in sorted(docs) if d and not d.startswith("testdata/")], 400))
    rows, seen = [], set()
    for s in lines:
        if s not in seen:
            seen.add(s)
            rows.append(f"{s}\t{conv.convert(s)}")
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(f"# OpenCC ver.{opencc.__version__} tw2s（不含豆腐字字典）的轉換結果：輸入<Tab>預期。"
                   "由 scripts/simplified_fixture.py 產生，不要手改。\n" + "\n".join(rows) + "\n", encoding="utf-8")
    print(len(rows), "rows →", OUT)


if __name__ == "__main__":
    main()
