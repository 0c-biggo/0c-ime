//! Edit session：在 TSF 的寫入權限內，依快照更新組字區、提交文字、放置游標。
//! 提交語意是「所見即所得」：提交時送出的就是畫面上的組字（CLAUDE.md）。
use std::cell::{Cell, RefCell};
use std::mem::ManuallyDrop;
use std::rc::Rc;

use windows::Win32::Foundation::RECT;
use windows::Win32::System::Variant::{VARIANT, VARIANT_0, VARIANT_0_0, VARIANT_0_0_0, VT_I4};
use windows::Win32::UI::TextServices::{
    ITfComposition, ITfCompositionSink, ITfContext, ITfContextComposition, ITfEditSession,
    ITfEditSession_Impl, ITfInsertAtSelection, ITfRange, GUID_PROP_ATTRIBUTE, TF_AE_NONE,
    TF_ANCHOR_END, TF_ANCHOR_START, TF_DEFAULT_SELECTION, TF_ES_ASYNCDONTCARE, TF_ES_READWRITE,
    TF_ES_SYNC, TF_IAS_QUERYONLY, TF_SELECTION, TF_SELECTIONSTYLE,
};
use windows_core::{implement, ComObject, Interface, HRESULT};

use crate::displayattr::{style_ranges, StyledRange, KINDS};
use crate::log::{zdebug, zlog};
use crate::session::Snapshot;

#[implement(ITfEditSession)]
pub struct Apply {
    ctx: ITfContext,
    sink: ITfCompositionSink,
    composition: RefCell<Option<ITfComposition>>,
    commit: Vec<u16>,
    preedit: Vec<u16>,
    cursor: usize, // UTF-16 單位
    attr_atoms: [u32; KINDS.len()],
    styles: Vec<StyledRange>,
    caret: RefCell<Located>, // 游標的螢幕矩形（給候選窗定位）與查不到的原因
    /// 沒有組字時也要查游標矩形（這一鍵要顯示候選窗或提示）。
    want_caret: bool,
    ending: Rc<Cell<bool>>,
    /// 進行到哪一步：失敗時記進 tsf.log（只記步驟與 HRESULT，不記文字）。
    step: Cell<&'static str>,
    /// 已經對文件寫過提交文字（SetText 成功，或回報失敗但讀回確認已寫入）。之後不得再寫一次，
    /// 否則宿主會看到同一段字插入兩次（#88）。
    committed: Cell<bool>,
    /// DoEditSession 從頭到尾成功跑完：宿主回報失敗也照成功處理。
    done: Cell<bool>,
}

impl Drop for Apply {
    fn drop(&mut self) {
        crate::object_destroyed();
    }
}

pub struct Applied {
    /// 同步 edit session 失敗時：只是更新組字則是呼叫前的組字物件；有提交則為 None（已改排非同步）；
    /// 提交文字已寫入後才失敗則是當時剩下的組字（不重排）。
    pub composition: Option<ITfComposition>,
    pub caret: Located,
}

/// 宿主為什麼給不出游標矩形（記進 tsf.log，不含文字）。
#[derive(Clone, Copy, Debug, PartialEq)]
pub enum Miss {
    /// 沒有跑到查詢：edit session 沒成功，或這一鍵不顯示候選窗、沒有組字，不必查。
    NotRun,
    /// 按鍵沒有 context（沒有可寫入的文件）。
    NoContext,
    /// 沒有 active view。
    NoView(HRESULT),
    /// 組不出游標所在的範圍。
    Range(HRESULT),
    /// GetTextExt 失敗。Chromium（Edge、Electron、WebView2）在版面還沒從 renderer 回來時回
    /// TS_E_NOLAYOUT，之後以 OnLayoutChange 通知。
    Failed(HRESULT),
    /// GetTextExt 成功但矩形是空的。
    Empty(RECT),
}

impl std::fmt::Display for Miss {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Miss::NotRun => write!(f, "edit session 沒有完成"),
            Miss::NoContext => write!(f, "沒有 context"),
            Miss::NoView(hr) => write!(f, "GetActiveView {}", hr),
            Miss::Range(hr) => write!(f, "游標範圍 {}", hr),
            Miss::Failed(hr) if *hr == windows::Win32::UI::TextServices::TS_E_NOLAYOUT => {
                write!(f, "GetTextExt TS_E_NOLAYOUT")
            }
            Miss::Failed(hr) => write!(f, "GetTextExt {}", hr),
            Miss::Empty(r) => write!(f, "GetTextExt 空矩形 ({},{},{},{})", r.left, r.top, r.right, r.bottom),
        }
    }
}

impl Miss {
    /// tsf.log 去重用的類別：不含座標與視窗把手，同一種失敗只記一次（HRESULT 種類有限，保留）。
    pub fn class(&self) -> String {
        match self {
            Miss::NotRun => "not-run".into(),
            Miss::NoContext => "no-context".into(),
            Miss::NoView(hr) => format!("no-view {:#x}", hr.0),
            Miss::Range(hr) => format!("range {:#x}", hr.0),
            Miss::Failed(hr) => format!("failed {:#x}", hr.0),
            Miss::Empty(_) => "empty".into(),
        }
    }
}

/// 游標的螢幕矩形；拿不到時是原因。
pub type Located = Result<RECT, Miss>;

/// 範圍在螢幕上的矩形（GetTextExt）。空矩形不算：候選窗會跑到螢幕左上角。
unsafe fn text_ext(ctx: &ITfContext, ec: u32, range: &ITfRange) -> Result<RECT, Miss> {
    let view = ctx.GetActiveView().map_err(|e| Miss::NoView(e.code()))?;
    let mut rc = RECT::default();
    let mut clipped = windows_core::BOOL(0);
    view.GetTextExt(ec, range, &mut rc, &mut clipped).map_err(|e| Miss::Failed(e.code()))?;
    if rc.right > rc.left || rc.bottom > rc.top {
        Ok(rc)
    } else {
        Err(Miss::Empty(rc))
    }
}

/// 組字範圍裡、從起點算 cursor 個 UTF-16 單位的插入點。
unsafe fn composition_caret(ec: u32, comp: &ITfComposition, cursor: usize) -> windows_core::Result<ITfRange> {
    // 複製一份再移動，不動到組字本身的範圍。
    let caret = comp.GetRange()?.Clone()?;
    caret.Collapse(ec, TF_ANCHOR_START)?;
    let mut moved = 0i32;
    caret.ShiftEnd(ec, cursor as i32, &mut moved, std::ptr::null())?;
    caret.Collapse(ec, TF_ANCHOR_END)?;
    Ok(caret)
}

