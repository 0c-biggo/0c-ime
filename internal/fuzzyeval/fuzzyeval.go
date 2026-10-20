// Package fuzzyeval 量測模糊音（issue #45／#48／#49）的候選可達、自動修正與乾淨輸入誤改。
//
// 乾淨輸入就是參考注音；錯音輸入是在參考注音上以固定種子換成近音（只換音節成分、保留聲調、結果合法）。
// 指標的分母都明講：乾淨誤改率只看「原版本答對、且該句有可換音音節」的句子；
// 錯音只看實際換了音的句子。本套件只算數字，不決定門檻；合成錯音的比例不是自然錯音比例，
// 報告要與真人資料分開，且不能拿合成的總準確率決定預設。
package fuzzyeval

import (
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/0c-biggo/0c-ime/core/engine"
	"github.com/0c-biggo/0c-ime/core/fuzzy"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"github.com/0c-biggo/0c-ime/internal/eval"
	"github.com/0c-biggo/0c-ime/internal/neural"
)

// Case 是一個評測句：參考文字、參考注音與斷詞（每字一音節，斷詞只用來量詞級候選可達）。
type Case struct {
	Ref     string
	Reading []zhuyin.Syllable
	Words   []string
	Tags    []string // 分層標籤（例如來源檔、含罕用詞）；SummarizeBy 依它分層
}

// Noise 在 reading 上製造近音錯誤：每個有近音替代的音節各以 rate 的機率換成其中一個替代。
// 回傳換過音的位置。同樣的 rng 狀態得到同樣的結果。
func Noise(gen fuzzy.Config, reading []zhuyin.Syllable, rate float64, rng *rand.Rand) (noisy []zhuyin.Syllable, swapped []int) {
	noisy = append([]zhuyin.Syllable(nil), reading...)
	for i, y := range reading {
		alts := gen.Alternatives(y)
		if len(alts) == 0 || rng.Float64() >= rate {
			continue
		}
		noisy[i] = alts[rng.Intn(len(alts))].Syl
		swapped = append(swapped, i)
	}
	return noisy, swapped
}

// Eligible 回報 reading 是否有任何音節在 cfg 下有近音替代（乾淨誤改率的分母條件）。
func Eligible(cfg fuzzy.Config, reading []zhuyin.Syllable) bool {
	for _, y := range reading {
		if len(cfg.Alternatives(y)) > 0 {
			return true
		}
	}
	return false
}

// Arm 是評測用的引擎角色；宿主可依角色決定要不要接神經評分器（僅供選字的可達量測不需要）。
type Arm string

const (
	ArmOff    Arm = "off"    // 全關：原版本
	ArmAuto   Arm = "auto"   // 設定的 auto 配對
	ArmSelect Arm = "select" // 同樣的配對改為僅供選字
)

// Params 是一次評測的設定。
type Params struct {
	Auto   fuzzy.Config // 啟用 auto 的配對（Auto 模式）
	Policy fuzzy.AutoPolicy
	Rate   float64 // 錯音比例：每個可換音音節被換音的機率
	Seed   int64
}

// Base 是與自動修正門檻無關的部分：原版本（全關）在乾淨與錯音輸入上的結果，
// 以及僅供選字模式下目標詞的候選名次。
type Base struct {
	Case        Case
	Eligible    bool
	Noisy       []zhuyin.Syllable
	Swapped     []int
	OffClean    string
	OffNoisy    string
	OffDegraded bool // 打這一句時神經評分器有按鍵沒評分（逾時、不可用），排除不計
	OffRewrites int
	OffLatency  []time.Duration
	WordRank    []WordRank // 含換音音節的詞，目標詞在候選視窗中的名次
	SelectLatMs []float64  // 開候選（含近音）每次的耗時
}

// WordRank 是一個含換音音節的詞的候選名次（從 1 起算；0 表示不在候選中）。
type WordRank struct {
	Word  string
	Rank  int
	Near  bool // 目標詞是近音候選（有附註）
	Cands int
}

// Auto 是啟用 auto 後的結果。
type Auto struct {
	Clean     string
	Noisy     string
	Degraded  bool
	CleanWhy  []string // 乾淨輸入的自動修正說明（AutoChange.String）
	NoisyWhy  []string
	Rewrites  int
	CleanSwap bool // 乾淨輸入的顯示含自動近音修正
	Latency   []time.Duration
}

type worker struct {
	off, auto, sel *eval.Typist
}

// health 回報神經評分器目前累計的「沒有評分」次數（逾時、錯誤、不可用）；沒接評分器為 0。
// 一句打字期間這個數字增加，表示有按鍵退回統計引擎，那一句兩個角色的比較不成立，要排除。
func health(t *eval.Typist) int {
	if sc, ok := t.E.Rerank.Scorer.(interface{ Stats() neural.Stats }); ok {
		st := sc.Stats()
		return st.Timeouts + st.Errors + st.Unavailable
	}
	return 0
}

