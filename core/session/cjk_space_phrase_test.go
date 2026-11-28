package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/vocab"
)

// #365：「我的片語」是使用者自己寫好的內容，自動加空格不改它；英文、數字都一樣（#273 原本整個送出字串一起補）。

var spacingPhrases = []string{"台北市信義路5段7號", "統編12345678", "我的Email是a@b.c", "ABC", "第2版", "有50%的人"}

// phraseSpaceSession：有「我的片語」的 session；latin、digits 是自動加空格的兩個開關。
func phraseSpaceSession(t *testing.T, latin, digits bool) *Session {
	t.Helper()
	s := paletteSession()
	s.Preview02 = true
	s.CJKLatinSpace, s.CJKDigitSpace = latin, digits
	if _, err := s.model.VocabAdd([]vocab.Entry{{Text: "世傑", Readings: []string{"ㄕˋ ㄐㄧㄝˊ"}}}, vocab.SourceManual); err != nil {
		t.Fatal(err) // 建立詞彙集（同 TestPalettePhrasesTab）
	}
	if _, err := s.model.Vocab.AddPhrases(spacingPhrases, vocab.SourceManual, 0); err != nil {
		t.Fatal(err)
	}
	return s
}

// pickPhrase 開 = 選單、切到「我的片語」、選第 n 個（從 1 起）。
func pickPhrase(t *testing.T, s *Session, n int) proto.Snapshot {
	t.Helper()
	key(s, "=")
	if s.cand == nil || s.cand.kind != "phrases" {
		if snap := key(s, proto.KeyBackTab); snap.Candidates == nil || snap.Candidates.Kind != "phrases" {
			t.Fatalf("我的片語：%+v", snap.Candidates)
		}
	}
	return key(s, string(rune('0'+n)))
}

// 沒在組字時選片語：直接送出，內容跟片語一模一樣。三種設定都一樣。
func TestCJKSpaceLeavesPhrasesAsWritten(t *testing.T) {
	for _, mode := range []struct{ latin, digits bool }{{false, false}, {true, false}, {true, true}, {false, true}} {
		for i, phrase := range spacingPhrases {
			s := phraseSpaceSession(t, mode.latin, mode.digits)
			if snap := pickPhrase(t, s, i+1); snap.Commit != phrase {
				t.Errorf("latin=%v digits=%v：片語 %q 送出 %q", mode.latin, mode.digits, phrase, snap.Commit)
			}
			if s.verbatim != nil {
				t.Errorf("送出後要清掉：%q", s.verbatim)
			}
		}
	}
}

// 組字中選片語：片語內部照原樣；片語跟前後的字相接的地方照一般規則補。
func TestCJKSpacePhraseBoundariesFollowTheRules(t *testing.T) {
	for _, tc := range []struct {
		phrase        int // spacingPhrases 的第幾個（從 1 起）
		latin, digits bool
		want          string
	}{
		{4, true, true, "我 ABC 想"}, // 「用」＋片語「ABC」→「用 ABC」
		{4, true, false, "我 ABC 想"},
		{4, false, false, "我ABC想"},
		{1, true, true, "我台北市信義路5段7號想"},     // 內部的數字不補；兩邊都是漢字，本來就不補
		{2, true, true, "我統編12345678 想"},    // 片語最後的數字跟後面的字之間是邊界，照補
		{2, true, false, "我統編12345678想"},    // 只補英文時數字旁不補
		{3, true, true, "我我的Email是a@b.c 想"}, // 內部的 Email 不補；最後的 c 跟後面的字之間照補
		{3, true, false, "我我的Email是a@b.c 想"},
		{6, true, true, "我有50%的人想"},
	} {
		s := phraseSpaceSession(t, tc.latin, tc.digits)
		typeSyl(s, "ㄨㄛˇ")
		if snap := pickPhrase(t, s, tc.phrase); snap.Commit != "" || snap.Text() != "我"+spacingPhrases[tc.phrase-1] {
			t.Fatalf("片語留在組字裡，照原樣顯示：%+v", snap)
		}
		typeSyl(s, "ㄒㄧㄤˇ")
		if snap := key(s, proto.KeyEnter); snap.Commit != tc.want {
			t.Errorf("片語 %q latin=%v digits=%v：送出 %q，要 %q", spacingPhrases[tc.phrase-1], tc.latin, tc.digits, snap.Commit, tc.want)
		}
	}
}

