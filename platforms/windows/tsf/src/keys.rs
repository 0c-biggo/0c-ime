//! 把 Windows 的按鍵事件轉成協定的 Key（與 Fcitx 5 外殼的 toKey 對應）。
//! 排列由核心解讀：只把可見的 ASCII 交給核心；具名鍵用協定名稱；帶 Ctrl／Alt 的組合鍵交還應用程式，
//! 只有中文模式的 Ctrl＋標點（#168，見 [`ctrl_punct`]）例外。
use windows::Win32::UI::Input::KeyboardAndMouse::{
    GetAsyncKeyState, GetKeyState, GetKeyboardState, ToUnicode, VK_BACK, VK_CAPITAL, VK_CONTROL,
    VK_DELETE, VK_DOWN, VK_END, VK_ESCAPE, VK_F11, VK_HOME, VK_LCONTROL, VK_LEFT, VK_LMENU, VK_LSHIFT,
    VK_LWIN, VK_MENU, VK_NEXT, VK_PRIOR, VK_RCONTROL, VK_RETURN, VK_RIGHT, VK_RMENU, VK_RSHIFT,
    VK_RWIN, VK_SHIFT, VK_SPACE, VK_TAB, VK_UP,
};

pub fn shift_down() -> bool {
    unsafe { GetKeyState(VK_SHIFT.0 as i32) < 0 }
}

/// Caps Lock 是否鎖定（這個執行緒收到按鍵當下的狀態，#173）。不看鍵盤燈號。
pub fn caps_lock_on() -> bool {
    unsafe { GetKeyState(VK_CAPITAL.0 as i32) & 1 != 0 }
}

pub const VK_CAPS_LOCK: u32 = VK_CAPITAL.0 as u32;

/// 系統的輸入法開關熱鍵（Ctrl＋Space）是否按著：鍵盤開關 compartment 改變時用來分辨使用者按的熱鍵，
/// 與 TIP 啟用、換程式時系統的重設（#117）。Ctrl 看這個執行緒的按鍵狀態（只有前景、收到按鍵的執行緒才是按著），
/// Space 在熱鍵觸發時執行緒狀態可能還沒更新，所以也看實體按鍵狀態。
pub fn ctrl_space_down() -> bool {
    unsafe {
        GetKeyState(VK_CONTROL.0 as i32) < 0
            && (GetKeyState(VK_SPACE.0 as i32) < 0 || GetAsyncKeyState(VK_SPACE.0 as i32) < 0)
    }
}

pub fn modifier_down() -> bool {
    unsafe {
        GetKeyState(VK_CONTROL.0 as i32) < 0
            || GetKeyState(VK_MENU.0 as i32) < 0
            || GetKeyState(VK_LWIN.0 as i32) < 0
            || GetKeyState(VK_RWIN.0 as i32) < 0
    }
}

pub fn is_modifier(vk: u32) -> bool {
    let v = vk as u16;
    [
        VK_SHIFT,
        VK_LSHIFT,
        VK_RSHIFT,
        VK_CONTROL,
        VK_LCONTROL,
        VK_RCONTROL,
        VK_MENU,
        VK_LMENU,
        VK_RMENU,
        VK_LWIN,
        VK_RWIN,
        VK_CAPITAL,
    ]
    .iter()
    .any(|k| k.0 == v)
}

/// 具名鍵；不是具名鍵回傳 None。
pub fn named(vk: u32) -> Option<&'static str> {
    let v = vk as u16;
    Some(match v {
        x if x == VK_RETURN.0 => "Enter",
        x if x == VK_ESCAPE.0 => "Escape",
        x if x == VK_BACK.0 => "Backspace",
        x if x == VK_DELETE.0 => "Delete",
        x if x == VK_TAB.0 => "Tab",
        x if x == VK_LEFT.0 => "Left",
        x if x == VK_RIGHT.0 => "Right",
        x if x == VK_UP.0 => "Up",
        x if x == VK_DOWN.0 => "Down",
        x if x == VK_PRIOR.0 => "PageUp",
        x if x == VK_NEXT.0 => "PageDown",
        x if x == VK_HOME.0 => "Home",
        x if x == VK_END.0 => "End",
        x if x == VK_SPACE.0 => " ",
        _ => return None,
    })
}

/// 依目前鍵盤狀態把虛擬鍵轉成字元；只接受可見 ASCII（0x21–0x7e）。
/// `caps_lock` 為 false（中文模式，#168）時不理 Caps Lock：字母照沒開 Caps Lock 時的大小寫，沒按 Shift 是小寫注音鍵，
/// Shift＋字母是大寫；英文模式傳 true，照 Caps Lock 打大寫。
pub fn printable(vk: u32, lparam: isize, caps_lock: bool) -> Option<String> {
    let mut state = [0u8; 256];
    unsafe {
        if GetKeyboardState(&mut state).is_err() {
            return None;
        }
    }
    printable_with(vk, lparam, &mut state, caps_lock)
}

/// [`printable`] 用指定的鍵盤狀態轉換（測試走同一條路）。
pub fn printable_with(vk: u32, lparam: isize, state: &mut [u8; 256], caps_lock: bool) -> Option<String> {
    let scan = ((lparam >> 16) & 0xff) as u32;
    // 不讓 Ctrl／Alt 影響轉換（組合鍵已在 modifier_down 擋掉；Ctrl＋標點要的是不按 Ctrl 時的字元）
    for k in [VK_CONTROL, VK_LCONTROL, VK_RCONTROL, VK_MENU, VK_LMENU, VK_RMENU] {
        state[k.0 as usize] = 0;
    }
    if !caps_lock {
        state[VK_CAPITAL.0 as usize] &= !1; // 只清掉鎖定（toggle）位元
    }
    unsafe {
        let mut buf = [0u16; 8];
        // 0x4：不改變鍵盤的 dead-key 狀態（Windows 10 1607+）
        let n = ToUnicode(vk, scan, Some(&state), &mut buf, 0x4);
        if n != 1 {
            return None;
        }
        let c = buf[0];
        if (0x21..=0x7e).contains(&c) {
            Some(char::from(c as u8).to_string())
        } else {
            None
        }
    }
}

