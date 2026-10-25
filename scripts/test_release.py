"""Check release rejection, tampering and installation recovery in temporary directories."""

from contextlib import redirect_stderr, redirect_stdout
import io
import json
import os
import shutil
import plistlib
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import install_package as installer
import build_settings_macos
import macos_version
import package_macos
import package_macos_pkg
import package_release as packager
import package_windows
import package_windows_installer
import release_feed
import site_manifest
import make_installer
import subprocess
import upgrade


class ReleaseTests(unittest.TestCase):
    def add_kimpanel_spacing(self):
        self.add_status_icons()
        source = self.bundle / installer.KIMPANEL_SPACING
        shutil.copy2(packager.PLATFORM / "kimpanel-spacing.js", source)
        self.release['files'][installer.KIMPANEL_SPACING] = installer.digest(source)
        self.save_release()
        indicator = self.home / installer.KIMPANEL_INDICATOR
        indicator.parent.mkdir(parents=True)
        original = "class Indicator {\n    _setIcon(property) {\n        this.mainIcon.gicon = property.icon;\n    }\n}\n"
        indicator.write_text(original)
        return indicator, original

    def test_kimpanel_spacing_install_repeat_rollback_and_uninstall(self):
        indicator, original = self.add_kimpanel_spacing()
        backup = installer.install(self.bundle, self.home, self.release)
        compact = indicator.read_text()
        self.assertIn('padding: 0; margin: 0;', compact)
        self.assertNotIn(installer.KIMPANEL_INDICATOR, json.loads((self.home / installer.RECEIPT).read_text())['files'])
        second = installer.install(self.bundle, self.home, self.release)
        self.assertEqual(indicator.read_text(), compact)
        installer.rollback(self.home, second)
        self.assertEqual(indicator.read_text(), compact)
        installer.rollback(self.home, backup)
        self.assertEqual(indicator.read_text(), original)
        installer.install(self.bundle, self.home, self.release)
        installer.uninstall(self.home)
        self.assertEqual(indicator.read_text(), original)
        self.assertEqual(self.user.read_text(), 'private learning')

    def test_kimpanel_downgrade_removes_patch_and_rollback_restores_it(self):
        indicator, original = self.add_kimpanel_spacing()
        installer.install(self.bundle, self.home, self.release)
        compact = indicator.read_text()
        (self.bundle / installer.KIMPANEL_SPACING).unlink()
        del self.release['files'][installer.KIMPANEL_SPACING]
        self.release['channel'] = 'preview'
        self.save_release()
        # Exercise the supported CLI: the new installer verifies and installs an older bundle.
        with patch.object(installer.sys, 'argv', ['install.py', 'install', '--bundle', str(self.bundle), '--home', str(self.home)]), \
                patch.object(installer.os, 'getuid', return_value=1000), \
                patch.object(installer, 'runtime_check'), patch.object(installer, 'require_desktop_stopped'):
            installer.main()
        backup = Path(json.loads((self.home / installer.RECEIPT).read_text())['backup'])
        self.assertEqual(indicator.read_text(), original)
        installer.rollback(self.home, backup)
        self.assertEqual(indicator.read_text(), compact)

    def test_kimpanel_downgrade_rollback_restores_block_amid_user_edits(self):
        indicator, original = self.add_kimpanel_spacing()
        installer.install(self.bundle, self.home, self.release)
        compact = indicator.read_text()
        (self.bundle / installer.KIMPANEL_SPACING).unlink()
        backup = installer.install(self.bundle, self.home, self.release)
        self.assertEqual(indicator.read_text(), original)
        edited = original.replace('class Indicator {', 'class Indicator {\n    // user customization') + '// trailing edit\n'
        indicator.write_text(edited)
        installer.rollback(self.home, backup)
        self.assertEqual(indicator.read_text(), compact.replace('class Indicator {', 'class Indicator {\n    // user customization') + '// trailing edit\n')
        installer.uninstall(self.home)
        self.assertEqual(indicator.read_text(), edited)

    def apply_make_upgrade_spacing(self, indicator):
        platform = self.root / 'source-platform'
        platform.mkdir()
        shutil.copy2(packager.PLATFORM / 'kimpanel-spacing.js', platform / 'kimpanel-spacing.js')
        with patch.object(upgrade, 'INSTALL_HOME', self.home), patch.object(upgrade, 'BASE', self.home / '.local/share/zuin'), \
                patch.object(upgrade, 'PLATFORM', platform):
            upgrade.install({}, [])
            self.assertIsNone(upgrade.install({}, []))

    def test_make_upgrade_then_package_downgrade_rollback_and_uninstall(self):
        indicator, original = self.add_kimpanel_spacing()
        # Begin with a package that knows nothing about the spacing feature.
        (self.bundle / installer.KIMPANEL_SPACING).unlink()
        del self.release['files'][installer.KIMPANEL_SPACING]
        self.save_release()
        installer.install(self.bundle, self.home, self.release)
        receipt = (self.home / installer.RECEIPT).read_bytes()
        self.apply_make_upgrade_spacing(indicator)
        compact = indicator.read_text()
        self.assertNotEqual(compact, original)
        self.assertEqual((self.home / installer.RECEIPT).read_bytes(), receipt)
        backup = installer.install(self.bundle, self.home, self.release)
        self.assertEqual(indicator.read_text(), original)
        indicator.write_text(original + '// edit after downgrade\n')
        installer.rollback(self.home, backup)
        self.assertEqual(indicator.read_text(), compact + '// edit after downgrade\n')
        installer.uninstall(self.home)
        self.assertEqual(indicator.read_text(), original + '// edit after downgrade\n')
        self.assertFalse((self.home / installer.OPTIONAL[installer.KIMPANEL_SPACING]).exists())
        self.assertEqual(self.user.read_text(), 'private learning')

    def test_make_upgrade_then_package_uninstall_without_downgrade(self):
        indicator, original = self.add_kimpanel_spacing()
        (self.bundle / installer.KIMPANEL_SPACING).unlink()
        del self.release['files'][installer.KIMPANEL_SPACING]
        self.save_release()
        installer.install(self.bundle, self.home, self.release)
        self.apply_make_upgrade_spacing(indicator)
        installer.uninstall(self.home)
        self.assertEqual(indicator.read_text(), original)
        self.assertFalse((self.home / installer.OPTIONAL[installer.KIMPANEL_SPACING]).exists())

    def test_kimpanel_downgrade_rollback_preserves_updated_hook_or_other_patch(self):
        indicator, original = self.add_kimpanel_spacing()
        installer.install(self.bundle, self.home, self.release)
        compact = indicator.read_text()
        (self.bundle / installer.KIMPANEL_SPACING).unlink()
        backup = installer.install(self.bundle, self.home, self.release)
        for i, changed in enumerate((original.replace('_setIcon(property)', '_newSetIcon(property)'),
                                     compact.replace('padding: 0;', 'padding: 3px;'))):
            with self.subTest(changed=changed):
                if i:
                    indicator.write_text(compact)
                    backup = installer.install(self.bundle, self.home, self.release)
                indicator.write_text(changed)
                installer.rollback(self.home, backup)
                self.assertEqual(indicator.read_text(), changed)

    def test_kimpanel_legacy_receipt_without_shared_record_can_remove_patch(self):
        indicator, original = self.add_kimpanel_spacing()
        installer.install(self.bundle, self.home, self.release)
        (self.home / installer.KIMPANEL_RECORD).unlink()
        installer.uninstall(self.home)
        self.assertEqual(indicator.read_text(), original)

    def test_kimpanel_provenance_tracks_actual_fragment_bytes(self):
        indicator, original = self.add_kimpanel_spacing()
        source = self.bundle / installer.KIMPANEL_SPACING
        source.write_bytes(source.read_bytes().replace(b'\n', b'\r\n'))
        self.release['files'][installer.KIMPANEL_SPACING] = installer.digest(source)
        self.save_release()
        installer.verify(self.bundle)
        installer.install(self.bundle, self.home, self.release)
        self.assertIsNotNone(installer.owned_kimpanel_snippet(self.home))
        installer.uninstall(self.home)
        self.assertEqual(indicator.read_text(), original)

    def test_kimpanel_altered_provenance_does_not_authorize_patch_removal(self):
        indicator, original = self.add_kimpanel_spacing()
        installer.install(self.bundle, self.home, self.release)
        compact = indicator.read_text()
        record = self.home / installer.KIMPANEL_RECORD
        record.write_text('{"schema": 1, "snippet_sha256": "wrong"}')
        installer.uninstall(self.home)
        self.assertEqual(indicator.read_text(), compact)
        self.assertTrue(record.exists())

    def test_kimpanel_rollback_and_uninstall_preserve_other_extension_edits(self):
        indicator, original = self.add_kimpanel_spacing()
        backup = installer.install(self.bundle, self.home, self.release)
        indicator.write_text(indicator.read_text() + '// user customization\n')
        installer.rollback(self.home, backup)
        self.assertEqual(indicator.read_text(), original + '// user customization\n')
        installer.install(self.bundle, self.home, self.release)
        indicator.write_text(indicator.read_text() + '// another edit\n')
        installer.uninstall(self.home)
        self.assertEqual(indicator.read_text(), original + '// user customization\n// another edit\n')

    def test_kimpanel_extension_update_and_edited_patch_survive_rollback(self):
        indicator, original = self.add_kimpanel_spacing()
        backup = installer.install(self.bundle, self.home, self.release)
        upgraded = original.replace('_setIcon', '_newSetIcon')
        indicator.write_text(upgraded)
        installer.rollback(self.home, backup)
        self.assertEqual(indicator.read_text(), upgraded)
        indicator.write_text(original)
        backup = installer.install(self.bundle, self.home, self.release)
        customized = indicator.read_text().replace('padding: 0;', 'padding: 2px;')
        indicator.write_text(customized)
        installer.rollback(self.home, backup)
        self.assertEqual(indicator.read_text(), customized)

    def test_kimpanel_failed_install_restores_extension_and_programs(self):
        indicator, original = self.add_kimpanel_spacing()
        real_copy = installer.atomic_copy
        def fail_receipt(src, dst):
            if dst == self.home / installer.RECEIPT:
                raise OSError('receipt failure')
            return real_copy(src, dst)
        with patch.object(installer, 'atomic_copy', side_effect=fail_receipt):
            with self.assertRaisesRegex(OSError, 'receipt failure'):
                installer.install(self.bundle, self.home, self.release)
        self.assertEqual(indicator.read_text(), original)
        self.assertFalse((self.home / installer.OPTIONAL[installer.KIMPANEL_SPACING]).exists())

    def test_kimpanel_unknown_structure_and_absent_extension_are_untouched(self):
        indicator, original = self.add_kimpanel_spacing()
        unknown = original.replace('_setIcon(property)', '_setIcon(data)')
        indicator.write_text(unknown)
        installer.install(self.bundle, self.home, self.release)
        self.assertEqual(indicator.read_text(), unknown)
        indicator.unlink()
        installer.install(self.bundle, self.home, self.release)
        self.assertFalse(indicator.exists())

    def test_legacy_bundle_does_not_inspect_unrelated_kimpanel_symlink(self):
        indicator = self.home / installer.KIMPANEL_INDICATOR
        indicator.parent.mkdir(parents=True)
        external = self.root / 'indicator.js'
        external.write_text('unrelated extension')
        indicator.symlink_to(external)
        installer.install(self.bundle, self.home, self.release)
        installer.uninstall(self.home)
        self.assertEqual(external.read_text(), 'unrelated extension')

    def test_kimpanel_symlink_is_rejected_without_modifying_target(self):
        indicator, original = self.add_kimpanel_spacing()
        indicator.unlink()
        external = self.root / 'indicator.js'
        external.write_text(original)
        indicator.symlink_to(external)
        with self.assertRaisesRegex(ValueError, '符號連結'):
            installer.install(self.bundle, self.home, self.release)
        self.assertEqual(external.read_text(), original)
        self.assertFalse((self.home / installer.RECEIPT).exists())

    def test_kimpanel_tampered_replacement_backup_rejected(self):
        indicator, original = self.add_kimpanel_spacing()
        backup = installer.install(self.bundle, self.home, self.release)
        indicator.write_text(indicator.read_text() + '// user customization\n')
        current = indicator.read_text()
        (backup / 'new-indicator.js').write_text('tampered')
        with self.assertRaisesRegex(ValueError, '備份雜湊'):
            installer.rollback(self.home, backup)
        self.assertEqual(indicator.read_text(), current)

    def add_legacy_status_icons(self):
        self.add_status_icons()
        for name in installer.LEGACY_STATUS_ICONS:
            target = self.bundle / name
            target.write_text('<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16"/>\n')
            self.release['files'][name] = installer.digest(target)
        self.save_release()

    def remove_legacy_status_icons(self):
        for name in installer.LEGACY_STATUS_ICONS:
            (self.bundle / name).unlink()
            del self.release['files'][name]
        self.save_release()

    def test_color_icons_remove_old_symbolic_and_support_rollback_and_downgrade(self):
        self.add_legacy_status_icons()
        installer.verify(self.bundle)
        installer.install(self.bundle, self.home, self.release)
        old = {name: (self.home / name).read_bytes() for name in installer.LEGACY_STATUS_ICONS.values()}
        self.remove_legacy_status_icons()
        installer.verify(self.bundle)
        backup = installer.install(self.bundle, self.home, self.release)
        self.assertTrue(all(not (self.home / name).exists() for name in old))
        receipt = json.loads((self.home / installer.RECEIPT).read_text())
        self.assertTrue(all(name not in receipt['files'] for name in old))
        self.assertTrue(all((self.home / name).is_file() for name in installer.STATUS_ICONS.values()))
        installer.rollback(self.home, backup)
        self.assertEqual({name: (self.home / name).read_bytes() for name in old}, old)
        installer.install(self.bundle, self.home, self.release)
        self.add_legacy_status_icons()
        installer.verify(self.bundle)
        installer.install(self.bundle, self.home, self.release)
        self.assertEqual({name: (self.home / name).read_bytes() for name in old}, old)
        installer.uninstall(self.home)
        self.assertTrue(all(not (self.home / name).exists() for name in old))
        self.assertEqual(self.user.read_text(), 'private learning')

    def test_icon_changes_refresh_running_gnome_icon_theme(self):
        theme = self.home / installer.ICON_THEME
        applications = self.home / installer.APPLICATIONS
        self.add_legacy_status_icons()
        installer.install(self.bundle, self.home, self.release)
        applications.mkdir(parents=True)
        def age():
            for path in (theme, applications):
                os.utime(path, (1_000_000, 1_000_000))
        age()
        # 同樣的圖示重裝：不動目錄時間，桌面不必重讀。
        before = installer.icon_files(self.home)
        installer.install(self.bundle, self.home, self.release)
        self.assertFalse(installer.refresh_icon_theme(self.home, before))
        self.assertEqual([p.stat().st_mtime for p in (theme, applications)], [1_000_000] * 2)
        # 移除單色圖：hicolor 與應用程式目錄都要換時間，GNOME Shell 才會重讀並改用彩色圖。
        self.remove_legacy_status_icons()
        before = installer.icon_files(self.home)
        installer.install(self.bundle, self.home, self.release)
        self.assertTrue(installer.refresh_icon_theme(self.home, before))
        self.assertTrue(all(p.stat().st_mtime > 1_000_000 for p in (theme, applications)))

    def test_failed_color_icon_upgrade_restores_removed_symbolic_icons(self):
        self.add_legacy_status_icons()
        installer.install(self.bundle, self.home, self.release)
        old = {name: (self.home / name).read_bytes() for name in installer.LEGACY_STATUS_ICONS.values()}
        self.remove_legacy_status_icons()
        real_copy = installer.atomic_copy
        def fail_receipt(src, dst):
            if dst == self.home / installer.RECEIPT and src.name == 'new-receipt.json':
                raise OSError('receipt write failure')
            return real_copy(src, dst)
        with patch.object(installer, 'atomic_copy', side_effect=fail_receipt):
            with self.assertRaisesRegex(OSError, 'receipt write failure'):
                installer.install(self.bundle, self.home, self.release)
        self.assertEqual({name: (self.home / name).read_bytes() for name in old}, old)
        self.assertEqual(self.user.read_text(), 'private learning')

    def test_kde_icon_refresh_only_targets_installing_users_session(self):
        with patch.object(installer.Path, 'home', return_value=self.home), \
             patch.dict(os.environ, {'DBUS_SESSION_BUS_ADDRESS': 'unix:path=/private-test-bus'}), \
             patch.object(installer.shutil, 'which', return_value='/usr/bin/busctl'), \
             patch.object(installer.subprocess, 'run') as run:
            run.return_value.returncode = 0
            self.assertFalse(installer.refresh_kde_icons(self.home / 'other-user', run))
            with patch.dict(os.environ, {'DBUS_SESSION_BUS_ADDRESS': ''}):
                self.assertFalse(installer.refresh_kde_icons(self.home, run))
            run.assert_not_called()
            self.assertTrue(installer.refresh_kde_icons(self.home, run))
            run.assert_called_once_with(
                ['busctl', '--user', 'emit', '/KIconLoader', 'org.kde.KIconLoader', 'iconChanged', 'i', '0'],
                capture_output=True, timeout=3)
            run.reset_mock()
            with patch.object(installer.shutil, 'which', side_effect=lambda name: '/usr/bin/dbus-send' if name == 'dbus-send' else None):
                self.assertTrue(installer.refresh_kde_icons(self.home, run))
            self.assertEqual(run.call_args.args[0], [
                'dbus-send', '--session', '--type=signal', '/KIconLoader', 'org.kde.KIconLoader.iconChanged', 'int32:0'])
            run.side_effect = subprocess.TimeoutExpired('busctl', 3)
            self.assertFalse(installer.refresh_kde_icons(self.home, run))

    def test_icon_refresh_notifies_kde_only_when_icons_change(self):
        with patch.object(installer, 'refresh_kde_icons') as notify:
            self.assertFalse(installer.refresh_icon_theme(self.home, installer.icon_files(self.home)))
            notify.assert_not_called()
            before = installer.icon_files(self.home)
            icon = self.home / next(iter(installer.STATUS_ICONS.values()))
            icon.parent.mkdir(parents=True, exist_ok=True)
            icon.write_text('<svg/>')
            self.assertTrue(installer.refresh_icon_theme(self.home, before))
            notify.assert_called_once_with(self.home)

    def add_status_icons(self):
        for name in installer.STATUS_ICONS:
            target = self.bundle / name
            target.parent.mkdir(exist_ok=True)
            shutil.copy2(packager.PLATFORM / name, target)
            self.release["files"][name] = installer.digest(target)
        self.save_release()

    def test_status_icons_upgrade_downgrade_rollback_and_uninstall(self):
        preference = self.home / ".config/fcitx5/conf/classicui.conf"
        preference.parent.mkdir(parents=True)
        preference.write_text("PreferTextIcon=True\n")
        # A receipt from before the icons existed remains usable.
        installer.install(self.bundle, self.home, self.release)
        self.add_status_icons()
        installer.verify(self.bundle)
        backup = installer.install(self.bundle, self.home, self.release)
        self.assertEqual(preference.read_text(), "PreferTextIcon=False\n")
        targets = [self.home / name for name in installer.STATUS_ICONS.values()]
        expected = [path.read_bytes() for path in targets]
        installer.rollback(self.home, backup)
        self.assertEqual(preference.read_text(), "PreferTextIcon=True\n")
        self.assertTrue(all(not path.exists() for path in targets))
        installer.install(self.bundle, self.home, self.release)
        for name in installer.STATUS_ICONS:
            (self.bundle / name).unlink()
            del self.release["files"][name]
        self.save_release()
        installer.verify(self.bundle)
        backup = installer.install(self.bundle, self.home, self.release)
        self.assertTrue(all(not path.exists() for path in targets))
        installer.rollback(self.home, backup)
        self.assertEqual([path.read_bytes() for path in targets], expected)
        installer.uninstall(self.home)
        self.assertTrue(all(not path.exists() for path in targets))
        self.assertEqual(preference.read_text(), "PreferTextIcon=False\n")
        self.assertEqual(self.user.read_text(), "private learning")

    def test_icon_preference_created_and_removed_on_rollback(self):
        self.add_status_icons()
        preference = self.home / installer.CLASSIC_UI
        backup = installer.install(self.bundle, self.home, self.release)
        self.assertEqual(preference.read_text(), "PreferTextIcon=False\n")
        self.assertNotIn(installer.CLASSIC_UI, json.loads((self.home / installer.RECEIPT).read_text())["files"])
        installer.rollback(self.home, backup)
        self.assertFalse(preference.exists())

    def test_both_panel_preferences_are_transactional(self):
        self.add_status_icons()
        for relative in installer.UI_CONFIGS:
            path = self.home / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("# local\nPreferTextIcon=True\n")
        backup = installer.install(self.bundle, self.home, self.release)
        for relative in installer.UI_CONFIGS:
            self.assertEqual((self.home / relative).read_text(), "# local\nPreferTextIcon=False\n")
        installer.rollback(self.home, backup)
        for relative in installer.UI_CONFIGS:
            self.assertEqual((self.home / relative).read_text(), "# local\nPreferTextIcon=True\n")

    def test_second_panel_failure_restores_first_panel_and_programs(self):
        self.add_status_icons()
        for relative in installer.UI_CONFIGS:
            path = self.home / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("PreferTextIcon=True\n")
        real_copy = installer.atomic_copy
        def fail_second_panel(src, dst):
            if dst == self.home / installer.UI_CONFIGS[1]:
                raise OSError("second panel disk failure")
            real_copy(src, dst)
        with patch.object(installer, "atomic_copy", side_effect=fail_second_panel):
            with self.assertRaisesRegex(OSError, "second panel"):
                installer.install(self.bundle, self.home, self.release)
        for relative in installer.UI_CONFIGS:
            self.assertEqual((self.home / relative).read_text(), "PreferTextIcon=True\n")
        self.assertFalse((self.home / installer.RECEIPT).exists())
        self.assertTrue(all(not (self.home / name).exists() for name in installer.STATUS_ICONS.values()))

    def test_icon_preference_preserves_sections_and_comments(self):
        self.add_status_icons()
        preference = self.home / installer.CLASSIC_UI
        preference.parent.mkdir(parents=True)
        original = b"# theme\r\nTheme=Yaru-dark\r\n# PreferTextIcon=True\r\n[Font]\r\nPreferTextIcon=True\r\n"
        preference.write_bytes(original)
        backup = installer.install(self.bundle, self.home, self.release)
        self.assertEqual(preference.read_bytes(), original.replace(b"[Font]", b"PreferTextIcon=False\r\n[Font]"))
        installer.rollback(self.home, backup)
        self.assertEqual(preference.read_bytes(), original)

    def test_icon_rollback_preserves_later_theme_or_preference_changes(self):
        self.add_status_icons()
        preference = self.home / installer.CLASSIC_UI
        preference.parent.mkdir(parents=True)
        for changed, expected in (("Theme=New\nPreferTextIcon=False\n", "Theme=New\nPreferTextIcon=True\n"),
                                  ("Theme=New\nPreferTextIcon=True\n", "Theme=New\nPreferTextIcon=True\n")):
            with self.subTest(changed=changed):
                preference.write_text("Theme=Old\nPreferTextIcon=True\n")
                backup = installer.install(self.bundle, self.home, self.release)
                preference.write_text(changed)
                installer.rollback(self.home, backup)
                self.assertEqual(preference.read_text(), expected)

    def test_icon_preference_failure_restores_programs_and_original_config(self):
        installer.install(self.bundle, self.home, self.release)
        self.add_status_icons()
        preference = self.home / installer.CLASSIC_UI
        preference.parent.mkdir(parents=True)
        original = "Theme=Original\nPreferTextIcon=True\n"
        preference.write_text(original)
        real_copy = installer.atomic_copy
        def fail_receipt(src, dst):
            if dst == self.home / installer.RECEIPT and src.name == "new-receipt.json":
                raise OSError("disk full after preference")
            return real_copy(src, dst)
        with patch.object(installer, "atomic_copy", side_effect=fail_receipt):
            with self.assertRaisesRegex(OSError, "disk full"):
                installer.install(self.bundle, self.home, self.release)
        self.assertEqual(preference.read_text(), original)
        self.assertTrue(all(not (self.home / name).exists() for name in installer.STATUS_ICONS.values()))
        self.assertEqual(self.user.read_text(), "private learning")

    def test_icon_preference_symlink_rejected_before_install(self):
        self.add_status_icons()
        preference = self.home / installer.CLASSIC_UI
        preference.parent.mkdir(parents=True)
        external = self.root / "external.conf"
        external.write_text("PreferTextIcon=True\n")
        preference.symlink_to(external)
        with self.assertRaisesRegex(ValueError, "符號連結"):
            installer.install(self.bundle, self.home, self.release)
        self.assertEqual(external.read_text(), "PreferTextIcon=True\n")
        self.assertFalse((self.home / installer.RECEIPT).exists())

    def test_status_icons_partial_or_tampered_bundle_rejected(self):
        self.add_status_icons()
        name = next(iter(installer.STATUS_ICONS))
        (self.bundle / name).write_text("tampered")
        with self.assertRaisesRegex(ValueError, "雜湊"):
            installer.verify(self.bundle)
        del self.release["files"][name]
        (self.bundle / name).unlink()
        self.save_release()
        with self.assertRaisesRegex(ValueError, "狀態圖示"):
            installer.verify(self.bundle)

    @staticmethod
    def data_snapshot(stage):
        return {p.relative_to(stage): (p.read_bytes(), p.stat().st_mtime_ns)
                for p in stage.rglob("*") if p.is_file()}

    @staticmethod
    def source_notice(sources, statements=None):
        statements = statements or ["Permission is granted to redistribute this test fixture."] * len(sources)
        return "資料包\n\n" + "\n".join(
            f"== {s['Name']}（{s['ID']}）==\n授權：{s['License']}\n版本：{s['Version']}\n"
            f"網址：{s['URL']}\n姓名標示：{s['Attribution']}\n\n{statement}\n"
            for s, statement in zip(sources, statements))

    def write_paired_data(self, stage, *, public=False):
        """A paired fixture, including legacy mixed data that must never be sanitized for release."""
        source = {"ID": "fixture", "Name": "測試詞條", "URL": "https://example.org/fixture",
                  "Version": "1.0", "License": "MIT", "Attribution": "Fixture authors", "Distributable": True}
        manifest = {"format": 2, "files": {}, "counts": {"words": 3},
                    "options": {"Public": public}, "sources": [source], "runtime": {"context_weight": 0.075}}
        if not public:
            source = dict(source, ID=packager.PRIVATE_RESEARCH_SOURCE, License="研究資料", Distributable=False)
            manifest["sources"].append(source)
            manifest["research_news_mix"] = {"source": source, "base_manifest_sha256": "c" * 64,
                                             "bigram": {"max_row_mass_change": 1e-12}}
        data, neural = stage / "data", stage / "neural"
        data.mkdir(parents=True)
        for name in ("lexicon.bin", "lm.bin"):
            (data / name).write_bytes(("fixture " + name).encode())
            manifest["files"][name] = {"sha256": installer.digest(data / name), "size": (data / name).stat().st_size}
        (data / "report.txt").write_text("fixture\n")
        (data / "manifest.json").write_bytes(packager.json_bytes(manifest))
        notice = self.source_notice(manifest["sources"])
        if not public:
            notice += "研究來源：" + json.dumps(source, ensure_ascii=False) + "\n"
        (data / "NOTICE.txt").write_text(notice, encoding="utf-8")
        old = installer.digest(data / "manifest.json")
        for kind in ("select", "punct"):
            (neural / kind).mkdir(parents=True)
            (neural / kind / "model.onnx").write_bytes(kind.encode())
            (neural / kind / "vocab.json").write_bytes(packager.json_bytes(
                {"schema": "zuin-phonetic-v1", "characters": ["一", "ㄅ"], "dictionary_sha256": old}))
            model = {"schema": kind, "files": {"model.onnx": installer.digest(neural / kind / "model.onnx"),
                                               "vocab.json": installer.digest(neural / kind / "vocab.json")}}
            if kind == "select":
                model["dictionary_sha256"] = old
            (neural / kind / "manifest.json").write_bytes(packager.json_bytes(model))
        return data, neural, manifest, old

    def test_public_collect_sources_retain_attribution_and_license(self):
        root = Path(__file__).resolve().parents[1]
        sources, statements = [], []
        for line in (root / "data/sources/ledger.tsv").read_text(encoding="utf-8").splitlines():
            fields = line.split("\t")
            if fields[0] in ("0c_collect", "0c_collect-ns", "0c_collect-ss"):
                source = dict(zip(("ID", "Name", "URL", "Version", "License", "Distributable", "Attribution"), fields))
                self.assertEqual(source["License"], "CC-BY-SA-4.0")
                self.assertEqual(source["Distributable"], "yes")
                source["Distributable"] = True
                sources.append(source)
                statements.append((root / "data/sources/licenses" / (source["ID"] + ".txt")).read_text(encoding="utf-8"))
        self.assertEqual(len(sources), 3)
        data, _, manifest, _ = self.write_paired_data(self.root / "collect", public=True)
        manifest["sources"] = sources
        (data / "manifest.json").write_bytes(packager.json_bytes(manifest))
        (data / "NOTICE.txt").write_text(self.source_notice(sources, statements), encoding="utf-8")
        before = self.data_snapshot(data)
        self.assertEqual(packager.prepare_shipped_data(data), installer.digest(data / "manifest.json"))
        self.assertEqual(self.data_snapshot(data), before)

    def test_ledger_sources_have_complete_public_notices(self):
        sources, statements = [], []
        for line in (packager.ROOT / "data/sources/ledger.tsv").read_text(encoding="utf-8").splitlines():
            if not line or line.startswith("#"):
                continue
            values = line.split("\t")
            self.assertEqual(len(values), 7)
            source = dict(zip(("ID", "Name", "URL", "Version", "License", "Distributable", "Attribution"), values))
            self.assertEqual(source["Distributable"], "yes")
            source["Distributable"] = True
            sources.append(source)
            statement = (packager.ROOT / "data/sources/licenses" / (source["ID"] + ".txt")).read_text(encoding="utf-8")
            self.assertTrue(statement.strip(), source["ID"])
            statements.append(statement)
        packager.check_public_sources({"options": {"Public": True}, "sources": sources},
                                     self.source_notice(sources, statements))

    def test_preview_preserves_private_sources_notices_and_model_pairing(self):
        stage = self.root / "stage"
        data, neural, _, old = self.write_paired_data(stage)
        before = self.data_snapshot(stage)
        self.assertEqual(packager.prepare_shipped_data(data, neural, public=False), old)
        self.assertEqual(self.data_snapshot(stage), before)
        with self.assertRaisesRegex(ValueError, "-public"):
            packager.prepare_shipped_data(data, neural)
        self.assertEqual(self.data_snapshot(stage), before)

    def test_public_manifest_and_models_are_never_rebound(self):
        stage = self.root / "stage"
        data, neural, _, old = self.write_paired_data(stage, public=True)
        before = self.data_snapshot(stage)
        for models in (None, neural):
            self.assertEqual(packager.prepare_shipped_data(data, models), old)
            self.assertEqual(self.data_snapshot(stage), before)

    def test_shipped_manifest_rejects_broken_pairing_without_writing(self):
        def snapshot(stage):
            return {p: p.read_bytes() for p in stage.rglob("*") if p.is_file()}
        cases = {
            "選字 manifest": ("select/manifest.json", "dictionary_sha256", "不配對"),
            "選字 vocab": ("select/vocab.json", "dictionary_sha256", "vocab.json 與模型 manifest 不符"),
            "補標點 vocab": ("punct/vocab.json", "dictionary_sha256", "vocab.json 與模型 manifest 不符"),
        }
        for label, (name, key, error) in cases.items():
            with self.subTest(label):
                stage = self.root / ("broken-" + name.replace("/", "-"))
                data, neural, _, _ = self.write_paired_data(stage)
                path = neural / name
                value = json.loads(path.read_text(encoding="utf-8"))
                value[key] = "d" * 64
                path.write_bytes(packager.json_bytes(value))
                before = snapshot(stage)
                with self.assertRaisesRegex(ValueError, error):
                    packager.prepare_data_manifest(data, neural)
                self.assertEqual(snapshot(stage), before)
        # vocab.json 雜湊照樣登記在模型 manifest、只是記的字典不同：配對不成立。
        for kind in ("select", "punct"):
            with self.subTest(kind + " 登記過的 vocab"):
                stage = self.root / ("stale-" + kind)
                data, neural, _, _ = self.write_paired_data(stage)
                vocab = json.loads((neural / kind / "vocab.json").read_text(encoding="utf-8"))
                vocab["dictionary_sha256"] = "d" * 64
                (neural / kind / "vocab.json").write_bytes(packager.json_bytes(vocab))
                model = json.loads((neural / kind / "manifest.json").read_text(encoding="utf-8"))
                model["files"]["vocab.json"] = installer.digest(neural / kind / "vocab.json")
                (neural / kind / "manifest.json").write_bytes(packager.json_bytes(model))
                before = snapshot(stage)
                with self.assertRaisesRegex(ValueError, "不配對"):
                    packager.prepare_data_manifest(data, neural)
                self.assertEqual(snapshot(stage), before)

    def test_seal_data_rejects_before_obfuscation_or_metadata_changes(self):
        stage = self.root / "stage"
        self.write_paired_data(stage)
        before = self.data_snapshot(stage)
        with patch.object(packager.subprocess, "run") as run:
            with self.assertRaisesRegex(ValueError, "-public"):
                packager.seal_data(stage, "go")
        run.assert_not_called()
        self.assertEqual(self.data_snapshot(stage), before)

    def test_seal_data_preserves_provenance_and_pairing(self):
        for public in (False, True):
            with self.subTest(public=public):
                stage = self.root / str(public)
                self.write_paired_data(stage, public=public)
                before = self.data_snapshot(stage)
                with patch.object(packager.subprocess, "run") as run:
                    packager.seal_data(stage, "go", public=public)
                run.assert_called_once_with(["go", "run", "./cmd/0c-compile", "-seal", str(stage / "data")],
                                            cwd=packager.ROOT, check=True)
                self.assertEqual(self.data_snapshot(stage), before)

    def test_english_staging_keeps_chinese_hash_and_detects_tampering(self):
        source = self.bundle / "data"
        before = installer.digest(source / "manifest.json")
        packager.copy_english(source, source, "go")
        self.assertEqual(installer.digest(source / "manifest.json"), before)
        manifest = packager.check_english(source)
        self.assertEqual(manifest["words"], 82015)
        copied = self.root / "copied-data"
        copied.mkdir()
        with patch.object(packager.subprocess, "run", side_effect=AssertionError("Existing packs must be copied")):
            packager.copy_english(source, copied, "go")
        for name in packager.ENGLISH_FILES:
            self.assertEqual(installer.digest(source / "english" / name), installer.digest(copied / "english" / name))
        (copied / "english/words.bin").write_bytes(b"tampered")
        with self.assertRaisesRegex(ValueError, "雜湊"):
            packager.check_english(copied)

    def test_english_install_downgrade_rollback_and_uninstall_keep_user_data(self):
        data = self.bundle / "data"
        packager.copy_english(data, data, "go")
        english = data / "english"
        for name in packager.ENGLISH_FILES:
            relative = "data/english/" + name
            self.release["files"][relative] = installer.digest(english / name)
        self.save_release()
        installer.verify(self.bundle)
        installer.install(self.bundle, self.home, self.release)
        installed = self.home / ".local/share/zuin/data/english"
        expected = {name: (installed / name).read_bytes() for name in packager.ENGLISH_FILES}
        self.assertEqual(expected["words.bin"], (english / "words.bin").read_bytes())
        # A package without optional English files must remove the previous pack,
        # and rollback must restore all of it with the existing transaction.
        shutil.rmtree(english)
        self.release["files"] = {k: v for k, v in self.release["files"].items() if not k.startswith("data/english/")}
        self.save_release()
        backup = installer.install(self.bundle, self.home, self.release)
        self.assertFalse(any(installed.iterdir()))
        installer.rollback(self.home, backup)
        for name, content in expected.items():
            self.assertEqual((installed / name).read_bytes(), content)
        installer.uninstall(self.home)
        self.assertTrue((installed / "words.bin").exists())
        self.assertEqual(self.user.read_text(), "private learning")

    def test_partial_english_pack_rejected_before_install(self):
        name = "data/english/words.bin"
        path = self.bundle / name
        path.parent.mkdir()
        path.write_bytes(b"partial")
        self.release["files"][name] = installer.digest(path)
        self.save_release()
        with self.assertRaisesRegex(ValueError, "英文資料包"):
            installer.verify(self.bundle)

    def test_one_line_installer_pins_bootstrap_and_rejects_unsafe_values(self):
        with tempfile.TemporaryDirectory() as temp:
            boot = Path(temp) / "0c-update"
            boot.write_bytes(b"bootstrap")
            out = make_installer.generate(Path(temp) / "out", "https://example.test/feeds", "https://example.test/feeds/public-beta.json",
                                          "public-beta", {"linux_amd64": (boot, "https://example.test/releases/v/0c-update")})
            sh = out["install.sh"].read_text()
            self.assertIn(make_installer.sha256(boot), sh)
            self.assertIn("Darwin-arm64) URL=''", sh)
            subprocess.run(["sh", "-n", str(out["install.sh"])], check=True)
            self.assertIn(b"\r\n", out["install.cmd"].read_bytes())
            with self.assertRaises(ValueError):
                make_installer.generate(Path(temp) / "bad", "https://example.test/feeds", "https://x/'; rm -rf ~", "public-beta", {})

    def test_first_install_allowed_in_running_desktop_only_without_earlier_install(self):
        with tempfile.TemporaryDirectory() as temp:
            home = Path(temp)
            self.assertTrue(installer.first_install(home))
            (home / ".local/lib/fcitx5").mkdir(parents=True)
            (home / ".local/lib/fcitx5/libzuin.so").write_text("old")
            self.assertFalse(installer.first_install(home))

    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.bundle = self.root / "bundle"
        self.home = self.root / "home"
        self.home.mkdir()
        for name in (*installer.PAYLOAD, "install.py"):
            path = self.bundle / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("new " + name)
        shutil.copy2(packager.PLATFORM / "0c-addon.conf", self.bundle / installer.ADDON_CONF)
        self.release = {"schema": 1, "product": "0c", "version": "0.1.0-beta.1", "files": {
            str(p.relative_to(self.bundle)): installer.digest(p) for p in self.bundle.rglob("*") if p.is_file()}}
        self.save_release()
        self.user = self.home / ".local/share/zuin/user.json"
        self.user.parent.mkdir(parents=True)
        self.user.write_text("private learning")

    def save_release(self):
        (self.bundle / "release.json").write_text(json.dumps(self.release))

    def test_verify_rejects_tampering_missing_files_and_traversal(self):
        installer.verify(self.bundle)
        binary = self.bundle / "bin/0c-server"
        binary.write_text("tampered")
        with self.assertRaisesRegex(ValueError, "雜湊"):
            installer.verify(self.bundle)
        binary.unlink()
        with self.assertRaisesRegex(ValueError, "一般檔案"):
            installer.verify(self.bundle)
        self.release["files"] = {"../escape": "0"}
        self.save_release()
        with self.assertRaises(ValueError):
            installer.verify(self.bundle)

    def test_install_update_and_uninstall_keep_learning_and_dictionary(self):
        installer.install(self.bundle, self.home, self.release)
        server = self.home / ".local/bin/0c-server"
        old = server.read_bytes()
        (self.bundle / "bin/0c-server").write_text("updated engine")
        backup = installer.install(self.bundle, self.home, self.release)
        self.assertEqual((backup / ".local/bin/0c-server").read_bytes(), old)
        self.assertEqual(server.read_text(), "updated engine")
        local = self.home / ".local/share/fcitx5/inputmethod/zuin.conf"
        local.write_text("user customization")
        retained = installer.uninstall(self.home)
        self.assertFalse(server.exists())
        self.assertIn(str(local.relative_to(self.home)), retained)
        self.assertEqual(local.read_text(), "user customization")
        self.assertEqual(self.user.read_text(), "private learning")
        self.assertTrue((self.home / ".local/share/zuin/data/manifest.json").exists())

    def test_failed_update_rolls_back_without_changing_learning(self):
        installer.install(self.bundle, self.home, self.release)
        before = {p: p.read_bytes() for p in self.home.rglob("*") if p.is_file() and "backups" not in p.parts}
        real_copy = installer.atomic_copy
        count = 0
        def fail_third(src, dst):
            nonlocal count
            count += 1
            if count == 3:
                raise OSError("disk full")
            real_copy(src, dst)
        with patch.object(installer, "atomic_copy", side_effect=fail_third):
            with self.assertRaisesRegex(OSError, "disk full"):
                installer.install(self.bundle, self.home, self.release)
        for path, content in before.items():
            self.assertEqual(path.read_bytes(), content)

    def test_target_symlink_is_rejected_before_writes(self):
        (self.home / ".local/bin").symlink_to(self.bundle / "bin")
        with self.assertRaisesRegex(ValueError, "符號連結"):
            installer.install(self.bundle, self.home, self.release)
        self.assertFalse((self.home / ".local/share/zuin/backups").exists())

    def test_install_keeps_only_the_new_backup(self):
        # #179：每份安裝備份都含整個舊神經模型，以前從不刪。安裝成功後只留收據記錄的這一份，回復照常。
        backups = self.home / ".local/share/zuin/backups"
        first = installer.install(self.bundle, self.home, self.release)
        installer.install(self.bundle, self.home, self.release)
        (backups / "0c-install-old_old1" / "neural").mkdir(parents=True)
        (backups / "0c-install-old_old1" / "neural/model.onnx").write_text("old model")
        for name in ("upgrade-20261011-025419-t7yd75aw", "manual-dev39-20261003", "0c-user-abcd1234", "0c-install-notours"):
            (backups / name).mkdir()
        (backups / "user.json.pre-hint").write_text("hand-made copy")
        elsewhere = self.root / "elsewhere"
        elsewhere.mkdir()
        (elsewhere / "keep").write_text("not ours")
        (backups / "0c-install-linked01").symlink_to(elsewhere)
        (self.bundle / "bin/0c-server").write_text("next engine")
        self.release["files"]["bin/0c-server"] = installer.digest(self.bundle / "bin/0c-server")
        self.release["channel"] = "internal-preview"
        self.save_release()
        with patch.object(installer.sys, "argv", ["install.py", "install", "--bundle", str(self.bundle), "--home", str(self.home)]), \
                patch.object(installer.os, "getuid", return_value=1000), \
                patch.dict(installer.os.environ, {"ZC_MANAGED_RELOAD": "1"}), \
                patch.object(installer, "runtime_check"), patch.object(installer, "require_desktop_stopped"), \
                patch.object(installer, "clear_session_addon_dirs"), patch.object(installer, "settings_dependencies"), \
                patch.object(installer, "first_install", return_value=True), redirect_stdout(io.StringIO()):
            installer.main()
        backup = Path(json.loads((self.home / installer.RECEIPT).read_text())["backup"])
        self.assertEqual(sorted(p.name for p in backups.iterdir()), sorted([
            backup.name, "upgrade-20261011-025419-t7yd75aw", "manual-dev39-20261003", "0c-user-abcd1234",
            "0c-install-notours", "user.json.pre-hint", "0c-install-linked01"]))
        self.assertFalse(first.exists())
        self.assertEqual((elsewhere / "keep").read_text(), "not ours")
        installer.rollback(self.home, backup)
        self.assertEqual((self.home / ".local/bin/0c-server").read_text(), "new bin/0c-server")
        self.assertEqual(self.user.read_text(), "private learning")

    def test_backup_pruning_refuses_a_linked_backup_directory(self):
        real = self.root / "external"
        (real / "0c-install-abcdefgh").mkdir(parents=True)
        backups = self.home / ".local/share/zuin/backups"
        backups.symlink_to(real)
        with redirect_stderr(io.StringIO()):
            self.assertEqual(installer.prune_backups(self.home, backups / "0c-install-new12345"), [])
        self.assertTrue((real / "0c-install-abcdefgh").exists())

    def test_rollback_restores_paired_data_and_keeps_learning(self):
        first = installer.install(self.bundle, self.home, self.release)
        before = {target: (self.home / target).read_bytes() for target in installer.PAYLOAD.values()}
        (self.bundle / "data/lm.bin").write_text("next dictionary")
        (self.bundle / "bin/0c-server").write_text("next engine")
        self.release["version"] = "0.2.0"
        second = installer.install(self.bundle, self.home, self.release)
        installer.rollback(self.home, second)
        for target, content in before.items():
            self.assertEqual((self.home / target).read_bytes(), content)
        self.assertEqual(self.user.read_text(), "private learning")
        installer.rollback(self.home, first)
        self.assertFalse((self.home / installer.RECEIPT).exists())
        self.assertEqual(self.user.read_text(), "private learning")

    def test_addon_library_is_absolute_without_environment_file(self):
        # dev.59 以前的套件附有 environment.conf；新安裝器（含降版）一律不再安裝它。
        old_env = self.bundle / "config/environment.conf"
        old_env.write_text("FCITX_ADDON_DIRS=${HOME}/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5\n")
        self.release["files"]["config/environment.conf"] = installer.digest(old_env)
        self.save_release()
        installer.install(self.bundle, self.home, self.release)
        conf = self.home / ".local/share/fcitx5/addon/0c.conf"
        library = [line for line in conf.read_text().splitlines() if line.startswith("Library=")]
        self.assertEqual(library, [f"Library={self.home.resolve()}/.local/lib/fcitx5/lib0c"])
        self.assertIn("Type=SharedLibrary", conf.read_text())  # 其他設定照套件
        self.assertFalse((self.home / installer.ADDON_ENV).exists())
        receipt = json.loads((self.home / installer.RECEIPT).read_text())
        self.assertEqual(receipt["files"][".local/share/fcitx5/addon/0c.conf"], installer.digest(conf))
        installer.uninstall(self.home)
        self.assertFalse(conf.exists())

    def test_addon_library_rejects_home_fcitx_cannot_express(self):
        content = (packager.PLATFORM / "0c-addon.conf").read_bytes()
        for home in ("/home/a;b", '/home/a"b', "/home/a\\b", "/home/a\nb"):
            with self.assertRaisesRegex(ValueError, "家目錄"):
                installer.addon_conf(content, Path(home))
        with self.assertRaisesRegex(ValueError, "Library"):
            installer.addon_conf(b"[Addon]\nName=x\n", self.home)
        self.assertIn(b"Library=/home/\xe7\x8e\x8b/.local/lib/fcitx5/lib0c\n", installer.addon_conf(content, Path("/home/王")))

    def test_old_addon_dirs_are_recognized_without_touching_user_directories(self):
        home = Path("/home/u")
        for value in ("${HOME}/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5",  # dev.59 以前（Debian 建置機）
                      "${HOME}/.local/lib/fcitx5:/usr/lib64/fcitx5",  # #92 使用者自己改成 Fedora 路徑
                      "/home/u/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5:/usr/lib64/fcitx5",
                      '"$HOME/.local/lib/fcitx5:/usr/lib/fcitx5/"', "/home/u/.local/lib/fcitx5"):
            self.assertTrue(installer.obsolete_addon_dirs(value, home), value)
        for value in ("/usr/lib64/fcitx5", "", "${HOME}/.local/lib/fcitx5:/opt/my-addons/fcitx5",
                      "/home/other/.local/lib/fcitx5:/usr/lib/fcitx5", "${HOME}/build/fcitx5:/usr/lib/fcitx5"):
            self.assertFalse(installer.obsolete_addon_dirs(value, home), value)

    def test_install_removes_only_the_old_addon_path_and_rollback_restores_it(self):
        env = self.home / installer.ADDON_ENV
        env.parent.mkdir(parents=True)
        cases = {
            # dev.59 的套件內容：整個檔案是舊版寫的，移除。
            "# 靈犀注音 Fcitx5 外殼路徑；重新登入後生效。\nFCITX_ADDON_DIRS=${HOME}/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5\n": None,
            # #92：使用者把系統目錄改成 Fedora 的，仍是外殼路徑，移除。
            "FCITX_ADDON_DIRS=${HOME}/.local/lib/fcitx5:/usr/lib64/fcitx5\n# fixed\n": None,
            # 同一檔另有使用者自己的設定：只拿掉舊路徑與它的註解。
            "# 讓 Fcitx 5 在使用者目錄找 Zuin 的 addon（其餘 addon 仍在系統目錄）。需重新登入才生效。\n"
            "FCITX_ADDON_DIRS=${HOME}/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5\nGTK_IM_MODULE=fcitx\n": "GTK_IM_MODULE=fcitx\n",
            # 使用者自訂的 addon 目錄：不是舊版寫的，不動。
            "FCITX_ADDON_DIRS=${HOME}/.local/lib/fcitx5:/opt/addons:/usr/lib64/fcitx5\n": "same",
        }
        for before, after in cases.items():
            with self.subTest(before=before):
                env.write_text(before)
                backup = installer.install(self.bundle, self.home, self.release)
                if after is None:
                    self.assertFalse(env.exists())
                else:
                    self.assertEqual(env.read_text(), before if after == "same" else after)
                installer.rollback(self.home, backup)
                self.assertEqual(env.read_text(), before)

    def test_rollback_to_a_release_that_needs_the_addon_path_restores_the_session(self):
        # dev.59：相對的 Library，靠 90-0c.conf 的 FCITX_ADDON_DIRS 找外殼。
        conf = self.home / ".local/share/fcitx5/addon/0c.conf"
        conf.parent.mkdir(parents=True)
        conf.write_text("[Addon]\nName=0c\nType=SharedLibrary\nLibrary=lib0c\n")
        env = self.home / installer.ADDON_ENV
        env.parent.mkdir(parents=True)
        env.write_text("# 靈犀注音 Fcitx5 外殼路徑；重新登入後生效。\n"
                       "FCITX_ADDON_DIRS=${HOME}/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5\n")
        calls, tools = [], {"dbus-update-activation-environment", "systemctl"}
        run = lambda args, **kwargs: calls.append(args) or subprocess.CompletedProcess(args, 0, "", "")
        for p in (patch.object(installer.Path, "home", return_value=self.home),
                  patch.object(installer.shutil, "which", side_effect=lambda name: "/usr/bin/" + name if name in tools else None)):
            p.start()
            self.addCleanup(p.stop)
        backup = installer.install(self.bundle, self.home, self.release)
        self.assertFalse(env.exists())
        self.assertIsNone(installer.restore_session_addon_dirs(self.home, run))  # 新版是絕對路徑，不必設定
        self.assertEqual(calls, [])
        # 安裝時已把 D-Bus 啟動環境改成只有系統目錄；回復後依放回的 90-0c.conf 設回，D-Bus 啟動的 Fcitx5 才找得到外殼。
        installer.rollback(self.home, backup)
        value = f"{self.home.resolve()}/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5"
        self.assertEqual(installer.restore_session_addon_dirs(self.home, run), value)
        self.assertEqual(calls, [["dbus-update-activation-environment", "--systemd", f"FCITX_ADDON_DIRS={value}"]])
        calls.clear()
        tools.discard("dbus-update-activation-environment")
        installer.restore_session_addon_dirs(self.home, run)
        self.assertEqual(calls, [["systemctl", "--user", "set-environment", f"FCITX_ADDON_DIRS={value}"]])
        # 安裝給別的家目錄時不碰目前的桌面。
        with patch.object(installer.Path, "home", return_value=self.root / "other"):
            self.assertIsNone(installer.restore_session_addon_dirs(self.home, lambda *a, **k: self.fail("touched session")))
        # 0.1.0-dev.15 以前：zuin.conf 與 90-zuin.conf。
        conf.unlink()
        env.unlink()
        (conf.parent / "zuin.conf").write_text("[Addon]\nLibrary=libzuin\n")
        (env.parent / "90-zuin.conf").write_text('FCITX_ADDON_DIRS="$HOME/.local/lib/fcitx5:/usr/lib/fcitx5"\n')
        self.assertEqual(installer.restored_addon_dirs(self.home.resolve()), f"{self.home.resolve()}/.local/lib/fcitx5:/usr/lib/fcitx5")
        (env.parent / "90-zuin.conf").write_text("FCITX_ADDON_DIRS=${XDG_DATA_HOME}/fcitx5:/usr/lib/fcitx5\n")
        self.assertIsNone(installer.restored_addon_dirs(self.home.resolve()))  # 展開不了，不設定錯的值

    def test_rollback_command_resets_the_session_addon_path(self):
        backup = installer.install(self.bundle, self.home, self.release)
        with patch.object(installer.sys, "argv", ["install.py", "rollback", "--backup", str(backup), "--home", str(self.home)]), \
                patch.object(installer.os, "getuid", return_value=1000), \
                patch.object(installer, "require_desktop_stopped"), \
                patch.object(installer, "restore_session_addon_dirs") as restored:
            installer.main()
        restored.assert_called_once_with(self.home)

    def dev59_addon_setup(self):
        """dev.59 的安裝：相對的 Library，靠 90-0c.conf 的 FCITX_ADDON_DIRS 找外殼。"""
        self.release["channel"] = "public-beta"
        self.save_release()
        conf = self.home / ".local/share/fcitx5/addon/0c.conf"
        conf.parent.mkdir(parents=True, exist_ok=True)
        conf.write_text("[Addon]\nName=0c\nType=SharedLibrary\nLibrary=lib0c\nOnDemand=False\n")
        env = self.home / installer.ADDON_ENV
        env.parent.mkdir(parents=True, exist_ok=True)
        env.write_text("# 靈犀注音 Fcitx5 外殼路徑；重新登入後生效。\n"
                       "FCITX_ADDON_DIRS=${HOME}/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5\n")
        return conf, env

    def test_only_a_fcitx_that_cannot_find_the_old_shell_counts_as_stuck(self):
        conf, env = self.dev59_addon_setup()
        home = self.home.resolve()
        library = home / ".local/lib/fcitx5/lib0c.so"
        found = f"FCITX_ADDON_DIRS={home}/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5\0".encode()
        stuck = self.fake_process("fcitx5", environ=b"DISPLAY=:0\0")  # #93：D-Bus 啟動，拿不到登入環境的變數
        self.assertTrue(installer.stuck_without_shell(home, [stuck[1]]))
        # 系統目錄是別的發行版的也一樣找不到外殼。
        other = self.fake_process("fcitx5", environ=b"FCITX_ADDON_DIRS=/usr/lib64/fcitx5\0")
        self.assertTrue(installer.stuck_without_shell(home, [other[1]]))
        for name, procs in {
            "沒有 Fcitx5 在執行": [],
            # 找得到外殼卻沒載入：例如使用者在 Fcitx5 停用了這個 addon，是使用者的選擇。
            "環境帶著外殼目錄": [self.fake_process("fcitx5", environ=found)[1]],
            "環境寫 $HOME": [self.fake_process("fcitx5", environ=b"FCITX_ADDON_DIRS=$HOME/.local/lib/fcitx5/\0")[1]],
            # #92：外殼有載入，zuin 一直留在清單裡。使用者自己拿掉靈犀注音時外殼也照樣載入（OnDemand=False）。
            "外殼已映射": [self.fake_process("fcitx5", maps=f"7f00-7f10 r-xp 00000000 08:02 1 {library}\n")[1]],
            "其中一個已映射": [stuck[1], self.fake_process("fcitx5", maps=f"7f00-7f10 r-xp 00000000 08:02 1 {library}\n")[1]],
        }.items():
            with self.subTest(name):
                self.assertFalse(installer.stuck_without_shell(home, procs))
        unreadable = self.fake_process("fcitx5")
        (unreadable[1] / "maps").unlink()
        self.assertFalse(installer.stuck_without_shell(home, [unreadable[1]]))  # 讀不到 maps：當成已映射
        # 不靠 FCITX_ADDON_DIRS 的安裝：新版的絕對 Library，或 90-0c.conf 沒有舊版寫的那行。
        conf.write_text(f"[Addon]\nName=0c\nLibrary={home}/.local/lib/fcitx5/lib0c\n")
        self.assertFalse(installer.stuck_without_shell(home, [stuck[1]]))
        self.dev59_addon_setup()
        env.write_text("FCITX_ADDON_DIRS=/opt/addons:/usr/lib64/fcitx5\n")
        self.assertFalse(installer.stuck_without_shell(home, [stuck[1]]))
        env.unlink()
        self.assertFalse(installer.stuck_without_shell(home, [stuck[1]]))
        self.dev59_addon_setup()
        conf.unlink()
        self.assertFalse(installer.stuck_without_shell(home, [stuck[1]]))

    def test_stuck_install_leaves_a_one_time_todo_to_restore_the_input_method(self):
        _, env = self.dev59_addon_setup()
        stuck = self.fake_process("fcitx5", environ=b"DISPLAY=:0\0")
        marker = self.home / installer.ZUIN_RESTORE
        processes = {"fcitx5": [stuck]}

        def main(*argv, **environ):
            with patch.object(installer.sys, "argv", ["install.py", *argv, "--bundle", str(self.bundle), "--home", str(self.home)]), \
                    patch.object(installer.os, "getuid", return_value=1000), \
                    patch.dict(installer.os.environ, {"ZC_MANAGED_RELOAD": "1", **environ}), \
                    patch.object(installer.Path, "home", return_value=self.home), \
                    patch.object(installer, "user_processes", side_effect=lambda names: [p for n in names for p in processes.get(n, [])]), \
                    patch.object(installer, "runtime_check"), patch.object(installer, "require_desktop_stopped"), \
                    patch.object(installer, "clear_session_addon_dirs"), patch.object(installer, "restore_session_addon_dirs"), patch.object(installer, "settings_dependencies"):
                installer.main()

        # 0c-update 的 installUnusedShell（dev.60 更新器）以 ZC_SHELL_UNUSED=1 執行新套件的 install.py。
        main("install", ZC_SHELL_UNUSED="1")
        self.assertFalse(env.exists())  # 舊設定照常移除；判斷在移除之前
        todo = json.loads(marker.read_text())
        self.assertEqual((todo["schema"], todo["version"]), (1, self.release["version"]))
        # 已換成絕對路徑：之後的安裝不再判定卡住，也不清掉還沒完成的待辦（可能還沒重新登入）。
        main("install", ZC_SHELL_UNUSED="1")
        self.assertTrue(marker.exists())
        # 回復、移除後待辦作廢，免得日後把使用者自己拿掉的靈犀注音加回去。
        backup = Path(json.loads((self.home / installer.RECEIPT).read_text())["backup"])
        main("rollback", "--backup", str(backup))
        self.assertFalse(marker.exists())
        marker.parent.mkdir(parents=True, exist_ok=True)
        marker.write_text("{}")
        main("uninstall")
        self.assertFalse(marker.exists())

    def test_install_outside_the_stuck_state_leaves_no_restore_todo(self):
        marker = self.home / installer.ZUIN_RESTORE
        home = self.home.resolve()
        found = f"FCITX_ADDON_DIRS={home}/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5\0".encode()
        for name, (environ, process) in {
            "一行安裝自己設定輸入法清單": ({"ZC_INPUT_SETUP": "1"}, b"DISPLAY=:0\0"),
            "Fcitx5 找得到外殼": ({"ZC_SHELL_UNUSED": "1"}, found),
        }.items():
            with self.subTest(name):
                self.dev59_addon_setup()
                proc = self.fake_process("fcitx5", environ=process)
                with patch.object(installer.sys, "argv", ["install.py", "install", "--bundle", str(self.bundle), "--home", str(self.home)]), \
                        patch.object(installer.os, "getuid", return_value=1000), \
                        patch.dict(installer.os.environ, {"ZC_MANAGED_RELOAD": "1", **environ}), \
                        patch.object(installer.Path, "home", return_value=self.home), \
                        patch.object(installer, "user_processes", return_value=[proc]), \
                        patch.object(installer, "runtime_check"), patch.object(installer, "require_desktop_stopped"), \
                        patch.object(installer, "clear_session_addon_dirs"), patch.object(installer, "settings_dependencies"):
                    installer.main()
                self.assertFalse(marker.exists())
        # 安裝給別的家目錄（--home）時不看目前桌面的 Fcitx5。
        self.dev59_addon_setup()
        with patch.object(installer.Path, "home", return_value=self.root / "other"), \
                patch.object(installer, "user_processes", side_effect=AssertionError("looked at the desktop")):
            self.assertFalse(installer.zuin_restore_needed(self.home))

    def test_uninstall_removes_the_environment_file_an_old_release_installed(self):
        installer.install(self.bundle, self.home, self.release)
        env = self.home / installer.ADDON_ENV
        env.parent.mkdir(parents=True)
        env.write_text("FCITX_ADDON_DIRS=${HOME}/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5\n")
        receipt = json.loads((self.home / installer.RECEIPT).read_text())
        receipt["files"][installer.ADDON_ENV] = installer.digest(env)  # dev.59 的收據
        (self.home / installer.RECEIPT).write_text(json.dumps(receipt))
        installer.uninstall(self.home)
        self.assertFalse(env.exists())

    def fake_process(self, name, environ=b"", maps=""):
        proc = self.root / "proc" / str(len(list((self.root / "proc").glob("*"))) if (self.root / "proc").exists() else 0)
        proc.mkdir(parents=True)
        (proc / "environ").write_bytes(environ)
        (proc / "maps").write_text(maps)
        return name, proc

    def test_blocked_reinstall_says_how_to_stop_instead_of_pointing_to_finish(self):
        with patch.object(installer, "user_processes", return_value=[("0c-server", Path("/proc/2")), ("fcitx5", Path("/proc/1"))]):
            with self.assertRaises(ValueError) as caught:
                installer.require_desktop_stopped()
        message = str(caught.exception)
        self.assertIn("pkill -x fcitx5; pkill -x 0c-server", message)  # 先停框架，引擎才不會被重新啟動
        self.assertIn("登出再登入", message)
        self.assertNotIn("0c-update finish", message)
        with patch.object(installer, "user_processes", return_value=[("0c-ibus", Path("/proc/3"))]):
            with self.assertRaisesRegex(ValueError, "切回「靈犀注音」") as caught:
                installer.require_desktop_stopped()
        self.assertNotIn("登出", str(caught.exception))
        with patch.object(installer, "user_processes", return_value=[]):
            installer.require_desktop_stopped()

    def test_reinstall_proceeds_when_running_fcitx_does_not_use_the_shell(self):
        library = self.home.resolve() / ".local/lib/fcitx5/lib0c.so"
        unused = self.fake_process("fcitx5")  # #93：D-Bus 啟動的 Fcitx5 沒載入外殼
        loaded = self.fake_process("fcitx5", maps=f"7f00-7f10 r-xp 00000000 08:02 1 {library}\n")
        owner = {"value": "b true"}
        run = lambda args, **kwargs: subprocess.CompletedProcess(args, 0, owner["value"], "")
        which = patch.object(installer.shutil, "which", side_effect=lambda name: "/usr/bin/" + name)
        with which:
            with patch.object(installer, "user_processes", return_value=[unused]):
                installer.require_desktop_stopped(self.home, run)
            with patch.object(installer, "user_processes", return_value=[unused, ("0c-server", Path("/proc/9"))]):
                with self.assertRaisesRegex(ValueError, "pkill -x 0c-server 結束") as caught:
                    installer.require_desktop_stopped(self.home, run)
            self.assertNotIn("fcitx5", str(caught.exception))
            with patch.object(installer, "user_processes", return_value=[loaded]):
                with self.assertRaisesRegex(ValueError, "pkill -x fcitx5"):
                    installer.require_desktop_stopped(self.home, run)
                # #92：外殼載入了，但 Fcitx5 沒有自己的 D-Bus 名稱（系統 addon 載不到），沒人能打字。
                owner["value"] = "b false"
                installer.require_desktop_stopped(self.home, run)
                owner["value"] = ""  # 查不出來：當成使用中
                with self.assertRaises(ValueError):
                    installer.require_desktop_stopped(self.home, run)
                with patch.dict(installer.os.environ, {"ZC_SHELL_UNUSED": "1"}):  # 0c-update 已確認
                    installer.require_desktop_stopped(self.home, run)
                with self.assertRaises(ValueError):  # 解除安裝、回復不放寬
                    installer.require_desktop_stopped()

    def test_replaced_legacy_or_unreadable_shell_still_counts_as_in_use(self):
        # 上次更新換了 lib0c.so 但 Fcitx5 沒重新啟動：使用者還在用舊外殼打字，重跑安裝不能跳過組字保護。
        core = "7f00-7f10 r-xp 00000000 08:01 7 /usr/lib64/libFcitx5Core.so.7\n"
        home = self.home.resolve()
        cases = {
            "目前路徑": (f"7f20-7f30 r-xp 00000000 08:02 1234 {home}/.local/lib/fcitx5/lib0c.so\n", True),
            "已被取代": (f"7f20-7f30 r-xp 00000000 08:02 1234 {home}/.local/lib/fcitx5/lib0c.so (deleted)\n", True),
            "別的 inode": ("7f20-7f30 r-xp 00000000 08:02 99 /var/home/u/.local/lib/fcitx5/lib0c.so\n", True),
            "dev.15 以前的 libzuin.so": (f"7f20-7f30 r-xp 00000000 08:02 5 {home}/.local/lib/fcitx5/libzuin.so (deleted)\n", True),
            "家目錄含空白": ("7f20-7f30 r-xp 00000000 08:02 5 /home/u v/.local/lib/fcitx5/lib0c.so\n", True),
            "沒有外殼（#93）": ("7f20-7f30 r-xp 00000000 08:02 6 /usr/lib64/fcitx5/libdbus.so\n", False),
            "名字相近": ("7f20-7f30 r-xp 00000000 08:02 6 /opt/x/lib0c.so.bak\n", False),
        }
        run = lambda args, **kwargs: subprocess.CompletedProcess(args, 0, "b true", "")
        with patch.object(installer.shutil, "which", side_effect=lambda name: "/usr/bin/" + name):
            for name, (maps, mapped) in cases.items():
                with self.subTest(name):
                    proc = self.fake_process("fcitx5", maps=core + maps)
                    self.assertEqual(installer.maps_shell(proc[1]), mapped)
                    with patch.object(installer, "user_processes", return_value=[proc]):
                        if mapped:
                            with self.assertRaisesRegex(ValueError, "pkill -x fcitx5"):
                                installer.require_desktop_stopped(self.home, run)
                        else:
                            installer.require_desktop_stopped(self.home, run)
            unreadable = self.fake_process("fcitx5")
            (unreadable[1] / "maps").unlink()  # 讀不到 maps：當成使用中
            self.assertTrue(installer.maps_shell(unreadable[1]))
            with patch.object(installer, "user_processes", return_value=[unreadable]):
                with self.assertRaisesRegex(ValueError, "pkill -x fcitx5"):
                    installer.require_desktop_stopped(self.home, run)

    def test_system_addon_dir_follows_the_linked_fcitx_library(self):
        lib = self.root / "usr/lib64"
        (lib / "fcitx5").mkdir(parents=True)
        (lib / "libFcitx5Core.so.5.1.23").write_text("core")
        (lib / "libFcitx5Core.so.7").symlink_to("libFcitx5Core.so.5.1.23")
        out = f"\tlibFcitx5Utils.so.2 => {lib}/libFcitx5Utils.so.2 (0x1)\n\tlibFcitx5Core.so.7 => {lib}/libFcitx5Core.so.7 (0x2)\n"
        run = lambda *a, **k: subprocess.CompletedProcess(a[0], 0, out, "")
        with patch.object(installer.shutil, "which", return_value="/usr/bin/fcitx5"):
            self.assertIsNone(installer.system_addon_dir(run))  # 目錄裡沒有 addon
            (lib / "fcitx5/libdbus.so").write_text("addon")
            self.assertEqual(installer.system_addon_dir(run), lib / "fcitx5")
        with patch.object(installer.shutil, "which", return_value=None):
            self.assertIsNone(installer.system_addon_dir(run))

    def test_session_drops_the_old_addon_path(self):
        calls, systemd = [], {"FCITX_ADDON_DIRS": str(self.home.resolve()) + "/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5"}
        def run(args, **kwargs):
            calls.append(args)
            if args[:3] == ["systemctl", "--user", "show-environment"]:
                return subprocess.CompletedProcess(args, 0, "".join(f"{k}={v}\n" for k, v in systemd.items()), "")
            if args[:3] == ["systemctl", "--user", "unset-environment"]:
                systemd.pop(args[3], None)
            if args[0] == "dbus-update-activation-environment":
                systemd["FCITX_ADDON_DIRS"] = args[1].partition("=")[2]  # dbus-broker 轉給 systemd
            return subprocess.CompletedProcess(args, 0, "", "")
        patches = (patch.object(installer.Path, "home", return_value=self.home),
                   patch.object(installer, "user_processes", return_value=[]),
                   patch.object(installer, "system_addon_dir", return_value=Path("/usr/lib64/fcitx5")),
                   patch.object(installer.shutil, "which", return_value="/usr/bin/dbus-update-activation-environment"))
        for p in patches:
            p.start()
            self.addCleanup(p.stop)
        self.assertTrue(installer.clear_session_addon_dirs(self.home, run=run))
        # dbus-daemon 的啟動環境刪不掉變數：改成這台電腦的系統目錄，與沒設定時相同；systemd 那邊拿掉。
        self.assertIn(["dbus-update-activation-environment", "FCITX_ADDON_DIRS=/usr/lib64/fcitx5"], calls)
        self.assertEqual(calls[-1], ["systemctl", "--user", "unset-environment", "FCITX_ADDON_DIRS"])
        self.assertNotIn("FCITX_ADDON_DIRS", systemd)
        # 管理員沒帶舊值，但這次移除了舊的 90-0c.conf：D-Bus 可能載入過它，同樣更正。
        calls.clear()
        self.assertTrue(installer.clear_session_addon_dirs(self.home, stale=True, run=run))
        self.assertIn(["dbus-update-activation-environment", "FCITX_ADDON_DIRS=/usr/lib64/fcitx5"], calls)
        self.assertNotIn("FCITX_ADDON_DIRS", systemd)
        # 使用者自己的路徑不動。
        calls.clear()
        systemd["FCITX_ADDON_DIRS"] = "/opt/mine:/usr/lib64/fcitx5"
        self.assertFalse(installer.clear_session_addon_dirs(self.home, run=run))
        self.assertEqual(calls, [["systemctl", "--user", "show-environment"]])
        # 安裝給別的家目錄（測試、--home）時不碰目前的桌面。
        with patch.object(installer.Path, "home", return_value=self.root / "other"):
            self.assertFalse(installer.clear_session_addon_dirs(self.home, True, lambda *a, **k: self.fail("touched session")))

    def test_running_fcitx_is_asked_to_load_the_new_addon(self):
        library = self.home.resolve() / ".local/lib/fcitx5/lib0c.so"
        mapped = f"7f00-7f10 r-xp 00000000 08:02 1234 {library}\n"
        shown = []
        proc = self.fake_process("fcitx5")
        refreshed = []
        def run(args, **kwargs):
            refreshed.append(args)
            (proc[1] / "maps").write_text(mapped)  # Refresh 之後 Fcitx5 載入了 addon
            return subprocess.CompletedProcess(args, 0, "", "")
        with patch.object(installer, "user_processes", return_value=[proc]), \
             patch.object(installer.shutil, "which", side_effect=lambda name: "/usr/bin/" + name):
            self.assertTrue(installer.report_fcitx_addon(self.home, run, wait=1, show=shown.append))
            self.assertEqual(refreshed[0][:3], ["busctl", "--user", "--auto-start=no"])
            self.assertIn("不必重新登入", shown[-1])
            refreshed.clear()
            self.assertTrue(installer.report_fcitx_addon(self.home, run, wait=1, show=shown.append))
            self.assertEqual(refreshed, [])  # 已載入就不打擾
        with patch.object(installer, "user_processes", return_value=[]):
            self.assertIsNone(installer.report_fcitx_addon(self.home, run, show=shown.append))

    def test_refresh_never_starts_another_fcitx_over_dbus(self):
        calls = []
        def run(args, **kwargs):
            calls.append(args)
            owned = "boolean false" if self.owner is False else "boolean true"
            return subprocess.CompletedProcess(args, 0, owned if "org.freedesktop.DBus.NameHasOwner" in args else "", "")
        # 沒有 busctl（例如精簡的 Ubuntu 容器）時改用 dbus-send，名稱沒有人持有就不呼叫，免得 D-Bus 另外啟動 Fcitx5。
        with patch.object(installer.shutil, "which", side_effect=lambda name: None if name == "busctl" else "/usr/bin/" + name):
            self.owner = False
            self.assertFalse(installer.refresh_fcitx(run))
            self.assertFalse(any("org.fcitx.Fcitx.Controller1.Refresh" in args for args in calls))
            self.owner = True
            self.assertTrue(installer.refresh_fcitx(run))
            self.assertIn("org.fcitx.Fcitx.Controller1.Refresh", calls[-1])

    def test_fcitx_that_cannot_load_the_addon_gets_restart_hint(self):
        shown = []
        # #92：Fcitx5 以只含 Debian 路徑的 FCITX_ADDON_DIRS 啟動，連 D-Bus addon 都沒載入，Refresh 也失敗。
        proc = self.fake_process("fcitx5", b"FCITX_ADDON_DIRS=/h/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5\0")
        fail = lambda args, **kwargs: subprocess.CompletedProcess(args, 1, "", "")
        with patch.object(installer, "user_processes", return_value=[proc]), \
             patch.object(installer, "system_addon_dir", return_value=Path("/usr/lib64/fcitx5")), \
             patch.object(installer.shutil, "which", side_effect=lambda name: "/usr/bin/" + name):
            self.assertFalse(installer.report_fcitx_addon(self.home, fail, wait=0.2, show=shown.append))
        self.assertIn("請登出再登入", shown[0])
        self.assertIn("/usr/lib64/fcitx5", shown[1])

    def add_settings_program(self, content="settings program"):
        path = self.bundle / "bin/0c-settings"
        path.write_text(content)
        self.release["files"]["bin/0c-settings"] = installer.digest(path)
        self.save_release()

    def drop_settings_program(self):
        (self.bundle / "bin/0c-settings").unlink()
        del self.release["files"]["bin/0c-settings"]
        self.save_release()

    def test_packaging_checks_the_prebuilt_settings_program(self):
        def elf(machine):
            return b"\x7fELF" + bytes(14) + machine.to_bytes(2, "little") + bytes(40)
        def prebuilt(body, notices="Tauri 授權文字"):
            directory = self.root / f"prebuilt-{len(list(self.root.glob('prebuilt-*')))}"
            directory.mkdir()
            (directory / "0c-settings").write_bytes(body)
            (directory / "NOTICES.txt").write_text(notices)
            return directory
        stage = self.root / "stage"
        text = packager.add_settings(stage, prebuilt(elf(62)), machine="x86_64")
        self.assertEqual(text, "Tauri 授權文字")
        target = stage / "bin/0c-settings"
        self.assertEqual(target.read_bytes(), elf(62))
        self.assertTrue(os.access(target, os.X_OK))
        with self.assertRaisesRegex(ValueError, "x86_64"):
            packager.add_settings(self.root / "wrong-arch", prebuilt(elf(183)), machine="x86_64")
        packager.add_settings(self.root / "arm", prebuilt(elf(183)), machine="aarch64")
        with self.assertRaisesRegex(ValueError, "Linux 執行檔"):
            packager.add_settings(self.root / "not-elf", prebuilt(b"#!/bin/sh\n" + bytes(30)), machine="x86_64")
        with self.assertRaisesRegex(ValueError, "授權聲明"):
            packager.add_settings(self.root / "no-notices", prebuilt(elf(62), notices="  \n"), machine="x86_64")
        with self.assertRaisesRegex(ValueError, "一般檔案"):
            packager.add_settings(self.root / "missing", self.root / "does-not-exist", machine="x86_64")

    def test_windows_packaging_checks_the_prebuilt_settings_program(self):
        def pe(machine=b"\x64\x86"):
            return b"MZ" + bytes(0x3a) + (0x40).to_bytes(4, "little") + b"PE\0\0" + machine + bytes(40)

        def prebuilt(name, body=None, notices="Tauri 授權文字"):
            directory = self.root / name
            directory.mkdir()
            (directory / "0c-settings.exe").write_bytes(pe() if body is None else body)
            (directory / "NOTICES.txt").write_text(notices)
            return directory
        program, notices = package_windows.check_settings(prebuilt("win-ok"))
        self.assertEqual((program.name, notices), ("0c-settings.exe", "Tauri 授權文字"))
        with self.assertRaisesRegex(ValueError, "x64"):
            package_windows.check_settings(prebuilt("win-arm", pe(b"\x64\xaa")))
        with self.assertRaisesRegex(ValueError, "PE"):
            package_windows.check_settings(prebuilt("win-elf", b"\x7fELF" + bytes(80)))
        with self.assertRaisesRegex(ValueError, "授權聲明"):
            package_windows.check_settings(prebuilt("win-notices", notices=" \n"))
        with self.assertRaisesRegex(ValueError, "一般檔案"):
            package_windows.check_settings(self.root / "win-missing")
        self.assertIn("0c-settings.exe", package_windows.DESCRIPTIONS)
        info = package_windows.version_info("0c-settings.exe", "0.1.0-dev.43", False)
        self.assertIn("靈犀注音設定程式".encode("utf-16-le"), info)
        self.assertIn("0c-settings".encode("utf-16-le"), info)
        # 0c-mcp（#126）同樣寫入版本資訊並簽章；說明出現在檔案內容與 SmartScreen 報告。
        info = package_windows.version_info("0c-mcp.exe", "0.2.0-dev.1", False)
        self.assertIn(package_windows.DESCRIPTIONS["0c-mcp.exe"].encode("utf-16-le"), info)

    def test_macos_packaging_checks_the_prebuilt_settings_app(self):
        arm64 = package_macos.MACHO_ARM64 + bytes(40)

        def prebuilt(name, body=arm64, notices="Tauri 授權文字", plist="<plist/>"):
            directory = self.root / name
            app = directory / "0c-settings.app/Contents"
            (app / "MacOS").mkdir(parents=True)
            (app / "Info.plist").write_text(plist)
            (app / "MacOS/0c-settings").write_bytes(body)
            (directory / "NOTICES.txt").write_text(notices)
            return directory
        app, notices = package_macos.check_settings(prebuilt("mac-ok"))
        self.assertEqual((app.name, notices), ("0c-settings.app", "Tauri 授權文字"))
        x86_64 = bytes.fromhex("cffaedfe") + bytes.fromhex("07000001") + bytes(40)
        with self.assertRaisesRegex(ValueError, "arm64"):
            package_macos.check_settings(prebuilt("mac-x86", x86_64))
        with self.assertRaisesRegex(ValueError, "arm64"):
            package_macos.check_settings(prebuilt("mac-script", b"#!/bin/sh\n" + bytes(30)))
        with self.assertRaisesRegex(ValueError, "授權聲明"):
            package_macos.check_settings(prebuilt("mac-notices", notices=" \n"))
        with self.assertRaisesRegex(ValueError, "Info.plist"):
            package_macos.check_settings(prebuilt("mac-plist", plist=""))
        linked = prebuilt("mac-link")
        (linked / "0c-settings.app/Contents/Resources").symlink_to("/etc")
        with self.assertRaisesRegex(ValueError, "符號連結"):
            package_macos.check_settings(linked)
        with self.assertRaisesRegex(ValueError, "找不到設定程式"):
            package_macos.check_settings(self.root / "does-not-exist")

    def test_macos_settings_app_metadata_matches_what_the_packager_and_updater_expect(self):
        plist = build_settings_macos.info_plist("0.1.0-dev.43")
        self.assertEqual(plist["CFBundleExecutable"], package_macos.SETTINGS_EXECUTABLE)
        self.assertEqual(build_settings_macos.APP_NAME, package_macos.SETTINGS_BUNDLE)
        self.assertEqual(plist["CFBundleShortVersionString"], "0.1.0")
        self.assertEqual(plist["CFBundleVersion"], "100.1.3043000")
        self.assertEqual(plist["CFBundleIdentifier"], "com.biggo.0c.settings")
        self.assertTrue(plist["LSMultipleInstancesProhibited"])
        self.assertNotIn("LSUIElement", plist)  # 設定程式是一般視窗程式，要出現在 Dock
        with self.assertRaises(ValueError):
            build_settings_macos.info_plist("dev")

    def test_macos_pkg_refuses_downgrade_before_installing(self):
        # 已安裝較新的版本時，安裝程式在開始前就說明原因（postinstall 失敗只會顯示通用的「安裝失敗」）。
        # 比的是 CFBundleVersion，跟 App 的 Info.plist 用同一個公式。
        import xml.dom.minidom
        text = package_macos_pkg.distribution("0.2.0")
        xml.dom.minidom.parseString(text.encode("utf-8"))
        self.assertIn('<installation-check script="checkInstalledVersion()"/>', text)
        self.assertIn(f'"{macos_version.bundle_version("0.2.0")}"', text)
        self.assertIn("/Library/Input Methods/0c.app", text)
        self.assertIn("這個安裝檔是 0.2.0", text)
        self.assertIn('\\"$HOME/Library/Application Support/0c/bin/0c-update\\" rollback', text)
        self.assertIn("<title>靈犀注音 0.2.0</title>", text)

    def test_macos_pkg_pages_do_not_ask_upgraders_to_log_out(self):
        # #170：安裝時已結束舊的輸入法程式，升級不必登出；第一次安裝找不到輸入來源時才請使用者重新登入。
        self.assertIn("不必登出", package_macos_pkg.WELCOME)
        self.assertIn("從舊版升級時不必登出", package_macos_pkg.CONCLUSION)
        self.assertNotIn("登出再登入後全面生效", package_macos_pkg.CONCLUSION)
        self.assertIn("登出目前帳號再重新登入", package_macos_pkg.CONCLUSION)

    def test_macos_bundle_version_increases_with_every_release(self):
        # 舊版一律寫 1；新版號必須都比 1 新，且依 semver 順序遞增，LaunchServices 才會選最新的 App（#77）。
        order = ["0.1.0-alpha.1", "0.1.0-beta.2", "0.1.0-dev.58", "0.1.0-dev.58.1", "0.1.0-dev.59", "0.1.0-rc.1",
                 "0.1.0", "0.1.1-dev.1", "0.2.0-dev.1", "1.0.0"]
        key = lambda v: tuple(int(x) for x in macos_version.bundle_version(v).split("."))
        self.assertEqual([key(v) for v in order], sorted(key(v) for v in order))
        self.assertGreater(key("0.1.0-dev.59"), (1,))
        self.assertEqual(macos_version.bundle_version("0.1.0-dev.59"), "100.1.3059000")
        self.assertEqual(macos_version.short_version("0.1.0-dev.59"), "0.1.0")
        for bad in ["dev", "0.1.0-foo.1", "0.1.0-dev.1000", "0.1.0-dev"]:
            with self.assertRaises(ValueError):
                macos_version.bundle_version(bad)

    def test_macos_signing_seals_nested_apps_before_the_outer_app(self):
        outer = self.root / "0c.app"
        files = ["Contents/MacOS/0c", "Contents/MacOS/0c-update", "Contents/MacOS/0c-mcp", "Contents/Resources/data/lexicon.bin",
                 "Contents/Helpers/0c-settings.app/Contents/MacOS/0c-settings",
                 "Contents/Helpers/0c-settings.app/Contents/Info.plist",
                 package_macos.SETTINGS_LAUNCHER + "/Contents/MacOS/0c-settings-launcher",
                 package_macos.SETTINGS_LAUNCHER + "/Contents/Info.plist"]
        for name in files:
            path = outer / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b"x")
        nested = outer / "Contents/Helpers/0c-settings.app"
        launcher = outer / package_macos.SETTINGS_LAUNCHER
        machos = {outer / "Contents/MacOS/0c", outer / "Contents/MacOS/0c-update", outer / "Contents/MacOS/0c-mcp", nested / "Contents/MacOS/0c-settings",
                  launcher / "Contents/MacOS/0c-settings-launcher"}
        order = package_macos.signing_order(outer, lambda p: p in machos)
        self.assertEqual(order[-1], outer)
        self.assertEqual(set(order[-3:-1]), {nested, launcher})
        self.assertEqual(set(order[:-3]), machos)
        for app, exe in ((nested, "0c-settings"), (launcher, "0c-settings-launcher")):
            self.assertLess(order.index(app / "Contents/MacOS" / exe), order.index(app))
        self.assertNotIn(outer / "Contents/Resources/data/lexicon.bin", order)
        single = self.root / "0c-update"
        single.write_bytes(b"x")
        self.assertEqual(package_macos.signing_order(single, lambda p: True), [single])

    def test_macos_launcher_has_independent_identity_and_searchable_names(self):
        self.assertEqual(package_macos.SETTINGS_LAUNCHER, "Contents/Helpers/0c-settings-launcher.app", "must use a standard nested-code location so stapling does not break the outer seal")
        info = plistlib.loads((packager.ROOT / "platforms/macos/SettingsLauncher/Info.plist").read_bytes())
        self.assertEqual(info["CFBundleIdentifier"], "com.biggo.0c.settings-launcher")
        self.assertNotEqual(info["CFBundleIdentifier"], build_settings_macos.info_plist("0.1.0-dev.55")["CFBundleIdentifier"])
        self.assertEqual(info["CFBundleExecutable"], "0c-settings-launcher")
        self.assertIn("靈犀注音", info["CFBundleDisplayName"])
        self.assertIn("0c", info["CFBundleDisplayName"])
        self.assertTrue(info["LSUIElement"])
        self.assertTrue(info["0cManagedSettingsLauncher"])
        self.assertEqual(info["LSMinimumSystemVersion"], "13.0")

    def test_macos_notarization_staples_and_assesses_the_launcher_and_outer_app(self):
        stage, temp = self.root / "stage", self.root / "notary"
        (stage / "0c.app" / package_macos.SETTINGS_LAUNCHER).mkdir(parents=True)
        def run(args, **kwargs):
            if "submit" in args:
                return subprocess.CompletedProcess(args, 0, '{"id":"test-ticket","status":"Accepted"}', "")
            return subprocess.CompletedProcess(args, 0, "", "source=Notarized Developer ID")
        with patch.object(package_macos.subprocess, "run", side_effect=run) as mocked:
            package_macos.notarize(stage, temp, "test-profile")
        calls = [c.args[0] for c in mocked.call_args_list]
        for app in (stage / "0c.app" / package_macos.SETTINGS_LAUNCHER, stage / "0c.app"):
            for action in ("staple", "validate"):
                self.assertIn(["xcrun", "stapler", action, str(app)], calls)
            self.assertIn(["spctl", "--assess", "--type", "execute", "-vv", str(app)], calls)
        self.assertIn(["codesign", "--verify", "--deep", "--strict", str(stage / "0c.app")], calls)

    def test_optional_settings_program_installs_updates_and_uninstalls(self):
        program = self.home / ".local/bin/0c-settings"
        installer.install(self.bundle, self.home, self.release)
        self.assertFalse(program.exists(), "套件沒帶設定程式就不裝")
        self.add_settings_program()
        installer.install(self.bundle, self.home, self.release)
        self.assertEqual(program.read_text(), "settings program")
        receipt = json.loads((self.home / installer.RECEIPT).read_text())
        self.assertEqual(receipt["files"][".local/bin/0c-settings"], installer.digest(program), "收據要記錄雜湊，啟動前才能核對")
        self.add_settings_program("newer settings program")
        installer.install(self.bundle, self.home, self.release)
        self.assertEqual(program.read_text(), "newer settings program")
        entry = self.home / installer.DESKTOP_ENTRY
        entry.parent.mkdir(parents=True)
        entry.write_text("[Desktop Entry]\n" + installer.DESKTOP_MARKER + "\n")
        icon = self.home / installer.DESKTOP_ICON
        icon.parent.mkdir(parents=True)
        icon.write_bytes(b"png")
        installer.uninstall(self.home)
        self.assertFalse(program.exists())
        self.assertFalse(entry.exists(), "解除安裝要一併移除 0c-update 建立的選單項目")
        self.assertFalse(icon.exists(), "以及它一起寫入的圖示")

    def add_ibus_shell(self):
        path = self.bundle / "bin/0c-ibus"
        path.write_text("ibus shell")
        self.release["files"]["bin/0c-ibus"] = installer.digest(path)
        self.save_release()

    def register_ibus(self, name=installer.IBUS_COMPONENT_NAME):
        component = self.home / installer.IBUS_COMPONENT
        component.parent.mkdir(parents=True, exist_ok=True)
        component.write_text(f"<component>{name}</component>")
        for relative in (*installer.IBUS_ENV, installer.IBUS_CACHE):
            (self.home / relative).parent.mkdir(parents=True, exist_ok=True)
            (self.home / relative).write_text("x")
        return component

    def test_ibus_shell_is_optional_and_uninstall_removes_its_registration(self):
        program = self.home / installer.OPTIONAL["bin/0c-ibus"]
        self.add_ibus_shell()
        installer.install(self.bundle, self.home, self.release)
        self.assertEqual(program.read_text(), "ibus shell")
        receipt = json.loads((self.home / installer.RECEIPT).read_text())
        self.assertIn(".local/bin/0c-ibus", receipt["files"])
        component = self.register_ibus()
        installer.uninstall(self.home)
        self.assertFalse(program.exists())
        for relative in (installer.IBUS_COMPONENT, *installer.IBUS_ENV, installer.IBUS_CACHE):
            self.assertFalse((self.home / relative).exists(), f"解除安裝要撤銷 IBus 登錄：{relative}")
        # 不是靈犀注音的元件檔不動。
        foreign = self.register_ibus("<name>org.example.Other</name>")
        installer.uninstall(self.home)
        self.assertTrue(foreign.exists())
        self.assertFalse(component.exists() and installer.IBUS_COMPONENT_NAME in component.read_text())

    def test_rollback_to_a_package_without_the_ibus_shell_drops_the_registration(self):
        installer.install(self.bundle, self.home, self.release)
        self.add_ibus_shell()
        with_ibus = installer.install(self.bundle, self.home, self.release)
        self.register_ibus()
        second = installer.install(self.bundle, self.home, self.release)
        installer.rollback(self.home, second)
        self.assertTrue((self.home / installer.IBUS_COMPONENT).exists(), "外殼還在就保留登錄")
        installer.rollback(self.home, with_ibus)
        self.assertFalse((self.home / installer.OPTIONAL["bin/0c-ibus"]).exists())
        self.assertFalse((self.home / installer.IBUS_COMPONENT).exists(), "外殼不在了，IBus 不能再列出啟動不了的輸入法")
        self.assertFalse((self.home / installer.IBUS_ENV[0]).exists())

    def test_runtime_check_accepts_ibus_without_fcitx(self):
        self.release["machine"] = installer.platform.machine()
        self.add_ibus_shell()
        ran = []
        def which(name):
            return {"ibus-daemon": "/usr/bin/ibus-daemon"}.get(name)
        def run(args, **kwargs):
            ran.append(args)
            return subprocess.CompletedProcess(args, 0, "", "")
        with patch.object(installer.shutil, "which", which), patch.object(installer.subprocess, "run", run):
            installer.runtime_check(self.bundle, self.release)
        self.assertIn([str(self.bundle / "bin/0c-ibus"), "-version"], ran)
        self.assertFalse(any(args[0] == "ldd" for args in ran), "沒有 Fcitx5 時不檢查它的外殼函式庫")
        with patch.object(installer.shutil, "which", lambda name: None), patch.object(installer.subprocess, "run", run):
            with self.assertRaisesRegex(ValueError, "IBus"):
                installer.runtime_check(self.bundle, self.release)
        (self.bundle / "bin/0c-ibus").unlink()
        with patch.object(installer.shutil, "which", which), patch.object(installer.subprocess, "run", run):
            with self.assertRaisesRegex(ValueError, "Fcitx5"):
                installer.runtime_check(self.bundle, self.release)

    def test_runtime_check_rejects_shell_needing_newer_fcitx(self):
        # Ubuntu 24.04 的 Fcitx5 5.1.7：函式庫都找得到，只有 ldd -r 才看得出缺 5.1.9 的符號。
        self.release["machine"] = installer.platform.machine()
        ran = []
        def run(args, **kwargs):
            ran.append(args)
            out = ("\tlibFcitx5Core.so.7 => /usr/lib/x86_64-linux-gnu/libFcitx5Core.so.7\n"
                   "undefined symbol: _ZN5fcitx8Instance15eventDispatcherEv\t(lib/lib0c.so)\n") if args[0] == "ldd" else ""
            return subprocess.CompletedProcess(args, 0, out, "")
        with patch.object(installer.shutil, "which", lambda name: "/usr/bin/fcitx5" if name == "fcitx5" else None), \
                patch.object(installer.subprocess, "run", run):
            with self.assertRaisesRegex(ValueError, "Fcitx5 版本不相容(.|\n)*eventDispatcher"):
                installer.runtime_check(self.bundle, self.release)
        self.assertIn(["ldd", "-r", str(self.bundle / "lib/lib0c.so")], ran)

    def test_ibus_unregister_only_clears_the_toolkit_settings_it_made(self):
        gtk, qt = installer.IBUS_TOOLKIT_ENV
        user = self.home / ".config/environment.d/50-im.conf"
        cases = (("我們的設定", {gtk: "GTK_IM_MODULE=ibus\n", qt: "# x\nQT_IM_MODULES=ibus;wayland\n"}, None, {"GTK_IM_MODULE", "QT_IM_MODULES"}),
                 ("只有停用記號", {gtk: "# 已停用\n", qt: "# 已停用\n"}, None, set()),
                 ("沒有檔案", {}, None, set()),
                 ("使用者也指定了 Qt", {gtk: "GTK_IM_MODULE=ibus\n", qt: "QT_IM_MODULES=ibus;wayland\n"}, "export QT_IM_MODULES=fcitx\n", {"GTK_IM_MODULE"}))
        for label, files, mine, cleared in cases:
            with self.subTest(label):
                component = self.register_ibus()
                for relative in installer.IBUS_TOOLKIT_ENV:
                    (self.home / relative).unlink()
                for relative, text in files.items():
                    (self.home / relative).write_text(text)
                user.unlink(missing_ok=True)
                if mine:
                    user.write_text(mine)
                calls = []
                def run(args, **kwargs):
                    calls.append(args)
                    out = "[('xkb', 'us'), ('ibus', '0c')]\n" if args[:2] == ["gsettings", "get"] else ""
                    return subprocess.CompletedProcess(args, 0, out, "")
                with patch.object(installer.Path, "home", lambda: self.home), \
                        patch.object(installer, "ETC", self.home / "etc"):
                    self.assertTrue(installer.ibus_unregister(self.home, run=run))
                self.assertFalse(component.exists())
                self.assertIn(["gsettings", "set", "org.gnome.desktop.input-sources", "sources", "[('xkb', 'us')]"], calls)
                unset = {args[3] for args in calls if args[:3] == ["systemctl", "--user", "unset-environment"]}
                self.assertEqual(unset - {"IBUS_COMPONENT_PATH"}, cleared)
                for relative in installer.IBUS_ENV:
                    self.assertFalse((self.home / relative).exists(), relative)
                self.assertEqual(user.exists(), bool(mine), "使用者自己的設定檔不動")

    def test_ibus_source_is_removed_without_touching_other_sources(self):
        for before, after in (("[('xkb', 'us'), ('ibus', '0c')]", "[('xkb', 'us')]"),
                              ("[('ibus', '0c'), ('xkb', 'tw')]", "[('xkb', 'tw')]"),
                              ("[('ibus', '0c')]", "[]"),
                              ("[('xkb', 'us'), ('ibus', 'chewing')]", "[('xkb', 'us'), ('ibus', 'chewing')]")):
            self.assertEqual(installer.without_ibus_source(before), after)

    def test_stable_updater_copy_follows_the_installed_updater(self):
        # 自動更新由舊更新器代為安裝，它只會把自己複製成排程用的穩定副本；穩定副本停在舊版，
        # 選單的新子命令（open-settings）就沒有反應。安裝工具要把它換成這次套件的版本。
        stable = self.home / installer.STABLE_UPDATER
        installer.install(self.bundle, self.home, self.release)
        self.assertFalse(stable.exists(), "沒有排程（沒有穩定副本）就不建立")
        stable.parent.mkdir(parents=True)
        stable.write_text("old updater")
        stable.chmod(0o700)
        (self.bundle / "bin/0c-update").write_text("newer updater")
        self.release["files"]["bin/0c-update"] = installer.digest(self.bundle / "bin/0c-update")
        self.save_release()
        installer.install(self.bundle, self.home, self.release)
        self.assertEqual(stable.read_text(), "newer updater")
        self.assertEqual(stable.stat().st_mode & 0o777, 0o700, "穩定副本只給使用者執行")
        self.assertEqual((self.home / ".local/bin/0c-update").read_text(), "newer updater")
        self.assertFalse(installer.refresh_stable_updater(self.bundle, self.home), "相同就不動")
        stable.unlink()
        stable.symlink_to(self.root / "elsewhere")
        installer.install(self.bundle, self.home, self.release)  # 符號連結不跟隨；失敗也不能讓安裝失敗
        self.assertTrue(stable.is_symlink())
        self.assertFalse((self.root / "elsewhere").exists())

    def test_uninstall_keeps_a_settings_program_or_menu_entry_it_does_not_own(self):
        self.add_settings_program()
        installer.install(self.bundle, self.home, self.release)
        program = self.home / ".local/bin/0c-settings"
        program.write_text("replaced by someone")
        entry = self.home / installer.DESKTOP_ENTRY
        entry.parent.mkdir(parents=True)
        entry.write_text("[Desktop Entry]\nName=mine\n")
        retained = installer.uninstall(self.home)
        self.assertIn(".local/bin/0c-settings", retained)
        self.assertEqual(program.read_text(), "replaced by someone")
        self.assertTrue(entry.exists(), "不是我們建立的選單項目不動")

    def test_rollback_works_in_both_directions_around_the_optional_program(self):
        program = self.home / ".local/bin/0c-settings"
        without = installer.install(self.bundle, self.home, self.release)
        self.add_settings_program()
        with_settings = installer.install(self.bundle, self.home, self.release)
        self.assertTrue(program.exists())
        # 回復到沒有設定程式的那一版：新裝的設定程式要移除。
        installer.rollback(self.home, with_settings)
        self.assertFalse(program.exists())
        # 先裝有設定程式的，再裝沒帶的新版：舊的移除（不留沒人更新的檔案），回復時放回。
        with_again = installer.install(self.bundle, self.home, self.release)
        self.drop_settings_program()
        dropped = installer.install(self.bundle, self.home, self.release)
        self.assertFalse(program.exists(), "套件沒帶設定程式時，先前裝的舊版要移除")
        installer.rollback(self.home, dropped)
        self.assertEqual(program.read_text(), "settings program")
        # 依序倒退到最初沒有設定程式的安裝：選用檔案不屬於必備清單，整條鏈都回得去。
        installer.rollback(self.home, with_again)
        self.assertFalse(program.exists())
        installer.rollback(self.home, without)
        self.assertFalse((self.home / installer.RECEIPT).exists())

    def test_old_transaction_without_the_optional_entry_still_rolls_back(self):
        first = installer.install(self.bundle, self.home, self.release)
        (self.bundle / "bin/0c-server").write_text("next engine")
        self.release["files"]["bin/0c-server"] = installer.digest(self.bundle / "bin/0c-server")
        second = installer.install(self.bundle, self.home, self.release)
        transaction = json.loads((second / "transaction.json").read_text())
        for relative in installer.OPTIONAL.values():
            transaction.pop(relative, None)  # 模擬舊版安裝器寫的交易紀錄
        (second / "transaction.json").write_text(json.dumps(transaction))
        installer.rollback(self.home, second)
        self.assertEqual((self.home / ".local/bin/0c-server").read_text(), "new bin/0c-server")
        self.assertTrue(first.exists())

    def test_webkit_install_command_follows_the_distribution(self):
        for release, command in (
                ("ID=ubuntu\nID_LIKE=debian\n", ["sudo", "apt", "install", "libwebkit2gtk-4.1-0"]),
                ('ID="linuxmint"\nID_LIKE="ubuntu debian"\n', ["sudo", "apt", "install", "libwebkit2gtk-4.1-0"]),
                ("ID=fedora\n", ["sudo", "dnf", "install", "webkit2gtk4.1"]),
                ('ID=rocky\nID_LIKE="rhel centos fedora"\n', ["sudo", "dnf", "install", "webkit2gtk4.1"]),
                ("ID=arch\n", ["sudo", "pacman", "-S", "webkit2gtk-4.1"]),
                ('ID=opensuse-tumbleweed\nID_LIKE="opensuse suse"\n', ["sudo", "zypper", "install", "libwebkit2gtk-4_1-0"]),
                ("ID=nixos\n", None), ("ID=archive\n", None), ("", None)):
            self.assertEqual(installer.webkit_install_command(release), command, release)

    def test_missing_webkit_is_explained_and_only_installed_with_consent(self):
        self.add_settings_program()
        installer.install(self.bundle, self.home, self.release)
        lines, ran = [], []
        class Done:
            returncode = 0
        def settle(interactive, answer, **extra):
            lines.clear()
            ran.clear()
            with patch.object(installer, "missing_libraries", return_value=["libwebkit2gtk-4.1.so.0"]):
                return installer.settings_dependencies(
                    self.home, interactive=interactive, ask=lambda prompt: answer, run=lambda cmd: ran.append(cmd) or Done(),
                    show=lines.append, os_release="ID=ubuntu\n", **extra)
        self.assertTrue(settle(False, "y"))
        self.assertEqual(ran, [], "非互動環境（排程、管線）只說明，不執行 sudo")
        text = "\n".join(lines)
        self.assertIn("libwebkit2gtk-4.1.so.0", text)
        self.assertIn("sudo apt install libwebkit2gtk-4.1-0", text)
        self.assertIn("0c-update settings", text, "要告訴使用者沒有它也能用文字命令設定")
        settle(True, "n")
        self.assertEqual(ran, [], "使用者不同意就不執行")
        settle(True, "")
        self.assertEqual(ran, [], "直接按 Enter 視為不同意")
        settle(True, "y")
        self.assertEqual(ran, [["sudo", "apt", "install", "libwebkit2gtk-4.1-0"]])
        # 認不得的發行版：只說明要裝什麼，不猜指令。
        with patch.object(installer, "missing_libraries", return_value=["libwebkit2gtk-4.1.so.0"]):
            lines.clear()
            installer.settings_dependencies(self.home, interactive=True, ask=lambda p: "y", run=lambda c: ran.append(c), show=lines.append, os_release="ID=nixos\n")
        self.assertIn("WebKitGTK 4.1", "\n".join(lines))
        # 函式庫齊全或沒有設定程式：什麼都不說。
        lines.clear()
        with patch.object(installer, "missing_libraries", return_value=[]):
            self.assertFalse(installer.settings_dependencies(self.home, interactive=False, show=lines.append))
        self.assertEqual(lines, [])

    def test_sudo_failure_never_breaks_the_install(self):
        self.add_settings_program()
        installer.install(self.bundle, self.home, self.release)
        def broken(command):
            raise OSError("sudo: not found")
        with patch.object(installer, "missing_libraries", return_value=["libwebkit2gtk-4.1.so.0"]):
            self.assertTrue(installer.settings_dependencies(
                self.home, interactive=True, ask=lambda p: "y", run=broken, show=lambda line: None, os_release="ID=ubuntu\n"))

    def test_upgrade_from_zuin_release_removes_old_files_and_rolls_back(self):
        # Files as installed by the zuin-* installers through 0.1.0-dev.15.
        old = {".local/bin/zuin-server": "old engine", ".local/bin/zuin-trace": "old trace",
               ".local/lib/fcitx5/libzuin.so": "old addon", ".local/share/fcitx5/addon/zuin.conf": "Library=libzuin",
               ".local/share/fcitx5/inputmethod/zuin.conf": "Addon=zuin",
               ".config/environment.d/90-zuin.conf": "FCITX_ADDON_DIRS=old",
               ".local/share/zuin/tools/install.py": "old installer", ".local/bin/0c-update": "old updater",
               **{f".local/share/zuin/data/{name}": "old " + name for name in
                  ("lexicon.bin", "lm.bin", "manifest.json", "NOTICE.txt", "report.txt")},
               installer.RECEIPT: '{"version": "0.1.0-dev.15", "active": true}'}
        for relative, content in old.items():
            (self.home / relative).parent.mkdir(parents=True, exist_ok=True)
            (self.home / relative).write_text(content)
        backup = installer.install(self.bundle, self.home, self.release)
        for relative in installer.LEGACY:
            self.assertFalse((self.home / relative).exists(), relative)
        self.assertEqual((self.home / ".local/bin/0c-server").read_text(), "new bin/0c-server")
        self.assertEqual((self.home / ".local/share/fcitx5/inputmethod/zuin.conf").read_text(), "new config/0c-im.conf")
        installer.rollback(self.home, backup)
        for relative, content in old.items():
            self.assertEqual((self.home / relative).read_text(), content, relative)
        for relative in (".local/bin/0c-server", ".local/bin/0c-trace", ".local/lib/fcitx5/lib0c.so", ".local/share/fcitx5/addon/0c.conf",
                         ".config/environment.d/90-0c.conf"):
            self.assertFalse((self.home / relative).exists(), relative)
        self.assertEqual(self.user.read_text(), "private learning")
        # The old release can be upgraded again after rolling back.
        installer.install(self.bundle, self.home, self.release)
        self.assertFalse((self.home / ".local/bin/zuin-server").exists())

    def test_rollback_can_restore_saved_user_store_and_undo_it(self):
        first = installer.install(self.bundle, self.home, self.release)
        self.assertEqual((first / installer.SAVED_USER).read_text(), "private learning")
        self.user.write_text("written by newer version")
        (self.bundle / "bin/0c-server").write_text("next engine")
        second = installer.install(self.bundle, self.home, self.release)
        self.user.write_text("newer format")
        installer.rollback(self.home, second)
        self.assertEqual(self.user.read_text(), "newer format")  # default keeps learning
        installer.install(self.bundle, self.home, self.release)
        third = installer.install(self.bundle, self.home, self.release)
        kept = installer.rollback(self.home, third, restore_user=True)
        self.assertEqual(self.user.read_text(), "newer format")  # snapshot of third == state before it
        self.user.write_text("after third")
        (self.bundle / "bin/0c-server").write_text("fourth engine")
        fourth = installer.install(self.bundle, self.home, self.release)
        self.user.write_text("written by fourth")
        kept = installer.rollback(self.home, fourth, restore_user=True)
        self.assertEqual(self.user.read_text(), "after third")
        self.assertEqual((kept / installer.SAVED_USER).read_text(), "written by fourth")
        undo = installer.restore_user_store(self.home, kept)
        self.assertEqual(self.user.read_text(), "written by fourth")
        self.assertEqual((undo / installer.SAVED_USER).read_text(), "after third")

    def test_vocab_store_follows_user_store(self):
        # #127：自訂詞彙跟 user.json 一起備份；預設回復保留目前的詞，-restore-user 一起還原並可撤回；
        # 升級前還沒有自訂詞彙時，還原不刪掉後來加的詞。
        vocab = self.home / ".local/share/zuin/vocab.json"
        first = installer.install(self.bundle, self.home, self.release)
        self.assertFalse((first / installer.SAVED_VOCAB).exists())
        vocab.write_text("words added later")
        (self.bundle / "bin/0c-server").write_text("next engine")
        second = installer.install(self.bundle, self.home, self.release)
        self.assertEqual((second / installer.SAVED_VOCAB).read_text(), "words added later")
        installer.rollback(self.home, second)
        self.assertEqual(vocab.read_text(), "words added later")  # default keeps everything
        third = installer.install(self.bundle, self.home, self.release)
        vocab.write_text("more words")
        kept = installer.rollback(self.home, third, restore_user=True)
        self.assertEqual(vocab.read_text(), "words added later")
        self.assertEqual((kept / installer.SAVED_VOCAB).read_text(), "more words")
        installer.restore_user_store(self.home, kept)
        self.assertEqual(vocab.read_text(), "more words")
        # A snapshot without vocab.json leaves the current file alone.
        installer.restore_user_store(self.home, first)
        self.assertEqual(vocab.read_text(), "more words")

    def test_restore_user_without_snapshot_changes_nothing(self):
        self.user.unlink()
        backup = installer.install(self.bundle, self.home, self.release)
        before = {p: p.read_bytes() for p in self.home.rglob("*") if p.is_file() and "backups" not in p.parts}
        with self.assertRaisesRegex(ValueError, "一般檔案"):
            installer.rollback(self.home, backup, restore_user=True)
        self.assertEqual(before, {p: p.read_bytes() for p in self.home.rglob("*") if p.is_file() and "backups" not in p.parts})
        with self.assertRaisesRegex(ValueError, "備份目錄"):
            installer.restore_user_store(self.home, self.root)

    def write_neural(self, label):
        for name in ("python/bin/python3", "sequence_runtime.py", "select/manifest.json", "select/model.onnx"):
            path = self.bundle / "neural" / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(f"{label} {name}")
        self.release["files"] = {str(p.relative_to(self.bundle)): installer.digest(p)
                                 for p in self.bundle.rglob("*") if p.is_file() and p.name != "release.json"}
        self.save_release()

    def test_neural_tree_installs_upgrades_rolls_back_and_uninstalls(self):
        neural = self.home / installer.NEURAL
        self.write_neural("v1")
        first = installer.install(self.bundle, self.home, self.release)
        self.assertEqual((neural / "select/model.onnx").read_text(), "v1 select/model.onnx")
        v1 = installer.tree_digest(neural)
        self.write_neural("v2")
        second = installer.install(self.bundle, self.home, self.release)
        self.assertEqual((neural / "select/model.onnx").read_text(), "v2 select/model.onnx")
        self.assertEqual(installer.tree_digest(second / installer.NEURAL), v1)
        installer.rollback(self.home, second)
        self.assertEqual(installer.tree_digest(neural), v1)
        installer.rollback(self.home, first)
        self.assertFalse(neural.exists())
        installer.install(self.bundle, self.home, self.release)
        (neural / "select/model.onnx").write_text("locally modified")
        self.assertIn(installer.NEURAL, installer.uninstall(self.home))
        self.assertTrue(neural.exists())
        (neural / "select/model.onnx").write_text("v2 select/model.onnx")
        installer.install(self.bundle, self.home, self.release)
        installer.uninstall(self.home)
        self.assertFalse(neural.exists())
        self.assertEqual(self.user.read_text(), "private learning")

    def test_failed_install_restores_previous_neural_tree(self):
        neural = self.home / installer.NEURAL
        self.write_neural("v1")
        installer.install(self.bundle, self.home, self.release)
        before = installer.tree_digest(neural)
        self.write_neural("v2")
        real = installer.atomic_copy
        def fail_receipt(src, dst):
            if dst.name == "install-receipt.json":
                raise OSError("disk full")
            real(src, dst)
        with patch.object(installer, "atomic_copy", side_effect=fail_receipt):
            with self.assertRaisesRegex(OSError, "disk full"):
                installer.install(self.bundle, self.home, self.release)
        self.assertEqual(installer.tree_digest(neural), before)
        self.assertEqual([p.name for p in neural.parent.iterdir() if p.name.startswith(".neural-")], [])

    def test_rollback_rejects_modified_neural_tree(self):
        neural = self.home / installer.NEURAL
        self.write_neural("v1")
        installer.install(self.bundle, self.home, self.release)
        self.write_neural("v2")
        backup = installer.install(self.bundle, self.home, self.release)
        (neural / "sequence_runtime.py").write_text("patched")
        with self.assertRaisesRegex(ValueError, "已變更"):
            installer.rollback(self.home, backup)
        self.assertEqual((neural / "sequence_runtime.py").read_text(), "patched")

    def test_rollback_rejects_reappeared_legacy_file(self):
        legacy = self.home / ".local/bin/zuin-server"
        legacy.parent.mkdir(parents=True)
        legacy.write_text("old engine")
        backup = installer.install(self.bundle, self.home, self.release)
        legacy.write_text("someone else's file")
        with self.assertRaisesRegex(ValueError, "又出現"):
            installer.rollback(self.home, backup)
        self.assertEqual(legacy.read_text(), "someone else's file")

    def test_rollback_after_core_update_restores_previous_pointer(self):
        # 完整安裝後的核心更新會重新寫入引擎指標；回復完整安裝時改回安裝前的狀態。
        pointer = self.home / installer.POINTER
        pointer.write_text('{"directory": "versions/old", "version": "0.1.0-dev.1"}')
        backup = installer.install(self.bundle, self.home, self.release)
        self.assertFalse(pointer.exists())
        pointer.write_text('{"directory": "versions/core", "version": "0.1.0-beta.2"}')
        installer.rollback(self.home, backup)
        self.assertIn("versions/old", pointer.read_text())

        # 安裝前沒有指標：回復時刪除核心更新寫入的指標。
        pointer.unlink()
        backup = installer.install(self.bundle, self.home, self.release)
        pointer.write_text('{"directory": "versions/core", "version": "0.1.0-beta.2"}')
        installer.rollback(self.home, backup)
        self.assertFalse(pointer.exists())

        # 指標位置不是一般檔案：拒絕，且不動其他檔案。
        backup = installer.install(self.bundle, self.home, self.release)
        pointer.mkdir()
        with self.assertRaisesRegex(ValueError, "一般檔案"):
            installer.rollback(self.home, backup)
        self.assertTrue((self.home / ".local/bin/0c-server").exists())

    def test_rollback_rejects_modified_installation_and_backup(self):
        installer.install(self.bundle, self.home, self.release)
        backup = installer.install(self.bundle, self.home, self.release)
        server = self.home / ".local/bin/0c-server"
        content = server.read_bytes()
        server.write_text("local modification")
        with self.assertRaisesRegex(ValueError, "已變更"):
            installer.rollback(self.home, backup)
        server.write_bytes(content)
        (backup / ".local/bin/0c-server").write_text("damaged backup")
        with self.assertRaisesRegex(ValueError, "備份雜湊"):
            installer.rollback(self.home, backup)
        self.assertEqual(server.read_bytes(), content)

    def test_failed_rollback_restores_current_files(self):
        installer.install(self.bundle, self.home, self.release)
        (self.bundle / "bin/0c-server").write_text("next engine")
        backup = installer.install(self.bundle, self.home, self.release)
        before = {p: p.read_bytes() for p in self.home.rglob("*") if p.is_file() and "backups" not in p.parts}
        real_copy = installer.atomic_copy
        failed = False
        def fail_once(src, dst):
            nonlocal failed
            if str(src).startswith(str(backup)) and dst.name == "0c-server" and not failed:
                failed = True
                raise OSError("disk full")
            real_copy(src, dst)
        with patch.object(installer, "atomic_copy", side_effect=fail_once):
            with self.assertRaisesRegex(OSError, "disk full"):
                installer.rollback(self.home, backup)
        self.assertTrue(failed)
        for path, content in before.items():
            self.assertEqual(path.read_bytes(), content)

    def test_concurrent_mutations_are_rejected(self):
        with installer.mutation_lock(self.home):
            with self.assertRaisesRegex(ValueError, "正在執行"):
                with installer.mutation_lock(self.home):
                    self.fail("second lock acquired")

    def test_windows_binary_architecture_validation(self):
        import struct
        file = self.root / "fixture.exe"
        file.write_bytes(b"MZ" + b"\0" * 58 + struct.pack("<I", 64) + b"PE\0\0\x64\x86")
        package_windows.check_pe(file)
        file.write_bytes(file.read_bytes()[:-2] + b"\x4c\x01")
        with self.assertRaisesRegex(ValueError, "x64"):
            package_windows.check_pe(file)
        # 32 位元應用程式用的 x86/0c_tsf.dll（#112）要是 i686，不能誤放 x64 那份。
        package_windows.check_pe(file, "x86")
        file.write_bytes(file.read_bytes()[:-2] + b"\x64\x86")
        with self.assertRaisesRegex(ValueError, "x86"):
            package_windows.check_pe(file, "x86")
        self.assertEqual(package_windows.DLL32, "x86/0c_tsf.dll")

    def test_windows_version_info_resource(self):
        import struct
        def parse(data, offset=0):
            length, value_length, kind = struct.unpack_from("<HHH", data, offset)
            end = data.index(b"\0\0", offset + 6)
            while (end - offset - 6) % 2:
                end = data.index(b"\0\0", end + 1)
            key = data[offset + 6:end].decode("utf-16-le")
            cursor = (end + 2 + 3) & ~3
            size = value_length * 2 if kind == 1 else value_length
            value = data[cursor:cursor + size]
            cursor = (cursor + size + 3) & ~3
            children = []
            while cursor < offset + length:
                child, cursor = parse(data, cursor)
                children.append(child)
            return (key, kind, value, children), (offset + length + 3) & ~3

        data = package_windows.version_info("0c_tsf.dll", "0.1.0-dev.37", dll=True)
        (key, _, fixed, children), end = parse(data)
        self.assertEqual((key, end), ("VS_VERSION_INFO", len(data)))
        fields = struct.unpack("<13I", fixed)
        self.assertEqual(fields[0], 0xFEEF04BD)
        self.assertEqual(fields[2:4], (0 << 16 | 1, 0 << 16 | 0))   # 0.1.0.0
        self.assertEqual(fields[9], 2)                              # VFT_DLL
        (string_info, _, _, [table]), (var_info, _, _, [translation]) = children
        self.assertEqual((string_info, table[0], var_info), ("StringFileInfo", "040404B0", "VarFileInfo"))
        strings = {k: v.decode("utf-16-le").rstrip("\0") for k, kind, v, _ in table[3] if kind == 1}
        self.assertEqual(strings["CompanyName"], "FUNMULA PTE. LTD.")
        self.assertEqual(strings["FileVersion"], "0.1.0-dev.37")
        self.assertEqual(strings["OriginalFilename"], "0c_tsf.dll")
        self.assertEqual(strings["FileDescription"], package_windows.DESCRIPTIONS["0c_tsf.dll"])
        self.assertEqual(translation[2], struct.pack("<HH", 0x0404, 1200))
        exe = struct.unpack("<13I", parse(package_windows.version_info("0c-update.exe", "1.2.3", dll=False))[0][2])
        self.assertEqual((exe[2], exe[3], exe[9]), (1 << 16 | 2, 3 << 16 | 0, 1))

    def test_authenticode_leaf_signer_ignores_timestamp_chain(self):
        output = """Signing Certificate Chain:
    Issued to: Microsoft Identity Verification Root Certificate Authority 2020
        Issued to: Microsoft ID Verified Code Signing PCA 2021
            Issued to: Microsoft ID Verified CS AOC CA 03
                Issued to: FUNMULA PTE. LTD.
The signature is timestamped: Fri Oct 02 00:19:52 2026
Timestamp Verified by:
    Issued to: Microsoft Identity Verification Root Certificate Authority 2020
        Issued to: Microsoft Public RSA Timestamping CA 2020
            Issued to: Microsoft Public RSA Time Stamping Authority
Successfully verified: 0c-update.exe
"""
        self.assertEqual(package_windows.leaf_signer(output), "FUNMULA PTE. LTD.")
        self.assertIsNone(package_windows.leaf_signer("SignTool Error: No signature found."))

    def test_setup_rejects_tampered_or_extra_payload_before_bootstrap(self):
        import hashlib
        import zipfile
        names = ('0c-update.exe', '0c-server.exe', '0c-ui.exe', '0c_tsf.dll', 'x86/0c_tsf.dll', 'TERMS.md', 'LICENSE', 'legal/README.md', 'THIRD_PARTY_NOTICES.txt',
                 'data/manifest.json', 'data/lexicon.bin', 'data/lm.bin', 'data/NOTICE.txt', 'data/report.txt')
        payload = {name: name.encode() for name in names}
        release = {'schema': 1, 'product': '0c', 'version': '0.1.0', 'channel': 'internal-preview',
                   'os': 'windows', 'arch': 'amd64',
                   'files': {name: hashlib.sha256(data).hexdigest() for name, data in payload.items()}}
        archive = self.root / 'windows.zip'
        def save():
            with zipfile.ZipFile(archive, 'w') as package:
                package.writestr('bundle/release.json', json.dumps(release))
                for name, data in payload.items():
                    package.writestr('bundle/' + name, data)
        save()
        _, updater, terms = package_windows_installer.read_bundle(archive)
        self.assertEqual(updater, payload['0c-update.exe'])
        self.assertEqual(terms, 'TERMS.md')
        payload['0c-update.exe'] = b'tampered'
        save()
        with self.assertRaisesRegex(ValueError, '雜湊'):
            package_windows_installer.read_bundle(archive)
        payload['0c-update.exe'] = b'0c-update.exe'
        payload['unexpected.dll'] = b'extra'
        save()
        with self.assertRaisesRegex(ValueError, '未列出'):
            package_windows_installer.read_bundle(archive)
        del payload['unexpected.dll']
        # 沒有 32 位元 DLL 的套件做不出能給 32 位元應用程式用的安裝精靈（#112）。
        saved = payload.pop('x86/0c_tsf.dll'), release['files'].pop('x86/0c_tsf.dll')
        save()
        with self.assertRaisesRegex(ValueError, '缺少必要檔案'):
            package_windows_installer.read_bundle(archive)
        payload['x86/0c_tsf.dll'], release['files']['x86/0c_tsf.dll'] = saved
        for name in ('TERMS.md', 'LICENSE', 'legal/README.md'):
            with self.subTest(missing=name):
                saved = payload.pop(name), release['files'].pop(name)
                save()
                with self.assertRaisesRegex(ValueError, '缺少必要檔案'):
                    package_windows_installer.read_bundle(archive)
                payload[name], release['files'][name] = saved

    def test_bundle_keeps_license_texts_and_terms_together(self):
        stage = self.root / "licensed-bundle"
        packager.copy_licenses(stage)
        for source, destination in packager.LICENSE_FILES.items():
            self.assertEqual((stage / destination).read_bytes(), (packager.ROOT / source).read_bytes())
        self.assertIn("GNU AFFERO GENERAL PUBLIC LICENSE", (stage / "LICENSE").read_text())
        self.assertIn("AGPL-3.0-only", (stage / "TERMS.md").read_text())

    def test_missing_license_stops_staging_before_any_copy(self):
        source = self.root / "license-source"
        source.mkdir()
        for name in packager.LICENSE_FILES:
            path = source / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("fixture license")
        for name in packager.LICENSE_FILES:
            with self.subTest(name=name):
                path = source / name
                path.unlink()
                stage = self.root / "incomplete-license-bundle"
                with patch.object(packager, "ROOT", source), self.assertRaises((ValueError, OSError)):
                    packager.copy_licenses(stage)
                self.assertFalse(stage.exists())
                path.write_text("fixture license")

    def test_notices_cover_shipped_go_and_bundled_headers(self):
        import collect_notices
        text = collect_notices.collect("linux-amd64")
        self.assertIn("Go 工具鏈與標準函式庫", text)
        self.assertIn("nlohmann/json", text)
        self.assertIn("TERMS.md", text)
        self.assertIn("LICENSE", text)
        self.assertIn("legal/README.md", text)
        self.assertIn("OpenCC ver.1.4.2", text)  # 輸出簡體字的字典編在 0c-server 裡（#118）
        self.assertIn("Apache License", text)

    def test_versions_match_updater_constraints(self):
        for version in ("0.1.0", "1.2.3-beta.1", "1.0.0-rc.10"):
            self.assertTrue(packager.valid_version(version))
        for version in ("01.2.3", "1.0.0-01", "../escape", "1.0.0-", "1.0.0-beta..1"):
            self.assertFalse(packager.valid_version(version))

    CHANGELOG = """# 更新紀錄

## 未發布

- 還沒上架的內容不進官網。

## 0.1.0-dev.39（2026-10-03，public-beta）

### 公開摘要

- 新功能一句話，見[說明](https://0c.biggo.com/help/)。
- 第二項跨行
  接續。

### 開發紀錄

- `internal/path` 這類內部細節不出現在官網。

## 0.1.0-dev.38（2026-10-02，public-beta）

### 公開摘要

- 上一版。
"""
    BASE = "https://s3.biggo.com/0c-releases/releases/0.1.0-dev.39/"

    def site_feed(self, withdrawn=None):
        assets = [("linux", "amd64", "0c-0.1.0-dev.39-linux-amd64.tar.gz", 30), ("windows", "amd64", "0c-0.1.0-dev.39-windows-amd64.zip", 20),
                  ("darwin", "arm64", "0c-0.1.0-dev.39-macos-arm64.tar.gz", 10)]
        return {"version": "0.1.0-dev.39", "channel": "public-beta", "withdrawn": withdrawn,
                "assets": [{"os": o, "arch": a, "url": self.BASE + n, "sha256": n[-8:] * 8, "size": z} for o, a, n, z in assets]}

    def site_uploads(self, feed):
        uploads = [(a["url"].rsplit("/", 1)[1], a["url"], a["sha256"], a["size"]) for a in feed["assets"]]
        for name in ("0c-0.1.0-dev.39-windows-amd64-setup.exe", "0c-0.1.0-dev.39-macos-arm64.pkg"):
            uploads.append((name, self.BASE + name, "e" * 64, 5))
        return uploads

    def test_changelog_public_summary(self):
        entries = site_manifest.parse_changelog(self.CHANGELOG)
        self.assertEqual([e["version"] for e in entries], ["0.1.0-dev.39", "0.1.0-dev.38"])
        self.assertEqual(entries[0], {"version": "0.1.0-dev.39", "date": "2026-10-03", "stage": "public-beta",
                                      "items": ["新功能一句話，見[說明](https://0c.biggo.com/help/)。", "第二項跨行接續。"]})
        for bad in (self.CHANGELOG.replace("### 公開摘要\n\n- 上一版。\n", ""),  # 已上架版本缺公開摘要
                    self.CHANGELOG.replace("（2026-10-02，public-beta）", "(2026-10-02, public-beta)"),  # 標題格式
                    self.CHANGELOG.replace("https://0c.biggo.com/help/", "http://0c.biggo.com/help/"),
                    self.CHANGELOG.replace("新功能一句話", "**新功能**一句話"),
                    self.CHANGELOG.replace("- 上一版。", "上一版。")):
            with self.assertRaises(ValueError):
                site_manifest.parse_changelog(bad)
        preview = self.CHANGELOG.replace("（2026-10-03，public-beta）", "（2026-10-03，internal-preview）")
        self.assertEqual([e["version"] for e in site_manifest.parse_changelog(preview)], ["0.1.0-dev.38"])

    def test_site_manifest_matches_signed_feed(self):
        feed = self.site_feed()
        changelog = site_manifest.parse_changelog(self.CHANGELOG)
        site = site_manifest.build(feed, self.site_uploads(feed), changelog, "2026-10-03T08:00:00Z")
        self.assertEqual((site["schema"], site["version"], site["withdrawn"], site["changelog"]), (1, "0.1.0-dev.39", None, changelog))
        self.assertEqual(list(site["platforms"]), ["windows-amd64", "linux-amd64", "macos-arm64"])
        self.assertEqual(site["platforms"]["linux-amd64"], {"installer": {"url": feed["assets"][0]["url"], "size": 30, "sha256": feed["assets"][0]["sha256"]}})
        self.assertTrue(site["platforms"]["windows-amd64"]["installer"]["url"].endswith("-setup.exe"))
        self.assertTrue(site["platforms"]["macos-arm64"]["installer"]["url"].endswith(".pkg"))
        uploads = self.site_uploads(feed)
        uploads[1] = (uploads[1][0], uploads[1][1], "0" * 64, uploads[1][3])  # ZIP 與已簽章索引不符
        with self.assertRaises(ValueError):
            site_manifest.build(feed, uploads, changelog, "t")
        with self.assertRaises(ValueError):  # Windows 沒有 setup.exe
            site_manifest.build(feed, self.site_uploads(feed)[:3] + self.site_uploads(feed)[4:], changelog, "t")
        with self.assertRaises(ValueError):  # CHANGELOG 最新的已上架版本不是索引版本
            site_manifest.build(feed, self.site_uploads(feed), changelog[1:], "t")

    def test_site_manifest_withdrawn(self):
        feed = self.site_feed(withdrawn=["0.1.0-dev.39"])
        changelog = site_manifest.parse_changelog(self.CHANGELOG)
        with self.assertRaises(ValueError):
            site_manifest.build(feed, self.site_uploads(feed), changelog, "t")
        site = site_manifest.build(feed, self.site_uploads(feed), changelog, "t", "此版有問題，請暫停下載。")
        self.assertEqual(site["withdrawn"], {"reason": "此版有問題，請暫停下載。"})
        with self.assertRaises(ValueError):  # 沒有撤回卻填理由
            site_manifest.build(self.site_feed(), self.site_uploads(feed), changelog, "t", "理由")

    def old_platforms(self, version="0.1.0-dev.38", mac_installer=True):
        base = f"https://s3.biggo.com/0c-releases/releases/{version}/0c-{version}"
        f = lambda suffix, size: {"url": base + suffix, "size": size, "sha256": "a" * 64}
        mac = {"archive": f("-macos-arm64.tar.gz", 7)}
        if mac_installer:
            mac["installer"] = f("-macos-arm64.pkg", 8)
        return {"windows-amd64": {"installer": f("-windows-amd64-setup.exe", 9), "archive": f("-windows-amd64.zip", 6)},
                "linux-amd64": {"installer": f("-linux-amd64.tar.gz", 5)}, "macos-arm64": mac}

    def test_site_manifest_carries_past_downloads(self):
        # #81：舊版的下載資料從線上清單帶下來；上一版取線上清單最上層；目前版本不附。
        feed = self.site_feed()
        changelog = site_manifest.parse_changelog(self.CHANGELOG)
        history = {"version": "0.1.0-dev.38", "withdrawn": None, "platforms": self.old_platforms(),
                   "changelog": [{"version": "0.1.0-dev.38", "items": ["上一版。"]}]}
        site = site_manifest.build(feed, self.site_uploads(feed), changelog, "t", history=history)
        self.assertNotIn("platforms", site["changelog"][0])
        self.assertEqual(site["changelog"][1]["platforms"], self.old_platforms())
        self.assertEqual(list(site["changelog"][1]["platforms"]), ["windows-amd64", "linux-amd64", "macos-arm64"])
        # 之後的版本：已在 changelog 裡的資料原樣保留，即使它已不是線上清單的最上層。
        later = {"version": "0.1.0-dev.39", "platforms": site["platforms"], "changelog": site["changelog"]}
        again = site_manifest.build(feed, self.site_uploads(feed), changelog, "t", history=later)
        self.assertEqual(again["changelog"][1]["platforms"], self.old_platforms())
        # 沒有線上清單時與以前相同：各版只有說明。
        plain = site_manifest.build(feed, self.site_uploads(feed), changelog, "t")
        self.assertEqual(plain["changelog"], changelog)
        # backfill 只補線上清單沒有的版本；只有 archive 的平台照列。
        filled = site_manifest.build(feed, self.site_uploads(feed), changelog, "t",
                                     backfill={"0.1.0-dev.38": self.old_platforms(mac_installer=False)})
        self.assertEqual(filled["changelog"][1]["platforms"]["macos-arm64"], {"archive": self.old_platforms()["macos-arm64"]["archive"]})
        self.assertEqual(site_manifest.build(feed, self.site_uploads(feed), changelog, "t", history=history,
                                             backfill={"0.1.0-dev.38": {}})["changelog"][1]["platforms"], self.old_platforms())

    def test_site_manifest_drops_withdrawn_past_downloads(self):
        feed = self.site_feed(withdrawn=["0.1.0-dev.38"])
        changelog = site_manifest.parse_changelog(self.CHANGELOG)
        history = {"version": "0.1.0-dev.38", "platforms": self.old_platforms(), "changelog": []}
        site = site_manifest.build(feed, self.site_uploads(feed), changelog, "t", history=history)
        self.assertNotIn("platforms", site["changelog"][1])
        self.assertEqual(site["changelog"][1]["items"], ["上一版。"])  # 說明照常保留

    def test_site_manifest_rejects_bad_past_downloads(self):
        feed = self.site_feed()
        changelog = site_manifest.parse_changelog(self.CHANGELOG)
        def bad(edit):
            platforms = self.old_platforms()
            edit(platforms)
            with self.assertRaises(ValueError):
                site_manifest.build(feed, self.site_uploads(feed), changelog, "t", backfill={"0.1.0-dev.38": platforms})
        bad(lambda p: p["linux-amd64"]["installer"].update(url=p["linux-amd64"]["installer"]["url"].replace("dev.38", "dev.37")))
        bad(lambda p: p["linux-amd64"]["installer"].update(url=p["linux-amd64"]["installer"]["url"].replace("https", "http")))
        bad(lambda p: p["windows-amd64"].update(installer=p["windows-amd64"]["archive"]))  # 角色與檔名不符
        bad(lambda p: p["linux-amd64"]["installer"].update(size=0))
        bad(lambda p: p["linux-amd64"]["installer"].update(size=True))
        bad(lambda p: p["linux-amd64"]["installer"].update(sha256="A" * 64))
        bad(lambda p: p.update({"freebsd-amd64": p["linux-amd64"]}))
        bad(lambda p: p.update({"linux-amd64": {}}))
        bad(lambda p: p["linux-amd64"].update(extra=p["linux-amd64"]["installer"]))

    def test_repository_changelog_has_public_summaries(self):
        entries = site_manifest.parse_changelog((Path(__file__).resolve().parents[1] / "CHANGELOG.md").read_text(encoding="utf-8"))
        self.assertIn("0.1.0-dev.18", [e["version"] for e in entries])
        self.assertNotIn("0.1.0-dev.17", [e["version"] for e in entries])

    def test_release_docs_are_explicit_and_copied_without_rewriting(self):
        with self.assertRaisesRegex(ValueError, "--release-docs"):
            packager.check_release_docs(None)
        external = self.root / "external-docs"
        external.mkdir()
        stage = self.root / "documented-package"
        stage.mkdir()
        for name in packager.RELEASE_DOC_FILES:
            (external / name).write_bytes(("# " + name + "\r\nExternal package instructions.\r\n").encode())
        packager.copy_release_docs(external, stage)
        for name in packager.RELEASE_DOC_FILES:
            self.assertEqual((external / name).read_bytes(), (stage / name).read_bytes())
        for name in packager.RELEASE_DOC_FILES:
            with self.subTest(name=name):
                original = (external / name).read_bytes()
                (external / name).write_text("   ")
                with self.assertRaisesRegex(ValueError, "不可為空"):
                    packager.check_release_docs(external)
                (external / name).unlink()
                with self.assertRaises((ValueError, OSError)):
                    packager.check_release_docs(external)
                (external / name).write_bytes(original)

    def test_all_platforms_require_external_docs_before_building(self):
        from types import SimpleNamespace
        data, _, _, _ = self.write_paired_data(self.root / "external-docs-gate", public=True)
        out = self.root / "output"
        args = SimpleNamespace(version="0.2.0", preview=False, data=data, out=out, release_docs=None)
        for module, system, machine in ((packager, "linux", "x86_64"),
                                         (package_windows, "win32", "AMD64"),
                                         (package_macos, "darwin", "arm64")):
            with self.subTest(platform=system), patch.object(module.sys, "platform", system), \
                    patch.object(module.platform, "machine", return_value=machine), \
                    patch.object(subprocess, "run") as run, patch.object(subprocess, "check_output") as output:
                with self.assertRaisesRegex(ValueError, "--release-docs"):
                    module.build(args)
                run.assert_not_called()
                output.assert_not_called()
                self.assertFalse(out.exists())

    def test_update_feed_preserves_hashes_and_rejects_mixed_versions(self):
        import zipfile
        def archive(version, os_name):
            file = self.root / (os_name + ".zip")
            release = {"schema": 1, "product": "0c", "version": version,
                       "channel": "internal-preview", "os": os_name, "arch": "amd64"}
            with zipfile.ZipFile(file, "w") as package:
                package.writestr("bundle/release.json", json.dumps(release))
            return file
        windows = archive("0.1.0", "windows")
        linux = archive("0.1.0", "linux")
        assets = [(windows, "https://example.org/windows.zip"), (linux, "https://example.org/linux.tar.gz")]
        feed = release_feed.make_feed(assets)
        self.assertEqual(feed["assets"][0]["sha256"], installer.digest(windows))
        self.assertEqual(feed["assets"][1]["size"], linux.stat().st_size)
        archive("0.2.0", "linux")
        with self.assertRaisesRegex(ValueError, "版本"):
            release_feed.make_feed(assets)
        with self.assertRaisesRegex(ValueError, "重複"):
            release_feed.make_feed([assets[0], assets[0]])
        with self.assertRaisesRegex(ValueError, "HTTPS"):
            release_feed.make_feed([(windows, "http://example.org/windows.zip")])

    def test_public_data_rejects_incomplete_or_private_provenance(self):
        data, _, baseline, _ = self.write_paired_data(self.root / "data-gate", public=True)
        cases = []
        for value in (None, {}, {"Public": False}, {"Public": 1}, {"Public": "true"}):
            cases.append(("options", value))
        for value in (None, [], {}, [None]):
            cases.append(("sources", value))
        for value in (None, False, 0, 1, "yes", "true"):
            cases.append(("Distributable", value))
        for field in ("ID", "Name", "URL", "Version", "License", "Attribution"):
            for value in (None, "", "-", " unknown ", "未定", 123, "line\nbreak"):
                cases.append((field, value))
        cases += [("License", "私有"), ("ID", packager.PRIVATE_RESEARCH_SOURCE),
                  ("ID", "../fixture"), ("research_news_mix", {}),
                  ("sources", baseline["sources"] * 2)]
        for field, value in cases:
            with self.subTest(field=field, value=value):
                manifest = json.loads(json.dumps(baseline))
                target = manifest if field in ("options", "sources", "research_news_mix") else manifest["sources"][0]
                if value is None:
                    target.pop(field, None)
                else:
                    target[field] = value
                (data / "manifest.json").write_bytes(packager.json_bytes(manifest))
                before = self.data_snapshot(data)
                with self.assertRaises(ValueError):
                    packager.prepare_shipped_data(data)
                self.assertEqual(self.data_snapshot(data), before)
        (data / "manifest.json").write_bytes(packager.json_bytes(baseline))
        (data / "lm.bin").write_bytes(b"tampered")
        for public in (False, True):
            with self.assertRaisesRegex(ValueError, "雜湊"):
                packager.check_data(data, public)

    def test_public_notice_requires_matching_sources_metadata_and_license_statements(self):
        data, _, manifest, _ = self.write_paired_data(self.root / "notice-gate", public=True)
        notice = (data / "NOTICE.txt").read_text(encoding="utf-8")
        cases = ["", "資料包\n", notice + notice, notice.replace("（fixture）", "（other）"),
                 notice.replace("測試詞條", "other"), notice.replace("授權：MIT", "授權：私有"),
                 notice.replace("版本：1.0", "版本：2.0"), notice.replace("https://example.org/fixture", "other"),
                 notice.replace("姓名標示：Fixture authors", "姓名標示：other"),
                 notice[:notice.index("Permission")], notice + "研究來源：{}\n"]
        for broken in cases:
            with self.subTest(notice=broken):
                (data / "NOTICE.txt").write_text(broken, encoding="utf-8")
                before = self.data_snapshot(data)
                with self.assertRaises(ValueError):
                    packager.prepare_shipped_data(data)
                self.assertEqual(self.data_snapshot(data), before)
        (data / "NOTICE.txt").write_bytes(notice.replace("\n", "\r\n").encode())
        packager.check_data(data, True)

    def test_removing_legacy_source_markers_does_not_make_data_public(self):
        data, _, manifest, _ = self.write_paired_data(self.root / "stripped")
        # Reproduce old packaging: private sources and the nested marker disappear,
        # while the binary dictionary and mixed statistics remain untouched.
        manifest["sources"] = manifest["sources"][:1]
        del manifest["research_news_mix"]["source"]
        (data / "NOTICE.txt").write_text(self.source_notice(manifest["sources"]), encoding="utf-8")
        for public in (False, True):
            manifest["options"]["Public"] = public
            (data / "manifest.json").write_bytes(packager.json_bytes(manifest))
            before = self.data_snapshot(data)
            with self.assertRaisesRegex(ValueError, "重新編譯"):
                packager.prepare_shipped_data(data)
            self.assertEqual(self.data_snapshot(data), before)

    def test_all_platform_builds_reject_private_data_before_building(self):
        from types import SimpleNamespace
        data, _, manifest, _ = self.write_paired_data(self.root / "platform-gate", public=True)
        manifest["sources"][0]["Distributable"] = False
        (data / "manifest.json").write_bytes(packager.json_bytes(manifest))
        before = self.data_snapshot(data)
        out = self.root / "output"
        args = SimpleNamespace(version="0.2.0", preview=False, data=data, out=out)
        for module, system, machine in ((packager, "linux", "x86_64"),
                                         (package_windows, "win32", "AMD64"),
                                         (package_macos, "darwin", "arm64")):
            with self.subTest(platform=system), patch.object(module.sys, "platform", system), \
                    patch.object(module.platform, "machine", return_value=machine), \
                    patch.object(subprocess, "run") as run, patch.object(subprocess, "check_output") as output:
                with self.assertRaisesRegex(ValueError, "不可散布"):
                    module.build(args)
                run.assert_not_called()
                output.assert_not_called()
                self.assertFalse(out.exists())
                self.assertEqual(self.data_snapshot(data), before)

    def test_windows_neural_bundles_cpp_runtime_next_to_python(self):
        # #80：onnxruntime 需要 MSVCP140.dll／MSVCP140_1.dll，沒裝 Visual C++ 可轉散發套件的 Windows 載入失敗。
        import collect_notices
        import prepare_neural
        import zipfile
        url, sha256, names = prepare_neural.RUNTIMES["windows-amd64"]["crt"]
        self.assertEqual(names, package_windows.NEURAL_CRT)
        self.assertTrue(url.endswith("-cp314-cp314-win_amd64.whl"))
        with tempfile.TemporaryDirectory() as temp:
            temp = Path(temp)
            wheel = temp / "msvc_runtime.whl"
            with zipfile.ZipFile(wheel, "w") as z:
                for name in ("msvcp140.dll", "msvcp140_1.dll", "concrt140.dll"):
                    z.writestr("msvc_runtime-14.44.35112.data/data/" + name, name)
                    z.writestr("msvc_runtime-14.44.35112.data/data/Scripts/" + name, "scripts " + name)
            python = temp / "neural/python"
            (python / "Lib/site-packages").mkdir(parents=True)
            prepare_neural.install_crt(wheel, names, python)
            self.assertEqual(sorted(p.name for p in python.glob("*.dll")), ["msvcp140.dll", "msvcp140_1.dll"])
            self.assertEqual((python / "msvcp140.dll").read_text(), "msvcp140.dll")
            (python / "LICENSE.txt").write_text("PSF")
            text = "\n".join(collect_notices.python_sections(temp / "neural", "windows-amd64"))
            self.assertIn("Microsoft Visual C++ 執行階段（msvcp140.dll、msvcp140_1.dll）", text)
            with zipfile.ZipFile(wheel, "a") as z:
                z.writestr("other.data/data/msvcp140.dll", "duplicate")
            with self.assertRaisesRegex(ValueError, "msvcp140.dll"):
                prepare_neural.install_crt(wheel, names, python)

    def test_bundle_name_is_not_rebound_while_packaging(self):
        # dev.60 前 #80 的迴圈 `for name in NEURAL_CRT` 蓋掉了套件名稱，Windows ZIP 的頂層目錄變成 msvcp140_1.dll/。
        import ast
        for module in ("package_windows", "package_macos", "package_release"):
            path = Path(__file__).resolve().parent / f"{module}.py"
            tree = ast.parse(path.read_text(encoding="utf-8"))
            build = next(f for f in tree.body if isinstance(f, ast.FunctionDef) and f.name == "build")
            binds = [n.lineno for n in ast.walk(build) if isinstance(n, ast.Name) and n.id == "name" and isinstance(n.ctx, ast.Store)]
            self.assertLessEqual(len(binds), 1, f"{module}.build() 重新指派了套件名稱 name：第 {binds} 行")

    def test_settings_ui_script_parses(self):
        # dev.60 前曾重複宣告 const REASON，整支設定程式畫面載入失敗；node 可用時檢查語法。
        node = shutil.which("node")
        if not node:
            self.skipTest("沒有 node")
        for name in ("app.js", "models.js"):
            app = Path(__file__).resolve().parents[1] / "platforms/settings/ui" / name
            result = subprocess.run([node, "--check", str(app)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, name + "：" + result.stderr)

    def test_settings_ui_engine_state(self):
        # 總覽「輸入法仍未全部切換」的判斷：核心更新後不誤報、外殼等重新登入時要提示（0.2.1 候選版冒煙）。
        node = shutil.which("node")
        if not node:
            self.skipTest("沒有 node")
        test = Path(__file__).resolve().parents[1] / "platforms/settings/test/engine_state_test.js"
        result = subprocess.run([node, "--test", str(test)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_settings_ui_models(self):
        # #180「模型」頁：內建只顯示資訊、可下載／下載中／已安裝／重新下載各自的按鈕、缺推論元件與網址白名單。
        node = shutil.which("node")
        if not node:
            self.skipTest("沒有 node")
        test = Path(__file__).resolve().parents[1] / "platforms/settings/test/models_test.js"
        result = subprocess.run([node, "--test", str(test)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_system_install_puts_ibus_component_where_ibus_daemon_reads(self):
        # #181：ibus-daemon 只讀它自己的元件目錄（IBus 1.5.34 實測不讀 /usr/local/share/ibus/component），
        # 所以 make install PREFIX=/usr/local 也要把 0c.xml 放到 ibus 的目錄；明確指定 IBUS_COMPONENTDIR 時照指定。
        if not shutil.which("make"):
            self.skipTest("沒有 make")
        root = Path(__file__).resolve().parents[1]

        def component_dir(pc_dir, *extra):
            env = {**os.environ, "PKG_CONFIG_LIBDIR": pc_dir, "PKG_CONFIG_PATH": ""}
            result = subprocess.run(["make", "-s", "-n", "-C", str(root), "install", "PREFIX=/usr/local", "VERSION=0.0.0",
                                     "DESTDIR=/stage", "FCITX5=0", "SETTINGS=0", *extra], capture_output=True, text=True, env=env)
            self.assertEqual(result.returncode, 0, result.stderr)
            dirs = [line.split()[-1] for line in result.stdout.splitlines() if line.startswith("install -d") and "ibus" in line]
            self.assertEqual(len(dirs), 1, result.stdout)
            return dirs[0]

        with tempfile.TemporaryDirectory() as empty, tempfile.TemporaryDirectory() as pc:
            self.assertEqual(component_dir(empty), "/stage/usr/share/ibus/component")  # 沒有 libibus-1.0-dev
            Path(pc, "ibus-1.0.pc").write_text("prefix=/opt/ibus\ndatadir=${prefix}/share\nName: ibus-1.0\n"
                                               "Description: t\nVersion: 1.5.34\n", encoding="utf-8")
            self.assertEqual(component_dir(pc), "/stage/opt/ibus/share/ibus/component")
            self.assertEqual(component_dir(empty, "IBUS_COMPONENTDIR=/usr/local/share/ibus/component"),
                             "/stage/usr/local/share/ibus/component")

    def test_pairing_lock_matches_0c_ime_pair_models(self):
        # #180：發版產生的 pairing.lock 與 0c-ime 以 scripts/pair_models.py 產生的逐位元組相同（兩份不同的字典）。
        import pairing_lock
        testdata = Path(__file__).resolve().parents[1] / "internal/models/testdata"
        for name in ("0c-ime-dfsg-pairing.lock.json", "0c-ime-0.2.3-pairing.lock.json"):
            expected = (testdata / name).read_bytes()
            dictionary = json.loads(expected)["dictionary_manifest_sha256"]
            self.assertEqual(pairing_lock.pairing_lock(dictionary), expected, name)
        with tempfile.TemporaryDirectory() as temp:
            data = Path(temp, "data")
            data.mkdir()
            (data / "manifest.json").write_bytes(b'{"x": 1}\n')
            out = Path(temp, "neural/pairing.lock.json")
            dictionary = pairing_lock.write_pairing_lock(data, out)
            self.assertEqual(json.loads(out.read_bytes())["dictionary_manifest_sha256"], installer.digest(data / "manifest.json"))
            self.assertEqual(dictionary, installer.digest(data / "manifest.json"))

    def test_system_install_ships_pairing_lock_and_model_download_switch(self):
        # #180：make install 把配對清單放在字典旁的 neural/；MODEL_DOWNLOAD=0 寫進 distribution.json 關掉下載與提醒。
        if not shutil.which("make"):
            self.skipTest("沒有 make")
        root = Path(__file__).resolve().parents[1]
        lock = root / "internal/models/testdata/0c-ime-dfsg-pairing.lock.json"

        def dry_run(*extra):
            result = subprocess.run(["make", "-s", "-n", "-C", str(root), "install", "PREFIX=/usr", "VERSION=0.0.0", "DESTDIR=/stage",
                                     "FCITX5=0", "IBUS=0", "SETTINGS=0", *extra], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            return result.stdout

        out = dry_run(f"PAIRING_LOCK={lock}")
        self.assertIn(f"install -Dm644 '{lock}' /stage/usr/share/0c/neural/pairing.lock.json", out)
        self.assertIn('"version":"%s"}', out)
        self.assertNotIn("model_download", out)
        self.assertIn('"model_download":false}', dry_run(f"PAIRING_LOCK={lock}", "MODEL_DOWNLOAD=0"))
        self.assertNotIn("neural/pairing.lock.json\n", dry_run("PAIRING_LOCK=/nonexistent/pairing.lock.json"))


class HFModelsTests(unittest.TestCase):
    """#180：官網套件內建 Hugging Face 公開版模型（scripts/hf_models.py），0.2.2 以前的內部匯出視為等價。"""

    OLD = {"select": "cef689f1e706537e54eaed4241a776f1ea41b4cad2b504061d1e27cd0c1ca342",
           "punct": "2d98c7c6c75dc9d46081e967dbd2fccef67f73a1ed9b2da44d7e02002e760cf5"}

    def setUp(self):
        import hf_models
        import pairing_lock
        self.hf, self.pairing = hf_models, pairing_lock
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        # 小的假權重：models.lock 只換 model.onnx、LICENSE、README.md 的雜湊；manifest.json、vocab.json 仍是上游原檔。
        lock = json.loads(hf_models.LOCK.read_bytes())
        self.weights = self.root / "hf"
        for role in hf_models.ROLES:
            for name in ("model.onnx", "LICENSE", "README.md"):
                content = f"{role} {name}\n".encode() * 100
                (self.weights / role).mkdir(parents=True, exist_ok=True)
                (self.weights / role / name).write_bytes(content)
                lock["models"][role]["files"][name] = {"sha256": installer.digest(self.weights / role / name), "size": len(content)}
        self.lock = self.root / "models.lock.json"
        self.lock.write_text(json.dumps(lock, indent=2) + "\n", encoding="utf-8")
        self.data = self.root / "data"
        self.data.mkdir()
        (self.data / "manifest.json").write_bytes(b'{"format": 2, "dictionary": "release"}\n')

    def tearDown(self):
        self.temp.cleanup()

    def test_identity_accepts_hugging_face_and_old_shipped_hashes(self):
        lock = json.loads(self.hf.LOCK.read_bytes())
        for role in self.hf.ROLES:
            self.assertEqual(self.hf.model_identity(role, lock["models"][role]["files"]["model.onnx"]["sha256"]), "upstream")
            self.assertEqual(self.hf.model_identity(role, self.OLD[role]), "equivalent")
            self.assertIsNone(self.hf.model_identity(role, "c" * 64))
        self.assertIsNone(self.hf.model_identity("punct", self.OLD["select"]))  # 不跨角色
        # models.lock 換了 revision：舊的等價紀錄不再適用
        lock["models"]["select"]["revision"] = "e" * 40
        bumped = self.root / "bumped.lock.json"
        bumped.write_text(json.dumps(lock), encoding="utf-8")
        self.assertIsNone(self.hf.model_identity("select", self.OLD["select"], bumped))
        self.assertEqual(self.hf.model_identity("punct", self.OLD["punct"], bumped), "equivalent")
        # 等價清單與 Go 編進 0c-update 的是同一份，記的上游與 models.lock 一致
        equivalent = json.loads(self.hf.EQUIVALENT.read_text(encoding="utf-8"))
        real = json.loads(self.hf.LOCK.read_bytes())["models"]
        for role, item in equivalent["models"].items():
            self.assertEqual((item["repository"], item["revision"], item["model.onnx"]),
                             (real[role]["repository"], real[role]["revision"], real[role]["files"]["model.onnx"]["sha256"]))

    def test_fetch_verifies_source_and_caches_downloads(self):
        weights = self.hf.fetch(self.weights, lock_path=self.lock)
        self.assertEqual(sorted(weights["select"]), ["LICENSE", "README.md", "model.onnx"])
        (self.weights / "punct/model.onnx").write_bytes(b"tampered")
        with self.assertRaisesRegex(ValueError, "punct/model.onnx 與 models.lock 不符"):
            self.hf.fetch(self.weights, lock_path=self.lock)
        (self.weights / "punct/model.onnx").write_bytes("punct model.onnx\n".encode() * 100)
        # 沒有 --from：從鏡像（Hugging Face 的 <repo>/resolve/<revision>/<檔名>）下載進快取，第二次不再下載
        lock = json.loads(self.lock.read_bytes())
        urls = []

        def fake_download(url, target):
            urls.append(url)
            role = "select" if "/0c-sequence-d512/" in url else "punct"
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(self.weights / role / url.rsplit("/", 1)[1], target)

        with patch.object(self.hf, "download", fake_download):
            cache = self.root / "cache"
            got = self.hf.fetch(cache=cache, base_url="https://mirror.test/", lock_path=self.lock)
            self.assertEqual(len(urls), 6)
            self.assertIn(f"https://mirror.test/Funmula/0c-sequence-d512/resolve/{lock['models']['select']['revision']}/model.onnx", urls)
            self.assertEqual(got["punct"]["model.onnx"], cache / "punct" / lock["models"]["punct"]["revision"] / "model.onnx")
            self.hf.fetch(cache=cache, base_url="https://mirror.test", lock_path=self.lock)
            self.assertEqual(len(urls), 6)

    def test_replace_models_pairs_public_models_and_matches_pairing_lock(self):
        # 0.2.2 形狀的 neural/：可攜 Python、worker、內部匯出的模型（含 export-validation.json）
        old = self.root / "old-neural"
        for path in ("python/bin/python3", "sequence_runtime.py", "punct_runtime.py", "README.txt",
                     "select/model.onnx", "select/manifest.json", "select/vocab.json", "select/export-validation.json",
                     "punct/model.onnx", "punct/manifest.json", "punct/vocab.json", "pairing.lock.json"):
            (old / path).parent.mkdir(parents=True, exist_ok=True)
            (old / path).write_text("old " + path)
        weights = self.hf.fetch(self.weights, lock_path=self.lock)
        out = self.hf.replace_models(old, self.data, self.root / "new/neural", weights, self.lock)
        self.assertEqual((out / "python/bin/python3").read_text(), "old python/bin/python3")
        # worker 不沿用舊 neural/ 的：一律是 repo 的版本（例如關掉 onnxruntime 遙測的修正才會出貨）
        for name in self.hf.WORKER_NAMES:
            self.assertEqual((out / name).read_bytes(), (self.hf.WORKERS / name).read_bytes(), name)
        self.assertFalse((out / "pairing.lock.json").exists())
        self.assertEqual(sorted(p.name for p in (out / "select").iterdir()), ["LICENSE", "README.md", "manifest.json", "model.onnx", "vocab.json"])
        dictionary = installer.digest(self.data / "manifest.json")
        expected = json.loads(self.pairing.pairing_lock(dictionary, self.lock))
        for role in self.hf.ROLES:
            self.assertEqual((out / role / "model.onnx").read_bytes(), (self.weights / role / "model.onnx").read_bytes())
            self.assertEqual(json.loads((out / role / "vocab.json").read_bytes())["dictionary_sha256"], dictionary)
            for name in ("manifest.json", "vocab.json"):
                self.assertEqual(installer.digest(out / role / name), expected["packages"][role][name]["sha256"], f"{role}/{name}")
        self.assertEqual(self.hf.check_shipped(out, self.data, self.lock), self.pairing.pairing_lock(dictionary, self.lock))
        with self.assertRaisesRegex(ValueError, "輸出已存在"):
            self.hf.replace_models(old, self.data, out, weights, self.lock)
        # 內部匯出的 neural/（manifest 不是上游的）不能當公開包出貨
        with self.assertRaisesRegex(ValueError, "不是公開版模型配對出貨字典的結果"):
            self.hf.check_shipped(old, self.data, self.lock)
        # worker 跟 repo 不同（沿用舊版或手改）不能出貨
        for name in self.hf.WORKER_NAMES:
            original = (out / name).read_bytes()
            (out / name).write_bytes(original + b"\n")
            with self.assertRaisesRegex(ValueError, f"neural/{name} 與 platforms/neural/runtime/{name} 不同"):
                self.hf.check_shipped(out, self.data, self.lock)
            (out / name).write_bytes(original)
        # 只換掉權重（例如沿用舊的 model.onnx）也擋下
        shutil.copyfile(old / "select/model.onnx", out / "select/model.onnx")
        with self.assertRaisesRegex(ValueError, "官網套件要內建 Hugging Face 公開版"):
            self.hf.check_shipped(out, self.data, self.lock)

    def test_shipped_repairing_keeps_the_pairing_lock_consistent(self):
        # 打包時 prepare_data_manifest 拿掉不公開來源、把模型改配對到出貨 manifest：結果仍等於出貨字典的 pairing.lock。
        news = {"ID": "zuin-news-stratified-20260921", "Name": "新聞", "Distributable": False}
        (self.data / "manifest.json").write_bytes(packager.json_bytes(
            {"format": 2, "sources": [{"ID": "zhwiki", "Distributable": True}, news]}))
        weights = self.hf.fetch(self.weights, lock_path=self.lock)
        neural = self.root / "neural"
        neural.mkdir()
        self.hf.write_packages(weights, installer.digest(self.data / "manifest.json"), neural, self.lock)
        for name in self.hf.WORKER_NAMES:
            shutil.copyfile(self.hf.WORKERS / name, neural / name)
        shipped = packager.prepare_data_manifest(self.data, neural)
        self.assertEqual(shipped, installer.digest(self.data / "manifest.json"))
        self.assertEqual(json.loads(self.hf.check_shipped(neural, self.data, self.lock))["dictionary_manifest_sha256"], shipped)

    def test_missing_in_vocab_lists_unknown_characters(self):
        # #172 的 18 個人名字都在公開版模型的 vocab 裡；沒有的字逐一列出（兩個模型各一）
        self.assertEqual(self.hf.missing_in_vocab(list("凃姸晗峯鳯伃珢瀞㵢彣烱瓈慜諪琹坣佀啓")), [])
        self.assertEqual(self.hf.missing_in_vocab(["𠀀"]), ["select:𠀀", "punct:𠀀"])

    def test_prepare_neural_takes_models_from_the_lock(self):
        import prepare_neural
        import sys
        parser_help = subprocess.run([sys.executable, "-B", str(Path(prepare_neural.__file__)), "--help"],
                                     capture_output=True, text=True).stdout
        self.assertIn("--models", parser_help)
        self.assertNotIn("--select", parser_help)


if __name__ == "__main__":
    unittest.main()