// 兩則片語相連：各自內部照原樣，兩則之間是邊界。片語開在組字最前面時，它跟後面打的字之間也是邊界。
func TestCJKSpaceBetweenTwoPhrases(t *testing.T) {
	s := phraseSpaceSession(t, true, true)
	typeSyl(s, "ㄨㄛˇ")
	pickPhrase(t, s, 4) // ABC
	pickPhrase(t, s, 2) // 統編12345678
	if snap := key(s, proto.KeyEnter); snap.Commit != "我 ABC 統編12345678" {
		t.Fatalf("兩則片語：%q", snap.Commit)
	}
}

// 同樣的字自己打（不是從片語選的）照一般規則補；片語送出之後，下一段手打的同樣文字也照補。
func TestCJKSpaceOnlyProtectsPickedPhrases(t *testing.T) {
	typed := func(s *Session) string {
		typeSyl(s, "ㄨㄛˇ")
		s.Handle(toEnglish)
		mixedType(s, "2")
		s.Handle(toEnglish)
		typeSyl(s, "ㄒㄧㄤˇ")
		return key(s, proto.KeyEnter).Commit
	}
	s := phraseSpaceSession(t, true, true)
	if got := typed(s); got != "我 2 想" {
		t.Fatalf("手打：%q", got)
	}
	if snap := pickPhrase(t, s, 5); snap.Commit != "第2版" {
		t.Fatalf("片語：%q", snap.Commit)
	}
	if got := typed(s); got != "我 2 想" {
		t.Fatalf("片語送出後再手打：%q", got)
	}
	// 選了片語又按 Esc 取消整段組字：之後不再記得那則片語。
	typeSyl(s, "ㄨㄛˇ")
	pickPhrase(t, s, 5)
	key(s, proto.KeyEscape)
	if s.composing() || s.verbatim != nil {
		t.Fatalf("取消後：組字 %v，記得的片語 %q", s.composing(), s.verbatim)
	}
}

// 選進來之後又改過的片語不再是原樣：照一般規則補。
func TestCJKSpaceEditedPhraseIsOrdinaryText(t *testing.T) {
	s := phraseSpaceSession(t, true, true)
	typeSyl(s, "ㄨㄛˇ")
	pickPhrase(t, s, 1)
	key(s, proto.KeyBackspace) // 刪掉「號」
	if snap := key(s, proto.KeyEnter); snap.Commit != "我台北市信義路 5 段 7" {
		t.Fatalf("改過的片語：%q", snap.Commit)
	}
}

// 含換行的片語連同前面的組字直接送出：片語內部（含換行前後）照原樣，跟前面組字之間照補。
func TestCJKSpacePhraseWithNewline(t *testing.T) {
	s := paletteSession()
	s.Preview02 = true
	s.CJKLatinSpace, s.CJKDigitSpace = true, true
	if _, err := s.model.VocabAdd([]vocab.Entry{{Text: "世傑", Readings: []string{"ㄕˋ ㄐㄧㄝˊ"}}}, vocab.SourceManual); err != nil {
		t.Fatal(err)
	}
	if _, err := s.model.Vocab.AddPhrases([]string{"TEL:02電話\n第2行"}, vocab.SourceManual, 0); err != nil {
		t.Fatal(err)
	}
	typeSyl(s, "ㄨㄛˇ")
	if snap := pickPhrase(t, s, 1); snap.Commit != "我 TEL:02電話\n第2行" {
		t.Fatalf("含換行的片語：%q", snap.Commit)
	}
}

