//! 在記錄鍵碼或接觸引擎之前檢查 TSF 欄位政策；不讀取文件文字。
use std::cell::Cell;
use windows::Win32::Foundation::{E_FAIL, E_NOTIMPL, HWND, LPARAM, WPARAM};
use windows::Win32::System::Com::CoTaskMemFree;

use windows::Win32::System::Variant::{VariantClear, VARIANT, VT_I4, VT_UNKNOWN};
use windows::Win32::UI::Accessibility::{
    AccessibleObjectFromWindow, IAccessible, ROLE_SYSTEM_CLIENT, ROLE_SYSTEM_DOCUMENT,
    ROLE_SYSTEM_TEXT,
};
use windows::Win32::UI::Controls::EM_GETPASSWORDCHAR;
// 這兩個 Oleacc 狀態在 windows crate 中共用 Controls 的具名常數。
const STATE_SYSTEM_FOCUSABLE: u32 = windows::Win32::UI::Controls::STATE_SYSTEM_FOCUSABLE.0;
const STATE_SYSTEM_UNAVAILABLE: u32 = windows::Win32::UI::Controls::STATE_SYSTEM_UNAVAILABLE.0;
use windows::Win32::UI::Input::KeyboardAndMouse::{GetFocus, IsWindowEnabled};
use windows::Win32::UI::TextServices::*;
use windows::Win32::UI::WindowsAndMessaging::{
    GetClassNameW, GetWindowLongW, GetWindowThreadProcessId, IsChild, SendMessageW, ES_PASSWORD,
    ES_READONLY, GWL_STYLE, OBJID_CLIENT, STATE_SYSTEM_FOCUSED, STATE_SYSTEM_PROTECTED,
    STATE_SYSTEM_READONLY,
};
use windows_core::{implement, ComObject, Interface};

/// 欄位政策。密碼、PIN、唯讀或無法判斷的欄位不送引擎；只標 IS_PRIVATE 的欄位
/// （Chrome 無痕／訪客、Firefox 私密視窗的所有欄位都是）照常組字，但整段組字以
/// private 送引擎：不學習、不補標點、不做神經重排，外殼也不讀前文。
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Field {
    Blocked,
    Private,
    Open,
}

/// `ui_less`：本執行緒以 TF_TMAE_UIELEMENTENABLEDONLY 啟用（自行繪製候選的宿主，例如遊戲，#86）。
pub fn field(ctx: Option<&ITfContext>, client: u32, ui_less: bool) -> Field {
    match ctx {
        Some(ctx) => probe(ctx, client, ui_less).unwrap_or(Field::Blocked),
        None => Field::Blocked,
    }
}

/// 系統的輸入法開關熱鍵（Ctrl＋Space，#117）能不能切換中英：切換不送按鍵、不讀文件，只在確定擋下的欄位
/// （唯讀、停用、密碼）不切換。宿主正在處理按鍵、不給同步唯讀鎖時（Word、Outlook 收到熱鍵當下回
/// TS_E_SYNCHRONOUS，#164）讀不到欄位政策，照常切換，同輸入指示器按鈕。
pub fn hotkey_allowed(ctx: Option<&ITfContext>, client: u32, ui_less: bool) -> bool {
    ctx.is_some_and(|ctx| toggle_allowed(probe(ctx, client, ui_less)))
}

fn toggle_allowed(probed: Option<Field>) -> bool {
    probed != Some(Field::Blocked)
}

/// 欄位政策；宿主拒絕同步唯讀 edit session（讀不到）時回 None。停用或唯讀的 context 直接是 Blocked。
fn probe(ctx: &ITfContext, client: u32, ui_less: bool) -> Option<Field> {
    unsafe {
        if context_disabled(ctx) {
            return Some(Field::Blocked);
        }
        crate::object_created();
        let probe = ComObject::new(ScopeProbe {
            ctx: ctx.clone(),
            field: Cell::new(Field::Blocked),
            ui_less,
        });
        let edit: ITfEditSession = probe.to_interface();
        match ctx.RequestEditSession(client, &edit, TF_ES_SYNC | TF_ES_READ) {
            Ok(hr) if hr.is_ok() => Some(probe.field.get()),
            Ok(hr) => {
                unreadable(hr);
                None
            }
            Err(error) => {
                unreadable(error.code());
                None
            }
        }
    }
}

