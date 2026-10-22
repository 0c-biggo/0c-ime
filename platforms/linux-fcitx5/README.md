# 靈犀注音（0c）：Linux Fcitx5

這個 addon 載入在 fcitx5 行程裡，只做按鍵轉換與顯示；所有輸入法行為都在行程外的 `0c-server`（Go）。addon 第一次需要時自動啟動 server；server 當掉時，下一個按鍵會自動重連並重播這次組字的請求，組字不會遺失；server 無法使用時按鍵直接放行，不會卡住應用程式。

## 建置與安裝（全部裝在家目錄，不寫入系統目錄）

發行版的系統安裝（`/usr`，由套件管理員更新）用 repo 根目錄的 `make install PREFIX=/usr`，見 [Linux 系統安裝](../linux-system/README.md)。外殼找引擎與 `0c-update` 的規則在 `src/locate.h`：先找家目錄的官網安裝，再找系統安裝。

已安裝後，在**專案根目錄**執行 `make upgrade` 即可更新；已拉取程式或要安裝本機修改時使用 `make upgrade PULL=0`。流程會先測試建置、以獨立引擎確認現有字典能載入，再備份安裝並只重啟需要換版的引擎／外殼。需要 Python 3.9 以上；字典與個人詞庫保留，不執行 `install-data`。詳見 更新流程。

新版套件安裝／升級及 `make upgrade` 會關閉經典介面與 Kimpanel 的 `PreferTextIcon`，直接顯示自有「注／英」圖示。這是 Fcitx 面板共用的偏好，其他輸入法也會使用圖示；其他介面設定與註解保留，原檔會備份。套件安裝失敗會自動還原；套件版本回復撤回圖示偏好，保留之後另行修改的主題或圖示選擇。GNOME Kimpanel 的文字標籤會遮住圖示，所以靈犀注音不再提供重複的文字標籤；切換提示顯示「靈犀（注音）」或「靈犀（英文）」，狀態列圖示顯示「注／英」。舊套件缺少自有圖示時不調整這項偏好。

GNOME／Yaru 會替 Kimpanel 的狀態圖示再加左右留白。完整套件與 `make upgrade` 會在使用者已安裝的 `kimpanel@kde.org/indicator.js` 加入限定「注／英」的修正：保留圖示大小及配色，去掉圖示的 padding／margin；切到其他輸入法時恢復原有樣式。只處理 `~/.local/share/gnome-shell/extensions/` 中已知的 `_setIcon(property)` 接線（已驗證 Kimpanel 92／GNOME 50），不安裝延伸套件或改系統主題，未知接線保持原檔。修改會備份，安裝失敗、使用新版安裝工具的完整套件回復／降版與移除可撤回，保留後來的其他程式碼修改。已執行中的 GNOME 會快取 JavaScript，重新登入後套用；延伸套件自行更新後可能需要重新執行完整套件安裝或 `make upgrade`。`make install` 不修改延伸套件。

兩種安裝方式共用 `~/.local/share/0c/gnome/kimpanel-spacing.js` 片段及 `kimpanel-spacing.json` 雜湊紀錄，讓完整套件安裝器能辨認並撤回 `make upgrade` 加入的修正；`make upgrade` 不改寫完整套件收據。回復降版時，若接線仍相容，會補回原修正區塊並保留其他修改；已更換接線或另行修改修正區塊則保留原檔。

```sh
sudo apt install libfcitx5core-dev libfcitx5utils-dev libfcitx5config-dev   # 只有這一步需要 sudo
make test           # 以真的 0c-server 測試用戶端（不需要上面的套件）
make                # 建出 build/lib0c.so
make install        # 0c-server → ~/.local/bin；addon → ~/.local/lib/fcitx5、~/.local/share/fcitx5
make install-data   # 桌面預算版資料包 → ~/.local/share/zuin/data（需要先依 data/README 準備資料）
```

