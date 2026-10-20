package corpus

import (
	"bufio"
	"os"
	"strings"
	"unicode/utf8"
)

// 非台灣正體的內容（#172）：維基、新聞與 PTT 的計數混有日文、簡體與粵語句子，「空気」「国家」這類片段
// 以詞庫斷詞後變成「空→気」「国→家」的配對，單字候選就把詞頻低幾百倍的日文新字體、簡化字排到常用字前面。
// 計數前以句子為單位判斷，整句不計。

// Foreign 的判斷結果。
const (
	Taiwan     = ""
	Japanese   = "japanese"   // 有平假名或片假名
	Simplified = "simplified" // 有簡化字（OpenCC 簡轉繁的字、不在台灣用字裡）
	Cantonese  = "cantonese"  // 有粵語用字（嘅、咗、喺…，不在台灣用字裡）
)

// cantonese 是粵語書面語常用、國語不用的字；跟台灣用字重疊的（佢、咁、睇在 Big5 裡）由 common 排除。
const cantonese = "嘅咗冇喺啲嚟嘢哋噉咁佢乜嗰睇攞冚揾搵嬲咩唔"

// ScriptFilter 判斷句子是不是台灣正體中文。
type ScriptFilter struct {
	markers map[rune]string // 字 → 這個字代表的內容種類
}

// NewScriptFilter 以標記字建立過濾器。simplified 是簡化字（見 SimplifiedOnly），common 是台灣用字：
// 兩邊都有的字（台、后、着 這類簡繁同形或台灣也用的字）不當標記；粵語用字也一樣。
func NewScriptFilter(simplified map[rune]bool, common func(rune) bool) *ScriptFilter {
	f := &ScriptFilter{markers: map[rune]string{}}
	for r := range simplified {
		if !common(r) {
			f.markers[r] = Simplified
		}
	}
	for _, r := range cantonese {
		if !common(r) {
			f.markers[r] = Cantonese
		}
	}
	return f
}

// Markers 回報有幾個標記字（報告用）。
func (f *ScriptFilter) Markers() int { return len(f.markers) }

// Foreign 回傳句子的內容種類；台灣正體回傳 Taiwan（空字串）。
func (f *ScriptFilter) Foreign(sentence string) string {
	for _, r := range sentence {
		if IsKana(r) {
			return Japanese
		}
		if k, ok := f.markers[r]; ok {
			return k
		}
	}
	return Taiwan
}

// IsKana 回報 r 是不是平假名或片假名（含半形片假名與長音符號）。注音符號不算。
func IsKana(r rune) bool {
	return r >= 0x3041 && r <= 0x309F || r >= 0x30A0 && r <= 0x30FF && r != 0x30FB || // 中點「・」不算
		r >= 0x31F0 && r <= 0x31FF || r >= 0xFF66 && r <= 0xFF9D
}

// Sentences 以句末標點與換行切句，fn 收到的句子含句末標點。
func Sentences(text string, fn func(string)) {
	start := 0
	for i, r := range text {
		switch r {
		case '。', '！', '？', '!', '?', '\n', '；', ';':
			end := i + utf8.RuneLen(r)
			if end > start {
				fn(text[start:end])
			}
			start = end
		}
	}
	if start < len(text) {
		fn(text[start:])
	}
}

// SimplifiedOnly 讀 OpenCC 的 STCharacters.txt，回傳只當簡化字用的字：轉換目標不含它自己
// （国→國；后→後 后 含自己，不算）。
func SimplifiedOnly(path string) (map[rune]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[rune]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := sc.Text()
		if l == "" || l[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(l, "\t")
		if !ok || utf8.RuneCountInString(k) != 1 {
			continue
		}
		self := false
		for _, t := range strings.Fields(v) {
			self = self || t == k
		}
		if !self {
			r, _ := utf8.DecodeRuneInString(k)
			out[r] = true
		}
	}
	return out, sc.Err()
}
