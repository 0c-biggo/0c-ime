package session

import (
	"strings"
	"testing"

	"git.biggo.com/0c/ime/core/fuzzy"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/zhuyin"
)

// 後續詞的上下文很偏向近音，也不應把已完整顯示的原音詞改成另一個讀音。
// 使用正常 unigram／bigram 機率，讓問題不依賴私人詞庫或神經模型。
func exactVideoSession() *Session {
	lex, sys := fuzzyLexicon(
		word{"影", "ㄧㄥˇ", .01}, word{"飲", "ㄧㄣˇ", .001},
		word{"片", "ㄆㄧㄢˋ", .01}, word{"影片", "ㄧㄥˇ ㄆㄧㄢˋ", .01},
		word{"飲片", "ㄧㄣˇ ㄆㄧㄢˋ", .001}, word{"都", "ㄉㄨ", 1e-8},
	)
	sys.SetBi("飲片", "都", 1)
	s := New(personal.NewModel(lex, sys))
	s.Opt.PrefixAware = false
	s.Fuzzy = autoConfig(fuzzy.EnEng)
	return s
}

func TestFuzzyAutoKeepsCompletedExactWordWhileTypingSuffix(t *testing.T) {
	s := exactVideoSession()
	var snap proto.Snapshot
	for _, k := range "u/3qu04" {
		snap = key(s, string(k))
	}
	if snap.Text() != "影片" || s.autoFuzzed() {
		t.Fatalf("前提：正確影／片須顯示原音影片：%+v", snap)
	}
	for _, k := range "2j " {
		snap = key(s, string(k))
		if !strings.HasPrefix(snap.Text(), "影片") {
			t.Fatalf("續打 %q 不得把先前影片換成異音：%+v；%v", k, snap, s.AutoChanges())
		}
	}
	if snap.Text() != "影片都" || key(s, proto.KeyEnter).Commit != "影片都" {
		t.Fatalf("續打及提交須保留影片：%+v", snap)
	}
}

func TestFuzzyAutoKeepsAnExactWordDisplayedAsSeparateCharacters(t *testing.T) {
	s := exactVideoSession()
	s.model.LM.(*lm.Mem).SetUni("影片", 1e-7)
	if snap := fzType(s, "ㄧㄥˇ ㄆㄧㄢˋ"); snap.Text() != "影片" || len(s.shownPath) != 2 {
		t.Fatalf("前提：影片以兩個單字解出：%+v；%v", snap, s.shownPath)
	}
	if snap := typeSyl(s, "ㄉㄨ"); snap.Text() != "影片都" {
		t.Fatalf("詞的保護不應取決於斷詞：%+v", snap)
	}
}

func TestFuzzyAutoKeepsAnExactWordAlsoShownAsALongerPrefix(t *testing.T) {
	s := exactVideoSession()
	s.Opt.PrefixAware = true
	s.model.Lex.(*lexicon.Mem).Add(lexicon.Entry{Surface: "影片大全", Reading: zhuyin.ParseReading("ㄧㄥˇ ㄆㄧㄢˋ ㄉㄚˋ ㄑㄩㄢˊ"), PRW: 1})
	s.model.LM.(*lm.Mem).SetUni("影片大全", .5)
	if snap := fzType(s, "ㄧㄥˇ ㄆㄧㄢˋ"); snap.Text() != "影片" || len(s.shownPath) != 1 || !s.shownPath[0].Partial {
		t.Fatalf("前提：長詞前綴顯示影片，影片也有完整原音詞條：%+v；%v", snap, s.shownPath)
	}
	if snap := typeSyl(s, "ㄉㄨ"); snap.Text() != "影片都" {
		t.Fatalf("已有完整原音詞條的顯示仍須保護：%+v", snap)
	}
}

func TestFuzzyAutoStillCorrectsAnIncompletePrefixWithoutExactWord(t *testing.T) {
	lex, sys := fuzzyLexicon(
		word{"影", "ㄧㄥˇ", .01}, word{"飲", "ㄧㄣˇ", .001},
		word{"片", "ㄆㄧㄢˋ", .01}, word{"影片大全", "ㄧㄥˇ ㄆㄧㄢˋ ㄉㄚˋ ㄑㄩㄢˊ", .5},
		word{"飲片", "ㄧㄣˇ ㄆㄧㄢˋ", .001}, word{"都", "ㄉㄨ", 1e-8},
	)
	sys.SetBi("飲片", "都", 1)
	s := New(personal.NewModel(lex, sys))
	s.Fuzzy = autoConfig(fuzzy.EnEng)
	if snap := fzType(s, "ㄧㄥˇ ㄆㄧㄢˋ"); snap.Text() != "影片" || !s.shownPath[0].Partial {
		t.Fatalf("前提：只有長詞前綴，沒有完整影片詞條：%+v；%v", snap, s.shownPath)
	}
	if snap := typeSyl(s, "ㄉㄨ"); snap.Text() != "飲片都" || !s.autoFuzzed() {
		t.Fatalf("尚未成詞的前綴仍依原有門檻修正：%+v；%v", snap, s.AutoChanges())
	}
}