`make install` 把 addon 設定的 `Library` 寫成 `~/.local/lib/fcitx5/lib0c` 的絕對路徑，Fcitx 5 直接載入，不需要 `FCITX_ADDON_DIRS`（D-Bus 或 KWin 啟動的 Fcitx 5 拿不到登入環境的變數；各發行版的系統 addon 目錄也不同，見 #92、#93）。舊版 `make install` 寫入、內容未改的 `~/.config/environment.d/90-0c.conf` 一併刪除。它同時刪除 0.1.0-dev.15 以前的舊檔名（`zuin-server`、`zuin-trace`、`libzuin.so`、`addon/zuin.conf`、`90-zuin.conf`）；`inputmethod/zuin.conf` 的檔名是 Fcitx 的輸入法 ID，沿用不改，已加入的輸入法不必重新加入。重新啟動 Fcitx 5 後生效（會中斷目前的輸入法幾秒；KDE Plasma Wayland 改用「系統設定 → 鍵盤 → 虛擬鍵盤」先選「無」再選回「Fcitx 5」），之後在 Fcitx 5 設定中把「靈犀注音」加入輸入法清單。

```sh
fcitx5 -r -d
```

也可以不開設定視窗，直接用 D-Bus 加進目前的群組（先讀出現有的清單，把 `('zuin', '')` 接在後面寫回去；群組名稱與鍵盤配置以讀到的為準）：

```sh
gdbus call --session --dest org.fcitx.Fcitx5 --object-path /controller --method org.fcitx.Fcitx.Controller1.InputMethodGroupInfo "預設"
gdbus call --session --dest org.fcitx.Fcitx5 --object-path /controller --method org.fcitx.Fcitx.Controller1.SetInputMethodGroupInfo \
  "預設" "cn" "[('keyboard-cn', ''), ('chewing', ''), ('zuin', '')]"
```

**更新之後要重啟什麼。** 只換資料包或只改了核心（`0c-server`）：`make install`／`make install-data` 之後 `pkill -x 0c-server`，下一個按鍵會自動啟動新版，正在組的字由外殼重播，學習資料在收到 SIGTERM 時存檔。改了 `lib0c.so`（`src/` 底下的 C++）才需要重啟 fcitx5。`make install-data` 會從 `data/` 重新編譯，需要 `data/lm/bigram/`（見 [詞庫資料](../../data/README.md)）。

個人資料存在 `~/.local/share/zuin/user.json`；server 的 socket 在 `$XDG_RUNTIME_DIR/0c/0c.sock`（權限 0600）；過渡期同時監聽舊版的 `$XDG_RUNTIME_DIR/zuin/zuin.sock`，舊外殼也連得上。外殼連不上新 socket 時會先改連舊 socket（舊版 zuin-server 仍在執行時由它繼續寫入個人詞庫），都連不上才啟動 `0c-server`。移除：`make uninstall`。

設定檔優先使用 `~/.local/share/0c/settings.json`；新檔不存在時讀取舊 `~/.local/share/zuin/settings.json`，下次修改會寫入新路徑並保留舊檔。引擎重啟後載入新設定；自訂 `-user` 目錄時仍使用個人詞庫旁的設定。

目前新聞候選須使用配對的 server＋資料包更新；`git pull` 不會更新桌面字典，`make install-data` 也不會重現該候選。下載／轉移、備份與更新操作見 桌面更新與字典資料包。

## 自選的本機診斷紀錄

新版外殼預設不記錄詳細輸入內容。只有以 `ZUIN_TRACE=1` 啟動 Fcitx5，才會記錄操作、前後組字／候選／補全及耗時；僅設定 `ZUIN_TRACE_DIR` 不會啟用。保存在 `~/.local/state/zuin/trace/`（或 `$XDG_STATE_HOME/zuin/trace/`），每檔 16 MiB、共 4 檔，**最多 64 MiB**，滿了自動淘汰最舊紀錄；只有本人可讀寫，不上傳。

```sh
0c-trace                       # 最近 15 分鐘統計，不顯示文字
0c-trace -last 10m -details     # 看具體操作與當時文字
```

