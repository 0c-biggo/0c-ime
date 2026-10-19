#!/usr/bin/env python3
"""Run a real GTK/Fcitx desktop in a private home mount, PID namespace, D-Bus and Xvfb.

Requires bubblewrap, Xvfb, D-Bus, Fcitx5/GTK3 IM module, Python GI and libXtst.
Does not install into or restart the caller's desktop. No network is available inside.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time

from fcitx_env import addon_env

def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def inside(menu=False, shell_update=False, shell_manual=False, shell_recover=False,shell_legacy=False,kimpanel=False,icons=False,english_autocomplete=False,left_shift=False):
    if not Path('/tmp/0c-desktop-sandbox').is_file():
        raise RuntimeError('The inner test must run in the private bubblewrap mount')
    home = Path.home()  # Same path, backed by a new private mount; HOME is never reassigned.
    result = Path('/tmp/0c-results')
    report = {'passed': False, 'checks': [], 'error': ''}
    processes = []
    env = dict(os.environ)
    for key, suffix in [('XDG_CONFIG_HOME','.config'),('XDG_DATA_HOME','.local/share'),('XDG_CACHE_HOME','.cache'),('XDG_STATE_HOME','.local/state')]:
        env[key] = str(home / suffix)
        (home / suffix).mkdir(parents=True, exist_ok=True)
    runtime = Path('/tmp/runtime'); runtime.mkdir(mode=0o700)
    env.update(XDG_RUNTIME_DIR=str(runtime), GTK_IM_MODULE='fcitx', GDK_BACKEND='x11', QT_IM_MODULE='fcitx', XMODIFIERS='@im=fcitx')
    for key in ('WAYLAND_DISPLAY','GNOME_SETUP_DISPLAY','XAUTHORITY','ZUIN_TRACE','ZUIN_TRACE_DIR'):
        env.pop(key,None)
    env['LIBGL_ALWAYS_SOFTWARE']='1'
    Path('/tmp/.X11-unix').mkdir(mode=0o1777)
    def install(action, *extra):
        subprocess.run(['/usr/bin/python3','/tmp/0c-bundle/install.py',action,'--home',str(home),*extra],env=env,check=True)
    def running(pid):
        try: return not Path(f'/proc/{pid}/stat').read_text().rpartition(') ')[2].startswith('Z ')
        except (FileNotFoundError,ProcessLookupError): return False
    def stop_desktop():
        for p in reversed(processes):
            if p.poll() is None:
                p.terminate()
                try: p.wait(timeout=10)
                except subprocess.TimeoutExpired: p.kill(); p.wait()
        processes.clear()
        # /proc is this private PID namespace; these are only engines spawned by this test's Fcitx.
        engines=[]
        for proc in Path('/proc').iterdir():
            try:
                if proc.name.isdigit() and (proc/'comm').read_text().strip() in ('0c-server','fcitx5'):
                    engines.append(int(proc.name)); os.kill(int(proc.name),signal.SIGTERM)
            except (FileNotFoundError, ProcessLookupError): pass
        deadline=time.monotonic()+5
        while any(running(pid) for pid in engines) and time.monotonic()<deadline:
            time.sleep(.05)
    try:
        if icons:
            conf = home / '.config/fcitx5/conf'
            conf.mkdir(parents=True, exist_ok=True)
            for name in ('classicui.conf', 'kimpanel.conf'):
                (conf/name).write_text('# existing preference\nPreferTextIcon=True\n')
        install('install'); report['checks'].append('clean_profile_install')
        if menu or icons or kimpanel:
            # 故障注入會分別停止兩個模型；補標點 Beta 預設關閉，測試須明確啟用。
            subprocess.run([str(home / '.local/bin/0c-update'), 'set', 'punctuation', 'on'],
                           env=env, check=True)
        if english_autocomplete:
            subprocess.run([str(home / '.local/bin/0c-update'), 'set', 'english-autocomplete', 'on'], env=env, check=True)
        if icons:
            for name in ('classicui.conf', 'kimpanel.conf'):
                if (conf/name).read_text() != '# existing preference\nPreferTextIcon=False\n':
                    raise RuntimeError('Installer did not enable named icons for '+name)
            report['checks'].append('install_enables_named_icons_on_both_panels')
        config=home/'.config/fcitx5'; config.mkdir(parents=True,exist_ok=True)
        profile='[Groups/0]\nName=Default\nDefault Layout=us\nDefaultIM=zuin\n\n[Groups/0/Items/0]\nName=keyboard-us\nLayout=\n\n[Groups/0/Items/1]\nName=zuin\nLayout=\n\n[GroupOrder]\n0=Default\n'
        if shell_update: profile=profile.replace('[GroupOrder]', '[Groups/0/Items/2]\nName=pinyin\nLayout=\n\n[GroupOrder]')
        (config/'profile').write_text(profile)
        # 新版外殼的 addon 設定寫絕對路徑，不設 FCITX_ADDON_DIRS 才驗得到使用者的情況；舊套件才補（fcitx_env）。
        env.pop('FCITX_ADDON_DIRS',None); env.update(addon_env(home))
        # Xvfb is a fresh server, with its own socket in the private /tmp.
        processes.append(subprocess.Popen(['Xvfb',':99','-screen','0','1280x800x24','-nolisten','tcp','-extension','GLX'],env=env))
        env['DISPLAY']=':99'
        deadline=time.monotonic()+10
        while not Path('/tmp/.X11-unix/X99').exists() and time.monotonic()<deadline: time.sleep(.05)
        if processes[0].poll() is not None: raise RuntimeError('Private Xvfb failed to start')
        if menu or icons:
            processes.append(subprocess.Popen(['/usr/bin/python3','/tmp/0c-test/tray_menu.py','watcher','/tmp/0c-tray'],env=env))
            time.sleep(.5)
        processes.append(subprocess.Popen(['fcitx5','-D','--disable=wayland'],env=env))
        time.sleep(2)
        subprocess.run(['/usr/bin/python3','/tmp/0c-test/gtk_probe.py','/tmp/0c-results/gtk-result.json'] + (['--english-autocomplete'] if english_autocomplete else []) + (['--left-shift'] if left_shift else []) + (['--icons'] if icons else []) + (['--menu'] if menu else []) + (['--kimpanel'] if kimpanel else []) + (['--shell-update'] if shell_update else []) + (['--shell-manual'] if shell_manual else []) + (['--shell-recover'] if shell_recover else []) + (['--shell-legacy'] if shell_legacy else []),env=env,check=True,timeout=240)
        gtk=json.loads((result/'gtk-result.json').read_text())
        report['checks'].extend(gtk['checks'])
        stop_desktop()
        user=home/'.local/share/zuin/user.json'
        if not user.is_file(): raise RuntimeError('Test engine did not persist its isolated learning data')
        before=digest(user)
        trace=home/'.local/state/zuin/trace'
        if trace.exists() and list(trace.glob('*.jsonl')): raise RuntimeError('Default desktop unexpectedly created input trace')
        report['checks'].append('default_trace_disabled')
        install('install')
        if digest(user)!=before: raise RuntimeError('Upgrade changed isolated learning data')
        report['checks'].append('upgrade_preserves_learning')
        receipt=json.loads((home/'.local/share/zuin/install-receipt.json').read_text())
        # Installer receipt records the backup that belongs to this exact upgrade.
        backup=receipt['backup']
        install('rollback','--backup',backup)
        if digest(user)!=before: raise RuntimeError('Rollback changed isolated learning data')
        report['checks'].append('rollback_preserves_learning')
        install('uninstall')
        if digest(user)!=before or not (home/'.local/share/zuin/data/manifest.json').is_file(): raise RuntimeError('Uninstall removed data')
        if (home/'.local/lib/fcitx5/lib0c.so').exists(): raise RuntimeError('Uninstall left addon installed')
        report['checks'].append('uninstall_preserves_learning_and_dictionary')
        report['passed']=True
    except Exception as error:
        report['error']=str(error)
        raise
    finally:
        stop_desktop()
        (result/'desktop-result.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--bundle',type=Path)
    parser.add_argument('--result',type=Path)
    parser.add_argument('--shell-update',type=Path,help='另驗證完整外殼更新，不重登（隔離套件 B）')
    parser.add_argument('--shell-updater',type=Path,help='以開發更新器驗證隔離外殼更新')
    parser.add_argument('--shell-legacy',action='store_true',help='驗證舊外殼首次手動遷移')
    parser.add_argument('--shell-manual',action='store_true',help='shell-update 改驗證手動模式立即完成')
    parser.add_argument('--shell-recover',action='store_true',help='shell-update 中止更新器後接續')
    parser.add_argument('--shell-fault',type=Path,help='shell-update 的故障套件 F')
    parser.add_argument('--menu',action='store_true',help='另驗證系統匣模型狀態、設定切換與 worker 失效；只在隔離桌面執行')
    parser.add_argument('--icons',action='store_true',help='另驗證注／英圖示、模式切換與圖示載入；只在隔離桌面執行')
    parser.add_argument('--kimpanel',action='store_true',help='另驗證 GNOME Kimpanel 選單協定與設定操作；只在隔離桌面執行')
    parser.add_argument('--english-autocomplete',action='store_true',help='隔離驗證英文補字與 App Enter／Tab／快捷鍵')
    parser.add_argument('--left-shift',action='store_true',help='隔離驗證左 Shift 設定：關閉時照 Fcitx5，開啟後切換注／英')
    parser.add_argument('--inside',action='store_true',help=argparse.SUPPRESS)
    args=parser.parse_args()
    if args.inside:
        inside(menu=args.menu, shell_update=bool(args.shell_update),shell_manual=args.shell_manual,shell_recover=args.shell_recover,shell_legacy=args.shell_legacy,kimpanel=args.kimpanel,icons=args.icons,english_autocomplete=args.english_autocomplete,left_shift=args.left_shift); return
    if not args.bundle: parser.error('--bundle is required')
    bundle=args.bundle.resolve(strict=True)
    result=(args.result or Path('build/linux/validation')/time.strftime('%Y%m%d-%H%M%S')).resolve()
    result.mkdir(parents=True,exist_ok=False)
    with tempfile.TemporaryDirectory(prefix='0c-desktop-') as temp:
        scratch=Path(temp); (scratch/'home').mkdir(); (scratch/'marker').touch()
        # This bus is private to the network/PID/mount namespace. The host AppArmor
        # policy filesystem cannot be queried through the read-only namespace.
        (scratch/'bus.conf').write_text('<busconfig><type>session</type><listen>unix:tmpdir=/tmp</listen>'
            '<auth>EXTERNAL</auth><apparmor mode="disabled"/><policy context="default">'
            '<allow send_destination="*"/><allow receive_sender="*"/><allow own="*"/></policy></busconfig>')
        command=['bwrap','--die-with-parent','--new-session','--unshare-pid','--unshare-net','--unshare-ipc','--unshare-uts',
                 '--ro-bind','/','/','--bind',str(scratch/'home'),str(Path.home()),'--tmpfs','/tmp','--tmpfs','/run','--proc','/proc','--dev','/dev',
                 '--ro-bind',str(bundle),'/tmp/0c-bundle','--ro-bind',str(Path(__file__).resolve().parent),'/tmp/0c-test',
                 '--bind',str(result),'/tmp/0c-results','--ro-bind',str(scratch/'marker'),'/tmp/0c-desktop-sandbox','--ro-bind',str(scratch/'bus.conf'),'/tmp/0c-bus.conf','--unsetenv','XDG_RUNTIME_DIR','--chdir','/tmp/0c-test',
                 *(['--ro-bind',str(args.shell_update.resolve()),'/tmp/0c-update-b.tar.gz'] if args.shell_update else []),
                 *(['--ro-bind',str(args.shell_fault.resolve()),'/tmp/0c-update-f.tar.gz'] if args.shell_fault else []),
                 *(['--ro-bind',str(args.shell_updater.resolve()),'/tmp/0c-update-test'] if args.shell_updater else []),
                 'dbus-run-session','--config-file=/tmp/0c-bus.conf','--','/usr/bin/python3','/tmp/0c-test/desktop_test.py','--inside'] + (['--english-autocomplete'] if args.english_autocomplete else []) + (['--left-shift'] if args.left_shift else []) + (['--icons'] if args.icons else []) + (['--menu'] if args.menu else []) + (['--kimpanel'] if args.kimpanel else []) + (['--shell-update=/tmp/0c-update-b.tar.gz'] if args.shell_update else []) + (['--shell-manual'] if args.shell_manual else []) + (['--shell-recover'] if args.shell_recover else []) + (['--shell-legacy'] if args.shell_legacy else [])
        with (result/'desktop.log').open('w') as log:
            completed=subprocess.run(command,stdout=log,stderr=subprocess.STDOUT,timeout=420)
        print(result)
        if completed.returncode: raise SystemExit(completed.returncode)


if __name__=='__main__': main()
