package models

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// 配對的雜湊要逐位元組等於 Python 的 json.dumps(obj, ensure_ascii=False, indent=2) + "\n"
// （scripts/pair_models.py 的 json_bytes）。Go 的 encoding/json 有三處不同：map 的鍵會排序、浮點數寫法不同
// （2.0 寫成 2）、預設轉義 <>&。這裡保留原本的鍵順序、照 Python 的 repr(float) 寫浮點數，只轉義 Python 會轉義的字元。

type pyKind int

const (
	pyNull pyKind = iota
	pyBool
	pyInt
	pyFloat
	pyString
	pyArray
	pyObject
)

type pyMember struct {
	key   string
	value *pyValue
}

// pyValue 是 json.loads 的結果：物件保留插入順序（重複的鍵與 Python dict 相同：位置是第一次出現、值是最後一個）。
type pyValue struct {
	kind  pyKind
	b     bool
	num   string  // pyInt：十進位整數字面值
	f     float64 // pyFloat
	s     string
	array []*pyValue
	obj   []pyMember
}

func (v *pyValue) get(key string) *pyValue {
	if v == nil || v.kind != pyObject {
		return nil
	}
	for _, m := range v.obj {
		if m.key == key {
			return m.value
		}
	}
	return nil
}

// set 等於 Python 的 d[key] = value：已有的鍵原地換值，沒有的加在最後。
func (v *pyValue) set(key string, value *pyValue) {
	for i, m := range v.obj {
		if m.key == key {
			v.obj[i].value = value
			return
		}
	}
	v.obj = append(v.obj, pyMember{key, value})
}

func pyStr(s string) *pyValue { return &pyValue{kind: pyString, s: s} }

// parsePyJSON 照 Python json.loads 的語意解析（NaN、Infinity 這類非標準字面值不接受）。
func parsePyJSON(b []byte) (*pyValue, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	v, err := parsePyValue(d)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("JSON 後面有多餘的內容")
	}
	return v, nil
}

func parsePyValue(d *json.Decoder) (*pyValue, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t := t.(type) {
	case nil:
		return &pyValue{kind: pyNull}, nil
	case bool:
		return &pyValue{kind: pyBool, b: t}, nil
	case string:
		return pyStr(t), nil
	case json.Number:
		return pyNumber(string(t))
	case json.Delim:
		switch t {
		case '[':
			v := &pyValue{kind: pyArray}
			for d.More() {
				item, err := parsePyValue(d)
				if err != nil {
					return nil, err
				}
				v.array = append(v.array, item)
			}
			_, err := d.Token()
			return v, err
		case '{':
			v := &pyValue{kind: pyObject}
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return nil, err
				}
				key, ok := k.(string)
				if !ok {
					return nil, errors.New("JSON 物件的鍵不是字串")
				}
				item, err := parsePyValue(d)
				if err != nil {
					return nil, err
				}
				v.set(key, item)
			}
			_, err := d.Token()
			return v, err
		}
	}
	return nil, fmt.Errorf("無法解析的 JSON：%v", t)
}

func pyNumber(lit string) (*pyValue, error) {
	if !strings.ContainsAny(lit, ".eE") {
		// Python 的 int 沒有上限；字面值原樣保留，只有 -0 會變成 0。
		if lit == "-0" {
			lit = "0"
		}
		return &pyValue{kind: pyInt, num: lit}, nil
	}
	f, err := strconv.ParseFloat(lit, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) { // 超出範圍時 Python 也得到 inf 或 0.0
		return nil, err
	}
	return &pyValue{kind: pyFloat, f: f}, nil
}

// dumpPyJSON 等於 (json.dumps(v, ensure_ascii=False, indent=2) + "\n").encode("utf-8")。
func dumpPyJSON(v *pyValue) []byte {
	var b bytes.Buffer
	writePyValue(&b, v, 0)
	b.WriteByte('\n')
	return b.Bytes()
}

func writePyValue(b *bytes.Buffer, v *pyValue, depth int) {
	switch v.kind {
	case pyNull:
		b.WriteString("null")
	case pyBool:
		if v.b {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case pyInt:
		b.WriteString(v.num)
	case pyFloat:
		b.WriteString(pyFloatRepr(v.f))
	case pyString:
		writePyString(b, v.s)
	case pyArray:
		if len(v.array) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('[')
		for i, item := range v.array {
			if i > 0 {
				b.WriteByte(',')
			}
			newline(b, depth+1)
			writePyValue(b, item, depth+1)
		}
		newline(b, depth)
		b.WriteByte(']')
	case pyObject:
		if len(v.obj) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteByte('{')
		for i, m := range v.obj {
			if i > 0 {
				b.WriteByte(',')
			}
			newline(b, depth+1)
			writePyString(b, m.key)
			b.WriteString(": ")
			writePyValue(b, m.value, depth+1)
		}
		newline(b, depth)
		b.WriteByte('}')
	}
}

func newline(b *bytes.Buffer, depth int) {
	b.WriteByte('\n')
	for range depth {
		b.WriteString("  ")
	}
}

// writePyString 照 json.encoder.py_encode_basestring：只轉義 " \ 與 U+0000–U+001F，其餘（含中文、<>&、U+2028）原樣輸出。
func writePyString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// pyFloatRepr 等於 Python 的 repr(float)（float_repr_style 'short'）：最短可還原的位數；小數點位置
// decpt 在 (-4, 16] 時用定點寫法並補 .0，否則用指數寫法、指數至少兩位（1e-05、1e+16）。
// json.dumps 把 inf、nan 寫成 Infinity、NaN。
func pyFloatRepr(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	sign := ""
	if math.Signbit(f) {
		sign, f = "-", -f
	}
	e := strconv.FormatFloat(f, 'e', -1, 64) // d.ddde±XX，位數與 Python 的 dtoa mode 0 相同
	mant, exp, _ := strings.Cut(e, "e")
	digits := strings.Replace(mant, ".", "", 1)
	x, _ := strconv.Atoi(exp)
	decpt := x + 1
	if decpt <= -4 || decpt > 16 {
		out := digits[:1]
		if len(digits) > 1 {
			out += "." + digits[1:]
		}
		es := "+"
		if x < 0 {
			es, x = "-", -x
		}
		return fmt.Sprintf("%s%se%s%02d", sign, out, es, x)
	}
	switch {
	case decpt <= 0:
		return sign + "0." + strings.Repeat("0", -decpt) + digits
	case decpt >= len(digits):
		return sign + digits + strings.Repeat("0", decpt-len(digits)) + ".0"
	}
	return sign + digits[:decpt] + "." + digits[decpt:]
}
