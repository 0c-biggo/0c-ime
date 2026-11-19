package rerank

import (
	"encoding/json"
	"errors"
	"slices"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 選字序列模型套件的 schema。v2 另外宣告 tone_policy，接受逐音節的未知聲調（models zuin-tone-mask-v1）；
// v1（PF512）只收打了聲調的讀音。桌面 internal/neural 與手機外殼都依這裡判斷，不各自比字串。
const (
	SequenceSchemaV1 = "zuin-sequence-runtime-v1"
	SequenceSchemaV2 = "zuin-sequence-runtime-v2"
	ToneMaskSchema   = "zuin-tone-mask-v1"
)

// 手機引擎的融合權重（平台設定，不寫進套件 manifest）。在手機詞庫 744c608c、tone0-only step2048、
// 校準組 1,500 例上選定：沒前文時 0.5 不輸 1（部分聲調 +1.80pp，CI 不含 0），改錯也較少；
// 有前文時 1 較好（全打聲調 +0.87pp，CI 不含 0）。
// 桌面照套件 manifest 的 weight。
const (
	MobileWeight            = 1.0
	MobileContextlessWeight = 0.5
)

// TonePolicy 是套件 manifest 的 tone_policy。UnknownToneID 用指標區分「沒寫」與 0。
type TonePolicy struct {
	Schema        string `json:"schema"`
	UnknownToneID *int   `json:"unknown_tone_id"`
}

// UnknownTonesDeclared 回報套件是否宣告接受未知聲調：schema 是 v2，而且 tone_policy 是
// zuin-tone-mask-v1、unknown_tone_id 為 0。其他組合（含 PF512 的 v1）一律不開，沒打聲調的段落照統計引擎。
func UnknownTonesDeclared(schema string, policy *TonePolicy) bool {
	return schema == SequenceSchemaV2 && policy != nil && policy.Schema == ToneMaskSchema &&
		policy.UnknownToneID != nil && *policy.UnknownToneID == 0
}

// ValidTonePolicy 回報 schema 與 tone_policy 的組合是不是 worker 會接受的：v1 不能帶 tone_policy，
// v2 一定要帶合法的 tone_policy（同 models sequence_runtime.py 的檢查）。其他 schema 不帶 tone_policy。
func ValidTonePolicy(schema string, policy *TonePolicy) bool {
	if schema == SequenceSchemaV2 {
		return UnknownTonesDeclared(schema, policy)
	}
	return policy == nil
}

// ParseBases 讀出套件 vocab.json 的音節基底（bases，不帶聲調），給 Options.Bases。
func ParseBases(vocab []byte) (map[zhuyin.Syllable]bool, error) {
	var v struct {
		Bases []string `json:"bases"`
	}
	if err := json.Unmarshal(vocab, &v); err != nil {
		return nil, err
	}
	if len(v.Bases) == 0 {
		return nil, errors.New("vocabulary has no bases")
	}
	out := make(map[zhuyin.Syllable]bool, len(v.Bases))
	for _, b := range v.Bases {
		y := zhuyin.Syllable(b)
		if !y.Valid() || y.Base() != y {
			return nil, errors.New("vocabulary base is not a toneless syllable")
		}
		out[y] = true
	}
	return out, nil
}

// ParseChars 讀出套件 vocab.json 的字表（characters，每項一個字），給 Options.Chars。
func ParseChars(vocab []byte) (map[rune]bool, error) {
	var v struct {
		Characters []string `json:"characters"`
	}
	if err := json.Unmarshal(vocab, &v); err != nil {
		return nil, err
	}
	if len(v.Characters) == 0 {
		return nil, errors.New("vocabulary has no characters")
	}
	out := make(map[rune]bool, len(v.Characters))
	for _, c := range v.Characters {
		r, n := utf8.DecodeRuneInString(c)
		if n == 0 || n != len(c) { // 字表本身有 U+FFFD，不能用 RuneError 判斷；json 解碼已把無效位元組換成 U+FFFD
			return nil, errors.New("vocabulary character is not a single rune")
		}
		out[r] = true
	}
	return out, nil
}

// Encodable 回報評分器能不能編碼 text 的每個字（Chars 為 nil 時不檢查）。
func (o Options) Encodable(text string) bool {
	if o.Chars == nil {
		return true
	}
	for _, r := range text {
		if !o.Chars[r] {
			return false
		}
	}
	return true
}

// KeepEncodable 拿掉含字表外字元的候選，評分器才不會因為一個候選就整批拒絕（unknown_character）。
// 第 0 名是目前顯示的路徑，它含字表外字元時整段不送（ok 為 false）；剩下的候選不到 2 個時由 Supported 擋下。
// 沒有拿掉任何候選時原樣回傳。
func (o Options) KeepEncodable(rows []decode.Hypothesis) (kept []decode.Hypothesis, ok bool) {
	if o.Chars == nil {
		return rows, true
	}
	if len(rows) == 0 || !o.Encodable(rows[0].Text) {
		return rows, false
	}
	for i, c := range rows {
		if !o.Encodable(c.Text) {
			if kept == nil {
				kept = slices.Clone(rows[:i])
			}
		} else if kept != nil {
			kept = append(kept, c)
		}
	}
	if kept == nil {
		return rows, true
	}
	return kept, true
}

// Readable 回報段落的讀音能不能送給評分器：每個音節的基底都在 Bases 內（Bases 為 nil 時不檢查）；
// 沒打聲調的音節只在 UnknownTones 時可以送。
//
// 簡拼音節（開了簡拼時只打一個聲母或介音）：打的注音本身不是音節（ㄅ ㄉ ㄋ…）時沒有讀音可送，在本機照統計引擎
// （以前會送出去再被 worker 以 unknown_base 退回）。打的注音本身就是完整音節（ㄧ ㄨ ㄩ ㄓ ㄔ ㄕ ㄖ ㄗ ㄘ ㄙ）時，
// 開了簡拼的 session 沒打聲調的「一」「是」也是這樣表示（untonedSyllable），所以照沒打聲調的那個音節送評分。
// 評分器會偏向那個音節的讀法，所以宿主不能讓它改簡拼位置的讀音：session 排除在那裡讀音跟統計首選不同的候選
// （core/session rerank_abbrev.go）。
func (o Options) Readable(syls []zhuyin.Syllable) bool {
	for _, y := range syls {
		if y.Untoned() && !o.UnknownTones || y.Abbreviated() && !y.Base().Legal() {
			return false
		}
		if o.Bases != nil && !o.Bases[y.Base()] {
			return false
		}
	}
	return true
}

// Supports 是 Supported 再加上這個評分器的條件：讀音可讀（Readable 的基底檢查），
// ToneKnown 只送給 UnknownTones 的評分器。宿主送出請求前與評分器收到請求時都用它。
func (o Options) Supports(r Request) bool {
	if !Supported(r) || r.ToneKnown != nil && !o.UnknownTones {
		return false
	}
	for _, c := range r.Candidates {
		if !o.Encodable(c.Text) {
			return false
		}
	}
	if o.Bases != nil {
		for _, y := range r.Reading {
			if !o.Bases[y.Base()] {
				return false
			}
		}
	}
	return true
}

// WeightFor 是前文為 context 的請求用的融合權重：前文是空的而且設了 ContextlessWeight 時用它，否則用 Weight。
func (o Options) WeightFor(context string) float64 {
	if context == "" && o.ContextlessWeight > 0 {
		return o.ContextlessWeight
	}
	return o.Weight
}

// MaskTones 把讀音轉成送給評分器的形式：沒打聲調的音節換成它的基底，ToneKnown 標 false；
// 打的注音本身是完整音節的簡拼音節同樣當成那個音節、聲調未知（見 Readable）。
// 全部打了聲調時原樣回傳、known 為 nil，請求與舊協定相同。只打聲母的簡拼保持原樣（Valid 會拒絕），由 Readable 先擋掉。
func MaskTones(reading []zhuyin.Syllable) (out []zhuyin.Syllable, known []bool) {
	if !zhuyin.HasUntoned(reading) {
		return reading, nil
	}
	out = make([]zhuyin.Syllable, len(reading))
	known = make([]bool, len(reading))
	for i, y := range reading {
		out[i], known[i] = y, true
		if y.Untoned() && (!y.Abbreviated() || y.Base().Legal()) {
			out[i], known[i] = y.Base(), false
		}
	}
	return out, known
}

// ToneKnownKey 是 ToneKnown 在快取鍵裡的部分：沒打聲調的「ㄉㄧ」與一聲的「ㄉㄧ」讀音字串相同，鍵要分開。
func ToneKnownKey(r Request) string {
	if r.ToneKnown == nil {
		return ""
	}
	b := make([]byte, 0, len(r.ToneKnown)+4)
	b = append(b, "|tk="...)
	for _, k := range r.ToneKnown {
		if k {
			b = append(b, '1')
		} else {
			b = append(b, '0')
		}
	}
	return string(b)
}
