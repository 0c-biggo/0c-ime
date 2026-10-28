//! #209：模擬自己實作 TSF 文字儲存的宿主（Unreal Engine 的 UnrealWindow）。文字儲存不支援任何文字屬性，
//! 讀 InputScope 回 E_FAIL；焦點視窗是自訂類別、不是標準編輯框，也不是 IMM32 的暫時 context 或 UI-less。
//! 以真的視窗（本測試執行緒的焦點）配假的 context／property／range，走一次 privacy::field。
use super::*;
use std::rc::Rc;
use windows::Win32::Foundation::{E_NOTIMPL, LRESULT, S_OK};
use windows::Win32::Graphics::Gdi::HBRUSH;
use windows::Win32::System::Com::{
    CoInitializeEx, CoTaskMemAlloc, IDataObject, IDispatch, IDispatch_Impl, ITypeInfo, COINIT_APARTMENTTHREADED,
    DISPATCH_FLAGS, DISPPARAMS, EXCEPINFO,
};
use windows::Win32::System::LibraryLoader::GetModuleHandleW;
use windows::Win32::UI::Accessibility::{IAccessible_Impl, LresultFromObject};
use windows::Win32::UI::Input::KeyboardAndMouse::{EnableWindow, SetFocus};
use windows::Win32::UI::WindowsAndMessaging::{
    CreateWindowExW, DefWindowProcW, DestroyWindow, RegisterClassW, SetWindowLongW, ShowWindow, HCURSOR, HICON,
    HMENU, OBJID_CLIENT, SW_SHOWNOACTIVATE, WINDOW_EX_STYLE, WINDOW_STYLE, WM_GETOBJECT, WNDCLASSW, WS_POPUP,
    WS_VISIBLE,
};
use windows_core::{w, IUnknown, OutRef, Ref, BOOL, BSTR, GUID, HRESULT, PCWSTR, PWSTR};

type R<T> = windows_core::Result<T>;
fn no<T>() -> R<T> {
    Err(E_NOTIMPL.into())
}

/// 宿主讀 InputScope 時怎麼回。
#[derive(Clone)]
enum Scope {
    /// GetAppProperty 成功，GetValue 回這個錯誤（UE5 是 E_FAIL）。
    Fail(HRESULT),
    /// 回這些 InputScope。
    Scopes(Vec<InputScope>),
    /// 沒有 InputScope 屬性（GetAppProperty 失敗）。
    NoProperty,
}

struct Host {
    view: HWND,
    scope: Scope,
    static_flags: u32,
    dynamic_flags: u32,
}

#[implement(ITfContext)]
struct Ctx {
    host: Rc<Host>,
}
impl ITfContext_Impl for Ctx_Impl {
    fn RequestEditSession(&self, _: u32, session: Ref<'_, ITfEditSession>, _: TF_CONTEXT_EDIT_CONTEXT_FLAGS) -> R<HRESULT> {
        Ok(match unsafe { session.ok()?.DoEditSession(1) } {
            Ok(()) => S_OK,
            Err(e) => e.code(),
        })
    }
    fn InWriteSession(&self, _: u32) -> R<BOOL> {
        no()
    }
    fn GetSelection(&self, _: u32, _: u32, count: u32, out: *mut TF_SELECTION, fetched: *mut u32) -> R<()> {
        if count == 0 {
            return no();
        }
        let range: ITfRange = ComObject::new(Range).into_interface();
        unsafe {
            out.write(TF_SELECTION {
                range: std::mem::ManuallyDrop::new(Some(range)),
                style: TF_SELECTIONSTYLE::default(),
            });
            fetched.write(1);
        }
        Ok(())
    }
    fn SetSelection(&self, _: u32, _: u32, _: *const TF_SELECTION) -> R<()> {
        no()
    }
    fn GetStart(&self, _: u32) -> R<ITfRange> {
        no()
    }
    fn GetEnd(&self, _: u32) -> R<ITfRange> {
        no()
    }
    fn GetActiveView(&self) -> R<ITfContextView> {
        Ok(ComObject::new(View { hwnd: self.host.view }).into_interface())
    }
    fn EnumViews(&self) -> R<IEnumTfContextViews> {
        no()
    }
    fn GetStatus(&self) -> R<TS_STATUS> {
        Ok(TS_STATUS {
            dwDynamicFlags: self.host.dynamic_flags,
            dwStaticFlags: self.host.static_flags,
        })
    }
    fn GetProperty(&self, _: *const GUID) -> R<ITfProperty> {
        no()
    }
    fn GetAppProperty(&self, guid: *const GUID) -> R<ITfReadOnlyProperty> {
        if unsafe { *guid } != GUID_PROP_INPUTSCOPE {
            return no();
        }
        match &self.host.scope {
            Scope::NoProperty => no(),
            scope => Ok(ComObject::new(Property { scope: scope.clone() }).into_interface()),
        }
    }
    fn TrackProperties(&self, _: *const *const GUID, _: u32, _: *const *const GUID, _: u32) -> R<ITfReadOnlyProperty> {
        no()
    }
    fn EnumProperties(&self) -> R<IEnumTfProperties> {
        no()
    }
    fn GetDocumentMgr(&self) -> R<ITfDocumentMgr> {
        no()
    }
    fn CreateRangeBackup(&self, _: u32, _: Ref<'_, ITfRange>) -> R<ITfRangeBackup> {
        no()
    }
}

