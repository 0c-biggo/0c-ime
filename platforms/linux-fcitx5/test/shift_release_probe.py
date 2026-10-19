#!/usr/bin/env python3
"""切換注／英的 Shift 按下與放開都要交還應用程式（#165）。

GNOME Wayland 上 VS Code 等 Chromium／Electron 經 GNOME Shell 的 IBus 介面收鍵、只看按鍵事件記 Shift：
放開被輸入法吃掉（ProcessKeyEvent 回 true），應用程式就以為 Shift 一直按著，之後的 ↑ 變成 Shift＋↑。

對目前 session bus 上、已載入靈犀注音的 Fcitx5，從兩個入口各建一個輸入欄位送鍵：
Fcitx5 自己的 DBus InputContext1（回報者的實驗），以及 GNOME Shell 走的 IBus 介面（Fcitx5 的 ibusfrontend）。
每次單按 Shift 都檢查按下、放開回 false，而且注／英真的切換了（打 j 有沒有出現注音組字）。
需要 python3-gi；不碰真實桌面，由 shift_release_test.sh 在容器裡起私有的 Fcitx5 與引擎。
"""
import json
import sys
import time

from gi.repository import Gio, GLib

SHIFT_L, SHIFT_R, KEY_J = 0xffe1, 0xffe2, ord('j')
X_KEYCODE = {SHIFT_L: 50, SHIFT_R: 62, KEY_J: 44}  # evdev + 8
SHIFT_MASK, IBUS_RELEASE = 1 << 0, 1 << 30
CAP_PREEDIT, CAP_FORMATTED = 1 << 1, 1 << 4

bus = Gio.bus_get_sync(Gio.BusType.SESSION)


def call(name, path, iface, method, args=None, reply=None):
    return bus.call_sync(name, path, iface, method, args, GLib.VariantType(reply) if reply else None,
                         Gio.DBusCallFlags.NONE, 5000, None)


def pump(seconds=.15):
    ctx = GLib.MainContext.default()
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        while ctx.pending():
            ctx.iteration(False)
        time.sleep(.005)


class FcitxContext:
    name = 'fcitx'

    def __init__(self):
        path, _ = call('org.fcitx.Fcitx5', '/org/freedesktop/portal/inputmethod', 'org.fcitx.Fcitx.InputMethod1',
                       'CreateInputContext', GLib.Variant('(a(ss))', ([('program', 'shift_release_probe')],)), '(oay)').unpack()
        self.path, self.preedit = path, ''
        bus.signal_subscribe('org.fcitx.Fcitx5', 'org.fcitx.Fcitx.InputContext1', 'UpdateFormattedPreedit', path, None,
                             Gio.DBusSignalFlags.NONE, self._preedit)
        self.ic('SetCapability', GLib.Variant('(t)', (CAP_PREEDIT | CAP_FORMATTED,)))
        self.ic('FocusIn')
        call('org.fcitx.Fcitx5', '/controller', 'org.fcitx.Fcitx.Controller1', 'SetCurrentIM', GLib.Variant('(s)', ('zuin',)))
        pump(.5)
        current = call('org.fcitx.Fcitx5', '/controller', 'org.fcitx.Fcitx.Controller1', 'CurrentInputMethod', None, '(s)').unpack()[0]
        if current != 'zuin':
            raise RuntimeError(f'fcitx：目前輸入法是 {current!r}，不是 zuin')

    def _preedit(self, conn, sender, path, iface, signal, params):
        self.preedit = ''.join(text for text, _ in params.unpack()[0])

    def ic(self, method, args=None, reply=None):
        return call('org.fcitx.Fcitx5', self.path, 'org.fcitx.Fcitx.InputContext1', method, args, reply)

    def key(self, sym, release, state):
        handled = self.ic('ProcessKeyEvent', GLib.Variant('(uuubu)', (sym, X_KEYCODE[sym], state, release, 0)), '(b)').unpack()[0]
        pump()
        return handled

    def reset(self):
        self.ic('Reset'); pump()


