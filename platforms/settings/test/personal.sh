# 「我的詞彙」頁「靈犀學到的」清單底下的「清除學習紀錄」（#301）的 UI 測試，由 smoke.sh 載入（共用 launch、check、new_home 等函式）。
# 用真的 0c-update 與 0c-server（testdata/toy）；設定程式呼叫的 0c-update 包一層記錄參數與 vocab request 的內容（plog）：
#   - 「選字」頁沒有這張卡（截圖 personal-candidates）。
#   - 按「清除…」只跳出確認，焦點在「取消」；取消不送出任何動作，檔案不動。
#   - 引擎沒在跑：確認後 user.json 刪掉，升級前備份（Linux personal/user.json、Windows 的 windows-*/user.json）一起刪，vocab.json 保留。
#   - 引擎在跑：確認後 user.json 立刻是空白的個人資料、引擎照常在跑；結束引擎（SIGTERM 會存檔）也不會寫回清除前的學習。
#     清除後畫面向引擎重讀學到的詞，清單清空、「不再學的詞（N）」入口消失：分頁標籤之後的第一個控制項就是「清除…」。
# 截圖 personal-*.png。

PLEARN='{"Format":7,"Bi":{"<s>\t我":0.25},"Ctx":{"<s>":0.25},"Uni":{"我":1,"親子丼":2},"Total":3,"Tombstones":{"秘密":true},"Phrases":{"親子丼":"ㄑㄧㄣ ㄗˇ ㄐㄧㄥˇ"},"PhraseOrder":["親子丼"],"PhraseSeen":{"親子丼":3}}'
PVNAV=$((NAV + 2))   # 側欄之後是標題列的「匯出」「匯入」，再來是分頁列（同 vocab.sh 的 VNAV）
pwin() { xdotool search --onlyvisible --name '靈犀注音設定' | head -1; }
pshot() { import -window "$(pwin)" "$OUT/$1.png"; }
PWRAP=/tmp/plog-updater.sh
cat > "$PWRAP" <<'SH'
#!/bin/bash
# 記下設定程式呼叫 0c-update 的參數（vocab request 連同 stdin 的 JSON），再交給真的 0c-update。
if [ "$1 $2" = "vocab request" ]; then
  body=$(cat)
  printf '%s %s\n' "$*" "$body" >> "$PLOG"
  printf '%s' "$body" | exec "$REAL_UPDATER" "$@"
fi
printf '%s\n' "$*" >> "$PLOG"
exec "$REAL_UPDATER" "$@"
SH
chmod +x "$PWRAP"
plaunch() { # 場景 頁面
  local name=$1 page=$2
  HOME=$(home_of "$name") ZC_UPDATER=$PWRAP REAL_UPDATER=$UPDATE_BIN PLOG=$(home_of "$name")/plog "$SETTINGS_BIN" --page "$page" >"/tmp/app-$name-$page.log" 2>&1 &
  APP_PID=$!
}
plog() { cat "$(home_of "$1")/plog" 2>/dev/null; }
personal_home() { # 場景 → 有學習資料、自訂詞彙與升級前備份的個人資料目錄
  new_home "$1"
  local b; b=$(base_of "$1")
  printf '%s\n' "$PLEARN" > "$b/user.json"
  printf '{"format":1,"words":[{"word":"ls","readings":[]}]}\n' > "$b/vocab.json"
  mkdir -p "$b/backups/0c-install-abcd1234/personal" "$b/backups/windows-1234"
  printf '%s\n' "$PLEARN" > "$b/backups/0c-install-abcd1234/personal/user.json"
  printf 'vocab\n' > "$b/backups/0c-install-abcd1234/personal/vocab.json"
  printf '%s\n' "$PLEARN" > "$b/backups/windows-1234/user.json"
}
pempty() { # 檔案 → 是沒有任何學習的個人資料
  python3 - "$1" <<'PY'
import json,sys
s=json.load(open(sys.argv[1]))
sys.exit(0 if not any(s.get(k) for k in ('Bi','Ctx','Uni','Total','Tombstones','EvidenceByWord','EvidenceByContext','Phrases','PhraseOrder','PhraseSeen')) else 1)
PY
}
pwait() { for _ in $(seq 50); do eval "$1" && return 0; sleep .2; done; return 1; }
presets() { plog "$1" | grep -c '^personal reset -json' || true; }
# 場景：最後一次 personal reset 之後，畫面向引擎重讀了學到的詞（vocab request 的 learned）
preloaded() { plog "$1" | awk '/^personal reset -json/{r=1; next} r && /^vocab request .*"op": *"learned"/{f=1} END{exit !f}'; }

# ---- 「選字」頁沒有這張卡（移到「我的詞彙」） ----
personal_home pcand
plaunch pcand candidates
if window_up; then
  sleep 3; xdotool key shift+Tab; sleep 1          # 焦點移到頁面最後一個控制項，捲到頁尾
  pshot personal-candidates; ok "「選字」頁視窗出現，頁尾截圖"
else bad "pcand/candidates 視窗沒有出現"; cat /tmp/app-pcand-candidates.log; fi
stop_app

