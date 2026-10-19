//! 註冊：HKCR\CLSID 的 InprocServer32、TSF 的語言 profile 與類別。regsvr32 需要管理員權限。
//!
//! 32 位元應用程式（#112）讀 32 位元登錄視圖的 CLSID（HKCR\WOW6432Node\CLSID），載入 `x86\0c_tsf.dll`；
//! TSF 的 profile 與類別在 HKLM\SOFTWARE\Microsoft\CTF\TIP，是兩個視圖共用的機碼，只有一份。
//! 所以任一份 DLL 註冊時都寫兩個視圖的 InprocServer32（檔案存在的才寫，不存在的刪掉舊值），取消註冊時兩個都刪：
//! 從哪一份執行 regsvr32 結果都一樣，舊版更新器只對 x64 DLL 執行 regsvr32 也會補上 32 位元的註冊。
use windows::Win32::Foundation::ERROR_SUCCESS;
use windows::Win32::System::Com::{CoCreateInstance, CoInitializeEx, CoUninitialize, CLSCTX_INPROC_SERVER, COINIT_APARTMENTTHREADED};
use windows::Win32::System::LibraryLoader::GetModuleFileNameW;
use windows::Win32::System::Registry::{
    RegCloseKey, RegCreateKeyExW, RegDeleteKeyExW, RegDeleteTreeW, RegOpenKeyExW, RegSetValueExW, HKEY, HKEY_CLASSES_ROOT,
    KEY_ENUMERATE_SUB_KEYS, KEY_QUERY_VALUE, KEY_SET_VALUE, KEY_WOW64_32KEY, KEY_WOW64_64KEY, KEY_WRITE, REG_OPTION_NON_VOLATILE,
    REG_SAM_FLAGS, REG_SZ,
};
use windows::Win32::Foundation::{LocalFree, ERROR_FILE_NOT_FOUND, HLOCAL};
use windows::Win32::Security::Authorization::{
    ConvertStringSidToSidW, GetNamedSecurityInfoW, SetEntriesInAclW, SetNamedSecurityInfoW, EXPLICIT_ACCESS_W, GRANT_ACCESS,
    SE_FILE_OBJECT, TRUSTEE_IS_SID, TRUSTEE_IS_WELL_KNOWN_GROUP, TRUSTEE_W,
};
use windows::Win32::Security::{ACL, DACL_SECURITY_INFORMATION, NO_INHERITANCE, PSECURITY_DESCRIPTOR, PSID};
use windows::Win32::Storage::FileSystem::{DELETE, FILE_GENERIC_EXECUTE, FILE_GENERIC_READ};
use windows_core::PWSTR;
use windows::Win32::UI::TextServices::{
    ITfCategoryMgr, ITfInputProcessorProfiles, CLSID_TF_CategoryMgr, CLSID_TF_InputProcessorProfiles,
    GUID_TFCAT_DISPLAYATTRIBUTEPROVIDER, GUID_TFCAT_TIPCAP_COMLESS, GUID_TFCAT_TIPCAP_IMMERSIVESUPPORT,
    GUID_TFCAT_TIPCAP_SYSTRAYSUPPORT, GUID_TFCAT_TIPCAP_UIELEMENTENABLED, GUID_TFCAT_TIP_KEYBOARD,
};
use windows_core::{Error, PCWSTR, GUID};
use zuin_win_common::wide;

use crate::guids::{CLSID_ZUIN, DESCRIPTION, GUID_PROFILE, LANGID};
use crate::log::zlog;

