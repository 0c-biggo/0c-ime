package personal

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func zaiCorrection(word, right string) Commit {
	return Commit{Syls: zhuyin.ParseReading("ㄗㄞˋ " + right), Path: []Seg{{Word: word, End: 1, Manual: true}, {Word: "海", Start: 1, End: 2}}, Rejected: []RejectedSeg{{Word: "再", End: 1}}}
}

func TestScopedCorrectionPreservesOtherReadingsAndHistoricalCounts(t *testing.T) {
	m := evidenceModel()
	m.observe(lm.BOS, "再", 5)
	m.observeEvidence(lm.BOS, "再", 1)
	m.Learn(zaiCorrection("在", "ㄏㄞˇ"))
	if m.Store.Bi[pair(lm.BOS, "再")] != 5 || m.Evidence(lm.BOS, "再") <= 0 {
		t.Fatal("不得刪除共用歷史")
	}
	if m.Store.Bi[pair(lm.BOS, "在")] != m.P.WPassive {
		t.Fatal("强更正不應灌進全域句首計數")
	}
	p := m.CorrectionPenalty(zhuyin.ParseReading("ㄗㄞˋ ㄏㄞˇ"), nil)
	if p == nil || p(0, 1, "再") <= 0 || p(0, 1, "在") != 0 {
		t.Fatal("相符右文應使用更正")
	}
	for _, reading := range []string{"ㄗㄞˋ ㄐㄧㄢˋ", "ㄗㄞˋ", "ㄗㄞˋ ㄏㄞˋ", "ㄨㄛˇ ㄗㄞˋ ㄏㄞˇ"} {
		if p := m.CorrectionPenalty(zhuyin.ParseReading(reading), nil); p != nil {
			t.Fatalf("更正外溢到 %s", reading)
		}
	}
	if p(0, 2, "再海") != p(0, 1, "再") {
		t.Fatal("改變分詞不應繞過成本")
	}
}

func TestCorrectionSurvivesSegmentationAndLatestChoiceWins(t *testing.T) {
	m := evidenceModel()
	syls := zhuyin.ParseReading("ㄗㄞˋ ㄐㄧㄢˋ")
	m.Learn(Commit{Syls: syls, Path: []Seg{{Word: "再見", End: 2, Manual: true}}})
	p := m.CorrectionPenalty(syls, nil)
	if p(0, 2, "在建") != p(0, 1, "在")+p(1, 2, "建") || p(0, 2, "再見") != 0 {
		t.Fatal("跨分詞成本必須相同")
	}
	overrides := []Seg{{Word: "在", End: 1, Manual: true}}
	p = m.CorrectionPenalty(syls, overrides)
	if p(0, 2, "在見") != 0 {
		t.Fatal("當次選取優先")
	}
	if m.CorrectionPenalty(syls, nil)(0, 1, "在") == 0 {
		t.Fatal("預覽不得提前改變持久記錄")
	}
	// 舊整詞偏好已衰減過半時，新的局部明確改選直接勝出，不讓重疊紀錄累加。
	m.AdvanceTime(m.Store.Time + 31*daySeconds)
	m.Learn(Commit{Syls: syls, Path: []Seg{{Word: "在", End: 1, Manual: true}, {Word: "見", Start: 1, End: 2}}})
	p = m.CorrectionPenalty(syls, nil)
	if p == nil || p(0, 2, "在見") != 0 || p(0, 1, "再") == 0 {
		t.Fatal("新的選取應覆蓋已衰減的重疊舊偏好")
	}
}

