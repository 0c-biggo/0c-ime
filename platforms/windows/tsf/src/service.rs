//! TextService：TSF 的 text input processor。只接事件、送協定、依快照更新組字區；
//! 全部狀態在核心，這裡只留 TSF 物件的把手與最近一個快照的摘要。
use std::cell::{Cell, RefCell};
use std::collections::HashSet;
use std::rc::Rc;
use std::time::{Duration, Instant};

use serde_json::json;
use windows::Win32::Foundation::{E_INVALIDARG, HWND, LPARAM, RECT, WPARAM};
use windows::Win32::System::Variant::{VariantClear, VT_I4};
use windows::Win32::UI::TextServices::{
    IEnumTfDisplayAttributeInfo, ITfCompartment,
    ITfCompartmentEventSink, ITfCompartmentEventSink_Impl, ITfCompartmentMgr, ITfComposition,
    ITfCompositionSink, ITfCompositionSink_Impl, ITfContext, ITfContextView,
    ITfDisplayAttributeInfo, ITfDisplayAttributeProvider, ITfDisplayAttributeProvider_Impl,
    ITfDocumentMgr, ITfKeyEventSink, ITfKeyEventSink_Impl, ITfKeystrokeMgr, ITfLangBarItem,
    ITfLangBarItemButton, ITfLangBarItemMgr, ITfSource, ITfTextInputProcessor,
    ITfTextInputProcessorEx, ITfTextInputProcessorEx_Impl, ITfTextInputProcessor_Impl,
    ITfTextLayoutSink, ITfTextLayoutSink_Impl, ITfThreadMgr, ITfThreadMgrEventSink,
    ITfThreadMgrEventSink_Impl, ITfUIElementMgr, TfLayoutCode, GUID_COMPARTMENT_KEYBOARD_OPENCLOSE,
    TF_INVALID_COOKIE, TF_LC_DESTROY, TF_TMAE_COMLESS, TF_TMAE_SECUREMODE, TF_TMAE_UIELEMENTENABLEDONLY,
};
use windows::Win32::UI::WindowsAndMessaging::{GetForegroundWindow, GetWindowThreadProcessId};
use windows_core::{implement, ComObject, IUnknownImpl, Interface, Ref, BOOL, GUID};
use zuin_win_common::{has_content, notice_expires, UiCandidate, UiMessage, NOTICE_MS};

use crate::candidate::CandidateWindow;
use crate::displayattr::{AttributeEnum, InputAttribute, KINDS};
use crate::log::{zdebug, zlog};
use crate::session::{Session, Snapshot};
use crate::ui::UiClient;
use crate::uielement::Elements;
use crate::{caret, context, edit, keys, privacy};

#[implement(
    ITfTextInputProcessorEx,
    ITfTextInputProcessor,
    ITfKeyEventSink,
    ITfCompositionSink,
    ITfThreadMgrEventSink,
    ITfDisplayAttributeProvider,
    ITfTextLayoutSink,
    ITfCompartmentEventSink
)]
pub struct TextService {
    state: RefCell<State>,
    /// edit::apply 進行中（巢狀深度）。宿主可能在我們寫入時或剛寫完、apply 還沒回來時就送 OnLayoutChange，
    /// 甚至在同一個呼叫堆疊裡執行排隊的唯讀 edit session；這時 shown 與 revision 還是上一鍵的，不能拿來重送。
    applying: Cell<u32>,
    /// apply 進行中收到、被略過的版面通知：present_in_edit 套用完再補查一次。
    layout_missed: Cell<bool>,
}

/// 標記 edit::apply 進行中；離開（含 panic）時恢復。
struct Applying<'a>(&'a Cell<u32>);
impl<'a> Applying<'a> {
    fn enter(depth: &'a Cell<u32>, missed: &Cell<bool>) -> Self {
        if depth.get() == 0 {
            missed.set(false);
        }
        depth.set(depth.get() + 1);
        Applying(depth)
    }
}
impl Drop for Applying<'_> {
    fn drop(&mut self) {
        self.0.set(self.0.get().saturating_sub(1));
    }
}

struct State {
    thread_mgr: Option<ITfThreadMgr>,
    client_id: u32,
    thread_mgr_cookie: u32,
    composition: Option<ITfComposition>,
    ending_composition: Rc<Cell<bool>>,
    session: Session,
    composing: bool,
    attr_atoms: [u32; KINDS.len()],
    eaten: HashSet<u32>, // 吃掉了 key-down 的虛擬鍵，key-up 也要吃掉
    ui: UiClient,
    right_shift: keys::ShiftTap,
    left_shift: keys::ShiftTap,
    secure: bool,
    /// 以 TF_TMAE_UIELEMENTENABLEDONLY 啟用：宿主自行繪製候選（遊戲等，#86）。
    ui_less: bool,
    /// TSF 啟用本輸入法時給的旗標（ActivateEx）。系統相容層（CUAS）替 IMM32 程式啟用時是 0；
    /// 自己接 TSF 的程式依它呼叫 ActivateEx 的旗標（Windows Terminal 是 0x40000040）。
    activate_flags: u32,
    callback: Option<CandidateWindow>,
    revision: u32,
    selection: Option<(ITfContext, Vec<i64>)>,
    langbar: Option<(ITfLangBarItemMgr, ITfLangBarItemButton)>,
    /// 目前要顯示的候選窗（含還沒拿到位置、等 OnLayoutChange 的）；沒有候選就是 None。
    shown: Option<Shown>,
    /// 同一次組字最近一次 GetTextExt 查到的游標矩形（Chromium 新打的字版面晚到時先用它）。
    /// 每一鍵查到就更新（不只顯示候選的鍵）；組字結束時清掉。
    anchor: Option<RECT>,
    /// 最近一次查到游標矩形的視窗與位置，跨組字保留：新組字的第一鍵拿不到位置時，
    /// 還在同一個輸入框裡就用它（caret::around_field）。
    recent: Option<(HWND, RECT)>,
    /// 接收 OnLayoutChange 的 context 與 cookie：有候選窗時才註冊。
    layout_sink: Option<(ITfContext, u32)>,
    notes: caret::Notes,
    /// 執行緒的鍵盤開關 compartment（GUID_COMPARTMENT_KEYBOARD_OPENCLOSE）與通知的 cookie：
    /// 系統的輸入法開關熱鍵（繁中預設 Ctrl＋Space）改它，外殼當成中英切換（#117）。
    openclose: Option<(ITfCompartment, u32)>,
    /// 最近一次看到的鍵盤開關值（沒有值當成 0）；只有值真的改變才處理。
    openclose_value: i32,
    /// 組字中焦點移到 NULL 時組字所在的 context（沒有就是那次的原文件，#189）：送出時組字範圍與這次焦點變化的原文件都拿不到
    /// context（範圍或 context 已失效，A→NULL→B 第二次的原文件是 NULL）才送到這裡，免得打到一半的字沒地方送。
    left_from: Option<ITfContext>,
    /// OnTestKeyDown 對 Ctrl＋Shift＋F11 的決定（#192），OnKeyDown 沿用。
    hotkey_tested: keys::Tested,
    /// OnTestKeyDown 對其他按鍵的決定（#216）：說要吃、OnKeyDown 卻沒吃成時（等不到重啟中的引擎），以原字元補送。
    key_tested: keys::Tested,
}

/// 候選窗最近一次的呈現：OnLayoutChange 時重查游標位置並補送。
struct Shown {
    ctx: ITfContext,
    cursor: usize,
    message: UiMessage,
    owner: Option<HWND>,
    document: Option<ITfDocumentMgr>,
    /// 位置來自 GetTextExt（不是退路）。
    exact: bool,
    /// 這個畫面版本已經因 OnLayoutChange 重查幾次（避免宿主每查一次就再通知一次而停不下來）。
    requeries: u32,
    /// 送出的時間：提示（notice）在 0c-ui 顯示 NOTICE_MS 後自己收起，重送時不能讓它再跳出來。
    since: Instant,
}

/// 執行緒的焦點文件改變時，進行中的組字怎麼辦（#189）。
#[derive(Debug, PartialEq, Eq)]
enum FocusChange {
    /// 焦點落在組字所在的文件：宿主把組字搬了過去（Excel 的儲存格從「就緒」進入編輯），繼續組字。
    CompositionMoved,
    /// 組字中焦點移到 NULL：可能是宿主正在交接（Excel 先移開焦點、再交給儲存格編輯器），稍後再確認。
    Undecided,
    /// 換到別的文件，或沒在組字：送出組字。
    Left,
}

/// `owner`：組字所在的文件（沒在組字時為 None）。泛型只為了能在測試裡用一般型別代替 ITfDocumentMgr。
fn focus_change<T: PartialEq>(focus: Option<&T>, owner: Option<&T>) -> FocusChange {
    match (focus, owner) {
        (_, None) => FocusChange::Left,
        (Some(focus), Some(owner)) if focus == owner => FocusChange::CompositionMoved,
        (None, Some(_)) => FocusChange::Undecided,
        (Some(_), Some(_)) => FocusChange::Left,
    }
}

/// 焦點變化之後要做什麼（#189）。
#[derive(Debug, PartialEq, Eq)]
enum FocusStep<C> {
    /// 繼續組字。
    Keep,
    /// 等訊息佇列空下來再確認（settle_focus）。帶著這次的原文件的 context，記下來當送出時的最後退路。
    Wait(Option<C>),
    /// 送出組字並清掉欄位狀態。組字送到組字範圍本身的 context（宿主可能已經把組字搬到別的文件）；拿不到時才用這個
    /// （這次焦點變化的原文件的 context），再拿不到就用焦點移到 NULL 時記下的（follow_focus）。
    Leave(Option<C>),
}

/// 執行緒的焦點文件改變（ITfThreadMgrEventSink::OnSetFocus）時進行中的組字怎麼辦。`previous` 是原文件的 context（原文件是
/// NULL 時為 None），`can_wait`：有回呼視窗可以稍後確認；沒有就照舊立即送出。泛型只為了能在測試裡用一般型別代替
/// ITfDocumentMgr（T）與 ITfContext（C）。
///
/// 送出的目標以組字範圍為準，A→NULL→B 在確認前換到 B 也一樣；焦點移到 NULL 前的原文件只是最後的退路：記下的原文件可能不是
/// 組字現在所在的文件（確認前宿主把組字搬走，發版前 code review），但組字範圍失效、拿不到 context 時沒有它字就沒地方送（第三輪）。
fn focus_step<T: PartialEq, C>(
    focus: Option<&T>,
    owner: Option<&T>,
    previous: Option<C>,
    can_wait: bool,
) -> FocusStep<C> {
    match focus_change(focus, owner) {
        FocusChange::CompositionMoved => FocusStep::Keep,
        FocusChange::Undecided if can_wait => FocusStep::Wait(previous),
        _ => FocusStep::Leave(previous),
    }
}

