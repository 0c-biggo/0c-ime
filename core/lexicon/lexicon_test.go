package lexicon

import (
	"strings"
	"testing"

	"git.biggo.com/0c/ime/core/zhuyin"
)

func TestLoadValidates(t *testing.T) {
	good := "# 註解\n丼\tㄉㄨㄥˋ\n丼\tㄐㄧㄥˇ\t0.1\n應該\tㄧㄣ ㄍㄞ\t0.3\tcorrection\n"
	m, err := Load(strings.NewReader(good))
	if err != nil {
		t.Fatal(err)
	}
	if es := m.Exact(zhuyin.ParseReading("ㄧㄣ ㄍㄞ")); len(es) != 1 || !es[0].Correction || es[0].PRW != 0.3 {
		t.Errorf("%+v", es)
	}
	if es := m.Prefixed(zhuyin.ParseReading("ㄧㄣ")); len(es) != 1 {
		t.Errorf("前綴查詢：%+v", es)
	}
	if !m.Has("丼") || m.Has("景") || !m.HasPrefix(zhuyin.ParseReading("ㄧㄣ")) || m.HasPrefix(zhuyin.ParseReading("ㄍㄞ")) {
		t.Error("Has／HasPrefix")
	}
	for name, bad := range map[string]string{
		"音節數與字數不符": "親子丼\tㄑㄧㄣ ㄗˇ\n",
		"音節不合法":    "丼\tㄉㄨㄥx\n",
		"機率超出範圍":   "丼\tㄉㄨㄥˋ\t1.5\n",
		"缺少讀音":     "丼\n",
	} {
		if _, err := Load(strings.NewReader(bad)); err == nil {
			t.Errorf("%s：應回報錯誤", name)
		}
	}
}

// #142：系統詞庫回報最長的詞條讀音，讓解碼器在有長詞時搜尋到 8 個音節以後。
// Compact 由詞條長度算出，不寫進資料檔：舊資料包讀進來一樣有值，格式不變。
func TestLongestWord(t *testing.T) {
	long := "阿姆斯特朗回旋加速噴氣式阿姆斯特朗炮"
	reading := "ㄚ ㄇㄨˇ ㄙ ㄊㄜˋ ㄌㄤˇ ㄏㄨㄟˊ ㄒㄩㄢˊ ㄐㄧㄚ ㄙㄨˋ ㄆㄣ ㄑㄧˋ ㄕˋ ㄚ ㄇㄨˇ ㄙ ㄊㄜˋ ㄌㄤˇ ㄆㄠˋ"
	es := []Entry{
		{Surface: "阿姆斯特朗", Reading: zhuyin.ParseReading("ㄚ ㄇㄨˇ ㄙ ㄊㄜˋ ㄌㄤˇ"), PRW: 1},
		{Surface: "喔", Reading: zhuyin.ParseReading("ㄛ"), PRW: 1},
	}
	short, err := Build(es)
	if err != nil {
		t.Fatal(err)
	}
	if got := Longest(short); got != 5 {
		t.Errorf("短詞庫：%d", got)
	}
	es = append(es, Entry{Surface: long, Reading: zhuyin.ParseReading(reading), PRW: 1})
	mem := NewMem()
	for _, e := range es {
		mem.Add(e)
	}
	built, err := Build(es)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(built.Encode())
	if err != nil {
		t.Fatal(err)
	}
	for name, src := range map[string]Source{"Mem": mem, "Build": built, "Decode": decoded, "輕聲語氣詞": NeutralParticles(decoded)} {
		if got := Longest(src); got != 18 {
			t.Errorf("%s：%d", name, got)
		}
	}
}
