//! 輸入法選單的代理（#13）：TSF DLL 可能在沙箱宿主內，不能啟動程式或讀個人資料目錄，
//! 由 0c-ui 回答狀態並執行白名單內的 0c-update 命令。雙向管道，一問一答。
use std::ffi::c_void;
use std::os::windows::process::CommandExt;
use std::path::PathBuf;
use std::process::{Command, Stdio};
use std::sync::Mutex;
use std::time::{Duration, Instant};

use windows::Win32::Foundation::{CloseHandle, LocalFree, HANDLE, HLOCAL};
use windows::Win32::Security::Authorization::{
    ConvertStringSecurityDescriptorToSecurityDescriptorW, SDDL_REVISION_1,
};
use windows::Win32::Security::{PSECURITY_DESCRIPTOR, SECURITY_ATTRIBUTES};
use windows::Win32::Storage::FileSystem::{FlushFileBuffers, ReadFile, WriteFile, PIPE_ACCESS_DUPLEX};
use windows::Win32::System::Pipes::{
    ConnectNamedPipe, CreateNamedPipeW, DisconnectNamedPipe, PIPE_READMODE_BYTE, PIPE_TYPE_BYTE,
    PIPE_UNLIMITED_INSTANCES, PIPE_WAIT,
};
use windows_core::PCWSTR;
use zuin_win_common::menu::{allowed_command, MenuRequest};
use zuin_win_common::{menu_pipe, pipe_sddl, wide, MAX_FRAME};

const CREATE_NO_WINDOW: u32 = 0x0800_0000;
const DETACHED_PROCESS: u32 = 0x0000_0008;
const CACHE: Duration = Duration::from_secs(15);

static STATUS: Mutex<Option<(Instant, String)>> = Mutex::new(None);

/// 排程使用的穩定 0c-update；沒有時用與 0c-ui 同一版本目錄的那一份。
fn updater() -> PathBuf {
    if let Ok(base) = std::env::var("LOCALAPPDATA") {
        let p = PathBuf::from(base).join("Zuin").join("bin").join("0c-update.exe");
        if p.is_file() {
            return p;
        }
    }
    std::env::current_exe().ok().and_then(|p| p.parent().map(|d| d.join("0c-update.exe"))).unwrap_or_default()
}

/// 啟動 2 分鐘後與之後每 24 小時，以版本目錄內的 0c-update 執行 `schedule repair -notify`：
/// 排程或穩定副本被防毒軟體移除時重建一次，重建後又被移除則停止重建並通知使用者（規則在 0c-update）。
/// 不用穩定副本：它正是可能被移除的那一份。
pub fn watch_updater() {
    let Some(versioned) = std::env::current_exe().ok().and_then(|p| p.parent().map(|d| d.join("0c-update.exe"))) else {
        return;
    };
    std::thread::sleep(Duration::from_secs(120));
    loop {
        if versioned.is_file() {
            let _ = Command::new(&versioned)
                .args(["schedule", "repair", "-notify"])
                .creation_flags(CREATE_NO_WINDOW)
                .stdin(Stdio::null())
                .stdout(Stdio::null())
                .stderr(Stdio::null())
                .status();
            *STATUS.lock().unwrap() = None;
        }
        std::thread::sleep(Duration::from_secs(24 * 60 * 60));
    }
}

fn status() -> String {
    if let Some((at, json)) = STATUS.lock().unwrap().as_ref() {
        if at.elapsed() < CACHE {
            return json.clone();
        }
    }
    let out = Command::new(updater())
        .args(["status", "--json"])
        .creation_flags(CREATE_NO_WINDOW)
        .stdin(Stdio::null())
        .stderr(Stdio::null())
        .output();
    let json = match out {
        Ok(o) if o.status.success() => String::from_utf8_lossy(&o.stdout).trim().to_string(),
        _ => String::new(),
    };
    *STATUS.lock().unwrap() = Some((Instant::now(), json.clone()));
    json
}

fn run(args: &[String]) -> bool {
    if !allowed_command(args) {
        return false;
    }
    *STATUS.lock().unwrap() = None;
    Command::new(updater())
        .args(args)
        .creation_flags(CREATE_NO_WINDOW | DETACHED_PROCESS)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .is_ok()
}

fn read_exact(h: HANDLE, buf: &mut [u8]) -> bool {
    let mut off = 0usize;
    while off < buf.len() {
        let mut n = 0u32;
        if unsafe { ReadFile(h, Some(&mut buf[off..]), Some(&mut n), None) }.is_err() || n == 0 {
            return false;
        }
        off += n as usize;
    }
    true
}

fn serve(h: HANDLE) {
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
        let reply = match serde_json::from_slice::<MenuRequest>(&body) {
            Ok(r) if r.op == "status" => status(),
            Ok(r) if r.op == "run" => format!("{{\"ok\":{}}}", run(&r.args)),
            _ => "{\"ok\":false}".to_string(),
        };
        let body = if reply.is_empty() { "null".to_string() } else { reply };
        let mut bytes = (body.len() as u32).to_le_bytes().to_vec();
        bytes.extend_from_slice(body.as_bytes());
        let mut written = 0u32;
        if unsafe { WriteFile(h, Some(&bytes), Some(&mut written), None) }.is_err() {
            break;
        }
        unsafe {
            let _ = FlushFileBuffers(h);
        }
    }
    unsafe {
        let _ = DisconnectNamedPipe(h);
        let _ = CloseHandle(h);
    }
}

pub fn server() {
    let name = wide(&menu_pipe());
    let sddl = wide(&pipe_sddl());
    loop {
        unsafe {
            let mut sd = PSECURITY_DESCRIPTOR::default();
            if ConvertStringSecurityDescriptorToSecurityDescriptorW(PCWSTR::from_raw(sddl.as_ptr()), SDDL_REVISION_1, &mut sd, None)
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
                PIPE_ACCESS_DUPLEX,
                PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_WAIT,
                PIPE_UNLIMITED_INSTANCES,
                1 << 16,
                1 << 16,
                0,
                Some(&sa),
            );
            let _ = LocalFree(Some(HLOCAL(sd.0)));
            if pipe.is_invalid() {
                std::thread::sleep(Duration::from_millis(500));
                continue;
            }
            if ConnectNamedPipe(pipe, None).is_err() {
                let _ = CloseHandle(pipe);
                continue;
            }
            let h = pipe.0 as isize;
            std::thread::spawn(move || serve(HANDLE(h as *mut c_void)));
        }
    }
}
