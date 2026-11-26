package layout

import (
	"strconv"
	"strings"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// Shuangpin 是雙拼鍵盤：每個音節固定兩鍵，第一鍵是聲母、第二鍵是韻母，轉成拼音後交給漢語拼音鍵盤的同一條路
// （core/session/pinyin.go）：詞庫、語言模型、神經選字與學習都跟注音一樣。
//
// 跟全拼一樣這個排列沒有注音鍵：Symbol、Press 都回 false，字母由 session 收成一串，兩鍵一組換成拼音。
// 雙拼沒有切法的歧義（一定兩鍵一個音節），所以不需要隔音符號；打了 ' 會略過。
// 一個韻母鍵代表好幾個韻母（小鶴的 k 是 ing 與 uai）時，依聲母挑出拼得成音節的那個；
// 兩個都拼得成時（l＋o 是 luo 也是 lo）取 finals 裡先列的。
type Shuangpin struct {
	name     string
	initials map[byte]string   // 第一鍵 → 聲母（不在表內的字母照字面，例如 b → b、y → y）
	finals   map[byte][]string // 第二鍵 → 韻母（拼音鍵盤拼法，ü 打 v），先列的優先
	zero     map[byte]bool     // 零聲母音節的第一鍵（a o e）
	pairs    map[string]Syllables
	keys     map[zhuyin.Syllable]string
}

// Syllables 是同一組兩鍵可能代表的拼音（鍵盤拼法），第一個是預設。
type Syllables []string

// Xiaohe 是小鶴雙拼。
//
//	聲母：zh＝v、ch＝i、sh＝u，其餘同字母；y、w 當聲母（yj 是 yan、wh 是 wang）。
//	韻母：iu＝q ei＝w e＝e uan＝r ue/üe＝t un＝y u＝u i＝i o/uo＝o ie＝p a＝a ong/iong＝s ai＝d en＝f
//	     eng＝g ang＝h an＝j ing/uai＝k iang/uang＝l ou＝z ia/ua＝x ao＝c ui/ü＝v in＝b iao＝n ian＝m
//	零聲母：單韻母打兩次（aa＝a、oo＝o、ee＝e），雙字母韻母照打（ai an ao ei en er ou），
//	     三字母韻母打首字母加韻母鍵（ah＝ang、eg＝eng）；韻母鍵的寫法也收（aj＝an、ac＝ao）。
var Xiaohe = newShuangpin(NameXiaohe,
	map[byte]string{'v': "zh", 'i': "ch", 'u': "sh"},
	map[byte][]string{
		'q': {"iu"}, 'w': {"ei"}, 'e': {"e"}, 'r': {"uan"}, 't': {"ue", "ve"}, 'y': {"un"},
		'u': {"u"}, 'i': {"i"}, 'o': {"uo", "o"}, 'p': {"ie"}, 'a': {"a"}, 's': {"ong", "iong"},
		'd': {"ai"}, 'f': {"en"}, 'g': {"eng"}, 'h': {"ang"}, 'j': {"an"}, 'k': {"ing", "uai"},
		'l': {"iang", "uang"}, 'z': {"ou"}, 'x': {"ia", "ua"}, 'c': {"ao"}, 'v': {"ui", "v"},
		'b': {"in"}, 'n': {"iao"}, 'm': {"ian"},
	})

func newShuangpin(name string, initials map[byte]string, finals map[byte][]string) *Shuangpin {
	sp := &Shuangpin{name: name, initials: initials, finals: finals,
		zero:  map[byte]bool{'a': true, 'o': true, 'e': true},
		pairs: map[string]Syllables{}, keys: map[zhuyin.Syllable]string{}}
	for a := byte('a'); a <= 'z'; a++ {
		for b := byte('a'); b <= 'z'; b++ {
			pair := string([]byte{a, b})
			var out Syllables
			seen := map[zhuyin.Syllable]bool{}
			for _, p := range sp.spellings(a, b) {
				y, ok := zhuyin.PinyinSyllable(p)
				if !ok || seen[y] {
					continue
				}
				seen[y] = true
				out = append(out, zhuyin.PinyinKeys(y)) // 標準拼法（nue → nve）
				if _, ok := sp.keys[y]; !ok || preferredPair(pair, sp.keys[y]) {
					sp.keys[y] = pair
				}
			}
			if len(out) > 0 {
				sp.pairs[pair] = out
			}
		}
	}
	return sp
}

// spellings 列出兩鍵 a、b 可能的拼音拼法（還沒檢查合不合法），先列的優先。
func (sp *Shuangpin) spellings(a, b byte) []string {
	var out []string
	if sp.zero[a] {
		if a == b {
			out = append(out, string(a)) // aa、oo、ee
		}
		out = append(out, string([]byte{a, b})) // ai an ao ei en er ou
		for _, f := range sp.finals[b] {
			if f[0] == a {
				out = append(out, f) // ah＝ang、eg＝eng
			}
		}
		return out
	}
	ini, ok := sp.initials[a]
	if !ok {
		ini = string(a)
	}
	for _, f := range sp.finals[b] {
		out = append(out, ini+f)
	}
	return out
}

// preferredPair：同一個音節有好幾種打法時，Keys 回傳的那一個。零聲母優先標準打法（an 而不是 aj、ang 用 ah）。
func preferredPair(cand, cur string) bool {
	std := func(p string) int {
		switch {
		case p[0] == p[1]:
			return 0 // aa oo ee
		case strings.Contains("ai an ao ei en er ou ah eg", p):
			return 1
		}
		return 2
	}
	return std(cand) < std(cur)
}

func (sp *Shuangpin) Name() string { return sp.name }

func (sp *Shuangpin) Symbol(rune) (rune, bool) { return 0, false }

func (sp *Shuangpin) Press(c zhuyin.Composer, _ rune) (zhuyin.Composer, rune, bool) {
	return c, 0, false
}

func (sp *Shuangpin) Backspace(c zhuyin.Composer) zhuyin.Composer {
	c = c.Clone()
	c.Backspace()
	return c
}

func (sp *Shuangpin) Readings(zhuyin.Syllable) []zhuyin.Syllable { return nil }

// Keys 回傳打出 s 的兩鍵與聲調數字（ㄋㄧˇ 是 ni3、ㄏㄠˇ 是 hc3）；沒打聲調的音節只有兩鍵。音節表外回傳空字串。
func (sp *Shuangpin) Keys(s zhuyin.Syllable) string {
	k := sp.keys[s.Base()]
	if k == "" || s.Untoned() {
		return k
	}
	return k + strconv.Itoa(s.ToneOf())
}

func (sp *Shuangpin) Punct(key rune) (string, bool) {
	p, ok := hsuPunct[key]
	return p, ok
}

// Pair 回傳兩鍵 pair 代表的拼音（鍵盤拼法），第一個是預設；拼不成音節時 ok 為 false。
func (sp *Shuangpin) Pair(pair string) (Syllables, bool) {
	s, ok := sp.pairs[pair]
	return s, ok
}

// ToPinyin 把一串雙拼按鍵換成以隔音符號 ' 分開的拼音（nihc → ni'hao），供漢語拼音鍵盤的切音節使用；
// ' 會略過。按鍵是奇數個、或有一組兩鍵拼不成音節時 ok 為 false。
func (sp *Shuangpin) ToPinyin(keys string) (string, bool) {
	keys = strings.ReplaceAll(keys, string(zhuyin.PinyinSeparator), "")
	if keys == "" || len(keys)%2 != 0 {
		return "", false
	}
	parts := make([]string, 0, len(keys)/2)
	for i := 0; i < len(keys); i += 2 {
		s, ok := sp.pairs[keys[i:i+2]]
		if !ok {
			return "", false
		}
		parts = append(parts, s[0])
	}
	return strings.Join(parts, string(zhuyin.PinyinSeparator)), true
}

// Preview 是組字區顯示的寫法：打完的每兩鍵換成拼音、以空白分開，最後落單的一鍵與拼不成音節的兩鍵照字面
// （nihc → ni hao、nihcm → ni hao m、nihcu → ni hao sh）。
func (sp *Shuangpin) Preview(keys string) string {
	keys = strings.ReplaceAll(keys, string(zhuyin.PinyinSeparator), "")
	var b strings.Builder
	for i := 0; i < len(keys); i += 2 {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		if i+2 > len(keys) {
			if ini, ok := sp.initials[keys[i]]; ok {
				b.WriteString(ini) // 落單的聲母鍵顯示它代表的聲母（u 是 sh）
			} else {
				b.WriteString(keys[i:])
			}
			break
		}
		if s, ok := sp.pairs[keys[i:i+2]]; ok {
			b.WriteString(s[0])
		} else {
			b.WriteString(keys[i : i+2])
		}
	}
	return b.String()
}

// AsShuangpin 回傳 l 的雙拼排列；l 不是雙拼時 ok 為 false。
func AsShuangpin(l Layout) (*Shuangpin, bool) {
	sp, ok := l.(*Shuangpin)
	return sp, ok
}

// PinyinFamily 回報 l 是不是用字母打拼音的排列（漢語拼音或雙拼）：字母都是拼音鍵，不提供中英混打。
func PinyinFamily(l Layout) bool {
	_, sp := AsShuangpin(l)
	return l == Pinyin || sp
}
