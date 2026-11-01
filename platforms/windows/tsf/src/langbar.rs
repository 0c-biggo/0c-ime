//! 輸入指示器的「中／英」按鈕與輸入法選單（#13）。
//! 左鍵切換中英（同右 Shift）；右鍵顯示更新與設定選單。狀態與動作經 0c-ui 的選單管道處理：
//! 宿主可能在沙箱內，DLL 不直接執行 0c-update 也不讀個人資料目錄；0c-ui 不在時（更新後被請走）非沙箱宿主先啟動它。
use std::cell::{Cell, RefCell};
use std::ffi::c_void;
use std::sync::mpsc::{channel, Receiver};
use std::sync::{Arc, Mutex, MutexGuard};
use std::time::{Duration, Instant};

use windows::Win32::Foundation::{
    CloseHandle, FreeLibrary, ERROR_FILE_NOT_FOUND, E_INVALIDARG, E_POINTER, E_UNEXPECTED, HANDLE, HMODULE, POINT, RECT,
};
use windows::Win32::Graphics::Gdi::{
    CreateBitmap, CreateCompatibleDC, CreateDIBSection, CreateFontW, DeleteDC, DeleteObject, DrawTextW, SelectObject,
    SetBkMode, SetTextColor, BITMAPINFO, BITMAPINFOHEADER, BI_RGB, CLEARTYPE_QUALITY, CLIP_DEFAULT_PRECIS,
    DEFAULT_CHARSET, DEFAULT_PITCH, DIB_RGB_COLORS, DT_CENTER, DT_SINGLELINE, DT_VCENTER, FW_NORMAL, GetTextFaceW,
    HBITMAP, HDC, OUT_DEFAULT_PRECIS, TRANSPARENT,
};
use windows::Win32::Storage::FileSystem::{
    CreateFileW, ReadFile, WriteFile, FILE_GENERIC_READ, FILE_GENERIC_WRITE, FILE_SHARE_MODE, OPEN_EXISTING,
    FILE_FLAGS_AND_ATTRIBUTES,
};
use windows::Win32::System::LibraryLoader::{
    FreeLibraryAndExitThread, GetModuleHandleExW, GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS,
};
use windows::Win32::System::Pipes::GetNamedPipeServerProcessId;
use windows::Win32::System::Registry::{RegGetValueW, HKEY_CURRENT_USER, RRF_RT_REG_DWORD};
use windows::Win32::System::Threading::{CreateThread, THREAD_CREATION_FLAGS};
use windows::Win32::UI::TextServices::{
    ITfLangBarItem, ITfLangBarItemButton, ITfLangBarItemButton_Impl, ITfLangBarItemSink, ITfLangBarItem_Impl,
    ITfMenu, ITfSource, ITfSource_Impl, TfLBIClick, GUID_LBI_INPUTMODE, TF_LANGBARITEMINFO, TF_LBI_CLK_LEFT,
    TF_LBI_CLK_RIGHT, TF_LBI_ICON, TF_LBI_STYLE_BTN_BUTTON, TF_LBI_STYLE_SHOWNINTRAY, TF_LBI_TEXT,
    TF_LBI_TOOLTIP, TF_LBMENUF_CHECKED, TF_LBMENUF_SEPARATOR,
};
use windows::Win32::UI::WindowsAndMessaging::{
    AllowSetForegroundWindow, AppendMenuW, CreateIconIndirect, CreatePopupMenu, CreateWindowExW, DestroyMenu, DestroyWindow,
    GetSystemMetrics, PostMessageW, SetForegroundWindow, SetMenuItemInfoW, TrackPopupMenuEx, HICON, ICONINFO,
    MENUITEMINFOW, MF_CHECKED, MF_SEPARATOR, MF_STRING, MIIM_BITMAP, SM_CXSMICON, TPM_NONOTIFY, TPM_RETURNCMD, TPM_RIGHTBUTTON, WINDOW_EX_STYLE,
    WM_NULL, WS_POPUP,
};
use windows_core::{implement, w, AsImpl, Interface, Ref, BSTR, GUID, PCWSTR};
use zuin_win_common::menu::{menu_items, needs_attention, opens_window, parse_status, simplified_target, MenuItem, MenuRequest, UpdateStatus};
use zuin_win_common::{frame, menu_pipe, wide, MAX_FRAME};

use crate::guids::{CLSID_ZUIN, DESCRIPTION};
use crate::ipc::spawn_sibling;
use crate::log::zlog;

const SINK_COOKIE: u32 = 0x0c0c;

/// 啟動 0c-ui 後等它開始服務多久，以及這段時間裡隔多久再試連線、再啟動一次。
/// 舊版 0c-ui 正在結束時它還占著單一實例，新啟動的會立刻退出，所以等待中要能再啟動一次。
const UI_START_WAIT: Duration = Duration::from_secs(2);
const UI_POLL: Duration = Duration::from_millis(50);
const UI_RESTART: Duration = Duration::from_secs(1);
/// 開選單時等新狀態多久；這段時間宿主的 UI 執行緒在等，超過就先用快取，背景讀完下次開選單就是新的。
const MENU_STATUS_WAIT: Duration = Duration::from_millis(500);

