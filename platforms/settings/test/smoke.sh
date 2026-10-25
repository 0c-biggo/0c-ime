#!/bin/bash
# 設定程式的冒煙測試：在 Xvfb 裡執行真的 0c-settings、0c-update 與（用 testdata/toy 的）0c-server，
# 逐頁截圖，並用鍵盤實際切換一個開關，核對「畫面 → Rust → 0c-update → settings.json → 引擎交接」整條路徑。
# 由 scripts/test_settings_ui.py 在建置容器內執行；需要 SETTINGS_BIN、UPDATE_BIN、SERVER_BIN、DATA、OUT。
set -u
fail=0
ok()   { echo "PASS: $1"; }
bad()  { echo "FAIL: $1"; fail=1; }
check() { local desc=$1; shift; if "$@"; then ok "$desc"; else bad "$desc"; fi }

export LANG=C.UTF-8 LC_ALL=C.UTF-8   # xdotool 要在 UTF-8 locale 下才能比對中文視窗標題
export XDG_RUNTIME_DIR=/tmp/xdg-runtime
mkdir -p -m 700 "$XDG_RUNTIME_DIR" "$OUT"
Xvfb :99 -screen 0 1100x800x24 >/tmp/xvfb.log 2>&1 &
export DISPLAY=:99
for _ in $(seq 50); do xdpyinfo >/dev/null 2>&1 && break; sleep 0.2; done
eval "$(dbus-launch --sh-syntax)"

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

launch() { # 場景 頁面 → 啟動設定程式（背景）；設定 APP_PID
  local name=$1 page=$2
  HOME=$(home_of "$name") ZC_UPDATER=$UPDATE_BIN "$SETTINGS_BIN" --page "$page" >"/tmp/app-$name-$page.log" 2>&1 &
  APP_PID=$!
}
window_up() { timeout 30 xdotool search --sync --onlyvisible --name '靈犀注音設定' >/dev/null 2>&1; }
stop_app() { kill "$APP_PID" 2>/dev/null; wait "$APP_PID" 2>/dev/null; }
shoot() { # 場景 頁面
  local name=$1 page=$2
  launch "$name" "$page"
  if window_up; then sleep 2.5; import -window root "$OUT/$name-$page.png"; ok "$name/$page 視窗出現並截圖"
  else bad "$name/$page 視窗沒有出現"; cat "/tmp/app-$name-$page.log"; fi
  if kill -0 "$APP_PID" 2>/dev/null; then ok "$name/$page 程式仍在執行"; else bad "$name/$page 程式提早結束"; cat "/tmp/app-$name-$page.log"; fi
  stop_app
}
run_update() { local name=$1; shift; HOME=$(home_of "$name") "$UPDATE_BIN" "$@"; }
# 側欄的導覽按鈕數（總覽、輸入習慣、中英切換、選字、鍵盤與符號、輸出設定、我的詞彙、模型、更新、問題排解、關於；#207）。
# 每一頁的第一個控制項是第 NAV+1 個 Tab；vocab.sh、models.sh 也用它。
NAV=11
tabs() { local n=$1; local keys=(); for _ in $(seq "$n"); do keys+=(Tab); done; xdotool key --delay 120 "${keys[@]}"; }

# SMOKE_ONLY=vocab／models：只跑「我的詞彙」（vocab.sh）或「模型」（models.sh），開發時省時間。
if [ -z "${SMOKE_ONLY:-}" ]; then

# ---- 場景 A：全新安裝，沒有設定檔也沒有引擎 ----
new_home fresh
rm "$(base_of fresh)/settings.json"
for page in styles overview mode candidates keyboard output update troubleshoot about; do shoot fresh "$page"; done

# ---- 舊的頁名 input（0.2.5 以前的「輸入設定」，#207）導到「中英切換」：第一個控制項是中英切換鍵 ----
new_home alias
launch alias input
if window_up; then
  sleep 2.5
  import -window root "$OUT/alias-input.png"
  tabs $((NAV + 1)); xdotool key Down
  sleep 3
  check "--page input 開在中英切換（第一項中英切換鍵可改成左右 Shift）" grep -q '"toggle_key": "both"' "$(settings_of alias)"
