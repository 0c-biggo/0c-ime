"""Exercise upgrade failures with an isolated installation, never the user's desktop."""
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import upgrade
import install_package as installer


class UpgradeTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.home = self.root / "home"
        self.platform = self.root / "platform"
        self.base = self.home / ".local/share/zuin"
        self.base.mkdir(parents=True)
        self.files = {
            "build/0c-server": self.home / ".local/bin/0c-server",
            "build/0c-trace": self.home / ".local/bin/0c-trace",
            "build/lib0c.so": self.home / ".local/lib/fcitx5/lib0c.so",
        }
        self.old = {}
        for src, dst in self.files.items():
            dst.parent.mkdir(parents=True, exist_ok=True)
            dst.write_bytes(("old " + src).encode())
            self.old[dst] = dst.read_bytes()
            artifact = self.platform / src
            artifact.parent.mkdir(parents=True, exist_ok=True)
            artifact.write_bytes(("new " + src).encode())
            artifact.chmod(0o755)
        self.user = self.base / "user.json"
        self.user.write_bytes(b'private learning')
        self.data = self.base / "data/manifest.json"
        self.data.parent.mkdir()
        self.data.write_bytes(b'existing dictionary')
        # Every installed path must point into the temporary home, including the old zuin-* names.
        legacy_server = self.home / ".local/bin/zuin-server"
        legacy_addon = self.home / ".local/lib/fcitx5/libzuin.so"
        legacy_environment = self.home / ".config/environment.d/90-zuin.conf"
        self.legacy = (legacy_server, self.home / ".local/bin/zuin-trace", legacy_addon,
                       self.home / ".local/share/fcitx5/addon/zuin.conf", legacy_environment,
                       *(self.home / name for name in upgrade.LEGACY_STATUS_ICONS.values()))
        for name, value in dict(INSTALL_HOME=self.home, BASE=self.base,
                                PLATFORM=self.platform, FILES=self.files, LEGACY_SERVER=legacy_server,
                                LEGACY_ADDON=legacy_addon, LEGACY_ENVIRONMENT=legacy_environment, LEGACY=self.legacy,
                                ENVIRONMENT=self.home / ".config/environment.d/90-0c.conf").items():
            patcher = patch.object(upgrade, name, value)
            patcher.start()
            self.addCleanup(patcher.stop)

    def fake_proc(self, *entries):
        """假的 /proc：每項 (pid, comm, state, ppid)。"""
        proc = self.root / "proc"
        proc.mkdir()
        for pid, comm, state, parent in entries:
            directory = proc / str(pid)
            directory.mkdir()
            self.write_stat(directory, pid, comm, state, parent, 0)
        quiet = patch.object(upgrade, "settle", lambda: None)
        quiet.start()
        self.addCleanup(quiet.stop)
        return patch.object(upgrade, "PROC", proc)

    @staticmethod
    def write_stat(directory, pid, comm, state, parent, start):
        zeros = " ".join(["0"] * 17)
        (directory / "comm").write_text(comm + "\n")
        (directory / "stat").write_text(f"{pid} ({comm}) {state} {parent} {zeros} {start} 0\n")

    def test_fork_child_of_fcitx_is_not_a_second_desktop(self):
        # 外殼 fork 出的 0c-update 在 exec 之前仍叫 fcitx5：不能被數成另一個桌面。
        with self.fake_proc((49, "fcitx5", "S", 4), (215, "fcitx5", "S", 49), (216, "fcitx5", "S", 215), (300, "bash", "S", 4)):
            self.assertEqual([p.name for p in upgrade.processes("fcitx5")], ["49"])

    def test_two_unrelated_fcitx_processes_are_still_two(self):
        with self.fake_proc((49, "fcitx5", "S", 4), (50, "fcitx5", "S", 5)):
            self.assertEqual([p.name for p in upgrade.processes("fcitx5")], ["49", "50"])

    def test_zombie_and_other_names_are_ignored(self):
        with self.fake_proc((49, "fcitx5", "S", 4), (60, "fcitx5", "Z", 4), (61, "fcitx5-x", "S", 4)):
            self.assertEqual([p.name for p in upgrade.processes("fcitx5")], ["49"])

    def test_double_forked_grandchild_is_not_a_second_desktop(self):
        # 兩次 fork 啟動 0c-server：孫行程 exec 之前仍叫 fcitx5，父行程已是 init；重掃時它已 exec 成 0c-server。
        with self.fake_proc((49, "fcitx5", "S", 4), (900, "fcitx5", "R", 1)) as proc:
            def execed():
                self.write_stat(proc / "900", 900, "0c-server", "S", 1, 0)
            with patch.object(upgrade, "settle", execed):
                self.assertEqual([p.name for p in upgrade.processes("fcitx5")], ["49"])

    def test_reused_pid_is_not_persistent(self):
        with self.fake_proc((49, "fcitx5", "S", 4), (900, "fcitx5", "R", 1)) as proc:
            rounds = []
            def reused():
                rounds.append(1)
                self.write_stat(proc / "900", 900, "fcitx5", "R", 1, 1000 + len(rounds))
            with patch.object(upgrade, "settle", reused):
                self.assertEqual([p.name for p in upgrade.processes("fcitx5")], ["49"])

    def test_comm_with_spaces_and_parentheses_is_parsed(self):
        with self.fake_proc((49, "a b) c", "S", 4), (50, "a b) c", "S", 49)):
            self.assertEqual([p.name for p in upgrade.processes("a b) c")], ["49"])

    def assert_preserved(self):
        self.assertEqual(self.user.read_bytes(), b'private learning')
        self.assertEqual(self.data.read_bytes(), b'existing dictionary')

    def add_icons(self):
        for name in upgrade.STATUS_ICONS:
            path = self.platform / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("svg")

    def test_icons_apply_preference_even_when_programs_unchanged(self):
        self.add_icons()
        preference = self.home / ".config/fcitx5/conf/classicui.conf"
        preference.parent.mkdir(parents=True)
        original = "Theme=Yaru-dark\nPreferTextIcon=True\n"
        preference.write_text(original)
        backup = upgrade.install({}, [])
        self.assertEqual(preference.read_text(), "Theme=Yaru-dark\nPreferTextIcon=False\n")
        self.assertEqual((backup / preference.relative_to(self.home)).read_text(), original)
        self.assertIsNone(upgrade.install({}, []))
        self.assert_preserved()

    def test_kimpanel_provenance_write_failure_restores_patch_and_fragment(self):
        snippet = Path(__file__).resolve().parents[1] / 'platforms/linux-fcitx5/kimpanel-spacing.js'
        (self.platform / 'kimpanel-spacing.js').write_bytes(snippet.read_bytes())
        indicator = self.home / installer.KIMPANEL_INDICATOR
        indicator.parent.mkdir(parents=True)
        original = 'class Indicator {\n    _setIcon(property) {\n        this.mainIcon.gicon = property.icon;\n    }\n}\n'
        indicator.write_text(original)
        real_copy = upgrade.atomic_copy
        def fail_record(src, dst):
            if dst == self.home / installer.KIMPANEL_RECORD:
                raise OSError('provenance write failure')
            return real_copy(src, dst)
        with patch.object(upgrade, 'atomic_copy', side_effect=fail_record):
            with self.assertRaisesRegex(OSError, 'provenance write failure'):
                upgrade.install(self.files, [])
        self.assertEqual(indicator.read_text(), original)
        self.assertFalse((self.home / installer.OPTIONAL[installer.KIMPANEL_SPACING]).exists())
        self.assertFalse((self.home / installer.KIMPANEL_RECORD).exists())
        for dst, contents in self.old.items():
            self.assertEqual(dst.read_bytes(), contents)
        self.assert_preserved()

    def test_kimpanel_spacing_is_backed_up_and_not_repeated(self):
        snippet = Path(__file__).resolve().parents[1] / 'platforms/linux-fcitx5/kimpanel-spacing.js'
        (self.platform / 'kimpanel-spacing.js').write_bytes(snippet.read_bytes())
        indicator = self.home / installer.KIMPANEL_INDICATOR
        indicator.parent.mkdir(parents=True)
        original = 'class Indicator {\n    _setIcon(property) {\n        this.mainIcon.gicon = property.icon;\n    }\n}\n'
        indicator.write_text(original)
        backup = upgrade.install({}, [])
        self.assertIn('padding: 0; margin: 0;', indicator.read_text())
        self.assertEqual((backup / installer.KIMPANEL_INDICATOR).read_text(), original)
        self.assertIsNone(upgrade.install({}, []))
        self.assert_preserved()

    def test_icon_preference_failure_restores_programs(self):
        self.add_icons()
        preference = self.home / ".config/fcitx5/conf/classicui.conf"
        preference.parent.mkdir(parents=True)
        preference.write_text("PreferTextIcon=True\n")
        real_copy = upgrade.atomic_copy
        def fail_preference(src, dst):
            if dst == preference:
                raise OSError("config disk failure")
            real_copy(src, dst)
        with patch.object(upgrade, "atomic_copy", side_effect=fail_preference):
            with self.assertRaisesRegex(OSError, "config disk failure"):
                upgrade.install(self.files, [])
        for dst, contents in self.old.items():
            self.assertEqual(dst.read_bytes(), contents)
        self.assertEqual(preference.read_text(), "PreferTextIcon=True\n")
        self.assert_preserved()

    def test_icon_preference_only_change_reloads_shell(self):
        self.add_icons()
        for src, dst in self.files.items():
            (self.platform / src).write_bytes(dst.read_bytes())
        proc = self.root / "proc"
        proc.mkdir()
        (proc / "environ").write_bytes(b"WAYLAND_DISPLAY=wayland-test\0FCITX_ADDON_DIRS=/old/addons\0")
        with patch.object(upgrade, "run", return_value=subprocess.CompletedProcess([], 0, stdout="abc\n")) as run, \
                patch.object(upgrade, "check_data"), patch.object(upgrade, "processes", return_value=[proc]), \
                patch.object(upgrade, "addon_loaded", return_value=True), \
                patch.object(upgrade, "stop_old_servers"), patch.object(upgrade.time, "sleep"):
            upgrade.apply()
        self.assertEqual(len([call for call in run.call_args_list if call.args[0] == "fcitx5"]), 1)
        self.assert_preserved()

    def test_backup_atomic_replacement_preserves_loaded_inode_and_data(self):
        old_path = self.files['build/0c-server']
        with old_path.open('rb') as loaded:
            backup = upgrade.install(self.files, [])
            self.assertEqual(loaded.read(), self.old[old_path])
        self.assertEqual(backup.stat().st_mode & 0o777, 0o700)
        self.assertEqual((backup / 'personal/user.json').read_bytes(), b'private learning')
        for src, dst in self.files.items():
            self.assertEqual(dst.read_bytes(), (self.platform / src).read_bytes())
            self.assertEqual((backup / dst.relative_to(self.home)).read_bytes(), self.old[dst])
            self.assertEqual(dst.stat().st_mode & 0o777, 0o755)
        self.assert_preserved()

    def test_zuin_installation_moves_to_backup_and_old_engine_is_stopped(self):
        for path in self.legacy:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b'old ' + path.name.encode())
        removals = [path for path in self.legacy if path.is_file()]
        backup = upgrade.install(self.files, removals)
        for path in self.legacy:
            self.assertFalse(path.exists())
            self.assertEqual((backup / path.relative_to(self.home)).read_bytes(), b'old ' + path.name.encode())
        self.assertEqual((self.home / '.config/environment.d/90-0c.conf').read_bytes(), b'old 90-zuin.conf')
        self.assert_preserved()
        # The old engine is stopped even if it happens to be byte-identical to the new one.
        # Linux pidfd APIs are simulated even on macOS test hosts.
        with patch.object(upgrade, 'processes', side_effect=lambda name: [Path('/proc/123')] if name == 'zuin-server' else []), \
                patch.object(upgrade.os, 'pidfd_open', return_value=42, create=True), \
                patch.object(upgrade.os, 'readlink', return_value=str(self.legacy[0]) + ' (deleted)'), \
                patch.object(upgrade.os, 'close'), \
                patch.object(upgrade, 'running_same', return_value=True), \
                patch.object(upgrade.select, 'poll') as poll, \
                patch.object(upgrade.signal, 'pidfd_send_signal', create=True) as signal:
            poll.return_value.poll.return_value = [(42, 1)]
            upgrade.stop_old_servers()
            signal.assert_called_once_with(42, upgrade.signal.SIGTERM)

    def test_failed_install_restores_all_previously_replaced_files(self):
        real_copy = upgrade.atomic_copy
        calls = 0

        def fail_second(src, dst):
            nonlocal calls
            calls += 1
            if calls == 2:
                raise OSError('simulated disk failure')
            real_copy(src, dst)

        with patch.object(upgrade, 'atomic_copy', side_effect=fail_second):
            with self.assertRaisesRegex(OSError, 'disk failure'):
                upgrade.install(self.files, [])
        for dst, contents in self.old.items():
            self.assertEqual(dst.read_bytes(), contents)
        self.assert_preserved()

    def test_build_or_dictionary_failure_never_installs_or_restarts(self):
        for failed_step in ('run', 'check_data'):
            with self.subTest(step=failed_step), \
                    patch.object(upgrade, 'run'), patch.object(upgrade, 'check_data'), \
                    patch.object(upgrade, failed_step, side_effect=RuntimeError('failed')), \
                    patch.object(upgrade, 'install') as install, \
                    patch.object(upgrade, 'stop_old_servers') as stop:
                with self.assertRaisesRegex(RuntimeError, 'failed'):
                    upgrade.apply()
                install.assert_not_called()
                stop.assert_not_called()
        self.assert_preserved()

    def test_unchanged_install_does_not_restart_fcitx_or_create_backup(self):
        for src, dst in self.files.items():
            (self.platform / src).write_bytes(dst.read_bytes())
        proc = self.root / 'proc'
        proc.mkdir()
        (proc / 'environ').write_bytes(b'WAYLAND_DISPLAY=wayland-test\0ZUIN_DOCUMENT_CONTEXT=1\0')
        with patch.object(upgrade, 'run', return_value=subprocess.CompletedProcess([], 0, stdout='abc\n')) as run, \
                patch.object(upgrade, 'check_data'), \
                patch.object(upgrade, 'processes', return_value=[proc]), \
                patch.object(upgrade, 'addon_loaded', return_value=True), \
                patch.object(upgrade, 'stop_old_servers'), patch.object(upgrade.time, 'sleep') as sleep:
            upgrade.apply()
            self.assertFalse(any(call.args[0] == 'fcitx5' for call in run.call_args_list))
            sleep.assert_not_called()
        self.assertFalse((self.base / 'backups').exists())

    def test_restart_preserves_desktop_environment(self):
        proc = self.root / 'proc'
        proc.mkdir()
        (proc / 'environ').write_bytes(b'WAYLAND_DISPLAY=wayland-test\0ZUIN_DOCUMENT_CONTEXT=1\0FCITX_ADDON_DIRS=/old/addons\0')
        with patch.object(upgrade, 'run', return_value=subprocess.CompletedProcess([], 0, stdout='abc\n')) as run, \
                patch.object(upgrade, 'check_data'), \
                patch.object(upgrade, 'processes', return_value=[proc]), \
                patch.object(upgrade, 'addon_loaded', return_value=True), \
                patch.object(upgrade, 'stop_old_servers'), patch.object(upgrade.time, 'sleep'):
            upgrade.apply()
            restart = [call for call in run.call_args_list if call.args[0] == 'fcitx5']
            self.assertEqual(len(restart), 1)
            env = restart[0].kwargs['env']
            self.assertEqual(env['WAYLAND_DISPLAY'], 'wayland-test')
            self.assertEqual(env['ZUIN_DOCUMENT_CONTEXT'], '1')
            self.assertIn('/old/addons', env['FCITX_ADDON_DIRS'])
        self.assert_preserved()

    def kwin_desktop(self, user_value=True, tools=('kreadconfig6', 'kwriteconfig6', 'busctl'), ignores=False):
        """假的 KDE Plasma Wayland：KWin（PID 100）帶著 WAYLAND_SOCKET 啟動的 Fcitx5（PID 123）。清空 InputMethod
        時 KWin 停用並結束 Fcitx5，寫回時重新啟動（ignores 時沒有反應）。回傳 (fake_run, running, kwinrc 狀態)。"""
        proc = self.root / 'proc/123'
        proc.mkdir(parents=True)
        config = self.root / 'config'
        config.mkdir()
        desktop = '/usr/share/applications/org.fcitx.Fcitx5.desktop'
        if user_value:
            (config / 'kwinrc').write_text(f'[Wayland]\nInputMethod[$e]={desktop}\n')
        (proc / 'environ').write_bytes(b'WAYLAND_SOCKET=27\0XDG_RUNTIME_DIR=/run/user/1000\0XDG_CONFIG_HOME='
                                       + bytes(config) + b'\0')
        (proc / 'stat').write_text('123 (fcitx5) S 100 123 123 0')
        (self.root / 'proc/100').mkdir()
        (self.root / 'proc/100/comm').write_text('kwin_wayland\n')
        running = [proc]
        kwinrc = {'value': desktop, 'available': True}

        def current():
            # 使用者的 kwinrc 有這個鍵時讀它，否則是系統預設（desktop）。
            path = config / 'kwinrc'
            if upgrade.user_sets_input_method(path):
                return kwinrc['value']
            return desktop

        def notified():
            if ignores:
                return
            if current():
                kwinrc['available'] = True
                if proc not in running:
                    running.append(proc)
            else:
                kwinrc['available'] = False
                running.clear()  # KWin 收到清空通知後結束舊的 Fcitx5

        def fake_run(*args, **kwargs):
            name = Path(args[0]).name
            if name.startswith(('kwriteconfig', 'kreadconfig', 'busctl')):
                self.assertNotIn('WAYLAND_SOCKET', kwargs['env'])
            if name.startswith('kreadconfig'):
                return subprocess.CompletedProcess(args, 0, stdout=current() + '\n', stderr='')
            if name == 'busctl' and 'get-property' in args:
                return subprocess.CompletedProcess(args, 0, stdout=f"b {str(kwinrc['available']).lower()}\n", stderr='')
            if name.startswith('kwriteconfig'):
                kwinrc['value'] = args[-1]
                (config / 'kwinrc').write_text(f'[General]\nX=1\n\n[Wayland]\nInputMethod[$e]={args[-1]}\n')
                if '--notify' in args:
                    notified()
            if name == 'busctl' and 'emit' in args:
                notified()
            return subprocess.CompletedProcess(args, 0, stdout='abc\n', stderr='')
        which = lambda name: '/usr/bin/' + name if name in tools else None
        return fake_run, which, running, kwinrc

    def apply_kwin(self, fake_run, which, running):
        with patch.object(upgrade, 'run', side_effect=fake_run) as run, \
                patch.object(upgrade.shutil, 'which', side_effect=which), \
                patch.object(upgrade, 'check_data'), \
                patch.object(upgrade, 'processes', side_effect=lambda name: list(running)), \
                patch.object(upgrade, 'addon_loaded', return_value=True), \
                patch.object(upgrade, 'wait', side_effect=lambda seconds, ready: ready()), \
                patch.object(upgrade, 'stop_old_servers'), patch.object(upgrade.time, 'sleep'):
            upgrade.apply()
        return run.call_args_list

    KWIN_KEY = ('--file', 'kwinrc', '--group', 'Wayland', '--key', 'InputMethod')

    def test_kwin_launched_fcitx_is_restarted_through_kwin(self):
        # KDE Plasma Wayland：KWin 帶著 WAYLAND_SOCKET 啟動 Fcitx5；改 kwinrc 讓 KWin 重新啟動，不執行 fcitx5 -r。
        # 寫檔不帶 --notify（Plasma 5 的 kwriteconfig5 沒有），由 busctl 送 KConfig 的變更通知。
        fake_run, which, running, kwinrc = self.kwin_desktop()
        calls = self.apply_kwin(fake_run, which, running)
        self.assertFalse(any(call.args[0] == 'fcitx5' for call in calls))
        writes = [call.args[1:] for call in calls if 'kwriteconfig6' in call.args[0]]
        desktop = '/usr/share/applications/org.fcitx.Fcitx5.desktop'
        self.assertEqual(writes, [(*self.KWIN_KEY, ''), (*self.KWIN_KEY, '--type', 'path', desktop)])
        emits = [call.args for call in calls if call.args[0].endswith('busctl') and 'emit' in call.args]
        self.assertEqual(len(emits), 2)
        self.assertEqual(emits[0][1:7], ('--user', 'emit', '/kwinrc', 'org.kde.kconfig.notify', 'ConfigChanged', 'a{saay}'))
        self.assertEqual(bytes(int(b) for b in emits[0][11:]), b'InputMethod')
        self.assertEqual(kwinrc['value'], desktop)
        self.assertEqual(len(running), 1)

    def test_kwin_restart_keeps_a_system_default_inherited(self):
        # 使用者的 kwinrc 沒有 InputMethod（值來自 /etc/xdg/kwinrc）：寫回時刪掉鍵，不寫死在使用者設定。
        # 也不用 kwriteconfig --delete：有系統預設時它寫 InputMethod[$d]，把預設遮掉。
        fake_run, which, running, _ = self.kwin_desktop(user_value=False)
        calls = self.apply_kwin(fake_run, which, running)
        writes = [call.args[1:] for call in calls if 'kwriteconfig6' in call.args[0]]
        self.assertEqual(writes, [(*self.KWIN_KEY, '')])
        self.assertEqual((self.root / 'config/kwinrc').read_text(), '[General]\nX=1\n\n[Wayland]\n')
        self.assertEqual(len(running), 1)

    def test_kwin_restart_without_busctl_uses_kwriteconfig_notify(self):
        fake_run, which, running, _ = self.kwin_desktop(tools=('kreadconfig6', 'kwriteconfig6'))
        calls = self.apply_kwin(fake_run, which, running)
        desktop = '/usr/share/applications/org.fcitx.Fcitx5.desktop'
        writes = [call.args[1:] for call in calls if 'kwriteconfig6' in call.args[0]]
        self.assertEqual(writes, [(*self.KWIN_KEY, '--notify', ''), (*self.KWIN_KEY, '--notify', '--type', 'path', desktop)])

    def test_kwin_restart_falls_back_when_kwin_does_not_react(self):
        # KWin 沒有停用輸入法：kwinrc 寫回原值，改為直接啟動，不帶失效的 WAYLAND_SOCKET。
        fake_run, which, running, kwinrc = self.kwin_desktop(ignores=True)
        calls = self.apply_kwin(fake_run, which, running)
        self.assertEqual(kwinrc['value'], '/usr/share/applications/org.fcitx.Fcitx5.desktop')
        direct = [call for call in calls if call.args[0] == 'fcitx5']
        self.assertEqual([call.args for call in direct], [('fcitx5', '-r', '-d')])
        self.assertNotIn('WAYLAND_SOCKET', direct[0].kwargs['env'])

    def test_kwin_restart_falls_back_without_kde_tools(self):
        # 找不到 kreadconfig／kwriteconfig：照原本的 fcitx5 -r，不帶 WAYLAND_SOCKET，安裝照常完成。
        fake_run, which, running, _ = self.kwin_desktop(tools=())
        calls = self.apply_kwin(fake_run, lambda name: None, running)
        direct = [call for call in calls if call.args[0] == 'fcitx5']
        self.assertEqual(len(direct), 1)
        self.assertNotIn('WAYLAND_SOCKET', direct[0].kwargs['env'])

    def test_restart_unit_under_kwin_is_handed_back(self):
        # 0.2.4 以前的更新器自己啟動的 Fcitx5（0c-fcitx5-*.service）：KWin 在執行、虛擬鍵盤是 Fcitx 時交還 KWin。
        proc = self.root / 'proc/123'
        proc.mkdir(parents=True)
        (proc / 'stat').write_text('123 (fcitx5) S 1 123 123 0')
        (proc / 'cgroup').write_text('0::/user.slice/user-1000.slice/user@1000.service/app.slice/0c-fcitx5-17926.service\n')
        (self.root / 'proc/100').mkdir()
        (self.root / 'proc/100/comm').write_text('kwin_wayland\n')
        value = ['/usr/share/applications/org.fcitx.Fcitx5.desktop\n']
        fake = lambda *args, **kwargs: subprocess.CompletedProcess(args, 0, stdout=value[0], stderr='')
        with patch.object(upgrade, 'run', side_effect=fake), \
                patch.object(upgrade.shutil, 'which', side_effect=lambda name: '/usr/bin/' + name):
            self.assertTrue(upgrade.kwin_launched(proc, {}))
            value[0] = '/usr/share/applications/com.github.maliit.keyboard.desktop\n'
            self.assertFalse(upgrade.kwin_launched(proc, {}))
            value[0] = '/usr/share/applications/org.fcitx.Fcitx5.desktop\n'
            (self.root / 'proc/100/comm').write_text('gnome-shell\n')
            self.assertFalse(upgrade.kwin_launched(proc, {}))

    def test_kwin_wayland_launcher_is_restarted_through_kwin(self):
        # 「Fcitx 5 Wayland Launcher」：fcitx5 由 D-Bus 叫起，沒有 WAYLAND_SOCKET；KWin 的子行程是 launcher。
        proc = self.root / 'proc/123'
        proc.mkdir(parents=True)
        (proc / 'environ').write_bytes(b'XDG_RUNTIME_DIR=/run/user/1000\0')
        (proc / 'stat').write_text('123 (fcitx5) S 1 123 123 0')
        (self.root / 'proc/100').mkdir()
        (self.root / 'proc/100/comm').write_text('kwin_wayland\n')
        launcher = self.root / 'proc/200'
        launcher.mkdir()
        (launcher / 'comm').write_text('fcitx5-wayland-\n')
        (launcher / 'stat').write_text('200 (fcitx5-wayland-) S 100 200 200 0')
        self.assertTrue(upgrade.kwin_launched(proc, {}))
        (self.root / 'proc/100/comm').write_text('weston\n')
        self.assertFalse(upgrade.kwin_launched(proc, {}))

    def test_other_wayland_socket_launcher_keeps_fcitx_replace(self):
        # 其他合成器（例如 Weston）也可能以 WAYLAND_SOCKET 啟動 Fcitx5；父行程不是 kwin_wayland 就不碰 kwinrc。
        proc = self.root / 'proc/123'
        proc.mkdir(parents=True)
        (proc / 'environ').write_bytes(b'WAYLAND_SOCKET=27\0')
        (proc / 'stat').write_text('123 (fcitx5) S 100 123 123 0')
        (self.root / 'proc/100').mkdir()
        (self.root / 'proc/100/comm').write_text('weston\n')
        with patch.object(upgrade, 'run', return_value=subprocess.CompletedProcess([], 0, stdout='abc\n')) as run, \
                patch.object(upgrade, 'check_data'), \
                patch.object(upgrade, 'processes', return_value=[proc]), \
                patch.object(upgrade, 'addon_loaded', return_value=True), \
                patch.object(upgrade, 'stop_old_servers'), patch.object(upgrade.time, 'sleep'):
            upgrade.apply()
            self.assertEqual(len([call for call in run.call_args_list if call.args[0] == 'fcitx5']), 1)
            self.assertFalse(any('kwriteconfig' in str(call.args[0]) or 'kreadconfig' in str(call.args[0])
                                 for call in run.call_args_list))

    def test_restart_drops_the_old_addon_search_path(self):
        # #92：舊版的搜尋路徑只有建置機的系統目錄；外殼改用絕對路徑後重啟時不再沿用。
        proc = self.root / 'proc'
        proc.mkdir()
        (proc / 'environ').write_bytes(b'DISPLAY=:0\0FCITX_ADDON_DIRS=' + str(self.home).encode()
                                       + b'/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5\0')
        with patch.object(upgrade, 'run', return_value=subprocess.CompletedProcess([], 0, stdout='abc\n')) as run, \
                patch.object(upgrade, 'check_data'), \
                patch.object(upgrade, 'processes', return_value=[proc]), \
                patch.object(upgrade, 'addon_loaded', return_value=True), \
                patch.object(upgrade, 'stop_old_servers'), patch.object(upgrade.time, 'sleep'):
            upgrade.apply()
            restart, = [call for call in run.call_args_list if call.args[0] == 'fcitx5']
            self.assertNotIn('FCITX_ADDON_DIRS', restart.kwargs['env'])
            self.assertEqual(restart.kwargs['env']['DISPLAY'], ':0')

    def test_addon_conf_gets_absolute_library_and_old_search_path_is_removed(self):
        addon = self.home / '.local/share/fcitx5/addon/0c.conf'
        files = {**self.files, '0c-addon.conf': addon}
        (self.platform / '0c-addon.conf').write_bytes((upgrade.ROOT / 'platforms/linux-fcitx5/0c-addon.conf').read_bytes())
        environment = self.home / '.config/environment.d/90-0c.conf'
        environment.parent.mkdir(parents=True)
        old = b'# \xe8\xae\x93 Fcitx 5 \xe5\x9c\xa8\xe4\xbd\xbf\xe7\x94\xa8\xe8\x80\x85\xe7\x9b\xae\xe9\x8c\x84\xe6\x89\xbe Zuin\nFCITX_ADDON_DIRS=${HOME}/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5\n'
        environment.write_bytes(old)
        with patch.object(upgrade, 'FILES', files), \
                patch.object(upgrade, 'run', return_value=subprocess.CompletedProcess([], 0, stdout='abc\n')), \
                patch.object(upgrade, 'check_data'), patch.object(upgrade, 'processes', return_value=[]), \
                patch.object(upgrade, 'stop_old_servers'), patch.object(upgrade.time, 'sleep'):
            upgrade.apply()
            self.assertIn(f'Library={self.home.resolve()}/.local/lib/fcitx5/lib0c\n', addon.read_text())
            self.assertFalse(environment.exists())
            backup, = (self.base / 'backups').iterdir()
            self.assertEqual((backup / '.config/environment.d/90-0c.conf').read_bytes(), old)
            # 已安裝的改寫內容與這次要裝的相同：不再當成變更。
            upgrade.apply()
            self.assertEqual(len(list((self.base / 'backups').iterdir())), 1)
        self.assert_preserved()

    def test_zuin_environment_carries_only_user_settings(self):
        legacy = self.legacy[4]
        legacy.parent.mkdir(parents=True, exist_ok=True)
        for content, carried in (('FCITX_ADDON_DIRS=${HOME}/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5\n', None),
                                 ('FCITX_ADDON_DIRS=${HOME}/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5\nZUIN_TRACE=1\n', 'ZUIN_TRACE=1\n')):
            with self.subTest(content=content):
                legacy.write_text(content)
                environment = self.home / '.config/environment.d/90-0c.conf'
                environment.unlink(missing_ok=True)
                upgrade.install({}, [legacy])
                self.assertEqual(environment.read_text() if environment.exists() else None, carried)

    def test_unchanged_running_server_is_not_signalled(self):
        proc = self.root / '123'
        with patch.object(upgrade, 'processes', return_value=[proc]), \
                patch.object(upgrade.os, 'pidfd_open', return_value=42, create=True), \
                patch.object(upgrade.os, 'readlink', return_value=str(self.files['build/0c-server'])), \
                patch.object(upgrade.os, 'close'), \
                patch.object(upgrade, 'running_same', return_value=True), \
                patch.object(upgrade.signal, 'pidfd_send_signal', create=True) as signal:
            upgrade.stop_old_servers()
            signal.assert_not_called()

    def test_other_installation_is_not_signalled(self):
        with patch.object(upgrade, 'processes', return_value=[Path('/proc/123')]), \
                patch.object(upgrade.os, 'pidfd_open', return_value=42, create=True), \
                patch.object(upgrade.os, 'readlink', return_value='/other/zuin-server'), \
                patch.object(upgrade.os, 'close'), \
                patch.object(upgrade.signal, 'pidfd_send_signal', create=True) as signal:
            upgrade.stop_old_servers()
            signal.assert_not_called()

    def test_old_engine_gets_only_sigterm_even_on_timeout(self):
        for ready in ([(42, 1)], []):
            with self.subTest(exited=bool(ready)), \
                    patch.object(upgrade, 'processes', side_effect=lambda name: [Path('/proc/123')] if name == '0c-server' else []), \
                    patch.object(upgrade.os, 'pidfd_open', return_value=42, create=True), \
                    patch.object(upgrade.os, 'readlink', return_value=str(self.files['build/0c-server']) + ' (deleted)'), \
                    patch.object(upgrade.os, 'close') as close, \
                    patch.object(upgrade, 'running_same', return_value=False), \
                    patch.object(upgrade.select, 'poll') as poll, \
                    patch.object(upgrade.signal, 'pidfd_send_signal', create=True) as signal:
                poll.return_value.poll.return_value = ready
                if ready:
                    upgrade.stop_old_servers()
                else:
                    with self.assertRaisesRegex(RuntimeError, '未強制終止'):
                        upgrade.stop_old_servers()
                signal.assert_called_once_with(42, upgrade.signal.SIGTERM)
                close.assert_called_once_with(42)

    def test_restart_failure_is_reported_and_keeps_backup(self):
        proc = self.root / 'proc'
        proc.mkdir()
        (proc / 'environ').write_bytes(b'FCITX_ADDON_DIRS=/old/addons\0')

        def run(*args, **kwargs):
            if args[0] == 'fcitx5':
                raise subprocess.CalledProcessError(1, args)
            return subprocess.CompletedProcess(args, 0, stdout='abc\n')

        with patch.object(upgrade, 'run', side_effect=run), \
                patch.object(upgrade, 'check_data'), \
                patch.object(upgrade, 'processes', return_value=[proc]), \
                patch.object(upgrade, 'addon_loaded', return_value=True), \
                patch.object(upgrade, 'stop_old_servers'), patch.object(upgrade.time, 'sleep'):
            with self.assertRaises(subprocess.CalledProcessError):
                upgrade.apply()
        backup, = (self.base / 'backups').iterdir()
        self.assertTrue((backup / 'fcitx-restart.log').is_file())
        for dst, contents in self.old.items():
            self.assertEqual((backup / dst.relative_to(self.home)).read_bytes(), contents)
        self.assert_preserved()

    def test_dirty_tree_refuses_pull(self):
        with patch.object(upgrade, 'run', return_value=subprocess.CompletedProcess([], 0, stdout=' M main.go\n')) as run:
            with self.assertRaisesRegex(RuntimeError, 'PULL=0'):
                upgrade.pull()
            self.assertEqual(run.call_count, 1)

    def test_pull_is_fast_forward_only_and_propagates_failure(self):
        with patch.object(upgrade, 'run', side_effect=[
                subprocess.CompletedProcess([], 0, stdout=''),
                subprocess.CalledProcessError(1, 'git pull')]) as run:
            with self.assertRaises(subprocess.CalledProcessError):
                upgrade.pull()
            self.assertEqual(run.call_args.args, ('git', 'pull', '--ff-only'))


if __name__ == '__main__':
    unittest.main()