詳細紀錄包含正常中文輸入內容。Fcitx 標示為密碼／敏感的欄位不錄製也不學習；英文模式的一般文字鍵只記類別；英數進入預輸入框後，該次混合組字直到清空為止停錄，避免文字從快照洩漏。`pause:on` 亦停錄，尚未清空的敏感組字不會在恢復後洩漏到紀錄。是否為敏感欄位依應用程式提供的標記判定。

以 `ZUIN_TRACE=0` 啟動 Fcitx 可停用，`ZUIN_TRACE_DIR` 可改位置。停用後可刪除上述 trace 目錄中的紀錄。寫入由背景執行緒處理。安裝新外殼後需要重啟 Fcitx，已執行中的舊版不會套用新的預設值。

## 按鍵

按鍵的意義全部由核心決定，外殼只轉送按鍵、照快照畫（含候選窗的反白位置）。帶 Ctrl／Alt／Super 的組合鍵交還應用程式；組字中先送出組字再交還，Ctrl+Enter、Ctrl+V 才會在組字之後執行（單按修飾鍵本身不送出，#106）。例外是注音模式的 Ctrl＋標點（#168）：只按著 Ctrl（可加 Shift）的 , . ' ; ? ! 在引擎回報 `ctrl_punctuation=true`（設定「Ctrl＋標點打全形」，預設開）時送 `Ctrl+,` 等鍵名，核心打全形的 ，。、；？！ 插進組字區；引擎沒處理就照一般組合鍵。Ctrl+; 是 Fcitx5 剪貼簿外掛的預設熱鍵；剪貼簿在輸入法之後（`PostInputMethod`）才看按鍵，外殼吃掉就叫不出剪貼簿，所以剪貼簿外掛載入時外殼照舊放行 Ctrl+;，不打；（沒載入剪貼簿時才打；）。外殼不去改使用者的 Fcitx5 設定。注音模式不理 Caps Lock（#168）：字母照 `rawKey` 的 Shift 換回不受 Caps Lock 影響的大小寫，照打注音，Shift＋字母仍是大寫；英文模式照 Caps Lock 打大寫。設定「Caps Lock 開著時直接打英文」（`caps-lock-english`，預設關閉，#173）開啟時，Caps Lock 是暫時的英文模式：每個按鍵依 `rawKey` 帶的鎖定狀態（Caps Lock 鍵本身帶的是按下前的狀態，反過來算；不讀 LED，Wayland 讀不到）跟快照的 `caps_lock` 比較，不同才送 `caps_lock` 請求，核心開啟時先送出組字、切到英文，關閉時回到原本的模式，注／英圖示跟著快照，Caps Lock 鍵切換時也跳出輸入法資訊提示；猜錯時下一個按鍵改正。外殼只在 open 回覆 `caps_lock_english=true` 時送；Caps Lock 鍵本身不為此啟動 0c-server，並以 health 再確認一次（設定剛打開、引擎重啟後 open 的快取是舊的，否則第一個字母會照注音打），`caps_lock` 不寫進重播紀錄。

