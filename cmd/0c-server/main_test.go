//go:build !windows

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func buildServer(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "0c-server")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return binary
}

func waitDial(t *testing.T, sock string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if c, err := net.Dial("unix", sock); err == nil {
			c.Close()
			return
		}
	}
	t.Fatalf("連不上 %s", sock)
}

// 升級過渡期：0c-server 同時服務新舊 socket；舊名稱已有 server（舊版 zuin-server）時不另起第二個寫入者。
func TestServesLegacySocketAndDefersToRunningLegacyServer(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	sock, legacy := filepath.Join(dir, "0c.sock"), filepath.Join(dir, "zuin.sock")
	args := []string{"-data", data, "-user", filepath.Join(dir, "user.json"), "-socket", sock, "-legacy-socket", legacy}

	server := exec.Command(binary, args...)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	waitDial(t, sock)
	waitDial(t, legacy)
	second := exec.Command(binary, args...)
	if out, err := second.CombinedOutput(); err != nil {
		t.Fatalf("第二個實例應直接結束：%v\n%s", err, out)
	}
	server.Process.Signal(os.Interrupt)
	server.Wait()
	for _, name := range []string{sock, legacy} {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Errorf("結束後仍留下 %s", name)
		}
	}

	// 只有舊名稱被佔用時也不能啟動，否則兩個 server 會同時寫個人詞庫。
	old, err := net.Listen("unix", legacy)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if out, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatal("舊版執行中仍佔用新 socket")
	}
}

// 隔離測試只指定 -socket 時，不得碰使用者真正的舊 socket。
func TestExplicitSocketDisablesLegacyDefault(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	runtime := filepath.Join(dir, "runtime")
	os.MkdirAll(filepath.Join(runtime, "zuin"), 0o700)
	legacy, err := net.Listen("unix", filepath.Join(runtime, "zuin", "zuin.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	data, _ := filepath.Abs("../../testdata/toy")
	sock := filepath.Join(dir, "test.sock")
	server := exec.Command(binary, "-data", data, "-user", filepath.Join(dir, "user.json"), "-socket", sock)
	server.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtime)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { server.Process.Signal(os.Interrupt); server.Wait() }()
	waitDial(t, sock)
}

// 回復到舊版後讀到較新格式的個人詞庫：照常輸入，但不得寫回覆蓋。
func TestNewerUserStoreIsNeverOverwritten(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	user := filepath.Join(dir, "user.json")
	original := []byte(`{"Format":99,"Uni":{"麻婆":5},"Later":{"x":1}}` + "\n")
	os.WriteFile(user, original, 0o600)
	sock := filepath.Join(dir, "0c.sock")
	server := exec.Command(binary, "-data", data, "-user", user, "-socket", sock)
	var logs bytes.Buffer
	server.Stderr = &logs
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	waitDial(t, sock)
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	call := func(env ipc.Envelope) ipc.Reply {
		t.Helper()
		if err := ipc.WriteFrame(c, env); err != nil {
			t.Fatal(err)
		}
		var r ipc.Reply
		if err := ipc.ReadFrame(c, &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	open := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version})
	for _, k := range "a86qi6" {
		call(ipc.Envelope{Op: ipc.OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Key, Key: string(k)}})
	}
	r := call(ipc.Envelope{Op: ipc.OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Key, Key: proto.KeyEnter}})
	if r.Snapshot == nil || r.Snapshot.Commit != "麻婆" {
		t.Fatalf("輸入應照常運作：%+v", r)
	}
	c.Close()
	time.Sleep(saveIdle + 2*saveTick) // 跨過一次停止打字後的存檔時機；結束時的存檔也不得寫回
	server.Process.Signal(os.Interrupt)
	server.Wait()
	if got, _ := os.ReadFile(user); !bytes.Equal(got, original) {
		t.Fatalf("較新格式的個人詞庫被改寫：%s\n%s", got, logs.String())
	}
	if !bytes.Contains(logs.Bytes(), []byte("較新版本")) {
		t.Errorf("未記錄格式較新的原因：%s", logs.String())
	}
}

