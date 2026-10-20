package session

import (
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/english"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func mixedSession() *Session { s := newSession(); s.MixedInput = true; return s }
func mixedType(s *Session, keys string) proto.Snapshot {
	var snap proto.Snapshot
	for _, k := range keys {
		snap = key(s, string(k))
	}
	return snap
}

func TestAutomaticMixedChineseEnglishChinese(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"ji3hellot ", "我hello吃"},
		{"helloji3", "hello我"},
		{"ji3hello worldji3", "我hello world我"},
		{"ji3Hello API ji3", "我Hello API 我"},
		{"ji3", "我"}, {"t ", "吃"}, {"a86z06", "麻煩"},
	} {
		s := mixedSession()
		mixedType(s, tc.keys)
		snap := key(s, proto.KeyEnter)
		if snap.Commit != tc.want || snap.Text() != "" || !snap.Handled {
			t.Errorf("%q: %+v want %q", tc.keys, snap, tc.want)
		}
	}
}

func TestMixedRawKeysSurviveTyposAndIdentifiers(t *testing.T) {
	for _, raw := range []string{"recieve", "hellp", "helllo", "someNewName", "foo_bar123", "https://example.com/a?q=1", "C:\\Users\\kevin", "123456", "hello😀", "hello中文", strings.Repeat("x", 180)} {
		s := mixedSession()
		mixedType(s, raw)
		if snap := key(s, proto.KeyEnter); snap.Commit != raw {
			t.Errorf("%s -> %s", raw, snap.Commit)
		}
	}
}

func TestMixedCompletionCorrectionAndUndo(t *testing.T) {
	for _, tc := range []struct{ raw, word string }{{"meet", "meeting"}, {"recieve", "receive"}, {"hellp", "hello"}} {
		s := mixedSession()
		mixedType(s, "ji3"+tc.raw)
		if snap := s.snapshot(); snap.SuggestedText() != "我"+tc.word {
			t.Fatalf("%s: %+v", tc.raw, snap)
		}
		if snap := key(s, proto.KeyTab); snap.Text() != "我"+tc.word || !snap.Handled {
			t.Fatalf("accept: %+v", snap)
		}
		if snap := key(s, proto.KeyEscape); snap.Text() != "我"+tc.raw {
			t.Fatalf("undo: %+v", snap)
		}
		key(s, proto.KeyTab)
		if snap := key(s, proto.KeyBackspace); snap.Text() != "我"+tc.raw {
			t.Fatalf("backspace undo: %+v", snap)
		}
		key(s, proto.KeyTab)
		if snap := key(s, proto.KeyEnter); snap.Commit != "我"+tc.word {
			t.Fatalf("commit: %+v", snap)
		}
		if s.model.Store.Uni[tc.raw] != 0 || s.model.Store.Uni[tc.word] != 0 {
			t.Fatal("prototype English must not poison Chinese learning")
		}
	}
}

func TestMixedCandidatesRetainRawAndTabDoesNotAccept(t *testing.T) {
	s := mixedSession()
	mixedType(s, "recieve")
	key(s, proto.KeyDown)
	before := s.snapshot()
	if before.Candidates == nil || before.Candidates.Items[0].Text != "recieve" {
		t.Fatalf("raw candidate: %+v", before)
	}
	key(s, proto.KeyTab)
	if s.snapshot().Text() != before.Text() {
		t.Fatal("candidate Tab accepted correction")
	}
	pickText(t, s, "receive")
	if snap := key(s, proto.KeyEscape); snap.Text() != "recieve" {
		t.Fatalf("candidate undo: %+v", snap)
	}
	key(s, proto.KeyDown)
	pickText(t, s, "recieve")
	if snap := key(s, proto.KeyEnter); snap.Commit != "recieve" {
		t.Fatal(snap.Commit)
	}
}

