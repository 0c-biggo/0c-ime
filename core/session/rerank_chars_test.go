package session

import (
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// charsSession 打 ㄌㄧㄢˇ ㄕㄤˋ，候選裡有罕用字「𠄞」（models 校準組 826bbcbf：候選「臉𠄞」的 𠄞 不在選字模型字表）。
// 評分器記下每次收到的候選文字；chars 非 nil 時當成評分器的字表。
func charsSession(chars map[rune]bool, sent *[][]string) *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{{"臉", "ㄌㄧㄢˇ", 1e-3}, {"斂", "ㄌㄧㄢˇ", 1e-4}, {"上", "ㄕㄤˋ", 1e-3}, {"尚", "ㄕㄤˋ", 3e-4}, {"𠄞", "ㄕㄤˋ", 2e-4}} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.Opt.PrefixAware = false
	s.Rerank = rerank.Options{Weight: .1, Chars: chars, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		var texts []string
		for _, c := range r.Candidates {
			texts = append(texts, c.Text)
		}
		*sent = append(*sent, texts)
		return make([]float64, len(r.Candidates)), nil
	})}
	typeSyl(s, "ㄌㄧㄢˇ")
	typeSyl(s, "ㄕㄤˋ")
	return s
}

// 候選裡有評分器字表外的字時，只拿掉含那個字的候選，其餘照常評分（評分器不會整批拒絕 unknown_character）；
// 送出的候選與手動拿掉那些候選相同。顯示中的路徑本身有字表外的字時，整段照統計引擎，不送評分。
func TestRerankDropsCandidatesWithUnknownCharacters(t *testing.T) {
	var all [][]string
	charsSession(nil, &all)
	if len(all) == 0 {
		t.Fatal("沒有字表時應送評分")
	}
	full := all[len(all)-1]
	var want []string
	for _, text := range full {
		if !strings.ContainsRune(text, '𠄞') {
			want = append(want, text)
		}
	}
	if full[0] != "臉上" || len(want) < 2 || len(want) == len(full) {
		t.Fatalf("測試資料不適用：%q", full)
	}

	chars := map[rune]bool{'臉': true, '斂': true, '上': true, '尚': true}
	var sent [][]string
	if got := charsSession(chars, &sent).View().Text(); got != "臉上" {
		t.Fatalf("畫面 %q", got)
	}
	if len(sent) == 0 || !slices.Equal(sent[len(sent)-1], want) {
		t.Fatalf("送出 %q，應為 %q", sent, want)
	}

	// 首選含字表外的字：不送評分。
	chars['臉'] = false
	sent = nil
	if got := charsSession(chars, &sent).View().Text(); got != "臉上" {
		t.Fatalf("畫面 %q", got)
	}
	for _, texts := range sent {
		if len(texts) > 0 && texts[0] == "臉上" {
			t.Fatalf("首選含字表外的字時不該送評分：%q", sent)
		}
	}
}
