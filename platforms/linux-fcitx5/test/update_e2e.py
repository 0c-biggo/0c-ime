#!/usr/bin/env python3
"""Linux update E2E (docs/update-e2e.md): real install, systemd user timer, HTTPS signed feed, Fcitx5 on X.

Run as a dedicated, resettable OS account (never a personal one): it deletes that account's 0c install,
Fcitx5 profile and 0c-update timer before each case. Needs Xvfb, Fcitx5 + GTK3 IM module, Python GI,
libXtst, dunst and a lingering systemd user manager. Packages A and B must be --preview --e2etest builds.

Case core (E01/E04/E14 subset): install A like a user, let A create its own timer, publish B as a
compatible core update, keep typing for a while (no switch allowed), then stay idle and wait for the
timer to discover, prepare, drain and switch B without any user action; type again and compare.
Timing: feed check shortened to 2 minutes (labelled in the report); idle threshold is the real 5 minutes.
"""
import argparse
import configparser
import hashlib
import json
import re
import os
from pathlib import Path
import shutil
import socket
import struct
import subprocess
import tarfile
import time

from gi.repository import Gio

from fcitx_env import addon_env

HOME = Path.home()
BASE = HOME / '.local/share/zuin'
STABLE = BASE / 'bin/0c-update'
RUNTIME = Path(os.environ.get('XDG_RUNTIME_DIR', f'/run/user/{os.getuid()}'))
SOCK = RUNTIME / '0c/0c.sock'
DISPLAY = os.environ.get('ZC_E2E_DISPLAY', ':50')
PORT = int(os.environ.get('ZC_E2E_PORT', '8443'))
FEED = f'https://127.0.0.1:{PORT}/feeds/e2e.json'
UNITS = ('zc-e2e-feed', 'zc-e2e-xvfb', 'zc-e2e-fcitx', 'zc-e2e-dunst', 'zc-e2e-tray')


def run(*cmd, check=True, **kw):
    return subprocess.run([str(c) for c in cmd], check=check, text=True, capture_output=True, **kw)


def health():
    try:
        s = socket.socket(socket.AF_UNIX); s.settimeout(3); s.connect(str(SOCK))
        body = json.dumps({'op': 'health'}).encode()
        s.sendall(struct.pack('<I', len(body)) + body)
        n = struct.unpack('<I', s.recv(4))[0]
        data = b''
        while len(data) < n:
            data += s.recv(n - len(data))
        s.close()
        return json.loads(data).get('info')
    except OSError:
        return None


def state():
    try:
        return json.loads((BASE / 'update/state.json').read_text())
    except (OSError, ValueError):
        return None


def sha(p):
    return hashlib.sha256(Path(p).read_bytes()).hexdigest()


def priority(pid):
    """Nice value, I/O scheduling class and cgroup of a process (#141)."""
    stat = Path(f'/proc/{pid}/stat').read_text()
    nice = int(stat[stat.rindex(')') + 2:].split()[16])  # field 19
    io = run('ionice', '-p', pid, check=False).stdout.strip()
    cgroup = Path(f'/proc/{pid}/cgroup').read_text().strip()
    return {'nice': nice, 'ionice': io, 'cgroup': cgroup}


def guard():
    bus = Gio.bus_get_sync(Gio.BusType.SESSION)
    reply = bus.call_sync('org.biggo.ime.Update', '/update', 'org.biggo.ime.Update1', 'Status',
                          None, None, 0, 4000, None).unpack()[0]
    return json.loads(reply)


def profile_settings():
    profile = configparser.ConfigParser(interpolation=None)
    profile.optionxform = str
    profile.read(HOME / '.config/fcitx5/profile')
    return {section: dict(profile[section]) for section in profile.sections()}