else bad "alias/input 視窗沒有出現"; cat /tmp/app-alias-input.log; fi
stop_app

# ---- 問題排解：匯出狀態摘要到「下載」資料夾，提示寫明寄到哪裡（#152，看截圖） ----
new_home export
mkdir -p "$(home_of export)/Downloads"
launch export troubleshoot
if window_up; then
  sleep 2.5
  # 導覽之後第一個是匯出按鈕。
  tabs $((NAV + 1)); xdotool key space
  sleep 3
  import -window root "$OUT/export-troubleshoot.png"
  check "匯出的狀態摘要寫進「下載」資料夾" bash -c "ls '$(home_of export)'/Downloads/0c-status-*.json >/dev/null 2>&1"
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
# 鍵盤路徑：「鍵盤與符號」的鍵盤排列（一組單選）、Ctrl＋標點 → 「提交時補標點（Beta）」開關，按空白鍵開啟。
launch live keyboard
if window_up; then
  sleep 2.5
  import -window root "$OUT/live-keyboard-before.png"
  tabs $((NAV + 3)); xdotool key space
  sleep 6
  import -window root "$OUT/live-keyboard-after.png"
  check "自行開啟後新路徑 settings.json 的 punctuation 為 true" grep -q '"punctuation": true' "$(settings_of live)"
  check "套用後引擎已交接退出（下一個按鍵才以新設定重新啟動）" bash -c "! [ -S '$XDG_RUNTIME_DIR/0c/0c.sock' ] || ! python3 - <<'PY'
import socket,sys
s=socket.socket(socket.AF_UNIX); 
try: s.connect('$XDG_RUNTIME_DIR/0c/0c.sock'); sys.exit(0)
except OSError: sys.exit(1)
PY"
  xdotool key space
  sleep 3
  check "再按一次可關閉補標點" grep -q '"punctuation": false' "$(settings_of live)"
else bad "live/keyboard 視窗沒有出現"; cat /tmp/app-live-keyboard.log; fi
stop_app
# 「中英切換」：中英切換鍵、開始時的中英模式、切換視窗後的中英模式、中英混打、英文模式自動補字。
launch live mode
if window_up; then
  sleep 2.5
  # 第一個是中英切換鍵（一組單選，預設右 Shift，#117）：↓ 選「左右 Shift」，舊欄位 left_shift 跟著寫 true。
  tabs $((NAV + 1)); xdotool key Down
  sleep 3
  check "中英切換鍵可保存左右 Shift" bash -c "grep -q '\"toggle_key\": \"both\"' '$(settings_of live)' && grep -q '\"left_shift\": true' '$(settings_of live)'"
  import -window root "$OUT/toggle-key-both.png"
  xdotool key Up
  sleep 3
  check "中英切換鍵可改回右 Shift" bash -c "grep -q '\"toggle_key\": \"right\"' '$(settings_of live)' && grep -q '\"left_shift\": false' '$(settings_of live)'"
  # 再下一個是開始時的中英模式（一組單選，預設注音，#160）：↓ 選「英文」；Linux 的「切換視窗後」native 名稱跟著變成「每次回到英文」。
  xdotool key Tab Down
  sleep 3
  check "開始時的中英模式可保存 english" grep -q '"start_mode": "english"' "$(settings_of live)"
  check "native 的名稱跟著開始時的模式" bash -c "'$UPDATE_BIN' settings -json | python3 -c \"import json,sys; it=next(x for x in json.load(sys.stdin)['items'] if x['key']=='mode-memory'); sys.exit(0 if it['value_labels']['native']=='每次回到英文' else 1)\""
  import -window root "$OUT/start-mode-english.png"
  xdotool key Up
  sleep 3
  check "開始時的中英模式可改回 zhuyin" grep -q '"start_mode": "zhuyin"' "$(settings_of live)"
  # 再下一個是切換視窗後的中英模式（一組單選，Linux 預設 native，#115）：↓ 選「全部共用同一個模式」。
  xdotool key Tab Down
  sleep 3
  check "切換視窗後的中英模式可保存 shared" grep -q '"mode_memory": "shared"' "$(settings_of live)"
  import -window root "$OUT/mode-memory-shared.png"
  xdotool key Up
  sleep 3
  check "切換視窗後的中英模式可改回 native" grep -q '"mode_memory": "native"' "$(settings_of live)"
  # 混打之後是獨立的英文自動補字開關。
  xdotool key --delay 150 Tab Tab space
  sleep 3
  check "英文自動補字開關可保存 on" grep -q '"english_autocomplete": true' "$(settings_of live)"
  import -window root "$OUT/english-autocomplete-on.png"
  xdotool key space
  sleep 3
  check "英文自動補字開關可改回 off" grep -q '"english_autocomplete": false' "$(settings_of live)"
