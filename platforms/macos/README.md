# 靈犀注音（0c）macOS 輸入法

Swift／InputMethodKit 外殼，使用 App 內附的 Go `0c-server`，沿用 `bridge/ipc` 的 Unix socket 協定。需要 macOS 13 以上、Xcode Command Line Tools（或 Xcode）與 Go 1.26 以上。完整套件目前只提供 Apple Silicon（arm64）。

神經模型需要配對系統字典，runtime 需求見 [模型說明](../../models/README.md)。

## 建置

在此目錄執行：

```sh
make test
make DATA_DIR=/絕對路徑/資料包
```

資料包需含 `manifest.json`、`lexicon.bin`、`lm.bin` 與來源授權檔；引擎載入時會驗證 manifest 和雜湊。既有 Linux 資料包可以共用，App 會重新建置 macOS 引擎。新聞候選請使用完整配對資料，見桌面資料包說明。預設 `DATA_DIR` 是專案的 `build/data`，資料不存在時建置會失敗，不會偷偷使用測試詞庫。

剛 clone、沒有 bigram 資料時，可以先編譯 Git 內的基礎詞庫：

```sh
make data-basic
make DATA_DIR="$(pwd)/../../build/macos-basic-data"
```

此包沒有 bigram，也不是新聞校準版，選詞品質不能套用專案首頁的評測數字。`data-basic` 寫入獨立目錄，不覆蓋 `build/data`。

產物是 `build/0c.app`，含外殼、引擎和資料；加上 `NEURAL_DIR=<scripts/prepare_neural.py --platform macos-arm64 的輸出>` 會內附神經模型。開發建置使用本機 ad-hoc 簽章，不需要 Apple 開發者帳號；發行套件改用 Developer ID 簽章並經 Apple 公證（見下）。

### 完整套件

在 Apple Silicon 的 macOS 上：

```sh
python3 scripts/prepare_neural.py --platform macos-arm64 --models <Hugging Face 原檔目錄> \
  --dictionary <配對字典> --out build/neural-macos-arm64      # 也可在 Linux 產生後複製過來；模型見 models/models.lock.json
python3 scripts/package_macos.py --preview --version 0.1.0-dev.17 \
  --data <配對字典> --neural build/neural-macos-arm64 --release-docs <安裝包文件目錄>
```

輸出 `build/release/0c-<版本>-macos-arm64-preview.tar.gz` 與 `.sha256`，內含 `0c.app`、`0c-update`、說明文件、`release.json` 與 `SHA256SUMS`；打包時以套件內的 `0c-update verify` 核對。需要 Python 3.10 以上執行打包腳本（系統內建 3.9 不支援），一般使用者安裝時不需要 Python。

**簽章與公證：** 加上 `--sign-identity <Developer ID Application 憑證> --keychain <鑰匙圈> --notary-profile <notarytool 設定名稱>` 時，腳本以 Developer ID＋hardened runtime 由內而外重簽 App 內每個 Mach-O（含內附 Python，不需 entitlement）與 `0c-update`，一起送 Apple 公證，通過後把票據 staple 到 App 並以 Gatekeeper 核對，最後才計算雜湊。公開包（非 `--preview`）必須簽章並公證。

**設定程式：** `make app SETTINGS_APP=<目錄>/0c-settings.app` 把 `scripts/build_settings_macos.py` 建好的 App 放進 `0c.app/Contents/Helpers/0c-settings.app`（打包腳本的 `--settings` 會處理；沒有它時 App 不含設定程式，選單的「設定…」會說明沒有附設定程式）。它是輸入法 App 的一部分：安裝收據逐檔列出、安裝與回復一起換，`0c-update open-settings` 啟動前依收據核對。

**「我的詞彙…」（#125）：** 輸入法選單每 15 秒讀一次 `0c-update status -json`，`features.vocab` 為 true（0.2.0 起一律為 true）時在「設定…」下面加「我的詞彙…」，執行 `0c-update open-settings vocab`；和「設定…」一樣更新進行中也能按。設定程式宣告 `LSMultipleInstancesProhibited`，已經開著時 `open` 只把視窗帶到前面、參數到不了既有的行程，所以 `open-settings` 在 `open` 之後另送 Darwin 通知 `user.uid.<uid>.com.biggo.0c.settings.page.<頁面>`（`/usr/bin/notifyutil -p`），設定程式以 `notify_register_file_descriptor` 收到就切頁（`platforms/settings/src/navigate_mac.rs`）。選單動作的 sender 是 infoDictionary，改了選單接線一定要在真的輸入法裡點一次（`make test-update-ui` 只用人工選單）。

