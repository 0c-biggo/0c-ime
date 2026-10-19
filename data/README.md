# 詞庫資料

`0c-compile` 將此目錄編成執行期資料包。字詞讀音採下表的 TSV 來源格式；欄位定義見各檔案開頭，解析與驗證實作在 [internal/compile](../internal/compile)。

| 路徑 | 內容 | 誰維護 |
|---|---|---|
| `sources/name-references.tsv` | 自有名稱詞條參考的清單出處與版本；以詞條備註 `name-source:<ID>` 追蹤 | 人工 |
| `sources/ledger.tsv` | 來源帳本：分列 0c 自有資料與第三方資料，記錄網址、版本或雜湊、授權、是否可散布、姓名標示 | 人工 |
| `sources/licenses/<來源 ID>.txt` | 附進資料包 `NOTICE.txt` 的授權全文或聲明 | 人工 |
| `lexicon/words.tsv`、`chars.tsv` | 詞庫本體：每個詞面×讀音（字×讀音×variant_of）一行，人工詞條與外部來源都在這裡；`origin` 欄記錄來源（逗號分隔），行尾可加選填的備註欄寫審查理由。任何問題都是編譯錯誤 | 人工增修；外部來源經 `0c-import -apply` 寫入 |
| `lexicon/rejected.tsv` | 審過不收的字詞讀音（例：工口 ㄟ ㄌㄨㄛˊ）：不在詞庫裡，`0c-import -apply` 也不會加回來；缺依據、重複或仍在詞庫裡都是編譯錯誤 | 人工 |
| `lexicon/corrections.tsv` | 自動修正（錯誤讀音照字面不得成詞） | 人工 |
| `lexicon/reachability.tsv` | 可達性回歸：使用者回報的「打不出來」，永不移除 | 人工 |
| `lm/unigram.tsv` | 詞頻：每個詞一行，各來源的次數各一欄（第一行是 `# 詞` 加來源 ID）；0c_collect 欄是新聞＋PTT 語料以詞庫斷詞的次數，詞庫大改後要重算 | `0c-import`、`0c-corpus` 整欄更新，不要手改 |
| `pipeline/fetch.sh` | 下載外部來源到 `raw/` 並印出雜湊 | 人工 |
| `raw/` | 外部來源的原始檔 | 不進版本控制 |
| `english/` | 固定版本 ESDB／SCOWL 拼字詞表、自有產品詞、來源雜湊與完整授權；由 `0c-english` 編成獨立英文子包 | 來源與重建見 [英文產品詞表](english/README.md) |

`curated` 是 0c 自有詞條；`0c_collect`、`0c_collect-ns`、`0c_collect-ss` 是 0c 自行整理、分類與計算的詞頻／語言模型，名稱是 0c 的來源 ID，並非外部詞庫網站或供應者。三個 ID 分別對應 unigram、多字詞 bigram 與單字詞 bigram；編譯與語料工具使用相同的 ID。

## 授權與公開資料包

`curated` 自有詞條與三筆 `0c_collect` 統計均採 CC-BY-SA-4.0，可散布；授權全文與署名見 `sources/licenses/`。`0c_collect` 授權範圍為 0c 整理的詞頻／詞對統計，不含原始文章全文；第三方資料各依帳本授權。

公開包必須使用 `0c-compile -public`。工具拒絕不可散布、缺少來源名稱／網址／版本／授權／署名，或缺少 `sources/licenses/<ID>.txt` 的實際使用來源。開發編譯通過不代表公開檢查已通過。

股票名稱以 `curated` 維護，清單出處記在 `name-references.tsv` 與 `name-source:<ID>` 詞條備註，不另列為詞庫署名來源。

## 更新詞庫

`go run ./cmd/0c-audit` 可產生主讀音待審清單。

```sh
data/pipeline/fetch.sh            # 重新下載；把印出的雜湊更新到 ledger.tsv
go run ./cmd/0c-import            # 外部來源寫到 build/import，與詞庫比對；差異摘要在 build/import/diff.txt
go run ./cmd/0c-import -apply all # 審過差異後寫回詞庫（也可只列部分來源 ID）；以 git diff 審查
go run ./cmd/0c-compile           # 任何錯誤都不出版
go run ./cmd/0c-eval -summary     # 與前一版比較，不得有未解釋的退步
go run ./cmd/0c-reading-eval      # 照教育部辭典的審訂音打得出與首選的比例（讀 data/raw 的簡編本）
```

匯入來源有小麥注音、全字庫、CC-CEDICT（另補依小麥注音學到的台灣讀音）、內政部路名與村里、證交所股票簡稱、教育部學校名錄、健保署醫院名單、公文通訊錄的機關名稱，名稱類只以辭典來源與人工詞條斷詞自動標音。差異報告分「新增、加上來源、來源已沒有、來源等級較高、列為不收、基本平面外」，明細在 `build/import/diff/<來源>.tsv`。寫回時新增的詞條接在檔尾、依詞面排序，同一字的讀音維持原本的先後（讀音順序會影響編譯時的讀音展開）。含基本平面外字元的新詞條不會自動加入，審過再手動寫進詞庫。

使用者回報「某個詞打不出來」時，先把它加進 `lexicon/reachability.tsv`，再補資料到它通過為止。

外部來源的某個讀音有問題（例如把不該成詞的音收成某個詞的開頭），從 `words.tsv`／`chars.tsv` 刪掉那一行，並在 `lexicon/rejected.tsv` 寫下詞面、讀音與審查依據，下次匯入就不會加回來；不要為一筆讀音排除整個來源。

## 從 repo 內資料建立公開資料包

```sh
go run ./cmd/0c-compile -public -bigram '' -out build/public-data
```

此命令使用現有詞庫與 unigram，產生 `manifest.json`、`lexicon.bin`、`lm.bin`、`NOTICE.txt` 與 `report.txt`。NOTICE 逐筆附上實際使用來源的授權聲明。缺少必要聲明、版本或可散布標記時停止產出，不以刪除來源標記取代重新編譯。

完整 release tarball 直接附上可使用的統計字典，位於 `assets/data/`；檔案大小與 SHA-256 固定於 [dictionary.lock.json](dictionary.lock.json)，與內附模型配對。建置輸入法不需執行 `0c-compile` 或重新產生統計資料。

大型 `lm/bigram/` 不進 Git，也不附入 tarball。`sources/bigram.lock.json` 與 `scripts/build_release_data.py` 留供維護者更新字典時校驗外部統計輸入；這不是一般使用者或發行版建置的必要步驟。無 bigram 的快速開發產物不符合完整模型配對清單。

`curated` 的版本記錄此批詞庫快照與完整 SHA-256。雜湊按 `chars.tsv`、`words.tsv`、`corrections.tsv` 的順序，取來源欄含 `curated` 的資料列，每列以「檔名、Tab、原始資料列、LF」的 UTF-8 位元組串接計算。來源欄索引依序為 5、4、2（由 0 起算）；忽略空行與 `#` 註解。修改這些人工資料後須更新版本與雜湊，不改寫第三方來源版本。
