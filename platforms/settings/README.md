# 靈犀注音設定（Tauri）

獨立的設定程式（#47）：Rust 外殼＋系統 WebView，畫面是純 HTML／CSS／JS，沒有 npm、沒有打包器。設定邏輯與更新驗證都在 Go；這個程式只呼叫 `0c-update` 的 JSON 契約（[JSON 契約實作](../../cmd/0c-update/contract.go)），沒有網路、登入或機密。

## 結構

| 路徑 | 內容 |
|---|---|
| `src/updater.rs` | 找出並啟動 `0c-update`、逾時、解析 JSON、匯出摘要；附單元測試 |
| `src/main.rs` | 指令白名單、視窗、單一實例 |
| `src/watch.rs` | 「我的詞彙」的即時同步：常駐 `0c-update vocab watch`、轉成 `vocab-changed` 事件、意外結束時重新連上 |
| `ui/` | `index.html`、`app.css`、`app.js`；設定項目依契約動態產生。`vocab.js` 是「我的詞彙」頁，`models.js` 是「模型」頁，`personal-reset.js` 是「我的詞彙」頁「靈犀學到的」底下的「清除學習紀錄」，`input-styles.js` 是「輸入習慣」頁，`update-mode.js` 是更新方式的顯示名稱、下拉選單的選項與說明（#244、#340），`item-help.js` 是設定項目的選項提示與「說明」連結的判斷（#362），`search.js` 是設定搜尋的比對、分組與標示（#363；畫面與鍵盤操作在 `app.js`） |
| `test/` | `smoke.sh`（容器 Xvfb 冒煙測試）、`vocab.sh`（我的詞彙）、`models.sh`（模型）、`styles.sh`（輸入習慣）、`scroll.sh`（背景更新保留捲動位置，#268）、`personal.sh`（清除學習紀錄，#301）、`update-mode.sh`（更新頁的更新方式下拉選單，#340）、`help.sh`（選項提示與「說明」連結，#362）、`search.sh`（設定搜尋，#363）、`fake-update.py`（測試用的 0c-update 替身，只模擬 vocab、api 與 models）、`*_test.js`（`node --test`，不碰畫面的判斷）、`scroll_position_test.py`、`item_help_page_test.py`、`search_page_test.py`（Playwright 在 Chromium 與 WebKit 跑真的頁面，各缺一個就跳過） |
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
| 中英切換（`mode`） | 中英切換鍵、單按 Shift 的時限、開始時的中英模式、切換視窗後的中英模式、中英混打、英文模式自動補字、Caps Lock 開著時直接打英文（Windows、Linux） |
| 選字（`candidates`） | 選字方式（神經選字）、依前文選字、空白鍵選字、修改字詞的位置、選字後游標移到下一個字、選字排列、選字鍵、按 Esc 清除整段組字，另一張卡是模糊音總開關 |
| 鍵盤與符號（`keyboard`） | 鍵盤排列、Ctrl＋標點打全形、標點寬度、提交時補標點（Beta）、Emoji 選單（子選項「將 = 放在第一項」） |

側欄順序：總覽、中英切換、選字、鍵盤與符號、輸出設定、我的詞彙、模型、更新、問題排解、關於。「輸出設定」（`output`）照舊獨立。契約裡不在任何一頁的新項目（較新的 0c-update 才有）畫在「選字」最後，所以新增設定不必先改畫面；要放別頁再加進 `INPUT_PAGES`。舊的頁名 `input`（0.2.5 以前的 `0c-update open-settings input`、說明文件）照樣接受，畫面導到側欄第一頁「中英切換」（`PAGE_ALIAS`；Go 與 Rust 的白名單也保留它，舊版設定程式照舊開輸入設定）。三份頁名清單由 `internal/settingsapp` 的 `TestPageNamesMatchTheSettingsApp` 核對。總覽的「有設定已儲存、尚未套用」帶到第一個尚未套用的項目所在的那一頁。

契約的項目帶 `disabled` 時（倚天 41 鍵時的中英混打、Emoji 選單與等號首項，許氏時的中英混打，#183；漢語拼音時的中英混打，#324；小鶴雙拼時的中英混打，#370；不用 Shift 時的開始模式，#184）開關反灰、畫成關閉，名稱旁顯示 `disabled_reason`，不在 Tab 順序裡；設定檔的值不動，切回標準排列照原值顯示（#83）。造成停用的那一項（`disabled_by`）在別頁時，說明下面多一個「到「鍵盤與符號」修改「鍵盤排列」」，按了切到那一頁並把焦點放在那一項（例如中英混打在「中英切換」、鍵盤排列在「鍵盤與符號」）；在同一頁（開始模式就在中英切換鍵下面）不另外放。