/// 目前選取的插入點（空組字也能開 Emoji 選單，這時沒有組字範圍）。
unsafe fn selection_caret(ctx: &ITfContext, ec: u32) -> windows_core::Result<ITfRange> {
    let mut selected = [TF_SELECTION::default()];
    let mut fetched = 0;
    ctx.GetSelection(ec, TF_DEFAULT_SELECTION, &mut selected, &mut fetched)?;
    let range = ManuallyDrop::take(&mut selected[0].range);
    let range = range.ok_or_else(|| windows_core::Error::from(windows::Win32::Foundation::E_FAIL))?;
    range.Collapse(ec, TF_ANCHOR_END)?;
    Ok(range)
}

/// 目前選取插入點的螢幕矩形。
unsafe fn selection_rect(ctx: &ITfContext, ec: u32) -> Located {
    let range = selection_caret(ctx, ec).map_err(|e| Miss::Range(e.code()))?;
    text_ext(ctx, ec, &range)
}

/// 游標的螢幕矩形（OnLayoutChange 重查用）：有組字就用組字裡的游標位置；組不出這個範圍（組字已結束）
/// 才改用目前選取的插入點。GetTextExt 失敗不再查選取：組字中選取就是我們放的同一個插入點，結果相同。
pub(crate) unsafe fn locate(ctx: &ITfContext, ec: u32, comp: Option<&ITfComposition>, cursor: usize) -> Located {
    match comp.map(|comp| composition_caret(ec, comp, cursor)) {
        Some(Ok(range)) => text_ext(ctx, ec, &range),
        _ => selection_rect(ctx, ec),
    }
}

/// 唯讀 edit session：版面晚到（OnLayoutChange）時重查游標矩形。
#[implement(ITfEditSession)]
pub struct Locate {
    ctx: ITfContext,
    composition: Option<ITfComposition>,
    cursor: usize,
    done: Box<dyn Fn(Located)>,
}

impl Drop for Locate {
    fn drop(&mut self) {
        crate::object_destroyed();
    }
}

impl ITfEditSession_Impl for Locate_Impl {
    fn DoEditSession(&self, ec: u32) -> windows_core::Result<()> {
        crate::guarded("Locate", || {
            let located = unsafe { locate(&self.ctx, ec, self.composition.as_ref(), self.cursor) };
            (self.done)(located);
            Ok(())
        })
    }
}

/// 要一個唯讀 edit session 重查游標矩形，查到後呼叫 done（可能同步，也可能稍後才執行）。
pub(crate) fn request_locate(
    ctx: &ITfContext,
    client_id: u32,
    composition: Option<ITfComposition>,
    cursor: usize,
    done: Box<dyn Fn(Located)>,
) -> windows_core::Result<()> {
    crate::object_created();
    let es: ITfEditSession = ComObject::new(Locate {
        ctx: ctx.clone(),
        composition,
        cursor,
        done,
    })
    .into_interface();
    let hr = unsafe {
        ctx.RequestEditSession(
            client_id,
            &es,
            TF_ES_ASYNCDONTCARE | windows::Win32::UI::TextServices::TF_ES_READ,
        )
    }?;
    hr.ok()
}

/// 以同步的讀寫 edit session 套用快照；回傳套用後仍在進行的組字（沒有就是 None）與游標矩形。
/// 有組字時每一鍵都查游標矩形（同一次組字的位置退路要跟著游標走）；沒有組字時只有 `want_caret`
/// （這一鍵要顯示候選窗或提示，例如空組字的 Emoji 選單、提交後的提示）才查選取的插入點。
/// 同步不被允許時（例如失焦、停用時不在按鍵回呼內）：有提交文字就改排非同步 edit session，
/// 由它結束組字並寫入文字，回傳 composition=None；只是更新組字則保留原本的組字物件，讓外殼狀態一致。
/// 提交文字一旦寫過就不再改排：同一個 Apply 從頭重跑會在新的組字裡再插入一次（#88）。
pub fn apply(
    ctx: &ITfContext,
    client_id: u32,
    composition: Option<ITfComposition>,
    sink: ITfCompositionSink,
    snapshot: &Snapshot,
    attr_atoms: [u32; KINDS.len()],
    cookie: Option<u32>,
    ending: Rc<Cell<bool>>,
    want_caret: bool,
) -> Applied {
    crate::object_created();
    let obj = ComObject::new(Apply {
        ctx: ctx.clone(),
        sink,
        composition: RefCell::new(composition),
        commit: snapshot.commit.encode_utf16().collect(),
        preedit: snapshot.text().encode_utf16().collect(),
        cursor: snapshot.cursor_utf16,
        attr_atoms,
        styles: style_ranges(&snapshot.preedit).unwrap_or_default(),
        caret: RefCell::new(Err(Miss::NotRun)),
        want_caret,
        ending,
        step: Cell::new("not_run"),
        committed: Cell::new(false),
        done: Cell::new(false),
    });
    let es: ITfEditSession = obj.to_interface();
    zdebug!("RequestEditSession sync");
    let requested = match cookie {
        Some(ec) => unsafe {
            es.DoEditSession(ec)
                .map(|_| windows::Win32::Foundation::S_OK)
        },
        None => unsafe { ctx.RequestEditSession(client_id, &es, TF_ES_SYNC | TF_ES_READWRITE) },
    };
    let ok = match requested {
        Ok(hr) if hr.is_ok() => true,
        Ok(hr) => {
            zlog!("edit session 回傳 {:?}（步驟 {}）", hr, obj.step.get());
            false
        }
        Err(e) => {
            zlog!("RequestEditSession 失敗：{}（步驟 {}）", e.code(), obj.step.get());
            false
        }
    };
    // 有些宿主跑完 edit session 仍回報失敗；文件已經是快照的樣子，不能再跑一次。
    let ok = ok || obj.done.get();
    zdebug!("RequestEditSession done ok={}", ok);
    if ok {
        let composition = obj.composition.borrow().clone();
        let caret = *obj.caret.borrow();
        return Applied { composition, caret };
    }
    if obj.committed.get() {
        // 提交文字已寫進文件後才失敗（游標、結束組字或接著的新組字）：寧可少做後續，也不能重排
        // 再插入一次（#88）。剩下的組字物件照實回報，下一鍵再同步。
        zlog!("提交後 edit session 失敗（步驟 {}），不再重寫提交文字", obj.step.get());
        let composition = obj.composition.borrow().clone();
        return Applied {
            composition,
            caret: Err(Miss::NotRun),
        };
    }
    if snapshot.commit.is_empty() && !snapshot.preedit.is_empty() {
        let composition = obj.composition.borrow().clone();
        return Applied {
            composition,
            caret: Err(Miss::NotRun),
        };
    }
    // 提交不能丟：改排非同步 edit session（Apply 物件自己帶著組字與文字）。走到這裡表示
    // 提交文字還沒寫過：同步要求被拒（TF_E_SYNCHRONOUS）、在寫入前就失敗，或 SetText 失敗且讀回確認沒寫入。
    match unsafe { ctx.RequestEditSession(client_id, &es, TF_ES_ASYNCDONTCARE | TF_ES_READWRITE) } {
        Ok(hr) if hr.is_ok() => zlog!("提交改排非同步 edit session"),
        Ok(hr) => zlog!("非同步 edit session 回傳 {:?}，提交文字可能遺失", hr),
        Err(e) => zlog!("非同步 RequestEditSession 失敗：{}，提交文字可能遺失", e),
    }
    Applied {
        composition: None,
        caret: Err(Miss::NotRun),
    }
}

