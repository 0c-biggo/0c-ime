//go:build !windows

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"github.com/0c-biggo/0c-ime/internal/installs"
	"github.com/0c-biggo/0c-ime/internal/neural"
)

func TestInstallWatchEnabledOnlyForLinuxSystemInstall(t *testing.T) {
	root := t.TempDir()
	// Fedora Silverblue：/home 是 /var/home 的符號連結，$HOME 寫 /home/<user>，執行檔的實際路徑在 /var/home。
	realHome := filepath.Join(root, "var", "home", "u")
	os.MkdirAll(realHome, 0o755)
	os.MkdirAll(filepath.Join(root, "home"), 0o755)
	home := filepath.Join(root, "home", "u")
	if err := os.Symlink(realHome, home); err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(root, "usr")
	l := installs.Layout{Home: home, Libexec: filepath.Join(prefix, "libexec", "0c"), Data: filepath.Join(prefix, "share", "0c"), Bin: filepath.Join(prefix, "bin")}
	systemData := filepath.Join(prefix, "share", "0c", "data")
	officialData := filepath.Join(realHome, ".local", "share", "zuin", "data")
	devData := filepath.Join(root, "tmp", "share", "0c", "data")
	for _, d := range []string{systemData, officialData, devData} {
		os.MkdirAll(d, 0o755)
	}
	official := filepath.Join(realHome, ".local", "share", "zuin", "versions", "0.2.3", "bin", "0c-server")
	officialBin := filepath.Join(realHome, ".local", "bin", "0c-server")
	system := filepath.Join(l.Libexec, "0c-server")
	dev := filepath.Join(root, "tmp", "go-build", "0c-server")
	for _, exe := range []string{official, officialBin, system, dev} {
		os.MkdirAll(filepath.Dir(exe), 0o755)
		os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755)
	}
	for _, c := range []struct {
		name, goos, exe, data string
		marker, want          bool
	}{
		{"官網安裝的版本目錄（家目錄是符號連結）", "linux", official, officialData, false, false},
		{"官網安裝的 ~/.local/bin（家目錄是符號連結）", "linux", officialBin, officialData, false, false},
		{"官網安裝、字典旁卻有發行版標記：一律不開", "linux", official, officialData, true, false},
		{"官網安裝的引擎指定系統字典（有標記）：一律不開", "linux", officialBin, systemData, true, false},
		{"系統安裝在建置時設定的 libexec", "linux", system, systemData, false, true},
		{"系統安裝有發行版標記", "linux", system, systemData, true, true},
		{"/tmp 的開發建置", "linux", dev, devData, false, false},
		{"別的位置、字典旁有發行版標記（發行版自訂 libexec）", "linux", dev, devData, true, true},
		{"macOS 的 App 由 0c-update 交接", "darwin", system, systemData, true, false},
		{"Windows", "windows", system, systemData, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			marker := distributionMarker(c.data)
			os.Remove(marker)
			if c.marker {
				os.WriteFile(marker, []byte(`{"schema":1,"updates":"package-manager"}`), 0o644)
				defer os.Remove(marker)
			}
			if got := installWatchEnabled(c.goos, c.exe, c.data, l); got != c.want {
				t.Fatalf("installWatchEnabled = %v，應為 %v", got, c.want)
			}
		})
	}
	if distributionMarker(systemData) != filepath.Join(prefix, "share", "0c", "distribution.json") {
		t.Fatalf("marker path %s", distributionMarker(systemData))
	}
}

