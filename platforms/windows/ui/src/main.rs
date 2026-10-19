//! 0c-ui：候選窗與 Tab 補全提示。跑在 Zuin 自己的行程，接收 TSF DLL 經 named pipe 送來的
//! UiMessage（候選、反白、頁碼、建議、游標矩形），在游標旁畫一個不搶焦點的視窗。
//! 每個使用者工作階段一個實例；由 DLL 在需要時啟動。
#![windows_subsystem = "windows"]

mod menu;
use zuin_ui::candidate;

use std::ffi::c_void;

use windows::Win32::Foundation::{
    CloseHandle, GetLastError, LocalFree, ERROR_ALREADY_EXISTS, HANDLE, HLOCAL, HWND, LPARAM,
    LRESULT, WPARAM,
};
use windows::Win32::Security::Authorization::{
    ConvertStringSecurityDescriptorToSecurityDescriptorW, SDDL_REVISION_1,
};
use windows::Win32::Security::{PSECURITY_DESCRIPTOR, SECURITY_ATTRIBUTES};
use windows::Win32::Storage::FileSystem::{ReadFile, PIPE_ACCESS_INBOUND};
use windows::Win32::System::LibraryLoader::GetModuleHandleW;
use windows::Win32::System::Pipes::{
    ConnectNamedPipe, CreateNamedPipeW, DisconnectNamedPipe, PIPE_READMODE_BYTE, PIPE_TYPE_BYTE,
    PIPE_UNLIMITED_INSTANCES, PIPE_WAIT,
};
use windows::Win32::System::Threading::CreateMutexW;
use windows::Win32::UI::HiDpi::{
    SetProcessDpiAwarenessContext, DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2,
};
use windows::Win32::UI::WindowsAndMessaging::{
    DefWindowProcW, DispatchMessageW, GetMessageW, PostMessageW, PostQuitMessage,
    TranslateMessage, MSG, WM_APP, WM_DESTROY, WM_ENDSESSION,
};
use windows_core::{w, PCWSTR};
use zuin_win_common::{legacy_ui_pipe, pipe_sddl, ui_pipe, UiMessage, MAX_FRAME};

const WM_ZUIN_PRESENT: u32 = WM_APP + 1;

fn main() {
    unsafe {
        // 單一實例：第二個直接退出（DLL 在多個宿主同時啟動時會發生）。
        let mutex = CreateMutexW(None, false, w!("Local\\0c-ui"));
        if mutex.is_err() || GetLastError() == ERROR_ALREADY_EXISTS {
            return;
        }
        let _ = SetProcessDpiAwarenessContext(DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2);
        let instance = GetModuleHandleW(None).unwrap_or_default();
        let class = w!("ZuinCandidateWindow");
        candidate::register_class(instance.into(), class, Some(wndproc));
        let hwnd = candidate::create(instance.into(), class, None)
        .expect("建立候選窗");
        let hwnd_value = hwnd.0 as isize;
        std::thread::spawn(move || pipe_server(&ui_pipe(), HWND(hwnd_value as *mut c_void)));
        // 升級過渡期：仍載入中的舊版 DLL 連 zuin-ui 管道；呈現訊息格式相同，一併服務。
        std::thread::spawn(move || pipe_server(&legacy_ui_pipe(), HWND(hwnd_value as *mut c_void)));
        // 輸入法選單：狀態查詢與白名單內的 0c-update 命令（#13）。
        std::thread::spawn(menu::server);
        // 自動更新元件被防毒軟體移除時重建或通知（#22）。
        std::thread::spawn(menu::watch_updater);
        let mut msg = MSG::default();
        while GetMessageW(&mut msg, None, 0, 0).as_bool() {
            let _ = TranslateMessage(&msg);
            DispatchMessageW(&msg);
        }
    }
}

/// 管道伺服器：每個連線一條執行緒；每個訊框解成 UiMessage 後 PostMessage 給視窗執行緒。
fn pipe_server(pipe: &str, hwnd: HWND) {
    let name = zuin_win_common::wide(pipe);
    let sddl = zuin_win_common::wide(&pipe_sddl());
    loop {
        unsafe {
            let mut sd = PSECURITY_DESCRIPTOR::default();
            if ConvertStringSecurityDescriptorToSecurityDescriptorW(
                PCWSTR::from_raw(sddl.as_ptr()),
                SDDL_REVISION_1,
                &mut sd,
                None,
            )
            .is_err()
            {
                return;
            }
            let sa = SECURITY_ATTRIBUTES {
                nLength: std::mem::size_of::<SECURITY_ATTRIBUTES>() as u32,
                lpSecurityDescriptor: sd.0,
                bInheritHandle: false.into(),
            };
            let pipe = CreateNamedPipeW(
                PCWSTR::from_raw(name.as_ptr()),
                PIPE_ACCESS_INBOUND,
                PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_WAIT,
                PIPE_UNLIMITED_INSTANCES,
                1 << 16,
                1 << 16,
                0,
                Some(&sa),
            );
            let _ = LocalFree(Some(HLOCAL(sd.0)));
            if pipe.is_invalid() {
                std::thread::sleep(std::time::Duration::from_millis(500));
                continue;
            }
            if ConnectNamedPipe(pipe, None).is_err() {
                let _ = CloseHandle(pipe);
                continue;
            }
            let h = pipe.0 as isize;
            let hw = hwnd.0 as isize;
            std::thread::spawn(move || serve(HANDLE(h as *mut c_void), HWND(hw as *mut c_void)));
        }
    }
}

fn read_exact(h: HANDLE, buf: &mut [u8]) -> bool {
    let mut off = 0usize;
    while off < buf.len() {
        let mut n = 0u32;
        let r = unsafe { ReadFile(h, Some(&mut buf[off..]), Some(&mut n), None) };
        if r.is_err() || n == 0 {
            return false;
        }
        off += n as usize;
    }
    true
}

fn serve(h: HANDLE, hwnd: HWND) {
    loop {
        let mut hdr = [0u8; 4];
        if !read_exact(h, &mut hdr) {
            break;
        }
        let n = u32::from_le_bytes(hdr);
        if n > MAX_FRAME {
            break;
        }
        let mut body = vec![0u8; n as usize];
        if !read_exact(h, &mut body) {
            break;
        }
        if let Ok(m) = serde_json::from_slice::<UiMessage>(&body) {
            let boxed = Box::into_raw(Box::new(m));
            unsafe {
                if PostMessageW(
                    Some(hwnd),
                    WM_ZUIN_PRESENT,
                    WPARAM(0),
                    LPARAM(boxed as isize),
                )
                .is_err()
                {
                    drop(Box::from_raw(boxed));
                }
            }
        }
    }
    unsafe {
        let _ = DisconnectNamedPipe(h);
        let _ = CloseHandle(h);
    }
}


unsafe extern "system" fn wndproc(hwnd: HWND, msg: u32, wparam: WPARAM, lparam: LPARAM) -> LRESULT {
    match msg {
        WM_ZUIN_PRESENT => {
            let m = Box::from_raw(lparam.0 as *mut UiMessage);
            candidate::present(hwnd, *m);
            LRESULT(0)
        }
        WM_ENDSESSION | WM_DESTROY => {
            PostQuitMessage(0);
            LRESULT(0)
        }
        _ => candidate::handle(hwnd, msg, wparam, lparam)
            .unwrap_or_else(|| DefWindowProcW(hwnd, msg, wparam, lparam)),
    }
}
