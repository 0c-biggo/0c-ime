package personal

import (
	"bytes"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/zhuyin"
)

// pruneFixture 模擬兩個月的使用：常用句子、明確手選（更正、讀音、單字、片語晉升）、
// 只用一次的冷門詞對、刪除學習、自動修正被推翻，以及代名詞、英文與表情紀錄。
func pruneFixture(t *testing.T) (*Model, lexicon.Source, lm.System) {
	t.Helper()
	lex := lexicon.NewMem()
	sys := lm.NewMem()
	add := func(w, reading string, uni float64) {
		lex.Add(lexicon.Entry{Surface: w, Reading: zhuyin.ParseReading(reading), PRW: 1})
		sys.SetUni(w, uni)
	}
	add("我", "ㄨㄛˇ", 0.02)
	add("想", "ㄒㄧㄤˇ", 0.005)
	add("吃", "ㄔ", 0.003)
	add("親子", "ㄑㄧㄣ ㄗˇ", 1e-5)
	add("景", "ㄐㄧㄥˇ", 1e-4)
	add("警", "ㄐㄧㄥˇ", 5e-5)
	add("丼", "ㄐㄧㄥˇ", 1e-7)
	sys.SetBi("我", "想", 0.05)
	sys.SetBi("想", "吃", 0.02)
	m := NewModel(lex, sys)
	day := int64(20000)
	m.AdvanceTime(day * daySeconds)
	qzj := zhuyin.ParseReading("ㄑㄧㄣ ㄗˇ ㄐㄧㄥˇ")
	wxc := zhuyin.ParseReading("ㄨㄛˇ ㄒㄧㄤˇ ㄔ")
	for d := int64(0); d < 60; d++ {
		m.AdvanceTime((day + d) * daySeconds)
		for i := 0; i < 5; i++ {
			m.Learn(Commit{Syls: wxc, Path: []Seg{{Word: "我", End: 1}, {Word: "想", Start: 1, End: 2}, {Word: "吃", Start: 2, End: 3}}})
		}
		if d%10 == 0 {
			m.Learn(Commit{Syls: qzj, Path: []Seg{{Word: "親子", End: 2}, {Word: "丼", Start: 2, End: 3, Manual: true}}})
		}
		// 每天幾個只出現一次的冷門詞對（系統詞庫沒有，先驗極小）。
		for i := 0; i < 20; i++ {
			a, b := fmt.Sprintf("甲%d_%d", d, i), fmt.Sprintf("乙%d_%d", d, i)
			syls := zhuyin.ParseReading("ㄐㄧㄚˇ ㄧˇ")
			m.Learn(Commit{Syls: syls, Path: []Seg{{Word: a, End: 1}, {Word: b, Start: 1, End: 2}}})
		}
	}
	m.Learn(Commit{Syls: zhuyin.ParseReading("ㄐㄧㄥˇ"), Path: []Seg{{Word: "警", End: 1, Manual: true}}})
	m.Forget("景")
	s := m.Store
	s.Rejects["警\tㄐㄧㄥˇ"] = 3
	s.Pronouns["他"] = PronounHabit{All: PronounCount{Variant: 3}, Cues: map[string]PronounCount{"男朋友": {Variant: 2}}, Serial: 4}
	s.English["zuin"] = EnglishEvidence{Text: "Zuin", Confirmed: 3, Days: 2, Updated: s.Time, Day: s.Time / daySeconds}
	m.RememberEmoji("😀")
	m.RememberPaletteTab("kaomoji")
	return m, lex, sys
}

func explicitChoices(s *Store) map[string]any {
	return map[string]any{
		"Corrections": s.Corrections, "Readings": s.Readings, "Picked": s.Picked, "Phrases": s.Phrases,
		"PhraseOrder": s.PhraseOrder, "Tombstones": s.Tombstones, "Rejects": s.Rejects, "Pronouns": s.Pronouns,
		"English": s.English, "EmojiRecent": s.EmojiRecent, "EmojiStats": s.EmojiStats, "PaletteTab": s.PaletteTab, "CorrectionSerial": s.CorrectionSerial,
	}
}

// 清理只刪衰減到不影響排序的計數與證據：明確的選擇一筆不少，每個詞的機率幾乎不變（#129）。
func TestPruneKeepsExplicitChoicesAndScores(t *testing.T) {
	m, lex, sys := pruneFixture(t)
	var pruned PruneStats
	for _, years := range []int64{0, 1, 3} {
		m.AdvanceTime(m.Store.Time + years*365*daySeconds)
		before := NewModel(lex, sys)
		before.Store = m.Store.Clone()
		st := m.Prune()
		pruned.Bi += st.Bi
		pruned.Uni += st.Uni
		pruned.EvidenceByContext += st.EvidenceByContext
		if !reflect.DeepEqual(explicitChoices(before.Store), explicitChoices(m.Store)) {
			t.Fatalf("%d 年：明確的選擇被清理改動", years)
		}
		if _, ok := m.Store.Uni["親子丼"]; !ok {
			t.Fatalf("%d 年：個人片語的計數被清掉", years)
		}
		// 所有出現過的詞（含被清掉的）在各前文的機率改變都很小。
		for k := range before.Store.Bi {
			left, w, _ := strings.Cut(k, "\t")
			a, b := before.Prob(left, w), m.Prob(left, w)
			if d := math.Abs(math.Log(b / a)); d > 0.01 {
				t.Fatalf("%d 年：P(%s|%s) %g → %g，對數差 %.4f", years, w, left, a, b, d)
			}
		}
		for k := range before.Store.EvidenceByContext {
			left, w, _ := strings.Cut(k, "\t")
			if d := math.Abs(before.Evidence(left, w) - m.Evidence(left, w)); d > 1.25*DefaultPrunePolicy().Evidence {
				t.Fatalf("%d 年：補全證據 %s|%s 改變 %g", years, w, left, d)
			}
		}
		checkCounts(t, m.Store)
	}
	if pruned.Bi == 0 || pruned.EvidenceByContext == 0 || pruned.Uni == 0 {
		t.Fatalf("應清掉衰減的冷門詞對與證據：%+v", pruned)
	}
}

