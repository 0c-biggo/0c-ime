//go:build !windows && !darwin

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	fcitxDesktop    = "/usr/share/applications/org.fcitx.Fcitx5.desktop"
	launcherDesktop = "/usr/share/applications/fcitx5-wayland-launcher.desktop"
	restartCgroup   = "0::/user.slice/user-1000.slice/user@1000.service/app.slice/0c-fcitx5-1792651598028908613.service\n"
	kwinCgroup      = "0::/user.slice/user-1000.slice/user@1000.service/session.slice/plasma-kwin_wayland.service\n"
)

// fakeKWin 模擬 KWin 與 kwinrc：使用者的 kwinrc 是真的檔案（XDG_CONFIG_HOME），kreadconfig 讀它、沒有這個鍵時讀
// 系統預設；kwriteconfig 改它。KWin 收到變更通知時依現值停用並結束輸入法，或啟動 fcitx5（PID 123）。
type fakeKWin struct {
	t        *testing.T
	config   string // XDG_CONFIG_HOME
	value    string // 使用者 kwinrc 的值（有這個鍵時）
	fallback string // 系統預設（/etc/xdg/kwinrc）

	ignores    bool // KWin 沒有反應（VirtualKeyboard.available 一直是 true）
	noStart    bool // 寫回後 KWin 沒有啟動 fcitx5
	failWrites int  // 接下來幾次寫回失敗
	available  bool
	ran        []string
	emitted    int
}

func newFakeKWin(t *testing.T) *fakeKWin {
	t.Helper()
	k := &fakeKWin{t: t, config: t.TempDir(), available: true}
	saved := []any{procRoot, kconfigTool, runKConfig, kwinIMAvailable, emitKConfigChanged, kwinStateDir, kwinBus,
		kwinPoll, kwinClearWait, kwinGoneWait, kwinStartWait, currentBootID, settle,
		handbackPrepare, handbackExit, handbackRestore}
	t.Cleanup(func() {
		procRoot, kconfigTool, runKConfig = saved[0].(string), saved[1].(func(...string) (string, error)),
			saved[2].(func(context.Context, []string, string, ...string) ([]byte, error))
		kwinIMAvailable = saved[3].(func(context.Context, *dbus.Conn) (bool, error))
		emitKConfigChanged = saved[4].(func(*dbus.Conn, string, string, string) error)
		kwinStateDir = saved[5].(func() string)
		kwinBus = saved[6].(func() (*dbus.Conn, func(), error))
		kwinPoll, kwinClearWait, kwinGoneWait, kwinStartWait = saved[7].(time.Duration), saved[8].(time.Duration),
			saved[9].(time.Duration), saved[10].(time.Duration)
		currentBootID, settle = saved[11].(func() string), saved[12].(func())
		handbackPrepare = saved[13].(func(context.Context, *dbus.Conn, *shellRestart) error)
		handbackExit = saved[14].(func(context.Context, *dbus.Conn, uint64) (string, error))
		handbackRestore = saved[15].(func(context.Context, *dbus.Conn, *shellRestart) error)
	})
	procRoot = t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", k.config)
	state := t.TempDir()
	kwinStateDir = func() string { return state }
	kwinPoll, kwinClearWait, kwinGoneWait, kwinStartWait = time.Millisecond, 50*time.Millisecond, 50*time.Millisecond, 50*time.Millisecond
	currentBootID = func() string { return "boot-1" }
	settle = func() {}
	kwinBus = func() (*dbus.Conn, func(), error) { return new(dbus.Conn), func() {}, nil }
	kconfigTool = func(names ...string) (string, error) { return "/usr/bin/" + names[0], nil }
	kwinIMAvailable = func(context.Context, *dbus.Conn) (bool, error) { return k.available, nil }
	emitKConfigChanged = func(_ *dbus.Conn, file, group, key string) error {
		if file != "kwinrc" || group != "Wayland" || key != "InputMethod" {
			t.Fatalf("通知 %s %s %s", file, group, key)
		}
		k.emitted++
		if k.ignores {
			return nil
		}
		if k.current() == "" {
			k.available = false
			os.RemoveAll(filepath.Join(procRoot, "123"))
		} else if !k.noStart {
			k.available = true
			addProc(t, 123, "fcitx5", 50, kwinCgroup, "WAYLAND_SOCKET=27")
		}
		return nil
	}
	runKConfig = func(_ context.Context, env []string, path string, args ...string) ([]byte, error) {
		if slices.Contains(env, "WAYLAND_SOCKET=27") {
			t.Fatalf("kconfig 工具不該帶著失效的 WAYLAND_SOCKET：%v", args)
		}
		tool := filepath.Base(path)
		k.ran = append(k.ran, tool+" "+strings.Join(args[len(kwinrcArgs):], " "))
		if strings.HasPrefix(tool, "kread") {
			return []byte(k.current() + "\n"), nil
		}
		last := args[len(args)-1]
		if last != "" && k.failWrites > 0 {
			k.failWrites--
			return nil, fmt.Errorf("%s：kf.config.core: Failed to sync config (exit status 1)", tool)
		}
		k.set(last, true)
		return nil, nil
	}
	addProc(t, 50, "kwin_wayland", 1, kwinCgroup, "")
	return k
}

