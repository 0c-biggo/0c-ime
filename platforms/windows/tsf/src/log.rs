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
    // 單元測試不寫進測試機真正的 tsf.log。
    if cfg!(test) {
        return None;
    }
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

/// 同一則訊息每個行程只記一次（不必開 tsf-debug）：給每鍵都會遇到、但使用者回報時需要看到原因的情況，
/// 例如欄位政策讀不到 InputScope 而降級或放行（#209）。訊息不可含文字或鍵碼；最多記 32 則不同的訊息。
pub fn once(msg: &str) {
    use std::sync::Mutex;
    static SEEN: Mutex<Vec<String>> = Mutex::new(Vec::new());
    let first = first_time(&mut SEEN.lock().unwrap_or_else(|e| e.into_inner()), msg);
    if first {
        log(msg);
    }
}

const ONCE_LIMIT: usize = 32;

fn first_time(seen: &mut Vec<String>, msg: &str) -> bool {
    if seen.len() >= ONCE_LIMIT || seen.iter().any(|m| m == msg) {
        return false;
    }
    seen.push(msg.to_owned());
    true
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn once_logs_each_message_once_and_stops_at_the_limit() {
        let mut seen = Vec::new();
        assert!(first_time(&mut seen, "a"));
        assert!(!first_time(&mut seen, "a"));
        assert!(first_time(&mut seen, "b"));
        for i in 0..ONCE_LIMIT {
            first_time(&mut seen, &i.to_string());
        }
        assert_eq!(seen.len(), ONCE_LIMIT);
        assert!(!first_time(&mut seen, "new"));
    }
}
