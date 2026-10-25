//! 候選窗的繪製與視窗行為。`0c-ui.exe` 用它畫一般桌面上的候選窗；TSF DLL 在沙箱或系統沉浸式
//! 介面（例如 Windows 搜尋框）裡，用同一份程式在宿主行程內畫候選窗——另一個行程的視窗
//! 在一般桌面層，會被這些介面蓋住，宿主行程擁有的視窗才和它們同層。
//! 畫面狀態跟著視窗（GWLP_USERDATA），同一行程可以有多個候選窗（每個 TSF 執行緒一個）。
use crate::color_text;
use crate::palette::{self, Palette};
use crate::placement;

use std::cell::RefCell;
use std::ffi::c_void;
use std::rc::Rc;

use windows::Win32::Foundation::{HINSTANCE, HWND, LPARAM, LRESULT, POINT, RECT, SIZE, WPARAM};
use windows::Win32::Graphics::Gdi::{
    BeginPaint, CreateFontW, CreateSolidBrush, DeleteObject, DrawTextW, EndPaint, FillRect, GetDC,
    GetMonitorInfoW, GetTextExtentPoint32W, InvalidateRect, MonitorFromPoint, ReleaseDC,
    SelectObject, SetBkMode, SetTextColor, TextOutW, CLEARTYPE_QUALITY, CLIP_DEFAULT_PRECIS,
    DEFAULT_CHARSET, DEFAULT_PITCH, DT_CALCRECT, DT_CENTER, DT_NOPREFIX, DT_WORDBREAK, FW_NORMAL,
    HBRUSH, HDC, HFONT, HGDIOBJ, MONITORINFO, MONITOR_DEFAULTTONEAREST, OUT_DEFAULT_PRECIS,
    PAINTSTRUCT, TRANSPARENT,
};
use windows::Win32::UI::HiDpi::GetDpiForWindow;
use windows::Win32::UI::Shell::{SHQueryUserNotificationState, QUERY_USER_NOTIFICATION_STATE, QUNS_RUNNING_D3D_FULL_SCREEN};
use windows::Win32::UI::WindowsAndMessaging::{
    CreateWindowExW, GetAncestor, GetClientRect, GetForegroundWindow, GetGUIThreadInfo, GetWindow,
    GetWindowLongPtrW, GetWindowThreadProcessId, GUITHREADINFO, KillTimer, LoadCursorW, PostMessageW,
    RegisterClassW, SetTimer, SetWindowLongPtrW, SetWindowPos, ShowWindow, CS_HREDRAW, CS_VREDRAW,
    GA_ROOT, GWLP_USERDATA, GW_OWNER, HWND_TOPMOST, IDC_ARROW,
    MA_NOACTIVATE, SWP_NOACTIVATE, SWP_SHOWWINDOW, SW_HIDE, WM_APP, WM_DPICHANGED, WM_LBUTTONUP,
    WM_MOUSEACTIVATE, WM_NCDESTROY, WM_PAINT, WM_PRINTCLIENT, WM_SETTINGCHANGE, WM_SYSCOLORCHANGE,
    WM_THEMECHANGED, WM_TIMER, WNDCLASSW, WNDPROC, WS_EX_NOACTIVATE, WS_EX_TOOLWINDOW, WS_EX_TOPMOST,
    WS_POPUP,
};
use windows_core::{w, PCWSTR};
use zuin_win_common::{
    candidate_cells, candidate_key, has_content, header_height, header_lines, hit_candidate, large_heading, notice_expires,
    notice_row, UiCandidate, UiMessage, NOTICE_MS, WM_SELECT_CANDIDATE,
};

const WM_ZUIN_REFLOW: u32 = WM_APP + 2;
/// 一次性提示（notice）顯示 NOTICE_MS 後收起；下一鍵的訊息沒有提示時也會收起。
/// 標示加詞範圍時的說明（marking）不計時，留到標示結束。
const NOTICE_TIMER: usize = 0x0c01;

type State = RefCell<Option<UiMessage>>;

/// 註冊候選窗的視窗類別；wndproc 由呼叫端提供（DLL 要在裡面攔 panic），內容交給 [`handle`]。
pub fn register_class(instance: HINSTANCE, class: PCWSTR, wndproc: WNDPROC) {
    unsafe {
        RegisterClassW(&WNDCLASSW {
            style: CS_HREDRAW | CS_VREDRAW,
            lpfnWndProc: wndproc,
            hInstance: instance,
            hCursor: LoadCursorW(None, IDC_ARROW).unwrap_or_default(),
            lpszClassName: class,
            ..Default::default()
        });
    }
}

