//! 已確認前文：只核對游標前是否仍緊接著自己剛提交的文字。讀到的文字只拿來比對，
//! 不送給引擎、不記錄；有選取範圍、讀不到或不相符都視為未確認（對應 Fcitx 外殼的 DocumentContext）。
use std::cell::Cell;

use windows::Win32::UI::TextServices::{
    ITfContext, ITfEditSession, ITfEditSession_Impl, TF_DEFAULT_SELECTION, TF_ES_READ, TF_ES_SYNC,
    TF_SELECTION,
};
use windows_core::{implement, ComObject};

use crate::log::zdebug;

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
