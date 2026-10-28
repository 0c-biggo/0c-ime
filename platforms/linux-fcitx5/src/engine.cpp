// Zuin 的 Fcitx 5 輸入法引擎：把 Fcitx 的按鍵送給 0c-server，把回來的快照畫成組字區與候選窗。
// 所有輸入法行為都在核心（docs/19 原則 1）；這裡只做轉換與顯示。
#include <algorithm>
#include <chrono>
#include <dlfcn.h>
#include <memory>
#include <cstdlib>
#include <string>
#include <thread>
#include <vector>

#include <fcitx-config/configuration.h>
#include <fcitx-config/option.h>
#include <fcitx-utils/eventdispatcher.h>
#include <fcitx-utils/event.h>
#include <fcitx-utils/key.h>
#include <fcitx-utils/log.h>
#include <fcitx/action.h>
#include <fcitx/addonfactory.h>
#include <fcitx/addonmanager.h>
#include <fcitx/candidatelist.h>
#include <fcitx/inputcontext.h>
#include <fcitx/inputcontextmanager.h>
#include <fcitx/inputcontextproperty.h>
#include <fcitx/inputmethodengine.h>
#include <fcitx/inputpanel.h>
#include <fcitx/instance.h>
#include <fcitx/statusarea.h>
#include <fcitx/userinterfacemanager.h>
#include <fcitx/text.h>
#include <fcitx/userinterface.h>

#include "keys.h"
#include "menu.h"
#include "panel.h"
#include "update_guard.h"
#include "session.h"
#include "shift.h"
#include "context.h"