/// VT_I4 的 VARIANT（TfGuidAtom）。
fn variant_i4(value: i32) -> VARIANT {
    VARIANT {
        Anonymous: VARIANT_0 {
            Anonymous: ManuallyDrop::new(VARIANT_0_0 {
                vt: VT_I4,
                wReserved1: 0,
                wReserved2: 0,
                wReserved3: 0,
                Anonymous: VARIANT_0_0_0 { lVal: value },
            }),
        },
    }
}

impl Apply {
    /// 目前的組字；沒有就在插入點開始一個。
    unsafe fn ensure(&self, ec: u32) -> windows_core::Result<ITfComposition> {
        let existing = self.composition.borrow().clone();
        if let Some(c) = existing {
            return Ok(c);
        }
        let insert: ITfInsertAtSelection = self.ctx.cast()?;
        let range = insert.InsertTextAtSelection(ec, TF_IAS_QUERYONLY, &[])?;
        let contexts: ITfContextComposition = self.ctx.cast()?;
        let comp = contexts.StartComposition(ec, &range, &self.sink)?;
        *self.composition.borrow_mut() = Some(comp.clone());
        Ok(comp)
    }

    unsafe fn select(
        &self,
        ec: u32,
        range: &windows::Win32::UI::TextServices::ITfRange,
    ) -> windows_core::Result<()> {
        let mut sel = TF_SELECTION {
            range: ManuallyDrop::new(Some(range.clone())),
            style: TF_SELECTIONSTYLE {
                ase: TF_AE_NONE,
                fInterimChar: false.into(),
            },
        };
        let r = self.ctx.SetSelection(ec, std::slice::from_ref(&sel));
        ManuallyDrop::drop(&mut sel.range);
        r
    }

    /// 組字範圍的內容是否正好是提交文字（SetText 回報失敗時確認宿主到底寫了沒）。
    /// 緩衝區多留一格，範圍裡還有其他字就讀得出來、判為不符。
    unsafe fn holds_commit(&self, ec: u32, range: &ITfRange) -> windows_core::Result<bool> {
        let mut buf = vec![0u16; self.commit.len() + 1];
        let mut fetched = 0u32;
        range.GetText(ec, 0, &mut buf, &mut fetched)?;
        Ok(buf.get(..fetched as usize) == Some(&self.commit[..]))
    }

    unsafe fn end(&self, ec: u32, comp: &ITfComposition) -> windows_core::Result<()> {
        if let (Ok(prop), Ok(range)) = (self.ctx.GetProperty(&GUID_PROP_ATTRIBUTE), comp.GetRange())
        {
            let _ = prop.Clear(ec, &range);
        }
        let _ending = Ending::new(self.ending.clone());
        let r = comp.EndComposition(ec);
        *self.composition.borrow_mut() = None;
        r
    }
}

// 即使宿主回呼出錯或 panic，也恢復旗標，讓之後真正的外部取消照常處理。
struct Ending(Rc<Cell<bool>>, bool);
impl Ending {
    fn new(flag: Rc<Cell<bool>>) -> Self {
        let previous = flag.replace(true);
        Self(flag, previous)
    }
}
impl Drop for Ending {
    fn drop(&mut self) {
        self.0.set(self.1);
    }
}

impl ITfEditSession_Impl for Apply_Impl {
    fn DoEditSession(&self, ec: u32) -> windows_core::Result<()> {
        crate::guarded("DoEditSession", || {
            let result = unsafe { self.run(ec) };
            match &result {
                Ok(()) => self.done.set(true),
                Err(e) => zlog!("edit session 失敗：步驟 {} {}", self.step.get(), e.code()),
            }
            result
        })
    }
}

impl Apply {
    unsafe fn run(&self, ec: u32) -> windows_core::Result<()> {
        if !self.commit.is_empty() && !self.committed.get() {
            self.step.set("commit.start");
            let comp = self.ensure(ec)?;
            self.step.set("commit.range");
            let range = comp.GetRange()?;
            self.step.set("commit.text");
            if let Err(e) = range.SetText(ec, 0, &self.commit) {
                // 有些宿主寫入後仍回報失敗：用同一個 cookie 讀回組字範圍，內容就是提交文字才算
                // 已寫入（不能插兩次）；讀不回或不相符就照失敗處理，讓 apply 改排非同步重寫，
                // 提交文字不能遺失（#88）。
                match self.holds_commit(ec, &range) {
                    Ok(true) => zlog!("提交 SetText 回報失敗：{}，讀回確認已寫入", e.code()),
                    Ok(false) => {
                        zlog!("提交 SetText 失敗：{}，讀回內容不符，未寫入", e.code());
                        return Err(e);
                    }
                    Err(read) => {
                        zlog!(
                            "提交 SetText 失敗：{}，讀回失敗：{}，當作未寫入",
                            e.code(),
                            read.code()
                        );
                        return Err(e);
                    }
                }
            }
            self.committed.set(true);
            // 文字已在文件裡：放游標或結束組字失敗都只記錄，不讓整個 session 被當成失敗重來。
            self.step.set("commit.caret");
            let placed = range.Clone().and_then(|caret| {
                caret.Collapse(ec, TF_ANCHOR_END)?;
                self.select(ec, &caret)
            });
            if let Err(e) = placed {
                zlog!("提交後放置游標失敗：{}", e.code());
            }
            self.step.set("commit.end");
            if let Err(e) = self.end(ec, &comp) {
                zlog!("提交後結束組字失敗：{}", e.code());
            }
        }
        self.step.set("preedit");
        if !self.preedit.is_empty() {
            let comp = self.ensure(ec)?;
            let range = comp.GetRange()?;
            range.SetText(ec, 0, &self.preedit)?;
            if let Ok(prop) = self.ctx.GetProperty(&GUID_PROP_ATTRIBUTE) {
                // 先清掉上次組字的屬性，避免縮短或換樣式後殘留粗線。
                let _ = prop.Clear(ec, &range);
                for style in &self.styles {
                    let atom = self.attr_atoms[style.kind.index()];
                    if atom == 0 {
                        continue;
                    }
                    let part = range.Clone()?;
                    part.Collapse(ec, TF_ANCHOR_START)?;
                    let mut moved = 0;
                    part.ShiftEnd(ec, style.end, &mut moved, std::ptr::null())?;
                    if moved != style.end {
                        continue;
                    }
                    part.ShiftStart(ec, style.start, &mut moved, std::ptr::null())?;
                    if moved != style.start {
                        continue;
                    }
                    if let Err(e) = prop.SetValue(ec, &part, &variant_i4(atom as i32)) {
                        zlog!("顯示屬性設定失敗：{}", e);
                    }
                }
            }
            let caret = composition_caret(ec, &comp, self.cursor)?;
            self.select(ec, &caret)?;
            // 游標的螢幕座標：候選窗定位用，直接用剛放好的插入點。拿不到就記下原因，由外殼退回其他來源
            // 或等 OnLayoutChange。
            *self.caret.borrow_mut() = text_ext(&self.ctx, ec, &caret);
        } else {
            self.step.set("clear");
            let existing = self.composition.borrow().clone();
            if let Some(comp) = existing {
                let range = comp.GetRange()?;
                range.SetText(ec, 0, &[])?;
                self.end(ec, &comp)?;
            }
            // 空組字也能開 Emoji 選單、提交後也可能有提示：沒有組字範圍，從目前選取的插入點取得位置。
            if self.want_caret {
                *self.caret.borrow_mut() = selection_rect(&self.ctx, ec);
            }
        }
        Ok(())
    }
}