/// 建立不搶焦點的 topmost 候選窗。owner 決定視窗所在的層級：沉浸式介面要用宿主的視窗當 owner。
pub fn create(instance: HINSTANCE, class: PCWSTR, owner: Option<HWND>) -> windows_core::Result<HWND> {
    unsafe {
        let hwnd = CreateWindowExW(
            WS_EX_TOOLWINDOW | WS_EX_NOACTIVATE | WS_EX_TOPMOST,
            class,
            w!("Zuin"),
            WS_POPUP,
            0,
            0,
            10,
            10,
            owner,
            None,
            Some(instance),
            None,
        )?;
        let state: Box<State> = Box::new(RefCell::new(None));
        SetWindowLongPtrW(hwnd, GWLP_USERDATA, Box::into_raw(state) as _); // `as _`：32 位元版是 SetWindowLongW（i32）
        Ok(hwnd)
    }
}

fn with_state<R>(hwnd: HWND, f: impl FnOnce(&State) -> R) -> Option<R> {
    let ptr = unsafe { GetWindowLongPtrW(hwnd, GWLP_USERDATA) } as *const State;
    unsafe { ptr.as_ref() }.map(f)
}

fn current(hwnd: HWND) -> Option<UiMessage> {
    with_state(hwnd, |s| s.borrow().clone()).flatten()
}

struct Theme {
    font: HFONT,
    emoji: HFONT,
    color_emoji: Option<color_text::ColorText>,
    small: HFONT,
    bg: HBRUSH,
    border: HBRUSH,
    highlight: HBRUSH,
    pad: i32,
    row: i32,
    small_row: i32,
    colors: Palette,
}

thread_local! {
    static THEME: RefCell<Option<(u32, Rc<Theme>)>> = const { RefCell::new(None) };
}

/// 字型與筆刷依 DPI 與系統主題快取；系統變更訊息會清除快取。
fn theme(dpi: u32) -> Rc<Theme> {
    THEME.with(|cell| {
        let mut cached = cell.borrow_mut();
        if let Some((d, t)) = cached.as_ref() {
            if *d == dpi {
                return t.clone();
            }
        }
        let t = Rc::new(build_theme(dpi));
        *cached = Some((dpi, t.clone()));
        t
    })
}

fn build_theme(dpi: u32) -> Theme {
    let scale = |px: i32| (px * dpi as i32 + 48) / 96;
    let colors = palette::system_palette();
    unsafe {
        let font = CreateFontW(
            -scale(18),
            0,
            0,
            0,
            FW_NORMAL.0 as i32,
            0,
            0,
            0,
            DEFAULT_CHARSET,
            OUT_DEFAULT_PRECIS,
            CLIP_DEFAULT_PRECIS,
            CLEARTYPE_QUALITY,
            DEFAULT_PITCH.0 as u32,
            w!("Microsoft JhengHei UI"),
        );
        let small = CreateFontW(
            -scale(13),
            0,
            0,
            0,
            FW_NORMAL.0 as i32,
            0,
            0,
            0,
            DEFAULT_CHARSET,
            OUT_DEFAULT_PRECIS,
            CLIP_DEFAULT_PRECIS,
            CLEARTYPE_QUALITY,
            DEFAULT_PITCH.0 as u32,
            w!("Microsoft JhengHei UI"),
        );
        Theme {
            font,
            color_emoji: if colors.color_fonts {
                color_text::ColorText::new(scale(26) as f32).ok()
            } else {
                None
            },
            emoji: CreateFontW(
                -scale(26),
                0,
                0,
                0,
                FW_NORMAL.0 as i32,
                0,
                0,
                0,
                DEFAULT_CHARSET,
                OUT_DEFAULT_PRECIS,
                CLIP_DEFAULT_PRECIS,
                CLEARTYPE_QUALITY,
                DEFAULT_PITCH.0 as u32,
                w!("Segoe UI Emoji"),
            ),
            small,
            bg: CreateSolidBrush(windows::Win32::Foundation::COLORREF(colors.background)),
            border: CreateSolidBrush(windows::Win32::Foundation::COLORREF(colors.border)),
            highlight: CreateSolidBrush(windows::Win32::Foundation::COLORREF(colors.selection)),
            colors,
            pad: scale(8),
            row: scale(28),
            small_row: scale(20),
        }
    }
}

impl Drop for Theme {
    fn drop(&mut self) {
        unsafe {
            let _ = DeleteObject(HGDIOBJ(self.font.0));
            let _ = DeleteObject(HGDIOBJ(self.emoji.0));
            let _ = DeleteObject(HGDIOBJ(self.small.0));
            let _ = DeleteObject(HGDIOBJ(self.bg.0));
            let _ = DeleteObject(HGDIOBJ(self.border.0));
            let _ = DeleteObject(HGDIOBJ(self.highlight.0));
        }
    }
}