/// 數字鍵盤的數字與運算鍵（VK_NUMPAD0–9、VK_MULTIPLY…VK_DIVIDE）；NumLock 關閉時系統改送方向鍵等，不在此列。
pub fn is_keypad(vk: u32) -> bool {
    (0x60..=0x6f).contains(&vk)
}

/// 具名鍵加上 Shift：Shift+Tab 是獨立的 BackTab（組字中核心吃掉不動作，沒在組字時交還應用程式，#106）。
/// `vocab` 為 true（引擎回報 vocab_keys、不在英文模式）時，Shift 加上
/// ← → Delete Backspace 送 #123 的新鍵名：組字中標示要加入詞庫的範圍、候選窗的反悔鍵。
/// 核心保證這些鍵名在沒有對應功能時就是原本的鍵，所以外殼不必追蹤候選窗狀態；
/// 舊引擎不認得新鍵名，`vocab` 必須來自引擎的回報。
pub fn named_with(vk: u32, shift: bool, vocab: bool) -> Option<&'static str> {
    match named(vk)? {
        "Tab" if shift => Some("BackTab"),
        n if shift && vocab => Some(vocab_key(n).unwrap_or(n)),
        n => Some(n),
    }
}

/// #123 的新鍵名；不是這四個鍵回傳 None。
pub fn vocab_key(base: &str) -> Option<&'static str> {
    Some(match base {
        "Left" => "ShiftLeft",
        "Right" => "ShiftRight",
        "Delete" => "ShiftDelete",
        "Backspace" => "ShiftBackspace",
        _ => return None,
    })
}

/// 新鍵名對應的原本按鍵（送給不支援的引擎前換回來）；不是新鍵名回傳 None。
pub fn vocab_base(key: &str) -> Option<&'static str> {
    Some(match key {
        "ShiftLeft" => "Left",
        "ShiftRight" => "Right",
        "ShiftDelete" => "Delete",
        "ShiftBackspace" => "Backspace",
        _ => return None,
    })
}

/// 鎖定鍵（NumLock、ScrollLock）與 IME 控制鍵（VK_KANA…VK_MODECHANGE、VK_PROCESSKEY）：組字中也直接交給宿主，不送出組字。
pub fn is_lock_or_ime(vk: u32) -> bool {
    matches!(vk, 0x90 | 0x91 | 0x15..=0x1f | 0xe5)
}

/// 組字中要先送出組字、再交給宿主的按鍵（#106）：Ctrl／Alt／Win 組合鍵（單按修飾鍵本身除外），
/// 以及轉不成協定按鍵、核心不會處理的鍵（F 鍵、Insert、其他排列的非 ASCII 字元…）。
/// `protocol` 是 `to_key` 的結果。
pub fn commits_before_host(vk: u32, modified: bool, protocol: Option<&str>) -> bool {
    !is_modifier(vk) && !is_lock_or_ime(vk) && (modified || protocol.is_none())
}

/// 組字中（沒有修飾鍵）TestKeyDown 是否要吃，必須和 OnKeyDown 送核心後的結果一致，否則 WPF 會讓按鍵消失。
/// 轉得成協定按鍵的鍵核心在組字中都處理，只有英文模式的 ↓ 照舊交給宿主（核心回未處理）。
pub fn composing_wants(english: bool, protocol: Option<&str>) -> bool {
    match protocol {
        None => false,
        Some("Down") => !english,
        Some(_) => true,
    }
}

/// 協定的 Key；不該交給輸入法的按鍵回傳 None。數字鍵盤送 "KP" 加上產生的字元，核心不當注音鍵（#75）。
/// `vocab` 見 [`named_with`]；只有 Shift（沒有 Ctrl／Alt／Win，組合鍵在這裡已回 None）時才換成新鍵名。
/// `caps_lock` 見 [`printable`]：英文模式傳 true，中文模式傳 false（不理 Caps Lock，#168）。
pub fn style_key(key: String, shift: bool, caps: bool, shift_space: bool, caps_lower: bool) -> String {
    if key == " " && shift && shift_space { return "ShiftSpace".into(); }
    if caps && caps_lower && key.len() == 1 && key.as_bytes()[0].is_ascii_alphabetic() {
        return if shift { key.to_ascii_uppercase() } else { key.to_ascii_lowercase() };
    }
    key
}

pub fn to_key(vk: u32, lparam: isize, vocab: bool, caps_lock: bool) -> Option<String> {
    if modifier_down() || is_modifier(vk) {
        return None;
    }
    if let Some(n) = named_with(vk, shift_down(), vocab) {
        return Some(n.to_string());
    }
    let c = printable(vk, lparam, caps_lock)?;
    Some(if is_keypad(vk) { format!("KP{c}") } else { c })
}

