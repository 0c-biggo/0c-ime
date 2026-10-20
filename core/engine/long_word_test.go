package engine

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// #142：系統詞庫收 16 與 15 個音節的網路梗，超過一般詞的解碼上限 8。旋風照教育部辭典讀 ㄒㄩㄢˋ ㄈㄥ。
const (
	systemLong         = "新阿姆斯特朗旋風噴射阿姆斯特朗砲"
	systemLongReading  = "ㄒㄧㄣ " + systemShortReading
	systemShort        = "阿姆斯特朗旋風噴射阿姆斯特朗砲"
	systemShortReading = "ㄚ ㄇㄨˇ ㄙ ㄊㄜˋ ㄌㄤˇ ㄒㄩㄢˋ ㄈㄥ ㄆㄣ ㄕㄜˋ ㄚ ㄇㄨˇ ㄙ ㄊㄜˋ ㄌㄤˇ ㄆㄠˋ"
)

// longWordEngine 的系統詞庫有長詞與它的每個字、常見的短詞與同音字。機率取自重建的資料包：長詞沒有語料詞頻，
// 在平滑後的最低值 1.6e-8；新 4e-4、阿 3.9e-4、阿姆斯特朗 1.7e-6。withLong 為假時是沒有長詞的對照組：照字拼湊會打出常用的「炮」。
func longWordEngine(withLong bool) *Engine {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	add := func(w, r string, p float64) {
		lex.Add(lexicon.Entry{Surface: w, Reading: zhuyin.ParseReading(r), PRW: 1})
		sys.SetUni(w, p)
	}
	syls := zhuyin.ParseReading(systemLongReading)
	for i, r := range []rune(systemLong) {
		add(string(r), string(syls[i]), 1e-4)
	}
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"新", "ㄒㄧㄣ", 4e-4}, {"心", "ㄒㄧㄣ", 1e-4}, {"欣", "ㄒㄧㄣ", 1.4e-5}, {"啊", "ㄚ", 2e-4}, {"母", "ㄇㄨˇ", 1e-3},
		{"炮", "ㄆㄠˋ", 1e-3}, {"泡", "ㄆㄠˋ", 1e-3}, {"是", "ㄕˋ", 1e-2}, {"大", "ㄉㄚˋ", 1e-2}, {"大炮", "ㄉㄚˋ ㄆㄠˋ", 1e-5},
		{"阿姆斯特朗", "ㄚ ㄇㄨˇ ㄙ ㄊㄜˋ ㄌㄤˇ", 1.7e-6}, {"阿姆斯特丹", "ㄚ ㄇㄨˇ ㄙ ㄊㄜˋ ㄉㄢ", 4.4e-6}, {"丹", "ㄉㄢ", 1e-4},
		{"旋風", "ㄒㄩㄢˋ ㄈㄥ", 1e-5}, {"噴射", "ㄆㄣ ㄕㄜˋ", 1e-5}, {"阿", "ㄚ", 3.9e-4},
	} {
		add(e.w, e.r, e.p)
	}
	if withLong {
		add(systemLong, systemLongReading, 1.6e-8)
		add(systemShort, systemShortReading, 1.6e-8)
	}
	sys.SetBi("新", "阿", 2.1e-3) // 「新阿」解成「新｜阿」，和資料包相同（#143 的跨分段補全要測得到）
	return New(lex, sys)
}

func TestSystemLongWord(t *testing.T) {
	before := longWordEngine(false)
	if got := typeReading(before, before.OpenSession(), systemLongReading).Text(); got == systemLong {
		t.Fatal("沒有長詞時不該剛好打得出來，測試沒有意義")
	}
	e := longWordEngine(true)
	for _, c := range []struct{ reading, want string }{
		{systemLongReading, systemLong}, // 整個詞，不是「新」加不帶新的詞
		{systemShortReading, systemShort},
		{"ㄚ ㄇㄨˇ ㄙ ㄊㄜˋ ㄌㄤˇ", "阿姆斯特朗"},
		{"ㄚ ㄇㄨˇ ㄙ ㄊㄜˋ ㄉㄢ", "阿姆斯特丹"},
		{"ㄒㄧㄣ ㄚ ㄇㄨˇ ㄙ ㄊㄜˋ ㄌㄤˇ", "新阿姆斯特朗"},
		{systemLongReading + " ㄕˋ ㄉㄚˋ ㄆㄠˋ", systemLong + "是大炮"}, // 長詞後面照常接
		{"ㄕˋ " + systemShortReading, "是" + systemShort},         // 長詞前面照常接
	} {
		id := e.OpenSession()
		if got := typeReading(e, id, c.reading).Text(); got != c.want {
			t.Errorf("%s：%q，want %q", c.reading, got, c.want)
		}
		if snap, _ := e.Do(id, proto.Request{Type: proto.Key, Key: proto.KeyEnter}); snap.Commit != c.want {
			t.Errorf("%s 提交：%q，want %q", c.reading, snap.Commit, c.want)
		}
	}
}

// 打到「新阿姆斯特朗」「阿姆斯特朗旋」就提示長詞，按 Tab 收下：長詞（1.6e-8）勝過「新｜阿姆斯特朗」
// （4e-4 × 1.7e-6），解碼尾端成為長詞的前綴。只打「阿姆斯特朗」不提示長詞，「新阿」也不提示。
func TestSystemLongWordTab(t *testing.T) {
	e := longWordEngine(true)
	for _, r := range []string{"ㄚ ㄇㄨˇ ㄙ ㄊㄜˋ ㄌㄤˇ", "ㄒㄧㄣ ㄚ"} {
		if snap := typeReading(e, e.OpenSession(), r); snap.Suggestion != "" {
			t.Errorf("%s 不該提示長詞：%q", r, snap.Suggestion)
		}
	}
	for _, c := range []struct{ reading, want string }{
		{"ㄒㄧㄣ ㄚ ㄇㄨˇ ㄙ ㄊㄜˋ ㄌㄤˇ", systemLong},
		{"ㄚ ㄇㄨˇ ㄙ ㄊㄜˋ ㄌㄤˇ ㄒㄩㄢˋ", systemShort},
	} {
		id := e.OpenSession()
		if snap := typeReading(e, id, c.reading); snap.SuggestedText() != c.want {
			t.Errorf("%s 的提示：%q＋%q", c.reading, snap.Text(), snap.Suggestion)
		}
		snap, _ := e.Do(id, proto.Request{Type: proto.Key, Key: proto.KeyTab})
		if snap.Text() != c.want {
			t.Errorf("%s＋Tab：%q（建議 %q）", c.reading, snap.Text(), snap.Suggestion)
			continue
		}
		if snap, _ = e.Do(id, proto.Request{Type: proto.Key, Key: proto.KeyEnter}); snap.Commit != c.want {
			t.Errorf("%s＋Tab 提交：%q", c.reading, snap.Commit)
		}
	}
}