// 檔案換掉後要連續兩次看到同一個新狀態才交接（套件可能還沒裝完）；核對失敗的那一組不再重試，檔案再變才重新考慮。
func TestInstallWatchWaitsForStableFilesAndSkipsRejected(t *testing.T) {
	dir := t.TempDir()
	exe, manifest := filepath.Join(dir, "0c-server"), filepath.Join(dir, "manifest.json")
	missing := filepath.Join(dir, "neural", "select", "manifest.json")
	os.WriteFile(exe, []byte("v1"), 0o755)
	os.WriteFile(manifest, []byte("{}"), 0o644)
	start := time.Now()
	w := newInstallWatch([]string{exe, manifest, missing}, nil, time.Minute, start)
	at := func(m int) time.Time { return start.Add(time.Duration(m) * time.Minute) }
	poll := func(m int) (bool, bool) { return w.poll(at(m)) }
	if r, c := w.poll(start.Add(30 * time.Second)); r || c {
		t.Fatal("polled before interval")
	}
	if r, c := poll(1); r || c {
		t.Fatal("unchanged files reported")
	}
	replace := func(path, body string) { // 像 dpkg：寫新檔再改名蓋過去（新 inode）
		os.WriteFile(path+".dpkg-new", []byte(body), 0o755)
		os.Rename(path+".dpkg-new", path)
	}
	replace(exe, "v2")
	if r, c := poll(2); r || !c {
		t.Fatalf("first sight of change: ready=%v changed=%v", r, c)
	}
	replace(manifest, `{"x":1}`) // 還在裝：又變了
	if r, c := poll(3); r || !c {
		t.Fatalf("still changing: ready=%v changed=%v", r, c)
	}
	if r, _ := poll(4); !r {
		t.Fatal("stable change not ready")
	}
	step, stamp := w.nextStep()
	if step != watchCheck {
		t.Fatalf("step %d", step)
	}
	if !w.finish(stamp, &checkError{err: errors.New("exit status 1")}, at(4)) {
		t.Fatal("definite failure not rejected")
	}
	for m := 5; m < 8; m++ {
		if r, _ := poll(m); r {
			t.Fatal("rejected files retried without another change")
		}
	}
	// 發行版另外裝了模型套件：原本不存在的檔案出現也算。
	os.MkdirAll(filepath.Dir(missing), 0o755)
	os.WriteFile(missing, []byte("{}"), 0o644)
	if r, c := poll(8); r || !c {
		t.Fatal("new file not noticed")
	}
	if r, _ := poll(9); !r {
		t.Fatal("new stable files not ready")
	}
	// 執行中的程式以 /proc/self/exe 為準：啟動當下檔案正好被換掉時也看得出來。
	running, _ := os.Stat(manifest)
	w2 := newInstallWatch([]string{exe}, running, time.Minute, start)
	w2.poll(at(1))
	if r, _ := w2.poll(at(2)); !r {
		t.Fatal("running binary differs from the file on disk but was not noticed")
	}
}

