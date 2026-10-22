# 「我的詞彙」頁（#125）的 UI 測試，由 smoke.sh 載入（共用 launch、check、new_home 等函式）。
#
# 前半用假的 0c-update（fake-update.py）模擬詞彙與 AI 助手的回應：畫面內容固定，可以截圖比對、檢查畫面送出的指令；
# 其他指令（status、settings、set）由它轉給真的 0c-update，所以入口 features.vocab 走真契約（0.2.0 起恆為 true）。
# 後半用真的 0c-update 與 0c-server：從畫面加詞進 vocab.json、AI 助手（命令列）加詞後畫面跟上、
# 設定程式被強制結束時 watch 跟著結束。

FAKE=/src/platforms/settings/test/fake-update.py
fake_env() { # 場景
  echo "HOME=$(home_of "$1") ZC_UPDATER=$FAKE REAL_UPDATER=$UPDATE_BIN FAKE_VOCAB=$(home_of "$1")/fake-vocab.json FAKE_LOG=$(home_of "$1")/fake.log"
}
vocab_home() { # 場景 初始狀態（empty|full|ai）[old：模擬沒有 features.vocab 的舊版 0c-update]
  new_home "$1"
  python3 "$FAKE" --init "$(home_of "$1")/fake-vocab.json" "$2" ${3:-}
}
vlaunch() { # 場景 頁面
  local name=$1 page=$2
  env $(fake_env "$name") "$SETTINGS_BIN" --page "$page" >"/tmp/app-$name-$page.log" 2>&1 &
  APP_PID=$!
}
win() { xdotool search --onlyvisible --name '靈犀注音設定' | head -1; }
wshot() { import -window "$(win)" "$OUT/$1.png"; } # 只截設定視窗
fake_log() { cat "$(home_of "$1")/fake.log" 2>/dev/null; }
fake_state() { # 場景 python 運算式（state 是狀態檔）→ 結束碼
  python3 - "$(home_of "$1")/fake-vocab.json" "$2" <<'PY'
import json,sys,time
for _ in range(60):
    state=json.load(open(sys.argv[1]))
    if eval(sys.argv[2]): sys.exit(0)
    time.sleep(.1)
sys.exit(1)
PY
}
logged_after() { # 場景 指令 時間：時間之後記到這個指令
  python3 - "$(home_of "$1")/fake.log" "$2" "$3" <<'PY'
import json,sys,time
for _ in range(30):
    try: lines=[json.loads(l) for l in open(sys.argv[1])]
    except FileNotFoundError: lines=[]
    if any(l['op']==sys.argv[2] and l['t']>float(sys.argv[3]) for l in lines): sys.exit(0)
    time.sleep(.1)
sys.exit(1)
PY
}
watchers() { pgrep -fc 'vocab watc[h]' || true; }
second() { HOME=$(home_of "$1") ZC_UPDATER=$FAKE timeout 10 "$SETTINGS_BIN" --page "$2" >/dev/null 2>&1; }
# Tab 到某個位置：$NAV 個導覽按鈕（smoke.sh）之後是分頁列、搜尋、新增欄位、加入鈕，再來是每一列的讀音與刪除。
tabs() { local n=$1; local keys=(); for _ in $(seq "$n"); do keys+=(Tab); done; xdotool key --delay 120 "${keys[@]}"; }

# ---- 舊版 0c-update（沒有 features.vocab）：側欄沒有「我的詞彙」，--page vocab 開總覽，畫面也不呼叫任何詞彙指令 ----
vocab_home voff empty old
vlaunch voff vocab
if window_up; then sleep 4; wshot vocab-off-overview
  check "舊版 0c-update 時畫面不呼叫詞彙指令" test -z "$(fake_log voff)"
  check "舊版 0c-update 時沒有 watch" test "$(watchers)" -eq 0
else bad "voff/vocab 視窗沒有出現"; cat /tmp/app-voff-vocab.log; fi
stop_app

# ---- 空的 ----
vocab_home vempty empty
vlaunch vempty vocab
if window_up; then sleep 3.5; wshot vocab-empty
  check "空的詞彙頁讀取清單與最近變更" bash -c "grep -q '\"op\": \"list\"' '$(home_of vempty)/fake.log' && grep -q '\"op\": \"batches\"' '$(home_of vempty)/fake.log'"
