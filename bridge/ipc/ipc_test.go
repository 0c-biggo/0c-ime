package ipc

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/core/engine"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/proto"
)

// Unix socket paths have a small platform limit (including on Windows).
// A short prefix avoids t.TempDir embedding the full, descriptive test name.
func socketTestDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "0c-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestDrainKeepsEndpointUntilSaveAndResumesOnFailure(t *testing.T) {
	sock := filepath.Join(socketTestDir(t), "handoff.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	saving, finishSave := make(chan struct{}), make(chan error, 1)
	srv := &Server{Engine: toyEngine(t), BeforeDrain: func() error { close(saving); return <-finishSave },
		OnDrained: func() { t.Error("failed save must not release endpoint") }}
	go srv.Serve(listener)
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := WriteFrame(conn, Envelope{Op: OpDrain}); err != nil {
		t.Fatal(err)
	}
	var reply Reply
	if err := ReadFrame(conn, &reply); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
	select {
	case <-saving:
	case <-time.After(3 * time.Second):
		t.Fatal("save did not start")
	}
	probe, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("endpoint released before save finished: %v", err)
	}
	probe.Close()
	finishSave <- errors.New("injected disk-full")
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		probe, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatalf("endpoint lost after failed save: %v", err)
		}
		probe.SetDeadline(time.Now().Add(time.Second))
		WriteFrame(probe, Envelope{Op: OpHealth})
		var reply Reply
		err = ReadFrame(probe, &reply)
		probe.Close()
		if err == nil && reply.OK && reply.Info["draining"] == "false" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server did not resume after failed save")
}

func toyEngine(t *testing.T) *engine.Engine {
	lf, _ := os.Open("../../testdata/toy/lexicon.tsv")
	defer lf.Close()
	lex, err := lexicon.Load(lf)
	if err != nil {
		t.Fatal(err)
	}
	mf, _ := os.Open("../../testdata/toy/lm.tsv")
	defer mf.Close()
	sys, err := lm.Load(mf)
	if err != nil {
		t.Fatal(err)
	}
	return engine.New(lex, sys)
}

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	in := Envelope{Op: OpHandle, Session: 7, Req: &proto.Request{Type: proto.Key, Key: "j"}}
	if err := WriteFrame(&buf, in); err != nil {
		t.Fatal(err)
	}
	var out Envelope
	if err := ReadFrame(&buf, &out); err != nil || out.Session != 7 || out.Req.Key != "j" {
		t.Fatalf("%+v %v", out, err)
	}
	buf.Reset()
	buf.Write([]byte{0xff, 0xff, 0xff, 0x7f})
	if err := ReadFrame(&buf, &out); err == nil {
		t.Error("超過上限的訊框應被拒絕")
	}
}

func TestForgetSchedulesPersistenceWithoutCommit(t *testing.T) {
	saves := 0
	srv := &Server{Engine: toyEngine(t), OnCommit: func() { saves++ }}
	sessions := map[uint64]bool{}
	id := srv.do(Envelope{Op: OpOpen, Version: proto.Version}, sessions, new(bool)).Session
	r := srv.do(Envelope{Op: OpHandle, Session: id, Req: &proto.Request{Type: proto.Forget, Word: "麻婆豆腐"}}, sessions, new(bool))
	if !r.OK || saves != 1 || !srv.Engine.Model.Store.Tombstones["麻婆豆腐"] {
		t.Fatalf("刪除後即使沒有再提交文字，也必須排程保存 tombstone：%+v saves=%d", r, saves)
	}
}

// 設定程式的移除、不再學、恢復學習與復原改的是學習資料（#182）：照忘記盡快存 user.json，不存 vocab.json。
func TestLearnedOpsSaveLearningData(t *testing.T) {
	commits, forgets, vocabs := 0, 0, 0
	srv := &Server{Engine: toyEngine(t), OnCommit: func() { commits++ }, OnForget: func() { forgets++ }, OnVocab: func() { vocabs++ }}
	srv.Engine.Model.Store.Readings["麻婆豆腐\tㄇㄚˊ ㄆㄛˊ ㄉㄡˋ ㄈㄨˇ"] = true
	sessions := map[uint64]bool{}
	r := srv.do(Envelope{Op: OpVocab, Vocab: &engine.VocabRequest{Op: engine.VocabUnlearn, Texts: []string{"麻婆豆腐"}}}, sessions, new(bool))
	if !r.OK || r.Vocab.Undo == 0 || commits != 1 || forgets != 1 || vocabs != 0 {
		t.Fatalf("%+v commits=%d forgets=%d vocabs=%d", r.Vocab, commits, forgets, vocabs)
	}
	r = srv.do(Envelope{Op: OpVocab, Vocab: &engine.VocabRequest{Op: engine.VocabLearnedUndo, Undo: r.Vocab.Undo}}, sessions, new(bool))
	if !r.OK || !r.Vocab.OK || commits != 2 || forgets != 2 || vocabs != 0 {
		t.Fatalf("%+v commits=%d forgets=%d vocabs=%d", r.Vocab, commits, forgets, vocabs)
	}
}

