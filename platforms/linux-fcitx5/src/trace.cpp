#include "trace.h"

#include <cerrno>
#include <cstdio>
#include <cstdlib>
#include <filesystem>
#include <fcntl.h>
#include <sys/file.h>
#include <sys/stat.h>
#include <unistd.h>

namespace zuin {
namespace {
constexpr size_t maxEventBytes = 64 * 1024;
int64_t unixMicros() {
    return std::chrono::duration_cast<std::chrono::microseconds>(
               std::chrono::system_clock::now().time_since_epoch()).count();
}
bool privateFile(int fd) {
    struct stat st {};
    return fd >= 0 && fstat(fd, &st) == 0 && S_ISREG(st.st_mode) &&
           st.st_uid == geteuid() && st.st_nlink == 1 && fchmod(fd, 0600) == 0;
}
} // namespace

std::string defaultTraceDirectory() {
    // 詳細紀錄含輸入文字，只有明確選擇 ZUIN_TRACE=1 才啟用。
    if (const char *on = std::getenv("ZUIN_TRACE"); !on || std::string(on) != "1") {
        return {};
    }
    if (const char *dir = std::getenv("ZUIN_TRACE_DIR"); dir && *dir) {
        return dir;
    }
    if (const char *state = std::getenv("XDG_STATE_HOME"); state && *state == '/') {
        return std::string(state) + "/zuin/trace";
    }
    if (const char *home = std::getenv("HOME"); home && *home) {
        return std::string(home) + "/.local/state/zuin/trace";
    }
    return {};
}

int64_t Trace::monotonicMicros() {
    return std::chrono::duration_cast<std::chrono::microseconds>(
               std::chrono::steady_clock::now().time_since_epoch()).count();
}

Trace::Trace(std::string directory, size_t fileBytes, int files, size_t queueBytes)
    : directory_(std::move(directory)), runID_(std::to_string(unixMicros()) + "-" + std::to_string(getpid())),
      fileBytes_(fileBytes), queueLimit_(queueBytes), files_(files), start_(monotonicMicros()) {
    if (!directory_.empty() && fileBytes_ >= 1024 && files_ >= 1 && queueLimit_ > 0) {
        try { writer_ = std::thread([this] { run(); }); }
        catch (...) { fail(); return; }
        record({{"event", "start"}, {"schema", 1}, {"protocol", 0},
                {"frontend_build", __DATE__ " " __TIME__},
                {"file_bytes", fileBytes_}, {"files", files_}});
    }
}

Trace::~Trace() {
    if (writer_.joinable()) {
        { std::lock_guard lock(mutex_); stopping_ = true; }
        ready_.notify_one();
        writer_.join();
    }
}

uint64_t Trace::record(nlohmann::json event) noexcept {
    if (!writer_.joinable() || failed_) { return 0; }
    try {
        auto seq = ++sequence_;
        event["seq"] = seq;
        event["run"] = runID_;
        event["unix_us"] = unixMicros();
        event["mono_us"] = monotonicMicros() - start_;
        std::string line = event.dump() + '\n';
        std::unique_lock lock(mutex_, std::try_to_lock);
        if (!lock.owns_lock() || stopping_ || line.size() > maxEventBytes ||
            line.size() > fileBytes_ || queuedBytes_ + line.size() > queueLimit_) {
            ++dropped_;
            ready_.notify_one();
            return 0;
        }
        queuedBytes_ += line.size();
        queue_.push_back(std::move(line));
        ready_.notify_one();
        return seq;
    } catch (...) {
        ++dropped_;
        return 0;
    }
}

bool Trace::openFile() {
    fileFD_ = openat(dirFD_, "trace.jsonl", O_RDWR | O_APPEND | O_CREAT | O_CLOEXEC | O_NOFOLLOW, 0600);
    if (!privateFile(fileFD_)) { return false; }
    struct stat st {};
    if (fstat(fileFD_, &st) != 0) { return false; }
    written_ = static_cast<size_t>(st.st_size);
    // 上次中斷可能只寫了半行；丟棄未完成的一筆，避免黏到這次的第一筆。
    if (written_ > 0) {
        size_t n = std::min(written_, maxEventBytes);
        std::string tail(n, '\0');
        if (pread(fileFD_, tail.data(), n, static_cast<off_t>(written_ - n)) != static_cast<ssize_t>(n)) { return false; }
        if (tail.back() != '\n') {
            auto last = tail.rfind('\n');
            size_t keep = last == std::string::npos ? 0 : written_ - n + last + 1;
            if (ftruncate(fileFD_, static_cast<off_t>(keep)) != 0) { return false; }
            written_ = keep;
        }
    }
    return true;
}

bool Trace::rotate() {
    if (fileFD_ >= 0) { close(fileFD_); fileFD_ = -1; }
    auto name = [](int i) { return i == 0 ? std::string("trace.jsonl") : "trace." + std::to_string(i) + ".jsonl"; };
    if (unlinkat(dirFD_, name(files_ - 1).c_str(), 0) < 0 && errno != ENOENT) { return false; }
    for (int i = files_ - 1; i > 0; --i) {
        if (renameat(dirFD_, name(i - 1).c_str(), dirFD_, name(i).c_str()) < 0 && errno != ENOENT) { return false; }
    }
    return openFile();
}

bool Trace::writeLine(const std::string &line) {
    if (line.size() > fileBytes_) { return false; }
    if (written_ + line.size() > fileBytes_ && !rotate()) { return false; }
    size_t offset = 0;
    while (offset < line.size()) {
        ssize_t n = write(fileFD_, line.data() + offset, line.size() - offset);
        if (n < 0 && errno == EINTR) { continue; }
        if (n <= 0) { return false; }
        offset += static_cast<size_t>(n);
        written_ += static_cast<size_t>(n);
    }
    return true;
}

void Trace::fail() {
    failed_ = true;
    std::fprintf(stderr, "zuin: 本機 UX 紀錄無法寫入，已停錄；輸入不受影響\n");
}

void Trace::run() {
    try {
        std::filesystem::create_directories(directory_);
        dirFD_ = open(directory_.c_str(), O_RDONLY | O_DIRECTORY | O_CLOEXEC | O_NOFOLLOW);
        struct stat st {};
        if (dirFD_ < 0 || fstat(dirFD_, &st) != 0 || st.st_uid != geteuid() || fchmod(dirFD_, 0700) != 0) {
            fail();
        } else {
            lockFD_ = openat(dirFD_, "writer.lock", O_WRONLY | O_CREAT | O_CLOEXEC | O_NOFOLLOW, 0600);
            if (!privateFile(lockFD_) || flock(lockFD_, LOCK_EX | LOCK_NB) != 0 || !openFile()) { fail(); }
        }
        while (!failed_) {
            std::string line;
            uint64_t dropped;
            {
                std::unique_lock lock(mutex_);
                ready_.wait(lock, [this] { return stopping_ || !queue_.empty() || dropped_.load() > 0; });
                dropped = dropped_.exchange(0);
                if (!queue_.empty()) {
                    line = std::move(queue_.front());
                    queue_.pop_front();
                    queuedBytes_ -= line.size();
                } else if (stopping_ && dropped == 0) { break; }
            }
            if (dropped > 0) {
                nlohmann::json gap = {{"event", "gap"}, {"run", runID_}, {"dropped", dropped},
                                      {"unix_us", unixMicros()}, {"mono_us", monotonicMicros() - start_}};
                if (!writeLine(gap.dump() + '\n')) { fail(); }
            }
            if (!failed_ && !line.empty() && !writeLine(line)) { fail(); }
        }
    } catch (...) { fail(); }
    if (fileFD_ >= 0) { close(fileFD_); }
    if (lockFD_ >= 0) { close(lockFD_); }
    if (dirFD_ >= 0) { close(dirFD_); }
}

} // namespace zuin
