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

const dayInSeconds = 86400

// newHabitSession 用 #218 回報時的分差：的／這個後面系統偏好「城市」（bigram 差二十倍），
// 改 是「改成」的前綴，「大」「這座」後面「城市」的系統優勢遠超過習慣分數。
func newHabitSession(t *testing.T) (*Session, *personal.Model) {
	t.Helper()
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"程式", "ㄔㄥˊ ㄕˋ", 1e-4}, {"城市", "ㄔㄥˊ ㄕˋ", 1.2e-4}, {"成", "ㄔㄥˊ", 3e-4}, {"是", "ㄕˋ", 1e-2}, {"市", "ㄕˋ", 1e-4}, {"式", "ㄕˋ", 1e-5},
		{"的", "ㄉㄜ˙", 3e-2}, {"這", "ㄓㄜˋ", 1e-3}, {"個", "ㄍㄜ˙", 1e-3}, {"這個", "ㄓㄜˋ ㄍㄜ˙", 1e-3}, {"大", "ㄉㄚˋ", 2e-3},
		{"改", "ㄍㄞˇ", 2e-4}, {"改成", "ㄍㄞˇ ㄔㄥˊ", 2e-2}, {"用", "ㄩㄥˋ", 1e-3}, {"寫", "ㄒㄧㄝˇ", 5e-4},
		{"過", "ㄍㄨㄛˋ", 1e-3}, {"過程", "ㄍㄨㄛˋ ㄔㄥˊ", 3e-4}, {"看", "ㄎㄢˋ", 1e-3}, {"跑", "ㄆㄠˇ", 1e-3}, {"新", "ㄒㄧㄣ", 1e-3}, {"修", "ㄒㄧㄡ", 1e-3}, {"嗎", "ㄇㄚˇ", 1e-3}, {"碼", "ㄇㄚˇ", 1e-5}, {"程式碼", "ㄔㄥˊ ㄕˋ ㄇㄚˇ", 1e-5},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	sys.SetBi("的", "城市", 1.1e-3)
	sys.SetBi("的", "程式", 5.3e-5)
	sys.SetBi("這個", "城市", 1e-3)
	sys.SetBi("這個", "程式", 1e-4)
	sys.SetBi("大", "城市", .02)
	sys.SetBi("新", "城市", 2e-3)
	sys.SetBi("修", "程式", .05)
	sys.SetBi("程式", "嗎", .02)
	sys.SetBi("過", "程式", 1e-5)
	sys.SetBi("過程", "是", .02)
	sys.SetBi("改成", "是", .5)
	sys.SetBi("改", "程式", 1e-3)
	m := personal.NewModel(lex, sys)
	m.AdvanceTime(1000 * dayInSeconds)
	s := New(m)
	s.Opt.PrefixAware = false
	return s, m
}

func habitShown(s *Session, reading string) string {
	s.Handle(proto.Request{Type: proto.Cancel})
	text := typeReadingLocal(s, reading).Text()
	s.Handle(proto.Request{Type: proto.Cancel})
	return text
}

// habitPickIn 打 leftReading＋ㄔㄥˊ ㄕˋ、選 word 再提交；left 為空字串時句首打。
func habitPickIn(t *testing.T, s *Session, leftReading string, at int, word string) {
	t.Helper()
	s.Handle(proto.Request{Type: proto.Cancel})
	typeReadingLocal(s, strings.TrimSpace(leftReading+" ㄔㄥˊ ㄕˋ"))
	selectText(t, s, at, word)
	s.Handle(proto.Request{Type: proto.Cursor, To: 1 << 30})
	commitText(s)
}

