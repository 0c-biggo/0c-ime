package english

import (
	_ "embed"
	"fmt"
	"strings"
)

// AlnumKind 是英數詞在中英混打的處理方式（alnum.tsv 的「處理」欄）。
type AlnumKind int

const (
	AlnumUnknown AlnumKind = iota
	// AlnumLiteral：照字面，不組成中文（win10、ps4、usb4）。
	AlnumLiteral
	// AlnumKeysSecond：照順序打就是常用的注音（mp3 允、m3 與）：照舊出字，↓ 第二項是原按鍵。
	AlnumKeysSecond
)

//go:embed alnum.tsv
var alnumSource string

var alnumTokens = mustParseAlnum(alnumSource)

// Alnum 回報 t 是不是 alnum.tsv 收的英數詞（不分大小寫）與它的處理方式（#249）。
func Alnum(t string) AlnumKind { return alnumTokens[strings.ToLower(t)] }

// AlnumTokens 回傳收錄的英數詞（小寫），供測試檢查。
func AlnumTokens() map[string]AlnumKind {
	out := make(map[string]AlnumKind, len(alnumTokens))
	for t, k := range alnumTokens {
		out[t] = k
	}
	return out
}

func mustParseAlnum(src string) map[string]AlnumKind {
	m, err := ParseAlnum(src)
	if err != nil {
		panic(err)
	}
	return m
}

// ParseAlnum 讀 alnum.tsv：「詞<TAB>處理<TAB>依據」，# 開頭是註解。詞要是小寫英文字母加數字、有字母也有數字；
// 處理是 literal 或 keys；依據不能空白；不能重複。
func ParseAlnum(src string) (map[string]AlnumKind, error) {
	out := map[string]AlnumKind{}
	for n, line := range strings.Split(src, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 3 || strings.TrimSpace(f[2]) == "" {
			return nil, fmt.Errorf("alnum.tsv 第 %d 行：要有詞、處理、依據三欄", n+1)
		}
		t := f[0]
		letters, digits := false, false
		for _, r := range t {
			switch {
			case r >= 'a' && r <= 'z':
				letters = true
			case r >= '0' && r <= '9':
				digits = true
			default:
				return nil, fmt.Errorf("alnum.tsv 第 %d 行：%q 只能有小寫英文字母與數字", n+1, t)
			}
		}
		if !letters || !digits {
			return nil, fmt.Errorf("alnum.tsv 第 %d 行：%q 要有字母也有數字", n+1, t)
		}
		if _, dup := out[t]; dup {
			return nil, fmt.Errorf("alnum.tsv 第 %d 行：%q 重複", n+1, t)
		}
		switch f[1] {
		case "literal":
			out[t] = AlnumLiteral
		case "keys":
			out[t] = AlnumKeysSecond
		default:
			return nil, fmt.Errorf("alnum.tsv 第 %d 行：處理 %q 要是 literal 或 keys", n+1, f[1])
		}
	}
	return out, nil
}
