//! 一個輸入欄位在 0c-server 上的 session：請求、重連後的重播、快照的解讀。
//! 與 Fcitx 5 外殼的 session.cpp 對應；不依賴 TSF，可單獨測試。
use std::time::{Duration, Instant};

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
    pub full_width: bool,
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
    /// 送出的文字目前會轉成簡體（output_simplified，#169）。只有回報 output_simplified_live 的引擎才有這一項。
    pub output_simplified: bool,
}

impl Snapshot {
    /// 要顯示的提示：多則時以「；」串成一行。
    pub fn notice(&self) -> String {
        self.notices.join("；")
    }

    pub fn text(&self) -> String {
        self.preedit.iter().map(|s| s.text.as_str()).collect()
    }

    /// 字寬是每個 session 的狀態；這些字元在半形可直接交給宿主，不能只看靜態 idle_keys。
    pub fn full_width_extra_key(&self, key: &str) -> bool {
        self.full_width && key.len() == 1 && (key.as_bytes()[0].is_ascii_uppercase() || " \\'\"<>?".contains(key))
    }

    /// Predict only keys the English word buffer will actually consume.
    pub fn english_wants_key(&self, key: &str) -> bool {
        if self.english && self.full_width && (key.len() == 1 && key.as_bytes()[0].is_ascii_graphic() || key == " ") {
            return true;
        }
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
    // caps_lock_english 是在哪一代連線上問到的（#173）。設定一改，0c-update 就請引擎存檔退出、再以新設定啟動，
    // 舊連線跟著斷；連線還在而且是同一代時快取一定是新的，Caps Lock 鍵不必再問 health（refresh_caps_lock_english）。
    caps_generation: u32,
    // 最近一次快照的 caps_english（#173），換欄位清掉 english_state 後仍保留：重開 session 還原模式時，
    // Caps Lock 帶來的英文以 caps_lock 還原（關掉 Caps Lock 才回得到注音），不當成使用者切的英文。
    caps_english: bool,
    // 「輸出簡體字」（#169）：引擎能即時切換（health／open 的 output_simplified_live），以及引擎目前的值
    // （health／open 的 output_simplified、之後每個快照的 output_simplified）。輸入指示器的「简」與選單打勾看這兩項；
    // 跟著 session 物件（這個程式的 TSF 執行緒），換欄位也沿用。
    simplified_live: bool,
    simplified: bool,
    // 上一次從引擎得知「輸出簡體字」的時間（health／open／快照）；換欄位時剛問過就不再問（refresh_simplified_after）。
    simplified_at: Option<Instant>,
    // 沒在組字時哪些鍵交給引擎（health 的 info，見 idle_key_from，#183）與它是在哪一代連線上問到的。設定一改引擎就重啟、
    // 舊連線跟著斷，所以同一代、連線還在時沿用，不每按一個鍵就問一次 health（同 caps_generation）。
    idle_info: Option<Value>,
    idle_generation: u32,
}

/// 重開 session（引擎重啟、斷線）時要不要送「設定模式」還原外殼記得的模式（#160）。新 session 從開始時的模式起：
/// 還不知道模式（新開的程式還沒問過）就照新 session 的；記得的模式跟開始時的不同才送。舊引擎沒有回報開始時的模式，
/// 當成注音，跟以前只在英文時還原相同。
fn restore_mode(known: bool, want_english: bool, start_english: bool) -> Option<bool> {
    (known && want_english != start_english).then_some(want_english)
}

/// 重開 session 時依序送出的還原請求。Caps Lock 帶來的英文（#173，caps_english）底下是注音：模式照注音還原
/// （開始時英文就先設回注音），設定還開著時再以 caps_lock 還原暫時的英文，新 session 也記得是暫時的，關掉 Caps Lock
/// 才回到注音。設定剛關掉（引擎以新設定重啟）時就是注音。不這樣的話，暫時的英文會變成這個程式自己的英文。
fn restore_requests(
    known: bool,
    want_english: bool,
    caps_english: bool,
    caps_lock_english: bool,
    start_english: bool,
) -> Vec<Value> {
    let caps = want_english && caps_english;
    let mut out: Vec<Value> = restore_mode(known, want_english && !caps, start_english)
        .map(|english| json!({"type": "mode", "on": english}))
        .into_iter()
        .collect();
    if caps && caps_lock_english {
        out.push(json!({"type": "caps_lock", "on": true}));
    }
    out
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
            caps_generation: 0,
            caps_english: false,
            simplified_live: false,
            simplified: false,
            simplified_at: None,
            idle_info: None,
            idle_generation: 0,
        }
    }

    /// 從 health／open 的 info 記下「輸出簡體字」的能力與目前的值；回傳狀態是否因此改變。
    fn learn_simplified(&mut self, info: &Value) -> bool {
        let before = (self.simplified_live, self.simplified);
        self.simplified_live = info["output_simplified_live"] == "true";
        self.simplified = self.simplified_live && info["output_simplified"] == "true";
        self.simplified_at = Some(Instant::now());
        before != (self.simplified_live, self.simplified)
    }

    /// 「輸出簡體字」目前的值；引擎不能即時切換（舊引擎）或還沒問過時為 None，選單改看 0c-update 的狀態。
    pub fn simplified(&self) -> Option<bool> {
        self.simplified_live.then_some(self.simplified)
    }

    /// 換欄位時重新確認「輸出簡體字」（#169）：可能剛在設定程式或別的程式切換過，輸入指示器的「简」要跟上。
    /// 只用已經在執行的引擎、不為此啟動，沒有 session 時查完不留連線（同 learn_mode）。回傳狀態是否改變。
    pub fn refresh_simplified(&mut self) -> bool {
        let probe = !self.client.connected();
        if probe && !self.client.connect_existing() {
            return false;
        }
        let reply = self.client.call(&json!({"op": "health"}));
        if probe && self.id == 0 {
            self.client.release();
        }
        match reply.filter(|r| r["ok"] == true) {
            Some(reply) => self.learn_simplified(&reply["info"]),
            None => false,
        }
    }

    /// 換欄位用的 refresh_simplified：fresh 之內已經從引擎得知（剛打過字、剛換過欄位）就不再問，回傳 false。
    /// 換欄位在宿主的 UI 執行緒上，一次點擊常連著好幾次焦點變化；同一段時間裡值不會變，不必每次都多一次同步 IPC。
    pub fn refresh_simplified_after(&mut self, fresh: Duration) -> bool {
        if self.simplified_at.is_some_and(|at| at.elapsed() < fresh) {
            return false;
        }
        self.refresh_simplified()
    }

    /// 切換「輸出簡體字」（#169）：引擎立即改變之後送出的文字（所有程式，包括已經在組字區的字）並寫進設定檔，
    /// 組字不動；快照帶「已切換為輸出簡體字／繁體字」的提示。這是全域設定，不記進重播紀錄。
    /// 引擎不能即時切換時回 handled=false（呼叫端改請 0c-ui 執行 0c-update）。
    pub fn output_simplified(&mut self, on: bool) -> Snapshot {
        if !self.simplified_live {
            return Snapshot::default();
        }
        self.handle(json!({"type": "output_simplified", "on": on}))
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
        let generation = self.client.generation();
        if probe && self.id == 0 {
            self.client.release();
        }
        let Some(reply) = reply.filter(|r| r["ok"] == true) else {
            return false;
        };
        self.mode_known = true;
        self.shared = reply["info"]["mode_memory"] == "shared";
        self.caps_lock_english = reply["info"]["caps_lock_english"] == "true";
        self.caps_generation = generation;
        let simplified = self.learn_simplified(&reply["info"]);
        let before = self.english;
        if let Some(english) = initial_english(&reply["info"]) {
            self.english = english;
            self.caps_english = false; // 模式照引擎的（共用模式或開始時的）；Caps Lock 由下一個按鍵重新同步（#173）
        }
        before != self.english || simplified
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

    /// Caps Lock 鍵本身（#173）：確認 caps_lock_english 的快取還是新的。剛在設定程式打開這一項時引擎重啟了，
    /// open／health 的快取是舊的，不重新確認的話第一個字母會照注音打、下一個才切英文。設定改了一定會重啟引擎，
    /// 所以連線還在（不送請求，只看管道另一端關了沒有）而且快取是這一代連線上問到的就沿用，不在 UI 執行緒上問引擎；
    /// 連線斷了或還沒問過才以 health 重新確認。沒有連線而且引擎沒在執行時不為此啟動，沿用快取；
    /// 沒有 session 時查完不留連線（同 learn_mode），只按過 Caps Lock、沒打過字的程式不佔著一條連線。
    pub fn refresh_caps_lock_english(&mut self) {
        if self.client.connected() && self.caps_generation == self.client.generation() && self.client.alive() {
            return;
        }
        let probe = !self.client.connected(); // alive() 發現斷線時已經關掉，這裡重連
        if probe && !self.client.connect_existing() {
            return;
        }
        let reply = self.client.call(&json!({"op": "health"}));
        let generation = self.client.generation();
        if probe && self.id == 0 {
            self.client.release();
        }
        if let Some(reply) = reply.filter(|r| r["ok"] == true) {
            self.caps_lock_english = reply["info"]["caps_lock_english"] == "true";
            self.caps_generation = generation;
        }
    }

    /// 回報 Caps Lock 狀態（#173）：開啟時引擎先送出組字、切到英文；關閉時回到原本的模式。
    pub fn caps_lock(&mut self, on: bool) -> Snapshot {
        self.handle(json!({"type": "caps_lock", "on": on}))
    }

    /// 沒在組字時這個鍵要不要交給引擎（見 idle_key_from）。TestKeyDown 只查設定，不送按鍵：答錯時 WPF 會掉鍵，
    /// 或吃掉應用程式的鍵（關閉 Emoji 捷徑時的 =、許氏的數字與 q）。所有可見字元依排列及風格查詢；
    /// health 的答案跟著連線快取（設定一改引擎就重啟、連線跟著斷），連線還在時不再往返。
    /// 問不到（引擎正在交接、剛重啟還沒起來、連線錯誤）時不交還宿主：OnKeyDown 的 handle() 會等交接結束再送，
    /// 這裡說不吃的字母卻直接以英文打進文件（改設定後的第一個字）。沿用上一次問到的答案；從沒問到過就照沒有設定資訊的
    /// 舊推算（字母交給引擎，同 0.2.4 的本地判斷）。不記代數，下一個鍵重新問。英文模式、Ctrl／Alt 組合不會走到這裡（wants_key）。
    pub fn style_shift_space(&mut self) -> bool {
        let _ = self.idle_key("");
        matches!(self.idle_info.as_ref().and_then(|v| v["input_style_shift_space"].as_str()), Some("fullwidth" | "space" | "single"))
    }

    pub fn style_caps_lower(&mut self) -> bool {
        let _ = self.idle_key("");
        self.idle_info.as_ref().is_some_and(|v| v["input_style_caps_letter"] == "lower")
    }

    pub fn idle_key(&mut self, key: &str) -> bool {
        let fresh = self.idle_info.is_some()
            && self.client.connected()
            && self.idle_generation == self.client.generation()
            && self.client.alive();
        if !fresh {
            match self.client.call(&json!({"op": "health"})).filter(|r| r["ok"] == true) {
                Some(reply) => {
                    self.idle_info = Some(reply["info"].clone());
                    self.idle_generation = self.client.generation();
                }
                None => return idle_key_from(self.idle_info.as_ref().unwrap_or(&Value::Null), key),
            }
        }
        self.idle_info.as_ref().is_some_and(|info| idle_key_from(info, key))
    }

    /// 「用快捷鍵切換簡體字」開著、引擎也能即時切換（health 的 simplified_hotkey 與 output_simplified_live，#192）。
    /// 只在按下 Ctrl＋Shift＋F11 時查詢（同 ctrl_punctuation），設定套用（引擎重啟）後第一次按就照新設定；
    /// 順便記下引擎目前的「輸出簡體字」，切換的方向才對。引擎不可用或舊版引擎沒有這一項時為 false，照舊交給應用程式。
    pub fn simplified_hotkey(&mut self) -> bool {
        let Some(reply) = self.client.call(&json!({"op": "health"})).filter(|r| r["ok"] == true) else {
            return false;
        };
        self.learn_simplified(&reply["info"]);
        self.simplified_live && reply["info"]["simplified_hotkey"] == "true"
    }

    /// 引擎是否認得 Ctrl＋標點的鍵名而且設定開著（health 的 ctrl_punctuation，#168）。只在按下那六個鍵時查詢，
    /// 設定套用（引擎重啟）後立即生效；引擎不可用或舊版引擎沒有這一項時為 false，照舊交給應用程式。
    pub fn ctrl_punctuation(&mut self) -> bool {
        self.client
            .call(&json!({"op": "health"}))
            .is_some_and(|reply| reply["ok"] == true && reply["info"]["ctrl_punctuation"] == "true")
    }

    /// 引擎目前載入的中英切換鍵設定（左右 Shift 與單按時限，#117）。在 Shift 單按放開時查詢，
    /// 設定套用（引擎重啟）後立即生效；引擎不可用或舊版引擎見 ShiftConfig::from_info。
    pub fn shift_config(&mut self) -> crate::keys::ShiftConfig {
        // A retired pipe must reconnect before the first Shift tap asks for
        // capabilities; otherwise call() fails once and uses legacy defaults.
        if self.client.connected() { self.client.alive(); }
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
        self.caps_generation = self.generation;
        self.learn_simplified(&reply["info"]);
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
        for req in restore_requests(
            self.mode_known,
            want,
            self.caps_english,
            self.caps_lock_english,
            start_english,
        ) {
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
        // 引擎正在重啟時等到第一次看到的期限（#216）：交接、改設定後退出、當掉都一樣。宿主的 UI 執行緒等著，
        // 之後的按鍵在訊息佇列裡依序排隊，新引擎起來後照順序送到，不會亂序或重複。持續斷線不延長。
        let mut wait_until: Option<Instant> = None;
        loop {
            attempts += 1;
            if attempts > 3 {
                wait_until = wait_until.or(self.client.restart_deadline());
                if !wait_until.is_some_and(|until| Instant::now() < until) {
                    break;
                }
                std::thread::sleep(Duration::from_millis(20));
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
            if self.simplified_live {
                self.simplified = s.output_simplified; // 設定程式或別的程式切換後，下一個快照就跟上（#169）
                self.simplified_at = Some(Instant::now());
            }
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
            } else if kind != "caps_lock" && kind != "output_simplified" {
                // Caps Lock 狀態不重播（#173）：重開的 session 由還原模式與下一個按鍵重新同步。
                // 「輸出簡體字」是全域設定，重播會再切一次（#169）。
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
        self.client.abandon_restart(); // 本次期限結束；下一次按鍵仍可正常恢復連線。
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
    s.full_width = j["full_width"].as_bool().unwrap_or(false);
    s.english_autocomplete = j["english_autocomplete"].as_bool().unwrap_or(false);
    s.english_pending = j["english_pending"].as_bool().unwrap_or(false);
    s.english_can_start = j["english_can_start"].as_bool().unwrap_or(false);
    s.caps_lock = j["caps_lock"].as_bool().unwrap_or(false);
    s.caps_english = j["caps_english"].as_bool().unwrap_or(false);
    s.reading_hint = j["reading_hint"]["text"].as_str().unwrap_or("").to_string();
    s.output_simplified = j["output_simplified"].as_bool().unwrap_or(false);
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
        s.columns = c["columns"].as_u64().unwrap_or(0).min(10) as usize;
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
    fn chinese_full_width_preflight_covers_literal_keys_without_claiming_tones() {
        let snap = parse(&json!({"snapshot":{"mode":"zhuyin","full_width":true}}));
        for key in ["A", "Z", " ", "'", "\"", "\\", "<", ">", "?"] { assert!(snap.full_width_extra_key(key), "{key}"); }
        for key in ["3", "a", "Tab", "ShiftSpace"] { assert!(!snap.full_width_extra_key(key), "{key}"); }
        assert!(!Snapshot::default().full_width_extra_key("\\"));
    }

    #[test]
    fn full_width_english_preflight_consumes_printable_keys_without_completion() {
        let snap = parse(&json!({"snapshot":{"mode":"english","full_width":true}}));
        for key in ["a", "A", "0", "9", " ", "'", "\\"] { assert!(snap.english_wants_key(key), "{key}"); }
        for key in ["Tab", "Escape", "Left", "KP1"] { assert!(!snap.english_wants_key(key), "{key}"); }
        assert!(!parse(&json!({"snapshot":{"mode":"english"}})).english_wants_key("a"));
    }

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

    // #173：Caps Lock 帶來的英文重開 session 時以 caps_lock 還原；設定關掉或開始時英文都不能變成這個程式自己的英文。
    #[test]
    fn caps_english_is_restored_as_temporary() {
        let caps_on = json!({"type": "caps_lock", "on": true});
        let zhuyin = json!({"type": "mode", "on": false});
        let english = json!({"type": "mode", "on": true});
        assert_eq!(restore_requests(true, true, true, true, false), vec![caps_on.clone()], "跟以前一樣");
        assert_eq!(
            restore_requests(true, true, true, true, true),
            vec![zhuyin.clone(), caps_on.clone()],
            "開始時英文：先設回注音，Caps Lock 的英文才是暫時的"
        );
        assert_eq!(restore_requests(true, true, true, false, false), Vec::<Value>::new(), "設定剛關掉：照注音開始");
        assert_eq!(restore_requests(true, true, true, false, true), vec![zhuyin.clone()], "設定剛關掉、開始時英文：設回注音");
        assert_eq!(restore_requests(true, true, false, true, false), vec![english.clone()], "Shift 切的英文照舊還原");
        assert_eq!(restore_requests(true, false, false, true, true), vec![zhuyin.clone()]);
        assert_eq!(restore_requests(false, true, true, true, false), vec![caps_on], "還沒問過模式：仍還原 Caps Lock");
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

/// 沒在組字時的聲調鍵 3 4 6 7 要不要交給引擎，依 health 的 info：
/// - 沒開中英混打（tone_mark_keys）：引擎先把數字留在組字區，緊接空白換成聲調符號（#135）。
/// - 中英混打（mixed_input）：引擎把數字照字面留在組字區，跟 Linux／macOS 一樣，後面的 54 才知道前面是數字、
///   354 整串留數字（#175）；以前交還宿主時「3」直接打進文件，54 就組成「至」。
/// 引擎不可用或舊版引擎兩項都沒有時為 false，維持原本交還宿主的行為。
fn idle_tone_keys(info: &Value) -> bool {
    info["tone_mark_keys"] == "true" || info["mixed_input"] == "true"
}

/// 沒在組字時這個鍵要不要交給引擎，依 health 的 info：
/// - 引擎回報 idle_keys（#183 起）：照它。答案依鍵盤排列與設定而不同（許氏的數字、- / 不是注音鍵、交還宿主），
///   由引擎算（core/session.IdleKeys，測試逐鍵比對實際處理結果），外殼不必為每種排列各寫一份按鍵表。
/// - 舊引擎：依 keyboard_layout（標準或倚天，#83）、emoji_shortcut 與聲調鍵設定（idle_tone_keys）推算。
fn idle_key_from(info: &Value, key: &str) -> bool {
    if let Some(keys) = info["idle_keys"].as_str() {
        return key.chars().count() == 1 && keys.contains(key);
    }
    use crate::keys::{starts_composition, tone_key, Layout};
    let layout = if info["keyboard_layout"] == "eten" { Layout::ETen } else { Layout::Standard };
    if key == "=" && layout == Layout::Standard {
        info["emoji_shortcut"] == "equals"
    } else if tone_key(layout, key) {
        idle_tone_keys(info)
    } else {
        starts_composition(layout, key)
    }
}

#[cfg(test)]
mod idle_tone_key_tests {
    use super::*;

    #[test]
    fn idle_tone_keys_go_to_the_engine_in_mixed_input_too() {
        assert!(idle_tone_keys(&json!({"tone_mark_keys": "true", "mixed_input": "false"})), "#135");
        assert!(idle_tone_keys(&json!({"tone_mark_keys": "false", "mixed_input": "true"})), "#175：354");
        assert!(!idle_tone_keys(&json!({"tone_mark_keys": "false", "mixed_input": "false"})));
        assert!(!idle_tone_keys(&json!({})), "舊引擎");
    }

    #[test]
    fn idle_keys_follow_the_engine_report() {
        // #183 許氏：數字與 - / 交還宿主，字母、, . ; = 交給引擎。
        let hsu = json!({"idle_keys": "!\"&'(),.:;<=>?[\\]^_`abcdefghijklmnoprstuvwxyz{}~", "keyboard_layout": "hsu", "mixed_input": "true", "tone_mark_keys": "true"});
        for key in ["1", "3", "4", "6", "7", "0", "-", "/", "q"] {
            assert!(!idle_key_from(&hsu, key), "{key}");
        }
        for key in ["=", "a", ","] {
            assert!(idle_key_from(&hsu, key), "{key}");
        }
        // 舊引擎沒有 idle_keys：照原本的推算。
        let standard = json!({"keyboard_layout": "standard", "emoji_shortcut": "equals", "tone_mark_keys": "true"});
        for key in ["1", "2", "5", "3", "7", "=", "-", "/"] {
            assert!(idle_key_from(&standard, key), "{key}");
        }
        let eten = json!({"keyboard_layout": "eten", "emoji_shortcut": "equals", "tone_mark_keys": "true"});
        for key in ["1", "2", "7", "="] {
            assert!(idle_key_from(&eten, key), "{key}");
        }
        for key in ["5", "6"] {
            assert!(!idle_key_from(&eten, key), "{key}");
        }
        assert!(!idle_key_from(&json!({"emoji_shortcut": "off"}), "="));
        assert!(!idle_key_from(&json!({}), "3"), "舊引擎沒有聲調設定：交還宿主");
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
        assert!(s.idle_key("="));
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

    // #169：選單切換「輸出簡體字」即時生效（ZUIN_TEST_SIMPLIFIED_PIPE：testdata/toy、暫存個人資料的獨立引擎；
    // 這個測試會改它的設定，不和其他測試共用引擎）。另一個程式已經在組字區的字照新的設定送出；切換不記進重播紀錄。
    #[test]
    fn output_simplified_switches_live_and_is_not_replayed() {
        let Ok(pipe) = std::env::var("ZUIN_TEST_SIMPLIFIED_PIPE") else {
            return;
        };
        let mut menu = Session::with_client(Client::with_pipe(&pipe));
        let mut editor = Session::with_client(Client::with_pipe(&pipe));
        assert_eq!(menu.simplified(), None, "還沒問過引擎");
        assert!(menu.refresh_simplified(), "health 回報能即時切換");
        assert_eq!(menu.simplified(), Some(false));
        assert!(!menu.client.connected(), "換欄位的查詢不留連線");
        let scenery = ["z", "/", " ", "r", "u", "/", "3"]; // ㄈㄥ ㄐㄧㄥˇ
        for k in scenery {
            editor.key(k);
        }
        assert_eq!(editor.english_state.text(), "風景");
        let snap = menu.output_simplified(true);
        assert!(snap.ok && snap.handled && snap.output_simplified, "{:?}", snap);
        assert_eq!(snap.notice(), "已切換為輸出簡體字");
        assert_eq!(menu.simplified(), Some(true));
        // 換欄位的確認：剛從快照得知就不問（一次點擊連著好幾次焦點變化）；過了時效才問，指示器跟上別處的切換。
        assert!(!editor.refresh_simplified_after(Duration::from_secs(60)));
        assert_eq!(editor.simplified(), Some(false), "時效內沿用上一個快照");
        assert!(editor.refresh_simplified_after(Duration::ZERO), "過了時效重新問引擎");
        assert_eq!(editor.simplified(), Some(true));
        let sent = editor.key("Enter");
        assert_eq!(sent.commit, "风景", "組字中切換後照新設定送出");
        assert_eq!(editor.simplified(), Some(true), "下一個快照就跟上");
        for k in scenery {
            menu.key(k);
        }
        let snap = menu.output_simplified(false);
        assert_eq!(snap.text(), "風景", "切換不動組字");
        assert!(menu.log.iter().all(|r| r["type"] != "output_simplified"), "不記進重播紀錄");
        menu.client.disconnect_for_test();
        assert_eq!(menu.key("Enter").commit, "風景", "重連重播後送出，不會再切一次");
        assert_eq!(menu.simplified(), Some(false));
    }

    // #192：一般的 ZUIN_TEST_PIPE（設定關閉）不回報 simplified_hotkey，Ctrl＋Shift＋F11 照舊交給應用程式；
    // ZUIN_TEST_HOTKEY_PIPE（暫存個人資料、settings.json 有 simplified_hotkey=true 的獨立引擎；這個測試會改它的設定）
    // 回報開著，按下時送選單同一個請求：組字不動，之後送出簡體。
    #[test]
    fn simplified_hotkey_follows_the_setting() {
        if let Some(mut s) = session() {
            assert!(!s.simplified_hotkey(), "設定關閉時不攔");
        }
        let Ok(pipe) = std::env::var("ZUIN_TEST_HOTKEY_PIPE") else {
            return;
        };
        let mut s = Session::with_client(Client::with_pipe(&pipe));
        assert!(s.simplified_hotkey(), "設定開著");
        assert_eq!(s.simplified(), Some(false), "順便知道目前是繁體");
        for k in ["z", "/", " ", "r", "u", "/", "3"] {
            s.key(k);
        }
        let snap = s.output_simplified(!s.simplified().unwrap());
        assert!(snap.ok && snap.handled && snap.text() == "風景", "{:?}", snap);
        assert_eq!(snap.notice(), "已切換為輸出簡體字");
        assert_eq!(s.key("Enter").commit, "风景");
        let back = s.output_simplified(!s.simplified().unwrap());
        assert!(back.handled && s.simplified() == Some(false), "再按一次切回繁體");
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

    // #173 code review：引擎重啟後重開 session 時，Caps Lock 帶來的英文不能變成這個程式自己的英文。
    // (a) Caps Lock 英文中在設定程式關掉這一項：以 ZUIN_TEST_PIPE（設定關閉）的引擎當作重啟後的新引擎。
    // (b) ZUIN_TEST_CAPS_START_ENGLISH_PIPE：start_mode=english、caps_lock_english=true、mode_memory=native。
    #[test]
    fn caps_english_stays_temporary_after_engine_restart() {
        if let (Ok(caps), Ok(plain)) = (std::env::var("ZUIN_TEST_CAPS_PIPE"), std::env::var("ZUIN_TEST_PIPE")) {
            let mut s = Session::with_client(Client::with_pipe(&caps));
            s.learn_mode(true);
            assert!(s.caps_lock(true).caps_english);
            s.client = Client::with_pipe(&plain); // 引擎以新設定（關閉）重啟
            s.id = 0;
            let snap = s.key("j");
            assert!(snap.ok && !snap.english && snap.handled, "設定關掉後照注音：{snap:?}");
            assert_eq!(snap.text(), "ㄨ");
            s.key("Escape");
        }
        let Ok(pipe) = std::env::var("ZUIN_TEST_CAPS_START_ENGLISH_PIPE") else {
            return;
        };
        let mut s = Session::with_client(Client::with_pipe(&pipe));
        s.learn_mode(true);
        assert!(s.english(), "開始時英文");
        assert!(!s.toggle().english, "這個程式切到注音");
        let snap = s.caps_lock(true);
        assert!(snap.english && snap.caps_english);
        s.client.disconnect_for_test();
        let snap = s.key("A");
        assert!(snap.ok && snap.english && snap.caps_english && !snap.handled, "重開後仍是暫時的英文：{snap:?}");
        let snap = s.caps_lock(false);
        assert!(snap.ok && !snap.english, "關掉 Caps Lock 回到這個程式的注音：{snap:?}");
        // 開始時的英文（不是 Caps Lock 帶來的）照舊：開關 Caps Lock 都是英文。
        assert!(s.toggle().english);
        s.caps_lock(true);
        s.client.disconnect_for_test();
        assert!(s.key("A").english);
        assert!(s.caps_lock(false).english, "使用者的英文不受 Caps Lock 影響");
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

    // #183 code review：改設定讓引擎重啟時，交接中的第一個字母 TestKeyDown 問不到 idle_keys。不能交還宿主（中文模式直接打出
    // 英文字母）：沿用上一次的答案，從沒問到過就照舊推算；OnKeyDown 的 handle() 等交接結束，新引擎收到這個鍵一次。
    #[test]
    fn idle_letters_during_handoff_go_to_the_engine() {
        let server =
            HandoffServer::new("引擎正在切換", Some(std::time::Duration::from_millis(500)));
        let mut s = Session::with_client(Client::with_pipe(&server.name));
        assert!(s.idle_key("a"), "從沒問到過：字母交給引擎（同 0.2.4）");
        assert!(s.client.restart_pending(), "引擎回了「引擎正在切換」");
        // 上一次在舊連線上問到的答案（許氏：數字交還宿主、字母交給引擎）照樣適用，不因交接把數字也吃掉。
        s.idle_info = Some(json!({"idle_keys": "!\"&'(),.:;<=>?[\\]^_`abcdefghijklmnoprstuvwxyz{}~"}));
        assert!(s.idle_key("a") && s.idle_key("j"));
        assert!(!s.idle_key("1") && !s.idle_key("q"));
        let snap = s.key("a");
        assert!(snap.ok && snap.handled, "OnKeyDown 等交接結束才送");
        drop(s);
        assert_eq!(server.finish(), 1, "新引擎收到這個鍵一次");
    }

    #[test]
    fn closing_writer_has_a_fixed_deadline() {
        let server = HandoffServer::new("引擎正在切換", None);
        let mut s = Session::with_client(Client::with_pipe(&server.name));
        let start = std::time::Instant::now();
        assert!(!s.key("Enter").ok);
        assert!(
            start.elapsed() < crate::ipc::RESTART_WAIT + std::time::Duration::from_millis(1500),
            "refusals must not extend the deadline"
        );
        drop(s);
        assert_eq!(server.finish(), 0);
    }

    /// #216 的假引擎：模擬改設定套用、更新交接或當掉後的重啟。第一個引擎回覆 `old_keys` 個按鍵後關掉連線與管道（退出），
    /// 管道消失 `gap` 之後新引擎才建立管道（None：永遠不回來）。`hung`：第一個引擎回覆 `old_keys` 個按鍵後不退出，
    /// 之後收下請求卻永遠不回覆（卡住，不是重啟）。
    /// 每個引擎收到的按鍵依序記下（引擎代號, 按鍵）。
    struct RestartServer {
        name: String,
        keys: std::sync::Arc<std::sync::Mutex<Vec<(u32, String)>>>,
        stop: std::sync::Arc<std::sync::atomic::AtomicBool>,
        gone: std::sync::mpsc::Receiver<()>,
    }

    impl RestartServer {
        fn new(old_keys: usize, gap: Option<Duration>, hung: bool) -> Self {
            use std::io::{Read, Write};
            use std::os::windows::io::FromRawHandle;
            use std::sync::atomic::{AtomicBool, Ordering};
            use std::sync::{Arc, Mutex};
            use windows::Win32::Storage::FileSystem::PIPE_ACCESS_DUPLEX;
            use windows::Win32::System::Pipes::{
                ConnectNamedPipe, CreateNamedPipeW, PIPE_READMODE_BYTE, PIPE_TYPE_BYTE, PIPE_WAIT,
            };
            use windows_core::PCWSTR;
            let name = format!(
                r"\\.\pipe\0c-tsf-restart-{}-{}",
                std::process::id(),
                std::time::SystemTime::now()
                    .duration_since(std::time::UNIX_EPOCH)
                    .unwrap()
                    .as_nanos()
            );
            let keys = Arc::new(Mutex::new(Vec::new()));
            let stop = Arc::new(AtomicBool::new(false));
            let (seen, stopping) = (keys.clone(), stop.clone());
            let pipe_name = zuin_win_common::wide(&name);
            let (ready, wait) = std::sync::mpsc::channel();
            let (gone_tx, gone) = std::sync::mpsc::channel();
            std::thread::spawn(move || {
                // 一個引擎：一次一條連線，連線斷了再開下一個實例；回覆滿 limit 個按鍵就退出（關掉連線與管道）。
                let serve = |engine: u32, limit: Option<usize>, ready: Option<std::sync::mpsc::Sender<()>>| {
                    let mut ready = ready;
                    let mut served = 0;
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
                        let _ = unsafe { ConnectNamedPipe(handle, None) };
                        if stopping.load(Ordering::SeqCst) {
                            return;
                        }
                        loop {
                            let mut header = [0; 4];
                            if file.read_exact(&mut header).is_err() {
                                break;
                            }
                            let mut body = vec![0; u32::from_le_bytes(header) as usize];
                            if file.read_exact(&mut body).is_err() {
                                break;
                            }
                            let request: Value = serde_json::from_slice(&body).unwrap();
                            if hung && limit.is_some_and(|n| served >= n) {
                                continue; // 卡住：收下請求、永遠不回覆
                            }
                            let reply = if request["op"] == "open" {
                                json!({"ok": true, "session": engine, "info": {}})
                            } else if request["op"] == "handle" && request["req"]["type"] == "key" {
                                let key = request["req"]["key"].as_str().unwrap_or("").to_string();
                                seen.lock().unwrap().push((engine, key.clone()));
                                served += 1;
                                json!({"ok": true, "snapshot": {"handled": true, "commit": key}})
                            } else {
                                json!({"ok": true, "info": {}})
                            };
                            if file.write_all(&zuin_win_common::frame(&reply).unwrap()).is_err() {
                                break;
                            }
                            if !hung && limit.is_some_and(|n| served >= n) {
                                return; // 退出：drop(file) 關掉連線，管道跟著消失
                            }
                        }
                    }
                };
                serve(1, Some(old_keys), Some(ready));
                if hung {
                    return;
                }
                let _ = gone_tx.send(());
                let Some(gap) = gap else { return };
                std::thread::sleep(gap);
                serve(2, None, None);
            });
            wait.recv_timeout(Duration::from_secs(2)).unwrap();
            Self { name, keys, stop, gone }
        }

        /// 等第一個引擎退出（回覆完最後一個按鍵、關掉管道）。
        fn wait_gone(&self) {
            self.gone.recv_timeout(Duration::from_secs(2)).unwrap();
        }

        fn keys(&self) -> Vec<(u32, String)> {
            self.keys.lock().unwrap().clone()
        }
    }

    impl Drop for RestartServer {
        fn drop(&mut self) {
            self.stop.store(true, std::sync::atomic::Ordering::SeqCst);
            // 叫醒等著連線的實例。
            let _ = std::fs::OpenOptions::new().read(true).write(true).open(&self.name);
        }
    }

    fn keyed(list: &[(u32, &str)]) -> Vec<(u32, String)> {
        list.iter().map(|(e, k)| (*e, k.to_string())).collect()
    }

    // #216：改設定套用後引擎重啟（舊引擎關掉連線退出、新引擎約 1 秒後才開始監聽），這時打的字不能不見。
    // 修正前 handle() 試 3 次就放行（距上次啟動不到 10 秒時連等都不等），TestKeyDown 已吃下的字母就消失了。
    #[test]
    fn keys_typed_while_the_engine_restarts_reach_the_new_engine_in_order() {
        let gap = Duration::from_millis(800);
        let server = RestartServer::new(1, Some(gap), false);
        let mut s = Session::with_client(Client::with_pipe(&server.name));
        assert!(s.key("a").handled);
        server.wait_gone();
        // TestKeyDown 的查詢不等新引擎（不卡宿主的這一次呼叫），字母照舊交給引擎。
        let start = Instant::now();
        assert!(s.idle_key("g"), "交接中的字母交給引擎");
        assert!(start.elapsed() < Duration::from_millis(300), "TestKeyDown 不等：{:?}", start.elapsed());
        assert!(s.client.restart_pending(), "舊引擎關掉連線：等新引擎");
        // OnKeyDown：等到新引擎起來才送；之後的按鍵（宿主的訊息佇列裡依序排著）照順序送到新引擎。
        let first = s.key("g");
        assert!(first.ok && first.handled, "第一個字母要等到新引擎");
        assert!(start.elapsed() >= gap - Duration::from_millis(100), "{:?}", start.elapsed());
        assert!(first.reconnected);
        for k in ["4", "s", "u", "3"] {
            assert!(s.key(k).handled, "{k}");
        }
        assert!(!s.client.restart_pending(), "接上新引擎就不再等");
        assert_eq!(
            server.keys(),
            keyed(&[(1, "a"), (2, "g"), (2, "4"), (2, "s"), (2, "u"), (2, "3")]),
            "依序、沒有遺失、沒有重複"
        );
    }

    // #216：更新交接剛好在兩個詞之間、使用者還在連續打字：舊引擎退出前的鍵在舊引擎，之後的都到新引擎。
    #[test]
    fn typing_through_a_handover_between_words_loses_nothing() {
        let server = RestartServer::new(3, Some(Duration::from_millis(400)), false);
        let mut s = Session::with_client(Client::with_pipe(&server.name));
        for k in ["s", "u", "3", "c", "l", "3"] {
            assert!(s.key(k).ok, "{k}");
        }
        assert_eq!(
            server.keys(),
            keyed(&[(1, "s"), (1, "u"), (1, "3"), (2, "c"), (2, "l"), (2, "3")])
        );
    }

    // #216：組字中（還沒送出）引擎重啟：新引擎起來後重播組字一次，接著的鍵照順序到。
    #[test]
    fn composition_survives_the_restart_and_is_replayed_once() {
        let server = RestartServer::new(1, Some(Duration::from_millis(500)), false);
        let mut s = Session::with_client(Client::with_pipe(&server.name));
        // 假引擎回 commit；這裡把 "a" 記成組字中的請求，模擬打到一半。
        assert!(s.key("a").handled);
        s.log.push(json!({"type": "key", "key": "a"}));
        server.wait_gone();
        assert!(s.key("b").handled);
        assert_eq!(server.keys(), keyed(&[(1, "a"), (2, "a"), (2, "b")]), "新引擎先重播一次組字，再收到這一鍵");
    }

    // #216：等到期限新引擎仍沒起來（例如啟動失敗）：這一鍵放行（外殼改以原字元送進文件），之後的鍵立刻放行，不每鍵都等。
    #[test]
    fn engine_that_never_returns_releases_within_the_deadline() {
        let server = RestartServer::new(1, None, false);
        let mut s = Session::with_client(Client::with_pipe(&server.name));
        assert!(s.key("a").handled);
        server.wait_gone();
        let start = Instant::now();
        assert!(!s.key("g").ok);
        let waited = start.elapsed();
        assert!(
            waited >= crate::ipc::RESTART_WAIT - Duration::from_millis(200)
                && waited < crate::ipc::RESTART_WAIT + Duration::from_millis(1500),
            "等到期限為止：{waited:?}"
        );
        let start = Instant::now();
        assert!(!s.key("4").ok);
        assert!(!s.available());
        assert!(start.elapsed() < Duration::from_millis(500), "之後不再等：{:?}", start.elapsed());
        assert_eq!(server.keys(), keyed(&[(1, "a")]));
    }

    // #216：引擎卡住（連線還在、只是不回覆）不是重啟：照舊每次 I/O 逾時、試 3 次就放行，不另外等到重啟期限。
    #[test]
    fn a_hung_engine_is_not_a_restart() {
        let server = RestartServer::new(1, None, true);
        let mut s = Session::with_client(Client::with_pipe(&server.name));
        assert!(s.key("a").handled);
        let start = Instant::now();
        assert!(!s.key("g").ok);
        // 記成重啟的話會等到 RESTART_WAIT（5 秒）才放行。
        assert!(start.elapsed() < Duration::from_millis(4500), "逾時不記成重啟：{:?}", start.elapsed());
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

    /// #173 的假引擎：health／open 回報 caps_lock_english，記下 health 次數與 handle 請求。`test_restart` 模擬套用設定時
    /// 引擎存檔退出、以新設定再起來：回覆讀完後關掉這條連線、caps_lock_english 換成另一個值（start_mode 不變），
    /// 下一條連線是「新的引擎」。永遠有一個管道在等連線。
    struct CapsServer {
        name: String,
        health: std::sync::Arc<std::sync::atomic::AtomicUsize>,
        requests: std::sync::Arc<std::sync::Mutex<Vec<Value>>>,
        restarted: std::sync::mpsc::Receiver<()>,
    }

    impl CapsServer {
        fn new(caps: bool, start_english: bool) -> Self {
            use std::io::{Read, Write};
            use std::os::windows::io::FromRawHandle;
            use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
            use std::sync::{Arc, Mutex};
            use windows::Win32::Storage::FileSystem::PIPE_ACCESS_DUPLEX;
            use windows::Win32::System::Pipes::{
                ConnectNamedPipe, CreateNamedPipeW, PIPE_READMODE_BYTE, PIPE_TYPE_BYTE, PIPE_UNLIMITED_INSTANCES,
                PIPE_WAIT,
            };
            use windows_core::PCWSTR;
            let name = format!(
                r"\\.\pipe\0c-tsf-caps-{}-{}",
                std::process::id(),
                std::time::SystemTime::now()
                    .duration_since(std::time::UNIX_EPOCH)
                    .unwrap()
                    .as_nanos()
            );
            let health = Arc::new(AtomicUsize::new(0));
            let requests = Arc::new(Mutex::new(Vec::new()));
            let caps = Arc::new(AtomicBool::new(caps));
            let (restart_tx, restarted) = std::sync::mpsc::channel();
            let (ready, wait) = std::sync::mpsc::channel();
            let pipe_name = zuin_win_common::wide(&name);
            let (h, r) = (health.clone(), requests.clone());
            std::thread::spawn(move || {
                let create = || unsafe {
                    CreateNamedPipeW(
                        PCWSTR::from_raw(pipe_name.as_ptr()),
                        PIPE_ACCESS_DUPLEX,
                        PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_WAIT,
                        PIPE_UNLIMITED_INSTANCES,
                        65536,
                        65536,
                        0,
                        None,
                    )
                };
                let mut next = create();
                ready.send(()).unwrap();
                loop {
                    let handle = next;
                    let _ = unsafe { ConnectNamedPipe(handle, None) };
                    next = create(); // 先開好下一個，客戶端重連時一定有管道
                    let (health, requests, caps, restart_tx) = (h.clone(), r.clone(), caps.clone(), restart_tx.clone());
                    let mut file = unsafe { std::fs::File::from_raw_handle(handle.0) };
                    std::thread::spawn(move || {
                        loop {
                            let mut header = [0; 4];
                            if file.read_exact(&mut header).is_err() {
                                return;
                            }
                            let mut body = vec![0; u32::from_le_bytes(header) as usize];
                            file.read_exact(&mut body).unwrap();
                            let request: Value = serde_json::from_slice(&body).unwrap();
                            let info = json!({
                                "caps_lock_english": caps.load(Ordering::SeqCst).to_string(),
                                "left_shift": caps.load(Ordering::SeqCst).to_string(),
                                "right_shift": caps.load(Ordering::SeqCst).to_string(),
                                "input_style_shift_space": if caps.load(Ordering::SeqCst) { "fullwidth" } else { "disabled" },
                                "input_style_caps_letter": if caps.load(Ordering::SeqCst) { "lower" } else { "platform" },
                                "start_mode": if start_english { "english" } else { "zhuyin" },
                                "initial_mode": if start_english { "english" } else { "zhuyin" },
                                "mode_memory": "native",
                            });
                            let reply = match request["op"].as_str() {
                                Some("health") => {
                                    health.fetch_add(1, Ordering::SeqCst);
                                    json!({"ok": true, "info": info})
                                }
                                Some("open") => json!({"ok": true, "session": 1, "info": info}),
                                Some("handle") => {
                                    let req = request["req"].clone();
                                    requests.lock().unwrap().push(req.clone());
                                    let caps_on = req["type"] == "caps_lock" && req["on"] == true;
                                    json!({"ok": true, "snapshot": {"handled": req["type"] != "key",
                                        "mode": if caps_on { "english" } else { "zhuyin" },
                                        "caps_lock": caps_on, "caps_english": caps_on}})
                                }
                                _ => json!({"ok": true}),
                            };
                            if file.write_all(&zuin_win_common::frame(&reply).unwrap()).is_err() {
                                return;
                            }
                            if request["op"] == "test_restart" {
                                let _ = file.sync_all(); // 等客戶端讀完回覆
                                drop(file);
                                caps.store(!caps.load(Ordering::SeqCst), Ordering::SeqCst);
                                restart_tx.send(()).unwrap();
                                return;
                            }
                        }
                    });
                }
            });
            wait.recv_timeout(std::time::Duration::from_secs(2)).unwrap();
            CapsServer {
                name,
                health,
                requests,
                restarted,
            }
        }

        fn health(&self) -> usize {
            self.health.load(std::sync::atomic::Ordering::SeqCst)
        }

        fn restart(&self, s: &mut Session) {
            let restart = json!({"op": "test_restart"});
            // 上一次重啟後這條連線已經斷了：第一次呼叫才發現，重連再送。
            assert!(s.client.call(&restart).or_else(|| s.client.call(&restart)).is_some());
            self.restarted.recv_timeout(std::time::Duration::from_secs(2)).unwrap();
        }
    }

    #[test]
    fn first_style_modifier_after_handoff_uses_replacement_capabilities() {
        for first in ["shift", "space", "caps"] {
            let server = CapsServer::new(false, false);
            let mut s = Session::with_client(Client::with_pipe(&server.name));
            assert!(!s.style_shift_space() && !s.style_caps_lower());
            assert!(!s.shift_config().toggles(crate::keys::Side::Left));
            for enabled in [true, false] {
                server.restart(&mut s);
                let actual = match first {
                    "shift" => s.shift_config().toggles(crate::keys::Side::Left),
                    "space" => s.style_shift_space(),
                    _ => s.style_caps_lower(),
                };
                assert_eq!(actual, enabled, "first {first} after handoff");
            }
        }
    }

    // #173 code review：Caps Lock 鍵本身確認 caps_lock_english 時，沒有 session 的程式查完不留連線；連線還在、
    // 快取是這條連線上問到的就不問引擎；引擎以新設定重啟（舊連線斷了）照樣在第一次 Caps Lock 就生效。
    #[test]
    fn caps_lock_key_checks_the_setting_without_holding_or_repeating() {
        let server = CapsServer::new(false, false);
        let mut s = Session::with_client(Client::with_pipe(&server.name));
        s.refresh_caps_lock_english();
        assert_eq!(server.health(), 1);
        assert!(!s.client.connected(), "只按過 Caps Lock、沒打過字的程式不留連線");
        assert!(!s.caps_needs_sync(true), "設定關閉");
        s.refresh_caps_lock_english();
        assert_eq!(server.health(), 2, "沒有連線時只能再問一次");
        assert!(!s.client.connected());

        // 打過字（有 session）：open 回覆的值就是這條連線上的，Caps Lock 鍵不再問 health。
        s.key("j");
        let before = server.health();
        for _ in 0..3 {
            s.refresh_caps_lock_english();
        }
        assert_eq!(server.health(), before, "連線還在時沿用 open 的值");
        assert!(s.client.connected());

        // 在設定程式打開這一項：引擎存檔退出、以新設定再起來。第一次 Caps Lock 就知道要切英文。
        server.restart(&mut s);
        s.refresh_caps_lock_english();
        assert_eq!(server.health(), before + 1, "舊連線斷了才重新確認");
        assert!(s.caps_needs_sync(true), "設定剛打開：這次 Caps Lock 就送 caps_lock");
        s.refresh_caps_lock_english();
        assert_eq!(server.health(), before + 1, "新連線上問到的值沿用");
        assert!(s.client.connected(), "有 session 的程式照舊保留連線");
    }

    // #173 code review：Caps Lock 帶來的英文在引擎重啟後重開 session 時不能變成這個程式自己的英文。
    #[test]
    fn reopened_session_keeps_caps_english_temporary() {
        let modes_after = |server: &CapsServer, from: usize| -> Vec<Value> {
            server.requests.lock().unwrap()[from..]
                .iter()
                .filter(|r| r["type"] == "mode" || r["type"] == "caps_lock")
                .cloned()
                .collect()
        };
        // (a) Caps Lock 英文中在設定程式關掉這一項（引擎以新設定重啟）：照注音重開，不送「設成英文」。
        let server = CapsServer::new(true, false);
        let mut s = Session::with_client(Client::with_pipe(&server.name));
        s.key("j");
        assert!(s.caps_lock(true).caps_english);
        server.restart(&mut s);
        let from = server.requests.lock().unwrap().len();
        s.key("a");
        assert_eq!(modes_after(&server, from), Vec::<Value>::new(), "設定關掉後照注音開始");

        // (b) 開始時英文：先設回注音，再以 caps_lock 還原暫時的英文（關掉 Caps Lock 才回得到注音）。
        let server = CapsServer::new(true, true);
        let mut s = Session::with_client(Client::with_pipe(&server.name));
        s.key("j");
        assert!(s.caps_lock(true).caps_english);
        server.restart(&mut s); // 換成關閉
        server.restart(&mut s); // 再換回開啟：引擎重啟過、設定仍開著
        let from = server.requests.lock().unwrap().len();
        s.key("A");
        assert_eq!(
            modes_after(&server, from),
            vec![json!({"type": "mode", "on": false}), json!({"type": "caps_lock", "on": true})]
        );
    }
}