#[cfg(test)]
mod tests {
    //! 以假的 ITfContext／ITfRange／ITfComposition 模擬宿主，注入「同步 edit session 被拒」
    //! 「結束組字失敗」「跑完卻回報失敗」等情況，檢查提交文字只寫進文件一次（#88）；
    //! 假的 ITfContextView 模擬 Chromium 版面還沒回來（TS_E_NOLAYOUT）與空矩形（#144）。
    use super::*;
    use std::collections::VecDeque;

    use windows::Win32::Foundation::{E_FAIL, E_NOTIMPL, S_OK};
    use windows::Win32::System::Com::IDataObject;
    use windows::Win32::UI::TextServices::{
        IEnumITfCompositionView, IEnumTfContextViews, IEnumTfProperties, ITfCompositionSink_Impl,
        ITfCompositionView, ITfComposition_Impl, ITfContextComposition_Impl, ITfContextView, ITfContextView_Impl,
        ITfContext_Impl, TS_E_NOLAYOUT,
        ITfDocumentMgr, ITfInsertAtSelection_Impl, ITfProperty, ITfRange, ITfRangeBackup, ITfRange_Impl,
        ITfReadOnlyProperty, TfAnchor, TfGravity, TfShiftDir, INSERT_TEXT_AT_SELECTION_FLAGS,
        TF_CONTEXT_EDIT_CONTEXT_FLAGS, TF_E_SYNCHRONOUS, TF_HALTCOND, TF_S_ASYNC, TS_STATUS,
    };
    use windows_core::{AsImpl, IUnknown, Ref, BOOL, GUID, HRESULT, PCWSTR, PWSTR};

    /// 宿主怎麼處理同步（TF_ES_SYNC）的 edit session。
    #[derive(Clone, Copy)]
    enum Sync {
        /// 照常執行，回傳 DoEditSession 的結果。
        Run,
        /// 不在按鍵回呼內：不執行，回 TF_E_SYNCHRONOUS。
        Refuse,
        /// 執行了，卻回報失敗（RequestEditSession 本身回錯誤）。
        RunButReport,
    }

    /// 宿主的版面（GetActiveView／GetTextExt）。
    #[derive(Clone, Copy)]
    enum Layout {
        /// 沒有 view。
        NoView,
        /// Chromium 剛打的字還沒排版：GetTextExt 回 TS_E_NOLAYOUT。
        NotReady,
        /// GetTextExt 成功但矩形是空的。
        Empty,
        /// 排好了：第 n 個 UTF-16 位置的游標在 x = 100 + 10n。
        Ready,
    }

    /// 下一次 SetText 怎麼失敗（只作用一次）。
    #[derive(Clone, Copy)]
    enum SetTextFailure {
        /// 文字寫進去了，卻回報失敗。
        Written,
        /// 沒寫入就回報失敗。
        Dropped,
    }

    /// 假文件：UTF-16 文字與插入點，以及可注入的失敗。非同步 edit session 先排隊，由測試決定何時執行。
    struct Doc {
        text: RefCell<Vec<u16>>,
        caret: Cell<usize>,
        sync: Cell<Sync>,
        fail_end: Cell<bool>,
        fail_select: Cell<bool>,
        fail_set_text: Cell<Option<SetTextFailure>>,
        ended: Cell<u32>,
        /// GetText 被呼叫的次數（讀回確認）。
        reads: Cell<u32>,
        queued: RefCell<VecDeque<ITfEditSession>>,
        layout: Cell<Layout>,
        /// GetTextExt 與 GetSelection 被呼叫的次數（每鍵不做多餘的查詢）。
        extents: Cell<u32>,
        selections: Cell<u32>,
        /// GetStatus 的 dwDynamicFlags（欄位政策看 TF_SD_READONLY）。
        dynamic: Cell<u32>,
    }

    struct Span {
        start: Cell<usize>,
        end: Cell<usize>,
    }

    fn span(start: usize, end: usize) -> Rc<Span> {
        Rc::new(Span { start: Cell::new(start), end: Cell::new(end) })
    }

    #[implement(ITfRange)]
    struct FakeRange {
        doc: Rc<Doc>,
        span: Rc<Span>,
    }

    fn range(doc: &Rc<Doc>, span: Rc<Span>) -> ITfRange {
        ComObject::new(FakeRange { doc: doc.clone(), span }).into_interface()
    }

