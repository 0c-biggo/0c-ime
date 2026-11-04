// 神經重排的 golden transcript：隨機操作序列加偽評分器驅動 Session，
// 每個 seed 的輸出以 SHA-256 對 golden；預期的行為改變後以 -update 重寫。

package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"math/rand/v2"
	"os"
	"reflect"
	"testing"

	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// fuzzSyllables 覆蓋：多音節歧義、輕聲/句尾助詞、無聲調（#150）、單音節段（ModeText）。
const fuzzSyllables = "ㄕㄜˊ ㄇㄜ˙ ㄨㄟˋ ㄕˊ ㄏㄡˋ ㄅㄨˋ ㄕˋ ㄧㄡˋ ㄒㄧㄚˋ ㄗㄞˋ ㄉㄜ ㄌㄜ ㄇㄚˇ ㄅㄚ˙ ㄋㄜ˙ ㄓ ㄕˋ ㄔㄨˊ ㄌㄧˋ ㄓㄨㄥ ㄨㄣˊ ㄓㄞ ㄒㄧㄢ ㄓㄜ˙ ㄏㄨㄟˊ ㄎㄜˇ ㄧˊ ㄉㄧㄢˇ ㄉㄨㄥ ㄒㄧ"

// fuzzKeyPool 展平所有音節按鍵；聲調鍵獨立加權，讓無聲調輸入自然出現。
func fuzzKeyPool() (plain, tones, misc []string) {
	seen := map[string]bool{}
	for _, y := range zhuyin.ParseReading(fuzzSyllables) {
		for _, k := range layout.Standard.Keys(y) {
			key := string(k)
			if seen[key] {
				continue
			}
			seen[key] = true
			plain = append(plain, key)
		}
	}
	tones = []string{"1", "2", "3", "4", "5"} // 聲調鍵位（打錯/漏打都由亂數覆蓋）
	misc = []string{"<", ">", "，", "。", " ", "a", "b", "c", "d", "1", "9", "0", "[", "]", "=", "-"}
	return
}

type fuzzScorer struct{ calls int }

func (d *fuzzScorer) Score(r rerank.Request) ([]float64, error) {
	d.calls++
	out := make([]float64, len(r.Candidates))
	for i, c := range r.Candidates {
		h := fnv.New64a()
		h.Write([]byte(r.Context))
		h.Write([]byte(r.Mode))
		for _, y := range r.Reading {
			h.Write([]byte(y))
		}
		h.Write([]byte(c.Text))
		// 決定性、依文字變動的分數：讓 decide/veto/hint/快取分支真的翻轉。
		v := h.Sum64()
		out[i] = float64(int64(v%2009)-1000) / 100.0
	}
	return out, nil
}

const rerankGolden = "../../testdata/transcripts/rerank-fuzz.golden.txt"

var updateGolden = flag.Bool("update", false, "以目前的行為重寫神經重排 golden transcript")