/// 焦點移到 NULL 之後佇列空下來（settle_focus）：焦點回到組字所在的文件就繼續組字，否則送出（到組字範圍的 context）。
fn settle_step<T: PartialEq, C>(current: Option<&T>, owner: Option<&T>) -> FocusStep<C> {
    match focus_change(current, owner) {
        FocusChange::CompositionMoved => FocusStep::Keep,
        _ => FocusStep::Leave(None),
    }
}

/// 同一個畫面版本最多因 OnLayoutChange 重查幾次。
const MAX_REQUERIES: u32 = 8;

/// 換欄位時，這段時間內已從引擎得知「輸出簡體字」（#169）就不再問：一次點擊或切換視窗常連著好幾次焦點變化，
/// 而從別處切換後再點回這個程式總要超過這個時間。
const SIMPLIFIED_FRESH: Duration = Duration::from_secs(1);

/// 重送舊畫面（OnLayoutChange 後移位置）之前拿掉 0c-ui 已自己收起的提示：0c-ui 在 NOTICE_MS 後收起提示，
/// DLL 不會知道；照舊重送會讓已收起的提示（或只有提示的視窗）在捲動、改視窗大小時又跳出來並重新計時。
/// 回傳拿掉之後還有沒有東西可畫（只剩提示就不再重送）。
fn drop_expired_notice(message: &mut UiMessage, age: Duration) -> bool {
    if notice_expires(message) && age >= Duration::from_millis(NOTICE_MS.into()) {
        message.notice.clear();
    }
    has_content(message)
}

impl State {
    /// 保留風格需要的 Shift＋Space 與 Caps 字母語意；預判和送鍵共用。
    fn styled_key(&mut self, vk: u32, lparam: isize, vocab: bool, english: bool) -> Option<String> {
        let key = keys::to_key(vk, lparam, vocab, english)?;
        let shift = keys::shift_down();
        let caps = keys::caps_lock_on();
        let shift_space = key == " " && shift && self.session.style_shift_space();
        let caps_lower = caps && self.session.style_caps_lower();
        Some(keys::style_key(key, shift, caps, shift_space, caps_lower))
    }

    fn vocab_keys(&self) -> bool {
        self.session.vocab_keys() && !self.session.english()
    }

    /// 這一鍵是不是要交給引擎的 Ctrl＋標點（#168）：中文模式、引擎可用而且回報 ctrl_punctuation（認得鍵名、設定開著）。
    /// 先看按鍵本身，不是 Ctrl＋標點鍵就不問引擎；Ctrl+: ( )（#231）還要引擎的 ctrl_punctuation_keys 列了它。
    /// TestKeyDown 與 OnKeyDown 用同一個答案；是的話不先送出組字。
    fn ctrl_punct_key(&mut self, vk: u32, lparam: isize) -> Option<&'static str> {
        if self.session.english() {
            return None;
        }
        let key = keys::ctrl_punct(vk, lparam)?;
        (self.session.available() && self.session.ctrl_punctuation(key)).then_some(key)
    }

    /// 這一鍵是不是要攔下來切換「輸出簡體字」的 Ctrl＋Shift＋F11（#192）：引擎可用而且回報 simplified_hotkey
    /// （設定開著、能即時切換）。先看按鍵本身，不是這個鍵就不問引擎。是的話不先送出組字（同選單，組字不動）。
    /// TestKeyDown 問（test_simplified_hotkey），OnKeyDown 沿用同一個答案（key_simplified_hotkey）。
    fn simplified_hotkey(&mut self, vk: u32) -> bool {
        keys::simplified_hotkey(vk) && self.session.available() && self.session.simplified_hotkey()
    }

    fn test_simplified_hotkey(&mut self, vk: u32) -> bool {
        let decision = self.simplified_hotkey(vk);
        self.hotkey_tested.record(vk, decision);
        decision
    }

    /// `tested`：OnKeyDown 一進來就從 hotkey_tested 取走的 TestKeyDown 決定；沒有時才問。
    fn key_simplified_hotkey(&mut self, vk: u32, tested: Option<bool>) -> bool {
        tested.unwrap_or_else(|| self.simplified_hotkey(vk))
    }

    fn tap(&mut self, side: keys::Side) -> &mut keys::ShiftTap {
        match side {
            keys::Side::Left => &mut self.left_shift,
            keys::Side::Right => &mut self.right_shift,
        }
    }

    /// 這次 Shift 放開是否切換：單按（中間沒有別的鍵、沒點滑鼠）、設定開啟這一側、而且沒超過時限。只有單按時才向引擎查設定。
    fn shift_toggles(&mut self, side: keys::Side, modified: bool, now: u64) -> bool {
        let mouse = keys::mouse_state();
        if !self.tap(side).solo(modified, mouse) {
            return false;
        }
        let config = self.session.shift_config();
        config.toggles(side) && self.tap(side).tap(modified, mouse, now, config.tap_ms)
    }

    fn reset_shift(&mut self) {
        self.right_shift = keys::ShiftTap::default();
        self.left_shift = keys::ShiftTap::default();
    }
}

impl TextService {
    pub fn new() -> TextService {
        crate::object_created();
        TextService {
            state: RefCell::new(State {
                thread_mgr: None,
                client_id: 0,
                thread_mgr_cookie: TF_INVALID_COOKIE,
                composition: None,
                ending_composition: Rc::new(Cell::new(false)),
                session: Session::new(),
                composing: false,
                attr_atoms: [0; KINDS.len()],
                eaten: HashSet::new(),
                ui: UiClient::new(),
                right_shift: keys::ShiftTap::default(),
                left_shift: keys::ShiftTap::default(),
                secure: false,
                ui_less: false,
                activate_flags: 0,
                callback: None,
                revision: 0,
                selection: None,
                langbar: None,
                shown: None,
                anchor: None,
                recent: None,
                layout_sink: None,
                notes: caret::Notes::default(),
                openclose: None,
                openclose_value: 0,
                left_from: None,
                hotkey_tested: keys::Tested::default(),
                key_tested: keys::Tested::default(),
            }),
            applying: Cell::new(0),
            layout_missed: Cell::new(false),
        }
    }
}

impl Drop for TextService {
    fn drop(&mut self) {
        crate::object_destroyed();
    }
}

impl TextService_Impl {
    fn composition_sink(&self) -> ITfCompositionSink {
        self.to_interface()
    }

    /// 焦點內的最上層 context。
    fn focused_context(&self) -> Option<ITfContext> {
        let tm = self.state.borrow().thread_mgr.clone()?;
        unsafe { tm.GetFocus().ok()?.GetTop().ok() }
    }

    /// 把快照套用到 context：更新組字區／提交。回傳是否吞掉按鍵。
    fn present(&self, ctx: Option<&ITfContext>, snapshot: &Snapshot) -> bool {
        let eaten = self.present_in_edit(ctx, snapshot, None);
        self.sync_langbar();
        eaten
    }

    /// 輸入指示器按鈕的左鍵：切換中英（同右 Shift 單按）。
    pub(crate) fn toggle_mode(&self) {
        let ctx = self.focused_context();
        let snapshot = self.state.borrow_mut().session.toggle();
        self.present(ctx.as_ref(), &snapshot);
    }

    /// 選單的「輸出簡體字」（#169）：在這個程式的 session 請引擎即時切換，組字不動；快照帶的提示
    /// 「已切換為輸出簡體字／繁體字」照一般提示顯示在游標旁、幾秒後收起，指示器跟著換成「简」／「中」。
    /// 走的是打字用的同一條引擎管道，沙箱宿主（搜尋框等）也可以。引擎不能即時切換或沒回應時回傳 false。
    pub(crate) fn set_output_simplified(&self, on: bool) -> bool {
        let ctx = self.focused_context();
        let snapshot = {
            let Ok(mut st) = self.state.try_borrow_mut() else { return false };
            if !st.session.available() {
                return false;
            }
            st.session.output_simplified(on)
        };
        if !snapshot.ok || !snapshot.handled {
            return false;
        }
        self.present(ctx.as_ref(), &snapshot);
        true
    }

    /// 鍵盤開關 compartment 改變（#117）。Windows 繁中的 Ctrl＋Space 是系統的輸入法開關熱鍵（輸入語言熱鍵
    /// IME_THOTKEY_IME_NONIME_TOGGLE），系統收到後翻轉這個值，按鍵本身不會送到 key event sink；外殼把每次翻轉當成
    /// 一次中英切換（同 Shift 單按，組字中保留組字）。值本身不代表中英：外殼從來不設定它（啟用後通常是 0），
    /// 在 OnChange 裡改回原值也會被拒（E_UNEXPECTED），所以只跟著翻轉、不去改它。
    /// TIP 啟用、換程式、應用程式自己開關輸入法時系統也可能改這個值，那時 Ctrl＋Space 沒有按著，只記下新值、不切換，
    /// 每個程式各自的中英狀態不受影響。使用者在系統的「輸入語言熱鍵」拿掉這個熱鍵後，Ctrl＋Space 就回到應用程式。
    fn keyboard_openclose_changed(&self) {
        let Ok(mut st) = self.state.try_borrow_mut() else {
            // 自己的處理中途（例如 edit session 裡）收到通知：不是使用者按的熱鍵。
            return;
        };
        let Some((compartment, _)) = st.openclose.clone() else {
            return;
        };
        let value = compartment_i32(&compartment).unwrap_or(0);
        let previous = std::mem::replace(&mut st.openclose_value, value);
        drop(st);
        if value == previous {
            return;
        }
        let hotkey = keys::ctrl_space_down();
        zdebug!(
            "鍵盤開關 {} -> {}{}",
            previous,
            value,
            if hotkey { "（Ctrl＋Space）" } else { "" }
        );
        if !hotkey {
            return;
        }
        // 不用 allowed()：它要同步唯讀鎖，Word、Outlook 收到熱鍵當下拒絕（TS_E_SYNCHRONOUS），被當成擋下的欄位，
        // 不但沒切換還清掉組字狀態（#164）。切換不送按鍵、不讀文件，只在確定擋下的欄位不切換。
        let ctx = self.focused_context();
        let (secure, client, ui_less) = {
            let st = self.state.borrow();
            (st.secure, st.client_id, st.ui_less)
        };
        if !secure && privacy::hotkey_allowed(ctx.as_ref(), client, ui_less) {
            self.toggle_mode();
        }
    }

    /// 第一個按鍵前確定這個程式的中英模式（#160）：攔不攔按鍵（wants_key、english_key_policy）看的是外殼記得的模式，
    /// 新開的程式還沒問過引擎時先問。Shift 等修飾鍵不必（切換的快照本身就帶模式）。
    fn learn_mode(&self, vk: u32) {
        if keys::is_modifier(vk) {
            return;
        }
        let changed = {
            let mut st = self.state.borrow_mut();
            st.session.available() && st.session.learn_mode(true)
        };
        if changed {
            self.sync_langbar();
        }
    }

