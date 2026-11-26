package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/english"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 桌面改用詞頻字典（格式 2，#358）時的 session 行為。

func rankedDictionary(t *testing.T, entries ...english.Entry) *english.Dictionary {
	t.Helper()
	d, err := english.NewRanked(entries)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// 語料新加、三個字母以內的詞（tv）在混打不算英文：T恤（Tvm4）照舊是 T恤，不變成 Tv預；同樣的詞不是
// 語料新詞（在 ESDB、technical.tsv）時照認。英文模式的補字仍看得到它們。
func TestMixedIgnoresShortCorpusWords(t *testing.T) {
	run := func(corpusOnly bool) (*Session, string) {
		s := tokenBoundarySession()
		for _, e := range []struct {
			w, r string
			p    float64
		}{{"恤", "ㄒㄩˋ", 2e-5}, {"預", "ㄩˋ", 3e-4}} {
			s.model.Lex.(*lexicon.Mem).Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
			s.model.LM.(*lm.Mem).SetUni(e.w, e.p)
		}
		entries := []english.Entry{
			{Text: "gq", Display: "GQ", Weight: 70, CorpusOnly: corpusOnly}, {Text: "tv", Display: "TV", Weight: 75, CorpusOnly: corpusOnly},
			{Text: "tvbs", Display: "TVBS", Weight: 60, CorpusOnly: true},
		}
		for _, w := range []string{"hello", "air", "okay", "http", "the", "model", "paypal", "jolts", "mac"} {
			entries = append(entries, english.Entry{Text: w, Weight: 65})
		}
		s.English = rankedDictionary(t, entries...)
		mixedType(s, "Tvm4")
		return s, key(s, proto.KeyEnter).Commit
	}
	if s, got := run(true); got != "T恤" || s.mixedEnglishWord("tv") || len(s.englishPrefix("tv", 3)) != 1 {
		t.Fatalf("語料短詞：%q", got)
	}
	if s, got := run(false); got != "Tv預" || !s.mixedEnglishWord("tv") {
		t.Fatalf("審查過的詞照認：%q", got)
	}
	s, _ := run(true)
	s.EnglishAutocomplete = true
	s.Handle(proto.Request{Type: proto.Mode, On: true})
	if snap := mixedType(s, "tv"); snap.Candidates == nil || snap.Candidates.Items[0].Text != "TV" {
		t.Fatalf("英文模式用完整字典：%+v", snap.Candidates)
	}
}

// 打完整個字按 Tab 改成常見寫法（iphone → iPhone、monday → Monday）；已經是常見寫法、或沒有常見寫法的字
// 不給建議。混打與英文模式補字都一樣。
func TestTabGivesTheCommonSpellingOfAWholeWord(t *testing.T) {
	dict := func() *english.Dictionary {
		return rankedDictionary(t,
			english.Entry{Text: "iphone", Display: "iPhone", Weight: 86}, english.Entry{Text: "iphones", Weight: 60},
			english.Entry{Text: "monday", Display: "Monday", Weight: 70}, english.Entry{Text: "hello", Weight: 74},
			english.Entry{Text: "help", Weight: 80}, english.Entry{Text: "helpful", Weight: 60})
	}
	for _, tc := range []struct{ keys, want string }{
		{"iphone", "iPhone"}, {"monday", "Monday"}, {"Monday", "Monday"}, {"iph", "iPhone"}, {"help", "helpful"},
	} {
		s := mixedSession()
		s.English = dict()
		mixedType(s, tc.keys)
		if snap := key(s, proto.KeyTab); snap.Text() != tc.want {
			t.Errorf("混打 %s＋Tab：%q，要 %q", tc.keys, snap.Text(), tc.want)
		}
	}
	for _, keys := range []string{"iPhone", "hello"} {
		s := mixedSession()
		s.English = dict()
		mixedType(s, keys)
		if s.mixedSuggestion() != "" {
			t.Errorf("混打 %s 不該有建議：%q", keys, s.mixedSuggestion())
		}
	}
	for _, tc := range []struct{ keys, want string }{{"iphone", "iPhone"}, {"monday", "Monday"}} {
		s := autocompleteSession()
		s.English = dict()
		snap := mixedType(s, tc.keys)
		if snap.Candidates == nil || snap.Candidates.Items[0].Text != tc.want {
			t.Fatalf("英文模式 %s：%+v", tc.keys, snap.Candidates)
		}
		if snap := key(s, proto.KeyTab); snap.Text() != tc.want {
			t.Errorf("英文模式 %s＋Tab：%q", tc.keys, snap.Text())
		}
	}
	s := autocompleteSession()
	s.English = dict()
	if snap := mixedType(s, "iPhone"); snap.Candidates != nil {
		for _, it := range snap.Candidates.Items {
			if it.Text == "iPhone" {
				t.Fatalf("和打的一樣不是建議：%+v", snap.Candidates)
			}
		}
	}
}

// 學英文時「接近常用詞」的門檻：詞頻字典的分數刻度不同，39 約是舊字典 50 的同樣多詞（#358）。
func TestEnglishCommonPriorFollowsTheDictionaryScale(t *testing.T) {
	ranked := rankedDictionary(t, english.Entry{Text: "receive", Weight: 45})
	if englishCommonPrior(ranked) != 39 || englishCommonPrior(english.New([]english.Word{{Text: "receive", Weight: 45}})) != 50 {
		t.Fatal("threshold")
	}
	if !ranked.NearCommon("recieve", englishCommonPrior(ranked)) {
		t.Fatal("詞頻字典 45 分的詞算常用")
	}
}
