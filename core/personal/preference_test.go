package personal

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

const habitReading = "ㄔㄥˊ ㄕˋ"

func habitModel() *Model {
	lex := lexicon.NewMem()
	for _, e := range []struct{ w, r string }{
		{"程式", habitReading}, {"城市", habitReading}, {"的", "ㄉㄜ˙"}, {"用", "ㄩㄥˋ"}, {"寫", "ㄒㄧㄝˇ"}, {"看", "ㄎㄢˋ"},
		{"這個", "ㄓㄜˋ ㄍㄜ˙"}, {"獨詞", "ㄉㄨˊ ㄘˊ"}, {"在", "ㄗㄞˋ"}, {"再", "ㄗㄞˋ"},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
	}
	m := NewModel(lex, lm.NewMem())
	m.AdvanceTime(100 * daySeconds)
	return m
}

func nextDay(m *Model) { m.AdvanceTime(m.Store.Time + daySeconds) }

// habitPick 在前詞 left（讀音 leftReading）之後手選 word。不同前文代表不同語境。
func habitPick(m *Model, left, leftReading, word string) {
	syls := zhuyin.ParseReading(leftReading + " " + habitReading)
	n := len(syls) - 2
	m.Learn(Commit{Syls: syls, Path: []Seg{{Word: left, End: n}, {Word: word, Start: n, End: n + 2, Manual: true}}})
}

// habitUse 被動提交 word：沒有手選，首選是什麼就送出什麼。
func habitUse(m *Model, word string) {
	m.Learn(Commit{Syls: zhuyin.ParseReading(habitReading), Path: []Seg{{Word: word, End: 2}}})
}

func habitLiftOf(m *Model, word string) float64 {
	return m.ReadingLift(word, zhuyin.ParseReading(habitReading))
}

var habitContexts = [][2]string{{"的", "ㄉㄜ˙"}, {"用", "ㄩㄥˋ"}, {"寫", "ㄒㄧㄝˇ"}, {"看", "ㄎㄢˋ"}, {"這個", "ㄓㄜˋ ㄍㄜ˙"}}

// 單次選擇不能蓋過任意語境，連同一天大量使用也不行。
func TestHabitNeedsMoreThanOnePick(t *testing.T) {
	m := habitModel()
	habitPick(m, "的", "ㄉㄜ˙", "程式")
	if got := habitLiftOf(m, "程式"); got != 0 {
		t.Fatalf("手選一次就有習慣分 %v", got)
	}
	for range 40 {
		habitUse(m, "程式")
	}
	if got := habitLiftOf(m, "程式"); got != 0 {
		t.Fatalf("一次手選加同一天的大量使用不該有習慣分：%v", got)
	}
}

// 反例（#218 留言）：同一天分散在很多語境各選一次，直接加總 Long 會破壞每日上限。
func TestHabitDailyCapAcrossContexts(t *testing.T) {
	m := habitModel()
	for i := range 12 {
		c := habitContexts[i%len(habitContexts)]
		habitPick(m, c[0], c[1], "程式")
	}
	if e := m.Store.Confirmed[pair("程式", habitReading)]; e.Long > 1 {
		t.Fatalf("同一天的手選超過每日上限：Long=%v", e.Long)
	}
	if got := habitLiftOf(m, "程式"); got != 0 {
		t.Fatalf("同一天再多語境都只算一天：%v", got)
	}
	// 隔天只再選一次還不夠，要到第三天。
	nextDay(m)
	habitPick(m, "的", "ㄉㄜ˙", "程式")
	if got := habitLiftOf(m, "程式"); got != 0 {
		t.Fatalf("第二天還不該生效：%v", got)
	}
	nextDay(m)
	habitPick(m, "用", "ㄩㄥˋ", "程式")
	if got := habitLiftOf(m, "程式"); got <= 0 || got > m.P.HabitLift {
		t.Fatalf("跨三天的手選應該生效且不超過上限：%v", got)
	}
}

