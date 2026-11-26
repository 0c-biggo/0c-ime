package session

import "github.com/0c-biggo/0c-ime/core/english"

// englishDict 是這次查詢用的英文字典：英文模式的補字與修正用完整字典；中英混打用 Mixed()，不認語料新加、
// 三個字母以內的詞（da、tv、ae），免得把 Dan␣、A股、T恤 切成 Da斯、Ae五、Tv預（#358）。舊字典兩者相同。
func (s *Session) englishDict() *english.Dictionary {
	if s.english {
		return s.English
	}
	return s.English.Mixed()
}

// englishCommonPrior 是學英文時「接近常用詞」（NearCommon）的門檻。舊字典是 ESDB size ≤ 50（63,681 個可建議的詞）；
// 詞頻字典裡 ESDB size 50 的詞是 39 分（englishpack 的 esdbRankedWeight），39 涵蓋同一批詞再加上語料裡的
// 896 個詞（#358），學習不因換字典變鬆。
func englishCommonPrior(d *english.Dictionary) float64 {
	if d.Ranked() {
		return 39
	}
	return 50
}
