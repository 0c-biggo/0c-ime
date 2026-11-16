//go:build !windows

package main

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/core/engine"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/vocab"
	"github.com/0c-biggo/0c-ime/internal/datadir"
	"github.com/0c-biggo/0c-ime/internal/settings"
)

// 清除學習紀錄（#301）：引擎在跑時用 reset_personal 清空並立刻存檔，user.json 馬上變成空白的個人資料；
// 之後結束（SIGINT／SIGTERM 會存檔）也不會把清除前的學習寫回來，正在組字的那段送出也不學。vocab.json 不動。
func TestResetPersonalSavesEmptyStoreAndKeepsVocab(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	user, sock := filepath.Join(dir, "user.json"), filepath.Join(dir, "reset.sock")
	config := settings.Default()
	config.Selection, config.Punctuation = settings.Statistical, false
	if err := settings.Save(filepath.Join(dir, settings.FileName), config); err != nil {
		t.Fatal(err)
	}
	load := func() *personal.Store {
		t.Helper()
		f, err := os.Open(user)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		st, err := personal.LoadStore(f)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	run := func(body func(call func(ipc.Envelope) ipc.Reply, typeKeys func(uint64, string) *proto.Snapshot)) {
		t.Helper()
		server := exec.Command(binary, "-data", data, "-user", user, "-socket", sock)
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
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
			var reply ipc.Reply
			if err := ipc.ReadFrame(conn, &reply); err != nil {
				t.Fatal(err)
			}
			return reply
		}
		typeKeys := func(id uint64, keys string) (snap *proto.Snapshot) {
			t.Helper()
			for _, k := range keys {
				key := string(k)
				if k == '\n' {
					key = proto.KeyEnter
				}
				snap = call(ipc.Envelope{Op: ipc.OpHandle, Session: id, Req: &proto.Request{Type: proto.Key, Key: key}}).Snapshot
			}
			return snap
		}
		body(call, typeKeys)
	}

	// 第一次：打字學到東西、加一個自訂詞，結束時存檔。
	run(func(call func(ipc.Envelope) ipc.Reply, typeKeys func(uint64, string) *proto.Snapshot) {
		id := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version}).Session
		if snap := typeKeys(id, "ji3\n"); snap.Commit == "" {
			t.Fatalf("沒有送出：%+v", snap)
		}
		add := call(ipc.Envelope{Op: ipc.OpVocab, Vocab: &engine.VocabRequest{Op: engine.VocabAdd, Words: []vocab.Entry{{Text: "ls"}}}})
		if !add.OK || !add.Vocab.OK {
			t.Fatalf("%+v", add)
		}
	})
	if st := load(); !learnedAnything(st) {
		b, _ := os.ReadFile(user)
		t.Fatalf("前提：第一次執行要學到東西：%s", b)
	}
	vocabBefore, err := os.ReadFile(datadir.VocabPath(user))
	if err != nil {
		t.Fatal(err)
	}

	// 第二次：組字到一半時清除，立刻存成空白；組字照常送出但不學，結束時也不寫回。
	run(func(call func(ipc.Envelope) ipc.Reply, typeKeys func(uint64, string) *proto.Snapshot) {
		if info := call(ipc.Envelope{Op: ipc.OpHealth}).Info; info["personal_reset"] != "true" || info["personal_lock"] != "held" {
			t.Fatalf("health：%+v", info)
		}
		id := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version}).Session
		typeKeys(id, "ji3")
		if r := call(ipc.Envelope{Op: ipc.OpResetPersonal}); !r.OK {
			t.Fatalf("%+v", r)
		}
		var saved, empty bytes.Buffer
		personal.NewStore().Save(&empty)
		b, _ := os.ReadFile(user)
		saved.Write(b)
		if saved.String() != empty.String() {
			t.Fatalf("清除後 user.json 要立刻是空白的個人資料：%s", saved.String())
		}
		if snap := typeKeys(id, "\n"); snap.Commit == "" {
			t.Fatalf("組字照常送出：%+v", snap)
		}
	})
	if st := load(); learnedAnything(st) {
		b, _ := os.ReadFile(user)
		t.Fatalf("結束時把清除前的學習或清除前開始的組字寫回來了：%s", b)
	}
	if b, _ := os.ReadFile(datadir.VocabPath(user)); !bytes.Equal(b, vocabBefore) {
		t.Fatal("自訂詞彙不能被清掉")
	}

	// 第三次：清除後照常學習。
	run(func(call func(ipc.Envelope) ipc.Reply, typeKeys func(uint64, string) *proto.Snapshot) {
		id := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version}).Session
		typeKeys(id, "ji3\n")
	})
	if st := load(); !learnedAnything(st) {
		t.Fatal("清除後要照常學習")
	}
}

func learnedAnything(st *personal.Store) bool {
	return st.Total != 0 || len(st.Uni)+len(st.Bi)+len(st.EvidenceByWord)+len(st.EvidenceByContext)+len(st.Corrections) != 0
}
