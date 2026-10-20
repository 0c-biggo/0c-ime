// 找引擎與工具的共用規則（#181）：與 Go 的 internal/installs 測試同樣的情境。
#include "../src/locate.h"

#include <cstdio>
#include <cstdlib>
#include <fstream>
#include <string>
#include <sys/stat.h>

static int failures = 0;
#define EXPECT(cond) do { if (!(cond)) { std::fprintf(stderr, "FAIL %s:%d %s\n", __FILE__, __LINE__, #cond); ++failures; } } while (0)

static void touch(const std::string &path, mode_t mode) {
    std::system(("mkdir -p '" + path.substr(0, path.rfind('/')) + "'").c_str());
    std::ofstream(path) << "#!/bin/sh\n";
    chmod(path.c_str(), mode);
}

int main() {
    char root[] = "/tmp/zuin-locate-XXXXXX";
    if (!mkdtemp(root)) { return 1; }
    const std::string r = root;
    zuin::InstallDirs dirs{r + "/home", r + "/usr/libexec/0c", r + "/usr/share/0c"};
    using Args = std::vector<std::string>;

    // 什麼都沒有：與以前相同，PATH 上的 0c-server、穩定副本的位置。
    auto none = zuin::locateServer(dirs);
    EXPECT(none.path == "0c-server" && none.args.empty());
    EXPECT(zuin::locateUpdater(dirs) == dirs.home + "/.local/share/zuin/bin/0c-update");

    // 只有系統安裝：引擎帶 -data 系統字典，0c-update 用 libexec 那一份。
    touch(dirs.libexec + "/0c-server", 0755);
    touch(dirs.libexec + "/0c-update", 0755);
    auto system = zuin::locateServer(dirs);
    EXPECT(system.path == dirs.libexec + "/0c-server" && system.args == (Args{"-data", dirs.data + "/data"}));
    EXPECT(zuin::locateUpdater(dirs) == dirs.libexec + "/0c-update");

    // 套件管理員模式（#181）：系統的 0c-update，而且 distribution.json 寫 package-manager；只看檔案。
    EXPECT(!zuin::packageManaged(dirs));
    std::system(("mkdir -p '" + dirs.data + "'").c_str());
    std::ofstream(dirs.data + "/distribution.json") << R"({"schema":1,"updates":"self"})";
    EXPECT(!zuin::packageManaged(dirs));
    std::ofstream(dirs.data + "/distribution.json") << R"({"schema":2,"updates":"package-manager"})";
    EXPECT(!zuin::packageManaged(dirs));
    std::ofstream(dirs.data + "/distribution.json") << "not json";
    EXPECT(!zuin::packageManaged(dirs));
    std::ofstream(dirs.data + "/distribution.json") << R"({"schema":1,"updates":"package-manager","version":"0.2.3"})";
    EXPECT(zuin::packageManaged(dirs));

    // 官網安裝也在：官網優先，引擎不帶參數（自己依 engine.json 轉交），0c-update 先找穩定副本。
    touch(dirs.home + "/.local/bin/0c-server", 0644); // 不能執行的不算
    EXPECT(zuin::locateServer(dirs).path == dirs.libexec + "/0c-server");
    touch(dirs.home + "/.local/bin/0c-server", 0755);
    touch(dirs.home + "/.local/bin/0c-update", 0755);
    auto personal = zuin::locateServer(dirs);
    EXPECT(personal.path == dirs.home + "/.local/bin/0c-server" && personal.args.empty());
    EXPECT(zuin::locateUpdater(dirs) == dirs.home + "/.local/bin/0c-update");
    EXPECT(!zuin::packageManaged(dirs)); // 選單用官網的 0c-update：照舊有更新列
    touch(dirs.home + "/.local/share/zuin/bin/0c-update", 0755);
    EXPECT(zuin::locateUpdater(dirs) == dirs.home + "/.local/share/zuin/bin/0c-update");

    // 沒有 HOME：不找家目錄。
    zuin::InstallDirs homeless{"", dirs.libexec, dirs.data};
    EXPECT(zuin::locateServer(homeless).path == dirs.libexec + "/0c-server");

    // 預設的系統目錄是建置時設定的（-DZC_SYSTEM_LIBEXECDIR）。
    EXPECT(zuin::defaultInstallDirs().libexec == ZC_SYSTEM_LIBEXECDIR);

    std::system(("rm -rf '" + r + "'").c_str());
    if (failures == 0) { std::puts("locate_test PASS"); }
    return failures ? 1 : 0;
}
