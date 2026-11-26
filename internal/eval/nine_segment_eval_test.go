package eval_test

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"github.com/0c-biggo/0c-ime/internal/datadir"
)

// TestNineSegment 量注音九宮格「自動切字」（core/session/nine_segment.go）的整句命中率與每個請求的處理時間：
// 五種分組 × 語料 × 三種打法，都打開自動切字。
//
//   - tone：每個音節後都按聲調鍵（聲調未指定）。
//   - run：整串不按聲調鍵，切法全由核心決定。
//   - half：隨機一半的音節後按聲調鍵（固定亂數種子），其餘不按。
//
// 引擎照手機的 bridge：聲調選填、簡拼與混用都開、不學習（暫停）、不接神經。命中是打完後組字區的文字等於參考句；
// 時間是 Engine.Do 一次（session.Handle，含重新切字與解碼）的牆鐘時間，不含外殼每鍵另外要的候選清單。
//
//	ZERO_C_PACK=<資料包目錄> ZERO_C_NINE_CASES=<文字\t讀音 的 TSV，逗號分隔多個> \
//	  go test ./internal/eval -run TestNineSegment -count=1 -v
//
// 兩個環境變數都要設，沒設時跳過。ZERO_C_NINE_LAYOUTS、ZERO_C_NINE_ARMS 只跑其中幾種（逗號分隔）；ZERO_C_NINE_MISSES=<目錄> 寫出錯的句子，
// ZERO_C_NINE_TRACE=<目錄> 寫出每個請求之後的組字文字與花的微秒數（比較打字途中畫面的差異、找慢的請求）。
func TestNineSegment(t *testing.T) {
	pack := os.Getenv("ZERO_C_PACK")
	if pack == "" {
		t.Skip("set ZERO_C_PACK to measure nine-key segmentation")
	}
	files := strings.Split(os.Getenv("ZERO_C_NINE_CASES"), ",")
	if files[0] == "" {
		t.Skip("set ZERO_C_NINE_CASES to the cases to measure")
	}
	layouts := nineSegLayouts
	if only := os.Getenv("ZERO_C_NINE_LAYOUTS"); only != "" {
		layouts = slices.DeleteFunc(slices.Clone(layouts), func(l nineSegLayout) bool { return !strings.Contains(","+only+",", ","+l.id+",") })
	}
	arms := []string{"tone", "run", "half"}
	if only := os.Getenv("ZERO_C_NINE_ARMS"); only != "" {
		arms = strings.Split(only, ",")
	}
	e, err := datadir.Open(pack)
	if err != nil {
		t.Fatal(err)
	}
	// 照 APP mobile/session.go 的 newSession 與 settings.go 的 applySettings（toneless 開、混打關）。
	e.CandidateBeforeCursor = true
	e.EmojiShortcut = "off"
	e.PrefixCacheKeys = 512
	e.Toneless, e.Abbreviation, e.MixedWithToneless = true, true, true
	e.MixedInput = false
	e.NineSegment = true
	id := e.OpenSession()
	if _, err := e.Do(id, proto.Request{Type: proto.Pause, On: true}); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		cases := nineCases(t, file)
		t.Logf("%s：%d 句", file, len(cases))
		for _, l := range layouts {
			for _, arm := range arms {
				var times []time.Duration
				hits := 0
				var misses, trace []string
				rng := rand.New(rand.NewSource(1))
				for i, c := range cases {
					var snap proto.Snapshot
					for k, req := range l.requests(c.reading, arm, rng) {
						start := time.Now()
						snap, err = e.Do(id, req)
						took := time.Since(start)
						times = append(times, took)
						if err != nil {
							t.Fatal(err)
						}
						trace = append(trace, fmt.Sprintf("%d\t%d\t%s\t%d", i, k, snap.Text(), took.Microseconds()))
					}
					if snap.Text() == c.text {
						hits++
					} else {
						var cut []string
						for _, g := range snap.NineSegments {
							cut = append(cut, g.Reading)
						}
						misses = append(misses, c.text+"\t"+snap.Text()+"\t"+zhuyin.Key(c.reading)+"\t"+strings.Join(cut, " "))
					}
					if _, err := e.Do(id, proto.Request{Type: proto.Cancel}); err != nil {
						t.Fatal(err)
					}
				}
				slices.Sort(times)
				var total time.Duration
				for _, d := range times {
					total += d
				}
				ms := func(d time.Duration) string { return fmt.Sprintf("%.2f", float64(d.Microseconds())/1000) }
				t.Logf("RESULT\t%s\t%s\t%s\t%d/%d\t%.1f%%\treq %d\tmean %s\tp95 %s\tmax %s",
					filepath.Base(file), l.id, arm, hits, len(cases), 100*float64(hits)/float64(len(cases)), len(times),
					ms(total/time.Duration(len(times))), ms(times[len(times)*95/100]), ms(times[len(times)-1]))
				if dir := os.Getenv("ZERO_C_NINE_TRACE"); dir != "" {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatal(err)
					}
					name := filepath.Join(dir, fmt.Sprintf("%s-%s-%s.tsv", strings.TrimSuffix(filepath.Base(file), ".tsv"), l.id, arm))
					if err := os.WriteFile(name, []byte(strings.Join(trace, "\n")+"\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if dir := os.Getenv("ZERO_C_NINE_MISSES"); dir != "" {
					name := filepath.Join(dir, fmt.Sprintf("%s-%s-%s.tsv", strings.TrimSuffix(filepath.Base(file), ".tsv"), l.id, arm))
					if err := os.WriteFile(name, []byte(strings.Join(misses, "\n")+"\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
}

type nineSegLayout struct {
	id, name string
	groups   []string
}

// requests 是打法 arm 打出 reading 的請求：每個注音按它那一組的鍵，音節之後依打法按聲調鍵。
func (l nineSegLayout) requests(reading []zhuyin.Syllable, arm string, rng *rand.Rand) []proto.Request {
	var out []proto.Request
	for _, y := range reading {
		for _, r := range string(y.Base()) {
			for _, g := range l.groups {
				if strings.ContainsRune(g, r) {
					out = append(out, proto.Request{Type: proto.Symbol, Group: g})
					break
				}
			}
		}
		if arm == "tone" || arm == "half" && rng.Intn(2) == 0 {
			out = append(out, proto.Request{Type: proto.Symbol, Group: "ˊˇˋ˙"})
		}
	}
	return out
}

// nineSegLayouts 照 APP mobile/ninekey.go 的 nineLayouts（只取注音鍵）。
var nineSegLayouts = []nineSegLayout{
	{"samsung", "三星式", []string{"ㄅㄉㄚ", "ㄍㄐㄞㄧ", "ㄓㄗㄢㄦ", "ㄆㄊㄛ", "ㄎㄑㄟㄨ", "ㄔㄘㄣ", "ㄇㄋㄜㄝ", "ㄏㄒㄠㄩ", "ㄕㄙㄤㄥ", "ㄈㄌㄡㄖ"}},
	{"samsung_alt", "三星式改良", []string{"ㄅㄉㄚ", "ㄍㄐㄞㄛ", "ㄓㄗㄢㄦ", "ㄆㄊㄧ", "ㄎㄑㄟㄨ", "ㄔㄘㄣ", "ㄇㄋㄜㄝ", "ㄏㄒㄠㄩ", "ㄕㄙㄤㄥ", "ㄈㄌㄡㄖ"}},
	{"std11", "11 組", []string{"ㄅㄉㄚ", "ㄍㄐㄞ", "ㄓㄗㄢㄦ", "ㄆㄊㄛ", "ㄎㄑㄟ", "ㄔㄘㄣㄧ", "ㄇㄋㄜ", "ㄏㄒㄠㄡ", "ㄕㄙㄤㄨ", "ㄈㄌㄝ", "ㄖㄥㄩ"}},
	{"std11_alt", "11 組改良・輕度", []string{"ㄅㄉㄚ", "ㄍㄐㄞ", "ㄓㄗㄢㄦ", "ㄆㄊㄛ", "ㄎㄑㄠ", "ㄔㄘㄣㄧ", "ㄇㄋㄜ", "ㄏㄒㄟㄡ", "ㄕㄙㄤㄨ", "ㄈㄌㄝ", "ㄖㄥㄩ"}},
	{"std11_alt2", "11 組改良・中度", []string{"ㄅㄉㄚ", "ㄍㄐㄞ", "ㄓㄗㄢㄦ", "ㄨㄊㄛ", "ㄎㄑㄠ", "ㄔㄘㄣㄧ", "ㄇㄋㄜ", "ㄏㄒㄟㄡ", "ㄕㄙㄤㄆ", "ㄈㄌㄝ", "ㄖㄥㄩ"}},
}
