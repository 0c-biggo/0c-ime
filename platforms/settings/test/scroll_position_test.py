"""Real Chromium regression tests: python3 platforms/settings/test/scroll_position_test.py.

Requires Playwright and Chromium; mocks only the Tauri command boundary, using
the actual HTML/CSS/JS. Does not run an engine or access personal settings.
"""
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import shutil
import threading
import unittest


class QuietHandler(SimpleHTTPRequestHandler):
    def log_message(self, *_):
        pass


class SettingsPositionTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        try:
            from playwright.sync_api import sync_playwright
        except ImportError:
            raise unittest.SkipTest("requires Playwright")
        executable = shutil.which(os.environ.get("CHROMIUM", "chromium"))
        if not executable:
            raise unittest.SkipTest("requires Chromium (or CHROMIUM)")
        cls.playwright = sync_playwright().start()
        cls.browser = cls.playwright.chromium.launch(executable_path=executable, headless=True, args=["--no-sandbox"])
        directory = Path(__file__).resolve().parents[1] / "ui"
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), partial(QuietHandler, directory=str(directory)))
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.thread.join()
        cls.server.server_close()
        cls.browser.close()
        cls.playwright.stop()

    def setUp(self):
        items = [{"key": "extra-" + str(i), "label": "測試選項 " + str(i),
                  "description": "用於驗證長頁面背景更新的捲動位置，不讀取或修改真實設定。",
                  "values": ["off", "on"], "value": "off"} for i in range(30)]
        fixture = {"items": items, "file": {"state": "ok", "read_only": False}, "features": {"vocab": False}}
        self.page = self.browser.new_page(viewport={"width": 960, "height": 680})
        self.addCleanup(self.page.close)
        self.errors = []
        self.page.on("pageerror", lambda error: self.errors.append(str(error)))
        self.page.add_init_script("""
          window.testIntervals = [];
          window.setInterval = (callback, delay) => window.testIntervals.push({callback, delay});
          window.testSettings = FIXTURE;
          window.__TAURI__ = {
            core: {invoke: async (command) => {
              if (command === 'app_info') return {platform:'windows/x86_64'};
              if (command === 'get_settings') return structuredClone(window.testSettings);
              if (command === 'get_status') return {schema:1, installed:'0.2.8', mode:'manual', runtime:{state:'not_running'}, update:{state:'never_checked'}};
              throw new Error('unexpected command: ' + command);
            }}, event: {listen: async () => () => {}}
          };
        """.replace("FIXTURE", json.dumps(fixture, ensure_ascii=False)))
        self.page.goto(f"http://127.0.0.1:{self.server.server_port}/index.html?page=candidates")
        self.page.locator('[data-focus="extra-29"]').wait_for(state="attached")

    def assert_position_preserved(self, focused):
        before = self.page.evaluate("""(focused) => {
          if (focused) document.querySelector('[data-focus="extra-0"]').focus();
          const main = document.querySelector('main');
          main.scrollTop = 850;
          return main.scrollTop;
        }""", focused)
        self.assertGreater(before, 500)
        # Simulate three real 15-second background callbacks, including a change
        # in settings. Rendering must still update data while preserving reading.
        for _ in range(3):
            after = self.page.evaluate("""async () => {
              window.testSettings.items.find(item => item.key === 'extra-29').value = 'on';
              await window.testIntervals.find(timer => timer.delay === 15000).callback();
              return document.querySelector('main').scrollTop;
            }""")
            self.assertAlmostEqual(after, before, delta=1)
        self.assertEqual(self.page.locator('[data-focus="extra-29"]').get_attribute('aria-checked'), 'true')
        self.assertEqual(self.errors, [])

    def test_background_refresh_with_previously_focused_control(self):
        self.assert_position_preserved(True)
        self.assertEqual(self.page.locator(':focus').get_attribute('data-focus'), 'extra-0')

    def test_background_refresh_without_focused_control(self):
        self.assert_position_preserved(False)

    def test_returning_to_window_preserves_scroll(self):
        before = self.page.evaluate("""() => {
          document.querySelector('[data-focus="extra-0"]').focus();
          document.querySelector('main').scrollTop = 850;
          return document.querySelector('main').scrollTop;
        }""")
        after = self.page.evaluate("""async () => {
          window.dispatchEvent(new Event('focus'));
          await new Promise(requestAnimationFrame);
          await new Promise(requestAnimationFrame);
          return document.querySelector('main').scrollTop;
        }""")
        self.assertAlmostEqual(after, before, delta=1)

    def test_navigation_to_another_page_starts_at_top(self):
        self.page.evaluate("document.querySelector('main').scrollTop = 850")
        self.page.locator('.nav[data-page="mode"]').click()
        self.assertEqual(self.page.evaluate("document.querySelector('main').scrollTop"), 0)
        self.assertEqual(self.errors, [])


if __name__ == '__main__':
    unittest.main()
