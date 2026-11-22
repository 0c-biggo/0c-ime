package session

import (
	"bytes"
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/rerank"
)

// savedStore 是個人資料存成 user.json 的內容：比較前後是否一字不差。
func savedStore(t *testing.T, s *Session) string {
	t.Helper()
	var b bytes.Buffer
	if err := s.model.Store.Save(&b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// pickJing 打「ㄐㄧㄥˇ」、手選「井」（系統機率較低）再送出，回傳送出的字。
func pickJing(t *testing.T, s *Session) string {
	t.Helper()
	typeSyl(s, "ㄐㄧㄥˇ")
	selectText(t, s, 0, "井")
	return key(s, proto.KeyEnter).Commit
}

// #297：關掉「記住我選的字詞」時，手選、送出都不改個人資料；打開時照舊學。
func TestNoLearningLeavesPersonalDataUnchanged(t *testing.T) {
	s := newSession()
	typeSyl(s, "ㄨㄛˇ")
	key(s, proto.KeyEnter) // 先學一點，個人資料不是空的
	before := savedStore(t, s)

	s.NoLearning = true
	if got := pickJing(t, s); got != "井" {
		t.Fatalf("關閉學習照常送出手選的字：%q", got)
	}
	typeSyl(s, "ㄈㄥ")
	typeSyl(s, "ㄐㄧㄥˇ")
	key(s, proto.KeyEnter)
	if after := savedStore(t, s); after != before {
		t.Fatalf("關閉學習時個人資料被改了：\n前 %s\n後 %s", before, after)
	}

	s.NoLearning = false
	pickJing(t, s)
	if savedStore(t, s) == before || s.model.Store.Uni["井"] == 0 {
		t.Fatal("打開學習後要照常學")
	}
}

// 已經學到的照常參與排序（同新酷音 AUTOLEARN_DISABLED：只停寫入，個人詞庫照用）。
func TestNoLearningStillRanksWithLearnedData(t *testing.T) {
	s := newSession()
	if got := typeSyl(s, "ㄐㄧㄥˇ").Text(); got != "景" {
		t.Fatalf("前提：系統預設是「景」：%q", got)
	}
	key(s, proto.KeyEscape)
	key(s, proto.KeyEscape)
	pickJing(t, s)
	pickJing(t, s)
	if got := typeSyl(s, "ㄐㄧㄥˇ").Text(); got != "井" {
		t.Fatalf("前提：學過以後是「井」：%q", got)
	}
	key(s, proto.KeyEscape)
	key(s, proto.KeyEscape)
	s.NoLearning = true
	if got := typeSyl(s, "ㄐㄧㄥˇ").Text(); got != "井" {
		t.Fatalf("關閉學習仍用已學到的：%q", got)
	}
}

// 關閉學習不是暫停學習：神經選字、依前文選字照常（暫停學習與私人欄位才停掉推論與前文）。
func TestNoLearningKeepsNeuralSelectionAndContext(t *testing.T) {
	calls := 0
	s := neuralSession(&calls)
	s.NoLearning = true
	typeSyl(s, "ㄨㄛˇ")
	if snap := typeSyl(s, "ㄐㄧㄥˇ"); snap.Text() != "我井" || calls != 1 {
		t.Fatalf("關閉學習時神經選字照常：%q calls=%d", snap.Text(), calls)
	}
	if got := key(s, proto.KeyEnter).Commit; got != "我井" {
		t.Fatalf("送出 %q", got)
	}
	if st := s.model.Store; st.Total != 0 || len(st.Uni)+len(st.Bi)+len(st.Corrections) != 0 {
		t.Fatalf("神經選字的結果不學：%+v", st)
	}

	var ctx []string
	s = contextNeuralSession(&ctx, rerank.MaxContextRunes)
	s.NoLearning = true
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄐㄧㄥˇ")
	c := key(s, proto.KeyEnter)
	if c.ContextID == 0 {
		t.Fatalf("關閉學習照常記前文：%+v", c)
	}
	s.Handle(proto.Request{Type: proto.Context, On: true, ContextID: c.ContextID})
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄐㄧㄥˇ")
	if len(ctx) == 0 || ctx[len(ctx)-1] != c.Commit {
		t.Fatalf("關閉學習時前文照常給神經選字：%q，要 %q", ctx, c.Commit)
	}
	if s.model.Store.Total != 0 {
		t.Fatal("關閉學習時不學")
	}
}

// 英文自動補字照常列建議，只是送出的英文單字不記；中英混打的英文、選過的注音文也不記。
func TestNoLearningSkipsEnglishWords(t *testing.T) {
	for _, off := range []bool{false, true} {
		s := autocompleteSession()
		s.NoLearning = off
		s.model.AdvanceTime(100 * englishDay)
		if snap := mixedType(s, "hel"); snap.Candidates == nil || snap.Candidates.Items[0].Text != "hello" {
			t.Fatalf("關閉 %v：照常列補字建議：%+v", off, snap)
		}
		key(s, proto.KeyEscape)
		for i := 0; i < 3; i++ {
			mixedType(s, "zzqword ")
			key(s, proto.KeyEnter)
		}
		if got := len(s.model.Store.English) != 0; got == off {
			t.Fatalf("關閉 %v：英文單字紀錄 %+v", off, s.model.Store.English)
		}
	}
	for _, off := range []bool{false, true} {
		s := mixedSession()
		s.NoLearning = off
		s.model.AdvanceTime(100 * englishDay)
		for i := 0; i < 3; i++ {
			mixedType(s, "newWidget_52 newWidget_52")
			key(s, proto.KeyEnter)
		}
		if got := len(s.model.Store.English) != 0; got == off {
			t.Fatalf("混打，關閉 %v：英文單字紀錄 %+v", off, s.model.Store.English)
		}
	}
}

// 「最近」的表情、符號與 = 選單上次的分頁也是打字留下的紀錄，關閉時不記；選了照常插入。
func TestNoLearningSkipsPaletteAndSymbolHistory(t *testing.T) {
	s := paletteSession()
	s.NoLearning = true
	key(s, "=")
	key(s, proto.KeyTab)
	if snap := key(s, "1"); snap.PersonalChanged || snap.Text()+snap.Commit == "" {
		t.Fatalf("選了顏文字照常插入、不要求存檔：%+v", snap)
	}
	if s.model.Store.PaletteTab != "" || len(s.model.Store.EmojiRecent) != 0 {
		t.Fatalf("%q %v", s.model.Store.PaletteTab, s.model.Store.EmojiRecent)
	}

	s = newSession()
	s.NoLearning = true
	keys(s, "`", proto.KeyTab, proto.KeyTab, proto.KeyTab, proto.KeyTab) // 圖形
	if snap := pickText(t, s, "※"); snap.PersonalChanged || snap.Text() != "※" {
		t.Fatalf("選了符號照常插入、不要求存檔：%+v", snap)
	}
	if len(s.model.Store.SymbolRecent) != 0 {
		t.Fatalf("最近用過的符號不記：%v", s.model.Store.SymbolRecent)
	}
}