/// Ctrl＋標點（#168）：只按著 Ctrl（可以加 Shift，沒有 Alt／Win）時這一鍵的協定鍵名；不是這六個鍵回傳 None。
/// 字元是不按 Ctrl 時這一鍵產生的字元（Ctrl+Shift+/ 是 ?）。AltGr 是 Ctrl＋Alt，不算。
/// 要不要送由呼叫端決定（中文模式、引擎回報 ctrl_punctuation）。
pub fn ctrl_punct(vk: u32, lparam: isize) -> Option<&'static str> {
    let alone = unsafe {
        GetKeyState(VK_CONTROL.0 as i32) < 0
            && GetKeyState(VK_MENU.0 as i32) >= 0
            && GetKeyState(VK_LWIN.0 as i32) >= 0
            && GetKeyState(VK_RWIN.0 as i32) >= 0
    };
    if !alone || is_modifier(vk) || named(vk).is_some() || is_keypad(vk) {
        return None;
    }
    ctrl_punct_name(&printable(vk, lparam, false)?)
}

/// 切換「輸出簡體字」的鍵（#192，settings.SimplifiedHotkeyLabel 的 Ctrl＋Shift＋F11）。
pub const VK_SIMPLIFIED_HOTKEY: u32 = VK_F11.0 as u32;

/// 這一鍵是不是切換簡體字的 Ctrl＋Shift＋F11（#192）：只按著 Ctrl 與 Shift，沒有 Alt、Win。
/// 要不要攔由呼叫端看引擎回報的 simplified_hotkey。
pub fn simplified_hotkey(vk: u32) -> bool {
    unsafe {
        is_simplified_hotkey(
            vk,
            GetKeyState(VK_CONTROL.0 as i32) < 0,
            GetKeyState(VK_SHIFT.0 as i32) < 0,
            GetKeyState(VK_MENU.0 as i32) < 0
                || GetKeyState(VK_LWIN.0 as i32) < 0
                || GetKeyState(VK_RWIN.0 as i32) < 0,
        )
    }
}

/// [`simplified_hotkey`] 的判斷本身（測試用）。
pub fn is_simplified_hotkey(vk: u32, ctrl: bool, shift: bool, other: bool) -> bool {
    vk == VK_SIMPLIFIED_HOTKEY && ctrl && shift && !other
}

/// OnTestKeyDown 對一個鍵的決定，留給接著的 OnKeyDown 沿用（#192）：要問引擎的決定（health IPC）在 TestKeyDown 問一次，
/// 兩次答案不會因為引擎剛好在中間斷線或改了設定而不同（TestKeyDown 說要吃、OnKeyDown 卻交給應用程式），
/// 宿主的 UI 執行緒每按一次也只多一次同步往返。
///
/// 只給緊接的那一次 OnKeyDown 用：OnKeyDown 一進來就取走（不論之後是否在 allowed() 提早回傳），OnTestKeyDown 與放開按鍵
/// 一進來就清掉（TestKeyDown 說不吃時 TSF 不會再呼叫 OnKeyDown）。否則留下的舊決定會被之後宿主沒先 TestKeyDown、直接呼叫的
/// OnKeyDown 拿去用（發版前 code review）。
#[derive(Default)]
pub struct Tested(Option<(u32, bool)>);
impl Tested {
    pub fn record(&mut self, vk: u32, decision: bool) {
        self.0 = Some((vk, decision));
    }
    /// OnTestKeyDown、OnTestKeyUp、OnKeyUp 一進來就呼叫。
    pub fn forget(&mut self) {
        self.0 = None;
    }
    /// OnKeyDown 一進來就呼叫：TestKeyDown 剛為同一個鍵決定過就回傳那個決定（只用一次）；None 時（宿主沒先呼叫
    /// TestKeyDown，或是別的鍵）由呼叫端自己問。
    pub fn take(&mut self, vk: u32) -> Option<bool> {
        match self.0.take() {
            Some((tested, decision)) if tested == vk => Some(decision),
            _ => None,
        }
    }
}

/// 不按 Ctrl 時的字元對應的 Ctrl＋標點鍵名。
pub fn ctrl_punct_name(c: &str) -> Option<&'static str> {
    Some(match c {
        "," => "Ctrl+,",
        "." => "Ctrl+.",
        "'" => "Ctrl+'",
        ";" => "Ctrl+;",
        "?" => "Ctrl+?",
        "!" => "Ctrl+!",
        _ => return None,
    })
}

/// health 沒有回報 shift_tap_ms 時的單按時限（毫秒）。
pub const DEFAULT_SHIFT_TAP_MS: u64 = 300;

/// 哪一個 Shift。
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Side {
    Left,
    Right,
}

/// 引擎載入的中英切換鍵設定（health 的 left_shift、right_shift、shift_tap_ms，#117）。
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct ShiftConfig {
    pub left: bool,
    pub right: bool,
    pub tap_ms: u64,
}

impl Default for ShiftConfig {
    /// 引擎不可用或舊版引擎：右 Shift 切換、左 Shift 照系統原本的用途（原本的行為）。
    fn default() -> Self {
        ShiftConfig {
            left: false,
            right: true,
            tap_ms: DEFAULT_SHIFT_TAP_MS,
        }
    }
}

impl ShiftConfig {
    /// 從 health 回覆的 info 取出；沒有 right_shift 的舊引擎右 Shift 照舊切換，沒有 shift_tap_ms 用 300 毫秒。
    pub fn from_info(info: Option<&serde_json::Value>) -> ShiftConfig {
        let mut config = ShiftConfig::default();
        let Some(info) = info else { return config };
        let flag = |key: &str| info[key].as_str().map(|v| v == "true");
        config.left = flag("left_shift").unwrap_or(false);
        config.right = flag("right_shift").unwrap_or(true);
        if let Some(ms) = info["shift_tap_ms"]
            .as_str()
            .and_then(|v| v.parse::<u64>().ok())
        {
            config.tap_ms = ms;
        }
        config
    }

    pub fn toggles(&self, side: Side) -> bool {
        match side {
            Side::Left => self.left,
            Side::Right => self.right,
        }
    }
}

