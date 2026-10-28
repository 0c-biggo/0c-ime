// 一個輸入欄位在 0c-server 上的 session：請求、重連後的重播、快照的解讀。不依賴 Fcitx。
#pragma once

#include <string>
#include <vector>

#include "client.h"
#include "trace.h"

namespace zuin {

struct Segment {
    std::string text, style; // style：normal、locked、corrected、partial、pending、marked、reading
};

// candidateKey 是候選第 index 項的鍵標（#112）：核心給的 key（1、q、;）；舊核心沒有這個欄位時依位置 1–0，
// 英文模式（只有自動補字候選，數字在那裡是文字）不畫。
inline std::string candidateKey(const nlohmann::json &item, size_t index, bool english) {
    if (item.contains("key") && item["key"].is_string()) { return item["key"].get<std::string>(); }
    return english ? std::string() : std::to_string((index + 1) % 10);
}

struct Snapshot {
    bool ok = false;      // false：server 不可用，呼叫端應放行按鍵
    bool handled = false; // false：把原始按鍵交還應用程式
    std::string commit;
    std::vector<Segment> preedit;
    int cursorRune = 0;
    std::string suggestion;
    std::vector<std::pair<int, std::string>> candidates; // （ID, 文字），目前這一頁
    std::vector<std::string> candidateNotes; // 與 candidates 等長的低調附註；模糊音近音為「近音 ㄓ ㄕˋ」，其餘為空字串
    // 與 candidates 等長的鍵標（#112）：核心給的選字鍵（1、q、;）；舊核心沒有時依位置 1–0，英文模式的補字候選為空字串（不畫）。
    std::vector<std::string> candidateKeys;
    int page = 0, pages = 0;
    int columns = 1;
    int highlight = 0; // 反白的候選在 candidates 裡的索引
	std::string candidateTitle;
    std::vector<std::string> notices;
    // 中英混打時照字面顯示、按空白會組成中文的數字按鍵的讀音（20 → ㄉㄢ，#162）；沒有時為空。
    std::string readingHint;
    bool english = false; // true 為英文；有組字時英數留在框內，空白時放行
    bool englishPending = false;
    bool capsLock = false; // 引擎記得的 Caps Lock 狀態（caps_lock，#173）
    // 送出的文字目前會轉成簡體（output_simplified，#169）。只有回報 output_simplified_live 的引擎才有這一項。
    bool outputSimplified = false;
    uint64_t traceSequence = 0;
    uint64_t contextID = 0;
    bool reconnected = false;

    std::string text() const;
    // cursorByte 是游標在 text() 中的 UTF-8 位元組位置（Fcitx 的 Text 以位元組計）。
    int cursorByte() const;
};

// 中英切換鍵（health 的 left_shift、right_shift、shift_tap_ms，#117）。引擎不可用或舊版引擎沒有某一項時：
// 左 Shift 照 Fcitx5 原本的用途（false）、右 Shift 照舊切換（true）、單按門檻 300 ms。
struct ShiftKeys {
    bool left = false, right = true;
    int tapMs = 300;
    static ShiftKeys from(const nlohmann::json &info);
};

class Session {
public:
    explicit Session(Client &client, Trace *trace = nullptr, bool documentContextEnabled = false);
    ~Session();

    // handle 送出一個請求（proto.Request 的 JSON）。連線中斷時自動重連、重開 session，
    // 並重播這次組字以來的請求，組字狀態因此不會遺失（docs/19）。
    Snapshot handle(const nlohmann::json &request);

