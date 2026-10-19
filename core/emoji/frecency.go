package emoji

import (
	"math"
	"slices"
)

// 常用＋最近排序（#124）：每個項目記衰減後的選取次數，常用的穩定排前面，很久沒用的慢慢退後；
// 新選的會往前，但一次不會直接超過天天用的。順序本身另外保存（personal.Store.EmojiRecent），
// 分數要明顯超過前一名才交換，兩個差不多的表情不會每次互換、數字鍵按錯。

// Stat 是一個項目的常用分數：At 時的衰減後次數。At 為 0 表示時間未知（由舊版的最近清單轉來），
// 下一次有時鐘的選取時才定錨，在那之前不衰減。
type Stat struct {
	Score float64 `json:"s"`
	At    int64   `json:"t,omitempty"`
}

const (
	HalfLife   = 14 * 86400 // 秒：兩週沒用分數減半
	SwapMargin = 1.5        // 分數要超過前一名的這個倍數才交換
)

// Decayed 回傳 now 時的分數。
func Decayed(st Stat, now int64) float64 {
	if st.At == 0 || now <= st.At {
		return st.Score
	}
	return st.Score * math.Exp2(-float64(now-st.At)/HalfLife)
}

// SeedStats 把舊版的最近清單轉成初始分數，順序不變：第一個略低於 2、最後一個略高於 1，
// 新選一次（1 分）不會立刻超過任何一個。
func SeedStats(recent []string) map[string]Stat {
	stats := map[string]Stat{}
	n := float64(len(recent))
	for i, text := range recent {
		stats[text] = Stat{Score: 1 + (n-float64(i))/(n+1)}
	}
	return stats
}

// Pick 記下一次選取，回傳新的順序與分數（都不與輸入共用）。order 是目前保存的順序。
func Pick(order []string, stats map[string]Stat, text string, now int64) ([]string, map[string]Stat) {
	next := make(map[string]Stat, len(stats)+1)
	for k, st := range stats {
		if st.At == 0 && now > 0 {
			st.At = now // 定錨：從現在開始衰減
		}
		next[k] = st
	}
	st := next[text]
	next[text] = Stat{Score: Decayed(st, now) + 1, At: now}
	order = CleanRecent(order)
	if !slices.Contains(order, text) {
		if len(order) >= MaxRecent { // 滿了：讓出分數最低的位置，新選的一定進得來
			weakest := 0
			for i := range order {
				if Decayed(next[order[i]], now) <= Decayed(next[order[weakest]], now) {
					weakest = i
				}
			}
			order = slices.Delete(order, weakest, weakest+1)
		}
		order = append(order, text) // 新項目從清單尾端（其餘目錄之前）開始往前爬
	}
	score := func(k string) float64 { return Decayed(next[k], now) }
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && score(order[j]) > SwapMargin*score(order[j-1]); j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	if len(order) > MaxRecent {
		order = order[:MaxRecent]
	}
	for k := range next { // 分數只留給清單裡的項目，大小有界
		if !slices.Contains(order, k) {
			delete(next, k)
		}
	}
	return order, next
}

// CleanStats 只保留目錄裡、而且在順序清單中的項目；分數不合理的丟掉。
func CleanStats(stats map[string]Stat, order []string) map[string]Stat {
	var out map[string]Stat
	for k, st := range stats {
		if Contains(k) && slices.Contains(order, k) && st.Score > 0 && !math.IsInf(st.Score, 0) && !math.IsNaN(st.Score) {
			if out == nil {
				out = map[string]Stat{}
			}
			out[k] = st
		}
	}
	return out // 沒有任何項目時為 nil，存檔來回一致
}
