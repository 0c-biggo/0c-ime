package session

import (
	"slices"
	"testing"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// tonelessNeural：聲調選填的測試詞庫加一個偏好「回憶」的評分器。統計首選是「會議」（6e-4 對 3e-4，
// 引擎分數差 ln 2），融合權重 1 時評分器換成「回憶」，0.1 時不換。
func tonelessNeural(opt rerank.Options, seen *[]rerank.Request) *Session {
	s := newTonelessSession(true)
	s.Opt.PrefixAware = false
	opt.Scorer = rankFunc(func(r rerank.Request) ([]float64, error) {
		*seen = append(*seen, r)
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			if c.Text == "回憶" {
				scores[i] = 1
			}
		}
		return scores, nil
	})
	if opt.Weight == 0 {
		opt.Weight = 1
	}
	s.Rerank = opt
	return s
}

func commitBare(s *Session, text string) string {
	typeBare(s, text)
	return key(s, proto.KeyEnter).Commit
}

func TestUntonedSegmentStaysStatisticalWithoutUnknownTones(t *testing.T) {
	var seen []rerank.Request
	s := tonelessNeural(rerank.Options{}, &seen)
	if got := commitBare(s, "ㄏㄨㄟㄧ"); got != "會議" || len(seen) != 0 {
		t.Fatalf("v1 評分器不該收到沒打聲調的段落：%q %d", got, len(seen))
	}
}

func TestUntonedSegmentIsScoredWithToneMask(t *testing.T) {
	var seen []rerank.Request
	s := tonelessNeural(rerank.Options{UnknownTones: true}, &seen)
	if got := commitBare(s, "ㄏㄨㄟㄧ"); got != "回憶" || len(seen) == 0 {
		t.Fatalf("未知聲調應送評分：%q %d", got, len(seen))
	}
	r := seen[len(seen)-1]
	if !slices.Equal(r.Reading, zhuyin.ParseReading("ㄏㄨㄟ ㄧ")) || !slices.Equal(r.ToneKnown, []bool{false, false}) {
		t.Fatalf("讀音送基底、tone_known 全 false：%q %v", r.Reading, r.ToneKnown)
	}
}

func TestMixedTonesMarkOnlyUntonedSyllables(t *testing.T) {
	var seen []rerank.Request
	s := tonelessNeural(rerank.Options{UnknownTones: true}, &seen)
	typeBare(s, "ㄏㄨㄟˊㄧ")
	key(s, proto.KeyEnter)
	if len(seen) == 0 {
		t.Fatal("沒有評分")
	}
	r := seen[len(seen)-1]
	if !slices.Equal(r.Reading, zhuyin.ParseReading("ㄏㄨㄟˊ ㄧ")) || !slices.Equal(r.ToneKnown, []bool{true, false}) {
		t.Fatalf("只有沒打聲調的音節標 false：%q %v", r.Reading, r.ToneKnown)
	}
}

func TestTonedSegmentKeepsOldRequestWithUnknownTones(t *testing.T) {
	var seen []rerank.Request
	s := tonelessNeural(rerank.Options{UnknownTones: true}, &seen)
	typeSyl(s, "ㄏㄨㄟˋ")
	typeSyl(s, "ㄧˋ")
	if len(seen) == 0 || seen[len(seen)-1].ToneKnown != nil {
		t.Fatalf("全打聲調不帶 tone_known：%+v", seen)
	}
}

func TestReadingOutsideScorerBasesIsNotSent(t *testing.T) {
	var seen []rerank.Request
	bases := map[zhuyin.Syllable]bool{"ㄧ": true} // 少了 ㄏㄨㄟ
	s := tonelessNeural(rerank.Options{UnknownTones: true, Bases: bases}, &seen)
	if got := commitBare(s, "ㄏㄨㄟㄧ"); got != "會議" || len(seen) != 0 {
		t.Fatalf("字表外的基底不送評分：%q %d", got, len(seen))
	}
}

func TestInitialOnlyAbbreviationsAreNotSent(t *testing.T) {
	// ㄨ 開頭＋只打聲母 ㄇ（我們）、ㄋㄧ＋只打聲母 ㄏ（你好）：只打聲母的音節沒有讀音，整段在本機照統計引擎。
	for _, text := range []string{"ㄨㄇ", "ㄋㄧㄏ"} {
		var seen []rerank.Request
		s := tonelessNeural(rerank.Options{UnknownTones: true}, &seen)
		s.Abbreviation = true
		typeBare(s, text)
		if snap := key(s, proto.KeyEnter); snap.Commit == "" || len(seen) != 0 {
			t.Fatalf("%s：只打聲母的段落不送評分：%q %+v", text, snap.Commit, seen)
		}
	}
}