#[implement(ITfContextView)]
struct View {
    hwnd: HWND,
}
impl ITfContextView_Impl for View_Impl {
    fn GetRangeFromPoint(&self, _: u32, _: *const windows::Win32::Foundation::POINT, _: u32) -> R<ITfRange> {
        no()
    }
    fn GetTextExt(&self, _: u32, _: Ref<'_, ITfRange>, _: *mut windows::Win32::Foundation::RECT, _: *mut BOOL) -> R<()> {
        no()
    }
    fn GetScreenExt(&self) -> R<windows::Win32::Foundation::RECT> {
        no()
    }
    fn GetWnd(&self) -> R<HWND> {
        Ok(self.hwnd)
    }
}

#[implement(ITfReadOnlyProperty)]
struct Property {
    scope: Scope,
}
impl ITfReadOnlyProperty_Impl for Property_Impl {
    fn GetType(&self) -> R<GUID> {
        Ok(GUID_PROP_INPUTSCOPE)
    }
    fn EnumRanges(&self, _: u32, _: OutRef<'_, IEnumTfRanges>, _: Ref<'_, ITfRange>) -> R<()> {
        no()
    }
    fn GetValue(&self, _: u32, _: Ref<'_, ITfRange>) -> R<VARIANT> {
        match &self.scope {
            Scope::Fail(hr) => Err((*hr).into()),
            Scope::Scopes(scopes) => {
                let scope: IUnknown = ComObject::new(Scopes(scopes.clone())).into_interface();
                Ok(VARIANT::from(scope))
            }
            Scope::NoProperty => no(),
        }
    }
    fn GetContext(&self) -> R<ITfContext> {
        no()
    }
}

#[implement(ITfInputScope)]
struct Scopes(Vec<InputScope>);
impl ITfInputScope_Impl for Scopes_Impl {
    fn GetInputScopes(&self, out: *mut *mut InputScope, count: *mut u32) -> R<()> {
        unsafe {
            let size = std::mem::size_of::<InputScope>() * self.0.len().max(1);
            let buf = CoTaskMemAlloc(size) as *mut InputScope;
            std::ptr::copy_nonoverlapping(self.0.as_ptr(), buf, self.0.len());
            out.write(buf);
            count.write(self.0.len() as u32);
        }
        Ok(())
    }
    fn GetPhrase(&self, _: *mut *mut BSTR, _: *mut u32) -> R<()> {
        no()
    }
    fn GetRegularExpression(&self) -> R<BSTR> {
        no()
    }
    fn GetSRGS(&self) -> R<BSTR> {
        no()
    }
    fn GetXML(&self) -> R<BSTR> {
        no()
    }
}

