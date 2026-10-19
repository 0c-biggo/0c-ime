package engine

import (
	"testing"

	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/zhuyin"
)

// #143：系統詞庫的長成語。機率取自資料包（data-142）：成語與「不散的筵席」沒有語料詞頻，在平滑後的最低值；
// 一般詞有系統 bigram。
const (
	idiom          = "天下沒有不散的筵席"
	idiomReading   = "ㄊㄧㄢ ㄒㄧㄚˋ ㄇㄟˊ ㄧㄡˇ ㄅㄨˋ ㄙㄢˋ ㄉㄜ˙ ㄧㄢˊ ㄒㄧˊ"
	idiomTyped     = "ㄊㄧㄢ ㄒㄧㄚˋ ㄇㄟˊ ㄧㄡˇ ㄅㄨˋ"
	idiomRest      = "散的筵席"
	idiomTail      = "不散的筵席"
	idiomOther     = "沒有不透風的牆"
	idiomOtherRead = "ㄇㄟˊ ㄧㄡˇ ㄅㄨˋ ㄊㄡˋ ㄈㄥ ㄉㄜ˙ ㄑㄧㄤˊ"
)

func idiomEngine() *Engine {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	add := func(w, r string, p float64) {
		lex.Add(lexicon.Entry{Surface: w, Reading: zhuyin.ParseReading(r), PRW: 1})
		sys.SetUni(w, p)
	}
	syls := zhuyin.ParseReading(idiomReading)
	for i, r := range []rune(idiom) {
		add(string(r), string(syls[i]), 1e-4)
	}
	other := zhuyin.ParseReading(idiomOtherRead)
	for i, r := range []rune(idiomOther) {
		add(string(r), string(other[i]), 1e-4)
	}
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"天下", "ㄊㄧㄢ ㄒㄧㄚˋ", 3.4e-5}, {"沒有", "ㄇㄟˊ ㄧㄡˇ", 7e-4}, {"不", "ㄅㄨˋ", 1e-3}, {"天下沒有", "ㄊㄧㄢ ㄒㄧㄚˋ ㄇㄟˊ ㄧㄡˇ", 1.4e-7},
		{"不散", "ㄅㄨˋ ㄙㄢˋ", 3e-7}, {"筵席", "ㄧㄢˊ ㄒㄧˊ", 1.4e-7}, {"透風", "ㄊㄡˋ ㄈㄥ", 1e-6}, {"的", "ㄉㄜ˙", 1.7e-2},
		{"不同", "ㄅㄨˋ ㄊㄨㄥˊ", 3e-4}, {"不會", "ㄅㄨˋ ㄏㄨㄟˋ", 5e-4}, {"不是", "ㄅㄨˋ ㄕˋ", 1e-3},
		{idiom, idiomReading, 1.6e-8}, {idiomTail, "ㄅㄨˋ ㄙㄢˋ ㄉㄜ˙ ㄧㄢˊ ㄒㄧˊ", 1.6e-8}, {idiomOther, idiomOtherRead, 1.6e-8},
	} {
		add(e.w, e.r, e.p)
	}
	sys.SetBi("天下", "沒有", 1.3e-3)
	sys.SetBi("沒有", "不", 1.7e-3)
	sys.SetBi("沒有", idiomTail, 1.7e-6)
	return New(lex, sys)
}

// 在「沒有」之後手選「不散的筵席」，學到「沒有｜不散的筵席」的切法（維護者桌機上 Corrections 的那筆）。
func learnIdiomSplit(t *testing.T, e *Engine) {
	t.Helper()
	e.Model.Learn(personal.Commit{Syls: zhuyin.ParseReading(idiomReading), Path: []personal.Seg{
		{Word: "天下", Start: 0, End: 2}, {Word: "沒有", Start: 2, End: 4}, {Word: idiomTail, Start: 4, End: 9, Manual: true},
	}})
	if len(e.Model.Store.Corrections) != 1 {
		t.Fatalf("應該學到一筆修正：%d", len(e.Model.Store.Corrections))
	}
}

// 打「天下沒有不」要提示「散的筵席」，按 Tab 收下。學到「沒有｜不散的筵席」之後解碼成「天下｜沒有｜不」，
// 最後一段只剩「不」，以前就不再提示（#143）。
func TestCrossSegmentIdiomCompletion(t *testing.T) {
	for _, learned := range []bool{false, true} {
		e := idiomEngine()
		if learned {
			learnIdiomSplit(t, e)
		}
		id := e.OpenSession()
		snap := typeReading(e, id, idiomTyped)
		// 重現條件：沒學過時尾端整段是成語的開頭；學過切法後切成「天下｜沒有｜不」，否則測不到跨分段。
		if split := len(snap.Preedit) > 1; split != learned {
			t.Fatalf("學過切法=%v：組字 %+v", learned, snap.Preedit)
		}
		if snap.Suggestion != idiomRest || snap.SuggestedText() != idiom {
			t.Errorf("學過切法=%v：%q 的提示 %q（%q）", learned, snap.Text(), snap.Suggestion, snap.SuggestedText())
			continue
		}
		snap, _ = e.Do(id, proto.Request{Type: proto.Key, Key: proto.KeyTab})
		if snap.Text() != idiom {
			t.Errorf("學過切法=%v：Tab 之後 %q", learned, snap.Text())
			continue
		}
		if snap, _ = e.Do(id, proto.Request{Type: proto.Key, Key: proto.KeyEnter}); snap.Commit != idiom {
			t.Errorf("學過切法=%v：提交 %q", learned, snap.Commit)
		}
	}
}

// 同一個原因的反例：「沒有不」之後多半是「不同」「不會」，不提示「沒有不透風的牆」；學過切法也一樣。
func TestCrossSegmentNoEagerIdiom(t *testing.T) {
	for _, learned := range []bool{false, true} {
		e := idiomEngine()
		if learned {
			learnIdiomSplit(t, e)
		}
		if snap := typeReading(e, e.OpenSession(), "ㄇㄟˊ ㄧㄡˇ ㄅㄨˋ"); snap.Suggestion != "" {
			t.Errorf("學過切法=%v：沒有不 不該提示：%q", learned, snap.SuggestedText())
		}
	}
}
