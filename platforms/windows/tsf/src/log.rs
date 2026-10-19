//! 診斷紀錄：只記事件與錯誤，不記任何輸入的文字（隱私）。
//! 檔案：%LOCALAPPDATA%\Zuin\tsf.log；目錄不存在時改送 OutputDebugString。
use std::fs::OpenOptions;
use std::io::Write;
use std::path::PathBuf;
use std::time::{SystemTime, UNIX_EPOCH};

use windows::Win32::System::Diagnostics::Debug::OutputDebugStringW;
use windows_core::PCWSTR;

const MAX_BYTES: u64 = 4 << 20;

fn path() -> Option<PathBuf> {
    let base = std::env::var_os("LOCALAPPDATA")?;
    let dir = PathBuf::from(base).join("Zuin");
    if dir.is_dir() {
        Some(dir.join("tsf.log"))
    } else {
        None
    }
}

/// 除錯紀錄：只在 %LOCALAPPDATA%\Zuin\tsf-debug 這個檔案存在時才寫（會記按鍵的虛擬鍵碼，等於記下輸入內容，
/// 日常不可開啟）。啟動後只檢查一次。
pub fn debug_enabled() -> bool {
    use std::sync::OnceLock;
    static ENABLED: OnceLock<bool> = OnceLock::new();
    *ENABLED.get_or_init(|| {
        std::env::var_os("LOCALAPPDATA")
            .map(|b| PathBuf::from(b).join("Zuin").join("tsf-debug").is_file())
            .unwrap_or(false)
    })
}

pub fn log(msg: &str) {
    let now = SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_millis()).unwrap_or(0);
    let line = format!("{} pid={} {}\n", now, std::process::id(), msg);
    if let Some(p) = path() {
        if let Ok(meta) = std::fs::metadata(&p) {
            if meta.len() > MAX_BYTES {
                let _ = std::fs::remove_file(&p);
            }
        }
        if let Ok(mut f) = OpenOptions::new().create(true).append(true).open(&p) {
            let _ = f.write_all(line.as_bytes());
            return;
        }
    }
    let mut w: Vec<u16> = format!("zuin-tsf: {}", line).encode_utf16().collect();
    w.push(0);
    unsafe { OutputDebugStringW(PCWSTR::from_raw(w.as_ptr())) };
}

/// panic 時記下發生位置（檔案:行號），再由 guarded／guard 記錄攔截到的入口（#89）。
/// 只記位置、不記訊息：切字串之類的 panic 訊息會帶出使用者打的字。DLL 第一次被使用時設定一次；
/// 每個 Rust DLL 有自己的 std，這個 hook 只影響本 DLL。
pub fn install_panic_hook() {
    static ONCE: std::sync::Once = std::sync::Once::new();
    ONCE.call_once(|| {
        std::panic::set_hook(Box::new(|info| {
            let at = info
                .location()
                .map(|l| format!("{}:{}", l.file(), l.line()))
                .unwrap_or_else(|| "?".into());
            log(&format!("panic 位置：{}", at));
        }));
    });
}

/// 宿主程式的執行檔名稱（不含路徑），給事件紀錄辨認是哪個應用程式。
pub fn exe_name() -> &'static str {
    use std::sync::OnceLock;
    static NAME: OnceLock<String> = OnceLock::new();
    NAME.get_or_init(|| {
        std::env::current_exe()
            .ok()
            .and_then(|p| p.file_name().map(|n| n.to_string_lossy().into_owned()))
            .unwrap_or_else(|| "?".into())
    })
}

macro_rules! zlog {
    ($($arg:tt)*) => { $crate::log::log(&format!($($arg)*)) };
}
macro_rules! zdebug {
    ($($arg:tt)*) => { if $crate::log::debug_enabled() { $crate::log::log(&format!("debug {}", format!($($arg)*))) } };
}
pub(crate) use zdebug;
pub(crate) use zlog;
