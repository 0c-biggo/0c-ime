package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/vocab"
)

// #124：= 開在上次真的選了東西的分頁；Tab 過去看看、Esc 取消不算。Shift＋Tab 往回，頭尾相接。
func TestPaletteRemembersChosenTabOnly(t *testing.T) {
	s := paletteSession()
	if snap := key(s, "="); snap.Candidates.Kind != "emoji" || snap.Candidates.Title != "Tab → 顏文字" {
		t.Fatalf("第一次開在 Emoji：%+v", snap.Candidates)
	}
	key(s, proto.KeyTab)
	key(s, proto.KeyEscape)
	if snap := key(s, "="); snap.Candidates.Kind != "emoji" {
		t.Fatalf("只是 Tab 過去看看不算：%q", snap.Candidates.Kind)
	}
	if snap := key(s, proto.KeyBackTab); snap.Candidates.Kind != "kaomoji" || snap.Candidates.Title != "Tab → Emoji" {
		t.Fatalf("Shift＋Tab 往回、兩頁循環：%+v", snap.Candidates)
	}
	if snap := key(s, "1"); !snap.PersonalChanged || s.model.Store.PaletteTab != "kaomoji" {
		t.Fatalf("選了顏文字要記住分頁並存檔：%+v %q", snap, s.model.Store.PaletteTab)
	}
	key(s, proto.KeyEnter)
	if snap := key(s, "="); snap.Candidates.Kind != "kaomoji" {
		t.Fatalf("下次開在顏文字：%q", snap.Candidates.Kind)
	}
}

// 私人欄位或暫停學習時不記住分頁，也不改表情順序。
func TestPaletteTabNotRememberedWhenPaused(t *testing.T) {
	s := paletteSession()
	s.paused = true
	key(s, "=")
	key(s, proto.KeyTab)
	key(s, "1")
	if s.model.Store.PaletteTab != "" || len(s.model.Store.EmojiRecent) != 0 {
		t.Fatalf("%q %v", s.model.Store.PaletteTab, s.model.Store.EmojiRecent)
	}
}

// 「我的片語」分頁只在 0.2.0 開關打開、而且有片語時出現；三頁循環。長片語截斷顯示、插入完整內容；
// 含換行的片語直接提交。
func TestPalettePhrasesTab(t *testing.T) {
	s := paletteSession()
	if _, err := s.model.VocabAdd([]vocab.Entry{{Text: "世傑", Readings: []string{"ㄕˋ ㄐㄧㄝˊ"}}}, vocab.SourceManual); err != nil {
		t.Fatal(err)
	}
	long := "台北市信義區市府路一號，靈犀注音客服團隊收，謝謝您的來信"
	if _, err := s.model.Vocab.AddPhrases([]string{"——", long, "第一行\n第二行"}, vocab.SourceManual, 0); err != nil {
		t.Fatal(err)
	}
	key(s, "=")
	if snap := key(s, proto.KeyBackTab); snap.Candidates.Kind != "kaomoji" {
		t.Fatalf("開關關閉：只有兩頁：%q", snap.Candidates.Kind)
	}
	key(s, proto.KeyEscape)

	s.Preview02 = true
	key(s, "=")
	snap := key(s, proto.KeyBackTab)
	if snap.Candidates.Kind != "phrases" || snap.Candidates.Title != "Tab → Emoji" || snap.Candidates.Columns != 0 {
		t.Fatalf("三頁循環，Shift＋Tab 從 Emoji 到我的片語：%+v", snap.Candidates)
	}
	items := snap.Candidates.Items
	if len(items) != 3 || items[0].Text != "——" || items[1].Text == long || []rune(items[1].Text)[23] != '…' || items[2].Text != "第一行⏎第二行" || items[0].ID != phraseIDBase+1 {
		t.Fatalf("片語顯示：%+v", items)
	}
	key(s, proto.KeyEscape)
	typeSyl(s, "ㄨㄛˇ")
	key(s, "=") // 還沒選過：開在 Emoji
	key(s, proto.KeyBackTab)
	if snap := key(s, "2"); snap.Text() != "我"+long || s.model.Store.PaletteTab != "phrases" {
		t.Fatalf("插入完整片語：%q %q", snap.Text(), s.model.Store.PaletteTab)
	}
	if len(s.model.Store.EmojiRecent) != 0 {
		t.Fatal("片語不進表情排序")
	}
	key(s, proto.KeyEnter)
	typeSyl(s, "ㄨㄛˇ")
	key(s, "=") // 上次選片語：開在我的片語
	if snap := key(s, "3"); snap.Commit != "我第一行\n第二行" {
		t.Fatalf("含換行的片語連同組字直接提交：%+v", snap)
	}
	// 片語刪光或開關關掉：記住的分頁不存在時開 Emoji。
	s.Preview02 = false
	if snap := key(s, "="); snap.Candidates.Kind != "emoji" {
		t.Fatalf("%q", snap.Candidates.Kind)
	}
}
