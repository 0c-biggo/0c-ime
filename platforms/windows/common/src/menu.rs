//! 輸入法選單（#13）：更新與設定的狀態、選單項目與允許的 0c-update 命令。
//! TSF DLL 顯示選單，0c-ui 代為執行 0c-update（DLL 可能在沙箱內，不能直接啟動程式或讀個人資料目錄）。
use serde::{Deserialize, Serialize};
use serde_json::Value;

/// DLL 與 0c-ui 之間選單管道的請求：`status` 取狀態、`run` 執行白名單內的命令。
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
pub struct MenuRequest {
    pub op: String,
    #[serde(default)]
    pub args: Vec<String>,
}

/// `0c-update status --json` 中選單需要的部分。
#[derive(Clone, Debug, PartialEq)]
pub struct UpdateStatus {
    pub ok: bool,
    pub installed: String,
    pub engine: String,
    pub mode: String,
    pub target: String,
    pub blocked: String,
    pub detail: String,
    pub action: String,
    pub paused_until: String,
    /// 自動更新元件被移除（`updater_missing`／`schedule_missing`，通常是防毒軟體誤判）；空字串為正常。
    pub updater: String,
    pub selection: String,
    pub runtime: String,
    pub selection_state: String,
    pub punctuation_state: String,
    pub punctuation: bool,
    pub document_context: bool,
    /// `features.vocab`：0.2.0 的「我的詞彙」入口（0.2.0 起恆為 true）。舊版 0c-update 沒有這個欄位，視為關閉。
    pub vocab: bool,
    /// 「輸出簡體字」（#169）：`settings.output_simplified`（0.2.1 起的 0c-update 才有，沒有時 None，選單不顯示這一項）；
    /// `runtime.output_simplified_live` 時以 `runtime.output_simplified`（引擎目前的值）為準。
    pub output_simplified: Option<bool>,
}

impl Default for UpdateStatus {
    fn default() -> Self {
        UpdateStatus {
            ok: false,
            installed: String::new(),
            engine: String::new(),
            mode: "auto".into(),
            target: String::new(),
            blocked: String::new(),
            detail: String::new(),
            action: String::new(),
            paused_until: String::new(),
            updater: String::new(),
            selection: "neural".into(),
            runtime: String::new(),
            selection_state: String::new(),
            punctuation_state: String::new(),
            punctuation: false,
            document_context: true,
            vocab: false,
            output_simplified: None,
        }
    }
}

fn text(v: &Value, key: &str) -> String {
    v.get(key).and_then(Value::as_str).unwrap_or_default().to_string()
}

pub fn parse_status(json: &str) -> UpdateStatus {
    let mut s = UpdateStatus::default();
    let Ok(v) = serde_json::from_str::<Value>(json) else { return s };
    if !v.is_object() {
        return s;
    }
    s.ok = true;
    s.installed = text(&v, "installed");
    s.engine = text(&v, "engine");
    s.action = text(&v, "action");
    s.paused_until = text(&v, "paused_until");
    s.updater = text(&v, "updater");
    if let Some(m) = v.get("mode").and_then(Value::as_str) {
        s.mode = m.into();
    }
    if let Some(t) = v.get("target") {
        s.target = text(t, "version");
    }
    if let Some(b) = v.get("blocked") {
        s.blocked = text(b, "reason");
        s.detail = text(b, "detail");
    }
    if let Some(c) = v.get("settings") {
        if let Some(x) = c.get("selection").and_then(Value::as_str) {
            s.selection = x.into();
        }
        if let Some(x) = c.get("punctuation").and_then(Value::as_bool) {
            s.punctuation = x;
        }
        if let Some(x) = c.get("document_context").and_then(Value::as_bool) {
            s.document_context = x;
        }
        s.output_simplified = c.get("output_simplified").and_then(Value::as_bool);
    }
    if let Some(f) = v.get("features") {
        s.vocab = f.get("vocab").and_then(Value::as_bool).unwrap_or(false);
    }
    if let Some(r) = v.get("runtime") {
        s.runtime = text(r, "state");
        s.selection_state = text(r, "selection_state");
        s.punctuation_state = text(r, "punctuation_state");
        if r.get("output_simplified_live").and_then(Value::as_bool) == Some(true) {
            if let Some(x) = r.get("output_simplified").and_then(Value::as_bool) {
                s.output_simplified = Some(x);
            }
        }
    }
    s
}

