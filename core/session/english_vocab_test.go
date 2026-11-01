package session

import (
	"slices"
	"testing"

	"github.com/0c-biggo/0c-ime/core/english"
	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/vocab"
)

// englishLayouts 是混打有開、沒開、開了沒作用的各種情況（#247）：許氏與倚天不支援混打，聲調選填和混打二選一。
// 英文模式補字與英文學習在這些情況都一樣。
var englishLayouts = []struct {
	name  string
	setup func(*Session)
}{
	{"許氏", func(s *Session) { s.SetLayout(layout.Hsu) }},
	{"許氏開混打", func(s *Session) { s.SetLayout(layout.Hsu); s.MixedInput = true }},
	{"倚天", func(s *Session) { s.SetLayout(layout.ETen) }},
	{"標準沒開混打", func(s *Session) {}},
	{"標準開混打", func(s *Session) { s.MixedInput = true }},
	{"聲調選填", func(s *Session) { s.Toneless = true; s.MixedInput = true }},
}

func vocabAutocompleteSession(t *testing.T, words ...string) *Session {
	t.Helper()
	s := autocompleteSession()
	s.Preview02 = true
	var entries []vocab.Entry
	for _, w := range words {
		entries = append(entries, vocab.Entry{Text: w})
	}
	if _, err := s.model.VocabAdd(entries, vocab.SourceManual); err != nil {
		t.Fatal(err)
	}
	return s
}

func snapCandidates(snap proto.Snapshot) []string {
	if snap.Candidates == nil {
		return nil
	}
	var out []string
	for _, c := range snap.Candidates.Items {
		out = append(out, c.Text)
	}
	return out
}

// #247（GitHub 0c-ime#39）：「我的詞」的英文詞參與英文模式補字，前綴不分大小寫、候選照詞條字面。
func TestEnglishAutocompleteVocabWords(t *testing.T) {
	for _, l := range englishLayouts {
		for _, c := range []struct{ typed, want string }{
			{"pc", "pCloud"}, {"PC", "pCloud"}, {"pC", "pCloud"}, {"pcloud", "pCloud"}, {"rcl", "Rclone"},
			{"git", "git-lfs"}, {"ip", "ipv6"},
		} {
			s := vocabAutocompleteSession(t, "pCloud", "Rclone", "git-lfs", "ipv6")
			l.setup(s)
			snap := mixedType(s, c.typed)
			if got := snapCandidates(snap); len(got) == 0 || got[0] != c.want || snap.Text() != c.typed {
				t.Errorf("%s：%q 候選 %q、畫面 %q，第一個要 %q", l.name, c.typed, got, snap.Text(), c.want)
				continue
			}
			if snap := key(s, proto.KeyTab); snap.Text() != c.want {
				t.Errorf("%s：%q Tab 後 %q", l.name, c.typed, snap.Text())
			}
			if snap := key(s, proto.KeyEnter); !snap.Handled || snap.Commit != c.want {
				t.Errorf("%s：%q 送出 %+v", l.name, c.typed, snap)
			}
		}
	}
}

func TestEnglishAutocompleteVocabOrderAndDuplicates(t *testing.T) {
	for _, c := range []struct {
		words []string
		typed string
		want  []string
	}{
		{[]string{"helpdesk"}, "hel", []string{"helpdesk", "hello", "help"}}, // 排在字典前面
		{[]string{"Hello"}, "hel", []string{"Hello", "help"}},                // 和字典同字不重複，照詞條字面
		{[]string{"Hello"}, "hello", []string{"Hello"}},                      // 只差大小寫也是建議
		{[]string{"hello"}, "hello", nil},                                    // 和打的完全相同不算補字
		// 最多六個；留給拼字修正的位置照舊（help 是 hel 的修正）
		{[]string{"hela", "helb", "helc", "held", "hele", "helf", "helg"}, "hel", []string{"hela", "helb", "helc", "held", "hele", "help"}},
	} {
		s := vocabAutocompleteSession(t, c.words...)
		if got := snapCandidates(mixedType(s, c.typed)); !slices.Equal(got, c.want) {
			t.Errorf("%q 打 %q：%q，要 %q", c.words, c.typed, got, c.want)
		}
	}
}

// 使用者親手加的拼法和字典、學到的拼法一樣不提供修正：終端機打 recieve 按 Tab 不該被換掉。
func TestEnglishAutocompleteVocabSpellingIsProtected(t *testing.T) {
	s := vocabAutocompleteSession(t, "recieve")
	s.English = english.Prototype()
	if snap := mixedType(s, "recieve"); snap.Candidates != nil {
		t.Fatalf("加進「我的詞」的拼法被修正：%+v", snap.Candidates)
	}
	if snap := key(s, proto.KeyTab); snap.Handled || snap.Commit != "recieve" {
		t.Fatal(snap)
	}
}

