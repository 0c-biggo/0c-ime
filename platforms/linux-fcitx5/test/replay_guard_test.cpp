// 使用真 C++ Session/IPC 對照重播前後文字，不依賴 trace 或桌面服務。
#include "../src/session.h"
#include <cassert>
#include <atomic>
#include <cstring>
#include <thread>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>

using nlohmann::json;
static bool transfer(int fd, void *data, size_t n, bool write) {
    auto *p = static_cast<char *>(data);
    while (n) {
        auto got = write ? ::write(fd, p, n) : ::read(fd, p, n);
        if (got <= 0) { return false; }
        p += got; n -= got;
    }
    return true;
}
static bool receive(int fd, json &j) {
    unsigned char h[4]; if (!transfer(fd, h, 4, false)) { return false; }
    uint32_t n = h[0] | h[1] << 8 | h[2] << 16 | h[3] << 24;
    assert(n < 65536); std::string b(n, '\0');
    if (!transfer(fd, b.data(), n, false)) { return false; }
    j = json::parse(b); return true;
}
static void send(int fd, const json &j) {
    auto b = j.dump(); uint32_t n = b.size();
    unsigned char h[4] = {static_cast<unsigned char>(n), static_cast<unsigned char>(n >> 8), 0, 0};
    assert(transfer(fd, h, 4, true) && transfer(fd, b.data(), b.size(), true));
}
static void run(const std::string &action) {
    std::string path = "/tmp/zuin-replay-guard-" + std::to_string(getpid()) + "-" + action + ".sock";
    int listener = socket(AF_UNIX, SOCK_STREAM, 0); assert(listener >= 0);
    sockaddr_un address{}; address.sun_family = AF_UNIX; std::strcpy(address.sun_path, path.c_str());
    assert(bind(listener, reinterpret_cast<sockaddr *>(&address), sizeof(address)) == 0);
    assert(listen(listener, 2) == 0);
    int commits = 0;
    std::thread server([&]() {
        for (int generation = 0; generation < 2; generation++) {
            int fd = accept(listener, nullptr, nullptr); assert(fd >= 0);
            json r;
            while (receive(fd, r)) {
                auto op = r.value("op", "");
                if (op == "open") { send(fd, {{"ok", true}, {"session", generation + 1}}); continue; }
                if (op == "close") { send(fd, {{"ok", true}}); break; }
                auto req = r["req"];
                bool enter = req.value("key", "") == "Enter";
                if (enter) { commits++; }
                json snap = {{"handled", true}, {"revision", 1}, {"mode", "zhuyin"}, {"cursor", {{"rune", 1}, {"utf16", 1}}}};
                if (enter) { snap["commit"] = "乙"; }
                else if (req.value("type", "") == "cancel") { /* 保持空組字 */ }
                else { snap["preedit"] = {{{"text", generation == 0 ? "甲" : "乙"}, {"style", "normal"}}}; }
                send(fd, {{"ok", true}, {"snapshot", snap}});
                if (generation == 0) { shutdown(fd, SHUT_RDWR); break; }
            }
            close(fd);
        }
    });
    {
        zuin::Client client(path, "/bin/false"); zuin::Session session(client);
        assert(session.key("a").text() == "甲");
        usleep(10000);
        if (action == "commit") {
            auto committed = session.handle({{"type", "commit"}});
            assert(committed.ok && committed.commit == "甲" && committed.preedit.empty());
        } else if (action == "cancel") {
            auto canceled = session.handle({{"type", "cancel"}});
            assert(canceled.ok && canceled.commit.empty() && canceled.preedit.empty());
        } else {
            if (action == "context") {
                assert(!session.handle({{"type", "context"}, {"on", false}}).ok);
            }
            auto recovered = session.key("Enter");
            assert(recovered.ok && recovered.handled && recovered.reconnected);
            assert(recovered.text() == "乙" && recovered.commit.empty() && !recovered.notices.empty());
            assert(session.key("Enter").commit == "乙");
        }
    }
    server.join(); close(listener); unlink(path.c_str());
    assert(commits == ((action == "commit" || action == "cancel") ? 0 : 1));
}

static void styleHandoff(const std::string &first) {
    std::string path = "/tmp/0c-style-handoff-" + std::to_string(getpid()) + "-" + first + ".sock";
    int listener = socket(AF_UNIX, SOCK_STREAM, 0); assert(listener >= 0);
    sockaddr_un address{}; address.sun_family = AF_UNIX; std::strcpy(address.sun_path, path.c_str());
    assert(bind(listener, reinterpret_cast<sockaddr *>(&address), sizeof(address)) == 0);
    assert(listen(listener, 2) == 0);
    std::atomic<int> retired{0};
    std::thread server([&]() {
        for (int generation = 0; generation < 3; generation++) {
            int fd = accept(listener, nullptr, nullptr); assert(fd >= 0);
            bool enabled = generation == 1;
            json info = {{"input_style_shift_space", enabled ? "fullwidth" : "disabled"},
                         {"input_style_caps_letter", enabled ? "lower" : "platform"},
                         {"left_shift", enabled ? "true" : "false"}, {"right_shift", enabled ? "true" : "false"}};
            json request;
            while (receive(fd, request)) {
                auto op = request.value("op", "");
                send(fd, {{"ok", true}, {"session", generation + 1}, {"info", info}});
                if (op == "test_restart" || op == "close") { break; }
            }
            shutdown(fd, SHUT_RDWR); close(fd); retired.store(generation + 1);
        }
    });
    {
        zuin::Client client(path, "/bin/false"); zuin::Session session(client);
        assert(!session.styleShiftSpace() && !session.styleCapsLower());
        for (int generation = 1; generation < 3; generation++) {
            json reply; assert(client.call({{"op", "test_restart"}}, reply));
            while (retired.load() < generation) { std::this_thread::yield(); }
            bool got = first == "space" ? session.styleShiftSpace() :
                       first == "caps" ? session.styleCapsLower() : session.shiftKeys().left;
            assert(got == (generation == 1));
        }
    }
    server.join(); close(listener); unlink(path.c_str());
}

int main() {
    for (const auto &action : {"enter", "context", "commit", "cancel"}) { run(action); }
    for (const auto &first : {"space", "caps", "shift"}) { styleHandoff(first); }
}
