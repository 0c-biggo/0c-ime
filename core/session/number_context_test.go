package session

import (
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// numberContextSession 模擬真實資料包的情況：句首時量詞的常用同音字（為、原、各、越、典）比較常見，
// 只有接在中文數詞後面（bigram）量詞才勝出。資料包沒有阿拉伯數字，所以這些 bigram 都以中文數詞為前詞。
// 也收了「單位」「八個」「之後」，確認只用數字鍵拼成的一聲字（#162）與數字語境互不影響。
func numberContextSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"我", "ㄨㄛˇ", 1e-2}, {"有", "ㄧㄡˇ", 5e-3}, {"很", "ㄏㄣˇ", 3e-3}, {"好", "ㄏㄠˇ", 3e-3}, {"很好", "ㄏㄣˇ ㄏㄠˇ", 1e-3},
		{"為", "ㄨㄟˋ", 2e-3}, {"位", "ㄨㄟˋ", 3e-4},
		{"原", "ㄩㄢˊ", 1e-3}, {"元", "ㄩㄢˊ", 3e-4},
		{"各", "ㄍㄜˋ", 2e-3}, {"個", "ㄍㄜˋ", 3e-4},
		{"越", "ㄩㄝˋ", 2e-3}, {"月", "ㄩㄝˋ", 3e-4},
		{"典", "ㄉㄧㄢˇ", 2e-3}, {"點", "ㄉㄧㄢˇ", 3e-4},
		{"黏", "ㄋㄧㄢˊ", 2e-3}, {"年", "ㄋㄧㄢˊ", 3e-4},
		{"單", "ㄉㄢ", 2e-4}, {"單位", "ㄉㄢ ㄨㄟˋ", 3e-4},
		{"八", "ㄅㄚ", 5e-4}, {"八個", "ㄅㄚ ㄍㄜˋ", 4e-5},
		{"之", "ㄓ", 1e-3}, {"後", "ㄏㄡˋ", 1e-3}, {"之後", "ㄓ ㄏㄡˋ", 1e-3},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	for _, left := range numberLefts {
		for _, w := range []string{"位", "元", "個", "月", "點", "年"} {
			sys.SetBi(left, w, 0.05)
		}
	}
	return New(personal.NewModel(lex, sys))
}

// 中英混打：句首或英文後面的字面數字（含後面一個空白）接量詞時，量詞以數字當左語境解碼，
// 不再是句首最常見的同音字（「15 為」「15 原」「12 各」）。#162 起只用數字鍵拼得成一聲字的 20、18 接空白是中文，
// 這裡用拼不成音節的 15、12（ㄅㄓ、ㄅㄉ）。
func TestMixedMeasureAfterLiteralNumber(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"15 jo4", "15 位"},
		{"15 m06", "15 元"},
		{"18 ek4", "八個"}, // #162：18 接空白是八，接著打的字一起解碼
		{"15 ek4", "15 個"},
		{"15 su06", "15 年"},
		{"5m,4", "5月"}, // #162：單鍵的 5 接空白是之，要數字＋量詞不空格打，或先按 Enter
		{"10 2u03", "10 點"},
		{"ji3u.3 15 ek4", "我有 15 個"},
		{"ji3u.3 12 ek4", "我有 12 個"}, // 「中文＋空白」的數字留白寫法維持數字，量詞也要對
		{"2024 su06", "2024 年"},
		{"2024su06", "2024年"}, // 不空格也一樣
		{"hello 15 jo4", "hello 15 位"},
	} {
		s := numberContextSession()
		s.MixedInput = true
		if snap := mixedType(s, tc.keys); snap.Text() != tc.want {
			t.Errorf("%q 顯示 %q，要 %q", tc.keys, snap.Text(), tc.want)
		}
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// 數字語境只影響數字後面的那一段；不是量詞的中文、非數字字面、數字鍵拼成的中文與原樣注音都不變。
func TestMixedNumberContextLeavesOtherTextAlone(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"iPhone 15 cp3cl3", "iPhone 15 很好"},
		{"hello 5 c.4", "hello 之後"}, // #134／#162：5 接空白是之
		{"5 c.4", "之後"},
		{"15 ji3", "15 我"},
		{"hello jo4", "hello 為"}, // 英文後面不是數字：句首語境
		{"jo4", "為"},
		{"ji3 jo4", "我 為"},
		{"A4 jo4", "A4 為"}, // 字母開頭的型號（A4、mp3）不當數量
	} {
		s := numberContextSession()
		s.MixedInput = true
		if snap := mixedType(s, tc.keys); snap.Text() != tc.want {
			t.Errorf("%q 顯示 %q，要 %q", tc.keys, snap.Text(), tc.want)
		}
	}
}

