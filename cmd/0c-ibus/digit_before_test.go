//go:build linux

package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/godbus/dbus/v5"
)

// digitScorer 記下每個請求帶不帶「游標前一個字是阿拉伯數字」（#337）。
type digitScorer struct {
	mu   sync.Mutex
	last *bool
}

func (d *digitScorer) Score(r rerank.Request) ([]float64, error) {
	d.mu.Lock()
	v := r.DigitBefore
	d.last = &v
	d.mu.Unlock()
	return make([]float64, len(r.Candidates)), nil
}

func (d *digitScorer) take() *bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	v := d.last
	d.last = nil
	return v
}

// digitEngine：外殼宣告前文確認（同出貨的預設），引擎接一個宣告 DigitContext 的評分器。
func digitEngine(t *testing.T) (*engine, *digitScorer) {
	t.Helper()
	dir, err := os.MkdirTemp("", "0c-ibus-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	c := &client{socket: sock, server: filepath.Join(dir, "missing-server"), startWait: 100 * time.Millisecond}
	svc := &service{signal: (&recorder{}).emit, client: c, engines: map[dbus.ObjectPath]*engine{}, documentContext: true}
	e := &engine{svc: svc, path: enginePrefix + "1", session: newSession(c, true)}
	svc.focused = e
	eng := toyEngine(t)
	sc := &digitScorer{}
	eng.Rerank = rerank.Options{Scorer: sc, Weight: 1, ContextRunes: rerank.MaxContextRunes, DigitContext: true}
	t.Cleanup(startServer(t, sock, eng))
	return e, sc
}

// 英數模式打 3（交還應用程式）、切回注音打「豆腐」：選字模型收到旗標。中間有快捷鍵或失焦（點到別處的重設）時，
// 游標不一定還在 3 後面，外殼讓核心清掉。
func TestDigitBeforeSurvivesToggleButNotShortcutsOrFocus(t *testing.T) {
	for _, c := range []struct {
		name    string
		between func(*engine)
		want    bool
	}{
		{"切回注音直接打", nil, true},
		{"Ctrl+V", func(e *engine) { e.processKey('v', 0, maskControl) }, false},
		{"失焦", func(e *engine) { e.finish("focus_out") }, false},
		{"點到別處的重設", func(e *engine) { e.finish("reset") }, false},
		{"刪掉數字", func(e *engine) { e.processKey(keyBackSpace, 0, 0) }, false},
	} {
		e, sc := digitEngine(t)
		e.toggleMode()
		if e.processKey('3', 0, 0) {
			t.Fatal("英數模式的 3 應交還應用程式")
		}
		if !e.session.digitPassed {
			t.Fatal("外殼應記得上一個交還的是數字")
		}
		if c.between != nil {
			c.between(e)
		}
		e.toggleMode()
		sc.take()
		typeKeys(e, "2.4zj3") // ㄉㄡˋ ㄈㄨˇ
		got := sc.take()
		if got == nil {
			t.Fatalf("%s：沒有送評分（組字 %q）", c.name, e.preedit)
		}
		if *got != c.want {
			t.Fatalf("%s：DigitBefore %v", c.name, *got)
		}
	}
}

// 沒有交還過數字時，失焦不為此送請求、也不啟動引擎。
func TestForgetDigitOnlyAfterADigit(t *testing.T) {
	e, _, _ := newTestEngine(t)
	e.finish("focus_out")
	if e.session.client.conn != nil {
		t.Fatal("失焦不該為此連線")
	}
	e.session.digitPassed = true
	e.finish("focus_out")
	if e.session.digitPassed || e.session.client.conn != nil {
		t.Fatal("沒有 session 時只清掉外殼的記號，不連線")
	}
}
