# 專案範圍

`0c-ime` 包含靈犀注音輸入法的 Go 核心、IPC、平台介接、設定程式、資料包工具與產品回歸測試。

| 目錄 | 內容 |
|---|---|
| `core/` | 不依賴 I/O 的輸入法邏輯 |
| `bridge/`、`internal/`、`cmd/` | IPC、系統整合與命令列入口 |
| `platforms/` | Linux、macOS、Windows 外殼與設定程式 |
| `data/` | 詞庫來源格式、帳本、授權聲明與編譯輸入 |
| `scripts/` | 建置、打包與回歸工具 |
| `testdata/` | 測試資料 |
| `models/` | 固定模型版本與雜湊；權重在 Release tarball 中加入 |
| `legal/` | 授權說明與元件聲明 |

模型訓練管線、checkpoint 與研究結果不屬於本 repo。程式與資料的授權範圍見 [授權說明](legal/README.md)。

開發入口為 [README.md](README.md) 與 [CONTRIBUTING.md](CONTRIBUTING.md)。
