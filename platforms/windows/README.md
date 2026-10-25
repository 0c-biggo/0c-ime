# 靈犀注音（0c）Windows 輸入法（TSF）

三個部分（cargo workspace）：

- `tsf/`：Rust 寫的 TSF text service 薄殼（cdylib），跑在每個應用程式裡；x64 一份、32 位元應用程式用的 i686 一份（版本目錄的 `x86\0c_tsf.dll`，見下方「32 位元應用程式」），只接 TSF 事件、經 named pipe 送協定給 Go 的 `0c-server`、依快照更新組字區；協定與 Fcitx 5、macOS 外殼相同（`bridge/ipc`）。
- `ui/`：候選窗程式 `0c-ui.exe`，跑在 Zuin 自己的行程，DLL 只送「畫什麼、畫在哪」的訊息。繪製本身是函式庫 `zuin_ui::candidate`，沙箱與沉浸式宿主（Windows 搜尋框）由 DLL 在宿主行程內用同一份繪製，見下方「候選窗」。
- `common/`：兩者共用的使用者 SID、管道名稱與訊息型別。

程式與目錄分工見 [專案範圍](../../REPOSITORIES.md)。

## 需要

- Windows 10 1803 以上（x64；新套件暫不支援 ARM64）。
- 建置：Rust stable（msvc，含 `i686-pc-windows-msvc` target）、VS 2022 Build Tools（C++ workload、Windows 11 SDK）、Go 1.26。

## 建置

```powershell
cd platforms\windows
cargo build --release                       # target\release\0c_tsf.dll、0c-ui.exe
$env:CARGO_TARGET_I686_PC_WINDOWS_MSVC_RUSTFLAGS = "-C target-feature=+crt-static"
cargo build --release --target i686-pc-windows-msvc -p zuin-tsf   # 32 位元應用程式用的 DLL
cd ..\..
go build -o platforms\windows\target\0c-server.exe .\cmd\0c-server
```

從 Linux 交叉編譯 server 也可以：`GOOS=windows GOARCH=amd64 go build -o build/windows/0c-server.exe ./cmd/0c-server`（沒有 cgo）。

## 套件安裝與更新

一般使用者直接雙擊 `*-setup.exe`，依精靈完成安裝或升級；解除安裝入口會出現在 Windows「已安裝的應用程式」。下列 ZIP 命令僅供開發驗證與進階操作。

