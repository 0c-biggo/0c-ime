# DFSG 檢查

本 repo（[github.com/0c-biggo/0c-ime](https://github.com/0c-biggo/0c-ime)）與每個版本 Release 的原始碼 tarball `0c-ime-<版本>.tar.xz` 都必須符合 [Debian 自由軟體指導方針](https://www.debian.org/social_contract#guidelines)（DFSG），可以整包放進 Debian main。本文件記錄逐項檢查的結果；新增程式、資料、素材或相依套件時，照本文件的標準檢查並更新。

檢查日期：2026-10-20；對象：`main`（module path 改為 `github.com/0c-biggo/0c-ime` 之後）與 Release v0.2.2 tarball。

## 判斷標準

- **授權**：允許任何人為任何用途使用、修改、再散布，不限制使用領域（DFSG 1、3、5、6）。只要求署名或 copyleft 的授權（MIT、BSD、Apache-2.0、AGPL-3.0、CC BY 4.0、CC BY-SA 4.0 等）可以接受。
- **原始碼**（DFSG 2）：散布的每個產物都要附上修改時偏好使用的形式，並能從散布的內容重建。編譯後的字典要能從公開的輸入重建；神經模型依 [Debian 機器學習政策](https://salsa.debian.org/deeplearning-team/ml-policy)，訓練程式與訓練資料都須自由且公開，才算有原始碼。
- **相依套件**：建置與執行時需要的元件也要符合 DFSG；Debian main 已收錄的版本視為通過。

## 結果摘要

| 項目 | 授權 | 結果 |
|---|---|---|
| 自有程式碼、建置腳本、測試程式、文件 | AGPL-3.0-only，Funmula Co., Limited | 符合 |
| 圖示與圖片素材 | CC BY-SA 4.0，Funmula Co., Limited | 符合 |
| Go 相依模組 | MIT、BSD-2-Clause、BSD-3-Clause | 符合 |
| Rust 相依 crate（設定程式、Windows 外殼） | MIT、Apache-2.0、MPL-2.0、Unicode-3.0 等 | 符合 |
| 內附第三方程式碼：nlohmann/json | MIT | 符合 |
| 推論執行環境：NumPy、ONNX Runtime | BSD-3-Clause、MIT | 符合（Debian main 已收錄） |
| 詞庫來源（`data/`） | CC BY-SA 4.0、CC BY 4.0、MIT、政府資料開放授權條款第 1 版 | 符合 |
| 簡體轉換字典（`internal/simplified/opencc`） | Apache-2.0 | 符合 |
| 英文詞表（`data/english`） | SCOWL 授權 | 符合 |
| 測試資料（`testdata/`） | AGPL-3.0-only | 符合 |
| Release：英文子包 `assets/data/english/` | SCOWL 授權 | 符合，可由 `data/english` 重建 |
| Release：統計字典 `assets/data/lexicon.bin`、`lm.bin` | CC BY-SA 4.0 等 | 符合，可由 Git 內資料加 [0c-ime-data](https://github.com/0c-biggo/0c-ime-data) 的詞對統計重建 |
| Release：神經模型 `models/select`、`models/punct` | MIT | 不符合（沒有訓練原始碼），自 0.2.3 起移出原始碼 tarball，另附 `0c-ime-models-<版本>.tar.xz`；見「神經模型」 |

## 逐項說明

### 自有程式

著作權屬 Funmula Co., Limited，[LICENSE](../LICENSE) 為 AGPL-3.0-only，[TERMS.md](TERMS.md) 的另議商業授權不限制 AGPL 已授予的權利。

### Go 相依模組

| 模組 | 版本 | 授權 | Debian main |
|---|---|---|---|
| github.com/Microsoft/go-winio | v0.6.2 | MIT | golang-github-microsoft-go-winio |
| github.com/godbus/dbus/v5 | v5.1.0 | BSD-2-Clause | golang-dbus |
| github.com/clipperhouse/uax29/v2 | v2.7.0 | MIT（一個測試檔為 Apache-2.0） | golang-github-clipperhouse-uax29 |
| golang.org/x/sys | v0.10.0 | BSD-3-Clause＋Go 專利授權 | golang-golang-x-sys |
| github.com/sirupsen/logrus、golang.org/x/mod、golang.org/x/tools | — | MIT、BSD-3-Clause | 已收錄；只在模組圖中，不編進執行檔 |

go-winio 只用於 Windows 的 named pipe，Linux 與 macOS 的執行檔不含。

### Rust 相依 crate

`platforms/settings/Cargo.lock`（設定程式，Tauri 2）與 `platforms/windows/Cargo.lock`（Windows 外殼）共 463 個 crate，依 crates.io 的授權欄位全部是自由授權：

| 授權 | crate 數 |
|---|---|
| MIT、Apache-2.0，或兩者擇一 | 389 |
| 擇一或並列授權，含 Zlib、BSD-3-Clause、Unicode-3.0、0BSD、Unlicense、CC0-1.0、LGPL-2.1-or-later、LLVM-exception | 45 |
| Unicode-3.0（ICU4X 相關） | 18 |
| 單一授權：BSD-3-Clause、Zlib、ISC、Apache-2.0 WITH LLVM-exception | 6 |
| MPL-2.0（`cssparser`、`selectors` 等） | 5 |

`windows_*`、`winapi-*-pc-windows-gnu` 這類 crate 內含 Windows 匯入程式庫的預編譯檔，`webview2-com-sys` 綁定 WebView2；這些只在 Windows 目標使用，Linux 建置不會用到，Debian 的 Rust 套件也會把它們排除。

### 內附第三方程式碼

`platforms/linux-fcitx5/third_party/nlohmann/json.hpp` 為 MIT。Debian 打包時可改用系統的 `nlohmann-json3-dev`。

### 詞庫來源

`data/sources/ledger.tsv` 列出的每個來源都標為可散布，授權全文在 `data/sources/licenses/`：

| 來源 | 授權 |
|---|---|
| curated、0c_collect、0c_collect-ns、0c_collect-ss | CC BY-SA 4.0 |
| cc-cedict、zhwiki | CC BY-SA 4.0 |
| chewing-v4 | CC BY 4.0（上游 `dict/chewing_v4/README.md` 確認） |
| mcbopomofo、mcbopomofo-rare | MIT |
| cns11643、moi-places、moi-villages、moe-schools、nhi-hospitals、gov-agencies | 政府資料開放授權條款第 1 版 |
| opencc | Apache-2.0 |

政府資料開放授權條款第 1 版只要求署名，允許重製、改作、散布與商業利用，條款第 4 條聲明與 CC BY 4.0 相容。

0c_collect 系列與 mcbopomofo 的 `phrase.occ` 都是從語料統計出的詞頻；授權的是統計表，不含原始文章，統計表本身就是修改時使用的形式。Debian main 已有同類前例（`rime-essay` 的詞頻、`libtabe` 的詞庫）。mcbopomofo 的 `BPMFMappings` 源自 libtabe 的 `tsi.src`（BSD 授權，`libtabe` 在 Debian main）。

`curated` 與三筆 `0c_collect` 的著作權屬 Funmula Co., Limited，署名寫在 `data/sources/ledger.tsv` 與 `data/sources/licenses/`，隨字典的 manifest 與 `NOTICE.txt` 出貨。

### 統計字典

`lexicon.bin` 與 `lm.bin` 由 `scripts/build_release_data.py` 編譯，輸入是 Git 內的詞庫、詞頻，以及 `data/sources/bigram.lock.json` 固定的四個詞對統計檔。統計檔（CC BY 4.0、CC BY-SA 4.0）公開在 [0c-ime-data](https://github.com/0c-biggo/0c-ime-data) 的 Release，不放進主 tarball；Debian 可把它當成第二個上游 tarball（component tarball）。以 0.2.2 原始碼套件與 `bigram-20261019` 重建，輸出與 Release 內附的字典位元組相同，步驟見 [打包與發佈](../PACKAGING.md)。

### 神經模型

兩個模型的權重採 MIT，但模型卡寫明「Training code and training materials are not included」。依 Debian 機器學習政策，沒有公開訓練程式與訓練資料的模型不能進 main，本專案也不公開它們。因此自 0.2.3 起，模型不放在原始碼 tarball，改由同一個 Release 的 `0c-ime-models-<版本>.tar.xz` 另行提供；Git 只保存模型與配對的 lock 檔。沒有模型時輸入法以統計選字運作，`make assets MODELS=0` 只取字典。Debian 可把模型另外打包成 non-free 的選用套件，主套件以 `Suggests` 指向它。

0.2.2 以前的 Release 只有一個 tarball，內含模型，不符合本文件的要求。

### 英文詞表

`data/english/LICENSE.txt` 為 SCOWL 的上游授權（允許使用、修改、散布與販售，只要求保留聲明），Debian 的 `scowl` 套件同樣在 main。

### 圖示與圖片

`.github/assets/`、`internal/settingsapp/`、`platforms/settings/icons/`、`platforms/settings/ui/icon.png`、`platforms/linux-fcitx5/icons/`、`platforms/macos/Icon/` 的圖示與截圖為專案自有。SVG 只以名稱指定字型，沒有內嵌字型檔。依 [legal/README.md](README.md) 採 CC BY-SA 4.0，著作權屬 Funmula Co., Limited，條款全文在 [CC-BY-SA-4.0.txt](CC-BY-SA-4.0.txt)。原始檔都在 repo 內：App 圖示的設計原檔是 `.github/assets/logo.svg`（與官網 favicon 相同）；macOS 的 `0c.tiff`、`AppIcon.icns` 由 `platforms/macos/Icon/make_icon.swift`、`make_app_icon.swift` 繪製產生；設定程式與 `internal/settingsapp` 的 PNG、ICO 是同一設計的點陣輸出；fcitx5 狀態列圖示本身就是 SVG。

## 送 Debian 前的其他事項

以下不影響 DFSG 判定，但 Debian 打包時會遇到：

- **上游 tarball**：0.2.3 起 `0c-ime-<版本>.tar.xz` 不含模型，可直接當 Debian 的上游 tarball；0c-ime-data 的詞對統計可當 component tarball。0.2.2 以前的 tarball 含模型，要以 `Files-Excluded: models/select models/punct` 重新打包成 `+dfsg` 版本。
- **自動更新**：Linux 安裝後 `0c-update` 會建立 systemd user timer，從 `s3.biggo.com` 取得已簽章的更新並自行安裝。Debian 套件由 apt 管理更新，需要能在建置或安裝時關閉自動更新與更新提示。
- **內嵌的第三方副本**：`nlohmann/json.hpp`（Debian：`nlohmann-json3-dev`）與 `internal/simplified/opencc` 的 OpenCC 字典（Debian：`opencc` 1.4.2）。Debian 偏好使用系統套件，至少要在 `debian/copyright` 逐一列出。
- **設定程式**：`platforms/settings` 使用 Tauri 2；`tauri`、`wry`、`tao` 等 crate 目前不在 Debian，要先打包這些 crate，或第一版不含設定程式。
- **工具鏈與執行相依**：`go.mod` 要求 Go 1.26（Debian sid 為 1.27）；`platforms/neural/requirements.txt` 固定 `onnxruntime==1.30.0`、`numpy==2.5.3`，Debian sid 為 ONNX Runtime 1.27.1、NumPy 2.4.6，要確認推論 worker 在這些版本可用。
- **測試金鑰**：`testdata/e2e/` 的私鑰只供測試（見該目錄 README），lintian 會提出警告，需要 override 並說明。

## 維持方式

- 新增相依套件、第三方程式碼或資料來源前，確認授權在「判斷標準」的範圍內，並更新本文件與 `data/sources/ledger.tsv`。
- Release 內每個編譯產物都要能從同一個 Release 公開的內容重建。
- 模型更新時，訓練程式與訓練資料的公開狀態一併記錄在模型卡與本文件。