// set 設定 kwinrc：user 時寫在使用者的 kwinrc，否則只有系統預設。
func (k *fakeKWin) set(value string, user bool) {
	body := "[Plugins]\nblurEnabled=false\n\n[Wayland]\n"
	if user {
		k.value = value
		body += "InputMethod[$e]=" + value + "\n"
	} else {
		k.fallback = value
	}
	if err := os.WriteFile(filepath.Join(k.config, "kwinrc"), []byte(body+"\n[Xwayland]\nScale=1\n"), 0o600); err != nil {
		k.t.Fatal(err)
	}
}

func (k *fakeKWin) current() string {
	if userSetsInputMethod(filepath.Join(k.config, "kwinrc")) {
		return k.value
	}
	return k.fallback
}

func (k *fakeKWin) writes() []string {
	var out []string
	for _, r := range k.ran {
		if strings.HasPrefix(r, "kwrite") {
			out = append(out, r)
		}
	}
	return out
}

func addProc(t *testing.T, pid int, comm string, ppid int, cgroup, environ string) {
	t.Helper()
	dir := filepath.Join(procRoot, strconv.Itoa(pid))
	os.MkdirAll(dir, 0o700)
	files := map[string]string{
		"comm":    comm + "\n",
		"stat":    fmt.Sprintf("%d (%s) S %d %d %d 0", pid, comm, ppid, pid, pid),
		"cmdline": "/usr/bin/" + comm + "\x00",
		"environ": "XDG_RUNTIME_DIR=/run/user/1000\x00" + strings.ReplaceAll(environ, " ", "\x00") + "\x00",
		"cgroup":  cgroup,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// 寫回時不帶 --notify（Plasma 5 的 kwriteconfig5 沒有）；兩次寫入各自送出 KConfig 的變更通知。
// 原值寫在使用者的 kwinrc 時以 path 型別寫回。
func TestRestartKWinIMWritesThenNotifies(t *testing.T) {
	k := newFakeKWin(t)
	k.set(fcitxDesktop, true)
	im, err := readKWinIM(kconfigEnv(nil))
	if err != nil || im != (kwinIM{Value: fcitxDesktop}) {
		t.Fatalf("讀 kwinrc：%+v %v", im, err)
	}
	if err = restartKWinIM(context.Background(), new(dbus.Conn), kwinStateDir(), im, kconfigEnv(nil)); err != nil {
		t.Fatal(err)
	}
	want := []string{"kwriteconfig6 ", "kwriteconfig6 --type path " + fcitxDesktop}
	if !slices.Equal(k.writes(), want) || k.emitted != 2 || k.current() != fcitxDesktop || !anyFcitx() {
		t.Fatalf("寫入 %q、通知 %d 次、現值 %q", k.writes(), k.emitted, k.current())
	}
	if _, err := os.Stat(kwinPendingPath(kwinStateDir())); !os.IsNotExist(err) {
		t.Fatal("寫回成功後不該留下紀錄")
	}
}

// 原值來自系統預設（使用者的 kwinrc 沒有這個鍵）：寫回時刪掉清空時寫入的鍵，不把預設固定寫進使用者設定；
// 也不用 kwriteconfig --delete（有系統預設時它寫 InputMethod[$d]，把預設遮掉）。使用者 kwinrc 的其他設定照舊。
func TestRestartKWinIMKeepsSystemDefaultInherited(t *testing.T) {
	k := newFakeKWin(t)
	k.set(launcherDesktop, false)
	im, err := readKWinIM(kconfigEnv(nil))
	if err != nil || !im.Inherited || !im.fcitx() {
		t.Fatalf("讀 kwinrc：%+v %v", im, err)
	}
	if err = restartKWinIM(context.Background(), new(dbus.Conn), kwinStateDir(), im, kconfigEnv(nil)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(k.config, "kwinrc"))
	if want := []string{"kwriteconfig6 "}; !slices.Equal(k.writes(), want) || k.emitted != 2 || k.current() != launcherDesktop || !anyFcitx() {
		t.Fatalf("寫入 %q、通知 %d 次、現值 %q", k.writes(), k.emitted, k.current())
	}
	if string(raw) != "[Plugins]\nblurEnabled=false\n\n[Wayland]\n\n[Xwayland]\nScale=1\n" {
		t.Fatalf("使用者的 kwinrc：%q", raw)
	}
}

// KWin 沒有反應（available 沒變 false）或寫回後沒有啟動 fcitx5：kwinrc 仍寫回原值並回報錯誤，由呼叫端直接啟動。
func TestRestartKWinIMReportsWhenKWinDoesNotReact(t *testing.T) {
	for _, tc := range []struct {
		name string
		mod  func(*fakeKWin)
		want string
	}{
		{"沒有停用", func(k *fakeKWin) { k.ignores = true }, "沒有停用輸入法"},
		{"沒有啟動", func(k *fakeKWin) { k.noStart = true }, "沒有啟動 Fcitx5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := newFakeKWin(t)
			k.set(fcitxDesktop, true)
			tc.mod(k)
			err := restartKWinIM(context.Background(), new(dbus.Conn), kwinStateDir(), kwinIM{Value: fcitxDesktop}, kconfigEnv(nil))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("錯誤：%v", err)
			}
			if k.current() != fcitxDesktop {
				t.Fatalf("kwinrc 應寫回原值：%q", k.current())
			}
		})
	}
}