namespace {

FCITX_DEFINE_LOG_CATEGORY(zuin_log, "zuin");
#define ZUIN_WARN() FCITX_LOGC(zuin_log, Warn)

FCITX_CONFIGURATION(
    InputSettings,
    fcitx::Option<bool> neural{this, "Neural", "啟用神經選字", true};
    fcitx::Option<bool> punctuation{this, "Punctuation", "提交時補標點（Beta）", false};
    fcitx::Option<bool> context{this, "DocumentContext", "依前文選字", true};
);

class ZuinEngine;

// ZuinState 是每個輸入欄位的狀態：一個 0c-server 上的 session。
class ZuinState : public fcitx::InputContextProperty {
public:
    ZuinState(zuin::Client &client, zuin::Trace &trace, bool contextEnabled) : session(client, &trace, contextEnabled) {}
    zuin::Session session;
    zuin::ShiftTap rightShift, leftShift;
    bool leftShiftOwned = false; // 這次左 Shift 按下時設定為開啟，由外殼處理到放開為止
    int leftShiftTapMs = 300;    // 這次左 Shift 按下時引擎回報的單按門檻
    zuin::DocumentContext context;
    bool active = false;
    bool shownEnglish = false; // 狀態區（注／英圖示）最近一次畫的模式
};

// Fcitx 5.1.9 才有候選附註 CandidateWord::setComment；Ubuntu 24.04 的 5.1.7 沒有，直接連結會讓整個外殼載入失敗。
// 執行時找符號（非虛擬成員函式在 Itanium ABI 等同第一個參數是 this 的函式），找不到回傳 nullptr。
using SetComment = void (*)(fcitx::CandidateWord *, fcitx::Text);
SetComment setCommentFunction() {
    static const auto fn = [] {
        auto *found = reinterpret_cast<SetComment>(dlsym(RTLD_DEFAULT, "_ZN5fcitx13CandidateWord10setCommentENS_4TextE"));
        // 5.1.9 以後還查不到表示簽章變了，附註會退回接在候選文字後；記一次，讓這種退化看得出來。
        if (!found) { FCITX_LOGC(zuin_log, Info) << "Fcitx 沒有 CandidateWord::setComment，候選附註接在文字後面顯示"; }
        return found;
    }();
    return fn;
}

// Instance::showCustomInputMethodInformation 是 Fcitx 5.1.11 才有的（Ubuntu 24.04 的 5.1.7 沒有）；同 setCommentFunction 在執行時找，
// 找不到回傳 nullptr，切換提示退回 showInputMethodInformation（顯示子模式的標籤「注簡」）。
using ShowCustomInformation = void (*)(fcitx::Instance *, fcitx::InputContext *, const std::string &);
ShowCustomInformation showCustomInformationFunction() {
    static const auto fn = reinterpret_cast<ShowCustomInformation>(dlsym(RTLD_DEFAULT,
        "_ZN5fcitx8Instance32showCustomInputMethodInformationEPNS_12InputContextERKNSt7__cxx1112basic_stringIcSt11char_traitsIcESaIcEEE"));
    return fn;
}

class ZuinCandidate : public fcitx::CandidateWord {
public:
    // note 是低調附註（模糊音近音為「近音 ㄓ」），以 Fcitx 的 comment 顯示在候選之後；空字串不顯示。
    // 沒有 comment 的舊版 Fcitx 把附註接在候選文字後面，與新版 textWithComment 的顯示相同；選字以 id 提交，不受影響。
    ZuinCandidate(ZuinEngine *engine, const std::string &text, int id, const std::string &note = "")
        : fcitx::CandidateWord(fcitx::Text(text)), engine_(engine), id_(id) {
        if (note.empty()) { return; }
        if (auto *set = setCommentFunction()) { set(this, fcitx::Text(note)); }
        else { setText(fcitx::Text(text + " " + note)); }
    }
    void select(fcitx::InputContext *ic) const override;

private:
    ZuinEngine *engine_;
    int id_;
};

// toKey 把 Fcitx 的按鍵轉成協定的 Key；不該交給輸入法的按鍵回傳空字串。
std::string toKey(const fcitx::Key &key) {
    // 帶 Ctrl、Alt、Super 的組合鍵一律交還應用程式
    if (key.states().testAny(fcitx::KeyStates{fcitx::KeyState::Ctrl, fcitx::KeyState::Alt, fcitx::KeyState::Super})) {
        return "";
    }
    switch (key.sym()) {
    case FcitxKey_Return:
    case FcitxKey_KP_Enter:
        return "Enter";
    case FcitxKey_Escape:
        return "Escape";
    case FcitxKey_BackSpace:
        return "Backspace";
	case FcitxKey_Delete:
		return "Delete";
    case FcitxKey_Tab: // Shift+Tab 送 BackTab：組字中核心吃掉不動作，沒在組字時交還應用程式（#106）
        if (key.states().testAny(fcitx::KeyStates{fcitx::KeyState::Shift})) { return "BackTab"; }
        return "Tab";
    case FcitxKey_ISO_Left_Tab:
        return "BackTab";
    case FcitxKey_Left:
        return "Left";
    case FcitxKey_Right:
        return "Right";
    case FcitxKey_Home: // 組字中移到組字區最前；沒在組字時核心交還應用程式（#106）
    case FcitxKey_KP_Home:
        return "Home";
    case FcitxKey_End:
    case FcitxKey_KP_End:
        return "End";
    case FcitxKey_Up:
        return "Up";
    case FcitxKey_Down:
        return "Down";
    case FcitxKey_Page_Down:
        return "PageDown";
    case FcitxKey_Page_Up:
        return "PageUp";
    case FcitxKey_space:
        return " ";
    default:
        break;
    }
    uint32_t uc = fcitx::Key::keySymToUnicode(key.sym());
    if (uc < 0x21 || uc > 0x7e) { // 只把可見的 ASCII 交給核心；排列由核心解讀
        return "";
    }
    // 數字鍵盤（NumLock 開啟時的 KP_0–9、KP_Multiply…KP_Divide、KP_Equal）送 "KP" 加上字元，核心不當注音鍵（#75）。
    auto sym = key.sym();
    if ((sym >= FcitxKey_KP_Multiply && sym <= FcitxKey_KP_9) || sym == FcitxKey_KP_Equal) {
        return "KP" + std::string(1, static_cast<char>(uc));
    }
    return std::string(1, static_cast<char>(uc));
}

fcitx::TextFormatFlags styleOf(const zuin::PreeditStyle &look) {
    using F = fcitx::TextFormatFlag;
    fcitx::TextFormatFlags flags;
    if (look.underline) { flags |= F::Underline; }
    if (look.bold) { flags |= F::Bold; }
    if (look.italic) { flags |= F::Italic; }
    if (look.highlight) { flags |= F::HighLight; }
    return flags;
}

class ZuinEngine : public fcitx::InputMethodEngineV2 {
public:
    explicit ZuinEngine(fcitx::Instance *instance)
        : instance_(instance), trace_(zuin::defaultTraceDirectory()),
          // 預設確認自己剛提交的文字供下一段選字（神經選字的必要條件）；使用者以 0c-update set
          // document-context off 關閉，ZUIN_DOCUMENT_CONTEXT=0 只供排錯時讓外殼完全不追蹤。
          documentContextEnabled_(!(std::getenv("ZUIN_DOCUMENT_CONTEXT") && std::string(std::getenv("ZUIN_DOCUMENT_CONTEXT")) == "0")),
          factory_([this](fcitx::InputContext &) { return new ZuinState(client_, trace_, documentContextEnabled_); }) {
        instance_->inputContextManager().registerProperty("zuinState", &factory_);
        surroundingWatcher_ = instance_->watchEvent(fcitx::EventType::InputContextSurroundingTextUpdated,
            fcitx::EventWatcherPhase::Default, [this](fcitx::Event &event) {
                auto *ic = static_cast<fcitx::InputContextEvent &>(event).inputContext();
                auto *s = state(ic);
                if (s->active && s->session.contextAllowed()) {
                    bool tracked = s->context.tracked(), confirmed = s->context.confirmed();
                    s->context.observed(document(ic));
                    if (tracked && !s->context.tracked()) { s->session.traceEvent("document_context", {{"state", "invalidated"}, {"reason", "document_update_mismatch"}}); }
                    else if (!confirmed && s->context.confirmed()) { s->session.traceEvent("document_context", {{"state", "confirmed"}}); }
                }
                else { s->context.clear(); }
            });
        try { updateGuard_ = std::make_unique<zuin::UpdateGuard>(instance_); }
        catch (const std::exception &e) { ZUIN_WARN() << "更新交接入口不可用：" << e.what(); }
        dispatcher_->attach(&instance_->eventLoop());
        // 左 Shift 在 Fcitx5 預設是「暫時切換輸入法」（AltTriggerKeys），框架在 InputMethod 階段就攔下，
        // 輸入法收不到。設定開啟時在更早的 PreInputMethod 階段接手；關閉時完全不碰，照 Fcitx5 原本的用途。
        leftShiftWatcher_ = instance_->watchEvent(fcitx::EventType::InputContextKeyEvent,
            fcitx::EventWatcherPhase::PreInputMethod, [this](fcitx::Event &event) { leftShiftKey(static_cast<fcitx::KeyEvent &>(event)); });
        setupMenu();
    }

    ~ZuinEngine() override {
        *alive_ = false;
        dispatcher_->detach();
    }

    const fcitx::Configuration *getConfig() const override { return &inputSettings_; }
    const fcitx::Configuration *getConfigForInputMethod(const fcitx::InputMethodEntry &) const override {
        return &inputSettings_;
    }
    void setConfig(const fcitx::RawConfig &config) override {
        if (!update_.ok) { runUpdater({"status"}); return; }
        inputSettings_.load(config, true);
        runUpdater({"set", "selection", *inputSettings_.neural ? "neural" : "statistical"},
            {{"set", "punctuation", *inputSettings_.punctuation ? "on" : "off"},
             {"set", "document-context", *inputSettings_.context ? "on" : "off", "-apply", "-notify"}});
    }
    void setConfigForInputMethod(const fcitx::InputMethodEntry &, const fcitx::RawConfig &config) override {
        setConfig(config);
    }

