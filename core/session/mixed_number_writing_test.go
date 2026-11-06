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
		// #200：只用數字與標點鍵拼得出的罕用字（1/3 繃、1/6 甭、5.3 肘、86 嗄），和同樣這樣拼、但常用的字（5.6 軸、5. 周）。
		{"繃", "ㄅㄥˇ", 3e-7}, {"甭", "ㄅㄥˊ", 6e-7}, {"肘", "ㄓㄡˇ", 8e-7}, {"嗄", "ㄚˊ", 1e-7}, {"噢", "ㄡ˙", 1e-7},
		{"軸", "ㄓㄡˊ", 1e-5}, {"周", "ㄓㄡ", 1e-3}, {"手", "ㄕㄡˇ", 1e-3}, {"手肘", "ㄕㄡˇ ㄓㄡˇ", 2e-5},
		{"疊", "ㄉㄧㄝˊ", 1e-4}, {"折", "ㄓㄜˊ", 1e-4}, {"折疊", "ㄓㄜˊ ㄉㄧㄝˊ", 2e-4},
		{"丟", "ㄉㄧㄡ", 1e-4},
		{"窄", "ㄓㄞˇ", 2e-5}, {"齋", "ㄓㄞ", 2e-5}, {"眨", "ㄓㄚˇ", 8e-6}, {"把", "ㄅㄚˇ", 2e-3},
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

// #201：數字鍵＋聲調鍵出的字緊接在中文後面、自己緊接著半形 . , / 再打數字時，也改回原按鍵（我53,082 不是「我只,082」）。
// 冒號（答案：1）、-（負數：大-800）、中間隔著照字面送出的數字（至3.5、至6,762：「降至3.5」）照舊是中文。
func TestMixedDigitWordAfterChineseBeforeNumberPunct(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"ji353,082", "我53,082"},
		{"ji353.5", "我53.5"},
		{"ji354.88", "我54.88"},
		{"ji384.73", "我84.73"},
		{"ji304/09", "我04/09"},
		{"ji3 53,082", "我 53,082"},
		// 照舊
		{"28604:1", "答案：1"},
		{"ji304:10", "我案：10"},
		{"ji3284-800", "我大-800"},
		{"ji3543.5", "我至3.5"},
		{"ji3546,762", "我至6,762"},
		{"ji3294-3", "我戴爾"},
		{"ji353.", "我只."},
	} {
		s := numberWritingSession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
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

// #200：英文字後面緊接著「數字＋. : ,＋數字」的數字寫法（hello2.4、hello5.6）整串照字面，跟 #188 的 englishThenPunct
// 一樣不把英文後面的按鍵切給中文（hello豆、hello軸）。/ 不比照：英文後面緊接著打的「等」「正」很常見（Google等、AI整）。
// 英文後面隔了空白（hello 2.4 → hello 豆，跟單獨打的 2.4 一樣）、後面只有數字加標點再接空白（hello5. → hello周，#188）、
// 切開的是字母（hellosu3 → hello你）也照舊。
func TestMixedNumberWritingRightAfterEnglish(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"hello2.4", "hello2.4"},
		{"hello2.4 ", "hello2.4 "},
		{"hello5.6", "hello5.6"},
		{"hello2.3", "hello2.3"},
		{"hello2.40", "hello2.40"},
		{"hello2/3", "hello等"},
		{"hello5/4", "hello正"},
		{"xyz2/3", "xyz等"},
		{"hello 2.4", "hello 豆"},
		{"hello5. ", "hello周"},
		{"hello2. ", "hello都"},
		{"hellosu3", "hello你"},
		{"hello824", "hello824"},
		{"v1.2", "v1.2"},
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

// #119：緊接中文時，英文＋數字的保護不得攔走完整的反序音節（u2,6 是 ㄧㄉㄝˊ）。
// 句首、英文、空白後維持數字寫法。中文後真的要輸入這個識別字時，仍可選原按鍵或切到英文。
func TestMixedCompleteReadingAfterChineseBeforeEnglishNumberGuard(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"u2,6", "u2,6"}, {"2u,6", "疊"}, {"u2.7", "u2.7"},
		{"5k6u2,6", "折疊"},
		{"5k6 u2,6", "折 u2,6"}, {"ji3u2.7", "我丟"},
		{"hello u2,6", "hello u2,6"},
		{"hellou2,6", "hellou2,6"}, {"v1.2", "v1.2"},
		{"ji3hellou2,6", "我hellou2,6"}, {"ji3v1.2", "我v1.2"},
	} {
		s := numberWritingSession()
		if snap := mixedType(s, tc.keys); snap.Text() != tc.want {
			t.Errorf("%q 顯示 %q，要 %q", tc.keys, snap.Text(), tc.want)
		}
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
	}
	s := numberWritingSession()
	mixedType(s, "ji3u2,")
	key(s, proto.KeyDown)
	s.Handle(proto.Request{Type: proto.Select, Index: 0}) // 明確選原按鍵
	if snap := mixedType(s, "60"); snap.Text() != "我u2,60" {
		t.Fatalf("明確選原按鍵後仍應維持識別字：%q", snap.Text())
	}
}

