package session

import (
	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"unicode/utf8"
)

// 只保留自己最近提交的完整末詞；外殼確認文件更新後才可作為下一段的前詞。
// 不傳入、保存或學習任意 surrounding text，不從 session 存活推測文件連續性。
func (s *Session) clearDocumentContext() {
	s.documentLeft = ""
	s.pendingLeft = ""
	s.neuralLeft = ""
	s.pendingNeural = ""
	s.pendingContextID = 0
}
func (s *Session) contextLeft(start int) string {
	if start == 0 && !s.private && !s.paused && s.documentLeft != "" {
		return s.documentLeft
	}
	return lm.BOS
}
func (s *Session) confirmDocumentContext(r proto.Request) {
	if s.DocumentContextOff {
		s.documentEnabled = false
		s.clearDocumentContext()
		return
	}
	if r.ContextEnabled {
		s.documentEnabled = true
	}
	if !r.On {
		s.clearDocumentContext()
		return
	}
	if s.composing() || s.private || s.paused || s.english || r.ContextID == 0 || r.ContextID != s.pendingContextID {
		s.clearDocumentContext()
		return
	}
	s.documentLeft = s.pendingLeft
	s.neuralLeft = s.pendingNeural
	s.pendingLeft = ""
	s.pendingNeural = ""
	s.pendingContextID = 0
}

// neuralCommitContext 在 reset 前計算待確認的神經前文：沿用同一個確認握手，
// 只在評分器宣告需要前文、外殼支援確認且非私人／暫停／英數時保留。
func (s *Session) neuralCommitContext(text string) string {
	if s.Rerank.ContextRunes <= 0 || text == "" || !s.documentEnabled || s.private || s.paused || s.english {
		return ""
	}
	return lastRunes(s.neuralLeft+text, min(s.Rerank.ContextRunes, rerank.MaxContextRunes))
}

// neuralContext 是送給評分器的已確認前文；任何保護狀態都回空字串。
func (s *Session) neuralContext() string {
	if s.Rerank.ContextRunes <= 0 || !s.documentEnabled || s.private || s.paused || s.english {
		return ""
	}
	return lastRunes(s.neuralLeft, min(s.Rerank.ContextRunes, rerank.MaxContextRunes))
}

func lastRunes(text string, n int) string {
	r := []rune(text)
	if len(r) > n {
		r = r[len(r)-n:]
	}
	return string(r)
}
func (s *Session) committedTail() string {
	if !s.documentEnabled || s.private || s.paused || s.english || !s.comp.Empty() || len(s.path) == 0 {
		return ""
	}
	g := s.path[len(s.path)-1]
	if g.Partial || g.End != len(s.syls) || s.literals[g.Start] || utf8.RuneCountInString(g.Word) > decode.MaxWordLen {
		return ""
	}
	// 沒打聲調的音節換回詞庫讀音再比（同學習）；換不回來就不當前文。
	syls, _ := s.resolvedSyllables()
	for _, e := range s.model.Source().Exact(syls[g.Start:g.End]) {
		if e.Surface == g.Word {
			return g.Word
		}
	}
	return ""
}

// documentScorer 僅在第一個未被標點分隔的組字段使用。
// 在 BOS 混合原句首與已確認末詞；其餘詞對、先驗快取及個人檔維持原模型。
type documentScorer struct {
	*personal.Model
	left   string
	weight float64
}

func (m *documentScorer) mix(left, w string, f func(string, string) float64) float64 {
	if left != lm.BOS {
		return f(left, w)
	}
	return (1-m.weight)*f(lm.BOS, w) + m.weight*f(m.left, w)
}
func (m *documentScorer) Prob(left, w string) float64 { return m.mix(left, w, m.Model.Prob) }
func (m *documentScorer) SystemProb(left, w string) float64 {
	return m.mix(left, w, m.Model.SystemProb)
}
func (m *documentScorer) Evidence(left, w string) float64  { return m.mix(left, w, m.Model.Evidence) }
func (m *documentScorer) PriorMass(left, w string) float64 { return m.mix(left, w, m.Model.PriorMass) }

func (m *documentScorer) HasContext(left string) bool {
	if left == lm.BOS {
		return true
	}
	return m.Model.HasContext(left)
}
func (m *documentScorer) DocumentContext() (string, float64) { return m.left, m.weight }

// configureDocumentContext 設定從 start 開始的那一段組字（s.syls 目前的樣子）的前詞與評分器。
func (s *Session) configureDocumentContext(start int) { s.configureContext(start, true) }

// configureContext 是 configureDocumentContext；lone 為 false 時不套用「單獨一個不打聲調的音節」
// （untoned_lone.go）：呼叫端解的不是 s.syls 目前的那一段（漢語拼音還沒收的字母、觸控的合併弧在比較別的切法）。
func (s *Session) configureContext(start int, lone bool) {
	s.dec.Left = lm.BOS
	s.dec.Score = s.model
	if s.afterNumber(start) {
		s.dec.Score = &numberScorer{Model: s.model} // 數字後面的一段以數字為語境（只有 start > 0 會走到）
		return
	}
	if word := s.englishLeft(start); word != "" {
		s.dec.Left = word // 字面英文詞後面的一段以那個英文詞為前詞（english_context.go）
		return
	}
	// 無效設定安全回退；零完全等於沒有前文，不改變個人頻率保護的分母。
	if weight := s.Opt.DocumentContextWeight; weight > 0 && weight <= 1 {
		if left := s.contextLeft(start); left != lm.BOS {
			s.dec.Score = &documentScorer{Model: s.model, left: left, weight: weight}
			return
		}
	}
	// 沒有任何前文，這一段又只有一個不打聲調的音節：句首只照詞頻（#360）。游標前是數字時照舊（#337）。
	if lone && s.loneUntoned(start) && !(start == 0 && s.digitBefore()) {
		s.dec.Score = &loneScorer{Model: s.model}
	}
}
