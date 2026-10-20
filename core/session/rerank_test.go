package session

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

type rankFunc func(rerank.Request) ([]float64, error)

func (f rankFunc) Score(r rerank.Request) ([]float64, error) { return f(r) }
func neuralSession(calls *int) *Session {
	s := newSession()
	s.Opt.PrefixAware = false
	s.Rerank = rerank.Options{Weight: .1, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		*calls++
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			if strings.Contains(c.Text, "井") {
				scores[i] = 1000
			}
		}
		// 不讓外部評分器持有的 mutable slice 成為核心路徑。
		r.Reading[0] = "壞"
		r.Candidates[0].Segments[0].Word = "壞"
		return scores, nil
	})}
	return s
}
func TestNeuralDisplayNavigationAndCommit(t *testing.T) {
	calls := 0
	s := neuralSession(&calls)
	typeSyl(s, "ㄨㄛˇ")
	snap := typeSyl(s, "ㄐㄧㄥˇ")
	if snap.Text() != "我井" || calls != 1 {
		t.Fatalf("rerank %q calls=%d", snap.Text(), calls)
	}
	s.model.Gen++ // 別的欄位的學習不能讓游標移動或候選導覽悄悄換字。
	for _, r := range []proto.Request{{Type: proto.Cursor, To: 0}, {Type: proto.Cursor, To: 2}, {Type: proto.Candidates, Length: 1}, {Type: proto.Page, Index: 0}, {Type: proto.Close}} {
		got := s.Handle(r)
		if got.Text() != snap.Text() || calls != 1 {
			t.Fatalf("navigation reranked: %+v calls=%d", got, calls)
		}
	}
	s.model.Gen++ // 別的欄位可能更新個人模型；Enter 仍提交已顯示文字。
	if got := s.Handle(proto.Request{Type: proto.Commit}); got.Commit != snap.Text() || calls != 1 {
		t.Fatalf("commit changed: %+v calls=%d", got, calls)
	}
	if got := s.Handle(proto.Request{Type: proto.Commit}); got.Commit != "" {
		t.Fatal("duplicate commit")
	}
}
func TestNeuralPendingLockCancelAndPrivacy(t *testing.T) {
	calls := 0
	s := neuralSession(&calls)
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄐㄧㄥˇ")
	if got := key(s, "v"); got.Text() != "我井ㄒ" || calls != 1 {
		t.Fatalf("pending %+v", got)
	}
	if got := key(s, proto.KeyBackspace); got.Text() != "我井" || calls != 1 {
		t.Fatalf("pending delete %+v", got)
	}
	got := s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
	choice := -1
	for _, c := range got.Candidates.Items {
		if c.Text == "景" {
			choice = c.ID
		}
	}
	if choice < 0 {
		t.Fatal("missing manual choice")
	}
	got = s.Handle(proto.Request{Type: proto.Select, Index: choice})
	if got.Text() != "我景" || calls != 1 {
		t.Fatalf("manual overwritten %+v", got)
	}
	got = typeSyl(s, "ㄨㄛˇ")
	if got.Text() != "我景我" || calls != 1 {
		t.Fatalf("lock overwritten %+v", got)
	}
	s.Handle(proto.Request{Type: proto.Cancel})
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄐㄧㄥˇ")
	if calls != 2 {
		t.Fatalf("new composition calls %d", calls)
	}
	s.Handle(proto.Request{Type: proto.Cancel})
	s.Handle(proto.Request{Type: proto.Pause, On: true})
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄐㄧㄥˇ")
	if calls != 2 {
		t.Fatal("paused inference")
	}
	s.Handle(proto.Request{Type: proto.Cancel})
	s.Handle(proto.Request{Type: proto.Pause, On: false})
	s.Handle(proto.Request{Type: proto.Symbol, Symbol: "ㄨㄛˇ", Private: true})
	s.Handle(proto.Request{Type: proto.Symbol, Symbol: "ㄐㄧㄥˇ", Private: true})
	if calls != 2 {
		t.Fatal("private inference")
	}
}
func TestNeuralFailuresKeepEngineAndLiterals(t *testing.T) {
	for _, kind := range []string{"error", "nan", "count"} {
		t.Run(kind, func(t *testing.T) {
			s := newSession()
			s.Opt.PrefixAware = false
			s.Rerank = rerank.Options{Weight: .1, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
				switch kind {
				case "error":
					return nil, errors.New("timeout")
				case "nan":
					return []float64{math.NaN(), 1}, nil
				default:
					return []float64{1000}, nil
				}
			})}
			typeSyl(s, "ㄨㄛˇ")
			got := typeSyl(s, "ㄐㄧㄥˇ")
			if got.Text() != "我景" {
				t.Fatalf("fallback %q", got.Text())
			}
		})
	}
	calls := 0
	s := neuralSession(&calls)
	typeSyl(s, "ㄨㄛˇ")
	key(s, "@")
	got := typeSyl(s, "ㄐㄧㄥˇ")
	if got.Text() != "我@景" || calls != 0 {
		t.Fatalf("mixed text inference %+v calls=%d", got, calls)
	}
}