/// 試連選單管道一次的結果。
enum MenuPipe<T> {
    Connected(T),
    /// 管道不存在：0c-ui 沒有在執行。
    Missing,
    /// 其他失敗。ERROR_PIPE_BUSY 是 0c-ui 在執行、下一個管道還沒開好（menu.rs 的 server 在兩次 CreateNamedPipeW 之間）。
    Unavailable,
}

fn connect_menu_pipe() -> MenuPipe<HANDLE> {
    let name = wide(&menu_pipe());
    let result = unsafe {
        CreateFileW(
            PCWSTR::from_raw(name.as_ptr()),
            FILE_GENERIC_READ.0 | FILE_GENERIC_WRITE.0,
            FILE_SHARE_MODE(0),
            None,
            OPEN_EXISTING,
            FILE_FLAGS_AND_ATTRIBUTES(0),
            None,
        )
    };
    match result {
        Ok(h) => MenuPipe::Connected(h),
        Err(e) if e.code() == ERROR_FILE_NOT_FOUND.to_hresult() => MenuPipe::Missing,
        Err(_) => MenuPipe::Unavailable,
    }
}

/// 連上 0c-ui（#243）。更新器會請舊版 0c-ui 結束，下一次打字才由 Activate 或候選窗帶起新版，這段時間選單如果只試一次，
/// 「設定…」等項目按了沒有反應、狀態也一直是舊的。只有管道不存在才呼叫 `start`（它回 false 就不再等，見 `when_ui_missing`）；
/// 其他失敗就等，0c-ui 在執行時再啟動一個只會因單一實例立刻退出。只有連線可以重試：連上之後的錯誤可能是命令已經執行、
/// 只是沒收到回覆。
fn connect_or_start<T>(
    mut connect: impl FnMut() -> MenuPipe<T>,
    mut start: impl FnMut() -> bool,
    wait: Duration,
    poll: Duration,
    restart: Duration,
) -> Option<T> {
    let begun = Instant::now();
    let mut started: Option<Instant> = None;
    loop {
        match connect() {
            MenuPipe::Connected(connection) => return Some(connection),
            MenuPipe::Missing if started.map_or(true, |at| at.elapsed() >= restart) => {
                if !start() {
                    return None;
                }
                started = Some(Instant::now());
            }
            MenuPipe::Missing | MenuPipe::Unavailable => {}
        }
        if begun.elapsed() >= wait {
            return None;
        }
        std::thread::sleep(poll);
    }
}

/// 管道不存在時怎麼辦；回傳還要不要等它出現。只有使用者在選單上操作才啟動 0c-ui。按鈕建立與背景讀狀態不啟動，否則解除安裝
/// 之後已開著的程式也會把它叫回來留到登出；Activate 自己的 warm_up 另外規定，這裡等它（剛啟動時狀態才讀得到）。
/// 沙箱宿主不能啟動程式，也沒有別人會為它帶起，不等。
fn when_ui_missing(user_asked: bool, sandboxed: bool, spawn: impl FnOnce() -> bool) -> bool {
    if sandboxed {
        return false;
    }
    !user_asked || spawn()
}

/// 把前景權限交給管道另一端的 0c-ui（#251）。使用者剛在這個宿主的選單上按下項目，所以宿主還有權限；設定視窗由
/// 0c-ui → 0c-update → 0c-settings 開，都是背景程式，沒有這一棒視窗就開在其他視窗後面。0c-ui 啟動 0c-update 時權限會跟著過去，
/// 0c-update 再指定給設定程式（settingsapp.Launch）。宿主沒有權限時（例如沙箱）系統會拒絕，命令照樣送，只記下原因。
fn grant_foreground(pipe: HANDLE) {
    let mut pid = 0u32;
    let result = unsafe { GetNamedPipeServerProcessId(pipe, &mut pid).and_then(|()| AllowSetForegroundWindow(pid)) };
    match result {
        Ok(()) => zlog!("langbar: 前景權限交給 0c-ui pid={}", pid),
        Err(e) => zlog!("langbar: 前景權限交不出去 pid={} {} sandboxed={}", pid, e, crate::ui::sandboxed()),
    }
}

/// 問 0c-ui 一次（阻塞，只在背景執行緒呼叫）。`user_asked`：使用者正在選單上操作，0c-ui 不在時要啟動它。
fn ask(req: &MenuRequest, user_asked: bool) -> Option<String> {
    let sandboxed = crate::ui::sandboxed();
    let start = || when_ui_missing(user_asked, sandboxed, || spawn_sibling("0c-ui.exe", "").is_some());
    let Some(h) = connect_or_start(connect_menu_pipe, start, UI_START_WAIT, UI_POLL, UI_RESTART) else {
        zlog!("langbar: 連不上 0c-ui，{} 沒有執行 sandboxed={} user_asked={}", req.op, sandboxed, user_asked);
        return None;
    };
    if req.op == "run" && opens_window(&req.args) {
        grant_foreground(h);
    }
    unsafe {
        let result = (|| {
            let bytes = frame(req)?;
            let mut n = 0u32;
            WriteFile(h, Some(&bytes), Some(&mut n), None).ok()?;
            let mut hdr = [0u8; 4];
            read_all(h, &mut hdr)?;
            let len = u32::from_le_bytes(hdr);
            if len > MAX_FRAME {
                return None;
            }
            let mut body = vec![0u8; len as usize];
            read_all(h, &mut body)?;
            String::from_utf8(body).ok()
        })();
        let _ = CloseHandle(h);
        result
    }
}

