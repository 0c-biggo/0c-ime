//go:build !windows && !darwin

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/update"
	"github.com/godbus/dbus/v5"
)

// 外殼的 addon 設定（install.py 寫入）與外殼本身。新版的 Library 是外殼的絕對路徑，Fcitx5 不必設定
// FCITX_ADDON_DIRS（#92、#93）；0.1.0-dev.59 以前寫相對的 lib0c，要靠 environment.d 的 FCITX_ADDON_DIRS。
const (
	fcitxAddonConf  = ".local/share/fcitx5/addon/0c.conf"
	fcitxShellLib   = ".local/lib/fcitx5/lib0c.so"
	legacyShellLib  = ".local/lib/fcitx5/libzuin.so" // 0.1.0-dev.15 以前的外殼
	fcitxAddonDirs  = "FCITX_ADDON_DIRS"
	fcitxUserAddons = ".local/lib/fcitx5"
)

// systemAddonDir 認得各發行版的 Fcitx5 系統 addon 目錄（與 install.py 的 SYSTEM_ADDON_DIR 相同）。
var systemAddonDir = regexp.MustCompile(`^/(usr/)?(local/)?lib[^/:]*(/[^/:]+)?/fcitx5$`)

var libraryLine = regexp.MustCompile(`(?m)^\s*Library\s*=\s*(\S+)`)

// obsoleteAddonDirs：FCITX_ADDON_DIRS 是舊版安裝器寫的（外殼的使用者目錄加系統 addon 目錄），不是使用者自己加的目錄。
func obsoleteAddonDirs(value, home string) bool {
	user := map[string]bool{"${HOME}/" + fcitxUserAddons: true, "$HOME/" + fcitxUserAddons: true, filepath.Join(home, fcitxUserAddons): true}
	if real, err := filepath.EvalSymlinks(home); err == nil { // 家目錄是符號連結（例如 /home → /var/home）
		user[filepath.Join(real, fcitxUserAddons)] = true
	}
	ours := false
	for _, part := range strings.Split(strings.Trim(strings.TrimSpace(value), `"'`), ":") {
		if part == "" {
			continue
		}
		if trimmed := strings.TrimRight(part, "/"); trimmed != "" {
			part = trimmed
		}
		switch {
		case user[part]:
			ours = true
		case !systemAddonDir.MatchString(part):
			return false
		}
	}
	return ours
}

// addonLibraryAbsolute：已安裝的 addon 設定以絕對路徑指向外殼（新版安裝器），不需要 FCITX_ADDON_DIRS。
func addonLibraryAbsolute(home string) bool {
	b, err := os.ReadFile(filepath.Join(home, fcitxAddonConf))
	if err != nil {
		return false
	}
	m := libraryLine.FindSubmatch(b)
	return m != nil && strings.HasPrefix(string(m[1]), "/")
}

// fcitxRestartEnv 是更新器重新啟動 Fcitx5 時的環境。外殼以絕對路徑載入時，拿掉舊版留下的 FCITX_ADDON_DIRS：
// 它的系統目錄是建置機的（Debian 多架構路徑），Fedora 等發行版的 Fcitx5 會因此找不到自己的 addon（#92）；
// 使用者管理員與 D-Bus 跨登出存活時，這個舊值也會一直留在更新器的環境裡。回復到舊版（相對路徑）時照舊沿用。
func fcitxRestartEnv(env []string, home string) []string {
	if !addonLibraryAbsolute(home) {
		return env
	}
	out := make([]string, 0, len(env))
	for _, pair := range env {
		if key, value, _ := strings.Cut(pair, "="); key == fcitxAddonDirs && obsoleteAddonDirs(value, home) {
			continue
		}
		out = append(out, pair)
	}
	return out
}

// shellLoaded：唯一執行中的 Fcitx5 已載入 libraries（家目錄的相對路徑）之一目前的檔案，不是被取代的舊檔。
// 用於核對新外殼生效；判斷外殼是否仍在使用要用 shellMapped。
func shellLoaded(libraries ...string) bool {
	pids, err := fcitxProcesses()
	if err != nil || len(pids) != 1 {
		return false
	}
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pids[0]), "maps"))
	if err != nil {
		return false
	}
	for _, library := range libraries {
		// maps 記的是解開符號連結後的路徑（例如家目錄 /home → /var/home）。
		path, err := filepath.EvalSymlinks(homePath(library))
		var stat syscall.Stat_t
		if err == nil && syscall.Stat(path, &stat) == nil && mappedShell(data, path, stat.Ino, stat.Dev) {
			return true
		}
	}
	return false
}

