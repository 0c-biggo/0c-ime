#!/bin/bash
# 設定程式的冒煙測試：在 Xvfb 裡執行真的 0c-settings、0c-update 與（用 testdata/toy 的）0c-server，
# 逐頁截圖，並用鍵盤實際切換一個開關，核對「畫面 → Rust → 0c-update → settings.json → 引擎交接」整條路徑。
# 由 scripts/test_settings_ui.py 在建置容器內執行；需要 SETTINGS_BIN、UPDATE_BIN、SERVER_BIN、DATA、OUT。
set -u
fail=0
failures=0
ok()   { echo "PASS: $1"; }
bad()  { echo "FAIL: $1"; fail=1; scene "$1"; }
check() { local desc=$1; shift; if "$@"; then ok "$desc"; else bad "$desc"; fi }

export LANG=C.UTF-8 LC_ALL=C.UTF-8   # xdotool 要在 UTF-8 locale 下才能比對中文視窗標題
export XDG_RUNTIME_DIR=/tmp/xdg-runtime
mkdir -p -m 700 "$XDG_RUNTIME_DIR" "$OUT"
# -noreset：X 伺服器預設在最後一個用戶端離開時重設自己，重設期間不收新連線（#366）。場景之間設定程式結束、
# 畫面上一個用戶端都不剩，下一個 xdotool 或設定程式剛好在重設時連線就「Can't open display」；機器忙時重設拖得久。
Xvfb :99 -noreset -screen 0 1100x800x24 >/tmp/xvfb.log 2>&1 &
export DISPLAY=:99
for _ in $(seq 50); do xdpyinfo >/dev/null 2>&1 && break; sleep 0.2; done
eval "$(dbus-launch --sh-syntax)"

# 失敗當下的現場（#366）：視窗樹、行程、設定程式與 WebKit 的輸出（app log）、呼叫 0c-update 的紀錄、整個畫面。
# 放在 $OUT/failures/<序號>/，主機上是 --out 底下的 shots/failures。
scene() {
  failures=$((failures + 1))
  local dir="$OUT/failures/$failures"
  mkdir -p "$dir"
  printf '%s\n' "$1" > "$dir/what.txt"
  xwininfo -root -tree > "$dir/xwininfo.txt" 2>&1
  ps -eo pid,ppid,stat,etimes,time,args > "$dir/ps.txt" 2>&1
  cat /proc/loadavg > "$dir/loadavg.txt" 2>&1
  cp "$(ls -t /tmp/app-*.log 2>/dev/null | head -1)" /tmp/xvfb.log /tmp/server*.log "$dir/" 2>/dev/null
  cp "$CALLS" "$dir/update-calls.log" 2>/dev/null
  import -window root "$dir/screen.png" 2>/dev/null
  echo "  現場：failures/$failures（$1）"
}

# 設定程式經 calls.sh 呼叫 0c-update，記下每次呼叫的開始與結束（#366），用來等「畫面讀完」「儲存完成」。
CALLS=/tmp/update-calls.log
CALLS_SH=/src/platforms/settings/test/calls.sh
calls_mark() { wc -l < "$CALLS" 2>/dev/null || echo 0; }
# ready：視窗出現後等畫面拿到設定並畫好；saved：按了會儲存的鍵之後等儲存完成、畫面重讀設定。上限 30 秒。
# 等不到不算一項失敗（接著的 check 會核對結果），只印出紀錄並留下現場。
calls_wait() { # ready|saved 起點
  if ! python3 /src/platforms/settings/test/calls_wait.py "$CALLS" "$2" "$1" 30 "${APP_PID:-}"; then
    echo "WAIT: 等不到 $1"; scene "等不到 $1"
  fi
}
ready() { calls_wait ready 0; }
wait_for() { # 上限秒數 指令…：每 0.1 秒試一次，直到指令成功
  local deadline=$((SECONDS + $1)); shift
  until "$@"; do [ $SECONDS -lt $deadline ] || return 1; sleep 0.1; done
}
press() { # 按鍵…：按下會儲存一項設定的鍵，等儲存完成
  local mark; mark=$(calls_mark)
  xdotool key --delay 150 "$@"
  calls_wait saved "$mark"
}

home_of() { echo "/tmp/home-$1"; }
base_of() { echo "/tmp/home-$1/.local/share/zuin"; }
settings_of() { echo "/tmp/home-$1/.local/share/0c/settings.json"; }
# 舊設定會保留為自訂習慣，既有逐項操作測試從升級情境開始。
# 全新安裝的固定預設另於 fresh 與 styles.sh 刪除此檔驗證。
new_home() {
  rm -rf "$(home_of "$1")"
  mkdir -p "$(base_of "$1")/update"
  printf '{"schema":1}\n' > "$(base_of "$1")/settings.json"
}
state_json() { # 更新器狀態；排程標為已移除，狀態檢查就不會去查這台機器的系統排程
  cat > "$(base_of "$1")/update/state.json" <<JSON
{"schema":1,"install_id":"smoke","mode":"auto","stage":"${2:-idle}","schedule_removed":true$3}
JSON
}

calls_env() { # [實際的 0c-update] → 給設定程式的環境變數：經 calls.sh 轉給它，並清空呼叫紀錄
  : > "$CALLS"
  echo "ZC_UPDATER=$CALLS_SH CALLS_TARGET=${1:-$UPDATE_BIN} UPDATE_CALLS=$CALLS"
}
launch() { # 場景 頁面 → 啟動設定程式（背景）；設定 APP_PID
  local name=$1 page=$2
  env HOME="$(home_of "$name")" $(calls_env) "$SETTINGS_BIN" --page "$page" >"/tmp/app-$name-$page.log" 2>&1 &
  APP_PID=$!
}
# 可見的設定視窗。這版 xdotool（3.20160805）搜尋時逐一查視窗樹，查到一半有視窗被摧毀就因 BadWindow 直接結束，
# 連 --sync 也一起放棄（#366；xdotool issue #60）。上一個設定程式與它的 WebKitWebProcess 被結束後，X 視窗在 wait 回來之後
# 才陸續消失，正好跟下一個場景找視窗重疊。BadWindow 只代表這一輪沒查完，重查即可。其他錯誤（例如連不上畫面）
# 回傳空的，由呼叫的人照常重試或判失敗。每次出錯都記在 $OUT/xdotool-errors.log，看得出實際多常發生、在哪個場景。
# 給了行程編號就只找那個行程的視窗（_NET_WM_PID），不會把還沒消失的上一個視窗當成新的。
app_windows() { # [行程編號]
  local out i
  for i in $(seq 20); do
    out=$(xdotool search --onlyvisible ${1:+--pid "$1"} --name '靈犀注音設定' 2>/tmp/xdotool.err)
    if [ -s /tmp/xdotool.err ]; then
      printf '%s %s %s\n' "$(date +%T.%N | cut -c1-12)" "$(ls -t /tmp/app-*.log 2>/dev/null | head -1)" "$(grep -m1 -E 'Major opcode|Error' /tmp/xdotool.err)" >> "$OUT/xdotool-errors.log"
    fi
    grep -q 'BadWindow' /tmp/xdotool.err || { [ -n "$out" ] && echo "$out"; return 0; }
  done
}
app_window() { app_windows "${1:-}" | head -1; }
# 只截設定視窗（檔名不含 .png）。找不到視窗時記一項失敗：import -window 拿到空的視窗編號會等人用滑鼠點選，整個測試卡住。
window_shot() {
  local id; id=$(app_window)
  if [ -n "$id" ]; then import -window "$id" "$OUT/$1.png"; else bad "截圖 $1 時找不到設定視窗"; fi
}
# 等設定視窗出現（上限 30 秒）。預設等 launch 啟動的那個行程（APP_PID）的視窗，
# 不會把還沒消失的上一個視窗當成新的；any：不限行程（0c-update open-settings 啟動的）。
window_up() { # [any]
  local pid=${APP_PID:-} deadline=$((SECONDS + 30))
  [ "${1:-}" = any ] && pid=
  while [ $SECONDS -lt $deadline ]; do
    [ -n "$(app_windows "$pid")" ] && return 0
    if [ -n "$pid" ] && ! kill -0 "$pid" 2>/dev/null; then break; fi
    sleep 0.1
  done
  echo "  等了 30 秒沒有行程 ${pid:-（不限）} 的可見設定視窗；行程$(kill -0 "${pid:-1}" 2>/dev/null && echo 仍在執行 || echo 已結束)"
  return 1
}
stop_app() { # 結束 launch 啟動的設定程式，等它的視窗從畫面上消失（下一張整個畫面的截圖不會拍到它）
  kill "$APP_PID" 2>/dev/null; wait "$APP_PID" 2>/dev/null
  for _ in $(seq 50); do [ -z "$(app_windows "$APP_PID")" ] && break; sleep 0.1; done
}
shoot() { # 場景 頁面
  local name=$1 page=$2
  launch "$name" "$page"
  if window_up; then ready; import -window root "$OUT/$name-$page.png"; ok "$name/$page 視窗出現並截圖"
  else bad "$name/$page 視窗沒有出現"; cat "/tmp/app-$name-$page.log"; fi
  if kill -0 "$APP_PID" 2>/dev/null; then ok "$name/$page 程式仍在執行"; else bad "$name/$page 程式提早結束"; cat "/tmp/app-$name-$page.log"; fi
  stop_app
}
run_update() { local name=$1; shift; HOME=$(home_of "$name") "$UPDATE_BIN" "$@"; }
# 側欄的 Tab 站數：最上面的搜尋框（#363，排在導覽之前；清除鈕不占 Tab 站），加上 11 個導覽按鈕
# （總覽、輸入習慣、中英切換、選字、鍵盤與符號、輸出設定、我的詞彙、模型、更新、問題排解、關於；#207）。
# 每一頁的第一個控制項是第 NAV+1 個 Tab；vocab.sh、models.sh 等也用它。
NAV=12
tabs() { local n=$1; local keys=(); for _ in $(seq "$n"); do keys+=(Tab); done; xdotool key --delay 120 "${keys[@]}"; }

