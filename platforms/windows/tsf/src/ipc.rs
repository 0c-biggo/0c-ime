//! 0c-server 的 named pipe 用戶端：4 位元組小端序長度＋JSON 的訊框（bridge/ipc）。
//! 每次 I/O 都用 overlapped 加逾時：server 卡住時放棄連線並讓外殼放行按鍵，不得卡住宿主。
use std::path::PathBuf;

use serde_json::Value;
use windows::Win32::Foundation::{
    CloseHandle, GetLastError, ERROR_FILE_NOT_FOUND, ERROR_IO_PENDING, ERROR_PIPE_BUSY, HANDLE, WAIT_OBJECT_0,
};
use windows::Win32::Storage::FileSystem::{
    CreateFileW, ReadFile, WriteFile, FILE_FLAG_OVERLAPPED, FILE_GENERIC_READ, FILE_GENERIC_WRITE, FILE_SHARE_MODE,
    OPEN_EXISTING,
};
use windows::Win32::System::LibraryLoader::GetModuleFileNameW;
use windows::Win32::System::Pipes::{PeekNamedPipe, WaitNamedPipeW};
use windows::Win32::System::Threading::{
    CreateEventW, CreateProcessW, WaitForSingleObject, CREATE_NO_WINDOW, DETACHED_PROCESS, PROCESS_INFORMATION,
    STARTUPINFOW,
};
use windows::Win32::System::IO::{CancelIoEx, GetOverlappedResult, OVERLAPPED};
use windows_core::{PCWSTR, PWSTR};
use zuin_win_common::{frame, legacy_server_pipe, server_pipe, wide, MAX_FRAME};

use crate::log::zlog;

/// 每次 I/O 的逾時。
const IO_TIMEOUT_MS: u32 = 1000;
/// 啟動 server 後等待管道出現的時間。
const SPAWN_WAIT_MS: u32 = 5000;
/// 兩次啟動之間至少隔這麼久：server 反覆死掉時不會每鍵都啟動一次、也不會每鍵都等 5 秒。
pub const RESPAWN_INTERVAL: std::time::Duration = std::time::Duration::from_secs(10);
/// 引擎重啟時等新引擎可以服務的期限（#216），從發現舊引擎關掉連線、或舊引擎回「引擎正在切換」起算，只設定一次。
/// 改設定套用、更新交接、當掉後重新啟動，新引擎都要先等舊引擎存完檔放鎖、再載入資料包才開始監聽；
/// .14 實測改設定後約 1 秒可服務。期限內 OnKeyDown 等新引擎（宿主的下一個按鍵在訊息佇列裡依序等著），
/// 不在 server 根本沒在跑時每鍵都等（那時沒有斷線可發現，照舊立刻放行）。
pub const RESTART_WAIT: std::time::Duration = std::time::Duration::from_secs(5);

pub struct Client {
    pipe: Vec<u16>,           // 以 NUL 結尾
    legacy: Option<Vec<u16>>, // 舊版 zuin-server 的管道；只在找不到新管道時嘗試
    handle: Option<HANDLE>,
    generation: u32,
    last_spawn: Option<std::time::Instant>,
    /// 預期引擎正在重啟、等它到這個時間（RESTART_WAIT）；成功回覆或呼叫端放棄時清掉。
    restart_until: Option<std::time::Instant>,
    /// 舊 writer 明確拒絕過（「引擎正在切換」）：接管者由更新器或 0c-update 啟動，等的期間不自己啟動引擎。
    closing_writer: bool,
    /// 連不上時可以啟動安裝的 0c-server；只有日常的管道（Client::new）可以，測試用的管道不啟動真的引擎。
    may_start: bool,
}

impl Drop for Client {
    fn drop(&mut self) {
        self.close();
    }
}

/// 本 DLL 所在的目錄。x64 DLL 與 0c-server.exe、0c-ui.exe 同目錄；32 位元 DLL 在 `x86\` 子目錄（見 layout）。
pub fn dll_dir() -> Option<PathBuf> {
    let module = crate::dll_instance();
    let mut buf = vec![0u16; 32768]; // 64 KB 放 heap：宿主執行緒的堆疊可能很小（#89）
    let n = unsafe { GetModuleFileNameW(Some(module), &mut buf) } as usize;
    if n == 0 || n >= buf.len() {
        return None;
    }
    let dll = PathBuf::from(String::from_utf16_lossy(&buf[..n]));
    dll.parent().map(|p| p.to_path_buf())
}

