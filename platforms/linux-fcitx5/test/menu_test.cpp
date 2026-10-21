// 選單狀態解析與文字：不需要 Fcitx。
#include "../src/menu.h"

#include <cstdio>
#include <cstdlib>
#include <fstream>
#include <string>
#include <sys/stat.h>
#include <chrono>
#include <filesystem>
#include <thread>
#include <sys/wait.h>
#include <unistd.h>
#include <signal.h>

static int failures = 0;
#define EXPECT(cond) do { if (!(cond)) { std::fprintf(stderr, "FAIL %s:%d %s\n", __FILE__, __LINE__, #cond); ++failures; } } while (0)

int main() {
    auto idle = zuin::parseStatus(R"({"installed":"0.1.0-dev.18","engine":"0.1.0-dev.19","mode":"auto","stage":"active","last_success":"2026-10-03T00:00:00Z",
        "settings":{"selection":"statistical","punctuation":false,"document_context":true}})");
    EXPECT(idle.ok && idle.current() == "0.1.0-dev.19");
    EXPECT(zuin::statusLabel(idle) == "靈犀注音 0.1.0-dev.19 · 已是最新");
    EXPECT(zuin::statusCommand(idle) == (std::vector<std::string>{"auto", "-now", "-notify"}));
    EXPECT(idle.selection == "statistical" && !idle.punctuation && idle.documentContext);
    auto unchecked = zuin::parseStatus(R"({"installed":"0.1.0-dev.41","stage":"idle"})");
    EXPECT(!unchecked.punctuation && unchecked.documentContext && unchecked.selection == "neural");
    EXPECT(zuin::statusLabel(unchecked) == "靈犀注音 0.1.0-dev.41 · 尚未檢查更新");
    auto offline = zuin::parseStatus(R"({"installed":"0.1.0-dev.41","stage":"idle","last_success":"2026-10-03T00:00:00Z","last_error":"connection refused"})");
    EXPECT(zuin::statusLabel(offline) == "靈犀注音 0.1.0-dev.41 · 更新查詢失敗，稍後再試");

    auto consent = zuin::parseStatus(R"({"installed":"0.1.0-dev.18","mode":"ask","stage":"prepared",
        "target":{"version":"0.1.0-dev.19","kind":"core"},"blocked":{"reason":"needs_consent","detail":"x"},"action":"finish"})");
    EXPECT(zuin::statusLabel(consent) == "有新版本 0.1.0-dev.19 — 立即安裝");
    EXPECT(zuin::statusCommand(consent) == (std::vector<std::string>{"finish", "-notify"}));

    auto relogin = zuin::parseStatus(R"({"stage":"pending","target":{"version":"0.1.0-dev.19"},
        "blocked":{"reason":"needs_relogin","detail":"登出再登入時自動完成安裝"}})");
    EXPECT(zuin::statusLabel(relogin) == "0.1.0-dev.19：登出再登入時自動完成安裝");
    EXPECT(zuin::statusCommand(relogin)[0] == "auto");

    auto paused = zuin::parseStatus(R"({"installed":"0.1.0-dev.18","paused_until":"2026-10-06T10:00:00Z"})");
    EXPECT(zuin::statusLabel(paused) == "自動更新暫停至 2026-10-06");

    EXPECT(!zuin::parseStatus("not json").ok);
    EXPECT(!zuin::parseStatus("not json").punctuation);
    EXPECT(zuin::statusLabel(zuin::parseStatus("")) == "靈犀注音：無法讀取更新狀態");
    auto fallback = zuin::parseStatus(R"({"settings":{"selection":"neural","punctuation":true},
        "runtime":{"state":"running","selection_state":"fallback","punctuation_state":"ready"}})");
    EXPECT(fallback.selection == "neural" && fallback.punctuation);
    EXPECT(fallback.runtime == "running" && fallback.selectionState == "fallback" && fallback.punctuationState == "ready");
    // 「我的詞彙…」（#125）：只看 features.vocab 的布林值；舊版 0c-update 沒有 features 時不顯示。
    EXPECT(!idle.vocab && !zuin::parseStatus("").vocab);
    EXPECT(!zuin::parseStatus(R"({"features":{"vocab":false}})").vocab);
    EXPECT(!zuin::parseStatus(R"({"features":{"vocab":"true"}})").vocab);
    EXPECT(!zuin::parseStatus(R"({"features":true})").vocab);
    EXPECT(zuin::parseStatus(R"({"installed":"x","features":{"vocab":true}})").vocab);
    EXPECT(zuin::settingsCommand(false) == (std::vector<std::string>{"open-settings"}));
    EXPECT(zuin::settingsCommand(true) == (std::vector<std::string>{"open-settings", "vocab"}));
    EXPECT(std::string(zuin::vocabLabel) == "我的詞彙…");
    // 「輸出簡體字」（#169）：0.2.1 以前的 0c-update 沒有 settings.output_simplified，不顯示；引擎能即時切換時
    // 以 runtime 的值（引擎目前的狀態）為準，否則看設定檔。舊引擎退回的命令寫設定並交接重啟。
    EXPECT(!idle.simplifiedKnown && !idle.outputSimplified && !idle.simplifiedLive);
    auto stored = zuin::parseStatus(R"({"settings":{"output_simplified":true},"runtime":{"state":"running"}})");
    EXPECT(stored.simplifiedKnown && stored.outputSimplified && !stored.simplifiedLive);
    auto live = zuin::parseStatus(R"({"settings":{"output_simplified":false},"runtime":{"state":"running","output_simplified_live":true,"output_simplified":true}})");
    EXPECT(live.simplifiedKnown && live.outputSimplified && live.simplifiedLive);
    auto odd = zuin::parseStatus(R"({"settings":{"output_simplified":"yes"},"runtime":{"output_simplified_live":"true","output_simplified":true}})");
    EXPECT(!odd.simplifiedKnown && !odd.simplifiedLive);
    EXPECT(std::string(zuin::simplifiedLabel) == "輸出簡體字");
    EXPECT(zuin::simplifiedCommand(true) == (std::vector<std::string>{"set", "output-simplified", "on", "-apply", "-notify"}));
    EXPECT(zuin::simplifiedCommand(false)[2] == "off");
    // 由系統套件管理員更新（#181）：選單不放更新列；舊版 0c-update 沒有 updates 欄位，照舊顯示。
    EXPECT(!idle.packageManaged);
    EXPECT(!zuin::parseStatus(R"({"updates":"self"})").packageManaged);
    EXPECT(zuin::parseStatus(R"({"installed":"0.2.3","updates":"package-manager"})").packageManaged);
    // 以假的 0c-update 驗證參數傳遞、輸出讀取與逾時。
    char dir[] = "/tmp/zuin-menu-XXXXXX";
    if (!mkdtemp(dir)) { return 1; }
    setenv("HOME", dir, 1);
    std::string bin = std::string(dir) + "/.local/share/zuin/bin";
    if (std::system(("mkdir -p " + bin).c_str()) != 0) { return 1; }
    std::ofstream(bin + "/0c-update") << "#!/bin/sh\nif [ \"$1\" = slow ]; then sleep 5; fi\necho \"$@\"\n";
    chmod((bin + "/0c-update").c_str(), 0755);
    std::string out;
    EXPECT(zuin::runUpdater({"status", "--json"}, &out, 5) && out == "status --json\n");
    auto started = std::chrono::steady_clock::now();
    EXPECT(!zuin::runUpdater({"slow"}, &out, 1));
    EXPECT(std::chrono::steady_clock::now() - started < std::chrono::seconds(3));
    // 真正重現 Fcitx 交接：更新工具開始後，結束持有輸出讀端的外殼。
    // 後續輸出不能讓更新器收到 SIGPIPE，也不能中斷安裝後的還原。
    const std::string ready = std::string(dir) + "/ready";
    const std::string finished = std::string(dir) + "/finished";
    {
        std::ofstream tool(std::string(dir) + "/.local/share/zuin/bin/0c-update");
        tool << "#!/bin/sh\ntouch " << ready << "\nsleep 0.3\nprintf 'after shell exit\\n'\ntouch " << finished << "\n";
    }
    const auto caller = ::fork();
    if (caller == 0) {
        zuin::runUpdater({"finish"}, nullptr, 5);
        _exit(0);
    }
    EXPECT(caller > 0);
    if (caller > 0) {
        auto until = std::chrono::steady_clock::now() + std::chrono::seconds(3);
        while (!std::filesystem::exists(ready) && std::chrono::steady_clock::now() < until) {
            std::this_thread::sleep_for(std::chrono::milliseconds(10));
        }
        EXPECT(std::filesystem::exists(ready));
        ::kill(caller, SIGTERM);
        ::waitpid(caller, nullptr, 0);
        while (!std::filesystem::exists(finished) && std::chrono::steady_clock::now() < until) {
            std::this_thread::sleep_for(std::chrono::milliseconds(10));
        }
        EXPECT(std::filesystem::exists(finished));
    }
    if (std::system((std::string("rm -rf ") + dir).c_str()) != 0) { ++failures; }
    if (failures == 0) { std::puts("menu_test PASS"); }
    return failures ? 1 : 0;
}
