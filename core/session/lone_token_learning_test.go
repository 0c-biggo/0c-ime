package session

import (
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// #328：macOS 的 LINE 登入畫面，密碼欄沒標成密碼欄、也沒開安全輸入，按鍵照常進輸入法（.12 實機確認，
// 欄位提示「請切換鍵盤以英文輸入」）。每天登入打一次密碼，三天後補字候選就原樣列出整串密碼，分享螢幕時外洩。
// 密碼、帳號是單獨打的一串：整段組字只有這一串，打完按 Enter、Tab 或點別處（外殼送 commit）送出。這種送出不學。
func TestLoneTokenIsNotLearned(t *testing.T) {
	ends := map[string]func(*Session){
		"Enter":    func(s *Session) { key(s, proto.KeyEnter) },
		"Tab":      func(s *Session) { key(s, proto.KeyTab) },
		"點別處":      func(s *Session) { s.Handle(proto.Request{Type: proto.Commit}) },
		"標點":       func(s *Session) { key(s, "!") },
		"空白再Enter": func(s *Session) { key(s, " "); key(s, proto.KeyEnter) },
	}
	modes := map[string]func() *Session{
		"中英混打":      mixedSession,
		"英文模式補字":    autocompleteSession,
		"英文模式補字＋混打": func() *Session { s := autocompleteSession(); s.MixedInput = true; return s },
	}
	for _, pw := range []string{"steventest", "Passw0rd", "steven1990", "Ab12cd34", "MyPass_99", "qwerty"} {
		for mode, start := range modes {
			for end, finish := range ends {
				if mode != "中英混打" && end == "空白再Enter" {
					continue // 英文模式按空白就送出了，後面還有字，見下一個測試
				}
				s := start()
				for day := 100; day < 104; day++ {
					s.model.AdvanceTime(int64(day) * englishDay)
					for range 2 {
						mixedType(s, pw)
						finish(s)
						key(s, proto.KeyEnter) // 送出登入；沒在組字時交給 App
					}
				}
				if len(s.model.Store.English) != 0 {
					t.Errorf("%s %s %q：單獨打的一串學起來了：%+v", mode, end, pw, s.model.Store.English)
				}
				snap := mixedType(s, pw[:4])
				if snap.SuggestionEdit != nil && snap.SuggestionEdit.Text == pw || slices.Contains(snapCandidates(snap), pw) {
					t.Errorf("%s %s %q：打前綴就列出整串：%+v", mode, end, pw, snap)
				}
				s.Handle(proto.Request{Type: proto.Cancel})
			}
		}
	}
}

// 同樣的字出現在句子裡照常學（#247 的 rclone、pCloud，混打的 newWidget_52）：跟中文或別的詞同一段送出，
// 或英文模式按空白接著打。門檻不變：三次、跨兩天、每天最多兩次。
func TestTokenInSentenceIsStillLearned(t *testing.T) {
	cases := []struct {
		mode  string
		start func() *Session
		typed string // 打完再按 Enter
		word  string
	}{
		{"混打接中文", mixedSession, "newWidget_52 ji3", "newWidget_52"},
		{"混打中文在前", mixedSession, "ji3 newWidget_52", "newWidget_52"},
		{"混打兩個詞", mixedSession, "rclone sync", "rclone"},
		{"英文模式按空白", autocompleteSession, "pCloud ", "pCloud"},
		{"英文模式句尾前", autocompleteSession, "use LingxiCorn now", "LingxiCorn"},
	}
	for _, tc := range cases {
		s := tc.start()
		for day := 100; day < 102; day++ {
			s.model.AdvanceTime(int64(day) * englishDay)
			for range 2 {
				mixedType(s, tc.typed)
				key(s, proto.KeyEnter)
				key(s, proto.KeyEnter)
			}
		}
		if !s.model.EnglishKnown(tc.word) {
			t.Errorf("%s：%q 在句子裡打了四次（兩天）還沒學會：%+v", tc.mode, tc.word, s.model.Store.English)
		}
		prefix := tc.word[:4]
		snap := mixedType(s, prefix)
		if !(snap.SuggestionEdit != nil && snap.SuggestionEdit.Text == tc.word) && !slices.ContainsFunc(snapCandidates(snap), func(c string) bool { return strings.EqualFold(c, tc.word) }) {
			t.Errorf("%s：學會後打 %q 沒有建議 %q：%+v", tc.mode, prefix, tc.word, snap)
		}
		s.Handle(proto.Request{Type: proto.Cancel})
	}
}
