package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/english"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// #176 用的小詞庫：標點鍵開頭、只用數字與標點鍵就打得出的字（.4 嘔、-4 二、,4 誒、/2 的 ㄉㄥˇ 等），
// 以及數字鍵＋聲調鍵出的字（03 俺、04 案、53 只）。
func numberWritingSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"我", "ㄨㄛˇ", 1e-2}, {"是", "ㄕˋ", 5e-3}, {"你", "ㄋㄧˇ", 5e-3}, {"北", "ㄅㄟˇ", 1e-3}, {"大", "ㄉㄚˋ", 2e-3},
		{"嘔", "ㄡˋ", 1e-4}, {"偶", "ㄡˇ", 2e-4}, {"歐", "ㄡ", 3e-4}, {"二", "ㄦˋ", 1e-3}, {"而", "ㄦˊ", 2e-3},
		{"誒", "ㄝˋ", 1e-4}, {"鞥", "ㄥˋ", 1e-5},
		{"豆", "ㄉㄡˋ", 3e-4}, {"抖", "ㄉㄡˇ", 2e-4}, {"都", "ㄉㄡ", 2e-3},
		{"等", "ㄉㄥˇ", 2e-3}, {"正", "ㄓㄥˋ", 2e-3}, {"蹦", "ㄅㄥˋ", 1e-4},
		{"俺", "ㄢˇ", 1e-4}, {"案", "ㄢˋ", 3e-4}, {"只", "ㄓˇ", 2e-3}, {"至", "ㄓˋ", 1e-3},
		{"阿", "ㄚˋ", 2e-4}, {"戴", "ㄉㄞˋ", 3e-4}, {"爾", "ㄦˇ", 3e-4}, {"戴爾", "ㄉㄞˋ ㄦˇ", 2e-4}, {"大而", "ㄉㄚˋ ㄦˊ", 1e-5}, {"版", "ㄅㄢˇ", 3e-4}, {"答", "ㄉㄚˊ", 5e-4}, {"答案", "ㄉㄚˊ ㄢˋ", 3e-4},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.MixedInput = true
	s.English = english.New([]english.Word{{Text: "hello", Weight: 3}, {Text: "hell", Weight: 2}, {Text: "the", Weight: 1}})
	return s
}