/// 讀不到欄位政策（宿主拒絕同步唯讀鎖，或讀 InputScope 時出錯）：按鍵放行、熱鍵照常切換（#164）。
/// 預設在 tsf.log 每個行程、每種 HRESULT 記一次（#209）。
fn unreadable(hr: windows_core::HRESULT) {
    crate::log::once(&format!(
        "欄位政策：讀不到（{:?}），按鍵放行給程式、Ctrl＋Space 照常切換 exe={}",
        hr,
        crate::log::exe_name()
    ));
    crate::log::zdebug!("field policy unreadable={:?}", hr);
}

fn context_disabled(ctx: &ITfContext) -> bool {
    unsafe {
        if ctx
            .GetStatus()
            .map(|s| s.dwDynamicFlags & TF_SD_READONLY != 0)
            .unwrap_or(true)
        {
            return true;
        }
        if let Ok(mgr) = ctx.cast::<ITfCompartmentMgr>() {
            for guid in [
                GUID_COMPARTMENT_KEYBOARD_DISABLED,
                GUID_COMPARTMENT_EMPTYCONTEXT,
            ] {
                if let Ok(compartment) = mgr.GetCompartment(&guid) {
                    if let Ok(mut value) = compartment.GetValue() {
                        let disabled = value.Anonymous.Anonymous.vt == VT_I4
                            && value.Anonymous.Anonymous.Anonymous.lVal != 0;
                        let _ = VariantClear(&mut value);
                        if disabled {
                            return true;
                        }
                    }
                }
            }
        }
        false
    }
}

pub fn blocked_in_edit(ctx: &ITfContext, ec: u32, ui_less: bool) -> bool {
    if context_disabled(ctx) {
        return true;
    }
    crate::object_created();
    let probe = ComObject::new(ScopeProbe {
        ctx: ctx.clone(),
        field: Cell::new(Field::Blocked),
        ui_less,
    });
    let edit: ITfEditSession = probe.to_interface();
    unsafe { edit.DoEditSession(ec).is_err() || probe.field.get() == Field::Blocked }
}

/// 本執行緒目前的焦點視窗：必須是這個 context 的視窗（或其子視窗）、屬於本行程且可用。
fn focused_window(ctx: &ITfContext) -> Option<HWND> {
    unsafe {
        let focus = GetFocus();
        if focus.is_invalid() {
            return None;
        }
        let view = ctx.GetActiveView().and_then(|view| view.GetWnd()).ok()?;
        if view.is_invalid() || (view != focus && !IsChild(view, focus).as_bool()) {
            return None;
        }
        let mut pid = 0;
        GetWindowThreadProcessId(focus, Some(&mut pid));
        (pid == std::process::id() && IsWindowEnabled(focus).as_bool()).then_some(focus)
    }
}

/// 不讀文字，只檢查本執行緒目前焦點的原生控制項與可編輯文件。
/// Word 等文件編輯器沒有標準 EDIT 類別；InputScope 不可用時以 MSAA 的角色／狀態核對。
fn native_edit_allows(ctx: &ITfContext) -> bool {
    focused_window(ctx).is_some_and(|focus| native_edit_is_plain(focus) || accessible_edit_allows(focus))
}

/// 已知沒有 InputScope、MSAA 在行程內也查不到的文件畫布：OneNote 桌面版（2016、2024、Microsoft 365 是同一套程式）
/// 的頁面 OneNote::DocumentCanvas 讀 InputScope 回 E_FAIL；跨行程看它的 MSAA 是 PANE、焦點物件是 DOCUMENT，
/// 但在 OneNote 行程裡（輸入法所在）AccessibleObjectFromWindow 回 E_FAIL，不論在不在文件鎖裡，
/// 原本的原生／MSAA 檢查都核對不到，整個頁面被當成擋下的欄位，按鍵直接以英文進入、Shift 也切不了（#163）。
/// 頁面裡沒有密碼欄（密碼保護的節由另外的對話框輸入）；context 的停用／唯讀已先檢查，
/// 這裡再確認焦點屬於本 context、本行程且可用，MSAA 查得到時也不能標成保護、唯讀或停用。
fn document_canvas_allows(ctx: &ITfContext) -> bool {
    focused_window(ctx).is_some_and(|focus| {
        window_class(focus).is_some_and(|class| document_canvas(&class)) && !accessible_secret(focus)
    })
}

