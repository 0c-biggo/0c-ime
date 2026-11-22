package session

import (
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/vocab"
)

// 英文建議的反悔鍵（#328）：英文模式的補字候選、混打的 Tab 提示，以及混打 ↓ 候選裡的英文詞，按 Shift＋Delete
// （macOS 也可以 Shift＋⌫）跟中文候選的 forgetCandidate 一樣處理：是自己加的詞就從詞庫刪除，是學到的就忘記
// （Model.Forget：清掉紀錄並 tombstone，之後被動打字不會學回來，再次明確選原按鍵才重新累積）。內建的詞沒有反應，
// 按鍵照原本的 Delete／Backspace。用來刪掉以前學到的密碼這類不想再看到的字。

func shiftDeleteKey(k string) bool { return k == proto.KeyShiftDelete || k == proto.KeyShiftBackspace }

// englishForgettable 是現在按反悔鍵會作用的英文建議：反白的英文候選或混打的 Tab 提示，而且是自己加的詞或學到的詞。
// 其他情況回空字串，按鍵照原本的 Delete／Backspace 處理：英文補字候選和 Tab 提示是打字時自己跳出來的，
// 按著 Shift 打大寫順手按到 ⌫ 不能變成提示「內建的詞」而不刪字。
func (s *Session) englishForgettable() string {
	if s.private || s.paused {
		return ""
	}
	word := ""
	if c := s.cand; c != nil {
		if c.kind != "" || !c.english && !c.mixed || c.cursor < 0 || c.cursor >= len(c.items) {
			return ""
		}
		word = c.items[c.cursor].Entry.Surface // 混打的 ↓ 也列注音與中文字，下面只留英文詞
	} else if !s.english && s.InputStyle.ShiftDelete != "tone" {
		word = s.mixedSuggestion() // 混打的 Tab 提示（沒開候選窗）
	}
	if word == "" || !personal.EnglishToken(word) || !s.model.VocabEnglish(word) && !s.model.EnglishLearned(word) {
		return ""
	}
	return word
}

// forgetEnglish 對反白的英文建議 word（englishForgettable 的結果）按反悔鍵。
func (s *Session) forgetEnglish(word string) {
	if s.model.VocabEnglish(word) {
		if _, err := s.model.VocabRemove([]string{word}, vocab.SourceCompose); err != nil {
			s.notices = append(s.notices, err.Error())
			return
		}
		s.vocabChanged = true
		s.notices = append(s.notices, "已從詞庫刪除："+word+"（可在設定程式復原）")
	} else {
		s.model.Forget(word)
		s.lastLearn = nil // 同 forgetCandidate：之前的復原複本不能讓忘掉的詞回來
		s.forgot = true
		s.notices = append(s.notices, "不再推薦："+word)
	}
	// 換掉畫面上已經不該出現的建議：英文補字重新查，混打的 ↓ 原地重開，Tab 提示在快照時重新算。
	switch {
	case s.cand != nil && s.cand.english:
		s.cand = nil
		s.englishDismissed = false
	case s.cand != nil && s.cand.mixed:
		cursor := s.cand.cursor
		s.cand = nil
		s.openMixedCandidates()
		if s.cand != nil {
			s.cand.cursor = min(cursor, len(s.cand.items)-1)
		}
	}
}

// englishShiftDelete 處理英文建議上的反悔鍵；done 為 false 時照一般規則處理這個鍵。
func (s *Session) englishShiftDelete(k string) (done bool) {
	if !s.Preview02 || !shiftDeleteKey(k) {
		return false
	}
	word := s.englishForgettable()
	if word == "" {
		return false
	}
	s.forgetEnglish(word)
	return true
}