Emoji 選單以一個開關控制（`emoji-shortcut`：off／equals），預設開啟，保留使用者明確關閉的設定。另有 `emoji-equals-first` 開關（預設 on），可取消第一項的等號；設定說明寫明開或關都可以連按兩下 = 打出等號（想單按 = 打出等號要關閉 Emoji 選單，寫在設定說明頁）（GitHub 0c-ime#89）。模糊音顯示一列總開關，預設開啟（四組 select），開啟時以同一次設定交易將四組近音都設為 select，關閉時全部設 off；沒有逐組控制或自動修正單選。舊設定只啟用部分配對或使用 auto 時，畫面視為開啟，載入不改寫；關閉再開啟才統一成四組 select。CLI 的逐組設定與原有檔案格式仍保留，未知欄位與唯讀保護沿用既有契約。

## 說明、選項提示與「說明」連結（#362）

每一項的說明只有一句話（契約的 `description`，40 個全形字以內），畫面不再放一整段。細節分成兩處：

- **選項提示**：契約的 `value_hints`（`{值: 提示}`）。多選一的項目在說明下面只顯示**目前選中那個選項**的提示，前面加選項名稱（「倚天 41 鍵：= 是 ㄦ…」），改選後跟著換；選中的選項沒有提示就不顯示（標點寬度的「全形標點」）。停用的項目照實際使用的值（`effective`）。單選群組以 `aria-describedby` 連到提示。
- **「說明」連結**：契約的 `help_url`（`https://0c.biggo.com/guide/settings/#<鍵名>`），接在說明句子後面，按了用系統瀏覽器開官網的設定說明頁（Rust 的 `open_url`，放行範圍見「模型」一節的表）。模糊音四組合成一個開關，連結用第一組的網址。
  連結**不在 Tab 順序裡**（`tabindex="-1"`）：每頁十幾項，逐項多一站會讓鍵盤操作慢一倍，各頁的 Tab 順序維持原樣；鍵盤使用者把焦點放在那一項的開關或選項上按 **F1** 開同一個網址，螢幕報讀軟體照樣讀得到連結（`aria-label` 寫明是哪一項、會開瀏覽器）。
- **頁首的「說明」連結**：只有「輸入習慣」「更新」「我的詞彙」三頁有，接在頁首那一句後面，分別開設定說明頁的 `#input-styles`、`#update-mode`、`#vocab`（`ui/item-help.js` 的 `PAGE_ANCHORS`）。這三頁沒有逐項的連結，而各種風格參考了什麼、三種更新方式的差別、讀音與匯出匯入的規則這次都從頁面搬到說明頁。樣式與行為同逐項的連結：系統瀏覽器開、不在 Tab 順序裡、在那一頁按 **F1** 開（焦點在哪裡都可以）。網址由介面自己決定，不來自 0c-update：這三頁的文字本來就寫在介面裡，短的文字與連結一起出貨。模型、問題排解、總覽、關於本來就短，不放；四個設定頁只有逐項的連結，焦點不在任何一項上時 F1 不開東西。
- 兩個欄位都是選填。新的設定程式配舊的 0c-update（沒有這兩個欄位、說明還是一長段）：不顯示提示與連結，其他照舊；舊的設定程式配新的 0c-update：忽略這兩個欄位，顯示新的短說明。
- 各頁頁首的說明、輸入習慣頁、更新頁與問題排解頁的提示也照同一個標準縮短；拿掉的內容都在設定參考文件。

判斷在 `ui/item-help.js`（`test/item_help_test.js`）；`test/item_help_page_test.py` 用真的 `0c-update settings -json` 在 Chromium 與 WebKit 以預設視窗大小（960×680）量每一句說明與每一個提示都不超過兩行、點連結與按 F1 開對網址、鍵盤排列四個選項的提示跟著換、舊版契約不顯示、三頁的頁首各有一個連結而其他頁沒有；`test/help.sh`（`--only help`）在真的 WebView 確認 Tab 順序沒變、F1 把網址交給 `xdg-open`（逐項兩次、三頁的頁首各一次），並截四種鍵盤排列與三頁頁首的圖。

## 搜尋（#363）

側邊欄最上面（品牌列下面、導覽上面）有一個搜尋框，每一頁都在。打字就列出符合的設定，選了跳到那一頁的那一項並標出來。搜尋是純前端：只用已經讀到的 `settings -json` 與 `status -json`，不為了搜尋多呼叫 `0c-update`。

