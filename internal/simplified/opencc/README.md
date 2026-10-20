# OpenCC tw2s 字典

「輸出簡體字」（#118）送出時用的台灣正體轉簡體字典，原樣取自 [OpenCC](https://github.com/BYVoid/OpenCC) 的正式版 **ver.1.4.2**（commit `025f371dc76b598d77384fbdab90c937471844d8`，2026-08-22），Apache-2.0，授權全文在 `LICENSE`。來源登錄在 [data/sources/ledger.tsv](../../../data/sources/ledger.tsv) 的 `opencc`；第三方元件聲明由 `scripts/collect_notices.py` 附上。

| 檔案 | 上游路徑 | sha256 |
|---|---|---|
| `CJK_Compatibility_Ideographs.txt` | `data/dictionary/` | `663cc10b7de7c7cee3ff9df1ee963977d6c26c00b23f8fe670fab481283ed4f9` |
| `TWVariants.txt` | `data/dictionary/` | `e187278e119c427ca561180ac5da5b20e9f8681190458f35c327ce499e95a6a5` |
| `TWVariantsRevPhrases.txt` | `data/dictionary/` | `5ebfb4bdc938c2b14e01ace378988d5d3dc12462b3496ef1d424951ccd371256` |
| `TSPhrases.txt` | `data/dictionary/` | `362fa1b9a7d6edd04b462a32e12c9fef3adae822ab1dee9c83561cc37c06cb1f` |
| `TSCharacters.txt` | `data/dictionary/` | `737c21c66f55a419dd6956cb3089476cdefc5a36877452631617696df1e5d925` |
| `LICENSE` | `LICENSE` | `b534e465949558eec2597b04f5092b5e161236a68dfbfd04d547592ac3964308` |

- 這些檔案用 `go:embed` 編進 `0c-server`，不放進資料包：跟著外殼更新帶到四個平台，安裝與更新流程不用改。只有使用者開啟設定時才解析。
- `TWVariantsRev` 在上游是建置時把 `TWVariants.txt` 鍵值對調產生的（`data/scripts/reverse.py`，含 `@reverse-prefer`），這裡在載入時做同樣的事（`core/zhconv.ParseReversed`），不另存一份。
- 上游 tw2s 另有 `TSCharactersExt`（`TSCharacters.txt` 裡標 `@tofu-risk` 的字，簡體在多數字型沒有字形），同 OpenCC 命令列的預設不用。
- 不用 longbridge/opencc、liuzl/gocc 等 Go 移植：它們內附的字典較舊（例如把「顯著」轉成「显着」）。比對規則照上游（`core/zhconv` 的說明），`internal/simplified` 的測試以上游 Python 套件 `opencc==1.4.2` 的轉換結果對照（`testdata/simplified/tw2s.tsv`）。

## 換版本

1. 從新的正式版 tag 取同名檔案覆蓋，更新上表、`simplified_test.go` 的雜湊、`ledger.tsv` 的版本與 `scripts/collect_notices.py` 的標題。
2. 用同版本的 Python 套件跑 `scripts/simplified_fixture.py` 重產 `testdata/simplified/tw2s.tsv`（用法見檔頭），再跑 `go test ./internal/simplified ./core/zhconv ./core/session`。
3. 上游的 tw2s 設定（`data/config/tw2s.json`）若改了步驟或比對規則，`core/zhconv.TW2S` 要跟著改。
