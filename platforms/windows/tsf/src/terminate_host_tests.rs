//! #322：宿主自己終止組字（記事本、Edge 點到別處，Win＋Space 換輸入法，WPF 與原生 EDIT 失焦）時，失焦與 Deactivate 的
//! commit 走不到，文件只剩宿主留下的組字文字。以 edit.rs 的假文件當宿主，打字後照 TSF 的順序先呼叫
//! OnCompositionTerminated（範圍仍有效、給寫入 cookie），再由宿主結束組字，看文件留下什麼、引擎收到什麼。
//! 需要 ZUIN_TEST_PIPE 的測試引擎；沒設就略過。
use super::*;
use crate::edit::tests::{host, Doc, Sync};
use windows::Win32::UI::Input::KeyboardAndMouse::{
    GetKeyboardState, MapVirtualKeyW, SetKeyboardState, MAPVK_VK_TO_VSC, VK_CAPITAL, VK_CONTROL, VK_MENU, VK_SHIFT,
};
use windows::Win32::UI::TextServices::{ITfKeyEventSink, TS_SS_TRANSITORY};

/// 沒有修飾鍵、沒有 Caps Lock（只改本執行緒的鍵盤狀態）。
fn plain_keyboard() {
    let mut state = [0u8; 256];
    unsafe {
        GetKeyboardState(&mut state).unwrap();
        for vk in [VK_CAPITAL, VK_SHIFT, VK_CONTROL, VK_MENU] {
            state[vk.0 as usize] = 0;
        }
        SetKeyboardState(&state).unwrap();
    }
}

struct Host {
    service: ComObject<TextService>,
    sink: ITfKeyEventSink,
    ctx: ITfContext,
    doc: Rc<Doc>,
}

/// 文件原本是「前」的宿主；`static_flags` 是 context 的靜態旗標（經系統相容層的 IMM32 程式是 TS_SS_TRANSITORY）。
fn open(static_flags: u32) -> Option<Host> {
    let pipe = std::env::var("ZUIN_TEST_PIPE").ok()?;
    plain_keyboard();
    let (doc, ctx) = host(Sync::Run);
    doc.static_flags.set(static_flags);
    let service = ComObject::new(TextService::new());
    service.state.borrow_mut().session = Session::with_client(crate::ipc::Client::with_pipe(&pipe));
    assert!(!service.state.borrow_mut().session.english(), "一般的測試引擎是注音模式");
    let sink: ITfKeyEventSink = service.to_interface();
    Some(Host { service, sink, ctx, doc })
}

impl Host {
    /// 依序按這些鍵（TestKeyDown 吃下才有 OnKeyDown），都要被輸入法吃下。
    fn keys(&self, s: &str) {
        for c in s.chars() {
            let vk = c.to_ascii_uppercase() as u32;
            assert!(self.press(vk), "鍵 {c} 要吃下");
        }
    }
    fn press(&self, vk: u32) -> bool {
        let scan = unsafe { MapVirtualKeyW(vk, MAPVK_VK_TO_VSC) } as isize;
        let (w, l) = (WPARAM(vk as usize), LPARAM(1 | scan << 16));
        let eaten = unsafe { self.sink.OnTestKeyDown(&self.ctx, w, l) }.unwrap().as_bool();
        if eaten {
            assert!(unsafe { self.sink.OnKeyDown(&self.ctx, w, l) }.unwrap().as_bool(), "vk={vk:#x}");
        }
        eaten
    }
    /// 宿主終止組字：TSF 先通知（範圍仍有效），宿主再把組字範圍的文字留在文件裡。
    fn host_terminates(&self) {
        let composition = self.service.state.borrow().composition.clone().expect("組字中");
        let sink: ITfCompositionSink = self.service.to_interface();
        unsafe { sink.OnCompositionTerminated(7, &composition) }.unwrap();
        self.doc.end_composition();
    }
    fn composing(&self) -> bool {
        self.service.state.borrow().composing
    }
    fn sent(&self) -> Vec<String> {
        self.service.state.borrow().session.sent.clone()
    }
}

