# 設定搜尋（#363）的 UI 測試，由 smoke.sh 載入（共用 launch、check、new_home、tabs 等函式）。用真的 0c-settings 與 0c-update：
#   - Ctrl＋F 聚焦搜尋框、打字出結果（預設選取第一筆沒有停用的）、↑↓ 選、Enter 前往：焦點落在那一項的控制項上，
#     接著用鍵盤改得到（看 settings.json）。
#   - 前往之後那一項標出來，約 2.5 秒後淡掉（前後兩張截圖不同）。
#   - Esc 清除並回到原本那一頁，再按一次 Esc 把焦點還給原本的控制項（接著用鍵盤改得到原本那一項）。
#   - 找不到的畫面：建議的關鍵字點了就搜；官網設定說明交給 xdg-open（PATH 上假的，記下收到的網址）。
#   - 搜頁名（輸出設定）：那一頁本身是一筆結果，Enter 切過去，Tab 接著到那一頁的第一個控制項。
#   - 搜尋框是側邊欄的第一個 Tab 站（smoke.sh 的 NAV 已經把它算進去）；清除鈕不占 Tab 站。
# 截圖 search-*.png（只截視窗，960×680）：平常的樣子、結果頁、前往之後、找不到、深色的結果頁，對照設計稿。
# 比對規則、分組順序、各種焦點與主題的細節由 search_test.js 與 search_page_test.py（Playwright，Chromium＋WebKit）負責。

sshot() { window_shot "$1"; }
sopened() { for _ in $(seq 20); do grep -qx "$1" "$(home_of search)/opened-urls" 2>/dev/null && return 0; sleep .2; done; return 1; }
sjson() { python3 -c "import json,sys; d=json.load(open(sys.argv[1])); sys.exit(0 if eval(sys.argv[2], {'d': d}) else 1)" "$(settings_of search)" "$1"; }
slaunch() { # 頁面 [額外的環境變數…]
  local page=$1; shift
  env HOME="$(home_of search)" PATH="$(home_of search)/fakebin:$PATH" $(calls_env) "$@" "$SETTINGS_BIN" --page "$page" >"/tmp/app-search-$page.log" 2>&1 &
  APP_PID=$!
}
sdiff() { compare -metric AE "$OUT/$1.png" "$OUT/$2.png" null: 2>&1 | cut -d' ' -f1; }

new_home search
mkdir -p "$(home_of search)/fakebin"
printf '#!/bin/sh\nprintf "%%s\\n" "$@" >> "%s/opened-urls"\n' "$(home_of search)" > "$(home_of search)/fakebin/xdg-open"
chmod +x "$(home_of search)/fakebin/xdg-open"
check "settings -json 的項目帶搜尋用的關鍵字（輸出簡體字找得到「簡中」）" bash -c "HOME='$(home_of search)' '$UPDATE_BIN' settings -json | python3 -c \"
import json,sys
items={x['key']: x for x in json.load(sys.stdin)['items']}
sys.exit(0 if '簡中' in items['output-simplified']['keywords'] and '半形' in items['ctrl-punctuation']['keywords'] and all(len(w) <= 8 for x in items.values() for w in x.get('keywords', [])) else 1)\""

# ---- 平常的樣子：搜尋框在側邊欄最上面，右邊是 Ctrl F 的提示 ----
slaunch keyboard
if window_up; then
  ready
  sshot search-main
else bad "search/keyboard 視窗沒有出現"; cat /tmp/app-search-keyboard.log; fi
stop_app

# 標點寬度選「所有符號鍵打半形」：Ctrl＋標點固定開啟（反灰），結果頁要列出它與停用原因。
run_update search set shift-symbol-halfwidth all >/dev/null
check "標點寬度設成所有符號鍵打半形" sjson "d['shift_symbol_halfwidth'] is True and d['all_symbols_halfwidth'] is True"