else bad "vempty/vocab 視窗沒有出現"; cat /tmp/app-vempty-vocab.log; fi
stop_app

# ---- 有詞：清單、排名徽章、打字自動標注音、破音字選單、Enter 加入、刪除與復原、AI 加詞即時出現 ----
vocab_home vfull full
vlaunch vfull vocab
if window_up; then
  sleep 3.5
  wshot vocab-words
  check "頁面開著時常駐一支 vocab watch" test "$(watchers)" -eq 1
  check "排名徽章向引擎查了每個詞（rank）" bash -c "grep '\"op\": \"rank\"' '$(home_of vfull)/fake.log' | grep -q 世介"
  tabs $((NAV + 3))                                   # → 新增欄位
  xdotool type --delay 120 '曾雅婷'
  sleep 1.5
  check "打字當下查讀音（annotate）" bash -c "grep '\"op\": \"annotate\"' '$(home_of vfull)/fake.log' | grep -q 曾雅婷"
  wshot vocab-draft-polyphone
  xdotool key Tab Tab                      # 加入鈕 → 「曾」的讀音選單（破音字）
  xdotool key space; sleep 1
  import -window root "$OUT/vocab-polyphone-menu-full.png"
  xdotool key Escape; sleep .5
  xdotool key shift+Tab shift+Tab Return   # 回到新增欄位按 Enter
  check "Enter 加入：讀音照建議（曾＝ㄗㄥ），來源是手動" fake_state vfull "any(w['word']=='曾雅婷' and w['readings']==['ㄗㄥ ㄧㄚˇ ㄊㄧㄥˊ'] and w['source']=='manual' for w in state['words'])"
  sleep 1.5
  wshot vocab-added
  # AI 助手從 API 加詞：版本加一，watch 印一行，頁面一秒內重抓清單。
  before=$(date +%s.%N)
  env $(fake_env vfull) python3 "$FAKE" vocab add-as assistant 黃偉倫 'ㄏㄨㄤˊ ㄨㄟˇ ㄌㄨㄣˊ' >/dev/null
  check "AI 助手加詞後頁面重抓清單" logged_after vfull list "$before"
  check "AI 助手加詞後 1.5 秒內畫面跟上" python3 - "$(home_of vfull)/fake.log" "$before" <<'PY'
import json,sys
lines=[json.loads(l) for l in open(sys.argv[1])]
add=next(l['t'] for l in lines if l['op']=='add' and l['req'].get('source')=='assistant' and l['t']>float(sys.argv[2]))
sys.exit(0 if any(l['op']=='list' and add < l['t'] < add+1.5 for l in lines) else 1)
PY
  sleep .3; wshot vocab-ai-added
  # 刪除不跳確認框：焦點在新增欄位 → 加入鈕 → 第一列讀音 → 第一列刪除。
  tabs 3; xdotool key space
  check "刪除第一個詞（陳怡君）不需確認" fake_state vfull "not any(w['word']=='陳怡君' for w in state['words'])"
  sleep .8; wshot vocab-deleted-undo
  xdotool key Return                       # 焦點已交給底部提示的「復原」
  check "底部提示的復原把詞放回來" fake_state vfull "any(w['word']=='陳怡君' for w in state['words'])"
  sleep 1
  # 離開頁面（單一實例切到總覽）時結束 watch；回來再開一支。
  second vfull overview; sleep 2
  check "離開詞彙頁時結束 watch" test "$(watchers)" -eq 0
  wshot vocab-overview-card
  second vfull vocab; sleep 2.5
  check "回到詞彙頁重新開 watch" test "$(watchers)" -eq 1
else bad "vfull/vocab 視窗沒有出現"; cat /tmp/app-vfull-vocab.log; fi
stop_app
sleep 2
check "設定程式被強制結束後 watch 跟著結束" test "$(watchers)" -eq 0

# 最近變更：點開一批看明細。
vlaunch vfull vocab
if window_up; then
  sleep 3.5
  n=$(python3 -c "import json,sys; print(len(json.load(open(sys.argv[1]))['words']))" "$(home_of vfull)/fake-vocab.json")
  tabs $((NAV + 4 + 2 * n + 1)); xdotool key space   # 第一批的摘要
  check "點開最近變更向引擎要明細" logged_after vfull batches 0
  sleep 1.2; wshot vocab-recent-detail