// 反例（#218 留言）：1 次手選＋14 次被動與 15 次手選，一般計數相同，Confirmed 要分得開；
// 完全沒手選的被動使用不會自己變成習慣（沒改的錯字不自我強化）。
func TestHabitSeparatesPicksFromPassiveUse(t *testing.T) {
	passive := habitModel()
	manual := habitModel()
	never := habitModel()
	habitPick(passive, "的", "ㄉㄜ˙", "程式")
	habitPick(manual, "的", "ㄉㄜ˙", "程式")
	for day := 1; day < 15; day++ {
		for _, m := range []*Model{passive, manual, never} {
			nextDay(m)
		}
		habitUse(passive, "程式")
		habitPick(manual, "的", "ㄉㄜ˙", "程式")
		for range 4 {
			habitUse(never, "程式")
		}
	}
	a, b := passive.Store.Confirmed[pair("程式", habitReading)], manual.Store.Confirmed[pair("程式", habitReading)]
	if !(a.Long < b.Long) {
		t.Fatalf("手選與被動分不開：%v vs %v", a.Long, b.Long)
	}
	if got := habitLiftOf(never, "程式"); got != 0 || len(never.Store.Confirmed) != 0 {
		t.Fatalf("沒有手選的被動使用不該有習慣：%v %v", got, never.Store.Confirmed)
	}
	if got := habitLiftOf(manual, "程式"); got != manual.P.HabitLift {
		t.Fatalf("反覆手選應該拿到全部分數：%v", got)
	}
}

// 一次手選之後跨日打很多次，就是回報者的狀況（程式 Long 3.6、城市 0.7、更正 1 筆）：這是使用者的習慣。
func TestHabitOnePickPlusUseAcrossDays(t *testing.T) {
	m := habitModel()
	habitPick(m, "的", "ㄉㄜ˙", "程式")
	for range 4 {
		nextDay(m)
		for range 4 {
			habitUse(m, "程式")
		}
	}
	if got := habitLiftOf(m, "程式"); got <= 0 {
		t.Fatalf("一次手選加跨日使用應該生效：%v", got)
	}
}

func habitEstablish(m *Model, word string) {
	for range 3 {
		habitPick(m, "的", "ㄉㄜ˙", word)
		habitPick(m, "用", "ㄩㄥˋ", word)
		nextDay(m)
	}
}

// 兩個詞都常打時不加分，交回語境判斷；偶爾打的另一詞不被壓死。
func TestHabitNeedsToDominateTheReading(t *testing.T) {
	m := habitModel()
	habitEstablish(m, "程式")
	full := habitLiftOf(m, "程式")
	if full <= 0 {
		t.Fatal("測試前提：習慣應該生效")
	}
	for range 6 {
		nextDay(m)
		for range 4 {
			habitUse(m, "城市")
		}
	}
	if got := habitLiftOf(m, "程式"); got >= full {
		t.Fatalf("另一個詞常用之後分數應該下降：%v → %v", full, got)
	}
}

// 一次反向選取不抹掉習慣，選的次數追上之後讓位；對手的紀錄不會變成負的。
func TestHabitContestedByRivalPicks(t *testing.T) {
	m := habitModel()
	habitEstablish(m, "程式")
	before := m.Store.Confirmed[pair("程式", habitReading)].at(m.Store.Time).Long
	habitPick(m, "這個", "ㄓㄜˋ ㄍㄜ˙", "城市")
	after := m.Store.Confirmed[pair("程式", habitReading)].at(m.Store.Time).Long
	if after >= before || after <= 0 {
		t.Fatalf("反向選取應該扣一次：%v → %v", before, after)
	}
	if habitLiftOf(m, "程式") <= 0 {
		t.Fatal("一次反向選取不該抹掉習慣")
	}
	for i := range 8 { // 不同語境：同一個前文選兩次會晉升成個人片語，之後由片語計數
		nextDay(m)
		c := habitContexts[i%len(habitContexts)]
		habitPick(m, c[0], c[1], "城市")
	}
	if got := habitLiftOf(m, "程式"); got != 0 {
		t.Fatalf("反向選取追上之後應該讓位：%v", got)
	}
	if got := m.Store.Confirmed[pair("程式", habitReading)].Long; got < 0 {
		t.Fatalf("紀錄不該是負的：%v", got)
	}
	if got := habitLiftOf(m, "城市"); got <= 0 {
		t.Fatalf("新的習慣應該生效：%v confirmed=%+v used=%+v %+v", got, m.Store.Confirmed, m.Store.EvidenceByWord["城市"], m.Store.EvidenceByWord["程式"])
	}
}

