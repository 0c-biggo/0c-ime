package session

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 聯想詞（觸控）：沒在組字時，依外殼確認過的末詞（documentLeft）列出最可能接著打的詞。
// 只用核心自己送出、外殼確認落在游標前的詞，不讀文件；私人、暫停、英文或沒有確認的前文都不列。
// 點選即送出，送出的詞成為下一次的待確認前文，可以一路接下去。點了的詞對（前詞→聯想詞）照一般送出學習，
// 常接的詞越用越前面；這是唯一跨提交學的詞對，私人與暫停時不學。
//
// 沒有確認的前文時改列開頭詞（Kind 為 "start"）：先列使用者自己常當開頭送出的詞（個人 bigram 中前詞為句首的詞，
// 也就是每次送出的第一個詞），不足再補口語開頭詞。系統語言模型的句首機率來自新聞語料（年、月、日），不用。
// 點了照句首學習，越用越前面。

// maxPredictions 是一次列出的聯想詞數；手機候選列一行放得下。
const maxPredictions = 8

// predictionPool 是系統後詞先依系統機率留下的數量；個人詞對另外全部加入，再一起依 Model.Prob 排序。
// 常用前詞（的、我）有上萬個後詞，全部算個人混合太慢，個人學到的詞又不一定在系統前段。
const predictionPool = 64

// startWords 是個人資料還不夠時補上的口語開頭詞，依序列出。
var startWords = []string{"我", "你", "好", "謝謝", "對", "那", "這", "可以"}

// startMinUses 是個人開頭詞要列出前，當開頭送出的最少次數（以一般送出的權重計）；只用過一次的詞不搶預設清單的位置。
const startMinUses = 2

// startMaxLen 是個人開頭詞的最長字數；晉升的長片語不放進開頭詞。
const startMaxLen = 4

// predictionKindStart 標示沒有前文時列出的開頭詞。外殼據此知道退格不必先收起它們。
const predictionKindStart = "start"

type prediction struct {
	word string
	p    float64
}

// predict 列出聯想詞與它們接在哪個前詞之後；沒有前文時前詞是句首，列的是開頭詞。
func (s *Session) predict() ([]string, string) {
	if s.composing() || s.private || s.paused || s.english {
		return nil, ""
	}
	left := s.documentLeft
	if left == "" || !s.documentEnabled {
		return s.startPredictions(), lm.BOS
	}
	return s.followPredictions(left), left
}

// startPredictions 列出開頭詞：個人常用的開頭詞依次數排序，再補預設清單，共 maxPredictions 個。
func (s *Session) startPredictions() []string {
	var personal []prediction
	prefix := lm.BOS + "\t"
	for k, c := range s.model.Store.Bi {
		w, ok := strings.CutPrefix(k, prefix)
		if ok && c >= startMinUses*s.model.P.WPassive && allHan(w) && utf8.RuneCountInString(w) <= startMaxLen && !s.model.Store.Tombstones[w] {
			personal = append(personal, prediction{w, c})
		}
	}
	slices.SortFunc(personal, byProbability)
	words := make([]string, 0, maxPredictions)
	for _, c := range personal[:min(maxPredictions, len(personal))] {
		words = append(words, c.word)
	}
	for _, w := range startWords {
		if len(words) == maxPredictions {
			break
		}
		if !slices.Contains(words, w) && !s.model.Store.Tombstones[w] {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return nil
	}
	return words
}

func (s *Session) followPredictions(left string) []string {
	succ, ok := s.model.LM.(lm.Successors)
	if !ok {
		return nil
	}
	forgot := s.model.Store.Tombstones // 「不再推薦」的詞不列（同 startPredictions）
	var system []prediction
	succ.Successors(left, func(w string, p float64) {
		if allHan(w) && !forgot[w] {
			system = append(system, prediction{w, p})
		}
	})
	slices.SortFunc(system, byProbability)
	pool := map[string]bool{}
	for _, c := range system[:min(predictionPool, len(system))] {
		pool[c.word] = true
	}
	prefix := left + "\t"
	for k := range s.model.Store.Bi {
		if w, ok := strings.CutPrefix(k, prefix); ok && allHan(w) && !forgot[w] {
			pool[w] = true
		}
	}
	ranked := make([]prediction, 0, len(pool))
	for w := range pool {
		ranked = append(ranked, prediction{w, s.model.Prob(left, w)})
	}
	slices.SortFunc(ranked, byProbability)
	words := make([]string, 0, maxPredictions)
	for _, c := range ranked[:min(maxPredictions, len(ranked))] {
		words = append(words, c.word)
	}
	if len(words) == 0 {
		return nil
	}
	return words
}

func byProbability(a, b prediction) int {
	switch {
	case a.p > b.p:
		return -1
	case a.p < b.p:
		return 1
	}
	return strings.Compare(a.word, b.word)
}

func allHan(w string) bool {
	if w == "" {
		return false
	}
	for _, r := range w {
		if !zhuyin.IsHan(r) {
			return false
		}
	}
	return true
}

// commitPrediction 送出點選的聯想詞並學習這個詞對（開頭詞的前詞是句首），同一般送出設定待確認的前文。
func (s *Session) commitPrediction(words []string, left string, id int) (string, bool) {
	if id < 0 || id >= len(words) || s.composing() {
		return "", false
	}
	w := words[id]
	s.lastLearn = nil
	if !s.paused && !s.private && left != "" {
		s.lastLearn = s.model.Undoable(func() { s.model.LearnFollow(left, w) })
	}
	neural := s.neuralCommitContext(w)
	s.reset()
	if utf8.RuneCountInString(w) <= decode.MaxWordLen {
		s.pendingLeft = w
		s.pendingContextID = uint64(s.rev + 1)
	}
	if neural != "" {
		s.pendingNeural = neural
		s.pendingContextID = uint64(s.rev + 1)
	}
	return s.output(w, w, nil), true
}

func predictionList(words []string, left string) *proto.CandidateList {
	out := &proto.CandidateList{Pages: 1}
	if left == lm.BOS {
		out.Kind = predictionKindStart
	}
	for i, w := range words {
		out.Items = append(out.Items, proto.CandidateItem{ID: i, Text: w})
	}
	return out
}
