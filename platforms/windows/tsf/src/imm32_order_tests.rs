//! #260：IMM32 程式（系統 EDIT、IMM32 模式的 RichEdit、自繪文字框）經系統相容層（CUAS）接 TSF，提交是
//! WM_IME_COMPOSITION 的結果字串，排在程式自己的按鍵訊息之後：TestKeyDown 先送出英文單字再把空白交給程式，
//! 程式先打出空白（TranslateMessage 的 WM_CHAR）、之後才收到單字（.14 的訊息紀錄）；方向鍵先移動插入點，單字插到別處。
//! 以 edit.rs 的假文件當系統相容層的文字儲存，App 照這個順序模擬程式看到的文字：交給程式的鍵先作用，
//! 這一鍵期間送出的結果字串之後才插入。需要英文自動補字開著的測試引擎：ZUIN_TEST_ENGLISH_PIPE 或
//! ZUIN_TEST_CAPS_LOWER_PIPE（#112，同樣開著英文自動補字）；都沒設就略過。
//!
//! #262：同一個順序讓組字中的 Ctrl 組合鍵（Ctrl+A、Ctrl+S、Ctrl+Home）比組字先作用：外殼在 A 的 TestKeyDown 才送出
//! 組字，程式先全選／存檔、之後才收到組字。注音組字的測試用一般的 ZUIN_TEST_PIPE。
use super::*;
use crate::cuas::{Complete, Cuas};
use crate::edit::tests::{host, Doc, Sync};
use windows::Win32::UI::Input::KeyboardAndMouse::{
    GetKeyboardState, MapVirtualKeyW, SetKeyboardState, MAPVK_VK_TO_VSC, VK_CAPITAL, VK_CONTROL, VK_LCONTROL,
    VK_LMENU, VK_LSHIFT, VK_LWIN, VK_MENU, VK_SHIFT,
};
use std::collections::HashMap;
use windows::Win32::UI::TextServices::{ITfKeyEventSink, TS_SS_TRANSITORY};
use windows::Win32::UI::WindowsAndMessaging::{
    CreateCaret, CreateWindowExW, DestroyCaret, DestroyWindow, HMENU, WINDOW_EX_STYLE, WS_POPUP,
};
use windows_core::w;

fn engine() -> Option<String> {
    std::env::var("ZUIN_TEST_ENGLISH_PIPE")
        .or_else(|_| std::env::var("ZUIN_TEST_CAPS_LOWER_PIPE"))
        .ok()
}

/// 沒有 Shift、沒有 Caps Lock（只改本執行緒的鍵盤狀態）。
fn plain_keyboard() {
    let mut state = [0u8; 256];
    unsafe {
        GetKeyboardState(&mut state).unwrap();
        state[VK_CAPITAL.0 as usize] = 0;
        state[VK_SHIFT.0 as usize] = 0;
        SetKeyboardState(&state).unwrap();
    }
}

/// 按著或放開 Ctrl（只改本執行緒的鍵盤狀態，GetKeyState 看得到）。
fn hold_ctrl(down: bool) {
    let mut state = [0u8; 256];
    unsafe {
        GetKeyboardState(&mut state).unwrap();
        let v = if down { 0x80 } else { 0 };
        state[VK_CONTROL.0 as usize] = v;
        state[VK_LCONTROL.0 as usize] = v;
        SetKeyboardState(&state).unwrap();
    }
}

/// 一次設定四個修飾鍵（只改本執行緒的鍵盤狀態，GetKeyState 看得到）。
fn set_mods(ctrl: bool, alt: bool, win: bool, shift: bool) {
    let mut state = [0u8; 256];
    unsafe {
        GetKeyboardState(&mut state).unwrap();
        for (v, on) in [
            (VK_CONTROL.0, ctrl),
            (VK_LCONTROL.0, ctrl),
            (VK_MENU.0, alt),
            (VK_LMENU.0, alt),
            (VK_LWIN.0, win),
            (VK_SHIFT.0, shift),
            (VK_LSHIFT.0, shift),
        ] {
            state[v as usize] = if on { 0x80 } else { 0 };
        }
        SetKeyboardState(&state).unwrap();
    }
}

/// 程式那一端：文字與插入點（以字元計）。
struct App {
    text: Vec<char>,
    caret: usize,
    /// 程式的快捷鍵（Ctrl+S 存檔、Ctrl+A 全選）作用當下看到的全文（#262）。
    seen: Vec<String>,
    /// 每個快捷鍵作用一次記一筆：(vk, 當下全文)（#66，重送的快捷鍵要作用剛好一次）。
    fired: Vec<(u32, String)>,
}

impl App {
    fn new() -> App {
        App { text: "前".chars().collect(), caret: 1, seen: Vec::new(), fired: Vec::new() }
    }
    fn insert(&mut self, s: &str) {
        for c in s.chars() {
            self.text.insert(self.caret, c);
            self.caret += 1;
        }
    }
    fn text(&self) -> String {
        self.text.iter().collect()
    }
}

/// 程式自己處理這一鍵會做的事。
#[derive(Clone, Copy)]
enum Act {
    Char(char),
    Left,
    /// 修飾鍵本身：程式什麼都不做。
    Nothing,
    /// Ctrl 組合鍵：程式的快捷鍵讀全文（#262）。
    Shortcut,
    /// Ctrl+Home：插入點移到最前面。
    Top,
}

/// 程式怎麼處理系統相容層送來的結果字串（#66）。
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum HostKind {
    /// 自己處理 WM_IME_COMPOSITION，結果字串當場就到。
    SelfHandles,
    /// 交給 DefWindowProc：結果字串由相容層 post 成 WM_CHAR，排在佇列裡。
    DefWindowProc,
}

/// 程式的訊息佇列裡等著的訊息（先進先出）。
#[derive(Clone, Debug, PartialEq, Eq)]
enum Queued {
    Char { hwnd: HWND, text: String },
    Key { hwnd: HWND, vk: u32, lparam: isize },
}