// #218：手選幾次「程式」之後，沒打過的語境也上來。
func TestReadingHabitAppliesInNewContext(t *testing.T) {
	s, m := newHabitSession(t)
	for _, c := range []struct{ reading, want string }{
		{"ㄉㄜ˙ ㄔㄥˊ ㄕˋ", "的城市"}, {"ㄓㄜˋ ㄍㄜ˙ ㄔㄥˊ ㄕˋ", "這個城市"},
	} {
		if got := habitShown(s, c.reading); got != c.want {
			t.Fatalf("測試前提：%s 應該是 %q，實得 %q", c.reading, c.want, got)
		}
	}
	// 第一天在「用」後面選一次：只有這個語境變，其他語境不變（單次選擇不蓋過任意語境）。
	habitPickIn(t, s, "ㄩㄥˋ", 1, "程式")
	if got := habitShown(s, "ㄓㄜˋ ㄍㄜ˙ ㄔㄥˊ ㄕˋ"); got != "這個城市" {
		t.Fatalf("單次手選外溢到別的語境：%q", got)
	}
	// 同一天在別的語境再選，還是只算一天。
	habitPickIn(t, s, "ㄒㄧㄝˇ", 1, "程式")
	if got := habitShown(s, "ㄓㄜˋ ㄍㄜ˙ ㄔㄥˊ ㄕˋ"); got != "這個城市" {
		t.Fatalf("同一天的手選破壞每日上限：%q", got)
	}
	// 隔天再選一次還不夠，
	m.AdvanceTime(m.Store.Time + dayInSeconds)
	habitPickIn(t, s, "ㄎㄢˋ", 1, "程式")
	if got := habitShown(s, "ㄓㄜˋ ㄍㄜ˙ ㄔㄥˊ ㄕˋ"); got != "這個城市" {
		t.Fatalf("第二天還不該生效：%q", got)
	}
	// 第三天再選一次：習慣成立。
	m.AdvanceTime(m.Store.Time + dayInSeconds)
	habitPickIn(t, s, "ㄆㄠˇ", 1, "程式")
	for _, c := range []struct{ reading, want string }{
		{"ㄉㄜ˙ ㄔㄥˊ ㄕˋ", "的程式"},
		{"ㄓㄜˋ ㄍㄜ˙ ㄔㄥˊ ㄕˋ", "這個程式"},
		{"ㄔㄥˊ ㄕˋ", "程式"},
		// 系統差距遠超過習慣分數的語境不受影響，
		{"ㄉㄚˋ ㄔㄥˊ ㄕˋ", "大城市"},
		// 也不拆完整詞：前面的詞、後面的詞、以及它自己是前綴的更長的詞。
		{"ㄔㄥˊ ㄕˋ ㄇㄚˇ", "程式碼"},
		{"ㄍㄨㄛˋ ㄔㄥˊ ㄕˋ", "過程是"},
		{"ㄍㄞˇ ㄔㄥˊ ㄕˋ", "改成是"},
		{"ㄍㄞˇ ㄔㄥˊ", "改成"},
	} {
		if got := habitShown(s, c.reading); got != c.want {
			t.Errorf("%s = %q，want %q", c.reading, got, c.want)
		}
	}
}

// 「改程式」：改 是 改成 的前綴。沒打過這個配對不拆（上面的測試），打過之後，前綴保護不該擋住使用者自己的切法。
func TestReadingHabitOvercomesPrefixGuardOnlyForObservedPair(t *testing.T) {
	s, m := newHabitSession(t)
	for _, left := range []string{"ㄩㄥˋ", "ㄒㄧㄝˇ", "ㄎㄢˋ"} {
		habitPickIn(t, s, left, 1, "程式")
		m.AdvanceTime(m.Store.Time + dayInSeconds)
	}
	if got := habitShown(s, "ㄍㄞˇ ㄔㄥˊ ㄕˋ"); got != "改成是" {
		t.Fatalf("沒打過的配對不該拆完整詞：%q", got)
	}
	// 一般計數裡「改」用過很多次，前綴保護照常壓它，壓的量超過一份習慣分數。
	for range 200 {
		m.Learn(personal.Commit{Syls: zhuyin.ParseReading("ㄍㄞˇ"), Path: []personal.Seg{{Word: "改", End: 1}}})
	}
	// 只在這個配對被動提交過（跨提交或手動切開的結果），沒有這個語境的更正。
	m.Learn(personal.Commit{Syls: zhuyin.ParseReading("ㄍㄞˇ ㄔㄥˊ ㄕˋ"), Path: []personal.Seg{{Word: "改", End: 1}, {Word: "程式", Start: 1, End: 3}}})
	if got := habitShown(s, "ㄍㄞˇ ㄔㄥˊ ㄕˋ"); got != "改程式" {
		t.Fatalf("打過的配對應該上來：%q", got)
	}
	if got := habitShown(s, "ㄍㄨㄛˋ ㄔㄥˊ ㄕˋ"); got != "過程是" {
		t.Fatalf("別的完整詞不受影響：%q", got)
	}
	if got := habitShown(s, "ㄍㄞˇ ㄔㄥˊ"); got != "改成" {
		t.Fatalf("改成 本身不該受影響：%q", got)
	}
}

