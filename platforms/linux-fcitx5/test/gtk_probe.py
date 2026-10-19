#!/usr/bin/env python3
"""GTK3 + XTest acceptance child. Only runs in desktop_test.py's private mount."""
import ctypes
import json
from pathlib import Path
import subprocess
import sys
import time
import os
import signal
import tray_menu
import menu_stub
if not Path('/tmp/0c-desktop-sandbox').is_file():
    raise SystemExit('Run desktop_test.py; this child requires its isolated desktop')
import gi
gi.require_version('Gtk','3.0')
gi.require_version('GdkX11','3.0')
from gi.repository import Gtk, Gdk, GdkX11, Gio

x=ctypes.CDLL('libX11.so.6'); xtest=ctypes.CDLL('libXtst.so.6')
x.XOpenDisplay.argtypes=[ctypes.c_char_p]; x.XOpenDisplay.restype=ctypes.c_void_p
x.XStringToKeysym.argtypes=[ctypes.c_char_p];x.XStringToKeysym.restype=ctypes.c_ulong
x.XKeysymToKeycode.argtypes=[ctypes.c_void_p,ctypes.c_ulong];x.XKeysymToKeycode.restype=ctypes.c_uint
x.XSetInputFocus.argtypes=[ctypes.c_void_p,ctypes.c_ulong,ctypes.c_int,ctypes.c_ulong]
x.XFlush.argtypes=[ctypes.c_void_p]
xtest.XTestFakeKeyEvent.argtypes=[ctypes.c_void_p,ctypes.c_uint,ctypes.c_int,ctypes.c_ulong]
display=x.XOpenDisplay(None)
if not display: raise RuntimeError('Private X display missing')
win=Gtk.Window(title='0c isolated GTK acceptance'); win.set_default_size(550,180)
box=Gtk.Box(orientation=Gtk.Orientation.VERTICAL); entry=Gtk.Entry(); password=Gtk.Entry()
password.set_visibility(False);password.set_input_purpose(Gtk.InputPurpose.PASSWORD)
box.pack_start(entry,False,False,5);box.pack_start(password,False,False,5);win.add(box);win.show_all()
def pump(seconds=.15):
    deadline=time.monotonic()+seconds
    while time.monotonic()<deadline:
        while Gtk.events_pending(): Gtk.main_iteration_do(False)
        time.sleep(.005)
def key(name):
    sym=x.XStringToKeysym(name.encode())
    if not sym: raise RuntimeError('Unknown test keysym '+name)
    code=x.XKeysymToKeycode(display,sym)
    if not code: raise RuntimeError('Unknown test key '+name)
    xtest.XTestFakeKeyEvent(display,code,1,0);x.XFlush(display);pump(.03)
    xtest.XTestFakeKeyEvent(display,code,0,0);x.XFlush(display);pump()
def type_text(text):
    for c in text: key(c)
def focus(widget):
    widget.grab_focus();x.XSetInputFocus(display,win.get_window().get_xid(),1,0);x.XFlush(display);pump(.5)
report={'passed':False,'checks':[],'error':'','gtk':f'{Gtk.MAJOR_VERSION}.{Gtk.MINOR_VERSION}.{Gtk.MICRO_VERSION}'}
def expect(text,name,widget=entry):
    if widget.get_text()!=text: raise RuntimeError(f'{name}: expected {text!r}, got {widget.get_text()!r}')
    report['checks'].append(name)
