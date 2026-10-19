//! 候選窗的呈現：一般應用程式交給 0c-ui（候選窗程式）——只寫不讀，短逾時；ui 不在就啟動它，
//! 送不出去只是看不到候選窗，不影響組字與提交。
//! 沙箱（AppContainer）與系統沉浸式介面（例如 Windows 搜尋框）例外：另一個行程的視窗在一般桌面層，
//! 會被這些介面蓋住，沙箱裡也啟動不了 0c-ui；改在宿主行程內、以宿主視窗為 owner 建立候選窗，
//! 和 0c-ui 共用同一份繪製（zuin_ui::candidate）。
use std::sync::OnceLock;

use windows::Win32::Foundation::{CloseHandle, GetLastError, ERROR_FILE_NOT_FOUND, ERROR_PIPE_BUSY, HANDLE, HWND, LPARAM, LRESULT, WPARAM};
use windows::Win32::Security::{GetTokenInformation, TokenIsAppContainer};
use windows::Win32::System::LibraryLoader::{GetModuleHandleW, GetProcAddress};
use windows::Win32::System::Threading::{GetCurrentProcess, OpenProcessToken};
use windows::Win32::Security::TOKEN_QUERY;
use windows::Win32::UI::WindowsAndMessaging::{DefWindowProcW, DestroyWindow, IsWindow, UnregisterClassW};
use windows_core::{s, w};
use zuin_ui::candidate;
use windows::Win32::Storage::FileSystem::{CreateFileW, FILE_FLAG_OVERLAPPED, FILE_GENERIC_WRITE, FILE_SHARE_MODE, OPEN_EXISTING};
use windows::Win32::System::Pipes::WaitNamedPipeW;
use windows_core::PCWSTR;
use zuin_win_common::{frame, ui_pipe, wide, UiMessage};

use windows::Win32::UI::TextServices::ITfDocumentMgr;

use crate::ipc::{may_spawn, spawn_sibling, write_all};
use crate::log::zlog;
use crate::uielement::Elements;

const SEND_TIMEOUT_MS: u32 = 200;
const SPAWN_WAIT_MS: u64 = 2000;

pub struct UiClient {
    pipe: Vec<u16>,
    handle: Option<HANDLE>,
    last_spawn: Option<std::time::Instant>,
    /// warm_up 啟動 0c-ui 的時間；第一次 connect 時只等到啟動後 SPAWN_WAIT_MS 為止。
    warming: Option<std::time::Instant>,
    shown: bool,
    /// 宿主行程內的候選窗與建立時的 owner；owner 換了（換視窗）就重建。
    local: Option<(HWND, HWND)>,
    /// 交給自行繪製候選的宿主（UI-less 模式）的候選清單元件；Activate 到 Deactivate 之間存在。
    elements: Option<Elements>,
}

impl Drop for UiClient {
    fn drop(&mut self) {
        self.close();
        self.destroy_local();
    }
}

const LOCAL_CLASS: windows_core::PCWSTR = w!("0cCandidateInProcess");

/// 本行程是否在 AppContainer 沙箱裡（不會變，只查一次）。
fn sandboxed() -> bool {
    static SANDBOXED: OnceLock<bool> = OnceLock::new();
    *SANDBOXED.get_or_init(|| unsafe {
        let mut token = HANDLE::default();
        if OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut token).is_err() {
            return false;
        }
        let mut value = 0u32;
        let mut len = 0u32;
        let ok = GetTokenInformation(
            token,
            TokenIsAppContainer,
            Some(&mut value as *mut u32 as *mut _),
            std::mem::size_of::<u32>() as u32,
            &mut len,
        )
        .is_ok();
        let _ = CloseHandle(token);
        ok && value != 0
    })
}