// 寫回一直失敗：錯誤帶出 kwriteconfig 的訊息，留下紀錄；下一次執行更新器時補寫，虛擬鍵盤不會一直是「無」。
func TestKWinWriteBackIsRetriedOnTheNextRun(t *testing.T) {
	k := newFakeKWin(t)
	k.set(fcitxDesktop, true)
	k.failWrites = 3
	err := restartKWinIM(context.Background(), new(dbus.Conn), kwinStateDir(), kwinIM{Value: fcitxDesktop}, kconfigEnv(nil))
	if err == nil || !strings.Contains(err.Error(), "Failed to sync config") || k.current() != "" {
		t.Fatalf("錯誤 %v，現值 %q", err, k.current())
	}
	path := kwinPendingPath(kwinStateDir())
	finishKWinIM(kwinStateDir()) // 剛寫的紀錄可能屬於另一個還在交接的更新器：不碰
	if k.current() != "" {
		t.Fatal("30 秒內的紀錄不該補寫")
	}
	old := time.Now().Add(-time.Minute)
	os.Chtimes(path, old, old)
	finishKWinIM(kwinStateDir())
	if k.current() != fcitxDesktop || k.emitted == 0 {
		t.Fatalf("應補寫原值：%q", k.current())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("補寫後應刪除紀錄")
	}

	// 登入時（KWin 還沒啟動、沒有 session bus）只寫檔；KWin 在執行卻連不到 bus 時留到下次。
	kwinBus = func() (*dbus.Conn, func(), error) { return nil, nil, fmt.Errorf("no bus") }
	for _, kwin := range []bool{true, false} {
		k.set("", true)
		k.emitted = 0
		if !kwin {
			os.RemoveAll(filepath.Join(procRoot, "50"))
		}
		if err := saveKWinPending(kwinStateDir(), kwinIM{Value: fcitxDesktop}, nil); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(path, old, old)
		finishKWinIM(kwinStateDir())
		_, kept := os.Stat(path)
		if kwin && (k.current() != "" || kept != nil) || !kwin && (k.current() != fcitxDesktop || k.emitted != 0 || kept == nil) {
			t.Fatalf("KWin 執行中＝%v：現值 %q、通知 %d、紀錄 %v", kwin, k.current(), k.emitted, kept)
		}
	}

	// 已經有值（使用者自己選了別的）：只刪紀錄。
	k.set("/usr/share/applications/com.github.maliit.keyboard.desktop", true)
	saveKWinPending(kwinStateDir(), kwinIM{Value: fcitxDesktop}, nil)
	os.Chtimes(path, old, old)
	k.ran = nil
	finishKWinIM(kwinStateDir())
	if len(k.writes()) != 0 {
		t.Fatalf("不該覆寫使用者的選擇：%q", k.writes())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("應刪除紀錄")
	}
}

