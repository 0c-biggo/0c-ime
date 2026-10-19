// 本機 UX 飛行紀錄：逐鍵路徑只排入有界佇列，背景執行緒輪替寫檔。
#pragma once

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <deque>
#include <mutex>
#include <string>
#include <thread>

#include "nlohmann/json.hpp"

namespace zuin {

std::string defaultTraceDirectory(); // 預設停用；ZUIN_TRACE=1 才啟用，使用 XDG_STATE_HOME

class Trace {
public:
    explicit Trace(std::string directory, size_t fileBytes = 16 * 1024 * 1024,
                   int files = 4, size_t queueBytes = 1024 * 1024);
    ~Trace();
    Trace(const Trace &) = delete;
    Trace &operator=(const Trace &) = delete;

    // 回傳事件序號；佇列滿、停用或寫檔失敗時回傳 0，不等待磁碟。
    uint64_t record(nlohmann::json event) noexcept;
    bool failed() const { return failed_; }
    bool enabled() const { return writer_.joinable() && !failed_; }
    static int64_t monotonicMicros();

private:
    void run();
    bool openFile();
    bool writeLine(const std::string &line);
    bool rotate();
    void fail();

    std::string directory_, runID_;
    size_t fileBytes_, queueLimit_, queuedBytes_ = 0, written_ = 0;
    int files_, dirFD_ = -1, lockFD_ = -1, fileFD_ = -1;
    int64_t start_;
    std::atomic<uint64_t> sequence_{0}, dropped_{0};
    std::atomic<bool> failed_{false};
    std::mutex mutex_;
    std::condition_variable ready_;
    std::deque<std::string> queue_;
    bool stopping_ = false;
    std::thread writer_;
};

} // namespace zuin
