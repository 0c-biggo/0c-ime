//go:build !windows && !darwin

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

func TestCaptureFcitxPreservesExplicitTracePreference(t *testing.T) {
	previous := procRoot
	procRoot = t.TempDir()
	t.Cleanup(func() { procRoot = previous })
	dir := filepath.Join(procRoot, "123")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte("fcitx5\x00-d\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"1", "0", ""} {
		env := []string{"DISPLAY=:1", "APP_TOKEN=private-test-token"}
		want := []string{"DISPLAY=:1"}
		if value != "" {
			want = append(want, "ZUIN_TRACE="+value, "ZUIN_TRACE_DIR=/tmp/user trace")
			env = append(env, want[1:]...)
		}
		if err := os.WriteFile(filepath.Join(dir, "environ"), []byte(strings.Join(env, "\x00")+"\x00"), 0600); err != nil {
			t.Fatal(err)
		}
		state, err := captureFcitx(123)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(state.Env, want) {
			t.Fatalf("紀錄偏好 %q 交接後應保留，其他環境不得保存：%v", value, state.Env)
		}
	}
}

func TestMappedShellRejectsOldOrDeletedMapping(t *testing.T) {
	path := "/home/test user/.local/lib/fcitx5/lib0c.so"
	device := unix.Mkdev(8, 1)
	line := fmt.Sprintf("7f00-7f10 r--p 00000000 08:01 123 %s\n", path)
	if !mappedShell([]byte(line), path, 123, device) {
		t.Fatal("new shell mapping not recognized")
	}
	for _, tc := range []struct {
		line string
		ino  uint64
		dev  uint64
	}{
		{line, 124, device}, {line, 123, unix.Mkdev(8, 2)},
		{fmt.Sprintf("7f00-7f10 r--p 00000000 08:01 123 %s (deleted)\n", path), 123, device},
	} {
		if mappedShell([]byte(tc.line), path, tc.ino, tc.dev) {
			t.Fatal("stale shell was accepted")
		}
	}
}

type selectionController struct {
	mu      sync.Mutex
	calls   []string
	restore bool
}

func (s *selectionController) record(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, value)
}
func (s *selectionController) CurrentInputMethodGroup() (string, *dbus.Error) {
	return "old-group", nil
}
func (s *selectionController) SwitchInputMethodGroup(group string) *dbus.Error {
	s.record("group:" + group)
	return nil
}
func (s *selectionController) SetCurrentIM(im string) *dbus.Error { s.record("im:" + im); return nil }
func (s *selectionController) Status() (string, *dbus.Error) {
	return fmt.Sprintf(`{"restore_supported":%t}`, s.restore), nil
}
func (s *selectionController) RestoreInputMethod(program, im string) *dbus.Error {
	s.record("restore:" + program + ":" + im)
	return nil
}

func TestRestoreInputSelectionWaitsForOriginalApplication(t *testing.T) {
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("requires private D-Bus")
	}
	daemon := exec.Command("dbus-daemon", "--session", "--nofork", "--print-address=1")
	stdout, err := daemon.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = daemon.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { daemon.Process.Kill(); daemon.Wait() }()
	address, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	server, err := dbus.Connect(strings.TrimSpace(address))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := dbus.Connect(strings.TrimSpace(address))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, name := range []string{"org.fcitx.Fcitx5", guardName} {
		if _, err := server.RequestName(name, dbus.NameFlagDoNotQueue); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, program string
		supported     bool
		expected      string
	}{
		{"reconnected field", "gtk-test", true, "group:Default,im:zuin,restore:gtk-test:zuin"},
		{"old journal", "", true, "group:Default,im:zuin"},
		{"rollback to old shell", "gtk-test", false, "group:Default,im:zuin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &selectionController{restore: tc.supported}
			if err := server.Export(fixture, "/controller", "org.fcitx.Fcitx.Controller1"); err != nil {
				t.Fatal(err)
			}
			if err := server.Export(fixture, "/update", guardInterface); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := restoreInputSelection(ctx, client, &shellRestart{Group: "Default", IM: "zuin", Program: tc.program}); err != nil {
				t.Fatal(err)
			}
			fixture.mu.Lock()
			actual := strings.Join(fixture.calls, ",")
			fixture.mu.Unlock()
			if actual != tc.expected {
				t.Fatalf("calls %q, want %q", actual, tc.expected)
			}
		})
	}
}

