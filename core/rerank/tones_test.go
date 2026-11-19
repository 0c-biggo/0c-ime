package rerank

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func id(n int) *int { return &n }

func TestUnknownTonesDeclared(t *testing.T) {
	mask := &TonePolicy{Schema: ToneMaskSchema, UnknownToneID: id(0)}
	for _, c := range []struct {
		schema   string
		policy   *TonePolicy
		declared bool
		valid    bool
	}{
		{SequenceSchemaV1, nil, false, true}, // PF512
		{SequenceSchemaV1, mask, false, false},
		{SequenceSchemaV2, mask, true, true}, // tone0-only
		{SequenceSchemaV2, nil, false, false},
		{SequenceSchemaV2, &TonePolicy{Schema: ToneMaskSchema}, false, false},
		{SequenceSchemaV2, &TonePolicy{Schema: ToneMaskSchema, UnknownToneID: id(1)}, false, false},
		{SequenceSchemaV2, &TonePolicy{Schema: "other", UnknownToneID: id(0)}, false, false},
		{"zuin-encoder-runtime-v1", nil, false, true},
	} {
		if got := UnknownTonesDeclared(c.schema, c.policy); got != c.declared {
			t.Errorf("declared %s %+v = %v", c.schema, c.policy, got)
		}
		if got := ValidTonePolicy(c.schema, c.policy); got != c.valid {
			t.Errorf("valid %s %+v = %v", c.schema, c.policy, got)
		}
	}
}