    Snapshot key(const std::string &key) { return handle({{"type", "key"}, {"key", key}}); }
    // 單按 Shift 切換中英。上一次切換之後沒有任何按鍵或其他請求時帶 restore，
    // 核心才放回切到英文時清掉的未打完注音（#90 防誤觸）。
    Snapshot toggle() {
        const bool restore = toggled_;
        auto snap = handle({{"type", "toggle"}, {"restore", restore}});
        toggled_ = snap.ok;
        return snap;
    }
    // 沒有送給核心就交還應用程式的按鍵（快捷鍵、未映射鍵）同樣表示切換之後有操作。
    void bypassed() { toggled_ = false; }
    // 交還應用程式的 Ctrl＋Home／End、Ctrl＋Shift+Tab 在放行前同步英文 token 邊界；
    // 一般快捷鍵只交付 pending 英文。這個入口不得改動注音組字：有組字（英文模式保留的中文）時
    // 核心的 Home／End 會移動組字游標，所以只在沒有組字或組字就是 pending 英文時才送邊界鍵。
    Snapshot bypassEnglish(const std::string &boundary = "") {
        if (english_ && !boundary.empty() && (englishPending_ || !composing_)) { return key(boundary); }
        if (englishPending_) { return handle({{"type", "commit"}}); }
        return {};
    }
    // 組字中按 Ctrl／Alt／Super 組合鍵：先送出組字，再把原鍵交給應用程式（#106，同 macOS），
    // 聊天軟體的 Ctrl+Enter、Ctrl+V 才不會先執行快捷鍵、組字後送。沒有組字時不送請求。
    Snapshot commitForShortcut() {
        if (composing_ || englishPending_) { return handle({{"type", "commit"}}); }
        return {};
    }
    // 啟用（換到這個輸入框）時回到開始時的中英模式（mode_memory=native；start_mode，#160），注音組字保持原狀。
    // 不需恢復或恢復失敗時回傳 ok=false，外殼不重畫組字。
    // 「所有輸入框共用中英模式」（mode_memory=shared，#115）時一律送「設成中文」，核心改成回到共用的模式，
    // 換到這個輸入框時注／英指示立刻是共用的模式，不必等第一個按鍵。
    Snapshot activate();
    // 不把前文確認寫進重播；連線改變即失效。
    bool documentContext(uint64_t token) {
        if (!documentContextEnabled_ || id_ == 0) { return false; }
        auto s = handle({{"type", "context"}, {"on", token != 0}, {"context_id", token}});
        return s.ok && !s.reconnected;
    }
    bool contextAllowed() const { return documentContextEnabled_ && !private_ && !paused_ && !tainted_ && !english_; }
    bool english() const { return english_; }
    // 引擎目前載入的中英切換鍵設定（toggle-key，#117）。每次向引擎查詢，設定套用（引擎重啟）後立即生效。
    ShiftKeys shiftKeys();
    // 引擎是否認得 0.2.0 的加詞與反悔鍵名（open 回覆的 vocab_keys，#123）。值隨 session 快取，
    // 引擎重啟（例如套用設定）後重開 session 時更新，不必每個按鍵查 health。斷線要等下一個請求才發現，
    // 引擎剛重啟、還沒有任何請求時沿用舊值：舊值 false 只是這一鍵照原本的鍵送；舊值 true 送到新引擎，
    // 開關關閉時它把新鍵名當原本的鍵處理。引擎不可用或舊版引擎沒有這一項時為 false。
    bool vocabKeys();
    bool styleShiftSpace();
    bool styleCapsLower();
    // 引擎是否認得 Ctrl＋標點的鍵名 key 而且設定開著（open 回覆的 ctrl_punctuation 與 ctrl_punctuation_keys，#168、#231）；
    // 快取方式同 vocabKeys。舊引擎沒有這一項時為 false，Ctrl＋標點照舊交給應用程式。
    bool ctrlPunct(const std::string &key);
    // 引擎是否認得 caps_lock 請求而且「Caps Lock 開著時直接打英文」開著（open 回覆的 caps_lock_english，#173）；快取方式同 vocabKeys。
    // 舊引擎沒有這一項、或設定關閉時為 false，注音模式照舊不理 Caps Lock（#168）。spawn=false（Caps Lock 鍵本身）不為此啟動 0c-server，
    // 並以 health 重新確認（設定剛打開、引擎重啟後 open 的快取還是舊的）。
    bool capsLockEnglish(bool spawn);
    // 按鍵帶的 Caps Lock 狀態要不要告訴引擎（#173）：跟引擎記得的不同、而且 capsLockEnglish 時才送 capsLock(on)。
    bool capsNeedsSync(bool on, bool spawn) { return on != capsLock_ && capsLockEnglish(spawn); }
    Snapshot capsLock(bool on) { return handle({{"type", "caps_lock"}, {"on", on}}); }
    // 引擎能即時切換「輸出簡體字」（open 回覆的 output_simplified_live，#169）。不連線，只看這個 session 開啟時的回覆；
    // 還沒開 session 時為 false，呼叫端先 ensureLive。
    bool simplifiedLive() const;
    // 「用快捷鍵切換簡體字」開著、引擎也能即時切換（open 回覆的 simplified_hotkey 與 output_simplified_live，#192）。
    // 只在按下那個鍵時問：設定改了一定會重啟引擎，所以先看連線還在不在（不送請求），斷了就重連、重開 session 拿新的值，
    // 剛在設定程式打開或關掉後第一次按就照新設定。引擎沒在執行時照常啟動（使用者自己按的快捷鍵）；舊引擎沒有這一項時為 false。
    bool simplifiedHotkey();
    // 連上引擎、開好 session 後回報 simplifiedLive；引擎不可用時 false。
    bool ensureLive() { return client_.connect() && ensureOpen() && simplifiedLive(); }
    // 開啟 open 回覆裡引擎當時的「輸出簡體字」（output_simplified）；之後以快照的 outputSimplified 為準。
    bool simplifiedAtOpen() const;
    // 切換「輸出簡體字」（#169）：引擎立即改變之後送出的文字並寫進設定檔，組字不動。這是全域設定，不記進重播紀錄。
    Snapshot outputSimplified(bool on) { return handle({{"type", "output_simplified"}, {"on", on}}); }
    bool englishPending() const { return englishPending_; }
    void setPrivate(bool value);
    void traceEvent(const std::string &event, nlohmann::json detail = {});
    void presented(uint64_t sequence, int64_t duration);

private:
    bool ensureOpen();
    Snapshot parse(const nlohmann::json &reply);
    uint64_t traceRequest(const nlohmann::json &request, const nlohmann::json &after,
                          int64_t started, int attempts, unsigned generationBefore, int64_t engineUS = -1);

    bool documentContextEnabled_ = false;
    Client &client_;
    Trace *trace_;
    uint64_t traceID_;
    bool private_ = false, paused_ = false, tainted_ = false, composing_ = false;
    nlohmann::json previous_ = nlohmann::json::object();
    // 僅供斷線復原比對，與 trace 開關無關；同組字重播日誌一起清除。
    nlohmann::json replayView_, recovered_;
    nlohmann::json serverInfo_ = nlohmann::json::object();
    uint64_t id_ = 0;
    unsigned gen_ = 0;
    bool englishPending_ = false;
    bool toggled_ = false;            // 上一個操作是成功的切換；見 toggle()
    bool english_ = false;            // 最近快照的模式；有組字時連同模式重播
    bool capsLock_ = false;           // 最近快照的 caps_lock（#173）
    std::vector<nlohmann::json> log_; // 這次組字以來的請求（提交或放棄後清空）
    std::vector<nlohmann::json> cleared_; // 切到英文清空組字前的日誌；只給緊接著的放回切換接續（#90）
};

} // namespace zuin
