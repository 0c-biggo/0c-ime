"""Real-browser tests for the one-line descriptions, per-option hints and help links (#362):
python3 platforms/settings/test/item_help_page_test.py

Runs the actual HTML/CSS/JS in Chromium (WebView2 on Windows) and WebKit (WebKitGTK on Linux,
WKWebView on macOS) at the default window size, 960x680. Requires Playwright; an engine that is
missing is skipped. The items come from the real `0c-update settings -json` (built with `go run`
against an empty home), so the texts measured here are the ones users see; without Go the test is
skipped. Mocks only the Tauri command boundary. Does not run an engine or touch personal settings.
"""
from functools import partial
from http.server import ThreadingHTTPServer
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import threading
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
from scroll_position_test import QuietHandler, launch_chromium, launch_webkit  # noqa: E402

ROOT = Path(__file__).resolve().parents[3]
PAGES = ["mode", "candidates", "keyboard", "output"]
# 頁首有「說明」連結的三頁：頁面名稱 → (設定說明頁的錨點, 頁面標題)。
PAGE_HELP = {"styles": ("input-styles", "輸入習慣"), "update": ("update-mode", "更新"), "vocab": ("vocab", "我的詞彙")}


def real_settings():
    go = shutil.which(os.environ.get("GO", "go"))
    if not go:
        raise unittest.SkipTest("requires Go (the items come from 0c-update settings -json)")
    with tempfile.TemporaryDirectory() as home:
        # 舊設定（升級上來的使用者）：各項不被內建風格鎖住，四種鍵盤排列都能選。
        base = Path(home) / ".local/share/zuin"
        base.mkdir(parents=True)
        (base / "settings.json").write_text('{"schema":1}\n')
        env = dict(os.environ, HOME=home, XDG_RUNTIME_DIR=str(Path(home) / "run"), XDG_DATA_HOME="", LOCALAPPDATA=home, CGO_ENABLED="0")
        out = subprocess.run([go, "run", "./cmd/0c-update", "settings", "-json"], cwd=ROOT, env=env, capture_output=True, text=True, check=True)
    return json.loads(out.stdout)