else bad "vfull 最近變更視窗沒有出現"; fi
stop_app

# 改讀音：第二列（曾國城）的讀音鈕開編輯器，破音字「曾」改成 ㄘㄥˊ；再開一次，送出前別人（AI 助手）先改了，
# 引擎回 conflict，畫面不覆蓋別人的修改。
vlaunch vfull vocab
if window_up; then
  sleep 3.5
  row=$(python3 -c "import json,sys; print([w['word'] for w in json.load(open(sys.argv[1]))['words']].index('曾國城'))" "$(home_of vfull)/fake-vocab.json")
  tabs $((NAV + 5 + 2 * row)); xdotool key space; sleep 1.5   # 那一列的讀音
  wshot vocab-edit-reading
  xdotool key Down; sleep .3; xdotool key Return
  check "改讀音（readings，附上看到的原讀音）" fake_state vfull "any(w['word']=='曾國城' and w['readings']==['ㄘㄥˊ ㄍㄨㄛˊ ㄔㄥˊ'] for w in state['words'])"
  check "改讀音時送出 expect" bash -c "grep '\"op\": \"readings\"' '$(home_of vfull)/fake.log' | grep -q expect"
  sleep 1.5
  xdotool key space; sleep 1.5              # 改完焦點回到同一列的讀音：再開一次編輯器
  echo '{"op":"readings","text":"曾國城","readings":["ㄗㄥ ㄍㄨㄛˊ ㄔㄥˊ"],"source":"assistant"}' | env $(fake_env vfull) python3 "$FAKE" vocab request >/dev/null
  xdotool key Return; sleep 1               # 畫面上仍是 ㄘㄥˊ；照送就會蓋掉 AI 助手的 ㄗㄥ
  wshot vocab-edit-conflict
  check "別人剛改過時不覆蓋（conflict）" fake_state vfull "any(w['word']=='曾國城' and w['readings']==['ㄗㄥ ㄍㄨㄛˊ ㄔㄥˊ'] for w in state['words'])"
else bad "vfull 改讀音視窗沒有出現"; fi
stop_app

# 靈犀學到的（保留、移除、不再學）與我的片語（新增、排序）。
vlaunch vfull vocab
if window_up; then
  sleep 3.5
  tabs $((NAV + 1)); xdotool key Right; sleep 1.5      # 分頁列 → 靈犀學到的
  wshot vocab-learned
  check "沒有不再學的詞時不查也不顯示入口" bash -c "! grep -q '\"op\": \"forgotten\"' '$(home_of vfull)/fake.log'"
  xdotool key Tab Tab Tab space             # 全選 → 第一列勾選 → 第一列（大同區）保留
  check "保留學到的詞：轉成自訂詞，來源是學到的" fake_state vfull "any(w['word']=='大同區' and w['source']=='learned' for w in state['words'])"
  sleep 1
  # 保留鈕換成「已在我加的詞」，焦點交回分頁標籤。
  xdotool key Right; sleep 1.5              # → 我的片語
  wshot vocab-phrases
  xdotool key Tab; xdotool type --delay 120 '測試片語'; xdotool key ctrl+Return
  check "Ctrl+Enter 新增片語" fake_state vfull "len(state['phrases'])==4 and state['phrases'][-1]['text']=='測試片語'"
  sleep 1
  xdotool key Tab Tab space                 # 加入鈕 → 第一則的「下移」
  check "下移片語（phrase-move）" fake_state vfull "[p['id'] for p in state['phrases']][:2]==[2,1]"
  sleep 1; wshot vocab-phrases-moved
else bad "vfull 分頁視窗沒有出現"; fi
stop_app

# 學到的詞（#182）：全選後一起「不再學」，底部提示復原；再做一次，出現「不再學的詞」入口。
# 大同區（上面保留了）、長庚已是自訂詞，不能勾；全選勾的是研發部、銀行。
vlaunch vfull vocab
if window_up; then
  sleep 3.5
  tabs $((NAV + 1)); xdotool key Right; sleep 1.5
  xdotool key Tab space; sleep .5           # 全選
  wshot vocab-learned-selected
  xdotool key Tab Tab Tab space             # 批次：保留 → 移除 → 不再學
  check "勾選的兩個詞一起不再學" fake_state vfull "sorted(state['tombstones'])==['研發部','銀行'] and [l['word'] for l in state['learned']]==['大同區','長庚']"
  check "批次只送一次 forget" python3 - "$(home_of vfull)/fake.log" <<'PY'
