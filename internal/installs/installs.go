// Package installs 是 Linux 上找引擎與工具的共用規則（#181）：外殼（0c-ibus）、0c-update 都照這裡找
// 0c-server、0c-update、0c-settings 與字典。Fcitx5 外殼的 C++ 版本在 platforms/linux-fcitx5/src/locate.h，
// 兩邊的規則要一致。
//
// 同一台電腦可能有兩份：
//   - 官網安裝（personal）：一行安裝或套件的 install.py 放在家目錄，~/.local/bin/0c-server、
//     ~/.local/share/zuin/data，0c-update 有排程用的穩定副本 ~/.local/share/zuin/bin/0c-update。
//   - 系統安裝（system）：發行版套件以 make install PREFIX=/usr 放在 /usr/libexec/0c 與 /usr/share/0c，
//     由套件管理員更新（distribution.json）。
//
// 每一項都先找官網安裝、再找系統安裝：使用者自己裝的通常比較新，也是他明確選的。官網安裝的路徑與行為
// 跟以前完全一樣（引擎不帶參數啟動，自己依 engine.json 轉交到核心更新的版本）；系統安裝的引擎帶
// -data 指定系統的字典。
package installs

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/0c-biggo/0c-ime/internal/enginedir"
)

// 系統安裝的目錄，建置時以 -ldflags "-X <module>/internal/installs.LibexecDir=…" 改寫（make install 依 PREFIX 設定）。
// 預設值是 PREFIX=/usr 的位置，直接 go build 的系統套件也找得到。
var (
	LibexecDir = "/usr/libexec/0c" // 0c-server、0c-update、0c-ibus
	DataDir    = "/usr/share/0c"   // data/、neural/、distribution.json
	BinDir     = "/usr/bin"        // 0c-settings、0c-mcp
)

const (
	KindPersonal = "personal" // 官網安裝（家目錄）
	KindSystem   = "system"   // 系統套件

	// UpdatesSelf：0c-update 自己檢查、下載、安裝更新（官網安裝）。
	UpdatesSelf = "self"
	// UpdatesPackageManager：由系統套件管理員更新，0c-update 不連網、不排程、不安裝（#181）。
	UpdatesPackageManager = "package-manager"

	// DistributionFile 是 make install 放在 DataDir 的模式標記。
	DistributionFile = "distribution.json"
)

// Layout 是一組實際的目錄；測試換成暫存目錄。
type Layout struct {
	Home    string // 使用者家目錄
	Libexec string // 系統安裝的 libexec/0c
	Data    string // 系統安裝的 share/0c
	Bin     string // 系統安裝的 bin
}

// Default 是目前使用者與建置時設定的系統目錄。
func Default() Layout {
	home := os.Getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return Layout{Home: home, Libexec: LibexecDir, Data: DataDir, Bin: BinDir}
}

// Install 是找到的一份安裝。
type Install struct {
	Kind     string `json:"kind"`
	Server   string `json:"server"`
	Data     string `json:"data"`
	Updater  string `json:"updater,omitempty"`
	Settings string `json:"settings,omitempty"`
	// IBus 是官網安裝的 IBus 外殼；系統的 0c-ibus 被 ibus-daemon 帶起時交給它（兩個元件同名，誰先被掃到不一定）。
	IBus string `json:"ibus,omitempty"`
	// Active：外殼目前會用這一份的 0c-server（官網安裝優先）。
	Active bool `json:"active"`
	// Updates 是這一份怎麼更新（self／package-manager）。
	Updates string `json:"updates"`
	// Version 是系統安裝的 distribution.json 記的版本；官網安裝看收據，這裡不填。
	Version string `json:"version,omitempty"`
}

func executable(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0
}

func (l Layout) personalServer() string { return filepath.Join(l.Home, ".local/bin/0c-server") }

// personalUpdaters 依序是排程用的穩定副本與安裝器放在 PATH 上的那一份（與以前的外殼相同）。
func (l Layout) personalUpdaters() []string {
	return []string{filepath.Join(l.Home, ".local/share/zuin/bin/0c-update"), filepath.Join(l.Home, ".local/bin/0c-update")}
}

func (l Layout) systemServer() string  { return filepath.Join(l.Libexec, "0c-server") }
func (l Layout) systemUpdater() string { return filepath.Join(l.Libexec, "0c-update") }

// SystemData 是系統安裝的字典目錄。
func (l Layout) SystemData() string { return filepath.Join(l.Data, "data") }

// Personal 回報官網安裝（以 ~/.local/bin/0c-server 判斷，與 Fcitx5 外殼以前的做法相同）。
func (l Layout) Personal() (Install, bool) {
	if l.Home == "" || !executable(l.personalServer()) {
		return Install{}, false
	}
	base := filepath.Join(l.Home, ".local/share/zuin")
	in := Install{Kind: KindPersonal, Server: l.personalServer(), Data: filepath.Join(base, "data"), Updates: UpdatesSelf}
	if _, data, ok := enginedir.Resolve(base); ok { // 核心更新後引擎轉交到版本目錄，用的是那裡的配對字典
		in.Data = data
	}
	for _, p := range l.personalUpdaters() {
		if executable(p) {
			in.Updater = p
			break
		}
	}
	if p := filepath.Join(l.Home, ".local/bin/0c-settings"); executable(p) {
		in.Settings = p
	}
	if p := filepath.Join(l.Home, ".local/bin/0c-ibus"); executable(p) {
		in.IBus = p
	}
	return in, true
}

