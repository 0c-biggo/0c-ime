package zhuyin

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// 標準普通話拼音音節（不含聲調），加上台灣注音另有的少數音節（崖 ㄧㄞ、這 ㄓㄟ 等）。
// 音節表是詞庫編譯器的合法性檢查依據：不在表內的音節一律拒收，所以漏列會以編譯錯誤的形式
// 大聲出現，而不是悄悄放行。
const pinyinInventory = `
a o e ai ei ao ou an en ang eng er
yi ya yo ye yai yao you yan yin yang ying yong
wu wa wo wai wei wan wen wang weng
yu yue yuan yun
ba bo bai bei bao ban ben bang beng bi bie biao bian bin bing bu
pa po pai pei pao pou pan pen pang peng pi pie piao pian pin ping pu
ma mo me mai mei mao mou man men mang meng mi mie miao miu mian min ming mu
fa fo fei fou fan fen fang feng fu
da de dai dei dao dou dan den dang deng dong di dia die diao diu dian ding du duo dui duan dun
ta te tai tei tao tou tan tang teng tong ti tie tiao tian ting tu tuo tui tuan tun
na ne nai nei nao nou nan nen nang neng nong ni nie niao niu nian nin niang ning nu nuo nuan nun nü nüe
la le lo lai lei lao lou lan lang leng long li lia lie liao liu lian lin liang ling lu luo luan lun lü lüe
ga ge gai gei gao gou gan gen gang geng gong gu gua guo guai gui guan gun guang
ka ke kai kei kao kou kan ken kang keng kong ku kua kuo kuai kui kuan kun kuang
ha he hai hei hao hou han hen hang heng hong hu hua huo huai hui huan hun huang
ji jia jie jiao jiu jian jin jiang jing jiong ju jue juan jun
qi qia qie qiao qiu qian qin qiang qing qiong qu que quan qun
xi xia xie xiao xiu xian xin xiang xing xiong xu xue xuan xun
zha zhe zhi zhai zhei zhao zhou zhan zhen zhang zheng zhong zhu zhua zhuo zhuai zhui zhuan zhun zhuang
cha che chi chai chao chou chan chen chang cheng chong chu chua chuo chuai chui chuan chun chuang
sha she shi shai shei shao shou shan shen shang sheng shu shua shuo shuai shui shuan shun shuang
re ri rao rou ran ren rang reng rong ru rua ruo rui ruan run
za ze zi zai zei zao zou zan zen zang zeng zong zu zuo zui zuan zun
ca ce ci cai cao cou can cen cang ceng cong cu cuo cui cuan cun
sa se si sai sao sou san sen sang seng song su suo sui suan sun
ê
lüan sei biang pia fiao
`

var legalBases = func() map[string]bool {
	m := map[string]bool{}
	for _, p := range strings.Fields(pinyinInventory) {
		z, err := pinyinBase(p)
		if err != nil {
			panic(fmt.Sprintf("音節表內的 %q 無法轉換：%v", p, err))
		}
		m[z] = true
	}
	return m
}()