- **搜尋範圍**：設定名稱、一句說明（`description`）、選項名稱（`value_labels`，沒有時是畫面自己的對照表）、選項提示（`value_hints`）與關鍵字（契約的 `keywords`：使用者會打、但名稱和說明裡沒有的詞，例如「簡中」「熱鍵」「捲舌」，由 `internal/settings` 的 `keywords()` 提供）。開關的「開啟」「關閉」不算選項名稱。舊版 0c-update 沒有 `keywords` 時照樣能搜名稱、說明與選項。不搜尋「我的詞彙」裡使用者自己的詞（那一頁有自己的搜尋）。
- **比對**：不分大小寫；全形英數與符號（Ｓｈｉｆｔ、＋）視為半形；前後空白忽略；用空白分開的多個詞要全部命中（各自可以在不同欄位）。
- **結果頁**：有字就取代右邊的內容（蓋在目前的頁面上，`page` 不變）。標題「搜尋「…」」，下一行「找到 N 個設定。」（`aria-live="polite"`）。照頁面分組，頁面順序同側邊欄，組內順序同那一頁由上到下（`itemsOnPage`，跟設定頁共用），不依相關度重排。每列是名稱、一句說明（命中的字用 `<mark>`）、目前的值（小標籤，停用時是 `effective`）與 ›；選項名稱命中多一行「選項：…」（只列命中的選項），只有選項提示命中多一行「選項「…」：提示」，只有關鍵字命中多一行「也可以找：…」。反灰停用的項目照樣列出，名稱旁是停用原因（`disabled_reason`）。側邊欄各頁旁標出命中的筆數，搜尋中沒有「目前頁面」的高亮，沒命中的頁面變淡。底部是一行按鍵提示。
- **輸入習慣的規則**（`style-*`）歸在它實際所在的頁面（`INPUT_PAGES`）。四組模糊音在「選字」頁合成一個開關，搜尋也只列一筆「模糊音」，四組的關鍵字合在一起。更新頁的「更新方式」不是 `settings` 的項目，照一般項目列出（選項是三種方式）。
- **沒有設定項的頁面**（總覽、輸入習慣、我的詞彙、模型、更新、問題排解、關於）本身是一筆結果：頁面名稱、一句話與幾個關鍵字（`app.js` 的 `PAGE_ENTRIES`；這幾頁的文字本來就寫在畫面這一側）。輸入習慣另外把各個風格的名稱當成選項、目前選用的當成值。
- **四個設定頁本身**（中英切換、選字、鍵盤與符號、輸出設定）也是一筆結果，排在那一頁分組的最前面，側邊欄的筆數照實算進去；前往就是切到那一頁，焦點在頁首標題上。**只比對頁名**：頁首那一句只顯示（entry 的 `note`）、不比對也不標，不然搜「標點」「候選」這類的字每次都多出一筆。
- **鍵盤**：Ctrl＋F（macOS 是 ⌘F；Mac 的 Ctrl＋F 是文字框裡游標往前一格，不攔）聚焦搜尋框並全選，提示文字照平台顯示 `Ctrl F` 或 `⌘F`。搜尋框有焦點時 ↑↓ 移動選取（到頭就停）、Enter 前往、Esc 清除搜尋並回到搜尋前的那一頁（連捲到的位置）；搜尋框已經是空的時候，Esc 把焦點還給原本的位置。全域只攔 Ctrl＋F 這一個鍵，「我的詞彙」等輸入框的按鍵照舊。輸入法組字中（`isComposing`）不搜尋，↑↓、Enter、Esc 也留給輸入法；組字結束才搜。
- **預設選取**：結果一出來，選取的是第一筆**沒有停用**的結果（打「半形」時是「標點寬度」，不是反灰的「Ctrl＋標點打全形」；`ZcSearch.firstEnabled`），直接按 Enter 就到一個能改的項目。排列順序不變，反灰的照樣在原位、↑↓ 也走得到；全部都是反灰項目時才選第一筆。回到結果頁而查詢沒變時，選取留在原本那一筆。
- **前往**：按 Enter 或點結果，切到那一頁；那一項不在畫面裡才捲到中間，加外框與底色，約 2.5 秒後淡掉（背景重畫時會把標示加回新的節點）。`prefers-reduced-motion` 時不做漸變，時間到直接移除。焦點移到那一項的控制項（開關，或單選裡選中的那一個），鍵盤可以直接改；停用的項目有「到某頁修改」的連結就給它，否則把焦點放在那一列上。前往一整頁時焦點在頁首標題上。頁首上方出現「‹ 回到「…」的搜尋結果」，搜尋框保留查詢字串；點它或回到搜尋框就回到結果頁，換到別頁或清除搜尋時消失。
- **找不到**：「沒有符合的設定。」，卡片裡是「換個說法試試」加七個可以點的關鍵字（候選、標點、Shift、聲調、簡體、英文、更新），以及「還是找不到？」加官網設定說明的連結（`https://0c.biggo.com/guide/settings/`，Rust 的 `open_url`）。
- **無障礙**：搜尋框是 `type="search"`（WebKit 與 Chromium 自帶的外觀、放大鏡與清除鈕都關掉，只有自己的清除鈕），有看不到的 label「搜尋設定」；結果清單是 `role="listbox"`、每列 `role="option"`，搜尋框用 `aria-controls` 與 `aria-activedescendant` 指著選取的那一列，焦點一直留在搜尋框；值的小標籤前面有看不到的「目前：」，報讀時跟說明分得開。Tab 順序：搜尋框排在導覽之前（側邊欄的第一個 Tab 站），清除鈕 `tabindex="-1"` 不占 Tab 站，其餘不變；#362 的「說明」連結照舊不在 Tab 順序、F1 開說明（結果頁上 F1 不開東西）。