# ---- 從「中英切換」搜「半形」：結果依序是 Ctrl＋標點（停用）、標點寬度、自動加空格、Shift＋空白 ----
slaunch mode
if window_up; then
  ready
  # 先把焦點放在這一頁的第一個控制項（中英切換鍵，選中的是右 Shift），之後 Esc 要還回來。
  tabs $((NAV + 1))
  xdotool key ctrl+f; sleep .5
  xdotool type --delay 120 '半形'; sleep 1
  sshot search-results
  # 預設選取的是第一筆沒有停用的結果（標點寬度；第一筆 Ctrl＋標點是反灰的），直接 Enter 前往：
  # 切到「鍵盤與符號」，那一項標出來，焦點在選中的「所有符號鍵打半形」上。
  xdotool key Return; sleep .8
  sshot search-jumped
  sleep 3.4
  sshot search-jumped-faded
  check "前往之後標出那一項，約 2.5 秒後淡掉（兩張截圖不同）" test "$(sdiff search-jumped search-jumped-faded)" != 0
  # 鍵盤直接改：↑ 換成「Shift＋符號打半形」，↓ 換回來。
  press Up
  check "預設選取跳過反灰的第一筆，Enter 前往標點寬度，焦點在它的控制項：↑ 改成 Shift＋符號打半形" sjson "d['shift_symbol_halfwidth'] is True and d['all_symbols_halfwidth'] is False"
  press Down
  check "↓ 改回所有符號鍵打半形" sjson "d['all_symbols_halfwidth'] is True"
  # 回到搜尋框（裡面還是「半形」）就回到結果頁，選取還在標點寬度；↓ 選第三個（自動加空格，#365 起是三選一的單選），
  # Enter 前往，焦點在選中的「關閉」上：↓ 依序選中英文之間、中英文與數字之間，↑ 兩次關回去。
  xdotool key ctrl+f; sleep .8
  xdotool key Down; sleep .5
  sshot search-results-again
  check "回到搜尋框就回到結果頁，↓ 移到下一筆（跟第一次的結果頁只差選取的那一列）" test "$(sdiff search-results search-results-again)" != 0
  xdotool key Return; sleep 1
  press Down
  check "↓ 選到第三個結果、Enter 前往：↓ 把自動加空格改成中英文之間" sjson "d.get('cjk_latin_space') is True and d.get('cjk_digit_space') is False"
  press Down
  check "再 ↓ 改成中英文與數字之間" sjson "d.get('cjk_latin_space') is True and d.get('cjk_digit_space') is True"
  press Up
  press Up
  check "↑ 兩次關回去" sjson "d.get('cjk_latin_space') is False and d.get('cjk_digit_space') is False"
else bad "search/mode 視窗沒有出現"; cat /tmp/app-search-mode.log; fi
stop_app

# ---- Esc：清除並回到原本那一頁；搜尋框空了再按一次，焦點還給原本的控制項 ----
slaunch mode
if window_up; then
  ready
  tabs $((NAV + 1))                       # 中英切換鍵（右 Shift）
  xdotool key ctrl+f; sleep .5
  xdotool type --delay 120 '簡體'; sleep 1
  xdotool key Escape; sleep .8
  sshot search-escaped
  xdotool key Escape; sleep .5
  press Down
  check "Esc 清除並回到「中英切換」，再按 Esc 焦點回到中英切換鍵：↓ 改成左右 Shift" sjson "d.get('toggle_key') == 'both'"
  check "Esc 沒有前往任何結果（輸出簡體字沒被動到）" sjson "d.get('output_simplified') in (None, False)"
  press Up
  check "中英切換鍵改回右 Shift" sjson "d.get('toggle_key') == 'right'"
  # 搜尋框是第一個 Tab 站：從中英切換鍵按 Shift＋Tab 經過 11 個導覽按鈕回到搜尋框，直接打字就能搜。
  keys=(); for _ in $(seq "$NAV"); do keys+=(shift+Tab); done; xdotool key --delay 120 "${keys[@]}"
  xdotool type --delay 120 '簡中'; sleep 1
  xdotool key Return; sleep 1
  press space
  check "Tab 到搜尋框打「簡中」（關鍵字）、Enter：空白鍵打開輸出簡體字" sjson "d.get('output_simplified') is True"
  press space
  check "輸出簡體字關回去" sjson "d.get('output_simplified') is False"
else bad "search/mode（Esc）視窗沒有出現"; cat /tmp/app-search-mode.log; fi
stop_app