# SMOKE_ONLY=vocab／models／styles／scroll／personal／update／help／search：只跑「我的詞彙」（vocab.sh）、「模型」（models.sh）等其中一部分，開發時省時間。
if [ -z "${SMOKE_ONLY:-}" ]; then

# ---- 場景 A：全新安裝，沒有設定檔也沒有引擎 ----
new_home fresh
rm "$(base_of fresh)/settings.json"
for page in styles overview mode candidates keyboard output update troubleshoot about; do shoot fresh "$page"; done

# ---- 舊的頁名 input（0.2.5 以前的「輸入設定」，#207）導到「中英切換」：第一個控制項是中英切換鍵 ----
new_home alias
launch alias input
if window_up; then
  ready
  import -window root "$OUT/alias-input.png"
  tabs $((NAV + 1)); press Down
  check "--page input 開在中英切換（第一項中英切換鍵可改成左右 Shift）" grep -q '"toggle_key": "both"' "$(settings_of alias)"
else bad "alias/input 視窗沒有出現"; cat /tmp/app-alias-input.log; fi
stop_app

# ---- 問題排解：匯出狀態摘要到「下載」資料夾，提示寫明寄到哪裡（#152，看截圖） ----
new_home export
mkdir -p "$(home_of export)/Downloads"
launch export troubleshoot
if window_up; then
  ready
  # 搜尋框與導覽之後第一個是匯出按鈕。
  # 匯出只讀狀態、不改設定（沒有儲存可等）：等檔案出現。
  tabs $((NAV + 1)); xdotool key space
  check "匯出的狀態摘要寫進「下載」資料夾" wait_for 30 bash -c "ls '$(home_of export)'/Downloads/0c-status-*.json >/dev/null 2>&1"
  sleep 0.5   # 只為截圖：提示訊息
  import -window root "$OUT/export-troubleshoot.png"
else bad "export/troubleshoot 視窗沒有出現"; cat /tmp/app-export-troubleshoot.log; fi
stop_app

# ---- 場景 B：已調整過、含較新版本才有的欄位 ----
new_home configured
printf '{"schema":1,"selection":"statistical","fuzzy":{"z-zh":"select"},"future":{"x":1}}\n' > "$(base_of configured)/settings.json"
state_json configured idle ',"last_check":"2026-10-06T01:00:00Z","last_success":"2026-10-06T01:00:00Z","next_check":"2026-10-06T07:00:00Z"'
shoot configured candidates
shoot configured update

# ---- 場景 C：設定檔損毀、場景 D：來自較新版本 ----
new_home corrupt; printf 'not json {{' > "$(base_of corrupt)/settings.json"; shoot corrupt overview
new_home newer; printf '{"schema":9,"selection":"statistical"}\n' > "$(base_of newer)/settings.json"; shoot newer candidates

# ---- 場景 E：有新版 ----
new_home update
state_json update found ',"last_check":"2026-10-06T01:00:00Z","last_success":"2026-10-06T01:00:00Z","next_check":"2026-10-06T07:00:00Z","target":{"version":"0.1.0-dev.43","kind":"core","url":"https://example.invalid/x","sha256":"'$(printf 'a%.0s' $(seq 64))'","size":123,"found_at":"2026-10-06T01:00:00Z"},"latest":{"version":"0.1.0-dev.43","eligible":true}'
shoot update update

# ---- 可見下載進度與已安裝／待重新登入的標示 ----
new_home progress
state_json progress found ',"last_check":"2026-10-08T08:00:00Z","last_success":"2026-10-08T08:00:00Z","target":{"version":"0.1.0-dev.49","kind":"shell","sha256":"'$(printf 'a%.0s' $(seq 64))'","size":67108864}'
printf '%s\n' "$$" > "$(base_of progress)/update/coordinator.lock"
printf '{"pid":%s,"version":"0.1.0-dev.49","sha256":"%s","phase":"downloading","received":18874368,"total":67108864,"interactive":false}\n' "$$" "$(printf 'a%.0s' $(seq 64))" > "$(base_of progress)/update/operation.json"
shoot progress update
cp "$OUT/progress-update.png" "$OUT/progress-downloading.png"
python3 - "$(base_of progress)/update/operation.json" <<'PY'
import json,sys
p=sys.argv[1]; v=json.load(open(p)); v.update(phase='verifying', received=v['total']); json.dump(v,open(p,'w'))
PY
shoot progress update
mv "$OUT/progress-update.png" "$OUT/progress-verifying.png"
rm "$(base_of progress)/update/coordinator.lock"
new_home pending
state_json pending pending ',"last_check":"2026-10-08T08:00:00Z","last_success":"2026-10-08T08:00:00Z","target":{"version":"0.1.0-dev.49","kind":"shell","sha256":"'$(printf 'a%.0s' $(seq 64))'","size":67108864},"blocked":{"reason":"needs_relogin","detail":"已安裝，重新登入後生效","since":"2026-10-08T08:00:00Z"}'
printf '{"version":"0.1.0-dev.49","active":true}\n' > "$(base_of pending)/install-receipt.json"
shoot pending overview
shoot pending update

