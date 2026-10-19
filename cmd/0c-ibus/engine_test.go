//go:build linux

package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"git.biggo.com/0c/ime/bridge/ipc"
	core "git.biggo.com/0c/ime/core/engine"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/proto"
	"github.com/godbus/dbus/v5"
)

func toyEngine(t *testing.T) *core.Engine {
	t.Helper()
	lf, err := os.Open("../../testdata/toy/lexicon.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	lex, err := lexicon.Load(lf)
	if err != nil {
		t.Fatal(err)
	}
	mf, err := os.Open("../../testdata/toy/lm.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer mf.Close()
	sys, err := lm.Load(mf)
	if err != nil {
		t.Fatal(err)
	}
	return core.New(lex, sys)
}

// startServer 在短路徑的 unix socket 上跑真正的 ipc.Server；回傳的函式停止它（模擬 server 重啟）。
func startServer(t *testing.T, sock string, eng *core.Engine) func() {
	t.Helper()
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &ipc.Server{Engine: eng}
	go srv.Serve(l)
	return func() { l.Close() }
}

type recorded struct {
	name string
	args []interface{}
}

type recorder struct {
	mu      sync.Mutex
	signals []recorded
}

func (r *recorder) emit(_ dbus.ObjectPath, name string, args ...interface{}) error {
	r.mu.Lock()
	r.signals = append(r.signals, recorded{strings.TrimPrefix(name, ifaceEngine+"."), args})
	r.mu.Unlock()
	return nil
}

func (r *recorder) take() []recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.signals
	r.signals = nil
	return out
}

func variantText(t *testing.T, v interface{}) string {
	t.Helper()
	s, ok := textOf(v.(dbus.Variant))
	if !ok {
		t.Fatalf("不是 IBusText：%#v", v)
	}
	return s
}

func newTestEngine(t *testing.T) (*engine, *recorder, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "0c-ibus-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	rec := &recorder{}
	c := &client{socket: sock, server: filepath.Join(dir, "missing-server"), startWait: 100 * time.Millisecond}
	svc := &service{signal: rec.emit, client: c, engines: map[dbus.ObjectPath]*engine{}}
	e := &engine{svc: svc, path: enginePrefix + "1", session: newSession(c, false)}
	svc.focused = e
	return e, rec, sock
}

// keys 依標準注音排列打字：ㄨ=j ㄛ=i ˇ=3。
func typeKeys(e *engine, keys string) {
	for _, k := range keys {
		e.processKey(uint32(k), 0, 0)
		e.processKey(uint32(k), 0, maskRelease)
	}
}

func TestSerializedSignatures(t *testing.T) {
	for _, c := range []struct {
		v    dbus.Variant
		want string
	}{
		{newText("我", attribute{attrUnderline, underlineSingle, 0, 1}), "(sa{sv}sv)"},
		{newLookupTable([]string{"我"}, []string{"1"}, 0), "(sa{sv}uubbiavav)"},
		{property{key: "k", label: "l"}.variant(), "(sa{sv}suvsvbbuvv)"},
		{newPropList([]property{{key: "k"}}), "(sa{sv}av)"},
	} {
		if got := c.v.Signature().String(); got != c.want {
			t.Errorf("signature = %s，要 %s", got, c.want)
		}
	}
	attrs := newText("ab", attribute{attrUnderline, underlineError, 0, 2}).Value().(ibusText).Attrs.Value().(ibusAttrList)
	if a := attrs.Attrs[0].Value().(ibusAttribute); a.Name != "IBusAttribute" || a.Type != attrUnderline || a.End != 2 {
		t.Fatalf("attribute = %#v", a)
	}
}

func TestToKey(t *testing.T) {
	for _, c := range []struct {
		keyval, state uint32
		want          string
	}{
		{'j', 0, "j"}, {'J', maskShift, "J"}, {'3', 0, "3"}, {';', 0, ";"}, {keySpace, 0, " "},
		{keyReturn, 0, "Enter"}, {keyKPEnter, 0, "Enter"}, {keyBackSpace, 0, "Backspace"},
		{keyTab, 0, "Tab"}, {keyTab, maskShift, "BackTab"}, {keyISOLeftTab, maskShift, "BackTab"}, {keyUp, 0, "Up"}, {keyPageUp, 0, "PageUp"}, {keyPageDown, 0, "PageDown"},
		{keyKP0 + 3, 0, "KP3"}, {keyKPDecimal, 0, "KP."}, {keyKPSubtract, 0, "KP-"}, {keyKPEqual, 0, "KP="},
		{'c', maskControl, ""}, {'x', maskMod1, ""}, {'a', maskSuper, ""}, {'a', maskMod4, ""},
		{keyHome, 0, "Home"}, {keyEnd, maskShift, "End"}, {keyKPHome, 0, "Home"}, {keyKPEnd, 0, "End"},
		{keyHome, maskControl, ""}, {0x4e00, 0, ""}, {keyShiftL, 0, ""},
	} {
		if got := toKey(c.keyval, c.state); got != c.want {
			t.Errorf("toKey(%#x, %#x) = %q，要 %q", c.keyval, c.state, got, c.want)
		}
	}
}

func TestOrdererServesInArrivalOrder(t *testing.T) {
	o := newOrderer()
	msgs := make([]*dbus.Message, 20)
	for i := range msgs {
		msgs[i] = &dbus.Message{Type: dbus.TypeMethodCall, Headers: map[dbus.HeaderField]dbus.Variant{
			dbus.FieldPath:      dbus.MakeVariant(dbus.ObjectPath(enginePrefix + "1")),
			dbus.FieldInterface: dbus.MakeVariant(ifaceEngine),
		}}
		o.intercept(msgs[i])
	}
	var mu sync.Mutex
	var order []int
	var wg sync.WaitGroup
	for i := len(msgs) - 1; i >= 0; i-- { // 反向啟動，模擬 goroutine 排程打亂
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			done := o.wait(msgs[i])
			mu.Lock()
			order = append(order, i)
			mu.Unlock()
			done()
		}(i)
		time.Sleep(time.Millisecond)
	}
	wg.Wait()
	for i, v := range order {
		if v != i {
			t.Fatalf("處理順序 %v", order)
		}
	}
	// 沒有發號碼牌的呼叫（Peer、其他路徑）不等待。
	o.wait(&dbus.Message{})()
}

