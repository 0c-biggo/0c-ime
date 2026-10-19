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
new_home() { rm -rf "$(home_of "$1")"; mkdir -p "$(base_of "$1")/update"; }
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

# SMOKE_ONLY=vocab：只跑「我的詞彙」（vocab.sh），開發時省時間。
if [ "${SMOKE_ONLY:-}" != vocab ]; then

# ---- 場景 A：全新安裝，沒有設定檔也沒有引擎 ----
new_home fresh
for page in overview input update troubleshoot about; do shoot fresh "$page"; done

# ---- 問題排解：匯出狀態摘要到「下載」資料夾，提示寫明寄到哪裡（#152，看截圖） ----
new_home export
mkdir -p "$(home_of export)/Downloads"
launch export troubleshoot
if window_up; then
  sleep 2.5
  # 6 個導覽（含我的詞彙）→ 匯出按鈕（第 7 個）。
  xdotool key --delay 150 Tab Tab Tab Tab Tab Tab Tab space
  sleep 3
  import -window root "$OUT/export-troubleshoot.png"
  check "匯出的狀態摘要寫進「下載」資料夾" bash -c "ls '$(home_of export)'/Downloads/0c-status-*.json >/dev/null 2>&1"
else bad "export/troubleshoot 視窗沒有出現"; cat /tmp/app-export-troubleshoot.log; fi
stop_app

# ---- 場景 B：已調整過、含較新版本才有的欄位 ----
new_home configured
printf '{"schema":1,"selection":"statistical","fuzzy":{"z-zh":"select"},"future":{"x":1}}\n' > "$(base_of configured)/settings.json"
state_json configured idle ',"last_check":"2026-10-06T01:00:00Z","last_success":"2026-10-06T01:00:00Z","next_check":"2026-10-06T07:00:00Z"'
shoot configured input
shoot configured update

# ---- 場景 C：設定檔損毀、場景 D：來自較新版本 ----
new_home corrupt; printf 'not json {{' > "$(base_of corrupt)/settings.json"; shoot corrupt overview
new_home newer; printf '{"schema":9,"selection":"statistical"}\n' > "$(base_of newer)/settings.json"; shoot newer input

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
launch live input
if window_up; then
  sleep 2.5
  import -window root "$OUT/live-input-before.png"
  # 鍵盤路徑：6 個導覽按鈕（含我的詞彙）→ 選字方式（一組單選）→ 「提交時補標點（Beta）」開關，按空白鍵開啟。
  xdotool key --delay 150 Tab Tab Tab Tab Tab Tab Tab Tab space
  sleep 6
  import -window root "$OUT/live-input-after.png"
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
  # 前文、混打之後是獨立的英文自動補字開關。
  xdotool key --delay 150 Tab Tab Tab space
  sleep 3
  check "英文自動補字開關可保存 on" grep -q '"english_autocomplete": true' "$(settings_of live)"
  import -window root "$OUT/english-autocomplete-on.png"
  xdotool key space
  sleep 3
  check "英文自動補字開關可改回 off" grep -q '"english_autocomplete": false' "$(settings_of live)"
  # 下一個是中英切換鍵（一組單選，預設右 Shift，#117）：↓ 選「左右 Shift」，舊欄位 left_shift 跟著寫 true。
  xdotool key Tab Down
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
  # 再下一個是按 Esc 清除整段組字，預設開啟。
  xdotool key Tab space
  sleep 3
  check "Esc 清除整段組字開關可保存 off" grep -q '"esc_clear": false' "$(settings_of live)"
  import -window root "$OUT/esc-clear-off.png"
  xdotool key space
  sleep 3
  check "Esc 清除整段組字開關可改回 on" grep -q '"esc_clear": true' "$(settings_of live)"
  # 再下一個是空白鍵選字，預設開啟（#107、#110）。
  xdotool key Tab space
  sleep 3
  check "空白鍵選字開關可保存 off" grep -q '"space_selects": false' "$(settings_of live)"
  import -window root "$OUT/space-selects-off.png"
  xdotool key space
  sleep 3
  check "空白鍵選字開關可改回 on" grep -q '"space_selects": true' "$(settings_of live)"
  # 修改字詞的位置（一組單選）之後是選字後游標移到下一個字，預設關閉（#133）。
  xdotool key Tab Tab space
  sleep 3
  check "選字後游標開關可保存 on" grep -q '"advance_after_select": true' "$(settings_of live)"
  import -window root "$OUT/advance-after-select-on.png"
  xdotool key space
  sleep 3
  check "選字後游標開關可改回 off" grep -q '"advance_after_select": false' "$(settings_of live)"
