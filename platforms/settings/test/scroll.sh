#!/bin/bash
# 由 smoke.sh 載入。#268：在真的 0c-settings（WebKitGTK）裡，點過選項再往下捲，每 15 秒的背景更新不能把畫面拉回那個選項。
# WebKit 的 <input> 在排版前取得焦點時，排版後會另外捲進畫面且不理 preventScroll；按鈕（開關）不會，
# 所以這裡用「鍵盤排列」的單選（第一個是已選的「標準」）。只截圖比對，不改設定。
new_home scroll
rm "$(base_of scroll)/settings.json"   # 全新安裝：鍵盤頁最上面是「由風格決定」的提示與按鈕，接著是鍵盤排列
launch scroll keyboard
if window_up; then
  sleep 2.5
  wid=$(xdotool search --onlyvisible --name '靈犀注音設定' | head -1)
  tabs $((NAV + 2))   # 選用風格／複製成自訂 → 鍵盤排列「標準」
  sleep 0.5
  import -window root "$OUT/scroll-focused.png"
  xdotool mousemove --window "$wid" 600 450 click --repeat 8 --delay 60 5
  sleep 1.5
  import -window root "$OUT/scroll-down.png"
  sleep 18   # 至少經過一次 15 秒的背景更新
  import -window root "$OUT/scroll-after-refresh.png"
  # 不同的像素數。只比視窗內、右側捲軸以左：捲軸是會自己淡出的浮動指示，不代表捲動位置。
  eval "$(xdotool getwindowgeometry --shell "$wid")"
  area="$((WIDTH - 40))x${HEIGHT}+${X}+${Y}"
  pixels() { compare -metric AE "$1[$area]" "$2[$area]" null: 2>&1 | cut -d' ' -f1; }
  check "鍵盤與符號頁可用滾輪往下捲（焦點在「標準」）" test "$(pixels "$OUT/scroll-focused.png" "$OUT/scroll-down.png")" != 0
  check "焦點在選項上時背景更新不把畫面拉回該選項（#268）" test "$(pixels "$OUT/scroll-down.png" "$OUT/scroll-after-refresh.png")" = 0
  check "只取得焦點，沒有改到鍵盤排列" python3 -c 'import json,os,sys; p=sys.argv[1]; sys.exit(0 if not os.path.exists(p) or "keyboard_layout" not in json.load(open(p)) else 1)' "$(settings_of scroll)"
else bad "scroll/keyboard 視窗沒有出現"; cat /tmp/app-scroll-keyboard.log; fi
stop_app
