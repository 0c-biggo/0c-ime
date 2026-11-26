# 英文產品詞表

這裡保存中英混打使用的英文拼字來源與排序初值。編譯工具是 `cmd/0c-english`，執行期查詢在 `core/english`，讀檔與來源驗證在 `internal/englishpack`。沒有模型訓練或 Grammarly 相依項目。

## 固定來源

- [ESDB／SCOWL 2026.02.25](https://github.com/en-wl/wordlist/tree/7e99edab8e32f9f9ea2b15f249ca8d4d67237410)，commit `7e99edab8e32f9f9ea2b15f249ca8d4d67237410`。上游建議拼字檢查使用 size 60；本版選用 A／B／Z（美式及英式）、variant ≤ 1，保留小寫 ASCII 字母詞形。英式與美式合法拼字都能辨認，不因方言差異提出修正。
- `esdb.tsv`：82,000 詞，欄位為 `word<TAB>size<TAB>no_suggest`；採各詞最低 size。固定版本的 10／20 匯出為空，55 與 50 相同，實際詞形分布在 35／40／50／60。
- `technical.tsv`：141 個本專案自行編列的常用／產品詞及排序初值（2026-11-25 APP#21 補上月份、星期、sync、PDF、AirPods 等 37 個語料沒有的詞）。與來源重疊者只調整先驗；格式 1 最終 **82,045 詞**。選填的第三欄是常見寫法（Monday、PDF），只有詞頻資料包（`-frequency`）採用，優先於語料的寫法；格式 1 照舊只用小寫。
- `display.tsv`：只補常見寫法的詞（#358 後續，負責人 2026-11-25 確認：Taiwan、Netflix、DRAM、ID、III 等 36 個），欄位為 `word<TAB>寫法`，寫法只能差大小寫。只有詞頻資料包採用，不加詞、不改權重與旗標，混打認不認照舊；詞必須已在字典裡（ESDB、`technical.tsv` 或語料），否則編譯失敗；`technical.tsv` 第三欄已有寫法的詞不能再列。要給某個詞大寫寫法、又不想動它的權重（例如 id、ram 這類短詞），寫在這裡，不要寫進 `technical.tsv`。格式 1 不讀這個檔。
- `source.json`：上游版本、下載 SHA256、七份匯出雜湊、正規化 TSV 及授權檔雜湊。`LICENSE.txt` 原樣保存上游完整 [Copyright](https://github.com/en-wl/wordlist/blob/7e99edab8e32f9f9ea2b15f249ca8d4d67237410/Copyright)，編譯後附在包內 `NOTICE.txt`。

上游 50 個 `no-suggest` 詞仍供拼字存在檢查，補全／修正不推薦它們。此版沒有匯入專名、含撇號／連字號、數字或非 ASCII 詞形；這些輸入仍可原樣保留。含數字的英數詞（win10、DDR5、ps4）另列在 `core/english/alnum.tsv`（#249），隨程式編進核心，不在這個詞表裡。排序使用 `100-size` 及自有產品詞權重，**不是語料使用詞頻**；正式詞頻與混打語境校準另做。

## 詞頻資料（`frequency/`，APP#21）

`frequency/` 是 Funmula Co., Limited 編製的英文詞頻統計（`english-corpus-20261123-r5`），只有統計次數、沒有原文：

- `english-unigram.tsv`、`taiwan-unigram.tsv`：英文詞頻與台灣常用英文，含大小寫寫法分布；`in_esdb=0` 是 ESDB 沒有的詞。
- `english-bigram.tsv`：英文詞對；`mixed-bigram.tsv`：跨中英詞對。
- `manifest.json`：各檔雜湊與大小及排序用的總詞數，SHA-256 固定在 `source.json` 的 `manifest_sha256`；編譯時逐檔核對大小與雜湊。

`go run ./cmd/0c-english -frequency -source data/english -out <新目錄>` 編出**格式 2** 的詞頻資料包（`words.bin`、`bigram.bin`、`NOTICE.txt`、`manifest.json`，82,559 詞）。手機（APP 的 `scripts/prepare-mobile-data.py`）與桌面三平台（0.2.15 起，#358）都出貨這一種。不加 `-frequency` 編出格式 1（不讀 `frequency/`，產物逐位元組不變），留給舊資料包與測試。

格式 2 的內容（`internal/englishpack/ranked.go`、`core/english/ranked.go`）：

- **權重是詞頻**：兩份語料每百萬詞的次數加權平均，換成 `55 + 10·log10`（the 約 101，語料裡最少見的詞約 55）；語料沒有的 ESDB 詞照 size 給 35–45，技術詞表新增而語料沒有的給 50。技術詞表的人工先驗不用。
- **常見寫法**（#358 起只留可靠的，手機與桌面共用）：
  - 留：ESDB 以外、第一個字母以外也有大寫的寫法，而且是語料裡最常見、過半（AI、iPhone、OpenAI、OK、YouTube、XD）；代名詞 I 與 I 開頭的縮寫（I'm、I'll，七成以上）；`technical.tsv` 第三欄與 `display.tsv` 手寫的寫法（Monday、PDF、AirPods、Taiwan、DRAM），優先於語料。
  - 不留：ESDB 的一般英文詞（標題與句首的大寫讓 us、line、total、images 變成 US、LINE、Total、Images）；只有第一個字母大寫的寫法（語料分不出句首、標題與專有名詞：Apple、March 照小寫；要大寫而且詞已在字典裡就寫進 `display.tsv`，要加新詞才寫進 `technical.tsv`）。
  - 查詢照打的大小寫套上它：打全大寫就全大寫；打的字母裡第一個以外有大寫（DeepS、PCm）時，打的部分照打、後面照常見寫法接（DeepSeek、PCman）。
- **混打不認的短詞**：格式 1（ESDB＋`technical.tsv`）沒有、只來自語料的詞標成 `CorpusOnly`（旗標 8）。其中三個字母以內、不含撇號的（da、ji、tv、ae、ai、gdp，287 個）在中英混打的中英判斷不認（`Dictionary.Mixed()`），免得把 Dan␣、A股、T恤 切成 Da斯、Ae五、Tv預；英文模式的補字與修正照用。
- **縮寫**：詞可在字母之間含撇號；`Corrections` 把去掉撇號的拼法（dont、im、didnt）修成縮寫，`Contractions` 另外列出本身也是字的拼法（its → it's、ill → I'll），由外殼決定只建議、不自動修正（`Corrections` 對字典裡的字一律不修正）。
- **台灣常用英文**：併進字典（ESDB 沒有的字母詞加入，含數字的 t1、ps5 等 146 個不收，英數詞另見 `core/english/alnum.tsv`），標成 `Protected`：照打、不自動修正。
- **修正排序**：打字成本（`TypoCost`：重複字母少打或多打 0.5、漏打撇號 0.25、相鄰鍵打錯或顛倒或多打漏打 1、不相鄰鍵打錯 1.5）減權重／40（詞頻差一萬倍抵一個編輯）。舊字典照舊是編輯距離再權重。
- **T9**：`T9(digits, limit)` 照電話鍵（1 是撇號）列同長度的詞，權重高的在前。
- **詞對**：`english.Bigrams`（`bigram.bin`，`englishpack.LoadBigrams`）查英文 → 英文、中文詞 → 英文、英文 → 中文詞的次數（`After`、`Count`），只留兩端都在字典裡的英文詞。目前沒有排序用到它；桌面隨包出貨、先不載入。

## 編譯

```sh
go run ./cmd/0c-english -frequency -source data/english -out build/english
```

輸出包含 `words.bin`、`bigram.bin`、獨立的 `manifest.json` 與 `NOTICE.txt`（格式 1 沒有 `bigram.bin`）。輸出須為新目錄或空目錄；已有產物時選新目錄。任何來源雜湊、等級、排序、權重或授權錯誤都在建立產物前拒絕。相同輸入產生相同位元組，不寫時間戳。

Linux／Windows／macOS 打包器在簽章與資料混淆前加入 `data/english/`。若輸入中文包已有格式 2 的英文子包，先驗證再沿用；沒有、或是 0.2.14 以前資料包的格式 1，就從本來源編譯格式 2。中文 `manifest.json` 不加英文欄位，中文／神經模型配對雜湊保持原值。English `words.bin` 與 `bigram.bin` 沿用既有混淆，執行期解開後核對明文 SHA256。Linux 安裝器要求 `words.bin`、`manifest.json`、`NOTICE.txt` 全有或全無，`bigram.bin` 只能跟著它們。引擎與英文包永遠一起換、一起回復；0.2.14 以前的引擎讀不了格式 2。

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
