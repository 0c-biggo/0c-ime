#include "menu.h"
#include "locate.h"

#include <cerrno>
#include <chrono>
#include <cstdlib>
#include <cstring>
#include <cstdio>
#include <thread>
#include <fcntl.h>
#include <signal.h>
#include <spawn.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>

#include <nlohmann/json.hpp>

extern char **environ;

namespace zuin {

UpdateStatus parseStatus(const std::string &text) {
    UpdateStatus s;
    auto j = nlohmann::json::parse(text, nullptr, false);
    if (!j.is_object()) { return s; }
    auto str = [](const nlohmann::json &o, const char *k) {
        auto it = o.find(k);
        return it != o.end() && it->is_string() ? it->get<std::string>() : std::string();
    };
    s.ok = true;
    s.installed = str(j, "installed"); s.engine = str(j, "engine"); s.stage = str(j, "stage");
    s.action = str(j, "action"); s.pausedUntil = str(j, "paused_until");
    s.lastSuccess = str(j, "last_success"); s.lastError = str(j, "last_error");
    s.packageManaged = str(j, "updates") == "package-manager";
    if (auto m = str(j, "mode"); !m.empty()) { s.mode = m; }
    if (auto t = j.find("target"); t != j.end() && t->is_object()) { s.target = str(*t, "version"); }
    if (auto b = j.find("blocked"); b != j.end() && b->is_object()) { s.blocked = str(*b, "reason"); s.detail = str(*b, "detail"); }
    if (auto c = j.find("settings"); c != j.end() && c->is_object()) {
        if (auto v = str(*c, "selection"); !v.empty()) { s.selection = v; }
        if (auto v = c->find("punctuation"); v != c->end() && v->is_boolean()) { s.punctuation = v->get<bool>(); }
        if (auto v = c->find("document_context"); v != c->end() && v->is_boolean()) { s.documentContext = v->get<bool>(); }
        if (auto v = c->find("output_simplified"); v != c->end() && v->is_boolean()) {
            s.simplifiedKnown = true;
            s.outputSimplified = v->get<bool>();
        }
    }
    if (auto f = j.find("features"); f != j.end() && f->is_object()) {
        if (auto v = f->find("vocab"); v != f->end() && v->is_boolean()) { s.vocab = v->get<bool>(); }
    }
    if (auto r = j.find("runtime"); r != j.end() && r->is_object()) {
        s.runtime = str(*r, "state");
        s.selectionState = str(*r, "selection_state");
        s.punctuationState = str(*r, "punctuation_state");
        auto live = r->find("output_simplified_live");
        auto now = r->find("output_simplified");
        if (live != r->end() && live->is_boolean() && live->get<bool>() && now != r->end() && now->is_boolean()) {
            s.simplifiedKnown = s.simplifiedLive = true;
            s.outputSimplified = now->get<bool>();
        }
    }
    return s;
}

std::string statusLabel(const UpdateStatus &s) {
    if (!s.ok) { return "靈犀注音：無法讀取更新狀態"; }
    if (!s.target.empty()) {
        if (s.blocked == "needs_consent") { return "有新版本 " + s.target + " — 立即安裝"; }
        if (s.blocked == "needs_relogin") { return s.target + "：" + (s.detail.empty() ? "登出再登入後生效" : s.detail); }
        if (s.blocked == "health_failed") { return s.target + " 更新未完成，已保留原版本"; }
        if (s.blocked == "waiting_idle") { return s.target + " 已準備好，閒置時自動更新；可立即完成"; }
        if (s.blocked == "engine_busy") { return s.target + " 等待組字完成，再更新"; }
        if (s.action == "finish") { return s.target + " 已準備好 — 現在完成更新"; }
        if (s.blocked == "metered_network") { return s.target + " 等待非計量網路下載"; }
        if (s.blocked == "low_battery") { return s.target + " 等待充電後更新"; }
        return s.target + " 更新中";
    }
    if (!s.pausedUntil.empty()) { return "自動更新暫停至 " + s.pausedUntil.substr(0, 10); }
    std::string v = s.current();
    return "靈犀注音 " + (v.empty() ? std::string("（未知版本）") : v) + " · " +
        (!s.lastError.empty() ? "更新查詢失敗，稍後再試" : s.lastSuccess.empty() ? "尚未檢查更新" : "已是最新");
}

std::vector<std::string> statusCommand(const UpdateStatus &s) {
    if (s.action == "finish") { return {"finish", "-notify"}; }
    return {"auto", "-now", "-notify"};
}

std::vector<std::string> settingsCommand(bool vocab) {
    if (vocab) { return {"open-settings", "vocab"}; }
    return {"open-settings"};
}

std::vector<std::string> simplifiedCommand(bool on) {
    return {"set", "output-simplified", on ? "on" : "off", "-apply", "-notify"};
}

std::string updaterPath() { return locateUpdater(defaultInstallDirs()); }

bool runUpdater(const std::vector<std::string> &args, std::string *output, int timeoutSeconds) {
    std::string exe = updaterPath();
    std::vector<char *> argv{const_cast<char *>(exe.c_str())};
    for (auto &a : args) { argv.push_back(const_cast<char *>(a.c_str())); }
    argv.push_back(nullptr);
    // 更新會結束 Fcitx；輸出若接 pipe，父行程退出後更新器可能收到
    // SIGPIPE 而停在 installing。匿名暫存檔由子行程持有，能跨越交接。
    FILE *capture = std::tmpfile();
    if (!capture) { return false; }
    const int fd = ::fileno(capture);
    if (::fcntl(fd, F_SETFD, FD_CLOEXEC) < 0) { std::fclose(capture); return false; }
    posix_spawn_file_actions_t actions;
    posix_spawn_file_actions_init(&actions);
    posix_spawn_file_actions_adddup2(&actions, fd, STDOUT_FILENO);
    posix_spawn_file_actions_adddup2(&actions, fd, STDERR_FILENO);
    posix_spawn_file_actions_addopen(&actions, STDIN_FILENO, "/dev/null", O_RDONLY, 0);
    posix_spawnattr_t attr;
    posix_spawnattr_init(&attr);
    posix_spawnattr_setflags(&attr, POSIX_SPAWN_SETSID);  // 不隨 Fcitx 的行程群組收到訊號
    pid_t pid = 0;
    int rc = posix_spawn(&pid, exe.c_str(), &actions, &attr, argv.data(), environ);
    posix_spawn_file_actions_destroy(&actions);
    posix_spawnattr_destroy(&attr);
    if (rc != 0) { std::fclose(capture); return false; }
    std::string out;
    auto deadline = std::chrono::steady_clock::now() + std::chrono::seconds(timeoutSeconds);
    int status = 0;
    bool timedOut = false;
    for (;;) {
        const auto done = ::waitpid(pid, &status, WNOHANG);
        if (done == pid) { break; }
        if (done < 0 && errno != EINTR) { std::fclose(capture); return false; }
        if (std::chrono::steady_clock::now() >= deadline) {
            timedOut = true;
            ::kill(pid, SIGTERM);
            while (::waitpid(pid, &status, 0) < 0 && errno == EINTR) {}
            break;
        }
        std::this_thread::sleep_for(std::chrono::milliseconds(20));
    }
    std::rewind(capture);
    char buf[4096];
    while (out.size() < (1 << 20)) {
        const auto n = std::fread(buf, 1, std::min(sizeof buf, (1 << 20) - out.size()), capture);
        if (!n) { break; }
        out.append(buf, n);
    }
    std::fclose(capture);
    if (output) { *output = out; }
    return !timedOut && WIFEXITED(status) && WEXITSTATUS(status) == 0;
}

} // namespace zuin
