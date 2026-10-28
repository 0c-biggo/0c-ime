# 靈犀注音設定（Tauri）

獨立的設定程式（#47）：Rust 外殼＋系統 WebView，畫面是純 HTML／CSS／JS，沒有 npm、沒有打包器。設定邏輯與更新驗證都在 Go；這個程式只呼叫 `0c-update` 的 JSON 契約（[JSON 契約實作](../../cmd/0c-update/contract.go)），沒有網路、登入或機密。

## 結構

| 路徑 | 內容 |
|---|---|
| `src/updater.rs` | 找出並啟動 `0c-update`、逾時、解析 JSON、匯出摘要；附單元測試 |
| `src/main.rs` | 指令白名單、視窗、單一實例 |
| `src/watch.rs` | 「我的詞彙」的即時同步：常駐 `0c-update vocab watch`、轉成 `vocab-changed` 事件、意外結束時重新連上 |
| `ui/` | `index.html`、`app.css`、`app.js`；設定項目依契約動態產生。`vocab.js` 是「我的詞彙」頁，`models.js` 是「模型」頁，`input-styles.js` 是「輸入習慣」頁 |
| `test/` | `smoke.sh`（容器 Xvfb 冒煙測試）、`vocab.sh`（我的詞彙）、`models.sh`（模型）、`styles.sh`（輸入習慣）、`fake-update.py`（測試用的 0c-update 替身，只模擬 vocab、api 與 models）、`*_test.js`（`node --test`，不碰畫面的判斷） |
| `build.rs`、`capabilities/main.json` | 只有 `build.rs` 列出的指令能被前端呼叫，capability 只授權這些指令與事件監聽 |
| `tauri.linux.conf.json` | Linux 的 identifier 改為 `com.biggo.zc.settings`：外掛用它組 D-Bus 服務名，而服務名的每一段不能以數字開頭，`com.biggo.0c.settings` 會讓程式一啟動就 panic。macOS 與 Windows 維持 `com.biggo.0c.settings` |
| `tauri.conf.json` | CSP：`default-src 'none'; script-src 'self'; style-src 'self'`；不載入任何遠端內容 |

## 輸入習慣（#225）

側欄「輸入習慣」（`--page styles`）讀取 `settings -json` 的 `profiles`；挑選風格先列操作摘要與設定差異，按「套用這個習慣」才呼叫 `profile select`。內建風格固定，顯示其實際設定值；`items[].locked` 只鎖定編輯，與條件不適用的 `disabled` 分開。鍵盤排列保持獨立。

「複製成自訂」輸入名稱後呼叫 `profile copy`，建立並啟用副本，相關設定恢復可編輯。自訂可重新命名，非使用中的自訂可確認後刪除。中文名稱組字期間背景更新不重建輸入框；後端回報 pending 時提示完成目前輸入後套用。

Rust 的 `profile_command(action,id,name)` 只允許 `select`、`copy`、`rename`、`delete`；名稱作為單一參數交給 `0c-update profile`，select／copy 固定附 `-apply`。實際驗證、持久化及引擎交接由 Go 負責。`test/input_styles_test.js` 驗證預覽與提交、唯讀值、命名管理及失敗恢復；`test/styles.sh` 在真正 WebView 操作後核對 Go 契約。

## 中英切換、選字、鍵盤與符號（輸入設定，#207）

0.2.5 起原本一頁 19 項的「輸入設定」拆成側欄的三頁，不摺疊（摺起來的設定會找不到）。各頁依 `app.js` 的 `INPUT_PAGES` 排列，順序也是 Tab 順序：