// 寫一份已編譯的小字典（含 manifest.json）；回傳 manifest 的 SHA-256。
func writeCompiledData(t *testing.T, data string) string {
	t.Helper()
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	lex, err := lexicon.Build([]lexicon.Entry{{Surface: "麻婆", Reading: zhuyin.ParseReading("ㄇㄚˊ ㄆㄛˊ"), PRW: 1}})
	if err != nil {
		t.Fatal(err)
	}
	sys, err := lm.BuildCompact(map[string]float64{"麻婆": 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]any{}
	for name, body := range map[string][]byte{"lexicon.bin": lex.Encode(), "lm.bin": sys.Encode()} {
		if err := os.WriteFile(filepath.Join(data, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
		files[name] = map[string]string{"sha256": fmt.Sprintf("%x", sha256.Sum256(body))}
	}
	manifest, _ := json.Marshal(map[string]any{"format": 1, "files": files})
	if err := os.WriteFile(filepath.Join(data, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(manifest))
}

func healthOf(t *testing.T, sock string) map[string]string {
	t.Helper()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if err := ipc.WriteFrame(c, ipc.Envelope{Op: ipc.OpHealth}); err != nil {
		t.Fatal(err)
	}
	var r ipc.Reply
	if err := ipc.ReadFrame(c, &r); err != nil || !r.OK {
		t.Fatalf("health: %+v %v", r, err)
	}
	return r.Info
}

// 系統安裝（Debian）：沒有內附 Python 時用系統的 python3，模型可以放在個人目錄；找到模型卻沒有 Python
// 時回報 runtime_missing，照常用統計引擎打字。
func TestSystemInstallNeuralDiscovery(t *testing.T) {
	binary := buildServer(t)
	root := t.TempDir()
	data := filepath.Join(root, "share", "0c", "data")
	sha := writeCompiledData(t, data)
	neuralDir := filepath.Join(root, "share", "0c", "neural")
	os.MkdirAll(neuralDir, 0o755)
	os.WriteFile(filepath.Join(neuralDir, "sequence_runtime.py"), nil, 0o644)
	os.WriteFile(filepath.Join(neuralDir, "punct_runtime.py"), nil, 0o644)
	userDir := filepath.Join(root, "home", "zuin")
	// 核對得過的模型套件（雜湊與字典配對都對；模型內容是假的，worker 不會真的就緒）。
	for name, manifest := range map[string]map[string]any{
		"select": {"schema": "zuin-sequence-runtime-v1", "context_policy": "confirmed_left_64", "dictionary_sha256": sha, "weight": 1},
		"punct":  {"schema": neural.PunctSchema},
	} {
		p := filepath.Join(userDir, "models", sha, name)
		os.MkdirAll(p, 0o755)
		files := map[string]string{}
		for _, f := range []string{"model.onnx", "vocab.json"} {
			os.WriteFile(filepath.Join(p, f), []byte(f), 0o644)
			files[f] = fmt.Sprintf("%x", sha256.Sum256([]byte(f)))
		}
		manifest["files"] = files
		b, _ := json.Marshal(manifest)
		os.WriteFile(filepath.Join(p, "manifest.json"), b, 0o644)
	}
	os.WriteFile(filepath.Join(userDir, "settings.json"), []byte(`{"punctuation":true}`), 0o600)
	run := func(t *testing.T, python string) map[string]string {
		sock := filepath.Join(t.TempDir(), "0c.sock")
		server := exec.Command(binary, "-data", data, "-user", filepath.Join(userDir, "user.json"), "-socket", sock,
			"-system-python", python, "-install-watch", "off")
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { server.Process.Signal(os.Interrupt); server.Wait() }()
		waitDial(t, sock)
		return healthOf(t, sock)
	}
	t.Run("沒有 Python", func(t *testing.T) {
		// 照樣以系統 Python 的路徑啟動 worker：執行檔不存在是 runtime_missing（10 分鐘重試，之後裝好 python3
		// 不必重啟引擎）；模型來源照實回報。
		sock := filepath.Join(t.TempDir(), "0c.sock")
		server := exec.Command(binary, "-data", data, "-user", filepath.Join(userDir, "user.json"), "-socket", sock,
			"-system-python", filepath.Join(root, "no", "python3"), "-install-watch", "off")
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { server.Process.Signal(os.Interrupt); server.Wait() }()
		waitDial(t, sock)
		info := healthOf(t, sock)
		for deadline := time.Now().Add(5 * time.Second); info["selection_state"] == "loading" || info["punctuation_state"] == "loading"; info = healthOf(t, sock) {
			if time.Now().After(deadline) {
				t.Fatalf("still loading: %v", info)
			}
			time.Sleep(20 * time.Millisecond)
		}
		want := map[string]string{"neural_startup_state": "runtime_missing", "selection_state": "runtime_missing",
			"punctuation_state": "runtime_missing", "neural_python": "missing", "selection_source": "user", "punctuation_source": "user"}
		for k, v := range want {
			if info[k] != v {
				t.Errorf("%s = %q，應為 %q（%v）", k, info[k], v, info)
			}
		}
	})
	t.Run("系統 Python 與個人目錄的模型", func(t *testing.T) {
		info := run(t, "/bin/sh") // 存在的執行檔；worker 腳本是空的，啟動後沒有就緒就退回
		want := map[string]string{"neural_python": "system", "neural_startup_state": "enabled", "selection_source": "user", "punctuation_source": "user"}
		for k, v := range want {
			if info[k] != v {
				t.Errorf("%s = %q，應為 %q（%v）", k, info[k], v, info)
			}
		}
	})
}

// 測試用的 stderr 收集器：引擎 exec 成新版之後仍寫同一個 fd，讀寫要上鎖。
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func buildServerVersion(t *testing.T, version string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "0c-server")
	if out, err := exec.Command("go", "build", "-ldflags", "-X main.buildVersion="+version, "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return binary
}

// 像 dpkg 一樣換檔：寫到 .dpkg-new 再改名蓋過去，執行中的引擎留著舊的 inode。
func installFile(t *testing.T, src, dst string) {
	t.Helper()
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst+".dpkg-new", body, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dst+".dpkg-new", dst); err != nil {
		t.Fatal(err)
	}
}

// #181：apt upgrade 換掉 /usr 下的 0c-server 之後，引擎在使用者停手、沒有組字時自己交接到新版：
// 交接前的學習存檔、連線斷開，外殼重連後由新版服務；組字中不交接；新執行檔載入不了字典時不交接。
func TestSystemInstallHandsOverAfterPackageUpgrade(t *testing.T) {
	v1, v2 := buildServerVersion(t, "v1"), buildServerVersion(t, "v2")
	root := t.TempDir()
	exe := filepath.Join(root, "libexec", "0c", "0c-server")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	installFile(t, v1, exe)
	data := filepath.Join(root, "share", "0c", "data")
	writeCompiledData(t, data)
	user := filepath.Join(root, "home", "user.json")
	os.MkdirAll(filepath.Dir(user), 0o700)
	sock := filepath.Join(root, "0c.sock")
	logs := &lockedBuffer{}
	server := exec.Command(exe, "-data", data, "-user", user, "-socket", sock,
		"-install-watch", "on", "-install-watch-interval", "200ms", "-install-watch-idle", "500ms")
	server.Stderr = logs
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { server.Process.Signal(os.Interrupt); server.Wait() }()
	waitDial(t, sock)
	pid := healthOf(t, sock)["pid"]

	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	call := func(c net.Conn, env ipc.Envelope) (ipc.Reply, error) {
		c.SetDeadline(time.Now().Add(3 * time.Second))
		if err := ipc.WriteFrame(c, env); err != nil {
			return ipc.Reply{}, err
		}
		var r ipc.Reply
		err := ipc.ReadFrame(c, &r)
		return r, err
	}
	must := func(c net.Conn, env ipc.Envelope) ipc.Reply {
		t.Helper()
		r, err := call(c, env)
		if err != nil || !r.OK {
			t.Fatalf("%+v %v\n%s", r, err, logs.String())
		}
		return r
	}
	open := must(c, ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version})
	key := func(c net.Conn, session uint64, k string) ipc.Reply {
		return must(c, ipc.Envelope{Op: ipc.OpHandle, Session: session, Req: &proto.Request{Type: proto.Key, Key: k}})
	}
	for _, k := range "a86qi6" {
		key(c, open.Session, string(k))
	}
	if r := key(c, open.Session, proto.KeyEnter); r.Snapshot == nil || r.Snapshot.Commit != "麻婆" {
		t.Fatalf("commit: %+v", r)
	}
	// 新的執行檔壞了（例如套件裝到一半）：不交接，舊引擎照常服務。
	broken := filepath.Join(t.TempDir(), "broken")
	os.WriteFile(broken, []byte("#!/bin/sh\nexit 1\n"), 0o755)
	installFile(t, broken, exe)
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(logs.String(), "暫不交接"); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("broken binary was not checked:\n%s", logs.String())
		}
	}
	if info := healthOf(t, sock); info["version"] != "v1" || info["draining"] != "false" {
		t.Fatalf("handed over to a broken binary: %v", info)
	}
	// 組字中：不交接。
	for _, k := range "a86" {
		key(c, open.Session, string(k))
	}
	installFile(t, v2, exe)
	time.Sleep(2 * time.Second)
	if info := healthOf(t, sock); info["version"] != "v1" || info["composing"] != "1" {
		t.Fatalf("handed over while composing: %v", info)
	}
	for _, k := range "qi6" {
		key(c, open.Session, string(k))
	}
	if r := key(c, open.Session, proto.KeyEnter); r.Snapshot == nil || r.Snapshot.Commit != "麻婆" {
		t.Fatalf("commit while upgrade pending: %+v", r)
	}
	// 停手後交接：連線被斷開（外殼視為引擎切換），學習已存檔。
	c.SetDeadline(time.Now().Add(15 * time.Second))
	var r ipc.Reply
	if err := ipc.ReadFrame(c, &r); err == nil {
		t.Fatalf("connection not closed by handover: %+v", r)
	} else if err != io.EOF && !strings.Contains(err.Error(), "reset") && !strings.Contains(err.Error(), "closed") {
		t.Fatalf("waiting for handover: %v\n%s", err, logs.String())
	}
	// 外殼重連：新版在同一個行程（exec）服務。交接中的舊引擎可能還接受連線（存檔後才關端點），重試到新版回應為止。
	var info map[string]string
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if c, err := net.Dial("unix", sock); err == nil {
			r, err := call(c, ipc.Envelope{Op: ipc.OpHealth})
			c.Close()
			if err == nil && r.OK && r.Info["version"] == "v2" {
				info = r.Info
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("new engine not serving:\n%s", logs.String())
		}
	}
	// 交接前的學習在斷線後、放鎖前存檔，新版讀得到。
	if b, err := os.ReadFile(user); err != nil || !strings.Contains(string(b), "麻婆") {
		t.Fatalf("learning not saved before handover: %v %s\n%s", err, b, logs.String())
	}
	if info["pid"] != pid || info["personal_store"] != "loaded" || info["personal_lock"] == "busy" {
		t.Fatalf("handover state: pid %s→%s %v\n%s", pid, info["pid"], info, logs.String())
	}
	c2, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	open2 := must(c2, ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version})
	for _, k := range "a86qi6" {
		key(c2, open2.Session, string(k))
	}
	if r := key(c2, open2.Session, proto.KeyEnter); r.Snapshot == nil || r.Snapshot.Commit != "麻婆" {
		t.Fatalf("typing after handover: %+v", r)
	}
	if !strings.Contains(logs.String(), "交接到新版") || !strings.Contains(logs.String(), "換成新的執行檔") {
		t.Fatalf("handover not logged:\n%s", logs.String())
	}
}