func TestTypeCommitsWithPreeditClearedFirst(t *testing.T) {
	e, rec, sock := newTestEngine(t)
	defer startServer(t, sock, toyEngine(t))()
	typeKeys(e, "ji3")
	if e.preedit != "我" {
		t.Fatalf("組字 = %q", e.preedit)
	}
	rec.take()
	if !e.processKey(keyReturn, 0, 0) {
		t.Fatal("Enter 應由輸入法處理")
	}
	sig := rec.take()
	// 先清掉 preeditCommit 模式的組字，再提交；否則用戶端可能再提交一次組字。
	if len(sig) < 2 || sig[0].name != "UpdatePreeditText" || variantText(t, sig[0].args[0]) != "" ||
		sig[1].name != "CommitText" || variantText(t, sig[1].args[0]) != "我" {
		t.Fatalf("signals = %+v", sig)
	}
	for _, s := range sig {
		if s.name == "UpdatePreeditText" && s.args[3].(uint32) != preeditCommit {
			t.Fatalf("組字模式 = %v", s.args[3])
		}
	}
}

func TestFocusOutDoesNotCommitAgain(t *testing.T) {
	e, rec, sock := newTestEngine(t)
	defer startServer(t, sock, toyEngine(t))()
	typeKeys(e, "ji3")
	rec.take()
	e.finish("focus_out")
	for _, s := range rec.take() {
		if s.name == "CommitText" {
			t.Fatal("ibus-daemon 已提交組字，引擎不得再提交")
		}
	}
	if e.preedit != "" || len(e.session.log) != 0 {
		t.Fatalf("失焦後仍有組字 %q／%d", e.preedit, len(e.session.log))
	}
}

func TestCandidatesAndMouseSelect(t *testing.T) {
	e, rec, sock := newTestEngine(t)
	defer startServer(t, sock, toyEngine(t))()
	typeKeys(e, "ji3")
	rec.take()
	e.processKey(keyDown, 0, 0)
	if len(e.table) == 0 || len(e.ids) != len(e.table) {
		t.Fatalf("候選 %v／%v", e.table, e.ids)
	}
	var table *recorded
	for _, s := range rec.take() {
		if s.name == "UpdateLookupTable" {
			s := s
			table = &s
		}
	}
	if table == nil || !table.args[1].(bool) {
		t.Fatal("沒有顯示候選表")
	}
	lt := table.args[0].(dbus.Variant).Value().(ibusLookupTable)
	if len(lt.Candidates) != len(e.table) || len(lt.Labels) != len(e.table) || lt.Labels[0].Value().(ibusText).Text != "1" {
		t.Fatalf("候選表 %#v", lt)
	}
	e.selectCandidate(0)
	if len(e.table) != 0 {
		t.Fatalf("選字後候選仍在 %v", e.table)
	}
}

func TestRightShiftTogglesModeAndProperty(t *testing.T) {
	e, rec, sock := newTestEngine(t)
	defer startServer(t, sock, toyEngine(t))()
	e.processKey(keyShiftR, 0, 0)
	if e.processKey(keyShiftR, 0, maskRelease) || !e.english {
		t.Fatal("右 Shift 單按應切到英文，放開照樣交給應用程式（#165）")
	}
	found := false
	for _, s := range rec.take() {
		if s.name == "UpdateProperty" {
			p := s.args[0].(dbus.Variant).Value().(ibusProperty)
			found = p.Key == propInputMode && p.Symbol.Value().(ibusText).Text == "英"
		}
	}
	if !found {
		t.Fatal("沒有更新注／英指示")
	}
	// 右 Shift 與其他鍵組合不切換。
	e.processKey(keyShiftR, 0, 0)
	e.processKey('a', maskShift, 0)
	if e.processKey(keyShiftR, maskShift, maskRelease) || !e.english {
		t.Fatal("組合鍵不應切換")
	}
}

// tapRightShift 單按右 Shift，回傳注／英有沒有切換（放開一律交給應用程式，見 TestShiftReleaseReachesApp）。
func tapRightShift(e *engine) bool {
	before := e.english
	e.processKey(keyShiftR, 0, 0)
	e.processKey(keyShiftR, maskShift, maskRelease)
	return e.english != before
}

