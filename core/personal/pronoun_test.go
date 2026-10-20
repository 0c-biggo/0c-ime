package personal

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func pronounModel() *Model {
	lex := lexicon.NewMem()
	for _, e := range [][2]string{{"他們", "ㄊㄚ ㄇㄣ˙"}, {"她們", "ㄊㄚ ㄇㄣ˙"}, {"吉他", "ㄐㄧˊ ㄊㄚ"}, {"你好", "ㄋㄧˇ ㄏㄠˇ"}, {"妳好", "ㄋㄧˇ ㄏㄠˇ"}} {
		lex.Add(lexicon.Entry{Surface: e[0], Reading: zhuyin.ParseReading(e[1]), PRW: 1})
	}
	return NewModel(lex, lm.NewMem())
}

func TestPronounSites(t *testing.T) {
	m := pronounModel()
	path := []Seg{{Word: "吉他", Start: 0, End: 2}, {Word: "她們", Start: 2, End: 4}, {Word: "男朋友", Start: 4, End: 7}, {Word: "他", Start: 7, End: 8}}
	sites := m.PronounSites(path)
	if len(sites) != 2 || sites[0].Seg != 1 || !sites[0].Variant || sites[1].Seg != 3 || sites[1].Variant {
		t.Fatalf("「吉他」不是代名詞，「她們」「他」是：%+v", sites)
	}
	// 線索的變體字寫成中性字，自身不算線索。
	if got := sites[1].Cues; len(got) != 3 || got[0] != "吉他" || got[1] != "他們" || got[2] != "男朋友" {
		t.Fatalf("線索：%q", got)
	}
}

func learnPronoun(m *Model, word string, manual bool, cue string) {
	path := []Seg{{Word: word, Start: 0, End: len([]rune(word)), Manual: manual}}
	if cue != "" {
		n := len([]rune(word))
		path = append(path, Seg{Word: cue, Start: n, End: n + len([]rune(cue))})
	}
	syls := make([]zhuyin.Syllable, path[len(path)-1].End)
	for i := range syls {
		syls[i] = "ㄋㄧˇ"
	}
	m.Learn(Commit{Syls: syls, Path: path})
}

// 被動接受不建立線索、也不會累積成習慣；整體習慣需要多次明確選取且占多數。
func TestPronounGlobalHabit(t *testing.T) {
	m := pronounModel()
	for range 20 {
		learnPronoun(m, "妳", false, "朋友們")
	}
	if m.PronounVariant('你', []string{"朋友們"}) || len(m.Store.Pronouns["你"].Cues) != 0 {
		t.Fatalf("被動接受不應成為習慣：%+v", m.Store.Pronouns["你"])
	}
	m = pronounModel()
	cues := []string{"甲甲", "乙乙", "丙丙", "丁丁", "戊戊"}
	for i, cue := range cues {
		learnPronoun(m, "妳", true, cue)
		if got := m.PronounVariant('你', nil); got != (i == len(cues)-1) {
			t.Fatalf("第 %d 次明確選「妳」後整體習慣為 %v", i+1, got)
		}
	}
	for range 8 {
		learnPronoun(m, "你", false, "")
	}
	if m.PronounVariant('你', nil) {
		t.Fatalf("中性用法占多數時不應預設變體：%+v", m.Store.Pronouns["你"].All)
	}
	if m.PronounVariant('他', nil) {
		t.Fatal("「妳」的習慣不影響「他／她」")
	}
}

// 固定詞中的「她」沒有對應的中性詞，不是可換的代名詞（code review：崇她社 被改成 崇他社）。
func TestPronounFixedWordsAreNotSites(t *testing.T) {
	m := pronounModel()
	m.Lex.(*lexicon.Mem).Add(lexicon.Entry{Surface: "崇她社", Reading: zhuyin.ParseReading("ㄔㄨㄥˊ ㄊㄚ ㄕㄜˋ"), PRW: 1})
	if sites := m.PronounSites([]Seg{{Word: "崇她社", Start: 0, End: 3}, {Word: "她", Start: 3, End: 4}}); len(sites) != 1 || sites[0].Seg != 1 {
		t.Fatalf("只有單字「她」是代名詞：%+v", sites)
	}
}

// 明確選中性字只在推翻變體習慣時建立否決線索；線索額滿時逐出最久未更新者，新線索留得下來。
func TestPronounCueCreationAndEviction(t *testing.T) {
	m := pronounModel()
	learnPronoun(m, "他", true, "男朋友")
	if n := len(m.Store.Pronouns["他"].Cues); n != 0 {
		t.Fatalf("沒有變體習慣時選中性字不應建立線索：%d", n)
	}
	for i := range maxPronounCues + 20 {
		learnPronoun(m, "她", true, "線索"+string(rune('一'+i)))
	}
	learnPronoun(m, "她", true, "啊啊") // 依鍵排序在最前面
	cues := m.Store.Pronouns["他"].Cues
	if len(cues) != maxPronounCues {
		t.Fatalf("線索數 %d", len(cues))
	}
	if _, ok := cues["啊啊"]; !ok {
		t.Fatal("剛建立的線索在同一次提交就被逐出")
	}
	if !m.PronounVariant('他', nil) {
		t.Fatal("測試前提：此時整體習慣是變體")
	}
	learnPronoun(m, "他", true, "男朋友") // 推翻變體：建立否決
	if c, ok := m.Store.Pronouns["他"].Cues["男朋友"]; !ok || c.LastManual >= 0 || m.PronounVariant('他', []string{"男朋友"}) {
		t.Fatalf("推翻變體習慣應建立否決線索：%+v", c)
	}
}

// 撤銷固定詞不清除代名詞習慣；撤銷「她」才清除。
func TestPronounForgetOnlyPronounWords(t *testing.T) {
	m := pronounModel()
	m.Lex.(*lexicon.Mem).Add(lexicon.Entry{Surface: "崇她社", Reading: zhuyin.ParseReading("ㄔㄨㄥˊ ㄊㄚ ㄕㄜˋ"), PRW: 1})
	learnPronoun(m, "她", true, "男朋友")
	m.Forget("崇她社")
	if _, ok := m.Store.Pronouns["他"]; !ok {
		t.Fatal("撤銷固定詞不應清除代名詞習慣")
	}
	m.Forget("她們")
	if _, ok := m.Store.Pronouns["他"]; ok {
		t.Fatal("撤銷「她們」應清除「他／她」的習慣")
	}
}

// 接受神經提示不算明確選取：提示中的代名詞是習慣決定的，不能拿來強化習慣。
func TestPronounSuggestedIsNotExplicit(t *testing.T) {
	m := pronounModel()
	m.Learn(Commit{Syls: []zhuyin.Syllable{"ㄊㄚ", "ㄏㄣˇ", "ㄏㄠˇ"}, Path: []Seg{{Word: "她", Start: 0, End: 1, Manual: true, Suggested: true}, {Word: "很好", Start: 1, End: 3, Manual: true, Suggested: true}}})
	h := m.Store.Pronouns["他"]
	if h.All.ManualVariant != 0 || len(h.Cues) != 0 {
		t.Fatalf("接受提示被當成明確選取：%+v", h)
	}
}
