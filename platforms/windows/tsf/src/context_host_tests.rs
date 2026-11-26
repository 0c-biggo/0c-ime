//! #337：IMM32 程式（系統相容層）裡游標前一個字是不是阿拉伯數字。焦點視窗用真的系統 EDIT、RichEdit 與自訂視窗，
//! context 用 privacy 宿主測試的假 context：暫時 context 的文件是空的，選取是一個游標、往前一個字動不了，
//! 跟 .14 「執行」對話框的 tsf.log（`digit before probe=document_start`）一樣。
use super::*;
use crate::privacy::host_tests::context_on;
use std::cell::Cell;
use std::sync::atomic::{AtomicUsize, Ordering};
use windows::Win32::Foundation::LRESULT;
use windows::Win32::System::LibraryLoader::{GetModuleHandleW, LoadLibraryW};
use windows::Win32::UI::Controls::{EM_GETLINECOUNT, EM_SETPASSWORDCHAR, EM_SETSEL};
use windows::Win32::UI::TextServices::{TS_SS_NOHIDDENTEXT, TS_SS_TRANSITORY};
use windows::Win32::UI::WindowsAndMessaging::{
    CallWindowProcW, CreateWindowExA, CreateWindowExW, DefWindowProcW, DestroyWindow, GetClassInfoExW,
    RegisterClassExW, RegisterClassW, SetWindowLongW, SetWindowTextW, CS_GLOBALCLASS, ES_AUTOHSCROLL, ES_MULTILINE,
    HMENU, WINDOW_EX_STYLE, WINDOW_STYLE, WNDCLASSEXW, WNDCLASSW, WNDPROC, WS_CHILD, WS_POPUP, WS_VISIBLE,
};
use windows_core::{s, w, HSTRING, PCWSTR};

thread_local! {
    /// 自訂視窗對 IMR_DOCUMENTFEED 的回覆（每個測試執行緒各一份）。
    static REPLY: Cell<Reply> = const { Cell::new(Reply::Unsupported) };
}

#[derive(Clone, Copy)]
enum Reply {
    /// 不處理，交給 DefWindowProc。
    Unsupported,
    /// 問大小時回這個值，不填緩衝區。
    Size(isize),
    /// 問大小時回合理的值，填緩衝區時回 0。
    Refuse,
    /// 「買了3」，游標在最後。
    Digit,
}

/// 「買了3」、游標在最後的 RECONVERTSTRING。
fn digit_feed() -> Vec<u8> {
    let units: Vec<u16> = "買了3".encode_utf16().collect();
    let mut out = Vec::new();
    for value in [32 + units.len() * 2 + 2, 0, units.len(), 32, 0, units.len() * 2, 0, units.len() * 2] {
        out.extend_from_slice(&(value as u32).to_le_bytes());
    }
    for unit in units.iter().chain(std::iter::once(&0)) {
        out.extend_from_slice(&unit.to_le_bytes());
    }
    out
}

unsafe extern "system" fn host_proc(hwnd: HWND, msg: u32, wparam: WPARAM, lparam: LPARAM) -> LRESULT {
    if msg == WM_IME_REQUEST && wparam.0 == IMR_DOCUMENTFEED as usize {
        let feed = digit_feed();
        match REPLY.get() {
            Reply::Unsupported => {}
            Reply::Size(size) => return LRESULT(size),
            Reply::Refuse => return LRESULT(if lparam.0 == 0 { feed.len() as isize } else { 0 }),
            Reply::Digit => {
                if lparam.0 != 0 {
                    std::ptr::copy_nonoverlapping(feed.as_ptr(), lparam.0 as *mut u8, feed.len());
                }
                return LRESULT(feed.len() as isize);
            }
        }
    }
    DefWindowProcW(hwnd, msg, wparam, lparam)
}

const HOST_CLASS: PCWSTR = w!("ZcDigitHost");

