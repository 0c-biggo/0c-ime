//! 已確認前文：只核對游標前是否仍緊接著自己剛提交的文字。讀到的文字只拿來比對，
//! 不送給引擎、不記錄；有選取範圍、讀不到或不相符都視為未確認（對應 Fcitx 外殼的 DocumentContext）。
//!
//! 另外在開始組字前讀游標前一個字是不是阿拉伯數字（#337，`digit_before`）：只把「是／不是」交給引擎，
//! 不送、不記那個字。英文模式打的數字不經過引擎，這是 Windows 唯一能知道「3 隻」前面有 3 的地方。
//! 從哪裡讀看文件是哪一種：
//! - TSF 文字儲存（記事本的 RichEdit、WPF、Word）與 Chromium：context 的文件就是程式的文字，以唯讀 edit session 讀。
//! - IMM32 程式經系統相容層（CUAS；系統 EDIT、IMM32 模式的 RichEdit、Scintilla……）：context 的文件只有組字，
//!   沒在組字時是空的，游標前永遠讀不到字，看起來就像文件開頭。這種 context 不看 TSF 的文件，改問焦點視窗：
//!   系統 EDIT（含 WinForms 包過的 EDIT）用它自己的訊息（EM_GETSEL、EM_GETLINE），其他視窗用 IMM32 的
//!   IMR_DOCUMENTFEED（程式回游標所在的段落與游標位置；Mozc 在這類程式也這樣讀前後文）。都是送給本執行緒視窗的
//!   同步訊息，不等別的執行緒。
//!   程式不支援就是讀不到，不送旗標。
use std::cell::Cell;

use windows::Win32::Foundation::{HWND, LPARAM, WPARAM};
use windows::Win32::UI::Controls::{
    EM_GETLINE, EM_GETPASSWORDCHAR, EM_GETSEL, EM_LINEFROMCHAR, EM_LINEINDEX, EM_LINELENGTH,
};
use windows::Win32::UI::Input::Ime::{IMR_DOCUMENTFEED, RECONVERTSTRING};
use windows::Win32::UI::TextServices::{
    ITfContext, ITfEditSession, ITfEditSession_Impl, TF_DEFAULT_SELECTION, TF_ES_READ, TF_ES_SYNC,
    TF_SELECTION,
};
use windows::Win32::UI::WindowsAndMessaging::{
    GetWindowLongW, IsWindowUnicode, RealGetWindowClassW, SendMessageW, ES_PASSWORD, GWL_STYLE, WM_IME_REQUEST,
};
use windows_core::{implement, ComObject};

use crate::log::zdebug;
use crate::privacy;

/// 游標（無選取）前的 `expected.len()` 個 UTF-16 單位是否正好等於 `expected`。
pub fn preceded_by(ctx: &ITfContext, client: u32, expected: &[u16]) -> bool {
    if expected.is_empty() || expected.len() > 256 {
        return false;
    }
    crate::object_created();
    let probe = ComObject::new(Probe {
        ctx: ctx.clone(),
        expected: expected.to_vec(),
        matched: Cell::new(false),
        reason: Cell::new("not_run"),
    });
    let edit: ITfEditSession = probe.to_interface();
    let matched = match unsafe { ctx.RequestEditSession(client, &edit, TF_ES_SYNC | TF_ES_READ) } {
        Ok(hr) if hr.is_ok() => probe.matched.get(),
        Ok(_) | Err(_) => {
            probe.reason.set("edit_session_refused");
            false
        }
    };
    // 只記原因代碼，不記文字。
    zdebug!("document context probe={}", probe.reason.get());
    matched
}

#[implement(ITfEditSession)]
struct Probe {
    ctx: ITfContext,
    expected: Vec<u16>,
    matched: Cell<bool>,
    reason: Cell<&'static str>,
}
impl Drop for Probe {
    fn drop(&mut self) {
        crate::object_destroyed();
    }
}
impl ITfEditSession_Impl for Probe_Impl {
    fn DoEditSession(&self, ec: u32) -> windows_core::Result<()> {
        crate::guarded("DocumentContext", || unsafe {
            let mut selected = [TF_SELECTION::default()];
            let mut fetched = 0;
            self.ctx
                .GetSelection(ec, TF_DEFAULT_SELECTION, &mut selected, &mut fetched)?;
            let range = std::mem::ManuallyDrop::take(&mut selected[0].range);
            let Some(range) = range else {
                self.reason.set("no_selection");
                return Ok(());
            };
            if fetched != 1 || !range.IsEmpty(ec)?.as_bool() {
                self.reason.set("selection_not_caret");
                return Ok(());
            }
            let want = self.expected.len() as i32;
            let mut moved = 0i32;
            range.ShiftStart(ec, -want, &mut moved, std::ptr::null())?;
            if moved != -want {
                self.reason.set("short_shift");
                return Ok(());
            }
            let mut text = vec![0u16; self.expected.len()];
            let mut got = 0u32;
            range.GetText(ec, 0, &mut text, &mut got)?;
            if got as usize != self.expected.len() {
                self.reason.set("short_text");
            } else if text != self.expected {
                self.reason.set("mismatch");
            } else {
                self.reason.set("matched");
                self.matched.set(true);
            }
            Ok(())
        })
    }
}