fn measure(hdc: HDC, font: HFONT, text: &[u16]) -> i32 {
    unsafe {
        let old = SelectObject(hdc, HGDIOBJ(font.0));
        let mut size = SIZE::default();
        let _ = GetTextExtentPoint32W(hdc, text, &mut size);
        SelectObject(hdc, old);
        size.cx
    }
}

/// 候選列的文字：鍵標（#112，1、q、;；英文補字沒有）、候選，有附註（模糊音近音）時接在候選之後。
fn candidate_line(i: usize, c: &UiCandidate) -> String {
    let key = candidate_key(i, c);
    let prefix = if key.is_empty() { String::new() } else { format!("{key}. ") };
    if c.note.is_empty() {
        format!("{prefix}{}", c.text)
    } else {
        format!("{prefix}{}　〔{}〕", c.text, c.note)
    }
}

/// 補全提示列開頭的「⇥ 」：用提示色畫，後面的補全內容用一般文字色（#118）。
const SUGGESTION_MARK: &str = "⇥ ";

/// 直排候選窗的一列是什麼：提示（一般字級、強調色）、讀音提示（一般字級、一般文字色，#162）、
/// 補全提示（一般字級，⇥ 用提示色、內容一般文字色，#118）、小字（標題、頁碼）、候選。
#[derive(Clone, Copy, Debug, PartialEq)]
enum Row {
    Notice,
    Reading,
    Suggestion,
    Small,
    Candidate { highlighted: bool },
}

impl Row {
    /// 列高；提示列與 common::header_height 用同一個值，命中測試才對得上。
    fn height(self, t: &Theme) -> i32 {
        match self {
            Row::Notice | Row::Reading | Row::Suggestion => notice_row(t.small_row),
            Row::Small => t.small_row,
            Row::Candidate { .. } => t.row,
        }
    }
    fn font(self, t: &Theme) -> HFONT {
        if self == Row::Small {
            t.small
        } else {
            t.font
        }
    }
    /// 字的高度（垂直置中用）。
    fn glyph(self, t: &Theme) -> i32 {
        if self == Row::Small {
            t.small_row - 4
        } else {
            t.row - 6
        }
    }
}

/// 一列列的內容。候選之前的列由 header_lines 決定（提示優先：有提示時不畫標題）。
fn lines(m: &UiMessage) -> Vec<(Vec<u16>, Row)> {
    let mut out = Vec::new();
    for heading in header_lines(m) {
        if !m.notice.is_empty() && heading == m.notice {
            out.push((heading.encode_utf16().collect(), Row::Notice));
        } else if !m.reading.is_empty() && heading == m.reading {
            out.push((heading.encode_utf16().collect(), Row::Reading));
        } else if heading == m.suggestion {
            out.push((format!("{SUGGESTION_MARK}{heading}").encode_utf16().collect(), Row::Suggestion));
        } else {
            out.push((heading.encode_utf16().collect(), Row::Small));
        }
    }
    for (i, c) in m.candidates.iter().enumerate() {
        out.push((
            candidate_line(i, c).encode_utf16().collect(),
            Row::Candidate { highlighted: i as i64 == m.highlight },
        ));
    }
    if m.pages > 1 {
        out.push((
            format!("{} / {}　← → 翻頁", m.page, m.pages)
                .encode_utf16()
                .collect(),
            Row::Small,
        ));
    }
    out
}

fn grid_font(m: &UiMessage, t: &Theme) -> HFONT {
    if m.kind == "emoji" {
        t.emoji
    } else {
        t.font
    }
}

// 單欄 Emoji 仍用彩色格子；量測、繪製與滑鼠命中必須採同一份幾何。
fn uses_grid(m: &UiMessage) -> bool {
    m.columns > 1 || m.kind == "emoji"
}

fn grid_row(m: &UiMessage, width: i32, t: &Theme, hdc: HDC) -> i32 {
    unsafe {
        let old = SelectObject(hdc, HGDIOBJ(grid_font(m, t).0));
        let cells = candidate_cells(m, width, t.pad, t.row, t.small_row);
        let mut height = t.row;
        for (index, cell) in cells.iter().enumerate() {
            let mut text: Vec<u16> = candidate_line(index, &m.candidates[index])
                .encode_utf16()
                .collect();
            let mut rect = RECT {
                right: (cell.width - t.pad * 2).max(1),
                ..Default::default()
            };
            DrawTextW(
                hdc,
                &mut text,
                &mut rect,
                DT_CALCRECT | DT_WORDBREAK | DT_NOPREFIX,
            );
            height = height.max(rect.bottom - rect.top + t.pad * 2);
        }
        SelectObject(hdc, old);
        height
    }
}

