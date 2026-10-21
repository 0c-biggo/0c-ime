//! #189：以模擬的文件、context 與組字走一次 ITfThreadMgrEventSink::OnSetFocus，確認焦點變化時組字送到哪個 context。
//! WPF 與原生 EDIT 在焦點移開時自己結束組字（OnCompositionTerminated），桌面測試走不到這條路；Excel 不會結束組字。
use super::*;
use windows::Win32::Foundation::E_NOTIMPL;
use windows::Win32::System::Com::IDataObject;
use windows::Win32::UI::TextServices::{
    IEnumTfContextViews, IEnumTfContexts, IEnumTfProperties, ITfComposition_Impl, ITfContext_Impl,
    ITfDocumentMgr_Impl, ITfEditSession, ITfProperty, ITfRange, ITfRangeBackup, ITfRange_Impl,
    ITfReadOnlyProperty, TfAnchor, TfGravity, TfShiftDir, TF_CONTEXT_EDIT_CONTEXT_FLAGS, TF_HALTCOND,
    TF_SELECTION, TS_STATUS,
};
use windows_core::{IUnknown, OutRef, HRESULT, PCWSTR, PWSTR};

type R<T> = windows_core::Result<T>;
fn no<T>() -> R<T> {
    Err(E_NOTIMPL.into())
}

#[implement(ITfDocumentMgr)]
struct Doc {
    top: RefCell<Option<ITfContext>>,
}
impl ITfDocumentMgr_Impl for Doc_Impl {
    fn CreateContext(&self, _: u32, _: u32, _: Ref<'_, IUnknown>, _: OutRef<'_, ITfContext>, _: *mut u32) -> R<()> {
        no()
    }
    fn Push(&self, _: Ref<'_, ITfContext>) -> R<()> {
        no()
    }
    fn Pop(&self, _: u32) -> R<()> {
        no()
    }
    fn GetTop(&self) -> R<ITfContext> {
        self.top.borrow().clone().ok_or_else(|| E_NOTIMPL.into())
    }
    fn GetBase(&self) -> R<ITfContext> {
        self.GetTop()
    }
    fn EnumContexts(&self) -> R<IEnumTfContexts> {
        no()
    }
}

/// 只記下有幾次編輯要求，不真的編輯。
#[implement(ITfContext)]
struct Ctx {
    doc: ITfDocumentMgr,
    edits: Rc<Cell<u32>>,
}
impl ITfContext_Impl for Ctx_Impl {
    fn RequestEditSession(&self, _: u32, _: Ref<'_, ITfEditSession>, _: TF_CONTEXT_EDIT_CONTEXT_FLAGS) -> R<HRESULT> {
        self.edits.set(self.edits.get() + 1);
        Ok(E_NOTIMPL)
    }
    fn InWriteSession(&self, _: u32) -> R<BOOL> {
        no()
    }
    fn GetSelection(&self, _: u32, _: u32, _: u32, _: *mut TF_SELECTION, _: *mut u32) -> R<()> {
        no()
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
        no()
    }
    fn EnumViews(&self) -> R<IEnumTfContextViews> {
        no()
    }
    fn GetStatus(&self) -> R<TS_STATUS> {
        no()
    }
    fn GetProperty(&self, _: *const GUID) -> R<ITfProperty> {
        no()
    }
    fn GetAppProperty(&self, _: *const GUID) -> R<ITfReadOnlyProperty> {
        no()
    }
    fn TrackProperties(&self, _: *const *const GUID, _: u32, _: *const *const GUID, _: u32) -> R<ITfReadOnlyProperty> {
        no()
    }
    fn EnumProperties(&self) -> R<IEnumTfProperties> {
        no()
    }
    fn GetDocumentMgr(&self) -> R<ITfDocumentMgr> {
        Ok(self.doc.clone())
    }
    fn CreateRangeBackup(&self, _: u32, _: Ref<'_, ITfRange>) -> R<ITfRangeBackup> {
        no()
    }
}

/// 組字範圍：只回答它在哪個 context。
#[implement(ITfRange)]
struct Range {
    ctx: ITfContext,
}
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
        Ok(self.ctx.clone())
    }
}

#[implement(ITfComposition)]
struct Comp {
    range: ITfRange,
}
impl ITfComposition_Impl for Comp_Impl {
    fn GetRange(&self) -> R<ITfRange> {
        Ok(self.range.clone())
    }
    fn ShiftStart(&self, _: u32, _: Ref<'_, ITfRange>) -> R<()> {
        no()
    }
    fn ShiftEnd(&self, _: u32, _: Ref<'_, ITfRange>) -> R<()> {
        no()
    }
    fn EndComposition(&self, _: u32) -> R<()> {
        no()
    }
}

/// 一個文件與它最上層的 context；回傳 context 收到的編輯要求次數。
fn document() -> (ITfDocumentMgr, ITfContext, Rc<Cell<u32>>) {
    let doc = ComObject::new(Doc { top: RefCell::new(None) });
    let edits = Rc::new(Cell::new(0));
    let ctx: ITfContext = ComObject::new(Ctx {
        doc: doc.to_interface(),
        edits: edits.clone(),
    })
    .into_interface();
    doc.top.replace(Some(ctx.clone()));
    (doc.to_interface(), ctx, edits)
}