# ---- 場景 F：真的引擎，補標點（Beta）預設關閉，鍵盤可開啟並再關閉 ----
new_home live
live_home=$(home_of live)
export HOME=$live_home
"$SERVER_BIN" -data "$DATA" -user "$(base_of live)/user.json" -socket "$XDG_RUNTIME_DIR/0c/0c.sock" -legacy-socket "" >/tmp/server.log 2>&1 &
for _ in $(seq 50); do [ -S "$XDG_RUNTIME_DIR/0c/0c.sock" ] && break; sleep 0.2; done
check "引擎已啟動" test -S "$XDG_RUNTIME_DIR/0c/0c.sock"
check "更新器看得到執行中的引擎" bash -c "'$UPDATE_BIN' settings -json | python3 -c \"import json,sys; sys.exit(0 if json.load(sys.stdin)['engine']['state']=='running' else 1)\""
check "補標點 Beta 預設關閉且引擎未啟用" bash -c "'$UPDATE_BIN' settings -json | python3 -c \"import json,sys; it=next(x for x in json.load(sys.stdin)['items'] if x['key']=='punctuation'); sys.exit(0 if it['value']=='off' and it['running']=='off' and it['default']=='off' and 'Beta' in it['label'] else 1)\""
check "補標點狀態為依設定關閉" bash -c "'$UPDATE_BIN' status -json | python3 -c \"import json,sys; sys.exit(0 if json.load(sys.stdin)['runtime']['punctuation_state']=='disabled_by_setting' else 1)\""
# 鍵盤路徑：「鍵盤與符號」的鍵盤排列（一組單選）、Ctrl＋標點、標點寬度（一組單選，#258）、自動加空格（一組單選，#273、#365）→ 「提交時補標點（Beta）」開關，按空白鍵開啟。
launch live keyboard
if window_up; then
  ready
  import -window root "$OUT/live-keyboard-before.png"
  tabs $((NAV + 5)); press space
  import -window root "$OUT/live-keyboard-after.png"
  check "自行開啟後新路徑 settings.json 的 punctuation 為 true" grep -q '"punctuation": true' "$(settings_of live)"
  check "套用後引擎已交接退出（下一個按鍵才以新設定重新啟動）" bash -c "! [ -S '$XDG_RUNTIME_DIR/0c/0c.sock' ] || ! python3 - <<'PY'
import socket,sys
s=socket.socket(socket.AF_UNIX); 
try: s.connect('$XDG_RUNTIME_DIR/0c/0c.sock'); sys.exit(0)
except OSError: sys.exit(1)
PY"
  press space
  check "再按一次可關閉補標點" grep -q '"punctuation": false' "$(settings_of live)"
else bad "live/keyboard 視窗沒有出現"; cat /tmp/app-live-keyboard.log; fi
stop_app
# 「中英切換」：中英切換鍵、單按 Shift 的時限、開始時的中英模式、切換視窗後的中英模式、中英混打、英文模式自動補字。
launch live mode
if window_up; then
  ready
  # 第一個是中英切換鍵（一組單選，預設右 Shift，#117）：↓ 選「左右 Shift」，舊欄位 left_shift 跟著寫 true。
  tabs $((NAV + 1)); press Down
  check "中英切換鍵可保存左右 Shift" bash -c "grep -q '\"toggle_key\": \"both\"' '$(settings_of live)' && grep -q '\"left_shift\": true' '$(settings_of live)'"
  import -window root "$OUT/toggle-key-both.png"
  press Up
  check "中英切換鍵可改回右 Shift" bash -c "grep -q '\"toggle_key\": \"right\"' '$(settings_of live)' && grep -q '\"left_shift\": false' '$(settings_of live)'"
  # 再下一個是單按 Shift 的時限（一組單選，預設 0.3 秒，#319）：↓ 選 0.4 秒，↑ 改回。
  press Tab Down
  check "單按 Shift 的時限可保存 400" grep -q '"shift_tap_ms": 400' "$(settings_of live)"
  import -window root "$OUT/shift-tap-400.png"
  press Up
  check "單按 Shift 的時限可改回 300" grep -q '"shift_tap_ms": 300' "$(settings_of live)"
  # 再下一個是開始時的中英模式（一組單選，預設注音，#160）：↓ 選「英文」；Linux 的「切換視窗後」native 名稱跟著變成「每次回到英文」。
  press Tab Down
  check "開始時的中英模式可保存 english" grep -q '"start_mode": "english"' "$(settings_of live)"
  check "native 的名稱跟著開始時的模式" bash -c "'$UPDATE_BIN' settings -json | python3 -c \"import json,sys; it=next(x for x in json.load(sys.stdin)['items'] if x['key']=='mode-memory'); sys.exit(0 if it['value_labels']['native']=='每次回到英文' else 1)\""
  import -window root "$OUT/start-mode-english.png"
  press Up
  check "開始時的中英模式可改回 zhuyin" grep -q '"start_mode": "zhuyin"' "$(settings_of live)"
  # 再下一個是切換視窗後的中英模式（一組單選，Linux 預設 native，#115）：↓ 選「全部共用同一個模式」。
  press Tab Down
  check "切換視窗後的中英模式可保存 shared" grep -q '"mode_memory": "shared"' "$(settings_of live)"
  import -window root "$OUT/mode-memory-shared.png"
  press Up
  check "切換視窗後的中英模式可改回 native" grep -q '"mode_memory": "native"' "$(settings_of live)"
  # 混打之後是獨立的英文自動補字開關。
  press Tab Tab space
  check "英文自動補字開關可保存 on" grep -q '"english_autocomplete": true' "$(settings_of live)"
  import -window root "$OUT/english-autocomplete-on.png"
  press space
  check "英文自動補字開關可改回 off" grep -q '"english_autocomplete": false' "$(settings_of live)"
else bad "live/mode 視窗沒有出現"; cat /tmp/app-live-mode.log; fi
stop_app
# 「選字」：選字方式（一組單選）、依前文選字 → 空白鍵選字、修改字詞的位置 → 選字後游標、選字排列、選字鍵 → Esc。
launch live candidates
if window_up; then
  ready
  import -window root "$OUT/live-candidates.png"
  # 空白鍵選字，預設開啟（#107、#110）。
  tabs $((NAV + 3)); press space
  check "空白鍵選字開關可保存 off" grep -q '"space_selects": false' "$(settings_of live)"
  import -window root "$OUT/space-selects-off.png"
  press space
  check "空白鍵選字開關可改回 on" grep -q '"space_selects": true' "$(settings_of live)"
  # 修改字詞的位置（一組單選）之後是選字後游標移到下一個字，預設關閉（#133）。
  press Tab Tab space
  check "選字後游標開關可保存 on" grep -q '"advance_after_select": true' "$(settings_of live)"
  import -window root "$OUT/advance-after-select-on.png"
  press space
  check "選字後游標開關可改回 off" grep -q '"advance_after_select": false' "$(settings_of live)"
  # 選字排列、選字鍵（兩組單選）之後是按 Esc 清除整段組字，預設開啟。
  press Tab Tab Tab space
  check "Esc 清除整段組字開關可保存 off" grep -q '"esc_clear": false' "$(settings_of live)"
  import -window root "$OUT/esc-clear-off.png"
  press space
  check "Esc 清除整段組字開關可改回 on" grep -q '"esc_clear": true' "$(settings_of live)"
else bad "live/candidates 視窗沒有出現"; cat /tmp/app-live-candidates.log; fi
# 單一實例：再啟動一次，不該多出視窗，也不該卡住。
HOME=$live_home ZC_UPDATER=$UPDATE_BIN timeout 10 "$SETTINGS_BIN" --page update >/tmp/second.log 2>&1
second=$?
check "第二個實例很快結束（單一實例）" test "$second" -eq 0
check "仍然只有一個視窗" test "$(app_windows | wc -l)" -eq 1
sleep 1.5   # 只為截圖：既有視窗切到更新頁
import -window root "$OUT/live-second-instance.png"
stop_app
unset HOME

# 輸出設定是左側獨立一頁（#118）：導覽之後第一個是輸出簡體字開關，預設關閉。
launch live output
if window_up; then
  ready
  tabs $((NAV + 1)); press space
  check "輸出簡體字開關可保存 on" grep -q '"output_simplified": true' "$(settings_of live)"
  import -window root "$OUT/output-simplified-on.png"
  press space
  check "輸出簡體字開關可改回 off" grep -q '"output_simplified": false' "$(settings_of live)"
  # 下一個是「用快捷鍵切換簡體字」（#192），預設關閉，說明寫出快捷鍵。
  press Tab space
  check "快捷鍵開關可保存 on" grep -q '"simplified_hotkey": true' "$(settings_of live)"
  import -window root "$OUT/simplified-hotkey-on.png"
  press space
  check "快捷鍵開關可改回 off" grep -q '"simplified_hotkey": false' "$(settings_of live)"
else bad "live/output 視窗沒有出現"; cat /tmp/app-live-output.log; fi
stop_app