| 頁面（`--page`） | 項目 |
|---|---|
| 中英切換（`mode`） | 中英切換鍵、開始時的中英模式、切換視窗後的中英模式、中英混打、英文模式自動補字、Caps Lock 開著時直接打英文（Windows、Linux） |
| 選字（`candidates`） | 選字方式（神經選字）、依前文選字、空白鍵選字、修改字詞的位置、選字後游標移到下一個字、選字排列、選字鍵、按 Esc 清除整段組字，另一張卡是模糊音總開關 |
| 鍵盤與符號（`keyboard`） | 鍵盤排列、Ctrl＋標點打全形、提交時補標點（Beta）、Emoji 選單（子選項「將 = 放在第一項」） |

側欄順序：總覽、中英切換、選字、鍵盤與符號、輸出設定、我的詞彙、模型、更新、問題排解、關於。「輸出設定」（`output`）照舊獨立。契約裡不在任何一頁的新項目（較新的 0c-update 才有）畫在「選字」最後，所以新增設定不必先改畫面；要放別頁再加進 `INPUT_PAGES`。舊的頁名 `input`（0.2.5 以前的 `0c-update open-settings input`、說明文件）照樣接受，畫面導到側欄第一頁「中英切換」（`PAGE_ALIAS`；Go 與 Rust 的白名單也保留它，舊版設定程式照舊開輸入設定）。三份頁名清單由 `internal/settingsapp` 的 `TestPageNamesMatchTheSettingsApp` 核對。總覽的「有設定已儲存、尚未套用」帶到第一個尚未套用的項目所在的那一頁。

契約的項目帶 `disabled` 時（倚天 41 鍵時的中英混打、Emoji 選單與等號首項，許氏時的中英混打，#183；不用 Shift 時的開始模式，#184）開關反灰、畫成關閉，名稱旁顯示 `disabled_reason`，不在 Tab 順序裡；設定檔的值不動，切回標準排列照原值顯示（#83）。造成停用的那一項（`disabled_by`）在別頁時，說明下面多一個「到「鍵盤與符號」修改「鍵盤排列」」，按了切到那一頁並把焦點放在那一項（例如中英混打在「中英切換」、鍵盤排列在「鍵盤與符號」）；在同一頁（開始模式就在中英切換鍵下面）不另外放。

Emoji 選單以一個開關控制（`emoji-shortcut`：off／equals），預設開啟，保留使用者明確關閉的設定。另有 `emoji-equals-first` 開關（預設 on），可取消第一項的等號；設定說明提醒關閉後仍可按兩次 == 輸入一個 =。模糊音顯示一列總開關，預設開啟（四組 select），開啟時以同一次設定交易將四組近音都設為 select，關閉時全部設 off；沒有逐組控制或自動修正單選。舊設定只啟用部分配對或使用 auto 時，畫面視為開啟，載入不改寫；關閉再開啟才統一成四組 select。CLI 的逐組設定與原有檔案格式仍保留，未知欄位與唯讀保護沿用既有契約。

## 我的詞彙（0.2.0，#125）

`features.vocab` 為真時（0.2.0 起的 `0c-update` 一律為真；舊版沒有這個欄位時不顯示），側欄「輸出設定」下面出現「我的詞彙」，總覽多一張小卡（詞數與 AI 助手最近加的數量；沒有詞時是「把常打錯的人名、術語加進來」）。後端命令、Tauri 指令與事件見 [詞彙命令實作](../../cmd/0c-update/vocab.go)。

