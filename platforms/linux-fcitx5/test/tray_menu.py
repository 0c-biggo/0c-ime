#!/usr/bin/env python3
"""Drive the Fcitx5 tray menu the way a desktop tray does (update E2E, #13).

`watcher FILE`: owns org.kde.StatusNotifierWatcher on the session bus (a minimal tray host) and writes the
registered item's bus name to FILE. Fcitx5's notificationitem addon then exports its menu over
com.canonical.dbusmenu, exactly what GNOME AppIndicator / KDE Plasma read.
`list FILE`: prints the menu as JSON [{id, label, toggle, checked}] (flattened).
`click FILE SUBSTRING`: sends a dbusmenu "clicked" event to the first item whose label contains SUBSTRING.
"""
import json
import sys
import time
from gi.repository import Gio, GLib

WATCHER_XML = """<node><interface name="org.kde.StatusNotifierWatcher">
<method name="RegisterStatusNotifierItem"><arg type="s" direction="in"/></method>
<method name="RegisterStatusNotifierHost"><arg type="s" direction="in"/></method>
<property name="RegisteredStatusNotifierItems" type="as" access="read"/>
<property name="IsStatusNotifierHostRegistered" type="b" access="read"/>
<property name="ProtocolVersion" type="i" access="read"/>
<signal name="StatusNotifierItemRegistered"><arg type="s"/></signal>
<signal name="StatusNotifierHostRegistered"/>
</interface></node>"""


def watcher(path):
    bus = Gio.bus_get_sync(Gio.BusType.SESSION)
    items = []

    def call(conn, sender, obj, iface, method, params, inv):
        if method == 'RegisterStatusNotifierItem':
            item = params.unpack()[0]
            service = item if item.startswith(':') or '.' in item and not item.startswith('/') else sender
            obj_path = item if item.startswith('/') else '/StatusNotifierItem'
            items.append(f'{service} {obj_path}')
            open(path, 'w').write(items[-1] + '\n')
            conn.emit_signal(None, '/StatusNotifierWatcher', 'org.kde.StatusNotifierWatcher', 'StatusNotifierItemRegistered',
                             GLib.Variant('(s)', (item,)))
        inv.return_value(None)

    def prop(conn, sender, obj, iface, name):
        return {'RegisteredStatusNotifierItems': GLib.Variant('as', items),
                'IsStatusNotifierHostRegistered': GLib.Variant('b', True),
                'ProtocolVersion': GLib.Variant('i', 0)}[name]

    info = Gio.DBusNodeInfo.new_for_xml(WATCHER_XML).interfaces[0]
    bus.register_object('/StatusNotifierWatcher', info, call, prop, None)
    Gio.bus_own_name_on_connection(bus, 'org.kde.StatusNotifierWatcher', Gio.BusNameOwnerFlags.NONE, None, None)
    GLib.MainLoop().run()


def menu_target(path):
    service, obj = open(path).read().split()
    bus = Gio.bus_get_sync(Gio.BusType.SESSION)
    menu = bus.call_sync(service, obj, 'org.freedesktop.DBus.Properties', 'Get',
                         GLib.Variant('(ss)', ('org.kde.StatusNotifierItem', 'Menu')), None, 0, 5000, None).unpack()[0]
    return bus, service, menu


def icon_name(path):
    service, obj = open(path).read().split()
    bus = Gio.bus_get_sync(Gio.BusType.SESSION)
    return bus.call_sync(service, obj, 'org.freedesktop.DBus.Properties', 'Get',
                         GLib.Variant('(ss)', ('org.kde.StatusNotifierItem', 'IconName')),
                         None, 0, 5000, None).unpack()[0]


def flatten(node, out):
    ident, props, children = node
    label = props.get('label', '')
    if label or props.get('type') != 'separator':
        out.append({'id': ident, 'label': label.replace('_', ''), 'toggle': props.get('toggle-type', ''),
                    'checked': props.get('toggle-state', 0) == 1})
    for child in children:
        flatten(child, out)


def layout(path):
    bus, service, menu = menu_target(path)
    # 選單內容在開啟時才向 Fcitx 取得：先送 AboutToShow，如同系統匣開啟選單。
    bus.call_sync(service, menu, 'com.canonical.dbusmenu', 'AboutToShow', GLib.Variant('(i)', (0,)), None, 0, 5000, None)
    _, root = bus.call_sync(service, menu, 'com.canonical.dbusmenu', 'GetLayout',
                            GLib.Variant('(iias)', (0, -1, [])), None, 0, 5000, None).unpack()
    out = []
    flatten(root, out)
    # 子選單（輸入法的狀態列動作通常在子選單裡）也要展開：對每個項目送 AboutToShow 後重取
    for item in list(out):
        try:
            bus.call_sync(service, menu, 'com.canonical.dbusmenu', 'AboutToShow', GLib.Variant('(i)', (item['id'],)), None, 0, 5000, None)
        except GLib.Error:
            pass
    _, root = bus.call_sync(service, menu, 'com.canonical.dbusmenu', 'GetLayout',
                            GLib.Variant('(iias)', (0, -1, [])), None, 0, 5000, None).unpack()
    out = []
    flatten(root, out)
    return bus, service, menu, out


def main():
    mode, path = sys.argv[1], sys.argv[2]
    if mode == 'watcher':
        watcher(path)
    elif mode == 'list':
        print(json.dumps(layout(path)[3], ensure_ascii=False))
    elif mode == 'click':
        bus, service, menu, items = layout(path)
        for item in items:
            if sys.argv[3] in item['label']:
                bus.call_sync(service, menu, 'com.canonical.dbusmenu', 'Event',
                              GLib.Variant('(isvu)', (item['id'], 'clicked', GLib.Variant('s', ''), int(time.time()))),
                              None, 0, 5000, None)
                print(json.dumps(item, ensure_ascii=False))
                return
        raise SystemExit(f'no menu item contains {sys.argv[3]!r}: {[i["label"] for i in items]}')


if __name__ == '__main__':
    main()
