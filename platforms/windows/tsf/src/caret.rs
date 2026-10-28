//! 候選窗放在哪裡：GetTextExt 給不出游標位置時的退路，以及「為什麼沒顯示」的 tsf.log 紀錄（#144）。
//!
//! Chromium（Edge、Electron、WebView2）的版面由 renderer 非同步送回：剛打的字還沒排版時 GetTextExt 回
//! TS_E_NOLAYOUT，排好後才以 ITfTextLayoutSink::OnLayoutChange 通知（service.rs 在那時重查）。
//! Chromium 也沒有系統插入點，所以只靠 GetGUIThreadInfo 的舊退路一定落空，候選窗就不出現。
//! 這裡依序退到：系統插入點（有的程式照舊用它，和 #144 之前相同）、同一次組字上一次的位置、
//! 這個視窗上一次組字的位置（還在同一個輸入框裡才用）、輸入框範圍（GetScreenExt）或視窗客戶區，
//! 讓候選窗至少出現在游標附近；版面回來後再移到游標下方。
//!
//! Chromium 的 GetScreenExt 回傳有焦點的輸入元素範圍（裁到可見區域），拿不到時才回傳整個客戶區
//! （ui/base/ime/win/tsf_text_store.cc）。多行輸入框、網頁編輯器或客戶區很高，整塊當成游標會把候選窗
//! 放到它的下緣以下，放不下就翻到上緣以上，離游標很遠；所以高的範圍只取第一行。
use std::collections::HashSet;

use windows::Win32::Foundation::{HWND, POINT, RECT};
use windows::Win32::Graphics::Gdi::ClientToScreen;
use windows::Win32::UI::HiDpi::GetDpiForWindow;
use windows::Win32::UI::TextServices::ITfContextView;
use windows::Win32::UI::WindowsAndMessaging::{
    GetClientRect, GetForegroundWindow, GetGUIThreadInfo, GetWindowThreadProcessId, GUITHREADINFO,
};

use crate::log::zlog;

/// 候選窗位置的來源，由準到粗。
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Source {
    /// GetTextExt：游標的實際位置。
    Exact,
    /// 系統插入點（GetGUIThreadInfo）：輸入框視窗的執行緒，或前景執行緒。有插入點的程式由它決定，
    /// 比同一次組字的舊位置準。
    SystemCaret,
    /// 同一次組字最近一次查到的位置（新打的字版面還沒回來，差一兩個字寬）。
    Previous,
    /// 這個視窗上一次查到的位置（之前的組字），而且還在目前的輸入框範圍裡。
    Recent,
    /// 輸入框（GetScreenExt）或視窗客戶區的範圍：只知道大概在哪；矮的範圍放在它的下方，高的只取第一行。
    Field,
}

impl Source {
    pub fn name(self) -> &'static str {
        match self {
            Source::Exact => "游標位置",
            Source::SystemCaret => "系統插入點",
            Source::Previous => "同一次組字上一次的游標位置",
            Source::Recent => "這個視窗上一次的游標位置",
            Source::Field => "輸入框範圍",
        }
    }
}

/// 依序取第一個拿得到的位置；後面的來源只在前面都沒有時才查。
pub fn choose(
    exact: Option<RECT>,
    system_caret: impl FnOnce() -> Option<RECT>,
    previous: Option<RECT>,
    around_field: impl FnOnce() -> Option<(RECT, Source)>,
) -> Option<(RECT, Source)> {
    if let Some(r) = exact {
        return Some((r, Source::Exact));
    }
    if let Some(r) = system_caret() {
        return Some((r, Source::SystemCaret));
    }
    if let Some(r) = previous {
        return Some((r, Source::Previous));
    }
    around_field()
}

/// 只知道輸入框範圍（`field`）與這個視窗上一次的游標（`recent`）時的位置。
/// - 上一次的游標還在這個輸入框裡：用它（使用者多半接著同一處打字）。
/// - 範圍不高於三行：整塊當成游標，候選窗放在輸入框下方，不蓋住輸入框（單行輸入框）。
/// - 高的範圍（多行輸入框、網頁編輯器、整個客戶區）：只取第一行。空的輸入框游標就在這裡；
///   整塊當游標則會把候選窗推到下緣以下或上緣以上，離游標很遠。
/// `line` 是一行的高度：上一次游標的高度，沒有就依 DPI 估計。
pub fn around_field(field: Option<RECT>, recent: Option<RECT>, line: i32) -> Option<(RECT, Source)> {
    let field = field.filter(usable);
    if let Some(r) = recent.filter(|r| field.is_none_or(|f| inside(r, &f))) {
        return Some((r, Source::Recent));
    }
    let f = field?;
    let line = line.max(1);
    if f.bottom - f.top <= line * 3 {
        return Some((f, Source::Field));
    }
    Some((RECT { left: f.left, top: f.top, right: f.left + 1, bottom: f.top + line }, Source::Field))
}

