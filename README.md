<p align="center">
  <img src=".github/assets/logo.svg" width="96" height="96" alt="靈犀注音 0C">
</p>

<h1 align="center">靈犀注音 0C</h1>

<p align="center">
  <b>免選字，把話打完。</b><br>
  在你的電腦上用 Transformer 看前文選字的注音輸入法，支援 Linux、macOS 與 Windows。
</p>

<p align="center">
  <a href="https://github.com/0c-biggo/0c-ime/releases/latest"><img src="https://img.shields.io/github/v/release/0c-biggo/0c-ime?color=005c73&label=release" alt="Release"></a>
  <a href="https://github.com/0c-biggo/0c-ime/actions/workflows/checks.yml"><img src="https://github.com/0c-biggo/0c-ime/actions/workflows/checks.yml/badge.svg" alt="Checks"></a>
  <a href="legal/README.md"><img src="https://img.shields.io/badge/license-AGPL--3.0-005c73" alt="License"></a>
  <img src="https://img.shields.io/badge/go-1.26-005c73?logo=go&logoColor=white" alt="Go 1.26">
  <img src="https://img.shields.io/badge/platform-Linux%20%7C%20macOS%20%7C%20Windows-005c73" alt="Platforms">
</p>

<p align="center">
  <a href="https://0c.biggo.com">官網</a> ·
  <a href="https://0c.biggo.com/download/">下載</a> ·
  <a href="https://0c.biggo.com/technology/">選字技術</a> ·
  <a href="https://0c.biggo.com/guide/basics/">操作指南</a> ·
  <a href="https://0c.biggo.com/docs/cli/">CLI 文件</a> ·
  <a href="https://0c.biggo.com/help/">支援</a> ·
  <a href="https://0c.biggo.com/privacy/">隱私</a> ·
  <a href="https://github.com/0c-biggo/0c-ime/issues">回報問題</a>
</p>

<p align="center">
  <img src=".github/assets/demo.svg" width="800" alt="打字示範：整句注音打下去不用選字，按 Tab 把話打完">
</p>

> **English** — 0C (靈犀注音) is a Zhuyin (Bopomofo) input method for Traditional Chinese. An on-device Transformer language model reads what you have just written and picks the right homophones, so you can type a whole sentence without stopping to choose characters. It runs entirely on your computer, and ships shells for Fcitx5, IBus, macOS InputMethodKit and Windows TSF.

## ✨ 特色

- 🧠 **Transformer 選字**：自行訓練的語言模型參考整段前文與注音判斷同音字，大多數時候不必停下來挑字。
- ⇥ **Tab 補全**：有把握才提示後續字詞，按 Tab 接受；補全只來自詞庫與個人片語，不生成新內容。
- 🔒 **全在本機**：選字、補標點與個人學習都在你的電腦上以 CPU 執行，不連網、不上傳打字內容。
- 📚 **我的詞彙**：人名、術語、英文指令加進來就排在前面，也看得到靈犀學到了什麼。
- 🔤 **中英混打、模糊音、Emoji 與顏文字**：ㄗ／ㄓ、ㄘ／ㄔ、ㄙ／ㄕ、ㄣ／ㄥ 近音放在後面備選；按 = 開 Emoji 選單。
- 🔁 **輸出簡體字、自訂選字鍵**：看繁體打字、送出時才轉簡體，可從輸入法選單一鍵切換；選字鍵可用 1–0 或 asdfghjkl;。
- ✍️ **送出時補標點**（Beta）：Enter 送出時在漢字之間補上 ，。？！、；，不改動你打的字。
- 🛟 **自動退回**：模型無法啟動或回應太慢時改用統計引擎排序，打字不會卡住。

<p align="center">
  <img src=".github/assets/settings-vocabulary.webp" width="640" alt="靈犀注音設定的「我的詞彙」頁">
</p>

## 🎯 免選字是怎麼做到的

傳統注音輸入法靠詞庫與詞頻挑字，只看得到相鄰的一兩個詞。靈犀注音在統計引擎之上多了一層 Transformer 重排：

