//! 「我的詞彙」頁的即時同步（#125）：頁面開著時常駐一支 `0c-update vocab watch -since <版本>`，
//! 它在詞彙版本改變時印一行 `{"version":N,"count":M}`，這裡逐行轉交給前端（Tauri 事件 `vocab-changed`）。
//!
//! 離開頁面、視窗隱藏或程式結束時結束那支行程。它意外結束（更新交接換掉了執行檔、被系統結束）時
//! 等一下再以最後看到的版本重新啟動：版本在中斷期間變了，新的 watch 會立刻印出一行，頁面不會漏掉修改。

use serde_json::Value;
use std::io::{BufRead, BufReader};
use std::path::PathBuf;
use std::process::{Child, Command, Stdio};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::Duration;

/// 重新啟動的等待時間：從 1 秒起倍增，最多 30 秒；收到一行就重設。
const BACKOFF_MIN: Duration = Duration::from_secs(1);
const BACKOFF_MAX: Duration = Duration::from_secs(30);

struct Running {
    stop: Arc<AtomicBool>,
    child: Arc<Mutex<Option<Child>>>,
}

impl Running {
    fn halt(&self) {
        self.stop.store(true, Ordering::SeqCst);
        if let Some(mut child) = self.child.lock().unwrap_or_else(|e| e.into_inner()).take() {
            let _ = child.kill();
            let _ = child.wait();
        }
    }
}

/// 同一時間最多一支 watch；再次 start 會先結束舊的。
#[derive(Default)]
pub struct Watcher {
    running: Mutex<Option<Running>>,
}

pub fn watch_args(since: Option<u64>) -> Vec<String> {
    let mut args = vec!["vocab".to_string(), "watch".to_string()];
    if let Some(v) = since {
        args.push("-since".to_string());
        args.push(v.to_string());
    }
    args
}

/// watch 印出的一行：只轉交帶版本號的物件，其他（空行、雜訊）略過。
pub fn parse_line(line: &str) -> Option<Value> {
    let value: Value = serde_json::from_str(line.trim()).ok()?;
    value.get("version")?.as_u64()?;
    Some(value)
}

impl Watcher {
    pub fn start<F>(&self, updater: PathBuf, since: Option<u64>, emit: F)
    where
        F: Fn(Value) + Send + 'static,
    {
        let mut slot = self.running.lock().unwrap_or_else(|e| e.into_inner());
        if let Some(old) = slot.take() {
            old.halt();
        }
        let stop = Arc::new(AtomicBool::new(false));
        let child: Arc<Mutex<Option<Child>>> = Arc::new(Mutex::new(None));
        *slot = Some(Running { stop: stop.clone(), child: child.clone() });
        drop(slot);
        thread::spawn(move || run(updater, since, stop, child, emit));
    }

    pub fn stop(&self) {
        if let Some(old) = self.running.lock().unwrap_or_else(|e| e.into_inner()).take() {
            old.halt();
        }
    }

    #[cfg(all(test, unix))]
    pub fn active(&self) -> bool {
        self.running.lock().unwrap_or_else(|e| e.into_inner()).is_some()
    }
}

