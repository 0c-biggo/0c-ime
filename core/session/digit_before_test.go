package session

import (
	"encoding/json"
	"testing"

	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func keyOf(r rune) string { return string([]rune(layout.Standard.Keys(zhuyin.Syllable(string(r))))[0]) }

func snapJSON(s proto.Snapshot) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// digitNeural：tonelessNeural 加上外殼的前文確認與 DigitContext。沒前文時權重 .1（統計首選「會議」不換），
// 帶「前一個字是數字」時照有前文的 Weight 1（評分器換成「回憶」），看得出旗標有沒有送到。
func digitNeural(seen *[]rerank.Request) *Session {
	s := tonelessNeural(rerank.Options{UnknownTones: true, Weight: 1, ContextlessWeight: .1, ContextRunes: rerank.MaxContextRunes,
		DigitContext: true}, seen)
	s.Handle(proto.Request{Type: proto.Context, ContextEnabled: true})
	return s
}

func toggle(s *Session) { s.Handle(proto.Request{Type: proto.Toggle}) }

// typeDigitThenZhuyin：英數模式打 digits（交還應用程式），切回注音打 zhuyinText。
func typeDigitThenZhuyin(t *testing.T, s *Session, digits string) {
	t.Helper()
	toggle(s)
	for _, d := range digits {
		if key(s, string(d)).Handled {
			t.Fatalf("英數模式沒在組字時的 %q 應交還應用程式", d)
		}
	}
	toggle(s)
}

func lastRequest(t *testing.T, seen []rerank.Request) rerank.Request {
	t.Helper()
	if len(seen) == 0 {
		t.Fatal("沒有送評分")
	}
	return seen[len(seen)-1]
}

// 英數模式打「3」、切回注音打「ㄏㄨㄟㄧ」：第一段帶 DigitBefore、不帶數字本身，權重照有前文。
func TestDigitTypedInEnglishModeReachesTheScorer(t *testing.T) {
	var seen []rerank.Request
	s := digitNeural(&seen)
	typeDigitThenZhuyin(t, s, "3")
	if got := commitBare(s, "ㄏㄨㄟㄧ"); got != "回憶" {
		t.Fatalf("帶數字旗標時應照有前文的權重換成回憶：%q", got)
	}
	r := lastRequest(t, seen)
	if !r.DigitBefore || r.Context != "" {
		t.Fatalf("請求 %+v", r)
	}
	// 送出之後游標前是剛打的中文，不再帶（剛學過回憶，只看請求）。
	seen = nil
	commitBare(s, "ㄏㄨㄟㄧ")
	if lastRequest(t, seen).DigitBefore {
		t.Fatal("送出後還帶旗標")
	}
}

// 數字鍵盤在注音模式沒在組字時也交還應用程式，同樣記下（#75）；主鍵盤的數字在注音模式是注音鍵。
func TestKeypadDigitInZhuyinModeCounts(t *testing.T) {
	var seen []rerank.Request
	s := digitNeural(&seen)
	if key(s, proto.KeypadPrefix+"7").Handled {
		t.Fatal("沒在組字時數字鍵盤應交還應用程式")
	}
	if got := commitBare(s, "ㄏㄨㄟㄧ"); got != "回憶" || !lastRequest(t, seen).DigitBefore {
		t.Fatalf("%q %+v", got, seen)
	}
}

// 數字之後又交還了別的鍵（字母、空白、方向鍵、Backspace 刪掉數字）：游標前不是數字，不帶。
func TestOtherPassedKeysClearTheDigit(t *testing.T) {
	for _, k := range []string{"a", " ", proto.KeyLeft, proto.KeyBackspace, proto.KeyEnter, proto.KeyEscape, proto.KeypadPrefix + "+"} {
		var seen []rerank.Request
		s := digitNeural(&seen)
		toggle(s)
		key(s, "3")
		if key(s, k).Handled {
			t.Fatalf("%q 應交還應用程式", k)
		}
		toggle(s)
		if got := commitBare(s, "ㄏㄨㄟㄧ"); got != "會議" || lastRequest(t, seen).DigitBefore {
			t.Fatalf("數字後再按 %q 還帶旗標：%q", k, got)
		}
	}
}

// Esc 清掉整段組字（游標回到數字後面也一樣當作新的開始：外殼的重設、失焦都是 cancel／commit）、
// 外殼的 cancel 與前文清除、私人欄位、暫停都清掉。
func TestDigitIsForgottenOnResetPrivateAndPause(t *testing.T) {
	for name, clear := range map[string]func(*Session){
		"esc": func(s *Session) {
			typeBare(s, "ㄏㄨㄟ")
			key(s, proto.KeyEscape)
			key(s, proto.KeyEscape)
		},
		"cancel": func(s *Session) { s.Handle(proto.Request{Type: proto.Cancel}) },
		"commit": func(s *Session) { s.Handle(proto.Request{Type: proto.Commit}) },
		"pause":  func(s *Session) { s.Handle(proto.Request{Type: proto.Pause, On: true}) },
	} {
		var seen []rerank.Request
		s := digitNeural(&seen)
		typeDigitThenZhuyin(t, s, "3")
		clear(s)
		if name == "pause" {
			s.Handle(proto.Request{Type: proto.Pause})
		}
		if got := commitBare(s, "ㄏㄨㄟㄧ"); got != "會議" || len(seen) > 0 && lastRequest(t, seen).DigitBefore {
			t.Fatalf("%s 之後還帶旗標：%q", name, got)
		}
	}
}

// 前文確認的清除（context on=false）不清：Fcitx5 外殼每次切換中英前都送一次（.13 真機 trace 看到的順序）。
func TestDigitSurvivesContextClear(t *testing.T) {
	var seen []rerank.Request
	s := digitNeural(&seen)
	s.Handle(proto.Request{Type: proto.Context})
	toggle(s)
	s.Handle(proto.Request{Type: proto.Context})
	key(s, "3")
	s.Handle(proto.Request{Type: proto.Context})
	toggle(s)
	if got := commitBare(s, "ㄏㄨㄟㄧ"); got != "回憶" || !lastRequest(t, seen).DigitBefore {
		t.Fatalf("%q", got)
	}
}

// 私人欄位、暫停、前文設定關閉、外殼沒有前文確認時不帶；英數模式下照常記，切回注音才用。
func TestDigitIsNotSentInProtectedStates(t *testing.T) {
	cases := map[string]func(*Session){
		"private": func(s *Session) {
			toggle(s)
			s.Handle(proto.Request{Type: proto.Key, Key: "3", Private: true})
			toggle(s)
		},
		"context-off": func(s *Session) {
			s.DocumentContextOff = true
			typeDigitThenZhuyin(t, s, "3")
		},
		"no-shell-context": func(s *Session) {
			s.documentEnabled = false
			typeDigitThenZhuyin(t, s, "3")
		},
	}
	for name, setup := range cases {
		var seen []rerank.Request
		s := digitNeural(&seen)
		setup(s)
		commitBare(s, "ㄏㄨㄟㄧ")
		if len(seen) > 0 && lastRequest(t, seen).DigitBefore {
			t.Fatalf("%s 帶了旗標", name)
		}
	}
	// 暫停中打的數字：恢復後也不帶（暫停清掉）。
	var seen []rerank.Request
	s := digitNeural(&seen)
	s.Handle(proto.Request{Type: proto.Pause, On: true})
	typeDigitThenZhuyin(t, s, "3")
	s.Handle(proto.Request{Type: proto.Pause})
	commitBare(s, "ㄏㄨㄟㄧ")
	if len(seen) > 0 && lastRequest(t, seen).DigitBefore {
		t.Fatal("暫停中打的數字，恢復後帶了旗標")
	}
}

// 評分器沒宣告 DigitContext（手機、沒有前文的套件）：請求與以前相同。
func TestDigitNeedsScorerDeclaration(t *testing.T) {
	var seen []rerank.Request
	s := tonelessNeural(rerank.Options{UnknownTones: true, Weight: 1, ContextlessWeight: .1, ContextRunes: rerank.MaxContextRunes}, &seen)
	s.Handle(proto.Request{Type: proto.Context, ContextEnabled: true})
	typeDigitThenZhuyin(t, s, "3")
	if got := commitBare(s, "ㄏㄨㄟㄧ"); got != "會議" || lastRequest(t, seen).DigitBefore {
		t.Fatalf("%q %+v", got, seen)
	}
}

// 只用在組字的第一段：標點後面那一段的前文是前一段的字，不帶旗標。
func TestDigitOnlyForTheFirstSegment(t *testing.T) {
	var seen []rerank.Request
	s := digitNeural(&seen)
	typeDigitThenZhuyin(t, s, "3")
	typeBare(s, "ㄏㄨㄟㄧ")
	key(s, "!") // 大千的 , 是注音
	seen = nil
	commitBare(s, "ㄏㄨㄟㄧ")
	later := 0
	for _, r := range seen {
		if r.Context != "" {
			later++
			if r.DigitBefore {
				t.Fatalf("後一段帶了旗標：%+v", r)
			}
		}
	}
	if later == 0 {
		t.Fatalf("後一段沒有送評分：%+v", seen)
	}
}

// Windows TSF 讀游標前一個字（DigitBefore 請求欄位）：讀到數字就記，讀到不是數字就清；組字中送來的不看。
func TestShellReportedDigitBefore(t *testing.T) {
	yes, no := true, false
	first := []rune("ㄏㄨㄟㄧ")
	for _, c := range []struct {
		name    string
		prepare func(*Session)
		reports []*bool // 依序跟著前幾個鍵送的 digit_before
		want    bool
	}{
		{"讀到數字", nil, []*bool{&yes}, true},
		{"讀到不是數字蓋掉核心記的", func(s *Session) { key(s, proto.KeypadPrefix+"7") }, []*bool{&no}, false},
		{"沒讀照核心記的", func(s *Session) { key(s, proto.KeypadPrefix+"7") }, []*bool{nil}, true},
		{"組字中送來的不看", nil, []*bool{nil, &yes}, false},
	} {
		var seen []rerank.Request
		s := digitNeural(&seen)
		if c.prepare != nil {
			c.prepare(s)
		}
		for i, r := range c.reports {
			s.Handle(proto.Request{Type: proto.Key, Key: keyOf(first[i]), DigitBefore: r})
		}
		commitBare(s, string(first[len(c.reports):]))
		if got := lastRequest(t, seen).DigitBefore; got != c.want {
			t.Fatalf("%s：DigitBefore %v", c.name, got)
		}
	}
}

// 旗標不進快照（外殼的 trace 錄快照與請求）。
func TestDigitIsNotInTheSnapshot(t *testing.T) {
	var seen []rerank.Request
	a, b := digitNeural(&seen), digitNeural(&seen)
	typeDigitThenZhuyin(t, a, "3")
	toggle(b)
	key(b, "a")
	toggle(b)
	sa, sb := typeBare(a, "ㄏㄨ"), typeBare(b, "ㄏㄨ")
	sa.Revision, sb.Revision = 0, 0
	if snapJSON(sa) != snapJSON(sb) {
		t.Fatalf("快照不同：\n%s\n%s", snapJSON(sa), snapJSON(sb))
	}
}