```mermaid
flowchart TD
    K["⌨️ 注音按鍵"] --> L["📖 詞庫查詞，建立詞圖"]
    L --> V["📊 統計引擎：unigram／bigram＋Viterbi<br/>取 N-best 前 8 名"]
    C["📝 已送出的前文<br/>最多 64 字"] -.-> T
    V --> T{"🧠 Transformer 重排<br/>前文＋注音＋候選"}
    T -->|"100 ms 內完成"| P["👤 套用個人詞庫"]
    T -.->|"逾時或模型無法使用"| F["🛟 統計排序"]
    F --> P
    P --> O["✅ 首選上屏"]
    O -->|"Enter 送出"| Q["✍️ 補標點（Beta）"]
```

1. **列出可能的句子** 📖：依注音在詞庫找出所有可能的字詞，以 unigram／bigram 統計語言模型與 Viterbi 在詞圖上搜尋最可能的組合，打字時即時更新。
2. **Transformer 重排** 🧠：讀入剛送出、游標仍緊接其後的前文（最多 64 字）、目前的注音與每個候選，替前 8 名重新評分。attention 能同時參考整段前文，不只看緊鄰的詞。
3. **本機學習** 👤：你選過、改過的字詞記在本機個人詞庫，下次同樣的情況優先出現。
4. **有把握才補全** ⇥：後續字詞的信心夠高時才顯示 Tab 建議。

## 📈 評測：首選錯誤少約六成

在事前凍結、模型沒看過的台灣新聞測試（2026-09-24〜26，共 6,000 例，一半是困難的同音短詞）上：

| 方法 | 首選正確率 | 首選錯誤率 |
|---|---:|---:|
| 新酷音 0.11.0 | 64.2% | 35.8% |
| 小麥注音 | 67.2% | 32.8% |
| 靈犀注音（只用統計引擎） | 76.9% | 23.1% |
| **靈犀注音（Transformer 重排）** | **86.0%／86.1%** | **14.0%** |

```mermaid
xychart-beta
    title "首選錯誤率（越低越好）"
    x-axis ["新酷音", "小麥注音", "靈犀・統計", "靈犀・Transformer"]
    y-axis "錯誤率 %" 0 --> 40
    bar [35.8, 32.8, 23.1, 14.0]
```

測試規格、成功標準與評分程式都在讀取資料前凍結，只評一次；讀音由字典合成、答案取原文，不是真人打字紀錄。外部輸入法採對它們最有利的輸入方式、無個人詞庫。靈犀注音的兩個數字來自兩個不同的訓練種子；「只用統計引擎」也是模型無法啟動時的表現。免選字是目標而非保證：罕見人名與詞庫外的詞仍可能要手動選。

### 🧩 模型

