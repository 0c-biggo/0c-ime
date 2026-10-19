//! 螢幕座標可為負數；候選窗在縮放後也必須留在工作區。
use windows::Win32::Foundation::RECT;

pub fn place(x: i32, y: i32, caret_height: i32, width: i32, height: i32, work: RECT) -> RECT {
    let width = width
        .max(1)
        .min(work.right.saturating_sub(work.left).max(1));
    let height = height
        .max(1)
        .min(work.bottom.saturating_sub(work.top).max(1));
    let left = x.clamp(work.left, work.right.saturating_sub(width).max(work.left));
    let below = y.saturating_add(caret_height).saturating_add(2);
    let top = if below.saturating_add(height) <= work.bottom {
        below
    } else {
        y.saturating_sub(height).saturating_sub(2)
    }
    .clamp(work.top, work.bottom.saturating_sub(height).max(work.top));
    RECT {
        left,
        top,
        right: left.saturating_add(width),
        bottom: top.saturating_add(height),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn scaled_windows_fit_negative_monitors_and_extreme_carets() {
        for work in [
            RECT {
                left: 0,
                top: 0,
                right: 1920,
                bottom: 1040,
            },
            RECT {
                left: -1920,
                top: -200,
                right: 0,
                bottom: 840,
            },
        ] {
            for (x, y) in [
                (work.left, work.top),
                (work.right - 1, work.bottom - 1),
                (i32::MIN, i32::MIN),
                (i32::MAX, i32::MAX),
            ] {
                for dpi in [96, 120, 144, 192, 288, 384] {
                    let r = place(x, y, 20 * dpi / 96, 400 * dpi / 96, 600 * dpi / 96, work);
                    assert!(
                        r.left >= work.left
                            && r.top >= work.top
                            && r.right <= work.right
                            && r.bottom <= work.bottom
                    );
                    assert!(r.right > r.left && r.bottom > r.top);
                }
            }
        }
    }
}
