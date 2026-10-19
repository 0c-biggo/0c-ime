package session

import (
	"math/rand"
	"strings"
	"testing"

	"git.biggo.com/0c/ime/core/english"
	"git.biggo.com/0c/ime/core/proto"
)

// 具名鍵（Backspace、Enter…）是協定裡的按鍵名稱，不是文字。不論英文模式、英文自動補完、
// 中英混打、未打完的注音、候選或 Emoji 選單，核心都不能把鍵名放進組字或提交（#46、#95）；
// 沒有組字時（例如 App 裡選取了文字）Backspace／Enter 一律交還 App，不帶提交文字。
var protocolKeyNames = []string{
	proto.KeyEnter, proto.KeyEscape, proto.KeyBackspace, proto.KeyDelete, proto.KeyTab,
	proto.KeyLeft, proto.KeyRight, proto.KeyUp, proto.KeyDown, proto.KeyPageUp, proto.KeyPageDown,
	proto.KeyBackTab, proto.KeyHome, proto.KeyEnd,
}

func containsKeyName(text string) string {
	for _, name := range protocolKeyNames {
		if strings.Contains(text, name) {
			return name
		}
	}
	return ""
}

func namedKeySessions() map[string]func() *Session {
	return map[string]func() *Session{
		"zhuyin": newSession,
		"mixed":  mixedSession,
		"english": func() *Session {
			s := newSession()
			s.Handle(proto.Request{Type: proto.Mode, On: true})
			return s
		},
		"autocomplete": autocompleteSession,
		"palette": func() *Session {
			s := paletteSession()
			s.MixedInput = true
			return s
		},
		"everything": func() *Session {
			s := paletteSession()
			s.MixedInput = true
			s.EnglishAutocomplete = true
			s.English = english.New([]english.Word{{Text: "hello", Weight: 100}, {Text: "help", Weight: 50}})
			s.EscKeepComposition = true
			return s
		},
	}
}

func TestIdleNamedKeysCarryNoText(t *testing.T) {
	for name, build := range namedKeySessions() {
		for _, k := range []string{proto.KeyBackspace, proto.KeyEnter, proto.KeyDelete, proto.KeyEscape} {
			for _, english := range []bool{false, true} {
				s := build()
				s.Handle(proto.Request{Type: proto.Mode, On: english})
				// 剛切換、剛提交與剛關閉候選之後都是「沒有組字」：App 裡的選取範圍由 App 自己刪。
				for _, prefix := range [][]proto.Request{
					nil,
					{{Type: proto.Toggle}, {Type: proto.Toggle}},
					{{Type: proto.Key, Key: "h"}, {Type: proto.Commit}},
					{{Type: proto.Key, Key: "="}, {Type: proto.Key, Key: proto.KeyEscape}, {Type: proto.Commit}},
				} {
					for _, r := range prefix {
						s.Handle(r)
					}
					if snap := s.Handle(proto.Request{Type: proto.Commit}); snap.Text() != "" {
						t.Fatalf("%s：提交後仍有組字 %q", name, snap.Text())
					}
					snap := key(s, k)
					if snap.Handled || snap.Commit != "" || snap.Text() != "" {
						t.Fatalf("%s english=%v %v：閒置 %s 應原樣交還 App：%+v", name, english, prefix, k, snap)
					}
				}
			}
		}
	}
}

func TestNamedKeysNeverBecomeText(t *testing.T) {
	keys := append([]string{
		"h", "e", "l", "p", "j", "3", "4", "6", "7", "u", "/", ";", ",", ".", " ", "=", "`", "1", "KP1", "KP.",
	}, protocolKeyNames...)
	for name, build := range namedKeySessions() {
		rng := rand.New(rand.NewSource(95))
		for round := 0; round < 200; round++ {
			s := build()
			for step := 0; step < 40; step++ {
				var r proto.Request
				switch n := rng.Intn(20); {
				case n == 0:
					r = proto.Request{Type: proto.Toggle}
				case n == 1:
					r = proto.Request{Type: proto.Commit}
				case n == 2:
					r = proto.Request{Type: proto.Select, Index: rng.Intn(4)}
				default:
					r = proto.Request{Type: proto.Key, Key: keys[rng.Intn(len(keys))]}
				}
				snap := s.Handle(r)
				if k := containsKeyName(snap.Commit); k != "" {
					t.Fatalf("%s 第 %d 輪第 %d 步 %+v：提交含鍵名 %q：%q", name, round, step, r, k, snap.Commit)
				}
				if k := containsKeyName(snap.Text()); k != "" {
					t.Fatalf("%s 第 %d 輪第 %d 步 %+v：組字含鍵名 %q：%q", name, round, step, r, k, snap.Text())
				}
			}
		}
	}
}