/// 組字區最後是打到一半的 ㄕ：回傳它前面已經組好的字。
fn composed_before_pending(h: &Host) -> String {
    let text = h.doc.composing_text();
    let composed = text.strip_suffix('ㄕ').unwrap_or_else(|| panic!("組字區要以未打完的 ㄕ 結尾：{text}"));
    assert!(!composed.is_empty() && !composed.contains('ㄕ'), "{text}");
    composed.to_string()
}

#[test]
fn host_ending_the_composition_keeps_only_the_composed_text() {
    // #322：su3cl3g（你好＋ㄕ）後點到別處。修正前文件是「前你好ㄕ」，引擎收到 cancel（不學習）。
    // 自己接 TSF 的程式（記事本、Edge）與經相容層的 IMM32 程式（Notepad++、原生 EDIT）一樣。
    for flags in [0, TS_SS_TRANSITORY] {
        let Some(h) = open(flags) else { return };
        h.keys("su3cl3g");
        let composed = composed_before_pending(&h);
        h.host_terminates();
        assert_eq!(h.doc.committed(), format!("前{composed}"), "flags={flags:#x}：未打完的注音不留在文件裡");
        assert!(!h.composing());
        let sent = h.sent();
        assert_eq!(sent.last().map(String::as_str), Some("commit"), "照失焦送出（學習已組好的字）：{sent:?}");
        assert!(!sent.iter().any(|t| t == "cancel"), "{sent:?}");
        // 引擎沒有殘留的組字：下一個字從頭開始，文件也不會再多出什麼。
        h.keys("u");
        assert_eq!(h.doc.composing_text(), "ㄧ");
        assert_eq!(h.doc.committed(), format!("前{composed}"));
    }
}

#[test]
fn host_ending_a_lone_unfinished_syllable_leaves_nothing() {
    // #322：只打了 ㄕ 就按 Win＋Space 換輸入法（系統先終止組字才 Deactivate）：文件不留 ㄕ。
    let Some(h) = open(0) else { return };
    h.keys("g");
    assert_eq!(h.doc.composing_text(), "ㄕ");
    h.host_terminates();
    assert_eq!(h.doc.committed(), "前");
    assert_eq!(h.sent().last().map(String::as_str), Some("commit"));
}

#[test]
fn host_ending_composed_text_does_not_touch_the_document() {
    // 只有組好的字（su3cl3）：文件裡已經是要留下的文字，終止時不改寫範圍（宿主看到的跟修正前一樣），引擎照失焦送出。
    let Some(h) = open(0) else { return };
    h.keys("su3cl3");
    let composed = h.doc.composing_text();
    assert!(!composed.is_empty() && !composed.contains(|c: char| ('ㄅ'..='ㄩ').contains(&c)), "{composed}");
    let writes = h.doc.text_writes();
    h.host_terminates();
    assert_eq!(h.doc.text_writes(), writes, "不改寫範圍");
    assert_eq!(h.doc.committed(), format!("前{composed}"));
    assert_eq!(h.sent().last().map(String::as_str), Some("commit"));
}

#[test]
fn host_changed_composition_is_given_up_as_before() {
    // 組字範圍已經不是外殼最後顯示的組字（宿主自己改過）：不碰文件，照舊放棄（cancel、不學習）。
    let Some(h) = open(0) else { return };
    h.keys("su3cl3g");
    h.doc.overwrite_composing("別的字");
    let writes = h.doc.text_writes();
    h.host_terminates();
    assert_eq!(h.doc.text_writes(), writes);
    assert_eq!(h.doc.committed(), "前別的字");
    assert_eq!(h.sent().last().map(String::as_str), Some("cancel"));
    assert!(!h.composing());
}

#[test]
fn host_refusing_the_rewrite_keeps_its_text() {
    // 宿主不讓改寫（SetText 失敗、讀回也不是送出的文字）：文件留著原本的組字，不重試也不插入第二次。
    let Some(h) = open(0) else { return };
    h.keys("su3cl3g");
    let shown = h.doc.composing_text();
    h.doc.drop_next_set_text();
    h.host_terminates();
    assert_eq!(h.doc.committed(), format!("前{shown}"));
    assert!(!h.composing());
}