- **我加的詞**：頂端是搜尋與新增列。打字時（組字結束後 250 毫秒）查 `annotate`，每個字一格讀音：破音字是黃色下拉選單，查不到的字自己填注音；Enter 加入。英文詞（`ls`、`kubectl`，#146）不查讀音，直接 Enter 加入，清單上讀音的位置標示「英文」、沒有排名。一次貼上多行變成預覽清單（不是漢字也不是英文詞的行不能勾），勾選後同一批加入。每列：詞、讀音（點了改，可加到 4 個讀音，送出時附 `expect`）、排名徽章（`rank`：首選／第 N 個／第 N 頁，滑過顯示前面是哪個詞）、來源與時間、刪除。超過 1,000 筆分頁，搜尋交給引擎（`list` 的 `query`）。
- **靈犀學到的**（#182）：`learned` 清單，`kind` 顯示成「常一起用」（`phrase`）或「你選過的寫法」（`reading`），不顯示次數；頂端說明三個動作，每列有勾選框與「保留」（`keep`）、「移除」（`unlearn`）、「不再學」（`forget`），已是自訂詞的顯示「已在我加的詞」、不能勾。勾選列有全選（部分勾選時半勾）與三個批次按鈕，一次送一個指令。移除、不再學、恢復學習之後先拿掉那幾列，底部提示附「復原」（`learned-undo`），焦點交給它；保留是自訂詞的批次，照舊從「最近變更」也能復原。`forgotten_count` 大於 0 時右上角出現「不再學的詞（N）」，打開是另一個清單（`forgotten`），每列與批次都能「恢復學習」（`relearn`），「‹ 靈犀學到的」返回；全部恢復後自動返回、入口消失。
- **我的片語**：新增（Ctrl／⌘＋Enter）、刪除、↑↓ 或拖曳把手排序（`phrase-move`）。拖曳用指標事件，不用 HTML5 拖放（各平台 WebView 的拖放處理不一致）。
- **最近變更**：最近 5 批（可展開全部）：時間、誰（你／AI 助手／打字時／學到的）、摘要、復原；點摘要看明細。
- **讓 AI 幫你加詞**（頁面最下方，預設收合）：開關旁一行「AI 助手讀得到你加的詞」；token 預設遮住，可顯示、複製、重新產生（先確認「已設定的 AI 工具要重新貼上設定」），列出建立與最後使用時間；「你用哪個 AI 工具？」只顯示選到的那段範例（網址、token、`0c-mcp` 路徑自動填好，未顯示 token 時範例裡也遮住，「複製」一律複製完整內容，含 token 的註明不要分享）；「測試連線」。埠被占用時可以換埠。
- 引擎沒有執行時只能查看：新增列停用、刪除與復原鈕藏起來，頁首說明。
- 這一頁的 DOM 只建一次，新增列與搜尋框在頁面存在期間不重建（組字中重建會打斷輸入法）；各區塊重畫時以 `data-k` 找回原本的鍵盤焦點，刪除後焦點交給底部提示的「復原」。

`0c-mcp` 三平台的套件都內附，放在不隨版本變動的位置：Linux `~/.local/bin/0c-mcp`、Windows `%LOCALAPPDATA%\Zuin\bin\0c-mcp.exe`（版本目錄那份的穩定副本）、macOS 輸入法 App 的 `Contents/MacOS/0c-mcp`。那個位置沒有檔案（較舊的版本）時範例下方註明「這個版本還沒附 0c-mcp」。

UI 測試：`python3 scripts/test_settings_ui.py --settings <輸出>/0c-settings [--only vocab]`。前半用 `test/fake-update.py` 模擬詞彙與 AI 助手的回應（其他命令轉給真的 `0c-update`；`--init … old` 模擬沒有 `features.vocab` 的舊版，入口要藏起來），檢查畫面送出的指令與結果；後半用真的 `0c-update`＋`0c-server`（`testdata/toy`）從畫面加詞、命令列以 AI 助手身分加詞後畫面跟上，以及強制結束設定程式後 watch 跟著結束。

## 模型（#180）

側欄「模型」在「更新」上面（`--page models`、`0c-update open-settings models`），總覽的「神經選字」列有「查看」連過來。選字、補標點各一張卡片：引擎回報的狀態（文字同總覽）、版本、路徑、`model.onnx` 的 SHA-256（等寬字型、截斷顯示、可複製）；`0c-update models status` 給了 Hugging Face 網址（本機雜湊等於 `models.lock.json` 時才有）就顯示連結與授權。