/// 選取範圍：欄位政策只把它交給 GetValue。
#[implement(ITfRange)]
struct Range;
impl ITfRange_Impl for Range_Impl {
    fn GetText(&self, _: u32, _: u32, _: PWSTR, _: u32, _: *mut u32) -> R<()> {
        no()
    }
    fn SetText(&self, _: u32, _: u32, _: &PCWSTR, _: i32) -> R<()> {
        no()
    }
    fn GetFormattedText(&self, _: u32) -> R<IDataObject> {
        no()
    }
    fn GetEmbedded(&self, _: u32, _: *const GUID, _: *const GUID) -> R<IUnknown> {
        no()
    }
    fn InsertEmbedded(&self, _: u32, _: u32, _: Ref<'_, IDataObject>) -> R<()> {
        no()
    }
    fn ShiftStart(&self, _: u32, _: i32, _: *mut i32, _: *const TF_HALTCOND) -> R<()> {
        no()
    }
    fn ShiftEnd(&self, _: u32, _: i32, _: *mut i32, _: *const TF_HALTCOND) -> R<()> {
        no()
    }
    fn ShiftStartToRange(&self, _: u32, _: Ref<'_, ITfRange>, _: TfAnchor) -> R<()> {
        no()
    }
    fn ShiftEndToRange(&self, _: u32, _: Ref<'_, ITfRange>, _: TfAnchor) -> R<()> {
        no()
    }
    fn ShiftStartRegion(&self, _: u32, _: TfShiftDir) -> R<BOOL> {
        no()
    }
    fn ShiftEndRegion(&self, _: u32, _: TfShiftDir) -> R<BOOL> {
        no()
    }
    fn IsEmpty(&self, _: u32) -> R<BOOL> {
        no()
    }
    fn Collapse(&self, _: u32, _: TfAnchor) -> R<()> {
        no()
    }
    fn IsEqualStart(&self, _: u32, _: Ref<'_, ITfRange>, _: TfAnchor) -> R<BOOL> {
        no()
    }
    fn IsEqualEnd(&self, _: u32, _: Ref<'_, ITfRange>, _: TfAnchor) -> R<BOOL> {
        no()
    }
    fn CompareStart(&self, _: u32, _: Ref<'_, ITfRange>, _: TfAnchor) -> R<i32> {
        no()
    }
    fn CompareEnd(&self, _: u32, _: Ref<'_, ITfRange>, _: TfAnchor) -> R<i32> {
        no()
    }
    fn AdjustForInsert(&self, _: u32, _: u32) -> R<BOOL> {
        no()
    }
    fn GetGravity(&self, _: *mut TfGravity, _: *mut TfGravity) -> R<()> {
        no()
    }
    fn SetGravity(&self, _: u32, _: TfGravity, _: TfGravity) -> R<()> {
        no()
    }
    fn Clone(&self) -> R<ITfRange> {
        no()
    }
    fn GetContext(&self) -> R<ITfContext> {
        no()
    }
}

