//! #322：Chromium（Edge、Chrome、Electron 與 WebView2 的程式）在網頁裡按下滑鼠時，瀏覽器自己先把組字原樣定案
//! （RenderWidgetHostViewEventHandler::FinishImeCompositionSession → ImeFinishComposingText，連打到一半的注音一起），
//! 之後才請 TSF 終止組字；而 TSFTextStore::CancelComposition 期間輸入法在終止通知裡做的改寫會被丟掉
//! （is_cancel_composition_in_progress_），網頁裡就留下 ㄕ。
//!
//! 所以在 Chromium 的視窗組字時，在宿主 UI 執行緒掛只看本執行緒的滑鼠 hook（WH_MOUSE）：滑鼠鍵按下的訊息由
//! GetMessage／PeekMessage 取出、交給程式之前，先照失焦的規則送出組字（組好的字送出、未打完的注音丟掉）。瀏覽器接著處理
//! 這次點擊時已經沒有組字可定案。送出走的是平常的 edit session，文字送到的仍是組字所在的欄位（點擊還沒交給網頁）。
//! 只在組字期間掛著，組字結束、換到不是 Chromium 的視窗或停用就拿掉；不攔下點擊。
use crate::log::{zdebug, zlog};
use crate::service::{TextService, TextService_Impl};
use std::cell::Cell;
use windows::Win32::Foundation::{HWND, LPARAM, LRESULT, WPARAM};
use windows::Win32::System::Threading::GetCurrentThreadId;
use windows::Win32::UI::WindowsAndMessaging::{
    CallNextHookEx, GetAncestor, GetClassNameW, SetWindowsHookExW, UnhookWindowsHookEx, GA_ROOT, HC_ACTION, HHOOK,
    MOUSEHOOKSTRUCT, WH_MOUSE, WM_LBUTTONDBLCLK, WM_LBUTTONDOWN, WM_MBUTTONDBLCLK, WM_MBUTTONDOWN, WM_RBUTTONDBLCLK,
    WM_RBUTTONDOWN, WM_XBUTTONDBLCLK, WM_XBUTTONDOWN,
};

#[derive(Clone, Copy)]
struct Watch {
    hook: HHOOK,
    /// 掛 hook 的服務。Deactivate 與 Drop 都會先拿掉 hook，指標不會比服務活得久。
    service: *const TextService_Impl,
    /// 同一個服務的內層物件（Drop 時只拿得到它）。
    inner: *const TextService,
    /// 組字所在的 view 視窗（已確認是 Chromium 的視窗），同一個視窗不必每一鍵重查類別。
    owner: HWND,
}

thread_local! {
    static WATCH: Cell<Option<Watch>> = const { Cell::new(None) };
    /// hook 程序執行中：這時要拿掉 hook 就等程序結束再拿。
    static IN_PROC: Cell<bool> = const { Cell::new(false) };
    static STOP_AFTER_PROC: Cell<bool> = const { Cell::new(false) };
}

/// Chromium 的頂層視窗類別（Chrome、Edge、Electron、CEF、WebView2 都是 Chrome_WidgetWin_0／_1…）。
pub(crate) fn chromium_class(class: &str) -> bool {
    class
        .strip_prefix("Chrome_WidgetWin_")
        .is_some_and(|n| !n.is_empty() && n.bytes().all(|b| b.is_ascii_digit()))
}

fn chromium_window(hwnd: HWND) -> bool {
    if hwnd.is_invalid() {
        return false;
    }
    let mut name = [0u16; 64];
    let len = unsafe { GetClassNameW(hwnd, &mut name) };
    len > 0 && chromium_class(&String::from_utf16_lossy(&name[..len as usize]))
}

/// 每次套用快照後呼叫：`owner` 是組字中（自己接 TSF 的 context）那個 context 的 view 視窗，不在組字就是 None。
/// 是 Chromium 的視窗就掛上 hook，否則拿掉。
pub(crate) fn follow(service: &TextService_Impl, owner: Option<HWND>) {
    let current = WATCH.get();
    let wanted = owner.filter(|&h| current.is_some_and(|w| w.owner == h) || chromium_window(h));
    let inner = &**service as *const TextService;
    let service = service as *const TextService_Impl;
    match (wanted, current) {
        (Some(owner), Some(w)) => {
            STOP_AFTER_PROC.set(false);
            WATCH.set(Some(Watch { owner, service, inner, ..w }));
        }
        (Some(owner), None) => {
            match unsafe { SetWindowsHookExW(WH_MOUSE, Some(proc), None, GetCurrentThreadId()) } {
                Ok(hook) => {
                    zdebug!("Chromium 視窗組字：開始看滑鼠按下");
                    WATCH.set(Some(Watch { hook, service, inner, owner }));
                }
                Err(e) => zlog!("無法掛滑鼠 hook：{}", e.code()),
            }
        }
        (None, Some(w)) if w.service == service => stop(),
        _ => {}
    }
}

