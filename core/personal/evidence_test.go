package personal

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func evidenceModel() *Model {
	m := NewModel(lexicon.NewMem(), lm.NewMem())
	m.AdvanceTime(100 * daySeconds)
	return m
}

func learnWord(m *Model, manual, completion bool) {
	m.Learn(Commit{
		Syls: zhuyin.ParseReading("ㄇㄚˊ ㄆㄛˊ ㄉㄡˋ ㄈㄨˇ"),
		Path: []Seg{{Word: "麻婆豆腐", End: 4, Manual: manual, Completion: completion}},
	})
}

func TestEvidenceDistinguishesIntent(t *testing.T) {
	var scores []float64
	for _, event := range []struct{ manual, completion bool }{{false, false}, {true, true}, {true, false}} {
		m := evidenceModel()
		learnWord(m, event.manual, event.completion)
		score := m.Evidence(lm.BOS, "麻婆豆腐")
		if scoped := m.CorrectionUsage(zhuyin.ParseReading("ㄇㄚˊ ㄆㄛˊ")); scoped != nil {
			score += scoped(0, "麻婆豆腐")
		}
		scores = append(scores, score)
	}
	if !(scores[0] > 0 && scores[0] < scores[1] && scores[1] < scores[2]) {
		t.Fatalf("證據應為手選 > 接受補全 > 被動提交 > 0：%v", scores)
	}
}

// 同日爆量可立即影響當下，跨日持續使用才形成較強的長期偏好。
func TestBurstAndHabitReplay(t *testing.T) {
	burst, habit := evidenceModel(), evidenceModel()
	for i := 0; i < 12; i++ {
		learnWord(burst, true, false)
	}
	immediate := burst.Evidence(lm.BOS, "麻婆豆腐")
	for day := int64(0); day < 12; day++ {
		habit.AdvanceTime((100 + day) * daySeconds)
		// 先觀察預測，再學習下一筆，不能拿未來資料回填過去。
		if day == 0 && habit.Evidence(lm.BOS, "麻婆豆腐") != 0 {
			t.Fatal("冷啟動洩漏未來觀察")
		}
		learnWord(habit, true, false)
	}
	burst.AdvanceTime(130 * daySeconds)
	habit.AdvanceTime(130 * daySeconds)
	if b, h := burst.Evidence(lm.BOS, "麻婆豆腐"), habit.Evidence(lm.BOS, "麻婆豆腐"); !(b < immediate && h > b*3) {
		t.Fatalf("爆量應淡出、跨日習慣應保留較多證據：立即=%g 爆量=%g 習慣=%g", immediate, b, h)
	}
	if got := habit.Evidence("完全不同的上下文", "麻婆豆腐"); got > 2 {
		t.Fatalf("全域回退不應讓局部習慣到處搶建議：%g", got)
	}
}

func TestEvidenceBoundedAndContextual(t *testing.T) {
	m := evidenceModel()
	for day := int64(100); day < 200; day++ {
		m.AdvanceTime(day * daySeconds)
		for i := 0; i < 100; i++ {
			m.observeEvidence("想吃", "麻婆豆腐", 0.5)
		}
	}
	local, elsewhere := m.Evidence("想吃", "麻婆豆腐"), m.Evidence("討論", "麻婆豆腐")
	if local > 24 || local <= elsewhere || elsewhere > 2 {
		t.Fatalf("大量接受補全仍需有界且限制在上下文：本地=%g 外地=%g", local, elsewhere)
	}

}

func TestTimeDecaySurvivesRestartAndClockRollback(t *testing.T) {
	m := evidenceModel()
	learnWord(m, true, false)
	before := m.Store.Uni["麻婆豆腐"]
	var buf bytes.Buffer
	if err := m.Store.Save(&buf); err != nil {
		t.Fatal(err)
	}
	back := evidenceModel()
	var err error
	back.Store, err = LoadStore(&buf)
	if err != nil {
		t.Fatal(err)
	}
	back.AdvanceTime(130 * daySeconds)
	if math.Abs(back.Store.Uni["麻婆豆腐"]-before/2) > 1e-12 {
		t.Fatal("排序權重未在重啟後依 30 日半衰期衰減")
	}
	evidence := back.Evidence(lm.BOS, "麻婆豆腐")
	back.AdvanceTime(120 * daySeconds)
	if back.Evidence(lm.BOS, "麻婆豆腐") != evidence {
		t.Fatal("時鐘倒退不應恢復已衰減的證據")
	}
	back.AdvanceTime(160 * daySeconds)
	if back.Evidence(lm.BOS, "麻婆豆腐") >= evidence {
		t.Fatal("閒置時也應衰減，不必等再次使用該詞")
	}
}

