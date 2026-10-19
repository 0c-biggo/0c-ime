// 按鍵名稱的轉換中不需要 Fcitx 的部分。
#pragma once

#include <string>

namespace zuin {

// vocabKey 把按著 Shift 的 ←→、Delete、Backspace 換成 0.2.0 的新鍵名（#123）：組字中 Shift＋←／→ 標示要加入詞庫的範圍，
// 候選窗的 Shift＋Delete／⌫ 刪除自訂詞或忘記學到的詞。key 是不看 Shift 的原本鍵名（toKey 的結果）；
// shiftOnly 是只按著 Shift（沒有 Ctrl／Alt／Super）；supported() 回報引擎是否認得新鍵名（open 回覆的 vocab_keys），
// 只在真的要換名時才問，其他按鍵不多一次連線。
// 舊引擎不認得新鍵名，沒回報支援時照舊送原本的鍵；新引擎在沒有對應功能時把新鍵名當原本的鍵處理，
// 所以外殼不必追蹤組字或候選窗的狀態。
template <class Supported>
std::string vocabKey(const std::string &key, bool shiftOnly, Supported &&supported) {
    if (!shiftOnly) { return key; }
    const char *renamed = key == "Left" ? "ShiftLeft" : key == "Right" ? "ShiftRight"
                        : key == "Delete" ? "ShiftDelete" : key == "Backspace" ? "ShiftBackspace" : nullptr;
    if (!renamed || !supported()) { return key; }
    return renamed;
}

} // namespace zuin
