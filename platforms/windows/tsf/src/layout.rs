//! 版本目錄裡兩份 DLL 的位置（#112）：x64 的 `0c_tsf.dll` 與 `0c-server.exe`、`0c-ui.exe` 同目錄，
//! 32 位元應用程式用的 i686 版在子目錄 `x86\0c_tsf.dll`。引擎與候選窗只有 x64 一份，32 位元 DLL 經
//! named pipe 連它們（管道跨位元可用），所以要從 `x86\` 往上一層找程式。
//!
//! 0.1.0-dev.62 起註冊的是固定路徑（#121）：`<安裝目錄>\tsf\0c_tsf.dll` 與 `<安裝目錄>\tsf\x86\0c_tsf.dll`，
//! 是版本目錄的複本，每次更新路徑不變（防毒軟體的例外清單只接受完整路徑）。這時程式在安裝收據記錄的版本目錄。
use std::path::{Path, PathBuf};

/// 32 位元 DLL 所在的子目錄名稱（與 internal/update 的 WindowsDLL32 相同）。
pub const X86_DIR: &str = "x86";

/// 固定路徑的目錄名稱（與 internal/update 的 FixedDir 相同）。
pub const FIXED_DIR: &str = "tsf";

/// 本 DLL 是否為 32 位元版。
pub const IS_32BIT: bool = cfg!(target_pointer_width = "32");

fn is_x86_dir(dir: &Path) -> bool {
    dir.file_name().is_some_and(|n| n.to_string_lossy().eq_ignore_ascii_case(X86_DIR))
}

fn named(dir: &Path, name: &str) -> bool {
    dir.file_name().is_some_and(|n| n.to_string_lossy().eq_ignore_ascii_case(name))
}

/// 本 DLL 在固定路徑時回傳安裝目錄（`tsf\` 的上一層）；在版本目錄或其他地方時為 None。
pub fn fixed_base(dll_dir: &Path, is_32bit: bool) -> Option<PathBuf> {
    let tsf = if is_32bit && is_x86_dir(dll_dir) { dll_dir.parent()? } else { dll_dir };
    if named(tsf, FIXED_DIR) {
        tsf.parent().map(Path::to_path_buf)
    } else {
        None
    }
}

/// 本 DLL 在某個版本目錄（`versions\<名稱>\0c_tsf.dll`）時回傳安裝目錄。舊版更新器（dev.61 以前）只對這裡的 DLL
/// 執行 regsvr32，註冊時要改由同目錄的 0c-update 部署到固定路徑（見 register.rs）。
pub fn version_base(dll_dir: &Path) -> Option<PathBuf> {
    let versions = dll_dir.parent()?;
    if named(versions, "versions") {
        versions.parent().map(Path::to_path_buf)
    } else {
        None
    }
}

/// 安裝收據（install-receipt.json）記錄的版本目錄；只接受 `versions/<名稱>`，不含 ..、反斜線或更深的層級。
pub fn receipt_directory(json: &str) -> Option<String> {
    let value: serde_json::Value = serde_json::from_str(json).ok()?;
    let dir = value["directory"].as_str()?;
    let mut parts = dir.split('/');
    match (parts.next(), parts.next(), parts.next()) {
        (Some("versions"), Some(name), None) if !name.is_empty() && name != "." && name != ".." && !dir.contains('\\') => {
            Some(dir.to_string())
        }
        _ => None,
    }
}

/// 0c-server.exe、0c-ui.exe 與 data 所在的版本目錄。32 位元 DLL 在 `x86\` 子目錄時是上一層。
pub fn program_dir(dll_dir: &Path, is_32bit: bool) -> PathBuf {
    match dll_dir.parent() {
        Some(parent) if is_32bit && is_x86_dir(dll_dir) => parent.to_path_buf(),
        _ => dll_dir.to_path_buf(),
    }
}