- 內建模型（`source=bundled`）只顯示資訊，沒有按鈕。
- 可下載（`can_download`，只有沒有內建模型的 Linux 系統安裝）：頁首一句狀態（未安裝、已安裝、需要重新下載、字典更新後自動重新配對），下載中改顯示進度條與已下載大小、核對中、配對中；按鈕「下載模型」（上方寫明大小、Hugging Face 的 repo 與 revision、授權）、下載中「取消」、個人目錄的模型「刪除」（先確認，焦點在「取消」）。`runtime.ok=false` 時列出要 `apt install` 的套件（可複製），`last_error` 顯示它的 `message`。
- 不能下載又沒有模型：依 `disabled_reason`（platform／distribution）說明原因，沒有按鈕。`dictionary_mismatch`（安裝的字典不在 pairing.lock 上，例如發行版自己重建字典）不論有沒有下載過的權重都說明「不能使用下載的模型」，不顯示「已安裝」、不給下載；個人目錄裡的仍可刪除。

網路與檔案動作都在 `0c-update models status|download|cancel|remove -json`；畫面每秒讀一次 status 的 `operation`（只在這一頁顯示中、有進行中的作業時），同「更新」頁。Tauri 指令：

| 指令 | 參數 | 說明 |
|---|---|---|
| `models_command` | `action`：`status`／`download`／`cancel`／`remove` | 對應 `0c-update models <action> -json`，結果原樣回傳；status 逾時 20 秒，其他 60 秒（download 在背景開始、立刻回傳） |
| `open_url` | `url` | 交給系統瀏覽器（Linux `xdg-open`、macOS `open`、Windows `rundll32 url.dll,FileProtocolHandler`）；只放行 `https://huggingface.co/` 開頭、路徑只有英數與 `/._-` 的網址。設定視窗本身照舊不離開本機頁面 |

UI 測試：`python3 scripts/test_settings_ui.py --settings <輸出>/0c-settings [--only models]`，`fake-update.py --models` 依契約模擬內建、未安裝、下載中、已安裝、需要重新下載、缺推論元件、發行版關閉下載、字典不在配對清單上；外部網址交給 PATH 上假的 `xdg-open` 記下來。

## 建置

Linux（容器）與 macOS（在 Apple Silicon 的 Mac 上）各有建置腳本，打包機不需要 Rust：

在容器裡建置（Ubuntu 22.04、固定 Rust 版本），打包機不需要 Rust：

```sh
python3 scripts/build_settings_linux.py --version <版本> --test
```

輸出在 `build/settings/linux-<arch>/`：`0c-settings`（約 3–4 MB）與 `NOTICES.txt`。macOS：

```sh
python3 scripts/build_settings_macos.py --version <版本> --test    # 在 Apple Silicon 的 Mac 上
```

把 Rust 裝在 `--out` 旁的快取（rustup 以 `--no-modify-path` 安裝，不改任何 shell 設定檔），輸出 `build/settings/macos-arm64/0c-settings.app`（ad-hoc 簽章）與 `NOTICES.txt`；`scripts/package_macos.py --settings` 把 App 放進 `0c.app/Contents/Helpers/`，先以 bundle 簽章再簽外層並公證。直接用 `cargo build` 時**必須**加 `--features tauri/custom-protocol`，否則會編成開發模式並嘗試載入 `devUrl`。`Cargo.lock` 納入版本控制，建置一律 `--locked`。

Windows（在 Windows x64 建置機上，用機器上已安裝的 MSVC 工具鏈 Rust，需要 1.90 以上；不安裝或修改工具鏈）：

```powershell
python scripts\build_settings_windows.py --version <版本> --test
```

輸出 `build\settings\windows-amd64\0c-settings.exe`（未簽章，約 3 MB）與 `NOTICES.txt`；`scripts/package_windows.py --settings` 把它放進套件、以版本資訊資源取代 Tauri 寫的那一份、與其他執行檔一起簽章。需要 `icons/icon.ico`（`tauri-build` 在 Windows 上要它產生資源檔）。

