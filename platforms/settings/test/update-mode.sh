# 更新頁「更新方式」下拉選單（#340）的 UI 測試，由 smoke.sh 載入（共用 launch、check、new_home、state_json 等函式）。
# 用真的 0c-update；選單送 0c-update mode auto|ask|manual -json，只改 update/state.json 的 mode：
#   - 三個選項依序是 自動更新（建議）／下載後詢問／手動更新；用鍵盤（↓／↑）在三者之間互切，每次核對狀態檔。
#   - 打開選單後等幾秒（更新頁每秒重讀狀態），選單不能被背景重畫換掉；選了才改。
#   - 手動更新查到新版（needs_download）：更新頁有「立即下載」，沒有「立即安裝」。
#   - 舊版 0c-update（沒有 features.update_mode）不在這裡測：畫面照舊只列「更新方式」，由 update_mode_test.js 與程式碼審查負責。
# 截圖 umode-*.png（只截視窗；打開的選單是另一個視窗，umode-open.png 截整個畫面）。

umode_is() { python3 -c "import json,sys; sys.exit(0 if json.load(open(sys.argv[1]))['mode']==sys.argv[2] else 1)" "$(base_of "$1")/update/state.json" "$2"; }
ushot() { window_shot "$1"; }
# 沒有待辦時更新頁的控制項依序是「檢查更新」、「更新方式」下拉選單。
UMODE_SELECT=$((NAV + 2))
TS=',"last_check":"2026-11-24T01:00:00Z","last_success":"2026-11-24T01:00:00Z","next_check":"2026-11-24T07:00:00Z"'

new_home umode
state_json umode idle "$TS"
check "status -json 回報 features.update_mode" bash -c "HOME='$(home_of umode)' '$UPDATE_BIN' status -json | python3 -c \"import json,sys; sys.exit(0 if json.load(sys.stdin)['features'].get('update_mode') is True else 1)\""
launch umode update
if window_up; then
  ready
  ushot umode-auto
  tabs $UMODE_SELECT
  press Down
  check "選單 ↓：自動更新 → 下載後詢問（ask）" umode_is umode ask
  ushot umode-ask
  press Down
  check "選單 ↓：下載後詢問 → 手動更新（manual）" umode_is umode manual
  ushot umode-manual
  press Up
  check "選單 ↑：手動更新 → 下載後詢問（ask）" umode_is umode ask
  press Up
  check "選單 ↑：下載後詢問 → 自動更新（auto）" umode_is umode auto
  # 打開選單（WebKitGTK 是空白鍵），等 3 秒（更新頁每秒重讀狀態）。↓ 只移動反白、Esc 取消，方式不變。
  xdotool key space; sleep 3
  import -window root "$OUT/umode-open.png"
  xdotool key Down Escape; sleep 2.5
  check "打開選單 3 秒後按 ↓ 再 Esc：仍是自動更新" umode_is umode auto
  # 再打開、等 3 秒、選第三項。選單有焦點時頁面若照樣每秒重畫，選單元素被換掉，GTK 的選單還浮著，選了卻不會改
  # （2026-11-24 拿掉 holdingUpdateMode 實測：這一項失敗）。
  xdotool key space; sleep 3
  press Down Down Return
  check "打開選單 3 秒後選第三項：手動更新（選單沒有被背景重畫換掉）" umode_is umode manual
  ushot umode-manual-picked
else bad "umode/update 視窗沒有出現"; cat /tmp/app-umode-update.log; fi
stop_app

# 手動更新查到新版：更新頁顯示新版本與「立即下載」，沒有「立即安裝」；截圖看提示與選單一起的樣子。
new_home ufound
state_json ufound found "$TS"',"target":{"version":"0.2.15","kind":"core","url":"https://example.invalid/x","sha256":"'$(printf 'a%.0s' $(seq 64))'","size":123,"found_at":"2026-11-24T01:00:00Z"},"latest":{"version":"0.2.15","eligible":true},"blocked":{"reason":"needs_download","detail":"有新版本，按立即下載","since":"2026-11-24T01:00:00Z"}'
run_update ufound mode manual >/dev/null
check "命令列設成手動更新" umode_is ufound manual
check "status -json：手動更新查到新版的動作是 download" bash -c "HOME='$(home_of ufound)' '$UPDATE_BIN' status -json | python3 -c \"import json,sys; d=json.load(sys.stdin); sys.exit(0 if d.get('action')=='download' and d['update']['stage']=='found' else 1)\""
shoot ufound update
mv "$OUT/ufound-update.png" "$OUT/umode-manual-found.png"

# 下載後詢問、已下載好等你安裝：更新頁有「立即安裝」。
new_home uask
state_json uask prepared "$TS"',"target":{"version":"0.2.15","kind":"core","url":"https://example.invalid/x","sha256":"'$(printf 'a%.0s' $(seq 64))'","size":123,"found_at":"2026-11-24T01:00:00Z"},"latest":{"version":"0.2.15","eligible":true},"blocked":{"reason":"needs_consent","detail":"已下載，等待使用者安裝","since":"2026-11-24T01:00:00Z"}'
run_update uask mode ask >/dev/null
shoot uask update
mv "$OUT/uask-update.png" "$OUT/umode-ask-pending.png"