    /// Caps Lock 開著時直接打英文（#173）：按鍵當下的鎖定狀態跟引擎記得的不同時告訴引擎；開啟時引擎先送出組字、
    /// 切到英文，關閉時回到原本的模式，組字區與中／英指示照快照更新。TestKeyDown 與 OnKeyDown 都呼叫，第二次狀態已相同、
    /// 不再送，兩者看到的模式一樣；Caps Lock 鍵本身在按下時（系統已切換鎖定狀態）與放開時（保險）各同步一次。
    /// 設定關閉或舊引擎時什麼都不做，注音模式照舊不理 Caps Lock（#168）。
    fn sync_caps(&self, ctx: Option<&ITfContext>, vk: u32) {
        let on = keys::caps_lock_on();
        let snapshot = {
            let mut st = self.state.borrow_mut();
            if vk == keys::VK_CAPS_LOCK {
                st.session.refresh_caps_lock_english(); // 設定剛打開、引擎重啟後的第一次 Caps Lock
            }
            if !st.session.caps_needs_sync(on) || !st.session.available() {
                return;
            }
            st.session.caps_lock(on)
        };
        zdebug!("Caps Lock {} -> english={}", on, snapshot.english);
        self.present(ctx, &snapshot);
    }

    /// 開輸入指示器選單前重新確認「輸出簡體字」（#169）：設定程式或別的程式剛切換過、這個程式還沒有新快照時，
    /// 勾選與「简」／「中」也要對。只用已經在執行的引擎；宿主正在處理別的事（狀態借用中）時略過。
    pub(crate) fn recheck_simplified(&self) {
        let changed = match self.state.try_borrow_mut() {
            Ok(mut st) => st.session.refresh_simplified(),
            Err(_) => false,
        };
        if changed {
            self.sync_langbar();
        }
    }

    fn sync_langbar(&self) {
        let st = self.state.borrow();
        if let Some((_, button)) = st.langbar.as_ref() {
            crate::langbar::set_state(button, st.session.english(), st.session.simplified());
        }
    }

    fn present_in_edit(
        &self,
        ctx: Option<&ITfContext>,
        snapshot: &Snapshot,
        cookie: Option<u32>,
    ) -> bool {
        if !snapshot.ok {
            return false;
        }
        let (composition, client_id, atom, ending, ui_less) = {
            let st = self.state.borrow();
            (
                st.composition.clone(),
                st.client_id,
                st.attr_atoms,
                st.ending_composition.clone(),
                st.ui_less,
            )
        };
        // 提示（#123「已加入詞庫：…」等）跟候選一起顯示；沒有候選時單獨顯示，下一鍵的快照沒有提示就收起。
        // 自行繪製候選的宿主（UI-less，例如遊戲）不另外跳出提示視窗。
        let notice = if ui_less { String::new() } else { snapshot.notice() };
        // 讀音提示（#162，中英混打打 20 時的 ㄉㄢ）同理：UI-less 宿主不另外跳出視窗。
        let reading = if ui_less { String::new() } else { snapshot.reading_hint.clone() };
        let show = snapshot.composing()
            && (!snapshot.candidates.is_empty() || !snapshot.suggestion.is_empty() || !reading.is_empty())
            || !notice.is_empty();
        let applied = match ctx {
            Some(ctx) => {
                let _applying = Applying::enter(&self.applying, &self.layout_missed);
                edit::apply(
                    ctx,
                    client_id,
                    composition,
                    self.composition_sink(),
                    snapshot,
                    atom,
                    cookie,
                    ending,
                    show,
                )
            }
            None => {
                if !snapshot.commit.is_empty() {
                    zlog!("沒有 context 可提交，提交文字遺失");
                }
                edit::Applied {
                    composition: None,
                    caret: Err(edit::Miss::NoContext),
                }
            }
        };
        // 宿主視窗決定候選窗畫在哪裡（沙箱與沉浸式介面在宿主行程內畫），也是位置退路的依據。
        let view = ctx.and_then(|c| unsafe { c.GetActiveView() }.ok());
        let owner = view.as_ref().and_then(|v| unsafe { v.GetWnd() }.ok());
        if let Ok(rect) = applied.caret {
            // 每一鍵查到的位置都記下（不只顯示候選的鍵），版面晚到時的退路才不會落後好幾個字。
            let mut st = self.state.borrow_mut();
            st.anchor = Some(rect);
            if let Some(h) = owner.filter(|h| !h.is_invalid()) {
                st.recent = Some((h, rect));
            }
        }
        zdebug!("located {:?}", applied.caret.map(|r| (r.left, r.top, r.right, r.bottom)));
        let placed = if show { self.place(&applied.caret, view.as_ref(), owner) } else { None };
        let caret = placed.map(|(r, _)| r);
        let (target, revision) = {
            let mut st = self.state.borrow_mut();
            st.revision = st.revision.wrapping_add(1).max(1);
            st.selection = if show {
                ctx.map(|c| (c.clone(), snapshot.candidates.iter().map(|c| c.0).collect()))
            } else {
                None
            };
            (
                st.callback.as_ref().map(|w| w.handle()).unwrap_or(0),
                st.revision,
            )
        };
        let message = UiMessage {
            show: show && caret.is_some(),
            x: caret.map(|r| r.left).unwrap_or(0),
            y: caret.map(|r| r.top).unwrap_or(0),
            w: caret.map(|r| r.right - r.left).unwrap_or(0),
            h: caret.map(|r| r.bottom - r.top).unwrap_or(0),
            candidates: snapshot
                .candidates
                .iter()
                .enumerate()
                .map(|(i, (id, text))| UiCandidate {
                    id: *id,
                    text: text.clone(),
                    note: snapshot.candidate_notes.get(i).cloned().unwrap_or_default(),
                    key: Some(snapshot.candidate_keys.get(i).cloned().unwrap_or_default()),
                })
                .collect(),
            highlight: snapshot.highlight,
            page: snapshot.page + 1,
            pages: snapshot.pages,
            columns: snapshot.columns,
            kind: snapshot.candidate_kind.clone(),
            title: snapshot.candidate_title.clone(),
            suggestion: snapshot.suggestion.clone(),
            notice,
            marking: snapshot.marking(),
            reading,
            owner: std::process::id(),
            target,
            revision,
        };
        let document = ctx.and_then(|c| unsafe { c.GetDocumentMgr() }.ok());
        {
            let mut st = self.state.borrow_mut();
            // 核心已經是快照的狀態；文件端沒套用成功時（只是更新組字）保留原組字物件，下一鍵再同步。
            st.composing = snapshot.composing();
            st.composition = applied.composition;
            st.ui.present(&message, owner, document.clone());
            if !st.composing {
                st.anchor = None;
                st.left_from = None;
            }
            st.shown = match ctx.filter(|_| show) {
                Some(ctx) => Some(Shown {
                    ctx: ctx.clone(),
                    cursor: snapshot.cursor_utf16,
                    message,
                    owner,
                    document,
                    exact: placed.is_some_and(|(_, source)| source == caret::Source::Exact),
                    requeries: 0,
                    since: Instant::now(),
                }),
                None => None,
            };
        }
        // 版面晚到（Chromium）時由 OnLayoutChange 補上位置；沒有候選就不必收通知。
        self.watch_layout(if show { ctx } else { None });
        // 套用期間略過的版面通知：現在 shown 與 revision 已是這一鍵的，位置不是準確的就補查一次。
        let exact = placed.is_some_and(|(_, source)| source == caret::Source::Exact);
        if self.layout_missed.take() && show && !exact && self.applying.get() == 0 {
            self.requery(ctx);
        }
        snapshot.handled
    }

    /// 候選窗的位置：GetTextExt 的結果，拿不到就依序退到系統插入點、同一次組字上一次的位置、
    /// 這個視窗上一次的位置或輸入框範圍（caret.rs）。每種「拿不到」與退到哪裡只在 tsf.log 記一次。
    fn place(
        &self,
        located: &edit::Located,
        view: Option<&ITfContextView>,
        view_window: Option<HWND>,
    ) -> Option<(RECT, caret::Source)> {
        let (previous, recent) = {
            let st = self.state.borrow();
            let recent = st.recent.filter(|(h, _)| Some(*h) == view_window).map(|(_, r)| r);
            (st.anchor, recent)
        };
        let placed = caret::choose(
            located.ok(),
            || caret::system_caret(view_window),
            previous,
            || caret::around_field(caret::field(view, view_window), recent, caret::line_height(view_window, recent)),
        );
        let mut st = self.state.borrow_mut();
        if let Err(miss) = located {
            let to = placed.map(|(_, source)| source.name()).unwrap_or("none");
            st.notes.once(&format!("{}→{}", miss.class(), to), || {
                let window = view_window.map(|h| h.0 as usize).unwrap_or(0);
                Some(match placed {
                    Some((r, source)) => format!(
                        "{}，改用{} ({},{},{},{})（view {:#x}）",
                        miss,
                        source.name(),
                        r.left,
                        r.top,
                        r.right,
                        r.bottom,
                        window
                    ),
                    None => format!("{}，也沒有其他位置可用，先不顯示、等版面更新（view {:#x}）", miss, window),
                })
            });
        }
        if placed.is_some() {
            // 每個 TSF 執行緒只查一次：前景視窗與焦點的查詢只為了診斷，不在每鍵的熱路徑上重做。
            st.notes.once("foreground", || {
                let foreground = unsafe { GetForegroundWindow() };
                let mut pid = 0;
                unsafe { GetWindowThreadProcessId(foreground, Some(&mut pid)) };
                // WebView2 這類宿主：前景視窗屬於另一個行程，0c-ui 要靠焦點視窗判斷（#144）。
                (pid != std::process::id()).then(|| {
                    let focus = zuin_ui::candidate::foreground_belongs_to(std::process::id(), foreground);
                    format!("前景視窗屬於其他行程，鍵盤焦點{}在本行程", if focus { "" } else { "不" })
                })
            });
        }
        zdebug!("caret {:?}", placed.map(|(r, s)| (r.left, r.top, r.right, r.bottom, s)));
        placed
    }

    /// 在有候選窗的 context 上註冊 ITfTextLayoutSink；換 context 或沒有候選時取消。
    fn watch_layout(&self, ctx: Option<&ITfContext>) {
        let current = self.state.borrow_mut().layout_sink.take();
        if let Some((watched, cookie)) = current {
            if Some(&watched) == ctx {
                self.state.borrow_mut().layout_sink = Some((watched, cookie));
                return;
            }
            if let Ok(source) = watched.cast::<ITfSource>() {
                let _ = unsafe { source.UnadviseSink(cookie) };
            }
        }
        let Some(ctx) = ctx else { return };
        let sink: ITfTextLayoutSink = self.to_interface();
        match ctx
            .cast::<ITfSource>()
            .and_then(|source| unsafe { source.AdviseSink(&ITfTextLayoutSink::IID, &sink) })
        {
            Ok(cookie) => self.state.borrow_mut().layout_sink = Some((ctx.clone(), cookie)),
            Err(e) => self
                .state
                .borrow_mut()
                .notes
                .once(&format!("advise {:#x}", e.code().0), || Some(format!("無法註冊 OnLayoutChange：{}", e.code()))),
        }
    }

