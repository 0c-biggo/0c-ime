package session

import (
	"strings"
	"testing"

	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/punct"
	"git.biggo.com/0c/ime/core/vocab"
	"git.biggo.com/0c/ime/core/zhconv"
	"git.biggo.com/0c/ime/core/zhuyin"
	"git.biggo.com/0c/ime/internal/simplified"
)

// #118 第 2 點「輸出簡體字」：組字與候選照樣是繁體，送出時才用 OpenCC tw2s 轉成簡體。
func simplifiedSession(t *testing.T, on bool) (*Session, *zhconv.Converter) {
	t.Helper()
	conv, err := simplified.TW2S()
	if err != nil {
		t.Fatal(err)
	}
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"計畫", "ㄐㄧˋ ㄏㄨㄚˋ", 1e-3}, {"計", "ㄐㄧˋ", 1e-4}, {"畫", "ㄏㄨㄚˋ", 1e-4},
		{"顯著", "ㄒㄧㄢˇ ㄓㄨˋ", 1e-3}, {"顯著", "ㄒㄧㄢˇ ㄓㄜ˙", 1e-3}, {"顯", "ㄒㄧㄢˇ", 1e-5},
		{"土著", "ㄊㄨˇ ㄓㄨˋ", 1e-3}, {"原著", "ㄩㄢˊ ㄓㄨˋ", 1e-3}, {"看著", "ㄎㄢˋ ㄓㄜ˙", 1e-3},
		{"著", "ㄓㄨˋ", 1e-5}, {"著", "ㄓㄜ˙", 1e-5},
		{"乾安", "ㄑㄧㄢˊ ㄢ", 1e-3}, {"乾淨", "ㄍㄢ ㄐㄧㄥˋ", 1e-3}, {"下沈", "ㄒㄧㄚˋ ㄔㄣˊ", 1e-3},
		{"明瞭", "ㄇㄧㄥˊ ㄌㄧㄠˇ", 1e-3}, {"瞭望", "ㄌㄧㄠˋ ㄨㄤˋ", 1e-3},
		{"我", "ㄨㄛˇ", 1e-2}, {"們", "ㄇㄣ˙", 1e-3}, {"我們", "ㄨㄛˇ ㄇㄣ˙", 1e-2},
		{"臺灣", "ㄊㄞˊ ㄨㄢ", 1e-3}, {"台", "ㄊㄞˊ", 1e-4}, {"灣", "ㄨㄢ", 1e-4},
		// 單字：打第一個音時要有字可以顯示
		{"土", "ㄊㄨˇ", 1e-6}, {"原", "ㄩㄢˊ", 1e-6}, {"看", "ㄎㄢˋ", 1e-6}, {"乾", "ㄑㄧㄢˊ", 1e-6}, {"乾", "ㄍㄢ", 1e-6},
		{"安", "ㄢ", 1e-6}, {"淨", "ㄐㄧㄥˋ", 1e-6}, {"下", "ㄒㄧㄚˋ", 1e-6}, {"沈", "ㄔㄣˊ", 1e-6}, {"明", "ㄇㄧㄥˊ", 1e-6},
		{"瞭", "ㄌㄧㄠˋ", 1e-6}, {"瞭", "ㄌㄧㄠˇ", 1e-6}, {"望", "ㄨㄤˋ", 1e-6}, {"大", "ㄉㄚˋ", 1e-6}, {"學", "ㄒㄩㄝˊ", 1e-6},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.Opt.PrefixAware = false
	s.Preview02 = true
	if on {
		s.Simplified = conv
	}
	return s, conv
}

func typeReading(s *Session, reading string) proto.Snapshot {
	var snap proto.Snapshot
	for _, y := range strings.Fields(reading) {
		snap = typeSyl(s, y)
	}
	return snap
}

