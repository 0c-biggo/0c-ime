// 輸入面板的顯示規則：兩條提示列的內容與組字分段的樣式。不需要 Fcitx。
#pragma once

#include <string>
#include <vector>

#include "session.h"

namespace zuin {

struct PanelAux {
    std::string up, down; // Fcitx 的 auxUp、auxDown
};

// 組字分段的顯示樣式，對應 Fcitx 的 TextFormatFlag。
struct PreeditStyle {
    bool underline = true, bold = false, italic = false, highlight = false;
};

inline PreeditStyle preeditStyle(const std::string &style) {
    PreeditStyle out;
    if (style == "locked") { out.bold = true; }         // 使用者選過的字
    else if (style == "corrected") { out.italic = true; } // 自動修正：以不同樣式標示
    // Shift＋←／→ 標示要加入詞庫的範圍（#123）：反白，與一般組字的底線一眼分得出來。
    // classicui／kimpanel 畫成選取色，GTK／Qt 前端轉成反白的前景與背景色；GTK3 的選取色就是反白色，
    // 跟滑鼠選取只差一條底線，所以範圍前後另加【】（見 preeditLayout）。
    else if (style == "marked") { out.highlight = true; }
    // 按空白會組成中文的數字（reading，#162）：Fcitx5 的組字格式沒有顏色，畫不出灰字，
    // 照一般組字畫底線；讀音畫在提示列（見 panelAux）。
    return out;
}

// 組字的一段。display 為 true 的是只顯示、不屬於組字文字的記號（Fcitx 的 DontCommit：失焦時 Fcitx 提交
// 組字也不會帶出去）。
struct PreeditRun {
    std::string text;
    PreeditStyle style;
    bool display = false;
};

struct PreeditLayout {
    std::vector<PreeditRun> runs;
    int cursor = 0; // 在所有 runs 接起來的文字中的 UTF-8 位元組位置
};

// 組字分段加上加詞標示的【】：Fcitx5 只能要求反白，不能指定顏色，GTK3 把反白畫成程式的選取色，
// 標示看起來就像選取了文字（2026-10-18 使用者在 A/B 截圖中選定括號）。游標照舊停在範圍外側：
// 範圍左端的游標在【之前，右端的在】之後。
// cursor 是游標在原本組字（segments 接起來）中的 UTF-8 位元組位置（Snapshot::cursorByte）。
inline PreeditLayout preeditLayout(const std::vector<Segment> &segments, int cursor) {
    static const std::string open = "【", close = "】";
    PreeditLayout out;
    int at = 0, shift = 0;
    const auto marked = [&](size_t i) { return i < segments.size() && segments[i].style == "marked"; };
    for (size_t i = 0; i < segments.size(); ++i) {
        const auto &g = segments[i];
        if (marked(i) && (i == 0 || !marked(i - 1))) {
            out.runs.push_back({open, PreeditStyle{}, true});
            if (cursor > at) { shift += static_cast<int>(open.size()); }
        }
        out.runs.push_back({g.text, preeditStyle(g.style), false});
        at += static_cast<int>(g.text.size());
        if (marked(i) && !marked(i + 1)) {
            out.runs.push_back({close, PreeditStyle{}, true});
            if (cursor >= at) { shift += static_cast<int>(close.size()); }
        }
    }
    out.cursor = cursor + shift;
    return out;
}

// 提示（通知、Tab 補全、候選標題）與頁碼要放哪一列。Fcitx5 的 classicui 與 kimpanel 都把 auxUp 直接接在面板組字前面，
// 畫成同一列；auxDown 自成一列，在組字之下、候選之上。應用程式自己畫組字（clientPreedit）時面板第一列只有 auxUp，
// 提示照舊放在那裡；組字畫在面板上時提示改放 auxDown，免得與組字擠成「⇥ 側刺 → 冊次側刺」（#136），
// 頁碼則接在同一列後面。
// 通知（例如候選窗的 Shift＋Delete 之後「已從詞庫刪除：…」）是使用者剛做的動作的回饋，只出現在這一次快照，
// 優先於候選標題；標題在下一個按鍵就回來。兩者擠在同一列會太長，kimpanel 也會截斷。
// 讀音提示（中英混打時數字按空白會組成的注音，#162）只有讀音本身（ㄉㄢ），畫在游標旁的提示列，跟常見注音輸入法在數字下方附注音一樣短；
// 通知優先，補全提示在後（兩者跟讀音提示實際上不會同時出現；開候選時核心不送讀音提示）。
inline PanelAux panelAux(const Snapshot &snap, bool clientPreedit) {
    std::string hint;
    if (!snap.notices.empty()) {
        hint = snap.notices.front();
    } else if (!snap.readingHint.empty()) {
        hint = snap.readingHint;
    } else if (!snap.suggestion.empty()) { // Tab 補全：在提示列顯示，不塞進應用程式的組字區
        hint = "⇥ " + snap.suggestion;
    }
    std::string page;
    if (!snap.candidates.empty()) {
        if (snap.notices.empty() && !snap.candidateTitle.empty()) { hint = snap.candidateTitle; }
        if (snap.pages > 1) { page = std::to_string(snap.page + 1) + "/" + std::to_string(snap.pages); }
    }
    if (clientPreedit) { return {hint, page}; }
    if (hint.empty() || page.empty()) { return {"", hint + page}; }
    return {"", hint + "　" + page};
}

} // namespace zuin
