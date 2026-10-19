// 跨提交上下文只在應用程式回報完全相符的插入結果後生效。
#pragma once
#include <cstdint>
#include <optional>
#include <string>

namespace zuin {
class DocumentContext {
public:
    struct Text { std::string text; unsigned cursor = 0, anchor = 0; bool valid = false; };
    void clear() { expected_ = {}; token_ = 0; confirmed_ = false; action_ = uint64_t(0); }
    // 在 commitString 前呼叫；這時核心已清除上一段上下文。
    void committed(const Text &before, const std::string &commit, uint64_t token) {
        expected_ = {}; token_ = 0; confirmed_ = false; action_.reset();
        if (!token || commit.empty() || !usable(before)) { return; }
        size_t byte = 0; unsigned chars = 0;
        while (byte < before.text.size() && chars < before.cursor) {
            ++byte;
            while (byte < before.text.size() && continuation(before.text[byte])) { ++byte; }
            ++chars;
        }
        expected_ = before;
        expected_.text.insert(byte, commit);
        if (expected_.text.size() > maxBytes) { expected_ = {}; return; }
        for (unsigned char c : commit) { if (!continuation(c)) { ++expected_.cursor; } }
        expected_.anchor = expected_.cursor;
        token_ = token;
    }
    // 只由 surrounding-text 更新事件確認；讀取舊快取不能充當提交回覆。
    void observed(const Text &now) {
        if (!token_) { return; }
        if (!matches(now)) { clear(); return; }
        if (!confirmed_) { confirmed_ = true; action_ = token_; }
    }
    // 每次操作再查目前文字／游標／選取。尚未收到更新就開始下一段時放棄。
    void validate(const Text &now) {
        if (token_ && (!confirmed_ || !matches(now))) { clear(); }
    }
    std::optional<uint64_t> takeAction() { auto a = action_; action_.reset(); return a; }
    bool confirmed() const { return confirmed_; }
    bool tracked() const { return token_ != 0; }
private:
    static constexpr size_t maxBytes = 16384;
    static bool continuation(unsigned char c) { return (c & 0xc0) == 0x80; }
    static bool usable(const Text &t) {
        if (!t.valid || t.text.size() > maxBytes || t.cursor != t.anchor) { return false; }
        unsigned n = 0;
        for (unsigned char c : t.text) { if (!continuation(c)) { ++n; } }
        return t.cursor <= n;
    }
    bool matches(const Text &t) const {
        return usable(t) && expected_.valid && t.cursor == expected_.cursor && t.text == expected_.text;
    }
    Text expected_;
    uint64_t token_ = 0;
    bool confirmed_ = false;
    std::optional<uint64_t> action_;
};
} // namespace zuin