**「輸出簡體字」（#169）：** 輸入法選單在「我的詞彙…」下面加一項可以打勾的「輸出簡體字」。引擎在 open／health 回報 `output_simplified_live=true` 時勾選照引擎目前的值（open 回覆、每 5 秒的 health、每個快照的 `output_simplified`），點了在這個欄位的 session 送 `output_simplified` 請求：組字不動，之後送出的字（包括已經在組字區的）照新設定，引擎寫 `settings.json`，不重啟；游標旁的小提示（切換中／英用的同一個面板）顯示「已切換為輸出簡體字／繁體字」1.6 秒。舊引擎，或引擎還在啟動時，退回 `0c-update set output-simplified on|off -apply -notify`，勾選照 0c-update 回報的 `settings.output_simplified`；兩者都不知道時（0.2.1 以前的 0c-update、引擎沒有回報）不顯示。選單列的圖示不變：圖示來自 Info.plist 輸入模式的 `tsInputModeMenuIconFileKey`，IMKit 沒有在執行時換圖示的公開介面，要標出簡體只能另外宣告一個輸入模式（會在系統的輸入來源清單多一項，要使用者另外加入），這版不做，靠選單的勾選與切換提示。設定「用快捷鍵切換簡體字」（#192，預設關閉）開著時，⌃⇧F11（鍵碼 103，只按 Control 與 Shift，沒有 ⌘／⌥；F 鍵事件本來就帶 `.function`，鍵盤 F 鍵預設是媒體鍵時要加按 fn）走選單同一個動作（`simplifiedMenuAction`），組字不動、游標旁提示 1.6 秒、選單打勾跟著變。要不要攔看 open／health 的 `simplified_hotkey`（快取同 `ctrl_punctuation`：open 的回覆與切回 App 時的背景 health），設定關閉或舊引擎時照一般快捷鍵先送出組字再交給 App。單按 F11 是系統的「顯示桌面」，加了 ⌃⇧ 系統不攔，.12 實測輸入法收得到。

**設定啟動器：** 含設定程式的 App 另內附 `Contents/Helpers/0c-settings-launcher.app`。更新器把核對過的副本放到 `~/Applications/靈犀注音設定（0c）.app`（顯示「靈犀注音設定（0c）」），供 Spotlight 搜尋「0c」或「靈犀注音」；它只呼叫穩定更新器的 `open-settings` 後結束，不另佔 Dock。實際設定程式不搬家。安裝、背景更新、開設定及回復維護入口，移除或回復到沒有啟動器的版本時清除；只維護自己建立的 App。入口內容相同不重寫，副本保持原簽章與公證票據，另以 `LSRegisterURL` 登記。直接 `make install` 的開發者複製流程不建立收據或 Applications 入口，產品安裝請用 `0c-update install`。

啟動器的原生行為測試：`make test-settings-launcher`。已簽署產物的複製與簽章驗證：`ZC_TEST_SETTINGS_LAUNCHER=<內附啟動器.app> go test -v ./internal/settingsapp -run TestMacLauncherCopyPreservesNativeSignature`；加 `ZC_TEST_SETTINGS_LAUNCHER_NOTARIZED=1` 同時驗 staple 與 Gatekeeper。它們不安裝日常輸入法、不呼叫背景排程。

**選單列圖示：** `Icon/make_icon.swift` 產生 `Icon/0c.tiff`（22×16 pt 範本圖，鏤空「注」與「0c」），改圖後執行 `make icon`。

## 安裝與啟用

一般使用者用 `.pkg` 安裝檔：雙擊後依畫面安裝，只裝到目前使用者帳號、不需要系統管理員密碼。pkg 由 `scripts/package_macos_pkg.py` 從完整套件產生：不直接放檔案，而是把整個套件放在安裝腳本目錄，由 postinstall 以使用者身分執行套件內的 `0c-update install -bundle`，因此核對、備份、排程與輸入來源啟用都與下面的指令相同；輸出記在 `/var/log/install.log`。