/// 速率限制：距上次啟動不到 RESPAWN_INTERVAL 就不再啟動。
pub fn may_spawn(last: &mut Option<std::time::Instant>) -> bool {
    let now = std::time::Instant::now();
    if let Some(t) = *last {
        if now.duration_since(t) < RESPAWN_INTERVAL {
            return false;
        }
    }
    *last = Some(now);
    true
}

/// engine.json 的目錄欄位：只接受 versions/<名稱>，不含 ..、反斜線或更深的層級（與 internal/enginedir 相同）。
pub fn valid_engine_directory(dir: &str) -> bool {
    let mut parts = dir.split('/');
    matches!((parts.next(), parts.next(), parts.next()), (Some("versions"), Some(name), None)
        if !name.is_empty() && name != "." && name != ".." && !dir.contains('\\'))
}

/// 相容核心更新：%LOCALAPPDATA%\Zuin\engine.json 指向的引擎版本目錄；無效或檔案不齊全時為 None。
fn engine_dir() -> Option<PathBuf> {
    let base = PathBuf::from(std::env::var_os("LOCALAPPDATA")?).join("Zuin");
    let text = std::fs::read_to_string(base.join("engine.json")).ok()?;
    let value: Value = serde_json::from_str(&text).ok()?;
    let dir = value["directory"].as_str()?;
    if value["schema"].as_i64() != Some(1) || !valid_engine_directory(dir) {
        return None;
    }
    let path = base.join(dir.replace('/', "\\"));
    (path.join("0c-server.exe").is_file() && path.join("data").join("manifest.json").is_file()).then_some(path)
}

/// 0c-server.exe、0c-ui.exe 所在的版本目錄。固定路徑的 DLL（#121）依安裝收據找目前的版本目錄：
/// 更新後仍被應用程式載入的舊 DLL（已改名成 .old-*）也會啟動目前版本的程式，與相容核心更新後的情況相同。
fn programs_dir() -> Option<PathBuf> {
    let dll = dll_dir()?;
    if let Some(base) = crate::layout::fixed_base(&dll, crate::layout::IS_32BIT) {
        let text = std::fs::read_to_string(base.join("install-receipt.json")).ok()?;
        let dir = crate::layout::receipt_directory(&text)?;
        return Some(base.join(dir.replace('/', "\\")));
    }
    Some(crate::layout::program_dir(&dll, crate::layout::IS_32BIT))
}

/// 啟動版本目錄裡的程式（脫離宿主，不繼承把手）；32 位元 DLL 也啟動同一份 x64 程式。回傳 pid。引擎優先使用 engine.json 指向的版本。
pub fn spawn_sibling(exe_name: &str, args: &str) -> Option<u32> {
    let pointed = if exe_name == "0c-server.exe" {
        engine_dir()
    } else {
        None
    };
    let directory = match pointed {
        Some(dir) => {
            zlog!("spawn: 使用 engine.json 指向的引擎版本");
            dir
        }
        None => programs_dir()?,
    };
    let exe = directory.join(exe_name);
    if !exe.is_file() {
        zlog!("spawn: 沒有 {}", exe.display());
        return None;
    }
    let app = wide(&exe.to_string_lossy());
    // 版本目錄中的引擎固定使用同版詞庫；個人詞庫仍使用原 LOCALAPPDATA 路徑。
    // 舊版仍載入中的 DLL 也會保持自己的程式／詞庫配對，登出後才全面切換。
    let data = directory.join("data");
    let data_arg = if exe_name == "0c-server.exe" && data.join("manifest.json").is_file() {
        format!(" -data \"{}\"", data.to_string_lossy())
    } else {
        String::new()
    };
    let mut cmd = wide(&format!("\"{}\" {}{}", exe.to_string_lossy(), args, data_arg));
    let si = STARTUPINFOW {
        cb: std::mem::size_of::<STARTUPINFOW>() as u32,
        ..Default::default()
    };
    let mut pi = PROCESS_INFORMATION::default();
    let r = unsafe {
        CreateProcessW(
            PCWSTR::from_raw(app.as_ptr()),
            Some(PWSTR::from_raw(cmd.as_mut_ptr())),
            None,
            None,
            false,
            CREATE_NO_WINDOW | DETACHED_PROCESS,
            None,
            PCWSTR::null(),
            &si,
            &mut pi,
        )
    };
    match r {
        Ok(()) => unsafe {
            zlog!("spawn: {} pid={}", exe_name, pi.dwProcessId);
            let _ = CloseHandle(pi.hThread);
            let _ = CloseHandle(pi.hProcess);
            Some(pi.dwProcessId)
        },
        Err(e) => {
            zlog!("spawn: {} 失敗 {}", exe_name, e);
            None
        }
    }
}

