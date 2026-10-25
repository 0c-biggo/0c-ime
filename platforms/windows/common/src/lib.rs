//! TSF DLL 與 0c-ui 共用的小東西。沒有任何 UI 或協定邏輯。
use std::ffi::c_void;

pub mod menu;

use serde::{Deserialize, Serialize};
use windows::Win32::Foundation::{CloseHandle, LocalFree, HANDLE, HLOCAL};
use windows::Win32::Security::Authorization::ConvertSidToStringSidW;
use windows::Win32::Security::{GetTokenInformation, TokenUser, TOKEN_QUERY, TOKEN_USER};
use windows::Win32::System::Threading::{GetCurrentProcess, OpenProcessToken};
use windows_core::PWSTR;

/// 以 NUL 結尾的 UTF-16。
pub fn wide(s: &str) -> Vec<u16> {
    let mut v: Vec<u16> = s.encode_utf16().collect();
    v.push(0);
    v
}

/// 目前使用者的 SID 字串（小寫），與 0c-server 的 defaultPipe 一致。
pub fn user_sid() -> Option<String> {
    unsafe {
        let mut token = HANDLE::default();
        OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut token).ok()?;
        let mut len = 0u32;
        let _ = GetTokenInformation(token, TokenUser, None, 0, &mut len);
        let mut buf = vec![0u8; len as usize];
        let r = GetTokenInformation(
            token,
            TokenUser,
            Some(buf.as_mut_ptr() as *mut c_void),
            len,
            &mut len,
        );
        let _ = CloseHandle(token);
        r.ok()?;
        let tu = &*(buf.as_ptr() as *const TOKEN_USER);
        let mut s = PWSTR::null();
        ConvertSidToStringSidW(tu.User.Sid, &mut s).ok()?;
        let out = s.to_string().ok();
        let _ = LocalFree(Some(HLOCAL(s.0 as *mut c_void)));
        out.map(|x| x.to_lowercase())
    }
}

fn user_pipe(prefix: &str) -> String {
    match user_sid() {
        Some(sid) => format!(r"\\.\pipe\{}-{}", prefix, sid),
        None => format!(r"\\.\pipe\{}", prefix),
    }
}

/// 0c-server 的管道。
pub fn server_pipe() -> String {
    user_pipe("0c")
}

/// 舊版 zuin-server 的管道。升級過渡期舊 server 仍在執行時，它是個人詞庫唯一的寫入者，新 DLL 先接上它。
pub fn legacy_server_pipe() -> String {
    user_pipe("zuin")
}

/// 0c-ui（候選窗）的管道。
pub fn ui_pipe() -> String {
    user_pipe("0c-ui")
}

/// 輸入法選單的雙向管道（DLL 問狀態、請 0c-ui 執行 0c-update）。
pub fn menu_pipe() -> String {
    user_pipe("0c-menu")
}

/// 舊版 DLL 連的候選窗管道；0c-ui 在過渡期一併服務。
pub fn legacy_ui_pipe() -> String {
    user_pipe("zuin-ui")
}

/// 管道的存取控制（與 0c-server 相同）：本人完全存取；ALL APPLICATION PACKAGES 與
/// ALL RESTRICTED APPLICATION PACKAGES 可讀寫（沙箱宿主）；低完整性等級可開啟。其他使用者連不上。
pub fn pipe_sddl() -> String {
    let sid = user_sid().unwrap_or_else(|| "OW".to_string());
    format!(
        "D:P(A;;GA;;;{})(A;;GRGW;;;S-1-15-2-1)(A;;GRGW;;;S-1-15-2-2)S:(ML;;NW;;;LW)",
        sid.to_uppercase()
    )
}

/// 訊框：4 位元組小端序長度＋JSON（與 bridge/ipc 相同）。
pub const MAX_FRAME: u32 = 1 << 20;

pub fn frame<T: Serialize>(v: &T) -> Option<Vec<u8>> {
    let body = serde_json::to_vec(v).ok()?;
    if body.len() as u32 > MAX_FRAME {
        return None;
    }
    let mut out = (body.len() as u32).to_le_bytes().to_vec();
    out.extend_from_slice(&body);
    Some(out)
}

