# Linux IBus 外殼

給不用 Fcitx5 的 Linux 桌面（Ubuntu／GNOME 預設就是 IBus）使用。程式在 [`cmd/0c-ibus`](../../cmd/0c-ibus)，以 Go 寫成：由 ibus-daemon 依元件描述檔啟動，透過 IBus 的私有 D-Bus 提供 engine，按鍵與畫面交給 `0c-server`。引擎、配對字典、個人資料與設定程式都和 Fcitx5 外殼共用；兩個外殼同時使用時連到同一個 `0c-server`。

Linux 完整套件內附 `bin/0c-ibus`（靜態連結），安裝到 `~/.local/bin/0c-ibus`。一行安裝依桌面目前使用的框架選擇：Fcitx5 在執行（或之前就以 Fcitx5 使用靈犀注音）時維持 Fcitx5；IBus 在執行時以 `0c-ibus register` 登錄到 IBus。

## 行為

與 Fcitx5 外殼（`platforms/linux-fcitx5/src/engine.cpp`）相同的部分：

- 按鍵對應：帶 Ctrl／Alt／Super 的組合鍵一律交還應用程式，組字中先送出組字再交還（單按修飾鍵本身不送出，#106）；可見 ASCII 與具名鍵交給核心。Home／End（含數字鍵盤）、PageUp／PageDown、↑ 與 Shift+Tab（含 ISO_Left_Tab，送 `BackTab`）都交給核心：組字中 Home／End 移到組字區頭尾，其他吃掉不動作，沒在組字時交還應用程式（#106）。Ctrl＋Home／End 與 Ctrl＋Shift+Tab 在送出組字後、放行前同步英文 token 邊界。
- 0.2.0 的加詞與反悔鍵（#123）：引擎在 open 回覆回報 `vocab_keys=true`（0.2.0 起的引擎在自訂詞彙可以修改時回報）時，只按著 Shift 的 ← →、Delete、Backspace 送 `ShiftLeft`、`ShiftRight`、`ShiftDelete`、`ShiftBackspace`：組字中 Shift＋← → 從游標標示一段字（反白：底線加藍底白字），Enter 加入詞庫、Esc 取消；候選窗開著時 Shift＋Delete／⌫ 刪除反白的自訂詞或忘記學到的詞。值隨 session 快取，引擎重啟後重開 session 時更新；沒回報（舊引擎不認得新鍵名）時照舊送原本的鍵。提示列有通知時優先於候選標題。
- 單按 Shift 切換注／英，哪一側依「中英切換鍵」（`toggle-key`：`right` 預設、`both`、`left`、`none`，#117；取代舊的 `left-shift`）；組合鍵中的 Shift、按住超過 0.3 秒（health 的 `shift_tap_ms`，沒有時 300 ms）都不算單按。確定是單按的放開時才向引擎查詢 health 的 `left_shift`、`right_shift`，設定套用後下一次按 Shift 就生效；舊引擎沒有 `right_shift` 時右 Shift 照舊切換。不在切換鍵裡的 Shift 交給應用程式（IBus 與 GNOME 預設沒有用到單按 Shift）。切到英文保留已組好的字、清掉未打完的注音，沒按其他鍵就立刻再按一次切回時放回；失焦或重設後不放回。GNOME 頂列「注／英」選單項目的說明跟著切換鍵（例如「注音（右 Shift 切換英文）」，`none` 時只寫「注音」）。
- 「切換視窗後的中英模式」（`mode-memory`，#115）：`native`（Linux 預設）時每次取得焦點（`FocusIn`）都回到開始時的模式（「開始時的中英模式」`start-mode`，預設注音，#160；外殼的模式跟 open 回覆的 `start_mode` 不同時送「設定模式」，舊引擎沒有回報時當成注音）；GNOME Wayland 上所有視窗共用一個 IBus 輸入框，換視窗只看得到同一個輸入框的 FocusOut／FocusIn，這就是「換視窗回到注音」。`shared` 時聚焦一樣送「設成中文」，核心改成回到共用的模式，在 A 切到英文、換到 B 也是英文（只用現有連線，聚焦不為此啟動 `0c-server`）。
- 斷線重連後重開 session 並重播這次組字；重播結果與斷線前不同時先顯示、等使用者確認。
- 依前文選字：只在應用程式回報的文字與游標完全符合剛提交的結果時才確認（`SetSurroundingText`）。
- 欄位用途是密碼／PIN 或帶 private 提示時，請求加上 `private`（GNOME 對密碼欄位會直接停用輸入法）。