    /// 收起候選窗並停止收版面通知（失焦、換欄位、組字被終止、停用）。
    fn forget_candidates(&self) {
        {
            let mut st = self.state.borrow_mut();
            st.shown = None;
            st.anchor = None;
        }
        self.watch_layout(None);
    }

    /// 版面變了：另開唯讀 edit session 重查目前候選窗的游標位置（ctx 要是顯示候選的那個）。
    fn requery(&self, ctx: Option<&ITfContext>) {
        let request = {
            // 其他地方正借用狀態（不該發生在宿主回呼裡，保險起見）就不重查。
            let Ok(mut st) = self.state.try_borrow_mut() else { return };
            let st = &mut *st;
            // 自行繪製候選的宿主（UI-less）不用我們的候選窗位置。
            if st.ui_less {
                return;
            }
            let Some(shown) = st.shown.as_mut() else { return };
            if ctx != Some(&shown.ctx) || shown.requeries >= MAX_REQUERIES {
                return;
            }
            shown.requeries += 1;
            (shown.ctx.clone(), st.client_id, st.composition.clone(), shown.cursor, st.revision)
        };
        let (ctx, client, composition, cursor, revision) = request;
        let service = self.to_object();
        let requested = edit::request_locate(
            &ctx,
            client,
            composition,
            cursor,
            Box::new(move |located| service.relocated(revision, located)),
        );
        if let Err(e) = requested {
            self.state.borrow_mut().notes.once(&format!("requery {:#x}", e.code().0), || {
                Some(format!("OnLayoutChange 後無法重查位置：{}", e.code()))
            });
        }
    }

    /// OnLayoutChange 之後的唯讀 edit session 查到的位置：還是同一個畫面版本、位置變了才補送。
    fn relocated(&self, revision: u32, located: edit::Located) {
        if self.applying.get() > 0 {
            // 排隊的唯讀 session 在我們的 apply 裡被執行（宿主釋放寫入鎖時接著處理佇列）：
            // revision 與 shown 還是上一鍵的，交給 present_in_edit 套用完再補查。
            self.layout_missed.set(true);
            return;
        }
        let Ok(mut st) = self.state.try_borrow_mut() else { return };
        let Ok(rect) = located else { return };
        if st.revision != revision {
            return;
        }
        let st = &mut *st;
        let Some(shown) = st.shown.as_mut() else { return };
        if !drop_expired_notice(&mut shown.message, shown.since.elapsed()) {
            st.shown = None;
            return;
        }
        let moved = (shown.message.x, shown.message.y, shown.message.w, shown.message.h)
            != (rect.left, rect.top, rect.right - rect.left, rect.bottom - rect.top);
        if shown.exact && shown.message.show && !moved {
            return;
        }
        let first = !shown.exact;
        shown.exact = true;
        shown.message.show = true;
        shown.message.x = rect.left;
        shown.message.y = rect.top;
        shown.message.w = rect.right - rect.left;
        shown.message.h = rect.bottom - rect.top;
        if st.composing {
            st.anchor = Some(rect);
        }
        if let Some(h) = shown.owner.filter(|h| !h.is_invalid()) {
            st.recent = Some((h, rect));
        }
        st.ui.present(&shown.message, shown.owner, shown.document.clone());
        if first {
            st.notes.once("relocated", || Some("版面更新（OnLayoutChange）後取得游標位置".into()));
        }
    }

    pub(crate) fn select_candidate(&self, revision: u32, id: i64) {
        let target = {
            let st = self.state.borrow();
            st.selection
                .as_ref()
                .filter(|(_, ids)| revision == st.revision && ids.contains(&id))
                .map(|(ctx, _)| (ctx.clone(), st.client_id))
        };
        if let Some((ctx, client)) = target {
            crate::candidate::request(self, &ctx, client, revision, id);
        }
    }

    pub(crate) fn select_in_edit(&self, ec: u32, revision: u32, id: i64) {
        let ctx = {
            let st = self.state.borrow();
            let Some((ctx, ids)) = st.selection.as_ref() else {
                return;
            };
            if st.secure || revision != st.revision || !ids.contains(&id) {
                return;
            }
            (ctx.clone(), st.ui_less)
        };
        let (ctx, ui_less) = ctx;
        // 前景是本行程的視窗，或前景執行緒的焦點在本行程（WebView2 宿主，#144）。
        if !zuin_ui::candidate::foreground_belongs_to(std::process::id(), unsafe { GetForegroundWindow() })
            || self.focused_context().as_ref() != Some(&ctx)
            || privacy::blocked_in_edit(&ctx, ec, ui_less)
        {
            return;
        }
        let snapshot = self
            .state
            .borrow_mut()
            .session
            .handle(json!({"type": "select", "index": id}));
        self.present_in_edit(Some(&ctx), &snapshot, Some(ec));
    }

    /// 新組字的第一鍵之前：游標仍緊接著剛提交的文字，才讓引擎以它作為前文。
    fn confirm_document_context(&self, ctx: Option<&ITfContext>) {
        let (composing, client) = {
            let st = self.state.borrow();
            (st.composing, st.client_id)
        };
        if composing {
            return;
        }
        let Some((id, commit)) = self.state.borrow_mut().session.take_pending_context() else {
            return;
        };
        let expected: Vec<u16> = commit.encode_utf16().collect();
        let confirmed = ctx.is_some_and(|c| context::preceded_by(c, client, &expected));
        zdebug!("document context confirmed={}", confirmed);
        self.state
            .borrow_mut()
            .session
            .confirm_context(id, confirmed);
    }

    fn handle_key(&self, ctx: Option<&ITfContext>, key: &str) -> bool {
        let snapshot = self.state.borrow_mut().session.key(key);
        zdebug!(
            "snapshot ok={} handled={} composing={} commit_len={}",
            snapshot.ok,
            snapshot.handled,
            snapshot.composing(),
            snapshot.commit.chars().count()
        );
        self.present(ctx, &snapshot)
    }

    /// OnTestKeyDown 已經說要吃、OnKeyDown 卻沒吃成的鍵（#216）：WPF 等宿主看 TestKeyDown 的答案就不再自己處理，
    /// 不補送就整個不見。只在沒在組字的注音模式發生：引擎正在重啟（改設定套用、更新交接、當掉後）而且等到期限仍連不上，
    /// 或新引擎以新設定不處理這個鍵（例如剛改成許氏的數字）。以沒有輸入法時會打出的字送進文件（照 Shift 與 Caps Lock）；
    /// 轉不成可見字元或按著 Ctrl／Alt 時照舊交還宿主。
    fn type_literal(&self, ctx: Option<&ITfContext>, vk: u32, lparam: isize) -> bool {
        if keys::modifier_down() {
            return false;
        }
        let Some(text) = keys::printable(vk, lparam, true) else {
            return false;
        };
        zlog!("TestKeyDown 已吃下的鍵引擎沒有處理，以原字元送進文件"); // 不記鍵碼（隱私）
        zdebug!("type_literal vk={}", vk);
        let literal = Snapshot {
            ok: true,
            handled: true,
            commit: text,
            ..Default::default()
        };
        let eaten = self.present(ctx, &literal);
        if eaten {
            self.state.borrow_mut().eaten.insert(vk);
        }
        eaten
    }

    /// 結束目前組字：commit（失焦、停用）或 cancel（應用程式自行終止組字）。
    fn finish(&self, ctx: Option<&ITfContext>, kind: &str) {
        let composing = self.state.borrow().composing;
        if !composing {
            return;
        }
        let snapshot = self
            .state
            .borrow_mut()
            .session
            .handle(json!({"type": kind}));
        self.present(ctx, &snapshot);
    }

    /// 進行中的組字所在的文件。宿主可能把組字搬到別的文件（Excel 的儲存格編輯器，#189），以組字範圍的 context 為準。
    fn composition_document(&self) -> Option<ITfDocumentMgr> {
        unsafe { self.composition_context()?.GetDocumentMgr().ok() }
    }

    /// 進行中的組字所在的 context；焦點變化時組字送到這裡（#189）。
    fn composition_context(&self) -> Option<ITfContext> {
        let st = self.state.borrow();
        if !st.composing {
            return None;
        }
        let composition = st.composition.as_ref()?;
        unsafe { composition.GetRange().and_then(|r| r.GetContext()).ok() }
    }

    /// 焦點移到 NULL 之後佇列空下來了：焦點回到組字所在的文件（宿主把組字交給新文件）就繼續組字，
    /// 否則照焦點移開時的做法送出到原文件。
    pub(crate) fn settle_focus(&self) {
        let current = self
            .state
            .borrow()
            .thread_mgr
            .as_ref()
            .and_then(|t| unsafe { t.GetFocus() }.ok());
        let owner = self.composition_document();
        let step = settle_step(current.as_ref(), owner.as_ref());
        self.follow_focus(step, current.is_some());
    }

    /// 照 focus_step／settle_step 的決定繼續組字、稍後確認或送出組字；`focused`：焦點落在某個文件（不是 NULL）。
    fn follow_focus(&self, step: FocusStep<ITfContext>, focused: bool) {
        if let Some(window) = self.state.borrow().callback.as_ref() {
            if matches!(step, FocusStep::Wait(_)) {
                window.settle_focus_later();
            } else {
                window.cancel_settle_focus();
            }
        }
        match step {
            FocusStep::Keep => {
                self.state.borrow_mut().left_from = None;
                zdebug!("焦點落在組字所在的文件，繼續組字");
            }
            FocusStep::Wait(previous) => {
                // 記下組字現在所在的 context（拿不到就用這次的原文件）；連續兩次移到 NULL 時保留第一次的。
                let from = self.composition_context().or(previous);
                let mut st = self.state.borrow_mut();
                if st.left_from.is_none() {
                    st.left_from = from;
                }
                zdebug!("組字中焦點移到 NULL，稍後確認");
            }
            FocusStep::Leave(previous) => {
                let left_from = self.state.borrow_mut().left_from.take();
                let target = self.composition_context().or(previous).or(left_from);
                self.leave_document(target.as_ref(), focused);
            }
        }
    }

    /// 換欄位：把進行中的組字提交到原欄位（與 Fcitx 5 外殼的 deactivate 相同），清掉候選與這個欄位的狀態。
    fn leave_document(&self, previous: Option<&ITfContext>, focused: bool) {
        self.finish(previous, "commit");
        self.forget_candidates();
        let changed = {
            let mut st = self.state.borrow_mut();
            st.reset_shift();
            st.session.reset();
            st.selection = None;
            st.ui.hide();
            // 全部共用（#115）時別的程式可能已經切換：引擎在執行就重新問，中／英指示跟著換（不為此啟動引擎）。
            // 「輸出簡體字」也可能剛在設定程式或別的程式切換過（#169）：指示器的「简」跟著換。焦點離開文件（focus 為
            // NULL）時不問；SIMPLIFIED_FRESH 之內剛從引擎得知（剛打字、同一次點擊的前一次焦點變化）也不問，
            // 不在宿主的 UI 執行緒上每次多一次同步 IPC。開選單時另外照常確認（recheck_simplified）。
            let mode = st.session.learn_mode(false);
            let simplified = focused && st.session.refresh_simplified_after(SIMPLIFIED_FRESH);
            mode || simplified
        };
        if changed {
            self.sync_langbar();
        }
    }