## 安全界線

- 前端只能呼叫 `build.rs` 列出的指令；Rust 端限制鍵值的字元（不接受以 `-` 開頭的值，免得被當成旗標），Go 端再驗證一次。
- 視窗的 `on_navigation` 只允許自己的本機頁面；登入與付款以後一律開系統瀏覽器，不在這個 WebView 內進行。
- 所有畫面文字以 `textContent` 組成，不使用 `innerHTML`；CSP 不允許 inline script 與 style。
- 匯出的狀態摘要把家目錄換成 `~`，不含輸入內容、個人詞庫或使用者名稱。

## 執行環境

- 桌面項目的檔名：GTK 在 Wayland 上向合成器宣告的 app id 是**程式名稱** `0c-settings`（用 `WAYLAND_DEBUG=client` 在 GNOME 50 實測 `xdg_toplevel.set_app_id("0c-settings")`），不是 Tauri 的 identifier；所以應用程式項目必須叫 `0c-settings.desktop`，GNOME 才會把視窗對到它，顯示正確的圖示與分組。

- Linux 需要 WebKitGTK 4.1（`libwebkit2gtk-4.1`）與 GTK 3；缺少時載入器會在程式開始前以 exit 127 結束，所以由 `0c-update open-settings` 在啟動前偵測並說明。
- macOS 用系統內建的 WKWebView，沒有額外相依，hardened runtime 不需要 entitlement（以 Developer ID 簽章並通過公證、在 macOS 26 執行驗證）。bundle ID 是 `com.biggo.0c.settings`，`LSMultipleInstancesProhibited` 讓重複啟動由 LaunchServices 把既有視窗帶到前面（單一實例外掛在這個情況下不會被觸發），`open` 的參數也到不了既有的行程。切頁改走 Darwin 通知（#125）：啟動時以 `notify_register_file_descriptor` 為每個頁面登記 `user.uid.<uid>.com.biggo.0c.settings.page.<頁面>`（名稱由 `updater::navigate_notification` 產生，與 Go 端 `settingsapp.NavigateNotification` 相同），`0c-update open-settings` 在 `open` 之後以 `notifyutil -p` 送出，收到就聚焦視窗並發出 `navigate` 事件（`src/navigate_mac.rs`）。`user.uid.<uid>.` 名稱空間只有同一個使用者能送，通知不帶資料，只認白名單內的頁面。
- Windows 用 WebView2 Runtime（Windows 11 與近年的 Windows 10 內建，Evergreen 版）；缺少時由 `0c-update open-settings` 在啟動前偵測並說明。WebView2 的使用者資料夾放在 `%LOCALAPPDATA%\Zuin\settings-webview`，不另外建立以 identifier 命名的目錄。找不到 `0c-update` 時依序試 `ZC_UPDATER`、同資料夾（版本目錄）、`%LOCALAPPDATA%\Zuin\bin`（穩定副本）。匯出的狀態摘要把家目錄換成 `~`，連同 JSON 跳脫後的反斜線與正斜線寫法一起換。
- 偵測到 NVIDIA 私有驅動（`/proc/driver/nvidia`）時自動設 `WEBKIT_DISABLE_DMABUF_RENDERER=1`，避開 Wayland 下 WebKitGTK 白畫面與 Error 71（Tauri 文件列為已知問題，不無條件關閉）。
- 單一實例：已開著就聚焦既有視窗並切到要求的頁面；Linux 沒有 D-Bus session 時這個機制不生效，兩個實例各自執行。在 Wayland 上合成器不准程式自己置頂（沒有 xdg-activation token，在 GNOME 50 實測 `set_focus` 不會把被蓋住的視窗拉到前面），所以第二次啟動改成開新視窗再關掉舊的，新視窗會正常出現在最前面。