先用 `scripts/package_windows.py` 建置完整 ZIP（須提供 `--release-docs`，見 [完整安裝包](../../README.md#完整安裝包)），解壓並核對雜湊。以同一位桌面使用者的系統管理員 PowerShell，在**解壓套件目錄**執行：

```powershell
.\install.ps1 -Channel internal-preview
```

`install.ps1` 現在委派給套件內的 `0c-update.exe`，不再接受原本的 `-DataDir`／`-Profile` 散檔安裝方式。開發包也使用 `python scripts/package_windows.py --preview --data <資料目錄> --version <版本>` 建立完整套件。

每版 DLL、server、UI 與字典放到 `%LOCALAPPDATA%\Zuin\versions\`，個人詞庫維持 `%LOCALAPPDATA%\Zuin\user.json`。0.1.0-dev.62 起 TSF 註冊固定指向 `%LOCALAPPDATA%\Zuin\tsf\0c_tsf.dll`（32 位元 `tsf\x86\`），是目前版本目錄的複本，防毒軟體的例外只要加一次（#121，換檔流程見下方「固定路徑」）。先驗證新包與字典，再切換 TSF 註冊；既有固定目錄開發版可保留原檔案遷移。安裝／更新不強制結束舊引擎，**完成後須登出再登入**。

註冊只在 `HKLM\SOFTWARE\Microsoft\CTF\TIP` 登記 TSF profile，Windows 不會自動把新輸入法加進使用者的語言清單：沒有加入的話 Win＋空白切換不到，「進階鍵盤設定 → 覆寫預設輸入法」也只列清單裡的輸入法（加上目前的覆寫值）。所以安裝後（`afterInstall`）`0c-update` 以 `input.dll` 的 `InstallLayoutOrTip("0404:{CLSID}{profile}", 0)` 把靈犀注音加入這個帳號的清單（#191）：排在「中文（台灣）」原有的鍵盤後面，清單沒有中文（台灣）時一併加入這個語言；不帶 `ILOT_DEFPROFILE`，不改預設輸入法與覆寫。每個帳號只加一次，成功後留下 `Zuin\update\input-list`，之後自己從清單移除的，更新不會再放回去；失敗只提示、下次再試。從 0.2.3 以前背景更新上來時，執行安裝的是舊版的穩定更新器（沒有這一步），由新版的登入工作 `0c-update engine` 與 `0c-ui` 每天的 `schedule repair` 補做。測試安裝（`ZC_NO_SCHEDULE`）不改清單。小狼毫的安裝程式同樣呼叫 `InstallLayoutOrTip`。

0.1.0-dev.15 以前的版本使用 `zuin_tsf.dll`、`zuin-server.exe`、`zuin-ui.exe`；新版為 `0c_tsf.dll`、`0c-server.exe`、`0c-ui.exe`。從舊版升級要用**新套件內附**的 `0c-update.exe`（`install.ps1` 與 EXE 安裝精靈都是如此）；舊版工具不認得新檔名，會在變更註冊前拒絕。升級時取消舊 DLL 的註冊，舊版目錄保留為回復目標；不再是回復目標的舊版目錄，會在之後的更新或回復時刪除，仍被應用程式載入的檔案則留到登出後的下一次更新。TSF CLSID 與 profile 不變，已加入的輸入法不必重新加入。

`.\install.ps1 -Rollback` 回復上一版（含回到 zuin-* 舊版）；個人詞庫預設保留，需要還原升級前的詞庫時改用 `0c-update.exe rollback -restore-user`（見升級工具說明）；`-Uninstall` 取消註冊，保留版本檔案、字典、備份與個人詞庫。指令選項可用 `0c-update.exe help` 查詢。新流程已通過 Windows 11 x64 實機驗收；dev.38 起已有簽章 EXE／ZIP，尚未提供 MSI／MSIX，乾淨機與其他應用驗收仍待完成。

## 執行方式

- 第一次按鍵時 DLL 連 `\\.\pipe\0c-<使用者 SID>`；找不到時先試舊版的 `\\.\pipe\zuin-<SID>`（升級後舊版 zuin-server 仍在執行時，由它繼續當個人詞庫唯一的寫入者），都沒有才啟動同目錄的 `0c-server.exe`。引擎常駐、不閒置結束：Windows 搜尋框等沙箱應用程式（AppContainer）裡的 DLL 不能啟動程式（`CreateProcess` 回 0x80073D07），只能連上已在執行的引擎。所以安裝時另建登入排程 `0c-engine-<SID>`，登入時立即以穩定副本執行 `0c-update engine`；設定程式套用設定後（引擎已交接退出或原本沒在跑），`0c-update` 也立刻觸發這個排程啟動引擎；從自動更新等低優先順序排程啟動引擎時同樣交給它，引擎才不會整個登入期間繼承低 I/O 與記憶體優先順序。關閉自動更新不影響這個排程，被刪掉時 `schedule repair`（`0c-ui` 每天執行）會補回；解除安裝時移除排程，並請引擎存檔後結束（仍在組字時留到登出）。啟用時試連 `\\.\pipe\0c-ui-<SID>`，沒有就啟動 `0c-ui.exe`（單一實例，named mutex），但不在宿主的 UI 執行緒上等它；第一次顯示候選時才連線，必要時等到啟動後 2 秒（#89）。
- 32 位元應用程式（#112）：32 位元行程只能載入 32 位元 DLL，讀的是 32 位元登錄視圖的 CLSID（`HKCR\WOW6432Node\CLSID`）。套件在版本目錄多放一份 i686 的 `x86\0c_tsf.dll`（C 執行階段靜態連結），引擎與候選窗仍只有 x64：32 位元 DLL 從 `x86\` 往上一層啟動同一份 `0c-server.exe`、`0c-ui.exe`，管道跨位元可用。TSF 的 profile 與類別在 `HKLM\SOFTWARE\Microsoft\CTF\TIP`，是兩個視圖共用的機碼（[Registry Keys Affected by WOW64](https://learn.microsoft.com/en-us/windows/win32/winprog64/shared-registry-keys)），只註冊一份。任一份 DLL 的 `DllRegisterServer` 都寫兩個視圖的 InprocServer32（檔案存在的才寫，沒有的刪掉舊值，並對 x86 那份補上沙箱讀取權限），`DllUnregisterServer` 兩個都刪；所以 `0c-update` 照舊只對 x64 DLL 執行 regsvr32，舊版更新器（dev.61 以前）安裝 dev.62 也會補上 32 位元的註冊，回復到沒有 x86 DLL 的舊版時 32 位元註冊一併清掉。`0c-update` 的 DLL 被移走時直接刪兩個視圖的機碼；`status -json -diagnostics` 另列 `registered_dll_x86`。其他輸入法做法相同：小狼毫（[weasel](https://github.com/rime/weasel)）、[PIME](https://github.com/EasyIME/PIME) 都各出一份 x86／x64 DLL 分別註冊。
- 固定路徑（#121）：ESET 等防毒軟體的例外清單只吃完整路徑，所以 InprocServer32 不再指向每版不同的版本目錄，而是 `Zuin\tsf\0c_tsf.dll` 與 `Zuin\tsf\x86\0c_tsf.dll`。版本目錄仍是正本；`internal/update`（`windows_fixed.go`）換檔：複製成 `.new`、fsync 後核對雜湊 → 舊檔改名成 `.old-<舊版本目錄名>`（名稱已被占用就加序號；上一次的 `.old` 可能還被 explorer 載著）→ `.new` 改名成正式檔名；改名失敗（防毒掃描）會重試，第一步失敗就維持舊版、整個更新照常還原，第二步失敗把 `.old` 改回來。刪得掉的 `.old-*` 隨即刪除，刪不掉的留到下次（不用 `MOVEFILE_DELAY_UNTIL_REBOOT`，那要系統管理員）。每次安裝、回復與 `schedule repair`（0c-ui 每天以一般權限執行）都依收據復原：停在任何一步、固定檔被刪或被改，都從版本目錄補回。不用 junction／符號連結：防毒看到的是解析後路徑，而且 RedirectionGuard 會讓部分行程不跟隨使用者建立的連結。固定路徑的 DLL 依安裝收據 `install-receipt.json` 的版本目錄啟動 `0c-server.exe`、`0c-ui.exe`；更新後仍被載入的舊 DLL（已改名成 `.old-*`）也會啟動目前版本的程式，和相容核心更新後的情況一樣。`tsf` 目錄建立時加上沙箱讀取權限並由檔案繼承，`.new` 一建立就有權限，改名後保留；不開放整個 `Zuin`（個人詞庫在那裡）。只有 dev.62 以後的 DLL 會從收據找程式，所以回復到更舊的版本時註冊該版本目錄並移除固定檔。舊版更新器（dev.61 以前）只對版本目錄的 DLL 執行 regsvr32：新版 DLL 的 `DllRegisterServer` 發現自己在 `versions\<版本>\` 時，先執行同目錄的 `0c-update deploy-tsf` 部署並註冊固定路徑（那次 regsvr32 從固定路徑執行），失敗才照舊註冊版本目錄、等下一次新版更新器再改。
- 升級過渡期：`0c-server` 同時服務 `0c-<SID>` 與舊的 `zuin-<SID>` 管道，`0c-ui` 同時服務 `0c-ui-<SID>` 與 `zuin-ui-<SID>`，讓升級當下仍載入的舊 DLL 也連得上新版；任一名稱已有 server 回應時，新 server 不啟動。兩者都有 10 秒的啟動速率限制：引擎死掉後會再啟動（不是每個宿主只一次），但不會每按一鍵就重啟一次。
- **引擎不可用時不吃按鍵**：上一次請求失敗後，`OnTestKeyDown` 先做一次快速連線嘗試，連不上就回報不處理，讓應用程式自己收鍵。這很重要：`OnTestKeyDown` 一旦說要吃，有些宿主就不再自己處理，之後 `OnKeyDown` 回報不吃也救不回來。實測引擎被砍掉後，按鍵在 0.7 秒內放行，下一次允許啟動時（10 秒後）自動接回，組字恢復正常。server 用 SID 命名管道、ACL 只給本人與 ALL APPLICATION PACKAGES／低完整性等級，別的使用者連不上（`cmd/0c-server/transport_windows.go`）。版本目錄在 `%LOCALAPPDATA%`，繼承的權限沒有開給沙箱，所以每次註冊 DLL（安裝、更新、回復）前，`0c-update` 對 `0c_tsf.dll` 加上 ALL APPLICATION PACKAGES 與 ALL RESTRICTED APPLICATION PACKAGES 的讀取／執行權限；少了它，搜尋框載不進 DLL，TSF 改用其他輸入法或直接打出英文。
- 每次 I/O 有 1 秒逾時（overlapped）：server 卡住時放棄連線、放行按鍵，不卡宿主。斷線後下一鍵重連、重開 session 並重播本次組字的請求（與 Fcitx 5 外殼相同）。
- 按鍵：帶 Ctrl／Alt／Win 的組合鍵交還應用程式（例外是下面的 Ctrl＋標點），組字中在 `OnTestKeyDown` 就先送出組字再交還（Ctrl+Enter、Ctrl+V 排在組字之後；單按修飾鍵本身不送出，#106）；沒在組字時只有可能產生可見 ASCII 的鍵會問核心，具名鍵（Enter、Esc、Backspace、方向鍵、Tab、空白）與數字鍵盤直接交還；數字鍵盤以 `KP` 加字元送核心，不當注音鍵（#75）；組字中轉得成協定按鍵的鍵都交給核心；F 鍵、Insert 等轉換不了的鍵在 `OnTestKeyDown` 就先送出組字、回報不吃（NumLock／ScrollLock 與 IME 控制鍵直接放行、不送出），英文模式的 ↓ 照核心的結果放行，`OnTestKeyDown` 與 `OnKeyDown` 對同一個鍵的答案一致（#106）；Shift+Tab 送 `BackTab`。組字中 Home／End 移到組字區頭尾，↑、PgUp、PgDn、Shift+Tab 與沒有補全的 Tab 由核心吃掉不動作，所以 `OnTestKeyDown` 說要吃的這些鍵 `OnKeyDown` 也一定吃，WPF 不會掉鍵；候選窗開著時 Home／End 到第一個／最後一個候選，Backspace／Del 同 Esc 關窗（#106）。單按 Shift 切換注／英，哪一側看設定「中英切換鍵」（`toggle-key`，Windows 預設左右 Shift，#206；外殼依 health 的 `left_shift`、`right_shift`）。0.2.4 以前每次存檔都會把當時的預設 right 寫進 `toggle_key`，所以新版引擎第一次啟動時把 right 換成 both 一次（`settings.AdoptBothShift`，記號 `update\toggle-key-both`；之後自己改回右 Shift 不再被換，新版引擎啟動前以 `0c-update set toggle-key|left-shift` 自己選的也不換）。Shift 組合鍵、按住超過 0.3 秒、按著 Shift 點滑鼠（Shift＋點選延伸選取、Shift＋拖曳；`keys::mouse_state` 比較 Shift 按下與放開時滑鼠鍵的 `GetKeyState` 翻轉位元與按下位元）都不切換，Windows 的 Alt＋Shift、Ctrl＋Shift 切換語言／鍵盤屬於組合鍵，不受影響；在第一次收到這次 Shift 放開時（一般是 `OnTestKeyUp`；宿主沒先問就直接 `OnKeyUp` 時在那裡）切換，放開本身跟按下一樣交給應用程式、不吃（同系統注音與新酷音；PuTTY 等 IMM32 程式不對放開呼叫 `TranslateMessage`，以前吃掉放開、等 `OnKeyUp` 才切換，在那裡切不了，#217）；切換保留已組好的字，可在同一段混合中英；切到英文時清掉未打完的注音，沒按其他鍵就立刻再按一次（或再點一次輸入指示器）切回時放回。中英模式每個程式（TSF 執行緒）各自記住；新開的程式從「開始時的中英模式」（`start-mode`，預設注音，#160）起：Activate 時引擎已在執行就以 health 的 `initial_mode` 決定中／英指示（不為此啟動引擎、查完不留連線），否則在第一個非修飾鍵前先查，`OnTestKeyDown` 攔不攔按鍵才會對；引擎重啟重開 session 時，記得的模式跟 `start_mode` 不同才送「設定模式」還原。`mode-memory=shared`（#115）時換欄位重新查共用的模式。Caps Lock 在注音模式不影響輸入（#168，同常見注音輸入法）：`keys::printable` 在 `ToUnicode` 前清掉 `VK_CAPITAL` 的鎖定位元，字母照沒開 Caps Lock 時轉換，Caps Lock 開著打 su3cl3 照樣是「你好」，Shift＋字母仍是大寫（沒在組字時交給應用程式，照系統的大小寫）；英文模式（含組字中切到英文的那段）照 Caps Lock 打大寫。`OnTestKeyDown` 與 `OnKeyDown` 走同一個函式，答案一致。#168 以前字母變大寫、數字與標點仍是注音（su3cl3 → SU＋組字 3CL3）。設定「Caps Lock 開著時直接打英文」（`caps-lock-english`，預設關閉，#173）開啟時，Caps Lock 是暫時的英文模式（同 macOS 的 #84）：每個按鍵（含 Caps Lock 鍵本身的按下與放開）先以 `GetKeyState(VK_CAPITAL)` 的鎖定位元跟快照的 `caps_lock` 比較，不同才送 `caps_lock` 請求，`OnTestKeyDown` 與 `OnKeyDown` 都先同步、第二次已相同不再送，所以兩者看到的模式一樣；開啟時核心先送出組字、切到英文，字母交給應用程式照系統打大寫（Shift＋字母是小寫），數字與符號半形，Ctrl＋標點、混打、英文補字都照英文模式；關掉回到原本的模式，先用 Shift 切到英文時只影響大小寫；Caps Lock 開著時單按 Shift 照常切換，之後關掉 Caps Lock 不再改模式。中／英指示跟著快照。外殼只在 health／open 回報 `caps_lock_english=true` 時送（health 在第一個按鍵前查、open 時更新，換欄位沿用；Caps Lock 鍵本身再以 health 確認一次，設定剛打開、引擎重啟後第一個字母就是英文），舊引擎或設定關閉時照上面的 #168 行為。換欄位、引擎重啟後，Caps Lock 帶來的英文以 `caps_lock` 還原（不當成這個程式切的英文），在別處關掉 Caps Lock 回來的第一個按鍵就回到注音。Win＋空白可換鍵盤。
- Ctrl＋標點（#168）：注音模式按 Ctrl＋, . ' ; 與 Ctrl＋Shift＋/ 1（只按著 Ctrl，可加 Shift；AltGr 是 Ctrl＋Alt，不算）送 `Ctrl+,`、`Ctrl+.`、`Ctrl+'`、`Ctrl+;`、`Ctrl+?`、`Ctrl+!`，核心打全形的 ，。、；？！ 插進組字區，沒在組字時也一樣。字元是清掉 Ctrl 後 `ToUnicode` 的結果，先看按鍵本身，是這六個鍵才以 health 查 `ctrl_punctuation`（引擎認得而且設定「Ctrl＋標點打全形」開著，預設開），所以 `OnTestKeyDown` 與 `OnKeyDown` 的答案一致，組字中也不先送出組字；英文模式、設定關閉、舊引擎時照一般組合鍵交還應用程式。
- 0.2.0 加詞與反悔鍵（#123）：引擎在 open 回覆的 info 回報 `vocab_keys=true` 時（跟著 session 快取，不每鍵查 health；套用設定時引擎重啟、重開 session 就更新），只按著 Shift（沒有 Ctrl／Alt／Win）的 ←、→、Delete、Backspace 改送 `ShiftLeft`、`ShiftRight`、`ShiftDelete`、`ShiftBackspace`；英文模式照舊送原本的鍵。核心保證這些鍵名沒有對應功能時就等於原本的鍵（候選窗開著時的 Shift＋←／→、沒有一般候選窗時的反悔鍵），所以外殼不追蹤候選窗狀態，組字中 `OnTestKeyDown` 與 `OnKeyDown` 的答案也和原本的鍵相同；沒在組字時照舊直接交還應用程式（選取文字）。舊引擎不認得新鍵名：沒有回報就不送，重連到不支援的引擎時 session 也會把新鍵名換回原本的鍵。組字中 Shift＋←／→ 從游標標出範圍（樣式 `marked`），Enter 加入詞庫、Esc 取消、其他鍵取消後照常處理；候選窗開著時 Shift＋Delete（或 Shift＋Backspace）刪除反白的自訂詞或忘記學到的詞，候選重開。結果以提示顯示（見候選窗）。
- 輸入指示器選單（右鍵）：第一項「設定…」執行 `0c-update open-settings`；`0c-update status -json` 的 `features.vocab` 為 true（0.2.0 起一律為 true）時，下面多一項「我的詞彙…」（字典圖示），執行 `open-settings vocab`（#125）。舊版 0c-update 沒有這個欄位就不顯示。狀態由 `0c-ui` 讀取並快取 15 秒，開選單時用快取、背景重讀，所以這個欄位改變後（例如回復到舊版）下一次開選單（最多約 15 秒）才出現或消失；選取時依「上次交給系統的那份選單」找項目，項目數改變也不會選錯。`0c-ui` 只替 DLL 執行白名單內的命令，`open-settings` 只准不帶頁面或帶 `vocab`。
- 「輸出簡體字」（#169）：`0c-update status -json` 有 `settings.output_simplified`（0.2.1 起）時，選單在「依前文選字」下面多一項可以打勾的「輸出簡體字」。引擎在 health／open 回報 `output_simplified_live=true` 時，DLL 直接在這個程式的 session 送 `output_simplified` 請求，走打字用的同一條引擎管道（不經 `0c-ui`、不啟動程式，搜尋框等沙箱宿主也能用）：組字不動，之後送出的字（包括已經在組字區、還沒送出的）照新的設定，引擎把值寫進 `settings.json`；快照的提示「已切換為輸出簡體字／繁體字」照一般提示顯示在游標旁、4 秒後收起。引擎不能即時切換（舊引擎）或沒有回應時，改請 `0c-ui` 執行 `0c-update set output-simplified on|off -apply -notify`（白名單只多這兩個命令；0c-update 寫設定、交接重啟引擎、送通知）。勾選照引擎目前的值（health／open 的 `output_simplified`、之後每個快照的 `output_simplified`），不用 `0c-ui` 快取 15 秒的狀態；舊引擎時照 `settings.output_simplified`。輸入指示器在中文模式而且輸出簡體時畫「简」取代「中」，滑鼠停留的說明是「靈犀注音（中・簡體輸出）」；英文模式維持「英」。換欄位（`OnSetFocus`）時以 health 重新確認（引擎沒在執行時不為此啟動、查完不留連線），在設定程式或別的程式切換後，回到這個程式就跟上；開輸入指示器選單時也以 health 再確認一次（.14 實測：少了這一步時，同一個程式沒打字、沒換焦點，勾選和「简」會停在舊值）。這個請求不記進斷線重播紀錄。設定「用快捷鍵切換簡體字」（#192，預設關閉）開著時，Ctrl＋Shift＋F11（只按 Ctrl 與 Shift，沒有 Alt／Win）走同一條路：`OnTestKeyDown` 先看按鍵本身，是這個鍵才以 health 確認 `simplified_hotkey` 與 `output_simplified_live`（設定套用、引擎重啟後第一次按就照新設定），說要吃、不先送出組字；`OnKeyDown` 切換（方向照 health 剛回報的目前值），組字不動、提示照舊、工作列「中」↔「简」；key-up 一併吃掉。設定關閉、舊引擎或引擎不可用時不吃，照一般快捷鍵交給應用程式（組字中先送出組字，#106）。沒用 `ITfKeystrokeMgr::PreserveKey`：註冊了就不分設定開關一律被 TSF 攔走，設定改了還要重新註冊。
- 組字區：一個 TSF composition，文字＝快照的 preedit，游標＝快照的 UTF-16 位置，依 UTF-16 範圍套用分段樣式：一般細實線、locked 粗實線、corrected 點線，沿用宿主文字／背景色；marked（#123 加詞的標示範圍）用系統選取色（`COLOR_HIGHLIGHT`／`COLOR_HIGHLIGHTTEXT`，查詢時才讀、以 COLORREF 提供）反白，加上選取色的粗底線：記事本等會畫背景的宿主反白，只畫底線的宿主也和已選字的黑色粗線分得出來。提交＝把 composition 文字換成 commit 再結束 composition。
- 候選窗：每次快照後 DLL 在 edit session 內以 `ITfContextView::GetTextExt` 取游標的螢幕矩形，連同候選、反白、頁碼、Tab 建議、核心的提示（快照的 `notices`，例如「已加入詞庫：世傑」，以一般字級、強調色畫在最上面一列，提示優先：有提示時取代候選標題，下一個快照標題就回來；沒有候選時單獨顯示，4 秒後或下一鍵沒有提示時收起；Shift＋←／→ 標示加詞範圍時（組字區有 marked）核心每個快照都帶的說明不計時，留到標示結束，`UiMessage.marking` 由 DLL 依組字區樣式設定，0c-ui 與宿主行程內候選窗共用同一判斷；UI-less 宿主不另外顯示）送給 `0c-ui`（只寫、200 ms 逾時，送不出去只是看不到候選窗）。`0c-ui` 是不搶焦點的 topmost 視窗（GDI 繪製、per-monitor DPI v2），放在游標下方、放不下就放上方、不超出工作區；沒有候選與建議時隱藏。ui 不向 server 訂閱快照，而是由 DLL 直接送呈現訊息（少一個協定改動、少一跳延遲）；滑鼠點選回傳候選 ID 與畫面版本，由原宿主執行緒確認焦點、欄位與候選仍有效後選取；提示與頁碼不視為候選。
- 候選窗的位置拿不到時（#144）：Chromium 系宿主（Edge、Chrome、Electron 程式、WebView2 程式如新版 Teams／Outlook）的版面由 renderer 非同步送回，剛打的字還沒排版時 `GetTextExt` 回 `TS_E_NOLAYOUT`，排好後才以 `ITfTextLayoutSink::OnLayoutChange` 通知；這些宿主也沒有系統插入點。有候選窗時 DLL 在該 context 上註冊 text layout sink（沒有候選就取消），`OnLayoutChange` 時以唯讀 edit session 重查同一個游標位置，位置變了或之前沒有位置才補送（同一個畫面版本最多 8 次，避免宿主一查就再通知而停不下來；UI-less 宿主不處理）。重送時拿掉 0c-ui 已自己收起的提示（`NOTICE_MS` 到時），只剩提示就不再重送；`edit::apply` 進行中（宿主在寫入時或剛寫完就通知，或在同一個呼叫堆疊裡執行排隊的唯讀 session）收到的通知先記下，套用完這一鍵、位置不是準確的才補查，不拿上一鍵的畫面重送。組字中每一鍵都查游標矩形（只查剛放好的插入點一次），沒有組字時只在這一鍵要顯示候選或提示時才查選取；查到就記下。按鍵當下拿不到位置時依序退到：輸入框視窗執行緒與前景執行緒的系統插入點（有插入點的程式照 #144 之前的行為）、同一次組字上一次查到的位置（Chromium 組字第一鍵還沒有組字文字，回得出插入點；之後幾鍵 `TS_E_NOLAYOUT` 時沿用，差一兩個字寬）、這個視窗上一次查到的位置（之前的組字，而且還在目前的輸入框範圍裡）、`GetScreenExt`（Chromium 回傳有焦點的輸入元素範圍並裁到可見區域，拿不到才回整個客戶區）或 view 視窗的客戶區——不高於三行就整塊當游標（候選窗在輸入框下方），更高的（多行輸入框、比可見區域高的網頁編輯器、客戶區）只取第一行，否則候選窗會被推到下緣以下或翻到上緣以上；候選窗先出現在游標附近，版面回來再移到游標下方。每種「拿不到」與退到哪裡（以失敗類別去重，不含座標與 HWND；第一次的紀錄含座標與 view 的 HWND），以及收到 OnLayoutChange 後第一次補上位置，在 tsf.log 各記一次（`候選窗：…`），不每鍵洗版；`tsf-debug` 開著時每鍵記查到的位置與最後的座標、來源。
- 跨行程的宿主視窗（#144）：WebView2 的最上層視窗屬於宿主程式（例如 `ms-teams.exe`），有鍵盤焦點的子視窗與 TSF 在 `msedgewebview2.exe`。`0c-ui` 顯示候選窗、處理滑鼠點選，以及 DLL 核對滑鼠選字時，原本只認「前景視窗屬於送出者」，WebView2 的候選窗因此永遠不顯示、也點不了；現在前景視窗屬於送出者，或前景執行緒的鍵盤焦點視窗屬於送出者（跨行程父子視窗共用輸入狀態，`GetGUIThreadInfo` 的 `hwndFocus`）都算（`zuin_ui::candidate::foreground_belongs_to`）。背景程式晚到的訊息兩者都不符合，照舊不顯示。
- 沙箱與沉浸式宿主的候選窗：宿主是 AppContainer，或宿主視窗的 z 順序層級（user32 `GetWindowBand`）高於一般桌面（例如 Windows 搜尋框的 SearchHost 在層級 6）時，`0c-ui` 的視窗會被宿主介面蓋住，沙箱裡也啟動不了 `0c-ui`。此時 DLL 在宿主行程內、以 context 的宿主視窗（搜尋框是 CoreWindow）為 owner 建立候選窗，和 `0c-ui` 共用 `zuin_ui::candidate` 的繪製、主題與命中；owned popup 會落在宿主同一層級，顯示在搜尋面板上方。候選窗只在前景視窗屬於本行程，或是候選窗 owner 的頂層視窗時顯示：檔案總管的搜尋框（22H2 的 FESearchHost 沙箱）是嵌在 explorer.exe 視窗裡的 CoreWindow，前景視窗屬於 explorer，系統把 owner 換成外層的 `CabinetWClass`（#105）。wndproc 以 `catch_unwind` 攔 panic；service 結束時銷毀視窗。**平台限制：** 搜尋面板這一層的滑鼠輸入不會送到非系統元件的視窗（候選窗收不到 hit test，點擊會穿到下層並讓搜尋框失焦），所以搜尋框裡用數字鍵、方向鍵、空白選字，不能用滑鼠點選；改成 CoreWindow 的子視窗則完全不顯示。
- 敏感欄位：每次送鍵前核對 TSF 的停用／空 context compartment、唯讀狀態及 InputScope。密碼、PIN、secure mode 不送鍵到引擎、不記錄鍵碼；清除候選與待重播組字。只標 `IS_PRIVATE` 的欄位（Chrome 無痕／訪客、Firefox 私密視窗的所有欄位）照常組字，但每個請求帶 `private`：引擎不學習、不補標點、不做神經重排，外殼也不讀前文；同時帶密碼／PIN 時仍繞過。部分原生 RichEdit（含記事本）未提供 InputScope 而回傳 E_FAIL／E_NOTIMPL 時，只在核對 context、焦點、程序及已知原生控制項類別後，改查密碼遮罩、唯讀與停用狀態。其他讀不到 InputScope 的自訂欄位——IMM32 程式（系統相容層的暫時 context，`TF_SS_TRANSITORY`）、自行繪製候選的宿主（UI-less，#86），以及自己實作 TSF 文字儲存、不支援任何文字屬性的程式（Unreal Engine 遊戲的 `UnrealWindow`，WARDOGS 聊天框，#209）——焦點、程序相符且不是密碼／唯讀的原生編輯框、MSAA 也沒標成保護、唯讀或停用時，以私人欄位組字（不學習、不讀前文）。Windows App SDK（WinUI 3）XAML island 的文字框（23H2 起檔案總管的網址列與搜尋框、相片等）沒設 InputScope 時同樣回 E_FAIL，焦點視窗是 `InputSiteWindowClass`、MSAA 只有空的 CLIENT；焦點與程序相符時照常組字，宿主開了觸控鍵盤預測或自動校正（`TS_SS_TKB*`）視為一般欄位，沒開的以私人欄位組字（#105）；PasswordBox 的預設 InputScope 是 Password，仍由 InputScope 擋下。OneNote 桌面版的頁面 `OneNote::DocumentCanvas` 同樣回 E_FAIL，而且在 OneNote 行程內查不到 MSAA（AccessibleObjectFromWindow 回 E_FAIL），焦點與程序相符時以已知文件畫布照常組字（#163）。其他無法確認的欄位（焦點不在這個 context、MSAA 標成保護等）放行給宿主，不讀取文件文字；降級成私人組字或放行時，tsf.log 每個行程對同樣的情況記一次「欄位政策：…」（宿主執行檔、HRESULT、context 旗標、焦點與 view 的視窗類別，不含文字與鍵碼），不必開 `tsf-debug`（#209）。系統的輸入法開關熱鍵 Ctrl＋Space 不送按鍵、不讀文件，只有確定擋下的欄位（停用、唯讀、密碼）不切換；Word、Outlook 收到熱鍵當下不給同步唯讀鎖，讀不到欄位政策時照常切換（#164）。
- UI-less 宿主（#86）：遊戲等自行繪製候選的程式（例如英雄聯盟遊戲內聊天、SDL2 程式）以 `TF_TMAE_UIELEMENTENABLEDONLY` 啟用，只載入註冊了 `GUID_TFCAT_TIPCAP_UIELEMENTENABLED` 的輸入法。候選清單以 `ITfCandidateListUIElement` 提供（只含目前這一頁）；宿主在 `BeginUIElement` 回 pbShow=FALSE 時不顯示外殼的候選窗，一般程式照常顯示。新類別隨每次更新以新版 DLL 重新註冊，升級即生效。
- COM-less 啟用（#157）：GUI 執行緒的 COM 不是 STA（例如以 MTA 初始化）時，系統以 `TF_TMAE_COMLESS` 啟用輸入法，只載入註冊了 `GUID_TFCAT_TIPCAP_COMLESS` 的輸入法；沒註冊時工作列仍顯示「中」，按鍵卻直接以英文送出（Minecraft Java 版）。DLL 註冊這個類別；啟用時不呼叫 `CoCreateInstance`，顯示屬性的 GUID 以 msctf 的 `TF_CreateCategoryMgr` 註冊（`msctf.rs`，不分是不是 COM-less 啟用）。
- 換欄位（ITfThreadMgrEventSink::OnSetFocus）與停用時送 `commit`；應用程式自行終止組字（滑鼠點到別處）時送 `cancel`，不學習。例外是宿主把組字搬到新的焦點文件（#189）：Excel 的儲存格還沒進入編輯時，第一鍵在試算表的文件開始組字，Excel 隨即把焦點移到 NULL、建立儲存格編輯器，再把焦點連同組字交給編輯器的文件。新的焦點文件就是組字範圍所在的文件時繼續組字，不送出、不重開 session；焦點移到 NULL 時先不送出，以回呼視窗的 WM_TIMER（佇列空了才產生，宿主接連的焦點變化都處理完才輪到）再確認一次，焦點沒回到組字所在的文件才照舊送出到原文件。以前第一次焦點變化就把第一鍵的注音送出，「第一次」變成「ㄉ億一次」（0.2.1 的混打是「2億一次」）。TSF 範例輸入法與 libIME（PIME、新酷音）都不因換文件結束組字；小狼毫曾在 OnSetFocus 結束組字，在 Excel 與檔案總管網址列造成同樣的第一鍵問題後撤回（rime/weasel#1178、#1182）。
- 每個 COM 入口以 `catch_unwind` 攔 panic：四個 DLL 匯出用 `guard`，`#[implement]` 產生的介面方法（`OnKeyDown`、`DoEditSession`、`Activate`、`CreateInstance`…）一律用 `guarded` 包住方法本體。windows crate 的 vtable thunk 不會自己攔，漏掉一個就等於讓 panic 終止宿主應用程式。`profile.release.panic = "unwind"` 不可改成 abort。
- `DllCanUnloadNow` 依活著的 COM 物件數回答：`TextService`、`ClassFactory`、edit session 與顯示屬性物件都在建構時加一、`Drop` 時減一，加上 `LockServer`。漏算任何一種，DLL 可能在還有物件存活時被卸載。輸入指示器向 `0c-ui` 查狀態的背景執行緒另外持有一份模組參考（`GetModuleHandleExW`），以 `FreeLibraryAndExitThread` 結束，執行緒收尾時 DLL 不會被卸載（#89）。
- 提交不能丟：`TF_ES_SYNC` 的 edit session 在按鍵回呼之外可能被拒（失焦、停用時）。此時若快照有提交文字，改排 `TF_ES_ASYNCDONTCARE` 的 edit session 由它寫入並結束組字；只是更新組字則保留原本的組字物件，下一鍵再同步。提交文字一旦寫進文件就不再重排：之後放游標、結束組字失敗只記錄，宿主跑完 edit session 卻回報失敗也照成功處理，否則同一段字會在新組字裡再插入一次（#88）。SetText 本身回報失敗時，以同一個 edit cookie 讀回組字範圍：內容就是提交文字才算已寫入，讀不回或不相符就照未寫入改排非同步，提交不會遺失。
- 診斷紀錄：`%LOCALAPPDATA%\Zuin\tsf.log`（目錄存在才寫；超過 4 MiB 重來），只記事件與錯誤，不記輸入的文字。每次啟用記一行開始、一行完成（含宿主執行檔名稱）；panic 只記檔案與行號，不記訊息（訊息可能帶出輸入的字，#89）；edit session 失敗記步驟與 HRESULT。
- 除錯紀錄：另外建立空檔 `%LOCALAPPDATA%\Zuin\tsf-debug` 後，同一個檔會多記每個按鍵的虛擬鍵碼與快照摘要。**這等於記下輸入內容，只在排錯時開，用完刪掉那個檔。**

候選窗跟隨 Windows 應用程式亮／暗主題，收到設定變更立即重繪；高對比模式採系統文字、背景與選取色。Emoji 多排及單欄退回都使用 DirectWrite／Direct2D 彩色字型，高對比與繪圖不可用時保留系統色／GDI 退回。DPI 改變後重新排版最新快照，避免舊候選重新出現。

## 還沒做（依優先順序）

1. 擴大真實應用驗收：其他 Word 版本、其他瀏覽器、其他 UWP／沙箱應用程式、全螢幕、高 DPI、多螢幕；WPF、原生 EDIT、Edge、記事本、Windows Terminal 與 Windows 11 系統搜尋框（組字）已通過。
2. 完整操作 trace；更多應用程式的敏感欄位驗收。
3. 其他安裝格式（MSI／MSIX）、ARM64。

## 測試

- 新套件先執行 `test\package-test.ps1 -BundleDir <解壓目錄>`，檢查 Go、Rust 與獨立管道。它不安裝、不使用日常詞庫，參數見該測試腳本。

- `cargo test --release`：`session.rs` 對真的 `0c-server` 做協定整合測試（打字／提交、放行未處理鍵、斷線重播）。先在另一個管道啟動 server，再設 `ZUIN_TEST_PIPE`；沒有設定就略過，不讀日常個人詞庫：

  ```powershell
  $pipe = "\\.\pipe\0c-test"
  Start-Process .\target\0c-server.exe -ArgumentList "-data",".\data","-user",".\test-user.json","-socket",$pipe
  $env:ZUIN_TEST_PIPE = $pipe; cargo test --release
  ```

- `test\desktop-test.ps1`：桌面驗收。要在**已登入的互動工作階段**執行（沒有人坐在機器前時，可用 `Register-ScheduledTask -Principal (New-ScheduledTaskPrincipal -UserId <使用者> -LogonType Interactive)` 從 SSH 觸發）。它開一個 WPF 文字框、SendKeys 打字，每步把文字框內容記到 `job.out`、截圖到 `shot-NN.png`。前提是 Zuin 已是目前輸入法：`Set-WinUserLanguageList` 加入 `0404:{138C35D0-7F08-4CEF-BA90-C34C16301528}{EF62F743-9855-4C94-A658-1F0F98B7B840}`，再 `Set-WinDefaultInputMethodOverride` 或以 `ITfInputProcessorProfileMgr::ActivateProfile`（profile type 1）切換。這是舊版手動驗收腳本，會產生個人學習；只能搭配使用暫存 `-user` 的隔離引擎，不要對日常詞庫執行，也不要為清理測試而刪除日常 `user.json`。建議改用下列新版腳本。

新版驗收工具可一次完成註冊、七組測試與還原。在已登入的測試機，以同一位使用者的系統管理員 PowerShell 執行：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\platforms\windows\test\acceptance-test.ps1 -BundleDir C:\path\to\unpacked-bundle
```

只需指定套件目錄。預設執行 WPF、原生 EDIT、32 位元 WPF 與原生 EDIT（`wpf32`、`native32`：同一份測試在 SysWOW64 的 32 位元 PowerShell 裡跑，宿主載入 `x86\0c_tsf.dll`，並核對 32 位元登錄視圖指向本次套件，#112）、Edge、WebView2、記事本、Windows Terminal、候選窗主題、Windows 搜尋框與無痕視窗測試，結果自動放在 `build/windows/validation/acceptance-*`；也可用 `-Suite terminal` 選一組。已安裝 Word 的測試機可另跑 `-Suite word`，已安裝 OneNote 桌面版的可另跑 `-Suite onenote`；兩者都不列入預設組。工具會拒絕覆蓋既有結果目錄、同時執行另一輪測試，或在日常引擎／候選窗仍執行時開始；不會替使用者強制停止它們。每組測試有獨立的暫存個人詞庫，使用互動排程工作，完成後清除該工作與測試引擎。

任一組失敗即停止後續測試，回傳非零結束碼；`finally` 還原原註冊與收據，並核對個人詞庫及舊字典 manifest 雜湊。報告會分別列出測試與還原是否成功；逾時時須檢查遺留的測試視窗後再重跑。此流程不代替 EXE 安裝精靈、乾淨機或登出登入驗收。

新版互動測試腳本（明確的測試機操作，不列入一般建置）：

- `test/smoke-test.ps1`：空白欄位開 Emoji、`==` 輸入等號、Tab 切顏文字、WPF 組字、Tab 補全及閒置放行、中英切換、混合組字、滑鼠候選、PasswordBox 與回到普通欄位。
- `test/candidate-test.ps1`：直接檢查候選窗與彩色 Emoji 的亮／暗主題像素、工作區邊界與跨宿主隱藏訊息；暫時切換 AppsUseLightTheme，完成後還原原值與型別，總控工具逾時時亦會還原。高對比模式的系統色與縮放／負座標由 Rust 測試覆蓋，尚非實際高對比／多螢幕驗收。
- `test/migration-test.ps1 -LegacyBundleDir <zuin-* 舊版解壓目錄> -BundleDir <0c-* 新版解壓目錄> -ResultDir <新目錄>`：以舊版工具安裝舊版、確認舊工具安全拒絕新包、以新工具升級（舊 DLL 取消註冊、舊版目錄保留、TSF profile 仍在）、以隔離管道核對新 server 同時服務新舊名稱且不與執行中的舊 server 並存，再回復到舊版並回到新版；`finally` 還原原註冊與收據並核對個人詞庫。執行前須結束日常引擎與候選窗。
- `test/settings-test.ps1 -Setup <新版EXE>`（#47 設定程式）：在**沒有任何安裝**的測試機以靜默模式安裝新版，驗證收據記錄的設定程式雜湊、開始功能表「靈犀注音設定（0c）」（以 conhost --headless 執行穩定的 0c-update、圖示檔與標記檔）、`0c-update open-settings`（只有一個行程）、從捷徑與 TSF 輸入指示器選單的第一項「設定…」（經 menu-probe，由 `0c-ui` 轉給 0c-update）啟動、選單第二項是「我的詞彙…」且選了開到「我的詞彙」頁（#125）、設定程式被改一個位元組時拒絕並說明，再解除安裝並確認捷徑與標記檔一併移除；視窗以 PrintWindow 只擷取設定程式自己的視窗。測試前備份更新狀態檔，結束時逐位元組還原，新增的版本目錄、備份與穩定副本一併移除並回報是否完全還原。已有 EXE 安裝、收據或引擎指標時拒絕執行。
- `test/installer-test.ps1 -Setup <新版EXE> -BaselineSetup <舊版EXE>`：舊版靜默安裝、新版互動精靈升級、已登錄的解除安裝入口與還原核對；與桌面測試共用互斥鎖，不覆蓋既有 EXE 安裝或收據。
- `test/word-test.ps1`：拒絕接手既有 Word，開啟自己的空白文件；先確認原生英文字可輸入，再驗證中文組字／提交／取消、連續提交、`==`、Ctrl＋Space 切英文再切回（#164）與表格輸入，核對載入 DLL及唯讀文件沒有引擎輸入，測後不存檔並退出自己的 Word。需先開啟一次 Word 完成首次使用提示，且 Word 必須能編輯文件；只安裝而未完成必要啟用時不能當作輸入法驗收。
- `test/excel-test.ps1`（`-Suite excel`，不在預設組，#189）：拒絕接手既有 Excel，以 COM 開自己的空白活頁簿；在還沒進入編輯的儲存格直接打「第一次」（2u4u h4）、「安安」（0 0 ）與「你好」，以及 F2 進入編輯後打「你好」，按 Enter 送出、再按 Enter 離開儲存格後讀值，核對載入本次 DLL；每格在送出前截圖。測後不存檔並退出自己的 Excel。需要 Excel 桌面版（.14 是 Office LTSC 2024 以 ODT 加裝的 Excel）。
- `test/onenote-test.ps1`（#163）：拒絕接手既有 OneNote，以 OneNote 的 COM 介面在結果目錄建立自己的本機筆記本與新頁面，驗證頁面標題組字提交、右 Shift 與 Ctrl＋Space 切英文再切回、載入本次 DLL，並記下頁面視窗的類別與 MSAA 角色／狀態；測後關閉筆記本並退出 OneNote。需先開啟一次 OneNote 關掉首次使用的登入視窗（不必登入）。
- `test/native-test.ps1`：原生 EDIT，以及私人／密碼／PIN InputScope；私人欄位要能組字提交且不讀前文，密碼／PIN 欄位沒有鍵碼、快照或新 session 紀錄。
- `test/browser-test.ps1`：以全新 Edge 設定目錄開啟本機測試頁，驗證組字、提交、取消、候選窗看得到、候選與密碼框；不操作日常瀏覽器頁面。
- `test/webview2-test.ps1`（#144）：測試行程的 WinForms 視窗裡以 WebView2 Core API 建 controller（和新版 Teams 相同的視窗結構：前景視窗屬於測試行程，鍵盤焦點與 TSF 在另一個 `msedgewebview2.exe`），對工作階段切到靈犀注音，驗證組字、提交、取消、候選窗看得到且在輸入框附近、↓＋Enter 與滑鼠選字、載入本次 DLL；截圖 `webview2-candidate.png`，並把該行程在 tsf.log 的紀錄存成 `webview2-tsf.log`。WebView2 SDK 第一次執行時從 NuGet 下載固定版本（`Microsoft.Web.WebView2` 1.0.4129.50，核對 SHA-256）到 `build/windows/webview2-sdk`；WebView2 執行階段用系統內建的。離線下載不到 SDK 或沒有 WebView2 執行階段時，報告標 `skipped` 與原因，驗收總表列為 skipped、不算失敗（雜湊不符仍是失敗）。使用者資料目錄放在結果目錄，不碰日常 WebView2 程式的資料。
- `test/private-test.ps1`：Chrome 無痕、Chrome 訪客與 Firefox 私密視窗（欄位帶 `IS_PRIVATE`，#68、#70），以專用設定目錄開本機頁，驗證組字、選字、右 Shift 與密碼欄繞過，並核對私密視窗選的字沒有進個人詞庫；最後用一般 Chrome 視窗對照學習有效。沒有安裝 Firefox 時略過該瀏覽器。
- `test/terminal-test.ps1`：以唯一標題開啟專用 Windows Terminal 視窗，只讀取輸入文字而不執行；核對中文提交與換行分離、取消、滑鼠候選、右 Shift、混合組字、連續快速輸入及載入 DLL。
- `test/search-test.ps1`：Windows 11 系統搜尋框（SearchHost，AppContainer＋沉浸式層級）。對工作階段切到靈犀注音、重新啟動 SearchHost、Win＋S 開搜尋框，以 UI Automation 讀搜尋框文字，核對 SearchHost 載入本次 DLL、組出「你好」、候選窗建在宿主層級（沙箱 tsf.log 記錄層級 > 1）、按 3 選第三個候選；截圖候選窗與選字後畫面。為了取得沙箱 tsf.log，測試期間在 `%LOCALAPPDATA%\Packages\MicrosoftWindows.Client.CBS_cw5n1h2txyewy\AC\Zuin` 建目錄，結束時移除；最後按 Esc 關閉搜尋。
- `test/explorer-test.ps1`（`-Suite explorer`，不在預設組，#105）：在暫存目錄開自己的檔案總管視窗，網址列（Ctrl+L）、搜尋框（Ctrl+E）與新增資料夾的重新命名欄位各打 `su3cl3`、↓、1、Enter，核對按鍵進了組字、候選窗看得到（0c-ui 或宿主行程內）、送出中文（網址列只在確定組字中才按 Enter，送出後 Esc 還原；資料夾名稱以檔案系統核對）。檔案總管與工作列同一個 explorer.exe，tsf.log 寫在真正的 `%LOCALAPPDATA%\Zuin`，只取本次測試行程的紀錄；測試期間暫時建立 `tsf-debug`。
- `test/winui-test.ps1`（`-Suite winui`，不在預設組，#105）：Windows App SDK（WinUI 3）文字框，以「相片」的搜尋框代表 23H2 起檔案總管的 XAML 網址列與搜尋框；拒絕接手已開啟的相片，核對焦點在 `InputSiteWindowClass`、組字、選字與送出，結束時關閉相片。
- `test/notepad-test.ps1`：在沒有記事本程序時啟動專用暫存文件，驗證提交／取消後的存檔、右 Shift、滑鼠選字、快速連續輸入、原生密碼遮罩放行與無按鍵紀錄，並核對載入 DLL。

這些腳本都接受 `-ResultDir <新的暫存目錄> -ExpectedDLL <本次版本 DLL>`，需在互動工作階段執行。執行前由測試操作者暫時註冊該版本，先啟動配對引擎並以 `-user` 指向該次測試的暫存詞庫；每組測試使用不同的個人詞庫。測後還原原 TSF 註冊與安裝收據，停止自己啟動的引擎／候選窗，確認原個人詞庫雜湊不變。腳本拒絕在自己的視窗失去焦點後送鍵；不要將其當成日常桌面的無人值守測試。

欄位處理依據：[Microsoft InputScope](https://learn.microsoft.com/en-us/windows/win32/api/inputscope/ne-inputscope-inputscope)、[TSF compartments](https://learn.microsoft.com/en-us/windows/win32/tsf/predefined-compartments)。密碼欄位應由宿主停用文字服務，InputScope 額外防護不取代宿主責任。

原生控制項替代檢查依據：[ITfReadOnlyProperty::GetValue](https://learn.microsoft.com/en-us/windows/win32/api/msctf/nf-msctf-itfreadonlyproperty-getvalue)、[EM_GETPASSWORDCHAR](https://learn.microsoft.com/en-us/windows/win32/controls/em-getpasswordchar)。後者只取得遮罩字元，不讀取欄位文字。

Windows Terminal 的獨立視窗參數依據：[Microsoft 命令列說明](https://learn.microsoft.com/en-us/windows/terminal/command-line-arguments)。測試不修改 Terminal 設定或全系統預設輸入法。