/// 在 A 組字（打了ㄋ），回傳服務、A、B 與兩邊的編輯要求次數。沒有測試引擎時回傳 None。
fn composing_in_a() -> Option<(ComObject<TextService>, ITfDocumentMgr, Rc<Cell<u32>>, ITfDocumentMgr, Rc<Cell<u32>>)> {
    let pipe = std::env::var("ZUIN_TEST_PIPE").ok()?;
    let service = ComObject::new(TextService::new());
    let (doc_a, ctx_a, edits_a) = document();
    let (doc_b, _ctx_b, edits_b) = document();
    let range: ITfRange = ComObject::new(Range { ctx: ctx_a }).into_interface();
    let composition: ITfComposition = ComObject::new(Comp { range }).into_interface();
    let window = CandidateWindow::new(&service).expect("回呼視窗");
    {
        let mut st = service.state.borrow_mut();
        st.session = Session::with_client(crate::ipc::Client::with_pipe(&pipe));
        assert_eq!(st.session.key("s").text(), "ㄋ");
        st.composing = true;
        st.composition = Some(composition);
        st.callback = Some(window);
    }
    Some((service, doc_a, edits_a, doc_b, edits_b))
}

#[test]
fn focus_to_null_then_another_document_commits_to_the_composing_document() {
    // 發版前 code review（#189 修正後的回歸）：組字中焦點 A → NULL → B，第二次焦點變化在確認之前（50ms 內）就來了。
    // 修正前第二次拿它的原文件（NULL）去送出，沒有 context，打到一半的字就丟了。
    let Some((service, doc_a, edits_a, doc_b, edits_b)) = composing_in_a() else {
        return;
    };
    let sink: ITfThreadMgrEventSink = service.to_interface();
    unsafe { sink.OnSetFocus(None::<&ITfDocumentMgr>, &doc_a) }.unwrap();
    assert_eq!(edits_a.get(), 0, "焦點移到 NULL 先不送出（Excel 的交接）");
    assert!(service.state.borrow().composing);
    unsafe { sink.OnSetFocus(&doc_b, None::<&ITfDocumentMgr>) }.unwrap();
    assert!(edits_a.get() > 0, "組字要送到 A"); // 同步編輯被拒時另以非同步再要求一次，次數不一定是 1
    assert_eq!(edits_b.get(), 0, "不送到 B");
    service.state.borrow_mut().callback = None;
}

#[test]
fn focus_handed_to_the_composing_document_keeps_composing() {
    // #189 原本的情況：A → NULL → A（宿主把組字所在的文件交回焦點）：不送出、繼續組字。
    let Some((service, doc_a, edits_a, _doc_b, _)) = composing_in_a() else {
        return;
    };
    let sink: ITfThreadMgrEventSink = service.to_interface();
    unsafe { sink.OnSetFocus(None::<&ITfDocumentMgr>, &doc_a) }.unwrap();
    unsafe { sink.OnSetFocus(&doc_a, None::<&ITfDocumentMgr>) }.unwrap();
    assert_eq!(edits_a.get(), 0);
    assert!(service.state.borrow().composing);
    service.state.borrow_mut().callback = None;
}

#[test]
fn switching_documents_directly_commits_to_the_previous() {
    let Some((service, doc_a, edits_a, doc_b, edits_b)) = composing_in_a() else {
        return;
    };
    let sink: ITfThreadMgrEventSink = service.to_interface();
    unsafe { sink.OnSetFocus(&doc_b, &doc_a) }.unwrap();
    assert!(edits_a.get() > 0 && edits_b.get() == 0, "{} {}", edits_a.get(), edits_b.get());
    service.state.borrow_mut().callback = None;
}

/// 宿主把組字搬到另一個 context（Excel 把組字交給儲存格編輯器）。
fn move_composition(service: &ComObject<TextService>, ctx: ITfContext) {
    let range: ITfRange = ComObject::new(Range { ctx }).into_interface();
    let composition: ITfComposition = ComObject::new(Comp { range }).into_interface();
    service.state.borrow_mut().composition = Some(composition);
}

#[test]
fn focus_to_null_commits_to_where_the_composition_now_is() {
    // 發版前 code review 第二輪：組字中焦點 A → NULL，確認前宿主把組字搬到 C，焦點卻接著落到 B（例如馬上點了別的地方）。
    // 要送到組字實際所在的 C；送到焦點移到 NULL 前記下的 A，A 的 edit session 改不到 C 裡的組字範圍，打到一半的字會不見。
    let Some((service, doc_a, edits_a, doc_b, edits_b)) = composing_in_a() else {
        return;
    };
    let (_doc_c, ctx_c, edits_c) = document();
    let sink: ITfThreadMgrEventSink = service.to_interface();
    unsafe { sink.OnSetFocus(None::<&ITfDocumentMgr>, &doc_a) }.unwrap();
    move_composition(&service, ctx_c);
    unsafe { sink.OnSetFocus(&doc_b, None::<&ITfDocumentMgr>) }.unwrap();
    assert!(edits_c.get() > 0, "組字要送到 C");
    assert_eq!((edits_a.get(), edits_b.get()), (0, 0), "不送到 A、B");
    service.state.borrow_mut().callback = None;
}