// 長期不打就退掉；照常打著的詞不必重新手選。
func TestHabitDecaysUnlessKeptInUse(t *testing.T) {
	idle, used := habitModel(), habitModel()
	habitEstablish(idle, "程式")
	habitEstablish(used, "程式")
	for range 12 { // 六個月
		idle.AdvanceTime(idle.Store.Time + 15*daySeconds)
		used.AdvanceTime(used.Store.Time + 15*daySeconds)
		for range 4 {
			habitUse(used, "程式")
		}
	}
	if got := habitLiftOf(idle, "程式"); got != 0 {
		t.Fatalf("半年沒打應該退掉：%v", got)
	}
	if got := habitLiftOf(used, "程式"); got <= 0 {
		t.Fatalf("照常打著的詞不該退掉：%v", got)
	}
	e := used.Store.Confirmed[pair("程式", habitReading)]
	if e.Long > 3 {
		t.Fatalf("被動使用不該增加手選證據：%v", e.Long)
	}
}

// 單字的同音字隨語境而定（在／再）；沒有同音詞的詞不必加分，加了只會去搶斷詞。
func TestHabitOnlyForContestedMultiSyllableWords(t *testing.T) {
	m := habitModel()
	for range 5 {
		m.Learn(Commit{Syls: zhuyin.ParseReading("ㄗㄞˋ"), Path: []Seg{{Word: "再", End: 1, Manual: true}}})
		m.Learn(Commit{Syls: zhuyin.ParseReading("ㄉㄨˊ ㄘˊ"), Path: []Seg{{Word: "獨詞", End: 2, Manual: true}}})
		nextDay(m)
	}
	if len(m.Store.Confirmed) != 1 {
		t.Fatalf("單字不該有紀錄：%v", m.Store.Confirmed)
	}
	if got := m.ReadingLift("再", zhuyin.ParseReading("ㄗㄞˋ")); got != 0 {
		t.Fatalf("單字：%v", got)
	}
	if got := m.ReadingLift("獨詞", zhuyin.ParseReading("ㄉㄨˊ ㄘˊ")); got != 0 {
		t.Fatalf("沒有同音詞：%v", got)
	}
}

// 本來就是系統首選的詞不需要習慣幫忙：它輸的語境是系統有根據的（不適用）。
func TestHabitSkipsTheSystemLeader(t *testing.T) {
	m := habitModel()
	sys := lm.NewMem()
	sys.SetUni("程式", 1e-3)
	sys.SetUni("城市", 1e-5)
	m.LM = sys
	habitEstablish(m, "程式")
	if got := habitLiftOf(m, "程式"); got != 0 {
		t.Fatalf("系統首選不該加分：%v", got)
	}
	// 反過來，使用者習慣的是系統的次要詞，照常加分。
	n := habitModel()
	n.LM = sys
	habitEstablish(n, "城市")
	if got := habitLiftOf(n, "城市"); got <= 0 {
		t.Fatalf("系統的次要詞應該加分：%v", got)
	}
}

// 補全不是手選；模糊音與沒打聲調到達的詞不學（Fuzzy 由 Learn 濾掉）。
func TestHabitIgnoresCompletionAndFuzzy(t *testing.T) {
	m := habitModel()
	syls := zhuyin.ParseReading(habitReading)
	m.Learn(Commit{Syls: syls, Path: []Seg{{Word: "程式", End: 2, Manual: true, Completion: true}}})
	m.Learn(Commit{Syls: syls, Path: []Seg{{Word: "程式", End: 2, Manual: true, Fuzzy: true}}})
	m.Learn(Commit{Syls: syls, Path: []Seg{{Word: "程式", End: 2, Manual: true, Partial: true}}})
	if len(m.Store.Confirmed) != 0 {
		t.Fatalf("不該留下手選紀錄：%v", m.Store.Confirmed)
	}
}

