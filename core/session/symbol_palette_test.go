package session

import (
	"slices"
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/symbols"
)

// #290：反引號符號選單分頁、組字中按 ↑ 叫出選單並定位、第一頁是最近使用的符號。

func symbolTab(s *Session) string {
	if s.cand == nil {
		return ""
	}
	return s.cand.symbols
}

// 原本平的符號選單（↓ 的各組）裡的每個符號，分頁後都還找得到。
func TestSymbolMenuKeepsEveryGroupSymbol(t *testing.T) {
	for _, group := range symbolGroups {
		for _, item := range group {
			if !symbols.Contains(item) {
				t.Errorf("符號選單少了 %q", item)
			}
		}
	}
}

func TestSymbolMenuTabs(t *testing.T) {
	s := newSession()
	snap := key(s, "`")
	if !snap.Handled || symbolTab(s) != "標點" || snap.Candidates == nil || snap.Candidates.Title != "標點 · Tab → 括號引號" {
		t.Fatalf("沒有最近使用時從標點開：%q %+v", symbolTab(s), snap.Candidates)
	}
	if snap.Candidates.Kind != "" || snap.Candidates.Items[0].Text != "，" || snap.Candidates.Items[0].Key != "1" {
		t.Fatalf("沿用一般候選的版面與鍵標：%+v", snap.Candidates)
	}
	var seen []string
	for range symbols.Tabs {
		seen = append(seen, symbolTab(s))
		key(s, proto.KeyTab)
	}
	want := []string{"標點", "括號引號", "數學", "箭頭", "圖形", "編號", "注音"}
	if !slices.Equal(seen, want) || symbolTab(s) != "標點" {
		t.Fatalf("Tab 依序換分頁、頭尾相接：%v → %q", seen, symbolTab(s))
	}
	if snap = key(s, proto.KeyBackTab); symbolTab(s) != "注音" || snap.Candidates.Title != "注音 · Tab → 標點" {
		t.Fatalf("Shift＋Tab 往前：%q %+v", symbolTab(s), snap.Candidates)
	}
	// 分頁內照舊翻頁；換分頁從第一頁開始。
	key(s, proto.KeyPageDown)
	if snap = key(s, proto.KeyTab); snap.Candidates.Page != 0 || snap.Candidates.Highlight != 0 {
		t.Fatalf("換分頁回到第一頁：%+v", snap.Candidates)
	}
	if snap = pickText(t, s, "；"); snap.Text() != "；" || snap.Commit != "" || snap.Candidates != nil {
		t.Fatalf("標點分頁選 ；：%+v", snap)
	}
	if s.model.Store.Total != 0 || len(s.model.Store.English) != 0 {
		t.Fatal("選符號不學詞")
	}
}

// 未完成的注音按 ` 開選單，換分頁後選取照樣替換它；Esc 保留原注音。
func TestSymbolMenuTabsKeepPendingReplacement(t *testing.T) {
	s := newSession()
	typeSyl(s, "ㄨㄛˇ")
	key(s, "j") // ㄨ 未完成
	keys(s, "`", proto.KeyTab)
	if s.cand == nil || !s.cand.pending || s.cand.start != 1 || s.cand.length != 0 {
		t.Fatalf("換分頁保留替換的對象：%+v", s.cand)
	}
	if snap := pickText(t, s, "「"); snap.Text() != "我「" {
		t.Fatalf("選取替換未完成的注音：%q", snap.Text())
	}
	key(s, "j")
	keys(s, "`", proto.KeyTab, proto.KeyTab)
	if snap := key(s, proto.KeyEscape); snap.Text() != "我「ㄨ" || snap.Candidates != nil {
		t.Fatalf("Esc 保留原注音：%q", snap.Text())
	}
}

