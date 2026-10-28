package session

import (
	"slices"
	"testing"

	"github.com/0c-biggo/0c-ime/core/english"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// #146：句首、空白後、英文後的 u1,6（ㄧㄅㄝˊ，反序打的 ㄅㄧㄝˊ 別）、u2,6（ㄉㄧㄝˊ 疊）照 #200 的
// 「英文接數字寫法」照字面顯示；↓ 除了原按鍵與原樣注音，還要有完整讀音的中文字，選了就是那個字。
func mixedReadingSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"我", "ㄨㄛˇ", 1e-2}, {"折", "ㄓㄜˊ", 1e-4}, {"折疊", "ㄓㄜˊ ㄉㄧㄝˊ", 2e-4},
		{"別", "ㄅㄧㄝˊ", 1e-3}, {"蹩", "ㄅㄧㄝˊ", 1e-6}, {"疊", "ㄉㄧㄝˊ", 1e-4}, {"蝶", "ㄉㄧㄝˊ", 5e-5},
		{"繃", "ㄅㄥˇ", 3e-7}, {"大", "ㄉㄚˋ", 2e-3}, {"挲", "ㄙㄚ˙", 1e-6}, {"啊", "ㄚ˙", 1e-3},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.MixedInput = true
	s.English = english.New([]english.Word{{Text: "hello", Weight: 3}, {Text: "hell", Weight: 2}})
	return s
}

func itemTexts(snap proto.Snapshot) []string {
	var out []string
	for _, it := range snap.Candidates.Items {
		out = append(out, it.Text)
	}
	return out
}

func TestMixedLiteralToneKeysOfferChineseOfTheReading(t *testing.T) {
	for _, tc := range []struct {
		keys, shown, pick, want string
		zhuyin                  string // 原樣注音仍在清單裡
	}{
		{"u1,6", "u1,6", "別", "別", "ㄅㄧㄝˊ"},
		{"u2,6", "u2,6", "疊", "疊", "ㄉㄧㄝˊ"},
		{"5k6 u2,6", "折 u2,6", "疊", "折 疊", "ㄉㄧㄝˊ"},
		{"hello u1,6", "hello u1,6", "別", "hello 別", "ㄅㄧㄝˊ"},
		{"hellou2,6", "hellou2,6", "hello疊", "hello疊", ""},
		{"1/3", "1/3", "繃", "繃", "ㄅㄥˇ"}, // #200 照字面的罕用字，明確按 ↓ 時也選得到
	} {
		t.Run(tc.keys, func(t *testing.T) {
			s := mixedReadingSession()
			if snap := mixedType(s, tc.keys); snap.Text() != tc.shown {
				t.Fatalf("預設顯示 %q，要維持英數 %q", snap.Text(), tc.shown)
			}
			snap := key(s, proto.KeyDown)
			got := itemTexts(snap)
			if len(got) == 0 || got[0] == "" {
				t.Fatalf("↓ 沒有候選：%q", got)
			}
			id := slices.Index(got, tc.pick)
			if id < 0 {
				t.Fatalf("↓ 候選 %q 沒有中文字 %q", got, tc.pick)
			}
			if id != 1 {
				t.Errorf("中文字 %q 應緊接在原按鍵後面（第 2 項），候選 %q", tc.pick, got)
			}
			if tc.zhuyin != "" && !slices.Contains(got, tc.zhuyin) {
				t.Errorf("原樣注音 %q 不見了：%q", tc.zhuyin, got)
			}
			snap = s.Handle(proto.Request{Type: proto.Select, Index: snap.Candidates.Items[id].ID})
			if snap.Text() != tc.want {
				t.Fatalf("選 %q 後顯示 %q，要 %q", tc.pick, snap.Text(), tc.want)
			}
			// 選好的字還能用 ↓ 找回原按鍵。
			snap = key(s, proto.KeyDown)
			if keys := tc.keys[max(0, len(tc.keys)-4):]; tc.keys != "1/3" && !slices.Contains(itemTexts(snap), keys) {
				t.Errorf("選字後 ↓ 找不回原按鍵 %q：%q", keys, itemTexts(snap))
			}
			key(s, proto.KeyEscape)
			if commit := key(s, proto.KeyEnter).Commit; commit != tc.want {
				t.Errorf("提交 %q，要 %q", commit, tc.want)
			}
		})
	}
}

// 原按鍵仍排第一、選了照字面；組不出有字讀音的按鍵（2024、co6 的 ㄏㄟˊ）不多出中文，
// 字典不認得的英文前段也不切（kevin87 不列 kevi挲）。
func TestMixedLiteralToneKeysKeepTheKeysFirst(t *testing.T) {
	s := mixedReadingSession()
	mixedType(s, "u1,6")
	snap := key(s, proto.KeyDown)
	if got := itemTexts(snap); got[0] != "u1,6" {
		t.Fatalf("原按鍵應排第一：%q", got)
	}
	snap = s.Handle(proto.Request{Type: proto.Select, Index: snap.Candidates.Items[0].ID})
	if snap.Text() != "u1,6" {
		t.Fatalf("選原按鍵後顯示 %q", snap.Text())
	}
	if commit := key(s, proto.KeyEnter).Commit; commit != "u1,6" {
		t.Fatalf("提交 %q", commit)
	}
	for _, keys := range []string{"2024", "co6", "kevin87"} {
		s := mixedReadingSession()
		mixedType(s, keys)
		got := itemTexts(key(s, proto.KeyDown))
		for _, text := range got {
			for _, r := range text {
				if r >= 0x4E00 && r <= 0x9FFF {
					t.Errorf("%q 的 ↓ 不該多出中文字：%q", keys, got)
				}
			}
		}
	}
}