// UIELEMENTENABLED：候選清單以 ITfCandidateListUIElement 提供（uielement.rs）。自行繪製候選的宿主
// （例如英雄聯盟遊戲內聊天）以 TF_TMAE_UIELEMENTENABLEDONLY 啟用，只載入有這個類別的輸入法（#86）。
// COMLESS：以 COM-less 方式啟用輸入法的宿主（Minecraft Java 版等 GLFW／LWJGL 程式）只載入有這個類別的輸入法，
// 沒有時工作列顯示「中」但按鍵直接以英文送出（#157）。啟用時因此不呼叫 CoCreateInstance（msctf.rs）。
// 每次更新都會以新版 DLL 重新註冊，升級即生效；回復舊版時新版 DLL 先取消註冊，舊版不會留著這些類別。
const CATEGORIES: [GUID; 6] = [
    GUID_TFCAT_TIP_KEYBOARD,
    GUID_TFCAT_TIPCAP_IMMERSIVESUPPORT,
    GUID_TFCAT_TIPCAP_SYSTRAYSUPPORT,
    GUID_TFCAT_TIPCAP_UIELEMENTENABLED,
    GUID_TFCAT_TIPCAP_COMLESS,
    GUID_TFCAT_DISPLAYATTRIBUTEPROVIDER,
];

fn clsid_key() -> String {
    format!("CLSID\\{{{:?}}}", CLSID_ZUIN)
}

fn dll_path() -> windows_core::Result<String> {
    let mut buf = vec![0u16; 32768]; // 64 KB 放 heap：宿主執行緒的堆疊可能很小（#89）
    let n = unsafe { GetModuleFileNameW(Some(crate::dll_instance()), &mut buf) } as usize;
    if n == 0 || n >= buf.len() {
        return Err(Error::from_win32());
    }
    Ok(String::from_utf16_lossy(&buf[..n]))
}

fn set_string(key: HKEY, name: Option<&str>, value: &str) -> windows_core::Result<()> {
    let w = wide(value);
    let bytes: Vec<u8> = w.iter().flat_map(|u| u.to_le_bytes()).collect();
    let name_w = name.map(wide);
    let name_p = name_w.as_ref().map(|v| PCWSTR::from_raw(v.as_ptr())).unwrap_or(PCWSTR::null());
    let r = unsafe { RegSetValueExW(key, name_p, None, REG_SZ, Some(&bytes)) };
    if r == ERROR_SUCCESS {
        Ok(())
    } else {
        Err(r.into())
    }
}

fn create_key(parent: HKEY, path: &str, view: REG_SAM_FLAGS) -> windows_core::Result<HKEY> {
    let w = wide(path);
    let mut key = HKEY::default();
    let r = unsafe {
        RegCreateKeyExW(parent, PCWSTR::from_raw(w.as_ptr()), None, PCWSTR::null(), REG_OPTION_NON_VOLATILE, KEY_WRITE | view, None, &mut key, None)
    };
    if r == ERROR_SUCCESS {
        Ok(key)
    } else {
        Err(r.into())
    }
}

/// 兩個登錄視圖：64 位元應用程式讀 KEY_WOW64_64KEY，32 位元應用程式讀 KEY_WOW64_32KEY（HKCR\WOW6432Node）。
/// 明確指定視圖，不依賴本 DLL 自己的位元數。
const VIEWS: [REG_SAM_FLAGS; 2] = [KEY_WOW64_64KEY, KEY_WOW64_32KEY];

/// 兩份 DLL 的實際路徑（x64, x86）；檔案不存在的為 None。
fn dll_paths() -> windows_core::Result<[Option<String>; 2]> {
    let own = std::path::PathBuf::from(dll_path()?);
    let (x64, x86) = crate::layout::dll_pair(&own, crate::layout::IS_32BIT);
    let existing = |p: Option<std::path::PathBuf>| p.filter(|p| p.is_file()).map(|p| p.to_string_lossy().into_owned());
    Ok([existing(x64), existing(x86)])
}

fn write_inproc(view: REG_SAM_FLAGS, path: &str) -> windows_core::Result<()> {
    let key = create_key(HKEY_CLASSES_ROOT, &clsid_key(), view)?;
    let result = (|| {
        set_string(key, None, DESCRIPTION)?;
        let inproc = create_key(key, "InprocServer32", view)?;
        let r = set_string(inproc, None, path).and_then(|_| set_string(inproc, Some("ThreadingModel"), "Apartment"));
        unsafe {
            let _ = RegCloseKey(inproc);
        }
        r
    })();
    unsafe {
        let _ = RegCloseKey(key);
    }
    result
}

