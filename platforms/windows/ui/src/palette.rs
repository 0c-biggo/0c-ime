//! 候選窗跟隨應用程式主題；高對比模式優先使用使用者指定的系統色。
use windows::Win32::Graphics::Gdi::{
    GetSysColor, COLOR_HIGHLIGHT, COLOR_HIGHLIGHTTEXT, COLOR_WINDOW, COLOR_WINDOWTEXT,
};
use windows::Win32::System::Registry::{RegGetValueW, HKEY_CURRENT_USER, RRF_RT_REG_DWORD};
use windows::Win32::UI::Accessibility::{HCF_HIGHCONTRASTON, HIGHCONTRASTW};
use windows::Win32::UI::WindowsAndMessaging::{
    SystemParametersInfoW, SPI_GETHIGHCONTRAST, SYSTEM_PARAMETERS_INFO_UPDATE_FLAGS,
};
use windows_core::w;

/// COLORREF 的 BGR 色值。
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Palette {
    pub background: u32,
    pub text: u32,
    pub hint: u32,
    pub border: u32,
    pub selection: u32,
    pub selected_text: u32,
    /// 一次性提示（#123「已加入詞庫：…」）的字色：一般主題用強調色，高對比用使用者的文字色。
    pub notice: u32,
    pub color_fonts: bool,
}

impl Palette {
    pub fn standard(dark: bool) -> Self {
        if dark {
            Self {
                background: 0x202020,
                text: 0xF2F2F2,
                hint: 0xB3B3B3,
                border: 0x707070,
                selection: 0xFFCF9B,
                selected_text: 0x181818,
                notice: 0xFFCF9B,
                color_fonts: true,
            }
        } else {
            Self {
                background: 0xFFFFFF,
                text: 0x202020,
                hint: 0x606060,
                border: 0xB0B0B0,
                selection: 0xB86600,
                selected_text: 0xFFFFFF,
                notice: 0xB86600,
                color_fonts: true,
            }
        }
    }
    fn contrast(background: u32, text: u32, selection: u32, selected_text: u32) -> Self {
        Self {
            background,
            text,
            hint: text,
            border: text,
            selection,
            selected_text,
            notice: text,
            color_fonts: false,
        }
    }
}

pub fn system_palette() -> Palette {
    unsafe {
        let mut hc = HIGHCONTRASTW {
            cbSize: std::mem::size_of::<HIGHCONTRASTW>() as u32,
            ..Default::default()
        };
        if SystemParametersInfoW(
            SPI_GETHIGHCONTRAST,
            hc.cbSize,
            Some((&mut hc as *mut HIGHCONTRASTW).cast()),
            SYSTEM_PARAMETERS_INFO_UPDATE_FLAGS(0),
        )
        .is_ok()
            && hc.dwFlags.contains(HCF_HIGHCONTRASTON)
        {
            return Palette::contrast(
                GetSysColor(COLOR_WINDOW),
                GetSysColor(COLOR_WINDOWTEXT),
                GetSysColor(COLOR_HIGHLIGHT),
                GetSysColor(COLOR_HIGHLIGHTTEXT),
            );
        }
        let mut light = 1u32;
        let mut size = std::mem::size_of_val(&light) as u32;
        let read = RegGetValueW(
            HKEY_CURRENT_USER,
            w!("Software\\Microsoft\\Windows\\CurrentVersion\\Themes\\Personalize"),
            w!("AppsUseLightTheme"),
            RRF_RT_REG_DWORD,
            None,
            Some((&mut light as *mut u32).cast()),
            Some(&mut size),
        );
        Palette::standard(read.is_ok() && light == 0)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn luminance(color: u32) -> f64 {
        let c = |shift: u32| {
            let v = ((color >> shift) & 255) as f64 / 255.0;
            if v <= 0.04045 {
                v / 12.92
            } else {
                ((v + 0.055) / 1.055).powf(2.4)
            }
        };
        0.2126 * c(0) + 0.7152 * c(8) + 0.0722 * c(16)
    }
    #[test]
    fn candidate_hint_and_selection_text_remain_readable_in_both_themes() {
        for dark in [false, true] {
            let p = Palette::standard(dark);
            for (foreground, background) in [
                (p.text, p.background),
                (p.hint, p.background),
                (p.notice, p.background),
                (p.selected_text, p.selection),
            ] {
                let a = luminance(foreground);
                let b = luminance(background);
                assert!(
                    (a.max(b) + 0.05) / (a.min(b) + 0.05) >= 4.5,
                    "dark={dark} foreground={foreground:x} background={background:x}"
                );
            }
        }
    }
    #[test]
    fn contrast_theme_keeps_all_user_selected_color_pairs() {
        let p = Palette::contrast(0x001122, 0xF0FFFF, 0x335500, 0xEECCFF);
        assert_eq!(
            (p.background, p.text, p.selection, p.selected_text),
            (0x001122, 0xF0FFFF, 0x335500, 0xEECCFF)
        );
        assert_eq!((p.hint, p.border, p.notice), (p.text, p.text, p.text));
    }
}
