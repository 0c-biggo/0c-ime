//! DirectWrite／Direct2D 的彩色字型繪製；GDI 僅作不可用時的退回。
use windows::Win32::Foundation::RECT;
use windows::Win32::Graphics::Direct2D::Common::{
    D2D1_ALPHA_MODE_IGNORE, D2D1_COLOR_F, D2D1_PIXEL_FORMAT, D2D_RECT_F,
};
use windows::Win32::Graphics::Direct2D::{
    D2D1CreateFactory, ID2D1DCRenderTarget, ID2D1Factory, D2D1_DRAW_TEXT_OPTIONS_CLIP,
    D2D1_DRAW_TEXT_OPTIONS_ENABLE_COLOR_FONT, D2D1_FACTORY_TYPE_SINGLE_THREADED,
    D2D1_RENDER_TARGET_PROPERTIES,
};
use windows::Win32::Graphics::DirectWrite::{
    DWriteCreateFactory, IDWriteFactory, IDWriteTextFormat, DWRITE_FACTORY_TYPE_SHARED,
    DWRITE_FONT_STRETCH_NORMAL, DWRITE_FONT_STYLE_NORMAL, DWRITE_FONT_WEIGHT_NORMAL,
    DWRITE_MEASURING_MODE_NATURAL, DWRITE_PARAGRAPH_ALIGNMENT_CENTER, DWRITE_TEXT_ALIGNMENT_CENTER,
};
use windows::Win32::Graphics::Dxgi::Common::DXGI_FORMAT_B8G8R8A8_UNORM;
use windows::Win32::Graphics::Gdi::HDC;
use windows_core::w;

pub struct ColorText {
    target: ID2D1DCRenderTarget,
    format: IDWriteTextFormat,
}

fn color(value: u32) -> D2D1_COLOR_F {
    D2D1_COLOR_F {
        r: (value & 255) as f32 / 255.0,
        g: ((value >> 8) & 255) as f32 / 255.0,
        b: ((value >> 16) & 255) as f32 / 255.0,
        a: 1.0,
    }
}

impl ColorText {
    pub fn new(size: f32) -> windows_core::Result<Self> {
        unsafe {
            let factory: ID2D1Factory = D2D1CreateFactory(D2D1_FACTORY_TYPE_SINGLE_THREADED, None)?;
            let target = factory.CreateDCRenderTarget(&D2D1_RENDER_TARGET_PROPERTIES {
                pixelFormat: D2D1_PIXEL_FORMAT {
                    format: DXGI_FORMAT_B8G8R8A8_UNORM,
                    alphaMode: D2D1_ALPHA_MODE_IGNORE,
                },
                dpiX: 96.0,
                dpiY: 96.0,
                ..Default::default()
            })?;
            let write: IDWriteFactory = DWriteCreateFactory(DWRITE_FACTORY_TYPE_SHARED)?;
            let format = write.CreateTextFormat(
                w!("Segoe UI Emoji"),
                None,
                DWRITE_FONT_WEIGHT_NORMAL,
                DWRITE_FONT_STYLE_NORMAL,
                DWRITE_FONT_STRETCH_NORMAL,
                size,
                w!("zh-TW"),
            )?;
            format.SetTextAlignment(DWRITE_TEXT_ALIGNMENT_CENTER)?;
            format.SetParagraphAlignment(DWRITE_PARAGRAPH_ALIGNMENT_CENTER)?;
            Ok(Self { target, format })
        }
    }

    /// DC 可以來自 WM_PAINT 或 WM_PRINTCLIENT；每格完整清底，避免蓋住選取色。
    pub fn draw(
        &self,
        hdc: HDC,
        rect: RECT,
        text: &[u16],
        foreground: u32,
        background: u32,
    ) -> windows_core::Result<()> {
        unsafe {
            self.target.BindDC(hdc, &rect)?;
            let brush = self
                .target
                .CreateSolidColorBrush(&color(foreground), None)?;
            self.target.BeginDraw();
            self.target.Clear(Some(&color(background)));
            self.target.DrawText(
                text,
                &self.format,
                &D2D_RECT_F {
                    left: 0.0,
                    top: 0.0,
                    right: (rect.right - rect.left) as f32,
                    bottom: (rect.bottom - rect.top) as f32,
                },
                &brush,
                D2D1_DRAW_TEXT_OPTIONS_ENABLE_COLOR_FONT | D2D1_DRAW_TEXT_OPTIONS_CLIP,
                DWRITE_MEASURING_MODE_NATURAL,
            );
            self.target.EndDraw(None, None)
        }
    }
}
