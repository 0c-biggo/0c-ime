//! 0c-ui 升級交接：安裝、回復或自動更新後，0c-update 對舊版 0c-ui 的候選窗送 WM_CLOSE（#159 的全螢幕
//! 檢查在 0c-ui，舊的不退出就要重新登入才生效）。0c-ui 不立刻關：候選窗或提示還顯示著、或剛收到呈現訊息
//! （使用者正在打字，下一則可能已在路上）時先記下，每 RETIRE_POLL 再看一次，閒下來才結束。
//! 結束後由 TSF DLL 在下一次需要時啟動安裝收據所指的新版。
use std::time::Duration;

/// 最後一則呈現訊息之後要安靜多久才結束。
pub const RETIRE_QUIET: Duration = Duration::from_millis(1500);
/// 延後結束時多久再檢查一次。
pub const RETIRE_POLL_MS: u32 = 500;

/// 收到結束要求時可不可以現在結束。
pub fn ready_to_exit(visible: bool, since_last_present: Option<Duration>) -> bool {
    !visible && since_last_present.map_or(true, |d| d >= RETIRE_QUIET)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn exits_only_when_hidden_and_quiet() {
        // 從沒畫過東西：可以結束。
        assert!(ready_to_exit(false, None));
        // 候選窗顯示中：不結束（使用者在選字）。
        assert!(!ready_to_exit(true, None));
        assert!(!ready_to_exit(true, Some(Duration::from_secs(60))));
        // 剛收到隱藏訊息（打完一個字）：再等一下，下一個按鍵的顯示訊息可能已在路上。
        assert!(!ready_to_exit(false, Some(Duration::from_millis(200))));
        assert!(ready_to_exit(false, Some(RETIRE_QUIET)));
    }
}