# ---- 找不到：建議的關鍵字與官網設定說明 ----
slaunch keyboard
if window_up; then
  ready
  xdotool key ctrl+f; sleep .5
  xdotool type --delay 120 '字體大小'; sleep 1
  sshot search-empty
  # 從搜尋框 Tab：11 個導覽按鈕（清除鈕不占 Tab 站）、7 個建議的關鍵字，再來是「設定說明」。
  tabs $((NAV - 1 + 7 + 1)); xdotool key Return
  check "找不到時的「設定說明」：官網設定說明頁交給 xdg-open" sopened 'https://0c.biggo.com/guide/settings/'
  # 第三個建議是「Shift」：按了就搜那個詞、焦點回到搜尋框；Enter 前往第一個結果（中英切換鍵），↓ 改成左右 Shift。
  xdotool key ctrl+f; sleep .5
  tabs $((NAV - 1 + 3)); xdotool key space; sleep 1
  sshot search-suggested
  xdotool key Return; sleep 1
  press Down
  check "點建議的「Shift」就搜它，Enter 前往中英切換鍵：↓ 改成左右 Shift" sjson "d.get('toggle_key') == 'both'"
  press Up
  # 搜頁名：「輸出設定」那一頁本身是一筆結果。Enter 切到那一頁、焦點在頁首標題上，下一個 Tab 就是這一頁的第一個控制項
  # （輸出簡體字的開關），空白鍵打開它。
  xdotool key ctrl+f; sleep .5
  xdotool type --delay 120 '輸出設定'; sleep 1
  sshot search-page-result
  xdotool key Return; sleep 1
  sshot search-page-jumped
  press Tab space
  check "搜頁名「輸出設定」、Enter 前往那一頁，Tab 到第一個控制項：空白鍵打開輸出簡體字" sjson "d.get('output_simplified') is True"
  press space
  check "輸出簡體字關回去（頁名結果）" sjson "d.get('output_simplified') is False"
  check "只開了設定說明這一個網址" bash -c "[ \"\$(wc -l < '$(home_of search)/opened-urls')\" = 1 ]"
else bad "search/keyboard（找不到）視窗沒有出現"; cat /tmp/app-search-keyboard.log; fi
stop_app

# ---- 自動加空格（#365）：一項三選一的單選。搜「空格」找得到（名稱裡有）；搜名稱、Enter 前往，焦點在選中的「關閉」上，
#      ↓ 依序選中英文之間、中英文與數字之間，說明下面的提示跟著換，↑ 兩次關回去。截圖 spacing-*.png。 ----
slaunch mode
if window_up; then
  ready
  xdotool key ctrl+f; sleep .5
  xdotool type --delay 120 '空格'; sleep 1
  sshot spacing-search
  check "搜「空格」的結果頁跟搜「半形」的不同" test "$(sdiff search-results spacing-search)" != 0
  xdotool key ctrl+f; sleep .5
  xdotool type --delay 120 '自動加空格'; sleep 1
  xdotool key Return; sleep 4
  sshot spacing-off
  press Down
  check "搜「自動加空格」、Enter 前往：↓ 選中英文之間" sjson "d.get('cjk_latin_space') is True and d.get('cjk_digit_space') is False"
  sshot spacing-on
  press Down
  check "再 ↓ 選中英文與數字之間" sjson "d.get('cjk_latin_space') is True and d.get('cjk_digit_space') is True"
  sshot spacing-all
  check "三個選項的畫面各不相同（選中的選項與它的提示）" bash -c "[ '$(sdiff spacing-off spacing-on)' != 0 ] && [ '$(sdiff spacing-on spacing-all)' != 0 ]"
  check "settings -json 的值是 all，沒有 cjk-digit-space 這一項" bash -c "HOME='$(home_of search)' '$UPDATE_BIN' settings -json | python3 -c \"
import json,sys
items={x['key']: x for x in json.load(sys.stdin)['items']}
it=items['cjk-latin-space']
sys.exit(0 if it['value'] == 'all' and it['values'] == ['off', 'on', 'all'] and it['label'] == '自動加空格' and 'cjk-digit-space' not in items else 1)\""
  press Up
  press Up
  check "↑ 兩次回到關閉" sjson "d.get('cjk_latin_space') is False and d.get('cjk_digit_space') is False"
else bad "search/mode（自動加空格）視窗沒有出現"; cat /tmp/app-search-mode.log; fi
stop_app

# ---- 搜「雙拼」（#370）：選項名稱「小鶴雙拼」找得到鍵盤排列；只截圖 ----
slaunch keyboard
if window_up; then
  ready
  xdotool key ctrl+f; sleep .5
  xdotool type --delay 120 '雙拼'; sleep 1
  sshot search-shuangpin
else bad "search/keyboard（雙拼）視窗沒有出現"; cat /tmp/app-search-keyboard.log; fi
stop_app

# ---- 深色主題的結果頁（GTK 的深色偏好讓 WebKitGTK 回報 prefers-color-scheme: dark）；只截圖 ----
slaunch keyboard GTK_THEME=Adwaita:dark
if window_up; then
  ready
  xdotool key ctrl+f; sleep .5
  xdotool type --delay 120 '半形'; sleep 1
  sshot search-results-dark
  check "深色主題的結果頁跟亮色的不同（有套用深色）" test "$(sdiff search-results search-results-dark)" != 0
else bad "search/keyboard（深色）視窗沒有出現"; cat /tmp/app-search-keyboard.log; fi
stop_app