func TestControllerPollingDoesNotLaunchDefaultFcitx(t *testing.T) {
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("requires a private dbus-daemon")
	}
	dir := t.TempDir()
	services := filepath.Join(dir, "services")
	if err := os.Mkdir(services, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "unexpected-activation")
	launcher := filepath.Join(dir, "default-fcitx")
	quotedMarker := "'" + strings.ReplaceAll(marker, "'", "'\\''") + "'"
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\ntouch -- "+quotedMarker+"\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	service := "[D-BUS Service]\nName=org.fcitx.Fcitx5\nExec=" + launcher + "\n"
	if err := os.WriteFile(filepath.Join(services, "org.fcitx.Fcitx5.service"), []byte(service), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "bus.conf")
	xml := fmt.Sprintf(`<busconfig><type>session</type><listen>unix:tmpdir=%s</listen><auth>EXTERNAL</auth><apparmor mode="disabled"/><servicedir>%s</servicedir><policy context="default"><allow send_destination="*"/><allow receive_sender="*"/><allow own="*"/></policy></busconfig>`, dir, services)
	if err := os.WriteFile(config, []byte(xml), 0600); err != nil {
		t.Fatal(err)
	}
	daemon := exec.Command("dbus-daemon", "--config-file="+config, "--nofork", "--print-address=1")
	stdout, err := daemon.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { daemon.Process.Kill(); daemon.Wait() }()
	address, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dbus.Connect(strings.TrimSpace(address))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if controller(ctx, conn, "State").Err == nil {
		t.Fatal("missing Fcitx must not appear ready")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("polling launched the unconfigured D-Bus activation service")
	}
	// Positive control: the same bus really has an activation service. An ordinary
	// call launches it; only the updater's polling must refrain from doing so.
	conn.Object("org.fcitx.Fcitx5", "/controller").CallWithContext(ctx, "org.fcitx.Fcitx.Controller1.State", 0)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("activation fixture did not run: %v", err)
	}
}

func TestOldPendingShellHasManualFinishAction(t *testing.T) {
	s := &autoupdate.State{Stage: autoupdate.StagePending, Target: &autoupdate.Target{Kind: "shell", Version: "0.1.0-dev.40"}, Blocked: &autoupdate.Blocked{Reason: "needs_relogin"}}
	if actionFor(s) != "finish" {
		t.Fatal("old staged shell must be manually finishable")
	}
}