// #90：切到英文清掉未打完的注音；立即再按一次放回，中間有按鍵、快捷鍵或失焦就不放回。
func TestShiftClearsPendingAndImmediateTapRestores(t *testing.T) {
	e, _, sock := newTestEngine(t)
	stop := startServer(t, sock, toyEngine(t))
	typeKeys(e, "ji3j")
	if !tapRightShift(e) || !e.english || e.preedit != "我" {
		t.Fatalf("切到英文清掉未打完的注音、保留已組好的字：english=%v %q", e.english, e.preedit)
	}
	if !tapRightShift(e) || e.english || e.preedit != "我ㄨ" {
		t.Fatalf("立即切回放回注音：english=%v %q", e.english, e.preedit)
	}
	for _, c := range []struct {
		name    string
		between func()
		want    string
	}{
		{"英文字母", func() { typeKeys(e, "a"); e.processKey(keyBackSpace, 0, 0) }, "我"},
		{"快捷鍵", func() { e.processKey('c', 0, maskControl) }, ""}, // #106：快捷鍵先送出組字
	} {
		tapRightShift(e)
		c.between()
		if !tapRightShift(e) || e.english || e.preedit != c.want {
			t.Fatalf("%s之後切回不放回注音：english=%v %q", c.name, e.english, e.preedit)
		}
		typeKeys(e, "j")
	}
	// 只有未打完的注音：切到英文結束組字；全域 engine 失焦後切回不能把注音帶到別的視窗。
	e.session.handle(proto.Request{Type: proto.Cancel})
	typeKeys(e, "j")
	tapRightShift(e)
	e.finish("focus_out")
	if !tapRightShift(e) || e.english || e.preedit != "" {
		t.Fatalf("失焦之後切回不放回注音：english=%v %q", e.english, e.preedit)
	}
	// 立即切回重新開始組字，斷線重播後仍是放回的注音。
	typeKeys(e, "j")
	if !tapRightShift(e) || e.preedit != "" || !tapRightShift(e) || e.preedit != "ㄨ" {
		t.Fatalf("空白組字的誤觸也能放回：%q", e.preedit)
	}
	stop()
	e.svc.client.close()
	defer startServer(t, sock, toyEngine(t))()
	typeKeys(e, "i")
	if e.english || e.preedit != "ㄨㄛ" || e.aux != "" {
		t.Fatalf("重連重播後放回的注音仍在：english=%v %q aux=%q", e.english, e.preedit, e.aux)
	}
}

// GNOME Text Editor 把每個 Shift 按放都送兩次；重複的按下與放開仍算一次單按。
func TestShiftTapSurvivesDuplicatedEvents(t *testing.T) {
	e, _, sock := newTestEngine(t)
	defer startServer(t, sock, toyEngine(t))()
	typeKeys(e, "ji3j")
	e.processKey(keyShiftR, 0, 0)
	e.processKey(keyShiftR, 0, 0)
	consumed := e.processKey(keyShiftR, maskShift, maskRelease)
	consumed = e.processKey(keyShiftR, maskShift, maskRelease) || consumed
	if consumed || !e.english || e.preedit != "我" {
		t.Fatalf("重複事件的右 Shift 單按應切到英文並保留已組好的字：consumed=%v english=%v %q", consumed, e.english, e.preedit)
	}
	// 重複事件之間夾了其他鍵（Shift＋字母），仍不切換。
	e.processKey(keyShiftR, 0, 0)
	e.processKey(keyShiftR, 0, 0)
	e.processKey('A', maskShift, 0)
	if e.processKey(keyShiftR, maskShift, maskRelease) || e.processKey(keyShiftR, maskShift, maskRelease) || !e.english {
		t.Fatal("組合鍵不應切換")
	}
	// 按住左 Shift 再點右 Shift（左右同時按），兩側都不算單按。
	for _, order := range [][2]uint32{{keyShiftL, keyShiftR}, {keyShiftR, keyShiftL}} {
		toggled := false
		for _, k := range []uint32{order[0], order[0], order[1], order[1]} {
			toggled = e.processKey(k, 0, 0) || toggled
		}
		for _, k := range []uint32{order[1], order[1], order[0], order[0]} {
			toggled = e.processKey(k, maskShift, maskRelease) || toggled
		}
		if toggled || !e.english {
			t.Fatalf("左右 Shift 同時按不應切換：%v", order)
		}
	}
}

func TestLeftShiftTogglesOnlyWhenEnabled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		e, _, sock := newTestEngine(t)
		l, err := net.Listen("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		srv := &ipc.Server{Engine: toyEngine(t), Info: map[string]string{}}
		if enabled {
			srv.Info["left_shift"] = "true"
		}
		go srv.Serve(l)
		typeKeys(e, "ji3")
		e.processKey(keyShiftL, 0, 0)
		if e.processKey(keyShiftL, maskShift, maskRelease) || e.english != enabled {
			t.Fatalf("enabled=%v：左 Shift 單按切換=%v", enabled, e.english)
		}
		if enabled {
			if e.preedit != "我" {
				t.Fatalf("左 Shift 切換應保留組字：%q", e.preedit)
			}
			// Shift 加字母打大寫，不切換。
			e.processKey(keyShiftL, 0, 0)
			e.processKey('A', maskShift, 0)
			if e.processKey(keyShiftL, maskShift, maskRelease) || !e.english {
				t.Fatal("左 Shift 組合鍵不應切換")
			}
			// 開啟時左右同時按也不切換。
			e.processKey(keyShiftL, 0, 0)
			e.processKey(keyShiftR, maskShift, 0)
			r := e.processKey(keyShiftR, maskShift, maskRelease)
			if l := e.processKey(keyShiftL, maskShift, maskRelease); r || l || !e.english {
				t.Fatalf("左右 Shift 同時按不應切換：右=%v 左=%v", r, l)
			}
		}
		l.Close()
	}
}