| 情境 | 按鍵 | 作用 |
|---|---|---|
| 組字中 | 注音鍵、聲調（一聲是空白） | 輸入；完成的音節若沒有任何字可讀（按錯鍵），不收進組字，符號留著讓你用退格改 |
| | ↓ 或空白（沒有未完成的符號時） | 在游標位置開候選，由最長的詞開始；設定「空白鍵選字」關閉或開了中英混打時，空白改在游標處輸入空格，只用 ↓ 開候選 |
| | ← → | 移動游標 |
| | Home／End | 移到組字區最前／最後；注音打到一半時同 ← → 不移動。沒在組字時照常交給應用程式 |
| | Tab | 接受提示列的補全（「⇥ 豆腐」）；沒有一般建議時可要求「小龍 → 小籠包」同音預覽，再 Tab 接受；沒有合格補全時吃掉不動作 |
| | ↑、PageUp／PageDown、Shift+Tab | 吃掉不動作，組字留著、應用程式游標不動（以上各鍵沒在組字時照常交給應用程式） |
| 同音替換預覽中 | Enter／Esc | Enter 提交目前組字、不接受預覽；Esc 關閉預覽、保留組字 |
| | Enter／Esc／Backspace | 提交／放棄組字／刪除 |
| | Shift＋標點鍵、`[`／`]`、`\`（頓號） | 全形標點留在組字區；可移動、退格，Enter 才提交 |
| | 符號位置按 ↓／空白 | 重選同組符號，例如「→『、」→』；選完仍留在組字區 |
| | 未完成注音按 ↓ | 可選原樣注音，例如 ㄡ；第一次 Enter 選取，第二次 Enter 提交 |
| | 反引號（鍵盤左上角） | 開啟符號選單，含標點、括號、引號及注音；可翻頁、數字選取 |
| | 單按左 Shift | 預設依 Fcitx5 快捷鍵設定切換系統鍵盤與 Zuin；「中英切換鍵」選「左右 Shift」或「左 Shift」時同右 Shift（見下） |
| | 單按右 Shift | 「中英切換鍵」是預設的「右 Shift」或「左右 Shift」時切換 Zuin 內部注音／英文；保留已組好的字，英數插入框內游標處。切到英文時清掉未打完的注音（例如 ㄋㄧ 還沒按聲調），沒按其他鍵就立刻再按一次切回時放回。選「左 Shift」或「不用」時右 Shift 照系統原本的用途交給應用程式。按住超過 0.3 秒、或中間按了別的鍵（Shift＋字母打大寫）都不算單按 |
| | Shift＋← →（0.2.0 開發中，見下） | 從游標標示一段字（反白），最多 32 字；Enter 把畫面上這段字連同你打的注音加入詞庫，提示列顯示「已加入詞庫：…」；Esc 取消標示；按其他鍵取消標示並照常處理。注音打到一半時先打完。沒在組字時照常交給應用程式（選取文字） |
| 候選窗開啟 | ↑ ↓ | 移動反白；走過這個詞長的最後一項就接到較短的詞長，往上則回到較長的，頭尾相接 |
| | ← →、空白、PageUp／PageDown | 換頁 |
| | Home／End | 到第一個／最後一個候選 |
| | Backspace／Del | 同 Esc，關閉候選窗、組字保留 |
| | Enter | 選反白的那一項 |
| | 1–9、0 | 直接選這一頁的第 n 項 |
| | Esc | 關閉候選窗，組字保留 |
| | Shift＋Delete、Shift＋Backspace（0.2.0 開發中，見下） | 反白的是自訂詞就刪除（可在設定程式復原），是學到的詞就不再推薦；提示列說明做了什麼，候選窗重開 |

標「0.2.0」的按鍵只在引擎回報 `vocab_keys`（0.2.0 起的引擎在自訂詞彙可以修改時回報）時，外殼才把只按著 Shift 的 ← →、Delete、Backspace 送成新鍵名（`ShiftLeft`、`ShiftRight`、`ShiftDelete`、`ShiftBackspace`，#123）；這個值在 session 開啟時取得，引擎重啟後重開 session 時更新，不必每個按鍵查詢。新鍵名在沒有對應功能時（候選窗開著時的 Shift＋← →、沒有候選窗時的 Shift＋Delete）由核心當原本的鍵處理；舊引擎不認得新鍵名，所以沒回報時照舊送 ← →、Delete、Backspace，行為與以前相同。提示列有通知（剛做的動作的回饋，例如「已從詞庫刪除：…」）時優先於候選標題，標題在下一個按鍵回來。

候選窗開著的時候其他按鍵一律吞掉，不會同時被當成注音；候選窗一關，數字鍵就變回注音鍵（1＝ㄅ、2＝ㄉ…）。

已轉為漢字的一音節候選，最後也附上原樣注音。符號／原樣注音選入後游標移到它的後方，可繼續打字；它們不會學成個人詞彙，兩側中文的學習不跨越符號。中段改符號時先把游標移到符號前，再按 ↓；組字末尾則直接按 ↓。例如 `]` → ↓ → `2` 得到留在組字區的「』」，再按 Enter 才送出。設計與回歸案例見 符號組字。

## 目前的限制

- 排列由核心決定（標準、倚天 41 鍵或許氏，`keyboard-layout`，#83、#183）；外殼把所有可見按鍵交給核心，不另外判斷注音鍵。左 Shift 依 Fcitx5 設定切換輸入來源；中英切換鍵依設定交給引擎。重新啟用 Zuin 會恢復注音；服務重連時，有未提交組字則還原當時的中英模式，無組字則恢復注音。Shift＋標點仍輸入全形標點。
- 「中英切換鍵」（`0c-update set toggle-key right|both|left|none -apply`，預設 `right`，#117；取代舊的 `left-shift`，舊設定 `left-shift on` 讀成 `both`）：外殼每次確定是單按 Shift 的放開時向引擎查詢 health 的 `right_shift`、`shift_tap_ms`（左 Shift 在按下時查 `left_shift`），設定套用後下一次按 Shift 就生效；舊引擎沒有 `right_shift` 時右 Shift 照舊切換，沒有 `shift_tap_ms` 時門檻 300 ms。按下到放開超過門檻（預設 0.3 秒）不算切換，按著 Shift 猶豫後放開不會誤切；中間按了別的鍵也不算。右 Shift 不在切換鍵裡時（`left`、`none`）按下與放開都交給應用程式。
- 左 Shift：Fcitx5 預設把左 Shift 設為「暫時切換輸入法」（全域選項 `AltTriggerKeys`），框架會在輸入法之前攔下。切換鍵含左 Shift（`both`、`left`）時，目前輸入法是靈犀就由外殼在更早的階段接手左 Shift：單按切換注／英（與右 Shift 相同：保留已組好的字、清掉未打完的注音），Shift＋字母仍是大寫，Fcitx5 的暫時切換在靈犀裡不再生效；切到其他輸入法時左 Shift 照 Fcitx5 設定。不含左 Shift 時外殼完全不處理左 Shift。
- 「切換視窗後的中英模式」（`0c-update set mode-memory native|shared -apply`，#115）：`native`（Linux 預設）時換輸入框或視窗回到開始時的模式（「開始時的中英模式」`start-mode`，預設注音，#160；Fcitx5 每次聚焦都會呼叫 activate，外殼的模式跟 open 回覆的 `start_mode` 不同時送「設定模式」，舊引擎沒有回報時當成注音）；新的 session 由核心從開始時的模式起，引擎已連上時新的輸入框一聚焦注／英指示就對；`shared` 時所有輸入框共用中英模式，在 A 切到英文、換到 B 也是英文。共用模式由核心處理：外殼聚焦時一律送「設成中文」，核心改成回到共用的模式，注／英指示一換過去就是共用的模式（只用現有連線，聚焦不為此啟動 `0c-server`）。用左 Shift 切到系統英文鍵盤是 Fcitx5 的狀態，換視窗時依 Fcitx5 的「共享輸入法狀態」，不受這個設定影響。GNOME Wayland 上 GTK4 程式都經 GNOME Shell 走 Fcitx5 的 IBus 前端、共用一個輸入框，換視窗是同一個輸入框的失焦／聚焦。
- 有預輸入內容時，右 Shift 切換英文保留已組好的字（未打完的注音清掉，立刻切回時放回），英數、空白及標點插入目前游標位置；可用左右鍵、Backspace 編輯，Enter 一次提交。注音模式下多按的落單聲調鍵也留在框內，可單獨刪除。左 Shift 切至系統鍵盤則會提交組字。見 混合組字流程。
- 候選以基本平面（U+FFFF 以內）的字為主：常見的 Linux 字型不涵蓋擴充 B 區以後，顯示出來只會是方塊。詞庫只收審過的少數擴充區字（台灣地名、台語與客語用字、元素名稱等），其中一部分在 Noto Sans CJK TC 沒有字形。
- 候選分頁與反白由核心決定，Fcitx 的候選窗只顯示目前這一頁；用滑鼠點候選也可以選字。
- 自動修正以斜體加底線標示；Fcitx 沒有點狀底線。
- Tab 補全顯示在提示列，不放進應用程式的組字區。應用程式不顯示組字、組字改畫在面板上時，提示列（含通知、候選標題與頁碼）移到組字下面另起一列，不與組字擠在同一列（#136）。一般建議接在目前字形後；同音替換由使用者按 Tab 要求，明示「小龍 → 小籠包」，再次 Tab 才接受，不跨手選鎖定。見 上下文候選與同音補全。個人補全依選字行為、上下文、近期與跨日使用證據調整，沒有固定「打滿幾次」門檻；較久沒用的偏好會逐漸衰減，詞條仍保留。見 個人詞彙設計。

## 隔離桌面驗收

已有完整預覽包時，在 repo 根目錄執行：

```sh
python3 platforms/linux-fcitx5/test/desktop_test.py --bundle <解壓套件目錄>
```

需要 bubblewrap、Xvfb、D-Bus、Fcitx5 及 GTK3 輸入模組、Python GI、libXtst。工具以私有家目錄掛載、PID／網路／IPC namespace、D-Bus 與 Xvfb 執行；不變更目前的桌面或個人詞庫。結果自動寫入 `build/linux/validation/`，也可用 `--result` 指定新目錄。

檢查全新設定安裝、GTK 中文提交／取消／候選／Tab／右 Shift／密碼欄位、預設關閉 trace，以及更新、回復、解除安裝保留詞庫。這是現有系統函式庫上的 X11／GTK 驗收，不代表其他發行版、Wayland 或 ARM64 已驗證。

切換中英的 Shift 按下與放開都必須交還應用程式（#165：GNOME Wayland 上 VS Code 經 GNOME Shell 的 IBus 介面收鍵，放開被吃掉就以為 Shift 沒放開）。不需要套件，在拋棄式容器裡用建好的 `lib0c.so` 驗：

```sh
docker run --rm -v "$PWD":/w:ro <有 fcitx5 與 python3-gi 的映像> dbus-run-session -- \
  sh /w/platforms/linux-fcitx5/test/shift_release_test.sh <lib0c.so> <0c-server> /w/testdata/toy