# ---- 總覽的「輸入法仍未全部切換」（0.2.1 候選版冒煙）：真引擎回報 ENGINE_VERSION ----
# 核心更新只換引擎、外殼收據不變，不該提示；核心已切換但舊引擎還在跑、外殼等重新登入時要提示。
# 判斷本身由 test/engine_state_test.js 逐平台斷言，這裡核對真的 0c-update 給畫面的欄位並截圖。
pkill -x 0c-server 2>/dev/null; sleep 0.5
start_engine() { # 場景 → 背景執行真引擎；設定 ENGINE_PID
  rm -f "$XDG_RUNTIME_DIR/0c/0c.sock"
  HOME=$(home_of "$1") "$SERVER_BIN" -data "$DATA" -user "$(base_of "$1")/user.json" -socket "$XDG_RUNTIME_DIR/0c/0c.sock" -legacy-socket "" >"/tmp/server-$1.log" 2>&1 &
  ENGINE_PID=$!
  for _ in $(seq 50); do [ -S "$XDG_RUNTIME_DIR/0c/0c.sock" ] && break; sleep 0.2; done
}
stop_engine() { kill "$ENGINE_PID" 2>/dev/null; wait "$ENGINE_PID" 2>/dev/null; }
receipt() { printf '{"version":"%s","active":true}\n' "$2" > "$(base_of "$1")/install-receipt.json"; }
core_pointer() { # 場景 版本 → 核心版本目錄與 engine.json（相容核心更新寫的指標）
  local dir="versions/$2-c-aaaaaaaaaaaa" b
  b=$(base_of "$1")
  mkdir -p "$b/$dir/data"; echo '{}' > "$b/$dir/data/manifest.json"; install -m 0755 "$SERVER_BIN" "$b/$dir/0c-server"
  printf '{"schema":1,"directory":"%s","version":"%s"}\n' "$dir" "$2" > "$b/engine.json"
}
status_is() { # 場景 收據 指標 執行中 → 核對 status -json 給畫面的三個版本
  run_update "$1" status -json | python3 -c "import json,sys; d=json.load(sys.stdin); sys.exit(0 if (d['installed'],d['engine'],d['runtime'].get('version'))==tuple(sys.argv[1:]) else 1)" "$2" "$3" "$4"
}
done_at=',"last_check":"2026-10-18T08:00:00Z","last_success":"2026-10-18T08:00:00Z","next_check":"2026-10-18T14:00:00Z"'
hash64=$(printf 'a%.0s' $(seq 64))

new_home coreupdated   # 0.2.0 外殼＋相容核心更新到 ENGINE_VERSION，新引擎已在跑：不提示
receipt coreupdated 0.2.0; core_pointer coreupdated "$ENGINE_VERSION"; state_json coreupdated idle "$done_at"
start_engine coreupdated
check "核心更新後：收據 0.2.0、指標與執行中的引擎都是 $ENGINE_VERSION" status_is coreupdated 0.2.0 "$ENGINE_VERSION" "$ENGINE_VERSION"
shoot coreupdated overview
shoot coreupdated update
stop_engine

new_home corepending   # 指標已切到 0.2.9，舊引擎（ENGINE_VERSION）還在組字：提示等引擎重新啟動，不叫人登出
receipt corepending 0.2.0; core_pointer corepending 0.2.9
state_json corepending pending "$done_at"',"target":{"version":"0.2.9","kind":"core","sha256":"'$hash64'","size":1},"switched":true,"blocked":{"reason":"engine_busy","detail":"仍在組字","since":"2026-10-18T08:00:00Z"}'
start_engine corepending
check "核心待交接：指標 0.2.9、執行中仍是 $ENGINE_VERSION" status_is corepending 0.2.0 0.2.9 "$ENGINE_VERSION"
shoot corepending overview
stop_engine

new_home shellpending  # 外殼更新已安裝、等重新登入，引擎還是舊版：提示登出再登入
receipt shellpending 0.2.9
state_json shellpending pending "$done_at"',"target":{"version":"0.2.9","kind":"shell","sha256":"'$hash64'","size":1},"blocked":{"reason":"needs_relogin","detail":"已安裝，重新登入後生效","since":"2026-10-18T08:00:00Z"}'
start_engine shellpending
check "外殼等重新登入：收據 0.2.9、沒有指標、執行中是 $ENGINE_VERSION" status_is shellpending 0.2.9 "" "$ENGINE_VERSION"
shoot shellpending overview
stop_engine

# #169：「輸出簡體字」可以即時切換：引擎在執行時，設定程式的開關不交接重啟引擎（同一個行程），引擎立刻回報新的值。
new_home outputlive
start_engine outputlive
launch outputlive output
live_runtime() { # 期望值 → 引擎回報能即時切換，而且目前的值是它
  run_update outputlive status -json | python3 -c "import json,sys; r=json.load(sys.stdin)['runtime']; sys.exit(0 if r.get('output_simplified_live') is True and r.get('output_simplified') is (sys.argv[1]=='on') else 1)" "$1"
}
if window_up; then
  ready
  tabs $((NAV + 1)); press space
  check "輸出簡體字：引擎在執行時開啟，寫入設定" grep -q '"output_simplified": true' "$(settings_of outputlive)"
  check "輸出簡體字：引擎沒有重啟（同一個行程）" kill -0 "$ENGINE_PID"
  check "輸出簡體字：引擎立即回報開啟" live_runtime on
  import -window root "$OUT/output-simplified-live-on.png"
  press space
  check "輸出簡體字：即時關閉，寫入設定" grep -q '"output_simplified": false' "$(settings_of outputlive)"
  check "輸出簡體字：關閉後引擎仍是同一個行程" kill -0 "$ENGINE_PID"
  check "輸出簡體字：引擎立即回報關閉" live_runtime off
  # #192：「用快捷鍵切換簡體字」要交接重啟引擎（外殼看新引擎回報的 simplified_hotkey），下一個按鍵起生效。
  # 引擎在執行時交接；之後引擎沒在執行（容器裡沒有常駐引擎），外殼下一個按鍵啟動引擎時讀新設定，提示一樣。
  press Tab space
  check "快捷鍵：引擎在執行時開啟，寫入設定" grep -q '"simplified_hotkey": true' "$(settings_of outputlive)"
  check "快捷鍵：開啟時交接，舊引擎已結束" bash -c "! kill -0 $ENGINE_PID 2>/dev/null"
  import -window root "$OUT/simplified-hotkey-live-on.png"
  press space
  check "快捷鍵：引擎沒在執行時關閉，寫入設定" grep -q '"simplified_hotkey": false' "$(settings_of outputlive)"
  import -window root "$OUT/simplified-hotkey-stopped-off.png"
else bad "outputlive/output 視窗沒有出現"; cat /tmp/app-outputlive-output.log; fi
stop_app
stop_engine

new_home switched      # 完整安裝 ENGINE_VERSION、引擎也是：不提示
receipt switched "$ENGINE_VERSION"; state_json switched idle "$done_at"
start_engine switched
check "全部已切換：收據與執行中的引擎都是 $ENGINE_VERSION" status_is switched "$ENGINE_VERSION" "" "$ENGINE_VERSION"
shoot switched overview
stop_engine

