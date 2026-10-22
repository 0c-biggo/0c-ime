# 授權

Copyright (C) 2026 Funmula Co., Limited

本專案的自有程式碼、建置腳本、測試程式與文件，著作權屬 Funmula Co., Limited，除另有聲明外，依 **GNU Affero General Public License version 3 only（AGPL-3.0-only）** 授權。完整條款見 [LICENSE](../LICENSE)。

使用與授權說明見 [TERMS.md](TERMS.md)，不影響 LICENSE 已授予的權利；隱私與資料的說明見官網 [隱私與資料](https://0c.biggo.com/privacy/)。依 Debian 自由軟體指導方針（DFSG）的逐項檢查見 [DFSG-free.md](DFSG-free.md)；逐檔的著作權人與授權以 Debian 的 DEP-5 格式列在 [copyright](copyright)。

## 圖示與圖片

本專案自有的圖示、標誌與圖片，著作權屬 Funmula Co., Limited，依 **Creative Commons Attribution-ShareAlike 4.0 International（CC BY-SA 4.0）** 授權，署名為 Funmula Co., Limited，完整條款見 [CC-BY-SA-4.0.txt](CC-BY-SA-4.0.txt)。範圍包括：

- `.github/assets/`
- `internal/settingsapp/icon.ico`、`internal/settingsapp/icon.png`
- `platforms/linux-fcitx5/icons/`
- `platforms/macos/Icon/`
- `platforms/settings/icons/`、`platforms/settings/ui/icon.png`

## 商業授權

AGPL-3.0 允許研究、個人及商業使用。若需要以不符合 AGPL 條件的方式進行閉源整合或散布，請聯絡專案維護者洽談另行書面商業授權。未取得書面授權前，仍適用 AGPL-3.0。

另行授權僅涵蓋授權方有權授權且書面約定納入的內容；外部貢獻不自動取得閉源重授權權利。

## 資料、模型與第三方元件

第三方程式與相依元件、詞庫、語料、模型、字型及圖片依各自授權或權利聲明處理，不因本專案的 LICENSE 而變更。0c 自有的詞條與詞頻統計（`curated`、`0c_collect` 系列）著作權屬 Funmula Co., Limited，依 CC BY-SA 4.0 授權。

- 詞庫與詞頻：原始碼中的 `data/sources/ledger.tsv` 與 `data/sources/licenses/`；編譯後資料包中的 `data/NOTICE.txt`。
- 英文詞表：原始碼中的 `data/english/LICENSE.txt`；編譯後資料包中的 `data/english/NOTICE.txt`。
- 神經模型：各模型套件隨附的 `LICENSE`；目前選字與補標點的固定 D512 模型採 MIT。
- Unicode：[授權聲明](UNICODE-NOTICE.txt)。
- 其他隨安裝包出貨的元件：`THIRD_PARTY_NOTICES.txt`。

授權全文保留在各元件或資料所在目錄，供編譯、打包時一併附上。本文件不額外授予資料或素材的散布權，也不撤回先前版本已有效授予的權利。
