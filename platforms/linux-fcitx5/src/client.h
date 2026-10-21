// 靈犀注音的 0c-server 用戶端：unix socket、4 位元組小端序長度加 JSON 的訊框（bridge/ipc）。
// 不依賴 Fcitx，可單獨測試。
#pragma once

#include <string>
#include <vector>

#include "nlohmann/json.hpp"

namespace zuin {

class Client {
public:
    // socketPath 為空時使用 $XDG_RUNTIME_DIR/0c/0c.sock，連不上再試舊版的 zuin/zuin.sock；
    // serverPath 為空時依共用規則找（locate.h）：~/.local/bin/0c-server、系統安裝的 libexec/0c/0c-server，再找 PATH。
    Client(std::string socketPath = "", std::string serverPath = "");
    ~Client();
    Client(const Client &) = delete;
    Client &operator=(const Client &) = delete;

    // call 送出一個 Envelope 並讀回 Reply。連不上時嘗試啟動 0c-server 一次。
    // 失敗回傳 false（呼叫端應把按鍵交還應用程式，不得卡住宿主）。
    bool call(const nlohmann::json &envelope, nlohmann::json &reply);

    // connect 確保已連上（必要時啟動 0c-server）。送出請求前先呼叫，才知道連線是否換代。
    bool connect() { return ensureConnected(); }
    // connected 回報目前是否已有連線（不嘗試連線、不啟動 0c-server）。
    bool connected() const { return fd_ >= 0; }
    // alive 回報連線是否還在：不送請求、不等待，只看另一端關了沒有（引擎退出後讀到 EOF）。
    // 斷了、或有不該有的殘留資料（訊框會錯位）就關掉連線，下一次呼叫照常重連。
    bool alive();

    // generation 在每次重新連線後遞增；session 屬於某一代連線，代數變了就要重開並重播。
    unsigned generation() const { return generation_; }

    const std::string &lastError() const { return error_; }

private:
    bool connectOnce();
    bool connectTo(const std::string &path);
    bool ensureConnected();
    void spawnServer();
    void closeSocket();
    bool writeAll(const void *p, size_t n);
    bool readAll(void *p, size_t n);

    std::string socketPath_, legacySocketPath_, serverPath_, error_;
    std::vector<std::string> serverArgs_; // 系統安裝的引擎帶 -data <系統字典>
    int fd_ = -1;
    unsigned generation_ = 0;
};

} // namespace zuin
