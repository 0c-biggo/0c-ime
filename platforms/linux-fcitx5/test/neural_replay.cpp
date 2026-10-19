// 隔離 server 的真 C++ Session/IPC/快照解析重播；不存 raw trace、不啟動日常服務。
#include "../src/session.h"
#include <algorithm>
#include <chrono>
#include <fstream>
#include <iostream>
#include <stdexcept>

using nlohmann::json;
int main(int argc, char **argv) {
    if (argc != 4) { std::cerr << "usage: neural_replay socket cases.jsonl report.json\n"; return 2; }
    std::ifstream input(argv[2]); if (!input) { return 2; }
    zuin::Client client(argv[1], "/bin/false");
    std::vector<double> durations; std::string line; size_t cases = 0, correct = 0;
    while (std::getline(input, line)) {
        auto row = json::parse(line); zuin::Session session(client); zuin::Snapshot snap;
        for (const auto &key : row["keys"]) {
            auto start = std::chrono::steady_clock::now();
            snap = session.key(key.get<std::string>());
            // Fcitx render 使用的文字與 byte cursor 準備也在此計時內；未呼叫 compositor。
            auto shown = snap.text(); int cursor = snap.cursorByte();
            auto end = std::chrono::steady_clock::now();
            if (!snap.ok || !snap.handled || !snap.commit.empty() || cursor < 0 || size_t(cursor) > shown.size()) {
                throw std::runtime_error("invalid key/snapshot contract");
            }
            durations.push_back(std::chrono::duration<double, std::milli>(end-start).count());
        }
        correct += snap.text() == row["target"].get<std::string>(); cases++;
        if (!session.handle({{"type", "cancel"}}).ok) { throw std::runtime_error("cancel failed"); }
    }
    if (durations.empty()) { return 2; }
    std::sort(durations.begin(), durations.end());
    auto percentile = [&](double p) { return durations[std::min(durations.size()-1,size_t(std::ceil(p*durations.size()))-1)]; };
    json report = {{"cases", cases}, {"keys", durations.size()}, {"correct", correct},
        {"p50_ms", percentile(.5)}, {"p95_ms", percentile(.95)}, {"p99_ms", percentile(.99)}, {"max_ms", durations.back()},
        {"scope", "native C++ Session + IPC + engine + worker + snapshot/text/cursor preparation; no compositor rendering or human acceptance"}};
    std::ofstream output(argv[3]); output << report.dump(2) << '\n'; std::cout << report.dump() << '\n';
}
