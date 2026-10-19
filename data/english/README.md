# 英文產品詞表

這裡保存中英混打使用的英文拼字來源與排序初值。編譯工具是 `cmd/0c-english`，執行期查詢在 `core/english`，讀檔與來源驗證在 `internal/englishpack`。沒有模型訓練或 Grammarly 相依項目。

## 固定來源

- [ESDB／SCOWL 2026.02.25](https://github.com/en-wl/wordlist/tree/7e99edab8e32f9f9ea2b15f249ca8d4d67237410)，commit `7e99edab8e32f9f9ea2b15f249ca8d4d67237410`。上游建議拼字檢查使用 size 60；本版選用 A／B／Z（美式及英式）、variant ≤ 1，保留小寫 ASCII 字母詞形。英式與美式合法拼字都能辨認，不因方言差異提出修正。
- `esdb.tsv`：82,000 詞，欄位為 `word<TAB>size<TAB>no_suggest`；採各詞最低 size。固定版本的 10／20 匯出為空，55 與 50 相同，實際詞形分布在 35／40／50／60。
- `technical.tsv`：104 個本專案自行編列的常用／產品詞及排序初值。與來源重疊者只調整先驗，新增 15 詞；最終 **82,015 詞**。
- `source.json`：上游版本、下載 SHA256、七份匯出雜湊、正規化 TSV 及授權檔雜湊。`LICENSE.txt` 原樣保存上游完整 [Copyright](https://github.com/en-wl/wordlist/blob/7e99edab8e32f9f9ea2b15f249ca8d4d67237410/Copyright)，編譯後附在包內 `NOTICE.txt`。

上游 50 個 `no-suggest` 詞仍供拼字存在檢查，補全／修正不推薦它們。此版沒有匯入專名、含撇號／連字號、數字或非 ASCII 詞形；這些輸入仍可原樣保留。排序使用 `100-size` 及自有產品詞權重，**不是語料使用詞頻**；正式詞頻與混打語境校準另做。

## 編譯

```sh
go run ./cmd/0c-english -source data/english -out build/english
```

輸出包含 `words.bin`、獨立的 `manifest.json` 與 `NOTICE.txt`。輸出須為新目錄或空目錄；已有產物時選新目錄。任何來源雜湊、等級、排序、權重或授權錯誤都在建立產物前拒絕。相同輸入產生相同位元組，不寫時間戳。

Linux／Windows／macOS 打包器在簽章與資料混淆前加入 `data/english/`。若輸入中文包已有英文子包，先驗證再沿用；否則從本來源編譯。中文 `manifest.json` 不加英文欄位，中文／神經模型配對雜湊保持原值。English `words.bin` 沿用既有混淆，執行期解開後核對明文 SHA256。

沒有英文子包的舊資料仍使用 104 詞試用表。部分檔案缺漏、損壞、格式不支援或來源／授權缺漏的子包會拒絕載入；不把破損包當完整字典。降版／回復留下的空目錄可使用舊資料回退。

## 從上游重建來源

這是詞表匯入，無訓練相依。上游產生匯出檔需要 Python 3、SQLite 及 make；它們不放進使用者端套件。下載與建庫在忽略版控的建置目錄執行：

```sh
mkdir -p build/esdb-exports
curl -L https://codeload.github.com/en-wl/wordlist/tar.gz/7e99edab8e32f9f9ea2b15f249ca8d4d67237410 -o build/esdb-2026.02.25.tar.gz
printf '%s  %s\n' 770dd3e42d20b47bb2dd87d150db4ee5472841a7f062876592f67840f5a0b902 build/esdb-2026.02.25.tar.gz | sha256sum -c -
tar -xf build/esdb-2026.02.25.tar.gz -C build
esdb_source=build/wordlist-7e99edab8e32f9f9ea2b15f249ca8d4d67237410
make -C "$esdb_source"
for size in 10 20 35 40 50 55 60; do
  (cd "$esdb_source" && ./scowl word-list "$size" A,B,Z 1 --deaccent --nosuggest --nosuggest-suffix '!') > "build/esdb-exports/$size.txt"
done
go run ./cmd/0c-english -import-lists build/esdb-exports -out build/english-reproduced
```

`-import-lists` 不執行上游程式或連網；逐份驗證匯出 SHA256，確認重建詞數及 TSV 雜湊完全一致後才寫回 `esdb.tsv`。換來源版本須重新審查匯出、授權、詞形差異與產品回歸，不直接接受 `latest`。
