package session

import (
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/fuzzy"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

const typoReading = "ㄧ ㄋㄧㄢˊ ㄋㄢˊ ㄐㄧㄣˋ"
const idiomReading = "ㄧ ㄧㄢˊ ㄋㄢˊ ㄐㄧㄣˋ"

func TestOnePhoneticSymbolEdit(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"ㄋㄧㄢˊ", "ㄧㄢˊ", true}, {"ㄧㄢˊ", "ㄋㄧㄢˊ", true},
		{"ㄐㄧㄣˋ", "ㄐㄧㄥˋ", true}, {"ㄧㄢˊ", "ㄧㄢˇ", false},
		{"ㄋㄧㄢˊ", "ㄧㄣˊ", false}, {"ㄧㄢˊ", "ㄧㄢˊ", false},
	} {
		if got := oneSymbolApart(zhuyin.Syllable(tc.a), zhuyin.Syllable(tc.b)); got != tc.want {
			t.Errorf("%s → %s: %v", tc.a, tc.b, got)
		}
	}
}

func typoSession() *Session {
	return fuzzySession(fuzzy.Config{},
		word{"一年難近", typoReading, 1e-8},
		word{"一言難盡", idiomReading, 1e-2},
		word{"啊", "ㄚ", 1e-3},
		word{"泰平盛世", "ㄊㄞˋ ㄆㄧㄥˊ ㄕㄥˋ ㄕˋ", 1e-8},
		word{"太平盛世", "ㄊㄞˋ ㄆㄧㄥˊ ㄕㄥˋ ㄕˋ", 1e-2})
}

func splitTypoSession() *Session {
	words := append(fillers(typoReading),
		word{"一年", "ㄧ ㄋㄧㄢˊ", .01}, word{"難盡", "ㄋㄢˊ ㄐㄧㄣˋ", .01},
		word{"一言難盡", idiomReading, .1}, word{"我", "ㄨㄛˇ", .1})
	for _, ch := range "盡近進禁僅勁儘晉燼浸靳噤" {
		words = append(words, word{string(ch), "ㄐㄧㄣˋ", .001})
	}
	return fuzzySession(fuzzy.Config{}, words...)
}

func TestWholeWordPickerKeepsRawLastAndExactChoice(t *testing.T) {
	s := splitTypoSession()
	fzType(s, typoReading)
	// 句尾 ↓ 選結束在游標的詞（難盡）；單字清單由明確的詞長請求打開，原樣注音仍排最後。
	if snap := key(s, proto.KeyDown); snap.Candidates == nil || snap.Candidates.Start != 2 || snap.Candidates.Length != 2 {
		t.Fatalf("tail Down: %+v", snap.Candidates)
	}
	snap := s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
	first := snap.Candidates.Items[0].Text
	last := s.cand.items[len(s.cand.items)-1].Entry.Surface
	if last != "ㄐㄧㄣˋ" {
		t.Fatalf("raw last: %q", last)
	}
	if got := key(s, proto.KeyEnter).Text(); !strings.HasSuffix(got, first) || len([]rune(got)) != 4 {
		t.Fatal(got)
	}
	s = splitTypoSession()
	fzType(s, typoReading)
	s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
	if got := s.Handle(proto.Request{Type: proto.Select, Index: len(s.cand.items) - 1}).Text(); !strings.HasSuffix(got, "ㄐㄧㄣˋ") {
		t.Fatal("raw ID displaced")
	}
}

func TestPhoneticCandidatesAreSelectionOnly(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		s := typoSession()
		before := fzType(s, typoReading).Text()
		if before == "一言難盡" {
			t.Fatal("new repair leaked into automatic decoding")
		}
		if cancel {
			items := listCandidates(s, 0, 4)
			it, ok := find(items, "一言難盡")
			if !ok || it.Note != "近音 "+idiomReading || it.Label != "" {
				t.Fatalf("missing provenance: %+v", items)
			}
			if items[0].Text != "一年難近" {
				t.Fatalf("original first candidate changed: %+v", items)
			}
			if got := key(s, proto.KeyEscape).Text(); got != before {
				t.Fatalf("cancel changed composition: %q", got)
			}
		}
		if got := key(s, proto.KeyEnter).Commit; got != before {
			t.Fatalf("direct commit changed: %q, want %q", got, before)
		}
	}
}

