# 「模型」頁（#180）的 UI 測試，由 smoke.sh 載入（共用 launch、check、new_home 等函式）。
#
# models status|download|cancel|remove 由假的 0c-update（fake-update.py，FAKE_MODELS）依契約回應；status、settings 等
# 其他指令照舊轉給真的 0c-update。外部網址交給 PATH 上假的 xdg-open，記下收到的網址。
# 截圖只截設定視窗（models-*.png）。Tab 順序：$NAV 個導覽按鈕（smoke.sh）之後是這一頁的按鈕，再來是每張模型卡的「複製」與 Hugging Face 連結。

MFAKE=/src/platforms/settings/test/fake-update.py
mwin() { xdotool search --onlyvisible --name '靈犀注音設定' | head -1; }
mshot() { import -window "$(mwin)" "$OUT/$1.png"; }
mtabs() { local keys=(); for _ in $(seq "$1"); do keys+=(Tab); done; xdotool key --delay 120 "${keys[@]}"; }
models_home() { # 場景 初始狀態（bundled|none|downloading|user|redownload|runtime|distribution）
  new_home "$1"
  python3 "$MFAKE" --init "$(home_of "$1")/fake-vocab.json" empty
  python3 "$MFAKE" --models "$(home_of "$1")/fake-models.json" "$2"
  mkdir -p "$(home_of "$1")/fakebin"
  printf '#!/bin/sh\nprintf "%%s\\n" "$@" >> "%s/opened-urls"\n' "$(home_of "$1")" > "$(home_of "$1")/fakebin/xdg-open"
  chmod +x "$(home_of "$1")/fakebin/xdg-open"
}
mlaunch() { # 場景 [額外環境]
  local name=$1
  env HOME="$(home_of "$name")" PATH="$(home_of "$name")/fakebin:$PATH" ZC_UPDATER=$MFAKE REAL_UPDATER=$UPDATE_BIN \
    FAKE_VOCAB="$(home_of "$name")/fake-vocab.json" FAKE_MODELS="$(home_of "$name")/fake-models.json" FAKE_LOG="$(home_of "$name")/fake.log" \
    "${@:2}" "$SETTINGS_BIN" --page models >"/tmp/app-$name-models.log" 2>&1 &
  APP_PID=$!
}
mlog_has() { # 場景 指令 → 假的 0c-update 收到過這個 models 指令
  for _ in $(seq 40); do grep -q "\"op\": \"models-$2\"" "$(home_of "$1")/fake.log" 2>/dev/null && return 0; sleep .2; done; return 1
}
mlog_lacks() { ! grep -q "\"op\": \"models-$2\"" "$(home_of "$1")/fake.log" 2>/dev/null; }
mstate() { # 場景 python 運算式（s 是 status）
  python3 - "$(home_of "$1")/fake-models.json" "$2" <<'PY'
import json,sys,time
for _ in range(60):
    s=json.load(open(sys.argv[1]))['status']
    if eval(sys.argv[2]): sys.exit(0)
    time.sleep(.2)
sys.exit(1)
PY
}

# ---- 官網套件（內建模型）：只顯示資訊，沒有按鈕；--page models 直接開到這一頁 ----
models_home mbundled bundled
mlaunch mbundled
if window_up; then
  sleep 3; mshot models-bundled
  check "--page models 開到模型頁（讀了 models status）" mlog_has mbundled status
  # 內建的沒有按鈕：導覽之後第 1 個 Tab 是第一張卡的「複製」，第 2 個是它的 Hugging Face 連結（0.2.2 以前內建的模型與公開版
  # 權重相同，也附上游來源，卡片多一行小字）。按下去只開網址，不會送出任何 models 動作。
  mtabs $((NAV + 2)); xdotool key space; sleep 1
  check "0.2.2 以前內建的模型也附 Hugging Face 連結" bash -c "for i in \$(seq 20); do grep -qx 'https://huggingface.co/Funmula/0c-sequence-d512/tree/df4b9f04f8aee1dd34dcb246ac2b83c4ab3d14a9' '$(home_of mbundled)/opened-urls' 2>/dev/null && exit 0; sleep .2; done; exit 1"
  check "內建模型沒有下載、取消、刪除" bash -c "! grep -Eq 'models-(download|cancel|remove)' '$(home_of mbundled)/fake.log'"
  # 深色模式（GTK 的深色主題 → prefers-color-scheme: dark）
