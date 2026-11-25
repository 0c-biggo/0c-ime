#!/bin/bash
# 由 smoke.sh 載入；使用真正的 Go profiles 契約與 Rust 橋接，與舊設定逐項編輯的情境分開。
new_home styles
rm "$(base_of styles)/settings.json"
run_update styles settings -json > "$OUT/styles-fresh.json"
check "新安裝使用固定內建習慣，鍵盤排列保持獨立" python3 - "$OUT/styles-fresh.json" <<'PY'
import json, sys
s = json.load(open(sys.argv[1]))
p = s['profiles']
a = next(x for x in p['items'] if x['id'] == p['active'])
assert a['builtin']
assert 'keyboard-layout' not in a['values']
assert not next(x for x in s['items'] if x['key'] == 'keyboard-layout').get('locked', False)
assert any(x.get('locked') and x['value'] not in ('off', '') for x in s['items'])
assert len([x for x in p['items'] if x['builtin']]) >= 5
PY
launch styles styles
if window_up; then
  sleep 2.5
  import -window root "$OUT/styles-fresh.png"
  tabs $((NAV + 1))
  xdotool key Home Down
  sleep 1
  run_update styles settings -json > "$OUT/styles-preview.json"
  check "選用風格先預覽，尚未按套用不寫入" python3 - "$OUT/styles-fresh.json" "$OUT/styles-preview.json" <<'PY'
import json, sys
assert json.load(open(sys.argv[1]))['profiles']['active'] == json.load(open(sys.argv[2]))['profiles']['active']
PY
  import -window root "$OUT/styles-preview.png"
  xdotool key Tab Tab space
  sleep 3
  run_update styles settings -json > "$OUT/styles-selected.json"
  check "按套用透過 Rust 橋接切換整份風格" python3 - "$OUT/styles-fresh.json" "$OUT/styles-selected.json" <<'PY'
import json, sys
before, after = [json.load(open(p)) for p in sys.argv[1:]]
assert before['profiles']['active'] != after['profiles']['active']
assert next(x for x in after['profiles']['items'] if x['id'] == after['profiles']['active'])['builtin']
PY
else bad "輸入習慣頁沒有出現"; fi
stop_app
shoot styles mode
launch styles styles
if window_up; then
  sleep 2.5
  # 選擇框、鍵盤排列連結、複製；目前選用按鈕為 disabled，不占 Tab。
  tabs $((NAV + 3)); xdotool key space
  sleep 0.5
  xdotool key ctrl+a
  xdotool type --clearmodifiers 'My Microsoft style'
  xdotool key Return
  sleep 3
  run_update styles settings -json > "$OUT/styles-copy.json"
  check "命名複製會建立自訂並啟用，解除相關設定鎖定" python3 - "$OUT/styles-copy.json" <<'PY'
import json, sys
s = json.load(open(sys.argv[1]))
p = s['profiles']
a = next(x for x in p['items'] if x['id'] == p['active'])
assert not a['builtin'] and a['name'] == 'My Microsoft style'
assert not any(x.get('locked') for x in s['items'])
PY
  # #233：逐條規則判斷相容性。微軟習慣的 Shift＋空白全半形停用混打、標點停用聲調選填，英文補全照開；原因指向那一條規則。
  check "自訂習慣只停用衝突的功能並指出是哪一條規則" python3 - "$OUT/styles-copy.json" <<'PY_CONFLICT'
import json, sys
s = json.load(open(sys.argv[1]))
items = {x['key']: x for x in s['items']}
assert items['style-shift-space']['value'] == 'fullwidth', items['style-shift-space']
assert items['mixed-input'].get('disabled_by') == 'style-shift-space', items['mixed-input']
assert 'Shift＋空白' in items['mixed-input']['disabled_reason'], items['mixed-input']
assert items['toneless'].get('disabled_by') == 'style-punctuation', items['toneless']
assert not items['english-autocomplete'].get('disabled'), items['english-autocomplete']
PY_CONFLICT
  import -window root "$OUT/styles-custom.png"
else bad "輸入習慣複製頁沒有出現"; fi
stop_app
# 複製後能修改新規則；微軟習慣的 Emoji 關閉，因此等號子選項不占 Tab。
launch styles keyboard
if window_up; then
  sleep 2.5
  tabs $((NAV + 9)); xdotool key Down  # 鍵盤排列、Ctrl＋標點、Shift＋符號打半形（#258）、中英文之間自動加空格（#273）、中文與數字之間自動加空格、補標點、Emoji、標點操作之後
  sleep 3
  run_update styles settings -json > "$OUT/styles-rule.json"
  check "自訂習慣的 Shift＋空白可單獨修改並保留在同一份副本" python3 - "$OUT/styles-copy.json" "$OUT/styles-rule.json" <<'PY_RULE'