/// 視窗所在的 z 順序層級（user32 的 GetWindowBand，Windows 8 起提供但未公開文件）。
/// 一般桌面視窗是 1（ZBID_DESKTOP）；搜尋框、開始功能表等系統介面在更高的層級。取不到時視為 1。
fn window_band(hwnd: HWND) -> u32 {
    type GetWindowBand = unsafe extern "system" fn(HWND, *mut u32) -> i32;
    static PROC: OnceLock<Option<GetWindowBand>> = OnceLock::new();
    let proc = PROC.get_or_init(|| unsafe {
        let user32 = GetModuleHandleW(w!("user32.dll")).ok()?;
        GetProcAddress(user32, s!("GetWindowBand")).map(|f| std::mem::transmute::<_, GetWindowBand>(f))
    });
    let mut band = 1;
    if let Some(f) = proc {
        if unsafe { f(hwnd, &mut band) } == 0 {
            band = 1;
        }
    }
    band
}

/// 候選窗要不要畫在宿主行程內。
fn in_process(owner: HWND) -> bool {
    sandboxed() || window_band(owner) > 1
}

unsafe extern "system" fn local_proc(hwnd: HWND, msg: u32, wparam: WPARAM, lparam: LPARAM) -> LRESULT {
    // 宿主行程內不能讓 panic 穿過 wndproc（會終止宿主）。
    match std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| candidate::handle(hwnd, msg, wparam, lparam))) {
        Ok(Some(result)) => result,
        Ok(None) => DefWindowProcW(hwnd, msg, wparam, lparam),
        Err(_) => {
            crate::log::log("panic 已攔截：候選窗");
            DefWindowProcW(hwnd, msg, wparam, lparam)
        }
    }
}

impl UiClient {
    pub fn new() -> UiClient {
        UiClient {
            pipe: wide(&ui_pipe()),
            handle: None,
            last_spawn: None,
            warming: None,
            shown: false,
            local: None,
            elements: None,
        }
    }

    /// Activate 時設定、Deactivate 時清除（清除前結束仍在顯示的元件）。
    pub fn set_elements(&mut self, elements: Option<Elements>) {
        self.elements = elements;
    }

    fn present_local(&mut self, owner: HWND, message: &UiMessage) {
        let current = self
            .local
            .filter(|(hwnd, o)| *o == owner && unsafe { IsWindow(Some(*hwnd)) }.as_bool());
        let hwnd = match current {
            Some((hwnd, _)) => hwnd,
            None => {
                self.destroy_local();
                let instance = crate::dll_instance().into();
                candidate::register_class(instance, LOCAL_CLASS, Some(local_proc));
                match candidate::create(instance, LOCAL_CLASS, Some(owner)) {
                    Ok(hwnd) => {
                        zlog!("ui: 宿主行程內候選窗（層級 {}）", window_band(hwnd));
                        self.local = Some((hwnd, owner));
                        hwnd
                    }
                    Err(e) => {
                        zlog!("ui: 宿主行程內候選窗建立失敗 {}", e);
                        return;
                    }
                }
            }
        };
        candidate::present(hwnd, message.clone());
    }

    fn hide_local(&mut self) {
        if let Some((hwnd, _)) = self.local {
            candidate::present(
                hwnd,
                UiMessage {
                    show: false,
                    owner: std::process::id(),
                    ..Default::default()
                },
            );
        }
    }

    fn destroy_local(&mut self) {
        if let Some((hwnd, _)) = self.local.take() {
            unsafe {
                // owner 先關掉時系統已一併銷毀；對無效視窗呼叫沒有作用。
                let _ = DestroyWindow(hwnd);
                // 同一行程還有其他候選窗時會失敗，留給最後一個。
                let _ = UnregisterClassW(LOCAL_CLASS, Some(crate::dll_instance().into()));
            }
        }
    }

    fn close(&mut self) {
        if let Some(h) = self.handle.take() {
            unsafe {
                let _ = CloseHandle(h);
            }
        }
    }

    fn connect_once(&mut self) -> Result<(), u32> {
        unsafe {
            match CreateFileW(
                PCWSTR::from_raw(self.pipe.as_ptr()),
                FILE_GENERIC_WRITE.0,
                FILE_SHARE_MODE(0),
                None,
                OPEN_EXISTING,
                FILE_FLAG_OVERLAPPED,
                None,
            ) {
                Ok(h) => {
                    self.handle = Some(h);
                    Ok(())
                }
                Err(_) => Err(GetLastError().0),
            }
        }
    }

