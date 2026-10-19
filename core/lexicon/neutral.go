package lexicon

import (
	"sort"

	"git.biggo.com/0c/ime/core/zhuyin"
)

// particles 是語氣詞與嘆詞及其優先順序。它們口語常讀輕聲，詞庫卻往往只收在其他聲調
// （喔 ㄛ、呃 ㄜˋ、欸 ㄟ、唉 ㄞ），使用者打輕聲（例如 i7）時會找不到字（#69）。
// 同一音節有好幾個語氣詞時依這個順序（ㄛ˙ 是喔、ㄞ˙ 是唉）。
var particles = map[string]int{}

// home 是有好幾個讀音的語氣詞只在哪個音節補輕聲：哦也讀 ㄜˊ、欸也讀 ㄞ，
// 但 ㄜ˙ 是呃、ㄞ˙ 是唉；詞庫的讀音權重分不出主次，語境雙字頻率（好哦）又會蓋過順序。
var home = map[string]zhuyin.Syllable{"哦": "ㄛ", "欸": "ㄟ", "啊": "ㄚ"}

func init() {
	for i, r := range []rune("喔呃唉欸啊呀哇嗯哈嘿吼齁喲唷耶啦囉嘍嗎呢吧嘛哪哦噢誒哎嗷呦咦咧嘞吶咩哩哼噯唄呵嗚喂") {
		particles[string(r)] = i
	}
}

// NeutralParticles 在 src 之上補輕聲語氣詞：單一輕聲音節完全查不到詞（也不是任何詞的開頭）時，
// 回傳同一音節其他聲調裡的語氣詞，讀音改標輕聲；讀音權重依優先順序每名降為十分之一，讓排第一的語氣詞勝過單字頻率較高的其他候選。
// 詞庫已有輕聲字的音節（ㄌㄚ˙ 啦、ㄋㄜ˙ 呢）不受影響；只在詞中出現的輕聲（東西的 ㄒㄧ˙）也不補。
func NeutralParticles(src Source) Source { return neutralParticles{src} }

type neutralParticles struct{ Source }

func (n neutralParticles) fallback(syls []zhuyin.Syllable) []Entry {
	if len(syls) != 1 || syls[0].ToneOf() != 5 || n.Source.HasPrefix(syls) {
		return nil
	}
	var out []Entry
	seen := map[string]bool{}
	for tone := 1; tone <= 4; tone++ {
		for _, e := range n.Source.Exact([]zhuyin.Syllable{syls[0].WithTone(tone)}) {
			if _, ok := particles[e.Surface]; ok && !seen[e.Surface] && (home[e.Surface] == "" || home[e.Surface] == syls[0].Base()) {
				seen[e.Surface] = true
				e.Reading = []zhuyin.Syllable{syls[0]}
				out = append(out, e)
			}
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return particles[out[a].Surface] < particles[out[b].Surface] })
	weight := 1.0
	for i := range out {
		out[i].PRW *= weight
		weight /= 10
	}
	return out
}

func (n neutralParticles) Exact(syls []zhuyin.Syllable) []Entry {
	if es := n.fallback(syls); es != nil {
		return es
	}
	return n.Source.Exact(syls)
}

func (n neutralParticles) Prefixed(syls []zhuyin.Syllable) []Entry {
	if es := n.fallback(syls); es != nil {
		return es
	}
	return n.Source.Prefixed(syls)
}

func (n neutralParticles) HasPrefix(syls []zhuyin.Syllable) bool {
	return n.Source.HasPrefix(syls) || len(n.fallback(syls)) > 0
}

// LongestWord 沿用底層詞庫：補的語氣詞都是單一音節。
func (n neutralParticles) LongestWord() int { return Longest(n.Source) }