```

它在私有 session bus 起 Fcitx5（只開 DBus 與 IBus 介面）與 0c-server，從 Fcitx5 的 InputContext1 與 IBus portal 兩個入口單按左右 Shift，確認按下、放開都回 false 而且注／英確實切換。不要在日常桌面的 session bus 上執行。

### Ubuntu 26.04／GNOME 50 的 GTK 設定

這個組合的原生 GTK Wayland → GNOME IBus 路徑，在開工作列選單時會先重設組字，可能丟失未提交的文字。使用 Fcitx GTK 模組與 Kimpanel；本外殼已處理前端失焦時的提交，避免重複輸出。GTK 模組設定只用於 GNOME，不套用到 Plasma 的原生 Wayland 配置。

確認已安裝 `fcitx5-frontend-gtk3`、`fcitx5-frontend-gtk4` 與符合 GNOME 版本的 Kimpanel，並在 GNOME 的啟動應用程式加入 Fcitx5。於 GNOME 帳號建立 `~/.config/environment.d/92-0c-gnome.conf`：

```ini
GTK_IM_MODULE=fcitx
QT_IM_MODULE=fcitx
XMODIFIERS=@im=fcitx
```

登出再登入後才套用到新啟動的應用程式。組字中開選單會把已完成的中文字提交一次；回到欄位能繼續輸入。此配置是已驗證的處理方式，原生 IBus／合成器的失焦丟字仍是相容性限制。

Fcitx5 設定工具中，選取「靈犀注音」後按齒輪，可修改神經選字、補標點與前文選字；設定同樣交給 `0c-update` 儲存。更新模式、暫停等其餘設定在「靈犀注音設定」程式裡。

工作列選單最上面的「⚙ 靈犀注音設定」會執行 `0c-update open-settings`，開啟獨立的「靈犀注音設定」程式（`platforms/settings`）：它完整列出所有設定（含四組模糊音）與更新狀態。`open-settings` 啟動設定程式後立刻返回。程式找不到、核對不過、缺少 WebKitGTK 或沒有圖形桌面時，桌面通知會說明原因，並提示改用 `0c-update settings` 與 `0c-update set`。選單的第二列是更新狀態，點一下檢查更新或完成已準備好的更新。

`0c-update status --json` 的 `features.vocab` 為 true 時（0.2.0 起一律為 true），設定列下面多一列「我的詞彙…」，執行 `0c-update open-settings vocab`，開到設定程式的「我的詞彙」頁；設定程式已經開著時由單一實例切過去（#125）。舊版 0c-update 沒有這個欄位就不顯示。外殼每 5 秒讀一次狀態，這個欄位改變時（例如回復到舊版）把這一列加進或拿出目前欄位的狀態區（Fcitx 的狀態區不能插在中間，所以拿掉自己的項目後依序加回）。開啟失敗的原因顯示在被點的那一列。

「輸出簡體字」（#169）：`0c-update status --json` 有 `settings.output_simplified`（0.2.1 起），或引擎在 open 回覆 `output_simplified_live=true` 時，「我的詞彙…」與更新列之間多一個可以打勾的「輸出簡體字」。點了在這個欄位的 session 送 `output_simplified` 請求：引擎立即改變之後送出的文字（所有欄位，包括已經在組字區、還沒送出的字），組字不動，並把值寫進 `settings.json`（同 `0c-update set` 的寫入鎖與寫法），不重啟引擎；提示用輸入法資訊小窗：Fcitx 5.1.11 起（`showCustomInputMethodInformation`，執行時查符號）顯示「已切換為輸出簡體字／繁體字」，Ubuntu 24.04 的 5.1.7 沒有這個函式，退回一般的輸入法資訊，顯示標籤「注簡」。舊引擎退回 `0c-update set output-simplified on|off -apply -notify`。勾選與狀態顯示跟著每個快照的 `output_simplified` 與狀態查詢的 `runtime.output_simplified`（舊引擎時是 `settings.output_simplified`），在設定程式切換後下一個按鍵或下一次查詢（5 秒內）就跟上；選單切換後，切換前開始的查詢不會把勾選改回去。開著時注音模式的子模式文字是「注音・簡體輸出」，Kimpanel 的標籤是「注簡」（Kimpanel 有標籤時會用文字取代「注」圖示，所以只在這個狀態給標籤）；Classic UI／系統匣的圖示不變，看選單的勾選。英文模式照舊是「英」（兩個字的「注簡」與 IBus 外殼相同：GNOME 頂列的 IBus 指示只採用不到 3 個字）。這個請求不記進重播紀錄。設定「用快捷鍵切換簡體字」（#192，預設關閉）開著時，Ctrl＋Shift＋F11（只按 Ctrl 與 Shift，沒有 Alt／Super／Hyper；`zuin::simplifiedHotkey`）走選單同一條路，吃掉這個鍵、組字不動、跳出同樣的提示；要不要攔在按下時看 open 回覆的 `simplified_hotkey` 與 `output_simplified_live`（先以 `alive()` 確認連線還在，引擎因設定套用重啟過就重開 session 拿新值）。設定關閉或舊引擎時照一般快捷鍵先送出組字再交給應用程式。限制（#118 起就有，與切換無關）：組字中直接失焦時，組字由 Fcitx／用戶端照畫面上的文字送出，所以是繁體；按 Enter、空白選字或選候選送出的才會轉成簡體。

實機回歸可在專用帳號執行 `test/wayland_probe.py --result /tmp/0c-wayland-result.json`，用真正桌面操作組字、選單、跨欄位及敏感欄位。JSON 的文字是應用程式已提交內容，避免把組字預覽當成提交結果。
