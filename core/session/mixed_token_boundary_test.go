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

// tokenBoundarySession 照真實資料包的量級收字；ok、gcp、ai、gg、nina 不在英文字典裡，air、ok 的 okay 在。
func tokenBoundarySession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"嗎", "ㄇㄚ˙", 2e-3}, {"吧", "ㄅㄚ˙", 2e-3}, {"啦", "ㄌㄚ˙", 2e-3}, {"的", "ㄉㄜ˙", 1.6e-2}, {"了", "ㄌㄜ˙", 5e-3},
		{"是", "ㄕˋ", 5e-3}, {"在", "ㄗㄞˋ", 3e-3}, {"大", "ㄉㄚˋ", 3e-3}, {"但", "ㄉㄢˋ", 1e-3}, {"半", "ㄅㄢˋ", 4e-4}, {"版", "ㄅㄢˇ", 2e-4},
		{"拔", "ㄅㄚˊ", 3e-5}, {"膽", "ㄉㄢˇ", 2e-5}, {"白", "ㄅㄞˊ", 3e-4}, {"我", "ㄨㄛˇ", 2e-3}, {"天", "ㄊㄧㄢ", 1e-3}, {"今", "ㄐㄧㄣ", 5e-4},
		{"法", "ㄈㄚˇ", 4e-4}, {"泡", "ㄆㄠˋ", 4e-5}, {"晶", "ㄐㄧㄥ", 2e-5}, {"競", "ㄐㄧㄥˋ", 1.2e-4}, {"應", "ㄧㄥˋ", 1.7e-4}, {"財", "ㄘㄞˊ", 2e-4},
		{"盤", "ㄆㄢˊ", 1e-4}, {"貓", "ㄇㄠ", 5e-5}, {"罵", "ㄇㄚˋ", 5e-5}, {"馬", "ㄇㄚˇ", 2e-4}, {"滿", "ㄇㄢˇ", 2e-4}, {"案", "ㄢˋ", 3e-4},
		{"啊", "ㄚ˙", 8e-4}, {"二", "ㄦˋ", 3e-4}, {"爾", "ㄦˇ", 1e-4}, {"挱", "ㄙㄚ˙", 3e-8}, {"跌", "ㄉㄧㄝˊ", 3e-4}, {"都", "ㄉㄡ", 1e-3},
		{"美", "ㄇㄟˇ", 8e-4}, {"收", "ㄕㄡ", 3e-4}, {"崩", "ㄅㄥ", 3e-5},
		{"安", "ㄢ", 3e-4}, {"裝", "ㄓㄨㄤ", 3e-4}, {"站", "ㄓㄢˋ", 3e-4}, {"俺", "ㄢˇ", 1e-4}, {"把", "ㄅㄚˇ", 1e-3}, {"答", "ㄉㄚˊ", 2e-4},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.MixedInput = true
	var words []english.Word
	for _, w := range []string{"hello", "air", "okay", "http", "the", "model", "paypal", "jolts", "mac"} {
		words = append(words, english.Word{Text: w, Weight: 65})
	}
	s.English = english.New(words)
	return s
}

