#!/usr/bin/env python3
"""GTK3 + XTest typing probe for update E2E: types Zhuyin keys into a fresh GTK entry and prints the committed text.

Runs on the E2E account's own X display (update_e2e.py); never on a personal desktop.
"""
import ctypes
import json
from pathlib import Path
import subprocess
import sys
import time
import gi
gi.require_version('Gtk', '3.0')
gi.require_version('GdkX11', '3.0')
from gi.repository import Gtk, GdkX11  # noqa: F401

x = ctypes.CDLL('libX11.so.6'); xtest = ctypes.CDLL('libXtst.so.6')
x.XOpenDisplay.argtypes = [ctypes.c_char_p]; x.XOpenDisplay.restype = ctypes.c_void_p
x.XStringToKeysym.argtypes = [ctypes.c_char_p]; x.XStringToKeysym.restype = ctypes.c_ulong
x.XKeysymToKeycode.argtypes = [ctypes.c_void_p, ctypes.c_ulong]; x.XKeysymToKeycode.restype = ctypes.c_uint
x.XSetInputFocus.argtypes = [ctypes.c_void_p, ctypes.c_ulong, ctypes.c_int, ctypes.c_ulong]
x.XFlush.argtypes = [ctypes.c_void_p]
xtest.XTestFakeKeyEvent.argtypes = [ctypes.c_void_p, ctypes.c_uint, ctypes.c_int, ctypes.c_ulong]
display = x.XOpenDisplay(None)
if not display:
    raise SystemExit('no X display')
win = Gtk.Window(title='0c update E2E probe'); entry = Gtk.Entry(); win.add(entry); win.show_all()


def pump(seconds=.15):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        while Gtk.events_pending():
            Gtk.main_iteration_do(False)
        time.sleep(.005)


def key(name):
    code = x.XKeysymToKeycode(display, x.XStringToKeysym(name.encode()))
    xtest.XTestFakeKeyEvent(display, code, 1, 0); x.XFlush(display); pump(.03)
    xtest.XTestFakeKeyEvent(display, code, 0, 0); x.XFlush(display); pump()


result = {'keys': sys.argv[1], 'text': None, 'focus': False}
try:
    pump(.5)
    entry.grab_focus(); x.XSetInputFocus(display, win.get_window().get_xid(), 1, 0); x.XFlush(display); pump(.5)
    subprocess.run(['fcitx5-remote', '-s', 'zuin'], check=True); subprocess.run(['fcitx5-remote', '-o'], check=True); pump(.8)
    result['focus'] = entry.has_focus()
    for k in sys.argv[1].split():
        if len(k) == 1 or k in ('Return', 'Escape', 'Tab', 'BackSpace', 'Down', 'space'):
            for c in ([k] if len(k) > 1 else list(k)):
                key(c)
        else:
            for c in k:
                key(c)
    pump(.5)
    result['text'] = entry.get_text()
    if len(sys.argv) > 2 and sys.argv[2] == '--shell-auto':
        # 一直保留同一個 GTK 欄位與連線：排程不得提交組字；更新後也要能繼續輸入。
        work = Path(sys.argv[3])
        def mark(name, data):
            tmp = work / (name + '.tmp')
            tmp.write_text(json.dumps(data, ensure_ascii=False))
            tmp.replace(work / name)
        def wait_marker(name):
            deadline = time.monotonic() + 1200
            while not (work / name).exists():
                if time.monotonic() >= deadline:
                    raise RuntimeError('timed out waiting for ' + name)
                pump(.1)
        result['window_before'] = win.get_window().get_xid()
        mark('held.json', {'text': entry.get_text(), 'started': time.monotonic()})
        wait_marker('commit')
        result['text_after_hold'] = entry.get_text()
        key('Return')
        result['text_after_commit'] = entry.get_text()
        mark('committed.json', result)
        wait_marker('verify')
        for k in 'su3cl3': key(k)
        key('Return')
        result['text_after_update'] = entry.get_text()
        result['window_after'] = win.get_window().get_xid()
    elif len(sys.argv) > 2:  # keep the composition open (E11/E22), then read again
        pump(float(sys.argv[2]))
        result['text_after_hold'] = entry.get_text()
finally:
    print(json.dumps(result, ensure_ascii=False))
    win.destroy(); pump(.1)