func TestRerankGoldenTranscript(t *testing.T) {
	const seeds = 60
	var out bytes.Buffer
	for seed := range seeds {
		h := sha256.New()
		runRerankSeed(t, seed, h, nil)
		fmt.Fprintf(&out, "%d %x\n", seed, h.Sum(nil))
	}
	if *updateGolden {
		if err := os.WriteFile(rerankGolden, out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(rerankGolden)
	if err != nil {
		t.Fatalf("%v（以 -update 產生）", err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Errorf("與 golden 不符；確認是預期的行為改變後以 -update 重寫")
	}
}

func runRerankSeed(t *testing.T, seed int, w io.Writer, check func(*Session)) {
	t.Helper()
	plain, tones, misc := fuzzKeyPool()
	syls := zhuyin.ParseReading(fuzzSyllables)

	rng := rand.New(rand.NewPCG(uint64(seed)*104729+17, 0x9E3779B97F4A7C15))
	s := newShemeSessionWithKeeps(t)
	sc := &fuzzScorer{}
	s.Rerank = rerank.Options{
		Weight: 2, ContextRunes: rerank.MaxContextRunes,
		HintGap: 100, TextWeight: 2, TextMargin: 1, AllowPartial: true,
		Scorer: sc,
	}
	s.Handle(proto.Request{Type: proto.Context, ContextEnabled: true})
	emit := func(op string, req proto.Request, snap proto.Snapshot) {
		b, err := json.Marshal(snap)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(w, `{"seed":%d,"op":%q,"req":%s,"snap":%s,"gen":%d}`+"\n",
			seed, op, mustJSON(t, req), b, s.model.Gen)
	}
	do := func(op string, req proto.Request) proto.Snapshot {
		snap := s.Handle(req)
		if check != nil {
			check(s)
		}
		emit(op, req, snap)
		return snap
	}
	var lastCands *proto.CandidateList
	for range 70 {
		switch n := rng.IntN(100); {
		case n < 46: // 真實打字主形：整串帶調音節，零星標點切段（rerank 每鍵必經）
			for range 2 + rng.IntN(4) {
				y := syls[rng.IntN(len(syls))]
				for _, k := range layout.Standard.Keys(y) {
					do("read", proto.Request{Type: proto.Key, Key: string(k)})
				}
				if rng.IntN(6) == 0 {
					do("punct", proto.Request{Type: proto.Key, Key: []string{"<", ">"}[rng.IntN(2)]})
				}
			}
		case n < 58: // 單鍵：未完成音節、錯調、misc（comp 非空與 #150 路徑）
			if rng.IntN(6) == 0 {
				do("misc", proto.Request{Type: proto.Key, Key: misc[rng.IntN(len(misc))], Private: rng.IntN(24) == 0})
				continue
			}
			pool := plain
			if rng.IntN(5) == 0 {
				pool = tones
			}
			do("key", proto.Request{Type: proto.Key, Key: pool[rng.IntN(len(pool))], Private: rng.IntN(24) == 0})
		case n < 64: // 整音節（含輕聲句尾助詞：ModeText 路徑）
			y := syls[rng.IntN(len(syls))]
			for _, k := range layout.Standard.Keys(y) {
				do("syl", proto.Request{Type: proto.Key, Key: string(k)})
			}
		case n < 70: // 開候選窗＋分頁
			snap := do("cand", proto.Request{Type: proto.Candidates, Length: rng.IntN(5)})
			lastCands = snap.Candidates
			if lastCands != nil && lastCands.Pages > 1 {
				do("page", proto.Request{Type: proto.Page, Index: rng.IntN(lastCands.Pages)})
			}
		case n < 78: // 選字後多半立刻送出（解鎖，讓後續段可重排）
			if lastCands != nil && len(lastCands.Items) > 0 {
				it := lastCands.Items[rng.IntN(len(lastCands.Items))]
				do("select", proto.Request{Type: proto.Select, Index: it.ID})
				lastCands = nil
				if rng.IntN(3) > 0 {
					do("commit", proto.Request{Type: proto.Commit})
				}
			} else {
				do("cancel", proto.Request{Type: proto.Cancel})
			}
		case n < 83:
			do("cursor", proto.Request{Type: proto.Cursor, To: rng.IntN(6)})
		case n < 89:
			do("backspace", proto.Request{Type: proto.Backspace})
		case n < 94: // 送出/接受/放棄
			switch rng.IntN(4) {
			case 0:
				do("commit", proto.Request{Type: proto.Commit})
			case 1:
				do("accept", proto.Request{Type: proto.Accept})
			case 2:
				do("cancel", proto.Request{Type: proto.Cancel})
			default:
				do("close", proto.Request{Type: proto.Close})
			}
			lastCands = nil
		case n < 97: // 模式/學習開關/前文/記憶
			switch rng.IntN(5) {
			case 0:
				do("pause", proto.Request{Type: proto.Pause, On: rng.IntN(2) == 0})
			case 1:
				do("mode", proto.Request{Type: proto.Mode, On: rng.IntN(2) == 0})
			case 2:
				do("ctx", proto.Request{Type: proto.Context, ContextEnabled: rng.IntN(2) == 0})
			case 3:
				do("forget", proto.Request{Type: proto.Forget, Word: "什麼"})
			default:
				do("singles", proto.Request{Type: proto.Symbol, Symbol: string(syls[rng.IntN(len(syls))])})
			}
		case n < 99:
			do("nav", proto.Request{Type: proto.Key, Key: []string{
				proto.KeyLeft, proto.KeyRight, proto.KeyUp, proto.KeyDown, proto.KeyHome, proto.KeyEnd,
				proto.KeyTab, proto.KeyShiftLeft, proto.KeyShiftRight, proto.KeyBackspace, proto.KeyDelete,
				proto.KeyEscape, proto.KeyEnter, proto.KeyShiftSpace, proto.KeyBackTab, proto.KeyPageUp, proto.KeyPageDown,
			}[rng.IntN(17)]})
		default:
			do("toggle", proto.Request{Type: proto.Toggle, Restore: rng.IntN(2) == 0})
		}
	}
	fmt.Fprintf(w, `{"seed":%d,"op":"end","score_calls":%d}`+"\n", seed, sc.calls)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// 沿用上次的補全建議必須等於重算的結果。
func TestRerankSuggestionFresh(t *testing.T) {
	for seed := range 60 {
		runRerankSeed(t, seed, io.Discard, func(s *Session) {
			if s.english || s.mixed.raw != "" || s.cursor != len(s.syls) || !s.comp.Empty() || s.cand != nil || s.autoFuzzed() {
				return
			}
			if want := s.suggestAfterLiteral(); !reflect.DeepEqual(s.sug, want) {
				t.Fatalf("seed %d: 補全建議 %+v，重算為 %+v", seed, s.sug, want)
			}
		})
	}
}