// 透過真的 unix socket 打完一段字：開 session、按鍵、提交；連線關閉後 session 一併關閉。
func TestServerOverUnixSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "zuin.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	commits := 0
	srv := &Server{Engine: toyEngine(t), OnCommit: func() { commits++ }}
	go srv.Serve(l)
	defer l.Close()

	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	call := func(env Envelope) Reply {
		t.Helper()
		if err := WriteFrame(c, env); err != nil {
			t.Fatal(err)
		}
		var r Reply
		if err := ReadFrame(c, &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := call(Envelope{Op: OpOpen, Version: proto.Version + 1}); r.OK {
		t.Error("協定版本不符應拒絕")
	}
	open := call(Envelope{Op: OpOpen, Version: proto.Version})
	if !open.OK || open.Session == 0 {
		t.Fatalf("%+v", open)
	}
	var snap *proto.Snapshot
	for _, k := range "a86qi6" { // ㄇㄚˊ ㄆㄛˊ
		r := call(Envelope{Op: OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Key, Key: string(k)}})
		if !r.OK {
			t.Fatal(r.Error)
		}
		snap = r.Snapshot
	}
	if snap.Text() != "麻婆" || snap.Suggestion != "豆腐" {
		t.Errorf("%q %q", snap.Text(), snap.Suggestion)
	}
	r := call(Envelope{Op: OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Key, Key: proto.KeyEnter}})
	if r.Snapshot.Commit != "麻婆" || commits != 1 {
		t.Errorf("提交 %q，OnCommit %d 次", r.Snapshot.Commit, commits)
	}
	if r := call(Envelope{Op: OpHandle, Session: open.Session + 100, Req: &proto.Request{Type: proto.Cancel}}); r.OK {
		t.Error("不屬於這個連線的 session 應拒絕")
	}
	c.Close()
	for deadline := time.Now().Add(2 * time.Second); srv.Conns() > 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if srv.Engine.Session(open.Session) != nil {
		t.Error("連線關閉後 session 應一併關閉")
	}
}

// 過渡期 server 同時服務新舊名稱：兩個監聽器共用引擎與連線計數，關掉其中一個不影響另一個的連線。
func TestServerSharesEngineAcrossListeners(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{Engine: toyEngine(t)}
	var conns []net.Conn
	for _, name := range []string{"0c.sock", "zuin.sock"} {
		l, err := net.Listen("unix", filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		go srv.Serve(l)
		c, err := net.Dial("unix", filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		conns = append(conns, c)
	}
	for _, c := range conns {
		if err := WriteFrame(c, Envelope{Op: OpOpen, Version: proto.Version}); err != nil {
			t.Fatal(err)
		}
		var r Reply
		if err := ReadFrame(c, &r); err != nil || !r.OK {
			t.Fatalf("%+v %v", r, err)
		}
	}
	if n := srv.Conns(); n != 2 {
		t.Fatalf("連線數 %d，應為 2", n)
	}
}

// 更新交接：有組字時不退出；組字結束後斷開連線並通知宿主；交接中的請求被拒，外殼會重連新版。
func TestDrainWaitsForCompositionThenDisconnects(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "0c.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	drained := make(chan struct{})
	srv := &Server{Engine: toyEngine(t), DrainTimeout: 5 * time.Second, Info: map[string]string{"version": "test"},
		OnDrained: func() { close(drained) }}
	go srv.Serve(l)
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	call := func(env Envelope) Reply {
		t.Helper()
		if err := WriteFrame(c, env); err != nil {
			t.Fatal(err)
		}
		var r Reply
		if err := ReadFrame(c, &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	open := call(Envelope{Op: OpOpen, Version: proto.Version})
	call(Envelope{Op: OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Key, Key: "a"}})
	h := call(Envelope{Op: OpHealth})
	if h.Info["composing"] != "1" || h.Info["version"] != "test" || h.Info["pid"] == "" {
		t.Fatalf("health: %v", h.Info)
	}
	if r := call(Envelope{Op: OpDrain}); !r.OK || r.Info["draining"] != "true" {
		t.Fatalf("drain: %+v", r)
	}
	select {
	case <-drained:
		t.Fatal("must not hand over while composing")
	case <-time.After(300 * time.Millisecond):
	}
	call(Envelope{Op: OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Cancel}})
	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		t.Fatal("drain did not complete after composition ended")
	}
	if err := WriteFrame(c, Envelope{Op: OpPing}); err == nil {
		var r Reply
		if err := ReadFrame(c, &r); err == nil && r.OK {
			t.Fatal("connection must be closed after handover")
		}
	}
}

func TestDrainTimeoutKeepsServing(t *testing.T) {
	srv := &Server{Engine: toyEngine(t), DrainTimeout: 200 * time.Millisecond, OnDrained: func() { t.Error("must not drain") }}
	sessions := map[uint64]bool{}
	open := srv.do(Envelope{Op: OpOpen, Version: proto.Version}, sessions, new(bool))
	srv.do(Envelope{Op: OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Key, Key: "a"}}, sessions, new(bool))
	if start := false; srv.do(Envelope{Op: OpDrain}, sessions, &start).OK && start {
		srv.runDrain()
	}
	time.Sleep(500 * time.Millisecond)
	if h := srv.do(Envelope{Op: OpHealth}, sessions, new(bool)); h.Info["draining"] != "false" {
		t.Fatalf("after timeout the server must resume: %v", h.Info)
	}
	if r := srv.do(Envelope{Op: OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Key, Key: "8"}}, sessions, new(bool)); !r.OK {
		t.Fatalf("still serving: %+v", r)
	}
}