import json, sys
before, after = [json.load(open(p)) for p in sys.argv[1:]]
p = after['profiles']
assert p['active'] == before['profiles']['active']
a = next(x for x in p['items'] if x['id'] == p['active'])
assert not a['builtin'] and a['values']['style-shift-space'] == 'space'
PY_RULE
  import -window root "$OUT/styles-custom-rule.png"
else bad "自訂習慣鍵盤頁沒有出現"; fi
stop_app
# 舊 schema 已存在的偏好要保留；與新安裝的固定預設不同。
new_home styles-legacy
printf '{"schema":1,"toggle_key":"none","space_selects":false}\n' > "$(base_of styles-legacy)/settings.json"
run_update styles-legacy settings -json > "$OUT/styles-legacy.json"
check "升級保留原有偏好為可編輯自訂習慣" python3 - "$OUT/styles-legacy.json" <<'PY'
import json, sys
s = json.load(open(sys.argv[1]))
p = s['profiles']
a = next(x for x in p['items'] if x['id'] == p['active'])
assert not a['builtin']
assert next(x for x in s['items'] if x['key'] == 'toggle-key')['value'] == 'none'
assert not any(x.get('locked') for x in s['items'])
PY
shoot styles-legacy styles
# #318：新安裝（內建習慣）在設定頁直接改被鎖住的項目，一按就複製成自訂並改好，不再先跳出「複製成自訂並…」。
# 「中英切換」頁：頁首的「選用風格／複製成自訂」之後是中英切換鍵（一組單選）；↓ 換到下一個選項。
new_home styles-one
rm "$(base_of styles-one)/settings.json"
run_update styles-one settings -json > "$OUT/styles-one-before.json"
launch styles-one mode
if window_up; then
  sleep 2.5
  import -window root "$OUT/styles-one-locked.png"
  tabs $((NAV + 2)); xdotool key Down
  sleep 4
  import -window root "$OUT/styles-one-changed.png"
  run_update styles-one settings -json > "$OUT/styles-one-after.json"
  check "按被鎖住的中英切換鍵直接複製成「…（自訂）」、套用並改好這一項，其他設定照舊" python3 - "$OUT/styles-one-before.json" "$OUT/styles-one-after.json" <<'PY_ONE'
import json, sys
before, after = [json.load(open(p)) for p in sys.argv[1:]]
bp, ap = before['profiles'], after['profiles']
builtin = next(x for x in bp['items'] if x['id'] == bp['active'])
active = next(x for x in ap['items'] if x['id'] == ap['active'])
assert builtin['builtin'] and not active['builtin'], (builtin, active)
assert active['name'] == builtin['name'] + '（自訂）', active['name']
b = {x['key']: x['value'] for x in before['items']}
a = {x['key']: x['value'] for x in after['items']}
assert a['toggle-key'] != b['toggle-key'], (b['toggle-key'], a['toggle-key'])
assert {k: v for k, v in a.items() if k != 'toggle-key'} == {k: v for k, v in b.items() if k != 'toggle-key'}
assert not any(x.get('locked') for x in after['items'])
PY_ONE
else bad "styles-one/mode 視窗沒有出現"; cat /tmp/app-styles-one-mode.log; fi
stop_app
# 切回內建習慣再改一次：已經有「…（自訂）」時另建「…（自訂 2）」，不沿用舊副本。
run_update styles-one profile select "$(python3 -c "import json,sys; print(json.load(open(sys.argv[1]))['profiles']['active'])" "$OUT/styles-one-before.json")" > /dev/null
launch styles-one mode
if window_up; then
  sleep 2.5
  tabs $((NAV + 2)); xdotool key Down
  sleep 4
  import -window root "$OUT/styles-one-again.png"
  run_update styles-one settings -json > "$OUT/styles-one-again.json"
  check "已有同名自訂時另建編號副本，舊副本保留" python3 - "$OUT/styles-one-before.json" "$OUT/styles-one-again.json" <<'PY_AGAIN'
import json, sys
before, after = [json.load(open(p)) for p in sys.argv[1:]]
name = next(x for x in before['profiles']['items'] if x['id'] == before['profiles']['active'])['name']
p = after['profiles']
custom = sorted(x['name'] for x in p['items'] if not x['builtin'])
assert custom == sorted([name + '（自訂）', name + '（自訂 2）']), custom
assert next(x for x in p['items'] if x['id'] == p['active'])['name'] == name + '（自訂 2）'
PY_AGAIN
else bad "styles-one/mode 重新開啟失敗"; cat /tmp/app-styles-one-mode.log; fi
stop_app
