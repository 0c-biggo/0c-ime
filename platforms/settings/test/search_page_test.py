"""Real-browser tests for the settings search (#363): python3 platforms/settings/test/search_page_test.py

Runs the actual HTML/CSS/JS in Chromium (WebView2 on Windows) and WebKit (WebKitGTK on Linux,
WKWebView on macOS) at the default window size, 960x680. Requires Playwright; an engine that is
missing is skipped. The items come from the real `0c-update settings -json` (built with `go build`
against an empty home, with 標點寬度 set to 所有符號鍵打半形 so one item is disabled), so the texts
and keywords searched here are the ones users get; without Go the test is skipped. Mocks only the
Tauri command boundary. Does not run an engine or touch personal settings.
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
NAV = ["overview", "styles", "mode", "candidates", "keyboard", "output", "vocab", "models", "update", "troubleshoot", "about"]
SUGGESTIONS = ["候選", "標點", "Shift", "聲調", "簡體", "英文", "更新"]
GUIDE = "https://0c.biggo.com/guide/settings/"
_SETTINGS = None


def real_settings():
    """settings -json from the real 0c-update: an upgraded user (items not locked by a built-in style) who chose 所有符號鍵打半形."""
    global _SETTINGS
    if _SETTINGS is not None:
        return _SETTINGS
    go = shutil.which(os.environ.get("GO", "go"))
    if not go:
        raise unittest.SkipTest("requires Go (the items come from 0c-update settings -json)")
    with tempfile.TemporaryDirectory() as home:
        base = Path(home) / ".local/share/zuin"
        base.mkdir(parents=True)
        (base / "settings.json").write_text('{"schema":1}\n')
        binary = Path(home) / "0c-update"
        env = dict(os.environ, HOME=home, XDG_RUNTIME_DIR=str(Path(home) / "run"), XDG_DATA_HOME="", LOCALAPPDATA=home, CGO_ENABLED="0")
        subprocess.run([go, "build", "-o", str(binary), "./cmd/0c-update"], cwd=ROOT, env=dict(os.environ, CGO_ENABLED="0"), check=True)
        subprocess.run([str(binary), "set", "shift-symbol-halfwidth", "all"], cwd=home, env=env, capture_output=True, text=True, check=True)
        out = subprocess.run([str(binary), "settings", "-json"], cwd=home, env=env, capture_output=True, text=True, check=True)
    _SETTINGS = json.loads(out.stdout)
    return _SETTINGS


class SearchTests:
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

    def open(self, page="keyboard", settings=None, platform="linux/x86_64", wait=".card .row", **context):
        self.page = self.browser.new_page(viewport={"width": 960, "height": 680}, **context)
        self.addCleanup(self.page.close)
        self.errors = []
        self.page.on("pageerror", lambda error: self.errors.append(str(error)))
        self.page.add_init_script("""
          window.setInterval = () => 0;
          window.testSettings = FIXTURE;
          window.testOpened = [];
          window.testCommands = [];
          window.__TAURI__ = {
            core: {invoke: async (command, args) => {
              window.testCommands.push(command);
              if (command === 'app_info') return {platform: 'PLATFORM'};
              if (command === 'get_settings') return structuredClone(window.testSettings);
              if (command === 'get_status') return {schema: 1, installed: '0.2.16', mode: 'auto', features: {vocab: true, update_mode: true}, runtime: {state: 'not_running'}, update: {state: 'never_checked'}};
              if (command === 'set_settings') {
                for (const [key, value] of args.pairs) window.testSettings.items.find((item) => item.key === key).value = value;
                return {ok: true, apply: {state: 'applied'}};
              }
              if (command === 'open_url') { window.testOpened.push(args.url); return null; }
              if (command === 'vocab_request') return {ok: false, error: {code: 'engine_unavailable', message: '引擎沒有在執行'}};
              if (command === 'vocab_watch' || command === 'vocab_unwatch') return null;
              if (command === 'api_command' || command === 'models_command') return {ok: false, error: {code: 'unavailable', message: ''}};
              throw new Error('unexpected command: ' + command);
            }}, event: {listen: async () => () => {}}
          };
        """.replace("FIXTURE", json.dumps(settings or self.settings, ensure_ascii=False)).replace("PLATFORM", platform))
        self.page.goto(f"http://127.0.0.1:{self.server.server_port}/index.html?page={page}")
        self.page.locator(f"#page-{page} {wait}").first.wait_for(state="attached")

    # ---- helpers ----
    def js(self, expression, arg=None):
        return self.page.evaluate(expression, arg)

    def shortcut(self, key="Control+f"):
        self.page.keyboard.press(key)

    def search(self, text):
        """Focus the box with the shortcut (selects what is there) and type."""
        self.shortcut()
        self.assertEqual(self.js("document.activeElement.id"), "search-input")
        self.page.keyboard.type(text)

    def results(self):
        return self.js("""() => Array.from(document.querySelectorAll('#search-results [role=option]')).map((node) => ({
          id: node.dataset.result, selected: node.getAttribute('aria-selected'), domId: node.id,
          name: node.querySelector('.result-name').firstChild.textContent + Array.from(node.querySelector('.result-name').childNodes).slice(1).filter((n) => !n.classList || !n.classList.contains('result-reason')).map((n) => n.textContent).join(''),
          reason: node.querySelector('.result-reason')?.textContent || '',
          value: node.querySelector('.result-value')?.lastChild.textContent || '', spoken: node.querySelector('.result-value')?.textContent || '',
          lines: Array.from(node.querySelectorAll('.result-line')).map((line) => [line.dataset.line || 'description', line.textContent]),
          marks: Array.from(node.querySelectorAll('mark')).map((mark) => mark.textContent),
        }))""")

    def shown_page(self):
        return self.js("Array.from(document.querySelectorAll('main > .page')).filter((node) => !node.hidden).map((node) => node.id)")

    def nav_state(self):
        return self.js("""() => Object.fromEntries(Array.from(document.querySelectorAll('.nav')).filter((node) => !node.hidden).map((node) => [node.dataset.page, {
          current: node.getAttribute('aria-current'), dim: node.classList.contains('nav-dim'),
          count: node.querySelector('.nav-count > [aria-hidden]')?.textContent || '',
        }]))""")

    def page_rows(self, page):
        """Setting names on a settings page, top to bottom."""
        return self.js("(page) => Array.from(document.querySelectorAll('#page-' + page + ' .row .label')).map((node) => node.firstChild.textContent)", page)

    # ---- the box ----
    def test_box_sits_above_the_navigation_on_every_page_and_is_the_first_tab_stop(self):
        for page, wait in (("overview", "h1"), ("keyboard", ".card .row"), ("update", "h1"), ("vocab", "h1"), ("about", "h1")):
            self.open(page, wait=wait)
            box = self.page.locator("#search-input")
            self.assertTrue(box.is_visible(), page)
            self.assertEqual(box.get_attribute("type"), "search")
            self.assertEqual(box.get_attribute("placeholder"), "搜尋設定")
            self.assertEqual(self.js("document.querySelector('label[for=search-input]').textContent"), "搜尋設定")
            geometry = self.js("""() => { const r = (s) => document.querySelector(s).getBoundingClientRect();
              return {brand: r('.brand').bottom, top: r('#search-input').top, bottom: r('#search-input').bottom, nav: r('.nav').top, height: r('#search-input').height}; }""")
            self.assertLessEqual(geometry["brand"], geometry["top"])
            self.assertLessEqual(geometry["bottom"], geometry["nav"])
            self.assertEqual(geometry["height"], 36)
            # 快捷鍵提示在右邊；沒有字時沒有清除鈕。
            self.assertEqual(self.page.locator("#search-key").text_content(), "Ctrl F")
            self.assertTrue(self.page.locator("#search-key").is_visible())
            self.assertFalse(self.page.locator("#search-clear").is_visible())
            self.page.keyboard.press("Tab")
            self.assertEqual(self.js("document.activeElement.id"), "search-input", page)
            self.page.keyboard.press("Tab")
            self.assertEqual(self.js("document.activeElement.dataset.page"), "overview", page)
            self.assertEqual(self.errors, [])

    def test_sidebar_still_fits_without_scrolling_at_the_default_size(self):
        self.open("keyboard")
        self.assertTrue(self.js("document.querySelector('.side').scrollHeight <= document.querySelector('.side').clientHeight"))
        self.assertTrue(self.js("document.documentElement.scrollWidth <= document.documentElement.clientWidth"))

    def test_narrow_window_keeps_the_box_beside_the_brand_and_nothing_overflows(self):
        # 視窗最小是 640×480：側邊欄橫放在上面，搜尋框接在品牌列右邊、導覽另起一行；結果頁照樣不會左右捲。
        self.open("keyboard")
        self.page.set_viewport_size({"width": 640, "height": 480})
        place = self.js("""() => { const r = (s) => document.querySelector(s).getBoundingClientRect(); return {
          brand: [r('.brand').right, r('.brand').top, r('.brand').bottom], box: [r('.search').left, r('.search').right, r('.search').top, r('.search').bottom, r('#search-input').height],
          nav: r('.navlist').top, side: [document.querySelector('.side').scrollWidth, document.querySelector('.side').clientWidth] }; }""")
        self.assertGreaterEqual(place["box"][0], place["brand"][0])
        self.assertLess(place["box"][2], place["brand"][2])
        self.assertLessEqual(place["box"][1], 640)
        self.assertGreaterEqual(place["box"][1] - place["box"][0], 180)
        self.assertEqual(place["box"][4], 36)
        self.assertGreaterEqual(place["nav"], place["box"][3])
        self.assertLessEqual(place["side"][0], place["side"][1])
        self.search("半形")
        self.assertEqual(len(self.results()), 4)
        self.assertTrue(self.js("document.querySelector('main').scrollWidth <= document.querySelector('main').clientWidth && document.documentElement.scrollWidth <= 640"))
        self.assertEqual(self.js("Array.from(document.querySelectorAll('#search-results [role=option]')).filter((row) => row.scrollWidth > row.clientWidth + 1).length"), 0)
        self.page.keyboard.press("Enter")
        self.assertEqual(self.page.locator("#page-keyboard .row.search-hit .label").text_content(), "標點寬度")
        self.assertTrue(self.page.locator("#search-back").is_visible())
        self.assertEqual(self.errors, [])

    def test_shortcut_focuses_the_box_and_selects_its_text(self):
        self.open("keyboard")
        self.page.locator('[data-focus="cjk-latin-space:off"]').focus()
        self.shortcut()
        self.assertEqual(self.js("document.activeElement.id"), "search-input")
        self.page.keyboard.type("標點")
        # 再按一次：全選，接著打的字取代原本的。
        self.page.locator('.nav[data-page="keyboard"]').focus()
        self.shortcut()
        self.assertEqual(self.js("[document.activeElement.id, document.activeElement.selectionStart, document.activeElement.selectionEnd]"), ["search-input", 0, 2])
        self.page.keyboard.type("簡體")
        self.assertEqual(self.page.locator("#search-input").input_value(), "簡體")
        # 大寫鎖定開著（key 是 F）也算。
        self.page.locator('.nav[data-page="keyboard"]').focus()
        self.page.keyboard.press("Control+F")
        self.assertEqual(self.js("document.activeElement.id"), "search-input")
        # 輸入法開著、key 不是字母時（注音鍵盤的 F 鍵是 ㄑ）看實體鍵位；別的鍵、加了 Shift 或 Alt 的都不算。
        for init, want in (({"key": "ㄑ", "code": "KeyF", "ctrlKey": True}, True), ({"key": "g", "code": "KeyG", "ctrlKey": True}, False),
                           ({"key": "F", "code": "KeyF", "ctrlKey": True, "shiftKey": True}, False), ({"key": "f", "code": "KeyF", "ctrlKey": True, "altKey": True}, False),
                           ({"key": "f", "code": "KeyF"}, False), ({"key": "f", "code": "KeyF", "metaKey": True}, False)):
            self.page.locator('.nav[data-page="keyboard"]').focus()
            prevented = self.js("(init) => { const event = new KeyboardEvent('keydown', Object.assign({bubbles: true, cancelable: true}, init)); document.activeElement.dispatchEvent(event); return event.defaultPrevented; }", init)
            self.assertEqual((prevented, self.js("document.activeElement.id") == "search-input"), (want, want), init)
        self.assertEqual(self.errors, [])

    def test_mac_uses_command_f_and_leaves_control_f_alone(self):
        self.open("keyboard", platform="macos/aarch64")
        self.page.locator("#search-key").wait_for()
        self.page.wait_for_function("document.getElementById('search-key').textContent === '⌘F'")
        self.assertEqual(self.page.locator("#search-input").get_attribute("aria-keyshortcuts"), "Meta+F")
        self.page.locator('[data-focus="cjk-latin-space:off"]').focus()
        self.page.keyboard.press("Control+f")  # Mac 的 Ctrl＋F 是游標往前一格，不搶
        self.assertEqual(self.js("document.activeElement.dataset.focus"), "cjk-latin-space:off")
        self.page.keyboard.press("Meta+f")
        self.assertEqual(self.js("document.activeElement.id"), "search-input")
        self.assertEqual(self.errors, [])

    def test_only_the_shortcut_is_taken_from_other_text_fields(self):
        # 「我的詞彙」的新增欄位：一般的字、/、方向鍵、Enter、Esc 都照舊留在那個欄位；只有 Ctrl＋F 會到搜尋框。
        self.open("vocab", wait=".vadd")
        field = self.page.locator(".vadd")
        field.click()
        self.page.keyboard.type("find/f")
        for key in ("ArrowLeft", "ArrowUp", "ArrowDown", "Escape", "Shift+f", "Alt+f"):
            self.page.keyboard.press(key)
            self.assertTrue(self.js("document.activeElement.classList.contains('vadd')"), key)
        self.assertIn("find/f", field.input_value())
        self.assertEqual(self.shown_page(), ["page-vocab"])
        self.shortcut()
        self.assertEqual(self.js("document.activeElement.id"), "search-input")
        # 搜尋框是空的：Esc 把焦點還給原本的欄位。
        self.page.keyboard.press("Escape")
        self.assertTrue(self.js("document.activeElement.classList.contains('vadd')"))
        self.assertEqual(self.errors, [])

    def test_native_search_decorations_are_gone_so_there_is_one_clear_button(self):
        # type=search 在 WebKit 與 Chromium 有自帶的清除鈕（有字、有焦點或滑鼠移上去時出現）。
        # 把自己的清除鈕藏起來，輸入框右邊那一塊的畫面要跟 type=text（沒有自帶按鈕）時一模一樣。
        self.open("keyboard")
        self.search("abc")
        self.assertTrue(self.page.locator("#search-clear").is_visible())
        self.assertFalse(self.page.locator("#search-key").is_visible())
        self.assertEqual(self.js("getComputedStyle(document.getElementById('search-input')).appearance"), "none")
        self.page.add_style_tag(content="#search-clear { visibility: hidden !important; }")
        box = self.page.locator("#search-input").bounding_box()
        self.page.mouse.move(box["x"] + box["width"] - 14, box["y"] + box["height"] / 2)
        # 只比輸入框裡面（四邊各縮 8px）：圓角邊線的反鋸齒每次畫會差一階，不是要量的東西。
        clip = {"x": box["x"] + box["width"] - 60, "y": box["y"] + 8, "width": 52, "height": box["height"] - 16}
        as_search = self.page.screenshot(clip=clip)
        self.js("document.getElementById('search-input').type = 'text'")
        as_text = self.page.screenshot(clip=clip)
        self.assertEqual(as_search, as_text)
        # 量得出差別：拿掉那幾條規則，自帶的外觀就回來了（Chromium 與 WebKit 都有）。
        self.js("document.getElementById('search-input').type = 'search'")
        self.page.add_style_tag(content="#search-input { -webkit-appearance: auto !important; appearance: auto !important; } #search-input::-webkit-search-cancel-button { -webkit-appearance: auto !important; appearance: auto !important; display: block !important; }")
        self.assertNotEqual(self.page.screenshot(clip=clip), as_text)
        self.assertEqual(self.errors, [])

    # ---- results ----
    def test_typing_replaces_the_page_with_grouped_results(self):
        self.open("keyboard")
        before = self.js("window.testCommands.length")
        self.search("半形")
        self.assertEqual(self.shown_page(), ["page-search"])
        self.assertEqual(self.page.locator("#search-title").text_content(), "搜尋「半形」")
        self.assertEqual(self.page.locator("#search-count").text_content(), "找到 4 個設定。")
        self.assertEqual(self.page.locator("#search-count").get_attribute("aria-live"), "polite")
        rows = self.results()
        self.assertEqual([row["id"] for row in rows], ["item:ctrl-punctuation", "item:shift-symbol-halfwidth", "item:cjk-latin-space", "item:style-shift-space"])
        self.assertEqual(self.js("Array.from(document.querySelectorAll('.search-group')).map((node) => node.textContent)"), ["鍵盤與符號"])
        by = {row["id"]: row for row in rows}
        # 反灰停用的項目：名稱旁是停用原因，值是實際使用的（固定開啟），靠關鍵字找到。
        ctrl = by["item:ctrl-punctuation"]
        self.assertEqual((ctrl["name"].strip(), ctrl["reason"], ctrl["value"]), ("Ctrl＋標點打全形", "所有符號鍵打半形時固定開啟", "開啟"))
        self.assertEqual(ctrl["lines"][-1], ["keywords", "也可以找：半形、逗號、句號、Control"])
        self.assertTrue(self.js("document.querySelector('[data-result=\"item:ctrl-punctuation\"]').classList.contains('is-disabled')"))
        # 說明與選項名稱命中：多一行「選項：」，只列命中的選項。
        width = by["item:shift-symbol-halfwidth"]
        self.assertEqual(width["value"], "所有符號鍵打半形")
        self.assertEqual(width["lines"], [["description", "注音模式的符號鍵打全形標點還是半形符號。"], ["options", "選項：Shift＋符號打半形、所有符號鍵打半形"]])
        self.assertEqual(by["item:cjk-latin-space"]["lines"], [["description", "送出時在中文與英文、數字之間補半形空格，只看同一次送出的字。"]])
        self.assertEqual(by["item:cjk-latin-space"]["value"], "關閉")
        self.assertEqual(by["item:cjk-latin-space"]["spoken"], "目前：關閉")   # 報讀軟體唸的；「目前：」畫面上看不到
        self.assertLessEqual(self.js("document.querySelector('.result-value .sr-only').getBoundingClientRect().width"), 1)
        self.assertEqual(by["item:style-shift-space"]["lines"][1], ["options", "選項：切換全半形"])
        for row in rows:
            self.assertTrue(row["marks"], row["id"])
            self.assertEqual(set(row["marks"]), {"半形"}, row["id"])
        # 預設選取第一筆沒有停用的（標點寬度）：第一筆 Ctrl＋標點是反灰的，照樣列在原位。
        # 清單是 listbox／option，搜尋框用 aria-activedescendant 指著選取的那一個。
        self.assertEqual([row["selected"] for row in rows], ["false", "true", "false", "false"])
        self.assertEqual(self.page.locator("#search-input").get_attribute("aria-activedescendant"), rows[1]["domId"])
        self.assertEqual(self.js("document.querySelector('main').scrollTop"), 0)
        self.assertEqual(self.page.locator("#search-input").get_attribute("aria-controls"), "search-results")
        self.assertEqual(self.page.locator("#search-results").get_attribute("role"), "listbox")
        self.assertEqual(self.js("document.activeElement.id"), "search-input")
        # 側邊欄：沒有目前頁面，命中的頁面標筆數，其他變淡。
        nav = self.nav_state()
        self.assertEqual([name for name, state in nav.items() if state["current"]], [])
        self.assertEqual({name: state["count"] for name, state in nav.items() if state["count"]}, {"keyboard": "4"})
        self.assertEqual(sorted(name for name, state in nav.items() if state["dim"]), sorted(name for name in NAV if name != "keyboard"))
        self.assertEqual(self.js("document.querySelector('.nav[data-page=keyboard]').textContent"), "鍵盤與符號4，4 個符合")
        # 底部的按鍵提示貼在視窗底部。
        self.assertEqual(self.js("Array.from(document.querySelectorAll('.search-keys > span')).map((node) => node.textContent)"), ["↑ ↓ 選擇", "Enter 前往", "Esc 清除，回到原本那一頁"])
        bottom = self.js("document.querySelector('.search-keys').getBoundingClientRect().bottom")
        self.assertAlmostEqual(bottom, 660, delta=1)   # 離視窗底 20px，同設計稿
        # 結果不多時整頁剛好一個畫面，不會多出一小段可以捲。
        self.assertEqual(self.js("document.querySelector('main').scrollHeight - document.querySelector('main').clientHeight"), 0)
        # 搜尋是純前端：打字沒有多呼叫後端。
        self.assertEqual(self.js("window.testCommands.length"), before)
        self.assertEqual(self.errors, [])

    def test_groups_follow_the_sidebar_and_rows_follow_each_page(self):
        self.open("keyboard")
        self.search("shift")
        rows = self.results()
        groups = self.js("""() => Array.from(document.querySelectorAll('#search-results [role=group]')).map((group) => [
          group.querySelector('.search-group').textContent, Array.from(group.querySelectorAll('[role=option] .result-name')).map((node) => node.firstChild.textContent + (node.childNodes[1] && node.childNodes[1].nodeName === 'MARK' ? '' : ''))])""")
        titles = [title for title, _ in groups]
        self.assertEqual(titles, ["中英切換", "選字", "鍵盤與符號", "輸出設定"])
        counts = {name: state["count"] for name, state in self.nav_state().items() if state["count"]}
        self.assertEqual(list(counts), ["mode", "candidates", "keyboard", "output"])
        self.assertEqual(sum(int(count) for count in counts.values()), len(rows))
        self.assertEqual(self.page.locator("#search-count").text_content(), f"找到 {len(rows)} 個設定。")
        # 每一組裡的順序就是那一頁由上到下的順序（輸入習慣的規則 style-* 歸在它實際所在的頁面）。
        found = {}
        for row in rows:
            found.setdefault(row["id"], row)
        self.assertIn("item:style-shift-letter", found)   # 在「中英切換」
        self.assertIn("item:style-shift-space", found)    # 在「鍵盤與符號」
        per_page = self.js("""() => Array.from(document.querySelectorAll('#search-results [role=group]')).map((group) =>
          Array.from(group.querySelectorAll('[role=option]')).map((node) => node.dataset.result.replace('item:', '')))""")
        self.page.keyboard.press("Escape")
        for name, keys in zip(["mode", "candidates", "keyboard", "output"], per_page):
            self.page.locator(f'.nav[data-page="{name}"]').click()
            order = self.js("(page) => Array.from(document.querySelectorAll('#page-' + page + ' .row .label[id]')).map((node) => node.id.replace('label-', ''))", name)
            positions = [order.index(key) for key in keys]
            self.assertEqual(positions, sorted(positions), f"{name}: {keys} vs {order}")
        self.assertEqual(self.errors, [])

    def test_auto_spacing_is_one_item_with_three_choices(self):
        # #365：自動加空格是一項三選一（關閉／中英文之間／中英文與數字之間），沒有另一個數字的開關；搜「空格」「數字」「中英文」都找得到。
        self.open("keyboard")
        radios = "Array.from(document.querySelectorAll('#page-keyboard input[name=\"cjk-latin-space\"]')).map((node) => [node.value, node.parentElement.textContent.trim(), node.checked])"
        self.assertEqual(self.js(radios), [["off", "關閉", True], ["on", "中英文之間", False], ["all", "中英文與數字之間", False]])
        self.assertEqual(self.js("document.querySelectorAll('[data-focus^=\"cjk-digit-space\"]').length"), 0)
        self.assertEqual(self.js("document.getElementById('label-cjk-latin-space').firstChild.textContent"), "自動加空格")
        # 方向鍵換選項（一組單選只占一個 Tab 站）：↓ 中英文之間、再 ↓ 中英文與數字之間。
        self.page.locator('[data-focus="cjk-latin-space:off"]').focus()
        for value in ("on", "all"):
            self.page.keyboard.press("ArrowDown")
            self.page.wait_for_function("(value) => window.testSettings.items.find((item) => item.key === 'cjk-latin-space').value === value", arg=value)
            self.page.wait_for_function("(value) => document.activeElement.dataset.focus === 'cjk-latin-space:' + value && document.activeElement.checked", arg=value)
        self.page.keyboard.press("Tab")
        self.assertEqual(self.js("document.activeElement.dataset.focus"), "punctuation")
        rows = {}
        for query in ("空格", "數字", "中英文", "盤古"):
            self.search(query)
            rows[query] = {row["id"]: row for row in self.results()}
            self.assertIn("item:cjk-latin-space", rows[query], query)
            self.assertEqual(rows[query]["item:cjk-latin-space"]["value"], "中英文與數字之間")
        self.assertEqual(rows["中英文"]["item:cjk-latin-space"]["lines"][1], ["options", "選項：中英文之間、中英文與數字之間"])
        self.assertEqual(rows["盤古"]["item:cjk-latin-space"]["lines"][-1], ["keywords", "也可以找：空白、間距、排版、盤古"])
        self.assertEqual(self.errors, [])

    def test_background_refresh_keeps_the_rows_and_the_selection(self):
        # 背景更新（每 15 秒、視窗重新取得焦點、更新頁每秒一次）會重畫：結果沒變時那幾列是同一批節點（換掉的話滑鼠按到一半會點不到），
        # 選取與焦點都留著；設定值變了才重建，值的標籤跟著換。
        self.open("keyboard")
        self.search("半形")
        self.page.keyboard.press("ArrowDown")
        self.js("window.testRow = document.querySelector('[data-result=\"item:cjk-latin-space\"]')")
        self.js("window.dispatchEvent(new Event('focus'))")
        self.page.wait_for_function("window.testCommands.filter((name) => name === 'get_settings').length >= 2")
        self.page.wait_for_timeout(100)
        self.assertTrue(self.js("window.testRow.isConnected && window.testRow === document.querySelector('[data-result=\"item:cjk-latin-space\"]')"))
        self.assertEqual([row["selected"] for row in self.results()], ["false", "false", "true", "false"])
        self.assertEqual(self.js("document.activeElement.id"), "search-input")
        self.assertEqual(self.shown_page(), ["page-search"])
        self.js("window.testSettings.items.find((item) => item.key === 'cjk-latin-space').value = 'on'")
        self.js("window.dispatchEvent(new Event('focus'))")
        self.page.wait_for_function("document.querySelector('[data-result=\"item:cjk-latin-space\"] .result-value').lastChild.textContent === '中英文之間'")
        self.assertEqual([row["selected"] for row in self.results()], ["false", "false", "true", "false"])
        self.assertEqual(self.errors, [])

    def test_multiple_words_must_all_match_and_width_and_case_are_ignored(self):
        self.open("keyboard")
        self.search("Shift 空白")
        ids = [row["id"] for row in self.results()]
        self.assertIn("item:style-shift-space", ids)
        self.assertNotIn("item:toggle-key", ids)
        for query in ("ＳＨＩＦＴ　空白", "  shift   空白  "):
            self.search(query)
            self.assertEqual([row["id"] for row in self.results()], ids, query)
        self.assertEqual(self.page.locator("#search-title").text_content(), "搜尋「shift   空白」")
        self.assertEqual(self.errors, [])

    def test_rows_do_not_overflow_at_the_default_size(self):
        self.open("keyboard")
        for query in ("半形", "shift", "的", "候選", "更新", "鍵"):
            self.search(query)
            self.assertGreater(len(self.results()), 0, query)
            overflow = self.js("""() => {
              const main = document.querySelector('main').getBoundingClientRect();
              const bad = [];
              if (document.querySelector('main').scrollWidth > document.querySelector('main').clientWidth) bad.push('main scrolls sideways');
              for (const row of document.querySelectorAll('#search-results [role=option]')) {
                const box = row.getBoundingClientRect();
                if (row.scrollWidth > row.clientWidth + 1) bad.push('row: ' + row.dataset.result);
                for (const part of row.querySelectorAll('.result-text, .result-value, .result-go, .result-reason, .result-line, mark')) {
                  const r = part.getBoundingClientRect();
                  if (r.left < box.left - 0.5 || r.right > box.right + 0.5 || r.width <= 0) bad.push(part.className + ': ' + row.dataset.result);
                }
                const value = row.querySelector('.result-value');
                if (value && value.scrollWidth > value.clientWidth + 1) bad.push('value clipped: ' + value.textContent);
                if (box.right > main.right - 30) bad.push('row past the page: ' + row.dataset.result);
              }
              return bad;
            }""")
            self.assertEqual(overflow, [], query)
        self.assertEqual(self.errors, [])

    # ---- keyboard ----
    def test_arrows_move_the_selection_and_stop_at_the_ends(self):
        self.open("keyboard")
        self.search("半形")
        selected = lambda: [row["selected"] for row in self.results()].index("true")
        self.assertEqual(selected(), 1)   # 預設跳過反灰的第一筆
        # ↑ 照樣走得到反灰的那一筆，再按就停在那裡。
        for want in (0, 0):
            self.page.keyboard.press("ArrowUp")
            self.assertEqual(selected(), want)
            self.assertEqual(self.results()[want]["id"], "item:ctrl-punctuation")
        for want in (1, 2, 3, 3):
            self.page.keyboard.press("ArrowDown")
            self.assertEqual(selected(), want)
            self.assertEqual(self.page.locator("#search-input").get_attribute("aria-activedescendant"), self.results()[want]["domId"])
        self.page.keyboard.press("ArrowUp")
        self.assertEqual(selected(), 2)
        # 焦點一直在搜尋框，字照樣能打；再打字回到第一個。
        self.assertEqual(self.js("document.activeElement.id"), "search-input")
        self.page.keyboard.type("符")
        self.assertEqual(self.page.locator("#search-input").input_value(), "半形符")
        self.assertEqual(selected(), 0)
        self.assertEqual(self.errors, [])

    def test_default_selection_skips_disabled_results(self):
        # 打「半形」：第一筆是反灰的 Ctrl＋標點（所有符號鍵打半形時固定開啟），預設選取的是下一筆能改的「標點寬度」；
        # 直接按 Enter 就到標點寬度。順序不變，反灰的照樣列在第一筆。
        self.open("mode")
        self.search("半形")
        rows = self.results()
        self.assertEqual([(row["id"], row["selected"]) for row in rows][:2], [("item:ctrl-punctuation", "false"), ("item:shift-symbol-halfwidth", "true")])
        self.assertTrue(rows[0]["reason"])
        self.page.keyboard.press("Enter")
        self.assertEqual(self.page.locator("#page-keyboard .row.search-hit .label").text_content(), "標點寬度")
        self.assertEqual(self.js("document.activeElement.name"), "shift-symbol-halfwidth")
        # 每次查詢變了都重新挑：改成只找得到反灰項目的詞，選第一筆（全部都反灰時才選反灰的）。
        self.search("Control")
        self.assertEqual([(row["id"], row["selected"], bool(row["reason"])) for row in self.results()], [("item:ctrl-punctuation", "true", True)])
        self.page.keyboard.press("Enter")
        self.assertEqual(self.page.locator("#page-keyboard .row.search-hit .label").first.text_content(), "Ctrl＋標點打全形所有符號鍵打半形時固定開啟")
        # 第一筆能改時就是第一筆；「聲調」的結果裡反灰的「不打聲調也能選字」不在第一筆，不影響。
        self.search("shift")
        self.assertEqual([row["selected"] for row in self.results()].index("true"), 0)
        self.search("聲調")
        rows = self.results()
        self.assertFalse(rows[[row["selected"] for row in rows].index("true")]["reason"])
        self.assertEqual([row["selected"] for row in rows].index("true"), next(i for i, row in enumerate(rows) if not row["reason"]))
        # 回到結果頁（沒有改查詢）時不重新挑：選取留在剛才那一筆。
        self.search("半形")
        self.page.keyboard.press("ArrowUp")
        self.assertEqual([row["selected"] for row in self.results()].index("true"), 0)
        self.page.locator('.nav[data-page="about"]').click()
        self.page.locator("#search-input").click()
        self.assertEqual(self.shown_page(), ["page-search"])
        self.assertEqual([row["selected"] for row in self.results()].index("true"), 0)
        self.assertEqual(self.errors, [])

    def test_enter_jumps_to_the_item_marks_it_and_fades(self):
        self.open("mode")
        self.search("半形")
        # 記下標示出現、開始淡出、移除的時間（用 MutationObserver，不靠測試自己的等待時間）。
        self.js("""() => { window.testMarks = []; new MutationObserver(() => {
            const row = document.querySelector('.search-hit');
            const now = row ? (row.classList.contains('search-hit-fade') ? 'fade' : 'on') : 'off';
            if (!window.testMarks.length || window.testMarks[window.testMarks.length - 1][0] !== now) window.testMarks.push([now, performance.now()]);
          }).observe(document.querySelector('main'), {subtree: true, childList: true, attributes: true, attributeFilter: ['class']}); }""")
        self.page.keyboard.press("Enter")
        # 切到那一頁、那一項有外框與底色、焦點在它的控制項上（選中的單選鈕），鍵盤可以直接改。
        self.assertEqual(self.shown_page(), ["page-keyboard"])
        row = self.page.locator("#page-keyboard .row.search-hit")
        self.assertEqual(row.count(), 1)
        self.assertEqual(row.locator(".label").text_content(), "標點寬度")
        style = row.evaluate("(node) => { const s = getComputedStyle(node); return [s.backgroundColor, s.outlineStyle, s.outlineWidth, s.outlineColor]; }")
        self.assertEqual(style, ["rgb(226, 240, 244)", "solid", "2px", "rgb(0, 92, 115)"])
        self.assertEqual(self.js("[document.activeElement.name, document.activeElement.value, document.activeElement.checked]"), ["shift-symbol-halfwidth", "all", True])
        visible = row.evaluate("(node) => { const r = node.getBoundingClientRect(), m = document.querySelector('main').getBoundingClientRect(); return r.top >= m.top && r.bottom <= m.bottom; }")
        self.assertTrue(visible)
        # 側邊欄回到目前頁面；搜尋框保留查詢字串；頁首上方有「回到搜尋結果」。
        nav = self.nav_state()
        self.assertEqual([name for name, state in nav.items() if state["current"]], ["keyboard"])
        self.assertEqual([name for name, state in nav.items() if state["count"] or state["dim"]], [])
        self.assertEqual(self.page.locator("#search-input").input_value(), "半形")
        self.assertTrue(self.page.locator("#search-clear").is_visible())
        back = self.page.locator("#search-back")
        self.assertTrue(back.is_visible())
        self.assertEqual(back.text_content(), "回到「半形」的搜尋結果")
        # 位置同設計稿：離頂端 20px，跟頁首標題隔 10px，不疊在標題上。
        place = self.js("[document.getElementById('search-back').getBoundingClientRect().top, document.getElementById('search-back').getBoundingClientRect().bottom, document.querySelector('#page-keyboard h1').getBoundingClientRect().top]")
        self.assertEqual(place[0], 20)
        self.assertAlmostEqual(place[2] - place[1], 10, delta=1)
        # 鍵盤直接改：↑ 選上一個選項。
        self.page.keyboard.press("ArrowUp")
        self.page.wait_for_function("window.testSettings.items.find((item) => item.key === 'shift-symbol-halfwidth').value === 'on'")
        # 背景重畫（每 15 秒、改設定之後）那一列是新的節點，標示還在。
        self.page.locator("#page-keyboard .row.search-hit").wait_for()
        self.assertEqual(self.page.locator("#page-keyboard .row.search-hit .label").text_content(), "標點寬度")
        # 約 2.5 秒後淡掉：先加上淡出的 class（0.6 秒的漸變），再整個移除。
        self.page.wait_for_function("document.querySelectorAll('.search-hit').length === 0", timeout=8000)
        marks = self.js("window.testMarks")
        self.assertEqual([name for name, _ in marks], ["on", "fade", "off"], marks)
        self.assertTrue(2300 <= marks[1][1] - marks[0][1] <= 3500, marks)
        self.assertTrue(500 <= marks[2][1] - marks[1][1] <= 1500, marks)
        self.assertTrue(back.is_visible())
        self.assertEqual(self.errors, [])

    def test_item_far_down_the_page_is_scrolled_into_view_and_the_back_link_stays_on_top(self):
        self.open("mode")
        self.search("Shift＋Delete")
        self.assertEqual([row["id"] for row in self.results()], ["item:style-shift-delete"])
        self.page.keyboard.press("Enter")
        row = self.page.locator("#page-keyboard .row.search-hit")
        self.assertEqual(row.locator(".label").text_content(), "Shift＋Delete")
        place = self.js("""() => { const r = (s) => document.querySelector(s).getBoundingClientRect(); return {
          scroll: document.querySelector('main').scrollTop, rowTop: r('.search-hit').top, rowBottom: r('.search-hit').bottom, back: r('#search-back').top,
          focus: [document.activeElement.name, document.activeElement.checked] }; }""")
        self.assertGreater(place["scroll"], 100)
        self.assertTrue(56 <= place["rowTop"] and place["rowBottom"] <= 680, place)
        self.assertEqual(place["back"], 10)   # 捲下去之後留在上緣
        self.assertEqual(place["focus"], ["style-shift-delete", True])
        # 從那裡回結果頁再清除：回到這一頁、捲到的位置也還原。
        scrolled = place["scroll"]
        self.page.locator("#search-back").click()
        self.assertEqual(self.js("document.querySelector('main').scrollTop"), 0)
        self.page.keyboard.press("Escape")
        self.assertEqual(self.shown_page(), ["page-keyboard"])
        # （「回到搜尋結果」那一行不見了，頁面短了一點；這一項在頁尾，所以最多差那一行的高度。）
        self.assertTrue(scrolled - 30 <= self.js("document.querySelector('main').scrollTop") <= scrolled)
        self.assertTrue(self.js("(() => { const r = document.getElementById('label-style-shift-delete').getBoundingClientRect(); return r.top >= 0 && r.bottom <= 680; })()"))
        self.assertEqual(self.errors, [])

    def test_the_mark_does_not_move_the_row_when_it_appears_or_fades(self):
        # 標示只加外框與底色：那一列的文字、控制項與下面各列的位置，標示中與淡掉之後完全一樣（一般的列、Emoji 的子選項、模糊音）。
        rects = """(key) => { const main = document.querySelector('main'); const page = document.querySelector('main > .page:not([hidden])');
          const r = (node) => { const b = node.getBoundingClientRect(); return [Math.round(b.left * 10) / 10, Math.round((b.top + main.scrollTop) * 10) / 10, Math.round(b.width * 10) / 10, Math.round(b.height * 10) / 10]; };
          return Array.from(page.querySelectorAll('.label, .desc, .switch, fieldset.choices')).map(r); }"""
        for query, result, label in (("半形", "item:cjk-latin-space", "自動加空格"), ("等號", "item:emoji-equals-first", "將 = 放在第一項"), ("捲舌", "fuzzy", "模糊音")):
            self.open("mode", reduced_motion="reduce")
            self.search(query)
            self.page.locator(f'[data-result="{result}"]').click()
            row = self.page.locator(".row.search-hit")
            self.assertEqual(row.locator(".label").first.evaluate("(node) => node.firstChild.textContent"), label)
            marked = self.js(rects)
            self.page.wait_for_function("document.querySelectorAll('.search-hit').length === 0", timeout=5000)
            self.assertEqual(self.js(rects), marked, label)
        self.assertEqual(self.errors, [])

    def test_reduced_motion_removes_the_mark_without_a_transition(self):
        self.open("mode", reduced_motion="reduce")
        self.js("""() => { window.testClasses = []; new MutationObserver((records) => { for (const r of records) if (r.target.classList) window.testClasses.push(r.target.className); })
          .observe(document.querySelector('main'), {subtree: true, attributes: true, attributeFilter: ['class']}); }""")
        self.search("半形")
        started = self.js("performance.now()")
        self.page.keyboard.press("Enter")
        row = self.page.locator("#page-keyboard .row.search-hit")
        row.wait_for()
        self.assertEqual(row.evaluate("(node) => getComputedStyle(node).transitionDuration"), "0s")
        self.page.wait_for_function("document.querySelectorAll('.search-hit').length === 0", timeout=5000)
        elapsed = self.js("performance.now()") - started
        self.assertGreaterEqual(elapsed, 2300)
        self.assertLess(elapsed, 6000)
        self.assertFalse([name for name in self.js("window.testClasses") if "search-hit-fade" in name])
        # 一般情況有漸變。
        self.open("mode")
        self.search("半形")
        self.page.keyboard.press("Enter")
        self.assertIn("0.6s", self.page.locator("#page-keyboard .row.search-hit").evaluate("(node) => getComputedStyle(node).transitionDuration"))
        self.assertEqual(self.errors, [])

    def test_clicking_a_result_jumps_too_and_a_disabled_item_takes_the_focus_on_its_row(self):
        self.open("mode")
        self.search("半形")
        self.page.locator('[data-result="item:ctrl-punctuation"]').click()
        self.assertEqual(self.shown_page(), ["page-keyboard"])
        row = self.page.locator("#page-keyboard .row.search-hit")
        self.assertEqual(row.locator(".label").first.text_content(), "Ctrl＋標點打全形所有符號鍵打半形時固定開啟")
        # 固定開啟、不能改：焦點在那一列上。
        self.assertTrue(self.js("document.activeElement.classList.contains('search-hit')"))
        self.assertEqual(self.page.locator('[data-focus="ctrl-punctuation"]').get_attribute("aria-checked"), "true")
        self.assertTrue(self.page.locator('[data-focus="ctrl-punctuation"]').is_disabled())
        self.assertEqual(self.errors, [])

    def test_switch_items_take_the_focus_on_the_switch(self):
        self.open("mode")
        self.search("簡中")
        self.assertEqual([row["id"] for row in self.results()], ["item:output-simplified", "item:simplified-hotkey"])
        self.page.keyboard.press("Enter")
        self.assertEqual(self.shown_page(), ["page-output"])
        self.assertEqual(self.js("[document.activeElement.dataset.focus, document.activeElement.getAttribute('role')]"), ["output-simplified", "switch"])
        self.page.keyboard.press("Space")
        self.page.wait_for_function("window.testSettings.items.find((item) => item.key === 'output-simplified').value === 'on'")
        self.assertEqual(self.errors, [])

    def test_back_link_returns_to_the_results_and_disappears_on_another_page(self):
        self.open("mode")
        self.search("半形")
        self.page.keyboard.press("ArrowDown")
        self.page.keyboard.press("Enter")
        self.assertEqual(self.shown_page(), ["page-keyboard"])
        self.page.locator("#search-back").click()
        # 回到結果頁：同一個查詢、選取停在剛才那一個，焦點在搜尋框。
        self.assertEqual(self.shown_page(), ["page-search"])
        self.assertEqual([row["selected"] for row in self.results()], ["false", "false", "true", "false"])
        self.assertEqual(self.js("document.activeElement.id"), "search-input")
        self.assertFalse(self.page.locator("#search-back").is_visible())
        # 再前往一次，然後從側邊欄換到別頁：連結消失，查詢字串還在；回到搜尋框就回到結果頁。
        self.page.keyboard.press("Enter")
        self.assertTrue(self.page.locator("#search-back").is_visible())
        self.page.locator('.nav[data-page="output"]').click()
        self.assertEqual(self.shown_page(), ["page-output"])
        self.assertFalse(self.page.locator("#search-back").is_visible())
        self.assertEqual(self.page.locator(".search-hit").count(), 0)
        self.assertEqual(self.page.locator("#search-input").input_value(), "半形")
        self.page.locator("#search-input").click()
        self.assertEqual(self.shown_page(), ["page-search"])
        # 在結果頁按側邊欄：直接去那一頁。
        self.page.locator('.nav[data-page="keyboard"]').click()
        self.assertEqual(self.shown_page(), ["page-keyboard"])
        self.assertEqual(self.nav_state()["keyboard"], {"current": "page", "dim": False, "count": ""})
        self.assertFalse(self.page.locator("#search-back").is_visible())
        # 清除搜尋時連結也消失。
        self.shortcut()
        self.page.keyboard.press("Enter")
        self.assertTrue(self.page.locator("#search-back").is_visible())
        self.page.locator("#search-clear").click()
        self.assertFalse(self.page.locator("#search-back").is_visible())
        self.assertEqual(self.page.locator("#search-input").input_value(), "")
        self.assertEqual(self.errors, [])

    def test_escape_clears_and_returns_to_the_page_scroll_and_focus_from_before(self):
        self.open("candidates")
        self.page.locator('[data-focus="personal-learning"]').focus()
        self.js("document.querySelector('main').scrollTop = 240")
        self.search("簡體")
        self.assertEqual(self.shown_page(), ["page-search"])
        self.assertEqual(self.js("document.querySelector('main').scrollTop"), 0)
        self.page.keyboard.press("Escape")
        # 清除、回到搜尋前的那一頁（捲到的位置也一樣），焦點還在搜尋框。
        self.assertEqual(self.page.locator("#search-input").input_value(), "")
        self.assertEqual(self.shown_page(), ["page-candidates"])
        self.assertEqual(self.js("document.querySelector('main').scrollTop"), 240)
        self.assertEqual(self.js("document.activeElement.id"), "search-input")
        self.assertEqual(self.nav_state()["candidates"]["current"], "page")
        self.assertTrue(self.page.locator("#search-key").is_visible())
        self.assertFalse(self.page.locator("#search-clear").is_visible())
        # 已經是空的：Esc 把焦點還給原本的位置。
        self.page.keyboard.press("Escape")
        self.assertEqual(self.js("document.activeElement.dataset.focus"), "personal-learning")
        # 把字刪光也一樣回到原本那一頁。
        self.search("簡")
        self.assertEqual(self.shown_page(), ["page-search"])
        self.page.keyboard.press("Backspace")
        self.assertEqual(self.shown_page(), ["page-candidates"])
        # 清除鈕：清除並留在搜尋框。
        self.page.keyboard.type("簡體")
        self.page.locator("#search-clear").click()
        self.assertEqual(self.shown_page(), ["page-candidates"])
        self.assertEqual(self.js("[document.activeElement.id, document.activeElement.value]"), ["search-input", ""])
        self.assertEqual(self.page.locator("#search-clear").get_attribute("tabindex"), "-1")
        self.assertEqual(self.errors, [])

    def test_composition_keys_belong_to_the_input_method(self):
        # 用輸入法在搜尋框打中文：組字中的注音不搜（不閃「沒有符合的設定」），組字中的 ↑↓、Enter、Esc 是輸入法的。
        self.open("keyboard")
        self.search("半形")
        self.page.keyboard.press("ArrowDown")
        state = self.js("""() => {
          const input = document.getElementById('search-input');
          const fire = (key) => { const event = new KeyboardEvent('keydown', {key, isComposing: true, bubbles: true, cancelable: true}); input.dispatchEvent(event); return event.defaultPrevented; };
          input.dispatchEvent(new CompositionEvent('compositionstart', {bubbles: true}));
          input.value = '半形ㄈㄨ';
          input.dispatchEvent(new InputEvent('input', {bubbles: true, isComposing: true}));
          const during = {title: document.getElementById('search-title').textContent, prevented: ['ArrowDown', 'ArrowUp', 'Enter', 'Escape'].map(fire),
            selected: document.querySelector('[role=option][aria-selected=true]').id, shown: !document.getElementById('page-search').hidden, value: input.value};
          input.value = '半形符';
          input.dispatchEvent(new CompositionEvent('compositionend', {bubbles: true, data: '符'}));
          return {during, after: document.getElementById('search-title').textContent};
        }""")
        self.assertEqual(state["during"], {"title": "搜尋「半形」", "prevented": [False, False, False, False], "selected": "search-result-2", "shown": True, "value": "半形ㄈㄨ"})
        self.assertEqual(state["after"], "搜尋「半形符」")
        self.assertEqual(self.errors, [])

    # ---- nothing found ----
    def test_nothing_found_offers_other_words_and_the_guide(self):
        self.open("keyboard")
        self.search("字體大小")
        self.assertEqual(self.page.locator("#search-title").text_content(), "搜尋「字體大小」")
        self.assertEqual(self.page.locator("#search-count").text_content(), "沒有符合的設定。")
        self.assertFalse(self.page.locator("#search-results").is_visible())
        self.assertIsNone(self.page.locator("#search-input").get_attribute("aria-activedescendant"))
        card = self.page.locator(".search-empty")
        self.assertTrue(card.is_visible())
        self.assertEqual(self.js("Array.from(document.querySelectorAll('.search-empty-title')).map((node) => node.textContent)"), ["換個說法試試", "還是找不到？"])
        self.assertEqual(self.js("Array.from(document.querySelectorAll('.search-chip')).map((node) => node.textContent)"), SUGGESTIONS)
        self.assertEqual(self.js("Array.from(document.querySelectorAll('.search-keys > span')).map((node) => node.textContent)"), ["Esc 清除，回到原本那一頁"])
        self.assertEqual({name for name, state in self.nav_state().items() if state["dim"]}, set(NAV))
        # Enter 沒有東西可以前往：留在原地。
        self.page.keyboard.press("Enter")
        self.assertEqual(self.shown_page(), ["page-search"])
        # 官網設定說明用系統瀏覽器開（open_url）。
        self.page.locator(".search-more button").click()
        self.assertEqual(self.js("window.testOpened"), [GUIDE])
        # 每個建議的詞都真的找得到東西；點了就搜那個詞。
        for word in SUGGESTIONS:
            self.search("字體大小")
            self.page.locator(f'.search-chip[data-suggest="{word}"]').click()
            self.assertEqual(self.page.locator("#search-input").input_value(), word)
            self.assertEqual(self.page.locator("#search-title").text_content(), f"搜尋「{word}」")
            self.assertGreater(len(self.results()), 0, word)
            self.assertEqual(self.js("document.activeElement.id"), "search-input")
            self.assertFalse(card.is_visible())
        self.assertEqual(self.errors, [])

    # ---- pages and merged items ----
    def test_pages_without_items_are_results_themselves(self):
        self.open("keyboard")
        for query, want in (("匯出", "page:vocab"), ("加詞", "page:vocab"), ("清除學習紀錄", "page:vocab"), ("下載模型", "page:models"), ("神經", "page:models"),
                            ("回報", "page:troubleshoot"), ("匯出狀態", "page:troubleshoot"), ("升級", "page:update"), ("自動更新", "page:update"),
                            ("風格", "page:styles"), ("小麥", "page:styles"), ("關於", "page:about"), ("總覽", "page:overview")):
            self.search(query)
            self.assertIn(want, [row["id"] for row in self.results()], query)
        # 「更新」：更新頁本身，加上更新方式（照一般項目，值是目前的方式，選項也搜得到）。
        self.search("更新")
        rows = {row["id"]: row for row in self.results()}
        self.assertIn("page:update", rows)
        self.assertEqual(rows["update-mode"]["value"], "自動更新（建議）")
        self.assertEqual(self.nav_state()["update"]["count"], "2")
        self.search("手動")
        self.assertEqual([(row["id"], row["lines"][-1]) for row in self.results()], [("update-mode", ["options", "選項：手動更新"])])
        # 前往更新方式：焦點在下拉選單上。
        self.page.keyboard.press("Enter")
        self.assertEqual(self.shown_page(), ["page-update"])
        self.assertEqual(self.js("document.activeElement.id"), "update-mode")
        self.assertEqual(self.page.locator("#page-update .row.search-hit").count(), 1)
        # 前往一整頁：沒有要標的項目，焦點在頁首標題上（下一個 Tab 從這一頁開始），上方一樣有回到結果的連結。
        self.search("回報")
        self.page.keyboard.press("Enter")
        self.assertEqual(self.shown_page(), ["page-troubleshoot"])
        self.assertEqual(self.js("[document.activeElement.tagName, document.activeElement.textContent]"), ["H1", "問題排解"])
        self.assertEqual(self.page.locator(".search-hit").count(), 0)
        self.assertTrue(self.page.locator("#search-back").is_visible())
        self.page.keyboard.press("Tab")
        self.assertEqual(self.js("document.activeElement.dataset.focus"), "export")
        # 不搜尋「我的詞彙」裡使用者自己的詞：搜尋沒有呼叫詞彙的指令。
        self.assertNotIn("vocab_request", self.js("window.testCommands"))
        self.assertEqual(self.errors, [])

    def test_the_four_settings_pages_are_results_by_their_names_only(self):
        # 中英切換、選字、鍵盤與符號、輸出設定：搜頁名時那一頁本身是一筆結果，排在該頁分組的最前面，側邊欄的筆數照實算；
        # 前往就是切到那一頁，焦點在頁首標題上（同其他頁面結果）。
        titles = {"mode": "中英切換", "candidates": "選字", "keyboard": "鍵盤與符號", "output": "輸出設定"}
        self.open("about", wait="h1")
        self.page.wait_for_function("window.testCommands.includes('get_settings')")
        for name, title in titles.items():
            self.search(title)
            rows = self.results()
            groups = self.js("""() => Array.from(document.querySelectorAll('#search-results [role=group]')).map((group) =>
              [group.querySelector('.search-group').textContent, Array.from(group.querySelectorAll('[role=option]')).map((node) => node.dataset.result)])""")
            own = dict(groups)[title]
            self.assertEqual(own[0], "page:" + name, title)   # 分組的最前面
            self.assertEqual(sum(1 for row in rows if row["id"] == "page:" + name), 1)
            row = next(row for row in rows if row["id"] == "page:" + name)
            self.assertEqual((row["name"], row["value"], row["reason"]), (title, "", ""))
            self.assertEqual(row["marks"], [title])            # 只標頁名
            self.assertEqual([kind for kind, _ in row["lines"]], ["note"])   # 頁首那一句：只顯示
            self.assertEqual(self.nav_state()[name]["count"], str(len(own)), title)
            self.assertEqual(self.page.locator("#search-count").text_content(), f"找到 {len(rows)} 個設定。")
        # 「鍵盤與符號」「輸出設定」沒有別的項目寫到整個頁名：就這一筆。「中英切換」另外找到中英切換鍵與單按 Shift 的時限，
        # 頁面那一筆排第一。都是預設選取第一筆，Enter 前往那一頁。
        for name in ("keyboard", "output", "mode"):
            title = titles[name]
            self.search(title)
            rows = [(row["id"], row["selected"]) for row in self.results()]
            self.assertEqual(rows[0], ("page:" + name, "true"), title)
            if name == "mode":
                self.assertEqual([row for row, _ in rows], ["page:mode", "item:toggle-key", "item:shift-tap-ms"])
            else:
                self.assertEqual(len(rows), 1, title)
            self.assertEqual({page: state["count"] for page, state in self.nav_state().items() if state["count"]}, {name: str(len(rows))})
            self.page.keyboard.press("Enter")
            self.assertEqual(self.shown_page(), ["page-" + name])
            self.assertEqual(self.js("[document.activeElement.tagName, document.activeElement.textContent]"), ["H1", title])
            self.assertEqual(self.page.locator(".search-hit").count(), 0)
            self.assertEqual(self.page.locator("#search-back").text_content(), f"回到「{title}」的搜尋結果")
            self.assertEqual(self.nav_state()[name]["current"], "page")
            # 下一個 Tab 從這一頁的第一個控制項開始。
            self.page.keyboard.press("Tab")
            self.assertTrue(self.js("(page) => document.getElementById('page-' + page).contains(document.activeElement)", name), title)
        # 「選字」很多項目的說明都有：頁面那一筆在「選字」分組最前面，其他照舊。
        self.search("選字")
        ids = [row["id"] for row in self.results()]
        self.assertEqual(ids.count("page:candidates"), 1)
        self.assertIn("item:mixed-input", ids)        # 中英切換頁的項目排在前面的分組
        self.assertLess(ids.index("item:mixed-input"), ids.index("page:candidates"))
        self.assertLess(ids.index("page:candidates"), ids.index("item:selection"))
        # 只比對頁名：頁首那一句裡的字不會讓那一頁多出一筆。
        for query, page in (("標點符號", "keyboard"), ("Emoji", "keyboard"), ("轉成簡體字", "output"), ("輸入法選單", "output"), ("模糊音", "candidates"), ("用哪個鍵", "mode"), ("半形", "keyboard"), ("候選", "candidates")):
            self.search(query)
            self.assertNotIn("page:" + page, [row["id"] for row in self.results()], query)
        for query in ("怎麼打", "輸入法選單", "用哪個鍵"):
            self.search(query)
            self.assertEqual(self.results(), [], query)
            self.assertEqual(self.page.locator("#search-count").text_content(), "沒有符合的設定。")
        # 頁名的一部分也算（比對的是頁名這個欄位）；不分全半形。
        self.search("鍵盤與")
        self.assertEqual([row["id"] for row in self.results()], ["page:keyboard"])
        self.assertEqual(self.errors, [])

    def test_fuzzy_is_one_result_for_its_one_switch(self):
        self.open("keyboard")
        for query in ("捲舌", "ㄓㄗ", "鼻音", "模糊音", "近音"):
            self.search(query)
            self.assertEqual([row["id"] for row in self.results()], ["fuzzy"], query)
        self.search("捲舌")
        row = self.results()[0]
        self.assertEqual((row["name"], row["value"]), ("模糊音", "開啟"))
        self.assertEqual(row["lines"][-1], ["keywords", "也可以找：捲舌、ㄓㄗ、ㄔㄘ、ㄕㄙ、鼻音、ㄣㄥ"])
        self.page.keyboard.press("Enter")
        self.assertEqual(self.shown_page(), ["page-candidates"])
        self.assertEqual(self.js("document.activeElement.dataset.focus"), "fuzzy")
        self.assertEqual(self.page.locator("#page-candidates .row.search-hit .label").text_content(), "模糊音")
        self.assertEqual(self.errors, [])

    def test_old_updater_without_keywords_still_searches_names_and_descriptions(self):
        old = json.loads(json.dumps(self.settings))
        for item in old["items"]:
            item.pop("keywords", None)
        self.open("keyboard", old)
        self.search("半形")
        self.assertEqual([row["id"] for row in self.results()], ["item:shift-symbol-halfwidth", "item:cjk-latin-space", "item:style-shift-space"])
        self.search("簡中")
        self.assertEqual(self.results(), [])
        self.assertEqual(self.page.locator("#search-count").text_content(), "沒有符合的設定。")
        self.search("簡體")
        self.assertEqual([row["id"] for row in self.results()], ["item:output-simplified", "item:simplified-hotkey"])
        self.assertEqual(self.errors, [])

    def test_f1_and_help_links_are_untouched(self):
        # #362：「說明」連結不在 Tab 順序、F1 開說明。搜尋框有焦點、沒在搜尋時，F1 照舊開那一頁的說明；結果頁上不開。
        self.open("update", wait=".lead [data-help]")
        self.page.keyboard.press("Tab")
        self.assertEqual(self.js("document.activeElement.id"), "search-input")
        self.page.keyboard.press("F1")
        self.assertEqual(self.js("window.testOpened"), [GUIDE + "#update-mode"])
        self.page.keyboard.type("半形")
        self.page.keyboard.press("F1")
        self.assertEqual(self.js("window.testOpened"), [GUIDE + "#update-mode"])
        self.page.keyboard.press("Enter")
        self.assertEqual(self.page.locator('#page-keyboard [data-help="ctrl-punctuation"]').get_attribute("tabindex"), "-1")
        self.assertEqual(self.errors, [])

    # ---- themes ----
    def test_dark_theme_uses_the_dark_tokens(self):
        self.open("keyboard", color_scheme="dark")
        self.search("半形")
        colors = self.js("""() => { const c = (selector, property) => getComputedStyle(document.querySelector(selector))[property]; return {
          body: c('body', 'backgroundColor'), selected: c('[role=option][aria-selected=true]', 'backgroundColor'), outline: c('[role=option][aria-selected=true]', 'outlineColor'),
          mark: [c('mark', 'backgroundColor'), c('mark', 'color')], line: c('[role=option][aria-selected=true] .result-line', 'color'),
          other: c('[role=option][aria-selected=false] .result-line', 'color'), value: c('[role=option][aria-selected=true] .result-value', 'backgroundColor'),
          reason: [c('.result-reason', 'backgroundColor'), c('.result-reason', 'color')], input: [c('#search-input', 'backgroundColor'), c('#search-input', 'borderTopColor'), c('#search-input', 'color')],
          count: [c('.nav-count', 'backgroundColor'), c('.nav-count', 'color')], dim: c('.nav.nav-dim', 'color'), clear: [c('#search-clear', 'backgroundColor'), c('#search-clear', 'color')],
        }; }""")
        self.assertEqual(colors, {
            "body": "rgb(18, 24, 27)", "selected": "rgb(25, 52, 60)", "outline": "rgb(82, 196, 220)",
            "mark": ["rgb(106, 82, 0)", "rgb(255, 242, 194)"], "line": "rgb(180, 192, 197)", "other": "rgb(154, 168, 174)", "value": "rgb(18, 24, 27)",
            "reason": ["rgb(58, 44, 16)", "rgb(241, 185, 92)"], "input": ["rgb(18, 24, 27)", "rgb(82, 196, 220)", "rgb(231, 236, 238)"],
            "count": ["rgb(25, 52, 60)", "rgb(82, 196, 220)"], "dim": "rgb(154, 168, 174)", "clear": ["rgb(25, 52, 60)", "rgb(82, 196, 220)"],
        })
        self.page.keyboard.press("ArrowDown")
        self.page.keyboard.press("Enter")
        row = self.page.locator("#page-keyboard .row.search-hit")
        self.assertEqual(row.evaluate("(node) => [getComputedStyle(node).backgroundColor, getComputedStyle(node).outlineColor, getComputedStyle(node.querySelector('.desc')).color]"),
                         ["rgb(25, 52, 60)", "rgb(82, 196, 220)", "rgb(180, 192, 197)"])
        self.assertEqual(self.errors, [])

    def test_light_theme_matches_the_design_tokens(self):
        self.open("keyboard", color_scheme="light")
        self.search("半形")
        colors = self.js("""() => { const c = (selector, property) => getComputedStyle(document.querySelector(selector))[property]; return {
          selected: c('[role=option][aria-selected=true]', 'backgroundColor'), mark: [c('mark', 'backgroundColor'), c('mark', 'color')],
          value: [c('[role=option][aria-selected=true] .result-value', 'backgroundColor'), c('[role=option][aria-selected=false] .result-value', 'backgroundColor')],
          input: [c('#search-input', 'backgroundColor'), c('#search-input', 'borderTopColor')], group: [c('.search-group', 'fontSize'), c('.search-group', 'color')],
        }; }""")
        self.assertEqual(colors, {"selected": "rgb(226, 240, 244)", "mark": ["rgb(255, 224, 138)", "rgb(27, 36, 40)"], "value": ["rgb(255, 255, 255)", "rgb(244, 246, 247)"],
                                  "input": ["rgb(255, 255, 255)", "rgb(0, 92, 115)"], "group": ["13px", "rgb(90, 102, 109)"]})
        # 沒有焦點時：1px 的邊線、頁面底色。
        self.page.locator('.nav[data-page="about"]').focus()
        self.assertEqual(self.js("(() => { const s = getComputedStyle(document.getElementById('search-input')); return [s.backgroundColor, s.borderTopColor, s.borderTopWidth, s.boxShadow]; })()"),
                         ["rgb(244, 246, 247)", "rgb(195, 205, 210)", "1px", "none"])
        self.assertEqual(self.errors, [])


class ChromiumSearchTests(SearchTests, unittest.TestCase):
    """WebView2 (Windows) is Chromium."""
    launch = staticmethod(launch_chromium)


class WebKitSearchTests(SearchTests, unittest.TestCase):
    """WebKitGTK (Linux) and WKWebView (macOS) are WebKit."""
    launch = staticmethod(launch_webkit)


if __name__ == '__main__':
    unittest.main()