func TestMixedExplicitPhoneticAndLiteralAmbiguity(t *testing.T) {
	// One physical key can be kept literally or explicitly interpreted as ㄔ.
	s := mixedSession()
	mixedType(s, "t")
	key(s, proto.KeyDown)
	pickText(t, s, "t")
	if snap := key(s, " "); snap.Text() != "t " {
		t.Fatalf("explicit raw: %+v", snap)
	}
	s = mixedSession()
	mixedType(s, "t")
	key(s, proto.KeyDown)
	pickText(t, s, "ㄔ")
	if snap := key(s, " "); snap.Text() != "吃" {
		t.Fatalf("explicit phonetic: %+v", snap)
	}
	s = mixedSession()
	s.model.Lex.(*lexicon.Mem).Add(lexicon.Entry{Surface: "誰", Reading: zhuyin.ParseReading("ㄕㄟ"), PRW: 1})
	mixedType(s, "go")
	key(s, proto.KeyDown)
	pickText(t, s, "go")
	key(s, " ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "go " {
		t.Fatalf("English whitespace boundary: %+v", snap)
	}
	mixedType(s, "go")
	key(s, proto.KeyDown)
	pickText(t, s, "ㄕㄟ")
	if snap := key(s, " "); snap.Text() != "誰" {
		t.Fatalf("phonetic alternative for English word: %+v", snap)
	}
}

// #58: common English words must not steal a reachable first-tone reading.
func TestMixedFirstToneEnglishCollisions(t *testing.T) {
	for _, tc := range []struct{ raw, reading, word, completion string }{
		{"up", "ㄧㄣ", "音", "update"}, {"co", "ㄏㄟ", "黑", "code"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			fresh := func() *Session {
				s := mixedSession()
				s.English = english.New([]english.Word{{Text: tc.raw, Weight: 100}, {Text: tc.completion, Weight: 50}})
				s.model.Lex.(*lexicon.Mem).Add(lexicon.Entry{Surface: tc.word, Reading: zhuyin.ParseReading(tc.reading), PRW: 1})
				return s
			}
			s := fresh()
			if snap := mixedType(s, tc.raw); snap.Text() != tc.reading || snap.SuggestionEdit != nil {
				t.Fatalf("pending reading: %+v", snap)
			}
			key(s, proto.KeyBackspace)
			if snap := key(s, tc.raw[1:]); snap.Text() != tc.reading {
				t.Fatalf("retyped reading: %+v", snap)
			}
			if snap := key(s, " "); snap.Text() != tc.word {
				t.Fatalf("first tone: %+v", snap)
			}
			if snap := key(s, proto.KeyEnter); snap.Commit != tc.word {
				t.Fatalf("Chinese commit: %+v", snap)
			}
			if len(s.model.Store.English) != 0 {
				t.Fatal("phonetic keys learned as English")
			}
			s = fresh()
			mixedType(s, tc.raw)
			key(s, proto.KeyDown)
			pickText(t, s, tc.raw)
			key(s, " ")
			if snap := key(s, proto.KeyEnter); snap.Commit != tc.raw+" " {
				t.Fatalf("explicit English: %+v", snap)
			}
			for _, suffix := range []string{tc.completion[len(tc.raw):], "://example.com", "_name", "/path"} {
				s = fresh()
				mixedType(s, tc.raw+suffix)
				if snap := key(s, proto.KeyEnter); snap.Commit != tc.raw+suffix {
					t.Fatalf("English continuation %q: %+v", suffix, snap)
				}
			}
			s = fresh()
			mixedType(s, tc.raw)
			key(s, proto.KeyDown)
			pickText(t, s, tc.raw)
			if snap := key(s, proto.KeyTab); snap.Text() != tc.completion {
				t.Fatalf("explicit English completion: %+v", snap)
			}
			if snap := key(s, proto.KeyEscape); snap.Text() != tc.raw {
				t.Fatalf("English completion undo: %+v", snap)
			}
			s = fresh()
			mixedType(s, "ji3"+tc.raw+" ji3")
			if snap := key(s, proto.KeyEnter); snap.Commit != "我"+tc.word+"我" {
				t.Fatalf("Chinese context: %+v", snap)
			}
		})
	}
}