fn document_canvas(class: &str) -> bool {
    class == "OneNote::DocumentCanvas"
}

/// 缺 InputScope、也不是上面已知控制項的自訂欄位：IMM32 程式（系統相容層建立的暫時 context，TF_SS_TRANSITORY）、
/// 自行繪製候選的宿主（UI-less，例如英雄聯盟遊戲內聊天，#86），以及自己實作 TSF 文字儲存的程式——Unreal Engine 的
/// UnrealWindow（UE4、UE5 同一份 FTextStoreACP）的 RequestSupportedAttrs 回 S_OK 卻不提供任何屬性，讀 InputScope 回
/// E_FAIL，靜態旗標只有 TS_SS_NOHIDDENTEXT（WARDOGS、《二之國：交錯世界》的聊天框，#209、#210）。原本只放行前兩種，
/// 第三種整個被當成無法判斷而放行給宿主：按鍵直接以英文進入，Shift、Ctrl＋Space 也切不了中英。
/// UE 的密碼框同樣不停用輸入法、也不標 InputScope，其他輸入法（新酷音、小狼毫、PIME 只看停用／空 context）照常組字。
/// 焦點在本 context、本行程且可用，不是密碼／唯讀的標準編輯框，MSAA 也沒標成保護、唯讀或停用時，以 Private
/// 組字（不學習、不讀前文）。宿主在密碼欄等處取消輸入法關聯或改用空 context 時，context_disabled 已先擋下；
/// 標了 IS_PASSWORD 等的欄位讀得到 InputScope，不會走到這裡。
fn custom_field_allows(ctx: &ITfContext) -> bool {
    let Some(focus) = focused_window(ctx) else {
        return false;
    };
    if window_class(focus).is_some_and(|class| native_edit_class(&class)) {
        return native_edit_is_plain(focus);
    }
    !accessible_secret(focus)
}

/// Windows App SDK（WinUI 3）XAML island 的文字框：TextBox 沒設 InputScope 時讀取回 E_FAIL，
/// 焦點視窗是 island 的輸入視窗 InputSiteWindowClass，MSAA 只有沒有子物件的 CLIENT，
/// 原生／MSAA 檢查都核對不到而被擋，按鍵直接以英文進入欄位（23H2 起檔案總管的網址列與搜尋框、
/// 相片的搜尋框，#105）。XAML 的 PasswordBox 預設 InputScope 為 Password，會先由 secret_scope 擋下；
/// 唯讀、停用的文字框已由 context_disabled 擋下。這裡只再確認焦點屬於本 context、本行程且可用。
/// 宿主開了觸控鍵盤的文字預測或自動校正（TS_SS_TKB*，XAML TextBox 的預設）就是一般文字欄位，
/// 照常組字；沒開的（例如路徑或網址欄位關掉了預測）以私人方式組字（不學習、不讀前文）。
fn xaml_island_field(ctx: &ITfContext) -> Option<Field> {
    let class = focused_window(ctx).and_then(window_class)?;
    let flags = unsafe { ctx.GetStatus() }.ok()?.dwStaticFlags;
    island_field(&class, flags)
}

fn island_field(class: &str, static_flags: u32) -> Option<Field> {
    if class != "InputSiteWindowClass" {
        return None;
    }
    Some(if static_flags & (TS_SS_TKBPREDICTIONENABLE | TS_SS_TKBAUTOCORRECTENABLE) != 0 {
        Field::Open
    } else {
        Field::Private
    })
}