IBus 特有的部分：

- **組字用 `IBUS_ENGINE_PREEDIT_COMMIT` 模式**：失焦、重設、切換輸入法時由 ibus-daemon 或用戶端把畫面上的組字提交出去；engine 在這些時候只讓核心完成同一段組字（學習並清除），自己不再送出提交，避免重複。提交新文字前先清掉畫面上的組字。
- **候選表只放目前一頁**，翻頁與反白由核心決定；頁碼、候選標題、Tab 補全與提示放在提示列（auxiliary text）。模糊音附註接在候選文字後面。
- **組字樣式用 IBus 屬性**：一般底線、選過的字雙底線、自動修正用錯誤底線、加詞標示範圍加上前景／背景色。GTK 與 Qt 的 IBus 輸入模組會畫出顏色；組字畫面沒變（文字、游標與各段樣式都相同）時不重送，避免閃爍，只改樣式（例如 Esc 取消標示）也會重送。
- **選單用 IBus 屬性**：`InputMode`（GNOME 頂列顯示「注／英」，點了切換）、「⚙ 靈犀注音設定」、更新狀態列。文字與點擊後的動作和 Fcitx5 選單相同（`0c-update open-settings`、`auto -now -notify`／`finish -notify`）。`features.vocab` 為 true（0.2.0 起一律為 true）時，設定下面多一個「我的詞彙…」屬性（`vocab`），執行 `open-settings vocab`（#125）。這個屬性一律登錄，`features.vocab` 只改它的 `visible`（以 `UpdateProperty` 送出）：GNOME Shell 每次換 engine 只收第一個 `RegisterProperties`（`ibusManager.js` 的 `_engineChanged`），之後重新登錄會被丟掉，而外殼第一次登錄時狀態通常還沒讀回來。狀態在取得焦點且上次讀取超過一分鐘時重讀。
- **依序處理呼叫**：godbus 為每個方法呼叫各開一個 goroutine，順序不保證；收訊執行緒依到達順序發號碼牌，處理時照號碼輪流（`dispatch.go`），按鍵、失焦與重設不會互相超車。
- IBus 預設是全域 engine（`use-global-engine`），所有視窗共用同一個 engine 物件，靠 FocusIn／FocusOut 切換；每個 engine 物件一個核心 session。

## 登錄到 IBus

一行安裝在 IBus 桌面會自動登錄；手動安裝套件或要從 Fcitx5 改用 IBus 時執行：

```sh
~/.local/bin/0c-ibus register        # 元件檔、環境、GNOME 輸入來源
~/.local/bin/0c-ibus unregister      # 移除
```

`register` 不需要 root；已登錄且內容沒變時（例如重跑一行安裝）只回報已登錄，不重啟 IBus、也不把使用者自己移掉的輸入來源加回去。第一次或內容有變時會：