    void keyEvent(const fcitx::InputMethodEntry &, fcitx::KeyEvent &event) override {
        auto *ic = event.inputContext();
        auto *s = state(ic);
        auto &session = s->session;
        // 左 Shift 還按著（左右同時按）不算單按，同 IBus 外殼（#151）。
        bool toggle = s->rightShift.key(event.key().sym() == FcitxKey_Shift_R, event.isRelease(),
                                        modified(event) || s->leftShift.down(), nowMs());
        // 左 Shift 預設交給 Fcitx5（設定開啟時由 leftShiftKey 先處理）；右 Shift 單按才切換 Zuin 的注／英。
        bool shift = event.key().sym() == FcitxKey_Shift_L || event.key().sym() == FcitxKey_Shift_R;
        if (shift) {
            session.traceEvent(event.isRelease() ? "shift_release" : "shift_press",
                               {{"side", event.key().sym() == FcitxKey_Shift_L ? "left" : "right"}});
            // 確定是單按的放開才問引擎（Shift＋字母不查詢）：中英切換鍵不含右 Shift（toggle-key 為 left／none）時
            // 照系統原本的用途交給應用程式；按住超過門檻（預設 0.3 秒）不算切換（#117）。
            if (toggle) {
                auto keys = session.shiftKeys();
                toggle = keys.right && s->rightShift.within(keys.tapMs);
            }
            // 切換鍵的按下與放開都交給應用程式，切換了也不吃掉放開（同 Fcitx5 的 AltTriggerKeys、RIME、ibus-chewing）：
            // GNOME Wayland 經 IBus 介面收鍵的 Chromium／Electron（VS Code）只看按鍵事件，收到按下卻收不到放開，
            // 會以為 Shift 一直按著，之後的 ↑ 變成 Shift＋↑（#165）。
            if (toggle) { toggleMode(ic); }
            return;
        }
        if (event.isRelease()) { return; }
        // 用快捷鍵切換簡體字（#192）：設定開著（引擎回報 simplified_hotkey）時吃掉這個鍵，跟選單的「輸出簡體字」一樣即時切換：
        // 組字不動、跳出「已切換為…」提示、標籤跟著變，之後送出的字照新設定。設定關閉或舊引擎時照一般快捷鍵交給應用程式。
        {
            const auto states = event.key().states();
            if (zuin::simplifiedHotkey(static_cast<uint32_t>(event.key().sym()), states.test(fcitx::KeyState::Ctrl), states.test(fcitx::KeyState::Shift),
                    states.testAny(fcitx::KeyStates{fcitx::KeyState::Alt, fcitx::KeyState::Super, fcitx::KeyState::Hyper})) &&
                session.simplifiedHotkey()) {
                session.bypassed();
                toggleSimplified(ic);
                event.filterAndAccept();
                return;
            }
        }
        // Caps Lock 開著時直接打英文（#173）：按鍵事件帶的是按下之前的鎖定狀態（同 RIME 的 ascii_composer），Caps Lock 鍵本身
        // 要反過來算；其他按鍵照它帶的狀態（rawKey，正規化會拿掉 CapsLock），猜錯時下一個按鍵就改正。不讀 LED（Wayland 讀不到）。
        // 開啟時引擎先送出組字、切到英文；關閉時回到原本的模式。設定關閉或舊引擎時不送，照 #168 不理 Caps Lock。
        {
            const bool capsKey = event.key().sym() == FcitxKey_Caps_Lock;
            const bool locked = event.rawKey().states().test(fcitx::KeyState::CapsLock);
            const bool on = capsKey ? !locked : locked;
            if (session.capsNeedsSync(on, !capsKey)) {
                s->context.clear(); syncContext(ic);
                const bool before = s->shownEnglish;
                auto snap = session.capsLock(on);
                render(ic, snap);
                // 內部模式切換不會觸發框架的輸入法切換提示（同 toggleMode）；Caps Lock 鍵本身切換時主動顯示。
                if (snap.ok && capsKey && before != session.english()) { instance_->showInputMethodInformation(ic); }
            }
        }
        fcitx::Key typed = event.key();
        const auto raw = event.rawKey().states();
        const bool lower = session.english() && raw.test(fcitx::KeyState::CapsLock) && session.styleCapsLower();
        const auto folded = zuin::foldStyleCaps(typed.sym(), raw.test(fcitx::KeyState::CapsLock),
                                               raw.test(fcitx::KeyState::Shift), session.english(), lower);
        if (folded != static_cast<uint32_t>(typed.sym())) { typed = fcitx::Key(static_cast<fcitx::KeySym>(folded), typed.states()); }
        if (!session.english()) {
            // Ctrl＋標點打全形（#168）：只在中文模式、引擎回報 ctrl_punctuation（認得鍵名而且設定開著）時送；
            // 引擎沒處理（設定剛關掉、模式不同步）就照下面的一般快捷鍵先送出組字再交給應用程式。
            auto ctrlKey = zuin::ctrlPunctKey(fcitx::Key::keySymToUnicode(typed.sym()), typed.states().test(fcitx::KeyState::Ctrl),
                typed.states().testAny(fcitx::KeyStates{fcitx::KeyState::Alt, fcitx::KeyState::Super, fcitx::KeyState::Hyper}));
            // Ctrl+; 是 Fcitx5 剪貼簿外掛的預設熱鍵。剪貼簿在輸入法之後（PostInputMethod）才看這個鍵，輸入法吃掉就叫不出剪貼簿，
            // 所以剪貼簿外掛載入時照舊放行，不搶走使用者原本就有的功能（#168）。
            if (ctrlKey == "Ctrl+;" && instance_->addonManager().addon("clipboard")) { ctrlKey.clear(); }
            if (!ctrlKey.empty() && session.ctrlPunct(ctrlKey)) {
                syncContext(ic);
                zuin::Snapshot snap = session.key(ctrlKey);
                if (snap.ok && snap.handled) {
                    render(ic, snap);
                    event.filterAndAccept();
                    return;
                }
            }
        }
        std::string key = toKey(typed);
        if (key.empty()) {
            session.bypassed();
            std::string boundary;
            switch (event.key().sym()) {
            case FcitxKey_Home: case FcitxKey_KP_Home: boundary = "Home"; break;
            case FcitxKey_End: case FcitxKey_KP_End: boundary = "End"; break;
            case FcitxKey_ISO_Left_Tab: boundary = "BackTab"; break;
            case FcitxKey_Tab:
                if (event.key().states().testAny(fcitx::KeyStates{fcitx::KeyState::Shift})) { boundary = "BackTab"; }
                break;
            default: break;
            }
            // 組合鍵（單按修飾鍵本身除外）先送出組字，再交給應用程式（#106）。
            if (modified(event) && !event.key().isModifier()) {
                auto committed = session.commitForShortcut();
                if (committed.ok) { render(ic, committed); }
            }
            auto snap = session.bypassEnglish(boundary);
            if (snap.ok) { render(ic, snap); }
            s->context.clear(); syncContext(ic);
            // 不記應用程式快捷鍵的文字內容；保留原本未支援的導覽鍵，方便檢查組字操作。
            const char *kind = "unmapped_or_modified";
            switch (event.key().sym()) {
            case FcitxKey_Home: kind = "Home"; break;
            case FcitxKey_End: kind = "End"; break;
            case FcitxKey_Delete: kind = "Delete"; break;
            default: break;
            }
            session.traceEvent("bypass", {{"key_class", kind}});
            return;
        }
        // 按著 Shift 的 ←→、Delete、Backspace：引擎回報支援時送 0.2.0 的新鍵名（#123）；toKey 已排除 Ctrl／Alt／Super。
        // Shift 要看 rawKey：Fcitx 正規化按鍵時，有對應字元的鍵（Delete 是 U+007F、BackSpace 是 U+0008）會拿掉 Shift。
        key = zuin::styleKey(key, event.rawKey().states().test(fcitx::KeyState::Shift), [&] { return session.styleShiftSpace(); });
        key = zuin::vocabKey(key, event.rawKey().states().test(fcitx::KeyState::Shift), [&] { return session.vocabKeys(); });
        syncContext(ic);
        zuin::Snapshot snap = session.key(key);
        if (snap.reconnected || !snap.handled || !snap.ok) { s->context.clear(); }
        if (!snap.ok) { // server 不可用：放行按鍵，不得卡住應用程式
            ZUIN_WARN() << "0c-server 不可用，放行按鍵";
            return;
        }
        render(ic, snap); // 先送出提交的文字，再決定是否把按鍵交還應用程式，順序才不會顛倒
        if (snap.handled) {
            event.filterAndAccept();
        }
    }

