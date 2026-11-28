package session

import (
	"slices"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// newMinorReadingSession 是 #373 的測試詞庫，讀音權重與詞頻照 0.2.13 資料包的量級：
// 「鞥」主讀音 ㄥ、次要讀音 ㄧˋ；「喔」主讀音 ㄛ、次要讀音 ㄨ 與 ㄨㄛˋ；「哈」主讀音 ㄏㄚ、次要讀音 ㄎㄚ（都是 0.1）。
func newMinorReadingSession(toneless bool) *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r     string
		uni, bos float64
		prw      float64
	}{
		{"鞥", "ㄥ", 2.0e-8, 1.0e-8, 1}, {"鞥", "ㄧˋ", 2.0e-8, 1.0e-8, 0.1},
		{"一", "ㄧ", 4.196e-3, 4.090e-4, 1}, {"一", "ㄧˊ", 4.196e-3, 4.090e-4, 0.5}, {"一", "ㄧˋ", 4.196e-3, 4.090e-4, 0.5},
		{"以", "ㄧˇ", 2.382e-3, 1.501e-3, 1}, {"億", "ㄧˋ", 3.071e-4, 1.125e-3, 1}, {"意", "ㄧˋ", 2.0e-4, 5.0e-5, 1},
		{"喔", "ㄛ", 2.0e-5, 1.0e-5, 1}, {"喔", "ㄨ", 2.0e-5, 1.0e-5, 0.1}, {"喔", "ㄨㄛˋ", 2.0e-5, 1.0e-5, 0.1},
		{"我", "ㄨㄛˇ", 5.418e-3, 1.735e-3, 1}, {"沃", "ㄨㄛˋ", 1.370e-5, 4.357e-5, 1}, {"握", "ㄨㄛˋ", 1.2e-5, 3.0e-6, 1},
		{"烏", "ㄨ", 6.0e-5, 8.0e-5, 1}, {"屋", "ㄨ", 5.0e-5, 2.0e-5, 1}, {"無", "ㄨˊ", 1.2e-3, 4.0e-4, 1},
		{"哈", "ㄏㄚ", 1.0e-4, 5.0e-5, 1}, {"哈", "ㄎㄚ", 1.0e-4, 5.0e-5, 0.1}, {"咖", "ㄎㄚ", 3.0e-5, 2.0e-5, 1},
		{"是", "ㄕˋ", 8.586e-3, 2.489e-3, 1}, {"好", "ㄏㄠˇ", 8.229e-4, 3.540e-4, 1},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: e.prw})
		sys.SetUni(e.w, e.uni)
		sys.SetBi(lm.BOS, e.w, e.bos)
	}
	sys.SetBi("我", "是", 0.03)
	sys.SetBi("我", "好", 0.01)
	s := New(personal.NewModel(lex, sys))
	s.Toneless = toneless
	return s
}

// sendAlone 單獨打一個音節（帶聲調，空白是一聲）送出 times 次，確認送出的是 want。
func sendAlone(t *testing.T, s *Session, typed, want string, times int) {
	t.Helper()
	for range times {
		typeBare(s, typed)
		if snap := key(s, proto.KeyEnter); snap.Commit != want {
			t.Fatalf("%s 送出 %q，應為 %q", typed, snap.Commit, want)
		}
	}
}

