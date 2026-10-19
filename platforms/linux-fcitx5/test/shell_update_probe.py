"""Real shell update checks inside desktop_test.py's private desktop only."""
from datetime import datetime, timedelta, timezone
import hashlib
import json
import os
from pathlib import Path
import socket
import struct
import sys
import subprocess
import tarfile
import time

from gi.repository import Gio, GLib


def run(report, entry, pump, key, type_text, expect):
    if not Path('/tmp/0c-desktop-sandbox').is_file():
        raise RuntimeError('Shell test requires an isolated desktop')
    home = Path.home()
    base = home / '.local/share/zuin'
    updater = Path('/tmp/0c-update-test') if Path('/tmp/0c-update-test').exists() else home / '.local/bin/0c-update'
    state_path = base / 'update/state.json'
    env = dict(os.environ, ZC_NO_SCHEDULE='1')
    bus = Gio.bus_get_sync(Gio.BusType.SESSION, None)

    def guard(method='Status', args=None):
        reply = bus.call_sync('org.biggo.ime.Update', '/update', 'org.biggo.ime.Update1', method,
                              args, None, 0, 4000, None).unpack()[0]
        return json.loads(reply) if method == 'Status' else reply

    def health():
        with socket.socket(socket.AF_UNIX) as s:
            s.connect(str(Path(env['XDG_RUNTIME_DIR']) / '0c/0c.sock'))
            raw = b'{"op":"health"}'
            s.sendall(struct.pack('<I', len(raw)) + raw)
            size = struct.unpack('<I', s.recv(4))[0]
            data = b''
            while len(data) < size:
                data += s.recv(size-len(data))
        return json.loads(data)['info']

    def step(action):
        p = subprocess.Popen([str(updater), action], env=env, stdout=subprocess.PIPE,
                             stderr=subprocess.STDOUT, text=True)
        deadline = time.monotonic()+60
        while p.poll() is None and time.monotonic()<deadline:
            pump(.03)
        if p.poll() is None:
            p.kill(); raise RuntimeError('Shell update timed out')
        out = p.communicate()[0]
        if p.returncode:
            raise RuntimeError('Shell update failed: '+out[-800:])
        return json.loads(state_path.read_text()), out

    def stage(archive):
        with tarfile.open(archive) as t:
            member = next(m for m in t if m.name.endswith('/release.json'))
            release = json.load(t.extractfile(member))
        state = {'schema': 1, 'install_id': 'private-shell-test', 'mode': 'auto', 'stage': 'prepared',
                 'last_check': datetime.now(timezone.utc).isoformat(), 'next_check': (datetime.now(timezone.utc)+timedelta(hours=1)).isoformat(),
                 'download': str(archive), 'prepared_at': '2026-10-03T00:00:00Z',
                 'test': {'idle_needed': '2s'},
                 'target': {'version': release['version'], 'kind': 'shell',
                            'url': 'https://test.invalid/bundle.tar.gz',
                            'sha256': hashlib.sha256(archive.read_bytes()).hexdigest(), 'size': archive.stat().st_size}}
        state_path.parent.mkdir(exist_ok=True)
        state_path.write_text(json.dumps(state))
        return release['version']

    def check(ok, name):
        if not ok: raise RuntimeError(name)
        report['checks'].append(name)

    a = health()['version']
    if '--shell-legacy' in sys.argv:
        try:
            guard()
        except GLib.Error:
            check(True,'legacy_shell_has_no_guard')
        else:
            raise RuntimeError('Legacy fixture unexpectedly has an update guard')
        b=stage(Path('/tmp/0c-update-b.tar.gz'))
        state=json.loads(state_path.read_text());state['stage']='pending';state['blocked']={'reason':'needs_relogin','since':'2026-10-03T00:00:00Z'}
        state_path.write_text(json.dumps(state));pump(2.2)
        state,_=step('auto')
        check(state['blocked']['reason']=='needs_consent','legacy_shell_requires_manual_migration')
        entry.set_text('');type_text('su3')
        state,_=step('finish')
        check(state['blocked']['reason']=='engine_busy','legacy_shell_keeps_composition')
        expect('','legacy_shell_does_not_force_commit');key('Return');expect('你','legacy_held_composition_finishes_normally')
        pump(12.5);before=(base/'user.json').read_bytes()
        state,out=step('finish');report['shell_apply_result']={'state':state,'output':out}
        check(state['stage']=='active' and guard()['version']==b and health()['version']==b,'legacy_pending_shell_migrates_without_logout')
        check((base/'user.json').read_bytes()==before,'legacy_migration_keeps_learning')
        entry.set_text('');type_text('su3cl3');key('Return');expect('你好','typing_after_legacy_shell_migration')
        report['shell_update']={'from':a,'to':b,'logout':False,'legacy':True}
        return
    check(guard()['version'] == a, 'loaded_shell_version_a')
    b = stage(Path('/tmp/0c-update-b.tar.gz'))
    # 真正的另一個輸入法；0c health 沒有組字仍不得讓它退出。
    subprocess.run(['fcitx5-remote','-s','pinyin'],check=True);pump(.3)
    entry.set_text('');type_text('ni')
    check(health()['composing']=='0' and guard()['busy'], 'other_input_method_composition_visible')
    state,_=step('finish')
    check(state['blocked']['reason']=='engine_busy' and guard()['version']==a, 'other_input_method_blocks_shell_restart')
    expect('', 'other_input_method_not_force_committed')
    key('space');expect('你', 'other_input_method_composition_kept')
    subprocess.run(['fcitx5-remote','-s','zuin'],check=True);pump(.3)
    entry.set_text(''); type_text('su3')
    state, _ = step('finish')
    check(state['blocked']['reason'] == 'engine_busy' and health()['version'] == a,
          'manual_shell_update_preserves_composition')
    expect('', 'shell_update_does_not_force_commit')
    check(guard('ExitForUpdate', GLib.Variant('(t)', (0,))) == 'composing', 'guard_refuses_exit_with_composition')
    key('Return'); expect('你', 'held_composition_commits_normally')
    # 組字完成後，仍在使用時不自動重啟。
    state, _ = step('auto')
    check(state['blocked']['reason'] == 'waiting_idle', 'automatic_shell_waits_for_idle')
    pump(12.5) # 等待引擎停打 10 秒後的正常存檔，避免把提交後的新學習當成更新改寫。
    before = (base / 'user.json').read_bytes()
    if '--shell-manual' in sys.argv or '--shell-recover' in sys.argv:
        key('Control_L') # 閒置門檻尚未成立，手動完成仍可進行。
        state['mode']='manual';state_path.write_text(json.dumps(state))
        if '--shell-recover' in sys.argv:
            p=subprocess.Popen([str(updater),'finish'],env=env,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
            deadline=time.monotonic()+60
            killed=False
            journal=base/'update/shell-restart.json'
            while p.poll() is None and time.monotonic()<deadline:
                try:
                    if json.loads(journal.read_text())['phase']=='verifying':
                        p.kill();p.wait();killed=True;break
                except (FileNotFoundError,json.JSONDecodeError):pass
                pump(.005)
            check(killed,'coordinator_killed_before_shell_verification')
            state,out=step('auto') # 手動模式下仍需恢復已開始的交易。
            check(guard()['version']==b and health()['version']==b,'interrupted_shell_transaction_recovered_in_manual_mode')
        if '--shell-recover' not in sys.argv:
            state,out=step('finish')
        success='manual_shell_update_without_logout'
    else:
        state,out=step('auto')
        success='idle_shell_update_without_logout'
    report['shell_apply_result']={'state':state,'output':out}
    check(state['stage'] == 'active' and not state.get('blocked'), success)
    check(guard()['version'] == b and health()['version'] == b, 'both_shell_and_engine_version_b_verified')
    check((base / 'user.json').read_bytes() == before, 'shell_update_keeps_personal_store')
    check(not (base/'update/shell-restart.json').exists(), 'shell_update_transaction_finished')
    entry.set_text(''); type_text('su3cl3');key('Return');expect('你好', 'same_gtk_connection_types_after_shell_reload')
    # 故障版通過安裝但 health 版本錯誤，須回復並恢復同一桌面。
    fault = Path('/tmp/0c-update-f.tar.gz')
    if fault.exists():
        f = stage(fault)
        pump(12.5) # 先等上面打字的學習正常存檔（停打 10 秒），才不會把它當成故障回復改寫了個人詞庫。
        before = (base/'user.json').read_bytes()
        state, out = step('finish')
        report['shell_failure_result']={'state':state,'output':out}
        check(state['stage'] == 'failed' and f in state.get('failed', []), 'bad_shell_version_suspended')
        check(guard()['version'] == b and health()['version'] == b, 'bad_shell_restores_previous_shell_and_engine')
        check((base/'user.json').read_bytes() == before, 'shell_failure_keeps_personal_store')
        entry.set_text('');type_text('su3cl3');key('Return');expect('你好', 'typing_after_shell_failure_rollback')
    report['shell_update'] = {'from': a, 'to': b, 'logout': False, 'test_idle_seconds': 2}