unsafe fn read_all(h: HANDLE, buf: &mut [u8]) -> Option<()> {
    let mut off = 0usize;
    while off < buf.len() {
        let mut n = 0u32;
        ReadFile(h, Some(&mut buf[off..]), Some(&mut n), None).ok()?;
        if n == 0 {
            return None;
        }
        off += n as usize;
    }
    Some(())
}

/// 狀態快取：背景執行緒寫、宿主執行緒讀。執行緒中途 panic 也不能讓之後每次讀取都跟著 panic。
fn lock(status: &Mutex<UpdateStatus>) -> MutexGuard<'_, UpdateStatus> {
    status.lock().unwrap_or_else(|e| e.into_inner())
}

/// 背景執行緒的工作與它持有的 DLL 模組參考。
struct Job {
    module: HMODULE,
    work: Box<dyn FnOnce() + Send>,
}

/// 在背景執行。期間計入 DLL 物件數，另外由呼叫端先取得一份模組參考交給執行緒，
/// 執行緒以 FreeLibraryAndExitThread 放開（#89）：減完物件數到執行緒真正結束之間仍在跑本 DLL 的程式碼，
/// 只靠物件數時宿主可能在這段時間卸載 DLL（CoFreeUnusedLibraries），執行緒就跑進已卸載的位址。
fn background(f: impl FnOnce() + Send + 'static) {
    let mut module = HMODULE::default();
    let locked = unsafe {
        GetModuleHandleExW(
            GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS,
            PCWSTR::from_raw(background_main as *const () as *const u16), // FROM_ADDRESS：以本 DLL 內的位址找模組
            &mut module,
        )
    };
    if locked.is_err() {
        zlog!("langbar: 取得模組參考失敗，略過背景工作");
        return;
    }
    crate::object_created();
    let job = Box::into_raw(Box::new(Job { module, work: Box::new(f) }));
    let started =
        unsafe { CreateThread(None, 0, Some(background_main), Some(job as *const c_void), THREAD_CREATION_FLAGS(0), None) };
    match started {
        Ok(thread) => unsafe {
            let _ = CloseHandle(thread);
        },
        Err(e) => unsafe {
            zlog!("langbar: 建立背景執行緒失敗 {}", e);
            drop(Box::from_raw(job));
            crate::object_destroyed();
            let _ = FreeLibrary(module);
        },
    }
}

unsafe extern "system" fn background_main(param: *mut c_void) -> u32 {
    let job = Box::from_raw(param as *mut Job);
    let Job { module, work } = *job;
    if std::panic::catch_unwind(std::panic::AssertUnwindSafe(work)).is_err() {
        crate::log::log("panic 已攔截：langbar 背景工作");
    }
    crate::object_destroyed();
    FreeLibraryAndExitThread(module, 0)
}

/// 重新讀狀態；讀完（或讀不到）時回傳的 channel 會收到通知或斷線。
fn refresh(status: Arc<Mutex<UpdateStatus>>, user_asked: bool) -> Receiver<()> {
    let (done, finished) = channel();
    background(move || {
        if let Some(json) = ask(&MenuRequest { op: "status".into(), args: vec![] }, user_asked) {
            *lock(&status) = parse_status(&json);
        }
        let _ = done.send(());
    });
    finished
}

fn run(status: Arc<Mutex<UpdateStatus>>, args: Vec<String>) {
    background(move || {
        let _ = ask(&MenuRequest { op: "run".into(), args }, true);
        // 設定與模式會立刻改變；更新在背景進行，稍後再讀一次。
        for wait in [2u64, 20] {
            std::thread::sleep(std::time::Duration::from_secs(wait));
            if let Some(json) = ask(&MenuRequest { op: "status".into(), args: vec![] }, false) {
                *lock(&status) = parse_status(&json);
            }
        }
    });
}

fn dword(path: PCWSTR, name: PCWSTR) -> Option<u32> {
    let mut value = 0u32;
    let mut size = 4u32;
    unsafe {
        RegGetValueW(
            HKEY_CURRENT_USER,
            path,
            name,
            RRF_RT_REG_DWORD,
            None,
            Some(&mut value as *mut u32 as *mut c_void),
            Some(&mut size),
        )
        .is_ok()
        .then_some(value)
    }
}

/// 工作列底色是否偏亮（圖示要用黑字）。開了「在開始與工作列顯示輔色」時底色是輔色，
/// 系統依輔色亮度決定字色，與淺色／深色模式無關；否則跟隨系統（非應用程式）模式。
fn light_taskbar() -> bool {
    let personalize = w!("Software\\Microsoft\\Windows\\CurrentVersion\\Themes\\Personalize");
    if dword(personalize, w!("ColorPrevalence")) == Some(1) {
        let accent = dword(w!("Software\\Microsoft\\Windows\\CurrentVersion\\Explorer\\Accent"), w!("AccentColorMenu"))
            .or_else(|| dword(w!("Software\\Microsoft\\Windows\\DWM"), w!("AccentColor")));
        if let Some(abgr) = accent {
            return light_color(abgr);
        }
    }
    dword(personalize, w!("SystemUsesLightTheme")) == Some(1)
}

/// 登錄裡的輔色是 0xAABBGGRR；亮度判斷同 Windows 主題文件的 IsColorLight（5G＋2R＋B > 8×128）。
fn light_color(abgr: u32) -> bool {
    let (r, g, b) = (abgr & 0xff, (abgr >> 8) & 0xff, (abgr >> 16) & 0xff);
    5 * g + 2 * r + b > 8 * 128
}