比對、分組與標示在 `ui/search.js`（`test/search_test.js`）；`test/search_page_test.py` 用真的 `0c-update settings -json` 在 Chromium 與 WebKit 以預設視窗大小（960×680）測結果的分組與順序、結果列不溢出、↑↓／Enter／Esc、前往後的焦點與標示（含 reduced-motion、標示出現與淡掉時那一列不移動）、找不到的畫面、Ctrl＋F 與 ⌘F、輸入法組字、深淺主題的顏色、最小視窗（640×480）的排版，以及 `type=search` 沒有第二個清除鈕；`test/search.sh`（`--only search`）在真的 WebView 用鍵盤走一遍並截圖（`search-*.png`）。

## 我的詞彙（0.2.0，#125）

`features.vocab` 為真時（0.2.0 起的 `0c-update` 一律為真；舊版沒有這個欄位時不顯示），側欄「輸出設定」下面出現「我的詞彙」，總覽多一張小卡（詞數與 AI 助手最近加的數量；沒有詞時是「把常打錯的人名、術語加進來」）。後端命令、Tauri 指令與事件見 [詞彙命令實作](../../cmd/0c-update/vocab.go)。

- **我加的詞**：頂端是搜尋與新增列。打字時（組字結束後 250 毫秒）查 `annotate`，每個字一格讀音：破音字是黃色下拉選單，查不到的字自己填注音；Enter 加入。英文詞（`ls`、`kubectl`，#146）不查讀音，直接 Enter 加入，清單上讀音的位置標示「英文」、沒有排名。一次貼上多行變成預覽清單（不是漢字也不是英文詞的行不能勾），勾選後一起加入（超過 500 個分批送，整批復原）；「詞＋Tab＋讀音」的行照那個讀音加，不重查。每列：詞、讀音（點了改，可加到 4 個讀音，送出時附 `expect`）、排名徽章（`rank`：首選／第 N 個／第 N 頁，滑過顯示前面是哪個詞）、來源與時間、刪除。超過 1,000 筆分頁，搜尋交給引擎（`list` 的 `query`）。
- **匯出、匯入**（#255）：標題列右邊兩個按鈕。「匯出」把我加的詞與我的片語存成文字檔（`ui/vocab-text.js` 組字串，Rust 的 `export_vocab` 寫到下載資料夾），頁首顯示路徑。「匯入」開 WebView 的檔案對話框（`<input type="file">`），讀進來和貼上多行同一個預覽：上方是摘要（已經有的略過幾個、幾行不能加）與「加入勾選的…」，清單超過 12 列時在框內捲動，片語列在詞下面。格式與合併規則見 [`ui/vocab-text.js`](ui/vocab-text.js)。
- **靈犀學到的**（#182）：`learned` 清單，`kind` 顯示成「常一起用」（`phrase`）或「你選過的寫法」（`reading`），不顯示次數；頂端說明三個動作，每列有勾選框與「保留」（`keep`）、「移除」（`unlearn`）、「不再學」（`forget`），已是自訂詞的顯示「已在我加的詞」、不能勾。勾選列有全選（部分勾選時半勾）與三個批次按鈕，一次送一個指令。移除、不再學、恢復學習之後先拿掉那幾列，底部提示附「復原」（`learned-undo`），焦點交給它；保留是自訂詞的批次，照舊從「最近變更」也能復原。`forgotten_count` 大於 0 時右上角出現「不再學的詞（N）」，打開是另一個清單（`forgotten`），每列與批次都能「恢復學習」（`relearn`），「‹ 靈犀學到的」返回；全部恢復後自動返回、入口消失。清單最後是「清除學習紀錄」（#301，見下面一節；「不再學的詞」裡不顯示）。
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
| `open_url` | `url` | 交給系統瀏覽器（Linux `xdg-open`、macOS `open`、Windows `rundll32 url.dll,FileProtocolHandler`）；只放行兩種：`https://huggingface.co/` 開頭、路徑只有英數與 `/._-` 的網址，以及官網的說明文件 `https://0c.biggo.com/guide/…`（#362，設定項目的「說明」連結；路徑同樣只有英數與 `/._-`，可帶一個小寫英數與 `-` 的錨點，即設定的鍵名）。設定視窗本身照舊不離開本機頁面 |