else bad "live/mode 視窗沒有出現"; cat /tmp/app-live-mode.log; fi
stop_app
# 「選字」：選字方式（一組單選）、依前文選字 → 空白鍵選字、修改字詞的位置 → 選字後游標、選字排列、選字鍵 → Esc。
launch live candidates
if window_up; then
  sleep 2.5
  import -window root "$OUT/live-candidates.png"
  # 空白鍵選字，預設開啟（#107、#110）。
  tabs $((NAV + 3)); xdotool key space
  sleep 3
  check "空白鍵選字開關可保存 off" grep -q '"space_selects": false' "$(settings_of live)"
  import -window root "$OUT/space-selects-off.png"
  xdotool key space
  sleep 3
  check "空白鍵選字開關可改回 on" grep -q '"space_selects": true' "$(settings_of live)"
  # 修改字詞的位置（一組單選）之後是選字後游標移到下一個字，預設關閉（#133）。
  xdotool key --delay 150 Tab Tab space
  sleep 3
  check "選字後游標開關可保存 on" grep -q '"advance_after_select": true' "$(settings_of live)"
  import -window root "$OUT/advance-after-select-on.png"
  xdotool key space
  sleep 3
  check "選字後游標開關可改回 off" grep -q '"advance_after_select": false' "$(settings_of live)"
  # 選字排列、選字鍵（兩組單選）之後是按 Esc 清除整段組字，預設開啟。
  xdotool key --delay 150 Tab Tab Tab space
  sleep 3
  check "Esc 清除整段組字開關可保存 off" grep -q '"esc_clear": false' "$(settings_of live)"
  import -window root "$OUT/esc-clear-off.png"
  xdotool key space
  sleep 3
  check "Esc 清除整段組字開關可改回 on" grep -q '"esc_clear": true' "$(settings_of live)"
else bad "live/candidates 視窗沒有出現"; cat /tmp/app-live-candidates.log; fi
# 單一實例：再啟動一次，不該多出視窗，也不該卡住。
sleep 1
HOME=$live_home ZC_UPDATER=$UPDATE_BIN timeout 10 "$SETTINGS_BIN" --page update >/tmp/second.log 2>&1
second=$?
check "第二個實例很快結束（單一實例）" test "$second" -eq 0
check "仍然只有一個視窗" test "$(xdotool search --onlyvisible --name '靈犀注音設定' | wc -l)" -eq 1
sleep 1.5
import -window root "$OUT/live-second-instance.png"
stop_app
unset HOME