// shellMapped：唯一執行中的 Fcitx5 映射著任何一版外殼。和 shellLoaded 不同，已被取代的舊檔（maps 顯示
// (deleted) 或別的 inode）、0.1.0-dev.15 以前的 libzuin.so 都算：上次更新換了檔案但 Fcitx5 沒有重新啟動時，
// 使用者仍以舊外殼打字。行程或 maps 讀不到時當成已映射。
func shellMapped() bool {
	pids, err := fcitxProcesses()
	if err != nil || len(pids) != 1 {
		return true
	}
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pids[0]), "maps"))
	return err != nil || mapsShell(data)
}

// mapsShell：/proc/PID/maps 裡有任何路徑的 lib0c.so 或 libzuin.so（含 deleted）。
func mapsShell(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		name := filepath.Base(strings.TrimSuffix(strings.Join(fields[5:], " "), " (deleted)"))
		if name == filepath.Base(fcitxShellLib) || name == filepath.Base(legacyShellLib) {
			return true
		}
	}
	return false
}

// shellInUse：執行中的 Fcitx5 真的透過外殼在輸入。Fcitx5 沒映射任何一版外殼（#93：D-Bus 啟動的 Fcitx5
// 拿不到舊版的搜尋路徑），或 Fcitx5 連自己的 D-Bus 介面都沒有（#92：系統 addon 全部找不到，任何程式都打不了字）時，
// 都沒有我們的組字要保護。查不出來時當成使用中，走原本有組字保護的路徑。
func shellInUse(conn *dbus.Conn) bool {
	if !shellMapped() {
		return false
	}
	var owned bool
	err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, "org.fcitx.Fcitx5").Store(&owned)
	return err != nil || owned
}

// installUnusedShell 在外殼沒有運作時安裝：不停止、不重啟 Fcitx5，不需要使用者確認（自動模式也會完成），
// 新外殼重新登入後生效（pending）。之前卡在 #92／#93 的使用者只要登出再登入；重新登入後由一般流程核對。
// 已安裝這一版時只回報仍待重新登入，不重裝。
func (p *system) installUnusedShell(ctx context.Context, root string, r *update.Release) (bool, error) {
	if err := p.Drain(ctx); err != nil {
		return false, fmt.Errorf("%w：%v", autoupdate.ErrShellBusy, err)
	}
	if err := waitServersStopped(ctx); err != nil {
		return false, fmt.Errorf("%w：%v", autoupdate.ErrShellBusy, err)
	}
	if installed, _ := installedVersion(); installed != r.Version {
		// install.py 平常要求先停止 Fcitx5；這裡已確認外殼沒有在用，換檔不影響執行中的 Fcitx5。
		if err := installLiveBundle(ctx, root, r, "ZC_SHELL_UNUSED=1"); err != nil {
			return false, err
		}
		afterInstall()
		fmt.Printf("已安裝 %s；目前的 Fcitx5 沒有在使用靈犀注音，登出再登入後生效。\n", r.Version)
	}
	return true, nil
}

type groupItem struct {
	Name   string
	Layout string
}

// currentGroup 讀執行中 Fcitx5 目前的輸入法群組。
func currentGroup(ctx context.Context, conn *dbus.Conn) (group, layout string, items []groupItem, err error) {
	if err = controller(ctx, conn, "CurrentInputMethodGroup").Store(&group); err != nil {
		return
	}
	err = controller(ctx, conn, "InputMethodGroupInfo", group).Store(&layout, &items)
	return
}

func hasZuin(items []groupItem) bool {
	for _, item := range items {
		if item.Name == "zuin" {
			return true
		}
	}
	return false
}

// liveGroupHasZuin：執行中 Fcitx5 目前的輸入法群組裡有沒有靈犀注音。
func liveGroupHasZuin() (bool, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return false, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, items, err := currentGroup(ctx, conn)
	return err == nil && hasZuin(items), err
}

// addInputMethodLive 把 zuin 加進執行中 Fcitx5 目前的輸入法群組（Fcitx5 自己存回 profile）；已有時不動。
// 只在外殼已載入時呼叫：Fcitx5 會略過還沒載入的輸入法，加了也不會出現。
func addInputMethodLive() error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	group, layout, items, err := currentGroup(ctx, conn)
	if err != nil {
		return err
	}
	if hasZuin(items) {
		return nil
	}
	items = append(items, groupItem{Name: "zuin"})
	if err = controller(ctx, conn, "SetInputMethodGroupInfo", group, layout, items).Err; err != nil {
		return err
	}
	return verifyInputMethod(ctx, conn, group)
}