/// 自訂視窗的 MSAA：角色 TEXT、有焦點，另外帶 `extra` 狀態（例如 STATE_SYSTEM_PROTECTED，自訂的密碼欄）。
#[implement(IAccessible)]
struct Accessible {
    extra: u32,
}
impl IDispatch_Impl for Accessible_Impl {
    fn GetTypeInfoCount(&self) -> R<u32> {
        Ok(0)
    }
    fn GetTypeInfo(&self, _: u32, _: u32) -> R<ITypeInfo> {
        no()
    }
    fn GetIDsOfNames(&self, _: *const GUID, _: *const PCWSTR, _: u32, _: u32, _: *mut i32) -> R<()> {
        no()
    }
    fn Invoke(
        &self,
        _: i32,
        _: *const GUID,
        _: u32,
        _: DISPATCH_FLAGS,
        _: *const DISPPARAMS,
        _: *mut VARIANT,
        _: *mut EXCEPINFO,
        _: *mut u32,
    ) -> R<()> {
        no()
    }
}
impl IAccessible_Impl for Accessible_Impl {
    fn accParent(&self) -> R<IDispatch> {
        no()
    }
    fn accChildCount(&self) -> R<i32> {
        Ok(0)
    }
    fn get_accChild(&self, _: &VARIANT) -> R<IDispatch> {
        no()
    }
    fn get_accName(&self, _: &VARIANT) -> R<BSTR> {
        no()
    }
    fn get_accValue(&self, _: &VARIANT) -> R<BSTR> {
        no()
    }
    fn get_accDescription(&self, _: &VARIANT) -> R<BSTR> {
        no()
    }
    fn get_accRole(&self, _: &VARIANT) -> R<VARIANT> {
        Ok(VARIANT::from(ROLE_SYSTEM_TEXT as i32))
    }
    fn get_accState(&self, _: &VARIANT) -> R<VARIANT> {
        Ok(VARIANT::from((STATE_SYSTEM_FOCUSED | STATE_SYSTEM_FOCUSABLE | self.extra) as i32))
    }
    fn get_accHelp(&self, _: &VARIANT) -> R<BSTR> {
        no()
    }
    fn get_accHelpTopic(&self, _: *mut BSTR, _: &VARIANT) -> R<i32> {
        no()
    }
    fn get_accKeyboardShortcut(&self, _: &VARIANT) -> R<BSTR> {
        no()
    }
    fn accFocus(&self) -> R<VARIANT> {
        no()
    }
    fn accSelection(&self) -> R<VARIANT> {
        no()
    }
    fn get_accDefaultAction(&self, _: &VARIANT) -> R<BSTR> {
        no()
    }
    fn accSelect(&self, _: i32, _: &VARIANT) -> R<()> {
        no()
    }
    fn accLocation(&self, _: *mut i32, _: *mut i32, _: *mut i32, _: *mut i32, _: &VARIANT) -> R<()> {
        no()
    }
    fn accNavigate(&self, _: i32, _: &VARIANT) -> R<VARIANT> {
        no()
    }
    fn accHitTest(&self, _: i32, _: i32) -> R<VARIANT> {
        no()
    }
    fn accDoDefaultAction(&self, _: &VARIANT) -> R<()> {
        no()
    }
    fn put_accName(&self, _: &VARIANT, _: &BSTR) -> R<()> {
        no()
    }
    fn put_accValue(&self, _: &VARIANT, _: &BSTR) -> R<()> {
        no()
    }
}

/// 回報 MSAA 為受保護（密碼）的自訂視窗。
unsafe extern "system" fn protected_proc(hwnd: HWND, msg: u32, wparam: WPARAM, lparam: LPARAM) -> LRESULT {
    if msg == WM_GETOBJECT && lparam.0 as i32 == OBJID_CLIENT.0 {
        let accessible: IAccessible = ComObject::new(Accessible { extra: STATE_SYSTEM_PROTECTED }).into_interface();
        return LresultFromObject(&IAccessible::IID, wparam, &accessible);
    }
    DefWindowProcW(hwnd, msg, wparam, lparam)
}

fn register(class: PCWSTR, proc: windows::Win32::UI::WindowsAndMessaging::WNDPROC) {
    unsafe {
        let wc = WNDCLASSW {
            lpfnWndProc: proc,
            hInstance: GetModuleHandleW(None).unwrap().into(),
            lpszClassName: class,
            hCursor: HCURSOR::default(),
            hIcon: HICON::default(),
            hbrBackground: HBRUSH::default(),
            ..Default::default()
        };
        // 同一個行程的其他測試可能已經註冊過。
        let _ = RegisterClassW(&wc);
    }
}

unsafe extern "system" fn plain_proc(hwnd: HWND, msg: u32, wparam: WPARAM, lparam: LPARAM) -> LRESULT {
    DefWindowProcW(hwnd, msg, wparam, lparam)
}

fn window(class: PCWSTR, style: u32) -> HWND {
    unsafe {
        let hwnd = CreateWindowExW(
            WINDOW_EX_STYLE(0),
            class,
            w!(""),
            WINDOW_STYLE(WS_POPUP.0 | WS_VISIBLE.0 | style),
            -32000,
            -32000,
            200,
            40,
            None,
            None::<HMENU>,
            None,
            None,
        )
        .unwrap();
        let _ = ShowWindow(hwnd, SW_SHOWNOACTIVATE);
        hwnd
    }
}