/// 刪掉一個視圖的 CLSID 機碼（含子機碼）；本來就沒有不算錯。
fn delete_clsid(view: REG_SAM_FLAGS) -> windows_core::Result<()> {
    let path = wide(&clsid_key());
    let mut key = HKEY::default();
    let access = DELETE.0 | KEY_ENUMERATE_SUB_KEYS.0 | KEY_QUERY_VALUE.0 | KEY_SET_VALUE.0 | view.0;
    let r = unsafe { RegOpenKeyExW(HKEY_CLASSES_ROOT, PCWSTR::from_raw(path.as_ptr()), None, REG_SAM_FLAGS(access), &mut key) };
    if r == ERROR_FILE_NOT_FOUND {
        return Ok(());
    }
    if r != ERROR_SUCCESS {
        return Err(r.into());
    }
    let r = unsafe { RegDeleteTreeW(key, PCWSTR::null()) };
    unsafe {
        let _ = RegCloseKey(key);
    }
    if r != ERROR_SUCCESS && r != ERROR_FILE_NOT_FOUND {
        return Err(r.into());
    }
    let r = unsafe { RegDeleteKeyExW(HKEY_CLASSES_ROOT, PCWSTR::from_raw(path.as_ptr()), view.0, None) };
    if r == ERROR_SUCCESS || r == ERROR_FILE_NOT_FOUND {
        Ok(())
    } else {
        Err(r.into())
    }
}

/// 讓沙箱應用程式（AppContainer）讀得到並載入 DLL：加上 ALL APPLICATION PACKAGES 與 ALL RESTRICTED APPLICATION
/// PACKAGES 的讀取／執行（與 cmd/0c-update 的 grantAppContainerRead 相同）。0c-update 註冊時會對 x64 DLL 做，
/// 新版更新器也會對 x86 那份做；舊版更新器（0.1.0-dev.61 以前）不認得 x86 DLL，所以這裡在 regsvr32（已提升權限）
/// 執行時也補一次。失敗只記錄：只影響沙箱應用程式，不讓註冊失敗。
fn grant_appcontainer_read(path: &str) -> windows_core::Result<()> {
    let name = wide(path);
    let mut sd = PSECURITY_DESCRIPTOR::default();
    let mut dacl: *mut ACL = std::ptr::null_mut();
    let r = unsafe {
        GetNamedSecurityInfoW(PCWSTR::from_raw(name.as_ptr()), SE_FILE_OBJECT, DACL_SECURITY_INFORMATION, None, None, Some(&mut dacl), None, &mut sd)
    };
    if r != ERROR_SUCCESS {
        return Err(r.into());
    }
    let mut sids: Vec<PSID> = Vec::new();
    let result = (|| {
        if dacl.is_null() {
            return Ok(()); // NULL DACL 已允許所有人；合併會只剩這兩筆，反而擋住本人
        }
        let mut entries = Vec::new();
        for s in ["S-1-15-2-1", "S-1-15-2-2"] {
            let text = wide(s);
            let mut sid = PSID::default();
            unsafe { ConvertStringSidToSidW(PCWSTR::from_raw(text.as_ptr()), &mut sid)? };
            sids.push(sid);
            entries.push(EXPLICIT_ACCESS_W {
                grfAccessPermissions: FILE_GENERIC_READ.0 | FILE_GENERIC_EXECUTE.0,
                grfAccessMode: GRANT_ACCESS,
                grfInheritance: NO_INHERITANCE,
                Trustee: TRUSTEE_W {
                    TrusteeForm: TRUSTEE_IS_SID,
                    TrusteeType: TRUSTEE_IS_WELL_KNOWN_GROUP,
                    ptstrName: PWSTR(sid.0 as *mut u16),
                    ..Default::default()
                },
            });
        }
        let mut merged: *mut ACL = std::ptr::null_mut();
        let r = unsafe { SetEntriesInAclW(Some(&entries), Some(dacl), &mut merged) };
        if r != ERROR_SUCCESS {
            return Err(r.into());
        }
        let r = unsafe { SetNamedSecurityInfoW(PCWSTR::from_raw(name.as_ptr()), SE_FILE_OBJECT, DACL_SECURITY_INFORMATION, None, None, Some(merged), None) };
        unsafe {
            let _ = LocalFree(Some(HLOCAL(merged as *mut _)));
        }
        if r == ERROR_SUCCESS {
            Ok(())
        } else {
            Err(r.into())
        }
    })();
    unsafe {
        for sid in sids {
            let _ = LocalFree(Some(HLOCAL(sid.0)));
        }
        let _ = LocalFree(Some(HLOCAL(sd.0)));
    }
    result
}

