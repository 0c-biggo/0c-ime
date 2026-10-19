package zhuyin

import "unicode"

// han 是 Unicode 15.0 的漢字（Script=Han）區段。刻意不用 unicode.Han：那張表跟著 Go 版本升級
// （Go 1.26 是 Unicode 15.0，Go 1.27 是 17.0），同一份來源用不同版本的 Go 匯入會得到不同的字表。
// 要收新區段的字時改這張表，讓它成為資料上的決定，而不是換工具鏈的副作用。
var han = &unicode.RangeTable{
	R16: []unicode.Range16{
		{0x2E80, 0x2E99, 1},
		{0x2E9B, 0x2EF3, 1},
		{0x2F00, 0x2FD5, 1},
		{0x3005, 0x3007, 2},
		{0x3021, 0x3029, 1},
		{0x3038, 0x303B, 1},
		{0x3400, 0x4DBF, 1},
		{0x4E00, 0x9FFF, 1},
		{0xF900, 0xFA6D, 1},
		{0xFA70, 0xFAD9, 1},
	},
	R32: []unicode.Range32{
		{0x16FE2, 0x16FE3, 1},
		{0x16FF0, 0x16FF1, 1},
		{0x20000, 0x2A6DF, 1},
		{0x2A700, 0x2B739, 1},
		{0x2B740, 0x2B81D, 1},
		{0x2B820, 0x2CEA1, 1},
		{0x2CEB0, 0x2EBE0, 1},
		{0x2F800, 0x2FA1D, 1},
		{0x30000, 0x3134A, 1},
		{0x31350, 0x323AF, 1},
	},
}

// IsHan 回報是否為漢字。
func IsHan(r rune) bool { return unicode.Is(han, r) }