#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
pub struct UiCandidate {
    pub id: i64,
    pub text: String,
    /// 低調附註（模糊音近音為「近音 ㄓ」）；沒有附註、或舊 DLL 沒送時為空字串。
    #[serde(default)]
    pub note: String,
    /// 鍵標（#112）：核心給的選字鍵（1、q、;）；Some("") 不畫（英文模式的補字候選）。
    /// 舊 DLL 沒送時為 None：依位置畫 1–0（見 candidate_key）。
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub key: Option<String>,
}

/// 第 index 項候選的鍵標（#112）：DLL 送來的選字鍵；舊 DLL 沒送時依位置 1–9、0（第 10 項按 0 選，不是「10」）。
pub fn candidate_key(index: usize, c: &UiCandidate) -> String {
    match &c.key {
        Some(key) => key.clone(),
        None => ((index + 1) % 10).to_string(),
    }
}

/// DLL 送給 0c-ui 的呈現訊息：畫什麼、畫在哪。不含文件文字，不含組字以外的內容。
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
pub struct UiMessage {
    /// false：隱藏候選窗（其餘欄位忽略）。
    pub show: bool,
    /// 游標的螢幕座標矩形（實體像素）。
    pub x: i32,
    pub y: i32,
    pub w: i32,
    pub h: i32,
    pub candidates: Vec<UiCandidate>,
    /// 反白的候選在 candidates 裡的索引。
    pub highlight: i64,
    /// 1 起算的頁碼與總頁數。
    pub page: i64,
    pub pages: i64,
    #[serde(default)]
    pub columns: usize,
    #[serde(default)]
    pub kind: String,
    #[serde(default)]
    pub title: String,
    /// Tab 補全提示（一般為後半；同音替換時為「原文 → 完整詞」）。
    pub suggestion: String,
    /// 核心的一次性提示（例如「已加入詞庫：世傑」，#123），畫在最上面；幾秒後或下一鍵後消失。
    /// 舊 DLL 沒送時為空字串。
    #[serde(default)]
    pub notice: String,
    /// 組字區還在標示要加入詞庫的範圍（核心樣式 marked，#123）：這時的提示是標示的說明，
    /// 留到標示結束，不自動收起。舊 DLL 沒送時為 false（照舊幾秒後收起）。
    #[serde(default)]
    pub marking: bool,
    /// 讀音提示（#162）：中英混打時照字面顯示的數字按空白會組成的注音（20 → ㄉㄢ），畫在游標下方；
    /// Enter 留數字。舊 DLL 沒送時為空字串；舊 0c-ui 不認得時忽略。
    #[serde(default)]
    pub reading: String,
    /// 送出者（宿主行程 ID）；不同宿主的訊息互相取代。
    pub owner: u32,
    /// 同一宿主 TSF 執行緒的訊息視窗；舊 UI 可忽略，舊 DLL 預設不接受滑鼠操作。
    #[serde(default)]
    pub target: u64,
    #[serde(default)]
    pub revision: u32,
}

/// UI → TSF：wParam 是畫面版本，lParam 是候選 ID；不傳文字或指標。
pub const WM_SELECT_CANDIDATE: u32 = 0x8000 + 55;

/// 候選之前的列：提示（notice）、讀音提示（reading）、補全提示（suggestion）、標題（title），依此順序。
/// 提示優先（同 Linux 外殼）：有提示時取代標題，下一個快照沒有提示（或提示到時收起）標題就回來。
/// 讀音提示只在沒有候選時出現（核心開候選時不送）。
pub fn header_lines(m: &UiMessage) -> Vec<&str> {
    let title = if m.notice.is_empty() { m.title.as_str() } else { "" };
    [m.notice.as_str(), m.reading.as_str(), m.suggestion.as_str(), title]
        .into_iter()
        .filter(|s| !s.is_empty())
        .collect()
}

/// 提示列的高度：提示、讀音提示與補全提示用一般字級（比標題的小字大），列高跟著放大。
/// 繪製與命中測試都用這個值，兩者的位置才會一致。
pub fn notice_row(small_row: i32) -> i32 {
    small_row * 7 / 5
}