/// 滑鼠按鍵的狀態（#206）：這個執行緒的 GetKeyState，每個按鍵的低位元在每次按下時翻轉（低 8 位元），
/// 高位元是正按著（高 8 位元）。按下 Shift 時記下，放開時翻轉過（中間點過滑鼠：Shift＋點選延伸選取）或還按著
/// （Shift＋拖曳）就不算單按。宿主處理完滑鼠訊息才輪到 Shift 放開的按鍵訊息，執行緒的狀態已經更新。
pub fn mouse_state() -> u16 {
    use windows::Win32::UI::Input::KeyboardAndMouse::{
        VK_LBUTTON, VK_MBUTTON, VK_RBUTTON, VK_XBUTTON1, VK_XBUTTON2,
    };
    let mut state = 0u16;
    for (i, vk) in [VK_LBUTTON, VK_RBUTTON, VK_MBUTTON, VK_XBUTTON1, VK_XBUTTON2]
        .iter()
        .enumerate()
    {
        let k = unsafe { GetKeyState(vk.0 as i32) };
        if k & 1 != 0 {
            state |= 1 << i;
        }
        if k < 0 {
            state |= 1 << (i + 8);
        }
    }
    state
}

/// 單按 Shift 才切換（左右各用一個）；連續 TestKeyDown、長按重複及 Shift 組合鍵不切換，
/// 按下到放開超過時限（shift_tap_ms）也不切換：按著 Shift 猶豫、或 Shift 配合滑鼠選取時不誤切（#117）。
/// 按著 Shift 時點過滑鼠（Shift＋點選延伸選取）也不切換（#206，見 [`mouse_state`]）。
/// 時間與滑鼠狀態由呼叫端傳入（毫秒，單調遞增），方便測試。
#[derive(Default)]
pub struct ShiftTap {
    down: bool,
    solo: bool,
    since: u64,
    mouse: u16,
}
impl ShiftTap {
    /// target：這個按鍵是否為追蹤的那一個 Shift；now：這次按鍵的時間；mouse：當下的 [`mouse_state`]。
    pub fn observe(&mut self, target: bool, repeat: bool, modified: bool, now: u64, mouse: u16) {
        if target {
            if !self.down && !repeat {
                self.down = true;
                self.solo = !modified;
                self.since = now;
                self.mouse = mouse;
            }
            if modified {
                self.solo = false;
            }
        } else if self.down {
            self.solo = false;
        }
    }
    #[cfg(test)]
    pub fn pending(&self) -> bool {
        self.down
    }
    /// 現在放開是否算單按（不看時間、不改變狀態）；用來決定要不要向引擎查設定。mouse：當下的 [`mouse_state`]。
    pub fn solo(&self, modified: bool, mouse: u16) -> bool {
        self.down && self.solo && !modified && mouse == self.mouse && mouse >> 8 == 0
    }
    /// 現在放開是否算單按，而且按下到現在沒有超過 limit 毫秒（不改變狀態）。
    pub fn tap(&self, modified: bool, mouse: u16, now: u64, limit: u64) -> bool {
        self.solo(modified, mouse) && now.saturating_sub(self.since) <= limit
    }
    #[cfg(test)]
    pub fn release(&mut self, modified: bool, now: u64, limit: u64) -> bool {
        let toggle = self.tap(modified, self.mouse, now, limit);
        *self = Self::default();
        toggle
    }
}

/// 這個按鍵是左或右 Shift；都不是回傳 None。
pub fn shift_side(vk: u32, lparam: isize) -> Option<Side> {
    if right_shift(vk, lparam) {
        Some(Side::Right)
    } else if left_shift(vk, lparam) {
        Some(Side::Left)
    } else {
        None
    }
}

/// 按鍵事件的時間（毫秒，單調遞增）。在 TestKeyDown／KeyUp 當下取，宿主忙碌時與實際按放有些落差，
/// 但不依賴 GetMessageTime（TSF 不一定在宿主取出這個按鍵訊息時呼叫我們）。
pub fn now_ms() -> u64 {
    unsafe { windows::Win32::System::SystemInformation::GetTickCount64() }
}

pub fn right_shift(vk: u32, lparam: isize) -> bool {
    vk == VK_RSHIFT.0 as u32 || (vk == VK_SHIFT.0 as u32 && ((lparam >> 16) & 0xff) == 0x36)
}