func TestReconnectReplaysComposition(t *testing.T) {
	e, _, sock := newTestEngine(t)
	stop := startServer(t, sock, toyEngine(t))
	typeKeys(e, "ji")
	stop() // server 重啟：外殼重連後重播，組字不遺失
	e.svc.client.close()
	defer startServer(t, sock, toyEngine(t))()
	typeKeys(e, "3")
	if e.preedit != "我" {
		t.Fatalf("重連後組字 = %q", e.preedit)
	}
}

func TestServerUnavailablePassesKeys(t *testing.T) {
	e, _, _ := newTestEngine(t)
	if e.processKey('j', 0, 0) {
		t.Fatal("server 不可用時應放行按鍵")
	}
}

func TestPrivateFields(t *testing.T) {
	e, _, _ := newTestEngine(t)
	for _, c := range []struct {
		purpose, hints uint32
		want           bool
	}{{0, 0, false}, {purposePassword, 0, true}, {purposePIN, 0, true}, {0, hintPrivate, true}} {
		e.purpose, e.hints = c.purpose, c.hints
		if e.private() != c.want {
			t.Errorf("purpose %d hints %#x：private = %v", c.purpose, c.hints, !c.want)
		}
	}
}

func TestDocumentContextConfirmsOnlyMatchingUpdate(t *testing.T) {
	var d documentContext
	before := docText{text: "你好", cursor: 2, anchor: 2, valid: true}
	d.committed(before, "我", 7)
	d.observed(docText{text: "你好我", cursor: 3, anchor: 3, valid: true})
	if a, ok := d.takeAction(); !ok || a != 7 {
		t.Fatal("相符的文件更新應確認上下文")
	}
	d.committed(before, "我", 8)
	d.observed(docText{text: "你好", cursor: 2, anchor: 2, valid: true})
	if d.tracked() {
		t.Fatal("不相符的更新應放棄上下文")
	}
	d.committed(before, "我", 9)
	d.validate(docText{text: "你好我", cursor: 3, anchor: 3, valid: true})
	if d.tracked() {
		t.Fatal("尚未收到更新就開始下一段時應放棄")
	}
}

func TestParseStatusAndLabels(t *testing.T) {
	s := parseStatus(`{"installed":"0.1.0-dev.57","last_success":"2026-10-13T00:00:00Z","settings":{"selection":"neural"}}`)
	if got := statusLabel(s); got != "靈犀注音 0.1.0-dev.57 · 已是最新" {
		t.Fatalf("label = %q", got)
	}
	s = parseStatus(`{"installed":"a","target":{"version":"b"},"action":"finish"}`)
	if got := statusLabel(s); got != "b 已準備好 — 現在完成更新" || strings.Join(statusCommand(s), " ") != "finish -notify" {
		t.Fatalf("label = %q／%v", got, statusCommand(s))
	}
	if statusLabel(parseStatus("not json")) != "靈犀注音：無法讀取更新狀態" {
		t.Fatal("無法解析時應說明")
	}
}

// propVisibility 取出 RegisterProperties 訊號裡的屬性鍵與 visible（依順序），例如 "vocab:false"。
func propVisibility(t *testing.T, s recorded) []string {
	t.Helper()
	var keys []string
	for _, v := range s.args[0].(dbus.Variant).Value().(ibusPropList).Props {
		p := v.Value().(ibusProperty)
		keys = append(keys, p.Key+":"+strconv.FormatBool(p.Visible))
	}
	return keys
}

// vocabUpdate 回傳 refreshMenu 送出的 UpdateProperty 鍵順序，以及「我的詞彙…」的 visible。
func vocabUpdate(t *testing.T, sig []recorded) (string, bool) {
	t.Helper()
	var keys []string
	visible := false
	for _, s := range sig {
		if s.name != "UpdateProperty" {
			t.Fatalf("只能送 UpdateProperty（GNOME 換 engine 後不再收 RegisterProperties）：%+v", sig)
		}
		p := s.args[0].(dbus.Variant).Value().(ibusProperty)
		keys = append(keys, p.Key)
		if p.Key == propVocab {
			visible = p.Visible
			if p.Label.Value().(ibusText).Text != vocabLabel && !strings.HasPrefix(p.Label.Value().(ibusText).Text, vocabLabel+"（") {
				t.Fatalf("label = %v", p.Label)
			}
		}
	}
	return strings.Join(keys, ","), visible
}