    impl ITfRange_Impl for FakeRange_Impl {
        fn GetText(&self, _: u32, _: u32, out: PWSTR, cch: u32, fetched: *mut u32) -> windows_core::Result<()> {
            self.doc.reads.set(self.doc.reads.get() + 1);
            let text = self.doc.text.borrow();
            let part = &text[self.span.start.get()..self.span.end.get()];
            let n = part.len().min(cch as usize);
            unsafe {
                std::ptr::copy_nonoverlapping(part.as_ptr(), out.0, n);
                *fetched = n as u32;
            }
            Ok(())
        }
        fn SetText(&self, _: u32, _: u32, text: &PCWSTR, cch: i32) -> windows_core::Result<()> {
            let failure = self.doc.fail_set_text.take();
            if let Some(SetTextFailure::Dropped) = failure {
                return Err(E_FAIL.into());
            }
            let new: Vec<u16> =
                if cch > 0 { unsafe { std::slice::from_raw_parts(text.0, cch as usize) }.to_vec() } else { Vec::new() };
            let (s, e) = (self.span.start.get(), self.span.end.get());
            self.doc.text.borrow_mut().splice(s..e, new.iter().copied());
            self.span.end.set(s + new.len());
            match failure {
                Some(SetTextFailure::Written) => Err(E_FAIL.into()),
                _ => Ok(()),
            }
        }
        fn GetFormattedText(&self, _: u32) -> windows_core::Result<IDataObject> {
            Err(E_NOTIMPL.into())
        }
        fn GetEmbedded(&self, _: u32, _: *const GUID, _: *const GUID) -> windows_core::Result<IUnknown> {
            Err(E_NOTIMPL.into())
        }
        fn InsertEmbedded(&self, _: u32, _: u32, _: Ref<'_, IDataObject>) -> windows_core::Result<()> {
            Err(E_NOTIMPL.into())
        }
        fn ShiftStart(&self, _: u32, cch: i32, moved: *mut i32, _: *const TF_HALTCOND) -> windows_core::Result<()> {
            let from = self.span.start.get() as i32;
            let to = (from + cch).clamp(0, self.span.end.get() as i32);
            self.span.start.set(to as usize);
            unsafe { moved.write(to - from) };
            Ok(())
        }
        fn ShiftEnd(&self, _: u32, cch: i32, moved: *mut i32, _: *const TF_HALTCOND) -> windows_core::Result<()> {
            let from = self.span.end.get() as i32;
            let to = (from + cch).clamp(self.span.start.get() as i32, self.doc.text.borrow().len() as i32);
            self.span.end.set(to as usize);
            unsafe { moved.write(to - from) };
            Ok(())
        }
        fn ShiftStartToRange(&self, _: u32, _: Ref<'_, ITfRange>, _: TfAnchor) -> windows_core::Result<()> {
            Err(E_NOTIMPL.into())
        }
        fn ShiftEndToRange(&self, _: u32, _: Ref<'_, ITfRange>, _: TfAnchor) -> windows_core::Result<()> {
            Err(E_NOTIMPL.into())
        }
        fn ShiftStartRegion(&self, _: u32, _: TfShiftDir) -> windows_core::Result<BOOL> {
            Err(E_NOTIMPL.into())
        }
        fn ShiftEndRegion(&self, _: u32, _: TfShiftDir) -> windows_core::Result<BOOL> {
            Err(E_NOTIMPL.into())
        }
        fn IsEmpty(&self, _: u32) -> windows_core::Result<BOOL> {
            Ok((self.span.start.get() == self.span.end.get()).into())
        }
        fn Collapse(&self, _: u32, anchor: TfAnchor) -> windows_core::Result<()> {
            if anchor == TF_ANCHOR_START {
                self.span.end.set(self.span.start.get());
            } else {
                self.span.start.set(self.span.end.get());
            }
            Ok(())
        }
        fn IsEqualStart(&self, _: u32, _: Ref<'_, ITfRange>, _: TfAnchor) -> windows_core::Result<BOOL> {
            Err(E_NOTIMPL.into())
        }
        fn IsEqualEnd(&self, _: u32, _: Ref<'_, ITfRange>, _: TfAnchor) -> windows_core::Result<BOOL> {
            Err(E_NOTIMPL.into())
        }
        fn CompareStart(&self, _: u32, _: Ref<'_, ITfRange>, _: TfAnchor) -> windows_core::Result<i32> {
            Err(E_NOTIMPL.into())
        }
        fn CompareEnd(&self, _: u32, _: Ref<'_, ITfRange>, _: TfAnchor) -> windows_core::Result<i32> {
            Err(E_NOTIMPL.into())
        }
        fn AdjustForInsert(&self, _: u32, _: u32) -> windows_core::Result<BOOL> {
            Err(E_NOTIMPL.into())
        }
        fn GetGravity(&self, _: *mut TfGravity, _: *mut TfGravity) -> windows_core::Result<()> {
            Err(E_NOTIMPL.into())
        }
        fn SetGravity(&self, _: u32, _: TfGravity, _: TfGravity) -> windows_core::Result<()> {
            Err(E_NOTIMPL.into())
        }
        fn Clone(&self) -> windows_core::Result<ITfRange> {
            Ok(range(&self.doc, span(self.span.start.get(), self.span.end.get())))
        }
        fn GetContext(&self) -> windows_core::Result<ITfContext> {
            Err(E_NOTIMPL.into())
        }
    }

    #[implement(ITfComposition)]
    struct FakeComposition {
        doc: Rc<Doc>,
        span: Rc<Span>,
    }

    impl ITfComposition_Impl for FakeComposition_Impl {
        fn GetRange(&self) -> windows_core::Result<ITfRange> {
            Ok(range(&self.doc, self.span.clone()))
        }
        fn ShiftStart(&self, _: u32, _: Ref<'_, ITfRange>) -> windows_core::Result<()> {
            Ok(())
        }
        fn ShiftEnd(&self, _: u32, _: Ref<'_, ITfRange>) -> windows_core::Result<()> {
            Ok(())
        }
        fn EndComposition(&self, _: u32) -> windows_core::Result<()> {
            if self.doc.fail_end.get() {
                return Err(E_FAIL.into());
            }
            self.doc.ended.set(self.doc.ended.get() + 1);
            Ok(())
        }
    }

    #[implement(ITfContext, ITfInsertAtSelection, ITfContextComposition)]
    struct FakeContext {
        doc: Rc<Doc>,
    }