// 選字模式依 settings.json 與內附模型決定；任何情況下輸入都照常運作，回報實際狀態。
func TestSelectionSettingAndBundledDiscovery(t *testing.T) {
	binary := buildServer(t)
	toy, _ := filepath.Abs("../../testdata/toy")
	info := func(t *testing.T, root, settingsJSON string) map[string]string {
		t.Helper()
		user := filepath.Join(root, "user", "user.json")
		os.MkdirAll(filepath.Dir(user), 0o700)
		if settingsJSON != "" {
			os.WriteFile(filepath.Join(filepath.Dir(user), "settings.json"), []byte(settingsJSON), 0o600)
		}
		sock := filepath.Join(root, "0c.sock")
		server := exec.Command(binary, "-data", filepath.Join(root, "data"), "-user", user, "-socket", sock)
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { server.Process.Signal(os.Interrupt); server.Wait() }()
		waitDial(t, sock)
		c, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if err := ipc.WriteFrame(c, ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version}); err != nil {
			t.Fatal(err)
		}
		var r ipc.Reply
		if err := ipc.ReadFrame(c, &r); err != nil || !r.OK {
			t.Fatalf("%+v %v", r, err)
		}
		return r.Info
	}
	layout := func(t *testing.T, withBundle bool) string {
		root := t.TempDir()
		os.Symlink(toy, filepath.Join(root, "data"))
		if withBundle {
			n := filepath.Join(root, "neural")
			os.MkdirAll(filepath.Join(n, "python", "bin"), 0o755)
			os.MkdirAll(filepath.Join(n, "select"), 0o755)
			os.WriteFile(filepath.Join(n, "python", "bin", "python3"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
			os.WriteFile(filepath.Join(n, "sequence_runtime.py"), nil, 0o644)
			os.WriteFile(filepath.Join(n, "select", "manifest.json"), []byte(`{}`), 0o644)
		}
		return root
	}
	for _, c := range []struct {
		name, settings string
		bundle         bool
		want           map[string]string
	}{
		{"預設但未附模型", "", false, map[string]string{"neural_startup_state": "not_installed", "selection_setting": "neural", "document_context": "true"}},
		{"使用者選統計", `{"selection":"statistical","document_context":false}`, true, map[string]string{"neural_startup_state": "disabled_by_setting", "selection_setting": "statistical", "document_context": "false"}},
		{"模型無法啟動", "", true, map[string]string{"neural_startup_state": "startup_fallback", "selection_setting": "neural"}},
		{"設定檔損毀回預設", `{"selection":"magic"}`, false, map[string]string{"neural_startup_state": "not_installed", "selection_setting": "neural"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := info(t, layout(t, c.bundle), c.settings)
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("%s = %q，應為 %q（%v）", k, got[k], v, got)
				}
			}
		})
	}
}