func newWorker(open func(Arm) (*engine.Engine, error), p Params, arms ...Arm) (*worker, error) {
	mk := func(arm Arm, cfg fuzzy.Config) (*eval.Typist, error) {
		e, err := open(arm)
		if err != nil {
			return nil, err
		}
		e.Fuzzy = cfg
		// 不暫停學習：暫停狀態下神經評分器不會被呼叫，評測就看不到神經與自動修正的交互作用。
		// 每個句子都以取消結束（不提交、不學習），所以句與句之間沒有狀態。
		ty := &eval.Typist{E: e, ID: e.OpenSession()}
		e.Session(ty.ID).Opt.FuzzyAuto = p.Policy
		return ty, nil
	}
	var sel fuzzy.Config
	for _, pair := range fuzzy.Pairs() {
		if p.Auto.Mode(pair) != fuzzy.Off {
			sel = sel.With(pair, fuzzy.Select)
		}
	}
	var w worker
	var err error
	for _, arm := range arms {
		switch arm {
		case ArmOff:
			w.off, err = mk(ArmOff, fuzzy.Config{})
		case ArmAuto:
			w.auto, err = mk(ArmAuto, p.Auto)
		case ArmSelect:
			w.sel, err = mk(ArmSelect, sel)
		}
		if err != nil {
			return nil, err
		}
	}
	return &w, nil
}

func caseRand(seed int64, i int) *rand.Rand {
	return rand.New(rand.NewSource(seed*1_000_003 + int64(i)))
}