func TestEnglishAutocompleteVocabRespectsGates(t *testing.T) {
	s := vocabAutocompleteSession(t, "pCloud")
	s.Preview02 = false
	if got := snapCandidates(mixedType(s, "pc")); got != nil {
		t.Errorf("Preview02 關閉：%q", got)
	}
	s = vocabAutocompleteSession(t, "pCloud")
	if snap := s.Handle(proto.Request{Type: proto.Key, Key: "p", Private: true}); snap.Handled || snap.Candidates != nil {
		t.Errorf("私人欄位：%+v", snap)
	}
	s = vocabAutocompleteSession(t, "pCloud")
	s.Handle(proto.Request{Type: proto.Pause, On: true})
	if snap := mixedType(s, "pc"); snap.Handled || snap.Candidates != nil {
		t.Errorf("暫停學習：%+v", snap)
	}
	s = vocabAutocompleteSession(t, "pCloud")
	s.localBlocked = true
	if got := s.vocabEnglishWords("pc", 6); got != nil {
		t.Errorf("這段組字進過保護狀態：%q", got)
	}
	s = vocabAutocompleteSession(t, "pCloud")
	s.EnglishAutocomplete = false
	if snap := mixedType(s, "pc"); snap.Handled || snap.Candidates != nil {
		t.Errorf("補字關閉：%+v", snap)
	}
}

// 接受「我的詞」的建議不算自行打字：不建立英文觀察。
func TestEnglishAutocompleteVocabAcceptDoesNotLearn(t *testing.T) {
	s := vocabAutocompleteSession(t, "pCloud")
	s.model.AdvanceTime(100 * englishDay)
	mixedType(s, "pc")
	key(s, proto.KeyTab)
	key(s, proto.KeyEnter)
	if len(s.model.Store.English) != 0 {
		t.Fatal(s.model.Store.English)
	}
}

// 「我的詞」只進英文模式補字，不進混打共用的 englishPrefix：ji（ㄨㄛ）不因為有 jira 這個詞就從注音變成字面，
// 也不會多一個英文補全提示。
func TestEnglishVocabPrefixDoesNotChangeMixedReading(t *testing.T) {
	for _, words := range [][]string{nil, {"jira"}} {
		s := englishVocabSession(t, true, words...)
		if snap := mixedType(s, "ji"); snap.Text() != "ㄨㄛ" || snap.Suggestion != "" {
			t.Errorf("%q：%+v", words, snap)
		}
	}
}

func typeEnglishWord(s *Session, word string) proto.Snapshot {
	mixedType(s, word)
	return key(s, proto.KeyEnter)
}

// #247：英文學習跟著「英文模式自動補字」走，混打沒開、排列不支援混打也學得到；門檻照舊（三次、跨兩天，每天最多兩次）。
func TestEnglishAutocompleteLearnsInEveryLayout(t *testing.T) {
	for _, l := range englishLayouts {
		t.Run(l.name, func(t *testing.T) {
			s := autocompleteSession()
			l.setup(s)
			s.model.AdvanceTime(100 * englishDay)
			for range 3 {
				typeEnglishWord(s, "rclone")
			}
			if e := s.model.Store.English["rclone"]; e.Confirmed != 2 || e.DayConfirmed != 2 {
				t.Fatalf("同一天最多確認兩次：%+v", e)
			}
			if got := snapCandidates(mixedType(s, "rcl")); got != nil {
				t.Fatalf("還沒跨日就建議：%q", got)
			}
			key(s, proto.KeyEscape)
			key(s, proto.KeyEnter)
			s.model.AdvanceTime(101 * englishDay)
			typeEnglishWord(s, "rclone")
			if got := snapCandidates(mixedType(s, "rcl")); !slices.Equal(got, []string{"rclone"}) {
				t.Fatalf("達到門檻後要建議：%q（%+v）", got, s.model.Store.English["rclone"])
			}
			key(s, proto.KeyTab)
			before := s.model.Store.English["rclone"].Confirmed
			key(s, proto.KeyEnter)
			if after := s.model.Store.English["rclone"].Confirmed; after > before {
				t.Fatalf("接受建議不算自行打字：%v → %v", before, after)
			}
		})
	}
}