func contextNeuralSession(got *[]string, runes int) *Session {
	s := newSession()
	s.Opt.PrefixAware = false
	s.Handle(proto.Request{Type: proto.Context, ContextEnabled: true})
	s.Rerank = rerank.Options{Weight: .1, ContextRunes: runes, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		*got = append(*got, r.Context)
		return make([]float64, len(r.Candidates)), nil
	})}
	return s
}

func TestNeuralContextRequiresConfirmationAndStaysPrivate(t *testing.T) {
	var got []string
	s := contextNeuralSession(&got, rerank.MaxContextRunes)
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄐㄧㄥˇ")
	first := key(s, proto.KeyEnter)
	if first.Commit == "" || first.ContextID == 0 {
		t.Fatalf("missing commit/context token: %+v", first)
	}
	typeSyl(s, "ㄨㄛˇ") // 外殼尚未確認提交，已開始下一段組字：不得當作前文
	typeSyl(s, "ㄐㄧㄥˇ")
	if got[len(got)-1] != "" {
		t.Fatalf("unconfirmed commit leaked as context: %q", got[len(got)-1])
	}
	got = nil
	s = contextNeuralSession(&got, rerank.MaxContextRunes)
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄐㄧㄥˇ")
	c := key(s, proto.KeyEnter)
	s.Handle(proto.Request{Type: proto.Context, On: true, ContextID: c.ContextID})
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄐㄧㄥˇ")
	if got[len(got)-1] != c.Commit {
		t.Fatalf("confirmed context = %q, want %q", got[len(got)-1], c.Commit)
	}
	s.Handle(proto.Request{Type: proto.Cancel})
	s.Handle(proto.Request{Type: proto.Context, Private: true})
	if s.neuralContext() != "" {
		t.Fatal("private request kept neural context")
	}
}

func TestNeuralContextTruncatesAndNeedsDeclaration(t *testing.T) {
	var got []string
	s := contextNeuralSession(&got, rerank.MaxContextRunes)
	s.neuralLeft = strings.Repeat("甲", 70)
	if n := len([]rune(s.neuralContext())); n != rerank.MaxContextRunes {
		t.Fatalf("context runes = %d", n)
	}
	if next := s.neuralCommitContext("乙丙"); !strings.HasSuffix(next, "乙丙") || len([]rune(next)) != rerank.MaxContextRunes {
		t.Fatalf("commit context = %q", next)
	}
	plain := contextNeuralSession(&got, 0)
	plain.neuralLeft = "甲乙"
	typeSyl(plain, "ㄨㄛˇ")
	typeSyl(plain, "ㄐㄧㄥˇ")
	if got[len(got)-1] != "" || plain.neuralCommitContext("丙") != "" {
		t.Fatal("scorer without context declaration received context")
	}
}

func TestNeuralRanksSegmentsAroundLiteralsWithCache(t *testing.T) {
	var contexts []string
	calls := 0
	s := newSession()
	s.Opt.PrefixAware = false
	s.Rerank = rerank.Options{Weight: .1, ContextRunes: rerank.MaxContextRunes, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		calls++
		contexts = append(contexts, r.Context)
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			if strings.Contains(c.Text, "井") {
				scores[i] = 1000
			}
		}
		return scores, nil
	})}
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄐㄧㄥˇ")
	key(s, "<")
	typeSyl(s, "ㄨㄛˇ")
	snap := typeSyl(s, "ㄐㄧㄥˇ")
	if got := snap.Text(); got != "我井，我井" {
		t.Fatalf("segmented rerank = %q", got)
	}
	if last := contexts[len(contexts)-1]; last != "我井，" {
		t.Fatalf("segment context = %q, want shown text before the segment", last)
	}
	before := calls
	snap = key(s, "<") // 新增的字面符號只帶來單音節空段，兩個既有段落都應走快取
	if calls != before || snap.Text() != "我井，我井，" {
		t.Fatalf("cached segments rescored: calls %d→%d text=%q", before, calls, snap.Text())
	}
}

