//! 一個輸入欄位在 0c-server 上的 session：請求、重連後的重播、快照的解讀。
//! 與 Fcitx 5 外殼的 session.cpp 對應；不依賴 TSF，可單獨測試。
use serde_json::{json, Value};

use crate::ipc::Client;
use crate::log::zlog;

pub const PROTOCOL_VERSION: i64 = 0;

#[derive(Clone, Debug, Default)]
pub struct Segment {
    pub text: String,
    pub style: String,
}

#[derive(Clone, Debug, Default)]
pub struct Snapshot {
    pub ok: bool,      // false：server 不可用，呼叫端應放行按鍵
    pub handled: bool, // false：把原始按鍵交還應用程式
    pub commit: String,
    pub preedit: Vec<Segment>,
    pub cursor_rune: usize,
    pub cursor_utf16: usize, // 核心直接提供 UTF-16 位置；TSF 的範圍以 UTF-16 計
    pub suggestion: String,
    pub candidates: Vec<(i64, String)>, // 目前這一頁的（ID, 文字）
    pub candidate_notes: Vec<String>, // 與 candidates 等長的低調附註（模糊音近音為「近音 ㄓ」），其餘為空字串
    /// 與 candidates 等長的鍵標（#112）：核心給的選字鍵（1、q、;）；舊核心沒有時依位置 1–0，
    /// 英文模式（只有自動補字候選，數字是文字）為空字串、不畫。
    pub candidate_keys: Vec<String>,
    pub columns: usize,
    pub candidate_kind: String,
    pub candidate_title: String,
    pub page: i64,
    pub pages: i64,
    pub highlight: i64,
    pub english: bool,
    pub english_autocomplete: bool,
    pub english_pending: bool,
    pub english_can_start: bool,
    /// 引擎記得的 Caps Lock 狀態（caps_lock，#173）；跟按鍵當下的鎖定狀態不同時外殼送 caps_lock。
    pub caps_lock: bool,
    /// 目前的英文是 Caps Lock 帶來的（caps_english，#173），關掉 Caps Lock 會回到注音。
    pub caps_english: bool,
    pub reconnected: bool,
    pub context_id: u64, // 本次提交可供外殼確認的代號；不是文件文字
    /// 核心這次請求的提示（例如「已加入詞庫：…」，#123）；顯示在候選窗上方，下一鍵或幾秒後消失。
    pub notices: Vec<String>,
    /// 讀音提示（#162）：中英混打時照字面顯示的數字按空白會組成的注音（20 → ㄉㄢ）；沒有時為空字串。
    pub reading_hint: String,
}

impl Snapshot {
    /// 要顯示的提示：多則時以「；」串成一行。
    pub fn notice(&self) -> String {
        self.notices.join("；")
    }

    pub fn text(&self) -> String {
        self.preedit.iter().map(|s| s.text.as_str()).collect()
    }

    /// Predict only keys the English word buffer will actually consume.
    pub fn english_wants_key(&self, key: &str) -> bool {
        if !self.english_autocomplete || !self.english {
            return false;
        }
        let letter = key.len() == 1 && key.as_bytes()[0].is_ascii_alphabetic();
        if self.english_pending {
            letter && self.text().len() < 48
                || matches!(key, "Backspace" | "Escape")
                || (!self.candidates.is_empty() && matches!(key, "Tab" | "Up" | "Down"))
        } else {
            self.english_can_start && letter
        }
    }

    pub fn composing(&self) -> bool {
        !self.preedit.is_empty() || !self.candidates.is_empty()
    }

    /// 組字區還在標示要加入詞庫的範圍（樣式 marked，#123）：這時的提示是標示說明，留到標示結束。
    pub fn marking(&self) -> bool {
        self.preedit.iter().any(|s| s.style == "marked")
    }
}

pub struct Session {
    client: Client,
    id: u64,
    generation: u32,
    english: bool,
    replay_english: bool,
    pub english_state: Snapshot,
    down: bool,      // 上次請求時 server 不可用；available() 會試著重連
    log: Vec<Value>, // 這次組字以來的請求（提交或放棄後清空）
    // 切到英文清空組字前的日誌與當時的模式；只給緊接著的放回切換接續（#90）。
    cleared: Option<(Vec<Value>, bool)>,
    toggled: bool, // 上一個操作是成功的切換；見 toggle()
    // 已確認前文：預設開啟，ZUIN_DOCUMENT_CONTEXT=0 時外殼完全不追蹤（使用者設定在引擎端）。
    context_enabled: bool,
    pending_context: Option<(u64, String)>, // 最近一次提交的代號與文字，等待外殼確認游標仍緊接其後
    // 欄位標 IS_PRIVATE（無痕／私密視窗）：每個請求帶 private，引擎不學習；不提供前文給外殼讀取。
    private: bool,
    // 引擎是否認得 #123 的新鍵名（open 回覆 info 的 vocab_keys，0.2.0 起的引擎回報 true）。
    // 跟著 session 快取，不每鍵查 health；設定套用時引擎重啟，重開 session 就會更新。
    vocab_keys: bool,
    // english 是否已是引擎那邊的模式（#160）：拿到過快照，或向引擎查過新 session 開始時的模式。新開的程式在這之前
    // 不知道開始時是不是英文，第一個按鍵前先查（learn_mode），攔不攔按鍵與中／英指示才會對。
    mode_known: bool,
    // 引擎回報「所有輸入框共用中英模式」（mode_memory=shared，#115）：換欄位後別的程式可能已經切換，重新查。
    shared: bool,
    // 引擎認得 caps_lock 請求而且「Caps Lock 開著時直接打英文」開著（health／open 的 caps_lock_english，#173）。
    // 跟著最近一次 health（learn_mode）或 open 快取：新開的程式第一個按鍵前就知道，換欄位（session 關掉）也沿用。
    caps_lock_english: bool,
    // 最近一次快照的 caps_english（#173），換欄位清掉 english_state 後仍保留：重開 session 還原模式時，
    // Caps Lock 帶來的英文以 caps_lock 還原（關掉 Caps Lock 才回得到注音），不當成使用者切的英文。
    caps_english: bool,
}

/// 重開 session（引擎重啟、斷線）時要不要送「設定模式」還原外殼記得的模式（#160）。新 session 從開始時的模式起：
/// 還不知道模式（新開的程式還沒問過）就照新 session 的；記得的模式跟開始時的不同才送。舊引擎沒有回報開始時的模式，
/// 當成注音，跟以前只在英文時還原相同。
fn restore_mode(known: bool, want_english: bool, start_english: bool) -> Option<bool> {
    (known && want_english != start_english).then_some(want_english)
}

