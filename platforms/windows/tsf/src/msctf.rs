//! msctf.dll 直接匯出的建立函式。以 COM-less 方式啟用輸入法的宿主（Minecraft 等 GLFW／LWJGL 程式，#157）
//! 可能沒有初始化 COM，ActivateEx 帶 TF_TMAE_COMLESS 時不能呼叫 CoCreateInstance；改用 msctf 的
//! TF_Create* 建立 TSF 物件，不必區分是不是 COM-less 啟用（新酷音 windows-chewing-tsf PR #651 同樣做法）。
//! windows crate 0.61 沒有這些函式的繫結，以 windows-link（raw-dylib，x86 為未修飾名稱）匯入。
use windows::Win32::UI::TextServices::ITfCategoryMgr;
use windows_core::{Interface, HRESULT};

mod ffi {
    windows_link::link!("msctf.dll" "system" fn TF_CreateCategoryMgr(ppcat: *mut *mut core::ffi::c_void) -> windows_core::HRESULT);
}

/// 不經 COM 建立類別管理員。回傳的物件由 msctf 持有，用完照常 Release（Drop）即可。
pub fn create_category_mgr() -> windows_core::Result<ITfCategoryMgr> {
    let mut raw: *mut core::ffi::c_void = std::ptr::null_mut();
    let hr: HRESULT = unsafe { ffi::TF_CreateCategoryMgr(&mut raw) };
    hr.ok()?;
    if raw.is_null() {
        return Err(windows::Win32::Foundation::E_POINTER.into());
    }
    Ok(unsafe { ITfCategoryMgr::from_raw(raw) })
}

#[cfg(test)]
mod tests {
    #[test]
    fn category_mgr_without_com() {
        // 測試執行緒沒有呼叫 CoInitialize：跟 COM-less 啟用的宿主一樣，仍要能註冊顯示屬性的 GUID。
        let mgr = super::create_category_mgr().expect("TF_CreateCategoryMgr");
        let atom = unsafe { mgr.RegisterGUID(&crate::guids::GUID_DISPLAY_ATTR_INPUT) }.expect("RegisterGUID");
        assert_ne!(atom, 0);
    }
}