fn run<F: Fn(Value)>(updater: PathBuf, mut since: Option<u64>, stop: Arc<AtomicBool>, slot: Arc<Mutex<Option<Child>>>, emit: F) {
    let mut backoff = BACKOFF_MIN;
    while !stop.load(Ordering::SeqCst) {
        let mut command = Command::new(&updater);
        command.args(watch_args(since)).stdin(Stdio::null()).stdout(Stdio::piped()).stderr(Stdio::null());
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            command.creation_flags(0x0800_0000); // CREATE_NO_WINDOW
        }
        if let Ok(mut child) = command.spawn() {
            let stdout = child.stdout.take();
            {
                let mut guard = slot.lock().unwrap_or_else(|e| e.into_inner());
                if stop.load(Ordering::SeqCst) {
                    let _ = child.kill();
                    let _ = child.wait();
                    return;
                }
                *guard = Some(child);
            }
            if let Some(stdout) = stdout {
                for line in BufReader::new(stdout).lines() {
                    let Ok(line) = line else { break };
                    if let Some(value) = parse_line(&line) {
                        since = value["version"].as_u64();
                        backoff = BACKOFF_MIN;
                        if !stop.load(Ordering::SeqCst) {
                            emit(value);
                        }
                    }
                }
            }
            if let Some(mut child) = slot.lock().unwrap_or_else(|e| e.into_inner()).take() {
                let _ = child.kill();
                let _ = child.wait();
            }
        }
        // 意外結束：等一下再重新連上。分段睡，停止時馬上離開。
        let mut waited = Duration::ZERO;
        while waited < backoff && !stop.load(Ordering::SeqCst) {
            thread::sleep(Duration::from_millis(100));
            waited += Duration::from_millis(100);
        }
        backoff = (backoff * 2).min(BACKOFF_MAX);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[cfg(unix)]
    use std::sync::mpsc;
    #[cfg(unix)]
    use std::time::Instant;

    #[test]
    fn arguments_carry_the_last_seen_version() {
        assert_eq!(watch_args(None), vec!["vocab", "watch"]);
        assert_eq!(watch_args(Some(42)), vec!["vocab", "watch", "-since", "42"]);
    }

    #[test]
    fn only_versioned_lines_are_forwarded() {
        assert_eq!(parse_line(r#"{"version":3,"count":12}"#).unwrap()["count"], 12);
        assert!(parse_line("").is_none());
        assert!(parse_line("not json").is_none());
        assert!(parse_line(r#"{"ok":false}"#).is_none());
        assert!(parse_line(r#"{"version":"x"}"#).is_none());
    }

    #[cfg(unix)]
    fn script(name: &str, body: &str) -> PathBuf {
        use std::os::unix::fs::PermissionsExt;
        let dir = std::env::temp_dir().join(format!("zc-watch-test-{}-{}", std::process::id(), name));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("0c-update");
        std::fs::write(&path, format!("#!/bin/sh\n{body}\n")).unwrap();
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o755)).unwrap();
        path
    }

    /// 第一支印出版本 5 後結束（模擬引擎交接把它換掉）；重新啟動時要帶 -since 5，第二支印出 6 後一直等。
    #[cfg(unix)]
    #[test]
    fn restarts_after_the_watch_exits_and_resumes_from_the_last_version() {
        let updater = script("restart", r#"if [ "$4" = "5" ]; then echo '{"version":6,"count":2}'; exec sleep 30; fi; echo '{"version":5,"count":1}'"#);
        let watcher = Watcher::default();
        let (tx, rx) = mpsc::channel();
        watcher.start(updater, None, move |v| {
            let _ = tx.send(v["version"].as_u64().unwrap());
        });
        assert_eq!(rx.recv_timeout(Duration::from_secs(10)).unwrap(), 5);
        assert_eq!(rx.recv_timeout(Duration::from_secs(10)).unwrap(), 6);
        assert!(watcher.active());
        let start = Instant::now();
        watcher.stop();
        assert!(!watcher.active());
        assert!(start.elapsed() < Duration::from_secs(5), "停止時要結束還在等的 watch");
        assert!(rx.recv_timeout(Duration::from_millis(1500)).is_err(), "停止後不再轉交");
    }

    /// 再次 start 換掉舊的那支：舊的行程被結束，不會留下兩支。
    #[cfg(unix)]
    #[test]
    fn starting_again_replaces_the_previous_watch() {
        let updater = script("replace", r#"echo "{\"version\":${4:-1}}"; exec sleep 30"#);
        let watcher = Watcher::default();
        let (tx, rx) = mpsc::channel();
        let first = tx.clone();
        watcher.start(updater.clone(), None, move |v| {
            let _ = first.send(("first", v["version"].as_u64().unwrap()));
        });
        assert_eq!(rx.recv_timeout(Duration::from_secs(10)).unwrap(), ("first", 1));
        watcher.start(updater, Some(9), move |v| {
            let _ = tx.send(("second", v["version"].as_u64().unwrap()));
        });
        assert_eq!(rx.recv_timeout(Duration::from_secs(10)).unwrap(), ("second", 9));
        watcher.stop();
    }
}
