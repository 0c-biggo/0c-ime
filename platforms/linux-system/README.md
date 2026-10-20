# Linux 系統安裝（發行版套件）

給發行版維護者（例如 Debian）：以 `make install PREFIX=/usr` 把靈犀注音裝到系統目錄，更新交給套件管理員（#181）。官網的一行安裝與完整套件照舊裝在家目錄、自己更新，跟這裡無關；同一個帳號兩種都裝時，以官網安裝優先。

## 建置與安裝

在 repo（或原始碼 tarball）根目錄：

```sh
make build   PREFIX=/usr VERSION=0.2.3
make install PREFIX=/usr VERSION=0.2.3 DESTDIR=$PWD/debian/tmp
```

`make` 不帶目標只印說明；Debian 的 `dh_auto_build`、`dh_auto_install` 請以 override 指定 `build`／`install` 與上面的變數。

| 變數 | 預設 | 說明 |
|---|---|---|
| `PREFIX` | `/usr/local` | 發行版用 `/usr` |
| `BINDIR`、`LIBDIR`、`LIBEXECDIR`、`DATAROOTDIR` | `$(PREFIX)/bin` 等 | 依 GNU 慣例 |
| `DESTDIR` | 空 | 打包用的暫存根目錄 |
| `VERSION` | `git describe` 的 tag | 沒有 `.git` 時必須指定；寫進引擎與 `distribution.json`，設定程式顯示這個版本 |
| `DATA` | `assets/data` | 字典目錄（0c-ime 原始碼 tarball 內附，`make assets` 取得；也可指向完整套件的 `data/` 或 `0c-compile` 的輸出） |
| `FCITX5`、`IBUS`、`SETTINGS` | `1` | 設成 `0` 就不建、不裝那一項 |
| `SETTINGS_BIN` | 空 | 已建好的 `0c-settings`；空的時候以 `cargo build --locked` 建置 |
| `FCITX5_ADDONDIR` | `PREFIX=/usr` 時是 `pkg-config --variable=libdir Fcitx5Core`/fcitx5（Debian：`/usr/lib/<triplet>/fcitx5`），否則 `$(LIBDIR)/fcitx5` | 不是 Fcitx5 自己的 addon 目錄時，addon 設定的 `Library` 寫絕對路徑 |
| `IBUS_COMPONENTDIR` | `$(DATAROOTDIR)/ibus/component` | IBus 只讀 `/usr/share/ibus/component`（及 `IBUS_COMPONENT_PATH`） |

`build` 建出 `build/system/` 下的 `0c-server`、`0c-update`、`0c-ibus`、`0c-mcp`、`fcitx5/lib0c.so` 與設定程式；系統目錄在建置時寫進程式（Go 的 `-ldflags -X …/internal/installs.LibexecDir=…`，C++ 的 `-DZC_SYSTEM_LIBEXECDIR`，設定程式的 `ZC_SYSTEM_LIBEXECDIR` 環境變數），所以 `build` 與 `install` 要用同樣的 `PREFIX`。直接 `go build` 的程式預設就是 `PREFIX=/usr` 的位置。

`install` 不寫安裝收據、不建 systemd 單元、不碰任何家目錄，也不執行任何會連網的動作。

### 建置相依

| 用途 | Debian 套件 |
|---|---|
| Go 程式（引擎、更新工具、IBus 外殼、0c-mcp） | `golang-go`（Go 1.26 以上，見 `go.mod`）與 `go.sum` 列的模組 |
| Fcitx5 外殼 | `g++`（C++20）、`pkg-config`、`libfcitx5core-dev`、`libfcitx5utils-dev`、`libfcitx5config-dev`；內附的 `nlohmann/json.hpp` 可換成 `nlohmann-json3-dev` |
| 設定程式（Tauri 2） | `cargo`／`rustc` 1.90 以上、`libwebkit2gtk-4.1-dev`、`libgtk-3-dev`，以及 `Cargo.lock` 列的 crate |

### 執行相依

| 用途 | Debian 套件 |
|---|---|
| 輸入法框架（擇一或兩者） | `fcitx5`，或 `ibus` |
| 設定程式 | `libwebkit2gtk-4.1-0` |
| 神經選字、補標點（選用，見 #180） | `python3`、`python3-numpy`、`python3-onnxruntime`；沒有模型或缺少時自動退回統計選字 |

## 放到哪裡

以 `PREFIX=/usr`、Debian amd64 為例：