/// 游標前一個 UTF-16 單位是不是阿拉伯數字（半形 0–9 或全形 ０–９）。
pub fn is_digit_unit(u: u16) -> bool {
    (0x30..=0x39).contains(&u) || (0xFF10..=0xFF19).contains(&u)
}

/// 游標（無選取）前一個字是不是阿拉伯數字（#337）。文件、段落或行的開頭是 `Some(false)`；有選取範圍、讀不到時 `None`
/// （引擎照它自己記的）。讀到的字只拿來判斷，不送出、不記錄。`activate_flags` 是本執行緒啟用 TIP 時的旗標，
/// 跟 context 的靜態旗標一起分辨 IMM32 程式（[`privacy::imm32_context`]）。
pub fn digit_before(ctx: &ITfContext, client: u32, activate_flags: u32) -> Option<bool> {
    let (result, reason) = if privacy::imm32_context(ctx, activate_flags) {
        // 系統相容層的 context 只有組字：ShiftStart 一定動不了，照 TSF 讀會把每個位置都當成文件開頭、送出 false。
        match privacy::focused_window(ctx) {
            Some(focus) => window_digit_before(focus),
            None => (None, "imm32_no_focus"),
        }
    } else {
        document_digit_before(ctx, client)
    };
    zdebug!("digit before probe={}", reason); // 只記原因代碼，不記結果與文字
    result
}

/// TSF 文件：唯讀 edit session 讀游標前一個 UTF-16 單位。
fn document_digit_before(ctx: &ITfContext, client: u32) -> (Option<bool>, &'static str) {
    crate::object_created();
    let probe = ComObject::new(DigitProbe { ctx: ctx.clone(), result: Cell::new(None), reason: Cell::new("not_run") });
    let edit: ITfEditSession = probe.to_interface();
    match unsafe { ctx.RequestEditSession(client, &edit, TF_ES_SYNC | TF_ES_READ) } {
        Ok(hr) if hr.is_ok() => {}
        Ok(_) | Err(_) => probe.reason.set("edit_session_refused"),
    }
    (probe.result.get(), probe.reason.get())
}

/// IMM32 程式的焦點視窗（本執行緒的視窗）：系統 EDIT 問控制項本身，其他視窗問 IMR_DOCUMENTFEED。
/// ANSI 視窗不讀：EDIT 的位置可能以位元組計、RECONVERTSTRING 的字串可能是 ANSI，分不出來就當讀不到。
pub(crate) fn window_digit_before(hwnd: HWND) -> (Option<bool>, &'static str) {
    unsafe {
        if !IsWindowUnicode(hwnd).as_bool() {
            return (None, "imm32_ansi_window");
        }
        if is_system_edit(hwnd) {
            edit_digit_before(hwnd)
        } else {
            feed_request(hwnd)
        }
    }
}

/// 視窗實際上是不是系統 EDIT 控制項；EM_* 的訊息編號只對 EDIT 有這個意義，其他視窗不送。
/// RealGetWindowClass 對 user32 的 EDIT 回「Edit」，改了類別名稱的衍生控制項（沒有視覺樣式的 WinForms TextBox）也一樣。
/// 基底是 comctl32 第 6 版 EDIT 的衍生控制項，它回的是衍生類別自己的名稱：有 `Application.EnableVisualStyles()` 的
/// WinForms 程式（Visual Studio 範本的預設）裡 TextBox、MaskedTextBox 是 `WindowsForms10.EDIT.app.0.…`
/// （.14 Windows 11 25H2 實測，64、32 位元相同），照名稱認，見 [`edit_class_name`]。
unsafe fn is_system_edit(hwnd: HWND) -> bool {
    let mut name = [0u16; 256]; // 類別名稱最長 256 個字，WinForms 的約 40 個字
    let len = RealGetWindowClassW(hwnd, &mut name) as usize;
    edit_class_name(&String::from_utf16_lossy(&name[..len.min(name.len())]))
}

