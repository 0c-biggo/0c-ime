#!/usr/bin/env python3
"""等設定程式對 0c-update 的呼叫到某個狀態（#366）。紀錄由 calls.sh 寫：`start <pid> <指令> <設定程式>`、
`end <pid> <指令> <設定程式> <結束碼>`。只看這一個設定程式的呼叫：上一個設定程式被結束時還沒跑完的呼叫，
可能在下一個啟動、紀錄清空之後才寫下結束，不能拿來當這一個的「讀完設定」。

  calls_wait.py 紀錄檔 起點行數 ready|saved 上限秒數 [設定程式的行程編號]

ready：起點之後讀過一次設定（settings 結束），而且沒有進行中的呼叫——畫面已經拿到資料、畫好。
saved：起點之後有一個會改東西的呼叫（status、settings 以外的指令）結束，接著畫面重讀設定（之後開始的 settings 也結束），
       而且沒有進行中的呼叫——儲存完成、開關已經恢復可以按。
條件成立後再看 0.3 秒仍然沒有進行中的呼叫才算數（畫面重畫、焦點還原在同一輪事件裡做完）。
逾時結束碼 1，把起點之後的紀錄印到 stderr。
"""
import sys
import time

READS = {"status", "settings"}


def parse(path, mark, app=None):
    try:
        with open(path) as f:
            lines = f.read().splitlines()
    except FileNotFoundError:
        lines = []
    events = [line.split() for line in lines[mark:] if line.strip()]
    return [e for e in events if app is None or (len(e) > 3 and e[3] == app)]


def in_flight(events):
    started = {}
    for e in events:
        if e[0] == "start":
            started[e[1]] = e[2]
        elif e[0] == "end":
            started.pop(e[1], None)
    return started


def met(events, mode):
    if in_flight(events):
        return False
    if mode == "ready":
        return any(e[0] == "end" and e[2] == "settings" for e in events)
    # saved：最後一個結束的動作之後，要有一個之後才開始的 settings 也結束了。
    last_action = max((i for i, e in enumerate(events) if e[0] == "end" and e[2] not in READS), default=None)
    if last_action is None:
        return False
    return any(e[0] == "end" and e[2] == "settings" for e in events[last_action + 1:]
               if any(s[0] == "start" and s[1] == e[1] for s in events[last_action + 1:]))


def main():
    path, mark, mode, limit = sys.argv[1], int(sys.argv[2]), sys.argv[3], float(sys.argv[4])
    app = sys.argv[5] if len(sys.argv) > 5 and sys.argv[5] else None
    deadline = time.monotonic() + limit
    while time.monotonic() < deadline:
        if met(parse(path, mark, app), mode):
            time.sleep(0.3)
            if not in_flight(parse(path, mark, app)):
                return 0
        time.sleep(0.05)
    print(f"等不到 {mode}（{limit:g} 秒）；起點之後的呼叫：", file=sys.stderr)
    for e in parse(path, mark):
        print("  " + " ".join(e), file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