impl Client {
    pub fn new() -> Client {
        let mut client = Client::with_pipe(&server_pipe());
        client.legacy = Some(wide(&legacy_server_pipe()));
        client.may_start = true;
        client
    }

    pub fn with_pipe(name: &str) -> Client {
        Client {
            pipe: wide(name),
            legacy: None,
            handle: None,
            generation: 0,
            last_spawn: None,
            restart_until: None,
            closing_writer: false,
            may_start: false,
        }
    }

    #[cfg(test)]
    pub fn disconnect_for_test(&mut self) {
        self.close();
    }

    /// 每次重新連線後遞增；session 屬於某一代連線，代數變了就要重開並重播。
    pub fn generation(&self) -> u32 {
        self.generation
    }

    /// 引擎正在重啟（交接、改設定、當掉後重新啟動），還在等新引擎的期限內（#216）。
    pub fn restart_pending(&self) -> bool {
        self.restart_until
            .is_some_and(|until| std::time::Instant::now() < until)
    }

    /// 等新引擎的期限（過了也照樣回報，直到成功或放棄）；呼叫端以第一次看到的期限為準，持續斷線不能一直延長。
    pub fn restart_deadline(&self) -> Option<std::time::Instant> {
        self.restart_until
    }

    /// 呼叫端不再等：下一次按鍵照一般情況連線（server 不在就立刻放行）。
    pub fn abandon_restart(&mut self) {
        self.restart_until = None;
        self.closing_writer = false;
    }

    /// 舊引擎關掉了這條連線（交接、改設定後退出、當掉）：新引擎通常馬上會由登入工作、更新器或這裡啟動，
    /// 從現在起 RESTART_WAIT 內等它。已經在等就不延長。
    fn expect_restart(&mut self) {
        if self.restart_until.is_none() {
            self.restart_until = Some(std::time::Instant::now() + RESTART_WAIT);
        }
    }

    /// 剛失敗的把手是不是被另一端關掉（不是逾時）：引擎還在、只是慢，不當成重啟。
    fn peer_closed(&self) -> bool {
        let Some(h) = self.handle else {
            return false;
        };
        unsafe { PeekNamedPipe(h, None, 0, None, None, None) }.is_err()
    }

    /// I/O 失敗後關掉連線；另一端已經關掉時記下引擎正在重啟。
    fn close_after_failure(&mut self) {
        if self.peer_closed() {
            self.expect_restart();
        }
        self.close();
    }

    /// 目前是否持有連線（不代表引擎仍在；下一次呼叫才會發現斷線）。
    pub fn connected(&self) -> bool {
        self.handle.is_some()
    }

    /// 連線是否還在：不送請求、不等待，只看管道另一端關了沒有（引擎退出後 PeekNamedPipe 失敗）。斷了，或有不該有的
    /// 殘留資料（訊框會錯位），就關掉連線，下一次呼叫照常重連。
    pub fn alive(&mut self) -> bool {
        let Some(h) = self.handle else {
            return false;
        };
        let mut available = 0u32;
        let ok = unsafe { PeekNamedPipe(h, None, 0, None, Some(&mut available), None) }.is_ok();
        if !ok {
            self.expect_restart(); // 引擎關掉了連線：多半正在重啟（#216）
        }
        if !ok || available != 0 {
            self.close();
            return false;
        }
        true
    }

    /// 只連已經在執行的引擎，不啟動 0c-server、不等待（Activate 時查開始時的模式，#160）。
    pub fn connect_existing(&mut self) -> bool {
        if self.handle.is_some() {
            return true;
        }
        if self.closing_writer && self.restart_pending() {
            return false;
        }
        self.connect_once().is_ok() || self.connect_legacy()
    }