// 近期在重疊的詞長上選了不同的字（整詞「再見」後又單選「在」）：互相抵銷，交回模型，
// 不由最後一次全強度決定，也不讓被蓋住的舊紀錄在之後復活。
func TestContraryPickAcrossWindowsReturnsToModel(t *testing.T) {
	m := evidenceModel()
	syls := zhuyin.ParseReading("ㄗㄞˋ ㄐㄧㄢˋ")
	m.Learn(Commit{Syls: syls, Path: []Seg{{Word: "再見", End: 2, Manual: true}}})
	m.Learn(Commit{Syls: syls, Path: []Seg{{Word: "在", End: 1, Manual: true}, {Word: "見", Start: 1, End: 2}}})
	if p := m.CorrectionPenalty(syls, nil); p != nil {
		t.Fatal("近期矛盾的手選應交回模型")
	}

	// 先單選「再」、再單選「在」覆蓋（不同鍵，舊算法以最新為準）；之後誤點「再」時，
	// 不得刪掉單字紀錄後讓更早的整詞「再海」復活成全強度。
	m = evidenceModel()
	hai := zhuyin.ParseReading("ㄗㄞˋ ㄏㄞˇ")
	m.Learn(Commit{Syls: hai, Path: []Seg{{Word: "再海", End: 2, Manual: true}}})
	m.AdvanceTime(m.Store.Time + 31*daySeconds)
	m.Learn(zaiCorrection("在", "ㄏㄞˇ"))
	m.AdvanceTime(m.Store.Time + 2*3600)
	m.Learn(zaiCorrection("再", "ㄏㄞˇ"))
	if p := m.CorrectionPenalty(hai, nil); p != nil && p(0, 1, "在") > 0 {
		t.Fatal("抵銷後不得讓更早的「再海」復活")
	}
}

// 長期反覆確認的偏好，一次反向選取只扣掉一次確認；反向選取次數追上才交回模型。
func TestRepeatedPreferenceSurvivesOneContraryPick(t *testing.T) {
	syls := zhuyin.ParseReading("ㄗㄞˋ ㄏㄞˇ")
	m := evidenceModel()
	for range 3 {
		m.Learn(zaiCorrection("在", "ㄏㄞˇ"))
		m.AdvanceTime(m.Store.Time + daySeconds)
	}
	m.Learn(zaiCorrection("再", "ㄏㄞˇ"))
	if p := m.CorrectionPenalty(syls, nil); p == nil || p(0, 1, "再") <= 0 {
		t.Fatal("一次反向選取不應抹掉反覆確認的偏好")
	}
	for range 3 {
		m.Learn(zaiCorrection("再", "ㄏㄞˇ"))
	}
	if p := m.CorrectionPenalty(syls, nil); p == nil || p(0, 1, "在") <= 0 {
		t.Fatal("反向選取持續累積後應改記新的選擇")
	}
}

func TestScopedUsageDoesNotPromoteOtherWordsOrContexts(t *testing.T) {
	m := evidenceModel()
	m.Learn(Commit{Syls: zhuyin.ParseReading("ㄇㄚˊ ㄆㄛˊ ㄉㄡˋ ㄈㄨˇ"), Path: []Seg{{Word: "麻婆豆腐", End: 4, Manual: true}}})
	f := m.CorrectionUsage(zhuyin.ParseReading("ㄇㄚˊ ㄆㄛˊ"))
	if f == nil || f(0, "麻婆豆腐") <= 0 || f(0, "麻婆豆腐店") != 0 {
		t.Fatal("補全只加權被選取的原詞")
	}
	f = m.CorrectionUsage(zhuyin.ParseReading("ㄕˋ ㄇㄚˊ ㄆㄛˊ"))
	if f(1, "麻婆豆腐") != 0 {
		t.Fatal("補全不得套用不同左文")
	}
	m = evidenceModel()
	m.Learn(zaiCorrection("在", "ㄏㄞˇ"))
	f = m.CorrectionUsage(zhuyin.ParseReading("ㄗㄞˋ"))
	if f(0, "在") != 0 {
		t.Fatal("沒有右文時不能套用右文限定證據")
	}
	f = m.CorrectionUsage(zhuyin.ParseReading("ㄗㄞˋ ㄏㄞˇ"))
	if f(0, "在") <= 0 {
		t.Fatal("相符右文應有證據")
	}
}