fn usable(r: &RECT) -> bool {
    r.right > r.left && r.bottom > r.top
}

/// 游標矩形的左上角在範圍內（游標寬度常是 0，不看右下角）。
fn inside(caret: &RECT, field: &RECT) -> bool {
    (field.left..=field.right).contains(&caret.left) && (field.top..field.bottom).contains(&caret.top)
}

/// 不知道游標高度時估的一行高度：100% 縮放約 24 像素，依視窗 DPI 放大。
pub fn line_height(view_window: Option<HWND>, recent: Option<RECT>) -> i32 {
    if let Some(h) = recent.map(|r| r.bottom - r.top).filter(|h| *h > 0) {
        return h;
    }
    let dpi = view_window
        .filter(|h| !h.is_invalid())
        .map(|h| unsafe { GetDpiForWindow(h) })
        .filter(|d| *d > 0)
        .unwrap_or(96);
    (24 * dpi / 96) as i32
}

/// 呼叫端執行緒（宿主的 UI 執行緒）現在有沒有系統插入點。
pub fn thread_has_caret() -> bool {
    thread_caret(unsafe { windows::Win32::System::Threading::GetCurrentThreadId() }).is_some()
}

/// 執行緒的系統插入點（螢幕座標）。
fn thread_caret(thread: u32) -> Option<RECT> {
    let mut info = GUITHREADINFO {
        cbSize: std::mem::size_of::<GUITHREADINFO>() as u32,
        ..Default::default()
    };
    unsafe { GetGUIThreadInfo(thread, &mut info) }.ok()?;
    if info.hwndCaret.is_invalid() {
        return None;
    }
    client_to_screen(info.hwndCaret, info.rcCaret)
}

fn client_to_screen(hwnd: HWND, r: RECT) -> Option<RECT> {
    let mut tl = POINT { x: r.left, y: r.top };
    let mut br = POINT { x: r.right, y: r.bottom };
    unsafe {
        if !ClientToScreen(hwnd, &mut tl).as_bool() || !ClientToScreen(hwnd, &mut br).as_bool() {
            return None;
        }
    }
    Some(RECT { left: tl.x, top: tl.y, right: br.x, bottom: br.y })
}

/// 系統插入點：先看輸入框視窗（context view 的 HWND）所屬的執行緒，再看前景執行緒。
/// 只看前景執行緒在 WebView2 會落空：最上層視窗屬於宿主程式，輸入框在 msedgewebview2.exe。
pub fn system_caret(view_window: Option<HWND>) -> Option<RECT> {
    let view_thread = view_window
        .filter(|h| !h.is_invalid())
        .map(|h| unsafe { GetWindowThreadProcessId(h, None) })
        .filter(|t| *t != 0);
    if let Some(r) = view_thread.and_then(thread_caret) {
        return Some(r);
    }
    let foreground = unsafe { GetWindowThreadProcessId(GetForegroundWindow(), None) };
    if foreground == 0 || Some(foreground) == view_thread {
        return None;
    }
    thread_caret(foreground)
}

/// 輸入框的範圍：GetScreenExt（Chromium 回傳目前輸入框的範圍），沒有就用 view 視窗的客戶區。
pub fn field(view: Option<&ITfContextView>, view_window: Option<HWND>) -> Option<RECT> {
    if let Some(r) = view.and_then(|v| unsafe { v.GetScreenExt() }.ok()).filter(usable) {
        return Some(r);
    }
    let hwnd = view_window.filter(|h| !h.is_invalid())?;
    let mut client = RECT::default();
    unsafe { GetClientRect(hwnd, &mut client) }.ok()?;
    client_to_screen(hwnd, client).filter(usable)
}

/// 每種情況在 tsf.log 只記一次（每鍵都會跑，不能洗版）。去重看的是類別（失敗種類、退到哪裡），
/// 不含座標與視窗把手，種類有限；NOTE_LIMIT 只是防呆上限。
#[derive(Default)]
pub struct Notes(HashSet<String>);

const NOTE_LIMIT: usize = 64;