# 輸出設定是左側獨立一頁（#118）：導覽之後第一個是輸出簡體字開關，預設關閉。
launch live output
if window_up; then
  sleep 2.5
  tabs $((NAV + 1)); xdotool key space
  sleep 3
  check "輸出簡體字開關可保存 on" grep -q '"output_simplified": true' "$(settings_of live)"
  import -window root "$OUT/output-simplified-on.png"
  xdotool key space
  sleep 3
  check "輸出簡體字開關可改回 off" grep -q '"output_simplified": false' "$(settings_of live)"
  # 下一個是「用快捷鍵切換簡體字」（#192），預設關閉，說明寫出快捷鍵。
  xdotool key --delay 150 Tab space
  sleep 3
  check "快捷鍵開關可保存 on" grep -q '"simplified_hotkey": true' "$(settings_of live)"
  import -window root "$OUT/simplified-hotkey-on.png"
  xdotool key space
  sleep 3
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
  sleep 2.5
  tabs $((NAV + 1)); xdotool key space
  sleep 3
  check "輸出簡體字：引擎在執行時開啟，寫入設定" grep -q '"output_simplified": true' "$(settings_of outputlive)"
  check "輸出簡體字：引擎沒有重啟（同一個行程）" kill -0 "$ENGINE_PID"
  check "輸出簡體字：引擎立即回報開啟" live_runtime on
  import -window root "$OUT/output-simplified-live-on.png"
  xdotool key space
  sleep 3
  check "輸出簡體字：即時關閉，寫入設定" grep -q '"output_simplified": false' "$(settings_of outputlive)"
  check "輸出簡體字：關閉後引擎仍是同一個行程" kill -0 "$ENGINE_PID"
  check "輸出簡體字：引擎立即回報關閉" live_runtime off
  # #192：「用快捷鍵切換簡體字」要交接重啟引擎（外殼看新引擎回報的 simplified_hotkey），下一個按鍵起生效。
  # 引擎在執行時交接；之後引擎沒在執行（容器裡沒有常駐引擎），外殼下一個按鍵啟動引擎時讀新設定，提示一樣。
  xdotool key --delay 150 Tab space
  sleep 3
  check "快捷鍵：引擎在執行時開啟，寫入設定" grep -q '"simplified_hotkey": true' "$(settings_of outputlive)"
  check "快捷鍵：開啟時交接，舊引擎已結束" bash -c "! kill -0 $ENGINE_PID 2>/dev/null"
  import -window root "$OUT/simplified-hotkey-live-on.png"
  xdotool key space
  sleep 3
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
    else:
        expected='select' if mode=='fuzzy_on' else 'off'
        # 模糊音預設 select：維持預設的配對不寫入設定檔，關閉時明確寫 off。
        ok=all(value.get('fuzzy',{}).get(pair,'select')==expected for pair in ['z-zh','c-ch','s-sh','en-eng'])
    if ok and value.get('future')=={'x':1}:sys.exit(0)
    time.sleep(.1)
sys.exit(1)
PY
}
# Emoji 在「鍵盤與符號」：鍵盤排列（一組單選）、Ctrl＋標點、補標點 → Emoji（導覽後第 4 個）→ 等號首項（子選項）。
launch toggles keyboard
if window_up; then
  sleep 2.5
  import -window root "$OUT/toggles-before.png"
  tabs $((NAV + 4)); xdotool key space
  check "預設啟用的 Emoji 開關可以關閉，保留未知設定" toggle_value emoji_off
  sleep 1
  xdotool key space
  check "Emoji 開關可以重新啟用" toggle_value emoji_on
  sleep 1
  xdotool key Tab space
  check "可以取消第一項的等號，保留未知設定" toggle_value first_off
  sleep 1
  xdotool key space
  check "可以恢復第一項的等號" toggle_value first_on
  sleep 1
  import -window root "$OUT/toggles-on.png"
  xdotool key shift+Tab space
  check "Emoji 開關可以關閉" toggle_value emoji_off
  sleep 1
  import -window root "$OUT/toggles-off.png"
  # 子選項停用時不在 Tab 順序裡：Tab 跳到下一項符號習慣，不會改到等號首項。
  xdotool key Tab space
  sleep 1
  check "Emoji 關閉時 Tab 跳過停用的子選項，保留等號首項原值" toggle_value first_on
  check "Tab＋空白之後 Emoji 仍是關閉" toggle_value emoji_off
else bad "toggles/keyboard 視窗沒有出現"; cat /tmp/app-toggles-keyboard.log; fi
stop_app
launch toggles keyboard
if window_up; then
  sleep 2.5
  tabs $((NAV + 4)); xdotool key space
  check "停用子選項後仍可重新啟用 Emoji" toggle_value emoji_on
  sleep 1
  xdotool key Tab space
  check "Emoji 重新啟用後子選項可再次操作" toggle_value first_off