// System 回報系統安裝（以 libexec/0c/0c-server 判斷）。
func (l Layout) System() (Install, bool) {
	if l.Libexec == "" || !executable(l.systemServer()) {
		return Install{}, false
	}
	d, _ := l.Distribution()
	in := Install{Kind: KindSystem, Server: l.systemServer(), Data: l.SystemData(), Updates: d.Updates, Version: d.Version}
	if in.Updates == "" {
		in.Updates = UpdatesSelf
	}
	if executable(l.systemUpdater()) {
		in.Updater = l.systemUpdater()
	}
	if p := filepath.Join(l.Bin, "0c-settings"); executable(p) {
		in.Settings = p
	}
	return in, true
}

// All 回傳找到的安裝，使用中的（官網安裝優先）排第一個。
func (l Layout) All() []Install {
	var out []Install
	if in, ok := l.Personal(); ok {
		out = append(out, in)
	}
	if in, ok := l.System(); ok {
		out = append(out, in)
	}
	if len(out) > 0 {
		out[0].Active = true
	}
	return out
}

// Server 回傳外殼要啟動的引擎與參數。官網安裝不帶參數（引擎自己依 engine.json 轉交到核心更新的版本，
// 與以前相同）；系統安裝帶 -data 指定系統字典。兩份都沒有時是 PATH 上的 0c-server。
func (l Layout) Server() (path string, args []string) {
	if l.Home != "" && executable(l.personalServer()) {
		return l.personalServer(), nil
	}
	if l.Libexec != "" && executable(l.systemServer()) {
		return l.systemServer(), []string{"-data", l.SystemData()}
	}
	return "0c-server", nil
}

// Updater 回傳外殼選單要執行的 0c-update：官網安裝的穩定副本、~/.local/bin，再來是系統安裝；
// 都沒有時回傳穩定副本的位置（與以前相同，執行失敗由呼叫端顯示）。
func (l Layout) Updater() string {
	for _, p := range l.personalUpdaters() {
		if executable(p) {
			return p
		}
	}
	if l.Libexec != "" && executable(l.systemUpdater()) {
		return l.systemUpdater()
	}
	return l.personalUpdaters()[0]
}

// Distribution 是 make install 放的模式標記。
type Distribution struct {
	Schema  int    `json:"schema"`
	Updates string `json:"updates"`
	Version string `json:"version,omitempty"`
}

// Distribution 讀系統安裝的模式標記；沒有檔案時 ok 為 false。
func (l Layout) Distribution() (Distribution, bool) {
	var d Distribution
	if l.Data == "" {
		return d, false
	}
	b, err := os.ReadFile(filepath.Join(l.Data, DistributionFile))
	if errors.Is(err, fs.ErrNotExist) || err != nil {
		return d, false
	}
	if json.Unmarshal(b, &d) != nil || d.Schema != 1 {
		return Distribution{}, false
	}
	return d, true
}

// InSystem 回報 exe 是不是系統安裝 libexec 裡的程式（例如正在執行的 0c-update）。比對前解開符號連結。
func (l Layout) InSystem(exe string) bool {
	if l.Libexec == "" || exe == "" {
		return false
	}
	dir := filepath.Dir(resolve(exe))
	return dir == resolve(l.Libexec)
}

// InPersonal 回報 exe 是不是官網安裝的程式（~/.local/bin 或 ~/.local/share/zuin 底下，例如核心更新後的
// 版本目錄）。比對前兩邊都解開符號連結：Fedora Silverblue 的 /home 是 /var/home 的連結。
func (l Layout) InPersonal(exe string) bool {
	if l.Home == "" || exe == "" {
		return false
	}
	path := resolve(exe)
	if filepath.Dir(path) == resolve(filepath.Join(l.Home, ".local/bin")) {
		return true
	}
	base := resolve(filepath.Join(l.Home, ".local/share/zuin"))
	return strings.HasPrefix(path, base+string(filepath.Separator))
}

func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	if a, err := filepath.Abs(p); err == nil {
		p = a
	}
	return filepath.Clean(p)
}

// PackageManaged 回報 exe（正在執行的 0c-update）是否屬於「由套件管理員更新」的系統安裝：
// 程式在系統的 libexec/0c，而且模式標記寫 package-manager。官網安裝的 0c-update 不受系統套件影響。
func (l Layout) PackageManaged(exe string) bool {
	if !l.InSystem(exe) {
		return false
	}
	d, ok := l.Distribution()
	return ok && strings.TrimSpace(d.Updates) == UpdatesPackageManager
}
