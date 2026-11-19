//go:build !windows

package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/internal/settings"
)

// #297：settings.json 的 personal_learning（缺欄位時是開）。關閉時照常送出，但 user.json 一字不改、也不重寫；
// 打開後照常學。health 回報引擎載入的值，設定程式據此比較「已儲存，尚未套用」。
func TestPersonalLearningSettingInServer(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	user := filepath.Join(dir, "user.json")
	run := func(file map[string]any, want string) {
		t.Helper()
		b, _ := json.Marshal(file)
		if err := os.WriteFile(filepath.Join(dir, settings.FileName), b, 0600); err != nil {
			t.Fatal(err)
		}
		sock := filepath.Join(dir, "learn.sock")
		server := exec.Command(binary, "-data", data, "-user", user, "-socket", sock, "-legacy-socket", "")
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { server.Process.Signal(os.Interrupt); server.Wait() }() // 結束時存檔
		waitDial(t, sock)
		conn, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		call := func(env ipc.Envelope) ipc.Reply {
			t.Helper()
			if err := ipc.WriteFrame(conn, env); err != nil {
				t.Fatal(err)
			}
			var r ipc.Reply
			if err := ipc.ReadFrame(conn, &r); err != nil {
				t.Fatal(err)
			}
			return r
		}
		if health := call(ipc.Envelope{Op: ipc.OpHealth}); health.Info["personal_learning"] != want {
			t.Fatalf("health personal_learning=%q，要 %q", health.Info["personal_learning"], want)
		}
		open := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version})
		do := func(req proto.Request) *proto.Snapshot {
			return call(ipc.Envelope{Op: ipc.OpHandle, Session: open.Session, Req: &req}).Snapshot
		}
		for range 3 {
			for _, k := range []string{"j", "i", "3"} { // ㄨㄛˇ 我
				do(proto.Request{Type: proto.Key, Key: k})
			}
			if snap := do(proto.Request{Type: proto.Key, Key: proto.KeyEnter}); snap == nil || snap.Commit != "我" {
				t.Fatalf("personal_learning=%s：照常送出：%+v", want, snap)
			}
		}
	}
	run(map[string]any{"selection": "statistical"}, "true")
	before, err := os.ReadFile(user)
	if err != nil || !bytes.Contains(before, []byte(`"我"`)) {
		t.Fatalf("預設照常學：%s %v", before, err)
	}
	stat, _ := os.Stat(user)

	run(map[string]any{"selection": "statistical", "personal_learning": false}, "false")
	after, err := os.ReadFile(user)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("關閉學習時 user.json 被改了：\n前 %s\n後 %s", before, after)
	}
	if now, _ := os.Stat(user); !now.ModTime().Equal(stat.ModTime()) {
		t.Fatal("關閉學習時 user.json 不重寫")
	}

	run(map[string]any{"selection": "statistical", "personal_learning": true}, "true")
	if again, _ := os.ReadFile(user); bytes.Equal(before, again) {
		t.Fatal("打開後照常學")
	}
}
