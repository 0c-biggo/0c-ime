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
    VK_SHIFT,
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
    #[allow(dead_code)] // 後續提交相容層的測試才用（#66）
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
        })
    }

    fn deliver(&self, hwnd: HWND, text: String) {
        self.via.borrow_mut().push(text.clone());
        match self.kind.get() {
            HostKind::SelfHandles => self.handed.borrow_mut().push(text),
            HostKind::DefWindowProc => self.queue.borrow_mut().push(Queued::Char { hwnd, text }),
        }
    }
}

impl Cuas for FakeCuas {
    fn focus(&self) -> HWND {
        self.focus.get()
    }

    fn complete(&self, hwnd: HWND) -> Complete {
        let text = self.doc.composing_text();
        let service = self.service.borrow().clone().expect("FakeCuas 要先接上外殼");
        let sent = service.state.borrow().session.sent.last().cloned();
        self.completions.borrow_mut().push((text.clone(), sent));
        let result = self.complete_result.get();
        if result == Complete::Notified && self.terminates.get() {
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
        if let Some(focus) = self.focus_after_complete.get() {
            self.focus.set(focus);
        }
        result
    }

    fn has_char(&self, hwnd: HWND) -> bool {
        self.queue.borrow().iter().any(|q| matches!(q, Queued::Char { hwnd: h, .. } if *h == hwnd))
    }

    fn repost(&self, hwnd: HWND, vk: u32, lparam: isize) -> bool {
        if self.repost_ok.get() {
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
    let pipe = std::env::var("ZUIN_TEST_PIPE").ok()?;
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
    // #262：IMM32 程式裡注音組字中按 Ctrl+A／Ctrl+S，程式的快捷鍵要看到組字。修正前外殼在 A 的 TestKeyDown 才送出組字，
    // 結果字串排在程式處理 Ctrl+A 之後：全選（存檔）時「我」還不在文件裡（.14 的 IMM32 RichEdit 得到「我你」）。
    // 改成 Ctrl 按下時就送出（同微軟注音，訊息紀錄 k0x11 R(result=我) k0x41），結果字串在 A 之前到。
    let Some(mut h) = open_zhuyin(TS_SS_TRANSITORY) else { return };
    plain_keyboard();
    h.zhuyin("ji3");
    assert!(h.composing());
    assert_eq!(h.doc.committed(), "前", "組字還沒送出");
    assert!(!h.ctrl_down(false), "Ctrl 本身照舊交給程式");
    assert!(!h.composing(), "IMM32 程式裡 Ctrl 按下就送出組字");
    let typed = h.app.text();
    assert_ne!(typed, "前");
    assert!(!h.ctrl_key(0x41, Act::Shortcut), "Ctrl+A 交給程式");
    h.ctrl_up();
    assert_eq!(h.app.seen, vec![typed.clone()], "Ctrl+A 看到已送出的組字");
    assert_eq!(h.doc.committed(), typed);
    // 按住 Ctrl 的自動重複、沒在組字時的 Ctrl 都照舊交給程式。
    assert!(!h.ctrl_down(false));
    assert!(!h.ctrl_down(true));
    h.ctrl_up();
    assert_eq!(h.app.text(), typed);
}

#[test]
fn imm32_ctrl_home_moves_after_the_composition() {
    // #262：Ctrl+Home 這類移動插入點的組合鍵也一樣：修正前插入點先移到最前面，「我」才插進去，跑到「前」前面。
    let Some(mut h) = open_zhuyin(TS_SS_TRANSITORY) else { return };
    plain_keyboard();
    h.zhuyin("ji3");
    h.ctrl_down(false);
    assert!(!h.ctrl_key(0x24, Act::Top));
    h.ctrl_up();
    let text = h.app.text();
    assert!(text.starts_with('前') && text.chars().count() == 2, "組字留在原處：{text}");
    assert_eq!(h.app.caret, 0);
}

#[test]
fn imm32_ctrl_punctuation_after_ctrl_still_types_the_mark() {
    // #168 的 Ctrl＋標點在 IMM32 程式裡：Ctrl 按下時組字已送出，Ctrl+, 在沒在組字時照舊打出「，」（另起一段組字），
    // 文字與修正前（插進同一段組字）相同。按住 Ctrl 的自動重複不送出這一段，接著的 Ctrl+. 接在後面。
    let Some(mut h) = open_zhuyin(TS_SS_TRANSITORY) else { return };
    plain_keyboard();
    h.zhuyin("ji3");
    h.ctrl_down(false);
    assert!(h.ctrl_key(0xBC, Act::Nothing), "Ctrl+, 由輸入法打全形逗號");
    assert!(h.composing());
    assert!(!h.ctrl_down(true), "按住 Ctrl 的自動重複照舊交給程式");
    assert!(h.composing(), "自動重複不送出組字");
    assert!(h.ctrl_key(0xBE, Act::Nothing));
    h.ctrl_up();
    assert!(h.enter());
    let text = h.app.text();
    assert!(text.starts_with('前') && text.ends_with("，。") && text.chars().count() == 4, "{text}");
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