// 組字中游標在符號上按 ↑：開完整符號選單，停在它的分類並反白它，選了就替換它；↓ 照舊是同組變體。
func TestUpOpensSymbolMenuAtSymbol(t *testing.T) {
	cases := []struct {
		keys   []string
		pre    string // 開了候選之後先選這一項
		symbol string
		tab    string
		pick   string
		want   string
	}{
		{[]string{"<"}, "", "，", "標點", "；", "我；"},
		{[]string{"^"}, "", "……", "標點", "～", "我～"}, // 兩個字元的符號整個換掉
		{[]string{"]"}, "", "」", "括號引號", "】", "我】"},
		{[]string{"_", proto.KeyDown}, "→", "→", "箭頭", "⇒", "我⇒"},
		{[]string{"`", proto.KeyTab, proto.KeyTab}, "÷", "÷", "數學", "×", "我×"},
		{[]string{"`", proto.KeyBackTab}, "ㄅ", "ㄅ", "注音", "ㄆ", "我ㄆ"},
	}
	for _, c := range cases {
		s := newSession()
		typeSyl(s, "ㄨㄛˇ")
		keys(s, c.keys...)
		if c.pre != "" {
			pickText(t, s, c.pre)
		}
		snap := key(s, proto.KeyUp)
		if !snap.Handled || symbolTab(s) != c.tab || snap.Candidates == nil {
			t.Fatalf("%q 上按 ↑ 應開 %s 分頁：%q %+v", c.symbol, c.tab, symbolTab(s), snap.Candidates)
		}
		cl := snap.Candidates
		if cl.Items[cl.Highlight].Text != c.symbol || cl.Length != len([]rune(c.symbol)) || cl.Title == "" {
			t.Fatalf("%q：反白它、替換它：%+v", c.symbol, cl)
		}
		for symbolTab(s) != symbols.Tabs[0].Name {
			key(s, proto.KeyTab) // 換分頁照樣替換同一個符號
		}
		for _, tab := range symbols.Tabs {
			if slices.Contains(tab.Items, c.pick) {
				for symbolTab(s) != tab.Name {
					key(s, proto.KeyTab)
				}
			}
		}
		if snap = pickText(t, s, c.pick); snap.Text() != c.want || snap.Cursor.Rune != len([]rune(c.want)) || snap.Commit != "" {
			t.Fatalf("%q 換成 %q：%+v", c.symbol, c.pick, snap)
		}
	}

	// Esc 保留原符號；換分頁再 Esc 也一樣。
	s := newSession()
	typeSyl(s, "ㄨㄛˇ")
	keys(s, "^", proto.KeyUp, proto.KeyTab)
	if snap := key(s, proto.KeyEscape); snap.Text() != "我……" || snap.Candidates != nil || len(s.model.Store.SymbolRecent) != 0 {
		t.Fatalf("Esc 保留原符號、不記：%+v", snap)
	}

	// ↓ 照舊是同組變體。
	s = newSession()
	typeSyl(s, "ㄨㄛˇ")
	key(s, "<")
	if snap := key(s, proto.KeyDown); symbolTab(s) != "" || snap.Candidates.Items[0].Text != "，" || len(snap.Candidates.Items) > 10 {
		t.Fatalf("↓ 是同組變體：%+v", snap.Candidates)
	}
}

func TestUpOutsideSymbolsUnchanged(t *testing.T) {
	s := newSession()
	if snap := key(s, proto.KeyUp); snap.Handled || snap.Candidates != nil {
		t.Fatalf("沒有組字時 ↑ 交還應用程式：%+v", snap)
	}
	typeSyl(s, "ㄨㄛˇ")
	if snap := key(s, proto.KeyUp); !snap.Handled || snap.Candidates != nil || snap.Text() != "我" {
		t.Fatalf("游標在中文上 ↑ 吃掉不動作（#106）：%+v", snap)
	}
	key(s, "1") // ㄅ 未完成：不是符號
	if snap := key(s, proto.KeyUp); snap.Candidates != nil {
		t.Fatalf("注音打到一半 ↑ 不開選單：%+v", snap.Candidates)
	}
	// 英文模式照舊。
	s = newSession()
	typeSyl(s, "ㄨㄛˇ")
	key(s, "<")
	s.Handle(proto.Request{Type: proto.Mode, On: true})
	if snap := key(s, proto.KeyUp); snap.Candidates != nil {
		t.Fatalf("英文模式 ↑ 不開符號選單：%+v", snap.Candidates)
	}
	// ↑ 換候選的輸入風格照舊。
	s = styleSession("microsoft")
	typeSyl(s, "ㄨㄛˇ")
	key(s, "<")
	key(s, proto.KeyUp)
	if symbolTab(s) != "" {
		t.Fatal("↑ 換候選的風格不開符號選單")
	}
}