func TestParseBases(t *testing.T) {
	b, err := ParseBases([]byte(`{"bases":["ㄅㄚ","ㄧ"],"characters":["八"]}`))
	if err != nil || len(b) != 2 || !b["ㄅㄚ"] || !b["ㄧ"] {
		t.Fatalf("%v %v", b, err)
	}
	for _, bad := range []string{`{}`, `{"bases":[]}`, `{"bases":["ㄅㄚˇ"]}`, `{"bases":["ㄅㄚ?"]}`, `not json`} {
		if _, err := ParseBases([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestReadable(t *testing.T) {
	bases := map[zhuyin.Syllable]bool{"ㄏㄨㄟ": true, "ㄧ": true, "ㄨ": true}
	untoned := []zhuyin.Syllable{zhuyin.Untone("ㄏㄨㄟ"), "ㄧˋ"}
	abbrev := []zhuyin.Syllable{zhuyin.Abbreviate("ㄨ", 0), "ㄧˋ"}  // ㄨ 本身是完整音節
	initial := []zhuyin.Syllable{zhuyin.Abbreviate("ㄉ", 0), "ㄧˋ"} // 只打聲母，沒有讀音
	for _, c := range []struct {
		opt  Options
		syls []zhuyin.Syllable
		want bool
	}{
		{Options{}, zhuyin.ParseReading("ㄏㄨㄟˋ ㄧˋ"), true},
		{Options{}, untoned, false},
		{Options{UnknownTones: true}, untoned, true},
		{Options{UnknownTones: true, Bases: bases}, untoned, true},
		{Options{UnknownTones: true, Bases: map[zhuyin.Syllable]bool{"ㄧ": true}}, untoned, false},
		{Options{Bases: map[zhuyin.Syllable]bool{"ㄧ": true}}, zhuyin.ParseReading("ㄏㄨㄟˋ ㄧˋ"), false},
		{Options{}, abbrev, false},
		{Options{UnknownTones: true}, abbrev, true},
		{Options{UnknownTones: true, Bases: bases}, abbrev, true},
		{Options{UnknownTones: true, Bases: map[zhuyin.Syllable]bool{"ㄧ": true}}, abbrev, false},
		{Options{UnknownTones: true}, initial, false},
		{Options{UnknownTones: true, Bases: map[zhuyin.Syllable]bool{"ㄉ": true, "ㄧ": true}}, initial, false},
	} {
		if got := c.opt.Readable(c.syls); got != c.want {
			t.Errorf("%+v %q = %v", c.opt, c.syls, got)
		}
	}
}

func TestMaskTones(t *testing.T) {
	toned := zhuyin.ParseReading("ㄏㄨㄟˋ ㄧˋ")
	if out, known := MaskTones(toned); known != nil || &out[0] != &toned[0] {
		t.Fatalf("全打聲調原樣回傳：%q %v", out, known)
	}
	in := []zhuyin.Syllable{"ㄏㄨㄟˊ", zhuyin.Untone("ㄧ")}
	out, known := MaskTones(in)
	if !slices.Equal(out, []zhuyin.Syllable{"ㄏㄨㄟˊ", "ㄧ"}) || !slices.Equal(known, []bool{true, false}) || in[1] != zhuyin.Untone("ㄧ") {
		t.Fatalf("%q %v（輸入不能被改：%q）", out, known, in)
	}
	ab := zhuyin.Abbreviate("ㄉ", 0)
	if out, _ := MaskTones([]zhuyin.Syllable{ab, zhuyin.Untone("ㄧ")}); out[0] != ab {
		t.Fatalf("只打聲母的簡拼不換成基底：%q", out)
	}
	if out, known := MaskTones([]zhuyin.Syllable{zhuyin.Abbreviate("ㄨ", 0), "ㄧˋ"}); out[0] != "ㄨ" || !slices.Equal(known, []bool{false, true}) {
		t.Fatalf("ㄨ 開頭的簡拼當成沒打聲調的 ㄨ：%q %v", out, known)
	}
}

func maskedRequest() Request {
	return Request{Revision: 1, Reading: []zhuyin.Syllable{"ㄏㄨㄟ", "ㄧ"}, ToneKnown: []bool{false, false}, Candidates: []decode.Hypothesis{
		{Text: "會議", LogScore: -2, Segments: []decode.Seg{{Word: "會議", Start: 0, End: 2}}},
		{Text: "回憶", LogScore: -3, Segments: []decode.Seg{{Word: "回憶", Start: 0, End: 2}}},
	}}
}

func TestSupportedToneKnown(t *testing.T) {
	r := maskedRequest()
	if !Supported(r) {
		t.Fatal("合法的 tone_known 被拒")
	}
	short := maskedRequest()
	short.ToneKnown = []bool{false}
	marked := maskedRequest()
	marked.Reading[1] = "ㄧˋ" // 聲調未知卻帶了聲調符號
	ab := maskedRequest()
	ab.Reading[0], ab.ToneKnown[0] = zhuyin.Abbreviate("ㄏ", 0), true
	for name, bad := range map[string]Request{"length": short, "marked": marked, "abbreviated": ab} {
		if Supported(bad) {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestSupportsChecksScorerConditions(t *testing.T) {
	r := maskedRequest()
	if (Options{}).Supports(r) {
		t.Fatal("v1 評分器收到 tone_known")
	}
	if !(Options{UnknownTones: true}).Supports(r) {
		t.Fatal("v2 評分器拒絕 tone_known")
	}
	if (Options{UnknownTones: true, Bases: map[zhuyin.Syllable]bool{"ㄧ": true}}).Supports(r) {
		t.Fatal("字表外的基底被送出")
	}
	toned := maskedRequest()
	toned.Reading, toned.ToneKnown = zhuyin.ParseReading("ㄏㄨㄟˋ ㄧˋ"), nil
	if !(Options{Bases: map[zhuyin.Syllable]bool{"ㄏㄨㄟ": true, "ㄧ": true}}).Supports(toned) {
		t.Fatal("全打聲調、基底都在表內卻被拒")
	}
}

func TestWeightFor(t *testing.T) {
	o := Options{Weight: 2}
	if o.WeightFor("") != 2 || o.WeightFor("前文") != 2 {
		t.Fatal("沒設 ContextlessWeight 一律用 Weight")
	}
	o = Options{Weight: MobileWeight, ContextlessWeight: MobileContextlessWeight}
	if o.WeightFor("") != .5 || o.WeightFor("前文") != 1 {
		t.Fatalf("手機：%v %v", o.WeightFor(""), o.WeightFor("前文"))
	}
}

func TestToneKnownWireFormat(t *testing.T) {
	r := maskedRequest()
	r.ToneKnown = nil
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "tone_known") {
		t.Fatalf("全部已知時不送 tone_known（舊協定逐位元組相同）：%s", b)
	}
	b, _ = json.Marshal(maskedRequest())
	if !strings.Contains(string(b), `"tone_known":[false,false]`) {
		t.Fatalf("%s", b)
	}
	if ToneKnownKey(maskedRequest()) == ToneKnownKey(r) || ToneKnownKey(r) != "" {
		t.Fatal("快取鍵要分開沒打聲調與一聲")
	}
}

func TestParseChars(t *testing.T) {
	c, err := ParseChars([]byte(`{"bases":["ㄅㄚ"],"characters":["八","\n","𠄞","\ufffd"]}`))
	if err != nil || len(c) != 4 || !c['八'] || !c['\n'] || !c['𠄞'] || !c['\ufffd'] {
		t.Fatalf("%v %v", c, err)
	}
	for _, bad := range []string{`{}`, `{"characters":[]}`, `{"characters":["八八"]}`, `{"characters":[""]}`, `not json`} {
		if _, err := ParseChars([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestKeepEncodable(t *testing.T) {
	rows := []decode.Hypothesis{{Text: "臉上"}, {Text: "臉𠄞"}, {Text: "連上"}}
	if got, ok := (Options{}).KeepEncodable(rows); !ok || len(got) != 3 {
		t.Fatalf("沒有字表時原樣：%v %v", got, ok)
	}
	o := Options{Chars: map[rune]bool{'臉': true, '上': true, '連': true}}
	got, ok := o.KeepEncodable(rows)
	if !ok || len(got) != 2 || got[0].Text != "臉上" || got[1].Text != "連上" || len(rows) != 3 || rows[1].Text != "臉𠄞" {
		t.Fatalf("只拿掉表外的候選、不改原陣列：%v %v %v", got, ok, rows)
	}
	if _, ok := o.KeepEncodable([]decode.Hypothesis{{Text: "臉𠄞"}, {Text: "臉上"}}); ok {
		t.Fatal("首選含表外字時整段不送")
	}
	r := Request{Reading: []zhuyin.Syllable{"ㄌㄧㄢˇ", "ㄕㄤˋ"}, Candidates: []decode.Hypothesis{
		{Text: "臉上", Segments: []decode.Seg{{Start: 0, End: 2, Word: "臉上"}}},
		{Text: "臉𠄞", Segments: []decode.Seg{{Start: 0, End: 2, Word: "臉𠄞"}}}}}
	if !(Options{}).Supports(r) || o.Supports(r) {
		t.Fatal("Supports 應拒絕含表外字的候選")
	}
}