/// 假的系統相容層宿主（#66）：先進先出的訊息佇列，加上可設定的行為。`Char` 一律排給 `complete` 收到的那個 hwnd，
/// 不是當下的 `focus`。`NoContext`／`Failed` 不產生結果字串、也不終止組字。
struct FakeCuas {
    doc: Rc<Doc>,
    /// 外殼本身（取 OnCompositionTerminated 的 sink 與組字）。service 與 cuas 互相持有，測試裡洩漏可以接受。
    service: RefCell<Option<ComObject<TextService>>>,
    queue: RefCell<Vec<Queued>>,
    kind: Cell<HostKind>,
    focus: Cell<HWND>,
    complete_result: Cell<Complete>,
    /// Notified 時相容層是否真的終止組字。
    terminates: Cell<bool>,
    repost_ok: Cell<bool>,
    focus_after_complete: Cell<Option<HWND>>,
    hotkey: Cell<bool>,
    /// 外殼自己 EndComposition 時，相容層仍把結果字串排給 `complete` 收到的 hwnd（真實 CUAS 裡 #262 那種晚到的結果字串）。
    result_on_self_end: Cell<bool>,
    /// 每次 `complete` 當下文件裡組字區的文字，與外殼最後送給引擎的請求 type。
    completions: RefCell<Vec<(String, Option<String>)>>,
    /// `SelfHandles` 當場交給程式、等 Host 插進 App 的結果字串。
    handed: RefCell<Vec<String>>,
    /// 經相容層送出的文字：Host 不得再從 `doc.committed()` 的差異重複插入。
    via: RefCell<Vec<String>>,
    /// `result_on_self_end` 等外殼自己結束組字的結果字串。
    late: RefCell<Option<(HWND, String)>>,
    /// 相容層結束組字時不經 OnCompositionTerminated（像 Deactivate 或重入的 present 那樣自己清掉組字狀態，#66）。
    silent_end: Cell<bool>,
    /// 結果字串改排給這個視窗（不是 `complete` 收到的 hwnd）：模擬 view 與焦點視窗不一致時字落在焦點視窗（#66）。
    deliver_to: Cell<Option<HWND>>,
    /// 所有排進佇列的訊息，依序（Host 不會清它）：驗證 `Char` 在 `Key` 之前。
    history: RefCell<Vec<Queued>>,
}

fn app_hwnd() -> HWND {
    HWND(0x1000 as *mut std::ffi::c_void)
}

impl FakeCuas {
    fn new(doc: Rc<Doc>) -> Rc<FakeCuas> {
        Rc::new(FakeCuas {
            doc,
            service: RefCell::new(None),
            queue: RefCell::new(Vec::new()),
            kind: Cell::new(HostKind::SelfHandles),
            focus: Cell::new(app_hwnd()),
            complete_result: Cell::new(Complete::Notified),
            terminates: Cell::new(true),
            repost_ok: Cell::new(true),
            focus_after_complete: Cell::new(None),
            hotkey: Cell::new(false),
            result_on_self_end: Cell::new(false),
            completions: RefCell::new(Vec::new()),
            handed: RefCell::new(Vec::new()),
            via: RefCell::new(Vec::new()),
            late: RefCell::new(None),
            silent_end: Cell::new(false),
            deliver_to: Cell::new(None),
            history: RefCell::new(Vec::new()),
        })
    }

    fn deliver(&self, hwnd: HWND, text: String) {
        self.via.borrow_mut().push(text.clone());
        match self.kind.get() {
            HostKind::SelfHandles => self.handed.borrow_mut().push(text),
            HostKind::DefWindowProc => {
                self.history.borrow_mut().push(Queued::Char { hwnd, text: text.clone() });
                self.queue.borrow_mut().push(Queued::Char { hwnd, text });
            }
        }
    }
}

impl Cuas for FakeCuas {
    fn focus(&self) -> HWND {
        self.focus.get()
    }

    fn complete(&self, hwnd: HWND) -> Complete {
        let hwnd = self.deliver_to.get().unwrap_or(hwnd);
        let text = self.doc.composing_text();
        let service = self.service.borrow().clone().expect("FakeCuas 要先接上外殼");
        let sent = service.state.borrow().session.sent.last().cloned();
        self.completions.borrow_mut().push((text.clone(), sent));
        let result = self.complete_result.get();
        if let Some(focus) = self.focus_after_complete.get() {
            // 相容層完成組字時焦點跳走：外殼真正的焦點變化入口（OnSetFocus，最後走到 finish）在組字還在時同步被呼叫。
            self.focus.set(focus);
            let sink: ITfThreadMgrEventSink = service.to_interface();
            unsafe { sink.OnSetFocus(None::<&ITfDocumentMgr>, None::<&ITfDocumentMgr>) }.unwrap();
        }
        if result == Complete::Notified && self.terminates.get() && self.silent_end.get() {
            // 結果字串照送，但外殼的組字狀態是別的路徑清掉的：沒有 OnCompositionTerminated。
            self.doc.end_composition();
            if !text.is_empty() {
                self.deliver(hwnd, text);
            }
            let mut st = service.state.borrow_mut();
            st.composition = None;
            st.composing = false;
        } else if result == Complete::Notified && self.terminates.get() {
            let composition = service.state.borrow().composition.clone();
            self.doc.end_composition();
            if !text.is_empty() {
                self.deliver(hwnd, text);
            }
            if let Some(composition) = composition {
                // 同步回呼外殼：這時外殼不得持有 state 的借用。
                let sink: ITfCompositionSink = service.to_interface();
                unsafe { sink.OnCompositionTerminated(0, &composition) }.unwrap();
            }
        } else if self.result_on_self_end.get() && !text.is_empty() {
            // NoContext、Failed、不終止的 Notified 都一樣：結果字串等外殼自己結束組字才排進佇列。
            *self.late.borrow_mut() = Some((hwnd, text));
        }
        result
    }

    fn has_char(&self, hwnd: HWND) -> bool {
        self.queue.borrow().iter().any(|q| matches!(q, Queued::Char { hwnd: h, .. } if *h == hwnd))
    }

    fn repost(&self, hwnd: HWND, vk: u32, lparam: isize) -> bool {
        if self.repost_ok.get() {
            self.history.borrow_mut().push(Queued::Key { hwnd, vk, lparam });
            self.queue.borrow_mut().push(Queued::Key { hwnd, vk, lparam });
        }
        self.repost_ok.get()
    }

    fn ime_hotkey(&self, _vk: u32) -> bool {
        self.hotkey.get()
    }
}

struct Host {
    service: ComObject<TextService>,
    sink: ITfKeyEventSink,
    ctx: ITfContext,
    doc: Rc<Doc>,
    app: App,
    /// IMM32：結果字串排在程式自己的訊息之後。false 是自己接 TSF 的程式：送出當下就在文件裡。
    imm32: bool,
    cuas: Rc<FakeCuas>,
    /// 這個 App 視窗；給別的視窗的 `Char` 不插進 App，只記在 `other_chars`。
    hwnd: HWND,
    other_chars: Vec<(HWND, String)>,
    /// 重送給別的視窗的按鍵，不重播進這個 App。
    other_keys: Vec<(HWND, u32, isize)>,
    /// 每個 vk 最近一次按下時程式自己處理的動作（重送回來的鍵照這個動作）。
    acts: HashMap<u32, Act>,
}