    std::string subMode(const fcitx::InputMethodEntry &, fcitx::InputContext &ic) override {
        if (state(&ic)->session.english()) { return "英文"; }
        return simplified_ ? "注音・簡體輸出" : "注音"; // #169：送出時轉成簡體
    }

    std::string subModeIconImpl(const fcitx::InputMethodEntry &, fcitx::InputContext &ic) override {
        return state(&ic)->session.english() ? "0c-english" : "0c-zhuyin";
    }

    // GNOME Kimpanel always prefers a nonempty label= hint over the supplied icon.
    // The icon itself contains 注／英; leave the text label empty on both modes.
    // 「輸出簡體字」開著時（#169）注音模式改用文字「注簡」：誤開時從面板看得出來（組字和候選照樣是繁體，送出才轉）。
    // 跟 IBus 外殼同樣兩個字（GNOME 頂列的 IBus 指示只採用不到 3 個字的 symbol），換框架時看到的一樣。
    std::string subModeLabelImpl(const fcitx::InputMethodEntry &, fcitx::InputContext &ic) override {
        if (simplified_ && !state(&ic)->session.english()) { return "注簡"; }
        return {};
    }

    void select(fcitx::InputContext *ic, int id) {
        syncContext(ic);
        render(ic, state(ic)->session.handle({{"type", "select"}, {"index", id}}));
    }

    void activate(const fcitx::InputMethodEntry &entry, fcitx::InputContextEvent &event) override {
        InputMethodEngineV2::activate(entry, event);
        auto *ic = event.inputContext();
        auto *s = state(ic);
        s->rightShift.reset(); s->leftShift.reset(); s->leftShiftOwned = false;
        s->active = true; s->context.clear(); syncContext(ic);
        auto snap = s->session.activate();
        if (snap.ok) { render(ic, snap); }
        placeActions(ic);
        if (std::chrono::steady_clock::now() - refreshed_ > std::chrono::minutes(1)) { runUpdater({}); }
        s->shownEnglish = s->session.english();
        ic->updateUserInterface(fcitx::UserInterfaceComponent::StatusArea);
    }

    // FocusOut 的 client preedit 已由 Fcitx／前端提交；只有切換輸入法或
    // 使用伺服器端組字窗時，由外殼送出提交。核心仍完成組字並清除舊欄位。
    void deactivate(const fcitx::InputMethodEntry &entry, fcitx::InputContextEvent &event) override {
        auto *ic = event.inputContext();
        state(ic)->rightShift.reset(); state(ic)->leftShift.reset(); state(ic)->leftShiftOwned = false;
        state(ic)->active = false; state(ic)->context.clear(); syncContext(ic);
        state(ic)->session.traceEvent("deactivate", {{"event_type", static_cast<int>(event.type())}, {"frontend", ic->frontend()}});
        zuin::Snapshot snap = state(ic)->session.handle({{"type", "commit"}});
        auto started = zuin::Trace::monotonicMicros();
        if (snap.ok && !snap.commit.empty() &&
            (event.type() != fcitx::EventType::InputContextFocusOut ||
             !ic->capabilityFlags().test(fcitx::CapabilityFlag::Preedit))) {
            ic->commitString(snap.commit);
        }
        clear(ic);
        state(ic)->session.presented(snap.traceSequence, zuin::Trace::monotonicMicros() - started);
        InputMethodEngineV2::deactivate(entry, event);
    }

