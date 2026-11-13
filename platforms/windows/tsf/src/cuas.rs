//! 系統相容層（CUAS）的操作，收在可替換的介面後面（#66）。
//!
//! IMM32 程式（系統 EDIT、IMM32 模式的 RichEdit、自繪文字框）經 CUAS 接 TSF：外殼要請 CUAS 完成組字
//! （ImmNotifyIME CPS_COMPLETE）、偷看程式的訊息佇列有沒有結果字串的 WM_CHAR、把快捷鍵重送回程式、
//! 判斷這一鍵是不是系統的輸入法熱鍵。這些都碰真正的視窗，測試用 `imm32_order_tests` 裡的 FIFO 假宿主取代。
//!
//! 規則：`complete` 會同步回呼外殼（OnCompositionTerminated、OnEndEdit），所以呼叫任何 `Cuas` 方法前先
//! clone 出 `Rc<dyn Cuas>`，呼叫期間不持有 `state` 的任何借用。

use windows::Win32::Foundation::{HWND, LPARAM, WPARAM};
use windows::Win32::UI::Input::Ime::{
    ImmGetContext, ImmGetHotKey, ImmNotifyIME, ImmReleaseContext, CPS_COMPLETE, NI_COMPOSITIONSTR,
};
use windows::Win32::UI::Input::KeyboardAndMouse::{GetFocus, GetKeyState, HKL, VK_CONTROL, VK_MENU, VK_SHIFT, VK_SPACE};
use windows::Win32::UI::WindowsAndMessaging::{
    PeekMessageW, PostMessageW, MSG, PM_NOREMOVE, PM_QS_POSTMESSAGE, WM_CHAR, WM_KEYDOWN,
};

use crate::keys;

/// `Cuas::complete` 的結果。
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Complete {
    /// 這個視窗沒有輸入法 context：不是 IMM32 程式，什麼都沒發生。
    NoContext,
    /// 有 context，但 ImmNotifyIME 回報失敗。
    Failed,
    /// 相容層已受理：它可能已同步回呼外殼，結果字串之後以 WM_CHAR 送給程式。
    Notified,
}

pub(crate) trait Cuas {
    /// GetFocus：目前執行緒有鍵盤焦點的視窗。
    fn focus(&self) -> HWND;
    /// ImmGetContext(hwnd) + ImmNotifyIME(NI_COMPOSITIONSTR, CPS_COMPLETE)，可能同步回呼外殼。
    fn complete(&self, hwnd: HWND) -> Complete;
    /// 程式的訊息佇列裡有沒有給 `hwnd` 的 WM_CHAR。帶 PM_QS_POSTMESSAGE：只看 post 的訊息，
    /// 不分派別的執行緒 SendMessage 過來、還在等的訊息（VM 上驗證過，沒帶時會分派）。
    fn has_char(&self, hwnd: HWND) -> bool;
    /// PostMessage(hwnd, WM_KEYDOWN, vk, lparam)：把按鍵排在佇列最後。
    fn repost(&self, hwnd: HWND, vk: u32, lparam: isize) -> bool;
    /// 這一鍵是不是系統的輸入法熱鍵：Ctrl+Space，或 ImmGetHotKey 任一登記以 `keys::hotkey_matches` 符合。
    fn ime_hotkey(&self, vk: u32) -> bool;
}

pub(crate) struct Win32Cuas;

/// SDK 的輸入法熱鍵 ID 全集（https://learn.microsoft.com/en-us/windows/win32/intl/ime-hot-key-identifiers）：
/// IME_CHOTKEY_*、IME_JHOTKEY_CLOSE_OPEN、IME_KHOTKEY_*、IME_THOTKEY_*、IME_HOTKEY_DSWITCH_*、IME_HOTKEY_PRIVATE_*。
pub(crate) fn hotkey_ids() -> impl Iterator<Item = u32> {
    (0x10..=0x12u32)
        .chain(std::iter::once(0x30))
        .chain(0x50..=0x52)
        .chain(0x70..=0x72)
        .chain(0x100..=0x11F)
        .chain(0x200..=0x21F)
}

/// ImmGetHotKey 的登記：(修飾鍵位元, vk)；沒有登記回 None。
pub(crate) fn registered_hotkey(id: u32) -> Option<(u32, u32)> {
    let (mut mods, mut key, mut hkl) = (0u32, 0u32, HKL::default());
    unsafe { ImmGetHotKey(id, &mut mods, &mut key, &mut hkl) }.as_bool().then_some((mods, key))
}

impl Cuas for Win32Cuas {
    fn focus(&self) -> HWND {
        unsafe { GetFocus() }
    }

    fn complete(&self, hwnd: HWND) -> Complete {
        unsafe {
            let himc = ImmGetContext(hwnd);
            if himc.is_invalid() {
                return Complete::NoContext;
            }
            let ok = ImmNotifyIME(himc, NI_COMPOSITIONSTR, CPS_COMPLETE, 0).as_bool();
            let _ = ImmReleaseContext(hwnd, himc);
            if ok { Complete::Notified } else { Complete::Failed }
        }
    }

