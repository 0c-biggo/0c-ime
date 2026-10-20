#include "client.h"
#include "locate.h"

#include <cerrno>
#include <cstdint>
#include <cstdlib>
#include <cstring>
#include <fcntl.h>
#include <poll.h>
#include <signal.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/un.h>
#include <sys/wait.h>
#include <unistd.h>

namespace zuin {

namespace {

constexpr uint32_t kMaxFrame = 1u << 20;
constexpr int kTimeoutMs = 2000; // 單次請求的上限；超過就放棄，不得卡住宿主

std::string runtimeDir() {
    const char *rt = std::getenv("XDG_RUNTIME_DIR");
    return rt && *rt ? rt : "/tmp";
}

std::string defaultSocket() { return runtimeDir() + "/0c/0c.sock"; }

// 升級過渡期：舊版 zuin-server 仍在執行時它是個人詞庫唯一的寫入者，新外殼先接上它，不另起新 server。
std::string legacySocket() { return runtimeDir() + "/zuin/zuin.sock"; }

} // namespace

Client::Client(std::string socketPath, std::string serverPath)
    : socketPath_(socketPath.empty() ? defaultSocket() : std::move(socketPath)),
      legacySocketPath_(socketPath_ == defaultSocket() ? legacySocket() : "") {
    if (serverPath.empty()) {
        // 共用規則（locate.h，#181）：官網安裝的 ~/.local/bin/0c-server，再來是系統安裝（帶 -data），都沒有時找 PATH。
        auto server = locateServer(defaultInstallDirs());
        serverPath_ = std::move(server.path);
        serverArgs_ = std::move(server.args);
    } else {
        serverPath_ = std::move(serverPath);
    }
}

Client::~Client() { closeSocket(); }

void Client::closeSocket() {
    if (fd_ >= 0) {
        close(fd_);
        fd_ = -1;
    }
}

bool Client::alive() {
    if (fd_ < 0) { return false; }
    char b;
    ssize_t r = recv(fd_, &b, 1, MSG_PEEK | MSG_DONTWAIT);
    if (r < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) { return true; }
    closeSocket();
    return false;
}

bool Client::connectOnce() { return connectTo(socketPath_) || (!legacySocketPath_.empty() && connectTo(legacySocketPath_)); }

bool Client::connectTo(const std::string &path) {
    int fd = socket(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0);
    if (fd < 0) {
        error_ = std::strerror(errno);
        return false;
    }
    sockaddr_un addr{};
    addr.sun_family = AF_UNIX;
    if (path.size() >= sizeof(addr.sun_path)) {
        close(fd);
        error_ = "socket 路徑過長";
        return false;
    }
    std::memcpy(addr.sun_path, path.c_str(), path.size() + 1);
    if (::connect(fd, reinterpret_cast<sockaddr *>(&addr), sizeof(addr)) != 0) {
        error_ = std::strerror(errno);
        close(fd);
        return false;
    }
    fd_ = fd;
    generation_++;
    return true;
}

void Client::spawnServer() {
    // 兩次 fork：server 成為 init 的子行程，與宿主（fcitx5）脫離，也不留下殭屍行程。
    pid_t pid = fork();
    if (pid == 0) {
        if (fork() == 0) {
            setsid();
            int devnull = open("/dev/null", O_RDWR);
            if (devnull >= 0) {
                dup2(devnull, 0);
                dup2(devnull, 1);
            }
            std::vector<char *> argv{const_cast<char *>(serverPath_.c_str())};
            for (auto &a : serverArgs_) { argv.push_back(const_cast<char *>(a.c_str())); }
            argv.push_back(nullptr);
            execvp(serverPath_.c_str(), argv.data());
            _exit(127);
        }
        _exit(0);
    }
    if (pid > 0) {
        waitpid(pid, nullptr, 0);
    }
}

bool Client::ensureConnected() {
    if (fd_ >= 0) {
        return true;
    }
    if (connectOnce()) {
        return true;
    }
    spawnServer();
    for (int i = 0; i < 40; i++) { // 最多等 2 秒：server 要載入資料包
        usleep(50 * 1000);
        if (connectOnce()) {
            return true;
        }
    }
    return false;
}

bool Client::writeAll(const void *p, size_t n) {
    auto *b = static_cast<const char *>(p);
    while (n > 0) {
        ssize_t w = send(fd_, b, n, MSG_NOSIGNAL);
        if (w < 0 && errno == EINTR) {
            continue;
        }
        if (w <= 0) {
            error_ = std::strerror(errno);
            return false;
        }
        b += w;
        n -= static_cast<size_t>(w);
    }
    return true;
}

bool Client::readAll(void *p, size_t n) {
    auto *b = static_cast<char *>(p);
    while (n > 0) {
        pollfd pfd{fd_, POLLIN, 0};
        int r = poll(&pfd, 1, kTimeoutMs);
        if (r < 0 && errno == EINTR) {
            continue;
        }
        if (r <= 0) {
            error_ = r == 0 ? "等待 0c-server 逾時" : std::strerror(errno);
            return false;
        }
        ssize_t got = recv(fd_, b, n, 0);
        if (got < 0 && errno == EINTR) {
            continue;
        }
        if (got <= 0) {
            error_ = got == 0 ? "0c-server 關閉了連線" : std::strerror(errno);
            return false;
        }
        b += got;
        n -= static_cast<size_t>(got);
    }
    return true;
}

bool Client::call(const nlohmann::json &envelope, nlohmann::json &reply) {
    if (!ensureConnected()) {
        return false;
    }
    std::string body = envelope.dump();
    uint8_t hdr[4];
    uint32_t len = static_cast<uint32_t>(body.size());
    for (int i = 0; i < 4; i++) {
        hdr[i] = static_cast<uint8_t>(len >> (8 * i));
    }
    if (!writeAll(hdr, 4) || !writeAll(body.data(), body.size()) || !readAll(hdr, 4)) {
        closeSocket();
        return false;
    }
    len = uint32_t(hdr[0]) | uint32_t(hdr[1]) << 8 | uint32_t(hdr[2]) << 16 | uint32_t(hdr[3]) << 24;
    if (len > kMaxFrame) {
        error_ = "訊框超過上限";
        closeSocket();
        return false;
    }
    std::string resp(len, '\0');
    if (!readAll(resp.data(), len)) {
        closeSocket();
        return false;
    }
    reply = nlohmann::json::parse(resp, nullptr, false);
    if (reply.is_discarded()) {
        error_ = "回應不是合法的 JSON";
        closeSocket();
        return false;
    }
    return true;
}

} // namespace zuin