// 私人欄位、暫停學習時不學。
func TestEnglishAutocompleteProtectedStatesDoNotLearn(t *testing.T) {
	for _, l := range englishLayouts {
		t.Run(l.name, func(t *testing.T) {
			start := func() *Session {
				s := autocompleteSession()
				l.setup(s)
				s.model.AdvanceTime(100 * englishDay)
				return s
			}
			check := func(name string, s *Session) {
				t.Helper()
				if len(s.model.Store.English) != 0 {
					t.Errorf("%s學到英文：%+v", name, s.model.Store.English)
				}
			}
			s := start()
			for _, k := range "rclone" {
				s.Handle(proto.Request{Type: proto.Key, Key: string(k), Private: true})
			}
			s.Handle(proto.Request{Type: proto.Key, Key: proto.KeyEnter, Private: true})
			check("私人欄位", s)

			s = start()
			mixedType(s, "rclone")
			if snap := s.Handle(proto.Request{Type: proto.Key, Key: proto.KeyEnter, Private: true}); snap.Commit != "rclone" {
				t.Errorf("打到一半進私人欄位：%+v", snap)
			}
			check("打到一半進私人欄位", s)

			s = start()
			s.Handle(proto.Request{Type: proto.Pause, On: true})
			typeEnglishWord(s, "rclone")
			check("暫停學習", s)

			s = start()
			mixedType(s, "rclone")
			if snap := s.Handle(proto.Request{Type: proto.Pause, On: true}); snap.Commit != "rclone" {
				t.Errorf("打到一半暫停：%+v", snap)
			}
			check("打到一半暫停", s)
		})
	}
}

// 英文補字關閉、混打也沒作用時不學：字母直接交給程式，引擎看不到。
func TestEnglishAutocompleteOffDoesNotLearn(t *testing.T) {
	for _, l := range englishLayouts {
		if l.name == "標準開混打" {
			continue // 混打開著本來就從混打學
		}
		t.Run(l.name, func(t *testing.T) {
			s := autocompleteSession()
			l.setup(s)
			s.EnglishAutocomplete = false
			s.model.AdvanceTime(100 * englishDay)
			for _, k := range "rclone" {
				if snap := key(s, string(k)); snap.Handled {
					t.Fatalf("字母要直接交給程式：%+v", snap)
				}
			}
			key(s, proto.KeyEnter)
			if len(s.model.Store.English) != 0 {
				t.Fatal(s.model.Store.English)
			}
		})
	}
}

// 這個開關只管英文模式：補字開著、混打沒開時，中文模式組字裡的大寫英文不學。
func TestEnglishAutocompleteDoesNotLearnFromChineseMode(t *testing.T) {
	s := autocompleteSession()
	s.Handle(proto.Request{Type: proto.Mode, On: false})
	s.model.AdvanceTime(100 * englishDay)
	mixedType(s, "ji3Rclone")
	if end := key(s, proto.KeyEnter); end.Commit == "" || len(s.model.Store.English) != 0 {
		t.Fatalf("%+v %+v", end, s.model.Store.English)
	}
}

// 防誤學照舊（混打沒開）：接受補字或修正不算獨立確認，網址、路徑、含數字底線的片段不學前綴。
func TestEnglishAutocompleteWithoutMixedKeepsLearningFilters(t *testing.T) {
	s := autocompleteSession()
	s.SetLayout(layout.Hsu)
	s.model.AdvanceTime(100 * englishDay)
	mixedType(s, "hel")
	key(s, proto.KeyTab)
	mixedType(s, "sx")
	key(s, proto.KeyBackspace)
	key(s, proto.KeyEscape)
	if end := key(s, proto.KeyEnter); end.Commit != "hellos" || len(s.model.Store.English) != 0 {
		t.Fatalf("接受補字後編輯：%+v %+v", end, s.model.Store.English)
	}
	s.English = english.Prototype()
	mixedType(s, "recieve")
	key(s, proto.KeyTab)
	if end := key(s, proto.KeyEnter); end.Commit != "receive" || len(s.model.Store.English) != 0 {
		t.Fatalf("接受修正：%+v %+v", end, s.model.Store.English)
	}
	for _, raw := range []string{"name@example.com", "my_name", "name123", "https://example.com", "file.txt", "folder/file", "name-value"} {
		s := autocompleteSession()
		s.SetLayout(layout.Hsu)
		s.model.AdvanceTime(100 * englishDay)
		for _, r := range raw + " " {
			key(s, string(r))
		}
		if len(s.model.Store.English) != 0 {
			t.Errorf("%q 學到前綴：%+v", raw, s.model.Store.English)
		}
	}
}
