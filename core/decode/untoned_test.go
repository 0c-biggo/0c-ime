package decode

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 聲調選填（#150）：沒打聲調的位置每組（起點、終點）只留機率最高的 maxUntonedArcs 個弧，
// 有打聲調的位置不裁切；鎖定碰到的範圍不裁切，鎖定的字一定還在。
func TestUntonedArcsAreCappedPerSpan(t *testing.T) {
	lex := lexicon.NewMem()
	score := uni{}
	tones := []string{"", "ˊ", "ˇ", "ˋ", "˙"}
	for k := 0; k < 3*maxUntonedArcs; k++ {
		w := string(rune(0x4e00 + k))
		lex.Add(lexicon.Entry{Surface: w, Reading: zhuyin.ParseReading("ㄧ" + tones[k%5]), PRW: 1})
		score[w] = 1e-3 / float64(k+1)
	}
	d := &Decoder{Lex: lex, Score: score}
	toned := input.Exact{}.Expand([]zhuyin.Syllable{"ㄧˋ"})
	if n := len(d.wordArcs(toned, 0, nil)); n != 3*maxUntonedArcs/5 {
		t.Fatalf("有打聲調不裁切：%d", n)
	}
	untoned := input.Exact{}.Expand([]zhuyin.Syllable{zhuyin.Untone("ㄧ")})
	arcs := d.wordArcs(untoned, 0, nil)
	if len(arcs) != maxUntonedArcs {
		t.Fatalf("沒打聲調裁到 %d 個：%d", maxUntonedArcs, len(arcs))
	}
	for k, a := range arcs {
		if want := string(rune(0x4e00 + k)); a.e.Surface != want {
			t.Fatalf("依機率留前面的：第 %d 個是 %q，應為 %q", k, a.e.Surface, want)
		}
	}
	rare := string(rune(0x4e00 + 3*maxUntonedArcs - 1))
	locks := map[int]Lock{0: {Word: rare, Len: 1}}
	if got := text(d.Decode(untoned, locks, false)); got != rare {
		t.Fatalf("鎖定的罕用字不能被裁掉：%q", got)
	}
	if got := text(d.Decode(untoned, nil, false)); got != "一" {
		t.Fatalf("沒鎖定時取機率最高的：%q", got)
	}
}

// decode.Reading 把沒打聲調的音節換回詞條的讀音；有打聲調的位置要相同，詞庫沒有的詞找不到。
func TestReadingResolvesUntonedSyllables(t *testing.T) {
	lex := lexicon.NewMem()
	for _, e := range []struct{ w, r string }{{"會議", "ㄏㄨㄟˋ ㄧˋ"}, {"回憶", "ㄏㄨㄟˊ ㄧˋ"}, {"行", "ㄒㄧㄥˊ"}, {"行", "ㄏㄤˊ"}} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
	}
	lat := input.Exact{}.Expand([]zhuyin.Syllable{zhuyin.Untone("ㄏㄨㄟ"), zhuyin.Untone("ㄧ")})
	for w, want := range map[string]string{"會議": "ㄏㄨㄟˋ ㄧˋ", "回憶": "ㄏㄨㄟˊ ㄧˋ"} {
		if r, ok := Reading(lex, lat, 0, 2, w); !ok || zhuyin.Key(r) != want {
			t.Fatalf("%s：%v %v", w, r, ok)
		}
	}
	if _, ok := Reading(lex, lat, 0, 2, "灰衣"); ok {
		t.Fatal("詞庫沒有的詞")
	}
	mixed := input.Exact{}.Expand([]zhuyin.Syllable{"ㄏㄨㄟˋ", zhuyin.Untone("ㄧ")})
	if _, ok := Reading(lex, mixed, 0, 2, "回憶"); ok {
		t.Fatal("有打的聲調要相同")
	}
	if r, ok := Reading(lex, input.Exact{}.Expand([]zhuyin.Syllable{zhuyin.Untone("ㄒㄧㄥ")}), 0, 1, "行"); !ok || r[0] != "ㄒㄧㄥˊ" {
		t.Fatalf("同一個字取符合打的音的讀音：%v %v", r, ok)
	}
}