func TestFuzzyAutoExactWordDoesNotBlockNearCandidateSelection(t *testing.T) {
	s := exactVideoSession()
	fzType(s, "ㄧㄥˇ ㄆㄧㄢˋ ㄉㄨ")
	fzSelect(t, s, 0, 2, "飲片")
	if got := key(s, proto.KeyEnter).Commit; got != "飲片都" {
		t.Fatalf("仍可明確手選近音：%q", got)
	}
	for reading := range s.model.Store.Readings {
		if strings.Contains(reading, "飲") {
			t.Fatalf("跨音手選仍不得學成原音：%q", reading)
		}
	}
}

func TestFuzzyAutoExactWordDoesNotLockItsCharacters(t *testing.T) {
	s := homophoneSession()
	s.Fuzzy = autoConfig(fuzzy.EnEng)
	if snap := typeXiaolong(s); snap.Text() != "小龍" {
		t.Fatal(snap)
	}
	if snap := typeSyl(s, "ㄅㄠ"); snap.Text() != "小籠包" {
		t.Fatalf("同音仍應組成更長的詞：%+v", snap)
	}
}

func TestFuzzyAutoKeepsExactWordAfterLiteral(t *testing.T) {
	s := exactVideoSession()
	fzType(s, "ㄨㄛˇ")
	fzLiteral(s, "1")
	if snap := fzType(s, "ㄧㄥˇ ㄆㄧㄢˋ ㄉㄨ"); snap.Text() != "我1影片都" {
		t.Fatalf("非零起點的分段也須保護原音詞：%+v", snap)
	}
}

func TestFuzzyAutoKeepsExactWordWithRequiredSuffixCorrection(t *testing.T) {
	s := exactVideoSession()
	s.Fuzzy = autoConfig(fuzzy.EnEng, fuzzy.SSh)
	s.model.Lex.(*lexicon.Mem).Add(lexicon.Entry{Surface: "送", Reading: zhuyin.ParseReading("ㄙㄨㄥˋ"), PRW: 1})
	s.model.LM.(*lm.Mem).SetUni("送", 1e-3)
	if snap := fzType(s, "ㄧㄥˇ ㄆㄧㄢˋ ㄕㄨㄥˋ ㄒㄧㄣˋ"); snap.Text() != "影片送信" {
		t.Fatalf("新音節必須換音時也須保留前面的原音：%+v；%v", snap, s.AutoChanges())
	}
}

func TestFuzzyAutoExactWordDoesNotBlockRetypingItsReading(t *testing.T) {
	s := exactVideoSession()
	fzType(s, "ㄧㄥˇ ㄆㄧㄢˋ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	key(s, proto.KeyDelete)
	for _, k := range "up3" {
		key(s, string(k))
	}
	s.Handle(proto.Request{Type: proto.Cursor, To: 2})
	if snap := typeSyl(s, "ㄉㄨ"); snap.Text() != "飲片都" {
		t.Fatalf("真的改打讀音後應沿新讀音解碼：%+v", snap)
	}
}

func TestFuzzyAutoExactWordDoesNotBlockNewSuffixCorrection(t *testing.T) {
	s := exactVideoSession()
	s.Fuzzy = autoConfig(fuzzy.EnEng, fuzzy.CCh)
	s.model.Lex.(*lexicon.Mem).Add(lexicon.Entry{Surface: "次", Reading: zhuyin.ParseReading("ㄘ"), PRW: 1})
	s.model.LM.(*lm.Mem).SetUni("次", 1e-6)
	if snap := fzType(s, "ㄧㄥˇ ㄆㄧㄢˋ ㄘ ㄈㄢˋ"); snap.Text() != "影片吃飯" {
		t.Fatalf("新尾端仍可自動修正，先前影片不換音：%+v；%v", snap, s.AutoChanges())
	}
	if changes := s.AutoChanges(); len(changes) != 1 || changes[0].Start != 2 || changes[0].Text != "吃飯" {
		t.Fatalf("只修正新輸入的尾端：%v", changes)
	}
}