func TestCorrectionStoreDecayForgetAndLegacy(t *testing.T) {
	m := evidenceModel()
	syls := zhuyin.ParseReading("ㄗㄞˋ ㄏㄞˇ")
	m.Learn(zaiCorrection("在", "ㄏㄞˇ"))
	before := m.CorrectionPenalty(syls, nil)(0, 1, "再")
	var b bytes.Buffer
	if err := m.Store.Save(&b); err != nil {
		t.Fatal(err)
	}
	st, err := LoadStore(&b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st, m.Store) {
		t.Fatal("重啟遺失條件偏好")
	}
	m.Store = st
	m.AdvanceTime(m.Store.Time + 30*daySeconds)
	if got := m.CorrectionPenalty(syls, nil)(0, 1, "再"); got != before/2 {
		t.Fatal("更正須隨時間衰減", got)
	}
	m.Forget("在")
	if m.CorrectionPenalty(syls, nil) != nil {
		t.Fatal("刪除後仍有更正殘留")
	}
	st, err = LoadStore(bytes.NewBufferString(`{"Bi":{"<s>\t再":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	m.Store = st
	if len(st.Corrections) != 0 {
		t.Fatal("不可替舊計數捏造語境")
	}
	m.Learn(zaiCorrection("在", "ㄏㄞˇ"))
	if len(st.Corrections) != 1 {
		t.Fatal("舊檔不能繼續學習")
	}
}

func TestCorrectionCapacityDeterministicAndNoTabEvidence(t *testing.T) {
	m := evidenceModel()
	c := zaiCorrection("在", "ㄏㄞˇ")
	c.Path[0].Completion = true
	m.Learn(c)
	if len(m.Store.Corrections) != 0 {
		t.Fatal("Tab 不是明確更正")
	}
	// 模擬達到容量上限的歷史記錄，新的明確選取僅逐出最舊的一份。
	for i := range maxCorrections {
		m.Store.Corrections[fmt.Sprint(i)] = Correction{Serial: uint64(i + 1)}
	}
	m.Store.CorrectionSerial = maxCorrections
	c.Path[0].Completion = false
	m.Learn(c)
	if len(m.Store.Corrections) != maxCorrections {
		t.Fatal("容量失去上限")
	}
	if _, ok := m.Store.Corrections["0"]; ok {
		t.Fatal("應逐出最舊條件偏好")
	}
}

func TestForgetNeighbourRemovesDependentCorrection(t *testing.T) {
	m := evidenceModel()
	m.Learn(zaiCorrection("在", "ㄏㄞˇ"))
	m.Forget("海")
	if len(m.Store.Corrections) != 0 {
		t.Fatal("撤銷相鄰詞須移除依賴它的更正")
	}
}

func BenchmarkCorrectionLookupsAtCapacity(b *testing.B) {
	m := evidenceModel()
	ys := []string{"ㄅㄚ", "ㄆㄚ", "ㄇㄚ", "ㄈㄚ", "ㄉㄚ", "ㄊㄚ", "ㄋㄚ", "ㄌㄚ"}
	for i := range maxCorrections {
		c := Correction{Reading: fmt.Sprintf("%s %s %s %s", ys[i%8], ys[i/8%8], ys[i/64%8], ys[i/512%8]), Word: "甲乙丙丁", Serial: uint64(i + 1), Updated: m.Store.Time}
		m.Store.Corrections[correctionKey(c.Reading, c.Left, c.Right)] = c
	}
	syls := zhuyin.ParseReading("ㄗㄞˋ ㄐㄧㄢˋ ㄧˊ ㄇㄧㄢˋ ㄗㄞˋ ㄏㄞˇ ㄧˋ ㄈㄤ ㄗㄞˋ ㄕㄨㄛ ㄧˊ ㄘˋ ㄗㄞˋ ㄓㄨㄛ ㄗ˙ ㄕㄤˋ")
	m.CorrectionUsage(syls) // 一次重建後的逐鍵查詢
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.CorrectionPenalty(syls, nil)
		m.CorrectionUsage(syls)
	}
}

// 同一位置近期先後選了不同的字，交回模型判斷；再選一次才重新記住。舊紀錄已衰減過半則直接取代。
func TestConflictingPicksReturnToModel(t *testing.T) {
	syls := zhuyin.ParseReading("ㄗㄞˋ ㄏㄞˇ")
	m := evidenceModel()
	m.Learn(zaiCorrection("在", "ㄏㄞˇ"))
	m.AdvanceTime(m.Store.Time + 2*3600)
	m.Learn(zaiCorrection("再", "ㄏㄞˇ"))
	if p := m.CorrectionPenalty(syls, nil); p != nil {
		t.Fatal("前後矛盾的手選不應以最後一次全強度生效")
	}
	m.Learn(zaiCorrection("再", "ㄏㄞˇ"))
	if p := m.CorrectionPenalty(syls, nil); p == nil || p(0, 1, "在") <= 0 || p(0, 1, "再") != 0 {
		t.Fatal("再選一次應重新記住")
	}

	m = evidenceModel()
	m.Learn(zaiCorrection("在", "ㄏㄞˇ"))
	m.AdvanceTime(m.Store.Time + 31*daySeconds)
	m.Learn(zaiCorrection("再", "ㄏㄞˇ"))
	if p := m.CorrectionPenalty(syls, nil); p == nil || p(0, 1, "在") <= 0 {
		t.Fatal("舊紀錄已衰減過半時，新的選取直接取代")
	}
}

// #96：手選被較長的詞包住時，路徑上只有包住它的詞；手選照樣成為更正與個人讀音，
// 一般計數仍依路徑，跨音手選不學。
func TestSelectionEnclosedByLongerWordIsLearned(t *testing.T) {
	m := evidenceModel()
	syls := zhuyin.ParseReading("ㄨㄟˋ ㄕㄜˊ ㄇㄜ˙")
	m.Learn(Commit{Syls: syls, Path: []Seg{{Word: "為什麼", End: 3}}, Selected: []Seg{{Word: "什麼", Start: 1, End: 3, Manual: true}}})
	c, ok := m.Store.Corrections[correctionKey("ㄕㄜˊ ㄇㄜ˙", "ㄨㄟˋ", "")]
	if !ok || c.Word != "什麼" || c.LeftWord != "為什麼" {
		t.Fatalf("被包住的手選沒有成為更正：%+v", m.Store.Corrections)
	}
	if !m.ChoseReading("什麼", syls[1:]) || m.ChoseReading("為什麼", syls) {
		t.Fatalf("個人讀音應只記手選的區間：%v", m.Store.Readings)
	}
	if m.Store.Uni["為什麼"] != m.P.WPassive || m.Store.Uni["什麼"] != 0 {
		t.Fatalf("一般計數應依路徑：%v", m.Store.Uni)
	}
	m.Forget("為什麼")
	if len(m.Store.Corrections) != 0 {
		t.Fatal("撤銷包住它的詞應一併移除依附的更正")
	}

	fuzzy := evidenceModel()
	fuzzy.Learn(Commit{Syls: syls, Path: []Seg{{Word: "為什麼", End: 3}}, Selected: []Seg{{Word: "什麼", Start: 1, End: 3, Manual: true, Fuzzy: true}}})
	if len(fuzzy.Store.Corrections) != 0 || len(fuzzy.Store.Readings) != 0 {
		t.Fatal("跨音手選不應學習")
	}
}

// #150：讀音還帶「沒打聲調」記號（提交時沒能換回詞庫讀音）的手選與被推翻的自動修正，
// 就算呼叫端沒標 Fuzzy 也不把記號寫進個人資料；被推翻的範圍照樣不學，讀音完整的拒絕照常計次。
func TestUntonedMarkNeverReachesPersonalData(t *testing.T) {
	m := evidenceModel()
	syls := []zhuyin.Syllable{zhuyin.Untone("ㄨㄟ"), zhuyin.Untone("ㄕㄜ"), zhuyin.Untone("ㄇㄜ"), "ㄏㄠˇ"}
	m.Learn(Commit{Syls: syls,
		Path:     []Seg{{Word: "為什麼", End: 3, Fuzzy: true}, {Word: "好", Start: 3, End: 4}},
		Selected: []Seg{{Word: "什", Start: 1, End: 2, Manual: true}},
		Rejected: []RejectedSeg{{Word: "摸", Start: 2, End: 3, Correction: true}, {Word: "號", Start: 3, End: 4, Correction: true}}})
	for k := range m.Store.Readings {
		t.Errorf("個人讀音不能有記號：%q", k)
	}
	for k := range m.Store.Picked {
		t.Errorf("單字手選不能有記號：%q", k)
	}
	for k := range m.Store.Rejects {
		if strings.ContainsRune(k, zhuyin.UntonedMark) {
			t.Errorf("拒絕次數不能有記號：%q", k)
		}
	}
	if m.Store.Rejects["號\tㄏㄠˇ"] != 1 {
		t.Errorf("讀音完整的拒絕照常計次：%v", m.Store.Rejects)
	}
	if m.Store.Uni["好"] != 0 {
		t.Errorf("被推翻的範圍不學：%v", m.Store.Uni)
	}
}