// 核對通過的那一組記下來：交接逾時（一直在組字）之後直接再交接，不再跑一次 -check-data；核對進行中不重複開始。
// 暫時性失敗（逾時、執行不了）以加倍的間隔重試，次數用完才拒絕；確定載入不了立刻拒絕到檔案再變。
func TestInstallWatchRemembersPassedCheckAndRetriesTransientFailures(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "0c-server")
	os.WriteFile(exe, []byte("v1"), 0o755)
	start := time.Now()
	at := func(d time.Duration) time.Time { return start.Add(d) }
	w := newInstallWatch([]string{exe}, nil, time.Minute, start)
	os.WriteFile(exe+".new", []byte("v2 longer"), 0o755)
	os.Rename(exe+".new", exe)
	w.poll(at(time.Minute))
	if r, _ := w.poll(at(2 * time.Minute)); !r {
		t.Fatal("stable change not ready")
	}
	step, stamp := w.nextStep()
	if step != watchCheck {
		t.Fatalf("first step %d", step)
	}
	if again, _ := w.nextStep(); again != watchWait {
		t.Fatalf("second check started while the first is running: %d", again)
	}
	transient := &checkError{err: context.DeadlineExceeded, transient: true}
	now := at(2 * time.Minute)
	for i := 1; i < installCheckRetries; i++ {
		if w.finish(stamp, transient, now) {
			t.Fatalf("transient failure %d rejected", i)
		}
		if r, _ := w.poll(now.Add(w.interval<<i - time.Second)); r {
			t.Fatalf("retry %d before backoff", i)
		}
		now = now.Add(w.interval << i)
		if r, _ := w.poll(now); !r {
			t.Fatalf("retry %d not ready after backoff", i)
		}
		if step, stamp = w.nextStep(); step != watchCheck {
			t.Fatalf("retry %d step %d", i, step)
		}
	}
	if !w.finish(stamp, transient, now) {
		t.Fatal("transient failures not rejected after the retries ran out")
	}
	if r, _ := w.poll(now.Add(time.Hour)); r {
		t.Fatal("rejected files retried")
	}
	// 檔案又變了（套件修好重裝）：重新核對，通過後下一次 poll 立刻交接；交接逾時後再交接也不必再核對。
	os.WriteFile(exe+".new", []byte("v3 even longer"), 0o755)
	os.Rename(exe+".new", exe)
	now = now.Add(2 * time.Hour)
	w.poll(now)
	now = now.Add(time.Minute)
	if r, _ := w.poll(now); !r {
		t.Fatal("new files not ready")
	}
	if step, stamp = w.nextStep(); step != watchCheck {
		t.Fatalf("step %d", step)
	}
	if w.finish(stamp, nil, now.Add(10*time.Second)) {
		t.Fatal("passed check rejected")
	}
	if r, _ := w.poll(now.Add(10 * time.Second)); !r {
		t.Fatal("passed check did not hand over on the next tick")
	}
	for i := 0; i < 3; i++ { // 交接逾時之後的每一次
		if step, _ := w.nextStep(); step != watchHandOver {
			t.Fatalf("passed files checked again: step %d", step)
		}
	}
}