func hintSession(gap float64) *Session {
	s := newSession()
	s.Opt.PrefixAware = false
	s.Opt.Suggest.MinConf = 2 // 關閉詞補全，單獨觀察神經提示
	s.Rerank = rerank.Options{Weight: 1, HintGap: gap, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		return make([]float64, len(r.Candidates)), nil // 全 0：融合分數等於引擎分數
	})}
	return s
}

func TestNeuralHintShowsCloseSecondAndTabLocksIt(t *testing.T) {
	s := hintSession(100)
	typeSyl(s, "ㄨㄛˇ")
	snap := typeSyl(s, "ㄐㄧㄥˇ")
	if snap.SuggestionEdit == nil || !strings.Contains(snap.Suggestion, " → ") {
		t.Fatalf("missing neural hint: %+v", snap)
	}
	want, shown := snap.SuggestionEdit.Text, snap.Text()
	if want == shown {
		t.Fatalf("hint repeats the shown text %q", shown)
	}
	after := key(s, proto.KeyTab)
	if after.Text() != want || len(s.locks) == 0 {
		t.Fatalf("Tab did not accept hint: got %q want %q locks=%v", after.Text(), want, s.locks)
	}
	if after.Suggestion != "" {
		t.Fatalf("hint remained after acceptance: %q", after.Suggestion)
	}
	if got := s.Handle(proto.Request{Type: proto.Commit}); got.Commit != want {
		t.Fatalf("commit %q want %q", got.Commit, want)
	}
}

func TestNeuralHintDisabledAtZeroGap(t *testing.T) {
	s := hintSession(0)
	typeSyl(s, "ㄨㄛˇ")
	if snap := typeSyl(s, "ㄐㄧㄥˇ"); snap.Suggestion != "" || s.hint != nil {
		t.Fatalf("hint shown with gap 0: %+v", snap)
	}
}

func TestNeuralTextModeRanksSingleSyllableFromContext(t *testing.T) {
	var modes []string
	s := newSession()
	s.Opt.PrefixAware = false
	s.Handle(proto.Request{Type: proto.Context, ContextEnabled: true})
	s.Rerank = rerank.Options{Weight: 1, TextWeight: 2, TextMargin: 1, ContextRunes: rerank.MaxContextRunes, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		modes = append(modes, r.Mode)
		if r.Mode == rerank.ModeText && (len(r.Reading) != 1 || r.Context == "") {
			t.Fatalf("text mode request without single syllable/context: %+v", r)
		}
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			if c.Text == "井" {
				scores[i] = 100
			}
		}
		return scores, nil
	})}
	if got := typeSyl(s, "ㄐㄧㄥˇ").Text(); got != "景" || len(modes) != 0 {
		t.Fatalf("no confirmed context must not rank single syllable: %q modes=%v", got, modes)
	}
	s.Handle(proto.Request{Type: proto.Cancel})
	s.neuralLeft = "好"
	if got := typeSyl(s, "ㄐㄧㄥˇ").Text(); got != "井" || modes[len(modes)-1] != rerank.ModeText {
		t.Fatalf("text-mode particle = %q modes=%v", got, modes)
	}
	s.Handle(proto.Request{Type: proto.Cancel})
	s.Rerank.TextWeight = 0
	before := len(modes)
	if got := typeSyl(s, "ㄐㄧㄥˇ").Text(); got != "景" || len(modes) != before {
		t.Fatalf("TextWeight 0 must disable single-syllable ranking: %q", got)
	}
}