// #241：字典不認得的英文接帶聲調的注音。大寫字母不是注音鍵，大寫的後面就是邊界；只有兩個字母的前段（ok）也切。
func TestMixedUnknownTokenThenZhuyin(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		// 回報的案例
		{"OKa87", "OK嗎"}, {"Oka87", "Ok嗎"}, {"oka87", "ok嗎"}, {"GCP187", "GCP吧"},
		// 同一條規則：後段以數字鍵結尾
		{"ABCa87", "ABC嗎"}, {"AWSy94", "AWS在"}, {"OK187", "OK吧"}, {"ok187", "ok吧"}, {"okx87", "ok啦"}, {"PTTz83", "PTT法"}, {"LDPC284", "LDPC大"},
		// 後段只有一個字母鍵（明確的聲調鍵）
		{"OKg4", "OK是"}, {"GCPg4", "GCP是"},
		// 組字機率低、但不是罕用字（常用字門檻 mixedUnknownWordFloor 擋不到大寫縮寫）
		{"AIql4", "AI泡"},
		// 接空白的一聲，後段緊接在大寫後面
		{"GCPwu0 ", "GCP天"}, {"GGrup ", "GG今"}, {"AIru/ ", "AI晶"}, {"GG2. ", "GG都"}, {"GG1/ ", "GG崩"},
		// AI 後面的 r 不是英文字 air 的一部分
		{"AIru/4", "AI競"},
		// 單一個大寫字母
		{"Vxk7", "V了"}, {"Gq06", "G盤"},
		// 大小寫混合、夾數字
		{"Q2h96", "Q2財"}, {"H20ru/4", "H20競"}, {"00953Bq06", "00953B盤"}, {"CoWoSq06", "CoWoS盤"},
		// 字典認得的英文照舊
		{"HTTPa87", "HTTP嗎"}, {"helloa87", "hello嗎"}, {"hello187", "hello吧"},
		// 接著打的字也照常
		{"GCP187ji3", "GCP吧我"}, {"OKa87ji3", "OK嗎我"}, {"AI2842k7", "AI大的"},
		// 大寫縮寫加複數的 s 照舊是英文字
		{"JOLTsg4", "JOLTs是"},
	} {
		s := tokenBoundarySession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #241：帳號、人名、型號照字面。全小寫或只有字首大寫、前段超過兩個字母的沒有英文邊界的證據；只有一個數字鍵、
// 只有數字接空白、單一字母接數字的是型號。
func TestMixedUnknownTokenStaysLiteral(t *testing.T) {
	for _, keys := range []string{
		"nina87", "lisa87", "lisa0304", "kevin87", "Nina87", "Lisa87", "Kevin87", "abc123", "abc187", "gcp187", "test187",
		"A87", "T87", "H87", "Z87", "AE86", "GT86", "MA87", "KD84", "PE84", "MP3", "PS4", "AK47", "RSI84",
		"F104", "T184", "F16", "M16", "A320", "B787",
		"GPT-4", "GPT-4o", "CVN-68", "GDP-30%",
		"RTX4090", "RTX3070", "GTX1060", "GTX1070", "GT1030", "PCB2030", "GG1040", "GG1960", "SC18400",
		"MA20 ", "GT20 ", "RX100 ", "ABC1, ", "PayPal ", "Model3", "mp3", "h264", "u2", "gpt4o ", "ps4u ", "i7", "abc3",
		"EPS:2.46", "YOY+103%",
	} {
		s := tokenBoundarySession()
		mixedType(s, keys)
		if got := key(s, proto.KeyEnter).Commit; got != keys {
			t.Errorf("%q 應照字面：%q", keys, got)
		}
	}
}

// #241：切錯了照常救得回來：字上按 ↓ 有原按鍵。
func TestMixedTokenWordOffersKeys(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"GCP187", "187"}, {"OKa87", "a87"}, {"AB103", "103"},
	} {
		s := tokenBoundarySession()
		mixedType(s, tc.keys)
		got := itemTexts(key(s, proto.KeyDown))
		if !slices.Contains(got, tc.want) {
			t.Errorf("%q 的 ↓ 應有原按鍵 %q：%q", tc.keys, tc.want, got)
		}
	}
}

// #241：照字面留下的小寫英文接常用字的讀音，↓ 排在原按鍵後面，選了就是中文；組成罕用字的（kevin87）不列。
func TestMixedLiteralTokenOffersCharacters(t *testing.T) {
	for _, tc := range []struct{ keys, want, commit string }{
		{"gcp187", "gcp吧", "gcp吧"}, {"nina87", "nin嗎", "nin嗎"},
	} {
		s := tokenBoundarySession()
		mixedType(s, tc.keys)
		snap := key(s, proto.KeyDown)
		got := itemTexts(snap)
		if len(got) < 2 || got[0] != tc.keys || !slices.Contains(got, tc.want) {
			t.Fatalf("%q 的 ↓：%q", tc.keys, got)
		}
		id := slices.Index(got, tc.want)
		s.Handle(proto.Request{Type: proto.Select, Index: snap.Candidates.Items[id].ID})
		if commit := key(s, proto.KeyEnter).Commit; commit != tc.commit {
			t.Errorf("%q 選 %q 後提交 %q", tc.keys, tc.want, commit)
		}
	}
	s := tokenBoundarySession()
	mixedType(s, "kevin87")
	if got := itemTexts(key(s, proto.KeyDown)); slices.Contains(got, "kevi挱") {
		t.Errorf("kevin87 的 ↓ 不該列罕用字：%q", got)
	}
}

