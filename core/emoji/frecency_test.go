package emoji

import (
	"slices"
	"testing"
)

const day = 86400

func TestSeedKeepsOrderAndOnePickDoesNotJump(t *testing.T) {
	recent := []string{"😂", "👍", "🙏", "🔥"}
	stats := SeedStats(recent)
	order, stats := Pick(recent, stats, "🎉", 100*day)
	if !slices.Equal(order, []string{"😂", "👍", "🙏", "🔥", "🎉"}) {
		t.Fatalf("升級後不洗牌、新選一次不超過舊的：%v", order)
	}
	// 連續選幾次就往前爬，但要明顯超過才交換。
	for i := 0; i < 3; i++ {
		order, stats = Pick(order, stats, "🎉", 100*day+int64(i))
	}
	if order[0] != "🎉" {
		t.Fatalf("常用的排前面：%v", order)
	}
}

func TestHysteresisKeepsSimilarItemsStable(t *testing.T) {
	order, stats := []string{}, map[string]Stat{}
	now := int64(10 * day)
	for i := 0; i < 5; i++ {
		order, stats = Pick(order, stats, "😀", now)
		order, stats = Pick(order, stats, "😃", now)
	}
	before := slices.Clone(order)
	order, stats = Pick(order, stats, "😃", now) // 只多一次：差距不到 1.5 倍，不換位置
	if !slices.Equal(order, before) {
		t.Fatalf("差不多的兩個不互換：%v → %v", before, order)
	}
}

func TestDecayLetsOldFavouritesFallBack(t *testing.T) {
	order, stats := []string{}, map[string]Stat{}
	for i := 0; i < 10; i++ {
		order, stats = Pick(order, stats, "🐱", int64(day))
	}
	later := int64(day + 8*HalfLife)
	order, stats = Pick(order, stats, "🐶", later)
	order, stats = Pick(order, stats, "🐶", later)
	if order[0] != "🐶" {
		t.Fatalf("很久沒用的退後：%v %v", order, stats)
	}
}

func TestPickBoundsAndClean(t *testing.T) {
	order, stats := []string{}, map[string]Stat{}
	for i := 0; i < MaxRecent+10; i++ {
		order, stats = Pick(order, stats, faces[i], int64(day+i))
	}
	if len(order) != MaxRecent || len(stats) != MaxRecent {
		t.Fatalf("%d %d", len(order), len(stats))
	}
	clean := CleanStats(map[string]Stat{"not-emoji": {Score: 1}, faces[0]: {Score: 2}, faces[1]: {Score: -1}}, []string{faces[0], faces[1]})
	if len(clean) != 1 {
		t.Fatalf("%v", clean)
	}
}
