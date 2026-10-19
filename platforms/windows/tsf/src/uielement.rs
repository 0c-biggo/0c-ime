//! UI-less 模式（#86）：遊戲等自己畫候選的宿主（例如英雄聯盟遊戲內聊天）以
//! `TF_TMAE_UIELEMENTENABLEDONLY` 啟用輸入法，只接受註冊了 UIELEMENTENABLED 的輸入法，
//! 並從 ITfCandidateListUIElement 讀候選清單自己畫。
//!
//! 每次顯示候選都先 BeginUIElement：宿主回 pbShow=FALSE 表示它自己畫，外殼不顯示候選窗；
//! 一般應用程式沒有 UI 元件接收者，TSF 回 TRUE，照常畫自己的候選窗。清單只放目前這一頁
//! （核心只送目前頁），翻頁照常用按鍵。
//!
//! 沒有候選、只有補全提示（Tab）、提示訊息或加詞標示說明時，沒有對應的 UI 元件可交給宿主，
//! 外殼也不畫自己的視窗：遊戲在獨佔全螢幕時，任何疊上去的視窗都會讓它失去全螢幕並跳回桌面（#159）。
use std::cell::{Cell, RefCell};

use windows::Win32::Foundation::{E_INVALIDARG, E_NOTIMPL, E_POINTER};
use windows::Win32::UI::TextServices::{
    ITfCandidateListUIElement, ITfCandidateListUIElement_Impl, ITfDocumentMgr, ITfUIElementMgr,
    ITfUIElement_Impl, TF_CLUIE_COUNT, TF_CLUIE_CURRENTPAGE, TF_CLUIE_DOCUMENTMGR, TF_CLUIE_PAGEINDEX,
    TF_CLUIE_SELECTION, TF_CLUIE_STRING,
};
use windows_core::{implement, ComObject, BOOL, BSTR, GUID};
use zuin_win_common::UiMessage;

use crate::log::zlog;

/// 本元件的識別碼（ITfUIElement::GetGUID）；宿主只用來分辨元件，不需要註冊。
const ELEMENT_GUID: GUID = GUID::from_u128(0xda0ca60a_1a05_4b70_980d_a964070868e1);
const ALL_FLAGS: u32 =
    TF_CLUIE_DOCUMENTMGR | TF_CLUIE_COUNT | TF_CLUIE_SELECTION | TF_CLUIE_STRING | TF_CLUIE_PAGEINDEX | TF_CLUIE_CURRENTPAGE;

#[implement(ITfCandidateListUIElement)]
struct CandidateList {
    items: RefCell<Vec<String>>,
    selection: Cell<u32>,
    document: RefCell<Option<ITfDocumentMgr>>,
    flags: Cell<u32>,
    /// 外殼是否該畫自己的候選窗：BeginUIElement 的 pbShow，之後宿主可用 Show 改變。
    shown: Cell<bool>,
}

impl Drop for CandidateList {
    fn drop(&mut self) {
        crate::object_destroyed();
    }
}

impl ITfUIElement_Impl for CandidateList_Impl {
    fn GetDescription(&self) -> windows_core::Result<BSTR> {
        crate::guarded("UIElement.GetDescription", || Ok(BSTR::from("候選字")))
    }
    fn GetGUID(&self) -> windows_core::Result<GUID> {
        crate::guarded("UIElement.GetGUID", || Ok(ELEMENT_GUID))
    }
    fn Show(&self, show: BOOL) -> windows_core::Result<()> {
        // 下一次更新候選時依此決定是否畫自己的候選窗。
        crate::guarded("UIElement.Show", || {
            self.shown.set(show.as_bool());
            Ok(())
        })
    }
    fn IsShown(&self) -> windows_core::Result<BOOL> {
        crate::guarded("UIElement.IsShown", || Ok(self.shown.get().into()))
    }
}