/// 除錯紀錄用：缺 InputScope 時記下 context 狀態旗標與焦點／檢視視窗類別（不含文字）。
fn missing_scope_context(ctx: &ITfContext) -> String {
    unsafe {
        let status = ctx
            .GetStatus()
            .map(|s| format!("{:#x}/{:#x}", s.dwStaticFlags, s.dwDynamicFlags))
            .unwrap_or_else(|e| format!("{:?}", e.code()));
        let focus = GetFocus();
        let view = ctx.GetActiveView().and_then(|view| view.GetWnd()).unwrap_or_default();
        format!(
            "status={} focus={} view={} same={}",
            status,
            window_class(focus).unwrap_or_default(),
            window_class(view).unwrap_or_default(),
            focused_window(ctx).is_some()
        )
    }
}

fn window_class(hwnd: HWND) -> Option<String> {
    let mut name = [0u16; 128];
    let len = unsafe { GetClassNameW(hwnd, &mut name) };
    (len > 0).then(|| String::from_utf16_lossy(&name[..len as usize]))
}

/// MSAA 標示為保護（密碼）、唯讀或停用。取不到 MSAA（多數自訂視窗）時不算。
fn accessible_secret(hwnd: HWND) -> bool {
    unsafe {
        let mut raw = std::ptr::null_mut();
        if AccessibleObjectFromWindow(hwnd, OBJID_CLIENT.0 as u32, &IAccessible::IID, &mut raw).is_err() || raw.is_null() {
            return false;
        }
        let accessible = IAccessible::from_raw(raw);
        let Ok(mut state) = accessible.get_accState(&VARIANT::from(0i32)) else {
            return false;
        };
        let secret = state.Anonymous.Anonymous.vt == VT_I4
            && state.Anonymous.Anonymous.Anonymous.lVal as u32
                & (STATE_SYSTEM_READONLY | STATE_SYSTEM_PROTECTED | STATE_SYSTEM_UNAVAILABLE)
                != 0;
        let _ = VariantClear(&mut state);
        secret
    }
}

fn editable_accessible(role: u32, state: u32, word_document: bool) -> bool {
    (matches!(role, ROLE_SYSTEM_DOCUMENT | ROLE_SYSTEM_TEXT)
        || (word_document && role == ROLE_SYSTEM_CLIENT))
        && state & (STATE_SYSTEM_FOCUSED | STATE_SYSTEM_FOCUSABLE)
            == STATE_SYSTEM_FOCUSED | STATE_SYSTEM_FOCUSABLE
        && state & (STATE_SYSTEM_READONLY | STATE_SYSTEM_PROTECTED | STATE_SYSTEM_UNAVAILABLE) == 0
}

fn accessible_edit_allows(hwnd: HWND) -> bool {
    unsafe {
        let mut raw = std::ptr::null_mut();
        if AccessibleObjectFromWindow(hwnd, OBJID_CLIENT.0 as u32, &IAccessible::IID, &mut raw)
            .is_err()
            || raw.is_null()
        {
            return false;
        }
        let accessible = IAccessible::from_raw(raw);
        // Word 2024 的文件 _WwG 在 MSAA 暴露 CLIENT（沒有子物件），
        // 而非 DOCUMENT／TEXT。只對這個已知文件控制項接受 CLIENT；
        // context 的唯讀／停用、焦點、密碼與 MSAA 狀態仍須全部通過。
        let mut class = [0u16; 128];
        let length = GetClassNameW(hwnd, &mut class);
        let word_document =
            length > 0 && class[..length as usize] == "_WwG".encode_utf16().collect::<Vec<_>>();
        // CHILDID_SELF=0；僅讀角色及狀態，不讀 Value／Name／文件文字。
        let child = VARIANT::from(0i32);
        let Ok(mut role) = accessible.get_accRole(&child) else {
            return false;
        };
        let mut state = match accessible.get_accState(&child) {
            Ok(state) => state,
            Err(_) => {
                let _ = VariantClear(&mut role);
                return false;
            }
        };
        let allows = role.Anonymous.Anonymous.vt == VT_I4
            && state.Anonymous.Anonymous.vt == VT_I4
            && editable_accessible(
                role.Anonymous.Anonymous.Anonymous.lVal as u32,
                state.Anonymous.Anonymous.Anonymous.lVal as u32,
                word_document,
            );
        let _ = VariantClear(&mut role);
        let _ = VariantClear(&mut state);
        allows
    }
}

