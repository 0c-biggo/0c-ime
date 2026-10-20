"""Exercise Fcitx's real Kimpanel protocol on the private GTK desktop (GNOME's menu path)."""
import json
import os
from pathlib import Path
import signal
import subprocess
import time

from gi.repository import Gio, GLib

import menu_stub


def run(report, entry, pump, key, type_text, expect):
    if not Path('/tmp/0c-desktop-sandbox').is_file():
        raise RuntimeError('Kimpanel probe requires the private desktop')
    bus = Gio.bus_get_sync(Gio.BusType.SESSION)
    xml = '''<node><interface name="org.kde.impanel2">
      <method name="SetSpotRect"><arg type="i" direction="in"/><arg type="i" direction="in"/></method>
      <method name="SetLookupTable"><arg type="as" direction="in"/><arg type="as" direction="in"/>
        <arg type="as" direction="in"/><arg type="b" direction="in"/><arg type="b" direction="in"/>
        <arg type="i" direction="in"/><arg type="i" direction="in"/></method>
      </interface></node>'''
    interface = Gio.DBusNodeInfo.new_for_xml(xml).interfaces[0]
    registration = bus.register_object('/org/kde/impanel', interface,
                                      lambda c, s, p, i, m, a, inv: inv.return_value(None), None, None)
    # GNOME 的 Kimpanel 把狀態列動作當成屬性列顯示：RegisterProperties／UpdateProperty 帶「路徑:文字:圖示:提示」。
    props, icon_props = {}, {}
    switch_messages = []
    def received(c, sender, path, interface, signal, params):
        items = params.unpack()[0] if signal == 'RegisterProperties' else [params.unpack()[0]]
        for item in items:
            fields = item.split(':')
            props[fields[0]] = fields[1]
            icon_props[fields[0]] = fields
    subscriptions = [bus.signal_subscribe('org.kde.kimpanel.inputmethod', 'org.kde.kimpanel.inputmethod',
                                          name, '/kimpanel', None, Gio.DBusSignalFlags.NONE, received)
                     for name in ('RegisterProperties', 'UpdateProperty')]
    subscriptions.extend(bus.signal_subscribe(
        'org.kde.kimpanel.inputmethod', 'org.kde.kimpanel.inputmethod',
        name, '/kimpanel', None, Gio.DBusSignalFlags.NONE,
        lambda c, s, p, i, n, a: switch_messages.append(a.unpack()[0]))
        for name in ('UpdatePreeditText', 'UpdateAux'))
    owner = Gio.bus_own_name_on_connection(bus, 'org.kde.impanel', Gio.BusNameOwnerFlags.NONE, None, None)
    def trigger(path):
        bus.emit_signal(None, '/org/kde/impanel', 'org.kde.impanel', 'TriggerProperty', GLib.Variant('(s)', (path,)))
    def wait(text, seconds=35):
        deadline = time.monotonic()+seconds
        while time.monotonic()<deadline:
            if any(text in label for label in props.values()): return dict(props)
            pump(.1)
        raise RuntimeError('Kimpanel did not show '+text+': '+str(props))
    def click(text):
        path = next(p for p, label in props.items() if text in label)
        trigger(path); pump(.15)  # Kimpanel 的動作在 Fcitx 事件迴圈延遲 30ms。
    def check(name): report['checks'].append(name)
    def expect_icon(expected, name):
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            fields = icon_props.get('/Fcitx/im', [])
            if len(fields) > 4 and fields[2] == expected:
                # GNOME Kimpanel's _setIcon unconditionally chooses a nonempty label= hint.
                hints = fields[4].split(',')
                if any(h.startswith('label=') and h != 'label=' for h in hints):
                    raise RuntimeError('GNOME Kimpanel would hide the mode icon behind a text label: '+str(fields))
                check(name)
                return
            pump(.1)
        raise RuntimeError('Kimpanel mode icon did not become '+expected+': '+str(icon_props.get('/Fcitx/im')))
    try:
        pump(.8)
        expect_icon('0c-zhuyin', 'kimpanel_zhuyin_icon_without_forced_text_label')
        def switch_mode(icon, mode, name):
            switch_messages.clear()
            key('Shift_R'); expect_icon(icon, name)
            expected = '靈犀 ('+mode+')'
            if expected not in switch_messages or any(
                    text and text != expected for text in switch_messages):
                raise RuntimeError('Unexpected mode switch popup: '+str(switch_messages))
            fields = icon_props['/Fcitx/im']
            if fields[1] != '靈犀' or fields[3] != mode:
                raise RuntimeError('Unexpected input method display name: '+str(fields))
            check(name+'_name_and_popup')
        switch_mode('0c-english', '英文', 'kimpanel_right_shift_english_icon')
        switch_mode('0c-zhuyin', '注音', 'kimpanel_right_shift_zhuyin_icon')
        pump(.8); entry.set_text(''); type_text('su3'); key('Return'); expect('你','kimpanel_typing')
        # 輸入法選單只有兩列：「⚙ 靈犀注音設定」與更新狀態；其餘設定都在設定程式裡。
        settings_label = '⚙ 靈犀注音設定'
        report['kimpanel_before'] = wait(settings_label); wait('（點此檢查更新）')
        check('kimpanel_settings_and_update_rows_visible')
        if '/Fcitx/zuin-menu' in props: raise RuntimeError('The old submenu is still registered')
        for gone in ('自動更新：','暫停自動更新','神經選字（','提交時補標點（','依前文選字（','操作結果：','檢查並準備更新'):
            if any(gone in label for label in props.values()): raise RuntimeError('Old submenu item is still there: '+gone)
        check('kimpanel_old_submenu_items_are_gone')
        # #169：「輸出簡體字」在 Kimpanel 的屬性列；點了切換，開著時注音模式的標籤是「注簡」（GNOME Kimpanel 用文字取代
        # 「注」圖示），提示「已切換為輸出簡體字」；再點一次回到「注」圖示、沒有標籤。
        wait('輸出簡體字')
        def im_label():
            fields = icon_props.get('/Fcitx/im', [])
            return next((h[len('label='):] for h in (fields[4].split(',') if len(fields) > 4 else []) if h.startswith('label=')), '')
        def wait_label(text, seconds=5):
            deadline = time.monotonic()+seconds
            while time.monotonic()<deadline:
                if im_label() == text: return
                pump(.1)
            raise RuntimeError(f'Kimpanel label did not become {text!r}: {icon_props.get("/Fcitx/im")}')
        switch_messages.clear()
        click('輸出簡體字'); wait_label('注簡')
        report['kimpanel_simplified_on'] = {'im': icon_props.get('/Fcitx/im'), 'messages': list(switch_messages)}
        # Fcitx 5.1.11 起是自訂提示；5.1.7 沒有 showCustomInputMethodInformation，退回輸入法資訊（顯示標籤「注簡」）。
        if not any('已切換為輸出簡體字' in m or '注簡' in m for m in switch_messages):
            raise RuntimeError('Kimpanel did not show the switch notice: '+str(switch_messages))
        check('kimpanel_simplified_label_and_notice')
        entry.set_text(''); type_text('su3'); key('Return'); expect('你','kimpanel_simplified_typing')
        click('輸出簡體字'); wait_label('')
        check('kimpanel_simplified_off_restores_icon')
        updater = menu_stub.Updater()
        try:
            updater.stub()
            click(settings_label); updater.wait_call('open-settings', pump)
            check('kimpanel_settings_click_runs_open_settings')
            wait('（點此檢查更新）'); updater.stub()
            click('（點此檢查更新）'); updater.wait_call('auto -now -notify', pump)
            check('kimpanel_update_click_checks_for_update')
            wait('（點此檢查更新）')
            # 更新檢查跑很久（最長 30 分鐘）時，開設定不能被擋住。
            updater.stub(slow=True)
            click('（點此檢查更新）'); updater.wait_call('auto -now -notify', pump); wait('正在檢查並準備更新')
            updater.calls.write_text('')
            click(settings_label); updater.wait_call('open-settings', pump, 5)
            check('kimpanel_settings_opens_while_update_runs')
            wait('（點此檢查更新）', 30)
            # 開設定失敗：原因顯示在設定列，點同一列重試，成功後原因消失。
            updater.stub(fail=True)
            click(settings_label); wait('開啟失敗：測試用更新工具失敗'); wait('，點此重試）')
            updater.stub()
            click('開啟失敗'); updater.wait_call('open-settings', pump)
            deadline = time.monotonic()+20
            while any('開啟失敗' in label for label in props.values()):
                if time.monotonic()>deadline: raise RuntimeError('Settings failure text was not cleared by a successful retry')
                pump(.3)
            check('kimpanel_settings_failure_visible_and_retry_clears_it')
            updater.stub(fail=True)
            click('（點此檢查更新）'); wait('操作失敗：測試用更新工具失敗'); wait('（點此重試）')
            check('kimpanel_updater_failure_visible_on_update_row')
        finally: updater.restore()
        click('（點此重試）'); wait('更新查詢失敗，稍後再試', 60)  # 私有桌面沒有網路，不能誤報「已是最新」。
        if any('操作失敗' in label for label in props.values()): raise RuntimeError('Failure text is not cleared by the next success')
        check('kimpanel_offline_check_failure_visible_and_retry_clears_error')
        def runtime_state(key):
            return json.loads(subprocess.check_output([str(Path.home()/'.local/bin/0c-update'),'status','--json'],text=True))['runtime'].get(key)
        def kill_worker(name):
            victims=[]
            for proc in Path('/proc').iterdir():
                try:
                    if proc.name.isdigit() and any(a.endswith(name) for a in (proc/'cmdline').read_bytes().split(b'\0')):
                        os.kill(int(proc.name),signal.SIGTERM);victims.append(proc.name)
                except (FileNotFoundError,ProcessLookupError,PermissionError): pass
            if not victims: raise RuntimeError('No private worker to fail: '+name.decode())
        deadline = time.monotonic()+35
        while runtime_state('selection_state')!='ready' or runtime_state('punctuation_state')!='ready':
            if time.monotonic()>deadline: raise RuntimeError('Models never became ready')
            pump(.3)
        # 任一模型退回，設定列都要加註：先只殺補標點，確認神經仍就緒，再殺神經選字。
        kill_worker(b'/punct_runtime.py')
        wait(settings_label+'（模型已退回）')
        if runtime_state('punctuation_state')!='fallback' or runtime_state('selection_state')!='ready':
            raise RuntimeError('Punctuation failure was not isolated')
        check('kimpanel_punctuation_worker_failure_visible_on_settings_entry')
        kill_worker(b'/sequence_runtime.py')
        deadline = time.monotonic()+35
        while runtime_state('selection_state')!='fallback':
            if time.monotonic()>deadline: raise RuntimeError('Neural failure not reported')
            pump(.3)
        wait(settings_label+'（模型已退回）'); check('kimpanel_neural_worker_failure_visible_on_settings_entry')
        report['kimpanel_after_failure'] = dict(props)
    finally:
        Gio.bus_unown_name(owner)
        for subscription in subscriptions: bus.signal_unsubscribe(subscription)
        bus.unregister_object(registration)