    /// Activate 時呼叫：0c-ui 已在執行就連上；沒有就啟動它但不等管道出現（#89）。
    /// Activate 跑在宿主的 UI 執行緒上（Outlook 啟動時一次好幾個），在這裡等最多 2 秒會卡住宿主；
    /// 等待留到第一次顯示候選時（connect），那時 0c-ui 通常早已就緒。
    pub fn warm_up(&mut self) {
        if self.handle.is_some() || sandboxed() {
            return;
        }
        if self.connect_once() == Err(ERROR_FILE_NOT_FOUND.0)
            && may_spawn(&mut self.last_spawn)
            && spawn_sibling("0c-ui.exe", "").is_some()
        {
            self.warming = self.last_spawn;
        }
    }

    /// 確保連上 0c-ui；沒有就啟動（有速率限制，ui 死掉後會再啟動）。沙箱裡一律在宿主行程內畫，不連。
    pub fn connect(&mut self) -> bool {
        if self.handle.is_some() {
            return true;
        }
        if sandboxed() {
            return false;
        }
        match self.connect_once() {
            Ok(()) => true,
            Err(code) if code == ERROR_PIPE_BUSY.0 => {
                unsafe {
                    let _ = WaitNamedPipeW(PCWSTR::from_raw(self.pipe.as_ptr()), SEND_TIMEOUT_MS);
                }
                self.connect_once().is_ok()
            }
            Err(code) if code == ERROR_FILE_NOT_FOUND.0 => {
                let wait = std::time::Duration::from_millis(SPAWN_WAIT_MS);
                // Activate 剛啟動過（warm_up）：只等剩下的時間，不再啟動第二個。
                let started = match self.warming.take().filter(|t| t.elapsed() < wait) {
                    Some(t) => t,
                    None => {
                        if !may_spawn(&mut self.last_spawn) || spawn_sibling("0c-ui.exe", "").is_none() {
                            return false;
                        }
                        std::time::Instant::now()
                    }
                };
                let deadline = started + wait;
                while std::time::Instant::now() < deadline {
                    if self.connect_once().is_ok() {
                        return true;
                    }
                    std::thread::sleep(std::time::Duration::from_millis(50));
                }
                false
            }
            Err(_) => false,
        }
    }

    /// 呈現候選窗。owner 是目前 context 的宿主視窗；沙箱或沉浸式宿主在本行程內畫，其餘交給 0c-ui。
    /// 宿主表示要自己畫候選（UI-less）時只更新候選清單元件，不顯示自己的候選窗，補全提示等也不畫（#159）。
    pub fn present(&mut self, message: &UiMessage, owner: Option<HWND>, document: Option<ITfDocumentMgr>) {
        if !self.elements.as_mut().map_or(true, |e| e.present(message, document)) {
            self.hide_windows();
            return;
        }
        let owner = owner.filter(|h| !h.is_invalid());
        if message.show {
            if let Some(owner) = owner.filter(|o| in_process(*o)) {
                if self.shown {
                    self.send(&UiMessage {
                        show: false,
                        owner: std::process::id(),
                        ..Default::default()
                    });
                }
                self.present_local(owner, message);
                return;
            }
        }
        self.hide_local();
        self.send(message);
    }

    /// 送出呈現訊息給 0c-ui。隱藏訊息只在目前顯示中才送，省掉每鍵一次寫入。
    fn send(&mut self, message: &UiMessage) {
        if !message.show && !self.shown {
            return;
        }
        if !self.connect() {
            return;
        }
        let Some(bytes) = frame(message) else { return };
        let Some(h) = self.handle else { return };
        if write_all(h, &bytes, SEND_TIMEOUT_MS) {
            self.shown = message.show;
        } else {
            zlog!("ui: 寫入失敗，斷線");
            self.close();
            self.shown = false;
        }
    }

    pub fn hide(&mut self) {
        if let Some(elements) = self.elements.as_mut() {
            elements.end();
        }
        self.hide_windows();
    }

    fn hide_windows(&mut self) {
        self.hide_local();
        self.send(&UiMessage {
            show: false,
            owner: std::process::id(),
            ..Default::default()
        });
    }
}
