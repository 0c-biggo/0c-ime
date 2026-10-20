//go:build !windows

package main

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"git.biggo.com/0c/ime/bridge/ipc"
	"git.biggo.com/0c/ime/core/layout"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/zhuyin"
	"git.biggo.com/0c/ime/internal/settings"
)

// #118：settings.json 的 output_simplified 開啟時，經 IPC 送出的文字是簡體，組字區仍是繁體；
// health 回報引擎實際載入的值，給設定程式比較「已儲存，尚未套用」。
func TestOutputSimplifiedSettingInServer(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	for _, on := range []bool{false, true} {
		b, _ := json.Marshal(map[string]any{"selection": "statistical", "output_simplified": on})
		if err := os.WriteFile(filepath.Join(dir, settings.FileName), b, 0600); err != nil {
			t.Fatal(err)
		}
		sock := filepath.Join(dir, "simplified.sock")
		server := exec.Command(binary, "-data", data, "-user", filepath.Join(dir, "user.json"), "-socket", sock, "-legacy-socket", "")
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		func() {
			defer func() { server.Process.Signal(os.Interrupt); server.Wait() }()
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
			want := map[bool]string{false: "false", true: "true"}[on]
			if health := call(ipc.Envelope{Op: ipc.OpHealth}); health.Info["output_simplified"] != want {
				t.Fatalf("health：%+v", health.Info)
			}
			id := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version}).Session
			handle := func(req proto.Request) *proto.Snapshot {
				return call(ipc.Envelope{Op: ipc.OpHandle, Session: id, Req: &req}).Snapshot
			}
			var snap *proto.Snapshot
			for _, y := range []string{"ㄈㄥ", "ㄐㄧㄥˇ"} {
				for _, k := range layout.Standard.Keys(zhuyin.Syllable(y)) {
					snap = handle(proto.Request{Type: proto.Key, Key: string(k)})
				}
			}
			if snap == nil || snap.Text() != "風景" {
				t.Fatalf("組字區應是繁體：%+v", snap)
			}
			commit := map[bool]string{false: "風景", true: "风景"}[on]
			if snap = handle(proto.Request{Type: proto.Key, Key: proto.KeyEnter}); snap == nil || snap.Commit != commit {
				t.Fatalf("output_simplified=%v：送出 %+v，預期 %q", on, snap, commit)
			}
		}()
	}
}

// #169：輸入法選單送 output_simplified 請求即時切換，不重啟引擎（pid 不變）：另一個輸入框裡已經在組字區的字，
// 送出時照新的設定；引擎把新值寫進 settings.json，保留不認得的欄位；health 回報能力與目前的值。
func TestOutputSimplifiedSwitchesLiveInServer(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	path := filepath.Join(dir, settings.FileName)
	if err := os.WriteFile(path, []byte(`{"selection":"statistical","future_key":{"x":1}}`), 0600); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "live.sock")
	server := exec.Command(binary, "-data", data, "-user", filepath.Join(dir, "user.json"), "-socket", sock, "-legacy-socket", "")
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { server.Process.Signal(os.Interrupt); server.Wait() }()
	waitDial(t, sock)
	dial := func() func(ipc.Envelope) ipc.Reply {
		conn, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		return func(env ipc.Envelope) ipc.Reply {
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
	}
	menu, editor := dial(), dial() // 兩個輸入框：一個從選單切換，另一個正在組字
	health := menu(ipc.Envelope{Op: ipc.OpHealth}).Info
	if health["output_simplified_live"] != "true" || health["output_simplified"] != "false" {
		t.Fatalf("health：%+v", health)
	}
	pid := health["pid"]
	a := menu(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version}).Session
	opened := editor(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version})
	if opened.Info["output_simplified_live"] != "true" || opened.Info["output_simplified"] != "false" {
		t.Fatalf("open：%+v", opened.Info)
	}
	b := opened.Session
	typeScenery := func() *proto.Snapshot {
		var snap *proto.Snapshot
		for _, y := range []string{"ㄈㄥ", "ㄐㄧㄥˇ"} {
			for _, k := range layout.Standard.Keys(zhuyin.Syllable(y)) {
				snap = editor(ipc.Envelope{Op: ipc.OpHandle, Session: b, Req: &proto.Request{Type: proto.Key, Key: string(k)}}).Snapshot
			}
		}
		return snap
	}
	enter := func() *proto.Snapshot {
		return editor(ipc.Envelope{Op: ipc.OpHandle, Session: b, Req: &proto.Request{Type: proto.Key, Key: proto.KeyEnter}}).Snapshot
	}
	if snap := typeScenery(); snap.Text() != "風景" || snap.OutputSimplified {
		t.Fatalf("切換前：%+v", snap)
	}
	toggle := func(on bool) *proto.Snapshot {
		r := menu(ipc.Envelope{Op: ipc.OpHandle, Session: a, Req: &proto.Request{Type: proto.OutputSimplified, On: on}})
		if !r.OK || !r.Snapshot.Handled || r.Snapshot.OutputSimplified != on {
			t.Fatalf("切換 %v：%+v", on, r)
		}
		return r.Snapshot
	}
	if s := toggle(true); len(s.Notices) != 1 || s.Notices[0] != "已切換為輸出簡體字" {
		t.Fatalf("提示：%+v", s.Notices)
	}
	saved, err := settings.Read(path)
	if err != nil || !saved.Settings.OutputSimplified {
		t.Fatalf("settings.json 沒有寫入：%+v %v", saved, err)
	}
	raw, _ := os.ReadFile(path)
	var kept map[string]any
	if json.Unmarshal(raw, &kept) != nil || kept["future_key"] == nil || kept["selection"] != "statistical" {
		t.Fatalf("寫檔要保留其他欄位：%s", raw)
	}
	if snap := enter(); snap.Commit != "风景" || !snap.OutputSimplified {
		t.Fatalf("組字中切換後送出 %+v", snap)
	}
	if h := menu(ipc.Envelope{Op: ipc.OpHealth}).Info; h["pid"] != pid || h["output_simplified"] != "true" {
		t.Fatalf("不重啟引擎、health 跟著變：%+v（原 pid %s）", h, pid)
	}
	toggle(false)
	typeScenery()
	if snap := enter(); snap.Commit != "風景" || snap.OutputSimplified {
		t.Fatalf("切回繁體後送出 %+v", snap)
	}
	if saved, _ := settings.Read(path); saved.Settings.OutputSimplified {
		t.Fatal("關閉也要寫進 settings.json")
	}
	if h := menu(ipc.Envelope{Op: ipc.OpHealth}).Info; h["pid"] != pid || h["output_simplified"] != "false" {
		t.Fatalf("%+v", h)
	}
}