// 輸出簡體字：送出的是轉換後的片語，一樣不補。
func TestCJKSpacePhraseWithSimplifiedOutput(t *testing.T) {
	s, conv := simplifiedSession(t, true)
	s.EmojiShortcut, s.CandidateColumns, s.Preview02 = "equals", 5, true
	s.CJKLatinSpace, s.CJKDigitSpace = true, true
	if _, err := s.model.VocabAdd([]vocab.Entry{{Text: "世傑", Readings: []string{"ㄕˋ ㄐㄧㄝˊ"}}}, vocab.SourceManual); err != nil {
		t.Fatal(err)
	}
	phrase := "統編12345678號ABC樓"
	if _, err := s.model.Vocab.AddPhrases([]string{phrase}, vocab.SourceManual, 0); err != nil {
		t.Fatal(err)
	}
	want := conv.Convert(phrase)
	if want == phrase {
		t.Fatalf("這則片語要有轉成簡體會變的字：%q", want)
	}
	if snap := pickPhrase(t, s, 1); snap.Commit != want {
		t.Fatalf("直接送出：%q，要 %q", snap.Commit, want)
	}
	typeReading(s, "ㄐㄧˋ ㄏㄨㄚˋ")
	pickPhrase(t, s, 1)
	typeReading(s, "ㄐㄧˋ ㄏㄨㄚˋ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "计划"+want+"计划" {
		t.Fatalf("組字中：%q", snap.Commit)
	}
}

// 私人欄位本來就不補；片語照原樣。
func TestCJKSpacePhraseInPrivateField(t *testing.T) {
	s := phraseSpaceSession(t, true, true)
	s.Handle(proto.Request{Type: proto.Key, Key: "j", Private: true})
	s.Handle(proto.Request{Type: proto.Key, Key: "i", Private: true})
	s.Handle(proto.Request{Type: proto.Key, Key: "3", Private: true})
	s.Handle(proto.Request{Type: proto.Toggle, Private: true})
	s.Handle(proto.Request{Type: proto.Key, Key: "2", Private: true})
	if got := s.Handle(proto.Request{Type: proto.Key, Key: proto.KeyEnter, Private: true}).Commit; got != "我2" {
		t.Fatalf("私人欄位：%q", got)
	}
}

// verbatimInside：只標片語內部，第一個字元不標；出現幾次標幾次；沒出現回 nil。
func TestSpaceCommitVerbatimRanges(t *testing.T) {
	for _, tc := range []struct {
		in       string
		verbatim []string
		want     string
	}{
		{"用ABC寫", []string{"ABC"}, "用 ABC 寫"},
		{"寫第2版和第2版", []string{"第2版"}, "寫第2版和第2版"},
		{"第2版", nil, "第 2 版"},
		{"第2版", []string{""}, "第 2 版"},
		{"第2版", []string{"第3版"}, "第 2 版"},
		{"第2版", []string{"2"}, "第 2 版"},         // 只有一個字元的片語沒有內部
		{"共3個第2版", []string{"第2版"}, "共 3 個第2版"}, // 片語以外的照補
		{"用Python3第2版", []string{"Python3第2版"}, "用 Python3第2版"},
		{"A第2版B", []string{"第2版"}, "A 第2版 B"},
		{"統編12345678號", []string{"統編12345678", "8號"}, "統編12345678號"}, // 兩段重疊：聯集
	} {
		if got := spaceCommit(tc.in, true, true, tc.verbatim); got != tc.want {
			t.Errorf("%q %q：%q，要 %q", tc.in, tc.verbatim, got, tc.want)
		}
		if got := spaceCommit(tc.in, false, false, tc.verbatim); got != tc.in {
			t.Errorf("%q 都關：%q", tc.in, got)
		}
	}
}

// 「我的詞彙」裡一般的自訂詞（不是片語）照一般規則補。
func TestCJKSpaceStillSpacesCustomWords(t *testing.T) {
	s := spaceSession(true)
	s.CJKDigitSpace = true
	if _, err := s.model.VocabAdd([]vocab.Entry{{Text: "kubectl"}}, vocab.SourceManual); err != nil {
		t.Fatal(err)
	}
	mixedType(s, "ji3kubectlsu3")
	if got := key(s, proto.KeyEnter).Commit; got != "我 kubectl 你" {
		t.Fatalf("自訂的英文詞：%q", got)
	}
}
