package main

import "testing"

func TestSplitPointKeepsReferenceWordBoundaries(t *testing.T) {
	for _, c := range []struct {
		words   []string
		n, want int
	}{
		{[]string{"一", "口", "井"}, 3, 1},
		{[]string{"應該", "另存", "新", "圖"}, 6, 2},
		{[]string{"我", "想", "吃", "小籠包"}, 6, 3},
		{[]string{"小籠包"}, 3, 0},
		{nil, 0, 0},
	} {
		if got := splitPoint(c.words, c.n); got != c.want {
			t.Fatalf("%v: %d want %d", c.words, got, c.want)
		}
	}
}