func TestSimplifiedOutputOnlyChangesTheCommittedText(t *testing.T) {
	for _, on := range []bool{false, true} {
		s, _ := simplifiedSession(t, on)
		s.Rerank.ContextRunes = 8                                                     // 評分器要前文
		s.Handle(proto.Request{Type: proto.Context, ContextEnabled: true, On: false}) // 外殼支援前文確認
		snap := typeReading(s, "ㄐㄧˋ ㄏㄨㄚˋ")
		if snap.Text() != "計畫" {
			t.Fatalf("組字區照樣是繁體：%q", snap.Text())
		}
		snap = key(s, proto.KeyDown)
		if snap.Candidates == nil || snap.Candidates.Items[0].Text != "計畫" {
			t.Fatalf("候選照樣是繁體：%+v", snap.Candidates)
		}
		key(s, proto.KeyEscape)
		want := map[bool]string{false: "計畫", true: "计划"}[on]
		if got := key(s, proto.KeyEnter).Commit; got != want {
			t.Fatalf("開關 %v：送出 %q，預期 %q", on, got, want)
		}
		// 學習記的是繁體：之後的選字、個人詞庫與前文都不會出現簡體。
		if s.model.Store.Uni["計畫"] == 0 || s.model.Store.Uni["计划"] != 0 {
			t.Fatalf("開關 %v：學習應記繁體：%v", on, s.model.Store.Uni)
		}
		if s.pendingLeft != "計畫" || s.pendingNeural != "計畫" {
			t.Fatalf("開關 %v：待確認的前文應是繁體：%q %q", on, s.pendingLeft, s.pendingNeural)
		}
	}
}

// 讀音決定寫法的字：「著」讀 ㄓㄨˋ 留「著」（OpenCC 只看字會把「土著」「原著」轉成「土着」「原着」），
// 「乾」讀 ㄑㄧㄢˊ 留「乾」，「沈」讀 ㄔㄣˊ 是「沉」，「瞭」依讀音是「了」或「瞭」；其餘照 OpenCC。
func TestSimplifiedOutputUsesTheTypedReading(t *testing.T) {
	for _, tc := range []struct {
		reading, traditional, want, opencc string
	}{
		{"ㄊㄨˇ ㄓㄨˋ", "土著", "土著", "土着"},
		{"ㄩㄢˊ ㄓㄨˋ", "原著", "原著", "原着"},
		{"ㄒㄧㄢˇ ㄓㄨˋ", "顯著", "显著", "显著"},
		{"ㄒㄧㄢˇ ㄓㄜ˙", "顯著", "显著", "显著"}, // 不是「显着」
		{"ㄎㄢˋ ㄓㄜ˙", "看著", "看着", "看着"},
		{"ㄑㄧㄢˊ ㄢ", "乾安", "乾安", "干安"},
		{"ㄍㄢ ㄐㄧㄥˋ", "乾淨", "干净", "干净"},
		{"ㄒㄧㄚˋ ㄔㄣˊ", "下沈", "下沉", "下沈"},
		{"ㄇㄧㄥˊ ㄌㄧㄠˇ", "明瞭", "明了", "明了"},
		{"ㄌㄧㄠˋ ㄨㄤˋ", "瞭望", "瞭望", "瞭望"},
	} {
		s, conv := simplifiedSession(t, true)
		if snap := typeReading(s, tc.reading); snap.Text() != tc.traditional {
			t.Fatalf("%s 測試前提：組字 %q", tc.reading, snap.Text())
		}
		if got := conv.Convert(tc.traditional); got != tc.opencc {
			t.Fatalf("%s 測試前提：OpenCC 轉成 %q", tc.traditional, got)
		}
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%s（%s）送出 %q，預期 %q", tc.traditional, tc.reading, got, tc.want)
		}
	}
}

// 補標點在轉換之前：模型看到的是繁體，插入標點後讀音指定的位置跟著移動。
func TestSimplifiedOutputAfterPunctuation(t *testing.T) {
	s, _ := simplifiedSession(t, true)
	var seen string
	s.Punct = punctFunc(func(text string) ([]punct.Edit, error) {
		seen = text
		return []punct.Edit{{Offset: 2, Text: "，"}}, nil
	})
	typeReading(s, "ㄐㄧˋ ㄏㄨㄚˋ ㄊㄨˇ ㄓㄨˋ")
	if got := key(s, proto.KeyEnter).Commit; got != "计划，土著" || seen != "計畫土著" {
		t.Fatalf("送出 %q，補標點看到 %q", got, seen)
	}
}