// manifest 核對成功後 worker 才在背景失敗；啟動配置 enabled 不等於實際模型可用。
func TestHealthReportsBackgroundWorkerFailureWhileInputContinues(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	if err := os.Mkdir(data, 0o700); err != nil {
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
	dataFiles := map[string]any{}
	for name, body := range map[string][]byte{"lexicon.bin": lex.Encode(), "lm.bin": sys.Encode()} {
		if err := os.WriteFile(filepath.Join(data, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
		dataFiles[name] = map[string]string{"sha256": fmt.Sprintf("%x", sha256.Sum256(body))}
	}
	manifest, _ := json.Marshal(map[string]any{"format": 1, "files": dataFiles})
	if err := os.WriteFile(filepath.Join(data, "manifest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	packages := map[string]string{"select": "zuin-sequence-runtime-v1", "punct": "zuin-punct-runtime-v1"}
	for name, schema := range packages {
		path := filepath.Join(dir, name)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		files := map[string]string{}
		for _, file := range []string{"model.onnx", "vocab.json"} {
			body := []byte(file)
			if err := os.WriteFile(filepath.Join(path, file), body, 0o600); err != nil {
				t.Fatal(err)
			}
			files[file] = fmt.Sprintf("%x", sha256.Sum256(body))
		}
		body, _ := json.Marshal(map[string]any{"schema": schema, "files": files, "weight": .1,
			"context_policy": "confirmed_left_64", "dictionary_sha256": fmt.Sprintf("%x", sha256.Sum256(manifest))})
		if err := os.WriteFile(filepath.Join(path, "manifest.json"), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sock := filepath.Join(dir, "e.sock")
	server := exec.Command(binary, "-data", data, "-user", filepath.Join(dir, "user.json"), "-socket", sock,
		"-neural-package", filepath.Join(dir, "select"), "-punct-package", filepath.Join(dir, "punct"), "-neural-python", "/usr/bin/false")
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { server.Process.Signal(os.Interrupt); server.Wait() }()
	waitDial(t, sock)
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	call := func(env ipc.Envelope) ipc.Reply {
		t.Helper()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		if err := ipc.WriteFrame(c, env); err != nil {
			t.Fatal(err)
		}
		var r ipc.Reply
		if err := ipc.ReadFrame(c, &r); err != nil || !r.OK {
			t.Fatalf("IPC: %+v %v", r, err)
		}
		return r
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		info := call(ipc.Envelope{Op: ipc.OpHealth}).Info
		if info["selection_state"] == "fallback" && info["punctuation_state"] == "fallback" {
			if info["selection_setting"] != "neural" || info["neural_startup_state"] != "enabled" {
				t.Fatalf("runtime availability must not overwrite preference or startup configuration: %v", info)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("failed workers never reported fallback: %v", info)
		}
	}
	open := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version})
	if open.Info["selection_state"] != "fallback" || open.Info["punctuation_state"] != "fallback" {
		t.Fatalf("open also needs current availability: %v", open.Info)
	}
	for _, k := range "a86qi6" {
		call(ipc.Envelope{Op: ipc.OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Key, Key: string(k)}})
	}
	commit := call(ipc.Envelope{Op: ipc.OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Key, Key: proto.KeyEnter}})
	if commit.Snapshot == nil || commit.Snapshot.Commit != "麻婆" {
		t.Fatalf("fallback input failed: %+v", commit)
	}
}

// 交接：提交後送 drain，引擎在沒有組字時存檔並自行結束，個人詞庫含交接前的學習。
func TestDrainSavesLearningAndExits(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	user := filepath.Join(dir, "user.json")
	sock := filepath.Join(dir, "0c.sock")
	server := exec.Command(binary, "-data", data, "-user", user, "-socket", sock)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	waitDial(t, sock)
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	call := func(env ipc.Envelope) ipc.Reply {
		t.Helper()
		if err := ipc.WriteFrame(c, env); err != nil {
			t.Fatal(err)
		}
		var r ipc.Reply
		if err := ipc.ReadFrame(c, &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	open := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version})
	if open.Info["version"] != "dev" || open.Info["protocol"] != "0" {
		t.Fatalf("version info: %v", open.Info)
	}
	for _, k := range "a86qi6" {
		call(ipc.Envelope{Op: ipc.OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Key, Key: string(k)}})
	}
	call(ipc.Envelope{Op: ipc.OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Key, Key: proto.KeyEnter}})
	// 明確刪除學習一定寫入個人詞庫（tombstone），用來確認交接前的變更有存檔。
	call(ipc.Envelope{Op: ipc.OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Forget, Word: "麻婆豆腐"}})
	call(ipc.Envelope{Op: ipc.OpDrain})
	done := make(chan error, 1)
	go func() { done <- server.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server exit: %v", err)
		}
	case <-time.After(10 * time.Second):
		server.Process.Kill()
		t.Fatal("server did not exit after drain")
	}
	b, err := os.ReadFile(user)
	if err != nil || !bytes.Contains(b, []byte(`"麻婆豆腐":true`)) {
		t.Fatalf("learning not saved before handover: %v %s", err, b)
	}
}