    impl ITfContext_Impl for FakeContext_Impl {
        fn RequestEditSession(
            &self,
            _: u32,
            session: Ref<'_, ITfEditSession>,
            flags: TF_CONTEXT_EDIT_CONTEXT_FLAGS,
        ) -> windows_core::Result<HRESULT> {
            let session = session.ok()?.clone();
            if flags.0 & TF_ES_SYNC.0 == 0 {
                self.doc.queued.borrow_mut().push_back(session);
                return Ok(TF_S_ASYNC);
            }
            match self.doc.sync.get() {
                Sync::Refuse => Ok(TF_E_SYNCHRONOUS),
                Sync::Run => Ok(match unsafe { session.DoEditSession(1) } {
                    Ok(()) => S_OK,
                    Err(e) => e.code(),
                }),
                Sync::RunButReport => {
                    let _ = unsafe { session.DoEditSession(1) };
                    Err(E_FAIL.into())
                }
            }
        }
        fn InWriteSession(&self, _: u32) -> windows_core::Result<BOOL> {
            Err(E_NOTIMPL.into())
        }
        fn GetSelection(&self, _: u32, _: u32, _: u32, _: *mut TF_SELECTION, _: *mut u32) -> windows_core::Result<()> {
            self.doc.selections.set(self.doc.selections.get() + 1);
            Err(E_NOTIMPL.into())
        }
        fn SetSelection(&self, _: u32, count: u32, selection: *const TF_SELECTION) -> windows_core::Result<()> {
            if self.doc.fail_select.get() || count != 1 {
                return Err(E_FAIL.into());
            }
            let r = unsafe { (*selection).range.as_ref() }.ok_or(windows_core::Error::from(E_FAIL))?;
            let r: &FakeRange = unsafe { r.as_impl() };
            self.doc.caret.set(r.span.end.get());
            Ok(())
        }
        fn GetStart(&self, _: u32) -> windows_core::Result<ITfRange> {
            Err(E_NOTIMPL.into())
        }
        fn GetEnd(&self, _: u32) -> windows_core::Result<ITfRange> {
            Err(E_NOTIMPL.into())
        }
        fn GetActiveView(&self) -> windows_core::Result<ITfContextView> {
            match self.doc.layout.get() {
                Layout::NoView => Err(E_NOTIMPL.into()),
                _ => Ok(ComObject::new(FakeView { doc: self.doc.clone() }).into_interface()),
            }
        }
        fn EnumViews(&self) -> windows_core::Result<IEnumTfContextViews> {
            Err(E_NOTIMPL.into())
        }
        fn GetStatus(&self) -> windows_core::Result<TS_STATUS> {
            Ok(TS_STATUS {
                dwDynamicFlags: self.doc.dynamic.get(),
                dwStaticFlags: 0,
            })
        }
        fn GetProperty(&self, _: *const GUID) -> windows_core::Result<ITfProperty> {
            Err(E_NOTIMPL.into())
        }
        fn GetAppProperty(&self, _: *const GUID) -> windows_core::Result<ITfReadOnlyProperty> {
            Err(E_NOTIMPL.into())
        }
        fn TrackProperties(
            &self,
            _: *const *const GUID,
            _: u32,
            _: *const *const GUID,
            _: u32,
        ) -> windows_core::Result<ITfReadOnlyProperty> {
            Err(E_NOTIMPL.into())
        }
        fn EnumProperties(&self) -> windows_core::Result<IEnumTfProperties> {
            Err(E_NOTIMPL.into())
        }
        fn GetDocumentMgr(&self) -> windows_core::Result<ITfDocumentMgr> {
            Err(E_NOTIMPL.into())
        }
        fn CreateRangeBackup(&self, _: u32, _: Ref<'_, ITfRange>) -> windows_core::Result<ITfRangeBackup> {
            Err(E_NOTIMPL.into())
        }
    }

    impl ITfInsertAtSelection_Impl for FakeContext_Impl {
        fn InsertTextAtSelection(
            &self,
            _: u32,
            _: INSERT_TEXT_AT_SELECTION_FLAGS,
            _: &PCWSTR,
            _: i32,
        ) -> windows_core::Result<ITfRange> {
            let at = self.doc.caret.get();
            Ok(range(&self.doc, span(at, at)))
        }
        fn InsertEmbeddedAtSelection(&self, _: u32, _: u32, _: Ref<'_, IDataObject>) -> windows_core::Result<ITfRange> {
            Err(E_NOTIMPL.into())
        }
    }

    impl ITfContextComposition_Impl for FakeContext_Impl {
        fn StartComposition(
            &self,
            _: u32,
            at: Ref<'_, ITfRange>,
            _: Ref<'_, ITfCompositionSink>,
        ) -> windows_core::Result<ITfComposition> {
            let at: &FakeRange = unsafe { at.ok()?.as_impl() };
            Ok(ComObject::new(FakeComposition {
                doc: self.doc.clone(),
                span: span(at.span.start.get(), at.span.end.get()),
            })
            .into_interface())
        }
        fn EnumCompositions(&self) -> windows_core::Result<IEnumITfCompositionView> {
            Err(E_NOTIMPL.into())
        }
        fn FindComposition(&self, _: u32, _: Ref<'_, ITfRange>) -> windows_core::Result<IEnumITfCompositionView> {
            Err(E_NOTIMPL.into())
        }
        fn TakeOwnership(
            &self,
            _: u32,
            _: Ref<'_, ITfCompositionView>,
            _: Ref<'_, ITfCompositionSink>,
        ) -> windows_core::Result<ITfComposition> {
            Err(E_NOTIMPL.into())
        }
    }

    #[implement(ITfContextView)]
    struct FakeView {
        doc: Rc<Doc>,
    }

    fn caret_at(pos: usize) -> RECT {
        let left = 100 + 10 * pos as i32;
        RECT { left, top: 200, right: left + 1, bottom: 220 }
    }

    impl ITfContextView_Impl for FakeView_Impl {
        fn GetRangeFromPoint(&self, _: u32, _: *const windows::Win32::Foundation::POINT, _: u32) -> windows_core::Result<ITfRange> {
            Err(E_NOTIMPL.into())
        }
        fn GetTextExt(&self, _: u32, range: Ref<'_, ITfRange>, rc: *mut RECT, clipped: *mut BOOL) -> windows_core::Result<()> {
            let range: &FakeRange = unsafe { range.ok()?.as_impl() };
            self.doc.extents.set(self.doc.extents.get() + 1);
            let rect = match self.doc.layout.get() {
                Layout::NotReady => return Err(TS_E_NOLAYOUT.into()),
                Layout::Empty | Layout::NoView => RECT::default(),
                Layout::Ready => caret_at(range.span.end.get()),
            };
            unsafe {
                rc.write(rect);
                clipped.write(BOOL(0));
            }
            Ok(())
        }
        fn GetScreenExt(&self) -> windows_core::Result<RECT> {
            Ok(RECT { left: 50, top: 180, right: 600, bottom: 240 })
        }
        fn GetWnd(&self) -> windows_core::Result<windows::Win32::Foundation::HWND> {
            Ok(windows::Win32::Foundation::HWND::default())
        }
    }

    #[implement(ITfCompositionSink)]
    struct NullSink;
    impl ITfCompositionSink_Impl for NullSink_Impl {
        fn OnCompositionTerminated(&self, _: u32, _: Ref<'_, ITfComposition>) -> windows_core::Result<()> {
            Ok(())
        }
    }