/// 畫面外的頂層視窗，當控制項的父視窗（也是不回應 IMR_DOCUMENTFEED 的自訂視窗）。
fn top() -> HWND {
    unsafe {
        let class = WNDCLASSW {
            lpfnWndProc: Some(host_proc),
            hInstance: GetModuleHandleW(None).unwrap().into(),
            lpszClassName: HOST_CLASS,
            ..Default::default()
        };
        let _ = RegisterClassW(&class); // 同一個行程的其他測試可能已經註冊過
        CreateWindowExW(
            WINDOW_EX_STYLE(0),
            HOST_CLASS,
            w!(""),
            WINDOW_STYLE(WS_POPUP.0 | WS_VISIBLE.0),
            -32000,
            -32000,
            400,
            200,
            None,
            None::<HMENU>,
            None,
            None,
        )
        .unwrap()
    }
}

fn child(parent: HWND, class: PCWSTR, style: i32, width: i32) -> HWND {
    unsafe {
        CreateWindowExW(
            WINDOW_EX_STYLE(0),
            class,
            w!(""),
            WINDOW_STYLE(WS_CHILD.0 | WS_VISIBLE.0 | style as u32),
            0,
            0,
            width,
            120,
            Some(parent),
            None::<HMENU>,
            None,
            None,
        )
        .unwrap()
    }
}

fn message(hwnd: HWND, msg: u32, w: usize, l: isize) -> isize {
    unsafe { SendMessageW(hwnd, msg, Some(WPARAM(w)), Some(LPARAM(l))).0 }
}

/// 換掉控制項的文字，選取 `start..end`（相等就是游標）。
fn put(hwnd: HWND, text: &str, start: usize, end: usize) {
    unsafe { SetWindowTextW(hwnd, &HSTRING::from(text)).unwrap() };
    message(hwnd, EM_SETSEL, start, end as isize);
}

/// 「執行」對話框那種 IMM32 程式的 EDIT：TSF 的文件讀不到字，要從控制項讀。修正前每個位置都是 Some(false)。
#[test]
fn imm32_edit_reads_the_character_before_the_caret() {
    let top = top();
    let edit = child(top, w!("EDIT"), ES_AUTOHSCROLL, 300);
    let ctx = context_on(edit, TS_SS_TRANSITORY);
    for (text, caret, expected) in [
        ("買了3", 3, Some(true)),
        ("買了３", 3, Some(true)),
        ("買了3箱", 3, Some(true)),
        ("買了3箱", 4, Some(false)),
        ("買了3箱", 2, Some(false)),
        ("3", 0, Some(false)), // 文件開頭
        ("", 0, Some(false)),
    ] {
        put(edit, text, caret, caret);
        assert_eq!(digit_before(&ctx, 1, 0), expected, "{text} 游標在 {caret}");
    }
    // 有選取範圍：不知道，不送。
    put(edit, "買了3箱", 2, 3);
    assert_eq!(digit_before(&ctx, 1, 0), None);

    // 不是系統相容層的 context 照舊讀 TSF 的文件，不問視窗：自己啟用 TSF 的程式（啟用旗標不是 0，Windows Terminal），
    // 以及宣告 TS_SS_NOHIDDENTEXT 的 Chromium。這個假文件是空的，所以是文件開頭。
    put(edit, "買了3", 3, 3);
    assert_eq!(digit_before(&ctx, 1, 0x4000_0040), Some(false));
    let chromium = context_on(edit, TS_SS_TRANSITORY | TS_SS_NOHIDDENTEXT);
    assert_eq!(digit_before(&chromium, 1, 0), Some(false));
    unsafe {
        let _ = DestroyWindow(top);
    }
}