用 `sudo installer -pkg … -target /`、MDM 或 Homebrew Cask 安裝時，postinstall 以 root 執行（雙擊或 `-target CurrentUserHomeDirectory` 才是使用者身分）。0.2.5 起 `0c-update` 以 root 執行時會改以使用者的身分重新執行同一個命令（`launchctl asuser` ＋ `sudo -u`，家目錄取自帳號資料庫、不信 `HOME`）：直接 `sudo 0c-update` 是下指令的人，安裝程式與 MDM 是螢幕前登入的使用者；沒有人登入、或 `HOME` 是另一個帳號的家目錄時不安裝、不碰任何家目錄。降權前先結束以 root 執行的靈犀注音程序，並把靈犀注音自己的路徑（`0c.app`、`Application Support/0c`、`Caches/0c`、排程 plist，含舊的 Zuin 名稱）中屬於 root 的項目改回使用者（不跟隨連結、不改有多個硬連結的檔案），所以 0.2.4 以前用 sudo 裝壞的帳號，再用 sudo 裝一次新版就會修好。一般帳號改不動 root 的目錄，使用者身分執行 install／apply／rollback／uninstall 時遇到這種檔案會直接說明要執行的 `sudo installer` 指令（#198）。

用終端機時，完整套件解開後執行（不需要 sudo）：

```sh
shasum -a 256 -c 0c-<版本>-macos-arm64-preview.tar.gz.sha256
tar -xzf 0c-<版本>-macos-arm64-preview.tar.gz && cd 0c-<版本>-macos-arm64-preview
./0c-update install -bundle . -channel internal-preview
```

`0c-update` 先以包內引擎載入配對字典，再逐檔核對 App 雜湊，暫存於 `~/Library/Application Support/0c`，才把 `~/Library/Input Methods/0c.app` 換成新版；原有的 App（含 0.1.0-dev.21 以前的 `Zuin.app`）移入同目錄的 `backups/`。原有的 App 確認是自己的安裝、但有檔案被改動或缺少（例如重新簽章過、防毒軟體動過）時照常更新並列出不符的檔案，不是靈犀注音的 App 或是連結時才拒絕。從 Zuin 名稱的舊版升級時，個人資料目錄 `Application Support/Zuin` 改名為 `0c` 並在原位置留下連結。安裝後請仍在執行的舊引擎在沒有組字時存檔退出，下一個按鍵由新版啟動。公證過的套件不受 Gatekeeper 阻擋；未公證的預覽包由 `0c-update` 核對雜湊後移除隔離標記。

輸入法 bundle ID（頂層輸入來源 ID）是 `com.biggo.inputmethod.0c`，實際選取的是它宣告的輸入模式 `com.biggo.inputmethod.0c.Bopomofo`（#87；dev.59 以前沒有宣告模式，系統當成不分模式的輸入法）。安裝器會登記 App、依序啟用父項與模式；登記成功但清單還找不到時，等待 0.5 秒、再等待 1.5 秒，各重查一次。啟用後在新的子行程回讀（TIS 在行程內快取狀態），約 3 秒一輪、最多約 15 秒，沒啟用就重試；目前選的是靈犀注音的舊來源時改選模式，使用者正在用別的輸入法時不切換。重查不重複要求允許；登記或啟用失敗會保留錯誤原因，並留待登入後補做。

