//go:build !windows

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func TestInstallWatchEnabledOnlyForLinuxSystemInstall(t *testing.T) {
	home := t.TempDir()
	prefix := t.TempDir()
	data := filepath.Join(prefix, "share", "0c", "data")
	os.MkdirAll(data, 0o755)
	official := filepath.Join(home, ".local", "share", "zuin", "versions", "0.2.3", "bin", "0c-server")
	system := filepath.Join(prefix, "libexec", "0c", "0c-server")
	for _, c := range []struct {
		name, goos, exe string
		marker, want    bool
	}{
		{"官網安裝在家目錄", "linux", official, false, false},
		{"系統安裝在 /usr", "linux", system, false, true},
		{"家目錄裡但有發行版標記", "linux", official, true, true},
		{"macOS 的 App 由 0c-update 交接", "darwin", system, true, false},
		{"Windows", "windows", system, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			marker := distributionMarker(data)
			os.Remove(marker)
			if c.marker {
				os.WriteFile(marker, []byte(`{"schema":1,"updates":"package-manager"}`), 0o644)
			}
			if got := installWatchEnabled(c.goos, c.exe, data, home); got != c.want {
				t.Fatalf("installWatchEnabled = %v，應為 %v", got, c.want)
			}
		})
	}
	if distributionMarker(data) != filepath.Join(prefix, "share", "0c", "distribution.json") {
		t.Fatalf("marker path %s", distributionMarker(data))
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
	w.reject()
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
	for _, name := range []string{"select", "punct"} {
		p := filepath.Join(userDir, "models", sha, name)
		os.MkdirAll(p, 0o755)
		os.WriteFile(filepath.Join(p, "manifest.json"), []byte(`{}`), 0o644)
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
		info := run(t, filepath.Join(root, "no", "python3"))
		want := map[string]string{"neural_startup_state": "runtime_missing", "selection_state": "runtime_missing",
			"punctuation_state": "runtime_missing", "neural_python": "missing", "selection_source": "", "punctuation": "false"}
		for k, v := range want {
			if info[k] != v {
				t.Errorf("%s = %q，應為 %q（%v）", k, info[k], v, info)
			}
		}
	})
	t.Run("系統 Python 與個人目錄的模型", func(t *testing.T) {
		info := run(t, "/bin/sh") // 存在的執行檔；模型是假的，核對 manifest 時就退回
		want := map[string]string{"neural_python": "system", "neural_startup_state": "startup_fallback", "selection_source": "user"}
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