1. 寫 `~/.local/share/ibus/component/0c.xml`（路徑都是絕對路徑；ibus-daemon 不搜尋 PATH）。
2. 寫 `~/.config/environment.d/91-0c-ibus.conf`：`IBUS_COMPONENT_PATH=/usr/share/ibus/component:${HOME}/.local/share/ibus/component`。ibus-daemon 沒有使用者元件目錄，只認這個變數。
3. 刪掉 `~/.cache/ibus/bus/registry`。ibus-daemon 預設先讀這份快取，快取只核對它當初看過的目錄，不刪掉就看不到新目錄。
4. `systemctl --user set-environment` 同一個變數並重新啟動 IBus 服務：systemd 使用者管理員只在啟動時讀 environment.d，已在執行（例如開了 linger）時要另外更新。這樣不必登出就能用。
5. GNOME 上把 `('ibus', '0c')` 加到輸入來源清單最後；清單原本是空的（GNOME 用系統鍵盤配置）時，先放入 `localectl` 的系統配置，才切得回原本的鍵盤。
6. 讓 GTK／Qt 改用 IBus 自己的輸入模組（原因見下一節；重新登入後生效，程式在 `cmd/0c-ibus/toolkit.go`）：`~/.config/environment.d/92-0c-ibus-gtk.conf` 寫 `GTK_IM_MODULE=ibus`，`93-0c-ibus-qt.conf` 寫 `QT_IM_MODULES=ibus;wayland`。
   - 使用者或系統自己指定了別的值（其他 environment.d 檔、`/etc/environment` 或目前環境裡，例如 `GTK_IM_MODULE=fcitx`、`QT_IM_MODULE=fcitx`）時不寫，並移掉我們會蓋過它的舊檔案；GNOME 自己的預設（`wayland;ibus`、`QT_IM_MODULE=ibus`）不算。
   - `-gtk-module=false`／`-qt-module=false` 停用：移掉設定，留下只有註解的停用記號，之後重跑安裝或更新都不加回來；`-qt-module=true` 才恢復。沒寫旗標時維持原本的選擇。
   - 已登錄時重跑 `register` 只補上缺少的設定，不重啟 IBus。IBus 外殼更新完成後，更新器執行 `0c-ibus toolkits` 做同樣的事，新版本加入的設定不必重跑安裝就會補上。
7. `unregister` 移除兩個檔案；只有檔案裡是我們的設定、而且沒有使用者自己的值時才清 `GTK_IM_MODULE`／`QT_IM_MODULES`。從 environment.d 讀進 systemd 使用者管理員的變數無法以 `unset-environment` 移除，刪檔後要重新登入才還原。

系統若有 `/var/cache/ibus/bus/registry`（`ibus write-cache --system` 產生），ibus-daemon 會先用它；Ubuntu 26.04 沒有這個檔，其他發行版要另外確認。

## GNOME：焦點移走時丟組字

