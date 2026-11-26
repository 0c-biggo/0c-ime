package session

import "github.com/0c-biggo/0c-ime/core/suggest"

// Completion 回傳目前組字的補全建議：跟桌面按 Tab 接受的是同一個，信心與效益門檻
// 相同，但不看候選視窗開著與否、游標在哪。手機的候選列在打字途中把它列成一個候選，四字詞打到一半
// 就點得到。未打完的注音不算，要先完成：手機是在 PreviewPending 那樣的複本上開候選，那時已經完成。只回傳往後補的
// 建議，不回傳同音替換（那要清楚預覽才能接受）。
func (s *Session) Completion() *suggest.Suggestion {
	if s.english || s.mixed.raw != "" || !s.comp.Empty() || s.keysPending() || len(s.syls) == 0 {
		return nil
	}
	s.refresh()
	if len(s.path) == 0 || s.autoFuzzed() {
		return nil
	}
	saved := s.previewRewrite
	s.previewRewrite = false
	g := s.suggestAfterLiteral()
	s.previewRewrite = saved
	if g == nil || g.Replace != "" {
		return nil
	}
	return g
}

// acceptCompletion 接受手機候選列上的補全（proto.Complete）：未打完的注音先不打聲調就完成，再算一次補全，
// 是同一個詞才接受，免得接受到畫面上沒有的建議。接受之後跟 Tab 一樣是一段手選的補全詞（Lock.Completion），
// 送出時照補全學習。
func (s *Session) acceptCompletion(word string) bool {
	if word == "" || s.english || s.mixed.raw != "" {
		return false
	}
	if (!s.comp.Empty() || s.keysPending()) && !s.finishUntoned() {
		return false
	}
	s.cand = nil
	g := s.Completion()
	if g == nil || g.Word != word {
		return false
	}
	s.sug = g
	return s.accept()
}