    /// 放掉連線；下一次呼叫照常重連（代數會變，session 會重開）。
    pub fn release(&mut self) {
        self.close();
    }

    fn close(&mut self) {
        if let Some(h) = self.handle.take() {
            unsafe {
                let _ = CloseHandle(h);
            }
        }
    }

    fn connect_once(&mut self) -> Result<(), u32> {
        let pipe = self.pipe.clone();
        self.connect_to(&pipe)
    }

    /// 升級過渡期：舊版 zuin-server 還在執行時接上它，不另起第二個寫入者。
    fn connect_legacy(&mut self) -> bool {
        match self.legacy.clone() {
            Some(pipe) => self.connect_to(&pipe).is_ok(),
            None => false,
        }
    }

    fn connect_to(&mut self, pipe: &[u16]) -> Result<(), u32> {
        unsafe {
            match CreateFileW(
                PCWSTR::from_raw(pipe.as_ptr()),
                FILE_GENERIC_READ.0 | FILE_GENERIC_WRITE.0,
                FILE_SHARE_MODE(0),
                None,
                OPEN_EXISTING,
                FILE_FLAG_OVERLAPPED,
                None,
            ) {
                Ok(h) => {
                    self.handle = Some(h);
                    self.generation = self.generation.wrapping_add(1);
                    Ok(())
                }
                Err(_) => Err(GetLastError().0),
            }
        }
    }

    /// 啟動 0c-server；回傳是否真的啟動了（有速率限制）。引擎常駐、不閒置結束：
    /// 沙箱應用程式（Windows 搜尋框等）裡的 DLL 不能啟動程式，只能連上已在執行的引擎。
    fn spawn_server(&mut self) -> bool {
        if !self.may_start || !may_spawn(&mut self.last_spawn) {
            return false;
        }
        spawn_sibling("0c-server.exe", "").is_some()
    }

    /// 確保已連上；必要時啟動 0c-server。失敗回傳 false。
    pub fn connect(&mut self) -> bool {
        if self.handle.is_some() {
            return true;
        }
        if self.restart_pending() {
            // 引擎正在重啟（#216）：只試一次、不在這裡等，由 Session::handle 在期限內反覆試（TestKeyDown 的查詢不卡住宿主）。
            // 舊 writer 拒絕過時接管者由更新器啟動：不要連回仍在關閉的 writer 後一直使用同一把手，也不自己啟動。
            // 其他情況（改設定後退出、當掉）管道不在就照舊啟動一次（有速率限制；登入工作同時啟動的那個會讓位）。
            return match self.connect_once() {
                Ok(()) => true,
                Err(code) if code == ERROR_FILE_NOT_FOUND.0 => {
                    if self.connect_legacy() {
                        return true;
                    }
                    if !self.closing_writer && self.spawn_server() {
                        zlog!("connect: 引擎重啟中，管道還不在，已啟動 0c-server");
                    }
                    false
                }
                Err(_) => self.connect_legacy(),
            };
        }
        if self.restart_until.is_some() {
            self.abandon_restart(); // 期限已過：照一般情況處理，這一次 server 不在就不等
        }
        for attempt in 0..2 {
            match self.connect_once() {
                Ok(()) => return true,
                Err(code) if code == ERROR_PIPE_BUSY.0 => {
                    unsafe {
                        let _ = WaitNamedPipeW(PCWSTR::from_raw(self.pipe.as_ptr()), IO_TIMEOUT_MS);
                    }
                    if self.connect_once().is_ok() {
                        return true;
                    }
                }
                Err(code) if code == ERROR_FILE_NOT_FOUND.0 && attempt == 0 => {
                    if self.connect_legacy() {
                        return true;
                    }
                    if !self.spawn_server() {
                        return false; // 沒有啟動就不等：server 不在時每鍵立刻放行，不卡宿主
                    }
                    // 等 server 建好管道；沒等到的話之後的呼叫在同一個期限內繼續等（restart_pending），不再多等一輪。
                    let deadline = std::time::Instant::now() + std::time::Duration::from_millis(SPAWN_WAIT_MS as u64);
                    self.restart_until = Some(deadline);
                    while std::time::Instant::now() < deadline {
                        if self.connect_once().is_ok() {
                            return true;
                        }
                        std::thread::sleep(std::time::Duration::from_millis(50));
                    }
                }
                Err(code) => {
                    zlog!("connect: 失敗 code={}", code);
                    return false;
                }
            }
        }
        false
    }