/// health 回報的新 session 開始時的模式（#160 的 initial_mode；全部共用時是目前共用的模式）。舊引擎沒有這一項時為 None。
fn initial_english(info: &Value) -> Option<bool> {
    info["initial_mode"].as_str().map(|m| m == "english")
}

impl Session {
    pub fn new() -> Session {
        Session::with_client(Client::new())
    }

    pub fn with_client(client: Client) -> Session {
        Session {
            client,
            id: 0,
            generation: 0,
            english: false,
            replay_english: false,
            english_state: Snapshot::default(),
            down: false,
            log: Vec::new(),
            cleared: None,
            toggled: false,
            context_enabled: std::env::var("ZUIN_DOCUMENT_CONTEXT").as_deref() != Ok("0"),
            pending_context: None,
            private: false,
            vocab_keys: false,
            mode_known: false,
            shared: false,
            caps_lock_english: false,
            caps_english: false,
        }
    }

    /// 還不知道這個程式（TSF 執行緒）的中英模式時，向引擎查新 session 開始時的模式（#160）。
    /// spawn=false 只用已經在執行的引擎、查完不留連線（Activate 時決定中／英指示，不為此啟動引擎，
    /// 也不讓沒有打字的執行緒各自佔著一條連線）。回傳模式是否因此改變。
    pub fn learn_mode(&mut self, spawn: bool) -> bool {
        if self.mode_known {
            return false;
        }
        let probe = !spawn && !self.client.connected();
        if probe && !self.client.connect_existing() {
            return false;
        }
        let reply = self.client.call(&json!({"op": "health"}));
        if probe && self.id == 0 {
            self.client.release();
        }
        let Some(reply) = reply.filter(|r| r["ok"] == true) else {
            return false;
        };
        self.mode_known = true;
        self.shared = reply["info"]["mode_memory"] == "shared";
        self.caps_lock_english = reply["info"]["caps_lock_english"] == "true";
        let before = self.english;
        if let Some(english) = initial_english(&reply["info"]) {
            self.english = english;
            self.caps_english = false; // 模式照引擎的（共用模式或開始時的）；Caps Lock 由下一個按鍵重新同步（#173）
        }
        before != self.english
    }

    /// 目前的 session 是否可以送 #123 的新鍵名（Shift＋←／→、Shift＋Delete／Backspace）。
    /// 還沒開 session 或引擎不支援時為 false；沒在組字時這些鍵本來就交還應用程式，不需要查。
    pub fn vocab_keys(&self) -> bool {
        self.vocab_keys && self.id != 0 && self.generation == self.client.generation()
    }

    /// 每次按鍵前由外殼依欄位政策設定；組字中途變成私人時，引擎讓整段組字保持私人。
    pub fn set_private(&mut self, private: bool) {
        self.private = private;
        if private {
            self.pending_context = None;
        }
    }

    /// 開始新組字前由外殼呼叫：回傳剛提交、等待確認的文字（每次提交只提供一次）。
    pub fn take_pending_context(&mut self) -> Option<(u64, String)> {
        if self.private {
            self.pending_context = None;
        }
        self.pending_context.take()
    }

    /// 外殼核對游標前的文字後回報；相符才請引擎以該段提交作為下一段的前文。
    pub fn confirm_context(&mut self, id: u64, confirmed: bool) {
        if !confirmed || id == 0 || self.id == 0 || self.generation != self.client.generation() {
            return;
        }
        let _ = self.client.call(
            &json!({"op": "handle", "session": self.id, "req": {"type": "context", "on": true, "context_id": id}}),
        );
    }

    pub fn english(&self) -> bool {
        self.english
    }

    /// 按鍵當下的 Caps Lock 鎖定狀態要不要告訴引擎（#173）：引擎回報 caps_lock_english，而且跟引擎記得的不同。
    /// 換欄位後（english_state 已清掉）還記得 Caps Lock 帶來的英文時當成開著：在別處關掉 Caps Lock 回來，
    /// 第一個按鍵就回到注音。設定關閉或舊引擎時一律 false，注音模式照舊不理 Caps Lock（#168）。
    pub fn caps_needs_sync(&self, on: bool) -> bool {
        self.caps_lock_english && (self.english_state.caps_lock || self.caps_english) != on
    }

    /// Caps Lock 鍵本身（#173）：以 health 重新確認 caps_lock_english。剛在設定程式打開這一項時引擎重啟了，
    /// open／health 的快取還是舊的，不重新確認的話第一個字母會照注音打、下一個才切英文。舊連線這一次才發現斷了，
    /// 重連再問一次。沒有連線而且引擎沒在執行時不為此啟動，沿用快取。
    pub fn refresh_caps_lock_english(&mut self) {
        if !self.client.connected() && !self.client.connect_existing() {
            return;
        }
        let health = json!({"op": "health"});
        let reply = self.client.call(&health).or_else(|| self.client.call(&health));
        if let Some(reply) = reply.filter(|r| r["ok"] == true) {
            self.caps_lock_english = reply["info"]["caps_lock_english"] == "true";
        }
    }

    /// 回報 Caps Lock 狀態（#173）：開啟時引擎先送出組字、切到英文；關閉時回到原本的模式。
    pub fn caps_lock(&mut self, on: bool) -> Snapshot {
        self.handle(json!({"type": "caps_lock", "on": on}))
    }

    /// TestKeyDown 只查設定，不送按鍵：關閉捷徑或接舊引擎時，= 必須交還宿主。
    pub fn emoji_shortcut(&mut self) -> bool {
        self.client
            .call(&json!({"op": "health"}))
            .is_some_and(|reply| reply["ok"] == true && reply["info"]["emoji_shortcut"] == "equals")
    }

    /// 沒在組字時的聲調鍵是否交給引擎（#135：單按聲調鍵＋空白是聲調符號，數字先留在組字區）。
    /// 中英混打時照舊交還宿主；引擎不可用或舊版引擎沒有這一項時為 false，維持原本交還宿主的行為。
    pub fn tone_mark_keys(&mut self) -> bool {
        self.client
            .call(&json!({"op": "health"}))
            .is_some_and(|reply| reply["ok"] == true && reply["info"]["tone_mark_keys"] == "true")
    }

    /// 引擎是否認得 Ctrl＋標點的鍵名而且設定開著（health 的 ctrl_punctuation，#168）。只在按下那六個鍵時查詢，
    /// 設定套用（引擎重啟）後立即生效；引擎不可用或舊版引擎沒有這一項時為 false，照舊交給應用程式。
    pub fn ctrl_punctuation(&mut self) -> bool {
        self.client
            .call(&json!({"op": "health"}))
            .is_some_and(|reply| reply["ok"] == true && reply["info"]["ctrl_punctuation"] == "true")
    }