class IBusContext:
    name = 'ibus'

    def __init__(self):
        self.path = call('org.freedesktop.portal.IBus', '/org/freedesktop/IBus', 'org.freedesktop.IBus.Portal',
                         'CreateInputContext', GLib.Variant('(s)', ('shift_release_probe',)), '(o)').unpack()[0]
        self.preedit = ''
        bus.signal_subscribe('org.freedesktop.portal.IBus', 'org.freedesktop.IBus.InputContext', 'UpdatePreeditText',
                             self.path, None, Gio.DBusSignalFlags.NONE, self._preedit)
        self.ic('SetCapabilities', GLib.Variant('(u)', (1 | 8,)))  # IBUS_CAP_PREEDIT_TEXT | IBUS_CAP_FOCUS
        self.ic('FocusIn')
        call('org.fcitx.Fcitx5', '/controller', 'org.fcitx.Fcitx.Controller1', 'SetCurrentIM', GLib.Variant('(s)', ('zuin',)))
        pump(.5)

    def _preedit(self, conn, sender, path, iface, signal, params):
        # (vub)：IBusText 的序列化是 (sa{sv}sv)，第三欄是文字。
        self.preedit = params.unpack()[0][2]

    def ic(self, method, args=None, reply=None):
        return call('org.freedesktop.portal.IBus', self.path, 'org.freedesktop.IBus.InputContext', method, args, reply)

    def key(self, sym, release, state):
        state |= IBUS_RELEASE if release else 0
        handled = self.ic('ProcessKeyEvent', GLib.Variant('(uuu)', (sym, X_KEYCODE[sym] - 8, state)), '(b)').unpack()[0]
        pump()
        return handled

    def reset(self):
        self.ic('Reset'); pump()


def zhuyin(ic):
    """打 j：注音模式會出現組字「ㄨ」，英文模式不會。打完清掉，不留組字。"""
    ic.preedit = ''
    ic.key(KEY_J, False, 0); ic.key(KEY_J, True, 0)
    mode = 'ㄨ' in ic.preedit
    ic.reset(); ic.preedit = ''
    return mode


def wait_for_fcitx(timeout=10):
    """等 Fcitx5 起來、兩個入口都在 bus 上。"""
    deadline = time.monotonic() + timeout
    names = ('org.fcitx.Fcitx5', 'org.freedesktop.portal.IBus')
    while True:
        owned = [call('org.freedesktop.DBus', '/org/freedesktop/DBus', 'org.freedesktop.DBus', 'NameHasOwner',
                      GLib.Variant('(s)', (n,)), '(b)').unpack()[0] for n in names]
        if all(owned):
            pump(1)  # 讓 addon 載完
            return
        if time.monotonic() > deadline:
            raise RuntimeError(f'Fcitx5 沒有在 {timeout} 秒內提供 {names}：{owned}')
        time.sleep(.1)


def main():
    sides = sys.argv[1].split(',') if len(sys.argv) > 1 else ['right']
    report = {'passed': False, 'checks': [], 'events': []}
    try:
        wait_for_fcitx()
        for ic in (FcitxContext(), IBusContext()):
            if not zhuyin(ic):
                raise RuntimeError(f'{ic.name}：一開始不是注音模式')
            for side in sides:
                sym = SHIFT_R if side == 'right' else SHIFT_L
                for round_ in range(2):  # 注 → 英 → 注
                    before = zhuyin(ic)
                    press = ic.key(sym, False, 0)
                    release = ic.key(sym, True, SHIFT_MASK)
                    after = zhuyin(ic)
                    report['events'].append({'frontend': ic.name, 'side': side, 'press_handled': press,
                                             'release_handled': release, 'zhuyin_before': before, 'zhuyin_after': after})
                    if press or release:
                        raise RuntimeError(f'{ic.name}：{side} Shift 被輸入法吃掉（按下 {press}、放開 {release}），應用程式收不到')
                    if after == before:
                        raise RuntimeError(f'{ic.name}：{side} Shift 單按沒有切換注／英')
                report['checks'].append(f'{ic.name}_{side}_shift_release_reaches_app')
        report['passed'] = True
    finally:
        print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0


if __name__ == '__main__':
    sys.exit(main())