// #125：「我的詞彙…」一律登錄在設定下面，visible 跟著 features.vocab；開關改變只送 UpdateProperty。
func TestVocabMenuFollowsFeatureFlag(t *testing.T) {
	for _, j := range []string{`{"installed":"x"}`, `{"features":{"vocab":false}}`, `{"features":{"vocab":"true"}}`, `{"features":true}`, `not json`} {
		if parseStatus(j).vocab {
			t.Fatalf("%s 不應顯示我的詞彙", j)
		}
	}
	if s := parseStatus(`{"installed":"0.2.0","features":{"vocab":true},"runtime":{"selection_state":"ready"}}`); !s.vocab || s.installed != "0.2.0" || s.selectionState != "ready" {
		t.Fatalf("features 不能影響其他欄位：%+v", s)
	}
	if s := parseStatus(`{"installed":"0.2.0","features":"bogus"}`); !s.ok || s.installed != "0.2.0" {
		t.Fatalf("features 型別不對時其他欄位照常讀取：%+v", s)
	}

	e, rec, _ := newTestEngine(t)
	svc := e.svc
	e.registerProperties()
	if got := strings.Join(propVisibility(t, rec.take()[0]), ","); got != "InputMode:true,settings:true,vocab:false,update:true" {
		t.Fatalf("開關關閉：%s", got)
	}
	svc.update = parseStatus(`{"installed":"x","features":{"vocab":true}}`)
	svc.refreshMenu()
	if keys, visible := vocabUpdate(t, rec.take()); keys != "settings,vocab,update" || !visible {
		t.Fatalf("開關打開：%s visible=%v", keys, visible)
	}
	e.registerProperties() // 換焦點時重新登錄：已經是看得到的
	if got := strings.Join(propVisibility(t, rec.take()[0]), ","); got != "InputMode:true,settings:true,vocab:true,update:true" {
		t.Fatalf("開關打開後重新登錄：%s", got)
	}

	// 點了執行 0c-update open-settings vocab（以記錄參數的假更新器代替）。
	home := t.TempDir()
	t.Setenv("HOME", home)
	calls := filepath.Join(home, "calls")
	if err := os.MkdirAll(filepath.Join(home, ".local/bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".local/bin/0c-update"), []byte("#!/bin/sh\necho \"$@\" >> "+calls+"\n[ \"$2\" = vocab ] && { echo 測試失敗; exit 1; }\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	wait := func(cond func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			svc.mu.Lock()
			ok := cond()
			svc.mu.Unlock()
			if ok {
				return
			}
		}
		t.Fatal("逾時")
	}
	if _, err := svc.engineMethod(e, "PropertyActivate", []interface{}{propVocab, uint32(0)}); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { return !svc.openingSettings })
	if b, _ := os.ReadFile(calls); strings.TrimSpace(string(b)) != "open-settings vocab" {
		t.Fatalf("calls = %q", b)
	}
	// 失敗原因顯示在被點的那一列；點設定列成功後兩列的原因都清掉。
	svc.mu.Lock()
	if svc.vocabProperty().label != vocabLabel+"（開啟失敗：測試失敗，點此重試）" || svc.settingsFailure != "" {
		t.Fatalf("vocab = %q settings = %q", svc.vocabProperty().label, svc.settingsFailure)
	}
	svc.mu.Unlock()
	if _, err := svc.engineMethod(e, "PropertyActivate", []interface{}{propSettings, uint32(0)}); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { return !svc.openingSettings })
	if b, _ := os.ReadFile(calls); !strings.HasSuffix(strings.TrimSpace(string(b)), "\nopen-settings") {
		t.Fatalf("calls = %q", b)
	}
	svc.mu.Lock()
	if svc.vocabFailure != "" || svc.settingsFailure != "" {
		t.Fatalf("成功後要清掉失敗原因：%q %q", svc.vocabFailure, svc.settingsFailure)
	}
	// 開關關回：只送 UpdateProperty，我的詞彙改成看不到。
	rec.take()
	svc.update = parseStatus(`{"installed":"x"}`)
	svc.refreshMenu()
	svc.mu.Unlock()
	if keys, visible := vocabUpdate(t, rec.take()); keys != "settings,vocab,update" || visible {
		t.Fatalf("開關關回：%s visible=%v", keys, visible)
	}
}

func TestComponentDescribesEngine(t *testing.T) {
	x := component("/home/u/.local/bin/0c-ibus", "/home/u")
	for _, want := range []string{"<name>" + componentName + "</name>", "<exec>/home/u/.local/bin/0c-ibus --ibus</exec>",
		"<name>0c</name>", "<language>zh_TW</language>", "<layout>us</layout>", "<symbol>注</symbol>",
		"/home/u/.local/share/icons/hicolor/scalable/status/0c-zhuyin.svg"} {
		if !strings.Contains(x, want) {
			t.Errorf("元件描述缺 %s", want)
		}
	}
}

func TestInputSourceListEdits(t *testing.T) {
	for _, c := range []struct{ cur, add, remove string }{
		{"[('xkb', 'us')]", "[('xkb', 'us'), ('ibus', '0c')]", "[('xkb', 'us')]"},
		{"@a(ss) []", "[('xkb', 'de+nodeadkeys'), ('ibus', '0c')]", "@a(ss) []"},
		{"[('xkb', 'us'), ('ibus', '0c')]", "[('xkb', 'us'), ('ibus', '0c')]", "[('xkb', 'us')]"},
		{"[('ibus', '0c'), ('xkb', 'tw')]", "[('ibus', '0c'), ('xkb', 'tw')]", "[('xkb', 'tw')]"},
	} {
		if got := withSource(c.cur, "de+nodeadkeys"); got != c.add {
			t.Errorf("withSource(%s) = %s，要 %s", c.cur, got, c.add)
		}
		if got := withoutSource(c.cur); got != c.remove {
			t.Errorf("withoutSource(%s) = %s，要 %s", c.cur, got, c.remove)
		}
	}
}

func TestGuardRefusesWhileComposingOrTyping(t *testing.T) {
	e, _, sock := newTestEngine(t)
	defer startServer(t, sock, toyEngine(t))()
	s := e.svc
	s.engines[e.path] = e
	g := guard{s}
	typeKeys(e, "ji3")
	if answer, _ := g.ExitForUpdate(0); answer != "composing" {
		t.Fatalf("組字中 = %q", answer)
	}
	e.processKey(keyReturn, 0, 0)
	s.lastKey = time.Now()
	if answer, _ := g.ExitForUpdate(60); answer != "typing" {
		t.Fatalf("剛打字 = %q", answer)
	}
	raw, _ := g.Status()
	var st guardStatus
	json.Unmarshal([]byte(raw), &st)
	if st.Busy || st.Frontend != "ibus" || !st.Active || st.Version != buildVersion {
		t.Fatalf("status = %+v", st)
	}
	exited := make(chan struct{})
	exitProcess = func() { close(exited) }
	defer func() { exitProcess = func() { os.Exit(0) } }()
	s.lastKey = time.Now().Add(-2 * time.Minute)
	if answer, _ := g.ExitForUpdate(60); answer != "ready" {
		t.Fatalf("閒置 = %q", answer)
	}
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("沒有結束外殼")
	}
	if s.mu.TryLock() {
		t.Fatal("答應退出後要一直持有鎖，不再處理按鍵")
	}
}