impl Host {
    /// 一次按鍵：TestKeyDown 說不吃就交給程式（OnKeyDown 不會來），說吃才有 OnKeyDown，
    /// 最後依序排空程式的訊息佇列（相容層 post 的 WM_CHAR、重送的按鍵）。回傳 TestKeyDown 的答案。
    fn press(&mut self, vk: u32, scan: isize, act: Act) -> bool {
        self.acts.insert(vk, act);
        let eaten = self.step(vk, 1 | scan << 16, act, true);
        self.drain();
        eaten
    }
    /// TestKeyDown 吃下之後不呼叫 OnKeyDown（#66：模擬 TSF 沒有接著送 OnKeyDown 的情況）。
    #[allow(dead_code)] // 後續的測試才用（#66）
    fn press_without_keydown(&mut self, vk: u32, scan: isize, act: Act) -> bool {
        self.acts.insert(vk, act);
        let eaten = self.step(vk, 1 | scan << 16, act, false);
        self.drain();
        eaten
    }
    #[allow(dead_code)] // 後續的測試才用（#66）
    /// 對同一則按鍵再呼叫一次 TestKeyDown（不呼叫 OnKeyDown、程式也不再處理）。
    fn retest(&mut self, vk: u32, scan: isize) -> bool {
        let eaten = self.step(vk, 1 | scan << 16, Act::Nothing, false);
        self.drain();
        eaten
    }
    /// 一則按鍵訊息：TestKeyDown →（吃下才）OnKeyDown → 套用結果 → 不吃的鍵由程式處理。
    fn step(&mut self, vk: u32, lparam: isize, act: Act, keydown: bool) -> bool {
        let (w, l) = (WPARAM(vk as usize), LPARAM(lparam));
        self.cuas.via.borrow_mut().clear(); // 上一則留下的紀錄不得吃掉這一則的差異
        let before = self.doc.committed().chars().count();
        let eaten = unsafe { self.sink.OnTestKeyDown(&self.ctx, w, l) }.unwrap().as_bool();
        if eaten && keydown {
            let key = unsafe { self.sink.OnKeyDown(&self.ctx, w, l) }.unwrap().as_bool();
            assert!(key, "TestKeyDown 說吃，OnKeyDown 也要吃 vk={vk:#x}");
        }
        // 相容層當場交給程式的結果字串（SelfHandles）：程式在這一鍵之前就收到。
        for text in std::mem::take(&mut *self.cuas.handed.borrow_mut()) {
            self.app.insert(&text);
        }
        // 外殼自己結束了組字：結果字串只走佇列（晚到的 Char），文件差異不再插入一次。
        if !self.doc.composing() {
            if let Some((hwnd, text)) = self.cuas.late.borrow_mut().take() {
                self.cuas.via.borrow_mut().push(text.clone());
                self.cuas.history.borrow_mut().push(Queued::Char { hwnd, text: text.clone() });
                self.cuas.queue.borrow_mut().push(Queued::Char { hwnd, text });
            }
        }
        let mut result: String = self.doc.committed().chars().skip(before).collect();
        // 經相容層送出的文字已經照它自己的路徑交給程式，不從文件差異重複插入。
        for text in std::mem::take(&mut *self.cuas.via.borrow_mut()) {
            result = result.replacen(&text, "", 1);
        }
        if eaten || !self.imm32 {
            self.app.insert(&result);
        }
        if !eaten {
            match act {
                Act::Char(c) => self.app.insert(&c.to_string()),
                Act::Left => self.app.caret = self.app.caret.saturating_sub(1),
                Act::Nothing => {}
                Act::Shortcut => {
                    self.app.seen.push(self.app.text());
                    self.app.fired.push((vk, self.app.text()));
                }
                Act::Top => self.app.caret = 0,
            }
            if self.imm32 {
                self.app.insert(&result);
            }
        }
        eaten
    }
    /// 依序排空程式的訊息佇列：給這個視窗的 `Char` 插進 App，`Key` 以同一個 vk、lParam 再走一次 TestKeyDown，
    /// 回答不吃才由程式處理（動作作用一次）。
    fn drain(&mut self) {
        loop {
            let next = {
                let mut queue = self.cuas.queue.borrow_mut();
                if queue.is_empty() { None } else { Some(queue.remove(0)) }
            };
            match next {
                None => break,
                Some(Queued::Char { hwnd, text }) if hwnd == self.hwnd => self.app.insert(&text),
                Some(Queued::Char { hwnd, text }) => self.other_chars.push((hwnd, text)),
                Some(Queued::Key { hwnd, vk, lparam }) if hwnd != self.hwnd => self.other_keys.push((hwnd, vk, lparam)),
                Some(Queued::Key { vk, lparam, .. }) => {
                    let act = self.acts.get(&vk).copied().unwrap_or(Act::Nothing);
                    self.step(vk, lparam, act, true);
                }
            }
        }
    }
    fn letters(&mut self, s: &str) {
        for c in s.chars() {
            let vk = c.to_ascii_uppercase() as u32;
            let scan = unsafe { MapVirtualKeyW(vk, MAPVK_VK_TO_VSC) } as isize;
            assert!(self.press(vk, scan, Act::Char(c)), "字母 {c} 開始或接著英文單字");
        }
    }
    fn space(&mut self) -> bool {
        self.press(0x20, 0x39, Act::Char(' '))
    }
    fn comma(&mut self) -> bool {
        self.press(0xBC, 0x33, Act::Char(','))
    }
    fn one(&mut self) -> bool {
        self.press(0x31, 0x02, Act::Char('1'))
    }
    fn left(&mut self) -> bool {
        self.press(0x25, 0x14b, Act::Left)
    }
    fn pending(&self) -> bool {
        self.service.state.borrow().session.english_state.english_pending
    }
    fn composing(&self) -> bool {
        self.service.state.borrow().composing
    }
    /// 注音：依序按這些鍵（字母、數字聲調），都要被輸入法吃下。
    fn zhuyin(&mut self, s: &str) {
        for c in s.chars() {
            let vk = c.to_ascii_uppercase() as u32;
            let scan = unsafe { MapVirtualKeyW(vk, MAPVK_VK_TO_VSC) } as isize;
            assert!(self.press(vk, scan, Act::Char(c)), "注音鍵 {c} 要吃下");
        }
    }
    fn enter(&mut self) -> bool {
        self.press(0x0D, 0x1C, Act::Char('\n'))
    }
    /// 按下 Ctrl（`repeat` 是按住不放的自動重複），回傳 TestKeyDown 的答案。
    fn ctrl_down(&mut self, repeat: bool) -> bool {
        hold_ctrl(true);
        // press() 把這個值放在 lParam 的 16 位以上：1 << 14 是第 30 位（這個鍵原本就按著）。
        let lparam = 0x1d | if repeat { 1 << 14 } else { 0 };
        self.press(0x11, lparam, Act::Nothing)
    }
    /// 按著 Ctrl 再按這一鍵（Ctrl 已由 ctrl_down 按下）。
    fn ctrl_key(&mut self, vk: u32, act: Act) -> bool {
        let scan = unsafe { MapVirtualKeyW(vk, MAPVK_VK_TO_VSC) } as isize;
        self.press(vk, scan, act)
    }
    fn ctrl_up(&mut self) {
        hold_ctrl(false);
    }
    /// context 的 view 視窗設成 App：經相容層的結果字串才會排給 App（Doc.view 預設是 0，FakeCuas.focus 預設是 App）。
    fn view_is_app(&self) {
        self.doc.view.set(Some(self.hwnd));
    }
    /// 外殼送給引擎的請求 type，依序。
    fn sent(&self) -> Vec<String> {
        self.service.state.borrow().session.sent.clone()
    }
}