    /// 文件原本是「前」、插入點在它後面。
    fn host(sync: Sync) -> (Rc<Doc>, ITfContext) {
        let doc = Rc::new(Doc {
            text: RefCell::new("前".encode_utf16().collect()),
            caret: Cell::new(1),
            sync: Cell::new(sync),
            fail_end: Cell::new(false),
            fail_select: Cell::new(false),
            fail_set_text: Cell::new(None),
            ended: Cell::new(0),
            reads: Cell::new(0),
            queued: RefCell::new(VecDeque::new()),
            layout: Cell::new(Layout::NoView),
            extents: Cell::new(0),
            selections: Cell::new(0),
            dynamic: Cell::new(0),
        });
        let ctx = ComObject::new(FakeContext { doc: doc.clone() }).into_interface();
        (doc, ctx)
    }

    /// 選字後的快照：提交「你好」，沒有新組字。
    fn commit(ctx: &ITfContext, cookie: Option<u32>) -> Applied {
        commit_showing(ctx, cookie, false)
    }

    /// 同上；want_caret：這一鍵還要顯示提示（例如「已加入詞庫」），要查選取的插入點。
    fn commit_showing(ctx: &ITfContext, cookie: Option<u32>, want_caret: bool) -> Applied {
        let snapshot = Snapshot {
            ok: true,
            handled: true,
            commit: "你好".into(),
            ..Default::default()
        };
        let sink: ITfCompositionSink = ComObject::new(NullSink).into_interface();
        apply(ctx, 1, None, sink, &snapshot, [0; KINDS.len()], cookie, Rc::new(Cell::new(false)), want_caret)
    }

    /// 執行排隊的非同步 edit session（宿主稍後才給寫入權限）。
    fn drain(doc: &Doc) {
        loop {
            let next = doc.queued.borrow_mut().pop_front();
            let Some(session) = next else { break };
            let _ = unsafe { session.DoEditSession(2) };
        }
    }

    fn text(doc: &Doc) -> String {
        String::from_utf16(&doc.text.borrow()).unwrap()
    }

    #[test]
    fn end_composition_failure_does_not_insert_commit_again() {
        // 修正前：結束組字失敗讓整個 session 改排非同步從頭重跑，在新組字裡再插入一次「你好」。
        let (doc, ctx) = host(Sync::Run);
        doc.fail_end.set(true);
        let applied = commit(&ctx, None);
        drain(&doc);
        assert_eq!(text(&doc), "前你好");
        assert!(applied.composition.is_none());
    }

    #[test]
    fn host_reporting_failure_after_running_session_is_not_retried() {
        let (doc, ctx) = host(Sync::RunButReport);
        commit(&ctx, None);
        drain(&doc);
        assert_eq!(text(&doc), "前你好");
        assert_eq!(doc.ended.get(), 1);
    }

    #[test]
    fn candidate_click_in_edit_session_commits_once_when_end_fails() {
        // 滑鼠選字：在宿主給的 edit cookie 內直接執行（select_in_edit），同樣不能重排。
        let (doc, ctx) = host(Sync::Run);
        doc.fail_end.set(true);
        commit(&ctx, Some(7));
        drain(&doc);
        assert_eq!(text(&doc), "前你好");
    }

    #[test]
    fn caret_failure_after_commit_still_ends_composition_once() {
        let (doc, ctx) = host(Sync::Run);
        doc.fail_select.set(true);
        let applied = commit(&ctx, None);
        drain(&doc);
        assert_eq!(text(&doc), "前你好");
        assert_eq!(doc.ended.get(), 1);
        assert!(applied.composition.is_none());
    }

    #[test]
    fn refused_sync_session_still_commits_once_asynchronously() {
        // 失焦、停用時同步 edit session 不被允許：提交不能丟，改排非同步寫一次。
        let (doc, ctx) = host(Sync::Refuse);
        commit(&ctx, None);
        assert_eq!(text(&doc), "前", "非同步 session 尚未執行");
        assert_eq!(doc.queued.borrow().len(), 1);
        drain(&doc);
        assert_eq!(text(&doc), "前你好");
        assert_eq!(doc.ended.get(), 1);
    }

    #[test]
    fn successful_set_text_commits_once_without_reading_back() {
        let (doc, ctx) = host(Sync::Run);
        let applied = commit(&ctx, None);
        drain(&doc);
        assert_eq!(text(&doc), "前你好");
        assert_eq!(doc.ended.get(), 1);
        assert_eq!(doc.reads.get(), 0, "SetText 成功不需要讀回");
        assert!(applied.composition.is_none());
    }

    #[test]
    fn set_text_reporting_failure_after_writing_is_not_retried() {
        // 宿主寫入後仍回報失敗：讀回確認已寫入，照常結束組字，不改排非同步、不插第二次。
        let (doc, ctx) = host(Sync::Run);
        doc.fail_set_text.set(Some(SetTextFailure::Written));
        let applied = commit(&ctx, None);
        assert!(doc.queued.borrow().is_empty(), "已寫入就不該改排非同步");
        drain(&doc);
        assert_eq!(text(&doc), "前你好");
        assert_eq!(doc.ended.get(), 1);
        assert_eq!(doc.reads.get(), 1);
        assert!(applied.composition.is_none());
    }

    #[test]
    fn set_text_failing_without_writing_is_retried_asynchronously_once() {
        // 修正前：SetText 之前就標記已提交，SetText 失敗又沒寫入時不再改排，提交文字遺失。
        let (doc, ctx) = host(Sync::Run);
        doc.fail_set_text.set(Some(SetTextFailure::Dropped));
        let applied = commit(&ctx, None);
        assert_eq!(text(&doc), "前", "非同步 session 尚未執行");
        assert_eq!(doc.queued.borrow().len(), 1);
        assert!(applied.composition.is_none());
        drain(&doc);
        assert_eq!(text(&doc), "前你好");
        assert_eq!(doc.ended.get(), 1);
    }

    #[test]
    fn commit_then_new_preedit_keeps_both_in_order() {
        let (doc, ctx) = host(Sync::Run);
        let snapshot = Snapshot {
            ok: true,
            handled: true,
            commit: "你好".into(),
            preedit: vec![crate::session::Segment { text: "ㄇ".into(), style: "normal".into() }],
            cursor_utf16: 1,
            ..Default::default()
        };
        let sink: ITfCompositionSink = ComObject::new(NullSink).into_interface();
        let applied = apply(&ctx, 1, None, sink, &snapshot, [0; KINDS.len()], None, Rc::new(Cell::new(false)), false);
        drain(&doc);
        assert_eq!(text(&doc), "前你好ㄇ");
        assert!(applied.composition.is_some());
    }

    /// 組字「ㄋㄧ」、游標在 cursor。
    fn preedit(ctx: &ITfContext, cursor: usize) -> Applied {
        let snapshot = Snapshot {
            ok: true,
            handled: true,
            preedit: vec![crate::session::Segment { text: "ㄋㄧ".into(), style: "normal".into() }],
            cursor_utf16: cursor,
            ..Default::default()
        };
        let sink: ITfCompositionSink = ComObject::new(NullSink).into_interface();
        apply(ctx, 1, None, sink, &snapshot, [0; KINDS.len()], None, Rc::new(Cell::new(false)), false)
    }

