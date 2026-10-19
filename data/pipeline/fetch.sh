#!/bin/sh
# 下載外部來源到 data/raw/（不進版本控制），並印出 SHA-256 供記入來源帳本。
# 只抓授權允許併入發行資料的來源；研究比對用的來源不在這裡。
set -eu
mkdir -p "$(dirname "$0")/../raw" # 全新 clone 沒有這個目錄（不進版本控制）
cd "$(dirname "$0")/../raw"

# 小麥注音資料（MIT；詞表最初衍生自 libtabe，BSD）
if [ ! -d McBopomofo ]; then
  git clone --depth 1 --filter=blob:none --sparse https://github.com/openvanilla/McBopomofo.git
  git -C McBopomofo sparse-checkout set Source/Data
fi
git -C McBopomofo rev-parse HEAD > mcbopomofo.commit

# 全字庫（政府資料開放授權條款第 1 版）
curl -fsSLo cns-properties.zip https://www.cns11643.gov.tw/opendata/Properties.zip
curl -fsSLo cns-mapping.zip https://www.cns11643.gov.tw/opendata/MapingTables.zip
rm -rf cns && mkdir cns && (cd cns && unzip -q ../cns-properties.zip && unzip -q ../cns-mapping.zip)

# CC-CEDICT（CC BY-SA 4.0）
curl -fsSLo cedict.txt.gz https://www.mdbg.net/chinese/export/cedict/cedict_1_0_ts_utf-8_mdbg.txt.gz

echo "McBopomofo commit: $(cat mcbopomofo.commit)"
sha256sum cns-properties.zip cns-mapping.zip cedict.txt.gz

# 評測用句子：Common Voice zh-TW 句庫（README 稱大部分為 CC0；只用於評測，不進詞庫）
mkdir -p cv
for f in sentence-collector setences chatlogs lms; do
  curl -fsSLo "cv/$f.txt" "https://raw.githubusercontent.com/common-voice/common-voice/main/server/data/zh-TW/$f.txt"
done
wc -l cv/*.txt | tail -1

# 新酷音詞庫資料（chewing_v4 的 unigram／bigram 語言模型：CC BY 4.0）
if [ ! -d libchewing-data ]; then
  git clone --filter=blob:none https://codeberg.org/chewing/libchewing-data.git
fi
git -C libchewing-data fetch -q origin e5d726d9c09afd562309b05d9adad29940de5ebb
git -C libchewing-data checkout -q e5d726d9c09afd562309b05d9adad29940de5ebb

# 內政部全國路名資料（115 年版）
mkdir -p moi
curl -fsSLo moi/roads-115.csv "https://opdadm.moi.gov.tw/api/v1/no-auth/resource/api/dataset/E2EDC47D-2D3F-4EB1-878A-4DEB6160FD4C/resource/EB1B8C49-5C2A-42E0-8CC3-6B69B37DA66F/download"

# 臺灣證券交易所 ISIN 公開清單：上市（strMode=2）、上櫃（4）、興櫃（5）的股票簡稱；原檔 Big5-HKSCS，轉成 UTF-8
mkdir -p twse
for m in 2 4 5; do
  curl -fsSL "https://isin.twse.com.tw/isin/C_public.jsp?strMode=$m" | iconv -f BIG5-HKSCS -t UTF-8 > "twse/isin-$m.html"
done
sha256sum twse/isin-*.html

# 名稱清單（政府資料開放授權條款第 1 版；0c-import 的 nameLists）。學校名錄的路徑含學年度，換學年要跟著改。
mkdir -p moe nhi agencies
curl -fsSLo moe/u1.csv https://stats.moe.gov.tw/files/opendata/u1_new.csv     # 大專校院
curl -fsSLo moe/high.csv https://stats.moe.gov.tw/files/school/115/high.csv    # 一般高級中等學校
curl -fsSLo moe/j1.csv https://stats.moe.gov.tw/files/opendata/j1_new.csv      # 國民中學
curl -fsSLo moe/e1.csv https://stats.moe.gov.tw/files/school/115/e1_new.csv    # 國民小學
curl -fsSLo nhi/center.csv "https://info.nhi.gov.tw/api/iode0000s01/Dataset?rId=A21030000I-D21001-003"   # 醫學中心
curl -fsSLo nhi/regional.csv "https://info.nhi.gov.tw/api/iode0000s01/Dataset?rId=A21030000I-D21002-005" # 區域醫院
curl -fsSLo nhi/district.csv "https://info.nhi.gov.tw/api/iode0000s01/Dataset?rId=A21030000I-D21003-003" # 地區醫院
curl -fsSLo moi/villages.csv "https://opdadm.moi.gov.tw/api/v1/no-auth/resource/api/dataset/2C7688CB-B505-4D00-B11C-66C4D31B024F/resource/2DE5067B-2A06-4DC4-A9B5-F8C4506B86B8/download"
curl -fsSLo agencies/org.csv https://edit.good.nat.gov.tw/adbook_good/api/org_csv.aspx  # 筆硯公文製作通訊錄
sha256sum moe/*.csv nhi/*.csv moi/villages.csv agencies/org.csv

# 中文維基百科 dump 與 OpenCC 詞典（0c-corpus 用）
mkdir -p zhwiki opencc
curl -fsSL -C - -o zhwiki/zhwiki-latest-pages-articles.xml.bz2 https://dumps.wikimedia.org/zhwiki/latest/zhwiki-latest-pages-articles.xml.bz2
for f in STCharacters STPhrases TWVariants TWPhrases; do
  curl -fsSLo "opencc/$f.txt" "https://raw.githubusercontent.com/BYVoid/OpenCC/master/data/dictionary/$f.txt"
done