/// 英文模式、文件原本是「前」的宿主。`static_flags` 是 context 的靜態旗標（IMM32 是 TS_SS_TRANSITORY）。
fn open(pipe: &str, static_flags: u32) -> Host {
    let (doc, ctx) = host(Sync::Run);
    doc.static_flags.set(static_flags);
    let service = ComObject::new(TextService::new());
    service.state.borrow_mut().session = Session::with_client(crate::ipc::Client::with_pipe(pipe));
    let english = service.state.borrow_mut().session.handle(json!({"type": "mode", "on": true}));
    assert!(english.ok && english.english_can_start, "測試引擎要開著英文自動補字");
    new_host(service, doc, ctx, static_flags)
}

/// 注音模式（預設）、文件原本是「前」的宿主（#262），用一般的 ZUIN_TEST_PIPE。
fn open_zhuyin(static_flags: u32) -> Option<Host> {
    open_zhuyin_on("ZUIN_TEST_PIPE", static_flags)
}

/// 同上，引擎的管道名稱取自指定的環境變數（#192 的 ZUIN_TEST_HOTKEY_PIPE 是設定開著簡體字快捷鍵的獨立引擎）。
fn open_zhuyin_on(env: &str, static_flags: u32) -> Option<Host> {
    let pipe = std::env::var(env).ok()?;
    let (doc, ctx) = host(Sync::Run);
    doc.static_flags.set(static_flags);
    let service = ComObject::new(TextService::new());
    service.state.borrow_mut().session = Session::with_client(crate::ipc::Client::with_pipe(&pipe));
    assert!(!service.state.borrow_mut().session.english(), "一般的測試引擎是注音模式");
    Some(new_host(service, doc, ctx, static_flags))
}

/// 接上假的系統相容層（預設：程式自己處理、相容層受理並終止組字、重送成功、沒有焦點變化、不是輸入法熱鍵）。
fn new_host(service: ComObject<TextService>, doc: Rc<Doc>, ctx: ITfContext, static_flags: u32) -> Host {
    let cuas = FakeCuas::new(doc.clone());
    *cuas.service.borrow_mut() = Some(service.clone());
    service.state.borrow_mut().cuas = cuas.clone();
    let sink: ITfKeyEventSink = service.to_interface();
    let imm32 = static_flags == TS_SS_TRANSITORY;
    Host { service, sink, ctx, doc, app: App::new(), imm32, cuas, hwnd: app_hwnd(), other_chars: Vec::new(), other_keys: Vec::new(), acts: HashMap::new() }
}

/// 本執行緒的系統插入點：IMM32 文字框有焦點時都有，#210 才照常英文補字。
struct Caret(HWND);

impl Caret {
    fn new() -> Caret {
        unsafe {
            let hwnd = CreateWindowExW(
                WINDOW_EX_STYLE(0),
                w!("STATIC"),
                w!(""),
                WS_POPUP,
                -32000,
                -32000,
                100,
                20,
                None,
                None::<HMENU>,
                None,
                None,
            )
            .unwrap();
            CreateCaret(hwnd, None, 1, 16).unwrap();
            assert!(crate::caret::thread_has_caret());
            Caret(hwnd)
        }
    }
}

impl Drop for Caret {
    fn drop(&mut self) {
        unsafe {
            let _ = DestroyCaret();
            let _ = DestroyWindow(self.0);
        }
    }
}

#[test]
fn imm32_word_and_space_or_punctuation_arrive_in_order() {
    let Some(pipe) = engine() else { return };
    plain_keyboard();
    let _caret = Caret::new();
    let mut h = open(&pipe, TS_SS_TRANSITORY);
    // 依序打 abc def,ghi ：修正前程式看到「前 abc,defghi 」（.14 的 EDIT 實測同樣順序）。
    h.letters("abc");
    assert!(h.pending(), "有插入點的 IMM32 文字框照常英文補字");
    let eaten = h.space();
    assert_eq!(h.app.text(), "前abc ");
    assert!(eaten, "單字中的空白在 IMM32 程式吃下，連同單字一起送出");
    assert!(!h.pending());
    h.letters("def");
    let eaten = h.comma();
    assert_eq!(h.app.text(), "前abc def,");
    assert!(eaten);
    // 逗號之後不重新開始補字（網址、識別字）：ghi 與之後的空白照常交給程式。
    for c in ['g', 'h', 'i'] {
        let vk = c.to_ascii_uppercase() as u32;
        let scan = unsafe { MapVirtualKeyW(vk, MAPVK_VK_TO_VSC) } as isize;
        assert!(!h.press(vk, scan, Act::Char(c)), "逗號後的 {c} 交給程式");
    }
    assert!(!h.space(), "沒有單字時空白照常交給程式");
    assert_eq!(h.app.text(), "前abc def,ghi ");
    // 數字同樣跟單字一起送出。
    h.letters("abc");
    let eaten = h.one();
    assert_eq!(h.app.text(), "前abc def,ghi abc1");
    assert!(eaten && !h.pending());
}

#[test]
fn imm32_arrow_after_a_word_only_confirms_it() {
    let Some(pipe) = engine() else { return };
    plain_keyboard();
    let _caret = Caret::new();
    let mut h = open(&pipe, TS_SS_TRANSITORY);
    // 修正前：← 先把插入點移到「前」之前，單字才插進去，得到「abcd,前」。
    h.letters("abcd");
    let eaten = h.left();
    assert!(!h.comma());
    assert_eq!(h.app.text(), "前abcd,");
    assert!(eaten, "單字中的 ← 在 IMM32 程式只送出單字（同 Enter，#235）");
    assert!(!h.pending());
    // 沒有單字時 ← 照常交給程式。
    assert!(!h.left());
    assert!(!h.comma());
    assert_eq!(h.app.text(), "前abcd,,");
    assert_eq!(h.app.caret, 6);
}

#[test]
fn tsf_programs_still_get_the_key_after_the_word() {
    // 自己接 TSF 的程式（記事本、Word、Chromium）送出當下就在文件裡：照舊送出單字後把空白、← 交給程式。
    let Some(pipe) = engine() else { return };
    plain_keyboard();
    let mut h = open(&pipe, 0);
    h.letters("abc");
    assert!(h.pending());
    assert!(!h.space(), "自己接 TSF 的程式照舊把空白交給程式");
    assert_eq!(h.app.text(), "前abc ");
    h.letters("abcd");
    assert!(!h.left());
    assert!(!h.pending());
    assert_eq!(h.app.text(), "前abc abcd");
    assert_eq!(h.app.caret, 8);
}