    /// 每次按鍵重新確認欄位；阻擋路徑不記錄鍵碼、不連線／重播引擎。
    fn allowed(&self, ctx: Option<&ITfContext>) -> bool {
        let (secure, client, ui_less) = {
            let st = self.state.borrow();
            (st.secure, st.client_id, st.ui_less)
        };
        let field = if secure {
            privacy::Field::Blocked
        } else {
            privacy::field(ctx, client, ui_less)
        };
        if field != privacy::Field::Blocked {
            self.state
                .borrow_mut()
                .session
                .set_private(field == privacy::Field::Private);
            return true;
        }
        let composition = self.state.borrow().composition.clone();
        if let Some(comp) = composition {
            let original = unsafe { comp.GetRange().and_then(|r| r.GetContext()).ok() };
            self.present(
                original.as_ref(),
                &Snapshot {
                    ok: true,
                    ..Default::default()
                },
            );
        }
        self.forget_candidates();
        let mut st = self.state.borrow_mut();
        st.reset_shift();
        st.selection = None;
        st.eaten.clear();
        st.ui.hide();
        st.session.reset();
        st.composing = false;
        st.composition = None;
        st.left_from = None;
        false
    }

    /// Commit before declining TestKeyDown: hosts such as WPF rely on this
    /// answer and cannot recover a key declined only in OnKeyDown.
    fn english_key_policy(&self, ctx: Option<&ITfContext>, vk: u32, lparam: isize) -> Option<bool> {
        if keys::is_modifier(vk) && !keys::modifier_down() {
            return None;
        }
        let snap = {
            let mut st = self.state.borrow_mut();
            if !st.session.english() || !st.session.available() {
                return None;
            }
            if !st.composing {
                st.session.handle(json!({"type": "display", "columns": 0}))
            } else {
                st.session.english_state.clone()
            }
        };
        let caps_lower = snap.caps_lock && self.state.borrow_mut().session.style_caps_lower();
        if !snap.ok || (!snap.english_autocomplete && !snap.full_width && !caps_lower) || snap.composing() && !snap.english_pending {
            return None;
        }
        if keys::modifier_down() {
            if snap.english_pending {
                self.finish(ctx, "commit");
            }
            return Some(false);
        }
        let key = self.state.borrow_mut().styled_key(vk, lparam, false, true);
        if key.as_deref() == Some("ShiftSpace") { return Some(true); }
        // IMM32 程式的焦點不是文字框（Photoshop 的畫布）：英文模式的按鍵是快捷鍵，整個交給程式，不開始補字（#210）。
        let flags = self.state.borrow().activate_flags;
        if !snap.english_pending && ctx.is_some_and(|c| privacy::english_passthrough(c, flags)) {
            crate::log::once(&format!(
                "英文模式：IMM32 程式的焦點沒有插入點，不開始英文補字，按鍵直接交給程式 exe={}",
                crate::log::exe_name()
            ));
            return Some(false);
        }
        if key.as_ref().is_some_and(|k| snap.english_wants_key(k) || caps_lower && k.len() == 1 && k.as_bytes()[0].is_ascii_alphabetic()) {
            return Some(true);
        }
        if let Some(key) = key {
            let next = self.state.borrow_mut().session.key(&key);
            // Also inform the core about punctuation/whitespace in idle English
            // mode so a URL or identifier cannot restart completion mid-token.
            if next.ok && (!next.commit.is_empty() || snap.english_pending) {
                self.present(ctx, &next);
            }
            return Some(next.handled);
        }
        if snap.english_pending {
            self.finish(ctx, "commit");
        }
        Some(false)
    }

    /// 組字中交給宿主的按鍵（Ctrl／Alt／Win 組合鍵、F 鍵、Insert 等核心轉換不了的鍵）先送出組字（#106）。
    /// 必須在 TestKeyDown 就做：WPF 等宿主只看 TestKeyDown 的答案，說要吃之後 OnKeyDown 再回不吃，按鍵就消失；
    /// 先送出組字也讓 Ctrl+Enter、Ctrl+V 這類快捷鍵在組字之後才執行。OnKeyDown 再呼叫一次時已沒有組字，不會重複。
    fn commit_before_host_key(&self, ctx: Option<&ITfContext>, vk: u32, lparam: isize) {
        if !self.state.borrow().composing {
            return;
        }
        // 要交給引擎的 Ctrl＋標點插進組字區，不先送出組字（#168）。
        if keys::modifier_down() && self.state.borrow_mut().ctrl_punct_key(vk, lparam).is_some() {
            return;
        }
        let protocol = keys::to_key(vk, lparam, false, true); // 只看轉不轉得成協定按鍵，與新鍵名、大小寫無關
        if keys::commits_before_host(vk, keys::modifier_down(), protocol.as_deref()) {
            self.finish(ctx, "commit");
        }
    }

    /// 追蹤右 Shift 與左 Shift 是否為單按與按下的時間；Shift 按下本身一律交給宿主（is_modifier）。
    fn observe_shift(&self, vk: u32, lparam: isize) {
        let repeat = lparam & (1 << 30) != 0;
        let modified = keys::modifier_down();
        let mut st = self.state.borrow_mut();
        let now = keys::now_ms();
        let (right, left) = (keys::right_shift(vk, lparam), keys::left_shift(vk, lparam));
        let mouse = keys::mouse_state();
        st.right_shift.observe(right, repeat, modified, now, mouse);
        st.left_shift.observe(left, repeat, modified, now, mouse);
        // Shift 以外的任何按鍵（包含不問核心就交還宿主的快捷鍵與英文字母）都讓下一次切換不再還原注音（#90）。
        if !right && !left {
            st.session.bypassed();
        }
    }

    /// Shift 放開（#117、#217）：單按、設定開啟這一側、沒超過時限就切換中英，回傳有沒有切換。
    /// 在第一次收到這次放開時判斷並結束這次按放（一般是 OnTestKeyUp；宿主沒先問就直接呼叫 OnKeyUp 時在那裡），
    /// 同一次放開之後再來的 OnTestKeyUp／OnKeyUp 不會再切一次。呼叫端一律不吃放開：Shift 按下本來就交給應用程式，
    /// 放開也照樣交給它（同系統注音與新酷音）。以前吃掉放開、等 OnKeyUp 才切換，IMM32 程式要對放開呼叫
    /// TranslateMessage 才會有 OnKeyUp，PuTTY 從不呼叫，Shift 就切不了；吃掉的放開在程式那裡也變成 VK_PROCESSKEY。
    fn shift_released(&self, ctx: Option<&ITfContext>, side: keys::Side) -> bool {
        let toggle = {
            let mut st = self.state.borrow_mut();
            let toggle = st.shift_toggles(side, keys::modifier_down(), keys::now_ms());
            *st.tap(side) = keys::ShiftTap::default();
            toggle
        };
        zdebug!("Shift 放開 side={:?} toggle={}", side, toggle);
        if toggle {
            let snapshot = self.state.borrow_mut().session.toggle();
            self.present(ctx, &snapshot);
        }
        toggle
    }

    fn wants_key(&self, vk: u32, lparam: isize) -> bool {
        // Ctrl＋標點（#168）：中文模式而且引擎回報 ctrl_punctuation 時吃，組字中或沒在組字都一樣；核心插進組字區。
        if keys::modifier_down() && self.state.borrow_mut().ctrl_punct_key(vk, lparam).is_some() {
            return true;
        }
        if keys::modifier_down() || keys::is_modifier(vk) {
            return false;
        }
        let mut st = self.state.borrow_mut();
        // server 不可用時什麼都不吃：OnTestKeyDown 一旦說要吃，有些宿主（WPF）就不再自己處理這個鍵，
        // 之後 OnKeyDown 回傳不吃也救不回來，按鍵會憑空消失。
        if !st.session.available() {
            return false;
        }
        // 組字中轉得成協定按鍵的鍵都問核心：Home／End／↑／PgUp／PgDn／Tab／Shift+Tab（BackTab）核心都回 handled，
        // 只有英文模式的 ↓ 回未處理；轉不成的鍵（F 鍵、Insert…）已由 commit_before_host_key 先送出組字（#106）。
        // 這裡的答案與 OnKeyDown 的結果一致，WPF 不會掉鍵。Shift＋←→ Delete Backspace 的新鍵名（#123）
        // 核心在組字中同樣都回 handled（沒有對應功能時就是原本的鍵）。
        let english = st.session.english();
        let style_key = st.styled_key(vk, lparam, false, english);
        if style_key.as_deref() == Some("ShiftSpace") { return true; }
        if style_key.as_ref().is_some_and(|k| st.session.english_state.full_width_extra_key(k)) { return true; }
        if st.composing {
            let english = st.session.english();
            let vocab = st.vocab_keys();
            return keys::composing_wants(english, st.styled_key(vk, lparam, vocab, english).as_deref());
        }
        if st.session.english() {
            return false;
        }
        // 沒在組字：具名鍵、空白與數字鍵盤交還應用程式；可能產生可見 ASCII 的鍵（數字、字母、OEM 符號）才問核心
        if keys::named(vk).is_some() || keys::is_keypad(vk) {
            return false;
        }
        keys::printable(vk, lparam, false) // 中文模式不理 Caps Lock（#168）
            .map(|key| {
                // 排列與風格都可能改變閒置時攔哪些字元；包含 Shift 字母及標點。
                // idle_keys 依連線快取，不會每鍵往返。
                st.session.idle_key(&key)
            })
            .unwrap_or(false)
    }
}