/// 「輸出簡體字」退回 0c-update 時的命令（#169）：寫設定、請引擎套用（能即時切換就不重啟），並送通知。
/// DLL 能直接請引擎切換時不用它（見 tsf 的 langbar）。
pub fn simplified_command(on: bool) -> Vec<String> {
    args(&["set", "output-simplified", if on { "on" } else { "off" }, "-apply", "-notify"])
}

/// 選單項目是不是「輸出簡體字」；是的話回傳要切換成的值。
pub fn simplified_target(a: &[String]) -> Option<bool> {
    match a.iter().map(String::as_str).collect::<Vec<_>>().as_slice() {
        ["set", "output-simplified", v @ ("on" | "off"), "-apply", "-notify"] => Some(*v == "on"),
        _ => None,
    }
}

pub fn model_label(state: &str, runtime: &str, fallback: &str) -> String {
    match runtime {
        "not_running" => return "引擎未執行".into(),
        "unavailable" => return "狀態無法確認".into(),
        _ => {}
    }
    match state {
        "ready" => "已就緒".into(),
        "loading" => format!("載入中，暫用{fallback}"),
        "fallback" => format!("已退回{fallback}"),
        "disabled_by_setting" => "依設定關閉".into(),
        "not_installed" => format!("未附模型，使用{fallback}"),
        "runtime_missing" => format!("缺少推論元件，使用{fallback}"),
        "closed" => format!("已停止，使用{fallback}"),
        _ => "狀態未知".into(),
    }
}

pub fn model_fallback(s: &UpdateStatus) -> bool {
    s.selection_state == "fallback" || s.punctuation_state == "fallback"
}

/// 選單第一列：目前版本或待辦（與 Linux 選單相同）。
pub fn status_label(s: &UpdateStatus) -> String {
    if !s.ok {
        return "靈犀注音：無法讀取更新狀態".into();
    }
    if !s.updater.is_empty() {
        return "自動更新已停止（可能被防毒軟體移除）— 修復".into();
    }
    if !s.target.is_empty() {
        let t = &s.target;
        return match s.blocked.as_str() {
            "needs_consent" => format!("有新版本 {t} — 立即安裝"),
            "needs_relogin" => format!("{t}：{}", if s.detail.is_empty() { "登出再登入後生效" } else { &s.detail }),
            "health_failed" => format!("{t} 更新未完成，已保留原版本"),
            _ if s.action == "finish" => format!("{t} 已準備好 — 現在完成更新"),
            "metered_network" => format!("{t} 等待非計量網路下載"),
            "low_battery" => format!("{t} 等待充電後更新"),
            _ => format!("{t} 更新中"),
        };
    }
    if s.paused_until.len() >= 10 {
        return format!("自動更新暫停至 {}", &s.paused_until[..10]);
    }
    let v = if s.engine.is_empty() { &s.installed } else { &s.engine };
    format!("靈犀注音 {} · 已是最新", if v.is_empty() { "（未知版本）" } else { v })
}

pub fn needs_attention(s: &UpdateStatus) -> bool {
    model_fallback(s) || !s.updater.is_empty() || !s.target.is_empty() && (s.action == "finish" || s.blocked == "needs_relogin" || s.blocked == "health_failed")
}

#[derive(Clone, Debug, PartialEq)]
pub struct MenuItem {
    pub id: u32,
    pub label: String,
    pub checked: bool,
    pub separator: bool,
    pub args: Vec<String>,
}

fn args(a: &[&str]) -> Vec<String> {
    a.iter().map(|x| x.to_string()).collect()
}

