//go:build !windows

package main

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/core/engine"
	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/vocab"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"github.com/0c-biggo/0c-ime/internal/settings"
)

// 自訂詞彙（#122）經 IPC 修改、立即存成 vocab.json、重啟後照樣打得出來；
// 舊設定檔寫著 preview_0_2=false 也照樣開放（0.2.0 起不再看這個鍵）；較新版本寫成的詞彙檔不讀入、不改寫。
func TestVocabOverIPCPersistsAndRespectsGate(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	user := filepath.Join(dir, "user.json")
	vocabPath := filepath.Join(dir, "vocab.json")
	sock := filepath.Join(dir, "vocab.sock")
	const name, reading = "王小明", "ㄨㄤˊ ㄒㄧㄠˇ ㄇㄧㄥˊ"

	run := func(legacyOff bool, fn func(call func(ipc.Envelope) ipc.Reply)) string {
		t.Helper()
		body := `{"schema":1,"selection":"statistical"}`
		if legacyOff { // 0.2.0 開發期間的設定檔：內部開關明確關閉
			body = `{"schema":1,"selection":"statistical","preview_0_2":false}`
		}
		if err := os.WriteFile(filepath.Join(dir, settings.FileName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		server := exec.Command(binary, "-data", data, "-user", user, "-socket", sock)
		var logs bytes.Buffer
		server.Stderr = &logs
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		// 讀 logs 之前要先等行程結束：exec 在另一個 goroutine 把 stderr 抄進 logs（-race 會抓到）。
		stopped := false
		stop := func() {
			if !stopped {
				stopped = true
				server.Process.Signal(os.Interrupt)
				server.Wait()
			}
		}
		defer stop()
		waitDial(t, sock)
		conn, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		fn(func(env ipc.Envelope) ipc.Reply {
			t.Helper()
			if err := ipc.WriteFrame(conn, env); err != nil {
				t.Fatal(err)
			}
			var r ipc.Reply
			if err := ipc.ReadFrame(conn, &r); err != nil {
				t.Fatal(err)
			}
			return r
		})
		conn.Close()
		stop()
		return logs.String()
	}
	add := &engine.VocabRequest{Op: engine.VocabAdd, Words: []vocab.Entry{{Text: name, Readings: []string{reading}}}}
	composed := func(call func(ipc.Envelope) ipc.Reply) string {
		open := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version})
		var snap *proto.Snapshot
		for _, syl := range zhuyin.ParseReading(reading) {
			for _, k := range layout.Standard.Keys(syl) {
				snap = call(ipc.Envelope{Op: ipc.OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Key, Key: string(k)}}).Snapshot
			}
		}
		return snap.Text()
	}

	run(true, func(call func(ipc.Envelope) ipc.Reply) {
		h := call(ipc.Envelope{Op: ipc.OpHealth})
		if h.Info["preview_0_2"] != "true" || h.Info["vocab_keys"] != "true" {
			t.Fatalf("舊檔的 preview_0_2=false 不再關掉功能：%v", h.Info)
		}
	})
	if _, err := os.Stat(vocabPath); !os.IsNotExist(err) {
		t.Fatal("沒有加詞時不建立 vocab.json")
	}

	run(false, func(call func(ipc.Envelope) ipc.Reply) {
		r := call(ipc.Envelope{Op: ipc.OpVocab, Vocab: add})
		if !r.OK || r.Vocab == nil || !r.Vocab.OK || r.Vocab.Count != 1 {
			t.Fatalf("%+v", r)
		}
		if _, err := os.Stat(vocabPath); err != nil {
			t.Fatalf("修改後立即存檔：%v", err)
		}
		if got := composed(call); got != name {
			t.Fatalf("加入後立即打得出來：%q", got)
		}
		h := call(ipc.Envelope{Op: ipc.OpHealth})
		if h.Info["vocab_state"] != "loaded" || h.Info["preview_0_2"] != "true" {
			t.Fatalf("health：%v", h.Info)
		}
	})
	run(false, func(call func(ipc.Envelope) ipc.Reply) {
		if got := composed(call); got != name {
			t.Fatalf("重啟後照樣打得出來：%q", got)
		}
		// #146：英文詞經 IPC 新增，立即存檔。
		en := call(ipc.Envelope{Op: ipc.OpVocab, Vocab: &engine.VocabRequest{Op: engine.VocabAdd, Words: []vocab.Entry{{Text: "ls"}}}})
		if !en.OK || en.Vocab == nil || !en.Vocab.OK || en.Vocab.Count != 2 {
			t.Fatalf("%+v", en)
		}
	})
	run(false, func(call func(ipc.Envelope) ipc.Reply) {
		l := call(ipc.Envelope{Op: ipc.OpVocab, Vocab: &engine.VocabRequest{Op: engine.VocabList, Query: "ls"}})
		if !l.OK || l.Vocab == nil || l.Vocab.Total != 1 || !l.Vocab.Words[0].English() {
			t.Fatalf("重啟後英文詞還在：%+v", l)
		}
		if r := call(ipc.Envelope{Op: ipc.OpVocab, Vocab: &engine.VocabRequest{Op: engine.VocabRemove, Texts: []string{"ls"}}}); !r.OK || r.Vocab.Count != 1 {
			t.Fatalf("%+v", r)
		}
	})
	// 舊檔寫著 preview_0_2=false：自訂詞照樣載入。
	run(true, func(call func(ipc.Envelope) ipc.Reply) {
		if got := composed(call); got != name {
			t.Fatalf("舊檔的 preview_0_2=false 不再停用自訂詞：%q", got)
		}
	})

	newer := []byte(`{"format":99,"words":[{"word":"王小明","readings":["ㄨㄤˊ ㄒㄧㄠˇ ㄇㄧㄥˊ"]}],"later":1}` + "\n")
	if err := os.WriteFile(vocabPath, newer, 0o600); err != nil {
		t.Fatal(err)
	}
	logs := run(false, func(call func(ipc.Envelope) ipc.Reply) {
		if r := call(ipc.Envelope{Op: ipc.OpVocab, Vocab: add}); r.OK || !strings.Contains(r.Error, "較新版本") {
			t.Fatalf("較新格式時拒絕修改：%+v", r)
		}
	})
	if got, _ := os.ReadFile(vocabPath); !bytes.Equal(got, newer) {
		t.Fatalf("較新格式的詞彙檔被改寫：%s", got)
	}
	if !strings.Contains(logs, "較新版本") {
		t.Errorf("沒有記錄原因：%s", logs)
	}
}