fn grid_size(m: &UiMessage, max_width: i32, t: &Theme, hdc: HDC) -> (i32, i32) {
    let columns = m.columns.clamp(1, 10) as i32;
    let cell = m
        .candidates
        .iter()
        .enumerate()
        .map(|(i, candidate)| {
            measure(
                hdc,
                grid_font(m, t),
                &candidate_line(i, candidate)
                    .encode_utf16()
                    .collect::<Vec<_>>(),
            ) + t.pad * 2
        })
        .max()
        .unwrap_or(t.row * 3)
        .max(t.row * 3);
    let headings = header_lines(m)
        .iter()
        .map(|h| {
            let font = if large_heading(m, h) { t.font } else { t.small };
            measure(hdc, font, &h.encode_utf16().collect::<Vec<_>>()) + t.pad * 3
        })
        .max()
        .unwrap_or(0);
    let width = (cell * columns + t.pad * 2 + (columns - 1) * (t.pad / 2))
        .max(headings)
        .min(max_width)
        .max(1);
    let row = grid_row(m, width, t, hdc);
    let rows = (m.candidates.len() as i32 + columns - 1) / columns;
    (width, 2 * t.pad + row * rows + header_height(m, t.small_row) + t.small_row)
}

unsafe fn draw_grid(m: &UiMessage, hdc: HDC, width: i32, t: &Theme) {
    let row = grid_row(m, width, t, hdc);
    let mut heading_y = t.pad;
    let small_old = SelectObject(hdc, HGDIOBJ(t.small.0));
    for heading in header_lines(m) {
        let notice = !m.notice.is_empty() && heading == m.notice;
        let (color, font, height) = if notice {
            (t.colors.notice, t.font, notice_row(t.small_row))
        } else if large_heading(m, heading) {
            (t.colors.text, t.font, notice_row(t.small_row))
        } else {
            (t.colors.hint, t.small, t.small_row)
        };
        SetTextColor(hdc, windows::Win32::Foundation::COLORREF(color));
        SelectObject(hdc, HGDIOBJ(font.0));
        let text: Vec<u16> = heading.encode_utf16().collect();
        let _ = TextOutW(hdc, t.pad, heading_y, &text);
        heading_y += height;
    }
    SelectObject(hdc, small_old);
    let old = SelectObject(hdc, HGDIOBJ(grid_font(m, t).0));
    let cells = candidate_cells(m, width, t.pad, row, t.small_row);
    for (i, cell) in cells.iter().enumerate() {
        let selected = i as i64 == m.highlight;
        let mut rect = RECT {
            left: cell.left,
            top: cell.top,
            right: cell.left + cell.width,
            bottom: cell.top + cell.height,
        };
        if selected {
            FillRect(hdc, &rect, t.highlight);
        }
        let foreground = if selected {
            t.colors.selected_text
        } else {
            t.colors.text
        };
        let background = if selected {
            t.colors.selection
        } else {
            t.colors.background
        };
        let mut text: Vec<u16> = candidate_line(i, &m.candidates[i]).encode_utf16().collect();
        if m.kind == "emoji"
            && t.color_emoji.as_ref().is_some_and(|renderer| {
                renderer
                    .draw(hdc, rect, &text, foreground, background)
                    .is_ok()
            })
        {
            continue;
        }
        SetTextColor(
            hdc,
            windows::Win32::Foundation::COLORREF(if selected {
                t.colors.selected_text
            } else {
                t.colors.text
            }),
        );
        rect.left += t.pad;
        rect.right -= t.pad;
        rect.top += t.pad / 2;
        let flags = DT_WORDBREAK
            | DT_NOPREFIX
            | if m.kind == "emoji" {
                DT_CENTER
            } else {
                Default::default()
            };
        DrawTextW(hdc, &mut text, &mut rect, flags);
    }
    SelectObject(hdc, old);
    let bottom = cells.last().map(|cell| cell.top + row).unwrap_or(heading_y);
    let old = SelectObject(hdc, HGDIOBJ(t.small.0));
    SetTextColor(hdc, windows::Win32::Foundation::COLORREF(t.colors.hint));
    let text: Vec<u16> = format!("{} / {}　方向鍵移動 · PgUp/PgDn 翻頁", m.page, m.pages)
        .encode_utf16()
        .collect();
    let _ = TextOutW(hdc, t.pad, bottom, &text);
    SelectObject(hdc, old);
}


/// 宿主行程內的候選窗：宿主的文字框可能是嵌在別的行程視窗裡的子視窗（檔案總管的搜尋框是 FESearchHost
/// 沙箱的 CoreWindow，嵌在 explorer.exe 的視窗裡，#105），前景視窗屬於外層行程。候選窗的 owner
/// 由系統換成該子視窗的頂層視窗；它就是前景視窗時照常顯示。0c-ui 的候選窗沒有 owner，只比對行程。
fn owned_by_foreground(hwnd: HWND, foreground: HWND) -> bool {
    if foreground.is_invalid() {
        return false;
    }
    unsafe { GetWindow(hwnd, GW_OWNER) }
        .ok()
        .filter(|owner| !owner.is_invalid())
        .is_some_and(|owner| unsafe { GetAncestor(owner, GA_ROOT) } == foreground)
}

