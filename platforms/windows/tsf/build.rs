use std::path::PathBuf;

/// 品牌圖示：與設定程式共用 platforms/settings/icons/icon.ico。輸入指示器以 TSF 註冊的圖示檔（DLL 本身、索引 0）
/// 顯示品牌圖示；DLL 沒有圖示資源時，系統改顯示語言縮寫（例如「繁體」）。
const BRAND_ICON: &str = "../../settings/icons/icon.ico";

fn main() {
    // DllGetClassObject 等匯出由 #[no_mangle] 提供；MSVC 連結器會提醒它們應為 PRIVATE（LNK4104），
    // 這對 in-proc COM server 沒有影響，關掉提醒。
    if std::env::var("CARGO_CFG_TARGET_ENV").as_deref() == Ok("msvc") {
        println!("cargo:rustc-link-arg=/IGNORE:4104");
        println!("cargo:rerun-if-changed={BRAND_ICON}");
        let ico = std::fs::read(BRAND_ICON).expect("讀取品牌圖示");
        let res = PathBuf::from(std::env::var("OUT_DIR").unwrap()).join("brand.res");
        std::fs::write(&res, icon_res(&ico)).expect("寫入品牌圖示資源");
        // link.exe 直接接受 .res，不需要 rc.exe。
        println!("cargo:rustc-link-arg={}", res.display());
    }
}

/// 把 .ico 轉成 .res：每張圖一個 RT_ICON（ID 1..n），加上一個 RT_GROUP_ICON（ID 1）。
fn icon_res(ico: &[u8]) -> Vec<u8> {
    let u16_at = |i: usize| u16::from_le_bytes([ico[i], ico[i + 1]]);
    let u32_at = |i: usize| u32::from_le_bytes([ico[i], ico[i + 1], ico[i + 2], ico[i + 3]]);
    assert!(ico.len() >= 6 && u16_at(0) == 0 && u16_at(2) == 1, "不是 .ico 檔");
    let count = u16_at(4) as usize;
    let mut out = Vec::new();
    resource(&mut out, 0, 0, 0, &[]); // .res 檔開頭的空項目
    let mut group = vec![0, 0, 1, 0];
    group.extend_from_slice(&(count as u16).to_le_bytes());
    for i in 0..count {
        let entry = 6 + 16 * i;
        let (size, offset) = (u32_at(entry + 8) as usize, u32_at(entry + 12) as usize);
        resource(&mut out, 3, i as u16 + 1, 0x1010, &ico[offset..offset + size]); // RT_ICON
        group.extend_from_slice(&ico[entry..entry + 12]); // 寬、高、色數、保留、planes、bit 數、大小
        group.extend_from_slice(&(i as u16 + 1).to_le_bytes());
    }
    resource(&mut out, 14, 1, 0x1030, &group); // RT_GROUP_ICON
    out
}

fn resource(out: &mut Vec<u8>, kind: u16, id: u16, flags: u16, data: &[u8]) {
    out.extend_from_slice(&(data.len() as u32).to_le_bytes());
    out.extend_from_slice(&32u32.to_le_bytes()); // header 大小
    for v in [0xffff, kind, 0xffff, id] {
        out.extend_from_slice(&u16::to_le_bytes(v));
    }
    out.extend_from_slice(&0u32.to_le_bytes()); // DataVersion
    out.extend_from_slice(&flags.to_le_bytes());
    out.extend_from_slice(&0u16.to_le_bytes()); // 語言中立
    out.extend_from_slice(&[0; 8]); // Version、Characteristics
    out.extend_from_slice(data);
    out.resize(out.len().next_multiple_of(4), 0);
}
