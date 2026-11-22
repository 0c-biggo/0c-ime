package decode_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"golang.org/x/sys/cpu"
)

// 一般音節格（每個位置一個音節）的解碼結果，跟加入按鍵圖（注音九宮格自動切字）之前逐一相同。
//
// testdata/plain-golden.json 是 0e2899e6（加入按鍵圖之前）的 decode 對同一批輸入算出的結果：亂數產生的詞庫、
// 語言模型與學過幾次的個人資料（前綴保護、讀音習慣、個人片語都會用到），以及打聲調、沒打聲調、簡拼、帶成本的近音、
// 鎖定混在一起的音節格；每一筆是 Best（含尾端前綴與否）、NBest、CandidatesInContext、Candidates、Fits、Reading、
// Complete、CompleteHomophones、CompleteAcross、ExplainBest、AuditTarget 結果的 JSON 的雜湊。
//
// 重新產生（只在要換基準時；必須用加入按鍵圖之前的程式碼跑）：
//
//	ZERO_C_WRITE_GOLDEN=$PWD/core/decode/testdata/plain-golden.json go test ./core/decode -run TestPlainLatticeGolden
//
// 雜湊只在有 FMA 的 amd64 比對（#325）。分數是浮點數，最後一兩位隨機器而不同，雜湊整筆都會變：math.Log、math.Exp
// 在 amd64 與 arm64（手機、Apple Silicon）各有不同的實作，amd64 的 math.Exp 在沒有 FMA 的 CPU（Rosetta、2013 年以前
// 的 CPU）走另一條路，arm64 的編譯器還會把 x*y+z 融合成一個 FMA。其他機器改跟 amd64 算出的內容逐項對照：
// 文字、順序、位置與整數必須相同，浮點數相對誤差在 plainFloatTolerance 以內（make test-arm64）：
//
//	ZERO_C_GOLDEN_DUMP=<檔案> GOARCH=amd64 go test ./core/decode -run TestPlainLatticeGolden    # 寫出內容
//	ZERO_C_GOLDEN_COMPARE=<檔案> GOARCH=arm64 go test -exec qemu-aarch64-static ./core/decode   # 對照
func TestPlainLatticeGolden(t *testing.T) {
	cases := plainCases()
	if path := os.Getenv("ZERO_C_GOLDEN_DUMP"); path != "" {
		data, _ := json.Marshal(cases)
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if path := os.Getenv("ZERO_C_GOLDEN_COMPARE"); path != "" {
		comparePlainCases(t, path, cases)
		return
	}
	if runtime.GOARCH != "amd64" || !cpu.X86.HasAVX || !cpu.X86.HasFMA { // 同 math.Exp 選 FMA 的條件
		t.Skipf("the golden hashes hold on amd64 with FMA only; here compare with an amd64 dump (ZERO_C_GOLDEN_COMPARE, make test-arm64)")
	}
	got := plainHashes(cases)
	if path := os.Getenv("ZERO_C_WRITE_GOLDEN"); path != "" {
		data, _ := json.MarshalIndent(got, "", " ")
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	data, err := os.ReadFile("testdata/plain-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(got) {
		t.Fatalf("%d cases, golden has %d", len(got), len(want))
	}
	bad := 0
	for i := range got {
		if got[i] != want[i] {
			bad++
			if bad <= 5 {
				t.Errorf("case %d differs", i)
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d of %d cases differ", bad, len(got))
	}
}

// plainFloatTolerance 是跨架構對照容許的浮點數相對誤差。架構之間實際只差最後一兩位（相對 5e-16 以內）；
// 會改變結果的差異（排序、候選、路徑、整數）不靠這個容差，一定會被逐項比出來。
const plainFloatTolerance = 1e-9

var plainNumber = regexp.MustCompile(`[-+]?Inf|NaN|-?[0-9]+(\.[0-9]+)?(e[-+]?[0-9]+)?`)

// comparePlainCases 把這個架構算出的 cases 跟另一個架構寫出的內容（ZERO_C_GOLDEN_DUMP）逐項對照：
// 數字以外的部分必須完全相同，數字逐一比，浮點數容許 plainFloatTolerance 的相對誤差。
func comparePlainCases(t *testing.T, path string, cases []string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(cases) {
		t.Fatalf("%d cases, %s has %d", len(cases), path, len(want))
	}
	bad, inexact := 0, 0
	for i, got := range cases {
		if got == want[i] {
			continue
		}
		inexact++
		if why := plainDiff(want[i], got); why != "" {
			bad++
			if bad <= 5 {
				t.Errorf("case %d: %s", i, why)
			}
		}
	}
	t.Logf("%s: %d of %d cases differ only in the last digits of a float", runtime.GOARCH, inexact-bad, len(cases))
	if bad > 0 {
		t.Fatalf("%d of %d cases differ from %s beyond float rounding", bad, len(cases), path)
	}
}

// plainDiff 說明 want 跟 got 除了浮點數的捨入以外哪裡不同；只差捨入時回傳空字串。
func plainDiff(want, got string) string {
	if a, b := plainNumber.ReplaceAllString(want, "#"), plainNumber.ReplaceAllString(got, "#"); a != b {
		at := 0
		for at < min(len(a), len(b)) && a[at] == b[at] {
			at++
		}
		from := max(at-80, 0)
		return fmt.Sprintf("differs beyond numbers:\n  want …%s\n  got  …%s", clip(a[from:], 240), clip(b[from:], 240))
	}
	wn, gn := plainNumber.FindAllString(want, -1), plainNumber.FindAllString(got, -1)
	for k := range wn {
		if wn[k] == gn[k] {
			continue
		}
		w, err1 := strconv.ParseFloat(wn[k], 64)
		g, err2 := strconv.ParseFloat(gn[k], 64)
		if err1 != nil || err2 != nil || math.Abs(w-g) > plainFloatTolerance*max(math.Abs(w), math.Abs(g)) {
			return fmt.Sprintf("number %d is %s, want %s", k, gn[k], wn[k])
		}
	}
	return ""
}

// 跨架構對照只放過浮點數最後幾位的差異：換了順序、換了詞、整數或分數真的不同都要報出來。
func TestPlainDiffAllowsOnlyRounding(t *testing.T) {
	want := `[best=[{Word:甲乙 Start:0 End:2 } ] score=-43.83810461376882 conf=1 ]`
	for got, ok := range map[string]bool{
		want: true,
		`[best=[{Word:甲乙 Start:0 End:2 } ] score=-43.83810461376881 conf=1.0000000000000002 ]`:        true,
		`[best=[{Word:乙甲 Start:0 End:2 } ] score=-43.83810461376882 conf=1 ]`:                         false,
		`[best=[{Word:甲乙 Start:0 End:1 } ] score=-43.83810461376882 conf=1 ]`:                         false,
		`[best=[{Word:甲乙 Start:0 End:2 } ] score=-43.8381 conf=1 ]`:                                   false,
		`[best=[{Word:甲乙 Start:0 End:2 } {Word:丙 Start:2 End:3 } ] score=-43.83810461376882 conf=1 ]`: false,
	} {
		if why := plainDiff(want, got); (why == "") != ok {
			t.Errorf("plainDiff(%q) = %q, want ok %v", got, why, ok)
		}
	}
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

type goldenWorld struct {
	model   *personal.Model
	words   []lexicon.Entry
	singles map[zhuyin.Syllable][]lexicon.Entry
	syls    []zhuyin.Syllable // 用到的帶聲調音節
}

// goldenModel 是亂數詞庫：80 個音節、每個音節 1–4 個單字、1,200 個 2–4 字詞（含疊字與少數自動修正詞條），
// unigram 與部分 bigram，再學幾次提交，讓個人資料（學到的計數、讀音習慣、片語）參與解碼。
func goldenModel() *goldenWorld {
	rng := rand.New(rand.NewSource(51))
	bases := zhuyin.Inventory()
	slices.Sort(bases)
	var syls []zhuyin.Syllable
	for _, b := range bases {
		if len(syls) == 80 {
			break
		}
		if rng.Intn(5) == 0 {
			syls = append(syls, b.WithTone(1+rng.Intn(5)))
		}
	}
	lex, sys := lexicon.NewMem(), lm.NewMem()
	w := &goldenWorld{syls: syls, singles: map[zhuyin.Syllable][]lexicon.Entry{}}
	char := 0x4E00
	add := func(surface string, reading []zhuyin.Syllable, correction bool) lexicon.Entry {
		e := lexicon.Entry{Surface: surface, Reading: reading, PRW: []float64{1, 1, 0.5, 0.2}[rng.Intn(4)], Correction: correction}
		lex.Add(e)
		sys.SetUni(surface, math.Exp(-4-8*rng.Float64()))
		w.words = append(w.words, e)
		return e
	}
	var singles []lexicon.Entry
	for _, y := range syls {
		for range 1 + rng.Intn(4) {
			e := add(string(rune(char)), []zhuyin.Syllable{y}, false)
			singles = append(singles, e)
			w.singles[y] = append(w.singles[y], e)
			char++
		}
	}
	for range 1200 {
		n := 2 + rng.Intn(3)
		var surface strings.Builder
		var reading []zhuyin.Syllable
		repeat := n == 2 && rng.Intn(8) == 0
		first := singles[rng.Intn(len(singles))]
		for k := range n {
			s := singles[rng.Intn(len(singles))]
			if k == 0 || repeat {
				s = first
			}
			surface.WriteString(s.Surface)
			reading = append(reading, s.Reading...)
		}
		add(surface.String(), reading, rng.Intn(40) == 0)
	}
	for range 3000 {
		a, b := w.words[rng.Intn(len(w.words))], w.words[rng.Intn(len(w.words))]
		sys.SetBi(a.Surface, b.Surface, math.Exp(-1-5*rng.Float64()))
	}
	w.model = personal.NewModel(lex, sys)
	for range 40 {
		var c personal.Commit
		for range 1 + rng.Intn(3) {
			e := w.words[rng.Intn(len(w.words))]
			start := len(c.Syls)
			c.Syls = append(c.Syls, e.Reading...)
			c.Path = append(c.Path, personal.Seg{Word: e.Surface, Start: start, End: len(c.Syls), Manual: rng.Intn(2) == 0})
		}
		w.model.Learn(c)
	}
	return w
}

// goldenLattice 是一個亂數音節格：每個位置是詞庫裡某個詞的讀音（讓句子解得出來），再隨機改成沒打聲調、簡拼、
// 加上帶成本的近音。
func (w *goldenWorld) goldenLattice(rng *rand.Rand) (input.Lattice, []zhuyin.Syllable) {
	var syls []zhuyin.Syllable
	for len(syls) < 1+rng.Intn(9) {
		syls = append(syls, w.words[rng.Intn(len(w.words))].Reading...)
	}
	syls = syls[:min(len(syls), 10)]
	lat := make(input.Lattice, len(syls))
	for i, y := range syls {
		switch rng.Intn(6) {
		case 0: // 沒打聲調
			for _, v := range zhuyin.Untone(y.Base()).Tones() {
				lat[i] = append(lat[i], input.Hyp{Syl: v, Untoned: true})
			}
		case 1: // 簡拼：同聲母的音節
			var row []input.Hyp
			first := []rune(string(y.Base()))[0]
			for _, v := range w.syls {
				if []rune(string(v.Base()))[0] == first {
					cost := 0.0
					if v != y {
						cost = input.AbbreviationCost
					}
					row = append(row, input.Hyp{Syl: v, Untoned: true, Abbreviated: true, Cost: cost})
				}
			}
			lat[i] = row
		case 2: // 近音
			lat[i] = []input.Hyp{{Syl: y}, {Syl: w.syls[rng.Intn(len(w.syls))], Cost: 1.2}}
		default:
			lat[i] = []input.Hyp{{Syl: y}}
		}
	}
	return lat, syls
}

// plainHashes 是每一筆內容的雜湊（testdata/plain-golden.json 存的就是這些）。
func plainHashes(cases []string) []string {
	out := make([]string, len(cases))
	for i, c := range cases {
		sum := sha256.Sum256([]byte(c))
		out[i] = hex.EncodeToString(sum[:8])
	}
	return out
}

// plainCases 是 300 個亂數音節格各自的解碼結果（canon 寫出的內容）。
func plainCases() []string {
	w := goldenModel()
	rng := rand.New(rand.NewSource(7))
	var out []string
	for c := range 300 {
		lat, syls := w.goldenLattice(rng)
		d := &decode.Decoder{Lex: w.model.Source(), Score: w.model, Beam: 8, Continue: 0.5, SeparateContinuation: true}
		if c%3 == 1 {
			d.Left = w.words[rng.Intn(len(w.words))].Surface
		}
		locks := map[int]decode.Lock{}
		res := map[string]any{}
		path, score := d.Best(lat, locks, true)
		res["best"], res["score"] = path, score
		if len(path) > 1 && rng.Intn(2) == 0 {
			g := path[rng.Intn(len(path))]
			if !g.Partial {
				locks[g.Start] = decode.Lock{Word: g.Word, Len: g.End - g.Start}
			}
		}
		if rng.Intn(4) == 0 && len(syls) > 1 {
			at := rng.Intn(len(syls))
			for _, e := range w.singles[syls[at]] {
				locks[at] = decode.Lock{Word: e.Surface, Len: 1}
			}
		}
		res["locks"] = locks
		p2, s2 := d.Best(lat, locks, true)
		p3, s3 := d.Best(lat, locks, false)
		res["locked"], res["lockedScore"], res["full"], res["fullScore"] = p2, s2, p3, s3
		nb, stats := d.NBest(lat, locks, true, 5)
		res["nbest"], res["stats"] = nb, stats
		start := rng.Intn(len(lat))
		length := 1 + rng.Intn(len(lat)-start)
		res["cand"] = d.CandidatesInContext(lat, start, length, locks, nil)
		res["candLeft"] = d.Candidates(lat, start, length, "", nil)
		for _, g := range p2 {
			if !g.Partial {
				r, ok := decode.Reading(w.model.Source(), lat, g.Start, g.End, g.Word)
				f, corr := d.Fits(lat, g.Start, g.Word)
				res[fmt.Sprintf("seg%d", g.Start)] = []any{r, ok, f, corr}
			}
		}
		if len(p2) > 0 {
			comp, ok := d.Complete(lat, p2)
			res["complete"] = []any{comp, ok}
			homo, ok := d.CompleteHomophones(lat, p2[len(p2)-1].Start, d.InitialLeft())
			res["homophones"] = []any{homo, ok}
			if len(p2) > 1 {
				across, ratio, ok := d.CompleteAcross(lat, p2, 0)
				res["across"] = []any{across, ratio, ok}
			}
			text := ""
			for _, g := range p2 {
				text += g.Word
			}
			res["audit"] = d.AuditTarget(lat, locks, true, text)
		}
		res["explain"] = d.ExplainBest(lat, locks, true)
		var b strings.Builder
		canon(&b, reflect.ValueOf(res))
		out = append(out, b.String())
	}
	return out
}

// canon 寫出 v 的內容：結構的匯出欄位（nil 的切片、指標與 map 省略，同 omitempty）、依鍵排序的 map、浮點數用最短的
// 精確寫法（含 ±Inf、NaN）。雜湊只看內容，不看版本之間新增、值為 nil 的欄位。
func canon(b *strings.Builder, v reflect.Value) {
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			b.WriteString("nil")
			return
		}
		canon(b, v.Elem())
	case reflect.Struct:
		b.WriteByte('{')
		for i := range v.NumField() {
			f := v.Type().Field(i)
			fv := v.Field(i)
			if !f.IsExported() {
				continue
			}
			if k := fv.Kind(); (k == reflect.Slice || k == reflect.Pointer || k == reflect.Map) && fv.IsNil() {
				continue
			}
			b.WriteString(f.Name + ":")
			canon(b, fv)
			b.WriteByte(' ')
		}
		b.WriteByte('}')
	case reflect.Map:
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j]) })
		b.WriteByte('[')
		for _, k := range keys {
			canon(b, k)
			b.WriteByte('=')
			canon(b, v.MapIndex(k))
			b.WriteByte(' ')
		}
		b.WriteByte(']')
	case reflect.Slice, reflect.Array:
		b.WriteByte('[')
		for i := range v.Len() {
			canon(b, v.Index(i))
			b.WriteByte(' ')
		}
		b.WriteByte(']')
	case reflect.Float32, reflect.Float64:
		b.WriteString(strconv.FormatFloat(v.Float(), 'g', -1, 64))
	default:
		fmt.Fprint(b, v.Interface())
	}
}
