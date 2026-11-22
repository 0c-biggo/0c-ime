package session

import (
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// learnInSentence 讓 word 在句子裡打過四次（兩天），達到建議門檻；英文模式按空白接著打，混打接中文。
func learnInSentence(t *testing.T, s *Session, word string) {
	t.Helper()
	typed := word + " ji3"
	if s.english {
		typed = word + " "
	}
	for day := 100; day < 102; day++ {
		s.model.AdvanceTime(int64(day) * englishDay)
		for range 2 {
			mixedType(s, typed)
			key(s, proto.KeyEnter)
			key(s, proto.KeyEnter)
		}
	}
	if !s.model.EnglishKnown(word) {
		t.Fatalf("測試前提：%q 要學到：%+v", word, s.model.Store.English)
	}
}

func hasNotice(snap proto.Snapshot, text string) bool {
	return slices.ContainsFunc(snap.Notices, func(n string) bool { return strings.Contains(n, text) })
}

// #328：已經學到的密碼這類字，在出現的地方按 Shift＋Delete（macOS 也可以 Shift＋⌫）忘記，並 tombstone：
// 之後照樣打（連在句子裡）也不會學回來、不再建議。混打的 Tab 提示（沒開候選窗）與英文模式的補字候選都可以。
func TestShiftDeleteForgetsLearnedEnglish(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start func() *Session
		k     string
	}{
		{"混打 Tab 提示 Shift＋Delete", func() *Session { s := mixedSession(); s.Preview02 = true; return s }, proto.KeyShiftDelete},
		{"混打 Tab 提示 Shift＋⌫", func() *Session { s := mixedSession(); s.Preview02 = true; return s }, proto.KeyShiftBackspace},
		{"英文模式候選 Shift＋Delete", func() *Session { s := autocompleteSession(); s.Preview02 = true; return s }, proto.KeyShiftDelete},
		{"英文模式候選 Shift＋⌫", func() *Session { s := autocompleteSession(); s.Preview02 = true; return s }, proto.KeyShiftBackspace},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const pw = "zuinfake2026"
			s := tc.start()
			word := pw
			if s.english {
				word = "zuinfake" // 英文模式的補字只在字母上運作
			}
			learnInSentence(t, s, word)
			shown := func(snap proto.Snapshot) bool {
				return snap.SuggestionEdit != nil && snap.SuggestionEdit.Text == word || slices.Contains(snapCandidates(snap), word)
			}
			if snap := mixedType(s, "zuin"); !shown(snap) {
				t.Fatalf("測試前提：打前綴要建議 %q：%+v", word, snap)
			}
			snap := key(s, tc.k)
			if !snap.Handled || !snap.Forgot || !hasNotice(snap, "不再推薦："+word) || shown(snap) {
				t.Fatalf("反悔鍵沒忘記：%+v", snap)
			}
			if snap.Text() != "zuin" {
				t.Fatalf("忘記不刪字：%q", snap.Text())
			}
			if s.model.EnglishLearned(word) || s.model.EnglishKnown(word) {
				t.Fatalf("紀錄還在：%+v", s.model.Store.English)
			}
			s.Handle(proto.Request{Type: proto.Cancel})
			// tombstone：之後照樣在句子裡打也不學回來。
			for day := 102; day < 106; day++ {
				s.model.AdvanceTime(int64(day) * englishDay)
				for range 2 {
					typed := word + " ji3"
					if s.english {
						typed = word + " "
					}
					mixedType(s, typed)
					key(s, proto.KeyEnter)
					key(s, proto.KeyEnter)
				}
			}
			if s.model.EnglishLearned(word) {
				t.Fatalf("忘記後被動打字學回來：%+v", s.model.Store.English)
			}
			if snap := mixedType(s, "zuin"); shown(snap) {
				t.Fatalf("忘記後又建議：%+v", snap)
			}
		})
	}
}