// 統計與神經共用同一套語意：習慣讓統計首選換上來時，評分器不得換回去；習慣退掉之後交回評分器。
func TestReadingHabitAndNeuralShareTheSameSemantics(t *testing.T) {
	s, m := newHabitSession(t)
	s.Rerank = rerank.Options{Weight: 2, HintGap: 100, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			// 評分器照語料偏好「的城市」，但兩個寫法都說得通；「新」後面則清楚指向城市。
			scores[i] = 4 * float64(strings.Count(c.Text, "城市"))
			if strings.Contains(c.Text, "新城市") || strings.Contains(c.Text, "修城市") {
				scores[i] = 7
			}
		}
		return scores, nil
	})}
	reading := "ㄉㄜ˙ ㄔㄥˊ ㄕˋ"
	if got := habitShown(s, reading); got != "的城市" {
		t.Fatalf("測試前提：%q", got)
	}
	for _, left := range []string{"ㄩㄥˋ", "ㄒㄧㄝˇ", "ㄎㄢˋ", "ㄆㄠˇ"} { // 同一個前文選兩次會晉升成個人片語，之後由片語計數
		at := 1
		habitPickIn(t, s, left, at, "程式")
		m.AdvanceTime(m.Store.Time + dayInSeconds)
	}
	if got := habitShown(s, reading); got != "的程式" {
		t.Fatalf("習慣成立後神經不該換回去：%q", got)
	}
	// 評分器有壓倒性的把握時聽它：習慣是弱偏好，不是硬否決（城市交通壅塞）。
	if got := habitShown(s, "ㄒㄧㄣ ㄔㄥˊ ㄕˋ"); got != "新城市" {
		t.Fatalf("評分器清楚指向另一個詞時不該擋：%q", got)
	}
	// 不靠習慣分數也是首選的詞，照原本的規則保護（#96），評分器再有把握也不換。
	if got := habitShown(s, "ㄒㄧㄡ ㄔㄥˊ ㄕˋ"); got != "修程式" {
		t.Fatalf("本來就是首選的詞被換掉：%q", got)
	}
	// 在別的語境選一次「城市」：兩種都選過，以前會整個交回評分器；習慣還在時不交回。
	habitPickIn(t, s, "ㄓㄜˋ ㄍㄜ˙", 2, "城市")
	if got := habitShown(s, reading); got != "的程式" {
		t.Fatalf("一次反向選取不該讓評分器把習慣換回去：%q", got)
	}
	// 那個語境本身照使用者剛選的。
	if got := habitShown(s, "ㄓㄜˋ ㄍㄜ˙ ㄔㄥˊ ㄕˋ"); got != "這個城市" {
		t.Fatalf("有語境的更正優先：%q", got)
	}
	// 半年沒打：習慣退掉，回到評分器。沒有留下硬保護。
	m.AdvanceTime(m.Store.Time + 180*dayInSeconds)
	if got := habitShown(s, reading); got != "的城市" {
		t.Fatalf("習慣退掉後應該交回評分器：%q", got)
	}
}

// habitOverride 把系統二元機率改成指定的分差，並以這些語境建立「程式」的習慣（用、寫、看、跑各選一次，跨四天）。
func habitOverride(t *testing.T, bi map[[2]string]float64) (*Session, *personal.Model) {
	t.Helper()
	s, m := newHabitSession(t)
	sys := m.LM.(*lm.Mem)
	for k, p := range bi {
		sys.SetBi(k[0], k[1], p)
	}
	for _, left := range []string{"ㄩㄥˋ", "ㄒㄧㄝˇ", "ㄎㄢˋ", "ㄆㄠˇ"} {
		habitPickIn(t, s, left, 1, "程式")
		m.AdvanceTime(m.Store.Time + dayInSeconds)
	}
	return s, m
}