else bad "toggles/keyboard 重新開啟失敗"; cat /tmp/app-toggles-keyboard.log; fi
stop_app
# 模糊音在「選字」最後：原有 9 項＋7 項選字習慣規則之後（導覽後第 17 個）。
launch toggles candidates
if window_up; then
  sleep 2.5
  tabs $((NAV + 17)); xdotool key space
  check "預設開啟的模糊音總開關一次關閉四組，保留未知設定" toggle_value fuzzy_off
  sleep 1
  import -window root "$OUT/toggles-fuzzy-off.png"
  xdotool key space
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
  sleep 2.5
  tabs $((NAV + 4)); xdotool key Left
  check "游標前方單選儲存 before，保留未知設定" cursor_value before
  sleep 1
  import -window root "$OUT/cursor-before.png"
else bad "cursor/candidates 視窗沒有出現"; fi
stop_app
launch cursor candidates
if window_up; then
  sleep 2.5
  tabs $((NAV + 4)); xdotool key Right
  check "重新開啟設定後可改回游標後方，保留未知設定" cursor_value after
  sleep 1
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
# 是中英切換鍵、開始時的中英模式、切換視窗後的中英模式之後的第 4 個；按了切到鍵盤與符號、焦點在選中的倚天，← 選標準。
launch eten mode
if window_up; then
  sleep 2.5
  import -window root "$OUT/eten-mode.png"
  tabs $((NAV + 4)); xdotool key space
  sleep 1.5
  import -window root "$OUT/eten-goto-keyboard.png"
  xdotool key Left
  check "從混打的說明跳到鍵盤排列，切回標準排列，保留混打原值與未知設定" layout_value standard
  sleep 2
  import -window root "$OUT/eten-back-to-standard.png"
  # 鍵盤排列之後是「Ctrl＋標點打全形」（#168），預設開啟；空白鍵關閉。
  xdotool key Tab space
  sleep 3
  check "Ctrl＋標點開關可關閉" grep -q '"ctrl_punctuation": false' "$(settings_of eten)"
  import -window root "$OUT/ctrl-punctuation-off.png"
else bad "eten/mode 視窗沒有出現"; cat /tmp/app-eten-mode.log; fi
stop_app
# 選字鍵在「選字」（一組單選，預設 1234567890，#112）：選字方式、前文、空白選字、修改位置、選字後游標、排列之後第 7 個。
# ↓ 選 1234qweras，再 ↓ 選 asdfghjkl;，按兩次 ↑ 改回。
launch eten candidates
if window_up; then
  sleep 2.5
  tabs $((NAV + 7)); xdotool key Down
  sleep 3
  check "選字鍵可保存 1234qweras，保留未知設定" bash -c "grep -q '\"selection_keys\": \"1234qweras\"' '$(settings_of eten)' && grep -q '\"future\"' '$(settings_of eten)'"
  import -window root "$OUT/selection-keys-qwer.png"
  xdotool key Down
  sleep 3
  check "選字鍵可保存 asdfghjkl;" grep -q '"selection_keys": "asdfghjkl;"' "$(settings_of eten)"
  xdotool key Up
  sleep 3
  xdotool key Up
  sleep 3
  check "選字鍵可改回 1234567890" grep -q '"selection_keys": "1234567890"' "$(settings_of eten)"