    void reset(const fcitx::InputMethodEntry &, fcitx::InputContextEvent &event) override {
        auto *ic = event.inputContext();
        state(ic)->rightShift.reset(); state(ic)->leftShift.reset(); state(ic)->leftShiftOwned = false;
        state(ic)->context.clear(); syncContext(ic);
        state(ic)->session.traceEvent("reset", {{"event_type", static_cast<int>(event.type())}, {"frontend", ic->frontend()}});
        auto snap = state(ic)->session.handle({{"type", "cancel"}});
        auto started = zuin::Trace::monotonicMicros();
        clear(ic);
        state(ic)->session.presented(snap.traceSequence, zuin::Trace::monotonicMicros() - started);
    }

private:
    static int64_t nowMs() {
        return std::chrono::duration_cast<std::chrono::milliseconds>(std::chrono::steady_clock::now().time_since_epoch()).count();
    }

    static bool modified(const fcitx::KeyEvent &event) {
        return event.key().states().testAny(fcitx::KeyStates{fcitx::KeyState::Ctrl, fcitx::KeyState::Alt, fcitx::KeyState::Super});
    }

    // 切換注／英：保留已組好的字，切到英文時清掉未打完的注音（#90）。引擎不可用時不切換。
    void toggleMode(fcitx::InputContext *ic) {
        auto *s = state(ic);
        s->context.clear(); syncContext(ic);
        auto snap = s->session.toggle();
        if (!snap.ok) { return; }
        render(ic, snap);
        s->shownEnglish = s->session.english();
        ic->updateUserInterface(fcitx::UserInterfaceComponent::StatusArea);
        // 內部模式切換不會觸發框架的輸入法切換提示，需主動顯示。
        instance_->showInputMethodInformation(ic);
    }

    // 設定開啟「左 Shift 切換中英」時，左 Shift 單按與右 Shift 相同。按下與放開都 filter：不給 Fcitx5 的
    // AltTriggerKeys 與輸入法，但照樣送給應用程式，Shift 加字母仍是大寫；切換成功的放開也照樣送出（#165）。
    void leftShiftKey(fcitx::KeyEvent &event) {
        auto *ic = event.inputContext();
        if (instance_->inputMethodEngine(ic) != this) { return; }
        auto *s = state(ic);
        const bool left = event.key().sym() == FcitxKey_Shift_L;
        const bool toggle = s->leftShift.key(left, event.isRelease(), modified(event) || s->rightShift.down(), nowMs());
        if (!left) { return; }
        if (!event.isRelease()) {
            // 長按的自動重複不再查詢；只在第一次按下時問引擎目前的設定。
            if (!s->leftShiftOwned) {
                auto keys = s->session.shiftKeys();
                s->leftShiftOwned = keys.left;
                s->leftShiftTapMs = keys.tapMs;
            }
            if (!s->leftShiftOwned) { return; }
            s->session.traceEvent("shift_press", {{"side", "left"}});
            // 接手的按下被 filter 掉，keyEvent 收不到：替右側記一次「另一個鍵按下」，右 Shift 按著時再按左 Shift
            // 就不算右側的單按（#151）。
            s->rightShift.key(false, false, false, nowMs());
            event.filter();
            return;
        }
        if (!s->leftShiftOwned) { return; }
        s->leftShiftOwned = false;
        s->session.traceEvent("shift_release", {{"side", "left"}});
        // 按住超過門檻（預設 0.3 秒）不算切換；放開照樣不給 Fcitx5，按下時已經接手了（#117）。
        // 切換了也只 filter 不 accept：放開要送到應用程式，否則 GNOME Wayland 的 VS Code 會以為 Shift 沒放開（#165）。
        if (toggle && s->leftShift.within(s->leftShiftTapMs)) { toggleMode(ic); }
        event.filter();
    }

    ZuinState *state(fcitx::InputContext *ic) {
        auto *s = ic->propertyFor(&factory_);
        s->session.setPrivate(ic->capabilityFlags().testAny(fcitx::CapabilityFlag::PasswordOrSensitive));
        if (!s->session.contextAllowed()) { s->context.clear(); }
        return s;
    }

    zuin::DocumentContext::Text document(fcitx::InputContext *ic) {
        if (!documentContextEnabled_ || ic->capabilityFlags().testAny(fcitx::CapabilityFlag::PasswordOrSensitive)) { return {}; }
        const auto &t = ic->surroundingText();
        if (!ic->capabilityFlags().test(fcitx::CapabilityFlag::SurroundingText) || !t.isValid() || t.text().size() > 16384) { return {}; }
        return {t.text(), t.cursor(), t.anchor(), true};
    }
    void syncContext(fcitx::InputContext *ic) {
        auto *s = state(ic);
        bool tracked = s->context.tracked();
        s->context.validate(s->session.contextAllowed() ? document(ic) : zuin::DocumentContext::Text{});
        if (tracked && !s->context.tracked()) { s->session.traceEvent("document_context", {{"state", "invalidated"}, {"reason", "unconfirmed_or_position_changed"}}); }
        if (auto action = s->context.takeAction()) {
            if (!s->session.documentContext(*action)) { s->context.clear(); }
        }
    }

    void clear(fcitx::InputContext *ic) {
        ic->inputPanel().reset();
        ic->updatePreedit();
        ic->updateUserInterface(fcitx::UserInterfaceComponent::InputPanel);
    }