class ItemHelpTests:
    @classmethod
    def setUpClass(cls):
        try:
            from playwright.sync_api import sync_playwright
        except ImportError:
            raise unittest.SkipTest("requires Playwright")
        cls.settings = real_settings()
        cls.playwright = sync_playwright().start()
        try:
            cls.browser = cls.launch(cls.playwright)
        except unittest.SkipTest:
            cls.playwright.stop()
            raise
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), partial(QuietHandler, directory=str(ROOT / "platforms/settings/ui")))
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.thread.join()
        cls.server.server_close()
        cls.browser.close()
        cls.playwright.stop()

    def open(self, page, settings=None, wait=".card .row"):
        self.page = self.browser.new_page(viewport={"width": 960, "height": 680})
        self.addCleanup(self.page.close)
        self.errors = []
        self.page.on("pageerror", lambda error: self.errors.append(str(error)))
        self.page.add_init_script("""
          window.setInterval = () => 0;
          window.testSettings = FIXTURE;
          window.testOpened = [];
          window.__TAURI__ = {
            core: {invoke: async (command, args) => {
              if (command === 'app_info') return {platform: 'linux/x86_64'};
              if (command === 'get_settings') return structuredClone(window.testSettings);
              if (command === 'get_status') return {schema: 1, installed: '0.2.15', mode: 'auto', runtime: {state: 'not_running'}, update: {state: 'never_checked'}};
              if (command === 'set_settings') {
                for (const [key, value] of args.pairs) window.testSettings.items.find((item) => item.key === key).value = value;
                return {ok: true, apply: {state: 'applied'}};
              }
              if (command === 'open_url') { window.testOpened.push(args.url); return null; }
              // 「我的詞彙」「模型」頁自己的指令：這裡只看頁首，回「沒有引擎」。
              if (command === 'vocab_request') return {ok: false, error: {code: 'engine_unavailable', message: '引擎沒有在執行'}};
              if (command === 'vocab_watch' || command === 'vocab_unwatch') return null;
              if (command === 'api_command' || command === 'models_command') return {ok: false, error: {code: 'unavailable', message: ''}};
              throw new Error('unexpected command: ' + command);
            }}, event: {listen: async () => () => {}}
          };
        """.replace("FIXTURE", json.dumps(settings or self.settings, ensure_ascii=False)))
        self.page.goto(f"http://127.0.0.1:{self.server.server_port}/index.html?page={page}")
        self.page.locator(f"#page-{page} {wait}").first.wait_for(state="attached")

    def lines(self, selector):
        """[(text, number of rendered lines)] for every element matching selector on the visible page."""
        return self.page.evaluate("""(selector) => Array.from(document.querySelectorAll('.page:not([hidden]) ' + selector)).map((node) => {
          const line = parseFloat(getComputedStyle(node).lineHeight);
          const style = getComputedStyle(node);
          const inner = node.getBoundingClientRect().height - parseFloat(style.paddingTop) - parseFloat(style.paddingBottom);
          return [node.textContent, Math.round(inner / line)];
        })""", selector)

    def choose(self, key, value):
        self.page.locator(f'[data-focus="{key}:{value}"]').click()
        self.page.wait_for_function("([key, value]) => document.querySelector('[data-focus=\"' + key + ':' + value + '\"]')?.checked", arg=[key, value])

    def test_descriptions_take_at_most_two_lines(self):
        for page in PAGES:
            self.open(page)
            rows = self.lines(".row .desc")
            self.assertGreater(len(rows), 1, page)
            for text, count in rows:
                self.assertLessEqual(count, 2, f"{page}: {text}")
            self.assertEqual(self.errors, [])

    def test_keyboard_layout_shows_only_the_selected_hint(self):
        self.open("keyboard")
        item = next(it for it in self.settings["items"] if it["key"] == "keyboard-layout")
        hint = self.page.locator("#hint-keyboard-layout")
        self.assertIn("大千式", hint.text_content())
        for value, want in (("eten", "頓號改按"), ("hsu", "d f j s"), ("pinyin", "ni3hao3"), ("standard", "大千式")):
            self.choose("keyboard-layout", value)
            text = hint.text_content()
            self.assertIn(want, text)
            self.assertTrue(text.startswith(item["value_labels"][value] + "："), text)
            for other, other_hint in item["value_hints"].items():
                if other != value:
                    self.assertNotIn(other_hint, self.page.locator("#page-keyboard").text_content())
            for shown, count in self.lines(".row .hint"):
                self.assertLessEqual(count, 2, shown)
        # 單選群組用 aria-describedby 連到提示，螢幕報讀軟體選到這一組時會唸出來。
        self.assertEqual(self.page.locator('fieldset[aria-labelledby="label-keyboard-layout"]').get_attribute("aria-describedby"), "hint-keyboard-layout")
        self.assertEqual(self.errors, [])

    def test_option_without_a_hint_shows_none(self):
        self.open("keyboard")
        self.assertEqual(self.page.locator("#hint-shift-symbol-halfwidth").count(), 0)  # 全形標點（預設）沒有提示
        self.choose("shift-symbol-halfwidth", "on")
        self.assertIn("Shift＋, 打半形逗號", self.page.locator("#hint-shift-symbol-halfwidth").text_content())
        self.choose("shift-symbol-halfwidth", "off")
        self.assertEqual(self.page.locator("#hint-shift-symbol-halfwidth").count(), 0)
        for text, count in self.lines(".row .hint"):
            self.assertLessEqual(count, 2, text)

    def test_hints_on_every_page_take_at_most_two_lines(self):
        # 每個有提示的選項都選一次，量提示的行數。
        for page in PAGES:
            self.open(page)
            for item in self.settings["items"]:
                for value in (item.get("value_hints") or {}):
                    radio = self.page.locator(f'#page-{page} [data-focus="{item["key"]}:{value}"]')
                    if radio.count() == 0 or radio.is_disabled():
                        continue
                    if not radio.is_checked():
                        self.choose(item["key"], value)
                    for text, count in self.lines(f'#hint-{item["key"]}'):
                        self.assertLessEqual(count, 2, text)
            self.assertEqual(self.errors, [])

    def test_help_link_opens_the_guide_in_the_system_browser(self):
        self.open("keyboard")
        link = self.page.locator('[data-help="keyboard-layout"]')
        self.assertEqual(link.text_content().strip(), "說明 ↗")
        self.assertEqual(link.get_attribute("tabindex"), "-1")  # 不在 Tab 順序裡：每頁的 Tab 站數不變
        link.click()
        self.assertEqual(self.page.evaluate("window.testOpened"), ["https://0c.biggo.com/guide/settings/#keyboard-layout"])
        # 鍵盤：焦點在這一項的控制項上按 F1。
        self.page.locator('[data-focus="ctrl-punctuation"]').focus()
        self.page.keyboard.press("F1")
        self.assertEqual(self.page.evaluate("window.testOpened[1]"), "https://0c.biggo.com/guide/settings/#ctrl-punctuation")
        # 每一項都有連結，錨點是鍵名。
        keys = self.page.evaluate("Array.from(document.querySelectorAll('.page:not([hidden]) [data-help]')).map((node) => [node.dataset.help, node.title])")
        self.assertGreater(len(keys), 5)
        for key, url in keys:
            self.assertEqual(url, "https://0c.biggo.com/guide/settings/#" + key)
        self.assertEqual(self.errors, [])

    def test_three_pages_have_a_help_link_after_the_lead(self):
        # 輸入習慣、更新、我的詞彙：頁首那一句後面一個「說明」連結，樣式與行為同逐項的連結。
        for page, (anchor, title) in PAGE_HELP.items():
            url = "https://0c.biggo.com/guide/settings/#" + anchor
            self.open(page, wait=".lead [data-help]")
            links = self.page.locator(f"#page-{page} [data-help]")
            self.assertEqual(links.count(), 1, page)
            link = self.page.locator(f'#page-{page} .lead [data-help="{anchor}"]')
            self.assertEqual(link.text_content().strip(), "說明 ↗")
            self.assertEqual(link.get_attribute("tabindex"), "-1")
            self.assertEqual(link.get_attribute("title"), url)
            self.assertIn(title, link.get_attribute("aria-label"))
            # 跟逐項的連結同一個樣式：同樣的字級與顏色，接在句子後面不換行。
            self.assertEqual(link.evaluate("(node) => [getComputedStyle(node).fontSize, getComputedStyle(node).whiteSpace, getComputedStyle(node).paddingTop]"), ["13px", "nowrap", "0px"])
            for text, count in self.lines(".lead"):
                self.assertEqual(count, 1, text)
            link.click()
            self.assertEqual(self.page.evaluate("window.testOpened"), [url])
            # 鍵盤：這一頁上按 F1（焦點在哪裡都可以）。
            self.page.locator(f'.nav[data-page="{page}"]').focus()
            self.page.keyboard.press("F1")
            self.assertEqual(self.page.evaluate("window.testOpened"), [url, url])
            self.assertEqual(self.errors, [])

    def test_other_pages_have_no_header_link(self):
        # 模型、問題排解、總覽、關於不放；四個設定頁只有逐項的連結，焦點不在任何一項上時 F1 不開東西。
        for page, wait in (("overview", "h1"), ("models", "h1"), ("troubleshoot", ".card .row"), ("about", "h1")):
            self.open(page, wait=wait)
            self.assertEqual(self.page.locator(f"#page-{page} [data-help]").count(), 0, page)
            self.page.keyboard.press("F1")
            self.assertEqual(self.page.evaluate("window.testOpened"), [], page)
        for page in PAGES:
            self.open(page)
            self.assertEqual(self.page.locator(f"#page-{page} .lead [data-help]").count(), 0, page)
            self.page.locator(f'.nav[data-page="{page}"]').focus()
            self.page.keyboard.press("F1")
            self.assertEqual(self.page.evaluate("window.testOpened"), [], page)
        # 換頁之後照新的那一頁：從鍵盤與符號換到更新，F1 開更新的說明；焦點在某一項上時只開那一項的。
        self.open("keyboard")
        self.page.locator('[data-focus="ctrl-punctuation"]').focus()
        self.page.keyboard.press("F1")
        self.page.locator('.nav[data-page="update"]').click()
        self.page.locator("#page-update .lead [data-help]").wait_for(state="attached")
        self.page.keyboard.press("F1")
        self.assertEqual(self.page.evaluate("window.testOpened"), ["https://0c.biggo.com/guide/settings/#ctrl-punctuation", "https://0c.biggo.com/guide/settings/#update-mode"])

    def test_fuzzy_switch_links_to_its_section(self):
        self.open("candidates")
        self.page.locator('[data-help="fuzzy"]').click()
        self.assertEqual(self.page.evaluate("window.testOpened"), ["https://0c.biggo.com/guide/settings/#fuzzy-z-zh"])

    def test_old_updater_without_the_new_fields_shows_neither(self):
        # 新的設定程式配舊的 0c-update：沒有 value_hints、help_url，說明還是舊的一長段；不顯示提示與連結，其他照舊。
        old = json.loads(json.dumps(self.settings))
        for item in old["items"]:
            item.pop("value_hints", None)
            item.pop("help_url", None)
            item["description"] = "舊版的說明，一整段很長的文字。" * 6
        for page in PAGES:
            self.open(page, old)
            self.assertEqual(self.page.locator(".page:not([hidden]) .hint").count(), 0)
            self.assertEqual(self.page.locator(".page:not([hidden]) [data-help]").count(), 0)
            self.assertGreater(self.page.locator(".page:not([hidden]) .row .desc").count(), 1)
            # 量行數的方法本身要量得出長說明（舊說明就是三到五行）。
            self.assertGreater(max(count for _, count in self.lines(".row .desc")), 2)
            self.page.keyboard.press("F1")
            self.assertEqual(self.page.evaluate("window.testOpened"), [])
            self.assertEqual(self.errors, [])
        self.open("keyboard", old)
        self.choose("keyboard-layout", "eten")  # 照樣能改
        self.assertEqual(self.errors, [])


class ChromiumItemHelpTests(ItemHelpTests, unittest.TestCase):
    """WebView2 (Windows) is Chromium."""
    launch = staticmethod(launch_chromium)


class WebKitItemHelpTests(ItemHelpTests, unittest.TestCase):
    """WebKitGTK (Linux) and WKWebView (macOS) are WebKit."""
    launch = staticmethod(launch_webkit)


if __name__ == '__main__':
    unittest.main()