UI 測試：`python3 scripts/test_settings_ui.py --settings <輸出>/0c-settings [--only models]`，`fake-update.py --models` 依契約模擬內建、未安裝、下載中、已安裝、需要重新下載、缺推論元件、發行版關閉下載、字典不在配對清單上；外部網址交給 PATH 上假的 `xdg-open` 記下來。

## 清除學習紀錄（#301）

「我的詞彙」頁「靈犀學到的」分頁的最後一張卡：在學到的詞清單（含勾選列與批次按鈕）之後，「不再學的詞」子清單裡不顯示。`settings -json` 的 `features.personal_reset` 為真（0.2.12 起的 `0c-update`）才出現；引擎沒在跑、清單讀不到時照樣顯示（由 `0c-update` 直接刪檔）。「我的詞彙」要 `features.vocab` 為真才有（0.2.0 起恆為真）；為假的舊版 `0c-update` 也沒有 `personal_reset`，不另找地方放。不是開關：按「清除…」先在卡片下方確認（說明清除後選字回到剛安裝時的樣子、無法復原、你加的詞與片語不受影響；焦點先在「取消」），按「確認清除」才呼叫 Rust 的 `personal_command`（只允許 `reset`，對應 `0c-update personal reset -json`，逾時 120 秒）。成功後提示並把卡片說明換成「已清除學習紀錄…」，「靈犀學到的」先清空畫面、收起學到的詞的「復原」提示（引擎裡的復原複本也清掉了），再向引擎重讀 `learned`：清單變空、「不再學的詞（N）」入口消失；`busy` 提示稍後再試，其他錯誤顯示 `message`，都不動清單。引擎在不在跑、要刪哪些備份都由 Go 處理，輸出格式見 `cmd/0c-update/personal.go` 的 `personalResetResult`。

「我的詞彙」的 DOM 只建一次，這張卡放在清單旁的固定容器（`vocab.js` 的 `el.reset`），由 `personal-reset.js` 自己重畫：清單重新整理時確認不會被收掉，清單重畫保留焦點（`data-k`）也認得這幾個按鈕。

UI 測試：`python3 scripts/test_settings_ui.py --settings <輸出>/0c-settings --only personal`，用真的 `0c-update` 與 `0c-server`（`testdata/toy`），設定程式呼叫的 `0c-update` 外面包一層記錄參數：「選字」頁沒有這張卡；取消不動作；引擎沒在跑時刪掉 `user.json` 與 Linux／Windows 形式的備份、`vocab.json` 保留；引擎在跑時 `user.json` 立刻變成空白的個人資料、引擎不結束，之後 SIGTERM 結束也不寫回；清除後畫面重讀 `learned`，學到的詞與「不再學的詞（1）」入口消失（分頁標籤的下一個控制項就是「清除…」，再清一次不會按到任何一列）。`test/personal_reset_test.js` 驗證確認文案、結果提示，以及只有成功時才通知「靈犀學到的」重讀。

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
- 匯出的狀態摘要把家目錄換成 `~`，不含輸入內容、個人詞庫或使用者名稱。Windows 另含最近用過輸入法的程式檔名（不含路徑）與輸入法在各程式的判定（`shell.tsf_log`，#257）。

## 執行環境

- 桌面項目的檔名：GTK 在 Wayland 上向合成器宣告的 app id 是**程式名稱** `0c-settings`（用 `WAYLAND_DEBUG=client` 在 GNOME 50 實測 `xdg_toplevel.set_app_id("0c-settings")`），不是 Tauri 的 identifier；所以應用程式項目必須叫 `0c-settings.desktop`，GNOME 才會把視窗對到它，顯示正確的圖示與分組。