模式的預設狀態是未啟用（`tsInputModeDefaultStateKey` 為 false）：設成 true 時，從沒有模式的版本升級後系統只把模式「視為」已啟用、不寫進 `com.apple.inputsources` 的 `AppleEnabledThirdPartyInputSources`，重新登入後選單列就沒有靈犀注音。從沒有模式的版本（dev.60 以前）升級時，系統會詢問一次「要允許「靈犀注音」啟用「靈犀注音」嗎？」。由背景自動更新安裝時，同一個登入工作階段內就會詢問；按「允許」後模式寫進啟用清單，但選單列與切換小窗裡靈犀注音那一列的名稱和圖示是空白，選了也打不出中文，**要登出再登入一次**才恢復（#130，在工作階段內重啟 TextInputMenuAgent 只救回名稱，仍不能打字）。從終端機（ssh）執行安裝時，工作階段內不會詢問，改由**重新登入後**的背景排程補做。兩種情況下，重新登入前目前選著的舊登記都仍可打字。**第一次安裝**若仍找不到，請登出再登入，再到「系統設定 → 鍵盤 → 文字輸入 → 編輯 → ＋ → 繁體中文」加入「靈犀注音」並允許。之後的更新不必重新加入；簽章身分（Team ID）改變時系統會撤銷原本的核准，安裝程式會主動觸發詢問並說明，請按「允許」。更新、回復與解除安裝後，0c-update 在引擎沒有組字、使用者停手時以 SIGTERM 結束舊的輸入法程序，再以 `open -g` 啟動新的 App，不必登出（#170）。不加 `-j`：隱藏啟動的 App 整個是隱藏狀態，候選窗排好版也不會上螢幕，空白鍵、↓ 叫不出選字窗（#194，0.2.3 的更新器如此啟動）；App 啟動完成與每次顯示候選窗前都會解除隱藏（不搶前景），舊更新器以 `-j` 啟動新版也照常顯示。結束舊程序後，已經連著它的 App 要等焦點改變（切換 App、解鎖、螢幕喚醒）才連上新版，在那之前按的第一個鍵會以英文字母交給 App（沒有先啟動新版時整批按鍵都會漏掉，#178），所以只有使用者自己安裝時立即交接；背景自動安裝與排程只在這個帳號的螢幕鎖定、螢幕保護程式、螢幕關閉或切換到別的使用者時（`ioreg` 的 IOConsoleUsers：`CGSSessionScreenIsLocked`、`kCGSSessionOnConsoleKey`）交接，使用中停手多久都不交接，舊程序搭新版引擎照常能打字。排程每 15 分鐘才喚起一次，鎖一下就解開的話碰不到喚起的那一刻（#195），所以 App 在螢幕鎖定、螢幕保護程式啟動、螢幕關閉或切換使用者時，若自己的執行檔已被更新換掉，就以 `launchctl kickstart gui/<uid>/com.biggo.0c.update` 請排程立即執行一次（`Sources/Handover.swift`，等 3 秒讓系統先標成已鎖定，同一段離開只叫一次），交接條件照舊由 0c-update 判斷。新版 App 收到 SIGTERM 時若有組字或剛按過鍵，會等到閒下來才結束。上面要重新登入的情況（輸入來源還沒登記好、簽章身分改變要按「允許」）不結束舊程序，留到重新登入；是否要重新登入只看安裝時記下的 `update/relogin-required`（跨過登入邊界就清掉），還沒啟用成功的輸入來源（`update/input-source`）由之後的排程補做，不會讓之後的每次更新都要求重新登入（#195）；沒啟用成功時每執行一次啟用就再跳一次系統詢問（按了「不允許」也一樣），所以同一個登入工作階段只試一次（安裝、回復當下那次算這個工作階段的；沒有紀錄的舊記號立即試），跨過登入邊界再試，連續 3 個登入工作階段失敗就停止，記進更新紀錄（`input_source_retry_stopped`）與狀態摘要（`input_source_pending`），紀錄在 `update/input-source-retry.json`。

背景項目接管保留新舊 App 的完整簽章（#213）。0.2.5 曾把備份裡的 LaunchAgent plist 改名，以阻止舊外殼把 SMAppService 登記拉回備份（#205），但這會破壞 App 的資源封印；更新器現在只還原既有的 `.inactive`，不再改動完整 App。舊選單每 15 秒執行目前安裝位置更新器的 `status`；需要交接時，由既有的單次 `schedule adopt` 在使用者離開、沒有組字且停手後請舊外殼退場，再以新版 App 接管排程並喚起一次。使用者關閉背景執行或移除排程時不重新啟用；安裝、回復交易尚未結束或仍需重新登入時繼續等待。已完成的安裝以 App 執行檔與收據身分記在 App 外的 `update/agent-shell`，同版重裝或回復也會重新判斷，正常的選單輪詢不反覆掃描程序。這個交接只處理已安裝的版本，不會下載或接受待安裝更新。

```sh
./0c-update rollback     # 換回上一個 App（含原型）；目前的 App 移入備份，可再次 rollback 撤回
./0c-update uninstall    # App 移入備份；個人詞庫、設定與備份保留
./0c-update settings     # 神經選字、補標點、前文設定；set 切換
```

開發時仍可用 `make install DATA_DIR=...` 做首次安裝（已有 `0c.app` 或 `Zuin.app` 時會停止）。

## 行為與儲存位置