    /// 送出一個 Envelope 並讀回 Reply。連線失敗回傳 None，並關閉連線（下次呼叫會重連）。
    pub fn call(&mut self, envelope: &Value) -> Option<Value> {
        if !self.connect() {
            return None;
        }
        let bytes = frame(envelope)?;
        let h = self.handle?;
        if !write_all(h, &bytes, IO_TIMEOUT_MS) {
            zlog!("call: 寫入失敗，斷線");
            self.close_after_failure();
            return None;
        }
        let mut hdr = [0u8; 4];
        if !read_all(h, &mut hdr, IO_TIMEOUT_MS) {
            zlog!("call: 讀取失敗，斷線");
            self.close_after_failure();
            return None;
        }
        let n = u32::from_le_bytes(hdr);
        if n > MAX_FRAME {
            self.close();
            return None;
        }
        let mut buf = vec![0u8; n as usize];
        if !read_all(h, &mut buf, IO_TIMEOUT_MS) {
            self.close_after_failure();
            return None;
        }
        match serde_json::from_slice::<Value>(&buf) {
            Ok(v) => {
                if v["ok"].as_bool() == Some(false) && v["error"].as_str() == Some("引擎正在切換") {
                    // 此回覆表示請求完全未執行，重試不會重複提交。
                    // 只設定一次期限；持續拒絕不能無限延長宿主等待。
                    self.expect_restart();
                    self.closing_writer = true;
                    self.close();
                } else if v["ok"].as_bool() == Some(true) {
                    self.abandon_restart(); // 新引擎接上了
                }
                Some(v)
            }
            Err(_) => {
                self.close();
                None
            }
        }
    }
}

/// 一次 overlapped I/O：回傳完成的位元組數；逾時或錯誤回傳 None。
fn overlapped(h: HANDLE, timeout_ms: u32, op: impl FnOnce(*mut OVERLAPPED) -> windows_core::Result<()>) -> Option<u32> {
    unsafe {
        let event = CreateEventW(None, true, false, PCWSTR::null()).ok()?;
        let mut ov = OVERLAPPED {
            hEvent: event,
            ..Default::default()
        };
        let started = op(&mut ov);
        let mut done = 0u32;
        let result = match started {
            Ok(()) => GetOverlappedResult(h, &ov, &mut done, false).ok().map(|_| done),
            Err(_) if GetLastError() == ERROR_IO_PENDING => {
                if WaitForSingleObject(event, timeout_ms) == WAIT_OBJECT_0 {
                    GetOverlappedResult(h, &ov, &mut done, false).ok().map(|_| done)
                } else {
                    let _ = CancelIoEx(h, Some(&ov));
                    let _ = GetOverlappedResult(h, &ov, &mut done, true);
                    None
                }
            }
            Err(_) => None,
        };
        let _ = CloseHandle(event);
        result
    }
}

pub fn write_all(h: HANDLE, mut data: &[u8], timeout_ms: u32) -> bool {
    while !data.is_empty() {
        let chunk = data;
        let n = overlapped(h, timeout_ms, |ov| unsafe { WriteFile(h, Some(chunk), None, Some(ov)) });
        match n {
            Some(n) if n > 0 => data = &data[n as usize..],
            _ => return false,
        }
    }
    true
}

fn read_all(h: HANDLE, mut buf: &mut [u8], timeout_ms: u32) -> bool {
    while !buf.is_empty() {
        let n = {
            let chunk: &mut [u8] = buf;
            overlapped(h, timeout_ms, |ov| unsafe { ReadFile(h, Some(chunk), None, Some(ov)) })
        };
        match n {
            Some(n) if n > 0 => buf = &mut buf[n as usize..],
            _ => return false,
        }
    }
    true
}

#[cfg(test)]
mod engine_pointer_tests {
    use super::valid_engine_directory;

    #[test]
    fn only_direct_version_directories_are_accepted() {
        assert!(valid_engine_directory("versions/0.1.0-dev.18-abc"));
        for bad in [
            "../x",
            "versions/../x",
            "versions/a/b",
            "other/x",
            "versions\\x",
            "versions/",
            "versions/.",
            "versions/..",
            "versions",
        ] {
            assert!(!valid_engine_directory(bad), "{bad}");
        }
    }
}