/// 類別名稱是不是 EDIT：「Edit」，或 WinForms 包過的 EDIT（`WindowsForms10.EDIT.app.…`：第一段以 WindowsForms 開頭、
/// 第二段正好是 EDIT、後面還有別段），不分大小寫。只認這兩種，不看名稱裡有沒有 edit 字樣：RichEdit
/// （`RichEdit20W`、`WindowsForms10.RichEdit20W.app.…`）等控制項的 EM_GETLINE 之類意義不同，照舊問 IMR_DOCUMENTFEED。
fn edit_class_name(name: &str) -> bool {
    if name.eq_ignore_ascii_case("Edit") {
        return true;
    }
    let mut parts = name.split('.');
    let (Some(framework), Some(base), Some(_)) = (parts.next(), parts.next(), parts.next()) else {
        return false;
    };
    framework.get(..12).is_some_and(|prefix| prefix.eq_ignore_ascii_case("WindowsForms"))
        && base.eq_ignore_ascii_case("EDIT")
}

/// 系統 EDIT：EM_GETSEL 取游標位置，EM_GETLINE 取游標所在那一行到游標為止的字，看最後一個。
/// EDIT 不回應 IMR_DOCUMENTFEED（.14 Windows 11 25H2 實測回 0）；IMR_RECONVERTSTRING 會複製整份文字，不用。
unsafe fn edit_digit_before(hwnd: HWND) -> (Option<bool>, &'static str) {
    let send = |msg: u32, w: usize, l: isize| SendMessageW(hwnd, msg, Some(WPARAM(w)), Some(LPARAM(l))).0;
    // 密碼欄位在欄位政策就擋下、不會走到這裡；這裡再確認一次，不讀密碼框的文字：有 ES_PASSWORD 樣式，或 EDIT 說它有
    // 遮罩字元（EM_GETPASSWORDCHAR）。WinForms 的 UseSystemPasswordChar 與 PasswordChar 兩種 .14 實測都帶 ES_PASSWORD。
    if GetWindowLongW(hwnd, GWL_STYLE) & ES_PASSWORD != 0 || send(EM_GETPASSWORDCHAR, 0, 0) != 0 {
        return (None, "edit_password");
    }
    let (mut start, mut end) = (0u32, 0u32);
    send(EM_GETSEL, &mut start as *mut u32 as usize, &mut end as *mut u32 as isize);
    if start != end {
        return (None, "edit_selection");
    }
    if start == 0 {
        return (Some(false), "edit_start");
    }
    let caret = start as isize;
    let mut line = send(EM_LINEFROMCHAR, start as usize, 0);
    let mut first = send(EM_LINEINDEX, line as usize, 0);
    if line < 0 || first < 0 || first > caret {
        return (None, "edit_line");
    }
    if first == caret {
        // 游標在顯示行的開頭：前面隔著換行字元（硬換行）就不是數字；自動換行時前一個字是上一行的最後一個字。
        if line == 0 {
            return (None, "edit_line");
        }
        let previous = send(EM_LINEINDEX, line as usize - 1, 0);
        if previous < 0 || previous >= caret {
            return (None, "edit_line");
        }
        if previous + send(EM_LINELENGTH, previous as usize, 0) != caret {
            return (Some(false), "edit_line_start");
        }
        line -= 1;
        first = previous;
    }
    // EM_GETLINE 的緩衝區第一個 WORD 是容量（最多 65535 個字），從行首複製到游標為止。
    let column = (caret - first) as usize;
    if column > u16::MAX as usize {
        return (None, "edit_long_line");
    }
    let mut text = vec![0u16; column];
    text[0] = column as u16;
    let copied = send(EM_GETLINE, line as usize, text.as_mut_ptr() as isize);
    if copied < column as isize {
        return (None, "edit_short_line");
    }
    (Some(is_digit_unit(text[column - 1])), "edit_read")
}

/// IMR_DOCUMENTFEED 的回覆上限：段落加前後文通常幾百個字；超過就不讀，不為了一個字配置大緩衝區。
const FEED_LIMIT: usize = 1 << 20;