// #59: a complete English word keeps its space; only an unseparated tail
// (helloji␣) or a whole first-tone reading (up␣) becomes Chinese.
func TestMixedCompleteEnglishWordKeepsSpace(t *testing.T) {
	fresh := func() *Session {
		s := mixedSession()
		var words []english.Word
		for _, w := range []string{"hello", "hell", "thank", "than", "you", "yo", "open", "ope", "the", "door", "see", "up"} {
			words = append(words, english.Word{Text: w, Weight: 100})
		}
		s.English = english.New(words)
		for _, e := range []struct{ surface, reading string }{{"欸", "ㄟ"}, {"婀", "ㄜ"}, {"斯", "ㄙ"}, {"一", "ㄧ"}, {"窩", "ㄨㄛ"}, {"音", "ㄧㄣ"}} {
			s.model.Lex.(*lexicon.Mem).Add(lexicon.Entry{Surface: e.surface, Reading: zhuyin.ParseReading(e.reading), PRW: 1})
		}
		return s
	}
	for _, tc := range []struct{ keys, want string }{
		{"hello ", "hello "}, {"thank you ", "thank you "}, {"open the door ", "open the door "},
		{"see you ", "see you "}, {"helloji ", "hello窩"}, {"up ", "音"}, {"ji3hello ji3", "我hello 我"},
	} {
		s := fresh()
		if snap := mixedType(s, tc.keys); snap.Text() != tc.want {
			t.Errorf("%q composition: %q want %q", tc.keys, snap.Text(), tc.want)
		}
		if snap := key(s, proto.KeyEnter); snap.Commit != tc.want {
			t.Errorf("%q commit: %+v want %q", tc.keys, snap, tc.want)
		}
	}
	// Explicitly choosing the raw keys through ↓ keeps the same space boundary.
	s := fresh()
	mixedType(s, "you")
	key(s, proto.KeyDown)
	pickText(t, s, "you")
	if snap := key(s, " "); snap.Text() != "you " {
		t.Fatalf("explicit English: %+v", snap)
	}
}