// 句尾助詞打錯聲調時，「嘛」只在輕聲讀音的候選中；宿主併入輕聲 N-best（扣 NeutralExpand）。
func TestNeuralNeutralExpansionAddsParticleCandidates(t *testing.T) {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{{"好", "ㄏㄠˇ", 1e-2}, {"馬", "ㄇㄚˇ", 1e-3}, {"碼", "ㄇㄚˇ", 5e-4}, {"嗎", "ㄇㄚ˙", 1e-3}, {"嘛", "ㄇㄚ˙", 5e-4}} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	var seen [][]string
	var scores map[string]float64
	s := New(personal.NewModel(lex, sys))
	s.Opt.PrefixAware = false
	s.Rerank = rerank.Options{Weight: 2, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		var texts []string
		out := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			texts = append(texts, c.Text)
			scores[c.Text] = c.LogScore
			if c.Text == "好嘛" {
				out[i] = 100
			}
		}
		seen = append(seen, texts)
		return out, nil
	}), NeutralBases: map[zhuyin.Syllable]bool{"ㄇㄚ": true}}
	run := func(expand float64, last string) (string, []string) {
		s.Handle(proto.Request{Type: proto.Cancel})
		s.rerankCache = nil
		seen, scores = nil, map[string]float64{}
		s.Rerank.NeutralExpand = expand
		typeSyl(s, "ㄏㄠˇ")
		got := typeSyl(s, last).Text()
		if len(seen) == 0 {
			return got, nil
		}
		return got, seen[len(seen)-1]
	}
	if got, texts := run(0, "ㄇㄚˇ"); got != "好馬" || strings.Contains(strings.Join(texts, ","), "嘛") {
		t.Fatalf("expansion off must keep typed candidates: %q %v", got, texts)
	}
	got, texts := run(4, "ㄇㄚˇ")
	if got != "好嘛" || texts[0] != "好馬" {
		t.Fatalf("expanded particle = %q candidates=%v", got, texts)
	}
	typed := map[string]bool{"好馬": true, "好碼": true}
	for _, text := range texts {
		if !typed[text] && !strings.HasSuffix(text, "嗎") && !strings.HasSuffix(text, "嘛") {
			t.Fatalf("unexpected candidate %q in %v", text, texts)
		}
	}
	if scores["好嘛"] >= scores["好碼"] {
		t.Fatalf("neutral candidates must carry the penalty: %v", scores)
	}
	if _, texts := run(4, "ㄇㄚ˙"); strings.Contains(strings.Join(texts, ","), "馬") {
		t.Fatalf("typed neutral must not expand: %v", texts)
	}
	s.Rerank.NeutralBases = map[zhuyin.Syllable]bool{"ㄅㄚ": true}
	if got, texts := run(4, "ㄇㄚˇ"); got != "好馬" || strings.Contains(strings.Join(texts, ","), "嘛") {
		t.Fatalf("unlisted base must not expand: %q %v", got, texts)
	}
}

// #20：每個音節兩個同音字，統計偏好常用字、模型偏好罕用字。第 4 音節時 16 種組合只取 8-best，
// 全罕用的正確句不在其中；前段已改對的字不得被換回。
func TestNeuralKeepsShownChoicesWhenNBestOverflows(t *testing.T) {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	syls := []string{"ㄅㄚ", "ㄉㄚ", "ㄇㄚ", "ㄈㄚ", "ㄊㄚ"}
	pairs := [][2]string{{"八", "巴"}, {"搭", "耷"}, {"媽", "抹"}, {"發", "罰"}, {"他", "塌"}}
	good := map[rune]bool{}
	for i, p := range pairs {
		lex.Add(lexicon.Entry{Surface: p[0], Reading: zhuyin.ParseReading(syls[i]), PRW: 1})
		lex.Add(lexicon.Entry{Surface: p[1], Reading: zhuyin.ParseReading(syls[i]), PRW: 1})
		sys.SetUni(p[0], 1e-2)
		sys.SetUni(p[1], 1e-3)
		good[[]rune(p[1])[0]] = true
	}
	s := New(personal.NewModel(lex, sys))
	s.Opt.PrefixAware = false
	s.Opt.Suggest.MinConf = 2
	counts := []int{}
	s.Rerank = rerank.Options{Weight: .1, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		counts = append(counts, len(r.Candidates))
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			for _, ch := range c.Text {
				if good[ch] {
					scores[i] += 1000
				}
			}
		}
		return scores, nil
	})}
	want := ""
	for i, y := range syls {
		want += pairs[i][1]
		if got := typeSyl(s, y).Text(); i > 0 && got != want { // 單音節段沒有 TextWeight 不評分
			t.Fatalf("第 %d 音節顯示 %q，want %q（候選數 %v）", i+1, got, want, counts)
		}
	}
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != want {
		t.Fatalf("commit %q want %q", got, want)
	}
}