    void render(fcitx::InputContext *ic, const zuin::Snapshot &snap) {
        if (!snap.ok) {
            return;
        }
        auto started = zuin::Trace::monotonicMicros();
        if (snap.reconnected) { state(ic)->context.clear(); }
        if (!snap.commit.empty()) {
            auto *s = state(ic);
            s->context.committed(s->session.contextAllowed() ? document(ic) : zuin::DocumentContext::Text{}, snap.commit, snap.contextID);
            if (s->session.contextAllowed()) { s->session.traceEvent("document_context", {{"state", "offered"}, {"token_available", snap.contextID != 0}, {"tracked", s->context.tracked()}}); }
            ic->commitString(snap.commit);
        }
        auto &panel = ic->inputPanel();
        panel.reset();
        fcitx::Text preedit;
        const auto layout = zuin::preeditLayout(snap.preedit, snap.cursorByte());
        for (const auto &run : layout.runs) {
            auto flags = styleOf(run.style);
            if (run.display) { flags |= fcitx::TextFormatFlag::DontCommit; }
            preedit.append(run.text, flags);
        }
        preedit.setCursor(layout.cursor);
        const bool clientPreedit = ic->capabilityFlags().test(fcitx::CapabilityFlag::Preedit);
        if (clientPreedit) {
            panel.setClientPreedit(preedit);
        } else {
            panel.setPreedit(preedit);
        }
        const auto aux = zuin::panelAux(snap, clientPreedit); // 通知、Tab 補全、候選標題與頁碼
        if (!aux.up.empty()) { panel.setAuxUp(fcitx::Text(aux.up)); }
        if (!aux.down.empty()) { panel.setAuxDown(fcitx::Text(aux.down)); }
        if (!snap.candidates.empty()) {
            auto list = std::make_unique<fcitx::CommonCandidateList>();
            std::vector<std::string> labels;
            for (size_t i = 0; i < snap.candidates.size(); i++) {
                const std::string key = i < snap.candidateKeys.size() ? snap.candidateKeys[i] : std::string();
                labels.push_back(key.empty() ? std::string() : key + ". "); // 選字鍵由核心給（#112）
                list->append<ZuinCandidate>(this, snap.candidates[i].second, snap.candidates[i].first,
                                            i < snap.candidateNotes.size() ? snap.candidateNotes[i] : std::string());
            }
            list->setLabels(labels);
            list->setPageSize(static_cast<int>(snap.candidates.size()));
            list->setLayoutHint(snap.columns > 5 ? fcitx::CandidateLayoutHint::Horizontal : fcitx::CandidateLayoutHint::Vertical);
            list->setGlobalCursorIndex(snap.highlight); // 反白由核心決定（上下鍵移動、Enter 選取）
            panel.setCandidateList(std::move(list));
        }
        ic->updatePreedit();
        ic->updateUserInterface(fcitx::UserInterfaceComponent::InputPanel);
        // 模式不是使用者切換而改變時（開始時是英文的新 session 第一個按鍵、共用模式跟上別的輸入框，#160、#115），
        // 注／英圖示也要跟著換。
        if (state(ic)->shownEnglish != state(ic)->session.english()) {
            state(ic)->shownEnglish = state(ic)->session.english();
            ic->updateUserInterface(fcitx::UserInterfaceComponent::StatusArea);
        }
        // 「輸出簡體字」在設定程式或別的外殼切換後（#169），下一個快照就跟上；只信回報即時切換的引擎（舊引擎沒有這一項）。
        if (state(ic)->session.simplifiedLive()) { setSimplified(snap.outputSimplified, ic); }
        state(ic)->session.presented(snap.traceSequence, zuin::Trace::monotonicMicros() - started);
    }

    fcitx::Instance *instance_;
    zuin::Client client_; // 整個 fcitx5 行程共用一條連線；每個輸入欄位一個 session
    zuin::Trace trace_;
    bool documentContextEnabled_ = false; // 初始化值；連線後依引擎設定更新
    // 輸入法選單只放兩列：「⚙ 靈犀注音設定」開設定程式，第二列是更新狀態（點了檢查更新或完成已準備好的更新）。
    // features.vocab 為真時，兩列中間多一列「我的詞彙…」，開設定程式的 vocab 頁（#125）。
    // 更新模式、暫停、神經選字、補標點、依前文選字與模糊音都在設定程式裡；選單點一下就關，操作結果沒有地方顯示，
    // 以前的子選單因此像是「按了沒反應」。動作一律交給 0c-update，在背景執行緒執行，完成後重新讀取狀態；輸入不會因此等待。
    void setupMenu() {
        auto &ui = instance_->userInterfaceManager();
        openSettings_.setShortText("⚙ 靈犀注音設定");
        ui.registerAction("zuin-settings", &openSettings_);
        ui.registerAction("zuin-status", &status_);
        vocab_.setShortText(zuin::vocabLabel);
        ui.registerAction("zuin-vocab", &vocab_);
        auto on = [this](fcitx::SimpleAction &a, auto args) {
            a.connect<fcitx::SimpleAction::Activated>([this, args](fcitx::InputContext *) { runUpdater(args()); });
        };
        openSettings_.connect<fcitx::SimpleAction::Activated>([this](fcitx::InputContext *) { openSettings(false); });
        vocab_.connect<fcitx::SimpleAction::Activated>([this](fcitx::InputContext *) { openSettings(true); });
        simplifiedAction_.setShortText(zuin::simplifiedLabel);
        simplifiedAction_.setCheckable(true);
        ui.registerAction("zuin-simplified", &simplifiedAction_);
        simplifiedAction_.connect<fcitx::SimpleAction::Activated>([this](fcitx::InputContext *ic) { toggleSimplified(ic); });
        on(status_, [this] { return zuin::statusCommand(update_); });
        update_ = zuin::readStatus(std::string()); // 第一次查詢前：套件管理員模式也不顯示更新列
        applyStatus();
        runUpdater({});
        modelTimer_ = instance_->eventLoop().addTimeEvent(CLOCK_MONOTONIC, fcitx::now(CLOCK_MONOTONIC) + 5000000, 100000,
            [this](fcitx::EventSourceTime *event, uint64_t) {
                // 狀態查詢在背景；沒有重疊查詢，不阻塞按鍵與選單。
                if (!querying_) { runUpdater({}); }
                event->setNextInterval(5000000);
                event->setEnabled(true);
                return true;
            });
    }