// 交還 KWin 失敗改為直接啟動時，不帶更新器自己繼承到的 WAYLAND_SOCKET，並記下這次開機不再一次性修復。
func TestStartFcitxFallbackDropsWaylandSocket(t *testing.T) {
	k := newFakeKWin(t)
	k.set(fcitxDesktop, true)
	k.noStart = true
	t.Setenv("WAYLAND_SOCKET", "27")
	oldLook, oldRun := lookSystemdRun, runSystemdRun
	t.Cleanup(func() { lookSystemdRun, runSystemdRun = oldLook, oldRun })
	var unit []string
	lookSystemdRun = func() (string, error) { return "/usr/bin/systemd-run", nil }
	runSystemdRun = func(_ context.Context, _ string, args []string) ([]byte, error) { unit = args; return nil, nil }
	if err := startFcitx(new(dbus.Conn), &shellRestart{KWinIM: fcitxDesktop, Env: []string{"WAYLAND_DISPLAY=wayland-0"}}); err != nil {
		t.Fatal(err)
	}
	if len(unit) == 0 || slices.ContainsFunc(unit, func(a string) bool { return strings.Contains(a, "WAYLAND_SOCKET") }) {
		t.Fatalf("直接啟動的參數：%q", unit)
	}
	if !slices.Contains(unit, "--setenv=WAYLAND_DISPLAY=wayland-0") {
		t.Fatalf("應保留 WAYLAND_DISPLAY：%q", unit)
	}
	if h := loadKWinHandback(kwinStateDir()); h.Phase != "failed" || h.Boot != "boot-1" {
		t.Fatalf("交還失敗應記下這次開機：%+v", h)
	}
}

// 0.2.4 以前的更新器在 Plasma Wayland 自己啟動的 Fcitx5（0c-fcitx5-*.service）：虛擬鍵盤是 Fcitx 時當成 KWin 管理。
func TestCaptureTreatsOurRestartUnitUnderKWinAsKWinManaged(t *testing.T) {
	k := newFakeKWin(t)
	k.set(fcitxDesktop, true)
	addProc(t, 1, "systemd", 0, "0::/init.scope\n", "")
	addProc(t, 123, "fcitx5", 1, restartCgroup, "WAYLAND_DISPLAY=wayland-0")
	if s, err := captureFcitx(123); err != nil || s.KWinIM != fcitxDesktop {
		t.Fatalf("我們啟動的 Fcitx5：%+v %v", s, err)
	}
	k.set("/usr/share/applications/com.github.maliit.keyboard.desktop", true)
	if s, _ := captureFcitx(123); s.KWinIM != "" {
		t.Fatalf("虛擬鍵盤不是 Fcitx 時照原本方式：%+v", s)
	}
	k.set(fcitxDesktop, true)
	addProc(t, 123, "fcitx5", 1, "0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-fcitx5.scope\n", "")
	if s, _ := captureFcitx(123); s.KWinIM != "" {
		t.Fatalf("不是我們的單元：%+v", s)
	}
	addProc(t, 123, "fcitx5", 1, restartCgroup, "")
	os.RemoveAll(filepath.Join(procRoot, "50")) // 沒有 KWin（GNOME、X11）
	if s, _ := captureFcitx(123); s.KWinIM != "" {
		t.Fatalf("沒有 kwin_wayland：%+v", s)
	}
}