else bad "mbundled/models 視窗沒有出現"; cat /tmp/app-mbundled-models.log; fi
stop_app
mlaunch mbundled GTK_THEME=Adwaita:dark
if window_up; then sleep 3; mshot models-bundled-dark; else bad "mbundled（深色）視窗沒有出現"; fi
stop_app

# ---- 未安裝、可下載：按「下載模型」→ 進度 → 核對 → 配對 → 已安裝 ----
models_home mnone none
mlaunch mnone
if window_up; then
  sleep 3; mshot models-none
  mtabs $((NAV + 1)); xdotool key space              # 第一個按鈕是「下載模型」
  check "按「下載模型」送出 models download" mlog_has mnone download
  sleep 1.2; mshot models-downloading-live
  check "每秒輪詢進度，下載完成後是已安裝（個人目錄）" mstate mnone "s['source']=='user' and s['operation'] is None"
  sleep 1.5; mshot models-installed-after-download
else bad "mnone/models 視窗沒有出現"; cat /tmp/app-mnone-models.log; fi
stop_app
mlaunch mnone GTK_THEME=Adwaita:dark
if window_up; then sleep 3; mshot models-user-dark; else bad "mnone（深色）視窗沒有出現"; fi
stop_app

# ---- 下載中：進度條與已下載大小，只能取消 ----
models_home mdl downloading
mlaunch mdl
if window_up; then
  sleep 3; mshot models-downloading
  mtabs $((NAV + 1)); xdotool key space              # 下載中唯一的按鈕是「取消」
  check "下載中按「取消」送出 models cancel" mlog_has mdl cancel
  check "取消後沒有進行中的作業" mstate mdl "s['operation'] is None and s['last_error']['code']=='canceled'"
  sleep 1.5; mshot models-canceled
else bad "mdl/models 視窗沒有出現"; cat /tmp/app-mdl-models.log; fi
stop_app

# ---- 已安裝（個人目錄）：Hugging Face 連結交給系統瀏覽器；刪除要先確認 ----
models_home muser user
mlaunch muser
if window_up; then
  sleep 3; mshot models-user
  mtabs $((NAV + 3)); xdotool key space             # 刪除 → 選字卡的「複製」→ 選字卡的 Hugging Face 連結
  check "Hugging Face 連結交給 xdg-open（系統瀏覽器）" bash -c "for i in \$(seq 20); do grep -qx 'https://huggingface.co/Funmula/0c-sequence-d512/tree/df4b9f04f8aee1dd34dcb246ac2b83c4ab3d14a9' '$(home_of muser)/opened-urls' 2>/dev/null && exit 0; sleep .2; done; exit 1"
  xdotool key shift+Tab shift+Tab space   # 回到「刪除」
  sleep 1; mshot models-remove-confirm
  check "按「刪除」先確認，還沒送出 models remove" mlog_lacks muser remove
  xdotool key space                       # 焦點在確認列的「取消」
  sleep 1
  check "確認列按「取消」不刪除" mlog_lacks muser remove
  xdotool key space; sleep 1              # 焦點回到「刪除」，再按一次
  xdotool key shift+Tab space             # 「取消」前面是「確認刪除」
  check "確認後送出 models remove" mlog_has muser remove
  check "刪除後回到未安裝" mstate muser "s['source']=='none' and s['need']=='download'"
  sleep 1.5; mshot models-removed
else bad "muser/models 視窗沒有出現"; cat /tmp/app-muser-models.log; fi
stop_app

# ---- 需要重新下載、缺推論元件、發行版關閉下載、字典不在配對清單上：截圖 ----
for kind in redownload runtime distribution dictionary; do
  models_home "m$kind" "$kind"
  mlaunch "m$kind"
  if window_up; then sleep 3; mshot "models-$kind"; else bad "m$kind/models 視窗沒有出現"; cat "/tmp/app-m$kind-models.log"; fi
  stop_app
done
check "發行版關閉下載時沒有任何下載動作" mlog_lacks mdistribution download
check "字典不在配對清單上時沒有任何下載動作" mlog_lacks mdictionary download