    /// 引擎目前載入的鍵盤排列是否為倚天 41 鍵（#83）。只在答案依排列而不同的鍵（keys::layout_dependent）查詢；
    /// 引擎不可用或舊版引擎沒有這一項時為標準排列。
    pub fn eten_layout(&mut self) -> bool {
        self.client
            .call(&json!({"op": "health"}))
            .is_some_and(|reply| reply["ok"] == true && reply["info"]["keyboard_layout"] == "eten")
    }

    /// 引擎目前載入的中英切換鍵設定（左右 Shift 與單按時限，#117）。在 Shift 單按放開時查詢，
    /// 設定套用（引擎重啟）後立即生效；引擎不可用或舊版引擎見 ShiftConfig::from_info。
    pub fn shift_config(&mut self) -> crate::keys::ShiftConfig {
        let reply = self.client.call(&json!({"op": "health"}));
        let info = reply
            .as_ref()
            .filter(|r| r["ok"] == true)
            .map(|r| &r["info"]);
        crate::keys::ShiftConfig::from_info(info)
    }

    /// server 是否可用。上次失敗後只做一次快速的連線嘗試（有速率限制，不會等啟動），
    /// 連得上才恢復吃鍵；連不上就讓宿主自己處理按鍵。
    pub fn available(&mut self) -> bool {
        if self.down && self.client.connect() {
            self.down = false;
        }
        !self.down
    }

    fn ensure_open(&mut self) -> bool {
        if self.id != 0 && self.generation == self.client.generation() {
            return true;
        }
        let reply = self
            .client
            .call(&json!({"op": "open", "version": PROTOCOL_VERSION}));
        let Some(reply) = reply else {
            self.id = 0;
            return false;
        };
        if !reply["ok"].as_bool().unwrap_or(false) {
            zlog!("open: {}", reply["error"].as_str().unwrap_or("?"));
            self.id = 0;
            return false;
        }
        self.id = reply["session"].as_u64().unwrap_or(0);
        self.generation = self.client.generation();
        self.vocab_keys = reply["info"]["vocab_keys"] == "true";
        self.shared = reply["info"]["mode_memory"] == "shared";
        self.caps_lock_english = reply["info"]["caps_lock_english"] == "true";
        let start_english = reply["info"]["start_mode"] == "english";
        self.pending_context = None; // 新 session 不認得舊代號
        if self.context_enabled {
            // 宣告外殼能確認文件位置；引擎依使用者設定決定是否採用。失敗只是不用前文。
            let _ = self.client.call(
                &json!({"op": "handle", "session": self.id, "req": {"type": "context", "context_enabled": true}}),
            );
        }
        if let Some(info) = reply["info"].as_object() {
            zlog!(
                "open: session={} server={} data={}",
                self.id,
                info.get("executable_sha256")
                    .and_then(|v| v.as_str())
                    .map(|s| &s[..s.len().min(12)])
                    .unwrap_or("?"),
                info.get("data_sha256")
                    .and_then(|v| v.as_str())
                    .map(|s| &s[..s.len().min(12)])
                    .unwrap_or("?")
            );
        }
        let want = if self.log.is_empty() {
            self.english
        } else {
            self.replay_english
        };
        // Caps Lock 帶來的英文（#173）以 caps_lock 還原：新 session 也記得是暫時的，關掉 Caps Lock 才回到注音。
        let restore = if want && self.caps_english && self.caps_lock_english && !start_english {
            Some(json!({"type": "caps_lock", "on": true}))
        } else {
            restore_mode(self.mode_known, want, start_english)
                .map(|english| json!({"type": "mode", "on": english}))
        };
        if let Some(req) = restore {
            let restored = self
                .client
                .call(&json!({"op": "handle", "session": self.id, "req": req}));
            if !restored
                .as_ref()
                .and_then(|r| r["ok"].as_bool())
                .unwrap_or(false)
            {
                self.id = 0;
                return false;
            }
        }
        // 重播：新 session 回到斷線前的組字狀態
        let log = self.log.clone();
        for req in &log {
            let r = self
                .client
                .call(&json!({"op": "handle", "session": self.id, "req": req}));
            if r.is_none() {
                self.id = 0;
                return false;
            }
        }
        true
    }

    /// 送出一個 proto.Request。連線中斷時自動重連、重開 session 並重播。
    pub fn handle(&mut self, mut request: Value) -> Snapshot {
        if self.private {
            request["private"] = Value::Bool(true); // 也記進重播紀錄，重連後仍是私人組字
        }
        let generation_before = self.client.generation();
        let english_before = self.english;
        let kind = request["type"].as_str().unwrap_or("").to_string();
        // 欄數不是使用者操作；其他請求都讓下一次切換不再還原注音（#90）。
        let mut cleared = None;
        if kind != "context" && kind != "display" {
            self.toggled = false;
            cleared = self.cleared.take();
        }
        let mut attempts = 0;
        loop {
            attempts += 1;
            if attempts > 3 {
                if !self.client.handoff_pending() {
                    break;
                }
                std::thread::sleep(std::time::Duration::from_millis(10));
            }
            if !self.client.connect() || !self.ensure_open() {
                continue;
            }
            if !self.vocab_keys {
                // 按鍵當下的判斷用的是舊 session；重連到不支援新鍵名的引擎時換回原本的鍵。
                downgrade_vocab_key(&mut request);
            }
            let reply = self
                .client
                .call(&json!({"op": "handle", "session": self.id, "req": request}));
            let Some(reply) = reply else { continue }; // 連線斷了：下一輪重連並重播
            if !reply["ok"].as_bool().unwrap_or(false) {
                zlog!("handle: {}", reply["error"].as_str().unwrap_or("?"));
                self.id = 0; // server 不認得這個 session（例如重啟過）：重開並重播
                continue;
            }
            let mut s = parse(&reply);
            s.reconnected = generation_before != self.client.generation();
            if !s.commit.is_empty() {
                self.pending_context =
                    (self.context_enabled && !self.private && s.context_id != 0 && !s.reconnected)
                        .then(|| (s.context_id, s.commit.clone()));
            } else if s.reconnected {
                self.pending_context = None;
            }
            self.english = s.english;
            self.caps_english = s.caps_english;
            self.mode_known = true;
            self.english_state = s.clone();
            self.down = false;
            if !s.commit.is_empty() || kind == "cancel" || !s.composing() {
                // #90：切到英文清掉未打完的注音而結束組字時留下這段日誌；立即切回放回注音時接上，重播才得到同樣的組字。
                if kind == "toggle" && s.commit.is_empty() && !self.log.is_empty() {
                    let mut log = std::mem::take(&mut self.log);
                    log.push(request);
                    self.cleared = Some((log, self.replay_english));
                }
                self.log.clear();
            } else if kind != "caps_lock" {
                // Caps Lock 狀態不重播（#173）：重開的 session 由還原模式與下一個按鍵重新同步。
                if self.log.is_empty() {
                    match cleared.take().filter(|_| kind == "toggle") {
                        Some((log, english)) => {
                            self.log = log;
                            self.replay_english = english;
                        }
                        None => self.replay_english = english_before,
                    }
                }
                self.log.push(request);
            }
            return s;
        }
        zlog!("handle: server 不可用，放行按鍵");
        self.client.abandon_handoff(); // 本次期限結束；下一次按鍵仍可正常恢復連線。
        self.down = true;
        Snapshot::default()
    }