else bad "eten/candidates 視窗沒有出現"; cat /tmp/app-eten-candidates.log; fi
stop_app
# 「Caps Lock 開著時直接打英文」（#173，Windows、Linux）在「中英切換」最後，預設關閉；已切回標準排列，混打可以按，
# 前面是中英切換鍵、開始模式、切換視窗後、混打、英文補字，第 6 個；空白鍵開啟。
launch eten mode
if window_up; then
  sleep 2.5
  tabs $((NAV + 6)); xdotool key space
  sleep 3
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
ok=ok and items['keyboard-layout']['value']=='hsu' and items['keyboard-layout']['values']==['standard','eten','hsu']
ok=ok and items['keyboard-layout'].get('value_labels',{}).get('hsu')=='許氏'
sys.exit(0 if ok else 1)
PY_HSU
launch hsu mode
if window_up; then
  sleep 2.5
  import -window root "$OUT/hsu-mode.png"
  # 混打反灰、寫「許氏排列時關閉」，下面的「到「鍵盤與符號」修改「鍵盤排列」」切過去，鍵盤排列選中「許氏」。
  tabs $((NAV + 4)); xdotool key Return
  sleep 1.5
  import -window root "$OUT/hsu-goto-keyboard.png"
else bad "hsu/mode 視窗沒有出現"; cat /tmp/app-hsu-mode.log; fi
stop_app
check "許氏時從混打的說明跳過去，沒有改到任何設定" test ! -e "$(settings_of hsu)"
shoot hsu keyboard

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
  sleep 2.5
  import -window root "$OUT/toneless-candidates.png"
  tabs $((NAV + 9)); xdotool key space
  sleep 3
  check "不打聲調開關可關閉，保留混打原值" bash -c "grep -q '\"toneless\": false' '$(settings_of toneless)' && grep -q '\"mixed_input\": true' '$(settings_of toneless)'"
  import -window root "$OUT/toneless-off.png"
else bad "toneless/candidates 視窗沒有出現"; cat /tmp/app-toneless-candidates.log; fi
stop_app

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
  sleep 2.5
  import -window root "$OUT/noshift-mode.png"
  # 第一個是中英切換鍵（焦點在「不用 Shift」），↑ 選左 Shift。
  tabs $((NAV + 1)); xdotool key Up
  check "改成左 Shift，開始時的英文照舊保留" noshift_value left
  sleep 2
  check "有 Shift 可以切換時開始模式恢復可改、是英文" start_mode_shown enabled
  import -window root "$OUT/noshift-left-shift.png"
  xdotool key Down
  check "再改回不用 Shift" noshift_value none
  sleep 2
  check "不用 Shift 時開始模式又固定為注音" start_mode_shown disabled
  import -window root "$OUT/noshift-again.png"
  # 原因（中英切換鍵）就在同一頁，開始模式下面不另外放連結：停用的開始模式跳過，下一個就是切換視窗後的中英模式。
  xdotool key Tab Down
  sleep 3
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
  sleep 2.5
  tabs $((NAV + 1)); xdotool key space
  sleep 3
  import -window root "$OUT/checking-in-progress.png"
  check "背景更新忙碌時設定視窗仍正常執行" kill -0 "$APP_PID"
else bad "checking/update 視窗沒有出現"; cat /tmp/app-checking-update.log; fi
stop_app
rm "$(base_of checking)/update/coordinator.lock"

# ---- 場景 G：0c-update open-settings（完整安裝的副本、核心版本目錄、被竄改）----
sha() { sha256sum "$1" | cut -d' ' -f1; }
# 用行程名稱精確比對（-f 會比對到執行檢查的 bash 自己的命令列）
app_exe() { readlink "/proc/$(pgrep -n -x 0c-settings)/exe" 2>/dev/null; }
kill_apps() { pkill -x 0c-settings 2>/dev/null; sleep 1; }

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
check "設定視窗出現" window_up
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
window_up; sleep 1
check "啟動的是核心版本目錄的較新版本" test "$(app_exe candidates)" = "$eb/$dir/0c-settings"
kill_apps

# 核心目錄的檔案被竄改：拒絕它，退回完整安裝的副本（仍核對過）
echo "tampered" >> "$eb/$dir/0c-settings"
HOME=$eh "$eh/.local/bin/0c-update" open-settings input >/tmp/open3.log 2>&1; code=$?
check "核心目錄被竄改時退回安裝的副本而不是啟動它（舊的頁名 input 照樣接受）" test "$code" -eq 0
window_up; sleep 1
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

echo "SMOKE_RESULT=$([ $fail -eq 0 ] && echo PASS || echo FAIL)"
exit $fail
