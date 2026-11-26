package english

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
)

const MaxPackWords = 200000
const MaxPackBytes = 16 << 20

var magic = []byte("0cEN\x00\x01")

// magicRanked 是詞頻字典（格式 2，APP#21）：詞可含撇號，每詞多一個旗標位元組（不建議、台灣常用、
// 有常見寫法、只來自語料），有常見寫法時接著同長度的寫法。
var magicRanked = []byte("0cEN\x00\x02")

const (
	flagNoSuggest = 1 << iota
	flagProtected
	flagDisplay
	flagCorpusOnly // #358：只來自語料統計，不在 ESDB 與 technical.tsv
)

// Encode contains words and priors, not a platform-dependent in-memory index.
// The host verifies the pack hash; Decode rebuilds the immutable lookup index.
func (d *Dictionary) Encode() []byte {
	if d.Ranked() {
		return d.encodeRanked()
	}
	var out bytes.Buffer
	out.Write(magic)
	binary.Write(&out, binary.LittleEndian, uint32(d.Len()))
	if d == nil {
		return out.Bytes()
	}
	for i, w := range d.words {
		out.WriteByte(byte(len(w.Text)))
		out.WriteString(w.Text)
		binary.Write(&out, binary.LittleEndian, w.Weight)
		flag := byte(0)
		if d.noSuggest[i] {
			flag = 1
		}
		out.WriteByte(flag)
	}
	return out.Bytes()
}

func Decode(data []byte) (*Dictionary, error) {
	if bytes.HasPrefix(data, magicRanked) {
		return decodeRanked(data)
	}
	if len(data) > MaxPackBytes || len(data) < len(magic)+4 || !bytes.HasPrefix(data, magic) {
		return nil, fmt.Errorf("英文資料包標頭或大小不合法")
	}
	count := binary.LittleEndian.Uint32(data[len(magic):])
	if count > MaxPackWords || uint64(count)*11 > uint64(len(data)-len(magic)-4) {
		return nil, fmt.Errorf("英文資料包詞數不合法")
	}
	data = data[len(magic)+4:]
	words := make([]Word, 0, count)
	noSuggest := make([]bool, 0, count)
	previous := ""
	for i := uint32(0); i < count; i++ {
		if len(data) == 0 {
			return nil, fmt.Errorf("英文資料包截斷")
		}
		n := int(data[0])
		data = data[1:]
		if n < 1 || n > MaxWordBytes || len(data) < n+9 {
			return nil, fmt.Errorf("英文詞條截斷或過長")
		}
		text := string(data[:n])
		data = data[n:]
		weight := math.Float64frombits(binary.LittleEndian.Uint64(data))
		flag := data[8]
		data = data[9:]
		if !letters(text) || text <= previous || weight <= 0 || math.IsNaN(weight) || math.IsInf(weight, 0) || flag > 1 {
			return nil, fmt.Errorf("英文詞條內容或排序不合法")
		}
		previous = text
		words = append(words, Word{text, weight})
		noSuggest = append(noSuggest, flag == 1)
	}
	if len(data) != 0 {
		return nil, fmt.Errorf("英文資料包有多餘內容")
	}
	// Ordering and uniqueness were validated above. Rebuilding a deduplication
	// map and sorting again would only add cold-load allocations.
	d := &Dictionary{words: words, noSuggest: noSuggest}
	d.buildIndex()
	return d, nil
}

func (d *Dictionary) encodeRanked() []byte {
	var out bytes.Buffer
	out.Write(magicRanked)
	binary.Write(&out, binary.LittleEndian, uint32(d.Len()))
	for i, w := range d.words {
		out.WriteByte(byte(len(w.Text)))
		out.WriteString(w.Text)
		binary.Write(&out, binary.LittleEndian, w.Weight)
		flag := byte(0)
		if d.noSuggest[i] {
			flag |= flagNoSuggest
		}
		if d.protected[i] {
			flag |= flagProtected
		}
		if d.corpusOnly[i] {
			flag |= flagCorpusOnly
		}
		common := d.common(i)
		if common != w.Text {
			flag |= flagDisplay
		}
		out.WriteByte(flag)
		if common != w.Text {
			out.WriteString(common)
		}
	}
	return out.Bytes()
}

func decodeRanked(data []byte) (*Dictionary, error) {
	if len(data) > MaxPackBytes || len(data) < len(magicRanked)+4 {
		return nil, fmt.Errorf("英文資料包標頭或大小不合法")
	}
	count := binary.LittleEndian.Uint32(data[len(magicRanked):])
	if count > MaxPackWords || uint64(count)*11 > uint64(len(data)-len(magicRanked)-4) {
		return nil, fmt.Errorf("英文資料包詞數不合法")
	}
	data = data[len(magicRanked)+4:]
	d := newRanked(int(count))
	for i := uint32(0); i < count; i++ {
		if len(data) == 0 {
			return nil, fmt.Errorf("英文資料包截斷")
		}
		n := int(data[0])
		data = data[1:]
		if n < 1 || n > MaxWordBytes || len(data) < n+9 {
			return nil, fmt.Errorf("英文詞條截斷或過長")
		}
		e := Entry{Text: string(data[:n])}
		e.Weight = math.Float64frombits(binary.LittleEndian.Uint64(data[n:]))
		flag := data[n+8]
		data = data[n+9:]
		if flag&^(flagNoSuggest|flagProtected|flagDisplay|flagCorpusOnly) != 0 {
			return nil, fmt.Errorf("英文詞條旗標不合法")
		}
		e.NoSuggest, e.Protected, e.CorpusOnly = flag&flagNoSuggest != 0, flag&flagProtected != 0, flag&flagCorpusOnly != 0
		if flag&flagDisplay != 0 {
			if len(data) < n {
				return nil, fmt.Errorf("英文詞條截斷")
			}
			e.Display = string(data[:n])
			data = data[n:]
		}
		// addRanked 逐條檢查詞形、權重、寫法與順序，不再排序。
		if err := d.addRanked(e); err != nil {
			return nil, err
		}
	}
	if len(data) != 0 {
		return nil, fmt.Errorf("英文資料包有多餘內容")
	}
	return d.finishRanked()
}