    /// 清除重播紀錄；不重連、不重播，不讓舊組字流入私人欄位或下個欄位。
    pub fn reset(&mut self) {
        self.log.clear();
        self.cleared = None;
        self.toggled = false;
        self.english_state = Snapshot::default();
        self.pending_context = None;
        // 全部共用時別的程式可能已經切換：下一次重新查（learn_mode）。各程式各自記住時沿用這個程式的模式。
        if self.shared {
            self.mode_known = false;
        }
        if self.id != 0 && self.generation == self.client.generation() {
            let _ = self
                .client
                .call(&json!({"op": "close", "session": self.id}));
        }
        self.id = 0;
    }

    pub fn key(&mut self, key: &str) -> Snapshot {
        self.handle(json!({"type": "key", "key": key}))
    }

    /// 單按 Shift 或輸入指示器按鈕切換中英。上一次切換之後沒有任何按鍵或其他請求時帶 restore，
    /// 核心才放回切到英文時清掉的未打完注音（#90 防誤觸）。
    pub fn toggle(&mut self) -> Snapshot {
        let restore = self.toggled;
        let snapshot = self.handle(json!({"type": "toggle", "restore": restore}));
        self.toggled = snapshot.ok;
        snapshot
    }

    /// 沒有送給核心就交還宿主的按鍵（快捷鍵、閒置時的英文字母）同樣表示切換之後有操作。
    pub fn bypassed(&mut self) {
        self.toggled = false;
    }
}

impl Drop for Session {
    fn drop(&mut self) {
        if self.id != 0 && self.generation == self.client.generation() {
            let _ = self
                .client
                .call(&json!({"op": "close", "session": self.id}));
        }
    }
}

/// 把 #123 的新鍵名換回原本的鍵（引擎不支援時）；其他請求不變。
fn downgrade_vocab_key(request: &mut Value) {
    if request["type"] != "key" {
        return;
    }
    if let Some(base) = request["key"].as_str().and_then(crate::keys::vocab_base) {
        request["key"] = Value::from(base);
    }
}

fn parse(reply: &Value) -> Snapshot {
    let mut s = Snapshot::default();
    let Some(j) = reply.get("snapshot") else {
        return s;
    };
    s.ok = true;
    s.handled = j["handled"].as_bool().unwrap_or(false);
    s.commit = j["commit"].as_str().unwrap_or("").to_string();
    s.context_id = j["context_id"].as_u64().unwrap_or(0);
    s.suggestion = j["suggestion"].as_str().unwrap_or("").to_string();
    s.english = j["mode"].as_str() == Some("english");
    s.english_autocomplete = j["english_autocomplete"].as_bool().unwrap_or(false);
    s.english_pending = j["english_pending"].as_bool().unwrap_or(false);
    s.english_can_start = j["english_can_start"].as_bool().unwrap_or(false);
    s.caps_lock = j["caps_lock"].as_bool().unwrap_or(false);
    s.caps_english = j["caps_english"].as_bool().unwrap_or(false);
    s.reading_hint = j["reading_hint"]["text"].as_str().unwrap_or("").to_string();
    if let Some(notices) = j["notices"].as_array() {
        s.notices = notices
            .iter()
            .filter_map(|n| n.as_str())
            .filter(|n| !n.is_empty())
            .map(str::to_string)
            .collect();
    }
    if let Some(p) = j["preedit"].as_array() {
        for g in p {
            s.preedit.push(Segment {
                text: g["text"].as_str().unwrap_or("").to_string(),
                style: g["style"].as_str().unwrap_or("normal").to_string(),
            });
        }
    }
    s.cursor_rune = j["cursor"]["rune"].as_u64().unwrap_or(0) as usize;
    s.cursor_utf16 = j["cursor"]["utf16"].as_u64().unwrap_or(0) as usize;
    if let Some(c) = j.get("candidates") {
        s.columns = c["columns"].as_u64().unwrap_or(0).min(5) as usize;
        s.candidate_kind = c["kind"].as_str().unwrap_or("").to_string();
        s.candidate_title = c["title"].as_str().unwrap_or("").to_string();
        s.page = c["page"].as_i64().unwrap_or(0);
        s.pages = c["pages"].as_i64().unwrap_or(0);
        s.highlight = c["highlight"].as_i64().unwrap_or(0);
        if let Some(items) = c["items"].as_array() {
            for it in items {
                s.candidates.push((
                    it["id"].as_i64().unwrap_or(0),
                    it["text"].as_str().unwrap_or("").to_string(),
                ));
                s.candidate_notes
                    .push(it["note"].as_str().unwrap_or("").to_string());
                let index = s.candidate_keys.len();
                s.candidate_keys.push(match it["key"].as_str() {
                    Some(key) => key.to_string(),
                    None if s.english => String::new(),
                    None => ((index + 1) % 10).to_string(),
                });
            }
        }
    }
    s
}

#[cfg(test)]
mod parse_tests {
    use super::*;

    #[test]
    fn english_key_preflight_matches_buffer_boundaries() {
        let mut s = parse(
            &json!({"snapshot": {"mode": "english", "english_autocomplete": true,
            "english_pending": true, "preedit": [{"text":"hel"}],
            "candidates": {"items":[{"id":0,"text":"hello"}]}}}),
        );
        assert!(s.english_pending);
        for k in ["h", "H", "Backspace", "Escape", "Tab", "Up", "Down"] {
            assert!(s.english_wants_key(k), "{}", k);
        }
        for k in [
            "Enter", " ", ".", "1", "Left", "Right", "Delete", "Home", "End", "PageUp", "PageDown",
        ] {
            assert!(!s.english_wants_key(k), "must pass host action: {}", k);
        }
        s.candidates.clear();
        for k in ["Tab", "Up", "Down"] {
            assert!(!s.english_wants_key(k));
        }
        s.preedit[0].text = "a".repeat(48);
        assert!(
            !s.english_wants_key("a"),
            "word limit must pass the next letter"
        );
        let legacy = parse(&json!({"snapshot":{"mode":"english"}}));
        assert!(!legacy.english_pending && !legacy.english_wants_key("a"));
    }