/// 系統圖示字型（Win11 Segoe Fluent Icons、Win10 Segoe MDL2 Assets）的「設定」齒輪。
const GEAR: &str = "\u{E713}";
/// 同一套字型的「字典」，給「我的詞彙…」（#125）。
const DICTIONARY: &str = "\u{E82D}";

/// 選單左側圖示欄用的字形圖：黑色、預乘 alpha 的 32 位元 DIB（選單以 alpha 混合繪製）。
/// 兩種圖示字型都沒有時回傳 None，選單照常只有文字。呼叫端在選單關閉後負責刪除。
fn glyph_bitmap(glyph: &str) -> Option<HBITMAP> {
    unsafe {
        let size = GetSystemMetrics(SM_CXSMICON).max(16);
        let dc = CreateCompatibleDC(None);
        let bmi = BITMAPINFO {
            bmiHeader: BITMAPINFOHEADER {
                biSize: std::mem::size_of::<BITMAPINFOHEADER>() as u32,
                biWidth: size,
                biHeight: -size,
                biPlanes: 1,
                biBitCount: 32,
                biCompression: BI_RGB.0,
                ..Default::default()
            },
            ..Default::default()
        };
        let mut bits: *mut c_void = std::ptr::null_mut();
        let Ok(bitmap) = CreateDIBSection(Some(dc), &bmi, DIB_RGB_COLORS, &mut bits, None, 0) else {
            let _ = DeleteDC(dc);
            return None;
        };
        let old = SelectObject(dc, bitmap.into());
        let mut drawn = false;
        for face in [w!("Segoe Fluent Icons"), w!("Segoe MDL2 Assets")] {
            let font = CreateFontW(
                -size, 0, 0, 0, FW_NORMAL.0 as i32, 0, 0, 0, DEFAULT_CHARSET, OUT_DEFAULT_PRECIS,
                CLIP_DEFAULT_PRECIS, CLEARTYPE_QUALITY, DEFAULT_PITCH.0 as u32, face,
            );
            let old_font = SelectObject(dc, font.into());
            // 字型不存在時 GDI 會換成別的字型，畫出來是方框；確認選到的就是要的字型。
            if selected_face(dc).eq_ignore_ascii_case(&face.to_string().unwrap_or_default()) {
                SetBkMode(dc, TRANSPARENT);
                SetTextColor(dc, windows::Win32::Foundation::COLORREF(0x00FF_FFFF));
                let mut rc = RECT { left: 0, top: 0, right: size, bottom: size };
                let mut t: Vec<u16> = glyph.encode_utf16().collect();
                DrawTextW(dc, &mut t, &mut rc, DT_CENTER | DT_VCENTER | DT_SINGLELINE);
                drawn = true;
            }
            SelectObject(dc, old_font);
            let _ = DeleteObject(font.into());
            if drawn {
                break;
            }
        }
        if drawn {
            // 白字的亮度當覆蓋率；黑色預乘後 RGB 為 0，只留 alpha。
            let px = std::slice::from_raw_parts_mut(bits as *mut u32, (size * size) as usize);
            for p in px.iter_mut() {
                let a = (*p & 0xff).max((*p >> 8) & 0xff).max((*p >> 16) & 0xff);
                *p = a << 24;
            }
        }
        SelectObject(dc, old);
        let _ = DeleteDC(dc);
        if drawn {
            Some(bitmap)
        } else {
            let _ = DeleteObject(bitmap.into());
            None
        }
    }
}

unsafe fn selected_face(dc: HDC) -> String {
    let mut buf = [0u16; 64];
    let n = GetTextFaceW(dc, Some(&mut buf)) as usize;
    String::from_utf16_lossy(&buf[..n.min(buf.len()).saturating_sub(1)])
}

/// 以文字（中／英）畫出工作列圖示；GDI 不寫 alpha，畫完後把有顏色的像素設為不透明。
fn text_icon(text: &str) -> Option<HICON> {
    unsafe {
        let size = GetSystemMetrics(SM_CXSMICON).max(16);
        let dc = CreateCompatibleDC(None);
        let bmi = BITMAPINFO {
            bmiHeader: BITMAPINFOHEADER {
                biSize: std::mem::size_of::<BITMAPINFOHEADER>() as u32,
                biWidth: size,
                biHeight: -size,
                biPlanes: 1,
                biBitCount: 32,
                biCompression: BI_RGB.0,
                ..Default::default()
            },
            ..Default::default()
        };
        let mut bits: *mut c_void = std::ptr::null_mut();
        let color = CreateDIBSection(Some(dc), &bmi, DIB_RGB_COLORS, &mut bits, None, 0).ok()?;
        let old = SelectObject(dc, color.into());
        let font = CreateFontW(
            -(size * 7 / 8), 0, 0, 0, FW_NORMAL.0 as i32, 0, 0, 0, DEFAULT_CHARSET, OUT_DEFAULT_PRECIS,
            CLIP_DEFAULT_PRECIS, CLEARTYPE_QUALITY, DEFAULT_PITCH.0 as u32, w!("Microsoft JhengHei UI"),
        );
        let old_font = SelectObject(dc, font.into());
        SetBkMode(dc, TRANSPARENT);
        // 先以白字畫在全透明（0）背景上取得覆蓋率，再依工作列主題填入黑或白（非預乘 alpha）。
        SetTextColor(dc, windows::Win32::Foundation::COLORREF(0x00FF_FFFF));
        let mut rc = RECT { left: 0, top: 0, right: size, bottom: size };
        let mut t: Vec<u16> = text.encode_utf16().collect();
        DrawTextW(dc, &mut t, &mut rc, DT_CENTER | DT_VCENTER | DT_SINGLELINE);
        let fill = if light_taskbar() { 0x0000_0000u32 } else { 0x00FF_FFFFu32 };
        let px = std::slice::from_raw_parts_mut(bits as *mut u32, (size * size) as usize);
        for p in px.iter_mut() {
            let a = (*p & 0xff).max((*p >> 8) & 0xff).max((*p >> 16) & 0xff);
            *p = if a == 0 { 0 } else { (a << 24) | fill };
        }
        SelectObject(dc, old_font);
        let _ = DeleteObject(font.into());
        SelectObject(dc, old);
        let _ = DeleteDC(dc);
        let mask = CreateBitmap(size, size, 1, 1, None);
        let info = ICONINFO { fIcon: true.into(), xHotspot: 0, yHotspot: 0, hbmMask: mask, hbmColor: color };
        let icon = CreateIconIndirect(&info).ok();
        let _ = DeleteObject(mask.into());
        let _ = DeleteObject(color.into());
        icon
    }
}