#[test]
fn imm32_ctrl_shortcut_sees_the_composition() {
    // #262、#66：IMM32 程式裡注音組字中按 Ctrl+A／Ctrl+S，程式的快捷鍵要看到組字。修正前外殼在 A 的 TestKeyDown 才送出組字，
    // 結果字串排在程式處理 Ctrl+A 之後（.14 的 IMM32 RichEdit 得到「我你」）；0.2.9 改成 Ctrl 按下就送出，卻讓單按 Ctrl 也送出。
    // 現在 Ctrl 單按保留組字，Ctrl+A 的 TestKeyDown 請系統相容層完成組字，結果字串在 A 作用之前到（spike mode 3/18 在 Notepad++ 實測）。
    let Some(mut h) = open_zhuyin(TS_SS_TRANSITORY) else { return };
    plain_keyboard();
    h.view_is_app();
    for round in 1..=2usize {
        h.zhuyin("ji3");
        let word = h.doc.composing_text();
        assert!(h.composing() && !word.is_empty(), "組字中");
        let before = h.app.text();
        assert!(!h.ctrl_down(false), "Ctrl 本身照舊交給程式");
        assert!(h.composing(), "單按 Ctrl 不送出組字");
        assert_eq!(h.cuas.completions.borrow().len(), round - 1, "單按 Ctrl 不經相容層");
        assert!(!h.ctrl_key(0x41, Act::Shortcut), "Ctrl+A 不吃，交給程式");
        h.ctrl_up();
        assert!(!h.composing());
        assert_eq!(h.cuas.completions.borrow().len(), round, "每次快捷鍵都經相容層完成一次（第 {round} 輪）");
        assert_eq!(h.cuas.completions.borrow()[round - 1].0, word, "相容層完成時組字區就是送出的文字");
        let typed = format!("{before}{word}");
        assert_eq!(h.app.text(), typed);
        assert_eq!(h.app.fired.len(), round, "快捷鍵作用一次");
        assert_eq!(h.app.fired[round - 1], (0x41, typed.clone()), "Ctrl+A 看到含組字的全文");
        assert_eq!(h.doc.committed(), typed);
    }
    // 守衛已解除：之後的一般送出（例如焦點移開時的 finish）照常進行，不被當成還在請相容層完成。
    h.zhuyin("ji3");
    h.service.finish(Some(&h.ctx), "commit");
    assert!(!h.composing(), "離開守衛後 finish 照常送出");
}

#[test]
fn imm32_ctrl_home_moves_after_the_composition() {
    // #262：Ctrl+Home 這類移動插入點的組合鍵也一樣：修正前插入點先移到最前面，「我」才插進去，跑到「前」前面。
    // #66：經相容層完成組字，字留在原處，插入點才到最前。
    let Some(mut h) = open_zhuyin(TS_SS_TRANSITORY) else { return };
    plain_keyboard();
    h.view_is_app();
    h.zhuyin("ji3");
    h.ctrl_down(false);
    assert!(!h.ctrl_key(0x24, Act::Top));
    h.ctrl_up();
    let text = h.app.text();
    assert!(text.starts_with('前') && text.chars().count() == 2, "組字留在原處：{text}");
    assert_eq!(h.app.caret, 0);
    assert_eq!(h.cuas.completions.borrow().len(), 1, "經相容層完成");
}

#[test]
fn imm32_ctrl_punctuation_after_ctrl_still_types_the_mark() {
    // #168、#66：IMM32 程式裡 Ctrl 單按不送出，Ctrl+, 與 Ctrl+. 插進同一段組字，Enter 一次送出（同新版微軟注音，
    // 研究紀錄 §11：Ctrl+, 得「你，好」）。按住 Ctrl 的自動重複同樣不送出。
    let Some(mut h) = open_zhuyin(TS_SS_TRANSITORY) else { return };
    plain_keyboard();
    h.view_is_app();
    h.zhuyin("ji3");
    h.ctrl_down(false);
    assert!(h.composing(), "單按 Ctrl 不送出");
    assert!(h.ctrl_key(0xBC, Act::Nothing), "Ctrl+, 由輸入法打全形逗號");
    assert!(h.composing());
    assert!(!h.ctrl_down(true), "按住 Ctrl 的自動重複照舊交給程式");
    assert!(h.composing(), "自動重複不送出組字");
    assert!(h.ctrl_key(0xBE, Act::Nothing));
    h.ctrl_up();
    assert!(h.composing());
    assert_eq!(h.app.text(), "前", "Enter 之前什麼都沒送出");
    assert!(h.cuas.completions.borrow().is_empty());
    assert!(h.enter());
    let text = h.app.text();
    assert!(text.starts_with('前') && text.ends_with("，。") && text.chars().count() == 4, "{text}");
    assert!(h.cuas.completions.borrow().is_empty(), "Enter 由外殼自己送出，不經相容層");
}

#[test]
fn imm32_lone_modifiers_keep_the_composition() {
    // #66：單按 Shift、單按 Alt、單按 Ctrl 都不送出組字（單按 Shift 還要切換中英，不在這裡測）。
    let Some(mut h) = open_zhuyin(TS_SS_TRANSITORY) else { return };
    plain_keyboard();
    h.view_is_app();
    h.zhuyin("ji3");
    assert!(!h.press(0x10, 0x2a, Act::Nothing), "Shift 照舊交給程式");
    assert!(h.composing(), "單按 Shift 不送出");
    assert!(!h.press(0x12, 0x38, Act::Nothing), "Alt 照舊交給程式");
    assert!(h.composing(), "單按 Alt 不送出");
    assert!(!h.ctrl_down(false));
    assert!(!h.ctrl_down(true));
    h.ctrl_up();
    assert!(h.composing(), "單按 Ctrl 與自動重複不送出");
    assert!(h.cuas.completions.borrow().is_empty());
    assert_eq!(h.app.text(), "前");
}

#[test]
fn imm32_shortcut_commit_is_learned_and_ordered() {
    // #66 spec〈送出方式〉：引擎先 commit（學習）、組字區換成送出文字，才請相容層完成；相容層終止組字時外殼不送取消，
    // 否則引擎放棄這次組字、不學習。Session.sent 是外殼與引擎的協定邊界。
    let Some(mut h) = open_zhuyin(TS_SS_TRANSITORY) else { return };
    plain_keyboard();
    h.view_is_app();
    h.zhuyin("ji3");
    let word = h.doc.composing_text();
    // 組字區與引擎送出的文字不同（例如候選預覽）：外殼要先把組字區換成送出文字，相容層才送得對。
    h.doc.overwrite_composing("預覽");
    let sent_before = h.sent().len();
    h.ctrl_down(false);
    assert!(!h.ctrl_key(0x41, Act::Shortcut));
    h.ctrl_up();
    let completions = h.cuas.completions.borrow().clone();
    assert_eq!(completions, vec![(word, Some("commit".to_string()))], "complete 當下引擎已收到 commit，組字區是送出文字");
    let sent = h.sent();
    let after = &sent[sent_before..];
    assert_eq!(after.iter().filter(|t| *t == "commit").count(), 1, "引擎只收到一次 commit：{after:?}");
    assert!(!after.iter().any(|t| t == "cancel"), "相容層終止組字不送取消：{after:?}");
}

