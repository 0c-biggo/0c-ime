#!/usr/bin/env python3
"""Supervised GTK4 probe for a real desktop; never injects keys or reads other apps.

Start inside the dedicated test user's Wayland session. Type through the desktop
controller, open/close the real tray menu, and compare the committed text in the
JSON result. Run once with native Wayland and once with GTK_IM_MODULE=fcitx.
"""
import argparse
import json
import os
from pathlib import Path

import gi

gi.require_version("Gtk", "4.0")
from gi.repository import GLib, Gtk

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--result", type=Path, required=True)
args = parser.parse_args()
args.result.parent.mkdir(parents=True, exist_ok=True)
app = Gtk.Application(application_id="org.biggo.ime.WaylandProbe")


def activate(application):
    window = Gtk.ApplicationWindow(application=application, title="0c 輸入測試")
    window.set_default_size(680, 340)
    box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=12)
    box.set_margin_start(24)
    box.set_margin_end(24)
    box.set_margin_top(24)
    box.set_margin_bottom(24)
    fields = []
    for label, sensitive in [("第一欄", False), ("第二欄", False), ("敏感欄位（僅輸入測試字串）", True)]:
        box.append(Gtk.Label(label=label, xalign=0))
        field = Gtk.Entry(visibility=not sensitive)
        box.append(field)
        fields.append(field)
    window.set_child(box)
    window.present()
    fields[0].grab_focus()

    def observe():
        state = {"module": os.environ.get("GTK_IM_MODULE", "native"),
                 "active": window.is_active(),
                 "text": [field.get_text() for field in fields],
                 "focus": [field.get_delegate().has_focus() for field in fields]}
        temporary = args.result.with_suffix(".new")
        temporary.write_text(json.dumps(state, ensure_ascii=False) + "\n")
        temporary.replace(args.result)
        return GLib.SOURCE_CONTINUE

    GLib.timeout_add(100, observe)


app.connect("activate", activate)
app.run([])