/// 送出訊息的宿主是不是使用者眼前的程式：前景視窗屬於它，或前景執行緒的鍵盤焦點在它的視窗。
/// 焦點這一條給 WebView2（新版 Teams、Outlook 等）：最上層視窗屬於宿主程式（ms-teams.exe），
/// 有焦點的子視窗與 TSF 屬於 msedgewebview2.exe。跨行程的父子視窗共用輸入狀態，前景執行緒的
/// 焦點視窗就是 WebView2 的視窗；只比前景視窗的行程，候選窗永遠不會出現、也點不了（#144）。
fn in_front(owner: u32, foreground_pid: u32, focus_pid: impl FnOnce() -> Option<u32>) -> bool {
    foreground_pid == owner || focus_pid() == Some(owner)
}

/// 前景執行緒的鍵盤焦點視窗所屬的行程。
fn focus_pid(foreground_thread: u32) -> Option<u32> {
    let mut info = GUITHREADINFO {
        cbSize: std::mem::size_of::<GUITHREADINFO>() as u32,
        ..Default::default()
    };
    unsafe { GetGUIThreadInfo(foreground_thread, &mut info) }.ok()?;
    if info.hwndFocus.is_invalid() {
        return None;
    }
    let mut pid = 0;
    unsafe { GetWindowThreadProcessId(info.hwndFocus, Some(&mut pid)) };
    (pid != 0).then_some(pid)
}

/// `foreground` 視窗（GetForegroundWindow）是不是 `pid` 行程在用的：見 in_front。
/// TSF DLL 核對滑鼠選字時也用它。
pub fn foreground_belongs_to(pid: u32, foreground: HWND) -> bool {
    if foreground.is_invalid() {
        return false;
    }
    let mut foreground_pid = 0;
    let thread = unsafe { GetWindowThreadProcessId(foreground, Some(&mut foreground_pid)) };
    in_front(pid, foreground_pid, || focus_pid(thread))
}

/// 使用者狀態是不是「D3D 獨佔全螢幕」。無邊框全螢幕是 QUNS_BUSY、簡報模式是 QUNS_PRESENTATION_MODE，
/// 都由 DWM 合成，疊上候選窗不會把程式踢出全螢幕。
fn yields_to(state: QUERY_USER_NOTIFICATION_STATE) -> bool {
    state == QUNS_RUNNING_D3D_FULL_SCREEN
}

/// 前景程式正在 D3D 獨佔全螢幕（SHQueryUserNotificationState；系統不通知變化，每次顯示前查，約 0.1–0.3 ms）。
/// 查不到時當作不是。
fn exclusive_fullscreen() -> bool {
    unsafe { SHQueryUserNotificationState() }.is_ok_and(yields_to)
}

/// 依呈現訊息顯示、移動或隱藏候選窗。
pub fn present(hwnd: HWND, m: UiMessage) {
    unsafe {
        if !m.show || !has_content(&m) {
            let other = with_state(hwnd, |s| {
                let mut state = s.borrow_mut();
                if state.as_ref().map(|current| current.owner != m.owner).unwrap_or(false) {
                    return true;
                }
                *state = None;
                false
            });
            if other != Some(true) {
                let _ = ShowWindow(hwnd, SW_HIDE);
            }
            return;
        }
        let foreground = GetForegroundWindow();
        if !foreground_belongs_to(m.owner, foreground) && !owned_by_foreground(hwnd, foreground) {
            return;
        }
        // 送出者是獨佔全螢幕的遊戲（D3D exclusive）：疊上任何最上層視窗都會讓它失去前景與全螢幕、
        // 跳回桌面（#159）。寧可看不到候選（照常組字、可用數字鍵選字），不出視窗；無邊框視窗由 DWM 合成，照常顯示。
        if exclusive_fullscreen() {
            let _ = KillTimer(Some(hwnd), NOTICE_TIMER);
            with_state(hwnd, |s| *s.borrow_mut() = None);
            let _ = ShowWindow(hwnd, SW_HIDE);
            return;
        }
        let dpi = GetDpiForWindow(hwnd);
        let t = theme(dpi);
        // 位置：游標下方；放不下就放上方；不超出該螢幕的工作區。
        let monitor = MonitorFromPoint(POINT { x: m.x, y: m.y }, MONITOR_DEFAULTTONEAREST);
        let mut info = MONITORINFO {
            cbSize: std::mem::size_of::<MONITORINFO>() as u32,
            ..Default::default()
        };
        let _ = GetMonitorInfoW(monitor, &mut info);
        if info.rcWork.right <= info.rcWork.left || info.rcWork.bottom <= info.rcWork.top {
            return;
        }
        let hdc = GetDC(Some(hwnd));
        let mut width = 0;
        let mut height = t.pad;
        for (text, row) in lines(&m) {
            width = width.max(measure(hdc, row.font(&t), &text));
            height += row.height(&t);
        }
        width += t.pad * 3;
        height += t.pad;
        width = width.max(t.row * 3);
        // Emoji 的單欄退回也使用同一個彩色字型路徑。
        if uses_grid(&m) {
            (width, height) = grid_size(&m, info.rcWork.right - info.rcWork.left, &t, hdc);
        }
        ReleaseDC(Some(hwnd), hdc);
        let bounds = placement::place(m.x, m.y, m.h, width, height, info.rcWork);
        // 提示幾秒後自動收起（WM_TIMER）；每次新的提示重新計時，沒有提示或標示中的說明就不計時。
        if notice_expires(&m) {
            SetTimer(Some(hwnd), NOTICE_TIMER, NOTICE_MS, None);
        } else {
            let _ = KillTimer(Some(hwnd), NOTICE_TIMER);
        }
        with_state(hwnd, |s| *s.borrow_mut() = Some(m));
        let _ = SetWindowPos(
            hwnd,
            Some(HWND_TOPMOST),
            bounds.left,
            bounds.top,
            bounds.right - bounds.left,
            bounds.bottom - bounds.top,
            SWP_NOACTIVATE | SWP_SHOWWINDOW,
        );
        let _ = InvalidateRect(Some(hwnd), None, true);
    }
}