else bad "live/input 視窗沒有出現"; cat /tmp/app-live-input.log; fi
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
launch toggles input
if window_up; then
  sleep 2.5
  import -window root "$OUT/toggles-before.png"
  # 6 個導覽（含我的詞彙）、選字方式、補標點、前文、混打、英文補字、中英切換鍵、開始時的中英模式、切換視窗後的中英模式、Esc、空白選字、修改位置、選字後游標、排列 → Emoji（第20個）。
  xdotool key --delay 100 Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab space
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
  xdotool key Tab space
  check "預設開啟的模糊音總開關一次關閉四組，保留未知設定" toggle_value fuzzy_off
  sleep 1
  xdotool key space
  check "單一模糊音開關一次恢復四組 select" toggle_value fuzzy_on
  sleep 1
  import -window root "$OUT/toggles-on.png"
  xdotool key shift+Tab shift+Tab space
  check "Emoji 開關可以關閉" toggle_value emoji_off
  sleep 1
  import -window root "$OUT/toggles-off.png"
  xdotool key Tab space
  check "Emoji 關閉時 Tab 跳過停用的子選項，直接操作模糊音" toggle_value fuzzy_off
  check "關閉 Emoji 保留等號首項原值" toggle_value first_on
  sleep 1
  xdotool key space
  check "跳過子選項後可正常重新開啟模糊音" toggle_value fuzzy_on
  sleep 1
  xdotool key shift+Tab space
  check "停用子選項後仍可重新啟用 Emoji" toggle_value emoji_on
  sleep 1
  xdotool key Tab space
  check "Emoji 重新啟用後子選項可再次操作" toggle_value first_off
else bad "toggles/input 視窗沒有出現"; cat /tmp/app-toggles-input.log; fi
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
launch cursor input
if window_up; then
  sleep 2.5
  # 6 個導覽（含我的詞彙）、選字方式、補標點、前文、混打、英文補字、中英切換鍵、開始時的中英模式、切換視窗後的中英模式、Esc、空白選字 → 修改字詞的位置（第17個）。
  xdotool key --delay 100 Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Left
  check "游標前方單選儲存 before，保留未知設定" cursor_value before
  sleep 1
  import -window root "$OUT/cursor-before.png"
else bad "cursor/input 視窗沒有出現"; fi
stop_app
launch cursor input
if window_up; then
  sleep 2.5
  xdotool key --delay 100 Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Right
  check "重新開啟設定後可改回游標後方，保留未知設定" cursor_value after
  sleep 1
  import -window root "$OUT/cursor-after.png"
else bad "cursor/input 重新開啟失敗"; fi
stop_app

# ---- 倚天 41 鍵（#83）：混打與 Emoji 反灰並提示原因；切回標準排列恢復原本的值 ----
new_home eten
printf '{"schema":1,"keyboard_layout":"eten","mixed_input":true,"future":{"x":1}}\n' > "$(base_of eten)/settings.json"
run_update eten settings -json > "$OUT/eten-settings.json"
check "倚天時契約標示混打與 Emoji 停用並附原因，值保留" python3 - "$OUT/eten-settings.json" <<'PY_ETEN'
import json,sys
items={x['key']:x for x in json.load(open(sys.argv[1]))['items']}
ok=all(items[k].get('disabled') and items[k].get('disabled_reason')=='倚天 41 鍵時關閉' for k in ('mixed-input','emoji-shortcut','emoji-equals-first'))
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
launch eten input
if window_up; then
  sleep 2.5
  import -window root "$OUT/eten-input.png"
  # 停用的混打、Emoji、等號首項不在 Tab 順序裡：6 個導覽（含我的詞彙）、選字方式、補標點、前文、英文補字、中英切換鍵、開始時的中英模式、
  # 切換視窗後的中英模式、Esc、空白選字、修改位置、選字後游標、排列、模糊音 → 鍵盤排列（第20個），← 選標準。
  xdotool key --delay 100 Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab Tab
  sleep 1
  import -window root "$OUT/eten-input-keyboard.png"
  xdotool key Left
  check "切回標準排列，保留混打原值與未知設定" layout_value standard
  sleep 2
  import -window root "$OUT/eten-back-to-standard.png"
else bad "eten/input 視窗沒有出現"; cat /tmp/app-eten-input.log; fi
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
  xdotool key --delay 100 Tab Tab Tab Tab Tab Tab Tab space
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
HOME=$eh "$eh/.local/bin/0c-update" open-settings overview >/tmp/open2.log 2>&1; code=$?
check "open-settings（核心版本目錄）結束碼 0" test "$code" -eq 0
window_up; sleep 1
check "啟動的是核心版本目錄的較新版本" test "$(app_exe overview)" = "$eb/$dir/0c-settings"
kill_apps

# 核心目錄的檔案被竄改：拒絕它，退回完整安裝的副本（仍核對過）
echo "tampered" >> "$eb/$dir/0c-settings"
HOME=$eh "$eh/.local/bin/0c-update" open-settings input >/tmp/open3.log 2>&1; code=$?
check "核心目錄被竄改時退回安裝的副本而不是啟動它" test "$code" -eq 0
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
. "$(dirname "$0")/vocab.sh"

echo "SMOKE_RESULT=$([ $fail -eq 0 ] && echo PASS || echo FAIL)"
exit $fail