func TestUpdaterSurvivesLegacyShellClosingOutput(t *testing.T) {
	if os.Getenv("ZC_TEST_CLOSED_UPDATE_OUTPUT") == "1" {
		// 舊版外殼退出後還會有安裝／恢復訊息。使用真 stdout fd 1，
		// 覆蓋 Go 預設會以 SIGPIPE 結束程序的路徑。
		fmt.Println("restoring Fcitx after install")
		if err := os.WriteFile(os.Getenv("ZC_TEST_UPDATE_COMPLETED"), []byte("restored"), 0600); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()
	defer writer.Close()
	marker := filepath.Join(t.TempDir(), "completed")
	child := exec.Command(os.Args[0], "-test.run=^TestUpdaterSurvivesLegacyShellClosingOutput$")
	child.Env = append(os.Environ(), "ZC_TEST_CLOSED_UPDATE_OUTPUT=1", "ZC_TEST_UPDATE_COMPLETED="+marker)
	child.Stdout = writer
	if err := child.Run(); err != nil {
		t.Fatalf("legacy output closed during handoff: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("updater did not finish restoring the shell")
	}
}

// KDE Plasma Wayland：Fcitx5 由 KWin 帶著 WAYLAND_SOCKET 啟動。交接時記下 kwinrc 的 InputMethod，
// 重新啟動時交還 KWin（先清空再寫回並通知），不自己執行 fcitx5，也不保存那個 fd。
func TestKWinLaunchedFcitxIsRestartedThroughKWin(t *testing.T) {
	previous, savedTool, savedRun, savedAvail, savedEmit := procRoot, kconfigTool, runKConfig, kwinIMAvailable, emitKConfigChanged
	procRoot = t.TempDir()
	t.Cleanup(func() {
		procRoot, kconfigTool, runKConfig, kwinIMAvailable, emitKConfigChanged = previous, savedTool, savedRun, savedAvail, savedEmit
	})
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	dir := filepath.Join(procRoot, "123")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	const desktop = "/usr/share/applications/org.fcitx.Fcitx5.desktop"
	if err := os.WriteFile(filepath.Join(config, "kwinrc"), []byte("[Wayland]\nInputMethod[$e]="+desktop+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte("/usr/bin/fcitx5\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "environ"), []byte("WAYLAND_SOCKET=27\x00XDG_RUNTIME_DIR=/run/user/1000\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(procRoot, "100")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte("123 (fcitx5) S 100 123 123 0"), 0600); err != nil {
		t.Fatal(err)
	}
	var ran []string
	kconfigTool = func(names ...string) (string, error) { return "/usr/bin/" + names[0], nil }
	runKConfig = func(_ context.Context, _ []string, path string, args ...string) ([]byte, error) {
		ran = append(ran, filepath.Base(path)+" "+strings.Join(args, " "))
		if filepath.Base(path) == "kreadconfig6" {
			return []byte(desktop + "\n"), nil
		}
		if args[len(args)-1] == desktop { // KWin 讀到寫回的值，重新啟動 fcitx5
			os.Mkdir(dir, 0700)
			os.WriteFile(filepath.Join(dir, "stat"), []byte("123 (fcitx5) S 100 123 123 0"), 0600)
			os.WriteFile(filepath.Join(dir, "comm"), []byte("fcitx5\n"), 0600)
		}
		return nil, nil
	}
	kwinIMAvailable = func(context.Context, *dbus.Conn) (bool, error) { return false, nil }
	var emitted int
	emitKConfigChanged = func(_ *dbus.Conn, file, group, key string) error {
		if file != "kwinrc" || group != "Wayland" || key != "InputMethod" {
			t.Fatalf("通知 %s %s %s", file, group, key)
		}
		emitted++
		return nil
	}
	// 其他合成器（例如 Weston）也可能以 WAYLAND_SOCKET 啟動 Fcitx5：父行程不是 kwin_wayland 時不碰 kwinrc。
	for _, other := range []string{"weston", "gnome-shell", "systemd"} {
		if err := os.WriteFile(filepath.Join(parent, "comm"), []byte(other+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		state, err := captureFcitx(123)
		if err != nil {
			t.Fatal(err)
		}
		if state.KWinIM != "" || len(ran) != 0 {
			t.Fatalf("父行程 %s 不該走 KWin 路徑：%+v %q", other, state, ran)
		}
	}
	// 「Fcitx 5 Wayland Launcher」：fcitx5 由 D-Bus 叫起（沒有 WAYLAND_SOCKET、父行程不是 KWin），
	// KWin 的子行程是 fcitx5-wayland-launcher。這時也要交還 KWin。
	if err := os.WriteFile(filepath.Join(dir, "environ"), []byte("XDG_RUNTIME_DIR=/run/user/1000\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(procRoot, "200")
	if err := os.Mkdir(launcher, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(launcher, "stat"), []byte("200 (fcitx5-wayland-) S 100 200 200 0"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(launcher, "comm"), []byte("fcitx5-wayland-\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if state, err := captureFcitx(123); err != nil || state.KWinIM != "" {
		t.Fatalf("launcher 的父行程不是 kwin_wayland 時不走 KWin 路徑：%+v %v", state, err)
	}
	if err := os.WriteFile(filepath.Join(parent, "comm"), []byte("kwin_wayland\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if state, err := captureFcitx(123); err != nil || state.KWinIM != desktop {
		t.Fatalf("Wayland Launcher：%+v %v", state, err)
	}
	os.RemoveAll(launcher)
	if err := os.WriteFile(filepath.Join(dir, "environ"), []byte("WAYLAND_SOCKET=27\x00XDG_RUNTIME_DIR=/run/user/1000\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	ran = nil
	state, err := captureFcitx(123)
	if err != nil {
		t.Fatal(err)
	}
	if state.KWinIM != desktop || slices.ContainsFunc(state.Env, func(e string) bool { return strings.HasPrefix(e, "WAYLAND_SOCKET=") }) {
		t.Fatalf("KWin 啟動的 Fcitx5：%+v", state)
	}
	os.RemoveAll(dir) // Fcitx5 已交接退出
	ran = nil
	if err = startFcitx(new(dbus.Conn), state); err != nil {
		t.Fatal(err)
	}
	// 不用 kwriteconfig --notify（Plasma 5 的 kwriteconfig5 沒有）：寫檔後自己送 KConfig 的變更通知。
	key := "--file kwinrc --group Wayland --key InputMethod"
	want := []string{"kwriteconfig6 " + key + " ", "kwriteconfig6 " + key + " --type path " + desktop}
	if !slices.Equal(ran, want) || emitted != 2 {
		t.Fatalf("應由 KWin 重新啟動：%q，通知 %d 次", ran, emitted)
	}
}