#[test]
fn imm32_shortcut_commits_even_if_cuas_does_not() {
    // #66 spec〈送出方式〉第 4 點：拿不到輸入法 context、通知失敗、或受理了卻沒終止組字，外殼自己結束組字，字只出現一次。
    for (result, terminates) in [(Complete::NoContext, true), (Complete::Failed, true), (Complete::Notified, false)] {
        let Some(mut h) = open_zhuyin(TS_SS_TRANSITORY) else { return };
        plain_keyboard();
        h.view_is_app();
        h.cuas.complete_result.set(result);
        h.cuas.terminates.set(terminates);
        h.cuas.result_on_self_end.set(true); // 真實相容層：外殼自己結束組字時結果字串仍會晚到
        h.zhuyin("ji3");
        let word = h.doc.composing_text();
        h.ctrl_down(false);
        assert!(!h.ctrl_key(0x41, Act::Shortcut), "{result:?}/{terminates}：Ctrl+A 交給程式");
        h.ctrl_up();
        assert!(!h.composing(), "{result:?}/{terminates}：外殼自己結束組字");
        assert_eq!(h.cuas.completions.borrow().len(), 1, "{result:?}/{terminates}");
        assert_eq!(h.app.text(), format!("前{word}"), "{result:?}/{terminates}：字只出現一次");
        assert_eq!(h.doc.committed(), format!("前{word}"), "{result:?}/{terminates}");
        let sent = h.sent();
        assert_eq!(sent.iter().filter(|t| *t == "commit").count(), 1, "{result:?}/{terminates}：引擎只收到一次 commit：{sent:?}");
    }
}

#[test]
fn imm32_shortcut_with_a_dead_engine_does_not_notify_cuas() {
    // #66 spec〈送出方式〉第 5 點：引擎沒有回應時退回一般送出，沒有送出文字，所以不通知相容層、不吃鍵、不 panic。
    let Some(mut h) = open_zhuyin(TS_SS_TRANSITORY) else { return };
    plain_keyboard();
    h.view_is_app();
    h.zhuyin("ji3");
    h.service
        .state
        .borrow_mut()
        .session
        .set_client_for_test(crate::ipc::Client::with_pipe(r"\\.\pipe\zuin-test-66-no-such-engine"));
    h.ctrl_down(false);
    assert!(!h.ctrl_key(0x41, Act::Shortcut), "不吃鍵，快捷鍵交給程式");
    h.ctrl_up();
    assert!(h.cuas.completions.borrow().is_empty(), "引擎沒回應，不呼叫 Cuas::complete");
    assert_eq!(h.app.fired.len(), 1, "快捷鍵照常作用");
    assert!(h.sent().iter().any(|t| t == "commit"), "有試著請引擎 commit");
}

#[test]
fn imm32_focus_move_during_completion_keeps_the_text() {
    // #66：相容層完成組字時焦點跳走（OnSetFocus 同步進來）：守衛期間外殼的焦點處理不再 finish、不改文件，
    // 組字由 complete_via_cuas 收尾。字只出現一次、沒有被空字串蓋掉、引擎只收到一次 commit。
    let Some(mut h) = open_zhuyin(TS_SS_TRANSITORY) else { return };
    plain_keyboard();
    h.view_is_app();
    h.cuas.focus_after_complete.set(Some(HWND(0x2000 as *mut std::ffi::c_void)));
    h.zhuyin("ji3");
    let word = h.doc.composing_text();
    h.ctrl_down(false);
    assert!(!h.ctrl_key(0x41, Act::Shortcut));
    h.ctrl_up();
    assert!(!h.composing());
    assert_eq!(h.cuas.completions.borrow().len(), 1);
    assert_eq!(h.app.text(), format!("前{word}"), "字只出現一次、沒被蓋掉");
    assert_eq!(h.doc.committed(), format!("前{word}"));
    let sent = h.sent();
    assert_eq!(sent.iter().filter(|t| *t == "commit").count(), 1, "引擎只收到一次 commit：{sent:?}");
}

#[test]
fn imm32_english_word_is_sent_when_ctrl_goes_down() {
    // 英文補字的單字本來就在 Ctrl 按下時送出（english_key_policy 看 modifier_down），IMM32 程式裡 Ctrl+A 看得到單字。
    let Some(pipe) = engine() else { return };
    plain_keyboard();
    let _caret = Caret::new();
    let mut h = open(&pipe, TS_SS_TRANSITORY);
    h.letters("abc");
    assert!(h.pending());
    assert!(!h.ctrl_down(false));
    assert!(!h.ctrl_key(0x41, Act::Shortcut));
    h.ctrl_up();
    assert_eq!(h.app.seen, vec!["前abc".to_string()]);
}

#[test]
fn tsf_programs_keep_the_composition_when_ctrl_goes_down() {
    // 自己接 TSF 的程式（記事本、Word、Chromium）送出當下就在文件裡：照舊在 Ctrl 組合鍵的那一鍵才送出，
    // 單按 Ctrl 不結束組字，Ctrl＋標點照舊插進同一段組字（#168）。
    let Some(mut h) = open_zhuyin(0) else { return };
    plain_keyboard();
    h.zhuyin("ji3");
    assert!(!h.ctrl_down(false));
    assert!(h.composing(), "TSF 程式裡單按 Ctrl 不送出組字");
    assert!(h.ctrl_key(0xBC, Act::Nothing), "Ctrl+, 插進組字");
    assert!(h.composing());
    assert_eq!(h.doc.committed(), "前");
    assert!(!h.ctrl_key(0x41, Act::Shortcut), "Ctrl+A 先送出組字再交給程式");
    h.ctrl_up();
    assert!(!h.composing());
    let text = h.app.text();
    assert!(text.starts_with('前') && text.ends_with('，') && text.chars().count() == 3, "{text}");
    assert_eq!(h.app.seen, vec![text]);
}

/// 交給預設處理的程式（DefWindowProc）、注音組字「ji3」，回傳組字的文字。
fn composing_in_default_host(h: &mut Host) -> String {
    plain_keyboard();
    h.view_is_app();
    h.cuas.kind.set(HostKind::DefWindowProc);
    h.zhuyin("ji3");
    let word = h.doc.composing_text();
    assert!(h.composing() && !word.is_empty(), "組字中");
    h.cuas.history.borrow_mut().clear();
    word
}