#[test]
fn enter_still_commits_the_unfinished_syllable_as_shown() {
    // Enter 照畫面送出，打到一半的 ㄕ 也送（#322 只改外殼結束組字的情況）；之後宿主不會再有終止通知。
    let Some(h) = open(0) else { return };
    h.keys("su3cl3g");
    let shown = h.doc.composing_text();
    assert!(h.press(0x0D), "組字中的 Enter 要吃下");
    assert!(!h.composing());
    assert_eq!(h.doc.committed(), format!("前{shown}"));
}

#[test]
fn shell_commit_on_focus_loss_is_unchanged() {
    // 不終止組字的宿主（Excel 等）：焦點離開時外殼自己 commit（leave_document），結果同 #322 的核心修正。
    let Some(h) = open(0) else { return };
    h.keys("su3cl3g");
    let composed = composed_before_pending(&h);
    h.service.finish(Some(&h.ctx), "commit");
    assert_eq!(h.doc.committed(), format!("前{composed}"));
    assert!(!h.composing() && !h.doc.composing());
}

// #322：Edge／Chrome 網頁裡點 textarea 外的空白處（或同一個欄位裡別的位置）。Chromium 在點擊交給網頁前自己把組字原樣定案
// （FinishImeCompositionSession），才在 TSFTextStore::CancelComposition 裡終止組字；那時終止通知裡的改寫會被丟掉，修正前
// 網頁留下「你好ㄕ」。外殼在 Chromium 的視窗組字時掛滑鼠 hook（click.rs），點擊交給程式前先照失焦的規則送出。

/// 指定類別的隱藏頂層視窗（測試行程裡註冊同名類別；只拿來當 view 與點擊的目標）。
fn window_of_class(class: windows_core::PCWSTR) -> HWND {
    use windows::Win32::System::LibraryLoader::GetModuleHandleW;
    use windows::Win32::UI::WindowsAndMessaging::{
        CreateWindowExW, DefWindowProcW, RegisterClassW, WINDOW_EX_STYLE, WNDCLASSW, WS_POPUP,
    };
    unsafe extern "system" fn plain(hwnd: HWND, msg: u32, w: WPARAM, l: LPARAM) -> windows::Win32::Foundation::LRESULT {
        DefWindowProcW(hwnd, msg, w, l)
    }
    unsafe {
        let wc = WNDCLASSW {
            lpfnWndProc: Some(plain),
            hInstance: GetModuleHandleW(None).unwrap().into(),
            lpszClassName: class,
            ..Default::default()
        };
        let _ = RegisterClassW(&wc); // 同一個行程的其他測試可能已經註冊過
        CreateWindowExW(WINDOW_EX_STYLE(0), class, windows_core::w!(""), WS_POPUP, 0, 0, 10, 10, None, None, None, None)
            .unwrap()
    }
}

fn destroy(hwnd: HWND) {
    let _ = unsafe { windows::Win32::UI::WindowsAndMessaging::DestroyWindow(hwnd) };
}

