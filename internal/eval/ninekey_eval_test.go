package eval_test

import (
	"bufio"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"github.com/0c-biggo/0c-ime/internal/datadir"
)

// TestNineKeyAccuracy 量注音九宮格（core/session/ninekey.go）的整句首選命中率與每個請求的處理時間，
// 跟一般注音鍵盤比：
//
//   - nine：三星分組的九宮格，每個音節都按聲調鍵（聲調未指定）。
//   - toneless：一般注音鍵，不打聲調，但每個音節後按 '（只切音節），音節邊界跟九宮格一樣是使用者給的。
//   - toned：一般注音鍵，打聲調（一聲按空白的 ˉ）。
//
// 只用統計引擎（不接神經）、不學習（每句 cancel），聲調選填開、簡拼關（toneless 的單一注音不當簡拼）。
// 命中是整句打完後組字區的文字（就是候選列的整句）等於參考句。時間是 session.Handle 一次的牆鐘時間，
// 不含手機外殼每鍵另外要的候選清單。
//
//	ZERO_C_PACK=<資料包目錄> ZERO_C_NINE_CASES=<文字\t讀音 的 TSV，逗號分隔多個> \
//	  go test ./internal/eval -run TestNineKeyAccuracy -count=1 -v
//
// 兩個環境變數都要設，沒設時跳過。
func TestNineKeyAccuracy(t *testing.T) {
	pack := os.Getenv("ZERO_C_PACK")
	if pack == "" {
		t.Skip("set ZERO_C_PACK to measure the nine-key layout")
	}
	files := strings.Split(os.Getenv("ZERO_C_NINE_CASES"), ",")
	if files[0] == "" {
		t.Skip("set ZERO_C_NINE_CASES to the cases to measure")
	}
	e, err := datadir.Open(pack)
	if err != nil {
		t.Fatal(err)
	}
	e.Toneless, e.Abbreviation, e.PrefixCacheKeys = true, false, 512
	id := e.OpenSession()
	for _, file := range files {
		cases := nineCases(t, file)
		t.Logf("%s：%d 句", file, len(cases))
		for _, arm := range []string{"nine", "toneless", "toned"} {
			var times []time.Duration
			hits := 0
			for _, c := range cases {
				var snap proto.Snapshot
				for _, req := range nineRequests(arm, c.reading) {
					start := time.Now()
					snap, err = e.Do(id, req)
					times = append(times, time.Since(start))
					if err != nil {
						t.Fatal(err)
					}
				}
				if snap.Text() == c.text {
					hits++
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
			ms := func(d time.Duration) string { return fmt.Sprintf("%.2f ms", float64(d.Microseconds())/1000) }
			t.Logf("%-8s 整句 %d/%d（%.1f%%）  請求 %d 個，平均 %s、p50 %s、p95 %s、p99 %s、最慢 %s",
				arm, hits, len(cases), 100*float64(hits)/float64(len(cases)), len(times),
				ms(total/time.Duration(len(times))), ms(times[len(times)/2]), ms(times[len(times)*95/100]), ms(times[len(times)*99/100]), ms(times[len(times)-1]))
		}
	}
}

type nineCase struct {
	text    string
	reading []zhuyin.Syllable
}

// nineCases 讀「文字\t讀音[\t…]」的 TSV；讀音要每字一個合法音節，其他行（標題、含標點或英數的句子）略過。
func nineCases(t *testing.T, path string) []nineCase {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []nineCase
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		cols := strings.Split(sc.Text(), "\t")
		if len(cols) < 2 {
			continue
		}
		reading := zhuyin.ParseReading(cols[1])
		ok := len(reading) > 0 && len(reading) == len([]rune(cols[0]))
		for _, y := range reading {
			ok = ok && y.Legal()
		}
		if ok {
			out = append(out, nineCase{cols[0], reading})
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// 三星式注音九宮格的分組。
var nineLayout = []string{"ㄅㄉㄚ", "ㄍㄐㄞㄧ", "ㄓㄗㄢㄦ", "ㄆㄊㄛ", "ㄎㄑㄟㄨ", "ㄔㄘㄣ", "ㄇㄋㄜㄝ", "ㄏㄒㄠㄩ", "ㄕㄙㄤㄥ", "ㄈㄌㄡㄖ"}

func nineGroupOf(r rune) string {
	for _, g := range nineLayout {
		if strings.ContainsRune(g, r) {
			return g
		}
	}
	panic("not a zhuyin symbol: " + string(r))
}

// nineRequests 是打法 arm 打出 reading 的請求。
func nineRequests(arm string, reading []zhuyin.Syllable) []proto.Request {
	var out []proto.Request
	for _, y := range reading {
		base := string(y.Base())
		switch arm {
		case "nine":
			for _, r := range base {
				out = append(out, proto.Request{Type: proto.Symbol, Group: nineGroupOf(r)})
			}
			out = append(out, proto.Request{Type: proto.Symbol, Group: "ˊˇˋ˙"})
		case "toneless":
			for _, r := range base {
				out = append(out, proto.Request{Type: proto.Symbol, Symbol: string(r)})
			}
			out = append(out, proto.Request{Type: proto.Key, Key: "'"})
		case "toned":
			for _, r := range base {
				out = append(out, proto.Request{Type: proto.Symbol, Symbol: string(r)})
			}
			tone := strings.TrimPrefix(string(y), base)
			if tone == "" {
				tone = string(zhuyin.Tone1)
			}
			out = append(out, proto.Request{Type: proto.Symbol, Symbol: tone})
		}
	}
	return out
}