fn paint(hwnd: HWND) {
    unsafe {
        let mut ps = PAINTSTRUCT::default();
        let hdc = BeginPaint(hwnd, &mut ps);
        draw(hwnd, hdc);
        let _ = EndPaint(hwnd, &ps);
    }
}

/// WM_PAINT 與 WM_PRINTCLIENT 共用繪製，截圖不必讀取整個桌面。
unsafe fn draw(hwnd: HWND, hdc: HDC) {
    let dpi = GetDpiForWindow(hwnd);
    let t = theme(dpi);
    let mut rc = RECT::default();
    let _ = GetClientRect(hwnd, &mut rc);
    FillRect(hdc, &rc, t.border);
    let inner = RECT {
        left: rc.left + 1,
        top: rc.top + 1,
        right: rc.right - 1,
        bottom: rc.bottom - 1,
    };
    FillRect(hdc, &inner, t.bg);
    SetBkMode(hdc, TRANSPARENT);
    if let Some(m) = current(hwnd) {
        if uses_grid(&m) {
            draw_grid(&m, hdc, rc.right - rc.left, &t);
            return;
        }
        let mut y = t.pad;
        for (text, kind) in lines(&m) {
            let row = kind.height(&t);
            let highlighted = kind == Row::Candidate { highlighted: true };
            if highlighted {
                let r = RECT {
                    left: inner.left + t.pad / 2,
                    top: y,
                    right: inner.right - t.pad / 2,
                    bottom: y + row,
                };
                FillRect(hdc, &r, t.highlight);
            }
            let color = if highlighted {
                t.colors.selected_text
            } else if kind == Row::Notice {
                t.colors.notice
            } else if kind == Row::Small {
                t.colors.hint
            } else {
                t.colors.text
            };
            let old = SelectObject(hdc, HGDIOBJ(kind.font(&t).0));
            let top = y + (row - kind.glyph(&t)) / 2;
            let mut x = inner.left + t.pad;
            let mut body = &text[..];
            if kind == Row::Suggestion {
                let mark = SUGGESTION_MARK.encode_utf16().count().min(text.len());
                SetTextColor(hdc, windows::Win32::Foundation::COLORREF(t.colors.hint));
                let _ = TextOutW(hdc, x, top, &text[..mark]);
                x += measure(hdc, kind.font(&t), &text[..mark]);
                body = &text[mark..];
            }
            SetTextColor(hdc, windows::Win32::Foundation::COLORREF(color));
            let _ = TextOutW(hdc, x, top, body);
            SelectObject(hdc, old);
            y += row;
        }
    }
}