func TestMixedPendingEditsCancelAndManualMode(t *testing.T) {
	s := mixedSession()
	mixedType(s, "ji3hello")
	for range 5 {
		key(s, proto.KeyBackspace)
	}
	if snap := s.snapshot(); snap.Text() != "我" {
		t.Fatalf("raw backspace: %+v", snap)
	}
	mixedType(s, "hello")
	key(s, proto.KeyLeft)
	key(s, proto.KeyBackspace)
	if snap := key(s, proto.KeyEnter); snap.Commit != "我helo" {
		t.Fatalf("edit literal: %+v", snap)
	}
	s = mixedSession()
	mixedType(s, "ji3t ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	mixedType(s, "hello")
	if snap := s.snapshot(); snap.Text() != "我hello吃" || snap.Cursor.Rune != 6 {
		t.Fatalf("middle: %+v", snap)
	}
	if snap := key(s, proto.KeyEnter); snap.Commit != "我hello吃" {
		t.Fatal(snap.Commit)
	}
	s = mixedSession()
	mixedType(s, "hello")
	s.Handle(proto.Request{Type: proto.Toggle})
	mixedType(s, " 123")
	if snap := key(s, proto.KeyEnter); snap.Commit != "hello 123" || snap.Mode != proto.ModeEnglish {
		t.Fatalf("manual mode: %+v", snap)
	}
	s.Handle(proto.Request{Type: proto.Toggle})
	mixedType(s, "meet")
	key(s, proto.KeyTab)
	s.Handle(proto.Request{Type: proto.Cancel})
	if s.Composing() || s.model.Store.Total != 0 {
		t.Fatal("cancel must clear all pending input without learning")
	}
	if key(s, proto.KeyTab).Handled {
		t.Fatal("idle Tab must reach app")
	}
}

func TestMixedOffPreservesLegacyKeys(t *testing.T) {
	s := newSession()
	s.MixedInput = false
	if key(s, "A").Handled {
		t.Fatal("disabled must not intercept idle English")
	}
	if snap := mixedType(s, "ji3"); snap.Text() != "我" {
		t.Fatalf("phonetic keys: %+v", snap)
	}
	s.Handle(proto.Request{Type: proto.Toggle})
	mixedType(s, "hello")
	if snap := key(s, proto.KeyEnter); snap.Commit != "我hello" {
		t.Fatalf("manual mix: %+v", snap)
	}
}

func TestMixedCommitWithNeuralDisplayCache(t *testing.T) {
	calls := 0
	s := neuralSession(&calls)
	s.MixedInput = true
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄐㄧㄥˇ")
	mixedType(s, "hello")
	before := s.snapshot().Text()
	count := calls
	if before != "我井hello" {
		t.Fatalf("display: %q", before)
	}
	if snap := key(s, proto.KeyEnter); snap.Commit != before || calls != count {
		t.Fatalf("commit changed text or reranked: %+v calls=%d→%d", snap, count, calls)
	}
}

func TestMixedRestorePendingZhuyinAfterKeypadDigits(t *testing.T) {
	s := mixedSession()
	mixedType(s, "ji")
	for _, k := range []string{"KP1", "KP2", "KP3"} {
		key(s, k)
	}
	for range 3 {
		key(s, proto.KeyBackspace)
	}
	if snap := key(s, "3"); snap.Text() != "我" {
		t.Fatalf("pending restoration: %+v", snap)
	}
}

func TestMixedCompletedChineseCanRecoverPhysicalKeys(t *testing.T) {
	for _, tc := range []struct {
		keys, shown string
		length      int
	}{
		{"ji3", "我", 1}, {"a86z06", "麻煩", 2},
	} {
		s := mixedSession()
		if snap := mixedType(s, tc.keys); snap.Text() != tc.shown {
			t.Fatalf("before recovery: %+v", snap)
		}
		s.Handle(proto.Request{Type: proto.Cursor, To: 0})
		snap := s.Handle(proto.Request{Type: proto.Candidates, Length: tc.length})
		found := false
		for _, item := range snap.Candidates.Items {
			if item.Text == tc.keys {
				found = true
				if item.Note != "原始按鍵" || item.Label != "" {
					t.Fatalf("raw keys mislabeled: %+v", item)
				}
			}
		}
		if !found {
			t.Fatalf("missing physical keys: %+v", snap)
		}
		pickText(t, s, tc.keys)
		if snap := key(s, proto.KeyEnter); snap.Commit != tc.keys {
			t.Fatalf("recovered: %+v", snap)
		}
		if s.model.Store.Uni[tc.keys] != 0 || len(s.mixedKeys) != 0 {
			t.Fatal("raw keys learned as Chinese or retained after commit")
		}
	}
}

func TestMixedPhysicalKeysFollowCursorEditsAndCancel(t *testing.T) {
	s := mixedSession()
	mixedType(s, "ji3t ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	mixedType(s, "hello")
	s.Handle(proto.Request{Type: proto.Cursor, To: 6})
	key(s, proto.KeyDown)
	if snap := pickText(t, s, "t "); snap.Text() != "我hellot " {
		t.Fatalf("shifted keys: %+v", snap)
	}
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	key(s, proto.KeyBackspace)
	if len(s.mixedKeys) != 0 {
		t.Fatal("deleted/replaced readings retained physical keys")
	}
	s.Handle(proto.Request{Type: proto.Cancel})
	mixedType(s, "ji3")
	s.Handle(proto.Request{Type: proto.Cancel})
	if len(s.mixedKeys) != 0 {
		t.Fatal("cancel retained physical keys")
	}
}

// #69：中英混打時 i＋空白／i7 要打出「喔」；i 後面接著打完整英文單字（i am、i love you）
// 時回頭改成英文。a 仍是保留空白的英文單字。詞庫沒有輕聲 ㄛ˙、ㄜ˙ 時取其他聲調的語氣詞
// （喔、呃），沒有語氣詞的才退回一聲。
func TestMixedLetterIAndNeutralToneParticle(t *testing.T) {
	fresh := func(mixed bool) *Session {
		s := newSession()
		s.MixedInput = mixed
		var words []english.Word
		for _, w := range []string{"a", "i", "am", "iphone", "think", "love", "you"} {
			words = append(words, english.Word{Text: w, Weight: 100})
		}
		s.English = english.New(words)
		for _, e := range []struct{ surface, reading string }{
			{"喔", "ㄛ"}, {"呣", "ㄇ"}, {"阿", "ㄜ"}, {"餓", "ㄜˋ"}, {"呃", "ㄜˋ"}, {"安", "ㄢ"}, {"唉", "ㄞ"}, {"欸", "ㄞ"}, {"欸", "ㄟ"}, {"哦", "ㄜˊ"}, {"哦", "ㄛˊ"}, {"啦", "ㄌㄚ˙"}, {"拉", "ㄌㄚ"}, {"東", "ㄉㄨㄥ"}, {"西", "ㄒㄧ"}, {"東西", "ㄉㄨㄥ ㄒㄧ˙"},
		} {
			s.model.Lex.(*lexicon.Mem).Add(lexicon.Entry{Surface: e.surface, Reading: zhuyin.ParseReading(e.reading), PRW: 1})
		}
		return s
	}
	for _, tc := range []struct {
		keys, want string
		mixed      bool
	}{
		{"t i ", "吃喔", true}, {"t i7", "吃喔", true}, {"i ", "喔", true}, {"i7", "喔", true},
		{"i7", "喔", false}, {"i ", "喔", false},
		{"I am ", "I am ", true}, {"a ", "a ", true}, {"iphone ", "iphone ", true},
		{"i am ", "i am ", true}, {"i think ", "i think ", true}, {"i love you ", "i love you ", true}, {"i'm ", "i'm ", true},
		{"i ji3", "喔我", true}, {"t i ji3", "吃喔我", true}, {"t i7am ", "吃喔am ", true}, // 接中文或明確打輕聲時保留喔
		{"k7", "呃", false}, {"k7", "呃", true}, {"07", "安", false}, // 語氣詞優先；沒有語氣詞才退回一聲
		{"t k7", "吃呃", false}, {"97", "唉", false}, {"o7", "欸", false}, // 哦、欸只在主音節（ㄛ、ㄟ）補輕聲
		{"x87", "啦", true}, {"x87", "啦", false}, // 有輕聲字時不改讀一聲
		{"2j/ vu7", "東西", false}, {"2j/ vu7", "東西", true}, // 輕聲只在詞裡出現也算找得到
	} {
		s := fresh(tc.mixed)
		if snap := mixedType(s, tc.keys); snap.Text() != tc.want {
			t.Errorf("%q mixed=%v composition: %q want %q", tc.keys, tc.mixed, snap.Text(), tc.want)
		}
		if snap := key(s, proto.KeyEnter); snap.Commit != tc.want {
			t.Errorf("%q mixed=%v commit: %+v want %q", tc.keys, tc.mixed, snap, tc.want)
		}
	}
	// 英文詞還沒結束時畫面是「喔am」，Enter 具體化英文時同樣改判。
	s := fresh(true)
	if snap := mixedType(s, "i am"); snap.Text() != "喔am" {
		t.Fatalf("pending English after i: %+v", snap)
	}
	if snap := key(s, proto.KeyEnter); snap.Commit != "i am" {
		t.Fatalf("Enter should revise i before English: %+v", snap)
	}
	// 待定的 i 顯示注音；要英文 i 時按 ↓ 選原始按鍵。
	s = fresh(true)
	if snap := key(s, "i"); snap.Text() != "ㄛ" {
		t.Fatalf("pending i should show ㄛ: %+v", snap)
	}
	key(s, proto.KeyDown)
	pickText(t, s, "i")
	if snap := key(s, " "); snap.Text() != "i " {
		t.Fatalf("explicit English i: %+v", snap)
	}
}
