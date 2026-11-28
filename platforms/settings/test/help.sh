# 選項提示與「說明」連結（#362）的 UI 測試，由 smoke.sh 載入（共用 launch、check、new_home、tabs 等函式）。
# 用真的 0c-update：
#   - 「說明」連結不在 Tab 順序裡：第 NAV+1 個 Tab 仍是鍵盤排列，再一個 Tab 仍是 Ctrl＋標點的開關。
#   - 焦點在某一項上按 F1，官網設定說明頁的那一節交給 xdg-open（PATH 上假的，記下收到的網址）。
#   - 鍵盤排列四個選項各截一張（help-layout-*.png），提示跟著選項換；看截圖。
#   - 輸入習慣、更新、我的詞彙三頁的頁首各有一個「說明」連結：在那一頁按 F1，#input-styles、#update-mode、#vocab 交給 xdg-open；
#     各截一張（help-page-*.png）。
# 提示內容、行數、滑鼠點連結、舊版 0c-update 沒有新欄位時的畫面由 item_help_page_test.py（Playwright）與 item_help_test.js 負責。

hshot() { window_shot "$1"; }
hopened() { # 場景 網址：等 xdg-open 收到這個網址（最多 4 秒）
  for _ in $(seq 20); do grep -qx "$2" "$(home_of "$1")/opened-urls" 2>/dev/null && return 0; sleep .2; done; return 1
}
hlayout() { grep -q "\"keyboard_layout\": \"$2\"" "$(settings_of "$1")"; }

new_home help
mkdir -p "$(home_of help)/fakebin"
printf '#!/bin/sh\nprintf "%%s\\n" "$@" >> "%s/opened-urls"\n' "$(home_of help)" > "$(home_of help)/fakebin/xdg-open"
chmod +x "$(home_of help)/fakebin/xdg-open"
check "settings -json 的鍵盤排列帶五個選項的提示與說明連結" bash -c "HOME='$(home_of help)' '$UPDATE_BIN' settings -json | python3 -c \"
import json,sys
it=next(x for x in json.load(sys.stdin)['items'] if x['key']=='keyboard-layout')
sys.exit(0 if sorted(it['value_hints'])==['eten','hsu','pinyin','standard','xiaohe'] and it['help_url']=='https://0c.biggo.com/guide/settings/#keyboard-layout' else 1)\""
env HOME="$(home_of help)" PATH="$(home_of help)/fakebin:$PATH" $(calls_env) "$SETTINGS_BIN" --page keyboard >/tmp/app-help-keyboard.log 2>&1 &
APP_PID=$!
if window_up; then
  ready
  hshot help-layout-standard
  # 第一個控制項是鍵盤排列（一組單選）：F1 開這一項的說明。
  tabs $((NAV + 1)); xdotool key F1
  check "鍵盤排列按 F1：設定說明頁的 #keyboard-layout 交給 xdg-open" hopened help 'https://0c.biggo.com/guide/settings/#keyboard-layout'
  for layout in eten hsu pinyin xiaohe; do
    press Down
    check "鍵盤排列 ↓：$layout" hlayout help "$layout"
    hshot "help-layout-$layout"
  done
  # 一次改一格：儲存中這一項暫時停用，連按會掉鍵；press 等儲存完成、畫面重讀設定才回來。
  for _ in 1 2 3 4; do press Up; done
  check "鍵盤排列改回標準" hlayout help standard
  # 「說明」連結不是 Tab 站：再一個 Tab 就是 Ctrl＋標點打全形的開關（預設開），空白鍵關掉它。
  xdotool key Tab F1
  check "下一個 Tab 是 Ctrl＋標點：F1 開 #ctrl-punctuation" hopened help 'https://0c.biggo.com/guide/settings/#ctrl-punctuation'
  press space
  check "下一個 Tab 是 Ctrl＋標點的開關（說明連結不占 Tab 站）" grep -q '"ctrl_punctuation": false' "$(settings_of help)"
  check "只開了這兩個網址" bash -c "[ \"\$(wc -l < '$(home_of help)/opened-urls')\" = 2 ]"
else bad "help/keyboard 視窗沒有出現"; cat /tmp/app-help-keyboard.log; fi
stop_app

# 頁首的「說明」連結（輸入習慣、更新、我的詞彙）：這三頁沒有逐項的連結，F1 開那一頁的章節。
# 先按一個 Tab 讓鍵盤焦點進到頁面裡（第一站是側邊欄最上面的搜尋框，#363；沒在搜尋時 F1 照舊開這一頁的說明），連結本身不是 Tab 站。
for pair in styles:input-styles update:update-mode vocab:vocab; do
  hpage=${pair%%:*}; hanchor=${pair#*:}
  env HOME="$(home_of help)" PATH="$(home_of help)/fakebin:$PATH" $(calls_env) "$SETTINGS_BIN" --page "$hpage" >"/tmp/app-help-$hpage.log" 2>&1 &
  APP_PID=$!
  if window_up; then
    ready
    hshot "help-page-$hpage"
    tabs 1; xdotool key F1
    check "$hpage 頁按 F1：設定說明頁的 #$hanchor 交給 xdg-open" hopened help "https://0c.biggo.com/guide/settings/#$hanchor"
  else bad "help/$hpage 視窗沒有出現"; cat "/tmp/app-help-$hpage.log"; fi
  stop_app
done
check "三頁各開一次，沒有多開別的網址" bash -c "[ \"\$(wc -l < '$(home_of help)/opened-urls')\" = 5 ]"
