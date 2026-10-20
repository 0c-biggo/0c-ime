package decode

import (
	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// Only a proper prefix of an already reachable complete word needs this guard.
// Ordinary evidence for complete words and learned adjacent word pairs remains
// available. Unfinished future words do not create a protected boundary.
func (d *Decoder) prepareMass(lat input.Lattice) {
	d.massScale, d.longerEnd = nil, nil
	a, ok := d.Score.(Adaptive)
	if !ok || !a.HasAdaptation() {
		return
	}
	d.longerEnd = make([]int, len(lat))
	for at := range lat {
		d.spans(lat, at, func(reading []zhuyin.Syllable, _ float64) {
			if len(reading) <= 1 {
				return
			}
			for _, e := range d.Lex.Exact(reading) {
				if !e.Correction {
					d.longerEnd[at] = max(d.longerEnd[at], at+len(reading))
					break
				}
			}
		})
	}
}

func (d *Decoder) arcProbability(start, end int, left string, e lexicon.Entry) float64 {
	if start >= 0 && start < len(d.longerEnd) && end < d.longerEnd[start] {
		return d.languageProbability(left, e)
	}
	return d.Score.Prob(left, e.Surface)
}

// Adaptive identifies a scorer with ordinary learned counts. These counts may
// redistribute a reading's alternatives, but must not inflate its total mass.
type Adaptive interface {
	// PriorMass retains the mixture's denominator but excludes positive learned
	// counts. Otherwise normalization itself could inflate unobserved fragments.
	PriorMass(left, word string) float64
	HasAdaptation() bool
}

func (d *Decoder) languageProbability(left string, e lexicon.Entry) float64 {
	p := d.Score.Prob(left, e.Surface)
	a, ok := d.Score.(Adaptive)
	if !ok || !a.HasAdaptation() {
		return p
	}
	key := left + "\t" + zhuyin.Key(e.Reading)
	if factor, ok := d.massScale[key]; ok {
		return p * factor
	}
	// Deduplicate surfaces so duplicated sources cannot manufacture mass.
	entries := d.Lex.Exact(e.Reading)
	weights := map[string]float64{}
	var order []string
	for _, c := range entries {
		if _, ok := weights[c.Surface]; !ok {
			order = append(order, c.Surface)
		}
		weights[c.Surface] = max(weights[c.Surface], c.PRW)
	}
	system, personal := 0.0, 0.0
	for _, word := range order {
		system += a.PriorMass(left, word) * weights[word]
		personal += d.Score.Prob(left, word) * weights[word]
	}
	factor := 1.0
	if system > 0 && personal > 0 {
		factor = min(1, system/personal)
	}
	if d.massScale == nil {
		d.massScale = map[string]float64{}
	}
	d.massScale[key] = factor
	return p * factor
}
