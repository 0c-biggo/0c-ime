// 輸入法選單的更新與設定狀態（#13）：讀 `0c-update status --json`，產生選單文字與要執行的命令。
// 不依賴 Fcitx，方便單元測試；Fcitx 的 action 接線在 engine.cpp。
#pragma once

#include <string>
#include <vector>

namespace zuin {

struct UpdateStatus {
    bool ok = false;
    std::string installed, engine, mode = "auto", stage, target, blocked, detail, action, pausedUntil;
    std::string lastSuccess, lastError;
    std::string selection = "neural";
    std::string runtime, selectionState, punctuationState;
    bool punctuation = false, documentContext = true;
    // features.vocab：0.2.0「我的詞彙」的入口（0.2.0 起恆為 true）。舊版 0c-update 沒有這個欄位，視為關閉。
    bool vocab = false;
    std::string current() const { return engine.empty() ? installed : engine; }
};

UpdateStatus parseStatus(const std::string &json);
// 更新列：目前版本或待辦。
std::string statusLabel(const UpdateStatus &s);
// 點更新操作列要執行的 0c-update 參數：有待辦時完成它，否則立即檢查。
std::vector<std::string> statusCommand(const UpdateStatus &s);
// 「⚙ 靈犀注音設定」下面的「我的詞彙…」（#125）：只在 features.vocab 為真時出現，點了開設定程式的 vocab 頁。
inline const char *vocabLabel = "我的詞彙…";
std::vector<std::string> settingsCommand(bool vocab);
// 排程使用的穩定 0c-update；不存在時退回安裝器放在 ~/.local/bin 的版本。
std::string updaterPath();
// 執行 0c-update 並取得標準輸出；逾時或失敗回傳 false。會阻塞，請在背景執行緒呼叫。
bool runUpdater(const std::vector<std::string> &args, std::string *output, int timeoutSeconds);

} // namespace zuin