/// 多行 EDIT：硬換行後的行首不是數字；自動換行時前一個字是上一個顯示行的最後一個字。
#[test]
fn edit_line_starts() {
    let top = top();
    let plain = child(top, w!("EDIT"), ES_MULTILINE | ES_AUTOHSCROLL, 300);
    for (caret, expected) in [
        (3, (Some(true), "edit_read")),         // ab3|
        (2, (Some(false), "edit_read")),        // ab|3
        (5, (Some(false), "edit_line_start")),  // 第二行開頭，前面是換行
        (8, (Some(true), "edit_read")),         // cd5|
        (10, (Some(false), "edit_line_start")), // 最後的空行
        (0, (Some(false), "edit_start")),
    ] {
        put(plain, "ab3\r\ncd5\r\n", caret, caret);
        assert_eq!(window_digit_before(plain), expected, "游標在 {caret}");
    }
    put(plain, "ab3\r\ncd5\r\n", 1, 3);
    assert_eq!(window_digit_before(plain), (None, "edit_selection"));

    // 沒有 ES_AUTOHSCROLL 的窄 EDIT 會自動換行：第二個顯示行的開頭緊接著上一行的最後一個字。
    let wrapped = child(top, w!("EDIT"), ES_MULTILINE, 60);
    for (text, expected) in [("1".repeat(60), Some(true)), ("一".repeat(60), Some(false))] {
        put(wrapped, &text, 0, 0);
        assert!(message(wrapped, EM_GETLINECOUNT, 0, 0) > 1, "測試前提：文字要換行");
        let second = message(wrapped, EM_LINEINDEX, 1, 0);
        assert!(second > 0 && (second as usize) < 60);
        message(wrapped, EM_SETSEL, second as usize, second);
        assert_eq!(window_digit_before(wrapped), (expected, "edit_read"), "{}", &text[..1]);
    }
    unsafe {
        let _ = DestroyWindow(top);
    }
}

/// 密碼框與 ANSI 視窗不讀。
#[test]
fn edit_password_and_ansi_windows_are_not_read() {
    const ES_PASSWORD_STYLE: i32 = ES_PASSWORD;
    let top = top();
    let password = child(top, w!("EDIT"), ES_PASSWORD_STYLE | ES_AUTOHSCROLL, 300);
    put(password, "abc3", 4, 4);
    assert_eq!(window_digit_before(password), (None, "edit_password"));
    let ansi = unsafe {
        CreateWindowExA(
            WINDOW_EX_STYLE(0),
            s!("EDIT"),
            s!("abc3"),
            WINDOW_STYLE(WS_CHILD.0 | WS_VISIBLE.0 | ES_AUTOHSCROLL as u32),
            0,
            0,
            300,
            40,
            Some(top),
            None::<HMENU>,
            None,
            None,
        )
        .unwrap()
    };
    message(ansi, EM_SETSEL, 4, 4);
    assert_eq!(window_digit_before(ansi), (None, "imm32_ansi_window"));
    unsafe {
        let _ = DestroyWindow(top);
    }
}

/// IMM32 模式的 RichEdit 不是系統 EDIT，回應 IMR_DOCUMENTFEED（游標所在的段落）。
#[test]
fn rich_edit_answers_document_feed() {
    if unsafe { LoadLibraryW(w!("Msftedit.dll")) }.is_err() {
        eprintln!("略過：沒有 Msftedit.dll");
        return;
    }
    let top = top();
    let rich = child(top, w!("RICHEDIT50W"), ES_MULTILINE, 300);
    // RichEdit 內部的段落結尾是一個 CR：「ab3」「cd5ef」兩段，第二段從位置 4 開始。
    for (caret, expected) in [
        (3, (Some(true), "feed_read")),
        (2, (Some(false), "feed_read")),
        (4, (Some(false), "feed_start")),
        (7, (Some(true), "feed_read")),
        (9, (Some(false), "feed_read")),
    ] {
        put(rich, "ab3\r\ncd5ef", caret, caret);
        assert_eq!(window_digit_before(rich), expected, "游標在 {caret}");
    }
    put(rich, "ab3\r\ncd5ef", 1, 3);
    assert_eq!(window_digit_before(rich), (None, "feed_selection"));
    unsafe {
        let _ = DestroyWindow(top);
    }
}