fn register_com() -> windows_core::Result<()> {
    let paths = dll_paths()?;
    if let Some(x86) = &paths[1] {
        if let Err(e) = grant_appcontainer_read(x86) {
            zlog!("register: 無法讓沙箱應用程式讀取 32 位元 DLL：{}", e);
        }
    }
    for (view, path) in VIEWS.iter().zip(paths.iter()) {
        match path {
            Some(p) => write_inproc(*view, p)?,
            // 這一版沒有這份 DLL（或被移走）：清掉舊版留下、指向不存在檔案的註冊。
            None => delete_clsid(*view)?,
        }
    }
    Ok(())
}

fn unregister_com() -> windows_core::Result<()> {
    let mut result = Ok(());
    for view in VIEWS {
        result = result.and(delete_clsid(view));
    }
    result
}

struct ComInit(bool);
impl ComInit {
    fn new() -> ComInit {
        ComInit(unsafe { CoInitializeEx(None, COINIT_APARTMENTTHREADED) }.is_ok())
    }
}
impl Drop for ComInit {
    fn drop(&mut self) {
        if self.0 {
            unsafe { CoUninitialize() };
        }
    }
}

fn register_profiles() -> windows_core::Result<()> {
    unsafe {
        zlog!("register: profiles");
        let profiles: ITfInputProcessorProfiles = CoCreateInstance(&CLSID_TF_InputProcessorProfiles, None, CLSCTX_INPROC_SERVER)?;
        profiles.Register(&CLSID_ZUIN)?;
        // msctf 把這兩個字串當 NUL 結尾讀（不只看長度）：不含 NUL 會把後面的記憶體一起寫進登錄。
        let desc = wide(DESCRIPTION);
        // 圖示檔不能傳空 slice：msctf 會讀那個懸空指標而當掉。品牌圖示是 DLL 本身的第一個圖示（build.rs 連入）。
        // 兩份 DLL 都有同一個圖示；優先用 x64 那份，32 位元與 64 位元的註冊才不會互相改寫圖示路徑。
        let [x64, _] = dll_paths()?;
        let icon = wide(&match x64 {
            Some(p) => p,
            None => dll_path()?,
        });
        zlog!("register: language profile");
        profiles.AddLanguageProfile(&CLSID_ZUIN, LANGID, &GUID_PROFILE, &desc, &icon, 0)?;
        zlog!("register: categories");
        let categories: ITfCategoryMgr = CoCreateInstance(&CLSID_TF_CategoryMgr, None, CLSCTX_INPROC_SERVER)?;
        for cat in &CATEGORIES {
            zlog!("register: category {:?}", cat);
            categories.RegisterCategory(&CLSID_ZUIN, cat, &CLSID_ZUIN)?;
        }
        zlog!("register: done");
    }
    Ok(())
}

fn unregister_profiles() -> windows_core::Result<()> {
    unsafe {
        if let Ok(categories) = CoCreateInstance::<_, ITfCategoryMgr>(&CLSID_TF_CategoryMgr, None, CLSCTX_INPROC_SERVER) {
            for cat in &CATEGORIES {
                let _ = categories.UnregisterCategory(&CLSID_ZUIN, cat, &CLSID_ZUIN);
            }
        }
        let profiles: ITfInputProcessorProfiles = CoCreateInstance(&CLSID_TF_InputProcessorProfiles, None, CLSCTX_INPROC_SERVER)?;
        profiles.Unregister(&CLSID_ZUIN)?;
    }
    Ok(())
}