impl ITfTextInputProcessor_Impl for TextService_Impl {
    fn Activate(&self, ptim: Ref<'_, ITfThreadMgr>, tid: u32) -> windows_core::Result<()> {
        crate::guarded("Activate", || {
            // 開頭與結尾各記一行：宿主在 Activate 中途崩潰時，紀錄只有開頭（#89）。
            zlog!("activate 開始 exe={} tid={}{}", crate::log::exe_name(), tid, if crate::layout::IS_32BIT { " x86" } else { "" });
            let tm = ptim.ok()?.clone();
            let callback = CandidateWindow::new(self).ok();
            unsafe {
                let keystrokes: ITfKeystrokeMgr = tm.cast()?;
                let key_sink: ITfKeyEventSink = self.to_interface();
                keystrokes.AdviseKeyEventSink(tid, &key_sink, true)?;
                let source: ITfSource = tm.cast()?;
                let tm_sink: ITfThreadMgrEventSink = self.to_interface();
                let cookie = source
                    .AdviseSink(&ITfThreadMgrEventSink::IID, &tm_sink)
                    .unwrap_or(TF_INVALID_COOKIE);
                // 鍵盤開關（Ctrl＋Space，#117）；拿不到就只是沒有這個熱鍵。
                let openclose = tm
                    .cast::<ITfCompartmentMgr>()
                    .and_then(|mgr| mgr.GetCompartment(&GUID_COMPARTMENT_KEYBOARD_OPENCLOSE))
                    .ok()
                    .and_then(|compartment| {
                        let sink: ITfCompartmentEventSink = self.to_interface();
                        let cookie = compartment
                            .cast::<ITfSource>()
                            .and_then(|s| s.AdviseSink(&ITfCompartmentEventSink::IID, &sink))
                            .ok()?;
                        Some((compartment, cookie))
                    });
                let openclose_value = openclose
                    .as_ref()
                    .and_then(|(c, _)| compartment_i32(c))
                    .unwrap_or(0);
                // 不用 CoCreateInstance：COM-less 啟用（TF_TMAE_COMLESS，#157）的宿主可能沒有初始化 COM。
                let category = crate::msctf::create_category_mgr().ok();
                let atom = KINDS.map(|kind| {
                    category
                        .as_ref()
                        .and_then(|c| c.RegisterGUID(&kind.guid()).ok())
                        .unwrap_or(0)
                });
                let mut st = self.state.borrow_mut();
                st.callback = callback;
                st.thread_mgr = Some(tm);
                st.client_id = tid;
                st.thread_mgr_cookie = cookie;
                st.attr_atoms = atom;
                st.openclose = openclose;
                st.openclose_value = openclose_value;
                // 候選窗程式不在就先啟動，但不在宿主 UI 執行緒上等它（#89）；第一次顯示候選時才連線。
                st.ui.warm_up();
                // 只有以 UI-less 啟用的執行緒（自行繪製候選的遊戲等）把候選交給宿主；一般程式即使回 pbShow=FALSE
                // （例如清掉了 ISC_SHOWUICANDIDATEWINDOW 的 IMM32 程式）也照常顯示靈犀的候選窗。
                let elements = st
                    .thread_mgr
                    .as_ref()
                    .filter(|_| st.ui_less)
                    .and_then(|tm| tm.cast::<ITfUIElementMgr>().ok())
                    .map(Elements::new);
                st.ui.set_elements(elements);
                // 新開的程式從開始時的中英模式起（#160）：引擎已在執行就先問，中／英指示一開始就對；不為此啟動引擎。
                st.session.learn_mode(false);
                let english = st.session.english();
                let simplified = st.session.simplified();
                drop(st);
                // 輸入指示器的中／英按鈕與選單（#13）。按鈕持有本服務的參考，Deactivate 時移除以解開循環。
                let tm = self.state.borrow().thread_mgr.clone();
                if let Some(Ok(mgr)) = tm.map(|tm| tm.cast::<ITfLangBarItemMgr>()) {
                    let service = self.to_object();
                    let simplify = self.to_object();
                    let recheck = self.to_object();
                    let button = crate::langbar::LangBarButton::create(
                        english,
                        simplified,
                        Box::new(move || service.toggle_mode()),
                        Box::new(move |on| simplify.set_output_simplified(on)),
                        Box::new(move || recheck.recheck_simplified()),
                    );
                    if let Ok(item) = button.cast::<ITfLangBarItem>() {
                        if mgr.AddItem(&item).is_ok() {
                            self.state.borrow_mut().langbar = Some((mgr, button));
                        }
                    }
                }
            }
            zlog!(
                "activate 完成 exe={} tid={} ui_less={}",
                crate::log::exe_name(),
                tid,
                self.state.borrow().ui_less
            );
            Ok(())
        })
    }

    fn Deactivate(&self) -> windows_core::Result<()> {
        crate::guarded("Deactivate", || {
            let ctx = self.focused_context();
            self.finish(ctx.as_ref(), "commit");
            self.forget_candidates();
            let (tm, tid, cookie) = {
                let mut st = self.state.borrow_mut();
                st.callback = None;
                st.composition = None;
                st.composing = false;
                st.left_from = None;
                st.eaten.clear();
                st.reset_shift();
                st.selection = None;
                st.ui.hide();
                st.ui.set_elements(None);
                (st.thread_mgr.take(), st.client_id, st.thread_mgr_cookie)
            };
            let openclose = self.state.borrow_mut().openclose.take();
            if let Some((compartment, cookie)) = openclose {
                if let Ok(source) = compartment.cast::<ITfSource>() {
                    unsafe {
                        let _ = source.UnadviseSink(cookie);
                    }
                }
            }
            let langbar = self.state.borrow_mut().langbar.take();
            if let Some((mgr, button)) = langbar {
                if let Ok(item) = button.cast::<ITfLangBarItem>() {
                    unsafe {
                        let _ = mgr.RemoveItem(&item);
                    }
                }
            }
            if let Some(tm) = tm {
                unsafe {
                    if let Ok(keystrokes) = tm.cast::<ITfKeystrokeMgr>() {
                        let _ = keystrokes.UnadviseKeyEventSink(tid);
                    }
                    if cookie != TF_INVALID_COOKIE {
                        if let Ok(source) = tm.cast::<ITfSource>() {
                            let _ = source.UnadviseSink(cookie);
                        }
                    }
                }
            }
            zlog!("deactivate");
            Ok(())
        })
    }
}

impl ITfTextInputProcessorEx_Impl for TextService_Impl {
    fn ActivateEx(
        &self,
        ptim: Ref<'_, ITfThreadMgr>,
        tid: u32,
        flags: u32,
    ) -> windows_core::Result<()> {
        crate::guarded("ActivateEx", || {
            {
                let mut st = self.state.borrow_mut();
                st.secure = flags & TF_TMAE_SECUREMODE != 0;
                st.ui_less = flags & TF_TMAE_UIELEMENTENABLEDONLY != 0;
                st.activate_flags = flags;
            }
            zlog!("activate flags={:#x} comless={}", flags, flags & TF_TMAE_COMLESS != 0);
            self.Activate(ptim, tid)
        })
    }
}

impl ITfKeyEventSink_Impl for TextService_Impl {
    fn OnSetFocus(&self, foreground: BOOL) -> windows_core::Result<()> {
        crate::guarded("OnSetFocus", || {
            if !foreground.as_bool() {
                {
                    let mut st = self.state.borrow_mut();
                    st.reset_shift();
                    st.selection = None;
                    st.ui.hide();
                }
                self.forget_candidates();
            }
            Ok(())
        })
    }

    fn OnTestKeyDown(
        &self,
        pic: Ref<'_, ITfContext>,
        wparam: WPARAM,
        lparam: LPARAM,
    ) -> windows_core::Result<BOOL> {
        crate::guarded("OnTestKeyDown", || {
            {
                // 上一次按鍵的決定不留給這一次（keys::Tested）
                let mut st = self.state.borrow_mut();
                st.hotkey_tested.forget();
                st.key_tested.forget();
            }
            if !self.allowed(pic.as_ref()) {
                return Ok(false.into());
            }
            let vk = wparam.0 as u32;
            self.observe_shift(vk, lparam.0);
            // 用快捷鍵切換簡體字（#192）：設定開著時吃掉，OnKeyDown 才切換；不先送出組字。
            if self.state.borrow_mut().test_simplified_hotkey(vk) {
                zdebug!("OnTestKeyDown vk={} -> 切換簡體字", vk);
                return Ok(true.into());
            }
            self.commit_before_host_key(pic.as_ref(), vk, lparam.0);
            self.learn_mode(vk);
            self.sync_caps(pic.as_ref(), vk);
            let wants = self
                .english_key_policy(pic.as_ref(), vk, lparam.0)
                .unwrap_or_else(|| self.wants_key(vk, lparam.0));
            zdebug!("OnTestKeyDown vk={} -> {}", wparam.0, wants);
            self.state.borrow_mut().key_tested.record(vk, wants);
            Ok(wants.into())
        })
    }

    fn OnTestKeyUp(
        &self,
        pic: Ref<'_, ITfContext>,
        wparam: WPARAM,
        lparam: LPARAM,
    ) -> windows_core::Result<BOOL> {
        crate::guarded("OnTestKeyUp", || {
            {
                let mut st = self.state.borrow_mut();
                st.hotkey_tested.forget();
                st.key_tested.forget();
            }
            if !self.allowed(pic.as_ref()) {
                return Ok(false.into());
            }
            let vk = wparam.0 as u32;
            if vk == keys::VK_CAPS_LOCK {
                self.sync_caps(pic.as_ref(), 0); // #173：按下時沒同步到的話（宿主時序不同），放開時一定已切換
            }
            // Shift 放開在這裡就切換、不吃（#217）：IMM32 程式（PuTTY）不一定讓 OnKeyUp 來。
            if let Some(side) = keys::shift_side(vk, lparam.0) {
                self.shift_released(pic.as_ref(), side);
                return Ok(false.into());
            }
            Ok(self.state.borrow().eaten.contains(&vk).into())
        })
    }

    fn OnKeyDown(
        &self,
        pic: Ref<'_, ITfContext>,
        wparam: WPARAM,
        lparam: LPARAM,
    ) -> windows_core::Result<BOOL> {
        crate::guarded("OnKeyDown", || {
            let vk = wparam.0 as u32;
            // TestKeyDown 的決定一進來就取走，在 allowed() 提早回傳時也不留給之後的按鍵（keys::Tested）。
            let (tested, key_tested) = {
                let mut st = self.state.borrow_mut();
                (st.hotkey_tested.take(vk), st.key_tested.take(vk))
            };
            if !self.allowed(pic.as_ref()) {
                return Ok(false.into());
            }
            self.observe_shift(vk, lparam.0);
            // 用快捷鍵切換簡體字（#192）：跟輸入指示器選單的「輸出簡體字」同一條路，組字不動、提示「已切換為…」、
            // 工作列「中」↔「简」。沿用 TestKeyDown 的決定（不再問引擎）：說要吃就一定吃，切換失敗（引擎剛好斷線）也照樣吃掉，
            // 按鍵不會半途交給應用程式（F11 在瀏覽器、Excel 是全螢幕），放開時 OnTestKeyUp 看 eaten 也一致。
            if self.state.borrow_mut().key_simplified_hotkey(vk, tested) {
                let on = !self.state.borrow().session.simplified().unwrap_or(false);
                let switched = self.set_output_simplified(on);
                zdebug!("OnKeyDown vk={} 切換簡體字 on={} ok={}", vk, on, switched);
                self.state.borrow_mut().eaten.insert(vk);
                return Ok(true.into());
            }
            self.commit_before_host_key(pic.as_ref(), vk, lparam.0);
            self.learn_mode(vk);
            self.sync_caps(pic.as_ref(), vk);
            // TestKeyDown 吃下、沒在組字的注音模式按鍵，最後沒吃成就以原字元補送（type_literal，#216）。
            let literal_fallback = key_tested == Some(true) && {
                let st = self.state.borrow();
                !st.composing && !st.session.english()
            };
            if !self
                .english_key_policy(pic.as_ref(), vk, lparam.0)
                .unwrap_or_else(|| self.wants_key(vk, lparam.0))
            {
                zdebug!("OnKeyDown vk={} wants=false", vk);
                if literal_fallback {
                    return Ok(self.type_literal(pic.as_ref(), vk, lparam.0).into());
                }
                return Ok(false.into());
            }
            let (vocab, english) = {
                let st = self.state.borrow();
                (st.vocab_keys(), st.session.english())
            };
            // Ctrl＋標點（#168）與 wants_key 同一個判斷；其他鍵照舊，中文模式不理 Caps Lock。
            let ctrl = if keys::modifier_down() {
                self.state.borrow_mut().ctrl_punct_key(vk, lparam.0)
            } else {
                None
            };
            let Some(key) = ctrl
                .map(str::to_string)
                .or_else(|| self.state.borrow_mut().styled_key(vk, lparam.0, vocab, english))
            else {
                return Ok(false.into());
            };
            self.confirm_document_context(pic.as_ref());
            let mut eaten = self.handle_key(pic.as_ref(), &key);
            if !eaten && literal_fallback && !self.state.borrow().composing {
                eaten = self.type_literal(pic.as_ref(), vk, lparam.0);
            }
            zdebug!("OnKeyDown vk={} handled={}", vk, eaten);
            if eaten {
                self.state.borrow_mut().eaten.insert(vk);
            }
            Ok(eaten.into())
        })
    }