fn scan_of(vk: u32) -> isize {
    unsafe { MapVirtualKeyW(vk, MAPVK_VK_TO_VSC) as isize }
}

fn keys_in(h: &Host) -> Vec<Queued> {
    h.cuas.history.borrow().iter().filter(|q| matches!(q, Queued::Key { .. })).cloned().collect()
}

#[test]
fn imm32_default_handling_host_gets_the_key_after_the_text() {
    // #66：交給 DefWindowProc 的程式（EDIT、WinForms）收到的結果字串是 post 的 WM_CHAR，排在這一鍵之後。
    // 白名單內的鍵（Ctrl 組合鍵、F1 到 F12 除了 F10）攔下，把同一個 WM_KEYDOWN 再 post 給焦點視窗，排在 WM_CHAR 之後
    // （spike mode 18 在標準文字框與 WinForms 實測）。重排回來的鍵 TestKeyDown 不吃，快捷鍵作用剛好一次。
    let cases: Vec<(&str, bool, bool, u32, Act)> = {
        let mut v = vec![("Ctrl+S", true, false, 0x53, Act::Shortcut), ("Ctrl+Home", true, false, 0x24, Act::Top)];
        for f in (0x70..=0x7bu32).filter(|f| *f != 0x79) {
            v.push(("F 鍵", false, false, f, Act::Shortcut));
        }
        v.push(("Shift+F5", false, true, 0x74, Act::Shortcut));
        v.push(("Ctrl+F5", true, false, 0x74, Act::Shortcut));
        v
    };
    let Some(mut h) = open_zhuyin(TS_SS_TRANSITORY) else { return };
    for (name, ctrl, shift, vk, act) in cases {
        let word = composing_in_default_host(&mut h);
        h.app = App::new();
        set_mods(ctrl, false, false, shift);
        let scan = scan_of(vk);
        let eaten = h.press(vk, scan, act);
        set_mods(false, false, false, false);
        let tag = format!("{name} vk={vk:#x}");
        assert!(eaten, "{tag}：TestKeyDown 吃下");
        assert!(!h.composing(), "{tag}：組字送出");
        let lparam = 1 | scan << 16;
        assert_eq!(
            *h.cuas.history.borrow(),
            vec![
                Queued::Char { hwnd: app_hwnd(), text: word.clone() },
                Queued::Key { hwnd: app_hwnd(), vk, lparam }
            ],
            "{tag}：Char 在前、Key 在後，同 vk、同 lParam、目標是焦點視窗"
        );
        assert!(h.service.state.borrow().pending_repost.is_none(), "{tag}：重排的鍵回來就清掉");
        let full = format!("前{word}");
        assert_eq!(h.app.text(), full, "{tag}");
        match act {
            Act::Top => assert_eq!(h.app.caret, 0, "{tag}：字留原處、插入點才到最前"),
            _ => assert_eq!(h.app.fired, vec![(vk, full)], "{tag}：快捷鍵作用一次、看到完整文字"),
        }
        assert_eq!(h.cuas.completions.borrow().len(), 1, "{tag}：只經相容層完成一次");
        h.cuas.completions.borrow_mut().clear();
    }
}

#[test]
fn imm32_self_handling_host_is_never_reposted() {
    // #66：自己處理結果字串的程式（Notepad++）當場就收到字，快捷鍵照舊交給它：不吃、不重排。
    let Some(mut h) = open_zhuyin(TS_SS_TRANSITORY) else { return };
    for (ctrl, vk, act) in [(true, 0x53u32, Act::Shortcut), (true, 0x24, Act::Top), (false, 0x70, Act::Shortcut), (false, 0x7b, Act::Shortcut)] {
        let word = composing_in_default_host(&mut h);
        h.cuas.kind.set(HostKind::SelfHandles);
        h.app = App::new();
        set_mods(ctrl, false, false, false);
        let eaten = h.press(vk, scan_of(vk), act);
        set_mods(false, false, false, false);
        assert!(!eaten, "vk={vk:#x}：不吃");
        assert!(keys_in(&h).is_empty(), "vk={vk:#x}：佇列沒有 Key");
        assert!(!h.composing());
        assert_eq!(h.app.text(), format!("前{word}"), "vk={vk:#x}");
        if !matches!(act, Act::Top) {
            assert_eq!(h.app.fired, vec![(vk, format!("前{word}"))], "vk={vk:#x}：快捷鍵作用一次、看到完整文字");
        }
        h.cuas.completions.borrow_mut().clear();
    }
    // 送出前佇列裡只有別的視窗的 Char：只看目標視窗，DefWindowProc 照樣重排。
    let word = composing_in_default_host(&mut h);
    let other = HWND(0x2000 as *mut std::ffi::c_void);
    h.cuas.queue.borrow_mut().push(Queued::Char { hwnd: other, text: "別".to_string() });
    h.app = App::new();
    set_mods(true, false, false, false);
    assert!(h.press(0x53, scan_of(0x53), Act::Shortcut), "別的視窗的 Char 不影響判斷");
    set_mods(false, false, false, false);
    assert_eq!(keys_in(&h).len(), 1, "仍然重排");
    assert_eq!(h.app.fired, vec![(0x53, format!("前{word}"))]);
    assert_eq!(h.other_chars, vec![(other, "別".to_string())]);
}

#[test]
fn imm32_system_and_ime_keys_are_not_reposted() {
    // #66：F10 與 Alt、Win 組合鍵是系統按鍵（WM_SYSKEYDOWN），重排的一般按鍵訊息重現不了，Ctrl+Alt 是 AltGr，
    // 輸入法熱鍵（Ctrl+Space）交給系統，Insert、應用程式鍵、多媒體鍵不在白名單：組字照樣送出、不吃、不重排。
    // (名稱, ctrl, alt, win, vk, 輸入法熱鍵)
    let cases: [(&str, bool, bool, bool, u32, bool); 9] = [
        ("F10", false, false, false, 0x79, false),
        ("Alt+S", false, true, false, 0x53, false),
        ("Win+S", false, false, true, 0x53, false),
        ("Ctrl+Alt+Q", true, true, false, 0x51, false),
        ("Ctrl+Space", true, false, false, 0x20, true),
        ("Insert", false, false, false, 0x2d, false),
        ("Apps", false, false, false, 0x5d, false),
        ("音量加", false, false, false, 0xaf, false),
        ("播放暫停", false, false, false, 0xb3, false),
    ];
    for (name, ctrl, alt, win, vk, hotkey) in cases {
        let Some(mut h) = open_zhuyin(TS_SS_TRANSITORY) else { return };
        let word = composing_in_default_host(&mut h);
        h.cuas.hotkey.set(hotkey);
        h.app = App::new();
        set_mods(ctrl, alt, win, false);
        let eaten = h.press(vk, scan_of(vk), Act::Shortcut);
        set_mods(false, false, false, false);
        assert!(!eaten, "{name}：不吃");
        assert!(keys_in(&h).is_empty(), "{name}：佇列沒有 Key");
        assert!(!h.composing(), "{name}：組字照樣送出");
        assert_eq!(h.doc.committed(), format!("前{word}"), "{name}");
        assert_eq!(h.app.text(), format!("前{word}"), "{name}：字只出現一次");
        assert_eq!(h.app.fired.len(), 1, "{name}：快捷鍵作用一次");
        h.cuas.hotkey.set(false);
        h.cuas.completions.borrow_mut().clear();
    }
}