/// 其他 IMM32 視窗：不支援 IMR_DOCUMENTFEED、回的大小不合理或不填緩衝區，都是讀不到；照規矩回覆的讀得到。
#[test]
fn custom_window_document_feed() {
    let top = top();
    for (reply, expected) in [
        (Reply::Unsupported, (None, "feed_unsupported")),
        (Reply::Size(-1), (None, "feed_unsupported")),
        (Reply::Size(16), (None, "feed_size")),
        (Reply::Size(FEED_LIMIT as isize + 1), (None, "feed_size")),
        (Reply::Refuse, (None, "feed_refused")),
        (Reply::Digit, (Some(true), "feed_read")),
    ] {
        REPLY.set(reply);
        assert_eq!(window_digit_before(top), expected);
    }
    // 經 context：IMM32 程式的自訂視窗（Scintilla 這類自己回應 IMR_DOCUMENTFEED 的編輯器）。
    let ctx = context_on(top, TS_SS_TRANSITORY);
    assert_eq!(digit_before(&ctx, 1, 0), Some(true));
    REPLY.set(Reply::Unsupported);
    assert_eq!(digit_before(&ctx, 1, 0), None);
    unsafe {
        let _ = DestroyWindow(top);
    }
}

/// activation context 的函式與結構不在 windows crate 啟用的功能裡，以 windows-link 匯入（同 msctf.rs）。
mod actctx {
    /// ACTCTXW。
    #[repr(C)]
    pub struct ActCtx {
        pub size: u32,
        pub flags: u32,
        pub source: *const u16,
        pub processor_architecture: u16,
        pub lang_id: u16,
        pub assembly_directory: *const u16,
        pub resource_name: *const u16,
        pub application_name: *const u16,
        pub module: *mut core::ffi::c_void,
    }
    windows_link::link!("kernel32.dll" "system" fn CreateActCtxW(context: *const ActCtx) -> *mut core::ffi::c_void);
    windows_link::link!("kernel32.dll" "system" fn ActivateActCtx(context: *mut core::ffi::c_void, cookie: *mut usize) -> i32);
    windows_link::link!("kernel32.dll" "system" fn DeactivateActCtx(flags: u32, cookie: usize) -> i32);
    windows_link::link!("kernel32.dll" "system" fn ReleaseActCtx(context: *mut core::ffi::c_void));
}

/// 在這個執行緒活化 comctl32 第 6 版（視覺樣式）的 activation context，放掉時還原。活化期間「EDIT」這個類別名稱
/// 指向 comctl32 的 EDIT，不是 user32 的；WinForms 的 `Application.EnableVisualStyles()` 就是在建立視窗類別與
/// 視窗時活化這樣的 context。測試行程本身沒有 manifest，所以其他測試拿到的都是 user32 的 EDIT。
struct VisualStyles {
    context: *mut core::ffi::c_void,
    cookie: usize,
}

impl VisualStyles {
    fn activate() -> Self {
        const MANIFEST: &str = r#"<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0"><dependency><dependentAssembly>
<assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/>
</dependentAssembly></dependency></assembly>"#;
        let path = std::env::temp_dir().join(format!("zc-comctl6-{}.manifest", std::process::id()));
        std::fs::write(&path, MANIFEST).unwrap();
        let source = HSTRING::from(path.as_os_str());
        let request = actctx::ActCtx {
            size: std::mem::size_of::<actctx::ActCtx>() as u32,
            flags: 0,
            source: source.as_ptr(),
            processor_architecture: 0,
            lang_id: 0,
            assembly_directory: std::ptr::null(),
            resource_name: std::ptr::null(),
            application_name: std::ptr::null(),
            module: std::ptr::null_mut(),
        };
        let context = unsafe { actctx::CreateActCtxW(&request) };
        let _ = std::fs::remove_file(&path);
        assert!(context as isize != -1 && !context.is_null(), "建不出 comctl32 第 6 版的 activation context");
        let mut cookie = 0usize;
        assert!(unsafe { actctx::ActivateActCtx(context, &mut cookie) } != 0);
        Self { context, cookie }
    }
}