/// 輸入指示器的字（#169）：英文「英」；中文「中」，送出時轉成簡體的話是「简」（簡體字形，一眼看得出是簡體輸出）。
pub fn indicator_text(english: bool, simplified: bool) -> &'static str {
    match (english, simplified) {
        (true, _) => "英",
        (false, true) => "简",
        (false, false) => "中",
    }
}

/// 滑鼠停在輸入指示器上的說明。
pub fn tooltip_text(english: bool, simplified: bool, attention: bool) -> String {
    let mode = match (english, simplified) {
        (true, _) => "英文",
        (false, true) => "中・簡體輸出",
        (false, false) => "中文",
    };
    if attention {
        format!("靈犀注音（{mode}）：有待辦，按右鍵查看")
    } else {
        format!("靈犀注音（{mode}）")
    }
}

#[implement(ITfLangBarItemButton, ITfLangBarItem, ITfSource)]
pub struct LangBarButton {
    english: Cell<bool>,
    /// 「輸出簡體字」（#169）：引擎能即時切換時是它目前的值（Some），否則 None（選單看 0c-update 的狀態）。
    simplified: Cell<Option<bool>>,
    status: Arc<Mutex<UpdateStatus>>,
    sink: RefCell<Option<ITfLangBarItemSink>>,
    toggle: Box<dyn Fn()>,
    /// 選單的「輸出簡體字」：請引擎即時切換，回傳是否成功（不成功時改請 0c-ui 執行 0c-update）。
    simplify: Box<dyn Fn(bool) -> bool>,
    /// 開選單前向引擎重新確認「輸出簡體字」（設定程式剛改過時，這個程式還沒有新的快照）；結果經 set_state 回到按鈕。
    recheck: Box<dyn Fn()>,
    /// 最近一次交給系統的選單項目。選取時依它找 id：狀態在背景更新，「我的詞彙…」出現或消失會讓之後的 id 位移，
    /// 不能拿最新狀態重算（#125）。
    shown: RefCell<Vec<MenuItem>>,
}

impl LangBarButton {
    pub fn create(
        english: bool,
        simplified: Option<bool>,
        toggle: Box<dyn Fn()>,
        simplify: Box<dyn Fn(bool) -> bool>,
        recheck: Box<dyn Fn()>,
    ) -> ITfLangBarItemButton {
        crate::object_created();
        let status = Arc::new(Mutex::new(UpdateStatus::default()));
        let _ = refresh(status.clone(), false);
        LangBarButton {
            english: Cell::new(english),
            simplified: Cell::new(simplified),
            status,
            sink: RefCell::new(None),
            toggle,
            simplify,
            recheck,
            shown: RefCell::new(Vec::new()),
        }
        .into()
    }
}

impl Drop for LangBarButton {
    fn drop(&mut self) {
        crate::object_destroyed();
    }
}

/// 中英狀態或「輸出簡體字」改變時更新圖示與文字（「中」／「简」／「英」，#169）。
pub fn set_state(button: &ITfLangBarItemButton, english: bool, simplified: Option<bool>) {
    let this = unsafe { button.as_impl() };
    let shown = |e: bool, s: Option<bool>| (e, e || s.unwrap_or(false));
    let before = shown(this.english.get(), this.simplified.get());
    this.english.set(english);
    this.simplified.set(simplified);
    if before != shown(english, simplified) {
        // 先取出再通知：OnUpdate 期間宿主可能回頭呼叫 AdviseSink／UnadviseSink。
        let sink = this.sink.try_borrow().ok().and_then(|s| s.clone());
        if let Some(sink) = sink {
            unsafe {
                let _ = sink.OnUpdate(TF_LBI_ICON | TF_LBI_TEXT | TF_LBI_TOOLTIP);
            }
        }
    }
}