// #176：緊接在字面數字後面、照字面是數字寫法的按鍵（3.4、3-4、6/23、3,472）按下聲調鍵照字面留下，
// 不把標點鍵和聲調組成中文（3嘔、3二、6等、3誒72）。聲調鍵 3 4 6 7 沒在組字時先照字面送出，
// 所以 3.4 的待定按鍵只剩 .4，要連同前面的 3 一起看，與 354（#175）、3.5 （#114）同一條規則。
func TestMixedNumberWritingAfterLiteralDigit(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"3.4", "3.4"},
		{"3.3", "3.3"},
		{"3.7", "3.7"}, // ㄡ˙ 沒有字、退回一聲的「歐」也不出
		{"3.40", "3.40"},
		{"ji33.4", "我3.4"},
		{"ji3 3.4", "我 3.4"},
		{"3-4", "3-4"},
		{"6-7", "6-7"},
		{"3,4", "3,4"},
		{"3,472", "3,472"},
		{"6/23", "6/23"},
		{"4-6-1", "4-6-1"},
		{"3.4 ", "3.4 "},
		{"3.4ji3", "3.4我"}, // 後面直接接中文
	} {
		s := numberWritingSession()
		if snap := mixedType(s, tc.keys); snap.Text() != tc.want {
			t.Errorf("%q 顯示 %q，要 %q", tc.keys, snap.Text(), tc.want)
		}
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #176：數字鍵＋聲調鍵出的字（#175 的 03 俺、04 案、53 只、84 阿、103 版）後面緊接著數字標點再打數字，
// 是時間或數字寫法（03:00、04:10、53,082、84.73、103,020）：那個字改回原按鍵，: 也是半形。
func TestMixedDigitWordBeforeNumberPunct(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"03:00", "03:00"},
		{"04:10", "04:10"},
		{"04:30", "04:30"}, // : 後面直接是聲調鍵
		{"53,082", "53,082"},
		{"84.73", "84.73"},
		{"103,020", "103,020"},
		{"846,000", "846,000"}, // 84 阿 後面隔著照字面送出的 6
		{"534,154", "534,154"},
		{"04-06", "04-06"},
		{"04/09", "04/09"},
		{"ji3 03:00", "我 03:00"},
		{"hello 03:00", "hello 03:00"},
		{"2.7", "2.7"}, // ㄉㄡ˙ 沒有字，跟 #175 的 57 一樣不靠輕聲退回一聲出「都」
		{"ji32.7", "我2.7"},
	} {
		s := numberWritingSession()
		if snap := mixedType(s, tc.keys); snap.Text() != tc.want {
			t.Errorf("%q 顯示 %q，要 %q", tc.keys, snap.Text(), tc.want)
		}
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
	}
	// 接在中文後面、後面不是數字、或被選過的字照舊。
	for _, tc := range []struct{ keys, want string }{
		{"28604:1", "答案：1"},
		{"53:", "只："},
		{"04", "案"},
		{"294-3", "戴爾"}, // -3 是 ㄦˇ：標點鍵和聲調鍵組得成字，是中文
		{"284-6", "大而"},
		{"53.", "只."},
	} {
		s := numberWritingSession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
	}
	s := numberWritingSession()
	mixedType(s, "04")
	key(s, proto.KeyDown)
	s.Handle(proto.Request{Type: proto.Select, Index: 0}) // 明確選了「案」
	mixedType(s, ":1")
	if got := key(s, proto.KeyEnter).Commit; got != "案：1" {
		t.Errorf("選過的「案」後面打 :1 提交 %q，要「案：1」", got)
	}
}

// #176：緊接在字面數字後面的 Shift＋;（:）是半形，與 10:30 一樣併進待定按鍵（3:00 不是「3：00」）。
// 前面不是字面數字時照舊是全形「：」。
func TestMixedColonAfterLiteralDigit(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"3:00", "3:00"},
		{"ji33:00", "我3:00"},
		{"7:33", "7:33"},
		{"4:3", "4:3"},
		{"3: ", "3: "},
		{"10:30", "10:30"}, // 本來就是半形
		{"ji3:", "我："},     // 中文後面照舊是全形
		{":", "："},
	} {
		s := numberWritingSession()
		if snap := mixedType(s, tc.keys); snap.Text() != tc.want {
			t.Errorf("%q 顯示 %q，要 %q", tc.keys, snap.Text(), tc.want)
		}
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #176 沒有改的部分：不是緊接在字面數字後面時，數字鍵加標點鍵加聲調鍵照舊組成中文（2/3 等、5/4 正、2.4 豆，
// 「等」「正」在句首也常打）；標點鍵開頭的字接在中文或空白後面照舊（我嘔、二）；#114／#116 的結尾 . 照舊。
func TestMixedNumberWritingKeepsChinese(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"2/3", "等"},
		{"5/4", "正"},
		{"2.4", "豆"},
		{"ji32/3", "我等"},
		{"ji3.4", "我嘔"},
		{"-4", "二"},
		{"3 -4", "3 二"}, // 空白隔開就不是同一個數字
		{"2. ", "都"},
		{"3.5 ", "3.5 "},
		{"53", "只"},
		{"354", "354"},
	} {
		s := numberWritingSession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #176 順帶：英文字後面直接打的數字，切出來組字的按鍵順序顛倒時照字面（hello13 不是 hell北：o1 是 ㄟㄅ），
// 跟 #175 句首或英文後面的反序數字留數字一樣。照順序打的照舊切開（hellosu3 → hello你）。
func TestMixedEnglishBoundaryKeysInOrder(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"hello13", "hello13"},
		{"hello824", "hello824"},
		{"hellosu3", "hello你"},
		{"helloji3", "hello我"},
	} {
		s := numberWritingSession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
	}
}
