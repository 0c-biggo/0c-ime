#include "session.h"
#include "keys.h"

namespace zuin {

namespace {
nlohmann::json replayView(const nlohmann::json &snapshot) {
    nlohmann::json view = nlohmann::json::object();
    for (const char *key : {"preedit", "cursor", "candidates", "suggestion", "suggestion_edit", "mode"}) {
        if (snapshot.contains(key)) { view[key] = snapshot[key]; }
    }
    return view;
}

// 會產生文字的按鍵：單一字元，或數字鍵盤的 "KP" 加一個字元。
bool textKey(const std::string &k) { return k.size() == 1 || (k.size() == 3 && k.compare(0, 2, "KP") == 0); }
} // namespace


Session::Session(Client &client, Trace *trace, bool documentContextEnabled)
    : documentContextEnabled_(documentContextEnabled), client_(client), trace_(trace) {
    static std::atomic<uint64_t> next{0};
    traceID_ = ++next;
}

Snapshot Session::activate() {
    traceEvent("activate");
    // 只用現有連線：焦點事件不為此啟動 0c-server（引擎不在時第一個按鍵照常連線，新 session 從開始時的模式起，
    // 核心也會跟上共用的模式）。目前是英文時照舊連線（以前就會為了回到注音而連線）。
    if (!(client_.connected() && ensureOpen()) && (!english_ || !client_.connect() || !ensureOpen())) { return Snapshot{}; }
    if (serverInfo_.value("mode_memory", "") == "shared") { return handle({{"type", "mode"}, {"on", false}}); }
    // native：回到開始時的模式（start-mode，#160；舊引擎沒有回報時是注音）。
    const bool start = serverInfo_.value("start_mode", "") == "english";
    if (english_ == start) { return Snapshot{}; }
    return handle({{"type", "mode"}, {"on", start}});
}

void Session::setPrivate(bool value) {
    private_ = value;
    if (private_) {
        tainted_ = true;
        previous_ = nlohmann::json::object();
    } else if (!composing_ && !paused_) {
        tainted_ = false;
    }
}

void Session::traceEvent(const std::string &event, nlohmann::json detail) {
    if (!trace_ || !trace_->enabled() || private_ || paused_ || tainted_) { return; }
    if (!detail.is_object()) { detail = nlohmann::json::object(); }
    detail["event"] = event;
    detail["session"] = traceID_;
    trace_->record(std::move(detail));
}

void Session::presented(uint64_t sequence, int64_t duration) {
    if (sequence) { traceEvent("present", {{"request_seq", sequence}, {"duration_us", duration}}); }
}

uint64_t Session::traceRequest(const nlohmann::json &request, const nlohmann::json &after,
                              int64_t started, int attempts, unsigned generationBefore, int64_t engineUS) {
    uint64_t sequence = 0;
    // 英文文字現在可能留在組字快照；僅遮罩請求鍵已不足以保護內容。
    // 這次混合組字到清空為止停錄，包含切回注音後的快照與提交。
    if (after.value("mode", "") == "english" && request.value("type", "") == "key" &&
        textKey(request.value("key", "")) && after.contains("preedit") && !after["preedit"].empty()) {
        tainted_ = true;
        previous_ = nlohmann::json::object();
    }
    bool suppressed = private_ || paused_ || tainted_;
    if (trace_ && trace_->enabled() && !suppressed) {
        auto req = request;
        // 英文模式不記普通文字鍵的內容；仍保留事件、導覽鍵與回應時間。
        if (english_ && req.value("type", "") == "key") {
            std::string k = req.value("key", "");
            if (textKey(k)) { req.erase("key"); req["key_class"] = "text"; }
        }
        sequence = trace_->record({{"event", "request"}, {"session", traceID_},
            {"engine_session", id_}, {"generation", client_.generation()},
            {"reconnected", generationBefore != client_.generation()}, {"attempts", attempts},
            {"engine_us", engineUS}, {"server", serverInfo_},
            {"duration_us", Trace::monotonicMicros() - started},
            {"request", req}, {"before", previous_}, {"after", after}});
    }
    if (after.contains("error")) { return sequence; } // 失敗不是清空組字，不能解除隱私污染標記
    composing_ = after.contains("preedit") && !after["preedit"].empty();
    previous_ = suppressed || !trace_ || !trace_->enabled() ? nlohmann::json::object() : after;
    if (!private_ && !paused_ && !composing_) { tainted_ = false; }
    return sequence;
}

std::string Snapshot::text() const {
    std::string s;
    for (const auto &g : preedit) {
        s += g.text;
    }
    return s;
}

int Snapshot::cursorByte() const {
    std::string t = text();
    int runes = 0;
    for (size_t i = 0; i < t.size(); i++) {
        if ((static_cast<unsigned char>(t[i]) & 0xC0) != 0x80) {
            if (runes == cursorRune) {
                return static_cast<int>(i);
            }
            runes++;
        }
    }
    return static_cast<int>(t.size());
}

Session::~Session() {
    traceEvent("close");
    if (id_ != 0 && gen_ == client_.generation()) {
        nlohmann::json reply;
        client_.call({{"op", "close"}, {"session", id_}}, reply);
    }
}

ShiftKeys ShiftKeys::from(const nlohmann::json &info) {
    ShiftKeys k;
    if (!info.is_object()) { return k; }
    auto text = [&](const char *name) {
        auto value = info.find(name);
        return value != info.end() && value->is_string() ? value->get<std::string>() : std::string();
    };
    k.left = text("left_shift") == "true";
    k.right = text("right_shift") != "false"; // 舊引擎沒有 right_shift：右 Shift 一直都會切換
    try {
        auto ms = std::stoi(text("shift_tap_ms"));
        if (ms > 0) { k.tapMs = ms; }
    } catch (const std::exception &) {}
    return k;
}

ShiftKeys Session::shiftKeys() {
    nlohmann::json reply;
    // 套用設定會重啟引擎，舊連線在這一次才發現斷了；重連再問一次，才不會照預設值切換（例如剛關掉右 Shift）。
    bool ok = client_.call({{"op", "health"}}, reply) || client_.call({{"op", "health"}}, reply);
    if (!ok || !reply.value("ok", false)) { return {}; }
    return ShiftKeys::from(reply.value("info", nlohmann::json::object()));
}

bool Session::vocabKeys() {
    if (!client_.connect() || !ensureOpen()) { return false; }
    auto value = serverInfo_.find("vocab_keys");
    return value != serverInfo_.end() && value->is_string() && value->get<std::string>() == "true";
}

bool Session::styleShiftSpace() {
    if (client_.connected()) { client_.alive(); } // refresh a retired session before reading cached capabilities
    if (!client_.connect() || !ensureOpen()) { return false; }
    auto value = serverInfo_.value("input_style_shift_space", std::string());
    return !value.empty() && value != "disabled";
}

bool Session::styleCapsLower() {
    if (client_.connected()) { client_.alive(); } // refresh a retired session before reading cached capabilities
    return client_.connect() && ensureOpen() && serverInfo_.value("input_style_caps_letter", "") == "lower";
}

bool Session::capsLockEnglish(bool spawn) {
    if (!spawn) {
        // Caps Lock 鍵本身：不為此啟動 0c-server。剛在設定程式打開這一項時引擎重啟了，open 的快取是舊的，
        // 不重新確認的話第一個字母會照注音打、下一個才切英文。設定改了一定會重啟引擎（舊連線跟著斷），所以連線還在、
        // session 是這一代連線開的就沿用 open 的值，不問引擎；斷了才以 health 重新確認（舊連線這一次才發現斷了，重連再問一次）。
        if (!client_.connected()) { return false; }
        if (id_ != 0 && gen_ == client_.generation() && client_.alive()) {
            auto value = serverInfo_.find("caps_lock_english");
            return value != serverInfo_.end() && value->is_string() && value->get<std::string>() == "true";
        }
        nlohmann::json reply;
        bool ok = client_.call({{"op", "health"}}, reply) || client_.call({{"op", "health"}}, reply);
        if (!ok || !reply.value("ok", false)) { return false; }
        auto info = reply.value("info", nlohmann::json::object());
        auto value = info.find("caps_lock_english");
        if (value == info.end() || !value->is_string() || value->get<std::string>() != "true") { return false; }
    }
    if (!client_.connect() || !ensureOpen()) { return false; }
    auto value = serverInfo_.find("caps_lock_english");
    return value != serverInfo_.end() && value->is_string() && value->get<std::string>() == "true";
}

bool Session::simplifiedLive() const {
    auto value = serverInfo_.find("output_simplified_live");
    return id_ != 0 && gen_ == client_.generation() && value != serverInfo_.end() && value->is_string() && value->get<std::string>() == "true";
}

bool Session::simplifiedAtOpen() const {
    auto value = serverInfo_.find("output_simplified");
    return value != serverInfo_.end() && value->is_string() && value->get<std::string>() == "true";
}

bool Session::simplifiedHotkey() {
    if (client_.connected()) { client_.alive(); } // 斷了就關掉，下面重連
    if (!client_.connect() || !ensureOpen() || !simplifiedLive()) { return false; }
    auto value = serverInfo_.find("simplified_hotkey");
    return value != serverInfo_.end() && value->is_string() && value->get<std::string>() == "true";
}

bool Session::ctrlPunct(const std::string &key) {
    if (!client_.connect() || !ensureOpen()) { return false; }
    auto value = serverInfo_.find("ctrl_punctuation");
    if (value == serverInfo_.end() || !value->is_string() || value->get<std::string>() != "true") { return false; }
    auto keys = serverInfo_.find("ctrl_punctuation_keys");
    return zuin::ctrlPunctKnown(key, keys != serverInfo_.end() && keys->is_string() ? keys->get<std::string>() : std::string());
}

bool Session::ensureOpen() {
    if (id_ != 0 && gen_ == client_.generation()) {
        return true;
    }
    nlohmann::json reply;
    if (!client_.call({{"op", "open"}, {"version", 0}}, reply) || !reply.value("ok", false)) {
        id_ = 0;
        return false;
    }
    id_ = reply.value("session", uint64_t(0));
    serverInfo_ = reply.value("info", nlohmann::json::object());
    gen_ = client_.generation();
	// Native panels support vertical and horizontal hints, but not arbitrary grids.
	// Preserve explicit horizontal styles; flatten other grids to a vertical list.
	nlohmann::json display;
    if (serverInfo_.contains("candidate_layout") && serverInfo_.value("candidate_layout", "") != "horizontal" &&
		(!client_.call({{"op", "handle"}, {"session", id_}, {"req", {{"type", "display"}, {"columns", 1}}}}, display) || !display.value("ok", false))) {
		id_ = 0; return false;
	}
    // 能確認應用程式文件更新才啟用跨提交協定；舊 server 會安全忽略。
    nlohmann::json capability;
    if (documentContextEnabled_ && (!client_.call({{"op", "handle"}, {"session", id_}, {"req", {{"type", "context"}, {"context_enabled", true}}}}, capability) || !capability.value("ok", false))) {
        id_ = 0; return false;
    }
    // 空白 session 以注音重開；仍有組字時由下方日誌重播中英切換，保留混合文字。
    if (paused_) {
        nlohmann::json r;
        if (!client_.call({{"op", "handle"}, {"session", id_}, {"req", {{"type", "pause"}, {"on", true}}}}, r) ||
            !r.value("ok", false)) {
            id_ = 0;
            return false;
        }
    }
    // Caps Lock 帶來的英文（#173）：新 session 不知道 Caps Lock 開著，先還原，不然重開後的第一個字母照注音打、
    // 下一個才切回英文。設定剛關掉（引擎以新設定重啟）時不送。真正的鎖定狀態由這次按鍵帶的同步改正。
    if (capsLock_) {
        auto value = serverInfo_.find("caps_lock_english");
        nlohmann::json r;
        if (value == serverInfo_.end() || !value->is_string() || value->get<std::string>() != "true") {
            capsLock_ = false;
        } else if (!client_.call({{"op", "handle"}, {"session", id_}, {"req", {{"type", "caps_lock"}, {"on", true}}}}, r) ||
                   !r.value("ok", false)) {
            id_ = 0;
            return false;
        }
    }
    // 重播：新 session 回到斷線前的組字狀態
    nlohmann::json lastReplay;
    recovered_ = nullptr;
    for (const auto &req : log_) {
        nlohmann::json r;
        auto replayRequest = req;
        if (private_) { replayRequest["private"] = true; }
        if (!client_.call({{"op", "handle"}, {"session", id_}, {"req", replayRequest}}, r) || !r.value("ok", false)) {
            id_ = 0;
            return false;
        }
        lastReplay = r;
    }
    // 神經逾時／模型版本或個人資料變動可能使同一串按鍵重播成不同文字。
    // 先顯示恢復結果，不在使用者看見之前執行 Enter 或按候選 ID 選字。
    if (!log_.empty() && !replayView_.is_null() && lastReplay.contains("snapshot") &&
        replayView(lastReplay["snapshot"]) != replayView_) {
        recovered_ = lastReplay;
    }
    return true;
}

Snapshot Session::handle(const nlohmann::json &request) {
    const auto started = Trace::monotonicMicros();
    const unsigned generationBefore = client_.generation();
    auto wireRequest = request;
    if (private_) { wireRequest["private"] = true; }
    // 前文確認與欄數不是使用者操作；其他請求都讓下一次切換不再還原注音。
    std::vector<nlohmann::json> cleared;
    if (request.value("type", "") != "context" && request.value("type", "") != "display") {
        toggled_ = false;
        cleared.swap(cleared_);
    }
    if (request.value("type", "") == "pause") {
        paused_ = request.value("on", false);
        if (paused_) { tainted_ = true; previous_ = nlohmann::json::object(); }
        else if (!composing_) { tainted_ = false; }
    }
    for (int attempt = 0; attempt < 3; attempt++) {
        // 先確認連線：若因此重新連線，代數改變，ensureOpen 會重開 session 並重播
        if (!client_.connect() || !ensureOpen()) {
            continue;
        }
        if (!recovered_.is_null() && request.value("type", "") == "context") {
            // 文件確認不是可見畫面更新；保留復原提示給下一次真正操作。
            return Snapshot{};
        }
        if (!recovered_.is_null() && request.value("type", "") == "cancel") {
            recovered_ = nullptr; // Escape/reset 的取消意圖不需要重新確認。
        }
        if (!recovered_.is_null() && request.value("type", "") == "commit") {
            // 失焦時不能等待下一鍵；提交斷線前實際顯示文字，取消重播狀態而不學習新猜測。
            auto prior = parse({{"ok", true}, {"snapshot", replayView_}});
            nlohmann::json ignored;
            if (!client_.call({{"op", "handle"}, {"session", id_}, {"req", {{"type", "cancel"}}}}, ignored)) {
                id_ = 0;
            }
            Snapshot s; s.ok = true; s.handled = true; s.reconnected = true; s.commit = prior.text();
            log_.clear(); replayView_ = nullptr; recovered_ = nullptr; composing_ = false;
            return s;
        }
        if (!recovered_.is_null()) {
            auto reply = recovered_;
            recovered_ = nullptr;
            reply["snapshot"]["handled"] = true;
            reply["snapshot"].erase("commit");
            reply["snapshot"]["notices"] = {"連線恢復，請確認組字後再繼續。"};
            replayView_ = replayView(reply["snapshot"]);
            Snapshot s = parse(reply);
            s.reconnected = true;
            english_ = s.english;
        englishPending_ = s.englishPending;
            capsLock_ = s.capsLock;
            s.traceSequence = traceRequest({{"type", "recovery"}}, reply["snapshot"], started,
                                          attempt + 1, generationBefore);
            return s;
        }
        if (request.value("type", "") == "context" && generationBefore != client_.generation()) {
            wireRequest["on"] = false; wireRequest["context_id"] = 0;
        }
        nlohmann::json reply;
        if (!client_.call({{"op", "handle"}, {"session", id_}, {"req", wireRequest}}, reply)) {
            continue; // 連線斷了：下一輪重連並重播
        }
        if (!reply.value("ok", false)) {
            id_ = 0; // server 不認得這個 session（例如 server 重啟過）：重開並重播
            continue;
        }
        Snapshot s = parse(reply);
        s.reconnected = generationBefore != client_.generation();
        s.traceSequence = traceRequest(wireRequest, reply["snapshot"], started, attempt + 1, generationBefore,
                                       reply.value("engine_us", int64_t(-1)));
        english_ = s.english;
        englishPending_ = s.englishPending;
        capsLock_ = s.capsLock;
        std::string type = request.value("type", "");
        // 符號選單可以在空白組字區開啟，也必須重播才能保留選單與反白位置。
        bool composing = !s.preedit.empty() || !s.candidates.empty();
        if (!s.commit.empty() || type == "cancel" || !composing) {
            // #90：切到英文清掉未打完的注音而結束組字時留下這段日誌；立即切回放回注音時接上，重播才得到同樣的組字。
            if (type == "toggle" && s.commit.empty() && !log_.empty()) {
                cleared_ = std::move(log_);
                cleared_.push_back(wireRequest);
            }
            log_.clear();
            replayView_ = nullptr;
        } else if (type != "context" && type != "caps_lock" && type != "output_simplified") {
            // Caps Lock 狀態不重播：新 session 由下一個按鍵重新同步（#173）；「輸出簡體字」是全域設定，重播會再切一次（#169）
            if (log_.empty() && type == "toggle") { log_ = std::move(cleared); }
            log_.push_back(wireRequest);
        }
        if (composing) { replayView_ = replayView(reply["snapshot"]); }
        return s;
    }
    traceRequest(wireRequest, {{"error", "server_unavailable"}}, started, 3, generationBefore);
    return Snapshot{};
}

Snapshot Session::parse(const nlohmann::json &reply) {
    Snapshot s;
    if (!reply.value("ok", false) || !reply.contains("snapshot")) {
        return s;
    }
    const auto &j = reply["snapshot"];
    s.ok = true;
    s.handled = j.value("handled", false);
    s.commit = j.value("commit", "");
    s.contextID = j.value("context_id", uint64_t(0));
    s.suggestion = j.value("suggestion", "");
    s.english = j.value("mode", "") == "english";
    s.englishPending = j.value("english_pending", false);
    s.capsLock = j.value("caps_lock", false);
    s.outputSimplified = j.value("output_simplified", false);
    if (j.contains("preedit")) {
        for (const auto &g : j["preedit"]) {
            s.preedit.push_back({g.value("text", ""), g.value("style", "normal")});
        }
    }
    if (j.contains("cursor")) {
        s.cursorRune = j["cursor"].value("rune", 0);
    }
    if (j.contains("candidates")) {
        const auto &c = j["candidates"];
        s.columns = c.value("columns", 1);
        s.page = c.value("page", 0);
        s.pages = c.value("pages", 0);
        s.highlight = c.value("highlight", 0);
		s.candidateTitle = c.value("title", "");
        for (const auto &it : c["items"]) {
            s.candidateKeys.push_back(candidateKey(it, s.candidates.size(), s.english));
            s.candidates.emplace_back(it.value("id", 0), it.value("text", ""));
            s.candidateNotes.push_back(it.value("note", ""));
        }
    }
    if (j.contains("reading_hint") && j["reading_hint"].is_object()) {
        s.readingHint = j["reading_hint"].value("text", "");
    }
    if (j.contains("notices")) {
        for (const auto &n : j["notices"]) {
            s.notices.push_back(n.get<std::string>());
        }
    }
    return s;
}

} // namespace zuin