// 英文、數字、全形標點、注音符號不在字典裡，照原樣送出。
func TestSimplifiedOutputKeepsNonChinese(t *testing.T) {
	s, _ := simplifiedSession(t, true)
	typeReading(s, "ㄐㄧˋ ㄏㄨㄚˋ")
	key(s, "<") // 全形逗號
	s.Handle(proto.Request{Type: proto.Mode, On: true})
	for _, k := range "OK 123" {
		key(s, string(k))
	}
	s.Handle(proto.Request{Type: proto.Mode, On: false})
	if snap := s.snapshot(); snap.Text() != "計畫，OK 123" {
		t.Fatalf("測試前提：%q", snap.Text())
	}
	if got := key(s, proto.KeyEnter).Commit; got != "计划，OK 123" {
		t.Fatalf("送出 %q", got)
	}
	// 未打完的注音照原樣。
	typeSyl(s, "ㄨㄛˇ")
	key(s, "w") // ㄊ（標準排列 w）
	if got := key(s, proto.KeyEnter).Commit; got != "我ㄊ" {
		t.Fatalf("送出 %q", got)
	}
}

// 我的詞彙與我的片語也跟著轉；Emoji 不變。
func TestSimplifiedOutputConvertsVocabAndPhrases(t *testing.T) {
	s, _ := simplifiedSession(t, true)
	if _, err := s.model.VocabAdd([]vocab.Entry{{Text: "臺灣大學", Readings: []string{"ㄊㄞˊ ㄨㄢ ㄉㄚˋ ㄒㄩㄝˊ"}}}, vocab.SourceManual); err != nil {
		t.Fatal(err)
	}
	if snap := typeReading(s, "ㄊㄞˊ ㄨㄢ ㄉㄚˋ ㄒㄩㄝˊ"); snap.Text() != "臺灣大學" {
		t.Fatalf("測試前提：%q", snap.Text())
	}
	if got := key(s, proto.KeyEnter).Commit; got != "台湾大学" {
		t.Fatalf("我的詞彙送出 %q", got)
	}
	if s.model.Store.Uni["臺灣大學"] == 0 {
		t.Fatalf("我的詞彙照樣以繁體學習：%v", s.model.Store.Uni)
	}

	if _, err := s.model.Vocab.AddPhrases([]string{"謝謝您的來信", "第一行\n請參考附件"}, vocab.SourceManual, 0); err != nil {
		t.Fatal(err)
	}
	key(s, "=")
	snap := key(s, proto.KeyBackTab)
	if snap.Candidates == nil || snap.Candidates.Kind != "phrases" || snap.Candidates.Items[0].Text != "謝謝您的來信" {
		t.Fatalf("片語清單照樣是繁體：%+v", snap.Candidates)
	}
	if got := key(s, "1").Commit; got != "谢谢您的来信" {
		t.Fatalf("沒有組字時選片語直接送出 %q", got)
	}
	typeSyl(s, "ㄨㄛˇ")
	key(s, "=") // 上次選片語：開在我的片語
	if got := key(s, "2").Commit; got != "我第一行\n请参考附件" {
		t.Fatalf("含換行的片語連同組字送出 %q", got)
	}

	key(s, "=")
	snap = key(s, proto.KeyTab) // 片語 → Emoji
	if snap.Candidates == nil || snap.Candidates.Kind != "emoji" {
		t.Fatalf("%+v", snap.Candidates)
	}
	emoji := snap.Candidates.Items[1].Text
	if got := key(s, "2").Commit; got != emoji {
		t.Fatalf("Emoji 送出 %q，預期 %q", got, emoji)
	}
}