pub fn left_shift(vk: u32, lparam: isize) -> bool {
    vk == VK_LSHIFT.0 as u32 || (vk == VK_SHIFT.0 as u32 && ((lparam >> 16) & 0xff) == 0x2a)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn right_shift_tap_and_chords() {
        const L: u64 = DEFAULT_SHIFT_TAP_MS;
        let mut tap = ShiftTap::default();
        tap.observe(true, false, false, 0, 0);
        tap.observe(true, false, false, 10, 0); // TestKeyDown 可重複
        tap.observe(true, true, false, 20, 0); // 長按
        assert!(tap.release(false, 100, L));
        assert!(!tap.release(false, 100, L));
        tap.observe(true, false, false, 0, 0);
        tap.observe(false, false, false, 10, 0); // Shift + 字母
        assert!(!tap.release(false, 50, L));
        tap.observe(true, false, true, 0, 0); // Ctrl + Shift
        assert!(!tap.release(false, 50, L));
        tap.observe(true, false, false, 0, 0);
        assert!(!tap.release(true, 50, L));
        assert!(right_shift(0x10, 0x36 << 16));
        assert!(!right_shift(0x10, 0x2a << 16));
        assert!(left_shift(0x10, 0x2a << 16) && left_shift(0xa0, 0));
        assert!(!left_shift(0x10, 0x36 << 16) && !left_shift(0xa1, 0));
        assert_eq!(shift_side(0xa1, 0), Some(Side::Right));
        assert_eq!(shift_side(0x10, 0x2a << 16), Some(Side::Left));
        assert_eq!(shift_side(0x41, 0), None);
        tap.observe(true, false, false, 0, 0);
        assert!(tap.solo(false, 0) && !tap.solo(true, 0) && tap.solo(false, 0));
        assert!(tap.release(false, 10, L));
    }

    #[test]
    fn shift_with_mouse_click_does_not_toggle() {
        // #206：按著 Shift 點滑鼠（延伸選取）不切換：左鍵、右鍵按下時低位元翻轉；拖曳中（還按著）也不切換。
        // 兩次點選（雙擊）低位元翻回原狀，只看得到正按著的那一下。
        const L: u64 = DEFAULT_SHIFT_TAP_MS;
        let mut tap = ShiftTap::default();
        tap.observe(true, false, false, 0, 0b0);
        assert!(!tap.solo(false, 0b1), "Shift＋左鍵點選");
        assert!(!tap.tap(false, 0b1, 100, L));
        assert!(!tap.solo(false, 0b10), "Shift＋右鍵");
        assert!(!tap.solo(false, 1 << 8 | 0b1), "Shift＋拖曳中");
        assert!(tap.solo(false, 0b0), "沒點滑鼠照樣是單按");
        tap = ShiftTap::default();
        tap.observe(true, false, false, 0, 0b1); // 先前點過，低位元本來就是 1
        assert!(tap.tap(false, 0b1, 100, L), "按下 Shift 之前的點選不算");
        assert!(!tap.tap(false, 0b0, 100, L));
        // 長按重複不更新按下時的滑鼠狀態。
        tap = ShiftTap::default();
        tap.observe(true, false, false, 0, 0b0);
        tap.observe(true, true, false, 30, 0b1);
        assert!(!tap.solo(false, 0b1));
    }

    #[test]
    fn long_shift_press_does_not_toggle() {
        // #117：按下到放開超過時限不切換；剛好在時限內仍切換。時間從第一次按下算，長按的重複不重設。
        const L: u64 = DEFAULT_SHIFT_TAP_MS;
        let mut tap = ShiftTap::default();
        tap.observe(true, false, false, 1_000, 0);
        assert!(tap.tap(false, 0, 1_300, L));
        assert!(tap.release(false, 1_300, L));
        tap.observe(true, false, false, 1_000, 0);
        tap.observe(true, true, false, 1_250, 0); // 自動重複
        tap.observe(true, false, false, 1_290, 0); // TestKeyDown 與 KeyDown 各一次
        assert!(tap.solo(false, 0), "沒有其他按鍵仍是單按，只是太久");
        assert!(!tap.tap(false, 0, 1_301, L));
        assert!(!tap.release(false, 1_301, L));
        assert!(!tap.pending(), "放開後重新判斷");
        // 時限來自引擎：設成 500 時 0.4 秒仍切換。
        tap.observe(true, false, false, 0, 0);
        assert!(tap.release(false, 400, 500));
        // 時鐘倒退（不會發生，但不能溢位）。
        tap.observe(true, false, false, 100, 0);
        assert!(tap.release(false, 50, L));
    }

    #[test]
    fn shift_config_from_engine() {
        use serde_json::json;
        // 引擎不可用：右 Shift 照舊切換，左 Shift 不切。
        assert_eq!(
            ShiftConfig::from_info(None),
            ShiftConfig {
                left: false,
                right: true,
                tap_ms: 300
            }
        );
        // 舊引擎只回報 left_shift（#65）：右 Shift 開、左 Shift 看 left_shift、時限 300。
        let old = json!({"left_shift": "true"});
        assert_eq!(
            ShiftConfig::from_info(Some(&old)),
            ShiftConfig {
                left: true,
                right: true,
                tap_ms: 300
            }
        );
        let older = json!({"emoji_shortcut": "equals"});
        assert_eq!(ShiftConfig::from_info(Some(&older)), ShiftConfig::default());
        // toggle-key 四種值（#117）。
        for (left, right, want_left, want_right) in [
            ("false", "true", false, true),   // right
            ("true", "true", true, true),     // both
            ("true", "false", true, false),   // left
            ("false", "false", false, false), // none
        ] {
            let info = json!({"left_shift": left, "right_shift": right, "shift_tap_ms": "300"});
            let c = ShiftConfig::from_info(Some(&info));
            assert_eq!(
                (c.left, c.right, c.tap_ms),
                (want_left, want_right, 300),
                "{left} {right}"
            );
            assert_eq!(c.toggles(Side::Left), want_left);
            assert_eq!(c.toggles(Side::Right), want_right);
        }
        let custom = json!({"right_shift": "true", "shift_tap_ms": "450"});
        assert_eq!(ShiftConfig::from_info(Some(&custom)).tap_ms, 450);
        let bad = json!({"shift_tap_ms": "abc"});
        assert_eq!(ShiftConfig::from_info(Some(&bad)).tap_ms, 300);
    }

    #[test]
    fn input_style_modifier_keys() {
        assert_eq!(style_key(" ".into(), true, false, true, false), "ShiftSpace");
        assert_eq!(style_key(" ".into(), true, false, false, false), " ");
        assert_eq!(style_key("A".into(), false, true, false, true), "a");
        assert_eq!(style_key("A".into(), true, true, false, true), "A");
        assert_eq!(style_key("a".into(), true, true, false, true), "A");
        assert_eq!(style_key("a".into(), true, true, false, false), "a");
    }

    #[test]
    fn navigation_keys() {
        // #106：組字中由核心處理的導覽鍵都要有協定名稱，TestKeyDown 與 OnKeyDown 才會一致。
        for (vk, shift, want) in [
            (VK_HOME.0, false, "Home"),
            (VK_END.0, true, "End"),
            (VK_PRIOR.0, false, "PageUp"),
            (VK_NEXT.0, false, "PageDown"),
            (VK_UP.0, false, "Up"),
            (VK_TAB.0, false, "Tab"),
            (VK_TAB.0, true, "BackTab"),
        ] {
            assert_eq!(named_with(vk as u32, shift, false), Some(want), "{vk:#x}");
        }
        assert_eq!(named_with(0x41, true, false), None);
    }

    #[test]
    fn vocab_key_names_need_shift_and_engine_support() {
        // #123：引擎回報 vocab_keys 而且按著 Shift 才送新鍵名。
        for (vk, base, name) in [
            (VK_LEFT.0, "Left", "ShiftLeft"),
            (VK_RIGHT.0, "Right", "ShiftRight"),
            (VK_DELETE.0, "Delete", "ShiftDelete"),
            (VK_BACK.0, "Backspace", "ShiftBackspace"),
        ] {
            let vk = vk as u32;
            assert_eq!(named_with(vk, true, true), Some(name), "{base}");
            assert_eq!(named_with(vk, true, false), Some(base), "開關關閉／舊引擎：Shift 不影響鍵名");
            assert_eq!(named_with(vk, false, true), Some(base), "沒按 Shift");
            assert_eq!(named_with(vk, false, false), Some(base));
            assert_eq!(vocab_base(name), Some(base));
            assert_eq!(vocab_key(base), Some(name));
        }
        // 其他具名鍵不變；Shift+Tab 仍是 BackTab。
        for (vk, want) in [
            (VK_UP.0, "Up"),
            (VK_DOWN.0, "Down"),
            (VK_HOME.0, "Home"),
            (VK_END.0, "End"),
            (VK_RETURN.0, "Enter"),
            (VK_ESCAPE.0, "Escape"),
            (VK_PRIOR.0, "PageUp"),
            (VK_SPACE.0, " "),
            (VK_TAB.0, "BackTab"),
        ] {
            assert_eq!(named_with(vk as u32, true, true), Some(want), "{want}");
        }
        assert_eq!(named_with(0x41, true, true), None);
        for key in ["Left", "Enter", "BackTab", "a"] {
            assert_eq!(vocab_base(key), None, "{key}");
        }
        // 組字中新鍵名一律交給核心（核心在組字中都回 handled），TestKeyDown 與 OnKeyDown 一致，
        // 而且答案和原本的鍵相同：開關切換不會改變哪些鍵被吃。
        for name in ["ShiftLeft", "ShiftRight", "ShiftDelete", "ShiftBackspace"] {
            for english in [false, true] {
                assert!(composing_wants(english, Some(name)), "{name}");
                assert_eq!(
                    composing_wants(english, Some(name)),
                    composing_wants(english, vocab_base(name)),
                    "{name}"
                );
            }
        }
        // Ctrl＋Shift＋← 這類組合鍵：to_key 回 None，組字中先送出組字再交給宿主（不送新鍵名）。
        assert!(commits_before_host(VK_LEFT.0 as u32, true, None));
    }

    #[test]
    fn host_keys_while_composing() {
        // F5、Insert 轉不成協定按鍵：先送出組字再交給宿主，TestKeyDown 不吃。
        for vk in [0x74u32, 0x2d] { // VK_F5、VK_INSERT
            assert!(commits_before_host(vk, false, None), "{vk:#x}");
            assert!(!composing_wants(false, None));
        }
        // Ctrl+Enter、Ctrl+V：組合鍵先送出組字。
        assert!(commits_before_host(VK_RETURN.0 as u32, true, None));
        assert!(commits_before_host(0x56, true, None));
        // 單按修飾鍵、鎖定鍵與 IME 控制鍵不送出組字。
        for vk in [VK_CONTROL.0, VK_LCONTROL.0, VK_MENU.0, VK_LWIN.0, VK_SHIFT.0, VK_CAPITAL.0] {
            assert!(!commits_before_host(vk as u32, true, None), "{vk:#x}");
        }
        for vk in [0x90u32, 0x91, 0x15, 0x19, 0x1c, 0xe5] {
            assert!(!commits_before_host(vk, false, None), "{vk:#x}");
        }
        // 轉得成協定按鍵的鍵由核心處理；只有英文模式的 ↓ 照舊交給宿主。
        assert!(!commits_before_host(VK_HOME.0 as u32, false, Some("Home")));
        for key in ["Home", "End", "Up", "PageUp", "PageDown", "Tab", "BackTab", "a", "KP1"] {
            assert!(composing_wants(false, Some(key)) && composing_wants(true, Some(key)), "{key}");
        }
        assert!(composing_wants(false, Some("Down")) && !composing_wants(true, Some("Down")));
    }

    #[test]
    fn composition_start_keys_follow_layout() {
        // 標準排列維持原本的集合（= 由 Emoji 設定決定，不在集合裡）。
        for key in ["a", "1", "2", "5", "8", "9", "0", ",", "'", "<", "`"] {
            assert!(starts_composition(Layout::Standard, key), "{key}");
        }
        for key in ["3", "4", "6", "7", "=", "A", "@"] {
            assert!(!starts_composition(Layout::Standard, key), "{key}");
        }
        // 倚天 41 鍵（#83）：7 8 9 0 = ' 是注音、\ 是頓號；1–4 是聲調、5 6 不是注音鍵，交還宿主。
        for key in ["a", "7", "8", "9", "0", "=", "'", ";", ",", ".", "/", "-", "\\", "<", "`"] {
            assert!(starts_composition(Layout::ETen, key), "{key}");
        }
        // 核心標點表的每個鍵兩種排列都要交給核心：\ 頓號（#24）、& 全形＆（#46）、^ 刪節號、_ 破折號（#118）。
        for key in ["<", ">", "?", ":", "\"", "!", "~", "[", "]", "{", "}", "\\", "(", ")", "&", "^", "_"] {
            assert!(starts_composition(Layout::Standard, key), "{key}");
            assert!(starts_composition(Layout::ETen, key), "{key}");
        }
        for key in ["1", "2", "3", "4", "5", "6", "A", "@"] {
            assert!(!starts_composition(Layout::ETen, key), "{key}");
        }
        // 聲調鍵（#135）另依設定，不在固定集合裡。
        for key in ["3", "4", "6", "7"] {
            assert!(tone_key(Layout::Standard, key) && !starts_composition(Layout::Standard, key), "{key}");
        }
        for key in ["1", "2", "3", "4"] {
            assert!(tone_key(Layout::ETen, key) && !starts_composition(Layout::ETen, key), "{key}");
        }
        for key in ["1", "2", "5", " ", "a"] {
            assert!(!tone_key(Layout::Standard, key), "{key}");
        }
        for key in ["5", "6", "7", " ", "a"] {
            assert!(!tone_key(Layout::ETen, key), "{key}");
        }
        // 只有答案不同的鍵要查排列。
        for c in (0x21u8..0x7f).map(|b| (b as char).to_string()) {
            let differs = starts_composition(Layout::Standard, &c) != starts_composition(Layout::ETen, &c)
                || tone_key(Layout::Standard, &c) != tone_key(Layout::ETen, &c);
            assert!(!differs || layout_dependent(&c), "{c} 的答案依排列而不同，必須查排列");
        }
        assert!(layout_dependent("=") && layout_dependent("q") && layout_dependent("a"));
        assert!(!layout_dependent("A") && !layout_dependent(",") && !layout_dependent("@"));
    }

    #[test]
    fn simplified_hotkey_is_only_ctrl_shift_f11() {
        let f11 = VK_SIMPLIFIED_HOTKEY;
        assert_eq!(f11, 0x7A);
        assert!(is_simplified_hotkey(f11, true, true, false));
        assert!(!is_simplified_hotkey(f11, true, false, false), "Ctrl＋F11");
        assert!(!is_simplified_hotkey(f11, false, true, false), "Shift＋F11");
        assert!(!is_simplified_hotkey(f11, false, false, false), "F11（全螢幕）");
        assert!(!is_simplified_hotkey(f11, true, true, true), "多按 Alt／Win");
        assert!(!is_simplified_hotkey(0x7B, true, true, false), "Ctrl＋Shift＋F12");
        assert!(!is_simplified_hotkey(0x46, true, true, false), "Ctrl＋Shift＋F");
    }

    #[test]
    fn key_down_reuses_the_test_key_down_decision() {
        // #192 code review：TestKeyDown 說要吃 Ctrl＋Shift＋F11，OnKeyDown 時引擎剛好斷線也照樣吃，不再問一次。
        let f11 = VK_SIMPLIFIED_HOTKEY;
        let mut tested = Tested::default();
        tested.record(f11, true);
        assert_eq!(tested.take(f11), Some(true));
        // 只用一次：下一次（宿主直接呼叫 OnKeyDown、沒先 TestKeyDown）重新問。
        assert_eq!(tested.take(f11), None);
        // TestKeyDown 說不吃，OnKeyDown 也不吃。
        tested.record(f11, false);
        assert_eq!(tested.take(f11), Some(false));
        // 記下的是別的鍵：照常問，而且那個舊決定也一起丟掉。
        tested.record(0x41, false);
        assert_eq!(tested.take(f11), None);
        assert_eq!(tested.take(0x41), None);
    }

    #[test]
    fn test_key_down_decision_does_not_outlive_its_key_press() {
        // #192 發版前 code review 第二輪：OnKeyDown 在 allowed() 提早回傳時，原本沒取走 TestKeyDown 的決定，
        // 之後宿主沒先 TestKeyDown 就直接呼叫 OnKeyDown 時沿用了舊的「要吃」。
        let f11 = VK_SIMPLIFIED_HOTKEY;
        let mut tested = Tested::default();
        // OnTestKeyDown 說要吃 → OnKeyDown 一進來就取走（接著在 allowed() 提早回傳，沒用到）。
        tested.record(f11, true);
        let _ = tested.take(f11);
        // 之後宿主直接呼叫 OnKeyDown：沒有舊決定，重新問。
        assert_eq!(tested.take(f11), None);
        // TestKeyDown 說不吃，TSF 不再呼叫 OnKeyDown；放開（OnTestKeyUp／OnKeyUp）一進來就清掉。
        tested.record(f11, false);
        tested.forget();
        assert_eq!(tested.take(f11), None);
        // OnTestKeyDown 在 allowed() 提早回傳（沒走到記錄）：一進來就清掉上一次的決定。
        tested.record(f11, true);
        tested.forget();
        assert_eq!(tested.take(f11), None);
    }

    #[test]
    fn ctrl_punct_names() {
        // #168：六個鍵；Ctrl+/、Ctrl+1、Ctrl+Shift+,（<）、Ctrl+[ 照舊交給應用程式。
        for (c, want) in [
            (",", "Ctrl+,"),
            (".", "Ctrl+."),
            ("'", "Ctrl+'"),
            (";", "Ctrl+;"),
            ("?", "Ctrl+?"),
            ("!", "Ctrl+!"),
        ] {
            assert_eq!(ctrl_punct_name(c), Some(want), "{c}");
        }
        for c in ["/", "1", "<", ">", "[", "]", "a", "A", "\"", ":"] {
            assert_eq!(ctrl_punct_name(c), None, "{c}");
        }
    }

    #[test]
    fn caps_lock_follows_mode() {
        // #168：中文模式（caps_lock=false）不理 Caps Lock；英文模式照 Caps Lock 打大寫。Shift＋字母照沒開 Caps Lock 時。
        // 用美式排列的掃描碼（A=0x1e、逗號=0x33、1=0x02、斜線=0x35）。
        const VK_A: u32 = 0x41;
        let lp = |scan: isize| scan << 16;
        let state = |caps: bool, shift: bool, ctrl: bool| {
            let mut s = [0u8; 256];
            if caps {
                s[VK_CAPITAL.0 as usize] = 1;
            }
            if shift {
                s[VK_SHIFT.0 as usize] = 0x80;
                s[VK_LSHIFT.0 as usize] = 0x80;
            }
            if ctrl {
                s[VK_CONTROL.0 as usize] = 0x80;
                s[VK_LCONTROL.0 as usize] = 0x80;
            }
            s
        };
        for (caps, shift, honor, want) in [
            (true, false, false, "a"), // 中文模式、Caps Lock：小寫注音鍵
            (true, true, false, "A"),  // 中文模式、Caps Lock＋Shift：照沒開 Caps Lock 時，大寫
            (true, false, true, "A"),  // 英文模式照 Caps Lock
            (true, true, true, "a"),   // 英文模式 Caps Lock＋Shift 反轉（系統行為）
            (false, false, false, "a"),
            (false, true, false, "A"),
        ] {
            assert_eq!(
                printable_with(VK_A, lp(0x1e), &mut state(caps, shift, false), honor).as_deref(),
                Some(want),
                "caps={caps} shift={shift} honor={honor}"
            );
        }
        // 數字與標點不受 Caps Lock 影響；Ctrl 不影響轉換（Ctrl＋標點用不按 Ctrl 的字元）。
        assert_eq!(printable_with(0xbc, lp(0x33), &mut state(true, false, true), false).as_deref(), Some(","));
        assert_eq!(printable_with(0x31, lp(0x02), &mut state(true, true, true), false).as_deref(), Some("!"));
        assert_eq!(printable_with(0xbf, lp(0x35), &mut state(false, true, true), false).as_deref(), Some("?"));
    }

    #[test]
    fn keypad_keys() {
        for vk in [0x60, 0x61, 0x69, 0x6a, 0x6b, 0x6d, 0x6e, 0x6f] {
            assert!(is_keypad(vk), "{vk:#x}");
        }
        // 主鍵盤數字、OEM 斜線與減號、NumLock 關閉時的方向鍵都不是數字鍵盤。
        for vk in [0x30, 0x31, 0x39, 0xbd, 0xbf, 0x23, 0x25, 0x90] {
            assert!(!is_keypad(vk), "{vk:#x}");
        }
    }
}