/// 候選窗的視窗訊息；不是候選窗負責的訊息回傳 None，由呼叫端交給 DefWindowProc。
pub fn handle(hwnd: HWND, msg: u32, wparam: WPARAM, lparam: LPARAM) -> Option<LRESULT> {
    unsafe {
        match msg {
            WM_SETTINGCHANGE | WM_SYSCOLORCHANGE | WM_THEMECHANGED => {
                THEME.with(|cell| *cell.borrow_mut() = None);
                let _ = InvalidateRect(Some(hwnd), None, true);
                Some(LRESULT(0))
            }
            WM_DPICHANGED => {
                // SetWindowPos 可同步觸發此訊息；排程重新量測，避免遞迴。
                THEME.with(|cell| *cell.borrow_mut() = None);
                let _ = PostMessageW(Some(hwnd), WM_ZUIN_REFLOW, WPARAM(0), LPARAM(0));
                Some(LRESULT(0))
            }
            WM_ZUIN_REFLOW => {
                // 此時才取最新畫面，避免 DPI 事件把較舊的候選重新蓋回來。
                if let Some(m) = current(hwnd) {
                    present(hwnd, m);
                }
                Some(LRESULT(0))
            }
            WM_TIMER if wparam.0 == NOTICE_TIMER => {
                let _ = KillTimer(Some(hwnd), NOTICE_TIMER);
                if let Some(m) = current(hwnd).filter(notice_expires) {
                    present(hwnd, without_notice(m));
                }
                Some(LRESULT(0))
            }
            WM_PRINTCLIENT => {
                draw(hwnd, HDC(wparam.0 as *mut c_void));
                Some(LRESULT(0))
            }
            WM_PAINT => {
                paint(hwnd);
                Some(LRESULT(0))
            }
            WM_LBUTTONUP => {
                if let Some(m) = current(hwnd) {
                    click(hwnd, &m, lparam);
                }
                Some(LRESULT(0))
            }
            WM_MOUSEACTIVATE => Some(LRESULT(MA_NOACTIVATE as isize)),
            WM_NCDESTROY => {
                let ptr = GetWindowLongPtrW(hwnd, GWLP_USERDATA) as *mut State;
                SetWindowLongPtrW(hwnd, GWLP_USERDATA, 0);
                if !ptr.is_null() {
                    drop(Box::from_raw(ptr));
                }
                None
            }
            _ => None,
        }
    }
}

/// 提示到時收起：拿掉提示後只剩候選或補全提示就照常重畫，什麼都沒有就隱藏（present 處理）。
fn without_notice(mut m: UiMessage) -> UiMessage {
    m.notice.clear();
    if !has_content(&m) {
        m.show = false;
    }
    m
}