    // #160：重開 session 時只在記得的模式跟新 session 開始時的模式不同才還原；還沒問過模式就照新 session 的。
    #[test]
    fn restore_mode_only_when_it_differs_from_the_start() {
        assert_eq!(restore_mode(true, true, false), Some(true), "舊引擎（開始時注音）：記得英文才還原，跟以前一樣");
        assert_eq!(restore_mode(true, false, false), None);
        assert_eq!(restore_mode(true, false, true), Some(false), "開始時英文：記得注音要還原，不然引擎重啟就變英文");
        assert_eq!(restore_mode(true, true, true), None);
        assert_eq!(restore_mode(false, false, true), None, "新開的程式還沒問過：照新 session 開始時的英文");
        assert_eq!(initial_english(&json!({"initial_mode": "english"})), Some(true));
        assert_eq!(initial_english(&json!({"initial_mode": "zhuyin"})), Some(false));
        assert_eq!(initial_english(&json!({"version": "old"})), None, "舊引擎沒有回報");
    }

    #[test]
    fn candidate_notes_line_up_with_candidates() {
        let reply: Value = serde_json::from_str(
            r#"{"snapshot":{"handled":true,"candidates":{"page":0,"pages":1,"highlight":0,
                "items":[{"id":0,"text":"吃"},{"id":1,"text":"痴","note":"近音 ㄔ"}]}}}"#,
        )
        .unwrap();
        let s = parse(&reply);
        assert_eq!(s.candidates.len(), 2);
        assert_eq!(
            s.candidate_notes,
            vec!["".to_string(), "近音 ㄔ".to_string()]
        );
    }

    // #112：鍵標照核心給的選字鍵；舊核心依位置 1–0（第 10 項是 0），英文模式的補字候選不畫。
    #[test]
    fn candidate_keys_follow_the_engine() {
        let s = parse(&json!({"snapshot": {"mode": "zhuyin", "candidates": {"items": [
            {"id": 0, "text": "是", "key": "1"}, {"id": 1, "text": "式", "key": "q"}, {"id": 2, "text": "示", "key": ";"}]}}}));
        assert_eq!(s.candidate_keys, vec!["1", "q", ";"]);
        let items: Vec<Value> = (0..10).map(|i| json!({"id": i, "text": "字"})).collect();
        let old = parse(&json!({"snapshot": {"mode": "zhuyin", "candidates": {"items": items}}}));
        assert_eq!(old.candidate_keys, vec!["1", "2", "3", "4", "5", "6", "7", "8", "9", "0"]);
        let english = parse(&json!({"snapshot": {"mode": "english", "candidates": {"items": [{"id": 0, "text": "hello"}]}}}));
        assert_eq!(english.candidate_keys, vec![""]);
    }

    #[test]
    fn notices_and_marked_style_are_parsed() {
        // #123：加詞的提示與標示樣式。
        let s = parse(&json!({"snapshot": {"handled": true,
            "preedit": [{"text": "我和", "style": "normal"}, {"text": "世傑", "style": "marked"}],
            "notices": ["已加入詞庫：世傑", "", 3]}}));
        assert_eq!(s.notices, vec!["已加入詞庫：世傑".to_string()]);
        assert_eq!(s.notice(), "已加入詞庫：世傑");
        assert_eq!(s.preedit[1].style, "marked");
        assert!(s.marking(), "組字區有 marked 分段就是標示中");
        let two = parse(&json!({"snapshot": {"notices": ["甲", "乙"]}}));
        assert_eq!(two.notice(), "甲；乙");
        let old = parse(&json!({"snapshot": {"handled": true}}));
        assert!(old.notices.is_empty() && old.notice().is_empty(), "舊引擎沒有 notices");
        assert!(!two.marking() && !old.marking(), "沒有 marked 分段不是標示中");
    }

    #[test]
    fn reading_hint_is_parsed() {
        // #162：中英混打打 20 時的讀音提示；舊引擎沒有這個欄位。
        let s = parse(&json!({"snapshot": {"handled": true,
            "preedit": [{"text": "20", "style": "reading"}],
            "reading_hint": {"start": 0, "length": 2, "text": "ㄉㄢ"}}}));
        assert_eq!(s.reading_hint, "ㄉㄢ");
        assert_eq!(s.preedit[0].style, "reading");
        let old = parse(&json!({"snapshot": {"handled": true}}));
        assert!(old.reading_hint.is_empty());
    }

    #[test]
    fn vocab_key_names_fall_back_for_unsupported_engines() {
        for (name, base) in [
            ("ShiftLeft", "Left"),
            ("ShiftRight", "Right"),
            ("ShiftDelete", "Delete"),
            ("ShiftBackspace", "Backspace"),
        ] {
            let mut r = json!({"type": "key", "key": name, "private": true});
            downgrade_vocab_key(&mut r);
            assert_eq!(r, json!({"type": "key", "key": base, "private": true}));
        }
        for mut r in [
            json!({"type": "key", "key": "Left"}),
            json!({"type": "key", "key": "a"}),
            json!({"type": "select", "key": "ShiftLeft", "index": 1}),
        ] {
            let before = r.clone();
            downgrade_vocab_key(&mut r);
            assert_eq!(r, before);
        }
    }
}

#[cfg(test)]
mod tests {
    //! 對真的 0c-server 做整合測試：先在另一個管道名稱啟動 server，
    //! 再以 ZUIN_TEST_PIPE 指定；沒有設定就略過（不讀日常個人詞庫）。
    use super::*;

    fn session() -> Option<Session> {
        let pipe = std::env::var("ZUIN_TEST_PIPE").ok()?;
        Some(Session::with_client(Client::with_pipe(&pipe)))
    }

    #[test]
    fn english_autocomplete_preserves_host_boundaries() {
        let Ok(pipe) = std::env::var("ZUIN_TEST_ENGLISH_PIPE") else {
            return;
        };
        let mut s = Session::with_client(Client::with_pipe(&pipe));
        let start = s.handle(json!({"type":"mode", "on":true}));
        assert!(start.ok && start.english_can_start);
        for boundary in ["Enter", " ", "Left", "Delete", "."] {
            for k in ["h", "e", "l"] {
                assert!(s.key(k).handled);
            }
            assert!(s.english_state.english_pending);
            assert!(
                !s.english_state.english_wants_key(boundary),
                "TestKeyDown must decline {}",
                boundary
            );
            let next = s.key(boundary);
            assert!(next.ok && !next.handled && !next.english_pending);
            assert_eq!(next.commit, "hel");
            s.key(" ");
        }
        for k in ["h", "e", "l"] {
            s.key(k);
        }
        let accepted = s.key("Tab");
        assert!(accepted.handled && accepted.text() != "hel");
        assert_eq!(s.key("Enter").commit, accepted.text());
    }