import json,sys
lines=[json.loads(l) for l in open(sys.argv[1])]
lines=[l for l in lines if l['op']=='forget']
sys.exit(0 if len(lines)==1 and sorted(lines[0]['req']['texts'])==['研發部','銀行'] else 1)
PY
  sleep .8; wshot vocab-learned-forget-undo
  xdotool key Return                        # 焦點已交給底部提示的「復原」
  check "復原不再學：詞回到清單、沒有 tombstone" fake_state vfull "state['tombstones']==[] and [l['word'] for l in state['learned']]==['大同區','研發部','銀行','長庚']"
  sleep 1.5
  xdotool key Tab space Tab Tab Tab space   # 分頁標籤 → 全選 → 不再學
  check "再一次不再學" fake_state vfull "sorted(state['tombstones'])==['研發部','銀行']"
else bad "vfull 學到的詞批次視窗沒有出現"; fi
stop_app

# 不再學的詞：有 tombstone 時才有入口；恢復學習可以復原。
vlaunch vfull vocab
if window_up; then
  sleep 3.5
  tabs $((NAV + 1)); xdotool key Right; sleep 1.5
  wshot vocab-learned-with-forgotten
  xdotool key Tab space; sleep 1.5          # 「不再學的詞（2）」
  check "打開不再學的詞（forgotten）" logged_after vfull forgotten 0
  wshot vocab-forgotten
  xdotool key Tab Tab Tab space             # （焦點在返回）全選 → 第一列勾選 → 第一列（研發部）恢復學習
  check "恢復學習一個詞" fake_state vfull "state['tombstones']==['銀行']"
  sleep .8; wshot vocab-forgotten-relearned
  xdotool key Return
  check "復原恢復學習" fake_state vfull "sorted(state['tombstones'])==['研發部','銀行']"
  sleep 1.5
  xdotool key Tab Tab space Tab space       # 分頁標籤 → 返回 → 全選 → 恢復學習 2 個
  check "全選恢復學習" fake_state vfull "state['tombstones']==[]"
  sleep 1.5; wshot vocab-forgotten-empty-back
else bad "vfull 不再學的詞視窗沒有出現"; fi
stop_app

# 移除（不留 tombstone）：全選三個一起移除，入口仍不出現；復原。
vocab_home vlearn full
vlaunch vlearn vocab
if window_up; then
  sleep 3.5
  tabs $((NAV + 1)); xdotool key Right; sleep 1.5
  xdotool key Tab space Tab Tab space       # 全選 → 保留 → 移除
  check "全選移除：三個學到的詞都清掉、不留 tombstone" fake_state vlearn "state['tombstones']==[] and [l['word'] for l in state['learned']]==['長庚']"
  sleep .8; wshot vocab-learned-unlearn-undo
  xdotool key Return
  check "復原移除" fake_state vlearn "len(state['learned'])==4"
else bad "vlearn 視窗沒有出現"; fi
stop_app

# 一次貼上多行：變成預覽清單，勾選後一起加入。剪貼簿內容由片語框複製（容器裡沒有剪貼簿工具）。
vlaunch vfull vocab
if window_up; then
  sleep 3.5
  tabs $((NAV + 1)); xdotool key Right Right; sleep 1  # → 我的片語
  xdotool key Tab
  xdotool type --delay 100 '張天堂'; xdotool key Return
  xdotool type --delay 100 '林志玲'; xdotool key Return
  xdotool type --delay 100 'kubectl'; xdotool key Return   # 英文詞（#146）：不查讀音，預設勾選
  xdotool type --delay 100 'Hello world'                    # 有空白，不是詞也不是英文詞
  xdotool key ctrl+a ctrl+c BackSpace
  xdotool key shift+Tab Left Left; sleep 1   # 回到「我加的詞」
  xdotool key Tab Tab ctrl+v; sleep 2        # 新增欄位貼上
  check "貼上多行時一次查讀音" bash -c "grep '\"op\": \"annotate\"' '$(home_of vfull)/fake.log' | grep 張天堂 | grep -q 林志玲"
  wshot vocab-paste-preview
  check "貼上的英文詞不查讀音" bash -c "! grep '\"op\": \"annotate\"' '$(home_of vfull)/fake.log' | grep -q kubectl"
  tabs 5; xdotool key space                  # 加入鈕、三個勾選框（Hello world 不能勾）→ 加入勾選的 3 個
  check "勾選的詞一起加入（同一批，含英文詞）" fake_state vfull "any(b['op']=='add' and sorted(c['after']['word'] for c in b['changes'])==['kubectl','張天堂','林志玲'] for b in state['batches'])"
  sleep 1; wshot vocab-paste-added