try:
    pump(.5) # Complete the GTK connection's map requests before XTest sets focus.
    focus(entry)
    subprocess.run(['fcitx5-remote','-s','zuin'],check=True);subprocess.run(['fcitx5-remote','-o'],check=True);pump(.8)
    report['focus']={'entry':entry.has_focus(),'window':win.is_active()}
    if not entry.has_focus() or not win.is_active(): raise RuntimeError('Private GTK test field did not gain focus')
    if '--icons' in sys.argv[2:]:
        def expect_icon(expected, name):
            until = time.monotonic() + 5
            while time.monotonic() < until:
                actual = tray_menu.icon_name('/tmp/0c-tray')
                if actual == expected:
                    report['checks'].append(name)
                    return
                pump(.1)
            raise RuntimeError(f'{name}: expected {expected!r}, got {actual!r}')
        expect_icon('0c-zhuyin', 'tray_zhuyin_icon')
        for name in ('0c-zhuyin', '0c-english'):
            info = Gtk.IconTheme.get_default().lookup_icon(name, 16, Gtk.IconLookupFlags.FORCE_SVG)
            expected = Path.home() / f'.local/share/icons/hicolor/scalable/status/{name}.svg'
            if info is None or Path(info.get_filename()) != expected:
                raise RuntimeError('Installed color icon was not resolved: ' + name)
            pixbuf = info.load_icon()
            if pixbuf.get_width() != 16 or pixbuf.get_height() != 16:
                raise RuntimeError('Color icon did not render at 16px: ' + name)
            pixbuf.savev(str(Path(sys.argv[1]).parent / f'{name}-16.png'), 'png', [], [])
        report['checks'].append('tray_brand_icons_resolve_and_render_at_16px')
        for name in ('0c-zhuyin', '0c-english'):
            expected = Path.home() / f'.local/share/icons/hicolor/scalable/status/{name}.svg'
            if expected.with_name(name + '-symbolic.svg').exists():
                raise RuntimeError('Old symbolic icon still shadows the color icon: ' + name)
            baseline = Gtk.IconTheme.get_default().lookup_icon(name, 16, Gtk.IconLookupFlags.FORCE_SVG).load_icon()
            # Same name fallback chain as GNOME Kimpanel's createIcon().
            icon = Gio.ThemedIcon.new_with_default_fallbacks(name + '-symbolic-hack')
            for flags in (Gtk.IconLookupFlags.FORCE_SVG,
                          Gtk.IconLookupFlags.FORCE_SVG | Gtk.IconLookupFlags.FORCE_SYMBOLIC):
                info = Gtk.IconTheme.get_default().lookup_by_gicon(icon, 16, flags)
                if info is None or Path(info.get_filename()) != expected:
                    raise RuntimeError('Kimpanel fallback did not choose the color icon: ' + name)
                for mode, foreground in (('dark', '#ffffff'), ('light', '#202020')):
                    color = Gdk.RGBA()
                    color.parse(foreground)
                    pixbuf, symbolic = info.load_symbolic(color, None, None, None)
                    if symbolic or pixbuf.get_pixels() != baseline.get_pixels():
                        raise RuntimeError(f'Kimpanel recolored the {mode} brand icon: ' + name)
                    offset = 8 * pixbuf.get_rowstride() + pixbuf.get_n_channels()
                    background = tuple(pixbuf.get_pixels()[offset:offset + 3])
                    wanted = (7, 93, 83) if name == '0c-zhuyin' else (229, 236, 232)
                    if background != wanted:
                        raise RuntimeError('Brand background color was lost: ' + name)
                    pixbuf.savev(str(Path(sys.argv[1]).parent / f'{name}-{mode}.png'), 'png', [], [])
        report['checks'].append('kimpanel_fallback_preserves_brand_colors_in_light_and_dark')
        key('Shift_R'); expect_icon('0c-english', 'tray_right_shift_english_icon')
        type_text('abc'); expect('abc', 'tray_english_icon_matches_input')
        key('Shift_R'); expect_icon('0c-zhuyin', 'tray_right_shift_zhuyin_icon')
        key('Shift_R'); expect_icon('0c-english', 'tray_english_icon_before_focus_change')
        focus(password); focus(entry)
        # Session::activate returns to Zhuyin on reactivation; the icon must follow it.
        expect_icon('0c-zhuyin', 'tray_zhuyin_icon_matches_reactivation')
        subprocess.run(['fcitx5-remote', '-s', 'keyboard-us'], check=True); pump(.5)
        if tray_menu.icon_name('/tmp/0c-tray').startswith('0c-'):
            raise RuntimeError('System keyboard unexpectedly uses an IME mode icon')
        report['checks'].append('tray_system_keyboard_keeps_framework_icon')
        subprocess.run(['fcitx5-remote', '-s', 'zuin'], check=True); pump(.5)
        expect_icon('0c-zhuyin', 'tray_zhuyin_icon_restored_after_im_switch')
        entry.set_text('')
    type_text('su3cl3');expect('','preedit_not_committed')
    key('Return');expect('你好','gtk_commit_nihao')
    entry.set_text('');type_text('su3');key('Escape');expect('','gtk_cancel')
    type_text('a86qi6');key('Tab');key('Return');expect('麻婆豆腐','gtk_tab_completion')
    entry.set_text('');type_text('s');key('BackSpace');expect('','gtk_backspace_pending')
    key('Shift_R');type_text('abc')
    if '--english-autocomplete' in sys.argv[2:]: key('Return')
    expect('abc','gtk_right_shift_english')
    key('Shift_R');entry.set_text('');type_text('su3');key('Down');key('Return');key('Return');expect('你','gtk_candidate_enter')
    focus(password);type_text('su3cl3');expect('su3cl3','gtk_password_passthrough',password);password.set_text('')
    focus(entry);entry.set_text('');type_text('cl3');key('Return');expect('好','gtk_return_from_password')
    # #75：數字鍵盤不是注音鍵。Xvfb 的 NumLock 預設關閉，先打開才會送 KP_1 等 keysym，測完關回。
    # 舊外殼 fixture（--shell-legacy）用的是 dev.40 的 addon，沒有 #75 的修正，只驗遷移。
    if '--shell-legacy' not in sys.argv[2:]:
        key('Num_Lock')
        entry.set_text('');key('KP_1');key('KP_0');key('KP_Subtract');expect('10-','keypad_idle_goes_to_app')
        entry.set_text('');type_text('su3');key('KP_1');expect('','keypad_stays_in_preedit');key('Return');expect('你1','keypad_inserted_half_width')
        entry.set_text('');type_text('su');key('KP_2');key('Return');expect('ㄋㄧ2','keypad_keeps_pending_zhuyin')
        entry.set_text('');type_text('su3');key('Down');key('KP_1');key('Return');expect('你','keypad_selects_candidate')
        entry.set_text('');type_text('su3');key('KP_Enter');expect('你','keypad_enter_commits')
        key('Num_Lock');entry.set_text('')

    if '--english-autocomplete' in sys.argv[2:]:
        entry.set_text(''); key('Shift_R')
        activated=[]
        entry.connect('activate',lambda *_: activated.append(entry.get_text()))
        type_text('hel'); expect('', 'english_prefix_is_preedit')
        key('Return'); expect('hel', 'english_enter_keeps_raw_prefix')
        if activated != ['hel']: raise RuntimeError('English Enter did not activate App after commit: '+repr(activated))
        report['checks'].append('english_enter_runs_native_app_action_once')
        entry.set_text('');type_text('hel');key('Tab');key('Return')
        chosen=entry.get_text()
        if not chosen.startswith('hel') or chosen=='hel' or len(activated)!=2:
            raise RuntimeError('English Tab completion/native Enter failed: '+repr((chosen,activated)))
        report['checks'].append('english_tab_explicitly_completes_before_native_enter')
        entry.set_text('');type_text('hel');key('Escape');key('Return');expect('hel','english_escape_dismisses_without_changing_prefix')
        entry.set_text('');type_text('h');key('Tab');expect('h','english_tab_without_candidate_commits_raw')
        if not password.has_focus(): raise RuntimeError('Tab without candidate did not move native focus')
        report['checks'].append('english_tab_without_candidate_moves_native_focus')
        focus(entry);entry.set_text('');key('Shift_R');type_text('hel')
        control=x.XKeysymToKeycode(display,x.XStringToKeysym(b'Control_L'))
        xtest.XTestFakeKeyEvent(display,control,1,0);x.XFlush(display);pump(.03)
        key('a')
        xtest.XTestFakeKeyEvent(display,control,0,0);x.XFlush(display);pump()
        expect('hel','english_shortcut_commits_before_app_selection')
        if entry.get_selection_bounds() != (0,3): raise RuntimeError('Ctrl+A did not select the committed word')
        report['checks'].append('english_ctrl_a_keeps_native_app_selection')
        key('BackSpace');expect('','english_native_backspace_deletes_selection')
        type_text('hel');key('space');expect('hel ','english_space_never_auto_accepts')
        for boundary in ('Home','End'):
            entry.set_text('');type_text('word');key('period');expect('word.','english_token_blocks_before_'+boundary)
            key(boundary)
            wanted=0 if boundary=='Home' else 5
            if entry.get_position()!=wanted: raise RuntimeError(boundary+' did not retain native cursor movement')
            report['checks'].append('english_'+boundary.lower()+'_keeps_native_cursor')
            type_text('hel');expect('word.','english_'+boundary.lower()+'_clears_token_block')
            key('Return');expect('helword.' if boundary=='Home' else 'word.hel','english_'+boundary.lower()+'_commits_at_native_cursor')
        # Shift+Tab must reach native focus traversal even with visible candidates.
        entry.set_text('');type_text('hel')
        shift=x.XKeysymToKeycode(display,x.XStringToKeysym(b'Shift_L'))
        xtest.XTestFakeKeyEvent(display,shift,1,0);x.XFlush(display);pump(.03)
        key('Tab')
        xtest.XTestFakeKeyEvent(display,shift,0,0);x.XFlush(display);pump()
        expect('hel','english_shift_tab_keeps_raw_prefix')
        if entry.has_focus(): raise RuntimeError('Shift+Tab did not move native focus')
        report['checks'].append('english_shift_tab_moves_native_focus')
        focus(entry);key('Shift_R')
    if '--left-shift' in sys.argv[2:]:
        def current():
            return subprocess.run(['fcitx5-remote','-n'],capture_output=True,text=True).stdout.strip()
        def running():
            doc=json.loads(subprocess.check_output([str(Path.home()/'.local/bin/0c-update'),'settings','--json'],text=True))
            # left-shift 從 #117 起是不列出的舊鍵（改成 toggle-key），只能看 toggle-key
            return next(i for i in doc['items'] if i['key']=='toggle-key').get('running')
        # 預設 toggle-key=right：外殼不攔左 Shift，照 Fcitx5 的 AltTriggerKeys 暫時切到第一個輸入法，再按一次回來。
        entry.set_text('');key('Shift_L')
        if current()!='keyboard-us': raise RuntimeError('Left Shift (off) no longer reaches Fcitx5: '+current())
        key('Shift_L')
        if current()!='zuin': raise RuntimeError('Fcitx5 alt trigger did not return to zuin: '+current())
        report['checks'].append('left_shift_off_keeps_fcitx_alt_trigger')
        subprocess.run([str(Path.home()/'.local/bin/0c-update'),'set','toggle-key','both','-apply'],check=True,capture_output=True)
        deadline=time.monotonic()+30
        while running()!='both':
            if time.monotonic()>deadline: raise RuntimeError('Engine did not load toggle-key=both: '+repr(running()))
            key('Escape');pump(.5) # 套用會結束引擎，由外殼的下一個請求重新啟動
        report['checks'].append('left_shift_setting_applied')
        entry.set_text('');type_text('su3');key('Shift_L')
        if current()!='zuin': raise RuntimeError('Left Shift (on) still switched the input method: '+current())
        type_text('ab');key('Shift_L');key('Return');expect('你ab','left_shift_toggles_mode_keeping_composition')
        entry.set_text('')
        shift=x.XKeysymToKeycode(display,x.XStringToKeysym(b'Shift_L'))
        xtest.XTestFakeKeyEvent(display,shift,1,0);x.XFlush(display);pump(.03)
        key('a')
        xtest.XTestFakeKeyEvent(display,shift,0,0);x.XFlush(display);pump()
        type_text('su3');key('Return')
        if not entry.get_text().endswith('你') or current()!='zuin':
            raise RuntimeError('Left Shift chord changed mode: '+repr((entry.get_text(),current())))
        report['checks'].append('left_shift_chord_does_not_toggle')
        entry.set_text('')
        key('Shift_R');entry.set_text('')

    if '--shell-update' in sys.argv[2:]:
        import shell_update_probe
        shell_update_probe.run(report,entry,pump,key,type_text,expect)

    if '--kimpanel' in sys.argv[2:]:
        import kimpanel_probe
        kimpanel_probe.run(report,entry,pump,key,type_text,expect)

    if '--menu' in sys.argv[2:]:
        def menu_items(): return tray_menu.layout('/tmp/0c-tray')[3]
        def wait_menu(text, seconds=35):
            until=time.monotonic()+seconds
            while time.monotonic()<until:
                items=menu_items()
                if any(text in i['label'] for i in items): return items
                pump(.2)
            raise RuntimeError('Menu did not show '+text+': '+str(items))
        def click_menu(text):
            bus,service,menu,items=tray_menu.layout('/tmp/0c-tray')
            item=next(i for i in items if text in i['label'])
            from gi.repository import GLib
            bus.call_sync(service,menu,'com.canonical.dbusmenu','Event',GLib.Variant('(isvu)',(item['id'],'clicked',GLib.Variant('i',0),0)),None,0,5000,None)
        def status():
            return json.loads(subprocess.check_output([str(Path.home()/'.local/bin/0c-update'),'status','--json'],text=True))
        # 輸入法選單只有兩列：「⚙ 靈犀注音設定」與更新狀態；其餘設定都在設定程式裡。
        settings_label='⚙ 靈犀注音設定'
        items=wait_menu(settings_label)
        report['menu_before']=items
        report['checks'].append('tray_settings_entry_visible')
        wait_menu('（點此檢查更新）')
        report['checks'].append('tray_update_row_visible')
        labels=[i['label'] for i in menu_items()]
        if sum(l.startswith(settings_label) for l in labels)!=1: raise RuntimeError('Settings entry is not unique: '+str(labels))
        for gone in ('自動更新：','暫停自動更新','神經選字（','提交時補標點（','依前文選字（','操作結果：','檢查並準備更新','目前神經選字：'):
            if any(gone in l for l in labels): raise RuntimeError('Old submenu item is still there: '+gone)
        report['checks'].append('tray_old_submenu_items_are_gone')
        # #125：0.2.0 起「我的詞彙…」一律出現在設定列與更新列中間；舊版更新器（status 沒有 features）時藏起來。
        vocab_label='我的詞彙…'
        labels=[i['label'] for i in wait_menu(vocab_label,15)]
        order=[next(k for k,l in enumerate(labels) if l.startswith(t)) for t in (settings_label,vocab_label)]
        update_row=next(k for k,l in enumerate(labels) if '（點此' in l or '正在' in l)
        if not order[0]<order[1]<update_row: raise RuntimeError('Vocab entry is not between settings and update rows: '+str(labels))
        report['menu_vocab']=labels
        report['checks'].append('tray_vocab_entry_between_settings_and_update')
        updater=menu_stub.Updater()
        try:
            updater.stub()
            click_menu(vocab_label); updater.wait_call('open-settings vocab',pump)
            report['checks'].append('tray_vocab_click_runs_open_settings_vocab')
            updater.stub(old_status=True)
            until=time.monotonic()+15
            while any(i['label'].startswith(vocab_label) for i in menu_items()):
                if time.monotonic()>until: raise RuntimeError('Vocab entry stayed with an updater that has no features.vocab')
                pump(.3)
            report['checks'].append('tray_vocab_entry_hidden_for_old_updater')
            updater.stub()
            click_menu(settings_label); updater.wait_call('open-settings',pump)
            report['checks'].append('tray_settings_click_runs_open_settings')
            wait_menu('（點此檢查更新）')  # 上一個動作結束後才能再點
            updater.stub()
            click_menu('（點此檢查更新）'); updater.wait_call('auto -now -notify',pump)
            report['checks'].append('tray_update_click_checks_for_update')
            wait_menu('（點此檢查更新）')
            # 更新檢查跑很久（最長 30 分鐘）時，開設定不能被擋住。
            updater.stub(slow=True)
            click_menu('（點此檢查更新）'); updater.wait_call('auto -now -notify',pump); wait_menu('正在檢查並準備更新')
            updater.calls.write_text('')
            click_menu(settings_label); updater.wait_call('open-settings',pump,5)
            report['checks'].append('tray_settings_opens_while_update_runs')
            wait_menu('（點此檢查更新）',30)
            # 開設定失敗：原因顯示在設定列，點同一列重試，成功後原因消失。
            updater.stub(fail=True)
            click_menu(settings_label); wait_menu('開啟失敗：測試用更新工具失敗'); wait_menu('，點此重試）')
            updater.stub()
            click_menu('開啟失敗'); updater.wait_call('open-settings',pump)
            until=time.monotonic()+20
            while any('開啟失敗' in i['label'] for i in menu_items()):
                if time.monotonic()>until: raise RuntimeError('Settings failure text was not cleared by a successful retry')
                pump(.3)
            report['checks'].append('tray_settings_failure_visible_and_retry_clears_it')
            updater.stub(fail=True)
            click_menu('（點此檢查更新）')
            wait_menu('操作失敗：測試用更新工具失敗'); wait_menu('（點此重試）')
            report['checks'].append('tray_updater_failure_visible_on_update_row')
        finally: updater.restore()
        click_menu('（點此重試）')
        wait_menu('更新查詢失敗，稍後再試',60)  # 私有桌面沒有網路，不能誤報「已是最新」。
        if not status().get('last_error'): raise RuntimeError('Offline check missing error evidence')
        if any('操作失敗' in i['label'] for i in menu_items()): raise RuntimeError('Failure text is not cleared by the next success')
        report['checks'].append('tray_check_offline_failure_visible_and_retry_clears_error')
        def runtime_state(key): return status()['runtime'].get(key)
        until=time.monotonic()+35
        while runtime_state('selection_state')!='ready' or runtime_state('punctuation_state')!='ready':
            if time.monotonic()>until: raise RuntimeError('Models never became ready: '+str(status()['runtime']))
            pump(.3)
        def kill_worker(name):
            victims=[]
            for proc in Path('/proc').iterdir():
                if proc.name.isdigit():
                    try:
                        args=(proc/'cmdline').read_bytes().split(b'\0')
                        if any(a.endswith(name) for a in args):
                            os.kill(int(proc.name),signal.SIGTERM);victims.append(proc.name)
                    except (FileNotFoundError,ProcessLookupError,PermissionError): pass
            if not victims: raise RuntimeError('No isolated worker to fail: '+name.decode())
        # 設定列的「（模型已退回）」任一模型退回就要出現：先只殺補標點，確認神經仍就緒，再殺神經選字。
        kill_worker(b'/punct_runtime.py')
        wait_menu(settings_label+'（模型已退回）')
        if runtime_state('punctuation_state')!='fallback' or runtime_state('selection_state')!='ready':
            raise RuntimeError('Punctuation failure was not isolated: '+str(status()['runtime']))
        report['checks'].append('tray_punctuation_worker_failure_visible_on_settings_entry')
        kill_worker(b'/sequence_runtime.py')
        until=time.monotonic()+35
        while runtime_state('selection_state')!='fallback':
            if time.monotonic()>until: raise RuntimeError('Neural failure not reported: '+str(status()['runtime']))
            pump(.3)
        items=wait_menu(settings_label+'（模型已退回）')
        report['checks'].append('tray_neural_worker_failure_visible_on_settings_entry')
        report['menu_after_failure']=items
    report['passed']=True
except Exception as error: report['error']=str(error)
finally:
    Path(sys.argv[1]).write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n');win.destroy();pump(.1)
if not report['passed']: raise SystemExit(1)