// 宿主自己開始的交接（#181 系統安裝的升級）：與 drain 請求相同，組字中逾時就放棄、Draining 回到 false。
func TestHostDrainReportsProgressAndTimeout(t *testing.T) {
	srv := &Server{Engine: toyEngine(t), DrainTimeout: 200 * time.Millisecond, OnDrained: func() { t.Error("must not drain") }}
	sessions := map[uint64]bool{}
	open := srv.do(Envelope{Op: OpOpen, Version: proto.Version}, sessions, new(bool))
	srv.do(Envelope{Op: OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Key, Key: "a"}}, sessions, new(bool))
	if srv.Draining() || !srv.Drain() || !srv.Draining() {
		t.Fatal("host drain did not start")
	}
	if srv.Drain() {
		t.Fatal("second drain started while one is running")
	}
	time.Sleep(500 * time.Millisecond)
	if srv.Draining() {
		t.Fatal("drain timed out but still reported")
	}
}

func TestOpenAndHealthRefreshRuntimeInfo(t *testing.T) {
	state := "loading"
	srv := &Server{Engine: toyEngine(t), Info: map[string]string{"version": "test", "neural_startup_state": "enabled"},
		RuntimeInfo: func() map[string]string { return map[string]string{"selection_state": state} }}
	sessions := map[uint64]bool{}
	open := srv.do(Envelope{Op: OpOpen, Version: proto.Version}, sessions, new(bool))
	if open.Info["selection_state"] != "loading" || open.Info["version"] != "test" {
		t.Fatalf("open runtime info: %v", open.Info)
	}
	state = "fallback"
	health := srv.do(Envelope{Op: OpHealth}, sessions, new(bool))
	if health.Info["selection_state"] != "fallback" || health.Info["neural_startup_state"] != "enabled" {
		t.Fatalf("health must refresh availability and preserve startup info: %v", health.Info)
	}
	if open.Info["selection_state"] != "loading" || srv.Info["selection_state"] != "" {
		t.Fatal("runtime query mutated a previous reply or static info")
	}
}

// A settings change is not an update timeout: it stays armed while any field
// is composing, and applies only after the last field has finished.
func TestSettingsApplyWaitsForAllSessionsBeyondDrainTimeout(t *testing.T) {
	drained := make(chan struct{})
	srv := &Server{Engine: toyEngine(t), DrainTimeout: 20 * time.Millisecond, OnDrained: func() { close(drained) }}
	sessions := map[uint64]bool{}
	call := func(env Envelope) Reply { return srv.do(env, sessions, new(bool)) }
	var ids []uint64
	for range 2 {
		id := call(Envelope{Op: OpOpen, Version: proto.Version}).Session
		ids = append(ids, id)
		call(Envelope{Op: OpHandle, Session: id, Req: &proto.Request{Type: proto.Key, Key: "a"}})
	}
	start := false
	if r := srv.do(Envelope{Op: OpApplySettings}, sessions, &start); !r.OK || !start {
		t.Fatal(r)
	}
	srv.runDrain()
	time.Sleep(100 * time.Millisecond)
	if !srv.Draining() {
		t.Fatal("settings apply expired while composing")
	}
	call(Envelope{Op: OpHandle, Session: ids[0], Req: &proto.Request{Type: proto.Cancel}})
	select {
	case <-drained:
		t.Fatal("another field is still composing")
	case <-time.After(100 * time.Millisecond):
	}
	call(Envelope{Op: OpHandle, Session: ids[1], Req: &proto.Request{Type: proto.Cancel}})
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("settings did not apply after the last field finished")
	}
}