- Linux 需要 WebKitGTK 4.1（`libwebkit2gtk-4.1`）與 GTK 3；缺少時載入器會在程式開始前以 exit 127 結束，所以由 `0c-update open-settings` 在啟動前偵測並說明。
- macOS 用系統內建的 WKWebView，沒有額外相依，hardened runtime 不需要 entitlement（以 Developer ID 簽章並通過公證、在 macOS 26 執行驗證）。bundle ID 是 `com.biggo.0c.settings`，`LSMultipleInstancesProhibited` 讓重複啟動由 LaunchServices 把既有視窗帶到前面（單一實例外掛在這個情況下不會被觸發），`open` 的參數也到不了既有的行程。切頁改走 Darwin 通知（#125）：啟動時以 `notify_register_file_descriptor` 為每個頁面登記 `user.uid.<uid>.com.biggo.0c.settings.page.<頁面>`（名稱由 `updater::navigate_notification` 產生，與 Go 端 `settingsapp.NavigateNotification` 相同），`0c-update open-settings` 在 `open` 之後以 `notifyutil -p` 送出，收到就聚焦視窗並發出 `navigate` 事件（`src/navigate_mac.rs`）。`user.uid.<uid>.` 名稱空間只有同一個使用者能送，通知不帶資料，只認白名單內的頁面。
- Windows 用 WebView2 Runtime（Windows 11 與近年的 Windows 10 內建，Evergreen 版）；缺少時由 `0c-update open-settings` 在啟動前偵測並說明。WebView2 的使用者資料夾放在 `%LOCALAPPDATA%\Zuin\settings-webview`，不另外建立以 identifier 命名的目錄。找不到 `0c-update` 時依序試 `ZC_UPDATER`、同資料夾（版本目錄）、`%LOCALAPPDATA%\Zuin\bin`（穩定副本）。匯出的狀態摘要把家目錄換成 `~`，連同 JSON 跳脫後的反斜線與正斜線寫法一起換。
- 偵測到 NVIDIA 私有驅動（`/proc/driver/nvidia`）時自動設 `WEBKIT_DISABLE_DMABUF_RENDERER=1`，避開 Wayland 下 WebKitGTK 白畫面與 Error 71（Tauri 文件列為已知問題，不無條件關閉）。
- 單一實例：已開著就聚焦既有視窗並切到要求的頁面；Linux 沒有 D-Bus session 時這個機制不生效，兩個實例各自執行。在 Wayland 上合成器不准程式自己置頂（沒有 xdg-activation token，在 GNOME 50 實測 `set_focus` 不會把被蓋住的視窗拉到前面），所以第二次啟動改成開新視窗再關掉舊的，新視窗會正常出現在最前面。

更新頁提供檢查更新、立即下載、背景下載加速及立即安裝，按鈕下方是「更新方式」下拉選單（#340）：自動更新（建議）＝`0c-update mode auto`、下載後詢問＝`ask`（在背景下載並通知新版本，按「立即安裝」才更新）、手動更新＝`manual`（照排程檢查並通知新版本，不自己下載；按「立即下載」再按「立即安裝」），選到的那項底下一句說明。Rust 指令 `set_update_mode` 只接受 auto／ask／manual，對應 `0c-update mode <值> -json`；`status -json` 的 `features.update_mode` 為真（0.2.15 起）才顯示選單，舊版 0c-update 照舊只列「更新方式」。更新頁每秒重畫，選單有焦點時先不重畫（離開時補畫），免得打開的選單被換掉。說明文字在 `ui/update-mode.js`（`test/update_mode_test.js`），UI 測試 `test/update-mode.sh`（`--only update`）在真的 WebView 用鍵盤在三個選項間切換、打開選單等 3 秒再選，並核對狀態檔。下載顯示實際百分比與已收到／總大小，每秒重讀執行狀態；核對和安裝顯示處理中的進度條。總覽與關於頁分開標示已安裝版本、目前執行的引擎及設定視窗版本，Windows 新外殼已安裝但尚未重新登入時明確提示。後端命令及進度格式見 [JSON 契約實作](../../cmd/0c-update/contract.go)。

Windows 完整更新視窗驗收另有 `test/settings-update-test.ps1`：在沒有安裝的專用測試機，以隔離個人資料目錄、已簽章測試 HTTPS 索引和真套件驗證立即下載、共用並加速背景下載、手動安裝與重新登入前的版本提示。測試使用 e2etest 更新器；互動視窗以管理員執行，所以不包含原生 UAC 同意／取消對話框的真人驗證。

輸入設定的「左 Shift 切換中英」（`left-shift`，`settings.json` 的 `left_shift`）預設關閉，左 Shift 照系統原本的用途；開啟後單按左 Shift 與右 Shift 一樣切換注音／英文。外殼在單按時向引擎查目前載入的值（`health` 的 `left_shift`），套用（引擎重啟）後生效。

輸入設定的「按 Esc 清除整段組字」（`esc-clear`，`settings.json` 的 `esc_clear`）預設開啟，與多數注音輸入法相同。不論開關，Esc 都先清除還沒打完的注音；開啟時再按一次清除整段，關閉時保留已組好的字。引擎讀設定，套用（引擎重啟）後生效，外殼不必改。

輸入設定的「空白鍵選字」（`space-selects`，`settings.json` 的 `space_selects`）預設開啟：注音打完按空白開候選。關閉後注音打完按空白在游標處輸入空格，改用 ↓ 選字；注音打到一半時空白仍是一聲，候選開著時空白仍換頁（#107、#110）。中英混打開啟時本來就是如此，不受這個設定影響，所以這時這一項反灰、寫「中英混打時關閉，按 ↓ 選字」，並附到「中英切換」修改混打的連結（#288）；設定檔的值不動，關掉混打就照舊。引擎讀設定，套用（引擎重啟）後生效，外殼不必改。