    // 開設定程式不排進更新命令的佇列（更新檢查最長 30 分鐘）。設定程式自成一個工作階段，更新器啟動它後立刻返回；
    // 失敗時原因顯示在被點的那一列（點同一列重試），0c-update 也會送桌面通知。vocab 為真時開「我的詞彙」頁。
    void openSettings(bool vocab) {
        if (openingSettings_) { return; }
        openingSettings_ = true;
        settingsFailure_.clear();
        vocabFailure_.clear();
        applyStatus();
        auto alive = alive_;
        auto dispatcher = dispatcher_;
        std::thread([this, alive, dispatcher, vocab] {
            std::string output;
            const bool ok = zuin::runUpdater(zuin::settingsCommand(vocab), &output, 60);
            const std::string failure = ok ? std::string() :
                "開啟失敗：" + (output.empty() ? std::string("無法執行更新工具") : output.substr(0, output.find('\n')));
            dispatcher->schedule([this, alive, failure, vocab] {
                if (!*alive) { return; }
                openingSettings_ = false;
                (vocab ? vocabFailure_ : settingsFailure_) = failure;
                applyStatus();
            });
        }).detach();
    }

    // 依 features.vocab 決定選單有沒有「我的詞彙…」，照「設定、我的詞彙、輸出簡體字、更新」的順序放進輸入法的狀態區。
    // 「輸出簡體字」（#169）在 0c-update 回報這項設定、或引擎能即時切換時出現。
    // 狀態區沒有「插在中間」，所以先拿掉自己的項目再依序加回。
    void placeActions(fcitx::InputContext *ic) {
        auto &area = ic->statusArea();
        for (auto *a : {&openSettings_, &vocab_, &simplifiedAction_, &status_}) {
            if (shows(ic, a)) { area.removeAction(a); }
        }
        area.addAction(fcitx::StatusGroup::InputMethod, &openSettings_);
        if (update_.vocab) { area.addAction(fcitx::StatusGroup::InputMethod, &vocab_); }
        if (simplifiedShown()) { area.addAction(fcitx::StatusGroup::InputMethod, &simplifiedAction_); }
        if (!update_.packageManaged) { area.addAction(fcitx::StatusGroup::InputMethod, &status_); } // 套件管理員更新時沒有更新列（#181）
    }

    bool simplifiedShown() const { return update_.simplifiedKnown || simplifiedLive_; }

    // 「輸出簡體字」的狀態改變：選單打勾、注／英圖示旁的「簡」與子模式文字跟著換。
    void setSimplified(bool on, fcitx::InputContext *ic = nullptr) {
        simplifiedLive_ = simplifiedLive_ || (ic && state(ic)->session.simplifiedLive());
        if (on == simplified_ && simplifiedAction_.isChecked(nullptr) == on) { return; }
        simplified_ = on;
        simplifiedAction_.setChecked(on);
        if (!ic) { ic = instance_->mostRecentInputContext(); }
        if (ic && instance_->inputMethodEngine(ic) == this) {
            if (shows(ic, &openSettings_) && !shows(ic, &simplifiedAction_)) { placeActions(ic); }
            if (shows(ic, &simplifiedAction_)) { simplifiedAction_.update(ic); }
            ic->updateUserInterface(fcitx::UserInterfaceComponent::StatusArea);
        }
    }

    // 選單的「輸出簡體字」（#169）：引擎能即時切換時直接在這個輸入框的 session 送請求（組字不動、之後送出的字立刻照新設定），
    // 引擎寫設定檔；提示用輸入法資訊（Fcitx 5.1.11 起顯示「已切換為輸出簡體字」，較舊的版本顯示標籤「注簡」）。
    // 舊引擎退回 0c-update set output-simplified … -apply -notify（寫設定、交接重啟，0c-update 送桌面通知）。
    void toggleSimplified(fcitx::InputContext *ic) {
        if (!ic) { ic = instance_->mostRecentInputContext(); }
        const bool on = !simplified_;
        if (ic && instance_->inputMethodEngine(ic) == this && state(ic)->session.ensureLive()) {
            auto snap = state(ic)->session.outputSimplified(on);
            if (snap.ok && snap.handled) {
                const std::string notice = snap.notices.empty() ? std::string() : snap.notices.front();
                snap.notices.clear(); // 提示用輸入法資訊顯示，不留在組字面板上
                if (!commandRunning_) { ++querySequence_; querying_ = false; } // 切換前開始的狀態查詢不得把勾選改回去
                render(ic, snap);
                setSimplified(snap.outputSimplified, ic);
                if (auto *show = showCustomInformationFunction(); show && !notice.empty()) { show(instance_, ic, notice); }
                else { instance_->showInputMethodInformation(ic); }
                return;
            }
        }
        runUpdater(zuin::simplifiedCommand(on));
    }

    static bool shows(fcitx::InputContext *ic, const fcitx::Action *action) {
        const auto list = ic->statusArea().actions(fcitx::StatusGroup::InputMethod);
        return std::find(list.begin(), list.end(), action) != list.end();
    }