// 存檔時機（#129）：提交後不立刻整份重寫，等使用者停下來；明確刪除學習則馬上寫入。
func TestSaveWaitsForPauseButForgetIsPrompt(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	user := filepath.Join(dir, "user.json")
	sock := filepath.Join(dir, "0c.sock")
	server := exec.Command(binary, "-data", data, "-user", user, "-socket", sock)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { server.Process.Signal(os.Interrupt); server.Wait() }()
	waitDial(t, sock)
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	call := func(env ipc.Envelope) ipc.Reply {
		t.Helper()
		if err := ipc.WriteFrame(c, env); err != nil {
			t.Fatal(err)
		}
		var r ipc.Reply
		if err := ipc.ReadFrame(c, &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	open := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version})
	for _, k := range "a86qi6" {
		call(ipc.Envelope{Op: ipc.OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Key, Key: string(k)}})
	}
	if r := call(ipc.Envelope{Op: ipc.OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Key, Key: proto.KeyEnter}}); r.Snapshot == nil || r.Snapshot.Commit != "麻婆" {
		t.Fatalf("提交：%+v", r)
	}
	time.Sleep(3 * time.Second)
	if _, err := os.Stat(user); err == nil {
		t.Fatal("提交後 3 秒就整份重寫；應等停止打字")
	}
	call(ipc.Envelope{Op: ipc.OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Forget, Word: "麻婆豆腐"}})
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if b, err := os.ReadFile(user); err == nil && bytes.Contains(b, []byte(`"麻婆豆腐":true`)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("刪除學習後 3 秒內沒有寫入個人詞庫")
		}
	}
}

// engine.json 指向另一個版本目錄時，預設啟動改由該版引擎與配對字典執行；明確 -data 不轉交。
func TestEnginePointerRedirectsDefaultLaunch(t *testing.T) {
	binary := buildServer(t)
	home := t.TempDir()
	base := filepath.Join(home, ".local/share/zuin")
	ver := filepath.Join(base, "versions", "0.1.0-dev.99-test")
	os.MkdirAll(filepath.Join(ver, "data"), 0o700)
	// 目標「引擎」是一支腳本，記錄它收到的參數後結束，證明轉交發生且帶入配對字典。
	marker := filepath.Join(home, "redirected")
	os.WriteFile(filepath.Join(ver, "0c-server"), []byte("#!/bin/sh\necho \"$@\" > "+marker+"\n"), 0o755)
	os.WriteFile(filepath.Join(ver, "data", "manifest.json"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(base, "engine.json"), []byte(`{"schema":1,"directory":"versions/0.1.0-dev.99-test","version":"0.1.0-dev.99"}`), 0o600)
	cmd := exec.Command(binary, "-idle-exit", "1s")
	cmd.Env = append(os.Environ(), "HOME="+home, "XDG_RUNTIME_DIR="+t.TempDir(), "ZC_ENGINE_REDIRECTED=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	got, err := os.ReadFile(marker)
	if err != nil || !strings.Contains(string(got), "-data "+filepath.Join(ver, "data")) || !strings.Contains(string(got), "-idle-exit 1s") {
		t.Fatalf("not redirected with paired data: %q %v", got, err)
	}
	os.Remove(marker)
	toy, _ := filepath.Abs("../../testdata/toy")
	explicit := exec.Command(binary, "-check-data", "-data", toy)
	explicit.Env = append(os.Environ(), "HOME="+home, "ZC_ENGINE_REDIRECTED=")
	if out, err := explicit.CombinedOutput(); err != nil || !strings.Contains(string(out), "可載入") {
		t.Fatalf("explicit -data must not redirect: %v %s", err, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("explicit -data was redirected")
	}
}