- 標準、倚天 41 鍵或許氏注音排列（核心依 `keyboard-layout` 設定解讀，外殼把按鍵原樣交給核心）；啟用時要求 US 鍵盤配置。組字、游標、退格、標點、候選分頁／反白、數字選字、Enter／Esc 與 Tab 補全沿用核心。
- 候選面板支援滑鼠點選。補全顯示在面板中，接受前不放進應用程式的組字區；同音替換先顯示「原詞 → 新詞」。
- 候選面板與中／英提示採系統半透明毛玻璃、圓角及陰影，跟隨 macOS 亮／暗外觀；開啟「減少透明度」時改為實色背景，候選文字不降低透明度。右側顯示「常用」「關聯」或「個人常用」：依系統詞頻、相鄰詞的機率提升與有時間衰減的個人使用證據判定，非依排名或反白決定；無足夠證據時不標示，近音候選優先保留讀音附註。標籤不改變排序、翻頁或選字 ID。
- Home／End（鍵碼 115／119，筆電是 Fn＋←／→）、PageUp／PageDown（116／121）與 Shift+Tab（送 `BackTab`）交給核心：組字中 Home／End 移到組字區頭尾，↑、PageUp／PageDown、Shift+Tab 與沒有補全的 Tab 吃掉不動作；候選窗開著時 Home／End 到第一個／最後一個候選，Backspace／Delete 同 Esc 關窗；沒在組字時交還應用程式（#106）。Command＋←／→ 是 App 的快捷鍵，照下一條處理。
- 組字中加詞與候選窗反悔鍵（#123，0.2.0）：引擎在 open 回覆的 info 回報 `vocab_keys=true` 時，只按著 Shift（沒有 Command／Control／Option）的 ←／→ 送 `ShiftLeft`／`ShiftRight`、⌫ 送 `ShiftBackspace`、Fn＋⌫（鍵碼 117）送 `ShiftDelete`；舊引擎（或自訂詞彙不能修改、引擎沒回報）時照舊忽略 Shift，Shift＋←／→ 與 ←／→ 相同（組字中不會選到 App 的文字）。組字中 Shift＋←／→ 從游標標出範圍，Enter 把標出的字連同打的注音加入詞庫，Esc 取消標示，按其他鍵取消後照常處理。標示範圍用粗底線，App 當成「目前的文節」畫：文字編輯是亮的強調色底線（其他段變暗）、Safari 是深色粗底線、Chrome 是淡藍底色；底線顏色與背景色這幾個 App 都不採用，所以不帶。Mac 筆電沒有獨立的 Delete 鍵，所以候選窗的反悔鍵是 Shift＋⌫：反白的候選是自訂詞就刪除（設定程式可復原），是學到的詞就不再推薦；候選窗沒開時 Shift＋⌫ 就是 ⌫。核心的提示（「已加入詞庫：…」、反悔鍵的結果）畫在候選面板最上面，候選窗開著時也看得到；只有提示時面板停留約 3 秒或到下一個按鍵。
- Control＋標點（#168）：注音模式按 ⌃, ⌃. ⌃' ⌃; 與 ⌃⇧/ ⌃⇧1（只按著 Control，可加 Shift；依鍵碼判斷，輸入法啟用時鍵盤固定是美式排列）送 `Ctrl+,` 等鍵名，核心打全形的 ，。、；？！ 插進組字區，沒在組字時也一樣。引擎在 open／health 回報 `ctrl_punctuation=true`（設定「Ctrl＋標點打全形」，預設開）才送；英文模式、Caps Lock 開著、設定關閉或舊引擎時照下一條交還 App。引擎冷啟動時同其他按鍵排隊，重送後引擎沒處理就補送那個全形字。
- Command／Control／Option 快捷鍵及無法處理的按鍵，先結束既有組字，再交還應用程式。macOS 的「中英切換鍵」預設為「不用 Shift」，使用系統的輸入來源切換，避免誤觸後留在英文模式；想用 Shift 可在設定中選右／左／左右 Shift。已儲存的 `toggle_key` 與舊版 `left_shift=true` 選擇保留，缺少設定時採新預設。啟用後單按指定的 Shift 切換注音／英文（按住超過 0.3 秒才放開不算單按，時限取核心 health 的 `shift_tap_ms`）：保留已組好的字，英數與空白插入游標處，Enter 一次提交；切到英文時清掉未打完的注音（例如 ㄋㄧ 還沒按聲調），沒按其他鍵就立刻再按一次切回時放回；切換時游標下方短暫顯示「中」或「英」。哪一側 Shift 切換看設定「中英切換」（`toggle-key`：右／左右／左／不用 Shift（macOS 預設），#117），外殼依 health 的 `right_shift`、`left_shift` 決定，沒開的一側照舊交給系統；舊核心沒有 `right_shift` 時右 Shift 照舊切換、左 Shift 看 `left_shift`。Shift＋字母仍是大寫，系統沒有預設用到單按 Shift。Caps Lock 開啟時先送出組字並顯示「英」，之後字母輸出大寫英文，Shift＋字母也是大寫（同 macOS 系統的大寫鎖定）；關閉後回到注音並顯示「中」（#84）。Info.plist 宣告 `TICapsLockLanguageSwitchCapable`（#67），系統的「使用大寫鎖定鍵切換 ABC 及目前輸入方式」（預設開，NSGlobalDomain `TISRomanSwitchState`）因此作用在靈犀：短按由系統切換靈犀與 ABC（組字由 deactivateServer 送出），按住才開啟大寫鎖定並照上述輸出大寫；關掉該選項時 Caps Lock 就是大寫鎖定。dev.63 以前沒有宣告這個鍵，#84 誤以為該選項對第三方輸入法無效。Windows 與 Linux 不這樣做：注音模式不理 Caps Lock、照打注音，英文模式才打大寫（#168，同常見注音輸入法）。改 Info.plist 後系統要重新登記，可能要重新登入才生效（新的輸入法程序會讀到新的 Info.plist，但系統對輸入來源屬性的快取見上面 #130 的說明）。切到別的欄位或 App 時的模式由核心的 `mode-memory` 決定：macOS 預設 shared，跟著最後一次 Shift 切換（#115）；native 則每次切到新的輸入欄位或重新選用輸入法時回到開始時的模式。「開始時的中英模式」（`start-mode`：注音預設／英文，#160）由核心決定：每個新 session 從它開始（IMK 每次 activateServer 都重開 session），shared 時是引擎啟動（登入）後第一個共用的模式；外殼不必另外處理。
- 候選與提示的位置優先用 `firstRect`，高度不足 1 點時改用 `attributes(forCharacterIndex:lineHeightRectangle:)` 的行矩形：Chrome、Arc、VS Code 等 Chromium／Electron App 與 Warp 的 `firstRect` 是約 1e-314 的未初始化值，直接採用會把候選窗固定在主螢幕左下角（#67）。Chromium／Electron 把每個 flagsChanged 交給輸入法兩次，右 Shift 的重複按下不取消單按（#71）。
- 安全輸入：系統的 `IsSecureEventInputEnabled` 是全系統狀態，回報的 PID 也只是前景 App。密碼欄位系統本來就切到 ABC，不會把按鍵交給輸入法；只有客戶端是終端機（Terminal、iTerm2、Ghostty，見 `SecureInputPolicy`）且安全輸入開著時才放行，不送模型、不學習。背景 App 留著安全輸入時，一般 App 照常輸入（#71）。前景 App 開了安全輸入後退到背景卻沒關，系統本身會把該 App 的輸入來源鎖在 ABC，這不是輸入法能處理的。
- 每個 InputMethodKit controller 有獨立 session，失焦／切換輸入法提交組字並關閉連線。維持 InputMethodKit 的預設滑鼠行為，點到組字區外時提交。
- 引擎第一次輸入時自動啟動，所有連線結束後閒置約 300 秒退出（神經模型重新載入約數秒，期間以統計引擎服務）。IPC 每次交易上限 1 秒，訊框上限 1 MiB。
- 前文：提交後下一段組字前，以 `selectedRange`／`attributedSubstring` 核對游標（無選取）前正是剛提交的文字，才請引擎採用；讀到的文字只比對、不送出。`ZUIN_DOCUMENT_CONTEXT=0` 讓外殼完全不追蹤，使用者設定用 `0c-update set document-context off`。
- 引擎斷線時，保留已顯示的組字為一般文字並放行當前按鍵；下一鍵開新 session。第一版沒有 Fcitx5 的組字重播，避免對回應遺失的提交自動重試。

