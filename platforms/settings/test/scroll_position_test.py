"""Real-browser regression tests (#268): python3 platforms/settings/test/scroll_position_test.py.

Runs in Chromium (WebView2 on Windows) and WebKit (WebKitGTK on Linux, WKWebView
on macOS). Requires Playwright; Chromium from PATH or CHROMIUM, WebKit from
`python3 -m playwright install webkit`. An engine that is missing is skipped.
Mocks only the Tauri command boundary, using the actual HTML/CSS/JS. Does not
run an engine or access personal settings.
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


def launch_chromium(playwright):
    executable = shutil.which(os.environ.get("CHROMIUM", "chromium"))
    if not executable:
        raise unittest.SkipTest("requires Chromium (or CHROMIUM)")
    return playwright.chromium.launch(executable_path=executable, headless=True, args=["--no-sandbox"])


def launch_webkit(playwright):
    try:
        return playwright.webkit.launch(headless=True)
    except Exception as error:  # Playwright's WebKit build is not installed.
        raise unittest.SkipTest("requires Playwright WebKit (python3 -m playwright install webkit): " + str(error).splitlines()[0])


class PositionTests:
    @classmethod
    def setUpClass(cls):
        try:
            from playwright.sync_api import sync_playwright
        except ImportError:
            raise unittest.SkipTest("requires Playwright")
        cls.playwright = sync_playwright().start()
        try:
            cls.browser = cls.launch(cls.playwright)
        except unittest.SkipTest:
            cls.playwright.stop()
            raise
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
        # A choice item is drawn as radio inputs (like 鍵盤排列), the others as switch buttons.
        items[1].update(values=["a", "b", "c"], value="a")
        fixture = {"items": items, "file": {"state": "ok", "read_only": False}, "features": {"vocab": False}}
        self.page = self.browser.new_page(viewport={"width": 960, "height": 680})
        self.addCleanup(self.page.close)
        self.errors = []
        self.page.on("pageerror", lambda error: self.errors.append(str(error)))
        self.page.add_init_script("""
          window.testIntervals = [];
          window.setInterval = (callback, delay) => window.testIntervals.push({callback, delay});
          window.testSettings = FIXTURE;
          // Lets render() and anything the engine defers to a task or the next frame happen
          // (WebKit scrolls a focused element into view from a queued task, not inside focus()).
          window.testSettle = async () => {
            await new Promise(resolve => setTimeout(resolve, 50));
            await new Promise(requestAnimationFrame);
            await new Promise(resolve => setTimeout(resolve, 50));
          };
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

    def read_then_scroll(self, focused):
        """Focus a control near the top the way a person does, then scroll down to read."""
        if focused == 'extra-1:a':
            # Clicking the already selected choice changes nothing but moves focus to it.
            self.page.locator('[data-focus="extra-1:a"]').click()
        elif focused:
            self.page.evaluate("(key) => document.querySelector('[data-focus=\"' + key + '\"]').focus()", focused)
        before = self.page.evaluate("""async () => {
          await window.testSettle();
          document.querySelector('main').scrollTop = 850;
          await window.testSettle();
          return document.querySelector('main').scrollTop;
        }""")
        self.assertGreater(before, 500)
        return before

    def assert_position_preserved(self, focused):
        before = self.read_then_scroll(focused)
        # Simulate three real 15-second background callbacks, including a change
        # in settings. Rendering must still update data while preserving reading.
        for _ in range(3):
            after = self.page.evaluate("""async () => {
              window.testSettings.items.find(item => item.key === 'extra-29').value = 'on';
              await window.testIntervals.find(timer => timer.delay === 15000).callback();
              await window.testSettle();
              return document.querySelector('main').scrollTop;
            }""")
            self.assertAlmostEqual(after, before, delta=1)
        self.assertEqual(self.page.locator('[data-focus="extra-29"]').get_attribute('aria-checked'), 'true')
        self.assertEqual(self.page.evaluate("document.activeElement.dataset.focus || null"), focused)
        self.assertEqual(self.errors, [])

    def test_background_refresh_with_previously_focused_switch(self):
        self.assert_position_preserved('extra-0')

    def test_background_refresh_with_previously_clicked_choice(self):
        # #268: the radio input is focused again before WebKit has laid it out.
        self.assert_position_preserved('extra-1:a')

    def test_background_refresh_without_focused_control(self):
        self.assert_position_preserved(None)

    def test_returning_to_window_preserves_scroll(self):
        for focused in ('extra-0', 'extra-1:a'):
            with self.subTest(focused=focused):
                self.page.reload()
                self.page.locator('[data-focus="extra-29"]').wait_for(state="attached")
                before = self.read_then_scroll(focused)
                after = self.page.evaluate("""async () => {
                  window.dispatchEvent(new Event('focus'));
                  await window.testSettle();
                  return document.querySelector('main').scrollTop;
                }""")
                self.assertAlmostEqual(after, before, delta=1)
                self.assertEqual(self.page.evaluate("document.activeElement.dataset.focus || null"), focused)

    def test_navigation_to_another_page_starts_at_top(self):
        self.page.evaluate("document.querySelector('main').scrollTop = 850")
        self.page.locator('.nav[data-page="mode"]').click()
        self.assertEqual(self.page.evaluate("document.querySelector('main').scrollTop"), 0)
        self.assertEqual(self.errors, [])


class ChromiumPositionTests(PositionTests, unittest.TestCase):
    """WebView2 (Windows) is Chromium."""
    launch = staticmethod(launch_chromium)


class WebKitPositionTests(PositionTests, unittest.TestCase):
    """WebKitGTK (Linux) and WKWebView (macOS) are WebKit."""
    launch = staticmethod(launch_webkit)


if __name__ == '__main__':
    unittest.main()