// #200：只用數字鍵與標點鍵、加聲調鍵拼出的讀音，自己的首選字是罕用字（1/3 繃、1/6 甭、5.3 肘、86 嗄）時照字面，
// 比照「組不出常用字就照字面」；接在中文後面、英文後面也一樣。常用字（2.4 豆、5.6 軸、2/3 等、5/4 正、1/4 蹦）照舊出字，
// 跟前面的中文成詞時（手肘）也照舊。照字面的按鍵按 ↓ 仍有帶聲調的注音。
func TestMixedRareDigitKeyReading(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"1/3", "1/3"},
		{"1/6", "1/6"},
		{"5.3", "5.3"},
		{"86", "86"},
		{"1/3 ", "1/3 "},
		{"ji31/3", "我1/3"},
		{"ji3 1/3", "我 1/3"},
		{"hello1/3", "hello1/3"},
		{"2.4", "豆"},
		{"5.6", "軸"},
		{"2/3", "等"},
		{"5/4", "正"},
		{"1/4", "蹦"},
		{"ji35.6", "我軸"},
		{"g.35.3", "手肘"},
		{"84.73", "84.73"}, // 84 阿 後面的 .7 是罕用的「噢」，跟 #176 的 03:00 一樣改回原按鍵
	} {
		s := numberWritingSession()
		if snap := mixedType(s, tc.keys); snap.Text() != tc.want {
			t.Errorf("%q 顯示 %q，要 %q", tc.keys, snap.Text(), tc.want)
		}
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
	}
	s := numberWritingSession()
	mixedType(s, "1/3")
	snap := key(s, proto.KeyDown)
	found := false
	for _, c := range snap.Candidates.Items {
		found = found || c.Text == "ㄅㄥˇ"
	}
	if !found {
		t.Errorf("1/3 按 ↓ 沒有 ㄅㄥˇ：%v", snap.Candidates.Items)
	}
}

// Learning unrelated words must not turn valid digit-key syllables into numbers.
func TestMixedDigitReadingsSurviveUnrelatedLearning(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"593", "窄"}, {"583", "眨"}, {"03", "俺"}, {"53", "只"}, {"183", "把"},
		{"2.4", "豆"}, {"5.6", "軸"},
		{"354", "354"}, {"57", "57"}, {"107", "107"}, {"1/3", "1/3"}, {"86", "86"},
	} {
		t.Run(tc.keys, func(t *testing.T) {
			s := numberWritingSession()
			for i := 0; i < 100; i++ {
				mixedType(s, "ji3")
				if got := key(s, proto.KeyEnter).Commit; got != "我" {
					t.Fatalf("learning commit: %q", got)
				}
			}
			if s.model.Store.Total == 0 {
				t.Fatal("typing did not produce learning evidence")
			}
			for _, k := range tc.keys[:len(tc.keys)-1] {
				key(s, string(k))
			}
			if tc.keys == "593" {
				if hint := s.snapshot().ReadingHint; hint == nil || hint.Text != "ㄓㄞ" {
					t.Fatalf("59 reading hint: %+v", hint)
				}
			}
			if got := key(s, tc.keys[len(tc.keys)-1:]).Text(); got != tc.want {
				t.Fatalf("after unrelated learning: %q, want %q", got, tc.want)
			}
			if got := key(s, proto.KeyEnter).Commit; got != tc.want {
				t.Fatalf("commit: %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMixedRareDigitReadingCanStillLearn(t *testing.T) {
	s := numberWritingSession()
	if got := mixedType(s, "5.3").Text(); got != "5.3" {
		t.Fatalf("unlearned rare reading: %q", got)
	}
	key(s, proto.KeyEscape)
	// Normal Chinese input can teach a rare word; mixed input should honor that evidence.
	s.MixedInput = false
	for i := 0; i < 4; i++ {
		mixedType(s, "5.3")
		if got := key(s, proto.KeyEnter).Commit; got != "肘" {
			t.Fatalf("learning rare word: %q", got)
		}
	}
	s.MixedInput = true
	if got := mixedType(s, "5.3").Text(); got != "肘" {
		t.Fatalf("learned rare reading: %q", got)
	}
}

func TestMixedDigitReadingAfterLearningCanSelectNumber(t *testing.T) {
	s := numberWritingSession()
	for i := 0; i < 100; i++ {
		mixedType(s, "ji3")
		key(s, proto.KeyEnter)
	}
	if got := mixedType(s, "593").Text(); got != "窄" {
		t.Fatalf("default reading: %q", got)
	}
	items := downItems(key(s, proto.KeyDown))
	if len(items) < 2 || items[0] != "窄" || items[1] != "593" {
		t.Fatalf("word and original keys: %q", items)
	}
	s.Handle(proto.Request{Type: proto.Select, Index: 1})
	if got := key(s, proto.KeyEnter).Commit; got != "593" {
		t.Fatalf("selected number: %q", got)
	}
}