/// 註冊用：由本 DLL 的路徑推出 (x64 DLL, x86 DLL)。任一份 DLL 註冊時都寫兩個登錄視圖，
/// 所以從哪一份執行 regsvr32 結果都相同；舊版更新器只對 x64 DLL 執行 regsvr32，也能補上 32 位元的註冊。
/// 回傳的路徑不保證存在，由呼叫端核對。
pub fn dll_pair(own: &Path, is_32bit: bool) -> (Option<PathBuf>, Option<PathBuf>) {
    let name = own.file_name().map(PathBuf::from);
    let dir = own.parent();
    match (name, dir) {
        (Some(name), Some(dir)) if is_32bit => {
            let x64 = if is_x86_dir(dir) { dir.parent().map(|p| p.join(&name)) } else { None };
            (x64, Some(own.to_path_buf()))
        }
        (Some(name), Some(dir)) => (Some(own.to_path_buf()), Some(dir.join(X86_DIR).join(name))),
        _ if is_32bit => (None, Some(own.to_path_buf())),
        _ => (Some(own.to_path_buf()), None),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const V: &str = r"C:\Users\u\AppData\Local\Zuin\versions\0.1.0-dev.62-abc";

    #[test]
    fn x64_finds_programs_beside_itself() {
        assert_eq!(program_dir(Path::new(V), false), PathBuf::from(V));
        // x64 DLL 不會放在 x86 目錄；即使如此也不往上找。
        assert_eq!(program_dir(&Path::new(V).join("x86"), false), Path::new(V).join("x86"));
    }

    #[test]
    fn x86_finds_programs_one_level_up() {
        assert_eq!(program_dir(&Path::new(V).join("x86"), true), PathBuf::from(V));
        assert_eq!(program_dir(&Path::new(V).join("X86"), true), PathBuf::from(V));
        // 手動放在別處的 32 位元 DLL（開發測試）：照舊找同目錄。
        assert_eq!(program_dir(Path::new(V), true), PathBuf::from(V));
    }

    #[test]
    fn fixed_path_finds_the_installation() {
        let base = Path::new(r"C:\Users\u\AppData\Local\Zuin");
        assert_eq!(fixed_base(&base.join("tsf"), false), Some(base.to_path_buf()));
        assert_eq!(fixed_base(&base.join("TSF"), false), Some(base.to_path_buf()));
        assert_eq!(fixed_base(&base.join("tsf").join("x86"), true), Some(base.to_path_buf()));
        // 32 位元 DLL 不會直接在 tsf\；x64 DLL 不會在 tsf\x86。
        assert_eq!(fixed_base(&base.join("tsf").join("x86"), false), None);
        assert_eq!(fixed_base(Path::new(V), false), None);
        assert_eq!(fixed_base(&Path::new(V).join("x86"), true), None);
        assert_eq!(version_base(Path::new(V)), Some(base.to_path_buf()));
        assert_eq!(version_base(&base.join("tsf")), None);
        // 固定路徑的 DLL 註冊時，32 位元在 tsf\x86。
        let x64 = base.join("tsf").join("0c_tsf.dll");
        assert_eq!(dll_pair(&x64, false).1, Some(base.join("tsf").join("x86").join("0c_tsf.dll")));
    }

    #[test]
    fn receipt_directory_is_validated() {
        assert_eq!(receipt_directory(r#"{"directory":"versions/0.1.0-dev.62-abc","active":true}"#).as_deref(), Some("versions/0.1.0-dev.62-abc"));
        for bad in [r#"{"directory":"."}"#, r#"{"directory":"versions/.."}"#, r#"{"directory":"versions/a/b"}"#,
                    r#"{"directory":"versions\\a"}"#, r#"{"directory":"other/a"}"#, r#"{}"#, "not json"] {
            assert_eq!(receipt_directory(bad), None, "{bad}");
        }
    }

    #[test]
    fn pair_from_either_dll() {
        let x64 = Path::new(V).join("0c_tsf.dll");
        let x86 = Path::new(V).join("x86").join("0c_tsf.dll");
        assert_eq!(dll_pair(&x64, false), (Some(x64.clone()), Some(x86.clone())));
        assert_eq!(dll_pair(&x86, true), (Some(x64.clone()), Some(x86.clone())));
        // 32 位元 DLL 不在 x86 子目錄：只有自己。
        let loose = Path::new(V).join("0c_tsf.dll");
        assert_eq!(dll_pair(&loose, true), (None, Some(loose.clone())));
    }
}