輸入設定的「選字鍵」（`selection-keys`，`settings.json` 的 `selection_keys`）三選一：`1234567890`（預設，舊設定檔沒有欄位時也是）、`1234qweras`、`asdfghjkl;`，不開放自訂（#112）。候選窗依序用那一排鍵選第 1–10 個字，鍵標由引擎隨候選送給外殼；換成字母鍵時數字 1–0 仍照位置選字（舊外殼、自己畫候選窗的程式只顯示數字也能選）。沒有候選窗時這些鍵照舊是注音鍵。畫在「選字」頁、選字排列之後（#207）。引擎讀設定，套用（引擎重啟）後生效。

輸入設定的「修改字詞的位置」（`candidate-cursor`）可選「游標前方」／「游標後方」。macOS 未指定時採前方，Linux／Windows 採後方；舊設定缺少欄位時同樣使用平台預設，明確保存的 before／after 會保留。後方模式的游標在組字尾端時後面沒有字，改選結束在游標的詞（同前方模式），說明文字也註明這個例外。設定儲存在 `settings.json` 的 `candidate_cursor`，透過既有組字保護套用；不變更個人詞庫格式。

輸入設定的「Ctrl＋標點打全形」（`ctrl-punctuation`，`settings.json` 的 `ctrl_punctuation`）預設開啟，放在「鍵盤與符號」頁、鍵盤排列之後（#207）。開啟時注音模式按 Ctrl＋, . ' ; 與 Ctrl＋Shift＋/ 1 ; 9 0 打全形的 ，。、；？！：（）（#231）；設定說明頁列出會占用 VS Code 的 Ctrl＋, 、Ctrl＋. 、Excel 的 Ctrl＋Shift＋; 9、Konsole 的 Ctrl＋Shift＋9 0 等快速鍵，要保留時可以關掉，Windows 另寫明系統預設把 Ctrl＋Shift＋0 留給切換輸入法、這一鍵打不出「）」（畫面上的說明只有一句，#362）。關閉後這些鍵同其他快捷鍵交給應用程式（#168）。引擎讀設定，套用（引擎重啟）後生效；外殼依 health 的 `ctrl_punctuation` 決定要不要送這些鍵。

「標點寬度」（`shift-symbol-halfwidth`，`settings.json` 的 `shift_symbol_halfwidth` 與 `all_symbols_halfwidth`，#258、#282）三選一，預設「全形標點」，排在 Ctrl＋標點打全形之後：半形用 Shift 或所有符號鍵、全形用 Ctrl。「Shift＋符號打半形」時注音模式按 Shift＋符號鍵打半形標點（Shift＋, 是半形逗號、Shift＋9 是 `(`；< > 在 ↓ 裡），未按 Shift 的 [ ] 照舊打「」；「所有符號鍵打半形」時每個符號鍵都打按鍵上的字（Shift＋- 是 `_`、[ 是 `[`），這時 Ctrl＋標點固定開啟、不打聲調也能選字暫停（兩項顯示停用並帶回這一項）。三個選項畫成單選。不屬於輸入習慣風格，選用內建風格時也能開關。引擎讀設定，套用後生效。

「單按 Shift 的時限」（`shift-tap-ms`，`settings.json` 的 `shift_tap_ms`，#319）是一組單選：0.2／0.3／0.4／0.5／0.6 秒（選項名稱在 `value_labels`），預設 0.3 秒，排在「中英切換鍵」之後。中英切換鍵選「不用 Shift」時反灰，寫「不用 Shift 切換時不作用」。不屬於輸入習慣風格。引擎讀設定、在 health 的 `shift_tap_ms` 回報，套用（引擎重啟）後外殼下一次單按 Shift 就照新的時限。

「自動加空格」（`cjk-latin-space`，`settings.json` 的 `cjk_latin_space` 與 `cjk_digit_space`，#273、#365）三選一，預設「關閉」，排在「標點寬度」之後，畫成一組單選（Tab 一站、方向鍵換選項，同「標點寬度」）。「中英文之間」（`on`）：送出的文字裡，中文字和英文字母直接相連處補一個半形空格（「用Python寫」送出「用 Python 寫」），數字旁不補。「中英文與數字之間」（`all`）：中文字和半形數字相連處也補（「第2版」送出「第 2 版」）。組字區照打的顯示，已有空格與標點旁不補，只看這一次送出的字；數字要在組字裡打才補得到；「我的片語」的內容照原樣送出。說明只有一句，兩個選項各一句提示，細節在設定參考文件。不屬於輸入習慣風格。引擎讀設定，套用後生效。