unsafe fn click(hwnd: HWND, m: &UiMessage, lparam: LPARAM) {
    let t = theme(GetDpiForWindow(hwnd));
    let y = ((lparam.0 >> 16) as u16) as i16 as i32;
    let target = HWND(m.target as usize as *mut c_void);
    let mut target_pid = 0;
    GetWindowThreadProcessId(target, Some(&mut target_pid));
    // 前景要是送出者在用的視窗（WebView2 的焦點在另一個行程的子視窗，#144）。
    if !(m.show && m.target != 0 && target_pid == m.owner && foreground_belongs_to(m.owner, GetForegroundWindow())) {
        return;
    }
    let x = (lparam.0 as u16) as i16 as i32;
    let selected = if uses_grid(m) {
        let mut rc = RECT::default();
        let _ = GetClientRect(hwnd, &mut rc);
        let hdc = GetDC(Some(hwnd));
        let row = grid_row(m, rc.right - rc.left, &t, hdc);
        ReleaseDC(Some(hwnd), hdc);
        candidate_cells(m, rc.right - rc.left, t.pad, row, t.small_row)
            .iter()
            .find(|cell| cell.contains(x, y))
            .map(|cell| cell.id)
    } else {
        hit_candidate(m, y, t.pad, t.row, t.small_row)
    };
    if let Some(id) = selected {
        let _ = PostMessageW(
            Some(target),
            WM_SELECT_CANDIDATE,
            WPARAM(m.revision as usize),
            LPARAM(id as isize),
        );
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn webview2_host_counts_as_foreground_through_its_focus_window() {
        // 新版 Teams：前景視窗屬於 ms-teams.exe（100），焦點與 TSF 在 msedgewebview2.exe（200）。
        assert!(in_front(200, 100, || Some(200)));
        // 一般程式：前景視窗就是送出者，不必再查焦點。
        assert!(in_front(100, 100, || unreachable!()));
        // 背景程式晚到的訊息：前景與焦點都不是它，不顯示。
        assert!(!in_front(300, 100, || Some(100)));
        assert!(!in_front(300, 100, || None));
        assert!(!foreground_belongs_to(std::process::id(), HWND::default()));
    }

    #[test]
    fn only_exclusive_fullscreen_hides_the_window() {
        use windows::Win32::UI::Shell::{
            QUNS_ACCEPTS_NOTIFICATIONS, QUNS_APP, QUNS_BUSY, QUNS_NOT_PRESENT, QUNS_PRESENTATION_MODE, QUNS_QUIET_TIME,
        };
        // #159：獨佔全螢幕的遊戲被疊上視窗就跳回桌面；無邊框（BUSY）、簡報與一般桌面照常顯示。
        assert!(yields_to(QUNS_RUNNING_D3D_FULL_SCREEN));
        for state in [
            QUNS_NOT_PRESENT,
            QUNS_BUSY,
            QUNS_PRESENTATION_MODE,
            QUNS_ACCEPTS_NOTIFICATIONS,
            QUNS_QUIET_TIME,
            QUNS_APP,
        ] {
            assert!(!yields_to(state), "{state:?}");
        }
    }

    #[test]
    fn vertical_replacement_title_precedes_candidates() {
        let m = UiMessage {
            title: "修改「機場」 · Tab 切換詞長".into(),
            candidates: vec![UiCandidate {
                id: 42,
                text: "機場".into(),
                ..Default::default()
            }],
            ..Default::default()
        };
        let rows = lines(&m);
        assert_eq!(rows.len(), 2);
        assert_eq!(String::from_utf16(&rows[0].0).unwrap(), m.title);
        assert_eq!(rows[0].1, Row::Small);
        assert_eq!(String::from_utf16(&rows[1].0).unwrap(), "1. 機場");
        assert_eq!(rows[1].1, Row::Candidate { highlighted: true });
    }

    #[test]
    fn notice_is_the_first_row_and_expires_on_its_own() {
        let m = UiMessage {
            show: true,
            notice: "已從詞庫刪除：世傑（可在設定程式復原）".into(),
            suggestion: "補全".into(),
            title: "修改「世傑」 · Tab 切換詞長".into(),
            candidates: vec![UiCandidate {
                id: 1,
                text: "世界".into(),
                ..Default::default()
            }],
            ..Default::default()
        };
        // 提示優先：提示一列（一般字級），標題這次不畫，補全提示照常。
        let rows = lines(&m);
        assert_eq!(rows.len(), 3);
        assert_eq!(String::from_utf16(&rows[0].0).unwrap(), m.notice);
        assert_eq!(rows[0].1, Row::Notice);
        assert_eq!(String::from_utf16(&rows[1].0).unwrap(), "⇥ 補全");
        assert_eq!(rows[1].1, Row::Suggestion, "補全提示跟候選同字級（#118）");
        assert_eq!(rows[2].1, Row::Candidate { highlighted: true });
        // 收起提示後標題回來。
        let rows = lines(&without_notice(m.clone()));
        assert_eq!(String::from_utf16(&rows[1].0).unwrap(), m.title);
        // 到時收起：還有候選就照常顯示，只有提示就隱藏。
        let after = without_notice(m.clone());
        assert!(after.show && after.notice.is_empty() && after.candidates.len() == 1);
        let alone = without_notice(UiMessage {
            show: true,
            notice: "已加入詞庫：世傑".into(),
            ..Default::default()
        });
        assert!(!alone.show);
    }

    #[test]
    fn reading_hint_is_a_single_regular_row() {
        // #162：中英混打打 20 時只有一列讀音 ㄉㄢ（一般字級、一般文字色），不是補全那種小字。
        let m = UiMessage {
            show: true,
            reading: "ㄉㄢ".into(),
            ..Default::default()
        };
        let rows = lines(&m);
        assert_eq!(rows.len(), 1);
        assert_eq!(String::from_utf16(&rows[0].0).unwrap(), "ㄉㄢ");
        assert_eq!(rows[0].1, Row::Reading);
        assert!(without_notice(m).show, "提示收起不影響讀音提示");
    }

    #[test]
    fn near_reading_note_follows_the_candidate() {
        let plain = UiCandidate {
            id: 0,
            text: "吃".into(),
            ..Default::default()
        };
        let near = UiCandidate {
            id: 1,
            text: "痴".into(),
            note: "近音 ㄔ".into(),
            ..Default::default()
        };
        assert_eq!(candidate_line(0, &plain), "1. 吃");
        // 舊 DLL 沒送鍵標時第 10 項畫 0（按 0 選），不是「10」（#112）。
        assert_eq!(candidate_line(9, &near), "0. 痴　〔近音 ㄔ〕");
    }

    #[test]
    fn candidate_line_uses_the_engine_key() {
        let keyed = UiCandidate { id: 4, text: "式".into(), key: Some("q".into()), ..Default::default() };
        assert_eq!(candidate_line(4, &keyed), "q. 式");
        let semicolon = UiCandidate { id: 9, text: "示".into(), key: Some(";".into()), ..Default::default() };
        assert_eq!(candidate_line(9, &semicolon), ";. 示");
        let completion = UiCandidate { id: 0, text: "hello".into(), key: Some(String::new()), ..Default::default() };
        assert_eq!(candidate_line(0, &completion), "hello", "英文補字不畫鍵標");
    }
}