impl ITfCandidateListUIElement_Impl for CandidateList_Impl {
    fn GetUpdatedFlags(&self) -> windows_core::Result<u32> {
        crate::guarded("UIElement.GetUpdatedFlags", || Ok(self.flags.replace(0)))
    }
    fn GetDocumentMgr(&self) -> windows_core::Result<ITfDocumentMgr> {
        crate::guarded("UIElement.GetDocumentMgr", || {
            self.document.borrow().clone().ok_or_else(|| E_NOTIMPL.into())
        })
    }
    fn GetCount(&self) -> windows_core::Result<u32> {
        crate::guarded("UIElement.GetCount", || Ok(self.items.borrow().len() as u32))
    }
    fn GetSelection(&self) -> windows_core::Result<u32> {
        crate::guarded("UIElement.GetSelection", || Ok(self.selection.get()))
    }
    fn GetString(&self, index: u32) -> windows_core::Result<BSTR> {
        crate::guarded("UIElement.GetString", || {
            self.items
                .borrow()
                .get(index as usize)
                .map(|text| BSTR::from(text.as_str()))
                .ok_or_else(|| E_INVALIDARG.into())
        })
    }
    fn GetPageIndex(&self, index: *mut u32, size: u32, count: *mut u32) -> windows_core::Result<()> {
        // 只有一頁，從 0 開始。index 為 null 時只回頁數（宿主先問大小）。
        crate::guarded("UIElement.GetPageIndex", || {
            if count.is_null() {
                return Err(E_POINTER.into());
            }
            unsafe {
                count.write(1);
                if !index.is_null() && size >= 1 {
                    index.write(0);
                }
            }
            Ok(())
        })
    }
    fn SetPageIndex(&self, _index: *const u32, _count: u32) -> windows_core::Result<()> {
        crate::guarded("UIElement.SetPageIndex", || Err(E_NOTIMPL.into()))
    }
    fn GetCurrentPage(&self) -> windows_core::Result<u32> {
        crate::guarded("UIElement.GetCurrentPage", || Ok(0))
    }
}

/// 一個 TSF 執行緒的候選清單元件；Activate 時建立，Deactivate 前結束。
pub struct Elements {
    mgr: ITfUIElementMgr,
    element: ComObject<CandidateList>,
    id: Option<u32>,
}

impl Elements {
    pub fn new(mgr: ITfUIElementMgr) -> Elements {
        crate::object_created(); // CandidateList 的 Drop 扣回
        Elements {
            mgr,
            element: ComObject::new(CandidateList {
                items: RefCell::new(Vec::new()),
                selection: Cell::new(0),
                document: RefCell::new(None),
                flags: Cell::new(0),
                shown: Cell::new(true),
            }),
            id: None,
        }
    }

    /// 更新候選清單並回傳外殼是否要畫自己的視窗。只有宿主明確讓外殼畫（pbShow=TRUE）的候選清單才畫；
    /// 沒有候選（只有補全提示、提示訊息或標示說明）時結束元件、不畫（#159）。
    pub fn present(&mut self, message: &UiMessage, document: Option<ITfDocumentMgr>) -> bool {
        if !message.show {
            self.end();
            return true; // 收起：讓呼叫端照常送出隱藏訊息
        }
        if message.candidates.is_empty() {
            self.end();
            return false;
        }
        let element = &self.element;
        *element.items.borrow_mut() = message.candidates.iter().map(|c| c.text.clone()).collect();
        element
            .selection
            .set(message.highlight.clamp(0, message.candidates.len() as i64 - 1) as u32);
        *element.document.borrow_mut() = document;
        element.flags.set(ALL_FLAGS);
        match self.id {
            Some(id) => unsafe {
                let _ = self.mgr.UpdateUIElement(id);
            },
            None => {
                // ITfUIElement 是基底介面，衍生介面可直接當參數傳。
                let interface: ITfCandidateListUIElement = element.to_interface();
                let mut show = BOOL(1);
                let mut id = 0;
                match unsafe { self.mgr.BeginUIElement(&interface, &mut show, &mut id) } {
                    Ok(()) => {
                        if !show.as_bool() {
                            zlog!("ui: 宿主自行繪製候選（UI-less）");
                        }
                        element.shown.set(show.as_bool());
                        self.id = Some(id);
                    }
                    // 沒有元件管理員可用：照常畫自己的候選窗。
                    Err(_) => return true,
                }
            }
        }
        element.shown.get()
    }

    /// 候選窗關閉：通知宿主結束這個元件。
    pub fn end(&mut self) {
        if let Some(id) = self.id.take() {
            unsafe {
                let _ = self.mgr.EndUIElement(id);
            }
        }
        *self.element.document.borrow_mut() = None;
    }
}