type handbackCalls struct {
	exits    []uint64
	restored []*shellRestart
	answer   string
}

func stubHandback(t *testing.T, answer string) *handbackCalls {
	c := &handbackCalls{answer: answer}
	handbackPrepare = func(_ context.Context, _ *dbus.Conn, s *shellRestart) error {
		s.Group, s.IM, s.Program = "Default", "zuin", "kwrite"
		return nil
	}
	handbackExit = func(_ context.Context, _ *dbus.Conn, idle uint64) (string, error) {
		c.exits = append(c.exits, idle)
		if c.answer == "ready" {
			os.RemoveAll(filepath.Join(procRoot, "123"))
		}
		return c.answer, nil
	}
	handbackRestore = func(_ context.Context, _ *dbus.Conn, s *shellRestart) error {
		c.restored = append(c.restored, s)
		return nil
	}
	return c
}

// 一次性修復只在三個條件都成立時重啟：Plasma Wayland、虛擬鍵盤是 Fcitx、唯一的 Fcitx5 在 0c-fcitx5-*.service。
// KWin 自己啟動的 Fcitx5、其他桌面都不動；正在打字時下次再試；交還失敗的這次開機不再重試。
func TestRepairKWinFcitxOnlyForOurUnitUnderKWin(t *testing.T) {
	k := newFakeKWin(t)
	k.set(fcitxDesktop, true)
	base := kwinStateDir()
	addProc(t, 1, "systemd", 0, "0::/init.scope\n", "")

	// 正常：KWin 啟動的 Fcitx5。
	addProc(t, 123, "fcitx5", 50, kwinCgroup, "WAYLAND_SOCKET=27")
	c := stubHandback(t, "ready")
	repairKWinFcitx(base)
	if len(c.exits) != 0 {
		t.Fatal("KWin 啟動的 Fcitx5 不該重啟")
	}

	// 我們啟動的，但使用者正在打字：不重啟，下次再試。
	addProc(t, 123, "fcitx5", 1, restartCgroup, "")
	c.answer = "typing"
	repairKWinFcitx(base)
	if !slices.Equal(c.exits, []uint64{kwinHandbackIdle}) || len(c.restored) != 0 {
		t.Fatalf("打字中：%+v", c)
	}
	if _, err := os.Stat(kwinHandbackPath(base)); !os.IsNotExist(err) {
		t.Fatal("沒有退出時不留紀錄")
	}

	// 外殼更新進行中：交給更新流程。
	os.MkdirAll(filepath.Dir(shellJournal(base)), 0o700)
	os.WriteFile(shellJournal(base), []byte(`{"phase":"installing"}`), 0o600)
	c.answer, c.exits = "ready", nil
	repairKWinFcitx(base)
	if len(c.exits) != 0 {
		t.Fatal("外殼更新進行中不該重啟")
	}
	os.Remove(shellJournal(base))

	// 閒置：退出後交還 KWin，恢復原本的輸入法選擇。
	repairKWinFcitx(base)
	if len(c.exits) != 1 || len(c.restored) != 1 {
		t.Fatalf("應交還 KWin：%+v", c)
	}
	if r := c.restored[0]; r.KWinIM != fcitxDesktop || r.IM != "zuin" || r.Group != "Default" {
		t.Fatalf("恢復內容：%+v", r)
	}
	if _, err := os.Stat(kwinHandbackPath(base)); !os.IsNotExist(err) {
		t.Fatal("交還成功後刪除紀錄")
	}

	// 這次開機交還失敗過：不再重試；換了開機才再試。
	addProc(t, 123, "fcitx5", 1, restartCgroup, "")
	saveKWinHandback(base, kwinHandback{Boot: "boot-1", Phase: "failed"})
	c.exits = nil
	repairKWinFcitx(base)
	if len(c.exits) != 0 {
		t.Fatal("同一次開機失敗過不該重試")
	}
	currentBootID = func() string { return "boot-2" }
	repairKWinFcitx(base)
	if len(c.exits) != 1 {
		t.Fatal("下一次開機應再試")
	}
}