else bad "vfull 貼上視窗沒有出現"; fi
stop_app

# ---- 讓 AI 幫你加詞：展開、顯示範例、測試連線 ----
vocab_home vai ai
vlaunch vai vocab
if window_up; then
  sleep 3.5
  tabs $((NAV + 5)); xdotool key space; sleep 2       # 空清單：分頁、搜尋、新增、加入之後就是收合區塊
  check "展開 AI 區塊時查詢狀態（api status）" bash -c "grep -q '\"op\": \"api-status\"' '$(home_of vai)/fake.log'"
  wshot vocab-ai-open
  # 開關 → 顯示 → 複製 → 重新產生 → 工具選單：選「Claude Code」。
  xdotool key Tab Tab Tab Tab Tab; sleep .3; xdotool key Down; sleep 1
  wshot vocab-ai-claude-code
  xdotool key Tab Tab space; sleep 2        # 複製 → 測試連線
  check "測試連線（api test）" bash -c "grep -q '\"op\": \"api-test\"' '$(home_of vai)/fake.log'"
  wshot vocab-ai-test
else bad "vai/vocab 視窗沒有出現"; cat /tmp/app-vai-vocab.log; fi
stop_app

# ---- 引擎沒有執行：詞照樣能改（0c-update 直接改 vocab.json），學到的詞要等引擎 ----
vocab_home vdown full
python3 - "$(home_of vdown)/fake-vocab.json" <<'PY'
import json,sys; p=sys.argv[1]; s=json.load(open(p)); s['engine']=False; json.dump(s,open(p,'w'),ensure_ascii=False)
PY
vlaunch vdown vocab
if window_up; then sleep 3.5; wshot vocab-engine-down
else bad "vdown/vocab 視窗沒有出現"; fi
stop_app

# ---- 英文詞（#146）：清單標示「英文」、沒有讀音鈕與排名；新增欄位打英文詞不查讀音，Enter 直接加入 ----
vocab_home ven english
vlaunch ven vocab
if window_up; then
  sleep 3.5
  wshot vocab-english
  check "英文詞不查排名" bash -c "grep '\"op\": \"rank\"' '$(home_of ven)/fake.log' | grep -q 陳怡君 && ! grep '\"op\": \"rank\"' '$(home_of ven)/fake.log' | grep -q kubectl"
  tabs $((NAV + 3))                                   # → 新增欄位
  xdotool type --delay 120 'rm'
  sleep 1.2
  wshot vocab-english-draft
  check "英文詞不查讀音" bash -c "! grep '\"op\": \"annotate\"' '$(home_of ven)/fake.log' | grep -q '\"rm\"'"
  xdotool key Return
  check "Enter 加入英文詞（沒有讀音）" fake_state ven "any(w['word']=='rm' and w['readings']==[] for w in state['words'])"
  sleep 1.5; wshot vocab-english-added
else bad "ven/vocab 視窗沒有出現"; cat /tmp/app-ven-vocab.log; fi
stop_app