impl Drop for Elements {
    fn drop(&mut self) {
        self.end();
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use windows::Win32::UI::TextServices::{IEnumTfUIElements, ITfUIElement, ITfUIElementMgr_Impl};
    use windows_core::Ref;
    use zuin_win_common::UiCandidate;

    /// 模擬宿主的 UI 元件管理員：BeginUIElement 回傳指定的 pbShow，並記下呼叫次數。
    #[implement(ITfUIElementMgr)]
    struct Host {
        show: bool,
        begun: Cell<u32>,
        ended: Cell<u32>,
    }

    impl ITfUIElementMgr_Impl for Host_Impl {
        fn BeginUIElement(&self, _element: Ref<'_, ITfUIElement>, show: *mut BOOL, id: *mut u32) -> windows_core::Result<()> {
            self.begun.set(self.begun.get() + 1);
            unsafe {
                show.write(self.show.into());
                id.write(7);
            }
            Ok(())
        }
        fn UpdateUIElement(&self, _id: u32) -> windows_core::Result<()> {
            Ok(())
        }
        fn EndUIElement(&self, _id: u32) -> windows_core::Result<()> {
            self.ended.set(self.ended.get() + 1);
            Ok(())
        }
        fn GetUIElement(&self, _id: u32) -> windows_core::Result<ITfUIElement> {
            Err(E_NOTIMPL.into())
        }
        fn EnumUIElements(&self) -> windows_core::Result<IEnumTfUIElements> {
            Err(E_NOTIMPL.into())
        }
    }

    fn host(show: bool) -> (ComObject<Host>, Elements) {
        let host = ComObject::new(Host { show, begun: Cell::new(0), ended: Cell::new(0) });
        let elements = Elements::new(host.to_interface());
        (host, elements)
    }

    fn candidates() -> UiMessage {
        UiMessage {
            show: true,
            candidates: vec![UiCandidate { id: 1, text: "價".into(), ..Default::default() }],
            ..Default::default()
        }
    }

    #[test]
    fn uiless_host_never_gets_our_window_for_hints_without_candidates() {
        // #159：英雄聯盟（UI-less）打「價」只有補全提示「格」，0c-ui 的視窗疊在獨佔全螢幕的遊戲上，
        // 遊戲失去前景並跳回桌面。補全提示、提示訊息、標示說明都沒有 UI 元件可交給宿主，一律不畫。
        let (host, mut elements) = host(false);
        for message in [
            UiMessage { show: true, suggestion: "格".into(), ..Default::default() },
            UiMessage { show: true, notice: "已加入詞庫".into(), ..Default::default() },
            UiMessage { show: true, marking: true, notice: "按 Enter 加入詞庫".into(), ..Default::default() },
        ] {
            assert!(!elements.present(&message, None), "{message:?}");
        }
        assert_eq!(host.begun.get(), 0, "hints are not handed to the host as candidate lists");
        // 候選清單交給宿主畫，外殼不畫；之後只剩補全提示時結束元件，仍不畫。
        assert!(!elements.present(&candidates(), None));
        assert_eq!(host.begun.get(), 1);
        assert!(!elements.present(&UiMessage { show: true, suggestion: "格".into(), ..Default::default() }, None));
        assert_eq!(host.ended.get(), 1);
        // 收起照常放行（呼叫端送隱藏訊息）。
        assert!(elements.present(&UiMessage::default(), None));
    }

    #[test]
    fn uiless_host_that_lets_us_draw_still_sees_our_candidate_window() {
        let (host, mut elements) = host(true);
        assert!(elements.present(&candidates(), None));
        assert_eq!(host.begun.get(), 1);
    }

    fn list() -> ComObject<CandidateList> {
        crate::object_created();
        ComObject::new(CandidateList {
            items: RefCell::new(vec!["我".into(), "窩".into()]),
            selection: Cell::new(1),
            document: RefCell::new(None),
            flags: Cell::new(ALL_FLAGS),
            shown: Cell::new(true),
        })
    }

    #[test]
    fn candidate_list_reports_current_page_to_host() {
        let element: ITfCandidateListUIElement = list().into_interface();
        unsafe {
            assert_eq!(element.GetCount().unwrap(), 2);
            assert_eq!(element.GetSelection().unwrap(), 1);
            assert_eq!(element.GetString(0).unwrap().to_string(), "我");
            assert!(element.GetString(2).is_err());
            assert_eq!(element.GetUpdatedFlags().unwrap(), ALL_FLAGS);
            assert_eq!(element.GetUpdatedFlags().unwrap(), 0, "flags reset after read");
            let mut pages = 0;
            element.GetPageIndex(&mut [], &mut pages).unwrap();
            assert_eq!(pages, 1);
            let mut index = [7u32; 1];
            element.GetPageIndex(&mut index, &mut pages).unwrap();
            assert_eq!(index, [0]);
            assert_eq!(element.GetCurrentPage().unwrap(), 0);
            let base: ITfUIElement = windows_core::Interface::cast(&element).unwrap();
            assert!(base.IsShown().unwrap().as_bool());
            base.Show(false.into()).unwrap();
            assert!(!base.IsShown().unwrap().as_bool());
            assert_eq!(base.GetGUID().unwrap(), ELEMENT_GUID);
        }
    }
}