func TestFullSyllableAbbreviationIsScoredAsUntoned(t *testing.T) {
	// 開了簡拼，沒打聲調的 ㄧ 在 session 裡是「ㄧ 開頭」的簡拼音節；它本身是完整音節，照沒打聲調的 ㄧ 送評分。
	var seen []rerank.Request
	s := tonelessNeural(rerank.Options{UnknownTones: true}, &seen)
	s.Abbreviation = true
	if got := commitBare(s, "ㄏㄨㄟㄧ"); got != "回憶" || len(seen) == 0 {
		t.Fatalf("ㄏㄨㄟ＋ㄧ：%q %d", got, len(seen))
	}
	r := seen[len(seen)-1]
	if !slices.Equal(r.Reading, zhuyin.ParseReading("ㄏㄨㄟ ㄧ")) || !slices.Equal(r.ToneKnown, []bool{false, false}) {
		t.Fatalf("%q %v", r.Reading, r.ToneKnown)
	}
}

func TestContextlessWeightAppliesWithoutContext(t *testing.T) {
	for _, c := range []struct {
		weight, contextless float64
		want                string
	}{
		{.1, 0, "會議"}, // 沒設：一律用 Weight
		{.1, 1, "回憶"}, // 前文是空的：用 ContextlessWeight
		{1, .1, "會議"},
	} {
		var seen []rerank.Request
		s := tonelessNeural(rerank.Options{UnknownTones: true, Weight: c.weight, ContextlessWeight: c.contextless}, &seen)
		if got := commitBare(s, "ㄏㄨㄟㄧ"); got != c.want {
			t.Fatalf("%+v：%q", c, got)
		}
	}
}

// 簡拼位置的讀音不准評分器改：開了簡拼，沒打聲調的 ㄧ 是「ㄧ 開頭」；統計首選「會議」在那裡讀 ㄧ，
// 評分器偏好讀 ㄧㄠ 的「會要」也不能換過去（它在候選裡，是被排除，不是沒送）。
// 都讀 ㄧ 的候選之間照樣可以換（TestFullSyllableAbbreviationIsScoredAsUntoned）。
func TestAbbreviationKeepsStatisticalReading(t *testing.T) {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"會", "ㄏㄨㄟˋ", 6e-3}, {"回", "ㄏㄨㄟˊ", 3e-3}, {"議", "ㄧˋ", 5e-4}, {"要", "ㄧㄠˋ", 5e-3},
		{"會議", "ㄏㄨㄟˋ ㄧˋ", 6e-4}, {"會要", "ㄏㄨㄟˋ ㄧㄠˋ", 2e-4},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	var seen []rerank.Request
	s := New(personal.NewModel(lex, sys))
	s.Toneless, s.Abbreviation, s.Opt.PrefixAware = true, true, false
	s.Rerank = rerank.Options{UnknownTones: true, Weight: 100, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		seen = append(seen, r)
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			if c.Text == "會要" {
				scores[i] = 1
			}
		}
		return scores, nil
	})}
	got := commitBare(s, "ㄏㄨㄟㄧ")
	sent := false
	for _, r := range seen {
		for _, c := range r.Candidates {
			sent = sent || c.Text == "會要"
		}
	}
	if got != "會議" || !sent {
		t.Fatalf("簡拼位置不能從 ㄧ 換成 ㄧㄠ：%q（會要在候選裡：%v）", got, sent)
	}
}

// 簡拼位置的讀音檢查要在詞庫裡找每個候選的讀音（簡拼一列有上百個假設），重排快取命中時不再算：
// 外殼在模型算完後呼叫 Rescore，同一段沒變時沿用快取的決定。
func TestAbbreviationVetoIsCachedWithTheRerank(t *testing.T) {
	calls := 0
	saved := vetoAbbrev
	vetoAbbrev = func(s *Session, lat input.Lattice, start, end int, rows []decode.Hypothesis) ([]bool, bool) {
		calls++
		return saved(s, lat, start, end, rows)
	}
	defer func() { vetoAbbrev = saved }()
	var seen []rerank.Request
	s := tonelessNeural(rerank.Options{UnknownTones: true}, &seen)
	s.Abbreviation = true
	typeBare(s, "ㄏㄨㄟㄧ")
	key(s, proto.KeyRight) // 完成沒打聲調的 ㄧ（簡拼音節），重排一次
	if len(seen) == 0 || calls == 0 {
		t.Fatalf("沒有評分或沒有檢查簡拼讀音：%d %d", len(seen), calls)
	}
	before, scored := calls, len(seen)
	if !s.Rescore() {
		t.Fatal("沒有重排")
	}
	if len(seen) != scored || calls != before {
		t.Fatalf("同一段沒變，快取命中時又評分 %d 次、檢查簡拼讀音 %d 次", len(seen)-scored, calls-before)
	}
	// 段落變了（多打一個音節）就要重新檢查。
	typeBare(s, "ㄏㄨㄟ")
	key(s, proto.KeyRight)
	if len(seen) == scored || calls == before {
		t.Fatalf("段落變了卻沒有重新評分（%d）或檢查簡拼讀音（%d）", len(seen)-scored, calls-before)
	}
}