| 內容 | 路徑 |
|---|---|
| 程式與系統字典 | `~/Library/Input Methods/0c.app/Contents/` |
| 個人學習 | `~/Library/Application Support/0c/user.json`（舊位置 `Zuin` 為指向它的連結） |
| 設定、安裝收據與備份 | `~/Library/Application Support/0c/settings.json`、`install-receipt.json`、`backups/` |
| 設定程式 | `0c.app/Contents/Helpers/0c-settings.app`（選單「設定…」→ `0c-update open-settings`；`features.vocab` 為 true 時下面多「我的詞彙…」→ `open-settings vocab`） |
| 設定入口 | `~/Applications/靈犀注音設定（0c）.app` → `~/Library/Application Support/0c/bin/0c-update open-settings`；內附來源 `0c.app/Contents/Helpers/0c-settings-launcher.app` |
| AI 工具用的轉接（#126） | `0c.app/Contents/MacOS/0c-mcp`（只支援 stdio 的 AI 工具設定填這個路徑；`make app MCP=<執行檔>` 內附，`scripts/package_macos.py` 自動建置） |
| IPC socket | `~/Library/Caches/0c/engine.sock` |
| 自動更新排程 | `0c.app/Contents/Library/LaunchAgents/com.biggo.0c.update.plist`，執行 `0c.app/Contents/MacOS/0c-update auto`；以 SMAppService 註冊（`0c --update-agent register\|unregister\|status\|open-settings`），系統設定的背景項目顯示「靈犀注音」 |