/// 以本執行緒的焦點視窗為 view 的假 context；`focus` 是要給焦點的視窗（預設同 view）。
fn host(view: HWND, focus: HWND, scope: Scope, static_flags: u32, dynamic_flags: u32) -> ITfContext {
    unsafe {
        let _ = SetFocus(Some(focus));
        assert_eq!(GetFocus(), focus, "測試執行緒拿不到焦點");
    }
    ComObject::new(Ctx {
        host: Rc::new(Host { view, scope, static_flags, dynamic_flags }),
    })
    .into_interface()
}

/// 給其他模組的宿主測試（#217）：本執行緒焦點所在、InputScope 是一般欄位的假 context（`field` 為 Open）。
/// 回傳的視窗由呼叫端 DestroyWindow。
pub(crate) fn open_field() -> (HWND, ITfContext) {
    init();
    let view = window(w!("UnrealWindow"), 0);
    let ctx = host(view, view, Scope::Scopes(vec![IS_DEFAULT]), TS_SS_NOHIDDENTEXT, 0);
    assert_eq!(field(Some(&ctx), 1, false), Field::Open);
    (view, ctx)
}

fn init() {
    unsafe {
        let _ = CoInitializeEx(None, COINIT_APARTMENTTHREADED);
    }
    register(w!("UnrealWindow"), Some(plain_proc));
    register(w!("ZcProtectedField"), Some(protected_proc));
}

#[test]
fn unreal_text_store_without_inputscope_composes_privately() {
    init();
    let unreal = window(w!("UnrealWindow"), 0);
    // WARDOGS 的 tsf.log：scope=E_FAIL status=0x8/0x0 focus=UnrealWindow view=UnrealWindow same=true。
    // 修正前是 Blocked，每一鍵都直接以英文交給遊戲。
    for hr in [E_FAIL, E_NOTIMPL] {
        for ui_less in [false, true] {
            let ctx = host(unreal, unreal, Scope::Fail(hr), TS_SS_NOHIDDENTEXT, 0);
            assert_eq!(field(Some(&ctx), 1, ui_less), Field::Private, "{hr:?} ui_less={ui_less}");
            assert!(hotkey_allowed(Some(&ctx), 1, ui_less));
            assert!(!blocked_in_edit(&ctx, 1, ui_less));
        }
    }
    // IMM32 程式的暫時 context（#86）照舊。
    let ctx = host(unreal, unreal, Scope::Fail(E_FAIL), TS_SS_TRANSITORY, 0);
    assert_eq!(field(Some(&ctx), 1, false), Field::Private);
    // 讀得到 InputScope 時照 InputScope：密碼、PIN 一定擋，私人照私人，一般欄位照常。
    for secret in [IS_PASSWORD, IS_NUMERIC_PIN] {
        let ctx = host(unreal, unreal, Scope::Scopes(vec![secret]), TS_SS_NOHIDDENTEXT, 0);
        assert_eq!(field(Some(&ctx), 1, false), Field::Blocked, "{secret:?}");
        assert!(blocked_in_edit(&ctx, 1, false));
    }
    let ctx = host(unreal, unreal, Scope::Scopes(vec![IS_PRIVATE]), TS_SS_NOHIDDENTEXT, 0);
    assert_eq!(field(Some(&ctx), 1, false), Field::Private);
    let ctx = host(unreal, unreal, Scope::Scopes(vec![IS_DEFAULT]), TS_SS_NOHIDDENTEXT, 0);
    assert_eq!(field(Some(&ctx), 1, false), Field::Open);
    let ctx = host(unreal, unreal, Scope::NoProperty, TS_SS_NOHIDDENTEXT, 0);
    assert_eq!(field(Some(&ctx), 1, false), Field::Open);
    unsafe {
        let _ = DestroyWindow(unreal);
    }
}