impl LangBarButton_Impl {
    fn items(&self) -> Vec<MenuItem> {
        // 這個程式可能已經開了好幾天，快取是當時的狀態：安裝完新版後第一次開選單還顯示舊版號，看起來像更新失敗（#244）。
        // 開選單前等一下新狀態，等不到才用快取；0c-ui 有 15 秒快取，通常立刻回來，只有快取過期時要多跑一次 0c-update。
        // 開選單是使用者的操作：0c-ui 不在就帶起來，接著按的項目不必再等它，下次開選單的狀態也是新的。
        let _ = refresh(self.status.clone(), true).recv_timeout(MENU_STATUS_WAIT);
        (self.recheck)(); // 本機管道一次 health（不啟動引擎），設定程式剛改過也打對勾
        let mut status = lock(&self.status).clone();
        // 引擎能即時切換時，「輸出簡體字」的勾選照引擎目前的值（比 0c-ui 快取 15 秒的狀態新）。
        if let Some(on) = self.simplified.get() {
            status.output_simplified = Some(on);
        }
        let items = menu_items(&status);
        if let Ok(mut shown) = self.shown.try_borrow_mut() {
            *shown = items.clone();
        }
        items
    }

    fn select(&self, id: u32) {
        let items = match self.shown.try_borrow() {
            Ok(shown) if !shown.is_empty() => shown.clone(),
            _ => menu_items(&lock(&self.status)),
        };
        if let Some(item) = items.into_iter().find(|i| i.id == id && !i.separator) {
            // 「輸出簡體字」（#169）：DLL 直接請引擎切換（同一條連線，沙箱宿主也可以），提示顯示在游標旁、
            // 指示器立刻換成「简」／「中」；舊引擎或引擎沒回應時才請 0c-ui 執行 0c-update（寫設定、套用、送通知）。
            if let Some(on) = simplified_target(&item.args) {
                if (self.simplify)(on) {
                    return;
                }
            }
            run(self.status.clone(), item.args);
        }
    }

    fn popup(&self, pt: &POINT) {
        let items = self.items();
        unsafe {
            let Ok(menu) = CreatePopupMenu() else { return };
            let mut bitmaps = Vec::new();
            for item in &items {
                if item.separator {
                    let _ = AppendMenuW(menu, MF_SEPARATOR, 0, PCWSTR::null());
                } else {
                    let label = wide(&item.label);
                    let flags = if item.checked { MF_STRING | MF_CHECKED } else { MF_STRING };
                    let _ = AppendMenuW(menu, flags, item.id as usize, PCWSTR::from_raw(label.as_ptr()));
                    let glyph = match item.args.iter().map(String::as_str).collect::<Vec<_>>().as_slice() {
                        ["open-settings"] => Some(GEAR),
                        ["open-settings", "vocab"] => Some(DICTIONARY),
                        _ => None,
                    };
                    if let Some(bitmap) = glyph.and_then(glyph_bitmap) {
                        let info = MENUITEMINFOW {
                            cbSize: std::mem::size_of::<MENUITEMINFOW>() as u32,
                            fMask: MIIM_BITMAP,
                            hbmpItem: bitmap,
                            ..Default::default()
                        };
                        let _ = SetMenuItemInfoW(menu, item.id, false, &info);
                        bitmaps.push(bitmap);
                    }
                }
            }
            // TrackPopupMenu 需要本執行緒擁有、且在前景的視窗。
            let owner = CreateWindowExW(
                WINDOW_EX_STYLE(0), w!("STATIC"), w!(""), WS_POPUP, pt.x, pt.y, 0, 0, None, None, None, None,
            );
            if let Ok(owner) = owner {
                let _ = SetForegroundWindow(owner);
                let cmd = TrackPopupMenuEx(menu, (TPM_RETURNCMD | TPM_NONOTIFY | TPM_RIGHTBUTTON).0, pt.x, pt.y, owner, None);
                let _ = PostMessageW(Some(owner), WM_NULL, Default::default(), Default::default());
                let _ = DestroyWindow(owner);
                if cmd.0 > 0 {
                    self.select(cmd.0 as u32);
                }
            }
            let _ = DestroyMenu(menu);
            for bitmap in bitmaps {
                let _ = DeleteObject(bitmap.into()); // DestroyMenu 不會刪除項目的點陣圖
            }
        }
    }
}

// 以下每個方法都由宿主經 COM 呼叫：一律以 guarded 攔 panic（漏一個，panic 穿過 FFI 就終止宿主，#89），
// 並先檢查宿主給的指標。
impl ITfLangBarItem_Impl for LangBarButton_Impl {
    fn GetInfo(&self, pinfo: *mut TF_LANGBARITEMINFO) -> windows_core::Result<()> {
        crate::guarded("LangBar.GetInfo", || {
            if pinfo.is_null() {
                return Err(E_POINTER.into());
            }
            let mut info = TF_LANGBARITEMINFO {
                clsidService: CLSID_ZUIN,
                guidItem: GUID_LBI_INPUTMODE,
                dwStyle: TF_LBI_STYLE_BTN_BUTTON | TF_LBI_STYLE_SHOWNINTRAY,
                ulSort: 0,
                szDescription: [0; 32],
            };
            for (i, c) in DESCRIPTION.encode_utf16().take(31).enumerate() {
                info.szDescription[i] = c;
            }
            unsafe { pinfo.write(info) };
            Ok(())
        })
    }
    fn GetStatus(&self) -> windows_core::Result<u32> {
        crate::guarded("LangBar.GetStatus", || Ok(0))
    }
    fn Show(&self, _fshow: windows_core::BOOL) -> windows_core::Result<()> {
        crate::guarded("LangBar.Show", || Ok(()))
    }
    fn GetTooltipString(&self) -> windows_core::Result<BSTR> {
        crate::guarded("LangBar.GetTooltipString", || {
            let attention = needs_attention(&lock(&self.status));
            let simplified = self.simplified.get().unwrap_or(false);
            Ok(BSTR::from(tooltip_text(self.english.get(), simplified, attention)))
        })
    }
}