    fn OnKeyUp(
        &self,
        pic: Ref<'_, ITfContext>,
        wparam: WPARAM,
        lparam: LPARAM,
    ) -> windows_core::Result<BOOL> {
        crate::guarded("OnKeyUp", || {
            {
                let mut st = self.state.borrow_mut();
                st.hotkey_tested.forget();
                st.key_tested.forget();
            }
            if !self.allowed(pic.as_ref()) {
                return Ok(false.into());
            }
            if let Some(side) = keys::shift_side(wparam.0 as u32, lparam.0) {
                // 一般已在 OnTestKeyUp 切換過；宿主沒先問 OnTestKeyUp 就直接呼叫 OnKeyUp 時在這裡切換。
                self.shift_released(pic.as_ref(), side);
                return Ok(false.into());
            }
            Ok(self
                .state
                .borrow_mut()
                .eaten
                .remove(&(wparam.0 as u32))
                .into())
        })
    }

    fn OnPreservedKey(
        &self,
        _pic: Ref<'_, ITfContext>,
        _guid: *const GUID,
    ) -> windows_core::Result<BOOL> {
        crate::guarded("OnPreservedKey", || Ok(false.into()))
    }
}

impl ITfCompositionSink_Impl for TextService_Impl {
    fn OnCompositionTerminated(
        &self,
        _ec: u32,
        composition: Ref<'_, ITfComposition>,
    ) -> windows_core::Result<()> {
        crate::guarded("OnCompositionTerminated", || {
            {
                let st = self.state.borrow();
                // 正常 EndComposition 也可能同步回呼；不得把已提交／新組字當成外部取消。
                // 舊組字的延後通知同樣不能清掉目前欄位的新組字。
                if st.ending_composition.get()
                    || st.composition.is_none()
                    || st.composition.as_ref() != composition.as_ref()
                {
                    return Ok(());
                }
            }
            // 應用程式或系統終止了組字（例如滑鼠點到別處）：組字文字留在文件裡，核心放棄本次組字、不學習。
            // 之後的 Enter、方向鍵交還宿主（例如 RichEdit 2.0 讀取 WM_GETTEXT 時會結束組字，#88）。
            zlog!("組字被宿主終止 exe={}", crate::log::exe_name());
            {
                let mut st = self.state.borrow_mut();
                st.composition = None;
                st.composing = false;
                st.left_from = None;
                st.ui.hide();
            }
            self.forget_candidates();
            let _ = self
                .state
                .borrow_mut()
                .session
                .handle(json!({"type": "cancel"}));
            Ok(())
        })
    }
}

impl ITfThreadMgrEventSink_Impl for TextService_Impl {
    fn OnInitDocumentMgr(&self, _pdim: Ref<'_, ITfDocumentMgr>) -> windows_core::Result<()> {
        crate::guarded("OnInitDocumentMgr", || Ok(()))
    }
    fn OnUninitDocumentMgr(&self, _pdim: Ref<'_, ITfDocumentMgr>) -> windows_core::Result<()> {
        crate::guarded("OnUninitDocumentMgr", || Ok(()))
    }
    fn OnSetFocus(
        &self,
        focus: Ref<'_, ITfDocumentMgr>,
        previous: Ref<'_, ITfDocumentMgr>,
    ) -> windows_core::Result<()> {
        crate::guarded("OnSetFocus", || {
            let previous = previous.as_ref().and_then(|d| unsafe { d.GetTop().ok() });
            let owner = self.composition_document();
            zdebug!(
                "OnSetFocus focus={} previous={} composing={}",
                if focus.is_some() { "document" } else { "NULL" },
                if previous.is_some() { "context" } else { "none" },
                owner.is_some()
            );
            let can_wait = self.state.borrow().callback.is_some();
            let step = focus_step(focus.as_ref(), owner.as_ref(), previous, can_wait);
            self.follow_focus(step, focus.as_ref().is_some());
            Ok(())
        })
    }
    fn OnPushContext(&self, _pic: Ref<'_, ITfContext>) -> windows_core::Result<()> {
        crate::guarded("OnPushContext", || Ok(()))
    }
    fn OnPopContext(&self, _pic: Ref<'_, ITfContext>) -> windows_core::Result<()> {
        crate::guarded("OnPopContext", || Ok(()))
    }
}

impl ITfTextLayoutSink_Impl for TextService_Impl {
    /// 宿主的版面變了（Chromium：renderer 送回剛打的字的位置）。有候選窗時另開唯讀 edit session 重查游標，
    /// 位置不同或之前沒有位置才補送給候選窗（#144）。
    fn OnLayoutChange(
        &self,
        pic: Ref<'_, ITfContext>,
        lcode: TfLayoutCode,
        _view: Ref<'_, ITfContextView>,
    ) -> windows_core::Result<()> {
        crate::guarded("OnLayoutChange", || {
            if lcode == TF_LC_DESTROY {
                return Ok(());
            }
            // 我們的 apply 進行中（宿主在寫入時或剛寫完就同步通知）：shown 與 revision 還是上一鍵的，
            // 現在重查會把舊候選畫到新位置。記下來，present_in_edit 套用完再補查。
            if self.applying.get() > 0 {
                self.layout_missed.set(true);
                return Ok(());
            }
            self.requery(pic.as_ref());
            Ok(())
        })
    }
}

impl ITfCompartmentEventSink_Impl for TextService_Impl {
    fn OnChange(&self, guid: *const GUID) -> windows_core::Result<()> {
        crate::guarded("OnCompartmentChange", || {
            if !guid.is_null() && unsafe { *guid } == GUID_COMPARTMENT_KEYBOARD_OPENCLOSE {
                self.keyboard_openclose_changed();
            }
            Ok(())
        })
    }
}

/// compartment 的 VT_I4 值；沒有值（VT_EMPTY）或讀不到回傳 None。
fn compartment_i32(compartment: &ITfCompartment) -> Option<i32> {
    unsafe {
        let mut value = compartment.GetValue().ok()?;
        let out = (value.Anonymous.Anonymous.vt == VT_I4)
            .then(|| value.Anonymous.Anonymous.Anonymous.lVal);
        let _ = VariantClear(&mut value);
        out
    }
}

impl ITfDisplayAttributeProvider_Impl for TextService_Impl {
    fn EnumDisplayAttributeInfo(&self) -> windows_core::Result<IEnumTfDisplayAttributeInfo> {
        crate::guarded("EnumDisplayAttributeInfo", || {
            Ok(ComObject::new(AttributeEnum::new()).into_interface())
        })
    }
    fn GetDisplayAttributeInfo(
        &self,
        guid: *const GUID,
    ) -> windows_core::Result<ITfDisplayAttributeInfo> {
        crate::guarded("GetDisplayAttributeInfo", || {
            if let Some(kind) = KINDS
                .into_iter()
                .find(|kind| !guid.is_null() && unsafe { *guid } == kind.guid())
            {
                Ok(ComObject::new(InputAttribute::new(kind)).into_interface())
            } else {
                Err(E_INVALIDARG.into())
            }
        })
    }
}

#[cfg(test)]
mod layout_tests {
    use super::*;
    use windows::Win32::UI::TextServices::TF_LC_CHANGE;

    #[test]
    fn expired_notice_is_not_sent_again_when_the_layout_changes() {
        let notice = UiMessage { show: true, notice: "已加入詞庫：世傑".into(), ..Default::default() };
        let fresh = Duration::from_millis(100);
        let expired = Duration::from_millis(NOTICE_MS as u64 + 1);
        // 還在顯示的提示照常跟著移動。
        let mut m = notice.clone();
        assert!(drop_expired_notice(&mut m, fresh));
        assert_eq!(m.notice, notice.notice);
        // 修正前：0c-ui 已收起的提示在捲動時又跳出來（每個畫面版本最多 8 次）。只有提示就不再重送。
        let mut m = notice.clone();
        assert!(!drop_expired_notice(&mut m, expired));
        // 有候選時只拿掉提示，候選照常移位置。
        let mut m = UiMessage {
            candidates: vec![UiCandidate { id: 1, text: "世傑".into(), ..Default::default() }],
            ..notice.clone()
        };
        assert!(drop_expired_notice(&mut m, expired));
        assert!(m.notice.is_empty() && m.candidates.len() == 1);
        // 標示中的說明不計時（留到標示結束），不拿掉。
        let mut m = UiMessage { marking: true, ..notice.clone() };
        assert!(drop_expired_notice(&mut m, expired));
        assert_eq!(m.notice, notice.notice);
    }

    #[test]
    fn layout_change_during_apply_waits_for_the_new_snapshot() {
        // 修正前：apply 進行中沒有借用狀態，try_borrow_mut 的防護無效，會拿上一鍵的 shown 與 revision 重查、
        // 把舊候選畫到新位置。現在記下來，等 present_in_edit 套用完再補查。
        let service = ComObject::new(TextService::new());
        let sink: ITfTextLayoutSink = service.to_interface();
        {
            let _applying = Applying::enter(&service.applying, &service.layout_missed);
            unsafe { sink.OnLayoutChange(None, TF_LC_CHANGE, None).unwrap() };
            assert!(service.layout_missed.get());
            // 排隊的唯讀 session 在 apply 裡被執行：同樣不重送。
            service.relocated(0, Ok(RECT { left: 1, top: 2, right: 3, bottom: 4 }));
        }
        assert_eq!(service.applying.get(), 0);
        // 下一次 apply 開始時清掉舊的記號。
        let _applying = Applying::enter(&service.applying, &service.layout_missed);
        assert!(!service.layout_missed.get());
    }
}