    #[test]
    fn types_and_commits() {
        let Some(mut s) = session() else {
            eprintln!("略過：沒有 ZUIN_TEST_PIPE");
            return;
        };
        let mut last = Snapshot::default();
        for k in ["s", "u", "3", "c", "l", "3"] {
            last = s.key(k);
            assert!(last.ok, "server 不可用");
            assert!(last.handled);
        }
        assert_eq!(last.text(), "你好");
        assert_eq!(last.cursor_utf16, 2);
        assert!(last.composing());
        let committed = s.key("Enter");
        assert_eq!(committed.commit, "你好");
        assert!(!committed.composing());
        assert!(s.log.is_empty(), "提交後重播紀錄要清空");
    }

    #[test]
    fn emoji_opens_without_preedit_and_double_equals_commits_once() {
        let Some(mut s) = session() else { return };
        assert!(s.emoji_shortcut());
        assert_eq!(s.id, 0, "查設定不能建立或改動組字");
        let palette = s.key("=");
        assert!(palette.ok && palette.handled && palette.composing());
        assert_eq!(palette.text(), "");
        assert_eq!(palette.candidate_kind, "emoji");
        assert!(!palette.candidates.is_empty());
        let literal = s.key("=");
        assert_eq!(literal.commit, "=");
        assert!(!literal.composing());
    }

    #[test]
    fn commit_offers_document_context_once() {
        let Some(mut s) = session() else { return };
        for k in ["s", "u", "3", "c", "l", "3"] {
            s.key(k);
        }
        let committed = s.key("Enter");
        assert_eq!(committed.commit, "你好");
        let (id, text) = s.take_pending_context().expect("提交後應提供待確認前文");
        assert!(id != 0);
        assert_eq!(text, "你好");
        assert!(s.take_pending_context().is_none(), "每次提交只提供一次");
        s.confirm_context(id, true);
        let next = s.key("s");
        assert!(next.ok && next.handled, "確認前文後仍可繼續輸入");
        s.reset();
        assert!(s.take_pending_context().is_none());
    }

    #[test]
    fn private_field_composes_without_offering_context() {
        // 無痕／私密視窗（IS_PRIVATE）照常組字，請求帶 private，提交後不提供前文。
        let Some(mut s) = session() else { return };
        s.set_private(true);
        for k in ["s", "u", "3", "c", "l", "3"] {
            assert!(s.key(k).handled);
        }
        assert!(s.log.iter().all(|r| r["private"] == true), "重播紀錄也要是私人組字");
        assert_eq!(s.key("Enter").commit, "你好");
        assert!(s.take_pending_context().is_none(), "私人欄位不讀前文");
        s.set_private(false);
        for k in ["s", "u", "3"] {
            s.key(k);
        }
        assert!(s.log.iter().all(|r| r.get("private").is_none()));
        s.reset();
    }

    // #160：引擎設成開始時英文（ZUIN_TEST_START_ENGLISH_PIPE 的 settings.json 有 start_mode=english、mode_memory=native）。
    // 新開的程式第一個按鍵前就知道是英文；切到注音後引擎重啟（斷線重開）仍是注音；換欄位（reset）維持這個程式的模式。
    #[test]
    fn start_english_is_known_before_the_first_key() {
        let Ok(pipe) = std::env::var("ZUIN_TEST_START_ENGLISH_PIPE") else {
            return;
        };
        let mut s = Session::with_client(Client::with_pipe(&pipe));
        assert!(!s.english());
        assert!(s.learn_mode(false) && s.english(), "Activate 時就知道開始時是英文");
        assert!(!s.client.connected(), "Activate 的查詢不留連線");
        assert!(!s.learn_mode(true), "問過就不再問");
        let snap = s.key("t");
        assert!(snap.ok && snap.english && !snap.handled, "第一個按鍵是英文，交還應用程式");
        let snap = s.toggle();
        assert!(snap.ok && !snap.english);
        s.client.disconnect_for_test();
        let snap = s.key("j");
        assert!(!snap.english && snap.handled, "重開 session 後仍是這個程式切過的注音");
        s.key("Escape");
        s.reset();
        assert!(!s.learn_mode(false) && !s.english(), "各程式各自記住：換欄位不重新問");
        let snap = s.key("j");
        assert!(!snap.english, "換欄位後仍是注音");
        s.key("Escape");
        let mut other = Session::with_client(Client::with_pipe(&pipe));
        assert!(other.learn_mode(true) && other.english(), "另一個新開的程式從英文開始");
    }

    // #173：引擎開著「Caps Lock 開著時直接打英文」（ZUIN_TEST_CAPS_PIPE 的 settings.json 有 caps_lock_english=true）。
    // 開 Caps Lock 先送出組字、切到英文；換欄位或引擎重啟後仍是暫時的英文，關掉 Caps Lock 回到注音。
    // 一般的 ZUIN_TEST_PIPE（設定關閉）不回報 caps_lock_english，外殼不送 caps_lock。
    #[test]
    fn caps_lock_english_survives_focus_and_reconnect() {
        if let Some(mut s) = session() {
            s.learn_mode(true);
            assert!(s.mode_known && !s.caps_needs_sync(true), "設定關閉時不送 caps_lock");
        }
        let Ok(pipe) = std::env::var("ZUIN_TEST_CAPS_PIPE") else {
            return;
        };
        let mut s = Session::with_client(Client::with_pipe(&pipe));
        s.learn_mode(true);
        assert!(s.mode_known && !s.english());
        assert!(s.caps_needs_sync(true) && !s.caps_needs_sync(false), "新開的程式第一個按鍵前就知道要同步");
        for k in ["s", "u", "3"] {
            assert!(s.key(k).handled);
        }
        let snap = s.caps_lock(true);
        assert!(snap.ok && snap.commit == "你" && snap.english && snap.caps_lock && snap.caps_english);
        assert!(!s.caps_needs_sync(true));
        let snap = s.key("A");
        assert!(snap.ok && !snap.handled, "暫時英文的 A 交給應用程式");
        // 換欄位：session 關掉；Caps Lock 還開著時不必重送，引擎重開 session 時以 caps_lock 還原。
        s.reset();
        assert!(s.english() && !s.caps_needs_sync(true) && s.caps_needs_sync(false));
        // 在別的程式關掉了 Caps Lock：回來第一個按鍵就回到注音。
        let snap = s.caps_lock(false);
        assert!(snap.ok && !snap.english && !snap.caps_english, "關 Caps Lock 應回到注音：{snap:?}");
        // 引擎重啟（斷線）時 Caps Lock 帶來的英文照樣是暫時的。
        s.caps_lock(true);
        s.client.disconnect_for_test();
        let snap = s.caps_lock(false);
        assert!(snap.ok && !snap.english, "重開 session 後關 Caps Lock 也回到注音：{snap:?}");
        // Shift 切到英文後開關 Caps Lock：維持英文。
        s.toggle();
        s.caps_lock(true);
        let snap = s.caps_lock(false);
        assert!(snap.ok && snap.english, "Shift 切的英文不受 Caps Lock 影響");
        s.toggle();
    }