    fn has_char(&self, hwnd: HWND) -> bool {
        let mut msg = MSG::default();
        unsafe { PeekMessageW(&mut msg, Some(hwnd), WM_CHAR, WM_CHAR, PM_NOREMOVE | PM_QS_POSTMESSAGE) }.as_bool()
            && msg.hwnd == hwnd
    }

    fn repost(&self, hwnd: HWND, vk: u32, lparam: isize) -> bool {
        unsafe { PostMessageW(Some(hwnd), WM_KEYDOWN, WPARAM(vk as usize), LPARAM(lparam)) }.is_ok()
    }

    fn ime_hotkey(&self, vk: u32) -> bool {
        let down = |v: u16| unsafe { GetKeyState(v as i32) } < 0;
        let (ctrl, alt, shift) = (down(VK_CONTROL.0), down(VK_MENU.0), down(VK_SHIFT.0));
        if vk == VK_SPACE.0 as u32 && ctrl && !alt {
            return true; // 繁中輸入法的開關
        }
        hotkey_ids().any(|id| {
            registered_hotkey(id).is_some_and(|(mods, key)| keys::hotkey_matches(mods, key, vk, ctrl, alt, shift))
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use windows::Win32::UI::Input::KeyboardAndMouse::{GetKeyboardState, SetKeyboardState, VK_LCONTROL, VK_LMENU, VK_LSHIFT};

    fn set_mods(state: &mut [u8; 256], ctrl: bool, alt: bool, shift: bool) {
        for (v, on) in [
            (VK_CONTROL.0, ctrl),
            (VK_LCONTROL.0, ctrl),
            (VK_MENU.0, alt),
            (VK_LMENU.0, alt),
            (VK_SHIFT.0, shift),
            (VK_LSHIFT.0, shift),
        ] {
            state[v as usize] = if on { 0x80 } else { 0 };
        }
    }

    #[test]
    fn win32_cuas_recognizes_registered_hotkeys() {
        // #66：ime_hotkey 對系統實際登記的熱鍵回答是（登記帶 MOD_ON_KEYUP 的按下時不算），對一般鍵回答否。
        // 期望值來自系統自己的 ImmGetHotKey 回傳值，不是寫死的表。
        const MOD_ALT: u32 = 0x1;
        const MOD_CONTROL: u32 = 0x2;
        const MOD_SHIFT: u32 = 0x4;
        const MOD_ON_KEYUP: u32 = 0x800;
        const MOD_IGNORE_ALL_MODIFIER: u32 = 0x400;
        let cuas = Win32Cuas;
        let mut saved = [0u8; 256];
        unsafe { GetKeyboardState(&mut saved).unwrap() };
        // 測試自己列舉 SDK 的 ID，不借用實作的清單（實作少列時才抓得到）。
        let ids = (0x10..=0x12u32)
            .chain([0x30])
            .chain(0x50..=0x52)
            .chain(0x70..=0x72)
            .chain(0x100..=0x11F)
            .chain(0x200..=0x21F);
        let registered: Vec<(u32, u32, u32)> =
            ids.filter_map(|id| registered_hotkey(id).map(|(m, k)| (id, m, k))).collect();
        let mut state = saved;
        for &(id, mods, vk) in &registered {
            let (ctrl, alt, shift) = (mods & MOD_CONTROL != 0, mods & MOD_ALT != 0, mods & MOD_SHIFT != 0);
            set_mods(&mut state, ctrl, alt, shift);
            unsafe { SetKeyboardState(&state).unwrap() };
            // 同一個 vk、修飾鍵也相同的另一筆登記（不帶 MOD_ON_KEYUP）會讓答案變成是。
            let matched = registered.iter().any(|&(_, m, k)| {
                m & MOD_ON_KEYUP == 0
                    && k == vk
                    && (m & MOD_IGNORE_ALL_MODIFIER != 0
                        || (m & MOD_CONTROL != 0, m & MOD_ALT != 0, m & MOD_SHIFT != 0) == (ctrl, alt, shift))
            }) || (vk == VK_SPACE.0 as u32 && ctrl && !alt);
            assert_eq!(cuas.ime_hotkey(vk), matched, "熱鍵 id={id:#x} mods={mods:#x} vk={vk:#x}");
        }
        set_mods(&mut state, true, false, false);
        unsafe { SetKeyboardState(&state).unwrap() };
        let ctrl_space = cuas.ime_hotkey(VK_SPACE.0 as u32);
        set_mods(&mut state, false, false, false);
        unsafe { SetKeyboardState(&state).unwrap() };
        let plain_s = cuas.ime_hotkey('S' as u32);
        unsafe { SetKeyboardState(&saved).unwrap() };
        assert!(ctrl_space, "Ctrl+Space 是輸入法開關");
        assert!(!plain_s, "沒有修飾鍵的 S 不是熱鍵");
        if std::env::var("ZUIN_TEST_IME_HOTKEYS").as_deref() == Ok("1") {
            assert!(!registered.is_empty(), "繁中 VM 至少登記一筆輸入法熱鍵（Ctrl+Shift+0 的 0x104）");
        }
    }
}
