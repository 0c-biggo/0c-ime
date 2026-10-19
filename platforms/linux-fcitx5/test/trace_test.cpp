#include "../src/trace.h"

#include <cassert>
#include <cstdlib>
#include <algorithm>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <vector>
#include <sys/stat.h>
#include <unistd.h>

using nlohmann::json;
namespace fs = std::filesystem;

int main() {
    char tmp[] = "/tmp/0c-trace-test-XXXXXX";
    assert(mkdtemp(tmp));
    fs::path root(tmp);
    // 公開版預設不留下輸入內容；僅指定目錄、空值或誤寫都不算啟用。
    setenv("ZUIN_TRACE_DIR", (root / "opt-in").c_str(), 1);
    unsetenv("ZUIN_TRACE");
    assert(zuin::defaultTraceDirectory().empty());
    for (const char *value : {"", "0", "true", "yes", "2"}) {
        setenv("ZUIN_TRACE", value, 1);
        assert(zuin::defaultTraceDirectory().empty());
    }
    {
        zuin::Trace off(zuin::defaultTraceDirectory());
        assert(!off.enabled() && off.record({{"text", "不應保存"}}) == 0);
    }
    assert(!fs::exists(root / "opt-in"));
    setenv("ZUIN_TRACE", "1", 1);
    assert(zuin::defaultTraceDirectory() == (root / "opt-in").string());
    unsetenv("ZUIN_TRACE_DIR");
    setenv("XDG_STATE_HOME", root.c_str(), 1);
    assert(zuin::defaultTraceDirectory() == (root / "zuin/trace").string());
    unsetenv("ZUIN_TRACE");
    {
        zuin::Trace off("");
        assert(off.record({{"event", "disabled"}}) == 0);
    }
    {
        zuin::Trace trace((root / "rotate").string(), 4096, 3, 1024 * 1024);
        std::vector<int64_t> times;
        for (int i = 0; i < 300; ++i) {
            auto start = zuin::Trace::monotonicMicros();
            trace.record({{"event", "test"}, {"number", i}, {"text", std::string(300, 'x')}});
            times.push_back(zuin::Trace::monotonicMicros() - start);
        }
        std::sort(times.begin(), times.end());
        std::cout << "trace enqueue p99 " << times[times.size()*99/100] << " us (synthetic)\n";
        // 確保結尾可辨識；無須假設測試環境的排程不造成佇列丟棄。
        uint64_t last = 0;
        for (int i = 0; i < 1000 && !last && !trace.failed(); ++i) {
            last = trace.record({{"event", "last"}});
            if (!last) { usleep(1000); }
        }
        assert(last != 0);
    }
    size_t bytes = 0, files = 0;
    bool last = false;
    for (auto &file : fs::directory_iterator(root / "rotate")) {
        struct stat st {};
        assert(stat(file.path().c_str(), &st) == 0 && (st.st_mode & 0777) == 0600);
        if (file.path().extension() != ".jsonl") { continue; }
        ++files;
        bytes += st.st_size;
        assert(st.st_size <= 4096);
        std::ifstream in(file.path());
        std::string line;
        while (std::getline(in, line)) {
            auto event = json::parse(line);
            last = last || event.value("event", "") == "last";
        }
    }
    assert(files == 3 && bytes <= 3 * 4096 && last);
    struct stat dir {};
    assert(stat((root / "rotate").c_str(), &dir) == 0 && (dir.st_mode & 0777) == 0700);

    // 重啟時修復半筆 JSON，不讓新紀錄和損壞尾端黏在一起。
    fs::create_directory(root / "partial");
    { std::ofstream f(root / "partial/trace.jsonl"); f << "{\"event\":\"old\"}\n{\"incomplete\":"; }
    { zuin::Trace trace((root / "partial").string()); }
    {
        std::ifstream in(root / "partial/trace.jsonl");
        std::string line;
        int count = 0;
        while (std::getline(in, line)) { assert(json::parse(line).contains("event")); ++count; }
        assert(count == 2);
    }

    // 佇列太小仍留下缺口計數，不阻塞或默默把紀錄當成完整。
    { zuin::Trace trace((root / "overflow").string(), 4096, 2, 1); trace.record({{"event", "large"}}); }
    {
        std::ifstream in(root / "overflow/trace.jsonl");
        std::string line;
        uint64_t lost = 0;
        while (std::getline(in, line)) { lost += json::parse(line).value("dropped", uint64_t(0)); }
        assert(lost >= 2);
    }

    // 拒絕追蹤檔案符號連結，不能覆寫其他檔案。
    fs::create_directory(root / "symlink");
    { std::ofstream f(root / "untouched"); f << "keep"; }
    fs::create_symlink(root / "untouched", root / "symlink/trace.jsonl");
    {
        zuin::Trace trace((root / "symlink").string());
        for (int i = 0; i < 1000 && !trace.failed(); ++i) { usleep(1000); }
        assert(trace.failed() && trace.record({{"event", "ignored"}}) == 0);
    }
    std::ifstream untouched(root / "untouched");
    std::string contents;
    untouched >> contents;
    assert(contents == "keep");
    fs::remove_all(root);
    std::cout << "PASS trace rotation, restart, permissions, overflow, failure\n";
}