    #[test]
    fn mode_and_mixed_composition_survive_reconnect() {
        let Some(mut s) = session() else { return };
        s.key("s");
        s.key("u");
        s.key("3");
        s.handle(json!({"type":"toggle"}));
        s.key("a");
        s.client.disconnect_for_test();
        let snap = s.key("b");
        assert!(snap.english);
        assert_eq!(snap.text(), "你ab");
        s.key("Enter");
        s.client.disconnect_for_test();
        let snap = s.key("x");
        assert!(snap.english);
        assert!(!snap.handled);
        s.reset();
        assert!(s.log.is_empty());
        let snap = s.handle(json!({"type":"mode", "on":false}));
        assert!(snap.text().is_empty());
        assert!(!snap.english);
    }

    #[test]
    fn immediate_toggle_back_restores_pending_zhuyin() {
        let Some(mut s) = session() else { return };
        s.key("j");
        let english = s.toggle();
        assert!(english.english && !english.composing(), "切到英文清掉未打完的注音（#90）");
        let back = s.toggle();
        assert!(!back.english);
        assert_eq!(back.text(), "ㄨ", "立即切回放回注音");
        s.client.disconnect_for_test();
        let replayed = s.key("i");
        assert!(!replayed.english);
        assert_eq!(replayed.text(), "ㄨㄛ", "斷線重播後仍是放回的注音");
        s.toggle();
        s.bypassed();
        let snap = s.toggle();
        assert!(!snap.english && !snap.composing(), "切換之間有交還宿主的按鍵就不放回");
        s.reset();
    }

    #[test]
    fn reset_discards_pending_composition_even_after_disconnect() {
        let Some(mut s) = session() else { return };
        for k in ["s", "u", "3"] {
            s.key(k);
        }
        s.client.disconnect_for_test();
        s.reset();
        let snap = s.key("c");
        assert_eq!(snap.text(), "ㄏ");
        assert!(snap.commit.is_empty());
    }

    #[test]
    fn passes_through_when_not_handled() {
        let Some(mut s) = session() else { return };
        let snap = s.key("Enter"); // 空的組字區：Enter 交還應用程式
        assert!(snap.ok);
        assert!(!snap.handled);
        assert!(!snap.composing());
    }

    #[test]
    fn replays_after_reconnect() {
        let Some(mut s) = session() else { return };
        for k in ["s", "u", "3"] {
            s.key(k);
        }
        assert_eq!(s.log.len(), 3);
        // 模擬斷線：丟掉連線後下一鍵應重開 session 並重播，組字不遺失
        s.client.disconnect_for_test();
        let snap = s.key("c");
        assert!(snap.ok);
        assert!(snap.reconnected);
        assert_eq!(snap.text(), "你ㄏ");
        let _ = s.handle(json!({"type": "cancel"}));
    }

    // Native named-pipe fixture: the closing writer explicitly refuses all
    // requests, then its replacement accepts one key. No desktop registration,
    // daily dictionary, or external server is needed for these regressions.
    struct HandoffServer {
        name: String,
        stop: std::sync::Arc<std::sync::atomic::AtomicBool>,
        worker: Option<std::thread::JoinHandle<usize>>,
    }

    impl HandoffServer {
        fn new(error: &'static str, reject_for: Option<std::time::Duration>) -> Self {
            use std::io::{Read, Write};
            use std::os::windows::io::FromRawHandle;
            use std::sync::atomic::Ordering;
            use windows::Win32::Foundation::{GetLastError, ERROR_PIPE_CONNECTED};
            use windows::Win32::Storage::FileSystem::PIPE_ACCESS_DUPLEX;
            use windows::Win32::System::Pipes::{
                ConnectNamedPipe, CreateNamedPipeW, PIPE_READMODE_BYTE, PIPE_TYPE_BYTE, PIPE_WAIT,
            };
            use windows_core::PCWSTR;
            let name = format!(
                r"\\.\pipe\0c-tsf-handoff-{}-{}",
                std::process::id(),
                std::time::SystemTime::now()
                    .duration_since(std::time::UNIX_EPOCH)
                    .unwrap()
                    .as_nanos()
            );
            let stop = std::sync::Arc::new(std::sync::atomic::AtomicBool::new(false));
            let stopping = stop.clone();
            let pipe_name = zuin_win_common::wide(&name);
            let (ready, wait) = std::sync::mpsc::channel();
            let worker = std::thread::spawn(move || {
                let until = reject_for.map(|d| std::time::Instant::now() + d);
                let mut ready = Some(ready);
                let mut keys = 0;
                while !stopping.load(Ordering::SeqCst) {
                    let handle = unsafe {
                        CreateNamedPipeW(
                            PCWSTR::from_raw(pipe_name.as_ptr()),
                            PIPE_ACCESS_DUPLEX,
                            PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_WAIT,
                            1,
                            65536,
                            65536,
                            0,
                            None,
                        )
                    };
                    assert!(!handle.is_invalid());
                    let mut file = unsafe { std::fs::File::from_raw_handle(handle.0) };
                    if let Some(r) = ready.take() {
                        r.send(()).unwrap();
                    }
                    let connected = unsafe { ConnectNamedPipe(handle, None) };
                    assert!(connected.is_ok() || unsafe { GetLastError() } == ERROR_PIPE_CONNECTED);
                    if stopping.load(Ordering::SeqCst) {
                        break;
                    }
                    loop {
                        let mut header = [0; 4];
                        if file.read_exact(&mut header).is_err() {
                            break;
                        }
                        let mut body = vec![0; u32::from_le_bytes(header) as usize];
                        file.read_exact(&mut body).unwrap();
                        let request: Value = serde_json::from_slice(&body).unwrap();
                        let refusing = until.is_none_or(|t| std::time::Instant::now() < t);
                        let reply = if refusing {
                            json!({"ok": false, "error": error})
                        } else if request["op"] == "open" {
                            json!({"ok": true, "session": 1})
                        } else if request["op"] == "handle" && request["req"]["type"] == "key" {
                            keys += 1;
                            json!({"ok": true, "snapshot": {"handled": true, "commit": "你好"}})
                        } else {
                            json!({"ok": true})
                        };
                        if file
                            .write_all(&zuin_win_common::frame(&reply).unwrap())
                            .is_err()
                        {
                            break;
                        }
                    }
                }
                keys
            });
            wait.recv_timeout(std::time::Duration::from_secs(2))
                .unwrap();
            Self {
                name,
                stop,
                worker: Some(worker),
            }
        }