impl Drop for VisualStyles {
    fn drop(&mut self) {
        unsafe {
            actctx::DeactivateActCtx(0, self.cookie);
            actctx::ReleaseActCtx(self.context);
        }
    }
}

/// WinForms TextBox 的類別名稱樣式（真的名稱最後一段是應用程式網域的雜湊）。
const WINFORMS_STYLED: PCWSTR = w!("WindowsForms10.EDIT.app.0.test");
const WINFORMS_PLAIN: PCWSTR = w!("WindowsForms10.EDIT.app.0.test_plain");
/// 兩個衍生類別各自的基底（EDIT 原本的視窗程序）。
static STYLED_BASE: AtomicUsize = AtomicUsize::new(0);
static PLAIN_BASE: AtomicUsize = AtomicUsize::new(0);

unsafe fn to_base(base: &AtomicUsize, hwnd: HWND, msg: u32, wparam: WPARAM, lparam: LPARAM) -> LRESULT {
    let base: WNDPROC = std::mem::transmute::<usize, WNDPROC>(base.load(Ordering::SeqCst));
    CallWindowProcW(base, hwnd, msg, wparam, lparam)
}

unsafe extern "system" fn styled_proc(hwnd: HWND, msg: u32, wparam: WPARAM, lparam: LPARAM) -> LRESULT {
    to_base(&STYLED_BASE, hwnd, msg, wparam, lparam)
}

unsafe extern "system" fn plain_proc(hwnd: HWND, msg: u32, wparam: WPARAM, lparam: LPARAM) -> LRESULT {
    to_base(&PLAIN_BASE, hwnd, msg, wparam, lparam)
}

/// 以 EDIT 為基底註冊衍生類別（superclass），跟 WinForms 的 TextBox 一樣：類別名稱是自己的，視窗程序把訊息轉給
/// EDIT 原本的視窗程序。EDIT 在目前的 activation context 裡查：活化了 comctl32 第 6 版就是 comctl32 的，否則是 user32 的。
fn wrap_edit(name: PCWSTR, base: &AtomicUsize, proc: WNDPROC) {
    unsafe {
        let mut class = WNDCLASSEXW { cbSize: std::mem::size_of::<WNDCLASSEXW>() as u32, ..Default::default() };
        GetClassInfoExW(None, w!("EDIT"), &mut class).unwrap();
        base.store(class.lpfnWndProc.map_or(0, |f| f as usize), Ordering::SeqCst);
        class.style &= !CS_GLOBALCLASS;
        class.lpfnWndProc = proc;
        class.hInstance = GetModuleHandleW(None).unwrap().into();
        class.lpszMenuName = PCWSTR::null();
        class.lpszClassName = name;
        assert!(RegisterClassExW(&class) != 0, "註冊不了 EDIT 的衍生類別");
    }
}

fn real_class(hwnd: HWND) -> String {
    let mut name = [0u16; 256];
    let len = unsafe { RealGetWindowClassW(hwnd, &mut name) } as usize;
    String::from_utf16_lossy(&name[..len])
}