// 推翻自動修正的區間是例外不是偏好，沿用不學習規則。
func TestHabitIgnoresRejectedCorrectionRange(t *testing.T) {
	m := habitModel()
	m.Learn(Commit{Syls: zhuyin.ParseReading(habitReading), Path: []Seg{{Word: "程式", End: 2, Manual: true}},
		Rejected: []RejectedSeg{{Word: "城市", End: 2, Correction: true}}})
	if len(m.Store.Confirmed) != 0 {
		t.Fatalf("不該留下手選紀錄：%v", m.Store.Confirmed)
	}
}

// 移除、不再學、復原都要帶到手選紀錄，習慣分數跟著變；另一個詞的紀錄不動。
func TestHabitFollowsUnlearnForgetRestore(t *testing.T) {
	for name, drop := range map[string]func(*Model, string){"unlearn": (*Model).Unlearn, "forget": (*Model).Forget} {
		t.Run(name, func(t *testing.T) {
			m := habitModel()
			habitEstablish(m, "程式")
			habitPick(m, "這個", "ㄓㄜˋ ㄍㄜ˙", "城市")
			lift := habitLiftOf(m, "程式")
			confirmed := m.Store.Clone().Confirmed
			if lift <= 0 {
				t.Fatal("測試前提：習慣應該生效")
			}
			undo := m.Undoable(func() { drop(m, "程式") })
			if _, ok := m.Store.Confirmed[pair("程式", habitReading)]; ok {
				t.Fatal("手選紀錄沒有清掉")
			}
			if _, ok := m.Store.Confirmed[pair("城市", habitReading)]; !ok {
				t.Fatal("另一個詞的紀錄不該清掉")
			}
			if got := habitLiftOf(m, "程式"); got != 0 {
				t.Fatalf("清掉之後還有習慣分：%v", got)
			}
			if !m.Restore(undo) {
				t.Fatal("復原失敗")
			}
			if !reflect.DeepEqual(m.Store.Confirmed, confirmed) {
				t.Fatalf("復原不完整：%v want %v", m.Store.Confirmed, confirmed)
			}
			if got := habitLiftOf(m, "程式"); got != lift {
				t.Fatalf("復原後分數 %v want %v", got, lift)
			}
		})
	}
}

func TestHabitPrunedAfterDecayAndCloned(t *testing.T) {
	m := habitModel()
	habitEstablish(m, "程式")
	c := m.Store.Clone()
	c.Confirmed["x\ty"] = UsageEvidence{Long: 1}
	if _, ok := m.Store.Confirmed["x\ty"]; ok {
		t.Fatal("Clone 共用了 Confirmed")
	}
	m.AdvanceTime(m.Store.Time + 400*daySeconds)
	st := m.Prune()
	if st.Confirmed != 1 || len(m.Store.Confirmed) != 0 {
		t.Fatalf("衰減殆盡的紀錄應該清掉：%+v %v", st, m.Store.Confirmed)
	}
}