        fn finish(mut self) -> usize {
            use std::sync::atomic::Ordering;
            self.stop.store(true, Ordering::SeqCst);
            let worker = self.worker.take().unwrap();
            // Wake a blocking accept after the test session has closed its pipe.
            while !worker.is_finished() {
                if let Ok(wake) = std::fs::OpenOptions::new()
                    .read(true)
                    .write(true)
                    .open(&self.name)
                {
                    drop(wake);
                    break;
                }
                std::thread::sleep(std::time::Duration::from_millis(2));
            }
            worker.join().unwrap()
        }
    }

    #[test]
    fn closing_writer_waits_for_replacement_and_commits_once() {
        let server =
            HandoffServer::new("引擎正在切換", Some(std::time::Duration::from_millis(200)));
        let mut s = Session::with_client(Client::with_pipe(&server.name));
        let snap = s.key("Enter");
        assert!(snap.ok && snap.handled, "handoff must retain the key");
        assert_eq!(snap.commit, "你好");
        drop(s);
        assert_eq!(server.finish(), 1, "replacement must receive the key once");
    }

    #[test]
    fn closing_writer_has_a_fixed_deadline() {
        let server = HandoffServer::new("引擎正在切換", None);
        let mut s = Session::with_client(Client::with_pipe(&server.name));
        let start = std::time::Instant::now();
        assert!(!s.key("Enter").ok);
        assert!(
            start.elapsed() < std::time::Duration::from_millis(2500),
            "refusals must not extend the deadline"
        );
        drop(s);
        assert_eq!(server.finish(), 0);
    }

    /// 依 open 回覆的 info 決定 vocab_keys 的管道伺服器；記下收到的按鍵。
    fn vocab_server(vocab_keys: Option<&'static str>) -> (String, std::sync::Arc<std::sync::Mutex<Vec<String>>>) {
        use std::io::{Read, Write};
        use std::os::windows::io::FromRawHandle;
        use windows::Win32::Storage::FileSystem::PIPE_ACCESS_DUPLEX;
        use windows::Win32::System::Pipes::{
            ConnectNamedPipe, CreateNamedPipeW, PIPE_READMODE_BYTE, PIPE_TYPE_BYTE, PIPE_WAIT,
        };
        use windows_core::PCWSTR;
        let name = format!(
            r"\\.\pipe\0c-tsf-vocab-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        );
        let keys = std::sync::Arc::new(std::sync::Mutex::new(Vec::new()));
        let seen = keys.clone();
        let pipe_name = zuin_win_common::wide(&name);
        let (ready, wait) = std::sync::mpsc::channel();
        std::thread::spawn(move || {
            let handle = unsafe {
                CreateNamedPipeW(
                    PCWSTR::from_raw(pipe_name.as_ptr()),
                    PIPE_ACCESS_DUPLEX,
                    PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_WAIT,
                    1,
                    65536,
                    65536,
                    0,
                    None,
                )
            };
            let mut file = unsafe { std::fs::File::from_raw_handle(handle.0) };
            ready.send(()).unwrap();
            let _ = unsafe { ConnectNamedPipe(handle, None) };
            loop {
                let mut header = [0; 4];
                if file.read_exact(&mut header).is_err() {
                    break;
                }
                let mut body = vec![0; u32::from_le_bytes(header) as usize];
                file.read_exact(&mut body).unwrap();
                let request: Value = serde_json::from_slice(&body).unwrap();
                let reply = if request["op"] == "open" {
                    let mut info = json!({"data_sha256": "x"});
                    if let Some(v) = vocab_keys {
                        info["vocab_keys"] = Value::from(v);
                    }
                    json!({"ok": true, "session": 7, "info": info})
                } else if request["op"] == "handle" && request["req"]["type"] == "key" {
                    seen.lock().unwrap().push(request["req"]["key"].as_str().unwrap_or("").to_string());
                    json!({"ok": true, "snapshot": {"handled": true, "preedit": [{"text": "我", "style": "normal"}],
                        "notices": ["已加入詞庫：世傑"]}})
                } else {
                    json!({"ok": true})
                };
                if file.write_all(&zuin_win_common::frame(&reply).unwrap()).is_err() {
                    break;
                }
            }
        });
        wait.recv_timeout(std::time::Duration::from_secs(2)).unwrap();
        (name, keys)
    }

    #[test]
    fn vocab_keys_follow_the_open_reply() {
        // 支援的引擎：open 之後才知道，新鍵名原樣送出；提示一併解析。
        let (name, keys) = vocab_server(Some("true"));
        let mut s = Session::with_client(Client::with_pipe(&name));
        assert!(!s.vocab_keys(), "還沒開 session 不能送新鍵名");
        let first = s.key("j");
        assert!(first.ok && s.vocab_keys());
        assert_eq!(first.notices, vec!["已加入詞庫：世傑".to_string()]);
        s.key("ShiftLeft");
        assert_eq!(*keys.lock().unwrap(), vec!["j", "ShiftLeft"]);
        // 開關關閉（false）與舊引擎（沒有這一項）：不送新鍵名，萬一送到也換回原本的鍵。
        for v in [Some("false"), None] {
            let (name, keys) = vocab_server(v);
            let mut s = Session::with_client(Client::with_pipe(&name));
            s.key("j");
            assert!(!s.vocab_keys(), "{v:?}");
            s.key("ShiftLeft");
            s.key("ShiftBackspace");
            assert_eq!(*keys.lock().unwrap(), vec!["j", "Left", "Backspace"], "{v:?}");
            assert!(s.log.iter().all(|r| r["key"] != "ShiftLeft"), "重播紀錄也是原本的鍵");
        }
    }

    #[test]
    fn unrelated_rejections_do_not_wait_for_handoff() {
        let server = HandoffServer::new("協定版本不符", None);
        let mut s = Session::with_client(Client::with_pipe(&server.name));
        let start = std::time::Instant::now();
        assert!(!s.key("Enter").ok);
        assert!(start.elapsed() < std::time::Duration::from_millis(500));
        drop(s);
        assert_eq!(server.finish(), 0);
    }
}