    // 執行 0c-update（args 為空時只讀狀態），完成後在事件迴圈更新選單。命令一次只跑一個；
    // 更新列顯示更新命令（auto／finish）的進度與失敗原因，點同一列就是重試同一類命令。
    void runUpdater(std::vector<std::string> args, std::vector<std::vector<std::string>> following = {}) {
        if (args.empty() && (querying_ || commandRunning_)) { return; }
        if (!args.empty() && commandRunning_) { return; }
        const bool update = !args.empty() && (args[0] == "auto" || args[0] == "finish");
        if (!args.empty()) {
            commandRunning_ = true;
            pendingCommand_ = args[0];
            if (update) { updateFailure_.clear(); }
            applyStatus();
        }
        querying_ = true;
        const auto query = ++querySequence_;
        refreshed_ = std::chrono::steady_clock::now();
        auto alive = alive_;
        auto dispatcher = dispatcher_;
        std::thread([this, alive, dispatcher, query, update, args = std::move(args), following = std::move(following)] {
            std::string commandOutput;
            bool commandOK = args.empty() || zuin::runUpdater(args, &commandOutput, 30 * 60);
            for (const auto &command : following) {
                if (!commandOK) { break; }
                commandOK = zuin::runUpdater(command, &commandOutput, 30 * 60);
            }
            std::string failure;
            if (!args.empty() && !commandOK) {
                failure = "操作失敗：" + (commandOutput.empty() ? std::string("無法執行更新工具") : commandOutput.substr(0, commandOutput.find('\n')));
            }
            std::string out;
            bool ok = zuin::runUpdater({"status", "--json"}, &out, 20);
            dispatcher->schedule([this, alive, query, out, ok, failure, update, command = !args.empty()] {
                if (!*alive) { return; }
                // 已開始操作時，先前的背景查詢不得覆蓋操作後的設定與勾選。
                if (query != querySequence_) { return; }
                if (command) {
                    commandRunning_ = false;
                    if (update) { updateFailure_ = failure; }
                }
                querying_ = false;
                update_ = zuin::readStatus(ok ? out : std::string()); // 查詢失敗時套件管理員模式照舊（#181）
                applyStatus();
            });
        }).detach();
    }

    void applyStatus() {
        const auto &u = update_;
        if (u.ok) {
            inputSettings_.neural.setValue(u.selection == "neural");
            inputSettings_.punctuation.setValue(u.punctuation);
            inputSettings_.context.setValue(u.documentContext);
        }
        if (u.simplifiedKnown) {
            simplifiedLive_ = simplifiedLive_ || u.simplifiedLive;
            setSimplified(u.outputSimplified);
        }
        const bool fallback = u.selectionState == "fallback" || u.punctuationState == "fallback";
        // 設定列：開啟失敗時顯示原因（點此重試）；否則模型退回時加註。
        std::string settings = "⚙ 靈犀注音設定";
        if (!settingsFailure_.empty()) { settings += "（" + settingsFailure_ + "，點此重試）"; }
        else if (fallback) { settings += "（模型已退回）"; }
        openSettings_.setShortText(settings);
        // 更新列：執行中顯示進度；上一個更新動作失敗時顯示原因（點此重試）；否則是目前的更新狀態。
        std::string row = zuin::statusLabel(u) + (u.action == "finish" ? "（點此完成更新）" : "（點此檢查更新）");
        if (commandRunning_ && pendingCommand_ == "auto") { row = "正在檢查並準備更新"; }
        else if (commandRunning_ && pendingCommand_ == "finish") { row = "正在完成已準備好的更新"; }
        else if (commandRunning_ && pendingCommand_ == "set") { row = "正在套用設定；若有組字請先完成"; }
        else if (!updateFailure_.empty()) { row = updateFailure_ + "（點此重試）"; }
        status_.setShortText(row);
        std::string vocab = zuin::vocabLabel;
        if (!vocabFailure_.empty()) { vocab += "（" + vocabFailure_ + "，點此重試）"; }
        vocab_.setShortText(vocab);
        if (auto *ic = instance_->mostRecentInputContext()) {
            // 只動目前用著靈犀注音的欄位（狀態區裡有我們的設定列）；開關改變時加上或拿掉「我的詞彙…」。
            if (shows(ic, &openSettings_) && (shows(ic, &vocab_) != u.vocab || shows(ic, &simplifiedAction_) != simplifiedShown() ||
                                              shows(ic, &status_) == u.packageManaged)) { placeActions(ic); }
            openSettings_.update(ic);
            if (shows(ic, &status_)) { status_.update(ic); }
            if (shows(ic, &vocab_)) { vocab_.update(ic); }
            ic->updateUserInterface(fcitx::UserInterfaceComponent::StatusArea);
        }
    }

    std::unique_ptr<zuin::UpdateGuard> updateGuard_;
    std::string pendingCommand_;
    InputSettings inputSettings_;
    fcitx::SimpleAction openSettings_, vocab_, status_, simplifiedAction_;
    bool simplified_ = false;     // 「輸出簡體字」目前的狀態（#169）：快照、0c-update status 與選單切換更新
    bool simplifiedLive_ = false; // 引擎能即時切換（看過 output_simplified_live）
    std::unique_ptr<fcitx::EventSourceTime> modelTimer_;
    bool querying_ = false, commandRunning_ = false;
    uint64_t querySequence_ = 0;
    std::string updateFailure_, settingsFailure_, vocabFailure_;
    bool openingSettings_ = false;
    zuin::UpdateStatus update_;
    std::shared_ptr<bool> alive_ = std::make_shared<bool>(true);
    // 背景執行緒把結果送回事件迴圈。Instance::eventDispatcher 是 Fcitx 5.1.9 才有的，自建一個以支援 5.1.7；
    // 執行緒持有共用指標，引擎解構時 detach，之後送來的工作直接丟棄。
    std::shared_ptr<fcitx::EventDispatcher> dispatcher_ = std::make_shared<fcitx::EventDispatcher>();
    std::chrono::steady_clock::time_point refreshed_{};
    fcitx::FactoryFor<ZuinState> factory_;
    std::unique_ptr<fcitx::HandlerTableEntry<fcitx::EventHandler>> surroundingWatcher_, leftShiftWatcher_;
};

void ZuinCandidate::select(fcitx::InputContext *ic) const { engine_->select(ic, id_); }

class ZuinEngineFactory : public fcitx::AddonFactory {
public:
    fcitx::AddonInstance *create(fcitx::AddonManager *manager) override { return new ZuinEngine(manager->instance()); }
};

} // namespace

FCITX_ADDON_FACTORY(ZuinEngineFactory);