// #373：單獨送出過的字（計數記在詞面上，不分讀音）不能靠次要讀音跑到別的音的第一個。
// 回報的情境是學過「鞥」（ㄥ）之後單打 ㄧ 變成「鞥」；常見的是聊天時單獨回「喔」「哈」，之後 ㄨㄛ 變成「喔」、ㄎㄚ 變成「哈」。
func TestWordLearnedAloneDoesNotTakeOverItsMinorReading(t *testing.T) {
	for _, c := range []struct {
		learn, word string // 單獨送出的音節與字
		typed       string // 之後單獨打的音節
		toneless    bool
		want        string // 第一個候選、Enter 送出的字（沒學過時的結果）
	}{
		{"ㄥ ", "鞥", "ㄧ", true, "一"},
		{"ㄥ ", "鞥", "ㄧˋ", true, "億"},
		{"ㄥ ", "鞥", "ㄧˋ", false, "億"},
		{"ㄛ ", "喔", "ㄨㄛ", true, "我"},
		{"ㄛ ", "喔", "ㄨ", true, "無"},
		{"ㄛ ", "喔", "ㄨ ", false, "烏"},
		{"ㄛ ", "喔", "ㄨㄛˋ", false, "沃"},
		{"ㄏㄚ ", "哈", "ㄎㄚ ", false, "咖"},
	} {
		for _, times := range []int{1, 3, 12} {
			clean := loneCandidates(t, newMinorReadingSession(c.toneless), c.typed)
			if clean[0] != c.want {
				t.Fatalf("測試詞庫：沒學過時 %s 的候選 %v，第一個應為 %q", c.typed, clean, c.want)
			}
			s := newMinorReadingSession(c.toneless)
			sendAlone(t, s, c.learn, c.word, times)
			if got := loneCandidates(t, s, c.typed); !slices.Equal(got, clean) {
				t.Errorf("單獨送出 %d 次「%s」之後 %s 的候選 %v，應與沒學過時相同 %v", times, c.word, c.typed, got, clean)
			}
			s.Handle(proto.Request{Type: proto.Cancel})
			typeBare(s, c.typed)
			if snap := key(s, proto.KeyEnter); snap.Commit != c.want {
				t.Errorf("單獨送出 %d 次「%s」之後 %s 按 Enter 送出 %q，應為 %q", times, c.word, c.typed, snap.Commit, c.want)
			}
		}
	}
}

// 句子開頭也一樣：單獨回過幾次「喔」的人不打聲調打 ㄨㄛ ㄕ，打到一半組字條不該先顯示「喔」，送出是「我是」。
func TestWordLearnedAloneDoesNotStartSentenceByMinorReading(t *testing.T) {
	s := newMinorReadingSession(true)
	sendAlone(t, s, "ㄛ ", "喔", 10)
	if snap := typeBare(s, "ㄨㄛㄕ"); snap.Text() != "我ㄕ" {
		t.Errorf("打到 ㄨㄛㄕ 時組字是 %q，應為「我ㄕ」", snap.Text())
	}
	if snap := key(s, proto.KeyEnter); snap.Commit != "我是" {
		t.Errorf("送出 %q，應為「我是」", snap.Commit)
	}
}

// 照舊：字自己的主讀音照常學（ㄨㄛˋ 單獨送出過「握」就排到「沃」前面），
// 親手用次要讀音選過的字也照常記得（打 ㄧˋ 選了「鞥」，下次 ㄧˋ 就是「鞥」）。
func TestMinorReadingKeepsOrdinaryLearning(t *testing.T) {
	s := newMinorReadingSession(false)
	if got := loneCandidates(t, s, "ㄨㄛˋ"); !slices.Equal(got[:2], []string{"沃", "握"}) {
		t.Fatalf("測試詞庫：ㄨㄛˋ 的候選 %v", got)
	}
	id := slices.Index(texts(s.snapshot().Candidates.Items), "握")
	s.Handle(proto.Request{Type: proto.Select, Index: s.snapshot().Candidates.Items[id].ID})
	if snap := key(s, proto.KeyEnter); snap.Commit != "握" {
		t.Fatalf("選握後送出 %q", snap.Commit)
	}
	if got := loneCandidates(t, s, "ㄨㄛˋ"); got[0] != "握" {
		t.Errorf("選過握之後 ㄨㄛˋ 的候選 %v，第一個應為握", got)
	}

	for _, toneless := range []bool{false, true} {
		s = newMinorReadingSession(toneless)
		got := loneCandidates(t, s, "ㄧˋ")
		id = slices.Index(got, "鞥")
		if id <= 0 {
			t.Fatalf("測試詞庫：ㄧˋ 的候選 %v 應有鞥、且不在第一個", got)
		}
		s.Handle(proto.Request{Type: proto.Select, Index: s.snapshot().Candidates.Items[id].ID})
		if snap := key(s, proto.KeyEnter); snap.Commit != "鞥" {
			t.Fatalf("選鞥後送出 %q", snap.Commit)
		}
		if got := loneCandidates(t, s, "ㄧˋ"); got[0] != "鞥" {
			t.Errorf("聲調選填 %v：用 ㄧˋ 選過鞥之後 ㄧˋ 的候選 %v，第一個應為鞥", toneless, got)
		}
	}
}