#[test]
fn switching_documents_commits_to_the_composition_context() {
    // 直接換文件時也以組字範圍本身的 context 為準：原文件 A 的 context 跟組字所在的 C 不同時送到 C。
    let Some((service, doc_a, edits_a, doc_b, edits_b)) = composing_in_a() else {
        return;
    };
    let (_doc_c, ctx_c, edits_c) = document();
    move_composition(&service, ctx_c);
    let sink: ITfThreadMgrEventSink = service.to_interface();
    unsafe { sink.OnSetFocus(&doc_b, &doc_a) }.unwrap();
    assert!(edits_c.get() > 0, "組字要送到 C");
    assert_eq!((edits_a.get(), edits_b.get()), (0, 0), "不送到 A、B");
    service.state.borrow_mut().callback = None;
}

/// 拿不到範圍的組字（宿主那一端的組字範圍或 context 已失效，composing 仍為 true）。
#[implement(ITfComposition)]
struct DeadComp;
impl ITfComposition_Impl for DeadComp_Impl {
    fn GetRange(&self) -> R<ITfRange> {
        no()
    }
    fn ShiftStart(&self, _: u32, _: Ref<'_, ITfRange>) -> R<()> {
        no()
    }
    fn ShiftEnd(&self, _: u32, _: Ref<'_, ITfRange>) -> R<()> {
        no()
    }
    fn EndComposition(&self, _: u32) -> R<()> {
        no()
    }
}

fn break_composition(service: &ComObject<TextService>) {
    let composition: ITfComposition = ComObject::new(DeadComp).into_interface();
    service.state.borrow_mut().composition = Some(composition);
}

#[test]
fn focus_to_null_then_another_document_with_a_dead_range_commits_to_the_original() {
    // 發版前 code review 第三輪：A → NULL 之後組字範圍失效（composition_context() 拿不到），焦點接著落到 B。
    // 第二次焦點變化的原文件是 NULL，組字範圍也沒有 context：只剩焦點移到 NULL 時記下的 A，沒有它打到一半的字就沒地方送。
    let Some((service, doc_a, edits_a, doc_b, edits_b)) = composing_in_a() else {
        return;
    };
    let sink: ITfThreadMgrEventSink = service.to_interface();
    unsafe { sink.OnSetFocus(None::<&ITfDocumentMgr>, &doc_a) }.unwrap();
    assert_eq!(edits_a.get(), 0);
    break_composition(&service);
    unsafe { sink.OnSetFocus(&doc_b, None::<&ITfDocumentMgr>) }.unwrap();
    assert!(edits_a.get() > 0, "組字要送到 A");
    assert_eq!(edits_b.get(), 0, "不送到 B");
    assert!(service.state.borrow().left_from.is_none(), "送出後不再記著 A");
    service.state.borrow_mut().callback = None;
}

#[test]
fn settling_with_a_dead_range_commits_to_the_original() {
    // 同上，焦點停在 NULL、等到佇列空下來才確認（settle_focus）：一樣送到焦點移到 NULL 時記下的 A。
    let Some((service, doc_a, edits_a, _doc_b, edits_b)) = composing_in_a() else {
        return;
    };
    let sink: ITfThreadMgrEventSink = service.to_interface();
    unsafe { sink.OnSetFocus(None::<&ITfDocumentMgr>, &doc_a) }.unwrap();
    // 宿主連續移開兩次：第二次沒有原文件，保留第一次記下的 A。
    unsafe { sink.OnSetFocus(None::<&ITfDocumentMgr>, None::<&ITfDocumentMgr>) }.unwrap();
    break_composition(&service);
    service.settle_focus();
    assert!(edits_a.get() > 0, "組字要送到 A");
    assert_eq!(edits_b.get(), 0);
    service.state.borrow_mut().callback = None;
}

#[test]
fn focus_handed_back_forgets_the_original() {
    // A → NULL → A（繼續組字）之後不再記著 A：之後的送出照組字範圍與那次的原文件決定，不會送到舊的記錄。
    let Some((service, doc_a, _edits_a, _doc_b, _)) = composing_in_a() else {
        return;
    };
    let sink: ITfThreadMgrEventSink = service.to_interface();
    unsafe { sink.OnSetFocus(None::<&ITfDocumentMgr>, &doc_a) }.unwrap();
    assert!(service.state.borrow().left_from.is_some());
    unsafe { sink.OnSetFocus(&doc_a, None::<&ITfDocumentMgr>) }.unwrap();
    assert!(service.state.borrow().left_from.is_none());
    assert!(service.state.borrow().composing);
    service.state.borrow_mut().callback = None;
}
