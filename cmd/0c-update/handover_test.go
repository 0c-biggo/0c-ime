package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/atomicfile"
	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/update"
)

const handoverChildEnv = "ZC_HANDOVER_TEST"

// handoverChild：測試執行檔被當成套件裡的新版 0c-update 執行 install-staged。依環境變數決定新版「安裝」的結果，
// 並在 ran 檔記下收到的參數，讓父行程核對交接命令列。
func handoverChild(args []string) int {
	mode := os.Getenv(handoverChildEnv)
	if ran := os.Getenv("ZC_HANDOVER_RAN"); ran != "" {
		b, _ := json.Marshal(args)
		os.WriteFile(ran, b, 0o600)
	}
	if mode == "crash" {
		return 2 // 當掉：沒有結果檔
	}
	if reason, ok := strings.CutPrefix(mode, "raw:"); ok {
		// 不核對套件，直接回報 reason（Windows 的提權測試用：新版判斷要系統管理員）
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "-result" {
				atomicfile.WriteJSON(args[i+1], handoverResult{Schema: 1, Reason: reason, Error: "要改 TSF 註冊"})
			}
		}
		return 1
	}
	buildVersion = "0.2.16"
	installStagedRun = func(p *system, _ context.Context, root string, r *update.Release) (bool, error) {
		switch mode {
		case "busy":
			return false, fmt.Errorf("%w：Fcitx5 正在使用", autoupdate.ErrShellBusy)
		case "health":
			return false, fmt.Errorf("%w：新版引擎沒有接手", autoupdate.ErrShellHealth)
		case "failed":
			return false, errors.New("TSF 註冊命令失敗")
		case "pending":
			return true, nil
		}
		if !p.consent || p.shellIdle != 90*time.Second {
			return false, fmt.Errorf("參數沒有傳到：consent=%v idle=%s", p.consent, p.shellIdle)
		}
		return false, nil
	}
	if err := installStagedCommand(args); err != nil {
		return 1
	}
	return 0
}

// stagedBundle 建立通過 update.Verify 的 Linux 套件（只放必要檔案）。
func stagedBundle(t *testing.T, version string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "0c-"+version+"-linux-amd64")
	files := map[string]string{"install.py": "#", "bin/0c-server": "engine", "lib/lib0c.so": "addon", "bin/0c-update": "updater"}
	for _, n := range []string{"manifest.json", "lexicon.bin", "lm.bin", "NOTICE.txt", "report.txt"} {
		files["data/"+n] = n
	}
	listed := map[string]string{}
	for name, body := range files {
		writeFile(t, filepath.Join(root, filepath.FromSlash(name)), body)
		listed[name], _ = update.Hash(filepath.Join(root, filepath.FromSlash(name)))
	}
	b, _ := json.Marshal(update.Release{Schema: 1, Product: "0c", Version: version, Channel: "public-beta", OS: "linux", Arch: "amd64", Files: listed})
	writeFile(t, filepath.Join(root, "release.json"), string(b))
	if _, err := update.Verify(root, "linux", "amd64"); err != nil {
		t.Fatal(err)
	}
	return root
}

func handoverSetup(t *testing.T, mode string) (*system, string, string) {
	t.Helper()
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("交接的端到端測試用 Linux 套件")
	}
	t.Setenv(handoverChildEnv, mode)
	ran := filepath.Join(t.TempDir(), "ran.json")
	t.Setenv("ZC_HANDOVER_RAN", ran)
	old := stagedUpdater
	stagedUpdater = func(string) string { return os.Args[0] }
	t.Cleanup(func() { stagedUpdater = old; installCancelled = nil })
	t.Setenv("HOME", t.TempDir())
	return &system{base: basePath(), consent: true, shellIdle: 90 * time.Second}, stagedBundle(t, "0.2.16"), ran
}

// 舊的更新器只核對，安裝交給套件裡新版的 0c-update；交接命令列就是契約裡的那一串。
func TestHandOverInstallRunsTheNewUpdaterWithTheContract(t *testing.T) {
	p, root, ran := handoverSetup(t, "ok")
	r, _ := update.Verify(root, "linux", "amd64")
	pending, err := p.handOverInstall(context.Background(), root, r)
	if err != nil || pending {
		t.Fatalf("%v %v", pending, err)
	}
	var args []string
	b, _ := os.ReadFile(ran)
	json.Unmarshal(b, &args)
	result := filepath.Join(filepath.Dir(root), "handover-result.json")
	want := []string{"-handover", "1", "-root", root, "-version", "0.2.16", "-result", result, "-consent", "-idle-seconds", "90"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("contract changed:\n got %q\nwant %q", args, want)
	}
	var out handoverResult
	b, _ = os.ReadFile(result)
	if json.Unmarshal(b, &out) != nil || !out.OK || out.Version != "0.2.16" {
		t.Fatalf("result %s", b)
	}
}