// newTownshipSession 是 #373 後續（cv2136「雲林縣鄉道」）的測試詞庫，量級照 0.2.13 資料包：「鄉」主讀音 ㄒㄧㄤ、
// 可考讀音 ㄒㄧㄤˋ（0.06）；不打聲調的 ㄒㄧㄤ ㄉㄠ 另有完整詞「想到」跨過「鄉」的位置（前綴保護）。
func newTownshipSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		uni  float64
		prw  float64
	}{
		{"雲林縣", "ㄩㄣˊ ㄌㄧㄣˊ ㄒㄧㄢˋ", 2.0e-6, 1},
		{"鄉", "ㄒㄧㄤ", 6.0e-5, 1}, {"鄉", "ㄒㄧㄤˋ", 6.0e-5, 0.06},
		{"香", "ㄒㄧㄤ", 8.0e-5, 1}, {"想", "ㄒㄧㄤˇ", 1.2e-3, 1}, {"像", "ㄒㄧㄤˋ", 6.0e-4, 1}, {"向", "ㄒㄧㄤˋ", 3.0e-4, 1},
		{"想到", "ㄒㄧㄤˇ ㄉㄠˋ", 1.5e-4, 1},
		{"到", "ㄉㄠˋ", 3.0e-3, 1}, {"道", "ㄉㄠˋ", 4.0e-4, 1}, {"刀", "ㄉㄠ", 3.0e-5, 1},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: e.prw})
		sys.SetUni(e.w, e.uni)
	}
	sys.SetBi(lm.BOS, "雲林縣", 2.0e-6)
	sys.SetBi("雲林縣", "鄉", 1.2e-5)
	sys.SetBi("雲林縣", "想", 1.3e-4)
	sys.SetBi("雲林縣", "像", 1.2e-4)
	sys.SetBi("雲林縣", "想到", 1.7e-5)
	sys.SetBi("想", "到", 1.3e-3)
	sys.SetBi("鄉", "到", 8.0e-4)
	sys.SetBi("鄉", "道", 2.0e-3)
	s := New(personal.NewModel(lex, sys))
	s.Toneless = true
	return s
}

// #373 的後續：次要讀音的上限不能讓學過的句子不打聲調打不回來。打聲調送出過「雲林縣鄉道」的人不打聲調再打一次，
// 「鄉」這個位置同時有主讀音 ㄒㄧㄤ 與可考讀音 ㄒㄧㄤˋ 的弧，打的鍵就是他平常打「鄉」的鍵，學到的「雲林縣→鄉」照常計分。
func TestMinorReadingCapSparesTonelessSyllableOfMajorReading(t *testing.T) {
	const toned, toneless = "ㄩㄣˊㄌㄧㄣˊㄒㄧㄢˋㄒㄧㄤ ㄉㄠˋ", "ㄩㄣㄌㄧㄣㄒㄧㄢㄒㄧㄤㄉㄠ"
	s := newTownshipSession()
	typeBare(s, toneless)
	if snap := key(s, proto.KeyEnter); snap.Commit != "雲林縣想到" {
		t.Fatalf("測試詞庫：沒學過時不打聲調送出 %q，應為「雲林縣想到」", snap.Commit)
	}
	s = newTownshipSession()
	typeBare(s, toned)
	if snap := key(s, proto.KeyEnter); snap.Commit != "雲林縣鄉道" {
		t.Fatalf("測試詞庫：打聲調送出 %q，應為「雲林縣鄉道」", snap.Commit)
	}
	typeBare(s, toneless)
	if snap := key(s, proto.KeyEnter); snap.Commit != "雲林縣鄉道" {
		t.Errorf("學過「雲林縣鄉道」之後不打聲調送出 %q，應為「雲林縣鄉道」", snap.Commit)
	}
	// 照舊：明確打四聲 ㄒㄧㄤˋ 時「鄉」是次要讀音，學到的計數不算，還是「像」。
	typeBare(s, "ㄩㄣˊㄌㄧㄣˊㄒㄧㄢˋㄒㄧㄤˋ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "雲林縣像" {
		t.Errorf("學過「雲林縣鄉道」之後打 ㄒㄧㄤˋ 送出 %q，應為「雲林縣像」", snap.Commit)
	}
}
