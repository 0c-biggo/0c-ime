package engine

import (
	"errors"
	"slices"
	"testing"

	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/zhconv"
	"git.biggo.com/0c/ime/core/zhuyin"
	"git.biggo.com/0c/ime/internal/simplified"
)

// 「計畫」的引擎：ㄐㄧˋ ㄏㄨㄚˋ（標準排列 ru4 cj84）。
func planEngine(t *testing.T) *Engine {
	t.Helper()
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct{ w, r string }{{"計畫", "ㄐㄧˋ ㄏㄨㄚˋ"}, {"計", "ㄐㄧˋ"}, {"畫", "ㄏㄨㄚˋ"}} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, 1e-3)
	}
	return New(lex, sys)
}

func typeKeys(t *testing.T, e *Engine, id uint64, keys string) proto.Snapshot {
	t.Helper()
	var snap proto.Snapshot
	for _, k := range keys {
		var err error
		if snap, err = e.Do(id, proto.Request{Type: proto.Key, Key: string(k)}); err != nil {
			t.Fatal(err)
		}
	}
	return snap
}

type switchLog struct {
	loads int
	saved []bool
	fail  error // Save 回傳的錯誤
}

func (l *switchLog) hooks() SimplifiedSwitch {
	return SimplifiedSwitch{
		Load: func() (*zhconv.Converter, error) { l.loads++; return simplified.TW2S() },
		Save: func(on bool) error { l.saved = append(l.saved, on); return l.fail },
	}
}

// #169：從選單切換「輸出簡體字」立即生效，不重啟引擎。所有輸入框（包括已經在組字區、還沒送出的字）照新的設定送出，
// 組字畫面不變；快照帶切換後的狀態與提示，宿主把新值寫進設定檔。
func TestOutputSimplifiedSwitchesEverySessionLive(t *testing.T) {
	e := planEngine(t)
	var log switchLog
	e.SimplifiedSwitch = log.hooks()
	a, b := e.OpenSession(), e.OpenSession()
	composing := typeKeys(t, e, b, "ru4cj84") // b 正在組字：計畫
	if composing.Text() != "計畫" || composing.OutputSimplified {
		t.Fatalf("切換前：%q %v", composing.Text(), composing.OutputSimplified)
	}
	snap, err := e.Do(a, proto.Request{Type: proto.OutputSimplified, On: true})
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Handled || !snap.OutputSimplified || !slices.Equal(snap.Notices, []string{NoticeSimplifiedOn}) {
		t.Fatalf("切換的快照：%+v", snap)
	}
	if !e.OutputSimplified() || !slices.Equal(log.saved, []bool{true}) || log.loads != 1 {
		t.Fatalf("引擎 %v、寫檔 %v、載入 %d 次", e.OutputSimplified(), log.saved, log.loads)
	}
	// 別的輸入框：組字區照樣是繁體，送出時轉成簡體；下一個快照就帶新的狀態。
	got, _ := e.Do(b, proto.Request{Type: proto.Commit})
	if got.Commit != "计划" || !got.OutputSimplified {
		t.Fatalf("組字中切換：送出 %q，狀態 %v", got.Commit, got.OutputSimplified)
	}
	// 之後開的 session 也是簡體。
	c := e.OpenSession()
	typeKeys(t, e, c, "ru4cj84")
	if got, _ := e.Do(c, proto.Request{Type: proto.Commit}); got.Commit != "计划" {
		t.Fatalf("新 session：%q", got.Commit)
	}
	// 切回繁體：組字中的內容照新設定送出；字典不重新載入。
	typeKeys(t, e, a, "ru4cj84")
	snap, _ = e.Do(c, proto.Request{Type: proto.OutputSimplified, On: false})
	if !snap.Handled || snap.OutputSimplified || !slices.Equal(snap.Notices, []string{NoticeSimplifiedOff}) {
		t.Fatalf("切回繁體：%+v", snap)
	}
	if got, _ := e.Do(a, proto.Request{Type: proto.Commit}); got.Commit != "計畫" || got.OutputSimplified {
		t.Fatalf("切回後送出 %q", got.Commit)
	}
	e.Do(a, proto.Request{Type: proto.OutputSimplified, On: true})
	if log.loads != 2 || !slices.Equal(log.saved, []bool{true, false, true}) {
		t.Fatalf("載入 %d 次、寫檔 %v", log.loads, log.saved) // Load 由宿主快取（simplified.TW2S 只解析一次）
	}
}