// verifyInputMethod 確認 zuin 真的留在群組裡（Fcitx5 會丟掉找不到的輸入法）。
func verifyInputMethod(ctx context.Context, conn *dbus.Conn, group string) error {
	var layout string
	var items []groupItem
	if err := controller(ctx, conn, "InputMethodGroupInfo", group).Store(&layout, &items); err != nil {
		return err
	}
	if hasZuin(items) {
		return nil
	}
	return fmt.Errorf("Fcitx5 沒有保留靈犀注音")
}

// zuinRestoreMarker 是 install.py 在 #93 卡住狀態安裝外殼時留下的一次性待辦（相對個人資料目錄）：
// 沒載入外殼的 Fcitx5 會把 zuin 從輸入法清單拿掉，登出時存回 profile，換好外殼重新登入後清單裡就沒有靈犀注音。
// 卡住的判斷在 install.py（移除 90-0c.conf 舊設定之前，見 stuck_without_shell）；這裡只負責補回一次。
const zuinRestoreMarker = "update/fcitx-restore-zuin.json"

type zuinRestore struct {
	Schema  int    `json:"schema"`
	Created string `json:"created,omitempty"`
	Version string `json:"version,omitempty"`
	// ProfileBoot：登入時（Fcitx5 還沒執行）已把 zuin 寫進 profile 的那次開機（boot_id）。
	ProfileBoot string `json:"profile_boot,omitempty"`
}

// 測試替換用。
var (
	restoreFcitxPids  = fcitxProcesses
	restoreShellReady = func() bool { return shellLoaded(fcitxShellLib) }
	restoreProfileAdd = addInputMethod
	restoreLiveHas    = liveGroupHasZuin
	restoreLiveAdd    = addInputMethodLive
	currentBootID     = func() string {
		b, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
		return strings.TrimSpace(string(b))
	}
)

// restoreStuckInputMethod 完成 zuinRestoreMarker 的待辦；任何失敗都只記錄、保留待辦下次再試，不中斷更新。
//
//   - 登入時、桌面啟動前（auto -login）Fcitx5 還沒執行：把 zuin 寫進 profile（同首次安裝的 ProfileAtLogin），
//     Fcitx5 一啟動就有靈犀注音。Fcitx5 執行中不改檔案（它結束時會整個覆寫）。
//   - 之後的喚起：唯一的 Fcitx5 已載入目前的外殼時，以 D-Bus 確認目前群組有 zuin，沒有就加入並核對，然後清除待辦。
//     同一次開機已在登入時寫過 profile 的，執行中的 Fcitx5 都是之後才啟動、讀到了 zuin 又載入了外殼，
//     群組裡沒有就是使用者自己拿掉的，不再加回。
//
// 外殼沒載入時不動：Fcitx5 會丟掉沒載入的輸入法。待辦只做一次，清除後使用者自己移除也不再干涉。
func restoreStuckInputMethod(base string, atLogin bool) {
	path := filepath.Join(base, filepath.FromSlash(zuinRestoreMarker))
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var todo zuinRestore
	_ = json.Unmarshal(raw, &todo) // 內容讀不懂時當成剛建立的待辦
	pids, err := restoreFcitxPids()
	if err != nil {
		return
	}
	if len(pids) == 0 {
		if !atLogin {
			return
		}
		if err = restoreProfileAdd(); err != nil {
			fmt.Fprintln(os.Stderr, "0c-update: 把靈犀注音加回輸入法清單失敗，稍後再試：", err)
			return
		}
		todo.ProfileBoot = currentBootID()
		if b, err := json.Marshal(todo); err == nil {
			_ = os.WriteFile(path, append(b, '\n'), 0o600)
		}
		return
	}
	if len(pids) != 1 || !restoreShellReady() {
		return
	}
	has, err := restoreLiveHas()
	if err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 查詢 Fcitx5 輸入法清單失敗，稍後再試：", err)
		return
	}
	if !has && (todo.ProfileBoot == "" || todo.ProfileBoot != currentBootID()) {
		if err = restoreLiveAdd(); err != nil {
			fmt.Fprintln(os.Stderr, "0c-update: 把靈犀注音加回輸入法清單失敗，稍後再試：", err)
			return
		}
		fmt.Fprintln(os.Stderr, "0c-update: 已把靈犀注音加回 Fcitx5 的輸入法清單")
	}
	if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "0c-update: 清除待辦失敗：", err)
	}
}