// parallel 以 workers 個執行緒跑 fn(i)；每個執行緒有自己的引擎，結果依序寫回。
func parallel(n, workers int, init func() (*worker, error), fn func(w *worker, i int)) error {
	if workers < 1 {
		workers = 1
	}
	ws := make([]*worker, workers)
	for k := range ws {
		w, err := init()
		if err != nil {
			return err
		}
		ws[k] = w
	}
	var wg sync.WaitGroup
	next := make(chan int)
	for _, w := range ws {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				fn(w, i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)
	wg.Wait()
	return nil
}

// RunBase 跑原版本與僅供選字的部分；結果與自動修正門檻無關，可重複用於不同門檻。
func RunBase(open func(Arm) (*engine.Engine, error), cases []Case, p Params, workers int) ([]Base, error) {
	out := make([]Base, len(cases))
	var gen fuzzy.Config
	for _, pair := range fuzzy.Pairs() {
		if p.Auto.Mode(pair) != fuzzy.Off {
			gen = gen.With(pair, fuzzy.Select)
		}
	}
	err := parallel(len(cases), workers, func() (*worker, error) { return newWorker(open, p, ArmOff, ArmSelect) }, func(w *worker, i int) {
		c := cases[i]
		b := Base{Case: c, Eligible: Eligible(gen, c.Reading)}
		b.Noisy, b.Swapped = Noise(gen, c.Reading, p.Rate, caseRand(p.Seed, i))
		w.off.Latency = nil
		before := health(w.off)
		b.OffClean, b.OffRewrites, _ = replay(w.off, c.Reading)
		if len(b.Swapped) > 0 {
			b.OffNoisy, _, _ = replay(w.off, b.Noisy)
			b.WordRank, b.SelectLatMs = rankWords(w, c, b.Noisy, b.Swapped)
		}
		b.OffDegraded = health(w.off) != before
		b.OffLatency = append([]time.Duration(nil), w.off.Latency...)
		out[i] = b
	})
	return out, err
}

// replay 逐音節打完 reading，回傳最後顯示的文字（提交會得到的文字）與逐鍵回改次數（已顯示字元被改的次數），
// 然後取消組字：不提交、不學習。
func replay(t *eval.Typist, reading []zhuyin.Syllable) (text string, rewrites int, why []string) {
	var prev []rune
	for _, y := range reading {
		cur := []rune(t.Type(y).Text())
		for i := 0; i < len(prev) && i < len(cur); i++ {
			if prev[i] != cur[i] {
				rewrites++
			}
		}
		prev, text = cur, string(cur)
	}
	for _, c := range t.E.Session(t.ID).AutoChanges() {
		why = append(why, c.String())
	}
	t.Cancel()
	return text, rewrites, why
}

// rankWords 在僅供選字的 session 打完錯音輸入後，對每個含換音音節的詞開候選，找目標詞的名次。
func rankWords(w *worker, c Case, noisy []zhuyin.Syllable, swapped []int) (ranks []WordRank, near []float64) {
	t := w.sel
	for _, y := range noisy {
		t.Type(y)
	}
	defer t.Cancel()
	isSwapped := map[int]bool{}
	for _, at := range swapped {
		isSwapped[at] = true
	}
	pos := 0
	for _, word := range c.Words {
		n := len([]rune(word))
		touched := false
		for k := pos; k < pos+n; k++ {
			touched = touched || isSwapped[k]
		}
		if touched {
			t0 := time.Now()
			_, _ = t.E.Do(t.ID, proto.Request{Type: proto.Cursor, To: pos})
			snap, _ := t.E.Do(t.ID, proto.Request{Type: proto.Candidates, Length: n})
			near = append(near, float64(time.Since(t0).Microseconds())/1000)
			wr := WordRank{Word: word}
			if snap.Candidates != nil {
				all := append([]proto.CandidateItem(nil), snap.Candidates.Items...)
				for pg := 1; pg < snap.Candidates.Pages; pg++ {
					next, _ := t.E.Do(t.ID, proto.Request{Type: proto.Page, Index: pg})
					if next.Candidates != nil {
						all = append(all, next.Candidates.Items...)
					}
				}
				wr.Cands = len(all)
				for k, it := range all {
					if it.Text == word {
						wr.Rank, wr.Near = k+1, it.Note != ""
						break
					}
				}
			}
			ranks = append(ranks, wr)
			_, _ = t.E.Do(t.ID, proto.Request{Type: proto.Close})
		}
		pos += n
	}
	return ranks, near
}

// RunAuto 以 p.Policy 跑 auto 的乾淨與錯音輸入。
func RunAuto(open func(Arm) (*engine.Engine, error), bases []Base, p Params, workers int) ([]Auto, error) {
	out := make([]Auto, len(bases))
	err := parallel(len(bases), workers, func() (*worker, error) { return newWorker(open, p, ArmAuto) }, func(w *worker, i int) {
		b := bases[i]
		w.auto.Latency = nil
		before := health(w.auto)
		a := Auto{}
		a.Clean, a.Rewrites, a.CleanWhy = replay(w.auto, b.Case.Reading)
		if len(b.Swapped) > 0 {
			a.Noisy, _, a.NoisyWhy = replay(w.auto, b.Noisy)
		}
		a.Degraded = health(w.auto) != before
		a.Latency = append([]time.Duration(nil), w.auto.Latency...)
		out[i] = a
	})
	return out, err
}

// Report 是彙總。所有比率都附分母。
type Report struct {
	Sentences int `json:"sentences"`
	Chars     int `json:"chars"`
	Excluded  int `json:"excluded_degraded"` // 神經評分器打這句時有按鍵沒評分，兩個角色不可比，已排除

	Clean struct {
		Applicable      int     `json:"applicable_sentences"`   // 原版本答對且有可換音音節
		FalseChanges    int     `json:"false_change_sentences"` // 其中 auto 的結果與參考不同
		FalseChangeRate float64 `json:"false_change_rate"`
		FalseChars      int     `json:"false_change_chars"` // 編輯距離合計
		AppChars        int     `json:"applicable_chars"`
		EligibleTotal   int     `json:"eligible_sentences"` // 全部有可換音音節的句子（對照分母）
		BaseWrong       int     `json:"baseline_wrong_among_eligible"`
	} `json:"clean"`

	Noisy struct {
		Sentences   int     `json:"sentences"`
		Swaps       int     `json:"swapped_syllables"`
		OffExact    int     `json:"off_exact"`
		AutoExact   int     `json:"auto_exact"`
		OffCER      float64 `json:"off_cer"`
		AutoCER     float64 `json:"auto_cer"`
		Fixed       int     `json:"fixed"`  // 原版本錯、auto 對
		Harmed      int     `json:"harmed"` // 原版本對、auto 錯
		Changed     int     `json:"auto_changed"`
		Improved    int     `json:"changed_improved"`
		Worsened    int     `json:"changed_worsened"`
		Precision   float64 `json:"auto_precision"` // 改了之後編輯距離變小 ÷ 有改
		Coverage    float64 `json:"auto_coverage"`  // 有改 ÷ 原版本錯的句子
		RepairRate  float64 `json:"repair_rate"`    // 原版本錯、auto 對 ÷ 原版本錯
		OffChars    int     `json:"off_edit_chars"`
		AutoChars   int     `json:"auto_edit_chars"`
		RefChars    int     `json:"ref_chars"`
		OffWrongSen int     `json:"off_wrong_sentences"`
	} `json:"noisy"`

	Recall struct {
		Words     int     `json:"words"` // 含換音音節的詞（分母）
		At8       float64 `json:"recall_at_8"`
		AtPage1   float64 `json:"recall_at_10"`
		At16      float64 `json:"recall_at_16"`
		Any       float64 `json:"recall_any"`
		NearWords int     `json:"found_as_near"`
	} `json:"recall"`

	Rewrites struct {
		Off  int `json:"off"`
		Auto int `json:"auto"`
	} `json:"rewrites_clean"`

	LatencyMs struct {
		Off, Auto, OpenNear Percentiles
	} `json:"latency_ms"`
}

// Percentiles 是延遲分位數（毫秒）。
type Percentiles struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
}

