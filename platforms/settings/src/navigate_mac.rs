//! macOS 的切頁通知（#125）。設定程式宣告 `LSMultipleInstancesProhibited`，已經開著時 `open --args --page X`
//! 只會由 LaunchServices 把既有視窗帶到前面，參數不會交給它（單一實例外掛也因此不會被觸發）。
//! 0c-update open-settings 在 open 之後送出 Darwin 通知（notify(3)，`notifyutil -p <名稱>`），這裡收到就切頁。
//!
//! 每個頁面登記一個名稱（`updater::navigate_notification`），共用一個檔案描述子：通知到時系統在描述子寫入
//! 該名稱的登記代號，背景執行緒讀到後交給 `on_page`。名稱在 `user.uid.<uid>.` 底下，只有同一個使用者能送。
use std::ffi::CString;
use std::fs::File;
use std::io::Read;
use std::os::fd::FromRawFd;
use std::os::raw::{c_char, c_int};

use crate::updater::{navigate_notification, page_for_token, PAGES};

extern "C" {
    // libSystem（<notify.h>）。
    fn notify_register_file_descriptor(name: *const c_char, notify_fd: *mut c_int, flags: c_int, out_token: *mut c_int) -> u32;
    fn getuid() -> u32;
}

const NOTIFY_STATUS_OK: u32 = 0;
const NOTIFY_REUSE: c_int = 1;

/// 開始監聽；登記失敗時什麼都不做（視窗照樣會被帶到前面，只是不切頁）。
pub fn listen(on_page: impl Fn(&'static str) + Send + 'static) {
    let uid = unsafe { getuid() };
    let mut fd: c_int = -1;
    let mut tokens = Vec::new();
    for page in PAGES {
        let Ok(name) = CString::new(navigate_notification(uid, page)) else { continue };
        let mut token: c_int = 0;
        let flags = if fd >= 0 { NOTIFY_REUSE } else { 0 };
        if unsafe { notify_register_file_descriptor(name.as_ptr(), &mut fd, flags, &mut token) } == NOTIFY_STATUS_OK {
            tokens.push((token, page));
        }
    }
    if fd < 0 || tokens.is_empty() {
        return;
    }
    std::thread::spawn(move || {
        // 描述子由 notify 建立、整個程式期間都在用；結束時隨行程關閉。
        let mut file = unsafe { File::from_raw_fd(fd) };
        let mut bytes = [0u8; 4];
        while file.read_exact(&mut bytes).is_ok() {
            if let Some(page) = page_for_token(&tokens, bytes) {
                on_page(page);
            }
        }
    });
}
