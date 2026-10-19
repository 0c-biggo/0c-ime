package zhuyin

import "testing"

// 漢字的範圍固定在 Unicode 15.0，不跟著 Go 內建的 Unicode 表升級。
func TestIsHanIsPinned(t *testing.T) {
	for r, want := range map[rune]bool{
		'丼': true, 'A': false, 'ㄅ': false, '，': false,
		0x323AF: true,  // 擴充 H 區的最後一個字（Unicode 15.0）
		0x2B73A: false, // Unicode 15.0 之後才補進擴充 C 區尾端
		0x323B0: false, // 擴充 J 區（Unicode 17.0）
	} {
		if IsHan(r) != want {
			t.Errorf("IsHan(%U) 應為 %v", r, want)
		}
	}
}