impl ITfLangBarItemButton_Impl for LangBarButton_Impl {
    fn OnClick(&self, click: TfLBIClick, pt: &POINT, _prcarea: *const RECT) -> windows_core::Result<()> {
        crate::guarded("LangBarOnClick", || {
            if click == TF_LBI_CLK_LEFT {
                (self.toggle)();
            } else if click == TF_LBI_CLK_RIGHT {
                self.popup(pt);
            }
            Ok(())
        })
    }
    fn InitMenu(&self, pmenu: Ref<'_, ITfMenu>) -> windows_core::Result<()> {
        crate::guarded("LangBar.InitMenu", || {
            let menu = pmenu.ok()?;
            for item in self.items() {
                let label: Vec<u16> = item.label.encode_utf16().collect();
                let flags = if item.separator { TF_LBMENUF_SEPARATOR } else if item.checked { TF_LBMENUF_CHECKED } else { 0 };
                unsafe {
                    menu.AddMenuItem(item.id, flags, HBITMAP::default(), HBITMAP::default(), &label, std::ptr::null_mut())?;
                }
            }
            Ok(())
        })
    }
    fn OnMenuSelect(&self, wid: u32) -> windows_core::Result<()> {
        crate::guarded("LangBar.OnMenuSelect", || {
            self.select(wid);
            Ok(())
        })
    }
    fn GetIcon(&self) -> windows_core::Result<HICON> {
        crate::guarded("LangBar.GetIcon", || {
            text_icon(indicator_text(self.english.get(), self.simplified.get().unwrap_or(false))).ok_or_else(windows_core::Error::from_win32)
        })
    }
    fn GetText(&self) -> windows_core::Result<BSTR> {
        crate::guarded("LangBar.GetText", || {
            Ok(BSTR::from(indicator_text(self.english.get(), self.simplified.get().unwrap_or(false))))
        })
    }
}

impl ITfSource_Impl for LangBarButton_Impl {
    fn AdviseSink(&self, riid: *const GUID, punk: Ref<'_, windows_core::IUnknown>) -> windows_core::Result<u32> {
        crate::guarded("LangBar.AdviseSink", || {
            if riid.is_null() {
                return Err(E_INVALIDARG.into());
            }
            if unsafe { *riid } != ITfLangBarItemSink::IID {
                return Err(windows_core::HRESULT(0x8004_0202u32 as i32).into());
            }
            let sink: ITfLangBarItemSink = punk.ok()?.cast()?;
            // 宿主在 OnUpdate 期間重入時不 panic，回錯誤讓它稍後再試。
            *self.sink.try_borrow_mut().map_err(|_| windows_core::Error::from(E_UNEXPECTED))? = Some(sink);
            Ok(SINK_COOKIE)
        })
    }
    fn UnadviseSink(&self, cookie: u32) -> windows_core::Result<()> {
        crate::guarded("LangBar.UnadviseSink", || {
            if cookie == SINK_COOKIE {
                *self.sink.try_borrow_mut().map_err(|_| windows_core::Error::from(E_UNEXPECTED))? = None;
            }
            Ok(())
        })
    }
}

#[cfg(test)]
mod tests {
    use super::{connect_or_start, indicator_text, light_color, tooltip_text, when_ui_missing, MenuPipe};
    use std::cell::Cell;
    use std::time::Duration;

    const WAIT: Duration = Duration::from_millis(200);
    const POLL: Duration = Duration::from_millis(5);
    const RESTART: Duration = Duration::from_millis(60);

    #[test]
    fn running_ui_is_used_without_starting_another() {
        let starts = Cell::new(0);
        let got = connect_or_start(|| MenuPipe::Connected(7), || { starts.set(starts.get() + 1); true }, WAIT, POLL, RESTART);
        assert_eq!(got, Some(7));
        assert_eq!(starts.get(), 0);
    }

    #[test]
    fn missing_ui_is_started_and_the_connection_retried() {
        // #243：更新器請走舊版 0c-ui 之後，選單要自己帶起新版，連上再送命令。
        let (starts, tries) = (Cell::new(0), Cell::new(0));
        let got = connect_or_start(
            || {
                tries.set(tries.get() + 1);
                if starts.get() > 0 && tries.get() > 3 { MenuPipe::Connected(7) } else { MenuPipe::Missing }
            },
            || { starts.set(starts.get() + 1); true },
            WAIT,
            POLL,
            RESTART,
        );
        assert_eq!(got, Some(7));
        assert_eq!(starts.get(), 1, "很快就連上時不重複啟動");
    }

    #[test]
    fn sandboxed_host_gives_up_at_once_without_waiting() {
        // 沙箱宿主（搜尋框）啟動不了程式：start 回 false 就放棄，不白等。
        let tries = Cell::new(0);
        let begun = std::time::Instant::now();
        let got: Option<i32> =
            connect_or_start(|| { tries.set(tries.get() + 1); MenuPipe::Missing }, || false, Duration::from_secs(5), POLL, RESTART);
        assert_eq!(got, None);
        assert_eq!(tries.get(), 1);
        assert!(begun.elapsed() < Duration::from_secs(1));
    }