// -check-data 的失敗分兩種：新程式自己回報載入失敗是確定的；逾時、執行不了是暫時的。輸出依 UTF-8 字元截斷。
func TestCheckNewEngineClassifiesFailures(t *testing.T) {
	dir := t.TempDir()
	script := func(name, body string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755)
		return p
	}
	long := strings.Repeat("字", 150) // 450 位元組
	fails := script("fails", "echo '"+long+"' >&2\nexit 1")
	slow := script("slow", "exec sleep 30")
	ok := script("ok", "echo 資料包可載入")
	var check *checkError
	err := checkNewEngine(context.Background(), fails, dir, time.Minute)
	if !errors.As(err, &check) || check.transient {
		t.Fatalf("load failure: %#v", err)
	}
	if !utf8.ValidString(check.out) || len(check.out) > 300 || !strings.HasPrefix(long, check.out) || len(check.out) != 300 {
		t.Fatalf("output cut in the middle of a character: %d bytes %q", len(check.out), check.out)
	}
	started := time.Now()
	if err := checkNewEngine(context.Background(), slow, dir, 200*time.Millisecond); !errors.As(err, &check) || !check.transient {
		t.Fatalf("timeout: %#v", err)
	}
	if time.Since(started) > 10*time.Second {
		t.Fatal("timeout not enforced")
	}
	if err := checkNewEngine(context.Background(), filepath.Join(dir, "missing"), dir, time.Minute); !errors.As(err, &check) || !check.transient {
		t.Fatalf("not executable: %#v", err)
	}
	if err := checkNewEngine(context.Background(), ok, dir, time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := truncateUTF8("ab字", 3); got != "ab" {
		t.Fatalf("truncateUTF8 = %q", got)
	}
}