// #106：組字中 Home／End 移動組字游標，其他導覽鍵吃掉不動作；沒在組字時交還應用程式。
func TestHomeEndMoveCompositionCursor(t *testing.T) {
	e, _, sock := newTestEngine(t)
	defer startServer(t, sock, toyEngine(t))()
	for _, k := range []struct{ keyval, state uint32 }{{keyHome, 0}, {keyEnd, 0}, {keyUp, 0}, {keyPageDown, 0}, {keyTab, 0}, {keyISOLeftTab, maskShift}} {
		if e.processKey(k.keyval, 0, k.state) {
			t.Fatalf("沒在組字時 %#x 應交還應用程式", k.keyval)
		}
	}
	typeKeys(e, "ji3ji3")
	if !e.processKey(keyHome, 0, 0) || e.preCursor != 0 {
		t.Fatalf("Home 應由輸入法處理並移到最前，游標 %d", e.preCursor)
	}
	if !e.processKey(keyKPEnd, 0, 0) || e.preCursor != uint32(utf8.RuneCountInString(e.preedit)) {
		t.Fatalf("End 應移到最後，游標 %d／%q", e.preCursor, e.preedit)
	}
	// ↑、PgUp、PgDn、沒有補全的 Tab、Shift+Tab 在組字中吃掉不動作（#106）。
	text, at := e.preedit, e.preCursor
	for _, k := range []struct{ keyval, state uint32 }{{keyUp, 0}, {keyPageUp, 0}, {keyPageDown, 0}, {keyTab, 0}, {keyISOLeftTab, maskShift}} {
		if !e.processKey(k.keyval, 0, k.state) || e.preedit != text || e.preCursor != at || len(e.table) != 0 {
			t.Fatalf("組字中 %#x 應吃掉不動作：%q／%d", k.keyval, e.preedit, e.preCursor)
		}
	}
	// 候選窗中 Backspace 關閉候選、保留組字。
	e.processKey(keyDown, 0, 0)
	if len(e.table) == 0 {
		t.Fatal("應開候選")
	}
	if !e.processKey(keyBackSpace, 0, 0) || len(e.table) != 0 || e.preedit != text {
		t.Fatalf("候選中 Backspace 應關窗保留組字：%q／%v", e.preedit, e.table)
	}
	// 英文模式保留的中文組字：Home 仍在組字區移動。
	if !tapRightShift(e) || !e.english || e.preedit != text {
		t.Fatal("右 Shift 應切到英文並保留組字")
	}
	if !e.processKey(keyHome, 0, 0) || e.preCursor != 0 {
		t.Fatalf("英文模式保留的組字 Home 應移到最前，游標 %d", e.preCursor)
	}
	// Ctrl＋Home 是應用程式的快捷鍵：先送出組字，再交給應用程式（#106）。
	if e.processKey(keyHome, 0, maskControl) || e.preedit != "" {
		t.Fatalf("Ctrl＋Home 應先送出組字再交還應用程式：%q", e.preedit)
	}
}

// #106：組字中按 Ctrl／Alt／Super 組合鍵先送出組字，再把原鍵交給應用程式；單按修飾鍵不送出。
func TestShortcutCommitsCompositionFirst(t *testing.T) {
	e, rec, sock := newTestEngine(t)
	defer startServer(t, sock, toyEngine(t))()
	typeKeys(e, "ji3")
	rec.take()
	const keyControlL = 0xffe3
	if e.processKey(keyControlL, 0, 0) || e.processKey(keyControlL, 0, maskControl) || e.preedit != "我" {
		t.Fatalf("單按 Ctrl 不得送出組字：%q", e.preedit)
	}
	for _, s := range rec.take() {
		if s.name == "CommitText" {
			t.Fatal("單按 Ctrl 不得提交")
		}
	}
	if e.processKey(keyReturn, 0, maskControl) {
		t.Fatal("Ctrl+Enter 應交給應用程式")
	}
	committed := false
	for _, s := range rec.take() {
		committed = committed || s.name == "CommitText" && variantText(t, s.args[0]) == "我"
	}
	if !committed || e.preedit != "" || len(e.session.log) != 0 {
		t.Fatalf("Ctrl+Enter 前應先送出組字：%v／%q", committed, e.preedit)
	}
	// 沒有組字時組合鍵不送請求、不提交。
	if e.processKey('v', 0, maskControl) {
		t.Fatal("Ctrl+V 應交給應用程式")
	}
	for _, s := range rec.take() {
		if s.name == "CommitText" {
			t.Fatal("沒有組字時不得提交")
		}
	}
	typeKeys(e, "ji3")
	rec.take()
	if e.processKey('a', 0, maskMod1) || e.preedit != "" {
		t.Fatalf("Alt 組合鍵也應先送出組字：%q", e.preedit)
	}
}