# ---- 一列模糊音總開關與 Emoji 開關，真的 UI → 多鍵原子保存 ----
new_home toggles
printf '{"schema":1,"future":{"x":1}}\n' > "$(base_of toggles)/settings.json"
toggle_value() {
  python3 - "$(settings_of toggles)" "$1" <<'PY'
import json,sys,time
for _ in range(80):
    try: value=json.load(open(sys.argv[1]))
    except FileNotFoundError: time.sleep(.1); continue
    mode=sys.argv[2]
    if mode.startswith('first_'):
        ok=value.get('emoji_equals_first') == (mode=='first_on')
    elif mode.startswith('emoji_'):
        ok=value.get('emoji_shortcut') == ('equals' if mode=='emoji_on' else 'off')
    elif mode.startswith('hint_'):
        ok=value.get('zhuyin_error_hint') == (mode=='hint_on')
    elif mode.startswith('learning_'):
        ok=value.get('personal_learning') == (mode=='learning_on')
    else:
        expected='select' if mode=='fuzzy_on' else 'off'
        # 模糊音預設 select：維持預設的配對不寫入設定檔，關閉時明確寫 off。
        ok=all(value.get('fuzzy',{}).get(pair,'select')==expected for pair in ['z-zh','c-ch','s-sh','en-eng'])
    if ok and value.get('future')=={'x':1}:sys.exit(0)
    time.sleep(.1)
sys.exit(1)
PY
}
# Emoji 在「鍵盤與符號」：鍵盤排列（一組單選）、Ctrl＋標點、標點寬度（一組單選）、自動加空格（一組單選）、補標點 → Emoji（導覽後第 6 個）→ 等號首項（子選項）。
launch toggles keyboard
if window_up; then
  ready
  import -window root "$OUT/toggles-before.png"
  tabs $((NAV + 6)); press space
  check "預設啟用的 Emoji 開關可以關閉，保留未知設定" toggle_value emoji_off
  press space
  check "Emoji 開關可以重新啟用" toggle_value emoji_on
  press Tab space
  check "可以取消第一項的等號，保留未知設定" toggle_value first_off
  press space
  check "可以恢復第一項的等號" toggle_value first_on
  import -window root "$OUT/toggles-on.png"
  press shift+Tab space
  check "Emoji 開關可以關閉" toggle_value emoji_off
  import -window root "$OUT/toggles-off.png"
  # 子選項停用時不在 Tab 順序裡：Tab 跳到下一項標點操作（焦點在已選的那一個，空白鍵不改值、不儲存），不會改到等號首項。
  # 這一步本來就不該儲存，沒有呼叫可等：留 1 秒給可能誤送的儲存，再核對值沒變。
  xdotool key Tab space
  sleep 1
  check "Emoji 關閉時 Tab 跳過停用的子選項，保留等號首項原值" toggle_value first_on
  check "Tab＋空白之後 Emoji 仍是關閉" toggle_value emoji_off
else bad "toggles/keyboard 視窗沒有出現"; cat /tmp/app-toggles-keyboard.log; fi
stop_app
launch toggles keyboard
if window_up; then
  ready
  tabs $((NAV + 6)); press space
  check "停用子選項後仍可重新啟用 Emoji" toggle_value emoji_on
  press Tab space
  check "Emoji 重新啟用後子選項可再次操作" toggle_value first_off
else bad "toggles/keyboard 重新開啟失敗"; cat /tmp/app-toggles-keyboard.log; fi
stop_app
# 「打錯注音時提示」（#300，預設開）在「選字」的不打聲調後面（導覽後第 10 個）；空白鍵關閉、再開。
# 再下一項是「記住我選的字詞」（#297，預設開）；空白鍵關閉、再開。
# 模糊音在「選字」最後：原有 11 項＋7 項選字習慣規則之後（導覽後第 19 個）。
launch toggles candidates
if window_up; then
  ready
  tabs $((NAV + 10)); press space
  check "預設開啟的打錯注音時提示可以關閉，保留未知設定" toggle_value hint_off
  import -window root "$OUT/zhuyin-error-hint-off.png"
  press space
  check "打錯注音時提示可以重新開啟" toggle_value hint_on
  press Tab space
  check "預設開啟的記住我選的字詞可以關閉，保留未知設定" toggle_value learning_off
  import -window root "$OUT/personal-learning-off.png"
  press space
  check "記住我選的字詞可以重新開啟" toggle_value learning_on
  tabs 8; press space
  check "預設開啟的模糊音總開關一次關閉四組，保留未知設定" toggle_value fuzzy_off
  import -window root "$OUT/toggles-fuzzy-off.png"
  press space
  check "單一模糊音開關一次恢復四組 select" toggle_value fuzzy_on
else bad "toggles/candidates 視窗沒有出現"; cat /tmp/app-toggles-candidates.log; fi
stop_app

# GUI 的第一次修改建立新路徑，舊檔原樣保留；後續查詢以新檔為主。
check "設定介面修改後保留舊路徑原檔" python3 - "$(base_of toggles)/settings.json" <<'PY_LEGACY'
import pathlib,sys
sys.exit(0 if pathlib.Path(sys.argv[1]).read_text()=='{"schema":1,"future":{"x":1}}\n' else 1)
PY_LEGACY
run_update toggles settings -json > "$OUT/migrated-settings.json"
check "設定介面修改後查詢以新路徑為主" python3 - "$OUT/migrated-settings.json" "$(settings_of toggles)" <<'PY_MIGRATED'
import json,sys
doc=json.load(open(sys.argv[1]))
sys.exit(0 if doc['file']['path']==sys.argv[2] and 'write_path' not in doc['file'] else 1)
PY_MIGRATED

# ---- 游標前方／後方選字：真 UI 單選保存與重新開啟 ----
new_home cursor
printf '{"schema":1,"future":{"x":1}}\n' > "$(base_of cursor)/settings.json"
cursor_value() {
  python3 - "$(settings_of cursor)" "$1" <<'PY_CURSOR'
import json,sys,time
for _ in range(80):
    try: value=json.load(open(sys.argv[1]))
    except FileNotFoundError: time.sleep(.1); continue
    if value.get('candidate_cursor')==sys.argv[2] and value.get('future')=={'x':1}:sys.exit(0)
    time.sleep(.1)
sys.exit(1)
PY_CURSOR
}
# 「選字」：選字方式、前文、空白選字 → 修改字詞的位置（導覽後第 4 個）。
launch cursor candidates
if window_up; then
  ready
  tabs $((NAV + 4)); press Left
  check "游標前方單選儲存 before，保留未知設定" cursor_value before
  import -window root "$OUT/cursor-before.png"
else bad "cursor/candidates 視窗沒有出現"; fi
stop_app
launch cursor candidates
if window_up; then
  ready
  tabs $((NAV + 4)); press Right
  check "重新開啟設定後可改回游標後方，保留未知設定" cursor_value after
  import -window root "$OUT/cursor-after.png"
else bad "cursor/candidates 重新開啟失敗"; fi
stop_app