// #241：常打的型號可以加進我的詞彙（#146）：完全相同的按鍵不切。
func TestMixedVocabEnglishKeepsToken(t *testing.T) {
	s := englishVocabSession(t, true, "RXg4")
	mixedType(s, "RXg4")
	if got := key(s, proto.KeyEnter).Commit; got != "RXg4" {
		t.Errorf("加進詞彙的 RXg4 應照字面：%q", got)
	}
	s = englishVocabSession(t, true)
	mixedType(s, "RXg4")
	if got := key(s, proto.KeyEnter).Commit; got != "RX是" {
		t.Errorf("沒有加詞時 RXg4 是 RX是：%q", got)
	}
}

// #241：型號、編號的長度超過切出來的音節：下一鍵是聲調鍵或 @ _ 時回頭改成原按鍵，0 看再下一鍵（TestMixedTokenDigitsThenAn）；
// 1 2 5 是聲母、8 9 常是下一個字，照常接下去；選過的字不改。
func TestMixedTokenDigitsTurnBackOnNextDigit(t *testing.T) {
	s := tokenBoundarySession()
	mixedType(s, "GT103")
	snap := key(s, proto.KeyDown)
	id := slices.Index(itemTexts(snap), "版")
	if id < 0 {
		t.Fatalf("GT103 的 ↓ 應有版：%q", itemTexts(snap))
	}
	s.Handle(proto.Request{Type: proto.Select, Index: snap.Candidates.Items[id].ID})
	mixedType(s, "0")
	if got := key(s, proto.KeyEnter).Commit; got != "GT版0" {
		t.Errorf("選過的字不改回按鍵：%q", got)
	}
	for _, tc := range []struct{ keys, want string }{
		{"GT1030", "GT1030"}, {"GG1040", "GG1040"}, {"GG1960", "GG1960"}, {"AB1037", "AB1037"}, {"AB1034", "AB1034"},
		{"GCP187@x", "GCP187@x"}, {"GCP187_x", "GCP187_x"},
		{"AI2842k7", "AI大的"},
	} {
		s := tokenBoundarySession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// 英文後面只用數字鍵打出的字，下一鍵是 ㄢ 的 0 時還不能判斷：接聲調鍵或空白組成中文就留著（Mac版安裝），
// 接別的鍵、Enter 或結束才是型號，回頭改成原按鍵。0.2.7 之後 #241 一看到 0 就退回，吃掉組好的字。
func TestMixedTokenDigitsThenAn(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		// 接聲調鍵：案、俺
		{"hello10304", "hello版案"}, {"Mac10304", "Mac版案"}, {"GT10304", "GT版案"}, {"CPI28604", "CPI答案"}, {"hello10303", "hello版俺"},
		// 接空白：安
		{"hello1030 ", "hello版安"}, {"Mac1030 5j; ", "Mac版安裝"}, {"PC1030 5j; ", "PC版安裝"}, {"AI2840 504", "AI大安站"},
		// 同一條規則的其他字：但、半、把、站
		{"hello2040 ", "hello但安"}, {"hello1040 ", "hello半安"}, {"hello1830 ", "hello把安"}, {"hello5040 ", "hello站安"},
		// 組成中文之後照常接下去
		{"hello10304ji3", "hello版案我"},
		// 型號：後面接數字、字母、標點或 @ _，照字面
		{"GT10300", "GT10300"}, {"GT10301", "GT10301"}, {"GT10309", "GT10309"}, {"GT1030a", "GT1030a"}, {"GT1030.5", "GT1030.5"},
		{"GT1030:", "GT1030:"}, {"ABC1030_x", "ABC1030_x"}, {"ABC1030@x", "ABC1030@x"}, {"GT1030-", "GT1030-"},
		// 聲調組不成字的（ㄢˊ、ㄢ˙）也是型號
		{"GT10306", "GT10306"}, {"GT10307", "GT10307"},
		// 型號後面接字母開頭的注音：跟 0 沒有 ㄢ 的解釋時一樣
		{"RTX1030wu0 ", "RTX1030wu0 "},
	} {
		s := tokenBoundarySession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// 0 還在待定時：畫面上中文字留著；Enter、移動游標都改回型號；退格只退 0。
func TestMixedTokenDigitsPendingAn(t *testing.T) {
	s := tokenBoundarySession()
	if got := mixedType(s, "GT1030").Text(); got != "GT版0" {
		t.Errorf("0 待定時畫面：%q", got)
	}
	if got := key(s, proto.KeyEnter).Commit; got != "GT1030" {
		t.Errorf("Enter：%q", got)
	}

	s = tokenBoundarySession()
	mixedType(s, "GT1030")
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	s.Handle(proto.Request{Type: proto.Cursor, To: 1 << 30})
	if got := key(s, proto.KeyEnter).Commit; got != "GT1030" {
		t.Errorf("移動游標：%q", got)
	}

	s = tokenBoundarySession()
	mixedType(s, "hello1030")
	if got := key(s, proto.KeyBackspace).Text(); got != "hello版" {
		t.Errorf("退格只退 0：%q", got)
	}
	if got := mixedType(s, "04").Text(); got != "hello版案" {
		t.Errorf("退格後重打：%q", got)
	}

	// 按空白會出字，讀音提示要有。
	s = tokenBoundarySession()
	if snap := mixedType(s, "GT1030"); snap.ReadingHint == nil || snap.ReadingHint.Text != "ㄢ" {
		t.Errorf("讀音提示：%+v", snap.ReadingHint)
	}

	// 選過的字不改回按鍵；↓ 選了原按鍵 0 也不再改動。
	s = tokenBoundarySession()
	mixedType(s, "GT103")
	snap := key(s, proto.KeyDown)
	s.Handle(proto.Request{Type: proto.Select, Index: snap.Candidates.Items[slices.Index(itemTexts(snap), "版")].ID})
	if got := mixedType(s, "04").Text(); got != "GT版案" {
		t.Errorf("選過的字接 04：%q", got)
	}
	s = tokenBoundarySession()
	mixedType(s, "GT1030")
	snap = key(s, proto.KeyDown)
	s.Handle(proto.Request{Type: proto.Select, Index: snap.Candidates.Items[slices.Index(itemTexts(snap), "0")].ID})
	if got := key(s, proto.KeyEnter).Commit; got != "GT版0" {
		t.Errorf("↓ 選了 0：%q", got)
	}
}

// 取捨：GT1030 接空白分不出是不是 PC版安裝，不吃掉組好的字；要型號在兩個字上各按一次 ↓ 選原按鍵，或打完 1030 先按 Enter 再打空白。
func TestMixedTokenDigitsThenSpaceRecoverable(t *testing.T) {
	s := tokenBoundarySession()
	if got := mixedType(s, "GT1030 ").Text(); got != "GT版安" {
		t.Fatalf("GT1030␣：%q", got)
	}
	pick := func(want string) {
		t.Helper()
		snap := key(s, proto.KeyDown)
		id := slices.Index(itemTexts(snap), want)
		if id < 0 {
			t.Fatalf("↓ 應有 %q：%q", want, itemTexts(snap))
		}
		s.Handle(proto.Request{Type: proto.Select, Index: snap.Candidates.Items[id].ID})
	}
	pick("0 ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 2})
	pick("103")
	s.Handle(proto.Request{Type: proto.Cursor, To: 1 << 30})
	if got := key(s, proto.KeyEnter).Commit; got != "GT1030 " {
		t.Errorf("↓ 改回：%q", got)
	}
}