impl Notes {
    /// `key` 這種情況第一次出現時才呼叫 `detail` 並記錄它回傳的內容（None 表示查過、不必記）；
    /// 之後同一個 key 不再呼叫 `detail`，昂貴的查詢與字串組合只做一次。
    pub fn once(&mut self, key: &str, detail: impl FnOnce() -> Option<String>) {
        if self.0.len() >= NOTE_LIMIT || self.0.contains(key) {
            return;
        }
        self.0.insert(key.to_owned());
        if let Some(note) = detail() {
            zlog!("候選窗：{} exe={}", note, crate::log::exe_name());
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn rect(left: i32, top: i32, right: i32, bottom: i32) -> RECT {
        RECT { left, top, right, bottom }
    }

    #[test]
    fn missing_layout_falls_back_instead_of_hiding_candidates() {
        let caret = rect(100, 200, 101, 220);
        let older = rect(80, 200, 81, 220);
        let field_rect = rect(50, 180, 600, 240);
        // GetTextExt 有結果：其他來源都不查。
        assert_eq!(
            choose(Some(caret), || unreachable!(), Some(older), || unreachable!()),
            Some((caret, Source::Exact))
        );
        // 有系統插入點的程式照舊用它，即使同一次組字有較舊的位置（#144 之前的行為）。
        assert_eq!(
            choose(None, || Some(caret), Some(older), || unreachable!()),
            Some((caret, Source::SystemCaret))
        );
        // Chromium 新打的字版面還沒回來（TS_E_NOLAYOUT）、沒有系統插入點：用同一次組字上一次的位置。
        assert_eq!(choose(None, || None, Some(caret), || unreachable!()), Some((caret, Source::Previous)));
        // 組字的第一個字：退到輸入框範圍，候選窗不能不出現（#144）。
        assert_eq!(
            choose(None, || None, None, || around_field(Some(field_rect), None, 24)),
            Some((field_rect, Source::Field))
        );
        // 範圍是空的就真的沒有位置。
        assert_eq!(around_field(Some(rect(0, 0, 0, 0)), None, 24), None);
        assert_eq!(choose(None, || None, None, || around_field(None, None, 24)), None);
    }

    #[test]
    fn tall_fields_use_their_first_line_instead_of_the_whole_rect() {
        // 最大化的 Edge：GetScreenExt 退回整個客戶區（或很高的網頁編輯器）。整塊當成游標時候選窗要放在
        // 客戶區下緣以下，放不下就翻到上緣以上，離頁面中段的輸入框很遠。
        let client = rect(0, 80, 1920, 1040);
        assert_eq!(around_field(Some(client), None, 24), Some((rect(0, 80, 1, 104), Source::Field)));
        // 單行輸入框（不高於三行）：整塊當游標，候選窗在輸入框下方、不蓋住它。
        let input = rect(400, 500, 900, 540);
        assert_eq!(around_field(Some(input), None, 24), Some((input, Source::Field)));
    }

    #[test]
    fn recent_caret_is_used_only_inside_the_current_field() {
        let textarea = rect(400, 300, 1200, 900);
        let recent = rect(620, 640, 620, 668);
        // 上一次組字就在這個輸入框裡：接著用那個位置，不退到第一行。
        assert_eq!(around_field(Some(textarea), Some(recent), 28), Some((recent, Source::Recent)));
        // 焦點換到同一頁的另一個輸入框（同一個 HWND）：上一次的位置不在範圍裡，不用。
        let other = rect(400, 100, 900, 140);
        assert_eq!(around_field(Some(other), Some(recent), 28), Some((other, Source::Field)));
        // 拿不到範圍時，同一個視窗上一次的位置仍比什麼都沒有好。
        assert_eq!(around_field(None, Some(recent), 28), Some((recent, Source::Recent)));
    }

    #[test]
    fn notes_are_keyed_by_class_and_details_are_built_once() {
        let mut notes = Notes::default();
        let mut built = 0;
        for x in 0..100 {
            // 座標每次不同，類別相同：只組一次字串、只記一次，不會把上限吃光。
            notes.once("empty→輸入框範圍", || {
                built += 1;
                Some(format!("空矩形 ({x},0,0,0)"))
            });
        }
        assert_eq!(built, 1);
        notes.once("foreground", || None);
        notes.once("foreground", || unreachable!());
        assert_eq!(notes.0.len(), 2);
    }

    #[test]
    fn system_caret_without_window_does_not_panic() {
        // 測試行程沒有插入點；只確認無效視窗不會出錯。
        let _ = system_caret(None);
        let _ = system_caret(Some(HWND::default()));
        assert_eq!(field(None, None), None);
        assert_eq!(line_height(None, None), 24);
        assert_eq!(line_height(None, Some(rect(0, 0, 0, 30))), 30);
    }
}
