// 升級過渡期：只有舊 socket（$XDG_RUNTIME_DIR/zuin/zuin.sock）上有 server 時，新外殼要接上它，
// 不能另起第二個寫入個人詞庫的 server。
// 用法：legacy_socket_test <0c-server 路徑> <資料目錄>
#include <cassert>
#include <cstdio>
#include <cstdlib>
#include <signal.h>
#include <string>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>

#include "../src/client.h"

int main(int argc, char **argv) {
    if (argc != 3) {
        std::fprintf(stderr, "用法：%s <0c-server> <data>\n", argv[0]);
        return 2;
    }
    char tmp[] = "/tmp/0c-legacy-socket-XXXXXX";
    std::string runtime = mkdtemp(tmp);
    setenv("XDG_RUNTIME_DIR", runtime.c_str(), 1);
    std::string legacy = runtime + "/zuin/zuin.sock", user = runtime + "/user.json";
    pid_t server = fork();
    if (server == 0) {
        execl(argv[1], argv[1], "-socket", legacy.c_str(), "-data", argv[2], "-user", user.c_str(), static_cast<char *>(nullptr));
        _exit(127);
    }
    struct stat st{};
    for (int i = 0; i < 200 && stat(legacy.c_str(), &st) != 0; i++) {
        usleep(50 * 1000);
    }
    zuin::Client client("", "/bin/false"); // 不得自行啟動 server
    nlohmann::json reply;
    bool ok = client.call({{"op", "open"}, {"version", 0}}, reply) && reply.value("ok", false);
    kill(server, SIGTERM);
    waitpid(server, nullptr, 0);
    assert(ok && "新外殼未接上舊 socket 上的 server");
    assert(stat((runtime + "/0c/0c.sock").c_str(), &st) != 0);
    std::string cleanup = "rm -rf " + runtime;
    (void)!system(cleanup.c_str());
    std::puts("legacy socket PASS");
    return 0;
}
