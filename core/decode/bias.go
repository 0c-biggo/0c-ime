package decode

import (
	"git.biggo.com/0c/ime/core/lexicon"
	"math"
)

func (d *Decoder) evidenceMass(start, end int, left, word string, mass float64) float64 {
	mass *= d.correctionFactor(start, end, word) // 已提交偏好先計分；當次明確更正可覆蓋舊偏好。
	if d.Evidence != nil {
		a, total := d.Evidence(start, end, left, word)
		if total > 0 {
			mass = (mass + a) / (1 + total)
		}
	}
	return mass
}

func (d *Decoder) arcLog(start, end int, left string, e lexicon.Entry, cost float64) float64 {
	base := math.Log(d.arcProbability(start, end, left, e)) + math.Log(e.PRW) - cost
	if d.Evidence != nil || d.CorrectionPenalty != nil {
		base = math.Log(d.evidenceMass(start, end, left, e.Surface, math.Exp(base)))
	}
	return base
}

func (d *Decoder) correctionFactor(start, end int, word string) float64 {
	if d.CorrectionPenalty == nil {
		return 1
	}
	return math.Exp(-d.CorrectionPenalty(start, end, word))
}