# ---- 倚天 41 鍵（#83）：混打與 Emoji 反灰並提示原因；切回標準排列恢復原本的值 ----
new_home eten
printf '{"schema":1,"keyboard_layout":"eten","mixed_input":true,"future":{"x":1}}\n' > "$(base_of eten)/settings.json"
run_update eten settings -json > "$OUT/eten-settings.json"
check "倚天時契約標示混打、Emoji 與不打聲調停用並附原因，值保留" python3 - "$OUT/eten-settings.json" <<'PY_ETEN'
import json,sys
items={x['key']:x for x in json.load(open(sys.argv[1]))['items']}
ok=all(items[k].get('disabled') and items[k].get('disabled_reason')=='倚天 41 鍵時關閉' for k in ('mixed-input','emoji-shortcut','emoji-equals-first','toneless'))
ok=ok and items['mixed-input']['value']=='on' and items['keyboard-layout']['value']=='eten' and not items['space-selects'].get('disabled')
sys.exit(0 if ok else 1)
PY_ETEN
layout_value() {
  python3 - "$(settings_of eten)" "$1" <<'PY_LAYOUT'
import json,sys,time
for _ in range(80):
    try: value=json.load(open(sys.argv[1]))
    except FileNotFoundError: time.sleep(.1); continue
    if value.get('keyboard_layout')==sys.argv[2] and value.get('mixed_input') is True and value.get('future')=={'x':1}:sys.exit(0)
    time.sleep(.1)
sys.exit(1)
PY_LAYOUT
}
# 「中英切換」：停用的混打不在 Tab 順序裡，原因（鍵盤排列）在另一頁，所以下面多一個「到「鍵盤與符號」修改「鍵盤排列」」，
# 是中英切換鍵、單按 Shift 的時限（#319）、開始時的中英模式、切換視窗後的中英模式之後的第 5 個；按了切到鍵盤與符號、焦點在選中的倚天，← 選標準。
launch eten mode
if window_up; then
  ready
  import -window root "$OUT/eten-mode.png"
  tabs $((NAV + 5)); xdotool key space
  sleep 1.5
  import -window root "$OUT/eten-goto-keyboard.png"
  press Left
  check "從混打的說明跳到鍵盤排列，切回標準排列，保留混打原值與未知設定" layout_value standard
  import -window root "$OUT/eten-back-to-standard.png"
  # 鍵盤排列之後是「Ctrl＋標點打全形」（#168），預設開啟；空白鍵關閉。
  press Tab space
  check "Ctrl＋標點開關可關閉" grep -q '"ctrl_punctuation": false' "$(settings_of eten)"
  import -window root "$OUT/ctrl-punctuation-off.png"
  # 再下一項是「標點寬度」（#258、#282），三選一的單選，預設「全形標點」；→ 依序選 Shift＋符號打半形、所有符號鍵打半形，
  # ← 兩次回到全形標點。所有符號鍵打半形時上面的 Ctrl＋標點顯示停用（固定開啟），設定檔裡的 false 不動。
  press Tab Right
  check "標點寬度可選 Shift＋符號打半形" grep -q '"shift_symbol_halfwidth": true' "$(settings_of eten)"
  check "Shift＋符號打半形不是所有符號鍵" grep -q '"all_symbols_halfwidth": false' "$(settings_of eten)"
  import -window root "$OUT/shift-symbol-halfwidth-on.png"
  press Right
  check "標點寬度可選所有符號鍵打半形" grep -q '"all_symbols_halfwidth": true' "$(settings_of eten)"
  check "所有符號鍵打半形不改設定檔的 Ctrl＋標點" grep -q '"ctrl_punctuation": false' "$(settings_of eten)"
  import -window root "$OUT/all-symbols-halfwidth.png"
  press Left
  press Left
  check "標點寬度可選回全形標點" grep -q '"shift_symbol_halfwidth": false' "$(settings_of eten)"
  check "全形標點時所有符號鍵也關" grep -q '"all_symbols_halfwidth": false' "$(settings_of eten)"
  # 再下一項是「自動加空格」（#273、#365），三選一的單選，預設「關閉」；→ 依序選中英文之間、中英文與數字之間，
  # ← 兩次回到關閉。Tab 只占一站，跟上面的標點寬度一樣。
  press Tab Right
  check "自動加空格可選中英文之間" grep -q '"cjk_latin_space": true' "$(settings_of eten)"
  check "中英文之間不補數字" grep -q '"cjk_digit_space": false' "$(settings_of eten)"
  import -window root "$OUT/cjk-latin-space-on.png"
  press Right
  check "自動加空格可選中英文與數字之間" bash -c "grep -q '\"cjk_latin_space\": true' '$(settings_of eten)' && grep -q '\"cjk_digit_space\": true' '$(settings_of eten)'"
  import -window root "$OUT/cjk-latin-space-all.png"
  press Left
  check "← 回到中英文之間，數字那一欄跟著關" bash -c "grep -q '\"cjk_latin_space\": true' '$(settings_of eten)' && grep -q '\"cjk_digit_space\": false' '$(settings_of eten)'"
  press Left
  check "自動加空格可選回關閉" bash -c "grep -q '\"cjk_latin_space\": false' '$(settings_of eten)' && grep -q '\"cjk_digit_space\": false' '$(settings_of eten)'"
  # 再 Tab 就是下一項（提交時補標點）：單選只占一站。空白鍵開啟。
  press Tab space
  check "自動加空格之後一個 Tab 就到補標點" grep -q '"punctuation": true' "$(settings_of eten)"
  press space
  check "補標點關回去" grep -q '"punctuation": false' "$(settings_of eten)"
else bad "eten/mode 視窗沒有出現"; cat /tmp/app-eten-mode.log; fi
stop_app
# 選字鍵在「選字」（一組單選，預設 1234567890，#112）：選字方式、前文、空白選字、修改位置、選字後游標、排列之後第 7 個。
# ↓ 選 1234qweras，再 ↓ 選 asdfghjkl;，按兩次 ↑ 改回。
launch eten candidates
if window_up; then
  ready
  tabs $((NAV + 7)); press Down
  check "選字鍵可保存 1234qweras，保留未知設定" bash -c "grep -q '\"selection_keys\": \"1234qweras\"' '$(settings_of eten)' && grep -q '\"future\"' '$(settings_of eten)'"
  import -window root "$OUT/selection-keys-qwer.png"
  press Down
  check "選字鍵可保存 asdfghjkl;" grep -q '"selection_keys": "asdfghjkl;"' "$(settings_of eten)"
  press Up
  press Up
  check "選字鍵可改回 1234567890" grep -q '"selection_keys": "1234567890"' "$(settings_of eten)"
else bad "eten/candidates 視窗沒有出現"; cat /tmp/app-eten-candidates.log; fi
stop_app
# 「Caps Lock 開著時直接打英文」（#173，Windows、Linux）在「中英切換」最後，預設關閉；已切回標準排列，混打可以按，
# 前面是中英切換鍵、單按 Shift 的時限（#319）、開始模式、切換視窗後、混打、英文補字，第 7 個；空白鍵開啟。
launch eten mode
if window_up; then
  ready
  tabs $((NAV + 7)); press space
  check "Caps Lock 英文開關可開啟" grep -q '"caps_lock_english": true' "$(settings_of eten)"
  import -window root "$OUT/caps-lock-english-on.png"
else bad "eten/mode 重新開啟失敗"; cat /tmp/app-eten-mode.log; fi
stop_app

# ---- 許氏（#183）：中英混打與不打聲調（#150）反灰並提示原因，Emoji 選單照舊；鍵盤排列有「許氏」選項 ----
new_home hsu
printf '{"schema":1,"keyboard_layout":"hsu","mixed_input":true}\n' > "$(base_of hsu)/settings.json"
run_update hsu settings -json > "$OUT/hsu-settings.json"
check "許氏時契約只標示混打與不打聲調停用並附原因，值保留" python3 - "$OUT/hsu-settings.json" <<'PY_HSU'
import json,sys
items={x['key']:x for x in json.load(open(sys.argv[1]))['items']}
disabled=[k for k,x in items.items() if x.get('disabled') and k!='start-mode']
ok=disabled==['mixed-input','toneless'] and items['mixed-input'].get('disabled_reason')=='許氏排列時關閉' and items['mixed-input']['value']=='on'
ok=ok and items['toneless'].get('disabled_reason')=='許氏排列時關閉'  # 不打聲調只在標準排列有效（#150）
ok=ok and items['keyboard-layout']['value']=='hsu' and items['keyboard-layout']['values']==['standard','eten','hsu','pinyin','xiaohe']
ok=ok and items['keyboard-layout'].get('value_labels',{}).get('hsu')=='許氏'
sys.exit(0 if ok else 1)
PY_HSU
launch hsu mode
if window_up; then
  ready
  import -window root "$OUT/hsu-mode.png"
  # 混打反灰、寫「許氏排列時關閉」，下面的「到「鍵盤與符號」修改「鍵盤排列」」切過去，鍵盤排列選中「許氏」。
  # 前面是中英切換鍵、單按 Shift 的時限（#319）、開始模式、切換視窗後。
  tabs $((NAV + 5)); xdotool key Return
  sleep 1.5
  import -window root "$OUT/hsu-goto-keyboard.png"
else bad "hsu/mode 視窗沒有出現"; cat /tmp/app-hsu-mode.log; fi
stop_app
check "許氏時從混打的說明跳過去，沒有改到任何設定" test ! -e "$(settings_of hsu)"
shoot hsu keyboard

# ---- 漢語拼音（#324）：只有中英混打反灰並提示原因，不打聲調照常；鍵盤排列有「漢語拼音」選項 ----
new_home pinyin
printf '{"schema":1,"keyboard_layout":"pinyin","mixed_input":true,"toneless":true}\n' > "$(base_of pinyin)/settings.json"
run_update pinyin settings -json > "$OUT/pinyin-settings.json"
check "漢語拼音時契約只標示混打停用並附原因，值保留，不打聲調照常" python3 - "$OUT/pinyin-settings.json" <<'PY_PINYIN'
import json,sys
items={x['key']:x for x in json.load(open(sys.argv[1]))['items']}
disabled=[k for k,x in items.items() if x.get('disabled') and k!='start-mode']
ok=disabled==['mixed-input'] and items['mixed-input'].get('disabled_reason')=='漢語拼音時關閉' and items['mixed-input']['value']=='on'
ok=ok and items['toneless']['value']=='on' and not items['toneless'].get('disabled')
ok=ok and items['keyboard-layout']['value']=='pinyin' and items['keyboard-layout'].get('value_labels',{}).get('pinyin')=='漢語拼音'
sys.exit(0 if ok else 1)
PY_PINYIN
shoot pinyin keyboard
shoot pinyin mode