fn native_edit_is_plain(hwnd: HWND) -> bool {
    unsafe {
        let Some(class) = window_class(hwnd) else {
            return false;
        };
        if !native_edit_class(&class) || !IsWindowEnabled(hwnd).as_bool() {
            return false;
        }
        GetWindowLongW(hwnd, GWL_STYLE) & (ES_PASSWORD | ES_READONLY) == 0
            && SendMessageW(hwnd, EM_GETPASSWORDCHAR, Some(WPARAM(0)), Some(LPARAM(0))).0 == 0
    }
}

fn native_edit_class(class: &str) -> bool {
    [
        "Edit",
        "RichEdit",
        "RichEdit20A",
        "RichEdit20W",
        "RICHEDIT50W",
        "RichEditD2DPT",
    ]
    .iter()
    .any(|known| class.eq_ignore_ascii_case(known))
}

fn secret_scope(scope: InputScope) -> bool {
    [
        IS_PASSWORD,
        IS_NUMERIC_PASSWORD,
        IS_NUMERIC_PIN,
        IS_ALPHANUMERIC_PIN,
        IS_ALPHANUMERIC_PIN_SET,
    ]
    .contains(&scope)
}

/// 密碼／PIN 優先於 IS_PRIVATE：無痕視窗裡的密碼欄同時帶兩者，仍要繞過。
fn scope_field(scopes: &[InputScope]) -> Field {
    if scopes.iter().any(|s| secret_scope(*s)) {
        Field::Blocked
    } else if scopes.contains(&IS_PRIVATE) {
        Field::Private
    } else {
        Field::Open
    }
}

#[implement(ITfEditSession)]
struct ScopeProbe {
    ctx: ITfContext,
    field: Cell<Field>,
    ui_less: bool,
}
impl Drop for ScopeProbe {
    fn drop(&mut self) {
        crate::object_destroyed();
    }
}
impl ITfEditSession_Impl for ScopeProbe_Impl {
    fn DoEditSession(&self, ec: u32) -> windows_core::Result<()> {
        crate::guarded("InputScope", || unsafe {
            // 沒有 scope 屬性的傳統文字框可正常輸入；無法解讀的屬性則阻擋引擎處理。
            let Ok(prop) = self.ctx.GetAppProperty(&GUID_PROP_INPUTSCOPE) else {
                self.field.set(Field::Open);
                return Ok(());
            };
            let mut selected = [TF_SELECTION::default()];
            let mut fetched = 0;
            self.ctx
                .GetSelection(ec, TF_DEFAULT_SELECTION, &mut selected, &mut fetched)?;
            let range = std::mem::ManuallyDrop::take(&mut selected[0].range);
            let Some(range) = range else {
                crate::log::once(&format!(
                    "欄位政策：宿主沒有回報選取範圍，無法判斷，按鍵放行給程式 exe={}",
                    crate::log::exe_name()
                ));
                return Ok(());
            };
            let mut value = match prop.GetValue(ec, &range) {
                Ok(value) => value,
                // 部分 RichEdit（含 Windows 11 記事本）沒有 InputScope，回傳 E_FAIL。
                // 僅在焦點確實屬於本 context 且密碼／唯讀狀態可核對時退回原生／MSAA 檢查。
                Err(error) if error.code() == E_FAIL || error.code() == E_NOTIMPL => {
                    let field = if native_edit_allows(&self.ctx) || document_canvas_allows(&self.ctx) {
                        Field::Open
                    } else if let Some(field) = xaml_island_field(&self.ctx) {
                        field
                    } else if custom_field_allows(&self.ctx) {
                        Field::Private
                    } else {
                        Field::Blocked
                    };
                    let detail = format!(
                        "scope={:?} ui_less={} {} field={:?}",
                        error.code(),
                        self.ui_less,
                        missing_scope_context(&self.ctx),
                        field
                    );
                    // 降級成私人組字或放行給宿主時，預設在 tsf.log 記一次（每個行程同樣的情況只記一次，#209）；
                    // 只有欄位類別與旗標，不含文字與鍵碼。
                    if field != Field::Open {
                        crate::log::once(&format!(
                            "欄位政策：讀不到 InputScope，{} exe={} {}",
                            if field == Field::Private { "以私人方式組字" } else { "無法判斷，按鍵放行給程式" },
                            crate::log::exe_name(),
                            detail
                        ));
                    }
                    crate::log::zdebug!("field policy missing {}", detail);
                    self.field.set(field);
                    return Ok(());
                }
                Err(error) => return Err(error),
            };
            let result = (|| -> windows_core::Result<Field> {
                if value.Anonymous.Anonymous.vt != VT_UNKNOWN {
                    return Ok(Field::Open);
                }
                let Some(unknown) = value.Anonymous.Anonymous.Anonymous.punkVal.as_ref() else {
                    return Ok(Field::Open);
                };
                let scope: ITfInputScope = unknown.cast()?;
                let mut scopes = std::ptr::null_mut();
                let mut count = 0;
                scope.GetInputScopes(&mut scopes, &mut count)?;
                let field = if scopes.is_null() {
                    if count != 0 {
                        Field::Blocked
                    } else {
                        Field::Open
                    }
                } else {
                    scope_field(std::slice::from_raw_parts(scopes, count as usize))
                };
                CoTaskMemFree(Some(scopes.cast()));
                Ok(field)
            })();
            let _ = VariantClear(&mut value);
            self.field.set(result?);
            Ok(())
        })
    }
}

