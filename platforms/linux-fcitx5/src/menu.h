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
    // 「輸出簡體字」（#169）：settings.output_simplified 是設定檔的值（0.2.1 起的 0c-update 才有，沒有時 simplifiedKnown 為 false）；
    // runtime.output_simplified_live 時以 runtime.output_simplified（引擎目前的值）為準。
    bool simplifiedKnown = false, outputSimplified = false, simplifiedLive = false;
    // updates 是 package-manager（系統套件，#181）：由套件管理員更新，選單不放更新列。
    bool packageManaged = false;
    std::string current() const { return engine.empty() ? installed : engine; }
};

UpdateStatus parseStatus(const std::string &json);
// parseStatus 再加上依檔案判斷的套件管理員模式（locate.h 的 packageManaged）：json 為空（查詢失敗、逾時）時
// 更新列也不會又出現。
UpdateStatus readStatus(const std::string &json);
// 更新列：目前版本或待辦。
std::string statusLabel(const UpdateStatus &s);
// 點更新操作列要執行的 0c-update 參數：有待辦時完成它，否則立即檢查。
std::vector<std::string> statusCommand(const UpdateStatus &s);
// 「⚙ 靈犀注音設定」下面的「我的詞彙…」（#125）：只在 features.vocab 為真時出現，點了開設定程式的 vocab 頁。
inline const char *vocabLabel = "我的詞彙…";
std::vector<std::string> settingsCommand(bool vocab);
// 狀態區可以打勾的「輸出簡體字」（#169）。引擎能即時切換時外殼直接送請求；舊引擎退回這個命令（寫設定、交接重啟套用）。
inline const char *simplifiedLabel = "輸出簡體字";
std::vector<std::string> simplifiedCommand(bool on);
// 依共用規則（locate.h，#181）：排程使用的穩定 0c-update、安裝器放在 ~/.local/bin 的版本，再來是系統安裝。
std::string updaterPath();
// 執行 0c-update 並取得標準輸出；逾時或失敗回傳 false。會阻塞，請在背景執行緒呼叫。
bool runUpdater(const std::vector<std::string> &args, std::string *output, int timeoutSeconds);

} // namespace zuin