func TestVocabKey(t *testing.T) {
	asked := 0
	yes := func() bool { asked++; return true }
	no := func() bool { asked++; return false }
	for base, renamed := range map[string]string{"Left": "ShiftLeft", "Right": "ShiftRight", "Delete": "ShiftDelete", "Backspace": "ShiftBackspace"} {
		if got := vocabKey(base, true, yes); got != renamed {
			t.Errorf("引擎支援、按著 Shift：%s → %q，要 %q", base, got, renamed)
		}
		if got := vocabKey(base, true, no); got != base { // 舊引擎不認得新鍵名
			t.Errorf("引擎不支援：%s → %q", base, got)
		}
	}
	if asked != 8 {
		t.Fatalf("問了引擎 %d 次", asked)
	}
	asked = 0
	for _, k := range []string{"Left", "Right", "Delete", "Backspace"} {
		if got := vocabKey(k, false, yes); got != k {
			t.Errorf("沒按 Shift：%s → %q", k, got)
		}
	}
	for _, k := range []string{"A", "BackTab", "Home", "End", "Up", "Enter", " ", "KP4", ""} {
		if got := vocabKey(k, true, yes); got != k {
			t.Errorf("其他按鍵不換名：%q → %q", k, got)
		}
	}
	if asked != 0 {
		t.Fatalf("不換名的按鍵不該問引擎（%d 次）", asked)
	}
}

func TestPreeditAttrs(t *testing.T) {
	has := func(attrs []attribute, typ, value uint32) bool {
		for _, a := range attrs {
			if a.typ == typ && a.value == value && a.start == 2 && a.end == 4 {
				return true
			}
		}
		return false
	}
	for _, c := range []struct {
		style     string
		underline uint32
		marked    bool
		size      int
	}{
		{proto.StyleNormal, underlineSingle, false, 1}, {proto.StylePending, underlineSingle, false, 1},
		{proto.StyleLocked, underlineDouble, false, 1}, {proto.StyleCorrected, underlineError, false, 1},
		{proto.StyleMarked, underlineSingle, true, 3}, {"future", underlineSingle, false, 1},
		{proto.StyleReading, underlineSingle, false, 2},
	} {
		attrs := preeditAttrs(c.style, 2, 4)
		if len(attrs) != c.size || !has(attrs, attrUnderline, c.underline) ||
			c.marked && (!has(attrs, attrBackground, markedBackground) || !has(attrs, attrForeground, markedForeground)) ||
			c.style == proto.StyleReading && !has(attrs, attrForeground, readingForeground) {
			t.Errorf("%s：%+v", c.style, attrs)
		}
	}
}

// #162：中英混打打 20 時數字畫成灰字、提示列顯示讀音 ㄉㄢ（按空白出「單」、Enter 留 20）；通知優先，提示隨組字消失。
func TestReadingHintInAuxiliaryText(t *testing.T) {
	e, rec, _ := newTestEngine(t)
	snap := snapshot{ok: true}
	snap.Handled = true
	snap.Preedit = []proto.Segment{{Text: "20", Style: proto.StyleReading}}
	snap.Cursor = proto.CursorPos{Rune: 2, UTF16: 2}
	snap.ReadingHint = &proto.ReadingHint{Start: 0, Length: 2, Text: "ㄉㄢ"}
	e.render(snap)
	if e.aux != "ㄉㄢ" {
		t.Fatalf("讀音提示：%q", e.aux)
	}
	if _, attrs, ok := lastPreedit(t, rec.take()); !ok || !slices.ContainsFunc(attrs, func(a ibusAttribute) bool {
		return a.Type == attrForeground && a.Value == readingForeground && a.Start == 0 && a.End == 2
	}) {
		t.Fatalf("數字應畫成灰字：%+v", attrs)
	}
	snap.Notices = []string{"已加入詞庫：世傑"}
	e.render(snap)
	if e.aux != "已加入詞庫：世傑" {
		t.Fatalf("通知優先：%q", e.aux)
	}
	e.render(snapshot{ok: true, Snapshot: proto.Snapshot{Handled: true, Commit: "20"}})
	if e.aux != "" {
		t.Fatalf("提交後提示應消失：%q", e.aux)
	}
}

// vocabServer 跑打開 0.2.0 開關的引擎；vocab_keys 決定外殼送不送新鍵名。
func vocabServer(t *testing.T, sock string, vocabKeys bool) func() {
	t.Helper()
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	eng := toyEngine(t)
	eng.Preview02 = true
	srv := &ipc.Server{Engine: eng, Info: map[string]string{"vocab_keys": strconv.FormatBool(vocabKeys)}}
	go srv.Serve(l)
	return func() { l.Close() }
}

// lastPreedit 回傳最後一次 UpdatePreeditText 的文字與屬性；沒有重送時 ok=false。
func lastPreedit(t *testing.T, sig []recorded) (text string, attrs []ibusAttribute, ok bool) {
	t.Helper()
	for _, s := range sig {
		if s.name != "UpdatePreeditText" {
			continue
		}
		v := s.args[0].(dbus.Variant).Value().(ibusText)
		text, attrs, ok = v.Text, nil, true
		for _, a := range v.Attrs.Value().(ibusAttrList).Attrs {
			attrs = append(attrs, a.Value().(ibusAttribute))
		}
	}
	return text, attrs, ok
}

func markedRange(attrs []ibusAttribute) (start, end uint32, ok bool) {
	for _, a := range attrs {
		if a.Type == attrBackground && a.Value == markedBackground {
			return a.Start, a.End, true
		}
	}
	return 0, 0, false
}

