#!/bin/bash
# 設定程式呼叫 0c-update 時經過這裡（#366）：每次呼叫在 UPDATE_CALLS 記一行開始、一行結束（連同呼叫它的設定程式的行程編號），
# 再照原樣轉給 CALLS_TARGET。
# smoke.sh 靠這份紀錄等「儲存完成、畫面重讀完設定」再按下一個鍵，不用固定秒數猜。
# 常駐的 watch 直接 exec，不留中間的 bash：vocab.sh 用 pgrep 數 watch，設定程式結束時也要能直接結束它。
case "$1 ${2:-}" in
  watch*|"vocab watch") exec "$CALLS_TARGET" "$@" ;;
esac
printf 'start %s %s %s\n' "$$" "$1" "$PPID" >> "$UPDATE_CALLS"
"$CALLS_TARGET" "$@"
code=$?
printf 'end %s %s %s %s\n' "$$" "$1" "$PPID" "$code" >> "$UPDATE_CALLS"
exit "$code"