// 切換不動組字：組字區、游標與候選都跟切換前一樣，也不送出任何文字。
func TestOutputSimplifiedKeepsComposition(t *testing.T) {
	e := planEngine(t)
	var log switchLog
	e.SimplifiedSwitch = log.hooks()
	id := e.OpenSession()
	typeKeys(t, e, id, "ru4cj84")
	before, _ := e.Do(id, proto.Request{Type: proto.Key, Key: proto.KeyDown})
	after, _ := e.Do(id, proto.Request{Type: proto.OutputSimplified, On: true})
	if after.Commit != "" || after.Text() != before.Text() || after.Cursor != before.Cursor ||
		after.Candidates == nil || before.Candidates == nil || after.Candidates.Items[0] != before.Candidates.Items[0] {
		t.Fatalf("切換改了組字：\n前 %+v\n後 %+v", before, after)
	}
	e.Do(id, proto.Request{Type: proto.Key, Key: proto.KeyEscape})
	if got, _ := e.Do(id, proto.Request{Type: proto.Key, Key: proto.KeyEnter}); got.Commit != "计划" {
		t.Fatalf("送出 %q", got.Commit)
	}
}

// 宿主沒有提供即時切換（舊的用法、0c-cli）：回 handled=false、什麼都不改，外殼退回原本的設定流程。
func TestOutputSimplifiedWithoutHostSupportIsUnhandled(t *testing.T) {
	e := planEngine(t)
	id := e.OpenSession()
	typeKeys(t, e, id, "ru4")
	snap, err := e.Do(id, proto.Request{Type: proto.OutputSimplified, On: true})
	if err != nil || snap.Handled || snap.OutputSimplified || len(snap.Notices) != 0 || e.OutputSimplified() {
		t.Fatalf("%+v %v", snap, err)
	}
	if snap.Text() != "計" {
		t.Fatalf("組字被改了：%q", snap.Text())
	}
	if _, err := e.Do(99, proto.Request{Type: proto.OutputSimplified, On: true}); err == nil {
		t.Fatal("不存在的 session 要回錯誤")
	}
}

// 字典載入失敗：維持繁體、不寫設定檔，提示說明；寫設定檔失敗：照樣切換（這次登入有效），提示說明沒存起來。
func TestOutputSimplifiedReportsLoadAndSaveFailures(t *testing.T) {
	e := planEngine(t)
	saved := 0
	e.SimplifiedSwitch = SimplifiedSwitch{
		Load: func() (*zhconv.Converter, error) { return nil, errors.New("壞了") },
		Save: func(bool) error { saved++; return nil },
	}
	id := e.OpenSession()
	snap, _ := e.Do(id, proto.Request{Type: proto.OutputSimplified, On: true})
	if !snap.Handled || snap.OutputSimplified || e.OutputSimplified() || saved != 0 ||
		!slices.Equal(snap.Notices, []string{NoticeSimplifiedFail}) {
		t.Fatalf("載入失敗：%+v saved=%d", snap, saved)
	}
	var log switchLog
	log.fail = errors.New("唯讀")
	e.SimplifiedSwitch = log.hooks()
	snap, _ = e.Do(id, proto.Request{Type: proto.OutputSimplified, On: true})
	if !snap.OutputSimplified || !e.OutputSimplified() || len(snap.Notices) != 1 || snap.Notices[0] != NoticeSimplifiedOn+noticeNotSaved {
		t.Fatalf("寫檔失敗：%+v", snap)
	}
}

// 共用中英模式（#115）時也一樣：切換不是模式請求，不影響共用的模式。
func TestOutputSimplifiedInSharedMode(t *testing.T) {
	e := planEngine(t)
	e.SharedMode = true
	var log switchLog
	e.SimplifiedSwitch = log.hooks()
	a, b := e.OpenSession(), e.OpenSession()
	e.Do(a, proto.Request{Type: proto.Toggle})
	snap, _ := e.Do(b, proto.Request{Type: proto.OutputSimplified, On: true})
	if !snap.OutputSimplified {
		t.Fatal(snap)
	}
	if snap, _ := e.Do(b, proto.Request{Type: proto.Key, Key: "a"}); snap.Mode != proto.ModeEnglish {
		t.Fatalf("共用的模式沒跟上：%s", snap.Mode)
	}
}