GNOME Wayland 的原生輸入路徑（GTK → text-input-v3 → mutter → GNOME Shell → IBus）在焦點移走時會丟掉還沒提交的組字，例如組字中點頂列選單或切到別的視窗。mutter 50 的 `meta_wayland_text_input_set_focus` 先對應用程式送 `leave`、把它移出焦點清單，之後才執行「重設時提交組字」，提交因此送不到任何人；GNOME Shell 也對 ibus-daemon 設了 `ClientCommitPreedit`，daemon 不會代為提交。這與輸入法框架無關，Fcitx5 經 IBus 前端時也一樣（見 [Fcitx5 說明](../linux-fcitx5/README.md#ubuntu-2604gnome-50-的-gtk-設定)）。

GTK 程式改用 IBus 的 GTK 模組（`GTK_IM_MODULE=ibus`）時，由程式自己的輸入法模組在失焦時提交組字，實測不丟字也不重複。Firefox（snap）同樣適用。

Qt 6.8 起依 `QT_IM_MODULES` 的順序選輸入方式。gnome-session 在這個變數空著時設成 `wayland;ibus`，Qt 6 就先走原生路徑，組字中點頂列選單或切換視窗會丟字，欄位還留著帶底線的殘影。`register` 寫 `QT_IM_MODULES=ibus;wayland`，讓 Qt 6 改用 IBus 的 Qt 外掛（gnome-session 不會蓋掉已設定的值），實測換欄位、點選單都只提交一次；載不到 IBus 外掛的程式（例如自帶 Qt 的 AppImage）仍可退回 Wayland 原生路徑。Qt 5 只讀 `QT_IM_MODULE`，GNOME 本來就設成 `ibus`。

仍有的限制（#73）：

- Qt 的 IBus 外掛失焦時不提交組字，組字中切換視窗會丟字（Qt 5、Qt 6 都一樣）。
- Qt 5.15 的外掛沒有 `ClientCommitPreedit`（QTBUG-109576，Qt 6.5 起才修），同一視窗換欄位時 Qt 和 ibus-daemon 各提交一次。Qt 6.4（Ubuntu 24.04）也沒有這項修正。

Chromium（snap，原生 Wayland 加 `--enable-wayland-ime`）與 VS Code .deb（Electron 原生 Wayland）實測不丟字也不重複。VS Code 的 snap 版自帶的 GTK 沒有 IBus 模組，任何 IBus 輸入法都打不出中文（#74）。

## 測試

```sh
go test ./cmd/0c-ibus
```

單元測試用真的 `ipc.Server` 加玩具詞庫，檢查序列化格式（`(sa{sv}sv)` 等簽章）、按鍵對應（含 #123 的新鍵名與 vocab_keys 開關）、組字樣式與只改樣式時的重畫、通知優先於候選標題、呼叫順序、提交前先清組字、失焦不重複提交、候選與滑鼠選字、右 Shift、中英切換鍵四種值與 0.3 秒單按門檻（`toggle_key_test.go`）、聚焦回到注音與共用中英模式、重連重播、密碼欄位、依前文選字與輸入來源清單編輯、更新交接入口；更新器與安裝器另有 IBus 測試（`cmd/0c-update/ibus_setup_linux_test.go`、`scripts/test_release.py`）。

排錯時以 `systemctl --user set-environment ZC_IBUS_DEBUG=1` 後重新啟動 IBus，engine 把收到的方法名稱與欄位類型（不含按鍵與文字）寫到 `$XDG_RUNTIME_DIR/0c/ibus-debug.log`；ibus-daemon 把元件的 stderr 接到 /dev/null。

## 安裝、更新與移除

- `install.py`：`0c-ibus` 是選用檔案（舊套件沒有它）。只有 IBus、沒有 Fcitx5 的電腦也能安裝；這時不以 `ldd` 檢查 Fcitx5 外殼的函式庫（`lib0c.so` 照樣裝，不影響輸入）。升級、回復與移除時要求 `0c-ibus` 已結束（與 Fcitx5 相同，桌面更新請用 `0c-update finish`）。移除時、或回復到沒有 IBus 外殼的版本時，撤銷 IBus 登錄（只刪內容是靈犀注音的元件檔）。
- `0c-update`：換外殼的更新不必登出。Fcitx5 沒在執行、使用者登錄了 IBus 外殼時走 IBus 路徑；`0c-ibus` 沒在執行時直接交接引擎並換檔，下次選用時 ibus-daemon 啟動的就是新版。
- 交接入口與 Fcitx5 外殼相同（session bus 的 `org.biggo.ime.Update`／`/update`／`org.biggo.ime.Update1`）。`Status` 另回報 `frontend`、`active`（是否為選用中的 engine）與 `ibus_address`；`ExitForUpdate` 在沒有組字、閒置夠久時回 `ready`，之後不再處理按鍵並結束行程。兩個外殼同時在執行時只有先取得名稱的那個提供交接入口。

## 還沒做的

- Fcitx5 的齒輪設定（神經選字、補標點、依前文選字）在 IBus 沒有對應，請用「靈犀注音設定」程式；GNOME 設定裡輸入來源的齒輪會執行 `0c-update open-settings`。
- 沒有 trace 錄製。
- `make upgrade`（開發者從原始碼更新自己的桌面）只處理 Fcitx5。
- 未驗：KDE／其他桌面的 IBus、X11 工作階段、Ubuntu 24.04（Qt 6.4）、HiDPI／多螢幕、有系統登錄快取（`/var/cache/ibus/bus/registry`）的發行版。