# ---- 小鶴雙拼（#370）：同漢語拼音，只有中英混打反灰並提示原因，不打聲調照常；選項提示寫 nihc ----
new_home xiaohe
printf '{"schema":1,"keyboard_layout":"xiaohe","mixed_input":true,"toneless":true}\n' > "$(base_of xiaohe)/settings.json"
run_update xiaohe settings -json > "$OUT/xiaohe-settings.json"
check "小鶴雙拼時契約只標示混打停用並附原因，值保留，不打聲調照常" python3 - "$OUT/xiaohe-settings.json" <<'PY_XIAOHE'
import json,sys
items={x['key']:x for x in json.load(open(sys.argv[1]))['items']}
disabled=[k for k,x in items.items() if x.get('disabled') and k!='start-mode']
ok=disabled==['mixed-input'] and items['mixed-input'].get('disabled_reason')=='小鶴雙拼時關閉' and items['mixed-input']['value']=='on'
ok=ok and items['toneless']['value']=='on' and not items['toneless'].get('disabled')
kl=items['keyboard-layout']
ok=ok and kl['value']=='xiaohe' and kl.get('value_labels',{}).get('xiaohe')=='小鶴雙拼' and 'nihc' in kl.get('value_hints',{}).get('xiaohe','')
sys.exit(0 if ok else 1)
PY_XIAOHE
shoot xiaohe keyboard
shoot xiaohe mode

# ---- 不打聲調（#150）：中英混打反灰並附原因，設定檔的 mixed_input 保留 ----
new_home toneless
printf '{"schema":1,"toneless":true,"mixed_input":true}\n' > "$(base_of toneless)/settings.json"
run_update toneless settings -json > "$OUT/toneless-settings.json"
check "不打聲調時契約標示混打停用，值保留 on" python3 - "$OUT/toneless-settings.json" <<'PY_TONELESS'
import json,sys
items={x['key']:x for x in json.load(open(sys.argv[1]))['items']}
it=items['mixed-input']
sys.exit(0 if it.get('disabled') and it.get('disabled_reason')=='不打聲調時關閉' and it['value']=='on' and items['toneless']['value']=='on' else 1)
PY_TONELESS
# 中英切換頁：混打反灰、寫「不打聲調時關閉」，附到「選字」改「不打聲調也能選字」的連結。
shoot toneless mode
# 「選字」頁：不打聲調排在其他選字項目之後、模糊音之前（導覽後第 9 個）；空白鍵關閉，混打的原值不動。
launch toneless candidates
if window_up; then
  ready
  import -window root "$OUT/toneless-candidates.png"
  tabs $((NAV + 9)); press space
  check "不打聲調開關可關閉，保留混打原值" bash -c "grep -q '\"toneless\": false' '$(settings_of toneless)' && grep -q '\"mixed_input\": true' '$(settings_of toneless)'"
  import -window root "$OUT/toneless-off.png"
else bad "toneless/candidates 視窗沒有出現"; cat /tmp/app-toneless-candidates.log; fi
stop_app

# ---- 中英混打（#288）：混打開著時空白一律輸入空格、↓ 選字，空白鍵選字反灰並附原因，連到「中英切換」；關掉混打就恢復 ----
new_home mixed
printf '{"schema":1,"mixed_input":true,"space_selects":true}\n' > "$(base_of mixed)/settings.json"
run_update mixed settings -json > "$OUT/mixed-settings.json"
check "混打時契約標示空白鍵選字停用、指向混打，值保留 on" python3 - "$OUT/mixed-settings.json" <<'PY_MIXED'
import json,sys
items={x['key']:x for x in json.load(open(sys.argv[1]))['items']}
it=items['space-selects']
ok=it.get('disabled') and it.get('disabled_reason')=='中英混打時關閉，按 ↓ 選字' and it.get('disabled_by')=='mixed-input'
ok=ok and it['value']=='on' and not it.get('effective') and not items['mixed-input'].get('disabled')
sys.exit(0 if ok else 1)
PY_MIXED
# 「選字」頁：空白鍵選字反灰（按不到），它下面的「到「中英切換」修改「中英混打（試用）」」是導覽後第 3 個；
# 按了切到中英切換頁、焦點在混打開關，空白把混打關掉。
launch mixed candidates
if window_up; then
  ready
  import -window root "$OUT/mixed-candidates.png"
  tabs $((NAV + 3)); xdotool key Return
  sleep 1.5
  import -window root "$OUT/mixed-goto-mode.png"
  press space
  check "從空白鍵選字跳到混打並關閉，空白鍵選字原值不動" bash -c "grep -q '\"mixed_input\": false' '$(settings_of mixed)' && grep -q '\"space_selects\": true' '$(settings_of mixed)'"
else bad "mixed/candidates 視窗沒有出現"; cat /tmp/app-mixed-candidates.log; fi
stop_app
run_update mixed settings -json > "$OUT/mixed-off-settings.json"
check "關掉混打後空白鍵選字恢復可改" python3 -c "import json,sys; it=next(x for x in json.load(open(sys.argv[1]))['items'] if x['key']=='space-selects'); sys.exit(1 if it.get('disabled') else 0)" "$OUT/mixed-off-settings.json"
shoot mixed candidates

# ---- 不用 Shift 切換（#184）：開始時的中英模式固定為注音、反灰並附原因，設定檔的 english 保留；改回用 Shift 照原本的英文 ----
new_home noshift
printf '{"schema":1,"toggle_key":"none","start_mode":"english"}\n' > "$(base_of noshift)/settings.json"
run_update noshift settings -json > "$OUT/noshift-settings.json"
check "不用 Shift 時契約標示開始模式停用、實際是注音，值保留 english" python3 - "$OUT/noshift-settings.json" <<'PY_NOSHIFT'
import json,sys
items={x['key']:x for x in json.load(open(sys.argv[1]))['items']}
it=items['start-mode']
sys.exit(0 if it.get('disabled') and it.get('disabled_reason')=='不用 Shift 切換時固定從注音開始' and it['value']=='english' and it.get('effective')=='zhuyin' else 1)
PY_NOSHIFT
noshift_value() { # toggle_key 期望值 → 設定檔寫入，start_mode 仍是 english
  python3 - "$(settings_of noshift)" "$1" <<'PY_NOSHIFT_FILE'
import json,sys,time
for _ in range(80):
    try: value=json.load(open(sys.argv[1]))
    except FileNotFoundError: time.sleep(.1); continue
    if value.get('toggle_key')==sys.argv[2] and value.get('start_mode')=='english':sys.exit(0)
    time.sleep(.1)
sys.exit(1)
PY_NOSHIFT_FILE
}
start_mode_shown() { # 期望畫面：disabled／enabled → 契約與畫面用的欄位
  run_update noshift settings -json | python3 -c "import json,sys; it=next(x for x in json.load(sys.stdin)['items'] if x['key']=='start-mode'); sys.exit(0 if bool(it.get('disabled'))==(sys.argv[1]=='disabled') and it['value']=='english' and (it.get('effective')=='zhuyin')==(sys.argv[1]=='disabled') else 1)" "$1"
}
launch noshift mode
if window_up; then
  ready
  import -window root "$OUT/noshift-mode.png"
  # 第一個是中英切換鍵（焦點在「不用 Shift」），↑ 選左 Shift。
  tabs $((NAV + 1)); press Up
  check "改成左 Shift，開始時的英文照舊保留" noshift_value left
  check "有 Shift 可以切換時開始模式恢復可改、是英文" start_mode_shown enabled
  import -window root "$OUT/noshift-left-shift.png"
  press Down
  check "再改回不用 Shift" noshift_value none
  check "不用 Shift 時開始模式又固定為注音" start_mode_shown disabled
  import -window root "$OUT/noshift-again.png"
  # 原因（中英切換鍵）就在同一頁，開始模式下面不另外放連結：停用的開始模式跳過，下一個就是切換視窗後的中英模式。
  press Tab Down
  check "同一頁的停用原因不多一個連結，Tab 直接到切換視窗後的中英模式" grep -q '"mode_memory": "shared"' "$(settings_of noshift)"