個人詞庫不包在 App 裡，重建 App 不會覆蓋；引擎定期存檔及正常退出時存檔。本版未加入操作 trace 或完整桌面管理程式；設定與更新可從輸入法選單操作，完整更新驗收見 ime#13。密碼欄位是否由 macOS 正確切換回系統鍵盤，仍列在人工驗收項目。

## 驗證

在 macOS 執行 `go test ./...` 時，請把 `TMPDIR` 設為短且非連結的目錄（例如 `/private/tmp/...`）：預設 `/var/folders/...` 超過 unix socket 路徑上限，且 `/var` 是連結，安裝測試會依設計拒絕。

`make test` 以隔離暫存目錄與真實 Go server 驗證組字、UTF-16 游標、候選 ID、一般／同音 Tab 補全、提交／取消、欄位隔離、斷線重啟、鍵盤快捷鍵、分段 IPC 回應、超長／截斷訊框與逾時；不讀寫日常個人詞庫。

`make test-ui` 驗證候選面板排版、螢幕邊界、點選 ID 與不能取得主視窗／鍵盤焦點的設定，另檢查亮／暗外觀、標籤對齊與整列點選、近音附註及降低透明度的實色回退；有圖形桌面時短暫顯示面板，沒有螢幕時會標明改用模擬螢幕範圍。不切換目前輸入來源。早期 6 項面板檢查曾使用無螢幕模式；各次執行環境以報告為準，實際跨應用焦點行為仍在 ime#30 追蹤。

安裝後需在 TextEdit、Safari／Chrome 文字欄位、Terminal 各檢查：

1. `a86qi6`（麻婆）組字，Tab 補全、Enter 提交；空白組字時 Tab／Enter 交還應用程式；組字中沒有補全的 Tab、↑、PgDn 吃掉不動作。
2. `]`、↓ 開候選；方向鍵、翻頁、數字、滑鼠與 Esc；候選框跟隨游標且不搶走輸入焦點。
3. 中文前後有 emoji 時，左右移動、Home／End（Fn＋←／→）和退格仍正確；多螢幕與螢幕邊緣候選位置。
4. 組字中點擊其他欄位、切换 App／輸入來源、Command＋C／V／A；不可重複提交或跨欄位殘留。
5. 密碼欄位與 Secure Keyboard Entry 回到系統鍵盤；睡眠／喚醒及引擎意外結束後仍能繼續輸入。

平台依據：[Apple IMKServer](https://developer.apple.com/documentation/inputmethodkit/imkserver)、[IMKServerInput](https://developer.apple.com/documentation/inputmethodkit/imkserverinput)。候選位置使用 SDK `IMKTextInput` 所定義的「相對組字區 UTF-16 位置」，不是整份文件的位置。

### 選字位置

設定 → 選字 →「修改字詞的位置」可選游標前方／後方。macOS 預設前方：`他│好` 按 ↓ 修改「他」，詞尾按 ↓ 可重選整個詞；候選內 Tab 切換詞長時向左延伸或縮短。選字後游標位置保持不變。若習慣原有方式，可選「游標後方」，或執行 `0c-update set candidate-cursor after -apply`；改回前方使用 `before`。尚未儲存這項設定的既有安裝也採前方預設。

候選窗標示 `修改「他」 · Tab 切換詞長`，讓單字與整詞的替換範圍可見；多排上下鍵可跨頁並在頭尾切換詞長。
