// 找引擎與工具的共用規則（#181），與 Go 的 internal/installs 相同：每一項都先找官網安裝（家目錄），
// 再找系統安裝（發行版套件，make install PREFIX=/usr）。官網安裝的路徑與做法跟以前一樣：
// 引擎是 ~/.local/bin/0c-server、不帶參數（自己依 engine.json 轉交到核心更新的版本）；0c-update 先找
// 排程用的穩定副本 ~/.local/share/zuin/bin/0c-update，再找 ~/.local/bin/0c-update。系統安裝的引擎帶
// -data 指定系統字典。不依賴 Fcitx，方便單元測試。
#pragma once

#include <string>
#include <vector>

#ifndef ZC_SYSTEM_LIBEXECDIR
#define ZC_SYSTEM_LIBEXECDIR "/usr/libexec/0c"
#endif
#ifndef ZC_SYSTEM_DATADIR
#define ZC_SYSTEM_DATADIR "/usr/share/0c"
#endif

namespace zuin {

struct InstallDirs {
    std::string home;    // 使用者家目錄（空字串時不找官網安裝）
    std::string libexec; // 系統安裝的 libexec/0c
    std::string data;    // 系統安裝的 share/0c（字典在 data/ 底下）
};

// 目前使用者（$HOME）與建置時設定的系統目錄（-DZC_SYSTEM_LIBEXECDIR、-DZC_SYSTEM_DATADIR）。
InstallDirs defaultInstallDirs();

struct ServerCommand {
    std::string path;              // 找不到任何安裝時是 "0c-server"（由 PATH 找）
    std::vector<std::string> args; // 系統安裝：{"-data", <系統字典>}
};

ServerCommand locateServer(const InstallDirs &dirs);
// 都找不到時回傳穩定副本的位置（與以前相同，執行失敗由呼叫端顯示）。
std::string locateUpdater(const InstallDirs &dirs);

} // namespace zuin