更新頁提供檢查更新、立即下載、背景下載加速及立即安裝。下載顯示實際百分比與已收到／總大小，每秒重讀執行狀態；核對和安裝顯示處理中的進度條。總覽與關於頁分開標示已安裝版本、目前執行的引擎及設定視窗版本，Windows 新外殼已安裝但尚未重新登入時明確提示。後端命令及進度格式見 [JSON 契約實作](../../cmd/0c-update/contract.go)。

Windows 完整更新視窗驗收另有 `test/settings-update-test.ps1`：在沒有安裝的專用測試機，以隔離個人資料目錄、已簽章測試 HTTPS 索引和真套件驗證立即下載、共用並加速背景下載、手動安裝與重新登入前的版本提示。測試使用 e2etest 更新器；互動視窗以管理員執行，所以不包含原生 UAC 同意／取消對話框的真人驗證。

輸入設定的「左 Shift 切換中英」（`left-shift`，`settings.json` 的 `left_shift`）預設關閉，左 Shift 照系統原本的用途；開啟後單按左 Shift 與右 Shift 一樣切換注音／英文。外殼在單按時向引擎查目前載入的值（`health` 的 `left_shift`），套用（引擎重啟）後生效。

輸入設定的「按 Esc 清除整段組字」（`esc-clear`，`settings.json` 的 `esc_clear`）預設開啟，與多數注音輸入法相同。不論開關，Esc 都先清除還沒打完的注音；開啟時再按一次清除整段，關閉時保留已組好的字。引擎讀設定，套用（引擎重啟）後生效，外殼不必改。

輸入設定的「空白鍵選字」（`space-selects`，`settings.json` 的 `space_selects`）預設開啟：注音打完按空白開候選。關閉後注音打完按空白在游標處輸入空格，改用 ↓ 選字；注音打到一半時空白仍是一聲，候選開著時空白仍換頁（#107、#110）。中英混打開啟時本來就是如此，不受這個設定影響。引擎讀設定，套用（引擎重啟）後生效，外殼不必改。

輸入設定的「選字鍵」（`selection-keys`，`settings.json` 的 `selection_keys`）三選一：`1234567890`（預設，舊設定檔沒有欄位時也是）、`1234qweras`、`asdfghjkl;`，不開放自訂（#112）。候選窗依序用那一排鍵選第 1–10 個字，鍵標由引擎隨候選送給外殼；換成字母鍵時數字 1–0 仍照位置選字（舊外殼、自己畫候選窗的程式只顯示數字也能選）。沒有候選窗時這些鍵照舊是注音鍵。畫在「選字」頁、選字排列之後（#207）。引擎讀設定，套用（引擎重啟）後生效。

輸入設定的「修改字詞的位置」（`candidate-cursor`）可選「游標前方」／「游標後方」。macOS 未指定時採前方，Linux／Windows 採後方；舊設定缺少欄位時同樣使用平台預設，明確保存的 before／after 會保留。後方模式的游標在組字尾端時後面沒有字，改選結束在游標的詞（同前方模式），說明文字也註明這個例外。設定儲存在 `settings.json` 的 `candidate_cursor`，透過既有組字保護套用；不變更個人詞庫格式。

輸入設定的「Ctrl＋標點打全形」（`ctrl-punctuation`，`settings.json` 的 `ctrl_punctuation`）預設開啟，放在「鍵盤與符號」頁、鍵盤排列之後（#207）。開啟時注音模式按 Ctrl＋, . ' ; 與 Ctrl＋Shift＋/ 1 ; 9 0 打全形的 ，。、；？！：（）（#231）；說明寫出會占用 VS Code 的 Ctrl＋, 、Ctrl＋. 、Excel 的 Ctrl＋Shift＋; 9、Konsole 的 Ctrl＋Shift＋9 0 等快速鍵，要保留時可以關掉；Windows 另寫明系統預設把 Ctrl＋Shift＋0 留給切換輸入法、這一鍵打不出「）」。關閉後這些鍵同其他快捷鍵交給應用程式（#168）。引擎讀設定，套用（引擎重啟）後生效；外殼依 health 的 `ctrl_punctuation` 決定要不要送這些鍵。

