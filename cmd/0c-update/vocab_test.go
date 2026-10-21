package main

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/core/engine"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/vocab"
	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/userlock"
)

// engineDial 把每個連線交給同一個行程內的引擎回答詞彙指令，模擬執行中的 0c-server。
func engineDial(e *engine.Engine) autoupdate.Dialer {
	return func(time.Duration) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			var env ipc.Envelope
			if ipc.ReadFrame(server, &env) == nil && env.Op == ipc.OpVocab {
				resp := e.Vocab(*env.Vocab)
				ipc.WriteFrame(server, ipc.Reply{OK: true, Vocab: &resp})
			}
		}()
		return client, nil
	}
}

func TestVocabDoGoesThroughEngineAndReadsOffline(t *testing.T) {
	base := t.TempDir()
	e := engine.New(lexicon.NewMem(), lm.NewMem())
	resp, err := vocabDo(engineDial(e), base, engine.VocabRequest{Op: engine.VocabAdd, Words: []vocab.Entry{{Text: "陳怡君", Readings: []string{"ㄔㄣˊ ㄧˊ ㄐㄩㄣ"}}}})
	if err != nil || !resp.OK || resp.Count != 1 {
		t.Fatalf("%+v %v", resp, err)
	}
	var buf bytes.Buffer
	printVocab(&buf, engine.VocabAdd, resp)
	if !strings.Contains(buf.String(), "自訂詞 1／10000") {
		t.Fatal(buf.String())
	}
	// 引擎沒有執行：在寫入者鎖內直接讀寫 vocab.json（#120 的退路）。
	if r, err := vocabDo(noEngine, base, engine.VocabRequest{Op: engine.VocabList}); err != nil || !r.OK || r.Total != 0 {
		t.Fatalf("沒有詞彙檔時是空的：%+v %v", r, err)
	}
	f, _ := os.Create(filepath.Join(base, "vocab.json"))
	e.VocabSnapshot().Save(f)
	f.Close()
	r, err := vocabDo(noEngine, base, engine.VocabRequest{Op: engine.VocabList})
	if err != nil || r.Total != 1 || r.Words[0].Text != "陳怡君" {
		t.Fatalf("離線讀檔：%+v %v", r, err)
	}
	buf.Reset()
	printVocab(&buf, engine.VocabList, r)
	if !strings.Contains(buf.String(), "陳怡君\tㄔㄣˊ ㄧˊ ㄐㄩㄣ") {
		t.Fatal(buf.String())
	}
	if r, err := vocabDo(noEngine, base, engine.VocabRequest{Op: engine.VocabAdd, Words: []vocab.Entry{{Text: "世傑", Readings: []string{"ㄕˋ ㄐㄧㄝˊ"}}}}); err != nil || !r.OK || r.Count != 2 {
		t.Fatalf("離線加詞：%+v %v", r, err)
	}
	if r, _ := vocabDo(noEngine, base, engine.VocabRequest{Op: engine.VocabList}); r.Total != 2 {
		t.Fatalf("寫回檔案：%+v", r)
	}
	for _, op := range []string{engine.VocabLearned, engine.VocabUnlearn, engine.VocabForgotten, engine.VocabRelearn, engine.VocabLearnedUndo} {
		if _, err := vocabDo(noEngine, base, engine.VocabRequest{Op: op, Texts: []string{"世傑"}}); err == nil || !strings.Contains(err.Error(), "學到的詞") {
			t.Fatalf("%s：學到的詞只能交給引擎：%v", op, err)
		}
	}
	// 有引擎拿著寫入者鎖（還沒開始服務）：不碰檔案。
	lock, err := userlock.TryLock(base)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if _, err := vocabDo(noEngine, base, engine.VocabRequest{Op: engine.VocabRemove, Texts: []string{"世傑"}}); !errors.Is(err, errEngineBusy) {
		t.Fatalf("鎖被別人拿著：%v", err)
	}
}

func TestParseVocabArgs(t *testing.T) {
	asJSON, source, rest, opts, err := parseVocabArgs([]string{"list", "-json", "-query", "ㄔㄣˊ", "-source", "assistant", "-limit", "5"})
	if err != nil || !asJSON || source != vocab.SourceAssistant || len(rest) != 1 || opts["query"] != "ㄔㄣˊ" || opts["limit"] != "5" {
		t.Fatalf("%v %q %v %v %v", asJSON, source, rest, opts, err)
	}
	if _, _, _, _, err := parseVocabArgs([]string{"list", "-source", "evil"}); err == nil {
		t.Fatal("未知來源")
	}
	if _, _, _, _, err := parseVocabArgs([]string{"list", "-query"}); err == nil {
		t.Fatal("缺值")
	}
}

// #146：英文詞不給讀音；離線也能新增、列出（標示英文）與刪除。
func TestVocabEnglishWordsCommand(t *testing.T) {
	got, ok := addArgs([]string{"ls", "陳怡君", "ㄔㄣˊ ㄧˊ ㄐㄩㄣ", "kubectl"})
	if !ok || len(got) != 3 || got[0].Text != "ls" || got[0].Readings != nil || got[1].Readings[0] != "ㄔㄣˊ ㄧˊ ㄐㄩㄣ" || got[2].Text != "kubectl" {
		t.Fatalf("%+v %v", got, ok)
	}
	if _, ok := addArgs([]string{"ls", "陳怡君"}); ok {
		t.Fatal("漢字詞缺讀音")
	}
	base := t.TempDir()
	r, err := vocabDo(noEngine, base, engine.VocabRequest{Op: engine.VocabAdd, Words: got})
	if err != nil || !r.OK || r.Count != 3 {
		t.Fatalf("%+v %v", r, err)
	}
	r, _ = vocabDo(noEngine, base, engine.VocabRequest{Op: engine.VocabList})
	var buf bytes.Buffer
	printVocab(&buf, engine.VocabList, r)
	if !strings.Contains(buf.String(), "ls\t（英文）\n") || !strings.Contains(buf.String(), "kubectl\t（英文）\n") {
		t.Fatal(buf.String())
	}
	if r, err := vocabDo(noEngine, base, engine.VocabRequest{Op: engine.VocabRemove, Texts: []string{"ls"}}); err != nil || !r.OK || r.Count != 2 {
		t.Fatalf("%+v %v", r, err)
	}
}
