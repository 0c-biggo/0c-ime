package engine

import (
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/vocab"
)

// 「靈犀學到的」頁（#182）：移除（不留 tombstone）、不再學（留 tombstone）、多筆、復原，以及「不再學的詞」的列出與恢復學習。
func TestVocabUnlearnForgetUndoAndForgotten(t *testing.T) {
	e := vocabEngine()
	st := e.Model.Store
	for _, ph := range []string{"世結", "世介"} {
		st.Phrases[ph] = "ㄕˋ ㄐㄧㄝˊ"
		st.PhraseOrder = append(st.PhraseOrder, ph)
		st.PhraseSeen[ph] = 2
	}
	st.Readings["世界\tㄕˋ ㄐㄧㄝˋ"] = true
	texts := func(ls []Learned) (out []string) {
		for _, l := range ls {
			out = append(out, l.Text)
		}
		return out
	}
	r := e.Vocab(VocabRequest{Op: VocabLearned})
	if got := texts(r.Learned); strings.Join(got, ",") != "世結,世介,世界" || r.ForgottenCount != 0 {
		t.Fatalf("%v %+v", got, r)
	}
	if r := e.Vocab(VocabRequest{Op: VocabUnlearn}); r.OK || r.Error.Code != vocab.CodeEmpty {
		t.Fatalf("沒有詞：%+v", r)
	}

	// 移除兩個：清單少了、沒有 tombstone、可以復原。
	r = e.Vocab(VocabRequest{Op: VocabUnlearn, Texts: []string{"世結", "世界"}})
	if !r.OK || !r.Changed || r.Undo == 0 || len(st.Tombstones) != 0 || r.ForgottenCount != 0 {
		t.Fatalf("%+v %v", r, st.Tombstones)
	}
	if got := texts(e.Vocab(VocabRequest{Op: VocabLearned}).Learned); strings.Join(got, ",") != "世介" {
		t.Fatalf("%v", got)
	}
	if r := e.Vocab(VocabRequest{Op: VocabLearnedUndo, Undo: r.Undo}); !r.OK || !r.Changed {
		t.Fatalf("%+v", r)
	}
	if got := texts(e.Vocab(VocabRequest{Op: VocabLearned}).Learned); strings.Join(got, ",") != "世結,世介,世界" {
		t.Fatalf("復原後順序照舊：%v", got)
	}

	// 不再學：留 tombstone，「不再學的詞」看得到，恢復學習也能復原。
	r = e.Vocab(VocabRequest{Op: VocabForget, Texts: []string{"世結", "世界"}})
	if !r.OK || r.Undo == 0 || r.ForgottenCount != 2 || !st.Tombstones["世結"] || !st.Tombstones["世界"] {
		t.Fatalf("%+v", r)
	}
	f := e.Vocab(VocabRequest{Op: VocabForgotten})
	if !f.OK || strings.Join(f.Forgotten, ",") != "世界,世結" || f.ForgottenCount != 2 {
		t.Fatalf("%+v", f)
	}
	forgetUndo := r.Undo
	r = e.Vocab(VocabRequest{Op: VocabRelearn, Texts: []string{"世界", "不在清單"}})
	if !r.OK || r.Undo == 0 || r.ForgottenCount != 1 || st.Tombstones["世界"] {
		t.Fatalf("%+v", r)
	}
	if r := e.Vocab(VocabRequest{Op: VocabLearnedUndo, Undo: r.Undo}); !r.OK || r.ForgottenCount != 2 {
		t.Fatalf("復原恢復學習：%+v", r)
	}
	if r := e.Vocab(VocabRequest{Op: VocabLearnedUndo, Undo: forgetUndo}); !r.OK || r.ForgottenCount != 0 {
		t.Fatalf("復原不再學：%+v", r)
	}
	if got := texts(e.Vocab(VocabRequest{Op: VocabLearned}).Learned); strings.Join(got, ",") != "世結,世介,世界" {
		t.Fatalf("%v", got)
	}
	if r := e.Vocab(VocabRequest{Op: VocabLearnedUndo, Undo: forgetUndo}); r.OK || r.Error.Code != vocab.CodeNotFound {
		t.Fatalf("同一次只能復原一次：%+v", r)
	}
	if r := e.Vocab(VocabRequest{Op: VocabRelearn, Texts: []string{"世結"}}); !r.OK || r.Changed || r.Undo != 0 {
		t.Fatalf("沒有可以恢復的：%+v", r)
	}
	for _, op := range []string{VocabUnlearn, VocabForget, VocabRelearn, VocabLearnedUndo} {
		if !LearningOp(op) {
			t.Fatal(op)
		}
	}
	if LearningOp(VocabRemove) || LearningOp(VocabKeep) {
		t.Fatal("自訂詞的修改存的是 vocab.json")
	}
}

// 復原用的複本只留最近幾次，舊的拿不回來。
func TestVocabLearnedUndoLimit(t *testing.T) {
	e := vocabEngine()
	var first uint64
	for i := range maxLearnedUndo + 1 {
		e.Model.Store.Readings["世界\tㄕˋ ㄐㄧㄝˋ"] = true
		r := e.Vocab(VocabRequest{Op: VocabUnlearn, Texts: []string{"世界"}})
		if i == 0 {
			first = r.Undo
		}
	}
	if r := e.Vocab(VocabRequest{Op: VocabLearnedUndo, Undo: first}); r.OK || r.Error.Code != vocab.CodeNotFound {
		t.Fatalf("%+v", r)
	}
}
