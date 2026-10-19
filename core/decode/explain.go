package decode

import (
	"math"

	"git.biggo.com/0c/ime/core/input"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/zhuyin"
)

// Contribution 分開列出實際語言模型、純系統模型、讀音成本與輸入成本。
// 前綴是不同完整詞的機率和，不能假裝成某個單字的 unigram 分數。
type Contribution struct {
	Word                string   `json:"word"`
	Reading             []string `json:"reading"`
	Typed               []string `json:"typed,omitempty"` // 走了有成本的假設（近音）時，使用者實際打的音；與 Reading 逐音節對照
	LanguageProbability float64  `json:"language_probability"`
	SystemProbability   float64  `json:"system_probability"`
	ReadingWeight       float64  `json:"reading_weight"`
	InputCost           float64  `json:"input_cost"`
	Continuation        float64  `json:"continuation"`
	FrequencyScale      float64  `json:"frequency_scale"`
	Mass                float64  `json:"mass"`
}

type ExplainedSegment struct {
	Seg
	Left              string         `json:"left"`
	LogScore          float64        `json:"log_score"`
	LocalBonus        float64        `json:"local_bonus"`
	CorrectionPenalty float64        `json:"correction_penalty"`
	Contributions     []Contribution `json:"contributions"`
}

type DocumentContext struct {
	Left   string  `json:"left"`
	Weight float64 `json:"weight"`
}
type Explanation struct {
	DocumentContext *DocumentContext   `json:"document_context,omitempty"`
	Reachable       bool               `json:"reachable"`
	LogScore        float64            `json:"log_score"`
	Segments        []ExplainedSegment `json:"segments"`
}

func (d *Decoder) contribution(start, end int, left string, e lexicon.Entry, cost, cont float64) Contribution {
	p := d.Score.Prob(left, e.Surface)
	adjusted := d.arcProbability(start, end, left, e)
	scale := 1.0
	if p > 0 {
		scale = adjusted / p
	}
	sys := p
	if ev, ok := d.Score.(Evidenced); ok {
		sys = ev.SystemProb(left, e.Surface)
	}
	r := make([]string, len(e.Reading))
	for i, y := range e.Reading {
		r[i] = string(y)
	}
	return Contribution{Word: e.Surface, Reading: r, LanguageProbability: p, SystemProbability: sys, ReadingWeight: e.PRW,
		InputCost: cost, Continuation: cont, FrequencyScale: scale, Mass: adjusted * e.PRW * math.Exp(-cost) * cont}
}

// ExplainBest 僅供離線診斷；不在逐鍵熱路徑收集或保存文字。
func (d *Decoder) ExplainBest(lat input.Lattice, locks map[int]Lock, prefix bool) Explanation {
	path, score := d.Best(lat, locks, prefix)
	if math.IsInf(score, 0) {
		return Explanation{}
	}
	out := Explanation{Reachable: true, LogScore: score}
	if c, ok := d.Score.(interface{ DocumentContext() (string, float64) }); ok {
		left, weight := c.DocumentContext()
		out.DocumentContext = &DocumentContext{Left: left, Weight: weight}
	}
	left := d.InitialLeft()
	for _, g := range path {
		part := ExplainedSegment{Seg: g, Left: left}
		if g.Partial {
			seen := map[string]int{}
			cands := d.tailEntries(lat, g.Start)
			for _, c := range cands {
				if d.SeparateContinuation && len(c.e.Reading) <= g.End-g.Start {
					continue
				}
				if string([]rune(c.e.Surface)[:g.End-g.Start]) != g.Word {
					continue
				}
				cont := 1.0
				if len(c.e.Reading) > g.End-g.Start && d.Continue > 0 {
					cont = d.Continue
				}
				v := d.contribution(g.Start, g.End, left, c.e, c.cost, cont)
				v.LanguageProbability = d.Score.Prob(left, c.e.Surface)
				v.FrequencyScale = 1 // 前綴延伸沿用補全的頻率模型。
				v.Mass = v.LanguageProbability * v.ReadingWeight * math.Exp(-v.InputCost) * cont
				if i, ok := seen[v.Word]; ok {
					if v.Mass > part.Contributions[i].Mass {
						part.Contributions[i] = v
					}
				} else {
					seen[v.Word] = len(part.Contributions)
					part.Contributions = append(part.Contributions, v)
				}
			}
		} else {
			best := Contribution{}
			d.spans(lat[:g.End], g.Start, func(reading []zhuyin.Syllable, cost float64) {
				if len(reading) != g.End-g.Start {
					return
				}
				for _, e := range d.Lex.Exact(reading) {
					if e.Surface == g.Word {
						v := d.contribution(g.Start, g.End, left, e, cost, 1)
						if v.Mass > best.Mass {
							best = v
						}
					}
				}
			})
			part.Contributions = []Contribution{best}
		}
		for i := range part.Contributions {
			if c := &part.Contributions[i]; c.InputCost > 0 && g.End <= len(lat) {
				for at := g.Start; at < g.End; at++ {
					c.Typed = append(c.Typed, string(lat[at][0].Syl)) // 音節格第一個假設是使用者打的音
				}
			}
		}
		mass := 0.0
		for _, c := range part.Contributions {
			mass += c.Mass
		}
		adjusted := d.evidenceMass(g.Start, g.End, left, g.Word, mass)
		if d.CorrectionPenalty != nil {
			part.CorrectionPenalty = d.CorrectionPenalty(g.Start, g.End, g.Word)
		}
		part.LocalBonus += math.Log(adjusted) - math.Log(mass) + part.CorrectionPenalty
		part.LogScore = math.Log(adjusted)
		out.Segments = append(out.Segments, part)
		left = g.Word
	}
	return out
}