func ms(ds []time.Duration, p float64) float64 {
	return float64(eval.Percentile(ds, p).Microseconds()) / 1000
}

func pct(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	return s[min(len(s)-1, int(float64(len(s)-1)*p/100+0.5))]
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// Summarize 彙總。
func Summarize(bases []Base, autos []Auto) Report {
	var r Report
	var offLat, autoLat []time.Duration
	var near []float64
	var recallN, r8, r10, r16, rany int
	for i, b := range bases {
		a := autos[i]
		if b.OffDegraded || a.Degraded {
			r.Excluded++
			continue
		}
		refN := len([]rune(b.Case.Ref))
		r.Sentences++
		r.Chars += refN
		r.Rewrites.Off += b.OffRewrites
		r.Rewrites.Auto += a.Rewrites
		offLat = append(offLat, b.OffLatency...)
		autoLat = append(autoLat, a.Latency...)
		if b.Eligible {
			r.Clean.EligibleTotal++
			if b.OffClean != b.Case.Ref {
				r.Clean.BaseWrong++
			} else {
				r.Clean.Applicable++
				r.Clean.AppChars += refN
				if a.Clean != b.Case.Ref {
					r.Clean.FalseChanges++
					r.Clean.FalseChars += eval.EditDistance(a.Clean, b.Case.Ref)
				}
			}
		}
		if len(b.Swapped) > 0 {
			n := &r.Noisy
			n.Sentences++
			n.Swaps += len(b.Swapped)
			n.RefChars += refN
			eo, ea := eval.EditDistance(b.OffNoisy, b.Case.Ref), eval.EditDistance(a.Noisy, b.Case.Ref)
			n.OffChars += eo
			n.AutoChars += ea
			offOK, autoOK := b.OffNoisy == b.Case.Ref, a.Noisy == b.Case.Ref
			if offOK {
				n.OffExact++
			} else {
				n.OffWrongSen++
			}
			if autoOK {
				n.AutoExact++
			}
			if !offOK && autoOK {
				n.Fixed++
			}
			if offOK && !autoOK {
				n.Harmed++
			}
			if a.Noisy != b.OffNoisy {
				n.Changed++
				switch {
				case ea < eo:
					n.Improved++
				case ea > eo:
					n.Worsened++
				}
			}
			near = append(near, b.SelectLatMs...)
			for _, wr := range b.WordRank {
				recallN++
				if wr.Rank > 0 {
					rany++
					if wr.Rank <= 8 {
						r8++
					}
					if wr.Rank <= 10 {
						r10++
					}
					if wr.Rank <= 16 {
						r16++
					}
					if wr.Near {
						r.Recall.NearWords++
					}
				}
			}
		}
	}
	n := &r.Noisy
	n.OffCER = ratio(n.OffChars, n.RefChars)
	n.AutoCER = ratio(n.AutoChars, n.RefChars)
	n.Precision = ratio(n.Improved, n.Changed)
	n.Coverage = ratio(n.Changed, n.OffWrongSen)
	n.RepairRate = ratio(n.Fixed, n.OffWrongSen)
	r.Clean.FalseChangeRate = ratio(r.Clean.FalseChanges, r.Clean.Applicable)
	r.Recall.Words = recallN
	r.Recall.At8, r.Recall.AtPage1, r.Recall.At16, r.Recall.Any = ratio(r8, recallN), ratio(r10, recallN), ratio(r16, recallN), ratio(rany, recallN)
	r.LatencyMs.Off = Percentiles{ms(offLat, 50), ms(offLat, 95), ms(offLat, 99)}
	r.LatencyMs.Auto = Percentiles{ms(autoLat, 50), ms(autoLat, 95), ms(autoLat, 99)}
	r.LatencyMs.OpenNear = Percentiles{pct(near, 50), pct(near, 95), pct(near, 99)}
	return r
}

// SummarizeBy 依標籤分層彙總：每個標籤只含帶有該標籤的句子；沒有任何標籤的句子歸入 ""。
func SummarizeBy(bases []Base, autos []Auto) map[string]Report {
	groups := map[string][]int{}
	for i, b := range bases {
		if len(b.Case.Tags) == 0 {
			groups[""] = append(groups[""], i)
		}
		for _, t := range b.Case.Tags {
			groups[t] = append(groups[t], i)
		}
	}
	out := map[string]Report{}
	for tag, idx := range groups {
		var bs []Base
		var as []Auto
		for _, i := range idx {
			bs, as = append(bs, bases[i]), append(as, autos[i])
		}
		out[tag] = Summarize(bs, as)
	}
	return out
}
