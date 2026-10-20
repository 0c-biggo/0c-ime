#!/bin/sh
# 起私有的 Fcitx5（沒有顯示器，只開 DBus 與 IBus 介面）與 0c-server，跑 shift_release_probe.py（#165）。
# 用法：dbus-run-session -- sh shift_release_test.sh <lib0c.so> <0c-server> <資料包目錄>
# 只在暫時的家目錄與 XDG_RUNTIME_DIR 裡動作；要在拋棄式容器或 desktop_test.py 那樣的私有 session bus 裡跑，
# 不要在日常桌面的 session bus 上執行（會跟正在用的 Fcitx5 搶名稱）。中英切換鍵設為 both，左右 Shift 都驗。
set -eu
here=$(cd "$(dirname "$0")" && pwd)
lib=$(realpath "$1"); server=$(realpath "$2"); data=$(realpath "$3")
case "$lib" in *.so) lib=${lib%.so} ;; esac
HOME=$(mktemp -d); XDG_RUNTIME_DIR=$(mktemp -d)
export HOME XDG_RUNTIME_DIR XDG_CONFIG_HOME="$HOME/.config" XDG_DATA_HOME="$HOME/.local/share"
unset DISPLAY WAYLAND_DISPLAY FCITX_ADDON_DIRS ZUIN_TRACE
share=$XDG_DATA_HOME/fcitx5
mkdir -p "$share/addon" "$share/inputmethod" "$XDG_CONFIG_HOME/fcitx5" "$XDG_DATA_HOME/0c" "$XDG_RUNTIME_DIR/0c"
sed "s|^Library=lib0c\$|Library=$lib|" "$here/../0c-addon.conf" > "$share/addon/0c.conf"
cp "$here/../0c-im.conf" "$share/inputmethod/zuin.conf"
printf '[Groups/0]\nName=Default\nDefault Layout=us\nDefaultIM=zuin\n\n[Groups/0/Items/0]\nName=keyboard-us\nLayout=\n\n[Groups/0/Items/1]\nName=zuin\nLayout=\n\n[GroupOrder]\n0=Default\n' \
    > "$XDG_CONFIG_HOME/fcitx5/profile"
printf '{"toggle_key":"both"}\n' > "$XDG_DATA_HOME/0c/settings.json"
"$server" -data "$data" -user "$HOME/user.json" -socket "$XDG_RUNTIME_DIR/0c/0c.sock" 2>"$HOME/server.log" &
srv=$!
fcitx5 --disable=xcb,xim,wayland,waylandim,classicui,kimpanel,notificationitem,virtualkeyboard 2>"$HOME/fcitx5.log" &
fx=$!
trap 'kill $fx $srv 2>/dev/null || true' EXIT
python3 "$here/shift_release_probe.py" right,left || { echo '--- fcitx5.log'; tail -n 40 "$HOME/fcitx5.log"; echo '--- server.log'; tail -n 20 "$HOME/server.log"; exit 1; }