# ---- 引擎沒在跑：取消不動作；確認後刪檔與備份，vocab.json 保留 ----
personal_home poff
plaunch poff vocab
if window_up; then
  sleep 3
  tabs $((PVNAV + 1)); xdotool key Right; sleep 1.5   # 分頁列 → 靈犀學到的
  pshot personal-card
  xdotool key Tab space; sleep 1; pshot personal-confirm   # 引擎沒在跑：清單讀不到，下一個控制項就是「清除…」
  xdotool key space; sleep 1                       # 焦點在「取消」
  check "取消不清除（user.json 還在）" test -s "$(base_of poff)/user.json"
  check "取消不呼叫 personal reset" test "$(presets poff)" -eq 0
  xdotool key space; sleep 1                       # 取消後焦點回到「清除…」，再按一次 → 確認
  xdotool key shift+Tab space                      # 取消前一個是「確認清除」
  check "確認後刪掉 user.json" pwait "! test -e '$(base_of poff)/user.json'"
  sleep 1; pshot personal-done
  check "升級前的個人詞庫備份一起刪（Linux）" test ! -e "$(base_of poff)/backups/0c-install-abcd1234/personal/user.json"
  check "升級前的個人詞庫備份一起刪（Windows 形式）" test ! -e "$(base_of poff)/backups/windows-1234/user.json"
  check "自訂詞彙保留" bash -c "HOME='$(home_of poff)' '$UPDATE_BIN' vocab list -json | grep -q '\"word\":\"ls\"'"
  check "備份裡的自訂詞彙保留" test -s "$(base_of poff)/backups/0c-install-abcd1234/personal/vocab.json"
else bad "poff/vocab 視窗沒有出現"; cat /tmp/app-poff-vocab.log; fi
stop_app

# ---- 引擎在跑：交給引擎清空並立刻存檔，結束時不寫回；畫面上學到的詞跟著清空 ----
personal_home plive
pkill -x 0c-server 2>/dev/null; sleep 0.5          # 前面場景留下的引擎占著同一個 socket
(
  export HOME=$(home_of plive)
  "$SERVER_BIN" -data "$DATA" -user "$(base_of plive)/user.json" -socket "$XDG_RUNTIME_DIR/0c/0c.sock" -legacy-socket "" >/tmp/server-plive.log 2>&1 &
  echo $! > /tmp/server-plive.pid
)
PSERVER=$(cat /tmp/server-plive.pid)
for _ in $(seq 50); do [ -S "$XDG_RUNTIME_DIR/0c/0c.sock" ] && break; sleep 0.2; done
check "引擎已啟動（讀進清除前的學習）" test -S "$XDG_RUNTIME_DIR/0c/0c.sock"
check "清除前引擎列出學到的詞（親子丼）與不再學的詞" bash -c "HOME='$(home_of plive)' '$UPDATE_BIN' vocab learned -json | grep -q '親子丼.*\"forgotten_count\":1'"
plaunch plive vocab
if window_up; then
  sleep 3.5
  tabs $((PVNAV + 1)); xdotool key Right; sleep 1.5
  pshot personal-live-before
  # 「不再學的詞（1）」→ 全選 → 親子丼的勾選、保留、移除、不再學 → 「清除…」
  tabs 7; xdotool key space; sleep 1; pshot personal-live-confirm
  xdotool key shift+Tab space
  check "引擎清空並立刻存檔（user.json 是空白的個人資料）" pwait "pempty '$(base_of plive)/user.json'"
  sleep 1.5; pshot personal-done-live
  check "清除後畫面重讀學到的詞" pwait "preloaded plive"
  # 清單空了、「不再學的詞（1）」入口也沒了：「清除…」的前一個控制項就是分頁標籤（按空白鍵不做事），
  # 標籤之後的第一個控制項就是「清除…」，確認後再清一次。清單沒清掉的話，前一個是那一列的「不再學」，空白鍵會送出 forget。
  xdotool key shift+Tab space; sleep 1
  xdotool key Tab space; sleep 1; xdotool key shift+Tab space
  check "清除後靈犀學到的清單清空（分頁標籤下一個就是「清除…」）" pwait "test \"\$(presets plive)\" -eq 2"
  check "清除後畫面上沒有學到的詞可按（沒有送出 forget／forgotten）" bash -c "! plog plive | awk '/^personal reset -json/{r=1; next} r' | grep -Eq '\"op\": *\"(forget|forgotten|unlearn|keep)\"'"
  check "清除後引擎沒有學到的詞也沒有不再學的詞" bash -c "HOME='$(home_of plive)' '$UPDATE_BIN' vocab learned -json | grep -q '\"ok\":true' && ! HOME='$(home_of plive)' '$UPDATE_BIN' vocab learned -json | grep -Eq '親子丼|forgotten_count'"
  check "引擎沒有被結束" kill -0 "$PSERVER"
  check "升級前的個人詞庫備份一起刪" test ! -e "$(base_of plive)/backups/windows-1234/user.json"
  check "自訂詞彙保留" bash -c "HOME='$(home_of plive)' '$UPDATE_BIN' vocab list -json | grep -q '\"word\":\"ls\"'"
else bad "plive/vocab 視窗沒有出現"; cat /tmp/app-plive-vocab.log; fi
stop_app
kill -TERM "$PSERVER" 2>/dev/null; wait "$PSERVER" 2>/dev/null
for _ in $(seq 50); do kill -0 "$PSERVER" 2>/dev/null || break; sleep 0.2; done
check "引擎結束時沒有把清除前的學習寫回來" pempty "$(base_of plive)/user.json"