else bad "noshift/mode 視窗沒有出現"; cat /tmp/app-noshift-mode.log; fi
stop_app

# ---- 背景更新持鎖：手動檢查提示進度，輸入設定仍可保存 ----
new_home checking
state_json checking idle ''
printf '%s\n' "$$" > "$(base_of checking)/update/coordinator.lock"
run_update checking check -json >/tmp/checking-result.json
check "背景更新忙碌時，檢查回 in_progress 而非設定 busy" python3 -c 'import json; r=json.load(open("/tmp/checking-result.json")); assert not r["ok"] and r["error"]["code"] == "in_progress"'
check "背景更新期間仍可儲存輸入設定" run_update checking set punctuation off -json
launch checking update
if window_up; then
  ready
  tabs $((NAV + 1)); press space
  import -window root "$OUT/checking-in-progress.png"
  check "背景更新忙碌時設定視窗仍正常執行" kill -0 "$APP_PID"
else bad "checking/update 視窗沒有出現"; cat /tmp/app-checking-update.log; fi
stop_app
rm "$(base_of checking)/update/coordinator.lock"

# ---- 場景 G：0c-update open-settings（完整安裝的副本、核心版本目錄、被竄改）----
sha() { sha256sum "$1" | cut -d' ' -f1; }
# 用行程名稱精確比對（-f 會比對到執行檢查的 bash 自己的命令列）
app_exe() { readlink "/proc/$(pgrep -n -x 0c-settings)/exe" 2>/dev/null; }
kill_apps() { # 結束所有設定程式，等行程與視窗都消失
  pkill -x 0c-settings 2>/dev/null
  for _ in $(seq 50); do pgrep -x 0c-settings >/dev/null || [ -n "$(app_windows)" ] || break; sleep 0.1; done
}

new_home entry
eh=$(home_of entry); eb=$(base_of entry)
mkdir -p "$eh/.local/bin"
install -m 0755 "$SETTINGS_BIN" "$eh/.local/bin/0c-settings"
install -m 0755 "$UPDATE_BIN" "$eh/.local/bin/0c-update"
printf '{"version":"0.1.0-dev.43","files":{".local/bin/0c-settings":"%s"},"active":true}\n' "$(sha "$eh/.local/bin/0c-settings")" > "$eb/install-receipt.json"
state_json entry idle ''
start=$(date +%s%N)
HOME=$eh "$eh/.local/bin/0c-update" open-settings update >/tmp/open1.log 2>&1; code=$?
elapsed_ms=$(( ($(date +%s%N) - start) / 1000000 ))
check "open-settings（完整安裝的副本）結束碼 0" test "$code" -eq 0
check "open-settings 在 5 秒內返回，不等設定程式結束（實際 ${elapsed_ms} 毫秒）" test "$elapsed_ms" -lt 5000
check "設定視窗出現" window_up any
sleep 2; import -window root "$OUT/open-settings-install-update.png"
check "啟動的是完整安裝的副本" test "$(app_exe update)" = "$eh/.local/bin/0c-settings"
check "應用程式選單項目已建立並指向穩定的 0c-update" grep -q "Exec=\"$eh/.local/bin/0c-update\" open-settings" "$eh/.local/share/applications/0c-settings.desktop"
check "項目的 app id 與設定程式向桌面宣告的一致（StartupWMClass）" grep -q "StartupWMClass=0c-settings" "$eh/.local/share/applications/0c-settings.desktop"
check "品牌圖示已寫入使用者的圖示目錄" test -s "$eh/.local/share/icons/hicolor/128x128/apps/0c-settings.png"
kill_apps

# 核心版本目錄的較新版本優先
dir="versions/0.1.0-dev.43-c-aaaaaaaaaaaa"
mkdir -p "$eb/$dir/data"; echo '{}' > "$eb/$dir/data/manifest.json"
install -m 0755 "$SERVER_BIN" "$eb/$dir/0c-server"
install -m 0755 "$SETTINGS_BIN" "$eb/$dir/0c-settings"
printf '{"schema":1,"files":{"0c-settings":"%s"}}\n' "$(sha "$eb/$dir/0c-settings")" > "$eb/$dir/core-files.json"
printf '{"schema":1,"directory":"%s","version":"0.1.0-dev.43"}\n' "$dir" > "$eb/engine.json"
HOME=$eh "$eh/.local/bin/0c-update" open-settings candidates >/tmp/open2.log 2>&1; code=$?
check "open-settings candidates（核心版本目錄）結束碼 0" test "$code" -eq 0
window_up any
check "啟動的是核心版本目錄的較新版本" test "$(app_exe candidates)" = "$eb/$dir/0c-settings"
kill_apps

# 核心目錄的檔案被竄改：拒絕它，退回完整安裝的副本（仍核對過）
echo "tampered" >> "$eb/$dir/0c-settings"
HOME=$eh "$eh/.local/bin/0c-update" open-settings input >/tmp/open3.log 2>&1; code=$?
check "核心目錄被竄改時退回安裝的副本而不是啟動它（舊的頁名 input 照樣接受）" test "$code" -eq 0
window_up any
check "啟動的是安裝副本，不是被竄改的檔案" test "$(app_exe input)" = "$eh/.local/bin/0c-settings"
kill_apps

# 兩邊都被竄改：不啟動，說明原因與退路
echo "tampered" >> "$eh/.local/bin/0c-settings"
HOME=$eh "$eh/.local/bin/0c-update" open-settings >/tmp/open4.log 2>&1; code=$?
check "兩份都核對不過時失敗" test "$code" -ne 0
check "訊息說明核對失敗並指出退路" bash -c "grep -q '無法通過核對' /tmp/open4.log && grep -q '0c-update settings' /tmp/open4.log"
check "沒有啟動任何設定程式" bash -c "! pgrep -x 0c-settings >/dev/null"
sed -i 's/^/ /' /tmp/open4.log; cat /tmp/open4.log

fi # SMOKE_ONLY

# ---- 我的詞彙（#125）----
if [ "${SMOKE_ONLY:-vocab}" = vocab ]; then . "$(dirname "$0")/vocab.sh"; fi

# ---- 模型（#180）----
if [ "${SMOKE_ONLY:-models}" = models ]; then . "$(dirname "$0")/models.sh"; fi

# ---- 輸入習慣（#225）----
if [ "${SMOKE_ONLY:-styles}" = styles ]; then . "$(dirname "$0")/styles.sh"; fi

# ---- 捲動位置（#268）----
if [ "${SMOKE_ONLY:-scroll}" = scroll ]; then . "$(dirname "$0")/scroll.sh"; fi

# ---- 更新頁的「更新方式」下拉選單（#340）----
if [ "${SMOKE_ONLY:-update}" = update ]; then . "$(dirname "$0")/update-mode.sh"; fi

# ---- 清除學習紀錄（#301）----
if [ "${SMOKE_ONLY:-personal}" = personal ]; then . "$(dirname "$0")/personal.sh"; fi

# ---- 選項提示與「說明」連結（#362）----
if [ "${SMOKE_ONLY:-help}" = help ]; then . "$(dirname "$0")/help.sh"; fi

# ---- 設定搜尋（#363）----
if [ "${SMOKE_ONLY:-search}" = search ]; then . "$(dirname "$0")/search.sh"; fi

[ -s "$OUT/xdotool-errors.log" ] && echo "NOTE: 找視窗時 xdotool 出錯 $(wc -l < "$OUT/xdotool-errors.log") 次（BadWindow 已重查；明細在 xdotool-errors.log）"
echo "SMOKE_RESULT=$([ $fail -eq 0 ] && echo PASS || echo FAIL)"
exit $fail