// 第一頁是最近使用的符號；排序同表情（常用＋最近），只放一頁；↑ 定位到符號自己的分類。
func TestSymbolMenuRecentTab(t *testing.T) {
	s := newSession()
	keys(s, "`", proto.KeyTab, proto.KeyTab, proto.KeyTab, proto.KeyTab) // 圖形
	if snap := pickText(t, s, "※"); !snap.PersonalChanged || !slices.Equal(s.model.Store.SymbolRecent, []string{"※"}) {
		t.Fatalf("選了符號要記下並存檔：%+v %v", snap.PersonalChanged, s.model.Store.SymbolRecent)
	}
	snap := key(s, "`")
	if symbolTab(s) != symbols.Recent || snap.Candidates.Title != "最近 · Tab → 標點" || len(snap.Candidates.Items) != 1 || snap.Candidates.Items[0].Text != "※" {
		t.Fatalf("有紀錄時從最近開：%q %+v", symbolTab(s), snap.Candidates)
	}
	// 最近分頁的候選 ID 與它在分類裡相同：選取不受順序影響。
	if snap = s.Handle(proto.Request{Type: proto.Select, Index: symbols.ID("※")}); snap.Text() != "※※" {
		t.Fatalf("依穩定 ID 選取：%+v", snap)
	}
	if snap = key(s, proto.KeyUp); symbolTab(s) != "圖形" {
		t.Fatalf("↑ 定位到符號的分類，不是最近：%q", symbolTab(s))
	}
	key(s, proto.KeyEscape)
	key(s, proto.KeyEnter)
	// 有上限：選了很多種之後最近只放一頁。
	for _, item := range symbols.Tabs[0].Items {
		key(s, "`")
		for symbolTab(s) != "標點" {
			key(s, proto.KeyTab)
		}
		pickText(t, s, item)
	}
	if snap = key(s, "`"); len(s.model.Store.SymbolRecent) != symbols.MaxRecent || len(snap.Candidates.Items) != symbols.MaxRecent || snap.Candidates.Pages != 1 {
		t.Fatalf("最近只放一頁：%v %+v", s.model.Store.SymbolRecent, snap.Candidates)
	}
	if s.model.Store.Total != 0 || len(s.model.Store.English) != 0 || len(s.model.Store.EmojiRecent) != 0 {
		t.Fatal("最近使用的符號不碰學習與表情")
	}
}

// 從 ↓ 換的同組變體不算選單選取，不記。
func TestSymbolVariantNotRecent(t *testing.T) {
	s := newSession()
	key(s, "<")
	key(s, proto.KeyDown)
	if snap := pickText(t, s, "、"); snap.Text() != "、" || len(s.model.Store.SymbolRecent) != 0 || snap.PersonalChanged {
		t.Fatalf("↓ 的變體不記：%v", s.model.Store.SymbolRecent)
	}
}

// 暫停學習時不記；私人欄位不記也不顯示最近分頁。
func TestSymbolRecentPausedAndPrivate(t *testing.T) {
	s := newSession()
	s.model.RememberSymbol("※")
	s.Handle(proto.Request{Type: proto.Pause, On: true})
	if key(s, "`"); symbolTab(s) != symbols.Recent {
		t.Fatalf("暫停學習照樣列出最近：%q", symbolTab(s))
	}
	key(s, proto.KeyTab)
	if snap := pickText(t, s, "，"); snap.Text() != "，" || !slices.Equal(s.model.Store.SymbolRecent, []string{"※"}) || snap.PersonalChanged {
		t.Fatalf("暫停學習不記：%v", s.model.Store.SymbolRecent)
	}

	s = newSession()
	s.model.RememberSymbol("※")
	snap := s.Handle(proto.Request{Type: proto.Key, Key: "`", Private: true})
	if symbolTab(s) != "標點" || snap.Candidates.Title != "標點 · Tab → 括號引號" {
		t.Fatalf("私人欄位不列最近：%q %+v", symbolTab(s), snap.Candidates)
	}
	snap = s.Handle(proto.Request{Type: proto.Select, Index: symbols.ID("，"), Private: true})
	if snap.Text() != "，" || !slices.Equal(s.model.Store.SymbolRecent, []string{"※"}) || snap.PersonalChanged {
		t.Fatalf("私人欄位不記：%v", s.model.Store.SymbolRecent)
	}

	// 最近分頁開著時進入私人欄位：關掉，不讓個人資料跨進去。
	s = newSession()
	s.model.RememberSymbol("※")
	key(s, "`")
	if snap = s.Handle(proto.Request{Type: proto.Key, Key: proto.KeyTab, Private: true}); snap.Candidates != nil {
		t.Fatalf("最近分頁進入私人欄位要關：%+v", snap.Candidates)
	}
}