/// 這一列是不是用一般字級畫（提示、讀音提示、補全提示），標題是小字。
/// 補全提示（Tab 接受）跟候選同字級，小字看不清楚（#118）。
pub fn large_heading(m: &UiMessage, heading: &str) -> bool {
    !m.notice.is_empty() && heading == m.notice
        || !m.reading.is_empty() && heading == m.reading
        || !m.suggestion.is_empty() && heading == m.suggestion
}

/// 候選之前所有列的總高度（提示、讀音提示與補全提示每列 notice_row，標題 small_row）。
pub fn header_height(m: &UiMessage, small_row: i32) -> i32 {
    header_lines(m)
        .iter()
        .map(|h| if large_heading(m, h) { notice_row(small_row) } else { small_row })
        .sum()
}

/// 提示顯示多久後由候選窗自己收起（0c-ui 的計時器）；TSF 外殼重送舊畫面時也依此判斷提示是否已收起。
pub const NOTICE_MS: u32 = 4000;

/// 提示是否要計時自動收起：有提示、且不是標示中的說明（標示中說明留到標示結束）。
pub fn notice_expires(m: &UiMessage) -> bool {
    !m.notice.is_empty() && !m.marking
}

/// 呈現訊息是否有東西可畫（候選、補全提示、提示或讀音提示）。
pub fn has_content(m: &UiMessage) -> bool {
    !m.candidates.is_empty() || !m.suggestion.is_empty() || !m.notice.is_empty() || !m.reading.is_empty()
}

/// 與繪製一致的候選列命中；提示、頁碼、邊距不會選字。
pub fn hit_candidate(m: &UiMessage, y: i32, pad: i32, row: i32, small_row: i32) -> Option<i64> {
    let first = pad + header_height(m, small_row);
    if y < first || row <= 0 {
        return None;
    }
    m.candidates.get(((y - first) / row) as usize).map(|c| c.id)
}

/// Grid geometry is shared by drawing and pointer selection. It retains empty
/// slots on a partial final row, so those slots never select a different item.
pub fn candidate_cells(
    m: &UiMessage,
    width: i32,
    pad: i32,
    row: i32,
    small_row: i32,
) -> Vec<CandidateCell> {
    let columns = m.columns.clamp(1, 10);
    let gap = if columns > 1 { pad / 2 } else { 0 };
    let cell_width = ((width - 2 * pad - gap * (columns as i32 - 1)) / columns as i32).max(1);
    let top = pad + header_height(m, small_row);
    m.candidates
        .iter()
        .enumerate()
        .map(|(index, candidate)| CandidateCell {
            id: candidate.id,
            left: pad + (index % columns) as i32 * (cell_width + gap),
            top: top + (index / columns) as i32 * row,
            width: cell_width,
            height: row,
        })
        .collect()
}

#[derive(Clone, Debug, PartialEq)]
pub struct CandidateCell {
    pub id: i64,
    pub left: i32,
    pub top: i32,
    pub width: i32,
    pub height: i32,
}