    #[test]
    fn start_is_retried_while_the_old_ui_is_exiting_and_the_wait_is_bounded() {
        // 舊版還占著單一實例時，新啟動的會立刻退出：等待中再啟動一次；一直連不上就到期放棄。
        let starts = Cell::new(0);
        let begun = std::time::Instant::now();
        let got: Option<i32> = connect_or_start(|| MenuPipe::Missing, || { starts.set(starts.get() + 1); true }, WAIT, POLL, RESTART);
        assert_eq!(got, None);
        assert!(starts.get() >= 2, "starts={}", starts.get());
        assert!(begun.elapsed() >= WAIT && begun.elapsed() < Duration::from_secs(2));
    }

    #[test]
    fn busy_pipe_waits_for_the_running_ui_without_starting_another() {
        // 0c-ui 在兩次 CreateNamedPipeW 之間時管道是 busy：它在執行，再啟動一個只會立刻退出。
        let (starts, tries) = (Cell::new(0), Cell::new(0));
        let got = connect_or_start(
            || {
                tries.set(tries.get() + 1);
                if tries.get() > 3 { MenuPipe::Connected(7) } else { MenuPipe::Unavailable }
            },
            || { starts.set(starts.get() + 1); true },
            WAIT,
            POLL,
            RESTART,
        );
        assert_eq!(got, Some(7));
        assert_eq!(starts.get(), 0);
    }

    #[test]
    fn busy_pipe_never_starts_and_the_wait_is_bounded() {
        let starts = Cell::new(0);
        let begun = std::time::Instant::now();
        let got: Option<i32> =
            connect_or_start(|| MenuPipe::Unavailable, || { starts.set(starts.get() + 1); true }, WAIT, POLL, RESTART);
        assert_eq!(got, None);
        assert_eq!(starts.get(), 0);
        assert!(begun.elapsed() >= WAIT && begun.elapsed() < Duration::from_secs(2));
    }

    #[test]
    fn busy_pipe_is_waited_for_in_a_sandboxed_host_too() {
        // 沙箱宿主放棄的只有「0c-ui 不在」：在執行、只是忙的時候照樣等得到（#243 沙箱宿主的選單）。
        let tries = Cell::new(0);
        let got = connect_or_start(
            || {
                tries.set(tries.get() + 1);
                if tries.get() > 3 { MenuPipe::Connected(7) } else { MenuPipe::Unavailable }
            },
            || when_ui_missing(true, true, || panic!("沙箱宿主不啟動程式")),
            WAIT,
            POLL,
            RESTART,
        );
        assert_eq!(got, Some(7));
    }

    #[test]
    fn only_the_user_operating_the_menu_starts_the_ui() {
        let spawns = Cell::new(0);
        let spawn = || { spawns.set(spawns.get() + 1); true };
        // 背景讀狀態（按鈕建立、選單操作之後的補讀）：不啟動，等別人帶起（Activate 的 warm_up 可能剛啟動）。
        assert!(when_ui_missing(false, false, spawn));
        assert_eq!(spawns.get(), 0);
        // 開選單、選項目：啟動，啟動成功才等。
        assert!(when_ui_missing(true, false, spawn));
        assert_eq!(spawns.get(), 1);
        assert!(!when_ui_missing(true, false, || false), "找不到執行檔就不等");
        // 沙箱宿主：都不啟動也不等。
        assert!(!when_ui_missing(true, true, spawn));
        assert!(!when_ui_missing(false, true, spawn));
        assert_eq!(spawns.get(), 1);
    }

    #[test]
    fn background_read_does_not_start_a_missing_ui_and_the_wait_is_bounded() {
        // 解除安裝之後已開著的程式：按鈕建立時讀狀態，不把 0c-ui 叫回來。
        let spawns = Cell::new(0);
        let got: Option<i32> = connect_or_start(
            || MenuPipe::Missing,
            || when_ui_missing(false, false, || { spawns.set(spawns.get() + 1); true }),
            WAIT,
            POLL,
            RESTART,
        );
        assert_eq!(got, None);
        assert_eq!(spawns.get(), 0);
    }

    #[test]
    fn indicator_marks_simplified_output() {
        assert_eq!(indicator_text(false, false), "中");
        assert_eq!(indicator_text(false, true), "简");
        assert_eq!(indicator_text(true, true), "英", "英文模式維持「英」");
        assert_eq!(tooltip_text(false, true, false), "靈犀注音（中・簡體輸出）");
        assert_eq!(tooltip_text(false, false, false), "靈犀注音（中文）");
        assert_eq!(tooltip_text(true, true, false), "靈犀注音（英文）");
        assert_eq!(tooltip_text(false, true, true), "靈犀注音（中・簡體輸出）：有待辦，按右鍵查看");
    }

    #[test]
    fn accent_brightness_picks_glyph_color() {
        assert!(!light_color(0xFFD4_7800)); // 預設藍 #0078D4：白字
        assert!(light_color(0xFFB7_EAD5)); // 淡綠 #D5EAB7：黑字
        assert!(!light_color(0xFF00_0000));
        assert!(light_color(0xFFFF_FFFF));
    }
}
