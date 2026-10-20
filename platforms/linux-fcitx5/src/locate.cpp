#include "locate.h"

#include <cstdlib>
#include <sys/stat.h>

namespace zuin {

namespace {

bool executable(const std::string &path) {
    struct stat st;
    return !path.empty() && ::stat(path.c_str(), &st) == 0 && S_ISREG(st.st_mode) && (st.st_mode & 0111);
}

} // namespace

InstallDirs defaultInstallDirs() {
    const char *home = std::getenv("HOME");
    return {home ? home : "", ZC_SYSTEM_LIBEXECDIR, ZC_SYSTEM_DATADIR};
}

ServerCommand locateServer(const InstallDirs &dirs) {
    if (!dirs.home.empty()) {
        std::string personal = dirs.home + "/.local/bin/0c-server";
        if (executable(personal)) { return {personal, {}}; }
    }
    if (!dirs.libexec.empty()) {
        std::string system = dirs.libexec + "/0c-server";
        if (executable(system)) { return {system, {"-data", dirs.data + "/data"}}; }
    }
    return {"0c-server", {}};
}

std::string locateUpdater(const InstallDirs &dirs) {
    const std::string stable = dirs.home + "/.local/share/zuin/bin/0c-update";
    for (const auto &p : {stable, dirs.home + "/.local/bin/0c-update"}) {
        if (!dirs.home.empty() && executable(p)) { return p; }
    }
    if (!dirs.libexec.empty() && executable(dirs.libexec + "/0c-update")) { return dirs.libexec + "/0c-update"; }
    return stable;
}

} // namespace zuin