#[test]
fn chromium_click_commits_before_the_browser_finishes_the_composition() {
    use windows::Win32::UI::WindowsAndMessaging::{WM_LBUTTONDBLCLK, WM_LBUTTONDOWN, WM_MOUSEMOVE};
    let Some(h) = open(0) else { return };
    let chrome = window_of_class(windows_core::w!("Chrome_WidgetWin_1"));
    h.doc.view.set(Some(chrome));
    h.keys("su3cl3g");
    let composed = composed_before_pending(&h);
    assert!(crate::click::watching(), "Chromium 的視窗組字中要看滑鼠按下");
    // 移動滑鼠不算。
    crate::click::deliver(WM_MOUSEMOVE, chrome);
    assert!(h.composing());
    crate::click::deliver(WM_LBUTTONDOWN, chrome);
    assert_eq!(h.doc.committed(), format!("前{composed}"), "未打完的注音不進網頁");
    assert!(!h.composing() && !h.doc.composing(), "瀏覽器接著處理點擊時已經沒有組字");
    let sent = h.sent();
    assert_eq!(sent.last().map(String::as_str), Some("commit"), "照失焦送出（學習已組好的字）：{sent:?}");
    assert!(!crate::click::watching(), "組字結束就拿掉 hook");
    // 雙擊的第二下：沒有組字，不再動文件。
    let writes = h.doc.text_writes();
    crate::click::deliver(WM_LBUTTONDBLCLK, chrome);
    assert_eq!(h.doc.text_writes(), writes);
    // 同一個欄位續打：重新開始組字、照樣看滑鼠。
    h.keys("g");
    assert_eq!(h.doc.composing_text(), "ㄕ");
    assert!(crate::click::watching());
    crate::click::deliver(WM_LBUTTONDOWN, chrome);
    assert_eq!(h.doc.committed(), format!("前{composed}"), "只有 ㄕ 的組字點一下就沒了");
    assert!(!h.composing() && !crate::click::watching());
    destroy(chrome);
}

#[test]
fn enter_in_chromium_still_commits_as_shown_and_stops_watching() {
    let Some(h) = open(0) else { return };
    let chrome = window_of_class(windows_core::w!("Chrome_WidgetWin_1"));
    h.doc.view.set(Some(chrome));
    h.keys("su3cl3g");
    let shown = h.doc.composing_text();
    assert!(h.press(0x0D));
    assert_eq!(h.doc.committed(), format!("前{shown}"), "Enter 照畫面送出");
    assert!(!crate::click::watching());
    destroy(chrome);
}

#[test]
fn other_windows_and_imm32_programs_are_not_watched() {
    // 記事本、WPF 等由宿主終止組字的路徑處理（settle_terminated）；經相容層的 IMM32 程式即使視窗類別像 Chromium 也不看。
    let notepad = window_of_class(windows_core::w!("RichEditD2DPT"));
    let chrome = window_of_class(windows_core::w!("Chrome_WidgetWin_1"));
    for (view, flags) in [(notepad, 0), (chrome, TS_SS_TRANSITORY)] {
        let Some(h) = open(flags) else { return };
        h.doc.view.set(Some(view));
        h.keys("su3cl3g");
        assert!(!crate::click::watching(), "flags={flags:#x}");
        h.host_terminates();
    }
    destroy(notepad);
    destroy(chrome);
}

#[test]
fn click_outside_chromium_windows_or_mid_apply_is_left_alone() {
    use windows::Win32::UI::WindowsAndMessaging::WM_LBUTTONDOWN;
    let Some(h) = open(0) else { return };
    let chrome = window_of_class(windows_core::w!("Chrome_WidgetWin_1"));
    let other = window_of_class(windows_core::w!("0cTestDialog"));
    h.doc.view.set(Some(chrome));
    h.keys("su3cl3g");
    let shown = h.doc.composing_text();
    let writes = h.doc.text_writes();
    // 同一執行緒上不是 Chromium 的視窗（例如程式自己的原生對話框）收到的點擊不算。
    crate::click::deliver(WM_LBUTTONDOWN, other);
    // 套用快照中途（宿主在我們的呼叫裡取訊息）不動，留給宿主終止組字的路徑。
    {
        let _applying = Applying::enter(&h.service.applying, &h.service.layout_missed);
        crate::click::deliver(WM_LBUTTONDOWN, chrome);
    }
    {
        let _completing = Completing::enter(&h.service.state, None);
        crate::click::deliver(WM_LBUTTONDOWN, chrome);
    }
    assert_eq!(h.doc.text_writes(), writes);
    assert_eq!(h.doc.composing_text(), shown);
    assert!(h.composing() && crate::click::watching());
    // 停用（Deactivate）會拿掉 hook。
    crate::click::forget(&h.service);
    assert!(!crate::click::watching());
    h.host_terminates();
    destroy(chrome);
    destroy(other);
}