func TestShiftArrowMarksAndStyleOnlyChangeRedraws(t *testing.T) {
	e, rec, sock := newTestEngine(t)
	defer vocabServer(t, sock, true)()
	typeKeys(e, "a86qi6") // ㄇㄚˊ ㄆㄛˊ
	if e.preedit != "麻婆" {
		t.Fatalf("組字 = %q", e.preedit)
	}
	rec.take()
	if !e.processKey(keyLeft, 0, maskShift) {
		t.Fatal("組字中 Shift＋← 應由輸入法處理")
	}
	text, attrs, ok := lastPreedit(t, rec.take())
	if start, end, marked := markedRange(attrs); !ok || text != "麻婆" || !marked || start != 1 || end != 2 || e.preCursor != 1 {
		t.Fatalf("Shift＋← 應反白「婆」：%q %+v 游標 %d", text, attrs, e.preCursor)
	}
	// Esc 取消標示：游標回到開始標示的位置（句尾），反白要消失。
	if !e.processKey(keyEscape, 0, 0) {
		t.Fatal("標示中 Esc 應由輸入法處理")
	}
	text, attrs, ok = lastPreedit(t, rec.take())
	if _, _, marked := markedRange(attrs); !ok || text != "麻婆" || marked || e.preCursor != 2 {
		t.Fatalf("Esc 後應重畫成沒有反白：重送=%v %q %+v", ok, text, attrs)
	}
	// 什麼都沒變的快照不重送（避免閃爍）。
	e.processKey(keyUp, 0, 0)
	if _, _, ok := lastPreedit(t, rec.take()); ok {
		t.Fatal("畫面沒變不應重送組字")
	}
	// Ctrl＋Shift＋← 是應用程式的快捷鍵：先送出組字再交還，不標示。
	if e.processKey(keyLeft, 0, maskShift|maskControl) || e.preedit != "" {
		t.Fatalf("Ctrl＋Shift＋← 應先送出組字再交給應用程式：%q", e.preedit)
	}
	// 沒在組字時 Shift＋← 交還應用程式（選取文字）。
	if e.processKey(keyLeft, 0, maskShift) {
		t.Fatal("沒在組字時 Shift＋← 應交給應用程式")
	}
}

func TestShiftArrowWithoutVocabKeysIsPlainArrow(t *testing.T) {
	e, rec, sock := newTestEngine(t)
	defer vocabServer(t, sock, false)()
	typeKeys(e, "a86qi6")
	rec.take()
	// 引擎沒回報 vocab_keys：送原本的 Left，與不按 Shift 相同（只移動游標，不標示）。
	if !e.processKey(keyLeft, 0, maskShift) {
		t.Fatal("組字中 Shift＋← 應照 ← 由輸入法處理")
	}
	_, attrs, _ := lastPreedit(t, rec.take())
	if _, _, marked := markedRange(attrs); marked || e.preCursor != 1 {
		t.Fatalf("沒有 vocab_keys 時只移動游標、不標示：游標 %d %+v", e.preCursor, attrs)
	}
	e.finish("focus_out")
	if e.processKey(keyLeft, 0, maskShift) {
		t.Fatal("沒在組字時 Shift＋← 應交給應用程式")
	}
}

func TestNoticeWinsOverCandidateTitle(t *testing.T) {
	e, _, _ := newTestEngine(t)
	snap := snapshot{ok: true}
	snap.Preedit = []proto.Segment{{Text: "世傑", Style: proto.StyleNormal}}
	snap.Candidates = &proto.CandidateList{Title: "修改「世傑」 · Tab 切換詞長", Page: 0, Pages: 2,
		Items: []proto.CandidateItem{{ID: 1, Text: "世傑"}, {ID: 2, Text: "事件"}}}
	snap.Notices = []string{"已從詞庫刪除：世傑（可在設定程式復原）"}
	e.render(snap)
	if e.aux != "已從詞庫刪除：世傑（可在設定程式復原）　1/2" {
		t.Fatalf("通知應優先於候選標題：%q", e.aux)
	}
	snap.Notices = nil // 下一個按鍵沒有通知：標題回來
	e.render(snap)
	if e.aux != "修改「世傑」 · Tab 切換詞長　1/2" {
		t.Fatalf("沒有通知時顯示候選標題：%q", e.aux)
	}
}

// 文字與游標都沒變、只有樣式變了（例如標示消失）仍要重畫，否則畫面上留著反白（#123）。
func TestStyleOnlyChangeRedraws(t *testing.T) {
	e, rec, _ := newTestEngine(t)
	marked := proto.Snapshot{Handled: true, Cursor: proto.CursorPos{Rune: 2, UTF16: 2}, Preedit: []proto.Segment{{Text: "麻", Style: proto.StyleNormal}, {Text: "婆", Style: proto.StyleMarked}}}
	plain := marked
	plain.Preedit = []proto.Segment{{Text: "麻婆", Style: proto.StyleNormal}}
	e.render(snapshot{Snapshot: marked, ok: true})
	rec.take()
	e.render(snapshot{Snapshot: plain, ok: true})
	if _, attrs, ok := lastPreedit(t, rec.take()); !ok {
		t.Fatal("只改樣式也要重送組字")
	} else if _, _, still := markedRange(attrs); still {
		t.Fatalf("反白應消失：%+v", attrs)
	}
	e.render(snapshot{Snapshot: plain, ok: true})
	if _, _, ok := lastPreedit(t, rec.take()); ok {
		t.Fatal("畫面沒變不應重送")
	}
}