#[cfg(test)]
mod focus_tests {
    use super::*;

    #[test]
    fn composition_moved_by_the_host_keeps_composing() {
        // #189：Excel 的儲存格在「就緒」時打 2（ㄉ）。組字在試算表的文件 grid 開始，Excel 先把焦點移到 NULL、
        // 再交給儲存格編輯器 editor，組字一起搬過去。修正前第一次焦點變化就把 ㄉ 送出，得到「ㄉ億一次」（0.2.1 是「2億一次」）。
        let (grid, editor) = ("grid", "editor");
        assert_eq!(focus_change(None, Some(&grid)), FocusChange::Undecided);
        assert_eq!(focus_change(Some(&editor), Some(&editor)), FocusChange::CompositionMoved);
        // 焦點移開又回到同一個文件（宿主短暫移開焦點），組字仍在那裡：繼續組字。
        assert_eq!(focus_change(Some(&grid), Some(&grid)), FocusChange::CompositionMoved);
    }

    #[test]
    fn leaving_the_composition_document_still_commits() {
        // 換到別的欄位，組字沒有跟過去：照舊送出到原欄位。
        assert_eq!(focus_change(Some(&"other"), Some(&"field")), FocusChange::Left);
        // 沒在組字：照舊清掉這個欄位的狀態。
        assert_eq!(focus_change(Some(&"other"), None), FocusChange::Left);
        assert_eq!(focus_change::<&str>(None, None), FocusChange::Left);
    }

    #[test]
    fn excel_handover_waits_then_keeps_composing() {
        // #189：grid → NULL → editor，組字搬到 editor。等確認時焦點已在組字所在的文件：繼續組字。
        assert_eq!(focus_step(None, Some(&"grid"), Some("grid ctx"), true), FocusStep::Wait(Some("grid ctx")));
        assert_eq!(focus_step(Some(&"editor"), Some(&"editor"), None::<&str>, true), FocusStep::Keep);
        assert_eq!(settle_step(Some(&"editor"), Some(&"editor")), FocusStep::<&str>::Keep);
    }

    #[test]
    fn focus_to_null_then_another_document_commits() {
        // code review（#189 回歸）：組字中焦點 A → NULL → B，第二次焦點變化在確認之前（50ms 內）就來了。
        // 第二次的原文件是 NULL：照樣送出，目標是組字範圍本身的 context（follow_focus），不是這次的原文件。
        assert_eq!(focus_step(None, Some(&"A"), Some("A ctx"), true), FocusStep::Wait(Some("A ctx")));
        assert_eq!(focus_step(Some(&"B"), Some(&"A"), None::<&str>, true), FocusStep::Leave(None));
        // 確認時焦點仍不在組字所在的文件（A → NULL → NULL 也一樣）：送出。
        assert_eq!(settle_step(Some(&"B"), Some(&"A")), FocusStep::<&str>::Leave(None));
        assert_eq!(settle_step(None, Some(&"A")), FocusStep::<&str>::Leave(None));
        // 沒有回呼視窗時不等，照舊立即送出。
        assert_eq!(focus_step(None, Some(&"A"), Some("A ctx"), false), FocusStep::Leave(Some("A ctx")));
    }

    #[test]
    fn switching_documents_directly_commits() {
        assert_eq!(focus_step(Some(&"B"), Some(&"A"), Some("A ctx"), true), FocusStep::Leave(Some("A ctx")));
        // 沒在組字（沒有組字範圍）：照舊清掉原文件的欄位狀態。
        assert_eq!(focus_step(Some(&"C"), None, Some("B ctx"), true), FocusStep::Leave(Some("B ctx")));
    }
}

#[cfg(test)]
mod composition_tests {
    use super::*;
    use windows::Win32::UI::TextServices::{ITfComposition_Impl, ITfRange};

    #[implement(ITfComposition)]
    struct TestComposition;
    impl ITfComposition_Impl for TestComposition_Impl {
        fn GetRange(&self) -> windows_core::Result<ITfRange> {
            Err(windows::Win32::Foundation::E_NOTIMPL.into())
        }
        fn ShiftStart(&self, _: u32, _: Ref<'_, ITfRange>) -> windows_core::Result<()> {
            Ok(())
        }
        fn ShiftEnd(&self, _: u32, _: Ref<'_, ITfRange>) -> windows_core::Result<()> {
            Ok(())
        }
        fn EndComposition(&self, _: u32) -> windows_core::Result<()> {
            Ok(())
        }
    }

    #[test]
    fn normal_end_and_stale_notifications_preserve_new_composition() {
        let Some(pipe) = std::env::var("ZUIN_TEST_PIPE").ok() else {
            return;
        };
        let service = ComObject::new(TextService::new());
        let current: ITfComposition = ComObject::new(TestComposition).into_interface();
        let stale: ITfComposition = ComObject::new(TestComposition).into_interface();
        {
            let mut st = service.state.borrow_mut();
            st.session = Session::with_client(crate::ipc::Client::with_pipe(&pipe));
            assert_eq!(st.session.key("s").text(), "ㄋ");
            st.composing = true;
            st.composition = Some(current.clone());
            st.ending_composition.set(true);
        }
        let sink: ITfCompositionSink = service.to_interface();
        unsafe {
            sink.OnCompositionTerminated(0, &current).unwrap();
        }
        assert!(service.state.borrow().composing);
        service.state.borrow().ending_composition.set(false);
        unsafe {
            sink.OnCompositionTerminated(0, &stale).unwrap();
        }
        assert!(service.state.borrow().composing);
        assert_eq!(service.state.borrow_mut().session.key("u").text(), "ㄋㄧ");
        unsafe {
            sink.OnCompositionTerminated(0, &current).unwrap();
        }
        assert!(!service.state.borrow().composing);
        assert_eq!(service.state.borrow_mut().session.key("u").text(), "ㄧ");
    }
}

#[cfg(test)]
#[path = "focus_host_tests.rs"]
mod focus_host_tests;

#[cfg(test)]
mod shift_tests {
    //! #217：PuTTY 這類 IMM32 程式對 Shift 放開只經 OnTestKeyUp 問輸入法；放開被吃掉後，要宿主對那個放開
    //! 呼叫 TranslateMessage 才會有 OnKeyUp，PuTTY 從不呼叫。以前在 OnKeyUp 才切換，單按左右 Shift 都切不了。
    //! 以一般欄位的假 context 照各種宿主的呼叫順序走 ITfKeyEventSink；需要 ZUIN_TEST_PIPE 的測試引擎（預設左右 Shift，#206）。
    use super::*;
    use windows::Win32::UI::TextServices::ITfKeyEventSink;
    use windows::Win32::UI::WindowsAndMessaging::DestroyWindow;

    const SHIFT: WPARAM = WPARAM(0x10);
    const RIGHT: isize = 0x36;
    const LEFT: isize = 0x2a;

    /// 按鍵訊息的 lParam：重複次數 1、掃描碼；放開時帶「先前按著」與「放開」兩個位元。
    fn lparam(scan: isize, up: bool) -> LPARAM {
        LPARAM(1 | scan << 16 | if up { 0xC000_0000u32 as i32 as isize } else { 0 })
    }

    fn test_down(sink: &ITfKeyEventSink, ctx: &ITfContext, vk: WPARAM, scan: isize) -> bool {
        unsafe { sink.OnTestKeyDown(ctx, vk, lparam(scan, false)) }.unwrap().as_bool()
    }
    fn test_up(sink: &ITfKeyEventSink, ctx: &ITfContext, vk: WPARAM, scan: isize) -> bool {
        unsafe { sink.OnTestKeyUp(ctx, vk, lparam(scan, true)) }.unwrap().as_bool()
    }
    fn key_up(sink: &ITfKeyEventSink, ctx: &ITfContext, vk: WPARAM, scan: isize) -> bool {
        unsafe { sink.OnKeyUp(ctx, vk, lparam(scan, true)) }.unwrap().as_bool()
    }

    #[test]
    fn shift_release_toggles_without_on_key_up_and_reaches_the_app() {
        let Some(pipe) = std::env::var("ZUIN_TEST_PIPE").ok() else {
            return;
        };
        let (view, ctx) = crate::privacy::host_tests::open_field();
        let service = ComObject::new(TextService::new());
        service.state.borrow_mut().session = Session::with_client(crate::ipc::Client::with_pipe(&pipe));
        let sink: ITfKeyEventSink = service.to_interface();
        let english = || service.state.borrow().session.english();
        let start = english();
        for (scan, side) in [(RIGHT, "右"), (LEFT, "左")] {
            // PuTTY：按下不吃，放開只有 OnTestKeyUp。修正前這裡回 true（吃掉）而且沒切換。
            assert!(!test_down(&sink, &ctx, SHIFT, scan));
            assert!(!test_up(&sink, &ctx, SHIFT, scan), "{side} Shift 放開交給程式");
            assert_ne!(english(), start, "{side} Shift 只有 OnTestKeyUp 也要切換");
            // 一般 TSF 宿主：OnTestKeyUp 之後宿主仍呼叫 OnKeyUp 也只切一次。
            assert!(!test_down(&sink, &ctx, SHIFT, scan));
            assert!(!test_up(&sink, &ctx, SHIFT, scan));
            assert!(!key_up(&sink, &ctx, SHIFT, scan));
            assert_eq!(english(), start, "{side} Shift 同一次放開不切兩次");
            // 同一次放開的 OnTestKeyUp 來兩次也只切一次。
            assert!(!test_down(&sink, &ctx, SHIFT, scan));
            assert!(!test_up(&sink, &ctx, SHIFT, scan));
            assert!(!test_up(&sink, &ctx, SHIFT, scan));
            assert_ne!(english(), start);
            // 宿主沒先問、直接 OnKeyDown／OnKeyUp：在 OnKeyUp 切換，放開一樣交給程式。
            unsafe { sink.OnKeyDown(&ctx, SHIFT, lparam(scan, false)) }.unwrap();
            assert!(!key_up(&sink, &ctx, SHIFT, scan));
            assert_eq!(english(), start, "{side} Shift 只有 OnKeyUp 也要切換");
        }
        // Shift＋方向鍵（選取）不切換：中間有別的鍵，放開不是單按。
        assert!(!test_down(&sink, &ctx, SHIFT, LEFT));
        assert!(!test_down(&sink, &ctx, WPARAM(0x25), 0x4b));
        assert!(!test_up(&sink, &ctx, SHIFT, LEFT));
        assert_eq!(english(), start, "Shift＋← 不切換");
        // 單按下一次照常判斷。
        assert!(!test_down(&sink, &ctx, SHIFT, RIGHT));
        assert!(!test_up(&sink, &ctx, SHIFT, RIGHT));
        assert_ne!(english(), start);
        unsafe {
            let _ = DestroyWindow(view);
        }
    }
}