| 路徑 | 內容 |
|---|---|
| `/usr/bin/0c-settings` | 設定程式 |
| `/usr/bin/0c-mcp` | AI 工具用的 MCP 伺服器（stdio） |
| `/usr/libexec/0c/0c-server` | 引擎，由外殼在第一個按鍵帶起：`0c-server -data /usr/share/0c/data` |
| `/usr/libexec/0c/0c-update` | 設定、詞彙與本機 API 的命令列入口（這個模式下不更新） |
| `/usr/libexec/0c/0c-ibus` | IBus 外殼，由 ibus-daemon 依元件檔啟動 |
| `/usr/lib/x86_64-linux-gnu/fcitx5/lib0c.so` | Fcitx5 外殼 |
| `/usr/share/fcitx5/addon/0c.conf`、`/usr/share/fcitx5/inputmethod/zuin.conf` | Fcitx5 addon 與輸入法（ID `zuin`） |
| `/usr/share/ibus/component/0c.xml` | IBus 元件（`org.biggo.ime.IBus`、engine `0c`），由 `platforms/linux-ibus/0c.xml.in` 產生 |
| `/usr/share/0c/data/` | 字典 |
| `/usr/share/0c/neural/sequence_runtime.py`、`punct_runtime.py` | 神經推論 worker |
| `/usr/share/0c/distribution.json` | 模式標記，例如 `{"schema":1,"updates":"package-manager","version":"0.2.3"}` |
| `/usr/share/applications/0c-settings.desktop`、`/usr/share/icons/hicolor/…` | 應用程式選單項目、設定程式圖示、注／英狀態圖示 |

個人資料照舊在 `~/.local/share/zuin`（`user.json`、詞彙）與 `~/.local/share/0c/settings.json`。

## 由套件管理員更新

`/usr/share/0c/distribution.json` 的 `updates` 是 `package-manager`，而且執行的是 `/usr/libexec/0c/0c-update` 時：

- `0c-update` 拒絕 `check`、`download`、`download-now`、`apply`、`install`、`verify`、`rollback`、`uninstall`、`schedule`、`auto`、`finish`、`mode`、`pause`、`resume`、`snooze` 與通知的動作，說明「由系統套件管理員安裝與更新」；帶 `-json` 時回契約錯誤 `package_managed`。不打索引伺服器、不建排程、不複製穩定副本。
- `status`、`settings`、`set`、`vocab`、`api`、`engine`、`open-settings` 照常。`status -json` 回報 `"updates":"package-manager"`、`installed` 是 `distribution.json` 的版本，並以 `installs` 列出找到的安裝。
- 設定程式的「更新」頁只顯示「由系統套件管理員更新」與目前版本，沒有檢查、下載、安裝或回復；Fcitx5 與 IBus 的輸入法選單沒有更新列。
- 引擎不常駐：外殼在第一個按鍵帶起它（`set -apply` 換設定後也會立刻帶起）。套件升級換掉 `/usr` 的檔案後，執行中的外殼要等宿主重啟才換新。

標記檔不存在或 `updates` 不是 `package-manager` 時，`0c-update` 照官網安裝的方式運作。官網安裝（家目錄）的 `0c-update` 一律不受系統標記影響。

## 找引擎與工具的規則

外殼（Fcitx5 的 `src/locate.h`、`0c-ibus`）、`0c-update` 與設定程式都照同一套規則（Go：`internal/installs`；C++：`platforms/linux-fcitx5/src/locate.h`），每一項都先找官網安裝、再找系統安裝：

| 項目 | 官網安裝（先找） | 系統安裝 |
|---|---|---|
| 引擎 | `~/.local/bin/0c-server`（不帶參數，依 `engine.json` 轉交到核心更新的版本） | `/usr/libexec/0c/0c-server -data /usr/share/0c/data` |
| 0c-update | `~/.local/share/zuin/bin/0c-update`，再來 `~/.local/bin/0c-update` | `/usr/libexec/0c/0c-update` |
| 字典 | `~/.local/share/zuin/data`（或核心更新的版本目錄） | `/usr/share/0c/data` |
| 設定程式 | `~/.local/bin/0c-settings`（核對安裝收據的雜湊） | `/usr/bin/0c-settings`（核對屬於 root、其他人不能改） |

兩份都在時用官網安裝（使用者自己裝的通常比較新）；設定程式「問題排解」列出兩份並標示使用中的那一份。系統的 `0c-update open-settings`（例如 IBus 輸入來源的齒輪）在官網安裝也在時交給官網那一份。Fcitx5 依 XDG 順序先讀家目錄的 addon 設定，所以載入的是官網安裝的外殼；IBus 兩個元件同名，哪一個被啟動都會照上面的規則連到官網安裝的引擎。

## 加入輸入法

- Fcitx5：在 Fcitx5 設定把「靈犀」加入輸入法清單（套件安裝後重新啟動 Fcitx5）。
- IBus（GNOME）：「設定 → 鍵盤 → 輸入來源」加入「漢語（台灣）→ 靈犀注音」。官網安裝的 `0c-ibus register` 另外處理的 GTK／Qt 輸入模組設定（[IBus 外殼](../linux-ibus/README.md#gnome焦點移走時丟組字)）系統安裝不會自動做。

## 驗收

Debian 13 容器（`--network none`）：`make build`／`make install PREFIX=/usr` 後，Fcitx5（DBus 輸入介面）與 IBus（原生 ibus-daemon）都打出「你好」，外殼自己帶起 `/usr/libexec/0c/0c-server -data /usr/share/0c/data`；選單沒有更新列；更新命令被拒；全程以 `strace -e connect` 確認只有 AF_UNIX 連線。另放一份官網安裝到家目錄後，Fcitx5 帶起的是 `~/.local/bin/0c-server`，問題排解兩份都列出。