// 新版回報的結果換回協調器認得的錯誤：組字中等一下、檢查失敗暫停該版、其他失敗；新版當掉沒有結果時是失敗，原版不動。
func TestHandOverInstallMapsResults(t *testing.T) {
	for mode, check := range map[string]func(bool, error) bool{
		"pending": func(p bool, err error) bool { return p && err == nil },
		"busy":    func(_ bool, err error) bool { return errors.Is(err, autoupdate.ErrShellBusy) },
		"health":  func(_ bool, err error) bool { return errors.Is(err, autoupdate.ErrShellHealth) },
		"failed": func(_ bool, err error) bool {
			return err != nil && !errors.Is(err, autoupdate.ErrShellHealth) && err.Error() == "TSF 註冊命令失敗"
		},
		"crash": func(_ bool, err error) bool { return err != nil && !errors.Is(err, autoupdate.ErrShellHealth) },
	} {
		t.Run(mode, func(t *testing.T) {
			p, root, _ := handoverSetup(t, mode)
			r, _ := update.Verify(root, "linux", "amd64")
			if pending, err := p.handOverInstall(context.Background(), root, r); !check(pending, err) {
				t.Fatalf("%v %v", pending, err)
			}
		})
	}
}

// 新版沒通過自我測試就不交給它：這一版暫停（health），原版不動，新版也沒被執行。
func TestHandOverInstallRequiresSelftest(t *testing.T) {
	p, root, ran := handoverSetup(t, "ok")
	withSelftest(t, selftestFail)
	r, _ := update.Verify(root, "linux", "amd64")
	if _, err := p.handOverInstall(context.Background(), root, r); !errors.Is(err, autoupdate.ErrShellHealth) {
		t.Fatal(err)
	}
	if _, err := os.Stat(ran); !os.IsNotExist(err) {
		t.Fatal("ran the unqualified updater")
	}
	withSelftest(t, selftestInconclusive)
	os.Remove(filepath.Join(p.base, "update", qualifyFile))
	if _, err := p.handOverInstall(context.Background(), root, r); err == nil || errors.Is(err, autoupdate.ErrShellHealth) {
		t.Fatalf("inconclusive must retry later, not fail the version: %v", err)
	}
}

// install-staged 自己再核對一次套件；版本不符、契約版本不認得都寫明結果，不安裝。
func TestInstallStagedRejectsMismatches(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("用 Linux 套件")
	}
	t.Setenv("HOME", t.TempDir())
	root := stagedBundle(t, "0.2.16")
	called := false
	old := installStagedRun
	installStagedRun = func(*system, context.Context, string, *update.Release) (bool, error) {
		called = true
		return false, nil
	}
	t.Cleanup(func() { installStagedRun = old })
	result := filepath.Join(t.TempDir(), "r.json")
	read := func() handoverResult {
		var r handoverResult
		b, _ := os.ReadFile(result)
		json.Unmarshal(b, &r)
		return r
	}
	if err := installStagedCommand([]string{"-handover", "1", "-root", root, "-version", "0.2.17", "-result", result}); err == nil || read().OK {
		t.Fatal("version mismatch installed")
	}
	if err := installStagedCommand([]string{"-handover", "2", "-root", root, "-version", "0.2.16", "-result", result}); err == nil || read().Reason != "unsupported" {
		t.Fatal("unknown contract version")
	}
	os.WriteFile(filepath.Join(root, "bin/0c-server"), []byte("tampered"), 0o700)
	if err := installStagedCommand([]string{"-handover", "1", "-root", root, "-version", "0.2.16", "-result", result}); err == nil || read().OK {
		t.Fatal("tampered bundle installed")
	}
	if called {
		t.Fatal("installed despite a mismatch")
	}
	if handsOver("0.2.14") || !handsOver("0.2.15-dev.1") || !handsOver("0.2.15") {
		t.Fatal("handover starts at 0.2.15")
	}
}