// 請 Fcitx5 退出後被中止：下一次喚起時沒有 Fcitx5，接著交給 KWin。
func TestRepairKWinFcitxResumesAfterInterruption(t *testing.T) {
	k := newFakeKWin(t)
	k.set(fcitxDesktop, true)
	base := kwinStateDir()
	c := stubHandback(t, "ready")
	saved := &shellRestart{KWinIM: fcitxDesktop, IM: "zuin", Args: []string{}}
	saveKWinHandback(base, kwinHandback{Boot: "boot-1", Phase: "stopping", Restart: saved})
	repairKWinFcitx(base)
	if len(c.exits) != 0 || len(c.restored) != 1 || c.restored[0].KWinIM != fcitxDesktop {
		t.Fatalf("應接著交給 KWin：%+v", c)
	}
	if _, err := os.Stat(kwinHandbackPath(base)); !os.IsNotExist(err) {
		t.Fatal("完成後刪除紀錄")
	}
}

// stopping 紀錄只屬於寫下它的那次開機：重新開機後 KWin 照常啟動 Fcitx5，舊紀錄要清掉；否則使用者之後自己結束
// Fcitx5（或換到不跑 Fcitx5 的工作階段）時，排程會拿上次開機的環境、群組與輸入法重新啟動它、改寫 kwinrc。
// 同一次開機已有別人啟動的 Fcitx5 也一樣；我們單元裡還沒結束的舊行程則留著紀錄等它結束。
func TestRepairKWinFcitxDropsStaleStoppingMark(t *testing.T) {
	saved := &shellRestart{KWinIM: fcitxDesktop, IM: "zuin", Args: []string{}}
	stopped := func(t *testing.T, boot string) (*fakeKWin, string, *handbackCalls) {
		k := newFakeKWin(t)
		k.set(fcitxDesktop, true)
		base := kwinStateDir()
		c := stubHandback(t, "ready")
		saveKWinHandback(base, kwinHandback{Boot: boot, Phase: "stopping", Restart: saved})
		return k, base, c
	}
	gone := func(t *testing.T, base string) {
		t.Helper()
		if _, err := os.Stat(kwinHandbackPath(base)); !os.IsNotExist(err) {
			t.Fatal("舊紀錄應清掉")
		}
	}

	t.Run("rebooted, KWin started Fcitx5, user quits it later", func(t *testing.T) {
		k, base, c := stopped(t, "boot-0")
		addProc(t, 123, "fcitx5", 50, kwinCgroup, "WAYLAND_SOCKET=27")
		repairKWinFcitx(base)
		gone(t, base)
		os.RemoveAll(filepath.Join(procRoot, "123")) // 使用者自己結束 Fcitx5
		repairKWinFcitx(base)
		if len(c.exits) != 0 || len(c.restored) != 0 || len(k.writes()) != 0 {
			t.Fatalf("不該重新啟動或改 kwinrc：%+v %v", c, k.writes())
		}
	})
	t.Run("rebooted into a session without Fcitx5", func(t *testing.T) {
		k, base, c := stopped(t, "boot-0")
		repairKWinFcitx(base)
		gone(t, base)
		if len(c.restored) != 0 || len(k.writes()) != 0 {
			t.Fatalf("上次開機的紀錄不該拿來啟動：%+v %v", c, k.writes())
		}
	})
	t.Run("record without a boot ID", func(t *testing.T) {
		_, base, c := stopped(t, "")
		repairKWinFcitx(base)
		gone(t, base)
		if len(c.restored) != 0 {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("same boot, someone else already started Fcitx5", func(t *testing.T) {
		k, base, c := stopped(t, "boot-1")
		addProc(t, 123, "fcitx5", 1, "0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-fcitx5.scope\n", "")
		repairKWinFcitx(base)
		gone(t, base)
		os.RemoveAll(filepath.Join(procRoot, "123"))
		repairKWinFcitx(base)
		if len(c.exits) != 0 || len(c.restored) != 0 || len(k.writes()) != 0 {
			t.Fatalf("%+v %v", c, k.writes())
		}
	})
	t.Run("same boot, switched to a session without KWin", func(t *testing.T) {
		k, base, c := stopped(t, "boot-1")
		os.RemoveAll(filepath.Join(procRoot, "50")) // 登出 Plasma、改登入 GNOME 或 X11
		repairKWinFcitx(base)
		gone(t, base)
		if len(c.restored) != 0 || len(k.writes()) != 0 {
			t.Fatalf("%+v %v", c, k.writes())
		}
	})
	t.Run("same boot, our old Fcitx5 is still exiting", func(t *testing.T) {
		_, base, c := stopped(t, "boot-1")
		addProc(t, 123, "fcitx5", 1, restartCgroup, "")
		if staleKWinHandback(loadKWinHandback(base), []int{123}) {
			t.Fatal("我們單元裡的舊行程還在：紀錄仍有效")
		}
		repairKWinFcitx(base) // 照原本的一次性修復再問一次，退出後交還 KWin
		if len(c.exits) != 1 || len(c.restored) != 1 || c.restored[0].KWinIM != fcitxDesktop {
			t.Fatalf("應交還 KWin：%+v", c)
		}
		gone(t, base)
	})
}

func TestUserSetsInputMethod(t *testing.T) {
	dir := t.TempDir()
	for body, want := range map[string]bool{
		"[Wayland]\nInputMethod[$d]\n": true,
		"[Wayland]\nInputMethod[$e]=/usr/share/applications/org.fcitx.Fcitx5.desktop\n": true,
		"[Wayland]\nInputMethod=\n":                                     true,
		"[Wayland]\nVirtualKeyboardEnabled=true\n[Xwayland]\nScale=1\n": false,
		"[Plugins]\nInputMethod=x\n":                                    false,
		"[Wayland][Sub]\nInputMethod=x\n":                               false,
		"":                                                              false,
	} {
		path := filepath.Join(dir, "kwinrc")
		os.WriteFile(path, []byte(body), 0o600)
		if got := userSetsInputMethod(path); got != want {
			t.Fatalf("%q：%v", body, got)
		}
	}
	if userSetsInputMethod(filepath.Join(dir, "missing")) {
		t.Fatal("沒有檔案")
	}
}

// 錯誤訊息帶出 kreadconfig／kwriteconfig 的 stderr。
func TestRunKConfigReportsStderr(t *testing.T) {
	tool := filepath.Join(t.TempDir(), "kwriteconfig6")
	os.WriteFile(tool, []byte("#!/bin/sh\necho 'kf.config.core: Failed to sync config' >&2\nexit 1\n"), 0o700)
	_, err := runKConfig(context.Background(), os.Environ(), tool, kwinrcArgs...)
	if err == nil || !strings.Contains(err.Error(), "Failed to sync config") || !strings.Contains(err.Error(), "kwriteconfig6") {
		t.Fatalf("錯誤：%v", err)
	}
}

// 交接紀錄的欄位名稱固定（更新中途被中止時下一版要讀得懂）。
func TestShellRestartRecordsInheritedKWinIM(t *testing.T) {
	b, _ := json.Marshal(shellRestart{KWinIM: launcherDesktop, KWinIMInherited: true})
	if !strings.Contains(string(b), `"kwin_input_method":"`+launcherDesktop+`"`) || !strings.Contains(string(b), `"kwin_input_method_inherited":true`) {
		t.Fatal(string(b))
	}
}

// 還原系統預設只刪 [Wayland] 的 InputMethod；kwinrc 是符號連結（dotfiles）時改連結指向的檔案，連結留著。
func TestRevertUserInputMethodKeepsTheRestAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles-kwinrc")
	body := "[Wayland]\nInputMethod=\nVirtualKeyboardEnabled=true\n\n[Plugins]\nInputMethod=keep\n"
	os.WriteFile(real, []byte(body), 0o640)
	link := filepath.Join(dir, "kwinrc")
	os.Symlink(real, link)
	if err := revertUserInputMethod(link); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(real)
	info, _ := os.Lstat(link)
	st, _ := os.Stat(real)
	if string(raw) != "[Wayland]\nVirtualKeyboardEnabled=true\n\n[Plugins]\nInputMethod=keep\n" || info.Mode()&os.ModeSymlink == 0 || st.Mode().Perm() != 0o640 {
		t.Fatalf("%q %v %v", raw, info.Mode(), st.Mode())
	}
	if err := revertUserInputMethod(filepath.Join(dir, "missing")); err != nil {
		t.Fatal(err)
	}
}