輸入設定的「Caps Lock 開著時直接打英文」（`caps-lock-english`，`settings.json` 的 `caps_lock_english`）預設關閉，放在「中英切換」頁最後（#207），只有 Windows、Linux 有：macOS 開 Caps Lock 一律切到英文大寫（#84），`0c-update settings` 不列這一項，設定程式就不顯示。開啟時 Caps Lock 是暫時的英文模式（字母大寫、數字與符號半形，正在打的字先送出，關掉回到注音；先用 Shift 切到英文時只改大小寫）；關閉時注音模式不理 Caps Lock（#173、#168）。引擎讀設定，套用（引擎重啟）後生效；外殼依 health／open 的 `caps_lock_english` 決定要不要回報 Caps Lock 狀態。

輸入設定的「不打聲調也能選字」（`toneless`，`settings.json` 的 `toneless`）預設關閉，放在「選字」頁的選字項目最後、模糊音之前（#207 的頁面依 spec 排序，沒列在頁面清單裡的項目排在該頁最後）。開啟時聲調選填：ㄋㄧㄏㄠ 就是「你好」，有打聲調的字照聲調，組字中的 ' 用來切字；中英混打反灰並標「不打聲調時關閉」，已儲存的值不動。只在標準排列有效，倚天、許氏時這一項反灰（#150）。引擎讀設定，套用（引擎重啟）後生效，外殼不必改。

輸入設定的「輸出簡體字」（`output-simplified`，`settings.json` 的 `output_simplified`）預設關閉，在設定程式左側獨立的「輸出設定」頁（`--page output`）。開啟後組字和候選仍顯示繁體，送出時才轉成簡體字（OpenCC ver.1.4.2 的 tw2s 字典，編在 `0c-server` 裡，見 `internal/simplified/opencc`）；我的詞彙與我的片語也一起轉，英文、數字、標點與 Emoji 不變，學習照常記繁體（#118）。這一項可以即時切換（#169）：開關照樣執行 `0c-update set output-simplified on|off -apply -json`，0c-update 寫好設定後，引擎回報 `output_simplified_live=true` 就請它即時切換（`apply.state` 是 `applied`），不重啟引擎，組字中也立即生效；舊引擎照舊交接重啟。四個外殼的輸入法選單也有可以打勾的「輸出簡體字」，從選單切換時由引擎寫 `settings.json`，設定程式的開關下一次讀取（切回視窗或重新整理）就跟著變。

輸出設定頁的「用快捷鍵切換簡體字」（`simplified-hotkey`，`settings.json` 的 `simplified_hotkey`，#192）預設關閉，排在「輸出簡體字」下面；說明由 `0c-update settings` 依平台寫出快捷鍵（Windows／Linux Ctrl＋Shift＋F11、macOS ⌃⇧F11，提醒 F 鍵是媒體鍵時加按 Fn／fn）。開啟後四個外殼攔這個鍵，跟輸入法選單一樣即時切換「輸出簡體字」；這個開關本身照一般設定交接重啟引擎套用。

輸入設定的「選字後游標移到下一個字」（`advance-after-select`，`settings.json` 的 `advance_after_select`）預設關閉，選字後游標留在原處。開啟後從候選窗選好字（數字鍵、Enter、滑鼠都算），游標移到所選詞的後面，可以接著按 ↓ 改下一個字；修改游標前方字詞時往後移一個字（同新酷音的 autoShiftCur），游標在句尾時不動（#133）。符號、Emoji 與原樣注音本來就插入在游標處、游標停在它們後面。引擎讀設定，套用（引擎重啟）後生效，外殼不必改。