#[test]
fn missing_inputscope_still_blocks_readonly_unfocused_and_protected_fields() {
    init();
    let unreal = window(w!("UnrealWindow"), 0);
    let other = window(w!("UnrealWindow"), 0);
    // context 唯讀。
    let ctx = host(unreal, unreal, Scope::Fail(E_FAIL), TS_SS_NOHIDDENTEXT, TF_SD_READONLY);
    assert_eq!(field(Some(&ctx), 1, false), Field::Blocked);
    assert!(!hotkey_allowed(Some(&ctx), 1, false));
    // 焦點不在這個 context 的視窗：不知道按鍵會進哪裡。
    let ctx = host(unreal, other, Scope::Fail(E_FAIL), TS_SS_NOHIDDENTEXT, 0);
    assert_eq!(field(Some(&ctx), 1, false), Field::Blocked);
    // MSAA 標成受保護（自訂的密碼欄）。
    let protected = window(w!("ZcProtectedField"), 0);
    let ctx = host(protected, protected, Scope::Fail(E_FAIL), TS_SS_NOHIDDENTEXT, 0);
    assert_eq!(field(Some(&ctx), 1, false), Field::Blocked);
    assert!(blocked_in_edit(&ctx, 1, false));
    // 停用的視窗。
    let ctx = host(unreal, unreal, Scope::Fail(E_FAIL), TS_SS_NOHIDDENTEXT, 0);
    unsafe {
        let _ = EnableWindow(unreal, false);
    }
    assert_eq!(field(Some(&ctx), 1, false), Field::Blocked);
    unsafe {
        let _ = DestroyWindow(protected);
        let _ = DestroyWindow(other);
        let _ = DestroyWindow(unreal);
    }
}

#[test]
fn missing_inputscope_on_a_native_edit_checks_the_password_style() {
    use windows::Win32::UI::WindowsAndMessaging::{GetWindowLongW, GWL_STYLE};
    init();
    let edit = window(w!("Edit"), 0);
    // 一般的 EDIT（記事本類）照常組字；密碼樣式的 EDIT 不論 context 旗標一律擋下。
    let ctx = host(edit, edit, Scope::Fail(E_FAIL), TS_SS_NOHIDDENTEXT, 0);
    assert_eq!(field(Some(&ctx), 1, false), Field::Open);
    unsafe {
        let style = GetWindowLongW(edit, GWL_STYLE);
        SetWindowLongW(edit, GWL_STYLE, style | ES_PASSWORD);
    }
    for flags in [TS_SS_NOHIDDENTEXT, TS_SS_TRANSITORY] {
        let ctx = host(edit, edit, Scope::Fail(E_FAIL), flags, 0);
        assert_eq!(field(Some(&ctx), 1, true), Field::Blocked, "{flags:#x}");
    }
    unsafe {
        let _ = DestroyWindow(edit);
    }
}

#[test]
fn imm32_context_without_caret_passes_english_keys_to_the_app() {
    use windows::Win32::UI::WindowsAndMessaging::{CreateCaret, DestroyCaret};
    init();
    // #210：IMM32 程式的暫時 context（系統相容層）在焦點不是文字框時也有；Photoshop 的畫布沒有系統插入點。
    let canvas = window(w!("UnrealWindow"), 0);
    let ctx = host(canvas, canvas, Scope::Scopes(vec![IS_DEFAULT]), TS_SS_TRANSITORY, 0);
    assert_eq!(field(Some(&ctx), 1, false), Field::Open);
    assert!(english_passthrough(&ctx, 0), "沒有插入點：英文模式按鍵交給程式");
    assert!(!english_passthrough(&ctx, 0x4000_0040), "程式自己呼叫 ActivateEx（Windows Terminal）：照常補字");
    // 文字框有焦點時有插入點（EDIT、PuTTY）：照常英文補字。
    unsafe { CreateCaret(canvas, None, 1, 16) }.unwrap();
    assert!(!english_passthrough(&ctx, 0), "有插入點：照常補字");
    unsafe {
        let _ = DestroyCaret();
    }
    assert!(english_passthrough(&ctx, 0));
    // 自己實作 TSF 的程式（瀏覽器、Windows Terminal）沒有系統插入點也照常補字。
    let ctx = host(canvas, canvas, Scope::Scopes(vec![IS_DEFAULT]), TS_SS_NOHIDDENTEXT, 0);
    assert!(!english_passthrough(&ctx, 0));
    unsafe {
        let _ = DestroyWindow(canvas);
    }
}