func TestPhoneticCandidateNavigationBothDirections(t *testing.T) {
	for _, before := range []bool{false, true} {
		s := typoSession()
		fzType(s, typoReading)
		s.CandidateBeforeCursor = before
		cursor := 0
		if before {
			cursor = 4
		}
		s.Handle(proto.Request{Type: proto.Cursor, To: cursor})
		s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
		for range 4 {
			snap := key(s, proto.KeyTab)
			if snap.Candidates != nil && snap.Candidates.Length == 4 {
				it, ok := find(snap.Candidates.Items, "一言難盡")
				if !ok {
					t.Fatal("repair not on first page")
				}
				s.Handle(proto.Request{Type: proto.Select, Index: it.ID})
				break
			}
		}
		if s.locks[0].Matched != idiomReading {
			t.Fatalf("selection did not retain reading: %+v", s.locks)
		}
		s.Handle(proto.Request{Type: proto.Cursor, To: 4})
		if got := typeSyl(s, "ㄚ").Text(); got != "一言難盡啊" {
			t.Fatalf("continued input lost selection: %q", got)
		}
		key(s, proto.KeyEnter)
		st := s.model.Store
		if st.Uni["一言難盡"] != 0 || st.Uses["一言難盡"] != 0 {
			t.Fatal("assisted input learned as independent evidence")
		}
		for reading := range st.Readings {
			if strings.Contains(reading, "一言難盡") {
				t.Fatal("incorrect input learned as a pronunciation")
			}
		}
	}
}

func TestPhoneticCandidateScope(t *testing.T) {
	for _, tc := range []struct {
		typed, target, reading string
		want                   bool
	}{
		{typoReading, "一言難盡", idiomReading, true},
		{typoReading + " ㄚ", "一言難盡啊", idiomReading + " ㄚ", true},
		{"ㄧ ㄋㄧㄢˊ ㄋㄢˊ ㄧㄣˋ", "一言難盡", idiomReading, false}, // two edits
		{"ㄧ ㄧㄢˇ ㄋㄢˊ ㄐㄧㄣˋ", "一言難盡", idiomReading, false}, // tone
		{"ㄏㄢˋ ㄏㄨㄚˋ", "漫畫", "ㄇㄢˋ ㄏㄨㄚˋ", false},
		{"ㄌㄧㄣˊ ㄒㄧㄢ ㄕㄥ", "劉先生", "ㄌㄧㄡˊ ㄒㄧㄢ ㄕㄥ", false},
	} {
		t.Run(tc.target+tc.typed, func(t *testing.T) {
			extra := append(fillers(tc.typed), word{tc.target, tc.reading, 0.1})
			s := fuzzySession(fuzzy.Config{}, extra...)
			fzType(s, tc.typed)
			_, got := find(listCandidates(s, 0, len(strings.Fields(tc.typed))), tc.target)
			if got != tc.want {
				t.Fatalf("repair present %v, want %v", got, tc.want)
			}
		})
	}
	// A long sentence does not make its two-character component eligible.
	s := fuzzySession(fuzzy.Config{}, append(fillers("ㄧ ㄋㄧㄢˊ ㄏㄢˋ ㄏㄨㄚˋ"), word{"漫畫", "ㄇㄢˋ ㄏㄨㄚˋ", .1})...)
	fzType(s, "ㄧ ㄋㄧㄢˊ ㄏㄢˋ ㄏㄨㄚˋ")
	if _, ok := find(listCandidates(s, 2, 2), "漫畫"); ok {
		t.Fatal("short word inside a long sentence was repaired")
	}
}

func TestPhoneticCandidatePreservesDeliberateNameAndCanBeReselected(t *testing.T) {
	s := typoSession()
	fzType(s, "ㄊㄞˋ ㄆㄧㄥˊ ㄕㄥˋ ㄕˋ")
	fzSelect(t, s, 0, 4, "泰平盛世")
	if got := typeSyl(s, "ㄚ").Text(); got != "泰平盛世啊" {
		t.Fatalf("deliberate name overwritten: %q", got)
	}
	if got := key(s, proto.KeyEnter).Commit; got != "泰平盛世啊" {
		t.Fatal(got)
	}
	s = typoSession()
	fzType(s, typoReading)
	fzSelect(t, s, 0, 4, "一言難盡")
	fzSelect(t, s, 0, 4, "一年難近")
	if s.locks[0].Matched != "" || key(s, proto.KeyEnter).Commit != "一年難近" {
		t.Fatal("could not restore literal original reading")
	}
}

func TestPhoneticCandidateSkipsAliasesAndDeduplicatesDialect(t *testing.T) {
	s := typoSession()
	s.model.Lex.(*lexicon.Mem).Add(lexicon.Entry{Surface: "錯誤別名", Reading: zhuyin.ParseReading(idiomReading), PRW: 1, Correction: true})
	fzType(s, typoReading)
	if _, ok := find(listCandidates(s, 0, 4), "錯誤別名"); ok {
		t.Fatal("correction alias bypassed edit budget")
	}
	s = fuzzySession(allPairs(), append(fillers("ㄒㄧㄣ ㄒㄧㄥ ㄓ ㄏㄨㄛˇ"), word{"星星之火", "ㄒㄧㄥ ㄒㄧㄥ ㄓ ㄏㄨㄛˇ", .1})...)
	fzType(s, "ㄒㄧㄣ ㄒㄧㄥ ㄓ ㄏㄨㄛˇ")
	items := texts(listCandidates(s, 0, 4))
	if slices.Index(items, "星星之火") < 0 || strings.Count(strings.Join(items, ","), "星星之火") != 1 {
		t.Fatalf("duplicate/missing suggestion: %v", items)
	}
}
