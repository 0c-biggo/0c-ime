package personal

import (
	"bytes"
	"fmt"
	"github.com/0c-biggo/0c-ime/core/emoji"
	"reflect"
	"testing"
)

func TestEmojiOrderBoundMigrationAndRoundtrip(t *testing.T) {
	for _, format := range []int{0, 1, 2, 3, 4} {
		s, err := LoadStore(bytes.NewBufferString(fmt.Sprintf(`{"Format":%d,"Uni":{"保留":2},"EmojiRecent":["🇹🇼","😀","🇹🇼","arbitrary text"]}`, format)))
		if err != nil || !reflect.DeepEqual(s.EmojiRecent, []string{"🇹🇼", "😀"}) || s.Uni["保留"] != 2 {
			t.Fatalf("migration %d: %v %+v", format, err, s)
		}
		m := &Model{Store: s}
		for _, text := range emoji.Palette("emoji", nil) {
			m.RememberEmoji(text)
		}
		if len(s.EmojiRecent) != emoji.MaxRecent {
			t.Fatalf("bounded recent=%d", len(s.EmojiRecent))
		}
		m.RememberEmoji("🇹🇼")
		m.RememberEmoji("🇹🇼")
		m.RememberEmoji("unknown")
		if s.EmojiRecent[0] != "🇹🇼" || len(s.EmojiRecent) != emoji.MaxRecent || len(s.English) != 0 || s.Uni["保留"] != 2 {
			t.Fatal("recent order contaminated learning")
		}
		var b bytes.Buffer
		if err := s.Save(&b); err != nil {
			t.Fatal(err)
		}
		loaded, err := LoadStore(&b)
		if err != nil || loaded.Format != StoreFormat || !reflect.DeepEqual(loaded, s) {
			t.Fatalf("roundtrip: %v", err)
		}
	}
}

// 格式 4 的最近清單升級成常用分數：順序不變，新選一次不會讓位置洗牌（#124）。
func TestEmojiRecentUpgradeKeepsOrder(t *testing.T) {
	s, err := LoadStore(bytes.NewBufferString(`{"Format":4,"EmojiRecent":["😂","👍","🙏"]}`))
	if err != nil || !reflect.DeepEqual(s.EmojiRecent, []string{"😂", "👍", "🙏"}) || len(s.EmojiStats) != 3 {
		t.Fatalf("%v %+v", err, s)
	}
	m := &Model{Store: s, Now: func() int64 { return 1e9 }}
	m.RememberEmoji("🎉")
	m.RememberEmoji("🙏")
	if !reflect.DeepEqual(s.EmojiRecent, []string{"😂", "👍", "🙏", "🎉"}) {
		t.Fatalf("升級後選一兩次不洗牌：%v", s.EmojiRecent)
	}
	m.RememberPaletteTab("kaomoji")
	var b bytes.Buffer
	s.Save(&b)
	back, err := LoadStore(&b)
	if err != nil || back.PaletteTab != "kaomoji" || !reflect.DeepEqual(back.EmojiStats, s.EmojiStats) {
		t.Fatalf("%v %+v", err, back)
	}
}
