//! 候選窗只回送 ID 與畫面版本，由原 TSF 執行緒核對焦點後送核心。
use crate::service::TextService_Impl;
use windows::Win32::Foundation::{HWND, LPARAM, LRESULT, WPARAM};
use windows::Win32::UI::TextServices::{
    ITfContext, ITfEditSession, ITfEditSession_Impl, ITfKeyEventSink, TF_ES_ASYNCDONTCARE, TF_ES_READWRITE,
};
use windows::Win32::UI::WindowsAndMessaging::*;
use windows_core::{implement, w, ComObject, IUnknownImpl};
use zuin_win_common::WM_SELECT_CANDIDATE;

pub struct CandidateWindow(HWND);
impl CandidateWindow {
    pub fn new(service: &TextService_Impl) -> windows_core::Result<Self> {
        unsafe {
            let instance = crate::dll_instance().into();
            let class = w!("0cCandidateCallback");
            RegisterClassW(&WNDCLASSW {
                lpfnWndProc: Some(callback),
                hInstance: instance,
                lpszClassName: class,
                ..Default::default()
            });
            let hwnd = CreateWindowExW(
                WINDOW_EX_STYLE(0),
                class,
                w!(""),
                WINDOW_STYLE(0),
                0,
                0,
                0,
                0,
                Some(HWND_MESSAGE),
                None,
                Some(instance),
                None,
            )?;
            SetWindowLongPtrW(hwnd, GWLP_USERDATA, service as *const TextService_Impl as _); // 32 位元版是 SetWindowLongW（i32）
            Ok(Self(hwnd))
        }
    }
    pub fn handle(&self) -> u64 {
        self.0 .0 as usize as u64
    }
    /// 稍後（訊息佇列空下來時）再確認一次組字所在的文件還有沒有焦點（#189）。
    pub fn settle_focus_later(&self) {
        unsafe {
            SetTimer(Some(self.0), SETTLE_FOCUS_TIMER, SETTLE_FOCUS_MS, None);
        }
    }
    pub fn cancel_settle_focus(&self) {
        unsafe {
            let _ = KillTimer(Some(self.0), SETTLE_FOCUS_TIMER);
        }
    }
}

const SETTLE_FOCUS_TIMER: usize = 0x0c18;
/// WM_TIMER 只在佇列沒有其他訊息時才產生：宿主接連的焦點變化（Excel 先移開焦點、再交給儲存格編輯器）都處理完才輪到它。
const SETTLE_FOCUS_MS: u32 = 50;
impl Drop for CandidateWindow {
    fn drop(&mut self) {
        unsafe {
            SetWindowLongPtrW(self.0, GWLP_USERDATA, 0);
            let _ = DestroyWindow(self.0);
            // 多個 service 共用 class 時，仍有視窗會使此呼叫失敗；最後一個離開才移除。
            let _ = UnregisterClassW(w!("0cCandidateCallback"), Some(crate::dll_instance().into()));
        }
    }
}
unsafe extern "system" fn callback(hwnd: HWND, msg: u32, wp: WPARAM, lp: LPARAM) -> LRESULT {
    if msg == WM_SELECT_CANDIDATE {
        let _ = crate::guarded("CandidateClick", || {
            let ptr = GetWindowLongPtrW(hwnd, GWLP_USERDATA) as *const TextService_Impl;
            if let Some(service) = ptr.as_ref() {
                service.select_candidate(wp.0 as u32, lp.0 as i64);
            }
            Ok(())
        });
        LRESULT(0)
    } else if msg == WM_TIMER && wp.0 == SETTLE_FOCUS_TIMER {
        let _ = KillTimer(Some(hwnd), SETTLE_FOCUS_TIMER);
        let _ = crate::guarded("SettleFocus", || {
            let ptr = GetWindowLongPtrW(hwnd, GWLP_USERDATA) as *const TextService_Impl;
            if let Some(service) = ptr.as_ref() {
                service.settle_focus();
            }
            Ok(())
        });
        LRESULT(0)
    } else {
        DefWindowProcW(hwnd, msg, wp, lp)
    }
}

/// 持有 service 的 COM 參考直到 edit session 完成；Deactivate 會清空候選使待處理點選失效。
#[implement(ITfEditSession)]
struct SelectEdit {
    _keep_alive: ITfKeyEventSink,
    service: *const TextService_Impl,
    revision: u32,
    id: i64,
}
impl Drop for SelectEdit {
    fn drop(&mut self) {
        crate::object_destroyed();
    }
}
impl ITfEditSession_Impl for SelectEdit_Impl {
    fn DoEditSession(&self, ec: u32) -> windows_core::Result<()> {
        crate::guarded("SelectEdit", || {
            unsafe {
                (*self.service).select_in_edit(ec, self.revision, self.id);
            }
            Ok(())
        })
    }
}
pub fn request(service: &TextService_Impl, ctx: &ITfContext, client: u32, revision: u32, id: i64) {
    crate::object_created();
    let obj = ComObject::new(SelectEdit {
        _keep_alive: service.to_interface(),
        service,
        revision,
        id,
    });
    let edit: ITfEditSession = obj.into_interface();
    unsafe {
        let _ = ctx.RequestEditSession(client, &edit, TF_ES_ASYNCDONTCARE | TF_ES_READWRITE);
    }
}