class Case:
    def __init__(self, args):
        self.args = args
        self.work = args.work.resolve()
        self.result = args.result.resolve()
        self.result.mkdir(parents=True, exist_ok=False)
        self.report = {'case': args.case, 'run_id': self.result.name, 'passed': False, 'checks': [], 'timeline': [],
                       'timing': {'feed_check': '2m (shortened, e2etest build)', 'idle_needed': '5m (real)',
                                  'schedule_every': '1m (e2etest build)'}, 'error': ''}
        self.t0 = time.monotonic()
        self.keep_profile = False

    def log(self, kind, **data):
        entry = {'t': round(time.monotonic() - self.t0, 1), 'at': time.strftime('%H:%M:%S'), 'kind': kind, **data}
        self.report['timeline'].append(entry)
        print(json.dumps(entry, ensure_ascii=False), flush=True)

    def check(self, ok, name, detail=''):
        if not ok:
            raise RuntimeError(f'{name} failed {detail}')
        self.report['checks'].append(name)
        self.log('check', name=name)

    def env(self):
        return dict(os.environ, DISPLAY=DISPLAY, GTK_IM_MODULE='fcitx', QT_IM_MODULE='fcitx', XMODIFIERS='@im=fcitx',
                    GDK_BACKEND='x11', LIBGL_ALWAYS_SOFTWARE='1')

    def reset(self):
        for u in UNITS + ('0c-update.timer', '0c-update.service'):
            run('systemctl', '--user', 'stop', u, check=False)
        run('systemctl', '--user', 'reset-failed', check=False)
        run('pkill', '-u', str(os.getuid()), '-x', '0c-server', check=False)
        run('pkill', '-u', str(os.getuid()), '-x', 'fcitx5', check=False)  # 重載後已離開原 transient unit。
        for p in (BASE, HOME / '.local/bin/0c-server', HOME / '.local/bin/0c-update', HOME / '.local/bin/0c-trace',
                  HOME / '.local/lib/fcitx5', HOME / '.local/share/fcitx5', HOME / '.config/fcitx5',
                  HOME / '.config/environment.d/0c.conf', HOME / '.config/dunst', HOME / '.config/systemd/user/0c-update.service',
                  HOME / '.config/systemd/user/0c-update.timer', HOME / '.config/systemd/user/timers.target.wants/0c-update.timer',
                  self.work):
            if p.is_dir() and not p.is_symlink():
                shutil.rmtree(p)
            elif p.exists() or p.is_symlink():
                p.unlink()
        run('systemctl', '--user', 'daemon-reload')
        self.work.mkdir(parents=True)
        (self.work / 'feed/feeds').mkdir(parents=True)
        (self.work / 'feed/releases').mkdir(parents=True)

    def unpack(self, archive):
        with tarfile.open(archive) as t:
            root = t.getnames()[0].split('/')[0]
            t.extractall(self.work, filter='tar')
        return self.work / root

    def unit(self, name, *cmd, env=None):
        extra = [f'--setenv={k}={v}' for k, v in (env or {}).items()]
        run('systemd-run', '--user', '--collect', f'--unit={name}', *extra, *cmd)

    def publish(self, archive, serial, kind='core'):
        dest = self.work / 'feed/releases' / Path(archive).name
        if not dest.exists():
            shutil.copy2(archive, dest)
        run(self.args.tools / '0c-release', 'feed', '-key', self.args.tools / 'release-test.key', '-serial', serial,
            '-valid', '48h', '-kind', kind, '-out', self.work / 'feed/feeds/e2e.json',
            '-asset', f'{dest}=https://127.0.0.1:{PORT}/releases/{dest.name}')
        feed = json.loads((self.work / 'feed/feeds/e2e.json').read_text())
        self.log('published', version=feed['version'], serial=serial, asset_kind=kind)
        return feed

    def start_desktop(self, session_env=False):
        self.unit('zc-e2e-xvfb', '/usr/bin/Xvfb', DISPLAY, '-screen', '0', '1280x800x24', '-nolisten', 'tcp')
        deadline = time.monotonic() + 10
        while not Path('/tmp/.X11-unix/X' + DISPLAY.lstrip(':')).exists() and time.monotonic() < deadline:
            time.sleep(.1)
        config = HOME / '.config/fcitx5'; config.mkdir(parents=True, exist_ok=True)
        if not self.keep_profile:
            (config / 'profile').write_text('[Groups/0]\nName=Default\nDefault Layout=us\nDefaultIM=zuin\n\n[Groups/0/Items/0]\n'
                                            'Name=keyboard-us\nLayout=\n\n[Groups/0/Items/1]\nName=zuin\nLayout=\n\n[GroupOrder]\n0=Default\n')
        env = self.env()
        extra = addon_env(HOME)  # 新版外殼寫絕對路徑，不需要；dev.59 以前的套件才補（系統目錄依這台電腦）
        env.update(extra)
        keys = ('DISPLAY', 'GTK_IM_MODULE', 'XMODIFIERS') if session_env else ('DISPLAY', *extra, 'GTK_IM_MODULE', 'XMODIFIERS')
        # session_env：外殼路徑只來自安裝結果（舊套件寫的 environment.d 由使用者管理員在登入時讀取），如同真實登入。
        self.unit('zc-e2e-fcitx', '/usr/bin/fcitx5', '--disable=wayland', env={k: env[k] for k in keys})
        # Every notification the server receives is appended to a log by a dunst rule (evidence: received by the
        # notification service; whether a person saw it is not observable here).
        dunst = HOME / '.config/dunst'; dunst.mkdir(parents=True, exist_ok=True)
        hook = self.work / 'notify-hook.sh'
        hook.write_text('#!/bin/sh\nprintf "%s\\t%s\\t%s\\n" "$1" "$2" "$3" >> ' + str(self.work / 'notifications.log') + '\n')
        hook.chmod(0o755)
        # dunst 的動作選單以 dmenu 程式選擇；這裡選最新一則通知的「立即安裝」，等同使用者按下按鈕。
        pick = self.work / 'pick-action.sh'
        pick.write_text('#!/bin/sh\ngrep "立即安裝" | tail -1\n')
        pick.chmod(0o755)
        (dunst / 'dunstrc').write_text(f'[global]\ndmenu = {pick}\n[log]\nsummary = "*"\nscript = {hook}\n')
        # 最小的系統匣主機：Fcitx5 notificationitem 向它註冊後，以 dbusmenu 匯出輸入法選單。
        self.tray_item = self.work / 'tray.item'
        self.unit('zc-e2e-tray', '/usr/bin/python3', Path(__file__).resolve().parent / 'tray_menu.py', 'watcher', self.tray_item)
        self.unit('zc-e2e-dunst', '/usr/bin/dunst', '-config', dunst / 'dunstrc', env={'DISPLAY': DISPLAY})
        run('systemctl', '--user', 'set-environment', f'DISPLAY={DISPLAY}')
        time.sleep(3)

    def type(self, keys, expect):
        out = run('/usr/bin/python3', Path(__file__).resolve().parent / 'type_probe.py', keys, env=self.env(), timeout=60, check=False)
        try:
            r = json.loads(out.stdout.strip().splitlines()[-1])
        except (ValueError, IndexError):
            raise RuntimeError(f'probe failed: {out.stderr[-500:]}')
        self.log('typed', keys=keys, text=r['text'], expect=expect)
        return r['text'] == expect

    def notices(self):
        time.sleep(1)
        log = self.work / 'notifications.log'
        return log.read_text().splitlines() if log.exists() else []

    def setup_a(self, *schedule_args):
        a = self.args.a.resolve()
        self.reset(); self.log('reset')
        root = self.unpack(a)
        out = run(root / 'bin/0c-update', 'install', '-bundle', root, '-channel', 'internal-preview')
        self.log('installed_a', output=out.stdout.strip().splitlines()[-3:])
        timers = run('systemctl', '--user', 'list-timers', '--all', '--no-legend', check=False).stdout
        self.check(timers.count('0c-update.timer') == 1, 'a_created_exactly_one_timer', timers)
        self.check(STABLE.is_file(), 'stable_updater_installed')
        if self.args.updater:
            # Development iteration only: replaces A's scheduled updater; the report marks the run.
            shutil.copy2(self.args.updater, STABLE.with_suffix('.dev'))
            os.replace(STABLE.with_suffix('.dev'), STABLE)  # never write a file the timer may be executing
            self.report['updater_override'] = sha(STABLE)
        self.unit('zc-e2e-feed', self.args.tools / '0c-e2e-feed', '-addr', f'127.0.0.1:{PORT}', '-root', self.work / 'feed',
                  '-cert', self.args.tools / 'feed-server.pem', '-key', self.args.tools / 'feed-server.key')
        a_feed = self.publish(a, 1)
        run(STABLE, 'schedule', 'install', '-feed', FEED, '-test-check', '2m', *schedule_args)
        if schedule_args:
            self.report['timing']['extra'] = ' '.join(schedule_args) + ' (shortened, e2etest build)'
        self.start_desktop()
        self.check(self.type('su3cl3 Return', '你好'), 'a_typing')
        h = health(); self.log('health', info=h)
        self.check(h and h.get('version') == a_feed['version'], 'a_running')
        a_pid = h['pid']
        # Let the timer see the A-only feed first (not newer: stays idle, no notification).
        deadline = time.monotonic() + 360  # 新 user manager 的 OnStartupSec=5min 也必須涵蓋。
        while time.monotonic() < deadline and not str((state() or {}).get('last_success', '0001')).startswith('20'):
            time.sleep(5)
        self.check((state() or {}).get('stage') == 'idle', 'a_feed_seen_idle', str(state()))
        return a_feed, a_pid

    def wait_stage(self, stages, minutes, seen=None):
        seen = set() if seen is None else seen
        deadline = time.monotonic() + minutes * 60
        s = {}
        while time.monotonic() < deadline:
            s = state() or {}
            key = (s.get('stage'), (s.get('blocked') or {}).get('reason'))
            if key not in seen:
                seen.add(key); self.log('stage', stage=s.get('stage'), blocked=s.get('blocked'), error=s.get('last_error'))
            if s.get('stage') in stages:
                break
            time.sleep(10)
        return s

    def core(self):
        a_feed, a_pid = self.setup_a()
        b = self.args.b.resolve()
        b_feed = self.publish(b, 2, 'core')
        # Active use: keep typing for 8 minutes. B may be downloaded and prepared, but never switched.
        busy_until = time.monotonic() + 480
        seen = set()
        while time.monotonic() < busy_until:
            if not self.type('su3cl3 Return', '你好'):
                raise RuntimeError('typing failed while an update was pending')
            s = state() or {}
            if s.get('stage') not in seen:
                seen.add(s.get('stage')); self.log('stage', stage=s.get('stage'), blocked=s.get('blocked'))
            h = health()
            if h and h.get('version') != a_feed['version']:
                raise RuntimeError(f'engine switched while user was typing: {h}')
            time.sleep(50)
        self.check(True, 'typing_while_update_pending')
        s = state() or {}
        self.check(s.get('stage') == 'prepared' and (s.get('blocked') or {}).get('reason') == 'waiting_idle',
                   'prepared_waits_while_typing', json.dumps(s.get('blocked')))
        user = BASE / 'user.json'
        before = sha(user) if user.exists() else None
        # Idle: no input at all. The real 5-minute threshold plus one-minute timer.
        idle_start = time.monotonic()
        deadline = idle_start + 15 * 60
        while time.monotonic() < deadline:
            s = state() or {}
            if s.get('stage') not in seen:
                seen.add(s.get('stage')); self.log('stage', stage=s.get('stage'), blocked=s.get('blocked'))
            if s.get('stage') in ('active', 'failed'):
                break
            time.sleep(10)
        self.report['switch_after_idle_seconds'] = round(time.monotonic() - idle_start)
        self.check(s.get('stage') == 'active', 'switched_to_b_without_user_action', json.dumps(s.get('blocked')))
        h = health(); self.log('health', info=h)
        self.check(h and h.get('version') == b_feed['version'] and h.get('pid') != a_pid, 'b_running_new_writer')
        pids = run('pgrep', '-u', str(os.getuid()), '-x', '0c-server').stdout.split()
        self.check(pids == [str(h['pid'])], 'exactly_one_engine_after_switch', str(pids))
        # The timer's 0c-update.service runs at Nice=10 / idle I/O; the engine it hands over to must not (#141).
        prio = priority(h['pid']); self.log('engine_priority', **prio); self.report['engine_priority'] = prio
        self.check(prio['nice'] == 0 and not prio['ionice'].startswith('idle') and '0c-update.service' not in prio['cgroup'],
                   'new_engine_normal_priority', json.dumps(prio))
        pointer = json.loads((BASE / 'engine.json').read_text())
        self.check(pointer['version'] == b_feed['version'], 'engine_pointer_b', json.dumps(pointer))
        if before:
            self.check(sha(user) == before, 'learning_preserved_across_switch')
        self.check(self.type('su3cl3 Return', '你好'), 'b_typing_after_switch')
        self.check(self.type('cl3 Return', '好'), 'b_typing_again')
        self.check(not self.notices(), 'no_notification_for_silent_update', str(self.notices()))
        timers = run('systemctl', '--user', 'list-timers', '--all', '--no-legend', check=False).stdout
        self.check(timers.count('0c-update.timer') == 1, 'still_one_timer')
        self.report['state'] = state()
        self.report['versions'] = {'a': a_feed['version'], 'b': b_feed['version'],
                                   'a_sha256': a_feed['assets'][0]['sha256'], 'b_sha256': b_feed['assets'][0]['sha256']}
        self.report['passed'] = True

    def shell_auto(self, hold_past_idle=True):
        """Timer discovers a shell release, protects held composition past 5m, then installs without logout.

        No `auto`, `finish`, seeded state or updater override: the updater installed by A does all work.
        The same GTK window stays alive through the real 5m idle threshold and Fcitx restart.
        """
        self.check(not self.args.updater, 'uses_a_packaged_updater')
        a_feed, a_pid = self.setup_a()
        self.check(guard()['version'] == a_feed['version'], 'loaded_shell_a')
        profile = profile_settings()
        self.report['profile_before'] = profile
        b_feed = self.publish(self.args.b.resolve(), 2, 'shell')
        held = self.work / 'held-field'; held.mkdir()
        probe = subprocess.Popen(['/usr/bin/python3', str(Path(__file__).resolve().parent / 'type_probe.py'),
                                  'su3', '--shell-auto', str(held)], env=self.env(),
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            self.wait_for(lambda: (held / 'held.json').exists(), 30, 'held GTK composition')
            self.check(json.loads((held / 'held.json').read_text())['text'] == '', 'composition_initially_uncommitted')
            s = self.wait_stage(('prepared', 'active', 'failed'), 7)
            self.check(s.get('stage') == 'prepared' and s['target']['kind'] == 'shell', 'timer_discovered_downloaded_prepared_shell')
            if hold_past_idle:
                self.wait_for(lambda: guard()['idle_seconds'] >= 330 and
                              ((state() or {}).get('blocked') or {}).get('reason') == 'engine_busy', 480,
                              'timer retries after real 5m idle but protects composition')
            h = health(); g = guard()
            self.check(g['busy'] and g['version'] == a_feed['version'] and h['composing'] == '1' and
                       h['pid'] == a_pid, 'held_composition_blocks_update_after_real_idle' if hold_past_idle else
                       'composition_kept_while_shell_update_prepared')
            self.report['held_composition_idle_seconds'] = g['idle_seconds']
            self.log('held_guard', guard=g)
            (held / 'commit').touch()
            self.wait_for(lambda: (held / 'committed.json').exists(), 15, 'user finishes composition')
            r = json.loads((held / 'committed.json').read_text())
            self.check(r['text_after_hold'] == '' and r['text_after_commit'] == '你', 'no_forced_commit_and_user_finishes_normally')
            time.sleep(15)  # 讓正常學習存檔完成；後面沒有送鍵。
            user = BASE / 'user.json'; before = sha(user)
            idle_start = time.monotonic()
            s = self.wait_stage(('active', 'failed'), 15)
            self.report['switch_after_idle_seconds'] = round(time.monotonic() - idle_start + 15)
            self.check(s.get('stage') == 'active', 'timer_applies_shell_without_manual_update_or_logout', json.dumps(s.get('blocked')))
            h = health(); g = guard()
            receipt = json.loads((BASE / 'install-receipt.json').read_text())
            self.check(g['version'] == h['version'] == receipt['version'] == b_feed['version'], 'loaded_shell_engine_receipt_all_b')
            self.check(h['pid'] != a_pid, 'new_engine_writer')
            self.check(sha(user) == before, 'shell_upgrade_preserves_learning')
            self.report['profile_after'] = profile_settings()
            self.check(self.report['profile_after'] == profile, 'shell_upgrade_preserves_profile')
            self.check(not (BASE / 'update/shell-restart.json').exists(), 'shell_handover_journal_cleared')
            pids = run('pgrep', '-u', str(os.getuid()), '-x', 'fcitx5').stdout.splitlines()
            self.check(len(pids) == 1, 'exactly_one_fcitx_after_update')
            pids = run('pgrep', '-u', str(os.getuid()), '-x', '0c-server').stdout.splitlines()
            self.check(len(pids) == 1, 'exactly_one_engine_after_update')
            self.check(run('systemctl', '--user', 'is-active', '0c-update.timer').stdout.strip() == 'active', 'timer_still_active')
            (held / 'verify').touch()
            out, err = probe.communicate(timeout=45)
            self.check(probe.returncode == 0, 'original_gtk_probe_completed', err[-500:])
            r = json.loads(out.strip().splitlines()[-1]); self.report['gtk'] = r
            self.check(r['text_after_update'] == '你你好' and r['window_before'] == r['window_after'],
                       'same_gtk_window_continues_typing_after_shell_restart')
            self.check(not self.notices(), 'silent_update_has_no_unnecessary_notice', str(self.notices()))
            self.report['versions'] = {'a': a_feed['version'], 'b': b_feed['version'],
                                       'a_sha256': a_feed['assets'][0]['sha256'], 'b_sha256': b_feed['assets'][0]['sha256']}
            self.report['passed'] = True
        finally:
            if probe.poll() is None:
                probe.terminate(); probe.communicate(timeout=10)

    def shell_auto_idle(self):
        """Finish the held composition once B is prepared, then use the same real 5m idle threshold."""
        self.shell_auto(hold_past_idle=False)

    def fail(self):
        """E23: F fails the post-switch health check; A is restored and keeps working; F is not retried; B recovers."""
        a_feed, a_pid = self.setup_a('-test-idle', '1m')
        f_feed = self.publish(self.args.f.resolve(), 2, 'core')
        s = self.wait_stage(('failed', 'active'), 12)
        self.check(s.get('stage') == 'failed' and (s.get('blocked') or {}).get('reason') == 'health_failed', 'f_health_failed', json.dumps(s.get('blocked')))
        self.check(f_feed['version'] in (s.get('failed') or []), 'f_suspended')
        h = health(); self.log('health', info=h)
        self.check(h and h.get('version') == a_feed['version'], 'a_restored_running')
        self.check(not (BASE / 'engine.json').exists(), 'pointer_reverted')
        self.check(self.type('su3cl3 Return', '你好'), 'a_typing_after_revert')
        notes = self.notices(); self.log('notices', notices=notes)
        self.check(any('未完成' in n for n in notes), 'failure_notified')
        time.sleep(180)
        s = state() or {}
        switches = [e for e in s.get('history', []) if e['kind'] == 'switched' and e.get('version') == f_feed['version']]
        self.check(s.get('stage') == 'failed' and len(switches) == 1, 'f_not_retried', str(len(switches)))
        b_feed = self.publish(self.args.b.resolve(), 3, 'core')
        s = self.wait_stage(('active',), 12)
        self.check(s.get('stage') == 'active', 'fixed_release_recovers')
        h = health()
        self.check(h and h.get('version') == b_feed['version'], 'b_running_after_recovery')
        self.check(self.type('su3cl3 Return', '你好'), 'b_typing')
        self.report['passed'] = True

    def security(self):
        """E19: bad signature and tampered asset are rejected without changing the install; recovery after the fault."""
        a_feed, a_pid = self.setup_a('-test-idle', '1m')
        self.publish(self.args.b.resolve(), 2, 'core')
        sig = self.work / 'feed/feeds/e2e.json.sig'
        good = sig.read_text()
        sig.write_text('A' * 86 + '==\n')
        time.sleep(200)
        s = state() or {}
        self.log('state', error=s.get('last_error'), stage=s.get('stage'))
        self.check('signature' in (s.get('last_error') or '') and not s.get('target'), 'bad_signature_rejected', s.get('last_error') or '')
        self.check(health().get('version') == a_feed['version'], 'install_unchanged_after_bad_signature')
        run('curl', '-s', '--cacert', self.args.tools / 'feed-server.pem', f'https://127.0.0.1:{PORT}/_control/fault?kind=tamper&match=releases/')
        sig.write_text(good)
        s = self.wait_stage(('prepared', 'active'), 8)
        s = state() or {}
        self.check((s.get('blocked') or {}).get('reason') == 'download_failed' and s.get('stage') == 'found', 'tampered_asset_rejected',
                   json.dumps(s.get('blocked')))
        self.check(health().get('version') == a_feed['version'], 'install_unchanged_after_tamper')
        run('curl', '-s', '--cacert', self.args.tools / 'feed-server.pem', f'https://127.0.0.1:{PORT}/_control/fault?kind=none')
        s = self.wait_stage(('active',), 15)
        self.check(s.get('stage') == 'active', 'recovers_after_fault_cleared')
        self.check(self.type('su3cl3 Return', '你好'), 'typing_after_recovery')
        self.report['passed'] = True

    def withdraw(self):
        """E20: B withdrawn after it was prepared is dropped and never switched."""
        a_feed, a_pid = self.setup_a()
        self.publish(self.args.b.resolve(), 2, 'core')
        deadline = time.monotonic() + 10 * 60
        while time.monotonic() < deadline and (state() or {}).get('stage') != 'prepared':
            if not self.type('su3cl3 Return', '你好'):
                raise RuntimeError('typing failed')
            time.sleep(30)
        self.check((state() or {}).get('stage') == 'prepared', 'b_prepared_while_typing')
        b_version = state()['target']['version']
        dest = self.work / 'feed/releases' / self.args.a.name
        run(self.args.tools / '0c-release', 'feed', '-key', self.args.tools / 'release-test.key', '-serial', 3, '-valid', '48h',
            '-withdrawn', b_version, '-out', self.work / 'feed/feeds/e2e.json', '-asset', f'{dest}=https://127.0.0.1:{PORT}/releases/{dest.name}')
        self.log('withdrawn', version=b_version)
        s = self.wait_stage(('idle',), 6)
        self.check(s.get('stage') == 'idle' and not s.get('target'), 'withdrawn_target_dropped', json.dumps(s.get('blocked')))
        time.sleep(420)  # longer than the real idle threshold
        h = health()
        self.check(h and h.get('version') == a_feed['version'] and not (BASE / 'engine.json').exists(), 'withdrawn_never_switched')
        self.report['passed'] = True

    def ask(self):
        """E05/E06 (CLI part): ask mode notifies and waits; finish applies."""
        a_feed, a_pid = self.setup_a('-test-idle', '1m')
        run(STABLE, 'mode', 'ask')
        b_feed = self.publish(self.args.b.resolve(), 2, 'core')
        s = self.wait_stage(('prepared',), 8)
        time.sleep(15)
        s = state() or {}
        self.check((s.get('blocked') or {}).get('reason') == 'needs_consent', 'ask_mode_waits_for_consent', json.dumps(s.get('blocked')))
        notes = self.notices(); self.log('notices', notices=notes)
        self.check(any('新版本' in n for n in notes), 'consent_notified')
        time.sleep(150)
        self.check(health().get('version') == a_feed['version'], 'no_install_before_consent')
        out = run(STABLE, 'finish', check=False)
        self.log('finish', output=out.stdout.strip().splitlines()[-4:], code=out.returncode)
        h = health()
        self.check(state().get('stage') == 'active' and h and h.get('version') == b_feed['version'], 'finish_applies')
        self.check(self.type('su3cl3 Return', '你好'), 'b_typing')
        self.report['passed'] = True

    def kill(self):
        """E11 + E22: the user holds a composition, so the handover waits; the coordinator is killed right after
        switching the pointer. The next run must use the original rollback target (F fails health -> back to A)
        and the held composition is never committed by the update."""
        a_feed, a_pid = self.setup_a('-test-idle', '1m')
        probe = subprocess.Popen(['/usr/bin/python3', str(Path(__file__).resolve().parent / 'type_probe.py'), 'su3', '420'],
                                 env=self.env(), stdout=subprocess.PIPE, text=True)
        time.sleep(5)
        self.check(health().get('composing') == '1', 'composition_open')
        f_feed = self.publish(self.args.f.resolve(), 2, 'core')
        deadline = time.monotonic() + 360
        killed = False
        while time.monotonic() < deadline:
            s = state() or {}
            if s.get('switched'):
                run('pkill', '-KILL', '-f', 'zuin/bin/0c-update auto', check=False)
                killed = True
                self.log('killed_after_switch', previous=s.get('previous_engine'), stage=s.get('stage'))
                break
            time.sleep(.5)
        self.check(killed, 'coordinator_killed_mid_handover')
        h = health(); self.log('health', info=h)
        self.check(h and h.get('version') == a_feed['version'] and h.get('composing') == '1', 'composition_kept_during_handover')
        out = json.loads(probe.communicate(timeout=300)[0].strip().splitlines()[-1])
        self.log('held', result=out)
        self.check(out.get('text') == '' and out.get('text_after_hold') == '', 'held_composition_not_committed')
        s = self.wait_stage(('failed', 'active'), 8)
        self.check(s.get('stage') == 'failed', 'rerun_detects_f_unhealthy', json.dumps(s.get('blocked')))
        h = health(); self.log('health', info=h)
        self.check(h and h.get('version') == a_feed['version'] and not (BASE / 'engine.json').exists(), 'reverted_to_original_after_kill')
        self.check(self.type('su3cl3 Return', '你好'), 'a_typing_after_recovery')
        self.report['passed'] = True

    def hold_focus(self, seconds):
        """A focused GTK field with the input method active, as when a user opens the tray menu while typing."""
        p = subprocess.Popen(['/usr/bin/python3', str(Path(__file__).resolve().parent / 'type_probe.py'), 'su3cl3 Return', str(seconds)],
                             env=self.env(), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        time.sleep(6)
        return p

    def tray(self, *args):
        out = run('/usr/bin/python3', Path(__file__).resolve().parent / 'tray_menu.py', args[0], self.tray_item, *args[1:], check=False)
        if out.returncode:
            raise RuntimeError(f'tray {args}: {out.stderr[-400:]}')
        return json.loads(out.stdout)

    def menu_items(self):
        items = [i for i in self.tray('list') if i['id'] > 200]
        self.log('menu', items=[(i['label'], i['checked']) for i in items])
        return items

    def wait_for(self, predicate, seconds, what):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            try:
                if predicate():
                    return True
            except (OSError, ValueError, KeyError, AttributeError, RuntimeError):
                pass  # 檔案尚未寫出、服務尚未就緒
            time.sleep(3)
        raise RuntimeError(f'timed out waiting for {what}')

    def menu(self):
        """E06/E09/E25: the tray menu (dbusmenu, as GNOME AppIndicator / KDE read it) now has two rows, the settings entry
        and the update row. Settings change, mode switch and pause run the same updater commands the settings app uses;
        the pending update entry after the notification was dismissed and install from the menu go through the update row."""
        a_feed, a_pid = self.setup_a('-test-idle', '1m')
        p = self.hold_focus(90)
        items = self.menu_items()
        self.check(any(f"靈犀注音 {a_feed['version']} · 已是最新" in i['label'] for i in items), 'menu_shows_current_version')
        # 組字中改設定：命令先存檔，等組字結束才重新啟動引擎套用（設定程式與以前的選單項目走同一條命令）。
        setting = subprocess.Popen([str(STABLE), 'set', 'punctuation', 'off', '-apply', '-notify'], env=self.env(),
                                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.wait_for(lambda: json.loads((BASE / 'settings.json').read_text()).get('punctuation') is False, 30, 'punctuation off')
        self.check(True, 'setting_command_saves_setting')
        run(STABLE, 'mode', 'ask', env=self.env())
        self.wait_for(lambda: (state() or {}).get('mode') == 'ask', 30, 'ask mode')
        self.check(True, 'mode_command_switches_mode')
        p.wait()
        setting.wait(120)
        self.check(self.type('su3cl3 Return', '你好'), 'typing_after_setting_change')
        h = health(); self.log('health', info=h)
        self.check(h and h.get('punctuation') == 'false', 'engine_restarted_with_new_setting', str(h and h.get('punctuation')))
        b_feed = self.publish(self.args.b.resolve(), 2, 'core')
        self.wait_stage(('prepared',), 8)
        self.wait_for(lambda: (state().get('blocked') or {}).get('reason') == 'needs_consent', 60, 'needs_consent')
        run('dunstctl', 'close-all', env=self.env(), check=False)  # 使用者關掉通知（不算同意）
        time.sleep(20)
        self.check(health().get('version') == a_feed['version'], 'dismissed_notice_is_not_consent')
        p = self.hold_focus(120)
        label = f"有新版本 {b_feed['version']} — 立即安裝"
        self.wait_for(lambda: any(label in i['label'] for i in self.menu_items()), 90, 'pending entry in menu')
        self.check(True, 'pending_update_findable_in_menu')
        self.tray('click', label)  # 更新列：「有新版本 … — 立即安裝（點此完成更新）」
        self.wait_for(lambda: (state() or {}).get('stage') == 'active', 180, 'install from menu')
        h = health()
        self.check(h and h.get('version') == b_feed['version'], 'menu_install_applies_b')
        self.wait_for(lambda: any(f"已是最新版本 {b_feed['version']}" in n for n in self.notices()), 30, 'outcome notice')
        self.check(True, 'outcome_notified')
        self.wait_for(lambda: any(f"靈犀注音 {b_feed['version']} · 已是最新" in i['label'] for i in self.menu_items()), 90, 'menu shows B')
        self.check(True, 'menu_shows_new_version')
        run(STABLE, 'pause', '7', env=self.env())
        self.wait_for(lambda: any('自動更新暫停至' in i['label'] for i in self.menu_items()), 60, 'paused label')
        self.check(str((state() or {}).get('paused_until', '')).startswith('20'), 'pause_command_pauses_updates_and_update_row_shows_it')
        p.wait()
        self.check(self.type('su3cl3 Return', '你好'), 'b_typing')
        self.report['passed'] = True

    def button(self):
        """E05/E06 through the notification button: ask mode announces the download with 「立即安裝」; pressing it
        installs and reports the result, without any command line."""
        a_feed, a_pid = self.setup_a('-test-idle', '1m')
        run(STABLE, 'mode', 'ask')
        b_feed = self.publish(self.args.b.resolve(), 2, 'core')
        self.wait_for(lambda: any('需要你確認安裝' in n for n in self.notices()), 480, 'consent notice')
        self.check(True, 'consent_notice_with_button')
        time.sleep(90)
        self.check(health().get('version') == a_feed['version'], 'nothing_installed_before_button')
        run('dunstctl', 'context', env=self.env(), check=False)  # 按下「立即安裝」
        self.wait_for(lambda: (state() or {}).get('stage') == 'active', 180, 'install from button')
        h = health()
        self.check(h and h.get('version') == b_feed['version'], 'button_install_applies_b')
        self.wait_for(lambda: any(f"已是最新版本 {b_feed['version']}" in n for n in self.notices()), 30, 'outcome notice')
        self.check(True, 'outcome_notified')
        self.check(self.type('su3cl3 Return', '你好'), 'b_typing')
        self.report['passed'] = True

    def oneline_stage(self):
        """#19: a brand-new account runs the one-line installer from a terminal while its desktop and Fcitx5 run.
        The profile is left to Fcitx5's own first-run default (only the system keyboard)."""
        self.reset()
        self.keep_profile = True
        cfg = HOME / '.config/fcitx5'
        if cfg.exists():
            shutil.rmtree(cfg)
        self.start_desktop()
        a = self.args.a.resolve()
        self.unit('zc-e2e-feed', self.args.tools / '0c-e2e-feed', '-addr', f'127.0.0.1:{PORT}', '-root', self.work / 'feed',
                  '-cert', self.args.tools / 'feed-server.pem', '-key', self.args.tools / 'feed-server.key')
        feed = self.publish(a, 1)
        boot = self.work / 'bootstrap'
        with tarfile.open(a) as t:
            member = next(m for m in t.getmembers() if m.name.endswith('/bin/0c-update'))
            boot.write_bytes(t.extractfile(member).read())
        base = f'https://127.0.0.1:{PORT}'
        shutil.copy2(boot, self.work / 'feed/releases/0c-update')
        run('/usr/bin/python3', self.args.installer, '--out', self.work / 'feed/feeds', '--base-url', f'{base}/feeds',
            '--feed', FEED, '--channel', feed['channel'], '--cacert', self.args.tools / 'test-ca.pem',
            '--bootstrap', f'linux_amd64={boot}={base}/releases/0c-update')
        self.check(not (BASE / 'install-receipt.json').exists(), 'clean_account')
        cmd = f"curl --cacert {self.args.tools / 'test-ca.pem'} -fsSL {base}/feeds/install.sh | sh"
        out = subprocess.run(['/bin/sh', '-c', cmd], text=True, capture_output=True, timeout=1800,
                             env=dict(os.environ, GTK_IM_MODULE='fcitx', XMODIFIERS='@im=fcitx'))
        self.log('installer', code=out.returncode, stdout=out.stdout.splitlines()[-12:], stderr=out.stderr.splitlines()[-6:])
        self.check(out.returncode == 0, 'one_line_install_succeeds', out.stderr[-400:])
        receipt = json.loads((BASE / 'install-receipt.json').read_text())
        self.check(receipt.get('version') == feed['version'], 'installed_signed_version')
        timers = run('systemctl', '--user', 'list-timers', '--all', '--no-legend', check=False).stdout
        self.check(timers.count('0c-update.timer') == 1, 'schedule_created')
        self.check((state() or {}).get('profile_at_login') is True, 'profile_deferred_while_fcitx_runs')
        self.check('登出再登入' in out.stdout, 'tells_user_next_step')
        again = subprocess.run(['/bin/sh', '-c', cmd], text=True, capture_output=True, timeout=600)
        self.check(again.returncode == 0 and '不重新安裝' in again.stdout, 'rerun_is_safe', again.stdout[-300:])
        self.report['passed'] = True

    def oneline_verify(self):
        """#19 part 2, after a real logout/login: the login unit added 靈犀注音 to the Fcitx5 profile before the desktop,
        the addon path comes from the installed environment.d, and typing works."""
        profile = (HOME / '.config/fcitx5/profile').read_text()
        self.log('profile', profile=profile)
        self.check('Name=zuin' in profile, 'login_unit_added_input_method')
        headers = re.findall(r'^\[Groups/0/Items/(\d+)\]$', profile, re.M)
        self.check(len(headers) == len(set(headers)) and 'Name=keyboard-' in profile, 'user_keyboard_kept_no_duplicate_items', str(headers))
        self.check(not (state() or {}).get('profile_at_login'), 'profile_flag_cleared')
        self.work.mkdir(parents=True, exist_ok=True)
        self.keep_profile = True
        self.start_desktop(session_env=True)
        run('fcitx5-remote', '-s', 'zuin', env=self.env(), check=False)
        self.check(self.type('su3cl3 Return', '你好'), 'typing_after_one_line_install')
        p = self.hold_focus(40)
        items = self.menu_items()
        self.check(any('已是最新' in i['label'] for i in items), 'menu_available')
        p.wait()
        self.report['passed'] = True

    def shell_stage(self):
        """E15 part 1: a shell update while the desktop runs is downloaded, staged for the next login and announced."""
        a_feed, a_pid = self.setup_a()
        b_feed = self.publish(self.args.b.resolve(), 2, 'shell')
        s = self.wait_stage(('pending', 'active'), 8)
        self.check(s.get('stage') == 'pending' and (s.get('blocked') or {}).get('reason') == 'needs_relogin', 'shell_staged_for_login',
                   json.dumps(s.get('blocked')))
        self.check(health().get('version') == a_feed['version'], 'desktop_untouched_while_staged')
        notes = self.notices(); self.log('notices', notices=notes)
        self.check(any('登入' in n for n in notes), 'relogin_notified')
        self.check(self.type('su3cl3 Return', '你好'), 'a_typing_while_staged')
        (self.result / 'b_version').write_text(b_feed['version'])
        self.report['passed'] = True

    def shell_verify(self):
        """E15 part 2 (after a real logout/login of this account): the login unit installed B before the desktop started."""
        b_version = self.args.expect
        out = run('journalctl', '--user', '-u', '0c-update-login.service', '-b', '--no-pager', '-o', 'short-iso', check=False).stdout
        self.report['login_unit'] = out.splitlines()[-20:]
        receipt = json.loads((BASE / 'install-receipt.json').read_text())
        self.check(receipt.get('version') == b_version, 'login_unit_installed_b', json.dumps(receipt.get('version')))
        self.work.mkdir(parents=True, exist_ok=True)
        self.start_desktop()
        self.check(self.type('su3cl3 Return', '你好'), 'b_typing_after_relogin')
        h = health(); self.log('health', info=h)
        self.check(h and h.get('version') == b_version, 'b_running_after_relogin')
        s = self.wait_stage(('active',), 8)  # the timer first fires 5 minutes after login
        self.check(s.get('stage') == 'active', 'shell_update_confirmed_active')
        self.report['passed'] = True

    def finish(self):
        self.report.setdefault('state', state())
        self.report['notifications'] = self.notices() if self.work.exists() else []
        self.report['journal'] = run('journalctl', '--user', '-u', '0c-update.service', '--no-pager', '-o', 'short-iso',
                                     '-n', '400', check=False).stdout.splitlines()
        self.report['feed_log'] = run('journalctl', '--user', '-u', 'zc-e2e-feed', '--no-pager', '-n', '200',
                                      check=False).stdout.splitlines()
        (self.result / 'report.json').write_text(json.dumps(self.report, ensure_ascii=False, indent=2) + '\n')
        for u in UNITS:
            run('systemctl', '--user', 'stop', u, check=False)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--case', choices=['core', 'shell_auto', 'shell_auto_idle', 'fail', 'security', 'withdraw', 'ask', 'kill', 'menu', 'button', 'oneline_stage', 'oneline_verify', 'shell_stage', 'shell_verify'], default='core')
    p.add_argument('--a', type=Path)
    p.add_argument('--b', type=Path)
    p.add_argument('--installer', type=Path, help='scripts/make_installer.py (oneline cases)')
    p.add_argument('--expect', help='shell_verify: version staged by shell_stage')
    p.add_argument('--f', type=Path, help='fault package F (--e2e-fault version-mismatch), for --case fail')
    p.add_argument('--tools', type=Path, required=True, help='0c-e2e-feed, 0c-release, test key and certificate')
    p.add_argument('--updater', type=Path, help='development only: e2etest 0c-update replacing the one A installed')
    p.add_argument('--work', type=Path, default=HOME / 'e2e/work')
    p.add_argument('--result', type=Path, default=HOME / 'e2e/results' / time.strftime('%Y%m%d-%H%M%S'))
    args = p.parse_args()
    args.tools = args.tools.resolve()
    case = Case(args)
    try:
        getattr(case, args.case)()
    except Exception as e:
        case.report['error'] = str(e)
        case.log('error', error=str(e))
    finally:
        case.finish()
    print(case.result)
    raise SystemExit(0 if case.report['passed'] else 1)


if __name__ == '__main__':
    main()