/// 這個服務現在有沒有掛著 hook（拿掉的要求延到 hook 程序結束時也算沒有）。
pub(crate) fn watching_for(service: &TextService_Impl) -> bool {
    !STOP_AFTER_PROC.get() && WATCH.get().is_some_and(|w| w.service == service as *const TextService_Impl)
}

/// 拿掉這個服務掛的 hook（停用）。
pub(crate) fn forget(service: &TextService_Impl) {
    if WATCH.get().is_some_and(|w| w.service == service as *const TextService_Impl) {
        stop();
    }
}

/// 服務物件釋放時（Drop 只拿得到內層的 TextService）：hook 還指著它就拿掉。
pub(crate) fn forget_dropped(inner: &TextService) {
    if WATCH.get().is_some_and(|w| w.inner == inner as *const TextService) {
        stop();
    }
}

fn stop() {
    if IN_PROC.get() {
        STOP_AFTER_PROC.set(true);
        return;
    }
    if let Some(w) = WATCH.take() {
        let _ = unsafe { UnhookWindowsHookEx(w.hook) };
        zdebug!("停止看滑鼠按下");
    }
}

fn button_down(message: u32) -> bool {
    matches!(
        message,
        WM_LBUTTONDOWN
            | WM_RBUTTONDOWN
            | WM_MBUTTONDOWN
            | WM_XBUTTONDOWN
            | WM_LBUTTONDBLCLK
            | WM_RBUTTONDBLCLK
            | WM_MBUTTONDBLCLK
            | WM_XBUTTONDBLCLK
    )
}

unsafe extern "system" fn proc(code: i32, wparam: WPARAM, lparam: LPARAM) -> LRESULT {
    // HC_ACTION：訊息已從佇列取出、即將交給程式（PM_NOREMOVE 的查看是 HC_NOREMOVE，不處理）。
    if code == HC_ACTION as i32 && button_down(wparam.0 as u32) && !IN_PROC.get() {
        let target = (lparam.0 as *const MOUSEHOOKSTRUCT).as_ref().map(|m| m.hwnd).unwrap_or_default();
        if let Some(w) = WATCH.get().filter(|_| chromium_window(GetAncestor(target, GA_ROOT))) {
            IN_PROC.set(true);
            let _ = crate::guarded("mouse_pressed", || {
                (*w.service).mouse_pressed();
                Ok(())
            });
            IN_PROC.set(false);
            if STOP_AFTER_PROC.take() {
                stop();
            }
        }
    }
    CallNextHookEx(None, code, wparam, lparam)
}

/// 測試：本執行緒現在有沒有掛著 hook。
#[cfg(test)]
pub(crate) fn watching() -> bool {
    WATCH.get().is_some()
}

/// 測試：照系統的呼叫方式把一則滑鼠訊息交給 hook 程序（不必產生真的滑鼠輸入）。
#[cfg(test)]
pub(crate) fn deliver(message: u32, hwnd: HWND) {
    let info = MOUSEHOOKSTRUCT { hwnd, ..Default::default() };
    unsafe {
        proc(HC_ACTION as i32, WPARAM(message as usize), LPARAM(&info as *const MOUSEHOOKSTRUCT as isize));
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn chromium_windows_are_recognised_by_class() {
        for class in ["Chrome_WidgetWin_0", "Chrome_WidgetWin_1", "Chrome_WidgetWin_12"] {
            assert!(chromium_class(class), "{class}");
        }
        for class in [
            "Chrome_WidgetWin_",
            "Chrome_RenderWidgetHostHWND",
            "Notepad",
            "RichEditD2DPT",
            "MozillaWindowClass",
            "chrome_widgetwin_1",
            "Chrome_WidgetWin_1x",
        ] {
            assert!(!chromium_class(class), "{class}");
        }
    }

    #[test]
    fn button_presses_only() {
        assert!(button_down(WM_LBUTTONDOWN) && button_down(WM_RBUTTONDOWN) && button_down(WM_XBUTTONDBLCLK));
        use windows::Win32::UI::WindowsAndMessaging::{WM_LBUTTONUP, WM_MOUSEMOVE, WM_MOUSEWHEEL, WM_NCLBUTTONDOWN};
        for m in [WM_MOUSEMOVE, WM_LBUTTONUP, WM_MOUSEWHEEL, WM_NCLBUTTONDOWN] {
            assert!(!button_down(m), "{m:#x}");
        }
    }
}