/// 引擎載入的鍵盤排列（health 的 keyboard_layout，#83）：只給沒有回報 idle_keys 的舊引擎推算閒置按鍵（#183 起由引擎回報）。
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Layout {
    Standard,
    ETen,
}

/// 空組字時可接受的字元，依鍵盤排列。大寫／其他 ASCII 須在 TestKeyDown
/// 就放行；某些宿主在 Test 回報吃鍵後，會忽略 OnKeyDown 的 handled=false。聲調鍵另由 tone_key 依設定決定（#135）。
/// 標準排列的 = 由 Emoji 設定決定（呼叫端處理）。倚天 41 鍵的 = 是 ㄦ、7 是 ㄑ，1–4 是聲調、5 6 不是注音鍵，
/// ' 是 ㄘ（#83）。標點要跟核心的標點表（core/layout）一致，否則沒在打字時會直接交給應用程式打出半形：
/// \ 是頓號（#24）、& 是全形＆（#46）、^ 是「……」、_ 是「——」（#118），兩種排列都一樣。
pub fn starts_composition(layout: Layout, key: &str) -> bool {
    let keys = match layout {
        Layout::Standard => "`abcdefghijklmnopqrstuvwxyz125890,.;/-<>?:\"!~[]{}'\\()&^_",
        Layout::ETen => "`abcdefghijklmnopqrstuvwxyz7890,.;/-='\\<>?:\"!~[]{}()&^_",
    };
    key.len() == 1 && keys.contains(key)
}