func TestLegacyStoreDoesNotInventStrongEvidence(t *testing.T) {
	s, err := LoadStore(strings.NewReader(`{"Uses":{"麻婆豆腐":1000},"Phrases":{"親子丼":"ㄑㄧㄣ ㄗˇ ㄐㄧㄥˇ"},"PhraseOrder":["親子丼"],"Uni":{"親子丼":2},"Total":2}`))
	if err != nil {
		t.Fatal(err)
	}
	m := evidenceModel()
	m.Store = s
	m.AdvanceTime(100 * daySeconds)
	if m.Evidence(lm.BOS, "麻婆豆腐") != 0 {
		t.Fatal("不能替無時間／來源的舊 Uses 捏造強證據")
	}
	if !m.Source().Has("親子丼") || m.Store.Uni["親子丼"] != 2 {
		t.Fatal("舊個人詞條與排序必須保留")
	}
	learnWord(m, false, false)
	if m.Evidence(lm.BOS, "麻婆豆腐") <= 0 {
		t.Fatal("舊存檔應能繼續累積新證據")
	}
}

func TestForgetRemovesEvidenceAndPassiveCannotRestore(t *testing.T) {
	m := evidenceModel()
	learnWord(m, true, false)
	m.Forget("麻婆豆腐")
	learnWord(m, false, false)
	if m.Evidence(lm.BOS, "麻婆豆腐") != 0 {
		t.Fatal("刪除的偏好被被動提交學回")
	}
	learnWord(m, true, false)
	if m.Evidence(lm.BOS, "麻婆豆腐") <= 0 {
		t.Fatal("再次明確手選應可重新學習")
	}
}

func TestCorrectionPreservesAggregateEvidenceAndScopesRejection(t *testing.T) {
	m := evidenceModel()
	m.observeEvidence(lm.BOS, "景", 1)
	m.observeEvidence("風", "景", 1)
	m.Learn(Commit{
		Syls:     zhuyin.ParseReading("ㄐㄧㄥˇ"),
		Path:     []Seg{{Word: "丼", End: 1, Manual: true}},
		Rejected: []RejectedSeg{{Word: "景", End: 1}},
	})
	if m.Evidence(lm.BOS, "景") <= 0 || m.Evidence("風", "景") <= 0 {
		t.Fatal("更正不得清除共用補全證據")
	}
	p := m.CorrectionPenalty(zhuyin.ParseReading("ㄐㄧㄥˇ"), nil)
	if p == nil || p(0, 1, "景") <= 0 || p(0, 1, "丼") != 0 {
		t.Fatal("相符語境須抑制被否決詞")
	}
	if p := m.CorrectionPenalty(zhuyin.ParseReading("ㄈㄥ ㄐㄧㄥˇ"), nil); p != nil {
		t.Fatal("否決不應外溢至風景")
	}
	m.Forget("風")
	if m.Evidence("風", "景") != 0 || m.Evidence("其他", "景") != 0 {
		t.Fatal("刪除相關上下文後不應有全域回退殘留")
	}
}

func TestDecayRetainsPersonalPhraseAndReading(t *testing.T) {
	m := evidenceModel()
	c := Commit{Syls: zhuyin.ParseReading("ㄑㄧㄣ ㄗˇ ㄐㄧㄥˇ"), Path: []Seg{{Word: "親子", End: 2}, {Word: "丼", Start: 2, End: 3, Manual: true}}}
	m.Learn(c)
	m.Learn(c)
	evidence := m.Evidence(lm.BOS, "親子丼")
	if evidence <= 0 {
		t.Fatal("晉升時必須保留片語實際觀察的證據")
	}
	m.AdvanceTime(1000 * daySeconds)
	if !m.Source().Has("親子丼") || !m.Store.Readings[pair("丼", "ㄐㄧㄥˇ")] {
		t.Fatal("時間衰減不得刪除個人詞條與讀音")
	}
	if m.Evidence(lm.BOS, "親子丼") >= evidence {
		t.Fatal("保留詞條不等於永遠維持高推薦權重")
	}
}