/// IMM32 的 IMR_DOCUMENTFEED：先問需要多大的緩衝區，再請程式填 RECONVERTSTRING（游標所在的段落與游標位置）。
/// 不支援的程式交給 DefWindowProc，回 0。
unsafe fn feed_request(hwnd: HWND) -> (Option<bool>, &'static str) {
    let ask = |buffer: isize| {
        SendMessageW(hwnd, WM_IME_REQUEST, Some(WPARAM(IMR_DOCUMENTFEED as usize)), Some(LPARAM(buffer))).0
    };
    let need = ask(0);
    if need <= 0 {
        return (None, "feed_unsupported");
    }
    let need = need as usize;
    if need < std::mem::size_of::<RECONVERTSTRING>() || need > FEED_LIMIT {
        return (None, "feed_size");
    }
    // 以 u32 配置：RECONVERTSTRING 的欄位要對齊。dwSize 是緩衝區大小，dwVersion 是 0。
    let mut buffer = vec![0u32; need.div_ceil(4)];
    buffer[0] = need as u32;
    if ask(buffer.as_mut_ptr() as isize) <= 0 {
        return (None, "feed_refused");
    }
    feed_digit_before(std::slice::from_raw_parts(buffer.as_ptr() as *const u8, need))
}

/// 從程式填好的 RECONVERTSTRING 判斷游標前一個字。沒在組字、沒有選取時組字範圍與目標範圍都是空的，位置就是游標；
/// 欄位不合理（偏移超出緩衝區、不是偶數、兩個範圍不一致）一律當讀不到。偏移以位元組計、長度以字計。
fn feed_digit_before(feed: &[u8]) -> (Option<bool>, &'static str) {
    let field = |index: usize| {
        feed.get(index * 4..index * 4 + 4).map(|b| u32::from_le_bytes([b[0], b[1], b[2], b[3]]) as usize)
    };
    // 依序是 dwVersion、dwStrLen、dwStrOffset、dwCompStrLen、dwCompStrOffset、dwTargetStrLen、dwTargetStrOffset。
    let (Some(version), Some(len), Some(offset), Some(comp_len), Some(comp_offset), Some(target_len), Some(target_offset)) =
        (field(1), field(2), field(3), field(4), field(5), field(6), field(7))
    else {
        return (None, "feed_malformed");
    };
    let fits = offset >= std::mem::size_of::<RECONVERTSTRING>()
        && offset <= feed.len()
        && len <= (feed.len() - offset) / 2
        && comp_offset % 2 == 0
        && comp_offset / 2 <= len;
    if version != 0 || !fits {
        return (None, "feed_malformed");
    }
    if comp_len != 0 || target_len != 0 || target_offset != comp_offset {
        return (None, "feed_selection");
    }
    let caret = comp_offset / 2;
    if caret == 0 {
        return (Some(false), "feed_start");
    }
    let at = offset + (caret - 1) * 2;
    (Some(is_digit_unit(u16::from_le_bytes([feed[at], feed[at + 1]]))), "feed_read")
}

#[implement(ITfEditSession)]
struct DigitProbe {
    ctx: ITfContext,
    result: Cell<Option<bool>>,
    reason: Cell<&'static str>,
}
impl Drop for DigitProbe {
    fn drop(&mut self) {
        crate::object_destroyed();
    }
}
impl ITfEditSession_Impl for DigitProbe_Impl {
    fn DoEditSession(&self, ec: u32) -> windows_core::Result<()> {
        crate::guarded("DigitBefore", || unsafe {
            let mut selected = [TF_SELECTION::default()];
            let mut fetched = 0;
            self.ctx.GetSelection(ec, TF_DEFAULT_SELECTION, &mut selected, &mut fetched)?;
            let range = std::mem::ManuallyDrop::take(&mut selected[0].range);
            let Some(range) = range else {
                self.reason.set("no_selection");
                return Ok(());
            };
            if fetched != 1 || !range.IsEmpty(ec)?.as_bool() {
                self.reason.set("selection_not_caret");
                return Ok(());
            }
            let mut moved = 0i32;
            range.ShiftStart(ec, -1, &mut moved, std::ptr::null())?;
            if moved == 0 {
                self.reason.set("document_start");
                self.result.set(Some(false));
                return Ok(());
            }
            let mut text = [0u16; 1];
            let mut got = 0u32;
            range.GetText(ec, 0, &mut text, &mut got)?;
            if got != 1 {
                self.reason.set("short_text");
                return Ok(());
            }
            self.reason.set("read");
            self.result.set(Some(is_digit_unit(text[0])));
            Ok(())
        })
    }
}

#[cfg(test)]
#[path = "context_host_tests.rs"]
mod host_tests;

#[cfg(test)]
mod tests {
    use super::{edit_class_name, feed_digit_before, is_digit_unit};

    #[test]
    fn digit_units() {
        for c in "0123456789０１２３４５６７８９".encode_utf16() {
            assert!(is_digit_unit(c));
        }
        for c in "a一，. ½٣".encode_utf16() {
            assert!(!is_digit_unit(c), "{c:#x}");
        }
    }