// 混打按 ↓ 開的候選裡，反白學到的英文詞按 Shift＋Delete 也忘記，候選原地重開。
func TestShiftDeleteForgetsLearnedEnglishInMixedCandidates(t *testing.T) {
	s := mixedSession()
	s.Preview02 = true
	learnInSentence(t, s, "zuinfake2026")
	mixedType(s, "zuin")
	snap := key(s, proto.KeyDown)
	at := slices.Index(snapCandidates(snap), "zuinfake2026")
	if at < 0 {
		t.Fatalf("測試前提：↓ 要列出學到的詞：%q", snapCandidates(snap))
	}
	s.cand.cursor = at
	snap = key(s, proto.KeyShiftDelete)
	if !snap.Forgot || slices.Contains(snapCandidates(snap), "zuinfake2026") || snap.Candidates == nil {
		t.Fatalf("↓ 候選沒忘記或沒重開：%+v", snap)
	}
}

// 內建的詞、沒學到的字、注音與中文候選不受影響：英文補字候選與 Tab 提示是打字時自己出現的，
// 按著 Shift 順手按 ⌫ 照舊刪一個字。
func TestShiftDeleteLeavesBuiltinEnglishAlone(t *testing.T) {
	s := autocompleteSession()
	s.Preview02 = true
	if snap := mixedType(s, "hel"); snapCandidates(snap) == nil {
		t.Fatalf("測試前提：hel 要有字典候選：%+v", snap)
	}
	if snap := key(s, proto.KeyShiftBackspace); snap.Forgot || snap.Text() != "he" || len(s.model.Store.Tombstones) != 0 {
		t.Fatalf("英文模式字典候選上的 Shift＋⌫ 要照舊刪字：%+v", snap)
	}
	m := mixedSession()
	m.Preview02 = true
	m.English = s.English
	snap := mixedType(m, "hel")
	if snap.SuggestionEdit == nil {
		t.Fatalf("測試前提：混打 hel 要有 Tab 提示：%+v", snap)
	}
	if snap := key(m, proto.KeyShiftBackspace); snap.Forgot || snap.Text() != "he" || len(m.model.Store.Tombstones) != 0 {
		t.Fatalf("混打字典提示上的 Shift＋⌫ 要照舊刪字：%+v", snap)
	}
}

// 私人欄位不顯示個人英文，也不在那裡忘記。
func TestShiftDeleteEnglishNotInPrivateField(t *testing.T) {
	s := mixedSession()
	s.Preview02 = true
	learnInSentence(t, s, "zuinfake2026")
	s.Handle(proto.Request{Type: proto.Key, Key: "z", Private: true})
	mixedType(s, "uin")
	if snap := s.Handle(proto.Request{Type: proto.Key, Key: proto.KeyShiftDelete, Private: true}); snap.Forgot {
		t.Fatalf("私人欄位忘記了：%+v", snap)
	}
	if !s.model.EnglishKnown("zuinfake2026") {
		t.Fatal("私人欄位的按鍵動到個人英文")
	}
}

// 「我的詞」的英文詞在補字候選上按 Shift＋Delete 從詞庫刪除（可在設定程式復原），同中文候選。
func TestShiftDeleteRemovesVocabEnglish(t *testing.T) {
	s := vocabAutocompleteSession(t, "pCloud")
	snap := mixedType(s, "pc")
	if got := snapCandidates(snap); len(got) == 0 || got[0] != "pCloud" {
		t.Fatalf("測試前提：pc 要先列 pCloud：%q", got)
	}
	snap = key(s, proto.KeyShiftDelete)
	if !snap.VocabChanged || !hasNotice(snap, "已從詞庫刪除：pCloud") || slices.Contains(snapCandidates(snap), "pCloud") {
		t.Fatalf("自訂英文詞沒刪：%+v", snap)
	}
	if s.model.VocabEnglish("pCloud") {
		t.Fatal("詞庫還有 pCloud")
	}
}