// Inventory 回傳音節表內所有不帶聲調的合法音節，依字串排序；供窮舉式測試與資料稽核使用。
func Inventory() []Syllable {
	out := make([]Syllable, 0, len(legalBases))
	for b := range legalBases {
		out = append(out, Syllable(b))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Legal 檢查音節是否為國語實際存在的音節（音節表內、聲調符號在尾）。
// 與 Valid（只檢查結構）不同，Legal 會拒絕 ㄅㄩ 這類結構合法但不存在的組合。
func (s Syllable) Legal() bool {
	if !s.Valid() {
		return false
	}
	return legalBases[string(s.Base())]
}

// Base 回傳去掉聲調符號的音節；沒打聲調的音節（#150）去掉 UntonedMark。
func (s Syllable) Base() Syllable {
	if s.Untoned() {
		return Syllable(string(s[:len(s)-len(string(UntonedMark))]))
	}
	r := []rune(string(s))
	if n := len(r); n > 0 && ClassOf(r[n-1]) == Tone {
		return Syllable(r[:n-1])
	}
	return s
}

// ToneOf 回傳聲調：1–4，輕聲為 5；沒打聲調的音節（#150）為 0。
func (s Syllable) ToneOf() int {
	if strings.HasSuffix(string(s), string(UntonedMark)) && s.Untoned() {
		return 0
	}
	r := []rune(string(s))
	switch r[len(r)-1] {
	case 'ˊ':
		return 2
	case 'ˇ':
		return 3
	case 'ˋ':
		return 4
	case '˙':
		return 5
	}
	return 1
}

// WithTone 回傳同一個音節換成聲調 t（1–5）。
func (s Syllable) WithTone(t int) Syllable {
	b := string(s.Base())
	switch t {
	case 2:
		return Syllable(b + "ˊ")
	case 3:
		return Syllable(b + "ˇ")
	case 4:
		return Syllable(b + "ˋ")
	case 5:
		return Syllable(b + "˙")
	}
	return Syllable(b)
}

var pyInitials = []struct{ py, zy string }{ // zh ch sh 必須在 z c s 之前比對
	{"zh", "ㄓ"}, {"ch", "ㄔ"}, {"sh", "ㄕ"},
	{"b", "ㄅ"}, {"p", "ㄆ"}, {"m", "ㄇ"}, {"f", "ㄈ"}, {"d", "ㄉ"}, {"t", "ㄊ"}, {"n", "ㄋ"}, {"l", "ㄌ"},
	{"g", "ㄍ"}, {"k", "ㄎ"}, {"h", "ㄏ"}, {"j", "ㄐ"}, {"q", "ㄑ"}, {"x", "ㄒ"},
	{"r", "ㄖ"}, {"z", "ㄗ"}, {"c", "ㄘ"}, {"s", "ㄙ"},
}

var pyFinals = map[string]string{
	"": "", "a": "ㄚ", "o": "ㄛ", "e": "ㄜ", "ê": "ㄝ", "ai": "ㄞ", "ei": "ㄟ", "ao": "ㄠ", "ou": "ㄡ",
	"an": "ㄢ", "en": "ㄣ", "ang": "ㄤ", "eng": "ㄥ", "er": "ㄦ", "ong": "ㄨㄥ",
	"i": "ㄧ", "ia": "ㄧㄚ", "io": "ㄧㄛ", "ie": "ㄧㄝ", "iai": "ㄧㄞ", "iao": "ㄧㄠ", "iou": "ㄧㄡ",
	"ian": "ㄧㄢ", "in": "ㄧㄣ", "iang": "ㄧㄤ", "ing": "ㄧㄥ", "iong": "ㄩㄥ",
	"u": "ㄨ", "ua": "ㄨㄚ", "uo": "ㄨㄛ", "uai": "ㄨㄞ", "uei": "ㄨㄟ", "uan": "ㄨㄢ", "uen": "ㄨㄣ",
	"uang": "ㄨㄤ", "ueng": "ㄨㄥ",
	"ü": "ㄩ", "üe": "ㄩㄝ", "üan": "ㄩㄢ", "ün": "ㄩㄣ",
}

// y、w 開頭的零聲母拼寫還原成介音形式。
var pyZero = map[string]string{
	"yi": "i", "ya": "ia", "yo": "io", "ye": "ie", "yai": "iai", "yao": "iao", "you": "iou", "yan": "ian",
	"yin": "in", "yang": "iang", "ying": "ing", "yong": "iong",
	"yu": "ü", "yue": "üe", "yuan": "üan", "yun": "ün",
	"wu": "u", "wa": "ua", "wo": "uo", "wai": "uai", "wei": "uei", "wan": "uan", "wen": "uen",
	"wang": "uang", "weng": "ueng",
}

// pinyinBase 把不帶聲調的拼音轉成不帶聲調的注音。
func pinyinBase(p string) (string, error) {
	switch p {
	case "zhi", "chi", "shi", "ri", "zi", "ci", "si": // 空韻
		for _, in := range pyInitials {
			if strings.HasPrefix(p, in.py) {
				return in.zy, nil
			}
		}
	case "er", "r": // r：兒化音節（CC-CEDICT 以 r5 表示）
		return "ㄦ", nil
	}
	if f, ok := pyZero[p]; ok {
		return finalOf(f)
	}
	for _, in := range pyInitials {
		if !strings.HasPrefix(p, in.py) {
			continue
		}
		rest := p[len(in.py):]
		if strings.ContainsAny(in.py, "jqx") && strings.HasPrefix(rest, "u") {
			rest = "ü" + rest[1:] // ju → jü
		}
		switch rest { // 縮寫還原
		case "iu":
			rest = "iou"
		case "ui":
			rest = "uei"
		case "un":
			rest = "uen"
		}
		f, err := finalOf(rest)
		if err != nil {
			return "", err
		}
		if f == "" {
			return "", fmt.Errorf("%q 缺少韻母", p)
		}
		return in.zy + f, nil
	}
	return finalOf(p)
}

func finalOf(f string) (string, error) {
	if z, ok := pyFinals[f]; ok {
		return z, nil
	}
	return "", fmt.Errorf("不認得的韻母 %q", f)
}

var toneMarks = map[rune]struct {
	base rune
	tone int
}{
	'ā': {'a', 1}, 'á': {'a', 2}, 'ǎ': {'a', 3}, 'à': {'a', 4},
	'ē': {'e', 1}, 'é': {'e', 2}, 'ě': {'e', 3}, 'è': {'e', 4},
	'ī': {'i', 1}, 'í': {'i', 2}, 'ǐ': {'i', 3}, 'ì': {'i', 4},
	'ō': {'o', 1}, 'ó': {'o', 2}, 'ǒ': {'o', 3}, 'ò': {'o', 4},
	'ū': {'u', 1}, 'ú': {'u', 2}, 'ǔ': {'u', 3}, 'ù': {'u', 4},
	'ǖ': {'ü', 1}, 'ǘ': {'ü', 2}, 'ǚ': {'ü', 3}, 'ǜ': {'ü', 4},
	'ế': {'ê', 2}, 'ề': {'ê', 4},
}

var combiningTones = map[rune]int{'\u0304': 1, '\u0301': 2, '\u030C': 3, '\u0300': 4}

// FromPinyin 把一個拼音音節轉成注音音節。接受數字聲調（qi3、lu:4、r5）與
// 聲調符號（qǐ、lǜ）兩種寫法；沒有聲調時視為輕聲。結果必須是合法音節。
func FromPinyin(p string) (Syllable, error) {
	p = strings.ToLower(strings.TrimSpace(p))
	p = strings.NewReplacer("u:", "ü", "v", "ü", "e^", "ê").Replace(p)
	tone := 5
	if n := len(p); n > 0 && p[n-1] >= '1' && p[n-1] <= '5' {
		tone = int(p[n-1] - '0')
		p = p[:n-1]
	} else {
		var b strings.Builder
		for _, r := range p {
			if m, ok := toneMarks[r]; ok {
				tone = m.tone
				r = m.base
			} else if t, ok := combiningTones[r]; ok { // 分解形式：母音加組合用聲調符號
				tone = t
				continue
			} else if r == '\u0308' { // 組合用分音符：u + ¨ → ü
				s := []rune(b.String())
				if n := len(s); n > 0 && s[n-1] == 'u' {
					b.Reset()
					b.WriteString(string(s[:n-1]))
					r = 'ü'
				}
			}
			b.WriteRune(r)
		}
		p = b.String()
	}
	for _, r := range p {
		if !unicode.IsLower(r) && r != 'ü' && r != 'ê' {
			return "", fmt.Errorf("拼音 %q 含非字母", p)
		}
	}
	base, err := pinyinBase(p)
	if err != nil {
		return "", err
	}
	s := Syllable(base).WithTone(tone)
	if !s.Legal() {
		return "", fmt.Errorf("拼音 %q 轉成 %q，不在音節表內", p, s)
	}
	return s, nil
}