/// 聲調鍵（不含空白的一聲）：標準排列 3 4 6 7、倚天 1 2 3 4。沒在組字時要不要交給引擎依引擎回報（#135、#175，
/// 見 session::idle_tone_keys），不在 starts_composition 的固定集合裡。
pub fn tone_key(layout: Layout, key: &str) -> bool {
    let keys = match layout {
        Layout::Standard => "3467",
        Layout::ETen => "1234",
    };
    key.len() == 1 && keys.contains(key)
}

/// 沒在組字時答案依排列或設定而不同的鍵，照引擎回報的 idle_keys 決定（session::idle_key，依連線快取，不每鍵往返）：
/// 6 是標準排列的 ˊ、倚天不是注音鍵（#135）；許氏的數字、- / 與 q 都不是注音鍵，交還宿主（#183）；= 看 Emoji 設定。
/// 小寫字母也在內：之後的排列不一定每個字母都用到。其他鍵（Shift＋字母、, . ; ' 與 Shift 標點）三種排列答案相同，
/// 照 starts_composition 的固定集合；中英混打時引擎雖然也收 Shift＋字母與 @，外殼照舊交給應用程式。
pub fn layout_dependent(key: &str) -> bool {
    key.len() == 1 && (key.as_bytes()[0].is_ascii_lowercase() || "1234567890=-/".contains(key))
}
