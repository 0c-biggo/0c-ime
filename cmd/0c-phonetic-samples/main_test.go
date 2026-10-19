package main

import "testing"

func TestSplitPoint(t *testing.T) {
	for _, tc := range []struct {
		words   []string
		n, want int
	}{
		{[]string{"新聞", "報導", "中央", "銀行"}, 8, 4},
		{[]string{"新聞報導中央銀行"}, 8, 0},
		{[]string{"台灣", "人"}, 3, 0},
		{[]string{"新聞", "報導", "中央銀行"}, 8, 4},
	} {
		if got := splitPoint(tc.words, tc.n); got != tc.want {
			t.Fatalf("%v: got %d want %d", tc.words, got, tc.want)
		}
	}
}