/// 選單項目；id 從 1 起，0 保留給「沒有選擇」。
pub fn menu_items(s: &UpdateStatus) -> Vec<MenuItem> {
    let first = if !s.updater.is_empty() {
        args(&["schedule", "repair", "-notify"])
    } else if s.action == "finish" {
        args(&["finish", "-notify"])
    } else {
        args(&["auto", "-now", "-notify"])
    };
    let onoff = |b: bool| if b { "off" } else { "on" };
    let mut v = vec![
        // 設定程式由 0c-update open-settings 找出並核對；不算更新操作，所以更新中也能按、不受狀態影響。
        ("設定…".into(), false, false, args(&["open-settings"])),
    ];
    // 「我的詞彙…」只在 features.vocab 為真時出現（#125）；同樣不算更新操作。
    if s.vocab {
        v.push(("我的詞彙…".into(), false, false, args(&["open-settings", "vocab"])));
    }
    v.extend([
        (String::new(), false, true, vec![]),
        (status_label(s), false, false, first),
        ("檢查更新".into(), false, false, args(&["auto", "-now", "-notify"])),
        (String::new(), false, true, vec![]),
        ("自動更新：自動保持最新（建議）".into(), s.mode == "auto", false, args(&["mode", "auto"])),
        ("自動更新：下載後詢問".into(), s.mode == "ask", false, args(&["mode", "ask"])),
        ("自動更新：手動".into(), s.mode == "manual", false, args(&["mode", "manual"])),
        if s.paused_until.is_empty() {
            ("暫停自動更新 7 天".into(), false, false, args(&["pause", "7"]))
        } else {
            ("恢復自動更新".into(), false, false, args(&["resume"]))
        },
        (String::new(), false, true, vec![]),
        (format!("神經選字：{}", model_label(&s.selection_state, &s.runtime, "統計引擎")), s.selection == "neural", false,
         args(&["set", "selection", if s.selection == "neural" { "statistical" } else { "neural" }, "-apply"])),
        (format!("補標點：{}", model_label(&s.punctuation_state, &s.runtime, "原文提交")), s.punctuation, false, args(&["set", "punctuation", onoff(s.punctuation), "-apply"])),
        ("依前文選字".into(), s.document_context, false, args(&["set", "document-context", onoff(s.document_context), "-apply"])),
    ]);
    // 「輸出簡體字」（#169）：0c-update 回報這項設定時才有；DLL 能直接請引擎即時切換時改用 IPC（langbar）。
    if let Some(on) = s.output_simplified {
        v.push(("輸出簡體字".into(), on, false, simplified_command(!on)));
    }
    v.drain(..)
        .enumerate()
        .map(|(i, (label, checked, separator, args))| MenuItem { id: i as u32 + 1, label, checked, separator, args })
        .collect()
}