// Ctx[L] 恆等於 Σ Bi[L,·]，Total 恆等於 Σ Uni；清理不能讓分母與分子脫節。
func checkCounts(t *testing.T, s *Store) {
	t.Helper()
	sum := map[string]float64{}
	for k, v := range s.Bi {
		left, _, _ := strings.Cut(k, "\t")
		sum[left] += v
	}
	for left, v := range s.Ctx {
		if math.Abs(v-sum[left]) > 1e-9*max(1, v) {
			t.Fatalf("Ctx[%s]=%g，Σ Bi=%g", left, v, sum[left])
		}
	}
	for left := range sum {
		if _, ok := s.Ctx[left]; !ok {
			t.Fatalf("Bi 有前文 %s 但 Ctx 沒有", left)
		}
	}
	total := 0.0
	for _, v := range s.Uni {
		total += v
	}
	if math.Abs(total-s.Total) > 1e-9*max(1, total) {
		t.Fatalf("Total=%g，Σ Uni=%g", s.Total, total)
	}
}

// 上限：超過時淘汰最弱的項目，受保護的個人片語不淘汰。
func TestPruneCapsKeepStrongest(t *testing.T) {
	m, _, _ := pruneFixture(t)
	p := DefaultPrunePolicy()
	p.Share, p.Evidence = 0, 0 // 只看上限
	p.MaxPairs, p.MaxEvidence, p.MaxWord = 100, 80, 60
	m.PruneWith(p)
	s := m.Store
	if len(s.Bi) > p.MaxPairs+2 || len(s.EvidenceByContext) > p.MaxEvidence+2 || len(s.Uni) > p.MaxWord+10 || len(s.EvidenceByWord) > p.MaxWord+10 {
		t.Fatalf("超過上限：Bi=%d EBC=%d Uni=%d EBW=%d", len(s.Bi), len(s.EvidenceByContext), len(s.Uni), len(s.EvidenceByWord))
	}
	for _, k := range []string{"我\t想", "想\t吃"} {
		if _, ok := s.Bi[k]; !ok {
			t.Fatalf("天天用的 %q 被上限淘汰", k)
		}
		if _, ok := s.EvidenceByContext[k]; !ok {
			t.Fatalf("天天用的 %q 補全證據被上限淘汰", k)
		}
	}
	for _, w := range []string{"親子丼", "丼", "警"} {
		if _, ok := s.Uni[w]; !ok {
			t.Fatalf("明確選過的 %q 計數被上限淘汰", w)
		}
	}
	checkCounts(t, s)
}

// 每個 UTC 日至多清一次；同一份資料清兩次結果相同。
func TestPruneIfDueOncePerDay(t *testing.T) {
	m, _, _ := pruneFixture(t)
	m.AdvanceTime(m.Store.Time + 400*daySeconds)
	if _, ran := m.PruneIfDue(); !ran {
		t.Fatal("第一次應清理")
	}
	if _, ran := m.PruneIfDue(); ran {
		t.Fatal("同一天不應再清")
	}
	if st := m.Prune(); st.Total() != 0 {
		t.Fatalf("清過的資料再清應無變動：%+v", st)
	}
	m.AdvanceTime(m.Store.Time + daySeconds)
	if _, ran := m.PruneIfDue(); !ran {
		t.Fatal("隔天應再清")
	}
}

// 清理不改變存檔格式：清理後的檔案仍是 StoreFormat 5，同格式的舊版照常讀寫，
// 只是看到的項目較少；存檔來回一致。
func TestPrunedStoreKeepsFormat(t *testing.T) {
	if StoreFormat != 5 {
		t.Fatalf("清理不應改變格式（目前 %d）；若格式真的改了，要補舊版相容測試", StoreFormat)
	}
	m, _, _ := pruneFixture(t)
	m.AdvanceTime(m.Store.Time + 400*daySeconds)
	m.Prune()
	var buf bytes.Buffer
	if err := m.Store.Save(&buf); err != nil {
		t.Fatal(err)
	}
	back, err := LoadStore(bytes.NewReader(buf.Bytes()))
	if err != nil || back.Format != StoreFormat {
		t.Fatalf("讀回失敗：%v", err)
	}
	if !reflect.DeepEqual(m.Store, back) {
		t.Fatal("清理後存檔來回不一致")
	}
}