/// 舊版更新器（dev.61 以前）只對版本目錄的 DLL 執行 regsvr32（#121）。這一版要從固定路徑載入，所以改請同目錄的
/// 0c-update 把這一版部署到 `<安裝目錄>\tsf\` 並註冊那裡（那次 regsvr32 從固定路徑執行，不會再走到這裡）。
/// 成功回傳 true；沒有 0c-update、逾時或失敗時回傳 false，照舊註冊版本目錄，下一次新版更新器執行時再改到固定路徑。
fn delegate_to_fixed() -> bool {
    use windows::Win32::Foundation::{CloseHandle, WAIT_OBJECT_0};
    use windows::Win32::System::Threading::{
        CreateProcessW, GetExitCodeProcess, WaitForSingleObject, CREATE_NO_WINDOW, PROCESS_INFORMATION, STARTUPINFOW,
    };
    if crate::layout::IS_32BIT {
        return false; // 更新器只對 x64 DLL 執行 regsvr32
    }
    let Ok(own) = dll_path() else { return false };
    let own = std::path::PathBuf::from(own);
    let Some(dir) = own.parent() else { return false };
    if crate::layout::version_base(dir).is_none() {
        return false;
    }
    let updater = dir.join("0c-update.exe");
    if !updater.is_file() {
        return false;
    }
    let app = wide(&updater.to_string_lossy());
    let mut cmd = wide(&format!("\"{}\" deploy-tsf", updater.to_string_lossy()));
    let si = STARTUPINFOW { cb: std::mem::size_of::<STARTUPINFOW>() as u32, ..Default::default() };
    let mut pi = PROCESS_INFORMATION::default();
    let started = unsafe {
        CreateProcessW(PCWSTR::from_raw(app.as_ptr()), Some(windows_core::PWSTR::from_raw(cmd.as_mut_ptr())), None, None, false, CREATE_NO_WINDOW, None, PCWSTR::null(), &si, &mut pi)
    };
    if let Err(e) = started {
        zlog!("register: 無法執行 0c-update deploy-tsf：{}", e);
        return false;
    }
    let ok = unsafe {
        let done = WaitForSingleObject(pi.hProcess, 120_000) == WAIT_OBJECT_0;
        let mut code = 1u32;
        let ok = done && GetExitCodeProcess(pi.hProcess, &mut code).is_ok() && code == 0;
        let _ = CloseHandle(pi.hThread);
        let _ = CloseHandle(pi.hProcess);
        zlog!("register: 0c-update deploy-tsf 完成={} 結束碼={}", done, code);
        ok
    };
    ok
}

pub fn register() -> windows_core::Result<()> {
    zlog!("register: begin");
    if delegate_to_fixed() {
        zlog!("register: 已改由固定路徑註冊");
        return Ok(());
    }
    let _com = ComInit::new();
    register_com()?;
    zlog!("register: com key written");
    register_profiles()
}

pub fn unregister() -> windows_core::Result<()> {
    let _com = ComInit::new();
    let a = unregister_profiles();
    let b = unregister_com();
    a.and(b)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn categories_include_comless() {
        // #157：沒有這個類別，COM-less 啟用的宿主（Minecraft Java 版）不載入靈犀。
        assert_eq!(GUID_TFCAT_TIPCAP_COMLESS, GUID::from_u128(0x364215d9_75bc_11d7_a6ef_00065b84435c));
        assert!(CATEGORIES.contains(&GUID_TFCAT_TIPCAP_COMLESS));
    }

    #[test]
    fn categories_keep_existing_capabilities() {
        // 註冊與取消註冊走同一份清單；#86 的 UI-less 宿主仍要 UIELEMENTENABLED。
        for cat in [
            GUID_TFCAT_TIP_KEYBOARD,
            GUID_TFCAT_TIPCAP_IMMERSIVESUPPORT,
            GUID_TFCAT_TIPCAP_SYSTRAYSUPPORT,
            GUID_TFCAT_TIPCAP_UIELEMENTENABLED,
            GUID_TFCAT_DISPLAYATTRIBUTEPROVIDER,
        ] {
            assert!(CATEGORIES.contains(&cat), "{:?}", cat);
        }
        let mut seen = std::collections::HashSet::new();
        assert!(CATEGORIES.iter().all(|c| seen.insert(c.to_u128())), "重複的類別");
    }
}