#[test]
fn imm32_simplified_hotkey_still_toggles() {
    // #192：設定開著時 Ctrl+Shift+F11 吃下切換簡體字、不送出組字，也不重排（#66 在這之後才判斷）。
    // 需要 ZUIN_TEST_HOTKEY_PIPE（設定開著簡體字快捷鍵的獨立引擎）；沒設就略過。
    let Ok(pipe) = std::env::var("ZUIN_TEST_HOTKEY_PIPE") else { return };
    // 這個測試真的切換引擎全域的「輸出簡體字」：與同一引擎的其他測試互斥，失敗時也把設定切回繁體。
    let _engine = crate::hotkey_engine_lock();
    struct RestoreTraditional(String);
    impl Drop for RestoreTraditional {
        fn drop(&mut self) {
            let mut s = Session::with_client(crate::ipc::Client::with_pipe(&self.0));
            if s.simplified() == Some(true) {
                s.output_simplified(false);
            }
        }
    }
    let _restore = RestoreTraditional(pipe);
    let Some(mut h) = open_zhuyin_on("ZUIN_TEST_HOTKEY_PIPE", TS_SS_TRANSITORY) else { return };
    let word = composing_in_default_host(&mut h);
    for round in 0..2 {
        // 按兩次：切換之後再切回來，不改引擎的設定。
        set_mods(true, false, false, true);
        let eaten = h.press(0x7a, scan_of(0x7a), Act::Shortcut);
        set_mods(false, false, false, false);
        assert!(eaten, "吃下切換");
        let now = h.service.state.borrow().session.simplified();
        assert_eq!(now, Some(round == 0), "第 {} 次按下真的切換了簡體字設定", round + 1);
        assert!(h.composing(), "組字不送出");
        assert_eq!(h.doc.composing_text(), word);
    }
    assert!(h.cuas.completions.borrow().is_empty(), "沒有經相容層送出");
    assert!(h.cuas.history.borrow().is_empty(), "也沒有重排");
    assert!(h.app.fired.is_empty());
}

#[test]
fn imm32_any_doubt_falls_back_to_not_eating() {
    // #66：判斷有疑義就不攔：組字仍送出、字只出現一次、不吃、快捷鍵作用一次、佇列沒有 Key。
    // 每一種都在 Ctrl+S、DefWindowProc 下，其他條件都成立，只破壞這一條。
    let far = HWND(0x2000 as *mut std::ffi::c_void);
    let names = [
        "PostMessage 失敗",
        "送出前佇列已有同一視窗的 Char",
        "focus 與 view 不同",
        "focus 與 view 不同而且字落在 focus",
        "focus 與 view 都是 0",
        "complete 時換焦點",
        "NoContext",
        "Failed",
        "Notified 但沒有終止",
        "相容層沒經 OnCompositionTerminated 就清掉組字狀態",
    ];
    for name in names {
        let Some(mut h) = open_zhuyin(TS_SS_TRANSITORY) else { return };
        let word = composing_in_default_host(&mut h);
        match name {
            "PostMessage 失敗" => h.cuas.repost_ok.set(false),
            "送出前佇列已有同一視窗的 Char" => {
                h.cuas.queue.borrow_mut().push(Queued::Char { hwnd: app_hwnd(), text: "先".to_string() })
            }
            "focus 與 view 不同" => h.cuas.focus.set(far),
            "focus 與 view 不同而且字落在 focus" => {
                h.cuas.focus.set(far);
                h.cuas.deliver_to.set(Some(far));
            }
            "focus 與 view 都是 0" => {
                h.cuas.focus.set(HWND::default());
                h.doc.view.set(Some(HWND::default()));
            }
            "complete 時換焦點" => h.cuas.focus_after_complete.set(Some(far)),
            "NoContext" | "Failed" | "Notified 但沒有終止" => {
                h.cuas.complete_result.set(if name == "NoContext" { Complete::NoContext } else { Complete::Failed });
                if name.starts_with("Notified") {
                    h.cuas.complete_result.set(Complete::Notified);
                    h.cuas.terminates.set(false);
                }
                h.cuas.result_on_self_end.set(true);
            }
            _ => h.cuas.silent_end.set(true),
        }
        h.app = App::new();
        set_mods(true, false, false, false);
        let eaten = h.press(0x53, scan_of(0x53), Act::Shortcut);
        set_mods(false, false, false, false);
        assert!(!eaten, "{name}：不吃");
        assert!(keys_in(&h).is_empty(), "{name}：佇列沒有 Key");
        assert!(!h.composing(), "{name}：組字仍送出");
        assert_eq!(h.doc.committed(), format!("前{word}"), "{name}：字只出現一次");
        let chars: Vec<_> = h.cuas.history.borrow().iter().filter(|q| matches!(q, Queued::Char { text, .. } if *text == word)).cloned().collect();
        assert!(chars.len() <= 1, "{name}：結果字串不重複：{chars:?}");
        assert_eq!(h.app.fired.len(), 1, "{name}：快捷鍵作用一次");
        assert!(h.service.state.borrow().pending_repost.is_none(), "{name}");
        let sent = h.sent();
        assert_eq!(sent.iter().filter(|t| *t == "commit").count(), 1, "{name}：引擎只收到一次 commit");
    }
}

#[test]
fn imm32_complete_reports_by_cuas_only_when_cuas_ended_it() {
    // #66：ByCuas 是觀察到的（OnCompositionTerminated 對上守衛的組字），不是從「組字已結束」推論的。
    let Some(mut h) = open_zhuyin(TS_SS_TRANSITORY) else { return };
    plain_keyboard();
    h.view_is_app();
    h.zhuyin("ji3");
    assert_eq!(h.service.complete_via_cuas(Some(&h.ctx), app_hwnd()), Completed::ByCuas, "相容層終止了組字");
    let Some(mut h) = open_zhuyin(TS_SS_TRANSITORY) else { return };
    plain_keyboard();
    h.view_is_app();
    h.cuas.silent_end.set(true);
    h.zhuyin("ji3");
    assert_eq!(
        h.service.complete_via_cuas(Some(&h.ctx), app_hwnd()),
        Completed::BySelf,
        "組字狀態是別的路徑清掉的，不是相容層終止的"
    );
}