    #[test]
    fn caret_rect_follows_the_cursor_inside_the_composition() {
        let (doc, ctx) = host(Sync::Run);
        doc.layout.set(Layout::Ready);
        // 「前」之後開始組字，游標在組字的第 1 個字後面 = 文件位置 2。
        let applied = preedit(&ctx, 1);
        assert_eq!(text(&doc), "前ㄋㄧ");
        assert_eq!(applied.caret, Ok(caret_at(2)));
        // 組字中每一鍵都查（位置退路要跟著游標），但只查剛放好的插入點一次，不另外讀選取。
        assert_eq!((doc.extents.get(), doc.selections.get()), (1, 0));
    }

    #[test]
    fn commit_without_composition_queries_the_caret_only_when_something_is_shown() {
        // 修正前：每一個只提交、不顯示任何東西的按鍵都多做一次 GetSelection 與 GetTextExt。
        let (doc, ctx) = host(Sync::Run);
        doc.layout.set(Layout::Ready);
        let applied = commit(&ctx, None);
        assert_eq!(text(&doc), "前你好");
        assert_eq!(applied.caret, Err(Miss::NotRun));
        assert_eq!((doc.extents.get(), doc.selections.get()), (0, 0));
        // 提交後要顯示提示：查選取的插入點（假宿主不支援 GetSelection，回報原因）。
        let (doc, ctx) = host(Sync::Run);
        doc.layout.set(Layout::Ready);
        let applied = commit_showing(&ctx, None, true);
        assert_eq!(applied.caret, Err(Miss::Range(E_NOTIMPL)));
        assert_eq!(doc.selections.get(), 1);
    }

    #[test]
    fn missing_layout_is_reported_instead_of_silently_dropping_the_position() {
        // 修正前：GetTextExt 失敗只留 None，外殼的 GetGUIThreadInfo 退路在 Chromium 也落空，
        // 候選窗靜默不顯示，tsf.log 什麼都沒有（#144）。
        let (doc, ctx) = host(Sync::Run);
        doc.layout.set(Layout::NotReady);
        let applied = preedit(&ctx, 2);
        assert_eq!(text(&doc), "前ㄋㄧ", "拿不到位置不影響組字");
        assert!(applied.composition.is_some());
        assert_eq!(applied.caret, Err(Miss::Failed(TS_E_NOLAYOUT)));
        assert_eq!(Miss::Failed(TS_E_NOLAYOUT).to_string(), "GetTextExt TS_E_NOLAYOUT");
        // 修正前：失敗後再讀選取、再查一次 GetTextExt（選取就是同一個插入點，結果相同）。
        assert_eq!((doc.extents.get(), doc.selections.get()), (1, 0));

        let (doc, ctx) = host(Sync::Run);
        doc.layout.set(Layout::Empty);
        let applied = preedit(&ctx, 2);
        assert!(matches!(applied.caret, Err(Miss::Empty(_))));
        // 去重類別不含座標：不同的空矩形算同一種情況。
        assert_eq!(Miss::Empty(RECT { left: 5, ..Default::default() }).class(), Miss::Empty(RECT::default()).class());

        let (_doc, ctx) = host(Sync::Run);
        let applied = preedit(&ctx, 2);
        assert!(matches!(applied.caret, Err(Miss::NoView(_))));
    }

    #[test]
    fn layout_change_requery_finds_the_caret_once_the_layout_arrives() {
        // Chromium：按鍵當下 TS_E_NOLAYOUT，renderer 排好版後送 OnLayoutChange；外殼這時以唯讀
        // edit session 重查，要查到同一個游標位置（組字起點 1 + 游標 2 = 文件位置 3）。
        let (doc, ctx) = host(Sync::Run);
        doc.layout.set(Layout::NotReady);
        let applied = preedit(&ctx, 2);
        assert!(applied.caret.is_err());
        doc.layout.set(Layout::Ready);
        let found: Rc<Cell<Option<Located>>> = Rc::new(Cell::new(None));
        let sink = found.clone();
        request_locate(&ctx, 1, applied.composition.clone(), 2, Box::new(move |l| sink.set(Some(l)))).unwrap();
        assert_eq!(found.get(), None, "唯讀 session 由宿主排程（非同步）");
        drain(&doc);
        assert_eq!(found.get(), Some(Ok(caret_at(3))));
        assert_eq!(text(&doc), "前ㄋㄧ", "重查不改文件");
    }

    #[test]
    fn layout_change_requery_without_composition_uses_the_selection_or_reports_why() {
        // 空組字（Emoji 選單）沒有組字範圍：假宿主不支援 GetSelection，回報範圍錯誤而不是 panic。
        let (doc, ctx) = host(Sync::Run);
        doc.layout.set(Layout::Ready);
        let found: Rc<Cell<Option<Located>>> = Rc::new(Cell::new(None));
        let sink = found.clone();
        request_locate(&ctx, 1, None, 0, Box::new(move |l| sink.set(Some(l)))).unwrap();
        drain(&doc);
        assert_eq!(found.get(), Some(Err(Miss::Range(E_NOTIMPL))));
    }

    #[test]
    fn ctrl_space_toggles_when_the_host_refuses_a_sync_read_lock() {
        // #164：Word、Outlook 收到系統的 Ctrl＋Space 熱鍵當下拒絕同步唯讀鎖（TS_E_SYNCHRONOUS）。
        // 按鍵仍當成讀不到欄位而不送引擎，但熱鍵切換不讀文件、照常切換。
        use crate::privacy::{field, hotkey_allowed, Field};
        let (_, refusing) = host(Sync::Refuse);
        assert_eq!(field(Some(&refusing), 1, false), Field::Blocked);
        assert!(hotkey_allowed(Some(&refusing), 1, false));
        // 給鎖的宿主：沒有 InputScope 屬性的一般欄位，按鍵與熱鍵都照常。
        let (_, open) = host(Sync::Run);
        assert_eq!(field(Some(&open), 1, false), Field::Open);
        assert!(hotkey_allowed(Some(&open), 1, false));
        // 唯讀的欄位：不論給不給鎖都不切換。
        for sync in [Sync::Run, Sync::Refuse] {
            let (doc, readonly) = host(sync);
            doc.dynamic.set(windows::Win32::UI::TextServices::TF_SD_READONLY);
            assert_eq!(field(Some(&readonly), 1, false), Field::Blocked);
            assert!(!hotkey_allowed(Some(&readonly), 1, false));
        }
        assert!(!hotkey_allowed(None, 1, false));
    }
}