/// WinForms 的 TextBox 是 EDIT 的衍生類別，類別名稱是 `WindowsForms10.EDIT.app.0.…`。沒有視覺樣式時基底是 user32 的
/// EDIT，RealGetWindowClass 回 Edit；有 `Application.EnableVisualStyles()` 時（一般的 WinForms 程式）基底是
/// comctl32 第 6 版的 EDIT，RealGetWindowClass 回的是衍生類別的名稱。修正前後者被當成不是 EDIT、改問
/// IMR_DOCUMENTFEED，EDIT 不回應（.14 的 tsf.log：`digit before probe=feed_unsupported`），每個位置都讀不到。
#[test]
fn winforms_text_box_reads_like_the_system_edit() {
    let top = top();
    wrap_edit(WINFORMS_PLAIN, &PLAIN_BASE, Some(plain_proc));
    let plain = child(top, WINFORMS_PLAIN, ES_AUTOHSCROLL, 300);
    let (styled, lines, password) = {
        let _styles = VisualStyles::activate();
        wrap_edit(WINFORMS_STYLED, &STYLED_BASE, Some(styled_proc));
        (
            child(top, WINFORMS_STYLED, ES_AUTOHSCROLL, 300),
            child(top, WINFORMS_STYLED, ES_MULTILINE | ES_AUTOHSCROLL, 300),
            child(top, WINFORMS_STYLED, ES_PASSWORD | ES_AUTOHSCROLL, 300),
        )
    };
    // 測試前提：跟 .14 上 WinForms 的 TextBox 一樣。
    assert_eq!(real_class(plain), "Edit");
    assert_eq!(real_class(styled), "WindowsForms10.EDIT.app.0.test");

    for edit in [plain, styled] {
        let ctx = context_on(edit, TS_SS_TRANSITORY);
        for (text, caret, expected) in [
            ("買了3", 3, (Some(true), "edit_read")),
            ("買了３", 3, (Some(true), "edit_read")),
            ("買了3箱", 3, (Some(true), "edit_read")),
            ("買了3箱", 4, (Some(false), "edit_read")),
            ("買了3箱", 2, (Some(false), "edit_read")),
            ("3", 0, (Some(false), "edit_start")),
            ("", 0, (Some(false), "edit_start")),
        ] {
            put(edit, text, caret, caret);
            assert_eq!(window_digit_before(edit), expected, "{} {text} 游標在 {caret}", real_class(edit));
            assert_eq!(digit_before(&ctx, 1, 0), expected.0, "{} {text} 游標在 {caret}", real_class(edit));
        }
        put(edit, "買了3箱", 2, 3);
        assert_eq!(window_digit_before(edit), (None, "edit_selection"));
        assert_eq!(digit_before(&ctx, 1, 0), None);
    }

    // 多行（TextBox.Multiline）：硬換行後的行首不是數字。
    for (caret, expected) in [
        (3, (Some(true), "edit_read")),
        (2, (Some(false), "edit_read")),
        (5, (Some(false), "edit_line_start")),
        (8, (Some(true), "edit_read")),
        (10, (Some(false), "edit_line_start")),
        (0, (Some(false), "edit_start")),
    ] {
        put(lines, "ab3\r\ncd5\r\n", caret, caret);
        assert_eq!(window_digit_before(lines), expected, "游標在 {caret}");
    }

    // 密碼框不讀。UseSystemPasswordChar 是建立時就帶 ES_PASSWORD；PasswordChar 是事後 EM_SETPASSWORDCHAR，
    // EDIT 自己會補上 ES_PASSWORD（.14 實測兩種都帶）。
    put(password, "abc3", 4, 4);
    assert_eq!(window_digit_before(password), (None, "edit_password"));
    for edit in [plain, styled] {
        put(edit, "abc3", 4, 4);
        assert_eq!(window_digit_before(edit), (Some(true), "edit_read"));
        message(edit, EM_SETPASSWORDCHAR, '*' as usize, 0);
        let style = unsafe { GetWindowLongW(edit, GWL_STYLE) };
        assert!(style & ES_PASSWORD != 0, "測試前提：設了遮罩字元的 EDIT 帶 ES_PASSWORD");
        assert_eq!(window_digit_before(edit), (None, "edit_password"), "{}", real_class(edit));
        // 樣式被程式拿掉、遮罩字元還在：照樣不讀。
        unsafe { SetWindowLongW(edit, GWL_STYLE, style & !ES_PASSWORD) };
        if unsafe { GetWindowLongW(edit, GWL_STYLE) } & ES_PASSWORD == 0 && message(edit, EM_GETPASSWORDCHAR, 0, 0) != 0 {
            assert_eq!(window_digit_before(edit), (None, "edit_password"), "{}", real_class(edit));
        } else {
            eprintln!("略過：{} 拿掉 ES_PASSWORD 後遮罩字元也沒了", real_class(edit));
        }
    }
    unsafe {
        let _ = DestroyWindow(top);
    }
}