    /// #337：系統 EDIT 與 WinForms 包過的 EDIT 才用 EM_* 讀；名稱裡有 edit 字樣的其他控制項（RichEdit……）不算。
    /// WinForms 的名稱是 .14 實測的（64、32 位元的 TextBox 與 RichTextBox、ComboBox）。
    #[test]
    fn edit_class_names() {
        for name in [
            "Edit",
            "EDIT",
            "edit",
            "WindowsForms10.EDIT.app.0.38b2340_r8_ad1",
            "WindowsForms10.EDIT.app.0.450863_r9_ad1",
            "WindowsForms10.EDIT.app.0.test",
            "windowsforms10.edit.APP.0.x",
        ] {
            assert!(edit_class_name(name), "{name}");
        }
        for name in [
            "",
            "Edit2",
            "TEdit",
            "MyEdit",
            "Edit.EDIT.app",
            "RichEdit20W",
            "RICHEDIT50W",
            "RichEditD2DPT",
            "WindowsForms10.RichEdit20W.app.0.38b2340_r8_ad1",
            "WindowsForms10.RICHEDIT50W.app.0.38b2340_r8_ad1",
            "WindowsForms10.COMBOBOX.app.0.38b2340_r8_ad1",
            "WindowsForms10.Window.8.app.0.38b2340_r8_ad1",
            "WindowsForms10.EDITOR.app.0.x",
            "WindowsForms10.EDIT",
            "WindowsForms10.app.EDIT.0",
            "Forms.EDIT.app.0.x",
            "視窗.EDIT.app",
            "Scintilla",
            "Chrome_WidgetWin_1",
        ] {
            assert!(!edit_class_name(name), "{name}");
        }
    }

    /// 程式填好的 RECONVERTSTRING：`text` 是段落，組字範圍與目標範圍從第 `caret` 個字開始、各長 `selected` 個字。
    fn feed(text: &str, caret: usize, selected: usize) -> Vec<u8> {
        let units: Vec<u16> = text.encode_utf16().collect();
        let mut out = Vec::new();
        for value in [32 + units.len() * 2 + 2, 0, units.len(), 32, selected, caret * 2, selected, caret * 2] {
            out.extend_from_slice(&(value as u32).to_le_bytes());
        }
        for unit in units.iter().chain(std::iter::once(&0)) {
            out.extend_from_slice(&unit.to_le_bytes());
        }
        out
    }

    fn set(feed: &mut [u8], field: usize, value: u32) {
        feed[field * 4..field * 4 + 4].copy_from_slice(&value.to_le_bytes());
    }

    /// #337：IMR_DOCUMENTFEED 的回覆只看游標前一個字；段落開頭不是數字，有選取範圍不判斷。
    #[test]
    fn document_feed_reads_the_unit_before_the_caret() {
        assert_eq!(feed_digit_before(&feed("買了3", 3, 0)), (Some(true), "feed_read"));
        assert_eq!(feed_digit_before(&feed("買了３", 3, 0)), (Some(true), "feed_read"));
        assert_eq!(feed_digit_before(&feed("買了3箱", 4, 0)), (Some(false), "feed_read"));
        assert_eq!(feed_digit_before(&feed("買了3箱", 2, 0)), (Some(false), "feed_read"));
        assert_eq!(feed_digit_before(&feed("3箱", 0, 0)), (Some(false), "feed_start"));
        assert_eq!(feed_digit_before(&feed("", 0, 0)), (Some(false), "feed_start"));
        assert_eq!(feed_digit_before(&feed("買了3箱", 3, 1)), (None, "feed_selection"));
    }

    /// 程式回的欄位不合理時當讀不到，不讀緩衝區以外的位置。
    #[test]
    fn document_feed_rejects_malformed_replies() {
        let good = feed("買了3", 3, 0);
        assert_eq!(feed_digit_before(&good[..28]), (None, "feed_malformed"), "比表頭短");
        assert_eq!(feed_digit_before(&[]), (None, "feed_malformed"));
        // (欄位, 值)：版本不是 0、字數超出緩衝區、字串偏移在表頭裡或超出緩衝區、游標偏移是奇數或超出字串。
        for (field, value) in [(1, 1), (2, 5), (2, u32::MAX), (3, 8), (3, 4096), (5, 5), (5, 8), (5, u32::MAX - 1)] {
            let mut bad = good.clone();
            set(&mut bad, field, value);
            assert_eq!(feed_digit_before(&bad), (None, "feed_malformed"), "欄位 {field} = {value}");
        }
        // 兩個範圍不一致（目標範圍在別處）：不知道游標在哪裡。
        let mut split = good.clone();
        set(&mut split, 7, 2);
        assert_eq!(feed_digit_before(&split), (None, "feed_selection"));
    }
}