// 交接時收到結束訊號：不論訊號已經處理完、還是剛送進通道還沒處理，stop 都要回報收到（不能 exec）。
func TestWatchSignalsNeverMissesASignalBeforeStop(t *testing.T) {
	for i := 0; i < 2000; i++ {
		sig := make(chan os.Signal, 1)
		var calls atomic.Int32
		stop := watchSignals(sig, func() { calls.Add(1) }, func(chan<- os.Signal) {})
		if i%2 == 0 {
			sig <- syscall.SIGTERM // 剛到、處理的 goroutine 可能還沒拿到
		}
		if got := stop(); got != (i%2 == 0) {
			t.Fatalf("round %d: signaled = %v", i, got)
		}
	}
	// 真的訊號：Notify 之後送給自己。
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGHUP)
	closed := make(chan struct{})
	stop := watchSignals(sig, func() { close(closed) }, signal.Stop)
	syscall.Kill(os.Getpid(), syscall.SIGHUP)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("signal not handled")
	}
	if !stop() {
		t.Fatal("handled signal not reported")
	}
	sig = make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGHUP)
	if watchSignals(sig, func() {}, signal.Stop)() {
		t.Fatal("no signal but reported one")
	}
}

// 新執行檔的核對在背景跑：核對卡住（最長 2 分鐘）時，引擎照樣依 -idle-exit 結束，不被擋住。
func TestSystemInstallCheckDoesNotBlockTheEngineLoop(t *testing.T) {
	v1 := buildServerVersion(t, "v1")
	root := t.TempDir()
	exe := filepath.Join(root, "libexec", "0c", "0c-server")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	installFile(t, v1, exe)
	data := filepath.Join(root, "share", "0c", "data")
	writeCompiledData(t, data)
	user := filepath.Join(root, "home", "user.json")
	os.MkdirAll(filepath.Dir(user), 0o700)
	sock := filepath.Join(root, "0c.sock")
	logs := &lockedBuffer{}
	server := exec.Command(exe, "-data", data, "-user", user, "-socket", sock, "-idle-exit", "3s",
		"-install-watch", "on", "-install-watch-interval", "200ms", "-install-watch-idle", "0s")
	server.Stderr = logs
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer server.Process.Kill()
	waitDial(t, sock)
	stuck := filepath.Join(t.TempDir(), "stuck")
	os.WriteFile(stuck, []byte("#!/bin/sh\nexec sleep 300\n"), 0o755)
	installFile(t, stuck, exe)
	exited := make(chan error, 1)
	go func() { exited <- server.Wait() }()
	select {
	case <-exited:
	case <-time.After(20 * time.Second):
		t.Fatalf("engine loop blocked by the check:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "閒置") {
		t.Fatalf("engine did not exit by idle:\n%s", logs.String())
	}
}