/// 0c-ui 只替 DLL 執行這些命令；沙箱內的宿主不能藉此執行任意 0c-update 動作（例如解除安裝）。
pub fn allowed_command(a: &[String]) -> bool {
    let a: Vec<&str> = a.iter().map(String::as_str).collect();
    matches!(
        a.as_slice(),
        ["open-settings"]
            | ["open-settings", "vocab"]
            | ["finish", "-notify"]
            | ["auto", "-now", "-notify"]
            | ["mode", "auto" | "ask" | "manual"]
            | ["pause", "7"]
            | ["resume"]
            | ["schedule", "repair", "-notify"]
            | ["set", "selection", "neural" | "statistical", "-apply"]
            | ["set", "punctuation" | "document-context", "on" | "off", "-apply"]
            | ["set", "output-simplified", "on" | "off", "-apply", "-notify"]
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn punctuation_defaults_off_and_keeps_explicit_on() {
        for json in ["x", "{}", r#"{"settings":{}}"#, r#"{"settings":{"punctuation":"maybe"}}"#] {
            let s = parse_status(json);
            assert!(!s.punctuation);
            assert!(s.document_context);
            assert_eq!(s.selection, "neural");
        }
        assert!(parse_status(r#"{"settings":{"punctuation":true}}"#).punctuation);
    }

    #[test]
    fn failed_model_is_visible_without_changing_preference() {
        let s = parse_status(r#"{"settings":{"selection":"neural","punctuation":true},"runtime":{"state":"running","selection_state":"fallback","punctuation_state":"ready"}}"#);
        let items = menu_items(&s);
        assert_eq!(items[10].label, "神經選字：已退回統計引擎");
        assert!(items[10].checked);
        assert_eq!(items[11].label, "補標點：已就緒");
        assert!(needs_attention(&s));
        assert_eq!(model_label("ready", "not_running", "統計引擎"), "引擎未執行");
        assert_eq!(model_label("", "", "統計引擎"), "狀態未知");
        assert_eq!(model_label("runtime_missing", "running", "統計引擎"), "缺少推論元件，使用統計引擎");
    }

    #[test]
    fn labels_follow_status() {
        let idle = parse_status(r#"{"installed":"0.1.0-dev.18","engine":"0.1.0-dev.19","mode":"auto","settings":{"selection":"statistical","punctuation":false}}"#);
        assert_eq!(status_label(&idle), "靈犀注音 0.1.0-dev.19 · 已是最新");
        assert!(!idle.punctuation && idle.document_context && idle.selection == "statistical");
        let consent = parse_status(r#"{"mode":"ask","target":{"version":"0.1.0-dev.19"},"blocked":{"reason":"needs_consent"},"action":"finish"}"#);
        assert_eq!(status_label(&consent), "有新版本 0.1.0-dev.19 — 立即安裝");
        assert!(needs_attention(&consent));
        assert_eq!(menu_items(&consent)[2].args, vec!["finish", "-notify"]);
        assert_eq!(status_label(&parse_status("x")), "靈犀注音：無法讀取更新狀態");
        let paused = parse_status(r#"{"paused_until":"2026-10-06T10:00:00Z"}"#);
        assert_eq!(status_label(&paused), "自動更新暫停至 2026-10-06");
        assert_eq!(menu_items(&paused)[8].label, "恢復自動更新");
    }

    #[test]
    fn removed_updater_takes_priority() {
        let s = parse_status(r#"{"installed":"0.1.0-dev.38","updater":"updater_missing","target":{"version":"0.1.0-dev.39"},"action":"finish"}"#);
        assert_eq!(status_label(&s), "自動更新已停止（可能被防毒軟體移除）— 修復");
        assert!(needs_attention(&s));
        assert_eq!(menu_items(&s)[2].args, vec!["schedule", "repair", "-notify"]);
        assert!(parse_status(r#"{"installed":"x"}"#).updater.is_empty());
    }

    #[test]
    fn settings_is_the_first_entry_and_does_not_depend_on_update_state() {
        for s in [UpdateStatus::default(), parse_status(r#"{"action":"finish","target":{"version":"x"}}"#), parse_status(r#"{"updater":"schedule_missing"}"#)] {
            let items = menu_items(&s);
            assert_eq!((items[0].id, items[0].label.as_str(), &items[0].args[..]), (1, "設定…", &["open-settings".to_string()][..]));
            assert!(items[1].separator);
            let ids: Vec<u32> = items.iter().map(|i| i.id).collect();
            assert_eq!(ids, (1..=items.len() as u32).collect::<Vec<_>>(), "id 從 1 起連續，0 保留給沒有選擇");
        }
        // 只有恰好沒有引數的 open-settings 被接受：不讓選單替沙箱內的宿主傳頁面或其他旗標。
        assert!(allowed_command(&["open-settings".to_string()]));
        assert!(!allowed_command(&["open-settings".to_string(), "about".to_string()]));
    }

    #[test]
    fn vocab_entry_follows_the_feature_flag() {
        // 舊版 0c-update 沒有 features、開關關閉、值不是布林：都不顯示，其餘項目與順序不變。
        for json in [r#"{"installed":"x"}"#, r#"{"features":{"vocab":false}}"#, r#"{"features":{"vocab":"yes"}}"#, r#"{"features":{}}"#, "x"] {
            let s = parse_status(json);
            assert!(!s.vocab, "{json}");
            let items = menu_items(&s);
            assert!(items.iter().all(|i| i.label != "我的詞彙…"), "{json}");
            assert!(items[1].separator);
        }
        let on = parse_status(r#"{"installed":"x","features":{"vocab":true}}"#);
        assert!(on.vocab);
        let items = menu_items(&on);
        assert_eq!((items[0].label.as_str(), &items[0].args[..]), ("設定…", &["open-settings".to_string()][..]));
        assert_eq!((items[1].id, items[1].label.as_str()), (2, "我的詞彙…"));
        assert_eq!(items[1].args, vec!["open-settings", "vocab"]);
        assert!(items[2].separator);
        assert_eq!(items.len(), menu_items(&UpdateStatus::default()).len() + 1);
        let ids: Vec<u32> = items.iter().map(|i| i.id).collect();
        assert_eq!(ids, (1..=items.len() as u32).collect::<Vec<_>>());
        // 只多准許 vocab 這一頁。
        assert!(allowed_command(&["open-settings".to_string(), "vocab".to_string()]));
        assert!(!allowed_command(&["open-settings".to_string(), "vocab".to_string(), "x".to_string()]));
        // 更新進行中照樣能開。
        let busy = parse_status(r#"{"action":"finish","target":{"version":"y"},"features":{"vocab":true}}"#);
        assert_eq!(menu_items(&busy)[1].args, vec!["open-settings", "vocab"]);
    }

    #[test]
    fn simplified_entry_follows_the_setting() {
        // 0.2.1 以前的 0c-update 沒有這項設定：不顯示，其他項目不變。
        let old = parse_status(r#"{"settings":{"selection":"neural"}}"#);
        assert_eq!(old.output_simplified, None);
        assert!(menu_items(&old).iter().all(|i| i.label != "輸出簡體字"));
        // 設定檔的值；放在「依前文選字」下面，打勾表示開著，點了切成相反的值。
        let stored = parse_status(r#"{"settings":{"output_simplified":true,"document_context":true}}"#);
        let items = menu_items(&stored);
        let at = items.iter().position(|i| i.label == "輸出簡體字").unwrap();
        assert_eq!(items[at - 1].label, "依前文選字");
        assert!(items[at].checked);
        assert_eq!(items[at].args, vec!["set", "output-simplified", "off", "-apply", "-notify"]);
        assert_eq!(simplified_target(&items[at].args), Some(false));
        assert_eq!(items.len(), menu_items(&old).len() + 1);
        // 引擎能即時切換時以 runtime 的值為準（剛從別處切換、設定檔還沒讀到也一樣）。
        let live = parse_status(r#"{"settings":{"output_simplified":false},"runtime":{"state":"running","output_simplified_live":true,"output_simplified":true}}"#);
        assert_eq!(live.output_simplified, Some(true));
        let off = parse_status(r#"{"settings":{"output_simplified":false},"runtime":{"output_simplified":true}}"#);
        assert_eq!(off.output_simplified, Some(false), "舊引擎沒有 output_simplified_live：照設定檔");
        assert_eq!(parse_status(r#"{"settings":{"output_simplified":"yes"}}"#).output_simplified, None);
        assert_eq!(simplified_target(&simplified_command(true)), Some(true));
        assert_eq!(simplified_target(&args(&["set", "output-simplified", "on", "-apply"])), None);
    }

    #[test]
    fn every_menu_command_is_allowed_and_nothing_else() {
        for s in [UpdateStatus::default(), parse_status(r#"{"action":"finish","target":{"version":"x"},"paused_until":"2026-10-06"}"#),
                  parse_status(r#"{"updater":"schedule_missing"}"#), parse_status(r#"{"features":{"vocab":true}}"#),
                  parse_status(r#"{"settings":{"output_simplified":true}}"#), parse_status(r#"{"settings":{"output_simplified":false}}"#)] {
            for item in menu_items(&s).iter().filter(|i| !i.separator) {
                assert!(allowed_command(&item.args), "{:?}", item.args);
            }
        }
        for bad in [vec!["uninstall"], vec!["set", "selection", "evil", "-apply"], vec!["finish"], vec!["auto", "-feed", "https://x"], vec!["schedule", "remove"], vec!["schedule", "install"],
                    vec!["set", "output-simplified", "maybe", "-apply", "-notify"], vec!["set", "output-simplified", "on", "-notify", "-apply"]] {
            assert!(!allowed_command(&bad.iter().map(|x| x.to_string()).collect::<Vec<_>>()));
        }
    }
}
