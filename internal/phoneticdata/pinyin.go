// Package phoneticdata 保存訓練資料的觀測證據，與舊詞典的無調＝輕聲契約分離。
package phoneticdata

import (
	"fmt"
	"strings"

	"git.biggo.com/0c/ime/core/zhuyin"
)

type Reading struct {
	Raw        string `json:"raw"`
	Base       string `json:"base"`
	Tone       int    `json:"tone"` // 0 是未知，不是輕聲；5 才是觀測到的輕聲。
	ToneStatus string `json:"tone_status"`
	Status     string `json:"status"`
}

// Pinyin 不參考漢字答案。zeroIsNeutral 必須由來源格式明確聲明。
func Pinyin(raw string, zeroIsNeutral bool) (Reading, error) {
	p := strings.ToLower(strings.TrimSpace(raw))
	if p == "" {
		return Reading{}, fmt.Errorf("empty pinyin")
	}
	tone, count := 0, 0
	for _, r := range p {
		for i, group := range []string{"āēīōūǖ\u0304", "áéíóúǘế\u0301", "ǎěǐǒǔǚ\u030c", "àèìòùǜề\u0300"} {
			if strings.ContainsRune(group, r) {
				tone = i + 1
				count++
			}
		}
	}
	if p[len(p)-1] >= '0' && p[len(p)-1] <= '5' {
		if count > 0 {
			return Reading{}, fmt.Errorf("mixed numeric and marked tones")
		}
		count++
		tone = int(p[len(p)-1] - '0')
		if tone == 0 {
			if !zeroIsNeutral {
				return Reading{}, fmt.Errorf("tone zero convention not declared")
			}
			tone = 5
			p = p[:len(p)-1] + "5"
		}
	}
	if count > 1 {
		return Reading{}, fmt.Errorf("multiple tone marks")
	}
	syllable, err := zhuyin.FromPinyin(p)
	if err != nil {
		return Reading{}, err
	}
	result := Reading{Raw: raw, Base: string(syllable.Base()), Tone: tone, ToneStatus: "unknown", Status: "complete"}
	if count == 1 {
		result.ToneStatus = "explicit"
	}
	return result, nil
}