// 格式 5 的舊檔照讀：手選紀錄由留下的更正推回，同一天的多筆更正只算每日上限，
// 不用一般計數補造歷史；存回去是新格式，再讀回來結果相同。
func TestStoreFormat5SeedsConfirmedFromCorrections(t *testing.T) {
	const day1, day2 = 100 * daySeconds, 101 * daySeconds
	old := fmt.Sprintf(`{"Format":5,
		"Corrections":{
			"a":{"Reading":"%[1]s","Left":"ㄉㄜ˙","Word":"程式","Updated":%[2]d,"Serial":1},
			"b":{"Reading":"%[1]s","Left":"ㄩㄥˋ","Word":"程式","Updated":%[2]d,"Serial":2},
			"c":{"Reading":"%[1]s","Left":"ㄒㄧㄝˇ","Word":"程式","Updated":%[3]d,"Serial":3},
			"d":{"Reading":"ㄗㄞˋ","Word":"再","Updated":%[3]d,"Serial":4}},
		"Uni":{"程式":99,"城市":3},"Uses":{"程式":400},"Total":102,"Readings":{"城市\t%[1]s":true},
		"Time":%[3]d,"TimeSet":true}`, habitReading, day1, day2)
	s, err := LoadStore(strings.NewReader(old))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Confirmed) != 1 {
		t.Fatalf("只有多字詞的更正能推回：%v", s.Confirmed)
	}
	e := s.Confirmed[pair("程式", habitReading)]
	if e.Long <= confirmUnit || e.Long > 1+confirmUnit {
		t.Fatalf("同一天的更正要受每日上限：%v", e.Long)
	}
	if s.Uni["程式"] != 99 || s.Format != 5 {
		t.Fatalf("其他欄位不該變：%+v", s)
	}
	var buf bytes.Buffer
	if err := s.Save(&buf); err != nil {
		t.Fatal(err)
	}
	back, err := LoadStore(bytes.NewReader(buf.Bytes()))
	if err != nil || back.Format != StoreFormat {
		t.Fatalf("讀回失敗 %v", err)
	}
	if !reflect.DeepEqual(back.Confirmed, s.Confirmed) {
		t.Fatalf("新格式讀回不該再推一次：%v want %v", back.Confirmed, s.Confirmed)
	}
}

// 新格式檔案有空的 Confirmed 時不重新由更正推回（紀錄可能被扣到沒有、或使用者清過）；欄位是 null 時照常可寫。
func TestStoreFormat6KeepsConfirmedAsSaved(t *testing.T) {
	src := fmt.Sprintf(`{"Format":6,"Confirmed":null,"Corrections":{"a":{"Reading":"%s","Word":"程式","Updated":100}}}`, habitReading)
	s, err := LoadStore(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if s.Confirmed == nil || len(s.Confirmed) != 0 {
		t.Fatalf("Confirmed = %v", s.Confirmed)
	}
}

// 空白個人資料沒有任何分數，也不會為它重建索引。
func TestHabitEmptyStore(t *testing.T) {
	m := habitModel()
	if got := habitLiftOf(m, "程式"); got != 0 || m.habitIndex.store != nil {
		t.Fatalf("空白個人資料：%v %+v", got, m.habitIndex)
	}
}

// 重載另一份個人資料後，索引不沿用舊的。
func TestHabitIndexFollowsStoreSwap(t *testing.T) {
	m := habitModel()
	habitEstablish(m, "程式")
	if habitLiftOf(m, "程式") <= 0 {
		t.Fatal("測試前提")
	}
	other := NewStore()
	other.Confirmed[pair("城市", habitReading)] = UsageEvidence{Long: 2, Updated: m.Store.Time}
	other.EvidenceByWord["城市"] = UsageEvidence{Long: 2, Updated: m.Store.Time}
	other.Time, other.TimeSet = m.Store.Time, true
	m.Store = other
	if got := habitLiftOf(m, "程式"); got != 0 {
		t.Fatalf("換個人資料後還留著舊分數：%v", got)
	}
	if got := habitLiftOf(m, "城市"); got <= 0 {
		t.Fatalf("新個人資料的習慣沒有生效：%v", got)
	}
}

// 重建索引的成本：一份已知很大的手選紀錄（比實際個人檔大兩個數量級）。只在提交與每小時衰減後發生。
func BenchmarkBuildHabitIndex(b *testing.B) {
	m := habitModel()
	for i := range 20000 {
		w := fmt.Sprintf("詞%d", i)
		m.Store.Confirmed[pair(w, habitReading)] = UsageEvidence{Long: 3, Updated: m.Store.Time}
		m.Store.EvidenceByWord[w] = UsageEvidence{Long: 3, Updated: m.Store.Time}
	}
	for b.Loop() {
		m.buildHabitIndex()
	}
}

// 逐弧查分數：沒有紀錄的詞不組讀音鍵、不配置記憶體。
func BenchmarkReadingLiftMiss(b *testing.B) {
	m := habitModel()
	habitEstablish(m, "程式")
	reading := zhuyin.ParseReading(habitReading)
	b.ReportAllocs()
	for b.Loop() {
		if m.ReadingLift("城市", reading) != 0 {
			b.Fatal("不該有分數")
		}
	}
}