| | 選字（`models/select/`） | 補標點（`models/punct/`） |
|---|---|---|
| 架構 | 因果 Transformer decoder：8 層、寬度 512、8 個 attention head，約 3,261 萬參數 | 雙向 Transformer encoder |
| 輸入 | 前文（最多 64 字）＋注音＋候選 | 送出的文字，不看前文 |
| 權重 | [Funmula/0c-sequence-d512](https://huggingface.co/Funmula/0c-sequence-d512) | [Funmula/0c-punct-d512](https://huggingface.co/Funmula/0c-punct-d512) |

模型從零開始訓練，不使用通用大型語言模型的權重，採用 RoPE、GeGLU 等現代化元件。一般桌機 CPU 上選字的 P95 延遲約 7 ms（短片段）至 16 ms（長片段）；兩個模型合計約 250 MB。密碼欄位、暫停學習及英數模式下不送任何文字給模型。更多數字與方法見 [選字技術](https://0c.biggo.com/technology/)。

## 📥 安裝

Windows、macOS、Linux 安裝包請到官網下載：👉 [0c.biggo.com/download](https://0c.biggo.com/download/)

發行版打包請用 [Releases](https://github.com/0c-biggo/0c-ime/releases/latest) 的 `0c-ime-<版本>.tar.xz`：內附現成字典，符合 DFSG，解壓即可建置；已配對的神經模型在另附的 `0c-ime-models-<版本>.tar.xz`。見 [打包與發佈](PACKAGING.md)。

## 🛠️ 從原始碼建置

需要 Go 1.26 以上；測試與發佈工具使用 Python 3.11 以上。以下指令在 repo 根目錄執行，不會安裝或重啟桌面輸入法：

```sh
git clone https://github.com/0c-biggo/0c-ime.git
cd 0c-ime
make assets          # 下載與這份原始碼相符的字典與模型
go test ./...
go run ./cmd/0c-cli -data assets/data
```

`make assets` 從同版本的 [Release](https://github.com/0c-biggo/0c-ime/releases) 取得原始碼與模型 tarball（合計約 275 MB），驗證後把現成字典放到 `assets/data/`、已配對模型放到 `models/select/` 與 `models/punct/`，和出貨內容相同；0.2.2 起 Release 附的 Source code 壓縮檔也適用。要指定版本用 `make assets RELEASE=v0.2.2`，只取字典用 `make assets MODELS=0`（約 36 MB，以統計選字運作）。

`cmd/` 下工具的旗標、輸入輸出與範例見官網 [CLI 文件](https://0c.biggo.com/docs/cli/)，例如 [0c-cli](https://0c.biggo.com/docs/cli/0c-cli/)、[0c-update](https://0c.biggo.com/docs/cli/0c-update/)、[0c-mcp](https://0c.biggo.com/docs/cli/0c-mcp/)。

發行版維護者以 `make install PREFIX=/usr` 做系統安裝、由套件管理員更新，見 [Linux 系統安裝](platforms/linux-system/README.md)。

各平台外殼另需各自的工具鏈與系統函式庫：

| 平台 | 外殼 | 建置說明 |
|---|---|---|
| 🐧 Linux | Fcitx5（C++） | [platforms/linux-fcitx5](platforms/linux-fcitx5/README.md) |
| 🐧 Linux | IBus（Go） | [platforms/linux-ibus](platforms/linux-ibus/README.md) |
| 🍎 macOS | InputMethodKit（Swift） | [platforms/macos](platforms/macos/README.md) |
| 🪟 Windows | TSF（Rust） | [platforms/windows](platforms/windows/README.md) |
| ⚙️ 設定程式 | Tauri | [platforms/settings](platforms/settings/README.md) |
| 🧠 神經推論 | Python＋ONNX Runtime | [platforms/neural](platforms/neural/README.md) |

## 🗂️ 專案結構

```text
core/         輸入法邏輯：注音解析、詞圖解碼、語言模型、候選重排、個人學習（不做 I/O）
bridge/       引擎與外殼之間的行程間通訊（IPC）
internal/     設定、自動更新、神經推論 worker、詞庫編譯與評測等共用程式
cmd/          執行檔入口：0c-server、0c-ibus、0c-update、0c-cli、0c-compile、0c-eval…
platforms/    Linux／macOS／Windows 外殼、設定程式、神經推論
data/         詞庫來源、來源帳本與授權聲明
models/       上游模型與字典配對的固定清單
scripts/      建置、打包與回歸測試工具
testdata/     測試資料
```

詞庫格式與資料來源見 [data/README.md](data/README.md)，模型版本與配對見 [models/README.md](models/README.md)。

## 🤝 參與貢獻

歡迎回報問題與送 PR！請先看 [參與開發](CONTRIBUTING.md)：回報時附上版本、平台與逐鍵重現步驟；改程式前後跑一次檢查：

```sh
python3 scripts/check_public_tree.py
go test ./...
go vet ./...
make -C platforms/linux-fcitx5 test
make test-upgrade
make test-release
```

版本變更見 [CHANGELOG](CHANGELOG.md)。

## 📄 授權

自有程式採 AGPL-3.0-only，另可洽談商業授權；自有圖示與圖片採 CC BY-SA 4.0；資料、模型與第三方元件依各自授權，見 [授權說明](legal/README.md)。