// 一般模式（未開混打）：數字鍵盤在組字中插入的半形數字後面接量詞，同樣以數字當語境。
func TestKeypadNumberThenMeasure(t *testing.T) {
	s := numberContextSession()
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄧㄡˇ")
	key(s, "KP2")
	key(s, "KP0")
	if snap := typeSyl(s, "ㄍㄜˋ"); snap.Text() != "我有20個" {
		t.Fatalf("數字鍵盤的 20 後面接 ㄍㄜˋ：%q", snap.Text())
	}
	// 關閉「空白鍵選字」時，數字後面可以先打一個空白。
	s = numberContextSession()
	s.SpaceInsertsSpace = true
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄧㄡˇ")
	key(s, "KP2")
	key(s, "KP0")
	key(s, " ")
	if snap := typeSyl(s, "ㄨㄟˋ"); snap.Text() != "我有20 位" {
		t.Fatalf("數字＋空白後面接 ㄨㄟˋ：%q", snap.Text())
	}
}

// 候選視窗與畫面同一個語境：數字後面開 ㄨㄟˋ 的候選，第一個是「位」。
func TestNumberContextOrdersCandidates(t *testing.T) {
	s := numberContextSession()
	s.MixedInput = true
	mixedType(s, "15 jo4")
	snap := key(s, proto.KeyDown)
	if snap.Candidates == nil || len(snap.Candidates.Items) == 0 || snap.Candidates.Items[0].Text != "位" {
		t.Fatalf("候選：%+v", snap.Candidates)
	}
}

// 數字語境只用在解碼，不寫進學習：提交「15 位」後，學到的左鄰不是任何中文數詞。
func TestNumberContextIsNotLearned(t *testing.T) {
	s := numberContextSession()
	s.MixedInput = true
	mixedType(s, "15 jo4")
	key(s, proto.KeyEnter)
	for _, left := range numberLefts {
		if s.model.Store.Ctx[left] != 0 {
			t.Fatalf("學到以 %q 為左鄰的計數", left)
		}
	}
}

// 神經選字：評分器的前文照舊是畫面上的「15 」，N-best 首選與畫面一致，所以單音節段仍會送去評分。
func TestNeuralSeesNumberSegment(t *testing.T) {
	var got []rerank.Request
	s := numberContextSession()
	s.MixedInput = true
	s.Opt.PrefixAware = false
	s.Handle(proto.Request{Type: proto.Context, ContextEnabled: true})
	s.Rerank = rerank.Options{Weight: 1, TextWeight: 2, TextMargin: 1, ContextRunes: rerank.MaxContextRunes, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		got = append(got, r)
		return make([]float64, len(r.Candidates)), nil
	})}
	if snap := mixedType(s, "15 jo4"); snap.Text() != "15 位" {
		t.Fatalf("顯示 %q", snap.Text())
	}
	if len(got) == 0 {
		t.Fatal("數字後面的單音節段沒有送去神經評分")
	}
	last := got[len(got)-1]
	if !strings.HasSuffix(last.Context, "15 ") || last.Candidates[0].Text != "位" {
		t.Fatalf("前文 %q、首選 %q", last.Context, last.Candidates[0].Text)
	}
}