# ---- 真的 0c-update 與 0c-server：畫面加詞進 vocab.json，AI 助手加詞畫面跟上，強制結束時 watch 跟著結束 ----
new_home vlive
vlive_home=$(home_of vlive)
HOME=$vlive_home "$SERVER_BIN" -data "$DATA" -user "$(base_of vlive)/user.json" -socket "$XDG_RUNTIME_DIR/0c/0c.sock" -legacy-socket "" >/tmp/server-vlive.log 2>&1 &
VSERVER=$!
for _ in $(seq 50); do [ -S "$XDG_RUNTIME_DIR/0c/0c.sock" ] && break; sleep 0.2; done
check "（真引擎）詞彙指令可用" bash -c "HOME='$vlive_home' '$UPDATE_BIN' vocab status -json | grep -q '\"ok\":true'"
launch vlive vocab
if window_up; then
  sleep 3.5
  tabs $((NAV + 3)); xdotool type --delay 120 '紫風'; sleep 1.5
  wshot vocab-live-draft
  xdotool key Return
  check "（真引擎）從畫面加的詞寫進 vocab.json" bash -c "for i in \$(seq 30); do grep -q 紫風 '$(base_of vlive)/vocab.json' 2>/dev/null && exit 0; sleep .2; done; exit 1"
  check "（真引擎）讀音由 annotate 自動標好" bash -c "HOME='$vlive_home' '$UPDATE_BIN' vocab list -json | grep -q 'ㄗˇ ㄈㄥ'"
  check "（真引擎）頁面開著時有一支真的 0c-update vocab watch" test "$(watchers)" -eq 1
  HOME=$vlive_home "$UPDATE_BIN" vocab add -source assistant 親子 'ㄑㄧㄣ ㄗˇ' >/dev/null
  sleep 2.5; wshot vocab-live-ai-added
  # 英文詞（#146）：焦點還在新增欄位，打 ls 按 Enter；AI 助手（命令列）再加 kubectl。
  xdotool type --delay 120 'ls'; sleep 1; xdotool key Return
  check "（真引擎）從畫面加的英文詞寫進 vocab.json（沒有讀音）" bash -c "for i in \$(seq 30); do HOME='$vlive_home' '$UPDATE_BIN' vocab list -json -query ls | grep -q '\"word\":\"ls\",\"readings\":\[\]' && exit 0; sleep .2; done; exit 1"
  HOME=$vlive_home "$UPDATE_BIN" vocab add -source assistant kubectl >/dev/null
  sleep 2.5; wshot vocab-live-english
else bad "vlive/vocab 視窗沒有出現"; cat /tmp/app-vlive-vocab.log; fi
kill -9 "$APP_PID" 2>/dev/null; wait "$APP_PID" 2>/dev/null
sleep 2.5
check "（真引擎）設定程式被強制結束後真的 watch 也結束" test "$(watchers)" -eq 0
# 學到的詞（#182）：真的引擎回應不再學、不再學的詞、恢復學習與復原；有不再學的詞時頁面出現入口。
check "（真引擎）不再學" bash -c "HOME='$vlive_home' '$UPDATE_BIN' vocab forget 測試詞 -json | grep -q '\"forgotten_count\":1'"
check "（真引擎）列出不再學的詞" bash -c "HOME='$vlive_home' '$UPDATE_BIN' vocab forgotten -json | grep -q '\"forgotten\":\[\"測試詞\"\]'"
launch vlive vocab
if window_up; then
  sleep 3.5
  tabs $((NAV + 1)); xdotool key Right; sleep 1.5
  wshot vocab-live-learned-forgotten
  xdotool key Tab space; sleep 1.5
  wshot vocab-live-forgotten
else bad "vlive 學到的詞視窗沒有出現"; fi
stop_app
undo=$(HOME=$vlive_home "$UPDATE_BIN" vocab relearn 測試詞 -json | python3 -c 'import json,sys; print(json.load(sys.stdin).get("undo",0))')
check "（真引擎）恢復學習回傳復原編號" test "$undo" -gt 0
check "（真引擎）復原恢復學習" bash -c "echo '{\"op\":\"learned-undo\",\"undo\":$undo}' | HOME='$vlive_home' '$UPDATE_BIN' vocab request | grep -q '\"forgotten_count\":1'"
check "（真引擎）同一個復原只能用一次" bash -c "! echo '{\"op\":\"learned-undo\",\"undo\":$undo}' | HOME='$vlive_home' '$UPDATE_BIN' vocab request | grep -q '\"ok\":true'"
kill "$VSERVER" 2>/dev/null; wait "$VSERVER" 2>/dev/null