// 解碼器與評分器的判斷要看同一份分數：前綴保護下解碼器沒有加分（Decoder.HabitLift 是 0），詞贏了就是一般的首選，手選保護照 #96；
// 打過的切法解碼器加兩份，分差落在一份到兩份之間也是靠習慣。
func TestReadingHabitJudgedByDecoderLift(t *testing.T) {
	strong := rankFunc(func(r rerank.Request) ([]float64, error) {
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			scores[i] = 20 * float64(strings.Count(c.Text, "城市"))
		}
		return scores, nil
	})
	mild := rankFunc(func(r rerank.Request) ([]float64, error) {
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			scores[i] = 4 * float64(strings.Count(c.Text, "城市")) // 乘上權重 2 是 8，在 habitNeuralMargin 以內
		}
		return scores, nil
	})
	// lift 回報統計首選裡「程式」實際拿到的分數，以及它和換成城市的候選的分差。
	lift := func(t *testing.T, s *Session, r string) (got, gap float64) {
		t.Helper()
		typeReadingLocal(s, r)
		defer s.Handle(proto.Request{Type: proto.Cancel})
		lat := s.lattice()
		s.dec.Evidence = nil
		rows, _ := s.dec.NBest(lat, nil, s.Opt.PrefixAware, 8)
		if len(rows) == 0 {
			t.Fatalf("%s 沒有候選", r)
		}
		found := false
		for _, g := range rows[0].Segments {
			if g.Word == "程式" {
				got, found = s.dec.HabitLift(lat, rows[0], g, zhuyin.ParseReading("ㄔㄥˊ ㄕˋ")), true
			}
		}
		if !found {
			t.Fatalf("測試前提：%s 的統計首選要有程式：%q", r, rows[0].Text)
		}
		for _, h := range rows[1:] {
			if strings.Contains(h.Text, "城市") {
				return got, rows[0].LogScore - h.LogScore
			}
		}
		t.Fatalf("測試前提：%s 的候選要有城市：%v", r, rows)
		return 0, 0
	}
	full := func(m *personal.Model) float64 { return m.ReadingLift("程式", zhuyin.ParseReading("ㄔㄥˊ ㄕˋ")) }

	t.Run("詞本身是更長完整詞的前綴", func(t *testing.T) {
		s, m := habitOverride(t, map[[2]string]float64{
			{"修", "程式"}: 1e-3, {"修", "城市"}: 4e-4, {"程式", "嗎"}: .02, {"城市", "嗎"}: .02,
		})
		r := "ㄒㄧㄡ ㄔㄥˊ ㄕˋ ㄇㄚˇ"
		got, gap := lift(t, s, r)
		if got != 0 || full(m) <= 0 || gap > full(m) {
			t.Fatalf("測試前提：解碼器沒有加分、分差在原始習慣分數以內：got %v gap %v 原始 %v", got, gap, full(m))
		}
		s.Rerank = rerank.Options{Weight: 2, HintGap: 100, Scorer: strong}
		if got := habitShown(s, r); got != "修程式嗎" {
			t.Errorf("沒靠習慣的手選詞要照 #96 保護：%q", got)
		}
	})
	t.Run("前一詞是完整詞的前綴、沒打過這個切法", func(t *testing.T) {
		s, m := habitOverride(t, map[[2]string]float64{
			{"改", "程式"}: .5, {"改", "城市"}: .2, {"改成", "是"}: 1e-5,
		})
		r := "ㄍㄞˇ ㄔㄥˊ ㄕˋ"
		got, gap := lift(t, s, r)
		if got != 0 || full(m) <= 0 || gap > full(m) {
			t.Fatalf("測試前提：解碼器沒有加分、分差在原始習慣分數以內：got %v gap %v 原始 %v", got, gap, full(m))
		}
		s.Rerank = rerank.Options{Weight: 2, HintGap: 100, Scorer: strong}
		if got := habitShown(s, r); got != "改程式" {
			t.Errorf("沒靠習慣的手選詞要照 #96 保護：%q", got)
		}
	})
	t.Run("打過的切法加兩份", func(t *testing.T) {
		s, m := habitOverride(t, map[[2]string]float64{
			{"改", "程式"}: .02, {"改", "城市"}: .45, {"改成", "是"}: 1e-5,
		})
		m.Learn(personal.Commit{Syls: zhuyin.ParseReading("ㄍㄞˇ ㄔㄥˊ ㄕˋ"), Path: []personal.Seg{{Word: "改", End: 1}, {Word: "程式", Start: 1, End: 3}}})
		habitPickIn(t, s, "ㄓㄜˋ ㄍㄜ˙", 2, "城市") // 兩種都選過：#96 的保護不管，只剩習慣擋得住
		r := "ㄍㄞˇ ㄔㄥˊ ㄕˋ"
		got, gap := lift(t, s, r)
		if want := 2 * full(m); got != want || gap <= full(m) || gap > want {
			t.Fatalf("測試前提：解碼器加兩份、分差落在一份到兩份之間：got %v want %v gap %v", got, want, gap)
		}
		s.Rerank = rerank.Options{Weight: 2, HintGap: 100, Scorer: mild}
		if got := habitShown(s, r); got != "改程式" {
			t.Errorf("靠兩份習慣分數當上首選的詞，評分器沒有壓倒性把握時不換：%q", got)
		}
	})
}

// 空白個人資料不受影響。
func TestReadingHabitNoEffectWithoutPicks(t *testing.T) {
	s, m := newHabitSession(t)
	for range 30 {
		m.Learn(personal.Commit{Syls: zhuyin.ParseReading("ㄔㄥˊ ㄕˋ"), Path: []personal.Seg{{Word: "程式", End: 2}}})
		m.AdvanceTime(m.Store.Time + dayInSeconds)
	}
	if len(m.Store.Confirmed) != 0 {
		t.Fatalf("被動提交不該建立手選紀錄：%v", m.Store.Confirmed)
	}
	if got := m.ReadingLift("程式", zhuyin.ParseReading("ㄔㄥˊ ㄕˋ")); got != 0 {
		t.Fatalf("沒有手選不該有分數：%v", got)
	}
	_ = s
}