「打錯注音時提示」（`zhuyin-error-hint`，`settings.json` 的 `zhuyin_error_hint`，#300）預設開啟，排在「選字」頁的「不打聲調也能選字」之後。按了聲調但讀音沒有字時不收聲調、注音停住並提示（Windows、macOS 提示音，Linux 標示注音），緊接著再按一次同一個聲調照舊留下注音；只有一個注音符號（ㄟ、ㄅ＋空白）照舊直接留下。中英混打或不打聲調開著時反灰並附原因。不屬於輸入習慣風格。引擎讀設定，套用後生效。

「記住我選的字詞」（`personal-learning`，`settings.json` 的 `personal_learning`，#297）預設開啟，排在「選字」頁的「打錯注音時提示」之後、輸入習慣的規則之前。關閉時照常選字（神經選字、依前文選字、已學到的照用），只是不再記下新的選擇、英文單字和最近用過的符號；「我的詞彙」照常。個人偏好，不屬於輸入習慣風格。引擎讀設定，套用後生效。

輸入設定的「Caps Lock 開著時直接打英文」（`caps-lock-english`，`settings.json` 的 `caps_lock_english`）預設關閉，放在「中英切換」頁最後（#207），只有 Windows、Linux 有：macOS 開 Caps Lock 一律切到英文大寫（#84），`0c-update settings` 不列這一項，設定程式就不顯示。開啟時 Caps Lock 是暫時的英文模式（字母大寫、數字與符號半形，正在打的字先送出，關掉回到注音；先用 Shift 切到英文時只改大小寫）；關閉時注音模式不理 Caps Lock（#173、#168）。引擎讀設定，套用（引擎重啟）後生效；外殼依 health／open 的 `caps_lock_english` 決定要不要回報 Caps Lock 狀態。

輸入設定的「不打聲調也能選字」（`toneless`，`settings.json` 的 `toneless`）預設關閉，放在「選字」頁的選字項目最後、模糊音之前（#207 的頁面依 spec 排序，沒列在頁面清單裡的項目排在該頁最後）。開啟時聲調選填：ㄋㄧㄏㄠ 就是「你好」，有打聲調的字照聲調，組字中的 ' 用來切字；中英混打反灰並標「不打聲調時關閉」，已儲存的值不動。只在標準排列、漢語拼音與小鶴雙拼有效，倚天、許氏時這一項反灰（#150）；漢語拼音時它決定拼音後面的空白是一聲還是不打聲調就完成（#324），小鶴雙拼時它決定要不要每兩鍵就換成字（#370）。引擎讀設定，套用（引擎重啟）後生效，外殼不必改。

輸入設定的「輸出簡體字」（`output-simplified`，`settings.json` 的 `output_simplified`）預設關閉，在設定程式左側獨立的「輸出設定」頁（`--page output`）。開啟後組字和候選仍顯示繁體，送出時才轉成簡體字（OpenCC ver.1.4.2 的 tw2s 字典，編在 `0c-server` 裡，見 `internal/simplified/opencc`）；我的詞彙與我的片語也一起轉，英文、數字、標點與 Emoji 不變，學習照常記繁體（#118）。這一項可以即時切換（#169）：開關照樣執行 `0c-update set output-simplified on|off -apply -json`，0c-update 寫好設定後，引擎回報 `output_simplified_live=true` 就請它即時切換（`apply.state` 是 `applied`），不重啟引擎，組字中也立即生效；舊引擎照舊交接重啟。四個外殼的輸入法選單也有可以打勾的「輸出簡體字」，從選單切換時由引擎寫 `settings.json`，設定程式的開關下一次讀取（切回視窗或重新整理）就跟著變。

輸出設定頁的「用快捷鍵切換簡體字」（`simplified-hotkey`，`settings.json` 的 `simplified_hotkey`，#192）預設關閉，排在「輸出簡體字」下面；說明由 `0c-update settings` 依平台寫出快捷鍵（Windows／Linux Ctrl＋Shift＋F11、macOS ⌃⇧F11；F 鍵是媒體鍵時加按 Fn／fn 寫在設定說明頁）。開啟後四個外殼攔這個鍵，跟輸入法選單一樣即時切換「輸出簡體字」；這個開關本身照一般設定交接重啟引擎套用。

輸入設定的「選字後游標移到下一個字」（`advance-after-select`，`settings.json` 的 `advance_after_select`）預設關閉，選字後游標留在原處。開啟後從候選窗選好字（數字鍵、Enter、滑鼠都算），游標移到所選詞的後面，可以接著按 ↓ 改下一個字；修改游標前方字詞時往後移一個字（同新酷音的 autoShiftCur），游標在句尾時不動（#133）。符號、Emoji 與原樣注音本來就插入在游標處、游標停在它們後面。引擎讀設定，套用（引擎重啟）後生效，外殼不必改。