impl CandidateCell {
    pub fn contains(&self, x: i32, y: i32) -> bool {
        x >= self.left && x < self.left + self.width && y >= self.top && y < self.top + self.height
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn horizontal_ten_candidates_stay_on_one_row() {
        let message = UiMessage {
            columns: 10,
            candidates: (0..10).map(|id| UiCandidate { id, text: "字".into(), ..Default::default() }).collect(),
            ..Default::default()
        };
        let cells = candidate_cells(&message, 1000, 8, 40, 20);
        assert_eq!(cells.len(), 10);
        assert!(cells.iter().all(|c| c.top == cells[0].top));
        assert!(cells.windows(2).all(|c| c[0].left < c[1].left));
    }

    #[test]
    fn grid_cells_preserve_ids_gaps_and_partial_rows() {
        let message = UiMessage {
            columns: 5,
            title: "palette".into(),
            candidates: (0..7)
                .map(|i| UiCandidate {
                    id: i * 3 + 17,
                    text: "😀".into(),
                    ..Default::default()
                })
                .collect(),
            ..Default::default()
        };
        for scale in [1, 2, 3, 4] {
            let cells = candidate_cells(&message, 600 * scale, 8 * scale, 40 * scale, 20 * scale);
            assert_eq!(cells[5].left, cells[0].left);
            assert_eq!(cells[5].top, cells[0].top + 40 * scale);
            for cell in &cells {
                assert_eq!(
                    cells
                        .iter()
                        .find(|other| other.contains(cell.left + 1, cell.top + 1))
                        .map(|other| other.id),
                    Some(cell.id)
                );
            }
            assert!(cells
                .iter()
                .all(|cell| !cell.contains(cells[2].left, cells[5].top)));
            assert!(cells.iter().all(|cell| !cell.contains(0, 0)));
        }
    }
    #[test]
    fn candidate_note_is_optional_for_older_senders() {
        let old: UiCandidate = serde_json::from_str(r#"{"id":1,"text":"知"}"#).unwrap();
        assert_eq!(old.note, "", "舊 DLL 不送 note 時仍可解析");
        assert_eq!(old.key, None, "舊 DLL 不送 key");
        let now = UiCandidate {
            id: 2,
            text: "知".into(),
            note: "近音 ㄓ".into(),
            key: Some("q".into()),
        };
        let back: UiCandidate =
            serde_json::from_str(&serde_json::to_string(&now).unwrap()).unwrap();
        assert_eq!(back, now);
    }

    #[test]
    fn candidate_hit_ignores_hint_footer_and_padding() {
        let m = UiMessage {
            suggestion: "提示".into(),
            candidates: vec![
                UiCandidate {
                    id: 8,
                    text: "你".into(),
                    ..Default::default()
                },
                UiCandidate {
                    id: 42,
                    text: "妳".into(),
                    ..Default::default()
                },
            ],
            ..Default::default()
        };
        // 補全提示一般字級一列 28（#118）：第一個候選從 8 + 28 開始。
        for y in [0, 7, 8, 35, 92, 100] {
            assert_eq!(hit_candidate(&m, y, 8, 28, 20), None);
        }
        assert_eq!(hit_candidate(&m, 36, 8, 28, 20), Some(8));
        assert_eq!(hit_candidate(&m, 63, 8, 28, 20), Some(8));
        assert_eq!(hit_candidate(&m, 64, 8, 28, 20), Some(42));
    }

    #[test]
    fn notice_is_optional_and_shifts_candidate_hits() {
        let old: UiMessage = serde_json::from_str(
            r#"{"show":true,"x":0,"y":0,"w":0,"h":0,"candidates":[],"highlight":0,"page":1,"pages":1,"suggestion":"","owner":1}"#,
        )
        .unwrap();
        assert_eq!(old.notice, "", "舊 DLL 不送 notice 時仍可解析");
        assert!(!old.marking, "舊 DLL 不送 marking 時照舊計時收起");
        assert!(!has_content(&old));
        let only_notice = UiMessage {
            notice: "已加入詞庫：世傑".into(),
            ..Default::default()
        };
        assert!(has_content(&only_notice), "沒有候選時也要顯示提示");
        assert_eq!(header_lines(&only_notice), vec!["已加入詞庫：世傑"]);
        let m = UiMessage {
            notice: "已從詞庫刪除：世傑（可在設定程式復原）".into(),
            title: "標題".into(),
            candidates: vec![UiCandidate {
                id: 5,
                text: "世界".into(),
                ..Default::default()
            }],
            ..Default::default()
        };
        // 提示優先：有提示時標題不畫，提示一列 28（20 × 7 / 5）：第一個候選從 8 + 28 開始。
        assert_eq!(header_lines(&m), vec![m.notice.as_str()]);
        assert_eq!(header_height(&m, 20), 28);
        assert_eq!(hit_candidate(&m, 35, 8, 28, 20), None);
        assert_eq!(hit_candidate(&m, 36, 8, 28, 20), Some(5));
        let cells = candidate_cells(&m, 300, 8, 28, 20);
        assert_eq!(cells[0].top, 36);
        // 補全提示照常在提示之後，也是一般字級（#118）；提示收起後標題回來。
        let with_hint = UiMessage { suggestion: "補全".into(), ..m.clone() };
        assert_eq!(header_lines(&with_hint), vec![m.notice.as_str(), "補全"]);
        assert_eq!(header_height(&with_hint, 20), 56);
        assert_eq!(hit_candidate(&with_hint, 63, 8, 28, 20), None);
        assert_eq!(hit_candidate(&with_hint, 64, 8, 28, 20), Some(5));
        let later = UiMessage { notice: String::new(), ..m.clone() };
        assert_eq!(header_lines(&later), vec!["標題"]);
        assert_eq!(hit_candidate(&later, 28, 8, 28, 20), Some(5));
        let back: UiMessage = serde_json::from_str(&serde_json::to_string(&m).unwrap()).unwrap();
        assert_eq!(back, m);
    }

    #[test]
    fn vertical_title_and_suggestion_are_not_candidate_hits() {
        for suggestion in ["", "補全提示"] {
            let m = UiMessage {
                title: "修改「機」 · Tab 切換詞長".into(),
                suggestion: suggestion.into(),
                candidates: vec![
                    UiCandidate {
                        id: 8,
                        text: "機".into(),
                        ..Default::default()
                    },
                    UiCandidate {
                        id: 42,
                        text: "基".into(),
                        ..Default::default()
                    },
                ],
                ..Default::default()
            };
            let first = if suggestion.is_empty() { 28 } else { 56 }; // 補全提示一般字級一列 28（#118）
            for y in 0..first {
                assert_eq!(hit_candidate(&m, y, 8, 28, 20), None);
            }
            assert_eq!(hit_candidate(&m, first, 8, 28, 20), Some(8));
            assert_eq!(hit_candidate(&m, first + 28, 8, 28, 20), Some(42));
            assert_eq!(hit_candidate(&m, first + 56, 8, 28, 20), None);
        }
    }

    #[test]
    fn reading_hint_is_optional_and_draws_alone() {
        let old: UiMessage = serde_json::from_str(
            r#"{"show":true,"x":0,"y":0,"w":0,"h":0,"candidates":[],"highlight":0,"page":1,"pages":1,"suggestion":"","owner":1}"#,
        )
        .unwrap();
        assert_eq!(old.reading, "", "舊 DLL 不送 reading 時仍可解析");
        // #162：只有讀音提示（20 → ㄉㄢ）時也要畫，一般字級一列；不計時收起（組字還在就留著）。
        let m = UiMessage {
            show: true,
            reading: "ㄉㄢ".into(),
            ..Default::default()
        };
        assert!(has_content(&m));
        assert_eq!(header_lines(&m), vec!["ㄉㄢ"]);
        assert_eq!(header_height(&m, 20), 28);
        assert!(!notice_expires(&m));
        // 提示優先，讀音提示在補全提示之前。
        let both = UiMessage { notice: "已加入詞庫：世傑".into(), suggestion: "補全".into(), ..m.clone() };
        assert_eq!(header_lines(&both), vec!["已加入詞庫：世傑", "ㄉㄢ", "補全"]);
        assert_eq!(header_height(&both, 20), 28 + 28 + 28);
        let back: UiMessage = serde_json::from_str(&serde_json::to_string(&m).unwrap()).unwrap();
        assert_eq!(back, m);
    }

    #[test]
    fn marking_hint_stays_until_the_mark_ends() {
        let hint = UiMessage {
            show: true,
            notice: "Enter 加入詞庫：世傑（ㄕˋ ㄐㄧㄝˊ）　Esc 取消".into(),
            marking: true,
            ..Default::default()
        };
        assert!(!notice_expires(&hint), "標示中的說明不自動收起");
        let back: UiMessage =
            serde_json::from_str(&serde_json::to_string(&hint).unwrap()).unwrap();
        assert_eq!(back, hint);
        let added = UiMessage {
            notice: "已加入詞庫：世傑".into(),
            marking: false,
            ..hint.clone()
        };
        assert!(notice_expires(&added), "標示結束後的結果照舊計時收起");
        let quiet = UiMessage { notice: String::new(), ..hint };
        assert!(!notice_expires(&quiet), "沒有提示就不計時");
    }
}