#[cfg(test)]
#[path = "privacy_host_tests.rs"]
pub(crate) mod host_tests;

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn missing_scope_document_fallback_requires_editable_focused_nonprivate_content() {
        let focused = STATE_SYSTEM_FOCUSED | STATE_SYSTEM_FOCUSABLE;
        assert!(editable_accessible(ROLE_SYSTEM_DOCUMENT, focused, false));
        assert!(editable_accessible(ROLE_SYSTEM_TEXT, focused, false));
        assert!(editable_accessible(ROLE_SYSTEM_CLIENT, focused, true));
        for flag in [
            STATE_SYSTEM_READONLY,
            STATE_SYSTEM_PROTECTED,
            STATE_SYSTEM_UNAVAILABLE,
        ] {
            assert!(!editable_accessible(
                ROLE_SYSTEM_DOCUMENT,
                focused | flag,
                false
            ));
            assert!(!editable_accessible(
                ROLE_SYSTEM_TEXT,
                focused | flag,
                false
            ));
            assert!(!editable_accessible(
                ROLE_SYSTEM_CLIENT,
                focused | flag,
                true
            ));
        }
        assert!(!editable_accessible(
            ROLE_SYSTEM_DOCUMENT,
            STATE_SYSTEM_FOCUSABLE,
            false
        ));
        assert!(!editable_accessible(
            windows::Win32::UI::Accessibility::ROLE_SYSTEM_CLIENT,
            focused,
            false
        ));
    }

    #[test]
    fn onenote_page_canvas_is_a_document() {
        // #163：OneNote 桌面版的頁面（標題與內文）是這個視窗類別；大小寫不同、OneNote 的其他視窗不算。
        assert!(document_canvas("OneNote::DocumentCanvas"));
        for class in [
            "onenote::documentcanvas",
            "Framework::CFrame",
            "OneNote::CFirstRunDialog",
            "NetUIHWND",
            "_WwG",
            "RichEdit20W",
            "",
        ] {
            assert!(!document_canvas(class), "{class}");
        }
    }

    #[test]
    fn hotkey_toggles_unless_the_field_is_known_blocked() {
        // #164：讀不到欄位政策（宿主不給同步鎖）照常切換；確定擋下的欄位不切換。
        assert!(toggle_allowed(None));
        assert!(toggle_allowed(Some(Field::Open)));
        assert!(toggle_allowed(Some(Field::Private)));
        assert!(!toggle_allowed(Some(Field::Blocked)));
    }

    #[test]
    fn native_fallback_rejects_password_readonly_disabled_and_unknown_controls() {
        use windows::Win32::UI::Controls::EM_SETPASSWORDCHAR;
        use windows::Win32::UI::Input::KeyboardAndMouse::EnableWindow;
        use windows::Win32::UI::WindowsAndMessaging::*;
        use windows_core::w;
        unsafe {
            let edit = CreateWindowExW(
                WINDOW_EX_STYLE(0),
                w!("Edit"),
                w!(""),
                WINDOW_STYLE(0),
                0,
                0,
                100,
                20,
                None,
                None,
                None,
                None,
            )
            .unwrap();
            assert!(native_edit_is_plain(edit));
            SendMessageW(
                edit,
                EM_SETPASSWORDCHAR,
                Some(WPARAM('*' as usize)),
                Some(LPARAM(0)),
            );
            assert!(!native_edit_is_plain(edit));
            SendMessageW(edit, EM_SETPASSWORDCHAR, Some(WPARAM(0)), Some(LPARAM(0)));
            let style = GetWindowLongW(edit, GWL_STYLE);
            SetWindowLongW(edit, GWL_STYLE, style | ES_READONLY);
            assert!(!native_edit_is_plain(edit));
            SetWindowLongW(edit, GWL_STYLE, style);
            let _ = EnableWindow(edit, false);
            assert!(!native_edit_is_plain(edit));
            let _ = DestroyWindow(edit);
        }
        assert!(!native_edit_class("Chrome_RenderWidgetHostHWND"));
        assert!(!native_edit_class("Windows.UI.Core.CoreWindow"));
        assert!(!native_edit_class("RichEditPrivateCustom"));
    }

    #[test]
    fn missing_scope_fallback_accepts_only_winui_island_input() {
        // #105：WinUI 3 文字框的焦點視窗；其他 XAML 外框或自訂視窗仍須通過原有檢查。
        // 相片搜尋框實測的靜態旗標是 0x32（REGIONS＋觸控鍵盤預測＋自動校正）。
        assert_eq!(island_field("InputSiteWindowClass", 0x32), Some(Field::Open));
        assert_eq!(
            island_field("InputSiteWindowClass", TS_SS_TKBPREDICTIONENABLE),
            Some(Field::Open)
        );
        assert_eq!(
            island_field("InputSiteWindowClass", TS_SS_TKBAUTOCORRECTENABLE),
            Some(Field::Open)
        );
        // 宿主關掉預測與校正：仍可組字，但不學習、不讀前文。
        assert_eq!(
            island_field("InputSiteWindowClass", TS_SS_REGIONS),
            Some(Field::Private)
        );
        assert_eq!(island_field("InputSiteWindowClass", 0), Some(Field::Private));
        for class in [
            "Microsoft.UI.Content.DesktopChildSiteBridge",
            "WinUIDesktopWin32WindowClass",
            "Windows.UI.Core.CoreWindow",
            "Chrome_RenderWidgetHostHWND",
            "inputsitewindowclass",
            "Edit",
        ] {
            assert_eq!(island_field(class, 0x32), None, "{class}");
        }
    }

    #[test]
    fn secret_scopes_bypass_engine() {
        for s in [
            IS_PASSWORD,
            IS_NUMERIC_PASSWORD,
            IS_NUMERIC_PIN,
            IS_ALPHANUMERIC_PIN,
            IS_ALPHANUMERIC_PIN_SET,
        ] {
            assert_eq!(scope_field(&[s]), Field::Blocked);
            // 無痕視窗的密碼欄同時帶 IS_PRIVATE，仍要繞過。
            assert_eq!(scope_field(&[IS_PRIVATE, s]), Field::Blocked);
        }
        for s in [IS_DEFAULT, IS_TEXT, IS_EMAIL_SMTPEMAILADDRESS, IS_NUMBER] {
            assert_eq!(scope_field(&[s]), Field::Open);
        }
        assert_eq!(scope_field(&[]), Field::Open);
    }

    #[test]
    fn private_scope_composes_without_learning() {
        // Chrome 無痕／訪客與 Firefox 私密視窗把每個欄位標成 IS_PRIVATE（#68、#70）。
        assert_eq!(scope_field(&[IS_PRIVATE]), Field::Private);
        assert_eq!(scope_field(&[IS_DEFAULT, IS_PRIVATE]), Field::Private);
        assert_eq!(scope_field(&[IS_PRIVATE, IS_URL]), Field::Private);
    }
}
